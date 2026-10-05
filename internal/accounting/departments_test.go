package accounting_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/departments"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
)

// deptID is the id of a department of the hotel by its code.
func (h *hotel) deptID(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = $2`, h.propID, code).Scan(&id))
	return id
}

// revenueByDepartment is the credit less debit of the revenue accounts of the journals, by department code ("" when a line has none).
func (h *hotel) revenueByDepartment(t *testing.T, journalType string) map[string]decimal.Decimal {
	t.Helper()
	rows, err := h.Pool.Query(context.Background(), `SELECT COALESCE(d.code, ''), sum(l.credit - l.debit)::text
		FROM gl_journal_lines l
		JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
		JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
		LEFT JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id
		WHERE l.property_id = $1 AND j.journal_type = $2 AND a.account_type = 'REVENUE' GROUP BY 1`, h.propID, journalType)
	must(t, err)
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var code, amount string
		must(t, rows.Scan(&code, &amount))
		out[code] = dec(amount)
	}
	must(t, rows.Err())
	return out
}

func fieldOf(t *testing.T, err error, field string) string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("got %v, want an *apperr.Error", err)
	}
	for _, f := range e.Fields {
		if f.Field == field {
			return f.Code
		}
	}
	t.Fatalf("no field error on %s: %+v", field, e.Fields)
	return ""
}

func TestTheDayCloseCarriesTheDepartmentOfEachChargeCode(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-02")
	st := h.checkIn(t, res, h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	h.charge(t, st.Folio.ID, "MINIBAR", "50000", "m1")
	h.charge(t, st.Folio.ID, "LAUNDRY", "30000", "l1")
	h.charge(t, st.Folio.ID, "OTHER", "10000", "o1") // no default department
	h.closeDay(t)

	got := h.revenueByDepartment(t, "DAY_CLOSE")
	if !got["FB"].Equal(dec("150000")) {
		t.Errorf("food and beverage (restaurant and minibar): %s", got["FB"])
	}
	if !got["OOD"].Equal(dec("30000")) {
		t.Errorf("other operated departments (laundry): %s", got["OOD"])
	}
	if !got["ROOMS"].IsPositive() {
		t.Errorf("the night audit posted the room charge to Rooms: %s", got["ROOMS"])
	}
	if !got[""].Equal(dec("10000")) {
		t.Errorf("a charge code with no department is unassigned: %s", got[""])
	}
	// the control lines have no department, and the journal still balances
	var n int
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journal_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
		WHERE l.property_id = $1 AND a.account_type IN ('ASSET', 'LIABILITY') AND l.department_id IS NOT NULL`, h.propID).Scan(&n))
	if n != 0 {
		t.Fatalf("balance sheet lines of the day close have no department: %d", n)
	}
	h.requireBalanced(t)
	// one line per account and department: the restaurant and the minibar have their own accounts here, so two revenue lines of FB
	var lines int
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journal_lines l JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id WHERE l.property_id = $1 AND d.code = 'FB'`, h.propID).Scan(&lines))
	if lines != 2 {
		t.Fatalf("revenue lines of FB: %d", lines)
	}
}

func TestADepartmentIsSnapshottedAndAChangeOfTheMasterDoesNotMoveHistory(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-03")
	st := h.checkIn(t, res, h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	fb, ood := h.deptID(t, "FB"), h.deptID(t, "OOD")

	// the default of the charge code changes: the item already posted keeps its department, the next one takes the new one
	must(t, h.Exec(t, `UPDATE charge_codes SET department_id = $2 WHERE property_id = $1 AND code = 'RESTAURANT'`, h.propID, ood))
	h.charge(t, st.Folio.ID, "RESTAURANT", "40000", "r2")
	var first, second int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT department_id FROM folio_items WHERE property_id = $1 AND idempotency_key = 'r1'`, h.propID).Scan(&first))
	must(t, h.Pool.QueryRow(context.Background(), `SELECT department_id FROM folio_items WHERE property_id = $1 AND idempotency_key = 'r2'`, h.propID).Scan(&second))
	if first != fb || second != ood {
		t.Fatalf("snapshot: first %d (want %d), second %d (want %d)", first, fb, second, ood)
	}

	// a reversal copies the department of the item it reverses, not the default of today
	var itemID int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE property_id = $1 AND idempotency_key = 'r1'`, h.propID).Scan(&itemID))
	rev, err := h.Folios.Reverse(h.admin, h.propID, itemID, folios.CorrectionInput{Reason: "wrong table", Approval: h.approval()})
	must(t, err)
	var revDept int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT department_id FROM folio_items WHERE property_id = $1 AND id = $2`, h.propID, rev.Item.ID).Scan(&revDept))
	if revDept != fb {
		t.Fatalf("a reversal copies the department of the item: %d, want %d", revDept, fb)
	}
	h.closeDay(t)
	got := h.revenueByDepartment(t, "DAY_CLOSE")
	if !got["FB"].IsZero() {
		t.Errorf("the reversed charge nets to nothing in FB: %s", got["FB"])
	}
	if !got["OOD"].Equal(dec("40000")) {
		t.Errorf("the second charge is in OOD: %s", got["OOD"])
	}

	// renaming a department changes its label and nothing that was posted
	_, err = h.Departments.Update(h.admin, h.propID, fb, departments.Patch{Name: ptr("F and B")})
	must(t, err)
	if again := h.revenueByDepartment(t, "DAY_CLOSE"); !again["OOD"].Equal(dec("40000")) {
		t.Fatalf("history after a rename: %v", again)
	}
}

