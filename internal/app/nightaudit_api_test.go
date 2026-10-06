package app

import (
	"net/http"
	"testing"
	"time"
)

func TestNightAuditAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	var roomCode int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomCode = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "102", "initial_housekeeping_status": "CLEAN"})
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))
	walk := abc.doWith(http.MethodPost, base+"/walk-ins", map[string]any{"guest_id": mustInt(guest), "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-03", "adult_count": 2, "child_count": 0},
		map[string]string{"Idempotency-Key": "w-1"})
	if walk.status != http.StatusCreated {
		t.Fatalf("walk-in: %d %v", walk.status, walk.body)
	}
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-01", "adult_count": 2, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"})
	lineID := idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)})
	run := base + "/night-audit/run"

	// Preview: the unresolved arrival blocks.
	pv := abc.do(http.MethodGet, base+"/night-audit/preview", nil)
	bl, _ := pv.body["blockers"].(map[string]any)
	if pv.status != 200 || pv.body["can_run"] != false || pv.body["time_guard_ok"] != true || len(bl["unresolved_arrivals"].([]any)) != 1 ||
		len(bl["unresolved_departures"].([]any)) != 0 || bl["charge_errors"] == nil || pv.body["tonight_charges"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("preview: %d %v", pv.status, pv.body)
	}
	if r := abc.do(http.MethodPost, run, map[string]any{"business_date": "2026-09-30"}); r.status != 409 || r.body["code"] != "NIGHT_AUDIT_BLOCKED" ||
		r.body["context"].(map[string]any)["blockers"].(map[string]any)["unresolved_arrivals"] == nil {
		t.Fatalf("blocked: %d %v", r.status, r.body)
	}

	// Bulk no-show: confirm and the date are required, the set is exact.
	ns := base + "/night-audit/no-shows"
	if r := abc.do(http.MethodPost, ns, map[string]any{"business_date": "2026-09-30", "reservation_room_ids": []int64{mustInt(lineID)}}); r.status != 422 {
		t.Fatalf("confirm: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, ns, map[string]any{"business_date": "2026-09-30", "reservation_room_ids": []int64{999999}, "confirm": true}); r.status != 409 || r.body["code"] != "NO_SHOW_SET_CHANGED" {
		t.Fatalf("stale set: %d %v", r.status, r.body)
	}
	nr := abc.do(http.MethodPost, ns, map[string]any{"business_date": "2026-09-30", "reservation_room_ids": []int64{mustInt(lineID)}, "confirm": true, "reason": "no show"})
	if nr.status != 200 || len(nr.body["marked"].([]any)) != 1 || len(nr.body["remaining_blockers"].(map[string]any)["unresolved_arrivals"].([]any)) != 0 {
		t.Fatalf("no-show: %d %v", nr.status, nr.body)
	}

	// Run: validation, isolation, too early, then success.
	if r := abc.do(http.MethodPost, run, map[string]any{}); r.status != 422 {
		t.Fatalf("no date: %d", r.status)
	}
	if r := xyz.do(http.MethodPost, run, map[string]any{"business_date": "2026-09-30"}); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, run, map[string]any{"business_date": "2026-10-01"}); r.status != 409 || r.body["code"] != "BUSINESS_DATE_MISMATCH" {
		t.Fatalf("date mismatch: %d %v", r.status, r.body)
	}
	e.clock.Set(time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)) // 17:00 local
	if r := abc.do(http.MethodPost, run, map[string]any{"business_date": "2026-09-30"}); r.status != 409 || r.body["code"] != "NIGHT_AUDIT_TOO_EARLY" {
		t.Fatalf("too early: %d %v", r.status, r.body)
	}
	e.clock.Set(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	ok := abc.do(http.MethodPost, run, map[string]any{"business_date": "2026-09-30"})
	sum, _ := ok.body["summary"].(map[string]any)
	if ok.status != 200 || ok.body["closed_business_date"] != "2026-09-30" || ok.body["new_business_date"] != "2026-10-01" || ok.body["room_charges_posted"].(float64) != 1 ||
		sum["no_shows"].(float64) != 1 || sum["room_revenue"].(map[string]any)["net"] == nil || sum["rooms"].(map[string]any)["occupied"].(float64) != 1 {
		t.Fatalf("run: %d %v", ok.status, ok.body)
	}
	if bd := abc.do(http.MethodGet, base+"/business-date", nil); bd.body["business_date"] != "2026-10-01" {
		t.Fatalf("business date: %v", bd.body)
	}
}
