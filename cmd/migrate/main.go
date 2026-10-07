// Command migrate manages the database schema using the embedded migrations.
//
//	migrate up          apply all pending migrations
//	migrate down        roll back the most recent migration
//	migrate down-to N   roll back to version N (0 = everything)
//	migrate status      list migrations and their state
//	migrate version     print the current schema version
//
// It reads PMS_DATABASE_URL (see internal/platform/config) and nothing else. Changes to the schema hold a database lock, so two jobs started together take turns.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"kamarapms/internal/platform/config"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/migrate"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: migrate up | down | down-to N | status | version")
	}
	// Local development: read ./.env if present (real environment variables take precedence).
	if err := config.LoadDotEnv(".env"); err != nil {
		return err
	}
	databaseURL, err := config.LoadDatabaseURL(os.LookupEnv) // the job needs the database and nothing else: no JWT secret
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := db.Open(ctx, databaseURL, 2)
	if err != nil {
		return err
	}
	defer pool.Close()

	switch args[0] {
	case "up":
		n, err := migrate.Up(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Printf("applied %d migration(s)\n", n)
	case "down", "down-to":
		current, err := migrate.Version(ctx, pool)
		if err != nil {
			return err
		}
		target := current - 1
		if args[0] == "down-to" {
			if len(args) != 2 {
				return fmt.Errorf("usage: migrate down-to N")
			}
			if target, err = strconv.ParseInt(args[1], 10, 64); err != nil || target < 0 {
				return fmt.Errorf("invalid version %q", args[1])
			}
		}
		n, err := migrate.DownTo(ctx, pool, max(target, 0))
		if err != nil {
			return err
		}
		fmt.Printf("rolled back %d migration(s)\n", n)
	case "status":
		status, err := migrate.Status(ctx, pool)
		if err != nil {
			return err
		}
		for _, s := range status {
			applied := "pending"
			if !s.AppliedAt.IsZero() {
				applied = s.AppliedAt.UTC().Format("2006-01-02 15:04:05Z")
			}
			fmt.Printf("%-8s %-45s %s\n", s.State, s.Source.Path, applied)
		}
	case "version":
		v, err := migrate.Version(ctx, pool)
		if err != nil {
			return err
		}
		fmt.Println(v)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	return nil
}
