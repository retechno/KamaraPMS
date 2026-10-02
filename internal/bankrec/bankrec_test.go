package bankrec_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/bankrec"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func d(s string) civil.Date { return civil.MustParseDate(s) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	email            string
	acc              map[string]int64
	bank             bankrec.BankAccount
}

// setup: a property on 30 Sep 2026 with the standard chart and the bank account 1130 registered for reconciliation.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, email: email, acc: map[string]int64{}}
	list, err := e.Accounting.Accounts(admin, p.ID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		f.acc[a.Code] = a.ID
	}
	f.bank, err = e.BankRec.CreateBankAccount(admin, p.ID, bankrec.BankAccountInput{AccountID: f.acc["1130"], Name: "BCA operating", AccountNumber: "123-456"})
	must(t, err)
	return f
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.email, Password: roomstest.Password}
}

// book posts a manual journal between the bank account and another account: money in when in is true.
func (f *fx) book(t *testing.T, key, other, amount string, in bool) {
	t.Helper()
	bank, o := accounting.LineInput{AccountID: f.acc["1130"]}, accounting.LineInput{AccountID: f.acc[other]}
	if in {
		bank.Debit, o.Credit = dec(amount), dec(amount)
	} else {
		bank.Credit, o.Debit = dec(amount), dec(amount)
	}
	_, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: "book " + key, Lines: []accounting.LineInput{bank, o}}, key)
	must(t, err)
}

// firstLines books a deposit of 1,000,000, a payment of 250,000 and, for the "outstanding" cases, one of 100,000.
func (f *fx) books(t *testing.T) {
	f.book(t, "dep", "4110", "1000000", true)
	f.book(t, "pay", "6510", "250000", false)
}

const stmtCSV = "date,description,reference,amount\n2026-09-30,Transfer from guest,TRF1,1000000\n2026-09-30,Payment PLN,CHQ7,-250000\n2026-09-30,Monthly bank fee,,-15000\n"

func (f *fx) importFirst(t *testing.T) bankrec.StatementDetail {
	t.Helper()
	st, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{
		BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"), OpeningBalance: dec("0"), ClosingBalance: dec("735000"), CSV: stmtCSV,
	})
	must(t, err)
	return st
}

func lineOf(t *testing.T, st bankrec.StatementDetail, desc string) bankrec.StatementLine {
	t.Helper()
	for _, l := range st.Lines {
		if strings.Contains(l.Description, desc) {
			return l
		}
	}
	t.Fatalf("no statement line %q in %+v", desc, st.Lines)
	return bankrec.StatementLine{}
}

func (f *fx) uncleared(t *testing.T, id int64) []bankrec.UnclearedLine {
	t.Helper()
	list, err := f.BankRec.UnclearedLines(f.admin, f.propID, id)
	must(t, err)
	return list
}

