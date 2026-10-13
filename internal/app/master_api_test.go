package app

import (
	"net/http"
	"strings"
	"testing"
)

// The master folio of a group and its routing, through the real router and middleware (docs/architecture/30-group-folio.md, part C): the shape of the answers, the right of each route, and the
// tenant and property boundaries.

func groupBody(code string) map[string]any {
	return map[string]any{"code": code, "name": "Conference " + code, "arrival_date": "2026-09-30", "departure_date": "2026-10-02", "contact_name": "Dewi"}
}

func TestTheMasterFolioRoutes(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	w := buildWorld(t, abc, "BALI")
	group := idOf(abc.do(http.MethodPost, w.base+"/groups", groupBody("GRP1")))

	// no master yet: the routing says so, and every list is empty (not null)
	r := abc.do(http.MethodGet, w.base+"/groups/"+group+"/routing", nil)
	if r.status != 200 || r.body["master"] != nil || len(r.body["instructions"].([]any)) != 0 || r.body["lines"] == nil {
		t.Fatalf("routing before: %d %v", r.status, r.body)
	}
	// opening it: 201, the folio is a MASTER without a reservation, and asking again is 200 with the same folio
	open := abc.do(http.MethodPost, w.base+"/groups/"+group+"/master-folio", nil)
	if open.status != 201 || open.body["created"] != true {
		t.Fatalf("open: %d %v", open.status, open.body)
	}
	master := idOf(response{body: open.body["master"].(map[string]any)})
	again := abc.do(http.MethodPost, w.base+"/groups/"+group+"/master-folio", nil)
	if again.status != 200 || again.body["created"] != false || idOf(response{body: again.body["master"].(map[string]any)}) != master {
		t.Fatalf("open again: %d %v", again.status, again.body)
	}
	f := abc.do(http.MethodGet, w.base+"/folios/"+master, nil)
	if f.status != 200 || f.body["folio_type"] != "MASTER" || f.body["reservation_id"] != nil || f.body["stay_id"] != nil || f.body["booking_group_code"] != "GRP1" || f.body["booking_group_contact_name"] != "Dewi" {
		t.Fatalf("the folio: %d %v", f.status, f.body)
	}
	list := abc.do(http.MethodGet, w.base+"/folios?folio_type=MASTER&booking_group_id="+group, nil)
	if list.status != 200 || len(list.body["data"].([]any)) != 1 {
		t.Fatalf("the list of masters: %d %v", list.status, list.body)
	}
	if row := list.body["data"].([]any)[0].(map[string]any); row["reservation_id"] != nil || row["booking_group_code"] != "GRP1" {
		t.Fatalf("the row of a master: %v", row)
	}

	// the routing: instructions in, validated, and read back; nothing is posted by it
	bad := abc.do(http.MethodPut, w.base+"/groups/"+group+"/routing", map[string]any{"instructions": []map[string]any{{"scope": "EVERYTHING"}}, "excluded_lines": []int{}})
	if bad.status != 422 {
		t.Fatalf("a bad scope: %d %v", bad.status, bad.body)
	}
	put := abc.do(http.MethodPut, w.base+"/groups/"+group+"/routing", map[string]any{"instructions": []map[string]any{{"scope": "ROOM"}}, "excluded_lines": []int{}})
	if put.status != 200 || len(put.body["instructions"].([]any)) != 1 || put.body["master"] == nil {
		t.Fatalf("put: %d %v", put.status, put.body)
	}
	posted := abc.do(http.MethodGet, w.base+"/groups/"+group+"/routing/posted", nil)
	if posted.status != 200 || posted.body["data"] == nil {
		t.Fatalf("posted: %d %v", posted.status, posted.body)
	}
	// the notice of a room of a reservation that is in no group
	n := abc.do(http.MethodGet, w.base+"/reservations/"+w.res+"/rooms/"+w.line+"/routing", nil)
	if n.status != 200 || n.body["group"] != nil || n.body["room"] != "LINE" || len(n.body["group_instructions"].([]any)) != 0 {
		t.Fatalf("the notice: %d %v", n.status, n.body)
	}
}

