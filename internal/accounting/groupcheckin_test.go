package accounting_test

import (
	"context"
	"strings"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
)

// The check-in of a group (docs/architecture/30-group-folio.md 6.2, 6.4, 14): each room in its own transaction, the keys made from the line so that a repeat in a new request finds what the first did,
// the deposit that is never taken twice, and the books and the reconciliation agreeing with it.

// groupArrivals books one confirmed room for each room given, arriving on the business date, in a new group. Nothing is checked in.
func (h *hotel) groupArrivals(t *testing.T, rs ...rooms.Room) (int64, []reservations.Reservation) {
	t.Helper()
	var group int64
	must(t, h.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, arrival_date, departure_date) VALUES ($1, $2, 'GRP1', 'Conference', '2026-09-30', '2026-10-01') RETURNING id`,
		h.tenantID, h.propID).Scan(&group))
	out := make([]reservations.Reservation, len(rs))
	for i := range rs {
		out[i] = h.book(t, "2026-09-30", "2026-10-01")
		must(t, h.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, group, out[i].ID))
	}
	return group, out
}

func (h *hotel) gciRow(res reservations.Reservation, room rooms.Room) frontdesk.GroupCheckInRow {
	return frontdesk.GroupCheckInRow{LineID: res.Rooms[0].ID, Version: res.Version, RoomID: &room.ID, GuestID: h.guest, AdultCount: 2}
}

func (h *hotel) gci(t *testing.T, group int64, key string, rows ...frontdesk.GroupCheckInRow) frontdesk.GroupCheckInResult {
	t.Helper()
	out, err := h.Front.GroupCheckIn(h.admin, h.propID, group, key, frontdesk.GroupCheckInInput{Rows: rows})
	must(t, err)
	return out
}

func rowOf(t *testing.T, r frontdesk.GroupCheckInResult, line int64) frontdesk.GroupCheckInRowResult {
	t.Helper()
	for _, x := range r.Rows {
		if x.LineID == line {
			return x
		}
	}
	t.Fatalf("no row for the line %d in %+v", line, r.Rows)
	return frontdesk.GroupCheckInRowResult{}
}

func (h *hotel) setClean(t *testing.T, room rooms.Room) {
	t.Helper()
	must(t, h.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, room.ID))
}

func (h *hotel) paymentsOf(t *testing.T, resID int64) int {
	t.Helper()
	return h.Count(t, `SELECT count(*) FROM payments p JOIN folios f ON f.id = p.folio_id WHERE f.reservation_id = $1`, resID)
}

func TestAGroupCheckInIsPartialAndARepeatFindsWhatTheFirstRequestDid(t *testing.T) {
	h := setupHotel(t)
	r103 := h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	r104 := h.Room(t, h.admin, h.propID, h.typ.ID, "104", housekeeping.Clean)
	r105 := h.Room(t, h.admin, h.propID, h.typ.ID, "105", housekeeping.Clean)
	h.Room(t, h.admin, h.propID, h.typ.ID, "107", housekeeping.Clean) // the room type has a room for every reservation of the day
	h.Room(t, h.admin, h.propID, h.typ.ID, "108", housekeeping.Clean)
	// 102 is not ready, 103 is occupied by a guest that is not of the group, 104 has no guest named in its row
	must(t, h.Exec(t, `UPDATE room_housekeeping SET status = 'DIRTY' WHERE room_id = $1`, h.r102.ID))
	h.checkIn(t, h.book(t, "2026-09-30", "2026-10-01"), r103)
	group, rs := h.groupArrivals(t, h.r101, h.r102, r103, r104, r105)
	rowA, rowB, rowC, rowD, rowE := h.gciRow(rs[0], h.r101), h.gciRow(rs[1], h.r102), h.gciRow(rs[2], r103), h.gciRow(rs[3], r104), h.gciRow(rs[4], r105)
	rowD.GuestID = 0

	first := h.gci(t, group, "K1", rowE, rowD, rowC, rowB, rowA) // the order of the request does not matter: the rows are taken by line
	if first.CheckedIn != 2 || first.Failed != 3 || first.BatchID != "K1" {
		t.Fatalf("two check in, three are told why: %+v", first)
	}
	a, e := rowOf(t, first, rowA.LineID), rowOf(t, first, rowE.LineID)
	if a.Status != frontdesk.RowCheckedIn || a.Stay == nil || e.Status != frontdesk.RowCheckedIn || e.Stay == nil {
		t.Fatalf("A and E: %+v %+v", a, e)
	}
	if b := rowOf(t, first, rowB.LineID); b.Status != frontdesk.RowFailed || b.Code != "ROOM_NOT_READY" {
		t.Fatalf("B: %+v", b)
	}
	if c := rowOf(t, first, rowC.LineID); c.Status != frontdesk.RowFailed || c.Code != "ROOM_OCCUPIED" {
		t.Fatalf("C: %+v", c)
	}
	if d := rowOf(t, first, rowD.LineID); d.Status != frontdesk.RowFailed || d.Code == "" {
		t.Fatalf("D: %+v", d)
	}
	if h.Count(t, `SELECT count(*) FROM stays`) != 3 { // the occupier and the two that went through: nothing was rolled back, nothing is half done
		t.Fatal("three stays")
	}

	// the repeat is a NEW request (another Idempotency-Key): the two that went through answer with the same stay, the rooms that failed are tried again, nothing is done twice
	h.setClean(t, h.r102)
	rowD.GuestID = h.guest
	second := h.gci(t, group, "K2", rowA, rowB, rowC, rowD, rowE)
	a2, e2 := rowOf(t, second, rowA.LineID), rowOf(t, second, rowE.LineID)
	if a2.Status != frontdesk.RowCheckedIn || a2.Stay.ID != a.Stay.ID || a2.Stay.StayNumber != a.Stay.StayNumber || e2.Stay.ID != e.Stay.ID {
		t.Fatalf("the rooms that went through answer with the same stays: %+v %+v", a2, e2)
	}
	if b := rowOf(t, second, rowB.LineID); b.Status != frontdesk.RowCheckedIn {
		t.Fatalf("B checks in once its room is ready: %+v", b)
	}
	if d := rowOf(t, second, rowD.LineID); d.Status != frontdesk.RowCheckedIn {
		t.Fatalf("D checks in once it has a guest: %+v", d)
	}
	if c := rowOf(t, second, rowC.LineID); c.Status != frontdesk.RowFailed || c.Code != "ROOM_OCCUPIED" {
		t.Fatalf("C is still occupied: %+v", c)
	}
	if n, numbers := h.Count(t, `SELECT count(*) FROM stays`), h.Count(t, `SELECT count(DISTINCT stay_number) FROM stays`); n != 5 || numbers != 5 {
		t.Fatalf("one stay for each room that checked in and no stay number used twice: %d stays, %d numbers", n, numbers)
	}
	// a room that was checked in meanwhile by someone else is told as it is, not as an error
	other := h.Room(t, h.admin, h.propID, h.typ.ID, "106", housekeeping.Clean)
	_, more := h.groupArrivals2(t, group, 1)
	solo, err := h.Front.CheckIn(h.admin, h.propID, more[0].ID, more[0].Rooms[0].ID, "someone-else", frontdesk.CheckInInput{Version: more[0].Version, RoomID: &other.ID, GuestID: h.guest, AdultCount: 2})
	must(t, err)
	third := h.gci(t, group, "K3", h.gciRow(more[0], other))
	if r := rowOf(t, third, more[0].Rooms[0].ID); r.Status != frontdesk.RowAlreadyCheckedIn || r.StayID != solo.Stay.ID {
		t.Fatalf("checked in by someone else: %+v", r)
	}
	// a room that is not of the group is a failed row, not a check-in
	foreign := h.book(t, "2026-09-30", "2026-10-01")
	out := h.gci(t, group, "K4", h.gciRow(foreign, h.r101))
	if r := rowOf(t, out, foreign.Rooms[0].ID); r.Status != frontdesk.RowFailed || r.Code != "LINE_NOT_IN_GROUP" {
		t.Fatalf("a room of another reservation: %+v", r)
	}
}

// groupArrivals2 adds more confirmed rooms to a group that exists.
func (h *hotel) groupArrivals2(t *testing.T, group int64, n int) (int64, []reservations.Reservation) {
	t.Helper()
	out := make([]reservations.Reservation, n)
	for i := range out {
		out[i] = h.book(t, "2026-09-30", "2026-10-01")
		must(t, h.Exec(t, `UPDATE reservations SET booking_group_id = $1 WHERE id = $2`, group, out[i].ID))
	}
	return group, out
}

func TestADepositThatWentThroughIsNotTakenAgainWhenTheRowIsRepeatedInANewRequest(t *testing.T) {
	h := setupHotel(t)
	must(t, h.Exec(t, `UPDATE room_housekeeping SET status = 'DIRTY' WHERE room_id = $1`, h.r101.ID))
	group, rs := h.groupArrivals(t, h.r101)
	row := h.gciRow(rs[0], h.r101)
	row.Deposit = &frontdesk.GroupDepositInput{Amount: "500000", PaymentMethod: "BANK_TRANSFER"}

	first := h.gci(t, group, "K1", row)
	r := rowOf(t, first, row.LineID)
	if r.Status != frontdesk.RowFailed || r.Code != "ROOM_NOT_READY" || r.Deposit == nil || r.Deposit.Status != "RECORDED" || first.DepositsRecorded != 1 {
		t.Fatalf("the deposit went through and the check-in did not: %+v", r)
	}
	if h.paymentsOf(t, rs[0].ID) != 1 {
		t.Fatal("one deposit")
	}

	// the room is made ready and the row is sent again in a NEW request: the deposit is found, not taken a second time, and the room checks in
	h.setClean(t, h.r101)
	second := h.gci(t, group, "K2", row)
	r = rowOf(t, second, row.LineID)
	if r.Status != frontdesk.RowCheckedIn || r.Deposit == nil || r.Deposit.Status != "REPLAYED" || second.DepositsRecorded != 0 {
		t.Fatalf("the repeat: %+v", r)
	}
	if h.paymentsOf(t, rs[0].ID) != 1 {
		t.Fatalf("the deposit was taken twice: %d payments", h.paymentsOf(t, rs[0].ID))
	}
	// the deposit is on the folio of the stay, as an advance deposit
	if got := h.folioBalance(t, stayFolio(t, h, r.Stay.ID)); !got.Equal(dec("-500000")) {
		t.Fatalf("the folio of the stay holds the deposit: %s", got)
	}
}

func stayFolio(t *testing.T, h *hotel, stay int64) int64 {
	t.Helper()
	var id int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE stay_id = $1 AND folio_type = 'GUEST'`, stay).Scan(&id))
	return id
}

