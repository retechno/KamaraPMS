package billingconfig

import (
	"context"
	"errors"

	"github.com/shopspring/decimal"

	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// ChargeRequest asks for the breakdown of one charge.
type ChargeRequest struct {
	PropertyID   int64
	ChargeCodeID int64
	Quantity     decimal.Decimal
	UnitPrice    decimal.Decimal       // signed: a negative price calculates a credit (adjustments)
	PriceMode    *chargecalc.PriceMode // nil = the charge code's price mode
	Discount     decimal.Decimal       // magnitude, at most the currency's decimals
}

// Calculate is the ChargeCalculationService: it resolves the charge code's active, ordered rules and the
// property's currency precision, and hands them to the pure engine. It reads committed configuration and
// writes nothing, so it runs inside any caller's transaction (folio posting, estimates) or none (preview).
//
// The caller must be able to access the property. The charge code must belong to it and be active.
func (s *Service) Calculate(ctx context.Context, req ChargeRequest) (chargecalc.Breakdown, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return chargecalc.Breakdown{}, err
	}
	decimals, err := s.currencyDecimals(ctx, req.PropertyID) // also answers "is this property visible to the caller"
	if err != nil {
		return chargecalc.Breakdown{}, err
	}
	rules, err := s.ResolveRules(ctx, p.TenantID, req.PropertyID, req.ChargeCodeID)
	if err != nil {
		return chargecalc.Breakdown{}, err
	}
	if !rules.IsActive {
		return chargecalc.Breakdown{}, apperr.Conflict("CHARGE_CODE_INACTIVE", "the charge code is inactive").WithContext("charge_code", rules.Code)
	}

	mode := chargecalc.PriceMode(rules.PriceMode)
	if req.PriceMode != nil {
		mode = *req.PriceMode
	}
	in := chargecalc.Input{
		Quantity: req.Quantity, UnitPrice: req.UnitPrice, Discount: req.Discount, PriceMode: mode, Decimals: decimals,
		ServiceCharges: make([]chargecalc.ServiceChargeRule, len(rules.ServiceCharges)), Taxes: make([]chargecalc.TaxRule, len(rules.Taxes)),
	}
	for i, r := range rules.ServiceCharges {
		in.ServiceCharges[i] = chargecalc.ServiceChargeRule{ID: r.ID, Code: r.Code, Name: r.Name, Rate: r.Rate, Sequence: r.Sequence}
	}
	for i, r := range rules.Taxes {
		in.Taxes[i] = chargecalc.TaxRule{ID: r.ID, Code: r.Code, Name: r.Name, Rate: r.Rate, OnService: r.OnService, Sequence: r.Sequence}
	}

	b, err := chargecalc.Calculate(in)
	var bad *chargecalc.InputError
	if errors.As(err, &bad) {
		field := bad.Field
		if field == "discount" {
			field = "discount_amount" // the API's name for it
		}
		return chargecalc.Breakdown{}, apperr.Invalid("the calculation is invalid", apperr.FieldError{Field: field, Code: "INVALID_VALUE", Message: bad.Message})
	}
	return b, err
}
