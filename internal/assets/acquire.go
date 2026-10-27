package assets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Acquisition from a journal line (docs/architecture/37-fixed-assets.md 4.2): the waiting list, "make asset", "not an asset", and the two guards that stop a bill or a journal from being undone
// under an asset (F8). No journal is posted here: the journal of the bill, the cash, the bank or the manual entry made the debit; the register mirrors it.

// Sources of a waiting line, as the list names them.
const (
	WaitingBill    = "BILL"
	WaitingCashier = "CASHIER"
	WaitingBank    = "BANK"
	WaitingManual  = "MANUAL"
	WaitingOther   = "OTHER"

	maxUnits = 200
)

var waitingSources = map[string]bool{WaitingBill: true, WaitingCashier: true, WaitingBank: true, WaitingManual: true, WaitingOther: true}

// WaitingFilter narrows the waiting list.
type WaitingFilter struct {
	Source     string
	CategoryID *int64
	DateFrom   *civil.Date
	DateTo     *civil.Date
	Q          string
	Dismissed  bool // the lines marked "not an asset" instead of the ones that wait
	Limit      int
	Offset     int
}

// WaitingLine is a journal line that debits the cost account of a category and is not an asset.
type WaitingLine struct {
	LineID          int64           `json:"line_id"`
	JournalID       int64           `json:"journal_id"`
	JournalNumber   string          `json:"journal_number"`
	JournalType     string          `json:"journal_type"`
	JournalDate     civil.Date      `json:"journal_date"`
	LineNo          int32           `json:"line_no"`
	Source          string          `json:"source"`
	DocumentNumber  string          `json:"document_number"` // the bill number for a bill, else the journal number
	AccountID       int64           `json:"account_id"`
	AccountCode     string          `json:"account_code"`
	AccountName     string          `json:"account_name"`
	Description     string          `json:"description"`
	Cost            decimal.Decimal `json:"cost"`
	CategoryID      int64           `json:"category_id"`
	CategoryCode    string          `json:"category_code"`
	CategoryName    string          `json:"category_name"`
	CategoryKind    string          `json:"category_kind"`
	DepartmentID    *int64          `json:"department_id"`
	BillID          *int64          `json:"bill_id"`
	BillNumber      *string         `json:"bill_number"`
	BillLineNo      *int32          `json:"bill_line_no"`
	InvoiceNumber   *string         `json:"invoice_number"`
	SupplierID      *int64          `json:"supplier_id"`
	SupplierCode    *string         `json:"supplier_code"`
	SupplierName    *string         `json:"supplier_name"`
	VATTreatment    *string         `json:"vat_treatment"`
	AgeDays         int             `json:"age_days"`
	BelowThreshold  bool            `json:"below_threshold"`
	Dismissed       bool            `json:"dismissed"`
	DismissedReason *string         `json:"dismissed_reason"`
	DismissedAt     *time.Time      `json:"dismissed_at"`
}

// WaitingCounts are the badge of the register: how many lines wait and how many were marked "not an asset".
type WaitingCounts struct {
	Waiting       int             `json:"waiting"`
	WaitingCost   decimal.Decimal `json:"waiting_cost"`
	Dismissed     int             `json:"dismissed"`
	DismissedCost decimal.Decimal `json:"dismissed_cost"`
}

// WaitingPage is a page of the list with the totals of the whole filter.
type WaitingPage struct {
	Data         []WaitingLine   `json:"data"`
	Total        int             `json:"total"`
	TotalCost    decimal.Decimal `json:"total_cost"`
	RegisterFrom *civil.Date     `json:"register_from"` // empty: the register has not started, so nothing waits
	Threshold    decimal.Decimal `json:"capitalization_threshold"`
	Counts       WaitingCounts   `json:"counts"`
}

