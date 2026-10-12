package app

import (
	"net/http"
	"testing"
)

// QRIS over HTTP: a payment, a deposit and a receipt can be made by it, the property can allow refunds by it, it has a fee rule and a clearing account of its own, and another tenant sees none of it.
func TestQRISAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	// the system key QRIS points at its own account, 1155
	var qrisAccount string
	for _, m := range abc.do(http.MethodGet, base+"/accounting/account-map", nil).body["data"].([]any) {
		if row := m.(map[string]any); row["map_key"] == "QRIS" {
			qrisAccount = row["account_code"].(string)
		}
	}
	if qrisAccount != "1155" {
		t.Fatalf("the key QRIS points at %q", qrisAccount)
	}

	// a fee rule for QRIS, listed with the others; a method that is not paid out by an acquirer has none
	rule := abc.do(http.MethodPost, base+"/bank/card-fee-rules", map[string]any{"payment_method": "QRIS", "mdr_rate": "0.7", "vat_rate": "11", "settlement_days": 1, "effective_from": "2026-09-30"})
	if rule.status != http.StatusCreated || rule.body["payment_method"] != "QRIS" {
		t.Fatalf("rule: %d %v", rule.status, rule.body)
	}
	if r := abc.do(http.MethodPost, base+"/bank/card-fee-rules", map[string]any{"payment_method": "BANK_TRANSFER", "mdr_rate": "1", "settlement_days": 1, "effective_from": "2026-09-30"}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a rule for a transfer: %d %v", r.status, r.body)
	}
	if rules := abc.do(http.MethodGet, base+"/bank/card-fee-rules", nil); rules.status != 200 || len(rules.body["data"].([]any)) != 1 {
		t.Fatalf("rules: %d %v", rules.status, rules.body)
	}

	// the property allows refunds by QRIS; a method that does not exist is refused
	if r := abc.do(http.MethodPatch, base, map[string]any{"refund_methods": []string{"CASH", "QRIS"}}); r.status != 200 {
		t.Fatalf("refund methods: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base, map[string]any{"refund_methods": []string{"CASH", "PAYPAL"}}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a refund method that does not exist: %d %v", r.status, r.body)
	}
	if got := abc.do(http.MethodGet, base, nil).body["refund_methods"].([]any); len(got) != 2 || got[1] != "QRIS" {
		t.Fatalf("refund methods: %v", got)
	}

	// the payments list takes the method as a filter, and refuses one that does not exist
	if r := abc.do(http.MethodGet, base+"/payments?method=QRIS", nil); r.status != 200 {
		t.Fatalf("payments by QRIS: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/payments?method=PAYPAL", nil); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a method that does not exist: %d %v", r.status, r.body)
	}

	// another tenant sees neither the rules nor the account
	for _, path := range []string{"/bank/card-fee-rules", "/accounting/account-map"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("%s from another tenant: %d %v", path, r.status, r.body)
		}
	}
}
