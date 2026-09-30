package chargecalc_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	. "kamarapms/internal/chargecalc"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

var (
	svc10       = ServiceChargeRule{ID: 1, Code: "SVC", Name: "Service 10%", Rate: d("10"), Sequence: 1}
	vatOnSvc    = TaxRule{ID: 1, Code: "VAT", Name: "VAT 11%", Rate: d("11"), OnService: true, Sequence: 1}
	vatNoOnSvc  = TaxRule{ID: 1, Code: "VAT", Name: "VAT 11%", Rate: d("11"), OnService: false, Sequence: 1}
	cityNoOnSvc = TaxRule{ID: 2, Code: "CITY", Name: "City tax 1%", Rate: d("1"), Sequence: 2}
)

// The worked examples of docs/architecture/03-financial-engines.md §7.5 (IDR, decimals 0).
func TestWorkedExamples(t *testing.T) {
	cases := []struct {
		name                                   string
		in                                     Input
		net, service, taxable, tax, adj, total string
		serviceComponents, taxComponents       int
	}{
		{"exclusive, tax on service", Input{Quantity: d("1"), UnitPrice: d("1000000"), PriceMode: Exclusive, ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatOnSvc}},
			"1000000", "100000", "1100000", "121000", "0", "1221000", 1, 1},
		{"exclusive, no tax on service", Input{Quantity: d("1"), UnitPrice: d("1000000"), PriceMode: Exclusive, ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatNoOnSvc}},
			"1000000", "100000", "1000000", "110000", "0", "1210000", 1, 1},
		{"inclusive, tax on service", Input{Quantity: d("1"), UnitPrice: d("1110000"), PriceMode: Inclusive, ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatOnSvc}},
			"909091", "90909", "1000000", "110000", "0", "1110000", 1, 1},
		{"inclusive, VAT only", Input{Quantity: d("1"), UnitPrice: d("1110000"), PriceMode: Inclusive, Taxes: []TaxRule{vatNoOnSvc}},
			"1000000", "0", "1000000", "110000", "0", "1110000", 0, 1},
		{"exclusive with a discount of 100,000", Input{Quantity: d("1"), UnitPrice: d("1000000"), Discount: d("100000"), PriceMode: Exclusive, ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatOnSvc}},
			"900000", "90000", "990000", "108900", "0", "1098900", 1, 1},
		{"exempt room code (no rules), exclusive", Input{Quantity: d("1"), UnitPrice: d("1000000"), PriceMode: Exclusive},
			"1000000", "0", "0", "0", "0", "1000000", 0, 0},
		{"exempt room code (no rules), inclusive", Input{Quantity: d("1"), UnitPrice: d("1000000"), PriceMode: Inclusive},
			"1000000", "0", "0", "0", "0", "1000000", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.in.Decimals = 0
			b, err := Calculate(c.in)
			if err != nil {
				t.Fatal(err)
			}
			for field, pair := range map[string][2]decimal.Decimal{
				"net": {b.NetAmount, d(c.net)}, "service": {b.ServiceTotal, d(c.service)}, "taxable": {b.TaxableAmount, d(c.taxable)},
				"tax": {b.TaxTotal, d(c.tax)}, "rounding adjustment": {b.RoundingAdjustment, d(c.adj)}, "total": {b.TotalAmount, d(c.total)},
			} {
				if !pair[0].Equal(pair[1]) {
					t.Errorf("%s = %s, want %s", field, pair[0], pair[1])
				}
			}
			if len(b.ServiceComponents) != c.serviceComponents || len(b.TaxComponents) != c.taxComponents {
				t.Errorf("components: %d service, %d tax", len(b.ServiceComponents), len(b.TaxComponents))
			}
		})
	}
}

// The rounding-residual example of §7.5: inclusive 7.00 USD, service 10%, VAT 11% on service.
func TestInclusiveResidualExampleInUSD(t *testing.T) {
	b, err := Calculate(Input{Quantity: d("1"), UnitPrice: d("7.00"), PriceMode: Inclusive, Decimals: 2,
		ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatOnSvc}})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"service": "0.57", "tax": "0.69", "taxable": "6.30", "adjustment": "0.01", "net": "5.74", "total": "7.00"}
	got := map[string]decimal.Decimal{"service": b.ServiceTotal, "tax": b.TaxTotal, "taxable": b.TaxableAmount, "adjustment": b.RoundingAdjustment, "net": b.NetAmount, "total": b.TotalAmount}
	for k, v := range want {
		if !got[k].Equal(d(v)) {
			t.Errorf("%s = %s, want %s", k, got[k], v)
		}
	}
	// Service and tax are exactly round(base × rate) of their own base: the residual is never pushed into them.
	if b.ServiceComponents[0].BaseAmount.String() != "5.73" || b.TaxComponents[0].BaseAmount.String() != "6.3" {
		t.Errorf("component bases: %s / %s", b.ServiceComponents[0].BaseAmount, b.TaxComponents[0].BaseAmount)
	}
}

