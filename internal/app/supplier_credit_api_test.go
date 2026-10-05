package app

import (
	"net/http"
	"testing"
)

func TestSupplierCreditNoteAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	pay := base + "/payables"

	var expense float64
	for _, a := range abc.do(http.MethodGet, base+"/accounting/accounts?limit=500", nil).body["data"].([]any) {
		if m := a.(map[string]any); m["code"] == "6510" {
			expense = m["id"].(float64)
		}
	}
	sup := abc.do(http.MethodPost, pay+"/suppliers", map[string]any{"code": "PLN", "name": "PLN"})
	if sup.status != 201 {
		t.Fatalf("supplier: %d %v", sup.status, sup.body)
	}
	supplierID := sup.body["id"].(float64)
	bill := abc.doWith(http.MethodPost, pay+"/bills", map[string]any{
		"supplier_id": supplierID, "supplier_invoice_number": "INV-1", "bill_date": "2026-09-30",
		"lines": []map[string]any{{"account_id": expense, "amount": "1000", "description": "Electricity"}},
	}, map[string]string{"Idempotency-Key": "b1"})
	if bill.status != 201 {
		t.Fatalf("bill: %d %v", bill.status, bill.body)
	}
	billID := bill.body["id"].(float64)

	in := map[string]any{"bill_id": billID, "supplier_credit_number": "CR-1", "credit_date": "2026-09-30", "reason": "returned",
		"lines": []map[string]any{{"bill_line_no": 1, "amount": "300"}}}
	if r := abc.do(http.MethodPost, pay+"/credit-notes", in); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("needs a key: %d %v", r.status, r.body)
	}
	c := abc.doWith(http.MethodPost, pay+"/credit-notes", in, map[string]string{"Idempotency-Key": "c1"})
	if c.status != 201 || c.body["credit_number"] != "SCN000001" || c.body["applied"] != "300" || c.body["unapplied"] != "0" {
		t.Fatalf("credit note: %d %v", c.status, c.body)
	}
	id := itoaID(int64(c.body["id"].(float64)))
	if again := abc.doWith(http.MethodPost, pay+"/credit-notes", in, map[string]string{"Idempotency-Key": "c1"}); again.status != 201 || again.body["id"] != c.body["id"] {
		t.Fatalf("a retry returns the first: %d %v", again.status, again.body)
	}
	// the bill shows what was credited
	if b := abc.do(http.MethodGet, pay+"/bills/"+itoaID(int64(billID)), nil); b.body["credited"] != "300" || b.body["outstanding"] != "700" {
		t.Fatalf("bill: %v", b.body)
	}
	if l := abc.do(http.MethodGet, pay+"/credit-notes?bill_id="+itoaID(int64(billID)), nil); l.status != 200 || len(l.body["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", l.status, l.body)
	}
	if g := abc.do(http.MethodGet, pay+"/credit-notes/"+id, nil); g.status != 200 || len(g.body["lines"].([]any)) != 1 || len(g.body["allocations"].([]any)) != 1 {
		t.Fatalf("get: %d %v", g.status, g.body)
	}
	// the same supplier credit note is entered once; a line cannot be credited beyond what it has left
	dup := abc.doWith(http.MethodPost, pay+"/credit-notes", in, map[string]string{"Idempotency-Key": "c2"})
	if dup.status != 409 || dup.body["code"] != "DUPLICATE_CREDIT_NOTE" {
		t.Fatalf("duplicate: %d %v", dup.status, dup.body)
	}
	in["supplier_credit_number"] = "CR-2"
	in["lines"] = []map[string]any{{"bill_line_no": 1, "amount": "701"}}
	if r := abc.doWith(http.MethodPost, pay+"/credit-notes", in, map[string]string{"Idempotency-Key": "c3"}); r.status != 422 || fieldsOf(r)["lines[0].amount"] != "EXCEEDS_BILL_LINE" {
		t.Fatalf("too much: %d %v", r.status, r.body)
	}
	// nothing to apply, then void with an approval
	if r := abc.do(http.MethodPost, pay+"/credit-notes/"+id+"/apply", map[string]any{"allocations": []map[string]any{{"bill_id": billID, "amount": "1"}}}); r.status != 409 {
		t.Fatalf("apply: %d %v", r.status, r.body)
	}
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}
	if r := abc.do(http.MethodPost, pay+"/credit-notes/"+id+"/void", map[string]any{"reason": "withdrawn"}); r.status == 200 {
		t.Fatalf("a void needs an approval: %d", r.status)
	}
	if r := abc.do(http.MethodPost, pay+"/credit-notes/"+id+"/void", map[string]any{"reason": "withdrawn", "approval": approval}); r.status != 200 || r.body["status"] != "VOIDED" {
		t.Fatalf("void: %d %v", r.status, r.body)
	}
	if b := abc.do(http.MethodGet, pay+"/bills/"+itoaID(int64(billID)), nil); b.body["outstanding"] != "1000" {
		t.Fatalf("the bill owes it again: %v", b.body)
	}
	if r := abc.do(http.MethodGet, pay+"/credit-notes/999999", nil); r.status != 404 || r.body["code"] != "CREDIT_NOTE_NOT_FOUND" {
		t.Fatalf("unknown: %d %v", r.status, r.body)
	}
	for _, path := range []string{"/credit-notes", "/credit-notes/" + id} {
		if r := xyz.do(http.MethodGet, pay+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("foreign tenant %s: %d %v", path, r.status, r.body)
		}
	}
}
