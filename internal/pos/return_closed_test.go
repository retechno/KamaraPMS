package pos_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/pos"
	"kamarapms/internal/rooms/roomstest"
)

// KD-26 (docs/architecture/42-pos-charge-return-after-checkout.md): a charge of a bill to the wrong room, found after that guest checked
// out, goes back to the bill. The folio that settled it is settled again in the same transaction (a refund, or the credit stays on an
// open master) and stays CLOSED at zero. The numbers in the names are the tests of §9.

type closedFx struct {
	*billFx
	sup   context.Context // the supervisor at the outlet: cashier, folio.reverse, folio.reopen, payment.refund
	supID int64
}

func newClosedFx(t *testing.T) *closedFx {
	t.Helper()
	b := newBillFx(t)
	must(t, b.Exec(t, `UPDATE properties SET refund_methods = ARRAY['CASH', 'CARD', 'BANK_TRANSFER'] WHERE id = $1`, b.propID))
	c := &closedFx{billFx: b}
	c.sup = b.User(t, b.tenantID, b.propID, auth.PermPOSCashier, auth.PermFolioReverse, auth.PermFolioReopen, auth.PermPaymentRefund, auth.PermFolioRead)
	p, err := auth.Require(c.sup)
	must(t, err)
	c.supID = p.UserID
	return c
}

// guestRoom is an in-house guest who checked in an hour before the clock, so a payment from now on is not a deposit.
func (c *closedFx) guestRoom(t *testing.T, number string) (stay, folio int64) {
	t.Helper()
	stay, folio = c.room(t, number, "500000")
	must(t, c.Exec(t, `UPDATE stays SET actual_check_in_at = $1 WHERE id = $2`, roomstest.T0.Add(-time.Hour), stay))
	return stay, folio
}

// chargeToRoom sells a nasi goreng (60,500 with service and tax) at the restaurant and charges the bill to the room: the bill closes.
func (c *closedFx) chargeToRoom(t *testing.T, stay int64) (pos.Bill, int64) {
	t.Helper()
	bill := c.open(t, c.cashier, nil)
	bill = c.add(t, c.cashier, bill, c.nasi, "1")
	bill = c.send(t, c.cashier, bill)
	c.seq++
	res := c.pay(t, c.cashier, bill, fmt.Sprintf("kd26-room-%06d", c.seq), pos.PayLine{Method: "ROOM", Amount: dec("60500"), StayID: &stay})
	if !res.Closed {
		t.Fatalf("the bill charged to the room is closed: %+v", res.Bill.Totals)
	}
	var charge int64
	must(t, c.Pool.QueryRow(context.Background(), `SELECT id FROM pos_room_charges WHERE bill_id = $1`, bill.ID).Scan(&charge))
	return res.Bill, charge
}

// payFolio takes a payment on a folio at the front office.
func (c *closedFx) payFolio(t *testing.T, folio int64, amount, method string) int64 {
	t.Helper()
	c.seq++
	r, err := c.Folios.PostPayment(c.admin, c.propID, folio, fmt.Sprintf("kd26-pay-%06d", c.seq), folios.PaymentInput{Amount: amount, PaymentMethod: method})
	must(t, err)
	return r.Payment.ID
}

// checkOut closes the folios of the stay, as the check-out does (each at a zero balance).
func (c *closedFx) checkOut(t *testing.T, stay int64) {
	t.Helper()
	p, err := auth.Require(c.admin)
	must(t, err)
	must(t, c.TxM.WithinTx(c.admin, func(ctx context.Context) error {
		_, err := c.Folios.CloseStayFolios(ctx, p, c.propID, stay)
		return err
	}))
}

// checkedOut is the story of KD-26: the guest of the room is charged 60,500 by the restaurant, pays it by card at the front office and
// checks out.
func (c *closedFx) checkedOut(t *testing.T, number string) (guest int64, bill pos.Bill, charge, payment int64) {
	t.Helper()
	stay, guest := c.guestRoom(t, number)
	bill, charge = c.chargeToRoom(t, stay)
	payment = c.payFolio(t, guest, "60500", "CARD")
	c.checkOut(t, stay)
	return guest, bill, charge, payment
}

// nextDay closes the open business day and opens the next one, without the steps of the night audit.
func (c *closedFx) nextDay(t *testing.T) civil.Date {
	t.Helper()
	day, err := c.Tenancy.CurrentBusinessDay(c.admin, c.propID)
	must(t, err)
	next := day.BusinessDate.AddDays(1)
	must(t, c.Exec(t, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, c.propID))
	must(t, c.Exec(t, `INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, $3)`, c.tenantID, c.propID, next))
	return next
}

