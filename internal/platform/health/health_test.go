package health_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kamarapms/internal/platform/health"
)

type pinger struct{ err error }

func (p pinger) Ping(context.Context) error { return p.err }

func get(h http.Handler, path string) (int, string) {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(w.Body)
	return w.Code, string(b)
}

// Live says the process runs and never looks at a dependency: a database that is down must not make an orchestrator restart the API.
func TestLiveDoesNotTouchTheDatabase(t *testing.T) {
	if code, body := get(health.Live(), "/healthz"); code != http.StatusOK || !strings.Contains(body, `"ok"`) {
		t.Fatalf("%d %s", code, body)
	}
}

func TestReadyNeedsTheDatabaseAndEveryCheck(t *testing.T) {
	ok := func(context.Context) error { return nil }
	behind := func(context.Context) error { return errors.New("the schema is at version 3 and this release needs 62") }

	if code, body := get(health.Ready(pinger{}, ok), "/readyz"); code != http.StatusOK || !strings.Contains(body, `"ready"`) {
		t.Fatalf("ready: %d %s", code, body)
	}
	if code, body := get(health.Ready(pinger{}), "/readyz"); code != http.StatusOK {
		t.Fatalf("no checks is the database alone: %d %s", code, body)
	}
	code, body := get(health.Ready(pinger{err: errors.New("connection refused")}, ok), "/readyz")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "NOT_READY") {
		t.Fatalf("database down: %d %s", code, body)
	}
	code, body = get(health.Ready(pinger{}, ok, behind), "/readyz")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "NOT_READY") {
		t.Fatalf("schema behind: %d %s", code, body)
	}
	// the reason is for the log, not for the caller of a public probe
	if strings.Contains(body, "version 3") || strings.Contains(body, "62") {
		t.Fatalf("the probe must not tell the schema version: %s", body)
	}
}
