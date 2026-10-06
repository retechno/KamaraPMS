package app

import (
	"net/http"
	"testing"
)

// The restriction grid over HTTP: the fill, the rows, the effective view, the errors and the isolation of the tenants.
func TestRateRestrictionsAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	var roomID int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		if m := c.(map[string]any); m["code"] == "ROOM" {
			roomID = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	bar := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "Best", "meal_plan": "RO", "room_charge_code_id": roomID}))
	corp := idOf(abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "CORP", "name": "Corporate", "meal_plan": "RO", "room_charge_code_id": roomID}))
	dlxID := mustInt(dlx)

	// A room type is closed on two nights, for every plan.
	fill := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{
		"room_type_ids": []int64{dlxID}, "from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": true, "min_stay": 3},
	})
	if fill.status != 200 || fill.body["created"] != float64(2) || fill.body["dates"] != float64(2) || fill.body["scopes"] != float64(1) {
		t.Fatalf("fill: %d %v", fill.status, fill.body)
	}
	// CORP is opened inside it with a row of its own.
	if r := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{
		"room_type_ids": []int64{dlxID}, "rate_plan_ids": []int64{mustInt(corp)}, "from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": false},
	}); r.status != 200 || r.body["created"] != float64(2) {
		t.Fatalf("corp: %d %v", r.status, r.body)
	}

	rows := abc.do(http.MethodGet, base+"/rate-restrictions?from=2026-12-24&to=2026-12-26&room_type_id="+dlx+"&rate_plan_id="+bar, nil)
	list := rows.body["data"].([]any)
	if rows.status != 200 || len(list) != 2 || list[0].(map[string]any)["stop_sell"] != true || list[0].(map[string]any)["min_stay"] != float64(3) || list[0].(map[string]any)["rate_plan_id"] != nil {
		t.Fatalf("rows for BAR: %d %v", rows.status, rows.body)
	}
	if all := abc.do(http.MethodGet, base+"/rate-restrictions?from=2026-12-24&to=2026-12-26", nil).body["data"].([]any); len(all) != 4 {
		t.Fatalf("every row: %d", len(all))
	}

	// The effective view resolves the precedence on the server: BAR is closed, CORP is open, both keep the minimum.
	eff := func(plan string) []any {
		r := abc.do(http.MethodGet, base+"/rate-restrictions/effective?room_type_id="+dlx+"&rate_plan_id="+plan+"&from=2026-12-24&to=2026-12-26", nil)
		if r.status != 200 {
			t.Fatalf("effective: %d %v", r.status, r.body)
		}
		return r.body["data"].([]any)
	}
	b, c := eff(bar)[0].(map[string]any), eff(corp)[0].(map[string]any)
	if b["stop_sell"] != true || c["stop_sell"] != false || b["min_stay"] != float64(3) || c["min_stay"] != float64(3) {
		t.Fatalf("effective: %v %v", b, c)
	}
	if src := b["sources"].(map[string]any)["stop_sell"].(map[string]any); src["scope"] != "ROOM_TYPE" {
		t.Fatalf("sources: %v", b["sources"])
	}
	if src := c["sources"].(map[string]any)["stop_sell"].(map[string]any); src["scope"] != "ROOM_TYPE_AND_PLAN" {
		t.Fatalf("sources: %v", c["sources"])
	}

	// Clearing the last attributes removes the rows.
	if r := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{
		"room_type_ids": []int64{dlxID}, "rate_plan_ids": []int64{mustInt(corp)}, "from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{}, "clear": []string{"stop_sell"},
	}); r.status != 200 || r.body["deleted"] != float64(2) {
		t.Fatalf("clear: %d %v", r.status, r.body)
	}

	// Errors.
	if r := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{"from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{}}); r.status != 422 || fieldsOf(r)["set"] != "NOTHING_TO_CHANGE" {
		t.Fatalf("nothing: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{"from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"min_stay": 5, "max_stay": 2}}); r.status != 422 || fieldsOf(r)["set.min_stay"] != "INVALID_VALUE" {
		t.Fatalf("min above max: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{"room_type_ids": []int64{999999}, "from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": true}}); r.status != 404 || r.body["code"] != "ROOM_TYPE_NOT_FOUND" {
		t.Fatalf("unknown room type: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPut, base+"/rate-restrictions", map[string]any{"colour": "red", "from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": true}}); r.status != 422 || fieldsOf(r)["colour"] != "UNKNOWN_FIELD" {
		t.Fatalf("unknown field: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/rate-restrictions/effective?from=2026-12-24&to=2026-12-26", nil); r.status != 422 {
		t.Fatalf("effective needs a room type and a plan: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/rate-restrictions?from=2026-12-24&to=2028-12-26", nil); r.status != 422 {
		t.Fatalf("a window over 366 days: %d %v", r.status, r.body)
	}

	// Another tenant sees nothing of this property.
	for _, req := range []struct{ method, path string }{
		{http.MethodGet, base + "/rate-restrictions?from=2026-12-24&to=2026-12-26"},
		{http.MethodGet, base + "/rate-restrictions/effective?room_type_id=" + dlx + "&rate_plan_id=" + bar + "&from=2026-12-24&to=2026-12-26"},
	} {
		if r := xyz.do(req.method, req.path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("%s %s: %d %v", req.method, req.path, r.status, r.body)
		}
	}
	if r := xyz.do(http.MethodPut, base+"/rate-restrictions", map[string]any{"from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": true}}); r.status != 404 {
		t.Fatalf("another tenant writes: %d %v", r.status, r.body)
	}
}
