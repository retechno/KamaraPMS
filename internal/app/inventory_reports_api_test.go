package app

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// Inventory, PR 5, through the router (docs/architecture/35-inventory.md 12): the reports read the stock and the general ledger side by side after the night audit has posted the days, for
// both costing methods; the reports are for who may read the stock, in their own tenant, and each one is a CSV file on request.

func TestInventoryReportsAgreeWithTheGeneralLedgerForBothCostingMethodsAPI(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			w := newInvWorld(t)
			abc := w.abc
			// the costing method is chosen before the first movement
			st := w.must(t, abc.do(http.MethodGet, w.base+"/settings", nil), 200)
			w.must(t, abc.do(http.MethodPut, w.base+"/settings", map[string]any{"costing_method": method, "price_tolerance_pct": st.body["price_tolerance_pct"], "qty_tolerance_pct": st.body["qty_tolerance_pct"],
				"over_receipt_pct": st.body["over_receipt_pct"], "po_requires_pr": st.body["po_requires_pr"], "count_blocks_night_audit": st.body["count_blocks_night_audit"],
				"count_blind": st.body["count_blind"], "engineering_department_id": nil, "version": st.body["version"]}), 200)
			item, wh := w.setup(t)
			w.must(t, abc.do(http.MethodPost, w.base+"/openings", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a51", "warehouse_id": wh,
				"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "120", "unit_cost": "17360"}}}), 201)
			w.must(t, abc.do(http.MethodPost, w.prefix+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}), 200)
			// two kilograms of rice are thrown away on the next day, and that day closes too
			w.e.clock.Set(time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC))
			abc = w.e.login("ABC") // the token of an hour ago has expired
			reason := "SPOILAGE"
			w.must(t, abc.do(http.MethodPost, w.base+"/issues", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a52", "warehouse_id": wh, "purpose": "WASTE", "reason_code": reason,
				"lines": []map[string]any{{"item_id": item, "qty": "2"}}}), 201)
			// and one kilogram goes to the staff meal of the kitchen, ten covers
			var fb float64
			for _, d := range w.must(t, abc.do(http.MethodGet, w.prefix+"/departments", nil), 200).body["data"].([]any) {
				if m := d.(map[string]any); m["code"] == "FB" {
					fb = m["id"].(float64)
				}
			}
			w.must(t, abc.do(http.MethodPost, w.base+"/issues", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a53", "warehouse_id": wh, "purpose": "STAFF_MEAL", "department_id": fb, "covers": 10,
				"lines": []map[string]any{{"item_id": item, "qty": "1"}}}), 201)
			w.must(t, abc.do(http.MethodPost, w.prefix+"/night-audit/run", map[string]any{"business_date": "2026-10-01"}), 200)

			// the value of the stock, beside the account 1310
			val := w.must(t, abc.do(http.MethodGet, w.base+"/reports/stock-value", nil), 200)
			if val.body["status"] != "MATCH" || val.body["live"] != true || val.body["total"] != "2031120" {
				t.Fatalf("the value of the stock: %v", val.body)
			}
			var found bool
			for _, a := range val.body["accounts"].([]any) {
				m := a.(map[string]any)
				if m["code"] == "1310" {
					found = true
					if m["stock"] != "2031120" || m["gl"] != "2031120" || m["difference"] != "0" {
						t.Fatalf("1310: %v", m)
					}
				}
			}
			if !found {
				t.Fatalf("the accounts: %v", val.body["accounts"])
			}
			// a closed day reads the snapshot of its close: the end of the first day, before the waste
			old := w.must(t, abc.do(http.MethodGet, w.base+"/reports/stock-value?as_of=2026-09-30", nil), 200)
			if old.body["live"] != false || old.body["total"] != "2083200" || old.body["status"] != "MATCH" {
				t.Fatalf("the value at the end of the first day: %v", old.body)
			}

			// the reconciliation: the waste is the whole difference, and the cost accounts hold the same figure
			rec := w.must(t, abc.do(http.MethodGet, w.base+"/reports/fb-reconciliation?from=2026-09-30&to=2026-10-01", nil), 200)
			food := rec.body["food"].(map[string]any)
			if food["opening_posted"] != "2083200" || food["purchases"] != "0" || food["closing"] != "2031120" || food["goods_used"] != "52080" || food["staff_meal"] != "17360" || food["net_cost"] != "34720" || food["waste"] != "34720" || food["other_outflows"] != "0" {
				t.Fatalf("the food column: %v", food)
			}
			gl := rec.body["gl"].(map[string]any)
			if gl["status"] != "MATCH" || gl["difference"] != "0" {
				t.Fatalf("the check against the general ledger: %v", gl)
			}
			var waste map[string]any
			for _, l := range gl["lines"].([]any) {
				if l.(map[string]any)["key"] == "INV_WASTE" {
					waste = l.(map[string]any)
				}
			}
			if waste == nil || waste["stock"] != "34720" || waste["gl"] != "34720" {
				t.Fatalf("the waste line: %v", waste)
			}
			if len(gl["lines"].([]any)) < 3 {
				t.Fatalf("the cost accounts the property has mapped: %v", gl["lines"])
			}

			// the daily cost: the waste is on the second day, the total is the sum of the kinds, and the closed days are stored
			dc := w.must(t, abc.do(http.MethodGet, w.base+"/reports/daily-cost?from=2026-09-30&to=2026-10-01", nil), 200)
			days := dc.body["days"].([]any)
			if len(days) != 2 {
				t.Fatalf("days: %v", days)
			}
			d2 := days[1].(map[string]any)
			if d2["live"] != false || d2["total"] != "52080" || d2["kinds"].(map[string]any)["WASTE"] != "34720" || d2["kinds"].(map[string]any)["STAFF_MEAL"] != "17360" || days[0].(map[string]any)["total"] != "0" {
				t.Fatalf("the second day: %v", d2)
			}

			for _, p := range []string{"/reports/low-stock", "/reports/negative-stock", "/reports/received-not-invoiced", "/reports/usage?from=2026-09-30&to=2026-10-01", "/reports/outlet-cost?from=2026-09-30&to=2026-10-01", "/dashboard"} {
				w.must(t, abc.do(http.MethodGet, w.base+p, nil), 200)
			}
			dash := w.must(t, abc.do(http.MethodGet, w.base+"/dashboard", nil), 200)
			if dash.body["stock_value"] != "2031120" || dash.body["negative_items"] != float64(0) {
				t.Fatalf("the dashboard: %v", dash.body)
			}
			use := w.must(t, abc.do(http.MethodGet, w.base+"/reports/usage?from=2026-09-30&to=2026-10-01", nil), 200)
			if use.body["total"] != "52080" {
				t.Fatalf("the usage: %v", use.body)
			}
		})
	}
}

