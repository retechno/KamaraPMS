package reservations_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/availability"
	"kamarapms/internal/guests"
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

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	dlx, std         rooms.RoomType
	r101, r102, r201 rooms.Room
	plan, guest      int64
}

// setup: a property with DLX (rooms 101, 102) and STD (room 201), a BAR plan priced 1,000,000 (DLX) and
// 500,000 (STD) for every night of October 2026, and a guest.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin := roomstest.Admin(tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin}
	f.dlx = e.RoomType(t, admin, p.ID, "DLX")
	f.std = e.RoomType(t, admin, p.ID, "STD")
	f.r101, f.r102 = e.Room(t, admin, p.ID, f.dlx.ID, "101"), e.Room(t, admin, p.ID, f.dlx.ID, "102")
	f.r201 = e.Room(t, admin, p.ID, f.std.ID, "201")
	var chargeID int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`,
		tn.ID, p.ID, chargeID).Scan(&f.plan))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name) VALUES ($1, 'G1', 'Guest') RETURNING id`, tn.ID).Scan(&f.guest))
	for typ, amount := range map[int64]string{f.dlx.ID: "1000000", f.std.ID: "500000"} {
		_, err := e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{typ}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: amount})
		must(t, err)
	}
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fx) line(typ rooms.RoomType, arrival, departure string) reservations.LineInput {
	return reservations.LineInput{RoomTypeID: typ.ID, RatePlanID: f.plan, Arrival: d(arrival), Departure: d(departure), Adults: 2}
}

func (f *fx) input(confirm bool, lines ...reservations.LineInput) reservations.CreateInput {
	return reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Rooms: lines, Confirm: confirm}
}

func (f *fx) book(t *testing.T, typ rooms.RoomType, arrival, departure string) reservations.Reservation {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(typ, arrival, departure)))
	must(t, err)
	return res
}

func (f *fx) available(t *testing.T, typ rooms.RoomType, night string) availability.Night {
	t.Helper()
	inv, err := f.Avail.Inventory(f.admin, f.tenantID, f.propID, []int64{typ.ID}, []civil.Date{d(night)}, roomstest.BD, nil)
	must(t, err)
	return inv[typ.ID][d(night)]
}

func (f *fx) count(t *testing.T, sql string, args ...any) int { return f.Count(t, sql, args...) }

func TestDraftHoldsNothingAndConfirmClaimsInventory(t *testing.T) {
	f := setup(t)
	res, err := f.Res.Create(f.admin, f.propID, "k1", f.input(false, f.line(f.std, "2026-10-01", "2026-10-03")))
	must(t, err)
	if res.Status != "DRAFT" || res.DisplayStatus != "DRAFT" || res.Version != 1 || len(res.Rooms) != 1 || res.Rooms[0].Status != "DRAFT" {
		t.Fatalf("draft: %+v", res)
	}
	if res.ConfirmationNumber == "" || res.ReservationDate != roomstest.BD || res.ArrivalDate != d("2026-10-01") || res.DepartureDate != d("2026-10-03") {
		t.Fatalf("header: %+v", res)
	}
	line := res.Rooms[0]
	if len(line.NightlyRates) != 2 || line.NightlyRates[0].Amount.String() != "500000" || line.NightlyRates[0].IsOverride || line.NightlyRates[0].BaseRate == nil {
		t.Fatalf("nightly: %+v", line.NightlyRates)
	}
	if line.Estimate.Total.IsZero() || line.Nights != 2 || line.RoomTypeCode != "STD" {
		t.Fatalf("line: %+v", line)
	}
	if n := f.available(t, f.std, "2026-10-01"); n.Available != 1 {
		t.Fatalf("a draft holds no inventory: %+v", n)
	}
	// stale version is refused, the right one confirms
	_, err = f.Res.Confirm(f.admin, f.propID, res.ID, 7)
	wantCode(t, err, "VERSION_CONFLICT")
	conf, err := f.Res.Confirm(f.admin, f.propID, res.ID, res.Version)
	must(t, err)
	if conf.Status != "CONFIRMED" || conf.Version != 2 || conf.Rooms[0].Status != "CONFIRMED" || conf.ConfirmedAt == nil {
		t.Fatalf("confirmed: %+v", conf)
	}
	if n := f.available(t, f.std, "2026-10-02"); n.Available != 0 || n.Demand != 1 {
		t.Fatalf("confirmed holds: %+v", n)
	}
	if n := f.available(t, f.std, "2026-10-03"); n.Available != 1 {
		t.Fatalf("departure night is free: %+v", n)
	}
	// the second STD booking for the same nights is refused, with the failing nights listed
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.std, "2026-10-02", "2026-10-04")))
	e := code(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	if e.Context["short_nights"] != 1 {
		t.Fatalf("context: %v", e.Context)
	}
	if got := f.count(t, `SELECT count(*) FROM reservations`); got != 1 {
		t.Fatalf("nothing is written by a refused booking: %d reservations", got)
	}
	// confirm needs a booker
	noBooker := f.input(false, f.line(f.dlx, "2026-10-05", "2026-10-06"))
	noBooker.GuestID = nil
	dr, err := f.Res.Create(f.admin, f.propID, "", noBooker)
	must(t, err)
	_, err = f.Res.Confirm(f.admin, f.propID, dr.ID, dr.Version)
	wantCode(t, err, "VALIDATION_FAILED")
	wantCode(t, mustErr(f.Res.Create(f.admin, f.propID, "", withConfirm(noBooker))), "VALIDATION_FAILED")
}

