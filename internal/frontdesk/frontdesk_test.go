package frontdesk_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/guests"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	dlx, std         rooms.RoomType
	r101, r102, r201 rooms.Room // 101 CLEAN, 102 DIRTY (DLX), 201 CLEAN (STD)
	plan, guest      int64
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, _ := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin}
	f.dlx, f.std = e.RoomType(t, admin, p.ID, "DLX"), e.RoomType(t, admin, p.ID, "STD")
	f.r101 = e.Room(t, admin, p.ID, f.dlx.ID, "101", housekeeping.Clean)
	f.r102 = e.Room(t, admin, p.ID, f.dlx.ID, "102")
	f.r201 = e.Room(t, admin, p.ID, f.std.ID, "201", housekeeping.Clean)
	var chargeID int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&f.plan))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, tn.ID, p.ID).Scan(&f.guest))
	for typ, amount := range map[int64]string{f.dlx.ID: "1000000", f.std.ID: "500000"} {
		_, err := e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{typ}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: amount})
		must(t, err)
	}
	return f
}

// book confirms a type-level reservation of one room.
func (f *fx) book(t *testing.T, typ rooms.RoomType, arrival, departure string) reservations.Reservation {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: typ.ID, RatePlanID: f.plan, Arrival: d(arrival), Departure: d(departure), Adults: 2},
	}})
	must(t, err)
	return res
}

func (f *fx) in(res reservations.Reservation, room *rooms.Room) frontdesk.CheckInInput {
	in := frontdesk.CheckInInput{Version: res.Version, GuestID: f.guest, AdultCount: 2}
	if room != nil {
		in.RoomID = &room.ID
	}
	return in
}

func (f *fx) checkIn(t *testing.T, ctx context.Context, res reservations.Reservation, room *rooms.Room, key string) (frontdesk.CheckInResult, error) {
	t.Helper()
	return f.Front.CheckIn(ctx, f.propID, res.ID, res.Rooms[0].ID, key, f.in(res, room))
}

func (f *fx) reload(t *testing.T, id int64) reservations.Reservation {
	t.Helper()
	r, err := f.Res.Get(f.admin, f.propID, id)
	must(t, err)
	return r
}

func TestCheckInCreatesTheStay(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	out, err := f.checkIn(t, f.admin, res, &f.r101, "ci-1")
	must(t, err)
	if out.Stay.Status != "OPEN" || out.Stay.StayNumber == "" || out.Stay.ArrivalDate != roomstest.BD || out.Stay.DepartureDate != d("2026-10-02") ||
		out.Stay.ReservationID != res.ID || out.Stay.GuestID != f.guest || out.Stay.Version != 1 || out.Stay.AdultCount != 2 {
		t.Fatalf("stay: %+v", out.Stay)
	}
	if out.StayRoom.RoomNumber != "101" || out.StayRoom.CheckOutAt != nil || out.StayRoom.StartBusinessDate != roomstest.BD {
		t.Fatalf("segment: %+v", out.StayRoom)
	}
	if out.Folio.ID == 0 || out.Folio.Status != "OPEN" || out.Folio.Balance != "0" || out.Folio.FolioNumber == "" {
		t.Fatalf("folio: %+v", out.Folio)
	}
	after := f.reload(t, res.ID)
	line := after.Rooms[0]
	if line.Status != "CHECKED_IN" || line.RoomNumber != "101" || line.StayID == nil || *line.StayID != out.Stay.ID || after.DisplayStatus != "IN_HOUSE" || after.Version != res.Version+1 {
		t.Fatalf("reservation after: %+v", after)
	}
	// the derived occupancy of the room is OCCUPIED, and the room still counts against the type's inventory
	board, err := f.HK.Board(f.admin, f.propID, housekeeping.BoardFilter{})
	must(t, err)
	for _, r := range board {
		if r.RoomNumber == "101" && r.Occupancy != housekeeping.Occupied {
			t.Fatalf("occupancy of 101: %s", r.Occupancy)
		}
		if r.RoomNumber == "102" && r.Occupancy == housekeeping.Occupied {
			t.Fatalf("102 must not be occupied")
		}
	}
	inv, err := f.Avail.Inventory(f.admin, f.tenantID, f.propID, []int64{f.dlx.ID}, []civil.Date{d("2026-09-30"), d("2026-10-01"), d("2026-10-02")}, roomstest.BD, nil)
	must(t, err)
	if inv[f.dlx.ID][d("2026-09-30")].Demand != 1 || inv[f.dlx.ID][d("2026-10-01")].Demand != 1 || inv[f.dlx.ID][d("2026-10-02")].Demand != 0 {
		t.Fatalf("inventory: %+v", inv[f.dlx.ID])
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.checked_in'`) != 1 {
		t.Fatal("check-in is audited")
	}
	// double click: the same key returns the same stay, a new key finds the line already checked in
	again, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "ci-1", f.in(res, &f.r101))
	if err != nil || again.Stay.ID != out.Stay.ID || again.Folio.ID != out.Folio.ID || f.Count(t, `SELECT count(*) FROM stays`) != 1 {
		t.Fatalf("replay: %v %+v", err, again)
	}
	_, err = f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "ci-2", frontdesk.CheckInInput{Version: after.Version, GuestID: f.guest, AdultCount: 2})
	wantCode(t, err, "LINE_NOT_CONFIRMED")
	_, err = f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "ci-1", frontdesk.CheckInInput{Version: 1, GuestID: f.guest, AdultCount: 2})
	must(t, err) // a replay does not look at the (stale) version either
	other := f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	_, err = f.Front.CheckIn(f.admin, f.propID, other.ID, other.Rooms[0].ID, "ci-1", f.in(other, &f.r102))
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
}

