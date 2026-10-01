// Package frontdesk holds the arrival side of a stay: check-in, walk-in and reverse check-in, and the stay
// lists (docs/architecture/04-operations.md §14.3, 06-api.md §13).
package frontdesk

import (
	"slices"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/guests"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations"
	"kamarapms/internal/roomcharge"
)

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

const maxReasonLen = 500

// CheckInInput checks a CONFIRMED reservation room in. RoomID is optional when a room is already assigned.
type CheckInInput struct {
	Version              int32   `json:"version"`
	RoomID               *int64  `json:"room_id"`
	GuestID              int64   `json:"guest_id"`
	AccompanyingGuestIDs []int64 `json:"accompanying_guest_ids"`
	AdultCount           int     `json:"adult_count"`
	ChildCount           int     `json:"child_count"`
	OverrideRoomNotReady bool    `json:"override_room_not_ready"`
	OverrideReason       string  `json:"override_reason"`
}

// WalkInInput creates, confirms, assigns and checks in a room in one transaction. Exactly one of GuestID and
// NewGuest is given.
type WalkInInput struct {
	GuestID              *int64                       `json:"guest_id"`
	NewGuest             *guests.Profile              `json:"new_guest"`
	RoomID               int64                        `json:"room_id"`
	RatePlanID           int64                        `json:"rate_plan_id"`
	DepartureDate        civil.Date                   `json:"departure_date"`
	AdultCount           int                          `json:"adult_count"`
	ChildCount           int                          `json:"child_count"`
	NightlyOverrides     []reservations.NightOverride `json:"nightly_overrides"`
	AccompanyingGuestIDs []int64                      `json:"accompanying_guest_ids"`
	OverrideRoomNotReady bool                         `json:"override_room_not_ready"`
	OverrideReason       string                       `json:"override_reason"`
}

// ReverseInput undoes a check-in of the same business date.
type ReverseInput struct {
	Version int32  `json:"version"`
	Reason  string `json:"reason"`
}

// StayFilter narrows the stay list.
type StayFilter struct {
	Status         string
	DepartureDate  *civil.Date
	DepartureUntil *civil.Date // departure on or before this date (due out and overdue)
	RoomID         *int64
}

