package app

import (
	"net/http"
	"testing"
)

// Audit F-08 over HTTP: a manual cancellation fee, its stable errors, and the second request for the same cancellation.
func TestReservationFeeAPI(t *testing.T) {
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
	abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"})
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-10-02", "departure_date": "2026-10-04", "adult_count": 2, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"})
	id := idOf(res)
	fees := base + "/reservations/" + id + "/fees"
	fee := map[string]any{"type": "CANCEL_FEE", "amount": "500000", "reason": "late cancellation"}

	if r := abc.do(http.MethodPost, fees, fee); r.status != 409 || r.body["code"] != "RESERVATION_NOT_CANCELLED" {
		t.Fatalf("not cancelled yet: %d %v", r.status, r.body)
	}
	version := res.body["version"]
	if r := abc.do(http.MethodPost, base+"/reservations/"+id+"/cancel", map[string]any{"version": version, "reason": "guest cancelled"}); r.status != 200 {
		t.Fatalf("cancel: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, fees, map[string]any{"type": "CANCEL_FEE", "amount": "0", "reason": "x"}); r.status != 422 || fieldsOf(r)["amount"] != "INVALID_AMOUNT" {
		t.Fatalf("zero amount: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, fees, map[string]any{"type": "MINIBAR", "amount": "100", "reason": "x"}); r.status != 422 || fieldsOf(r)["type"] != "INVALID_VALUE" {
		t.Fatalf("another charge code is not accepted: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodPost, fees, fee); r.status != 404 {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}
	ok := abc.do(http.MethodPost, fees, fee)
	item, _ := ok.body["item"].(map[string]any)
	if ok.status != http.StatusCreated || item["debit"] != "500000" || item["group_code"] != "A" || ok.body["folio_created"] != true || item["revenue_account_code"] != "4510" {
		t.Fatalf("fee: %d %v", ok.status, ok.body)
	}
	if r := abc.do(http.MethodPost, fees, map[string]any{"type": "CANCEL_FEE", "amount": "1", "reason": "again"}); r.status != 409 || r.body["code"] != "FEE_ALREADY_POSTED" {
		t.Fatalf("the same cancellation twice: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, fees, map[string]any{"type": "CANCEL_FEE", "amount": "1", "reason": "x", "charge_code_id": 5}); r.status != 422 && r.status != 400 {
		t.Fatalf("an unknown field is refused: %d %v", r.status, r.body)
	}
}
