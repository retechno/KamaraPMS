package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"kamarapms/internal/platform/dbtest"
)

// Audit F-10: authorization is proved over every route the server registers, not over a sample.
//
// The routes are read from the source of the modules (the same reading as TestOpenAPIDescribesEveryRoute), so a route added later is in the matrix without anyone adding it. Each one is
// called by a user who is signed in, has a grant on the property and has a role with NO permission, against objects that exist, and must answer 403 PERMISSION_DENIED and change nothing.
// A route that cannot be proved that way must be named in one of the two lists below with the reason, which is how a new route is forced to say what it is.

type routeSpec struct{ method, path string }

func (r routeSpec) key() string { return r.method + " " + r.path }

var (
	namedParam    = regexp.MustCompile(`\{([^}]+)\}`)
	routeConstP   = regexp.MustCompile(`const p = "([^"]+)"`)
	routeLiteral  = regexp.MustCompile(`mux\.Handle\("([A-Z]+) (/[^"]*)"`)
	routePlusPref = regexp.MustCompile(`mux\.Handle\("([A-Z]+) "\+p(?:\+"([^"]*)")?`)
)

// serverRoutes: every "METHOD path" the modules register, with the names of the path parameters kept.
func serverRoutes(t *testing.T) []routeSpec {
	t.Helper()
	files, err := filepath.Glob("../*/*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no module sources: %v", err)
	}
	seen := map[string]routeSpec{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		prefix := ""
		if m := routeConstP.FindStringSubmatch(src); m != nil {
			prefix = m[1]
		}
		for _, m := range routeLiteral.FindAllStringSubmatch(src, -1) {
			r := routeSpec{m[1], m[2]}
			seen[r.key()] = r
		}
		for _, m := range routePlusPref.FindAllStringSubmatch(src, -1) {
			r := routeSpec{m[1], prefix + m[2]}
			seen[r.key()] = r
		}
	}
	out := make([]routeSpec, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// publicRoutes are reachable without a token: the only ones.
var publicRoutes = map[string]string{
	"POST /api/v1/auth/login":   "signs in",
	"POST /api/v1/auth/refresh": "uses the refresh cookie",
	"POST /api/v1/auth/logout":  "ends the session of the cookie",
	"GET /healthz":              "liveness probe",
	"GET /readyz":               "readiness probe",
}

// signedInRoutes need a token and nothing else: they act on the caller or list what the caller may see.
var signedInRoutes = map[string]string{
	"GET /api/v1/auth/me":        "the caller's own profile",
	"POST /api/v1/auth/password": "the caller's own password, checked against the current one",
	"GET /api/v1/permissions":    "the catalogue of permission names, the same for everyone",
	"GET /api/v1/properties":     "lists the properties the caller has a grant on",
}

// propertyReads are the reads that ask for a grant on the property and for no permission (the services call CanAccess and not a permission). They are configuration and reference data of the
// property a user already works in. This list is the current behaviour and is pinned on purpose: a route that starts to ask for a permission must leave it (the test fails when an entry
// answers 403), and a new read that asks for none must be added here, which is a decision.
const propertyRead = "configuration or reference data of a property the caller has a grant on; asks for the grant, not for a permission"

var propertyReads = map[string]string{
	"GET /api/v1/properties/{propertyId}":                                  propertyRead,
	"GET /api/v1/properties/{propertyId}/bed-types":                        propertyRead,
	"GET /api/v1/properties/{propertyId}/business-date":                    propertyRead,
	"GET /api/v1/properties/{propertyId}/business-days":                    propertyRead,
	"GET /api/v1/properties/{propertyId}/charge-codes":                     propertyRead,
	"GET /api/v1/properties/{propertyId}/charge-codes/{id}":                propertyRead,
	"GET /api/v1/properties/{propertyId}/housekeeping":                     propertyRead,
	"GET /api/v1/properties/{propertyId}/housekeeping/tasks":               propertyRead,
	"GET /api/v1/properties/{propertyId}/rate-plans":                       propertyRead,
	"GET /api/v1/properties/{propertyId}/rate-plans/{id}/bed-adjustments":  propertyRead,
	"GET /api/v1/properties/{propertyId}/rate-quotes":                      propertyRead,
	"GET /api/v1/properties/{propertyId}/rate-restrictions":                propertyRead,
	"GET /api/v1/properties/{propertyId}/rate-restrictions/effective":      propertyRead,
	"GET /api/v1/properties/{propertyId}/rates":                            propertyRead,
	"GET /api/v1/properties/{propertyId}/room-blocks":                      propertyRead,
	"GET /api/v1/properties/{propertyId}/room-blocks/{id}":                 propertyRead,
	"GET /api/v1/properties/{propertyId}/room-types":                       propertyRead,
	"GET /api/v1/properties/{propertyId}/room-types/{id}":                  propertyRead,
	"GET /api/v1/properties/{propertyId}/rooms":                            propertyRead,
	"GET /api/v1/properties/{propertyId}/rooms/{id}":                       propertyRead,
	"GET /api/v1/properties/{propertyId}/rooms/{roomId}/housekeeping/logs": propertyRead,
	"GET /api/v1/properties/{propertyId}/service-charges":                  propertyRead,
	"GET /api/v1/properties/{propertyId}/taxes":                            propertyRead,
	"GET /api/v1/properties/{propertyId}/yield-rules":                      propertyRead,
	"GET /api/v1/properties/{propertyId}/yield-rules/{id}":                 propertyRead,
	// a calculation of a charge code of the property (taxes and service charge of an amount); it writes nothing
	"POST /api/v1/properties/{propertyId}/charge-calculations": "a calculator over the charge codes of the property: asks for the grant, writes nothing",
}

// lookupFirst are the routes whose permission depends on what the object is, so the object is read before the permission is asked: an unknown id answers 404 to everyone with a grant. With
// an object that exists they answer 403 (the service tests of the module call them without the permission).
var lookupFirst = map[string]string{
	"POST /api/v1/properties/{propertyId}/city-ledger/adjustments/{id}/void": "a credit note needs city_ledger.credit_note and a write-off city_ledger.write_off: the adjustment is read to know which (cityledger/adjustments_test.go asks both)",
}

// bodies are the requests that need more than {} to get past the validation of the body (a route that validates before it authorizes would otherwise answer 422 and prove nothing).
func (w world) bodies(shift string) map[string]any {
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}
	return map[string]any{
		"POST /api/v1/guests":                                                    map[string]any{"origin_property_id": mustInt(w.prop), "last_name": "Probe"},
		"POST /api/v1/properties":                                                func() map[string]any { p := validProperty(); p["code"] = "PROBE"; return p }(),
		"POST /api/v1/properties/{propertyId}/night-audit/run":                   map[string]any{"business_date": "2026-09-30"},
		"POST /api/v1/properties/{propertyId}/night-audit/no-shows":              map[string]any{"business_date": "2026-09-30", "reservation_room_ids": []int64{1}},
		"POST /api/v1/properties/{propertyId}/night-audit/room-charges":          map[string]any{"business_date": "2026-09-30"},
		"POST /api/v1/properties/{propertyId}/night-audit/room-charges/preview":  map[string]any{"business_date": "2026-09-30"},
		"POST /api/v1/properties/{propertyId}/charge-calculations":               map[string]any{"charge_code_id": mustInt(w.minibar), "quantity": "1", "unit_price": "1000"},
		"POST /api/v1/properties/{propertyId}/cashier/shifts/{id}/close":         map[string]any{"counted_cash": "0"},
		"POST /api/v1/properties/{propertyId}/cashier/shifts/{id}/movements":     map[string]any{"kind": "DROP", "amount": "1000", "reason": "probe"},
		"POST /api/v1/properties/{propertyId}/city-ledger/adjustments/{id}/void": map[string]any{"reason": "probe", "approval": approval},
		"POST /api/v1/properties/{propertyId}/lost-found/{id}/dispose":           map[string]any{"reason": "probe"},
		"POST /api/v1/properties/{propertyId}/lost-found/{id}/return":            map[string]any{"claimant_name": "probe"},
		"POST /api/v1/properties/{propertyId}/maintenance-requests/{id}/cancel":  map[string]any{"note": "probe"},
		"POST /api/v1/properties/{propertyId}/room-blocks":                       map[string]any{"room_id": 1, "block_type": "OOO", "start_date": "2026-12-01", "end_date": "2026-12-03", "reason": "probe"},
		"POST /api/v1/properties/{propertyId}/room-types":                        map[string]any{"code": "PRB", "name": "Probe", "max_adult": 2, "max_child": 0, "max_occupancy": 2, "base_occupancy": 2},
		"PUT /api/v1/properties/{propertyId}/free-night-quotas/{kind}":           map[string]any{"monthly_nights": 1},
		"PUT /api/v1/properties/{propertyId}/rate-restrictions":                  map[string]any{"from": "2026-12-24", "to": "2026-12-26", "set": map[string]any{"stop_sell": true}},
		"PUT /api/v1/properties/{propertyId}/rates":                              map[string]any{"rate_plan_id": mustInt(w.plan), "room_type_ids": []int64{mustInt(w.roomType)}, "from": "2026-12-24", "to": "2026-12-26", "amount": "1"},
	}
}

