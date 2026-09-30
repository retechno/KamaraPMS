package app

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kamarapms/internal/platform/clock"
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
