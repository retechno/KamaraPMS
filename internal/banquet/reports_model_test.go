package banquet_test

import (
	"slices"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
)

func TestTheOccupancyCountsOverlappingSessionsOnceInsideTheHoursOfOperation(t *testing.T) {
	spans := []banquet.Span{{Start: 12 * 60, End: 16 * 60}, {Start: 14 * 60, End: 18 * 60}, {Start: 6 * 60, End: 8 * 60}, {Start: 22 * 60, End: 26 * 60}}
	// 07:00-23:00: 12-18 is 6 hours, 07-08 is 1 hour, 22-23 is 1 hour (the part after 23:00 and before 07:00 is outside)
	if got := banquet.OccupiedMinutes(spans, 7*60, 23*60); got != 8*60 {
		t.Fatalf("occupied minutes %d, want %d", got, 8*60)
	}
	if got := banquet.OccupiedMinutes(nil, 7*60, 23*60); got != 0 {
		t.Fatalf("no session, no minutes: %d", got)
	}
	sp := banquet.SessionSpan(*tod("20:00"), *tod("02:00"), true)
	if sp.Start != 20*60 || sp.End != 26*60 {
		t.Fatalf("a session after midnight ends on the next day: %+v", sp)
	}
}

func TestRoomsBlockEachOtherWhenTheyShareABaseRoom(t *testing.T) {
	rooms := []banquet.Room{{ID: 1}, {ID: 2}, {ID: 3},
		{ID: 4, IsCombo: true, Parts: []banquet.RoomPart{{RoomID: 1}, {RoomID: 2}}}, {ID: 5, IsCombo: true, Parts: []banquet.RoomPart{{RoomID: 2}, {RoomID: 3}}}}
	b := banquet.Blockers(rooms)
	for room, want := range map[int64][]int64{1: {1, 4}, 2: {2, 4, 5}, 3: {3, 5}, 4: {1, 2, 4, 5}, 5: {2, 3, 4, 5}} {
		got := slices.Clone(b[room])
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Errorf("room %d is blocked by %v, want %v", room, got, want)
		}
	}
}

func TestConversionIsDefiniteOverDefinitePlusLost(t *testing.T) {
	if got := banquet.Conversion(2, 1); got == nil || !got.Equal(decimal.RequireFromString("66.7")) {
		t.Fatalf("conversion %v, want 66.7", got)
	}
	if banquet.Conversion(0, 0) != nil {
		t.Fatal("nothing decided, no conversion")
	}
	if got := banquet.Conversion(3, 0); got == nil || !got.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("conversion %v, want 100", got)
	}
}

func TestTheQuantityTheStockTakesOutOfALine(t *testing.T) {
	line := func(basis, q string) banquet.Line { return banquet.Line{Basis: basis, Quantity: dec(q)} }
	comp := &banquet.LineComponent{QtyPerPax: dec("2")}
	for _, c := range []struct {
		name string
		l    banquet.Line
		c    *banquet.LineComponent
		want string
	}{
		{"a component of a package: guests x quantity per guest", line("PER_PAX", "1"), comp, "160"},
		{"an item per guest: guests x quantity", line("PER_PAX", "1.5"), nil, "120"},
		{"an item per session: the quantity", line("PER_SESSION", "3"), nil, "3"},
		{"an item per unit: the quantity", line("PER_UNIT", "7"), nil, "7"},
		{"an item per hour is a service and takes nothing", line("PER_HOUR", "4"), nil, "0"},
	} {
		if got := banquet.StockQty(c.l, c.c, 80); !got.Equal(dec(c.want)) {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
}
