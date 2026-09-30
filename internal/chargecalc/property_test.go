package chargecalc_test

import (
	"math/rand/v2"
	"testing"

	"github.com/shopspring/decimal"

	. "kamarapms/internal/chargecalc"
)

// Property-based tests: many random inputs from a fixed seed (reproducible), checking the invariants that
// must hold for every input rather than a few hand-picked ones.

const cases = 30000

type gen struct{ r *rand.Rand }

func (g gen) intn(n int) int { return g.r.IntN(n) }

// rate: 0..100 with up to four decimals, biased towards realistic small rates.
func (g gen) rate() decimal.Decimal {
	if g.intn(4) == 0 {
		return decimal.New(int64(g.intn(1_000_001)), -4) // 0..100.0000
	}
	return decimal.New(int64(g.intn(300_001)), -4).Div(decimal.NewFromInt(3)).Round(4) // 0..10 mostly
}

func (g gen) realisticRate(max int) decimal.Decimal {
	return decimal.New(int64(g.intn(max*100+1)), -2) // 0..max with two decimals
}

func (g gen) input(realistic bool) Input {
	decimals := int32(g.intn(4))
	price := decimal.New(int64(g.intn(2_000_000_000)), -int32(g.intn(5))) // up to 4 decimals of precision in the price
	qty := decimal.New(int64(1+g.intn(30_000)), -int32(g.intn(4)))
	in := Input{Quantity: qty, UnitPrice: price, Decimals: decimals, PriceMode: Exclusive}
	if g.intn(2) == 0 {
		in.PriceMode = Inclusive
	}
	for i := range g.intn(4) {
		r := g.rate()
		if realistic {
			r = g.realisticRate(20)
		}
		in.ServiceCharges = append(in.ServiceCharges, ServiceChargeRule{ID: int64(i + 1), Code: "S", Rate: r, Sequence: i + 1})
	}
	for i := range g.intn(4) {
		r := g.rate()
		if realistic {
			r = g.realisticRate(25)
		}
		in.Taxes = append(in.Taxes, TaxRule{ID: int64(i + 1), Code: "T", Rate: r, OnService: g.intn(2) == 0, Sequence: i + 1})
	}
	// A discount between 0 and the whole amount, at currency precision.
	base := in.Quantity.Mul(in.UnitPrice).Round(decimals).Abs()
	if g.intn(2) == 0 && base.Sign() > 0 {
		in.Discount = base.Mul(decimal.New(int64(g.intn(1001)), -3)).Round(decimals)
	}
	return in
}

func forAll(t *testing.T, seed uint64, realistic bool, check func(t *testing.T, in Input, b Breakdown)) {
	t.Helper()
	g := gen{rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))}
	for i := range cases {
		in := g.input(realistic)
		if in.Validate() != nil {
			continue // a random draw can be invalid (for example a discount rounding above the amount); skip it
		}
		b, err := Calculate(in)
		if err != nil {
			t.Fatalf("case %d: valid input rejected: %v\n%+v", i, err, in)
		}
		check(t, in, b)
		if t.Failed() {
			t.Fatalf("case %d failed for %+v", i, in)
		}
	}
}

func ulp(decimals int32) decimal.Decimal { return decimal.New(1, -decimals) }

func TestInclusiveTotalIsAlwaysTheQuotedPrice(t *testing.T) {
	forAll(t, 1, false, func(t *testing.T, in Input, b Breakdown) {
		if in.PriceMode != Inclusive {
			return
		}
		gross := in.Quantity.Mul(in.UnitPrice).Round(in.Decimals).Sub(in.Discount.Mul(signOf(b.BaseAmount)))
		if !b.TotalAmount.Equal(gross) {
			t.Errorf("total %s != quoted %s", b.TotalAmount, gross)
		}
		if !b.NetAmount.Add(b.ServiceTotal).Add(b.TaxTotal).Equal(b.TotalAmount) {
			t.Errorf("net + service + tax = %s, total %s", b.NetAmount.Add(b.ServiceTotal).Add(b.TaxTotal), b.TotalAmount)
		}
	})
}

