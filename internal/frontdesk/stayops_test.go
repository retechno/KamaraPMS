package frontdesk_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

// stay checks a stay of one type-level booking into a room on the business date.
func (f *fx) stay(t *testing.T, room int64, departure string) frontdesk.CheckInResult {
	t.Helper()
	res := f.book(t, f.dlx, "2026-09-30", departure)
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	return out
}

func (f *fx) clean(t *testing.T, room int64) {
	t.Helper()
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, room))
}

func (f *fx) detail(t *testing.T, id int64) frontdesk.StayDetail {
	t.Helper()
	d, err := f.Front.GetStay(f.admin, f.propID, id)
	must(t, err)
	return d
}

func (f *fx) nextDay(t *testing.T) {
	t.Helper()
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, day.BusinessDate, nil, json.RawMessage(`{}`))
		return err
	}))
}

func (f *fx) hkStatus(t *testing.T, room int64) string {
	t.Helper()
	var st string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT status FROM room_housekeeping WHERE room_id = $1`, room).Scan(&st))
	return st
}

func TestMoveRoom(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	move := func(room int64) frontdesk.MoveInput {
		return frontdesk.MoveInput{Version: st.Stay.Version, RoomID: room, Reason: "noisy room"}
	}
	// validation and guards
	_, err := f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Front.Move(f.admin, f.propID, st.Stay.ID, move(f.r101.ID))
	wantCode(t, err, "SAME_ROOM")
	_, err = f.Front.Move(f.admin, f.propID, st.Stay.ID, move(99999))
	wantCode(t, err, "ROOM_NOT_FOUND")
	in := move(f.r102.ID)
	in.Version = 9
	_, err = f.Front.Move(f.admin, f.propID, st.Stay.ID, in)
	wantCode(t, err, "VERSION_CONFLICT")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin)
	_, err = f.Front.Move(clerk, f.propID, st.Stay.ID, move(f.r102.ID))
	wantCode(t, err, "PERMISSION_DENIED")
	// the target must be ready
	_, err = f.Front.Move(f.admin, f.propID, st.Stay.ID, move(f.r102.ID))
	e := code(t, err, "ROOM_NOT_READY")
	if e.Context["current"] != "DIRTY" {
		t.Fatalf("context: %v", e.Context)
	}
	if f.hkStatus(t, f.r101.ID) != "CLEAN" {
		t.Fatal("a refused move leaves the old room alone")
	}
	f.clean(t, f.r102.ID)
	res, err := f.Front.Move(f.admin, f.propID, st.Stay.ID, move(f.r102.ID))
	must(t, err)
	if res.Stay.Version != st.Stay.Version+1 || res.ClosedSegment.RoomNumber != "101" || res.ClosedSegment.CheckOutAt == nil || res.ClosedSegment.EndBusinessDate == nil ||
		res.NewSegment.RoomNumber != "102" || res.NewSegment.CheckOutAt != nil || res.NewSegment.StartBusinessDate != roomstest.BD {
		t.Fatalf("move: %+v", res)
	}
	if f.hkStatus(t, f.r101.ID) != "DIRTY" || f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE source = 'ROOM_MOVE'`) != 1 {
		t.Fatal("the old room goes DIRTY, logged")
	}
	d := f.detail(t, st.Stay.ID)
	if len(d.Segments) != 2 || d.Segments[1].RoomNumber != "102" {
		t.Fatalf("segments: %+v", d.Segments)
	}
	board, err := f.HK.Board(f.admin, f.propID, housekeeping.BoardFilter{})
	must(t, err)
	for _, r := range board {
		if (r.RoomNumber == "102" && r.Occupancy != housekeeping.Occupied) || (r.RoomNumber == "101" && r.Occupancy == housekeeping.Occupied) {
			t.Fatalf("occupancy after the move: %s %s", r.RoomNumber, r.Occupancy)
		}
	}
	// tonight's room charge follows the segment that covers the business date: the new room
	rc, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if len(rc.Results) != 1 || rc.Results[0].Status != "POSTED" {
		t.Fatalf("post: %+v", rc)
	}
	var desc string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT description FROM folio_items WHERE transaction_type = 'CHARGE'`).Scan(&desc))
	if desc != "Room 102 - 30 Sep 2026" {
		t.Fatalf("the new room is charged: %q", desc)
	}
	// a moved stay cannot have its check-in reversed
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, st.Stay.ID, frontdesk.ReverseInput{Version: res.Stay.Version, Reason: "x"})
	wantCode(t, err, "CHECK_IN_NOT_REVERSIBLE")
}

func TestMoveTargets(t *testing.T) {
	f := setup(t)
	a := f.stay(t, f.r101.ID, "2026-10-03")
	// occupied by another stay
	f.clean(t, f.r102.ID)
	b := f.stay(t, f.r102.ID, "2026-10-02")
	_ = b
	_, err := f.Front.Move(f.admin, f.propID, a.Stay.ID, frontdesk.MoveInput{Version: a.Stay.Version, RoomID: f.r102.ID, Reason: "x"})
	wantCode(t, err, "ROOM_OCCUPIED")
	// another room type needs its inventory: the only STD room is booked for the nights
	f.book(t, f.std, "2026-09-30", "2026-10-03")
	_, err = f.Front.Move(f.admin, f.propID, a.Stay.ID, frontdesk.MoveInput{Version: a.Stay.Version, RoomID: f.r201.ID, Reason: "upgrade"})
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	// a room that is blocked for the rest of the stay
	if _, err := f.Rooms.CreateBlock(f.admin, f.propID, roomsBlock(f.r201.ID, "2026-10-04", "2026-10-05")); err != nil {
		t.Fatal(err)
	}
	// a stay that is not in house
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, b.Stay.ID, frontdesk.ReverseInput{Version: b.Stay.Version, Reason: "x"})
	must(t, err)
	_, err = f.Front.Move(f.admin, f.propID, b.Stay.ID, frontdesk.MoveInput{Version: b.Stay.Version + 1, RoomID: f.r101.ID, Reason: "x"})
	wantCode(t, err, "STAY_NOT_OPEN")
}

func TestMoveWithNewRatesOnlyForUnpostedNights(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	f.clean(t, f.r102.ID)
	rates := []reservations.NightOverride{{Date: d("2026-10-01"), Amount: "800000"}}
	agent := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskRoomMove, auth.PermReservationRead)
	_, err := f.Front.Move(agent, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID, Reason: "x", NewNightlyRates: rates})
	wantCode(t, err, "PERMISSION_DENIED") // frontdesk.rate_change
	// tonight is charged: its rate can no longer change
	_, err = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	_, err = f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID, Reason: "x", NewNightlyRates: []reservations.NightOverride{{Date: d("2026-09-30"), Amount: "1"}}})
	wantCode(t, err, "NIGHT_ALREADY_POSTED")
	_, err = f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID, Reason: "x", NewNightlyRates: []reservations.NightOverride{{Date: d("2026-12-01"), Amount: "1"}}})
	wantCode(t, err, "VALIDATION_FAILED")
	if _, err := f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID, Reason: "x", NewNightlyRates: rates}); err != nil {
		t.Fatalf("move with a new rate: %v", err)
	}
	d := f.detail(t, st.Stay.ID)
	var night string
	var override bool
	for _, n := range d.NightlyRates {
		if n.Date == civil.MustParseDate("2026-10-01") {
			night, override = n.Amount, n.IsOverride
		}
	}
	if night != "800000" || !override || d.NightlyRates[0].Amount != "1000000" {
		t.Fatalf("rates: %+v", d.NightlyRates)
	}
}

func TestConcurrentMovesIntoOneRoom(t *testing.T) {
	f := setup(t)
	a := f.stay(t, f.r101.ID, "2026-10-03")
	must(t, f.Exec(t, `INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number, bed_type_id) VALUES ($1, $2, $3, '103', (SELECT id FROM bed_types WHERE property_id = $2 ORDER BY sort_order, id LIMIT 1))`, f.tenantID, f.propID, f.dlx.ID))
	f.clean(t, f.r102.ID)
	bRoom := f.Room(t, f.admin, f.propID, f.dlx.ID, "104", housekeeping.Clean)
	b := f.stay(t, bRoom.ID, "2026-10-03")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, st := range []frontdesk.CheckInResult{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID, Reason: "race"})
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one move wins: %v / %v", errs[0], errs[1])
	}
	if f.Count(t, `SELECT count(*) FROM stay_rooms WHERE room_id = $1 AND check_out_at IS NULL`, f.r102.ID) != 1 {
		t.Fatal("one open segment per room")
	}
}

func TestExtendAndShorten(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-02")
	in := frontdesk.ChangeDepartureInput{Version: st.Stay.Version, DepartureDate: d("2026-10-02")}
	_, err := f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, in)
	wantCode(t, err, "VALIDATION_FAILED") // unchanged
	in.DepartureDate = d("2026-10-05")
	in.Version = 9
	_, err = f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, in)
	wantCode(t, err, "VERSION_CONFLICT")
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	in.Version = st.Stay.Version
	_, err = f.Front.ChangeDeparture(reader, f.propID, st.Stay.ID, in)
	wantCode(t, err, "PERMISSION_DENIED")

	// another booking holds the room for the extra nights: the answer suggests a move
	other := f.book(t, f.dlx, "2026-10-03", "2026-10-05")
	_, err = f.Res.AssignRoom(f.admin, f.propID, other.ID, other.Rooms[0].ID, other.Version, f.r101.ID, false)
	must(t, err)
	e := roomstestCode(t, mustStay(f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, in)), "ROOM_NOT_AVAILABLE_FOR_EXTENSION")
	if e.Context["suggest_room_move"] != true || e.Context["alternative_rooms"] == nil {
		t.Fatalf("context: %v", e.Context)
	}
	// extend by one night (free): priced from the grid, the line keeps its dates
	in.DepartureDate = d("2026-10-05")
	_, err = f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, in)
	wantCode(t, err, "ROOM_NOT_AVAILABLE_FOR_EXTENSION")
	in.DepartureDate = d("2026-10-03")
	ext, err := f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, in)
	must(t, err)
	if ext.DepartureDate != d("2026-10-03") || ext.Version != st.Stay.Version+1 {
		t.Fatalf("extended: %+v", ext)
	}
	dt := f.detail(t, st.Stay.ID)
	if len(dt.NightlyRates) != 3 || dt.NightlyRates[2].Amount != "1000000" || dt.NightlyRates[2].Date != d("2026-10-02") {
		t.Fatalf("nights: %+v", dt.NightlyRates)
	}
	res := f.reload(t, st.Stay.ReservationID)
	if res.Rooms[0].DepartureDate != d("2026-10-02") {
		t.Fatalf("the reservation room keeps its own dates: %s", res.Rooms[0].DepartureDate)
	}
	// the extra night counts against the room type
	inv, err := f.Avail.Inventory(f.admin, f.tenantID, f.propID, []int64{f.dlx.ID}, []civil.Date{d("2026-10-02")}, roomstest.BD, nil)
	must(t, err)
	if inv[f.dlx.ID][d("2026-10-02")].Demand != 1 {
		t.Fatalf("demand: %+v", inv[f.dlx.ID][d("2026-10-02")])
	}
	// no inventory left for more: both DLX rooms are wanted on 3 Oct
	f.book(t, f.dlx, "2026-10-03", "2026-10-05")
	in2 := frontdesk.ChangeDepartureInput{Version: ext.Version, DepartureDate: d("2026-10-05")}
	_, err = f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, in2)
	if !appCode(err, "ROOM_NOT_AVAILABLE_FOR_EXTENSION") && !appCode(err, "ROOM_TYPE_NOT_AVAILABLE") {
		t.Fatalf("extension beyond the inventory: %v", err)
	}
	// shorten: the nights beyond are dropped, today and earlier are refused
	short, err := f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: ext.Version, DepartureDate: d("2026-10-01")})
	must(t, err)
	dt = f.detail(t, st.Stay.ID)
	if short.DepartureDate != d("2026-10-01") || len(dt.NightlyRates) != 1 {
		t.Fatalf("shortened: %+v %+v", short, dt.NightlyRates)
	}
	_, err = f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: short.Version, DepartureDate: roomstest.BD})
	wantCode(t, err, "VALIDATION_FAILED")
}

func appCode(err error, code string) bool {
	e, ok := asAppErr(err)
	return ok && e == code
}

func TestAddGuestToAStay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-02")
	var comp int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G2', 'Companion', $2) RETURNING id`, f.tenantID, f.propID).Scan(&comp))
	d, err := f.Front.AddGuest(f.admin, f.propID, st.Stay.ID, frontdesk.AddGuestInput{GuestID: comp})
	must(t, err)
	if len(d.Guests) != 1 || d.Guests[0].Code != "G2" {
		t.Fatalf("guests: %+v", d.Guests)
	}
	_, err = f.Front.AddGuest(f.admin, f.propID, st.Stay.ID, frontdesk.AddGuestInput{GuestID: comp})
	wantCode(t, err, "GUEST_ALREADY_ON_STAY")
	_, err = f.Front.AddGuest(f.admin, f.propID, st.Stay.ID, frontdesk.AddGuestInput{GuestID: f.guest})
	wantCode(t, err, "GUEST_ALREADY_ON_STAY")
	_, err = f.Front.AddGuest(f.admin, f.propID, st.Stay.ID, frontdesk.AddGuestInput{GuestID: 99999})
	wantCode(t, err, "GUEST_NOT_FOUND")
	_, err = f.Front.AddGuest(f.admin, f.propID, st.Stay.ID, frontdesk.AddGuestInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Front.AddGuest(reader, f.propID, st.Stay.ID, frontdesk.AddGuestInput{GuestID: comp})
	wantCode(t, err, "PERMISSION_DENIED")
}