const probeQuery = "from=2026-09-30&to=2026-10-05&date=2026-09-30&business_date=2026-09-30&arrival=2026-10-03&departure=2026-10-05&arrival_date=2026-10-03&departure_date=2026-10-05&as_of=2026-09-30&year=2026&month=2026-09&room_type_id=1&rate_plan_id=1&adults=2&stay_id=1&period_start=2026-09-01&period_end=2026-09-30&start=2026-09-01&end=2026-09-30&tax_id=1&period=2026-09-01&q=a"

// concrete turns a route into a URL on the world: the property is the world's, every other parameter is the id 1 (the first object of its kind, which the world created for most kinds).
func (w world) concrete(r routeSpec) string {
	path := namedParam.ReplaceAllStringFunc(r.path, func(m string) string {
		switch m {
		case "{propertyId}":
			return w.prop
		case "{start}":
			return "2026-01-01"
		case "{kind}":
			return "COMPLIMENTARY"
		}
		return "1"
	})
	if r.method == http.MethodGet {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		path += sep + probeQuery
	}
	return path
}

func (w world) request(c *client, r routeSpec, bodies map[string]any) response {
	var body any
	if r.method != http.MethodGet && r.method != http.MethodDelete {
		body = map[string]any{}
		if b, ok := bodies[r.key()]; ok {
			body = b
		}
	}
	return c.doWith(r.method, w.concrete(r), body, map[string]string{"Idempotency-Key": "probe"})
}

