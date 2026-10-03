// Package billingconfig owns the billing configuration of a property: taxes, service charges, charge
// codes, and the ordered rules that map taxes and service charges onto a charge code. It also provides
// the ChargeRuleResolver the charge calculation engine reads from (M6).
//
// It never computes an amount: only chargecalc multiplies by a rate.
package billingconfig

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
)

var (
	codePattern    = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)
	ratePattern    = regexp.MustCompile(`^[0-9]{1,3}(\.[0-9]{1,4})?$`)
	amountPattern  = regexp.MustCompile(`^[0-9]{1,15}(\.[0-9]{1,3})?$`)
	hundred        = decimal.NewFromInt(100)
	maxRulesPerSet = 20
)

// Charge types and price modes (CHECK constraints of charge_codes).
const (
	TypeRoom         = "ROOM"
	TypeFoodBeverage = "FOOD_BEVERAGE"
	TypeService      = "SERVICE"
	TypeFee          = "FEE"
	TypeOther        = "OTHER"

	ModeExclusive = "EXCLUSIVE"
	ModeInclusive = "INCLUSIVE"
)

var chargeTypes = map[string]bool{TypeRoom: true, TypeFoodBeverage: true, TypeService: true, TypeFee: true, TypeOther: true}

// ParseRate parses a percentage such as "11" or "11.0000": 0 to 100 with at most four decimals.
func ParseRate(s string) (decimal.Decimal, error) {
	if !ratePattern.MatchString(s) {
		return decimal.Decimal{}, apperr.Invalid("not a percentage")
	}
	d, err := decimal.NewFromString(s)
	if err != nil || d.GreaterThan(hundred) {
		return decimal.Decimal{}, apperr.Invalid("not a percentage")
	}
	return d, nil
}

// FormatRate is the wire form of a percentage: always four decimals.
func FormatRate(d decimal.Decimal) string { return d.StringFixed(4) }

// ParseUnitPrice parses a non-negative amount and checks it fits the property's currency precision.
func ParseUnitPrice(s string, currencyDecimals int32) (decimal.Decimal, error) {
	if !amountPattern.MatchString(s) {
		return decimal.Decimal{}, apperr.Invalid("not an amount")
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Decimal{}, apperr.Invalid("not an amount")
	}
	if !d.Equal(d.Round(currencyDecimals)) {
		return decimal.Decimal{}, apperr.Invalid("too many decimals for the property currency")
	}
	return d, nil
}

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

