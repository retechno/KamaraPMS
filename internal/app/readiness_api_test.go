package app

import (
	"net/http"
	"testing"
	"time"
)

// The go-live check over HTTP: the same answer the night audit uses, in its structured form, and a night audit that refuses a day it cannot journal (audit F-15).
func TestAccountingReadinessAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	r := abc.do(http.MethodGet, base+"/accounting/readiness", nil)
	if r.status != 200 || r.body["ready"] != true || r.body["status"] != "READY" || len(r.body["blockers"].([]any)) != 0 {
		t.Fatalf("a new property is ready: %d %v", r.status, r.body)
	}

	// the room charge code loses its account: the room revenue would go to the suspense account
	var roomCode int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomCode = int64(m["id"].(float64))
		}
	}
	abc.do(http.MethodPatch, base+"/charge-codes/"+itoaID(roomCode), map[string]any{"gl_account_code": "NO-SUCH-ACCOUNT"})
	r = abc.do(http.MethodGet, base+"/accounting/readiness", nil)
	blockers, _ := r.body["blockers"].([]any)
	if r.status != 200 || r.body["ready"] != false || r.body["status"] != "NOT_READY" || len(blockers) != 1 {
		t.Fatalf("not ready: %d %v", r.status, r.body)
	}
	if b := blockers[0].(map[string]any); b["code"] != "ROOM_CHARGE_CODE_UNMAPPED" || b["ref"] != "ROOM" || b["message"] == "" {
		t.Fatalf("the blocker is structured: %v", b)
	}

	// the night audit says the same, and refuses to close the day
	pv := abc.do(http.MethodGet, base+"/night-audit/preview", nil)
	if pv.status != 200 || pv.body["can_run"] != false || len(pv.body["blockers"].(map[string]any)["accounting_readiness"].([]any)) != 1 {
		t.Fatalf("preview: %d %v", pv.status, pv.body)
	}
	e.clock.Set(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	run := abc.do(http.MethodPost, base+"/night-audit/run", map[string]any{"business_date": "2026-09-30"})
	if run.status != 409 || run.body["code"] != "NIGHT_AUDIT_BLOCKED" || len(run.body["context"].(map[string]any)["blockers"].(map[string]any)["accounting_readiness"].([]any)) != 1 {
		t.Fatalf("run: %d %v", run.status, run.body)
	}
	if got := abc.do(http.MethodGet, base+"/business-days", nil); got.status != 200 {
		t.Fatalf("business days: %d", got.status)
	}

	// another tenant does not see the property
	if r := xyz.do(http.MethodGet, base+"/accounting/readiness", nil); r.status != 404 {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}
}
