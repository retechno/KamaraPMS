package db

import (
	"context"
	"sync"
)

// LockEvent is one lock a transaction took, in the order it took it: a row lock on Table at Level, or (Table empty) a level entered by a query of the caller's own.
type LockEvent struct {
	Table string
	Level LockLevel
	// Begin marks the start of a transaction: the order of the locks starts again.
	Begin bool
}

// LockTrace records the locks of the transactions that run with its context. It is for tests only: a test that wants to see the locks of a use case runs it with WithLockTrace and reads
// Events afterwards (the lock-order test of the stock level uses it, docs/architecture/35-inventory.md 5.1). Nothing in the application sets it, and without it traceLock does nothing.
type LockTrace struct {
	mu     sync.Mutex
	events []LockEvent
}

type lockTraceKey struct{}

// WithLockTrace returns a context whose transactions are recorded in the returned trace.
func WithLockTrace(ctx context.Context) (context.Context, *LockTrace) {
	t := &LockTrace{}
	return context.WithValue(ctx, lockTraceKey{}, t), t
}

// Events returns a copy of the locks taken so far.
func (t *LockTrace) Events() []LockEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]LockEvent(nil), t.events...)
}

func traceLock(ctx context.Context, table string, level LockLevel) {
	if t, ok := ctx.Value(lockTraceKey{}).(*LockTrace); ok {
		t.mu.Lock()
		t.events = append(t.events, LockEvent{Table: table, Level: level})
		t.mu.Unlock()
	}
}

func traceBegin(ctx context.Context) {
	if t, ok := ctx.Value(lockTraceKey{}).(*LockTrace); ok {
		t.mu.Lock()
		t.events = append(t.events, LockEvent{Begin: true})
		t.mu.Unlock()
	}
}
