package app

import (
	"net/http"
	"testing"
	"time"
)

// userWith makes a user with a role that holds exactly these permissions on the property, and signs them in.
func userWith(t *testing.T, e *apiEnv, admin *client, propertyID, email string, permissions ...string) *client {
	t.Helper()
	if permissions == nil {
		permissions = []string{}
	}
	role := admin.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Role " + email, "permissions": permissions})
	if role.status != http.StatusCreated {
		t.Fatalf("role: %d %v", role.status, role.body)
	}
	user := admin.do(http.MethodPost, "/api/v1/users", map[string]any{
		"email": email, "full_name": "User " + email, "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(propertyID), "role_id": role.body["id"]}},
	})
	if user.status != http.StatusCreated {
		t.Fatalf("user: %d %v", user.status, user.body)
	}
	c := &client{env: e}
	if r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": email, "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d %v", r.status, r.body)
	}
	return c
}

func objectOf(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not an object: %v", v)
	}
	return m
}

// The room panel over HTTP: one call that says who is in the room, what the folio holds, who comes next, and what happened today, with each part only for the callers who may read it.
func TestRoomPanelAPI(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	codes := map[string]int64{}
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		m := c.(map[string]any)
		codes[m["code"].(string)] = int64(m["id"].(float64))
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	room := func(number, status string) string {
		return idOf(abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": number, "initial_housekeeping_status": status}))
	}
	r101, r102, r103 := room("101", "CLEAN"), room("102", "CLEAN"), room("103", "DIRTY")
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": codes["ROOM"]}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "Pratiwi", "first_name": "Ayu"}))
	book := func(arrival, departure, key, request string) response {
		res := abc.doWith(http.MethodPost, base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "special_request": request, "rooms": []map[string]any{
			{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "arrival_date": arrival, "departure_date": departure, "adult_count": 2, "child_count": 0}}},
			map[string]string{"Idempotency-Key": key})
		if res.status != http.StatusCreated {
			t.Fatalf("reservation: %d %v", res.status, res.body)
		}
		return res
	}
	lineOf := func(res response) string { return idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)}) }

	// 101: a guest checked in today for two nights, who asked for something, paid part of a bill.
	in := book("2026-09-30", "2026-10-02", "res-in", "high floor, peanut allergy")
	ci := abc.doWith(http.MethodPost, base+"/reservations/"+idOf(in)+"/rooms/"+lineOf(in)+"/check-in",
		map[string]any{"version": in.body["version"], "room_id": mustInt(r101), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}, map[string]string{"Idempotency-Key": "ci-1"})
	if ci.status != http.StatusCreated {
		t.Fatalf("check-in: %d %v", ci.status, ci.body)
	}
	folioID := idOf(response{body: ci.body["folio"].(map[string]any)})
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/charges", map[string]any{"charge_code_id": codes["MINIBAR"], "quantity": "2", "unit_price": "50000"}, map[string]string{"Idempotency-Key": "ch-1"}); r.status != http.StatusCreated {
		t.Fatalf("charge: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, base+"/folios/"+folioID+"/payments", map[string]any{"amount": "40000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "pay-1"}); r.status != http.StatusCreated {
		t.Fatalf("payment: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/rooms/"+r101+"/housekeeping", map[string]any{"status": "DIRTY", "notes": "guest asked for make-up"}); r.status != 200 {
		t.Fatalf("housekeeping: %d %v", r.status, r.body)
	}

	// 103: empty, with a guest booked into it for tomorrow.
	next := book("2026-10-01", "2026-10-03", "res-next", "")
	if r := abc.do(http.MethodPost, base+"/reservations/"+idOf(next)+"/rooms/"+lineOf(next)+"/assign-room", map[string]any{"version": next.body["version"], "room_id": mustInt(r103)}); r.status != 200 {
		t.Fatalf("assign: %d %v", r.status, r.body)
	}

	summary := func(c *client, room string) response {
		return c.do(http.MethodGet, base+"/rooms/"+room+"/summary", nil)
	}

	t.Run("an occupied room", func(t *testing.T) {
		r := summary(abc, r101)
		if r.status != 200 {
			t.Fatalf("summary: %d %v", r.status, r.body)
		}
		room, stay, fol := objectOf(t, r.body["room"]), objectOf(t, r.body["stay"]), objectOf(t, r.body["folio"])
		if room["room_number"] != "101" || room["occupancy"] != "OCCUPIED" || room["status"] != "DIRTY" || r.body["business_date"] != "2026-09-30" {
			t.Fatalf("room: %v", r.body)
		}
		if a := objectOf(t, r.body["access"]); a["stay"] != true || a["folio"] != true || a["arrival"] != false || a["audit"] != true {
			t.Fatalf("access: %v", a)
		}
		if objectOf(t, stay["guest"])["name"] != "Ayu Pratiwi" || stay["night"] != 1.0 || stay["nights"] != 2.0 || stay["departure_date"] != "2026-10-02" ||
			stay["special_request"] != "high floor, peanut allergy" || stay["source"] != "PHONE" || stay["adult_count"] != 2.0 {
			t.Fatalf("stay: %v", stay)
		}
		if fol["charges"] != "100000" || fol["paid"] != "40000" || fol["balance"] != "60000" || len(fol["folios"].([]any)) != 1 {
			t.Fatalf("folio: %v", fol)
		}
		// the folio page says the same
		page := abc.do(http.MethodGet, base+"/folios/"+folioID, nil)
		if page.body["balance"] != fol["balance"] || objectOf(t, page.body["totals"])["debit"] != fol["charges"] || objectOf(t, page.body["totals"])["credit"] != fol["paid"] {
			t.Fatalf("folio page: %v", page.body)
		}
		if r.body["next_arrival"] != nil {
			t.Fatalf("an occupied room has no next arrival here: %v", r.body["next_arrival"])
		}
		var cleaning, checkedIn bool
		for _, h := range r.body["history"].([]any) {
			item := objectOf(t, h)
			cleaning = cleaning || (item["kind"] == "CLEANING" && item["to"] == "DIRTY" && item["notes"] == "guest asked for make-up")
			checkedIn = checkedIn || (item["kind"] == "AUDIT" && item["action"] == "stay.checked_in")
			if item["action"] == "housekeeping.changed" {
				t.Fatalf("a cleaning change is listed once: %v", item)
			}
		}
		if !cleaning || !checkedIn {
			t.Fatalf("history: %v", r.body["history"])
		}
	})

	t.Run("an empty room shows who comes next", func(t *testing.T) {
		r := summary(abc, r103)
		if r.status != 200 || r.body["stay"] != nil || r.body["folio"] != nil {
			t.Fatalf("summary: %d %v", r.status, r.body)
		}
		if a := objectOf(t, r.body["access"]); a["stay"] != true || a["folio"] != true || a["arrival"] != true {
			t.Fatalf("access: %v", a)
		}
		n := objectOf(t, r.body["next_arrival"])
		if n["guest_name"] != "Ayu Pratiwi" || n["arrival_date"] != "2026-10-01" || n["nights"] != 2.0 || n["confirmation_number"] == nil {
			t.Fatalf("next arrival: %v", n)
		}
		if room := summary(abc, r102); room.body["next_arrival"] != nil || room.body["stay"] != nil || objectOf(t, room.body["room"])["occupancy"] != "VACANT" {
			t.Fatalf("a free room: %v", room.body)
		}
	})

	t.Run("each part needs the permission of its module", func(t *testing.T) {
		none := userWith(t, e, abc, bali, "none@hotel.com")
		r := summary(none, r101)
		if r.status != 200 || r.body["stay"] != nil || r.body["folio"] != nil || r.body["next_arrival"] != nil || objectOf(t, r.body["room"])["room_number"] != "101" {
			t.Fatalf("no permission: %d %v", r.status, r.body)
		}
		if a := objectOf(t, r.body["access"]); a["stay"] != false || a["folio"] != false || a["arrival"] != false || a["audit"] != false {
			t.Fatalf("access without permissions: %v", a)
		}
		for _, h := range r.body["history"].([]any) {
			if objectOf(t, h)["kind"] != "CLEANING" {
				t.Fatalf("only the cleaning without audit.read: %v", h)
			}
		}

		desk := userWith(t, e, abc, bali, "desk@hotel.com", "reservation.read")
		r = summary(desk, r101)
		if a := objectOf(t, r.body["access"]); a["stay"] != true || a["folio"] != false || a["audit"] != false || r.body["stay"] == nil || r.body["folio"] != nil {
			t.Fatalf("reservation.read: %v", r.body)
		}
		if s := summary(desk, r103); objectOf(t, s.body["access"])["arrival"] != true || s.body["next_arrival"] == nil {
			t.Fatalf("the next arrival is for reservation.read: %v", s.body)
		}

		cashier := userWith(t, e, abc, bali, "cashier@hotel.com", "reservation.read", "folio.read", "audit.read")
		r = summary(cashier, r101)
		if a := objectOf(t, r.body["access"]); a["folio"] != true || a["audit"] != true || objectOf(t, r.body["folio"])["balance"] != "60000" {
			t.Fatalf("folio.read and audit.read: %v", r.body)
		}
	})

	t.Run("today is the business date, not the date of the server", func(t *testing.T) {
		e.clock.Advance(30 * time.Hour) // the server is a day and more ahead of the open business day
		defer e.clock.Advance(-30 * time.Hour)
		r := summary(e.login("ABC"), r101) // a new sign-in: the old token has expired by the clock
		if r.status != 200 || r.body["business_date"] != "2026-09-30" || objectOf(t, r.body["stay"])["night"] != 1.0 {
			t.Fatalf("summary: %d %v", r.status, r.body)
		}
		var today bool
		for _, h := range r.body["history"].([]any) {
			today = today || objectOf(t, h)["action"] == "stay.checked_in"
		}
		if !today {
			t.Fatalf("the history is of the business date: %v", r.body["history"])
		}
	})

	t.Run("a room of somebody else is not found", func(t *testing.T) {
		if r := summary(abc, "999999"); r.status != 404 || r.body["code"] != "ROOM_NOT_FOUND" {
			t.Fatalf("unknown room: %d %v", r.status, r.body)
		}
		if r := summary(abc, "0"); r.status != 404 || r.body["code"] != "ROOM_NOT_FOUND" {
			t.Fatalf("room 0: %d %v", r.status, r.body)
		}
		if r := xyz.do(http.MethodGet, base+"/rooms/"+r101+"/summary", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("another tenant: %d %v", r.status, r.body)
		}
		// a second property of the same tenant: its rooms are not reachable through the first
		other := validProperty()
		other["code"], other["name"] = "UBUD", "Hotel Ubud"
		ubud := idOf(abc.do(http.MethodPost, "/api/v1/properties", other))
		if r := abc.do(http.MethodGet, "/api/v1/properties/"+ubud+"/rooms/"+r101+"/summary", nil); r.status != 404 {
			t.Fatalf("another property: %d %v", r.status, r.body)
		}
	})
}