func withConfirm(in reservations.CreateInput) reservations.CreateInput { in.Confirm = true; return in }

func mustErr(_ reservations.Reservation, err error) error { return err }

func TestCreateValidation(t *testing.T) {
	f := setup(t)
	try := func(edit func(*reservations.CreateInput)) error {
		in := f.input(false, f.line(f.dlx, "2026-10-01", "2026-10-03"))
		edit(&in)
		_, err := f.Res.Create(f.admin, f.propID, "", in)
		return err
	}
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].Arrival = d("2026-09-29") }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].Departure = d("2026-10-01") }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].Departure = d("2028-01-01") }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].Adults = 0 }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].Adults, in.Rooms[0].Children = 3, 1 }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Source = "CARRIER_PIGEON" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms = nil }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].RoomID = &f.r101.ID }), "VALIDATION_FAILED") // room needs confirm
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].RoomTypeID = 99999 }), "ROOM_TYPE_NOT_FOUND")
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].RatePlanID = 99999 }), "RATE_PLAN_NOT_FOUND")
	wantCode(t, try(func(in *reservations.CreateInput) { in.GuestID = ptr(int64(99999)) }), "GUEST_NOT_FOUND")
	// nights without a grid price are named
	e := code(t, try(func(in *reservations.CreateInput) {
		in.Rooms[0].Arrival, in.Rooms[0].Departure = d("2026-10-20"), d("2026-10-23")
		in.RateOverrideReason = "agreed with the guest"
	}), "RATE_NOT_SET")
	if e.Context["missing_nights"] != 2 {
		t.Fatalf("missing: %v", e.Context)
	}
	// ... unless overridden (needs the permission)
	over := func(in *reservations.CreateInput) {
		in.Rooms[0].Arrival, in.Rooms[0].Departure = d("2026-10-20"), d("2026-10-23")
		in.RateOverrideReason = "agreed with the guest"
		in.Rooms[0].Overrides = []reservations.NightOverride{{Date: d("2026-10-21"), Amount: "700000"}, {Date: d("2026-10-22"), Amount: "700000", DiscountAmount: "50000"}}
	}
	must(t, try(over))
	// an override outside the stay, a duplicate and a decimal on a 0-decimal currency are refused
	wantCode(t, try(func(in *reservations.CreateInput) {
		in.RateOverrideReason = "x"
		in.Rooms[0].Overrides = []reservations.NightOverride{{Date: d("2026-10-09"), Amount: "1"}}
	}), "VALIDATION_FAILED")
	wantCode(t, try(func(in *reservations.CreateInput) {
		in.RateOverrideReason = "x"
		in.Rooms[0].Overrides = []reservations.NightOverride{{Date: d("2026-10-01"), Amount: "10.5"}}
	}), "VALIDATION_FAILED")
	// a room of another type cannot be assigned at booking time
	wantCode(t, mustErr(f.Res.Create(f.admin, f.propID, "", f.input(true, withRoom(f.line(f.std, "2026-10-01", "2026-10-02"), f.r101.ID)))), "VALIDATION_FAILED")

	// permissions: create is needed; override_rate for overrides
	clerk := f.User(t, f.tenantID, f.propID, auth.PermReservationCreate, auth.PermReservationRead)
	in := f.input(false, f.line(f.dlx, "2026-10-01", "2026-10-03"))
	in.GuestID = nil
	if _, err := f.Res.Create(clerk, f.propID, "", in); err != nil {
		t.Fatalf("clerk: %v", err)
	}
	in.Rooms[0].Overrides = []reservations.NightOverride{{Date: d("2026-10-01"), Amount: "1"}}
	_, err := f.Res.Create(clerk, f.propID, "", in)
	wantCode(t, err, "PERMISSION_DENIED")
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Res.Create(reader, f.propID, "", f.input(false, f.line(f.dlx, "2026-10-01", "2026-10-03")))
	wantCode(t, err, "PERMISSION_DENIED")
	// an inactive room type cannot be booked
	must(t, f.Exec(t, `UPDATE room_types SET is_active = false WHERE id = $1`, f.std.ID))
	wantCode(t, try(func(in *reservations.CreateInput) { in.Rooms[0].RoomTypeID = f.std.ID }), "ROOM_TYPE_INACTIVE")
}

func ptr[T any](v T) *T { return &v }

func withRoom(l reservations.LineInput, room int64) reservations.LineInput {
	l.RoomID = &room
	return l
}

