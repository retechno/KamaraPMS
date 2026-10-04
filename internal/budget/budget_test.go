package budget_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/budget"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func d(s string) civil.Date { return civil.MustParseDate(s) }

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	adminEmail       string
	manager          context.Context // budget.view and budget.manage
	viewer           context.Context // budget.view only
	rooms, payroll   int64           // 4110 room revenue (revenue), 5110 rooms payroll (expense)
	admin6110        int64           // 6110 administrative payroll (expense)
	cash             int64           // 1110 (a balance sheet account)
}

// setup: a property (IDR, no decimals, fiscal year from January, books starting on 30 Sep 2026), an admin, a manager and a viewer.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, adminEmail: email}
	f.manager = e.User(t, tn.ID, p.ID, auth.PermBudgetView, auth.PermBudgetManage)
	f.viewer = e.User(t, tn.ID, p.ID, auth.PermBudgetView)
	f.rooms, f.payroll, f.admin6110, f.cash = f.account(t, "4110"), f.account(t, "5110"), f.account(t, "6110"), f.account(t, "1110")
	return f
}

func (f *fx) account(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}
}

// journal posts a balanced manual journal on the business date: debit one account, credit another.
func (f *fx) journal(t *testing.T, debit, credit int64, amount, key string) {
	t.Helper()
	_, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: "manual " + key, Lines: []accounting.LineInput{
		{AccountID: debit, Debit: decimal.RequireFromString(amount)}, {AccountID: credit, Credit: decimal.RequireFromString(amount)},
	}}, key)
	must(t, err)
}

func (f *fx) create(t *testing.T, year, name string) budget.Budget {
	t.Helper()
	y := d(year)
	b, err := f.Budget.Create(f.manager, f.propID, budget.CreateInput{YearStart: &y, Name: name})
	must(t, err)
	return b
}

// row is a grid row with the same amount in the listed months (1 to 12) and zero in the others.
func row(account int64, amount string, months ...int) budget.RowInput {
	r := budget.RowInput{AccountID: account, Amounts: make([]string, 12)}
	for i := range r.Amounts {
		r.Amounts[i] = "0"
	}
	for _, m := range months {
		r.Amounts[m-1] = amount
	}
	return r
}

func (f *fx) save(t *testing.T, id int64, rows ...budget.RowInput) budget.Budget {
	t.Helper()
	b, err := f.Budget.SaveGrid(f.manager, f.propID, id, budget.GridInput{Rows: rows})
	must(t, err)
	return b
}

func (f *fx) activate(t *testing.T, id int64) budget.Budget {
	t.Helper()
	b, err := f.Budget.Activate(f.manager, f.propID, id, budget.ActivateInput{Approval: f.approval()})
	must(t, err)
	return b
}

