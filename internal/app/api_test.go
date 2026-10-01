package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/notifications"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/tenancy"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

const testPassword = "correct horse battery staple"

// apiEnv runs the real router (middleware, authentication, handlers, services) on a real database.
type apiEnv struct {
	t       *testing.T
	handler http.Handler
	clock   *clock.Fake
	build   func(perMinute int) *App
	app     *App
	mail    notifications.Sender
}

func newAPI(t *testing.T) *apiEnv {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	txm := db.NewTxManager(pool, 5*time.Second)
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)) // 20:00 in Jakarta
	aw := audit.NewWriter(c)
	tokens := iam.TokenConfig{Secret: []byte(strings.Repeat("s", 32)), AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour}
	ten := tenancy.NewService(txm, c, aw, iam.NewAuthorizer(txm))
	users := iam.NewService(txm, c, aw, tokens)
	for _, code := range []string{"ABC", "XYZ"} {
		if _, err := ten.CreateTenant(context.Background(), code, code+" Hotels", "Asia/Jakarta"); err != nil {
			t.Fatal(err)
		}
		if _, err := users.BootstrapAdmin(context.Background(), code, "admin@hotel.com", "Admin", testPassword); err != nil {
			t.Fatal(err)
		}
	}
	env := &apiEnv{t: t, clock: c}
	env.build = func(perMinute int) *App {
		return New(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: pool, TxManager: txm, Clock: c, Tokens: tokens, RateLimitPerMinute: perMinute, Mail: env.mail})
	}
	env.app = env.build(0)
	env.handler = env.app.Handler
	return env
}

// rateLimited rebuilds the handler with a request limit per minute per client address.
func (e *apiEnv) rateLimited(perMinute int) {
	e.t.Helper()
	e.app = e.build(perMinute)
	e.handler = e.app.Handler
}

func (e *apiEnv) get(path string) int { return e.getRec(path).Code }

func (e *apiEnv) getRec(path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.RemoteAddr = "192.0.2.10:4000"
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	return rec
}

type response struct {
	status  int
	body    map[string]any
	cookies []*http.Cookie
}

type client struct {
	env    *apiEnv
	token  string
	cookie *http.Cookie
}

func (c *client) do(method, path string, body any) response {
	c.env.t.Helper()
	return c.doWith(method, path, body, nil)
}

// doWith is do with extra request headers (for example Idempotency-Key).
func (c *client) doWith(method, path string, body any, headers map[string]string) response {
	c.env.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			c.env.t.Fatal(err)
		}
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.cookie != nil && strings.HasPrefix(path, "/api/v1/auth") {
		r.AddCookie(c.cookie) // the browser only sends it to its Path
	}
	rec := httptest.NewRecorder()
	c.env.handler.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	res := response{rec.Code, out, rec.Result().Cookies()}
	for _, ck := range res.cookies {
		if ck.Name == iam.RefreshCookie {
			if ck.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = ck
			}
		}
	}
	if tok, ok := out["access_token"].(string); ok {
		c.token = tok
	}
	return res
}

func (e *apiEnv) login(tenant string) *client {
	e.t.Helper()
	c := &client{env: e}
	r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": tenant, "email": "admin@hotel.com", "password": testPassword})
	if r.status != 200 {
		e.t.Fatalf("login: %d %v", r.status, r.body)
	}
	return c
}

func validProperty() map[string]any {
	return map[string]any{
		"code": "BALI", "name": "Hotel Bali", "city": "Denpasar", "country_code": "ID",
		"timezone": "Asia/Jakarta", "currency_code": "IDR", "currency_decimals": 0,
		"check_in_time": "14:00", "check_out_time": "12:00",
		"opening_business_date": "2026-09-30",
	}
}

func fieldsOf(r response) map[string]string {
	out := map[string]string{}
	errs, _ := r.body["errors"].([]any)
	for _, e := range errs {
		m := e.(map[string]any)
		out[m["field"].(string)] = m["code"].(string)
	}
	return out
}

func idOf(r response) string { return strconv.FormatInt(int64(r.body["id"].(float64)), 10) }

