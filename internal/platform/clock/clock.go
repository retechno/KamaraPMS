// Package clock provides the current instant (server time).
//
// A Clock answers "what time is it on the server" and is used for *_at columns
// (actual check-in, transaction_at, paid_at, audit created_at). It must NEVER be
// used to derive a hotel business date: the property business date comes only
// from the OPEN business_days row (tenancy.BusinessDayService). The linter
// forbids time.Now outside this package for that reason.
package clock

import (
	"sync"
	"time"
)

// Clock returns the current instant.
type Clock interface {
	Now() time.Time
}

// System is the real clock. It always returns UTC.
type System struct{}

// Now returns the current UTC instant.
func (System) Now() time.Time { return time.Now().UTC() }

// Fake is a controllable clock for tests. It is safe for concurrent use.
type Fake struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a Fake set to t (converted to UTC).
func NewFake(t time.Time) *Fake { return &Fake{now: t.UTC()} }

// Now returns the fake instant.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set moves the clock to t.
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.UTC()
}

// Advance moves the clock forward by d.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
