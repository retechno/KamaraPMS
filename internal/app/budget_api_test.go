package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestBudgetAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	budgets := base + "/budgets"

	// The accounts a budget covers.
	var rooms, payroll float64
	for _, a := range abc.do(http.MethodGet, base+"/accounting/accounts?limit=500", nil).body["data"].([]any) {
		switch m := a.(map[string]any); m["code"] {
		case "4110":
			rooms = m["id"].(float64)
		case "5110":
			payroll = m["id"].(float64)
		}
	}
	if rooms == 0 || payroll == 0 {
		t.Fatal("the chart has the accounts 4110 and 5110")
	}

	// A draft of the fiscal year, with its months and the accounts it may cover.
	r := abc.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-01-01", "name": "Plan 2026", "description": "the first plan"})
	if r.status != 201 || r.body["status"] != "DRAFT" || r.body["version"] != float64(1) || r.body["year_label"] != "FY2026" || len(r.body["months"].([]any)) != 12 || len(r.body["available_accounts"].([]any)) == 0 {
		t.Fatalf("create: %d %v", r.status, r.body)
	}
	id := itoaID(int64(r.body["id"].(float64)))
	one := budgets + "/" + id

	if r := abc.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-03-01", "name": "x"}); r.status != 422 || fieldsOf(r)["year_start"] != "INVALID_FISCAL_YEAR" {
		t.Fatalf("not the first month of the fiscal year: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-01-01", "name": ""}); r.status != 422 {
		t.Fatalf("a name is needed: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-01-01", "name": "x", "unknown": 1}); r.status != 400 && r.status != 422 {
		t.Fatalf("an unknown field: %d %v", r.status, r.body)
	}

	months := func(amount string, at ...int) []string {
		out := make([]string, 12)
		for i := range out {
			out[i] = "0"
		}
		for _, m := range at {
			out[m-1] = amount
		}
		return out
	}
	r = abc.do(http.MethodPut, one+"/grid", map[string]any{"rows": []map[string]any{
		{"account_id": rooms, "amounts": months("1000000", 8, 9)},
		{"account_id": payroll, "amounts": months("300000", 9)},
	}})
	rows, _ := r.body["rows"].([]any)
	if r.status != 200 || len(rows) != 2 || r.body["total_revenue"] != "2000000" || r.body["total_expense"] != "300000" || r.body["total_result"] != "1700000" || r.body["account_count"] != float64(2) {
		t.Fatalf("grid: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, one+"/grid", map[string]any{"rows": []map[string]any{{"account_id": 1, "amounts": []string{"1"}}}}); r.status != 422 {
		t.Fatalf("a bad grid: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/spread", map[string]any{"account_id": rooms, "total": "1200", "method": "EQUAL"}); r.status != 200 {
		t.Fatalf("spread: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/fill-from-actuals", map[string]any{}); r.status != 409 || r.body["code"] != "NO_ACTUALS" {
		t.Fatalf("nothing to fill from: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, one, map[string]any{"name": "Plan 2026 (draft)"}); r.status != 200 || r.body["name"] != "Plan 2026 (draft)" {
		t.Fatalf("rename: %d %v", r.status, r.body)
	}

	// CSV: the export reads back through the import.
	exp := e.raw(abc, http.MethodGet, one+"/export")
	if exp.status != 200 || !strings.HasPrefix(exp.contentType, "text/csv") || !strings.HasPrefix(exp.body, "code,name,m1,m2,") || !strings.Contains(exp.body, "4110,") {
		t.Fatalf("export: %d %q %q", exp.status, exp.contentType, exp.body)
	}
	if r := abc.do(http.MethodPost, one+"/import", map[string]any{"csv": exp.body, "dry_run": true}); r.status != 200 || r.body["dry_run"] != true || r.body["accounts"] != float64(2) {
		t.Fatalf("import dry run: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/import", map[string]any{"csv": "code,name,m1\n4110,x,1\n"}); r.status != 422 {
		t.Fatalf("a file without the months: %d %v", r.status, r.body)
	}

	// The report needs an active budget; activating needs an approval.
	if r := abc.do(http.MethodGet, budgets+"/vs-actual", nil); r.status != 404 || r.body["code"] != "NO_ACTIVE_BUDGET" {
		t.Fatalf("no active budget: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/activate", map[string]any{}); r.status != 422 || r.body["code"] != "APPROVAL_REQUIRED" {
		t.Fatalf("no approval: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/activate", map[string]any{"approval": map[string]any{"email": "admin@hotel.com", "password": "wrong password"}}); r.status != 401 {
		t.Fatalf("a wrong password: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/activate", map[string]any{"approval": map[string]any{"email": "admin@hotel.com", "password": testPassword}}); r.status != 200 || r.body["status"] != "ACTIVE" || r.body["approved_by"] == nil {
		t.Fatalf("activate: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, one+"/grid", map[string]any{"rows": []map[string]any{}}); r.status != 409 || r.body["code"] != "BUDGET_NOT_DRAFT" {
		t.Fatalf("an active budget is fixed: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodDelete, one, nil); r.status != 409 || r.body["code"] != "BUDGET_NOT_DRAFT" {
		t.Fatalf("an active budget is not deleted: %d %v", r.status, r.body)
	}

	// A revision is a copy, and a draft is deleted.
	rev := abc.do(http.MethodPost, budgets, map[string]any{"name": "Revision", "copy_from_id": r.body["id"]})
	if rev.status != 201 || rev.body["version"] != float64(2) || rev.body["account_count"] != float64(2) {
		t.Fatalf("copy: %d %v", rev.status, rev.body)
	}
	if r := abc.do(http.MethodDelete, budgets+"/"+itoaID(int64(rev.body["id"].(float64))), nil); r.status != 204 {
		t.Fatalf("delete a draft: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, budgets+"/"+itoaID(int64(rev.body["id"].(float64))), nil); r.status != 404 || r.body["code"] != "BUDGET_NOT_FOUND" {
		t.Fatalf("it is gone: %d %v", r.status, r.body)
	}
	list := abc.do(http.MethodGet, budgets+"?year_start=2026-01-01&status=ACTIVE", nil)
	if data, _ := list.body["data"].([]any); list.status != 200 || len(data) != 1 || data[0].(map[string]any)["rows"] != nil {
		t.Fatalf("the list holds summaries: %d %v", list.status, list.body)
	}
	if r := abc.do(http.MethodGet, budgets+"?status=OPEN", nil); r.status != 422 {
		t.Fatalf("an unknown status: %d %v", r.status, r.body)
	}

	// The report: the actuals are zero here, the budget is the plan. The month and the year to date.
	rep := abc.do(http.MethodGet, budgets+"/vs-actual?from=2026-09-01&to=2026-09-30", nil)
	lines, _ := rep.body["lines"].([]any)
	if rep.status != 200 || rep.body["year_label"] != "FY2026" || rep.body["from"] != "2026-09-01" || rep.body["to"] != "2026-09-30" || len(lines) == 0 {
		t.Fatalf("report: %d %v", rep.status, rep.body)
	}
	var totalRevenue map[string]any
	for _, l := range lines {
		if m := l.(map[string]any); m["key"] == "TOTAL_REVENUE" {
			totalRevenue = m
		}
	}
	period, ytd := totalRevenue["period"].(map[string]any), totalRevenue["ytd"].(map[string]any)
	if period["budget"] != "100" || period["actual"] != "0" || ytd["budget"] != "900" || period["favourable"] != false {
		t.Fatalf("total revenue: %v", totalRevenue)
	}
	for _, bad := range []string{"?to=2027-02-01", "?from=2026-09-01&to=2026-08-01", "?year_start=2026-02-01", "?from=x", "?budget_id=abc"} {
		if r := abc.do(http.MethodGet, budgets+"/vs-actual"+bad, nil); r.status != 422 {
			t.Fatalf("%s: %d %v", bad, r.status, r.body)
		}
	}
	csv := e.raw(abc, http.MethodGet, budgets+"/vs-actual?format=csv&lang=id")
	if csv.status != 200 || !strings.HasPrefix(csv.contentType, "text/csv") || !strings.Contains(csv.body, "Total revenue") {
		t.Fatalf("report csv: %d %q %q", csv.status, csv.contentType, csv.body)
	}
	pdf := e.raw(abc, http.MethodGet, budgets+"/vs-actual.pdf?lang=id")
	if pdf.status != 200 || pdf.contentType != "application/pdf" || !strings.HasPrefix(pdf.body, "%PDF-") {
		t.Fatalf("report pdf: %d %q", pdf.status, pdf.contentType)
	}

	// Another tenant sees nothing.
	for _, path := range []string{"/budgets", "/budgets/" + id, "/budgets/vs-actual", "/budgets/" + id + "/export"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("foreign tenant %s: %d %v", path, r.status, r.body)
		}
	}
	if r := xyz.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-01-01", "name": "x"}); r.status != 404 {
		t.Fatalf("foreign tenant create: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, budgets+"/abc", nil); r.status != 404 || r.body["code"] != "BUDGET_NOT_FOUND" {
		t.Fatalf("a bad id: %d %v", r.status, r.body)
	}
}

func TestBudgetStatisticsAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	budgets := "/api/v1/properties/" + bali + "/budgets"
	r := abc.do(http.MethodPost, budgets, map[string]any{"year_start": "2026-01-01", "name": "Plan"})
	id := itoaID(int64(r.body["id"].(float64)))
	one := budgets + "/" + id

	rows := []map[string]any{{"month": 9, "rooms_available": 300, "rooms_sold": 210, "adr": "1000000"}}
	r = abc.do(http.MethodPut, one+"/statistics", map[string]any{"rows": rows})
	stats, _ := r.body["statistics"].([]any)
	if r.status != 200 || len(stats) != 1 || stats[0].(map[string]any)["occupancy_percent"] != "70.00" || stats[0].(map[string]any)["room_revenue"] != "210000000" || stats[0].(map[string]any)["revpar"] != "700000" {
		t.Fatalf("save: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, one+"/statistics", map[string]any{"rows": []map[string]any{{"month": 1, "rooms_available": 10, "rooms_sold": 11, "adr": "1"}}}); r.status != 422 || fieldsOf(r)["rows[0].rooms_sold"] != "INVALID_VALUE" {
		t.Fatalf("sold above available: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, budgets+"/statistics-vs-actual", nil); r.status != 404 || r.body["code"] != "NO_ACTIVE_BUDGET" {
		t.Fatalf("no active budget: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, one+"/activate", map[string]any{"approval": map[string]any{"email": "admin@hotel.com", "password": testPassword}}); r.status != 200 {
		t.Fatalf("statistics alone make an active budget: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, one+"/statistics", map[string]any{"rows": rows}); r.status != 409 || r.body["code"] != "BUDGET_NOT_DRAFT" {
		t.Fatalf("an active budget is fixed: %d %v", r.status, r.body)
	}
	rep := abc.do(http.MethodGet, budgets+"/statistics-vs-actual?from=2026-09-01&to=2026-09-30", nil)
	metrics, _ := rep.body["metrics"].([]any)
	if rep.status != 200 || rep.body["has_statistics"] != true || len(metrics) != 6 || rep.body["closed_days"] != float64(0) {
		t.Fatalf("report: %d %v", rep.status, rep.body)
	}
	if first := metrics[0].(map[string]any); first["key"] != "rooms_available" || first["unit"] != "NIGHTS" || first["period"].(map[string]any)["budget"] != "300" {
		t.Fatalf("the first metric: %v", first)
	}
	if r := abc.do(http.MethodGet, budgets+"/statistics-vs-actual?to=2027-03-01", nil); r.status != 422 {
		t.Fatalf("a range outside the year: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, budgets+"/statistics-vs-actual", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodPut, one+"/statistics", map[string]any{"rows": rows}); r.status != 404 {
		t.Fatalf("foreign tenant save: %d %v", r.status, r.body)
	}
}
