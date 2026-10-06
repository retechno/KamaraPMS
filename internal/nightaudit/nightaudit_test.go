package nightaudit_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/nightaudit"
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

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	dlx, std         rooms.RoomType
	r101, r102, r201 rooms.Room
	plan, guest      int64
}

// setup: a property (IDR, no decimals) on 30 Sep 2026 20:00 local (the night audit is allowed), three rooms
// (101 CLEAN and 102 CLEAN of DLX, 201 CLEAN of STD) and a rate grid for DLX (1,000,000) and STD (500,000).
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, _ := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin}
	f.dlx, f.std = e.RoomType(t, admin, p.ID, "DLX"), e.RoomType(t, admin, p.ID, "STD")
	f.r101 = e.Room(t, admin, p.ID, f.dlx.ID, "101", housekeeping.Clean)
	f.r102 = e.Room(t, admin, p.ID, f.dlx.ID, "102", housekeeping.Clean)
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

func (f *fx) book(t *testing.T, typ rooms.RoomType, arrival, departure string) reservations.Reservation {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: typ.ID, RatePlanID: f.plan, Arrival: d(arrival), Departure: d(departure), Adults: 2},
	}})
	must(t, err)
	return res
}

// stay books and checks in on the business date.
func (f *fx) stay(t *testing.T, room rooms.Room, departure string) frontdesk.CheckInResult {
	t.Helper()
	res := f.book(t, f.dlx, "2026-09-30", departure)
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	return out
}

func (f *fx) run(t *testing.T) (nightaudit.RunResult, error) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	return f.Audit.Run(f.admin, f.propID, day.BusinessDate)
}

func (f *fx) bd(t *testing.T) civil.Date {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	return day.BusinessDate
}

func (f *fx) charges(t *testing.T) int {
	return f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`)
}

func (f *fx) advanceWithoutPosting(t *testing.T) {
	t.Helper()
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, f.bd(t), nil, json.RawMessage(`{}`))
		return err
	}))
}

func TestPreviewListsEveryBlockerAndWarning(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-01")                    // leaves on the next business date
	f.advanceWithoutPosting(t)                               // the audit was skipped: the stay is due out today and its night was never charged
	arriving := f.book(t, f.dlx, "2026-10-01", "2026-10-03") // never checked in: an unresolved arrival
	f.book(t, f.std, "2026-10-05", "2026-10-06")
	must(t, f.Exec(t, `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, 'DRAFTRES', '2026-09-29', 'PHONE', 'DRAFT')`, f.tenantID, f.propID))
	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if pv.BusinessDate != d("2026-10-01") || pv.TimeGuardOK || pv.CanRun {
		t.Fatalf("preview: %+v", pv)
	}
	b := pv.Blockers
	if len(b.UnresolvedArrivals) != 1 || b.UnresolvedArrivals[0].ConfirmationNumber != arriving.ConfirmationNumber || b.UnresolvedArrivals[0].Guest != "Guest" || b.UnresolvedArrivals[0].RoomType != "DLX" {
		t.Fatalf("arrivals: %+v", b.UnresolvedArrivals)
	}
	if len(b.UnresolvedDepartures) != 1 || b.UnresolvedDepartures[0].StayID != st.Stay.ID || b.UnresolvedDepartures[0].Room != "101" {
		t.Fatalf("departures: %+v", b.UnresolvedDepartures)
	}
	if len(b.ChargeErrors) != 0 || len(b.InvalidCharges) != 0 || pv.TonightCharges.Count != 0 || pv.MissingCharges.Count != 1 {
		// the stay leaves today (departure = BD): tonight is not one of its nights, 30 Sep is missing
		t.Fatalf("charges: %+v %+v %+v", b, pv.TonightCharges, pv.MissingCharges)
	}
	if b.ChargeErrors == nil || b.InvalidCharges == nil || pv.Warnings.BlocksEnding == nil {
		t.Fatalf("empty lists are arrays: %+v", pv)
	}
	if f.charges(t) != 0 || f.bd(t) != d("2026-10-01") {
		t.Fatal("a preview writes nothing")
	}
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err = f.Audit.Preview(clerk, f.propID)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestPreviewTonightMissingAndWarnings(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if !pv.CanRun || pv.TonightCharges.Count != 1 || pv.TonightCharges.Total != "1000000" || pv.MissingCharges.Count != 0 {
		t.Fatalf("tonight: %+v", pv)
	}
	f.advanceWithoutPosting(t) // the audit was skipped: 30 Sep was never charged
	pv, err = f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if pv.BusinessDate != d("2026-10-01") || pv.MissingCharges.Count != 1 || pv.MissingCharges.Items[0].ServiceDate != d("2026-09-30") || pv.TonightCharges.Count != 1 {
		t.Fatalf("missing: %+v", pv)
	}
	if pv.TimeGuardOK {
		t.Fatal("30 Sep 20:00 local is before the earliest audit time of 1 Oct")
	}
	// warnings
	must(t, f.Exec(t, `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, 'DEAD1', '2026-09-29', 'PHONE', 'DRAFT')`, f.tenantID, f.propID))
	must(t, f.Exec(t, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) SELECT $1, $2, 'FDEAD', id FROM reservations WHERE confirmation_number = 'DEAD1'`, f.tenantID, f.propID))
	_, err = f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.r201.ID, BlockType: "OOS", StartDate: d("2026-10-01").AddDays(-1), EndDate: d("2026-10-01"), Reason: "paint"})
	_ = err
	pv, err = f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if len(pv.Warnings.BlocksEnding) > 1 || pv.Warnings.StaleDrafts == nil || pv.Warnings.OpenFolios == nil {
		t.Fatalf("warnings: %+v", pv.Warnings)
	}
}

