package app

import (
	"net/http"
	"strings"
	"testing"
)

func TestCashFlowAPIByBothMethods(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali + "/accounting/cash-flow"
	for query, method := range map[string]string{"": "INDIRECT", "?method=indirect": "INDIRECT", "?method=DIRECT": "DIRECT"} {
		r := abc.do(http.MethodGet, base+query, nil)
		lines, _ := r.body["lines"].([]any)
		if r.status != 200 || r.body["method"] != method || r.body["reconciled"] != true || len(lines) == 0 {
			t.Fatalf("%q: %d %v", query, r.status, r.body)
		}
	}
	if r := abc.do(http.MethodGet, base+"?method=weekly", nil); r.status != 422 || fieldsOf(r)["method"] != "INVALID_VALUE" {
		t.Fatalf("an unknown method: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, base+"?method=DIRECT", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}
	csv := e.raw(abc, http.MethodGet, base+"?method=DIRECT&format=csv")
	if csv.status != 200 || !strings.HasPrefix(csv.contentType, "text/csv") || !strings.Contains(csv.body, "Net cash from operating activities") {
		t.Fatalf("csv: %d %q", csv.status, csv.body)
	}
	for _, q := range []string{"", "?method=DIRECT"} {
		pdf := e.raw(abc, http.MethodGet, base+".pdf"+q)
		if pdf.status != 200 || pdf.contentType != "application/pdf" || !strings.HasPrefix(pdf.body, "%PDF-") {
			t.Fatalf("pdf %q: %d %q", q, pdf.status, pdf.contentType)
		}
	}
	if pdf := e.raw(abc, http.MethodGet, base+".pdf?method=x"); pdf.status != 422 {
		t.Fatalf("pdf with an unknown method: %d", pdf.status)
	}
}
