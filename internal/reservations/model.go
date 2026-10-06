// Package reservations holds bookings: the reservation header, its room lines and their nightly price
// snapshots (docs/architecture/04-operations.md §14.2, 06-api.md §12).
package reservations

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/availability"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// Header statuses.
const (
	StatusDraft     = "DRAFT"
	StatusConfirmed = "CONFIRMED"
	StatusCancelled = "CANCELLED"
)

// Line statuses (the header statuses above plus the arrival lifecycle).
const (
	LineDraft      = "DRAFT"
	LineConfirmed  = "CONFIRMED"
	LineCheckedIn  = "CHECKED_IN"
	LineCompleted  = "COMPLETED"
	LineCancelled  = "CANCELLED"
	LineNoShow     = "NO_SHOW"
	maxReasonLen   = 500
	maxBulkNoShow  = 500
	maxTextLen     = 2000
	maxLinesPerRes = 50
)

// Display statuses derived from the header and its lines.
const (
	DisplayDraft      = "DRAFT"
	DisplayConfirmed  = "CONFIRMED"
	DisplayInHouse    = "IN_HOUSE"
	DisplayCheckedOut = "CHECKED_OUT"
	DisplayNoShow     = "NO_SHOW"
	DisplayCancelled  = "CANCELLED"
)

var sources = []string{"WALK_IN", "PHONE", "EMAIL", "WEBSITE", "OTA", "AGENT", "OTHER"}

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

// NightOverride replaces the grid price of one night (needs reservation.override_rate). Amount is the agreed
// price; DiscountAmount is informational (how much below the grid it is meant to be) and is stored as given.
type NightOverride struct {
	Date           civil.Date `json:"date"`
	Amount         string     `json:"amount"`
	DiscountAmount string     `json:"discount_amount,omitempty"`
}

// LineInput is one room line of a booking.
type LineInput struct {
	RoomTypeID int64      `json:"room_type_id"`
	RatePlanID int64      `json:"rate_plan_id"`
	Arrival    civil.Date `json:"arrival_date"`
	Departure  civil.Date `json:"departure_date"`
	Adults     int        `json:"adult_count"`
	Children   int        `json:"child_count"`
	GuestID    *int64     `json:"guest_id,omitempty"`
	RoomID     *int64     `json:"room_id,omitempty"`
	// BedTypeID is the bed type the guest asks for: a request, not a reservation of inventory.
	BedTypeID *int64 `json:"bed_type_id,omitempty"`
	// BedLocked keeps the bed: the line then needs a room with that bed and uses the stock of the variant. Needs BedTypeID.
	BedLocked bool `json:"bed_locked,omitempty"`
	// OccupancyReason is why the room is given free; required (and only kept) when the rate plan is COMPLIMENTARY or HOUSE_USE.
	OccupancyReason string          `json:"occupancy_reason,omitempty"`
	Overrides       []NightOverride `json:"nightly_overrides,omitempty"`
	// RateOverrideReason and RateOverrideApproval justify the overrides when the line is added to a reservation (creating a
	// reservation carries them on the CreateInput instead): see requireRateOverrideApproval.
	RateOverrideReason   string             `json:"rate_override_reason,omitempty"`
	RateOverrideApproval *iam.ApprovalInput `json:"rate_override_approval,omitempty"`
	// OccupancyApproval and ExceedFreeQuota are the approval of a complimentary or house use room added to a reservation (see
	// CreateInput).
	OccupancyApproval *iam.ApprovalInput `json:"occupancy_approval,omitempty"`
	ExceedFreeQuota   bool               `json:"exceed_free_quota,omitempty"`
	// RestrictionOverride goes past a sales restriction of the line added to a reservation (see CreateInput).
	RestrictionOverride *RestrictionOverride `json:"restriction_override,omitempty"`
}

