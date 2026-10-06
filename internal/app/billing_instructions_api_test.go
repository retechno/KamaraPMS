package app

import (
	"net/http"
	"testing"
)

// The billing instructions of a room over HTTP: set before check-in, the company folio opened by the check-in, the folio fields, the errors and the isolation of the tenants.
func TestBillingInstructionsAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	var roomCode, minibar int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		switch m := c.(map[string]any); m["code"] {
		case "ROOM":
			roomCode = int64(m["id"].(float64))
		case "MINIBAR":
			minibar = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "first_name": "Siti", "last_name": "Nurhaliza"}))
	company := idOf(abc.do(http.MethodPost, base+"/companies", map[string]any{"code": "ACME", "name": "Acme Corp", "credit_limit": "5000000", "payment_terms_days": 30}))

	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "adult_count": 2, "child_count": 0}}}, map[string]string{"Idempotency-Key": "res-1"})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	resID := idOf(res)
	lineID := idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)})
	url := base + "/reservations/" + resID + "/rooms/" + lineID + "/billing-instructions"

	if r := abc.do(http.MethodGet, url, nil); r.status != 200 || len(r.body["instructions"].([]any)) != 0 {
		t.Fatalf("empty: %d %v", r.status, r.body)
	}
	put := func(body map[string]any) response { return abc.do(http.MethodPut, url, body) }

	// errors
	if r := put(map[string]any{"instructions": []map[string]any{{"scope": "ALL", "company_id": mustInt(company)}, {"scope": "ALL", "company_id": mustInt(company)}}}); r.status != 422 {
		t.Fatalf("duplicate: %d %v", r.status, r.body)
	}
	if r := put(map[string]any{"instructions": []map[string]any{{"scope": "CHARGE_CODE", "company_id": mustInt(company)}}}); r.status != 422 || fieldsOf(r)["instructions[0].charge_code_id"] != "REQUIRED" {
		t.Fatalf("no code: %d %v", r.status, r.body)
	}
	if r := put(map[string]any{"instructions": []map[string]any{{"scope": "ALL", "company_id": 999999}}}); r.status != 404 || r.body["code"] != "COMPANY_NOT_FOUND" {
		t.Fatalf("unknown company: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reservations/"+resID+"/rooms/999999/billing-instructions", nil); r.status != 404 || r.body["code"] != "RESERVATION_ROOM_NOT_FOUND" {
		t.Fatalf("unknown line: %d %v", r.status, r.body)
	}

	// set: the room and the minibar go to the company
	ok := put(map[string]any{"instructions": []map[string]any{{"scope": "ROOM", "company_id": mustInt(company)}, {"scope": "CHARGE_CODE", "charge_code_id": minibar, "company_id": mustInt(company)}}})
	list := ok.body["instructions"].([]any)
	if ok.status != 200 || len(list) != 2 || list[0].(map[string]any)["scope"] != "ROOM" || list[0].(map[string]any)["company_name"] != "Acme Corp" || list[1].(map[string]any)["charge_code"] != "MINIBAR" {
		t.Fatalf("set: %d %v", ok.status, ok.body)
	}
	if r := abc.do(http.MethodGet, url, nil); r.status != 200 || len(r.body["instructions"].([]any)) != 2 {
		t.Fatalf("get: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, url, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}

	// the check-in opens the company folio with the guest folio, and the folio fields say who pays
	ci := abc.doWith(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/check-in", map[string]any{
		"version": res.body["version"], "room_id": mustInt(r101), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}, map[string]string{"Idempotency-Key": "ci-1"})
	if ci.status != 201 {
		t.Fatalf("check-in: %d %v", ci.status, ci.body)
	}
	stay := idOf(response{body: ci.body["stay"].(map[string]any)})
	fl := abc.do(http.MethodGet, base+"/folios?stay_id="+stay, nil).body["data"].([]any)
	if len(fl) != 2 {
		t.Fatalf("folios of the stay: %v", fl)
	}
	g, c := fl[0].(map[string]any), fl[1].(map[string]any)
	if g["folio_type"] != "GUEST" || g["bill_to_company_id"] != nil || c["folio_type"] != "COMPANY" || c["bill_to_company_id"] != float64(mustInt(company)) {
		t.Fatalf("folio types: %v %v", g, c)
	}
	one := abc.do(http.MethodGet, base+"/folios/"+idOf(response{body: c}), nil)
	if one.status != 200 || one.body["bill_to_company_name"] != "Acme Corp" {
		t.Fatalf("company folio: %d %v", one.status, one.body)
	}
	// the company folio only transfers to its company
	abc.doWith(http.MethodPost, base+"/folios/"+idOf(response{body: c})+"/charges", map[string]any{"charge_code_id": minibar, "quantity": "1", "unit_price": "400000"}, map[string]string{"Idempotency-Key": "c1"})
	other := idOf(abc.do(http.MethodPost, base+"/companies", map[string]any{"code": "OTHER", "name": "Other", "credit_limit": "5000000", "payment_terms_days": 30}))
	if r := abc.doWith(http.MethodPost, base+"/folios/"+idOf(response{body: c})+"/city-ledger-transfers", map[string]any{"company_id": mustInt(other), "amount": "1000"}, map[string]string{"Idempotency-Key": "t1"}); r.status != 409 || r.body["code"] != "TRANSFER_COMPANY_MISMATCH" {
		t.Fatalf("mismatch: %d %v", r.status, r.body)
	}
}
