package app

import (
	"net/http"
	"testing"
)

func TestFrontDeskAPIFlow(t *testing.T) {
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
	r102 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "102"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "adult_count": 2, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	resID := idOf(res)
	line := res.body["rooms"].([]any)[0].(map[string]any)
	lineID := idOf(response{body: line})

	// Arrivals.
	arr := abc.do(http.MethodGet, base+"/arrivals", nil)
	if arr.status != 200 || len(arr.body["data"].([]any)) != 1 || arr.body["data"].([]any)[0].(map[string]any)["confirmation_number"] == nil {
		t.Fatalf("arrivals: %d %v", arr.status, arr.body)
	}

	// Check-in: key required, a dirty room is refused with what is needed, a clean room works.
	path := base + "/reservations/" + resID + "/rooms/" + lineID + "/check-in"
	body := func(room string) map[string]any {
		return map[string]any{"version": res.body["version"], "room_id": mustInt(room), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}
	}
	if r := abc.do(http.MethodPost, path, body(r101)); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	r := abc.doWith(http.MethodPost, path, body(r102), map[string]string{"Idempotency-Key": "ci-0"})
	if r.status != 409 || r.body["code"] != "ROOM_NOT_READY" || r.body["context"].(map[string]any)["current"] != "DIRTY" || r.body["context"].(map[string]any)["required"] != "CLEAN" {
		t.Fatalf("dirty room: %d %v", r.status, r.body)
	}
	ci := abc.doWith(http.MethodPost, path, body(r101), map[string]string{"Idempotency-Key": "ci-1"})
	stay := ci.body["stay"].(map[string]any)
	if ci.status != http.StatusCreated || stay["status"] != "OPEN" || ci.body["stay_room"].(map[string]any)["room_number"] != "101" || ci.body["folio"].(map[string]any)["folio_number"] == nil {
		t.Fatalf("check-in: %d %v", ci.status, ci.body)
	}
	if r := abc.doWith(http.MethodPost, path, body(r101), map[string]string{"Idempotency-Key": "ci-1"}); r.status != http.StatusCreated || int64(r.body["stay"].(map[string]any)["id"].(float64)) != int64(stay["id"].(float64)) {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}
	stayID := idOf(response{body: stay})

	// Stays.
	list := abc.do(http.MethodGet, base+"/stays?status=OPEN", nil)
	if list.status != 200 || len(list.body["data"].([]any)) != 1 || list.body["data"].([]any)[0].(map[string]any)["room_number"] != "101" {
		t.Fatalf("stays: %d %v", list.status, list.body)
	}
	if r := abc.do(http.MethodGet, base+"/stays?status=WHATEVER", nil); r.status != 422 {
		t.Fatalf("bad filter: %d", r.status)
	}
	detail := abc.do(http.MethodGet, base+"/stays/"+stayID, nil)
	if detail.status != 200 || len(detail.body["segments"].([]any)) != 1 || len(detail.body["nightly_rates"].([]any)) != 2 || detail.body["nightly_rates"].([]any)[0].(map[string]any)["posted"] != false {
		t.Fatalf("stay: %d %v", detail.status, detail.body)
	}
	if r := abc.do(http.MethodGet, base+"/stays/999999", nil); r.status != 404 || r.body["code"] != "STAY_NOT_FOUND" {
		t.Fatalf("missing stay: %d %v", r.status, r.body)
	}

	// Reverse the check-in, then walk in.
	rv := stay["version"]
	if r := abc.do(http.MethodPost, base+"/stays/"+stayID+"/reverse-check-in", map[string]any{"version": rv}); r.status != 422 {
		t.Fatalf("reason: %d %v", r.status, r.body)
	}
	back := abc.do(http.MethodPost, base+"/stays/"+stayID+"/reverse-check-in", map[string]any{"version": rv, "reason": "wrong guest"})
	if back.status != 200 || back.body["stay"].(map[string]any)["status"] != "CANCELLED" {
		t.Fatalf("reverse: %d %v", back.status, back.body)
	}
	walk := map[string]any{"new_guest": map[string]any{"last_name": "Walker"}, "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-01", "adult_count": 1, "child_count": 0}
	if r := abc.doWith(http.MethodPost, base+"/walk-ins", walk, map[string]string{"Idempotency-Key": "w-0"}); r.status != 409 || r.body["code"] != "ROOM_NOT_AVAILABLE" {
		t.Fatalf("101 is held by the reservation again: %d %v", r.status, r.body)
	}
	walk["room_id"] = mustInt(r102)
	if r := abc.doWith(http.MethodPost, base+"/walk-ins", walk, map[string]string{"Idempotency-Key": "w-0"}); r.status != 409 || r.body["code"] != "ROOM_NOT_READY" {
		t.Fatalf("102 is dirty: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/rooms/"+r102+"/housekeeping", map[string]any{"status": "CLEAN"}); r.status != 200 {
		t.Fatalf("clean: %d %v", r.status, r.body)
	}
	w := abc.doWith(http.MethodPost, base+"/walk-ins", walk, map[string]string{"Idempotency-Key": "w-1"})
	if w.status != http.StatusCreated || w.body["reservation"].(map[string]any)["confirmation_number"] == nil || w.body["stay"].(map[string]any)["status"] != "OPEN" {
		t.Fatalf("walk-in: %d %v", w.status, w.body)
	}
	if r := abc.do(http.MethodPost, base+"/walk-ins", walk); r.status != 400 {
		t.Fatalf("walk-in without key: %d", r.status)
	}

	// Isolation.
	if r := xyz.do(http.MethodGet, base+"/stays/"+stayID, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
}
