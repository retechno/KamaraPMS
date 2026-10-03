package cityledger

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestOverdueBuckets(t *testing.T) {
	for days, want := range map[int]string{1: "1-30", 30: "1-30", 31: "31-60", 60: "31-60", 61: "61-90", 90: "61-90", 91: "90+", 400: "90+"} {
		if got := overdueBucket(days); got != want {
			t.Errorf("%d days: %s, want %s", days, got, want)
		}
	}
}

func TestInterestIsTheMonthlyRateOnWhatIsOwedForTheDaysPastTheGrace(t *testing.T) {
	d := decimal.RequireFromString
	cases := []struct {
		out, rate   string
		days, grace int
		decimals    int32
		want        string
	}{
		{"300000", "3", 30, 0, 0, "9000"},   // a whole month
		{"300000", "3", 15, 0, 0, "4500"},   // half a month
		{"300000", "3", 15, 10, 0, "1500"},  // only the 5 days past the grace
		{"300000", "3", 10, 10, 0, "0"},     // within the grace
		{"300000", "0", 90, 0, 0, "0"},      // no late fee
		{"0", "3", 90, 0, 0, "0"},           // nothing owed
		{"100000", "2.5", 7, 0, 0, "583"},   // rounded to the currency: 100,000 x 2.5% x 7/30 = 583.33
		{"100.00", "1.5", 45, 0, 2, "2.25"}, // two decimals
	}
	for _, c := range cases {
		got := interestOf(d(c.out), c.days, d(c.rate), c.grace, c.decimals)
		if !got.Equal(d(c.want)) {
			t.Errorf("%s at %s%% for %d days (grace %d): %s, want %s", c.out, c.rate, c.days, c.grace, got, c.want)
		}
	}
}
