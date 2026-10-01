// Package groups keeps booking groups: a block of reservations (a conference, a tour, a wedding) that share
// dates and optionally a company that is billed. Rooms are booked as ordinary reservations that name the group.
package groups

import (
	"net/mail"
	"regexp"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)

// Group is a booking group with its totals.
type Group struct {
	ID               int64      `json:"id"`
	Code             string     `json:"code"`
	Name             string     `json:"name"`
	CompanyID        *int64     `json:"company_id"`
	CompanyName      string     `json:"company_name,omitempty"`
	ContactName      string     `json:"contact_name,omitempty"`
	ContactEmail     string     `json:"contact_email,omitempty"`
	ContactPhone     string     `json:"contact_phone,omitempty"`
	ArrivalDate      civil.Date `json:"arrival_date"`
	DepartureDate    civil.Date `json:"departure_date"`
	Notes            string     `json:"notes,omitempty"`
	IsActive         bool       `json:"is_active"`
	ReservationCount int        `json:"reservation_count"`
	RoomCount        int        `json:"room_count"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Member is a reservation of a group.
type Member struct {
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	Status             string     `json:"status"`
	GuestName          string     `json:"guest_name,omitempty"`
	CompanyID          *int64     `json:"company_id"`
	ArrivalDate        civil.Date `json:"arrival_date"`
	DepartureDate      civil.Date `json:"departure_date"`
	RoomCount          int        `json:"room_count"`
}

// Input is the editable part of a group (the code is fixed at creation).
type Input struct {
	Code          string
	Name          string
	CompanyID     *int64 // nil or below 1: no company
	ContactName   string
	ContactEmail  string
	ContactPhone  string
	ArrivalDate   civil.Date
	DepartureDate civil.Date
	Notes         string
	IsActive      bool
}

// Normalize trims text and upper-cases the code.
func (in *Input) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	for _, f := range []*string{&in.Name, &in.ContactName, &in.ContactEmail, &in.ContactPhone, &in.Notes} {
		*f = strings.TrimSpace(*f)
	}
	if in.CompanyID != nil && *in.CompanyID < 1 {
		in.CompanyID = nil
	}
}

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

// Validate returns every invalid field.
func (in Input) Validate(checkCode bool) []apperr.FieldError {
	var errs []apperr.FieldError
	if checkCode && !codePattern.MatchString(in.Code) {
		errs = append(errs, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit"))
	}
	if in.Name == "" || len([]rune(in.Name)) > 150 {
		errs = append(errs, fieldErr("name", "REQUIRED", "1-150 characters"))
	}
	if len([]rune(in.ContactName)) > 150 {
		errs = append(errs, fieldErr("contact_name", "TOO_LONG", "too long"))
	}
	if len([]rune(in.ContactPhone)) > 40 {
		errs = append(errs, fieldErr("contact_phone", "TOO_LONG", "too long"))
	}
	if len([]rune(in.Notes)) > 1000 {
		errs = append(errs, fieldErr("notes", "TOO_LONG", "too long"))
	}
	if in.ContactEmail != "" {
		if a, err := mail.ParseAddress(in.ContactEmail); err != nil || a.Address != in.ContactEmail || len(in.ContactEmail) > 254 {
			errs = append(errs, fieldErr("contact_email", "INVALID_FORMAT", "an e-mail address"))
		}
	}
	switch {
	case in.ArrivalDate.IsZero():
		errs = append(errs, fieldErr("arrival_date", "REQUIRED", "a date"))
	case in.DepartureDate.IsZero():
		errs = append(errs, fieldErr("departure_date", "REQUIRED", "a date"))
	case !in.DepartureDate.After(in.ArrivalDate):
		errs = append(errs, fieldErr("departure_date", "INVALID_RANGE", "after the arrival date"))
	}
	return errs
}
