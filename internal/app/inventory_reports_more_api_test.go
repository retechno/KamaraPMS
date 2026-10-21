package app

import (
	"net/http"
	"strings"
	"testing"
)

// Inventory, PR 6a, through the router (docs/architecture/35-inventory.md 22): the other reports are for who may read the stock, in their own tenant, and each one is a CSV file on request; a draft
// purchase request is made from the items below their minimum.

func TestTheOtherInventoryReportsAreCSVFilesAndForWhoMayReadTheStockInTheirOwnTenantAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	item, wh := w.setup(t)
	w.must(t, abc.do(http.MethodPost, w.base+"/openings", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9b61", "warehouse_id": wh,
		"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "10", "unit_cost": "1000"}}}), 201)
	w.must(t, abc.do(http.MethodPost, w.base+"/issues", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9b62", "warehouse_id": wh, "purpose": "WASTE", "reason_code": "SPOILAGE",
		"lines": []map[string]any{{"item_id": item, "qty": "2"}}}), 201)

	for path, head := range map[string]string{
		"/reports/waste?format=csv":                                 "reason,warehouse,department,item",
		"/reports/waste?format=csv&lang=id":                         "Alasan,Gudang,Departemen,Barang",
		"/reports/count-variances?format=csv":                       "count,approved_on,warehouse",
		"/reports/price-variances?format=csv":                       "supplier,item,name,unit",
		"/reports/transfers-without-requisition?format=csv":         "transfer,date,status,from_warehouse",
		"/reports/amenities-by-month?format=csv":                    "month,item,name,unit",
		"/reports/pending-approvals?format=csv":                     "type,number,department,requester",
		"/reports/recipe-costs?format=csv":                          "outlet,product,name,category",
		"/reports/meals?format=csv":                                 "department,kind,documents,covers",
		"/reports/cost-day?date=2026-09-30&format=csv":              "kind,source_type,document,department",
		"/reports/waste?warehouse_id=" + idText(wh) + "&format=csv": "reason,warehouse",
	} {
		r := w.e.raw(abc, http.MethodGet, w.base+path)
		if r.status != 200 || !strings.HasPrefix(r.contentType, "text/csv") || !strings.HasPrefix(r.body, head) {
			t.Fatalf("%s: %d %s %q", path, r.status, r.contentType, firstLine(r.body))
		}
	}
	if r := w.e.raw(abc, http.MethodGet, w.base+"/reports/waste?format=csv"); !strings.Contains(r.body, "SPOILAGE,Gudang Utama") || !strings.Contains(r.body, ",2000,") {
		t.Fatalf("the waste as a file: %q", r.body)
	}
	rep := w.must(t, abc.do(http.MethodGet, w.base+"/reports/waste", nil), 200)
	if rep.body["total"] != "2000" || rep.body["docs"] != float64(1) {
		t.Fatalf("the waste: %v", rep.body)
	}
	day := w.must(t, abc.do(http.MethodGet, w.base+"/reports/cost-day?date=2026-09-30", nil), 200)
	if day.body["total"] != "2000" || len(day.body["kinds"].([]any)) != 1 {
		t.Fatalf("the documents of the day: %v", day.body)
	}
	// a day after the open one, a date that is not one, and a missing date
	for _, q := range []string{"date=2026-10-05", "date=soon", ""} {
		if r := abc.do(http.MethodGet, w.base+"/reports/cost-day?"+q, nil); r.status != 422 {
			t.Fatalf("cost-day?%s: %d %v", q, r.status, r.body)
		}
	}

	// who reads the stock reads these reports; who does not is refused; another tenant does not find the property
	viewer := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Store viewer", "permissions": []string{"inventory.view"}})
	other := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Accountant", "permissions": []string{"accounting.view"}})
	for _, u := range []struct{ email, role string }{{"viewer@hotel.com", "viewer"}, {"acct@hotel.com", "other"}} {
		role := viewer
		if u.role == "other" {
			role = other
		}
		if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": u.email, "full_name": u.role, "password": testPassword,
			"grants": []map[string]any{{"property_id": mustInt(w.prop), "role_id": role.body["id"]}}}); r.status != 201 {
			t.Fatalf("user: %d %v", r.status, r.body)
		}
	}
	login := func(email string) *client {
		c := &client{env: w.e}
		if r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": email, "password": testPassword}); r.status != 200 {
			t.Fatalf("login: %d %v", r.status, r.body)
		}
		return c
	}
	v, a := login("viewer@hotel.com"), login("acct@hotel.com")
	xyz := w.e.login("XYZ")
	for _, p := range []string{"/reports/waste", "/reports/count-variances", "/reports/price-variances", "/reports/transfers-without-requisition", "/reports/amenities-by-month", "/reports/pending-approvals",
		"/reports/recipe-costs", "/reports/meals", "/reports/cost-day?date=2026-09-30"} {
		if r := v.do(http.MethodGet, w.base+p, nil); r.status != 200 {
			t.Fatalf("a viewer reads %s: %d %v", p, r.status, r.body)
		}
		if r := a.do(http.MethodGet, w.base+p, nil); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
			t.Fatalf("somebody with no right to the stock reads %s: %d %v", p, r.status, r.body)
		}
		if r := xyz.do(http.MethodGet, w.base+p, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("another tenant reads %s: %d %v", p, r.status, r.body)
		}
	}
	// a period that is not one
	if r := abc.do(http.MethodGet, w.base+"/reports/waste?from=2026-10-05&to=2026-10-01", nil); r.status != 422 {
		t.Fatalf("a period that ends before it starts: %d %v", r.status, r.body)
	}
	// a viewer cannot make a request from the low stock list
	if r := v.do(http.MethodPost, w.base+"/requests/from-low-stock", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9b70", "warehouse_id": wh, "item_ids": []float64{item}}); r.status != 403 {
		t.Fatalf("a viewer makes a request: %d %v", r.status, r.body)
	}
}

