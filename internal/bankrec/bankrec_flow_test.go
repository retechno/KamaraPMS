package bankrec_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/bankrec"
	"kamarapms/internal/folios"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
)

// guestFx is the bank fixture with a hotel: guests pay deposits on a future reservation by bank transfer, card and
// e-wallet, and the night audit closes 30 Sep, so the day close journal carries what the bank has to be matched with.
type guestFx struct {
	*fx
	resID int64
}

func setupGuests(t *testing.T) *guestFx {
	t.Helper()
	f := setup(t)
	typ := f.RoomType(t, f.admin, f.propID, "DLX")
	f.Room(t, f.admin, f.propID, typ.ID, "101", housekeeping.Clean)
	var chargeID, plan, guest int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, f.propID).Scan(&chargeID))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, f.tenantID, f.propID, chargeID).Scan(&plan))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, f.tenantID, f.propID).Scan(&guest))
	_, err := f.Rates.FillRates(f.admin, f.propID, rates.FillInput{RatePlanID: plan, RoomTypeIDs: []int64{typ.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: typ.ID, RatePlanID: plan, Arrival: d("2026-10-02"), Departure: d("2026-10-04"), Adults: 2},
	}})
	must(t, err)
	return &guestFx{fx: f, resID: res.ID}
}

// pay takes a deposit, which is credited to advance deposits and received into the account of the method.
func (g *guestFx) pay(t *testing.T, key, method, amount, reference string) folios.PaymentResult {
	t.Helper()
	out, err := g.Folios.Deposit(g.admin, g.propID, g.resID, key, folios.PaymentInput{Amount: amount, PaymentMethod: method, ReferenceNumber: reference})
	must(t, err)
	return out
}

// closeDay runs the night audit of the current business date.
func (g *guestFx) closeDay(t *testing.T) {
	t.Helper()
	day, err := g.Tenancy.CurrentBusinessDay(g.admin, g.propID)
	must(t, err)
	g.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = g.Audit.Run(g.admin, g.propID, day.BusinessDate)
	must(t, err)
}

// ledger is what the books hold on an account over the day.
func (g *guestFx) ledger(t *testing.T, code string) accounting.GeneralLedger {
	t.Helper()
	from, to := d("2026-09-01"), d("2026-09-30")
	gl, err := g.Accounting.GeneralLedger(g.admin, g.propID, g.acc[code], &from, &to)
	must(t, err)
	return gl
}

func (g *guestFx) importStatement(t *testing.T, opening, closing, csv string) bankrec.StatementDetail {
	t.Helper()
	st, err := g.BankRec.ImportStatement(g.admin, g.propID, bankrec.ImportInput{
		BankAccountID: g.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-10-01"), OpeningBalance: dec(opening), ClosingBalance: dec(closing), CSV: csv,
	})
	must(t, err)
	return st
}

