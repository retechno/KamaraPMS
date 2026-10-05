package payables_test

import (
	"context"
	"testing"

	"kamarapms/internal/departments"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
)

func (f *fx) dept(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

// linesOfJournal is the department code ("" for none) of each line of a journal, by account code.
func (f *fx) departmentOfLines(t *testing.T, journalID int64) map[string]string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT a.code, COALESCE(d.code, '') FROM gl_journal_lines l
		JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
		LEFT JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id
		WHERE l.property_id = $1 AND l.journal_id = $2`, f.propID, journalID)
	must(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var a, d string
		must(t, rows.Scan(&a, &d))
		out[a] = d
	}
	must(t, rows.Err())
	return out
}

func TestABillLineNamesItsDepartmentAndTheJournalCarriesIt(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 14)
	fb := f.dept(t, "FB")
	rest, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "REST", Name: "Restaurant", ParentID: &fb})
	must(t, err)
	ag := f.dept(t, "AG")
	b, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
		SupplierID: sup.ID, SupplierInvoiceNumber: "INV-D1", BillDate: d("2026-09-30"),
		Lines: []payables.BillLineInput{
			{AccountID: f.acc["5210"], Description: "Meat", Amount: dec("800000"), DepartmentID: &rest.ID}, // cost of food, a sub-department
			{AccountID: f.acc["6510"], Description: "Electricity", Amount: dec("200000"), DepartmentID: &ag},
			{AccountID: f.acc["6160"], Description: "Paper", Amount: dec("50000")}, // none
		},
	}, "dk1")
	must(t, err)
	if len(b.Lines) != 3 || b.Lines[0].DepartmentID == nil || *b.Lines[0].DepartmentID != rest.ID || b.Lines[0].DepartmentCode != "REST" || b.Lines[2].DepartmentID != nil {
		t.Fatalf("the lines keep their department: %+v", b.Lines)
	}
	got := f.departmentOfLines(t, b.JournalID)
	if got["5210"] != "REST" || got["6510"] != "AG" || got["6160"] != "" || got["2110"] != "" {
		t.Fatalf("the journal lines: %v (the payable has none)", got)
	}
	// the bill read back later shows the department of the line
	again, err := f.Payables.GetBill(f.admin, f.propID, b.ID)
	must(t, err)
	if again.Lines[1].DepartmentCode != "AG" {
		t.Fatalf("read back: %+v", again.Lines[1])
	}

	// voiding the bill reverses the journal with the same departments
	voided, err := f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "duplicate", Approval: f.approval()})
	must(t, err)
	var voidJournal int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT void_journal_id FROM supplier_bills WHERE id = $1`, voided.ID).Scan(&voidJournal))
	if rev := f.departmentOfLines(t, voidJournal); rev["5210"] != "REST" || rev["6510"] != "AG" {
		t.Fatalf("the reversal copies the departments: %v", rev)
	}
	var net string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id
		WHERE l.property_id = $1 AND d.code = 'REST'`, f.propID).Scan(&net))
	if !dec(net).IsZero() {
		t.Fatalf("the department nets to nothing after the void: %s", net)
	}
}

func TestADepartmentOfABillLineMustTakePostings(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 14)
	bill := func(dept int64, key string) error {
		_, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
			SupplierID: sup.ID, SupplierInvoiceNumber: "INV-" + key, BillDate: d("2026-09-30"),
			Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Amount: dec("1000"), DepartmentID: &dept}},
		}, key)
		return err
	}
	if code := fieldCode(t, bill(999999, "a"), "lines[0].department_id"); code != "DEPARTMENT_NOT_FOUND" {
		t.Fatalf("an unknown department: %s", code)
	}
	ag := f.dept(t, "AG")
	_, err := f.Departments.Update(f.admin, f.propID, ag, departments.Patch{Active: ptr(false)})
	must(t, err)
	if code := fieldCode(t, bill(ag, "b"), "lines[0].department_id"); code != "INACTIVE" {
		t.Fatalf("a switched off department: %s", code)
	}
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM supplier_bills WHERE property_id = $1`, f.propID).Scan(&n))
	if n != 0 {
		t.Fatalf("a refused bill leaves nothing: %d", n)
	}
}

func fieldCode(t *testing.T, err error, field string) string {
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
