package db_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/logging"
)

// A lock wait that times out names the lock it waited for on the error, and logs the transaction that held the table: its pid, its age and its last statement.
func TestALockTimeoutNamesTheLockAndLogsWhoHeldIt(t *testing.T) {
	f := setup(t)
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
			err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1])
			close(locked)
			if err != nil {
				return err
			}
			<-release
			return nil
		})
	}()
	<-locked

	var buf bytes.Buffer
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&buf, nil)))
	impatient := db.NewTxManager(f.pool, 200*time.Millisecond)
	err := impatient.WithinTx(ctx, func(ctx context.Context) error {
		if err := db.LockRows(ctx, db.RoomTypes, db.ForShare, f.propertyID, []int64{f.roomTypeID}); err != nil {
			return err
		}
		return db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1])
	})
	close(release)
	if holderErr := <-done; holderErr != nil {
		t.Fatalf("lock holder: %v", holderErr)
	}

	e, ok := apperr.As(err)
	if !ok || e.Code != "RESOURCE_BUSY" || !e.Retryable {
		t.Fatalf("got %v, want retriable RESOURCE_BUSY", err)
	}
	if e.Context["lock_table"] != "rooms" || e.Context["lock_mode"] != "FOR UPDATE" {
		t.Fatalf("context %v, want the rooms FOR UPDATE", e.Context)
	}

	var entry struct {
		Level     string          `json:"level"`
		Msg       string          `json:"msg"`
		LockTable string          `json:"lock_table"`
		LockMode  string          `json:"lock_mode"`
		Holders   []db.LockHolder `json:"holders"`
	}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log %q: %v", buf.String(), err)
	}
	if entry.Level != "WARN" || entry.Msg != "lock wait timed out" || entry.LockTable != "rooms" || entry.LockMode != "FOR UPDATE" {
		t.Fatalf("log entry %+v", entry)
	}
	if len(entry.Holders) != 1 || !strings.Contains(entry.Holders[0].Query, "FROM rooms") || entry.Holders[0].Seconds <= 0 || entry.Holders[0].PID == 0 {
		t.Fatalf("holders %+v, want the transaction that locked the room", entry.Holders)
	}
}

// Any other failure of a transaction is returned as it was and logs nothing.
func TestAnotherFailureLogsNoLockWait(t *testing.T) {
	f := setup(t)
	var buf bytes.Buffer
	ctx := logging.WithLogger(context.Background(), slog.New(slog.NewJSONHandler(&buf, nil)))
	err := f.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1]); err != nil {
			return err
		}
		return apperr.Conflict("SOMETHING", "no")
	})
	if e, ok := apperr.As(err); !ok || e.Code != "SOMETHING" || e.Context["lock_table"] != nil {
		t.Fatalf("got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("logged %q", buf.String())
	}
}
