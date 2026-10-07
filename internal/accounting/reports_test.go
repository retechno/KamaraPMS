package accounting_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

// manual posts a balanced manual journal on a date: the debit account against cash.
func (h *hotel) manual(t *testing.T, date, debitCode, creditCode, amount, key string) {
	t.Helper()
	_, err := h.Accounting.PostManual(h.admin, h.propID, accounting.ManualInput{Date: d(date), Description: "manual " + key, Lines: []accounting.LineInput{
		{AccountID: h.byCode(t, debitCode).ID, Debit: dec(amount)}, {AccountID: h.byCode(t, creditCode).ID, Credit: dec(amount)},
	}}, key)
	must(t, err)
}

func lineOf(t *testing.T, lines []accounting.StatementLine, key string) accounting.StatementLine {
	t.Helper()
	for _, l := range lines {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no statement line %s", key)
	return accounting.StatementLine{}
}

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

// busy: a stay with a deposit, charges, a card payment, a transfer and a receipt on 30 Sep (closed), plus manual journals.
func busyHotel(t *testing.T) (*hotel, int64) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-03")
	_, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	st := h.checkIn(t, res, h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	h.charge(t, st.Folio.ID, "MINIBAR", "1000000", "mb")
	_, err = h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "card", folios.PaymentInput{Amount: "200000", PaymentMethod: "CARD"})
	must(t, err)
	co, err := h.Companies.Create(h.admin, h.propID, companies.Input{Code: "ACME", Name: "Acme Corp", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	_, err = h.Folios.Transfer(h.admin, h.propID, st.Folio.ID, "tr", folios.TransferInput{CompanyID: co.ID, Amount: "100000"})
	must(t, err)
	_, err = h.CityLedger.Receive(h.admin, h.propID, co.ID, "rc", cityledger.ReceiptInput{Amount: "50000", PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	h.manual(t, "2026-09-30", "5110", "1110", "20000", "m1") // rooms payroll
	h.manual(t, "2026-09-30", "6110", "1110", "30000", "m2") // administrative payroll
	h.manual(t, "2026-09-30", "7110", "1110", "10000", "m3") // rent
	h.closeDay(t)
	return h, st.Folio.ID
}

func TestTrialBalanceAndLedger(t *testing.T) {
	h, _ := busyHotel(t)
	from, to := d("2026-09-30"), d("2026-09-30")
	tb, err := h.Accounting.TrialBalance(h.admin, h.propID, &from, &to)
	must(t, err)
	if !tb.Totals.Debit.Equal(tb.Totals.Credit) || !tb.Totals.ClosingDebit.Equal(tb.Totals.ClosingCredit) || tb.Totals.Debit.IsZero() {
		t.Fatalf("a trial balance balances: %+v", tb.Totals)
	}
	if !tb.Totals.OpeningDebit.IsZero() || !tb.Totals.OpeningCredit.IsZero() {
		t.Fatalf("nothing before the first day: %+v", tb.Totals)
	}
	row := func(rows []accounting.TrialRow, code string) accounting.TrialRow {
		for _, r := range rows {
			if r.Code == code {
				return r
			}
		}
		t.Fatalf("no row %s", code)
		return accounting.TrialRow{}
	}
	eq(t, "minibar credit", row(tb.Rows, "4230").Credit, "1000000")
	eq(t, "deposits closing credit", row(tb.Rows, "2310").ClosingCredit, "300000")
	eq(t, "cash closing debit", row(tb.Rows, "1110").ClosingDebit, "240000") // 300000 deposit less 60000 paid out
	// a later range opens with what came before
	h.manual(t, "2026-10-01", "5110", "1110", "5000", "m4")
	from2, to2 := d("2026-10-01"), d("2026-10-01")
	tb2, err := h.Accounting.TrialBalance(h.admin, h.propID, &from2, &to2)
	must(t, err)
	eq(t, "cash opens with the day before", row(tb2.Rows, "1110").OpeningDebit, "240000")
	eq(t, "cash closing", row(tb2.Rows, "1110").ClosingDebit, "235000")
	if !tb2.Totals.ClosingDebit.Equal(tb2.Totals.ClosingCredit) || !tb2.Totals.OpeningDebit.Equal(tb2.Totals.OpeningCredit) {
		t.Fatalf("balanced: %+v", tb2.Totals)
	}
	// the ledger of an account: running balance on its normal side, ending at the trial balance
	cash := h.byCode(t, "1110")
	gl, err := h.Accounting.GeneralLedger(h.admin, h.propID, cash.ID, &from, &to2)
	must(t, err)
	if len(gl.Lines) != 5 || gl.Truncated {
		t.Fatalf("ledger lines: %d", len(gl.Lines))
	}
	eq(t, "closing", gl.Closing, "235000")
	eq(t, "debits", gl.Debit, "300000")
	eq(t, "credits", gl.Credit, "65000")
	eq(t, "running balance after the first line", gl.Lines[0].Balance, gl.Lines[0].Debit.Sub(gl.Lines[0].Credit).String())
	dep := h.byCode(t, "2310")
	gl, err = h.Accounting.GeneralLedger(h.admin, h.propID, dep.ID, &from, &to)
	must(t, err)
	eq(t, "a credit account shows credit balances as positive", gl.Closing, "300000")
	if gl.Lines[0].SourceType != "PAYMENT" || gl.Lines[0].SourceRef != "CASH" {
		t.Fatalf("a day close line says what it adds up: %+v", gl.Lines[0])
	}
	opening, err := h.Accounting.GeneralLedger(h.admin, h.propID, dep.ID, &from2, &to2)
	must(t, err)
	eq(t, "opening of the next range", opening.Opening, "300000")
	// range and permission rules
	bad := d("2026-09-01")
	_, err = h.Accounting.TrialBalance(h.admin, h.propID, &to, &bad)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = h.Accounting.GeneralLedger(h.admin, h.propID, 999999, nil, nil)
	wantCode(t, err, "ACCOUNT_NOT_FOUND")
	none := h.User(t, h.tenantID, h.propID, auth.PermAccountingPost)
	_, err = h.Accounting.TrialBalance(none, h.propID, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	now := h.Clock.Now()
	h.Clock.Set(roomstest.T0)
	other := h.Tenant(t, "XYZ")
	op := h.Property(t, other.ID, "SG")
	h.Clock.Set(now)
	_, err = h.Accounting.TrialBalance(roomstest.Admin(other.ID), h.propID, nil, nil)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = h.Accounting.TrialBalance(roomstest.Admin(other.ID), op.ID, nil, nil)
	must(t, err)
}

func TestIncomeStatementFollowsUSALI(t *testing.T) {
	h, _ := busyHotel(t)
	from, to := d("2026-09-30"), d("2026-09-30")
	is, err := h.Accounting.IncomeStatement(h.admin, h.propID, &from, &to)
	must(t, err)
	revenue := lineOf(t, is.Lines, "TOTAL_REVENUE").Amount
	if lineOf(t, is.Lines, "REV_FB").Amount.Cmp(dec("1100000")) != 0 || lineOf(t, is.Lines, "REV_ROOMS").Amount.Cmp(dec("1000000")) != 0 {
		t.Fatalf("revenue by department: %+v", is.Lines)
	}
	if revenue.Cmp(dec("2100000")) != 0 {
		t.Fatalf("revenue %s", revenue)
	}
	eq(t, "departmental profit", lineOf(t, is.Lines, "DEPT_PROFIT").Amount, revenue.Sub(dec("20000")).String())
	gop := revenue.Sub(dec("20000")).Sub(dec("30000"))
	eq(t, "GOP", lineOf(t, is.Lines, "GOP").Amount, gop.String())
	eq(t, "EBITDA", lineOf(t, is.Lines, "EBITDA").Amount, gop.Sub(dec("10000")).String())
	eq(t, "net income", is.NetIncome, gop.Sub(dec("10000")).String())
	eq(t, "the last line is the net income", lineOf(t, is.Lines, "NET_INCOME").Amount, is.NetIncome.String())
	if len(lineOf(t, is.Lines, "UND_AG").Accounts) != 1 || lineOf(t, is.Lines, "UND_AG").Accounts[0].Code != "6110" {
		t.Fatalf("accounts under a group: %+v", lineOf(t, is.Lines, "UND_AG"))
	}
	// a range without activity is empty but complete
	e1, e2 := d("2026-10-01"), d("2026-10-31")
	empty, err := h.Accounting.IncomeStatement(h.admin, h.propID, &e1, &e2)
	must(t, err)
	eq(t, "empty net income", empty.NetIncome, "0")
	_ = lineOf(t, empty.Lines, "GOP")
}

func TestBalanceSheetBalancesAndCarriesTheEarnings(t *testing.T) {
	h, _ := busyHotel(t)
	asOf := d("2026-09-30")
	bs, err := h.Accounting.BalanceSheet(h.admin, h.propID, &asOf)
	must(t, err)
	if !bs.Difference.IsZero() || !bs.Assets.Equal(bs.Liabilities.Add(bs.Equity)) || bs.Assets.IsZero() {
		t.Fatalf("assets %s, liabilities %s, equity %s", bs.Assets, bs.Liabilities, bs.Equity)
	}
	from, to := d("2026-09-01"), d("2026-09-30")
	is, err := h.Accounting.IncomeStatement(h.admin, h.propID, &from, &to)
	must(t, err)
	eq(t, "earnings to date are the net income", lineOf(t, bs.Lines, "EARNINGS").Amount, is.NetIncome.String())
	eq(t, "deposits are a liability", lineOf(t, bs.Lines, "DEPOSITS").Amount, "300000")
	// the guest ledger is a receivable; the earlier date has nothing
	if lineOf(t, bs.Lines, "RECEIVABLES").Amount.IsZero() {
		t.Fatal("receivables")
	}
	before := d("2026-09-29")
	empty, err := h.Accounting.BalanceSheet(h.admin, h.propID, &before)
	must(t, err)
	if !empty.Assets.IsZero() || !empty.Difference.IsZero() {
		t.Fatalf("before the first day: %+v", empty)
	}
	future := d("2026-12-01")
	_, err = h.Accounting.BalanceSheet(h.admin, h.propID, &future)
	wantCode(t, err, "VALIDATION_FAILED")
	// the same accounts in a month later: the earnings stay (there is no year-end closing)
	h.manual(t, "2026-10-01", "6110", "1110", "1000", "m9")
	later := d("2026-10-01")
	bs2, err := h.Accounting.BalanceSheet(h.admin, h.propID, &later)
	must(t, err)
	eq(t, "earnings after another expense", lineOf(t, bs2.Lines, "EARNINGS").Amount, is.NetIncome.Sub(dec("1000")).String())
	if !bs2.Difference.IsZero() {
		t.Fatalf("difference %s", bs2.Difference)
	}
}

func TestReconciliationAgainstTheFolios(t *testing.T) {
	h, folioID := busyHotel(t)
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, nil)
	must(t, err)
	if !rec.Reconciled || rec.PendingDays != 0 || len(rec.Controls) != 4 {
		t.Fatalf("reconciled after a day close: %+v", rec)
	}
	for _, c := range rec.Controls {
		if !c.Difference.IsZero() {
			t.Fatalf("%s: ledger %s, source %s", c.Key, c.Ledger, c.Source)
		}
	}
	eq(t, "deposits", rec.Controls[1].Ledger, "300000")
	eq(t, "city ledger", rec.Controls[2].Ledger, "50000")
	// activity of the open day is not in the ledger yet: it shows as a difference
	h.charge(t, folioID, "LAUNDRY", "40000", "l1")
	rec, err = h.Accounting.Reconciliation(h.admin, h.propID, nil)
	must(t, err)
	if rec.Reconciled || rec.Controls[0].Difference.IsZero() || !rec.IncludesToday {
		t.Fatalf("the open day is ahead of the ledger: %+v", rec.Controls[0])
	}
	asOf := d("2026-09-30")
	rec, err = h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("as of the closed day: %+v", rec)
	}
	// a closed day that was never journaled is reported
	must(t, h.Exec(t, `DELETE FROM accounting_settings WHERE property_id = $1`, h.propID))
	h.Audit.SetJournaler(nil) // a day that closed before the readiness check existed
	h.closeDay(t)
	h.Audit.SetJournaler(h.Accounting)
	must(t, h.Exec(t, `INSERT INTO accounting_settings (tenant_id, property_id, start_date) VALUES ($1, $2, '2026-09-30')`, h.tenantID, h.propID))
	later := civil.MustParseDate("2026-10-01")
	rec, err = h.Accounting.Reconciliation(h.admin, h.propID, &later)
	must(t, err)
	if rec.PendingDays != 1 || rec.Reconciled {
		t.Fatalf("a day without its journal: %+v", rec)
	}
	n, err := h.Accounting.PostPending(h.admin, h.propID)
	must(t, err)
	rec, err = h.Accounting.Reconciliation(h.admin, h.propID, &later)
	must(t, err)
	if n != 1 || !rec.Reconciled {
		t.Fatalf("after the backfill: %d %+v", n, rec)
	}
}
