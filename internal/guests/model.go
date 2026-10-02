// Package guests owns tenant-wide guest profiles: create, edit, search with visibility rules,
// duplicate hints and cross-property history.
//
// A guest profile belongs to the tenant, not to a property. What a user may see depends on their
// properties: see Service.scope.
package guests

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// Gender values accepted by the schema.
var genders = map[string]bool{"MALE": true, "FEMALE": true, "OTHER": true, "UNDISCLOSED": true}

var (
	isoPattern   = regexp.MustCompile(`^[A-Z]{2}$`)
	phonePattern = regexp.MustCompile(`^\+?[0-9][0-9 ()./-]{2,28}$`)
	emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	nonDigits    = regexp.MustCompile(`\D`)
)

// Guest is a tenant-wide profile.
type Guest struct {
	ID               int64       `json:"id"`
	Code             string      `json:"code"`
	OriginPropertyID *int64      `json:"origin_property_id,omitempty"`
	FirstName        string      `json:"first_name,omitempty"`
	LastName         string      `json:"last_name"`
	Email            string      `json:"email,omitempty"`
	Phone            string      `json:"phone,omitempty"`
	Nationality      string      `json:"nationality,omitempty"`
	CountryCode      string      `json:"country_code,omitempty"`
	DateOfBirth      *civil.Date `json:"date_of_birth,omitempty"`
	Gender           string      `json:"gender,omitempty"`
	IDType           string      `json:"id_type,omitempty"`
	IDNumber         string      `json:"id_number,omitempty"`
	Address          string      `json:"address,omitempty"`
	City             string      `json:"city,omitempty"`
	Notes            string      `json:"notes,omitempty"`
	CreatedAt        time.Time   `json:"created_at"`
	UpdatedAt        time.Time   `json:"updated_at"`
}

// FullName is "First Last" for display and duplicate comparison.
func (g Guest) FullName() string { return strings.TrimSpace(g.FirstName + " " + g.LastName) }

// Profile is the editable part of a guest.
type Profile struct {
	FirstName   string      `json:"first_name"`
	LastName    string      `json:"last_name"`
	Email       string      `json:"email"`
	Phone       string      `json:"phone"`
	Nationality string      `json:"nationality"`
	CountryCode string      `json:"country_code"`
	DateOfBirth *civil.Date `json:"date_of_birth"`
	Gender      string      `json:"gender"`
	IDType      string      `json:"id_type"`
	IDNumber    string      `json:"id_number"`
	Address     string      `json:"address"`
	City        string      `json:"city"`
	Notes       string      `json:"notes"`
}

// Normalize trims text and upper-cases codes.
func (p *Profile) Normalize() {
	p.FirstName = strings.TrimSpace(p.FirstName)
	p.LastName = strings.TrimSpace(p.LastName)
	p.Email = strings.TrimSpace(p.Email)
	p.Phone = strings.TrimSpace(p.Phone)
	p.Nationality = strings.ToUpper(strings.TrimSpace(p.Nationality))
	p.CountryCode = strings.ToUpper(strings.TrimSpace(p.CountryCode))
	p.Gender = strings.ToUpper(strings.TrimSpace(p.Gender))
	p.IDType = strings.ToUpper(strings.TrimSpace(p.IDType))
	p.IDNumber = strings.TrimSpace(p.IDNumber)
	p.Address = strings.TrimSpace(p.Address)
	p.City = strings.TrimSpace(p.City)
	p.Notes = strings.TrimSpace(p.Notes)
}

