// Package companies keeps the corporate accounts a hotel bills instead of the guest. What a company owes (the
// city ledger) is in package cityledger; here are only the account's details and its credit terms.
package companies

import (
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/money"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)

// Company is a corporate account of a property.
type Company struct {
	ID               int64     `json:"id"`
	Code             string    `json:"code"`
	Name             string    `json:"name"`
	ContactName      string    `json:"contact_name,omitempty"`
	Email            string    `json:"email,omitempty"`
	Phone            string    `json:"phone,omitempty"`
	Address          string    `json:"address,omitempty"`
	City             string    `json:"city,omitempty"`
	TaxID            string    `json:"tax_id,omitempty"`
	CreditLimit      *string   `json:"credit_limit"` // null: no limit; "0": no credit
	PaymentTermsDays int       `json:"payment_terms_days"`
	Notes            string    `json:"notes,omitempty"`
	IsActive         bool      `json:"is_active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Input is the editable part of a company (the code is fixed at creation).
type Input struct {
	Code             string
	Name             string
	ContactName      string
	Email            string
	Phone            string
	Address          string
	City             string
	TaxID            string
	CreditLimit      string // "" = no limit
	PaymentTermsDays int
	Notes            string
	IsActive         bool
}

// Normalize trims text and upper-cases the code.
func (in *Input) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	for _, f := range []*string{&in.Name, &in.ContactName, &in.Email, &in.Phone, &in.Address, &in.City, &in.TaxID, &in.CreditLimit, &in.Notes} {
		*f = strings.TrimSpace(*f)
	}
}

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

// Validate returns every invalid field; decimals is the property's currency precision.
func (in Input) Validate(checkCode bool, decimals int32) []apperr.FieldError {
	var errs []apperr.FieldError
	if checkCode && !codePattern.MatchString(in.Code) {
		errs = append(errs, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit"))
	}
	if in.Name == "" || len([]rune(in.Name)) > 150 {
		errs = append(errs, fieldErr("name", "REQUIRED", "1-150 characters"))
	}
	for field, max := range map[string]int{"contact_name": 150, "phone": 40, "address": 300, "city": 100, "tax_id": 40, "notes": 1000} {
		v := map[string]string{"contact_name": in.ContactName, "phone": in.Phone, "address": in.Address, "city": in.City, "tax_id": in.TaxID, "notes": in.Notes}[field]
		if len([]rune(v)) > max {
			errs = append(errs, fieldErr(field, "TOO_LONG", "too long"))
		}
	}
	if in.Email != "" {
		if a, err := mail.ParseAddress(in.Email); err != nil || a.Address != in.Email || len(in.Email) > 254 {
			errs = append(errs, fieldErr("email", "INVALID_FORMAT", "an e-mail address"))
		}
	}
	if in.CreditLimit != "" {
		d, err := money.Parse(in.CreditLimit)
		if err != nil || d.IsNegative() || !d.Equal(d.Round(decimals)) {
			errs = append(errs, fieldErr("credit_limit", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals, or empty for no limit"))
		}
	}
	if in.PaymentTermsDays < 0 || in.PaymentTermsDays > 365 {
		errs = append(errs, fieldErr("payment_terms_days", "OUT_OF_RANGE", "between 0 and 365"))
	}
	return errs
}

func limitOrNil(s string) *decimal.Decimal {
	if s == "" {
		return nil
	}
	d, _ := money.Parse(s) // validated
	return &d
}