func TestCheckInLinksTheDepositFolio(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	dep, err := f.Folios.Deposit(f.admin, f.propID, res.ID, "d1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	out, err := f.checkIn(t, f.admin, res, &f.r101, "ci-1")
	must(t, err)
	if out.Folio.ID != dep.Payment.FolioID || out.Folio.Balance != "-300000" {
		t.Fatalf("the deposit folio is linked: %+v (deposit folio %d)", out.Folio, dep.Payment.FolioID)
	}
	var stay *int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT stay_id FROM folios WHERE id = $1`, out.Folio.ID).Scan(&stay))
	if stay == nil || *stay != out.Stay.ID || f.Count(t, `SELECT count(*) FROM folios`) != 1 {
		t.Fatalf("folio stay link: %v", stay)
	}
}

func TestCheckInValidation(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	try := func(mut func(*frontdesk.CheckInInput)) error {
		in := f.in(res, &f.r101)
		mut(&in)
		_, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", in)
		return err
	}
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.GuestID = 0 }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.AdultCount = 0 }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.AdultCount, in.ChildCount = 3, 1 }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.Version = 0 }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.Version = 99 }), "VERSION_CONFLICT")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.RoomID = nil }), "VALIDATION_FAILED") // no room assigned yet
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.RoomID = ptr(int64(99999)) }), "ROOM_NOT_FOUND")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.GuestID = 99999 }), "GUEST_NOT_FOUND")
	wantCode(t, try(func(in *frontdesk.CheckInInput) { in.AccompanyingGuestIDs = []int64{99999} }), "GUEST_NOT_FOUND")
	if f.Count(t, `SELECT count(*) FROM stays`) != 0 {
		t.Fatal("nothing is written by a refused check-in")
	}
	// the arrival date must be the business date
	later := f.book(t, f.dlx, "2026-10-01", "2026-10-02")
	_, err := f.Front.CheckIn(f.admin, f.propID, later.ID, later.Rooms[0].ID, "", f.in(later, &f.r101))
	e := code(t, err, "ARRIVAL_DATE_MISMATCH")
	if e.Context["arrival_date"] == nil || e.Context["business_date"] == nil {
		t.Fatalf("context: %v", e.Context)
	}
	// draft reservations and unknown lines
	dr, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Rooms: []reservations.LineInput{{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: roomstest.BD, Departure: d("2026-10-01"), Adults: 1}}})
	must(t, err)
	_, err = f.Front.CheckIn(f.admin, f.propID, dr.ID, dr.Rooms[0].ID, "", f.in(dr, &f.r101))
	wantCode(t, err, "RESERVATION_NOT_CONFIRMED")
	_, err = f.Front.CheckIn(f.admin, f.propID, res.ID, 99999, "", f.in(res, &f.r101))
	wantCode(t, err, "RESERVATION_ROOM_NOT_FOUND")
	// permissions
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Front.CheckIn(reader, f.propID, res.ID, res.Rooms[0].ID, "", f.in(res, &f.r101))
	wantCode(t, err, "PERMISSION_DENIED")
	// accompanying guests are stored, without duplicates or the primary guest
	var comp int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G2', 'Companion', $2) RETURNING id`, f.tenantID, f.propID).Scan(&comp))
	in := f.in(res, &f.r101)
	in.AccompanyingGuestIDs = []int64{comp, comp, f.guest}
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", in)
	must(t, err)
	detail, err := f.Front.GetStay(f.admin, f.propID, out.Stay.ID)
	must(t, err)
	if len(detail.Guests) != 1 || detail.Guests[0].Code != "G2" || detail.Guest.Code != "G1" {
		t.Fatalf("guests: %+v", detail)
	}
}

