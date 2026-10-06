package availability_test

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"kamarapms/internal/availability"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// The pure part of the sales restrictions: the precedence of the grid (Resolve) and the five checks of a stay (Check).
// The database and EvaluateStay are in restrictions_db_test.go.

func bp(b bool) *bool    { return &b }
func ip(n int) *int      { return &n }
func i64(n int64) *int64 { return &n }

const (
	typeT int64 = 10 // the room type asked for
	typeU int64 = 11 // another room type
	planP int64 = 20 // the rate plan asked for
	planQ int64 = 21 // another rate plan
)

// row builds a grid row; set fills the attributes.
func row(id int64, roomType, ratePlan *int64, date string, set func(*availability.Restriction)) availability.Restriction {
	r := availability.Restriction{ID: id, RoomTypeID: roomType, RatePlanID: ratePlan, Date: d(date)}
	set(&r)
	return r
}

func TestEachAttributeTakesTheValueOfTheMostSpecificRowThatHasOne(t *testing.T) {
	rows := []availability.Restriction{
		row(1, nil, nil, "2026-10-05", func(r *availability.Restriction) { r.StopSell, r.MinStay = bp(true), ip(2) }), // the whole property
		row(2, nil, i64(planP), "2026-10-05", func(r *availability.Restriction) { r.MinStay = ip(3) }),                // a plan
		row(3, i64(typeT), nil, "2026-10-05", func(r *availability.Restriction) { r.StopSell = bp(false) }),           // a room type
		row(4, i64(typeT), i64(planP), "2026-10-05", func(r *availability.Restriction) { r.MaxStay = ip(5) }),         // both
		row(5, i64(typeU), nil, "2026-10-05", func(r *availability.Restriction) { r.ClosedToArrival = bp(true) }),     // another room type
		row(6, nil, i64(planQ), "2026-10-05", func(r *availability.Restriction) { r.ClosedToDeparture = bp(true) }),   // another plan
		row(7, nil, nil, "2026-10-06", func(r *availability.Restriction) { r.ClosedToArrival = bp(true) }),            // another date
	}
	e := availability.Resolve(rows, typeT, planP, d("2026-10-05"))
	if e.StopSell.Value || e.StopSell.Row != 3 || e.StopSell.Scope != availability.ScopeRoomType {
		t.Fatalf("the room type row opens what the property closed: %+v", e.StopSell)
	}
	if e.MinStay.Row != 2 || *e.MinStay.Value != 3 || e.MinStay.Scope != availability.ScopeRatePlan {
		t.Fatalf("the plan row beats the property row: %+v", e.MinStay)
	}
	if e.MaxStay.Row != 4 || *e.MaxStay.Value != 5 || e.MaxStay.Scope != availability.ScopeRoomTypeAndPlan {
		t.Fatalf("the row for both: %+v", e.MaxStay)
	}
	if e.ClosedToArrival.Row != 0 || e.ClosedToDeparture.Row != 0 {
		t.Fatalf("rows of another room type, plan or date do not speak: %+v %+v", e.ClosedToArrival, e.ClosedToDeparture)
	}
	// nothing at all: every attribute is open
	if e := availability.Resolve(nil, typeT, planP, d("2026-10-05")); e.StopSell.Row != 0 || e.MinStay.Row != 0 || e.MinStay.Value != nil {
		t.Fatalf("open: %+v", e)
	}
}

func TestTheRoomTypeWinsOverTheRatePlanWhenBothGiveAValue(t *testing.T) {
	rows := []availability.Restriction{
		row(1, i64(typeT), nil, "2026-10-05", func(r *availability.Restriction) { r.MinStay = ip(4) }),
		row(2, nil, i64(planP), "2026-10-05", func(r *availability.Restriction) { r.MinStay = ip(2) }),
	}
	e := availability.Resolve(rows, typeT, planP, d("2026-10-05"))
	if e.MinStay.Row != 1 || *e.MinStay.Value != 4 {
		t.Fatalf("the room type is the tie-break of the two middle scopes: %+v", e.MinStay)
	}
	// and the order of the rows does not matter
	rows[0], rows[1] = rows[1], rows[0]
	if e := availability.Resolve(rows, typeT, planP, d("2026-10-05")); e.MinStay.Row != 1 {
		t.Fatalf("order: %+v", e.MinStay)
	}
}

