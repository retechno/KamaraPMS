// Package budget holds the budget of a property: the plan of the revenue and the expenses of a fiscal year, a figure per account and per month, in
// versions, and the report that sets it against what the books show (design: docs/architecture/11-cashier-budget-cashflow-card.md, part B).
package budget

import (
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
)

// Statuses of a budget: a DRAFT is edited, an ACTIVE one is fixed, an ARCHIVED one was replaced by a newer active version.
const (
	StatusDraft    = "DRAFT"
	StatusActive   = "ACTIVE"
	StatusArchived = "ARCHIVED"
)

// How a yearly figure is spread over the months.
const (
	SpreadEqual    = "EQUAL"
	SpreadLastYear = "LAST_YEAR"
)

const (
	monthsInYear  = 12
	maxNameLen    = 100
	maxTextLen    = 500
	maxGridRows   = 1000
	maxImportRows = 1000
	yearsAhead    = 2 // a budget may be made for the fiscal years up to this many after the current one
)

// maxAmount is the largest figure of a month (the column holds 15 digits before the decimals; a year of twelve of them still fits).
var maxAmount = decimal.New(9999999999999, 0)

// Budget is a version of the budget of a fiscal year. The list holds the summary, the detail adds the grid.
type Budget struct {
	ID           int64      `json:"id"`
	YearStart    civil.Date `json:"year_start"`
	YearEnd      civil.Date `json:"year_end"`
	YearLabel    string     `json:"year_label"`
	Version      int        `json:"version"`
	Name         string     `json:"name"`
	Description  string     `json:"description,omitempty"`
	Status       string     `json:"status"`
	CopiedFromID *int64     `json:"copied_from_id"`
	ActivatedAt  *time.Time `json:"activated_at"`
	ActivatedBy  *int64     `json:"activated_by"`
	ApprovedBy   *int64     `json:"approved_by"`
	ArchivedAt   *time.Time `json:"archived_at"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	AccountCount int        `json:"account_count"`
	Revenue      string     `json:"total_revenue"`
	Expense      string     `json:"total_expense"`
	Result       string     `json:"total_result"`

	Months    []Month      `json:"months,omitempty"`
	Rows      []Row        `json:"rows,omitempty"`
	Available []AccountRef `json:"available_accounts,omitempty"`
}

// Month is a month of the fiscal year, from 1 to 12.
type Month struct {
	Number int        `json:"number"`
	Start  civil.Date `json:"start"`
}

// AccountRef is an account a budget may cover.
type AccountRef struct {
	ID          int64  `json:"id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	AccountType string `json:"account_type"`
	Group       string `json:"statement_group"`
	Active      bool   `json:"is_active"`
}

// Row is the figures of one account: twelve amounts, on the normal side of the account (revenue and expense both show positive).
type Row struct {
	AccountID   int64    `json:"account_id"`
	Code        string   `json:"code"`
	Name        string   `json:"name"`
	AccountType string   `json:"account_type"`
	Group       string   `json:"statement_group"`
	Amounts     []string `json:"amounts"`
	Total       string   `json:"total"`
}

// ListFilter narrows the list of budgets.
type ListFilter struct {
	YearStart *civil.Date
	Status    string
}

