package budget

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/budget/budgetdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service is the budget application service. Reading needs budget.view; drafting, importing and copying budget.manage; making a version active
// needs budget.manage and the approval of someone who holds budget.approve (the caller may be the approver).
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	iam   *iam.Service
}

// NewService wires the service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, i *iam.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, iam: i}
}

func (s *Service) q(ctx context.Context) *budgetdb.Queries { return budgetdb.New(s.txm.DB(ctx)) }

func (s *Service) need(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

func errBudgetNotFound() *apperr.Error {
	return apperr.NotFound("BUDGET_NOT_FOUND", "the budget does not exist in this property")
}

func errNotDraft(status string) *apperr.Error {
	return apperr.Conflict("BUDGET_NOT_DRAFT", "only a draft budget is changed: copy the version to revise it").WithContext("status", status)
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

func nullable(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "budget", EntityID: id, Old: old, New: updated}
}

// calendar is the fiscal calendar of a property.
type calendar struct {
	startMonth int
	booksStart civil.Date
}

func (s *Service) calendar(ctx context.Context, tenantID, propertyID int64) (calendar, error) {
	cfg, err := s.q(ctx).GetFiscalSettings(ctx, budgetdb.GetFiscalSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if isNoRows(err) {
		return calendar{}, apperr.NotFound("ACCOUNTING_NOT_SET_UP", "accounting is not set up for this property")
	}
	if err != nil {
		return calendar{}, err
	}
	return calendar{startMonth: int(cfg.FiscalYearStartMonth), booksStart: cfg.StartDate}, nil
}

// checkYear says whether a date is the first day of a fiscal year a budget may be made for: from the year the books start in to a few years after the current one.
func (c calendar) checkYear(yearStart, today civil.Date) *apperr.FieldError {
	if yearStart.Day() != 1 || int(yearStart.Month()) != c.startMonth {
		fe := fieldErr("year_start", "INVALID_FISCAL_YEAR", "the first day of a fiscal year: the fiscal year starts in month "+strconv.Itoa(c.startMonth))
		return &fe
	}
	first, _ := fiscalYearOf(c.booksStart, c.startMonth)
	cur, _ := fiscalYearOf(today, c.startMonth)
	last := civil.NewDate(cur.Year()+yearsAhead, cur.Month(), 1)
	if yearStart.Before(first) || yearStart.After(last) {
		fe := fieldErr("year_start", "OUT_OF_RANGE", "from the fiscal year the books start in to "+strconv.Itoa(yearsAhead)+" years after the current one")
		return &fe
	}
	return nil
}

// ---------------------------------------------------------------------------------------------------------------
// Reading

func toBudget(r budgetdb.GetBudgetRow, decimals int32) Budget {
	_, end := fiscalYearOf(r.YearStart, int(r.YearStart.Month()))
	return Budget{
		ID: r.ID, YearStart: r.YearStart, YearEnd: end, YearLabel: yearLabel(end), Version: int(r.Version), Name: r.Name, Description: deref(r.Description), Status: r.Status,
		CopiedFromID: r.CopiedFromID, ActivatedAt: r.ActivatedAt, ActivatedBy: r.ActivatedBy, ApprovedBy: r.ApprovedBy, ArchivedAt: r.ArchivedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		AccountCount: int(r.AccountCount), Revenue: r.TotalRevenue.StringFixed(decimals), Expense: r.TotalExpense.StringFixed(decimals),
		Result: r.TotalRevenue.Sub(r.TotalExpense).StringFixed(decimals),
	}
}

// List answers the budgets of the property, the latest year and version first (budget.view).
func (s *Service) List(ctx context.Context, propertyID int64, f ListFilter) ([]Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetView)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if f.Status != "" && f.Status != StatusDraft && f.Status != StatusActive && f.Status != StatusArchived {
		return nil, apperr.Invalid("the filter is invalid", fieldErr("status", "INVALID_VALUE", "DRAFT, ACTIVE or ARCHIVED"))
	}
	rows, err := s.q(ctx).ListBudgets(ctx, budgetdb.ListBudgetsParams{TenantID: p.TenantID, PropertyID: propertyID, YearStart: f.YearStart, Status: nullable(f.Status)})
	if err != nil {
		return nil, err
	}
	out := make([]Budget, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBudget(budgetdb.GetBudgetRow(r), decimals))
	}
	return out, nil
}

