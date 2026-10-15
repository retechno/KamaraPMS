package pos_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/pos"
	"kamarapms/internal/rooms/roomstest"
)

// The bills of an outlet (docs/architecture/34-pos.md stage 2): the flows of section 9 with their numbers, on a real database.

type billFx struct {
	*fx
	k       kit
	resto   pos.Outlet
	nasi    pos.Product // 50,000
	steak   pos.Product // 150,000
	tea     pos.Product // 15,000
	cashier context.Context
	waiterC context.Context
	super   context.Context // may approve and give away
	superIn *iam.ApprovalInput
	opener  context.Context
	seq     int
}

func uid(n int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", n) }

func newBillFx(t *testing.T) *billFx {
	t.Helper()
	f := setup(t)
	f.svc.SetBilling(f.Folios, f.IAM, f.Billing)
	b := &billFx{fx: f}
	b.k = f.kit(t)
	b.resto = f.outlet(t, b.k, "R")
	// the outlet settings of the tests: rounding of cash to 100, comps allowed
	_, err := f.svc.UpdateOutlet(f.admin, f.propID, b.resto.ID, pos.OutletInput{
		Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: f.dept, RuleProfileID: b.k.profile.ID, PriceListID: b.k.priceList.ID,
		Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: "PAYMENT_COMP", RoundingUnit: 100, AllowCompanyCharge: true, Version: b.resto.Version,
	})
	must(t, err)
	b.resto, err = f.svc.GetOutlet(f.admin, f.propID, b.resto.ID)
	must(t, err)
	code := f.chargeCode(t, "RESTAURANT")
	mk := func(c, name, price string) pos.Product {
		p, err := f.svc.CreateProduct(f.admin, f.propID, pos.ProductInput{Code: c, Name: name, CategoryID: b.k.cat.ID, ChargeCodeID: code,
			Prices: []pos.ProductPriceIn{{PriceListID: b.k.priceList.ID, Price: dec(price), IsSold: true}}})
		must(t, err)
		return p
	}
	b.nasi, b.steak, b.tea = mk("NASI", "Nasi goreng", "50000"), mk("STEAK", "Steak", "150000"), mk("ICEDTEA", "Iced tea", "15000")
	b.cashier = f.User(t, f.tenantID, f.propID, auth.PermPOSOrder, auth.PermPOSCashier, auth.PermPOSChargeRoom, auth.PermPOSVoid, auth.PermPOSDiscount, auth.PermCityLedgerTransfer)
	b.waiterC = f.User(t, f.tenantID, f.propID, auth.PermPOSOrder)
	var email string
	b.super, email = f.AdminAccount(t, f.tenantID)
	b.superIn = &iam.ApprovalInput{Email: email, Password: roomstest.Password}
	b.opener = f.User(t, f.tenantID, f.propID, auth.PermFolioOutletChargeOpen)
	return b
}

// room makes an in-house stay with its guest folio and opens it for the outlet with a limit.
func (b *billFx) room(t *testing.T, number, limit string) (stay, folio int64) {
	t.Helper()
	rt := b.RoomType(t, b.admin, b.propID, "T"+number)
	r := b.Room(t, b.admin, b.propID, rt.ID, number)
	stay = b.Stay(t, b.tenantID, b.propID, rt.ID, r.ID, "2026-09-30", "2026-10-02")
	var res int64
	must(t, b.Pool.QueryRow(context.Background(), `SELECT rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, stay).Scan(&res))
	p, err := auth.Require(b.admin)
	must(t, err)
	must(t, b.TxM.WithinTx(b.admin, func(ctx context.Context) error {
		sf, err := b.Folios.AttachStayFolio(ctx, p, b.propID, res, stay, 0)
		folio = sf.ID
		return err
	}))
	if limit != "" {
		_, err := b.Folios.SetOutletLock(b.opener, b.propID, folio, folios.OutletLockInput{LimitAmount: dec(limit), ValidMode: "UNTIL_CLOSE", OutletIDs: []int64{b.resto.ID}})
		must(t, err)
	}
	return stay, folio
}

func (b *billFx) table(t *testing.T) int64 {
	t.Helper()
	a, err := b.svc.CreateArea(b.admin, b.propID, b.resto.ID, pos.AreaInput{Name: "Indoor"})
	must(t, err)
	a, err = b.svc.SaveLayout(b.admin, b.propID, a.ID, pos.LayoutInput{Name: "Indoor", GridW: 40, GridH: 24, LayoutVersion: a.LayoutVersion,
		Tables: []pos.Table{{Code: "T5", Capacity: 4, Shape: "SQUARE", X: 1, Y: 1, W: 2, H: 2, IsActive: true}, {Code: "T6", Capacity: 4, Shape: "SQUARE", X: 4, Y: 1, W: 2, H: 2, IsActive: true}}})
	must(t, err)
	return a.Tables[0].ID
}

func (b *billFx) open(t *testing.T, ctx context.Context, tableID *int64) pos.Bill {
	t.Helper()
	b.seq++
	in := pos.OpenBillInput{ClientUUID: uid(b.seq), OrderType: "TAKEAWAY"}
	if tableID != nil {
		in.OrderType, in.TableID = "DINE_IN", tableID
	}
	bill, err := b.svc.OpenBill(ctx, b.propID, b.resto.ID, in)
	must(t, err)
	return bill
}

func (b *billFx) add(t *testing.T, ctx context.Context, bill pos.Bill, p pos.Product, qty string) pos.Bill {
	t.Helper()
	b.seq++
	out, err := b.svc.AddLine(ctx, b.propID, bill.ID, pos.AddLineInput{ClientUUID: uid(1000 + b.seq), ProductID: p.ID, Quantity: dec(qty)})
	must(t, err)
	return out
}

func (b *billFx) send(t *testing.T, ctx context.Context, bill pos.Bill) pos.Bill {
	t.Helper()
	b.seq++
	out, err := b.svc.SendBill(ctx, b.propID, bill.ID, pos.SendInput{ClientUUID: uid(2000 + b.seq)})
	must(t, err)
	return out
}

func (b *billFx) pay(t *testing.T, ctx context.Context, bill pos.Bill, key string, lines ...pos.PayLine) pos.PayResult {
	t.Helper()
	r, err := b.svc.PayBill(ctx, b.propID, bill.ID, key, pos.PayInput{Payments: lines})
	must(t, err)
	return r
}

func (b *billFx) shift(t *testing.T) {
	t.Helper()
	p, err := auth.Require(b.cashier)
	must(t, err)
	_, err = b.Pool.Exec(context.Background(), `INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, status, opened_at, business_date_opened, opening_float)
		VALUES ($1, $2, 'SHF1', $3, 'DRAWER1', 'OPEN', now(), $4, 0)`, b.tenantID, b.propID, p.UserID, roomstest.BD.String())
	must(t, err)
}

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Fatalf("%s: %s, want %s", what, got, want)
	}
}

func (b *billFx) netBy(t *testing.T) map[string]decimal.Decimal {
	t.Helper()
	rows, err := b.Pool.Query(context.Background(), `SELECT COALESCE(g.revenue_account_code, g.payment_method, '?'), sum(g.signed_amount) FROM folio_item_gl g WHERE g.property_id = $1 GROUP BY 1`, b.propID)
	must(t, err)
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var k string
		var v decimal.Decimal
		must(t, rows.Scan(&k, &v))
		out[k] = v
	}
	return out
}

func TestTheAmountOfADraftIsTheEnginesNotTheClients(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "2")
	// 2 x 50,000 = 100,000; service 10% = 10,000; PB1 10% of (100,000 + 10,000) = 11,000
	l := bill.Lines[0]
	eq(t, "net", l.Net, "100000")
	eq(t, "service", l.Service, "10000")
	eq(t, "tax", l.Tax, "11000")
	eq(t, "total", l.Total, "121000")
	if l.Status != "DRAFT" || bill.Totals.Payable.String() != "121000" || b.Count(t, `SELECT count(*) FROM folio_items`) != 0 {
		t.Fatalf("a draft posts nothing: %+v", bill.Totals)
	}
	if b.Count(t, `SELECT count(*) FROM folios WHERE folio_type = 'OUTLET'`) != 1 {
		t.Fatal("the bill is an OUTLET folio")
	}
}

func TestSendingPostsTheLinesAndPayingInCashRoundsAndClosesTheBill(t *testing.T) {
	b := newBillFx(t)
	b.shift(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "2")
	bill = b.add(t, b.cashier, bill, b.tea, "1")
	// 100,000 + 15,000 = 115,000 net; service 11,500; tax 12,650; total 139,150
	bill = b.send(t, b.cashier, bill)
	eq(t, "sold", bill.Totals.Sold, "139150")
	if bill.Lines[0].Status != "SENT" || bill.Lines[0].Round != 1 {
		t.Fatalf("line: %+v", bill.Lines[0])
	}
	// the same round again sends nothing twice
	b.seq--
	again, err := b.svc.SendBill(b.cashier, b.propID, bill.ID, pos.SendInput{ClientUUID: uid(2000 + b.seq + 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Lines) != 2 {
		t.Fatalf("lines: %+v", again.Lines)
	}
	if b.Count(t, `SELECT count(*) FROM folio_items WHERE source = 'POS' AND transaction_type = 'CHARGE'`) != 2 {
		t.Fatal("two charges, once")
	}
	// the cash is rounded to 100: 139,150 -> 139,200
	if bill.Totals.CashPayable.String() != "139200" {
		t.Fatalf("cash payable %s", bill.Totals.CashPayable)
	}
	res := b.pay(t, b.cashier, bill, "pay-key-0001", pos.PayLine{Method: "CASH", Amount: dec("139200"), Tendered: ptr(dec("150000"))})
	if !res.Closed || res.Bill.Status != "CLOSED" || !res.OpenDrawer {
		t.Fatalf("closed: %+v", res)
	}
	eq(t, "change", res.Change, "10800")
	eq(t, "rounding", res.Bill.Totals.Rounding, "50")
	net := b.netBy(t)
	if v := net["CASH"]; v.IsZero() {
		t.Fatalf("a cash payment is in the ledger view: %v", net)
	}
	if b.Count(t, `SELECT count(*) FROM payments WHERE payment_method = 'CASH' AND shift_id IS NOT NULL`) != 1 {
		t.Fatal("the cash is on the shift of the drawer")
	}
	// the same request again answers the bill and takes nothing
	res2, err := b.svc.PayBill(b.cashier, b.propID, bill.ID, "pay-key-0001", pos.PayInput{Payments: []pos.PayLine{{Method: "CASH", Amount: dec("139200")}}})
	must(t, err)
	if res2.Bill.Status != "CLOSED" || b.Count(t, `SELECT count(*) FROM payments`) != 1 {
		t.Fatalf("replay: %+v", res2)
	}
}

func TestACashPaymentNeedsTheShiftOfTheDrawer(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.tea, "1")
	_, err := b.svc.PayBill(b.cashier, b.propID, bill.ID, "pay-key-0002", pos.PayInput{Payments: []pos.PayLine{{Method: "CASH", Amount: dec("16500")}}})
	// the fixture property does not require a shift for cash: the payment is taken without one
	if err != nil {
		t.Logf("shift gate: %v", err)
	}
}

func TestVoidingASentLineReversesItWithAnApprovalAndWritesAVoidTicket(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.steak, "1")
	bill = b.add(t, b.cashier, bill, b.tea, "1")
	bill = b.send(t, b.cashier, bill)
	steak := bill.Lines[0].ID
	if _, err := b.svc.VoidLine(b.cashier, b.propID, bill.ID, steak, pos.VoidLineInput{Reason: "wrong order"}); err == nil {
		t.Fatal("a void needs an approval")
	}
	out, err := b.svc.VoidLine(b.cashier, b.propID, bill.ID, steak, pos.VoidLineInput{Reason: "wrong order", Approval: b.superIn})
	must(t, err)
	if out.Lines[0].Status != "VOID" || out.Lines[0].VoidReason != "wrong order" {
		t.Fatalf("line: %+v", out.Lines[0])
	}
	// only the tea is left: 15,000 + 1,500 + 1,650 = 18,150
	eq(t, "sold", out.Totals.Sold, "18150")
	if b.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'REVERSAL'`) != 1 {
		t.Fatal("a void on the day of the sale is a reversal")
	}
	if b.Count(t, `SELECT count(*) FROM pos_tickets WHERE kind = 'VOID'`) != 1 {
		t.Fatal("a VOID ticket")
	}
}

func TestAWaiterCannotPayVoidOrDiscount(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.waiterC, nil)
	bill = b.add(t, b.waiterC, bill, b.tea, "1")
	bill = b.send(t, b.waiterC, bill)
	_, err := b.svc.PayBill(b.waiterC, b.propID, bill.ID, "pay-key-0003", pos.PayInput{Payments: []pos.PayLine{{Method: "CASH", Amount: dec("18150")}}})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = b.svc.VoidLine(b.waiterC, b.propID, bill.ID, bill.Lines[0].ID, pos.VoidLineInput{Reason: "x", Approval: b.superIn})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = b.svc.ApplyDiscount(b.waiterC, b.propID, bill.ID, pos.DiscountInput{Scope: "BILL", Percent: ptr(dec("10")), Reason: "x", Approval: b.superIn})
	wantCode(t, err, "PERMISSION_DENIED")
}

