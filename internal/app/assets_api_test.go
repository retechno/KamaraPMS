package app

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/subscription"
)

// Fixed assets, PR 1, through the router (docs/architecture/37-fixed-assets.md): the categories, the opening balances, the register, the labels, the control accounts, the module gate and
// the isolation of the tenants.

func TestAssetsOpeningRegisterLabelsAndGatesAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	base := w.prefix + "/assets"

	cats := w.must(t, abc.do(http.MethodGet, base+"/categories", nil), 200)
	if n := len(cats.body["data"].([]any)); n != 7 {
		t.Fatalf("%d default categories", n)
	}
	rows := []map[string]any{
		{"name": "Lobby furniture", "category": "FFE", "cost": "50000000", "accumulated_depreciation": "30000000", "in_service_date": "2024-01-15"},
		{"name": "Hotel van", "category": "VEHICLE", "cost": "200000000", "accumulated_depreciation": "80000000", "in_service_date": "2024-01-15"},
	}
	req := map[string]any{"as_of": "2026-09-30", "journal_date": "2026-09-30", "rows": rows}
	dry := w.must(t, abc.do(http.MethodPost, base+"/opening/import?dry_run=true", req), 200)
	if dry.body["valid"] != true || dry.body["net_to_equity"] != "140000000" {
		t.Fatalf("dry run: %v", dry.body)
	}
	if r := abc.do(http.MethodPost, base+"/opening/import", req); r.status != 400 {
		t.Fatalf("an import without a key: %d %v", r.status, r.body)
	}
	hdr := map[string]string{"Idempotency-Key": "import-1"}
	res := abc.doWith(http.MethodPost, base+"/opening/import", req, hdr)
	w.must(t, res, 201)
	if res.body["mode"] != "WITH_JOURNAL" || res.body["register_from"] != "2026-10-01" || res.body["journal_id"] == nil {
		t.Fatalf("import: %v", res.body)
	}
	w.must(t, abc.doWith(http.MethodPost, base+"/opening/import", req, hdr), 200) // the same key answers the first

	page := w.must(t, abc.do(http.MethodGet, base+"?limit=10", nil), 200)
	if page.body["total"] != float64(2) || page.body["summary"].(map[string]any)["carrying_value"] != "140000000" {
		t.Fatalf("register: %v", page.body)
	}
	first := page.body["data"].([]any)[0].(map[string]any)
	one := w.must(t, abc.do(http.MethodGet, base+"/"+itoa(int64(first["id"].(float64))), nil), 200)
	if len(one.body["ledger"].([]any)) != 1 {
		t.Fatalf("the ledger of an opening asset: %v", one.body["ledger"])
	}

	// the labels: a PDF, and the literal route is not taken for an asset id
	raw := w.e.raw(abc, http.MethodGet, base+"/labels.pdf?layout=ROLL_50x30")
	if raw.status != 200 || raw.contentType != "application/pdf" || !strings.HasPrefix(raw.body, "%PDF-") {
		t.Fatalf("labels: %d %s %s", raw.status, raw.contentType, raw.body)
	}
	if raw := w.e.raw(abc, http.MethodGet, base+"/labels.pdf?layout=NOPE"); raw.status != 422 {
		t.Fatalf("an unknown layout: %d", raw.status)
	}

	// the accumulated accounts are the register's own
	manual := map[string]any{"journal_date": "2026-09-30", "description": "By hand", "lines": []map[string]any{
		{"account_id": w.acct["1592"], "debit": "0", "credit": "1000"}, {"account_id": w.acct["3900"], "debit": "1000", "credit": "0"}}}
	if r := abc.doWith(http.MethodPost, w.prefix+"/accounting/journals", manual, map[string]string{"Idempotency-Key": "m1"}); r.status != 422 || fieldsOf(r)["lines[0].account_id"] != "CONTROL_ACCOUNT" {
		t.Fatalf("a manual journal on 1592: %d %v", r.status, r.body)
	}

	// another tenant does not see the property
	xyz := w.e.login("XYZ")
	for _, p := range []string{"/assets", "/assets/categories", "/assets/settings", "/assets/labels.pdf"} {
		if r := xyz.do(http.MethodGet, w.prefix+p, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("another tenant %s: %d %v", p, r.status, r.body)
		}
	}

	// a plan without the module
	txm := db.NewTxManager(dbtest.Pool(t), 5*time.Second)
	svc := subscription.NewService(txm, w.e.clock, audit.NewWriter(w.e.clock))
	ctx := context.Background()
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{Code: "NOINV", Name: "No inventory", Modules: []subscription.Module{subscription.Housekeeping}, Price: decimal.RequireFromString("1"), CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("NOINV")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/assets", "/assets/categories", "/assets/settings", "/assets/labels.pdf"} {
		if r := abc.do(http.MethodGet, w.prefix+p, nil); r.status != 403 || r.body["code"] != "MODULE_NOT_IN_PLAN" || r.body["context"].(map[string]any)["module"] != "INVENTORY" {
			t.Fatalf("%s without the module: %d %v", p, r.status, r.body)
		}
	}
}
