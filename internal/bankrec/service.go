package bankrec

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/bankrec/bankrecdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

const maxUncleared = 2000

// Service is the bank reconciliation application service. Reading needs bank.view, registering bank accounts
// bank.manage, and importing, matching and reconciling bank.reconcile (reopening also needs an approval).
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	acct  *accounting.Service
	iam   *iam.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, acct *accounting.Service, iamSvc *iam.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, acct: acct, iam: iamSvc}
}

func (s *Service) q(ctx context.Context) *bankrecdb.Queries { return bankrecdb.New(s.txm.DB(ctx)) }

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func ptr[T any](v T) *T { return &v }

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func entry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: entity, EntityID: id, Old: old, New: updated}
}

func errBankAccountNotFound() *apperr.Error {
	return apperr.NotFound("BANK_ACCOUNT_NOT_FOUND", "the bank account does not exist in this property")
}
func errStatementNotFound() *apperr.Error {
	return apperr.NotFound("STATEMENT_NOT_FOUND", "the bank statement does not exist in this property")
}

// ---------------------------------------------------------------------------------------------------------------
// Bank accounts

func toBankAccount(r bankrecdb.ListBankAccountsRow) BankAccount {
	b := BankAccount{
		ID: r.ID, AccountID: r.AccountID, AccountCode: r.AccountCode, AccountName: r.AccountName, Name: r.Name, AccountNumber: deref(r.AccountNumber), IsActive: r.IsActive,
		BookBalance: r.BookBalance, OpenStatements: int(r.OpenStatements), CreatedAt: r.CreatedAt,
	}
	if r.ReconciledTo.Year() > 1900 {
		b.ReconciledTo = ptr(r.ReconciledTo)
	}
	return b
}

// BankAccounts lists the accounts of the books that are reconciled, with their book balance and how far they are
// reconciled (bank.view).
func (s *Service) BankAccounts(ctx context.Context, propertyID int64) ([]BankAccount, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return nil, err
	}
	return s.listBankAccounts(ctx, p.TenantID, propertyID, nil)
}

func (s *Service) listBankAccounts(ctx context.Context, tenantID, propertyID int64, id *int64) ([]BankAccount, error) {
	rows, err := s.q(ctx).ListBankAccounts(ctx, bankrecdb.ListBankAccountsParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return nil, err
	}
	out := make([]BankAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBankAccount(r))
	}
	return out, nil
}

func (s *Service) loadBankAccount(ctx context.Context, tenantID, propertyID, id int64) (BankAccount, error) {
	list, err := s.listBankAccounts(ctx, tenantID, propertyID, &id)
	if err != nil {
		return BankAccount{}, err
	}
	if len(list) == 0 {
		return BankAccount{}, errBankAccountNotFound()
	}
	return list[0], nil
}

// GetBankAccount is one bank account (bank.view).
func (s *Service) GetBankAccount(ctx context.Context, propertyID, id int64) (BankAccount, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return BankAccount{}, err
	}
	return s.loadBankAccount(ctx, p.TenantID, propertyID, id)
}

