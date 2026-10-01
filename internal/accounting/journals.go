package accounting

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Journal types.
const (
	JournalDayClose = "DAY_CLOSE"
	JournalManual   = "MANUAL"
	JournalReversal = "REVERSAL"
)

const (
	maxJournalLines = 200
	maxJournalPage  = 200
	maxBackfillDays = 400
)

// JournalLine is one line of a journal; exactly one of debit and credit is above zero.
type JournalLine struct {
	LineNo      int32           `json:"line_no"`
	AccountID   int64           `json:"account_id"`
	AccountCode string          `json:"account_code"`
	AccountName string          `json:"account_name"`
	Debit       decimal.Decimal `json:"debit"`
	Credit      decimal.Decimal `json:"credit"`
	Description string          `json:"description,omitempty"`
	SourceType  string          `json:"source_type,omitempty"`
	SourceRef   string          `json:"source_ref,omitempty"`
}

// Journal is a posted journal. Lines are filled when one journal is read.
type Journal struct {
	ID               int64           `json:"id"`
	Number           string          `json:"journal_number"`
	Type             string          `json:"journal_type"`
	Date             civil.Date      `json:"journal_date"`
	Description      string          `json:"description"`
	Reference        string          `json:"reference,omitempty"`
	ReversesID       *int64          `json:"reverses_journal_id"`
	ReversesNumber   string          `json:"reverses_number,omitempty"`
	ReversedByID     *int64          `json:"reversed_by_journal_id"`
	ReversedByNumber string          `json:"reversed_by_number,omitempty"`
	Reason           string          `json:"reason,omitempty"`
	PostedAt         time.Time       `json:"posted_at"`
	PostedBy         *int64          `json:"posted_by"`
	ApprovedBy       *int64          `json:"approved_by"`
	Total            decimal.Decimal `json:"total"`
	LineCount        int             `json:"line_count"`
	Lines            []JournalLine   `json:"lines,omitempty"`
}

// JournalFilter narrows the journal list.
type JournalFilter struct {
	From, To  *civil.Date
	Type      string
	AccountID *int64
	Q         string
	Limit     int
}

// LineInput is a line of a manual journal.
type LineInput struct {
	AccountID   int64           `json:"account_id"`
	Debit       decimal.Decimal `json:"debit"`
	Credit      decimal.Decimal `json:"credit"`
	Description string          `json:"description"`
}

// ManualInput is a manual journal.
type ManualInput struct {
	Date        civil.Date  `json:"journal_date"`
	Description string      `json:"description"`
	Reference   string      `json:"reference"`
	Lines       []LineInput `json:"lines"`
}

// ReverseInput reverses a manual journal.
type ReverseInput struct {
	Date     *civil.Date        `json:"journal_date"`
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

func errJournalNotFound() *apperr.Error {
	return apperr.NotFound("JOURNAL_NOT_FOUND", "the journal does not exist in this property")
}

func toJournal(r accountingdb.ListJournalsRow) Journal {
	return Journal{
		ID: r.ID, Number: r.JournalNumber, Type: r.JournalType, Date: r.JournalDate, Description: r.Description, Reference: deref(r.Reference),
		ReversesID: r.ReversesJournalID, ReversesNumber: deref(r.ReversesNumber), ReversedByID: r.ReversedByID, ReversedByNumber: deref(r.ReversedByNumber),
		Reason: deref(r.Reason), PostedAt: r.PostedAt, PostedBy: r.PostedBy, ApprovedBy: r.ApprovedBy, Total: r.Total, LineCount: int(r.LineCount),
	}
}

// Journals lists journals, newest first (accounting.view).
func (s *Service) Journals(ctx context.Context, propertyID int64, f JournalFilter) ([]Journal, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return nil, err
	}
	limit := f.Limit
	if limit < 1 || limit > maxJournalPage {
		limit = maxJournalPage
	}
	rows, err := s.q(ctx).ListJournals(ctx, accountingdb.ListJournalsParams{
		TenantID: p.TenantID, PropertyID: propertyID, FromDate: f.From, ToDate: f.To, JournalType: nullable(f.Type), AccountID: f.AccountID,
		Q: nullable(strings.TrimSpace(f.Q)), RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Journal, 0, len(rows))
	for _, r := range rows {
		out = append(out, toJournal(r))
	}
	return out, nil
}

// GetJournal is one journal with its lines (accounting.view).
func (s *Service) GetJournal(ctx context.Context, propertyID, id int64) (Journal, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingView)
	if err != nil {
		return Journal{}, err
	}
	return s.loadJournal(ctx, p.TenantID, propertyID, id)
}