func TestCleanlinessRule(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	// a dirty room is refused, with what is current and what is needed
	_, err := f.checkIn(t, f.admin, res, &f.r102, "")
	e := code(t, err, "ROOM_NOT_READY")
	if e.Context["current"] != "DIRTY" || e.Context["required"] != "CLEAN" {
		t.Fatalf("context: %v", e.Context)
	}
	// an override needs the permission and a reason
	over := f.in(res, &f.r102)
	over.OverrideRoomNotReady = true
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin, auth.PermReservationRead, auth.PermGuestRead)
	_, err = f.Front.CheckIn(clerk, f.propID, res.ID, res.Rooms[0].ID, "", over)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", over)
	wantCode(t, err, "VALIDATION_FAILED") // no reason
	supervisor := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin, auth.PermFrontdeskCheckinUnreadyRoom, auth.PermReservationRead, auth.PermGuestRead)
	over.OverrideReason = "guest waiting, housekeeping on the way"
	if _, err := f.Front.CheckIn(supervisor, f.propID, res.ID, res.Rooms[0].ID, "", over); err != nil {
		t.Fatalf("override: %v", err)
	}
	var entry string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'stay.checked_in'`).Scan(&entry))
	var m map[string]any
	must(t, json.Unmarshal([]byte(entry), &m))
	if m["override_room_not_ready"] != true || m["override_reason"] != over.OverrideReason || m["housekeeping_status"] != "DIRTY" {
		t.Fatalf("audit: %v", m)
	}

	// with the property requiring an inspection, CLEAN is not enough and INSPECTED is
	must(t, f.Exec(t, `UPDATE properties SET require_room_inspection_for_checkin = true WHERE id = $1`, f.propID))
	res2 := f.book(t, f.std, "2026-09-30", "2026-10-01")
	_, err = f.checkIn(t, f.admin, res2, &f.r201, "")
	e = code(t, err, "ROOM_NOT_READY")
	if e.Context["current"] != "CLEAN" || e.Context["required"] != "INSPECTED" {
		t.Fatalf("context: %v", e.Context)
	}
	_, err = f.HK.SetStatus(f.admin, f.propID, f.r201.ID, housekeeping.Inspected, "")
	must(t, err)
	if _, err := f.checkIn(t, f.admin, res2, &f.r201, ""); err != nil {
		t.Fatalf("inspected room: %v", err)
	}
}

func TestRoomMustBeFree(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	// blocked: one DLX room may be blocked while one is booked
	if _, err := f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.r102.ID, BlockType: "OOO", StartDate: d("2026-09-30"), EndDate: d("2026-10-02"), Reason: "AC"}); err != nil {
		t.Fatal(err)
	}
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, f.r102.ID))
	_, err := f.checkIn(t, f.admin, res, &f.r102, "")
	wantCode(t, err, "ROOM_BLOCKED")
	// occupied by another stay
	f.Stay(t, f.tenantID, f.propID, f.dlx.ID, f.r101.ID, "2026-09-29", "2026-10-02")
	_, err = f.checkIn(t, f.admin, res, &f.r101, "")
	wantCode(t, err, "ROOM_OCCUPIED")
	// inactive
	must(t, f.Exec(t, `UPDATE rooms SET is_active = false WHERE id = $1`, f.r102.ID))
	wantCode(t, mustErr(f.checkIn(t, f.admin, res, &f.r102, "")), "ROOM_NOT_AVAILABLE")
	if f.Count(t, `SELECT count(*) FROM stays WHERE status = 'OPEN'`) != 1 {
		t.Fatal("only the seeded stay is open")
	}
}

func mustErr(_ frontdesk.CheckInResult, err error) error { return err }

func TestUpgradeAtCheckIn(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.std, "2026-09-30", "2026-10-02")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin, auth.PermReservationRead, auth.PermGuestRead)
	_, err := f.Front.CheckIn(clerk, f.propID, res.ID, res.Rooms[0].ID, "", f.in(res, &f.r101))
	wantCode(t, err, "PERMISSION_DENIED")
	// both DLX rooms are wanted on those nights: no inventory to upgrade into
	f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	_, err = f.checkIn(t, f.admin, res, &f.r101, "")
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
}

func TestConcurrentCheckInsIntoOneRoom(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	b := f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, r := range []reservations.Reservation{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.checkIn(t, f.admin, r, &f.r101, "")
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one check-in wins: %v / %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil {
			e, _ := asApp(err)
			if e != "ROOM_OCCUPIED" && e != "ROOM_NOT_AVAILABLE" {
				t.Fatalf("loser: %v", err)
			}
		}
	}
	if f.Count(t, `SELECT count(*) FROM stay_rooms WHERE room_id = $1 AND check_out_at IS NULL`, f.r101.ID) != 1 {
		t.Fatal("one open segment per room")
	}
}

func TestSameLineCheckedInTwiceAtOnce(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	ids := make([]int64, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.checkIn(t, f.admin, res, &f.r101, "same-key")
			ids[i], errs[i] = r.Stay.ID, err
		}()
	}
	wg.Wait()
	for i := range errs {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("request %d: %v (stay %d, first %d)", i, errs[i], ids[i], ids[0])
		}
	}
	if f.Count(t, `SELECT count(*) FROM stays`) != 1 || f.Count(t, `SELECT count(*) FROM folios`) != 1 {
		t.Fatal("one stay and one folio")
	}
	// without a key, concurrent clicks: one stay, the others refused
	res2 := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	errs2 := make([]error, 4)
	for i := range errs2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs2[i] = f.checkIn(t, f.admin, res2, &f.r102, "")
			_ = i
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs2 {
		if err == nil {
			ok++
		}
	}
	if ok > 1 || f.Count(t, `SELECT count(*) FROM stays WHERE reservation_room_id = $1`, res2.Rooms[0].ID) > 1 {
		t.Fatalf("%d check-ins for one line", ok)
	}
}

func TestWalkIn(t *testing.T) {
	f := setup(t)
	in := frontdesk.WalkInInput{GuestID: &f.guest, RoomID: f.r101.ID, RatePlanID: f.plan, DepartureDate: d("2026-10-02"), AdultCount: 2}
	out, err := f.Front.WalkIn(f.admin, f.propID, "w1", in)
	must(t, err)
	if out.Reservation == nil || out.Reservation.ConfirmationNumber == "" || out.Stay.Status != "OPEN" || out.StayRoom.RoomNumber != "101" || out.Folio.ID == 0 {
		t.Fatalf("walk-in: %+v", out)
	}
	res := f.reload(t, out.Reservation.ID)
	if res.Source != "WALK_IN" || res.Status != "CONFIRMED" || res.Rooms[0].Status != "CHECKED_IN" || res.ArrivalDate != roomstest.BD || len(res.Rooms[0].NightlyRates) != 2 || res.DisplayStatus != "IN_HOUSE" {
		t.Fatalf("reservation: %+v", res)
	}
	// retries with the key answer with the same stay
	again, err := f.Front.WalkIn(f.admin, f.propID, "w1", in)
	if err != nil || again.Stay.ID != out.Stay.ID || f.Count(t, `SELECT count(*) FROM reservations`) != 1 {
		t.Fatalf("replay: %v %+v", err, again)
	}
	// a room that is occupied, a dirty room, an unknown room, missing rates
	_, err = f.Front.WalkIn(f.admin, f.propID, "w2", in)
	wantCode(t, err, "ROOM_NOT_AVAILABLE") // its own stay holds it
	in2 := in
	in2.RoomID = f.r102.ID
	_, err = f.Front.WalkIn(f.admin, f.propID, "w3", in2)
	wantCode(t, err, "ROOM_NOT_READY")
	if f.Count(t, `SELECT count(*) FROM reservations`) != 1 {
		t.Fatal("a refused walk-in leaves no reservation behind")
	}
	in2.RoomID = 99999
	_, err = f.Front.WalkIn(f.admin, f.propID, "w4", in2)
	wantCode(t, err, "ROOM_NOT_FOUND")
	in3 := in
	in3.RoomID, in3.DepartureDate = f.r201.ID, d("2026-11-30")
	_, err = f.Front.WalkIn(f.admin, f.propID, "w5", in3)
	wantCode(t, err, "RATE_NOT_SET")
	in3.DepartureDate = roomstest.BD
	_, err = f.Front.WalkIn(f.admin, f.propID, "w6", in3)
	wantCode(t, err, "VALIDATION_FAILED")
	// a new guest is created with the walk-in; both or neither guest fields are refused
	in4 := frontdesk.WalkInInput{NewGuest: &guests.Profile{LastName: "Walker", FirstName: "Wendy"}, RoomID: f.r201.ID, RatePlanID: f.plan, DepartureDate: d("2026-10-01"), AdultCount: 1}
	res4, err := f.Front.WalkIn(f.admin, f.propID, "w7", in4)
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM guests WHERE last_name = 'Walker'`) != 1 || res4.Stay.GuestID == 0 {
		t.Fatalf("new guest: %+v", res4)
	}
	in4.GuestID = &f.guest
	_, err = f.Front.WalkIn(f.admin, f.propID, "w8", in4)
	wantCode(t, err, "VALIDATION_FAILED")
	// both permissions are needed
	front := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin)
	_, err = f.Front.WalkIn(front, f.propID, "w9", in)
	wantCode(t, err, "PERMISSION_DENIED")
	// two walk-ins for the last room at once: one wins
	var wg sync.WaitGroup
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, f.r102.ID))
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			in := frontdesk.WalkInInput{GuestID: &f.guest, RoomID: f.r102.ID, RatePlanID: f.plan, DepartureDate: d("2026-10-01"), AdultCount: 1}
			_, errs[i] = f.Front.WalkIn(f.admin, f.propID, "race-"+string(rune('a'+i)), in)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 || f.Count(t, `SELECT count(*) FROM stay_rooms WHERE room_id = $1 AND check_out_at IS NULL`, f.r102.ID) != 1 {
		t.Fatalf("%d walk-ins into one room: %v", ok, errs)
	}
}

