package budget

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/budget/budgetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// figure is what an account has for a department: the actual and the budget of the period and of the year to date, on the normal side of the account (revenue and
// expense both positive).
type figure struct {
	accountType string
	code, name  string
	amounts     [4]decimal.Decimal // actual of the period, budget of the period, actual to date, budget to date
}

// figures reads the four sets of figures by account and department (0: none).
func (s *Service) figures(ctx context.Context, tenantID, propertyID, budgetID int64, yearStart civil.Date, from, to int) (map[rowKey]*figure, error) {
	q := s.q(ctx)
	periodFrom, periodTo := monthStart(yearStart, from), monthEnd(yearStart, to)
	out := map[rowKey]*figure{}
	touch := func(account, dept int64, typ, code, name string) *figure {
		k := rowKey{account, dept}
		f := out[k]
		if f == nil {
			f = &figure{accountType: typ, code: code, name: name}
			out[k] = f
		}
		return f
	}
	for i, r := range []struct{ from, to civil.Date }{{periodFrom, periodTo}, {yearStart, periodTo}} {
		rows, err := q.ActualByAccountDepartment(ctx, budgetdb.ActualByAccountDepartmentParams{TenantID: tenantID, PropertyID: propertyID, FromDate: r.from, ToDate: r.to})
		if err != nil {
			return nil, err
		}
		for _, x := range rows {
			touch(x.ID, x.DepartmentID, x.AccountType, x.Code, x.Name).amounts[i*2] = natural(x.AccountType, x.Balance)
		}
	}
	for i, r := range []struct{ from, to int }{{from, to}, {1, to}} {
		rows, err := q.BudgetByAccountDepartment(ctx, budgetdb.BudgetByAccountDepartmentParams{TenantID: tenantID, PropertyID: propertyID, BudgetID: budgetID, FromMonth: int16(r.from), ToMonth: int16(r.to)}) //nolint:gosec // G115: a month of the fiscal year is 1 to 12
		if err != nil {
			return nil, err
		}
		for _, x := range rows {
			touch(x.ID, x.DepartmentID, x.AccountType, x.Code, x.Name).amounts[i*2+1] = x.Amount
		}
	}
	return out, nil
}

// AccountDepartment is the cells of an account for one department of the report, under the account; no department is the figures that were posted or planned without one.
type AccountDepartment struct {
	DepartmentID *int64 `json:"department_id"`
	Code         string `json:"department_code"`
	Name         string `json:"department_name"`
	Period       Cell   `json:"period"`
	YTD          Cell   `json:"ytd"`
}

// departmentsOf lists the departments of an account of the report, the figures without one first, when the account has any department at all.
func departmentsOf(account int64, figs map[rowKey]*figure, names map[int64]deptName, better bool) []AccountDepartment {
	var keys []rowKey
	has := false
	for k := range figs {
		if k.account != account {
			continue
		}
		keys = append(keys, k)
		has = has || k.dept != 0
	}
	if !has {
		return []AccountDepartment{}
	}
	sort.Slice(keys, func(i, j int) bool { return names[keys[i].dept].code < names[keys[j].dept].code })
	out := make([]AccountDepartment, 0, len(keys))
	for _, k := range keys {
		f := figs[k]
		d := names[k.dept]
		out = append(out, AccountDepartment{
			DepartmentID: deptPtr(k.dept), Code: d.code, Name: d.name,
			Period: newCell(f.amounts[0], f.amounts[1], better), YTD: newCell(f.amounts[2], f.amounts[3], better),
		})
	}
	return out
}

// Pair is a figure for the period and for the year to date.
type Pair struct {
	Period Cell `json:"period"`
	YTD    Cell `json:"ytd"`
}

// DepartmentVariance is a department or a sub-department of the budget against actual by department: revenue, expense and the departmental profit, the actual against the budget. A department
// adds up its sub-departments.
type DepartmentVariance struct {
	ID       int64                `json:"id"`
	ParentID *int64               `json:"parent_id"`
	Code     string               `json:"code"`
	Name     string               `json:"name"`
	Level    int                  `json:"level"`
	Active   bool                 `json:"is_active"`
	Revenue  Pair                 `json:"revenue"`
	Expense  Pair                 `json:"expense"`
	Profit   Pair                 `json:"profit"`
	Children []DepartmentVariance `json:"children"`
}

// DepartmentVsActual is the budget against actual by department.
type DepartmentVsActual struct {
	YearStart   civil.Date           `json:"year_start"`
	YearEnd     civil.Date           `json:"year_end"`
	YearLabel   string               `json:"year_label"`
	From        civil.Date           `json:"from"`
	To          civil.Date           `json:"to"`
	Budget      BudgetRef            `json:"budget"`
	Departments []DepartmentVariance `json:"departments"`
	Unassigned  DepartmentVariance   `json:"unassigned"`
	Totals      DepartmentVariance   `json:"totals"`
}

// sums are the revenue and the expense of a department in the four sets.
type sums struct{ rev, exp [4]decimal.Decimal }

func (a *sums) add(b sums) {
	for i := range a.rev {
		a.rev[i], a.exp[i] = a.rev[i].Add(b.rev[i]), a.exp[i].Add(b.exp[i])
	}
}

func (a sums) zero() bool {
	for i := range a.rev {
		if !a.rev[i].IsZero() || !a.exp[i].IsZero() {
			return false
		}
	}
	return true
}

