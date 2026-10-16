package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/subscription"
)

// POS stage 1 through the router: the masters, the module gate and the shape of the answers (docs/architecture/34-pos.md).
func TestPOSMastersAndTheModuleGateAPI(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	base := "/api/v1/properties/" + idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))

	// the standard departments are there; the food and beverage one is the department of the outlet
	deps := abc.do(http.MethodGet, base+"/departments", nil)
	var dept any
	for _, d := range deps.body["data"].([]any) {
		if d.(map[string]any)["code"] == "FB" {
			dept = d.(map[string]any)["id"]
		}
	}
	if dept == nil {
		t.Fatalf("the F&B department: %v", deps.body)
	}
	taxes := abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "PB1", "name": "PB1", "rate": "10", "tax_on_service": true})
	if taxes.status != 201 {
		t.Fatalf("tax: %d %v", taxes.status, taxes.body)
	}
	profile := abc.do(http.MethodPost, base+"/pos/rule-profiles", map[string]any{"code": "FB", "name": "F&B", "price_mode": "EXCLUSIVE", "rules": []any{map[string]any{"tax_id": taxes.body["id"]}}})
	if profile.status != 201 || len(profile.body["rules"].([]any)) != 1 {
		t.Fatalf("profile: %d %v", profile.status, profile.body)
	}
	list := abc.do(http.MethodPost, base+"/pos/price-lists", map[string]any{"code": "MAIN", "name": "Main"})
	if list.status != 201 {
		t.Fatalf("price list: %d %v", list.status, list.body)
	}
	outlet := abc.do(http.MethodPost, base+"/pos/outlets", map[string]any{
		"code": "RESTO", "name": "Restoran Teratai", "outlet_type": "RESTAURANT", "order_mode": "PRINT", "department_id": dept, "rule_profile_id": profile.body["id"], "price_list_id": list.body["id"],
		"drawer": "LACI1", "uses_tables": true, "allow_room_charge": true, "comp_treatment": "PAYMENT_COMP", "max_discount_pct_no_approval": "0", "rounding_unit": 0,
		"guest_count_required": false, "allow_company_charge": false, "allow_comp": false, "block_night_audit_with_open_bills": false,
	})
	if outlet.status != 201 || outlet.body["code"] != "RESTO" || outlet.body["version"] != float64(1) || outlet.body["comp_treatment"] != "PAYMENT_COMP" {
		t.Fatalf("outlet: %d %v", outlet.status, outlet.body)
	}
	if r := abc.do(http.MethodGet, base+"/pos/outlets", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("outlets: %d %v", r.status, r.body)
	}
	area := abc.do(http.MethodPost, base+"/pos/outlets/"+idOf(outlet)+"/areas", map[string]any{"name": "Indoors"})
	if area.status != 201 || area.body["layout_version"] != float64(1) {
		t.Fatalf("area: %d %v", area.status, area.body)
	}
	saved := abc.do(http.MethodPut, base+"/pos/areas/"+idOf(area)+"/layout", map[string]any{
		"name": "Indoors", "grid_w": 40, "grid_h": 24, "layout_version": 1,
		"tables": []any{map[string]any{"code": "1", "capacity": 2, "shape": "SQUARE", "x": 2, "y": 2, "w": 4, "h": 4, "is_active": true}},
		"marks":  []any{map[string]any{"kind": "WINDOW", "label": "Window", "x": 0, "y": 0, "w": 10, "h": 1}},
	})
	if saved.status != 200 || saved.body["layout_version"] != float64(2) || len(saved.body["tables"].([]any)) != 1 {
		t.Fatalf("layout: %d %v", saved.status, saved.body)
	}
	if r := abc.do(http.MethodPut, base+"/pos/areas/"+idOf(area)+"/layout", map[string]any{"name": "Indoors", "grid_w": 40, "grid_h": 24, "layout_version": 1, "tables": []any{}, "marks": []any{}}); r.status != 409 || r.body["code"] != "VERSION_CONFLICT" {
		t.Fatalf("a stale plan: %d %v", r.status, r.body)
	}
	imp := abc.do(http.MethodPost, base+"/pos/products/import?dry_run=true", map[string]any{"csv": "code,name,category,charge_code\nA,Nasi,NOPE,RESTAURANT\n"})
	if imp.status != 200 || imp.body["dry_run"] != true || len(imp.body["errors"].([]any)) != 1 {
		t.Fatalf("import: %d %v", imp.status, imp.body)
	}
	// the lock of a folio: a folio that does not exist
	if r := abc.do(http.MethodGet, base+"/folios/999999/outlet-lock", nil); r.status != 404 || r.body["code"] != "FOLIO_NOT_FOUND" {
		t.Fatalf("lock of a missing folio: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/outlet-charges", nil); r.status != 200 || r.body["accepted"] != float64(0) {
		t.Fatalf("outlet charges: %d %v", r.status, r.body)
	}

	// A plan without POS: the routes of the module are closed for this tenant, the routes of the core are not.
	txm := db.NewTxManager(dbtest.Pool(t), 5*time.Second)
	svc := subscription.NewService(txm, e.clock, audit.NewWriter(e.clock))
	ctx := context.Background()
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{
		Code: "NOPOS", Name: "No POS", Modules: []subscription.Module{subscription.Housekeeping}, Price: decimal.RequireFromString("1"), CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("NOPOS")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/pos/outlets", "/pos/products", "/outlet-charges", "/folios/1/outlet-lock"} {
		if r := abc.do(http.MethodGet, base+p, nil); r.status != 403 || r.body["code"] != "MODULE_NOT_IN_PLAN" || r.body["context"].(map[string]any)["module"] != "POS" {
			t.Errorf("%s: %d %v", p, r.status, r.body)
		}
	}
	if r := abc.do(http.MethodGet, base+"/folios", nil); r.status != 200 {
		t.Fatalf("folios stay core: %d %v", r.status, r.body)
	}
}

func TestPOSRoutesBelongToThePOSModule(t *testing.T) {
	for pattern, want := range map[string]subscription.Module{
		"GET /api/v1/properties/{propertyId}/pos/outlets":                   subscription.POS,
		"PUT /api/v1/properties/{propertyId}/pos/areas/{id}/layout":         subscription.POS,
		"GET /api/v1/properties/{propertyId}/outlet-charges":                subscription.POS,
		"GET /api/v1/properties/{propertyId}/pos/events":                    subscription.POS,
		"POST /api/v1/properties/{propertyId}/pos/tickets/{id}/status":      subscription.POS,
		"GET /api/v1/properties/{propertyId}/folios/{id}/outlet-lock":       subscription.POS,
		"POST /api/v1/properties/{propertyId}/folios/{id}/outlet-lock/lock": subscription.POS,
		"GET /api/v1/device/jobs/next":                                      subscription.POS,
		"POST /api/v1/properties/{propertyId}/pos/devices/enrollments":      subscription.POS,
		"GET /api/v1/properties/{propertyId}/folios/{id}":                   subscription.Core,
		"POST /api/v1/properties/{propertyId}/folios/{id}/transfer-items":   subscription.Core,
	} {
		if got, ok := subscription.ModuleOf(pattern); !ok || got != want {
			t.Errorf("%s: module %q, want %q", pattern, got, want)
		}
	}
}
