// Package migrate applies the embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"kamarapms/migrations"
)

// withProvider opens a database/sql view of the pool (closing it does not close
// the pool) and runs fn with a goose provider over the embedded migrations. A provider
// that changes the schema (locked) holds a PostgreSQL session advisory lock while it runs,
// so two migration jobs started together (two instances, a retried deploy) take turns:
// the second one waits, finds nothing to do and ends.
func withProvider(pool *pgxpool.Pool, locked bool, fn func(*goose.Provider) error) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func(db *sql.DB) { _ = db.Close() }(sqlDB)

	var opts []goose.ProviderOption
	if locked {
		locker, err := lock.NewPostgresSessionLocker()
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		opts = append(opts, goose.WithSessionLocker(locker))
	}
	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, opts...)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return fn(p)
}

// Up applies all pending migrations and returns how many were applied.
func Up(ctx context.Context, pool *pgxpool.Pool) (applied int, err error) {
	err = withProvider(pool, true, func(p *goose.Provider) error {
		res, err := p.Up(ctx)
		applied = len(res)
		return err
	})
	return applied, err
}

// DownTo rolls back every migration above version (0 = everything).
func DownTo(ctx context.Context, pool *pgxpool.Pool, version int64) (rolledBack int, err error) {
	err = withProvider(pool, true, func(p *goose.Provider) error {
		res, err := p.DownTo(ctx, version)
		rolledBack = len(res)
		return err
	})
	return rolledBack, err
}

// Version returns the current schema version.
func Version(ctx context.Context, pool *pgxpool.Pool) (version int64, err error) {
	err = withProvider(pool, false, func(p *goose.Provider) error {
		version, err = p.GetDBVersion(ctx)
		return err
	})
	return version, err
}

// Status returns the state of every migration.
func Status(ctx context.Context, pool *pgxpool.Pool) (status []*goose.MigrationStatus, err error) {
	err = withProvider(pool, false, func(p *goose.Provider) error {
		status, err = p.Status(ctx)
		return err
	})
	return status, err
}

// Latest is the highest version among the migrations embedded in this binary.
func Latest() (int64, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return 0, fmt.Errorf("migrate: %w", err)
	}
	var latest int64
	for _, e := range entries {
		num, _, ok := strings.Cut(e.Name(), "_")
		if !ok || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		if v, err := strconv.ParseInt(num, 10, 64); err == nil && v > latest {
			latest = v
		}
	}
	if latest == 0 {
		return 0, fmt.Errorf("migrate: no migration is embedded")
	}
	return latest, nil
}

// SchemaCheck is a readiness check: the database has every migration this binary knows. A binary that starts before the migration job has finished is not ready, and
// no traffic is sent to it. A database that is ahead of the binary (a rolling release with the new schema already applied) is ready: migrations are written to be
// compatible with the release before them.
func SchemaCheck(pool *pgxpool.Pool) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		latest, err := Latest()
		if err != nil {
			return err
		}
		current, err := readVersion(ctx, pool)
		if err != nil {
			return fmt.Errorf("the schema version cannot be read: %w", err)
		}
		if current < latest {
			return fmt.Errorf("the schema is at version %d and this release needs %d: run the migrations", current, latest)
		}
		return nil
	}
}

// readVersion reads the applied version without writing anything (the goose provider creates its table when it is missing, which a readiness probe must not do). A database
// that has no migration table yet is at version 0.
func readVersion(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var v sql.NullInt64
	err := pool.QueryRow(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).Scan(&v)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return v.Int64, nil
}
