package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/subscription"
)

// Every route the server registers belongs to a module (docs/architecture/26-subscriptions.md §3): a new route must choose.
func TestEveryRouteHasAModule(t *testing.T) {
	for _, r := range serverRoutes(t) {
		if !strings.HasPrefix(r.path, "/api/") { // the probes are outside the API and its gate
			continue
		}
		if _, ok := subscription.ModuleOf(r.key()); !ok {
			t.Errorf("route %s has no module: add it to internal/subscription/modules.go", r.key())
		}
	}
	for pattern, want := range map[string]subscription.Module{
		"POST /api/v1/properties/{propertyId}/rooms/{roomId}/housekeeping": subscription.Core,
		"GET /api/v1/properties/{propertyId}/housekeeping":                 subscription.Core,
		"POST /api/v1/properties/{propertyId}/housekeeping/bulk":           subscription.Housekeeping,
		"GET /api/v1/properties/{propertyId}/housekeeping/tasks":           subscription.Housekeeping,
		"GET /api/v1/properties/{propertyId}/lost-found":                   subscription.Housekeeping,
		"GET /api/v1/properties/{propertyId}/reports/maintenance":          subscription.Housekeeping,
		"GET /api/v1/properties/{propertyId}/reports/daily-summary":        subscription.Core,
		"GET /api/v1/properties/{propertyId}/budgets":                      subscription.Accounting,
		"GET /api/v1/properties/{propertyId}/tax/invoices":                 subscription.Accounting,
		"GET /api/v1/properties/{propertyId}/taxes":                        subscription.Core,
		"GET /api/v1/properties/{propertyId}/folios/{id}":                  subscription.Core,
		"GET /api/v1/subscription":                                         subscription.Core,
	} {
		if got, _ := subscription.ModuleOf(pattern); got != want {
			t.Errorf("%s: module %q, want %q", pattern, got, want)
		}
	}
}

func intp(n int) *int { return &n }

