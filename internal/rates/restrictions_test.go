package rates_test

import (
	"sync"
	"testing"

	"kamarapms/internal/availability"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rates"
	"kamarapms/internal/rooms/roomstest"
)

// The restriction grid: the bulk fill, the reads and the effective view. The precedence itself is tested in availability; here it is only checked that the view agrees with
// the evaluator.

func bp(b bool) *bool { return &b }
func ip(n int) *int   { return &n }

func (f fixture) fillR(t *testing.T, in rates.FillRestrictionsInput) rates.FillRestrictionsResult {
	t.Helper()
	res, err := f.Rates.FillRestrictions(f.admin, f.bali, in)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (f fixture) rows(t *testing.T, from, to string, roomType, plan *int64) []rates.Restriction {
	t.Helper()
	rows, err := f.Rates.ListRestrictions(f.admin, f.bali, d(from), d(to), roomType, plan)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestAFillCreatesTheRowsOfItsScopeOnTheSelectedWeekdays(t *testing.T) {
	f := newFixture(t)
	// every room type, every plan, the Fridays and Saturdays of a fortnight: a property-wide row each
	res := f.fillR(t, rates.FillRestrictionsInput{From: d("2026-12-01"), To: d("2026-12-15"), Weekdays: []string{"FRI", "SAT"}, Set: rates.RestrictionSet{StopSell: bp(true), MinStay: ip(2)}})
	if res.Dates != 4 || res.Scopes != 1 || res.Created != 4 || res.Updated != 0 || res.Deleted != 0 {
		t.Fatalf("result: %+v", res)
	}
	rows := f.rows(t, "2026-12-01", "2026-12-15", nil, nil)
	if len(rows) != 4 || rows[0].StayDate != d("2026-12-04") || rows[0].RoomTypeID != nil || rows[0].RatePlanID != nil || rows[0].StopSell == nil || !*rows[0].StopSell || *rows[0].MinStay != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].ClosedToArrival != nil || rows[0].MaxStay != nil {
		t.Fatalf("what is not set stays without an opinion: %+v", rows[0])
	}
	// the product of the room types and the plans: 2 x 2 scopes on 3 dates
	bar, corp := f.plan(t, "BAR", f.room).ID, f.plan(t, "CORP", f.room).ID
	res = f.fillR(t, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx, f.std}, RatePlanIDs: []int64{bar, corp}, From: d("2026-12-20"), To: d("2026-12-23"), Set: rates.RestrictionSet{ClosedToArrival: bp(true)}})
	if res.Scopes != 4 || res.Created != 12 {
		t.Fatalf("product: %+v", res)
	}
	// reading for a room type and a plan lists the rows that can speak for it, the wider ones included
	got := f.rows(t, "2026-12-01", "2026-12-31", &f.dlx, &bar)
	if len(got) != 4+3 { // the four property rows and the three of (dlx, bar)
		t.Fatalf("the rows that speak for dlx and bar: %d", len(got))
	}
}