// dayJournal posts the journal of the business date d and answers the net of each account (debit less credit) over the journals of d.
func (c *closedFx) dayJournal(t *testing.T, d civil.Date) map[string]decimal.Decimal {
	t.Helper()
	p, err := auth.Require(c.admin)
	must(t, err)
	must(t, c.TxM.WithinTx(c.admin, func(ctx context.Context) error { return c.Accounting.PostDay(ctx, p, c.propID, d) }))
	rows, err := c.Pool.Query(context.Background(), `SELECT a.code, sum(l.debit - l.credit) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id
		JOIN gl_accounts a ON a.id = l.account_id WHERE l.property_id = $1 AND j.journal_date = $2 GROUP BY a.code`, c.propID, d.String())
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

// journalText is every line of the journals of the date d, as text, to compare a closed day before and after.
func (c *closedFx) journalText(t *testing.T, d civil.Date) string {
	t.Helper()
	var s string
	must(t, c.Pool.QueryRow(context.Background(), `SELECT COALESCE(string_agg(j.id || ':' || l.line_no || ':' || l.account_id || ':' || l.debit || ':' || l.credit, ',' ORDER BY j.id, l.line_no), '')
		FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE l.property_id = $1 AND j.journal_date = $2`, c.propID, d.String()).Scan(&s))
	return s
}

func (c *closedFx) items(t *testing.T, folio int64) int {
	t.Helper()
	return c.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, folio)
}