func TestInventoryReportsAreCSVFilesAndForWhoMayReadTheStockInTheirOwnTenantAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	item, wh := w.setup(t)
	w.must(t, abc.do(http.MethodPost, w.base+"/openings", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a61", "warehouse_id": wh,
		"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "10", "unit_cost": "1000"}}}), 201)

	// each report is a file on request, with the names of the columns in the language asked for
	for path, head := range map[string]string{
		"/reports/daily-cost?format=csv":                                                "date,live,SALE_COST_FOOD",
		"/reports/daily-cost?format=csv&lang=id":                                        "Tanggal,live,SALE_COST_FOOD",
		"/reports/outlet-cost?format=csv":                                               "outlet,name,class,revenue",
		"/reports/fb-reconciliation?format=csv":                                         "line,food,beverage,total",
		"/reports/stock-value?format=csv":                                               "warehouse,name,category",
		"/reports/low-stock?format=csv":                                                 "warehouse,item,name,unit",
		"/reports/negative-stock?format=csv":                                            "warehouse,item,name,unit",
		"/reports/received-not-invoiced?format=csv":                                     "receipt,received_on,supplier",
		"/reports/usage?format=csv":                                                     "department,kind,item",
		"/stock-card?warehouse=" + idText(wh) + "&item=" + idText(item) + "&format=csv": "date,kind,document",
	} {
		r := w.e.raw(abc, http.MethodGet, w.base+path)
		if r.status != 200 || !strings.HasPrefix(r.contentType, "text/csv") || !strings.HasPrefix(r.body, head) {
			t.Fatalf("%s: %d %s %q", path, r.status, r.contentType, firstLine(r.body))
		}
	}
	if r := w.e.raw(abc, http.MethodGet, w.base+"/reports/stock-value?format=csv"); !strings.Contains(r.body, "MAIN,Gudang Utama,FOOD,Makanan,1,10000") {
		t.Fatalf("the value of the stock as a file: %q", r.body)
	}

	// who reads the stock reads its reports; who does not is refused
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
	for _, p := range []string{"/dashboard", "/cost-targets", "/reports/daily-cost", "/reports/outlet-cost", "/reports/fb-reconciliation", "/reports/stock-value", "/reports/low-stock",
		"/reports/negative-stock", "/reports/received-not-invoiced", "/reports/usage"} {
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
	// a target is set by who manages the stock
	target := map[string]any{"class": "FOOD", "target_pct": "30"}
	if r := v.do(http.MethodPut, w.base+"/cost-targets", target); r.status != 403 {
		t.Fatalf("a viewer sets a target: %d %v", r.status, r.body)
	}
	w.must(t, abc.do(http.MethodPut, w.base+"/cost-targets", target), 200)
	if r := abc.do(http.MethodPut, w.base+"/cost-targets", map[string]any{"class": "FOOD", "target_pct": "130"}); r.status != 422 {
		t.Fatalf("a target above 100: %d %v", r.status, r.body)
	}
	list := w.must(t, v.do(http.MethodGet, w.base+"/cost-targets", nil), 200)
	if len(list.body["data"].([]any)) != 1 {
		t.Fatalf("targets: %v", list.body)
	}
	if r := abc.do(http.MethodDelete, w.base+"/cost-targets?class=FOOD", nil); r.status != 204 {
		t.Fatalf("remove: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodDelete, w.base+"/cost-targets?class=FOOD", nil); r.status != 404 {
		t.Fatalf("remove again: %d %v", r.status, r.body)
	}
	// a period that is not one
	if r := abc.do(http.MethodGet, w.base+"/reports/daily-cost?from=2026-10-05&to=2026-10-01", nil); r.status != 422 {
		t.Fatalf("a period that ends before it starts: %d %v", r.status, r.body)
	}
}

func idText(f float64) string { return itoaID(int64(f)) }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