func eqd(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

func eqs(t *testing.T, what, got, want string) {
	t.Helper()
	if !decimal.RequireFromString(got).Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

func fieldCodes(t *testing.T, err error) map[string]string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || len(e.Fields) == 0 {
		t.Fatalf("got %v, want a validation error with fields", err)
	}
	out := map[string]string{}
	for _, fe := range e.Fields {
		out[fe.Field] = fe.Code
	}
	return out
}

func rowOf(t *testing.T, b budget.Budget, code string) budget.Row {
	t.Helper()
	for _, r := range b.Rows {
		if r.Code == code {
			return r
		}
	}
	t.Fatalf("no row for account %s in %+v", code, b.Rows)
	return budget.Row{}
}

func lineOf(t *testing.T, r budget.VsActual, key string) budget.Line {
	t.Helper()
	for _, l := range r.Lines {
		if l.Key == key {
			return l
		}
	}
	t.Fatalf("no line %s", key)
	return budget.Line{}
}

func TestDraftActivationAndRevision(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "Budget 2026")
	if b.Version != 1 || b.Status != budget.StatusDraft || b.YearLabel != "FY2026" || len(b.Months) != 12 || !b.YearEnd.Equal(d("2026-12-31")) {
		t.Fatalf("a new draft: %+v", b)
	}
	if len(b.Available) == 0 {
		t.Fatal("the draft lists the accounts it may cover")
	}
	if second := f.create(t, "2026-01-01", "Second try"); second.Version != 2 {
		t.Fatalf("the next version of the year is 2: %d", second.Version)
	}

	b = f.save(t, b.ID, row(f.rooms, "1000000", 1, 2, 3), row(f.payroll, "200000", 1, 2, 3, 4))
	if b.AccountCount != 2 || len(b.Rows) != 2 {
		t.Fatalf("two accounts in the grid: %+v", b.Rows)
	}
	r := rowOf(t, b, "4110")
	eqs(t, "the total of a row", r.Total, "3000000")
	eqs(t, "revenue of the budget", b.Revenue, "3000000")
	eqs(t, "expense of the budget", b.Expense, "800000")
	eqs(t, "result of the budget", b.Result, "2200000")

	b, err := f.Budget.Update(f.manager, f.propID, b.ID, budget.UpdateInput{Name: "Budget 2026 (first)", Description: "the plan"})
	must(t, err)
	if b.Name != "Budget 2026 (first)" || b.Description != "the plan" {
		t.Fatalf("renamed: %+v", b)
	}

	// An approval is required, and a budget with no figures is not activated.
	_, err = f.Budget.Activate(f.manager, f.propID, b.ID, budget.ActivateInput{})
	wantCode(t, err, "APPROVAL_REQUIRED")
	empty := f.create(t, "2026-01-01", "Empty")
	_, err = f.Budget.Activate(f.manager, f.propID, empty.ID, budget.ActivateInput{Approval: f.approval()})
	wantCode(t, err, "BUDGET_EMPTY")

	active := f.activate(t, b.ID)
	if active.Status != budget.StatusActive || active.ApprovedBy == nil || active.ActivatedAt == nil {
		t.Fatalf("activated: %+v", active)
	}

	// An active budget is fixed.
	_, err = f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{row(f.rooms, "1", 1)}})
	wantCode(t, err, "BUDGET_NOT_DRAFT")
	_, err = f.Budget.Update(f.manager, f.propID, b.ID, budget.UpdateInput{Name: "x"})
	wantCode(t, err, "BUDGET_NOT_DRAFT")
	wantCode(t, f.Budget.Delete(f.manager, f.propID, b.ID), "BUDGET_NOT_DRAFT")
	_, err = f.Budget.Activate(f.manager, f.propID, b.ID, budget.ActivateInput{Approval: f.approval()})
	wantCode(t, err, "BUDGET_NOT_DRAFT")

	// A revision is a copy that keeps the year and takes the next version; activating it archives the version it replaces.
	copyOf, err := f.Budget.Create(f.manager, f.propID, budget.CreateInput{Name: "Revision", CopyFromID: &b.ID})
	must(t, err)
	if copyOf.Version != 4 || copyOf.Status != budget.StatusDraft || !copyOf.YearStart.Equal(b.YearStart) || copyOf.CopiedFromID == nil || *copyOf.CopiedFromID != b.ID {
		t.Fatalf("a copy of the active version: %+v", copyOf)
	}
	eqs(t, "the copy has the figures", rowOf(t, copyOf, "4110").Total, "3000000")
	copyOf = f.save(t, copyOf.ID, row(f.rooms, "1200000", 1, 2, 3))
	eqs(t, "the revision changed", copyOf.Revenue, "3600000")
	eqs(t, "the active version did not", mustGet(t, f, b.ID).Revenue, "3000000")
	y := d("2027-01-01")
	_, err = f.Budget.Create(f.manager, f.propID, budget.CreateInput{YearStart: &y, Name: "Other year", CopyFromID: &b.ID})
	if got := fieldCodes(t, err); got["year_start"] != "MISMATCH" {
		t.Fatalf("a copy keeps its year: %v", got)
	}

	f.activate(t, copyOf.ID)
	if got := mustGet(t, f, b.ID); got.Status != budget.StatusArchived || got.ArchivedAt == nil {
		t.Fatalf("the replaced version is archived: %+v", got)
	}
	if got := mustGet(t, f, copyOf.ID); got.Status != budget.StatusActive {
		t.Fatalf("the revision is active: %+v", got)
	}
	_, err = f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{row(f.rooms, "1", 1)}})
	wantCode(t, err, "BUDGET_NOT_DRAFT")

	// A draft is deleted with its figures; a draft cannot be deleted while a copy came from it.
	must(t, f.Budget.Delete(f.manager, f.propID, empty.ID))
	_, err = f.Budget.Get(f.manager, f.propID, empty.ID)
	wantCode(t, err, "BUDGET_NOT_FOUND")
	source := f.create(t, "2027-01-01", "Source")
	f.save(t, source.ID, row(f.rooms, "1", 1))
	_, err = f.Budget.Create(f.manager, f.propID, budget.CreateInput{Name: "Copy", CopyFromID: &source.ID})
	must(t, err)
	wantCode(t, f.Budget.Delete(f.manager, f.propID, source.ID), "BUDGET_IN_USE")
	if n := f.Count(t, `SELECT count(*) FROM budget_lines WHERE budget_id = $1`, source.ID); n != 12 {
		t.Fatalf("a refused deletion keeps the figures: %d lines", n)
	}
}

func mustGet(t *testing.T, f *fx, id int64) budget.Budget {
	t.Helper()
	b, err := f.Budget.Get(f.manager, f.propID, id)
	must(t, err)
	return b
}

