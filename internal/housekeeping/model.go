// Package housekeeping owns the current cleaning state of each room and its
// change history. It is housekeeping only: whether a room is occupied is derived
// from stays and reservations and is never stored here.
package housekeeping

import (
	"time"

	"kamarapms/internal/platform/civil"
)

// Status is a room's housekeeping state.
type Status string

const (
	Clean     Status = "CLEAN"
	Dirty     Status = "DIRTY"
	Cleaning  Status = "CLEANING"
	Inspected Status = "INSPECTED"
)

// Statuses lists every status in board order.
var Statuses = []Status{Dirty, Cleaning, Clean, Inspected}

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case Clean, Dirty, Cleaning, Inspected:
		return true
	}
	return false
}

// Source says what caused a status change (housekeeping_logs.source).
type Source string

const (
	SourceManual          Source = "MANUAL"
	SourceCheckOut        Source = "CHECK_OUT"
	SourceRoomMove        Source = "ROOM_MOVE"
	SourceNightAudit      Source = "NIGHT_AUDIT"
	SourceCheckInReversal Source = "CHECK_IN_REVERSAL"
)

// transitions is the legal manual state machine (docs/architecture/04-operations.md §14.4):
// DIRTY→CLEANING→CLEAN→INSPECTED, DIRTY→CLEAN, and any→DIRTY.
var transitions = map[Status][]Status{
	Dirty:     {Cleaning, Clean},
	Cleaning:  {Clean, Dirty},
	Clean:     {Inspected, Dirty},
	Inspected: {Dirty},
}

// CanTransition reports whether from→to is a legal change. Staying in the same
// status is not a change.
func CanTransition(from, to Status) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// NextStatuses lists the statuses reachable from s.
func NextStatuses(s Status) []Status { return append([]Status(nil), transitions[s]...) }

// State is a room's current housekeeping status.
type State struct {
	RoomID    int64     `json:"room_id"`
	Status    Status    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Log is one recorded status change.
type Log struct {
	ID           int64      `json:"id"`
	RoomID       int64      `json:"room_id"`
	FromStatus   Status     `json:"from_status"`
	ToStatus     Status     `json:"to_status"`
	Source       Source     `json:"source"`
	BusinessDate civil.Date `json:"business_date"`
	Notes        string     `json:"notes,omitempty"`
	ChangedAt    time.Time  `json:"changed_at"`
	ChangedBy    *int64     `json:"changed_by,omitempty"`
}

// Occupancy is derived, never stored.
type Occupancy string

const (
	Occupied Occupancy = "OCCUPIED"
	Reserved Occupancy = "RESERVED"
	Vacant   Occupancy = "VACANT"
)

// BoardBlock is the active OOO/OOS block covering the business date.
type BoardBlock struct {
	Type    string     `json:"type"`
	EndDate civil.Date `json:"end_date"`
}

// BoardRoom is one row of the housekeeping board.
type BoardRoom struct {
	RoomID           int64       `json:"room_id"`
	RoomNumber       string      `json:"room_number"`
	Floor            string      `json:"floor,omitempty"`
	Building         string      `json:"building,omitempty"`
	RoomTypeID       int64       `json:"room_type_id"`
	RoomTypeCode     string      `json:"room_type_code"`
	RoomTypeName     string      `json:"room_type_name"`
	Status           Status      `json:"status"`
	StatusUpdatedAt  time.Time   `json:"status_updated_at"`
	Occupancy        Occupancy   `json:"occupancy"`
	Block            *BoardBlock `json:"block,omitempty"`
	AllowedNextState []Status    `json:"allowed_next"`
	// Flags (see SetFlags).
	Priority        string `json:"priority"`
	DND             bool   `json:"dnd"`
	MakeUpRequested bool   `json:"make_up_requested"`
	FlagNote        string `json:"flag_note,omitempty"`
}
