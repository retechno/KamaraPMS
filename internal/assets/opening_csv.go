package assets

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// OpeningRow is one old asset of the opening balances: a line of the CSV, or the form "add an existing asset" (37-fixed-assets.md 4.4). Blank fields follow the category, and what
// the register can compute it computes (with a warning).
type OpeningRow struct {
	Name                          string           `json:"name"`
	Category                      string           `json:"category"`
	Location                      string           `json:"location"`
	Department                    string           `json:"department"`
	Supplier                      string           `json:"supplier"`
	AcquisitionDate               *civil.Date      `json:"acquisition_date"`
	InServiceDate                 *civil.Date      `json:"in_service_date"`
	Cost                          decimal.Decimal  `json:"cost"`
	ResidualValue                 *decimal.Decimal `json:"residual_value"`
	LifeMonths                    *int             `json:"life_months"`
	Method                        string           `json:"method"`
	AccumulatedDepreciation       *decimal.Decimal `json:"accumulated_depreciation"`
	MonthsDone                    *int             `json:"months_done"`
	UnitCount                     *int             `json:"unit_count"`
	SerialNo                      string           `json:"serial_no"`
	WarrantyUntil                 *civil.Date      `json:"warranty_until"`
	StartRule                     string           `json:"start_rule"`
	FiscalGroup                   string           `json:"fiscal_group"`
	FiscalMethod                  string           `json:"fiscal_method"`
	FiscalAccumulatedDepreciation *decimal.Decimal `json:"fiscal_accumulated_depreciation"`
	FiscalMonthsDone              *int             `json:"fiscal_months_done"`
}

var csvColumns = []string{"name", "category", "location", "department", "supplier", "acquisition_date", "in_service_date", "cost", "residual_value", "life_months", "method",
	"accumulated_depreciation", "months_done", "unit_count", "serial_no", "warranty_until", "start_rule", "fiscal_group", "fiscal_method", "fiscal_accumulated_depreciation", "fiscal_months_done"}

// OpeningCSVHeader is the first line of a file of opening balances (the columns, in the order of the template).
func OpeningCSVHeader() []string { return append([]string{}, csvColumns...) }

// parseOpeningCSV reads a file of opening balances: UTF-8, comma or semicolon, a header line with the names of the columns (any order; name, category, in_service_date and cost are needed).
// It answers the rows and, for a row that cannot be read, the problems found (the row number is the line of the file minus the header).
func parseOpeningCSV(text string) ([]OpeningRow, map[int][]apperr.FieldError, error) {
	text = strings.TrimPrefix(text, "\ufeff")
	if len(text) > maxCSVBytes {
		return nil, nil, apperr.Invalid("the file is too large", fieldErr("csv", "TOO_LARGE", "4 MB at most"))
	}
	first, _, _ := strings.Cut(text, "\n")
	r := csv.NewReader(strings.NewReader(text))
	r.Comma = ','
	if strings.Count(first, ";") > strings.Count(first, ",") {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, nil, apperr.Invalid("the file cannot be read", fieldErr("csv", "INVALID_FORMAT", err.Error()))
	}
	if len(recs) < 2 {
		return nil, nil, apperr.Invalid("the file has no rows", fieldErr("csv", "EMPTY", "a header line and at least one row"))
	}
	index := map[string]int{}
	for i, h := range recs[0] {
		name := strings.ToLower(trimmed(h))
		known := false
		for _, c := range csvColumns {
			if c == name {
				known = true
			}
		}
		if !known {
			return nil, nil, apperr.Invalid("the header is invalid", fieldErr("csv", "UNKNOWN_COLUMN", fmt.Sprintf("the column %q is not known", trimmed(h))))
		}
		index[name] = i
	}
	for _, need := range []string{"name", "category", "in_service_date", "cost"} {
		if _, ok := index[need]; !ok {
			return nil, nil, apperr.Invalid("the header is invalid", fieldErr("csv", "MISSING_COLUMN", "the column "+need+" is needed"))
		}
	}
	if len(recs)-1 > maxOpeningRows {
		return nil, nil, apperr.Invalid("the file has too many rows", fieldErr("csv", "TOO_MANY_ROWS", fmt.Sprintf("%d rows at most", maxOpeningRows)))
	}
	rows := make([]OpeningRow, 0, len(recs)-1)
	problems := map[int][]apperr.FieldError{}
	for n, rec := range recs[1:] {
		no := n + 1
		cell := func(col string) string {
			i, ok := index[col]
			if !ok || i >= len(rec) {
				return ""
			}
			return trimmed(rec[i])
		}
		bad := func(col, code, msg string) { problems[no] = append(problems[no], fieldErr(col, code, msg)) }
		row := OpeningRow{Name: cell("name"), Category: cell("category"), Location: cell("location"), Department: cell("department"), Supplier: cell("supplier"), Method: strings.ToUpper(cell("method")),
			SerialNo: cell("serial_no"), StartRule: strings.ToUpper(cell("start_rule")), FiscalGroup: strings.ToUpper(cell("fiscal_group")), FiscalMethod: strings.ToUpper(cell("fiscal_method"))}
		date := func(col string) *civil.Date {
			v := cell(col)
			if v == "" {
				return nil
			}
			d, err := civil.ParseDate(v)
			if err != nil {
				bad(col, "INVALID_DATE", "YYYY-MM-DD")
				return nil
			}
			return &d
		}
		money := func(col string) *decimal.Decimal {
			v := cell(col)
			if v == "" {
				return nil
			}
			d, err := decimal.NewFromString(v)
			if err != nil {
				bad(col, "INVALID_NUMBER", "a number with a dot as the decimal mark")
				return nil
			}
			return &d
		}
		count := func(col string) *int {
			v := cell(col)
			if v == "" {
				return nil
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				bad(col, "INVALID_INTEGER", "a whole number")
				return nil
			}
			return &n
		}
		row.AcquisitionDate, row.InServiceDate, row.WarrantyUntil = date("acquisition_date"), date("in_service_date"), date("warranty_until")
		if c := money("cost"); c != nil {
			row.Cost = *c
		} else if cell("cost") == "" {
			bad("cost", "REQUIRED", "needed")
		}
		row.ResidualValue, row.AccumulatedDepreciation, row.FiscalAccumulatedDepreciation = money("residual_value"), money("accumulated_depreciation"), money("fiscal_accumulated_depreciation")
		row.LifeMonths, row.MonthsDone, row.UnitCount, row.FiscalMonthsDone = count("life_months"), count("months_done"), count("unit_count"), count("fiscal_months_done")
		rows = append(rows, row)
	}
	return rows, problems, nil
}
