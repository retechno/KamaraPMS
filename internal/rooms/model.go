// Package rooms owns the physical inventory: room types, rooms and OOO/OOS blocks.
//
// Occupancy is never stored here; it is derived from stays and reservations.
// Housekeeping state lives in the housekeeping module.
package rooms

import (
	"regexp"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)

// RoomType groups interchangeable rooms; inventory is counted per type.
type RoomType struct {
	ID            int64     `json:"id"`
	Code          string    `json:"code"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	MaxAdult      int32     `json:"max_adult"`
	MaxChild      int32     `json:"max_child"`
	MaxOccupancy  int32     `json:"max_occupancy"`
	BaseOccupancy int32     `json:"base_occupancy"`
	SortOrder     int32     `json:"sort_order"`
	IsActive      bool      `json:"is_active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// RoomTypeInput is the editable part of a room type. The code is fixed at creation.
type RoomTypeInput struct {
	Code          string
	Name          string
	Description   string
	MaxAdult      int32
	MaxChild      int32
	MaxOccupancy  int32
	BaseOccupancy int32
	SortOrder     int32
	IsActive      bool
}

// Normalize trims text and upper-cases the code.
func (in *RoomTypeInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
}

// Validate mirrors the table constraints so callers get field errors, not a generic 422.
func (in RoomTypeInput) Validate(checkCode bool) []apperr.FieldError {
	var errs []apperr.FieldError
	add := func(field, code, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Code: code, Message: msg})
	}
	if checkCode && !codePattern.MatchString(in.Code) {
		add("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit")
	}
	if in.Name == "" {
		add("name", "REQUIRED", "")
	} else if len(in.Name) > 100 {
		add("name", "TOO_LONG", "at most 100 characters")
	}
	if len(in.Description) > 1000 {
		add("description", "TOO_LONG", "at most 1000 characters")
	}
	if in.MaxAdult < 1 || in.MaxAdult > 20 {
		add("max_adult", "OUT_OF_RANGE", "between 1 and 20")
	}
	if in.MaxChild < 0 || in.MaxChild > 20 {
		add("max_child", "OUT_OF_RANGE", "between 0 and 20")
	}
	if in.MaxOccupancy < 1 || in.MaxOccupancy > in.MaxAdult+in.MaxChild {
		add("max_occupancy", "OUT_OF_RANGE", "between 1 and max_adult + max_child")
	}
	if in.BaseOccupancy < 1 || in.BaseOccupancy > in.MaxOccupancy {
		add("base_occupancy", "OUT_OF_RANGE", "between 1 and max_occupancy")
	}
	return errs
}

// BedType is an entry of the property's catalogue of beds (King, Twin, ...). A room has at most one; a reservation line
// can ask for one.
type BedType struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	SortOrder int32     `json:"sort_order"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// BedTypeInput is the editable part of a bed type. The code is fixed at creation.
type BedTypeInput struct {
	Code      string
	Name      string
	SortOrder int32
	IsActive  bool
}

// Normalize trims text and upper-cases the code.
func (in *BedTypeInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
}

// Validate mirrors the table constraints so callers get field errors.
func (in BedTypeInput) Validate(checkCode bool) []apperr.FieldError {
	var errs []apperr.FieldError
	add := func(field, code, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Code: code, Message: msg})
	}
	if checkCode && !codePattern.MatchString(in.Code) {
		add("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit")
	}
	if in.Name == "" {
		add("name", "REQUIRED", "")
	} else if len(in.Name) > 60 {
		add("name", "TOO_LONG", "at most 60 characters")
	}
	if in.SortOrder < -1000 || in.SortOrder > 1000 {
		add("sort_order", "OUT_OF_RANGE", "between -1000 and 1000")
	}
	return errs
}

// Room is a physical, sellable room.
type Room struct {
	ID         int64     `json:"id"`
	RoomTypeID int64     `json:"room_type_id"`
	RoomNumber string    `json:"room_number"`
	Floor      string    `json:"floor,omitempty"`
	Building   string    `json:"building,omitempty"`
	BedTypeID  int64     `json:"bed_type_id"`
	IsActive   bool      `json:"is_active"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// RoomInput is the editable part of a room.