func TestExclusiveNetIsBaseMinusDiscount(t *testing.T) {
	forAll(t, 2, false, func(t *testing.T, in Input, b Breakdown) {
		if in.PriceMode != Exclusive {
			return
		}
		if !b.NetAmount.Equal(b.BaseAmount.Sub(b.Discount)) {
			t.Errorf("net %s != base %s - discount %s", b.NetAmount, b.BaseAmount, b.Discount)
		}
		if !b.RoundingAdjustment.IsZero() {
			t.Errorf("exclusive prices have no rounding adjustment: %s", b.RoundingAdjustment)
		}
		if !b.TotalAmount.Equal(b.NetAmount.Add(b.ServiceTotal).Add(b.TaxTotal)) {
			t.Errorf("total %s != net + service + tax", b.TotalAmount)
		}
	})
}

func TestNegationSymmetry(t *testing.T) {
	forAll(t, 3, false, func(t *testing.T, in Input, b Breakdown) {
		neg := in
		neg.UnitPrice = in.UnitPrice.Neg()
		nb, err := Calculate(neg)
		if err != nil {
			t.Errorf("the credit of a valid charge must be valid: %v", err)
			return
		}
		pairs := map[string][2]decimal.Decimal{
			"base": {b.BaseAmount, nb.BaseAmount}, "discount": {b.Discount, nb.Discount}, "net": {b.NetAmount, nb.NetAmount},
			"adjustment": {b.RoundingAdjustment, nb.RoundingAdjustment}, "service": {b.ServiceTotal, nb.ServiceTotal},
			"tax": {b.TaxTotal, nb.TaxTotal}, "taxable": {b.TaxableAmount, nb.TaxableAmount}, "total": {b.TotalAmount, nb.TotalAmount},
		}
		for name, p := range pairs {
			if !p[0].Equal(p[1].Neg()) {
				t.Errorf("%s: %s and its credit %s are not mirror images", name, p[0], p[1])
			}
		}
		for i := range b.TaxComponents {
			if !b.TaxComponents[i].Amount.Equal(nb.TaxComponents[i].Amount.Neg()) || !b.TaxComponents[i].BaseAmount.Equal(nb.TaxComponents[i].BaseAmount.Neg()) {
				t.Errorf("tax component %d is not mirrored", i)
			}
		}
		for i := range b.ServiceComponents {
			if !b.ServiceComponents[i].Amount.Equal(nb.ServiceComponents[i].Amount.Neg()) {
				t.Errorf("service component %d is not mirrored", i)
			}
		}
	})
	// Negating the quantity is the same as negating the price.
	forAll(t, 4, false, func(t *testing.T, in Input, b Breakdown) {
		q := in
		q.Quantity = in.Quantity.Neg()
		qb, err := Calculate(q)
		if err != nil || !qb.TotalAmount.Equal(b.TotalAmount.Neg()) {
			t.Errorf("negative quantity: %v %s vs %s", err, qb.TotalAmount, b.TotalAmount)
		}
	})
}

// Every component is exactly round(its own base × rate): what an auditor recomputes. In an inclusive price
// the bases are the extracted net₀, so the residual never distorts service or tax.
func TestComponentsAreExactlyRoundedPercentagesOfTheirBase(t *testing.T) {
	forAll(t, 5, false, func(t *testing.T, in Input, b Breakdown) {
		hundred := decimal.NewFromInt(100)
		check := func(c Component) {
			want := c.BaseAmount.Mul(c.Rate).Div(hundred).Round(in.Decimals)
			if !c.Amount.Equal(want) {
				t.Errorf("%s %d: amount %s, want round(%s × %s%%) = %s", c.Type, c.RuleID, c.Amount, c.BaseAmount, c.Rate, want)
			}
		}
		serviceSum, taxSum := decimal.Zero, decimal.Zero
		for _, c := range b.ServiceComponents {
			check(c)
			serviceSum = serviceSum.Add(c.Amount)
			wantBase := b.NetAmount.Sub(b.RoundingAdjustment)
			if !c.BaseAmount.Equal(wantBase) {
				t.Errorf("service base %s, want the net before the rounding adjustment %s", c.BaseAmount, wantBase)
			}
		}
		for _, c := range b.TaxComponents {
			check(c)
			taxSum = taxSum.Add(c.Amount)
			wantBase := b.NetAmount.Sub(b.RoundingAdjustment)
			if *c.OnService {
				wantBase = wantBase.Add(b.ServiceTotal)
			}
			if !c.BaseAmount.Equal(wantBase) {
				t.Errorf("tax base %s, want %s", c.BaseAmount, wantBase)
			}
		}
		if !serviceSum.Equal(b.ServiceTotal) || !taxSum.Equal(b.TaxTotal) {
			t.Errorf("totals differ from the component sums")
		}
	})
}