// CreateInput starts a draft: empty, or a copy of another version (the revision of a budget), which keeps its year.
type CreateInput struct {
	YearStart   *civil.Date `json:"year_start"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	CopyFromID  *int64      `json:"copy_from_id"`
}

// UpdateInput renames a draft.
type UpdateInput struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// GridInput is the whole grid of a draft: a row per account. It replaces what the draft held.
type GridInput struct {
	Rows []RowInput `json:"rows"`
}

// RowInput is the twelve amounts of an account; an empty amount is zero.
type RowInput struct {
	AccountID int64    `json:"account_id"`
	Amounts   []string `json:"amounts"`
}

// SpreadInput spreads a yearly figure of an account over the months, equally or after the pattern of the actuals of the year before.
type SpreadInput struct {
	AccountID int64  `json:"account_id"`
	Total     string `json:"total"`
	Method    string `json:"method"`
}

// FillInput fills a draft from the actuals of a fiscal year (the year before the budget's by default), raised or lowered by a percent.
// Replace empties the grid first; otherwise the accounts that have a row already keep it.
type FillInput struct {
	SourceYearStart *civil.Date `json:"source_year_start"`
	PercentChange   string      `json:"percent_change"`
	Replace         bool        `json:"replace"`
}

// ActivateInput makes a draft the active version of its year. Someone who holds budget.approve approves it.
type ActivateInput struct {
	Approval *iam.ApprovalInput `json:"approval"`
}

// ImportResult is what a CSV import did (or, as a dry run, would do).
type ImportResult struct {
	DryRun   bool `json:"dry_run"`
	Accounts int  `json:"accounts"`
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

// fiscalYearOf is the fiscal year (first and last day) that contains a date when years start in startMonth.
func fiscalYearOf(d civil.Date, startMonth int) (start, end civil.Date) {
	y := d.Year()
	if int(d.Month()) < startMonth {
		y--
	}
	start = civil.NewDate(y, time.Month(startMonth), 1)
	end = civil.NewDate(y, time.Month(startMonth)+12, 1).AddDays(-1)
	return start, end
}

func yearLabel(end civil.Date) string { return "FY" + strconv.Itoa(end.Year()) }

// monthStart is the first day of month n (1 to 12) of the fiscal year that starts on yearStart.
func monthStart(yearStart civil.Date, n int) civil.Date {
	return civil.NewDate(yearStart.Year(), yearStart.Month()+time.Month(n-1), 1)
}

// monthEnd is the last day of month n of the fiscal year.
func monthEnd(yearStart civil.Date, n int) civil.Date { return monthStart(yearStart, n+1).AddDays(-1) }

// monthNumber is the number (1 to 12) of the month of d in the fiscal year that starts on yearStart.
func monthNumber(yearStart, d civil.Date) int {
	return (d.Year()-yearStart.Year())*monthsInYear + int(d.Month()) - int(yearStart.Month()) + 1
}

func months(yearStart civil.Date) []Month {
	out := make([]Month, monthsInYear)
	for i := range out {
		out[i] = Month{Number: i + 1, Start: monthStart(yearStart, i+1)}
	}
	return out
}

// natural is the amount of an account on its normal side from a balance (debit minus credit): revenue is a credit, so its balance is turned round.
func natural(accountType string, balance decimal.Decimal) decimal.Decimal {
	if accountType == accounting.TypeRevenue {
		return balance.Neg()
	}
	return balance
}

// parseAmount reads a figure of a month at the decimals of the currency; empty is zero. Budgets may hold a negative figure (a refund, a credit).
func parseAmount(field, s string, decimals int32) (decimal.Decimal, *apperr.FieldError) {
	s = strings.TrimSpace(s)
	if s == "" {
		return decimal.Zero, nil
	}
	d, err := money.Parse(s)
	if err != nil || d.Abs().GreaterThan(maxAmount) {
		fe := fieldErr(field, "INVALID_AMOUNT", "an amount without separators, up to 13 digits")
		return decimal.Zero, &fe
	}
	if !d.Equal(d.Round(decimals)) {
		fe := fieldErr(field, "INVALID_AMOUNT", "at most the currency's decimals")
		return decimal.Zero, &fe
	}
	return d, nil
}

func validText(name, description string) []apperr.FieldError {
	var fields []apperr.FieldError
	if n := len([]rune(strings.TrimSpace(name))); n < 1 || n > maxNameLen {
		fields = append(fields, fieldErr("name", "INVALID_VALUE", "1 to 100 characters"))
	}
	if len([]rune(strings.TrimSpace(description))) > maxTextLen {
		fields = append(fields, fieldErr("description", "INVALID_VALUE", "at most 500 characters"))
	}
	return fields
}

// cell is a figure of the grid on its way to the database.
type cell struct {
	AccountID int64  `json:"account_id"`
	Month     int    `json:"month"`
	Amount    string `json:"amount"`
}

// spreadEqual splits a total into twelve amounts at the decimals of the currency; the months before the last get the rounded twelfth and the last one
// what is left, so they add up to the total exactly.
func spreadEqual(total decimal.Decimal, decimals int32) []decimal.Decimal {
	out := make([]decimal.Decimal, monthsInYear)
	share := total.Div(decimal.NewFromInt(monthsInYear)).Round(decimals)
	sum := decimal.Zero
	for i := 0; i < monthsInYear-1; i++ {
		out[i] = share
		sum = sum.Add(share)
	}
	out[monthsInYear-1] = total.Sub(sum)
	return out
}

// spreadPattern splits a total after the shape of a pattern (the actuals of twelve months). The rounding difference goes to the biggest month.
// It answers false when the pattern is all zero.
func spreadPattern(total decimal.Decimal, pattern []decimal.Decimal, decimals int32) ([]decimal.Decimal, bool) {
	sum := decimal.Zero
	for _, p := range pattern {
		sum = sum.Add(p)
	}
	if sum.IsZero() {
		return nil, false
	}
	out := make([]decimal.Decimal, monthsInYear)
	got, big := decimal.Zero, 0
	for i, p := range pattern {
		out[i] = total.Mul(p).Div(sum).Round(decimals)
		got = got.Add(out[i])
		if p.Abs().GreaterThan(pattern[big].Abs()) {
			big = i
		}
	}
	out[big] = out[big].Add(total.Sub(got))
	return out, true
}
