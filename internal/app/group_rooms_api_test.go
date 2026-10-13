package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// raw sends a body that is not JSON (the CSV of the rooming list).
func (c *client) raw(method, path, contentType, body string) response {
	c.env.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("Authorization", "Bearer "+c.token)
	rec := httptest.NewRecorder()
	c.env.handler.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return response{rec.Code, out, rec.Result().Cookies()}
}

// Bulk rooms of a group over HTTP (docs/architecture/30-group-folio.md, part A).
func TestGroupBulkRoomsAPI(t *testing.T) {
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
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	for _, n := range []string{"101", "102"} {
		abc.do(http.MethodPost, base+"/rooms", map[string]any{"bed_type_id": bedOf(abc, base), "room_type_id": mustInt(dlx), "room_number": n, "initial_housekeeping_status": "CLEAN"})
	}
	plan := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomCode}))
	abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(plan), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-09-30", "to": "2026-10-12", "amount": "1000000"})
	group := idOf(abc.do(http.MethodPost, base+"/groups", map[string]any{"code": "ALPHA", "name": "Conference Alpha", "arrival_date": "2026-10-01", "departure_date": "2026-10-10",
		"contact_name": "Siti Rahmawati", "contact_email": "siti@alpha.test"}))
	url := base + "/groups/" + group
	row := func(count int, arrival, departure string) map[string]any {
		return map[string]any{"room_type_id": mustInt(dlx), "rate_plan_id": mustInt(plan), "count": count, "arrival_date": arrival, "departure_date": departure, "adult_count": 2}
	}

	// the preview: nothing is written, the price in force is the default of the form, a row that does not fit says by how many
	pv := abc.do(http.MethodPost, url+"/room-batches/preview", map[string]any{"rows": []any{row(2, "2026-10-02", "2026-10-04"), row(1, "2026-10-03", "2026-10-05")}})
	if pv.status != 200 || pv.body["ok"] != false || pv.body["rooms"].(float64) != 3 {
		t.Fatalf("preview: %d %v", pv.status, pv.body)
	}
	rows := pv.body["rows"].([]any)
	r0, r1 := rows[0].(map[string]any), rows[1].(map[string]any)
	if r0["uniform"] != true || r0["rate_per_night"] == "" || r0["short"] != nil || r1["short"].(map[string]any)["missing"].(float64) != 1 {
		t.Fatalf("rows: %v", rows)
	}
	if abc.do(http.MethodGet, url+"/reservations", nil).body["data"] != nil && len(abc.do(http.MethodGet, url+"/reservations", nil).body["data"].([]any)) != 0 {
		t.Fatal("a preview writes nothing")
	}

	// the save: 201, one reservation for each room
	hdr := map[string]string{"Idempotency-Key": "batch-1"}
	body := map[string]any{"rows": []any{row(2, "2026-10-02", "2026-10-04")}}
	ok := abc.doWith(http.MethodPost, url+"/room-batches", body, hdr)
	if ok.status != 201 || ok.body["rooms"].(float64) != 2 || ok.body["replayed"] != false || len(ok.body["reservations"].([]any)) != 2 {
		t.Fatalf("save: %d %v", ok.status, ok.body)
	}
	members := abc.do(http.MethodGet, url+"/reservations", nil)
	if members.status != 200 || len(members.body["data"].([]any)) != 2 {
		t.Fatalf("members: %d %v", members.status, members.body)
	}
	g := abc.do(http.MethodGet, url, nil)
	if g.body["contact_guest_id"] == nil {
		t.Fatalf("the contact guest is kept on the group: %v", g.body)
	}
	// the same request again is the same answer (200), another body under the key is refused
	if r := abc.doWith(http.MethodPost, url+"/room-batches", body, hdr); r.status != 200 || r.body["replayed"] != true {
		t.Fatalf("replay: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, url+"/room-batches", map[string]any{"rows": []any{row(1, "2026-10-02", "2026-10-04")}}, hdr); r.status != 422 || r.body["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("reused key: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, url+"/room-batches", body); r.status != 400 || r.body["code"] != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("no key: %d %v", r.status, r.body)
	}
	// the rooms are gone: nothing of a request that does not fit is kept, and the error names the row
	short := abc.doWith(http.MethodPost, url+"/room-batches", map[string]any{"rows": []any{row(1, "2026-10-02", "2026-10-03"), row(1, "2026-10-03", "2026-10-04")}}, map[string]string{"Idempotency-Key": "batch-2"})
	if short.status != 409 || short.body["code"] != "ROOM_TYPE_NOT_AVAILABLE" {
		t.Fatalf("short: %d %v", short.status, short.body)
	}
	if n := len(abc.do(http.MethodGet, url+"/reservations", nil).body["data"].([]any)); n != 2 {
		t.Fatalf("all or nothing: %d reservations", n)
	}
	if r := abc.doWith(http.MethodPost, url+"/room-batches", map[string]any{"rows": []any{row(1, "2026-10-08", "2026-10-12")}}, map[string]string{"Idempotency-Key": "batch-3"}); r.status != 422 || fieldsOf(r)["rows[0].arrival_date"] != "OUTSIDE_GROUP_DATES" {
		t.Fatalf("outside the dates: %d %v", r.status, r.body)
	}
	if r := abc.doWith(http.MethodPost, url+"/room-batches", map[string]any{"rows": []any{row(101, "2026-10-02", "2026-10-03")}}, map[string]string{"Idempotency-Key": "batch-4"}); r.status != 422 || r.body["code"] != "TOO_MANY_ROOMS" {
		t.Fatalf("limit: %d %v", r.status, r.body)
	}

	// the rooming list: the placeholders, an edit, a file
	list := abc.do(http.MethodGet, url+"/rooming-list", nil)
	data := list.body["data"].([]any)
	if list.status != 200 || len(data) != 2 || data[0].(map[string]any)["is_placeholder"] != true || data[0].(map[string]any)["name"] != "Conference Alpha · 1" {
		t.Fatalf("rooming list: %d %v", list.status, list.body)
	}
	line0, line1 := int64(data[0].(map[string]any)["line_id"].(float64)), int64(data[1].(map[string]any)["line_id"].(float64))
	number1 := data[1].(map[string]any)["confirmation_number"].(string)
	up := abc.do(http.MethodPatch, url+"/rooming-list", map[string]any{"rows": []any{map[string]any{"line_id": line0, "name": "Budi Santoso", "email": "budi@alpha.test", "phone": ""}}})
	if up.status != 200 || up.body["updated"].(float64) != 1 {
		t.Fatalf("update: %d %v", up.status, up.body)
	}
	bad := abc.do(http.MethodPatch, url+"/rooming-list", map[string]any{"rows": []any{map[string]any{"line_id": line1, "name": "Ayu Lestari", "email": "nope"}}})
	if bad.status != 422 || fieldsOf(bad)["rows[0].email"] == "" {
		t.Fatalf("bad row: %d %v", bad.status, bad.body)
	}
	csv := "confirmation_number,name,email,phone\n" + number1 + ",Ayu Lestari,ayu@alpha.test,\nMISSING,Who,,\n"
	dry := abc.raw(http.MethodPost, url+"/rooming-list/import?dry_run=true", "text/csv", csv)
	if dry.status != 200 || dry.body["applied"] != false || len(dry.body["problems"].([]any)) != 1 {
		t.Fatalf("dry run: %d %v", dry.status, dry.body)
	}
	wrong := abc.raw(http.MethodPost, url+"/rooming-list/import", "text/csv", csv)
	if wrong.status != 422 || wrong.body["code"] != "ROOMING_IMPORT_INVALID" {
		t.Fatalf("import with a wrong row: %d %v", wrong.status, wrong.body)
	}
	good := abc.raw(http.MethodPost, url+"/rooming-list/import", "text/csv", "confirmation_number,name,email,phone\n"+number1+",Ayu Lestari,ayu@alpha.test,\n")
	if good.status != 200 || good.body["applied"] != true {
		t.Fatalf("import: %d %v", good.status, good.body)
	}
	// the guest search finds the named guests, not the placeholders
	srch := abc.do(http.MethodGet, "/api/v1/guests?q=Conference", nil)
	if srch.status != 200 || len(srch.body["data"].([]any)) != 0 {
		t.Fatalf("placeholders are not found: %d %v", srch.status, srch.body)
	}
	if r := abc.do(http.MethodGet, "/api/v1/guests?q=Ayu", nil); len(r.body["data"].([]any)) != 1 {
		t.Fatalf("a named guest is found: %v", r.body)
	}

	// another tenant does not see the group at all
	if r := xyz.doWith(http.MethodPost, url+"/room-batches", body, map[string]string{"Idempotency-Key": "x"}); r.status != 404 {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, url+"/rooming-list", nil); r.status != 404 {
		t.Fatalf("another tenant, list: %d %v", r.status, r.body)
	}
}
