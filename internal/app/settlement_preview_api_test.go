package app

import (
	"net/http"
	"testing"
)

func TestSettlementPreviewAndTheFeeRuleVATAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali + "/bank"

	// a fee rule carries the VAT the acquirer charges on its commission
	rule := abc.do(http.MethodPost, base+"/card-fee-rules", map[string]any{"payment_method": "CARD", "mdr_rate": "2", "vat_rate": "11", "settlement_days": 1, "effective_from": "2026-09-01"})
	if rule.status != 201 || rule.body["vat_rate"] != "11" {
		t.Fatalf("rule: %d %v", rule.status, rule.body)
	}
	if r := abc.do(http.MethodPost, base+"/card-fee-rules", map[string]any{"payment_method": "CARD", "mdr_rate": "2", "vat_rate": "101", "settlement_days": 1, "effective_from": "2026-09-02"}); r.status != 422 || fieldsOf(r)["vat_rate"] != "INVALID_RATE" {
		t.Fatalf("a VAT rate above 100: %d %v", r.status, r.body)
	}
	if l := abc.do(http.MethodGet, base+"/card-fee-rules", nil); l.status != 200 || l.body["data"].([]any)[0].(map[string]any)["vat_rate"] != "11" {
		t.Fatalf("list: %d %v", l.status, l.body)
	}
	// the preview of a settlement of a statement that does not exist
	body := map[string]any{"account_key": "CARD", "journal_line_ids": []int64{1}}
	if r := abc.do(http.MethodPost, base+"/statements/999999/lines/1/settlement-preview", body); r.status != 404 {
		t.Fatalf("unknown statement: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/statements/999999/lines/1/settlement-preview", map[string]any{"account_key": "CASH", "journal_line_ids": []int64{1}}); r.status != 422 {
		t.Fatalf("bad key: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodPost, base+"/statements/1/lines/1/settlement-preview", body); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, base+"/card-settlements", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant list: %d %v", r.status, r.body)
	}
}