func TestListAndFilters(t *testing.T) {
	f := setup(t)
	a := f.create(t, "2026-01-01", "A")
	f.create(t, "2027-01-01", "B")
	f.save(t, a.ID, row(f.rooms, "100", 1))
	f.activate(t, a.ID)
	all, err := f.Budget.List(f.viewer, f.propID, budget.ListFilter{})
	must(t, err)
	if len(all) != 2 || all[0].YearLabel != "FY2027" {
		t.Fatalf("latest year first: %+v", all)
	}
	y := d("2026-01-01")
	got, err := f.Budget.List(f.viewer, f.propID, budget.ListFilter{YearStart: &y})
	must(t, err)
	if len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("by year: %+v", got)
	}
	got, err = f.Budget.List(f.viewer, f.propID, budget.ListFilter{Status: budget.StatusDraft})
	must(t, err)
	if len(got) != 1 || got[0].Name != "B" {
		t.Fatalf("by status: %+v", got)
	}
	_, err = f.Budget.List(f.viewer, f.propID, budget.ListFilter{Status: "OPEN"})
	if fieldCodes(t, err)["status"] != "INVALID_VALUE" {
		t.Fatal("an unknown status is refused")
	}
}

func TestPermissionsAndIsolation(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "A")

	y := d("2026-01-01")
	_, err := f.Budget.Create(f.viewer, f.propID, budget.CreateInput{YearStart: &y, Name: "no"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Budget.SaveGrid(f.viewer, f.propID, b.ID, budget.GridInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	wantCode(t, f.Budget.Delete(f.viewer, f.propID, b.ID), "PERMISSION_DENIED")
	if _, err := f.Budget.Get(f.viewer, f.propID, b.ID); err != nil {
		t.Fatalf("a viewer reads: %v", err)
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermAccountingView)
	_, err = f.Budget.List(nobody, f.propID, budget.ListFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Budget.VsActual(nobody, f.propID, budget.VsActualQuery{})
	wantCode(t, err, "PERMISSION_DENIED")

	// Activating needs an approver who holds budget.approve.
	f.save(t, b.ID, row(f.rooms, "100", 1))
	_, clerkEmail := f.Account(t, f.tenantID, f.propID, auth.PermBudgetView)
	_, err = f.Budget.Activate(f.manager, f.propID, b.ID, budget.ActivateInput{Approval: &iam.ApprovalInput{Email: clerkEmail, Password: roomstest.Password}})
	wantCode(t, err, "APPROVAL_NOT_PERMITTED")
	_, boss := f.Account(t, f.tenantID, f.propID, auth.PermBudgetApprove)
	if _, err := f.Budget.Activate(f.manager, f.propID, b.ID, budget.ActivateInput{Approval: &iam.ApprovalInput{Email: boss, Password: roomstest.Password}}); err != nil {
		t.Fatalf("an approver with budget.approve: %v", err)
	}

	// Another tenant sees nothing of this property.
	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "SG")
	adminOther := roomstest.Admin(other.ID)
	_, err = f.Budget.List(adminOther, f.propID, budget.ListFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Budget.Get(adminOther, f.propID, b.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	// Its own property cannot reach the budget by id, nor use the accounts of this property.
	_, err = f.Budget.Get(adminOther, op.ID, b.ID)
	wantCode(t, err, "BUDGET_NOT_FOUND")
	var foreign int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '4110'`, op.ID).Scan(&foreign))
	draft := f.create(t, "2027-01-01", "D")
	_, err = f.Budget.SaveGrid(f.manager, f.propID, draft.ID, budget.GridInput{Rows: []budget.RowInput{row(foreign, "1", 1)}})
	if fieldCodes(t, err)["rows[0].account_id"] != "INVALID_ACCOUNT" {
		t.Fatal("an account of another tenant is refused")
	}
}

func TestValidation(t *testing.T) {
	f := setup(t)
	y := func(s string) *civil.Date { v := d(s); return &v }
	for name, in := range map[string]budget.CreateInput{
		"not a first of the month":    {YearStart: y("2026-01-15"), Name: "x"},
		"not the first month":         {YearStart: y("2026-03-01"), Name: "x"},
		"before the books start":      {YearStart: y("2025-01-01"), Name: "x"},
		"too far ahead":               {YearStart: y("2029-01-01"), Name: "x"},
		"no year and no copy":         {Name: "x"},
		"a name is needed":            {YearStart: y("2026-01-01"), Name: "  "},
		"a long name":                 {YearStart: y("2026-01-01"), Name: strings.Repeat("n", 101)},
		"a long description":          {YearStart: y("2026-01-01"), Name: "x", Description: strings.Repeat("d", 501)},
		"a copy of a missing version": {Name: "x", CopyFromID: new(int64)},
	} {
		_, err := f.Budget.Create(f.manager, f.propID, in)
		if e, ok := apperr.As(err); !ok || len(e.Fields) == 0 {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
	b := f.create(t, "2026-01-01", "A")
	good := row(f.rooms, "100", 1)
	short := budget.RowInput{AccountID: f.rooms, Amounts: []string{"1", "2"}}
	for name, rows := range map[string][]budget.RowInput{
		"a balance sheet account": {row(f.cash, "1", 1)},
		"an unknown account":      {row(999999, "1", 1)},
		"a duplicate account":     {good, good},
		"eleven months":           {short},
		"decimals in IDR":         {row(f.rooms, "100.5", 1)},
		"a word":                  {row(f.rooms, "abc", 1)},
		"an exponent":             {row(f.rooms, "1e3", 1)},
		"a thousand separator":    {row(f.rooms, "1,000", 1)},
		"too big":                 {row(f.rooms, "99999999999999", 1)},
	} {
		_, err := f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: rows})
		if e, ok := apperr.As(err); !ok || len(e.Fields) == 0 {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
	// An empty amount is zero, a negative one is allowed (a refund), and nothing was kept of the refused grids.
	empty := budget.RowInput{AccountID: f.rooms, Amounts: make([]string, 12)}
	empty.Amounts[0], empty.Amounts[1] = "", "-500"
	saved := f.save(t, b.ID, empty)
	r := rowOf(t, saved, "4110")
	eqs(t, "empty is zero", r.Amounts[0], "0")
	eqs(t, "a negative figure", r.Amounts[1], "-500")
	eqs(t, "the total", r.Total, "-500")
	_, err := f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{row(f.cash, "1", 1)}})
	if err == nil {
		t.Fatal("refused")
	}
	eqs(t, "a refused grid changes nothing", rowOf(t, mustGet(t, f, b.ID), "4110").Total, "-500")
	_, err = f.Budget.Get(f.manager, f.propID, 999999)
	wantCode(t, err, "BUDGET_NOT_FOUND")
}

func TestSpread(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "A")
	b, err := f.Budget.Spread(f.manager, f.propID, b.ID, budget.SpreadInput{AccountID: f.rooms, Total: "1000000", Method: budget.SpreadEqual})
	must(t, err)
	r := rowOf(t, b, "4110")
	sum := decimal.Zero
	for i, a := range r.Amounts {
		sum = sum.Add(decimal.RequireFromString(a))
		if i < 11 {
			eqs(t, fmt.Sprintf("month %d", i+1), a, "83333")
		}
	}
	eqd(t, "the months add up to the total", sum, "1000000")
	eqs(t, "the last month takes the rounding", r.Amounts[11], "83337")

	// Spreading again replaces the row of the account, and leaves the others.
	b = f.save(t, b.ID, row(f.payroll, "10", 1), row(f.rooms, "5", 1))
	b, err = f.Budget.Spread(f.manager, f.propID, b.ID, budget.SpreadInput{AccountID: f.rooms, Total: "1200", Method: budget.SpreadEqual})
	must(t, err)
	eqs(t, "replaced", rowOf(t, b, "4110").Total, "1200")
	eqs(t, "the other account stays", rowOf(t, b, "5110").Total, "10")

	// The pattern of last year: the books start on 30 Sep 2026, so a budget of 2027 follows September 2026.
	f.journal(t, f.payroll, f.cash, "20000", "p1")
	next := f.create(t, "2027-01-01", "Next")
	next, err = f.Budget.Spread(f.manager, f.propID, next.ID, budget.SpreadInput{AccountID: f.payroll, Total: "120000", Method: budget.SpreadLastYear})
	must(t, err)
	r = rowOf(t, next, "5110")
	eqs(t, "all of it in the month of the actuals", r.Amounts[8], "120000")
	eqs(t, "none elsewhere", r.Total, "120000")
	_, err = f.Budget.Spread(f.manager, f.propID, next.ID, budget.SpreadInput{AccountID: f.rooms, Total: "120000", Method: budget.SpreadLastYear})
	if fieldCodes(t, err)["method"] != "NO_PATTERN" {
		t.Fatal("an account with no actuals has no pattern to follow")
	}

	for name, in := range map[string]budget.SpreadInput{
		"unknown method":  {AccountID: f.rooms, Total: "1", Method: "WEEKLY"},
		"bad total":       {AccountID: f.rooms, Total: "x", Method: budget.SpreadEqual},
		"balance account": {AccountID: f.cash, Total: "1", Method: budget.SpreadEqual},
	} {
		_, err := f.Budget.Spread(f.manager, f.propID, next.ID, in)
		if e, ok := apperr.As(err); !ok || len(e.Fields) == 0 {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
	f.save(t, next.ID, row(f.rooms, "1", 1))
	f.activate(t, next.ID)
	_, err = f.Budget.Spread(f.manager, f.propID, next.ID, budget.SpreadInput{AccountID: f.rooms, Total: "1", Method: budget.SpreadEqual})
	wantCode(t, err, "BUDGET_NOT_DRAFT")
}

func TestFillFromActuals(t *testing.T) {
	f := setup(t)
	f.journal(t, f.payroll, f.cash, "20000", "p1")
	f.journal(t, f.cash, f.rooms, "500000", "r1")

	// FY2025 has nothing in the books.
	first := f.create(t, "2026-01-01", "A")
	_, err := f.Budget.FillFromActuals(f.manager, f.propID, first.ID, budget.FillInput{})
	wantCode(t, err, "NO_ACTUALS")

	// FY2026 is the source of the budget of 2027, raised by 10 percent: revenue and expense, September only, on the normal side of each account.
	b := f.create(t, "2027-01-01", "B")
	src := d("2026-01-01")
	b, err = f.Budget.FillFromActuals(f.manager, f.propID, b.ID, budget.FillInput{SourceYearStart: &src, PercentChange: "10"})
	must(t, err)
	eqs(t, "revenue is a positive figure", rowOf(t, b, "4110").Amounts[8], "550000")
	eqs(t, "expense too", rowOf(t, b, "5110").Amounts[8], "22000")
	eqs(t, "only the month of the actuals", rowOf(t, b, "4110").Total, "550000")
	for _, r := range b.Rows {
		if r.Code == "1110" {
			t.Fatal("the balance sheet is not budgeted")
		}
	}

	// Without replace the accounts that have a row keep it; with it the grid is rebuilt.
	b = f.save(t, b.ID, row(f.rooms, "7", 1))
	b, err = f.Budget.FillFromActuals(f.manager, f.propID, b.ID, budget.FillInput{SourceYearStart: &src, PercentChange: "-10"})
	must(t, err)
	eqs(t, "a row that exists is kept", rowOf(t, b, "4110").Total, "7")
	eqs(t, "a new one is added", rowOf(t, b, "5110").Amounts[8], "18000")
	b, err = f.Budget.FillFromActuals(f.manager, f.propID, b.ID, budget.FillInput{SourceYearStart: &src, Replace: true})
	must(t, err)
	eqs(t, "replaced", rowOf(t, b, "4110").Total, "500000")

	for name, in := range map[string]budget.FillInput{
		"a percent below -100": {SourceYearStart: &src, PercentChange: "-101"},
		"a word":               {SourceYearStart: &src, PercentChange: "ten"},
		"three decimals":       {SourceYearStart: &src, PercentChange: "1.234"},
		"not a fiscal year":    {SourceYearStart: ptr(d("2026-02-01"))},
	} {
		_, err := f.Budget.FillFromActuals(f.manager, f.propID, b.ID, in)
		if e, ok := apperr.As(err); !ok || len(e.Fields) == 0 {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
	f.activate(t, b.ID)
	_, err = f.Budget.FillFromActuals(f.manager, f.propID, b.ID, budget.FillInput{SourceYearStart: &src})
	wantCode(t, err, "BUDGET_NOT_DRAFT")
}

func ptr[T any](v T) *T { return &v }

func TestCSVExportAndImport(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "A")
	b = f.save(t, b.ID, row(f.rooms, "1000", 1, 2), row(f.payroll, "50", 1))
	exp, rows, err := f.Budget.ExportCSV(f.viewer, f.propID, b.ID)
	must(t, err)
	if exp.ID != b.ID || len(rows) != 3 || len(rows[0]) != 14 || rows[0][0] != "code" || rows[0][2] != "m1" || rows[0][13] != "m12" {
		t.Fatalf("the export: %v", rows)
	}
	if rows[1][0] != "4110" || rows[1][2] != "1000" || rows[2][0] != "5110" {
		t.Fatalf("the rows are by code: %v", rows)
	}

	file := "code,name,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10,m11,m12\n" +
		"4110,Rooms,10,20,30,,,,,,,,,\n" +
		"6110,Admin,,,,,,,,,5,,,\n"
	res, err := f.Budget.ImportCSV(f.manager, f.propID, b.ID, file, true)
	must(t, err)
	if !res.DryRun || res.Accounts != 2 {
		t.Fatalf("a dry run: %+v", res)
	}
	eqs(t, "a dry run keeps nothing", rowOf(t, mustGet(t, f, b.ID), "4110").Total, "2000")

	res, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, "\uFEFF"+file, false)
	must(t, err)
	if res.DryRun || res.Accounts != 2 {
		t.Fatalf("the import: %+v", res)
	}
	got := mustGet(t, f, b.ID)
	if len(got.Rows) != 2 {
		t.Fatalf("the import replaces the grid: %+v", got.Rows)
	}
	eqs(t, "imported", rowOf(t, got, "4110").Total, "60")
	eqs(t, "imported", rowOf(t, got, "6110").Amounts[8], "5")

	// Every wrong row is listed, and nothing changes.
	head := "code,name,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10,m11,m12\n"
	_, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, head+"4110,Rooms,1,2,3,4,5,6,7,8,9,10,11,12\n5110,Payroll,x,,,,,,,,,,,\n4110,Again,1,,,,,,,,,,,\n", false)
	codes := fieldCodes(t, err)
	if codes["rows[3].m1"] != "INVALID_AMOUNT" || codes["rows[4].code"] != "DUPLICATE" {
		t.Fatalf("the problems of the file: %v", codes)
	}
	_, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, head+"4110,Rooms,1,,,,,,,,,,,\n9999,Nope,1,,,,,,,,,,,\n1110,Cash,1,,,,,,,,,,,\n", false)
	codes = fieldCodes(t, err)
	if codes["rows[3].code"] != "NOT_FOUND" || codes["rows[4].code"] != "NOT_FOUND" || len(codes) != 2 {
		t.Fatalf("accounts the budget cannot cover: %v", codes)
	}
	eqs(t, "a refused file changes nothing", rowOf(t, mustGet(t, f, b.ID), "4110").Total, "60")

	for name, text := range map[string]string{
		"empty":          "",
		"only a header":  "code,name,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10,m11,m12\n",
		"no code column": "name,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10,m11,m12\nx,1,,,,,,,,,,,\n",
		"no month":       "code,name,m1\n4110,Rooms,1\n",
	} {
		if _, err := f.Budget.ImportCSV(f.manager, f.propID, b.ID, text, false); err == nil {
			t.Errorf("%s: refused", name)
		}
	}
	f.activate(t, b.ID)
	_, err = f.Budget.ImportCSV(f.manager, f.propID, b.ID, file, false)
	wantCode(t, err, "BUDGET_NOT_DRAFT")
	if _, _, err := f.Budget.ExportCSV(f.viewer, f.propID, b.ID); err != nil {
		t.Fatalf("an active budget exports: %v", err)
	}
	_, err = f.Budget.ImportCSV(f.viewer, f.propID, b.ID, file, false)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestBudgetAgainstActual(t *testing.T) {
	f := setup(t)
	// The books on 30 Sep 2026: room revenue 500000, rooms payroll 20000, administrative payroll 30000.
	f.journal(t, f.cash, f.rooms, "500000", "r1")
	f.journal(t, f.payroll, f.cash, "20000", "p1")
	f.journal(t, f.admin6110, f.cash, "30000", "p2")

	_, err := f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{})
	wantCode(t, err, "NO_ACTIVE_BUDGET")

	// The plan: revenue 100000 in August and 400000 in September, payroll 25000 and administration 30000 in September.
	b := f.create(t, "2026-01-01", "Plan")
	rooms := row(f.rooms, "100000", 8)
	rooms.Amounts[8] = "400000"
	b = f.save(t, b.ID, rooms, row(f.payroll, "25000", 9), row(f.admin6110, "30000", 9))

	// A draft can be previewed by its id before it is active.
	rep, err := f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{BudgetID: &b.ID})
	must(t, err)
	if rep.Budget.ID != b.ID || rep.Budget.Status != budget.StatusDraft {
		t.Fatalf("the draft: %+v", rep.Budget)
	}
	f.activate(t, b.ID)

	// The default is the current fiscal year up to the current month: September.
	rep, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{})
	must(t, err)
	if !rep.YearStart.Equal(d("2026-01-01")) || !rep.From.Equal(d("2026-01-01")) || !rep.To.Equal(d("2026-09-30")) || rep.YearLabel != "FY2026" {
		t.Fatalf("the default range: %+v", rep)
	}
	sepFrom := d("2026-09-15") // any date of the month: the report covers whole months
	sepTo := d("2026-09-02")
	rep, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{From: &sepFrom, To: &sepTo})
	must(t, err)
	if !rep.From.Equal(d("2026-09-01")) || !rep.To.Equal(d("2026-09-30")) {
		t.Fatalf("snapped to whole months: %v to %v", rep.From, rep.To)
	}

	rev := lineOf(t, rep, "TOTAL_REVENUE")
	eqd(t, "period actual revenue", rev.Period.Actual, "500000")
	eqd(t, "period budget revenue", rev.Period.Budget, "400000")
	eqd(t, "variance", rev.Period.Variance, "100000")
	if rev.Period.VariancePercent == nil || !rev.Period.VariancePercent.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("variance percent: %v", rev.Period.VariancePercent)
	}
	if rev.Period.Favourable == nil || !*rev.Period.Favourable {
		t.Fatal("revenue above the budget is favourable")
	}
	eqd(t, "year to date budget includes August", rev.YTD.Budget, "500000")
	eqd(t, "year to date actual", rev.YTD.Actual, "500000")
	if rev.YTD.Favourable != nil {
		t.Fatal("no variance, nothing favourable")
	}

	// An expense below its budget is favourable, one on budget is neither.
	var payroll, adminPay budget.Account
	for _, l := range rep.Lines {
		for _, a := range l.Accounts {
			switch a.Code {
			case "5110":
				payroll = a
			case "6110":
				adminPay = a
			}
		}
	}
	eqd(t, "expense actual", payroll.Period.Actual, "20000")
	eqd(t, "expense budget", payroll.Period.Budget, "25000")
	eqd(t, "expense variance", payroll.Period.Variance, "-5000")
	if payroll.Period.Favourable == nil || !*payroll.Period.Favourable {
		t.Fatal("an expense below the budget is favourable")
	}
	if adminPay.Period.Favourable != nil || adminPay.Period.Variance.Sign() != 0 {
		t.Fatalf("on budget: %+v", adminPay.Period)
	}

	// The subtotals read like the income statement: department profit, GOP, net income.
	net := lineOf(t, rep, "NET_INCOME")
	eqd(t, "net income actual", net.Period.Actual, "450000")
	eqd(t, "net income budget", net.Period.Budget, "345000")
	eqd(t, "net income variance", net.Period.Variance, "105000")
	if net.Period.Favourable == nil || !*net.Period.Favourable {
		t.Fatal("a profit above the budget is favourable")
	}
	is, err := f.Accounting.IncomeStatement(f.admin, f.propID, &rep.From, &rep.To)
	must(t, err)
	eqd(t, "the actuals are those of the income statement", net.Period.Actual, is.NetIncome.String())
	eqd(t, "departmental profit", lineOf(t, rep, "DEPT_PROFIT").Period.Actual, "480000")
	eqd(t, "gross operating profit", lineOf(t, rep, "GOP").Period.Actual, "450000")

	// August: nothing in the books, a budget of 100000.
	augDay, augEnd := d("2026-08-01"), d("2026-08-31")
	rep, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{From: &augDay, To: &augEnd})
	must(t, err)
	rev = lineOf(t, rep, "TOTAL_REVENUE")
	eqd(t, "August actual", rev.Period.Actual, "0")
	eqd(t, "August budget", rev.Period.Budget, "100000")
	if rev.Period.Favourable == nil || *rev.Period.Favourable {
		t.Fatal("revenue under the budget is unfavourable")
	}
	eqd(t, "the year to date reaches the end of August only", rev.YTD.Budget, "100000")

	// A budget of another year, a range outside the year, and a range the wrong way round.
	other := f.create(t, "2027-01-01", "Next")
	_, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{BudgetID: &other.ID})
	if fieldCodes(t, err)["budget_id"] != "OTHER_YEAR" {
		t.Fatal("the budget is of another year")
	}
	outside := d("2027-02-01")
	_, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{To: &outside})
	if fieldCodes(t, err)["to"] != "OUT_OF_YEAR" {
		t.Fatal("a range inside the year")
	}
	_, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{From: &sepFrom, To: &augDay})
	if fieldCodes(t, err)["to"] != "BEFORE_FROM" {
		t.Fatal("the end is before the start")
	}
	bad := d("2026-02-01")
	_, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{YearStart: &bad})
	if fieldCodes(t, err)["year_start"] != "INVALID_FISCAL_YEAR" {
		t.Fatal("a fiscal year starts in January here")
	}
	_, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{BudgetID: new(int64)})
	wantCode(t, err, "BUDGET_NOT_FOUND")
}

func TestFiscalYearNotStartingInJanuary(t *testing.T) {
	f := setup(t)
	_, err := f.Pool.Exec(context.Background(), `UPDATE accounting_settings SET fiscal_year_start_month = 7 WHERE property_id = $1`, f.propID)
	must(t, err)
	f.journal(t, f.cash, f.rooms, "500000", "r1")

	y := d("2026-01-01")
	_, err = f.Budget.Create(f.manager, f.propID, budget.CreateInput{YearStart: &y, Name: "x"})
	if fieldCodes(t, err)["year_start"] != "INVALID_FISCAL_YEAR" {
		t.Fatal("this fiscal year starts in July")
	}
	b := f.create(t, "2026-07-01", "July to June")
	if b.YearLabel != "FY2027" || !b.YearEnd.Equal(d("2027-06-30")) || b.Months[0].Start.String() != "2026-07-01" || b.Months[11].Start.String() != "2027-06-01" {
		t.Fatalf("the year: %+v", b)
	}
	// September is month 3 of the year.
	b = f.save(t, b.ID, row(f.rooms, "400000", 3))
	f.activate(t, b.ID)
	rep, err := f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{})
	must(t, err)
	if !rep.YearStart.Equal(d("2026-07-01")) || !rep.To.Equal(d("2026-09-30")) {
		t.Fatalf("the report range: %+v", rep)
	}
	rev := lineOf(t, rep, "TOTAL_REVENUE")
	eqd(t, "actual", rev.Period.Actual, "500000")
	eqd(t, "budget", rev.Period.Budget, "400000")
	from := d("2026-09-01")
	rep, err = f.Budget.VsActual(f.viewer, f.propID, budget.VsActualQuery{From: &from, To: &from})
	must(t, err)
	rev = lineOf(t, rep, "TOTAL_REVENUE")
	eqd(t, "September alone: actual", rev.Period.Actual, "500000")
	eqd(t, "September alone: budget", rev.Period.Budget, "400000")
	eqd(t, "year to date budget", rev.YTD.Budget, "400000")

	// A pattern from last year follows the months of the fiscal year, not the calendar.
	next := f.create(t, "2027-07-01", "Next")
	src := d("2026-07-01")
	next, err = f.Budget.FillFromActuals(f.manager, f.propID, next.ID, budget.FillInput{SourceYearStart: &src})
	must(t, err)
	eqs(t, "September is month 3", rowOf(t, next, "4110").Amounts[2], "500000")
}

func TestAudit(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "A")
	f.save(t, b.ID, row(f.rooms, "100", 1))
	f.activate(t, b.ID)
	for _, action := range []string{"budget.created", "budget.grid_saved", "budget.activated"} {
		if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE property_id = $1 AND action = $2 AND entity_type = 'budget' AND entity_id = $3`, f.propID, action, b.ID); n != 1 {
			t.Errorf("%s: %d audit entries, want 1", action, n)
		}
	}
}

func TestConcurrentActivationsTakeTurns(t *testing.T) {
	f := setup(t)
	var ids []int64
	for i := 0; i < 3; i++ {
		b := f.create(t, "2026-01-01", fmt.Sprintf("V%d", i))
		f.save(t, b.ID, row(f.rooms, fmt.Sprint(100*(i+1)), 1))
		ids = append(ids, b.ID)
	}
	var wg sync.WaitGroup
	errs := make([]error, len(ids))
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Budget.Activate(f.manager, f.propID, id, budget.ActivateInput{Approval: f.approval()})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("activation %d: %v", i, err)
		}
	}
	if n := f.Count(t, `SELECT count(*) FROM budgets WHERE property_id = $1 AND status = 'ACTIVE'`, f.propID); n != 1 {
		t.Fatalf("one version is active: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM budgets WHERE property_id = $1 AND status = 'ARCHIVED'`, f.propID); n != 2 {
		t.Fatalf("the others are archived: %d", n)
	}
}

func TestConcurrentDraftsGetDistinctVersions(t *testing.T) {
	f := setup(t)
	y := d("2026-01-01")
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Budget.Create(f.manager, f.propID, budget.CreateInput{YearStart: &y, Name: fmt.Sprintf("V%d", i)})
		}()
	}
	wg.Wait()
	made := 0
	for _, err := range errs {
		if err == nil {
			made++
			continue
		}
		wantCode(t, err, "BUDGET_VERSION_TAKEN") // two creations that choose the same number: one is told to try again
	}
	if made == 0 {
		t.Fatal("at least one draft is made")
	}
	if got := f.Count(t, `SELECT count(DISTINCT version) FROM budgets WHERE property_id = $1`, f.propID); got != made {
		t.Fatalf("%d drafts have %d distinct versions", made, got)
	}
}

func TestConcurrentEditAndActivation(t *testing.T) {
	f := setup(t)
	b := f.create(t, "2026-01-01", "A")
	f.save(t, b.ID, row(f.rooms, "100", 1))
	var wg sync.WaitGroup
	var saveErr, actErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, saveErr = f.Budget.SaveGrid(f.manager, f.propID, b.ID, budget.GridInput{Rows: []budget.RowInput{row(f.rooms, "200", 1)}})
	}()
	go func() {
		defer wg.Done()
		_, actErr = f.Budget.Activate(f.manager, f.propID, b.ID, budget.ActivateInput{Approval: f.approval()})
	}()
	wg.Wait()
	if actErr != nil {
		t.Fatalf("the activation: %v", actErr)
	}
	if saveErr != nil {
		wantCode(t, saveErr, "BUDGET_NOT_DRAFT") // the edit came after the activation
	}
	got := mustGet(t, f, b.ID)
	want := "100"
	if saveErr == nil {
		want = "200" // the edit came first and is what was activated
	}
	eqs(t, "what was activated is what the edit left", rowOf(t, got, "4110").Total, want)
}
