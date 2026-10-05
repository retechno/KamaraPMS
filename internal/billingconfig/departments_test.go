package billingconfig_test

import (
	"context"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/departments"
	"kamarapms/internal/platform/apperr"
)

func (f fixture) dept(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = $2`, f.bali, code).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func deptFieldCode(t *testing.T, err error) string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("got %v, want an *apperr.Error", err)
	}
	for _, fe := range e.Fields {
		if fe.Field == "department_id" {
			return fe.Code
		}
	}
	t.Fatalf("no field error on department_id: %+v", e.Fields)
	return ""
}

func TestAChargeCodeHasADefaultDepartment(t *testing.T) {
	f := newFixture(t)
	spa := f.dept(t, "OOD")
	// the standard charge codes were given theirs
	if room := f.codeByName(t, "ROOM"); room.DepartmentID == nil || *room.DepartmentID != f.dept(t, "ROOMS") {
		t.Fatalf("ROOM: %v", room.DepartmentID)
	}
	if other := f.codeByName(t, "OTHER"); other.DepartmentID != nil {
		t.Fatalf("OTHER has none: %v", other.DepartmentID)
	}
	// a new code takes one, a patch changes it, and 0 clears it
	created, err := f.Billing.CreateChargeCode(f.admin, f.bali, billingconfig.ChargeCodeInput{Code: "SPA", Name: "Spa", ChargeType: "SERVICE", PriceMode: "EXCLUSIVE", DepartmentID: &spa, IsActive: true})
	if err != nil || created.DepartmentID == nil || *created.DepartmentID != spa {
		t.Fatalf("create: %v %+v", err, created.DepartmentID)
	}
	fb := f.dept(t, "FB")
	changed, err := f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{DepartmentID: &fb})
	if err != nil || changed.DepartmentID == nil || *changed.DepartmentID != fb {
		t.Fatalf("patch: %v %+v", err, changed.DepartmentID)
	}
	same, err := f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{Name: ptr("Spa and wellness")})
	if err != nil || same.DepartmentID == nil || *same.DepartmentID != fb {
		t.Fatalf("another patch keeps it: %v %+v", err, same.DepartmentID)
	}
	zero := int64(0)
	cleared, err := f.Billing.UpdateChargeCode(f.admin, f.bali, created.ID, billingconfig.ChargeCodePatch{DepartmentID: &zero})
	if err != nil || cleared.DepartmentID != nil {
		t.Fatalf("clear: %v %+v", err, cleared.DepartmentID)
	}
}

func ptr[T any](v T) *T { return &v }

func TestTheDefaultDepartmentOfAChargeCodeMustTakePostings(t *testing.T) {
	f := newFixture(t)
	input := func(dept int64) billingconfig.ChargeCodeInput {
		return billingconfig.ChargeCodeInput{Code: "X1", Name: "x", ChargeType: "OTHER", PriceMode: "EXCLUSIVE", DepartmentID: &dept, IsActive: true}
	}
	_, err := f.Billing.CreateChargeCode(f.admin, f.bali, input(999999))
	if deptFieldCode(t, err) != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("an unknown department")
	}
	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "SG")
	var foreign int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id FROM departments WHERE property_id = $1 AND code = 'FB'`, op.ID).Scan(&foreign); err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.CreateChargeCode(f.admin, f.bali, input(foreign))
	if deptFieldCode(t, err) != "DEPARTMENT_NOT_FOUND" {
		t.Fatal("the department of another property")
	}
	fb := f.dept(t, "FB")
	if _, err := f.Departments.Update(f.admin, f.bali, fb, departments.Patch{Active: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Billing.CreateChargeCode(f.admin, f.bali, input(fb))
	if deptFieldCode(t, err) != "INACTIVE" {
		t.Fatal("a switched off department")
	}
	// a code that already has it is not stopped from changing something else
	restaurant := f.codeByName(t, "RESTAURANT")
	if restaurant.DepartmentID == nil || *restaurant.DepartmentID != fb {
		t.Fatalf("RESTAURANT points to FB: %v", restaurant.DepartmentID)
	}
	if _, err := f.Billing.UpdateChargeCode(f.admin, f.bali, restaurant.ID, billingconfig.ChargeCodePatch{Name: ptr("Restaurant and bar")}); err != nil {
		t.Fatalf("an unchanged department of an old code: %v", err)
	}
}
