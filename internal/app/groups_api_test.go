package app

import (
	"net/http"
	"testing"
)

// Transaction Group over HTTP (docs/architecture/20-transaction-group.md): the move of a line, the payment variant, and what a caller without the right or from another tenant gets.
func TestTransactionGroupAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	var other int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "OTHER" {
			other = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	var roomCode int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomCode = int64(m["id"].(float64))
		}
	}
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))
	walk := abc.doWith(http.MethodPost, base+"/walk-ins", map[string]any{"guest_id": mustInt(guest), "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-03", "adult_count": 2, "child_count": 0},
		map[string]string{"Idempotency-Key": "w-1"})
	folio := idOf(response{body: walk.body["folio"].(map[string]any)})
	ch := abc.doWith(http.MethodPost, base+"/folios/"+folio+"/charges", map[string]any{"charge_code_id": other, "quantity": "1", "unit_price": "100000"}, map[string]string{"Idempotency-Key": "c1"})
	if ch.status != http.StatusCreated {
		t.Fatalf("charge: %d %v", ch.status, ch.body)
	}
	item := ch.body["item"].(map[string]any)
	if item["group_code"] != "A" {
		t.Fatalf("a new line is in group A: %v", item["group_code"])
	}
	pay := abc.doWith(http.MethodPost, base+"/folios/"+folio+"/payments", map[string]any{"amount": "40000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "p1"})
	if pay.status != http.StatusCreated || pay.body["payment"].(map[string]any)["group_code"] != "A" {
		t.Fatalf("payment: %d %v", pay.status, pay.body)
	}
	itemID := itoaID(int64(item["id"].(float64)))
	payID := itoaID(int64(pay.body["payment"].(map[string]any)["id"].(float64)))
	balance := func() any { return abc.do(http.MethodGet, base+"/folios/"+folio, nil).body["balance"] }
	before := balance()

	r := abc.do(http.MethodPatch, base+"/folio-items/"+itemID+"/group", map[string]any{"group_code": "B"})
	if r.status != 200 || r.body["group_code"] != "B" || r.body["previous_group_code"] != "A" || r.body["changed"] != true {
		t.Fatalf("move: %d %v", r.status, r.body)
	}
	r = abc.do(http.MethodPatch, base+"/payments/"+payID+"/group", map[string]any{"group_code": "C"})
	if r.status != 200 || r.body["group_code"] != "C" || r.body["payment_id"] == nil {
		t.Fatalf("payment move: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/folio-items/"+itemID+"/group", map[string]any{"group_code": "B"}); r.status != 200 || r.body["changed"] != false {
		t.Fatalf("the same group again: %d %v", r.status, r.body)
	}
	// the folio shows the groups on its lines and the payment on its list, and the balance is the balance of the whole folio
	fo := abc.do(http.MethodGet, base+"/folios/"+folio, nil)
	groups := map[string]string{}
	for _, it := range fo.body["items"].([]any) {
		m := it.(map[string]any)
		groups[itoaID(int64(m["id"].(float64)))] = m["group_code"].(string)
	}
	if groups[itemID] != "B" || fo.body["balance"] != before {
		t.Fatalf("folio: groups %v balance %v (was %v)", groups, fo.body["balance"], before)
	}
	list := abc.do(http.MethodGet, base+"/payments", nil)
	if list.status != 200 || list.body["data"].([]any)[0].(map[string]any)["group_code"] != "C" {
		t.Fatalf("payment list: %d %v", list.status, list.body)
	}

	// invalid
	if r := abc.do(http.MethodPatch, base+"/folio-items/"+itemID+"/group", map[string]any{"group_code": "Z"}); r.status != 422 || fieldsOf(r)["group_code"] != "INVALID_VALUE" {
		t.Fatalf("invalid group: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/folio-items/"+itemID+"/group", map[string]any{"group_code": "B", "amount": "1"}); r.status != 422 && r.status != 400 {
		t.Fatalf("an unknown field is refused: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/folio-items/999999/group", map[string]any{"group_code": "B"}); r.status != 404 || r.body["code"] != "FOLIO_ITEM_NOT_FOUND" {
		t.Fatalf("missing line: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/payments/999999/group", map[string]any{"group_code": "B"}); r.status != 404 || r.body["code"] != "PAYMENT_NOT_FOUND" {
		t.Fatalf("missing payment: %d %v", r.status, r.body)
	}
	// another tenant does not see the property at all
	if r := xyz.do(http.MethodPatch, base+"/folio-items/"+itemID+"/group", map[string]any{"group_code": "D"}); r.status != 404 {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}
	if abc.do(http.MethodGet, base+"/folios/"+folio, nil).body["items"] == nil || groups[itemID] != "B" {
		t.Fatal("unchanged")
	}
}
