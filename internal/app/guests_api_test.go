package app

import (
	"net/http"
	"testing"
)

func TestGuestsAPIFlow(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	jktBody := validProperty()
	jktBody["code"] = "JKT"
	jkt := idOf(abc.do(http.MethodPost, "/api/v1/properties", jktBody))

	// Create: code is generated, duplicates only warn.
	g := abc.do(http.MethodPost, "/api/v1/guests", map[string]any{
		"origin_property_id": mustInt(bali), "first_name": "Siti", "last_name": "Nurhaliza", "email": "siti@mail.com",
		"phone": "+62 812-3456-7890", "nationality": "id", "date_of_birth": "1990-05-01", "id_type": "ktp", "id_number": "3171234567890001",
	})
	if g.status != http.StatusCreated || g.body["code"] != "GST000001" || g.body["nationality"] != "ID" || g.body["id_type"] != "KTP" ||
		len(g.body["possible_duplicates"].([]any)) != 0 || g.body["hidden_duplicate_count"] != float64(0) {
		t.Fatalf("create: %d %v", g.status, g.body)
	}
	dup := abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(jkt), "last_name": "Siti N.", "email": "SITI@mail.com"})
	if dup.status != http.StatusCreated || len(dup.body["possible_duplicates"].([]any)) != 1 {
		t.Fatalf("duplicate hint: %d %v", dup.status, dup.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"last_name": "No origin"}); r.status != 422 || fieldsOf(r)["origin_property_id"] != "REQUIRED" {
		t.Fatalf("origin required: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "X", "date_of_birth": "01/05/1990", "email": "bad"}); r.status != 422 ||
		fieldsOf(r)["date_of_birth"] != "INVALID_FORMAT" {
		t.Fatalf("bad date: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "X", "email": "bad"}); r.status != 422 || fieldsOf(r)["email"] != "INVALID_FORMAT" {
		t.Fatalf("bad email: %d %v", r.status, r.body)
	}

	// Search and paging.
	s := abc.do(http.MethodGet, "/api/v1/guests?q=nurha", nil)
	if s.status != 200 || len(s.body["data"].([]any)) != 1 || s.body["data"].([]any)[0].(map[string]any)["code"] != "GST000001" {
		t.Fatalf("search: %d %v", s.status, s.body)
	}
	page := abc.do(http.MethodGet, "/api/v1/guests?limit=1", nil)
	if len(page.body["data"].([]any)) != 1 || page.body["next_cursor"] == nil {
		t.Fatalf("page 1: %v", page.body)
	}
	next := abc.do(http.MethodGet, "/api/v1/guests?limit=1&cursor="+page.body["next_cursor"].(string), nil)
	if len(next.body["data"].([]any)) != 1 || next.body["next_cursor"] != nil {
		t.Fatalf("page 2: %v", next.body)
	}
	if r := abc.do(http.MethodGet, "/api/v1/guests?q=zzz", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("empty result must be [] not null: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, "/api/v1/guests?property_id=abc", nil); r.status != 422 {
		t.Fatalf("bad property_id: %d", r.status)
	}

	// Get, patch (empty string clears), history.
	id := idOf(g)
	if r := abc.do(http.MethodGet, "/api/v1/guests/"+id, nil); r.status != 200 || r.body["can_edit"] != true || r.body["last_name"] != "Nurhaliza" {
		t.Fatalf("get: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, "/api/v1/guests/"+id, map[string]any{"city": "Ubud", "email": "", "date_of_birth": ""}); r.status != 200 ||
		r.body["city"] != "Ubud" || r.body["email"] != nil || r.body["date_of_birth"] != nil || r.body["phone"] != "+62 812-3456-7890" {
		t.Fatalf("patch: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodPatch, "/api/v1/guests/"+id, map[string]any{"code": "HACK"}); r.status != 422 || fieldsOf(r)["code"] != "UNKNOWN_FIELD" {
		t.Fatalf("code is not editable: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, "/api/v1/guests/"+id+"/history", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 || r.body["hidden_count"] != float64(0) {
		t.Fatalf("history: %d %v", r.status, r.body)
	}
	for _, path := range []string{"/api/v1/guests/999999", "/api/v1/guests/abc", "/api/v1/guests/999999/history"} {
		if r := abc.do(http.MethodGet, path, nil); r.status != 404 || r.body["code"] != "GUEST_NOT_FOUND" {
			t.Fatalf("GET %s: %d %v", path, r.status, r.body)
		}
	}

	// Another tenant sees nothing.
	if r := xyz.do(http.MethodGet, "/api/v1/guests", nil); r.status != 200 || len(r.body["data"].([]any)) != 0 {
		t.Fatalf("other tenant list: %d %v", r.status, r.body)
	}
	if r := xyz.do(http.MethodGet, "/api/v1/guests/"+id, nil); r.status != 404 {
		t.Fatalf("other tenant get: %d", r.status)
	}
	if r := xyz.do(http.MethodPatch, "/api/v1/guests/"+id, map[string]any{"city": "x"}); r.status != 404 {
		t.Fatalf("other tenant patch: %d", r.status)
	}
	if r := xyz.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "x"}); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("other tenant's origin property: %d %v", r.status, r.body)
	}

	// Permissions: a reader at Bali sees only Bali-linked guests and cannot write.
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Reader", "permissions": []string{"guest.read"}})
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "reader@hotel.com", "full_name": "Reader", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(bali), "role_id": role.body["id"]}},
	}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	reader := &client{env: e}
	if r := reader.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "reader@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d", r.status)
	}
	if r := reader.do(http.MethodGet, "/api/v1/guests", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("reader sees Bali-origin guests only: %d %v", r.status, r.body)
	}
	if r := reader.do(http.MethodGet, "/api/v1/guests/"+idOf(dup), nil); r.status != 404 {
		t.Fatalf("Jakarta guest is invisible to a Bali reader: %d", r.status)
	}
	if r := reader.do(http.MethodPost, "/api/v1/guests", map[string]any{"origin_property_id": mustInt(bali), "last_name": "x"}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("create without guest.write: %d %v", r.status, r.body)
	}
	if r := reader.do(http.MethodPatch, "/api/v1/guests/"+id, map[string]any{"city": "x"}); r.status != 403 {
		t.Fatalf("patch without guest.write: %d %v", r.status, r.body)
	}
	if r := reader.do(http.MethodGet, "/api/v1/guests/"+id, nil); r.status != 200 || r.body["can_edit"] != false {
		t.Fatalf("reader view: %d %v", r.status, r.body)
	}
	if r := (&client{env: e}).do(http.MethodGet, "/api/v1/guests", nil); r.status != 401 {
		t.Fatalf("anonymous: %d", r.status)
	}
}
