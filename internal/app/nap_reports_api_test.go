package app

import (
	"net/http"
	"strings"
	"testing"
)

// The five reports of the night audit pack over HTTP (docs/architecture/38-reports-night-audit-pack.md 7.2 to 7.5): JSON, CSV with the formulas neutralised, PDF, the date rules and the tenancy.
func TestNightAuditPackReportsAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	var roomCode int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomCode = int64(m["id"].(float64))
		}
	}
	abc.do(http.MethodPatch, base+"/charge-codes/"+itoaID(roomCode), map[string]any{"gl_account_code": "4110"})
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "first_name": "=HYPERLINK(\"x\")", "last_name": "Guest"}))
	walk := abc.doWith(http.MethodPost, base+"/walk-ins", map[string]any{"guest_id": mustInt(guest), "room_id": mustInt(r101), "rate_plan_id": mustInt(plan), "departure_date": "2026-10-03", "adult_count": 2, "child_count": 0},
		map[string]string{"Idempotency-Key": "w-1"})
	folio := idOf(response{body: walk.body["folio"].(map[string]any)})
	abc.doWith(http.MethodPost, base+"/folios/"+folio+"/payments", map[string]any{"amount": "300000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "p1"})
	if r := abc.do(http.MethodPost, base+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}); r.status != 200 {
		t.Fatalf("audit: %d %v", r.status, r.body)
	}

	// the guest ledger of the closed day: one folio, 1,000,000 charged, 300,000 paid
	gl := abc.do(http.MethodGet, base+"/reports/guest-ledger?as_of=2026-09-30", nil)
	rows, _ := gl.body["rows"].([]any)
	if gl.status != 200 || len(rows) != 1 || gl.body["balance"] != "700000" || gl.body["reconciled"] != true {
		t.Fatalf("guest ledger: %d %v", gl.status, gl.body)
	}
	// no date is the last closed day
	if r := abc.do(http.MethodGet, base+"/reports/guest-ledger", nil); r.status != 200 || r.body["as_of"] != "2026-09-30" {
		t.Fatalf("default date: %d %v", r.status, r.body)
	}
	// the open day and the future are not closed days
	for _, d := range []string{"2026-10-01", "2030-01-01"} {
		if r := abc.do(http.MethodGet, base+"/reports/guest-ledger?as_of="+d, nil); r.status != 409 || r.body["code"] != "DATE_NOT_CLOSED" {
			t.Fatalf("%s: %d %v", d, r.status, r.body)
		}
		if r := abc.do(http.MethodGet, base+"/reports/deposit-ledger?as_of="+d, nil); r.status != 409 || r.body["code"] != "DATE_NOT_CLOSED" {
			t.Fatalf("deposit %s: %d %v", d, r.status, r.body)
		}
	}
	if r := abc.do(http.MethodGet, base+"/reports/deposit-ledger?as_of=2026-09-30", nil); r.status != 200 || r.body["reconciled"] != true {
		t.Fatalf("deposit ledger: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/cancellations?from=2026-09-30&to=2026-09-30", nil); r.status != 200 || len(r.body["events"].([]any)) != 0 || r.body["fees_posted"] != "0" {
		t.Fatalf("cancellations: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/corrections?from=2026-09-30&to=2026-09-30", nil); r.status != 200 || r.body["revenue_taken_back"] != "0" || r.body["net_revenue"] != "1000000" {
		t.Fatalf("corrections: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/z-reports?date=2026-09-30", nil); r.status != 200 || r.body["reconciled"] != true || len(r.body["blocks"].([]any)) != 0 {
		t.Fatalf("z reports: %d %v", r.status, r.body)
	}

	// validation: a range that ends before it starts, a range of years, a kind that does not exist, a date that is not one
	for _, bad := range []string{"/reports/cancellations", "/reports/cancellations?from=2026-10-02&to=2026-10-01", "/reports/cancellations?from=2026-01-01&to=2027-06-01", "/reports/cancellations?from=2026-09-30&to=2026-09-30&kind=ALL_OF_IT",
		"/reports/corrections?from=2026-09-30&to=2026-09-30&kind=BOGUS", "/reports/corrections?from=x&to=y", "/reports/guest-ledger?as_of=x", "/reports/deposit-ledger?as_of=2026-13-45", "/reports/z-reports?date=x",
		"/reports/guest-ledger?as_of=2026-09-30&format=xml"} {
		if r := abc.do(http.MethodGet, base+bad, nil); r.status != 422 {
			t.Fatalf("%s: %d %v", bad, r.status, r.body)
		}
	}

	// another tenant sees no such property, on every route
	for _, path := range []string{"/reports/cancellations?from=2026-09-30&to=2026-09-30", "/reports/guest-ledger", "/reports/deposit-ledger", "/reports/corrections?from=2026-09-30&to=2026-09-30", "/reports/z-reports",
		"/reports/cancellations.pdf?from=2026-09-30&to=2026-09-30", "/reports/guest-ledger.pdf", "/reports/deposit-ledger.pdf", "/reports/corrections.pdf?from=2026-09-30&to=2026-09-30", "/reports/z-reports.pdf"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("foreign tenant %s: %d %v", path, r.status, r.body)
		}
	}

	// CSV: a file; the name of the guest starts with a formula and is neutralised
	csv := e.raw(abc, http.MethodGet, base+"/reports/guest-ledger?as_of=2026-09-30&format=csv")
	if csv.status != 200 || !strings.HasPrefix(csv.contentType, "text/csv") || !strings.HasPrefix(csv.body, "folio_number,folio_type,status,confirmation_number,room,owner") ||
		strings.Contains(csv.body, ",=HYPERLINK") || !strings.Contains(csv.body, "'=HYPERLINK") {
		t.Fatalf("csv: %d %q %q", csv.status, csv.contentType, csv.body)
	}
	for _, path := range []string{"/reports/cancellations?from=2026-09-30&to=2026-09-30", "/reports/deposit-ledger?as_of=2026-09-30", "/reports/corrections?from=2026-09-30&to=2026-09-30", "/reports/z-reports?date=2026-09-30"} {
		if r := e.raw(abc, http.MethodGet, base+path+"&format=csv"); r.status != 200 || !strings.HasPrefix(r.contentType, "text/csv") {
			t.Fatalf("csv %s: %d %q", path, r.status, r.contentType)
		}
	}

	// PDF: inline, in both languages
	for _, path := range []string{"/reports/cancellations.pdf?from=2026-09-30&to=2026-09-30", "/reports/guest-ledger.pdf?as_of=2026-09-30", "/reports/deposit-ledger.pdf?as_of=2026-09-30",
		"/reports/corrections.pdf?from=2026-09-30&to=2026-09-30", "/reports/z-reports.pdf?date=2026-09-30"} {
		for _, lang := range []string{"", "&lang=id"} {
			r := e.raw(abc, http.MethodGet, base+path+lang)
			if r.status != 200 || r.contentType != "application/pdf" || !strings.HasPrefix(r.body, "%PDF-") {
				t.Fatalf("pdf %s%s: %d %q", path, lang, r.status, r.contentType)
			}
		}
	}
	if r := abc.do(http.MethodGet, base+"/reports/guest-ledger.pdf?as_of=2026-10-01", nil); r.status != 409 || r.body["code"] != "DATE_NOT_CLOSED" {
		t.Fatalf("pdf of the open day: %d %v", r.status, r.body)
	}
}
