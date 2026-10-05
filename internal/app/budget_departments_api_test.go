package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestBudgetByDepartmentAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	budgets := base + "/budgets"

	var fb, rooms float64
	for _, d := range abc.do(http.MethodGet, base+"/departments", nil).body["data"].([]any) {
		if m := d.(map[string]any); m["code"] == "FB" {
			fb = m["id"].(float64)
		}
	}
	rest := abc.do(http.MethodPost, base+"/departments", map[string]any{"code": "REST", "name": "Restaurant", "parent_id": fb})
	restID := rest.body["id"].(float64)
	for _, a := range abc.do(http.MethodGet, base+"/accounting/accounts?limit=500", nil).body["data"].([]any) {
		if m := a.(map[string]any); m["code"] == "4210" {
			rooms = m["id"].(float64)
		}
	}
	months := func(amount string, at int) []string {
		out := make([]string, 12)
		for i := range out {
			out[i] = "0"
		}
		out[at-1] = amount
		return out
	}

	r := abc.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-01-01", "name": "Plan"})
	one := budgets + "/" + itoaID(int64(r.body["id"].(float64)))
	r = abc.do(http.MethodPut, one+"/grid", map[string]any{"rows": []map[string]any{
		{"account_id": rooms, "department_id": restID, "amounts": months("500000", 9)},
		{"account_id": rooms, "amounts": months("100000", 9)},
	}})
	rows, _ := r.body["rows"].([]any)
	if r.status != 200 || len(rows) != 2 || rows[0].(map[string]any)["department_id"] != nil || rows[1].(map[string]any)["department_code"] != "REST" || r.body["total_revenue"] != "600000" {
		t.Fatalf("grid by department: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, one+"/grid", map[string]any{"rows": []map[string]any{{"account_id": rooms, "department_id": 999999, "amounts": months("1", 1)}}}); r.status != 422 || fieldsOf(r)["rows[0].department_id"] != "DEPARTMENT_NOT_FOUND" {
		t.Fatalf("an unknown department: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, one+"/grid", map[string]any{"rows": []map[string]any{
		{"account_id": rooms, "department_id": restID, "amounts": months("1", 1)}, {"account_id": rooms, "department_id": restID, "amounts": months("2", 2)},
	}}); r.status != 422 || fieldsOf(r)["rows[1].account_id"] != "DUPLICATE" {
		t.Fatalf("the same account and department twice: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/spread", map[string]any{"account_id": rooms, "department_id": restID, "total": "1200", "method": "EQUAL"}); r.status != 200 {
		t.Fatalf("spread of a department row: %d %v", r.status, r.body)
	}
	exp := e.raw(abc, http.MethodGet, one+"/export")
	if exp.status != 200 || !strings.HasPrefix(exp.body, "code,name,department,m1,") || !strings.Contains(exp.body, "REST,") {
		t.Fatalf("export: %d %q", exp.status, exp.body)
	}
	if r := abc.do(http.MethodPost, one+"/import", map[string]any{"csv": exp.body, "dry_run": true}); r.status != 200 || r.body["accounts"] != float64(2) {
		t.Fatalf("the export reads back: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/import", map[string]any{"csv": "code,department,m1,m2,m3,m4,m5,m6,m7,m8,m9,m10,m11,m12\n4210,NOPE,1,,,,,,,,,,,\n"}); r.status != 422 || fieldsOf(r)["rows[2].department"] != "DEPARTMENT_NOT_FOUND" {
		t.Fatalf("an unknown department code: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/activate", map[string]any{"approval": map[string]any{"email": "admin@hotel.com", "password": testPassword}}); r.status != 200 {
		t.Fatalf("activate: %d %v", r.status, r.body)
	}

	rep := abc.do(http.MethodGet, budgets+"/department-vs-actual?from=2026-09-01&to=2026-09-30", nil)
	deps, _ := rep.body["departments"].([]any)
	totals, _ := rep.body["totals"].(map[string]any)
	if rep.status != 200 || len(deps) == 0 || totals["revenue"] == nil {
		t.Fatalf("report by department: %d %v", rep.status, rep.body)
	}
	for _, d := range deps {
		if m := d.(map[string]any); m["code"] == "FB" {
			kids, _ := m["children"].([]any)
			budgetOf := m["revenue"].(map[string]any)["period"].(map[string]any)["budget"]
			if budgetOf != "100" || len(kids) != 1 || kids[0].(map[string]any)["code"] != "REST" {
				t.Fatalf("FB adds up its restaurant: the spread of 1200 over twelve months is 100 in September: %v", m)
			}
		}
	}
	if r := abc.do(http.MethodGet, budgets+"/department-vs-actual?department_id="+itoaID(int64(restID)), nil); r.status != 200 || len(r.body["departments"].([]any)) != 1 {
		t.Fatalf("one sub-department: %d %v", r.status, r.body)
	}
	for _, bad := range []string{"?department_id=abc", "?department_id=999999"} {
		if r := abc.do(http.MethodGet, budgets+"/department-vs-actual"+bad, nil); r.status != 422 && r.status != 404 {
			t.Fatalf("%s: %d %v", bad, r.status, r.body)
		}
	}
	vs := abc.do(http.MethodGet, budgets+"/vs-actual?from=2026-09-01&to=2026-09-30", nil)
	var found bool
	for _, l := range vs.body["lines"].([]any) {
		for _, a := range l.(map[string]any)["accounts"].([]any) {
			if m := a.(map[string]any); m["code"] == "4210" {
				found = len(m["departments"].([]any)) == 2
			}
		}
	}
	if !found {
		t.Fatalf("the account shows its departments: %v", vs.body)
	}
	if r := xyz.do(http.MethodGet, budgets+"/department-vs-actual", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
}