// budgetable lists the accounts a budget may cover (revenue and expense accounts that take postings), by code, and by id.
func (s *Service) budgetable(ctx context.Context, tenantID, propertyID int64) ([]AccountRef, map[int64]AccountRef, error) {
	rows, err := s.q(ctx).ListBudgetAccounts(ctx, budgetdb.ListBudgetAccountsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, nil, err
	}
	list := make([]AccountRef, 0, len(rows))
	byID := make(map[int64]AccountRef, len(rows))
	for _, r := range rows {
		a := AccountRef{ID: r.ID, Code: r.Code, Name: r.Name, AccountType: r.AccountType, Group: r.StatementGroup, Active: r.IsActive}
		list = append(list, a)
		byID[a.ID] = a
	}
	return list, byID, nil
}

// grid is the figures of a budget by account, twelve amounts each.
func (s *Service) grid(ctx context.Context, tenantID, propertyID, budgetID int64) (map[int64]*[monthsInYear]decimal.Decimal, error) {
	lines, err := s.q(ctx).ListBudgetLines(ctx, budgetdb.ListBudgetLinesParams{TenantID: tenantID, PropertyID: propertyID, BudgetID: budgetID})
	if err != nil {
		return nil, err
	}
	out := map[int64]*[monthsInYear]decimal.Decimal{}
	for _, l := range lines {
		row := out[l.AccountID]
		if row == nil {
			row = new([monthsInYear]decimal.Decimal)
			out[l.AccountID] = row
		}
		if l.Month >= 1 && int(l.Month) <= monthsInYear {
			row[l.Month-1] = l.Amount
		}
	}
	return out, nil
}

func (s *Service) detail(ctx context.Context, tenantID, propertyID, id int64, decimals int32) (Budget, error) {
	row, err := s.q(ctx).GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if isNoRows(err) {
		return Budget{}, errBudgetNotFound()
	}
	if err != nil {
		return Budget{}, err
	}
	b := toBudget(row, decimals)
	b.Months = months(row.YearStart)
	accounts, _, err := s.budgetable(ctx, tenantID, propertyID)
	if err != nil {
		return Budget{}, err
	}
	grid, err := s.grid(ctx, tenantID, propertyID, id)
	if err != nil {
		return Budget{}, err
	}
	b.Available = accounts
	b.Rows = []Row{}
	for _, a := range accounts {
		g, ok := grid[a.ID]
		if !ok {
			continue
		}
		r := Row{AccountID: a.ID, Code: a.Code, Name: a.Name, AccountType: a.AccountType, Group: a.Group, Amounts: make([]string, monthsInYear)}
		total := decimal.Zero
		for i, v := range g {
			r.Amounts[i] = v.StringFixed(decimals)
			total = total.Add(v)
		}
		r.Total = total.StringFixed(decimals)
		b.Rows = append(b.Rows, r)
	}
	return b, nil
}

// Get answers a budget with its grid and the accounts it may still cover (budget.view).
func (s *Service) Get(ctx context.Context, propertyID, id int64) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetView)
	if err != nil {
		return Budget{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	return s.detail(ctx, p.TenantID, propertyID, id, decimals)
}

// ---------------------------------------------------------------------------------------------------------------
// Drafting

// lockDraft takes a budget (lock level 49) and answers it, after checking that it is a draft.
func (s *Service) lockDraft(ctx context.Context, tenantID, propertyID, id int64) (budgetdb.GetBudgetRow, error) {
	q := s.q(ctx)
	if _, err := q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: tenantID, PropertyID: propertyID, ID: id}); err != nil {
		if isNoRows(err) {
			return budgetdb.GetBudgetRow{}, errBudgetNotFound()
		}
		return budgetdb.GetBudgetRow{}, err
	}
	if err := db.LockRows(ctx, db.Budgets, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return budgetdb.GetBudgetRow{}, err
	}
	row, err := q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return row, err
	}
	if row.Status != StatusDraft {
		return row, errNotDraft(row.Status)
	}
	return row, nil
}

