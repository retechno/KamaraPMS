package money

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
)

func TestRoundHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		in       string
		decimals int32
		want     string
	}{
		{"2.5", 0, "3"},
		{"-2.5", 0, "-3"},
		{"3.5", 0, "4"},
		{"2.4", 0, "2"},
		{"0.125", 2, "0.13"},
		{"-0.125", 2, "-0.13"},
		{"909090.909", 0, "909091"},
		{"5.73300", 2, "5.73"},
		{"0.573", 2, "0.57"},
		{"0.693", 2, "0.69"},
		{"1221000", 0, "1221000"},
	}
	for _, c := range cases {
		got := Round(decimal.RequireFromString(c.in), c.decimals)
		if !got.Equal(decimal.RequireFromString(c.want)) {
			t.Errorf("Round(%s, %d) = %s, want %s", c.in, c.decimals, got, c.want)
		}
	}
}

func TestRoundIsSymmetric(t *testing.T) {
	for _, s := range []string{"0.005", "1.115", "99.995", "12345.675", "0.5"} {
		d := decimal.RequireFromString(s)
		for decimals := int32(0); decimals <= MaxDecimals; decimals++ {
			if !Round(d.Neg(), decimals).Equal(Round(d, decimals).Neg()) {
				t.Errorf("Round(-%s, %d) is not the negation of Round(%s, %d)", s, decimals, s, decimals)
			}
		}
	}
}

func TestParse(t *testing.T) {
	for _, ok := range []string{"0", "1221000", "-100000.50", "0.125", "1110000.00"} {
		if _, err := Parse(ok); err != nil {
			t.Errorf("Parse(%q) failed: %v", ok, err)
		}
	}
	for _, bad := range []string{"", " 1", "1e6", "+5", "1,000", "1.23456", "NaN", "12345678901234567", ".5", "5."} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestValidateDecimals(t *testing.T) {
	for _, d := range []int32{0, 2, 3} {
		if err := ValidateDecimals(d); err != nil {
			t.Errorf("%d: %v", d, err)
		}
	}
	for _, d := range []int32{-1, 4} {
		if ValidateDecimals(d) == nil {
			t.Errorf("%d should be rejected", d)
		}
	}
}

// Money must be serialized as a JSON string so no client parses it as a float.
func TestDecimalMarshalsAsJSONString(t *testing.T) {
	b, err := json.Marshal(struct {
		Amount decimal.Decimal `json:"amount"`
	}{decimal.RequireFromString("1221000.50")})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"amount":"1221000.5"}` {
		t.Fatalf("got %s", b)
	}
}
