package rates

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func decp(s string) *decimal.Decimal {
	v := dec(s)
	return &v
}
func intp(n int) *int    { return &n }
func i64(n int64) *int64 { return &n }
func datep(s string) *civil.Date {
	v := civil.MustParseDate(s)
	return &v
}

func night(date string, occupancy string) NightContext {
	return NightContext{RatePlanID: 1, RoomTypeID: 2, Date: civil.MustParseDate(date), Occupancy: dec(occupancy), LeadDays: 10, StayNights: 2}
}

func TestYieldRuleMatchesEveryCondition(t *testing.T) {
	// 2026-10-03 is a Saturday
	base := night("2026-10-03", "70")
	cases := []struct {
		name string
		rule YieldRule
		ctx  NightContext
		want bool
	}{
		{"no conditions", YieldRule{}, base, true},
		{"other plan", YieldRule{RatePlanID: i64(9)}, base, false},
		{"same plan and type", YieldRule{RatePlanID: i64(1), RoomTypeID: i64(2)}, base, true},
		{"other type", YieldRule{RoomTypeID: i64(9)}, base, false},
		{"before the season", YieldRule{StayFrom: datep("2026-10-04")}, base, false},
		{"after the season", YieldRule{StayTo: datep("2026-10-02")}, base, false},
		{"first and last day of the season", YieldRule{StayFrom: datep("2026-10-03"), StayTo: datep("2026-10-03")}, base, true},
		{"weekend", YieldRule{Weekdays: []string{"FRI", "SAT"}}, base, true},
		{"not on a Saturday", YieldRule{Weekdays: []string{"MON", "TUE"}}, base, false},
		{"occupancy from is included", YieldRule{OccupancyFrom: decp("70")}, base, true},
		{"occupancy below from", YieldRule{OccupancyFrom: decp("70.01")}, base, false},
		{"occupancy to is excluded", YieldRule{OccupancyTo: decp("70")}, base, false},
		{"occupancy under to", YieldRule{OccupancyTo: decp("70.01")}, base, true},
		{"a full house is inside a to of 100", YieldRule{OccupancyFrom: decp("90"), OccupancyTo: decp("100")}, night("2026-10-03", "100"), true},
		{"but not inside a to of 99", YieldRule{OccupancyFrom: decp("90"), OccupancyTo: decp("99")}, night("2026-10-03", "100"), false},
		{"lead time inside", YieldRule{LeadMin: intp(7), LeadMax: intp(10)}, base, true},
		{"too close", YieldRule{LeadMin: intp(11)}, base, false},
		{"too far", YieldRule{LeadMax: intp(9)}, base, false},
		{"long enough stay", YieldRule{StayMin: intp(2)}, base, true},
		{"stay too short", YieldRule{StayMin: intp(3)}, base, false},
		{"stay too long", YieldRule{StayMax: intp(1)}, base, false},
	}
	for _, c := range cases {
		if got := c.rule.Matches(c.ctx); got != c.want {
			t.Errorf("%s: matches = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestYieldAdjustment(t *testing.T) {
	cases := []struct {
		name  string
		rule  YieldRule
		price string
		dec   int32
		want  string
	}{
		{"percent up", YieldRule{AdjustmentType: AdjustPercent, AdjustmentValue: dec("20")}, "1000000", 0, "1200000"},
		{"percent down", YieldRule{AdjustmentType: AdjustPercent, AdjustmentValue: dec("-15")}, "1000000", 0, "850000"},
		{"amount", YieldRule{AdjustmentType: AdjustAmount, AdjustmentValue: dec("-25000")}, "1000000", 0, "975000"},
		{"rounds half away from zero", YieldRule{AdjustmentType: AdjustPercent, AdjustmentValue: dec("10")}, "100.05", 1, "110.1"},
		{"rounds to the currency", YieldRule{AdjustmentType: AdjustPercent, AdjustmentValue: dec("12.5")}, "333", 0, "375"},
		{"the floor holds a discount", YieldRule{AdjustmentType: AdjustPercent, AdjustmentValue: dec("-50"), FloorAmount: decp("700000")}, "1000000", 0, "700000"},
		{"the cap holds a surcharge", YieldRule{AdjustmentType: AdjustPercent, AdjustmentValue: dec("50"), CapAmount: decp("1300000")}, "1000000", 0, "1300000"},
		{"never below zero", YieldRule{AdjustmentType: AdjustAmount, AdjustmentValue: dec("-5000000")}, "1000000", 0, "0"},
	}
	for _, c := range cases {
		if got := c.rule.Adjust(dec(c.price), c.dec); !got.Equal(dec(c.want)) {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}

func TestApplyYieldStacksInPriorityOrder(t *testing.T) {
	busy := YieldRule{ID: 1, Code: "BUSY", Name: "Busy", IsActive: true, Priority: 20, OccupancyFrom: decp("60"), AdjustmentType: AdjustPercent, AdjustmentValue: dec("20")}
	weekend := YieldRule{ID: 2, Code: "WEEKEND", Name: "Weekend", IsActive: true, Priority: 10, Weekdays: []string{"FRI", "SAT"}, AdjustmentType: AdjustAmount, AdjustmentValue: dec("100000")}
	off := YieldRule{ID: 3, Code: "OFF", Name: "Off", IsActive: false, Priority: 5, AdjustmentType: AdjustPercent, AdjustmentValue: dec("-90")}
	other := YieldRule{ID: 4, Code: "SPRING", Name: "Spring", IsActive: true, Priority: 30, StayTo: datep("2026-04-30"), AdjustmentType: AdjustPercent, AdjustmentValue: dec("-10")}
	rules := []YieldRule{busy, weekend, off, other}
	SortRules(rules)

	// Saturday at 70%: the weekend amount first (priority 10), then +20% of that
	price, steps := ApplyYield(rules, dec("1000000"), night("2026-10-03", "70"), 0)
	if !price.Equal(dec("1320000")) || len(steps) != 2 || steps[0].Code != "WEEKEND" || steps[1].Code != "BUSY" || !steps[1].Before.Equal(dec("1100000")) {
		t.Fatalf("saturday at 70%%: %s %+v", price, steps)
	}
	// a quiet Monday: nothing applies, the grid price stands
	price, steps = ApplyYield(rules, dec("1000000"), night("2026-10-05", "20"), 0)
	if !price.Equal(dec("1000000")) || len(steps) != 0 {
		t.Fatalf("monday at 20%%: %s %+v", price, steps)
	}
	// a rule that leaves the price as it was is not listed
	flat := YieldRule{ID: 5, Code: "FLAT", IsActive: true, AdjustmentType: AdjustPercent, AdjustmentValue: dec("10"), CapAmount: decp("1000000")}
	price, steps = ApplyYield([]YieldRule{flat}, dec("1000000"), night("2026-10-05", "20"), 0)
	if !price.Equal(dec("1000000")) || len(steps) != 0 {
		t.Fatalf("capped: %s %+v", price, steps)
	}
	// the same priority applies the older rule first
	a := YieldRule{ID: 7, Priority: 1}
	b := YieldRule{ID: 6, Priority: 1}
	both := []YieldRule{a, b}
	SortRules(both)
	if both[0].ID != 6 {
		t.Fatalf("order: %+v", both)
	}
}

func TestParseYieldRule(t *testing.T) {
	ok := YieldRuleInput{Code: " busy ", Name: "Busy nights", AdjustmentType: "percent", AdjustmentValue: "20", OccupancyFrom: ptrStr("60"), Weekdays: []string{"fri", "SAT"}}
	pr, errs := parseYieldRule(ok, 0)
	if len(errs) != 0 || pr.code != "BUSY" || pr.adjType != AdjustPercent || !pr.adjValue.Equal(dec("20")) || len(pr.weekdays) != 2 || pr.weekdays[0] != "FRI" || pr.priority != 100 || !pr.active {
		t.Fatalf("valid: %+v %+v", pr, errs)
	}

	bad := map[string]YieldRuleInput{
		"code":             {Code: "no good", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5"},
		"name":             {Code: "A", AdjustmentType: "PERCENT", AdjustmentValue: "5"},
		"adjustment_type":  {Code: "A", Name: "x", AdjustmentType: "ADD", AdjustmentValue: "5"},
		"adjustment_value": {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "0"},
		"priority":         {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", Priority: intp(-1)},
		"weekdays":         {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", Weekdays: []string{"MON", "MON"}},
		"stay_to":          {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", StayFrom: datep("2026-10-05"), StayTo: datep("2026-10-01")},
		"occupancy_from":   {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", OccupancyFrom: ptrStr("100")},
		"occupancy_to":     {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", OccupancyFrom: ptrStr("60"), OccupancyTo: ptrStr("60")},
		"lead_days_max":    {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", LeadMin: intp(5), LeadMax: intp(2)},
		"stay_nights_min":  {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", StayMin: intp(0)},
		"cap_amount":       {Code: "A", Name: "x", AdjustmentType: "AMOUNT", AdjustmentValue: "5", FloorAmount: ptrStr("900"), CapAmount: ptrStr("800")},
		"floor_amount":     {Code: "A", Name: "x", AdjustmentType: "AMOUNT", AdjustmentValue: "5", FloorAmount: ptrStr("-1")},
	}
	for field, in := range bad {
		_, errs := parseYieldRule(in, 0)
		found := false
		for _, e := range errs {
			found = found || e.Field == field
		}
		if !found {
			t.Errorf("%s: not reported, got %+v", field, errs)
		}
	}
	// conditions have an upper bound too, so they always fit the column
	for field, in := range map[string]YieldRuleInput{
		"lead_days_max":   {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", LeadMax: intp(3000000000)},
		"stay_nights_max": {Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: "5", StayMax: intp(366)},
	} {
		_, errs := parseYieldRule(in, 0)
		found := false
		for _, e := range errs {
			found = found || e.Field == field
		}
		if !found {
			t.Errorf("%s: an out of range value was accepted: %+v", field, errs)
		}
	}
	// a percentage cannot take a price to nothing, and an amount keeps to the currency's decimals
	for _, v := range []string{"-100", "-150", "1000.01"} {
		if _, errs := parseYieldRule(YieldRuleInput{Code: "A", Name: "x", AdjustmentType: "PERCENT", AdjustmentValue: v}, 0); len(errs) == 0 {
			t.Errorf("percent %s accepted", v)
		}
	}
	if _, errs := parseYieldRule(YieldRuleInput{Code: "A", Name: "x", AdjustmentType: "AMOUNT", AdjustmentValue: "10.5"}, 0); len(errs) == 0 {
		t.Error("an amount with a decimal was accepted for a currency without decimals")
	}
	if _, errs := parseYieldRule(YieldRuleInput{Code: "A", Name: "x", AdjustmentType: "AMOUNT", AdjustmentValue: "10.5"}, 2); len(errs) != 0 {
		t.Errorf("an amount with a decimal was refused for a currency with two: %+v", errs)
	}
}

func ptrStr(s string) *string { return &s }
