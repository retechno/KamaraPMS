package availability_test

import (
	"math/rand"
	"testing"

	"kamarapms/internal/availability"
	"kamarapms/internal/platform/civil"
)

// A case is one room type on one night: rooms (each with a bed, some blocked), the demand that already sits in a room (fixed),
// the demand that keeps a bed (locked) and the demand with no preference (any).
type bedCase struct {
	beds    []int  // bed of each room
	blocked []bool // a blocked room is outside the stock
	fixed   []int  // room index of each fixed demand (a free room, one demand per room)
	locked  []int  // bed of each locked demand
	any     int
}

// feasible is the brute force: can every demand that has no room be given a distinct free room, a locked one a room of its bed?
func (c bedCase) feasible() bool {
	taken := make([]bool, len(c.beds))
	copy(taken, c.blocked)
	for _, r := range c.fixed {
		taken[r] = true // a fixed demand sits in its room
	}
	var assign func(i int) bool
	// the locked demands first, then those with no preference: try every free room
	needs := make([]int, 0, len(c.locked)+c.any)
	needs = append(needs, c.locked...)
	for i := 0; i < c.any; i++ {
		needs = append(needs, 0) // 0: any bed
	}
	assign = func(i int) bool {
		if i == len(needs) {
			return true
		}
		for r := range c.beds {
			if taken[r] || (needs[i] != 0 && c.beds[r] != needs[i]) {
				continue
			}
			taken[r] = true
			if assign(i + 1) {
				taken[r] = false
				return true
			}
			taken[r] = false
		}
		return false
	}
	return assign(0)
}

// counted is the engine's view of the same case: the stock of every bed and of the type, and the extra demand to test.
func (c bedCase) counted(extraBed int) []availability.Shortfall {
	day := civil.MustParseDate("2026-10-01")
	typeID := int64(1)
	stock := map[availability.BedKey]map[civil.Date]availability.BedStock{}
	for i, b := range c.beds {
		k := availability.BedKey{RoomTypeID: typeID, BedTypeID: int64(b)}
		if stock[k] == nil {
			stock[k] = map[civil.Date]availability.BedStock{}
		}
		s := stock[k][day]
		if !c.blocked[i] {
			s.Sellable++
		}
		stock[k][day] = s
	}
	sellable := 0
	for i := range c.beds {
		if !c.blocked[i] {
			sellable++
		}
	}
	for _, r := range c.fixed {
		k := availability.BedKey{RoomTypeID: typeID, BedTypeID: int64(c.beds[r])}
		s := stock[k][day]
		s.Fixed++
		stock[k][day] = s
	}
	for _, b := range c.locked {
		k := availability.BedKey{RoomTypeID: typeID, BedTypeID: int64(b)}
		s := stock[k][day]
		s.Locked++
		stock[k][day] = s
	}
	demand := len(c.fixed) + len(c.locked) + c.any
	inv := map[int64]map[civil.Date]availability.Night{typeID: {day: {Date: day, Sellable: sellable, Demand: demand, Available: sellable - demand}}}
	d := availability.NewDemand()
	d.Add(typeID, int64(extraBed), day, day.AddDays(1), 1)
	return availability.FindDemandShortfalls(inv, stock, d)
}

// TestBedLinesMatchBruteForceMatching proves the two counts of the rule (fixed_B + locked_B <= R_B for every bed, and
// fixed + locked + any <= R for the type) against a brute-force matching of demand to rooms on random small cases.
func TestBedLinesMatchBruteForceMatching(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	checked, fitting, refusing := 0, 0, 0
	for checked < 4000 {
		rooms := 1 + rng.Intn(6)
		c := bedCase{beds: make([]int, rooms), blocked: make([]bool, rooms)}
		for i := range c.beds {
			c.beds[i] = 1 + rng.Intn(3)
			c.blocked[i] = rng.Intn(5) == 0
		}
		var free []int
		for i := range c.beds {
			if !c.blocked[i] {
				free = append(free, i)
			}
		}
		rng.Shuffle(len(free), func(i, j int) { free[i], free[j] = free[j], free[i] })
		c.fixed = free[:rng.Intn(len(free)+1)]
		for i, n := 0, rng.Intn(4); i < n; i++ {
			c.locked = append(c.locked, 1+rng.Intn(3))
		}
		c.any = rng.Intn(3)
		if !c.feasible() { // the demand that exists is always matched (the engine never lets it be otherwise)
			continue
		}
		checked++
		// the extra demand: a locked booking of a bed, or a booking with no preference
		for _, extraBed := range []int{0, 1, 2, 3} {
			probe := c
			if extraBed == 0 {
				probe.any++
			} else {
				probe.locked = append(append([]int(nil), c.locked...), extraBed)
			}
			want := probe.feasible()
			got := len(c.counted(extraBed)) == 0
			if got != want {
				t.Fatalf("case %+v, extra bed %d: the counts say fits=%v, the matching says %v", c, extraBed, got, want)
			}
			if want {
				fitting++
			} else {
				refusing++
			}
		}
	}
	if fitting < 500 || refusing < 500 {
		t.Fatalf("the random cases must cover both answers: %d fit, %d refused", fitting, refusing)
	}
}

func TestFindBedShortfallsPure(t *testing.T) {
	night := civil.MustParseDate("2026-10-01")
	k := availability.BedKey{RoomTypeID: 1, BedTypeID: 7}
	stock := map[availability.BedKey]map[civil.Date]availability.BedStock{k: {night: {Sellable: 2, Fixed: 1, Locked: 0}}}
	extra := availability.BedExtra{}
	extra.Add(k, night, night.AddDays(1), 1)
	if got := availability.FindBedShortfalls(stock, extra); len(got) != 0 {
		t.Fatalf("one King is left: %+v", got)
	}
	extra.Add(k, night, night.AddDays(1), 1)
	got := availability.FindBedShortfalls(stock, extra)
	if len(got) != 1 || got[0].BedTypeID == nil || *got[0].BedTypeID != 7 || got[0].Available != 1 || got[0].Requested != 2 {
		t.Fatalf("two Kings asked, one left: %+v", got)
	}
	// a variant without any row has no rooms
	other := availability.BedExtra{}
	other.Add(availability.BedKey{RoomTypeID: 1, BedTypeID: 9}, night, night.AddDays(1), 1)
	if got := availability.FindBedShortfalls(stock, other); len(got) != 1 || got[0].Available != 0 {
		t.Fatalf("no rooms with the bed: %+v", got)
	}
}
