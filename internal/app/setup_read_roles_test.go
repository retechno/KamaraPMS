package app

import (
	"net/http"
	"testing"
)

// The reads of setup by role (docs/architecture/33-setup-read-permissions.md): a person of housekeeping reads none of the reference data of setup and still works on the board of
// housekeeping; the reception reads what the screens of reservation, check-in and folio need and none of the rules of rates, the grid or the table of the rules of approval.
func TestTheReadsOfSetupFollowTheRoleOfThePerson(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	w := buildWorld(t, abc, "BALI")

	mk := func(name, email string, perms ...string) *client {
		role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": name, "permissions": perms})
		if role.status != 201 {
			t.Fatalf("role %s: %d %v", name, role.status, role.body)
		}
		if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": email, "full_name": name, "password": testPassword,
			"grants": []map[string]any{{"property_id": mustInt(w.prop), "role_id": role.body["id"]}}}); r.status != 201 {
			t.Fatalf("user %s: %d %v", name, r.status, r.body)
		}
		c := &client{env: e}
		if r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": email, "password": testPassword}); r.status != 200 {
			t.Fatalf("login %s: %d %v", name, r.status, r.body)
		}
		return c
	}
	housekeeper := mk("Housekeeper", "hk@hotel.com", "housekeeping.update")
	reception := mk("Reception", "desk@hotel.com", "reservation.read", "reservation.create", "frontdesk.checkin", "folio.read", "payment.post", "guest.read", "guest.write")
	manager := mk("Setup manager", "mgr@hotel.com", "property.manage", "room.manage", "rate.manage", "billing_config.manage")

	p := w.base
	setup := []string{
		"/room-types", "/bed-types", "/rooms", "/taxes", "/service-charges", "/charge-codes", "/rate-plans", "/rates?rate_plan_id=1&from=2026-10-01&to=2026-10-02",
		"/rate-restrictions?from=2026-10-01&to=2026-10-02", "/yield-rules", "/approval-rules",
	}
	check := func(who string, c *client, path string, want int) {
		t.Helper()
		if r := c.do(http.MethodGet, p+path, nil); r.status != want {
			t.Errorf("%s GET %s: %d %v, want %d", who, path, r.status, r.body["code"], want)
		}
	}

	// housekeeping: none of the reference data of setup, all of the screens of housekeeping
	for _, path := range setup {
		check("housekeeping", housekeeper, path, http.StatusForbidden)
	}
	for _, path := range []string{"/housekeeping", "/housekeeping/tasks", "/business-date", "/room-blocks", "/approval-rules/in-force"} {
		check("housekeeping", housekeeper, path, http.StatusOK)
	}

	// reception: what reservation, check-in and folio need
	for _, path := range []string{"/room-types", "/bed-types", "/charge-codes", "/rate-plans", "/approval-rules/in-force", "/housekeeping", "/room-blocks", "/free-night-quotas"} {
		check("reception", reception, path, http.StatusOK)
	}
	for _, path := range []string{"/rooms", "/taxes", "/service-charges", "/rates?rate_plan_id=1&from=2026-10-01&to=2026-10-02", "/rate-restrictions?from=2026-10-01&to=2026-10-02", "/yield-rules", "/approval-rules"} {
		check("reception", reception, path, http.StatusForbidden)
	}

	// who manages setup reads what they manage
	for _, path := range append([]string{"/approval-rules/in-force"}, setup...) {
		check("manager", manager, path, http.StatusOK)
	}
}