func TestEveryRouteRefusesASignedInUserWithoutThePermission(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	w := buildWorld(t, abc, "BALI")
	shift := abc.do(http.MethodPost, w.base+"/cashier/shifts", map[string]any{"drawer": "MAIN", "opening_float": "0"})
	if shift.status != 201 {
		t.Fatalf("shift: %d %v", shift.status, shift.body)
	}
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Nothing", "permissions": []string{}})
	if role.status != 201 {
		t.Fatalf("role: %d %v", role.status, role.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": "nothing@hotel.com", "full_name": "Nothing", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(w.prop), "role_id": role.body["id"]}}}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	nobody := &client{env: e}
	if r := nobody.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "nothing@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d %v", r.status, r.body)
	}
	bodies := w.bodies(idOf(shift))
	auditRows := func() int { return e.countRows(t, "audit_logs") }
	before := auditRows()

	var proved, unproved []string
	for _, r := range serverRoutes(t) {
		k := r.key()
		if _, ok := publicRoutes[k]; ok {
			continue
		}
		res := w.request(nobody, r, bodies)
		code, _ := res.body["code"].(string)
		switch _, signedIn := signedInRoutes[k]; {
		case signedIn:
			if res.status == http.StatusForbidden || res.status == http.StatusUnauthorized || res.status >= 500 {
				unproved = append(unproved, fmt.Sprintf("%s: listed as open to a signed-in user but answers %d %s", k, res.status, code))
			}
		case lookupFirst[k] != "":
			if res.status != http.StatusNotFound || code != "ADJUSTMENT_NOT_FOUND" {
				unproved = append(unproved, fmt.Sprintf("%s: listed as read-first but answers %d %s", k, res.status, code))
			}
		case propertyReads[k] != "":
			if res.status == http.StatusForbidden || res.status == http.StatusUnauthorized || res.status >= 500 {
				unproved = append(unproved, fmt.Sprintf("%s: listed as a read that asks for no permission but answers %d %s: take it off the list", k, res.status, code))
			}
		case res.status == http.StatusForbidden && code == "PERMISSION_DENIED":
			proved = append(proved, k)
		default:
			unproved = append(unproved, fmt.Sprintf("%s: %d %s (a user without permission must get 403 PERMISSION_DENIED, or the route must be named as open with its reason)", k, res.status, code))
		}
	}
	if len(unproved) > 0 {
		t.Errorf("%d routes are not proved:\n  %s", len(unproved), strings.Join(unproved, "\n  "))
	}
	if after := auditRows(); after != before {
		t.Errorf("refused calls left %d audit entries behind: something was changed", after-before)
	}
	t.Logf("%d routes answer 403 to a user without permission, %d are public, %d need only a signed-in user, %d are reads of a property the caller has a grant on", len(proved), len(publicRoutes), len(signedInRoutes), len(propertyReads))
}

