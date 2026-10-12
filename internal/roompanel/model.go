// Package roompanel answers one question for the room panel of the front desk: what is going on in this room today. It owns no table and no rule: every part comes from
// the service of the module that owns it (housekeeping, frontdesk, reservations, folios, the audit trail), read together in one snapshot.
package roompanel

import (
	"time"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/civil"
)

// Access says which parts the caller may read. A part the caller may not read is left out (null) and its flag is false; with the flag true, null means "nothing there"
// (no guest in the room, no folio, no booking to come).
type Access struct {
	Stay    bool `json:"stay"`
	Folio   bool `json:"folio"`
	Arrival bool `json:"arrival"`
	Audit   bool `json:"audit"`
}

// Guest names the guest of a stay.
type Guest struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Stay is the stay that is in the room.
type Stay struct {
	ID                 int64      `json:"id"`
	StayNumber         string     `json:"stay_number"`
	Guest              Guest      `json:"guest"`
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	Source             string     `json:"source,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	// Night is the night the stay is in on the business date (1 on the arrival date), never above Nights; Nights is the length of the stay.
	Night          int    `json:"night"`
	Nights         int    `json:"nights"`
	AdultCount     int    `json:"adult_count"`
	ChildCount     int    `json:"child_count"`
	SpecialRequest string `json:"special_request,omitempty"`
}

// FolioRef is a folio of the stay.
type FolioRef struct {
	ID          int64  `json:"id"`
	FolioNumber string `json:"folio_number"`
	FolioType   string `json:"folio_type"`
	Status      string `json:"status"`
	Balance     string `json:"balance"`
}

// Folio sums the folios of the stay: what was charged, what was paid or credited, and what is left (charged minus paid), as the folio pages state them.
type Folio struct {
	Charges string     `json:"charges"`
	Paid    string     `json:"paid"`
	Balance string     `json:"balance"`
	Folios  []FolioRef `json:"folios"`
}

// HistoryItem is one thing that happened to the room or its stay on the business date: a change of cleaning, or an entry of the audit trail.
type HistoryItem struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // CLEANING or AUDIT
	// Action is the audit action of an AUDIT entry (housekeeping changes are shown as CLEANING, so they appear once).
	Action     string `json:"action,omitempty"`
	EntityType string `json:"entity_type,omitempty"`
	Label      string `json:"label,omitempty"`
	User       string `json:"user,omitempty"`
	// From, To and Notes describe a CLEANING change.
	From  housekeeping.Status `json:"from,omitempty"`
	To    housekeeping.Status `json:"to,omitempty"`
	Notes string              `json:"notes,omitempty"`
}

// Summary is the room panel.
type Summary struct {
	Room         housekeeping.BoardRoom `json:"room"`
	BusinessDate civil.Date             `json:"business_date"`
	Access       Access                 `json:"access"`
	Stay         *Stay                  `json:"stay"`
	Folio        *Folio                 `json:"folio"`
	NextArrival  *frontdesk.RoomArrival `json:"next_arrival"`
	History      []HistoryItem          `json:"history"`
}
