package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// Audit F-09: the routes that Architecture 18 added or changed (transfer of a charge, billing instructions, rate restrictions, the fee, the stay lifecycle) are tested as a user
// of another tenant, as a user without the permission and as a user whose permission is on another property. The router, the middleware, the services and the database are the real ones; nothing
// is switched off for the test.

// world is one property with a room line billed to a company, checked in: everything the routes below act on.
type world struct {
	c                                                                  *client
	prop, base                                                         string
	roomType, plan, company, res, line, stay, guestFolio, companyFolio string
	item, payment, minibar                                             string
	folioNumber                                                        string
}

func buildWorld(t *testing.T, c *client, code string) world {
	t.Helper()
	pr := validProperty()
	pr["code"] = code
	w := world{c: c}
	w.prop = idOf(c.do(http.MethodPost, "/api/v1/properties", pr))
	w.base = "/api/v1/properties/" + w.prop
	var roomCode int64
	for _, cc := range c.do(http.MethodGet, w.base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		switch m := cc.(map[string]any); m["code"] {
		case "ROOM":
			roomCode = int64(m["id"].(float64))
		case "MINIBAR":
			w.minibar = idOf(response{body: m})
		}
	}
	w.roomType = idOf(c.do(http.MethodPost, w.base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	room := idOf(c.do(http.MethodPost, w.base+"/rooms", map[string]any{"bed_type_id": bedOf(c, w.base), "room_type_id": mustInt(w.roomType), "room_number": "101", "initial_housekeeping_status": "CLEAN"}))
	w.plan = idOf(c.do(http.MethodPost, w.base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	c.do(http.MethodPut, w.base+"/rates", map[string]any{"rate_plan_id": mustInt(w.plan), "room_type_ids": []int64{mustInt(w.roomType)}, "from": "2026-09-30", "to": "2026-10-10", "amount": "1000000"})
	guest := idOf(c.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(w.prop), "first_name": "Siti", "last_name": "Nurhaliza"}))
	w.company = idOf(c.do(http.MethodPost, w.base+"/companies", map[string]any{"code": "ACME", "name": "Acme Corp", "credit_limit": "5000000", "payment_terms_days": 30}))
	res := c.doWith(http.MethodPost, w.base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "rooms": []map[string]any{
		{"room_type_id": mustInt(w.roomType), "rate_plan_id": mustInt(w.plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "adult_count": 2, "child_count": 0}}}, map[string]string{"Idempotency-Key": "res-" + code})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	w.res = idOf(res)
	w.line = idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)})
	if r := c.do(http.MethodPut, w.base+"/reservations/"+w.res+"/rooms/"+w.line+"/billing-instructions", map[string]any{"instructions": []map[string]any{{"scope": "ROOM", "company_id": mustInt(w.company)}}}); r.status != 200 {
		t.Fatalf("instructions: %d %v", r.status, r.body)
	}
	ci := c.doWith(http.MethodPost, w.base+"/reservations/"+w.res+"/rooms/"+w.line+"/check-in", map[string]any{
		"version": res.body["version"], "room_id": mustInt(room), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}, map[string]string{"Idempotency-Key": "ci-" + code})
	if ci.status != 201 {
		t.Fatalf("check-in: %d %v", ci.status, ci.body)
	}
	w.stay = idOf(response{body: ci.body["stay"].(map[string]any)})
	for _, f := range c.do(http.MethodGet, w.base+"/folios?stay_id="+w.stay, nil).body["data"].([]any) {
		m := f.(map[string]any)
		if m["folio_type"] == "COMPANY" {
			w.companyFolio = idOf(response{body: m})
		} else {
			w.guestFolio = idOf(response{body: m})
			w.folioNumber, _ = m["folio_number"].(string)
		}
	}
	ch := c.doWith(http.MethodPost, w.base+"/folios/"+w.guestFolio+"/charges", map[string]any{"charge_code_id": mustInt(w.minibar), "quantity": "1", "unit_price": "250000"}, map[string]string{"Idempotency-Key": "c-" + code})
	if ch.status != 201 {
		t.Fatalf("charge: %d %v", ch.status, ch.body)
	}
	w.item = idOf(response{body: ch.body["item"].(map[string]any)})
	pay := c.doWith(http.MethodPost, w.base+"/folios/"+w.guestFolio+"/payments", map[string]any{"amount": "100000", "payment_method": "CASH"}, map[string]string{"Idempotency-Key": "p-" + code})
	if pay.status != 201 {
		t.Fatalf("payment: %d %v", pay.status, pay.body)
	}
	w.payment = idOf(response{body: pay.body["payment"].(map[string]any)})
	return w
}

type call struct {
	name, method, path string
	body               any
	idem               bool
}

