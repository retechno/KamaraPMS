package banquet

import (
	"slices"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// The pure rules of the reports of 36-banquet.md 4.10: nothing here reads the database.

// DefaultOperatingFrom and DefaultOperatingTo are the hours of operation of a property that set none (07:00 to 23:00), in minutes: the hours the occupancy of a room is counted over.
const (
	DefaultOperatingFrom = 7 * 60
	DefaultOperatingTo   = 23 * 60
)

// Span is a time of a day in minutes from midnight: [Start, End).
type Span struct{ Start, End int }

func minutesOf(t civil.TimeOfDay) int { return t.Hour()*60 + t.Minute() }

// SessionSpan is the time a session holds its room, from its start to its end (the end of a session that ends the next day is counted from the midnight of its own date).
func SessionSpan(start, end civil.TimeOfDay, endsNextDay bool) Span {
	s, e := minutesOf(start), minutesOf(end)
	if endsNextDay {
		e += 24 * 60
	}
	return Span{Start: s, End: e}
}

// OccupiedMinutes is the length of the union of the spans inside the window of operation: two sessions that overlap (a combination and one of its parts, say) count once.
func OccupiedMinutes(spans []Span, from, to int) int {
	clipped := make([]Span, 0, len(spans))
	for _, sp := range spans {
		s, e := max(sp.Start, from), min(sp.End, to)
		if e > s {
			clipped = append(clipped, Span{s, e})
		}
	}
	slices.SortFunc(clipped, func(a, b Span) int { return a.Start - b.Start })
	total, cur := 0, -1
	for _, sp := range clipped {
		s := max(sp.Start, cur)
		if sp.End > s {
			total += sp.End - s
			cur = sp.End
		}
	}
	return total
}

// Blockers answers, for each room, the rooms whose sessions block it. A combination holds its parts, so two rooms block each other when the base rooms they hold have one in common: a room and the
// combinations it is a part of, a combination and its parts, and two combinations that share a part (the Ballroom A+B and the Garden B+C).
func Blockers(rooms []Room) map[int64][]int64 {
	base := make(map[int64]map[int64]bool, len(rooms))
	for _, r := range rooms {
		set := map[int64]bool{}
		if r.IsCombo && len(r.Parts) > 0 {
			for _, p := range r.Parts {
				set[p.RoomID] = true
			}
		} else {
			set[r.ID] = true
		}
		base[r.ID] = set
	}
	out := make(map[int64][]int64, len(rooms))
	for _, r := range rooms {
		for _, q := range rooms {
			for b := range base[r.ID] {
				if base[q.ID][b] {
					out[r.ID] = append(out[r.ID], q.ID)
					break
				}
			}
		}
	}
	return out
}

// Percent is num over den as a percentage rounded to one place, nil when there is nothing to divide by.
func Percent(num, den decimal.Decimal) *decimal.Decimal {
	if !den.IsPositive() {
		return nil
	}
	v := num.Mul(hundred).Div(den).Round(1)
	return &v
}

// Conversion is the share of the decided inquiries that became definite: definite / (definite + lost). Events still open are in neither.
func Conversion(definite, lost int) *decimal.Decimal {
	return Percent(decimal.NewFromInt(int64(definite)), decimal.NewFromInt(int64(definite+lost)))
}
