package frontdesk_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/apperr"
)

// Audit F-09 (Architecture 18 test gaps): billing instructions racing the lifecycle of the stay they route.
//
// SetBillingInstructions takes the reservation (L3) and then the stay (L4); check-in takes the reservation; check-out takes the rooms and then the stay; reversing a check-in takes the
// reservation and then the stay. What must hold whatever the interleaving: the company folio of a stay exists at most once (index folios_stay_payer_uk), no folio is OPEN on a stay that is
// CANCELLED (migration 00064) or CHECKED_OUT, the instructions are those of exactly one of the callers, and a loser fails with a code of the application, never a raw database error.

func stillWaiting(ch chan error) bool {
	select {
	case <-ch:
		return false
	case <-time.After(700 * time.Millisecond):
		return true
	}
}

// openFoliosOfLeftStays: an OPEN folio on a stay that is no longer in house, whatever the way it left.
func (f *fx) openFoliosOfLeftStays(t *testing.T) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM folios fo JOIN stays st ON st.property_id = fo.property_id AND st.id = fo.stay_id WHERE st.status <> 'OPEN' AND fo.status = 'OPEN'`)
}

func (f *fx) companyFolios(t *testing.T, stayID int64) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY'`, stayID)
}

func (f *fx) newCompany(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, $3, $3, 1000000) RETURNING id`, f.tenantID, f.propID, code).Scan(&id))
	return id
}

func (f *fx) room(companyID int64) []folios.InstructionInput {
	return []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: companyID}}
}

// appErrorOrNil fails the test for an error that is not one of the application (a raw database error, a deadlock).
func appErrorOrNil(t *testing.T, what string, err error) string {
	t.Helper()
	if err == nil {
		return ""
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("%s: a raw error escaped: %v", what, err)
	}
	return ae.Code
}

// sweep runs a and b at once, b starting `offset` after a, so that a run over several offsets walks b through the window of a.
func sweep(offset time.Duration, a, b func()) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a() }()
	go func() { defer wg.Done(); time.Sleep(offset); b() }()
	wg.Wait()
}

// inHouseReadyToLeave: checked in, no instruction, and the night paid, so that a check-out succeeds.
func inHouseReadyToLeave(t *testing.T) *scene {
	t.Helper()
	f := setup(t)
	s := &scene{fx: f}
	s.res = f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	var err error
	s.stay, err = f.checkIn(t, f.admin, s.res, &f.r101, "")
	must(t, err)
	in := frontdesk.CheckOutInput{Version: s.stay.Stay.Version, ConfirmEarlyDeparture: true}
	_, err = s.Front.CheckOut(s.admin, s.propID, s.stay.Stay.ID, in) // posts the night and says what is owed
	e := code(t, err, "FOLIO_NOT_BALANCED")
	for _, row := range e.Context["folios"].([]map[string]any) {
		folio, _ := row["folio_id"].(int64)
		owed, _ := row["balance"].(string)
		s.pay(t, folio, "settle", owed)
	}
	return s
}

// A: instructions set while the guest checks in. Both orders end with one company folio on the OPEN stay.
func TestInstructionsAndCheckInAtOnceLeaveOneCompanyFolio(t *testing.T) {
	for i := 0; i < 8; i++ {
		f := setup(t)
		res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
		company := f.newCompany(t, "ACME")
		var setErr, inErr error
		var stay frontdesk.CheckInResult
		sweep(time.Duration(i)*3*time.Millisecond,
			func() { stay, inErr = f.checkIn(t, f.admin, res, &f.r101, "") },
			func() {
				_, setErr = f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, f.room(company))
			})
		must(t, inErr)
		must(t, setErr)
		if n := f.companyFolios(t, stay.Stay.ID); n != 1 {
			t.Fatalf("run %d: %d company folios on the stay, want exactly one", i, n)
		}
		if f.openFoliosOfLeftStays(t) != 0 {
			t.Fatal("an open folio on a stay that left")
		}
		if n := f.Count(t, `SELECT count(*) FROM folio_billing_instructions WHERE reservation_room_id = $1`, res.Rooms[0].ID); n != 1 {
			t.Fatalf("%d instructions, want 1", n)
		}
	}
}

// B: instructions set while the guest checks out. If the check-out is first the line is over (INSTRUCTION_LINE_CLOSED); if the instructions are first the stay leaves with its company
// folio closed. A folio opened after the stay left would be an OPEN folio of a stay that is not in house.
func TestInstructionsAndCheckOutAtOnceNeverOpenAFolioOnAStayThatLeft(t *testing.T) {
	outcomes := map[string]int{}
	for i := 0; i < 24; i++ {
		s := inHouseReadyToLeave(t)
		company := s.newCompany(t, "ACME")
		var setErr, outErr error
		sweep(time.Duration(i)*2*time.Millisecond,
			func() {
				_, outErr = s.Front.CheckOut(s.admin, s.propID, s.stay.Stay.ID, frontdesk.CheckOutInput{Version: s.detail(t, s.stay.Stay.ID).Stay.Version, ConfirmEarlyDeparture: true})
			},
			func() {
				_, setErr = s.Folios.SetBillingInstructions(s.admin, s.propID, s.res.ID, s.res.Rooms[0].ID, s.room(company))
			})
		setCode := appErrorOrNil(t, "set", setErr)
		outCode := appErrorOrNil(t, "check-out", outErr)
		outcomes[fmt.Sprintf("set=%q out=%q", setCode, outCode)]++
		// RESOURCE_BUSY is the lock timeout (db/tx.go): contention fails fast with a retriable code of the application
		if setCode != "" && setCode != "INSTRUCTION_LINE_CLOSED" && setCode != "RESOURCE_BUSY" {
			t.Fatalf("run %d: the instructions may only win, find the line over or be busy, got %s", i, setCode)
		}
		if outCode != "" && outCode != "VERSION_CONFLICT" && outCode != "FOLIO_NOT_BALANCED" && outCode != "RESOURCE_BUSY" {
			t.Fatalf("run %d: check-out: %s", i, outCode)
		}
		if n := s.openFoliosOfLeftStays(t); n != 0 {
			t.Fatalf("run %d (%s): %d OPEN folios on a stay that is not in house", i, fmt.Sprint(outcomes), n)
		}
		if n := s.companyFolios(t, s.stay.Stay.ID); n > 1 {
			t.Fatalf("%d company folios on one stay", n)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// C: instructions set while the check-in is reversed: the company folio is closed with the stay, or never opened; none is left OPEN on a cancelled stay.
func TestInstructionsAndReverseCheckInAtOnceLeaveNoOpenFolioOnTheCancelledStay(t *testing.T) {
	for i := 0; i < 24; i++ {
		f := setup(t)
		s := &scene{fx: f}
		s.res = f.book(t, f.dlx, "2026-09-30", "2026-10-02")
		var err error
		s.stay, err = f.checkIn(t, f.admin, s.res, &f.r101, "")
		must(t, err)
		company := f.newCompany(t, "ACME")
		var setErr, revErr error
		sweep(time.Duration(i)*2*time.Millisecond,
			func() { _, revErr = s.reverse(t) },
			func() {
				_, setErr = f.Folios.SetBillingInstructions(f.admin, f.propID, s.res.ID, s.res.Rooms[0].ID, f.room(company))
			})
		setCode := appErrorOrNil(t, "set", setErr)
		revCode := appErrorOrNil(t, "reverse", revErr)
		if revCode != "" {
			t.Fatalf("run %d: the reversal of an empty check-in may not fail: %s", i, revCode)
		}
		if setCode != "" {
			t.Fatalf("run %d: the instructions of a line that is back to confirmed are valid: %s", i, setCode)
		}
		if n := s.openFoliosOfCancelledStays(t); n != 0 {
			t.Fatalf("run %d: %d OPEN folios on the cancelled stay", i, n)
		}
		if f.openFoliosOfLeftStays(t) != 0 {
			t.Fatal("an open folio on a stay that left")
		}
		if n := f.Count(t, `SELECT count(*) FROM folio_billing_instructions WHERE reservation_room_id = $1`, s.res.Rooms[0].ID); n != 1 {
			t.Fatalf("%d instructions, want 1", n)
		}
	}
}

// D: two instructions on one line at once, different companies: one transaction after the other, the last one stands as a whole, no company folio twice, no raw error.
func TestTwoInstructionsOnOneLineAtOnceAreSerialised(t *testing.T) {
	for i := 0; i < 8; i++ {
		s := withCompanyFolio(t) // ACME already routes the room and has its folio
		other := s.newCompany(t, "BETA")
		third := s.newCompany(t, "GAMMA")
		var e1, e2 error
		sweep(time.Duration(i)*2*time.Millisecond,
			func() {
				_, e1 = s.Folios.SetBillingInstructions(s.admin, s.propID, s.res.ID, s.res.Rooms[0].ID, s.room(other))
			},
			func() {
				_, e2 = s.Folios.SetBillingInstructions(s.admin, s.propID, s.res.ID, s.res.Rooms[0].ID, s.room(third))
			})
		must(t, e1)
		must(t, e2)
		// the instructions are those of one caller, whole
		var companies []int64
		rows, err := s.Pool.Query(context.Background(), `SELECT company_id FROM folio_billing_instructions WHERE reservation_room_id = $1`, s.res.Rooms[0].ID)
		must(t, err)
		for rows.Next() {
			var c int64
			must(t, rows.Scan(&c))
			companies = append(companies, c)
		}
		rows.Close()
		if len(companies) != 1 || (companies[0] != other && companies[0] != third) {
			t.Fatalf("run %d: the instructions are those of one caller, got %v", i, companies)
		}
		// no company folio twice for one company, none OPEN beside the stay's
		if n := s.Count(t, `SELECT count(*) FROM (SELECT bill_to_company_id FROM folios WHERE stay_id = $1 AND folio_type = 'COMPANY' GROUP BY bill_to_company_id HAVING count(*) > 1) x`, s.stay.Stay.ID); n != 0 {
			t.Fatalf("run %d: a company has two folios on the stay", i)
		}
		if got := s.companyFolios(t, s.stay.Stay.ID); got != 3 {
			t.Fatalf("run %d: ACME's folio stays (history) and each of the two new companies has one: %d", i, got)
		}
		if s.openFoliosOfLeftStays(t) != 0 {
			t.Fatal("an open folio on a stay that left")
		}
	}
}

// E: the stale read the lock must not leave behind. The instructions read the stay and then wait for its lock; if the stay leaves while they wait, they must see that after the lock.
// The stay is held by a transaction of ours that, like a check-out, ends the stay and closes its folios before it commits.
func TestInstructionsWaitingForTheStayLockSeeTheStayLeave(t *testing.T) {
	s := inHouseReadyToLeave(t)
	company := s.newCompany(t, "ACME")
	ctx := context.Background()
	tx, err := s.Pool.Begin(ctx)
	must(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT id FROM stays WHERE property_id = $1 AND id = $2 FOR UPDATE`, s.propID, s.stay.Stay.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Folios.SetBillingInstructions(s.admin, s.propID, s.res.ID, s.res.Rooms[0].ID, s.room(company))
		done <- err
	}()
	if !stillWaiting(done) {
		t.Fatal("the instructions did not wait for the stay lock")
	}
	// what the committed check-out is, as far as the stay and its folios go
	for _, q := range []string{
		`UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE stay_id = $1`,
		`UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = actual_check_in_at WHERE id = $1`,
		`UPDATE reservation_rooms SET status = 'COMPLETED' WHERE id = (SELECT reservation_room_id FROM stays WHERE id = $1)`,
	} {
		if _, err := tx.Exec(ctx, q, s.stay.Stay.ID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(t, tx.Commit(ctx))
	select {
	case err := <-done:
		if c := appErrorOrNil(t, "set", err); c != "INSTRUCTION_LINE_CLOSED" && c != "" {
			t.Fatalf("the instructions of a stay that left: %s", c)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the instructions never returned")
	}
	if n := s.openFoliosOfLeftStays(t); n != 0 {
		t.Fatalf("%d OPEN folios on a stay that left: the instructions acted on the stay as it was before the lock", n)
	}
}

// F: the same wait, but the stay leaves by a reversal of the check-in. The instructions that waited must not open a company folio on a stay that is CANCELLED (migration 00064 would refuse it
// at commit; the service must not get that far).
func TestInstructionsWaitingForTheStayLockSeeTheCheckInReversed(t *testing.T) {
	f := setup(t)
	s := &scene{fx: f}
	s.res = f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	var err error
	s.stay, err = f.checkIn(t, f.admin, s.res, &f.r101, "")
	must(t, err)
	company := f.newCompany(t, "ACME")
	ctx := context.Background()
	tx, err := f.Pool.Begin(ctx)
	must(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err := tx.Exec(ctx, `SELECT id FROM stays WHERE property_id = $1 AND id = $2 FOR UPDATE`, f.propID, s.stay.Stay.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := f.Folios.SetBillingInstructions(f.admin, f.propID, s.res.ID, s.res.Rooms[0].ID, f.room(company))
		done <- err
	}()
	if !stillWaiting(done) {
		t.Fatal("the instructions did not wait for the stay lock")
	}
	// what the committed reversal is, as far as the stay, its line and its folios go
	for _, q := range []string{
		`UPDATE stays SET status = 'CANCELLED' WHERE id = $1`,
		`UPDATE folios SET stay_id = NULL WHERE stay_id = $1 AND folio_type = 'GUEST'`,
		`UPDATE reservation_rooms SET status = 'CONFIRMED' WHERE id = (SELECT reservation_room_id FROM stays WHERE id = $1)`,
	} {
		if _, err := tx.Exec(ctx, q, s.stay.Stay.ID); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	must(t, tx.Commit(ctx))
	select {
	case err := <-done:
		must(t, err) // the line is a confirmed one again: its instructions are valid
	case <-time.After(10 * time.Second):
		t.Fatal("the instructions never returned")
	}
	if n := s.openFoliosOfCancelledStays(t); n != 0 {
		t.Fatalf("%d OPEN folios on a cancelled stay", n)
	}
	if n := f.companyFolios(t, s.stay.Stay.ID); n != 0 {
		t.Fatalf("%d company folios opened on the cancelled stay", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM folio_billing_instructions WHERE reservation_room_id = $1`, s.res.Rooms[0].ID); n != 1 {
		t.Fatalf("the instructions are stored for the next check-in: %d", n)
	}
}