// calls are the routes of the finding, written against one world; the same list is used for each kind of caller.
func (w world) calls() []call {
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}
	b := w.base
	return []call{
		{"folio read", http.MethodGet, b + "/folios/" + w.guestFolio, nil, false},
		{"folio charge", http.MethodPost, b + "/folios/" + w.guestFolio + "/charges", map[string]any{"charge_code_id": mustInt(w.minibar), "quantity": "1", "unit_price": "1000"}, true},
		{"folio payment", http.MethodPost, b + "/folios/" + w.guestFolio + "/payments", map[string]any{"amount": "1000", "payment_method": "CASH"}, true},
		{"item transfer", http.MethodPost, b + "/folio-items/" + w.item + "/transfer", map[string]any{"folio_id": mustInt(w.companyFolio), "reason": "company pays", "approval": approval}, false},
		{"item reverse", http.MethodPost, b + "/folio-items/" + w.item + "/reverse", map[string]any{"reason": "typo", "approval": approval}, false},
		{"item group", http.MethodPatch, b + "/folio-items/" + w.item + "/group", map[string]any{"group_code": "B"}, false},
		{"payment void", http.MethodPost, b + "/payments/" + w.payment + "/void", map[string]any{"reason": "typo", "approval": approval}, false},
		{"payment refund", http.MethodPost, b + "/payments/" + w.payment + "/refunds", map[string]any{"amount": "1000", "reason": "x", "approval": approval}, true},
		{"reservation read", http.MethodGet, b + "/reservations/" + w.res, nil, false},
		{"reservation fee", http.MethodPost, b + "/reservations/" + w.res + "/fees", map[string]any{"type": "CANCEL_FEE", "amount": "1000", "reason": "x"}, false},
		{"instructions read", http.MethodGet, b + "/reservations/" + w.res + "/rooms/" + w.line + "/billing-instructions", nil, false},
		{"instructions set", http.MethodPut, b + "/reservations/" + w.res + "/rooms/" + w.line + "/billing-instructions", map[string]any{"instructions": []map[string]any{{"scope": "ROOM", "company_id": mustInt(w.company)}}}, false},
		{"restriction fill", http.MethodPut, b + "/rate-restrictions", map[string]any{"room_type_ids": []int64{mustInt(w.roomType)}, "from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": true}}, false},
		{"stay read", http.MethodGet, b + "/stays/" + w.stay, nil, false},
		{"stay reverse check-in", http.MethodPost, b + "/stays/" + w.stay + "/reverse-check-in", map[string]any{"version": 1, "reason": "x"}, false},
		{"stay check-out", http.MethodPost, b + "/stays/" + w.stay + "/check-out", map[string]any{"version": 1}, true},
	}
}

func (cl call) run(c *client, key string) response {
	if cl.idem {
		return c.doWith(cl.method, cl.path, cl.body, map[string]string{"Idempotency-Key": key})
	}
	return c.do(cl.method, cl.path, cl.body)
}

// state is what must not move when a refused call was refused: the folios' ledger, the restrictions and the stay.
func (w world) state(t *testing.T) string {
	t.Helper()
	var parts []string
	for _, f := range []string{w.guestFolio, w.companyFolio} {
		r := w.c.do(http.MethodGet, w.base+"/folios/"+f, nil)
		if r.status != 200 {
			t.Fatalf("folio %s: %d %v", f, r.status, r.body)
		}
		parts = append(parts, f+":"+asString(r.body["balance"])+":"+asString(r.body["version"])+":"+lenOf(r.body["items"]))
	}
	st := w.c.do(http.MethodGet, w.base+"/stays/"+w.stay, nil)
	parts = append(parts, "stay:"+asString(st.body["stay"].(map[string]any)["status"])+":"+asString(st.body["stay"].(map[string]any)["version"]))
	rr := w.c.do(http.MethodGet, w.base+"/rate-restrictions?from=2026-12-01&to=2027-01-01", nil)
	parts = append(parts, "restrictions:"+lenOf(rr.body["data"]))
	ins := w.c.do(http.MethodGet, w.base+"/reservations/"+w.res+"/rooms/"+w.line+"/billing-instructions", nil)
	parts = append(parts, "instructions:"+lenOf(ins.body["instructions"]))
	return strings.Join(parts, "|")
}

func asString(v any) string { return fmt.Sprint(v) }

func lenOf(v any) string {
	if l, ok := v.([]any); ok {
		return fmt.Sprint(len(l))
	}
	return "-"
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestArchitecture18RoutesDoNotCrossTheTenantBoundary(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	mine := buildWorld(t, abc, "BALI")
	theirs := buildWorld(t, xyz, "BALI")
	before := theirs.state(t)
	mineBefore := mine.state(t)

	// 1. their property in the path: the property itself is unknown to this tenant
	for _, cl := range theirs.calls() {
		r := cl.run(abc, "x-"+cl.name)
		if r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Errorf("%s with their property: %d %v, want 404 PROPERTY_NOT_FOUND", cl.name, r.status, r.body)
		}
	}
	// 2. my property in the path, their object ids: the object does not exist for this tenant, and nothing of it is shown
	ghost := theirs
	ghost.base, ghost.prop = mine.base, mine.prop
	for _, cl := range ghost.calls() {
		r := cl.run(abc, "y-"+cl.name)
		code, _ := r.body["code"].(string)
		if r.status != 404 || !strings.HasSuffix(code, "_NOT_FOUND") {
			t.Errorf("%s with their object under my property: %d %v, want 404 *_NOT_FOUND", cl.name, r.status, r.body)
		}
		if theirs.folioNumber != "" && strings.Contains(string(mustJSON(r.body)), theirs.folioNumber) {
			t.Errorf("%s leaks their folio number: %v", cl.name, r.body)
		}
	}
	// 3. a list of mine filtered by their stay shows nothing of theirs
	if r := abc.do(http.MethodGet, mine.base+"/folios?stay_id="+theirs.stay, nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Errorf("their stay in my folio list: %d %v", r.status, r.body)
	}
	// and neither side moved
	if got := theirs.state(t); got != before {
		t.Errorf("their state changed: %s then %s", before, got)
	}
	if got := mine.state(t); got != mineBefore {
		t.Errorf("my state changed: %s then %s", mineBefore, got)
	}
}

