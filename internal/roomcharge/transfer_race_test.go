package roomcharge_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/rooms/roomstest"
)

// Audit F-09: moving a charge between two folios of a stay while the room charges are posted.
//
// The posting run locks the stays (L4) and then the folios (L5, ascending); a transfer locks the two folios ascending. The register of a room night (stay_charge_postings) points at the live
// charge: a transfer reverses the charge and posts its copy, and moves the register to the copy in the same transaction, so a posting run that comes after finds the night posted and a run that
// came before never sees it half moved. What must hold whatever the interleaving: the night is charged once, the register has one POSTED row for it and it is the live charge, the folios add up to
// what was posted, and nothing deadlocks or fails with a raw database error.

func appCodeOf(t *testing.T, what string, err error) string {
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

func stillWaiting(ch chan error) bool {
	select {
	case <-ch:
		return false
	case <-time.After(700 * time.Millisecond):
		return true
	}
}

func sweep(offset time.Duration, a, b func()) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); a() }()
	go func() { defer wg.Done(); time.Sleep(offset); b() }()
	wg.Wait()
}

type transferScene struct {
	*fx
	acme            int64
	guestFolio      int64
	companyFolio    int64
	firstNightItem  int64
	firstNightTotal string
	stayID          int64
}

// aNightOnTheGuestFolio: a two-night stay whose first night is posted to the guest folio, and then billed to a company from the next night on (the company folio exists, empty).
func aNightOnTheGuestFolio(t *testing.T) *transferScene {
	t.Helper()
	f := setup(t)
	s := &transferScene{fx: f}
	s.acme = f.company(t, "ACME")
	st := f.stay(t, f.r101, "2026-10-03")
	s.stayID, s.guestFolio = st.Stay.ID, st.Folio.ID
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if len(res.Results) != 1 || res.Results[0].Status != "POSTED" {
		t.Fatalf("the first night: %+v", res.Results)
	}
	s.firstNightItem, s.firstNightTotal = *res.Results[0].FolioItemID, res.Results[0].Total
	var line, reservation int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT rr.id, rr.reservation_id FROM stays s JOIN reservation_rooms rr ON rr.property_id = s.property_id AND rr.id = s.reservation_room_id WHERE s.id = $1`, s.stayID).Scan(&line, &reservation))
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, reservation, line, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: s.acme}})
	must(t, err)
	s.companyFolio = f.companyFolioID(t, s.stayID, s.acme)
	return s
}

func (s *transferScene) transfer() error {
	_, err := s.Folios.TransferItem(s.admin, s.propID, s.firstNightItem, folios.TransferItemInput{FolioID: s.companyFolio, Reason: "company pays the room",
		Approval: &iam.ApprovalInput{Email: s.adminEmail, Password: roomstest.Password}})
	return err
}

func (s *transferScene) sum(t *testing.T, where string, args ...any) string {
	t.Helper()
	var v string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT trim_scale(COALESCE(sum(debit - credit), 0))::text FROM folio_items WHERE `+where, args...).Scan(&v))
	return v
}

// nightsAreChargedOnce: for each night of the stay at most one POSTED register row and one live (not reversed) charge, and the register points at it.
func (s *transferScene) nightsAreChargedOnce(t *testing.T) {
	t.Helper()
	if n := s.Count(t, `SELECT count(*) FROM (SELECT service_date FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED' GROUP BY service_date HAVING count(*) > 1) x`, s.stayID); n != 0 {
		t.Fatalf("a night is registered twice")
	}
	live := `SELECT count(*) FROM folio_items i WHERE i.transaction_type = 'CHARGE' AND i.source IN ('ROOM_POSTING','TRANSFER') AND i.stay_id = $1 AND NOT EXISTS (SELECT 1 FROM folio_items r WHERE r.reverses_item_id = i.id)`
	registered := s.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED'`, s.stayID)
	if got := s.Count(t, live, s.stayID); got != registered {
		t.Fatalf("%d live room charges against %d registered nights: a night is charged twice or the register lost it", got, registered)
	}
	if n := s.Count(t, `SELECT count(*) FROM stay_charge_postings p WHERE p.stay_id = $1 AND p.status = 'POSTED' AND EXISTS (SELECT 1 FROM folio_items r WHERE r.reverses_item_id = p.folio_item_id)`, s.stayID); n != 0 {
		t.Fatalf("%d register rows point at a charge that was reversed", n)
	}
}

