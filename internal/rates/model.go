// Package rates owns rate plans and the rate grid, and the price lookup that reservations use to snapshot
// nightly prices.
//
// A rate plan selects the ROOM charge code that owns the tax and service rules and the price mode; the grid
// holds one amount per (plan, room type, night) in that code's price mode. Rates never calculate tax: the
// Charge Calculation Engine does.
package rates

import (
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
)

var codePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9_-]{0,19}$`)

// Grid and fill limits.
const (
	MaxSpanDays     = 730
	MaxRoomTypes    = 50
	MaxLookupNights = 365
)

var mealPlans = map[string]bool{"RO": true, "BB": true, "HB": true, "FB": true, "AI": true}

// weekdays maps the API names to time.Weekday. A calendar date has one weekday whatever the time zone.
var weekdays = map[string]time.Weekday{
	"MON": time.Monday, "TUE": time.Tuesday, "WED": time.Wednesday, "THU": time.Thursday,
	"FRI": time.Friday, "SAT": time.Saturday, "SUN": time.Sunday,
}

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

// RatePlan is a pricing strategy.
type RatePlan struct {
	ID                 int64     `json:"id"`
	Code               string    `json:"code"`
	Name               string    `json:"name"`
	Description        string    `json:"description,omitempty"`
	MealPlan           string    `json:"meal_plan"`
	CancellationPolicy string    `json:"cancellation_policy,omitempty"`
	IsRefundable       bool      `json:"is_refundable"`
	RoomChargeCodeID   int64     `json:"room_charge_code_id"`
	RoomChargeCode     string    `json:"room_charge_code"`
	PriceMode          string    `json:"price_mode"` // of the room charge code: how grid amounts are read
	IsActive           bool      `json:"is_active"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// RatePlanInput is the editable part of a rate plan (the code is fixed at creation).
type RatePlanInput struct {
	Code               string
	Name               string
	Description        string
	MealPlan           string
	CancellationPolicy string
	IsRefundable       bool
	RoomChargeCodeID   int64
	IsActive           bool
}

// Normalize trims text and upper-cases codes.
func (in *RatePlanInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.MealPlan = strings.ToUpper(strings.TrimSpace(in.MealPlan))
	in.CancellationPolicy = strings.TrimSpace(in.CancellationPolicy)
}

