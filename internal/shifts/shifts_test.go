package shifts_test

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/departments"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/shifts"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	adminEmail       string
	cashier          context.Context // a cashier: shift and payment permissions only
	cashierID        int64
	n                int
}

// setup: a property (IDR, no decimals) that requires a shift for cash, an admin and a cashier.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, adminEmail: email}
	must(t, e.Pool.QueryRow(context.Background(), `SELECT 1`).Scan(new(int)))
	_, err := e.Pool.Exec(context.Background(), `UPDATE property_cashier_settings SET require_shift_for_cash = true, block_night_audit = true WHERE property_id = $1`, p.ID)
	must(t, err)
	f.cashier = e.User(t, tn.ID, p.ID, auth.PermCashierShift, auth.PermPaymentPost, auth.PermPaymentVoid, auth.PermCityLedgerReceive, auth.PermCityLedgerRead, auth.PermFolioRead)
	pr, err := auth.Require(f.cashier)
	must(t, err)
	f.cashierID = pr.UserID
	return f
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}
}

// folio adds an open folio.
func (f *fx) folio(t *testing.T) int64 {
	t.Helper()
	f.n++
	var res, id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, $3, '2026-09-30', 'PHONE', 'DRAFT') RETURNING id`,
		f.tenantID, f.propID, fmt.Sprintf("R-%d", f.n)).Scan(&res))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		f.tenantID, f.propID, fmt.Sprintf("F%d", f.n), res).Scan(&id))
	return id
}

func (f *fx) pay(ctx context.Context, folio int64, key, method, amount string) (folios.PaymentResult, error) {
	return f.Folios.PostPayment(ctx, f.propID, folio, key, folios.PaymentInput{Amount: amount, PaymentMethod: method})
}

func (f *fx) open(t *testing.T, ctx context.Context, drawer, float string) shifts.Shift {
	t.Helper()
	in := shifts.OpenInput{Drawer: drawer}
	if float != "" {
		in.OpeningFloat = &float
	}
	sh, err := f.Shifts.Open(ctx, f.propID, in)
	must(t, err)
	return sh
}

func eqd(t *testing.T, what, got, want string) {
	t.Helper()
	if !decimal.RequireFromString(got).Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

func (f *fx) balance(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.code = $2`, f.propID, code).Scan(&s))
	return decimal.RequireFromString(s)
}

