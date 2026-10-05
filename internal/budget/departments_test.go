package budget_test

import (
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/budget"
	"kamarapms/internal/departments"
	"kamarapms/internal/platform/apperr"
)

func (f *fx) deptID(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

func (f *fx) subDept(t *testing.T, code, name, parent string) int64 {
	t.Helper()
	p := f.deptID(t, parent)
	d, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: code, Name: name, ParentID: &p})
	must(t, err)
	return d.ID
}

// deptRow is a grid row of an account for a department (nil: none) with the same amount in the listed months.
func deptRow(account int64, dept *int64, amount string, months ...int) budget.RowInput {
	r := row(account, amount, months...)
	r.DepartmentID = dept
	return r
}

func rowsOf(b budget.Budget, code string) []budget.Row {
	var out []budget.Row
	for _, r := range b.Rows {
		if r.Code == code {
			out = append(out, r)
		}
	}
	return out
}

func codeOf(t *testing.T, err error, field string) string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("got %v, want an *apperr.Error", err)
	}
	for _, fe := range e.Fields {
		if fe.Field == field {
			return fe.Code
		}
	}
	t.Fatalf("no field error on %s: %+v", field, e.Fields)
	return ""
}

func TestTheFiguresOfAnAccountAreGivenPerDepartment(t *testing.T) {
	f := setup(t)
	rest := f.subDept(t, "REST", "Restaurant", "FB")
	bar := f.subDept(t, "BAR", "Bar", "FB")
	b := f.create(t, "2026-01-01", "Plan")
	b = f.save(t, b.ID,
		deptRow(f.rooms, &rest, "300000", 9), deptRow(f.rooms, &bar, "100000", 9), deptRow(f.rooms, nil, "50000", 9), deptRow(f.payroll, nil, "20000", 9))
	got := rowsOf(b, "4110")
	if len(got) != 3 {
		t.Fatalf("one row per department: %+v", b.Rows)
	}
	// the row without a department first, then by the code of the department
	if got[0].DepartmentID != nil || got[1].DepartmentCode != "BAR" || got[2].DepartmentCode != "REST" || got[2].DepartmentName != "Restaurant" {
		t.Fatalf("the order of the rows: %+v", got)
	}
	eqs(t, "revenue is the sum of the departments", b.Revenue, "450000")
	eqs(t, "the rows have their own totals", got[2].Total, "300000")
	if b.AccountCount != 2 {
		t.Fatalf("two accounts: %d", b.AccountCount)
	}

	// the same account and department twice is refused, and so is the same account without any twice
	_, err := f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{deptRow(f.rooms, &rest, "1", 1), deptRow(f.rooms, &rest, "2", 2)}})
	if codeOf(t, err, "rows[1].account_id") != "DUPLICATE" {
		t.Fatal("a duplicate row")
	}
	_, err = f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{deptRow(f.rooms, nil, "1", 1), deptRow(f.rooms, nil, "2", 2)}})
	if codeOf(t, err, "rows[1].account_id") != "DUPLICATE" {
		t.Fatal("two rows without a department")
	}
	// the database says the same, where "none" is a department of its own
	_, dbErr := f.Pool.Exec(context.Background(), `INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount)
		SELECT tenant_id, property_id, budget_id, account_id, month, 1 FROM budget_lines WHERE budget_id = $1 AND account_id = $2 AND department_id IS NULL AND month = 9`, b.ID, f.rooms)
	if dbErr == nil {
		t.Fatal("a second line of the same account, none department and month")
	}
	if got := rowsOf(mustGet(t, f, b.ID), "4110"); len(got) != 3 {
		t.Fatalf("a refused grid changes nothing: %d rows", len(got))
	}
}