func TestBulkNoShowIsExactlyTheStaffSelection(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	b := f.book(t, f.std, "2026-09-30", "2026-10-01")
	c := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	ids := []int64{a.Rooms[0].ID, b.Rooms[0].ID}
	bd := roomstest.BD
	_, err := f.Audit.NoShows(f.admin, f.propID, bd, ids, false, "x")
	wantCode(t, err, "VALIDATION_FAILED") // confirm
	_, err = f.Audit.NoShows(f.admin, f.propID, bd, nil, true, "x")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Audit.NoShows(f.admin, f.propID, bd.AddDays(1), ids, true, "x")
	wantCode(t, err, "BUSINESS_DATE_MISMATCH")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermNightAuditRun)
	_, err = f.Audit.NoShows(clerk, f.propID, bd, ids, true, "x")
	wantCode(t, err, "PERMISSION_DENIED")

	// one of the lines changed since the staff looked: nothing is marked, the changed ones are listed
	_, err = f.Front.CheckIn(f.admin, f.propID, b.ID, b.Rooms[0].ID, "", frontdesk.CheckInInput{Version: b.Version, RoomID: &f.r201.ID, GuestID: f.guest, AdultCount: 1})
	must(t, err)
	_, err = f.Audit.NoShows(f.admin, f.propID, bd, ids, true, "x")
	e := code(t, err, "NO_SHOW_SET_CHANGED")
	if e.Context["changed"] == nil || f.Count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'NO_SHOW'`) != 0 {
		t.Fatalf("context %v", e.Context)
	}
	_, err = f.Audit.NoShows(f.admin, f.propID, bd, []int64{a.Rooms[0].ID, 99999}, true, "x")
	wantCode(t, err, "NO_SHOW_SET_CHANGED")

	// the exact set is marked; the other arrival stays a blocker (the server never expands the set)
	res, err := f.Audit.NoShows(f.admin, f.propID, bd, []int64{a.Rooms[0].ID}, true, "did not come")
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'NO_SHOW'`) != 1 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.no_show'`) != 1 {
		t.Fatal("exactly the selected line, audited")
	}
	if len(res.RemainingBlockers.UnresolvedArrivals) != 1 || res.RemainingBlockers.UnresolvedArrivals[0].ReservationRoomID != c.Rooms[0].ID {
		t.Fatalf("remaining: %+v", res.RemainingBlockers)
	}
	after, err := f.Res.Get(f.admin, f.propID, a.ID)
	must(t, err)
	if after.Rooms[0].Status != "NO_SHOW" || after.Version != a.Version+1 {
		t.Fatalf("reservation: %+v", after)
	}
	// a no-show releases the inventory
	inv, err := f.Avail.Inventory(f.admin, f.tenantID, f.propID, []int64{f.dlx.ID}, []civil.Date{d("2026-09-30")}, bd, nil)
	must(t, err)
	if inv[f.dlx.ID][d("2026-09-30")].Demand != 1 {
		t.Fatalf("demand: %+v", inv[f.dlx.ID][d("2026-09-30")])
	}
	_, err = f.Audit.NoShows(f.admin, f.propID, bd, []int64{a.Rooms[0].ID}, true, "again")
	wantCode(t, err, "NO_SHOW_SET_CHANGED")
}