func (c *closedFx) balance(t *testing.T, folio int64) decimal.Decimal {
	t.Helper()
	var v decimal.Decimal
	must(t, c.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit - credit), 0) FROM folio_items WHERE folio_id = $1`, folio).Scan(&v))
	return v
}

func (c *closedFx) status(t *testing.T, folio int64) string {
	t.Helper()
	var s string
	must(t, c.Pool.QueryRow(context.Background(), `SELECT status FROM folios WHERE id = $1`, folio).Scan(&s))
	return s
}

// refund is the input of a return that refunds the payment by the method.
func (c *closedFx) refund(payment int64, method string) pos.ReturnClosedInput {
	return pos.ReturnClosedInput{Reason: "charged to the wrong room", Approval: c.superIn, Refund: &pos.ReturnClosedRefund{PaymentID: payment, PaymentMethod: method, ReferenceNumber: "EDC-0001"}}
}

// openShift opens a cashier shift for the user on the open business date.
func (c *closedFx) openShift(t *testing.T, userID int64, drawer string) int64 {
	t.Helper()
	day, err := c.Tenancy.CurrentBusinessDay(c.admin, c.propID)
	must(t, err)
	var id int64
	must(t, c.Pool.QueryRow(context.Background(), `INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, status, opened_at, business_date_opened, opening_float)
		VALUES ($1, $2, 'SHF-' || $4, $3, $4, 'OPEN', now(), $5, 0) RETURNING id`, c.tenantID, c.propID, userID, drawer, day.BusinessDate.String()).Scan(&id))
	return id
}

// group puts the reservation of the stay in a new group and opens its master folio.
func (c *closedFx) group(t *testing.T, stay int64, code string) (group, master int64) {
	t.Helper()
	ctx := context.Background()
	var res int64
	must(t, c.Pool.QueryRow(ctx, `SELECT rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $1`, stay).Scan(&res))
	must(t, c.Pool.QueryRow(ctx, `INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date, contact_name)
		VALUES ($1, $2, $3, 'Conference', '2026-09-30', '2026-10-02', 'Dewi') RETURNING id`, c.tenantID, c.propID, code).Scan(&group))
	must(t, c.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, group, res))
	m, err := c.Folios.OpenMaster(c.admin, c.propID, group)
	must(t, err)
	return group, m.Master.ID
}

// closeMaster ends the rooms of the master's group and closes the master at zero.
func (c *closedFx) closeMaster(t *testing.T, master int64) {
	t.Helper()
	must(t, c.Exec(t, `UPDATE reservation_rooms SET status = 'COMPLETED' WHERE reservation_id IN (SELECT id FROM reservations WHERE booking_group_id = (SELECT booking_group_id FROM folios WHERE id = $1))`, master))
	f, err := c.Folios.GetFolio(c.admin, c.propID, master)
	must(t, err)
	_, err = c.Folios.CloseFolio(c.admin, c.propID, master, f.Version)
	must(t, err)
}

// toMaster hands the guest folio's balance of 60,500 to the master, as a group's cashier does.
func (c *closedFx) toMaster(t *testing.T, guest, master int64) {
	t.Helper()
	c.seq++
	_, err := c.Folios.TransferBalance(c.admin, c.propID, guest, fmt.Sprintf("kd26-master-%06d", c.seq), folios.TransferBalanceInput{FolioID: master, Amount: "60500", Reason: "the group pays", Approval: c.superIn})
	must(t, err)
}

// 1 and 10. The story of KD-26, case a: the guest paid by card and left. The next day the bill, closed, is reopened; the return pair and the
// refund are dated today; the guest folio stays CLOSED at zero, with its close untouched. Today's journal is Dr 1210 / Cr the card clearing;
// the closed day's journal does not change. The bill is then charged to the right room.
func TestKD26ReturnAfterCheckOutRefundsTheCardAndTheBillOwesAgain(t *testing.T) {
	c := newClosedFx(t)
	guest, bill, charge, payment := c.checkedOut(t, "321")
	var closedAt time.Time
	var closedOn civil.Date
	must(t, c.Pool.QueryRow(context.Background(), `SELECT closed_at, closed_on FROM folios WHERE id = $1`, guest).Scan(&closedAt, &closedOn))
	c.dayJournal(t, roomstest.BD)
	day1 := c.journalText(t, roomstest.BD)
	today := c.nextDay(t)

	if bill.Status != "CLOSED" {
		t.Fatalf("the bill is closed: %s", bill.Status)
	}
	pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
	must(t, err)
	if pv.Refusal != nil || pv.Case != pos.ReturnCaseRefund || pv.ProposedPaymentID == nil || *pv.ProposedPaymentID != payment || pv.Holder.Status != "CLOSED" ||
		pv.BillStatus != "CLOSED" || !pv.Amount.Equal(dec("60500")) || pv.Master != nil || len(pv.Payments) != 1 || !pv.Payments[0].Refundable.Equal(dec("60500")) {
		t.Fatalf("the preview: %+v", pv)
	}
	out, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CARD"))
	must(t, err)
	if out.Bill.Status != "OPEN" {
		t.Fatalf("the bill is reopened: %s", out.Bill.Status)
	}
	eq(t, "the bill owes again", out.Bill.Totals.Balance, "60500")
	if out.Holder.Status != "CLOSED" || !out.Holder.Balance.IsZero() || out.Refund == nil || out.Refund.PaymentMethod != "CARD" || !out.Refund.Amount.Equal(dec("60500")) {
		t.Fatalf("the answer: %+v %+v", out.Holder, out.Refund)
	}
	// the guest folio: closed, at zero, its close untouched; its two new rows are dated today
	var status string
	var at2 time.Time
	var on2 civil.Date
	must(t, c.Pool.QueryRow(context.Background(), `SELECT status, closed_at, closed_on FROM folios WHERE id = $1`, guest).Scan(&status, &at2, &on2))
	if status != "CLOSED" || !at2.Equal(closedAt) || on2 != closedOn {
		t.Fatalf("the guest folio: %s %v %v, was closed %v %v", status, at2, on2, closedAt, closedOn)
	}
	eq(t, "the guest folio", c.balance(t, guest), "0")
	if n := c.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND business_date = $2`, guest, today.String()); n != 2 {
		t.Fatalf("the return and the refund are dated today: %d rows", n)
	}
	// the refund: of the card payment, approved, dated today, with its reference
	if n := c.Count(t, `SELECT count(*) FROM payments WHERE payment_type = 'REFUND' AND refund_of_payment_id = $1 AND payment_method = 'CARD' AND amount = 60500
		AND business_date = $2 AND approved_by IS NOT NULL AND reference_number = 'EDC-0001'`, payment, today.String()); n != 1 {
		t.Fatalf("the refund: %d", n)
	}
	if n := c.Count(t, `SELECT count(*) FROM pos_room_charges WHERE id = $1 AND returned_at IS NOT NULL AND returned_by_transfer_id IS NOT NULL`, charge); n != 1 {
		t.Fatal("the charge to the room is marked returned")
	}
	// today's journal: Dr 1210 (the bill owes) / Cr the card clearing; the transfers net out; revenue does not move; the closed day is unchanged
	net := c.dayJournal(t, today)
	wantNet(t, net, "1210", "60500")
	wantNet(t, net, "1150", "-60500")
	wantNet(t, net, "1215", "0")
	wantNet(t, net, "4210", "0")
	if c.journalText(t, roomstest.BD) != day1 {
		t.Fatal("the journal of the closed day changed")
	}
	// audited: the act on the bill, the return on the guest folio, the refund
	for action, want := range map[string]int{"pos.room_charge_returned_after_checkout": 1, "folio.transfer_returned": 1, "payment.refunded": 1} {
		if n := c.Count(t, `SELECT count(*) FROM audit_logs WHERE action = $1`, action); n != want {
			t.Fatalf("%s: %d audit entries", action, n)
		}
	}
	if n := c.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'pos.room_charge_returned_after_checkout' AND entity_type = 'pos_bill' AND entity_id = $1
		AND new_data->>'case' = 'REFUND' AND new_data->>'refund_of_payment_id' = $2 AND new_data->>'approved_by' IS NOT NULL`, bill.ID, fmt.Sprint(payment)); n != 1 {
		t.Fatal("the audit entry names the case, the refunded payment and the approver")
	}
	// 10: while the bill is open with a balance the night audit warns; it is charged to the right room and closes
	_, warnings, err := c.svc.NightAuditChecks(context.Background(), c.tenantID, c.propID)
	must(t, err)
	if len(warnings) != 1 || warnings[0].Code != "POS_OPEN_BILLS" {
		t.Fatalf("the night audit warns of the open bill: %+v", warnings)
	}
	stay2, guest2 := c.guestRoom(t, "322")
	res := c.pay(t, c.cashier, out.Bill, "kd26-right-room1", pos.PayLine{Method: "ROOM", Amount: dec("60500"), StayID: &stay2})
	if !res.Closed {
		t.Fatalf("the bill charged to the right room closes: %+v", res.Bill.Totals)
	}
	eq(t, "the right room owes the meal", c.balance(t, guest2), "60500")
}

// 2. A cash refund goes through the cashier shift of the person who records it. Without an open shift the existing error, and nothing is
// written.
func TestKD26ACashRefundNeedsTheShiftOfTheSupervisor(t *testing.T) {
	c := newClosedFx(t)
	guest, bill, charge, payment := c.checkedOut(t, "321")
	must(t, c.Exec(t, `INSERT INTO property_cashier_settings (tenant_id, property_id, require_shift_for_cash, block_night_audit) VALUES ($1, $2, true, false)
		ON CONFLICT (property_id) DO UPDATE SET require_shift_for_cash = true`, c.tenantID, c.propID))
	before := c.items(t, guest)
	_, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CASH"))
	wantCode(t, err, "NO_OPEN_SHIFT")
	got, err := c.svc.GetBill(c.sup, c.propID, bill.ID)
	must(t, err)
	if got.Status != "CLOSED" || c.items(t, guest) != before || c.Count(t, `SELECT count(*) FROM pos_room_charges WHERE id = $1 AND returned_at IS NULL`, charge) != 1 {
		t.Fatalf("nothing is written: bill %s, items %d (was %d)", got.Status, c.items(t, guest), before)
	}
	shift := c.openShift(t, c.supID, "FO")
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CASH"))
	must(t, err)
	if n := c.Count(t, `SELECT count(*) FROM payments WHERE payment_type = 'REFUND' AND payment_method = 'CASH' AND shift_id = $1 AND amount = 60500`, shift); n != 1 {
		t.Fatal("the cash refund is in the supervisor's shift")
	}
}

// 3. The payment refunded must have what the charge comes to left to refund, and a payment that was a deposit is not refunded.
func TestKD26TheRefundedPaymentMustCoverTheChargeAndADepositIsRefused(t *testing.T) {
	c := newClosedFx(t)
	stay, guest := c.guestRoom(t, "321")
	_, charge := c.chargeToRoom(t, stay)
	c.chargeToRoom(t, stay) // the guest owes 121,000
	small := c.payFolio(t, guest, "10000", "CARD")
	large := c.payFolio(t, guest, "111000", "CARD")
	c.checkOut(t, stay)
	pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
	must(t, err)
	if pv.Refusal != nil || pv.ProposedPaymentID == nil || *pv.ProposedPaymentID != large || len(pv.Payments) != 2 {
		t.Fatalf("the preview proposes the last payment that covers the charge: %+v", pv)
	}
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(small, "CARD"))
	wantCode(t, err, "REFUND_EXCEEDS_PAYMENT")

	// a guest whose only payment was made before the check-in: a deposit
	stay2, guest2 := c.guestRoom(t, "322")
	_, charge2 := c.chargeToRoom(t, stay2)
	c.Clock.Set(roomstest.T0.Add(-2 * time.Hour))
	deposit := c.payFolio(t, guest2, "60500", "CARD")
	c.Clock.Set(roomstest.T0)
	c.checkOut(t, stay2)
	pv, err = c.svc.PreviewReturnClosed(c.sup, c.propID, charge2)
	must(t, err)
	if pv.Refusal == nil || pv.Refusal.Code != "REFUND_OF_DEPOSIT_AFTER_CLOSE" || len(pv.Payments) != 1 || !pv.Payments[0].IsDeposit {
		t.Fatalf("the preview refuses the deposit: %+v", pv)
	}
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge2, c.refund(deposit, "CARD"))
	wantCode(t, err, "REFUND_OF_DEPOSIT_AFTER_CLOSE")
}

// 4. Case b, deferred (KD-29): a guest folio settled by a transfer to the city ledger is refused before anything is written.
func TestKD26AGuestSentToTheCityLedgerIsRefused(t *testing.T) {
	c := newClosedFx(t)
	stay, guest := c.guestRoom(t, "321")
	bill, charge := c.chargeToRoom(t, stay)
	co, err := c.Companies.Create(c.admin, c.propID, companies.Input{Code: "ACME", Name: "Acme Corp", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	_, err = c.Folios.Transfer(c.admin, c.propID, guest, "kd26-cityledger", folios.TransferInput{CompanyID: co.ID, Amount: "60500"})
	must(t, err)
	c.checkOut(t, stay)
	pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
	must(t, err)
	if pv.Refusal == nil || pv.Refusal.Code != "RETURN_AFTER_CHECKOUT_CITY_LEDGER" {
		t.Fatalf("the preview: %+v", pv)
	}
	before := c.items(t, guest)
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, pos.ReturnClosedInput{Reason: "wrong room", Approval: c.superIn})
	wantCode(t, err, "RETURN_AFTER_CHECKOUT_CITY_LEDGER")
	got, err := c.svc.GetBill(c.sup, c.propID, bill.ID)
	must(t, err)
	if got.Status != "CLOSED" || c.items(t, guest) != before || c.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'pos.room_charge_returned_after_checkout'`) != 0 {
		t.Fatal("nothing is written")
	}
}

