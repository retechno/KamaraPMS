package pos

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/httpx"
)

// ---------------------------------------------------------------- the routes of an agent

// DeviceGate is what the subscription says about a tenant: whether the POS module is in its plan and whether it may still be changed.
type DeviceGate interface {
	CheckPOS(ctx context.Context, tenantID int64, write bool) error
}

// DeviceHandler serves /api/v1/device/...: the routes of an agent, with a token of a device and no user. They are classified POS for the module gate
// and a read-only subscription refuses the ones that change something, as any write.
type DeviceHandler struct {
	svc     *Service
	gate    DeviceGate
	limiter *httpx.RateLimiter
	enroll  *httpx.RateLimiter
}

// NewDeviceHandler returns the routes of the agents. gate may be nil (no subscription check: tests of the protocol alone).
func NewDeviceHandler(svc *Service, gate DeviceGate, now func() time.Time) *DeviceHandler {
	return &DeviceHandler{svc: svc, gate: gate, limiter: httpx.NewRateLimiter(240, now), enroll: httpx.NewRateLimiter(10, now)}
}

// Handler is the mux of the device routes; mount it at /api/v1/device/.
func (d *DeviceHandler) Handler() http.Handler {
	mux := http.NewServeMux()
	const p = "/api/v1/device"
	mux.Handle("POST "+p+"/enroll", httpx.HandlerFunc(d.enrollRoute))
	mux.Handle("GET "+p+"/config", d.authed(false, d.config))
	mux.Handle("GET "+p+"/jobs/next", d.authed(true, d.nextJob))
	mux.Handle("POST "+p+"/jobs/{id}/result", d.authed(true, d.result))
	mux.Handle("POST "+p+"/heartbeat", d.authed(true, d.heartbeat))
	mux.Handle(p+"/", httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		return apperr.NotFound("ROUTE_NOT_FOUND", "no such endpoint: "+r.Method+" "+r.URL.Path)
	}))
	return mux
}

// authed wraps a route: the token of a device (a user's token is a 401), the rate limit of the token, and the gate of the subscription.
func (d *DeviceHandler) authed(write bool, fn func(w http.ResponseWriter, r *http.Request, dev DeviceIdentity) error) http.Handler {
	return httpx.HandlerFunc(func(w http.ResponseWriter, r *http.Request) error {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			return apperr.Unauthorized("DEVICE_TOKEN_REQUIRED", "expected Authorization: Bearer <device token>")
		}
		// the limit is by token before the lookup, so a flood of wrong tokens costs the database nothing
		if okRate, wait := d.limiter.Allow(hashOf(token)); !okRate {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)))
			return apperr.New(apperr.KindRateLimited, "TOO_MANY_REQUESTS", "too many requests; slow down and try again shortly")
		}
		dev, err := d.svc.AuthenticateDevice(r.Context(), token)
		if err != nil {
			return err
		}
		if d.gate != nil {
			if err := d.gate.CheckPOS(r.Context(), dev.TenantID, write); err != nil {
				return err
			}
		}
		return fn(w, r, dev)
	})
}

func (d *DeviceHandler) enrollRoute(w http.ResponseWriter, r *http.Request) error {
	if ip, ok := httpx.ClientIPFrom(r.Context()); ok {
		if okRate, wait := d.enroll.Allow(ip.String()); !okRate {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)))
			return apperr.New(apperr.KindRateLimited, "TOO_MANY_REQUESTS", "too many enrolment attempts; wait a minute")
		}
	}
	var in struct {
		Code         string `json:"code"`
		Name         string `json:"name"`
		AgentVersion string `json:"agent_version"`
	}
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := d.svc.Enroll(r.Context(), in.Code, in.Name, in.AgentVersion)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "no-store")
	return httpx.WriteJSON(w, http.StatusCreated, v)
}

func (d *DeviceHandler) config(w http.ResponseWriter, r *http.Request, dev DeviceIdentity) error {
	v, err := d.svc.DeviceConfig(r.Context(), dev)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (d *DeviceHandler) nextJob(w http.ResponseWriter, r *http.Request, dev DeviceIdentity) error {
	wait := time.Duration(PollSeconds) * time.Second
	if v := r.URL.Query().Get("wait"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > int(MaxWait/time.Second) {
			return invalid("jobs", fieldErr("wait", "INVALID_VALUE", "0 to 30 seconds"))
		}
		wait = time.Duration(n) * time.Second
	}
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{}) // a long poll outlives the write timeout of an answer
	job, err := d.svc.NextJob(r.Context(), dev, wait)
	if err != nil {
		return err
	}
	if job == nil {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	return httpx.WriteJSON(w, http.StatusOK, job)
}

func (d *DeviceHandler) result(w http.ResponseWriter, r *http.Request, dev DeviceIdentity) error {
	var in JobResult
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := d.svc.ReportResult(r.Context(), dev, r.PathValue("id"), in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}

func (d *DeviceHandler) heartbeat(w http.ResponseWriter, r *http.Request, dev DeviceIdentity) error {
	var in Heartbeat
	if err := httpx.DecodeJSON(w, r, &in); err != nil {
		return err
	}
	v, err := d.svc.Heartbeat(r.Context(), dev, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, v)
}
