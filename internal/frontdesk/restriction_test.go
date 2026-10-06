package frontdesk_test

import (
	"encoding/json"
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
)

// The sales restrictions at the front desk: a walk-in is a sale, the extra nights of a stay are a sale, and a room move, an early departure and a room change are not.

func bp(b bool) *bool { return &b }
func ip(n int) *int   { return &n }

func (f *fx) restrict(t *testing.T, roomType *int64, from, to string, set rates.RestrictionSet) {
	t.Helper()
	in := rates.FillRestrictionsInput{From: d(from), To: d(to), Set: set}
	if roomType != nil {
		in.RoomTypeIDs = []int64{*roomType}
	}
	if _, err := f.Rates.FillRestrictions(f.admin, f.propID, in); err != nil {
		t.Fatal(err)
	}
}

func violationTypes(t *testing.T, err error) []string {
	t.Helper()
	c := roomstestCode(t, err, "STAY_RESTRICTED")
	raw, _ := json.Marshal(c.Context["violations"])
	var vs []struct {
		Type string `json:"type"`
	}
	if e := json.Unmarshal(raw, &vs); e != nil {
		t.Fatal(e)
	}
	var out []string
	for _, v := range vs {
		out = append(out, v.Type)
	}
	return out
}

func TestAWalkInIsASaleAndIsAskedTheRestrictions(t *testing.T) {
	f := setup(t)
	// today (the business date, 2026-09-30) is closed to arrival for DLX
	f.restrict(t, &f.dlx.ID, "2026-09-30", "2026-10-01", rates.RestrictionSet{ClosedToArrival: bp(true)})
	in := frontdesk.WalkInInput{GuestID: &f.guest, RoomID: f.r101.ID, RatePlanID: f.plan, DepartureDate: d("2026-10-02"), AdultCount: 2}
	_, err := f.Front.WalkIn(f.admin, f.propID, "w1", in)
	if got := violationTypes(t, err); len(got) != 1 || got[0] != "CLOSED_TO_ARRIVAL" {
		t.Fatalf("violations: %v", got)
	}
	if n := f.Count(t, `SELECT count(*) FROM reservations`) + f.Count(t, `SELECT count(*) FROM stays`); n != 0 {
		t.Fatalf("a refused walk-in leaves nothing behind: %d", n)
	}
	// the room type that is not closed takes the walk-in
	in2 := frontdesk.WalkInInput{GuestID: &f.guest, RoomID: f.r201.ID, RatePlanID: f.plan, DepartureDate: d("2026-10-02"), AdultCount: 2}
	if _, err := f.Front.WalkIn(f.admin, f.propID, "w2", in2); err != nil {
		t.Fatalf("another room type: %v", err)
	}
	// with an override that the front desk may give, and a reason, it goes in and the audit says so
	in.RestrictionOverride = &reservations.RestrictionOverride{Reason: "regular guest, no booking"}
	out, err := f.Front.WalkIn(f.admin, f.propID, "w3", in)
	if err != nil || out.Stay.Status != "OPEN" {
		t.Fatalf("override: %v", err)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.created' AND new_data->'restriction_override'->>'reason' = 'regular guest, no booking'`); n != 1 {
		t.Fatalf("the audit entry names the override: %d", n)
	}
}

func TestExtendingAStayIsAskedTheRestrictionsOfTheNewNightsOnly(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-02") // in since the 30th, until the 2nd
	// the 2nd and 3rd nights are closed; the 5th is closed to departure; the arrival and the minimum stay of the 30th would break if they were asked
	f.restrict(t, &f.dlx.ID, "2026-09-30", "2026-10-01", rates.RestrictionSet{ClosedToArrival: bp(true), MinStay: ip(9)})
	f.restrict(t, nil, "2026-09-30", "2026-10-01", rates.RestrictionSet{MaxStay: ip(5)}) // the property: at most five nights
	f.restrict(t, &f.dlx.ID, "2026-10-02", "2026-10-04", rates.RestrictionSet{StopSell: bp(true)})
	f.restrict(t, &f.dlx.ID, "2026-10-05", "2026-10-06", rates.RestrictionSet{ClosedToDeparture: bp(true)})

	extend := func(to string, ro *reservations.RestrictionOverride, version int32) (frontdesk.Stay, error) {
		return f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: version, DepartureDate: d(to), RestrictionOverride: ro})
	}
	// to the 4th: two new nights, both closed
	_, err := extend("2026-10-04", nil, st.Stay.Version)
	if got := violationTypes(t, err); len(got) != 2 || got[0] != "STOP_SELL" || got[1] != "STOP_SELL" {
		t.Fatalf("the new nights: %v", got)
	}
	// to the 5th: the same two closed nights, and the 5th is closed to departure
	_, err = extend("2026-10-05", nil, st.Stay.Version)
	if got := violationTypes(t, err); len(got) != 3 || got[2] != "CLOSED_TO_DEPARTURE" {
		t.Fatalf("two closed nights and a closed departure: %v", got)
	}
	// overridden, the extension goes through, and the audit entry of the stay says who approved and why
	got, err := extend("2026-10-04", &reservations.RestrictionOverride{Reason: "guest asked to stay"}, st.Stay.Version)
	if err != nil {
		t.Fatalf("override: %v", err)
	}
	if got.DepartureDate != d("2026-10-04") {
		t.Fatalf("departure: %+v", got)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.departure_changed' AND new_data->'restriction_override'->>'reason' = 'guest asked to stay'`); n != 1 {
		t.Fatalf("audit: %d", n)
	}
	// the arrival of the 30th (closed) and its minimum (9) were not asked of an extension: only the maximum is, over the total length (6 nights to the 6th, maximum 5)
	_, err = extend("2026-10-06", nil, got.Version)
	types := violationTypes(t, err)
	want := map[string]bool{"STOP_SELL": false, "MAX_STAY": false}
	for _, x := range types {
		if _, ok := want[x]; !ok {
			t.Fatalf("a restriction that is not asked of an extension: %v", types)
		}
		want[x] = true
	}
	if !want["MAX_STAY"] {
		t.Fatalf("the maximum stay is asked of the total length: %v", types)
	}
}

func TestARoomMoveAShorterStayAndACheckOutAreNotSales(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	// everything is closed for both room types from tonight on
	f.restrict(t, nil, "2026-09-30", "2026-10-05", rates.RestrictionSet{StopSell: bp(true), ClosedToArrival: bp(true), ClosedToDeparture: bp(true), MinStay: ip(9)})
	f.clean(t, f.r102.ID)
	moved, err := f.Front.Move(f.admin, f.propID, st.Stay.ID, frontdesk.MoveInput{Version: st.Stay.Version, RoomID: f.r102.ID, Reason: "noise"})
	if err != nil {
		t.Fatalf("a room move is not a sale: %v", err)
	}
	// shortening an in-house stay is not asked either
	short, err := f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: moved.Stay.Version, DepartureDate: d("2026-10-01")})
	if err != nil {
		t.Fatalf("a shorter in-house stay: %v", err)
	}
	_ = short
	// a person who may not override gets the refusal and not a permission error when no override is asked
	clerk := f.User(t, f.tenantID, f.propID, auth.PermReservationUpdate)
	_, err = f.Front.ChangeDeparture(clerk, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: short.Version, DepartureDate: d("2026-10-04")})
	if got := violationTypes(t, err); len(got) == 0 {
		t.Fatal("extending into closed nights is refused")
	}
}