func TestAuthHTTPFlow(t *testing.T) {
	e := newAPI(t)
	anon := &client{env: e}

	if r := anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "admin@hotel.com", "password": "nope nope nope"}); r.status != 401 || r.body["code"] != "INVALID_CREDENTIALS" {
		t.Fatalf("bad login: %d %v", r.status, r.body)
	}

	c := &client{env: e}
	r := c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "abc", "email": "Admin@Hotel.com", "password": testPassword})
	if r.status != 200 || r.body["token_type"] != "Bearer" || r.body["expires_in"] != float64(900) {
		t.Fatalf("login: %d %v", r.status, r.body)
	}
	if _, leaked := r.body["refresh_token"]; leaked {
		t.Fatal("the refresh token must never be in the JSON body")
	}
	ck := c.cookie
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/api/v1/auth" || ck.Value == "" {
		t.Fatalf("refresh cookie attributes: %+v", ck)
	}

	me := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	if me.status != 200 || me.body["tenant"].(map[string]any)["code"] != "ABC" || me.body["user"].(map[string]any)["is_tenant_admin"] != true {
		t.Fatalf("me: %d %v", me.status, me.body)
	}

	oldToken := c.token
	e.clock.Advance(time.Minute) // a later instant yields a distinct token
	if r := c.do(http.MethodPost, "/api/v1/auth/refresh", nil); r.status != 200 || c.token == oldToken || c.cookie.Value == ck.Value {
		t.Fatalf("refresh must rotate both tokens: %d %v", r.status, r.body)
	}
	stale := &client{env: e, token: oldToken}
	if r := stale.do(http.MethodGet, "/api/v1/auth/me", nil); r.status != 401 || r.body["code"] != "SESSION_REVOKED" {
		t.Fatalf("rotated-out access token: %d %v", r.status, r.body)
	}

	current := c.token
	if r := c.do(http.MethodPost, "/api/v1/auth/logout", nil); r.status != 204 || c.cookie != nil {
		t.Fatalf("logout must clear the cookie: %d %v", r.status, c.cookie)
	}
	loggedOut := &client{env: e, token: current}
	if r := loggedOut.do(http.MethodGet, "/api/v1/auth/me", nil); r.status != 401 || r.body["code"] != "SESSION_REVOKED" {
		t.Fatalf("access token after logout: %d %v", r.status, r.body)
	}
	if r := anon.do(http.MethodPost, "/api/v1/auth/refresh", nil); r.status != 401 || r.body["code"] != "REFRESH_TOKEN_MISSING" {
		t.Fatalf("refresh without cookie: %d %v", r.status, r.body)
	}
}

func TestPropertyAPIFlow(t *testing.T) {
	e := newAPI(t)
	c := e.login("ABC")

	created := c.do(http.MethodPost, "/api/v1/properties", validProperty())
	if created.status != http.StatusCreated {
		t.Fatalf("create: %d %v", created.status, created.body)
	}
	b := created.body
	if b["code"] != "BALI" || b["business_date"] != "2026-09-30" || b["check_in_time"] != "14:00" ||
		b["night_audit_earliest_time"] != "20:00" || b["night_audit_marks_occupied_dirty"] != true || b["currency_decimals"] != float64(0) {
		t.Fatalf("create body: %v", b)
	}
	id := idOf(created)

	if r := c.do(http.MethodGet, "/api/v1/properties/"+id, nil); r.status != 200 || r.body["name"] != "Hotel Bali" {
		t.Fatalf("get: %d %v", r.status, r.body)
	}
	if r := c.do(http.MethodGet, "/api/v1/properties", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("list: %d %v", r.status, r.body)
	}
	patched := c.do(http.MethodPatch, "/api/v1/properties/"+id,
		map[string]any{"name": "Hotel Bali Resort", "require_room_inspection_for_checkin": true, "check_in_time": "15:00"})
	if patched.status != 200 || patched.body["name"] != "Hotel Bali Resort" || patched.body["check_in_time"] != "15:00" {
		t.Fatalf("patch: %d %v", patched.status, patched.body)
	}

	// Server time vs property business date: 02:30 on 1 Oct locally, business date still 30 Sep.
	e.clock.Set(time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC))
	c = e.login("ABC") // the earlier access token has expired by now
	bd := c.do(http.MethodGet, "/api/v1/properties/"+id+"/business-date", nil)
	if bd.status != 200 || bd.body["business_date"] != "2026-09-30" || bd.body["property_local_time"] != "2026-10-01T02:30:00+07:00" ||
		bd.body["server_time"] != "2026-09-30T19:30:00Z" || bd.body["night_audit_allowed"] != true {
		t.Fatalf("business-date: %d %v", bd.status, bd.body)
	}
	if r := c.do(http.MethodGet, "/api/v1/properties/"+id+"/business-days", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("business-days: %d %v", r.status, r.body)
	}
	if r := c.do(http.MethodGet, "/api/v1/nope", nil); r.status != 404 || r.body["code"] != "ROUTE_NOT_FOUND" {
		t.Fatalf("unknown route: %d %v", r.status, r.body)
	}
}