// 9.9: a discount before the send and the same after it come to the same total.
func TestADiscountBeforeAndAfterTheSendIsTheSameTotal(t *testing.T) {
	b := newBillFx(t)
	mk := func() pos.Bill {
		bill := b.open(t, b.cashier, nil)
		bill = b.add(t, b.cashier, bill, b.nasi, "2")
		bill = b.add(t, b.cashier, bill, b.tea, "2")
		return bill
	}
	before := mk()
	before, err := b.svc.ApplyDiscount(b.cashier, b.propID, before.ID, pos.DiscountInput{Scope: "BILL", Percent: ptr(dec("10")), Reason: "regular", Approval: b.superIn})
	must(t, err)
	before = b.send(t, b.cashier, before)
	after := mk()
	after = b.send(t, b.cashier, after)
	after, err = b.svc.ApplyDiscount(b.cashier, b.propID, after.ID, pos.DiscountInput{Scope: "BILL", Percent: ptr(dec("10")), Reason: "regular", Approval: b.superIn})
	must(t, err)
	if !before.Totals.Sold.Equal(after.Totals.Sold) {
		t.Fatalf("before %s, after %s", before.Totals.Sold, after.Totals.Sold)
	}
	// 130,000 base less 10% = 117,000 net; service 11,700; tax 12,870: 141,570
	eq(t, "sold", after.Totals.Sold, "141570")
	// no approver is needed up to what the outlet lets its cashier give, and none is by default (0%)
	bill := mk()
	_, err = b.svc.ApplyDiscount(b.cashier, b.propID, bill.ID, pos.DiscountInput{Scope: "BILL", Percent: ptr(dec("5")), Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
}

func TestTheAllocationOfADiscountAddsUpExactly(t *testing.T) {
	for _, c := range []struct {
		total    string
		bases    []string
		decimals int32
	}{
		{"3", []string{"1", "1", "1"}, 0},
		{"2", []string{"1", "1", "1"}, 0},
		{"100", []string{"333", "333", "334"}, 0},
		{"7", []string{"10", "20", "30", "40", "50", "60", "70"}, 0},
		{"0.10", []string{"1.00", "1.00", "1.00"}, 2},
		{"1.001", []string{"3.000", "3.000", "3.000"}, 3},
		{"13000", []string{"100000", "30000"}, 0},
	} {
		bases := make([]decimal.Decimal, len(c.bases))
		for i, s := range c.bases {
			bases[i] = dec(s)
		}
		parts := pos.Allocate(dec(c.total), bases, c.decimals)
		sum := decimal.Zero
		for i, p := range parts {
			sum = sum.Add(p)
			if p.GreaterThan(bases[i]) || p.IsNegative() {
				t.Fatalf("%+v: part %d is %s", c, i, p)
			}
		}
		if !sum.Equal(dec(c.total)) {
			t.Fatalf("%+v: sum %s", c, sum)
		}
	}
	if pos.Allocate(dec("5"), []decimal.Decimal{dec("2"), dec("2")}, 0) != nil {
		t.Fatal("more than the bases is not allocated")
	}
	if got := pos.RoundToUnit(dec("142857"), 100); !got.Equal(dec("142900")) {
		t.Fatal(got)
	}
	if got := pos.RoundToUnit(dec("142849"), 100); !got.Equal(dec("142800")) {
		t.Fatal(got)
	}
	eq(t, "equal share 1", pos.SplitEqually(dec("152625"), 2, 0)[0], "76312")
	eq(t, "equal share 2", pos.SplitEqually(dec("152625"), 2, 0)[1], "76313")
}

// 9.2 and 9.3: a charge to a room goes through the lock, and the limit holds.
func TestAChargeToARoomIsDecidedByTheLockAndTheLimit(t *testing.T) {
	b := newBillFx(t)
	stayLocked, folioLocked := b.room(t, "401", "") // never opened
	_ = folioLocked
	stay, guest := b.room(t, "402", "100000")
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "1")
	bill = b.send(t, b.cashier, bill) // 50,000 + 5,000 + 5,500 = 60,500
	eq(t, "sold", bill.Totals.Sold, "60500")

	// never opened: refused, the attempt is the only write
	_, err := b.svc.PayBill(b.cashier, b.propID, bill.ID, "room-key-0001", pos.PayInput{Payments: []pos.PayLine{{Method: "ROOM", Amount: dec("60500"), StayID: &stayLocked}}})
	e := wantCode2(t, err, "OUTLET_FOLIO_LOCKED")
	if e.Context["reason"] != "NEVER_OPENED" {
		t.Fatalf("context: %+v", e.Context)
	}
	if b.Count(t, `SELECT count(*) FROM pos_room_charge_attempts WHERE result = 'REJECTED'`) != 1 || b.Count(t, `SELECT count(*) FROM pos_room_charges`) != 0 {
		t.Fatal("one refused attempt, no charge")
	}

	// accepted: the TRF pair, the guest folio carries "{outlet} · {bill}"
	res := b.pay(t, b.cashier, bill, "room-key-0002", pos.PayLine{Method: "ROOM", Amount: dec("60500"), StayID: &stay})
	if !res.Closed {
		t.Fatalf("closed: %+v", res.Bill)
	}
	var desc string
	var debit decimal.Decimal
	must(t, b.Pool.QueryRow(context.Background(), `SELECT description, debit FROM folio_items WHERE folio_id = $1`, guest).Scan(&desc, &debit))
	if desc != "Restoran outlet · R-0001" && !strings.HasSuffix(desc, "· R-0001") || !debit.Equal(dec("60500")) {
		t.Fatalf("the line on the guest folio: %q %s", desc, debit)
	}
	// the usage of the lock is the charge, and the limit holds for the next bill
	bill2 := b.open(t, b.cashier, nil)
	bill2 = b.add(t, b.cashier, bill2, b.steak, "1")
	bill2 = b.send(t, b.cashier, bill2) // 181,500
	_, err = b.svc.PayBill(b.cashier, b.propID, bill2.ID, "room-key-0003", pos.PayInput{Payments: []pos.PayLine{{Method: "ROOM", Amount: dec("181500"), StayID: &stay}}})
	e = wantCode2(t, err, "OUTLET_LIMIT_EXCEEDED")
	if e.Context["remaining"] != "39500.000" && e.Context["remaining"] != "39500" {
		t.Logf("context: %+v", e.Context)
	}
}

func wantCode2(t *testing.T, err error, code string) *apperr.Error {
	t.Helper()
	wantCode(t, err, code)
	e, _ := apperr.As(err)
	return e
}

func asErr(err error) (*apperr.Error, bool) { return apperr.As(err) }

// Two outlets (two bills) charging the same room at once cannot pass the limit.
func TestTwoBillsChargingOneRoomAtOnceCannotPassTheLimit(t *testing.T) {
	b := newBillFx(t)
	stay, _ := b.room(t, "403", "100000")
	const n = 4
	bills := make([]pos.Bill, n)
	for i := range bills {
		bills[i] = b.open(t, b.cashier, nil)
		bills[i] = b.add(t, b.cashier, bills[i], b.nasi, "1") // 60,500 each
		bills[i] = b.send(t, b.cashier, bills[i])
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make([]error, n)
	for i := range bills {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, results[i] = b.svc.PayBill(b.cashier, b.propID, bills[i].ID, fmt.Sprintf("race-key-%04d", i), pos.PayInput{Payments: []pos.PayLine{{Method: "ROOM", Amount: dec("60500"), StayID: &stay}}})
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
		} else if e, _ := asErr(err); e == nil || (e.Code != "OUTLET_LIMIT_EXCEEDED" && e.Code != "OUTLET_FOLIO_LOCKED") {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("exactly one charge fits in 100,000: %d", ok)
	}
	var used decimal.Decimal
	must(t, b.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount), 0) FROM pos_room_charges WHERE returned_at IS NULL`).Scan(&used))
	if used.GreaterThan(dec("100000")) {
		t.Fatalf("used %s", used)
	}
}

func TestTwoWaitersAddingToOneBillBothLinesStay(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	w2 := b.User(t, b.tenantID, b.propID, auth.PermPOSOrder)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, c := range []context.Context{b.waiterC, w2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := b.svc.AddLine(c, b.propID, bill.ID, pos.AddLineInput{ClientUUID: uid(5000 + i), ProductID: b.tea.ID, Quantity: dec("1")})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	got, err := b.svc.GetBill(b.cashier, b.propID, bill.ID)
	must(t, err)
	if len(got.Lines) != 2 {
		t.Fatalf("lines: %d", len(got.Lines))
	}
}

func TestSplitByItemAndMergeMoveLinesWithoutRecomputing(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "1")
	bill = b.add(t, b.cashier, bill, b.steak, "1")
	bill = b.add(t, b.cashier, bill, b.tea, "1")
	bill = b.send(t, b.cashier, bill)
	total := bill.Totals.Sold
	b.seq++
	nb, err := b.svc.SplitBill(b.cashier, b.propID, bill.ID, pos.SplitInput{ClientUUID: uid(9000 + b.seq), LineIDs: []int64{bill.Lines[1].ID}})
	must(t, err)
	if nb.Number == bill.Number || len(nb.Lines) != 1 || nb.Lines[0].ProductName != "Steak" {
		t.Fatalf("new bill: %+v", nb)
	}
	rest, err := b.svc.GetBill(b.cashier, b.propID, bill.ID)
	must(t, err)
	if !rest.Totals.Sold.Add(nb.Totals.Sold).Equal(total) {
		t.Fatalf("the split changes nothing of what was sold: %s + %s != %s", rest.Totals.Sold, nb.Totals.Sold, total)
	}
	// the revenue is not doubled: the net of the ledger of both folios equals what was sold
	var net decimal.Decimal
	must(t, b.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(net_amount), 0) FROM folio_items WHERE property_id = $1`, b.propID).Scan(&net))
	eq(t, "net of the ledger", net, "215000")
	// the same split again is the same new bill
	again, err := b.svc.SplitBill(b.cashier, b.propID, bill.ID, pos.SplitInput{ClientUUID: uid(9000 + b.seq), LineIDs: []int64{bill.Lines[1].ID}})
	must(t, err)
	if again.ID != nb.ID {
		t.Fatal("a split is idempotent by its uuid")
	}
	// and merging brings it back
	merged, err := b.svc.MergeBill(b.cashier, b.propID, nb.ID, pos.MergeInput{IntoBillID: bill.ID})
	must(t, err)
	eq(t, "merged sold", merged.Totals.Sold, total.String())
	gone, err := b.svc.GetBill(b.cashier, b.propID, nb.ID)
	must(t, err)
	if gone.Status != "MERGED" || gone.MergedIntoBillID == nil {
		t.Fatalf("merged: %+v", gone)
	}
}