// CreateBankAccount registers an asset account of the books for reconciliation (bank.manage).
func (s *Service) CreateBankAccount(ctx context.Context, propertyID int64, in BankAccountInput) (BankAccount, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankManage)
	if err != nil {
		return BankAccount{}, err
	}
	name, number := strings.TrimSpace(in.Name), strings.TrimSpace(in.AccountNumber)
	var fields []apperr.FieldError
	if name == "" {
		fields = append(fields, fieldErr("name", "REQUIRED", "name the bank account"))
	} else if len([]rune(name)) > 100 {
		fields = append(fields, fieldErr("name", "TOO_LONG", "at most 100 characters"))
	}
	if len([]rune(number)) > 60 {
		fields = append(fields, fieldErr("account_number", "TOO_LONG", "at most 60 characters"))
	}
	if len(fields) > 0 {
		return BankAccount{}, apperr.Invalid("the bank account is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		a, err := s.q(ctx).AccountEligible(ctx, bankrecdb.AccountEligibleParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.AccountID})
		switch {
		case isNoRows(err):
			return apperr.Invalid("the bank account is invalid", fieldErr("account_id", "NOT_FOUND", "no such account in this property"))
		case err != nil:
			return err
		case a.AccountType != accounting.TypeAsset || !a.IsPostable || !a.IsActive:
			return apperr.Invalid("the bank account is invalid", fieldErr("account_id", "NOT_USABLE", "an active asset account that takes postings"))
		}
		id, err = s.q(ctx).InsertBankAccount(ctx, bankrecdb.InsertBankAccountParams{
			TenantID: p.TenantID, PropertyID: propertyID, AccountID: in.AccountID, Name: name, AccountNumber: nullable(number), IsActive: in.IsActive == nil || *in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.account_created", "bank_account", id, nil, map[string]any{"name": name, "account_id": in.AccountID}))
	})
	if err != nil {
		return BankAccount{}, err
	}
	return s.loadBankAccount(ctx, p.TenantID, propertyID, id)
}

// UpdateBankAccount changes the name, the account number or whether the bank account is in use (bank.manage).
func (s *Service) UpdateBankAccount(ctx context.Context, propertyID, id int64, patch BankAccountPatch) (BankAccount, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankManage)
	if err != nil {
		return BankAccount{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.BankAccounts, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return errBankAccountNotFound()
		}
		cur, err := s.loadBankAccount(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		name, number, active := cur.Name, cur.AccountNumber, cur.IsActive
		if patch.Name != nil {
			name = strings.TrimSpace(*patch.Name)
		}
		if patch.AccountNumber != nil {
			number = strings.TrimSpace(*patch.AccountNumber)
		}
		if patch.IsActive != nil {
			active = *patch.IsActive
		}
		if name == "" || len([]rune(name)) > 100 || len([]rune(number)) > 60 {
			return apperr.Invalid("the bank account is invalid", fieldErr("name", "INVALID", "a name of up to 100 characters and an account number of up to 60"))
		}
		if err := s.q(ctx).UpdateBankAccount(ctx, bankrecdb.UpdateBankAccountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: name, AccountNumber: nullable(number), IsActive: active, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.account_updated", "bank_account", id,
			map[string]any{"name": cur.Name, "active": cur.IsActive}, map[string]any{"name": name, "active": active}))
	})
	if err != nil {
		return BankAccount{}, err
	}
	return s.loadBankAccount(ctx, p.TenantID, propertyID, id)
}

// ---------------------------------------------------------------------------------------------------------------
// Statements

func toStatement(r bankrecdb.ListStatementsRow) Statement {
	return Statement{
		ID: r.ID, BankAccountID: r.BankAccountID, BankName: r.BankName, AccountCode: r.AccountCode, PeriodFrom: r.PeriodFrom, PeriodTo: r.PeriodTo,
		OpeningBalance: r.OpeningBalance, ClosingBalance: r.ClosingBalance, Status: r.Status, Note: deref(r.Note), LineCount: int(r.LineCount), MatchedCount: int(r.MatchedCount),
		ImportedAt: r.ImportedAt, ReconciledAt: r.ReconciledAt, ReopenReason: deref(r.ReopenReason),
	}
}

// Statements lists the imported statements, newest first (bank.view).
func (s *Service) Statements(ctx context.Context, propertyID int64, bankAccountID *int64, status string) ([]Statement, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListStatements(ctx, bankrecdb.ListStatementsParams{TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: bankAccountID, Status: nullable(status), RowLimit: 200})
	if err != nil {
		return nil, err
	}
	out := make([]Statement, 0, len(rows))
	for _, r := range rows {
		out = append(out, toStatement(r))
	}
	return out, nil
}

func (s *Service) statementRow(ctx context.Context, tenantID, propertyID, id int64) (Statement, error) {
	rows, err := s.q(ctx).ListStatements(ctx, bankrecdb.ListStatementsParams{TenantID: tenantID, PropertyID: propertyID, ID: &id, RowLimit: 1})
	if err != nil {
		return Statement{}, err
	}
	if len(rows) == 0 {
		return Statement{}, errStatementNotFound()
	}
	return toStatement(rows[0]), nil
}

// GetStatement is a statement with its lines, clearings and the comparison of the bank with the books (bank.view).
func (s *Service) GetStatement(ctx context.Context, propertyID, id int64) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, id)
}