// MakeAssetInput makes assets of one journal line.
type MakeAssetInput struct {
	JournalLineID  int64       `json:"journal_line_id"`
	Units          int         `json:"units"` // how many assets the line is divided into (default 1)
	Name           string      `json:"name"`
	CategoryID     *int64      `json:"category_id"`
	LocationID     *int64      `json:"location_id"`
	DepartmentID   *int64      `json:"department_id"`
	SupplierID     *int64      `json:"supplier_id"`
	InServiceDate  *civil.Date `json:"in_service_date"`
	StartRule      string      `json:"start_rule"`
	UnitCount      *int        `json:"unit_count"` // the sets of a batch of operating equipment (one asset)
	SerialNo       *string     `json:"serial_no"`
	WarrantyUntil  *civil.Date `json:"warranty_until"`
	Notes          *string     `json:"notes"`
	IdempotencyKey string      `json:"-"`
}

// MadeAsset is an asset the request made.
type MadeAsset struct {
	ID     int64           `json:"id"`
	Number string          `json:"number"`
	Name   string          `json:"name"`
	Cost   decimal.Decimal `json:"cost"`
}

// MakeAssetResult is the answer of "make asset": the assets, the warnings, and whether the answer is the first one repeated.
type MakeAssetResult struct {
	Assets   []MadeAsset `json:"assets"`
	Warnings []Finding   `json:"warnings"`
	Replayed bool        `json:"replayed"`
}

// DismissInput marks a line "not an asset".
type DismissInput struct {
	JournalLineID int64  `json:"journal_line_id"`
	Reason        string `json:"reason"`
}

// ListWaiting lists the journal lines that wait to become assets, or the ones marked "not an asset" (any asset permission).
func (s *Service) ListWaiting(ctx context.Context, propertyID int64, f WaitingFilter) (WaitingPage, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return WaitingPage{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return WaitingPage{}, err
	}
	if f.Limit < 1 || f.Limit > maxPageRow {
		f.Limit = 50
	}
	if f.Source != "" && !waitingSources[f.Source] {
		return WaitingPage{}, invalid("filter", fieldErr("source", "INVALID_VALUE", "BILL, CASHIER, BANK, MANUAL or OTHER"))
	}
	q := s.q(ctx)
	cfg, err := q.EnsureSettings(ctx, assetsdb.EnsureSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, ActorID: p.ActorID()})
	if err != nil {
		return WaitingPage{}, err
	}
	out := WaitingPage{Data: []WaitingLine{}, RegisterFrom: cfg.RegisterFrom, Threshold: cfg.CapitalizationThreshold}
	totals, err := q.WaitingTotals(ctx, assetsdb.WaitingTotalsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return WaitingPage{}, err
	}
	out.Counts = WaitingCounts{Waiting: int(totals.Waiting), WaitingCost: totals.WaitingCost, Dismissed: int(totals.Dismissed), DismissedCost: totals.DismissedCost}
	var source, text *string
	if f.Source != "" {
		source = &f.Source
	}
	if t := trimmed(f.Q); t != "" {
		text = &t
	}
	rows, err := q.ListWaitingLines(ctx, assetsdb.ListWaitingLinesParams{TenantID: p.TenantID, PropertyID: propertyID, Dismissed: f.Dismissed, Source: source, CategoryID: f.CategoryID,
		DateFrom: f.DateFrom, DateTo: f.DateTo, Q: text, RowLimit: int32(f.Limit), RowOffset: int32(f.Offset)}) //nolint:gosec // G115: bounded above
	if err != nil {
		return WaitingPage{}, err
	}
	for _, r := range rows {
		l := WaitingLine{LineID: r.LineID, JournalID: r.JournalID, JournalNumber: r.JournalNumber, JournalType: r.JournalType, JournalDate: r.JournalDate, LineNo: r.LineNo, Source: r.Source,
			DocumentNumber: r.JournalNumber, AccountID: r.AccountID, AccountCode: r.AccountCode, AccountName: r.AccountName, Description: derefString(r.Description), Cost: r.Cost,
			CategoryID: r.CategoryID, CategoryCode: r.CategoryCode, CategoryName: r.CategoryName, CategoryKind: r.CategoryKind, DepartmentID: r.DepartmentID, BillID: r.BillID,
			BillNumber: r.BillNumber, BillLineNo: r.SourceLine, InvoiceNumber: r.SupplierInvoiceNumber, SupplierID: r.SupplierID, SupplierCode: r.SupplierCode, SupplierName: r.SupplierName,
			VATTreatment: r.BillVatTreatment, AgeDays: r.JournalDate.DaysUntil(day.BusinessDate), BelowThreshold: r.Cost.LessThan(cfg.CapitalizationThreshold), Dismissed: r.Dismissed,
			DismissedReason: r.DismissedReason, DismissedAt: r.DismissedAt}
		if l.Description == "" {
			l.Description = r.JournalDescription
		}
		if r.BillNumber != nil {
			l.DocumentNumber = *r.BillNumber
		}
		if r.BillID == nil {
			l.BillLineNo = nil
		}
		out.Data = append(out.Data, l)
		out.Total, out.TotalCost = int(r.TotalRows), r.TotalCost
	}
	return out, nil
}

