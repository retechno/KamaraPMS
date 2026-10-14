package accounting_test

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/tenancy"
)

// The release of deposits (docs/architecture/31-deposit-release.md): the books of a property that releases a deposit against the charges it pays every day (EVERY_DAY, the default of a new
// property) and of one that releases it when the folio closes (ON_CLOSE, what the books did until then), through the same stories, with the reconciliation checked at every close.

var modes = []string{"EVERY_DAY", "ON_CLOSE"}

// books checks the balance of some accounts and that the control accounts agree with the folios as of the date.
func (h *hotel) books(t *testing.T, when, date string, want map[string]string) {
	t.Helper()
	for code, w := range want {
		if got := h.balance(t, code); !got.Equal(dec(w)) {
			t.Fatalf("%s: account %s is %s, want %s", when, code, got, w)
		}
	}
	if got := h.balance(t, "1215"); !got.IsZero() {
		t.Fatalf("%s: the account of the transfers (1215) is %s", when, got)
	}
	h.requireBalanced(t)
	asOf := civil.MustParseDate(date)
	rec, err := h.Accounting.Reconciliation(h.admin, h.propID, &asOf)
	must(t, err)
	if !rec.Reconciled {
		t.Fatalf("%s: the reconciliation must agree: %+v", when, rec)
	}
}

func (h *hotel) closeFolioOf(t *testing.T, folio int64) {
	t.Helper()
	info, err := h.Folios.GetFolio(h.admin, h.propID, folio)
	must(t, err)
	_, err = h.Folios.CloseFolio(h.admin, h.propID, folio, info.Version)
	must(t, err)
}

// A guest in house with a deposit of 300,000, three nights of 1,000,000, the rest paid, the check-out.
func TestAGuestInHouseWithADepositThenTheCheckOut(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			h := setupHotelMode(t, mode)
			res := h.book(t, "2026-09-30", "2026-10-03")
			_, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
			must(t, err)
			h.Clock.Set(roomstest.T0.Add(time.Hour))
			st := h.checkIn(t, res, h.r101)
			// EVERY_DAY applies the deposit to the first night at once (2310 empties, the guest owes the rest); ON_CLOSE holds it whole until the folio closes
			every := mode == "EVERY_DAY"
			held, owed := func(n int64) string { return map[bool]string{true: "0", false: "-300000"}[every] }, func(nights int64) string {
				v := nights * 1000000
				if every {
					v -= 300000
				}
				return itoa(v)
			}
			for night := int64(1); night <= 3; night++ {
				h.closeDay(t)
				date := []string{"2026-09-30", "2026-10-01", "2026-10-02"}[night-1]
				h.books(t, mode+" night "+itoa(night), date, map[string]string{"2310": held(night), "1210": owed(night), "4110": itoa(-night * 1000000), "1110": "300000"})
			}
			// the rest is paid and the guest leaves: the folio closes and nothing of the deposit is left in either account
			bal := h.folioBalance(t, st.Folio.ID)
			if !bal.Equal(dec("2700000")) {
				t.Fatalf("the guest owes 3,000,000 less the deposit: %s", bal)
			}
			_, err = h.Folios.PostPayment(h.admin, h.propID, st.Folio.ID, "rest", folios.PaymentInput{Amount: bal.String(), PaymentMethod: "CARD"})
			must(t, err)
			cur, err := h.Front.GetStay(h.admin, h.propID, st.Stay.ID)
			must(t, err)
			_, err = h.Front.CheckOut(h.admin, h.propID, st.Stay.ID, frontdesk.CheckOutInput{Version: cur.Stay.Version})
			must(t, err)
			h.closeDay(t)
			h.books(t, mode+" after the check-out", "2026-10-03", map[string]string{"2310": "0", "1210": "0", "1110": "300000", "1150": "2700000", "4110": "-3000000"})
		})
	}
}

