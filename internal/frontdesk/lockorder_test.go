package frontdesk_test

import (
	"context"
	"testing"
	"time"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/rooms/roomstest"
)

// A writer of a folio of a stay (a payment, a charge, a transfer, an adjustment) holds the FOLIO and then key-shares the STAY, because the item it inserts points at it (folio_items.stay_id). A use
// case that holds the stay row FOR UPDATE and then waits for that folio makes a cycle: the key share waits for the use case, and the use case waits for the folio (SQLSTATE 40P01, shown as
// RESOURCE_BUSY). FOR NO KEY UPDATE keeps another use case away from the stay but lets the key share through (db.ForNoKeyUpdate).
//
// These tests hold the cycle open on purpose, so that they fail every time with the old lock and never by luck: a transaction takes the folio, the use case runs and waits for it, and then the
// transaction takes the key share of the stay, as the foreign key of its item would. The key share must not wait for the use case.

// holdAFolioThenKeyShareTheStay runs op while a transaction holds the folio. It reports whether op waited for the folio; when it did, the key share of the stay must go through at once.
func (f *fx) holdAFolioThenKeyShareTheStay(t *testing.T, folioID, stayID int64, op func() error) (waited bool, opErr error) {
	t.Helper()
	ctx := context.Background()
	tx, err := f.Pool.Begin(ctx)
	must(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT id FROM folios WHERE id = $1 FOR UPDATE`, folioID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- op() }()
	select {
	case err := <-done: // the use case did not need this folio: there is no cycle to hold open
		return false, err
	case <-time.After(700 * time.Millisecond): // it is waiting for the folio
	}
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM stays WHERE id = $1 FOR KEY SHARE`, stayID); err != nil {
		t.Fatalf("the key share of the stay waited for the use case that waits for the folio (a lock cycle): %v", err)
	}
	must(t, tx.Commit(ctx))
	return true, <-done
}

func TestCheckOutLetsAFolioWriterKeyShareTheStay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	waited, err := f.holdAFolioThenKeyShareTheStay(t, st.Folio.ID, st.Stay.ID, func() error {
		_, err := f.Front.CheckOut(f.admin, f.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: st.Stay.Version, ConfirmEarlyDeparture: true})
		return err
	})
	if !waited {
		t.Fatal("the check-out must wait for the guest folio that is held")
	}
	wantCode(t, err, "FOLIO_NOT_BALANCED") // the night is charged and owed: it is not paid, so the stay does not leave; what matters is that it did not deadlock
}

func TestChangingTheRateOfAChargedNightLetsAFolioWriterKeyShareTheStay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	_, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil) // 30 Sep is charged to the guest folio
	must(t, err)
	in := f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-09-30", "1200000")
	in.Approval = f.approval(t)
	waited, err := f.holdAFolioThenKeyShareTheStay(t, st.Folio.ID, st.Stay.ID, func() error {
		_, err := f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, in)
		return err
	})
	if !waited {
		t.Fatal("the rate change of a charged night must wait for the folio that is held")
	}
	must(t, err)
}
