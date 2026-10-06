package db_test

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

type fixture struct {
	pool            *pgxpool.Pool
	txm             *db.TxManager
	tenantID        int64
	propertyID      int64
	roomTypeID      int64
	roomIDs         []int64 // three rooms in propertyID
	otherPropertyID int64
	otherRoomID     int64 // a room in otherPropertyID
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	f := fixture{pool: pool, txm: db.NewTxManager(pool, 5*time.Second)}

	scan := func(dst *int64, sql string, args ...any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql, args...).Scan(dst); err != nil {
			t.Fatalf("seed: %v\n%s", err, sql)
		}
	}
	scan(&f.tenantID, `INSERT INTO tenants (code, name, timezone) VALUES ('ABC', 'ABC Hotels', 'Asia/Jakarta') RETURNING id`)
	property := `INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time)
	             VALUES ($1, $2, $2, 'Asia/Jakarta', 'IDR', 0, '14:00', '12:00') RETURNING id`
	scan(&f.propertyID, property, f.tenantID, "BALI")
	scan(&f.otherPropertyID, property, f.tenantID, "JKT")
	for _, pid := range []int64{f.propertyID, f.otherPropertyID} { // a room has a bed type
		if _, err := pool.Exec(ctx, `INSERT INTO bed_types (tenant_id, property_id, code, name) VALUES ($1, $2, 'KING', 'King')`, f.tenantID, pid); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	roomType := `INSERT INTO room_types (tenant_id, property_id, code, name, max_adult, max_child, max_occupancy, base_occupancy)
	             VALUES ($1, $2, 'DLX', 'Deluxe', 2, 1, 3, 2) RETURNING id`
	scan(&f.roomTypeID, roomType, f.tenantID, f.propertyID)
	var otherRoomType int64
	scan(&otherRoomType, roomType, f.tenantID, f.otherPropertyID)

	room := `INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number, bed_type_id) VALUES ($1, $2, $3, $4, (SELECT id FROM bed_types WHERE property_id = $2 ORDER BY sort_order, id LIMIT 1)) RETURNING id`
	for _, n := range []string{"201", "202", "203"} {
		var id int64
		scan(&id, room, f.tenantID, f.propertyID, f.roomTypeID, n)
		f.roomIDs = append(f.roomIDs, id)
	}
	scan(&f.otherRoomID, room, f.tenantID, f.otherPropertyID, otherRoomType, "101")
	return f
}

func countTenants(t *testing.T, pool *pgxpool.Pool, code string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM tenants WHERE code = $1`, code).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func insertTenant(ctx context.Context, code string) error {
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO tenants (code, name, timezone) VALUES ($1, $1, 'UTC')`, code)
	return err
}

func TestWithinTxCommits(t *testing.T) {
	f := setup(t)
	if err := f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
		return insertTenant(ctx, "T1")
	}); err != nil {
		t.Fatal(err)
	}
	if countTenants(t, f.pool, "T1") != 1 {
		t.Fatal("insert was not committed")
	}
}

func TestWithinTxRollsBackOnError(t *testing.T) {
	f := setup(t)
	boom := errors.New("boom")
	err := f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := insertTenant(ctx, "T1"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v, want boom", err)
	}
	if countTenants(t, f.pool, "T1") != 0 {
		t.Fatal("insert was not rolled back")
	}
}

func TestWithinTxRollsBackOnPanic(t *testing.T) {
	f := setup(t)
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("panic was swallowed")
			}
		}()
		_ = f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
			if err := insertTenant(ctx, "T1"); err != nil {
				return err
			}
			panic("bug")
		})
	}()
	if countTenants(t, f.pool, "T1") != 0 {
		t.Fatal("insert was not rolled back after panic")
	}
}

// Services join the ambient transaction; an error anywhere rolls back everything.
func TestNestedWithinTxJoinsOuterTransaction(t *testing.T) {
	f := setup(t)
	boom := errors.New("inner failed")
	err := f.txm.WithinTx(context.Background(), func(outer context.Context) error {
		outerTx, _ := db.Tx(outer)
		if err := insertTenant(outer, "T1"); err != nil {
			return err
		}
		return f.txm.WithinTx(outer, func(inner context.Context) error {
			innerTx, _ := db.Tx(inner)
			if innerTx != outerTx {
				t.Error("inner WithinTx did not join the outer transaction")
			}
			if err := insertTenant(inner, "T2"); err != nil {
				return err
			}
			return boom
		})
	})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
	if countTenants(t, f.pool, "T1")+countTenants(t, f.pool, "T2") != 0 {
		t.Fatal("joined transaction was not rolled back as a whole")
	}
}

