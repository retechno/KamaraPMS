package app

import (
	"net/http"
	"testing"
)

func TestBillingConfigAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	// A property created through the API starts with the ten standard charge codes, without rules.
	codes := abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil)
	list := codes.body["data"].([]any)
	if codes.status != 200 || len(list) != 10 {
		t.Fatalf("seeded charge codes: %d %v", codes.status, codes.body)
	}
	var roomID string
	for _, c := range list {
		m := c.(map[string]any)
		if len(m["taxes"].([]any)) != 0 || m["is_system"] != true {
			t.Fatalf("seeded code: %v", m)
		}
		if m["code"] == "ROOM" {
			roomID = idOf(response{body: m})
		}
	}

	// Taxes and service charges: rates are strings with four decimals.
	vat := abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "vat", "name": "VAT 11%", "rate": "11", "tax_on_service": true})
	if vat.status != http.StatusCreated || vat.body["rate"] != "11.0000" || vat.body["code"] != "VAT" || vat.body["tax_on_service"] != true {
		t.Fatalf("create tax: %d %v", vat.status, vat.body)
	}
	if r := abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "BAD", "name": "x", "rate": "101"}); r.status != 422 || fieldsOf(r)["rate"] != "INVALID_RATE" {
		t.Fatalf("rate above 100: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "VAT", "name": "dup", "rate": "1"}); r.status != 409 || r.body["code"] != "CODE_TAKEN" {
		t.Fatalf("duplicate: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/taxes", map[string]any{"code": "INC", "name": "x", "rate": "1", "is_inclusive": true}); r.status != 422 || fieldsOf(r)["is_inclusive"] != "UNKNOWN_FIELD" {
		t.Fatalf("there is no inclusive flag on a tax: %d %v", r.status, r.body)
	}
	svc := abc.do(http.MethodPost, base+"/service-charges", map[string]any{"code": "SVC", "name": "Service 10%", "rate": "10"})
	if svc.status != http.StatusCreated {
		t.Fatalf("create service charge: %d %v", svc.status, svc.body)
	}
	if r := abc.do(http.MethodPatch, base+"/taxes/"+idOf(vat), map[string]any{"rate": "12"}); r.status != 200 || r.body["rate"] != "12.0000" || r.body["affected_open_stays"] != float64(0) {
		t.Fatalf("rate change: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/taxes/"+idOf(vat), map[string]any{"code": "NEW"}); r.status != 422 || fieldsOf(r)["code"] != "UNKNOWN_FIELD" {
		t.Fatalf("code is immutable: %d %v", r.status, r.body)
	}

	// Rules: replace, read back in order, reject bad input.
	rules := map[string]any{
		"taxes":           []map[string]any{{"tax_id": mustInt(idOf(vat)), "sequence": 1}},
		"service_charges": []map[string]any{{"service_charge_id": mustInt(idOf(svc)), "sequence": 1}},
	}
	put := abc.do(http.MethodPut, base+"/charge-codes/"+roomID+"/rules", rules)
	if put.status != 200 || len(put.body["taxes"].([]any)) != 1 || put.body["taxes"].([]any)[0].(map[string]any)["code"] != "VAT" ||
		put.body["service_charges"].([]any)[0].(map[string]any)["rate"] != "10.0000" {
		t.Fatalf("put rules: %d %v", put.status, put.body)
	}
	if r := abc.do(http.MethodGet, base+"/charge-codes/"+roomID, nil); r.status != 200 || len(r.body["taxes"].([]any)) != 1 {
		t.Fatalf("get with rules: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, base+"/charge-codes/"+roomID+"/rules", map[string]any{
		"taxes": []map[string]any{{"tax_id": mustInt(idOf(vat)), "sequence": 1}, {"tax_id": mustInt(idOf(vat)), "sequence": 1}},
	}); r.status != 422 || fieldsOf(r)["taxes[1].sequence"] != "DUPLICATE" {
		t.Fatalf("duplicate rules: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, base+"/charge-codes/"+roomID+"/rules", map[string]any{"taxes": []map[string]any{{"tax_id": 999999, "sequence": 1}}}); r.status != 404 || r.body["code"] != "TAX_NOT_FOUND" {
		t.Fatalf("unknown tax: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/taxes/"+idOf(vat), map[string]any{"is_active": false}); r.status != 409 || r.body["code"] != "TAX_IN_USE" {
		t.Fatalf("mapped tax: %d %v", r.status, r.body)
	}

	// Charge codes.
	cc := abc.do(http.MethodPost, base+"/charge-codes", map[string]any{"code": "spa", "name": "Spa", "charge_type": "SERVICE", "price_mode": "INCLUSIVE", "default_unit_price": "250000"})
	if cc.status != http.StatusCreated || cc.body["default_unit_price"] != "250000.00" || cc.body["is_system"] != false || len(cc.body["taxes"].([]any)) != 0 {
		t.Fatalf("create charge code: %d %v", cc.status, cc.body)
	}
	if r := abc.do(http.MethodPost, base+"/charge-codes", map[string]any{"code": "X", "name": "x", "charge_type": "SERVICE", "price_mode": "INCLUSIVE", "default_unit_price": "1.5"}); r.status != 422 || fieldsOf(r)["default_unit_price"] != "INVALID_AMOUNT" {
		t.Fatalf("IDR has no decimals: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/charge-codes/"+roomID, map[string]any{"charge_type": "FEE"}); r.status != 409 || r.body["code"] != "SYSTEM_CHARGE_CODE_LOCKED" {
		t.Fatalf("system code type: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/charge-codes?charge_type=SERVICE&active=true", nil); len(r.body["data"].([]any)) != 3 {
		t.Fatalf("filters: %v", r.body)
	}
	if r := abc.do(http.MethodGet, base+"/charge-codes?limit=4", nil); len(r.body["data"].([]any)) != 4 || r.body["next_cursor"] == nil {
		t.Fatalf("paging: %v", r.body)
	}
	for _, path := range []string{"/charge-codes/abc", "/charge-codes/999999"} {
		if r := abc.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "CHARGE_CODE_NOT_FOUND" {
			t.Fatalf("GET %s: %d %v", path, r.status, r.body)
		}
	}

	// Another tenant sees nothing; a user without billing_config.manage can read but not write.
	for _, path := range []string{"/taxes", "/service-charges", "/charge-codes", "/charge-codes/" + roomID} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("GET %s from another tenant: %d %v", path, r.status, r.body)
		}
	}
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Clerk", "permissions": []string{"reservation.read"}})
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "clerk@hotel.com", "full_name": "Clerk", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(bali), "role_id": role.body["id"]}},
	}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	clerk := &client{env: e}
	if r := clerk.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "clerk@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d", r.status)
	}
	if r := clerk.do(http.MethodGet, base+"/charge-codes/"+roomID, nil); r.status != 200 {
		t.Fatalf("clerk read: %d", r.status)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, base + "/taxes", map[string]any{"code": "T", "name": "t", "rate": "1"}},
		{http.MethodPatch, base + "/taxes/" + idOf(vat), map[string]any{"name": "x"}},
		{http.MethodPost, base + "/service-charges", map[string]any{"code": "S", "name": "s", "rate": "1"}},
		{http.MethodPost, base + "/charge-codes", map[string]any{"code": "C", "name": "c", "charge_type": "OTHER", "price_mode": "EXCLUSIVE"}},
		{http.MethodPut, base + "/charge-codes/" + roomID + "/rules", map[string]any{}},
	} {
		if r := clerk.do(c.method, c.path, c.body); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
			t.Fatalf("%s %s as clerk: %d %v", c.method, c.path, r.status, r.body)
		}
	}
}