func TestNoAmountHasMoreDecimalsThanTheCurrency(t *testing.T) {
	forAll(t, 6, false, func(t *testing.T, in Input, b Breakdown) {
		all := []decimal.Decimal{b.BaseAmount, b.Discount, b.NetAmount, b.RoundingAdjustment, b.ServiceTotal, b.TaxTotal, b.TaxableAmount, b.TotalAmount}
		for _, c := range append(append([]Component{}, b.ServiceComponents...), b.TaxComponents...) {
			all = append(all, c.BaseAmount, c.Amount)
		}
		for _, a := range all {
			if !a.Equal(a.Round(in.Decimals)) {
				t.Errorf("amount %s has more than %d decimals", a, in.Decimals)
			}
		}
	})
}

// For realistic rates the rounding adjustment is a few units of the smallest currency unit, never more.
func TestRoundingAdjustmentIsSmallForRealisticRates(t *testing.T) {
	forAll(t, 7, true, func(t *testing.T, in Input, b Breakdown) {
		if in.PriceMode != Inclusive {
			return
		}
		bound := ulp(in.Decimals).Mul(decimal.NewFromInt(int64(4 + 2*(len(in.ServiceCharges)+len(in.Taxes)))))
		if b.RoundingAdjustment.Abs().GreaterThan(bound) {
			t.Errorf("rounding adjustment %s exceeds %s", b.RoundingAdjustment, bound)
		}
	})
}

func TestNoRulesMeansNetEqualsTheAmount(t *testing.T) {
	forAll(t, 8, false, func(t *testing.T, in Input, _ Breakdown) {
		in.ServiceCharges, in.Taxes = nil, nil
		b, err := Calculate(in)
		if err != nil {
			t.Fatal(err)
		}
		want := b.BaseAmount.Sub(b.Discount)
		if !b.NetAmount.Equal(want) || !b.TotalAmount.Equal(want) || !b.RoundingAdjustment.IsZero() || !b.ServiceTotal.IsZero() || !b.TaxTotal.IsZero() || !b.TaxableAmount.IsZero() {
			t.Errorf("exempt result: %+v", b)
		}
	})
}

// The order rules are listed in does not matter; their Sequence does. Calculation is deterministic.
func TestRuleOrderComesFromSequenceNotFromTheList(t *testing.T) {
	forAll(t, 9, false, func(t *testing.T, in Input, b Breakdown) {
		rev := in
		rev.Taxes = reversed(in.Taxes)
		rev.ServiceCharges = reversedS(in.ServiceCharges)
		rb, err := Calculate(rev)
		if err != nil || !rb.TotalAmount.Equal(b.TotalAmount) || !rb.NetAmount.Equal(b.NetAmount) || len(rb.TaxComponents) != len(b.TaxComponents) {
			t.Errorf("reordering the input changed the result: %v", err)
			return
		}
		for i := range b.TaxComponents {
			if rb.TaxComponents[i].RuleID != b.TaxComponents[i].RuleID || !rb.TaxComponents[i].Amount.Equal(b.TaxComponents[i].Amount) {
				t.Errorf("tax component %d differs", i)
			}
		}
		again, _ := Calculate(in)
		if !again.TotalAmount.Equal(b.TotalAmount) {
			t.Error("not deterministic")
		}
	})
}

// A claim produced by the engine always verifies against itself.
func TestVerifyAcceptsTheEnginesOwnResult(t *testing.T) {
	forAll(t, 10, false, func(t *testing.T, in Input, b Breakdown) {
		if diffs, err := Verify(in, b, decimal.Zero); err != nil || len(diffs) != 0 {
			t.Errorf("self-verification: %v %v", err, diffs)
		}
	})
}

func signOf(d decimal.Decimal) decimal.Decimal {
	if d.IsNegative() {
		return decimal.NewFromInt(-1)
	}
	return decimal.NewFromInt(1)
}

func reversed(in []TaxRule) []TaxRule {
	out := make([]TaxRule, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

func reversedS(in []ServiceChargeRule) []ServiceChargeRule {
	out := make([]ServiceChargeRule, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}