func TestADraftPurchaseRequestIsMadeFromTheLowStockListAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	item, wh := w.setup(t)
	w.must(t, abc.do(http.MethodPost, w.base+"/openings", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9c61", "warehouse_id": wh,
		"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "4", "unit_cost": "17360"}}}), 201)
	w.must(t, abc.do(http.MethodPut, w.base+"/warehouses/"+idText(wh)+"/levels", map[string]any{"levels": []map[string]any{{"item_id": item, "min_qty": "10"}}}), 200)

	low := w.must(t, abc.do(http.MethodGet, w.base+"/reports/low-stock", nil), 200)
	rows := low.body["data"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["missing"] != "6" || rows[0].(map[string]any)["suggest_qty"] != "6" {
		t.Fatalf("low stock: %v", low.body)
	}
	body := map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9c62", "warehouse_id": wh, "item_ids": []float64{item}}
	req := w.must(t, abc.do(http.MethodPost, w.base+"/requests/from-low-stock", body), 201)
	lines := req.body["lines"].([]any)
	if req.body["status"] != "DRAFT" || len(lines) != 1 || lines[0].(map[string]any)["qty"] != "6" {
		t.Fatalf("the request: %v", req.body)
	}
	again := w.must(t, abc.do(http.MethodPost, w.base+"/requests/from-low-stock", body), 201)
	if again.body["id"] != req.body["id"] {
		t.Fatalf("a repeated request made another: %v and %v", again.body["id"], req.body["id"])
	}
	// the item is back above its minimum: there is nothing to order
	w.must(t, abc.do(http.MethodPut, w.base+"/warehouses/"+idText(wh)+"/levels", map[string]any{"levels": []map[string]any{{"item_id": item, "min_qty": "3"}}}), 200)
	body["client_uuid"] = "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9c63"
	if r := abc.do(http.MethodPost, w.base+"/requests/from-low-stock", body); r.status != 409 || r.body["code"] != "NOTHING_TO_ORDER" {
		t.Fatalf("nothing to order: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, w.base+"/requests/from-low-stock", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9c64", "warehouse_id": wh, "item_ids": []float64{}}); r.status != 422 {
		t.Fatalf("no items: %d %v", r.status, r.body)
	}
}
