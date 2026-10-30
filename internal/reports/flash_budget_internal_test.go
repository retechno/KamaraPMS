package reports

import (
	"testing"

	"github.com/shopspring/decimal"
)

// The budget of the month to date is the budget of the month times the day over the days of the month (Q4): 3,000,000,000 on day 10 of 30 is 1,000,000,000, and on day 29 of 29 days in February
// it is the whole month.
func TestTheBudgetOfTheMonthToDateIsProRata(t *testing.T) {
	for _, c := range []struct {
		name        string
		day, dim    int
		month, want string
	}{
		{"day 10 of 30", 10, 30, "3000000000", "1000000000"},
		{"day 10 of 31", 10, 31, "3100000000", "1000000000"},
		{"the whole of February in a leap year", 29, 29, "2900000", "2900000"},
		{"the first day", 1, 30, "30000", "1000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := budgetMonth{day: c.day, dim: c.dim}.prorata(decimal.RequireFromString(c.month))
			if !got.Equal(decimal.RequireFromString(c.want)) {
				t.Fatalf("%s, want %s", got, c.want)
			}
		})
	}
}