// CreateInput creates a draft, optionally confirming it in the same transaction.
type CreateInput struct {
	GuestID        *int64      `json:"guest_id,omitempty"`
	Source         string      `json:"source"`
	Market         string      `json:"market,omitempty"`
	SpecialRequest string      `json:"special_request,omitempty"`
	Remarks        string      `json:"remarks,omitempty"`
	CompanyID      *int64      `json:"company_id,omitempty"`
	BookingGroupID *int64      `json:"booking_group_id,omitempty"`
	Rooms          []LineInput `json:"rooms"`
	Confirm        bool        `json:"confirm"`
	// RateOverrideReason and RateOverrideApproval justify the nightly rate overrides of the lines: a reason, and the approver's
	// credentials unless the person holds reservation.override_rate_approve. The approval is never stored.
	RateOverrideReason   string             `json:"rate_override_reason,omitempty"`
	RateOverrideApproval *iam.ApprovalInput `json:"rate_override_approval,omitempty"`
	// OccupancyApproval is the approval of the complimentary and house use rooms of the request: the credentials of a user
	// holding reservation.complimentary_approve (not needed when the caller holds it). ExceedFreeQuota says the request
	// knowingly takes a month over the quota of free nights.
	OccupancyApproval *iam.ApprovalInput `json:"occupancy_approval,omitempty"`
	ExceedFreeQuota   bool               `json:"exceed_free_quota,omitempty"`
	// RestrictionOverride goes past the sales restrictions of the rooms of the request: a reason and an approval (docs/architecture/18-architecture-decisions.md). Never for a web or OTA booking.
	RestrictionOverride *RestrictionOverride `json:"restriction_override,omitempty"`
}