func TestIdempotency(t *testing.T) {
	f := setup(t)
	in := f.input(true, f.line(f.dlx, "2026-10-01", "2026-10-03"))
	a, err := f.Res.Create(f.admin, f.propID, "key-1", in)
	must(t, err)
	b, err := f.Res.Create(f.admin, f.propID, "key-1", in)
	must(t, err)
	if a.ID != b.ID || a.ConfirmationNumber != b.ConfirmationNumber || f.count(t, `SELECT count(*) FROM reservations`) != 1 {
		t.Fatalf("replay: %d vs %d", a.ID, b.ID)
	}
	other := f.input(true, f.line(f.dlx, "2026-10-01", "2026-10-04"))
	_, err = f.Res.Create(f.admin, f.propID, "key-1", other)
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")

	// concurrent requests with one key create exactly one reservation and all see it
	in2 := f.input(true, f.line(f.dlx, "2026-10-10", "2026-10-12"))
	var wg sync.WaitGroup
	ids := make([]int64, 6)
	errs := make([]error, 6)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.Res.Create(f.admin, f.propID, "key-2", in2)
			ids[i], errs[i] = r.ID, err
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("request %d: %v id %d (want %d)", i, errs[i], ids[i], ids[0])
		}
	}
	if got := f.count(t, `SELECT count(*) FROM reservations WHERE idempotency_key = 'key-2'`); got != 1 {
		t.Fatalf("%d reservations for one key", got)
	}
}