func TestTheDayCloseCarriesALineForEachPayment(t *testing.T) {
	g := setupGuests(t)
	a := g.pay(t, "a", "BANK_TRANSFER", "100000", "TRF-A")
	g.pay(t, "b", "BANK_TRANSFER", "100000", "TRF-B")
	g.pay(t, "c", "BANK_TRANSFER", "300000", "TRF-C")
	g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	g.pay(t, "k2", "CARD", "600000", "AUTH-2")
	g.pay(t, "w", "OTHER", "50000", "QR-9")
	voided := g.pay(t, "v", "BANK_TRANSFER", "777000", "TRF-WRONG")
	_, err := g.Folios.Void(g.admin, g.propID, voided.Payment.ID, folios.CorrectionInput{Reason: "wrong guest", Approval: g.approval()})
	must(t, err)
	g.closeDay(t)

	bank := g.ledger(t, "1130")
	if len(bank.Lines) != 3 {
		t.Fatalf("a line for each transfer, and none for the one voided the same day: %+v", bank.Lines)
	}
	if bank.Lines[0].SourceType != "PAYMENT" || bank.Lines[0].SourceRef != a.Payment.PaymentNumber || bank.Lines[0].Description == "" {
		t.Fatalf("the line names the payment: %+v", bank.Lines[0])
	}
	got := map[string]string{}
	for _, l := range bank.Lines {
		got[l.SourceRef] = l.Description
		if !l.Credit.IsZero() {
			t.Fatalf("money in is a debit: %+v", l)
		}
	}
	for _, want := range []string{"TRF-A", "TRF-B", "TRF-C"} {
		found := false
		for _, desc := range got {
			found = found || contains(desc, want)
		}
		if !found {
			t.Errorf("no line carries the reference %s: %v", want, got)
		}
	}
	eq(t, "bank", bank.Closing, "500000")
	card := g.ledger(t, "1150")
	if len(card.Lines) != 2 {
		t.Fatalf("a line for each card payment: %+v", card.Lines)
	}
	eq(t, "card clearing", card.Closing, "1000000")
	if wallet := g.ledger(t, "1160"); len(wallet.Lines) != 1 || !wallet.Closing.Equal(dec("50000")) {
		t.Fatalf("e-wallet: %+v", wallet.Lines)
	}
	// the other side is still one line per method: the deposits held
	if dep := g.ledger(t, "2310"); len(dep.Lines) != 3 {
		t.Fatalf("deposits by method: %+v", dep.Lines)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestAutoMatchUsesTheReferenceTheGuestGave(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "a", "BANK_TRANSFER", "100000", "TRF-A")
	g.pay(t, "b", "BANK_TRANSFER", "100000", "TRF-B")
	g.pay(t, "c", "BANK_TRANSFER", "300000", "TRF-C")
	g.closeDay(t)
	// the bank lists them in another order, a day later; two have the same amount
	st := g.importStatement(t, "0", "500000", "date,description,reference,amount\n2026-10-01,Transfer,TRF-C,300000\n2026-10-01,Transfer,TRF-B,100000\n2026-10-01,Transfer,TRF-A,100000\n")
	res, err := g.BankRec.AutoMatch(g.admin, g.propID, st.ID)
	must(t, err)
	if res.Matched != 3 || res.Remaining != 0 {
		t.Fatalf("auto match: %+v", res)
	}
	st, err = g.BankRec.GetStatement(g.admin, g.propID, st.ID)
	must(t, err)
	for _, l := range st.Lines {
		if len(l.Clearings) != 1 || !contains(l.Clearings[0].Description, l.Reference) {
			t.Errorf("line %s is matched with %+v", l.Reference, l.Clearings)
		}
	}
	if !st.Summary.CanReconcile {
		t.Fatalf("ready: %+v", st.Summary)
	}
	done, err := g.BankRec.Reconcile(g.admin, g.propID, st.ID)
	must(t, err)
	if done.Status != "RECONCILED" {
		t.Fatalf("reconciled: %+v", done.Statement)
	}
}

func TestOneJournalLineIsClearedInPartsByManyStatementLines(t *testing.T) {
	f := setup(t)
	// the books hold the total of three transfers on one line, as the day close journals of earlier days do
	f.book(t, "agg", "4110", "600000", true)
	st, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{
		BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"), OpeningBalance: dec("0"), ClosingBalance: dec("600000"),
		CSV: "date,description,amount\n2026-09-30,Transfer 1,100000\n2026-09-30,Transfer 2,200000\n2026-09-30,Transfer 3,300000\n",
	})
	must(t, err)
	var jl int64
	for _, l := range f.uncleared(t, st.ID) {
		jl = l.JournalLineID
	}
	one, two, three := st.Lines[0].ID, st.Lines[1].ID, st.Lines[2].ID
	part := func(line int64, amount string) bankrec.ClearAllocation {
		a := dec(amount)
		return bankrec.ClearAllocation{StatementLineID: &line, JournalLineID: jl, Amount: &a}
	}
	// more than the journal line holds, on the wrong side, or more than the statement line needs
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{part(one, "100000"), part(two, "200000"), part(three, "400000")}})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{part(one, "-100000")}})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{part(one, "150000")}})
	wantCode(t, err, "VALIDATION_FAILED")
	if f.Count(t, `SELECT count(*) FROM bank_clearings`) != 0 {
		t.Fatal("a refused request clears nothing")
	}
	// the first transfer: the journal line is cleared in part, and still listed with what is left
	got, err := f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{part(one, "100000")}})
	must(t, err)
	if !lineOf(t, got, "Transfer 1").Matched || lineOf(t, got, "Transfer 2").Matched {
		t.Fatalf("matched: %+v", got.Lines)
	}
	left := f.uncleared(t, st.ID)
	if len(left) != 1 {
		t.Fatalf("uncleared: %+v", left)
	}
	eq(t, "cleared of the line", left[0].Cleared, "100000")
	eq(t, "left of the line", left[0].Remaining, "500000")
	eq(t, "money in transit counts what is left", got.Summary.UnclearedIn, "500000")
	// the other two together
	got, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{part(two, "200000"), part(three, "300000")}})
	must(t, err)
	if !got.Summary.CanReconcile || got.Summary.UnmatchedLines != 0 || len(f.uncleared(t, st.ID)) != 0 {
		t.Fatalf("ready: %+v", got.Summary)
	}
	eq(t, "cleared", got.Summary.ClearedTotal, "600000")
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{part(one, "1")}})
	wantCode(t, err, "ALREADY_CLEARED")
	// undoing one part gives it back to the journal line
	got, err = f.BankRec.Unclear(f.admin, f.propID, st.ID, got.Clearings[1].ID)
	must(t, err)
	if left := f.uncleared(t, st.ID); len(left) != 1 || !left[0].Remaining.Equal(dec("200000")) {
		t.Fatalf("after undoing: %+v", left)
	}
	// the database keeps a journal line within its amount, whatever the application does
	if err := f.Exec(t, `INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, journal_line_id, amount) VALUES ($1, $2, $3, $4, $5, 300000)`,
		f.tenantID, f.propID, f.bank.ID, st.ID, jl); err == nil {
		t.Fatal("a journal line cannot be cleared for more than it holds")
	}
	// a part cleared without a statement line is refused: that is for whole lines
	_, err = f.BankRec.Clear(f.admin, f.propID, st.ID, bankrec.ClearInput{Allocations: []bankrec.ClearAllocation{{JournalLineID: jl, Amount: ptr(dec("100000"))}}})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestCardPaymentsAreSettledNetOfTheCommission(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	g.pay(t, "k2", "CARD", "600000", "AUTH-2")
	g.pay(t, "w", "OTHER", "50000", "QR-9")
	g.closeDay(t)
	// the acquirer pays 980,000 for the card payments (2% commission) and the wallet 49,500
	st := g.importStatement(t, "0", "1029500", "date,description,reference,amount\n2026-10-01,Card settlement,SETT-77,980000\n2026-10-01,E-wallet payout,PO-5,49500\n")
	cardLines, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "CARD")
	must(t, err)
	if len(cardLines) != 2 {
		t.Fatalf("card payments waiting for their settlement: %+v", cardLines)
	}
	eq(t, "first card payment", cardLines[0].Amount, "400000")
	var ids []int64
	for _, l := range cardLines {
		ids = append(ids, l.JournalLineID)
	}
	card, wallet := lineOf(t, st, "Card settlement"), lineOf(t, st, "E-wallet")
	bad := func(name string, line int64, in bankrec.SettleInput, code string) {
		t.Helper()
		_, err := g.BankRec.Settle(g.admin, g.propID, st.ID, line, in)
		e := asApp(err)
		if e == nil || e.Code != code {
			t.Errorf("%s: %v", name, err)
		}
	}
	bad("no commission account", card.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids}, "VALIDATION_FAILED")
	bad("the bank account as commission", card.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["1130"]}, "VALIDATION_FAILED")
	bad("a header as commission", card.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6100"]}, "VALIDATION_FAILED")
	bad("an unknown key", card.ID, bankrec.SettleInput{AccountKey: "CASH", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]}, "VALIDATION_FAILED")
	bad("the wrong clearing account", card.ID, bankrec.SettleInput{AccountKey: "OTHER_PAYMENT", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]}, "VALIDATION_FAILED")
	bad("a single payment is less than was paid out", card.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids[:1], FeeAccountID: g.acc["6130"]}, "VALIDATION_FAILED")
	if g.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'BANK'`) != 0 {
		t.Fatal("a refused settlement posts nothing")
	}
	done, err := g.BankRec.Settle(g.admin, g.propID, st.ID, card.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"], Description: "Card settlement 1 Oct"})
	must(t, err)
	if !lineOf(t, done, "Card settlement").Matched {
		t.Fatalf("the statement line is matched: %+v", done.Lines)
	}
	// 1,000,000 of card payments: the bank 980,000, the commission 20,000
	eq(t, "bank", g.balanceOf(t, "1130"), "980000")
	eq(t, "commission", g.balanceOf(t, "6130"), "20000")
	eq(t, "card clearing is settled", g.balanceOf(t, "1150"), "0")
	var gross, net, fee string
	must(t, g.Pool.QueryRow(context.Background(), `SELECT gross::text, net::text, fee::text FROM card_settlements`).Scan(&gross, &net, &fee))
	if gross != "1000000.000" || net != "980000.000" || fee != "20000.000" || g.Count(t, `SELECT count(*) FROM card_settlement_items`) != 2 {
		t.Fatalf("settlement: %s %s %s", gross, net, fee)
	}
	// settled once: neither the payments nor the credit line of the settlement are offered again
	rest, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "CARD")
	must(t, err)
	if len(rest) != 0 {
		t.Fatalf("settled: %+v", rest)
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, wallet.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]})
	wantCode(t, err, "ALREADY_SETTLED")
	// the wallet: 50,000 paid out as 49,500
	walletLines, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "OTHER_PAYMENT")
	must(t, err)
	if len(walletLines) != 1 {
		t.Fatalf("e-wallet payments: %+v", walletLines)
	}
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, wallet.ID, bankrec.SettleInput{AccountKey: "OTHER_PAYMENT", JournalLineIDs: []int64{walletLines[0].JournalLineID}, FeeAccountID: g.acc["6130"]})
	must(t, err)
	// a payout of more than the payments is refused; money out is not a settlement
	bad("more than the payments", wallet.ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: ids, FeeAccountID: g.acc["6130"]}, "LINE_ALREADY_MATCHED")
	// the statement balances: nothing else to match, the transfers and the settlements are all the bank paid
	final, err := g.BankRec.GetStatement(g.admin, g.propID, st.ID)
	must(t, err)
	if !final.Summary.CanReconcile {
		t.Fatalf("ready: %+v", final.Summary)
	}
	eq(t, "cleared", final.Summary.ClearedTotal, "1029500")
	if _, err := g.BankRec.Reconcile(g.admin, g.propID, st.ID); err != nil {
		t.Fatal(err)
	}
	// the commission reaches the income statement
	from, to := d("2026-09-01"), d("2026-10-01")
	is, err := g.Accounting.IncomeStatement(g.admin, g.propID, &from, &to)
	must(t, err)
	eq(t, "net income", is.NetIncome, "-20500")
	if g.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'bank.settled'`) != 2 {
		t.Fatal("settlements are audited")
	}
}