// MakeAssetFromLine makes one asset or several of one journal line (asset.manage, an Idempotency-Key). The debit of the line is the cost, divided over the units with the rounding difference on the
// last one (F7); no journal is posted (4.2.2).
func (s *Service) MakeAssetFromLine(ctx context.Context, propertyID int64, in MakeAssetInput) (MakeAssetResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return MakeAssetResult{}, err
	}
	key := trimmed(in.IdempotencyKey)
	if key == "" {
		return MakeAssetResult{}, apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	if len(key) > 64 {
		return MakeAssetResult{}, apperr.BadRequest("IDEMPOTENCY_KEY_INVALID", "the Idempotency-Key is at most 64 characters")
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return MakeAssetResult{}, err
	}
	in.Name, in.SerialNo, in.Notes = trimmed(in.Name), optional(in.SerialNo), optional(in.Notes)
	if in.Units == 0 {
		in.Units = 1
	}
	var f []apperr.FieldError
	switch {
	case in.JournalLineID <= 0:
		f = append(f, fieldErr("journal_line_id", "REQUIRED", "the journal line to make an asset of"))
	case in.Units < 1 || in.Units > maxUnits:
		f = append(f, fieldErr("units", "UNITS_INVALID", fmt.Sprintf("1 to %d units", maxUnits)))
	}
	if n := len([]rune(in.Name)); n > maxName {
		f = append(f, fieldErr("name", "INVALID_VALUE", fmt.Sprintf("%d characters at most", maxName)))
	}
	if in.SerialNo != nil && len([]rune(*in.SerialNo)) > maxSerial {
		f = append(f, fieldErr("serial_no", "INVALID_VALUE", fmt.Sprintf("%d characters at most", maxSerial)))
	}
	if in.Notes != nil && len([]rune(*in.Notes)) > maxNotes {
		f = append(f, fieldErr("notes", "INVALID_VALUE", fmt.Sprintf("%d characters at most", maxNotes)))
	}
	if in.StartRule != "" && in.StartRule != StartInServiceMonth && in.StartRule != StartNextMonth {
		f = append(f, fieldErr("start_rule", "INVALID_VALUE", "IN_SERVICE_MONTH or NEXT_MONTH"))
	}
	if in.UnitCount != nil && (*in.UnitCount < 1 || *in.UnitCount > 1_000_000 || (*in.UnitCount > 1 && in.Units > 1)) {
		f = append(f, fieldErr("unit_count", "UNITS_INVALID", "a batch is one asset of 1 or more sets"))
	}
	if len(f) > 0 {
		return MakeAssetResult{}, invalid("asset", f...)
	}
	var res MakeAssetResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		res = MakeAssetResult{Assets: []MadeAsset{}, Warnings: []Finding{}}
		if _, err := s.acct.BeginPosting(ctx, propertyID); err != nil { // the accounting settings (46) first: the chart is stable while the asset is made
			return err
		}
		q := s.q(ctx)
		line, err := q.GetJournalLineForAsset(ctx, assetsdb.GetJournalLineForAssetParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: in.JournalLineID})
		if err != nil {
			return orNotFound(err, notFound("JOURNAL_LINE_NOT_FOUND", "journal line"))
		}
		if line.BillSupplierID != nil { // a bill line: the supplier of the bill (47, share), so a void of the bill waits for us or sees our asset
			if err := db.LockRows(ctx, db.Suppliers, db.ForShare, propertyID, []int64{*line.BillSupplierID}); err != nil {
				return err
			}
		}
		cfg, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate) // serialises against the reversal guard and the run
		if err != nil {
			return err
		}
		// READ COMMITTED: a void or a reversal that committed while we waited for the locks is not in the row read above, so the line is read again now that nothing can change it under us
		line, err = q.GetJournalLineForAsset(ctx, assetsdb.GetJournalLineForAssetParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: in.JournalLineID})
		if err != nil {
			return orNotFound(err, notFound("JOURNAL_LINE_NOT_FOUND", "journal line"))
		}
		if links, err := q.ListLinksByKey(ctx, assetsdb.ListLinksByKeyParams{PropertyID: propertyID, IdempotencyKey: &key}); err != nil {
			return err
		} else if len(links) > 0 {
			return s.replayMake(ctx, p, propertyID, links, &res)
		}
		cat, err := s.lineCategory(ctx, p, propertyID, line, cfg)
		if err != nil {
			return err
		}
		if n, err := q.CountLiveLinksOfLine(ctx, assetsdb.CountLiveLinksOfLineParams{PropertyID: propertyID, LineID: line.LineID}); err != nil {
			return err
		} else if n > 0 {
			return apperr.Conflict("LINE_ALREADY_ASSET", "the journal line is an asset already").WithContext("journal", line.JournalNumber).WithContext("line", line.LineNo)
		}
		if _, err := q.GetLiveDismissal(ctx, assetsdb.GetLiveDismissalParams{PropertyID: propertyID, LineID: line.LineID}); err == nil {
			return apperr.Conflict("LINE_DISMISSED", "the line is marked as not an asset: restore it first").WithContext("journal", line.JournalNumber).WithContext("line", line.LineNo)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.CategoryID != nil && *in.CategoryID != cat.ID {
			return invalid("asset", fieldErr("category_id", "CATEGORY_MISMATCH", "the category of the line is the one of its account"))
		}
		return s.makeUnits(ctx, p, propertyID, day, prop.CurrencyDecimals, cfg, line, cat, in, key, &res)
	})
	return res, err
}

