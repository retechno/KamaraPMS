package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/health"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func serve(t *testing.T, db fakeDB, method, path string, headers ...string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	h := NewHandler(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: db, Clock: clock.System{}})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	h.ServeHTTP(rec, r)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec, body
}

func TestHealthz(t *testing.T) {
	rec, body := serve(t, fakeDB{err: errors.New("db down")}, http.MethodGet, "/healthz")
	if rec.Code != 200 || body["status"] != "ok" {
		t.Fatalf("liveness must not depend on the database: %d %v", rec.Code, body)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing request id")
	}
}

func TestReadyz(t *testing.T) {
	if rec, body := serve(t, fakeDB{}, http.MethodGet, "/readyz"); rec.Code != 200 || body["status"] != "ready" {
		t.Fatalf("ready: %d %v", rec.Code, body)
	}
	rec, body := serve(t, fakeDB{err: errors.New("connection refused")}, http.MethodGet, "/readyz")
	if rec.Code != 503 || body["code"] != "NOT_READY" {
		t.Fatalf("not ready: %d %v", rec.Code, body)
	}
}

func TestAPIRequiresAuthentication(t *testing.T) {
	rec, body := serve(t, fakeDB{}, http.MethodGet, "/api/v1/properties")
	if rec.Code != 401 || body["code"] != "UNAUTHENTICATED" {
		t.Fatalf("got %d %v", rec.Code, body)
	}
}

func TestMalformedAuthorizationHeader(t *testing.T) {
	rec, body := serve(t, fakeDB{}, http.MethodGet, "/api/v1/properties", "Authorization", "Basic dXNlcjpwYXNz")
	if rec.Code != 401 || body["code"] != "TOKEN_INVALID" {
		t.Fatalf("got %d %v", rec.Code, body)
	}
}

// A check that fails (the schema is behind this release) makes the service not ready, and never makes it not alive.
func TestReadyzAsksTheChecksAndHealthzDoesNot(t *testing.T) {
	behind := func(context.Context) error { return errors.New("the schema is at version 3 and this release needs 62") }
	h := NewHandler(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}, Clock: clock.System{}, ReadyChecks: []health.Check{behind}})
	get := func(path string) (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := get("/readyz"); code != 503 || !strings.Contains(body, "NOT_READY") || strings.Contains(body, "version") {
		t.Fatalf("a release whose schema is behind is not ready, and the probe does not say why: %d %s", code, body)
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("the process is alive: %d", code)
	}
}

// Every answer of the API says not to sniff the type and not to be framed, errors and unknown routes included.
func TestAPIAnswersCarrySecurityHeaders(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz", "/api/v1/properties", "/api/v1/nowhere"} {
		rec, _ := serve(t, fakeDB{}, http.MethodGet, path)
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: %v", path, rec.Header())
		}
	}
}

// An error that is not an application error is never described to the caller: the answer is the generic problem, with the request id to look for in the log.
func TestAnUnexpectedErrorIsNotDescribed(t *testing.T) {
	h := NewHandler(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{err: errors.New("dial tcp 10.1.2.3:5432: connection refused (password=hunter2)")}, Clock: clock.System{}})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	body := rec.Body.String()
	for _, leak := range []string{"10.1.2.3", "hunter2", "dial tcp", "goroutine", ".go:"} {
		if strings.Contains(body, leak) {
			t.Fatalf("the answer leaks %q: %s", leak, body)
		}
	}
	if !strings.Contains(body, "request_id") {
		t.Fatalf("the answer must carry the request id: %s", body)
	}
}

// Through the whole application, behind a trusted proxy: each person has a rate limit of their own, and a client that talks to the API directly cannot choose its address.
func TestRateLimitBehindATrustedProxyThroughTheApplication(t *testing.T) {
	proxy := netip.MustParsePrefix("172.29.0.10/32")
	h := NewHandler(Deps{Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: fakeDB{}, Clock: clock.System{}, RateLimitPerMinute: 2, TrustedProxies: []netip.Prefix{proxy}})
	call := func(remote, xff string) int {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/properties", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, person := range []string{"198.51.100.1", "198.51.100.2"} {
		for i := 0; i < 2; i++ {
			if code := call("172.29.0.10:5000", person); code != 401 { // unauthenticated, but not limited
				t.Fatalf("%s %d: %d", person, i, code)
			}
		}
		if code := call("172.29.0.10:5000", person); code != 429 {
			t.Fatalf("%s over the limit: %d", person, code)
		}
	}
	for i := 0; i < 2; i++ {
		call("203.0.113.5:6000", "7.7.7."+string(rune('1'+i)))
	}
	if code := call("203.0.113.5:6000", "7.7.7.9"); code != 429 {
		t.Fatalf("a direct client forging the header stays one client: %d", code)
	}
}
