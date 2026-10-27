package assets

import (
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// FiscalRate is the rate of a fiscal group: the useful life in years and the percent of a year for the straight line and for the declining balance (UU PPh article 11, 4.9 of the design).
type FiscalRate struct {
	LifeYears int
	SLRate    decimal.Decimal // percent a year
	DBRate    decimal.Decimal // percent a year; zero when the group has no declining balance (the buildings)
}

var (
	decHundred = decimal.NewFromInt(100)
	decTwelve  = decimal.NewFromInt(12)
)

// taxYearStart is the first day of the tax year that holds the month m, for a tax year that starts in startMonth (1 = January).
func taxYearStart(m civil.Date, startMonth int) civil.Date {
	y := m.Year()
	if int(m.Month()) < startMonth {
		y--
	}
	return civil.NewDate(y, time.Month(startMonth), 1)
}

// FiscalAccumulated is the fiscal depreciation of an asset from the month of its acquisition through the month of upTo (4.9). It is a pure function of the cost, the group's rate and the method:
//
//   - the basis is the cost without a residual value, from the month of acquisition; the first tax year is prorated by the months left in it;
//   - straight line: cost x rate a year; declining balance: rate x the fiscal book value at the start of the year (the first year prorated);
//   - in the last year of the useful life the whole book value that is left is depreciated;
//   - up to a month inside a tax year the amount of that year is spread evenly over its months.
//
// It answers the accumulated amount and the months counted (never more than the useful life). Amounts are rounded at decimals.
func FiscalAccumulated(cost decimal.Decimal, rate FiscalRate, method string, acquisition, upTo civil.Date, taxYearStartMonth int, decimals int32) (decimal.Decimal, int) {
	if taxYearStartMonth < 1 || taxYearStartMonth > 12 {
		taxYearStartMonth = 1
	}
	cur, end := MonthStart(acquisition), MonthStart(upTo)
	life := rate.LifeYears * 12
	if !cost.IsPositive() || life <= 0 || end.Before(cur) {
		return decimal.Zero, 0
	}
	annualRate := rate.SLRate
	if method == MethodDB && rate.DBRate.IsPositive() {
		annualRate = rate.DBRate
	}
	acc, book := decimal.Zero, cost
	used, counted := 0, 0
	for used < life && book.IsPositive() {
		yearEnd := AddMonths(taxYearStart(cur, taxYearStartMonth), 11)
		avail := MonthsBetween(cur, yearEnd)
		if avail > life-used {
			avail = life - used
		}
		var full decimal.Decimal
		switch {
		case used+avail >= life: // the last year of the useful life takes what is left
			full = book
		case method == MethodDB && rate.DBRate.IsPositive():
			full = book.Mul(annualRate).Div(decHundred).Mul(decimal.NewFromInt(int64(avail))).Div(decTwelve).Round(decimals)
		default:
			full = cost.Mul(annualRate).Div(decHundred).Mul(decimal.NewFromInt(int64(avail))).Div(decTwelve).Round(decimals)
		}
		if full.GreaterThan(book) {
			full = book
		}
		if !end.After(AddMonths(cur, avail-1)) { // the month asked for is inside this stretch: take its share
			n := MonthsBetween(cur, end)
			part := full.Mul(decimal.NewFromInt(int64(n))).Div(decimal.NewFromInt(int64(avail))).Round(decimals)
			return acc.Add(part), counted + n
		}
		acc = acc.Add(full)
		book = book.Sub(full)
		used += avail
		counted += avail
		cur = AddMonths(cur, avail)
	}
	return acc, counted
}

// SLAccumulated is the straight-line depreciation of the months done, as the register would have booked it: (cost - residual) x done / life, rounded at decimals.
func SLAccumulated(cost, residual decimal.Decimal, life, done int, decimals int32) decimal.Decimal {
	if life <= 0 || done <= 0 {
		return decimal.Zero
	}
	if done >= life {
		return cost.Sub(residual)
	}
	return cost.Sub(residual).Mul(decimal.NewFromInt(int64(done))).Div(decimal.NewFromInt(int64(life))).Round(decimals)
}
