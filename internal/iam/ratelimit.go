package iam

import (
	"sync"
	"time"

	"kamarapms/internal/platform/clock"
)

// limiter counts failed attempts per key in a fixed window (login throttling).
// It is in-memory, i.e. per API instance; a shared store (Redis) can replace it
// when the API runs on several instances.
type limiter struct {
	mu      sync.Mutex
	max     int
	window  time.Duration
	clock   clock.Clock
	entries map[string]*window
}

type window struct {
	failures int
	resetAt  time.Time
}

func newLimiter(maxFailures int, w time.Duration, c clock.Clock) *limiter {
	return &limiter{max: maxFailures, window: w, clock: c, entries: map[string]*window{}}
}

// allowed reports whether key may try again, and when it may if not.
func (l *limiter) allowed(key string) (bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.entries[key]
	if !ok || !l.clock.Now().Before(w.resetAt) {
		return true, time.Time{}
	}
	return w.failures < l.max, w.resetAt
}

// fail records a failed attempt for key.
func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	w, ok := l.entries[key]
	if !ok || !now.Before(w.resetAt) {
		if len(l.entries) > 10_000 {
			l.sweep(now)
		}
		w = &window{resetAt: now.Add(l.window)}
		l.entries[key] = w
	}
	w.failures++
}

// reset forgets key (after a successful login).
func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *limiter) sweep(now time.Time) {
	for k, w := range l.entries {
		if !now.Before(w.resetAt) {
			delete(l.entries, k)
		}
	}
}