// 5 and 7. The master folio of a group: straight to a closed master paid by the group (a refund on the master); through the guest folio to an
// open master (the credit stays on it); to a closed master paid by the group; to a master settled by the city ledger (refused); a chain of
// two balances (refused).
func TestKD26TheMasterFolioIsFollowedOneHop(t *testing.T) {
	c := newClosedFx(t)

	t.Run("straight to a closed master paid by the group", func(t *testing.T) {
		stay, _ := c.guestRoom(t, "401")
		g, master := c.group(t, stay, "G401")
		_, err := c.Folios.SetGroupRouting(c.admin, c.propID, g, folios.GroupRoutingInput{Instructions: []folios.GroupInstructionInput{{Scope: folios.ScopeAll}}})
		must(t, err)
		_, err = c.Folios.SetOutletLock(c.opener, c.propID, master, folios.OutletLockInput{LimitAmount: dec("500000"), ValidMode: "UNTIL_CLOSE", OutletIDs: []int64{c.resto.ID}})
		must(t, err)
		_, charge := c.chargeToRoom(t, stay)
		if c.Count(t, `SELECT count(*) FROM pos_room_charges WHERE id = $1 AND to_folio_id = $2`, charge, master) != 1 {
			t.Fatal("the charge is routed to the master")
		}
		paid := c.payFolio(t, master, "60500", "CARD")
		c.closeMaster(t, master)
		pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
		must(t, err)
		if pv.Refusal != nil || pv.Case != pos.ReturnCaseRefund || pv.Holder.FolioID != master || pv.Master != nil {
			t.Fatalf("the preview: %+v", pv)
		}
		_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(paid, "CARD"))
		must(t, err)
		if c.status(t, master) != "CLOSED" || !c.balance(t, master).IsZero() {
			t.Fatal("the master stays closed at zero")
		}
	})

	t.Run("through the guest folio to an open master", func(t *testing.T) {
		stay, guest := c.guestRoom(t, "402")
		_, master := c.group(t, stay, "G402")
		_, charge := c.chargeToRoom(t, stay)
		c.toMaster(t, guest, master)
		c.checkOut(t, stay)
		pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
		must(t, err)
		if pv.Refusal != nil || pv.Case != pos.ReturnCaseMasterCredit || pv.Master == nil || pv.Master.FolioID != master || pv.Master.Status != "OPEN" || len(pv.Payments) != 0 {
			t.Fatalf("the preview: %+v", pv)
		}
		// no refund is asked, and none is taken
		bad := c.refund(1, "CARD")
		_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, bad)
		wantCode(t, err, "VALIDATION_FAILED")
		out, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, pos.ReturnClosedInput{Reason: "wrong room", Approval: c.superIn})
		must(t, err)
		if out.Refund != nil || out.Master == nil || !out.Master.Balance.IsZero() || c.status(t, guest) != "CLOSED" || !c.balance(t, guest).IsZero() || !c.balance(t, master).IsZero() {
			t.Fatalf("the guest is back at zero and the master owes 60,500 less: %+v %+v", out.Holder, out.Master)
		}
		eq(t, "the bill", out.Bill.Totals.Balance, "60500")
	})

	t.Run("through the guest folio to a closed master paid by the group", func(t *testing.T) {
		stay, guest := c.guestRoom(t, "403")
		_, master := c.group(t, stay, "G403")
		_, charge := c.chargeToRoom(t, stay)
		c.toMaster(t, guest, master)
		c.checkOut(t, stay)
		paid := c.payFolio(t, master, "60500", "CARD")
		c.closeMaster(t, master)
		pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
		must(t, err)
		if pv.Refusal != nil || pv.Case != pos.ReturnCaseRefund || pv.Master == nil || pv.Master.Status != "CLOSED" || pv.ProposedPaymentID == nil || *pv.ProposedPaymentID != paid {
			t.Fatalf("the preview: %+v", pv)
		}
		out, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(paid, "CARD"))
		must(t, err)
		if out.Refund == nil || c.status(t, master) != "CLOSED" || !c.balance(t, master).IsZero() || !c.balance(t, guest).IsZero() {
			t.Fatal("the master and the guest stay closed at zero, and the group is refunded")
		}
	})

	t.Run("a master settled by the city ledger is refused", func(t *testing.T) {
		stay, guest := c.guestRoom(t, "404")
		_, master := c.group(t, stay, "G404")
		_, charge := c.chargeToRoom(t, stay)
		c.toMaster(t, guest, master)
		c.checkOut(t, stay)
		co, err := c.Companies.Create(c.admin, c.propID, companies.Input{Code: "G404CO", Name: "Group Co", PaymentTermsDays: 30, IsActive: true})
		must(t, err)
		_, err = c.Folios.Transfer(c.admin, c.propID, master, "kd26-master-cl", folios.TransferInput{CompanyID: co.ID, Amount: "60500"})
		must(t, err)
		c.closeMaster(t, master)
		_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, pos.ReturnClosedInput{Reason: "wrong room", Approval: c.superIn})
		wantCode(t, err, "RETURN_AFTER_CHECKOUT_CITY_LEDGER")
	})

	t.Run("a chain of two balances is refused", func(t *testing.T) {
		stay, guest := c.guestRoom(t, "405")
		_, master := c.group(t, stay, "G405")
		_, charge := c.chargeToRoom(t, stay)
		c.toMaster(t, guest, master)
		c.checkOut(t, stay)
		_, other := c.guestRoom(t, "406")
		_, err := c.Folios.TransferBalance(c.admin, c.propID, master, "kd26-chain-0001", folios.TransferBalanceInput{FolioID: other, Amount: "60500", Reason: "moved on", Approval: c.superIn})
		must(t, err)
		pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
		must(t, err)
		if pv.Refusal == nil || pv.Refusal.Code != "RETURN_CHAIN_UNSUPPORTED" {
			t.Fatalf("the preview: %+v", pv)
		}
		_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, pos.ReturnClosedInput{Reason: "wrong room", Approval: c.superIn})
		wantCode(t, err, "RETURN_CHAIN_UNSUPPORTED")
	})
}

