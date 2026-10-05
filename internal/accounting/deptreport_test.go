package accounting_test

import (
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/departments"
	"kamarapms/internal/platform/auth"
)

// departmentBooks: on 30 Sep 2026 a sale of 1,000,000 split over Rooms, the restaurant and the bar (the last two sub-departments of Food and beverage), and
// expenses paid in cash: 120,000 of food cost to the restaurant, 80,000 of rooms payroll to Rooms, 50,000 of office payroll to Administrative and general,
// and 10,000 of paper to no department.
func departmentBooks(t *testing.T) (*hotel, map[string]int64) {
	h := setupHotel(t)
	ids := map[string]int64{"ROOMS": h.deptID(t, "ROOMS"), "FB": h.deptID(t, "FB"), "AG": h.deptID(t, "AG")}
	for code, name := range map[string]string{"REST": "Restaurant", "BAR": "Bar"} {
		d, err := h.Departments.Create(h.admin, h.propID, departments.Input{Code: code, Name: name, ParentID: ptr(ids["FB"])})
		must(t, err)
		ids[code] = d.ID
	}
	line := func(code string, debit, credit string, dept string) accounting.LineInput {
		in := accounting.LineInput{AccountID: h.byCode(t, code).ID, Debit: dec(debit), Credit: dec(credit)}
		if dept != "" {
			in.DepartmentID = ptr(ids[dept])
		}
		return in
	}
	post := func(key string, lines ...accounting.LineInput) {
		_, err := h.Accounting.PostManual(h.admin, h.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: key, Lines: lines}, key)
		must(t, err)
	}
	post("sale", line("1110", "1000000", "0", ""), line("4110", "0", "600000", "ROOMS"), line("4210", "0", "300000", "REST"), line("4230", "0", "100000", "BAR"))
	post("costs", line("5210", "120000", "0", "REST"), line("5110", "80000", "0", "ROOMS"), line("6110", "50000", "0", "AG"), line("6160", "10000", "0", ""), line("1110", "0", "260000", ""))
	return h, ids
}

func nodeOf(t *testing.T, rep accounting.DepartmentReport, code string) accounting.DepartmentNode {
	t.Helper()
	for _, n := range rep.Departments {
		if n.Code == code {
			return n
		}
		for _, c := range n.Children {
			if c.Code == code {
				return c
			}
		}
	}
	t.Fatalf("no department %s in the report", code)
	return accounting.DepartmentNode{}
}

func TestTheDepartmentReportAddsUpRevenueAndExpensesByDepartment(t *testing.T) {
	h, ids := departmentBooks(t)
	from, to := d("2026-09-01"), d("2026-09-30")
	rep, err := h.Accounting.DepartmentReport(h.admin, h.propID, &from, &to, nil)
	must(t, err)

	rooms := nodeOf(t, rep, "ROOMS")
	eq(t, "rooms revenue", rooms.Revenue, "600000")
	eq(t, "rooms expense", rooms.Expense, "80000")
	eq(t, "rooms profit", rooms.Profit, "520000")
	if rooms.Level != 1 || len(rooms.Children) != 0 || len(rooms.Accounts) != 2 {
		t.Fatalf("rooms: %+v", rooms)
	}
	// a department adds up its sub-departments
	fb := nodeOf(t, rep, "FB")
	eq(t, "FB revenue", fb.Revenue, "400000")
	eq(t, "FB expense", fb.Expense, "120000")
	eq(t, "FB profit", fb.Profit, "280000")
	eq(t, "FB own revenue", fb.OwnRevenue, "0")
	if len(fb.Children) != 2 || fb.Children[0].Code != "BAR" || fb.Children[1].Code != "REST" {
		t.Fatalf("FB sub-departments in order of their code: %+v", fb.Children)
	}
	rest := nodeOf(t, rep, "REST")
	eq(t, "restaurant revenue", rest.Revenue, "300000")
	eq(t, "restaurant expense", rest.Expense, "120000")
	eq(t, "restaurant profit", rest.Profit, "180000")
	if rest.Level != 2 || rest.ParentID == nil || *rest.ParentID != ids["FB"] || len(rest.Accounts) != 2 || rest.Accounts[0].Code != "4210" || rest.Accounts[1].Code != "5210" {
		t.Fatalf("restaurant: %+v", rest)
	}
	eq(t, "restaurant sales on the account", rest.Accounts[0].Amount, "300000")
	eq(t, "restaurant cost on the account", rest.Accounts[1].Amount, "120000")
	eq(t, "bar profit", nodeOf(t, rep, "BAR").Profit, "100000")
	ag := nodeOf(t, rep, "AG")
	eq(t, "A and G expense", ag.Expense, "50000")
	eq(t, "A and G profit", ag.Profit, "-50000")

	// what has no department is shown apart, and the totals are the income statement's
	eq(t, "unassigned expense", rep.Unassigned.Expense, "10000")
	if len(rep.Unassigned.Accounts) != 1 || rep.Unassigned.Accounts[0].Code != "6160" {
		t.Fatalf("unassigned: %+v", rep.Unassigned)
	}
	eq(t, "total revenue", rep.Totals.Revenue, "1000000")
	eq(t, "total expense", rep.Totals.Expense, "260000")
	eq(t, "total profit", rep.Totals.Profit, "740000")
	is, err := h.Accounting.IncomeStatement(h.admin, h.propID, &from, &to)
	must(t, err)
	eq(t, "the profit is the net income of the income statement", rep.Totals.Profit, is.NetIncome.String())

	// the departments of the property that were used or are in use are listed, the others are not
	var codes []string
	for _, n := range rep.Departments {
		codes = append(codes, n.Code)
	}
	if len(codes) != 8 {
		t.Fatalf("every department in use: %v", codes)
	}
}

