package accounting_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
)

// Audit F-07 (migration 00065): financial rows are written only on the OPEN business day. The service call RequireOpenBusinessDay stays; the trigger business_day_must_be_open is the
// database safety net, and these tests go around the service to prove it.

// postedAfterClose counts the rows of the guarded tables that are dated a closed day and were written by a transaction that began writing after the close: the rows that the service gate
// and the trigger exist to prevent. It compares transaction ids (xmin), because a clock is no help: the application clock of a test is a fake one. A row that a transaction wrote
// before the close and that committed before it (the close waits for it) has a smaller id and is not counted.
func (h *hotel) postedAfterClose(t *testing.T) int {
	t.Helper()
	n := 0
	for _, table := range []string{"folio_items", "payments", "stay_charge_postings"} {
		n += h.Count(t, `SELECT count(*) FROM `+table+` r JOIN business_days b ON b.property_id = r.property_id AND b.business_date = r.business_date
			WHERE b.status <> 'OPEN' AND r.xmin::text::bigint > b.xmin::text::bigint`)
	}
	return n
}

func TestTheDatabaseRefusesLedgerRowsDatedAClosedDayWhateverTheService(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	_, err := h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "60000", PaymentMethod: "CASH"})
	must(t, err)
	h.closeDay(t) // day 1 is closed, day 2 is open; the night audit posted the room night (a posting register row)
	before := h.Count(t, `SELECT count(*) FROM folio_items`)

	first := func(table string) int64 {
		var id int64
		must(t, h.Pool.QueryRow(context.Background(), `SELECT min(id) FROM `+table).Scan(&id))
		return id
	}
	day1 := civil.MustParseDate("2026-09-30").String()
	for _, c := range []struct{ table, column string }{{"folio_items", "business_date"}, {"payments", "business_date"}, {"stay_charge_postings", "business_date"}} {
		err := dbtest.CopyRow(context.Background(), h.Pool, c.table, c.column, first(c.table), day1, `{"idempotency_key": null}`)
		if !dbtest.IsClosedDayRefusal(err) {
			t.Errorf("%s dated the closed day 2026-09-30: want business_day_must_be_open, got %v", c.table, err)
		}
	}
	if got := h.Count(t, `SELECT count(*) FROM folio_items`); got != before || h.postedAfterClose(t) != 0 {
		t.Fatalf("a refused insert leaves nothing: %d -> %d rows, %d after close", before, got, h.postedAfterClose(t))
	}
	// the services still work on the open day
	h.charge(t, st.Folio.ID, "RESTAURANT", "25000", "r2")
	_, err = h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "p2", folios.PaymentInput{Amount: "10000", PaymentMethod: "CASH"})
	must(t, err)
}