// Nothing but the public routes answers without a token.
func TestEveryRouteButThePublicOnesRequiresAToken(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	w := buildWorld(t, abc, "BALI")
	anon := &client{env: e}
	var open []string
	for _, r := range serverRoutes(t) {
		k := r.key()
		if _, ok := publicRoutes[k]; ok {
			continue
		}
		if res := w.request(anon, r, w.bodies("1")); res.status != http.StatusUnauthorized {
			open = append(open, fmt.Sprintf("%s: %d", k, res.status))
		}
	}
	if len(open) > 0 {
		t.Errorf("routes that answer without a token:\n  %s", strings.Join(open, "\n  "))
	}
}

func (e *apiEnv) countRows(t *testing.T, table string) int {
	t.Helper()
	var n int
	if err := dbtest.Pool(t).QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A user with every permission on one property sees no other property of the tenant: every route that names a property answers 404 PROPERTY_NOT_FOUND for it, whatever the permission.
func TestEveryPropertyRouteHidesAPropertyThatHasNoGrantForTheUser(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	other := buildWorld(t, abc, "BALI") // the property the user has no grant on
	mine := buildWorld(t, abc, "JKT")
	cat := abc.do(http.MethodGet, "/api/v1/permissions", nil)
	var all []string
	for _, p := range cat.body["data"].([]any) {
		switch v := p.(type) {
		case string:
			all = append(all, v)
		case map[string]any:
			if code, ok := v["code"].(string); ok {
				all = append(all, code)
			} else if name, ok := v["name"].(string); ok {
				all = append(all, name)
			}
		}
	}
	if len(all) < 50 {
		t.Fatalf("the permission catalogue: %d entries", len(all))
	}
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Everything", "permissions": all})
	if role.status != 201 {
		t.Fatalf("role: %d %v", role.status, role.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": "everything@hotel.com", "full_name": "Everything", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(mine.prop), "role_id": role.body["id"]}}}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	u := &client{env: e}
	if r := u.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "everything@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d %v", r.status, r.body)
	}
	bodies := other.bodies("1")
	var wrong []string
	n := 0
	for _, r := range serverRoutes(t) {
		if !strings.Contains(r.path, "{propertyId}") {
			continue
		}
		n++
		res := other.request(u, r, bodies)
		if res.status != http.StatusNotFound || res.body["code"] != "PROPERTY_NOT_FOUND" {
			wrong = append(wrong, fmt.Sprintf("%s: %d %v", r.key(), res.status, res.body["code"]))
		}
	}
	if len(wrong) > 0 {
		t.Errorf("routes that do not hide a property without a grant:\n  %s", strings.Join(wrong, "\n  "))
	}
	// and the same user does work on its own property: the test is not answering 404 to everything
	if res := mine.request(u, routeSpec{http.MethodGet, "/api/v1/properties/{propertyId}/rooms"}, nil); res.status != http.StatusOK {
		t.Errorf("the user's own property: %d %v", res.status, res.body)
	}
	t.Logf("%d routes name a property", n)
}