type RoomInput struct {
	RoomTypeID int64
	RoomNumber string
	Floor      string
	Building   string
	BedTypeID  *int64 // required: every room has a bed type (the variant it is sold as)
	IsActive   bool
}

// Normalize trims text.
func (in *RoomInput) Normalize() {
	in.RoomNumber = strings.TrimSpace(in.RoomNumber)
	in.Floor = strings.TrimSpace(in.Floor)
	in.Building = strings.TrimSpace(in.Building)
}

// Validate checks lengths and required fields.
func (in RoomInput) Validate() []apperr.FieldError {
	var errs []apperr.FieldError
	add := func(field, code, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Code: code, Message: msg})
	}
	if in.RoomNumber == "" {
		add("room_number", "REQUIRED", "")
	} else if len(in.RoomNumber) > 20 {
		add("room_number", "TOO_LONG", "at most 20 characters")
	}
	if len(in.Floor) > 10 {
		add("floor", "TOO_LONG", "at most 10 characters")
	}
	if len(in.Building) > 50 {
		add("building", "TOO_LONG", "at most 50 characters")
	}
	if in.RoomTypeID < 1 {
		add("room_type_id", "REQUIRED", "")
	}
	if in.BedTypeID == nil || *in.BedTypeID < 1 {
		add("bed_type_id", "REQUIRED", "choose the bed type of the room")
	}
	return errs
}

// Block types and statuses.
const (
	BlockOOO = "OOO" // out of order
	BlockOOS = "OOS" // out of service

	BlockActive    = "ACTIVE"
	BlockCancelled = "CANCELLED"

	maxReason = 500
)

// RoomBlock takes a room out of sale for the half-open range [StartDate, EndDate).
type RoomBlock struct {
	ID          int64      `json:"id"`
	RoomID      int64      `json:"room_id"`
	BlockType   string     `json:"block_type"`
	StartDate   civil.Date `json:"start_date"`
	EndDate     civil.Date `json:"end_date"`
	Reason      string     `json:"reason"`
	Status      string     `json:"status"`
	CancelledAt *time.Time `json:"cancelled_at,omitempty"`
	CancelledBy *int64     `json:"cancelled_by,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Conflict is an existing stay or reservation line that prevents a change to a room.
type Conflict struct {
	Type      string     `json:"type"` // STAY or RESERVATION
	ID        int64      `json:"id"`
	Reference string     `json:"reference,omitempty"`
	From      civil.Date `json:"from"`
	To        civil.Date `json:"to"` // exclusive
}

// Conflict types.
const (
	ConflictStay        = "STAY"
	ConflictReservation = "RESERVATION"
)

// validBlockType reports whether t is OOO or OOS.
func validBlockType(t string) bool { return t == BlockOOO || t == BlockOOS }

// ValidateBlockDates applies the date rules against the business date:
// a block may not start in the past and must cover at least one night.
func ValidateBlockDates(start, end, bd civil.Date) []apperr.FieldError {
	var errs []apperr.FieldError
	if start.Before(bd) {
		errs = append(errs, apperr.FieldError{Field: "start_date", Code: "OUT_OF_RANGE", Message: "must not be before the business date " + bd.String()})
	}
	if !end.After(start) {
		errs = append(errs, apperr.FieldError{Field: "end_date", Code: "OUT_OF_RANGE", Message: "must be after start_date"})
	}
	return errs
}

func validateReason(field, reason string) []apperr.FieldError {
	switch {
	case reason == "":
		return []apperr.FieldError{{Field: field, Code: "REQUIRED", Message: "a reason is required"}}
	case len(reason) > maxReason:
		return []apperr.FieldError{{Field: field, Code: "TOO_LONG", Message: "at most 500 characters"}}
	}
	return nil
}
