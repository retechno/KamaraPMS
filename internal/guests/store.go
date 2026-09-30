package guests

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/guests/guestsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

func errGuestNotFound() *apperr.Error {
	return apperr.NotFound("GUEST_NOT_FOUND", "the guest does not exist or is not accessible")
}

func orNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return errGuestNotFound()
	}
	return err
}

func toGuest(g guestsdb.Guest) Guest {
	return Guest{
		ID: g.ID, Code: g.Code, OriginPropertyID: g.OriginPropertyID, FirstName: deref(g.FirstName), LastName: g.LastName,
		Email: deref(g.Email), Phone: deref(g.Phone), Nationality: deref(g.Nationality), CountryCode: deref(g.CountryCode),
		DateOfBirth: g.DateOfBirth, Gender: deref(g.Gender), IDType: deref(g.IDType), IDNumber: deref(g.IDNumber),
		Address: deref(g.Address), City: deref(g.City), Notes: deref(g.Notes), CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

func toProfile(g Guest) Profile {
	return Profile{
		FirstName: g.FirstName, LastName: g.LastName, Email: g.Email, Phone: g.Phone, Nationality: g.Nationality,
		CountryCode: g.CountryCode, DateOfBirth: g.DateOfBirth, Gender: g.Gender, IDType: g.IDType, IDNumber: g.IDNumber,
		Address: g.Address, City: g.City, Notes: g.Notes,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// dateOrNil passes a nullable birth date through.
func dateOrNil(d *civil.Date) *civil.Date { return d }

// maskID keeps only the last four characters of an ID number (for audit entries).
func maskID(s string) string {
	if len(s) <= 4 {
		if s == "" {
			return ""
		}
		return "****"
	}
	return "****" + s[len(s)-4:]
}

// auditView is a guest as written to the audit trail: the ID number is masked so the trail is not a second
// copy of identity documents.
func auditView(g Guest) map[string]any {
	return map[string]any{
		"code": g.Code, "origin_property_id": g.OriginPropertyID, "first_name": g.FirstName, "last_name": g.LastName,
		"email": g.Email, "phone": g.Phone, "nationality": g.Nationality, "country_code": g.CountryCode,
		"date_of_birth": g.DateOfBirth, "gender": g.Gender, "id_type": g.IDType, "id_number": maskID(g.IDNumber),
		"address": g.Address, "city": g.City, "notes_length": len(g.Notes),
	}
}

func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}