func TestAnExplicitFalseOpensOnePlanInsideAClosedRoomType(t *testing.T) {
	rows := []availability.Restriction{
		row(1, i64(typeT), nil, "2026-10-05", func(r *availability.Restriction) { r.StopSell = bp(true) }),
		row(2, i64(typeT), i64(planP), "2026-10-05", func(r *availability.Restriction) { r.StopSell = bp(false) }),
	}
	if e := availability.Resolve(rows, typeT, planP, d("2026-10-05")); e.StopSell.Value {
		t.Fatalf("the plan with its own FALSE is open: %+v", e.StopSell)
	}
	if e := availability.Resolve(rows, typeT, planQ, d("2026-10-05")); !e.StopSell.Value || e.StopSell.Row != 1 {
		t.Fatalf("another plan stays closed: %+v", e.StopSell)
	}
}

func TestTwoRowsOfOneScopeDecideByTheLowerIdWhateverTheOrder(t *testing.T) {
	a := row(7, i64(typeT), nil, "2026-10-05", func(r *availability.Restriction) { r.MinStay = ip(3) })
	b := row(4, i64(typeT), nil, "2026-10-05", func(r *availability.Restriction) { r.MinStay = ip(6) })
	for _, rows := range [][]availability.Restriction{{a, b}, {b, a}} {
		if e := availability.Resolve(rows, typeT, planP, d("2026-10-05")); e.MinStay.Row != 4 || *e.MinStay.Value != 6 {
			t.Fatalf("the table forbids two rows of a scope, but the answer must not depend on the order: %+v", e.MinStay)
		}
	}
}

// resolveByScopes is a second way to the same answer: look at the scopes one by one, most specific first, and take the first row with a value (lowest id on a tie).
func resolveByScopes(rows []availability.Restriction, roomType, ratePlan int64, date civil.Date, has func(availability.Restriction) bool) int64 {
	for _, sc := range []availability.Scope{availability.ScopeRoomTypeAndPlan, availability.ScopeRoomType, availability.ScopeRatePlan, availability.ScopeProperty} {
		var best int64
		for _, r := range rows {
			if availability.ScopeOf(r) != sc || !r.Date.Equal(date) || !has(r) {
				continue
			}
			if (r.RoomTypeID != nil && *r.RoomTypeID != roomType) || (r.RatePlanID != nil && *r.RatePlanID != ratePlan) {
				continue
			}
			if best == 0 || r.ID < best {
				best = r.ID
			}
		}
		if best != 0 {
			return best
		}
	}
	return 0
}

func TestPrecedenceMatchesAnIndependentReadingOnRandomGrids(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	types, plans := []int64{typeT, typeU}, []int64{planP, planQ}
	for n := 0; n < 3000; n++ {
		var rows []availability.Restriction
		for id := int64(1); id <= int64(1+rng.Intn(10)); id++ {
			r := availability.Restriction{ID: id, Date: d("2026-10-05")}
			if rng.Intn(3) > 0 {
				r.RoomTypeID = i64(types[rng.Intn(2)])
			}
			if rng.Intn(3) > 0 {
				r.RatePlanID = i64(plans[rng.Intn(2)])
			}
			if rng.Intn(2) == 0 {
				r.Date = d("2026-10-06")
			}
			if rng.Intn(2) == 0 {
				r.StopSell = bp(rng.Intn(2) == 0)
			}
			if rng.Intn(2) == 0 {
				r.MinStay = ip(1 + rng.Intn(5))
			}
			rows = append(rows, r)
		}
		rt, rp := types[rng.Intn(2)], plans[rng.Intn(2)]
		want := availability.Resolve(rows, rt, rp, d("2026-10-05"))
		if got := resolveByScopes(rows, rt, rp, d("2026-10-05"), func(r availability.Restriction) bool { return r.StopSell != nil }); got != want.StopSell.Row {
			t.Fatalf("stop sell: row %d, the reading says %d (%+v)", want.StopSell.Row, got, rows)
		}
		if got := resolveByScopes(rows, rt, rp, d("2026-10-05"), func(r availability.Restriction) bool { return r.MinStay != nil }); got != want.MinStay.Row {
			t.Fatalf("min stay: row %d, the reading says %d (%+v)", want.MinStay.Row, got, rows)
		}
		rng.Shuffle(len(rows), func(i, j int) { rows[i], rows[j] = rows[j], rows[i] })
		if got := availability.Resolve(rows, rt, rp, d("2026-10-05")); !reflect.DeepEqual(got, want) {
			t.Fatalf("the order of the rows changed the answer: %+v / %+v", got, want)
		}
	}
}