func TestTheMasterFolioRoutesAskTheirRightsAndStayInsideTheTenant(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	mine := buildWorld(t, abc, "BALI")
	theirs := buildWorld(t, xyz, "BALI")
	myGroup := idOf(abc.do(http.MethodPost, mine.base+"/groups", groupBody("GRP1")))
	theirGroup := idOf(xyz.do(http.MethodPost, theirs.base+"/groups", groupBody("GRP1")))
	theirMaster := idOf(response{body: xyz.do(http.MethodPost, theirs.base+"/groups/"+theirGroup+"/master-folio", nil).body["master"].(map[string]any)})
	opened := abc.do(http.MethodPost, mine.base+"/groups/"+myGroup+"/master-folio", nil)
	myMaster := idOf(response{body: opened.body["master"].(map[string]any)})

	type route struct {
		name, method, path string
		body               any
	}
	routes := func(base, group, line, res string) []route {
		return []route{
			{"routing read", http.MethodGet, base + "/groups/" + group + "/routing", nil},
			{"routing set", http.MethodPut, base + "/groups/" + group + "/routing", map[string]any{"instructions": []map[string]any{{"scope": "ROOM"}}, "excluded_lines": []int{}}},
			{"posted", http.MethodGet, base + "/groups/" + group + "/routing/posted", nil},
			{"master open", http.MethodPost, base + "/groups/" + group + "/master-folio", nil},
			{"line routing", http.MethodGet, base + "/reservations/" + res + "/rooms/" + line + "/routing", nil},
		}
	}
	// 1. their property in the path: it does not exist for this tenant
	for _, rt := range routes(theirs.base, theirGroup, theirs.line, theirs.res) {
		if r := abc.do(rt.method, rt.path, rt.body); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Errorf("%s with their property: %d %v", rt.name, r.status, r.body)
		}
	}
	// 2. my property, their ids: the group, the line and the folio are not found, and nothing of theirs is said
	for _, rt := range routes(mine.base, theirGroup, theirs.line, theirs.res) {
		r := abc.do(rt.method, rt.path, rt.body)
		code, _ := r.body["code"].(string)
		if r.status != 404 || !strings.HasSuffix(code, "_NOT_FOUND") {
			t.Errorf("%s with their ids under my property: %d %v", rt.name, r.status, r.body)
		}
	}
	if r := abc.do(http.MethodGet, mine.base+"/folios/"+theirMaster, nil); r.status != 404 || r.body["code"] != "FOLIO_NOT_FOUND" {
		t.Errorf("their master folio under my property: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, mine.base+"/folios/"+theirMaster+"/close", map[string]any{"version": 1}); r.status != 404 {
		t.Errorf("closing their master: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, theirs.base+"/folios/"+theirMaster, nil); r.status != 200 || r.body["status"] != "OPEN" {
		t.Errorf("their master is untouched: %d %v", r.status, r.body)
	}

	// 3. the rights: reading the routing needs reservation.read, setting it and opening the master folio.master_manage, closing the master folio.master_close
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
	reader := user("reader@hotel.com", role("Reader", "reservation.read", "folio.read"))
	cashier := user("cashier@hotel.com", role("Cashier", "reservation.read", "folio.read", "folio.post_charge", "payment.post"))
	manager := user("manager@hotel.com", role("Manager", "reservation.read", "folio.read", "folio.master_manage"))
	closer := user("closer@hotel.com", role("Closer", "reservation.read", "folio.read", "folio.master_close"))
	for _, rt := range routes(mine.base, myGroup, mine.line, mine.res) {
		read := strings.HasSuffix(rt.name, "read") || rt.name == "posted" || rt.name == "line routing"
		r := reader.do(rt.method, rt.path, rt.body)
		switch {
		case read && r.status != 200:
			t.Errorf("%s with the read rights: %d %v", rt.name, r.status, r.body)
		case !read && (r.status != 403 || r.body["code"] != "PERMISSION_DENIED"):
			t.Errorf("%s without folio.master_manage: %d %v, want 403", rt.name, r.status, r.body)
		}
	}
	// a cashier posts charges and takes payments, and may not manage or close the bill of a group
	if r := cashier.do(http.MethodPost, mine.base+"/groups/"+myGroup+"/master-folio", nil); r.status != 403 {
		t.Errorf("a cashier opens a master: %d %v", r.status, r.body)
	}
	if r := cashier.do(http.MethodPost, mine.base+"/folios/"+myMaster+"/close", map[string]any{"version": 1}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Errorf("a cashier closes a master: %d %v", r.status, r.body)
	}
	if r := manager.do(http.MethodPost, mine.base+"/folios/"+myMaster+"/close", map[string]any{"version": 1}); r.status != 403 {
		t.Errorf("a manager of the routing closes a master: %d %v", r.status, r.body)
	}
	if r := manager.do(http.MethodPut, mine.base+"/groups/"+myGroup+"/routing", map[string]any{"instructions": []map[string]any{{"scope": "ALL"}}, "excluded_lines": []int{}}); r.status != 200 {
		t.Errorf("a manager sets the routing: %d %v", r.status, r.body)
	}
	// a balance stops the close, whoever asks: 409 FOLIO_BALANCE_NOT_ZERO with the balance, and the master stays open
	ch := abc.doWith(http.MethodPost, mine.base+"/folios/"+myMaster+"/charges", map[string]any{"charge_code_id": mustInt(mine.minibar), "quantity": "1", "unit_price": "250000"}, map[string]string{"Idempotency-Key": "mc-1"})
	if ch.status != 201 {
		t.Fatalf("a charge on the master: %d %v", ch.status, ch.body)
	}
	mv := abc.do(http.MethodGet, mine.base+"/folios/"+myMaster, nil)
	owed := closer.do(http.MethodPost, mine.base+"/folios/"+myMaster+"/close", map[string]any{"version": mv.body["version"]})
	if owed.status != 409 || owed.body["code"] != "FOLIO_BALANCE_NOT_ZERO" {
		t.Errorf("closing a master with a balance: %d %v", owed.status, owed.body)
	}
	if ctx, _ := owed.body["context"].(map[string]any); ctx["balance"] == nil {
		t.Errorf("the refusal says the balance: %v", owed.body)
	}
	if got := abc.do(http.MethodGet, mine.base+"/folios/"+myMaster, nil); got.body["status"] != "OPEN" {
		t.Errorf("the master stays open: %v", got.body)
	}
	// paid, it closes: who holds folio.master_close closes it (the group has no room that is not over)
	if pay := abc.doWith(http.MethodPost, mine.base+"/folios/"+myMaster+"/payments", map[string]any{"amount": mv.body["balance"], "payment_method": "CARD"}, map[string]string{"Idempotency-Key": "mc-2"}); pay.status != 201 {
		t.Fatalf("the payment: %d %v", pay.status, pay.body)
	}
	mv = abc.do(http.MethodGet, mine.base+"/folios/"+myMaster, nil)
	r := closer.do(http.MethodPost, mine.base+"/folios/"+myMaster+"/close", map[string]any{"version": mv.body["version"]})
	if r.status != 200 || r.body["status"] != "CLOSED" {
		t.Errorf("a closer closes the master: %d %v", r.status, r.body)
	}
}
