package departments_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"kamarapms/internal/departments"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
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

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	viewer           context.Context
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	return &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: roomstest.Admin(tn.ID), viewer: e.User(t, tn.ID, p.ID, auth.PermAccountingView)}
}

func byCode(t *testing.T, list []departments.Department, code string) departments.Department {
	t.Helper()
	for _, d := range list {
		if d.Code == code {
			return d
		}
	}
	t.Fatalf("no department %s in %+v", code, list)
	return departments.Department{}
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
	t.Fatalf("no error on %s: %+v", field, e.Fields)
	return ""
}

func ptr[T any](v T) *T { return &v }

func TestEveryPropertyGetsTheStandardDepartmentsAndTheChargeCodesTheirDefaults(t *testing.T) {
	f := setup(t)
	list, err := f.Departments.List(f.viewer, f.propID, departments.Filter{})
	must(t, err)
	if len(list) != 8 {
		t.Fatalf("eight USALI departments: %+v", list)
	}
	rooms := byCode(t, list, "ROOMS")
	if rooms.Name != "Rooms" || rooms.Level != 1 || rooms.ParentID != nil || !rooms.Active || !rooms.InUse {
		t.Fatalf("rooms: %+v (the ROOM charge code points to it)", rooms)
	}
	for code, dept := range map[string]string{"ROOM": "ROOMS", "RESTAURANT": "FB", "MINIBAR": "FB", "LAUNDRY": "OOD"} {
		var got *string
		must(t, f.Pool.QueryRow(context.Background(), `SELECT d.code FROM charge_codes c LEFT JOIN departments d ON d.property_id = c.property_id AND d.id = c.department_id WHERE c.property_id = $1 AND c.code = $2`, f.propID, code).Scan(&got))
		if got == nil || *got != dept {
			t.Errorf("charge code %s: default department %v, want %s", code, got, dept)
		}
	}
	var none int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM charge_codes WHERE property_id = $1 AND charge_type IN ('FEE', 'OTHER') AND department_id IS NOT NULL`, f.propID).Scan(&none))
	if none != 0 {
		t.Fatalf("fees and others have no default: %d", none)
	}
}

func TestSubDepartmentsInTwoLevels(t *testing.T) {
	f := setup(t)
	list, err := f.Departments.List(f.admin, f.propID, departments.Filter{})
	must(t, err)
	fb := byCode(t, list, "FB")
	rest, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: " rest ", Name: "Restaurant", ParentID: &fb.ID, SortOrder: 1})
	must(t, err)
	if rest.Code != "REST" || rest.Level != 2 || rest.ParentID == nil || *rest.ParentID != fb.ID || rest.ParentCode != "FB" {
		t.Fatalf("a sub-department: %+v", rest)
	}
	_, err = f.Departments.Create(f.admin, f.propID, departments.Input{Code: "PIZZA", Name: "Pizza", ParentID: &rest.ID})
	if fieldCode(t, err, "parent_id") != "TOO_DEEP" {
		t.Fatal("two levels at most")
	}
	if _, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "BAR", Name: "Bar", ParentID: &fb.ID, SortOrder: 2}); err != nil {
		t.Fatal(err)
	}
	list, err = f.Departments.List(f.viewer, f.propID, departments.Filter{})
	must(t, err)
	var order []string
	for _, d := range list {
		if d.Code == "FB" || (d.ParentID != nil && *d.ParentID == fb.ID) {
			order = append(order, d.Code)
		}
	}
	if len(order) != 3 || order[0] != "FB" || order[1] != "REST" || order[2] != "BAR" {
		t.Fatalf("a department is followed by its sub-departments: %v", order)
	}
	if got := byCode(t, list, "FB").Children; got != 2 {
		t.Fatalf("children: %d", got)
	}
	_, err = f.Departments.Create(f.admin, f.propID, departments.Input{Code: "REST", Name: "Again"})
	wantCode(t, err, "CODE_TAKEN")
	for name, in := range map[string]departments.Input{
		"a bad code": {Code: "no way!", Name: "x"}, "no name": {Code: "X1", Name: "  "}, "a long name": {Code: "X2", Name: strings.Repeat("n", 101)},
		"a bad order": {Code: "X3", Name: "x", SortOrder: -1}, "an unknown parent": {Code: "X4", Name: "x", ParentID: ptr(int64(999999))},
	} {
		if _, err := f.Departments.Create(f.admin, f.propID, in); err == nil {
			t.Errorf("%s: refused", name)
		}
	}
}

func TestTheCodeAndTheParentNeverChange(t *testing.T) {
	f := setup(t)
	list, err := f.Departments.List(f.admin, f.propID, departments.Filter{})
	must(t, err)
	fb, rooms := byCode(t, list, "FB"), byCode(t, list, "ROOMS")
	rest, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "REST", Name: "Restaurant", ParentID: &fb.ID})
	must(t, err)
	// the master is protected by the database itself
	if _, err = f.Pool.Exec(context.Background(), `UPDATE departments SET code = 'OTHER' WHERE id = $1`, rest.ID); err == nil {
		t.Fatal("the code does not change")
	}
	if _, err = f.Pool.Exec(context.Background(), `UPDATE departments SET parent_id = $2 WHERE id = $1`, rest.ID, rooms.ID); err == nil {
		t.Fatal("the parent does not change")
	}
	if _, err = f.Pool.Exec(context.Background(), `UPDATE departments SET parent_id = $2 WHERE id = $1`, rooms.ID, fb.ID); err == nil {
		t.Fatal("a department does not become a sub-department")
	}
	renamed, err := f.Departments.Update(f.admin, f.propID, rest.ID, departments.Patch{Name: ptr("Main restaurant"), SortOrder: ptr(5)})
	must(t, err)
	if renamed.Name != "Main restaurant" || renamed.SortOrder != 5 || renamed.Code != "REST" || renamed.ParentID == nil {
		t.Fatalf("renamed: %+v", renamed)
	}
	_, err = f.Departments.Update(f.admin, f.propID, rest.ID, departments.Patch{Name: ptr("")})
	if fieldCode(t, err, "name") != "INVALID_VALUE" {
		t.Fatal("a name is needed")
	}
}

func TestSwitchingOffAndDeleting(t *testing.T) {
	f := setup(t)
	list, err := f.Departments.List(f.admin, f.propID, departments.Filter{})
	must(t, err)
	fb := byCode(t, list, "FB")
	rest, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "REST", Name: "Restaurant", ParentID: &fb.ID})
	must(t, err)

	_, err = f.Departments.Update(f.admin, f.propID, fb.ID, departments.Patch{Active: ptr(false)})
	wantCode(t, err, "DEPARTMENT_HAS_ACTIVE_CHILDREN")
	wantCode(t, f.Departments.Delete(f.admin, f.propID, fb.ID), "DEPARTMENT_IN_USE")
	if err := f.Departments.Delete(f.admin, f.propID, rest.ID); err != nil {
		t.Fatalf("an unused sub-department is deleted: %v", err)
	}
	_, err = f.Departments.Get(f.admin, f.propID, rest.ID)
	wantCode(t, err, "DEPARTMENT_NOT_FOUND")
	rooms := byCode(t, list, "ROOMS")
	wantCode(t, f.Departments.Delete(f.admin, f.propID, rooms.ID), "DEPARTMENT_IN_USE")
	off, err := f.Departments.Update(f.admin, f.propID, rooms.ID, departments.Patch{Active: ptr(false)})
	must(t, err)
	if off.Active {
		t.Fatal("switched off")
	}
	active := true
	got, err := f.Departments.List(f.viewer, f.propID, departments.Filter{Active: &active})
	must(t, err)
	for _, d := range got {
		if d.Code == "ROOMS" {
			t.Fatal("the active ones do not hold it")
		}
	}
	_, err = f.Departments.Create(f.admin, f.propID, departments.Input{Code: "SUITE", Name: "Suites", ParentID: &rooms.ID})
	if fieldCode(t, err, "parent_id") != "INACTIVE" {
		t.Fatal("under an inactive department")
	}
	check := func(id int64, want string) {
		t.Helper()
		err := f.Departments.Check(f.admin, f.tenantID, f.propID, id, "department_id")
		switch {
		case want == "" && err != nil:
			t.Fatalf("department %d: %v", id, err)
		case want != "" && (err == nil || fieldCode(t, err, "department_id") != want):
			t.Fatalf("department %d: got %v, want %s", id, err, want)
		}
	}
	check(fb.ID, "")
	check(rooms.ID, "INACTIVE")
	check(999999, "DEPARTMENT_NOT_FOUND")
}

func TestRolesAndTenants(t *testing.T) {
	f := setup(t)
	list, err := f.Departments.List(f.admin, f.propID, departments.Filter{})
	must(t, err)
	fb := byCode(t, list, "FB")
	_, err = f.Departments.Create(f.viewer, f.propID, departments.Input{Code: "X", Name: "x"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Departments.Update(f.viewer, f.propID, fb.ID, departments.Patch{Name: ptr("x")})
	wantCode(t, err, "PERMISSION_DENIED")
	wantCode(t, f.Departments.Delete(f.viewer, f.propID, fb.ID), "PERMISSION_DENIED")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermBankView)
	_, err = f.Departments.List(nobody, f.propID, departments.Filter{})
	wantCode(t, err, "PERMISSION_DENIED")

	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "SG")
	adminOther := roomstest.Admin(other.ID)
	_, err = f.Departments.List(adminOther, f.propID, departments.Filter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Departments.Get(adminOther, op.ID, fb.ID)
	wantCode(t, err, "DEPARTMENT_NOT_FOUND")
	_, err = f.Departments.Create(adminOther, op.ID, departments.Input{Code: "REST", Name: "x", ParentID: &fb.ID})
	if fieldCode(t, err, "parent_id") != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("another property's department is not a parent")
	}
	if err := f.Departments.Check(adminOther, other.ID, op.ID, fb.ID, "department_id"); err == nil {
		t.Fatal("another property's department takes no posting")
	}
	if n := f.Count(t, `SELECT count(*) FROM departments WHERE property_id = $1`, op.ID); n != 8 {
		t.Fatalf("each property has its own: %d", n)
	}
}

func TestAuditAndConcurrentCreation(t *testing.T) {
	f := setup(t)
	list, err := f.Departments.List(f.admin, f.propID, departments.Filter{})
	must(t, err)
	fb := byCode(t, list, "FB")
	d, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "SPA", Name: "Spa"})
	must(t, err)
	_, err = f.Departments.Update(f.admin, f.propID, d.ID, departments.Patch{Name: ptr("Spa and wellness")})
	must(t, err)
	must(t, f.Departments.Delete(f.admin, f.propID, d.ID))
	for _, action := range []string{"department.created", "department.updated", "department.deleted"} {
		if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE property_id = $1 AND action = $2 AND entity_id = $3`, f.propID, action, d.ID); n != 1 {
			t.Errorf("%s: %d entries", action, n)
		}
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "BAR", Name: "Bar", ParentID: &fb.ID})
			errs <- err
		}()
	}
	var ok, taken int
	for i := 0; i < 2; i++ {
		err := <-errs
		if err == nil {
			ok++
			continue
		}
		wantCode(t, err, "CODE_TAKEN")
		taken++
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("one wins: %d %d", ok, taken)
	}
}