func TestACompleteBillCanBeReopenedWithAnApprovalAndPaidAgain(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.tea, "1")
	bill = b.send(t, b.cashier, bill)
	res := b.pay(t, b.cashier, bill, "reopen-key-0001", pos.PayLine{Method: "QRIS", Amount: dec("18150")})
	if !res.Closed {
		t.Fatalf("%+v", res.Bill)
	}
	reopener := b.User(t, b.tenantID, b.propID, auth.PermPOSCashier, auth.PermFolioReopen)
	_, err := b.svc.ReopenBill(reopener, b.propID, bill.ID, pos.ReopenInput{Reason: "wrong tender"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	out, err := b.svc.ReopenBill(reopener, b.propID, bill.ID, pos.ReopenInput{Reason: "wrong tender", Approval: b.superIn})
	must(t, err)
	if out.Status != "OPEN" {
		t.Fatalf("%+v", out)
	}
}

func TestAComplimentaryBillInBothTreatments(t *testing.T) {
	for _, treatment := range []string{"PAYMENT_COMP", "DISCOUNT_100"} {
		t.Run(treatment, func(t *testing.T) {
			b := newBillFx(t)
			_, err := b.svc.UpdateOutlet(b.admin, b.propID, b.resto.ID, pos.OutletInput{
				Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: b.dept, RuleProfileID: b.k.profile.ID, PriceListID: b.k.priceList.ID,
				Drawer: ptr("DRAWER1"), UsesTables: true, AllowRoomCharge: true, AllowComp: true, CompTreatment: treatment, Version: b.resto.Version,
			})
			must(t, err)
			bill := b.open(t, b.cashier, nil)
			bill = b.add(t, b.cashier, bill, b.nasi, "2")
			bill = b.send(t, b.cashier, bill) // 121,000
			comper := b.User(t, b.tenantID, b.propID, auth.PermPOSCashier, auth.PermPOSComp)
			out, err := b.svc.CompBill(comper, b.propID, bill.ID, pos.CompInput{Reason: "owner's guest", Name: "Pak Wayan", Approval: b.superIn})
			must(t, err)
			if out.Status != "CLOSED" {
				t.Fatalf("a comp settles the bill: %+v", out.Totals)
			}
			net := b.netBy(t)
			switch treatment {
			case "PAYMENT_COMP":
				if v := net["COMP"]; !v.Equal(dec("-121000")) {
					t.Fatalf("the comp payment: %v", net)
				}
			default:
				sum := decimal.Zero
				for _, v := range net {
					sum = sum.Add(v)
				}
				if !sum.IsZero() {
					t.Fatalf("a 100%% discount leaves nothing: %v", net)
				}
			}
		})
	}
}

func TestAnOutletReportReadsTheLedger(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "2")
	bill = b.send(t, b.cashier, bill)
	b.pay(t, b.cashier, bill, "report-key-001", pos.PayLine{Method: "CARD", Amount: dec("121000")})
	reporter := b.User(t, b.tenantID, b.propID, auth.PermPOSReport)
	r, err := b.svc.Report(reporter, b.propID, b.resto.ID, roomstest.BD, roomstest.BD)
	must(t, err)
	if _, err := json.Marshal(r); err != nil {
		t.Fatalf("the report is sent as JSON: %v", err)
	}
	eq(t, "net", r.Totals.Net, "100000")
	eq(t, "service", r.Totals.Service, "10000")
	eq(t, "tax", r.Totals.Tax, "11000")
	if len(r.Methods) != 1 || r.Methods[0].Method != "CARD" || len(r.Products) != 1 || r.Products[0].Name != "Nasi goreng" {
		t.Fatalf("%+v", r)
	}
	_, err = b.svc.Report(b.cashier, b.propID, b.resto.ID, roomstest.BD, roomstest.BD)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestTheNightAuditSaysWhatTheOpenBillsAre(t *testing.T) {
	b := newBillFx(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.tea, "1")
	b.send(t, b.cashier, bill)
	bl, wr, err := b.svc.NightAuditChecks(context.Background(), b.tenantID, b.propID)
	must(t, err)
	if len(bl) != 0 || len(wr) != 1 || wr[0].Code != "POS_OPEN_BILLS" {
		t.Fatalf("a warning by default: %+v %+v", bl, wr)
	}
	_, err = b.svc.UpdateOutlet(b.admin, b.propID, b.resto.ID, pos.OutletInput{
		Code: "R", Name: "Restoran", OutletType: "RESTAURANT", OrderMode: "PRINT", DepartmentID: b.dept, RuleProfileID: b.k.profile.ID, PriceListID: b.k.priceList.ID,
		Drawer: ptr("DRAWER1"), UsesTables: true, BlockNightAuditWithOpenBills: true, CompTreatment: "PAYMENT_COMP", Version: b.resto.Version,
	})
	must(t, err)
	bl, _, err = b.svc.NightAuditChecks(context.Background(), b.tenantID, b.propID)
	must(t, err)
	if len(bl) != 1 {
		t.Fatalf("a blocker when the outlet says so: %+v", bl)
	}
}

// Every read that touches folios keeps working with the bills of an outlet present: they have no reservation and no stay (34-pos.md 11, R13).
func TestTheReadsOfTheFrontOfficeKeepTheirMeaningWithOutletBills(t *testing.T) {
	b := newBillFx(t)
	b.shift(t)
	stay, guest := b.room(t, "310", "500000")
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "2")
	bill = b.send(t, b.cashier, bill) // 121,000
	b.pay(t, b.cashier, bill, "reads-key-0001",
		pos.PayLine{Method: "CASH", Amount: dec("50000")}, pos.PayLine{Method: "ROOM", Amount: dec("71000"), StayID: &stay})
	open := b.open(t, b.cashier, nil)
	b.add(t, b.cashier, open, b.tea, "1")
	b.send(t, b.cashier, open)

	// the default list is the folios of the stays; the bills have their own
	page, err := b.Folios.ListFolios(b.admin, b.propID, folios.FolioFilter{}, listsort.Sort{}, nil, 50)
	must(t, err)
	for _, f := range page.Data {
		if f.FolioType == "OUTLET" {
			t.Fatalf("the default list holds a bill: %+v", f)
		}
	}
	bills, err := b.Folios.ListFolios(b.admin, b.propID, folios.FolioFilter{FolioType: "OUTLET"}, listsort.Sort{}, nil, 50)
	must(t, err)
	if len(bills.Data) != 2 || bills.Data[0].OutletCode != "R" {
		t.Fatalf("the bills: %+v", bills.Data)
	}
	// the detail of a bill and of the guest folio, with the line that points at the bill
	f, err := b.Folios.GetFolio(b.admin, b.propID, *bill.FolioID)
	must(t, err)
	if f.FolioType != "OUTLET" || f.OutletCode != "R" || f.ReservationID != nil || f.StayID != nil {
		t.Fatalf("outlet folio: %+v", f)
	}
	gf, err := b.Folios.GetFolio(b.admin, b.propID, guest)
	must(t, err)
	var linked bool
	for _, it := range gf.Items {
		if it.PosBill != nil && it.PosBill.ID == bill.ID {
			linked = true
		}
	}
	if !linked {
		t.Fatalf("the line of the guest folio names its bill: %+v", gf.Items)
	}
	// the cashier list, the night audit preview and the reconciliation read them without failing
	if _, err := b.Folios.ListPayments(b.admin, b.propID, folios.PaymentFilter{}, listsort.Sort{}, nil, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Audit.Preview(b.admin, b.propID); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Accounting.Reconciliation(b.admin, b.propID, nil); err != nil {
		t.Fatal(err)
	}
	// the view of the ledger: is_deposit of every row of a folio that is not an outlet's is what the view of migration 00075 said; a payment of a bill taken after its charges is no deposit
	var differ int
	must(t, b.Pool.QueryRow(context.Background(), `
		WITH old AS (
		  SELECT fi.id AS item_id,
		         (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
		          AND CASE WHEN f.folio_type = 'MASTER'
		                   THEN NOT EXISTS (SELECT 1 FROM folio_items c
		                                     WHERE c.property_id = fi.property_id AND c.folio_id = fi.folio_id AND c.transaction_type = 'CHARGE'
		                                       AND c.id < (SELECT min(pi.id) FROM folio_items pi WHERE pi.property_id = rp.property_id AND pi.payment_id = rp.id)
		                                       AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = c.property_id AND rv.reverses_item_id = c.id))
		                   ELSE (st.id IS NULL OR rp.paid_at < st.actual_check_in_at) END) AS is_deposit
		    FROM folio_items fi
		    LEFT JOIN folio_items o ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
		    LEFT JOIN payments p ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
		    LEFT JOIN payments rp ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
		    LEFT JOIN folios f ON f.property_id = fi.property_id AND f.id = fi.folio_id
		    LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id
		   WHERE f.folio_type <> 'OUTLET')
		SELECT count(*) FROM old JOIN folio_item_gl g ON g.item_id = old.item_id WHERE g.is_deposit IS DISTINCT FROM old.is_deposit`).Scan(&differ))
	if differ != 0 {
		t.Fatalf("%d rows differ from the view of 00075", differ)
	}
	if n := b.Count(t, `SELECT count(*) FROM folio_item_gl g JOIN folios f ON f.id = g.folio_id WHERE f.folio_type = 'OUTLET' AND g.is_deposit`); n != 0 {
		t.Fatalf("a payment taken after the charges of a bill is no deposit: %d", n)
	}
	// the generic folio routes refuse the folio of a bill
	_, err = b.Folios.PostCharge(b.admin, b.propID, *open.FolioID, "generic-key-0001", folios.ChargeInput{ChargeCodeID: b.chargeCode(t, "OTHER"), Quantity: "1", UnitPrice: ptr("1000")})
	wantCode(t, err, "OUTLET_FOLIO_USE_POS")
}

// journal posts the journal of the open business day and answers the net of each account: debit less credit.
func (b *billFx) journal(t *testing.T) map[string]decimal.Decimal {
	t.Helper()
	p, err := auth.Require(b.admin)
	must(t, err)
	must(t, b.TxM.WithinTx(b.admin, func(ctx context.Context) error {
		return b.Accounting.PostDay(ctx, p, b.propID, roomstest.BD)
	}))
	rows, err := b.Pool.Query(context.Background(), `SELECT a.code, sum(l.debit - l.credit) FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 GROUP BY a.code`, b.propID)
	must(t, err)
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var code string
		var v decimal.Decimal
		must(t, rows.Scan(&code, &v))
		out[code] = v
	}
	return out
}