func (s *Service) loadDetail(ctx context.Context, tenantID, propertyID, id int64) (StatementDetail, error) {
	st, err := s.statementRow(ctx, tenantID, propertyID, id)
	if err != nil {
		return StatementDetail{}, err
	}
	q := s.q(ctx)
	lines, err := q.ListStatementLines(ctx, bankrecdb.ListStatementLinesParams{TenantID: tenantID, PropertyID: propertyID, StatementID: id})
	if err != nil {
		return StatementDetail{}, err
	}
	cls, err := q.ListClearings(ctx, bankrecdb.ListClearingsParams{TenantID: tenantID, PropertyID: propertyID, StatementID: id})
	if err != nil {
		return StatementDetail{}, err
	}
	d := StatementDetail{Statement: st, Lines: make([]StatementLine, 0, len(lines)), Clearings: make([]Clearing, 0, len(cls))}
	byLine := map[int64][]Clearing{}
	for _, c := range cls {
		cl := Clearing{ID: c.ID, StatementLineID: c.StatementLineID, JournalLineID: c.JournalLineID, Amount: c.Amount, JournalDate: c.JournalDate, JournalNumber: c.JournalNumber, JournalType: c.JournalType, Description: c.Description}
		d.Clearings = append(d.Clearings, cl)
		if c.StatementLineID != nil {
			byLine[*c.StatementLineID] = append(byLine[*c.StatementLineID], cl)
		}
	}
	for _, l := range lines {
		cl := byLine[l.ID]
		if cl == nil {
			cl = []Clearing{}
		}
		d.Lines = append(d.Lines, StatementLine{ID: l.ID, LineNo: int(l.LineNo), Date: l.LineDate, Description: deref(l.Description), Reference: deref(l.Reference), Amount: l.Amount, Cleared: l.Cleared, Matched: l.Amount.Equal(l.Cleared), Clearings: cl})
	}
	d.Summary, err = s.summarize(ctx, tenantID, propertyID, d)
	return d, err
}

// summarize compares the bank with the books and says what stands in the way of reconciling.
func (s *Service) summarize(ctx context.Context, tenantID, propertyID int64, d StatementDetail) (Summary, error) {
	q := s.q(ctx)
	accts, err := s.loadBankAccount(ctx, tenantID, propertyID, d.BankAccountID)
	if err != nil {
		return Summary{}, err
	}
	book, err := q.BookBalance(ctx, bankrecdb.BookBalanceParams{TenantID: tenantID, PropertyID: propertyID, AccountID: accts.AccountID, ToDate: d.PeriodTo})
	if err != nil {
		return Summary{}, err
	}
	cleared, err := q.ClearedTotal(ctx, bankrecdb.ClearedTotalParams{TenantID: tenantID, PropertyID: propertyID, BankAccountID: d.BankAccountID, PeriodTo: d.PeriodTo})
	if err != nil {
		return Summary{}, err
	}
	unc, err := q.UnclearedTotals(ctx, bankrecdb.UnclearedTotalsParams{TenantID: tenantID, PropertyID: propertyID, AccountID: accts.AccountID, ToDate: d.PeriodTo})
	if err != nil {
		return Summary{}, err
	}
	sum := Summary{
		StatementClosing: d.ClosingBalance, BookBalance: book, ClearedTotal: cleared, UnclearedIn: unc.MoneyIn, UnclearedOut: unc.MoneyOut, UnclearedCount: int(unc.Lines), Blockers: []string{},
	}
	for _, l := range d.Lines {
		if !l.Matched {
			sum.UnmatchedLines++
			sum.UnmatchedAmount = sum.UnmatchedAmount.Add(l.Amount.Sub(l.Cleared))
		}
	}
	sum.AdjustedBank = d.ClosingBalance.Add(unc.MoneyIn).Sub(unc.MoneyOut)
	sum.Difference = sum.AdjustedBank.Sub(book)
	if d.Status == StatementReconciled {
		return sum, nil
	}
	prev, err := q.PreviousStatement(ctx, bankrecdb.PreviousStatementParams{TenantID: tenantID, PropertyID: propertyID, BankAccountID: d.BankAccountID, Before: d.PeriodFrom})
	switch {
	case err == nil && prev.Status != StatementReconciled:
		sum.Blockers = append(sum.Blockers, "The statement before this one is not reconciled yet.")
	case err != nil && !isNoRows(err):
		return Summary{}, err
	}
	if sum.UnmatchedLines > 0 {
		sum.Blockers = append(sum.Blockers, "Some statement lines are not matched with the books.")
	}
	if !cleared.Equal(d.ClosingBalance) {
		sum.Blockers = append(sum.Blockers, "The journal lines cleared so far add up to "+cleared.String()+", not to the closing balance of the bank.")
	}
	sum.CanReconcile = len(sum.Blockers) == 0
	return sum, nil
}

