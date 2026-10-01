package db

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"kamarapms/internal/platform/apperr"
)

// LockLevel is a position in the global lock order. Within one transaction,
// locks must be taken in non-decreasing level order; taking a lower level after
// a higher one is a programming error (it is how deadlocks are born), so it is
// rejected at runtime with ErrLockOrder.
//
// See docs/architecture/05-transactions-locking.md §1.1.
type LockLevel int

const (
	LevelNone         LockLevel = 0
	LevelNightAudit   LockLevel = 5  // L0: advisory, night audit only
	LevelBusinessDay  LockLevel = 10 // L1: OPEN business_days row (FOR SHARE / FOR UPDATE)
	LevelRoomTypes    LockLevel = 20 // L2
	LevelRooms        LockLevel = 30 // L3
	LevelReservations LockLevel = 40 // L4 (reservations, then their lines)
	LevelStays        LockLevel = 41 // L4
	LevelFolios       LockLevel = 42 // L4
	LevelPayments     LockLevel = 43 // L4
	LevelCompanies    LockLevel = 44 // L4: a city ledger account (serialises its credit limit checks and receipts)
	LevelGroups       LockLevel = 45 // L4: a booking group (its dates and company are stable while a reservation joins it)
	LevelAccounting   LockLevel = 46 // L4: a property's accounting settings row (the chart, the system accounts and the periods are edited under it)
	LevelSequences    LockLevel = 50 // L5: document_sequences (short, last)
)

// ErrLockOrder reports a violation of the global lock order.
var ErrLockOrder = errors.New("db: lock order violation")

// LockMode is the row-lock strength.
type LockMode string

const (
	ForUpdate LockMode = "FOR UPDATE"
	ForShare  LockMode = "FOR SHARE"
)

// LockTable is a property-scoped table that may be row-locked. The set is
// closed (unexported fields) so no caller can inject a table name into SQL.
type LockTable struct {
	name  string
	level LockLevel
}

func (t LockTable) String() string { return t.name }

// Level returns the table's position in the lock order.
func (t LockTable) Level() LockLevel { return t.level }

var (
	RoomTypes        = LockTable{"room_types", LevelRoomTypes}
	Rooms            = LockTable{"rooms", LevelRooms}
	Reservations     = LockTable{"reservations", LevelReservations}
	ReservationRooms = LockTable{"reservation_rooms", LevelReservations}
	Stays            = LockTable{"stays", LevelStays}
	Folios           = LockTable{"folios", LevelFolios}
	Payments         = LockTable{"payments", LevelPayments}
	Companies        = LockTable{"companies", LevelCompanies}
	Groups           = LockTable{"booking_groups", LevelGroups}
	Accounting       = LockTable{"accounting_settings", LevelAccounting}
)

// EnterLockLevel records that the caller is about to take a lock at level with
// its own query (for example the OPEN business day row). It enforces the order.
// Re-entering the same level is allowed.
func EnterLockLevel(ctx context.Context, level LockLevel) error {
	st := stateFrom(ctx)
	if st == nil {
		return ErrNoTx
	}
	if level < st.level {
		return fmt.Errorf("%w: level %d requested after level %d", ErrLockOrder, level, st.level)
	}
	st.level = level
	return nil
}

// LockRows locks rows of one property in ascending id order (deterministic
// order prevents deadlocks between transactions locking overlapping sets).
// Pass every id needed at this level in ONE call. If any id does not exist in
// the property, it returns a NOT_FOUND *apperr.Error.
func LockRows(ctx context.Context, table LockTable, mode LockMode, propertyID int64, ids []int64) error {
	if mode != ForUpdate && mode != ForShare {
		return fmt.Errorf("db: invalid lock mode %q", mode)
	}
	if err := EnterLockLevel(ctx, table.level); err != nil {
		return err
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return nil
	}

	tx, _ := Tx(ctx) // EnterLockLevel has already verified the transaction
	// table.name comes from the closed LockTable set and mode is validated above.
	sql := fmt.Sprintf("SELECT id FROM %s WHERE property_id = $1 AND id = ANY($2) ORDER BY id %s", table.name, mode) //nolint:gosec // G201: identifiers are not user input
	rows, err := tx.Query(ctx, sql, propertyID, ids)
	if err != nil {
		return err
	}
	locked := 0
	for rows.Next() {
		locked++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if locked != len(ids) {
		return apperr.NotFound("NOT_FOUND", fmt.Sprintf("one or more %s were not found", table.name)).
			WithContext("resource", table.name)
	}
	return nil
}

// TryAdvisoryXactLock takes a transaction-scoped advisory lock identified by
// (namespace, id) without waiting. It returns false if another transaction holds
// it. Used by night audit (namespace "night_audit", id = property id) to fail
// fast instead of queueing a second run.
func TryAdvisoryXactLock(ctx context.Context, namespace string, id int64) (bool, error) {
	if err := EnterLockLevel(ctx, LevelNightAudit); err != nil {
		return false, err
	}
	tx, _ := Tx(ctx)
	var ok bool
	key := fmt.Sprintf("%s:%d", namespace, id)
	err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(hashtextextended($1, 0))", key).Scan(&ok)
	return ok, err
}