func TestTheSameDepositKeyWithAnotherAmountOrMethodIsRefusedOnTheRow(t *testing.T) {
	h := setupHotel(t)
	must(t, h.Exec(t, `UPDATE room_housekeeping SET status = 'DIRTY' WHERE room_id = $1`, h.r101.ID))
	group, rs := h.groupArrivals(t, h.r101)
	row := h.gciRow(rs[0], h.r101)
	row.Deposit = &frontdesk.GroupDepositInput{Amount: "300000", PaymentMethod: "BANK_TRANSFER"}
	h.gci(t, group, "K1", row) // the deposit is taken, the room is not ready

	h.setClean(t, h.r101)
	for name, dep := range map[string]frontdesk.GroupDepositInput{
		"another amount": {Amount: "400000", PaymentMethod: "BANK_TRANSFER"},
		"another method": {Amount: "300000", PaymentMethod: "CARD"},
	} {
		row.Deposit = &dep
		r := rowOf(t, h.gci(t, group, "K-"+name, row), row.LineID)
		if r.Status != frontdesk.RowFailed || r.Code != "IDEMPOTENCY_KEY_REUSED" || r.Deposit == nil || r.Deposit.Status != frontdesk.RowFailed || r.Deposit.Code != "IDEMPOTENCY_KEY_REUSED" {
			t.Fatalf("%s: the answer is not the old one in silence: %+v", name, r)
		}
		if h.Count(t, `SELECT count(*) FROM stays`) != 0 || h.paymentsOf(t, rs[0].ID) != 1 {
			t.Fatalf("%s: nothing was done by the refused row", name)
		}
	}
	// with the amount that was recorded the room checks in
	row.Deposit = &frontdesk.GroupDepositInput{Amount: "300000", PaymentMethod: "BANK_TRANSFER"}
	if r := rowOf(t, h.gci(t, group, "K-ok", row), row.LineID); r.Status != frontdesk.RowCheckedIn || r.Deposit.Status != "REPLAYED" {
		t.Fatalf("the same deposit: %+v", r)
	}
}