// Transfer of the posted night while the same night is posted again (a second run): the run says ALREADY_POSTED or finds the night where the transfer put it, never charges it again.
func TestTransferAndAPostingRunOfTheSameNightChargeItOnce(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := aNightOnTheGuestFolio(t)
		var trErr, postErr error
		sweep(time.Duration(i)*3*time.Millisecond,
			func() { trErr = s.transfer() },
			func() { _, postErr = s.Charges.PostManual(s.admin, s.propID, roomstest.BD, nil) })
		must(t, trErr)
		must(t, postErr)
		s.nightsAreChargedOnce(t)
		if s.sum(t, `folio_id = $1`, s.guestFolio) != "0" || s.sum(t, `folio_id = $1`, s.companyFolio) != s.firstNightTotal {
			t.Fatalf("run %d: the night moved whole: guest %s, company %s, want 0 and %s", i, s.sum(t, `folio_id = $1`, s.guestFolio), s.sum(t, `folio_id = $1`, s.companyFolio), s.firstNightTotal)
		}
		if n := s.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED' AND folio_item_id IN (SELECT id FROM folio_items WHERE folio_id = $2)`, s.stayID, s.companyFolio); n != 1 {
			t.Fatalf("run %d: the register must point at the copy on the company folio, got %d", i, n)
		}
	}
}

// Transfer of last night's charge while tonight is posted: tonight goes to the company folio (the instruction), the transferred night lands there too, nothing is lost or doubled.
func TestTransferAndTonightsPostingRunBothComplete(t *testing.T) {
	for i := 0; i < 10; i++ {
		s := aNightOnTheGuestFolio(t)
		s.nextDay(t)
		today := s.currentBD(t)
		var trErr, postErr error
		var posted string
		sweep(time.Duration(i)*3*time.Millisecond,
			func() { trErr = s.transfer() },
			func() {
				r, err := s.Charges.PostManual(s.admin, s.propID, today, nil)
				postErr = err
				for _, it := range r.Results {
					if err == nil && it.Status == "POSTED" {
						posted = it.Total
					}
				}
			})
		if c := appCodeOf(t, "transfer", trErr); c != "" && c != "RESOURCE_BUSY" {
			t.Fatalf("run %d: transfer: %v", i, trErr)
		}
		if c := appCodeOf(t, "post", postErr); c != "" && c != "RESOURCE_BUSY" {
			t.Fatalf("run %d: post: %v", i, postErr)
		}
		if trErr != nil || postErr != nil { // busy: the lock timeout is the fail-fast of db/tx.go; the one that was refused is run again, as a caller does
			if trErr != nil {
				must(t, s.transfer())
			}
			if postErr != nil {
				r, err := s.Charges.PostManual(s.admin, s.propID, today, nil)
				must(t, err)
				// the run lists every night up to today, the first one as ALREADY_POSTED with no total: tonight is the night it posted
				posted = ""
				for _, it := range r.Results {
					if it.Status == "POSTED" {
						posted = it.Total
					}
				}
			}
		}
		s.nightsAreChargedOnce(t)
		if posted != s.firstNightTotal {
			t.Fatalf("run %d: tonight posted %q, want %s", i, posted, s.firstNightTotal)
		}
		if s.sum(t, `folio_id = $1`, s.guestFolio) != "0" {
			t.Fatalf("run %d: the guest folio is %s", i, s.sum(t, `folio_id = $1`, s.guestFolio))
		}
		if got := s.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED'`, s.stayID); got != 2 {
			t.Fatalf("run %d: two nights registered, got %d", i, got)
		}
		if s.sum(t, `folio_id = $1`, s.companyFolio) != double(t, s, s.firstNightTotal) {
			t.Fatalf("run %d: the company folio holds both nights: %s", i, s.sum(t, `folio_id = $1`, s.companyFolio))
		}
	}
}

