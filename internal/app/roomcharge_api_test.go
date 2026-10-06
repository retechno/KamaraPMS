package app

import (
	"net/http"
	"testing"
)

func TestRoomChargeAPIFlow(t *testing.T) {
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
	svc := idOf(abc.do(http.MethodPost, base+"/service-charges", map[string]any{"code": "SVC", "name": "Service", "rate": "10"}))
	vat := idOf(abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "VAT", "name": "VAT", "rate": "11", "tax_on_service": true}))
	abc.do(http.MethodPut, base+"/charge-codes/"+itoa(roomCode)+"/rules", map[string]any{
		"taxes": []map[string]any{{"tax_id": mustInt(vat), "sequence": 1}}, "service_charges": []map[string]any{{"service_charge_id": mustInt(svc), "sequence": 1}},
	})
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	walk := abc.doWith(http.MethodPost, base+"/walk-ins", map[string]any{"new_guest": map[string]any{"last_name": "Walker"}, "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-02", "adult_count": 1, "child_count": 0},
		map[string]string{"Idempotency-Key": "w1"})
	if walk.status != 201 {
		t.Fatalf("walk-in: %d %v", walk.status, walk.body)
	}
	folioID := itoa(int64(walk.body["folio"].(map[string]any)["id"].(float64)))

	pv := abc.do(http.MethodPost, base+"/night-audit/room-charges/preview", map[string]any{"business_date": "2026-09-30"})
	if pv.status != 200 || len(pv.body["items"].([]any)) != 1 || pv.body["totals"].(map[string]any)["ready_count"] != float64(1) || pv.body["totals"].(map[string]any)["ready_total"] != "1221000" {
		t.Fatalf("preview: %d %v", pv.status, pv.body)
	}
	item := pv.body["items"].([]any)[0].(map[string]any)
	if item["status"] != "READY" || item["room_number"] != "101" || item["service_charge"] != "100000" || item["tax"] != "121000" || item["total"] != "1221000" || item["charge_code"] != "ROOM" {
		t.Fatalf("item: %v", item)
	}
	if r := abc.do(http.MethodPost, base+"/night-audit/room-charges/preview", map[string]any{}); r.status != 422 || fieldsOf(r)["business_date"] != "REQUIRED" {
		t.Fatalf("missing date: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/night-audit/room-charges/preview", map[string]any{"business_date": "2026-10-01"}); r.status != 409 || r.body["code"] != "BUSINESS_DATE_MISMATCH" {
		t.Fatalf("stale date: %d %v", r.status, r.body)
	}

	if r := abc.do(http.MethodPost, base+"/night-audit/room-charges", map[string]any{"business_date": "2026-09-30"}); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	key := map[string]string{"Idempotency-Key": "rc-1"}
	post := abc.doWith(http.MethodPost, base+"/night-audit/room-charges", map[string]any{"business_date": "2026-09-30"}, key)
	res := post.body["results"].([]any)
	if post.status != 200 || len(res) != 1 || res[0].(map[string]any)["status"] != "POSTED" || res[0].(map[string]any)["total"] != "1221000" || res[0].(map[string]any)["folio_item_id"] == nil {
		t.Fatalf("post: %d %v", post.status, post.body)
	}
	if rv := post.body["revalidation"].(map[string]any); rv["ready"] != float64(0) || len(rv["errors"].([]any)) != 0 || len(rv["invalid"].([]any)) != 0 {
		t.Fatalf("revalidation: %v", rv)
	}
	again := abc.doWith(http.MethodPost, base+"/night-audit/room-charges", map[string]any{"business_date": "2026-09-30", "stay_ids": []int64{mustInt(idOf(response{body: walk.body["stay"].(map[string]any)}))}}, key)
	if again.status != 200 || again.body["results"].([]any)[0].(map[string]any)["status"] != "ALREADY_POSTED" {
		t.Fatalf("again: %d %v", again.status, again.body)
	}
	f := abc.do(http.MethodGet, base+"/folios/"+folioID, nil)
	if f.body["balance"] != "1221000" || len(f.body["items"].([]any)) != 1 || f.body["items"].([]any)[0].(map[string]any)["room_number"] != "101" {
		t.Fatalf("folio: %v", f.body)
	}
	// the stay detail now shows the night as charged
	stay := abc.do(http.MethodGet, base+"/stays/"+idOf(response{body: walk.body["stay"].(map[string]any)}), nil)
	if stay.body["nightly_rates"].([]any)[0].(map[string]any)["posted"] != true {
		t.Fatalf("stay nights: %v", stay.body["nightly_rates"])
	}
	if r := xyz.do(http.MethodPost, base+"/night-audit/room-charges/preview", map[string]any{"business_date": "2026-09-30"}); r.status != 404 {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
}