// The day that counts is the one of the row's own property: closing the day of another property of the tenant does not stop this one from posting.
func TestAnotherPropertysClosedDayDoesNotCount(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	other := h.Property(t, h.tenantID, "JKT")
	must(t, h.Exec(t, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1`, other.ID))
	h.charge(t, st.Folio.ID, "RESTAURANT", "5000", "r2")
	if h.postedAfterClose(t) != 0 {
		t.Fatal("nothing was posted to a closed day")
	}
}

// The share lock: a write that arrives while the night audit holds the day for update waits for it and is then refused; a write that got in first makes the close wait.
type lockScene struct {
	*hotel
	item int64
}

func lockSetup(t *testing.T) *lockScene {
	t.Helper()
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	s := &lockScene{hotel: h}
	must(t, h.Pool.QueryRow(context.Background(), `SELECT min(id) FROM folio_items WHERE transaction_type = 'CHARGE'`).Scan(&s.item))
	return s
}

// closeDayRow is what the night audit does to the row of the day: it locks it FOR UPDATE and sets it CLOSED.
func (s *lockScene) closeDayRow(tx pgx.Tx) error {
	if _, err := tx.Exec(context.Background(), `SELECT 1 FROM business_days WHERE property_id = $1 AND status = 'OPEN' FOR UPDATE`, s.propID); err != nil {
		return err
	}
	_, err := tx.Exec(context.Background(), `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, s.propID)
	return err
}

func stillWaiting(ch chan error) bool {
	select {
	case <-ch:
		return false
	case <-time.After(700 * time.Millisecond):
		return true
	}
}

func TestAWriteInFlightMakesTheCloseWaitAndLandsBeforeIt(t *testing.T) {
	s := lockSetup(t)
	ctx := context.Background()
	writer, err := s.Pool.Begin(ctx)
	must(t, err)
	defer writer.Rollback(ctx) //nolint:errcheck
	must(t, dbtest.CopyRow(ctx, writer, "folio_items", "business_date", s.item, "2026-09-30", `{"idempotency_key": null}`))
	closed := make(chan error, 1)
	go func() {
		closer, err := s.Pool.Begin(ctx)
		if err != nil {
			closed <- err
			return
		}
		defer closer.Rollback(ctx) //nolint:errcheck
		if err := s.closeDayRow(closer); err != nil {
			closed <- err
			return
		}
		closed <- closer.Commit(ctx)
	}()
	if !stillWaiting(closed) {
		t.Fatal("the close went through while a write to the day was in flight")
	}
	must(t, writer.Commit(ctx))
	select {
	case err := <-closed:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the close never went through after the write committed")
	}
	if s.Count(t, `SELECT count(*) FROM folio_items WHERE business_date = '2026-09-30'`) != 2 || s.postedAfterClose(t) != 0 {
		t.Fatal("the write that came first is committed, before the close")
	}
}

func TestAWriteThatArrivesDuringTheCloseWaitsAndIsRefused(t *testing.T) {
	s := lockSetup(t)
	ctx := context.Background()
	closer, err := s.Pool.Begin(ctx)
	must(t, err)
	defer closer.Rollback(ctx) //nolint:errcheck
	must(t, s.closeDayRow(closer))
	wrote := make(chan error, 1)
	go func() {
		writer, err := s.Pool.Begin(ctx)
		if err != nil {
			wrote <- err
			return
		}
		defer writer.Rollback(ctx) //nolint:errcheck
		if err := dbtest.CopyRow(ctx, writer, "folio_items", "business_date", s.item, "2026-09-30", `{"idempotency_key": null}`); err != nil {
			wrote <- err
			return
		}
		wrote <- writer.Commit(ctx)
	}()
	if !stillWaiting(wrote) {
		t.Fatal("the write did not wait for the close in flight: it would have seen the day open and committed after the close")
	}
	must(t, closer.Commit(ctx))
	select {
	case err := <-wrote:
		if !dbtest.IsClosedDayRefusal(err) {
			t.Fatalf("after the close the write must be refused, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the write never returned")
	}
	if s.Count(t, `SELECT count(*) FROM folio_items WHERE business_date = '2026-09-30'`) != 1 || s.postedAfterClose(t) != 0 {
		t.Fatal("nothing was committed on the closed day")
	}
}

// A posting through the service and the night audit at the same moment: whichever way it goes, the closed day and its journal agree with the folios.
func TestPostingAndTheNightAuditRaceLeavesNothingOnAClosedDay(t *testing.T) {
	for i := 0; i < 6; i++ {
		h := setupHotel(t)
		st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
		h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r0")
		day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
		must(t, err)
		h.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
		var wg sync.WaitGroup
		var chargeErr, payErr, auditErr error
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, auditErr = h.Audit.Run(h.admin, h.propID, day.BusinessDate)
		}()
		go func() {
			defer wg.Done()
			var id int64
			if err := h.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'LAUNDRY'`, h.propID).Scan(&id); err != nil {
				chargeErr = err
				return
			}
			u := "40000"
			_, chargeErr = h.Folios.PostCharge(h.admin, h.propID, st.Folio.ID, "race-c", folios.ChargeInput{ChargeCodeID: id, Quantity: "1", UnitPrice: &u})
		}()
		go func() {
			defer wg.Done()
			_, payErr = h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "race-p", folios.PaymentInput{Amount: "30000", PaymentMethod: "CASH"})
		}()
		wg.Wait()
		must(t, auditErr)
		// a posting either landed on the day before it closed, or was posted on the next day, or was refused: never on the closed day after it
		for name, err := range map[string]error{"charge": chargeErr, "payment": payErr} {
			if err == nil {
				continue
			}
			var ae *apperr.Error
			if !errors.As(err, &ae) || (ae.Code != "BUSINESS_DAY_CLOSED" && ae.Code != "RESOURCE_BUSY" && ae.Code != "BUSINESS_DATE_MISMATCH") {
				t.Fatalf("run %d: the %s failed in an unexpected way: %v", i, name, err)
			}
		}
		if n := h.postedAfterClose(t); n != 0 {
			t.Fatalf("run %d: %d rows were committed on a closed day after the close", i, n)
		}
		asOf := day.BusinessDate
		rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
		must(t, err)
		if !rec.Reconciled {
			t.Fatalf("run %d: the closed day and its journal disagree with the folios: %+v", i, rec)
		}
	}
}
