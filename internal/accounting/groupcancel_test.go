package accounting_test

import (
	"context"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// The cancellation of the rooms of a group (docs/architecture/30-group-folio.md part F): each reservation in its own transaction, the rooms in house skipped, the deposits listed and never moved, the
// follow-up of what is kept, and the books and the reconciliation agreeing with every decision.

func (h *hotel) cancelRows(rs ...reservations.Reservation) []reservations.GroupCancelRow {
	out := make([]reservations.GroupCancelRow, len(rs))
	for i, r := range rs {
		out[i] = reservations.GroupCancelRow{ReservationID: r.ID, Version: r.Version}
	}
	return out
}

func (h *hotel) groupCancel(t *testing.T, group int64, key string, rows ...reservations.GroupCancelRow) reservations.GroupCancelResult {
	t.Helper()
	out, err := h.Res.GroupCancel(h.admin, h.propID, group, key, reservations.GroupCancelInput{Reason: "the conference is off", Rows: rows})
	must(t, err)
	return out
}

func cancelRow(t *testing.T, r reservations.GroupCancelResult, id int64) reservations.GroupCancelRowResult {
	t.Helper()
	for _, x := range r.Rows {
		if x.ReservationID == id {
			return x
		}
	}
	t.Fatalf("no row for the reservation %d in %+v", id, r.Rows)
	return reservations.GroupCancelRowResult{}
}

// twoRooms books a reservation of two rooms for the group.
func (h *hotel) twoRooms(t *testing.T, group int64) reservations.Reservation {
	t.Helper()
	line := reservations.LineInput{RoomTypeID: h.typ.ID, RatePlanID: h.plan, Arrival: d("2026-09-30"), Departure: d("2026-10-01"), Adults: 2}
	res, err := h.Res.Create(h.admin, h.propID, "", reservations.CreateInput{GuestID: &h.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{line, line}})
	must(t, err)
	must(t, h.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, group, res.ID))
	return res
}

