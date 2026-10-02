package app

import (
	"net/http"
	"testing"
)

func TestReservationsAPIFlow(t *testing.T) {
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
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 1, "max_occupancy": 3, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "101"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	if r := abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-10", "amount": "1000000"}); r.status != 200 {
		t.Fatalf("rates: %d %v", r.status, r.body)
	}
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "first_name": "Siti", "last_name": "Nurhaliza"}))

	// Availability search.
	av := abc.do(http.MethodGet, base+"/availability?arrival=2026-10-02&departure=2026-10-04&adults=2", nil)
	types := av.body["room_types"].([]any)
	if av.status != 200 || len(av.body["nights"].([]any)) != 2 || len(types) != 1 {
		t.Fatalf("availability: %d %v", av.status, av.body)
	}
	first := types[0].(map[string]any)
	plans := first["rate_plans"].([]any)
	if first["available_min"] != float64(1) || first["fits_occupancy"] != true || len(plans) != 1 ||
		plans[0].(map[string]any)["estimate"].(map[string]any)["total"] != "2000000" {
		t.Fatalf("availability body: %v", first)
	}
	if r := abc.do(http.MethodGet, base+"/availability?arrival=2026-10-04&departure=2026-10-02", nil); r.status != 422 {
		t.Fatalf("bad range: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/availability?departure=2026-10-02", nil); r.status != 422 || fieldsOf(r)["arrival"] != "REQUIRED" {
		t.Fatalf("missing arrival: %d %v", r.status, r.body)
	}

	// Create: the Idempotency-Key is required; the same key replays.
	body := map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-10-02", "departure_date": "2026-10-04", "adult_count": 2, "child_count": 0},
	}}
	if r := abc.do(http.MethodPost, base+"/reservations", body); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	key := map[string]string{"Idempotency-Key": "abc-123"}
	created := abc.doWith(http.MethodPost, base+"/reservations", body, key)
	if created.status != http.StatusCreated || created.body["status"] != "CONFIRMED" || created.body["display_status"] != "CONFIRMED" ||
		created.body["confirmation_number"] != "RES000001" || created.body["arrival_date"] != "2026-10-02" || created.body["version"] != float64(2) {
		t.Fatalf("create: %d %v", created.status, created.body)
	}
	resID := idOf(created)
	line := created.body["rooms"].([]any)[0].(map[string]any)
	lineID := idOf(response{body: line})
	if len(line["nightly_rates"].([]any)) != 2 || line["room_type_code"] != "DLX" || line["estimate"].(map[string]any)["total"] != "2000000" {
		t.Fatalf("line: %v", line)
	}
	if r := abc.doWith(http.MethodPost, base+"/reservations", body, key); r.status != http.StatusCreated || idOf(r) != resID {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}
	body["source"] = "EMAIL"
	if r := abc.doWith(http.MethodPost, base+"/reservations", body, key); r.status != 422 || r.body["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reused key: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/reservations", body, map[string]string{"Idempotency-Key": "second"}); r.status != 409 || r.body["code"] != "ROOM_TYPE_NOT_AVAILABLE" {
		t.Fatalf("sold out: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"source": "PHONE", "surprise": 1}, map[string]string{"Idempotency-Key": "k9"}); r.status != 422 || fieldsOf(r)["surprise"] != "UNKNOWN_FIELD" {
		t.Fatalf("unknown field: %d %v", r.status, r.body)
	}

	// Read, list, patch, free rooms, assign.
	res := abc.do(http.MethodGet, base+"/reservations/"+resID, nil)
	if res.status != 200 || res.body["guest"].(map[string]any)["last_name"] != "Nurhaliza" || len(res.body["folios"].([]any)) != 0 {
		t.Fatalf("get: %d %v", res.status, res.body)
	}
	if r := abc.do(http.MethodGet, base+"/reservations/999999", nil); r.status != 404 || r.body["code"] != "RESERVATION_NOT_FOUND" {
		t.Fatalf("missing: %d %v", r.status, r.body)
	}
	list := abc.do(http.MethodGet, base+"/reservations?status=CONFIRMED&arrival_from=2026-10-01&q=nurhal&limit=5", nil)
	if list.status != 200 || len(list.body["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", list.status, list.body)
	}
	if r := abc.do(http.MethodGet, base+"/reservations?status=WHATEVER", nil); r.status != 422 {
		t.Fatalf("bad status filter: %d", r.status)
	}
	patched := abc.do(http.MethodPatch, base+"/reservations/"+resID, map[string]any{"version": 2, "remarks": "quiet room"})
	if patched.status != 200 || patched.body["remarks"] != "quiet room" || patched.body["version"] != float64(3) {
		t.Fatalf("patch: %d %v", patched.status, patched.body)
	}
	if r := abc.do(http.MethodPatch, base+"/reservations/"+resID, map[string]any{"version": 2, "remarks": "stale"}); r.status != 409 || r.body["code"] != "VERSION_CONFLICT" {
		t.Fatalf("stale: %d %v", r.status, r.body)
	}
	free := abc.do(http.MethodGet, base+"/availability/rooms?room_type_id="+dlx+"&arrival=2026-10-02&departure=2026-10-04", nil)
	if free.status != 200 || len(free.body["data"].([]any)) != 1 {
		t.Fatalf("free rooms: %d %v", free.status, free.body)
	}
	assigned := abc.do(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/assign-room", map[string]any{"version": 3, "room_id": mustInt(r101)})
	if assigned.status != 200 || assigned.body["rooms"].([]any)[0].(map[string]any)["room_number"] != "101" {
		t.Fatalf("assign: %d %v", assigned.status, assigned.body)
	}
	free = abc.do(http.MethodGet, base+"/availability/rooms?room_type_id="+dlx+"&arrival=2026-10-02&departure=2026-10-04", nil)
	if len(free.body["data"].([]any)) != 0 {
		t.Fatalf("assigned room is not free: %v", free.body)
	}
	if r := abc.do(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/unassign-room", map[string]any{"version": 4}); r.status != 200 {
		t.Fatalf("unassign: %d %v", r.status, r.body)
	}
	amended := abc.do(http.MethodPatch, base+"/reservations/"+resID+"/rooms/"+lineID, map[string]any{"version": 5, "departure_date": "2026-10-05"})
	if amended.status != 200 || amended.body["departure_date"] != "2026-10-05" || len(amended.body["rooms"].([]any)[0].(map[string]any)["nightly_rates"].([]any)) != 3 {
		t.Fatalf("amend: %d %v", amended.status, amended.body)
	}

	tape := abc.do(http.MethodGet, base+"/tape-chart?from=2026-10-01&to=2026-10-08", nil)
	if tape.status != 200 || len(tape.body["rooms"].([]any)) != 1 || len(tape.body["unassigned"].([]any)) != 1 {
		t.Fatalf("tape chart: %d %v", tape.status, tape.body)
	}
	if r := abc.do(http.MethodGet, base+"/tape-chart?from=2026-10-01&to=2027-10-01", nil); r.status != 422 {
		t.Fatalf("tape window: %d %v", r.status, r.body)
	}

	// Cancel needs a reason; the response carries the folio fields; reinstate brings it back.
	if r := abc.do(http.MethodPost, base+"/reservations/"+resID+"/cancel", map[string]any{"version": 6}); r.status != 422 || fieldsOf(r)["reason"] != "REQUIRED" {
		t.Fatalf("reason: %d %v", r.status, r.body)
	}
	cancelled := abc.do(http.MethodPost, base+"/reservations/"+resID+"/cancel", map[string]any{"version": 6, "reason": "guest request"})
	if cancelled.status != 200 || cancelled.body["reservation"].(map[string]any)["status"] != "CANCELLED" || cancelled.body["folio_balance"] != "0" ||
		cancelled.body["requires_folio_resolution"] != false {
		t.Fatalf("cancel: %d %v", cancelled.status, cancelled.body)
	}
	back := abc.do(http.MethodPost, base+"/reservations/"+resID+"/reinstate", map[string]any{"version": 7})
	if back.status != 200 || back.body["status"] != "CONFIRMED" {
		t.Fatalf("reinstate: %d %v", back.status, back.body)
	}
	ns := abc.do(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/no-show", map[string]any{"version": 8})
	if ns.status != 409 || ns.body["code"] != "ARRIVAL_NOT_DUE" {
		t.Fatalf("no-show before arrival: %d %v", ns.status, ns.body)
	}
	added := abc.do(http.MethodPost, base+"/reservations/"+resID+"/rooms", map[string]any{"version": 8, "room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan),
		"arrival_date": "2026-10-08", "departure_date": "2026-10-09", "adult_count": 1, "child_count": 0})
	if added.status != http.StatusCreated || len(added.body["rooms"].([]any)) != 2 {
		t.Fatalf("add room: %d %v", added.status, added.body)
	}
	lineCancel := abc.do(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/cancel", map[string]any{"version": 9, "reason": "shorter trip"})
	if lineCancel.status != 200 || lineCancel.body["reservation"].(map[string]any)["status"] != "CONFIRMED" {
		t.Fatalf("cancel room: %d %v", lineCancel.status, lineCancel.body)
	}

	// Isolation: another tenant cannot see the property or its reservations.
	if r := xyz.do(http.MethodGet, base+"/reservations/"+resID, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, base+"/availability?arrival=2026-10-02&departure=2026-10-04", nil); r.status != 404 {
		t.Fatalf("foreign availability: %d %v", r.status, r.body)
	}
}
