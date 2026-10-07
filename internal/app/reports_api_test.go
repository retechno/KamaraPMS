package app

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestReportsAPIFlow(t *testing.T) {
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

	rev := abc.do(http.MethodGet, base+"/reports/revenue?from=2026-09-30&to=2026-09-30", nil)
	line := rev.body["by_charge_code"].([]any)[0].(map[string]any)
	if rev.status != 200 || line["charge_code"] != "ROOM" || line["revenue_account_code"] != "4110" || line["net_amount"] != "1000000" || rev.body["totals"].(map[string]any)["total"] != "1000000" {
		t.Fatalf("revenue: %d %v", rev.status, rev.body)
	}
	cashier := abc.do(http.MethodGet, base+"/reports/cashier?from=2026-09-30&to=2026-09-30", nil)
	if cashier.status != 200 || cashier.body["net"] != "300000" {
		t.Fatalf("cashier: %d %v", cashier.status, cashier.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/tax?from=2026-09-30&to=2026-09-30", nil); r.status != 200 || r.body["taxes"] == nil {
		t.Fatalf("tax: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/statistics?from=2026-09-30&to=2026-10-05", nil); r.status != 200 || r.body["totals"].(map[string]any)["room_nights_sold"].(float64) != 1 {
		t.Fatalf("statistics: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/daily-summary?date=2026-09-30", nil); r.status != 200 || r.body["status"] != "CLOSED" || r.body["summary"] == nil {
		t.Fatalf("daily summary: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/daily-summary?date=2026-01-01", nil); r.status != 404 || r.body["code"] != "BUSINESS_DAY_NOT_FOUND" {
		t.Fatalf("unknown day: %d %v", r.status, r.body)
	}
	inh := abc.do(http.MethodGet, base+"/reports/in-house", nil)
	if inh.status != 200 || len(inh.body["rows"].([]any)) != 1 {
		t.Fatalf("in-house: %d %v", inh.status, inh.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/departures?date=2026-10-03", nil); r.status != 200 || len(r.body["rows"].([]any)) != 1 {
		t.Fatalf("departures: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/reports/arrivals?date=2026-09-30", nil); r.status != 200 || len(r.body["rows"].([]any)) != 1 {
		t.Fatalf("arrivals: %d %v", r.status, r.body)
	}

	// Validation, isolation.
	for _, bad := range []string{"/reports/revenue", "/reports/revenue?from=2026-10-02&to=2026-10-01", "/reports/revenue?from=x&to=y", "/reports/cashier?from=2026-01-01&to=2027-06-01", "/reports/arrivals", "/reports/revenue?from=2026-09-30&to=2026-09-30&format=xml"} {
		if r := abc.do(http.MethodGet, base+bad, nil); r.status != 422 {
			t.Fatalf("%s: %d %v", bad, r.status, r.body)
		}
	}
	if r := xyz.do(http.MethodGet, base+"/reports/in-house", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("foreign tenant: %d %v", r.status, r.body)
	}

	// CSV: a file, with spreadsheet formulas neutralised.
	csv := e.raw(abc, http.MethodGet, base+"/reports/in-house?format=csv")
	if csv.status != 200 || !strings.HasPrefix(csv.contentType, "text/csv") || !strings.HasPrefix(csv.body, "stay_number,status,confirmation_number,guest,room") ||
		strings.Contains(csv.body, ",=HYPERLINK") || !strings.Contains(csv.body, "'=HYPERLINK") {
		t.Fatalf("csv: %d %q %q", csv.status, csv.contentType, csv.body)
	}
}

func itoaID(id int64) string { return strconv.FormatInt(id, 10) }

type rawResponse struct {
	status      int
	contentType string
	body        string
}

// raw is a GET whose answer is not JSON.
func (e *apiEnv) raw(c *client, method, path string) rawResponse {
	e.t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+c.token)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	return rawResponse{status: rec.Code, contentType: rec.Header().Get("Content-Type"), body: rec.Body.String()}
}

// header is the response headers of a GET.
func (e *apiEnv) header(c *client, path string) http.Header {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.Header.Set("Authorization", "Bearer "+c.token)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	return rec.Header()
}