// A reservation that was cancelled, whose deposit of 300,000 is kept and which is charged the fee of its cancellation, 200,000 (the case of the review of PR 6).
func TestACancelledReservationWithAKeptDepositAndAFee(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			h := setupHotelMode(t, mode)
			res := h.book(t, "2026-09-30", "2026-10-01")
			dep, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
			must(t, err)
			_, err = h.Res.Cancel(h.admin, h.propID, res.ID, res.Version, "the guest cancelled")
			must(t, err)
			_, err = h.Folios.PostReservationFee(h.admin, h.propID, res.ID, folios.FeeInput{Type: "CANCEL_FEE", Amount: "200000", Reason: "the fee"})
			must(t, err)
			h.closeDay(t)
			if mode == "EVERY_DAY" { // the deposit has paid the fee: 100,000 is still held, and the guest owes nothing
				h.books(t, mode+" day of the fee", "2026-09-30", map[string]string{"2310": "-100000", "1210": "0", "4510": "-200000", "1110": "300000"})
			} else { // the deposit is whole and the fee is owed, until the folio closes
				h.books(t, mode+" day of the fee", "2026-09-30", map[string]string{"2310": "-300000", "1210": "200000", "4510": "-200000", "1110": "300000"})
			}
			// the rest is refunded (no more than the folio holds) and the folio closes
			folio := stayFolio2(t, h, res.ID)
			_, err = h.Folios.Refund(h.admin, h.propID, dep.Payment.ID, "rf", folios.RefundInput{Amount: "100000", Reason: "the rest", Approval: h.approval()})
			must(t, err)
			h.closeFolioOf(t, folio)
			h.closeDay(t)
			h.books(t, mode+" after the refund and the close", "2026-10-01", map[string]string{"2310": "0", "1210": "0", "4510": "-200000", "1110": "200000"})
		})
	}
}

// A deposit smaller than the fee: it pays what it can, the guest owes the rest.
func TestADepositSmallerThanTheFee(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			h := setupHotelMode(t, mode)
			res := h.book(t, "2026-09-30", "2026-10-01")
			_, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
			must(t, err)
			_, err = h.Res.Cancel(h.admin, h.propID, res.ID, res.Version, "the guest cancelled")
			must(t, err)
			_, err = h.Folios.PostReservationFee(h.admin, h.propID, res.ID, folios.FeeInput{Type: "CANCEL_FEE", Amount: "200000", Reason: "the fee"})
			must(t, err)
			h.closeDay(t)
			if mode == "EVERY_DAY" {
				h.books(t, mode+" day of the fee", "2026-09-30", map[string]string{"2310": "0", "1210": "100000", "4510": "-200000"})
			} else {
				h.books(t, mode+" day of the fee", "2026-09-30", map[string]string{"2310": "-100000", "1210": "200000", "4510": "-200000"})
			}
			folio := stayFolio2(t, h, res.ID)
			_, err = h.Folios.PostPayment(h.admin, h.propID, folio, "rest", folios.PaymentInput{Amount: "100000", PaymentMethod: "CARD"})
			must(t, err)
			h.closeFolioOf(t, folio)
			h.closeDay(t)
			h.books(t, mode+" paid and closed", "2026-10-01", map[string]string{"2310": "0", "1210": "0", "4510": "-200000", "1110": "100000", "1150": "100000"})
		})
	}
}