func (f *fx) pay(t *testing.T, folioID int64, amount string) {
	t.Helper()
	_, err := f.Folios.PostPayment(f.admin, f.propID, folioID, "", folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
	must(t, err)
}

func TestCheckOutSettlesTheStay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-01") // one night
	out := func() (frontdesk.CheckOutResult, error) {
		return f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
	}
	// the night is charged at check-out, the folio is not balanced: nothing changes
	_, err := out()
	e := code(t, err, "FOLIO_NOT_BALANCED")
	if e.Context["folios"] == nil {
		t.Fatalf("context: %v", e.Context)
	}
	if f.Count(t, `SELECT count(*) FROM folio_items`) != 0 || f.detail(t, st.Stay.ID).Stay.Status != "OPEN" || f.hkStatus(t, f.r101.ID) != "CLEAN" {
		t.Fatal("a refused check-out changes nothing, not even the room charge it posted")
	}
	f.pay(t, st.Folio.ID, "1000000")
	res, err := out()
	must(t, err)
	if res.Stay.Status != "CHECKED_OUT" || res.Stay.CheckedOutAt == nil || len(res.PostedRoomCharges) != 1 || res.PostedRoomCharges[0].Status != "POSTED" ||
		len(res.Folios) != 1 || res.Folios[0].Status != "CLOSED" || res.Housekeeping != "DIRTY" {
		t.Fatalf("check-out: %+v", res)
	}
	var trigger string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT posting_trigger FROM stay_charge_postings`).Scan(&trigger))
	if trigger != "CHECK_OUT" || f.hkStatus(t, f.r101.ID) != "DIRTY" {
		t.Fatalf("trigger %s", trigger)
	}
	after := f.reload(t, st.Stay.ReservationID)
	if after.Rooms[0].Status != "COMPLETED" || after.DisplayStatus != "CHECKED_OUT" {
		t.Fatalf("line after: %+v", after.Rooms[0])
	}
	if f.Count(t, `SELECT count(*) FROM stay_rooms WHERE check_out_at IS NULL`) != 0 {
		t.Fatal("the segment is closed")
	}
	board, err := f.HK.Board(f.admin, f.propID, housekeeping.BoardFilter{})
	must(t, err)
	for _, r := range board {
		if r.RoomNumber == "101" && (r.Occupancy != housekeeping.Vacant || r.Status != housekeeping.Dirty) {
			t.Fatalf("room after check-out: %s %s", r.Occupancy, r.Status)
		}
	}
	// a retry of the same request answers the same way; a stale one is refused
	again, err := out()
	if err != nil || again.Stay.ID != st.Stay.ID || again.Stay.Status != "CHECKED_OUT" {
		t.Fatalf("retry: %v %+v", err, again)
	}
	_, err = f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: 99})
	wantCode(t, err, "STAY_NOT_OPEN")
	// the room is free for the next guest; the room charge of a closed stay is not applicable
	if pv, err := f.Charges.Preview(f.admin, f.propID, roomstest.BD, nil); err != nil || len(pv.Items) != 0 {
		t.Fatalf("preview: %v %+v", err, pv)
	}
}

func TestEarlyDepartureNeedsConfirmation(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	in := frontdesk.CheckOutInput{Version: st.Stay.Version}
	_, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, in)
	e := code(t, err, "EARLY_DEPARTURE_NOT_CONFIRMED")
	if e.Context["new_departure_date"] == nil {
		t.Fatalf("context: %v", e.Context)
	}
	f.pay(t, st.Folio.ID, "1000000")
	in.ConfirmEarlyDeparture = true
	in.Version = f.detail(t, st.Stay.ID).Stay.Version
	res, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, in)
	must(t, err)
	// the local date is the business date: the guest leaves today, the night of today is the only one
	if res.Stay.DepartureDate != d("2026-10-01") || len(res.PostedRoomCharges) != 1 {
		t.Fatalf("early departure: %+v", res)
	}
	if n := len(f.detail(t, st.Stay.ID).NightlyRates); n != 1 {
		t.Fatalf("nights after the early departure: %d", n)
	}
}

func TestCheckOutAfterMidnightChargesTheBusinessDate(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	f.Clock.Set(roomstest.T0.Add(6 * time.Hour)) // 02:00 the next calendar day at the property; the audit has not run
	f.pay(t, st.Folio.ID, "1000000")
	res, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version, ConfirmEarlyDeparture: true})
	must(t, err)
	if len(res.PostedRoomCharges) != 1 || res.PostedRoomCharges[0].ServiceDate != roomstest.BD {
		t.Fatalf("the night of the business date is charged: %+v", res.PostedRoomCharges)
	}
	// after midnight the stay has consumed the night of the business date: departure is tomorrow, not today
	if res.Stay.DepartureDate != d("2026-10-01") {
		t.Fatalf("departure %s", res.Stay.DepartureDate)
	}
}

func TestCheckOutCatchesUpMissingNights(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-02")
	f.nextDay(t)
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour))
	f.pay(t, st.Folio.ID, "1000000") // the folio will hold one night: 30 Sep was never posted, 1 Oct is the day of departure
	res, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version, ConfirmEarlyDeparture: true})
	// on 1 Oct the guest leaves on the booked day? departure is 2 Oct: early, today; only the night of 30 Sep is owed
	if err != nil {
		t.Fatalf("check-out: %v", err)
	}
	if len(res.PostedRoomCharges) != 1 || res.PostedRoomCharges[0].ServiceDate != d("2026-09-30") {
		t.Fatalf("the missing night is posted: %+v", res.PostedRoomCharges)
	}
}

func TestCheckOutRefusedWhileChargesCannotBePosted(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-01")
	must(t, f.Exec(t, `DELETE FROM reservation_room_rates WHERE reservation_room_id = $1`, st.Stay.ReservationRoomID))
	_, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
	e := code(t, err, "REQUIRED_CHARGES_NOT_POSTED")
	if e.Context["items"] == nil {
		t.Fatalf("context: %v", e.Context)
	}
	if f.detail(t, st.Stay.ID).Stay.Status != "OPEN" {
		t.Fatal("still in house")
	}
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin)
	_, err = f.Front.CheckOut(clerk, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Front.CheckOut(f.admin, f.propID, 99999, frontdesk.CheckOutInput{Version: 1})
	wantCode(t, err, "STAY_NOT_FOUND")
}

func TestCheckOutAfterTonightWasChargedInAdvance(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	_, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	f.pay(t, st.Folio.ID, "1000000")
	res, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version, ConfirmEarlyDeparture: true})
	must(t, err)
	if len(res.PostedRoomCharges) != 0 || res.Stay.DepartureDate != d("2026-10-01") {
		t.Fatalf("tonight is not charged twice: %+v", res)
	}
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`) != 1 {
		t.Fatal("one night charged")
	}
}

