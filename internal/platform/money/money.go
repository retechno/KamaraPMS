// Package money holds the decimal helpers used for every monetary amount.
//
// Money is never a float. Amounts travel as decimal strings in JSON and as
// NUMERIC in PostgreSQL. Rounding is always half away from zero at the property's
// currency precision (properties.currency_decimals).
package money

import (
	"fmt"
	"regexp"

	"github.com/shopspring/decimal"
)

// MaxDecimals is the highest supported currency precision.
const MaxDecimals = 3

// plainAmount accepts "1221000", "-100000.50", "0.125". It rejects exponents,
// separators, "+" signs and whitespace, which are ambiguous in money input.
var plainAmount = regexp.MustCompile(`^-?[0-9]{1,16}(\.[0-9]{1,4})?$`)

// Parse converts a client-supplied amount string into a decimal.
func Parse(s string) (decimal.Decimal, error) {
	if !plainAmount.MatchString(s) {
		return decimal.Decimal{}, fmt.Errorf("money: %q is not a plain decimal amount", s)
	}
	return decimal.NewFromString(s)
}

// Round rounds d to the given number of decimals, half away from zero
// (2.5 -> 3, -2.5 -> -3). This is the only rounding rule used for money.
func Round(d decimal.Decimal, decimals int32) decimal.Decimal {
	return d.Round(decimals)
}

// Percent is rate percent of amount, rounded to the currency (a fee of 2.5 percent on 100000 is 2500).
func Percent(amount, rate decimal.Decimal, decimals int32) decimal.Decimal {
	return Round(amount.Mul(rate).Div(decimal.NewFromInt(100)), decimals)
}

// ValidateDecimals checks a currency precision.
func ValidateDecimals(decimals int32) error {
	if decimals < 0 || decimals > MaxDecimals {
		return fmt.Errorf("money: currency decimals must be between 0 and %d, got %d", MaxDecimals, decimals)
	}
	return nil
}