// lineCategory is the category a journal line belongs to, or the reason it cannot become an asset (409 LINE_NOT_ELIGIBLE, with the reason in the context).
func (s *Service) lineCategory(ctx context.Context, p auth.Principal, propertyID int64, line assetsdb.GetJournalLineForAssetRow, cfg assetsdb.FaSetting) (assetsdb.FaCategory, error) {
	no := func(reason string) error {
		return apperr.Conflict("LINE_NOT_ELIGIBLE", "the journal line cannot become an asset").WithContext("reason", reason).WithContext("journal", line.JournalNumber).WithContext("line", line.LineNo)
	}
	switch {
	case line.IsVoid:
		return assetsdb.FaCategory{}, no("JOURNAL_VOID")
	case line.JournalType == "ASSET" || derefString(line.SourceType) == journalSourceAsset:
		return assetsdb.FaCategory{}, no("WRITTEN_BY_REGISTER")
	case line.JournalType == "REVERSAL" || line.JournalType == "CLOSING":
		return assetsdb.FaCategory{}, no("JOURNAL_TYPE")
	case !line.Debit.IsPositive():
		return assetsdb.FaCategory{}, no("NOT_A_DEBIT")
	case cfg.RegisterFrom == nil:
		return assetsdb.FaCategory{}, no("REGISTER_NOT_STARTED")
	case line.JournalDate.Before(*cfg.RegisterFrom):
		return assetsdb.FaCategory{}, no("BEFORE_REGISTER_START")
	}
	cat, err := s.q(ctx).GetCategoryByCostAccount(ctx, assetsdb.GetCategoryByCostAccountParams{TenantID: p.TenantID, PropertyID: propertyID, AccountID: line.AccountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return cat, no("NOT_A_COST_ACCOUNT")
	}
	if err != nil {
		return cat, err
	}
	if cat.Kind == KindOE && cfg.OePolicy == OEExpense {
		return cat, no("OPERATING_EQUIPMENT_EXPENSED")
	}
	return cat, nil
}

func (s *Service) makeUnits(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, decimals int32, cfg assetsdb.FaSetting, line assetsdb.GetJournalLineForAssetRow,
	cat assetsdb.FaCategory, in MakeAssetInput, key string, res *MakeAssetResult) error {
	q := s.q(ctx)
	depreciable := cat.LifeMonths != nil && cat.Method != nil
	inService := line.JournalDate
	if in.InServiceDate != nil {
		inService = *in.InServiceDate
	}
	rule := cfg.StartRule
	if in.StartRule != "" {
		rule = in.StartRule
	}
	var firstDep *civil.Date
	if depreciable {
		fd := MonthStart(inService)
		if rule == StartNextMonth {
			fd = AddMonths(fd, 1)
		}
		firstDep = &fd
	}
	var f []apperr.FieldError
	if inService.After(day) {
		f = append(f, fieldErr("in_service_date", "IN_THE_FUTURE", "not after the open business date"))
	}
	if firstDep != nil && cfg.RegisterFrom != nil && firstDep.Before(MonthStart(*cfg.RegisterFrom)) {
		f = append(f, fieldErr("in_service_date", "BEFORE_REGISTER_START", "the depreciation cannot start before the register does"))
	}
	if in.UnitCount != nil && *in.UnitCount > 1 && cat.Kind != KindOE {
		f = append(f, fieldErr("unit_count", "NOT_ALLOWED", "only a batch of operating equipment has more than one set"))
	}
	var locationID *int64
	if in.LocationID != nil {
		loc, err := q.GetLocation(ctx, assetsdb.GetLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.LocationID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f = append(f, fieldErr("location_id", "NOT_FOUND", "no such location in this property"))
		case err != nil:
			return err
		case !loc.IsActive:
			f = append(f, fieldErr("location_id", "INACTIVE", "the location is switched off"))
		default:
			locationID = &loc.ID
		}
	}
	department := line.DepartmentID
	if in.DepartmentID != nil {
		if err := s.depts.Check(ctx, p.TenantID, propertyID, *in.DepartmentID, "department_id"); err != nil {
			return err
		}
		department = in.DepartmentID
	}
	if department == nil {
		department = cat.DepartmentID
	}
	supplier := line.BillSupplierID
	if supplier == nil {
		supplier = in.SupplierID
	}
	if len(f) > 0 {
		return invalid("asset", f...)
	}
	// the cost: the debit of the line divided over the units; the rounding difference goes to the last one (F7, 10.1)
	n := int32(in.Units) //nolint:gosec // G115: at most maxUnits
	each := line.Debit.Div(decimal.NewFromInt32(n)).RoundFloor(decimals)
	if !each.IsPositive() {
		return invalid("asset", fieldErr("units", "UNITS_INVALID", "the cost is too small to divide into so many units"))
	}
	name := in.Name
	if name == "" {
		name = derefString(line.Description)
	}
	if name == "" {
		name = cat.Name
	}
	var billID *int64
	var billLine *int32
	if line.BillID != nil && line.SourceLine != nil {
		billID, billLine = line.BillID, line.SourceLine
	}
	sum := decimal.Zero
	for k := int32(1); k <= n; k++ {
		cost := each
		if k == n {
			cost = line.Debit.Sub(sum)
		}
		sum = sum.Add(cost)
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFixedAsset)
		if err != nil {
			return err
		}
		label, err := newLabelCode()
		if err != nil {
			return err
		}
		unitName := name
		if n > 1 {
			unitName = fmt.Sprintf("%s (%d/%d)", truncateRunes(name, maxName-12), k, n)
		}
		arg := assetsdb.LedgerInsertAssetParams{Number: number, Name: truncateRunes(unitName, maxName), CategoryID: cat.ID, LocationID: locationID, DepartmentID: department, SupplierID: supplier,
			Source: sourceJournal, AcquisitionDate: line.JournalDate, InServiceDate: inService, StartRule: rule, FirstDepMonth: firstDep, Cost: cost, ResidualValue: decimal.Zero,
			FiscalGroup: cat.FiscalGroup, FiscalMethod: cat.FiscalMethod, UnitCount: 1, SerialNo: in.SerialNo, WarrantyUntil: in.WarrantyUntil, Notes: in.Notes, LabelCode: label}
		if depreciable {
			arg.LifeMonths, arg.Method = cat.LifeMonths, cat.Method
			if *cat.Method == MethodDB {
				arg.DbFactor = cat.DbFactor
			}
			arg.ResidualValue = cost.Mul(cat.ResidualPct).Div(decHundred).Round(decimals)
		}
		if in.UnitCount != nil {
			arg.UnitCount = int32(*in.UnitCount) //nolint:gosec // G115: checked above
		}
		id, err := s.acquireAsset(ctx, p, propertyID, arg, line.JournalDate, line.JournalID)
		if err != nil {
			return err
		}
		if err := q.InsertSourceLink(ctx, assetsdb.InsertSourceLinkParams{TenantID: p.TenantID, PropertyID: propertyID, JournalLineID: line.LineID, UnitNo: k, AssetID: id, Cost: cost,
			BillID: billID, BillLineNo: billLine, IdempotencyKey: &key, ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.made_from_line", "fa_asset", id, number, nil,
			map[string]any{"journal": line.JournalNumber, "journal_line": line.LineNo, "unit": k, "units": n, "cost": cost, "category": cat.Code})); err != nil {
			return err
		}
		res.Assets = append(res.Assets, MadeAsset{ID: id, Number: number, Name: arg.Name, Cost: cost})
		if cost.LessThan(cfg.CapitalizationThreshold) {
			res.Warnings = append(res.Warnings, Finding{Code: "BELOW_CAPITALIZATION_THRESHOLD", Message: "the cost is below the capitalization threshold of the property",
				Params: params("asset", number, "cost", cost.String(), "threshold", cfg.CapitalizationThreshold.String())})
		}
	}
	return nil
}

