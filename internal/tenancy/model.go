// Package tenancy owns tenants, properties, the property business date
// (business_days) and gapless document numbering.
//
// The business date is the only source of "what day is it at the hotel".
// It is never derived from the server clock; see BusinessDayService.
package tenancy

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
)

// Tenant is a SaaS customer (a hotel company).
type Tenant struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Timezone  string    `json:"timezone"`
	CreatedAt time.Time `json:"created_at"`
}

// Property statuses.
const (
	PropertyActive   = "ACTIVE"
	PropertyInactive = "INACTIVE"
)

// Property is one hotel.
type Property struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"-"`
	Code     string `json:"code"`
	PropertySettings
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Location returns the property's time zone (validated on write).
func (p Property) Location() *time.Location {
	loc, err := time.LoadLocation(p.Timezone)
	if err != nil {
		return time.UTC // unreachable: the zone is validated before it is stored
	}
	return loc
}

// PropertySettings are the editable property attributes.
type PropertySettings struct {
	Name                            string          `json:"name"`
	Address                         string          `json:"address,omitempty"`
	City                            string          `json:"city,omitempty"`
	CountryCode                     string          `json:"country_code,omitempty"`
	Phone                           string          `json:"phone,omitempty"`
	Email                           string          `json:"email,omitempty"`
	TaxID                           string          `json:"tax_id,omitempty"`
	DocumentFooter                  string          `json:"document_footer,omitempty"`
	Timezone                        string          `json:"timezone"`
	CurrencyCode                    string          `json:"currency_code"`
	CurrencyDecimals                int32           `json:"currency_decimals"`
	CheckInTime                     civil.TimeOfDay `json:"check_in_time"`
	CheckOutTime                    civil.TimeOfDay `json:"check_out_time"`
	RequireRoomInspectionForCheckin bool            `json:"require_room_inspection_for_checkin"`
	NightAuditMarksOccupiedDirty    bool            `json:"night_audit_marks_occupied_dirty"`
	NightAuditEarliestTime          civil.TimeOfDay `json:"night_audit_earliest_time"`
	// RefundMethods are the methods a refund may leave by (CASH by default).
	RefundMethods []string `json:"refund_methods"`
}

var (
	codePattern     = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)
	currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)
	countryPattern  = regexp.MustCompile(`^[A-Z]{2}$`)
)

// NormalizeCode trims and upper-cases a code.
func NormalizeCode(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func validateCode(field, code string) []apperr.FieldError {
	if !codePattern.MatchString(code) {
		return []apperr.FieldError{{Field: field, Code: "INVALID_FORMAT",
			Message: "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit"}}
	}
	return nil
}

// ValidateTimezone accepts IANA zone names only ("Asia/Jakarta"), never "Local".
func ValidateTimezone(tz string) error {
	if tz == "" || tz == "Local" || !strings.Contains(tz, "/") && tz != "UTC" {
		return apperr.Invalid("invalid time zone")
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return apperr.Invalid("invalid time zone")
	}
	return nil
}

// Normalize trims text and upper-cases codes.
func (s *PropertySettings) Normalize() {
	s.Name = strings.TrimSpace(s.Name)
	s.Address = strings.TrimSpace(s.Address)
	s.City = strings.TrimSpace(s.City)
	s.Phone = strings.TrimSpace(s.Phone)
	s.Email = strings.TrimSpace(s.Email)
	s.TaxID = strings.TrimSpace(s.TaxID)
	s.DocumentFooter = strings.TrimSpace(s.DocumentFooter)
	s.CountryCode = strings.ToUpper(strings.TrimSpace(s.CountryCode))
	s.Timezone = strings.TrimSpace(s.Timezone)
	s.CurrencyCode = strings.ToUpper(strings.TrimSpace(s.CurrencyCode))
}

// Validate returns every invalid field.
func (s PropertySettings) Validate() []apperr.FieldError {
	var errs []apperr.FieldError
	add := func(field, code, msg string) {
		errs = append(errs, apperr.FieldError{Field: field, Code: code, Message: msg})
	}

	if s.Name == "" || len(s.Name) > 200 {
		add("name", "REQUIRED", "1-200 characters")
	}
	if len(s.Address) > 300 {
		add("address", "TOO_LONG", "at most 300 characters")
	}
	if len(s.City) > 100 {
		add("city", "TOO_LONG", "at most 100 characters")
	}
	if len(s.Phone) > 40 {
		add("phone", "TOO_LONG", "at most 40 characters")
	}
	if s.Email != "" && (len(s.Email) > 254 || !strings.Contains(s.Email, "@") || strings.ContainsAny(s.Email, " \r\n<>,;")) {
		add("email", "INVALID_FORMAT", "an e-mail address")
	}
	if len(s.TaxID) > 40 {
		add("tax_id", "TOO_LONG", "at most 40 characters")
	}
	if len(s.DocumentFooter) > 500 {
		add("document_footer", "TOO_LONG", "at most 500 characters")
	}
	if s.CountryCode != "" && !countryPattern.MatchString(s.CountryCode) {
		add("country_code", "INVALID_FORMAT", "ISO 3166-1 alpha-2, e.g. ID")
	}
	if ValidateTimezone(s.Timezone) != nil {
		add("timezone", "INVALID_TIMEZONE", "an IANA time zone such as Asia/Jakarta")
	}
	if !currencyPattern.MatchString(s.CurrencyCode) {
		add("currency_code", "INVALID_FORMAT", "ISO 4217 code, e.g. IDR")
	}
	if money.ValidateDecimals(s.CurrencyDecimals) != nil {
		add("currency_decimals", "OUT_OF_RANGE", "between 0 and 3")
	}
	if len(s.RefundMethods) == 0 {
		add("refund_methods", "REQUIRED", "at least one of "+strings.Join(RefundMethodChoices, ", "))
	}
	seen := map[string]bool{}
	for _, m := range s.RefundMethods {
		if !slices.Contains(RefundMethodChoices, m) || seen[m] {
			add("refund_methods", "INVALID_VALUE", "each of "+strings.Join(RefundMethodChoices, ", ")+", once")
			break
		}
		seen[m] = true
	}
	return errs
}

// RefundMethodChoices are the methods a property can allow for refunds.
var RefundMethodChoices = []string{"CASH", "CARD", "BANK_TRANSFER", "OTHER"}

// Business day statuses.
const (
	DayOpen   = "OPEN"
	DayClosed = "CLOSED"
)

// BusinessDay is one business date of a property. Exactly one is OPEN.
type BusinessDay struct {
	ID           int64           `json:"-"`
	PropertyID   int64           `json:"-"`
	BusinessDate civil.Date      `json:"business_date"`
	Status       string          `json:"status"`
	OpenedAt     time.Time       `json:"opened_at"`
	OpenedBy     *int64          `json:"opened_by,omitempty"`
	ClosedAt     *time.Time      `json:"closed_at,omitempty"`
	ClosedBy     *int64          `json:"closed_by,omitempty"`
	Summary      json.RawMessage `json:"summary,omitempty"`
}

// DayClock relates the business date to server time and property local time.
type DayClock struct {
	BusinessDate          civil.Date `json:"business_date"`
	ServerTime            time.Time  `json:"server_time"`
	PropertyLocalTime     string     `json:"property_local_time"` // RFC 3339 with the property's offset
	Timezone              string     `json:"timezone"`
	NightAuditAllowed     bool       `json:"night_audit_allowed"`
	NightAuditAllowedFrom time.Time  `json:"night_audit_allowed_from"`
	NightAuditOverdue     bool       `json:"night_audit_overdue"`
}

// EvaluateDay is the business-date/clock rule set (docs/architecture/04-operations.md §13):
//
//   - closing business date BD is allowed when the property's local date is after BD,
//     or equals BD and the local time has reached night_audit_earliest_time;
//   - night audit is overdue when the local date is more than one day past BD.
//
// It is pure: the same inputs always give the same answer.
func EvaluateDay(bd civil.Date, now time.Time, loc *time.Location, earliest civil.TimeOfDay) DayClock {
	local := now.In(loc)
	localDate := civil.DateOf(local)
	allowedFrom := bd.At(earliest, loc)
	return DayClock{
		BusinessDate:          bd,
		ServerTime:            now.UTC(),
		PropertyLocalTime:     local.Format(time.RFC3339),
		Timezone:              loc.String(),
		NightAuditAllowed:     localDate.After(bd) || (localDate.Equal(bd) && !now.Before(allowedFrom)),
		NightAuditAllowedFrom: allowedFrom.UTC(),
		NightAuditOverdue:     localDate.After(bd.AddDays(1)),
	}
}

// ValidateOpeningDate: a property opens on its local "today" or "yesterday"
// (yesterday covers setting up after midnight before the first night audit).
// A future date would put the business date ahead of reality.
func ValidateOpeningDate(opening civil.Date, now time.Time, loc *time.Location) *apperr.FieldError {
	today := civil.DateOf(now.In(loc))
	if opening.After(today) || opening.Before(today.AddDays(-1)) {
		return &apperr.FieldError{Field: "opening_business_date", Code: "OUT_OF_RANGE",
			Message: "must be the property's current local date or the day before (" + today.AddDays(-1).String() + " or " + today.String() + ")"}
	}
	return nil
}

// SequenceType identifies a gapless document number series.
type SequenceType string

const (
	SeqReservation SequenceType = "RESERVATION"
	SeqStay        SequenceType = "STAY"
	SeqFolio       SequenceType = "FOLIO"
	SeqPayment     SequenceType = "PAYMENT"
	// SeqCityLedgerReceipt numbers what a company pays against its city ledger account.
	SeqCityLedgerReceipt SequenceType = "CITY_LEDGER_RECEIPT"
	// SeqCityLedgerInvoice numbers the invoices sent to companies.
	SeqCityLedgerInvoice SequenceType = "CITY_LEDGER_INVOICE"
	// SeqJournal numbers the general ledger journals.
	SeqJournal SequenceType = "JOURNAL"
	// SeqSupplierBill numbers the supplier bills entered in the payables.
	SeqSupplierBill SequenceType = "SUPPLIER_BILL"
	// SeqSupplierPayment numbers the payments to suppliers.
	SeqSupplierPayment SequenceType = "SUPPLIER_PAYMENT"
	// SeqTaxReturn numbers the tax returns filed.
	SeqTaxReturn SequenceType = "TAX_RETURN"
	// SeqTaxPayment numbers the payments to the tax authority.
	SeqTaxPayment SequenceType = "TAX_PAYMENT"
	// SeqMaintenance numbers maintenance requests.
	SeqMaintenance SequenceType = "MAINTENANCE"
	// SeqLostFound numbers lost and found items.
	SeqLostFound SequenceType = "LOST_FOUND"
)

// defaultSequences are created with every property.
var defaultSequences = []struct {
	Type   SequenceType
	Prefix string
}{
	{SeqReservation, "RES"},
	{SeqStay, "STY"},
	{SeqFolio, "FOL"},
	{SeqPayment, "PAY"},
	{SeqCityLedgerReceipt, "CLR"},
	{SeqCityLedgerInvoice, "CINV"},
	{SeqJournal, "JV"},
	{SeqSupplierBill, "BILL"},
	{SeqSupplierPayment, "SPAY"},
	{SeqTaxReturn, "TXR"},
	{SeqTaxPayment, "TXP"},
	{SeqMaintenance, "MNT"},
	{SeqLostFound, "LF"},
}
