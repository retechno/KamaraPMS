package db

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/logging"
)

// A lock wait that runs out (lock_timeout, SQLSTATE 55P03) is a RESOURCE_BUSY: another transaction kept a row longer than the timeout. Which one is the question that matters, and
// it is gone once the waiter has rolled back. So the waiter says which lock it asked for last (the context of the error: lock_table, lock_mode) and logs, as a warning, the open
// transactions that hold that table at that moment, oldest first, with their age and their last statement (docs/architecture/05-transactions-locking.md, section 1).

// LockHolder is an open transaction that held the table a lock wait timed out on.
type LockHolder struct {
	PID       int32   `json:"pid"`
	State     string  `json:"state"`
	Seconds   float64 `json:"seconds"` // how long its transaction had been open
	WaitEvent string  `json:"wait_event,omitempty"`
	Query     string  `json:"query"` // its current or last statement, cut to 300 characters
}

// lockHoldersSQL lists the other open transactions of the database, oldest first; with a table, only those holding a lock on it (a row lock takes a lock on its table, which pg_locks shows).
const lockHoldersSQL = `
SELECT a.pid, coalesce(a.state, ''), coalesce(extract(epoch FROM clock_timestamp() - a.xact_start), 0)::float8,
       coalesce(a.wait_event_type, ''), left(coalesce(a.query, ''), 300)
FROM pg_stat_activity a
WHERE a.datname = current_database() AND a.pid <> pg_backend_pid() AND a.xact_start IS NOT NULL
  AND ($1::text = '' OR EXISTS (SELECT 1 FROM pg_locks l WHERE l.pid = a.pid AND l.granted AND l.relation = to_regclass($1::text)))
ORDER BY a.xact_start
LIMIT 5`

// levelTable names the table of a level whose lock is taken by a query of its own (EnterLockLevel); "" for a level of several tables.
func levelTable(level LockLevel) string {
	switch level {
	case LevelBusinessDay:
		return "business_days"
	case LevelSubscription:
		return "subscriptions"
	}
	return ""
}

// lockWait is the end of a transaction that failed: when the failure is a lock wait that timed out, it names the lock on the error and logs who held it. Anything else is returned as is.
func (m *TxManager) lockWait(ctx context.Context, st *txState, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
		return err
	}
	if e, ok := apperr.As(err); ok {
		if st.lastTable != "" {
			e.WithContext("lock_table", st.lastTable)
		}
		if st.lastMode != "" {
			e.WithContext("lock_mode", string(st.lastMode))
		}
	}
	holders, qerr := m.lockHolders(ctx, st.lastTable)
	attrs := []slog.Attr{
		slog.String("lock_table", st.lastTable),
		slog.String("lock_mode", string(st.lastMode)),
		slog.Duration("lock_timeout", m.lockTimeout),
		slog.Any("holders", holders),
	}
	if qerr != nil {
		attrs = append(attrs, slog.String("holders_error", qerr.Error()))
	}
	logging.FromContext(ctx).LogAttrs(ctx, slog.LevelWarn, "lock wait timed out", attrs...)
	return err
}

// lockHolders reads, on a connection of its own and for two seconds at most, the open transactions that hold the table (all of them when the table is not known).
func (m *TxManager) lockHolders(ctx context.Context, table string) ([]LockHolder, error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	rows, err := m.pool.Query(ctx, lockHoldersSQL, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LockHolder{}
	for rows.Next() {
		var h LockHolder
		if err := rows.Scan(&h.PID, &h.State, &h.Seconds, &h.WaitEvent, &h.Query); err != nil {
			return out, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