func (h *hotel) lineStatus(t *testing.T, line int64) string {
	t.Helper()
	var s string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT status FROM reservation_rooms WHERE id = $1`, line).Scan(&s))
	return s
}

func TestAGroupCancelIsPartialSkipsTheRoomsInHouseAndARepeatFindsWhatWasDone(t *testing.T) {
	h := setupHotel(t)
	r103 := h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	h.Room(t, h.admin, h.propID, h.typ.ID, "104", housekeeping.Clean)
	h.Room(t, h.admin, h.propID, h.typ.ID, "105", housekeeping.Clean)
	h.Room(t, h.admin, h.propID, h.typ.ID, "106", housekeeping.Clean) // six rooms: three reservations, the one of two rooms and the one of another booking
	group, rs := h.groupArrivals(t, h.r101, h.r102, r103)
	a, b, c := rs[0], rs[1], rs[2]
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	h.checkIn(t, b, h.r102)     // B is in house
	two := h.twoRooms(t, group) // D has two rooms
	_, err := h.Front.CheckIn(h.admin, h.propID, two.ID, two.Rooms[0].ID, "d1", frontdesk.CheckInInput{Version: two.Version, RoomID: &r103.ID, GuestID: h.guest, AdultCount: 2})
	must(t, err) // one room of D is in house, the other waits
	two, err = h.Res.Get(h.admin, h.propID, two.ID)
	must(t, err)
	foreign := h.book(t, "2026-09-30", "2026-10-01")
	stale := c
	stale.Version = 999

	rows := append(h.cancelRows(a, b, two, foreign), reservations.GroupCancelRow{ReservationID: stale.ID, Version: stale.Version})
	first := h.groupCancel(t, group, "K1", rows...)
	if first.BatchID != "K1" || first.Cancelled != 1 || first.PartlyCancelled != 1 || first.Skipped != 1 || first.Failed != 2 || first.AlreadyCancelled != 0 {
		t.Fatalf("A is cancelled, D partly, B skipped; the stale version and the foreign reservation fail: %+v", first)
	}
	if r := cancelRow(t, first, a.ID); r.Status != reservations.RowCancelled || len(r.Rooms) != 1 || r.Rooms[0].Status != reservations.RowCancelled {
		t.Fatalf("A: %+v", r)
	}
	if r := cancelRow(t, first, b.ID); r.Status != reservations.RowSkipped || r.Code != "ALREADY_CHECKED_IN" || r.Rooms[0].Status != reservations.RowSkipped {
		t.Fatalf("B is in house: %+v", r)
	}
	d := cancelRow(t, first, two.ID)
	if d.Status != reservations.RowPartlyCancelled || len(d.Rooms) != 2 {
		t.Fatalf("D: the room that waits is cancelled, the one in house stays: %+v", d)
	}
	if h.lineStatus(t, two.Rooms[0].ID) != "CHECKED_IN" || h.lineStatus(t, two.Rooms[1].ID) != "CANCELLED" {
		t.Fatalf("lines of D: %s, %s", h.lineStatus(t, two.Rooms[0].ID), h.lineStatus(t, two.Rooms[1].ID))
	}
	if st := h.Count(t, `SELECT count(*) FROM reservations WHERE id = $1 AND status = 'CANCELLED'`, two.ID); st != 0 {
		t.Fatal("D is not cancelled as a whole: one of its rooms is in house")
	}
	if r := cancelRow(t, first, foreign.ID); r.Status != reservations.RowFailed || r.Code != "RESERVATION_NOT_IN_GROUP" {
		t.Fatalf("a reservation of another booking: %+v", r)
	}
	if r := cancelRow(t, first, stale.ID); r.Status != reservations.RowFailed || r.Code == "" {
		t.Fatalf("a stale version is told as it is: %+v", r)
	}
	if first.RoomsCancelled != 2 || first.RoomsSkipped != 2 {
		t.Fatalf("rooms: %d cancelled, %d skipped", first.RoomsCancelled, first.RoomsSkipped)
	}
	if h.lineStatus(t, a.Rooms[0].ID) != "CANCELLED" || h.lineStatus(t, c.Rooms[0].ID) != "CONFIRMED" {
		t.Fatal("A is cancelled; C was not named and waits")
	}

	// the repeat is a NEW request with the same (now old) versions: what was done is answered as it was, nothing is done twice
	again := h.groupCancel(t, group, "K2", rows[:3]...)
	if again.Cancelled != 0 || again.AlreadyCancelled != 1 || again.Skipped != 2 || again.Failed != 0 || again.RoomsCancelled != 0 {
		t.Fatalf("the repeat: %+v", again)
	}
	if r := cancelRow(t, again, a.ID); r.Status != reservations.RowAlreadyCancelled {
		t.Fatalf("A on the repeat: %+v", r)
	}
	if r := cancelRow(t, again, two.ID); r.Status != reservations.RowSkipped || r.Code != "ALREADY_CHECKED_IN" {
		t.Fatalf("D on the repeat has only the room in house left: %+v", r)
	}

	// C is cancelled by a request of its own, with the version it has now
	cur, err := h.Res.Get(h.admin, h.propID, c.ID)
	must(t, err)
	third := h.groupCancel(t, group, "K3", reservations.GroupCancelRow{ReservationID: c.ID, Version: cur.Version})
	if third.Cancelled != 1 {
		t.Fatalf("%+v", third)
	}

	// the audit: the entries of the reservations carry the batch of their request, and the group has one entry for each request that cancelled something
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('reservation.cancelled', 'reservation.room_cancelled') AND new_data->>'batch_id' = 'K1'`); n != 2 {
		t.Fatalf("%d entries of K1, want 2 (A whole, the room of D)", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'group.cancelled'`); n != 2 {
		t.Fatalf("%d group entries: K1 and K3 cancelled something, K2 did not", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'group.cancelled' AND new_data->>'batch_id' = 'K1' AND (new_data->>'rooms_cancelled')::int = 2 AND (new_data->>'rooms_skipped')::int = 2`); n != 1 {
		t.Fatal("the entry of the group says what K1 did")
	}
	// the cancelled rooms lose their room, so the inventory is free again
	if n := h.Count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'CANCELLED' AND room_id IS NOT NULL`); n != 0 {
		t.Fatalf("%d cancelled lines still hold a room", n)
	}
}

func TestACancellationOfAGroupNeedsTheRightToCancelAGroup(t *testing.T) {
	h := setupHotel(t)
	group, rs := h.groupArrivals(t, h.r101, h.r102)
	clerk := h.User(t, h.tenantID, h.propID, auth.PermReservationCancel, auth.PermReservationRead)
	_, err := h.Res.GroupCancel(clerk, h.propID, group, "K1", reservations.GroupCancelInput{Reason: "off", Rows: h.cancelRows(rs...)})
	wantCode(t, err, "PERMISSION_DENIED")
	if h.Count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'CANCELLED'`) != 0 {
		t.Fatal("nothing was done: the right to cancel one reservation is not the right to cancel a group")
	}
	boss := h.User(t, h.tenantID, h.propID, auth.PermGroupCancel, auth.PermReservationRead)
	out, err := h.Res.GroupCancel(boss, h.propID, group, "K2", reservations.GroupCancelInput{Reason: "off", Rows: h.cancelRows(rs...)})
	must(t, err)
	if out.Cancelled != 2 {
		t.Fatalf("%+v", out)
	}
	// the shape of a request
	_, err = h.Res.GroupCancel(h.admin, h.propID, group, "K3", reservations.GroupCancelInput{Reason: "off"})
	wantCode(t, err, "GROUP_CANCEL_EMPTY")
	_, err = h.Res.GroupCancel(h.admin, h.propID, group, "K4", reservations.GroupCancelInput{Rows: h.cancelRows(rs...)})
	wantCode(t, err, "VALIDATION_FAILED")
	many := make([]reservations.GroupCancelRow, 101)
	for i := range many {
		many[i] = reservations.GroupCancelRow{ReservationID: int64(i + 1), Version: 1}
	}
	_, err = h.Res.GroupCancel(h.admin, h.propID, group, "K5", reservations.GroupCancelInput{Reason: "off", Rows: many})
	wantCode(t, err, "TOO_MANY_ROWS")
	_, err = h.Res.GroupCancel(h.admin, h.propID, group, "K6", reservations.GroupCancelInput{Reason: "off", Rows: append(h.cancelRows(rs[0]), h.cancelRows(rs[0])...)})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = h.Res.GroupCancel(h.admin, h.propID, group+99, "K7", reservations.GroupCancelInput{Reason: "off", Rows: h.cancelRows(rs[0])})
	wantCode(t, err, "GROUP_NOT_FOUND")
}

