package app

import (
	"net/http"
	"testing"
)

// The cancellation of a group through the real router and middleware (docs/architecture/30-group-folio.md part F): the right of the route, the shape of the answers and the boundaries of the tenants.

func TestTheGroupCancelRoutes(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	mine := buildWorld(t, abc, "BALI")
	theirs := buildWorld(t, xyz, "BALI")
	group := idOf(abc.do(http.MethodPost, mine.base+"/groups", groupBody("GRP1")))
	theirGroup := idOf(xyz.do(http.MethodPost, theirs.base+"/groups", groupBody("GRP1")))
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(mine.prop), "first_name": "Ayu", "last_name": "Peserta"}))
	abc.do(http.MethodPost, mine.base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, mine.base), "room_type_id": mustInt(mine.roomType), "room_number": "102", "initial_housekeeping_status": "CLEAN"}) // a room for the night
	res := abc.doWith(http.MethodPost, mine.base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "booking_group_id": mustInt(group), "rooms": []map[string]any{
		{"room_type_id": mustInt(mine.roomType), "rate_plan_id": mustInt(mine.plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-01", "adult_count": 2, "child_count": 0}}}, map[string]string{"Idempotency-Key": "res-gc"})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	row := func() map[string]any {
		return map[string]any{"reservation_id": mustInt(idOf(res)), "version": res.body["version"]}
	}
	body := func() map[string]any { return map[string]any{"reason": "the conference is off", "rows": []any{row()}} }
	path := mine.base + "/groups/" + group + "/cancellations"
	with := func(c *client, p, key string, b any) response {
		return c.doWith(http.MethodPost, p, b, map[string]string{"Idempotency-Key": key})
	}

	// the members of the group carry what a cancellation needs
	members := abc.do(http.MethodGet, mine.base+"/groups/"+group+"/reservations", nil)
	m := members.body["data"].([]any)[0].(map[string]any)
	if members.status != 200 || m["version"] == nil || m["rooms_cancellable"] != float64(1) || m["rooms_in_house_or_done"] != float64(0) || m["held_balance"] == nil {
		t.Fatalf("the member: %d %v", members.status, members.body)
	}

	// the request: the key and the reason are required, a request names reservations, and another tenant's group or property is not found
	if r := abc.do(http.MethodPost, path, body()); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	if r := with(abc, path, "k0", map[string]any{"rows": []any{row()}}); r.status != 422 {
		t.Fatalf("no reason: %d %v", r.status, r.body)
	}
	if r := with(abc, path, "k0", map[string]any{"reason": "off", "rows": []any{}}); r.status != 422 || r.body["code"] != "GROUP_CANCEL_EMPTY" {
		t.Fatalf("no rows: %d %v", r.status, r.body)
	}
	if r := with(abc, theirs.base+"/groups/"+theirGroup+"/cancellations", "k0", body()); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("their property: %d %v", r.status, r.body)
	}
	if r := with(abc, mine.base+"/groups/"+theirGroup+"/cancellations", "k0", body()); r.status != 404 || r.body["code"] != "GROUP_NOT_FOUND" {
		t.Fatalf("their group under my property: %d %v", r.status, r.body)
	}

	// the rights: cancelling a group needs group.cancel (reservation.cancel is not enough), reinstating needs reservation.reinstate, the holdings need folio.read
	role := func(name string, perms ...string) any {
		r := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": name, "permissions": perms})
		if r.status != 201 {
			t.Fatalf("role %s: %d %v", name, r.status, r.body)
		}
		return r.body["id"]
	}
	user := func(email string, roleID any) *client {
		grant := map[string]any{"property_id": mustInt(mine.prop), "role_id": roleID}
		if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": email, "full_name": email, "password": testPassword, "grants": []map[string]any{grant}}); r.status != 201 {
			t.Fatalf("user %s: %d %v", email, r.status, r.body)
		}
		c := &client{env: e}
		if r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": email, "password": testPassword}); r.status != 200 {
			t.Fatalf("login %s: %d %v", email, r.status, r.body)
		}
		return c
	}
	clerk := user("clerk@hotel.com", role("Clerk", "reservation.read", "reservation.cancel"))
	boss := user("boss@hotel.com", role("Boss", "reservation.read", "group.cancel", "folio.read"))
	if r := with(clerk, path, "k1", body()); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("a clerk who may cancel one reservation: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, mine.base+"/reservations/"+idOf(res), nil); r.body["status"] != "CONFIRMED" {
		t.Fatalf("nothing was cancelled by the refused request: %v", r.body["status"])
	}
	redo := user("redo@hotel.com", role("Redo", "reservation.read", "reservation.reinstate"))
	if r := with(redo, mine.base+"/groups/"+group+"/reinstatements", "k1", map[string]any{"rows": []any{row()}}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("the right of one reservation is not the right to bring a group back: %d %v", r.status, r.body)
	}
	if r := clerk.do(http.MethodGet, mine.base+"/groups/"+group+"/holdings", nil); r.status != 403 {
		t.Fatalf("the holdings need folio.read: %d", r.status)
	}

	// the request that goes through: 200, the row, the batch
	ok := with(boss, path, "k2", body())
	if ok.status != 200 || ok.body["batch_id"] != "k2" || ok.body["cancelled"] != float64(1) || ok.body["failed"] != float64(0) || ok.body["rooms_cancelled"] != float64(1) {
		t.Fatalf("the cancellation: %d %v", ok.status, ok.body)
	}
	first := ok.body["rows"].([]any)[0].(map[string]any)
	if first["status"] != "CANCELLED" || first["rooms"].([]any)[0].(map[string]any)["status"] != "CANCELLED" {
		t.Fatalf("the row: %v", first)
	}
	// the repeat under another key: answered as it was
	again := with(boss, path, "k3", body())
	if again.status != 200 || again.body["already_cancelled"] != float64(1) || again.body["cancelled"] != float64(0) {
		t.Fatalf("the repeat: %d %v", again.status, again.body)
	}
	if r := boss.do(http.MethodGet, mine.base+"/groups/"+group+"/holdings", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("holdings: %d %v", r.status, r.body)
	}
	g := abc.do(http.MethodGet, mine.base+"/groups/"+group, nil)
	if g.body["reservation_count"] != float64(0) || g.body["cancelled_reservation_count"] != float64(1) {
		t.Fatalf("the group is cancelled: no reservation left, one cancelled: %v", g.body)
	}
	// the active list leaves out a group whose reservations are all cancelled; the whole list keeps it
	listed := func(q string) bool {
		r := abc.do(http.MethodGet, mine.base+"/groups"+q, nil)
		for _, g := range r.body["data"].([]any) {
			if idOf(response{body: g.(map[string]any)}) == group {
				return true
			}
		}
		return false
	}
	if listed("?active=true") || !listed("") {
		t.Fatalf("a cancelled group is not in the active list (%v) and is in the whole list (%v)", listed("?active=true"), listed(""))
	}
	// the same person brings it back: one row, the batch, and the group is active again
	back := with(boss, mine.base+"/groups/"+group+"/reinstatements", "k4", map[string]any{"rows": []any{map[string]any{"reservation_id": mustInt(idOf(res)), "version": abc.do(http.MethodGet, mine.base+"/reservations/"+idOf(res), nil).body["version"]}}})
	if back.status != 200 || back.body["cancelled"] != float64(1) || back.body["rows"].([]any)[0].(map[string]any)["status"] != "REINSTATED" {
		t.Fatalf("the reinstatement: %d %v", back.status, back.body)
	}
	if !listed("?active=true") {
		t.Fatal("the group is active again")
	}
	// the folio filter of the follow-up is understood
	if r := abc.do(http.MethodGet, mine.base+"/folios?cancelled_with_balance=true", nil); r.status != 200 {
		t.Fatalf("the filter: %d %v", r.status, r.body)
	}
}