// 8. The refusals: each permission (403 before an approval is asked), the approval rule of the property, another tenant, a charge whose
// holder is still open (the existing return is the way), a charge returned already.
func TestKD26TheRefusals(t *testing.T) {
	c := newClosedFx(t)
	_, _, charge, payment := c.checkedOut(t, "321")
	noApproval := c.refund(payment, "CARD")
	noApproval.Approval = nil
	for name, ctx := range map[string]context.Context{
		"folio.reverse":  c.User(t, c.tenantID, c.propID, auth.PermPOSCashier, auth.PermFolioReopen, auth.PermPaymentRefund),
		"folio.reopen":   c.User(t, c.tenantID, c.propID, auth.PermPOSCashier, auth.PermFolioReverse, auth.PermPaymentRefund),
		"payment.refund": c.User(t, c.tenantID, c.propID, auth.PermPOSCashier, auth.PermFolioReverse, auth.PermFolioReopen),
		"pos.cashier":    c.User(t, c.tenantID, c.propID, auth.PermFolioReverse, auth.PermFolioReopen, auth.PermPaymentRefund),
	} {
		_, err := c.svc.ReturnRoomChargeClosed(ctx, c.propID, charge, noApproval)
		if e, ok := asErr(err); !ok || e.Code != "PERMISSION_DENIED" {
			t.Fatalf("without %s: %v", name, err)
		}
	}
	_, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, noApproval)
	wantCode(t, err, "APPROVAL_REQUIRED")
	// the rule of the property: someone else approves (a supervisor who may approve corrections cannot approve their own)
	c.SetRule(t, c.tenantID, c.propID, "ROOM_CHARGE_RETURN_CLOSED", "DISTINCT")
	self, email := c.Account(t, c.tenantID, c.propID, auth.PermPOSCashier, auth.PermFolioReverse, auth.PermFolioReopen, auth.PermPaymentRefund, auth.PermCorrectionApprove)
	own := c.refund(payment, "CARD")
	own.Approval = &iam.ApprovalInput{Email: email, Password: roomstest.Password}
	_, err = c.svc.ReturnRoomChargeClosed(self, c.propID, charge, own)
	wantCode(t, err, "APPROVAL_SAME_USER")
	c.SetRule(t, c.tenantID, c.propID, "ROOM_CHARGE_RETURN_CLOSED", "FREE")
	// another tenant
	other := c.Tenant(t, "XYZ")
	_, err = c.svc.ReturnRoomChargeClosed(roomstest.Admin(other.ID), c.propID, charge, c.refund(payment, "CARD"))
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = c.svc.PreviewReturnClosed(roomstest.Admin(other.ID), c.propID, charge)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	// a reason is required
	blank := c.refund(payment, "CARD")
	blank.Reason = " "
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, blank)
	wantCode(t, err, "VALIDATION_FAILED")
	// done, then a second time
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CARD"))
	must(t, err)
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CARD"))
	wantCode(t, err, "ALREADY_RETURNED")
	pv, err := c.svc.PreviewReturnClosed(c.sup, c.propID, charge)
	must(t, err)
	if pv.Refusal == nil || pv.Refusal.Code != "ALREADY_RETURNED" {
		t.Fatalf("the preview of a returned charge: %+v", pv)
	}
	// a guest still in house: the existing return, not this one
	stay2, _ := c.guestRoom(t, "322")
	_, charge2 := c.chargeToRoom(t, stay2)
	pv, err = c.svc.PreviewReturnClosed(c.sup, c.propID, charge2)
	must(t, err)
	if pv.Refusal == nil || pv.Refusal.Code != "RETURN_HOLDER_OPEN" {
		t.Fatalf("the preview of an open holder: %+v", pv)
	}
	_, err = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge2, pos.ReturnClosedInput{Reason: "wrong room", Approval: c.superIn})
	wantCode(t, err, "RETURN_HOLDER_OPEN")
	_, err = c.svc.PreviewReturnClosed(c.sup, c.propID, 999999)
	wantCode(t, err, "ROOM_CHARGE_NOT_FOUND")
}

