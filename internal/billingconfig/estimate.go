package billingconfig

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/chargecalc"
	"kamarapms/internal/platform/apperr"
)

// NightCharge is one night to estimate: the charge code it is posted through, how its amount is read, and
// the agreed amount (quantity 1, no discount: the nightly snapshot already holds the agreed price).
type NightCharge struct {
	ChargeCodeID int64
	PriceMode    chargecalc.PriceMode
	Amount       decimal.Decimal
}

// EstimateTotals is the sum of the engine's breakdowns.
type EstimateTotals struct {
	Net     decimal.Decimal
	Service decimal.Decimal
	Tax     decimal.Decimal
	Total   decimal.Decimal
}

// Estimate runs the Charge Calculation Engine over every night and sums the results. It is for estimates
// (availability search, reservation detail): each night goes through chargecalc.Calculate with the charge
// code's current rules, so it can differ from what was estimated earlier if the rules changed. The rules of
// a charge code are resolved once. Nothing is stored.
func (s *Service) Estimate(ctx context.Context, tenantID, propertyID int64, nights []NightCharge) (EstimateTotals, error) {
	var out EstimateTotals
	if len(nights) == 0 {
		return out, nil
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return out, err
	}
	rules := map[int64]ChargeRules{}
	for _, n := range nights {
		r, ok := rules[n.ChargeCodeID]
		if !ok {
			if r, err = s.ResolveRules(ctx, tenantID, propertyID, n.ChargeCodeID); err != nil {
				return out, err
			}
			rules[n.ChargeCodeID] = r
		}
		in := chargecalc.Input{
			Quantity: decimal.NewFromInt(1), UnitPrice: n.Amount, PriceMode: n.PriceMode, Decimals: decimals,
			ServiceCharges: make([]chargecalc.ServiceChargeRule, len(r.ServiceCharges)), Taxes: make([]chargecalc.TaxRule, len(r.Taxes)),
		}
		for i, sc := range r.ServiceCharges {
			in.ServiceCharges[i] = chargecalc.ServiceChargeRule{ID: sc.ID, Code: sc.Code, Name: sc.Name, Rate: sc.Rate, Sequence: sc.Sequence}
		}
		for i, t := range r.Taxes {
			in.Taxes[i] = chargecalc.TaxRule{ID: t.ID, Code: t.Code, Name: t.Name, Rate: t.Rate, OnService: t.OnService, Sequence: t.Sequence}
		}
		b, err := chargecalc.Calculate(in)
		if err != nil {
			return EstimateTotals{}, apperr.Internal(err)
		}
		out.Net, out.Service, out.Tax, out.Total = out.Net.Add(b.NetAmount), out.Service.Add(b.ServiceTotal), out.Tax.Add(b.TaxTotal), out.Total.Add(b.TotalAmount)
	}
	return out, nil
}