func TestArchitecture18RoutesAskTheRightPermissionOnTheRightProperty(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	bali := buildWorld(t, abc, "BALI")
	jkt := buildWorld(t, abc, "JKT")

	role := func(name string, perms ...string) any {
		r := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": name, "permissions": perms})
		if r.status != 201 {
			t.Fatalf("role %s: %d %v", name, r.status, r.body)
		}
		return r.body["id"]
	}
	user := func(email string, grants ...map[string]any) *client {
		if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": email, "full_name": email, "password": testPassword, "grants": grants}); r.status != 201 {
			t.Fatalf("user %s: %d %v", email, r.status, r.body)
		}
		c := &client{env: e}
		if r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": email, "password": testPassword}); r.status != 200 {
			t.Fatalf("login %s: %d %v", email, r.status, r.body)
		}
		return c
	}
	grant := func(prop string, roleID any) map[string]any {
		return map[string]any{"property_id": mustInt(prop), "role_id": roleID}
	}

	readOnly := role("Read only", "reservation.read", "folio.read")
	office := role("Office", "reservation.read", "reservation.update", "folio.read", "folio.post_charge", "folio.reverse", "rate.manage")

	// a user with no permission to change anything: every changing route answers 403, the reading ones 200
	clerk := user("clerk@hotel.com", grant(bali.prop, readOnly))
	changing := map[string]string{"folio charge": "folio.post_charge", "folio payment": "payment.post", "item transfer": "folio.reverse", "item reverse": "folio.reverse", "payment void": "payment.void", "payment refund": "payment.refund",
		"reservation fee": "folio.post_charge", "instructions set": "reservation.update", "restriction fill": "rate.manage", "stay reverse check-in": "frontdesk.reverse_checkin", "stay check-out": "frontdesk.checkout"}
	before := bali.state(t)
	for _, cl := range bali.calls() {
		r := cl.run(clerk, "clerk-"+cl.name)
		if _, mustAsk := changing[cl.name]; mustAsk {
			if r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
				t.Errorf("%s without %s: %d %v, want 403 PERMISSION_DENIED", cl.name, changing[cl.name], r.status, r.body)
			}
			continue
		}
		if strings.HasSuffix(cl.name, "read") && r.status != 200 {
			t.Errorf("%s with the read permission: %d %v", cl.name, r.status, r.body)
		}
	}
	if got := bali.state(t); got != before {
		t.Errorf("a refused call changed the property: %s then %s", before, got)
	}
	// the clerk has no grant on the other property: it does not exist for them
	for _, cl := range jkt.calls() {
		if r := cl.run(clerk, "clerk-j-"+cl.name); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Errorf("%s on a property without a grant: %d %v", cl.name, r.status, r.body)
		}
	}

	// the right permission works, on the property it is granted on, and only there: the office role is on Bali, a read-only role on Jakarta
	desk := user("desk@hotel.com", grant(bali.prop, office), grant(jkt.prop, readOnly))
	jktBefore := jkt.state(t)
	allowedOnBali := []string{"folio charge", "restriction fill", "instructions set", "item transfer"}
	for _, name := range allowedOnBali {
		for _, cl := range bali.calls() {
			if cl.name != name {
				continue
			}
			r := cl.run(desk, "desk-"+name)
			if r.status != 200 && r.status != 201 {
				t.Errorf("%s with the right permission on Bali: %d %v", name, r.status, r.body)
			}
		}
	}
	for _, name := range allowedOnBali {
		for _, cl := range jkt.calls() {
			if cl.name != name {
				continue
			}
			if r := cl.run(desk, "desk-j-"+name); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
				t.Errorf("%s on Jakarta with a read-only role there: %d %v, want 403 PERMISSION_DENIED", name, r.status, r.body)
			}
		}
	}
	if got := jkt.state(t); got != jktBefore {
		t.Errorf("a refused call changed Jakarta: %s then %s", jktBefore, got)
	}
	// the transfer really happened where it was allowed
	if r := abc.do(http.MethodGet, bali.base+"/folios/"+bali.companyFolio, nil); r.status != 200 || r.body["balance"] == "0" {
		t.Errorf("the transfer by the right user: %d %v", r.status, r.body)
	}
}