func (s *Service) loadJournal(ctx context.Context, tenantID, propertyID, id int64) (Journal, error) {
	q := s.q(ctx)
	rows, err := q.ListJournals(ctx, accountingdb.ListJournalsParams{TenantID: tenantID, PropertyID: propertyID, ID: &id, RowLimit: 1})
	if err != nil {
		return Journal{}, err
	}
	if len(rows) == 0 {
		return Journal{}, errJournalNotFound()
	}
	j := toJournal(rows[0])
	lines, err := q.ListJournalLines(ctx, accountingdb.ListJournalLinesParams{TenantID: tenantID, PropertyID: propertyID, JournalID: id})
	if err != nil {
		return Journal{}, err
	}
	j.Lines = make([]JournalLine, 0, len(lines))
	for _, l := range lines {
		j.Lines = append(j.Lines, JournalLine{
			LineNo: l.LineNo, AccountID: l.AccountID, AccountCode: l.AccountCode, AccountName: l.AccountName, Debit: l.Debit, Credit: l.Credit,
			Description: deref(l.Description), SourceType: deref(l.SourceType), SourceRef: deref(l.SourceRef),
		})
	}
	return j, nil
}

// ---------------------------------------------------------------------------------------------------------------
// Day close

// methodKey is the system account a payment method is received into.
func methodKey(method string) string {
	if method == "OTHER" {
		return "OTHER_PAYMENT"
	}
	return method // CASH, CARD, BANK_TRANSFER and CITY_LEDGER are keys of their own
}

var sourceLabels = map[string]string{
	"CHARGE_CODE": "Charges", "TAX": "Tax", "SERVICE_CHARGE": "Service charge", "PAYMENT": "Payments",
	"RECEIPT": "City ledger receipts", "DEPOSIT_RELEASE": "Deposit released at checkout, folio",
}

type target struct {
	id         int64
	code, name string
}

// resolver turns what the folio ledger says into accounts of the chart. What cannot be placed (no account code, an
// unknown, inactive, header or wrong-typed account) goes to the fallback of its kind, so a day close never fails
// because of configuration; GET .../unmapped lists those items.
type resolver struct {
	byCode map[string]accountingdb.ListAccountsRow
	byKey  map[string]MapEntry
}

func (s *Service) newResolver(ctx context.Context, tenantID, propertyID int64) (resolver, error) {
	accs, err := s.q(ctx).ListAccounts(ctx, accountingdb.ListAccountsParams{TenantID: tenantID, PropertyID: propertyID, RowLimit: maxAccounts + 1})
	if err != nil {
		return resolver{}, err
	}
	entries, err := s.accountMap(ctx, tenantID, propertyID)
	if err != nil {
		return resolver{}, err
	}
	r := resolver{byCode: map[string]accountingdb.ListAccountsRow{}, byKey: map[string]MapEntry{}}
	for _, a := range accs {
		r.byCode[a.Code] = a
	}
	for _, e := range entries {
		r.byKey[e.Key] = e
	}
	return r, nil
}

func (r resolver) system(key string) (target, error) {
	e, ok := r.byKey[key]
	if !ok {
		return target{}, apperr.Conflict("ACCOUNT_MAP_INCOMPLETE", "the system account "+key+" is not mapped")
	}
	return target{e.AccountID, e.AccountCode, e.AccountName}, nil
}