// Validate mirrors the column limits and CHECK constraints. today is the server date (UTC): a birth date
// is a person's attribute, not a hotel date, so it is only bounded to "not in the future".
func (p Profile) Validate(today civil.Date) []apperr.FieldError {
	var errs []apperr.FieldError
	add := func(field, code, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Code: code, Message: msg})
	}
	tooLong := func(field, v string, max int) {
		if len(v) > max {
			add(field, "TOO_LONG", "at most "+itoa(max)+" characters")
		}
	}
	if p.LastName == "" {
		add("last_name", "REQUIRED", "")
	}
	tooLong("last_name", p.LastName, 100)
	tooLong("first_name", p.FirstName, 100)
	if p.Email != "" && (len(p.Email) > 254 || !emailPattern.MatchString(p.Email)) {
		add("email", "INVALID_FORMAT", "a valid email address")
	}
	if p.Phone != "" && !phonePattern.MatchString(p.Phone) {
		add("phone", "INVALID_FORMAT", "digits with an optional leading +, spaces, dashes or brackets (3-29 characters)")
	}
	for _, c := range []struct{ field, v string }{{"nationality", p.Nationality}, {"country_code", p.CountryCode}} {
		if c.v != "" && !isoPattern.MatchString(c.v) {
			add(c.field, "INVALID_FORMAT", "ISO 3166-1 alpha-2, e.g. ID")
		}
	}
	if p.Gender != "" && !genders[p.Gender] {
		add("gender", "INVALID_VALUE", "MALE, FEMALE, OTHER or UNDISCLOSED")
	}
	if p.DateOfBirth != nil && (p.DateOfBirth.After(today) || p.DateOfBirth.Year() < 1900) {
		add("date_of_birth", "OUT_OF_RANGE", "between 1900 and today")
	}
	tooLong("id_type", p.IDType, 30)
	tooLong("id_number", p.IDNumber, 50)
	if (p.IDType == "") != (p.IDNumber == "") {
		add("id_number", "INCOMPLETE", "id_type and id_number go together")
	}
	tooLong("address", p.Address, 300)
	tooLong("city", p.City, 100)
	tooLong("notes", p.Notes, 2000)
	return errs
}

// PhoneDigits is the phone number reduced to digits, the form used for matching.
func PhoneDigits(phone string) string { return nonDigits.ReplaceAllString(phone, "") }

// SearchTokens splits a free-text query into lower-cased LIKE-escaped prefix tokens (at most 6).
func SearchTokens(q string) []string {
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	var out []string
	for _, f := range strings.Fields(strings.ToLower(q)) {
		if len(f) > 50 {
			f = f[:50]
		}
		out = append(out, esc.Replace(f))
		if len(out) == 6 {
			break
		}
	}
	return out
}

// Duplicate reasons.
const (
	ReasonEmail    = "SAME_EMAIL"
	ReasonPhone    = "SAME_PHONE"
	ReasonID       = "SAME_ID_DOCUMENT"
	ReasonNameDOB  = "SAME_NAME_AND_BIRTH_DATE"
	maxSearchLimit = 200
)

// DuplicateReasons explains why candidate looks like the same person as p (empty: not a duplicate).
func DuplicateReasons(p Profile, candidate Guest) []string {
	var r []string
	if p.Email != "" && strings.EqualFold(p.Email, candidate.Email) {
		r = append(r, ReasonEmail)
	}
	if d := PhoneDigits(p.Phone); d != "" && d == PhoneDigits(candidate.Phone) {
		r = append(r, ReasonPhone)
	}
	if p.IDNumber != "" && p.IDNumber == candidate.IDNumber && p.IDType == candidate.IDType {
		r = append(r, ReasonID)
	}
	if p.DateOfBirth != nil && candidate.DateOfBirth != nil && p.DateOfBirth.Equal(*candidate.DateOfBirth) &&
		strings.EqualFold(p.LastName, candidate.LastName) && strings.EqualFold(p.FirstName, candidate.FirstName) {
		r = append(r, ReasonNameDOB)
	}
	return r
}

// PossibleDuplicate is a visible profile that may be the same person.
type PossibleDuplicate struct {
	Guest   Guest    `json:"guest"`
	Reasons []string `json:"reasons"`
}

// HistoryItem is one reservation or stay of a guest.
type HistoryItem struct {
	Type          string     `json:"type"` // RESERVATION or STAY
	ID            int64      `json:"id"`
	Number        string     `json:"number"` // confirmation or stay number
	Role          string     `json:"role"`   // BOOKER, OCCUPANT, PRIMARY or ACCOMPANYING
	Status        string     `json:"status"`
	PropertyID    int64      `json:"property_id"`
	PropertyCode  string     `json:"property_code"`
	PropertyName  string     `json:"property_name"`
	ArrivalDate   civil.Date `json:"arrival_date"`
	DepartureDate civil.Date `json:"departure_date"`
}

func itoa(n int) string { return strconv.Itoa(n) }
