package httpx

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRateLimiterRefillsWithTheClock(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	l := NewRateLimiter(60, func() time.Time { return now }) // one a second, burst of 60
	for i := 0; i < 60; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("burst request %d refused", i)
		}
	}
	ok, wait := l.Allow("a")
	if ok || wait != time.Second {
		t.Fatalf("over the burst: %v %v", ok, wait)
	}
	if ok, _ := l.Allow("b"); !ok {
		t.Fatal("another client has its own bucket")
	}
	now = now.Add(2500 * time.Millisecond)
	for i := 0; i < 2; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatalf("refilled token %d refused", i)
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("only two tokens were refilled")
	}
	now = now.Add(time.Hour)
	for i := 0; i < 60; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("the bucket never holds more than the burst, but must be full again")
		}
	}
	if ok, _ := l.Allow("a"); ok {
		t.Fatal("capped at the burst")
	}
}

func TestRateLimiterSweepsIdleBuckets(t *testing.T) {
	now := time.Now() //nolint:forbidigo // test clock seed
	l := NewRateLimiter(60, func() time.Time { return now })
	for i := 0; i < maxBuckets; i++ {
		l.Allow(string(rune(i)) + "k")
	}
	now = now.Add(time.Hour)
	l.Allow("new")
	if len(l.buckets) > 2 {
		t.Fatalf("idle buckets were not dropped: %d", len(l.buckets))
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		RequestID(slog.New(slog.NewTextHandler(io.Discard, nil))), RateLimit(2, func() time.Time { return now }))
	call := func(path, addr string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.RemoteAddr = addr
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if call("/api/v1/x", "10.0.0.1:1").Code != 204 || call("/api/v1/x", "10.0.0.1:2").Code != 204 {
		t.Fatal("the burst passes")
	}
	rec := call("/api/v1/x", "10.0.0.1:3")
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "30" || rec.Header().Get("Content-Type") != "application/problem+json" ||
		!strings.Contains(rec.Body.String(), "TOO_MANY_REQUESTS") {
		t.Fatalf("limited: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	if call("/healthz", "10.0.0.1:4").Code != 204 {
		t.Fatal("health probes are exempt")
	}
	if call("/api/v1/x", "10.0.0.2:1").Code != 204 {
		t.Fatal("another address is not limited")
	}
	off := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }), RateLimit(0, func() time.Time { return now }))
	for i := 0; i < 50; i++ {
		rec := httptest.NewRecorder()
		off.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/x", nil))
		if rec.Code != 204 {
			t.Fatal("a limit of 0 disables it")
		}
	}
}