// 9. The rule of the database: a CLOSED folio whose items do not sum to zero is refused at commit (CLOSED_FOLIO_UNBALANCED), whatever
// path wrote it.
func TestKD26AClosedFolioOffZeroIsRefusedAtCommit(t *testing.T) {
	c := newClosedFx(t)
	stay, guest := c.guestRoom(t, "321")
	c.chargeToRoom(t, stay) // the guest owes 60,500
	err := c.TxM.WithinTx(c.admin, func(ctx context.Context) error {
		tx, _ := db.Tx(ctx)
		_, err := tx.Exec(ctx, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, guest)
		return err
	})
	wantCode(t, err, "CLOSED_FOLIO_UNBALANCED")
	if c.status(t, guest) != "OPEN" {
		t.Fatal("the folio stays open")
	}
}

// 11. The night audit and the return at once: the return waits for the audit's lock of the business day and is then dated the next day,
// whole.
func TestKD26TheNightAuditAndTheReturnAtOnce(t *testing.T) {
	c := newClosedFx(t)
	guest, _, charge, payment := c.checkedOut(t, "321")
	ctx := context.Background()
	hold, err := c.Pool.Begin(ctx)
	must(t, err)
	defer func() { _ = hold.Rollback(ctx) }()
	_, err = hold.Exec(ctx, `SELECT 1 FROM business_days WHERE property_id = $1 AND status = 'OPEN' FOR UPDATE`, c.propID)
	must(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CARD"))
		done <- err
	}()
	c.waitForLockWait(t, "%business_days%")
	next := roomstest.BD.AddDays(1)
	_, err = hold.Exec(ctx, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, c.propID)
	must(t, err)
	_, err = hold.Exec(ctx, `INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, $3)`, c.tenantID, c.propID, next.String())
	must(t, err)
	must(t, hold.Commit(ctx))
	must(t, <-done)
	if n := c.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND business_date = $2`, guest, next.String()); n != 2 {
		t.Fatalf("the return and the refund are both dated the next day: %d", n)
	}
	if n := c.Count(t, `SELECT count(*) FROM payments WHERE payment_type = 'REFUND' AND business_date = $1`, next.String()); n != 1 {
		t.Fatalf("the refund is dated the next day: %d", n)
	}
	eq(t, "the guest folio", c.balance(t, guest), "0")
}

func (c *closedFx) waitForLockWait(t *testing.T, like string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) //nolint:forbidigo // a test waits on the wall clock for another session (the approval is verified first, slowly under load)
	for c.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query ILIKE $1`, like) == 0 {
		if time.Now().After(deadline) { //nolint:forbidigo // as above
			t.Fatal("the other transaction never waited")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 12 and 13. Two returns of the same charge at once: one is made, the other is ALREADY_RETURNED. A refund of the same payment from the folio
// screen at the same time is refused (the folio is closed), so the payment is never refunded twice.
func TestKD26TwoReturnsOfTheSameChargeAndARefundAtOnce(t *testing.T) {
	c := newClosedFx(t)
	guest, _, charge, payment := c.checkedOut(t, "321")
	var wg sync.WaitGroup
	errs := make([]error, 3)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CARD"))
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[2] = c.Folios.Refund(c.admin, c.propID, payment, "kd26-screen-refund", folios.RefundInput{Amount: "60500", PaymentMethod: "CARD", Reason: "the guest asked", Approval: c.superIn})
	}()
	wg.Wait()
	ok := 0
	for _, err := range errs[:2] {
		if err == nil {
			ok++
		} else if e, isApp := asErr(err); !isApp || e.Code != "ALREADY_RETURNED" {
			t.Fatalf("the loser: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("one return is made: %v", errs)
	}
	wantCode(t, errs[2], "FOLIO_CLOSED")
	if n := c.Count(t, `SELECT count(*) FROM payments WHERE refund_of_payment_id = $1`, payment); n != 1 {
		t.Fatalf("the payment is refunded once: %d", n)
	}
	eq(t, "the guest folio", c.balance(t, guest), "0")
}

// 14. The return and a payment of the reopened bill at once: the bill's folio lock. The payment sees the balance after the return, or is
// refused as more than the bill owes; never both half done.
func TestKD26TheReturnAndAPaymentOfTheBillAtOnce(t *testing.T) {
	c := newClosedFx(t)
	c.shift(t)
	_, bill, charge, payment := c.checkedOut(t, "321")
	reopened, err := c.svc.ReopenBill(c.sup, c.propID, bill.ID, pos.ReopenInput{Reason: "wrong room", Approval: c.superIn})
	must(t, err)
	var wg sync.WaitGroup
	var retErr, payErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, retErr = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, charge, c.refund(payment, "CARD"))
	}()
	go func() {
		defer wg.Done()
		_, payErr = c.svc.PayBill(c.cashier, c.propID, reopened.ID, "kd26-race-pay-01", pos.PayInput{Payments: []pos.PayLine{{Method: "CASH", Amount: dec("60500")}}})
	}()
	wg.Wait()
	must(t, retErr)
	got, err := c.svc.GetBill(c.sup, c.propID, bill.ID)
	must(t, err)
	switch {
	case payErr == nil && (got.Status != "CLOSED" || !got.Totals.Balance.IsZero()):
		t.Fatalf("the payment after the return closes the bill: %s %s", got.Status, got.Totals.Balance)
	case payErr != nil && (got.Status != "OPEN" || !got.Totals.Balance.Equal(dec("60500"))):
		t.Fatalf("the payment refused, the bill owes the charge: %s %s (%v)", got.Status, got.Totals.Balance, payErr)
	}
}

// 15. Two returns of two charges of the same closed guest at once, from the same payment that covers one of them only: one is made, the
// other refused; the guest folio ends at zero, and the other payment then takes the second.
func TestKD26TwoReturnsFromTheSameClosedFolioAtOnce(t *testing.T) {
	c := newClosedFx(t)
	stay, guest := c.guestRoom(t, "321")
	_, first := c.chargeToRoom(t, stay)
	_, second := c.chargeToRoom(t, stay)
	a := c.payFolio(t, guest, "60500", "CARD")
	b := c.payFolio(t, guest, "60500", "CARD")
	c.checkOut(t, stay)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, ch := range []int64{first, second} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.svc.ReturnRoomChargeClosed(c.sup, c.propID, ch, c.refund(a, "CARD"))
		}()
	}
	wg.Wait()
	ok, lost := 0, -1
	for i, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "REFUND_EXCEEDS_PAYMENT")
			lost = i
		}
	}
	if ok != 1 || lost < 0 {
		t.Fatalf("one return is made: %v", errs)
	}
	eq(t, "the guest folio after one", c.balance(t, guest), "0")
	_, err := c.svc.ReturnRoomChargeClosed(c.sup, c.propID, []int64{first, second}[lost], c.refund(b, "CARD"))
	must(t, err)
	eq(t, "the guest folio after both", c.balance(t, guest), "0")
	if n := c.Count(t, `SELECT COALESCE(sum(amount), 0)::int FROM payments WHERE refund_of_payment_id IN ($1, $2)`, a, b); n != 121000 {
		t.Fatalf("refunded: %d", n)
	}
}

