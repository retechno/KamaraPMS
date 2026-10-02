package rates

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// Adjustment types of a yield rule.
const (
	AdjustPercent = "PERCENT"
	AdjustAmount  = "AMOUNT"
)

// Limits of a rule's numbers.
var (
	hundred        = decimal.NewFromInt(100)
	maxPercentUp   = decimal.NewFromInt(1000) // +1000% at most
	minPercentDown = decimal.NewFromInt(-100) // exclusive: a price cannot be taken to nothing by a percentage
)

// YieldRule adjusts the price of a night when its conditions hold. A condition left empty always holds.
type YieldRule struct {
	ID              int64            `json:"id"`
	Code            string           `json:"code"`
	Name            string           `json:"name"`
	RatePlanID      *int64           `json:"rate_plan_id"`
	RoomTypeID      *int64           `json:"room_type_id"`
	StayFrom        *civil.Date      `json:"stay_from"`
	StayTo          *civil.Date      `json:"stay_to"`
	Weekdays        []string         `json:"weekdays"`
	OccupancyFrom   *decimal.Decimal `json:"occupancy_from"`
	OccupancyTo     *decimal.Decimal `json:"occupancy_to"`
	LeadMin         *int             `json:"lead_days_min"`
	LeadMax         *int             `json:"lead_days_max"`
	StayMin         *int             `json:"stay_nights_min"`
	StayMax         *int             `json:"stay_nights_max"`
	AdjustmentType  string           `json:"adjustment_type"`
	AdjustmentValue decimal.Decimal  `json:"adjustment_value"`
	FloorAmount     *decimal.Decimal `json:"floor_amount"`
	CapAmount       *decimal.Decimal `json:"cap_amount"`
	Priority        int              `json:"priority"`
	IsActive        bool             `json:"is_active"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
}

// NightContext is what a rule is matched against: the night, how full the property is that night (percent of the
// sellable rooms held, before this booking), the days between today and the night, and the nights of the stay.
type NightContext struct {
	RatePlanID int64
	RoomTypeID int64
	Date       civil.Date
	Occupancy  decimal.Decimal
	LeadDays   int
	StayNights int
}

var weekdayNames = [...]string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}

// UsesOccupancy reports whether the rule looks at how full the property is.
func (r YieldRule) UsesOccupancy() bool { return r.OccupancyFrom != nil || r.OccupancyTo != nil }

// Matches reports whether every condition of the rule holds for the night. The occupancy range is half open, from
// included and to excluded, except that a `to` of 100 includes a full house.
func (r YieldRule) Matches(c NightContext) bool {
	if r.RatePlanID != nil && *r.RatePlanID != c.RatePlanID {
		return false
	}
	if r.RoomTypeID != nil && *r.RoomTypeID != c.RoomTypeID {
		return false
	}
	if r.StayFrom != nil && c.Date.Before(*r.StayFrom) {
		return false
	}
	if r.StayTo != nil && c.Date.After(*r.StayTo) {
		return false
	}
	if len(r.Weekdays) > 0 {
		name := weekdayNames[c.Date.Weekday()]
		found := false
		for _, w := range r.Weekdays {
			if w == name {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if r.OccupancyFrom != nil && c.Occupancy.LessThan(*r.OccupancyFrom) {
		return false
	}
	if r.OccupancyTo != nil {
		if c.Occupancy.GreaterThan(*r.OccupancyTo) || (c.Occupancy.Equal(*r.OccupancyTo) && !r.OccupancyTo.Equal(hundred)) {
			return false
		}
	}
	if r.LeadMin != nil && c.LeadDays < *r.LeadMin {
		return false
	}
	if r.LeadMax != nil && c.LeadDays > *r.LeadMax {
		return false
	}
	if r.StayMin != nil && c.StayNights < *r.StayMin {
		return false
	}
	if r.StayMax != nil && c.StayNights > *r.StayMax {
		return false
	}
	return true
}

// Adjust returns the price after this rule: the percentage or amount added, kept within the rule's own floor and cap,
// never below zero, rounded half away from zero at the currency's decimals.
func (r YieldRule) Adjust(price decimal.Decimal, decimals int32) decimal.Decimal {
	var out decimal.Decimal
	if r.AdjustmentType == AdjustPercent {
		out = price.Mul(hundred.Add(r.AdjustmentValue)).Div(hundred)
	} else {
		out = price.Add(r.AdjustmentValue)
	}
	if r.FloorAmount != nil && out.LessThan(*r.FloorAmount) {
		out = *r.FloorAmount
	}
	if r.CapAmount != nil && out.GreaterThan(*r.CapAmount) {
		out = *r.CapAmount
	}
	if out.IsNegative() {
		out = decimal.Zero
	}
	return out.Round(decimals)
}

// SortRules orders rules the way they apply: lower priority first, then the older rule.
func SortRules(rules []YieldRule) {
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority < rules[j].Priority
		}
		return rules[i].ID < rules[j].ID
	})
}

// Step is one rule that moved a night's price.
type Step struct {
	Code   string          `json:"code"`
	Name   string          `json:"name"`
	Before decimal.Decimal `json:"before"`
	After  decimal.Decimal `json:"after"`
}

// ApplyYield runs the rules that match the night, in order, each on the price the one before left. rules must be
// sorted (SortRules). It returns the final price and the steps taken; a rule that leaves the price as it was is not
// listed.
func ApplyYield(rules []YieldRule, base decimal.Decimal, c NightContext, decimals int32) (decimal.Decimal, []Step) {
	price := base
	var steps []Step
	for _, r := range rules {
		if !r.IsActive || !r.Matches(c) {
			continue
		}
		next := r.Adjust(price, decimals)
		if next.Equal(price) {
			continue
		}
		steps = append(steps, Step{Code: r.Code, Name: r.Name, Before: price, After: next})
		price = next
	}
	return price, steps
}

// YieldRuleInput is the editable part of a rule as it arrives from the API.
type YieldRuleInput struct {
	Code            string
	Name            string
	RatePlanID      *int64
	RoomTypeID      *int64
	StayFrom        *civil.Date
	StayTo          *civil.Date
	Weekdays        []string
	OccupancyFrom   *string
	OccupancyTo     *string
	LeadMin         *int
	LeadMax         *int
	StayMin         *int
	StayMax         *int
	AdjustmentType  string
	AdjustmentValue string
	FloorAmount     *string
	CapAmount       *string
	Priority        *int
	IsActive        *bool
}

// parsedRule is a validated rule input.
type parsedRule struct {
	code, name             string
	ratePlanID, roomTypeID *int64
	stayFrom, stayTo       *civil.Date
	weekdays               []string
	occFrom, occTo         *decimal.Decimal
	leadMin, leadMax       *int
	stayMin, stayMax       *int
	adjType                string
	adjValue               decimal.Decimal
	floor, capAmount       *decimal.Decimal
	priority               int
	active                 bool
}

func parseDecimal(field, s string, scale int32) (decimal.Decimal, *apperr.FieldError) {
	d, err := decimal.NewFromString(strings.TrimSpace(s))
	if err != nil || d.Exponent() < -scale || d.Abs().GreaterThanOrEqual(maxAmount) {
		fe := fieldErr(field, "INVALID_AMOUNT", fmt.Sprintf("a number with at most %d decimals", scale))
		return decimal.Zero, &fe
	}
	return d, nil
}

func optDecimal(field string, s *string, scale int32, errs *[]apperr.FieldError) *decimal.Decimal {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	d, fe := parseDecimal(field, *s, scale)
	if fe != nil {
		*errs = append(*errs, *fe)
		return nil
	}
	return &d
}

// Bounds of the lead time and length-of-stay conditions.
const (
	maxLeadDays   = 3650
	maxStayNights = MaxLookupNights
)

func checkRange(field string, lo, hi *int, min, max int, errs *[]apperr.FieldError) {
	if lo != nil && *lo < min {
		*errs = append(*errs, fieldErr(field+"_min", "OUT_OF_RANGE", fmt.Sprintf("at least %d", min)))
	}
	if hi != nil && *hi < min {
		*errs = append(*errs, fieldErr(field+"_max", "OUT_OF_RANGE", fmt.Sprintf("at least %d", min)))
	}
	if (lo != nil && *lo > max) || (hi != nil && *hi > max) {
		*errs = append(*errs, fieldErr(field+"_max", "OUT_OF_RANGE", fmt.Sprintf("at most %d", max)))
	}
	if lo != nil && hi != nil && *lo > *hi {
		*errs = append(*errs, fieldErr(field+"_max", "OUT_OF_RANGE", "not less than the minimum"))
	}
}

// parseYieldRule validates a rule input. A rule must have an effect and must not be able to take a price below zero
// by a percentage (the engine also clamps at zero, whatever the amount).
func parseYieldRule(in YieldRuleInput, decimals int32) (parsedRule, []apperr.FieldError) {
	var errs []apperr.FieldError
	out := parsedRule{
		code: strings.ToUpper(strings.TrimSpace(in.Code)), name: strings.TrimSpace(in.Name), ratePlanID: in.RatePlanID, roomTypeID: in.RoomTypeID,
		stayFrom: in.StayFrom, stayTo: in.StayTo, leadMin: in.LeadMin, leadMax: in.LeadMax, stayMin: in.StayMin, stayMax: in.StayMax,
		adjType: strings.ToUpper(strings.TrimSpace(in.AdjustmentType)), priority: 100, active: true,
	}
	if !codePattern.MatchString(out.code) {
		errs = append(errs, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, _ or -"))
	}
	if out.name == "" || len(out.name) > 100 {
		errs = append(errs, fieldErr("name", "REQUIRED", "1-100 characters"))
	}
	if in.Priority != nil {
		out.priority = *in.Priority
		if out.priority < 0 || out.priority > 10000 {
			errs = append(errs, fieldErr("priority", "OUT_OF_RANGE", "between 0 and 10000"))
		}
	}
	if in.IsActive != nil {
		out.active = *in.IsActive
	}
	if out.stayFrom != nil && out.stayTo != nil && out.stayTo.Before(*out.stayFrom) {
		errs = append(errs, fieldErr("stay_to", "OUT_OF_RANGE", "on or after stay_from"))
	}
	seen := map[string]bool{}
	for _, w := range in.Weekdays {
		w = strings.ToUpper(strings.TrimSpace(w))
		if _, ok := weekdays[w]; !ok || seen[w] {
			errs = append(errs, fieldErr("weekdays", "INVALID_VALUE", "MON to SUN, each once"))
			break
		}
		seen[w] = true
		out.weekdays = append(out.weekdays, w)
	}
	out.occFrom = optDecimal("occupancy_from", in.OccupancyFrom, 2, &errs)
	out.occTo = optDecimal("occupancy_to", in.OccupancyTo, 2, &errs)
	if out.occFrom != nil && (out.occFrom.IsNegative() || out.occFrom.GreaterThan(hundred) || out.occFrom.Equal(hundred)) {
		errs = append(errs, fieldErr("occupancy_from", "OUT_OF_RANGE", "0 to under 100"))
	}
	if out.occTo != nil && (!out.occTo.IsPositive() || out.occTo.GreaterThan(hundred)) {
		errs = append(errs, fieldErr("occupancy_to", "OUT_OF_RANGE", "over 0, at most 100"))
	}
	if out.occFrom != nil && out.occTo != nil && !out.occFrom.LessThan(*out.occTo) {
		errs = append(errs, fieldErr("occupancy_to", "OUT_OF_RANGE", "above occupancy_from"))
	}
	checkRange("lead_days", out.leadMin, out.leadMax, 0, maxLeadDays, &errs)
	checkRange("stay_nights", out.stayMin, out.stayMax, 1, maxStayNights, &errs)

	switch out.adjType {
	case AdjustPercent, AdjustAmount:
	default:
		errs = append(errs, fieldErr("adjustment_type", "INVALID_VALUE", "PERCENT or AMOUNT"))
	}
	if v, fe := parseDecimal("adjustment_value", in.AdjustmentValue, 4); fe != nil {
		errs = append(errs, *fe)
	} else {
		out.adjValue = v
		switch {
		case v.IsZero():
			errs = append(errs, fieldErr("adjustment_value", "REQUIRED", "not zero: a rule must change the price"))
		case out.adjType == AdjustPercent && (!v.GreaterThan(minPercentDown) || v.GreaterThan(maxPercentUp)):
			errs = append(errs, fieldErr("adjustment_value", "OUT_OF_RANGE", "a percentage above -100 and at most 1000"))
		case out.adjType == AdjustAmount && v.Exponent() < -decimals:
			errs = append(errs, fieldErr("adjustment_value", "INVALID_AMOUNT", fmt.Sprintf("at most %d decimals", decimals)))
		}
	}
	out.floor = optDecimal("floor_amount", in.FloorAmount, decimals, &errs)
	out.capAmount = optDecimal("cap_amount", in.CapAmount, decimals, &errs)
	if out.floor != nil && out.floor.IsNegative() {
		errs = append(errs, fieldErr("floor_amount", "OUT_OF_RANGE", "not negative"))
	}
	if out.capAmount != nil && out.capAmount.IsNegative() {
		errs = append(errs, fieldErr("cap_amount", "OUT_OF_RANGE", "not negative"))
	}
	if out.floor != nil && out.capAmount != nil && out.floor.GreaterThan(*out.capAmount) {
		errs = append(errs, fieldErr("cap_amount", "OUT_OF_RANGE", "not below floor_amount"))
	}
	return out, errs
}