// UnclearedLines lists the journal lines of the statement's account up to its end that no statement has cleared, oldest
// first (bank.view).
func (s *Service) UnclearedLines(ctx context.Context, propertyID, statementID int64) ([]UnclearedLine, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankView)
	if err != nil {
		return nil, err
	}
	st, err := s.statementRow(ctx, p.TenantID, propertyID, statementID)
	if err != nil {
		return nil, err
	}
	return s.unclearedFor(ctx, p.TenantID, propertyID, st)
}

func (s *Service) unclearedFor(ctx context.Context, tenantID, propertyID int64, st Statement) ([]UnclearedLine, error) {
	ba, err := s.loadBankAccount(ctx, tenantID, propertyID, st.BankAccountID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).UnclearedLines(ctx, bankrecdb.UnclearedLinesParams{TenantID: tenantID, PropertyID: propertyID, AccountID: ba.AccountID, ToDate: st.PeriodTo, RowLimit: maxUncleared})
	if err != nil {
		return nil, err
	}
	out := make([]UnclearedLine, 0, len(rows))
	for _, r := range rows {
		desc := deref(r.Description)
		if desc == "" {
			desc = r.JournalDescription
		}
		out = append(out, UnclearedLine{JournalLineID: r.ID, Date: r.JournalDate, JournalID: r.JournalID, JournalNumber: r.JournalNumber, JournalType: r.JournalType, Description: desc, Reference: deref(r.SourceRef), Amount: r.Amount})
	}
	return out, nil
}