// A deposit whose check-in never succeeds stays on the reservation as an advance deposit (2310) and can be refunded; the books and the reconciliation agree on every day.
func TestADepositWhoseCheckInNeverSucceededIsAnAdvanceDepositThatCanBeRefunded(t *testing.T) {
	h := setupHotel(t)
	must(t, h.Exec(t, `UPDATE room_housekeeping SET status = 'DIRTY' WHERE room_id = $1`, h.r101.ID))
	group, rs := h.groupArrivals(t, h.r101)
	row := h.gciRow(rs[0], h.r101)
	row.Deposit = &frontdesk.GroupDepositInput{Amount: "500000", PaymentMethod: "BANK_TRANSFER"}
	r := rowOf(t, h.gci(t, group, "K1", row), row.LineID)
	if r.Status != frontdesk.RowFailed || r.Deposit.Status != "RECORDED" {
		t.Fatalf("%+v", r)
	}
	reconciled := func(when string, asOf civil.Date) {
		t.Helper()
		rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
		must(t, err)
		if !rec.Reconciled {
			t.Fatalf("%s: the reconciliation must agree: %+v", when, rec)
		}
	}
	if _, err := h.Res.NoShow(h.admin, h.propID, rs[0].ID, rs[0].Rooms[0].ID, h.reload(t, rs[0].ID).Version, "did not arrive"); err != nil { // the guest never came in
		t.Fatal(err)
	}
	h.closeDay(t) // the deposit is held
	if got := h.balance(t, "2310"); !got.Equal(dec("-500000")) {
		t.Fatalf("advance deposits: %s", got)
	}
	reconciled("with the deposit", civil.MustParseDate("2026-10-01"))
	var payment int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT p.id FROM payments p JOIN folios f ON f.id = p.folio_id WHERE f.reservation_id = $1`, rs[0].ID).Scan(&payment))
	_, err := h.Folios.Refund(h.admin, h.propID, payment, "refund-1", folios.RefundInput{Amount: "500000", PaymentMethod: "CASH", Reason: "the guest did not come", Approval: h.approval()})
	must(t, err)
	h.closeDay(t)
	if got := h.balance(t, "2310"); !got.IsZero() {
		t.Fatalf("advance deposits after the refund: %s", got)
	}
	reconciled("after the refund", civil.MustParseDate("2026-10-02"))
	h.requireBalanced(t)
}

// A cash deposit of a room needs an open shift like any cash payment: the row fails with NO_OPEN_SHIFT, is not checked in, and takes no deposit.
func TestACashDepositOfARowNeedsAnOpenShift(t *testing.T) {
	h := setupHotel(t)
	h.Folios.SetShiftGate(h.Shifts)
	must(t, h.Exec(t, `INSERT INTO property_cashier_settings (tenant_id, property_id, require_shift_for_cash, block_night_audit) VALUES ($1, $2, true, false) ON CONFLICT (property_id) DO UPDATE SET require_shift_for_cash = true`, h.tenantID, h.propID))
	group, rs := h.groupArrivals(t, h.r101, h.r102)
	cash, other := h.gciRow(rs[0], h.r101), h.gciRow(rs[1], h.r102)
	cash.Deposit = &frontdesk.GroupDepositInput{Amount: "250000", PaymentMethod: "CASH"}
	out := h.gci(t, group, "K1", cash, other)
	if r := rowOf(t, out, cash.LineID); r.Status != frontdesk.RowFailed || r.Code != "NO_OPEN_SHIFT" || r.Deposit == nil || r.Deposit.Code != "NO_OPEN_SHIFT" {
		t.Fatalf("the cash row: %+v", r)
	}
	if r := rowOf(t, out, other.LineID); r.Status != frontdesk.RowCheckedIn {
		t.Fatalf("the other row does not wait for it: %+v", r)
	}
	if h.paymentsOf(t, rs[0].ID) != 0 || h.Count(t, `SELECT count(*) FROM stays`) != 1 {
		t.Fatal("no deposit and no check-in for the cash row")
	}
}

// A request with a deposit row by a person without payment.post is refused whole, before any room is checked in.
func TestADepositRowWithoutPaymentPostRefusesTheWholeRequest(t *testing.T) {
	h := setupHotel(t)
	group, rs := h.groupArrivals(t, h.r101, h.r102)
	clerk := h.User(t, h.tenantID, h.propID, auth.PermFrontdeskCheckin, auth.PermReservationRead, auth.PermGuestRead)
	withDeposit, plain := h.gciRow(rs[0], h.r101), h.gciRow(rs[1], h.r102)
	withDeposit.Deposit = &frontdesk.GroupDepositInput{Amount: "100000", PaymentMethod: "BANK_TRANSFER"}
	_, err := h.Front.GroupCheckIn(clerk, h.propID, group, "K1", frontdesk.GroupCheckInInput{Rows: []frontdesk.GroupCheckInRow{plain, withDeposit}})
	wantCode(t, err, "PERMISSION_DENIED")
	if h.Count(t, `SELECT count(*) FROM stays`) != 0 || h.Count(t, `SELECT count(*) FROM payments`) != 0 {
		t.Fatal("nothing was written, not even the room without a deposit")
	}
	// without a deposit the same person checks the rooms in
	out, err := h.Front.GroupCheckIn(clerk, h.propID, group, "K2", frontdesk.GroupCheckInInput{Rows: []frontdesk.GroupCheckInRow{plain}})
	must(t, err)
	if out.CheckedIn != 1 {
		t.Fatalf("%+v", out)
	}
	// the shape of a request: no rows, too many rows, a line named twice
	_, err = h.Front.GroupCheckIn(h.admin, h.propID, group, "K3", frontdesk.GroupCheckInInput{})
	wantCode(t, err, "GROUP_CHECKIN_EMPTY")
	many := make([]frontdesk.GroupCheckInRow, 101)
	for i := range many {
		many[i] = frontdesk.GroupCheckInRow{LineID: int64(i + 1)}
	}
	_, err = h.Front.GroupCheckIn(h.admin, h.propID, group, "K4", frontdesk.GroupCheckInInput{Rows: many})
	wantCode(t, err, "TOO_MANY_ROWS")
	_, err = h.Front.GroupCheckIn(h.admin, h.propID, group, "K5", frontdesk.GroupCheckInInput{Rows: []frontdesk.GroupCheckInRow{plain, plain}})
	wantCode(t, err, "VALIDATION_FAILED")
}

// Every row of one request names the same batch in its audit entry: the stays, the deposits and the entry of the group.
func TestEveryAuditEntryOfAGroupCheckInCarriesTheSameBatch(t *testing.T) {
	h := setupHotel(t)
	group, rs := h.groupArrivals(t, h.r101, h.r102)
	a, b := h.gciRow(rs[0], h.r101), h.gciRow(rs[1], h.r102)
	a.Deposit = &frontdesk.GroupDepositInput{Amount: "100000", PaymentMethod: "BANK_TRANSFER"}
	out := h.gci(t, group, "BATCH-1", a, b)
	if out.CheckedIn != 2 {
		t.Fatalf("%+v", out)
	}
	count := func(action string) int {
		return h.Count(t, `SELECT count(*) FROM audit_logs WHERE new_data->>'batch_id' = 'BATCH-1' AND action = $1`, action)
	}
	if count("stay.checked_in") != 2 || count("payment.deposit_posted") != 1 || count("group.checked_in") != 1 {
		t.Fatalf("the entries of the batch: stays %d, deposits %d, group %d", count("stay.checked_in"), count("payment.deposit_posted"), count("group.checked_in"))
	}
	if h.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('stay.checked_in', 'payment.deposit_posted') AND COALESCE(new_data->>'batch_id', '') <> 'BATCH-1'`) != 0 {
		t.Fatal("an entry of the request names another batch or none")
	}
}