// Create starts a draft of a fiscal year, empty or copied from another version of the year (budget.manage). The new version is the next number of the year.
func (s *Service) Create(ctx context.Context, propertyID int64, in CreateInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	if fields := validText(in.Name, in.Description); len(fields) > 0 {
		return Budget{}, apperr.Invalid("the budget is invalid", fields...)
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cal, err := s.calendar(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		year := in.YearStart
		if in.CopyFromID != nil {
			src, err := q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.CopyFromID})
			if isNoRows(err) {
				return apperr.Invalid("the budget is invalid", fieldErr("copy_from_id", "NOT_FOUND", "the budget to copy does not exist in this property"))
			}
			if err != nil {
				return err
			}
			if year != nil && !year.Equal(src.YearStart) {
				return apperr.Invalid("the budget is invalid", fieldErr("year_start", "MISMATCH", "a copy keeps the year of the version it comes from"))
			}
			year = &src.YearStart
			if err := db.LockRows(ctx, db.Budgets, db.ForShare, propertyID, []int64{src.ID}); err != nil { // its figures do not change while they are copied
				return err
			}
		}
		if year == nil {
			return apperr.Invalid("the budget is invalid", fieldErr("year_start", "REQUIRED", "the first day of the fiscal year"))
		}
		if fe := cal.checkYear(*year, day.BusinessDate); fe != nil {
			return apperr.Invalid("the budget is invalid", *fe)
		}
		id, err := q.InsertBudget(ctx, budgetdb.InsertBudgetParams{
			TenantID: p.TenantID, PropertyID: propertyID, YearStart: *year, Name: strings.TrimSpace(in.Name), Description: nullable(in.Description), CopiedFromID: in.CopyFromID, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if in.CopyFromID != nil {
			if err := q.CopyBudgetLines(ctx, budgetdb.CopyBudgetLinesParams{TenantID: p.TenantID, PropertyID: propertyID, FromBudgetID: *in.CopyFromID, ToBudgetID: id}); err != nil {
				return err
			}
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.created", id, nil,
			map[string]any{"year": out.YearLabel, "version": out.Version, "name": out.Name, "copied_from": in.CopyFromID}))
	})
	return out, err
}

// Update renames a draft (budget.manage).
func (s *Service) Update(ctx context.Context, propertyID, id int64, in UpdateInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	if fields := validText(in.Name, in.Description); len(fields) > 0 {
		return Budget{}, apperr.Invalid("the budget is invalid", fields...)
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		old, err := s.lockDraft(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if _, err := s.q(ctx).UpdateBudgetText(ctx, budgetdb.UpdateBudgetTextParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: strings.TrimSpace(in.Name), Description: nullable(in.Description), ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.updated", id, map[string]any{"name": old.Name, "description": deref(old.Description)},
			map[string]any{"name": out.Name, "description": out.Description}))
	})
	return out, err
}

// Delete removes a draft with its figures (budget.manage).
func (s *Service) Delete(ctx context.Context, propertyID, id int64) error {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		old, err := s.lockDraft(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.q(ctx).DeleteBudget(ctx, budgetdb.DeleteBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.deleted", id,
			map[string]any{"year": yearLabelOf(old.YearStart), "version": old.Version, "name": old.Name}, nil))
	})
}

func yearLabelOf(yearStart civil.Date) string {
	_, end := fiscalYearOf(yearStart, int(yearStart.Month()))
	return yearLabel(end)
}

// ---------------------------------------------------------------------------------------------------------------
// The grid

// gridRow is the twelve amounts of an account on their way to the database.
type gridRow struct {
	accountID int64
	amounts   [monthsInYear]decimal.Decimal
}