func TestReverseCheckIn(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	dep, err := f.Folios.Deposit(f.admin, f.propID, res.ID, "d1", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	out, err := f.checkIn(t, f.admin, res, &f.r101, "")
	must(t, err)
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, out.Stay.ID, frontdesk.ReverseInput{Version: out.Stay.Version})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, out.Stay.ID, frontdesk.ReverseInput{Version: 9, Reason: "wrong guest"})
	wantCode(t, err, "VERSION_CONFLICT")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskCheckin)
	_, err = f.Front.ReverseCheckIn(clerk, f.propID, out.Stay.ID, frontdesk.ReverseInput{Version: out.Stay.Version, Reason: "x"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, 99999, frontdesk.ReverseInput{Version: 1, Reason: "x"})
	wantCode(t, err, "STAY_NOT_FOUND")

	rev, err := f.Front.ReverseCheckIn(f.admin, f.propID, out.Stay.ID, frontdesk.ReverseInput{Version: out.Stay.Version, Reason: "wrong guest"})
	must(t, err)
	if rev.Stay.Status != "CANCELLED" || rev.Folio.ID != dep.Payment.FolioID {
		t.Fatalf("reversal: %+v", rev)
	}
	after := f.reload(t, res.ID)
	if after.Rooms[0].Status != "CONFIRMED" || after.Rooms[0].RoomNumber != "101" || after.Rooms[0].StayID != nil || after.DisplayStatus != "CONFIRMED" {
		t.Fatalf("line after: %+v", after.Rooms[0])
	}
	var stay *int64
	var closed bool
	must(t, f.Pool.QueryRow(context.Background(), `SELECT stay_id FROM folios WHERE id = $1`, dep.Payment.FolioID).Scan(&stay))
	must(t, f.Pool.QueryRow(context.Background(), `SELECT check_out_at IS NOT NULL FROM stay_rooms WHERE stay_id = $1`, out.Stay.ID).Scan(&closed))
	if stay != nil || !closed {
		t.Fatalf("folio unlinked %v, segment closed %v", stay, closed)
	}
	if st, _ := f.HK.Board(f.admin, f.propID, housekeeping.BoardFilter{}); len(st) == 0 {
		t.Fatal("board")
	}
	var hk string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT status FROM room_housekeeping WHERE room_id = $1`, f.r101.ID).Scan(&hk))
	if hk != "DIRTY" {
		t.Fatalf("the room goes DIRTY, got %s", hk)
	}
	if f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE source = 'CHECK_IN_REVERSAL'`) != 1 {
		t.Fatal("logged")
	}
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, out.Stay.ID, frontdesk.ReverseInput{Version: rev.Stay.Version, Reason: "again"})
	wantCode(t, err, "STAY_NOT_OPEN")
	// the guest can check in again; the same folio is linked again
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, f.r101.ID))
	again, err := f.checkIn(t, f.admin, after, &f.r101, "")
	must(t, err)
	if again.Folio.ID != dep.Payment.FolioID || again.Stay.ID == out.Stay.ID {
		t.Fatalf("second check-in: %+v", again)
	}

	// a posted charge makes it irreversible
	if _, err := f.Folios.PostCharge(f.admin, f.propID, again.Folio.ID, "c1", folios.ChargeInput{ChargeCodeID: f.laundry(t), Quantity: "1", UnitPrice: ptr("10000")}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, again.Stay.ID, frontdesk.ReverseInput{Version: again.Stay.Version, Reason: "x"})
	wantCode(t, err, "CHECK_IN_HAS_CHARGES")
}

