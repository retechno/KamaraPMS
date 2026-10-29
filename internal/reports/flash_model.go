package reports

import (
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// The kinds of a line of the daily flash report: how its figures are written.
const (
	FlashMoney   = "MONEY"   // an amount at the decimals of the property
	FlashCount   = "COUNT"   // a whole number
	FlashPercent = "PERCENT" // a percentage with two decimals
	FlashRatio   = "RATIO"   // a quotient with two decimals
)

// The five actual columns, in the order of the arrays of the builder.
const (
	colToday = iota
	colMTD
	colLastMonth
	colLastYear
	colYTD
	nCols
)

// FlashLine is one row of the daily flash report, with the same cells in each column (docs/architecture/38-reports-night-audit-pack.md, 7.1). A cell with nothing to say is the empty string;
// a row without a budget has a null Budget, Variance and VariancePercent. Key is stable (the web names the known keys in the language of the user); Label is the English text of the row and
// the only name of a row that is data (a charge code, an outlet, a room type, a method).
type FlashLine struct {
	Key         string  `json:"key"`
	Label       string  `json:"label"`
	Kind        string  `json:"kind"`
	Today       string  `json:"today"`
	MTD         string  `json:"mtd"`
	LastMonth   string  `json:"last_month"`
	LastYear    string  `json:"last_year"`
	Budget      *string `json:"budget"`
	Variance    *string `json:"variance"`
	VariancePct *string `json:"variance_percent"`
	// Favourable says whether the variance is good news (a higher figure is better); null when the row has no budget.
	Favourable *bool  `json:"favourable"`
	YTD        string `json:"ytd"`
}

// FlashBlock is a block of rows of one group of the net revenue: the rooms, a POS outlet, the banquet, the hotel folios, or the items with no revenue account.
type FlashBlock struct {
	Kind  string      `json:"kind"` // ROOMS, HOTEL, OUTLET, BANQUET or UNMAPPED
	Title string      `json:"title"`
	Rows  []FlashLine `json:"rows"`
	Total FlashLine   `json:"total"`
}

// FlashRevenueGroup is group A (rooms), B (food and beverage) or C (other) of the net revenue.
type FlashRevenueGroup struct {
	Code   string       `json:"code"` // A, B or C
	Title  string       `json:"title"`
	Blocks []FlashBlock `json:"blocks"`
	Total  FlashLine    `json:"total"`
}

// FlashRevenueSection is the net revenue of the report: A + B + C is the revenue of the income statement.
type FlashRevenueSection struct {
	Groups   []FlashRevenueGroup `json:"groups"`
	TotalNet FlashLine           `json:"total_net"`
	Service  FlashLine           `json:"service"`
	Tax      FlashLine           `json:"tax"`
	// GTR is the gross total revenue: the net revenue with the service charge and the tax.
	GTR FlashLine `json:"gtr"`
	// Journal is the revenue posted by a journal and not from a folio, so that A + B + C + this row is the income statement; null when there is no accounting or for the open day.
	Journal  *FlashLine  `json:"journal"`
	BySource []FlashLine `json:"by_source"`
}

// FlashPaymentsSection is the money taken (GTP) in two cuts that add up to the same figure, the refunds below it, and what was moved to the city ledger.
type FlashPaymentsSection struct {
	ByCashier    []FlashLine `json:"by_cashier"`
	ByMethod     []FlashLine `json:"by_method"`
	GTP          FlashLine   `json:"gtp"`
	Refunds      []FlashLine `json:"refunds"`
	RefundsTotal FlashLine   `json:"refunds_total"`
	CityLedger   FlashLine   `json:"to_city_ledger"`
}

// FlashControlColumn is the control of one column: GTR - GTP + refunds - the city ledger = the change of the guest ledger.
type FlashControlColumn struct {
	GTR          string `json:"gtr"`
	GTP          string `json:"gtp"`
	Refunds      string `json:"refunds"`
	CityLedger   string `json:"to_city_ledger"`
	Expected     string `json:"expected_change"`
	ActualChange string `json:"actual_change"`
	Difference   string `json:"difference"`
}

// FlashControl is the control balance for the day and for the month to date. It is shown and never blocks.
type FlashControl struct {
	Today    FlashControlColumn `json:"today"`
	MTD      FlashControlColumn `json:"mtd"`
	Balanced bool               `json:"balanced"`
}

// FlashSignatures are the two lines at the foot of the PDF: prepared by the night audit, approved by accounting (a line on paper; there is no record of an approval).
type FlashSignatures struct {
	PreparedBy string  `json:"prepared_by"`
	PreparedAt *string `json:"prepared_at"`
}

// FlashWarning is a finding of the report that is not an error: the code names it, the params fill its text.
type FlashWarning struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params,omitempty"`
}