func TestAManualJournalLineNamesItsDepartment(t *testing.T) {
	h := setupHotel(t)
	rooms, fb := h.deptID(t, "ROOMS"), h.deptID(t, "FB")
	in := accounting.ManualInput{Date: d("2026-09-30"), Description: "adjustment", Lines: []accounting.LineInput{
		{AccountID: h.cashAccount, Debit: dec("500")},
		{AccountID: h.revenueAcct, Credit: dec("300"), DepartmentID: &rooms},
		{AccountID: h.byCode(t, "4210").ID, Credit: dec("200"), DepartmentID: &fb},
	}}
	j, err := h.Accounting.PostManual(h.admin, h.propID, in, "dept-1")
	must(t, err)
	byAccount := map[string]accounting.JournalLine{}
	for _, l := range j.Lines {
		byAccount[l.AccountCode] = l
	}
	if l := byAccount["1110"]; l.DepartmentID != nil {
		t.Fatalf("the cash line has none: %+v", l)
	}
	if l := byAccount["4110"]; l.DepartmentID == nil || *l.DepartmentID != rooms || l.DepartmentCode != "ROOMS" || l.DepartmentName != "Rooms" {
		t.Fatalf("the rooms revenue line: %+v", l)
	}
	if l := byAccount["4210"]; l.DepartmentCode != "FB" {
		t.Fatalf("the food revenue line: %+v", l)
	}

	// its reversal copies the departments
	rev, err := h.Accounting.Reverse(h.admin, h.propID, j.ID, accounting.ReverseInput{Reason: "mistake", Approval: h.approval()})
	must(t, err)
	for _, l := range rev.Lines {
		if l.AccountCode == "4110" && (l.DepartmentID == nil || *l.DepartmentID != rooms) {
			t.Fatalf("a reversal copies the department: %+v", l)
		}
	}
	if got := h.revenueByDepartment(t, "MANUAL"); !got["ROOMS"].Equal(dec("300")) {
		t.Fatalf("the manual journal and its reversal are of different types: %v", got)
	}
	if got := h.revenueByDepartment(t, "REVERSAL"); !got["ROOMS"].Equal(dec("-300")) || !got["FB"].Equal(dec("-200")) {
		t.Fatalf("the reversal: %v", got)
	}

	// a sub-department is a department too
	sub, err := h.Departments.Create(h.admin, h.propID, departments.Input{Code: "REST", Name: "Restaurant", ParentID: &fb})
	must(t, err)
	in.Lines[2].DepartmentID = &sub.ID
	if _, err := h.Accounting.PostManual(h.admin, h.propID, in, "dept-2"); err != nil {
		t.Fatalf("a line of a sub-department: %v", err)
	}
}

func TestADepartmentOfAJournalLineMustBeOneOfTheProperty(t *testing.T) {
	h := setupHotel(t)
	ghost, rooms := int64(999999), h.deptID(t, "ROOMS")
	line := func(dept *int64) accounting.ManualInput {
		return accounting.ManualInput{Date: d("2026-09-30"), Description: "x", Lines: []accounting.LineInput{
			{AccountID: h.cashAccount, Debit: dec("100")}, {AccountID: h.revenueAcct, Credit: dec("100"), DepartmentID: dept},
		}}
	}
	_, err := h.Accounting.PostManual(h.admin, h.propID, line(&ghost), "k1")
	if fieldOf(t, err, "lines[1].department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("an unknown department")
	}
	// the department of another property
	other := h.Tenant(t, "XYZ")
	op := h.Property(t, other.ID, "SG")
	var foreign int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = 'ROOMS'`, op.ID).Scan(&foreign))
	_, err = h.Accounting.PostManual(h.admin, h.propID, line(&foreign), "k2")
	if fieldOf(t, err, "lines[1].department_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("the department of another property")
	}
	// one that is switched off
	_, err = h.Departments.Update(h.admin, h.propID, rooms, departments.Patch{Active: ptr(false)})
	must(t, err)
	_, err = h.Accounting.PostManual(h.admin, h.propID, line(&rooms), "k3")
	if fieldOf(t, err, "lines[1].department_id") != "INACTIVE" {
		t.Fatal("a switched off department takes no new line")
	}
	// but what was posted to it stays, and the line with none is fine
	if _, err := h.Accounting.PostManual(h.admin, h.propID, line(nil), "k4"); err != nil {
		t.Fatalf("no department: %v", err)
	}
	var n int
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journals WHERE property_id = $1`, h.propID).Scan(&n))
	if n != 1 {
		t.Fatalf("the refused journals left nothing: %d", n)
	}
}