func (f *fx) laundry(t *testing.T) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'LAUNDRY'`, f.propID).Scan(&id))
	return id
}

func TestReverseNeedsTheSameBusinessDay(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-03")
	out, err := f.checkIn(t, f.admin, res, &f.r101, "")
	must(t, err)
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, roomstest.BD, nil, json.RawMessage(`{}`))
		return err
	}))
	_, err = f.Front.ReverseCheckIn(f.admin, f.propID, out.Stay.ID, frontdesk.ReverseInput{Version: out.Stay.Version, Reason: "late"})
	wantCode(t, err, "CHECK_IN_NOT_REVERSIBLE")
}

func TestArrivalsShowTheStatusOfTheAssignedRoom(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	_, err := f.Res.AssignRoom(f.admin, f.propID, res.ID, res.Rooms[0].ID, res.Version, f.r101.ID, false)
	must(t, err)
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'INSPECTED' WHERE room_id = $1`, f.r101.ID))
	arr, err := f.Front.Arrivals(f.admin, f.propID, nil)
	must(t, err)
	if len(arr) != 1 || arr[0].RoomNumber != "101" || arr[0].HousekeepingStatus != "INSPECTED" {
		t.Fatalf("arrivals: %+v", arr)
	}
	f.book(t, f.std, "2026-09-30", "2026-10-01")
	arr, _ = f.Front.Arrivals(f.admin, f.propID, nil)
	for _, a := range arr {
		if a.RoomID == nil && a.HousekeepingStatus != "" {
			t.Fatalf("no room, no status: %+v", a)
		}
	}
}