// The deposits of the rooms and of the group, and what a person decides for each: kept, refunded, or turned into the fee of the cancellation. The cancellation moves no money by itself; the books and the
// reconciliation agree on every day, and what is kept stays visible: in the list of folios and in the warnings of the night audit, until it is settled.
func TestACancelledGroupLeavesNoDepositUnseenAndTheBooksAgree(t *testing.T) {
	h := setupHotel(t)
	group, rs := h.groupArrivals(t, h.r101, h.r102, h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean))
	a, b, c := rs[0], rs[1], rs[2]
	dep := func(r reservations.Reservation, amount, key string) folios.PaymentResult {
		out, err := h.Folios.Deposit(h.admin, h.propID, r.ID, key, folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
		must(t, err)
		return out
	}
	depA, depB, depC := dep(a, "300000", "da"), dep(b, "200000", "db"), dep(c, "300000", "dc")
	master := h.openMasterOf(t, group)
	groupDep, err := h.Folios.PostPayment(h.admin, h.propID, master, "gd", folios.PaymentInput{Amount: "500000", PaymentMethod: "CASH"})
	must(t, err)
	reconcile := func(date, when string) {
		t.Helper()
		asOf := civil.MustParseDate(date)
		rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
		must(t, err)
		if !rec.Reconciled {
			t.Fatalf("%s: the reconciliation must agree: %+v", when, rec)
		}
	}

	out := h.groupCancel(t, group, "K1", h.cancelRows(a, b, c)...)
	if out.Cancelled != 3 || out.DepositsHeld != 3 {
		t.Fatalf("three reservations cancelled, each still holding its deposit: %+v", out)
	}
	for i, r := range []reservations.Reservation{a, b, c} {
		row := cancelRow(t, out, r.ID)
		if len(row.Deposits) != 1 || row.Deposits[0].PaymentID != []int64{depA.Payment.ID, depB.Payment.ID, depC.Payment.ID}[i] {
			t.Fatalf("the deposit of room %d is listed with the payment that can be refunded: %+v", i, row.Deposits)
		}
	}
	if got := h.balance(t, "2310"); !got.IsZero() { // nothing is journaled until the day closes; the cancellation itself posted nothing
		t.Fatalf("the cancellation moved no money: 2310 %s", got)
	}

	// the follow-up exists as long as something is held: the list of folios and the warnings of the night audit say so, with the folio to open
	held := func() []string {
		list, err := h.Folios.ListFolios(h.admin, h.propID, folios.FolioFilter{CancelledWithBalance: true}, listsort.Sort{}, nil, 50)
		must(t, err)
		var out []string
		for _, f := range list.Data {
			out = append(out, f.FolioNumber)
		}
		return out
	}
	if len(held()) != 3 {
		t.Fatalf("the filter of the follow-up lists the three folios that hold a deposit: %v", held())
	}
	holdings := func() []reservations.GroupHolding {
		got, err := h.Res.GroupHoldings(h.admin, h.propID, group)
		must(t, err)
		return got
	}
	if got := holdings(); len(got) != 3 || got[0].HeldBalance != "-300000" || len(got[0].Deposits) != 1 || got[0].Deposits[0].Refundable != "300000" {
		t.Fatalf("the group page lists what each cancelled reservation holds, with the deposit to refund: %+v", got)
	}
	warned := func() int {
		pre, err := h.Audit.Preview(h.admin, h.propID)
		must(t, err)
		return len(pre.Warnings.OpenFolios)
	}
	if warned() != 3 {
		t.Fatalf("the night audit warns of the three folios of cancelled reservations that hold a deposit: %d", warned())
	}

	// the decisions: A is kept; B is refunded in full; C is turned into the fee of the cancellation (200,000 of its 300,000, the rest stays); the deposit of the group is refunded
	_, err = h.Folios.Refund(h.admin, h.propID, depB.Payment.ID, "rf-b", folios.RefundInput{Amount: "200000", Reason: "the conference is off", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.PostReservationFee(h.admin, h.propID, c.ID, folios.FeeInput{Type: "CANCEL_FEE", Amount: "200000", Reason: "the deposit pays the fee"})
	must(t, err)
	_, err = h.Folios.Refund(h.admin, h.propID, groupDep.Payment.ID, "rf-g", folios.RefundInput{Amount: "500000", Reason: "the conference is off", Approval: h.approval()})
	must(t, err)
	if got := h.folioBalance(t, master); !got.IsZero() {
		t.Fatalf("the master holds nothing: %s", got)
	}
	cur, err := h.Folios.GetFolio(h.admin, h.propID, master)
	must(t, err)
	if _, err := h.Folios.CloseFolio(h.admin, h.propID, master, cur.Version); err != nil {
		t.Fatalf("the master of a group that is all cancelled closes once nothing is held: %v", err)
	}
	if len(held()) != 2 || warned() != 2 {
		t.Fatalf("A (kept) and C (the rest of the deposit) are still followed up, B is not: %v, %d warnings", held(), warned())
	}

	if got := holdings(); len(got) != 2 || got[0].HeldBalance != "-300000" || got[1].HeldBalance != "-100000" || got[1].Deposits[0].Refundable != "300000" {
		t.Fatalf("A is kept and C holds the rest of its deposit (the fee is not a refund of it): %+v", got)
	}
	h.closeDay(t)
	if got := h.balance(t, "2310"); !got.Equal(dec("-600000")) { // A 300,000 and C 300,000; B and the group were refunded
		t.Fatalf("the deposits still held: %s", got)
	}
	if got := h.balance(t, "4510"); !got.Equal(dec("-200000")) {
		t.Fatalf("the fee of the cancellation is posted once: 4510 %s", got)
	}
	if got := h.balance(t, "1215"); !got.IsZero() {
		t.Fatalf("1215 %s", got)
	}
	h.requireBalanced(t)
	reconcile("2026-09-30", "after the day of the cancellation")
}

// A reinstatement of the group is told per reservation: the rooms that were taken meanwhile are failed rows with a clear code, not an error for the whole request.
func TestAGroupIsReinstatedPerReservationAndATakenRoomIsTold(t *testing.T) {
	h := setupHotel(t)
	h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	h.Room(t, h.admin, h.propID, h.typ.ID, "104", housekeeping.Clean) // four rooms of the type
	group, rs := h.groupArrivals(t, h.r101, h.r102, h.r101)
	out := h.groupCancel(t, group, "K1", h.cancelRows(rs...)...)
	if out.Cancelled != 3 {
		t.Fatalf("%+v", out)
	}
	// two other bookings take two of the four rooms for the nights; only two are left for the three that come back
	h.book(t, "2026-09-30", "2026-10-01")
	h.book(t, "2026-09-30", "2026-10-01")
	cur := make([]reservations.GroupCancelRow, len(rs))
	for i, r := range rs {
		got, err := h.Res.Get(h.admin, h.propID, r.ID)
		must(t, err)
		cur[i] = reservations.GroupCancelRow{ReservationID: r.ID, Version: got.Version}
	}
	back, err := h.Res.GroupReinstate(h.admin, h.propID, group, "R1", reservations.GroupReinstateInput{Rows: cur})
	must(t, err)
	if back.Cancelled != 2 || back.Failed != 1 || back.Skipped != 0 {
		t.Fatalf("two come back, the third finds no room: %+v", back)
	}
	last := cancelRow(t, back, rs[2].ID)
	if last.Status != reservations.RowFailed || last.Code != "ROOM_UNAVAILABLE" || last.Message == "" {
		t.Fatalf("the room that was taken: %+v", last)
	}
	if n := h.Count(t, `SELECT count(*) FROM reservations WHERE booking_group_id = $1 AND status = 'CONFIRMED'`, group); n != 2 {
		t.Fatalf("%d reservations of the group are confirmed again", n)
	}
	// a reservation that is not cancelled is skipped, and the audit says what the request did
	again, err := h.Res.GroupReinstate(h.admin, h.propID, group, "R2", reservations.GroupReinstateInput{Rows: cur[:1]})
	must(t, err)
	if r := cancelRow(t, again, rs[0].ID); r.Status != reservations.RowSkipped || r.Code != "NOT_CANCELLED" {
		t.Fatalf("%+v", r)
	}
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.reinstated' AND new_data->>'batch_id' = 'R1'`); n != 2 {
		t.Fatalf("%d reinstatements carry the batch", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'group.reinstated' AND new_data->>'batch_id' = 'R1' AND (new_data->>'reinstated')::int = 2`); n != 1 {
		t.Fatal("the entry of the group")
	}
}
