package app

import (
	"net/http"
	"strings"
	"testing"
)

// TestCorporateAPIFlow walks the corporate flow over HTTP: a company, a group, rooms booked into it, a folio
// transferred to the company's city ledger account, a receipt, a void, the statement and its PDF.
func TestCorporateAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}

	// setup: a room, a rate and a guest
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

	// companies
	if r := abc.do(http.MethodPost, base+"/companies", map[string]any{"code": "ACME", "name": "Acme Corp", "credit_limit": "5000000", "payment_terms_days": 30}); r.status != 201 || r.body["credit_limit"] != "5000000" {
		t.Fatalf("create company: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/companies", map[string]any{"code": "acme", "name": "Again"}); r.status != 409 || r.body["code"] != "CODE_TAKEN" {
		t.Fatalf("duplicate company: %d %v", r.status, r.body)
	}
	company := idOf(response{body: abc.do(http.MethodGet, base+"/companies?q=acme", nil).body["data"].([]any)[0].(map[string]any)})
	if r := abc.do(http.MethodGet, base+"/companies/"+company, nil); r.status != 200 || r.body["name"] != "Acme Corp" {
		t.Fatalf("get company: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, base+"/companies/"+company, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}

	// a group of that company and a reservation in it
	grp := abc.do(http.MethodPost, base+"/groups", map[string]any{"code": "CONF", "name": "Annual conference", "company_id": mustInt(company), "arrival_date": "2026-09-30", "departure_date": "2026-10-04"})
	if grp.status != 201 || grp.body["company_name"] != "Acme Corp" {
		t.Fatalf("create group: %d %v", grp.status, grp.body)
	}
	group := idOf(grp)
	if r := abc.do(http.MethodPost, base+"/groups", map[string]any{"code": "BAD", "name": "x", "arrival_date": "2026-10-04", "departure_date": "2026-10-04"}); r.status != 422 {
		t.Fatalf("group dates: %d %v", r.status, r.body)
	}
	room := func(arrival, departure string) []map[string]any {
		return []map[string]any{{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": arrival, "departure_date": departure, "adult_count": 2, "child_count": 0}}
	}
	if r := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "booking_group_id": mustInt(group), "rooms": room("2026-09-30", "2026-10-06")}, map[string]string{"Idempotency-Key": "res-out"}); r.status != 422 {
		t.Fatalf("outside the group: %d %v", r.status, r.body)
	}
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "booking_group_id": mustInt(group), "rooms": room("2026-09-30", "2026-10-02")}, map[string]string{"Idempotency-Key": "res-1"})
	if res.status != 201 || res.body["group_code"] != "CONF" || res.body["company_name"] != "Acme Corp" {
		t.Fatalf("reservation in the group: %d %v", res.status, res.body)
	}
	resID := idOf(res)
	if r := abc.do(http.MethodGet, base+"/groups/"+group+"/reservations", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("members: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reservations?booking_group_id="+group, nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("filter by group: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/groups/"+group, nil); r.body["reservation_count"].(float64) != 1 || r.body["room_count"].(float64) != 1 {
		t.Fatalf("group totals: %v", r.body)
	}

	// check in, charge, transfer
	lineID := idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)})
	ci := abc.doWith(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/check-in", map[string]any{
		"version": res.body["version"], "room_id": mustInt(r101), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}, map[string]string{"Idempotency-Key": "ci-1"})
	if ci.status != 201 {
		t.Fatalf("check-in: %d %v", ci.status, ci.body)
	}
	folio := idOf(response{body: ci.body["folio"].(map[string]any)})
	abc.doWith(http.MethodPost, base+"/folios/"+folio+"/charges", map[string]any{"charge_code_id": minibar, "quantity": "1", "unit_price": "400000"}, map[string]string{"Idempotency-Key": "c1"})
	transfer := base + "/folios/" + folio + "/city-ledger-transfers"
	body := map[string]any{"company_id": mustInt(company), "amount": "300000", "reference_number": "PO-1"}
	if r := abc.do(http.MethodPost, transfer, body); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	tr := abc.doWith(http.MethodPost, transfer, body, map[string]string{"Idempotency-Key": "t1"})
	pay := tr.body["payment"].(map[string]any)
	if tr.status != 201 || pay["payment_method"] != "CITY_LEDGER" || pay["company_id"].(float64) != float64(mustInt(company)) || tr.body["folio_balance"] != "100000" {
		t.Fatalf("transfer: %d %v", tr.status, tr.body)
	}
	if r := abc.doWith(http.MethodPost, transfer, body, map[string]string{"Idempotency-Key": "t1"}); r.status != 201 || r.body["payment"].(map[string]any)["id"] != pay["id"] {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folio+"/payments", map[string]any{"amount": "1000", "payment_method": "CITY_LEDGER"}, map[string]string{"Idempotency-Key": "p9"}); r.status != 422 {
		t.Fatalf("CITY_LEDGER is not a payment method: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, transfer, map[string]any{"company_id": mustInt(company), "amount": "999999"}, map[string]string{"Idempotency-Key": "t2"}); r.status != 409 || r.body["code"] != "TRANSFER_EXCEEDS_BALANCE" {
		t.Fatalf("exceeds the folio: %d %v", r.status, r.body)
	}

	// the account, a receipt, the statement
	acct := base + "/city-ledger/accounts/" + company
	if r := abc.do(http.MethodGet, acct, nil); r.status != 200 || r.body["balance"] != "300000" || r.body["available"] != "4700000" {
		t.Fatalf("account: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/city-ledger/accounts?owing=true", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("owing: %d %v", r.status, r.body)
	}
	rcpt := abc.doWith(http.MethodPost, acct+"/receipts", map[string]any{"amount": "100000", "payment_method": "BANK_TRANSFER", "reference_number": "TRX-1"}, map[string]string{"Idempotency-Key": "r1"})
	if rcpt.status != 201 || rcpt.body["balance"] != "200000" {
		t.Fatalf("receipt: %d %v", rcpt.status, rcpt.body)
	}
	receiptID := idOf(response{body: rcpt.body["receipt"].(map[string]any)})
	if r := abc.doWith(http.MethodPost, acct+"/receipts", map[string]any{"amount": "300000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "r2"}); r.status != 409 || r.body["code"] != "RECEIPT_EXCEEDS_BALANCE" {
		t.Fatalf("exceeds the balance: %d %v", r.status, r.body)
	}
	st := abc.do(http.MethodGet, acct+"/statement", nil)
	if st.status != 200 || st.body["closing_balance"] != "200000" || len(st.body["lines"].([]any)) != 2 {
		t.Fatalf("statement: %d %v", st.status, st.body)
	}
	if r := abc.do(http.MethodGet, acct+"/statement?from=2026-13-01", nil); r.status != 422 {
		t.Fatalf("bad date: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, acct+"/aging", nil); r.status != 200 || r.body["total"] != "200000" {
		t.Fatalf("aging: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, acct+"/receipts", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("receipts: %d %v", r.status, r.body)
	}
	pdf := e.raw(abc, http.MethodGet, base+"/companies/"+company+"/statement.pdf")
	if pdf.status != 200 || !strings.HasPrefix(pdf.contentType, "application/pdf") || !strings.HasPrefix(pdf.body, "%PDF-") {
		t.Fatalf("statement pdf: %d %q", pdf.status, pdf.contentType)
	}
	if r := e.raw(xyz, http.MethodGet, base+"/companies/"+company+"/statement.pdf"); r.status != 404 {
		t.Fatalf("another tenant's statement: %d", r.status)
	}

	// voiding the receipt needs an approval
	void := base + "/city-ledger/receipts/" + receiptID + "/void"
	if r := abc.do(http.MethodPost, void, map[string]any{"reason": "bounced"}); r.status != 422 || r.body["code"] != "APPROVAL_REQUIRED" {
		t.Fatalf("void without approval: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, void, map[string]any{"reason": "bounced", "approval": approval}); r.status != 200 || r.body["balance"] != "300000" {
		t.Fatalf("void: %d %v", r.status, r.body)
	}

	// the folio can be settled with the rest and the company's deactivation is refused while it owes
	if r := abc.do(http.MethodPatch, base+"/companies/"+company, map[string]any{"is_active": false}); r.status != 409 || r.body["code"] != "COMPANY_HAS_BALANCE" {
		t.Fatalf("deactivate: %d %v", r.status, r.body)
	}
}