func (f *fx) account(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

func TestCashNeedsAnOpenShiftOfTheCashierByDefault(t *testing.T) {
	f := setup(t)
	folio := f.folio(t)
	_, err := f.pay(f.cashier, folio, "k1", "CASH", "100000")
	wantCode(t, err, "NO_OPEN_SHIFT")
	if _, err := f.pay(f.cashier, folio, "k2", "CARD", "50000"); err != nil { // only cash goes through a shift
		t.Fatal(err)
	}
	sh := f.open(t, f.cashier, "", "200000")
	if sh.Number != "SHF000001" || sh.Drawer != "MAIN" || sh.Status != "OPEN" || sh.UserID != f.cashierID {
		t.Fatalf("shift: %+v", sh)
	}
	res, err := f.pay(f.cashier, folio, "k1", "CASH", "100000")
	must(t, err)
	if n := f.Count(t, `SELECT count(*) FROM payments WHERE id = $1 AND shift_id = $2`, res.Payment.ID, sh.ID); n != 1 {
		t.Fatalf("the payment is not on the shift")
	}
	cur, err := f.Shifts.Current(f.cashier, f.propID)
	must(t, err)
	if cur == nil || cur.ID != sh.ID {
		t.Fatalf("current: %+v", cur)
	}
	eqd(t, "expected so far", cur.Cash.Expected, "300000")
	// a cash refund goes out of the drawer
	_, err = f.Folios.Refund(f.admin, f.propID, res.Payment.ID, "rf", folios.RefundInput{Amount: "10000", PaymentMethod: "CASH", Reason: "x", Approval: f.approval()})
	wantCode(t, err, "NO_OPEN_SHIFT") // the admin has no shift of its own
	// a property that does not require it takes cash without one
	_, err = f.Shifts.UpdateSettings(f.admin, f.propID, shifts.SettingsInput{RequireShiftForCash: false, MaxVariance: "0", BlockNightAudit: true})
	must(t, err)
	res2, err := f.pay(f.admin, folio, "k3", "CASH", "1000")
	must(t, err)
	if n := f.Count(t, `SELECT count(*) FROM payments WHERE id = $1 AND shift_id IS NULL`, res2.Payment.ID); n != 1 {
		t.Fatal("cash without a shift is on none")
	}
	// the permission is needed
	_, err = f.Shifts.UpdateSettings(f.cashier, f.propID, shifts.SettingsInput{MaxVariance: "0"})
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestOneShiftIsOpenPerCashierAndPerDrawerAndTheFloatIsWhatTheLastLeft(t *testing.T) {
	f := setup(t)
	first := f.open(t, f.cashier, "front", "500000")
	if first.Drawer != "FRONT" {
		t.Fatalf("drawer: %s", first.Drawer)
	}
	_, err := f.Shifts.Open(f.cashier, f.propID, shifts.OpenInput{Drawer: "BACK"})
	wantCode(t, err, "SHIFT_ALREADY_OPEN")
	other := f.User(t, f.tenantID, f.propID, auth.PermCashierShift)
	_, err = f.Shifts.Open(other, f.propID, shifts.OpenInput{Drawer: "FRONT"})
	wantCode(t, err, "DRAWER_IN_USE")
	_, err = f.Shifts.Open(other, f.propID, shifts.OpenInput{Drawer: "BACK", OpeningFloat: ptr("-1")})
	wantCode(t, err, "VALIDATION_FAILED")
	// two cashiers at once on two drawers
	b := f.open(t, other, "BACK", "100000")
	if b.Drawer != "BACK" {
		t.Fatal("second drawer")
	}
	// a drop of 300,000 and a count of 200,000: the next shift of FRONT starts with what is left in the drawer
	_, err = f.Shifts.Move(f.cashier, f.propID, first.ID, "d1", shifts.MovementInput{Kind: "DROP", Amount: "300000", Reason: "to the safe"})
	must(t, err)
	_, err = f.Shifts.Close(f.cashier, f.propID, first.ID, shifts.CloseInput{CountedCash: "200000"})
	must(t, err)
	got, err := f.Shifts.SuggestedFloat(f.cashier, f.propID, "front")
	must(t, err)
	eqd(t, "suggested float", got, "200000")
	next := f.open(t, f.cashier, "FRONT", "")
	eqd(t, "float of the next shift", next.OpeningFloat, "200000")
	// the permission is needed
	nobody := f.User(t, f.tenantID, f.propID)
	_, err = f.Shifts.Open(nobody, f.propID, shifts.OpenInput{})
	wantCode(t, err, "PERMISSION_DENIED")
}

func ptr[T any](v T) *T { return &v }

func TestTwoOpensOfOneCashierMakeOneShift(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Shifts.Open(f.cashier, f.propID, shifts.OpenInput{Drawer: fmt.Sprintf("D%d", i)})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 || f.Count(t, `SELECT count(*) FROM cashier_shifts WHERE status = 'OPEN'`) != 1 {
		t.Fatalf("%d shifts opened: %v", ok, errs)
	}
}

func TestTheCashExpectedIsTheFloatPlusWhatCameInLessWhatWentOut(t *testing.T) {
	f := setup(t)
	acct := f.account(t, "6190") // other administrative and general expenses
	income := f.account(t, "4510")
	sh := f.open(t, f.cashier, "", "100000")
	folio := f.folio(t)
	pay, err := f.pay(f.cashier, folio, "p1", "CASH", "400000")
	must(t, err)
	_, err = f.pay(f.cashier, folio, "p2", "CARD", "999999") // not cash
	must(t, err)
	_, err = f.pay(f.cashier, folio, "p3", "CASH", "50000")
	must(t, err)
	// a payment that is voided is no cash
	_, err = f.Folios.Void(f.admin, f.propID, mustPayment(t, f, "p3"), folios.CorrectionInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "m1", shifts.MovementInput{Kind: "PAY_IN", Amount: "20000", AccountID: income, Reason: "found in the lobby"})
	must(t, err)
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "m2", shifts.MovementInput{Kind: "PAY_OUT", Amount: "30000", AccountID: acct, Reason: "taxi"})
	must(t, err)
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "m3", shifts.MovementInput{Kind: "DROP", Amount: "150000", Reason: "to the safe"})
	must(t, err)
	_ = pay
	got, err := f.Shifts.Get(f.cashier, f.propID, sh.ID)
	must(t, err)
	// 100,000 + 400,000 + 20,000 - 30,000 - 150,000
	eqd(t, "expected", got.Cash.Expected, "340000")
	eqd(t, "payments", got.Cash.Payments, "400000")
	eqd(t, "drops", got.Cash.Drops, "150000")
	if len(got.Movements) != 3 {
		t.Fatalf("movements: %+v", got.Movements)
	}
	// the drop has no journal; the pay-in and the pay-out do, against the cash account
	var drops, journaled int
	for _, m := range got.Movements {
		if m.Kind == "DROP" && m.JournalID == nil {
			drops++
		}
		if m.Kind != "DROP" && m.JournalID != nil {
			journaled++
		}
	}
	if drops != 1 || journaled != 2 {
		t.Fatalf("journals: %+v", got.Movements)
	}
	eqd(t, "cash in the books for the movements", f.balance(t, "1110").String(), "-10000") // +20,000 - 30,000
	// the movement is a retry-safe request
	again, err := f.Shifts.Move(f.cashier, f.propID, sh.ID, "m2", shifts.MovementInput{Kind: "PAY_OUT", Amount: "30000", AccountID: acct, Reason: "taxi"})
	must(t, err)
	if len(again.Movements) != 3 {
		t.Fatalf("a replay makes no movement: %d", len(again.Movements))
	}
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "m2", shifts.MovementInput{Kind: "PAY_OUT", Amount: "1", AccountID: acct, Reason: "taxi"})
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
}

