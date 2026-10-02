package rates

import (
	"slices"
	"testing"

	"kamarapms/internal/platform/civil"
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func TestFillDatesFollowWeekdaysAndHalfOpenRange(t *testing.T) {
	// 2026-10-01 is a Thursday.
	all := FillInput{From: d("2026-10-01"), To: d("2026-10-08")}
	if got := all.Dates(); len(got) != 7 || got[0] != "2026-10-01" || got[6] != "2026-10-07" {
		t.Fatalf("every day, end exclusive: %v", got)
	}
	weekend := FillInput{From: d("2026-10-01"), To: d("2026-10-15"), Weekdays: []string{"FRI", "SAT"}}
	want := []string{"2026-10-02", "2026-10-03", "2026-10-09", "2026-10-10"}
	if got := weekend.Dates(); !slices.Equal(got, want) {
		t.Fatalf("weekend nights: %v, want %v", got, want)
	}
	if got := (FillInput{From: d("2026-10-01"), To: d("2026-10-02"), Weekdays: []string{"MON"}}).Dates(); len(got) != 0 {
		t.Fatalf("no matching weekday: %v", got)
	}
	// Month and year ends are plain calendar arithmetic.
	if got := (FillInput{From: d("2026-12-30"), To: d("2027-01-02")}).Dates(); !slices.Equal(got, []string{"2026-12-30", "2026-12-31", "2027-01-01"}) {
		t.Fatalf("year end: %v", got)
	}
}

func TestFillValidation(t *testing.T) {
	ok := FillInput{RatePlanID: 1, RoomTypeIDs: []int64{1, 2}, From: d("2026-10-01"), To: d("2026-10-02"), Weekdays: []string{"MON", "TUE"}}
	if fe := ok.Validate(); len(fe) != 0 {
		t.Fatalf("valid: %v", fe)
	}
	cases := map[string]func(*FillInput){
		"rate_plan_id":  func(i *FillInput) { i.RatePlanID = 0 },
		"room_type_ids": func(i *FillInput) { i.RoomTypeIDs = nil },
		"to":            func(i *FillInput) { i.To = i.From },
		"weekdays":      func(i *FillInput) { i.Weekdays = []string{"MON", "MON"} },
	}
	for field, edit := range cases {
		in := ok
		edit(&in)
		found := false
		for _, fe := range in.Validate() {
			found = found || fe.Field == field
		}
		if !found {
			t.Errorf("expected an error on %s", field)
		}
	}
	dup := ok
	dup.RoomTypeIDs = []int64{1, 1}
	if len(dup.Validate()) == 0 {
		t.Error("duplicate room types")
	}
	many := ok
	many.RoomTypeIDs = make([]int64, MaxRoomTypes+1)
	for i := range many.RoomTypeIDs {
		many.RoomTypeIDs[i] = int64(i + 1)
	}
	if len(many.Validate()) == 0 {
		t.Error("too many room types")
	}
	long := ok
	long.To = d("2026-10-01").AddDays(MaxSpanDays)
	if fe := long.Validate(); len(fe) != 0 {
		t.Errorf("exactly 730 days is allowed: %v", fe)
	}
	long.To = d("2026-10-01").AddDays(MaxSpanDays + 1)
	if fe := long.Validate(); len(fe) != 1 || fe[0].Code != "SPAN_TOO_LONG" {
		t.Errorf("731 days: %v", fe)
	}
	bad := ok
	bad.Weekdays = []string{"MONDAY"}
	if len(bad.Validate()) == 0 {
		t.Error("weekday names are three letters")
	}
}

func TestParseAmountFollowsCurrencyAndColumn(t *testing.T) {
	for _, tc := range []struct {
		in       string
		decimals int32
		ok       bool
	}{
		{"1500000", 0, true}, {"1500000.00", 0, true}, {"1500000.50", 0, false}, {"150.50", 2, true}, {"150.505", 2, false},
		{"150.50", 3, true}, {"150.501", 3, true}, {"150.5051", 3, false}, {"150.501", 2, false}, // KWD and BHD keep three decimals
		{"999999999999999.999", 3, true}, {"1000000000000000", 0, false}, // numeric(18,3) holds 15 integer digits
		{"0", 2, true}, {"-1", 2, false}, {"", 2, false}, {"1e3", 2, false}, {"1,5", 2, false},
	} {
		if _, err := ParseAmount(tc.in, tc.decimals); (err == nil) != tc.ok {
			t.Errorf("ParseAmount(%q, %d): ok=%v, want %v", tc.in, tc.decimals, err == nil, tc.ok)
		}
	}
}