// ImportStatement imports a bank statement (bank.reconcile): everything is checked first and a mistake changes nothing.
// The lines must add up to the difference of the printed balances, lie within the period, follow the statement before
// (its closing balance is this one's opening balance) and not overlap another statement of the account.
func (s *Service) ImportStatement(ctx context.Context, propertyID int64, in ImportInput) (StatementDetail, error) {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return StatementDetail{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return StatementDetail{}, err
	}
	var fields []apperr.FieldError
	switch {
	case in.PeriodFrom.IsZero() || in.PeriodTo.IsZero():
		fields = append(fields, fieldErr("period_to", "REQUIRED", "the first and the last day of the statement"))
	case in.PeriodTo.Before(in.PeriodFrom):
		fields = append(fields, fieldErr("period_to", "BEFORE_FROM", "not before the first day"))
	}
	if len([]rune(in.Note)) > 300 {
		fields = append(fields, fieldErr("note", "TOO_LONG", "at most 300 characters"))
	}
	lines, perr := parseStatement(in.CSV, prop.CurrencyDecimals)
	if perr != nil {
		var ae *apperr.Error
		if errors.As(perr, &ae) {
			fields = append(fields, ae.Fields...)
		} else {
			return StatementDetail{}, perr
		}
	}
	net := decimal.Zero
	for i, l := range lines {
		net = net.Add(l.amount)
		if !in.PeriodFrom.IsZero() && (l.date.Before(in.PeriodFrom) || l.date.After(in.PeriodTo)) {
			fields = append(fields, fieldErr("csv", "OUTSIDE_PERIOD", "line "+itoa(i+1)+" is dated "+l.date.String()+", outside the period of the statement"))
			break
		}
	}
	if len(fields) == 0 && !in.OpeningBalance.Add(net).Equal(in.ClosingBalance) {
		fields = append(fields, fieldErr("closing_balance", "DOES_NOT_ADD_UP", "the opening balance "+in.OpeningBalance.String()+" and the lines ("+net.String()+") make "+in.OpeningBalance.Add(net).String()+", not "+in.ClosingBalance.String()))
	}
	if len(fields) > 0 {
		return StatementDetail{}, apperr.Invalid("the statement is invalid", fields...)
	}
	var id int64
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.BankAccounts, db.ForUpdate, propertyID, []int64{in.BankAccountID}); err != nil {
			return errBankAccountNotFound()
		}
		ba, err := s.loadBankAccount(ctx, p.TenantID, propertyID, in.BankAccountID)
		if err != nil {
			return err
		}
		if !ba.IsActive {
			return apperr.Conflict("BANK_ACCOUNT_INACTIVE", "the bank account is not in use")
		}
		q := s.q(ctx)
		n, err := q.CountOverlappingStatements(ctx, bankrecdb.CountOverlappingStatementsParams{TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: in.BankAccountID, PeriodFrom: in.PeriodFrom, PeriodTo: in.PeriodTo})
		if err != nil {
			return err
		}
		if n > 0 {
			return apperr.Conflict("STATEMENT_OVERLAPS", "another statement of this bank account covers some of these days")
		}
		later, err := q.NextStatementExists(ctx, bankrecdb.NextStatementExistsParams{TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: in.BankAccountID, After: in.PeriodFrom})
		if err != nil {
			return err
		}
		if later {
			return apperr.Conflict("STATEMENT_OUT_OF_ORDER", "statements are imported in the order of their periods: a later one exists")
		}
		prev, err := q.PreviousStatement(ctx, bankrecdb.PreviousStatementParams{TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: in.BankAccountID, Before: in.PeriodFrom})
		switch {
		case err == nil && !prev.ClosingBalance.Equal(in.OpeningBalance):
			return apperr.Invalid("the statement is invalid", fieldErr("opening_balance", "NOT_CONTINUOUS", "the statement before closed at "+prev.ClosingBalance.String()))
		case err != nil && !isNoRows(err):
			return err
		}
		id, err = q.InsertStatement(ctx, bankrecdb.InsertStatementParams{
			TenantID: p.TenantID, PropertyID: propertyID, BankAccountID: in.BankAccountID, PeriodFrom: in.PeriodFrom, PeriodTo: in.PeriodTo, OpeningBalance: in.OpeningBalance,
			ClosingBalance: in.ClosingBalance, Note: nullable(in.Note), Now: s.clock.Now(), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		for i, l := range lines {
			if err := q.InsertStatementLine(ctx, bankrecdb.InsertStatementLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, StatementID: id, LineNo: int32(i + 1), LineDate: l.date, Description: nullable(l.description), Reference: nullable(l.reference), Amount: l.amount, //nolint:gosec // G115: at most maxStatementLines
			}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.statement_imported", "bank_statement", id, nil,
			map[string]any{"bank_account": ba.Name, "from": in.PeriodFrom.String(), "to": in.PeriodTo.String(), "lines": len(lines), "closing": in.ClosingBalance.String()}))
	})
	if err != nil {
		return StatementDetail{}, err
	}
	return s.loadDetail(ctx, p.TenantID, propertyID, id)
}

func itoa(n int) string { return decimal.NewFromInt(int64(n)).String() }

// DeleteStatement deletes a statement that is not reconciled, with its lines and clearings (bank.reconcile).
func (s *Service) DeleteStatement(ctx context.Context, propertyID, id int64) error {
	p, err := s.need(ctx, propertyID, auth.PermBankReconcile)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		st, err := s.statementRow(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.BankAccounts, db.ForUpdate, propertyID, []int64{st.BankAccountID}); err != nil {
			return errBankAccountNotFound()
		}
		st, err = s.statementRow(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if st.Status != StatementOpen {
			return apperr.Conflict("STATEMENT_RECONCILED", "a reconciled statement is deleted by reopening it first")
		}
		q := s.q(ctx)
		if err := q.DeleteStatementClearings(ctx, bankrecdb.DeleteStatementClearingsParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: id}); err != nil {
			return err
		}
		if err := q.DeleteStatementLines(ctx, bankrecdb.DeleteStatementLinesParams{TenantID: p.TenantID, PropertyID: propertyID, StatementID: id}); err != nil {
			return err
		}
		if err := q.DeleteStatement(ctx, bankrecdb.DeleteStatementParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "bank.statement_deleted", "bank_statement", id,
			map[string]any{"from": st.PeriodFrom.String(), "to": st.PeriodTo.String(), "lines": st.LineCount}, nil))
	})
}