// stay is a request for typeT and planP on 2026-10-01 (the business date), a new sale unless the test says otherwise.
func stay(arrival, departure string) availability.StayRequest {
	return availability.StayRequest{RoomTypeID: typeT, RatePlanID: planP, Arrival: d(arrival), Departure: d(departure), BusinessDate: d("2026-10-01")}
}

func kinds(vs []availability.Violation) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = string(v.Type) + "@" + v.Date.String()
	}
	return out
}

func wantKinds(t *testing.T, got []availability.Violation, want ...string) {
	t.Helper()
	g := kinds(got)
	if len(g) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(g, want) {
		t.Fatalf("violations %v, want %v", g, want)
	}
}

func TestStopSellIsAskedOfEveryNightButTheDepartureDate(t *testing.T) {
	rows := []availability.Restriction{
		row(1, i64(typeT), nil, "2026-10-04", func(r *availability.Restriction) { r.StopSell = bp(true) }),
		row(2, i64(typeT), nil, "2026-10-05", func(r *availability.Restriction) { r.StopSell = bp(true) }),
		row(3, i64(typeT), nil, "2026-10-06", func(r *availability.Restriction) { r.StopSell = bp(true) }), // the departure date: not a night of the stay
	}
	wantKinds(t, availability.Check(rows, stay("2026-10-03", "2026-10-06")), "STOP_SELL@2026-10-04", "STOP_SELL@2026-10-05")
	wantKinds(t, availability.Check(rows[2:], stay("2026-10-04", "2026-10-06"))) // only the departure date is closed: no night is
	// a night before the business date is past: not asked
	past := stay("2026-09-29", "2026-10-02")
	past.BusinessDate = d("2026-10-03")
	wantKinds(t, availability.Check([]availability.Restriction{row(1, nil, nil, "2026-09-30", func(r *availability.Restriction) { r.StopSell = bp(true) })}, past))
	v := availability.Check(rows, stay("2026-10-04", "2026-10-05"))[0]
	if v.Scope != "ROOM_TYPE" || v.RowID != 1 || v.RoomTypeID != typeT || v.RatePlanID != planP || v.Type != availability.StopSell {
		t.Fatalf("the violation says where it came from: %+v", v)
	}
}

func TestClosedToArrivalIsAskedOfTheArrivalDateAndClosedToDepartureOfTheDepartureDate(t *testing.T) {
	rows := []availability.Restriction{
		row(1, nil, nil, "2026-10-03", func(r *availability.Restriction) { r.ClosedToArrival, r.ClosedToDeparture = bp(true), bp(true) }),
		row(2, nil, nil, "2026-10-05", func(r *availability.Restriction) { r.ClosedToArrival, r.ClosedToDeparture = bp(true), bp(true) }),
	}
	// arrives on the 3rd (closed to arrival) and leaves on the 5th (closed to departure): one of each, and the departure restriction of the 3rd and the arrival one of the 5th are not asked
	wantKinds(t, availability.Check(rows, stay("2026-10-03", "2026-10-05")), "CLOSED_TO_ARRIVAL@2026-10-03", "CLOSED_TO_DEPARTURE@2026-10-05")
	wantKinds(t, availability.Check(rows, stay("2026-10-04", "2026-10-06")))
	// the room type that is not asked for does not matter
	other := []availability.Restriction{row(1, i64(typeU), nil, "2026-10-03", func(r *availability.Restriction) { r.ClosedToArrival = bp(true) })}
	wantKinds(t, availability.Check(other, stay("2026-10-03", "2026-10-05")))
}