func validateCode(code string) []apperr.FieldError {
	if !codePattern.MatchString(code) {
		return []apperr.FieldError{fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit")}
	}
	return nil
}

// glPattern is the shape of an account code of the chart of accounts the accounting module will own, for
// example 4-1100, 2.1.05 or REV:ROOM. It is a text code so accounting can adopt it without a migration.
var glPattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._:/-]{0,29}$`)

func normalizeGL(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

func validateGL(code string) []apperr.FieldError {
	if code != "" && !glPattern.MatchString(code) {
		return []apperr.FieldError{fieldErr("gl_account_code", "INVALID_FORMAT", "1-30 characters: A-Z, 0-9, '.', '-', '_', ':' or '/', starting with a letter or digit")}
	}
	return nil
}

func validateName(name string) []apperr.FieldError {
	switch {
	case name == "":
		return []apperr.FieldError{fieldErr("name", "REQUIRED", "")}
	case len(name) > 100:
		return []apperr.FieldError{fieldErr("name", "TOO_LONG", "at most 100 characters")}
	}
	return nil
}

func validateRate(s string) []apperr.FieldError {
	if _, err := ParseRate(s); err != nil {
		return []apperr.FieldError{fieldErr("rate", "INVALID_RATE", "a percentage from 0 to 100 with at most 4 decimals, e.g. 11.0000")}
	}
	return nil
}

// Tax is a percentage levied on a charge. Whether a price already contains it is a property of the price
// (the charge code's price mode), never of the tax.
type Tax struct {
	ID           int64  `json:"id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	Rate         string `json:"rate"`
	TaxOnService bool   `json:"tax_on_service"`
	// TaxKind is VAT, LOCAL (the hotel tax, PB1) or OTHER; only VAT takes part in the PKP rules.
	TaxKind string `json:"tax_kind"`
	// GLAccountCode is the tax payable account in the chart of accounts (optional, a code, never an id).
	GLAccountCode *string   `json:"gl_account_code"`
	IsActive      bool      `json:"is_active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// AffectedOpenStays is set on an update that changed the rate: open stays whose remaining nights are
	// charged through a code that maps this tax. They pick up the new rate for postings from now on.
	AffectedOpenStays *int64 `json:"affected_open_stays,omitempty"`
}

// TaxInput is the editable part of a tax (the code is fixed at creation).
type TaxInput struct {
	Code          string
	Name          string
	Rate          string
	TaxOnService  bool
	TaxKind       string // "" = LOCAL
	GLAccountCode string // "" = none
	IsActive      bool
}

// Normalize trims text and upper-cases the code.
func (in *TaxInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.Rate = strings.TrimSpace(in.Rate)
	in.TaxKind = strings.ToUpper(strings.TrimSpace(in.TaxKind))
	if in.TaxKind == "" {
		in.TaxKind = TaxKindLocal
	}
	in.GLAccountCode = normalizeGL(in.GLAccountCode)
}

// Tax kinds.
const (
	TaxKindVAT   = "VAT"
	TaxKindLocal = "LOCAL"
	TaxKindOther = "OTHER"
)

// Validate checks the input.
func (in TaxInput) Validate(checkCode bool) []apperr.FieldError {
	var errs []apperr.FieldError
	if checkCode {
		errs = append(errs, validateCode(in.Code)...)
	}
	errs = append(errs, validateName(in.Name)...)
	errs = append(errs, validateGL(in.GLAccountCode)...)
	if k := in.TaxKind; k != TaxKindVAT && k != TaxKindLocal && k != TaxKindOther {
		errs = append(errs, fieldErr("tax_kind", "INVALID_VALUE", "VAT, LOCAL (the hotel tax) or OTHER"))
	}
	return append(errs, validateRate(in.Rate)...)
}

// ServiceCharge is a percentage added to a charge (for example 10% service).
type ServiceCharge struct {
	ID                int64     `json:"id"`
	Code              string    `json:"code"`
	Name              string    `json:"name"`
	Rate              string    `json:"rate"`
	GLAccountCode     *string   `json:"gl_account_code"` // service charge payable account (optional)
	IsActive          bool      `json:"is_active"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	AffectedOpenStays *int64    `json:"affected_open_stays,omitempty"`
}

// ServiceChargeInput is the editable part of a service charge.
type ServiceChargeInput struct {
	Code          string
	Name          string
	Rate          string
	GLAccountCode string // "" = none
	IsActive      bool
}

// Normalize trims text and upper-cases the code.
func (in *ServiceChargeInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.Rate = strings.TrimSpace(in.Rate)
	in.GLAccountCode = normalizeGL(in.GLAccountCode)
}

// Validate checks the input.
func (in ServiceChargeInput) Validate(checkCode bool) []apperr.FieldError {
	var errs []apperr.FieldError
	if checkCode {
		errs = append(errs, validateCode(in.Code)...)
	}
	errs = append(errs, validateName(in.Name)...)
	errs = append(errs, validateGL(in.GLAccountCode)...)
	return append(errs, validateRate(in.Rate)...)
}