func (r resolver) resolve(role, key string) (target, error) {
	switch role {
	case "GUEST_LEDGER", "ADVANCE_DEPOSITS", "CITY_LEDGER":
		return r.system(role)
	case "METHOD":
		return r.system(methodKey(key))
	}
	kind, fallback := "CHARGE_CODE", "SUSPENSE"
	switch role {
	case "TAX":
		kind, fallback = "TAX", "TAX_PAYABLE"
	case "SERVICE":
		kind, fallback = "SERVICE_CHARGE", "SERVICE_PAYABLE"
	}
	if a, ok := r.byCode[key]; ok && a.IsActive && a.IsPostable && typeFits(kind, a.AccountType) {
		return target{a.ID, a.Code, a.Name}, nil
	}
	return r.system(fallback)
}

type lineKey struct {
	account         int64
	sourceType, ref string
}

// PostDay writes the journal of a business date from the folio ledger; the night audit calls it inside its transaction
// before the day is closed. A property without accounting set up, or a date before its start date, is skipped, and a
// date that has its journal already is left alone.
func (s *Service) PostDay(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	cfg, err := s.q(ctx).GetSettings(ctx, accountingdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if bd.Before(cfg.StartDate) {
		return nil
	}
	if err := db.LockRows(ctx, db.Accounting, db.ForShare, propertyID, []int64{cfg.ID}); err != nil {
		return err
	}
	_, err = s.postDay(ctx, p, propertyID, bd)
	return err
}

// postDay does the work of PostDay with the settings row locked. It reports whether a journal was written.
func (s *Service) postDay(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) (bool, error) {
	q := s.q(ctx)
	if _, err := q.GetDayPost(ctx, accountingdb.GetDayPostParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd}); err == nil {
		return false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	rows, err := q.DayActivity(ctx, accountingdb.DayActivityParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd})
	if err != nil {
		return false, err
	}
	now := s.clock.Now()
	post := accountingdb.InsertDayPostParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd, PostedAt: now, ActorID: p.ActorID()}
	if len(rows) == 0 {
		return false, q.InsertDayPost(ctx, post)
	}
	res, err := s.newResolver(ctx, p.TenantID, propertyID)
	if err != nil {
		return false, err
	}
	sums := map[lineKey]decimal.Decimal{}
	names := map[int64]target{}
	for _, r := range rows {
		t, err := res.resolve(r.Role, r.Key)
		if err != nil {
			return false, err
		}
		names[t.id] = t
		k := lineKey{t.id, r.SourceType, r.SourceRef}
		sums[k] = sums[k].Add(r.Amount)
	}
	keys := make([]lineKey, 0, len(sums))
	total := decimal.Zero
	for k, v := range sums {
		if !v.IsZero() {
			keys = append(keys, k)
			total = total.Add(v)
		}
	}
	if !total.IsZero() {
		return false, apperr.Internal(fmt.Errorf("the journal of %s does not balance by %s", bd, total))
	}
	if len(keys) == 0 {
		return false, q.InsertDayPost(ctx, post)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if ca, cb := names[a.account].code, names[b.account].code; ca != cb {
			return ca < cb
		}
		if a.sourceType != b.sourceType {
			return a.sourceType < b.sourceType
		}
		return a.ref < b.ref
	})
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqJournal)
	if err != nil {
		return false, err
	}
	id, err := q.InsertJournal(ctx, accountingdb.InsertJournalParams{
		TenantID: p.TenantID, PropertyID: propertyID, JournalNumber: number, JournalType: JournalDayClose, JournalDate: bd,
		Description: "Day close " + bd.String(), PostedAt: now, ActorID: p.ActorID(),
	})
	if err != nil {
		return false, err
	}
	var debits int
	for i, k := range keys {
		v := sums[k]
		line := accountingdb.InsertJournalLineParams{
			TenantID: p.TenantID, PropertyID: propertyID, JournalID: id, LineNo: int32(i + 1), AccountID: k.account,
			Description: nullable(sourceLabels[k.sourceType] + " " + k.ref), SourceType: &k.sourceType, SourceRef: &k.ref,
		}
		if v.IsPositive() {
			line.Debit, line.Credit = v, decimal.Zero
			debits++
		} else {
			line.Debit, line.Credit = decimal.Zero, v.Neg()
		}
		if err := q.InsertJournalLine(ctx, line); err != nil {
			return false, err
		}
	}
	post.JournalID = &id
	if err := q.InsertDayPost(ctx, post); err != nil {
		return false, err
	}
	return true, s.audit.Write(ctx, entry(p, propertyID, bd, "accounting.day_posted", "gl_journal", id, nil,
		map[string]any{"journal_number": number, "lines": len(keys), "debit_lines": debits}))
}

