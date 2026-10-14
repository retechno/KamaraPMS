package app

import (
	"net/http"
	"testing"
)

// The group check-in through the real router and middleware (docs/architecture/30-group-folio.md part E): the shape of the answers, the keys that come from the room, the right of each route, and the
// tenant and property boundaries.

func TestTheGroupCheckInRoute(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	mine := buildWorld(t, abc, "BALI")
	theirs := buildWorld(t, xyz, "BALI")
	group := idOf(abc.do(http.MethodPost, mine.base+"/groups", groupBody("GRP1")))
	theirGroup := idOf(xyz.do(http.MethodPost, theirs.base+"/groups", groupBody("GRP1")))
	guest := idOf(abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(mine.prop), "first_name": "Ayu", "last_name": "Peserta"}))
	room := idOf(abc.do(http.MethodPost, mine.base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, mine.base), "room_type_id": mustInt(mine.roomType), "room_number": "102", "initial_housekeeping_status": "CLEAN"}))
	res := abc.doWith(http.MethodPost, mine.base+"/reservations", map[string]any{"guest_id": mustInt(guest), "source": "PHONE", "confirm": true, "booking_group_id": mustInt(group), "rooms": []map[string]any{
		{"room_type_id": mustInt(mine.roomType), "rate_plan_id": mustInt(mine.plan), "arrival_date": "2026-09-30", "departure_date": "2026-10-01", "adult_count": 2, "child_count": 0}}}, map[string]string{"Idempotency-Key": "res-gci"})
	if res.status != 201 {
		t.Fatalf("reservation: %d %v", res.status, res.body)
	}
	line := idOf(response{body: res.body["rooms"].([]any)[0].(map[string]any)})
	row := func() map[string]any {
		return map[string]any{"line_id": mustInt(line), "version": res.body["version"], "room_id": mustInt(room), "guest_id": mustInt(guest), "adult_count": 2, "child_count": 0}
	}
	path := mine.base + "/groups/" + group + "/check-ins"
	with := func(c *client, p, key string, body any) response {
		return c.doWith(http.MethodPost, p, body, map[string]string{"Idempotency-Key": key})
	}

	// the arrivals of the group: the filter and the facts of the guest
	arr := abc.do(http.MethodGet, mine.base+"/arrivals?booking_group_id="+group+"&with_estimate=true", nil)
	if arr.status != 200 || len(arr.body["data"].([]any)) != 1 {
		t.Fatalf("arrivals of the group: %d %v", arr.status, arr.body)
	}
	if a := arr.body["data"].([]any)[0].(map[string]any); a["guest_has_id"] != false || a["guest_is_placeholder"] != false || a["estimate_total"] == nil || a["readiness"] == nil {
		t.Fatalf("the row of an arrival: %v", a)
	}
	if r := abc.do(http.MethodGet, mine.base+"/arrivals?booking_group_id=zero", nil); r.status != 422 {
		t.Fatalf("a bad filter: %d %v", r.status, r.body)
	}

	// the request: the key is required, a request names rooms, and a group of another tenant or property is not found
	if r := abc.do(http.MethodPost, path, map[string]any{"rows": []any{row()}}); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	if r := with(abc, path, "k0", map[string]any{"rows": []any{}}); r.status != 422 || r.body["code"] != "GROUP_CHECKIN_EMPTY" {
		t.Fatalf("no rows: %d %v", r.status, r.body)
	}
	if r := with(abc, theirs.base+"/groups/"+theirGroup+"/check-ins", "k0", map[string]any{"rows": []any{row()}}); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("their property: %d %v", r.status, r.body)
	}
	if r := with(abc, mine.base+"/groups/"+theirGroup+"/check-ins", "k0", map[string]any{"rows": []any{row()}}); r.status != 404 || r.body["code"] != "GROUP_NOT_FOUND" {
		t.Fatalf("their group under my property: %d %v", r.status, r.body)
	}

	// the rights: checking in needs frontdesk.checkin; a deposit row needs payment.post, and without it the whole request is refused before anything is written
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
	reader := user("reader@hotel.com", role("Reader", "reservation.read", "guest.read"))
	clerk := user("clerk@hotel.com", role("Clerk", "reservation.read", "guest.read", "frontdesk.checkin"))
	if r := with(reader, path, "k1", map[string]any{"rows": []any{row()}}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("a reader: %d %v", r.status, r.body)
	}
	withDeposit := row()
	withDeposit["deposit"] = map[string]any{"amount": "100000", "payment_method": "BANK_TRANSFER"}
	if r := with(clerk, path, "k2", map[string]any{"rows": []any{withDeposit}}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("a deposit without payment.post: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, mine.base+"/stays?limit=50", nil); len(r.body["data"].([]any)) != 1 { // only the stay of the world's own reservation
		t.Fatalf("nothing was checked in by the refused requests: %v", r.body)
	}

	// the request that goes through: 200, the stay, and the batch
	ok := with(clerk, path, "k3", map[string]any{"rows": []any{row()}})
	if ok.status != 200 || ok.body["batch_id"] != "k3" || ok.body["checked_in"] != float64(1) || ok.body["failed"] != float64(0) {
		t.Fatalf("the check-in: %d %v", ok.status, ok.body)
	}
	first := ok.body["rows"].([]any)[0].(map[string]any)
	if first["status"] != "CHECKED_IN" || first["room_number"] != "102" || first["stay"].(map[string]any)["stay_number"] == "" {
		t.Fatalf("the row: %v", first)
	}
	// the same room again under ANOTHER key: the same stay, nothing done twice
	again := with(clerk, path, "k4", map[string]any{"rows": []any{row()}})
	second := again.body["rows"].([]any)[0].(map[string]any)
	if again.status != 200 || second["status"] != "CHECKED_IN" || second["stay"].(map[string]any)["id"] != first["stay"].(map[string]any)["id"] {
		t.Fatalf("the repeat: %d %v", again.status, again.body)
	}
	// a room of the group that does not arrive: told on its row, with the answer still 200
	bad := row()
	bad["line_id"] = mustInt(w0(mine.line))
	if r := with(clerk, path, "k5", map[string]any{"rows": []any{bad}}); r.status != 200 || r.body["failed"] != float64(1) || r.body["rows"].([]any)[0].(map[string]any)["code"] != "LINE_NOT_IN_GROUP" {
		t.Fatalf("a room that is not of the group: %d %v", r.status, r.body)
	}
}

// w0 is the id as it is, so the name says what the test means: the room of the world's own reservation, which is not in the group.
func w0(id string) string { return id }