func TestConcurrentCheckOutsPostOnce(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-01")
	f.pay(t, st.Folio.ID, "1000000")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil && !appCode(err, "STAY_NOT_OPEN") && !appCode(err, "VERSION_CONFLICT") {
			t.Fatalf("unexpected: %v", err)
		}
	}
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`) != 1 || f.detail(t, st.Stay.ID).Stay.Status != "CHECKED_OUT" {
		t.Fatal("one night charged, stay checked out")
	}
}

func roomsBlock(room int64, from, to string) rooms.CreateBlockInput {
	return rooms.CreateBlockInput{RoomID: room, BlockType: "OOO", StartDate: d(from), EndDate: d(to), Reason: "x"}
}

func mustStay(_ frontdesk.Stay, err error) error { return err }

func roomstestCode(t *testing.T, err error, c string) *apperr.Error { return code(t, err, c) }

func asAppErr(err error) (string, bool) { return asApp(err) }

func TestTapeChartFollowsMovesAndDepartureChanges(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-04")
	f.clean(t, f.r102.ID)
	f.nextDay(t)
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour))
	cur := f.detail(t, st.Stay.ID)
	_, err := f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: cur.Stay.Version, RoomID: f.r102.ID, Reason: "x"})
	must(t, err)
	cur = f.detail(t, st.Stay.ID)
	_, err = f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: cur.Stay.Version, DepartureDate: d("2026-10-03")})
	must(t, err)
	tape, err := f.Res.TapeChart(f.admin, f.propID, d("2026-09-30"), d("2026-10-08"))
	must(t, err)
	bars := map[string][]reservations.TapeBooking{}
	for _, r := range tape.Rooms {
		bars[r.RoomNumber] = r.Bookings
	}
	if len(bars["101"]) != 1 || bars["101"][0].ArrivalDate != d("2026-09-30") || bars["101"][0].DepartureDate != d("2026-10-01") ||
		len(bars["102"]) != 1 || bars["102"][0].ArrivalDate != d("2026-10-01") || bars["102"][0].DepartureDate != d("2026-10-03") || bars["102"][0].Status != "CHECKED_IN" {
		t.Fatalf("bars: %+v", bars)
	}
}

func TestRoomChargesCarryTheRevenueAccountCode(t *testing.T) {
	f := setup(t)
	must(t, f.Exec(t, `UPDATE charge_codes SET gl_account_code = '4-1100' WHERE property_id = $1 AND code = 'ROOM'`, f.propID))
	st := f.stay(t, f.r101.ID, "2026-10-01")
	f.pay(t, st.Folio.ID, "1000000")
	_, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version})
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE' AND revenue_account_code = '4-1100'`) != 1 {
		t.Fatal("the room night carries the account of the ROOM code")
	}
	must(t, f.Exec(t, `UPDATE charge_codes SET gl_account_code = '4-2000' WHERE property_id = $1 AND code = 'ROOM'`, f.propID))
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE revenue_account_code = '4-1100'`) != 1 {
		t.Fatal("a remapping does not change the posted night")
	}
}