// DailyFlash is the daily flash report (DFR) of a business date.
type DailyFlash struct {
	BusinessDate civil.Date `json:"business_date"`
	Status       string     `json:"status"`
	// Interim is the flash of the open day: no control balance, no signatures.
	Interim         bool        `json:"interim"`
	Decimals        int32       `json:"decimals"`
	Ranges          FlashRanges `json:"ranges"`
	FiscalYearStart civil.Date  `json:"fiscal_year_start"`
	// HasBudget says whether an active budget covers the month; without it the budget cells are empty.
	HasBudget  bool                 `json:"has_budget"`
	Rooms      []FlashLine          `json:"rooms"`
	SoldByType []FlashLine          `json:"sold_by_type"`
	Activity   []FlashLine          `json:"activity"`
	Covers     []FlashLine          `json:"covers"`
	Revenue    FlashRevenueSection  `json:"revenue"`
	Payments   FlashPaymentsSection `json:"payments"`
	Control    *FlashControl        `json:"control"`
	Signatures FlashSignatures      `json:"signatures"`
	Warnings   []FlashWarning       `json:"warnings"`
}

// vals are the figures of one row in the five columns; a column in blank has no figure.
type vals struct {
	v     [nCols]decimal.Decimal
	blank [nCols]bool
}

func (a vals) add(b vals) vals {
	for i := range a.v {
		a.v[i] = a.v[i].Add(b.v[i])
	}
	return a
}

// CSV flattens the report into one table: the section, the key and the cells of the row.
func (d DailyFlash) CSV() ([]string, [][]string) {
	h := []string{"section", "key", "label", "today", "mtd", "mtd_last_month", "mtd_last_year", "budget_mtd", "variance_mtd", "variance_percent", "ytd"}
	var rows [][]string
	opt := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	add := func(section string, ls ...FlashLine) {
		for _, l := range ls {
			rows = append(rows, []string{section, l.Key, l.Label, l.Today, l.MTD, l.LastMonth, l.LastYear, opt(l.Budget), opt(l.Variance), opt(l.VariancePct), l.YTD})
		}
	}
	add("rooms", d.Rooms...)
	add("sold_by_type", d.SoldByType...)
	add("activity", d.Activity...)
	add("covers", d.Covers...)
	for _, g := range d.Revenue.Groups {
		for _, b := range g.Blocks {
			add("revenue_"+g.Code, b.Rows...)
			if b.Title != "" {
				add("revenue_"+g.Code, b.Total)
			}
		}
		add("revenue_"+g.Code, g.Total)
	}
	add("revenue", d.Revenue.TotalNet, d.Revenue.Service, d.Revenue.Tax, d.Revenue.GTR)
	if d.Revenue.Journal != nil {
		add("revenue", *d.Revenue.Journal)
	}
	add("revenue_by_source", d.Revenue.BySource...)
	add("payments_by_cashier", d.Payments.ByCashier...)
	add("payments_by_method", d.Payments.ByMethod...)
	add("payments", d.Payments.GTP)
	add("refunds", d.Payments.Refunds...)
	add("refunds", d.Payments.RefundsTotal)
	add("payments", d.Payments.CityLedger)
	if c := d.Control; c != nil {
		for _, r := range []struct{ key, label string }{{"gtr", "GTR"}, {"gtp", "GTP"}, {"refunds", "Refunds"}, {"to_city_ledger", "Transferred to the city ledger"}, {"expected_change", "Expected change of the guest ledger"}, {"actual_change", "Change of the guest ledger"}, {"difference", "Difference"}} {
			pick := func(cc FlashControlColumn) string {
				switch r.key {
				case "gtr":
					return cc.GTR
				case "gtp":
					return cc.GTP
				case "refunds":
					return cc.Refunds
				case "to_city_ledger":
					return cc.CityLedger
				case "expected_change":
					return cc.Expected
				case "actual_change":
					return cc.ActualChange
				}
				return cc.Difference
			}
			rows = append(rows, []string{"control", r.key, r.label, pick(c.Today), pick(c.MTD), "", "", "", "", "", ""})
		}
	}
	return h, rows
}
