package db_test

import (
	"context"
	"testing"

	"kamarapms/internal/platform/db"
)

// A read transaction sees one snapshot: what is committed meanwhile is not seen until it ends, and nothing can be written in it.
func TestWithinReadTxSeesOneSnapshotAndWritesNothing(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	count := func(ctx context.Context) int {
		t.Helper()
		var n int
		if err := f.txm.DB(ctx).QueryRow(ctx, `SELECT count(*) FROM room_types WHERE property_id = $1`, f.propertyID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	err := f.txm.WithinReadTx(ctx, func(ctx context.Context) error {
		before := count(ctx)
		// another session commits a new room type while the snapshot is open
		if _, err := f.pool.Exec(context.Background(), `INSERT INTO room_types (tenant_id, property_id, code, name, max_adult, max_child, max_occupancy, base_occupancy) VALUES ($1, $2, 'ZZZ', 'Late', 2, 0, 2, 2)`, f.tenantID, f.propertyID); err != nil {
			t.Fatal(err)
		}
		if after := count(ctx); after != before {
			t.Fatalf("the snapshot moved: %d then %d", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := count(ctx); got < 2 {
		t.Fatalf("after the transaction the new row is visible: %d", got)
	}

	err = f.txm.WithinReadTx(ctx, func(ctx context.Context) error {
		_, err := f.txm.DB(ctx).Exec(ctx, `UPDATE room_types SET name = 'x' WHERE property_id = $1`, f.propertyID)
		return err
	})
	if err == nil {
		t.Fatal("a write in a read transaction was accepted")
	}
	var name string
	if err := f.pool.QueryRow(ctx, `SELECT name FROM room_types WHERE property_id = $1 AND code = 'ZZZ'`, f.propertyID).Scan(&name); err != nil || name != "Late" {
		t.Fatalf("nothing was written: %q %v", name, err)
	}
}

// Inside a transaction that exists, a read joins it and takes no new one.
func TestWithinReadTxJoinsTheAmbientTransaction(t *testing.T) {
	f := setup(t)
	err := f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
		return f.txm.WithinReadTx(ctx, func(ctx context.Context) error {
			if !db.InTx(ctx) {
				t.Fatal("joined transaction expected")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
}