func TestLastRoomBookedConcurrentlyGoesToOne(t *testing.T) {
	f := setup(t)
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.std, "2026-10-02", "2026-10-05")))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
		}
	}
	if ok != 1 || f.count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'CONFIRMED'`) != 1 {
		t.Fatalf("%d of %d bookings won the last room", ok, n)
	}
}

func TestConcurrentConfirmOfDraftsRespectsInventory(t *testing.T) {
	f := setup(t)
	var drafts []reservations.Reservation
	for i := 0; i < 4; i++ {
		r, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.line(f.std, "2026-10-02", "2026-10-04")))
		must(t, err)
		drafts = append(drafts, r)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(drafts))
	for i, r := range drafts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Res.Confirm(f.admin, f.propID, r.ID, r.Version)
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
		}
	}
	if ok != 1 {
		t.Fatalf("%d drafts confirmed for one room", ok)
	}
}

func TestConcurrentAssignmentOfOneRoom(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-10-02", "2026-10-05")
	b := f.book(t, f.dlx, "2026-10-03", "2026-10-06")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, r := range []reservations.Reservation{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Res.AssignRoom(f.admin, f.propID, r.ID, r.Rooms[0].ID, r.Version, f.r101.ID, false)
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one assignment must win: %v / %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil {
			wantCode(t, err, "ROOM_NOT_AVAILABLE")
		}
	}
	if got := f.count(t, `SELECT count(*) FROM reservation_rooms WHERE room_id = $1`, f.r101.ID); got != 1 {
		t.Fatalf("%d lines hold room 101", got)
	}
}

func TestExcludeConstraintIsTheBackstop(t *testing.T) {
	f := setup(t)
	f.Line(t, f.tenantID, f.propID, f.dlx.ID, f.r101.ID, "2026-10-02", "2026-10-05", "CONFIRMED")
	err := f.Exec(t, `INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status)
		SELECT tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, '2026-10-04', '2026-10-06', 2, 'CONFIRMED' FROM reservation_rooms LIMIT 1`)
	if err == nil {
		t.Fatal("two confirmed lines held the same room on overlapping nights")
	}
	// back to back is allowed
	if err := f.Exec(t, `INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status)
		SELECT tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, '2026-10-05', '2026-10-06', 2, 'CONFIRMED' FROM reservation_rooms LIMIT 1`); err != nil {
		t.Fatalf("same-day turnover at the constraint: %v", err)
	}
}

func TestAssignSameDayTurnoverAndBlocks(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-10-01", "2026-10-03")
	b := f.book(t, f.dlx, "2026-10-03", "2026-10-05")
	_, err := f.Res.AssignRoom(f.admin, f.propID, a.ID, a.Rooms[0].ID, a.Version, f.r101.ID, false)
	must(t, err)
	got, err := f.Res.AssignRoom(f.admin, f.propID, b.ID, b.Rooms[0].ID, b.Version, f.r101.ID, false)
	if err != nil || got.Rooms[0].RoomNumber != "101" || got.Version != b.Version+1 {
		t.Fatalf("turnover: %v %+v", err, got)
	}
	// a block on the room for those nights refuses another assignment
	c := f.book(t, f.dlx, "2026-10-10", "2026-10-12")
	f.book(t, f.dlx, "2026-10-10", "2026-10-12") // both DLX rooms are wanted on those nights
	_, err = f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.r102.ID, BlockType: "OOO", StartDate: d("2026-10-10"), EndDate: d("2026-10-11"), Reason: "AC"})
	wantCode(t, err, "INVENTORY_OVERSOLD")
	_, err = f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.r102.ID, BlockType: "OOO", StartDate: d("2026-10-14"), EndDate: d("2026-10-15"), Reason: "AC"})
	must(t, err)
	_, err = f.Res.AssignRoom(f.admin, f.propID, c.ID, c.Rooms[0].ID, c.Version, f.r102.ID, false)
	must(t, err) // the block is on other nights
	// unknown room, draft line
	_, err = f.Res.AssignRoom(f.admin, f.propID, c.ID, c.Rooms[0].ID, c.Version+1, 99999, false)
	wantCode(t, err, "ROOM_NOT_FOUND")
	dr, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.line(f.dlx, "2026-10-16", "2026-10-17")))
	must(t, err)
	_, err = f.Res.AssignRoom(f.admin, f.propID, dr.ID, dr.Rooms[0].ID, dr.Version, f.r101.ID, false)
	wantCode(t, err, "LINE_NOT_CONFIRMED")
	// an in-house stay in the room blocks the assignment
	f.Stay(t, f.tenantID, f.propID, f.dlx.ID, f.r101.ID, "2026-09-30", "2026-10-16")
	e := f.book(t, f.dlx, "2026-10-15", "2026-10-16")
	_, err = f.Res.AssignRoom(f.admin, f.propID, e.ID, e.Rooms[0].ID, e.Version, f.r101.ID, false)
	wantCode(t, err, "ROOM_NOT_AVAILABLE")
}

func TestUpgradeConsumesThePhysicalType(t *testing.T) {
	f := setup(t)
	r := f.book(t, f.std, "2026-10-02", "2026-10-04") // the only STD room is booked
	_, err := f.Res.AssignRoom(f.admin, f.propID, r.ID, r.Rooms[0].ID, r.Version, f.r101.ID, false)
	wantCode(t, err, "ROOM_TYPE_MISMATCH")
	agent := f.User(t, f.tenantID, f.propID, auth.PermReservationUpdate, auth.PermReservationRead)
	_, err = f.Res.AssignRoom(agent, f.propID, r.ID, r.Rooms[0].ID, r.Version, f.r101.ID, true)
	wantCode(t, err, "PERMISSION_DENIED")

	up, err := f.Res.AssignRoom(f.admin, f.propID, r.ID, r.Rooms[0].ID, r.Version, f.r101.ID, true)
	must(t, err)
	if up.Rooms[0].RoomTypeCode != "STD" || up.Rooms[0].RoomNumber != "101" {
		t.Fatalf("upgraded: %+v", up.Rooms[0])
	}
	if n := f.available(t, f.std, "2026-10-02"); n.Available != 1 {
		t.Fatalf("the booked type is free again: %+v", n)
	}
	if n := f.available(t, f.dlx, "2026-10-02"); n.Available != 1 || n.Demand != 1 {
		t.Fatalf("the physical type is consumed: %+v", n)
	}
	// with the STD room taken by someone else, going back is refused; freeing it makes it work
	other := f.book(t, f.std, "2026-10-02", "2026-10-04")
	_, err = f.Res.UnassignRoom(f.admin, f.propID, r.ID, r.Rooms[0].ID, up.Version)
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	_, err = f.Res.CancelLine(f.admin, f.propID, other.ID, other.Rooms[0].ID, other.Version, "changed mind")
	must(t, err)
	back, err := f.Res.UnassignRoom(f.admin, f.propID, r.ID, r.Rooms[0].ID, up.Version)
	if err != nil || back.Rooms[0].RoomID != nil {
		t.Fatalf("unassign: %v %+v", err, back)
	}
	// an upgrade into a full physical type is refused
	f.book(t, f.dlx, "2026-10-08", "2026-10-09")
	f.book(t, f.dlx, "2026-10-08", "2026-10-09")
	s := f.book(t, f.std, "2026-10-08", "2026-10-09")
	_, err = f.Res.AssignRoom(f.admin, f.propID, s.ID, s.Rooms[0].ID, s.Version, f.r102.ID, true)
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
}

func TestAmendLine(t *testing.T) {
	f := setup(t)
	in := f.input(true, f.line(f.std, "2026-10-02", "2026-10-04"))
	in.RateOverrideReason = "x"
	in.Rooms[0].Overrides = []reservations.NightOverride{{Date: d("2026-10-02"), Amount: "400000"}}
	res, err := f.Res.Create(f.admin, f.propID, "", in)
	must(t, err)
	line := res.Rooms[0]

	// the line fills the only STD room: extending it works because its own demand is excluded
	ext := d("2026-10-06")
	got, err := f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: res.Version, Departure: &ext})
	must(t, err)
	rates := got.Rooms[0].NightlyRates
	if len(rates) != 4 || rates[0].Amount.String() != "400000" || !rates[0].IsOverride || rates[3].Amount.String() != "500000" {
		t.Fatalf("surviving nights keep their snapshot, new ones come from the grid: %+v", rates)
	}
	// shortening drops the trailing snapshots
	short := d("2026-10-03")
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, Departure: &short})
	must(t, err)
	if len(got.Rooms[0].NightlyRates) != 1 || got.Rooms[0].DepartureDate != short {
		t.Fatalf("shortened: %+v", got.Rooms[0])
	}
	// another booking on the tail nights blocks a later extension
	f.book(t, f.std, "2026-10-04", "2026-10-06")
	late := d("2026-10-05")
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, Departure: &late})
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	// changing the type prices every night again
	dlx := f.dlx.ID
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, RoomTypeID: &dlx})
	must(t, err)
	if got.Rooms[0].RoomTypeCode != "DLX" || got.Rooms[0].NightlyRates[0].Amount.String() != "1000000" || got.Rooms[0].NightlyRates[0].IsOverride {
		t.Fatalf("repriced: %+v", got.Rooms[0])
	}
	// validation, stale version, no assigned-room type change
	badAdults := 9
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, Adults: &badAdults})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version - 1, Adults: ptr(1)})
	wantCode(t, err, "VERSION_CONFLICT")
	got, err = f.Res.AssignRoom(f.admin, f.propID, res.ID, line.ID, got.Version, f.r101.ID, false)
	must(t, err)
	std := f.std.ID
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, RoomTypeID: &std})
	wantCode(t, err, "ROOM_ASSIGNED")
	// moving dates re-checks the assigned room
	x := f.book(t, f.dlx, "2026-10-03", "2026-10-05")
	_, err = f.Res.AssignRoom(f.admin, f.propID, x.ID, x.Rooms[0].ID, x.Version, f.r101.ID, false)
	must(t, err)
	end := d("2026-10-04")
	_, err = f.Res.AmendLine(f.admin, f.propID, res.ID, line.ID, reservations.LinePatch{Version: got.Version, Departure: &end})
	wantCode(t, err, "ROOM_NOT_AVAILABLE")
}

func TestCancelAndReinstate(t *testing.T) {
	f := setup(t)
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.std, "2026-10-02", "2026-10-04"), f.line(f.dlx, "2026-10-02", "2026-10-04")))
	must(t, err)
	_, err = f.Res.Cancel(f.admin, f.propID, res.ID, res.Version, "")
	wantCode(t, err, "VALIDATION_FAILED")
	f.Clock.Advance(time.Minute)
	// one room cancelled on its own stays cancelled after a reinstate of the reservation
	one, err := f.Res.CancelLine(f.admin, f.propID, res.ID, res.Rooms[1].ID, res.Version, "smaller party")
	must(t, err)
	if one.Reservation.Status != "CONFIRMED" || one.Reservation.Rooms[1].Status != "CANCELLED" || one.RequiresFolioResolution {
		t.Fatalf("line cancel: %+v", one)
	}
	f.Clock.Advance(time.Minute)
	c, err := f.Res.Cancel(f.admin, f.propID, res.ID, one.Reservation.Version, "guest cancelled")
	must(t, err)
	if c.Reservation.Status != "CANCELLED" || c.Reservation.DisplayStatus != "CANCELLED" || !c.FolioBalance.IsZero() || c.RequiresFolioResolution {
		t.Fatalf("cancel: %+v", c)
	}
	if n := f.available(t, f.std, "2026-10-02"); n.Available != 1 {
		t.Fatalf("cancelling releases inventory: %+v", n)
	}
	_, err = f.Res.Cancel(f.admin, f.propID, res.ID, c.Reservation.Version, "again")
	wantCode(t, err, "RESERVATION_CANCELLED")
	_, err = f.Res.UpdateHeader(f.admin, f.propID, res.ID, reservations.HeaderPatch{Version: c.Reservation.Version, Remarks: ptr("x")})
	wantCode(t, err, "RESERVATION_CANCELLED")

	// somebody takes the STD room: reinstating fails, then works once it is free
	other := f.book(t, f.std, "2026-10-03", "2026-10-05")
	_, err = f.Res.Reinstate(f.admin, f.propID, res.ID, c.Reservation.Version)
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	_, err = f.Res.CancelLine(f.admin, f.propID, other.ID, other.Rooms[0].ID, other.Version, "x")
	must(t, err)
	back, err := f.Res.Reinstate(f.admin, f.propID, res.ID, c.Reservation.Version)
	must(t, err)
	if back.Status != "CONFIRMED" || back.Rooms[0].Status != "CONFIRMED" || back.Rooms[1].Status != "CANCELLED" || back.CancelledAt != nil {
		t.Fatalf("reinstated: %+v", back)
	}
	_, err = f.Res.Reinstate(f.admin, f.propID, res.ID, back.Version)
	wantCode(t, err, "RESERVATION_NOT_CANCELLED")

	// cancelling the last active room cancels the reservation
	last, err := f.Res.CancelLine(f.admin, f.propID, res.ID, back.Rooms[0].ID, back.Version, "gone")
	must(t, err)
	if last.Reservation.Status != "CANCELLED" {
		t.Fatalf("last room: %+v", last.Reservation)
	}
	// checked-in rooms cannot be cancelled
	stay := f.Stay(t, f.tenantID, f.propID, f.dlx.ID, f.r102.ID, "2026-09-30", "2026-10-02")
	var resID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT l.reservation_id FROM stays s JOIN reservation_rooms l ON l.id = s.reservation_room_id WHERE s.id = $1`, stay).Scan(&resID))
	cur, err := f.Res.Get(f.admin, f.propID, resID)
	must(t, err)
	_, err = f.Res.Cancel(f.admin, f.propID, resID, cur.Version, "no")
	wantCode(t, err, "RESERVATION_HAS_STAYS")
	if cur.DisplayStatus != "IN_HOUSE" || cur.Rooms[0].StayID == nil {
		t.Fatalf("in house: %+v", cur)
	}
	_, err = f.Res.CancelLine(f.admin, f.propID, resID, cur.Rooms[0].ID, cur.Version, "no")
	wantCode(t, err, "LINE_NOT_CANCELLABLE")
}

