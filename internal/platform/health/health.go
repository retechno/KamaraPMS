// Package health serves liveness and readiness probes.
package health

import (
	"context"
	"net/http"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
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

// Ready reports whether the API can serve traffic (the database answers).
func Ready(db Pinger) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			return apperr.Unavailable("NOT_READY", "the database is not reachable").WithCause(err)
		}
		return httpx.WriteJSON(w, http.StatusOK, status{"ready"})
	})
}
