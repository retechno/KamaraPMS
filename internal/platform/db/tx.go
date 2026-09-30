package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DBTX is the query interface shared by the pool and a transaction
// (it matches what sqlc-generated code expects).
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ErrNoTx is returned when an operation requires the ambient transaction but
// was called outside TxManager.WithinTx.
var ErrNoTx = errors.New("db: operation requires a transaction (call inside TxManager.WithinTx)")

type txKey struct{}

// txState is the ambient transaction carried in the context.
type txState struct {
	tx    pgx.Tx
	level LockLevel // highest lock level taken so far (see locks.go)
}

func stateFrom(ctx context.Context) *txState {
	st, _ := ctx.Value(txKey{}).(*txState)
	return st
}

// TxManager runs use cases inside one database transaction.
//
// Design rules (docs/architecture/05-transactions-locking.md):
//   - one transaction per use case, opened only by the use case (WithinTx);
//   - services never open or commit transactions; they join the ambient one, so
//     orchestrators (check-in, check-out, night audit) compose them atomically;
//   - READ COMMITTED plus explicit, ordered row locks;
//   - every transaction sets lock_timeout so contention fails fast as RESOURCE_BUSY.
type TxManager struct {
	pool        *pgxpool.Pool
	lockTimeout time.Duration
}

// NewTxManager returns a TxManager. lockTimeout <= 0 disables the per-transaction timeout.
func NewTxManager(pool *pgxpool.Pool, lockTimeout time.Duration) *TxManager {
	return &TxManager{pool: pool, lockTimeout: lockTimeout}
}

// WithinTx runs fn inside a transaction carried by the context passed to fn.
//
// If ctx already carries a transaction, fn joins it: no new transaction, no
// commit, and errors propagate to the outermost WithinTx, which rolls back.
// fn's error is returned after rollback; database errors are mapped to
// *apperr.Error by MapError (including deferred-constraint failures at COMMIT).
// A panic in fn rolls back and re-panics.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if stateFrom(ctx) != nil {
		return fn(ctx)
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return MapError(fmt.Errorf("db: begin: %w", err))
	}
	// Rollback must not be skipped because the request context was cancelled.
	rollback := func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }

	defer func() {
		if p := recover(); p != nil {
			rollback()
			panic(p)
		}
	}()

	if m.lockTimeout > 0 {
		// SET LOCAL does not accept bind parameters; the value is an integer we format ourselves.
		if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL lock_timeout = %d", m.lockTimeout.Milliseconds())); err != nil {
			rollback()
			return MapError(fmt.Errorf("db: set lock_timeout: %w", err))
		}
	}

	if err := fn(context.WithValue(ctx, txKey{}, &txState{tx: tx})); err != nil {
		rollback()
		return MapError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return MapError(fmt.Errorf("db: commit: %w", err))
	}
	return nil
}

// DB returns the ambient transaction if there is one, otherwise the pool.
// Use it for reads that may run inside or outside a use-case transaction.
func (m *TxManager) DB(ctx context.Context) DBTX {
	if st := stateFrom(ctx); st != nil {
		return st.tx
	}
	return m.pool
}

// Tx returns the ambient transaction, or ErrNoTx. Writers and lockers use it
// so they can never run outside a use-case transaction by accident.
func Tx(ctx context.Context) (pgx.Tx, error) {
	if st := stateFrom(ctx); st != nil {
		return st.tx, nil
	}
	return nil, ErrNoTx
}

// InTx reports whether ctx carries a transaction.
func InTx(ctx context.Context) bool { return stateFrom(ctx) != nil }