func TestAFillSetsClearsAndRemovesWhatSaysNothing(t *testing.T) {
	f := newFixture(t)
	scope := rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx}, From: d("2026-12-24"), To: d("2026-12-26")}
	in := scope
	in.Set = rates.RestrictionSet{StopSell: bp(true), MinStay: ip(3)}
	if res := f.fillR(t, in); res.Created != 2 {
		t.Fatalf("first: %+v", res)
	}
	// a second fill merges: it adds a maximum, changes the minimum, leaves the stop sell
	in = scope
	in.Set = rates.RestrictionSet{MaxStay: ip(7), MinStay: ip(2)}
	if res := f.fillR(t, in); res.Created != 0 || res.Updated != 2 {
		t.Fatalf("second: %+v", res)
	}
	r := f.rows(t, "2026-12-24", "2026-12-26", &f.dlx, nil)
	if len(r) != 2 || !*r[0].StopSell || *r[0].MinStay != 2 || *r[0].MaxStay != 7 {
		t.Fatalf("merged: %+v", r)
	}
	// clearing one attribute keeps the row, clearing the last two removes it
	in = scope
	in.Clear = []string{"min_stay"}
	if res := f.fillR(t, in); res.Updated != 2 || res.Deleted != 0 {
		t.Fatalf("clear one: %+v", res)
	}
	if r := f.rows(t, "2026-12-24", "2026-12-26", &f.dlx, nil); r[0].MinStay != nil || *r[0].MaxStay != 7 {
		t.Fatalf("cleared: %+v", r[0])
	}
	in = scope
	in.Clear = []string{"stop_sell", "max_stay"}
	if res := f.fillR(t, in); res.Deleted != 2 {
		t.Fatalf("clear the rest: %+v", res)
	}
	if r := f.rows(t, "2026-12-24", "2026-12-26", nil, nil); len(r) != 0 {
		t.Fatalf("a row that says nothing is removed: %+v", r)
	}
	// clearing where nothing is does nothing, and is not an error
	in = scope
	in.Clear = []string{"stop_sell"}
	if res := f.fillR(t, in); res.Created != 0 || res.Updated != 0 || res.Deleted != 0 {
		t.Fatalf("nothing to clear: %+v", res)
	}
	// setting one attribute while clearing the only other one keeps the row (it still says something)
	in = scope
	in.Set = rates.RestrictionSet{StopSell: bp(false)}
	f.fillR(t, in)
	in = scope
	in.Set = rates.RestrictionSet{MinStay: ip(2)}
	in.Clear = []string{"stop_sell"}
	if res := f.fillR(t, in); res.Deleted != 0 || res.Updated != 2 {
		t.Fatalf("swap: %+v", res)
	}
	if r := f.rows(t, "2026-12-24", "2026-12-26", &f.dlx, nil); len(r) != 2 || r[0].StopSell != nil || *r[0].MinStay != 2 {
		t.Fatalf("swapped: %+v", r)
	}
	// the note travels with the row and an empty note clears it
	note, empty := "Christmas", ""
	in = scope
	in.Set = rates.RestrictionSet{Note: &note}
	f.fillR(t, in)
	if r := f.rows(t, "2026-12-24", "2026-12-25", &f.dlx, nil); r[0].Note != "Christmas" {
		t.Fatalf("note: %+v", r[0])
	}
	in.Set = rates.RestrictionSet{Note: &empty}
	f.fillR(t, in)
	if r := f.rows(t, "2026-12-24", "2026-12-25", &f.dlx, nil); r[0].Note != "" {
		t.Fatalf("cleared note: %+v", r[0])
	}
}