func TestRunClosesTheDayAndWritesTheSummary(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.stay(t, f.r102, "2026-10-02")
	_, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	if err := f.Exec(t, `UPDATE properties SET night_audit_marks_occupied_dirty = true WHERE id = $1`, f.propID); err != nil {
		t.Fatal(err)
	}
	f.Clock.Set(roomstest.T0.Add(4 * time.Hour)) // 00:00 on 1 Oct local: past midnight
	res, err := f.run(t)
	must(t, err)
	if res.ClosedBusinessDate != roomstest.BD || res.NewBusinessDate != d("2026-10-01") || res.RoomChargesPosted != 2 {
		t.Fatalf("run: %+v", res)
	}
	if f.bd(t) != d("2026-10-01") || f.charges(t) != 2 {
		t.Fatalf("bd %s charges %d", f.bd(t), f.charges(t))
	}
	s := res.Summary
	if s.Rooms.Total != 3 || s.Rooms.Occupied != 2 || s.Rooms.Sold != 2 || s.Rooms.Sellable != 3 || s.Arrivals != 2 || s.Departures != 0 || s.NoShows != 0 {
		t.Fatalf("summary: %+v", s)
	}
	if s.RoomRevenue.Net != "2000000" || s.OccupancyPercent != "66.67" || s.ADR != "1000000" || s.RevPAR != "666667" || s.RoomChargesPosted != 2 {
		t.Fatalf("summary figures: %+v", s)
	}
	if len(s.PaymentsByMethod) != 1 || s.PaymentsByMethod[0].Method != "CASH" || s.PaymentsByMethod[0].Net != "300000" || len(s.RevenueByChargeType) != 1 || s.RevenueByChargeType[0].ChargeType != "ROOM" {
		t.Fatalf("by method / type: %+v %+v", s.PaymentsByMethod, s.RevenueByChargeType)
	}
	// stored on the closed day, immutable
	var stored string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT summary->>'adr' FROM business_days WHERE property_id = $1 AND business_date = '2026-09-30'`, f.propID).Scan(&stored))
	if stored != "1000000" || f.Count(t, `SELECT count(*) FROM business_days WHERE property_id = $1 AND status = 'OPEN'`, f.propID) != 1 {
		t.Fatalf("stored summary %q", stored)
	}
	// housekeeping: the occupied rooms are DIRTY, from the night audit
	for _, r := range []rooms.Room{f.r101, f.r102} {
		var st string
		must(t, f.Pool.QueryRow(context.Background(), `SELECT status FROM room_housekeeping WHERE room_id = $1`, r.ID).Scan(&st))
		if st != "DIRTY" {
			t.Fatalf("room %s is %s", r.RoomNumber, st)
		}
	}
	if f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE source = 'NIGHT_AUDIT'`) != 2 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'night_audit.completed'`) != 1 {
		t.Fatal("housekeeping logs and the audit entry")
	}
	if f.Count(t, `SELECT count(*) FROM stays WHERE status <> 'OPEN'`) != 0 {
		t.Fatal("night audit never changes a stay")
	}
	// the day cannot be closed again: the same date, or the next one before its end of day
	_, err = f.Audit.Run(f.admin, f.propID, roomstest.BD)
	wantCode(t, err, "BUSINESS_DATE_MISMATCH")
	_, err = f.run(t)
	wantCode(t, err, "NIGHT_AUDIT_TOO_EARLY")
	// a second audit one day later posts one more night per stay and moves the date by exactly one
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour))
	res, err = f.run(t)
	must(t, err)
	if res.NewBusinessDate != d("2026-10-02") || res.RoomChargesPosted != 2 || res.Summary.Arrivals != 0 || f.charges(t) != 4 {
		t.Fatalf("second run: %+v", res)
	}
}

func TestRunWithoutTheHousekeepingOption(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-02")
	must(t, f.Exec(t, `UPDATE properties SET night_audit_marks_occupied_dirty = false WHERE id = $1`, f.propID))
	_, err := f.run(t)
	must(t, err)
	var st string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT status FROM room_housekeeping WHERE room_id = $1`, f.r101.ID).Scan(&st))
	if st != "CLEAN" || f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE source = 'NIGHT_AUDIT'`) != 0 {
		t.Fatalf("housekeeping untouched, got %s", st)
	}
}

func TestRunIsBlockedAndRollsBackEverything(t *testing.T) {
	f := setup(t)
	good := f.stay(t, f.r101, "2026-10-03")
	broken := f.stay(t, f.r102, "2026-10-03")
	_ = good
	must(t, f.Exec(t, `DELETE FROM reservation_room_rates WHERE reservation_room_id = $1`, broken.Stay.ReservationRoomID))
	f.book(t, f.std, "2026-09-30", "2026-10-01") // an unresolved arrival too
	must(t, f.Exec(t, `UPDATE properties SET night_audit_marks_occupied_dirty = true WHERE id = $1`, f.propID))
	_, err := f.run(t)
	e := code(t, err, "NIGHT_AUDIT_BLOCKED")
	b, ok := e.Context["blockers"].(nightaudit.Blockers)
	if !ok || len(b.UnresolvedArrivals) != 1 || len(b.ChargeErrors) != 1 || b.ChargeErrors[0].StayID != broken.Stay.ID {
		t.Fatalf("blockers: %+v", e.Context)
	}
	if f.charges(t) != 0 || f.bd(t) != roomstest.BD || f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE source = 'NIGHT_AUDIT'`) != 0 || f.Count(t, `SELECT count(*) FROM stay_charge_postings`) != 0 {
		t.Fatal("a blocked run leaves nothing behind")
	}
	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if pv.CanRun || len(pv.Blockers.ChargeErrors) != 1 {
		t.Fatalf("preview: %+v", pv.Blockers)
	}
}

