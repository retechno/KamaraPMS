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

// Banquet PR 1 through the router: a room and an event, and the module gate (docs/architecture/36-banquet.md).
func TestBanquetThroughTheRouterAndTheModuleGate(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	base := "/api/v1/properties/" + idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty())) + "/banquet"

	room := abc.do(http.MethodPost, base+"/rooms", map[string]any{"code": "SARA", "name": "Saraswati A", "setup_minutes": 60, "teardown_minutes": 30})
	if room.status != 201 || room.body["code"] != "SARA" {
		t.Fatalf("room: %d %v", room.status, room.body)
	}
	if r := abc.do(http.MethodPut, base+"/rooms/"+idOf(room)+"/setups", map[string]any{"setups": []any{map[string]any{"setup_style": "THEATER", "capacity": 100}}}); r.status != 200 {
		t.Fatalf("setups: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/calendar?from=2026-01-01&to=2026-01-07", nil); r.status != 200 || len(r.body["rooms"].([]any)) != 1 {
		t.Fatalf("calendar: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/events", nil); r.status != 200 {
		t.Fatalf("events: %d %v", r.status, r.body)
	}

	// A plan without the banquet: the routes of the module are closed for this tenant, the routes of the core are not.
	txm := db.NewTxManager(dbtest.Pool(t), 5*time.Second)
	svc := subscription.NewService(txm, e.clock, audit.NewWriter(e.clock))
	ctx := context.Background()
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{
		Code: "NOBANQUET", Name: "No banquet", Modules: []subscription.Module{subscription.Housekeeping}, Price: decimal.RequireFromString("1"), CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("NOBANQUET")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/rooms", "/calendar", "/events", "/items", "/settings", "/notices"} {
		if r := abc.do(http.MethodGet, base+p, nil); r.status != 403 || r.body["code"] != "MODULE_NOT_IN_PLAN" || r.body["context"].(map[string]any)["module"] != "BANQUET" {
			t.Errorf("%s: %d %v", p, r.status, r.body)
		}
	}
}

func TestBanquetRoutesBelongToTheBanquetModule(t *testing.T) {
	for pattern, want := range map[string]subscription.Module{
		"GET /api/v1/properties/{propertyId}/banquet/rooms":                        subscription.Banquet,
		"PUT /api/v1/properties/{propertyId}/banquet/rooms/{id}/parts":             subscription.Banquet,
		"POST /api/v1/properties/{propertyId}/banquet/events/{id}/confirm":         subscription.Banquet,
		"PATCH /api/v1/properties/{propertyId}/banquet/events/{id}/sessions/{sid}": subscription.Banquet,
		"GET /api/v1/properties/{propertyId}/folios/{id}":                          subscription.Core,
	} {
		if got, ok := subscription.ModuleOf(pattern); !ok || got != want {
			t.Errorf("%s: module %q, want %q", pattern, got, want)
		}
	}
}