func TestStayReads(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	f.book(t, f.std, "2026-09-30", "2026-10-01")
	f.book(t, f.std, "2026-10-05", "2026-10-06")
	arr, err := f.Front.Arrivals(f.admin, f.propID, nil)
	must(t, err)
	if len(arr) != 2 || arr[0].GuestName != "Guest" || arr[0].ReservationVersion == 0 || arr[0].RoomTypeCode == "" {
		t.Fatalf("arrivals: %+v", arr)
	}
	day := d("2026-10-05")
	later, err := f.Front.Arrivals(f.admin, f.propID, &day)
	must(t, err)
	if len(later) != 1 {
		t.Fatalf("arrivals on the 5th: %+v", later)
	}
	out, err := f.checkIn(t, f.admin, a, &f.r101, "")
	must(t, err)
	arr, _ = f.Front.Arrivals(f.admin, f.propID, nil)
	if len(arr) != 1 {
		t.Fatalf("a checked-in room is no longer an arrival: %+v", arr)
	}
	list, err := f.Front.ListStays(f.admin, f.propID, frontdesk.StayFilter{Status: "OPEN"}, 0, 10)
	must(t, err)
	if len(list) != 1 || list[0].RoomNumber != "101" || list[0].GuestName != "Guest" || list[0].ConfirmationNumber != a.ConfirmationNumber {
		t.Fatalf("stays: %+v", list)
	}
	dep := d("2026-10-02")
	if got, _ := f.Front.ListStays(f.admin, f.propID, frontdesk.StayFilter{DepartureDate: &dep, RoomID: &f.r101.ID}, 0, 10); len(got) != 1 {
		t.Fatalf("filters: %+v", got)
	}
	if got, _ := f.Front.ListStays(f.admin, f.propID, frontdesk.StayFilter{Status: "CHECKED_OUT"}, 0, 10); len(got) != 0 {
		t.Fatalf("checked out: %+v", got)
	}
	detail, err := f.Front.GetStay(f.admin, f.propID, out.Stay.ID)
	must(t, err)
	if len(detail.Segments) != 1 || len(detail.NightlyRates) != 2 || detail.NightlyRates[0].Posted || detail.NightlyRates[0].Amount != "1000000" ||
		len(detail.Folios) != 1 || detail.Line.RoomTypeCode != "DLX" || detail.Line.Status != "CHECKED_IN" {
		t.Fatalf("detail: %+v", detail)
	}
	_, err = f.Front.GetStay(f.admin, f.propID, 99999)
	wantCode(t, err, "STAY_NOT_FOUND")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Front.GetStay(nobody, f.propID, out.Stay.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Front.ListStays(nobody, f.propID, frontdesk.StayFilter{}, 0, 10)
	wantCode(t, err, "PERMISSION_DENIED")
	// another tenant sees nothing
	foreign := f.Tenant(t, "XYZ")
	fp := f.Property(t, foreign.ID, "FOR")
	fctx, _ := f.AdminAccount(t, foreign.ID)
	_, err = f.Front.GetStay(fctx, fp.ID, out.Stay.ID)
	wantCode(t, err, "STAY_NOT_FOUND")
	_, err = f.Front.GetStay(fctx, f.propID, out.Stay.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func asApp(err error) (string, bool) {
	if e, ok := apperr.As(err); ok {
		return e.Code, true
	}
	return "", false
}

// The detailed calendar splits the held rooms into in-house and reservations, and counts arrivals: the lines that
// arrive plus the stays (checked in, walk-ins included) whose arrival date it is.
func TestCalendarParts(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	_, err := f.checkIn(t, f.admin, a, &f.r101, "cal-1")
	must(t, err)
	f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	f.book(t, f.dlx, "2026-10-01", "2026-10-02")

	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, roomstest.BD, d("2026-10-02"), false)
	must(t, err)
	for i, want := range []struct{ inHouse, arrivals, reservations, held int }{{1, 2, 1, 2}, {1, 1, 1, 2}} {
		n := cal.RoomTypes[0].Nights[i]
		if n.InHouse != want.inHouse || n.Arrivals != want.arrivals || n.Reservations != want.reservations || n.Held != want.held {
			t.Fatalf("night %d: %+v want %+v", i, n, want)
		}
		if tot := cal.Totals[i]; tot.InHouse != want.inHouse || tot.Arrivals != want.arrivals || tot.Reservations != want.reservations {
			t.Fatalf("totals %d: %+v", i, tot)
		}
	}
}
