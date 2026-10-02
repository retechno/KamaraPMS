package app

import (
	"net/http"
	"testing"
)

func TestStayOperationsAPIFlow(t *testing.T) {
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
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	r102 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "102", "initial_housekeeping_status": "CLEAN"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))
	companion := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Companion"}))
	walk := abc.doWith(http.MethodPost, base+"/walk-ins", map[string]any{"guest_id": mustInt(guest), "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-03", "adult_count": 2, "child_count": 0},
		map[string]string{"Idempotency-Key": "w-1"})
	if walk.status != http.StatusCreated {
		t.Fatalf("walk-in: %d %v", walk.status, walk.body)
	}
	stay := walk.body["stay"].(map[string]any)
	stayID := idOf(response{body: stay})
	folioID := idOf(response{body: walk.body["folio"].(map[string]any)})
	version := func() any {
		return abc.do(http.MethodGet, base+"/stays/"+stayID, nil).body["stay"].(map[string]any)["version"]
	}

	// Move.
	mv := abc.do(http.MethodPost, base+"/stays/"+stayID+"/move", map[string]any{"version": version(), "room_id": mustInt(r102), "reason": "noise"})
	if mv.status != 200 || mv.body["new_segment"].(map[string]any)["room_number"] != "102" || mv.body["closed_segment"].(map[string]any)["room_number"] != "101" {
		t.Fatalf("move: %d %v", mv.status, mv.body)
	}
	if r := abc.do(http.MethodPost, base+"/stays/"+stayID+"/move", map[string]any{"version": 1, "room_id": mustInt(r101), "reason": "x"}); r.status != 409 || r.body["code"] != "VERSION_CONFLICT" {
		t.Fatalf("stale move: %d %v", r.status, r.body)
	}

	// Guests.
	g := abc.do(http.MethodPost, base+"/stays/"+stayID+"/guests", map[string]any{"guest_id": mustInt(companion)})
	if g.status != http.StatusCreated || len(g.body["guests"].([]any)) != 1 {
		t.Fatalf("add guest: %d %v", g.status, g.body)
	}
	if r := abc.do(http.MethodPost, base+"/stays/"+stayID+"/guests", map[string]any{"guest_id": mustInt(companion)}); r.status != 409 || r.body["code"] != "GUEST_ALREADY_ON_STAY" {
		t.Fatalf("duplicate guest: %d %v", r.status, r.body)
	}

	// Change departure.
	cd := abc.do(http.MethodPost, base+"/stays/"+stayID+"/change-departure", map[string]any{"version": version(), "departure_date": "2026-10-04"})
	if cd.status != 200 || cd.body["departure_date"] != "2026-10-04" {
		t.Fatalf("extend: %d %v", cd.status, cd.body)
	}
	if r := abc.do(http.MethodPost, base+"/stays/"+stayID+"/change-departure", map[string]any{"version": version(), "departure_date": "2026-09-30"}); r.status != 422 {
		t.Fatalf("shorten to today: %d %v", r.status, r.body)
	}
	due := abc.do(http.MethodGet, base+"/stays?status=OPEN&departure_until=2026-10-03", nil)
	if due.status != 200 || len(due.body["data"].([]any)) != 0 {
		t.Fatalf("departures: %d %v", due.status, due.body)
	}
	if r := abc.do(http.MethodPost, base+"/stays/"+stayID+"/change-departure", map[string]any{"version": version(), "departure_date": "2026-10-01"}); r.status != 200 {
		t.Fatalf("shorten: %d %v", r.status, r.body)
	}
	due = abc.do(http.MethodGet, base+"/stays?status=OPEN&departure_until=2026-10-01", nil)
	if due.status != 200 || len(due.body["data"].([]any)) != 1 {
		t.Fatalf("departures: %d %v", due.status, due.body)
	}

	// Check-out: key required; the folio is not balanced; pay; done; retry.
	out := base + "/stays/" + stayID + "/check-out"
	if r := abc.do(http.MethodPost, out, map[string]any{"version": version()}); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	v := version()
	key := map[string]string{"Idempotency-Key": "co-1"}
	if r := abc.doWith(http.MethodPost, out, map[string]any{"version": v}, key); r.status != 409 || r.body["code"] != "FOLIO_NOT_BALANCED" || r.body["context"].(map[string]any)["folios"] == nil {
		t.Fatalf("unbalanced: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "1000000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "pay-1"}); r.status != http.StatusCreated {
		t.Fatalf("payment: %d %v", r.status, r.body)
	}
	if r := xyz.doWith(http.MethodPost, out, map[string]any{"version": v}, key); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	co := abc.doWith(http.MethodPost, out, map[string]any{"version": v}, key)
	if co.status != 200 || co.body["stay"].(map[string]any)["status"] != "CHECKED_OUT" || len(co.body["posted_room_charges"].([]any)) != 1 || co.body["housekeeping"] != "DIRTY" ||
		co.body["folios"].([]any)[0].(map[string]any)["status"] != "CLOSED" {
		t.Fatalf("check-out: %d %v", co.status, co.body)
	}
	if r := abc.doWith(http.MethodPost, out, map[string]any{"version": v}, key); r.status != 200 || r.body["stay"].(map[string]any)["status"] != "CHECKED_OUT" {
		t.Fatalf("retry: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, out, map[string]any{"version": 99}, key); r.status != 409 {
		t.Fatalf("stale: %d %v", r.status, r.body)
	}
}