// Hash identifies the request body for idempotent replays.
func (in CreateInput) Hash() string {
	in.RateOverrideApproval, in.OccupancyApproval = nil, nil // credentials are never part of what is stored
	if in.RestrictionOverride != nil {
		ro := *in.RestrictionOverride
		ro.Approval = nil
		in.RestrictionOverride = &ro
	}
	b, _ := json.Marshal(in) //nolint:errchkjson // plain structs cannot fail to marshal
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HeaderPatch changes header fields. Nil leaves a field unchanged.
type HeaderPatch struct {
	Version        int32
	GuestID        *int64
	Source         *string
	Market         *string
	SpecialRequest *string
	Remarks        *string
	// CompanyID and BookingGroupID: nil leaves the link, a value below 1 clears it.
	CompanyID      *int64
	BookingGroupID *int64
}

// LinePatch amends a line. Nil leaves a field unchanged; Overrides replace the price of the listed nights.
type LinePatch struct {
	Version    int32
	Arrival    *civil.Date
	Departure  *civil.Date
	RoomTypeID *int64
	RatePlanID *int64
	Adults     *int
	Children   *int
	// BedTypeID changes the requested bed type; 0 takes the request off.
	BedTypeID *int64
	// BedLocked locks or unlocks the requested bed; taking the request off (BedTypeID 0) unlocks it.
	BedLocked *bool
	// OccupancyReason changes the reason of a complimentary or house use room.
	OccupancyReason *string
	Overrides       []NightOverride
	// RateOverrideReason and RateOverrideApproval justify the overrides (see CreateInput).
	RateOverrideReason   string
	RateOverrideApproval *iam.ApprovalInput
	// OccupancyApproval and ExceedFreeQuota: see CreateInput (moving a line to a free plan, or changing the dates of one).
	OccupancyApproval *iam.ApprovalInput
	ExceedFreeQuota   bool
	// RestrictionOverride goes past the sales restriction of changed dates, room type or rate plan (see CreateInput).
	RestrictionOverride *RestrictionOverride
}

// ListFilter narrows the reservation list.
type ListFilter struct {
	ArrivalFrom *civil.Date
	ArrivalTo   *civil.Date
	Status      string
	Query       string
	CompanyID   *int64
	GroupID     *int64
}

func (in CreateInput) validateHeader() []apperr.FieldError {
	var f []apperr.FieldError
	f = append(f, validateSource(in.Source, "source")...)
	f = append(f, validateText("market", in.Market, 30)...)
	f = append(f, validateText("special_request", in.SpecialRequest, maxTextLen)...)
	f = append(f, validateText("remarks", in.Remarks, maxTextLen)...)
	switch {
	case len(in.Rooms) == 0:
		f = append(f, fieldErr("rooms", "REQUIRED", "at least one room"))
	case len(in.Rooms) > maxLinesPerRes:
		f = append(f, fieldErr("rooms", "TOO_MANY", "at most 50 rooms per reservation"))
	}
	if in.Confirm && in.GuestID == nil {
		f = append(f, fieldErr("guest_id", "BOOKER_REQUIRED", "a confirmed reservation needs a booker"))
	}
	return f
}

func validateSource(s, field string) []apperr.FieldError {
	if !slices.Contains(sources, s) {
		return []apperr.FieldError{fieldErr(field, "INVALID_VALUE", "one of "+strings.Join(sources, ", "))}
	}
	return nil
}

func validateText(field, v string, max int) []apperr.FieldError {
	if len([]rune(v)) > max {
		return []apperr.FieldError{fieldErr(field, "TOO_LONG", "too long")}
	}
	return nil
}

// validateDates checks the stay dates. today is the business date; checkPast false skips the arrival >= BD
// rule (an overdue line amended without touching its dates).
func validateDates(prefix string, arrival, departure, today civil.Date, checkPast bool) []apperr.FieldError {
	var f []apperr.FieldError
	if checkPast && arrival.Before(today) {
		f = append(f, fieldErr(prefix+"arrival_date", "IN_THE_PAST", "before the business date"))
	}
	if !departure.After(arrival) {
		f = append(f, fieldErr(prefix+"departure_date", "BEFORE_ARRIVAL", "after arrival"))
	} else if arrival.DaysUntil(departure) > availability.MaxSearchNights {
		f = append(f, fieldErr(prefix+"departure_date", "TOO_LONG_STAY", "at most 365 nights"))
	}
	return f
}

// validateOccupancy checks the party against the room type limits.
func validateOccupancy(prefix string, adults, children int, maxAdult, maxChild, maxOccupancy int16) []apperr.FieldError {
	if adults < 1 {
		return []apperr.FieldError{fieldErr(prefix+"adult_count", "OUT_OF_RANGE", "at least one adult")}
	}
	if children < 0 {
		return []apperr.FieldError{fieldErr(prefix+"child_count", "OUT_OF_RANGE", "not negative")}
	}
	if !availability.FitsOccupancy(adults, children, int(maxAdult), int(maxChild), int(maxOccupancy)) {
		return []apperr.FieldError{fieldErr(prefix+"adult_count", "OCCUPANCY_EXCEEDED", "the party exceeds the room type limits")}
	}
	return nil
}

// NightRate is the stored price snapshot of one night.
type NightRate struct {
	Date           civil.Date       `json:"date"`
	RatePlanID     int64            `json:"rate_plan_id"`
	ChargeCodeID   int64            `json:"charge_code_id"`
	PriceMode      string           `json:"price_mode"`
	BaseRate       *decimal.Decimal `json:"base_rate"`
	DiscountAmount decimal.Decimal  `json:"discount_amount"`
	Amount         decimal.Decimal  `json:"amount"`
	IsOverride     bool             `json:"is_override"`
	// GridRate is the price in the rate grid and YieldRules the codes of the yield rules that moved it to BaseRate.
	GridRate   *decimal.Decimal `json:"grid_rate"`
	YieldRules []string         `json:"yield_rules"`
	// BedAdjustment is what the kept bed added to the price the night was sold at (0 when the line keeps no bed); BaseRate includes it.
	BedAdjustment decimal.Decimal `json:"bed_adjustment"`
}

// Estimate is the charge engine's total of a line's nights (advisory).
type Estimate struct {
	Net     decimal.Decimal `json:"net"`
	Service decimal.Decimal `json:"service"`
	Tax     decimal.Decimal `json:"tax"`
	Total   decimal.Decimal `json:"total"`
}

// Line is one booked room unit.
type Line struct {
	ID                 int64       `json:"id"`
	Status             string      `json:"status"`
	RoomTypeID         int64       `json:"room_type_id"`
	RoomTypeCode       string      `json:"room_type_code"`
	BedTypeID          *int64      `json:"bed_type_id"`
	BedLocked          bool        `json:"bed_locked"`
	BedTypeCode        string      `json:"bed_type_code,omitempty"`
	BedTypeName        string      `json:"bed_type_name,omitempty"`
	RoomID             *int64      `json:"room_id"`
	RoomNumber         string      `json:"room_number,omitempty"`
	RatePlanID         int64       `json:"rate_plan_id"`
	RatePlanCode       string      `json:"rate_plan_code"`
	OccupancyKind      string      `json:"occupancy_kind"`
	OccupancyReason    string      `json:"occupancy_reason,omitempty"`
	GuestID            *int64      `json:"guest_id"`
	Guest              *GuestName  `json:"guest,omitempty"`
	ArrivalDate        civil.Date  `json:"arrival_date"`
	DepartureDate      civil.Date  `json:"departure_date"`
	Nights             int         `json:"nights"`
	AdultCount         int         `json:"adult_count"`
	ChildCount         int         `json:"child_count"`
	StayID             *int64      `json:"stay_id"`
	CancelledAt        *time.Time  `json:"cancelled_at"`
	CancellationReason string      `json:"cancellation_reason,omitempty"`
	NoShowAt           *time.Time  `json:"no_show_at"`
	NightlyRates       []NightRate `json:"nightly_rates"`
	Estimate           Estimate    `json:"estimate"`
}

// GuestName is a short guest description.
type GuestName struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name"`
}

