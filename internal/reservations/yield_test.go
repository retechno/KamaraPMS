package reservations_test

import (
	"testing"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
)

// Yield rules move the price a night is sold at; the grid stays the base, and a reservation keeps the price it was
// sold at, with the grid price and the codes of the rules that moved it.
func TestYieldRulesPriceTheReservationsMadeAfterThem(t *testing.T) {
	f := setup(t)
	// the property has 3 rooms: one booked room is 33.33% on a night, two are 66.67%
	_, err := f.Rates.CreateYieldRule(f.admin, f.propID, rates.YieldRuleInput{
		Code: "busy", Name: "Busy nights", OccupancyFrom: ptr("60"), AdjustmentType: "PERCENT", AdjustmentValue: "20", Priority: ptr(10)})
	must(t, err)
	_, err = f.Rates.CreateYieldRule(f.admin, f.propID, rates.YieldRuleInput{
		Code: "weekend", Name: "Weekend", Weekdays: []string{"FRI", "SAT"}, AdjustmentType: "AMOUNT", AdjustmentValue: "100000", Priority: ptr(5)})
	must(t, err)

	quote := func(typ int64, arrival, departure string) rates.Quote {
		q, err := f.Rates.Quote(f.admin, f.propID, f.plan, typ, d(arrival), d(departure))
		must(t, err)
		return q
	}

	// an empty house: Thursday 1 Oct is at the grid price
	q := quote(f.dlx.ID, "2026-10-01", "2026-10-02")
	if q.Total != "1000000" || q.GridTotal != "1000000" || q.Nights[0].Occupancy != "0.00" || len(q.Nights[0].Steps) != 0 {
		t.Fatalf("empty house: %+v", q)
	}
	// Friday 2 Oct: the weekend amount
	q = quote(f.dlx.ID, "2026-10-02", "2026-10-03")
	if q.Total != "1100000" || q.GridTotal != "1000000" || len(q.Nights[0].Steps) != 1 || q.Nights[0].Steps[0].Code != "WEEKEND" {
		t.Fatalf("friday: %+v", q)
	}

	// two DLX rooms booked for 1 Oct: both are priced against the empty house
	two, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-01", "2026-10-02"), f.line(f.dlx, "2026-10-01", "2026-10-02")))
	must(t, err)
	for _, l := range two.Rooms {
		n := l.NightlyRates[0]
		if n.Amount.String() != "1000000" || n.GridRate == nil || n.GridRate.String() != "1000000" || len(n.YieldRules) != 0 {
			t.Fatalf("priced against an empty house: %+v", n)
		}
	}

	// now 66.67% full on 1 Oct: the STD room sells 20% up, and the reservation says why
	q = quote(f.std.ID, "2026-10-01", "2026-10-02")
	if q.Total != "600000" || q.GridTotal != "500000" || q.Nights[0].Occupancy != "66.67" {
		t.Fatalf("quote at 66.67%%: %+v", q)
	}
	third := f.book(t, f.std, "2026-10-01", "2026-10-02")
	n := third.Rooms[0].NightlyRates[0]
	if n.Amount.String() != "600000" || n.BaseRate == nil || n.BaseRate.String() != "600000" || n.GridRate.String() != "500000" ||
		len(n.YieldRules) != 1 || n.YieldRules[0] != "BUSY" || n.IsOverride || n.DiscountAmount.String() != "0" {
		t.Fatalf("yielded night: %+v", n)
	}
	// the first reservation did not move
	again, err := f.Res.Get(f.admin, f.propID, two.ID)
	must(t, err)
	if again.Rooms[0].NightlyRates[0].Amount.String() != "1000000" {
		t.Fatalf("an existing booking was repriced: %+v", again.Rooms[0].NightlyRates[0])
	}
	// the estimate follows the sold price
	if third.Rooms[0].Estimate.Net.String() != "600000" {
		t.Fatalf("estimate: %+v", third.Rooms[0].Estimate)
	}

	// a stay across both: Thursday (full house) and Friday (weekend)
	q = quote(f.dlx.ID, "2026-10-01", "2026-10-03")
	if q.Nights[0].Occupancy != "100.00" || q.Nights[0].Steps[0].Code != "BUSY" || q.Nights[1].Steps[0].Code != "WEEKEND" {
		t.Fatalf("two nights: %+v", q.Nights)
	}

	// switching the rule off prices the next booking at the grid again
	rule := ruleByCode(t, f, "BUSY")
	_, err = f.Rates.UpdateYieldRule(f.admin, f.propID, rule.ID, rates.YieldRuleInput{
		Code: "BUSY", Name: "Busy nights", OccupancyFrom: ptr("60"), AdjustmentType: "PERCENT", AdjustmentValue: "20", Priority: ptr(10), IsActive: ptr(false)})
	must(t, err)
	if q = quote(f.std.ID, "2026-10-01", "2026-10-02"); q.Total != "500000" {
		t.Fatalf("rule off: %+v", q)
	}
}

