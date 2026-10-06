package app

import (
	"net/http"
	"strconv"
	"testing"
)

func TestRoomsHousekeepingAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	// Room type: create, validate, duplicate, patch.
	rt := abc.do(http.MethodPost, base+"/room-types", map[string]any{
		"code": "dlx", "name": "Deluxe", "max_adult": 2, "max_child": 1, "max_occupancy": 3, "base_occupancy": 2,
	})
	if rt.status != http.StatusCreated || rt.body["code"] != "DLX" || rt.body["is_active"] != true {
		t.Fatalf("create room type: %d %v", rt.status, rt.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "X", "name": "x"}); r.status != 422 || fieldsOf(r)["max_adult"] != "REQUIRED" {
		t.Fatalf("missing capacity: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-types", map[string]any{
		"code": "DLX", "name": "Dup", "max_adult": 1, "max_child": 0, "max_occupancy": 1, "base_occupancy": 1,
	}); r.status != 409 || r.body["code"] != "CODE_TAKEN" {
		t.Fatalf("duplicate code: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/room-types/"+idOf(rt), map[string]any{"name": "Deluxe Sea", "code": "NEW"}); r.status != 422 || fieldsOf(r)["code"] != "UNKNOWN_FIELD" {
		t.Fatalf("code is immutable, unknown field rejected: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/room-types/"+idOf(rt), map[string]any{"name": "Deluxe Sea"}); r.status != 200 || r.body["name"] != "Deluxe Sea" {
		t.Fatalf("patch: %d %v", r.status, r.body)
	}

	// Rooms: housekeeping row is created, numbers are unique, lists page.
	var roomIDs []string
	for _, n := range []string{"201", "202", "203"} {
		r := abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_number": n, "room_type_id": rt.body["id"], "floor": "2"})
		if r.status != http.StatusCreated {
			t.Fatalf("create room %s: %d %v", n, r.status, r.body)
		}
		roomIDs = append(roomIDs, idOf(r))
	}
	if r := abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_number": "201", "room_type_id": rt.body["id"]}); r.status != 409 || r.body["code"] != "ROOM_NUMBER_TAKEN" {
		t.Fatalf("duplicate number: %d %v", r.status, r.body)
	}
	page := abc.do(http.MethodGet, base+"/rooms?limit=2", nil)
	if page.status != 200 || len(page.body["data"].([]any)) != 2 || page.body["next_cursor"] == nil {
		t.Fatalf("page 1: %d %v", page.status, page.body)
	}
	next := abc.do(http.MethodGet, base+"/rooms?limit=2&cursor="+page.body["next_cursor"].(string), nil)
	if len(next.body["data"].([]any)) != 1 || next.body["next_cursor"] != nil {
		t.Fatalf("page 2: %v", next.body)
	}
	if r := abc.do(http.MethodGet, base+"/rooms?active=maybe", nil); r.status != 422 {
		t.Fatalf("bad filter: %d", r.status)
	}

	// Housekeeping board and transitions.
	board := abc.do(http.MethodGet, base+"/housekeeping", nil)
	rows := board.body["data"].([]any)
	if board.status != 200 || len(rows) != 3 || rows[0].(map[string]any)["status"] != "DIRTY" || rows[0].(map[string]any)["occupancy"] != "VACANT" {
		t.Fatalf("board: %d %v", board.status, board.body)
	}
	hk := base + "/rooms/" + roomIDs[0] + "/housekeeping"
	if r := abc.do(http.MethodPost, hk, map[string]any{"status": "CLEANING", "notes": "started"}); r.status != 200 || r.body["status"] != "CLEANING" {
		t.Fatalf("set status: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, hk, map[string]any{"status": "INSPECTED"}); r.status != 409 || r.body["code"] != "INVALID_HK_TRANSITION" {
		t.Fatalf("invalid transition: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/housekeeping?status=CLEANING", nil); len(r.body["data"].([]any)) != 1 {
		t.Fatalf("status filter: %v", r.body)
	}
	if r := abc.do(http.MethodGet, hk+"/logs", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("logs: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/rooms/999999/housekeeping", map[string]any{"status": "DIRTY"}); r.status != 404 || r.body["code"] != "ROOM_NOT_FOUND" {
		t.Fatalf("unknown room: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/rooms/abc/housekeeping", map[string]any{"status": "DIRTY"}); r.status != 404 {
		t.Fatalf("malformed room id: %d", r.status)
	}

	// Blocks: create, conflict, patch, cancel.
	blk := abc.do(http.MethodPost, base+"/room-blocks", map[string]any{
		"room_id": mustInt(roomIDs[1]), "block_type": "OOO", "start_date": "2026-10-01", "end_date": "2026-10-05", "reason": "AC repair",
	})
	if blk.status != http.StatusCreated || blk.body["status"] != "ACTIVE" || blk.body["start_date"] != "2026-10-01" {
		t.Fatalf("create block: %d %v", blk.status, blk.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-blocks", map[string]any{
		"room_id": mustInt(roomIDs[1]), "block_type": "OOS", "start_date": "2026-10-04", "end_date": "2026-10-06", "reason": "x",
	}); r.status != 409 || r.body["code"] != "ROOM_BLOCK_CONFLICT" {
		t.Fatalf("overlap: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-blocks", map[string]any{"room_id": mustInt(roomIDs[1]), "block_type": "OOO", "start_date": "10/01/2026", "end_date": "2026-10-05", "reason": "x"}); r.status != 422 || fieldsOf(r)["start_date"] != "INVALID_FORMAT" {
		t.Fatalf("bad date: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/room-blocks/"+idOf(blk), map[string]any{"end_date": "2026-10-03"}); r.status != 200 || r.body["end_date"] != "2026-10-03" {
		t.Fatalf("patch block: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/room-blocks?room_id="+roomIDs[1]+"&status=ACTIVE&from=2026-10-01&to=2026-10-02", nil); len(r.body["data"].([]any)) != 1 {
		t.Fatalf("list blocks: %v", r.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-blocks/"+idOf(blk)+"/cancel", map[string]any{"reason": ""}); r.status != 422 {
		t.Fatalf("cancel needs a reason: %d", r.status)
	}
	if r := abc.do(http.MethodPost, base+"/room-blocks/"+idOf(blk)+"/cancel", map[string]any{"reason": "Fixed"}); r.status != 200 || r.body["status"] != "CANCELLED" {
		t.Fatalf("cancel: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/room-blocks/"+idOf(blk)+"/cancel", map[string]any{"reason": "Again"}); r.status != 409 || r.body["code"] != "ROOM_BLOCK_NOT_ACTIVE" {
		t.Fatalf("second cancel: %d %v", r.status, r.body)
	}
	// The board shows an active block covering the business date.
	if r := abc.do(http.MethodPost, base+"/room-blocks", map[string]any{
		"room_id": mustInt(roomIDs[2]), "block_type": "OOS", "start_date": "2026-09-30", "end_date": "2026-10-02", "reason": "Paint",
	}); r.status != 201 {
		t.Fatalf("block from today: %d %v", r.status, r.body)
	}
	found := false
	for _, row := range abc.do(http.MethodGet, base+"/housekeeping", nil).body["data"].([]any) {
		if m := row.(map[string]any); m["room_number"] == "203" {
			found = m["block"].(map[string]any)["type"] == "OOS"
		}
	}
	if !found {
		t.Fatal("board must show the OOS block")
	}

	// Another tenant sees none of it.
	for _, path := range []string{"/room-types", "/rooms", "/room-blocks", "/housekeeping", "/room-types/" + idOf(rt), "/rooms/" + roomIDs[0] + "/housekeeping/logs"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("GET %s from another tenant: %d %v", path, r.status, r.body)
		}
	}
	if r := xyz.do(http.MethodPost, hk, map[string]any{"status": "DIRTY"}); r.status != 404 {
		t.Fatalf("housekeeping from another tenant: %d", r.status)
	}

	// Permissions: a maid may update housekeeping but not inspect, manage rooms or blocks.
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Maid", "permissions": []string{"housekeeping.update"}})
	if role.status != 201 {
		t.Fatalf("role: %d %v", role.status, role.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "maid@hotel.com", "full_name": "Maid", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(bali), "role_id": role.body["id"]}},
	}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	maid := &client{env: e}
	if r := maid.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "maid@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("maid login: %d", r.status)
	}
	if r := maid.do(http.MethodGet, base+"/housekeeping", nil); r.status != 200 {
		t.Fatalf("maid board: %d", r.status)
	}
	if r := maid.do(http.MethodPost, hk, map[string]any{"status": "CLEAN"}); r.status != 200 {
		t.Fatalf("maid clean: %d %v", r.status, r.body)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, hk, map[string]any{"status": "INSPECTED"}},
		{http.MethodPost, base + "/rooms", map[string]any{"room_number": "999", "room_type_id": rt.body["id"]}},
		{http.MethodPatch, base + "/rooms/" + roomIDs[0], map[string]any{"floor": "9"}},
		{http.MethodPost, base + "/room-types", map[string]any{"code": "STD", "name": "s", "max_adult": 1, "max_child": 0, "max_occupancy": 1, "base_occupancy": 1}},
		{http.MethodPost, base + "/room-blocks", map[string]any{"room_id": mustInt(roomIDs[0]), "block_type": "OOO", "start_date": "2026-11-01", "end_date": "2026-11-02", "reason": "x"}},
		{http.MethodPost, base + "/room-blocks/" + strconv.Itoa(1) + "/cancel", map[string]any{"reason": "x"}},
	} {
		if r := maid.do(c.method, c.path, c.body); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
			t.Fatalf("%s %s as maid: %d %v", c.method, c.path, r.status, r.body)
		}
	}
}
