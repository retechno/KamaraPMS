package app

import (
	"context"
	"encoding/base64"
	"net/http"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/subscription"
)

// The print agent through the real router (docs/architecture/34-pos.md 10.3 and 20): the routes of a device take the token of a device and no
// other, the user routes take no token of a device, a tenant sees nothing of another, and the module gate and the read-only state apply.
func TestThePrintAgentRoutesAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	pid := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + pid
	xyzBase := "/api/v1/properties/" + idOf(xyz.do(http.MethodPost, "/api/v1/properties", validProperty()))

	pr := abc.do(http.MethodPost, base+"/pos/printers", map[string]any{"name": "Kitchen", "kind": "KITCHEN", "paper_width_mm": 80, "codepage": "CP437", "connection_info": "127.0.0.1:9100"})
	if pr.status != 201 || pr.body["state"] != "NO_DEVICE" {
		t.Fatalf("printer: %d %v", pr.status, pr.body)
	}

	// enrolment: a code, then the agent with no token at all
	en := abc.do(http.MethodPost, base+"/pos/devices/enrollments", nil)
	if en.status != 201 || en.body["expires_in_seconds"] != float64(900) {
		t.Fatalf("enrolment: %d %v", en.status, en.body)
	}
	anon := &client{env: e}
	if r := anon.do(http.MethodPost, "/api/v1/device/enroll", map[string]any{"code": "AAAA-AAAA", "name": "Kasir"}); r.status != 422 || r.body["code"] != "ENROLLMENT_CODE_INVALID" {
		t.Fatalf("a wrong code: %d %v", r.status, r.body)
	}
	dev := anon.do(http.MethodPost, "/api/v1/device/enroll", map[string]any{"code": en.body["code"], "name": "Kasir Lobby", "agent_version": "0.1.0"})
	if dev.status != 201 || len(dev.body["token"].(string)) < 40 || dev.body["token"].(string)[:4] != "kpd_" {
		t.Fatalf("enrolled: %d %v", dev.status, dev.body)
	}
	if r := anon.do(http.MethodPost, "/api/v1/device/enroll", map[string]any{"code": en.body["code"], "name": "Again"}); r.status != 422 {
		t.Fatalf("a code works once: %d %v", r.status, r.body)
	}
	agent := &client{env: e, token: dev.body["token"].(string)}
	devID := int64(dev.body["device_id"].(float64))

	// the printer is assigned to the device: it reads UNKNOWN until the agent reports
	up := abc.do(http.MethodPut, base+"/pos/printers/"+idOf(pr), map[string]any{"name": "Kitchen", "kind": "KITCHEN", "paper_width_mm": 80, "codepage": "CP437",
		"connection_info": "127.0.0.1:9100", "device_id": devID})
	if up.status != 200 || up.body["device_id"] != float64(devID) {
		t.Fatalf("assign: %d %v", up.status, up.body)
	}

	// two kinds of token, never mixed
	if r := agent.do(http.MethodGet, "/api/v1/device/config", nil); r.status != 200 || len(r.body["printers"].([]any)) != 1 {
		t.Fatalf("config: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, "/api/v1/device/config", nil); r.status != 401 || r.body["code"] != "DEVICE_TOKEN_INVALID" {
		t.Fatalf("a user's token on a device route: %d %v", r.status, r.body)
	}
	if r := anon.do(http.MethodGet, "/api/v1/device/config", nil); r.status != 401 || r.body["code"] != "DEVICE_TOKEN_REQUIRED" {
		t.Fatalf("no token: %d %v", r.status, r.body)
	}
	for _, p := range []string{"/pos/devices", "/pos/printers", "/pos/outlets"} {
		if r := agent.do(http.MethodGet, base+p, nil); r.status != 401 {
			t.Fatalf("a device's token on %s: %d %v", p, r.status, r.body)
		}
	}

	// a job in the queue: claimed, printed, and the queue is empty
	ctx := context.Background()
	pool := dbtest.Pool(t)
	if _, err := pool.Exec(ctx, `INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key, payload, document)
		SELECT p.tenant_id, p.property_id, p.id, 'TICKET', 'TICKET', 1, 'TICKET:1:1', '\x1b4041'::bytea, '{}'::jsonb FROM pos_printers p WHERE p.id = $1`, int64(pr.body["id"].(float64))); err != nil {
		t.Fatal(err)
	}
	nx := agent.do(http.MethodGet, "/api/v1/device/jobs/next?wait=0", nil)
	if nx.status != 200 || nx.body["attempt"] != float64(1) || nx.body["connection_info"] != "127.0.0.1:9100" {
		t.Fatalf("claim: %d %v", nx.status, nx.body)
	}
	if raw, err := base64.StdEncoding.DecodeString(nx.body["payload"].(string)); err != nil || string(raw) != "\x1b@A" {
		t.Fatalf("payload: %q %v", raw, err)
	}
	jobID := nx.body["id"].(string)
	if r := agent.do(http.MethodPost, "/api/v1/device/jobs/"+jobID+"/result", map[string]any{"status": "PRINTED", "attempt": 1, "printer_status": "ONLINE"}); r.status != 200 || r.body["final"] != true {
		t.Fatalf("result: %d %v", r.status, r.body)
	}
	if r := agent.do(http.MethodPost, "/api/v1/device/jobs/"+jobID+"/result", map[string]any{"status": "PRINTED", "attempt": 1}); r.status != 200 || r.body["status"] != "PRINTED" {
		t.Fatalf("the same result again: %d %v", r.status, r.body)
	}
	if r := agent.do(http.MethodGet, "/api/v1/device/jobs/next?wait=0", nil); r.status != 204 {
		t.Fatalf("nothing left: %d %v", r.status, r.body)
	}
	if r := agent.do(http.MethodGet, "/api/v1/device/jobs/next?wait=99", nil); r.status != 422 {
		t.Fatalf("a wait over 30 seconds: %d %v", r.status, r.body)
	}
	// a heartbeat: the printer reads online for the setup screens
	hb := agent.do(http.MethodPost, "/api/v1/device/heartbeat", map[string]any{"printers": []any{map[string]any{"id": pr.body["id"], "status": "PAPER_OUT"}}, "queue_depth": 0, "agent_version": "0.1.0"})
	if hb.status != 200 {
		t.Fatalf("heartbeat: %d %v", hb.status, hb.body)
	}
	list := abc.do(http.MethodGet, base+"/pos/printers", nil)
	if p := list.body["data"].([]any)[0].(map[string]any); p["state"] != "PAPER_OUT" || p["device_name"] != "Kasir Lobby" {
		t.Fatalf("printers: %v", list.body)
	}
	if r := agent.do(http.MethodPost, "/api/v1/device/heartbeat", map[string]any{"printers": []any{map[string]any{"id": 1, "status": "BURNING"}}}); r.status != 422 {
		t.Fatalf("a status that does not exist: %d %v", r.status, r.body)
	}
	devs := abc.do(http.MethodGet, base+"/pos/devices", nil)
	d0 := devs.body["data"].([]any)[0].(map[string]any)
	if d0["online"] != true || d0["printers"] != float64(1) || d0["agent_version"] != "0.1.0" || d0["status"] != "ACTIVE" {
		t.Fatalf("devices: %v", devs.body)
	}
	if _, has := d0["token"]; has {
		t.Fatal("the list never carries a token")
	}

	// another tenant sees nothing and can change nothing
	if r := xyz.do(http.MethodGet, base+"/pos/devices", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("another tenant's property: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodPost, xyzBase+"/pos/devices/"+itoa64(devID)+"/revoke", nil); r.status != 404 {
		t.Fatalf("revoking a device of another tenant: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodPost, xyzBase+"/pos/print-jobs/"+jobID+"/reprint", map[string]any{"reason": "x"}); r.status != 404 {
		t.Fatalf("reprinting a job of another tenant: %d %v", r.status, r.body)
	}

	// a plan without POS closes the device routes; a read-only subscription refuses what changes something and keeps the reads
	txm := db.NewTxManager(pool, 5*time.Second)
	svc := subscription.NewService(txm, e.clock, audit.NewWriter(e.clock))
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{Code: "NOPOS", Name: "No POS", Modules: []subscription.Module{subscription.Housekeeping}, Price: decimal.RequireFromString("1"),
		CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("NOPOS")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/api/v1/device/config", "/api/v1/device/jobs/next?wait=0"} {
		if r := agent.do(http.MethodGet, p, nil); r.status != 403 || r.body["code"] != "MODULE_NOT_IN_PLAN" {
			t.Fatalf("%s without the module: %d %v", p, r.status, r.body)
		}
	}
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{Code: "WITHPOS", Name: "With POS", Modules: []subscription.Module{subscription.POS}, Price: decimal.RequireFromString("1"),
		CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("WITHPOS")}); err != nil {
		t.Fatal(err)
	}
	e.clock.Set(time.Date(2026, 12, 20, 3, 0, 0, 0, time.UTC))
	if r := agent.do(http.MethodGet, "/api/v1/device/jobs/next?wait=0", nil); r.status != 403 || r.body["code"] != "SUBSCRIPTION_READ_ONLY" {
		t.Fatalf("a claim is a write: %d %v", r.status, r.body)
	}
	if r := agent.do(http.MethodGet, "/api/v1/device/config", nil); r.status != 200 {
		t.Fatalf("the config is a read: %d %v", r.status, r.body)
	}
	e.clock.Set(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))

	// revoke: the agent is told so and stops; the token cannot be replaced
	abc = e.login("ABC")
	if r := abc.do(http.MethodPost, base+"/pos/devices/"+itoa64(devID)+"/revoke", nil); r.status != 200 || r.body["status"] != "REVOKED" {
		t.Fatalf("revoke: %d %v", r.status, r.body)
	}
	if r := agent.do(http.MethodGet, "/api/v1/device/config", nil); r.status != 401 || r.body["code"] != "DEVICE_REVOKED" {
		t.Fatalf("a revoked token: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/pos/devices/"+itoa64(devID)+"/rotate-token", nil); r.status != 409 || r.body["code"] != "DEVICE_REVOKED" {
		t.Fatalf("rotate of a revoked device: %d %v", r.status, r.body)
	}
}

func itoa64(n int64) string { return decimal.NewFromInt(n).String() }

// A property with no agent never claims a job, so the close of its day is what removes the old documents (docs/architecture/34-pos.md 20.7).
func TestTheCloseOfTheDayPurgesPrintJobsOfAPropertyWithoutAnAgent(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	pid := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + pid
	pr := abc.do(http.MethodPost, base+"/pos/printers", map[string]any{"name": "Kitchen", "kind": "KITCHEN", "paper_width_mm": 80, "codepage": "CP437"})
	if pr.status != 201 {
		t.Fatalf("printer: %d %v", pr.status, pr.body)
	}
	pool := dbtest.Pool(t)
	ctx := context.Background()
	// one job of 12 days ago that never printed and one printed 10 days ago; one of today that must stay as it is
	for i, row := range []struct{ key, status, age string }{{"T:1:1", "QUEUED", "12 days"}, {"T:2:1", "PRINTED", "10 days"}, {"T:3:1", "QUEUED", "1 hour"}} {
		printed := "NULL"
		if row.status == "PRINTED" {
			printed = "now()"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key, status, attempts, printed_at, payload, document, created_at)
			SELECT p.tenant_id, p.property_id, p.id, 'TICKET', 'TICKET', $2, $3, $4, 0, `+printed+`, '\x1b40'::bytea, '{}'::jsonb, '2026-09-30 13:00:00+00'::timestamptz - $5::interval
			FROM pos_printers p WHERE p.id = $1`, int64(pr.body["id"].(float64)), int64(i+1), row.key, row.status, row.age); err != nil {
			t.Fatal(err)
		}
	}
	if r := abc.do(http.MethodPost, base+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}); r.status != 200 {
		t.Fatalf("night audit: %d %v", r.status, r.body)
	}
	var expired, kept, bytes int
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(pool.QueryRow(ctx, `SELECT count(*) FROM pos_print_jobs WHERE dedupe_key = 'T:1:1' AND status = 'FAILED' AND error_code = 'EXPIRED' AND payload IS NULL AND document IS NULL AND payload_purged_at IS NOT NULL`).Scan(&expired))
	must(pool.QueryRow(ctx, `SELECT count(*) FROM pos_print_jobs WHERE dedupe_key = 'T:2:1' AND status = 'PRINTED' AND payload IS NULL AND payload_purged_at IS NOT NULL`).Scan(&bytes))
	must(pool.QueryRow(ctx, `SELECT count(*) FROM pos_print_jobs WHERE dedupe_key = 'T:3:1' AND status = 'QUEUED' AND payload IS NOT NULL`).Scan(&kept))
	if expired != 1 || bytes != 1 || kept != 1 {
		t.Fatalf("after the close of the day: expired %d, printed and purged %d, recent kept %d", expired, bytes, kept)
	}
}