func (g *guestFx) balanceOf(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, g.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.code = $2`, g.propID, code).Scan(&s))
	return dec(s)
}

func TestSettlementsNeedTheirPermissionsAndAMoneyInLine(t *testing.T) {
	g := setupGuests(t)
	g.pay(t, "k1", "CARD", "400000", "AUTH-1")
	g.closeDay(t)
	st := g.importStatement(t, "0", "-300", "date,description,amount\n2026-10-01,Bank charge,-300\n")
	lines, err := g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "CARD")
	must(t, err)
	viewer := g.User(t, g.tenantID, g.propID, auth.PermBankView)
	if got, err := g.BankRec.SettlementLines(viewer, g.propID, st.ID, "CARD"); err != nil || len(got) != 1 {
		t.Fatalf("a viewer without accounting.view sees the payments waiting: %v %+v", err, got)
	}
	_, err = g.BankRec.Settle(viewer, g.propID, st.ID, st.Lines[0].ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{lines[0].JournalLineID}, FeeAccountID: g.acc["6130"]})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = g.BankRec.Settle(g.admin, g.propID, st.ID, st.Lines[0].ID, bankrec.SettleInput{AccountKey: "CARD", JournalLineIDs: []int64{lines[0].JournalLineID}, FeeAccountID: g.acc["6130"]})
	wantCode(t, err, "VALIDATION_FAILED") // money out
	_, err = g.BankRec.SettlementLines(g.admin, g.propID, st.ID, "CASH")
	wantCode(t, err, "VALIDATION_FAILED")
	if g.Count(t, `SELECT count(*) FROM card_settlements`) != 0 {
		t.Fatal("nothing was settled")
	}
}