func TestADepartmentOfARowMustBeOneOfThePropertyInUse(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "Plan")
	save := func(dept int64) error {
		_, err := f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{deptRow(f.rooms, &dept, "1", 1)}})
		return err
	}
	if codeOf(t, save(999999), "rows[0].department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("an unknown department")
	}
	if codeOf(t, save(0), "rows[0].department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("department 0")
	}
	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "SG")
	var foreign int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = 'ROOMS'`, op.ID).Scan(&foreign))
	if codeOf(t, save(foreign), "rows[0].department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("the department of another property")
	}
	spa := f.deptID(t, "OOD")
	_, err := f.Departments.Update(f.admin, f.propID, spa, departments.Patch{Active: ptr(false)})
	must(t, err)
	if codeOf(t, save(spa), "rows[0].department_id") != "INACTIVE" {
		t.Fatal("a switched off department")
	}
	// a department a budget uses is switched off, never deleted
	rooms := f.deptID(t, "ROOMS")
	must(t, save(rooms))
	err = f.Departments.Delete(f.admin, f.propID, rooms)
	wantCode(t, err, "DEPARTMENT_IN_USE")
	list, err := f.Departments.List(f.admin, f.propID, departments.Filter{})
	must(t, err)
	for _, d := range list {
		if d.ID == rooms && !d.InUse {
			t.Fatal("a department of a budget line is in use")
		}
	}
}

func TestSpreadAndRevisionKeepTheDepartmentsOfARow(t *testing.T) {
	f := setup(t)
	rest, bar := f.subDept(t, "REST", "Restaurant", "FB"), f.subDept(t, "BAR", "Bar", "FB")
	b := f.create(t, "2026-01-01", "Plan")
	b = f.save(t, b.ID, deptRow(f.rooms, &rest, "10", 1), deptRow(f.rooms, &bar, "20", 1))
	b, err := f.Budget.Spread(f.manager, f.propID, b.ID, budget.SpreadInput{AccountID: f.rooms, DepartmentID: &rest, Total: "1200", Method: budget.SpreadEqual})
	must(t, err)
	got := rowsOf(b, "4110")
	if len(got) != 2 || got[0].DepartmentCode != "BAR" || got[1].DepartmentCode != "REST" {
		t.Fatalf("both rows stay: %+v", got)
	}
	eqs(t, "the restaurant row was spread", got[1].Total, "1200")
	eqs(t, "the bar row was left alone", got[0].Total, "20")
	_, err = f.Budget.Spread(f.manager, f.propID, b.ID, budget.SpreadInput{AccountID: f.rooms, DepartmentID: ptr(int64(999999)), Total: "1", Method: budget.SpreadEqual})
	if codeOf(t, err, "department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("an unknown department")
	}

	// the revision copies the departments, and the active version does not change
	f.activate(t, b.ID)
	rev, err := f.Budget.Create(f.manager, f.propID, budget.CreateInput{Name: "Revision", CopyFromID: &b.ID})
	must(t, err)
	copied := rowsOf(rev, "4110")
	if len(copied) != 2 || copied[1].DepartmentCode != "REST" || copied[1].Total != "1200" {
		t.Fatalf("the copy: %+v", copied)
	}
	rev = f.save(t, rev.ID, deptRow(f.rooms, &rest, "5", 1))
	if len(rowsOf(rev, "4110")) != 1 || len(rowsOf(mustGet(t, f, b.ID), "4110")) != 2 {
		t.Fatal("the revision changed, the active version did not")
	}
}

func TestFillFromActualsGivesARowPerDepartment(t *testing.T) {
	f := setup(t)
	rest := f.subDept(t, "REST", "Restaurant", "FB")
	rooms := f.deptID(t, "ROOMS")
	post := func(key string, dept *int64, amount string) {
		_, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: key, Lines: []accounting.LineInput{
			{AccountID: f.cash, Debit: decimal.RequireFromString(amount)}, {AccountID: f.rooms, Credit: decimal.RequireFromString(amount), DepartmentID: dept},
		}}, key)
		must(t, err)
	}
	post("a", &rooms, "500000")
	post("b", &rest, "300000")
	post("c", nil, "100000")
	next := f.create(t, "2027-01-01", "Next")
	src := d("2026-01-01")
	next, err := f.Budget.FillFromActuals(f.manager, f.propID, next.ID, budget.FillInput{SourceYearStart: &src, PercentChange: "10"})
	must(t, err)
	got := rowsOf(next, "4110")
	if len(got) != 3 {
		t.Fatalf("a row per department and one for none: %+v", got)
	}
	byDept := map[string]string{}
	for _, r := range got {
		byDept[r.DepartmentCode] = r.Amounts[8]
	}
	if byDept["ROOMS"] != "550000" || byDept["REST"] != "330000" || byDept[""] != "110000" {
		t.Fatalf("the actuals of each department, raised by 10 percent: %v", byDept)
	}
	// the rows of a department that was switched off since are not filled
	_, err = f.Departments.Update(f.admin, f.propID, rest, departments.Patch{Active: ptr(false)})
	must(t, err)
	again := f.create(t, "2028-01-01", "Later")
	again, err = f.Budget.FillFromActuals(f.manager, f.propID, again.ID, budget.FillInput{SourceYearStart: &src})
	must(t, err)
	if got := rowsOf(again, "4110"); len(got) != 2 {
		t.Fatalf("not for a department that is off: %+v", got)
	}
}

func TestCSVCarriesTheDepartment(t *testing.T) {
	f := setup(t)
	rest := f.subDept(t, "REST", "Restaurant", "FB")
	b := f.create(t, "2026-01-01", "Plan")
	b = f.save(t, b.ID, deptRow(f.rooms, &rest, "300", 1), deptRow(f.rooms, nil, "50", 1))
	_, rows, err := f.Budget.ExportCSV(f.viewer, f.propID, b.ID)
	must(t, err)
	if len(rows) != 3 || rows[1][2] != "" || rows[2][2] != "REST" {
		t.Fatalf("the export names the department: %v", rows)
	}
	// the export reads back through the import
	var text strings.Builder
	for _, r := range rows {
		text.WriteString(strings.Join(r, ",") + "\n")
	}
	res, err := f.Budget.ImportCSV(f.manager, f.propID, b.ID, text.String(), false)
	must(t, err)
	if res.Accounts != 2 {
		t.Fatalf("two rows: %+v", res)
	}
	if got := rowsOf(mustGet(t, f, b.ID), "4110"); len(got) != 2 || got[1].DepartmentCode != "REST" || got[1].Total != "300" {
		t.Fatalf("read back: %+v", got)
	}
	head := "code,department,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10,m11,m12\n"
	_, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, head+"4110,NOPE,1,,,,,,,,,,,\n", false)
	if codeOf(t, err, "rows[2].department") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("an unknown department code")
	}
	_, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, head+"4110,rest,1,,,,,,,,,,,\n4110,REST,2,,,,,,,,,,,\n", false)
	if codeOf(t, err, "rows[3].code") != "DUPLICATE" {
		t.Fatal("the same account and department twice (the code of the department is read in upper case)")
	}
	ood := f.deptID(t, "OOD")
	_, err = f.Departments.Update(f.admin, f.propID, ood, departments.Patch{Active: ptr(false)})
	must(t, err)
	_, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, head+"4110,OOD,1,,,,,,,,,,,\n", false)
	if codeOf(t, err, "rows[2].department") != "INACTIVE" {
		t.Fatal("a department that is off is named by the line of the file")
	}
	if got := rowsOf(mustGet(t, f, b.ID), "4110"); len(got) != 2 {
		t.Fatalf("a refused file changes nothing: %+v", got)
	}
}

// departmentBooks posts on 30 Sep 2026: room revenue 600,000 to the restaurant and 200,000 to the bar (sub-departments of Food and beverage) and 100,000 to none, payroll of
// 150,000 to the restaurant. The plan for September: restaurant 500,000, bar 250,000, none 0; payroll of the restaurant 100,000.
func departmentBooks(t *testing.T) (*fx, budget.Budget, map[string]int64) {
	f := setup(t)
	ids := map[string]int64{"FB": f.deptID(t, "FB"), "REST": f.subDept(t, "REST", "Restaurant", "FB"), "BAR": f.subDept(t, "BAR", "Bar", "FB"), "ROOMS": f.deptID(t, "ROOMS")}
	post := func(key string, debit, credit int64, amount string, dept *int64) {
		_, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: key, Lines: []accounting.LineInput{
			{AccountID: debit, Debit: decimal.RequireFromString(amount), DepartmentID: deptIf(debit != f.cash, dept)}, {AccountID: credit, Credit: decimal.RequireFromString(amount), DepartmentID: deptIf(credit != f.cash, dept)},
		}}, key)
		must(t, err)
	}
	rest, bar := ids["REST"], ids["BAR"]
	post("a", f.cash, f.rooms, "600000", &rest)
	post("b", f.cash, f.rooms, "200000", &bar)
	post("c", f.cash, f.rooms, "100000", nil)
	post("d", f.payroll, f.cash, "150000", &rest)
	b := f.create(t, "2026-01-01", "Plan")
	b = f.save(t, b.ID, deptRow(f.rooms, &rest, "500000", 9), deptRow(f.rooms, &bar, "250000", 9), deptRow(f.payroll, &rest, "100000", 9))
	b = f.activate(t, b.ID)
	return f, b, ids
}

func deptIf(ok bool, d *int64) *int64 {
	if ok {
		return d
	}
	return nil
}

func TestTheBudgetAgainstActualShowsEachAccountByDepartment(t *testing.T) {
	f, _, _ := departmentBooks(t)
	from, to := d("2026-09-01"), d("2026-09-30")
	rep, err := f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{From: &from, To: &to})
	must(t, err)
	var acct budget.Account
	for _, l := range rep.Lines {
		for _, a := range l.Accounts {
			if a.Code == "4110" {
				acct = a
			}
		}
	}
	eqd(t, "the account: actual", acct.Period.Actual, "900000")
	eqd(t, "the account: budget", acct.Period.Budget, "750000")
	if len(acct.Departments) != 3 || acct.Departments[0].DepartmentID != nil || acct.Departments[1].Code != "BAR" || acct.Departments[2].Code != "REST" {
		t.Fatalf("the departments of the account, none first: %+v", acct.Departments)
	}
	none, bar, rest := acct.Departments[0], acct.Departments[1], acct.Departments[2]
	eqd(t, "none: actual", none.Period.Actual, "100000")
	eqd(t, "none: budget", none.Period.Budget, "0")
	eqd(t, "bar: actual", bar.Period.Actual, "200000")
	eqd(t, "bar: variance", bar.Period.Variance, "-50000")
	eqd(t, "restaurant: variance", rest.Period.Variance, "100000")
	if rest.Period.Favourable == nil || !*rest.Period.Favourable || bar.Period.Favourable == nil || *bar.Period.Favourable {
		t.Fatal("revenue above the budget is favourable, below it is not")
	}
	// the rows add up to the account
	sum := decimal.Zero
	for _, dpt := range acct.Departments {
		sum = sum.Add(dpt.Period.Actual)
	}
	eqd(t, "the departments add up to the account", sum, acct.Period.Actual.String())
	// an account with no department has no rows
	for _, l := range rep.Lines {
		for _, a := range l.Accounts {
			if a.Code != "4110" && a.Code != "5110" && len(a.Departments) != 0 {
				t.Fatalf("%s: %+v", a.Code, a.Departments)
			}
		}
	}
}

func TestTheBudgetAgainstActualByDepartmentAddsUpTheSubDepartments(t *testing.T) {
	f, _, ids := departmentBooks(t)
	from, to := d("2026-09-01"), d("2026-09-30")
	rep, err := f.Budget.DepartmentVsActual(f.viewer, f.propID, budget.VsActualQuery{From: &from, To: &to}, nil)
	must(t, err)
	var fb budget.DepartmentVariance
	for _, n := range rep.Departments {
		if n.Code == "FB" {
			fb = n
		}
	}
	// Food and beverage: the restaurant and the bar
	eqd(t, "FB revenue actual", fb.Revenue.Period.Actual, "800000")
	eqd(t, "FB revenue budget", fb.Revenue.Period.Budget, "750000")
	eqd(t, "FB expense actual", fb.Expense.Period.Actual, "150000")
	eqd(t, "FB expense budget", fb.Expense.Period.Budget, "100000")
	eqd(t, "FB profit actual", fb.Profit.Period.Actual, "650000")
	eqd(t, "FB profit budget", fb.Profit.Period.Budget, "650000")
	if fb.Profit.Period.Favourable != nil {
		t.Fatal("a profit on the budget is neither")
	}
	if fb.Expense.Period.Favourable == nil || *fb.Expense.Period.Favourable {
		t.Fatal("an expense above the budget is unfavourable")
	}
	if len(fb.Children) != 2 || fb.Children[0].Code != "BAR" || fb.Children[1].Code != "REST" || fb.Children[1].Level != 2 {
		t.Fatalf("sub-departments: %+v", fb.Children)
	}
	rest := fb.Children[1]
	eqd(t, "restaurant revenue", rest.Revenue.Period.Actual, "600000")
	eqd(t, "restaurant profit", rest.Profit.Period.Actual, "450000")
	eqd(t, "restaurant profit budget", rest.Profit.Period.Budget, "400000")
	eqd(t, "restaurant profit to date", rest.Profit.YTD.Actual, "450000")
	// what has no department is apart, and the totals are of everything
	eqd(t, "unassigned revenue", rep.Unassigned.Revenue.Period.Actual, "100000")
	eqd(t, "total revenue actual", rep.Totals.Revenue.Period.Actual, "900000")
	eqd(t, "total revenue budget", rep.Totals.Revenue.Period.Budget, "750000")
	eqd(t, "total expense actual", rep.Totals.Expense.Period.Actual, "150000")
	is, err := f.Accounting.IncomeStatement(f.admin, f.propID, &from, &to)
	must(t, err)
	eqd(t, "the total profit is the net income", rep.Totals.Profit.Period.Actual, is.NetIncome.String())

	// one department, or one sub-department
	one, err := f.Budget.DepartmentVsActual(f.viewer, f.propID, budget.VsActualQuery{From: &from, To: &to}, ptr(ids["FB"]))
	must(t, err)
	if len(one.Departments) != 1 || one.Departments[0].Code != "FB" {
		t.Fatalf("one department: %+v", one.Departments)
	}
	eqd(t, "its totals are its own", one.Totals.Revenue.Period.Actual, "800000")
	sub, err := f.Budget.DepartmentVsActual(f.viewer, f.propID, budget.VsActualQuery{From: &from, To: &to}, ptr(ids["BAR"]))
	must(t, err)
	if len(sub.Departments) != 1 || sub.Departments[0].Code != "BAR" || sub.Departments[0].Level != 2 {
		t.Fatalf("one sub-department: %+v", sub.Departments)
	}
	eqd(t, "bar revenue", sub.Totals.Revenue.Period.Actual, "200000")
	_, err = f.Budget.DepartmentVsActual(f.viewer, f.propID, budget.VsActualQuery{}, ptr(int64(999999)))
	wantCode(t, err, "DEPARTMENT_NOT_FOUND")
	nobody := f.User(t, f.tenantID, f.propID)
	_, err = f.Budget.DepartmentVsActual(nobody, f.propID, budget.VsActualQuery{}, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	// without an active budget there is nothing to set against
	f2 := setup(t)
	_, err = f2.Budget.DepartmentVsActual(f2.viewer, f2.propID, budget.VsActualQuery{}, nil)
	wantCode(t, err, "NO_ACTIVE_BUDGET")
}
