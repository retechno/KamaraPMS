package httpx

import (
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kamarapms/internal/platform/apperr"
)

// maxBuckets bounds the limiter's memory: past it, buckets that are full again (idle clients) are dropped.
const maxBuckets = 10000

type bucket struct {
	tokens float64
	last   time.Time
}

// RateLimiter is a token bucket per client address: perMinute requests on average, bursts up to perMinute.
// It is in-memory and per process; the sign-in and approval attempts have their own, stricter limits in iam.
type RateLimiter struct {
	perMinute float64
	now       func() time.Time
	mu        sync.Mutex
	buckets   map[string]*bucket
}

// NewRateLimiter returns a limiter allowing perMinute requests a minute per key. now is the injected clock.
func NewRateLimiter(perMinute int, now func() time.Time) *RateLimiter {
	return &RateLimiter{perMinute: float64(perMinute), now: now, buckets: map[string]*bucket{}}
}

// Allow takes a token for key. When none is left it reports how long until one is.
func (l *RateLimiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets {
			l.sweep(now)
		}
		b = &bucket{tokens: l.perMinute, last: now}
		l.buckets[key] = b
	}
	if el := now.Sub(b.last); el > 0 {
		b.tokens = math.Min(l.perMinute, b.tokens+el.Seconds()*l.perMinute/60)
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	need := (1 - b.tokens) * 60 / l.perMinute
	return false, time.Duration(math.Ceil(need)) * time.Second
}

// sweep drops buckets that have refilled completely.
func (l *RateLimiter) sweep(now time.Time) {
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.perMinute/60 >= l.perMinute {
			delete(l.buckets, k)
		}
	}
}

// RateLimit rejects requests over the limit with 429 TOO_MANY_REQUESTS and a Retry-After header. The key is the
// client address (see ClientIPFrom: behind a trusted proxy it is the address the proxy forwarded, one bucket per person). Health probes are exempt; a limit of 0 or less disables it.
func RateLimit(perMinute int, now func() time.Time) Middleware {
	if perMinute <= 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	l := NewRateLimiter(perMinute, now)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
				next.ServeHTTP(w, r)
				return
			}
			key := "-"
			if ip, ok := ClientIPFrom(r.Context()); ok {
				key = ip.String()
			}
			if ok, wait := l.Allow(key); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(wait/time.Second)))
				WriteError(w, r, apperr.New(apperr.KindRateLimited, "TOO_MANY_REQUESTS", "too many requests; slow down and try again shortly").
					WithContext("retry_after_seconds", int(wait/time.Second)))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
