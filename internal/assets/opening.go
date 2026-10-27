package assets

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The opening balances (37-fixed-assets.md 4.4, F6, A5): the old assets of the hotel come into the register with the accumulated depreciation they had at a month end. In the mode
// WITH_JOURNAL one journal of type ASSET books Dr the cost accounts / Cr the accumulated accounts / Cr 3900 "Opening balance equity" for the carrying value. In the mode REGISTER_ONLY
// (the general ledger already holds the balances) no journal is posted and the sums by category must equal the balances of the accounts. All or nothing; a dry run does everything in
// the transaction and rolls it back, so what it reports is what the real run would do.

// OpeningInput is the request of an import: a file (CSV) or rows, the month end the accumulated depreciation is as at, and the mode.
type OpeningInput struct {
	AsOf           civil.Date   `json:"as_of"`
	JournalDate    *civil.Date  `json:"journal_date"`
	PostJournal    *bool        `json:"post_journal"` // default true: the mode WITH_JOURNAL
	DryRun         bool         `json:"dry_run"`
	CSV            string       `json:"csv"`
	Rows           []OpeningRow `json:"rows"`
	IdempotencyKey string       `json:"-"`
}

// Finding is a warning or an error of one row, or of the import as a whole (RowNo 0).
type Finding struct {
	RowNo   int    `json:"row_no"`
	Field   string `json:"field,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// Params are the figures the message is made of (a length, a month, an asset number), so that the screen words it in its own language.
	Params map[string]string `json:"params,omitempty"`
}

// params makes the Params of a finding from key, value pairs.
func params(kv ...string) map[string]string {
	m := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return m
}

// OpeningRowResult says what became of a row.
type OpeningRowResult struct {
	RowNo            int              `json:"row_no"`
	Name             string           `json:"name"`
	CategoryCode     string           `json:"category_code"`
	Cost             decimal.Decimal  `json:"cost"`
	Accum            decimal.Decimal  `json:"accum"`
	MonthsDone       int              `json:"months_done"`
	FiscalAccum      *decimal.Decimal `json:"fiscal_accum"`
	FullyDepreciated bool             `json:"fully_depreciated"`
	AssetID          *int64           `json:"asset_id"`
	AssetNumber      *string          `json:"asset_number"`
	Warnings         []Finding        `json:"warnings"`
	Errors           []Finding        `json:"errors"`
}

// OpeningCategoryTotal adds up the rows of a category.
type OpeningCategoryTotal struct {
	CategoryCode string          `json:"category_code"`
	CategoryName string          `json:"category_name"`
	Count        int             `json:"count"`
	Cost         decimal.Decimal `json:"cost"`
	Accum        decimal.Decimal `json:"accum"`
	Net          decimal.Decimal `json:"net"`
}

// OpeningJournalLine is a line of the journal the import posts (or would post).
type OpeningJournalLine struct {
	AccountCode string          `json:"account_code"`
	AccountName string          `json:"account_name"`
	Debit       decimal.Decimal `json:"debit"`
	Credit      decimal.Decimal `json:"credit"`
}

// OpeningGLCheck compares the register (what it holds and the file brings) with the balances of the general ledger of a category at the month end.
type OpeningGLCheck struct {
	CategoryCode     string          `json:"category_code"`
	CostAccountCode  string          `json:"cost_account_code"`
	RegisterCost     decimal.Decimal `json:"register_cost"`
	LedgerCost       decimal.Decimal `json:"ledger_cost"`
	CostDifference   decimal.Decimal `json:"cost_difference"`
	AccumAccountCode *string         `json:"accumulated_account_code"`
	RegisterAccum    decimal.Decimal `json:"register_accum"`
	LedgerAccum      decimal.Decimal `json:"ledger_accum"`
	AccumDifference  decimal.Decimal `json:"accum_difference"`
}

// OpeningResult is the answer of an import or of a dry run.
type OpeningResult struct {
	DryRun         bool                   `json:"dry_run"`
	Valid          bool                   `json:"valid"`
	Replayed       bool                   `json:"replayed"`
	Mode           string                 `json:"mode"`
	AsOf           civil.Date             `json:"as_of"`
	JournalDate    *civil.Date            `json:"journal_date"`
	RegisterFrom   *civil.Date            `json:"register_from"`
	ImportID       *int64                 `json:"import_id"`
	Number         *string                `json:"number"`
	JournalID      *int64                 `json:"journal_id"`
	JournalNumber  *string                `json:"journal_number"`
	Rows           []OpeningRowResult     `json:"rows"`
	Totals         []OpeningCategoryTotal `json:"totals"`
	TotalCost      decimal.Decimal        `json:"total_cost"`
	TotalAccum     decimal.Decimal        `json:"total_accum"`
	NetToEquity    decimal.Decimal        `json:"net_to_equity"`
	JournalPreview []OpeningJournalLine   `json:"journal_preview"`
	GLCheck        []OpeningGLCheck       `json:"gl_check"`
	Errors         []Finding              `json:"errors"`
	Warnings       []Finding              `json:"warnings"`
}

// OpeningImport is an import made, for the list.
type OpeningImport struct {
	ID            int64           `json:"id"`
	Number        string          `json:"number"`
	AsOf          civil.Date      `json:"as_of"`
	Mode          string          `json:"mode"`
	JournalNumber *string         `json:"journal_number"`
	AssetCount    int             `json:"asset_count"`
	TotalCost     decimal.Decimal `json:"total_cost"`
	TotalAccum    decimal.Decimal `json:"total_accum"`
}

var errDryRun = errors.New("dry run: rolled back")

// prepared is a row that passed the checks, with the values the asset is made of.
type prepared struct {
	no       int
	row      OpeningRow
	cat      assetsdb.ListCategoriesRow
	arg      assetsdb.LedgerInsertAssetParams
	accum    decimal.Decimal
	months   int
	fullDone bool
}

// ListOpeningImports lists the imports made, newest first (any asset permission).
func (s *Service) ListOpeningImports(ctx context.Context, propertyID int64) ([]OpeningImport, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListOpeningImports(ctx, assetsdb.ListOpeningImportsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]OpeningImport, 0, len(rows))
	for _, r := range rows {
		out = append(out, OpeningImport{ID: r.ID, Number: r.Number, AsOf: r.AsOf, Mode: r.Mode, JournalNumber: r.JournalNumber, AssetCount: int(r.AssetCount), TotalCost: r.TotalCost, TotalAccum: r.TotalAccum})
	}
	return out, nil
}

// ImportOpening brings old assets into the register (asset.setup). A real run needs an Idempotency-Key: the second request with the same key answers the first.
func (s *Service) ImportOpening(ctx context.Context, propertyID int64, in OpeningInput) (OpeningResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return OpeningResult{}, err
	}
	if !in.DryRun && trimmed(in.IdempotencyKey) == "" {
		return OpeningResult{}, apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	parseProblems := map[int][]apperr.FieldError{}
	if trimmed(in.CSV) != "" {
		if len(in.Rows) > 0 {
			return OpeningResult{}, invalid("opening balances", fieldErr("rows", "CONFLICT", "a file or rows, not both"))
		}
		in.Rows, parseProblems, err = parseOpeningCSV(in.CSV)
		if err != nil {
			return OpeningResult{}, err
		}
	}
	if len(in.Rows) == 0 || len(in.Rows) > maxOpeningRows {
		return OpeningResult{}, invalid("opening balances", fieldErr("rows", "OUT_OF_RANGE", fmt.Sprintf("1 to %d rows", maxOpeningRows)))
	}
	if !isMonthEnd(in.AsOf) {
		return OpeningResult{}, invalid("opening balances", fieldErr("as_of", "INVALID_VALUE", "the last day of a month"))
	}
	var res OpeningResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		var err error
		res, err = s.importOpening(ctx, p, propertyID, day, in, parseProblems)
		if err != nil {
			return err
		}
		if in.DryRun || len(res.Errors) > 0 {
			return errDryRun // nothing is kept: a dry run reports, and a refused import (below) has nothing to keep
		}
		return nil
	})
	switch {
	case errors.Is(err, errDryRun):
		if !in.DryRun {
			return res, rowsInvalid(res)
		}
		res.DryRun = true
		res.Number, res.ImportID, res.JournalID, res.JournalNumber = nil, nil, nil, nil
		for i := range res.Rows {
			res.Rows[i].AssetID, res.Rows[i].AssetNumber = nil, nil
		}
		return res, nil
	case err != nil:
		return OpeningResult{}, err
	}
	return res, nil
}

// rowsInvalid is the refusal of an import with errors: the errors of the rows as field errors.
func rowsInvalid(res OpeningResult) error {
	e := apperr.New(apperr.KindInvalid, "OPENING_ROW_INVALID", "the opening balances have errors: nothing was imported")
	for _, f := range res.Errors {
		field := f.Field
		if f.RowNo > 0 {
			field = fmt.Sprintf("rows[%d].%s", f.RowNo-1, f.Field)
		}
		e.Fields = append(e.Fields, apperr.FieldError{Field: field, Code: f.Code, Message: f.Message})
	}
	if len(res.GLCheck) > 0 {
		for _, f := range res.Errors {
			if f.Code == "OPENING_GL_MISMATCH" {
				return apperr.Conflict("OPENING_GL_MISMATCH", f.Message).WithContext("differences", res.GLCheck)
			}
		}
	}
	return e
}

func (s *Service) importOpening(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, in OpeningInput, parseProblems map[int][]apperr.FieldError) (OpeningResult, error) {
	q := s.q(ctx)
	mode := ModeWithJournal
	if in.PostJournal != nil && !*in.PostJournal {
		mode = ModeRegisterOnly
	}
	res := OpeningResult{DryRun: in.DryRun, Mode: mode, AsOf: in.AsOf, Rows: []OpeningRowResult{}, Totals: []OpeningCategoryTotal{}, JournalPreview: []OpeningJournalLine{}, GLCheck: []OpeningGLCheck{},
		Errors: []Finding{}, Warnings: []Finding{}}

	// the order of 6: the business day (write), the accounting settings (46), the settings of the register (47)
	po, err := s.acct.BeginPosting(ctx, propertyID)
	if err != nil {
		return res, err
	}
	cfg, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate)
	if err != nil {
		return res, err
	}
	if !in.DryRun {
		if prev, err := q.GetOpeningImportByKey(ctx, assetsdb.GetOpeningImportByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &in.IdempotencyKey}); err == nil {
			return s.replayOpening(ctx, p, propertyID, prev)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return res, err
		}
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return res, err
	}
	decimals := prop.CurrencyDecimals
	global := func(code, field, msg string, kv ...string) {
		res.Errors = append(res.Errors, Finding{Code: code, Field: field, Message: msg, Params: params(kv...)})
	}

	if in.AsOf.After(day) {
		global("INVALID_VALUE", "as_of", "the month end is after the business date")
	}
	registerFrom := AddMonths(MonthStart(in.AsOf), 1)
	res.RegisterFrom = &registerFrom
	if cfg.RegisterFrom != nil && !cfg.RegisterFrom.Equal(registerFrom) {
		global("AS_OF_MISMATCH", "as_of", fmt.Sprintf("the register starts in %s: the opening balances are as at the end of the month before it", cfg.RegisterFrom.String()[:7]), "month", cfg.RegisterFrom.String()[:7])
		res.RegisterFrom = cfg.RegisterFrom
	}
	journalDate := in.AsOf.AddDays(1)
	if journalDate.After(day) {
		journalDate = day
	}
	if in.JournalDate != nil {
		journalDate = *in.JournalDate
	}
	if mode == ModeWithJournal {
		res.JournalDate = &journalDate
		if journalDate.After(day) {
			global("INVALID_VALUE", "journal_date", "the journal date is after the business date")
		} else if err := po.CheckDate(ctx, journalDate, "journal_date"); err != nil {
			if ae, ok := apperr.As(err); ok {
				global(ae.Code, "journal_date", ae.Message)
				for _, fe := range ae.Fields {
					global(fe.Code, "journal_date", fe.Message)
				}
			} else {
				return res, err
			}
		}
	}

	cats, err := q.ListCategories(ctx, assetsdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return res, err
	}
	catByCode := map[string]assetsdb.ListCategoriesRow{}
	for _, c := range cats {
		catByCode[c.Code] = c
	}
	groups := map[string]FiscalRate{}
	if gs, err := q.ListFiscalGroups(ctx, assetsdb.ListFiscalGroupsParams{TenantID: p.TenantID, PropertyID: propertyID}); err != nil {
		return res, err
	} else {
		for _, g := range gs {
			r := FiscalRate{LifeYears: int(g.LifeYears), SLRate: g.SlRate}
			if g.DbRate != nil {
				r.DBRate = *g.DbRate
			}
			groups[g.Code] = r
		}
	}
	taxYear := 1
	if cfg.TaxYearStartMonth != nil {
		taxYear = int(*cfg.TaxYearStartMonth)
	} else if m, err := q.GetFiscalYearStartMonth(ctx, assetsdb.GetFiscalYearStartMonthParams{TenantID: p.TenantID, PropertyID: propertyID}); err == nil {
		taxYear = int(m)
	}

	var prepared []prepared
	seen := map[string]bool{}
	for i, row := range in.Rows {
		no := i + 1
		rr := OpeningRowResult{RowNo: no, Name: trimmed(row.Name), CategoryCode: normalCode(row.Category), Cost: row.Cost, Warnings: []Finding{}, Errors: []Finding{}}
		for _, fe := range parseProblems[no] {
			rr.Errors = append(rr.Errors, Finding{RowNo: no, Field: fe.Field, Code: fe.Code, Message: fe.Message})
		}
		pr, err := s.prepareRow(ctx, p, propertyID, day, in.AsOf, no, row, catByCode, groups, taxYear, decimals, cfg.StartRule, &rr)
		if err != nil {
			return res, err
		}
		if pr != nil && len(rr.Errors) == 0 {
			key := strings.ToLower(pr.arg.Name) + "|" + pr.cat.Code + "|" + pr.arg.Cost.String() + "|" + strings.ToLower(derefString(pr.arg.SerialNo))
			if seen[key] {
				rr.Warnings = append(rr.Warnings, Finding{RowNo: no, Code: "DUPLICATE_ROW", Message: "another row of the file has the same name, category, cost and serial number"})
			}
			seen[key] = true
			if dup, err := q.FindDuplicateAssets(ctx, assetsdb.FindDuplicateAssetsParams{TenantID: p.TenantID, PropertyID: propertyID, CategoryID: pr.cat.ID, Name: pr.arg.Name, Cost: pr.arg.Cost, SerialNo: pr.arg.SerialNo}); err != nil {
				return res, err
			} else if len(dup) > 0 {
				rr.Warnings = append(rr.Warnings, Finding{RowNo: no, Code: "DUPLICATE_ASSET", Message: "the register has an asset with the same name, category, cost and serial number: " + dup[0].Number, Params: params("number", dup[0].Number)})
			}
			rr.Accum, rr.MonthsDone, rr.FullyDepreciated = pr.accum, pr.months, pr.fullDone
			if pr.arg.FiscalAccumOpening != nil {
				rr.FiscalAccum = pr.arg.FiscalAccumOpening
			}
			prepared = append(prepared, *pr)
		}
		res.Rows = append(res.Rows, rr)
	}
	// totals by category and the journal
	byCat := map[string]*OpeningCategoryTotal{}
	var order []string
	byAccount := map[int64]*OpeningJournalLine{}
	var accountOrder []int64
	line := func(id int64, debit, credit decimal.Decimal) error {
		l := byAccount[id]
		if l == nil {
			code, name, _, ok, err := po.AccountInfo(ctx, id)
			if err != nil {
				return err
			}
			if !ok {
				return apperr.Internal(fmt.Errorf("account %d is not in the chart", id))
			}
			l = &OpeningJournalLine{AccountCode: code, AccountName: name}
			byAccount[id] = l
			accountOrder = append(accountOrder, id)
		}
		l.Debit, l.Credit = l.Debit.Add(debit), l.Credit.Add(credit)
		return nil
	}
	for _, pr := range prepared {
		t := byCat[pr.cat.Code]
		if t == nil {
			t = &OpeningCategoryTotal{CategoryCode: pr.cat.Code, CategoryName: pr.cat.Name}
			byCat[pr.cat.Code] = t
			order = append(order, pr.cat.Code)
		}
		t.Count++
		t.Cost, t.Accum = t.Cost.Add(pr.arg.Cost), t.Accum.Add(pr.accum)
		t.Net = t.Cost.Sub(t.Accum)
		res.TotalCost, res.TotalAccum = res.TotalCost.Add(pr.arg.Cost), res.TotalAccum.Add(pr.accum)
		if err := line(pr.cat.CostAccountID, pr.arg.Cost, decimal.Zero); err != nil {
			return res, err
		}
		if pr.accum.IsPositive() {
			if pr.cat.AccumulatedAccountID == nil {
				return res, apperr.Internal(errors.New("an asset with accumulated depreciation in a category without an accumulated account"))
			}
			if err := line(*pr.cat.AccumulatedAccountID, decimal.Zero, pr.accum); err != nil {
				return res, err
			}
		}
	}
	slices.Sort(order)
	for _, c := range order {
		res.Totals = append(res.Totals, *byCat[c])
	}
	res.NetToEquity = res.TotalCost.Sub(res.TotalAccum)
	var equity assetsdb.GetAccountByCodeRow
	if mode == ModeWithJournal && len(prepared) > 0 {
		equity, err = q.GetAccountByCode(ctx, assetsdb.GetAccountByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: equityAccountCode})
		if err != nil || equity.AccountType != "EQUITY" || !equity.IsActive || !equity.IsPostable {
			global("OPENING_ACCOUNT_MISSING", "", "the opening balances are booked against the account 3900 \"Opening balance equity\", which is missing or not usable in this property")
		} else if err := line(equity.ID, decimal.Zero, res.NetToEquity); err != nil {
			return res, err
		}
	}
	if mode == ModeWithJournal {
		for _, id := range accountOrder {
			res.JournalPreview = append(res.JournalPreview, *byAccount[id])
		}
	}
	if mode == ModeRegisterOnly && len(prepared) > 0 {
		if err := s.tieOutToLedger(ctx, p, propertyID, in.AsOf, order, byCat, catByCode, &res); err != nil {
			return res, err
		}
	}
	for _, rr := range res.Rows {
		res.Errors = append(res.Errors, rr.Errors...)
		res.Warnings = append(res.Warnings, rr.Warnings...)
	}
	res.Valid = len(res.Errors) == 0
	if !res.Valid {
		return res, nil
	}

	// the writes
	var journalID *int64
	if mode == ModeWithJournal {
		lines := make([]accounting.SystemLine, 0, len(accountOrder))
		po.AllowControl(accounting.KeyFixedAssets)
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFAOpening)
		if err != nil {
			return res, err
		}
		res.Number = &number
		for _, id := range accountOrder {
			l := byAccount[id]
			if l.Debit.IsZero() && l.Credit.IsZero() { // a fully depreciated lot has no carrying value to take to equity
				continue
			}
			lines = append(lines, accounting.SystemLine{AccountID: id, Debit: l.Debit, Credit: l.Credit, Description: "Opening balances of fixed assets " + number, SourceType: journalSourceAsset, SourceRef: number})
		}
		jid, jnum, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalAsset, Date: journalDate, Description: "Opening balances of fixed assets " + number, Reference: number, Lines: lines})
		if err != nil {
			return res, err
		}
		journalID, res.JournalID, res.JournalNumber = &jid, &jid, &jnum
	} else {
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFAOpening)
		if err != nil {
			return res, err
		}
		res.Number = &number
	}
	var key *string
	if !in.DryRun {
		key = &in.IdempotencyKey
	}
	imp, err := q.InsertOpeningImport(ctx, assetsdb.InsertOpeningImportParams{TenantID: p.TenantID, PropertyID: propertyID, Number: *res.Number, AsOf: in.AsOf, Mode: mode, JournalID: journalID,
		AssetCount: int32(len(prepared)), TotalCost: res.TotalCost, TotalAccum: res.TotalAccum, IdempotencyKey: key, ActorID: p.ActorID()}) //nolint:gosec // G115: at most 2000
	if err != nil {
		return res, err
	}
	res.ImportID = &imp.ID
	for i := range prepared {
		pr := &prepared[i]
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFixedAsset)
		if err != nil {
			return res, err
		}
		label, err := newLabelCode()
		if err != nil {
			return res, err
		}
		pr.arg.Number, pr.arg.LabelCode, pr.arg.OpeningImportID = number, label, &imp.ID
		id, err := s.openAsset(ctx, p, propertyID, pr.arg, in.AsOf, imp.ID, int32(pr.no), journalID) //nolint:gosec // G115: at most 2000
		if err != nil {
			return res, err
		}
		for k := range res.Rows {
			if res.Rows[k].RowNo == pr.no {
				res.Rows[k].AssetID, res.Rows[k].AssetNumber = &id, &number
			}
		}
	}
	if cfg.RegisterFrom == nil {
		if err := q.SetRegisterFrom(ctx, assetsdb.SetRegisterFromParams{TenantID: p.TenantID, PropertyID: propertyID, RegisterFrom: &registerFrom, ActorID: p.ActorID()}); err != nil {
			return res, err
		}
	}
	return res, s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.opening_imported", "fa_opening_import", imp.ID, *res.Number, nil,
		map[string]any{"number": *res.Number, "as_of": in.AsOf, "mode": mode, "assets": len(prepared), "total_cost": res.TotalCost, "total_accum": res.TotalAccum, "journal": res.JournalNumber}))
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// replayOpening answers a repeated request with the import it made the first time.
func (s *Service) replayOpening(ctx context.Context, p auth.Principal, propertyID int64, imp assetsdb.FaOpeningImport) (OpeningResult, error) {
	q := s.q(ctx)
	res := OpeningResult{Replayed: true, Valid: true, Mode: imp.Mode, AsOf: imp.AsOf, ImportID: &imp.ID, Number: &imp.Number, Rows: []OpeningRowResult{}, Totals: []OpeningCategoryTotal{},
		JournalPreview: []OpeningJournalLine{}, GLCheck: []OpeningGLCheck{}, Errors: []Finding{}, Warnings: []Finding{}, TotalCost: imp.TotalCost, TotalAccum: imp.TotalAccum,
		NetToEquity: imp.TotalCost.Sub(imp.TotalAccum), JournalID: imp.JournalID}
	assets, err := q.ListAssetsOfImport(ctx, assetsdb.ListAssetsOfImportParams{TenantID: p.TenantID, PropertyID: propertyID, ImportID: &imp.ID})
	if err != nil {
		return res, err
	}
	for i, a := range assets {
		id, number := a.ID, a.Number
		res.Rows = append(res.Rows, OpeningRowResult{RowNo: i + 1, Name: a.Name, Cost: a.Cost, Accum: a.AccumDepr, AssetID: &id, AssetNumber: &number, Warnings: []Finding{}, Errors: []Finding{}})
	}
	if imp.JournalID != nil {
		for _, l := range s.journalNumber(ctx, p, propertyID, *imp.JournalID) {
			res.JournalNumber = &l
		}
	}
	return res, nil
}

func (s *Service) journalNumber(ctx context.Context, p auth.Principal, propertyID, journalID int64) []string {
	rows, err := s.q(ctx).ListOpeningImports(ctx, assetsdb.ListOpeningImportsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil
	}
	for _, r := range rows {
		if r.JournalID != nil && *r.JournalID == journalID && r.JournalNumber != nil {
			return []string{*r.JournalNumber}
		}
	}
	return nil
}

// tieOutToLedger is the check of the mode REGISTER_ONLY (F6): for each category of the file, what the register holds plus what the file brings must equal the balance of the cost and
// accumulated accounts in the general ledger at the month end.
func (s *Service) tieOutToLedger(ctx context.Context, p auth.Principal, propertyID int64, asOf civil.Date, order []string, byCat map[string]*OpeningCategoryTotal, catByCode map[string]assetsdb.ListCategoriesRow, res *OpeningResult) error {
	q := s.q(ctx)
	var ids []int64
	for _, code := range order {
		c := catByCode[code]
		ids = append(ids, c.CostAccountID)
		if c.AccumulatedAccountID != nil {
			ids = append(ids, *c.AccumulatedAccountID)
		}
	}
	bal := map[int64]decimal.Decimal{}
	rows, err := q.AccountBalances(ctx, assetsdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountIds: ids, AsOf: asOf})
	if err != nil {
		return err
	}
	for _, r := range rows {
		bal[r.AccountID] = r.Balance
	}
	held, err := q.RegisterTotalsByCategory(ctx, assetsdb.RegisterTotalsByCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: asOf})
	if err != nil {
		return err
	}
	reg := map[int64]assetsdb.RegisterTotalsByCategoryRow{}
	for _, h := range held {
		reg[h.CategoryID] = h
	}
	mismatch := false
	for _, code := range order {
		c, t := catByCode[code], byCat[code]
		g := OpeningGLCheck{CategoryCode: code, CostAccountCode: c.CostAccountCode, AccumAccountCode: c.AccumulatedAccountCode,
			RegisterCost: reg[c.ID].Cost.Add(t.Cost), LedgerCost: bal[c.CostAccountID], RegisterAccum: reg[c.ID].Accum.Add(t.Accum)}
		if c.AccumulatedAccountID != nil {
			g.LedgerAccum = bal[*c.AccumulatedAccountID].Neg()
		}
		g.CostDifference, g.AccumDifference = g.LedgerCost.Sub(g.RegisterCost), g.LedgerAccum.Sub(g.RegisterAccum)
		if !g.CostDifference.IsZero() || !g.AccumDifference.IsZero() {
			mismatch = true
		}
		res.GLCheck = append(res.GLCheck, g)
	}
	if mismatch {
		res.Errors = append(res.Errors, Finding{Code: "OPENING_GL_MISMATCH", Message: "the sums of the file do not equal the balances of the accounts in the general ledger: see the differences by category"})
	}
	return nil
}

// prepareRow checks one row and builds the asset it makes. The problems and warnings go into rr; a nil answer means the row is not usable. Locations are made here when a row names a room, a
// banquet room or a warehouse (the import is all or nothing, so a refused import leaves none).
func (s *Service) prepareRow(ctx context.Context, p auth.Principal, propertyID int64, day, asOf civil.Date, no int, row OpeningRow, cats map[string]assetsdb.ListCategoriesRow, groups map[string]FiscalRate,
	taxYear int, decimals int32, defaultStart string, rr *OpeningRowResult) (*prepared, error) {
	q := s.q(ctx)
	bad := func(field, code, msg string, kv ...string) {
		rr.Errors = append(rr.Errors, Finding{RowNo: no, Field: field, Code: code, Message: msg, Params: params(kv...)})
	}
	warn := func(field, code, msg string, kv ...string) {
		rr.Warnings = append(rr.Warnings, Finding{RowNo: no, Field: field, Code: code, Message: msg, Params: params(kv...)})
	}
	name := trimmed(row.Name)
	if n := len([]rune(name)); n < 1 || n > maxName {
		bad("name", "INVALID_VALUE", fmt.Sprintf("1 to %d characters", maxName), "max", strconv.Itoa(maxName))
	}
	cat, ok := cats[normalCode(row.Category)]
	switch {
	case !ok:
		bad("category", "NOT_FOUND", "no such category in this property")
	case !cat.IsActive:
		bad("category", "INACTIVE", "the category is switched off")
	}
	if !ok || !cat.IsActive {
		return nil, nil
	}
	depreciable := cat.IsDepreciable != nil && *cat.IsDepreciable
	if row.InServiceDate == nil {
		bad("in_service_date", "REQUIRED", "the date the asset was put in service")
		return nil, nil
	}
	inService := *row.InServiceDate
	acq := inService
	if row.AcquisitionDate != nil {
		acq = *row.AcquisitionDate
	}
	switch {
	case acq.After(inService):
		bad("acquisition_date", "INVALID_VALUE", "the acquisition date is after the date in service")
	case inService.After(asOf):
		bad("in_service_date", "INVALID_VALUE", "the asset was put in service after the month end of the opening balances")
	}
	if !row.Cost.IsPositive() || !moneyOK(row.Cost, decimals) {
		bad("cost", "INVALID_VALUE", fmt.Sprintf("above zero, %d decimals at most", decimals), "decimals", strconv.Itoa(int(decimals)))
	}
	// residual, life, method, start
	residual := decimal.Zero
	if depreciable {
		residual = row.Cost.Mul(cat.ResidualPct).Div(decHundred).Round(decimals)
	}
	if row.ResidualValue != nil {
		residual = *row.ResidualValue
		if !moneyOK(residual, decimals) || residual.GreaterThan(row.Cost) {
			bad("residual_value", "INVALID_VALUE", "zero up to the cost, at the decimals of the currency")
		} else if !depreciable && residual.IsPositive() {
			bad("residual_value", "NOT_ALLOWED", "an asset that is not depreciated has no residual value")
		}
	}
	var life *int32
	var method *string
	var factor *decimal.Decimal
	startRule := defaultStart
	var firstDep *civil.Date
	months, accum := 0, decimal.Zero
	fullDone := false
	if depreciable {
		l := *cat.LifeMonths
		if row.LifeMonths != nil {
			if *row.LifeMonths < 1 || *row.LifeMonths > maxLifeMonths {
				bad("life_months", "OUT_OF_RANGE", "1 to 1200 months")
			} else {
				l = int32(*row.LifeMonths) //nolint:gosec // G115: checked
			}
		}
		life = &l
		m := *cat.Method
		if row.Method != "" {
			if row.Method != MethodSL && row.Method != MethodDB {
				bad("method", "INVALID_VALUE", "SL or DB")
			} else {
				m = row.Method
			}
		}
		method = &m
		if m == MethodDB {
			factor = cat.DbFactor
		}
		if row.StartRule != "" {
			if row.StartRule != StartInServiceMonth && row.StartRule != StartNextMonth {
				bad("start_rule", "INVALID_VALUE", "IN_SERVICE_MONTH or NEXT_MONTH")
			} else {
				startRule = row.StartRule
			}
		}
		fd := MonthStart(inService)
		if startRule == StartNextMonth {
			fd = AddMonths(fd, 1)
		}
		firstDep = &fd
		months = MonthsBetween(fd, MonthStart(asOf))
		if months > int(l) {
			months = int(l)
		}
		if row.MonthsDone != nil {
			if *row.MonthsDone < 0 || *row.MonthsDone > int(l) {
				bad("months_done", "OUT_OF_RANGE", "0 up to the life of the asset")
			} else {
				months = *row.MonthsDone
			}
		}
	} else {
		if row.LifeMonths != nil || row.Method != "" || row.MonthsDone != nil {
			bad("life_months", "NOT_ALLOWED", "this category is not depreciated: no life, method or months done")
		}
	}
	// the accumulated depreciation
	maxAccum := row.Cost.Sub(residual)
	switch {
	case row.AccumulatedDepreciation != nil:
		accum = *row.AccumulatedDepreciation
		if !moneyOK(accum, decimals) || accum.GreaterThan(maxAccum) {
			bad("accumulated_depreciation", "OUT_OF_RANGE", "zero up to the cost less the residual value, at the decimals of the currency")
		}
		if !depreciable && accum.IsPositive() {
			bad("accumulated_depreciation", "NOT_ALLOWED", "this category is not depreciated")
		}
	case depreciable:
		accum = SLAccumulated(row.Cost, residual, int(*life), months, decimals)
		if months > 0 {
			warn("accumulated_depreciation", "ACCUMULATED_ESTIMATED", "no accumulated depreciation was given: the straight line of the months done was used")
		}
	}
	if depreciable && len(rr.Errors) == 0 {
		switch {
		case months == 0 && accum.IsPositive():
			bad("months_done", "INVALID_VALUE", "accumulated depreciation with no months done")
		case months >= int(*life) && accum.LessThan(maxAccum):
			bad("accumulated_depreciation", "LIFE_OVER_WITH_VALUE", "the life is over and value is left above the residual: give the remaining months by a longer life, or a residual value")
		}
		fullDone = accum.Equal(maxAccum) && maxAccum.IsPositive() || (accum.Equal(maxAccum) && months > 0)
		if len(rr.Errors) == 0 && row.AccumulatedDepreciation != nil && !accum.Equal(SLAccumulated(row.Cost, residual, int(*life), months, decimals)) && !fullDone {
			warn("accumulated_depreciation", "ACCUMULATED_DIFFERS_FROM_SL", "the accumulated depreciation is not what the straight line gives for the months done")
		}
	}
	// unit count
	units := int32(1)
	if row.UnitCount != nil {
		switch {
		case *row.UnitCount < 1 || *row.UnitCount > 1_000_000:
			bad("unit_count", "OUT_OF_RANGE", "1 or more")
		case *row.UnitCount > 1 && cat.Kind != KindOE:
			bad("unit_count", "NOT_ALLOWED", "only a batch of operating equipment has more than one unit")
		default:
			units = int32(*row.UnitCount) //nolint:gosec // G115: checked
		}
	}
	// location, department, supplier
	var locationID, departmentID, supplierID *int64
	if loc := trimmed(row.Location); loc != "" {
		id, err := s.resolveLocation(ctx, p, propertyID, day, loc)
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) && ae.Kind == apperr.KindInvalid {
				bad("location", "NOT_FOUND", "no such location, room, banquet room or warehouse")
			} else {
				return nil, err
			}
		} else {
			locationID = &id
		}
	}
	if d := trimmed(row.Department); d != "" {
		r, err := q.GetDepartmentRefByCode(ctx, assetsdb.GetDepartmentRefByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: normalCode(d)})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			bad("department", "NOT_FOUND", "no such department in this property")
		} else {
			departmentID = &r.ID
		}
	} else if cat.DepartmentID != nil {
		departmentID = cat.DepartmentID
	}
	if sc := trimmed(row.Supplier); sc != "" {
		r, err := q.GetSupplierRefByCode(ctx, assetsdb.GetSupplierRefByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: sc})
		if err != nil {
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
			bad("supplier", "NOT_FOUND", "no such supplier in this property")
		} else {
			supplierID = &r.ID
		}
	}
	serial := optional(&row.SerialNo)
	if serial != nil && len([]rune(*serial)) > maxSerial {
		bad("serial_no", "INVALID_VALUE", fmt.Sprintf("%d characters at most", maxSerial), "max", strconv.Itoa(maxSerial))
	}
	// the fiscal book (A9, 4.9)
	group, fmethod := cat.FiscalGroup, cat.FiscalMethod
	if row.FiscalGroup != "" {
		if !fiscalGroups[row.FiscalGroup] {
			bad("fiscal_group", "INVALID_VALUE", "NONE, G1, G2, G3, G4, BLD_PERMANENT or BLD_NON_PERMANENT")
		} else {
			group = row.FiscalGroup
			fmethod = nil
		}
	}
	if row.FiscalMethod != "" {
		if row.FiscalMethod != MethodSL && row.FiscalMethod != MethodDB {
			bad("fiscal_method", "INVALID_VALUE", "SL or DB")
		} else {
			fmethod = &row.FiscalMethod
		}
	}
	var fiscalAccum *decimal.Decimal
	var fiscalMonths *int32
	var fiscalAsOf *civil.Date
	estimated := false
	if group == FiscalNone {
		fmethod = nil
		if row.FiscalAccumulatedDepreciation != nil || row.FiscalMonthsDone != nil {
			bad("fiscal_accumulated_depreciation", "NOT_ALLOWED", "the asset has no fiscal group: it is not depreciated for tax")
		}
	} else {
		if fmethod == nil {
			fmethod = ptr(MethodSL)
		}
		if *fmethod == MethodDB && isBuildingGroup(group) {
			bad("fiscal_method", "NOT_ALLOWED", "a building has the straight line only")
		}
		rate, known := groups[group]
		fa := asOf
		fiscalAsOf = &fa
		switch {
		case row.FiscalAccumulatedDepreciation != nil:
			v := *row.FiscalAccumulatedDepreciation
			if !moneyOK(v, decimals) || v.GreaterThan(row.Cost) {
				bad("fiscal_accumulated_depreciation", "OUT_OF_RANGE", "zero up to the cost, at the decimals of the currency")
			}
			fiscalAccum = &v
			fm := MonthsBetween(MonthStart(acq), MonthStart(asOf))
			if known && fm > rate.LifeYears*12 {
				fm = rate.LifeYears * 12
			}
			if row.FiscalMonthsDone != nil {
				if *row.FiscalMonthsDone < 0 {
					bad("fiscal_months_done", "OUT_OF_RANGE", "zero or more")
				}
				fm = *row.FiscalMonthsDone
			}
			fiscalMonths = ptr(int32(fm)) //nolint:gosec // G115: bounded by the life
		case known:
			v, fm := FiscalAccumulated(row.Cost, rate, *fmethod, acq, asOf, taxYear, decimals)
			fiscalAccum, fiscalMonths, estimated = &v, ptr(int32(fm)), true //nolint:gosec // G115: bounded by the life
			warn("fiscal_accumulated_depreciation", "FISCAL_ACCUMULATED_ESTIMATED", "the fiscal accumulation was computed from the group's rate: check it against last year's annual return (SPT)")
		default:
			bad("fiscal_group", "NOT_FOUND", "the property has no rates for this fiscal group")
		}
	}
	if len(rr.Errors) > 0 {
		return nil, nil
	}
	return &prepared{no: no, row: row, cat: cat, accum: accum, months: months, fullDone: fullDone, arg: assetsdb.LedgerInsertAssetParams{
		Name: name, CategoryID: cat.ID, LocationID: locationID, DepartmentID: departmentID, SupplierID: supplierID, Source: sourceOpening, AcquisitionDate: acq, InServiceDate: inService,
		StartRule: startRule, FirstDepMonth: firstDep, Cost: row.Cost, ResidualValue: residual, LifeMonths: life, Method: method, DbFactor: factor, FiscalGroup: group, FiscalMethod: fmethod,
		FiscalAccumOpening: fiscalAccum, FiscalMonthsDoneOpening: fiscalMonths, FiscalOpeningAsOf: fiscalAsOf, FiscalOpeningEstimated: estimated, UnitCount: units, SerialNo: serial,
		WarrantyUntil: row.WarrantyUntil, AccumDepr: accum, DepMonthsDone: int32(months), LastDepMonth: lastMonth(months, asOf)}}, nil //nolint:gosec // G115: bounded by the life
}

func lastMonth(months int, asOf civil.Date) *civil.Date {
	if months <= 0 {
		return nil
	}
	return ptr(MonthStart(asOf))
}

// resolveLocation finds the location of a file's cell: the code of a location, or ROOM:101, BQ:CODE or WH:CODE for a master that has no location yet (it is made).
func (s *Service) resolveLocation(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, cell string) (int64, error) {
	q := s.q(ctx)
	if prefix, ref, ok := strings.Cut(cell, ":"); ok {
		kind := map[string]string{"ROOM": "ROOM", "BQ": "BANQUET_ROOM", "WH": "WAREHOUSE"}[strings.ToUpper(prefix)]
		if kind != "" {
			return s.ensureLocation(ctx, p, propertyID, day, kind, 0, 0, trimmed(ref))
		}
	}
	loc, err := q.GetLocationByCode(ctx, assetsdb.GetLocationByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: cell})
	if err != nil {
		return 0, orNotFound(err, invalid("location", fieldErr("location", "NOT_FOUND", "no such location")))
	}
	if !loc.IsActive {
		return 0, invalid("location", fieldErr("location", "INACTIVE", "the location is switched off"))
	}
	return loc.ID, nil
}
