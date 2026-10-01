package app

import (
	"net/http"
	"testing"
)

// TestCityLedgerInvoiceAPI checks the invoice endpoints over HTTP: the request rules and the errors (the issuing
// itself is covered against the real database in package cityledger).
func TestCityLedgerInvoiceAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}
	company := idOf(abc.do(http.MethodPost, base+"/companies", map[string]any{"code": "ACME", "name": "Acme Corp"}))
	acct := base + "/city-ledger/accounts/" + company

	if r := abc.do(http.MethodGet, acct+"/invoice-candidates", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("candidates: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, acct+"/invoices", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("invoices: %d %v", r.status, r.body)
	}
	body := map[string]any{"payment_ids": []int64{9999}, "notes": "x"}
	if r := abc.do(http.MethodPost, acct+"/invoices", body); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, acct+"/invoices", body, map[string]string{"Idempotency-Key": "i1"}); r.status != 409 || r.body["code"] != "TRANSFER_NOT_AVAILABLE" {
		t.Fatalf("unknown transfer: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, acct+"/invoices", map[string]any{"payment_ids": []int64{}}, map[string]string{"Idempotency-Key": "i2"}); r.status != 422 {
		t.Fatalf("no transfers: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, acct+"/invoices", map[string]any{"payment_ids": []int64{1}, "surprise": 1}, map[string]string{"Idempotency-Key": "i3"}); r.status != 422 {
		t.Fatalf("unknown field: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/city-ledger/invoices/9999", nil); r.status != 404 || r.body["code"] != "INVOICE_NOT_FOUND" {
		t.Fatalf("unknown invoice: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/city-ledger/invoices/9999/void", map[string]any{"reason": "x", "approval": approval}); r.status != 404 {
		t.Fatalf("void unknown: %d %v", r.status, r.body)
	}
	if r := e.raw(abc, http.MethodGet, base+"/city-ledger/invoices/9999/invoice.pdf"); r.status != 404 {
		t.Fatalf("pdf of an unknown invoice: %d", r.status)
	}
	if r := xyz.do(http.MethodGet, acct+"/invoices", nil); r.status != 404 {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}
}