func variance(id int64, parent *int64, code, name string, level int, active bool, a sums) DepartmentVariance {
	var profit [4]decimal.Decimal
	for i := range profit {
		profit[i] = a.rev[i].Sub(a.exp[i])
	}
	pair := func(v [4]decimal.Decimal, better bool) Pair {
		return Pair{Period: newCell(v[0], v[1], better), YTD: newCell(v[2], v[3], better)}
	}
	return DepartmentVariance{
		ID: id, ParentID: parent, Code: code, Name: name, Level: level, Active: active,
		Revenue: pair(a.rev, true), Expense: pair(a.exp, false), Profit: pair(profit, true), Children: []DepartmentVariance{},
	}
}

// DepartmentVsActual sets the budget against the actuals of the books by department (budget.view): revenue, expenses and the departmental profit for the period and the year to date, a department
// adding up its sub-departments, what has no department apart. The range, the year and the version are those of VsActual; departmentID narrows it to one department or sub-department.
func (s *Service) DepartmentVsActual(ctx context.Context, propertyID int64, in VsActualQuery, departmentID *int64) (DepartmentVsActual, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetView)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	cal, err := s.calendar(ctx, p.TenantID, propertyID)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	yearStart, yearEnd, from, to, err := in.resolve(cal, day.BusinessDate)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	ref, err := s.resolveBudget(ctx, p.TenantID, propertyID, in, yearStart)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	names, err := s.departmentNames(ctx, p.TenantID, propertyID)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	if departmentID != nil {
		if _, ok := names[*departmentID]; !ok {
			return DepartmentVsActual{}, apperr.NotFound("DEPARTMENT_NOT_FOUND", "the department does not exist in this property")
		}
	}
	figs, err := s.figures(ctx, p.TenantID, propertyID, ref.ID, yearStart, from, to)
	if err != nil {
		return DepartmentVsActual{}, err
	}
	own := map[int64]*sums{}
	var all sums
	for k, f := range figs {
		a := own[k.dept]
		if a == nil {
			a = &sums{}
			own[k.dept] = a
		}
		for i, v := range f.amounts {
			if f.accountType == accounting.TypeRevenue {
				a.rev[i] = a.rev[i].Add(v)
			} else {
				a.exp[i] = a.exp[i].Add(v)
			}
		}
	}
	for _, a := range own {
		all.add(*a)
	}
	// each department adds up its sub-departments
	total := map[int64]sums{}
	for id, d := range names {
		var a sums
		if o := own[id]; o != nil {
			a = *o
		}
		if d.parent == nil {
			total[id] = a
		}
	}
	children := map[int64][]deptName{}
	for _, d := range names {
		if d.parent == nil {
			continue
		}
		children[*d.parent] = append(children[*d.parent], d)
		a := total[*d.parent]
		if o := own[d.id]; o != nil {
			a.add(*o)
		}
		total[*d.parent] = a
	}
	shows := func(d deptName, a sums) bool { return d.active || !a.zero() }
	order := func(list []deptName) {
		sort.Slice(list, func(i, j int) bool {
			if list[i].sortOrder != list[j].sortOrder {
				return list[i].sortOrder < list[j].sortOrder
			}
			return list[i].code < list[j].code
		})
	}
	var tops []deptName
	for _, d := range names {
		if d.parent == nil {
			tops = append(tops, d)
		}
	}
	order(tops)
	var nodes []DepartmentVariance
	for _, d := range tops {
		node := variance(d.id, nil, d.code, d.name, 1, d.active, total[d.id])
		kids := children[d.id]
		order(kids)
		for _, c := range kids {
			var a sums
			if o := own[c.id]; o != nil {
				a = *o
			}
			if shows(c, a) {
				node.Children = append(node.Children, variance(c.id, &d.id, c.code, c.name, 2, c.active, a))
			}
		}
		if shows(d, total[d.id]) || len(node.Children) > 0 {
			nodes = append(nodes, node)
		}
	}
	var un sums
	if o := own[0]; o != nil {
		un = *o
	}
	unassigned := variance(0, nil, "", "", 1, true, un)
	totals := variance(0, nil, "", "", 1, true, all)
	if departmentID != nil {
		nodes = narrowVariance(nodes, *departmentID)
		unassigned = variance(0, nil, "", "", 1, true, sums{})
		var sel sums
		for _, n := range nodes {
			t := total[n.ID]
			if n.Level == 2 {
				t = sums{}
				if o := own[n.ID]; o != nil {
					t = *o
				}
			}
			sel.add(t)
		}
		totals = variance(0, nil, "", "", 1, true, sel)
	}
	if nodes == nil {
		nodes = []DepartmentVariance{}
	}
	return DepartmentVsActual{
		YearStart: yearStart, YearEnd: yearEnd, YearLabel: yearLabel(yearEnd), From: monthStart(yearStart, from), To: monthEnd(yearStart, to),
		Budget: BudgetRef{ID: ref.ID, Name: ref.Name, Version: int(ref.Version), Status: ref.Status}, Departments: nodes, Unassigned: unassigned, Totals: totals,
	}, nil
}

// narrowVariance keeps the department asked for, or the sub-department asked for with nothing around it.
func narrowVariance(nodes []DepartmentVariance, id int64) []DepartmentVariance {
	for _, n := range nodes {
		if n.ID == id {
			return []DepartmentVariance{n}
		}
		for _, c := range n.Children {
			if c.ID == id {
				return []DepartmentVariance{c}
			}
		}
	}
	return []DepartmentVariance{}
}