// The arrivals of a group: the filter, the facts of the guest for the identity hints, and the estimate of every line from one batch, equal to the one the reservation screen shows.
func TestTheArrivalsOfAGroupCarryTheEstimateAndTheFactsOfTheGuest(t *testing.T) {
	h := setupHotel(t)
	group, rs := h.groupArrivals(t, h.r101, h.r102)
	h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	h.book(t, "2026-09-30", "2026-10-02") // a reservation that is not of the group
	list, err := h.Front.Arrivals(h.admin, h.propID, nil, frontdesk.ArrivalFilter{BookingGroupID: &group, WithEstimate: true})
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("only the rooms of the group: %d", len(list))
	}
	for i, a := range list {
		want := h.reload(t, rs[i].ID).Rooms[0].Estimate.Total.StringFixed(0)
		if a.Estimate == nil || *a.Estimate != want {
			t.Fatalf("the estimate of line %d: %v, the reservation says %s", a.ReservationRoomID, a.Estimate, want)
		}
		if a.GuestHasID || a.GuestNationality != "" || a.GuestIsPlaceholder {
			t.Fatalf("the facts of the guest: %+v", a)
		}
	}
	plain, err := h.Front.Arrivals(h.admin, h.propID, nil, frontdesk.ArrivalFilter{BookingGroupID: &group})
	must(t, err)
	if len(plain) != 2 || plain[0].Estimate != nil {
		t.Fatal("the estimate is asked for, never sent by default")
	}
	all, err := h.Front.Arrivals(h.admin, h.propID, nil, frontdesk.ArrivalFilter{})
	must(t, err)
	if len(all) != 3 {
		t.Fatalf("without the filter every arrival of the day: %d", len(all))
	}
}