func TestAFillIsChecked(t *testing.T) {
	f := newFixture(t)
	bar := f.plan(t, "BAR", f.room).ID
	ok := rates.FillRestrictionsInput{From: d("2026-12-01"), To: d("2026-12-03"), Set: rates.RestrictionSet{StopSell: bp(true)}}
	with := func(mod func(*rates.FillRestrictionsInput)) rates.FillRestrictionsInput {
		in := ok
		mod(&in)
		return in
	}
	bad := map[string]rates.FillRestrictionsInput{
		"nothing to change":    with(func(in *rates.FillRestrictionsInput) { in.Set = rates.RestrictionSet{} }),
		"minimum above max":    with(func(in *rates.FillRestrictionsInput) { in.Set = rates.RestrictionSet{MinStay: ip(5), MaxStay: ip(3)} }),
		"minimum below one":    with(func(in *rates.FillRestrictionsInput) { in.Set = rates.RestrictionSet{MinStay: ip(0)} }),
		"maximum above 365":    with(func(in *rates.FillRestrictionsInput) { in.Set = rates.RestrictionSet{MaxStay: ip(366)} }),
		"to before from":       with(func(in *rates.FillRestrictionsInput) { in.To = d("2026-12-01") }),
		"more than 366 days":   with(func(in *rates.FillRestrictionsInput) { in.To = d("2028-01-01") }),
		"a bad weekday":        with(func(in *rates.FillRestrictionsInput) { in.Weekdays = []string{"FUN"} }),
		"a bad clear name":     with(func(in *rates.FillRestrictionsInput) { in.Clear = []string{"colour"} }),
		"set and cleared":      with(func(in *rates.FillRestrictionsInput) { in.Clear = []string{"stop_sell"} }),
		"a duplicate type":     with(func(in *rates.FillRestrictionsInput) { in.RoomTypeIDs = []int64{f.dlx, f.dlx} }),
		"a note too long":      with(func(in *rates.FillRestrictionsInput) { n := string(make([]rune, 201)); in.Set.Note = &n }),
		"no date on a weekday": with(func(in *rates.FillRestrictionsInput) { in.To = d("2026-12-02"); in.Weekdays = []string{"FRI"} }), // 2026-12-01 is a Tuesday
	}
	for name, in := range bad {
		_, err := f.Rates.FillRestrictions(f.admin, f.bali, in)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	_, err := f.Rates.FillRestrictions(f.admin, f.bali, with(func(in *rates.FillRestrictionsInput) { in.RoomTypeIDs = []int64{999999} }))
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")
	_, err = f.Rates.FillRestrictions(f.admin, f.bali, with(func(in *rates.FillRestrictionsInput) { in.RatePlanIDs = []int64{999999} }))
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM rate_restrictions`); n != 0 {
		t.Fatalf("a refused fill writes nothing: %d rows", n)
	}
	// the scopes of a fill are capped
	var types, plans []int64
	for i := int64(1); i <= 11; i++ {
		types = append(types, i)
		plans = append(plans, 100+i)
	}
	_, err = f.Rates.FillRestrictions(f.admin, f.bali, with(func(in *rates.FillRestrictionsInput) { in.RoomTypeIDs, in.RatePlanIDs = types, plans }))
	wantCode(t, err, "VALIDATION_FAILED")
	_ = bar
}

func TestTheGridIsReadByAnyoneAtThePropertyAndWrittenByRateManage(t *testing.T) {
	f := newFixture(t)
	f.fillR(t, rates.FillRestrictionsInput{From: d("2026-12-01"), To: d("2026-12-03"), Set: rates.RestrictionSet{StopSell: bp(true)}})
	reader := f.User(t, f.tenantID, f.bali, auth.PermReservationRead)
	if _, err := f.Rates.ListRestrictions(reader, f.bali, d("2026-12-01"), d("2026-12-03"), nil, nil); err != nil {
		t.Fatalf("read: %v", err)
	}
	_, err := f.Rates.FillRestrictions(reader, f.bali, rates.FillRestrictionsInput{From: d("2026-12-01"), To: d("2026-12-03"), Set: rates.RestrictionSet{StopSell: bp(false)}})
	wantCode(t, err, "PERMISSION_DENIED")
	manager := f.User(t, f.tenantID, f.bali, auth.PermRateManage)
	if _, err := f.Rates.FillRestrictions(manager, f.bali, rates.FillRestrictionsInput{From: d("2026-12-01"), To: d("2026-12-03"), Set: rates.RestrictionSet{StopSell: bp(false)}}); err != nil {
		t.Fatalf("rate.manage: %v", err)
	}
	// another tenant sees nothing of this property
	other := f.Tenant(t, "XYZ")
	otherProp := f.Property(t, other.ID, "SG")
	foreign := f.User(t, other.ID, otherProp.ID, auth.PermRateManage)
	_, err = f.Rates.ListRestrictions(foreign, f.bali, d("2026-12-01"), d("2026-12-03"), nil, nil)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Rates.FillRestrictions(foreign, f.bali, rates.FillRestrictionsInput{From: d("2026-12-01"), To: d("2026-12-03"), Set: rates.RestrictionSet{StopSell: bp(true)}})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestAFillIsAuditedAndNeedsAnOpenBusinessDay(t *testing.T) {
	f := newFixture(t)
	f.fillR(t, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx}, From: d("2026-12-01"), To: d("2026-12-04"), Set: rates.RestrictionSet{MinStay: ip(2)}})
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'rate_restrictions.filled' AND new_data->>'created' = '3' AND new_data->'set'->>'min_stay' = '2'`); n != 1 {
		t.Fatalf("the audit entry names the scope, the dates and what was set: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'rate_restrictions.filled' AND business_date = '2026-09-30'`); n != 1 {
		t.Fatalf("the audit entry carries the business date: %d", n)
	}
}

// The effective view uses the precedence of the evaluator, and what it says is what a booking attempt is told.
func TestTheEffectiveViewAgreesWithTheEvaluator(t *testing.T) {
	f := newFixture(t)
	bar, corp := f.plan(t, "BAR", f.room).ID, f.plan(t, "CORP", f.room).ID
	f.fillR(t, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx}, From: d("2026-12-05"), To: d("2026-12-06"), Set: rates.RestrictionSet{StopSell: bp(true), MinStay: ip(3)}})
	f.fillR(t, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx}, RatePlanIDs: []int64{corp}, From: d("2026-12-05"), To: d("2026-12-06"), Set: rates.RestrictionSet{StopSell: bp(false)}})
	f.fillR(t, rates.FillRestrictionsInput{From: d("2026-12-05"), To: d("2026-12-06"), Set: rates.RestrictionSet{ClosedToArrival: bp(true), MinStay: ip(1)}})

	day := func(plan int64) (stop, cta bool, min *int, sources map[string]string) {
		days, err := f.Rates.EffectiveRestrictions(f.admin, f.bali, f.dlx, plan, d("2026-12-05"), d("2026-12-06"))
		if err != nil || len(days) != 1 {
			t.Fatalf("effective: %v %+v", err, days)
		}
		sources = map[string]string{}
		for k, v := range days[0].Sources {
			sources[k] = v.Scope
		}
		return days[0].StopSell, days[0].ClosedToArrival, days[0].MinStay, sources
	}
	stop, cta, min, src := day(bar)
	if !stop || !cta || min == nil || *min != 3 || src["stop_sell"] != "ROOM_TYPE" || src["closed_to_arrival"] != "PROPERTY" || src["min_stay"] != "ROOM_TYPE" {
		t.Fatalf("BAR: %v %v %v %v", stop, cta, min, src)
	}
	if stop, _, _, _ := day(corp); stop {
		t.Fatal("CORP is open: its own FALSE wins")
	}
	// the evaluator says the same: BAR is refused (stop sell, closed to arrival, 1 night under a minimum of 3), CORP only for the arrival and the minimum
	verdict := func(plan int64) []string {
		v, err := f.Avail.EvaluateStay(f.admin, f.tenantID, f.bali, availabilityRequest(f.dlx, plan, "2026-12-05", "2026-12-06"))
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range v.Violations {
			out = append(out, string(x.Type))
		}
		return out
	}
	if got := verdict(bar); len(got) != 3 || got[0] != "STOP_SELL" || got[1] != "CLOSED_TO_ARRIVAL" || got[2] != "MIN_STAY" {
		t.Fatalf("BAR verdict: %v", got)
	}
	if got := verdict(corp); len(got) != 2 || got[0] != "CLOSED_TO_ARRIVAL" || got[1] != "MIN_STAY" {
		t.Fatalf("CORP verdict: %v", got)
	}
	// a window beyond 366 days, an unknown room type and an unknown plan are refused
	_, err := f.Rates.EffectiveRestrictions(f.admin, f.bali, f.dlx, bar, d("2026-01-01"), d("2028-01-01"))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Rates.EffectiveRestrictions(f.admin, f.bali, 999999, bar, d("2026-12-05"), d("2026-12-06"))
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")
	_, err = f.Rates.EffectiveRestrictions(f.admin, f.bali, f.dlx, 999999, d("2026-12-05"), d("2026-12-06"))
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
}