// putRows adds rows to the grid of a draft (the caller holds its lock). The cells go in one statement.
func (s *Service) putRows(ctx context.Context, tenantID, propertyID, budgetID int64, rows []gridRow, decimals int32) error {
	if len(rows) == 0 {
		return nil
	}
	cells := make([]cell, 0, len(rows)*monthsInYear)
	for _, r := range rows {
		for i, a := range r.amounts {
			cells = append(cells, cell{AccountID: r.accountID, Month: i + 1, Amount: a.StringFixed(decimals)})
		}
	}
	raw, err := json.Marshal(cells)
	if err != nil {
		return err
	}
	return s.q(ctx).InsertBudgetLines(ctx, budgetdb.InsertBudgetLinesParams{TenantID: tenantID, PropertyID: propertyID, BudgetID: budgetID, Cells: raw})
}

// parseGrid reads the rows of a grid and returns every problem it finds, row by row.
func parseGrid(rows []RowInput, byID map[int64]AccountRef, decimals int32) ([]gridRow, []apperr.FieldError) {
	var out []gridRow
	var fields []apperr.FieldError
	if len(rows) > maxGridRows {
		return nil, []apperr.FieldError{fieldErr("rows", "TOO_MANY_ROWS", "at most 1000 accounts")}
	}
	seen := map[int64]int{}
	for i, r := range rows {
		at := func(f string) string { return "rows[" + strconv.Itoa(i) + "]." + f }
		if _, ok := byID[r.AccountID]; !ok {
			fields = append(fields, fieldErr(at("account_id"), "INVALID_ACCOUNT", "a revenue or expense account of this property that takes postings"))
			continue
		}
		if first, dup := seen[r.AccountID]; dup {
			fields = append(fields, fieldErr(at("account_id"), "DUPLICATE", "already on row "+strconv.Itoa(first)))
			continue
		}
		seen[r.AccountID] = i
		if len(r.Amounts) != monthsInYear {
			fields = append(fields, fieldErr(at("amounts"), "INVALID_VALUE", "twelve amounts, one per month of the fiscal year"))
			continue
		}
		row := gridRow{accountID: r.AccountID}
		ok := true
		for m, a := range r.Amounts {
			d, fe := parseAmount("rows["+strconv.Itoa(i)+"].amounts["+strconv.Itoa(m)+"]", a, decimals)
			if fe != nil {
				fields = append(fields, *fe)
				ok = false
				continue
			}
			row.amounts[m] = d
		}
		if ok {
			out = append(out, row)
		}
	}
	return out, fields
}

// SaveGrid replaces the figures of a draft with a grid (budget.manage).
func (s *Service) SaveGrid(ctx context.Context, propertyID, id int64, in GridInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if _, err := s.lockDraft(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		_, byID, err := s.budgetable(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		rows, fields := parseGrid(in.Rows, byID, decimals)
		if len(fields) > 0 {
			return apperr.Invalid("the figures are invalid", fields...)
		}
		if err := s.replaceGrid(ctx, p.TenantID, propertyID, id, rows, decimals); err != nil {
			return err
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.grid_saved", id, nil,
			map[string]any{"accounts": out.AccountCount, "revenue": out.Revenue, "expense": out.Expense}))
	})
	return out, err
}

func (s *Service) replaceGrid(ctx context.Context, tenantID, propertyID, id int64, rows []gridRow, decimals int32) error {
	if err := s.q(ctx).DeleteBudgetLines(ctx, budgetdb.DeleteBudgetLinesParams{TenantID: tenantID, PropertyID: propertyID, BudgetID: id}); err != nil {
		return err
	}
	return s.putRows(ctx, tenantID, propertyID, id, rows, decimals)
}

// previousYear is the fiscal year before the one that starts on yearStart.
func previousYear(yearStart civil.Date) (start, end civil.Date) {
	start = civil.NewDate(yearStart.Year()-1, yearStart.Month(), 1)
	return start, yearStart.AddDays(-1)
}

