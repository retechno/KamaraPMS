package roompanel

import (
	"testing"

	"kamarapms/internal/platform/civil"
)

func TestNightOf(t *testing.T) {
	d := func(day int) civil.Date { return civil.NewDate(2026, 10, day) }
	for _, c := range []struct {
		name    string
		arrival civil.Date
		nights  int
		on      civil.Date
		want    int
	}{
		{"arrival day", d(9), 3, d(9), 1},
		{"second night", d(9), 3, d(10), 2},
		{"last night", d(9), 3, d(11), 3},
		{"departure day is still the last night", d(9), 3, d(12), 3},
		{"overdue stays at the last night", d(9), 3, d(20), 3},
		{"before the arrival is the first", d(9), 3, d(8), 1},
		{"a stay without nights is the first", d(9), 0, d(9), 1},
	} {
		if got := nightOf(c.arrival, c.nights, c.on); got != c.want {
			t.Errorf("%s: night %d, want %d", c.name, got, c.want)
		}
	}
}