// The charge a deposit paid moves to the folio of a guest in house the next day: in EVERY_DAY the deposit is a liability again (a negative amount applied), and the control accounts agree on
// both days.
func TestAnAppliedDepositIsGivenBackWhenItsChargeMovesAway(t *testing.T) {
	h := setupHotelMode(t, "EVERY_DAY")
	one := h.book(t, "2026-09-30", "2026-10-01")
	two := h.book(t, "2026-09-30", "2026-10-02")
	_, err := h.Folios.Deposit(h.admin, h.propID, one.ID, "dep", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = h.Res.Cancel(h.admin, h.propID, one.ID, one.Version, "off")
	must(t, err)
	fa := stayFolio2(t, h, one.ID)
	h.charge(t, fa, "MINIBAR", "100000", "mb")
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	guest := h.checkIn(t, two, h.r101)
	h.closeDay(t)
	charged := h.folioBalance(t, fa).Add(dec("300000")) // the minibar with its service charge and tax, before the deposit
	if !charged.IsPositive() || !h.balance(t, "2310").Equal(dec("-300000").Add(charged)) {
		t.Fatalf("day 1: the deposit pays the minibar (%s) and 2310 holds the rest: %s", charged, h.balance(t, "2310"))
	}
	h.books(t, "day 1", "2026-09-30", map[string]string{})
	var item int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM folio_items WHERE folio_id = $1 AND transaction_type = 'CHARGE'`, fa).Scan(&item))
	_, err = h.Folios.TransferItems(h.admin, h.propID, fa, folios.TransferItemsInput{FolioID: guest.Folio.ID, ItemIDs: []int64{item}, Reason: "the guest pays it", Approval: h.approval()})
	must(t, err)
	h.closeDay(t)
	// the charge is on the folio of the guest: nothing is left for the deposit of the cancelled reservation to pay, so it is a liability again, whole
	if got := h.balance(t, "2310"); !got.Equal(dec("-300000")) {
		t.Fatalf("day 2: the deposit is a liability again, 300,000: %s", got)
	}
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines WHERE source_type = 'DEPOSIT_APPLIED' AND source_ref = (SELECT folio_number FROM folios WHERE id = $1)`, fa); n != 4 {
		t.Fatalf("the deposit was applied on day 1 and given back on day 2: two pairs of lines, %d lines", n)
	}
	if net := h.Count(t, `SELECT COALESCE(sum(l.debit - l.credit), 0)::int FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id WHERE a.code = '2310' AND l.source_type = 'DEPOSIT_APPLIED' AND l.source_ref = (SELECT folio_number FROM folios WHERE id = $1)`, fa); net != 0 {
		t.Fatalf("what was applied and given back nets to zero: %d", net)
	}
	h.books(t, "day 2", "2026-10-01", map[string]string{})
}

// The master folio: a deposit before the first charge pays the nights that are charged to it.
func TestTheDepositOfAMasterFolioPaysItsNights(t *testing.T) {
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			h := setupHotelMode(t, mode)
			group, _ := h.groupOfMembers(t, "2026-09-30", "2026-10-02", 2)
			h.route(t, group)
			master := h.masterOf(t, group)
			_, err := h.Folios.PostPayment(h.admin, h.propID, master, "dep", folios.PaymentInput{Amount: "500000", PaymentMethod: "CARD"})
			must(t, err)
			h.closeDay(t) // two nights of 1,000,000 are charged to the master
			if mode == "EVERY_DAY" {
				h.books(t, mode+" day 1", "2026-09-30", map[string]string{"2310": "0", "1210": "1500000"})
			} else {
				h.books(t, mode+" day 1", "2026-09-30", map[string]string{"2310": "-500000", "1210": "2000000"})
			}
		})
	}
}

// A new property releases every day; the choice is free until the first transaction and locked after it, by the database, with the date of the first item.
func TestTheReleaseOfDepositsIsLockedByTheFirstTransaction(t *testing.T) {
	e := newHotel(t)
	var mode string
	must(t, e.Pool.QueryRow(context.Background(), `SELECT deposit_release_mode FROM properties WHERE id = $1`, e.propID).Scan(&mode))
	if mode != "EVERY_DAY" {
		t.Fatalf("a new property releases every day: %s", mode)
	}
	must(t, e.Exec(t, `UPDATE properties SET deposit_release_mode = 'ON_CLOSE' WHERE id = $1`, e.propID)) // free before the first transaction
	must(t, e.Exec(t, `UPDATE properties SET deposit_release_mode = 'EVERY_DAY' WHERE id = $1`, e.propID))
	res := e.book(t, "2026-09-30", "2026-10-01")
	_, err := e.Folios.Deposit(e.admin, e.propID, res.ID, "dep", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	var lockedOn *civil.Date
	must(t, e.Pool.QueryRow(context.Background(), `SELECT deposit_release_locked_on FROM properties WHERE id = $1`, e.propID).Scan(&lockedOn))
	if lockedOn == nil || lockedOn.String() != "2026-09-30" {
		t.Fatalf("the date of the first item: %v", lockedOn)
	}
	err = e.Exec(t, `UPDATE properties SET deposit_release_mode = 'ON_CLOSE' WHERE id = $1`, e.propID)
	if err == nil || !strings.Contains(err.Error(), "is locked since") {
		t.Fatalf("the database refuses a change after the lock: %v", err)
	}
	// after the lock a posting does not touch the row of the property: its version (xmin) is the same after many postings
	xmin := func() string {
		var x string
		must(t, e.Pool.QueryRow(context.Background(), `SELECT xmin::text FROM properties WHERE id = $1`, e.propID).Scan(&x))
		return x
	}
	before := xmin()
	for i := 0; i < 20; i++ {
		_, err := e.Folios.Deposit(e.admin, e.propID, res.ID, "more"+itoa(int64(i)), folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
		must(t, err)
	}
	if after := xmin(); after != before {
		t.Fatalf("a posting after the lock wrote the row of the property: xmin %s then %s", before, after)
	}
	// the first day close writes the entry of the lock, once
	_, err = e.Res.Cancel(e.admin, e.propID, res.ID, res.Version, "off") // no arrival is left for the night audit
	must(t, err)
	e.closeDay(t)
	e.closeDay(t)
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.deposit_release_locked' AND property_id = $1`, e.propID); n != 1 {
		t.Fatalf("%d entries of the lock, want 1", n)
	}
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.deposit_release_locked' AND new_data->>'locked_on' = '2026-09-30' AND new_data->>'mode' = 'EVERY_DAY'`); n != 1 {
		t.Fatal("the entry says the date and the mode")
	}
}