func TestComponentDetails(t *testing.T) {
	b, err := Calculate(Input{Quantity: d("1"), UnitPrice: d("1000000"), PriceMode: Exclusive,
		ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{cityNoOnSvc, vatOnSvc}}) // sequence 2 listed before sequence 1
	if err != nil {
		t.Fatal(err)
	}
	if len(b.TaxComponents) != 2 || b.TaxComponents[0].Code != "VAT" || b.TaxComponents[1].Code != "CITY" {
		t.Fatalf("taxes are calculated and listed in sequence order: %+v", b.TaxComponents)
	}
	vat, city := b.TaxComponents[0], b.TaxComponents[1]
	if vat.Type != TypeTax || vat.OnService == nil || !*vat.OnService || !vat.BaseAmount.Equal(d("1100000")) || !vat.Amount.Equal(d("121000")) {
		t.Errorf("vat: %+v", vat)
	}
	if city.OnService == nil || *city.OnService || !city.BaseAmount.Equal(d("1000000")) || !city.Amount.Equal(d("10000")) {
		t.Errorf("city: %+v", city) // taxes are not compounded: the city tax base ignores VAT
	}
	if s := b.ServiceComponents[0]; s.Type != TypeServiceCharge || s.OnService != nil || !s.BaseAmount.Equal(d("1000000")) || !s.Amount.Equal(d("100000")) {
		t.Errorf("service: %+v", s)
	}
	if !b.TaxTotal.Equal(d("131000")) || !b.TotalAmount.Equal(d("1231000")) {
		t.Errorf("totals: tax %s total %s", b.TaxTotal, b.TotalAmount)
	}
}