// 16. Every other path that writes to a folio refuses the closed folio with its own FOLIO_CLOSED, before the rule of the database is reached.
func TestKD26EveryOtherPathRefusesAClosedFolio(t *testing.T) {
	c := newClosedFx(t)
	guest, _, _, payment := c.checkedOut(t, "321")
	_, other := c.guestRoom(t, "322")
	var item int64
	must(t, c.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE' ORDER BY id LIMIT 1`, guest).Scan(&item))
	code := c.chargeCode(t, "RESTAURANT")
	co, err := c.Companies.Create(c.admin, c.propID, companies.Input{Code: "ACME", Name: "Acme Corp", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	before := c.items(t, guest)
	paths := map[string]func() error{
		"a charge": func() error {
			_, err := c.Folios.PostCharge(c.admin, c.propID, guest, "kd26-p-charge", folios.ChargeInput{ChargeCodeID: code, Quantity: "1", UnitPrice: ptr("1000")})
			return err
		},
		"an adjustment": func() error {
			_, err := c.Folios.PostAdjustment(c.admin, c.propID, guest, "kd26-p-adjust", folios.AdjustmentInput{ChargeCodeID: code, Amount: "1000", Reason: "x", Approval: c.superIn})
			return err
		},
		"a reversal": func() error {
			_, err := c.Folios.Reverse(c.admin, c.propID, item, folios.CorrectionInput{Reason: "x", Approval: c.superIn})
			return err
		},
		"a payment": func() error {
			_, err := c.Folios.PostPayment(c.admin, c.propID, guest, "kd26-p-pay", folios.PaymentInput{Amount: "1000", PaymentMethod: "CARD"})
			return err
		},
		"a refund": func() error {
			_, err := c.Folios.Refund(c.admin, c.propID, payment, "kd26-p-refund", folios.RefundInput{Amount: "1000", Reason: "x", Approval: c.superIn})
			return err
		},
		"a void": func() error {
			_, err := c.Folios.Void(c.admin, c.propID, payment, folios.CorrectionInput{Reason: "x", Approval: c.superIn})
			return err
		},
		"a transfer to the city ledger": func() error {
			_, err := c.Folios.Transfer(c.admin, c.propID, guest, "kd26-p-cl", folios.TransferInput{CompanyID: co.ID, Amount: "1000"})
			return err
		},
		"a balance from it": func() error {
			_, err := c.Folios.TransferBalance(c.admin, c.propID, guest, "kd26-p-bal1", folios.TransferBalanceInput{FolioID: other, Amount: "1000", Reason: "x", Approval: c.superIn})
			return err
		},
		"a balance to it": func() error {
			_, err := c.Folios.TransferBalance(c.admin, c.propID, other, "kd26-p-bal2", folios.TransferBalanceInput{FolioID: guest, Amount: "1000", Reason: "x", Approval: c.superIn})
			return err
		},
		"a charge moved from it": func() error {
			_, err := c.Folios.TransferItems(c.admin, c.propID, guest, folios.TransferItemsInput{FolioID: other, ItemIDs: []int64{item}, Reason: "x", Approval: c.superIn})
			return err
		},
	}
	for name, run := range paths {
		if e, ok := asErr(run()); !ok || e.Code != "FOLIO_CLOSED" {
			t.Fatalf("%s: %v, want FOLIO_CLOSED", name, e)
		}
	}
	if c.items(t, guest) != before {
		t.Fatal("nothing was written to the closed folio")
	}
}