func TestMinimumAndMaximumStayAreKeyedByTheArrivalDate(t *testing.T) {
	rows := []availability.Restriction{
		row(1, i64(typeT), nil, "2026-10-03", func(r *availability.Restriction) { r.MinStay, r.MaxStay = ip(3), ip(4) }),
		row(2, i64(typeT), nil, "2026-10-04", func(r *availability.Restriction) { r.MinStay = ip(9) }), // another arrival date: not asked
	}
	got := availability.Check(rows, stay("2026-10-03", "2026-10-05")) // 2 nights, minimum 3
	wantKinds(t, got, "MIN_STAY@2026-10-03")
	if got[0].Value == nil || *got[0].Value != 3 || got[0].Nights != 2 {
		t.Fatalf("the limit and the length are reported: %+v", got[0])
	}
	wantKinds(t, availability.Check(rows, stay("2026-10-03", "2026-10-06"))) // 3 nights: exactly the minimum
	wantKinds(t, availability.Check(rows, stay("2026-10-03", "2026-10-07"))) // 4 nights: exactly the maximum
	got = availability.Check(rows, stay("2026-10-03", "2026-10-08"))         // 5 nights, maximum 4
	wantKinds(t, got, "MAX_STAY@2026-10-03")
	if *got[0].Value != 4 || got[0].Nights != 5 {
		t.Fatalf("%+v", got[0])
	}
	// a minimum of the plan is lifted by a row of the room type and the plan that says 1
	lifted := append(rows, row(3, i64(typeT), i64(planP), "2026-10-03", func(r *availability.Restriction) { r.MinStay = ip(1) }))
	wantKinds(t, availability.Check(lifted, stay("2026-10-03", "2026-10-05")))
}

func TestAMinimumAboveTheMaximumLeavesNoLengthThatPasses(t *testing.T) {
	rows := []availability.Restriction{
		row(1, i64(typeT), nil, "2026-10-03", func(r *availability.Restriction) { r.MinStay = ip(5) }),
		row(2, nil, i64(planP), "2026-10-03", func(r *availability.Restriction) { r.MaxStay = ip(3) }),
	}
	wantKinds(t, availability.Check(rows, stay("2026-10-03", "2026-10-07")), "MIN_STAY@2026-10-03", "MAX_STAY@2026-10-03") // 4 nights
}

func TestViolationsComeInAStableOrder(t *testing.T) {
	rows := []availability.Restriction{
		row(1, nil, nil, "2026-10-03", func(r *availability.Restriction) {
			r.StopSell, r.ClosedToArrival, r.MinStay, r.MaxStay = bp(true), bp(true), ip(9), ip(1)
		}),
		row(2, nil, nil, "2026-10-04", func(r *availability.Restriction) { r.StopSell = bp(true) }),
		row(3, nil, nil, "2026-10-05", func(r *availability.Restriction) { r.ClosedToDeparture = bp(true) }),
	}
	wantKinds(t, availability.Check(rows, stay("2026-10-03", "2026-10-05")),
		"STOP_SELL@2026-10-03", "STOP_SELL@2026-10-04", "CLOSED_TO_ARRIVAL@2026-10-03", "CLOSED_TO_DEPARTURE@2026-10-05", "MIN_STAY@2026-10-03", "MAX_STAY@2026-10-03")
}

