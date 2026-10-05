package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestDepartmentsAPIAndTheDepartmentReport(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	depts := base + "/departments"

	// the standard departments
	r := abc.do(http.MethodGet, depts, nil)
	list, _ := r.body["data"].([]any)
	if r.status != 200 || len(list) != 8 {
		t.Fatalf("list: %d %v", r.status, r.body)
	}
	var fb float64
	for _, d := range list {
		if m := d.(map[string]any); m["code"] == "FB" {
			fb = m["id"].(float64)
		}
	}
	// a sub-department, in two levels at most
	sub := abc.do(http.MethodPost, depts, map[string]any{"code": "rest", "name": "Restaurant", "parent_id": fb})
	if sub.status != 201 || sub.body["code"] != "REST" || sub.body["level"] != float64(2) || sub.body["parent_code"] != "FB" {
		t.Fatalf("sub-department: %d %v", sub.status, sub.body)
	}
	subID := int64(sub.body["id"].(float64))
	if r := abc.do(http.MethodPost, depts, map[string]any{"code": "PIZZA", "name": "Pizza", "parent_id": sub.body["id"]}); r.status != 422 || fieldsOf(r)["parent_id"] != "TOO_DEEP" {
		t.Fatalf("three levels: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, depts, map[string]any{"code": "REST", "name": "Again"}); r.status != 409 || r.body["code"] != "CODE_TAKEN" {
		t.Fatalf("the same code: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, depts+"/"+itoaID(subID), map[string]any{"name": "Main restaurant", "code": "X"}); r.status != 400 && r.status != 422 {
		t.Fatalf("the code does not change: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, depts+"/"+itoaID(subID), map[string]any{"name": "Main restaurant", "sort_order": 3}); r.status != 200 || r.body["name"] != "Main restaurant" || r.body["code"] != "REST" {
		t.Fatalf("rename: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, depts+"?active=maybe", nil); r.status != 422 {
		t.Fatalf("a bad filter: %d %v", r.status, r.body)
	}

	// a department on a manual journal line, and the report by department
	var rooms, cash float64
	for _, a := range abc.do(http.MethodGet, base+"/accounting/accounts?limit=500", nil).body["data"].([]any) {
		switch m := a.(map[string]any); m["code"] {
		case "4210":
			rooms = m["id"].(float64)
		case "1110":
			cash = m["id"].(float64)
		}
	}
	post := abc.doWith(http.MethodPost, base+"/accounting/journals", map[string]any{"journal_date": "2026-09-30", "description": "sale", "lines": []map[string]any{
		{"account_id": cash, "debit": "300000"}, {"account_id": rooms, "credit": "300000", "department_id": subID},
	}}, map[string]string{"Idempotency-Key": "j-1"})
	jl, _ := post.body["lines"].([]any)
	if post.status != 201 || len(jl) != 2 || jl[1].(map[string]any)["department_code"] != "REST" {
		t.Fatalf("journal: %d %v", post.status, post.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/accounting/journals", map[string]any{"journal_date": "2026-09-30", "description": "x", "lines": []map[string]any{
		{"account_id": cash, "debit": "10"}, {"account_id": rooms, "credit": "10", "department_id": 999999},
	}}, map[string]string{"Idempotency-Key": "j-2"}); r.status != 422 || fieldsOf(r)["lines[1].department_id"] != "DEPARTMENT_NOT_FOUND" {
		t.Fatalf("an unknown department: %d %v", r.status, r.body)
	}
	rep := abc.do(http.MethodGet, base+"/accounting/department-report?from=2026-09-01&to=2026-09-30", nil)
	deps, _ := rep.body["departments"].([]any)
	totals, _ := rep.body["totals"].(map[string]any)
	if rep.status != 200 || len(deps) == 0 || totals["revenue"] != "300000" || totals["profit"] != "300000" {
		t.Fatalf("report: %d %v", rep.status, rep.body)
	}
	for _, d := range deps {
		if m := d.(map[string]any); m["code"] == "FB" {
			children, _ := m["children"].([]any)
			if m["revenue"] != "300000" || m["own_revenue"] != "0" || len(children) != 1 || children[0].(map[string]any)["revenue"] != "300000" {
				t.Fatalf("FB adds up its sub-department: %v", m)
			}
		}
	}
	one := abc.do(http.MethodGet, base+"/accounting/department-report?department_id="+itoaID(subID), nil)
	if one.status != 200 || len(one.body["departments"].([]any)) != 1 {
		t.Fatalf("one sub-department: %d %v", one.status, one.body)
	}
	for _, bad := range []string{"?department_id=abc", "?department_id=999999", "?from=2026-09-30&to=2026-09-01"} {
		if r := abc.do(http.MethodGet, base+"/accounting/department-report"+bad, nil); r.status != 422 && r.status != 404 {
			t.Fatalf("%s: %d %v", bad, r.status, r.body)
		}
	}
	csv := e.raw(abc, http.MethodGet, base+"/accounting/department-report?from=2026-09-01&to=2026-09-30&format=csv")
	if csv.status != 200 || !strings.HasPrefix(csv.contentType, "text/csv") || !strings.Contains(csv.body, "REST,Main restaurant,2,") || !strings.Contains(csv.body, "300000") {
		t.Fatalf("csv: %d %q", csv.status, csv.body)
	}
	pdf := e.raw(abc, http.MethodGet, base+"/accounting/department-report.pdf?lang=id")
	if pdf.status != 200 || pdf.contentType != "application/pdf" || !strings.HasPrefix(pdf.body, "%PDF-") {
		t.Fatalf("pdf: %d %q", pdf.status, pdf.contentType)
	}

	// the department of a used department is switched off, not deleted; an unused one is deleted
	if r := abc.do(http.MethodDelete, depts+"/"+itoaID(subID), nil); r.status != 409 || r.body["code"] != "DEPARTMENT_IN_USE" {
		t.Fatalf("delete a used department: %d %v", r.status, r.body)
	}
	spa := abc.do(http.MethodPost, depts, map[string]any{"code": "SPA", "name": "Spa"})
	if r := abc.do(http.MethodDelete, depts+"/"+itoaID(int64(spa.body["id"].(float64))), nil); r.status != 204 {
		t.Fatalf("delete an unused one: %d %v", r.status, r.body)
	}

	// another tenant sees none of it
	for _, path := range []string{"/departments", "/accounting/department-report"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("foreign tenant %s: %d %v", path, r.status, r.body)
		}
	}
}