func TestPropertyAPIValidation(t *testing.T) {
	e := newAPI(t)
	c := e.login("ABC")

	p := validProperty()
	p["check_in_time"] = "25:00"
	delete(p, "currency_decimals")
	delete(p, "opening_business_date")
	r := c.do(http.MethodPost, "/api/v1/properties", p)
	f := fieldsOf(r)
	if r.status != 422 || f["check_in_time"] != "INVALID_FORMAT" || f["currency_decimals"] != "REQUIRED" || f["opening_business_date"] != "REQUIRED" {
		t.Fatalf("field errors: %d %v", r.status, f)
	}
	p = validProperty()
	p["timezone"] = "WIB"
	if f := fieldsOf(c.do(http.MethodPost, "/api/v1/properties", p)); f["timezone"] != "INVALID_TIMEZONE" {
		t.Fatalf("timezone: %v", f)
	}
	p = validProperty()
	p["currency"] = "IDR"
	if f := fieldsOf(c.do(http.MethodPost, "/api/v1/properties", p)); f["currency"] != "UNKNOWN_FIELD" {
		t.Fatalf("unknown field: %v", f)
	}
	if r := c.do(http.MethodPost, "/api/v1/properties", validProperty()); r.status != 201 {
		t.Fatalf("create: %d %v", r.status, r.body)
	}
	if r := c.do(http.MethodPost, "/api/v1/properties", validProperty()); r.status != 409 || r.body["code"] != "CODE_TAKEN" {
		t.Fatalf("duplicate: %d %v", r.status, r.body)
	}
}

func TestTenantIsolationAndPermissions(t *testing.T) {
	e := newAPI(t)
	abc, xyz := e.login("ABC"), e.login("XYZ")
	bali := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))

	if r := (&client{env: e}).do(http.MethodGet, "/api/v1/properties/"+bali, nil); r.status != 401 {
		t.Fatalf("unauthenticated: %d", r.status)
	}
	for _, path := range []string{"/api/v1/properties/" + bali, "/api/v1/properties/" + bali + "/business-date"} {
		if r := xyz.do(http.MethodGet, path, nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
			t.Fatalf("%s from another tenant: %d %v", path, r.status, r.body)
		}
	}

	// A front-desk user with a role that lacks property.manage.
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Front Desk", "permissions": []string{"reservation.read"}})
	if role.status != 201 {
		t.Fatalf("role: %d %v", role.status, role.body)
	}
	user := abc.do(http.MethodPost, "/api/v1/users", map[string]any{
		"email": "fd@hotel.com", "full_name": "Front Desk", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(bali), "role_id": role.body["id"]}},
	})
	if user.status != 201 || len(user.body["grants"].([]any)) != 1 {
		t.Fatalf("user: %d %v", user.status, user.body)
	}
	fd := &client{env: e}
	if r := fd.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "fd@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("fd login: %d %v", r.status, r.body)
	}
	if r := fd.do(http.MethodGet, "/api/v1/properties/"+bali, nil); r.status != 200 {
		t.Fatalf("granted property: %d %v", r.status, r.body)
	}
	if r := fd.do(http.MethodPatch, "/api/v1/properties/"+bali, map[string]any{"name": "x"}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("patch without property.manage: %d %v", r.status, r.body)
	}
	if r := fd.do(http.MethodGet, "/api/v1/users", nil); r.status != 403 {
		t.Fatalf("non-admin user list: %d %v", r.status, r.body)
	}
	if r := fd.do(http.MethodGet, "/api/v1/permissions", nil); r.status != 200 || len(r.body["data"].([]any)) == 0 {
		t.Fatalf("permission catalogue: %d", r.status)
	}

	// Deactivation takes effect on the very next request.
	if r := abc.do(http.MethodPatch, "/api/v1/users/"+idOf(user), map[string]any{"is_active": false}); r.status != 200 {
		t.Fatalf("deactivate: %d %v", r.status, r.body)
	}
	if r := fd.do(http.MethodGet, "/api/v1/properties/"+bali, nil); r.status != 401 {
		t.Fatalf("deactivated user still has access: %d %v", r.status, r.body)
	}
}

func mustInt(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return n
}
