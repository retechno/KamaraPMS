package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestCashierHandoverAndShiftReportAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali + "/cashier"

	// the owner of the tenant can run a shift; nobody waits for them yet
	if r := abc.do(http.MethodGet, base+"/handovers", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("handovers: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/cashiers", nil); r.status != 200 || r.body["data"] == nil {
		t.Fatalf("cashiers: %d %v", r.status, r.body)
	}
	r := abc.do(http.MethodPost, base+"/shifts", map[string]any{"drawer": "MAIN", "opening_float": "100000"})
	if r.status != 201 && r.status != 200 {
		t.Fatalf("open: %d %v", r.status, r.body)
	}
	id := itoaID(int64(r.body["id"].(float64)))

	// an open shift reads as an X report, JSON and PDF
	rep := abc.do(http.MethodGet, base+"/shifts/"+id+"/report", nil)
	shift, _ := rep.body["shift"].(map[string]any)
	if rep.status != 200 || rep.body["kind"] != "X" || shift["number"] == nil || rep.body["cash_payments"] == nil || rep.body["other_tenders"] == nil {
		t.Fatalf("X report: %d %v", rep.status, rep.body)
	}
	pdf := e.raw(abc, http.MethodGet, base+"/shifts/"+id+"/report.pdf")
	if pdf.status != 200 || pdf.contentType != "application/pdf" || !strings.HasPrefix(pdf.body, "%PDF") {
		t.Fatalf("X report PDF: %d %s", pdf.status, pdf.contentType)
	}
	if r := abc.do(http.MethodPost, base+"/shifts/"+id+"/close", map[string]any{"counted_cash": "100000"}); r.status != 200 {
		t.Fatalf("close: %d %v", r.status, r.body)
	}
	if rep := abc.do(http.MethodGet, base+"/shifts/"+id+"/report", nil); rep.body["kind"] != "Z" {
		t.Fatalf("Z report: %v", rep.body)
	}
	if r := abc.do(http.MethodGet, base+"/shifts/999999/report", nil); r.status != 404 || r.body["code"] != "SHIFT_NOT_FOUND" {
		t.Fatalf("unknown shift: %d %v", r.status, r.body)
	}
	for _, path := range []string{"/handovers", "/cashiers", "/shifts/" + id + "/report"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("foreign tenant %s: %d %v", path, r.status, r.body)
		}
	}
	if r := e.raw(xyz, http.MethodGet, base+"/shifts/"+id+"/report.pdf"); r.status != 404 {
		t.Fatalf("foreign tenant PDF: %d", r.status)
	}
}
