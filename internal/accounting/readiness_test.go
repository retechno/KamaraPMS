package accounting_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"kamarapms/internal/accounting"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// runDay tries the night audit of the current business date, at 00:30 local of the next day, and returns its error.
func (h *hotel) runDay(t *testing.T) error {
	t.Helper()
	day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
	must(t, err)
	h.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = h.Audit.Run(h.admin, h.propID, day.BusinessDate)
	return err
}

func codesOf(list []accounting.ReadinessBlocker) []string {
	out := []string{}
	for _, b := range list {
		out = append(out, b.Code+":"+b.Ref)
	}
	return out
}

// sideEffects is what a night audit that did run would have changed: the closed days, the journals, the day posts and the ledger rows of the night.
func (h *hotel) sideEffects(t *testing.T) [4]int {
	t.Helper()
	return [4]int{
		h.Count(t, `SELECT count(*) FROM business_days WHERE property_id = $1 AND status = 'CLOSED'`, h.propID),
		h.Count(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, h.propID),
		h.Count(t, `SELECT count(*) FROM gl_day_posts WHERE property_id = $1`, h.propID),
		h.Count(t, `SELECT count(*) FROM folio_items WHERE property_id = $1`, h.propID),
	}
}

func TestAFullyConfiguredPropertyIsReadyAndTheNightAuditRuns(t *testing.T) {
	h := setupHotel(t)
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
	rep, err := h.Accounting.Readiness(h.admin, h.propID)
	must(t, err)
	if !rep.Ready || rep.Status != "READY" || len(rep.Blockers) != 0 {
		t.Fatalf("a property created the normal way is ready: %+v", rep)
	}
	pre, err := h.Audit.Preview(h.admin, h.propID)
	must(t, err)
	if len(pre.Blockers.AccountingReadiness) != 0 {
		t.Fatalf("the preview carries no accounting blocker: %+v", pre.Blockers.AccountingReadiness)
	}
	h.closeDay(t)
	if n := h.Count(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, h.propID); n != 1 {
		t.Fatalf("the day closed with %d journals", n)
	}
	h.requireBalanced(t)
}

func TestTheNightAuditRefusesToCloseADayItCannotJournal(t *testing.T) {
	cases := []struct {
		name   string
		break_ string // SQL that breaks one thing
		want   string // code:ref of the blocker
	}{
		{"accounting was never set up", `DELETE FROM accounting_settings WHERE property_id = $1`, "ACCOUNTING_NOT_SET_UP:"},
		{"the accounting start date is after the business date", `UPDATE accounting_settings SET start_date = '2026-10-05' WHERE property_id = $1`, "ACCOUNTING_NOT_STARTED:2026-10-05"},
		{"the cash account is not mapped", `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key = 'CASH'`, "ACCOUNT_MAP_MISSING:CASH"},
		{"the guest ledger is not mapped", `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key = 'GUEST_LEDGER'`, "ACCOUNT_MAP_MISSING:GUEST_LEDGER"},
		{"the suspense account is not mapped", `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key = 'SUSPENSE'`, "ACCOUNT_MAP_MISSING:SUSPENSE"},
		{"the cash account was switched off", `UPDATE gl_accounts SET is_active = false WHERE property_id = $1 AND code = '1110'`, "ACCOUNT_MAP_UNUSABLE:CASH"},
		{"the card account points at a revenue account", `UPDATE gl_account_map SET account_id = (SELECT id FROM gl_accounts WHERE property_id = $1 AND code = '4110') WHERE property_id = $1 AND map_key = 'CARD'`, "ACCOUNT_MAP_UNUSABLE:CARD"},
		{"the room charge code has no account", `UPDATE charge_codes SET gl_account_code = NULL WHERE property_id = $1 AND code = 'ROOM'`, "ROOM_CHARGE_CODE_UNMAPPED:ROOM"},
		{"the room charge code points at an expense account", `UPDATE charge_codes SET gl_account_code = '7110' WHERE property_id = $1 AND code = 'ROOM'`, "ROOM_CHARGE_CODE_UNMAPPED:ROOM"},
		{"the journals cannot be numbered", `DELETE FROM document_sequences WHERE property_id = $1 AND sequence_type = 'JOURNAL'`, "JOURNAL_SEQUENCE_MISSING:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := setupHotel(t)
			st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
			h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1")
			must(t, h.Exec(t, c.break_, h.propID))
			before := h.sideEffects(t)

			rep, err := h.Accounting.Readiness(h.admin, h.propID)
			must(t, err)
			if rep.Ready || rep.Status != "NOT_READY" || !slices.Contains(codesOf(rep.Blockers), c.want) {
				t.Fatalf("want blocker %s, got ready=%v %v", c.want, rep.Ready, codesOf(rep.Blockers))
			}
			pre, err := h.Audit.Preview(h.admin, h.propID)
			must(t, err)
			if pre.CanRun || !slices.Contains(codesOf(pre.Blockers.AccountingReadiness), c.want) {
				t.Fatalf("the preview must say so: can_run=%v %v", pre.CanRun, codesOf(pre.Blockers.AccountingReadiness))
			}

			err = h.runDay(t)
			var ae *apperr.Error
			if !errors.As(err, &ae) || ae.Code != "NIGHT_AUDIT_BLOCKED" {
				t.Fatalf("the night audit must be blocked, got %v", err)
			}
			blockers, ok := ae.Context["blockers"].(nightaudit.Blockers)
			if !ok || !slices.Contains(codesOf(blockers.AccountingReadiness), c.want) {
				t.Fatalf("the error carries the structured blocker %s: %+v", c.want, ae.Context)
			}
			if after := h.sideEffects(t); after != before {
				t.Fatalf("a blocked night audit changed something: before %v after %v", before, after)
			}
			day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
			must(t, err)
			if day.BusinessDate.String() != "2026-09-30" || day.Status != "OPEN" {
				t.Fatalf("the business day must stay open: %+v", day)
			}
		})
	}
}

