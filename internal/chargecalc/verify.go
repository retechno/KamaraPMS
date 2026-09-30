package chargecalc

import "github.com/shopspring/decimal"

// Difference is one value that differs between a claimed and a recalculated breakdown.
type Difference struct {
	Field    string
	Claimed  decimal.Decimal
	Expected decimal.Decimal
}

// Verify recalculates in and compares the claimed breakdown with it, amount by amount and component by
// component (a component is identified by its type and rule id). An amount matches when it is within
// tolerance of the recalculated one. It returns the differences, empty when the claim is acceptable.
//
// It is for future integrations (a POS that calculates its own tax): the claim is checked, never trusted.
func Verify(in Input, claimed Breakdown, tolerance decimal.Decimal) ([]Difference, error) {
	want, err := Calculate(in)
	if err != nil {
		return nil, err
	}
	tol := tolerance.Abs()
	var diffs []Difference
	cmp := func(field string, got, exp decimal.Decimal) {
		if got.Sub(exp).Abs().GreaterThan(tol) {
			diffs = append(diffs, Difference{Field: field, Claimed: got, Expected: exp})
		}
	}
	cmp("base_amount", claimed.BaseAmount, want.BaseAmount)
	cmp("discount", claimed.Discount, want.Discount)
	cmp("net_amount", claimed.NetAmount, want.NetAmount)
	cmp("rounding_adjustment", claimed.RoundingAdjustment, want.RoundingAdjustment)
	cmp("service_charge_total", claimed.ServiceTotal, want.ServiceTotal)
	cmp("tax_total", claimed.TaxTotal, want.TaxTotal)
	cmp("total_amount", claimed.TotalAmount, want.TotalAmount)

	compare := func(kind string, got, exp []Component) {
		byID := map[int64]Component{}
		for _, c := range got {
			byID[c.RuleID] = c
		}
		for _, e := range exp {
			c, ok := byID[e.RuleID]
			if !ok {
				diffs = append(diffs, Difference{Field: kind + ":" + e.Code + ":missing", Expected: e.Amount})
				continue
			}
			cmp(kind+":"+e.Code+":base_amount", c.BaseAmount, e.BaseAmount)
			cmp(kind+":"+e.Code+":amount", c.Amount, e.Amount)
			delete(byID, e.RuleID)
		}
		for _, c := range got {
			if _, extra := byID[c.RuleID]; extra {
				diffs = append(diffs, Difference{Field: kind + ":" + c.Code + ":unexpected", Claimed: c.Amount})
			}
		}
	}
	compare("service", claimed.ServiceComponents, want.ServiceComponents)
	compare("tax", claimed.TaxComponents, want.TaxComponents)
	return diffs, nil
}