func TestCancelReportsFolioBalance(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-10-02", "2026-10-04")
	// a deposit folio with 300,000 on it (deposits arrive in M9; seed the ledger rows directly)
	var folioID int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'F1', $3) RETURNING id`, f.tenantID, f.propID, res.ID).Scan(&folioID))
	var paymentID int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date)
		VALUES ($1, $2, 'P1', $3, 'PAYMENT', 'CASH', 300000, '2026-09-30') RETURNING id`, f.tenantID, f.propID, folioID).Scan(&paymentID))
	must(t, f.Exec(t, `INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, payment_id, description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source)
		VALUES ($1, $2, $3, '2026-09-30', '2026-09-30', 'PAYMENT', $4, 'deposit', 1, 300000, 'INCLUSIVE', -300000, -300000, 300000, 'MANUAL')`, f.tenantID, f.propID, folioID, paymentID))
	c, err := f.Res.Cancel(f.admin, f.propID, res.ID, res.Version, "no show up")
	must(t, err)
	if c.FolioBalance.String() != "-300000" || !c.RequiresFolioResolution || len(c.Reservation.Folios) != 1 {
		t.Fatalf("folio: %+v", c)
	}
}

func TestNoShow(t *testing.T) {
	f := setup(t)
	arriving := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	later := f.book(t, f.dlx, "2026-10-05", "2026-10-06")
	_, err := f.Res.NoShow(f.admin, f.propID, later.ID, later.Rooms[0].ID, later.Version, "")
	wantCode(t, err, "ARRIVAL_NOT_DUE")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermReservationUpdate)
	_, err = f.Res.NoShow(clerk, f.propID, arriving.ID, arriving.Rooms[0].ID, arriving.Version, "")
	wantCode(t, err, "PERMISSION_DENIED")
	if n := f.available(t, f.dlx, "2026-09-30"); n.Demand != 1 {
		t.Fatalf("before: %+v", n)
	}
	ns, err := f.Res.NoShow(f.admin, f.propID, arriving.ID, arriving.Rooms[0].ID, arriving.Version, "did not come")
	must(t, err)
	if ns.Rooms[0].Status != "NO_SHOW" || ns.DisplayStatus != "NO_SHOW" || ns.Rooms[0].NoShowAt == nil || ns.Version != arriving.Version+1 {
		t.Fatalf("no-show: %+v", ns)
	}
	if n := f.available(t, f.dlx, "2026-09-30"); n.Demand != 0 {
		t.Fatalf("a no-show releases inventory: %+v", n)
	}
	_, err = f.Res.NoShow(f.admin, f.propID, arriving.ID, arriving.Rooms[0].ID, ns.Version, "")
	wantCode(t, err, "LINE_NOT_CONFIRMED")
	// a no-show stays a no-show when the reservation is cancelled around it
	_, err = f.Res.CancelLine(f.admin, f.propID, arriving.ID, arriving.Rooms[0].ID, ns.Version, "x")
	wantCode(t, err, "LINE_NOT_CANCELLABLE")
}

