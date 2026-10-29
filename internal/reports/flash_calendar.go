package reports

import (
	"time"

	"kamarapms/internal/platform/civil"
)

// FlashRange is a range of business dates, both ends included.
type FlashRange struct {
	From civil.Date `json:"from"`
	To   civil.Date `json:"to"`
}

// FlashRanges are the ranges of the five actual columns of the daily flash report for the business date D (docs/architecture/38-reports-night-audit-pack.md, 7.1).
type FlashRanges struct {
	Today     FlashRange `json:"today"`
	MTD       FlashRange `json:"mtd"`
	LastMonth FlashRange `json:"last_month"`
	LastYear  FlashRange `json:"last_year"`
	YTD       FlashRange `json:"ytd"`
}

func daysIn(year int, month time.Month) int { return civil.NewDate(year, month+1, 1).AddDays(-1).Day() }

// flashRanges computes the ranges. MTD of last month is days 1 to n of the month before, where n is the day of D cut at the length of that month (31 October compares with 1 to 30
// September); MTD of last year is the same calendar date a year before (29 February compares with 28 February); YTD starts on the first day of the fiscal year that holds D
// (the fiscal year starts on the first of fiscalStartMonth, 1 to 12).
func flashRanges(d civil.Date, fiscalStartMonth int) FlashRanges {
	if fiscalStartMonth < 1 || fiscalStartMonth > 12 {
		fiscalStartMonth = 1
	}
	first := civil.NewDate(d.Year(), d.Month(), 1)
	lmYear, lmMonth := d.Year(), d.Month()-1
	if lmMonth < time.January {
		lmYear, lmMonth = lmYear-1, time.December
	}
	lmDay := d.Day()
	if n := daysIn(lmYear, lmMonth); lmDay > n {
		lmDay = n
	}
	lyDay := d.Day()
	if n := daysIn(d.Year()-1, d.Month()); lyDay > n {
		lyDay = n
	}
	fy := d.Year()
	if int(d.Month()) < fiscalStartMonth {
		fy--
	}
	return FlashRanges{
		Today:     FlashRange{d, d},
		MTD:       FlashRange{first, d},
		LastMonth: FlashRange{civil.NewDate(lmYear, lmMonth, 1), civil.NewDate(lmYear, lmMonth, lmDay)},
		LastYear:  FlashRange{civil.NewDate(d.Year()-1, d.Month(), 1), civil.NewDate(d.Year()-1, d.Month(), lyDay)},
		YTD:       FlashRange{civil.NewDate(fy, time.Month(fiscalStartMonth), 1), d},
	}
}

// fiscalMonthOf is the number (1 to 12) of the month of d in the fiscal year that starts in startMonth, and the first day of that year.
func fiscalMonthOf(d civil.Date, startMonth int) (n int, yearStart civil.Date) {
	if startMonth < 1 || startMonth > 12 {
		startMonth = 1
	}
	y := d.Year()
	if int(d.Month()) < startMonth {
		y--
	}
	yearStart = civil.NewDate(y, time.Month(startMonth), 1)
	return (d.Year()-y)*12 + int(d.Month()) - startMonth + 1, yearStart
}
