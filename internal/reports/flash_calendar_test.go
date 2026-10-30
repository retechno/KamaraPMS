package reports

import (
	"testing"

	"kamarapms/internal/platform/civil"
)

func TestFlashRangesCutTheComparisonMonths(t *testing.T) {
	cases := []struct {
		name, date                 string
		fiscal                     int
		lmFrom, lmTo, lyFrom, lyTo string
		ytd                        string
	}{
		{"31 October against 30 September", "2026-10-31", 1, "2026-09-01", "2026-09-30", "2025-10-01", "2025-10-31", "2026-01-01"},
		{"31 March against 28 February", "2026-03-31", 1, "2026-02-01", "2026-02-28", "2025-03-01", "2025-03-31", "2026-01-01"},
		{"29 February against 28 February of last year", "2028-02-29", 1, "2028-01-01", "2028-01-29", "2027-02-01", "2027-02-28", "2028-01-01"},
		{"1 January looks back to December", "2026-01-01", 1, "2025-12-01", "2025-12-01", "2025-01-01", "2025-01-01", "2026-01-01"},
		{"a fiscal year from July, in September", "2026-09-15", 7, "2026-08-01", "2026-08-15", "2025-09-01", "2025-09-15", "2026-07-01"},
		{"a fiscal year from July, in March", "2026-03-10", 7, "2026-02-01", "2026-02-10", "2025-03-01", "2025-03-10", "2025-07-01"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := flashRanges(civil.MustParseDate(c.date), c.fiscal)
			got := []string{r.LastMonth.From.String(), r.LastMonth.To.String(), r.LastYear.From.String(), r.LastYear.To.String(), r.YTD.From.String()}
			want := []string{c.lmFrom, c.lmTo, c.lyFrom, c.lyTo, c.ytd}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%v, want %v", got, want)
				}
			}
			if r.MTD.To.String() != c.date || r.Today.From.String() != c.date {
				t.Fatalf("mtd/today %v %v", r.MTD, r.Today)
			}
		})
	}
}

func TestFiscalMonthOf(t *testing.T) {
	n, start := fiscalMonthOf(civil.MustParseDate("2026-09-15"), 7)
	if n != 3 || start.String() != "2026-07-01" {
		t.Fatalf("%d %s", n, start)
	}
	n, start = fiscalMonthOf(civil.MustParseDate("2026-03-10"), 7)
	if n != 9 || start.String() != "2025-07-01" {
		t.Fatalf("%d %s", n, start)
	}
	n, _ = fiscalMonthOf(civil.MustParseDate("2026-01-31"), 1)
	if n != 1 {
		t.Fatal(n)
	}
}