// replayMake answers a request repeated with the same key: the assets the first one made.
func (s *Service) replayMake(ctx context.Context, p auth.Principal, propertyID int64, links []assetsdb.FaSourceLink, res *MakeAssetResult) error {
	res.Replayed = true
	ids := make([]int64, len(links))
	for i, l := range links {
		ids[i] = l.AssetID
	}
	rows, err := s.q(ctx).ListAssetsByIDs(ctx, assetsdb.ListAssetsByIDsParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
	if err != nil {
		return err
	}
	byID := map[int64]assetsdb.ListAssetsByIDsRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	for _, l := range links {
		if a, ok := byID[l.AssetID]; ok {
			res.Assets = append(res.Assets, MadeAsset{ID: a.ID, Number: a.Number, Name: a.Name, Cost: l.Cost})
		}
	}
	return nil
}

// DismissLine marks a waiting line "not an asset" (asset.manage, a reason): a repair booked to the asset account by mistake, for instance. The books are corrected by a journal; the line stops waiting.
func (s *Service) DismissLine(ctx context.Context, propertyID int64, in DismissInput) error {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return err
	}
	in.Reason = trimmed(in.Reason)
	if n := len([]rune(in.Reason)); n < 1 || n > 300 {
		return invalid("dismissal", fieldErr("reason", "INVALID_VALUE", "a reason of 1 to 300 characters"))
	}
	return s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		cfg, err := s.lockSettings(ctx, p, propertyID, db.ForShare)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		line, err := q.GetJournalLineForAsset(ctx, assetsdb.GetJournalLineForAssetParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: in.JournalLineID})
		if err != nil {
			return orNotFound(err, notFound("JOURNAL_LINE_NOT_FOUND", "journal line"))
		}
		if _, err := s.lineCategory(ctx, p, propertyID, line, cfg); err != nil {
			return err
		}
		if n, err := q.CountLiveLinksOfLine(ctx, assetsdb.CountLiveLinksOfLineParams{PropertyID: propertyID, LineID: line.LineID}); err != nil {
			return err
		} else if n > 0 {
			return apperr.Conflict("LINE_ALREADY_ASSET", "the journal line is an asset already").WithContext("journal", line.JournalNumber).WithContext("line", line.LineNo)
		}
		if err := q.InsertDismissal(ctx, assetsdb.InsertDismissalParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: line.LineID, Reason: in.Reason, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.line_dismissed", "gl_journal_line", line.LineID, line.JournalNumber, nil,
			map[string]any{"journal": line.JournalNumber, "journal_line": line.LineNo, "cost": line.Debit, "reason": in.Reason}))
	})
}

