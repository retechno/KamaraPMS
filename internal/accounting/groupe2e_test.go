package accounting_test

import (
	"context"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

// One group over several days, from the booking to the close of the master (docs/architecture/30-group-folio.md): four rooms of which one stays out of the routing, the master with the routing of the
// room nights and the minibar, the check-in of the group with a deposit for two rooms and a deposit of the group, charges, a transfer between folios, the refund of a deposit, three night audits, check-outs
// one after the other (the last room that fills the master among them), the payment of the rest and the close of the master.
// On every day: the reconciliation agrees, the account of the transfers (1215) is zero, the advance deposits (2310) are the deposits not yet used, and the revenue is posted once.
func TestAGroupFromBookingToTheCloseOfItsMaster(t *testing.T) {
	h := setupHotel(t)
	r103 := h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	r104 := h.Room(t, h.admin, h.propID, h.typ.ID, "104", housekeeping.Clean)

	// the group: A leaves after one night, B after two, C and D after three; D stays out of the routing
	var group int64
	must(t, h.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date) VALUES ($1, $2, 'GRP1', 'Conference', '2026-09-30', '2026-10-03') RETURNING id`,
		h.tenantID, h.propID).Scan(&group))
	depart := []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-03"}
	rows := make([]frontdesk.GroupCheckInRow, 4)
	for i, out := range depart {
		r := h.book(t, "2026-09-30", out)
		must(t, h.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, group, r.ID))
		room := []int64{h.r101.ID, h.r102.ID, r103.ID, r104.ID}[i]
		rows[i] = frontdesk.GroupCheckInRow{LineID: r.Rooms[0].ID, Version: r.Version, RoomID: &room, GuestID: h.guest, AdultCount: 2}
	}
	lineD := rows[3].LineID

	// the master with the routing of the rooms and of the minibar, D left out
	var minibar int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, h.propID).Scan(&minibar))
	master := h.openMasterOf(t, group)
	_, err := h.Folios.SetGroupRouting(h.admin, h.propID, group, folios.GroupRoutingInput{
		Instructions:  []folios.GroupInstructionInput{{Scope: folios.ScopeRoom}, {Scope: folios.ScopeChargeCode, ChargeCodeID: &minibar}},
		ExcludedLines: []int64{lineD},
	})
	must(t, err)

	// the invariants of every day
	reconcile := func(date, when string) {
		t.Helper()
		asOf := civil.MustParseDate(date)
		rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
		must(t, err)
		if !rec.Reconciled {
			t.Fatalf("%s: the reconciliation must agree: %+v", when, rec)
		}
	}
	held := func(want string, when string) {
		t.Helper()
		if got := h.balance(t, "2310"); !got.Equal(dec(want)) {
			t.Fatalf("%s: advance deposits (2310) %s, want %s", when, got, want)
		}
	}
	transfersZero := func(when string) {
		t.Helper()
		if got := h.balance(t, "1215"); !got.IsZero() {
			t.Fatalf("%s: the account of the transfers (1215) is %s", when, got)
		}
	}
	closeDay := func(date string) {
		t.Helper()
		h.closeDay(t)
		transfersZero("after the close of " + date)
		reconcile(date, "after the close of "+date)
		h.requireBalanced(t)
	}
	nights := func(folio int64) int {
		return h.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND source = 'ROOM_POSTING' AND transaction_type = 'CHARGE'`, folio)
	}
	warned := func() int {
		pre, err := h.Audit.Preview(h.admin, h.propID)
		must(t, err)
		return len(pre.Warnings.OpenMasters)
	}

	// day 1 (30 Sep): the group checks in, a deposit for A (the incidental one, its nights go to the master) and for D (its stay is paid on its own folio), and a deposit of the group on the master
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	rows[0].Deposit = &frontdesk.GroupDepositInput{Amount: "200000", PaymentMethod: "BANK_TRANSFER"}
	rows[3].Deposit = &frontdesk.GroupDepositInput{Amount: "1300000", PaymentMethod: "BANK_TRANSFER"}
	in, err := h.Front.GroupCheckIn(h.admin, h.propID, group, "gci-1", frontdesk.GroupCheckInInput{Rows: []frontdesk.GroupCheckInRow{rows[0], rows[1], rows[2], rows[3]}})
	must(t, err)
	if in.CheckedIn != 4 || in.Failed != 0 || in.DepositsRecorded != 2 {
		t.Fatalf("four rooms and two deposits: %+v", in)
	}
	stayID := make([]int64, 4)
	folioOf := make([]int64, 4)
	for i := range rows {
		r := rowOf(t, in, rows[i].LineID)
		if r.Status != frontdesk.RowCheckedIn || r.Stay == nil {
			t.Fatalf("room %d: %+v", i, r)
		}
		stayID[i], folioOf[i] = r.Stay.ID, stayFolio(t, h, r.Stay.ID)
	}
	// the clock of the test stands still, so a deposit and the check-in of its row carry the same instant; in service the deposit of a row is taken first, a moment before the check-in, and that moment
	// is what makes it an advance deposit (2310) and not a settlement
	must(t, h.Exec(t, `UPDATE stays SET actual_check_in_at = actual_check_in_at + interval '1 second'`))
	// the repeat of the whole request in a new one finds everything and takes no deposit again
	again, err := h.Front.GroupCheckIn(h.admin, h.propID, group, "gci-2", frontdesk.GroupCheckInInput{Rows: []frontdesk.GroupCheckInRow{rows[0], rows[1], rows[2], rows[3]}})
	must(t, err)
	if again.DepositsRecorded != 0 || h.Count(t, `SELECT count(*) FROM payments`) != 2 {
		t.Fatalf("the repeat took a deposit again: %+v, %d payments", again, h.Count(t, `SELECT count(*) FROM payments`))
	}
	_, err = h.Folios.PostPayment(h.admin, h.propID, master, "group-deposit", folios.PaymentInput{Amount: "500000", PaymentMethod: "CARD"}) // before the first charge of the master: an advance deposit
	must(t, err)

	// charges: the minibar of A goes to the master by the routing; the minibar of D (left out) stays on its folio and is then moved to the master; the laundry of C is not routed and stays on its folio
	// the routing answers for the charges of the stays that are posted without a named folio: the minibar of A goes to the master, that of D (left out) to its own folio
	for i, want := range map[int]int64{0: master, 3: folioOf[3]} {
		target, err := h.Folios.ResolveTarget(h.admin, h.tenantID, h.propID, stayID[i], folios.Route{ChargeCodeID: minibar})
		must(t, err)
		if target.FolioID != want {
			t.Fatalf("the minibar of room %d goes to folio %d, want %d", i, target.FolioID, want)
		}
	}
	unit := "100000"
	_, err = h.Folios.PostCharge(h.admin, h.propID, master, "mb-a", folios.ChargeInput{ChargeCodeID: minibar, Quantity: "1", UnitPrice: &unit, StayID: &stayID[0]}) // the front desk posts it to the master for the stay of A
	must(t, err)
	h.charge(t, folioOf[3], "MINIBAR", "150000", "mb-d")
	h.charge(t, folioOf[2], "LAUNDRY", "40000", "ld-c")
	if got := h.folioBalance(t, master); !got.Equal(dec("-400000")) {
		t.Fatalf("the master holds the deposit of 500,000 less the minibar of A: %s", got)
	}
	if got := h.folioBalance(t, folioOf[0]); !got.Equal(dec("-200000")) {
		t.Fatalf("the minibar of A is not on its folio, only its deposit: %s", got)
	}
	if got := h.folioBalance(t, folioOf[2]); !got.Equal(dec("40000")) {
		t.Fatalf("the laundry stays on the folio of C: %s", got)
	}
	var itemD int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND charge_code_id = $2 AND transaction_type = 'CHARGE'`, folioOf[3], minibar).Scan(&itemD))
	_, err = h.Folios.TransferItems(h.admin, h.propID, folioOf[3], folios.TransferItemsInput{FolioID: master, ItemIDs: []int64{itemD}, Reason: "the company pays the minibar of D", Approval: h.approval()})
	must(t, err)
	if got := h.folioBalance(t, folioOf[3]); !got.Equal(dec("-1300000")) {
		t.Fatalf("D owes nothing for the minibar now, only its deposit is on the folio: %s", got)
	}

	if warned() != 0 {
		t.Fatal("the group is in house: no warning of an open master")
	}
	closeDay("2026-09-30")
	// four nights on day 1: three on the master (A, B, C), one on the folio of D
	if nights(master) != 3 || nights(folioOf[3]) != 1 || nights(folioOf[0])+nights(folioOf[1])+nights(folioOf[2]) != 0 {
		t.Fatalf("day 1 nights: master %d, D %d", nights(master), nights(folioOf[3]))
	}
	// deposits held: A 200,000 + D 1,300,000 + the group 500,000
	held("-2000000", "day 1")

	// day 2 (1 Oct): A leaves. Its deposit is refunded first (a credit on a folio is not left behind)
	var depA int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM payments WHERE folio_id = $1`, folioOf[0]).Scan(&depA))
	_, err = h.Folios.Refund(h.admin, h.propID, depA, "refund-a", folios.RefundInput{Amount: "200000", Reason: "left, nothing used", Approval: h.approval()})
	must(t, err)
	outOf := func(i int) frontdesk.CheckOutResult {
		t.Helper()
		st, err := h.Front.GetStay(h.admin, h.propID, stayID[i])
		must(t, err)
		r, err := h.Front.CheckOut(h.admin, h.propID, stayID[i], frontdesk.CheckOutInput{Version: st.Stay.Version})
		must(t, err)
		return r
	}
	if first := outOf(0); first.MasterFolio != nil || warned() != 0 {
		t.Fatalf("A is not the last room: notice %+v, warnings %d", first.MasterFolio, warned())
	}
	closeDay("2026-10-01")
	held("-1800000", "day 2") // the deposit of A was refunded: D and the group remain
	if nights(master) != 5 || nights(folioOf[3]) != 2 {
		t.Fatalf("day 2 nights: master %d (B and C), D %d", nights(master), nights(folioOf[3]))
	}

	// day 3 (2 Oct): B leaves
	if b := outOf(1); b.MasterFolio != nil || warned() != 0 {
		t.Fatalf("B is not the last room: %+v", b.MasterFolio)
	}
	closeDay("2026-10-02")
	held("-1800000", "day 3")
	if nights(master) != 6 || nights(folioOf[3]) != 3 {
		t.Fatalf("day 3 nights: master %d (C), D %d", nights(master), nights(folioOf[3]))
	}

	// day 4 (3 Oct): D (left out of the routing) pays its stay and leaves; the master is still wanted by C, so nothing is said
	owedD := h.folioBalance(t, folioOf[3]) // 3 nights less the deposit
	if !owedD.Equal(dec("1700000")) {
		t.Fatalf("D owes 3,000,000 less the deposit of 1,300,000: %s", owedD)
	}
	_, err = h.Folios.PostPayment(h.admin, h.propID, folioOf[3], "pay-d", folios.PaymentInput{Amount: owedD.String(), PaymentMethod: "CARD"})
	must(t, err)
	if d := outOf(3); d.MasterFolio != nil || warned() != 0 {
		t.Fatalf("C still fills the master: %+v, warnings %d", d.MasterFolio, warned())
	}
	// C pays its laundry and leaves last: the group has left and its bill is open. Both the notice and the night audit say so
	_, err = h.Folios.PostPayment(h.admin, h.propID, folioOf[2], "pay-c", folios.PaymentInput{Amount: "40000", PaymentMethod: "CARD"})
	must(t, err)
	last := outOf(2)
	if last.MasterFolio == nil || last.MasterFolio.FolioID != master || last.MasterFolio.GroupCode != "GRP1" {
		t.Fatalf("the last room says the master is open: %+v", last.MasterFolio)
	}
	if warned() != 1 {
		t.Fatal("the night audit warns of the open master of a group that has left")
	}

	// the master: the nights of A (1), B (2) and C (3) = 6,000,000, the minibar of A 100,000 and of D 150,000, less the deposit of the group 500,000
	info, err := h.Folios.GetFolio(h.admin, h.propID, master)
	must(t, err)
	if info.Balance != "5750000" {
		t.Fatalf("the master owes 6,250,000 less 500,000: %s", info.Balance)
	}
	// the master cannot close with a balance; it is paid, then closed
	if _, err := h.Folios.CloseFolio(h.admin, h.propID, master, info.Version); err == nil {
		t.Fatal("a master with a balance does not close")
	}
	_, err = h.Folios.PostPayment(h.admin, h.propID, master, "pay-master", folios.PaymentInput{Amount: info.Balance, PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	info, err = h.Folios.GetFolio(h.admin, h.propID, master)
	must(t, err)
	if info.Balance != "0" {
		t.Fatalf("the master is paid: %s", info.Balance)
	}
	if _, err = h.Folios.CloseFolio(h.admin, h.propID, master, info.Version); err != nil {
		t.Fatalf("closing the master: %v", err)
	}
	if warned() != 0 {
		t.Fatal("no warning once the master is closed")
	}
	closeDay("2026-10-03")
	// every deposit is used or released: nothing is held, nothing is owed, and the revenue was posted once
	held("0", "the end")
	if got := h.balance(t, "1210"); !got.IsZero() {
		t.Fatalf("guest ledger at the end: %s", got)
	}
	if got := h.balance(t, "4110"); !got.Equal(dec("-9000000")) { // 1 + 2 + 3 + 3 nights of 1,000,000, each once
		t.Fatalf("room revenue %s: a night posted twice or not at all", got)
	}
	if got := h.balance(t, "4230"); !got.Equal(dec("-250000")) { // the minibar of A and of D, each once, though D's was moved to the master
		t.Fatalf("minibar revenue %s", got)
	}
	if got := h.balance(t, "4350"); !got.Equal(dec("-40000")) {
		t.Fatalf("laundry revenue %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`); n != 9 {
		t.Fatalf("%d nights in the register, want 9", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM (SELECT stay_id, service_date FROM stay_charge_postings WHERE status = 'POSTED' GROUP BY 1, 2 HAVING count(*) > 1) x`); n != 0 {
		t.Fatalf("%d nights posted twice", n)
	}
}