// actualMonths answers the actuals of a fiscal year by account and month number, on the normal side of each account.
func (s *Service) actualMonths(ctx context.Context, tenantID, propertyID int64, yearStart civil.Date, types map[int64]AccountRef) (map[int64]*[monthsInYear]decimal.Decimal, error) {
	_, end := fiscalYearOf(yearStart, int(yearStart.Month()))
	rows, err := s.q(ctx).ActualByAccountMonth(ctx, budgetdb.ActualByAccountMonthParams{TenantID: tenantID, PropertyID: propertyID, FromDate: yearStart, ToDate: end})
	if err != nil {
		return nil, err
	}
	out := map[int64]*[monthsInYear]decimal.Decimal{}
	for _, r := range rows {
		a, ok := types[r.AccountID]
		if !ok {
			continue
		}
		n := monthNumber(yearStart, r.MonthStart)
		if n < 1 || n > monthsInYear {
			continue
		}
		row := out[r.AccountID]
		if row == nil {
			row = new([monthsInYear]decimal.Decimal)
			out[r.AccountID] = row
		}
		row[n-1] = natural(a.AccountType, r.Balance)
	}
	return out, nil
}

// Spread sets the twelve months of an account from a yearly figure of a draft, equally or after the pattern of the actuals of the year before (budget.manage).
func (s *Service) Spread(ctx context.Context, propertyID, id int64, in SpreadInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	var fields []apperr.FieldError
	total, fe := parseAmount("total", in.Total, decimals)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if in.Method != SpreadEqual && in.Method != SpreadLastYear {
		fields = append(fields, fieldErr("method", "INVALID_VALUE", SpreadEqual+" or "+SpreadLastYear))
	}
	if len(fields) > 0 {
		return Budget{}, apperr.Invalid("the spread is invalid", fields...)
	}
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockDraft(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		_, byID, err := s.budgetable(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		if _, ok := byID[in.AccountID]; !ok {
			return apperr.Invalid("the spread is invalid", fieldErr("account_id", "INVALID_ACCOUNT", "a revenue or expense account of this property that takes postings"))
		}
		var amounts []decimal.Decimal
		if in.Method == SpreadEqual {
			amounts = spreadEqual(total, decimals)
		} else {
			prev, _ := previousYear(b.YearStart)
			actual, err := s.actualMonths(ctx, p.TenantID, propertyID, prev, byID)
			if err != nil {
				return err
			}
			pattern := make([]decimal.Decimal, monthsInYear)
			if a := actual[in.AccountID]; a != nil {
				copy(pattern, a[:])
			}
			var ok bool
			if amounts, ok = spreadPattern(total, pattern, decimals); !ok {
				return apperr.Invalid("the spread is invalid", fieldErr("method", "NO_PATTERN", "the account has no actuals in the year before to follow: spread it equally"))
			}
		}
		row := gridRow{accountID: in.AccountID}
		copy(row.amounts[:], amounts)
		if err := s.q(ctx).DeleteBudgetLinesOfAccount(ctx, budgetdb.DeleteBudgetLinesOfAccountParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: id, AccountID: in.AccountID}); err != nil {
			return err
		}
		if err := s.putRows(ctx, p.TenantID, propertyID, id, []gridRow{row}, decimals); err != nil {
			return err
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.spread", id, nil,
			map[string]any{"account_id": in.AccountID, "total": total.StringFixed(decimals), "method": in.Method}))
	})
	return out, err
}