// The check-out says where the room charges of the stay stand: on two folios when the routing of the group changed while the guest was in house, and, when it posted nothing, which master they go to.
func TestTheCheckOutSaysWhereTheRoomChargesOfTheStayStand(t *testing.T) {
	h := setupHotel(t)
	group, ms := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 2)
	h.closeDay(t) // the first night goes to the guest folios: the group has no routing yet
	h.route(t, group)
	h.closeDay(t) // the second night goes to the master
	master := h.masterOf(t, group)
	out := func(m member) frontdesk.CheckOutResult {
		st, err := h.Front.GetStay(h.admin, h.propID, m.st.Stay.ID)
		must(t, err)
		_, err = h.Folios.PostPayment(h.admin, h.propID, m.st.Folio.ID, "pay-"+m.st.Stay.StayNumber, folios.PaymentInput{Amount: h.folioBalance(t, m.st.Folio.ID).String(), PaymentMethod: "BANK_TRANSFER"})
		must(t, err)
		r, err := h.Front.CheckOut(h.admin, h.propID, m.st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
		must(t, err)
		return r
	}
	got := out(ms[0])
	if len(got.RoomCharges) != 2 {
		t.Fatalf("the nights stand on two folios: %+v", got.RoomCharges)
	}
	first, second := got.RoomCharges[0], got.RoomCharges[1]
	if first.FolioID != ms[0].st.Folio.ID || first.FolioType != "GUEST" || first.Nights != 1 || first.Total != "1000000" {
		t.Fatalf("the night before the routing is on the folio of the guest: %+v", first)
	}
	if second.FolioID != master || second.FolioType != "MASTER" || second.Nights != 1 || second.Total != "1000000" || second.FolioNumber == "" {
		t.Fatalf("the night after it is on the master: %+v", second)
	}
	if got.RoomChargesMaster == nil || got.RoomChargesMaster.FolioID != master {
		t.Fatalf("the room charges of this room go to the master now: %+v", got.RoomChargesMaster)
	}
	// a room that is taken out of the routing afterwards says no master for what is still to come, and keeps saying where its nights are
	h.route(t, group, ms[1].res.Rooms[0].ID)
	rest := out(ms[1])
	if rest.RoomChargesMaster != nil || len(rest.RoomCharges) != 2 {
		t.Fatalf("a room that stays out: %+v %+v", rest.RoomCharges, rest.RoomChargesMaster)
	}
}

