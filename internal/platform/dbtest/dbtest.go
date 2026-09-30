// Package dbtest provides a real, migrated PostgreSQL database for integration
// tests. Constraint and locking behaviour cannot be mocked, so every test that
// touches SQL runs against PostgreSQL.
//
// By default a disposable container (testcontainers) is started once per test
// binary. Set PMS_TEST_DATABASE_URL to use an existing, empty database instead
// (for example a CI service container). Tests are skipped with -short.
//
// Usage:
//
//	func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }
//
//	func TestSomething(t *testing.T) {
//	    pool := dbtest.Pool(t)
//	    dbtest.Reset(t, pool)
//	    ...
//	}
package dbtest

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/migrate"
)

// Image is the PostgreSQL image used for tests (the minimum supported version).
const Image = "postgres:16-alpine"

var (
	once      sync.Once
	pool      *pgxpool.Pool
	container testcontainers.Container
	setupErr  error
)

func setup() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	url := os.Getenv("PMS_TEST_DATABASE_URL")
	if url == "" {
		c, err := tcpostgres.Run(ctx, Image,
			tcpostgres.WithDatabase("pms"),
			tcpostgres.WithUsername("pms"),
			tcpostgres.WithPassword("pms"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			setupErr = fmt.Errorf("dbtest: start postgres container: %w", err)
			return
		}
		container = c
		if url, err = c.ConnectionString(ctx, "sslmode=disable"); err != nil {
			setupErr = fmt.Errorf("dbtest: connection string: %w", err)
			return
		}
	}

	p, err := db.Open(ctx, url, 10)
	if err != nil {
		setupErr = err
		return
	}
	if _, err := migrate.Up(ctx, p); err != nil {
		p.Close()
		setupErr = fmt.Errorf("dbtest: migrate: %w", err)
		return
	}
	pool = p
}

// Pool returns the shared, migrated pool, starting the database on first use.
func Pool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("database test skipped in -short mode")
	}
	once.Do(setup)
	if setupErr != nil {
		t.Fatal(setupErr)
	}
	return pool
}

// Reset empties every application table (keeping the migration history) so a
// test starts from a clean database. Tests using Reset must not run in parallel.
func Reset(t testing.TB, p *pgxpool.Pool) {
	t.Helper()
	_, err := p.Exec(context.Background(), `
DO $$
DECLARE tables text;
BEGIN
    SELECT string_agg(format('%I', tablename), ', ') INTO tables
      FROM pg_tables
     WHERE schemaname = 'public' AND tablename <> 'goose_db_version';
    IF tables IS NOT NULL THEN
        EXECUTE 'TRUNCATE ' || tables || ' RESTART IDENTITY CASCADE';
    END IF;
END $$`)
	if err != nil {
		t.Fatalf("dbtest: reset: %v", err)
	}
}

// RunMain runs the package's tests and then stops the database container.
func RunMain(m *testing.M) int {
	code := m.Run()
	if pool != nil {
		pool.Close()
	}
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
	return code
}