func TestSubscriptionAPI(t *testing.T) {
	e := newAPI(t)
	txm := db.NewTxManager(dbtest.Pool(t), 5*time.Second)
	svc := subscription.NewService(txm, e.clock, audit.NewWriter(e.clock))
	ctx := context.Background()

	abc, xyz := e.login("ABC"), e.login("XYZ")
	base := "/api/v1/properties/" + idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	xyzBase := "/api/v1/properties/" + idOf(xyz.do(http.MethodPost, "/api/v1/properties", validProperty()))

	// A new tenant is on the default plan's trial: 30 days from the day it was created (2026-09-30 in Jakarta).
	s := abc.do(http.MethodGet, "/api/v1/subscription", nil)
	if s.status != 200 || s.body["plan_code"] != "TRIAL" || s.body["status"] != "TRIAL" || s.body["access"] != "FULL" ||
		s.body["paid_through"] != "2026-10-29" || s.body["grace_ends_on"] != "2026-11-12" || s.body["days_left"] != float64(30) {
		t.Fatalf("trial summary: %d %v", s.status, s.body)
	}
	if u := s.body["usage"].(map[string]any); u["properties"] != float64(1) || u["users"] != float64(1) || u["rooms"] != float64(0) {
		t.Fatalf("usage: %v", u)
	}
	if r := abc.do(http.MethodGet, "/api/v1/subscription/invoices", nil); r.status != 200 || len(r.body["items"].([]any)) != 0 {
		t.Fatalf("no invoices yet: %d %v", r.status, r.body)
	}

	// A plan without ACCOUNTING: its routes answer 403 for ABC only; housekeeping stays.
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{
		Code: "basic", Name: "Basic", Modules: []subscription.Module{subscription.Housekeeping}, Limits: subscription.Limits{MaxRooms: intp(2)},
		Price: decimal.RequireFromString("1500000"), CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("BASIC")}); err != nil {
		t.Fatal(err)
	}
	if r := abc.do(http.MethodGet, base+"/budgets", nil); r.status != 403 || r.body["code"] != "MODULE_NOT_IN_PLAN" || r.body["context"].(map[string]any)["module"] != "ACCOUNTING" {
		t.Fatalf("accounting is off: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/lost-found", nil); r.status != 200 {
		t.Fatalf("housekeeping is on: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, xyzBase+"/budgets", nil); r.status != 200 {
		t.Fatalf("another tenant keeps its plan: %d %v", r.status, r.body)
	}

	// Two active rooms at most; an inactive room is free, activating it counts.
	rt := abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2})
	room := func(n string, active bool) response {
		return abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_number": n, "room_type_id": rt.body["id"], "is_active": active})
	}
	r101 := room("101", true)
	if r := room("102", true); r101.status != 201 || r.status != 201 {
		t.Fatalf("rooms within the limit: %d %d %v", r101.status, r.status, r.body)
	}
	if r := room("103", true); r.status != 409 || r.body["code"] != "PLAN_LIMIT_REACHED" || r.body["context"].(map[string]any)["limit"] != float64(2) {
		t.Fatalf("third active room: %d %v", r.status, r.body)
	}
	r104 := room("104", false)
	if r104.status != 201 {
		t.Fatalf("an inactive room is not counted: %d %v", r104.status, r104.body)
	}
	if r := abc.do(http.MethodPatch, base+"/rooms/"+idOf(r104), map[string]any{"is_active": true}); r.status != 409 || r.body["code"] != "PLAN_LIMIT_REACHED" {
		t.Fatalf("activating over the limit: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/rooms/"+idOf(r101), map[string]any{"is_active": false}); r.status != 200 {
		t.Fatalf("deactivate: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/rooms/"+idOf(r104), map[string]any{"is_active": true}); r.status != 200 {
		t.Fatalf("activating within the limit: %d %v", r.status, r.body)
	}
	// An override lifts the plan's limit for this tenant only.
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{Limits: subscription.Limits{MaxRooms: intp(3)}}); err != nil {
		t.Fatal(err)
	}
	if r := room("103", true); r.status != 201 {
		t.Fatalf("within the override: %d %v", r.status, r.body)
	}

	// Grace: after paid_through, writes still work and the summary counts the days left.
	e.clock.Set(time.Date(2026, 11, 5, 3, 0, 0, 0, time.UTC))
	abc = e.login("ABC")
	if s := abc.do(http.MethodGet, "/api/v1/subscription", nil); s.body["access"] != "GRACE" || s.body["days_left"] != float64(8) {
		t.Fatalf("grace: %v", s.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "STD", "name": "Standard", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}); r.status != 201 {
		t.Fatalf("writes in grace: %d %v", r.status, r.body)
	}

	// Read only: every read works, no write does (signing in does).
	e.clock.Set(time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC))
	abc = e.login("ABC")
	if r := abc.do(http.MethodGet, base+"/rooms", nil); r.status != 200 {
		t.Fatalf("reads stay open: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "SUP", "name": "Superior", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}); r.status != 403 || r.body["code"] != "SUBSCRIPTION_READ_ONLY" {
		t.Fatalf("writes are refused: %d %v", r.status, r.body)
	}
	if s := abc.do(http.MethodGet, "/api/v1/subscription", nil); s.body["access"] != "READ_ONLY" || s.body["days_left"] != float64(0) {
		t.Fatalf("read-only summary: %v", s.body)
	}
	xyz = e.login("XYZ")
	if r := xyz.do(http.MethodPost, xyzBase+"/room-types", map[string]any{"code": "SUP", "name": "Superior", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}); r.status != 403 {
		t.Fatalf("XYZ's trial has ended too: %d %v", r.status, r.body)
	}

	// The operator bills the next period from today (the paid period is past) and records the payment: writing comes back.
	inv, err := svc.IssueInvoice(ctx, subscription.IssueInput{TenantCode: "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Number != "SUB-2026-00001" || inv.PeriodStart.String() != "2026-11-20" || inv.PeriodEnd.String() != "2026-12-19" ||
		!inv.Amount.Equal(decimal.RequireFromString("1500000")) || inv.Status != subscription.InvoiceIssued || inv.PlanCode != "BASIC" {
		t.Fatalf("invoice: %+v", inv)
	}
	if _, err := svc.IssueInvoice(ctx, subscription.IssueInput{TenantCode: "ABC"}); !apperr.IsCode(err, "INVOICE_PERIOD_OVERLAP") {
		t.Fatalf("a second open invoice for the same period: %v", err)
	}
	if r := abc.do(http.MethodGet, "/api/v1/subscription/invoices", nil); r.status != 200 || len(r.body["items"].([]any)) != 1 {
		t.Fatalf("invoices: %d %v", r.status, r.body)
	}
	_, sub, err := svc.PayInvoice(ctx, inv.Number, civil.MustParseDate("2026-11-20"), "TRF-001")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != subscription.StatusActive || sub.PaidThrough.String() != "2026-12-19" {
		t.Fatalf("renewed: %+v", sub)
	}
	if r := abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "SUP", "name": "Superior", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}); r.status != 201 {
		t.Fatalf("writes after the payment: %d %v", r.status, r.body)
	}
	if _, _, err := svc.PayInvoice(ctx, inv.Number, civil.MustParseDate("2026-11-20"), "again"); !apperr.IsCode(err, "INVOICE_NOT_OPEN") {
		t.Fatalf("paying twice: %v", err)
	}
	// The next invoice follows the paid period.
	next, err := svc.IssueInvoice(ctx, subscription.IssueInput{TenantCode: "ABC", Periods: 2})
	if err != nil {
		t.Fatal(err)
	}
	if next.Number != "SUB-2026-00002" || next.PeriodStart.String() != "2026-12-20" || next.PeriodEnd.String() != "2027-02-19" || !next.Amount.Equal(decimal.RequireFromString("3000000")) {
		t.Fatalf("next invoice: %+v", next)
	}
	if _, err := svc.VoidInvoice(ctx, next.Number, "sent by mistake"); err != nil {
		t.Fatal(err)
	}
}
