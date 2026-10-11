package accounting

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting/accountingdb"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// SystemLine is a line of a journal that another module posts.
type SystemLine struct {
	AccountID   int64
	Debit       decimal.Decimal
	Credit      decimal.Decimal
	Description string
	SourceType  string
	SourceRef   string
	// DepartmentID is the department or sub-department of the line, when it has one (a bill line of an expense).
	DepartmentID *int64
}

// SystemJournal is a journal of the payables, posted by that module in its own transaction.
type SystemJournal struct {
	Type        string // JournalPayables (the default), JournalBank or JournalTax
	Date        civil.Date
	Description string
	Reference   string
	Lines       []SystemLine
}

// Poster posts journals for another module (the payables) inside that module's transaction. BeginPosting takes the
// property's accounting settings row first (lock level 46), so the module can then lock its own rows (a higher level)
// and post, in the global order.
type Poster struct {
	svc        *Service
	p          auth.Principal
	propertyID int64
	cfg        accountingdb.AccountingSetting
	today      civil.Date
	res        *resolver
	byID       map[int64]accountingdb.ListAccountsRow
	allowed    map[string]bool // control accounts this poster may post to
}

// BeginPosting locks the accounting settings of the property in share mode and returns the poster. The caller has the
// open business day (RequireOpenBusinessDay) already.
func (s *Service) BeginPosting(ctx context.Context, propertyID int64) (*Poster, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.lock(ctx, p.TenantID, propertyID, db.ForShare)
	if err != nil {
		return nil, err
	}
	today, err := s.businessDate(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	return &Poster{svc: s, p: p, propertyID: propertyID, cfg: cfg, today: today}, nil
}

func (po *Poster) load(ctx context.Context) error {
	if po.res != nil {
		return nil
	}
	res, err := po.svc.newResolver(ctx, po.p.TenantID, po.propertyID)
	if err != nil {
		return err
	}
	po.res = &res
	po.byID = map[int64]accountingdb.ListAccountsRow{}
	for _, a := range res.byCode {
		po.byID[a.ID] = a
	}
	return nil
}

// AllowControl lets this poster post to one control account that only the day close posts to otherwise (the city ledger, for the credit
// notes and the write-offs). Nothing wider: it names the key.
func (po *Poster) AllowControl(key string) {
	if po.allowed == nil {
		po.allowed = map[string]bool{}
	}
	po.allowed[key] = true
}

// AccountInfo is the code, the name and the type of an account of the property.
func (po *Poster) AccountInfo(ctx context.Context, id int64) (code, name, accountType string, ok bool, err error) {
	if err := po.load(ctx); err != nil {
		return "", "", "", false, err
	}
	a, found := po.byID[id]
	return a.Code, a.Name, a.AccountType, found, nil
}

// TaxPayableAccount is the account a tax is owed on: the account of the tax when it is an active liability account that takes postings,
// else the TAX_PAYABLE system account, which is where the day close put it.
func (po *Poster) TaxPayableAccount(ctx context.Context, code string) (int64, error) {
	if err := po.load(ctx); err != nil {
		return 0, err
	}
	if a, ok := po.res.byCode[code]; ok && code != "" && a.IsActive && a.IsPostable && a.AccountType == TypeLiability {
		return a.ID, nil
	}
	t, err := po.res.system(KeyTaxPayable)
	return t.id, err
}

// SystemAccount is the account of a system key (ACCOUNTS_PAYABLE, CASH, BANK_TRANSFER, ...).
func (po *Poster) SystemAccount(ctx context.Context, key string) (int64, error) {
	if err := po.load(ctx); err != nil {
		return 0, err
	}
	t, err := po.res.system(key)
	return t.id, err
}

// CheckDate refuses a date the books cannot take: before the accounting start, after the business date, or in a closed
// month. It returns the field error to attach to the caller's own field.
func (po *Poster) CheckDate(ctx context.Context, d civil.Date, field string) error {
	switch {
	case d.After(po.today):
		return apperr.Invalid("the date is invalid", fieldErr(field, "IN_THE_FUTURE", "not after the current business date "+po.today.String()))
	case d.Before(po.cfg.StartDate):
		return apperr.Invalid("the date is invalid", fieldErr(field, "BEFORE_START", "accounting starts on "+po.cfg.StartDate.String()))
	}
	return po.svc.requireOpenPeriod(ctx, po.p.TenantID, po.propertyID, d)
}

// CheckAccount says whether an account takes the lines of a bill: it exists, takes postings, is active, and is not one
// of the accounts that only the day close or the payables write.
func (po *Poster) CheckAccount(ctx context.Context, id int64, field string) error {
	if err := po.load(ctx); err != nil {
		return err
	}
	a, ok := po.byID[id]
	switch {
	case !ok:
		return apperr.Invalid("the account is invalid", fieldErr(field, "NOT_FOUND", "no such account in this property"))
	case !a.IsPostable:
		return apperr.Invalid("the account is invalid", fieldErr(field, "NOT_POSTABLE", "a header account takes no postings"))
	case !a.IsActive:
		return apperr.Invalid("the account is invalid", fieldErr(field, "INACTIVE", "the account is inactive"))
	}
	for _, k := range controlKeys {
		if k == KeyAccountsPayable || po.allowed[k] {
			continue // the payables post to it themselves, and a module may be allowed one more control account
		}
		if e, ok := po.res.byKey[k]; ok && e.AccountID == id {
			return apperr.Invalid("the account is invalid", fieldErr(field, "CONTROL_ACCOUNT", "only the day close posts to this account"))
		}
	}
	return nil
}

// CheckDepartment says whether a department takes a line of a bill: it exists in the property and is in use. A nil department is no department.
func (po *Poster) CheckDepartment(ctx context.Context, id *int64, field string) error {
	if id == nil || po.svc.depts == nil {
		return nil
	}
	return po.svc.depts.Check(ctx, po.p.TenantID, po.propertyID, *id, field)
}

// ResolveDepartment checks the department a module gives to a line of an account it chose and applies the rule of the account: the department named (it exists and is in use),
// else the default of the account, and none when the account takes none. A required department that is missing is refused (422 DEPARTMENT_REQUIRED on field). The module
// uses the result for its own record and for the SystemLine.
func (po *Poster) ResolveDepartment(ctx context.Context, accountID int64, id *int64, field string) (*int64, error) {
	if err := po.CheckDepartment(ctx, id, field); err != nil {
		return nil, err
	}
	if err := po.load(ctx); err != nil {
		return nil, err
	}
	a, ok := po.byID[accountID]
	if !ok {
		return id, nil // CheckAccount reports it
	}
	return lineDepartment(a, id, false, field, "")
}

// Post writes a balanced journal of type PAYABLES and returns its id and number. The caller has checked the date and
// the accounts of its own lines (CheckDate, CheckAccount), which Post checks again.
func (po *Poster) Post(ctx context.Context, in SystemJournal) (int64, string, error) {
	typ := in.Type
	if typ == "" {
		typ = JournalPayables
	}
	if typ != JournalPayables && typ != JournalBank && typ != JournalTax && typ != JournalReceivables && typ != JournalCashier {
		return 0, "", apperr.Internal(fmt.Errorf("a module cannot post a journal of type %s", typ))
	}
	if len(in.Lines) < 2 {
		return 0, "", apperr.Internal(fmt.Errorf("a journal has at least two lines"))
	}
	debit, credit := decimal.Zero, decimal.Zero
	for _, l := range in.Lines {
		debit, credit = debit.Add(l.Debit), credit.Add(l.Credit)
	}
	if !debit.Equal(credit) {
		return 0, "", apperr.Internal(fmt.Errorf("a payables journal does not balance: %s and %s", debit, credit))
	}
	if err := po.CheckDate(ctx, in.Date, "date"); err != nil {
		return 0, "", err
	}
	depts := make([]*int64, len(in.Lines))
	for i, l := range in.Lines {
		if err := po.CheckAccount(ctx, l.AccountID, fmt.Sprintf("lines[%d].account_id", i)); err != nil {
			return 0, "", err
		}
		// the final gate: whatever a module chose, a line takes the default of its account and a required department is never missing
		d, err := lineDepartment(po.byID[l.AccountID], l.DepartmentID, false, fmt.Sprintf("lines[%d].department_id", i), "")
		if err != nil {
			return 0, "", err
		}
		depts[i] = d
	}
	number, err := po.svc.days.NextDocumentNumber(ctx, po.propertyID, tenancy.SeqJournal)
	if err != nil {
		return 0, "", err
	}
	q := po.svc.q(ctx)
	id, err := q.InsertJournal(ctx, accountingdb.InsertJournalParams{
		TenantID: po.p.TenantID, PropertyID: po.propertyID, JournalNumber: number, JournalType: typ, JournalDate: in.Date,
		Description: in.Description, Reference: nullable(in.Reference), PostedAt: po.svc.clock.Now(), ActorID: po.p.ActorID(),
	})
	if err != nil {
		return 0, "", err
	}
	for i, l := range in.Lines {
		if err := q.InsertJournalLine(ctx, accountingdb.InsertJournalLineParams{
			TenantID: po.p.TenantID, PropertyID: po.propertyID, JournalID: id, LineNo: int32(i + 1), AccountID: l.AccountID, Debit: l.Debit, Credit: l.Credit,
			Description: nullable(l.Description), SourceType: nullable(l.SourceType), SourceRef: nullable(l.SourceRef), DepartmentID: depts[i],
		}); err != nil {
			return 0, "", err
		}
	}
	return id, number, po.svc.audit.Write(ctx, entry(po.p, po.propertyID, po.today, "accounting.module_journal_posted", "gl_journal", id, auditlabel.Journal(ctx, po.propertyID, id), nil,
		map[string]any{"journal_number": number, "type": typ, "journal_date": in.Date, "lines": len(in.Lines), "total": debit.String()}))
}

// Reverse posts the mirror image of a payables journal (a voided bill or payment) dated a given day, in an open month.
func (po *Poster) Reverse(ctx context.Context, journalID int64, date civil.Date, reason string, approvedBy int64) (int64, error) {
	orig, err := po.svc.loadJournal(ctx, po.p.TenantID, po.propertyID, journalID)
	if err != nil {
		return 0, err
	}
	switch {
	case orig.Type != JournalPayables && orig.Type != JournalTax && orig.Type != JournalReceivables:
		return 0, apperr.Conflict("JOURNAL_NOT_REVERSIBLE", "only the journal of a bill or a payment is reversed here")
	case orig.ReversedByID != nil:
		return 0, apperr.Conflict("JOURNAL_ALREADY_REVERSED", "the journal has been reversed already")
	case date.Before(orig.Date):
		return 0, apperr.Invalid("the date is invalid", fieldErr("date", "BEFORE_JOURNAL", "not before the journal it reverses"))
	}
	if err := po.CheckDate(ctx, date, "date"); err != nil {
		return 0, err
	}
	number, err := po.svc.days.NextDocumentNumber(ctx, po.propertyID, tenancy.SeqJournal)
	if err != nil {
		return 0, err
	}
	q := po.svc.q(ctx)
	id, err := q.InsertJournal(ctx, accountingdb.InsertJournalParams{
		TenantID: po.p.TenantID, PropertyID: po.propertyID, JournalNumber: number, JournalType: JournalReversal, JournalDate: date,
		Description: "Reversal of " + orig.Number + ": " + orig.Description, ReversesJournalID: &orig.ID, Reason: &reason, PostedAt: po.svc.clock.Now(),
		ActorID: po.p.ActorID(), ApprovedBy: &approvedBy,
	})
	if err != nil {
		return 0, err
	}
	for _, l := range orig.Lines {
		if err := q.InsertJournalLine(ctx, accountingdb.InsertJournalLineParams{
			TenantID: po.p.TenantID, PropertyID: po.propertyID, JournalID: id, LineNo: l.LineNo, AccountID: l.AccountID, Debit: l.Credit, Credit: l.Debit,
			Description: nullable(l.Description), SourceType: nullable(l.SourceType), SourceRef: nullable(l.SourceRef), DepartmentID: l.DepartmentID,
		}); err != nil {
			return 0, err
		}
	}
	return id, po.svc.audit.Write(ctx, entry(po.p, po.propertyID, po.today, "accounting.payables_journal_reversed", "gl_journal", id, auditlabel.Journal(ctx, po.propertyID, id), nil,
		map[string]any{"journal_number": number, "reverses": orig.Number, "reason": reason, "approved_by": approvedBy}))
}
