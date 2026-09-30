// Package dbtest provides a real, migrated PostgreSQL database for integration
// tests. Constraint and locking behaviour cannot be mocked, so every test that
// touches SQL runs against PostgreSQL.
//
// Two modes:
//
//   - Default: a disposable container (testcontainers, needs Docker) is started
//     once per test binary.
//   - PMS_TEST_DATABASE_URL set, in the environment or in the repository's .env (no Docker needed, e.g. CI services or cloud
//     sandboxes): every test binary creates its OWN temporary database on that
//     server and drops it afterwards. `go test ./...` runs packages in parallel,
//     so sharing one database would let one package truncate another's data.
//     The URL's role needs the CREATEDB privilege.
//
// Tests are skipped with -short.
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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/migrate"
)

// Image is the PostgreSQL image used for tests (the minimum supported version).
const Image = "postgres:16-alpine"

var (
	once      sync.Once
	pool      *pgxpool.Pool
	container testcontainers.Container
	dropDB    func() // removes the temporary database in external-server mode
	setupErr  error
)

func setup() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Sandboxes (scripts/cloud-setup.sh) record the test server in the repository's .env.
	if root, ok := repoRoot(); ok {
		if err := config.LoadDotEnv(filepath.Join(root, ".env")); err != nil {
			setupErr = err
			return
		}
	}

	var url string
	if server := os.Getenv("PMS_TEST_DATABASE_URL"); server != "" {
		url, dropDB, setupErr = temporaryDatabase(ctx, server)
		if setupErr != nil {
			return
		}
	} else {
		c, err := tcpostgres.Run(ctx, Image,
			tcpostgres.WithDatabase("pms"),
			tcpostgres.WithUsername("pms"),
			tcpostgres.WithPassword("pms"),
			tcpostgres.BasicWaitStrategies(),
		)
		if err != nil {
			setupErr = fmt.Errorf("dbtest: start postgres container (is Docker running? or set PMS_TEST_DATABASE_URL): %w", err)
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

// temporaryDatabase creates a uniquely named database on the server behind
// serverURL and returns its URL and a function that drops it.
func temporaryDatabase(ctx context.Context, serverURL string) (string, func(), error) {
	admin, err := pgx.Connect(ctx, serverURL)
	if err != nil {
		return "", nil, fmt.Errorf("dbtest: connect to PMS_TEST_DATABASE_URL: %w", err)
	}
	defer func() { _ = admin.Close(ctx) }()

	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return "", nil, err
	}
	name := "pms_test_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		return "", nil, fmt.Errorf("dbtest: create temporary database (the role needs CREATEDB): %w", err)
	}

	u, err := url.Parse(serverURL)
	if err != nil {
		return "", nil, fmt.Errorf("dbtest: PMS_TEST_DATABASE_URL must be a postgres:// URL: %w", err)
	}
	u.Path = "/" + name

	drop := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, serverURL)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close(ctx) }()
		_, _ = conn.Exec(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	}
	return u.String(), drop, nil
}

// repoRoot finds the module root (the directory holding go.mod) above the
// test's working directory, which is the package directory.
func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
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

// RunMain runs the package's tests and then removes the test database.
func RunMain(m *testing.M) int {
	code := m.Run()
	if pool != nil {
		pool.Close()
	}
	if dropDB != nil {
		dropDB()
	}
	if container != nil {
		_ = testcontainers.TerminateContainer(container)
	}
	return code
}
