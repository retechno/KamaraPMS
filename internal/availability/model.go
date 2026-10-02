// Package availability is the inventory engine: how many rooms of each type can still be sold on each night,
// whether a specific room is free, and the availability search.
//
// Nothing here is stored. sellable, demand and available are derived from rooms, blocks, CONFIRMED reservation
// lines and open stays (docs/architecture/04-operations.md §14.1):
//
//	available(T, n) = sellable(T, n) − demand(T, n),  and no operation may make it negative.
//
// The engine only reads. Writers (reservations, room blocks, room changes) take the locks of the global lock
// order first (business day, room types, rooms, reservations) and then ask it, so the answer cannot change
// under them.
package availability

import (
	"sort"

	"kamarapms/internal/platform/civil"
)

// MaxSearchNights bounds a search or a stay.
const MaxSearchNights = 365

// Night is the inventory of one room type on one night.
type Night struct {
	Date      civil.Date `json:"date"`
	Sellable  int        `json:"sellable"`
	Demand    int        `json:"demand"`
	Available int        `json:"available"`
}

// Shortfall is a night on which a room type cannot take the requested rooms.
type Shortfall struct {
	RoomTypeID int64      `json:"room_type_id"`
	Date       civil.Date `json:"date"`
	Available  int        `json:"available"`
	Requested  int        `json:"requested"`
}

// Extra is additional demand to test against the inventory: rooms per room type per night.
type Extra map[int64]map[civil.Date]int

// Add records count more rooms of a type on every night of [from, to).
func (e Extra) Add(roomTypeID int64, from, to civil.Date, count int) {
	if e[roomTypeID] == nil {
		e[roomTypeID] = map[civil.Date]int{}
	}
	for d := from; d.Before(to); d = d.AddDays(1) {
		e[roomTypeID][d] += count
	}
}

// Dates lists every night mentioned, ascending.
func (e Extra) Dates() []civil.Date {
	seen := map[civil.Date]bool{}
	for _, nights := range e {
		for d := range nights {
			seen[d] = true
		}
	}
	out := make([]civil.Date, 0, len(seen))
	for d := range seen {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// TypeIDs lists the room types mentioned, ascending.
func (e Extra) TypeIDs() []int64 {
	out := make([]int64, 0, len(e))
	for id := range e {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// FindShortfalls returns the nights (ascending, per room type) on which the extra demand does not fit,
// given the current inventory. Pure: inventory maps a room type to its nights.
func FindShortfalls(inventory map[int64]map[civil.Date]Night, extra Extra) []Shortfall {
	var out []Shortfall
	for _, typeID := range extra.TypeIDs() {
		for _, d := range extraDates(extra[typeID]) {
			need := extra[typeID][d]
			if need <= 0 {
				continue
			}
			n := inventory[typeID][d] // a night without inventory rows has nothing available
			if n.Available < need {
				out = append(out, Shortfall{RoomTypeID: typeID, Date: d, Available: n.Available, Requested: need})
			}
		}
	}
	return out
}

func extraDates(m map[civil.Date]int) []civil.Date {
	out := make([]civil.Date, 0, len(m))
	for d := range m {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// FitsOccupancy reports whether a party fits a room type's limits.
func FitsOccupancy(adults, children, maxAdult, maxChild, maxOccupancy int) bool {
	return adults >= 1 && children >= 0 && adults <= maxAdult && children <= maxChild && adults+children <= maxOccupancy
}

// RoomIssue explains why a specific room is not free.
type RoomIssue struct {
	Kind string     `json:"kind"` // INACTIVE, BLOCKED, RESERVED or OCCUPIED
	ID   int64      `json:"id,omitempty"`
	From civil.Date `json:"from,omitempty"`
	To   civil.Date `json:"to,omitempty"` // exclusive
}

// Issue kinds.
const (
	IssueInactive = "INACTIVE"
	IssueBlocked  = "BLOCKED"
	IssueReserved = "RESERVED"
	IssueOccupied = "OCCUPIED"
)