// The setting is chosen through the settings of the property: audited while it is free, and refused with its own code once the property has a transaction (the same value is not a change).
func TestTheModeIsChosenThroughThePropertyUntilTheLock(t *testing.T) {
	h := newHotel(t)
	set := func(mode string) error {
		_, err := h.Tenancy.UpdateProperty(h.admin, h.propID, tenancy.PropertyPatch{DepositReleaseMode: &mode})
		return err
	}
	must(t, set("ON_CLOSE"))
	must(t, set("ON_CLOSE"))
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'property.deposit_release_set' AND old_data->>'deposit_release_mode' = 'EVERY_DAY' AND new_data->>'deposit_release_mode' = 'ON_CLOSE'`); n != 1 {
		t.Fatalf("one entry for the one change (%d)", n)
	}
	wantCode(t, set("SOMETIMES"), "VALIDATION_FAILED")
	res := h.book(t, "2026-09-30", "2026-10-01")
	_, err := h.Folios.Deposit(h.admin, h.propID, res.ID, "dep", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	err = set("EVERY_DAY")
	wantCode(t, err, "DEPOSIT_RELEASE_LOCKED")
	must(t, set("ON_CLOSE")) // the same value is not a change
	got, err := h.Tenancy.GetProperty(h.admin, h.propID)
	must(t, err)
	if got.DepositReleaseMode != "ON_CLOSE" || got.DepositReleaseLockedOn == nil || got.DepositReleaseLockedOn.String() != "2026-09-30" {
		t.Fatalf("the property says its mode and since when it is locked: %+v %v", got.DepositReleaseMode, got.DepositReleaseLockedOn)
	}
}

// Two first transactions at the same moment on a property that is not locked: neither fails, the date is the earlier business date, and there is one entry of the lock.
func TestTwoFirstPostingsAtOnceLockTheReleaseOnce(t *testing.T) {
	h := setupHotelMode(t, "EVERY_DAY")
	a := h.book(t, "2026-09-30", "2026-10-01")
	b := h.book(t, "2026-09-30", "2026-10-01")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, r := range []reservations.Reservation{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = h.Folios.Deposit(h.admin, h.propID, r.ID, "dep"+itoa(int64(i)), folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("both postings go through: %v %v", errs[0], errs[1])
	}
	var lockedOn *civil.Date
	must(t, h.Pool.QueryRow(context.Background(), `SELECT deposit_release_locked_on FROM properties WHERE id = $1`, h.propID).Scan(&lockedOn))
	if lockedOn == nil || lockedOn.String() != "2026-09-30" {
		t.Fatalf("the date of the lock: %v", lockedOn)
	}
	for _, r := range []reservations.Reservation{a, b} { // no arrival is left for the night audit
		_, err := h.Res.Cancel(h.admin, h.propID, r.ID, r.Version, "off")
		must(t, err)
	}
	h.closeDay(t)
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.deposit_release_locked' AND property_id = $1`, h.propID); n != 1 {
		t.Fatalf("%d entries of the lock, want 1", n)
	}
}