// RestoreLine puts a line that was marked "not an asset" back in the waiting list (asset.manage). The dismissal is kept.
func (s *Service) RestoreLine(ctx context.Context, propertyID, lineID int64) error {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return err
	}
	return s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		n, err := s.q(ctx).RestoreDismissal(ctx, assetsdb.RestoreDismissalParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: lineID, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("DISMISSAL_NOT_FOUND", "dismissed line")
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.line_restored", "gl_journal_line", lineID, "", nil, map[string]any{"journal_line": lineID}))
	})
}

// CheckBillLine is the guard payables.AssetGuard asks inside the transaction of a void (lineNo 0) or of a credit note on a line: a line made an asset is 409 BILL_LINE_IS_ASSET, because the register
// mirrors it (F8). The caller holds the supplier of the bill (47), which "make asset" holds in share mode for a bill line, so the two serialise.
func (s *Service) CheckBillLine(ctx context.Context, tenantID, propertyID, billID int64, lineNo int32) error {
	rows, err := s.q(ctx).GuardBillAssets(ctx, assetsdb.GuardBillAssetsParams{TenantID: tenantID, PropertyID: propertyID, BillID: billID, LineNo: lineNo})
	if err != nil || len(rows) == 0 {
		return err
	}
	numbers := make([]string, len(rows))
	for i, r := range rows {
		numbers[i] = r.Number
	}
	return apperr.Conflict("BILL_LINE_IS_ASSET", "a line of the bill is an asset of the register: cancel the asset first").WithContext("assets", numbers)
}

// CheckReverse is the guard accounting.JournalGuard asks inside the transaction of a reversal: a journal with a line made an asset is 409 JOURNAL_LINE_IS_ASSET (F8). Only a journal that debits the
// cost account of a category can hold such a line, and only that one takes the settings of the register (47, share), which "make asset" holds for update.
func (s *Service) CheckReverse(ctx context.Context, tenantID, propertyID, journalID int64) error {
	q := s.q(ctx)
	debits, err := q.JournalDebitsCostAccount(ctx, assetsdb.JournalDebitsCostAccountParams{TenantID: tenantID, PropertyID: propertyID, JournalID: journalID})
	if err != nil || !debits {
		return err
	}
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
		return err
	}
	rows, err := q.GuardJournalAssets(ctx, assetsdb.GuardJournalAssetsParams{TenantID: tenantID, PropertyID: propertyID, JournalID: journalID})
	if err != nil || len(rows) == 0 {
		return err
	}
	numbers := make([]string, len(rows))
	for i, r := range rows {
		numbers[i] = r.Number
	}
	return apperr.Conflict("JOURNAL_LINE_IS_ASSET", "a line of the journal is an asset of the register: cancel the asset first").WithContext("assets", numbers)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