// The arrivals of a group say what became of the deposit of each room, and a deposit that was voided or refunded in full is never answered as taken: the row is refused with a clear code and is not checked
// in on money that is not there. The keys of the group check-in are scoped to the property (and so to the tenant).
func TestADepositThatWasVoidedOrRefundedIsNotAnsweredAsTaken(t *testing.T) {
	h := setupHotel(t)
	r103 := h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean)
	must(t, h.Exec(t, `UPDATE room_housekeeping SET status = 'DIRTY' WHERE room_id IN ($1, $2, $3)`, h.r101.ID, h.r102.ID, r103.ID)) // every room is not ready: the deposits are taken and the check-ins fail
	group, rs := h.groupArrivals(t, h.r101, h.r102, r103)
	rows := []frontdesk.GroupCheckInRow{h.gciRow(rs[0], h.r101), h.gciRow(rs[1], h.r102), h.gciRow(rs[2], r103)}
	for i := range rows {
		rows[i].Deposit = &frontdesk.GroupDepositInput{Amount: "500000", PaymentMethod: "BANK_TRANSFER"}
	}
	first := h.gci(t, group, "K1", rows...)
	if first.DepositsRecorded != 3 || first.Failed != 3 {
		t.Fatalf("three deposits taken, three rooms not ready: %+v", first)
	}
	depositOf := func(i int) int64 {
		var id int64
		must(t, h.Pool.QueryRow(context.Background(), `SELECT p.id FROM payments p JOIN folios f ON f.id = p.folio_id WHERE f.reservation_id = $1`, rs[i].ID).Scan(&id))
		return id
	}
	arrivals := func() map[int64]*folios.GroupDepositState {
		list, err := h.Front.Arrivals(h.admin, h.propID, nil, frontdesk.ArrivalFilter{BookingGroupID: &group})
		must(t, err)
		out := map[int64]*folios.GroupDepositState{}
		for _, a := range list {
			out[a.ReservationRoomID] = a.GroupDeposit
		}
		return out
	}
	// the arrivals say it is received, with its number and amount
	for _, r := range rows {
		if d := arrivals()[r.LineID]; d == nil || d.Status != "RECEIVED" || d.Amount != "500000" || d.PaymentNumber == "" {
			t.Fatalf("a deposit that was taken: %+v", d)
		}
	}
	// room 1: the deposit is voided; room 2: refunded in full; room 3: refunded in part
	_, err := h.Folios.Void(h.admin, h.propID, depositOf(0), folios.CorrectionInput{Reason: "taken by mistake", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.Refund(h.admin, h.propID, depositOf(1), "rf-2", folios.RefundInput{Amount: "500000", PaymentMethod: "CASH", Reason: "the guest changed plans", Approval: h.approval()})
	must(t, err)
	_, err = h.Folios.Refund(h.admin, h.propID, depositOf(2), "rf-3", folios.RefundInput{Amount: "100000", PaymentMethod: "CASH", Reason: "a part back", Approval: h.approval()})
	must(t, err)
	got := arrivals()
	if got[rows[0].LineID].Status != "VOIDED" || got[rows[1].LineID].Status != "REFUNDED" || got[rows[2].LineID].Status != "RECEIVED" || got[rows[2].LineID].Refunded != "100000" {
		t.Fatalf("what became of the deposits: %+v %+v %+v", got[rows[0].LineID], got[rows[1].LineID], got[rows[2].LineID])
	}

	// every room is ready now and the same rows are sent again in a new request, deposit and all
	for _, r := range []rooms.Room{h.r101, h.r102, r103} {
		h.setClean(t, r)
	}
	second := h.gci(t, group, "K2", rows...)
	voided, refunded, partly := rowOf(t, second, rows[0].LineID), rowOf(t, second, rows[1].LineID), rowOf(t, second, rows[2].LineID)
	if voided.Status != frontdesk.RowFailed || voided.Code != "DEPOSIT_VOIDED" || voided.Deposit == nil || voided.Deposit.Status != frontdesk.RowFailed {
		t.Fatalf("a voided deposit is not answered as taken: %+v", voided)
	}
	if refunded.Status != frontdesk.RowFailed || refunded.Code != "DEPOSIT_REFUNDED" {
		t.Fatalf("a deposit refunded in full is not answered as taken: %+v", refunded)
	}
	if partly.Status != frontdesk.RowCheckedIn || partly.Deposit == nil || partly.Deposit.Status != "REPLAYED" {
		t.Fatalf("a deposit refunded in part is still the deposit: %+v", partly)
	}
	if h.Count(t, `SELECT count(*) FROM stays`) != 1 {
		t.Fatal("only the room whose deposit stands was checked in")
	}
	if h.paymentsOf(t, rs[0].ID) != 1 || h.paymentsOf(t, rs[1].ID) != 2 {
		t.Fatal("no new deposit was taken by the refused rows (the second count is the deposit and its refund)")
	}
	// without a deposit in the row the room is checked in (the deposit, if any, is recorded on the folio)
	voided2 := rows[0]
	voided2.Deposit = nil
	if r := rowOf(t, h.gci(t, group, "K3", voided2), voided2.LineID); r.Status != frontdesk.RowCheckedIn {
		t.Fatalf("the room checks in without a deposit: %+v", r)
	}

	// the keys are scoped to the property: the unique indexes are (property, key), and every read of them asks for the tenant and the property
	for _, idx := range []string{"payments_idempotency_uk", "stays_idempotency_uk"} {
		var def string
		must(t, h.Pool.QueryRow(context.Background(), `SELECT indexdef FROM pg_indexes WHERE indexname = $1`, idx).Scan(&def))
		if !strings.Contains(def, "(property_id, idempotency_key)") {
			t.Fatalf("%s: %s", idx, def)
		}
	}
}
