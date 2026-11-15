// Package loginlimit throttles failed sign-in attempts per key (an account, an IP, a challenge). It is shared by the hotel's iam and the platform console's operator.
package loginlimit

import (
	"sync"
	"time"

	"kamarapms/internal/platform/clock"
)

// Limiter counts failed attempts per key in a fixed window (login throttling).
// It is in-memory, i.e. per API instance; a shared store (Redis) can replace it
// when the API runs on several instances.
type Limiter struct {
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

// New returns a Limiter that blocks a key after maxFailures failures within w.
func New(maxFailures int, w time.Duration, c clock.Clock) *Limiter {
	return &Limiter{max: maxFailures, window: w, clock: c, entries: map[string]*window{}}
}

// Allowed reports whether key may try again, and when it may if not.
func (l *Limiter) Allowed(key string) (bool, time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w, ok := l.entries[key]
	if !ok || !l.clock.Now().Before(w.resetAt) {
		return true, time.Time{}
	}
	return w.failures < l.max, w.resetAt
}

// Fail records a failed attempt for key.
func (l *Limiter) Fail(key string) {
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

// Reset forgets key (after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

func (l *Limiter) sweep(now time.Time) {
	for k, w := range l.entries {
		if !now.Before(w.resetAt) {
			delete(l.entries, k)
		}
	}
}