// PostPending makes the journals of closed business days that have none (accounting.close): the days from before
// accounting was set up and any the night audit skipped. At most 400 days a call, oldest first.
func (s *Service) PostPending(ctx context.Context, propertyID int64) (int, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingClose)
	if err != nil {
		return 0, err
	}
	var posted int
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		posted = 0
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForUpdate)
		if err != nil {
			return err
		}
		days, err := s.q(ctx).PendingDays(ctx, accountingdb.PendingDaysParams{TenantID: p.TenantID, PropertyID: propertyID, StartDate: cfg.StartDate, RowLimit: maxBackfillDays})
		if err != nil {
			return err
		}
		for _, d := range days {
			if _, err := s.postDay(ctx, p, propertyID, d); err != nil {
				return err
			}
			posted++
		}
		return nil
	})
	return posted, err
}

// ---------------------------------------------------------------------------------------------------------------
// Manual journals and reversals

// periodStart is the first day of the month of a date.
func periodStart(d civil.Date) civil.Date { return civil.NewDate(d.Year(), d.Month(), 1) }

func periodEnd(start civil.Date) civil.Date {
	return civil.NewDate(start.Year(), start.Month()+1, 1).AddDays(-1)
}

// requireOpenPeriod refuses a journal date in a closed month.
func (s *Service) requireOpenPeriod(ctx context.Context, tenantID, propertyID int64, d civil.Date) error {
	row, err := s.q(ctx).GetPeriod(ctx, accountingdb.GetPeriodParams{TenantID: tenantID, PropertyID: propertyID, PeriodStart: periodStart(d)})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Status == "CLOSED" {
		return apperr.Conflict("PERIOD_CLOSED", "the accounting period of this date is closed").WithContext("period", periodStart(d).String())
	}
	return nil
}

// controlKeys are the accounts that only the day close writes, so that they always agree with the folios.
var controlKeys = []string{"GUEST_LEDGER", "CITY_LEDGER", "ADVANCE_DEPOSITS"}