func TestADepartmentReportNarrowedToOneDepartmentOrSubDepartment(t *testing.T) {
	h, ids := departmentBooks(t)
	from, to := d("2026-09-01"), d("2026-09-30")
	fb, err := h.Accounting.DepartmentReport(h.admin, h.propID, &from, &to, ptr(ids["FB"]))
	must(t, err)
	if len(fb.Departments) != 1 || fb.Departments[0].Code != "FB" || len(fb.Departments[0].Children) != 2 {
		t.Fatalf("one department: %+v", fb.Departments)
	}
	eq(t, "its totals are its own", fb.Totals.Revenue, "400000")
	eq(t, "no expense outside it", fb.Totals.Expense, "120000")
	if len(fb.Unassigned.Accounts) != 0 {
		t.Fatal("no unassigned line when a department was asked for")
	}
	rest, err := h.Accounting.DepartmentReport(h.admin, h.propID, &from, &to, ptr(ids["REST"]))
	must(t, err)
	if len(rest.Departments) != 1 || rest.Departments[0].Code != "REST" || rest.Departments[0].Level != 2 {
		t.Fatalf("one sub-department: %+v", rest.Departments)
	}
	eq(t, "sub-department profit", rest.Totals.Profit, "180000")
	_, err = h.Accounting.DepartmentReport(h.admin, h.propID, &from, &to, ptr(int64(999999)))
	wantCode(t, err, "DEPARTMENT_NOT_FOUND")
}

func TestTheDepartmentReportFollowsReversalsAndTheRange(t *testing.T) {
	h, ids := departmentBooks(t)
	// the restaurant's sale is reversed: the reversal carries the department, so it nets out
	list := h.journals(t, accounting.JournalFilter{Type: "MANUAL"})
	var saleID int64
	for _, j := range list {
		if j.Description == "sale" {
			saleID = j.ID
		}
	}
	_, err := h.Accounting.Reverse(h.admin, h.propID, saleID, accounting.ReverseInput{Reason: "mistake", Approval: h.approval()})
	must(t, err)
	from, to := d("2026-09-01"), d("2026-09-30")
	rep, err := h.Accounting.DepartmentReport(h.admin, h.propID, &from, &to, nil)
	must(t, err)
	eq(t, "the sale is gone from the restaurant", nodeOf(t, rep, "REST").Revenue, "0")
	eq(t, "its cost stays", nodeOf(t, rep, "REST").Expense, "120000")
	eq(t, "the total revenue", rep.Totals.Revenue, "0")
	// a range with nothing in it
	later, laterEnd := d("2026-10-01"), d("2026-10-31")
	quiet, err := h.Accounting.DepartmentReport(h.admin, h.propID, &later, &laterEnd, nil)
	must(t, err)
	eq(t, "nothing in October", quiet.Totals.Profit, "0")
	if len(quiet.Unassigned.Accounts) != 0 {
		t.Fatal("nothing unassigned")
	}
	// a department switched off with nothing posted is not listed
	spa, err := h.Departments.Create(h.admin, h.propID, departments.Input{Code: "SPA", Name: "Spa"})
	must(t, err)
	_, err = h.Departments.Update(h.admin, h.propID, spa.ID, departments.Patch{Active: ptr(false)})
	must(t, err)
	hidden, err := h.Accounting.DepartmentReport(h.admin, h.propID, &from, &to, nil)
	must(t, err)
	for _, n := range hidden.Departments {
		if n.Code == "SPA" {
			t.Fatal("a switched off department with nothing posted is not listed")
		}
	}
	_ = ids
	nobody := h.User(t, h.tenantID, h.propID, auth.PermBankView)
	_, err = h.Accounting.DepartmentReport(nobody, h.propID, nil, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	rangeFrom, rangeTo := d("2026-10-05"), d("2026-10-01")
	_, err = h.Accounting.DepartmentReport(h.admin, h.propID, &rangeFrom, &rangeTo, nil)
	wantCode(t, err, "VALIDATION_FAILED")
}