func TestTransactionSettings(t *testing.T) {
	f := setup(t)
	txm := db.NewTxManager(f.pool, 250*time.Millisecond)
	err := txm.WithinTx(context.Background(), func(ctx context.Context) error {
		var iso, timeout, tz string
		if err := txm.DB(ctx).QueryRow(ctx, `SELECT current_setting('transaction_isolation'),
		                                            current_setting('lock_timeout'),
		                                            current_setting('TimeZone')`).Scan(&iso, &timeout, &tz); err != nil {
			return err
		}
		if iso != "read committed" || timeout != "250ms" || tz != "UTC" {
			t.Errorf("isolation=%q lock_timeout=%q timezone=%q", iso, timeout, tz)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWritersRequireAmbientTransaction(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	if _, err := db.Tx(ctx); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("Tx outside a transaction: got %v", err)
	}
	if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("LockRows outside a transaction: got %v", err)
	}
	if db.InTx(ctx) {
		t.Fatal("InTx should be false")
	}
	// Reads may use the pool outside a transaction.
	var one int
	if err := f.txm.DB(ctx).QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil || one != 1 {
		t.Fatalf("DB() outside a transaction: %v", err)
	}
}

func TestLockOrderIsEnforced(t *testing.T) {
	f := setup(t)
	err := f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := db.EnterLockLevel(ctx, db.LevelBusinessDay); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, f.propertyID, []int64{f.roomTypeID}); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1]); err != nil {
			return err
		}
		// Re-entering the same level is allowed (e.g. a service re-locking rows its caller holds).
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1]); err != nil {
			return err
		}
		// Going back down (rooms -> room types) is the classic deadlock recipe.
		return db.LockRows(ctx, db.RoomTypes, db.ForUpdate, f.propertyID, []int64{f.roomTypeID})
	})
	if !errors.Is(err, db.ErrLockOrder) {
		t.Fatalf("got %v, want ErrLockOrder", err)
	}
}

func TestLockRowsIsPropertyScoped(t *testing.T) {
	f := setup(t)
	err := f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
		return db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, []int64{f.roomIDs[0], f.otherRoomID})
	})
	if e, ok := apperr.As(err); !ok || e.Kind != apperr.KindNotFound {
		t.Fatalf("locking another property's room: got %v, want NOT_FOUND", err)
	}
}

// A lock wait longer than lock_timeout becomes a retriable RESOURCE_BUSY error.
func TestLockContentionMapsToBusy(t *testing.T) {
	f := setup(t)
	locked, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
			if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1]); err != nil {
				close(locked)
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	impatient := db.NewTxManager(f.pool, 200*time.Millisecond)
	err := impatient.WithinTx(context.Background(), func(ctx context.Context) error {
		return db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, f.roomIDs[:1])
	})
	close(release)
	if holderErr := <-done; holderErr != nil {
		t.Fatalf("lock holder: %v", holderErr)
	}

	e, ok := apperr.As(err)
	if !ok || e.Code != "RESOURCE_BUSY" || e.Kind != apperr.KindBusy || !e.Retryable {
		t.Fatalf("got %v, want retriable RESOURCE_BUSY", err)
	}
}

// Transactions lock overlapping room sets given in any order; because LockRows
// sorts ids, they queue instead of deadlocking.
func TestLockRowsOrderingPreventsDeadlocks(t *testing.T) {
	f := setup(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			ids := append([]int64(nil), f.roomIDs...)
			rand.New(rand.NewPCG(seed, seed)).Shuffle(len(ids), func(a, b int) { ids[a], ids[b] = ids[b], ids[a] })
			errs <- f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
				if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, f.propertyID, ids); err != nil {
					return err
				}
				time.Sleep(5 * time.Millisecond) // hold the locks briefly to force contention
				return nil
			})
		}(uint64(i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent ordered locking failed: %v", err)
		}
	}
}

func TestTryAdvisoryXactLock(t *testing.T) {
	f := setup(t)
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
			ok, err := db.TryAdvisoryXactLock(ctx, "night_audit", f.propertyID)
			if err != nil || !ok {
				close(held)
				return errors.Join(err, errors.New("first run did not get the lock"))
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	try := func(propertyID int64) bool {
		var got bool
		if err := f.txm.WithinTx(context.Background(), func(ctx context.Context) error {
			var err error
			got, err = db.TryAdvisoryXactLock(ctx, "night_audit", propertyID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if try(f.propertyID) {
		t.Error("a second night audit for the same property got the lock")
	}
	if !try(f.otherPropertyID) {
		t.Error("another property's night audit must not be blocked")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !try(f.propertyID) {
		t.Error("lock was not released at the end of the transaction")
	}
}

// Instants read from the database are UTC regardless of the server machine's zone.
func TestTimestamptzScansAsUTC(t *testing.T) {
	f := setup(t)
	var ts time.Time
	if err := f.pool.QueryRow(context.Background(), `SELECT '2026-09-30 16:07:50+08'::timestamptz`).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	if ts.Location() != time.UTC || !ts.Equal(time.Date(2026, 9, 30, 8, 7, 50, 0, time.UTC)) {
		t.Fatalf("got %v (%v)", ts, ts.Location())
	}
}