func double(t *testing.T, s *transferScene, v string) string {
	t.Helper()
	var out string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT trim_scale($1::numeric * 2)::text`, v).Scan(&out))
	return out
}

// The same charge moved twice at once: one winner; the other is told the item is no longer where it was (ALREADY_REVERSED), and the folios are as after one transfer.
func TestTwoTransfersOfOneNightHaveOneWinner(t *testing.T) {
	s := aNightOnTheGuestFolio(t)
	const n = 4
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = s.transfer() }()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch c := appCodeOf(t, "transfer", err); c {
		case "":
			wins++
		case "ALREADY_REVERSED", "RESOURCE_BUSY":
		default:
			t.Fatalf("a loser may only be told the charge is gone or that the resource is busy, got %s", c)
		}
	}
	if wins != 1 {
		t.Fatalf("one winner, got %d (%v)", wins, errs)
	}
	s.nightsAreChargedOnce(t)
	if s.sum(t, `folio_id = $1`, s.guestFolio) != "0" || s.sum(t, `folio_id = $1`, s.companyFolio) != s.firstNightTotal {
		t.Fatalf("the night is on the company folio once: guest %s, company %s", s.sum(t, `folio_id = $1`, s.guestFolio), s.sum(t, `folio_id = $1`, s.companyFolio))
	}
	if got := s.Count(t, `SELECT count(*) FROM folio_items WHERE reference_type = 'FOLIO_TRANSFER' AND reference_id = $1`, itoa(s.firstNightItem)); got != 2 {
		t.Fatalf("one reversal and one copy, got %d items", got)
	}
}

func itoa(v int64) string {
	var b []byte
	if v == 0 {
		return "0"
	}
	for ; v > 0; v /= 10 {
		b = append([]byte{byte('0' + v%10)}, b...)
	}
	return string(b)
}

// The posting run is mid-flight on the stay (it holds the stay): a transfer of that stay's charge waits for nothing it needs and the run waits for nothing the transfer holds only when it
// needs the same folio. Here the transfer must not complete half way while the run holds the folios: it waits for the folios and then completes whole.
func TestTransferWaitsForAFolioLockedByARunAndThenCompletesWhole(t *testing.T) {
	s := aNightOnTheGuestFolio(t)
	ctx := context.Background()
	run, err := s.Pool.Begin(ctx)
	must(t, err)
	defer run.Rollback(ctx) //nolint:errcheck
	if _, err := run.Exec(ctx, `SELECT id FROM folios WHERE property_id = $1 AND id IN ($2, $3) ORDER BY id FOR UPDATE`, s.propID, s.guestFolio, s.companyFolio); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.transfer() }()
	if !stillWaiting(done) {
		t.Fatal("the transfer did not wait for the folios held by the run")
	}
	if s.sum(t, `folio_id = $1`, s.guestFolio) != s.firstNightTotal {
		t.Fatal("nothing of the transfer is visible while it waits")
	}
	must(t, run.Commit(ctx))
	select {
	case err := <-done:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the transfer never returned")
	}
	s.nightsAreChargedOnce(t)
	if s.sum(t, `folio_id = $1`, s.companyFolio) != s.firstNightTotal {
		t.Fatalf("company folio %s", s.sum(t, `folio_id = $1`, s.companyFolio))
	}
}

// The posting runs of one routed night at once (Architecture 18 extends TestConcurrentPostingChargesEachNightOnce with routing): the night is charged once, on the company folio.
func TestConcurrentRunsOfARoutedNightChargeItOnce(t *testing.T) {
	f := setup(t)
	acme := f.company(t, "ACME")
	st, _ := f.stayBilledTo(t, f.r101, folios.InstructionInput{Scope: folios.ScopeRoom, CompanyID: acme})
	cf := f.companyFolioID(t, st.Stay.ID, acme)
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if c := appCodeOf(t, "run", err); c != "" && c != "RESOURCE_BUSY" {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if got := f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE'`, cf); got != 1 {
		t.Fatalf("%d charges on the company folio", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM folio_items WHERE folio_id = $1`, st.Folio.ID); got != 0 {
		t.Fatalf("%d items on the guest folio", got)
	}
	if got := f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE stay_id = $1 AND status = 'POSTED'`, st.Stay.ID); got != 1 {
		t.Fatalf("%d register rows", got)
	}
}

// The deadlock that the sweeps above found now and then (SQLSTATE 40P01, shown as RESOURCE_BUSY): a transfer, like a payment or any posting to a folio of a stay, holds the FOLIO and then key-shares the
// STAY, because the item it inserts points at it (folio_items.stay_id). A posting run holds the STAY and then waits for the folio. Under FOR UPDATE on the stay the key share waits for the run and
// the run waits for the folio: a cycle. The run takes the stay with FOR NO KEY UPDATE (db.ForNoKeyUpdate), which still keeps another run, a check-out and a room move away but lets the key share through.
// This holds the cycle open on purpose, so it fails every time with the old lock and never by luck.
func TestThePostingRunLetsAFolioWriterKeyShareTheStay(t *testing.T) {
	s := aNightOnTheGuestFolio(t)
	s.nextDay(t)
	today := s.currentBD(t)
	ctx := context.Background()
	tx, err := s.Pool.Begin(ctx)
	must(t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	// what a transfer or a payment has done by now: it holds the folio that tonight's night is charged to (the company folio)
	if _, err := tx.Exec(ctx, `SELECT id FROM folios WHERE id = $1 FOR UPDATE`, s.companyFolio); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Charges.PostManual(s.admin, s.propID, today, nil)
		done <- err
	}()
	if !stillWaiting(done) { // the run has the stay and waits for the folio
		t.Fatal("the posting run must wait for the folio that is held")
	}
	// and now the foreign key of the item it inserts: it must not wait for the run, which waits for this transaction
	if _, err := tx.Exec(ctx, `SET LOCAL lock_timeout = '5s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM stays WHERE id = $1 FOR KEY SHARE`, s.stayID); err != nil {
		t.Fatalf("the key share of the stay waited for the posting run (a lock cycle): %v", err)
	}
	must(t, tx.Commit(ctx))
	must(t, <-done)
	s.nightsAreChargedOnce(t)
}