// FolioBrief is a folio of the reservation with its computed balance.
type FolioBrief struct {
	ID          int64           `json:"id"`
	FolioNumber string          `json:"folio_number"`
	StayID      *int64          `json:"stay_id"`
	Status      string          `json:"status"`
	Balance     decimal.Decimal `json:"balance"`
	// FolioType is GUEST or COMPANY; a COMPANY folio is billed to BillToCompanyID.
	FolioType         string `json:"folio_type"`
	BillToCompanyID   *int64 `json:"bill_to_company_id"`
	BillToCompanyName string `json:"bill_to_company_name,omitempty"`
}

// Reservation is the detail view.
type Reservation struct {
	ID                 int64        `json:"id"`
	ConfirmationNumber string       `json:"confirmation_number"`
	GuestID            *int64       `json:"guest_id"`
	Guest              *GuestName   `json:"guest,omitempty"`
	CompanyID          *int64       `json:"company_id"`
	CompanyName        string       `json:"company_name,omitempty"`
	BookingGroupID     *int64       `json:"booking_group_id"`
	GroupCode          string       `json:"group_code,omitempty"`
	ReservationDate    civil.Date   `json:"reservation_date"`
	Source             string       `json:"source"`
	Market             string       `json:"market,omitempty"`
	Status             string       `json:"status"`
	DisplayStatus      string       `json:"display_status"`
	SpecialRequest     string       `json:"special_request,omitempty"`
	Remarks            string       `json:"remarks,omitempty"`
	ArrivalDate        civil.Date   `json:"arrival_date"`
	DepartureDate      civil.Date   `json:"departure_date"`
	ConfirmedAt        *time.Time   `json:"confirmed_at"`
	CancelledAt        *time.Time   `json:"cancelled_at"`
	CancellationReason string       `json:"cancellation_reason,omitempty"`
	Version            int32        `json:"version"`
	Rooms              []Line       `json:"rooms"`
	Folios             []FolioBrief `json:"folios"`
	CreatedAt          time.Time    `json:"created_at"`
}

// Summary is a row of the reservation list.
type Summary struct {
	ID                 int64      `json:"id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	GuestID            *int64     `json:"guest_id"`
	GuestName          string     `json:"guest_name,omitempty"`
	CompanyID          *int64     `json:"company_id"`
	CompanyName        string     `json:"company_name,omitempty"`
	BookingGroupID     *int64     `json:"booking_group_id"`
	GroupCode          string     `json:"group_code,omitempty"`
	Source             string     `json:"source"`
	Status             string     `json:"status"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	RoomCount          int        `json:"room_count"`
	Version            int32      `json:"version"`
	CreatedAt          time.Time  `json:"created_at"`
}

// CancelResult is the reservation after a cancellation, with what is left on its folios.
type CancelResult struct {
	Reservation             Reservation     `json:"reservation"`
	FolioBalance            decimal.Decimal `json:"folio_balance"`
	RequiresFolioResolution bool            `json:"requires_folio_resolution"`
}

// DisplayStatus derives the status shown to staff from the header and line statuses.
func DisplayStatus(header string, lines []string) string {
	switch header {
	case StatusDraft:
		return DisplayDraft
	case StatusCancelled:
		return DisplayCancelled
	}
	var confirmed, inHouse, completed, noShow int
	for _, s := range lines {
		switch s {
		case LineConfirmed, LineDraft:
			confirmed++
		case LineCheckedIn:
			inHouse++
		case LineCompleted:
			completed++
		case LineNoShow:
			noShow++
		}
	}
	switch {
	case inHouse > 0:
		return DisplayInHouse
	case confirmed > 0:
		return DisplayConfirmed
	case completed > 0:
		return DisplayCheckedOut
	case noShow > 0:
		return DisplayNoShow
	}
	return DisplayCancelled
}

// effectiveType is the room type a CONFIRMED line consumes: the assigned room's type, else the booked type.
func effectiveType(bookedType int64, roomID *int64, roomTypes map[int64]int64) int64 {
	if roomID != nil {
		if t, ok := roomTypes[*roomID]; ok {
			return t
		}
	}
	return bookedType
}