func TestYieldRulesLeaveOverridesAndTheSearchAlone(t *testing.T) {
	f := setup(t)
	_, err := f.Rates.CreateYieldRule(f.admin, f.propID, rates.YieldRuleInput{Code: "UP", Name: "Up", AdjustmentType: "PERCENT", AdjustmentValue: "10"})
	must(t, err)

	// an override is kept as typed, and still records the grid price
	line := f.line(f.dlx, "2026-10-04", "2026-10-05")
	line.Overrides = []reservations.NightOverride{{Date: d("2026-10-04"), Amount: "900000"}}
	over, err := f.Res.Create(f.admin, f.propID, "", f.input(true, line))
	must(t, err)
	o := over.Rooms[0].NightlyRates[0]
	if !o.IsOverride || o.Amount.String() != "900000" || o.GridRate == nil || o.GridRate.String() != "1000000" {
		t.Fatalf("override: %+v", o)
	}

	// the search shows the sold price (the grid plus the 10% rule)
	res, err := f.Res.SearchAvailability(f.admin, f.propID, d("2026-10-01"), d("2026-10-02"), 1, 0)
	must(t, err)
	seen := false
	for _, rt := range res.RoomTypes {
		if rt.RoomTypeID != f.std.ID {
			continue
		}
		for _, pl := range rt.RatePlans {
			seen = true
			if pl.Nightly[0].Amount.String() != "550000" {
				t.Fatalf("search price: %+v", pl.Nightly)
			}
		}
	}
	if !seen {
		t.Fatal("the search did not list the STD plan")
	}
}

func ruleByCode(t *testing.T, f *fx, code string) rates.YieldRule {
	t.Helper()
	list, err := f.Rates.ListYieldRules(f.admin, f.propID, nil)
	must(t, err)
	for _, r := range list {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("no rule %s in %+v", code, list)
	return rates.YieldRule{}
}

func TestYieldRuleManagement(t *testing.T) {
	f := setup(t)
	in := rates.YieldRuleInput{Code: "last", Name: "Last minute", LeadMin: ptr(0), LeadMax: ptr(2), AdjustmentType: "PERCENT", AdjustmentValue: "-10", FloorAmount: ptr("400000")}
	r, err := f.Rates.CreateYieldRule(f.admin, f.propID, in)
	must(t, err)
	if r.Code != "LAST" || r.Priority != 100 || !r.IsActive || r.LeadMax == nil || *r.LeadMax != 2 || r.FloorAmount == nil || r.FloorAmount.String() != "400000" {
		t.Fatalf("created: %+v", r)
	}
	_, err = f.Rates.CreateYieldRule(f.admin, f.propID, in)
	wantCode(t, err, "CODE_TAKEN")
	bad := in
	bad.Code, bad.AdjustmentValue = "OTHER", "-100"
	_, err = f.Rates.CreateYieldRule(f.admin, f.propID, bad)
	wantCode(t, err, "VALIDATION_FAILED")
	unknown := in
	unknown.Code, unknown.RatePlanID = "OTHER", ptr(int64(999999))
	_, err = f.Rates.CreateYieldRule(f.admin, f.propID, unknown)
	wantCode(t, err, "RATE_PLAN_NOT_FOUND")
	unknown.RatePlanID, unknown.RoomTypeID = nil, ptr(int64(999999))
	_, err = f.Rates.CreateYieldRule(f.admin, f.propID, unknown)
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")

	// a rule that names a plan and a type applies to them only
	scoped := rates.YieldRuleInput{Code: "STDONLY", Name: "STD only", RatePlanID: &f.plan, RoomTypeID: &f.std.ID, AdjustmentType: "AMOUNT", AdjustmentValue: "50000"}
	_, err = f.Rates.CreateYieldRule(f.admin, f.propID, scoped)
	must(t, err)
	q, err := f.Rates.Quote(f.admin, f.propID, f.plan, f.dlx.ID, d("2026-10-10"), d("2026-10-11"))
	must(t, err)
	if q.Total != "1000000" {
		t.Fatalf("a rule for STD priced DLX: %+v", q)
	}
	q, err = f.Rates.Quote(f.admin, f.propID, f.plan, f.std.ID, d("2026-10-10"), d("2026-10-11"))
	must(t, err)
	if q.Total != "550000" {
		t.Fatalf("STD: %+v", q)
	}

	// update: the code cannot change
	in.Code = "CHANGED"
	_, err = f.Rates.UpdateYieldRule(f.admin, f.propID, r.ID, in)
	wantCode(t, err, "VALIDATION_FAILED")
	in.Code, in.Name = "LAST", "Last minute, 3 days"
	in.LeadMax = ptr(3)
	up, err := f.Rates.UpdateYieldRule(f.admin, f.propID, r.ID, in)
	must(t, err)
	if up.Name != "Last minute, 3 days" || *up.LeadMax != 3 {
		t.Fatalf("updated: %+v", up)
	}
	_, err = f.Rates.UpdateYieldRule(f.admin, f.propID, 999999, in)
	wantCode(t, err, "YIELD_RULE_NOT_FOUND")

	// permissions: anyone at the property reads, rate.manage writes
	reader := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	list, err := f.Rates.ListYieldRules(reader, f.propID, nil)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	_, err = f.Rates.CreateYieldRule(reader, f.propID, scoped)
	wantCode(t, err, "PERMISSION_DENIED")
	wantCode(t, f.Rates.DeleteYieldRule(reader, f.propID, r.ID), "PERMISSION_DENIED")

	must(t, f.Rates.DeleteYieldRule(f.admin, f.propID, r.ID))
	wantCode(t, f.Rates.DeleteYieldRule(f.admin, f.propID, r.ID), "YIELD_RULE_NOT_FOUND")
	if n := f.count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'yield_rule.%'`); n != 4 { // two created, one updated, one deleted
		t.Fatalf("audit entries: %d", n)
	}
}