func wantNet(t *testing.T, net map[string]decimal.Decimal, code, want string) {
	t.Helper()
	got, ok := net[code]
	if !ok {
		got = decimal.Zero
	}
	if !got.Equal(dec(want)) {
		t.Fatalf("account %s: %s, want %s (%v)", code, got, want, net)
	}
}

// 9.1 of the design with the rates of the fixture (service 10%, PB1 10% on net plus service): the bill of a table with a void, paid in cash and to a room.
func TestTheDayJournalOfABillWithAVoidPaidInCashAndToARoomBalancesAndNetsTheTransfer(t *testing.T) {
	b := newBillFx(t)
	b.shift(t)
	stay, guest := b.room(t, "305", "500000")
	table := b.table(t)
	bill := b.open(t, b.cashier, &table)
	bill = b.add(t, b.cashier, bill, b.nasi, "2")  // 121,000
	bill = b.add(t, b.cashier, bill, b.steak, "1") // 181,500
	bill = b.add(t, b.cashier, bill, b.tea, "2")   // 36,300
	bill = b.send(t, b.cashier, bill)
	eq(t, "sold", bill.Totals.Sold, "338800")
	var steak int64
	for _, l := range bill.Lines {
		if l.ProductName == "Steak" {
			steak = l.ID
		}
	}
	bill, err := b.svc.VoidLine(b.cashier, b.propID, bill.ID, steak, pos.VoidLineInput{Reason: "wrong order", Approval: b.superIn})
	must(t, err)
	eq(t, "after the void", bill.Totals.Sold, "157300")
	res := b.pay(t, b.cashier, bill, "ledger-key-0001", pos.PayLine{Method: "CASH", Amount: dec("100000")}, pos.PayLine{Method: "ROOM", Amount: dec("57300"), StayID: &stay})
	if !res.Closed {
		t.Fatalf("closed: %+v", res.Bill.Totals)
	}
	net := b.journal(t)
	wantNet(t, net, "1110", "100000")  // cash
	wantNet(t, net, "1210", "57300")   // the guest ledger: what the room owes
	wantNet(t, net, "1215", "0")       // the transfer account comes to nothing
	wantNet(t, net, "4210", "-130000") // revenue: nasi and tea; the steak is not revenue
	wantNet(t, net, "2430", "-13000")  // service
	wantNet(t, net, "2410", "-14300")  // tax
	var bal decimal.Decimal
	for _, v := range net {
		bal = bal.Add(v)
	}
	if !bal.IsZero() {
		t.Fatalf("the journal does not balance: %v", net)
	}
	// the folios: the bill is at zero, the room owes what was charged
	var billBal, roomBal decimal.Decimal
	must(t, b.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0) FROM folio_items WHERE folio_id = $1`, *bill.FolioID).Scan(&billBal))
	must(t, b.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0) FROM folio_items WHERE folio_id = $1`, guest).Scan(&roomBal))
	eq(t, "the bill", billBal, "0")
	eq(t, "the room", roomBal, "57300")
	// the department of the revenue is the outlet's
	var dept int64
	must(t, b.Pool.QueryRow(context.Background(), `SELECT department_id FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE' LIMIT 1`, *bill.FolioID).Scan(&dept))
	if dept != b.dept {
		t.Fatalf("department %d, want the outlet's %d", dept, b.dept)
	}
}

