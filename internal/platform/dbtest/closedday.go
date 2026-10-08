package dbtest

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Querier is what a pool and a transaction have in common.
type Querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CopyRow inserts, with no service and no RequireOpenBusinessDay in the way, a copy of the row id of table dated day (the column dateColumn), with the extra columns of overrides
// (a JSON object, "{}" for none) set over the copy. Identity and generated columns are left to their defaults. It is the direct database bypass of audit F-07. It returns the
// PostgreSQL error of the insert. The trigger of migration 00065 runs before the unique and the foreign key checks, so whether the database lets a ledger row be dated a closed day
// does not depend on the copy being a valid new row.
func CopyRow(ctx context.Context, q Querier, table, dateColumn string, id int64, day, overrides string) error {
	var cols string
	if err := q.QueryRow(ctx, `SELECT string_agg(format('%I', column_name), ', ' ORDER BY ordinal_position) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = $1 AND is_identity = 'NO' AND is_generated = 'NEVER'`, table).Scan(&cols); err != nil {
		return err
	}
	sql := fmt.Sprintf(`INSERT INTO %[1]s (%[2]s) SELECT %[2]s FROM jsonb_populate_record(NULL::%[1]s, (SELECT to_jsonb(t) - 'id' || jsonb_build_object($2::text, $3::text) || $4::jsonb FROM %[1]s t WHERE t.id = $1))`,
		pgIdent(table), cols)
	_, err := q.Exec(ctx, sql, id, dateColumn, day, overrides)
	return err
}

// InsertCopyOnClosedDay closes the OPEN business day of the property of the row if it is still open (or takes the latest closed one) and inserts a copy of the row dated that
// closed day (CopyRow). It returns the PostgreSQL error of the insert, nil if the database accepted the row.
func InsertCopyOnClosedDay(t testing.TB, pool *pgxpool.Pool, table, dateColumn string, id int64) error {
	t.Helper()
	ctx := context.Background()
	var propertyID int64
	if err := pool.QueryRow(ctx, fmt.Sprintf(`SELECT property_id FROM %s WHERE id = $1`, pgIdent(table)), id).Scan(&propertyID); err != nil {
		t.Fatalf("no row %d in %s: %v", id, table, err)
	}
	var day string
	err := pool.QueryRow(ctx, `SELECT business_date::text FROM business_days WHERE property_id = $1 AND status = 'OPEN'`, propertyID).Scan(&day)
	if err == nil {
		if _, err := pool.Exec(ctx, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND business_date = $2::date`, propertyID, day); err != nil {
			t.Fatalf("closing the business day: %v", err)
		}
	} else if err := pool.QueryRow(ctx, `SELECT max(business_date)::text FROM business_days WHERE property_id = $1 AND status = 'CLOSED'`, propertyID).Scan(&day); err != nil {
		t.Fatalf("no closed business day: %v", err)
	}
	return CopyRow(ctx, pool, table, dateColumn, id, day, "{}")
}

// IsClosedDayRefusal says whether err is the refusal of migration 00065 (SQLSTATE 23514, constraint business_day_must_be_open).
func IsClosedDayRefusal(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23514" && pg.ConstraintName == "business_day_must_be_open"
}

// pgIdent quotes a table name that comes from a test, never from a user.
func pgIdent(name string) string { return `"` + name + `"` }
