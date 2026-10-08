// Package frontdesk holds the arrival side of a stay: check-in, walk-in and reverse check-in, and the stay
// lists (docs/architecture/04-operations.md §14.3, 06-api.md §13).
package frontdesk

import (
	"slices"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/guests"
	"kamarapms/internal/iam"
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
	OccupancyReason      string                       `json:"occupancy_reason"`       // why the room is free, on a complimentary or house use plan
	RateOverrideReason   string                       `json:"rate_override_reason"`   // why the nightly price is changed (with nightly_overrides)
	RateOverrideApproval *iam.ApprovalInput           `json:"rate_override_approval"` // the approver's credentials, unless the caller can approve
	OccupancyApproval    *iam.ApprovalInput           `json:"occupancy_approval"`     // the manager's credentials for a complimentary or house use room, unless the caller can approve
	ExceedFreeQuota      bool                         `json:"exceed_free_quota"`      // takes the month over the quota of free nights, knowingly
	// RestrictionOverride goes past a sales restriction (stop sell, closed to arrival, a minimum or maximum stay) of the walk-in: a reason and an approval.
	RestrictionOverride  *reservations.RestrictionOverride `json:"restriction_override"`
	AccompanyingGuestIDs []int64                           `json:"accompanying_guest_ids"`
	OverrideRoomNotReady bool                              `json:"override_room_not_ready"`
	OverrideReason       string                            `json:"override_reason"`
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
	// ClosedFolios are the company folios of the stay that were closed with it (open and empty); they stay linked to the cancelled stay as history.
	ClosedFolios []folios.ClosedFolio `json:"closed_folios"`
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

// InHouseRow is a row of the in-house list: an OPEN stay with what the front desk needs to see without opening it. Every figure is read, none is derived from a rate: the price is the night
// of the booking's snapshot, the balances are those of the folios.
type InHouseRow struct {
	ID                 int64            `json:"id"`
	StayNumber         string           `json:"stay_number"`
	Version            int32            `json:"version"`
	ReservationID      int64            `json:"reservation_id"`
	ConfirmationNumber string           `json:"confirmation_number"`
	Guest              InHouseGuest     `json:"guest"`
	Room               InHouseRoom      `json:"room"`
	Company            *InHouseCompany  `json:"company"`
	Billing            []InHouseBilling `json:"billing"`
	Rate               InHouseRate      `json:"rate"`
	Stay               InHouseStay      `json:"stay"`
	Balance            InHouseBalance   `json:"balance"`
	Checkout           InHouseCheckout  `json:"checkout"`
}

// InHouseCheckout is what the front desk can know before a check-out: Status is READY (no known blocker), BALANCE_DUE (the guest folio is not at zero), COMPANY_BILL (a company folio is not at
// zero), CHARGES_PENDING (room nights are not charged yet, so the folio will change) or FOLIO_ISSUE (no folio, or a closed guest folio). The check-out itself applies every rule; a zero balance
// is no promise that it goes through.
type InHouseCheckout struct {
	Status          string `json:"status"`
	UnchargedNights int    `json:"uncharged_nights"`
}

// InHouseGuest is the guest of the stay.
type InHouseGuest struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// InHouseRoom is the room the guest is in now, with its type.
type InHouseRoom struct {
	ID           int64  `json:"id"`
	Number       string `json:"number"`
	RoomTypeCode string `json:"room_type_code"`
	RoomTypeName string `json:"room_type_name"`
}

// InHouseCompany is the company that pays for the stay (the one its billing instructions name, else the one of a company folio).
type InHouseCompany struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// InHouseBilling is one billing instruction of the stay: what the company pays (ALL, ROOM, or one charge code).
type InHouseBilling struct {
	Scope       string `json:"scope"`
	ChargeCode  string `json:"charge_code,omitempty"`
	CompanyID   int64  `json:"company_id"`
	CompanyName string `json:"company_name"`
}

// InHouseRate is the price of the night the stay is in, from the snapshot of the booking.
type InHouseRate struct {
	RatePlanCode string `json:"rate_plan_code"`
	RatePlanName string `json:"rate_plan_name"`
	Amount       string `json:"amount"`
	PriceMode    string `json:"price_mode"`
	IsOverride   bool   `json:"is_override"`
}

// InHouseStay is the dates and the party.
type InHouseStay struct {
	Arrival   civil.Date `json:"arrival_date"`
	Departure civil.Date `json:"departure_date"`
	Nights    int        `json:"nights"`
	Adults    int        `json:"adults"`
	Children  int        `json:"children"`
}

// InHouseBalance is what the stay owes. Amount is the sum of the balances of its folios (the guest folio and the company folios), each of which is given in Folios with its own balance;
// Status is SETTLED (zero), OUTSTANDING (above zero) or CREDIT (below zero) for that sum, or NO_FOLIO when the stay has no folio (Amount is then empty: no folio is not a zero balance).
type InHouseBalance struct {
	Amount string             `json:"amount"`
	Status string             `json:"status"`
	Folios []folios.StayFolio `json:"folios"`
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
	// RequestedBedType is the bed type the guest asked for; RoomBedType is the bed type of the assigned room (each empty when none).
	RequestedBedTypeID   *int64 `json:"requested_bed_type_id"`
	RequestedBedTypeCode string `json:"requested_bed_type_code,omitempty"`
	// BedLocked says the guest keeps the requested bed: the room must have it.
	BedLocked       bool   `json:"bed_locked"`
	RoomBedTypeCode string `json:"room_bed_type_code,omitempty"`
	// HousekeepingStatus is the current status of the assigned room (empty when no room is assigned).
	HousekeepingStatus string     `json:"housekeeping_status,omitempty"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	AdultCount         int        `json:"adult_count"`
	ChildCount         int        `json:"child_count"`
	// Status is the status of the reservation room (CONFIRMED, CHECKED_IN, CANCELLED, NO_SHOW); ReservationStatus is the one of the reservation.
	Status            string `json:"status"`
	ReservationStatus string `json:"reservation_status"`
	RoomTypeName      string `json:"room_type_name"`
	// Rate is the booked night of the arrival date (the snapshot of the booking); Amount is empty when the night has no snapshot.
	Rate ArrivalRate `json:"rate"`
	// Company is the company of the first billing instruction of the line (nil: the guest pays).
	Company *InHouseCompany `json:"company"`
	// Deposit is what the reservation holds before check-in (nil: no deposit folio).
	Deposit *folios.ReservationDeposit `json:"deposit"`
	// Readiness says what stands in the way of a check-in, with the rules the check-in itself applies; the check-in still validates.
	Readiness Readiness `json:"readiness"`
}

// ArrivalRate is the rate plan of the booking and the price of its arrival night.
type ArrivalRate struct {
	RatePlanCode string `json:"rate_plan_code"`
	RatePlanName string `json:"rate_plan_name"`
	Amount       string `json:"amount"`
	PriceMode    string `json:"price_mode"`
}

// Readiness of an arrival: READY, or BLOCKED with the blockers. A blocker is one of NOT_BUSINESS_DATE, GUEST_MISSING, ROOM_NOT_ASSIGNED, ROOM_NOT_READY, ROOM_OCCUPIED, ROOM_BLOCKED, ROOM_NOT_AVAILABLE.
// It is not a status of the reservation. NOT_READY and NOT_ASSIGNED can be dealt with at the check-in itself (the room is chosen there, an unready room can be overridden with the right).
type Readiness struct {
	Status   string   `json:"status"`
	Blockers []string `json:"blockers"`
}

// ArrivalFilter narrows the arrivals of a date: the status of the reservation room (CONFIRMED when empty), a room type, and a search of the guest, the confirmation number and the room.
type ArrivalFilter struct {
	Status     string
	RoomTypeID *int64
	Q          string
}

// InHouseFilter narrows the in-house list; the departures list is the in-house list that leaves on or before a date.
type InHouseFilter struct {
	DepartureDate  *civil.Date
	DepartureUntil *civil.Date
	RoomTypeID     *int64
	Q              string
}

// NightView is a night of the stay's price snapshot with whether it has been charged.
type NightView struct {
	Date       civil.Date `json:"date"`
	Amount     string     `json:"amount"`
	PriceMode  string     `json:"price_mode"`
	IsOverride bool       `json:"is_override"`
	Posted     bool       `json:"posted"`
	// PostedOn is the business date the night was charged on (nil: not charged). Status is OPEN (not charged), POSTED (charged on the open business day) or CLOSED (charged on a day that has closed):
	// a charged night is corrected with an adjustment, never by changing the charge.
	PostedOn *civil.Date `json:"posted_on"`
	Status   string      `json:"status"`
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

// ChangeRatesInput changes the rate of the nights of an in-house stay. ApplyTo is NIGHT (the night of Date) or REMAINING (Date and every later night that is not charged yet: a charged night is
// never changed in bulk). A night that is already charged is corrected by an adjustment, which needs the approval of a correction.
type ChangeRatesInput struct {
	Version  int32              `json:"version"`
	ApplyTo  string             `json:"apply_to"`
	Date     civil.Date         `json:"date"`
	Amount   string             `json:"amount"`
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// RateChange is one night whose rate changed. Charged says the night was already charged: then the ledger got an adjustment (AdjustmentItemID, on FolioID) for the difference.
type RateChange struct {
	Date             civil.Date `json:"date"`
	OldAmount        string     `json:"old_amount"`
	NewAmount        string     `json:"new_amount"`
	PriceMode        string     `json:"price_mode"`
	Charged          bool       `json:"charged"`
	FolioID          *int64     `json:"folio_id"`
	AdjustmentItemID *int64     `json:"adjustment_item_id"`
}

// ChangeRatesResult is the stay after a rate change and the nights that changed.
type ChangeRatesResult struct {
	Stay    Stay         `json:"stay"`
	Changes []RateChange `json:"changes"`
}

// MoveResult is a room move: the stay, the segment that was closed and the one that opened.
type MoveResult struct {
	Stay          Stay    `json:"stay"`
	ClosedSegment Segment `json:"closed_segment"`
	NewSegment    Segment `json:"new_segment"`
}

// ChangeDepartureInput extends, shortens or corrects the departure of an in-house stay.
type ChangeDepartureInput struct {
	Version              int32                        `json:"version"`
	DepartureDate        civil.Date                   `json:"departure_date"`
	NightlyOverrides     []reservations.NightOverride `json:"nightly_overrides"`
	RateOverrideReason   string                       `json:"rate_override_reason"`
	RateOverrideApproval *iam.ApprovalInput           `json:"rate_override_approval"`
	// RestrictionOverride goes past a sales restriction of the extra nights (stop sell, closed to departure, the maximum stay): a reason and an approval.
	RestrictionOverride *reservations.RestrictionOverride `json:"restriction_override"`
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
