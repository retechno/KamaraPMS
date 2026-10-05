package accounting_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/accounting"
	"kamarapms/internal/departments"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
)

// rule sets the department rule of an account of the hotel.
func (h *hotel) rule(t *testing.T, code, requirement string, def *int64) accounting.Account {
	t.Helper()
	a := h.byCode(t, code)
	patch := accounting.AccountPatch{DepartmentRequirement: &requirement}
	if def != nil {
		patch.DefaultDepartmentID = def
	}
	got, err := h.Accounting.UpdateAccount(h.admin, h.propID, a.ID, patch)
	must(t, err)
	return got
}

// deptOfLine is the department code of the newest journal line of an account ("" when none).
func (h *hotel) deptOfLine(t *testing.T, code string) string {
	t.Helper()
	var got string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT COALESCE(d.code, '') FROM gl_journal_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
		LEFT JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id
		WHERE l.property_id = $1 AND a.code = $2 ORDER BY l.id DESC LIMIT 1`, h.propID, code).Scan(&got))
	return got
}

func manual(h *hotel, revenue int64, dept *int64) accounting.ManualInput {
	return accounting.ManualInput{Date: d("2026-09-30"), Description: "rule", Lines: []accounting.LineInput{
		{AccountID: h.cashAccount, Debit: dec("500")},
		{AccountID: revenue, Credit: dec("500"), DepartmentID: dept},
	}}
}

func TestTheDepartmentRuleOfAnAccountDecidesWhatAManualLineMayDo(t *testing.T) {
	h := setupHotel(t)
	rooms, fb := h.deptID(t, "ROOMS"), h.deptID(t, "FB")
	rev := h.byCode(t, "4110")
	if rev.DepartmentRequirement != "OPTIONAL" || rev.DefaultDepartmentID != nil {
		t.Fatalf("an account is optional until a property says otherwise: %+v", rev)
	}

	// REQUIRED: a line without a department is refused, never posted with none
	h.rule(t, "4110", "REQUIRED", nil)
	_, err := h.Accounting.PostManual(h.admin, h.propID, manual(h, rev.ID, nil), "r1")
	wantCode(t, err, "VALIDATION_FAILED")
	if c := fieldOf(t, err, "lines[1].department_id"); c != "DEPARTMENT_REQUIRED" {
		t.Fatalf("code %s", c)
	}
	if e, _ := apperr.As(err); e.Fields[0].Message != "account 4110 - "+rev.Name+" requires a department" {
		t.Fatalf("the message names the account: %q", e.Fields[0].Message)
	}
	if n := len(h.journals(t, accounting.JournalFilter{Type: "MANUAL"})); n != 0 {
		t.Fatalf("nothing was posted: %d", n)
	}
	// a department named is used
	_, err = h.Accounting.PostManual(h.admin, h.propID, manual(h, rev.ID, &fb), "r2")
	must(t, err)
	if got := h.deptOfLine(t, "4110"); got != "FB" {
		t.Fatalf("the department named: %q", got)
	}

	// the default of the account fills what is left empty; a department named wins over it
	a := h.rule(t, "4110", "REQUIRED", &rooms)
	if a.DefaultDepartmentCode != "ROOMS" {
		t.Fatalf("default: %+v", a)
	}
	_, err = h.Accounting.PostManual(h.admin, h.propID, manual(h, rev.ID, nil), "r3")
	must(t, err)
	if got := h.deptOfLine(t, "4110"); got != "ROOMS" {
		t.Fatalf("the default: %q", got)
	}
	_, err = h.Accounting.PostManual(h.admin, h.propID, manual(h, rev.ID, &fb), "r4")
	must(t, err)
	if got := h.deptOfLine(t, "4110"); got != "FB" {
		t.Fatalf("a department named wins: %q", got)
	}

	// changing the default does not move what was posted
	h.rule(t, "4110", "REQUIRED", &fb)
	var inRooms int
	must(t, h.Pool.QueryRow(context.Background(), `SELECT count(*) FROM gl_journal_lines l JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id WHERE l.property_id = $1 AND d.code = 'ROOMS'`, h.propID).Scan(&inRooms))
	if inRooms != 1 {
		t.Fatalf("the line posted with the old default stays: %d", inRooms)
	}

	// NONE: a department is refused, and the line has none
	h.rule(t, "4110", "NONE", nil)
	_, err = h.Accounting.PostManual(h.admin, h.propID, manual(h, rev.ID, &fb), "n1")
	if c := fieldOf(t, err, "lines[1].department_id"); c != "DEPARTMENT_NOT_ALLOWED" {
		t.Fatalf("code %s", c)
	}
	_, err = h.Accounting.PostManual(h.admin, h.propID, manual(h, rev.ID, nil), "n2")
	must(t, err)
	if got := h.deptOfLine(t, "4110"); got != "" {
		t.Fatalf("a line of an account that takes none: %q", got)
	}
}

func TestADepartmentRuleIsValidatedWhenItIsSet(t *testing.T) {
	h := setupHotel(t)
	fb := h.deptID(t, "FB")
	a := h.byCode(t, "4110")
	bad := "SOMETIMES"
	_, err := h.Accounting.UpdateAccount(h.admin, h.propID, a.ID, accounting.AccountPatch{DepartmentRequirement: &bad})
	if c := fieldOf(t, err, "department_requirement"); c != "INVALID_VALUE" {
		t.Fatalf("code %s", c)
	}
	none := "NONE"
	_, err = h.Accounting.UpdateAccount(h.admin, h.propID, a.ID, accounting.AccountPatch{DepartmentRequirement: &none, DefaultDepartmentID: &fb})
	if c := fieldOf(t, err, "default_department_id"); c != "DEPARTMENT_NOT_ALLOWED" {
		t.Fatalf("code %s", c)
	}
	missing := int64(999999)
	opt := "OPTIONAL"
	_, err = h.Accounting.UpdateAccount(h.admin, h.propID, a.ID, accounting.AccountPatch{DepartmentRequirement: &opt, DefaultDepartmentID: &missing})
	if c := fieldOf(t, err, "default_department_id"); c != "DEPARTMENT_NOT_FOUND" {
		t.Fatalf("code %s", c)
	}
	// switching to NONE clears the default
	got := h.rule(t, "4110", "OPTIONAL", &fb)
	if got.DefaultDepartmentID == nil {
		t.Fatalf("default: %+v", got)
	}
	got = h.rule(t, "4110", "NONE", nil)
	if got.DefaultDepartmentID != nil || got.DepartmentRequirement != "NONE" {
		t.Fatalf("NONE has no default: %+v", got)
	}
	// a new account takes the rule too
	created, err := h.Accounting.CreateAccount(h.admin, h.propID, accounting.AccountInput{Code: "6999", Name: "Test expense", AccountType: "EXPENSE", StatementGroup: "UND_AG", DepartmentRequirement: "REQUIRED", DefaultDepartmentID: &fb})
	must(t, err)
	if created.DepartmentRequirement != "REQUIRED" || created.DefaultDepartmentCode != "FB" {
		t.Fatalf("created: %+v", created)
	}
	// a department that is the default of an account cannot be switched off or deleted
	off := false
	_, err = h.Departments.Update(h.admin, h.propID, fb, departments.Patch{Active: &off})
	wantCode(t, err, "DEPARTMENT_IN_USE")
	wantCode(t, h.Departments.Delete(h.admin, h.propID, fb), "DEPARTMENT_IN_USE")
}

func TestAChargeThatWouldLeaveARequiredDepartmentEmptyIsRefusedBeforeTheNightAudit(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-02")
	st := h.checkIn(t, res, h.r101)
	var otherID int64
	var otherAccount string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id, gl_account_code FROM charge_codes WHERE property_id = $1 AND code = 'OTHER'`, h.propID).Scan(&otherID, &otherAccount))
	unit := "10000"
	post := func(key string) error {
		_, err := h.Folios.PostCharge(h.admin, h.propID, st.Folio.ID, key, folios.ChargeInput{ChargeCodeID: otherID, Quantity: "1", UnitPrice: &unit})
		return err
	}
	// the charge code has no department and neither has the account
	e, ok := apperr.As(h.ruleErr(t, otherAccount, "REQUIRED", nil))
	if !ok || e.Code != "DEPARTMENT_SETUP_INCOMPLETE" {
		t.Fatalf("the account cannot become required while a charge code has no department: %+v", e)
	}
	// the account gets a default department first
	ood := h.deptID(t, "OOD")
	h.rule(t, otherAccount, "REQUIRED", &ood)
	must(t, post("o1"))
	var dept int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT department_id FROM folio_items WHERE property_id = $1 AND charge_code_id = $2`, h.propID, otherID).Scan(&dept))
	if dept != ood {
		t.Fatalf("the charge took the default of the account: %d", dept)
	}
	h.closeDay(t)
	if got := h.deptOfLine(t, otherAccount); got != "OOD" {
		t.Fatalf("the day close line: %q", got)
	}
	// a charge code cannot lose its way to a department: the default goes away only with the requirement
	rep, err := h.Accounting.DepartmentSetup(h.admin, h.propID)
	must(t, err)
	if !rep.OK {
		t.Fatalf("the setup is complete: %+v", rep.Issues)
	}
}

// ruleErr tries to set a rule and returns the error.
func (h *hotel) ruleErr(t *testing.T, code, requirement string, def *int64) error {
	t.Helper()
	a := h.byCode(t, code)
	patch := accounting.AccountPatch{DepartmentRequirement: &requirement, DefaultDepartmentID: def}
	_, err := h.Accounting.UpdateAccount(h.admin, h.propID, a.ID, patch)
	return err
}

func TestTheDepartmentSetupListsWhatARequiredAccountCannotFindADepartmentFor(t *testing.T) {
	h := setupHotel(t)
	rep, err := h.Accounting.DepartmentSetup(h.admin, h.propID)
	must(t, err)
	if !rep.OK || len(rep.Issues) != 0 {
		t.Fatalf("nothing is required yet: %+v", rep)
	}
	// the system accounts (cash, the guest ledger, the tax payable) have no source of a department: they need the default
	e, ok := apperr.As(h.ruleErr(t, "1110", "REQUIRED", nil))
	if !ok || e.Code != "DEPARTMENT_SETUP_INCOMPLETE" {
		t.Fatalf("cash is a system account: %+v", e)
	}
	fo := h.deptID(t, "ROOMS")
	h.rule(t, "1110", "REQUIRED", &fo)
	rep, err = h.Accounting.DepartmentSetup(h.admin, h.propID)
	must(t, err)
	if !rep.OK {
		t.Fatalf("with a default department it is complete: %+v", rep.Issues)
	}
	// an account that takes none is told about a charge code that names one
	rooms := h.byCode(t, "4110")
	h.rule(t, "4110", "NONE", nil)
	rep, err = h.Accounting.DepartmentSetup(h.admin, h.propID)
	must(t, err)
	var warned bool
	for _, i := range rep.Issues {
		warned = warned || (i.Severity == accounting.SetupWarning && i.AccountCode == rooms.Code && i.Code == "DEPARTMENT_IGNORED")
	}
	if !warned || !rep.OK {
		t.Fatalf("a warning, not an error: %+v", rep)
	}
	// the day close then posts the room revenue without a department
	res := h.book(t, "2026-09-30", "2026-10-02")
	h.checkIn(t, res, h.r101)
	h.closeDay(t)
	if got := h.deptOfLine(t, "4110"); got != "" {
		t.Fatalf("the department of the charge code is dropped for an account that takes none: %q", got)
	}
	// and the cash line of the day close takes the default of its account, when a payment is made
}

// A required department that cannot be found stops the charge, and it stops the day close with a message that says where the department is missing: never a line without one.
func TestTheFolioAndTheDayCloseRefuseARequiredDepartmentThatIsMissing(t *testing.T) {
	h := setupHotel(t)
	res := h.book(t, "2026-09-30", "2026-10-02")
	st := h.checkIn(t, res, h.r101)
	var otherID int64
	var otherAccount string
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id, gl_account_code FROM charge_codes WHERE property_id = $1 AND code = 'OTHER'`, h.propID).Scan(&otherID, &otherAccount))
	unit := "10000"
	post := func(key string) error {
		_, err := h.Folios.PostCharge(h.admin, h.propID, st.Folio.ID, key, folios.ChargeInput{ChargeCodeID: otherID, Quantity: "1", UnitPrice: &unit})
		return err
	}
	must(t, post("before")) // optional so far: this charge has no department
	// a state the checks of the setup prevent: the account turns required behind their back
	must(t, h.Exec(t, `UPDATE gl_accounts SET department_requirement = 'REQUIRED' WHERE property_id = $1 AND code = $2`, h.propID, otherAccount))
	err := post("after")
	if c := fieldOf(t, err, "charge_code_id"); c != "DEPARTMENT_REQUIRED" {
		t.Fatalf("the charge: %s", c)
	}
	if e, _ := apperr.As(err); e.Fields[0].Message == "" || e.Context["charge_code"] != "OTHER" {
		t.Fatalf("it names the charge code: %+v", e)
	}
	// the day close meets the charge that has no department
	day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
	must(t, err)
	h.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = h.Audit.Run(h.admin, h.propID, day.BusinessDate)
	if c := fieldOf(t, err, "department_id"); c != "DEPARTMENT_REQUIRED" {
		t.Fatalf("the night audit: %s", c)
	}
	if e, _ := apperr.As(err); !strings.Contains(e.Fields[0].Message, "charge code OTHER") {
		t.Fatalf("it says where the department is missing: %q", e.Fields[0].Message)
	}
	// nothing was journaled: the whole close rolled back
	if n := len(h.journals(t, accounting.JournalFilter{Type: "DAY_CLOSE"})); n != 0 {
		t.Fatalf("no journal of the day close: %d", n)
	}
}

func TestTheSystemAccountsOfTheDayCloseTakeTheirDefaultDepartment(t *testing.T) {
	h := setupHotel(t)
	rooms := h.deptID(t, "ROOMS")
	h.rule(t, "1110", "REQUIRED", &rooms) // the cash account
	h.rule(t, "1210", "REQUIRED", &rooms) // the guest ledger
	res := h.book(t, "2026-09-30", "2026-10-02")
	st := h.checkIn(t, res, h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	h.closeDay(t)
	if got := h.deptOfLine(t, "1210"); got != "ROOMS" {
		t.Fatalf("the guest ledger line: %q", got)
	}
	h.requireBalanced(t)
}