func TestBankAccounts(t *testing.T) {
	f := setup(t)
	if f.bank.AccountCode != "1130" || !f.bank.IsActive || f.bank.ReconciledTo != nil || !f.bank.BookBalance.IsZero() {
		t.Fatalf("bank account: %+v", f.bank)
	}
	for name, id := range map[string]int64{"a header": f.acc["1100"], "a revenue account": f.acc["4110"], "an unknown account": 999999} {
		_, err := f.BankRec.CreateBankAccount(f.admin, f.propID, bankrec.BankAccountInput{AccountID: id, Name: "x"})
		if e := roomstest.Code(t, err, "VALIDATION_FAILED"); len(e.Fields) == 0 {
			t.Errorf("%s: %+v", name, e)
		}
	}
	_, err := f.BankRec.CreateBankAccount(f.admin, f.propID, bankrec.BankAccountInput{AccountID: f.acc["1130"], Name: "again"})
	wantCode(t, err, "BANK_ACCOUNT_EXISTS")
	_, err = f.BankRec.CreateBankAccount(f.admin, f.propID, bankrec.BankAccountInput{AccountID: f.acc["1140"], Name: " "})
	wantCode(t, err, "VALIDATION_FAILED")
	f.book(t, "dep", "4110", "500", true)
	up, err := f.BankRec.UpdateBankAccount(f.admin, f.propID, f.bank.ID, bankrec.BankAccountPatch{Name: ptr("BCA main"), IsActive: ptr(false)})
	must(t, err)
	if up.Name != "BCA main" || up.IsActive || up.AccountNumber != "123-456" {
		t.Fatalf("updated: %+v", up)
	}
	eq(t, "book balance", up.BookBalance, "500")
	_, err = f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"), ClosingBalance: dec("1"), CSV: "date,amount\n2026-09-30,1\n"})
	wantCode(t, err, "BANK_ACCOUNT_INACTIVE")
	_, err = f.BankRec.GetBankAccount(f.admin, f.propID, 999999)
	wantCode(t, err, "BANK_ACCOUNT_NOT_FOUND")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermBankView)
	if _, err := f.BankRec.BankAccounts(viewer, f.propID); err != nil {
		t.Fatal(err)
	}
	_, err = f.BankRec.CreateBankAccount(viewer, f.propID, bankrec.BankAccountInput{AccountID: f.acc["1140"], Name: "x"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.BankRec.UpdateBankAccount(viewer, f.propID, f.bank.ID, bankrec.BankAccountPatch{Name: ptr("x")})
	wantCode(t, err, "PERMISSION_DENIED")
	f.Clock.Set(roomstest.T0)
	other := f.Tenant(t, "XYZ")
	f.Property(t, other.ID, "SG")
	_, err = f.BankRec.BankAccounts(roomstest.Admin(other.ID), f.propID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestImportReadsTheCommonLayoutsAndRefusesWhatDoesNotAddUp(t *testing.T) {
	f := setup(t)
	imp := func(from, to, opening, closing, csv string) (bankrec.StatementDetail, error) {
		return f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{
			BankAccountID: f.bank.ID, PeriodFrom: d(from), PeriodTo: d(to), OpeningBalance: dec(opening), ClosingBalance: dec(closing), CSV: csv,
		})
	}
	// signed amounts, a BOM, thousands separators and a heading row without an amount
	st, err := imp("2026-09-01", "2026-09-30", "0", "1735000", "\ufeffDate,Description,Reference,Amount\n2026-09-30,Opening line,,0\n30/09/2026,\"Big, deposit\",R1,\"1,735,000\"\n")
	must(t, err)
	if len(st.Lines) != 1 || st.Lines[0].Date != d("2026-09-30") || !st.Lines[0].Amount.Equal(dec("1735000")) || st.Lines[0].Description != "Big, deposit" || st.Status != "OPEN" {
		t.Fatalf("statement: %+v", st)
	}
	must(t, f.BankRec.DeleteStatement(f.admin, f.propID, st.ID))
	// money in and money out in two columns
	st, err = imp("2026-09-01", "2026-09-30", "100", "1150", "date;x\n")
	if err == nil {
		t.Fatalf("a header without amount columns must fail: %+v", st)
	}
	st, err = imp("2026-09-01", "2026-09-30", "100", "1150", "Tanggal,Keterangan,Debit,Kredit\n2026-09-29,Cheque,50,\n2026-09-30,Deposit,,1100\n")
	must(t, err)
	if len(st.Lines) != 2 || !st.Lines[0].Amount.Equal(dec("-50")) || !st.Lines[1].Amount.Equal(dec("1100")) {
		t.Fatalf("two columns: %+v", st.Lines)
	}
	must(t, f.BankRec.DeleteStatement(f.admin, f.propID, st.ID))
	// what is wrong
	e := roomstest.Code(t, errOf(imp("2026-09-01", "2026-09-30", "0", "100", "date,amount\nnot-a-date,5\n2026-09-02,abc\n2026-09-03,1.2345\n")), "VALIDATION_FAILED")
	got := map[string]bool{}
	for _, fe := range e.Fields {
		got[fe.Field] = true
	}
	for _, want := range []string{"rows[2].date", "rows[3].amount", "rows[4].amount"} {
		if !got[want] {
			t.Errorf("want a field error on %s, got %+v", want, e.Fields)
		}
	}
	for name, c := range map[string]struct{ from, to, opening, closing, csv, field string }{
		"does not add up": {"2026-09-01", "2026-09-30", "0", "999", "date,amount\n2026-09-30,1000\n", "closing_balance"},
		"outside period":  {"2026-09-01", "2026-09-30", "0", "5", "date,amount\n2026-10-02,5\n", "csv"},
		"ends before":     {"2026-09-30", "2026-09-01", "0", "0", "date,amount\n", "period_to"},
		"empty":           {"2026-09-01", "2026-09-30", "0", "0", "", "csv"},
		"no date column":  {"2026-09-01", "2026-09-30", "0", "5", "when,amount\n2026-09-30,5\n", "csv"},
	} {
		e := roomstest.Code(t, errOf(imp(c.from, c.to, c.opening, c.closing, c.csv)), "VALIDATION_FAILED")
		found := false
		for _, fe := range e.Fields {
			found = found || fe.Field == c.field
		}
		if !found {
			t.Errorf("%s: want %s, got %+v", name, c.field, e.Fields)
		}
	}
	// a statement overlapping another, out of order, or not following the one before
	first, err := imp("2026-09-01", "2026-09-30", "0", "100", "date,amount\n2026-09-30,100\n")
	must(t, err)
	_, err = imp("2026-09-15", "2026-10-15", "100", "100", "date,amount\n")
	wantCode(t, err, "STATEMENT_OVERLAPS")
	_, err = imp("2026-10-01", "2026-10-31", "99", "99", "date,amount\n")
	wantCode(t, err, "VALIDATION_FAILED") // not continuous
	next, err := imp("2026-10-01", "2026-10-31", "100", "130", "date,amount\n2026-10-05,30\n")
	must(t, err)
	_, err = imp("2026-08-01", "2026-08-31", "0", "0", "date,amount\n")
	wantCode(t, err, "STATEMENT_OUT_OF_ORDER")
	list, err := f.BankRec.Statements(f.admin, f.propID, &f.bank.ID, "")
	must(t, err)
	if len(list) != 2 || list[0].ID != next.ID || list[1].ID != first.ID || list[0].LineCount != 1 || list[0].MatchedCount != 0 {
		t.Fatalf("statements: %+v", list)
	}
	_, err = f.BankRec.GetStatement(f.admin, f.propID, 999999)
	wantCode(t, err, "STATEMENT_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'bank.statement_imported'`); n != 4 {
		t.Fatalf("%d import audit rows", n)
	}
}

func asApp(err error) *apperr.Error {
	var e *apperr.Error
	if errors.As(err, &e) {
		return e
	}
	return nil
}

func errOf(_ bankrec.StatementDetail, err error) error { return err }

func TestMatchingAdjustingAndReconciling(t *testing.T) {
	f := setup(t)
	f.books(t)
	st := f.importFirst(t)
	if st.Summary.CanReconcile || st.Summary.UnmatchedLines != 3 || len(st.Summary.Blockers) == 0 {
		t.Fatalf("nothing matched yet: %+v", st.Summary)
	}
	eq(t, "book balance", st.Summary.BookBalance, "750000")
	if n := len(f.uncleared(t, st.ID)); n != 2 {
		t.Fatalf("%d uncleared journal lines", n)
	}
	// automatic matching takes the two lines it can decide; the bank fee has nothing in the books
	res, err := f.BankRec.AutoMatch(f.admin, f.propID, st.ID)
	must(t, err)
	if res.Matched != 2 || res.Remaining != 1 {
		t.Fatalf("auto match: %+v", res)
	}
	st, err = f.BankRec.GetStatement(f.admin, f.propID, st.ID)
	must(t, err)
	if !lineOf(t, st, "guest").Matched || !lineOf(t, st, "PLN").Matched || lineOf(t, st, "fee").Matched || st.MatchedCount != 2 {
		t.Fatalf("after auto match: %+v", st.Lines)
	}
	eq(t, "cleared", st.Summary.ClearedTotal, "750000")
	if st.Summary.CanReconcile {
		t.Fatal("the bank fee is not in the books yet")
	}
	_, err = f.BankRec.Reconcile(f.admin, f.propID, st.ID)
	wantCode(t, err, "STATEMENT_NOT_READY")
	// the fee is posted from the statement line, against the bank charges account
	fee := lineOf(t, st, "fee")
	_, err = f.BankRec.Adjust(f.admin, f.propID, st.ID, fee.ID, bankrec.AdjustInput{AccountID: f.acc["1130"]})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.BankRec.Adjust(f.admin, f.propID, st.ID, fee.ID, bankrec.AdjustInput{AccountID: f.acc["1100"]})
	wantCode(t, err, "VALIDATION_FAILED") // a header account
	_, err = f.BankRec.Adjust(f.admin, f.propID, st.ID, lineOf(t, st, "guest").ID, bankrec.AdjustInput{AccountID: f.acc["6130"]})
	wantCode(t, err, "LINE_ALREADY_MATCHED")
	st, err = f.BankRec.Adjust(f.admin, f.propID, st.ID, fee.ID, bankrec.AdjustInput{AccountID: f.acc["6130"], Description: "Bank fee September"})
	must(t, err)
	if !st.Summary.CanReconcile || st.Summary.UnmatchedLines != 0 || len(st.Summary.Blockers) != 0 {
		t.Fatalf("ready: %+v", st.Summary)
	}
	eq(t, "cleared equals the bank", st.Summary.ClearedTotal, "735000")
	eq(t, "book balance after the fee", st.Summary.BookBalance, "735000")
	eq(t, "difference", st.Summary.Difference, "0")
	// the fee is in the books: a BANK journal on the day of the line, and it reaches the income statement
	var typ, ref, desc string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT journal_type, reference, description FROM gl_journals WHERE journal_type = 'BANK'`).Scan(&typ, &ref, &desc))
	if ref != "ST1-3" || !strings.Contains(desc, "Bank fee September") {
		t.Fatalf("bank journal: %s %s %s", typ, ref, desc)
	}
	from, to := d("2026-09-01"), d("2026-09-30")
	is, err := f.Accounting.IncomeStatement(f.admin, f.propID, &from, &to)
	must(t, err)
	eq(t, "net income", is.NetIncome, "735000") // 1,000,000 revenue less 250,000 and 15,000 expenses
	// an item the books have and the bank does not show yet (a payment in transit) is an outstanding item
	f.book(t, "transit", "6510", "100000", false)
	st, err = f.BankRec.GetStatement(f.admin, f.propID, st.ID)
	must(t, err)
	eq(t, "outstanding payments", st.Summary.UnclearedOut, "100000")
	eq(t, "bank plus what is in transit", st.Summary.AdjustedBank, "635000")
	eq(t, "book balance", st.Summary.BookBalance, "635000")
	eq(t, "still reconciled", st.Summary.Difference, "0")
	done, err := f.BankRec.Reconcile(f.admin, f.propID, st.ID)
	must(t, err)
	if done.Status != "RECONCILED" || done.ReconciledAt == nil || done.Summary.CanReconcile {
		t.Fatalf("reconciled: %+v", done.Statement)
	}
	acc, err := f.BankRec.GetBankAccount(f.admin, f.propID, f.bank.ID)
	must(t, err)
	if acc.ReconciledTo == nil || *acc.ReconciledTo != d("2026-09-30") {
		t.Fatalf("reconciled to: %+v", acc.ReconciledTo)
	}
	// a reconciled statement is final
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{JournalLineIDs: []int64{1}})
	wantCode(t, err, "STATEMENT_RECONCILED")
	_, err = f.BankRec.Unclear(f.admin, f.propID, st.ID, done.Clearings[0].ID)
	wantCode(t, err, "STATEMENT_RECONCILED")
	_, err = f.BankRec.AutoMatch(f.admin, f.propID, st.ID)
	wantCode(t, err, "STATEMENT_RECONCILED")
	wantCode(t, f.BankRec.DeleteStatement(f.admin, f.propID, st.ID), "STATEMENT_RECONCILED")
	if f.Exec(t, `DELETE FROM bank_clearings`) == nil || f.Exec(t, `UPDATE bank_statements SET closing_balance = 1`) == nil {
		t.Fatal("the database keeps a reconciled statement as it is")
	}
}