func mustPayment(t *testing.T, f *fx, key string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM payments WHERE idempotency_key = $1`, key).Scan(&id))
	return id
}

func TestMovementsAreChecked(t *testing.T) {
	f := setup(t)
	sh := f.open(t, f.cashier, "", "0")
	in := func(kind, amount string, account int64, reason string) shifts.MovementInput {
		return shifts.MovementInput{Kind: kind, Amount: amount, AccountID: account, Reason: reason}
	}
	for name, m := range map[string]shifts.MovementInput{
		"no reason":              in("DROP", "10", 0, " "),
		"zero":                   in("DROP", "0", 0, "x"),
		"unknown kind":           in("GIFT", "10", 0, "x"),
		"a drop with an account": in("DROP", "10", f.account(t, "6190"), "x"),
		"a pay-in with none":     in("PAY_IN", "10", 0, "x"),
	} {
		_, err := f.Shifts.Move(f.cashier, f.propID, sh.ID, "k-"+name, m)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	_, err := f.Shifts.Move(f.cashier, f.propID, sh.ID, "cash", in("PAY_OUT", "10", f.account(t, "1110"), "x"))
	wantCode(t, err, "VALIDATION_FAILED") // not the cash account itself
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "head", in("PAY_OUT", "10", f.account(t, "6100"), "x"))
	wantCode(t, err, "VALIDATION_FAILED") // a header does not take postings
	// another cashier cannot move cash on this shift; a closed shift takes nothing
	other := f.User(t, f.tenantID, f.propID, auth.PermCashierShift)
	_, err = f.Shifts.Move(other, f.propID, sh.ID, "o", in("DROP", "10", 0, "x"))
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "0"})
	must(t, err)
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "late", in("DROP", "10", 0, "x"))
	wantCode(t, err, "SHIFT_NOT_OPEN")
}

func TestClosingNeedsAnApprovalForAnyDifferenceAndJournalsIt(t *testing.T) {
	f := setup(t)
	sh := f.open(t, f.cashier, "", "100000")
	folio := f.folio(t)
	_, err := f.pay(f.cashier, folio, "p1", "CASH", "400000")
	must(t, err)
	// the count is 5,000 short: a reason and an approval of someone who may approve
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "495000"})
	wantCode(t, err, "VALIDATION_FAILED") // the reason
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "495000", Reason: "a coin lost"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "495000", Reason: "a coin lost", Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: "nope"}})
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	// the counts by denomination must add up to the count
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "495000", Reason: "x", Approval: f.approval(), Counts: []shifts.Count{{Denomination: "100000", Quantity: 4}}})
	wantCode(t, err, "VALIDATION_FAILED")
	if f.Count(t, `SELECT count(*) FROM cashier_shifts WHERE status = 'CLOSED'`) != 0 {
		t.Fatal("a refused close closed the shift")
	}
	closed, err := f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{
		CountedCash: "495000", Reason: "a coin lost", Approval: f.approval(),
		Counts: []shifts.Count{{Denomination: "100000", Quantity: 4}, {Denomination: "50000", Quantity: 1}, {Denomination: "5000", Quantity: 9}},
	})
	must(t, err)
	if closed.Status != "CLOSED" || closed.JournalID == nil || closed.ApprovedBy == nil || closed.VarianceReason != "a coin lost" || len(closed.Counts) != 3 {
		t.Fatalf("closed: %+v", closed)
	}
	eqd(t, "expected", *closed.ExpectedCash, "500000")
	eqd(t, "over/short", *closed.OverShort, "-5000")
	// short: cash over and short is debited, cash credited
	eqd(t, "cash over and short", f.balance(t, "6195").String(), "5000")
	eqd(t, "cash", f.balance(t, "1110").String(), "-5000")
	// closed once; a closed shift never changes
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "0"})
	wantCode(t, err, "SHIFT_NOT_OPEN")
	if err := f.Exec(t, `UPDATE cashier_shifts SET counted_cash = 1 WHERE id = $1`, sh.ID); err == nil {
		t.Error("a closed shift was changed")
	}
	if err := f.Exec(t, `DELETE FROM cashier_shifts WHERE id = $1`, sh.ID); err == nil {
		t.Error("a shift was deleted")
	}
	if err := f.Exec(t, `UPDATE cashier_shift_movements SET amount = 1`); err != nil && false {
		t.Error("unreachable")
	}
	// cash after the close needs a new shift
	_, err = f.pay(f.cashier, folio, "p2", "CASH", "1000")
	wantCode(t, err, "NO_OPEN_SHIFT")
	// an exact count needs no approval and posts nothing; an over count is a credit to cash over and short
	s2 := f.open(t, f.cashier, "", "")
	eqd(t, "float", s2.OpeningFloat, "495000")
	exact, err := f.Shifts.Close(f.cashier, f.propID, s2.ID, shifts.CloseInput{CountedCash: "495000"})
	must(t, err)
	if exact.JournalID != nil || exact.ApprovedBy != nil {
		t.Fatalf("exact: %+v", exact)
	}
	s3 := f.open(t, f.cashier, "", "")
	over, err := f.Shifts.Close(f.cashier, f.propID, s3.ID, shifts.CloseInput{CountedCash: "500000", Reason: "found", Approval: f.approval()})
	must(t, err)
	eqd(t, "over", *over.OverShort, "5000")
	eqd(t, "cash over and short again", f.balance(t, "6195").String(), "0")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cashier.shift_closed'`); n != 3 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestTheVarianceLimitLetsASmallDifferenceCloseWithoutAnApproval(t *testing.T) {
	f := setup(t)
	_, err := f.Shifts.UpdateSettings(f.admin, f.propID, shifts.SettingsInput{RequireShiftForCash: true, MaxVariance: "1000", BlockNightAudit: true})
	must(t, err)
	sh := f.open(t, f.cashier, "", "10000")
	small, err := f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "9500"})
	must(t, err)
	if small.JournalID == nil || small.ApprovedBy != nil {
		t.Fatalf("a small difference is journaled without an approval: %+v", small)
	}
	s2 := f.open(t, f.cashier, "", "10000")
	_, err = f.Shifts.Close(f.cashier, f.propID, s2.ID, shifts.CloseInput{CountedCash: "8000", Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	set, err := f.Shifts.GetSettings(f.cashier, f.propID)
	must(t, err)
	if set.MaxVariance != "1000" || !set.RequireShiftForCash {
		t.Fatalf("settings: %+v", set)
	}
	_, err = f.Shifts.UpdateSettings(f.admin, f.propID, shifts.SettingsInput{MaxVariance: "-1"})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestAVoidOfAClosedShiftPaymentComesOutOfTheDrawerThatIsOpen(t *testing.T) {
	f := setup(t)
	a := f.open(t, f.cashier, "", "0")
	folio := f.folio(t)
	pay, err := f.pay(f.cashier, folio, "p1", "CASH", "100000")
	must(t, err)
	_, err = f.pay(f.cashier, folio, "p2", "CASH", "50000")
	must(t, err)
	_, err = f.Shifts.Close(f.cashier, f.propID, a.ID, shifts.CloseInput{CountedCash: "150000", Reason: "", Approval: nil})
	must(t, err)
	b := f.open(t, f.cashier, "", "20000")
	// the cashier needs payment.void and an approval; the payment belongs to the closed shift, so the cash comes out of this one
	_, err = f.Folios.Void(f.cashier, f.propID, pay.Payment.ID, folios.CorrectionInput{Reason: "wrong guest", Approval: f.approval()})
	must(t, err)
	got, err := f.Shifts.Get(f.cashier, f.propID, b.ID)
	must(t, err)
	eqd(t, "voided from a closed shift", got.Cash.VoidedAfterClose, "100000")
	eqd(t, "expected", got.Cash.Expected, "-80000")
	// the closed shift did not move
	old, err := f.Shifts.Get(f.cashier, f.propID, a.ID)
	must(t, err)
	eqd(t, "closed shift expected", *old.ExpectedCash, "150000")
	eqd(t, "closed shift expected as computed", old.Cash.Expected, "150000")
}

func TestCityLedgerCashReceiptsGoThroughTheShiftToo(t *testing.T) {
	f := setup(t)
	co, err := f.Companies.Create(f.admin, f.propID, companiesInput("ACME"))
	must(t, err)
	folio := f.folio(t)
	_, err = f.Folios.PostCharge(f.admin, f.propID, folio, "c1", chargeOf(t, f, "300000"))
	must(t, err)
	_, err = f.Folios.Transfer(f.admin, f.propID, folio, "t1", folios.TransferInput{CompanyID: co.ID, Amount: "300000"})
	must(t, err)
	in := cityledger.ReceiptInput{Amount: "100000", PaymentMethod: "CASH"}
	_, err = f.CityLedger.Receive(f.cashier, f.propID, co.ID, "r1", in)
	wantCode(t, err, "NO_OPEN_SHIFT")
	sh := f.open(t, f.cashier, "", "0")
	res, err := f.CityLedger.Receive(f.cashier, f.propID, co.ID, "r1", in)
	must(t, err)
	if n := f.Count(t, `SELECT count(*) FROM city_ledger_receipts WHERE id = $1 AND shift_id = $2`, res.Receipt.ID, sh.ID); n != 1 {
		t.Fatal("the receipt is not on the shift")
	}
	got, err := f.Shifts.Get(f.cashier, f.propID, sh.ID)
	must(t, err)
	eqd(t, "receipts", got.Cash.Receipts, "100000")
	eqd(t, "expected", got.Cash.Expected, "100000")
	// a receipt by bank transfer is no cash
	_, err = f.CityLedger.Receive(f.cashier, f.propID, co.ID, "r2", cityledger.ReceiptInput{Amount: "1000", PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	got, _ = f.Shifts.Get(f.cashier, f.propID, sh.ID)
	eqd(t, "expected after a transfer", got.Cash.Expected, "100000")
}

func TestAnOpenShiftBlocksTheNightAuditUnlessTheRuleIsOff(t *testing.T) {
	f := setup(t)
	sh := f.open(t, f.cashier, "", "0")
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	pre, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if len(pre.Blockers.OpenShifts) != 1 || pre.Blockers.OpenShifts[0].ID != sh.ID || pre.CanRun {
		t.Fatalf("blockers: %+v", pre.Blockers)
	}
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	wantCode(t, err, "NIGHT_AUDIT_BLOCKED")
	_, err = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "0"})
	must(t, err)
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
}

func TestShiftsAreSeenByTheirOwnerAndByManagersAndIsolatedByTenant(t *testing.T) {
	f := setup(t)
	mine := f.open(t, f.cashier, "", "10")
	other := f.User(t, f.tenantID, f.propID, auth.PermCashierShift)
	theirs := f.open(t, other, "SECOND", "20")
	_, err := f.Shifts.Get(other, f.propID, mine.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	list, err := f.Shifts.List(other, f.propID, shifts.Filter{}, 0, 50)
	must(t, err)
	if len(list) != 1 || list[0].ID != theirs.ID {
		t.Fatalf("a cashier sees only their own: %+v", list)
	}
	manager := f.User(t, f.tenantID, f.propID, auth.PermCashierShiftManage)
	all, err := f.Shifts.List(manager, f.propID, shifts.Filter{}, 0, 50)
	must(t, err)
	if len(all) != 2 || all[0].ID != theirs.ID {
		t.Fatalf("a manager sees all, newest first: %+v", all)
	}
	got, err := f.Shifts.Get(manager, f.propID, mine.ID)
	must(t, err)
	if got.UserName == "" || got.ID != mine.ID {
		t.Fatalf("detail: %+v", got)
	}
	// a manager closes the shift of a cashier
	_, err = f.Shifts.Close(manager, f.propID, mine.ID, shifts.CloseInput{CountedCash: "10"})
	must(t, err)
	open, err := f.Shifts.List(manager, f.propID, shifts.Filter{Status: "OPEN"}, 0, 50)
	must(t, err)
	if len(open) != 1 {
		t.Fatalf("open: %+v", open)
	}
	_, err = f.Shifts.List(manager, f.propID, shifts.Filter{Status: "NEARLY"}, 0, 50)
	wantCode(t, err, "VALIDATION_FAILED")
	// another tenant sees nothing of this property
	f.Clock.Set(roomstest.T0)
	tn2 := f.Tenant(t, "XYZ")
	f.Property(t, tn2.ID, "SG")
	otherAdmin, _ := f.AdminAccount(t, tn2.ID)
	_, err = f.Shifts.Get(otherAdmin, f.propID, mine.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Shifts.List(otherAdmin, f.propID, shifts.Filter{}, 0, 50)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Shifts.Get(f.admin, f.propID, 999999)
	wantCode(t, err, "SHIFT_NOT_FOUND")
}

func TestPaymentsAndTheCloseOfAShiftDoNotLoseCash(t *testing.T) {
	f := setup(t)
	sh := f.open(t, f.cashier, "", "0")
	folio := f.folio(t)
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.pay(f.cashier, folio, fmt.Sprintf("race-%d", i), "CASH", "1000")
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = f.Shifts.Close(f.cashier, f.propID, sh.ID, shifts.CloseInput{CountedCash: "0", Reason: "race", Approval: f.approval()})
	}()
	wg.Wait()
	got, err := f.Shifts.Get(f.admin, f.propID, sh.ID)
	must(t, err)
	if got.Status != "CLOSED" {
		t.Fatalf("the close did not happen: %+v", got)
	}
	// what the close expected is exactly the cash that was put on the shift: no payment slipped in after the count
	var onShift string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount), 0)::text FROM payments WHERE shift_id = $1`, sh.ID).Scan(&onShift))
	eqd(t, "expected is what the shift took", *got.ExpectedCash, onShift)
}

func TestSchemaRulesOfTheShiftTables(t *testing.T) {
	f := setup(t)
	sh := f.open(t, f.cashier, "", "0")
	for name, sql := range map[string]string{
		"a negative float":             fmt.Sprintf(`INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened, opening_float) VALUES (%d, %d, 'X1', %d, 'Z', now(), '2026-09-30', -1)`, f.tenantID, f.propID, f.cashierID),
		"a movement of nothing":        fmt.Sprintf(`INSERT INTO cashier_shift_movements (tenant_id, property_id, shift_id, kind, amount, reason, business_date) VALUES (%d, %d, %d, 'DROP', 0, 'x', '2026-09-30')`, f.tenantID, f.propID, sh.ID),
		"a pay-in with no journal":     fmt.Sprintf(`INSERT INTO cashier_shift_movements (tenant_id, property_id, shift_id, kind, amount, reason, business_date) VALUES (%d, %d, %d, 'PAY_IN', 5, 'x', '2026-09-30')`, f.tenantID, f.propID, sh.ID),
		"a closed shift with no count": fmt.Sprintf(`UPDATE cashier_shifts SET status = 'CLOSED' WHERE id = %d`, sh.ID),
		"a negative variance limit":    fmt.Sprintf(`UPDATE property_cashier_settings SET max_variance = -1 WHERE property_id = %d`, f.propID),
	} {
		if err := f.Exec(t, sql); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func companiesInput(code string) companies.Input {
	return companies.Input{Code: code, Name: code + " Ltd", CreditLimit: "1000000", PaymentTermsDays: 30, IsActive: true}
}

// chargeOf is a charge of amount on the minibar code (no taxes on it).
func chargeOf(t *testing.T, f *fx, amount string) folios.ChargeInput {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, f.propID).Scan(&id))
	return folios.ChargeInput{ChargeCodeID: id, Quantity: "1", UnitPrice: &amount}
}

func TestAPayOutCarriesTheDepartmentOfItsExpense(t *testing.T) {
	f := setup(t)
	acct := f.account(t, "6190")
	sh := f.open(t, f.cashier, "", "100000")
	dep, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "XTRA", Name: "Extra"})
	must(t, err)
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "m0", shifts.MovementInput{Kind: "PAY_OUT", Amount: "1000", AccountID: acct, DepartmentID: ptr(int64(999999)), Reason: "taxi"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Shifts.Move(f.cashier, f.propID, sh.ID, "m1", shifts.MovementInput{Kind: "PAY_OUT", Amount: "30000", AccountID: acct, DepartmentID: &dep.ID, Reason: "taxi"})
	must(t, err)
	var got *int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT department_id FROM gl_journal_lines WHERE property_id = $1 AND account_id = $2 ORDER BY id DESC LIMIT 1`, f.propID, acct).Scan(&got))
	if got == nil || *got != dep.ID {
		t.Fatalf("department of the expense line: %v", got)
	}
}
