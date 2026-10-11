// Package db is the PostgreSQL kernel: connection pool, transaction manager,
// lock-order enforcement and database-error mapping.
package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Open creates a pgx pool. Every connection runs in UTC so timestamptz values
// are never interpreted in a server-local zone; property time zones are applied
// explicitly by the application.
func Open(ctx context.Context, databaseURL string, maxConns int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("db: parse database url: %w", err)
	}
	cfg.MaxConns = maxConns
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	cfg.ConnConfig.RuntimeParams["application_name"] = "kamara-pms"
	// No JIT: the queries of a hotel are short, and the planner over-estimates the cost of a list that is sorted by a computed value (a balance, an arrival), which makes PostgreSQL compile
	// the query first: about a second on a list of 75 000 folios that then runs in 70 ms. Measured: docs/architecture/23-list-sort.md.
	cfg.ConnConfig.RuntimeParams["jit"] = "off"
	// Scan timestamptz as UTC instants (pgx defaults to the process's local zone), so
	// API responses never carry the server machine's offset.
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		conn.TypeMap().RegisterType(&pgtype.Type{
			Name: "timestamptz", OID: pgtype.TimestamptzOID, Codec: &pgtype.TimestamptzCodec{ScanLocation: time.UTC},
		})
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return pool, nil
}
