// Package migrate applies the embedded goose migrations.
package migrate

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"kamarapms/migrations"
)

// withProvider opens a database/sql view of the pool (closing it does not close
// the pool) and runs fn with a goose provider over the embedded migrations.
func withProvider(pool *pgxpool.Pool, fn func(*goose.Provider) error) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func(db *sql.DB) { _ = db.Close() }(sqlDB)

	p, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return fn(p)
}

// Up applies all pending migrations and returns how many were applied.
func Up(ctx context.Context, pool *pgxpool.Pool) (applied int, err error) {
	err = withProvider(pool, func(p *goose.Provider) error {
		res, err := p.Up(ctx)
		applied = len(res)
		return err
	})
	return applied, err
}

// DownTo rolls back every migration above version (0 = everything).
func DownTo(ctx context.Context, pool *pgxpool.Pool, version int64) (rolledBack int, err error) {
	err = withProvider(pool, func(p *goose.Provider) error {
		res, err := p.DownTo(ctx, version)
		rolledBack = len(res)
		return err
	})
	return rolledBack, err
}

// Version returns the current schema version.
func Version(ctx context.Context, pool *pgxpool.Pool) (version int64, err error) {
	err = withProvider(pool, func(p *goose.Provider) error {
		version, err = p.GetDBVersion(ctx)
		return err
	})
	return version, err
}

// Status returns the state of every migration.
func Status(ctx context.Context, pool *pgxpool.Pool) (status []*goose.MigrationStatus, err error) {
	err = withProvider(pool, func(p *goose.Provider) error {
		status, err = p.Status(ctx)
		return err
	})
	return status, err
}