func TestAChangeIsAskedOnlyWhatItMakesNew(t *testing.T) {
	everything := func(date string) availability.Restriction {
		return row(1, nil, nil, date, func(r *availability.Restriction) {
			r.StopSell, r.ClosedToArrival, r.ClosedToDeparture, r.MinStay, r.MaxStay = bp(true), bp(true), bp(true), ip(9), ip(1)
		})
	}
	var rows []availability.Restriction
	for i, date := range []string{"2026-10-01", "2026-10-02", "2026-10-03", "2026-10-04", "2026-10-05", "2026-10-06"} {
		r := everything(date)
		r.ID = int64(i + 1)
		rows = append(rows, r)
	}
	prev := func(a, dep string) *availability.StayDates {
		return &availability.StayDates{Arrival: d(a), Departure: d(dep)}
	}
	change := func(a, dep string, p *availability.StayDates) availability.StayRequest {
		r := stay(a, dep)
		r.Previous = p
		return r
	}
	// the same dates: nothing is asked, the guest keeps what was sold
	wantKinds(t, availability.Check(rows, change("2026-10-02", "2026-10-04", prev("2026-10-02", "2026-10-04"))))
	// a later departure: the new nights (the 4th), the new departure (the 5th) and the length; not the arrival, not the nights already sold
	wantKinds(t, availability.Check(rows, change("2026-10-02", "2026-10-05", prev("2026-10-02", "2026-10-04"))),
		"STOP_SELL@2026-10-04", "CLOSED_TO_DEPARTURE@2026-10-05", "MIN_STAY@2026-10-02", "MAX_STAY@2026-10-02")
	// an earlier arrival: the new night (the 1st) and the new arrival; the departure is the same
	wantKinds(t, availability.Check(rows, change("2026-10-01", "2026-10-04", prev("2026-10-02", "2026-10-04"))),
		"STOP_SELL@2026-10-01", "CLOSED_TO_ARRIVAL@2026-10-01", "MIN_STAY@2026-10-01", "MAX_STAY@2026-10-01")
	// a shorter stay: no new night, the new departure and the length
	wantKinds(t, availability.Check(rows, change("2026-10-01", "2026-10-03", prev("2026-10-01", "2026-10-05"))),
		"CLOSED_TO_DEPARTURE@2026-10-03", "MIN_STAY@2026-10-01", "MAX_STAY@2026-10-01")
}

func TestAnExtensionOfAGuestWhoIsInHouseIsNotAskedTheArrival(t *testing.T) {
	rows := []availability.Restriction{
		row(1, nil, nil, "2026-10-03", func(r *availability.Restriction) { r.ClosedToArrival, r.MinStay, r.MaxStay = bp(true), ip(9), ip(4) }),
		row(2, nil, nil, "2026-10-06", func(r *availability.Restriction) { r.StopSell = bp(true) }),
		row(3, nil, nil, "2026-10-07", func(r *availability.Restriction) { r.StopSell = bp(true) }),
		row(4, nil, nil, "2026-10-08", func(r *availability.Restriction) { r.ClosedToDeparture = bp(true) }),
	}
	req := stay("2026-10-03", "2026-10-08") // arrived on the 3rd, was to leave on the 6th, now the 8th
	req.BusinessDate = d("2026-10-05")
	req.Previous = &availability.StayDates{Arrival: d("2026-10-03"), Departure: d("2026-10-06")}
	req.InHouse = true
	wantKinds(t, availability.Check(rows, req), "STOP_SELL@2026-10-06", "STOP_SELL@2026-10-07", "CLOSED_TO_DEPARTURE@2026-10-08", "MAX_STAY@2026-10-03")
}

func TestARequestIsValidated(t *testing.T) {
	bad := []availability.StayRequest{
		{RatePlanID: planP, Arrival: d("2026-10-03"), Departure: d("2026-10-04")},
		{RoomTypeID: typeT, Arrival: d("2026-10-03"), Departure: d("2026-10-04")},
		{RoomTypeID: typeT, RatePlanID: planP, Arrival: d("2026-10-03"), Departure: d("2026-10-03")},
		{RoomTypeID: typeT, RatePlanID: planP, Arrival: d("2026-10-03"), Departure: d("2027-10-05")},
	}
	for i, r := range bad {
		err := r.Validate()
		var ae *apperr.Error
		if err == nil || !asApp(err, &ae) || ae.Code != "VALIDATION_FAILED" {
			t.Fatalf("case %d: %v", i, err)
		}
	}
	if err := stay("2026-10-03", "2026-10-04").Validate(); err != nil {
		t.Fatal(err)
	}
}

func asApp(err error, target **apperr.Error) bool { return errors.As(err, target) }