func TestSeveralBlockersAreAllReported(t *testing.T) {
	h := setupHotel(t)
	must(t, h.Exec(t, `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key IN ('CASH', 'CARD', 'CITY_LEDGER')`, h.propID))
	must(t, h.Exec(t, `UPDATE charge_codes SET gl_account_code = NULL WHERE property_id = $1 AND code = 'ROOM'`, h.propID))
	rep, err := h.Accounting.Readiness(h.admin, h.propID)
	must(t, err)
	got := codesOf(rep.Blockers)
	for _, want := range []string{"ACCOUNT_MAP_MISSING:CASH", "ACCOUNT_MAP_MISSING:CARD", "ACCOUNT_MAP_MISSING:CITY_LEDGER", "ROOM_CHARGE_CODE_UNMAPPED:ROOM"} {
		if !slices.Contains(got, want) {
			t.Fatalf("missing %s in %v", want, got)
		}
	}
	if len(got) != 4 {
		t.Fatalf("exactly the four things that are wrong: %v", got)
	}
	if err := h.runDay(t); err == nil {
		t.Fatal("a partial mapping must block the night audit")
	}
}

// What the night audit does not need must not stop it, and what the engine places by design (a tax or another charge code with no account of its own) is a warning.
func TestWhatTheDayCloseDoesNotNeedIsAWarningNotABlocker(t *testing.T) {
	h := setupHotel(t)
	must(t, h.Exec(t, `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key IN ('INPUT_VAT', 'CASH_OVER_SHORT')`, h.propID))
	must(t, h.Exec(t, `UPDATE charge_codes SET gl_account_code = NULL WHERE property_id = $1 AND code = 'RESTAURANT'`, h.propID))
	st := h.checkIn(t, h.book(t, "2026-09-30", "2026-10-05"), h.r101)
	h.charge(t, st.Folio.ID, "RESTAURANT", "100000", "r1") // posted after the code lost its account: the item carries none
	rep, err := h.Accounting.Readiness(h.admin, h.propID)
	must(t, err)
	if !rep.Ready {
		t.Fatalf("not a blocker: %v", codesOf(rep.Blockers))
	}
	w := codesOf(rep.Warnings)
	for _, want := range []string{"CODE_UNMAPPED:RESTAURANT", "ACCOUNT_MAP_OPTIONAL:INPUT_VAT", "ACCOUNT_MAP_OPTIONAL:CASH_OVER_SHORT"} {
		if !slices.Contains(w, want) {
			t.Fatalf("warning %s missing in %v", want, w)
		}
	}
	h.closeDay(t) // and the day still closes, the restaurant revenue goes to the suspense account as before
	if got := h.balance(t, "2990"); got.IsZero() {
		t.Fatal("the unmapped restaurant revenue is in the suspense account")
	}
}

func TestReadinessNeedsThePermissionAndIsScopedToTheProperty(t *testing.T) {
	h := setupHotel(t)
	nobody := h.User(t, h.tenantID, h.propID, auth.PermNightAuditRun)
	_, err := h.Accounting.Readiness(nobody, h.propID)
	wantCode(t, err, "PERMISSION_DENIED")
	other := h.Property(t, h.tenantID, "JKT")
	rep, err := h.Accounting.Readiness(h.admin, other.ID)
	must(t, err)
	if !rep.Ready {
		t.Fatalf("a second property is judged on its own setup: %v", codesOf(rep.Blockers))
	}
	must(t, h.Exec(t, `DELETE FROM accounting_settings WHERE property_id = $1`, other.ID))
	if rep, err = h.Accounting.Readiness(h.admin, h.propID); err != nil || !rep.Ready {
		t.Fatalf("the first property is not affected by the second: %+v %v", rep, err)
	}
}

// The check of the night audit holds the accounting settings row FOR SHARE until the transaction ends, so a change of the account map (FOR UPDATE) waits for the night audit and
// the journal is made with the setup that was checked.
func TestTheReadinessCheckOfTheNightAuditLocksTheSetup(t *testing.T) {
	h := setupHotel(t)
	p, err := auth.Require(h.admin)
	must(t, err)
	day, err := h.Tenancy.CurrentBusinessDay(h.admin, h.propID)
	must(t, err)
	rev := h.byCode(t, "1110")
	held := make(chan struct{})
	release := make(chan struct{})
	txDone := make(chan error, 1)
	go func() {
		txDone <- h.TxM.WithinTx(h.admin, func(ctx context.Context) error {
			rep, err := h.Accounting.JournalReadiness(ctx, p, h.propID, day.BusinessDate, true)
			if err != nil || !rep.Ready {
				return errors.Join(err, errors.New("not ready"))
			}
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	changed := make(chan error, 1)
	go func() {
		_, err := h.Accounting.SetAccountMap(h.admin, h.propID, []accounting.MapInput{{Key: "CASH", AccountID: rev.ID}})
		changed <- err
	}()
	select {
	case err := <-changed:
		t.Fatalf("the account map changed while the night audit held the setup: %v", err)
	case <-time.After(700 * time.Millisecond):
	}
	close(release)
	must(t, <-txDone)
	select {
	case err := <-changed:
		must(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the change never went through after the night audit ended")
	}
}
