// Package health serves liveness and readiness probes.
package health

import (
	"context"
	"net/http"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
)

// Pinger is satisfied by *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

type status struct {
	Status string `json:"status"`
}

// Live reports that the process is running. It never touches dependencies,
// so an orchestrator will not restart the API because the database is down.
func Live() http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return httpx.WriteJSON(w, http.StatusOK, status{"ok"})
	})
}

// Check is a dependency check of the readiness probe: nil when the dependency is fine.
type Check func(ctx context.Context) error

// Ready reports whether the API can serve traffic: the database answers and every check passes (for example that the schema has the migrations of this release). It answers 503
// with a stable code while it is not, so a proxy or an orchestrator holds the traffic back; the process is alive all the same (Live), so it is not restarted for it. The
// reason is logged, and the response says only which kind of dependency is missing.
func Ready(db Pinger, checks ...Check) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			logging.FromContext(r.Context()).Warn("not ready: the database does not answer", "error", err)
			return apperr.Unavailable("NOT_READY", "the database is not reachable").WithCause(err)
		}
		for _, c := range checks {
			if err := c(ctx); err != nil {
				logging.FromContext(r.Context()).Warn("not ready: a dependency check failed", "reason", err.Error())
				return apperr.Unavailable("NOT_READY", "the service is not ready: a dependency check failed").WithCause(err)
			}
		}
		return httpx.WriteJSON(w, http.StatusOK, status{"ready"})
	})
}
