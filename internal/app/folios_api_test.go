package app

import (
	"net/http"
	"strconv"
	"testing"
)

func TestFoliosAndPaymentsAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	codes := map[string]int64{}
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		m := c.(map[string]any)
		codes[m["code"].(string)] = int64(m["id"].(float64))
	}
	svc := idOf(abc.do(http.MethodPost, base+"/service-charges", map[string]any{"code": "SVC", "name": "Service", "rate": "10"}))
	vat := idOf(abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "VAT", "name": "VAT", "rate": "11", "tax_on_service": true}))
	if r := abc.do(http.MethodPut, base+"/charge-codes/"+itoa(codes["MINIBAR"])+"/rules", map[string]any{
		"taxes": []map[string]any{{"tax_id": mustInt(vat), "sequence": 1}}, "service_charges": []map[string]any{{"service_charge_id": mustInt(svc), "sequence": 1}},
	}); r.status != 200 {
		t.Fatalf("rules: %d %v", r.status, r.body)
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101"})
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": codes["ROOM"]}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-05", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))
	res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-10-02", "departure_date": "2026-10-03", "adult_count": 1, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	resID := idOf(res)

	// Deposit (creates the folio); the Idempotency-Key is required.
	dep := map[string]any{"amount": "500000", "payment_method": "BANK_TRANSFER", "reference_number": "TRF-1"}
	if r := abc.do(http.MethodPost, base+"/reservations/"+resID+"/deposits", dep); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("deposit without key: %d %v", r.status, r.body)
	}
	d := abc.doWith(http.MethodPost, base+"/reservations/"+resID+"/deposits", dep, map[string]string{"Idempotency-Key": "dep-1"})
	if d.status != http.StatusCreated || d.body["folio_balance"] != "-500000" || d.body["payment"].(map[string]any)["payment_type"] != "PAYMENT" {
		t.Fatalf("deposit: %d %v", d.status, d.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/reservations/"+resID+"/deposits", dep, map[string]string{"Idempotency-Key": "dep-1"}); r.status != http.StatusCreated ||
		int64(r.body["payment"].(map[string]any)["id"].(float64)) != int64(d.body["payment"].(map[string]any)["id"].(float64)) {
		t.Fatalf("deposit replay: %d %v", r.status, r.body)
	}
	folioID := itoa(int64(d.body["payment"].(map[string]any)["folio_id"].(float64)))
	// the reservation detail lists the folio with its balance
	detail := abc.do(http.MethodGet, base+"/reservations/"+resID, nil)
	fol := detail.body["folios"].([]any)
	if len(fol) != 1 || fol[0].(map[string]any)["balance"] != "-500000" {
		t.Fatalf("reservation folios: %v", detail.body["folios"])
	}

	// Charges.
	charge := map[string]any{"charge_code_id": codes["MINIBAR"], "quantity": "2", "unit_price": "50000"}
	if r := abc.do(http.MethodPost, base+"/folios/"+folioID+"/charges", charge); r.status != 400 {
		t.Fatalf("charge without key: %d", r.status)
	}
	ch := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/charges", charge, map[string]string{"Idempotency-Key": "ch-1"})
	item := ch.body["item"].(map[string]any)
	if ch.status != http.StatusCreated || item["debit"] != "122100" || item["service_charge_total"] != "10000" || len(item["components"].([]any)) != 2 || ch.body["folio_balance"] != "-377900" {
		t.Fatalf("charge: %d %v", ch.status, ch.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/charges", map[string]any{"charge_code_id": codes["ROOM"], "quantity": "1", "unit_price": "1"}, map[string]string{"Idempotency-Key": "ch-2"}); r.status != 409 || r.body["code"] != "ROOM_CHARGE_REQUIRES_ROOM_POSTING" {
		t.Fatalf("room code: %d %v", r.status, r.body)
	}

	// Corrections need the approval block.
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}
	adj := map[string]any{"charge_code_id": codes["MINIBAR"], "amount": "-50000", "reason": "spilled", "approval": approval}
	noApproval := map[string]any{"charge_code_id": codes["MINIBAR"], "amount": "-50000", "reason": "spilled"}
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/adjustments", noApproval, map[string]string{"Idempotency-Key": "a-0"}); r.status != 422 || r.body["code"] != "APPROVAL_REQUIRED" {
		t.Fatalf("no approval: %d %v", r.status, r.body)
	}
	bad := map[string]any{"charge_code_id": codes["MINIBAR"], "amount": "-50000", "reason": "spilled", "approval": map[string]any{"email": "admin@hotel.com", "password": "not the password"}}
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/adjustments", bad, map[string]string{"Idempotency-Key": "a-0"}); r.status != 401 || r.body["code"] != "APPROVAL_INVALID_CREDENTIALS" {
		t.Fatalf("bad password: %d %v", r.status, r.body)
	}
	a := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/adjustments", adj, map[string]string{"Idempotency-Key": "a-1"})
	if a.status != http.StatusCreated || a.body["item"].(map[string]any)["transaction_type"] != "ADJUSTMENT" || a.body["item"].(map[string]any)["approved_by"] == nil {
		t.Fatalf("adjustment: %d %v", a.status, a.body)
	}
	if body := a.body; body["item"].(map[string]any)["reason"] != "spilled" {
		t.Fatalf("reason: %v", body)
	}
	itemID := itoa(int64(item["id"].(float64)))
	if r := abc.do(http.MethodPost, base+"/folio-items/"+itemID+"/reverse", map[string]any{"reason": "duplicate"}); r.status != 422 || r.body["code"] != "APPROVAL_REQUIRED" {
		t.Fatalf("reverse without approval: %d %v", r.status, r.body)
	}
	rev := abc.do(http.MethodPost, base+"/folio-items/"+itemID+"/reverse", map[string]any{"reason": "duplicate", "approval": approval})
	if rev.status != http.StatusCreated || rev.body["item"].(map[string]any)["transaction_type"] != "REVERSAL" {
		t.Fatalf("reverse: %d %v", rev.status, rev.body)
	}
	if r := abc.do(http.MethodPost, base+"/folio-items/"+itemID+"/reverse", map[string]any{"reason": "again", "approval": approval}); r.status != 409 || r.body["code"] != "ALREADY_REVERSED" {
		t.Fatalf("reverse twice: %d %v", r.status, r.body)
	}

	// Folio detail.
	f := abc.do(http.MethodGet, base+"/folios/"+folioID, nil)
	if f.status != 200 || len(f.body["items"].([]any)) != 4 || f.body["status"] != "OPEN" {
		t.Fatalf("folio: %d %v", f.status, f.body)
	}
	lst := abc.do(http.MethodGet, base+"/folios?reservation_id="+resID+"&status=OPEN", nil)
	if lst.status != 200 || len(lst.body["data"].([]any)) != 1 {
		t.Fatalf("folios list: %d %v", lst.status, lst.body)
	}

	// Payments: post, refund, void.
	pay := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "100000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "pay-1"})
	if pay.status != http.StatusCreated || pay.body["payment"].(map[string]any)["refundable"] != "100000" {
		t.Fatalf("payment: %d %v", pay.status, pay.body)
	}
	payID := itoa(int64(pay.body["payment"].(map[string]any)["id"].(float64)))
	ref := abc.doWith(http.MethodPost, base+"/payments/"+payID+"/refunds", map[string]any{"amount": "200000", "reason": "x", "approval": approval}, map[string]string{"Idempotency-Key": "ref-1"})
	if ref.status != 409 || ref.body["code"] != "REFUND_EXCEEDS_PAYMENT" || ref.body["context"].(map[string]any)["refundable"] != "100000" {
		t.Fatalf("over refund: %d %v", ref.status, ref.body)
	}
	pay2 := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "1000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "pay-2"})
	pay2ID := itoa(int64(pay2.body["payment"].(map[string]any)["id"].(float64)))
	if r := abc.do(http.MethodPost, base+"/payments/"+pay2ID+"/void", map[string]any{"reason": "typo"}); r.status != 422 || r.body["code"] != "APPROVAL_REQUIRED" {
		t.Fatalf("void without approval: %d %v", r.status, r.body)
	}
	v := abc.do(http.MethodPost, base+"/payments/"+pay2ID+"/void", map[string]any{"reason": "typo", "approval": approval})
	if v.status != 200 || v.body["payment"].(map[string]any)["status"] != "VOIDED" {
		t.Fatalf("void: %d %v", v.status, v.body)
	}
	okRef := abc.doWith(http.MethodPost, base+"/payments/"+payID+"/refunds", map[string]any{"amount": "30000", "reason": "goodwill", "approval": approval}, map[string]string{"Idempotency-Key": "ref-2"})
	if okRef.status != http.StatusCreated || okRef.body["payment"].(map[string]any)["payment_type"] != "REFUND" {
		t.Fatalf("refund: %d %v", okRef.status, okRef.body)
	}
	cashier := abc.do(http.MethodGet, base+"/payments?business_date=2026-09-30", nil)
	if cashier.status != 200 || len(cashier.body["data"].([]any)) != 4 || len(cashier.body["totals"].([]any)) == 0 {
		t.Fatalf("cashier: %d %v", cashier.status, cashier.body)
	}

	// Close: the balance must be zero.
	if r := abc.do(http.MethodPost, base+"/folios/"+folioID+"/close", map[string]any{"version": f.body["version"]}); r.status != 409 {
		t.Fatalf("close with balance or stale version: %d %v", r.status, r.body)
	}

	// Isolation.
	if r := xyz.do(http.MethodGet, base+"/folios/"+folioID, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	if r := xyz.doWith(http.MethodPost, base+"/payments/"+payID+"/refunds", map[string]any{"amount": "1", "reason": "x", "approval": approval}, map[string]string{"Idempotency-Key": "z"}); r.status != 404 {
		t.Fatalf("foreign refund: %d %v", r.status, r.body)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