func dedupe(ids []int64, without int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id != without && id > 0 && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

// Stay is the stay header.
type Stay struct {
	ID                int64      `json:"id"`
	StayNumber        string     `json:"stay_number"`
	ReservationID     int64      `json:"reservation_id"`
	ReservationRoomID int64      `json:"reservation_room_id"`
	GuestID           int64      `json:"guest_id"`
	ArrivalDate       civil.Date `json:"arrival_date"`
	DepartureDate     civil.Date `json:"departure_date"`
	AdultCount        int        `json:"adult_count"`
	ChildCount        int        `json:"child_count"`
	Status            string     `json:"status"`
	CheckedInAt       time.Time  `json:"actual_check_in_at"`
	CheckedOutAt      *time.Time `json:"actual_check_out_at"`
	Version           int32      `json:"version"`
}

// Segment is a period the stay spent in one room.
type Segment struct {
	ID                int64       `json:"id"`
	RoomID            int64       `json:"room_id"`
	RoomNumber        string      `json:"room_number"`
	CheckInAt         time.Time   `json:"check_in_at"`
	CheckOutAt        *time.Time  `json:"check_out_at"`
	StartBusinessDate civil.Date  `json:"start_business_date"`
	EndBusinessDate   *civil.Date `json:"end_business_date"`
	MoveReason        string      `json:"move_reason,omitempty"`
}

// ReservationRef names the reservation of a stay.
type ReservationRef struct {
	ID                 int64  `json:"id"`
	ConfirmationNumber string `json:"confirmation_number"`
	Status             string `json:"status,omitempty"`
	Version            int32  `json:"version,omitempty"`
}

// CheckInResult is what check-in and walk-in answer.
type CheckInResult struct {
	Reservation *ReservationRef  `json:"reservation,omitempty"`
	Stay        Stay             `json:"stay"`
	StayRoom    Segment          `json:"stay_room"`
	Folio       folios.StayFolio `json:"folio"`
}

// ReverseResult is a reversed check-in.
type ReverseResult struct {
	Stay  Stay             `json:"stay"`
	Folio folios.StayFolio `json:"folio"`
}

// StaySummary is a row of the stay list.
type StaySummary struct {
	ID                 int64      `json:"id"`
	StayNumber         string     `json:"stay_number"`
	Status             string     `json:"status"`
	GuestID            int64      `json:"guest_id"`
	GuestName          string     `json:"guest_name"`
	RoomID             *int64     `json:"room_id"`
	RoomNumber         string     `json:"room_number,omitempty"`
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	AdultCount         int        `json:"adult_count"`
	ChildCount         int        `json:"child_count"`
	Version            int32      `json:"version"`
}

// Arrival is a CONFIRMED room due on a date.
type Arrival struct {
	ReservationID      int64  `json:"reservation_id"`
	ConfirmationNumber string `json:"confirmation_number"`
	ReservationRoomID  int64  `json:"reservation_room_id"`
	ReservationVersion int32  `json:"reservation_version"`
	GuestID            *int64 `json:"guest_id"`
	GuestName          string `json:"guest_name,omitempty"`
	RoomTypeID         int64  `json:"room_type_id"`
	RoomTypeCode       string `json:"room_type_code"`
	RoomID             *int64 `json:"room_id"`
	RoomNumber         string `json:"room_number,omitempty"`
	// HousekeepingStatus is the current status of the assigned room (empty when no room is assigned).
	HousekeepingStatus string     `json:"housekeeping_status,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	AdultCount         int        `json:"adult_count"`
	ChildCount         int        `json:"child_count"`
}

// NightView is a night of the stay's price snapshot with whether it has been charged.
type NightView struct {
	Date       civil.Date `json:"date"`
	Amount     string     `json:"amount"`
	PriceMode  string     `json:"price_mode"`
	IsOverride bool       `json:"is_override"`
	Posted     bool       `json:"posted"`
}

// GuestRef is a guest of the stay.
type GuestRef struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name"`
}

// LineRef is the reservation room a stay belongs to.
type LineRef struct {
	ID                 int64  `json:"id"`
	ReservationID      int64  `json:"reservation_id"`
	ConfirmationNumber string `json:"confirmation_number"`
	RoomTypeCode       string `json:"room_type_code"`
	Status             string `json:"status"`
}

// StayDetail is the full view of a stay.
type StayDetail struct {
	Stay         Stay               `json:"stay"`
	Guest        *GuestRef          `json:"guest"`
	Guests       []GuestRef         `json:"guests"`
	Segments     []Segment          `json:"segments"`
	Line         LineRef            `json:"line"`
	NightlyRates []NightView        `json:"nightly_rates"`
	Folios       []folios.StayFolio `json:"folios"`
}

// MoveInput moves an in-house stay to another room.
type MoveInput struct {
	Version              int32                        `json:"version"`
	RoomID               int64                        `json:"room_id"`
	Reason               string                       `json:"reason"`
	NewNightlyRates      []reservations.NightOverride `json:"new_nightly_rates"`
	OverrideRoomNotReady bool                         `json:"override_room_not_ready"`
	OverrideReason       string                       `json:"override_reason"`
}

// MoveResult is a room move: the stay, the segment that was closed and the one that opened.
type MoveResult struct {
	Stay          Stay    `json:"stay"`
	ClosedSegment Segment `json:"closed_segment"`
	NewSegment    Segment `json:"new_segment"`
}

// ChangeDepartureInput extends, shortens or corrects the departure of an in-house stay.
type ChangeDepartureInput struct {
	Version          int32                        `json:"version"`
	DepartureDate    civil.Date                   `json:"departure_date"`
	NightlyOverrides []reservations.NightOverride `json:"nightly_overrides"`
}

// AddGuestInput adds an accompanying guest.
type AddGuestInput struct {
	GuestID int64 `json:"guest_id"`
}

// CheckOutInput checks an in-house stay out.
type CheckOutInput struct {
	Version               int32 `json:"version"`
	ConfirmEarlyDeparture bool  `json:"confirm_early_departure"`
}

// CheckOutResult is a completed check-out.
type CheckOutResult struct {
	Stay              Stay                 `json:"stay"`
	PostedRoomCharges []roomcharge.Result  `json:"posted_room_charges"`
	Folios            []folios.ClosedFolio `json:"folios"`
	Housekeeping      string               `json:"housekeeping"`
}