func TestQuantityAndRoundingBoundaries(t *testing.T) {
	// Half away from zero on every line amount, at 2 decimals.
	b, err := Calculate(Input{Quantity: d("3"), UnitPrice: d("0.335"), PriceMode: Exclusive, Decimals: 2, Taxes: []TaxRule{{ID: 1, Code: "T", Rate: d("10"), Sequence: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	if !b.BaseAmount.Equal(d("1.01")) || !b.TaxTotal.Equal(d("0.10")) { // 1.005 → 1.01; 10% of 1.01 = 0.101 → 0.10
		t.Errorf("base %s tax %s", b.BaseAmount, b.TaxTotal)
	}
	b, _ = Calculate(Input{Quantity: d("1"), UnitPrice: d("0.05"), PriceMode: Exclusive, Decimals: 1})
	if !b.BaseAmount.Equal(d("0.1")) {
		t.Errorf("0.05 at one decimal rounds up to 0.1, got %s", b.BaseAmount)
	}
	b, _ = Calculate(Input{Quantity: d("-1"), UnitPrice: d("0.05"), PriceMode: Exclusive, Decimals: 1})
	if !b.BaseAmount.Equal(d("-0.1")) {
		t.Errorf("-0.05 rounds away from zero to -0.1, got %s", b.BaseAmount)
	}
	// Fractional quantities.
	b, _ = Calculate(Input{Quantity: d("1.5"), UnitPrice: d("100000"), PriceMode: Exclusive})
	if !b.BaseAmount.Equal(d("150000")) {
		t.Errorf("1.5 × 100000 = %s", b.BaseAmount)
	}
	// Three-decimal currency.
	b, _ = Calculate(Input{Quantity: d("1"), UnitPrice: d("10.000"), PriceMode: Inclusive, Decimals: 3, Taxes: []TaxRule{{ID: 1, Code: "T", Rate: d("5"), Sequence: 1}}})
	if !b.TotalAmount.Equal(d("10")) || !b.NetAmount.Add(b.TaxTotal).Equal(d("10")) {
		t.Errorf("3 decimals: %+v", b)
	}
}

func TestCreditsMirrorCharges(t *testing.T) {
	base := Input{Quantity: d("1"), UnitPrice: d("1000000"), Discount: d("100000"), PriceMode: Exclusive, ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatOnSvc}}
	credit := base
	credit.UnitPrice = d("-1000000")
	pos, _ := Calculate(base)
	neg, err := Calculate(credit)
	if err != nil {
		t.Fatal(err)
	}
	// The discount still reduces the amount towards zero: -1,000,000 - (-100,000) = -900,000.
	if !neg.NetAmount.Equal(d("-900000")) || !neg.Discount.Equal(d("-100000")) || !neg.TotalAmount.Equal(d("-1098900")) || !neg.TaxTotal.Equal(d("-108900")) {
		t.Errorf("credit: %+v", neg)
	}
	if !pos.TotalAmount.Equal(neg.TotalAmount.Neg()) {
		t.Errorf("credit total %s does not mirror %s", neg.TotalAmount, pos.TotalAmount)
	}
}

func TestValidation(t *testing.T) {
	ok := Input{Quantity: d("1"), UnitPrice: d("1000"), PriceMode: Exclusive}
	cases := []struct {
		name string
		edit func(*Input)
	}{
		{"zero quantity", func(i *Input) { i.Quantity = d("0") }},
		{"negative discount", func(i *Input) { i.Discount = d("-1") }},
		{"discount above the amount", func(i *Input) { i.Discount = d("1001") }},
		{"discount with more decimals than the currency", func(i *Input) { i.Discount = d("0.5") }},
		{"rate above 100", func(i *Input) { i.Taxes = []TaxRule{{ID: 1, Rate: d("100.0001")}} }},
		{"negative rate", func(i *Input) { i.ServiceCharges = []ServiceChargeRule{{ID: 1, Rate: d("-1")}} }},
		{"duplicate tax id", func(i *Input) { i.Taxes = []TaxRule{{ID: 1, Rate: d("1")}, {ID: 1, Rate: d("2"), Sequence: 2}} }},
		{"duplicate service id", func(i *Input) {
			i.ServiceCharges = []ServiceChargeRule{{ID: 3, Rate: d("1")}, {ID: 3, Rate: d("2"), Sequence: 2}}
		}},
		{"unknown price mode", func(i *Input) { i.PriceMode = "MIXED" }},
		{"decimals below 0", func(i *Input) { i.Decimals = -1 }},
		{"decimals above 3", func(i *Input) { i.Decimals = 4 }},
		{"exemptions", func(i *Input) { i.ExemptTaxIDs = []int64{1} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ok
			c.edit(&in)
			if _, err := Calculate(in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	// The same rule id in a service list and a tax list is fine: they are different rules.
	in := ok
	in.ServiceCharges = []ServiceChargeRule{{ID: 1, Rate: d("10")}}
	in.Taxes = []TaxRule{{ID: 1, Rate: d("11")}}
	if _, err := Calculate(in); err != nil {
		t.Fatalf("ids are unique per kind: %v", err)
	}
	// A 100% discount and 0% / 100% rates are the boundaries and are valid.
	in = Input{Quantity: d("1"), UnitPrice: d("1000"), Discount: d("1000"), PriceMode: Exclusive, Taxes: []TaxRule{{ID: 1, Rate: d("100")}, {ID: 2, Rate: d("0"), Sequence: 2}}}
	b, err := Calculate(in)
	if err != nil || !b.TotalAmount.IsZero() {
		t.Fatalf("boundaries: %v %+v", err, b)
	}
}

func TestVerify(t *testing.T) {
	in := Input{Quantity: d("1"), UnitPrice: d("1110000"), PriceMode: Inclusive, ServiceCharges: []ServiceChargeRule{svc10}, Taxes: []TaxRule{vatOnSvc}}
	good, _ := Calculate(in)
	if diffs, err := Verify(in, good, decimal.Zero); err != nil || len(diffs) != 0 {
		t.Fatalf("a correct claim: %v %v", err, diffs)
	}
	bad := good
	bad.TaxTotal = bad.TaxTotal.Add(d("1"))
	bad.TaxComponents = []Component{{RuleID: 1, Code: "VAT", Amount: good.TaxComponents[0].Amount.Add(d("1")), BaseAmount: good.TaxComponents[0].BaseAmount}}
	bad.ServiceComponents = nil
	diffs, err := Verify(in, bad, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	fields := map[string]bool{}
	for _, df := range diffs {
		fields[df.Field] = true
	}
	for _, f := range []string{"tax_total", "tax:VAT:amount", "service:SVC:missing"} {
		if !fields[f] {
			t.Errorf("expected a difference on %s, got %v", f, diffs)
		}
	}
	// A tolerance of 1 accepts the off-by-one tax claim (but not the missing component).
	diffs, _ = Verify(in, bad, d("1"))
	for _, df := range diffs {
		if df.Field == "tax_total" || df.Field == "tax:VAT:amount" {
			t.Errorf("within tolerance: %+v", df)
		}
	}
	if _, err := Verify(Input{}, good, decimal.Zero); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid input: %v", err)
	}
}