// PostManual posts a manual journal (accounting.post). The Idempotency-Key makes a retry return the first journal.
func (s *Service) PostManual(ctx context.Context, propertyID int64, in ManualInput, key string) (Journal, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingPost)
	if err != nil {
		return Journal{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Journal{}, err
	}
	desc, ref := strings.TrimSpace(in.Description), strings.TrimSpace(in.Reference)
	var fields []apperr.FieldError
	switch {
	case desc == "":
		fields = append(fields, fieldErr("description", "REQUIRED", "describe the journal"))
	case len([]rune(desc)) > 300:
		fields = append(fields, fieldErr("description", "TOO_LONG", "at most 300 characters"))
	}
	if len([]rune(ref)) > 100 {
		fields = append(fields, fieldErr("reference", "TOO_LONG", "at most 100 characters"))
	}
	if in.Date.IsZero() {
		fields = append(fields, fieldErr("journal_date", "REQUIRED", "the date of the journal"))
	}
	if len(in.Lines) < 2 || len(in.Lines) > maxJournalLines {
		fields = append(fields, fieldErr("lines", "INVALID_COUNT", "between 2 and 200 lines"))
	}
	debit, credit := decimal.Zero, decimal.Zero
	for i, l := range in.Lines {
		at := func(f string) string { return fmt.Sprintf("lines[%d].%s", i, f) }
		switch {
		case l.Debit.IsNegative() || l.Credit.IsNegative():
			fields = append(fields, fieldErr(at("debit"), "NEGATIVE", "amounts are not negative"))
		case l.Debit.IsPositive() == l.Credit.IsPositive():
			fields = append(fields, fieldErr(at("debit"), "ONE_SIDE", "a line is either a debit or a credit"))
		case !l.Debit.Equal(l.Debit.Round(prop.CurrencyDecimals)) || !l.Credit.Equal(l.Credit.Round(prop.CurrencyDecimals)):
			fields = append(fields, fieldErr(at("debit"), "TOO_PRECISE", fmt.Sprintf("at most %d decimals", prop.CurrencyDecimals)))
		}
		if len([]rune(l.Description)) > 300 {
			fields = append(fields, fieldErr(at("description"), "TOO_LONG", "at most 300 characters"))
		}
		debit, credit = debit.Add(l.Debit), credit.Add(l.Credit)
	}
	if len(fields) == 0 && !debit.Equal(credit) {
		fields = append(fields, fieldErr("lines", "UNBALANCED", fmt.Sprintf("debits %s and credits %s differ", debit, credit)))
	}
	if len(fields) > 0 {
		return Journal{}, apperr.Invalid("the journal is invalid", fields...)
	}
	var id int64
	for attempt := 0; ; attempt++ { // a concurrent request with the same key can win the insert: the retry then replays it
		err = s.postManual(ctx, p, propertyID, in, desc, ref, key, &id)
		var ae *apperr.Error
		if attempt == 0 && key != "" && errors.As(err, &ae) && ae.Code == "DUPLICATE_REQUEST" {
			continue
		}
		break
	}
	if err != nil {
		return Journal{}, err
	}
	return s.loadJournal(ctx, p.TenantID, propertyID, id)
}

