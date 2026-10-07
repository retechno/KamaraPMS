package main

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serverOn(t *testing.T, h http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(h)
	srv.Listener = l
	srv.Start()
	t.Cleanup(srv.Close)
	return l.Addr().String()
}

func TestHealthcheckLiveAndReady(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }) // alive, not ready
	addr := serverOn(t, mux)
	env := func(k string) string {
		if k == "PMS_HTTP_ADDR" {
			return addr
		}
		return ""
	}
	var out bytes.Buffer
	if code := healthcheck(nil, env, &out); code != 0 {
		t.Fatalf("alive: %d %s", code, out.String())
	}
	out.Reset()
	if code := healthcheck([]string{"-ready"}, env, &out); code != 1 || !strings.Contains(out.String(), "503") {
		t.Fatalf("not ready is a failure of the readiness check only: %d %s", code, out.String())
	}
}

func TestHealthcheckFailsWhenNothingListensOrTheArgumentIsWrong(t *testing.T) {
	var out bytes.Buffer
	// a port that nothing listens on
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_ = l.Close()
	if code := healthcheck(nil, func(string) string { return addr }, &out); code != 1 {
		t.Fatalf("nothing listens: %d", code)
	}
	if code := healthcheck([]string{"-nope"}, func(string) string { return addr }, &out); code != 2 {
		t.Fatalf("unknown argument: %d", code)
	}
	if code := healthcheck(nil, func(string) string { return "not-an-address" }, &out); code != 2 {
		t.Fatalf("bad address: %d", code)
	}
}

func TestHealthURLCallsAWildcardAsLocalhost(t *testing.T) {
	for listen, want := range map[string]string{
		":8080":           "http://127.0.0.1:8080/healthz",
		"0.0.0.0:9000":    "http://127.0.0.1:9000/healthz",
		"[::]:9000":       "http://127.0.0.1:9000/healthz",
		"127.0.0.1:18080": "http://127.0.0.1:18080/healthz",
		"[::1]:8080":      "http://[::1]:8080/healthz",
	} {
		got, err := healthURL(listen, "/healthz")
		if err != nil || got != want {
			t.Errorf("%s: %q %v, want %q", listen, got, err, want)
		}
	}
}
