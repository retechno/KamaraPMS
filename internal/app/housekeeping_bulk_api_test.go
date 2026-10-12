package app

import (
	"net/http"
	"testing"
)

// The bulk change over HTTP: the shape of the answer (a summary and one result per room, in the order asked), the code of a room that cannot change, and 422 for a bad list.
func TestHousekeepingBulkAPI(t *testing.T) {
	e := newAPI(t)
	c := e.login("ABC")
	base := "/api/v1/properties/" + idOf(c.do(http.MethodPost, "/api/v1/properties", validProperty()))
	dlx := idOf(c.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	room := func(number, status string) int64 {
		return mustInt(idOf(c.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(c, base), "room_type_id": mustInt(dlx), "room_number": number, "initial_housekeeping_status": status})))
	}
	dirty, clean := room("101", "DIRTY"), room("102", "CLEAN")

	r := c.do(http.MethodPost, base+"/housekeeping/bulk", map[string]any{"room_ids": []int64{dirty, clean, 99999}, "status": "CLEAN"})
	if r.status != http.StatusOK {
		t.Fatalf("bulk: %d %v", r.status, r.body)
	}
	sum := r.body["summary"].(map[string]any)
	if sum["requested"] != 3.0 || sum["changed"] != 1.0 || sum["failed"] != 2.0 {
		t.Fatalf("summary: %v", sum)
	}
	res := r.body["results"].([]any)
	first, second, third := res[0].(map[string]any), res[1].(map[string]any), res[2].(map[string]any)
	if first["ok"] != true || first["room_number"] != "101" || first["from"] != "DIRTY" || first["status"] != "CLEAN" {
		t.Fatalf("first: %v", first)
	}
	if e := second["error"].(map[string]any); second["ok"] != false || e["code"] != "INVALID_HK_TRANSITION" || e["context"].(map[string]any)["from"] != "CLEAN" {
		t.Fatalf("second: %v", second)
	}
	if e := third["error"].(map[string]any); e["code"] != "ROOM_NOT_FOUND" {
		t.Fatalf("third: %v", third)
	}

	if r := c.do(http.MethodPost, base+"/housekeeping/bulk", map[string]any{"room_ids": []int64{}, "status": "CLEAN"}); r.status != http.StatusUnprocessableEntity || fieldsOf(r)["room_ids"] != "OUT_OF_RANGE" {
		t.Fatalf("an empty list: %d %v", r.status, r.body)
	}
	if r := c.do(http.MethodPost, base+"/housekeeping/bulk", map[string]any{"room_ids": []int64{dirty}, "status": "NOPE"}); r.status != http.StatusUnprocessableEntity {
		t.Fatalf("a bad status: %d %v", r.status, r.body)
	}
}