func TestRunAfterTheBlockersAreResolved(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	a := f.book(t, f.std, "2026-09-30", "2026-10-01")
	_, err := f.run(t)
	wantCode(t, err, "NIGHT_AUDIT_BLOCKED")
	_, err = f.Audit.NoShows(f.admin, f.propID, roomstest.BD, []int64{a.Rooms[0].ID}, true, "no show")
	must(t, err)
	res, err := f.run(t)
	must(t, err)
	if res.Summary.NoShows != 1 || res.NewBusinessDate != d("2026-10-01") {
		t.Fatalf("run: %+v", res)
	}
}

func TestRunNeedsTheEndOfTheBusinessDay(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.Clock.Set(roomstest.T0.Add(-2 * time.Hour)) // 18:00, before the earliest audit time of 20:00
	_, err := f.run(t)
	e := code(t, err, "NIGHT_AUDIT_TOO_EARLY")
	if e.Context["allowed_from"] == nil || f.charges(t) != 0 || f.bd(t) != roomstest.BD {
		t.Fatalf("context %v", e.Context)
	}
	f.Clock.Set(roomstest.T0)
	if _, err := f.run(t); err != nil {
		t.Fatalf("20:00 sharp: %v", err)
	}
}

func TestRunPostsTheNightsASkippedAuditLeftBehind(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-04")
	f.advanceWithoutPosting(t)
	f.Clock.Set(roomstest.T0.Add(24 * time.Hour))
	res, err := f.run(t)
	must(t, err)
	if res.RoomChargesPosted != 2 || f.charges(t) != 2 || res.NewBusinessDate != d("2026-10-02") {
		t.Fatalf("catch-up: %+v", res)
	}
	var trig string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT DISTINCT posting_trigger FROM stay_charge_postings`).Scan(&trig))
	if trig != "NIGHT_AUDIT" {
		t.Fatalf("trigger %s", trig)
	}
}

func TestRunPermission(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.Audit.Run(clerk, f.propID, roomstest.BD)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Audit.Run(f.admin, f.propID, d("2026-10-01"))
	wantCode(t, err, "BUSINESS_DATE_MISMATCH")
}

func TestConcurrentRunsOneWins(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.stay(t, f.r102, "2026-10-03")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Audit.Run(f.admin, f.propID, roomstest.BD)
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case apperr.IsCode(err, "NIGHT_AUDIT_IN_PROGRESS"), apperr.IsCode(err, "BUSINESS_DATE_MISMATCH"):
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if wins != 1 || f.bd(t) != d("2026-10-01") || f.charges(t) != 2 || f.Count(t, `SELECT count(*) FROM business_days WHERE property_id = $1`, f.propID) != 2 {
		t.Fatalf("wins %d, bd %s, charges %d", wins, f.bd(t), f.charges(t))
	}
}

func TestWritersWaitWhileTheAuditHoldsTheDay(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	// a payment racing the audit lands on the closed day or the new one, never in between
	st := f.Count(t, `SELECT id FROM folios LIMIT 1`)
	var wg sync.WaitGroup
	var payErr, runErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, runErr = f.Audit.Run(f.admin, f.propID, roomstest.BD)
	}()
	go func() {
		defer wg.Done()
		_, payErr = f.Folios.PostPayment(f.admin, f.propID, int64(st), "race", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	}()
	wg.Wait()
	must(t, runErr)
	must(t, payErr)
	n := f.Count(t, `SELECT count(*) FROM payments WHERE business_date = '2026-09-30'`) + f.Count(t, `SELECT count(*) FROM payments WHERE business_date = '2026-10-01'`)
	if n != 1 {
		t.Fatalf("the payment belongs to exactly one business day, got %d", n)
	}
}

func TestSummaryCarriesTheCityLedgerAndKeepsTransfersOutOfTheTill(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	var minibar int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, f.propID).Scan(&minibar))
	unit := "500000"
	_, err := f.Folios.PostCharge(f.admin, f.propID, st.Folio.ID, "c1", folios.ChargeInput{ChargeCodeID: minibar, Quantity: "1", UnitPrice: &unit})
	must(t, err)
	co, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "ACME", Name: "Acme Corp", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	_, err = f.Folios.Transfer(f.admin, f.propID, st.Folio.ID, "t1", folios.TransferInput{CompanyID: co.ID, Amount: "200000"})
	must(t, err)
	_, err = f.CityLedger.Receive(f.admin, f.propID, co.ID, "r1", cityledger.ReceiptInput{Amount: "50000", PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	f.Clock.Set(roomstest.T0.Add(4 * time.Hour))
	res, err := f.run(t)
	must(t, err)
	s := res.Summary
	if s.CityLedger.Transferred != "200000" || s.CityLedger.Received != "50000" || s.CityLedger.Outstanding != "150000" {
		t.Fatalf("city ledger: %+v", s.CityLedger)
	}
	// the till holds the receipt, not the transfer
	if len(s.PaymentsByMethod) != 1 || s.PaymentsByMethod[0].Method != "BANK_TRANSFER" || s.PaymentsByMethod[0].Net != "50000" {
		t.Fatalf("by method: %+v", s.PaymentsByMethod)
	}
}

// A night routed to a company folio that has been closed blocks the night audit, and the guest folio is not charged instead.
func TestAClosedCompanyFolioBlocksTheNightAudit(t *testing.T) {
	f := setup(t)
	var company int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme', 100000000) RETURNING id`, f.tenantID, f.propID).Scan(&company))
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-03")
	_, err := f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: company}})
	must(t, err)
	st, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &f.r101.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	must(t, f.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE stay_id = $1 AND folio_type = 'COMPANY'`, st.Stay.ID))

	_, err = f.run(t)
	e := code(t, err, "NIGHT_AUDIT_BLOCKED")
	b, ok := e.Context["blockers"].(nightaudit.Blockers)
	if !ok || len(b.ChargeErrors) != 1 || b.ChargeErrors[0].Reason != "ROUTING_TARGET_CLOSED" || b.ChargeErrors[0].StayID != st.Stay.ID {
		t.Fatalf("blockers: %+v", e.Context)
	}
	if f.charges(t) != 0 || f.bd(t) != roomstest.BD {
		t.Fatal("a blocked run leaves nothing behind, and nothing is charged to the guest folio")
	}
}
