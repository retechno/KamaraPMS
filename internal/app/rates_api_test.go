package app

import (
	"net/http"
	"testing"
)

func TestRatesAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	base := "/api/v1/properties/" + bali

	var roomID, laundryID int64
	for _, c := range abc.do(http.MethodGet, base+"/charge-codes?limit=200", nil).body["data"].([]any) {
		m := c.(map[string]any)
		switch m["code"] {
		case "ROOM":
			roomID = int64(m["id"].(float64))
		case "LAUNDRY":
			laundryID = int64(m["id"].(float64))
		}
	}
	dlx := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "DLX", "name": "Deluxe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))
	std := idOf(abc.do(http.MethodPost, base+"/room-types", map[string]any{"code": "STD", "name": "Standard", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2}))

	// Plans.
	plan := abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "bar", "name": "Best available", "meal_plan": "BB", "room_charge_code_id": roomID})
	if plan.status != http.StatusCreated || plan.body["code"] != "BAR" || plan.body["price_mode"] != "EXCLUSIVE" || plan.body["room_charge_code"] != "ROOM" ||
		plan.body["is_refundable"] != true || plan.body["is_active"] != true {
		t.Fatalf("create plan: %d %v", plan.status, plan.body)
	}
	if r := abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "LND", "name": "x", "meal_plan": "RO", "room_charge_code_id": laundryID}); r.status != 422 || fieldsOf(r)["room_charge_code_id"] != "CHARGE_CODE_NOT_ROOM" {
		t.Fatalf("non-ROOM code: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, base+"/rate-plans", map[string]any{"code": "BAR", "name": "dup", "meal_plan": "RO", "room_charge_code_id": roomID}); r.status != 409 || r.body["code"] != "CODE_TAKEN" {
		t.Fatalf("duplicate: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/rate-plans/"+idOf(plan), map[string]any{"code": "NEW"}); r.status != 422 || fieldsOf(r)["code"] != "UNKNOWN_FIELD" {
		t.Fatalf("code is immutable: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/rate-plans/"+idOf(plan), map[string]any{"name": "BAR flexible", "is_refundable": false}); r.status != 200 || r.body["name"] != "BAR flexible" || r.body["is_refundable"] != false {
		t.Fatalf("patch: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, base+"/rate-plans/999999", map[string]any{"name": "x"}); r.status != 404 || r.body["code"] != "RATE_PLAN_NOT_FOUND" {
		t.Fatalf("unknown plan: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, base+"/rate-plans?active=true", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", r.status, r.body)
	}

	// Bulk fill: Fri and Sat nights of two weeks for two room types = 2 x 4.
	fill := abc.do(http.MethodPut, base+"/rates", map[string]any{
		"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx), mustInt(std)}, "from": "2026-10-01", "to": "2026-10-15",
		"weekdays": []string{"FRI", "SAT"}, "amount": "1750000",
	})
	if fill.status != 200 || fill.body["updated_nights"] != float64(8) || fill.body["created_nights"] != float64(8) {
		t.Fatalf("fill: %d %v", fill.status, fill.body)
	}
	again := abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-04", "amount": "1500000"})
	if again.body["updated_nights"] != float64(3) || again.body["created_nights"] != float64(1) { // Thu is new; Fri and Sat are overwritten
		t.Fatalf("overwrite: %v", again.body)
	}
	grid := abc.do(http.MethodGet, base+"/rates?rate_plan_id="+idOf(plan)+"&from=2026-10-01&to=2026-10-04&room_type_id="+dlx, nil)
	cells := grid.body["rates"].([]any)
	if grid.status != 200 || grid.body["price_mode"] != "EXCLUSIVE" || len(cells) != 3 || cells[1].(map[string]any)["stay_date"] != "2026-10-02" || cells[1].(map[string]any)["amount"] != "1500000" {
		t.Fatalf("grid: %d %v", grid.status, grid.body)
	}

	for name, c := range map[string]struct {
		body  map[string]any
		field string
	}{
		"negative amount": {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-02", "amount": "-5"}, "amount"},
		"IDR decimals":    {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-02", "amount": "5.5"}, "amount"},
		"to before from":  {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-05", "to": "2026-10-02", "amount": "5"}, "to"},
		"too long":        {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2029-10-01", "amount": "5"}, "to"},
		"bad weekday":     {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-02", "amount": "5", "weekdays": []string{"FUNDAY"}}, "weekdays"},
		"no room types":   {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "from": "2026-10-01", "to": "2026-10-02", "amount": "5"}, "room_type_ids"},
		"bad date":        {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "01/10/2026", "to": "2026-10-02", "amount": "5"}, "from"},
		"missing to":      {map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "amount": "5"}, "to"},
	} {
		if r := abc.do(http.MethodPut, base+"/rates", c.body); r.status != 422 || fieldsOf(r)[c.field] == "" {
			t.Fatalf("%s: %d %v", name, r.status, r.body)
		}
	}
	if r := abc.do(http.MethodPut, base+"/rates", map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{999999}, "from": "2026-10-01", "to": "2026-10-02", "amount": "5"}); r.status != 404 || r.body["code"] != "ROOM_TYPE_NOT_FOUND" {
		t.Fatalf("unknown room type: %d %v", r.status, r.body)
	}
	for _, q := range []string{"", "?rate_plan_id=" + idOf(plan), "?rate_plan_id=" + idOf(plan) + "&from=2026-10-01", "?rate_plan_id=" + idOf(plan) + "&from=2026-10-01&to=2026-10-01"} {
		if r := abc.do(http.MethodGet, base+"/rates"+q, nil); r.status != 422 {
			t.Fatalf("GET /rates%s: %d %v", q, r.status, r.body)
		}
	}

	// Other tenants see nothing; a reader can read but not write.
	for _, path := range []string{"/rate-plans", "/rates?rate_plan_id=" + idOf(plan) + "&from=2026-10-01&to=2026-10-02"} {
		if r := xyz.do(http.MethodGet, base+path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("GET %s from another tenant: %d %v", path, r.status, r.body)
		}
	}
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Clerk", "permissions": []string{"reservation.read"}})
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "clerk@hotel.com", "full_name": "Clerk", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(bali), "role_id": role.body["id"]}},
	}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	clerk := &client{env: e}
	if r := clerk.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "clerk@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d", r.status)
	}
	if r := clerk.do(http.MethodGet, base+"/rates?rate_plan_id="+idOf(plan)+"&from=2026-10-01&to=2026-10-04", nil); r.status != 200 {
		t.Fatalf("clerk read: %d", r.status)
	}
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPut, base + "/rates", map[string]any{"rate_plan_id": mustInt(idOf(plan)), "room_type_ids": []int64{mustInt(dlx)}, "from": "2026-10-01", "to": "2026-10-02", "amount": "1"}},
		{http.MethodPost, base + "/rate-plans", map[string]any{"code": "X", "name": "x", "meal_plan": "RO", "room_charge_code_id": roomID}},
		{http.MethodPatch, base + "/rate-plans/" + idOf(plan), map[string]any{"name": "x"}},
	} {
		if r := clerk.do(c.method, c.path, c.body); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
			t.Fatalf("%s %s as clerk: %d %v", c.method, c.path, r.status, r.body)
		}
	}
}