// TaxRule is a tax mapped onto a charge code, in calculation order.
type TaxRule struct {
	TaxID        int64  `json:"tax_id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	Rate         string `json:"rate"`
	TaxOnService bool   `json:"tax_on_service"`
	Sequence     int32  `json:"sequence"`
}

// ServiceRule is a service charge mapped onto a charge code, in calculation order.
type ServiceRule struct {
	ServiceChargeID int64  `json:"service_charge_id"`
	Code            string `json:"code"`
	Name            string `json:"name"`
	Rate            string `json:"rate"`
	Sequence        int32  `json:"sequence"`
}

// ChargeCode is what is charged, with the rules that apply to it.
type ChargeCode struct {
	ID               int64         `json:"id"`
	Code             string        `json:"code"`
	Name             string        `json:"name"`
	ChargeType       string        `json:"charge_type"`
	PriceMode        string        `json:"price_mode"`
	DefaultUnitPrice *string       `json:"default_unit_price,omitempty"`
	GLAccountCode    *string       `json:"gl_account_code"` // revenue account of this charge (optional)
	IsSystem         bool          `json:"is_system"`
	IsActive         bool          `json:"is_active"`
	Taxes            []TaxRule     `json:"taxes"`
	ServiceCharges   []ServiceRule `json:"service_charges"`
	CreatedAt        time.Time     `json:"created_at"`
	UpdatedAt        time.Time     `json:"updated_at"`
}

// ChargeCodeInput is the editable part of a charge code.
type ChargeCodeInput struct {
	Code             string
	Name             string
	ChargeType       string
	PriceMode        string
	DefaultUnitPrice string // "" = none
	GLAccountCode    string // "" = none
	IsActive         bool
}

// Normalize trims text and upper-cases codes and enums.
func (in *ChargeCodeInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.ChargeType = strings.ToUpper(strings.TrimSpace(in.ChargeType))
	in.PriceMode = strings.ToUpper(strings.TrimSpace(in.PriceMode))
	in.DefaultUnitPrice = strings.TrimSpace(in.DefaultUnitPrice)
	in.GLAccountCode = normalizeGL(in.GLAccountCode)
}

// Validate checks the input; currencyDecimals is the property's precision.
func (in ChargeCodeInput) Validate(checkCode bool, currencyDecimals int32) []apperr.FieldError {
	var errs []apperr.FieldError
	if checkCode {
		errs = append(errs, validateCode(in.Code)...)
	}
	errs = append(errs, validateName(in.Name)...)
	if !chargeTypes[in.ChargeType] {
		errs = append(errs, fieldErr("charge_type", "INVALID_VALUE", "ROOM, FOOD_BEVERAGE, SERVICE, FEE or OTHER"))
	}
	if in.PriceMode != ModeExclusive && in.PriceMode != ModeInclusive {
		errs = append(errs, fieldErr("price_mode", "INVALID_VALUE", "EXCLUSIVE or INCLUSIVE"))
	}
	errs = append(errs, validateGL(in.GLAccountCode)...)
	if in.DefaultUnitPrice != "" {
		if _, err := ParseUnitPrice(in.DefaultUnitPrice, currencyDecimals); err != nil {
			errs = append(errs, fieldErr("default_unit_price", "INVALID_AMOUNT",
				"a non-negative amount with at most the property's currency decimals ("+itoa(int(currencyDecimals))+")"))
		}
	}
	return errs
}

// TaxRuleInput and ServiceRuleInput are one entry of a rule replacement.
type TaxRuleInput struct {
	TaxID    int64
	Sequence int32
}

// ServiceRuleInput is one service charge in a rule replacement.
type ServiceRuleInput struct {
	ServiceChargeID int64
	Sequence        int32
}

// RulesInput is the complete, ordered rule set of a charge code.
type RulesInput struct {
	Taxes          []TaxRuleInput
	ServiceCharges []ServiceRuleInput
}

// Validate checks counts, sequences (unique, at least 1) and duplicate ids. Existence and activity of the
// referenced rows are checked under lock by the service.
func (in RulesInput) Validate() []apperr.FieldError {
	var errs []apperr.FieldError
	check := func(list string, n int, at func(i int) (id int64, seq int32), idField string) {
		if n > maxRulesPerSet {
			errs = append(errs, fieldErr(list, "TOO_MANY", "at most "+itoa(maxRulesPerSet)+" entries"))
			return
		}
		ids, seqs := map[int64]bool{}, map[int32]bool{}
		for i := range n {
			id, seq := at(i)
			prefix := list + "[" + itoa(i) + "]."
			if id < 1 {
				errs = append(errs, fieldErr(prefix+idField, "REQUIRED", ""))
			} else if ids[id] {
				errs = append(errs, fieldErr(prefix+idField, "DUPLICATE", "each rule can be listed once"))
			}
			ids[id] = true
			if seq < 1 || seq > 32767 {
				errs = append(errs, fieldErr(prefix+"sequence", "OUT_OF_RANGE", "between 1 and 32767"))
			} else if seqs[seq] {
				errs = append(errs, fieldErr(prefix+"sequence", "DUPLICATE", "sequences must be unique"))
			}
			seqs[seq] = true
		}
	}
	check("taxes", len(in.Taxes), func(i int) (int64, int32) { return in.Taxes[i].TaxID, in.Taxes[i].Sequence }, "tax_id")
	check("service_charges", len(in.ServiceCharges), func(i int) (int64, int32) {
		return in.ServiceCharges[i].ServiceChargeID, in.ServiceCharges[i].Sequence
	}, "service_charge_id")
	return errs
}

func itoa(n int) string { return strconv.Itoa(n) }
