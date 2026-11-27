package folios_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/shifts"
)

// KD-30: a deposit found the reservation's open folio, and before it locked the folio another request closed it (CloseFolio
// does not take the reservation lock). The deposit then wrote to a CLOSED folio and was refused at commit with
// CLOSED_FOLIO_UNBALANCED, a message staff could not act on. Now the deposit looks at the folio again once it holds the lock
// and, the folio being closed, opens a new one, as a deposit a moment later would. In cash, with the cashier's shift: the
// shift is still locked before the folio number (the lock order is checked at run time).
func TestKD30DepositWhileTheFolioIsClosed(t *testing.T) {
	f := setup(t)
	if _, err := f.Shifts.Open(f.admin, f.propID, shifts.OpenInput{Drawer: "MAIN"}); err != nil {
		t.Fatal(err)
	}
	old := f.newFolio(t, "F-KD30")
	var res int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT reservation_id FROM folios WHERE id = $1`, old).Scan(&res))
	var version int32
	must(t, f.Pool.QueryRow(context.Background(), `SELECT version FROM folios WHERE id = $1`, old).Scan(&version))

	// a third session holds the folio, so the close and then the deposit queue behind it in that order
	ctx := context.Background()
	hold, err := f.Pool.Begin(ctx)
	must(t, err)
	defer func() { _ = hold.Rollback(ctx) }()
	_, err = hold.Exec(ctx, `SELECT id FROM folios WHERE id = $1 FOR UPDATE`, old)
	must(t, err)

	var wg sync.WaitGroup
	var closeErr, depErr error
	var dep folios.PaymentResult
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, closeErr = f.Folios.CloseFolio(f.admin, f.propID, old, version)
	}()
	waitForFolioLockWaits(t, f, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dep, depErr = f.Folios.Deposit(f.admin, f.propID, res, "kd30", folios.PaymentInput{Amount: "250000", PaymentMethod: "CASH"})
	}()
	waitForFolioLockWaits(t, f, 2) // the deposit found the folio OPEN and waits for it
	must(t, hold.Rollback(ctx))
	wg.Wait()

	must(t, closeErr)
	if depErr != nil {
		t.Fatalf("the deposit after the folio was closed: %v, want it taken on a new folio", depErr)
	}
	if dep.Payment.FolioID == old {
		t.Fatalf("the deposit went to the closed folio %d", old)
	}
	if f.Count(t, `SELECT count(*) FROM folios WHERE id = $1 AND status = 'CLOSED'`, old) != 1 ||
		f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, old) != 0 {
		t.Fatal("the closed folio stays closed and empty")
	}
	if f.Count(t, `SELECT count(*) FROM folios WHERE id = $1 AND reservation_id = $2 AND stay_id IS NULL AND status = 'OPEN'`, dep.Payment.FolioID, res) != 1 || dep.FolioBalance != "-250000" {
		t.Fatalf("the new folio: %+v", dep)
	}
	if f.Count(t, `SELECT count(*) FROM payments WHERE id = $1 AND shift_id IS NOT NULL`, dep.Payment.ID) != 1 {
		t.Fatal("the cash deposit is in the cashier's shift")
	}
}

func waitForFolioLockWaits(t *testing.T, f *fx, n int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second) //nolint:forbidigo // a test waits on the wall clock for other sessions
	for f.Count(t, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query ILIKE '%FROM folios WHERE property_id%FOR UPDATE%'`) < n {
		if time.Now().After(deadline) { //nolint:forbidigo // as above
			t.Fatalf("%d sessions never waited for the folio", n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