// The migration moves a property that exists to EVERY_DAY: the first day close from there carries the deposits that were held whole until then, says so, counts the folios in the audit, and
// the days that were posted do not change.
func TestTheFirstCloseAfterTheMigrationCarriesTheAdjustment(t *testing.T) {
	h := setupHotelMode(t, "ON_CLOSE")
	h.Room(t, h.admin, h.propID, h.typ.ID, "103", housekeeping.Clean) // a room for the third reservation
	r1, r2 := h.book(t, "2026-09-30", "2026-10-03"), h.book(t, "2026-09-30", "2026-10-03")
	r3 := h.book(t, "2026-09-30", "2026-10-01")
	for i, r := range []reservations.Reservation{r1, r2} {
		_, err := h.Folios.Deposit(h.admin, h.propID, r.ID, "dep"+itoa(int64(i)), folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
		must(t, err)
	}
	// a third reservation whose deposit was released by the close of its folio before the migration
	_, err := h.Folios.Deposit(h.admin, h.propID, r3.ID, "dep3", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = h.Res.Cancel(h.admin, h.propID, r3.ID, r3.Version, "off")
	must(t, err)
	h.Clock.Set(roomstest.T0.Add(time.Hour))
	h.checkIn(t, r1, h.r101)
	h.checkIn(t, r2, h.r102)
	cancelled := stayFolio2(t, h, r3.ID)
	_, err = h.Folios.Refund(h.admin, h.propID, firstPaymentOf(t, h, cancelled), "rf3", folios.RefundInput{Amount: "100000", Reason: "off", Approval: h.approval()})
	must(t, err)
	h.closeFolioOf(t, cancelled)
	h.closeDay(t) // 30 Sep, on the books of ON_CLOSE: the two deposits are whole in 2310, the third was released at its close
	h.books(t, "before the migration", "2026-09-30", map[string]string{"2310": "-600000"})
	day1 := h.Count(t, `SELECT count(*) FROM gl_journal_lines`)

	// what migration 00078 does to a property that has transactions (the triggers are off for the statement, as the migration creates them after)
	must(t, h.Exec(t, `ALTER TABLE properties DISABLE TRIGGER USER`))
	must(t, h.Exec(t, `UPDATE properties SET deposit_release_mode = 'EVERY_DAY', deposit_release_adjust_pending = true WHERE id = $1`, h.propID))
	must(t, h.Exec(t, `ALTER TABLE properties ENABLE TRIGGER USER`))
	h.closeDay(t) // 1 Oct: the first close from there
	if got := h.Count(t, `SELECT count(*) FROM gl_journal_lines l JOIN gl_journals j ON j.id = l.journal_id WHERE j.journal_date = '2026-09-30'`); got != day1 {
		t.Fatalf("the days that were posted do not change: %d lines, %d before", got, day1)
	}
	// each of the two folios in house is carried: two nights of 1,000,000 and a deposit of 300,000 (all of it applied), which says "includes the adjustment"
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines WHERE source_type = 'DEPOSIT_APPLIED' AND description LIKE '%includes the adjustment%'`); n != 4 {
		t.Fatalf("%d lines say they include the adjustment, want 4 (two folios, two accounts)", n)
	}
	if n := h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.day_posted' AND (new_data->>'deposit_applied_adjusted_folios')::int = 2`); n != 1 {
		t.Fatal("the audit entry of that close counts the two folios")
	}
	if got := h.balance(t, "2310"); !got.IsZero() {
		t.Fatalf("every deposit of the folios in house is applied to their nights: 2310 %s", got)
	}
	h.books(t, "the first close from the migration", "2026-10-01", map[string]string{})
	h.closeDay(t) // the next one carries nothing more
	if n := h.Count(t, `SELECT count(*) FROM gl_journal_lines WHERE source_type = 'DEPOSIT_APPLIED' AND description LIKE '%includes the adjustment%'`); n != 4 {
		t.Fatalf("%d adjusted lines after the next close, want still 4", n)
	}
	h.books(t, "the next close", "2026-10-02", map[string]string{"2310": "0"})
}

func firstPaymentOf(t *testing.T, h *hotel, folio int64) int64 {
	t.Helper()
	var id int64
	must(t, h.Pool.QueryRow(context.Background(), `SELECT id FROM payments WHERE folio_id = $1 AND payment_type = 'PAYMENT' ORDER BY id LIMIT 1`, folio).Scan(&id))
	return id
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