// Validate checks the input; the room charge code is checked by the service.
func (in RatePlanInput) Validate(checkCode bool) []apperr.FieldError {
	var errs []apperr.FieldError
	if checkCode && !codePattern.MatchString(in.Code) {
		errs = append(errs, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, '-' or '_', starting with a letter or digit"))
	}
	if in.Name == "" {
		errs = append(errs, fieldErr("name", "REQUIRED", ""))
	} else if len(in.Name) > 100 {
		errs = append(errs, fieldErr("name", "TOO_LONG", "at most 100 characters"))
	}
	if len(in.Description) > 1000 {
		errs = append(errs, fieldErr("description", "TOO_LONG", "at most 1000 characters"))
	}
	if len(in.CancellationPolicy) > 2000 {
		errs = append(errs, fieldErr("cancellation_policy", "TOO_LONG", "at most 2000 characters"))
	}
	if !mealPlans[in.MealPlan] {
		errs = append(errs, fieldErr("meal_plan", "INVALID_VALUE", "RO, BB, HB, FB or AI"))
	}
	if in.RoomChargeCodeID < 1 {
		errs = append(errs, fieldErr("room_charge_code_id", "REQUIRED", ""))
	}
	return errs
}

// RateCell is one night's amount for a room type.
type RateCell struct {
	RoomTypeID int64      `json:"room_type_id"`
	StayDate   civil.Date `json:"stay_date"`
	Amount     string     `json:"amount"`
}

// RateGrid is a slice of the grid with the plan's price mode.
type RateGrid struct {
	RatePlanID     int64      `json:"rate_plan_id"`
	PriceMode      string     `json:"price_mode"`
	RoomChargeCode string     `json:"room_charge_code"`
	From           civil.Date `json:"from"`
	To             civil.Date `json:"to"` // exclusive
	Rates          []RateCell `json:"rates"`
}

// FillInput sets one amount for every listed room type on every selected night of [From, To).
type FillInput struct {
	RatePlanID  int64
	RoomTypeIDs []int64
	From        civil.Date
	To          civil.Date
	Weekdays    []string // MON..SUN; empty = every day
	Amount      string
}

// FillResult reports what a bulk fill wrote.
type FillResult struct {
	UpdatedNights int64 `json:"updated_nights"` // nights written (new or overwritten)
	CreatedNights int64 `json:"created_nights"` // of which did not have a rate before
}

// ValidateSpan checks a [from, to) range: to after from, at most MaxSpanDays.
func ValidateSpan(from, to civil.Date) []apperr.FieldError {
	switch {
	case from.IsZero() || to.IsZero():
		return nil
	case !to.After(from):
		return []apperr.FieldError{fieldErr("to", "OUT_OF_RANGE", "must be after from (to is exclusive)")}
	case from.DaysUntil(to) > MaxSpanDays:
		return []apperr.FieldError{fieldErr("to", "SPAN_TOO_LONG", "at most 730 days")}
	}
	return nil
}

// Validate checks the fill request; amount precision is checked against the currency by the service.
func (in FillInput) Validate() []apperr.FieldError {
	var errs []apperr.FieldError
	if in.RatePlanID < 1 {
		errs = append(errs, fieldErr("rate_plan_id", "REQUIRED", ""))
	}
	switch {
	case len(in.RoomTypeIDs) == 0:
		errs = append(errs, fieldErr("room_type_ids", "REQUIRED", "at least one room type"))
	case len(in.RoomTypeIDs) > MaxRoomTypes:
		errs = append(errs, fieldErr("room_type_ids", "TOO_MANY", "at most 50 room types"))
	default:
		seen := map[int64]bool{}
		for _, id := range in.RoomTypeIDs {
			if id < 1 || seen[id] {
				errs = append(errs, fieldErr("room_type_ids", "INVALID_VALUE", "positive, unique ids"))
				break
			}
			seen[id] = true
		}
	}
	errs = append(errs, ValidateSpan(in.From, in.To)...)
	seenDay := map[string]bool{}
	for _, w := range in.Weekdays {
		if _, ok := weekdays[w]; !ok || seenDay[w] {
			errs = append(errs, fieldErr("weekdays", "INVALID_VALUE", "unique values from MON, TUE, WED, THU, FRI, SAT, SUN"))
			break
		}
		seenDay[w] = true
	}
	return errs
}

// Dates lists the nights of [From, To) that fall on the selected weekdays, as ISO strings.
func (in FillInput) Dates() []string {
	want := map[time.Weekday]bool{}
	for _, w := range in.Weekdays {
		want[weekdays[w]] = true
	}
	var out []string
	for d := in.From; d.Before(in.To); d = d.AddDays(1) {
		if len(want) == 0 || want[d.Weekday()] {
			out = append(out, d.String())
		}
	}
	return out
}

// ParseAmount parses a grid amount: non-negative, with at most the currency's decimals (and the two the
// column stores).
func ParseAmount(s string, currencyDecimals int32) (decimal.Decimal, error) {
	d, err := money.Parse(s)
	if err != nil || d.IsNegative() {
		return decimal.Decimal{}, apperr.Invalid("not an amount")
	}
	limit := min(currencyDecimals, 2)
	if !d.Equal(d.Round(limit)) {
		return decimal.Decimal{}, apperr.Invalid("too many decimals")
	}
	return d, nil
}

// NightPrice is one priced night of a stay.
type NightPrice struct {
	Date   civil.Date
	Amount decimal.Decimal
}

// NightlyPrices is the price lookup result: the plan's nights with the code they are charged through.
type NightlyPrices struct {
	RatePlanID       int64
	RoomChargeCodeID int64
	RoomChargeCode   string
	PriceMode        string
	Nights           []NightPrice
}
