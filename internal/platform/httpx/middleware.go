package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"time"

	"kamarapms/internal/platform/logging"
)

// Middleware wraps a handler.
type Middleware func(http.Handler) http.Handler

// Chain applies middleware so that the first one listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// RequestIDHeader carries the correlation id (accepted from clients if well formed).
const RequestIDHeader = "X-Request-ID"

var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type (
	requestIDKey struct{}
	clientIPKey  struct{}
)

// RequestIDFrom returns the request id stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// ClientIPFrom returns the caller's IP address: the peer of the connection, or, when the peer is a trusted proxy (see ResolveClientIP), the client it forwarded.
func ClientIPFrom(ctx context.Context) (netip.Addr, bool) {
	ip, ok := ctx.Value(clientIPKey{}).(netip.Addr)
	return ip, ok
}

// RequestID assigns a correlation id to every request, echoes it in the
// response, and stores a logger tagged with it in the request context. It also
// resolves the client address (the rate limiter, the audit trail and the
// sign-in throttle read it): X-Forwarded-For counts only from the trusted proxies.
func RequestID(base *slog.Logger, trusted ...netip.Prefix) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(RequestIDHeader)
			if !validRequestID.MatchString(id) {
				id = newRequestID()
			}
			w.Header().Set(RequestIDHeader, id)
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			logger := base.With("request_id", id)
			if ip, ok := ResolveClientIP(r, trusted); ok {
				ctx = context.WithValue(ctx, clientIPKey{}, ip)
				logger = logger.With("client_ip", ip.String())
			}
			ctx = logging.WithLogger(ctx, logger)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// statusRecorder captures the status code and size written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// AccessLog logs one line per request with its route pattern, status and duration. A probe that succeeds (/healthz, /readyz: a container asks every few seconds) is logged at
// debug level, so it does not drown the log; a probe that fails is logged like any other request.
func AccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now() //nolint:forbidigo // request latency is a server-time measurement, not a business date
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		level := slog.LevelInfo
		switch {
		case rec.status >= 500:
			level = slog.LevelError
		case rec.status < 400 && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz"):
			level = slog.LevelDebug
		}
		logging.FromContext(r.Context()).Log(r.Context(), level, "http request",
			"method", r.Method,
			"route", r.Pattern,
			"path", r.URL.Path,
			"status", rec.status,
			"bytes", rec.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

// Recover turns a panic into a logged 500 problem response instead of a dropped connection.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			// http.ErrAbortHandler is the sanctioned way to abort a response; let net/http handle it.
			if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(p)
			}
			logging.FromContext(r.Context()).Error("panic in handler", "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			WriteError(w, r, fmt.Errorf("panic: %v", p))
		}()
		next.ServeHTTP(w, r)
	})
}
