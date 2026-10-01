package app

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestEndToEndStay walks a guest through the whole product over HTTP: create, confirm, assign, check in, post a
// charge, take a payment, two night audits, check out, and close the books of the last day. Every step is checked
// against what the reports and the audit trail say afterwards.
func TestEndToEndStay(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali
	mustOK := func(what string, r response, want int) response {
		t.Helper()
		if r.status != want {
			t.Fatalf("%s: %d %v", what, r.status, r.body)
		}
		return r
	}

	var roomCode, minibar int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		switch m := c.(map[string]any); m["code"] {
		case "ROOM":
			roomCode = int64(m["id"].(float64))
		case "MINIBAR":
			minibar = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	r101 := idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"room_type_id": mustInt(dlx), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	mustOK("rates", abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"}), 200)
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Guest"}))

	// create (draft) -> confirm -> assign
	res := mustOK("create", abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "rooms": []map[string]any{
		{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "adult_count": 2, "child_count": 0}}},
		map[string]string{"Idempotency-Key": "res-1"}), 201)
	resID := idOf(res)
	lineID := idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)})
	conf := mustOK("confirm", abc.do(http.MethodPost, base+"/reservations/"+resID+"/confirm", map[string]any{"version": res.body["version"]}), 200)
	asg := mustOK("assign", abc.do(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/assign-room", map[string]any{"version": conf.body["version"], "room_id": mustInt(r101)}), 200)

	// check in
	ci := mustOK("check-in", abc.doWith(http.MethodPost, base+"/reservations/"+resID+"/rooms/"+lineID+"/check-in", map[string]any{
		"version": asg.body["version"], "room_id": mustInt(r101), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}, map[string]string{"Idempotency-Key": "ci-1"}), 201)
	stayID := idOf(response{body: ci.body["stay"].(map[string]any)})
	folioID := idOf(response{body: ci.body["folio"].(map[string]any)})

	// a charge and a payment
	mustOK("charge", abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/charges", map[string]any{"charge_code_id": minibar, "quantity": "1", "unit_price": "100000"}, map[string]string{"Idempotency-Key": "c1"}), 201)
	mustOK("payment", abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "500000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "p1"}), 201)

	// night audit 1 (30 Sep 20:00) and 2 (1 Oct 20:00)
	a1 := mustOK("audit 1", abc.do(http.MethodPost, base+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}), 200)
	if a1.body["new_business_date"] != "2026-10-01" || a1.body["room_charges_posted"].(float64) != 1 {
		t.Fatalf("audit 1: %v", a1.body)
	}
	e.clock.Set(time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC))
	abc = e.login("ABC") // the access token of the day before has expired
	a2 := mustOK("audit 2", abc.do(http.MethodPost, base+"/night-audit/run", map[string]any{"business_date": "2026-10-01"}), 200)
	if a2.body["new_business_date"] != "2026-10-02" || a2.body["room_charges_posted"].(float64) != 1 {
		t.Fatalf("audit 2: %v", a2.body)
	}

	// check out: the folio holds 2 nights + the minibar charge less the payment; it must be settled first
	st := mustOK("stay", abc.do(http.MethodGet, base+"/stays/"+stayID, nil), 200)
	folio := st.body["folios"].([]any)[0].(map[string]any)
	if folio["balance"] != "1600000" {
		t.Fatalf("balance: %v", folio)
	}
	out := base + "/stays/" + stayID + "/check-out"
	version := st.body["stay"].(map[string]any)["version"]
	if r := abc.doWith(http.MethodPost, out, map[string]any{"version": version}, map[string]string{"Idempotency-Key": "co-1"}); r.status != 409 || r.body["code"] != "FOLIO_NOT_BALANCED" {
		t.Fatalf("unbalanced check-out: %d %v", r.status, r.body)
	}
	mustOK("settle", abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "1600000", "payment_method": "CARD"}, map[string]string{"Idempotency-Key": "p2"}), 201)
	co := mustOK("check-out", abc.doWith(http.MethodPost, out, map[string]any{"version": version}, map[string]string{"Idempotency-Key": "co-2"}), 200)
	if co.body["stay"].(map[string]any)["status"] != "CHECKED_OUT" || co.body["housekeeping"] != "DIRTY" || len(co.body["posted_room_charges"].([]any)) != 0 {
		t.Fatalf("check-out: %v", co.body)
	}
	if f := mustOK("folio", abc.do(http.MethodGet, base+"/folios/"+folioID, nil), 200); f.body["status"] != "CLOSED" || f.body["balance"] != "0" {
		t.Fatalf("folio after check-out: %v", f.body)
	}

	// close the last day: nothing is in house any more
	e.clock.Set(time.Date(2026, 10, 2, 13, 0, 0, 0, time.UTC))
	abc = e.login("ABC") // the access token of the day before has expired
	a3 := mustOK("audit 3", abc.do(http.MethodPost, base+"/night-audit/run", map[string]any{"business_date": "2026-10-02"}), 200)
	sum3 := a3.body["summary"].(map[string]any)
	if a3.body["new_business_date"] != "2026-10-03" || sum3["departures"].(float64) != 1 || sum3["rooms"].(map[string]any)["occupied"].(float64) != 0 {
		t.Fatalf("audit 3: %v", a3.body)
	}

	// the books agree: revenue = 2 room nights + the minibar, cash = all payments, statistics = 2 nights sold
	rev := mustOK("revenue", abc.do(http.MethodGet, base+"/reports/revenue?from=2026-09-30&to=2026-10-02", nil), 200)
	if rev.body["totals"].(map[string]any)["net_amount"] != "2100000" {
		t.Fatalf("revenue: %v", rev.body["totals"])
	}
	cash := mustOK("cashier", abc.do(http.MethodGet, base+"/reports/cashier?from=2026-09-30&to=2026-10-02", nil), 200)
	if cash.body["net"] != "2100000" {
		t.Fatalf("cashier: %v", cash.body)
	}
	stats := mustOK("statistics", abc.do(http.MethodGet, base+"/reports/statistics?from=2026-09-30&to=2026-10-02", nil), 200)
	tt := stats.body["totals"].(map[string]any)
	if tt["room_nights_sold"].(float64) != 2 || tt["room_revenue"] != "2000000" || tt["days"].(float64) != 3 {
		t.Fatalf("statistics: %v", tt)
	}

	// and the audit trail tells the story, newest first
	trail := mustOK("audit trail", abc.do(http.MethodGet, base+"/audit-logs?limit=200", nil), 200)
	actions := map[string]int{}
	for _, a := range trail.body["data"].([]any) {
		actions[a.(map[string]any)["action"].(string)]++
	}
	for _, want := range []string{"reservation.created", "reservation.confirmed", "stay.checked_in", "stay.checked_out", "night_audit.completed", "business_day.closed"} {
		if actions[want] == 0 {
			t.Fatalf("no %s in the audit trail: %v", want, actions)
		}
	}
	if actions["night_audit.completed"] != 3 {
		t.Fatalf("three audits: %v", actions)
	}
	byStay := mustOK("trail by entity", abc.do(http.MethodGet, base+"/audit-logs?entity_type=stay&entity_id="+stayID, nil), 200)
	if len(byStay.body["data"].([]any)) < 2 {
		t.Fatalf("stay trail: %v", byStay.body)
	}
	paged := mustOK("page", abc.do(http.MethodGet, base+"/audit-logs?limit=2", nil), 200)
	if len(paged.body["data"].([]any)) != 2 || paged.body["next_cursor"] == nil || !strings.HasPrefix(paged.body["next_cursor"].(string), "") {
		t.Fatalf("paging: %v", paged.body)
	}
	if r := abc.do(http.MethodGet, base+"/audit-logs?user_id=abc", nil); r.status != 422 {
		t.Fatalf("bad filter: %d", r.status)
	}
}

func TestRateLimitAnswers429WithRetryAfter(t *testing.T) {
	e := newAPI(t)
	e.rateLimited(3)
	for i := 0; i < 3; i++ {
		if r := e.get("/healthz"); r != http.StatusOK {
			t.Fatalf("healthz %d", r)
		}
	}
	for i := 0; i < 3; i++ {
		if code := e.get("/api/v1/properties"); code == http.StatusTooManyRequests {
			t.Fatalf("request %d is within the burst", i)
		}
	}
	rec := e.getRec("/api/v1/properties")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "TOO_MANY_REQUESTS") {
		t.Fatalf("limited: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if e.get("/healthz") != http.StatusOK {
		t.Fatal("health probes are never limited")
	}
	e.clock.Advance(time.Minute)
	if e.get("/api/v1/properties") == http.StatusTooManyRequests {
		t.Fatal("the bucket refills with time")
	}
}