// FillFromActuals fills a draft with the actuals of a fiscal year, raised or lowered by a percent (budget.manage). Only revenue and expense accounts that take
// postings are filled.
func (s *Service) FillFromActuals(ctx context.Context, propertyID, id int64, in FillInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	pct := decimal.Zero
	if v := strings.TrimSpace(in.PercentChange); v != "" {
		d, perr := decimal.NewFromString(v)
		if perr != nil || d.LessThan(decimal.NewFromInt(-100)) || d.GreaterThan(decimal.NewFromInt(1000)) || !d.Equal(d.Round(2)) {
			return Budget{}, apperr.Invalid("the percent is invalid", fieldErr("percent_change", "INVALID_VALUE", "a percent from -100 to 1000, up to two decimals"))
		}
		pct = d
	}
	factor := decimal.NewFromInt(1).Add(pct.Div(decimal.NewFromInt(100)))
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		cal, err := s.calendar(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		b, err := s.lockDraft(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		source, _ := previousYear(b.YearStart)
		if in.SourceYearStart != nil {
			source = *in.SourceYearStart
			if source.Day() != 1 || int(source.Month()) != cal.startMonth {
				return apperr.Invalid("the source is invalid", fieldErr("source_year_start", "INVALID_FISCAL_YEAR", "the first day of a fiscal year: the fiscal year starts in month "+strconv.Itoa(cal.startMonth)))
			}
		}
		_, byID, err := s.budgetable(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		actual, err := s.actualMonths(ctx, p.TenantID, propertyID, source, byID)
		if err != nil {
			return err
		}
		have, err := s.grid(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		ids := make([]int64, 0, len(actual))
		for aid := range actual {
			ids = append(ids, aid)
		}
		sort.Slice(ids, func(i, j int) bool { return byID[ids[i]].Code < byID[ids[j]].Code })
		var rows []gridRow
		for _, aid := range ids {
			if _, kept := have[aid]; kept && !in.Replace {
				continue
			}
			row := gridRow{accountID: aid}
			zero := true
			for m, v := range actual[aid] {
				row.amounts[m] = v.Mul(factor).Round(decimals)
				zero = zero && row.amounts[m].IsZero()
			}
			if !zero {
				rows = append(rows, row)
			}
		}
		if len(rows) == 0 {
			return apperr.Conflict("NO_ACTUALS", "the fiscal year has no revenue or expenses in the books to copy").WithContext("source_year_start", source.String())
		}
		if in.Replace {
			if err := s.q(ctx).DeleteBudgetLines(ctx, budgetdb.DeleteBudgetLinesParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: id}); err != nil {
				return err
			}
		}
		if err := s.putRows(ctx, p.TenantID, propertyID, id, rows, decimals); err != nil {
			return err
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.filled_from_actuals", id, nil,
			map[string]any{"source_year_start": source.String(), "percent_change": pct.String(), "replace": in.Replace, "accounts": len(rows)}))
	})
	return out, err
}

// ---------------------------------------------------------------------------------------------------------------
// Activation

// Activate makes a draft the active version of its fiscal year (budget.manage plus the approval of someone who holds budget.approve (the caller may be the approver)). The version
// that was active becomes archived. A budget with no figures is not activated.
func (s *Service) Activate(ctx context.Context, propertyID, id int64, in ActivateInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	approval, err := s.iam.VerifyApprovalFor(ctx, propertyID, in.Approval, auth.PermBudgetApprove)
	if err != nil {
		return Budget{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		first, err := q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if isNoRows(err) {
			return errBudgetNotFound()
		}
		if err != nil {
			return err
		}
		// Every version of the year is locked together, in one call, so two activations of a year take turns.
		ids, err := q.BudgetIDsOfYear(ctx, budgetdb.BudgetIDsOfYearParams{TenantID: p.TenantID, PropertyID: propertyID, YearStart: first.YearStart})
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Budgets, db.ForUpdate, propertyID, ids); err != nil {
			return err
		}
		b, err := q.GetBudget(ctx, budgetdb.GetBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return err
		}
		if b.Status != StatusDraft {
			return errNotDraft(b.Status)
		}
		if b.AccountCount == 0 {
			return apperr.Conflict("BUDGET_EMPTY", "a budget with no figures is not made active")
		}
		now := s.clock.Now()
		var archived *int64
		prev, err := q.ActiveBudgetOfYear(ctx, budgetdb.ActiveBudgetOfYearParams{TenantID: p.TenantID, PropertyID: propertyID, YearStart: b.YearStart})
		switch {
		case err == nil:
			if err := q.ArchiveBudget(ctx, budgetdb.ArchiveBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: prev, ArchivedAt: now, ActorID: p.ActorID()}); err != nil {
				return err
			}
			archived = &prev
		case !isNoRows(err):
			return err
		}
		if err := q.ActivateBudget(ctx, budgetdb.ActivateBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, ActivatedAt: now, ActorID: p.ActorID(), ApprovedBy: approval.UserID()}); err != nil {
			return err
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.activated", id, map[string]any{"status": StatusDraft},
			map[string]any{"status": StatusActive, "year": out.YearLabel, "version": out.Version, "approved_by": approval.UserID(), "archived_budget_id": archived}))
	})
	return out, err
}