func TestAddRoomAndHeaderPatch(t *testing.T) {
	f := setup(t)
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.line(f.std, "2026-10-02", "2026-10-04")))
	must(t, err)
	// on a draft the new room is a draft and holds nothing
	res, err = f.Res.AddLine(f.admin, f.propID, res.ID, res.Version, f.line(f.std, "2026-10-02", "2026-10-04"))
	must(t, err)
	if len(res.Rooms) != 2 || res.Rooms[1].Status != "DRAFT" || res.Version != 2 {
		t.Fatalf("draft add: %+v", res)
	}
	_, err = f.Res.AddLine(f.admin, f.propID, res.ID, res.Version, withRoom(f.line(f.std, "2026-10-02", "2026-10-04"), f.r201.ID))
	wantCode(t, err, "VALIDATION_FAILED")
	// confirming both draft STD rooms needs two rooms: there is one
	_, err = f.Res.Confirm(f.admin, f.propID, res.ID, res.Version)
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	if f.count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'CONFIRMED'`) != 0 {
		t.Fatal("confirm is all or nothing")
	}
	// header: change source and set the booker; stale version refused
	got, err := f.Res.UpdateHeader(f.admin, f.propID, res.ID, reservations.HeaderPatch{Version: res.Version, Source: ptr("EMAIL"), GuestID: &f.guest, Remarks: ptr("late arrival")})
	must(t, err)
	if got.Source != "EMAIL" || got.Remarks != "late arrival" || got.Guest == nil || got.Guest.Code != "G1" || got.Version != res.Version+1 {
		t.Fatalf("header: %+v", got)
	}
	_, err = f.Res.UpdateHeader(f.admin, f.propID, res.ID, reservations.HeaderPatch{Version: res.Version, Source: ptr("PHONE")})
	wantCode(t, err, "VERSION_CONFLICT")
	_, err = f.Res.UpdateHeader(f.admin, f.propID, res.ID, reservations.HeaderPatch{Version: got.Version, Source: ptr("PIGEON")})
	wantCode(t, err, "VALIDATION_FAILED")
	// drop the second room, confirm, then add a room to the confirmed reservation: checked
	cl, err := f.Res.CancelLine(f.admin, f.propID, res.ID, res.Rooms[1].ID, got.Version, "one is enough")
	must(t, err)
	got = cl.Reservation
	got, err = f.Res.Confirm(f.admin, f.propID, res.ID, got.Version)
	must(t, err)
	_, err = f.Res.AddLine(f.admin, f.propID, res.ID, got.Version, f.line(f.std, "2026-10-03", "2026-10-05"))
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	added, err := f.Res.AddLine(f.admin, f.propID, res.ID, got.Version, f.line(f.dlx, "2026-10-03", "2026-10-05"))
	must(t, err)
	if added.Rooms[2].Status != "CONFIRMED" || f.available(t, f.dlx, "2026-10-03").Demand != 1 {
		t.Fatalf("confirmed add: %+v", added.Rooms)
	}
}

func TestReadsPermissionsAndIsolation(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-10-02", "2026-10-04")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err := f.Res.Get(nobody, f.propID, res.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Res.List(nobody, f.propID, reservations.ListFilter{}, 0, 10)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Res.SearchAvailability(nobody, f.propID, d("2026-10-02"), d("2026-10-03"), 2, 0)
	wantCode(t, err, "PERMISSION_DENIED")

	// another property of the same tenant does not see it; neither does another tenant
	other := f.Property(t, f.tenantID, "JAVA")
	_, err = f.Res.Get(f.admin, other.ID, res.ID)
	wantCode(t, err, "RESERVATION_NOT_FOUND")
	foreign := f.Tenant(t, "XYZ")
	fp := f.Property(t, foreign.ID, "FOR")
	_, err = f.Res.Get(roomstest.Admin(foreign.ID), fp.ID, res.ID)
	wantCode(t, err, "RESERVATION_NOT_FOUND")
	_, err = f.Res.Get(roomstest.Admin(foreign.ID), f.propID, res.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Res.Confirm(roomstest.Admin(foreign.ID), f.propID, res.ID, res.Version)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	// a foreign guest cannot be a booker
	fg, err := f.Guests.Create(roomstest.Admin(foreign.ID), fp.ID, guests.Profile{LastName: "Foreign"})
	must(t, err)
	in := f.input(false, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.GuestID = &fg.Guest.ID
	_, err = f.Res.Create(f.admin, f.propID, "", in)
	wantCode(t, err, "GUEST_NOT_FOUND")
}

func TestListFilters(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-10-02", "2026-10-04")
	b := f.book(t, f.std, "2026-10-08", "2026-10-09")
	_, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.line(f.dlx, "2026-10-12", "2026-10-13")))
	must(t, err)
	_, err = f.Res.Cancel(f.admin, f.propID, b.ID, b.Version, "x")
	must(t, err)
	all, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{}, 0, 10)
	must(t, err)
	if len(all) != 3 || all[0].ID < all[1].ID || all[0].Status != "DRAFT" || all[2].ArrivalDate != d("2026-10-02") || all[2].GuestName != "Guest" {
		t.Fatalf("all: %+v", all)
	}
	from, to := d("2026-10-03"), d("2026-10-10")
	mid, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{ArrivalFrom: &from, ArrivalTo: &to}, 0, 10)
	must(t, err)
	if len(mid) != 1 || mid[0].ID != b.ID || mid[0].Status != "CANCELLED" || mid[0].ArrivalDate != d("2026-10-08") {
		t.Fatalf("range (a cancelled reservation keeps its dates): %+v", mid)
	}
	conf, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{Status: "CONFIRMED"}, 0, 10)
	must(t, err)
	if len(conf) != 1 || conf[0].ID != a.ID {
		t.Fatalf("status: %+v", conf)
	}
	byNumber, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{Query: a.ConfirmationNumber}, 0, 10)
	must(t, err)
	byName, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{Query: "gues"}, 0, 10)
	must(t, err)
	if len(byNumber) != 1 || byNumber[0].ID != a.ID || len(byName) != 3 {
		t.Fatalf("q: %d %d", len(byNumber), len(byName))
	}
	page, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{}, all[0].ID, 10)
	must(t, err)
	if len(page) != 2 || page[0].ID != all[1].ID {
		t.Fatalf("keyset: %+v", page)
	}
}

func TestSearchAvailabilityAndFreeRooms(t *testing.T) {
	f := setup(t)
	f.book(t, f.std, "2026-10-02", "2026-10-03")
	// a second plan with an incomplete grid is shown with its missing nights
	var chargeID, half int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT room_charge_code_id FROM rate_plans WHERE id = $1`, f.plan).Scan(&chargeID))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'HALF', 'Half', $3) RETURNING id`, f.tenantID, f.propID, chargeID).Scan(&half))
	_, err := f.Rates.FillRates(f.admin, f.propID, rates.FillInput{RatePlanID: half, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-10-02"), To: d("2026-10-03"), Amount: "900000"})
	must(t, err)

	res, err := f.Res.SearchAvailability(f.admin, f.propID, d("2026-10-02"), d("2026-10-04"), 2, 1)
	must(t, err)
	if len(res.Nights) != 2 || len(res.RoomTypes) != 2 {
		t.Fatalf("shape: %+v", res)
	}
	var dlx, std reservations.TypeOffer
	for _, t := range res.RoomTypes {
		if t.Code == "DLX" {
			dlx = t
		} else {
			std = t
		}
	}
	if dlx.AvailableMin != 2 || !dlx.FitsOccupancy || len(dlx.PerNight) != 2 || std.AvailableMin != 0 || std.PerNight[0].Sellable != 1 || std.PerNight[0].Demand != 1 {
		t.Fatalf("availability: dlx %+v std %+v", dlx, std)
	}
	byCode := map[string]reservations.PlanOffer{}
	for _, p := range dlx.RatePlans {
		byCode[p.Code] = p
	}
	bar, halfPlan := byCode["BAR"], byCode["HALF"]
	if bar.MissingNights != 0 || bar.Estimate == nil || bar.Estimate.Total.IsZero() || len(bar.Nightly) != 2 || bar.Nightly[0].Amount.String() != "1000000" {
		t.Fatalf("BAR: %+v", bar)
	}
	if halfPlan.MissingNights != 1 || halfPlan.Estimate != nil || len(halfPlan.Nightly) != 1 {
		t.Fatalf("HALF: %+v", halfPlan)
	}
	// 3 adults + 1 child does not fit the standard type limits (2 adults, 1 child, 3 in total)
	big, err := f.Res.SearchAvailability(f.admin, f.propID, d("2026-10-02"), d("2026-10-04"), 3, 1)
	must(t, err)
	if big.RoomTypes[0].FitsOccupancy {
		t.Fatalf("occupancy: %+v", big.RoomTypes[0])
	}
	_, err = f.Res.SearchAvailability(f.admin, f.propID, d("2026-09-01"), d("2026-10-04"), 2, 0)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Res.SearchAvailability(f.admin, f.propID, d("2026-10-04"), d("2026-10-04"), 2, 0)
	wantCode(t, err, "VALIDATION_FAILED")

	free, err := f.Res.FreeRooms(f.admin, f.propID, f.dlx.ID, d("2026-10-02"), d("2026-10-04"))
	must(t, err)
	if len(free) != 2 || free[0].HousekeepingStatus == "" {
		t.Fatalf("free rooms: %+v", free)
	}
	_, err = f.Res.FreeRooms(f.admin, f.propID, 99999, d("2026-10-02"), d("2026-10-04"))
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")
}

func TestOrphanedEnumerationHelpers(t *testing.T) {
	// display status derivation
	cases := []struct {
		header string
		lines  []string
		want   string
	}{
		{"DRAFT", []string{"DRAFT"}, "DRAFT"},
		{"CANCELLED", []string{"CANCELLED"}, "CANCELLED"},
		{"CONFIRMED", []string{"CONFIRMED", "CANCELLED"}, "CONFIRMED"},
		{"CONFIRMED", []string{"CHECKED_IN", "CONFIRMED"}, "IN_HOUSE"},
		{"CONFIRMED", []string{"COMPLETED", "CANCELLED"}, "CHECKED_OUT"},
		{"CONFIRMED", []string{"NO_SHOW", "CANCELLED"}, "NO_SHOW"},
		{"CONFIRMED", []string{"COMPLETED", "NO_SHOW"}, "CHECKED_OUT"},
	}
	for _, c := range cases {
		if got := reservations.DisplayStatus(c.header, c.lines); got != c.want {
			t.Errorf("%s %v: got %s, want %s", c.header, c.lines, got, c.want)
		}
	}
	a := reservations.CreateInput{Source: "PHONE"}
	b := reservations.CreateInput{Source: "PHONE", Confirm: true}
	if a.Hash() == b.Hash() || a.Hash() != (reservations.CreateInput{Source: "PHONE"}).Hash() {
		t.Error("hash must depend on the body only")
	}
}

func TestTapeChart(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-10-02", "2026-10-05")
	_, err := f.Res.AssignRoom(f.admin, f.propID, a.ID, a.Rooms[0].ID, a.Version, f.r102.ID, false)
	must(t, err)
	f.book(t, f.dlx, "2026-10-03", "2026-10-04") // no room yet
	f.book(t, f.std, "2026-10-18", "2026-10-20") // outside the window
	_, err = f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.r101.ID, BlockType: "OOS", StartDate: d("2026-10-01"), EndDate: d("2026-10-03"), Reason: "paint"})
	must(t, err)

	tape, err := f.Res.TapeChart(f.admin, f.propID, d("2026-10-01"), d("2026-10-08"))
	must(t, err)
	if len(tape.Rooms) != 3 || tape.Rooms[0].RoomNumber != "101" || tape.Rooms[2].RoomTypeCode != "STD" {
		t.Fatalf("rows: %+v", tape.Rooms)
	}
	if len(tape.Rooms[0].Blocks) != 1 || tape.Rooms[0].Blocks[0].BlockType != "OOS" || len(tape.Rooms[0].Bookings) != 0 {
		t.Fatalf("room 101: %+v", tape.Rooms[0])
	}
	if b := tape.Rooms[1].Bookings; len(b) != 1 || b[0].ReservationID != a.ID || b[0].GuestName != "Guest" || b[0].Status != "CONFIRMED" {
		t.Fatalf("room 102: %+v", tape.Rooms[1].Bookings)
	}
	if len(tape.Rooms[2].Bookings) != 0 || len(tape.Unassigned) != 1 || tape.Unassigned[0].RoomTypeCode != "DLX" || len(tape.Unassigned[0].Bookings) != 1 {
		t.Fatalf("unassigned: %+v", tape)
	}
	_, err = f.Res.TapeChart(f.admin, f.propID, d("2026-10-01"), d("2026-12-31"))
	wantCode(t, err, "VALIDATION_FAILED")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Res.TapeChart(nobody, f.propID, d("2026-10-01"), d("2026-10-08"))
	wantCode(t, err, "PERMISSION_DENIED")
}
