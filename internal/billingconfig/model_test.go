package billingconfig

import "testing"

func TestParseRate(t *testing.T) {
	for _, ok := range []string{"0", "11", "11.5", "11.0000", "100", "100.0000", "0.0001"} {
		if _, err := ParseRate(ok); err != nil {
			t.Errorf("%q should be accepted: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-1", "100.0001", "101", "11.12345", "1e2", "+5", " 5", "5%", "1,5", ".5", "1000"} {
		if _, err := ParseRate(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	d, _ := ParseRate("11")
	if FormatRate(d) != "11.0000" {
		t.Errorf("wire form %s", FormatRate(d))
	}
}

func TestParseUnitPriceFollowsCurrencyPrecision(t *testing.T) {
	if _, err := ParseUnitPrice("150000", 0); err != nil {
		t.Error(err)
	}
	if _, err := ParseUnitPrice("150000.00", 0); err != nil {
		t.Errorf("trailing zeros are not extra precision: %v", err)
	}
	if _, err := ParseUnitPrice("150000.50", 0); err == nil {
		t.Error("IDR has no decimals")
	}
	if _, err := ParseUnitPrice("7.05", 2); err != nil {
		t.Error(err)
	}
	if _, err := ParseUnitPrice("7.055", 3); err == nil {
		t.Error("the column holds two decimals at most")
	}
	for _, bad := range []string{"-1", "", "1e3", "1,000"} {
		if _, err := ParseUnitPrice(bad, 2); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

func TestRulesInputValidation(t *testing.T) {
	in := RulesInput{
		Taxes:          []TaxRuleInput{{TaxID: 1, Sequence: 1}, {TaxID: 1, Sequence: 1}, {TaxID: 0, Sequence: 0}},
		ServiceCharges: []ServiceRuleInput{{ServiceChargeID: 5, Sequence: 40000}},
	}
	got := map[string]string{}
	for _, f := range in.Validate() {
		got[f.Field] = f.Code
	}
	want := map[string]string{
		"taxes[1].tax_id": "DUPLICATE", "taxes[1].sequence": "DUPLICATE",
		"taxes[2].tax_id": "REQUIRED", "taxes[2].sequence": "OUT_OF_RANGE", "service_charges[0].sequence": "OUT_OF_RANGE",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if fe := (RulesInput{}).Validate(); len(fe) != 0 {
		t.Errorf("an empty rule set is valid (it removes all rules): %v", fe)
	}
}
