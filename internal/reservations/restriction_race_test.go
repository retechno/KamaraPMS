package reservations_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/rates"
)

// Audit F-09: a fill of the restriction grid racing a booking (docs/architecture/18-architecture-decisions.md section 4.7, "Locks and transactions").
//
// The design: no new lock level. A booking takes the room types it sells FOR UPDATE (L2) and reads the grid under that lock; an edit of the grid takes the same room types FOR SHARE and
// then the rate plans, as FillRates does. The two exclude each other, so each outcome is one of two: the booking commits first and the restriction applies to the next booking (the
// reservation is never touched), or the restriction commits first and the booking is refused STAY_RESTRICTED with nothing left behind. What must never happen is a booking that read the
// grid before a restriction committed and commits after it.

// nothingOfABooking says the database holds no reservation, no room line and no night price: a refused sale writes nothing.
func (f *fx) nothingOfABooking(t *testing.T) {
	t.Helper()
	for _, table := range []string{"reservations", "reservation_rooms", "reservation_room_rates"} {
		if n := f.count(t, `SELECT count(*) FROM `+table); n != 0 {
			t.Fatalf("a refused booking left %d rows in %s", n, table)
		}
	}
}

func stillWaiting(ch chan error) bool {
	select {
	case <-ch:
		return false
	case <-time.After(700 * time.Millisecond):
		return true
	}
}

// The restriction is mid-commit (it holds the room type for share, as a fill does, and has written the row): the booking waits for it and is then refused.
func TestARestrictionBeingCommittedMakesTheBookingWaitAndRefusesIt(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	fill, err := f.Pool.Begin(ctx)
	must(t, err)
	defer fill.Rollback(ctx) //nolint:errcheck
	if _, err := fill.Exec(ctx, `SELECT id FROM room_types WHERE property_id = $1 AND id = $2 FOR SHARE`, f.propID, f.dlx.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := fill.Exec(ctx, `INSERT INTO rate_restrictions (tenant_id, property_id, room_type_id, stay_date, stop_sell) VALUES ($1, $2, $3, '2026-10-05', true)`, f.tenantID, f.propID, f.dlx.ID); err != nil {
		t.Fatal(err)
	}
	booked := make(chan error, 1)
	go func() {
		_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-05", "2026-10-06")))
		booked <- err
	}()
	if !stillWaiting(booked) {
		t.Fatal("the booking did not wait for the restriction in flight: it would have read the grid before the restriction and committed after it")
	}
	must(t, fill.Commit(ctx))
	select {
	case err := <-booked:
		restricted(t, err, "STOP_SELL")
	case <-time.After(10 * time.Second):
		t.Fatal("the booking never returned")
	}
	f.nothingOfABooking(t)
}

// The booking is mid-flight (it holds the room type for update): an edit of the grid waits for it, and the booking that was made is never touched by the restriction that follows.
func TestABookingInFlightMakesTheFillWaitAndLeavesTheReservationAlone(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	booking, err := f.Pool.Begin(ctx)
	must(t, err)
	defer booking.Rollback(ctx) //nolint:errcheck
	if _, err := booking.Exec(ctx, `SELECT id FROM room_types WHERE property_id = $1 AND id = $2 FOR UPDATE`, f.propID, f.dlx.ID); err != nil {
		t.Fatal(err)
	}
	filled := make(chan error, 1)
	go func() {
		_, err := f.Rates.FillRestrictions(f.admin, f.propID, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-10-05"), To: d("2026-10-06"), Set: rates.RestrictionSet{StopSell: bp(true)}})
		filled <- err
	}()
	if !stillWaiting(filled) {
		t.Fatal("the fill did not wait for the booking in flight")
	}
	must(t, booking.Commit(ctx))
	select {
	case err := <-filled:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the fill never returned")
	}
	// the next booking is refused, an earlier one (made before the fill) is as it was
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-05", "2026-10-06")))
	restricted(t, err, "STOP_SELL")
}

func TestAReservationMadeBeforeARestrictionIsNotTouchedByIt(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-10-05", "2026-10-06")
	f.restrict(t, &f.dlx.ID, nil, "2026-10-05", "2026-10-06", rates.RestrictionSet{StopSell: bp(true)})
	got, err := f.Res.Get(f.admin, f.propID, res.ID)
	must(t, err)
	if got.Status != "CONFIRMED" || got.Rooms[0].Status != "CONFIRMED" || got.Version != res.Version {
		t.Fatalf("the reservation is as it was: %+v", got)
	}
}

// Both really at once, many times, over the kinds of restriction: the outcome is always one of the two valid ones, with nothing half made and no deadlock.
func TestFillAndBookingAtTheSameMomentAreAlwaysOneOfTheTwoValidOutcomes(t *testing.T) {
	kinds := map[string]struct {
		set       rates.RestrictionSet
		arrival   string
		departure string
		violation string
	}{
		"stop sell":           {rates.RestrictionSet{StopSell: bp(true)}, "2026-10-05", "2026-10-06", "STOP_SELL"},
		"closed to arrival":   {rates.RestrictionSet{ClosedToArrival: bp(true)}, "2026-10-05", "2026-10-06", "CLOSED_TO_ARRIVAL"},
		"closed to departure": {rates.RestrictionSet{ClosedToDeparture: bp(true)}, "2026-10-04", "2026-10-05", "CLOSED_TO_DEPARTURE"},
		"minimum stay":        {rates.RestrictionSet{MinStay: ip(3)}, "2026-10-05", "2026-10-06", "MIN_STAY"},
	}
	for name, k := range kinds {
		for i := 0; i < 5; i++ {
			f := setup(t)
			var wg sync.WaitGroup
			var fillErr, bookErr error
			wg.Add(2)
			go func() {
				defer wg.Done()
				time.Sleep(time.Duration(i) * 4 * time.Millisecond) // sweep the window in which the two overlap
				_, fillErr = f.Rates.FillRestrictions(f.admin, f.propID, rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-10-04"), To: d("2026-10-07"), Set: k.set})
			}()
			go func() {
				defer wg.Done()
				_, bookErr = f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, k.arrival, k.departure)))
			}()
			wg.Wait()
			must(t, fillErr)
			// the grid is whole: every date of the fill has its row
			if n := f.count(t, `SELECT count(*) FROM rate_restrictions`); n != 3 {
				t.Fatalf("%s: the fill is whole or absent, got %d rows", name, n)
			}
			if bookErr == nil {
				// the booking won: the reservation is whole, and the grid it did not see now applies to the next one
				if f.count(t, `SELECT count(*) FROM reservations WHERE status = 'CONFIRMED'`) != 1 || f.count(t, `SELECT count(*) FROM reservation_rooms WHERE status = 'CONFIRMED'`) != 1 {
					t.Fatalf("%s: a whole reservation", name)
				}
				_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, k.arrival, k.departure)))
				restricted(t, err, k.violation)
				continue
			}
			// the restriction won: refused for the right reason, nothing left behind
			var ae *apperr.Error
			if !errors.As(bookErr, &ae) || ae.Code != "STAY_RESTRICTED" {
				t.Fatalf("%s run %d: the booking may only win or be restricted, got %v", name, i, bookErr)
			}
			f.nothingOfABooking(t)
		}
	}
}