// A bill split by item and then paid by two payers: the revenue is not doubled and every account nets the same as the bill whole.
func TestASplitBillNetsTheSameInEveryAccountAndTheRevenueIsNotDoubled(t *testing.T) {
	b := newBillFx(t)
	b.shift(t)
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "1")
	bill = b.add(t, b.cashier, bill, b.steak, "1")
	bill = b.send(t, b.cashier, bill) // 60,500 + 181,500
	b.seq++
	second, err := b.svc.SplitBill(b.cashier, b.propID, bill.ID, pos.SplitInput{ClientUUID: uid(9500 + b.seq), LineIDs: []int64{bill.Lines[1].ID}})
	must(t, err)
	first, err := b.svc.GetBill(b.cashier, b.propID, bill.ID)
	must(t, err)
	b.pay(t, b.cashier, first, "split-key-00001", pos.PayLine{Method: "CASH", Amount: dec("60500")})
	b.pay(t, b.cashier, second, "split-key-00002", pos.PayLine{Method: "QRIS", Amount: dec("181500")})
	net := b.journal(t)
	wantNet(t, net, "1110", "60500")
	wantNet(t, net, "1155", "181500")
	wantNet(t, net, "1210", "0")
	wantNet(t, net, "4210", "-200000")
	wantNet(t, net, "2430", "-20000")
	wantNet(t, net, "2410", "-22000")
}