func (s *Service) postManual(ctx context.Context, p auth.Principal, propertyID int64, in ManualInput, desc, ref, key string, out *int64) error {
	var id int64
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForShare)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if key != "" {
			if prev, err := q.FindJournalByKey(ctx, accountingdb.FindJournalByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
				id = prev
				return nil
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		switch {
		case in.Date.After(day.BusinessDate):
			return apperr.Invalid("the journal is invalid", fieldErr("journal_date", "IN_THE_FUTURE", "not after the current business date "+day.BusinessDate.String()))
		case in.Date.Before(cfg.StartDate):
			return apperr.Invalid("the journal is invalid", fieldErr("journal_date", "BEFORE_START", "accounting starts on "+cfg.StartDate.String()))
		}
		if err := s.requireOpenPeriod(ctx, p.TenantID, propertyID, in.Date); err != nil {
			return err
		}
		res, err := s.newResolver(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		byID := map[int64]accountingdb.ListAccountsRow{}
		for _, a := range res.byCode {
			byID[a.ID] = a
		}
		control := map[int64]string{}
		for _, k := range controlKeys {
			if e, ok := res.byKey[k]; ok {
				control[e.AccountID] = k
			}
		}
		var lineErrs []apperr.FieldError
		for i, l := range in.Lines {
			at := fmt.Sprintf("lines[%d].account_id", i)
			a, ok := byID[l.AccountID]
			switch {
			case !ok:
				lineErrs = append(lineErrs, fieldErr(at, "NOT_FOUND", "no such account in this property"))
			case !a.IsPostable:
				lineErrs = append(lineErrs, fieldErr(at, "NOT_POSTABLE", "a header account takes no postings"))
			case !a.IsActive:
				lineErrs = append(lineErrs, fieldErr(at, "INACTIVE", "the account is inactive"))
			case control[a.ID] != "":
				lineErrs = append(lineErrs, fieldErr(at, "CONTROL_ACCOUNT", "only the day close posts to the "+strings.ToLower(strings.ReplaceAll(control[a.ID], "_", " "))+" account"))
			}
		}
		if len(lineErrs) > 0 {
			return apperr.Invalid("the journal is invalid", lineErrs...)
		}
		total := decimal.Zero
		for _, l := range in.Lines {
			total = total.Add(l.Debit)
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqJournal)
		if err != nil {
			return err
		}
		id, err = q.InsertJournal(ctx, accountingdb.InsertJournalParams{
			TenantID: p.TenantID, PropertyID: propertyID, JournalNumber: number, JournalType: JournalManual, JournalDate: in.Date, Description: desc,
			Reference: nullable(ref), IdempotencyKey: nullable(key), PostedAt: s.clock.Now(), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for i, l := range in.Lines {
			if err := q.InsertJournalLine(ctx, accountingdb.InsertJournalLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, JournalID: id, LineNo: int32(i + 1), AccountID: l.AccountID, Debit: l.Debit, Credit: l.Credit,
				Description: nullable(strings.TrimSpace(l.Description)),
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "accounting.journal_posted", "gl_journal", id, nil,
			map[string]any{"journal_number": number, "journal_date": in.Date, "lines": len(in.Lines), "total": total.String()}))
	})
	*out = id
	return err
}

// Reverse posts the mirror image of a manual journal (accounting.post plus an approval). The journals of the day close
// follow the folios and are corrected there, so they cannot be reversed here; a journal is reversed once.
func (s *Service) Reverse(ctx context.Context, propertyID, journalID int64, in ReverseInput) (Journal, error) {
	p, err := s.need(ctx, propertyID, auth.PermAccountingPost)
	if err != nil {
		return Journal{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return Journal{}, apperr.Invalid("the reversal is invalid", fieldErr("reason", "INVALID", "a reason of up to 500 characters is required"))
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return Journal{}, err
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForShare)
		if err != nil {
			return err
		}
		orig, err := s.loadJournal(ctx, p.TenantID, propertyID, journalID)
		if err != nil {
			return err
		}
		switch {
		case orig.Type != JournalManual:
			return apperr.Conflict("JOURNAL_NOT_REVERSIBLE", "only a manual journal can be reversed here: correct the folio for a day close journal")
		case orig.ReversedByID != nil:
			return apperr.Conflict("JOURNAL_ALREADY_REVERSED", "the journal has been reversed already").WithContext("reversed_by", orig.ReversedByNumber)
		}
		date := day.BusinessDate
		if in.Date != nil {
			date = *in.Date
		}
		switch {
		case date.After(day.BusinessDate):
			return apperr.Invalid("the reversal is invalid", fieldErr("journal_date", "IN_THE_FUTURE", "not after the current business date"))
		case date.Before(orig.Date):
			return apperr.Invalid("the reversal is invalid", fieldErr("journal_date", "BEFORE_JOURNAL", "not before the journal it reverses"))
		case date.Before(cfg.StartDate):
			return apperr.Invalid("the reversal is invalid", fieldErr("journal_date", "BEFORE_START", "accounting starts on "+cfg.StartDate.String()))
		}
		if err := s.requireOpenPeriod(ctx, p.TenantID, propertyID, date); err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqJournal)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		by := approval.UserID()
		id, err = q.InsertJournal(ctx, accountingdb.InsertJournalParams{
			TenantID: p.TenantID, PropertyID: propertyID, JournalNumber: number, JournalType: JournalReversal, JournalDate: date,
			Description: "Reversal of " + orig.Number + ": " + orig.Description, ReversesJournalID: &orig.ID, Reason: &reason, PostedAt: s.clock.Now(),
			ActorID: p.ActorID(), ApprovedBy: &by,
		})
		if err != nil {
			return err
		}
		for _, l := range orig.Lines {
			if err := q.InsertJournalLine(ctx, accountingdb.InsertJournalLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, JournalID: id, LineNo: l.LineNo, AccountID: l.AccountID, Debit: l.Credit, Credit: l.Debit,
				Description: nullable(l.Description),
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "accounting.journal_reversed", "gl_journal", id, nil,
			map[string]any{"journal_number": number, "reverses": orig.Number, "reason": reason, "approved_by": by}))
	})
	if err != nil {
		return Journal{}, err
	}
	return s.loadJournal(ctx, p.TenantID, propertyID, id)
}