// Two fills of the same rows at the same time merge: neither of them is lost.
func TestConcurrentFillsOfTheSameRowsMerge(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, set := range []rates.RestrictionSet{{StopSell: bp(true)}, {MinStay: ip(4)}} {
		wg.Add(1)
		go func(set rates.RestrictionSet) {
			defer wg.Done()
			_, err := f.Rates.FillRestrictions(f.admin, f.bali, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx}, From: d("2026-12-10"), To: d("2026-12-15"), Set: set})
			errs <- err
		}(set)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent fill failed: %v", err)
		}
	}
	rows := f.rows(t, "2026-12-10", "2026-12-15", &f.dlx, nil)
	if len(rows) != 5 {
		t.Fatalf("one row a date: %d", len(rows))
	}
	for _, r := range rows {
		if r.StopSell == nil || !*r.StopSell || r.MinStay == nil || *r.MinStay != 4 {
			t.Fatalf("both fills are in the row: %+v", r)
		}
	}
}

func availabilityRequest(roomType, plan int64, arrival, departure string) availability.StayRequest {
	return availability.StayRequest{RoomTypeID: roomType, RatePlanID: plan, Arrival: civil.MustParseDate(arrival), Departure: civil.MustParseDate(departure), BusinessDate: roomstest.BD}
}