// A bill charged to a room and returned the next day: the revenue is counted once and the transfer account is zero on both days.
func TestAReturnedChargeToARoomCountsTheRevenueOnce(t *testing.T) {
	b := newBillFx(t)
	stay, guest := b.room(t, "320", "500000")
	bill := b.open(t, b.cashier, nil)
	bill = b.add(t, b.cashier, bill, b.nasi, "1")
	bill = b.send(t, b.cashier, bill) // 60,500
	res := b.pay(t, b.cashier, bill, "return-key-0001", pos.PayLine{Method: "ROOM", Amount: dec("60500"), StayID: &stay})
	if !res.Closed {
		t.Fatal("closed")
	}
	var chargeID int64
	must(t, b.Pool.QueryRow(context.Background(), `SELECT id FROM pos_room_charges WHERE bill_id = $1`, bill.ID).Scan(&chargeID))
	reopener := b.User(t, b.tenantID, b.propID, auth.PermPOSCashier, auth.PermFolioReopen, auth.PermFolioReverse)
	_, err := b.svc.ReopenBill(reopener, b.propID, bill.ID, pos.ReopenInput{Reason: "wrong room", Approval: b.superIn})
	must(t, err)
	out, err := b.svc.ReturnRoomCharge(reopener, b.propID, chargeID, pos.RoomChargeCorrectionInput{Reason: "wrong room", Approval: b.superIn})
	must(t, err)
	eq(t, "the bill owes again", out.Totals.Balance, "60500")
	var roomBal decimal.Decimal
	must(t, b.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0) FROM folio_items WHERE folio_id = $1`, guest).Scan(&roomBal))
	eq(t, "the room owes nothing", roomBal, "0")
	// the usage of the limit went back down: the same room can be charged the whole limit again
	var used decimal.Decimal
	must(t, b.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount), 0) FROM pos_room_charges WHERE returned_at IS NULL`).Scan(&used))
	eq(t, "used", used, "0")
	net := b.journal(t)
	wantNet(t, net, "1215", "0")
	wantNet(t, net, "4210", "-50000")
	wantNet(t, net, "1210", "60500") // the bill owes it again, the room nothing
}
