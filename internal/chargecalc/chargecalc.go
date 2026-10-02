// Package chargecalc is the Charge Calculation Engine: the only place that multiplies an amount by a rate.
//
// It is pure. It knows numbers and rules, and nothing about charge codes, properties, databases, clocks,
// countries or charge types. Callers (ChargeCalculationService, and through it the folio posting service)
// resolve the rules and the currency precision and pass them in. See docs/architecture/03-financial-engines.md
// Step 7 for the rules and worked examples.
//
// Conventions:
//   - every line amount (the base, each component, and the net extracted from an inclusive price) is rounded
//     at the currency precision, half away from zero, so negating the input negates every output;
//   - amounts are signed: a negative quantity or unit price calculates a credit (used by adjustments). A
//     discount is a non-negative magnitude that always reduces the amount towards zero;
//   - taxes are not compounded: a tax is levied on the net, plus the service charges when the tax says so.
package chargecalc

import (
	"errors"
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// PriceMode says whether a price excludes or already contains the mapped service charges and taxes.
type PriceMode string

// Price modes. A future mode (for example MIXED) is a new case here and a CHECK change in the schema.
const (
	Exclusive PriceMode = "EXCLUSIVE"
	Inclusive PriceMode = "INCLUSIVE"
)

// Component types.
const (
	TypeServiceCharge = "SERVICE_CHARGE"
	TypeTax           = "TAX"
)

// MaxDecimals is the highest supported currency precision.
const MaxDecimals = 3

var (
	zero    = decimal.Zero
	one     = decimal.NewFromInt(1)
	hundred = decimal.NewFromInt(100)
)

// ServiceChargeRule is a service charge to apply, as a percentage of the net amount.
type ServiceChargeRule struct {
	ID       int64
	Code     string
	Name     string
	Rate     decimal.Decimal // percent, 0..100
	Sequence int
}

// TaxRule is a tax to apply, as a percentage of the net amount (plus the service charges if OnService).
type TaxRule struct {
	ID        int64
	Code      string
	Name      string
	Rate      decimal.Decimal // percent, 0..100
	OnService bool
	Sequence  int
}

// Input is everything the engine needs.
type Input struct {
	Quantity  decimal.Decimal // not zero; negative for a credit
	UnitPrice decimal.Decimal // in PriceMode terms; negative for a credit
	Discount  decimal.Decimal // magnitude in [0, |base|], at most Decimals decimals
	PriceMode PriceMode

	ServiceCharges []ServiceChargeRule // calculated in Sequence order
	Taxes          []TaxRule           // calculated in Sequence order
	Decimals       int32               // currency precision, 0..MaxDecimals

	// ExemptTaxIDs is reserved for tax exemptions. It is not supported yet: a non-empty list is rejected
	// rather than silently ignored.
	ExemptTaxIDs []int64
}

// Component is one calculated service charge or tax.
type Component struct {
	Type       string // TypeServiceCharge or TypeTax
	RuleID     int64
	Code       string
	Name       string
	Rate       decimal.Decimal
	OnService  *bool // taxes only
	BaseAmount decimal.Decimal
	Amount     decimal.Decimal
	Sequence   int
}

// Breakdown is the full result. NetAmount includes RoundingAdjustment.
type Breakdown struct {
	PriceMode PriceMode
	Decimals  int32
	Quantity  decimal.Decimal
	UnitPrice decimal.Decimal

	BaseAmount         decimal.Decimal // round(quantity × unit price), as quoted
	Discount           decimal.Decimal // signed like BaseAmount, so that Net = Base − Discount when exclusive
	NetAmount          decimal.Decimal // net revenue
	RoundingAdjustment decimal.Decimal // always 0 when exclusive

	ServiceComponents []Component
	TaxComponents     []Component
	ServiceTotal      decimal.Decimal
	TaxTotal          decimal.Decimal
	TaxableAmount     decimal.Decimal // the largest tax base, for display
	TotalAmount       decimal.Decimal // Net + Service + Tax; equals the quoted gross when inclusive
}

// ErrInvalid is wrapped by every validation error, so callers can map them to a 422.
var ErrInvalid = errors.New("chargecalc: invalid input")

// InputError is a validation error naming the offending input (quantity, discount, price_mode, decimals,
// tax, service_charge, exempt_tax_ids). It satisfies errors.Is(err, ErrInvalid).
type InputError struct {
	Field   string
	Message string
}

func (e *InputError) Error() string {
	return "chargecalc: invalid input: " + e.Field + ": " + e.Message
}
func (e *InputError) Is(target error) bool { return target == ErrInvalid }

func invalid(field, format string, a ...any) error {
	return &InputError{Field: field, Message: fmt.Sprintf(format, a...)}
}

// Validate checks an Input without calculating.
func (in Input) Validate() error {
	if in.Decimals < 0 || in.Decimals > MaxDecimals {
		return invalid("decimals", "currency decimals must be between 0 and %d, got %d", MaxDecimals, in.Decimals)
	}
	if in.PriceMode != Exclusive && in.PriceMode != Inclusive {
		return invalid("price_mode", "unknown price mode %q", in.PriceMode)
	}
	if in.Quantity.IsZero() {
		return invalid("quantity", "quantity must not be zero")
	}
	if len(in.ExemptTaxIDs) > 0 {
		return invalid("exempt_tax_ids", "tax exemptions are not supported yet")
	}
	base := round(in.Quantity.Mul(in.UnitPrice), in.Decimals)
	if in.Discount.IsNegative() || in.Discount.GreaterThan(base.Abs()) {
		return invalid("discount", "discount %s must be between 0 and the amount %s", in.Discount, base.Abs())
	}
	if !in.Discount.Equal(round(in.Discount, in.Decimals)) {
		return invalid("discount", "discount %s has more than %d decimals", in.Discount, in.Decimals)
	}
	seen := map[int64]bool{}
	for _, s := range in.ServiceCharges {
		if err := checkRate("service_charge", "service charge", s.ID, s.Rate, seen); err != nil {
			return err
		}
	}
	seen = map[int64]bool{}
	for _, t := range in.Taxes {
		if err := checkRate("tax", "tax", t.ID, t.Rate, seen); err != nil {
			return err
		}
	}
	return nil
}

func checkRate(field, kind string, id int64, rate decimal.Decimal, seen map[int64]bool) error {
	if rate.IsNegative() || rate.GreaterThan(hundred) {
		return invalid(field, "%s %d: rate %s is outside 0..100", kind, id, rate)
	}
	if seen[id] {
		return invalid(field, "%s %d is listed twice", kind, id)
	}
	seen[id] = true
	return nil
}

// round rounds half away from zero at the currency precision (shopspring's Round does exactly that).
func round(d decimal.Decimal, decimals int32) decimal.Decimal { return d.Round(decimals) }

// pct is amount × rate / 100, unrounded and exact (both operands have few decimals).
func pct(amount, rate decimal.Decimal) decimal.Decimal { return amount.Mul(rate).Div(hundred) }

// Calculate runs the engine. The same input always gives the same breakdown.
func Calculate(in Input) (Breakdown, error) {
	if err := in.Validate(); err != nil {
		return Breakdown{}, err
	}
	services := append([]ServiceChargeRule(nil), in.ServiceCharges...)
	sort.SliceStable(services, func(i, j int) bool { return services[i].Sequence < services[j].Sequence })
	taxes := append([]TaxRule(nil), in.Taxes...)
	sort.SliceStable(taxes, func(i, j int) bool { return taxes[i].Sequence < taxes[j].Sequence })

	quoted := round(in.Quantity.Mul(in.UnitPrice), in.Decimals)
	sign := one
	if quoted.IsNegative() {
		sign = one.Neg()
	}
	discount := in.Discount.Mul(sign) // reduces the amount towards zero, whatever its sign
	amount := quoted.Sub(discount)    // EXCLUSIVE: the net; INCLUSIVE: the gross the guest pays

	net := amount
	if in.PriceMode == Inclusive {
		net = extractNet(amount, services, taxes, in.Decimals)
	}

	b := Breakdown{
		PriceMode: in.PriceMode, Decimals: in.Decimals, Quantity: in.Quantity, UnitPrice: in.UnitPrice,
		BaseAmount: quoted, Discount: discount,
		ServiceComponents: []Component{}, TaxComponents: []Component{},
	}

	serviceSum := zero
	for _, s := range services {
		c := Component{
			Type: TypeServiceCharge, RuleID: s.ID, Code: s.Code, Name: s.Name, Rate: s.Rate, Sequence: s.Sequence,
			BaseAmount: net, Amount: round(pct(net, s.Rate), in.Decimals),
		}
		serviceSum = serviceSum.Add(c.Amount)
		b.ServiceComponents = append(b.ServiceComponents, c)
	}
	taxSum, taxable := zero, zero
	for _, t := range taxes {
		base := net
		if t.OnService {
			base = net.Add(serviceSum)
		}
		onService := t.OnService
		c := Component{
			Type: TypeTax, RuleID: t.ID, Code: t.Code, Name: t.Name, Rate: t.Rate, OnService: &onService, Sequence: t.Sequence,
			BaseAmount: base, Amount: round(pct(base, t.Rate), in.Decimals),
		}
		taxSum = taxSum.Add(c.Amount)
		if base.Abs().GreaterThan(taxable.Abs()) {
			taxable = base
		}
		b.TaxComponents = append(b.TaxComponents, c)
	}

	b.ServiceTotal, b.TaxTotal, b.TaxableAmount = serviceSum, taxSum, taxable
	if in.PriceMode == Inclusive {
		// The guest pays exactly the quoted price. Whatever rounding left over goes into net revenue and is
		// recorded, so service and tax stay exactly round(base × rate) of their own base.
		b.RoundingAdjustment = amount.Sub(net.Add(serviceSum).Add(taxSum))
		b.NetAmount = net.Add(b.RoundingAdjustment)
		b.TotalAmount = amount
	} else {
		b.NetAmount = net
		b.TotalAmount = net.Add(serviceSum).Add(taxSum)
	}
	return b, nil
}

// extractNet returns net₀ = round(gross / F) where F = 1 + s + Σ_j rate_j × (1 + (on_service_j ? s : 0)),
// s being the sum of the service rates (all as fractions). The division is exact before rounding.
func extractNet(gross decimal.Decimal, services []ServiceChargeRule, taxes []TaxRule, decimals int32) decimal.Decimal {
	s := zero
	for _, sc := range services {
		s = s.Add(sc.Rate.Div(hundred))
	}
	f := one.Add(s)
	for _, t := range taxes {
		factor := one
		if t.OnService {
			factor = one.Add(s)
		}
		f = f.Add(t.Rate.Div(hundred).Mul(factor))
	}
	return gross.DivRound(f, decimals)
}
