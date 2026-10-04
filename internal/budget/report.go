package budget

import (
	"context"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/budget/budgetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// VsActualQuery picks what the report covers: a fiscal year (the current one by default), the months from From to To of it (the first day of the year to the
// end of the current month by default) and the version (the active one by default).
type VsActualQuery struct {
	YearStart *civil.Date
	From      *civil.Date
	To        *civil.Date
	BudgetID  *int64
}

// Cell sets the actual against the budget: the variance is the actual less the budget; it is favourable when revenue (or a profit) is above the budget,
// or an expense is below it.
type Cell struct {
	Actual          decimal.Decimal  `json:"actual"`
	Budget          decimal.Decimal  `json:"budget"`
	Variance        decimal.Decimal  `json:"variance"`
	VariancePercent *decimal.Decimal `json:"variance_percent"`
	Favourable      *bool            `json:"favourable"`
}

// Account is an account of a line of the report.
type Account struct {
	AccountID int64  `json:"account_id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Period    Cell   `json:"period"`
	YTD       Cell   `json:"ytd"`
}

// Line is a group of accounts (kind GROUP) or a subtotal (SUBTOTAL, TOTAL), laid out like the income statement, with the cells of the period and of the
// year to date.
type Line struct {
	Key      string    `json:"key"`
	Title    string    `json:"title"`
	Kind     string    `json:"kind"`
	Period   Cell      `json:"period"`
	YTD      Cell      `json:"ytd"`
	Accounts []Account `json:"accounts"`
}

// BudgetRef names the version the report was made with.
type BudgetRef struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Status  string `json:"status"`
}

// VsActual is the budget against actual report of a range of months of a fiscal year. The period columns cover From to To, the year to date columns the
// first day of the fiscal year to To.
type VsActual struct {
	YearStart civil.Date `json:"year_start"`
	YearEnd   civil.Date `json:"year_end"`
	YearLabel string     `json:"year_label"`
	From      civil.Date `json:"from"`
	To        civil.Date `json:"to"`
	Budget    BudgetRef  `json:"budget"`
	Lines     []Line     `json:"lines"`
}

func newCell(actual, budget decimal.Decimal, higherIsBetter bool) Cell {
	c := Cell{Actual: actual, Budget: budget, Variance: actual.Sub(budget)}
	if !budget.IsZero() {
		p := c.Variance.Div(budget.Abs()).Mul(decimal.NewFromInt(100)).Round(1)
		c.VariancePercent = &p
	}
	if !c.Variance.IsZero() {
		fav := c.Variance.IsPositive() == higherIsBetter
		c.Favourable = &fav
	}
	return c
}

// higherIsBetter says whether a line of the income statement is better when it is bigger: revenue and every subtotal are; an expense group is not.
func higherIsBetter(l accounting.StatementLine) bool {
	return l.Kind != "GROUP" || strings.HasPrefix(l.Key, "REV_")
}

// resolve fills the defaults of the query and snaps the range to whole months of one fiscal year.
func (q VsActualQuery) resolve(cal calendar, today civil.Date) (yearStart, yearEnd civil.Date, from, to int, err error) {
	if q.YearStart != nil {
		yearStart = *q.YearStart
		if yearStart.Day() != 1 || int(yearStart.Month()) != cal.startMonth {
			return yearStart, yearEnd, 0, 0, apperr.Invalid("the report parameters are invalid", fieldErr("year_start", "INVALID_FISCAL_YEAR", "the first day of a fiscal year"))
		}
		_, yearEnd = fiscalYearOf(yearStart, cal.startMonth)
	} else {
		yearStart, yearEnd = fiscalYearOf(today, cal.startMonth)
	}
	from, to = 1, monthsInYear
	switch {
	case !today.Before(yearStart) && !today.After(yearEnd):
		to = monthNumber(yearStart, today)
	case today.Before(yearStart):
		to = 1
	}
	var errs []apperr.FieldError
	pick := func(name string, d *civil.Date, dst *int) {
		if d == nil {
			return
		}
		n := monthNumber(yearStart, *d)
		if d.Before(yearStart) || d.After(yearEnd) || n < 1 || n > monthsInYear {
			errs = append(errs, fieldErr(name, "OUT_OF_YEAR", "a date inside the fiscal year "+yearStart.String()+" to "+yearEnd.String()))
			return
		}
		*dst = n
	}
	pick("from", q.From, &from)
	pick("to", q.To, &to)
	if len(errs) == 0 && from > to {
		errs = append(errs, fieldErr("to", "BEFORE_FROM", "the end is before the start"))
	}
	if len(errs) > 0 {
		return yearStart, yearEnd, 0, 0, apperr.Invalid("the report parameters are invalid", errs...)
	}
	return yearStart, yearEnd, from, to, nil
}

// VsActual sets the budget against the actuals of the books (budget.view): by account and by department, laid out like the income statement down to
// net income, for a period and for the year to date. The actuals are the journals, the same source as the income statement.
func (s *Service) VsActual(ctx context.Context, propertyID int64, in VsActualQuery) (VsActual, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetView)
	if err != nil {
		return VsActual{}, err
	}
	cal, err := s.calendar(ctx, p.TenantID, propertyID)
	if err != nil {
		return VsActual{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return VsActual{}, err
	}
	yearStart, yearEnd, from, to, err := in.resolve(cal, day.BusinessDate)
	if err != nil {
		return VsActual{}, err
	}
	q := s.q(ctx)
	var ref budgetdb.GetBudgetRow
	if in.BudgetID != nil {
		ref, err = q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.BudgetID})
		if isNoRows(err) {
			return VsActual{}, errBudgetNotFound()
		}
		if err == nil && !ref.YearStart.Equal(yearStart) {
			return VsActual{}, apperr.Invalid("the report parameters are invalid", fieldErr("budget_id", "OTHER_YEAR", "the budget is of another fiscal year"))
		}
	} else {
		var id int64
		if id, err = q.ActiveBudgetOfYear(ctx, budgetdb.ActiveBudgetOfYearParams{TenantID: p.TenantID, PropertyID: propertyID, YearStart: yearStart}); isNoRows(err) {
			return VsActual{}, apperr.NotFound("NO_ACTIVE_BUDGET", "the fiscal year has no active budget: make a version active, or pick one").WithContext("year_start", yearStart.String())
		}
		if err == nil {
			ref, err = q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		}
	}
	if err != nil {
		return VsActual{}, err
	}
	periodFrom, periodTo := monthStart(yearStart, from), monthEnd(yearStart, to)

	// The four sets of figures: actual and budget, for the period and for the year to date.
	type meta struct {
		accounting.AccountBalance
		amounts [4]decimal.Decimal // debit minus credit: actual period, budget period, actual to date, budget to date
	}
	byID := map[int64]*meta{}
	touch := func(id int64, code, name, typ, group string) *meta {
		m := byID[id]
		if m == nil {
			m = &meta{AccountBalance: accounting.AccountBalance{ID: id, Code: code, Name: name, AccountType: typ, Group: group}}
			byID[id] = m
		}
		return m
	}
	for i, r := range []struct{ from, to civil.Date }{{periodFrom, periodTo}, {yearStart, periodTo}} {
		rows, err := q.ActualByAccount(ctx, budgetdb.ActualByAccountParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: r.from, ToDate: r.to})
		if err != nil {
			return VsActual{}, err
		}
		for _, x := range rows {
			touch(x.ID, x.Code, x.Name, x.AccountType, x.StatementGroup).amounts[i*2] = x.Balance
		}
	}
	for i, r := range []struct{ from, to int }{{from, to}, {1, to}} {
		rows, err := q.BudgetByAccount(ctx, budgetdb.BudgetByAccountParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: ref.ID, FromMonth: int16(r.from), ToMonth: int16(r.to)}) //nolint:gosec // G115: a month of the fiscal year is 1 to 12
		if err != nil {
			return VsActual{}, err
		}
		for _, x := range rows {
			bal := x.Amount // a budget is on the normal side of the account: back to debit minus credit
			if x.AccountType == accounting.TypeRevenue {
				bal = bal.Neg()
			}
			touch(x.ID, x.Code, x.Name, x.AccountType, x.StatementGroup).amounts[i*2+1] = bal
		}
	}
	accounts := make([]*meta, 0, len(byID))
	for _, m := range byID {
		if m.Group == "" {
			continue // an account with no statement group is in no line
		}
		accounts = append(accounts, m)
	}
	sort.Slice(accounts, func(i, j int) bool { return accounts[i].Code < accounts[j].Code })

	// The same accounts go through the layout four times, so the four layouts have the same lines and accounts in the same order.
	var layouts [4][]accounting.StatementLine
	for k := range layouts {
		rows := make([]accounting.AccountBalance, len(accounts))
		for i, m := range accounts {
			rows[i] = m.AccountBalance
			rows[i].Balance = m.amounts[k]
		}
		layouts[k], _ = accounting.IncomeLayout(rows)
	}
	out := VsActual{
		YearStart: yearStart, YearEnd: yearEnd, YearLabel: yearLabel(yearEnd), From: periodFrom, To: periodTo,
		Budget: BudgetRef{ID: ref.ID, Name: ref.Name, Version: int(ref.Version), Status: ref.Status}, Lines: make([]Line, 0, len(layouts[0])),
	}
	for i, l := range layouts[0] {
		better := higherIsBetter(l)
		line := Line{
			Key: l.Key, Title: l.Title, Kind: l.Kind, Accounts: make([]Account, 0, len(l.Accounts)),
			Period: newCell(layouts[0][i].Amount, layouts[1][i].Amount, better), YTD: newCell(layouts[2][i].Amount, layouts[3][i].Amount, better),
		}
		for j, a := range l.Accounts {
			line.Accounts = append(line.Accounts, Account{
				AccountID: a.AccountID, Code: a.Code, Name: a.Name,
				Period: newCell(layouts[0][i].Accounts[j].Amount, layouts[1][i].Accounts[j].Amount, better), YTD: newCell(layouts[2][i].Accounts[j].Amount, layouts[3][i].Accounts[j].Amount, better),
			})
		}
		out.Lines = append(out.Lines, line)
	}
	return out, nil
}
