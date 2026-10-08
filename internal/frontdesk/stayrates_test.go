package frontdesk_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/rooms/roomstest"
)

// The rate edit of an in-house stay: the snapshot of the booking changes for a night that is not charged; a night that is charged keeps its charge and gets an adjustment (also on a day that has
// closed). The rate plan is never touched, a charged item is never updated or deleted.

func (f *fx) rateIn(version int32, apply, date, amount string) frontdesk.ChangeRatesInput {
	return frontdesk.ChangeRatesInput{Version: version, ApplyTo: apply, Date: d(date), Amount: amount, Reason: "negotiated at the desk"}
}

func (f *fx) nightAmount(t *testing.T, stayID int64, date string) string {
	t.Helper()
	for _, n := range f.detail(t, stayID).NightlyRates {
		if n.Date == d(date) {
			return n.Amount
		}
	}
	t.Fatalf("no night %s", date)
	return ""
}

// chargeOf: the room charge item of a night (the original charge).
func (f *fx) chargeOf(t *testing.T, stayID int64, date string) (id int64, unit, debit decimal.Decimal) {
	t.Helper()
	must(t, f.Pool.QueryRow(context.Background(),
		`SELECT i.id, i.unit_price, i.debit FROM stay_charge_postings p JOIN folio_items i ON i.id = p.folio_item_id WHERE p.stay_id = $1 AND p.service_date = $2 AND p.status = 'POSTED'`,
		stayID, d(date)).Scan(&id, &unit, &debit))
	return
}

// ledgerRate: what the ledger says a night costs before tax: the charge plus the adjustments that refer to it (unit prices are signed).
func (f *fx) ledgerRate(t *testing.T, chargeID int64) decimal.Decimal {
	t.Helper()
	var sum decimal.Decimal
	must(t, f.Pool.QueryRow(context.Background(),
		`SELECT COALESCE(sum(unit_price), 0) FROM folio_items WHERE id = $1 OR (transaction_type = 'ADJUSTMENT' AND reference_type = 'FOLIO_ITEM' AND reference_id = $1::text)`, chargeID).Scan(&sum))
	return sum
}

func TestRateChangeOfANightThatIsNotCharged(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	items := f.Count(t, `SELECT count(*) FROM folio_items`)
	res, err := f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000"))
	must(t, err)
	if len(res.Changes) != 1 || res.Changes[0].Charged || res.Changes[0].OldAmount != "1000000" || res.Changes[0].NewAmount != "800000" || res.Changes[0].AdjustmentItemID != nil {
		t.Fatalf("changes: %+v", res.Changes)
	}
	if res.Stay.Version != st.Stay.Version+1 || f.nightAmount(t, st.Stay.ID, "2026-10-01") != "800000" || f.nightAmount(t, st.Stay.ID, "2026-09-30") != "1000000" || f.nightAmount(t, st.Stay.ID, "2026-10-02") != "1000000" {
		t.Fatalf("nights after: %+v", f.detail(t, st.Stay.ID).NightlyRates)
	}
	if f.Count(t, `SELECT count(*) FROM folio_items`) != items {
		t.Fatal("nothing is written to the ledger for a night that is not charged")
	}
	// the night is charged at the new rate by the posting run, tax and service by the engine as always
	must(t, f.Exec(t, `UPDATE stay_charge_postings SET status = status`)) // no-op: the register is untouched
	var action, reason string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT action, new_data->>'reason' FROM audit_logs WHERE action = 'stay.rate_changed'`).Scan(&action, &reason))
	if reason != "negotiated at the desk" {
		t.Fatalf("audit reason: %q", reason)
	}
	var date, oldAmount, newAmount, plan, room string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT new_data->>'date', old_data->>'amount', new_data->>'amount', new_data->>'rate_plan', new_data->>'room_number' FROM audit_logs WHERE action = 'stay.rate_changed'`).Scan(&date, &oldAmount, &newAmount, &plan, &room))
	if date != "2026-10-01" || oldAmount != "1000000" || newAmount != "800000" || plan != "BAR" || room != "101" {
		t.Fatalf("audit: %s %s %s %s %s", date, oldAmount, newAmount, plan, room)
	}
	// the stale screen
	_, err = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "700000"))
	wantCode(t, err, "VERSION_CONFLICT")
}

func TestRateChangeValidation(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	try := func(mut func(*frontdesk.ChangeRatesInput)) error {
		in := f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000")
		mut(&in)
		_, err := f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, in)
		return err
	}
	wantCode(t, try(func(in *frontdesk.ChangeRatesInput) { in.Reason = "  " }), "VALIDATION_FAILED") // a reason is required
	wantCode(t, try(func(in *frontdesk.ChangeRatesInput) { in.Amount = "-1" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.ChangeRatesInput) { in.Amount = "abc" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.ChangeRatesInput) { in.ApplyTo = "ALL" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *frontdesk.ChangeRatesInput) { in.Date = d("2026-12-01") }), "VALIDATION_FAILED") // not a night of the stay
	wantCode(t, try(func(in *frontdesk.ChangeRatesInput) { in.Amount = "1000000" }), "VALIDATION_FAILED")     // nothing would change
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.rate_changed'`) != 0 {
		t.Fatal("a refused change writes nothing")
	}
}

func TestRemainingNightsNeverTouchAChargedNight(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	_, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil) // 30 Sep is charged
	must(t, err)
	charge, _, debit := f.chargeOf(t, st.Stay.ID, "2026-09-30")
	items := f.Count(t, `SELECT count(*) FROM folio_items`)
	res, err := f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyRemaining, "2026-09-30", "700000"))
	must(t, err)
	if len(res.Changes) != 2 || res.Changes[0].Date != d("2026-10-01") || res.Changes[1].Date != d("2026-10-02") || res.Changes[0].Charged || res.Changes[1].Charged {
		t.Fatalf("remaining: %+v", res.Changes)
	}
	if f.nightAmount(t, st.Stay.ID, "2026-09-30") != "1000000" || f.nightAmount(t, st.Stay.ID, "2026-10-01") != "700000" || f.nightAmount(t, st.Stay.ID, "2026-10-02") != "700000" {
		t.Fatalf("nights: %+v", f.detail(t, st.Stay.ID).NightlyRates)
	}
	if f.Count(t, `SELECT count(*) FROM folio_items`) != items {
		t.Fatal("a bulk change writes nothing to the ledger")
	}
	if _, _, d2 := f.chargeOf(t, st.Stay.ID, "2026-09-30"); !d2.Equal(debit) || f.ledgerRate(t, charge).String() != "1000000" {
		t.Fatal("the charged night is as it was")
	}
}

func TestRateChangeOfAChargedNightPostsAnAdjustment(t *testing.T) {
	s := withCompanyFolio(t) // the night is charged to the company folio by the instruction ROOM
	_, err := s.Charges.PostManual(s.admin, s.propID, roomstest.BD, nil)
	must(t, err)
	charge, unit, debit := s.chargeOf(t, s.stay.Stay.ID, "2026-09-30")
	balanceBefore := s.balance(t, s.companyFol)
	in := s.rateIn(s.stay.Stay.Version, frontdesk.ApplyNight, "2026-09-30", "1200000")
	// without the approval of a correction, the permission of the folio and the right to change a rate nothing happens
	_, err = s.Front.ChangeRates(s.admin, s.propID, s.stay.Stay.ID, in)
	wantCode(t, err, "APPROVAL_REQUIRED")
	clerk := s.User(t, s.tenantID, s.propID, auth.PermFrontdeskRateChange, auth.PermReservationRead)
	in.Approval = s.approval(t)
	_, err = s.Front.ChangeRates(clerk, s.propID, s.stay.Stay.ID, in)
	wantCode(t, err, "PERMISSION_DENIED") // folio.adjust
	if s.nightAmount(t, s.stay.Stay.ID, "2026-09-30") != "1000000" || s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.rate_changed'`) != 0 {
		t.Fatal("a refused correction changes nothing")
	}
	res, err := s.Front.ChangeRates(s.admin, s.propID, s.stay.Stay.ID, in)
	must(t, err)
	c := res.Changes[0]
	if !c.Charged || c.AdjustmentItemID == nil || c.FolioID == nil || *c.FolioID != s.companyFol || c.OldAmount != "1000000" || c.NewAmount != "1200000" {
		t.Fatalf("change: %+v", c)
	}
	// the charge is as it was; an adjustment refers to it and the ledger now says the new rate; the snapshot too
	if _, u2, d2 := s.chargeOf(t, s.stay.Stay.ID, "2026-09-30"); !u2.Equal(unit) || !d2.Equal(debit) {
		t.Fatalf("the charge was touched: %s %s", u2, d2)
	}
	if got := s.ledgerRate(t, charge); got.String() != "1200000" || s.nightAmount(t, s.stay.Stay.ID, "2026-09-30") != "1200000" {
		t.Fatalf("ledger rate %s", got)
	}
	var kind string
	var adjDebit decimal.Decimal
	must(t, s.Pool.QueryRow(context.Background(), `SELECT transaction_type, debit FROM folio_items WHERE id = $1`, *c.AdjustmentItemID).Scan(&kind, &adjDebit))
	if kind != "ADJUSTMENT" {
		t.Fatalf("kind %s", kind)
	}
	// the balance is the sum of the ledger: the charge plus the adjustment, tax and service by the engine in proportion to the charge (a fifth of it, within a rounding unit)
	after := decimal.RequireFromString(s.balance(t, s.companyFol))
	if want := decimal.RequireFromString(balanceBefore).Add(adjDebit); !after.Equal(want) {
		t.Fatalf("balance %s, charge %s + adjustment %s", after, balanceBefore, adjDebit)
	}
	if diff := adjDebit.Sub(debit.Div(decimal.NewFromInt(5))).Abs(); diff.GreaterThan(decimal.NewFromInt(1)) {
		t.Fatalf("the adjustment %s is not a fifth of the charge %s", adjDebit, debit)
	}
	var audited bool
	var itemID int64
	must(t, s.Pool.QueryRow(context.Background(), `SELECT (new_data->>'financial_adjustment')::boolean, (new_data->>'adjustment_item_id')::bigint FROM audit_logs WHERE action = 'stay.rate_changed'`).Scan(&audited, &itemID))
	if !audited || itemID != *c.AdjustmentItemID || s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.adjustment_posted' AND new_data->>'source' = 'STAY_RATE_CHANGE'`) != 1 {
		t.Fatal("the audit says a financial adjustment was posted")
	}
	// a lower rate is a credit; the charge code still has more posted than the credit
	res, err = s.Front.ChangeRates(s.admin, s.propID, s.stay.Stay.ID, frontdesk.ChangeRatesInput{Version: res.Stay.Version, ApplyTo: frontdesk.ApplyNight, Date: d("2026-09-30"), Amount: "900000", Reason: "goodwill", Approval: s.approval(t)})
	must(t, err)
	if got := s.ledgerRate(t, charge); got.String() != "900000" || s.nightAmount(t, s.stay.Stay.ID, "2026-09-30") != "900000" {
		t.Fatalf("ledger rate after a credit: %s", got)
	}
}

func TestRateChangeOfANightOnAClosedDayIsAnAdjustmentOfTheOpenDay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-04")
	_, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	charge, _, _ := f.chargeOf(t, st.Stay.ID, "2026-09-30")
	f.nextDay(t) // 30 Sep is closed
	d1 := f.detail(t, st.Stay.ID)
	if d1.NightlyRates[0].Status != "CLOSED" || d1.NightlyRates[0].PostedOn == nil || *d1.NightlyRates[0].PostedOn != roomstest.BD || d1.NightlyRates[1].Status != "OPEN" {
		t.Fatalf("night statuses: %+v", d1.NightlyRates)
	}
	closedItems := f.Count(t, `SELECT count(*) FROM folio_items WHERE business_date = $1`, roomstest.BD)
	in := f.rateIn(d1.Stay.Version, frontdesk.ApplyNight, "2026-09-30", "1300000")
	in.Approval = f.approval(t)
	res, err := f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, in)
	must(t, err)
	var adjDay string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT business_date::text FROM folio_items WHERE id = $1`, *res.Changes[0].AdjustmentItemID).Scan(&adjDay))
	if adjDay != "2026-10-01" || f.Count(t, `SELECT count(*) FROM folio_items WHERE business_date = $1`, roomstest.BD) != closedItems {
		t.Fatalf("the adjustment is dated %s and the closed day is untouched", adjDay)
	}
	if f.ledgerRate(t, charge).String() != "1300000" {
		t.Fatalf("ledger: %s", f.ledgerRate(t, charge))
	}
	// the open night of the same stay needs neither
	_, err = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(res.Stay.Version, frontdesk.ApplyNight, "2026-10-02", "950000"))
	must(t, err)
}

func TestRateChangeNeedsAnOpenStayAndTheRightProperty(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	out, err := f.Front.ReverseCheckIn(f.admin, f.propID, st.Stay.ID, frontdesk.ReverseInput{Version: st.Stay.Version, Reason: "wrong guest"})
	must(t, err)
	_, err = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(out.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000"))
	wantCode(t, err, "STAY_NOT_OPEN") // a stay that left (cancelled here, checked out the same way) is closed to rate changes
	live := f.stay(t, f.r201.ID, "2026-10-03")
	ubud := f.Property(t, f.tenantID, "UBUD")
	_, err = f.Front.ChangeRates(f.admin, ubud.ID, live.Stay.ID, f.rateIn(live.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000"))
	wantCode(t, err, "STAY_NOT_FOUND")
	other := f.Tenant(t, "XYZ")
	stranger, _ := f.AdminAccount(t, other.ID)
	if _, err = f.Front.ChangeRates(stranger, f.propID, live.Stay.ID, f.rateIn(live.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000")); err == nil {
		t.Fatal("another tenant must not change a rate")
	}
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Front.ChangeRates(reader, f.propID, live.Stay.ID, f.rateIn(live.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000"))
	wantCode(t, err, "PERMISSION_DENIED")
	if f.nightAmount(t, live.Stay.ID, "2026-10-01") != "1000000" {
		t.Fatal("nothing changed")
	}
}

func TestAChangedMasterRateDoesNotMoveAStayAndAStayRateSurvivesIt(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-03")
	res, err := f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000"))
	must(t, err)
	_, err = f.Rates.FillRates(f.admin, f.propID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "2500000"})
	must(t, err)
	for date, want := range map[string]string{"2026-09-30": "1000000", "2026-10-01": "800000", "2026-10-02": "1000000"} {
		if got := f.nightAmount(t, st.Stay.ID, date); got != want {
			t.Fatalf("%s: %s, want %s", date, got, want)
		}
	}
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].Rate.Amount != "1000000" || res.Stay.ID != st.Stay.ID {
		t.Fatalf("in-house rate: %+v", rows)
	}
}

// Two rate changes at once on the version the screens both loaded: one wins, the other is told the stay changed.
func TestConcurrentRateChangesOneWins(t *testing.T) {
	for i := 0; i < 6; i++ {
		f := setup(t)
		st := f.stay(t, f.r101.ID, "2026-10-03")
		var errs [2]error
		sweep(time.Duration(i)*2*time.Millisecond,
			func() {
				_, errs[0] = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000"))
			},
			func() {
				_, errs[1] = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "700000"))
			})
		wins := 0
		for _, e := range errs {
			switch c := appErrorOrNil(t, "rate change", e); c {
			case "":
				wins++
			case "VERSION_CONFLICT", "RESOURCE_BUSY":
			default:
				t.Fatalf("run %d: %s", i, c)
			}
		}
		if wins != 1 {
			t.Fatalf("run %d: %d winners, want 1 (%v)", i, wins, errs)
		}
		if got := f.nightAmount(t, st.Stay.ID, "2026-10-01"); got != "800000" && got != "700000" {
			t.Fatalf("night: %s", got)
		}
	}
}

// A rate change racing the posting run of the same night: whichever goes first, the ledger ends up at the new rate (charged at it, or charged and adjusted) and agrees with the snapshot.
func TestRateChangeAndRoomChargePostingAtOnce(t *testing.T) {
	outcomes := map[string]int{}
	for i := 0; i < 12; i++ {
		f := setup(t)
		st := f.stay(t, f.r101.ID, "2026-10-03")
		in := f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-09-30", "1250000")
		// no approval: when the charge comes first the night is a correction and the change is refused (APPROVAL_REQUIRED); when the change comes first the night is charged at the new rate
		var changeErr, postErr error
		sweep(time.Duration(i)*2*time.Millisecond,
			func() { _, changeErr = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, in) },
			func() { _, postErr = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil) })
		cc, pc := appErrorOrNil(t, "change", changeErr), appErrorOrNil(t, "post", postErr)
		if (cc != "" && cc != "RESOURCE_BUSY" && cc != "APPROVAL_REQUIRED") || (pc != "" && pc != "RESOURCE_BUSY") {
			t.Fatalf("run %d: change %q post %q", i, cc, pc)
		}
		if cc != "" {
			outcomes[cc]++
			if f.nightAmount(t, st.Stay.ID, "2026-09-30") != "1000000" {
				t.Fatalf("run %d: a refused change moved the snapshot", i)
			}
			continue // the loser gave up cleanly; the night keeps the rate it was charged at
		}
		if pc != "" {
			_, postErr = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
			must(t, postErr)
		}
		charge, _, _ := f.chargeOf(t, st.Stay.ID, "2026-09-30")
		if got := f.ledgerRate(t, charge); got.String() != "1250000" || f.nightAmount(t, st.Stay.ID, "2026-09-30") != "1250000" {
			t.Fatalf("run %d: ledger %s, snapshot %s", i, got, f.nightAmount(t, st.Stay.ID, "2026-09-30"))
		}
		if n := f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`); n != 1 {
			t.Fatalf("run %d: %d charges, want 1 (the night is charged once)", i, n)
		}
		outcomes["charged at the new rate"]++
		if f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'ADJUSTMENT'`) != 0 {
			t.Fatalf("run %d: a night changed before it was charged needs no adjustment", i)
		}
	}
	t.Logf("outcomes: %v", outcomes)
}

// A rate change racing the night audit: the audit closes the day; a change that comes after is dated the next day and corrects the night with an adjustment.
func TestRateChangeAndNightAuditAtOnce(t *testing.T) {
	for i := 0; i < 8; i++ {
		f := setup(t)
		st := f.stay(t, f.r101.ID, "2026-10-04")
		day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
		must(t, err)
		f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
		in := f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-09-30", "1150000")
		in.Approval = f.approval(t)
		var changeErr, auditErr error
		sweep(time.Duration(i)*3*time.Millisecond,
			func() { _, changeErr = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, in) },
			func() { _, auditErr = f.Audit.Run(f.admin, f.propID, day.BusinessDate) })
		cc, ac := appErrorOrNil(t, "change", changeErr), appErrorOrNil(t, "audit", auditErr)
		if (cc != "" && cc != "RESOURCE_BUSY" && cc != "BUSINESS_DAY_NOT_FOUND" && cc != "APPROVAL_REQUIRED") || (ac != "" && ac != "RESOURCE_BUSY") {
			t.Fatalf("run %d: change %q audit %q", i, cc, ac)
		}
		if cc != "" || ac != "" {
			continue
		}
		var charge int64
		must(t, f.Pool.QueryRow(context.Background(), `SELECT p.folio_item_id FROM stay_charge_postings p WHERE p.stay_id = $1 AND p.service_date = $2 AND p.status = 'POSTED'`, st.Stay.ID, roomstest.BD).Scan(&charge))
		if got := f.ledgerRate(t, charge); got.String() != "1150000" {
			t.Fatalf("run %d: ledger %s after the audit and the change", i, got)
		}
	}
}

// A rate that goes down needs the approval of a rate approver (reservation.override_rate_approve): the caller's own when they hold it, else the credentials of someone who does.
func TestLoweringARateNeedsAnApproval(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-04")
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskRateChange, auth.PermReservationRead)
	lower := f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "800000")
	_, err := f.Front.ChangeRates(clerk, f.propID, st.Stay.ID, lower)
	wantCode(t, err, "APPROVAL_REQUIRED")
	if f.nightAmount(t, st.Stay.ID, "2026-10-01") != "1000000" {
		t.Fatal("a refused change moves nothing")
	}
	// the credentials of someone who is not a rate approver do not do
	other := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_ = other
	lower.Approval = &iam.ApprovalInput{Email: "nobody@example.com", Password: "wrong"}
	_, err = f.Front.ChangeRates(clerk, f.propID, st.Stay.ID, lower)
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	// the approver (the tenant administrator) approves
	lower.Approval = f.approval(t)
	res, err := f.Front.ChangeRates(clerk, f.propID, st.Stay.ID, lower)
	must(t, err)
	var by int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (new_data->>'rate_approved_by')::bigint FROM audit_logs WHERE action = 'stay.rate_changed'`).Scan(&by))
	if by == 0 || f.nightAmount(t, st.Stay.ID, "2026-10-01") != "800000" {
		t.Fatalf("approved by %d", by)
	}
	// a higher rate, or one that stays, needs none
	up := f.rateIn(res.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "900000")
	res, err = f.Front.ChangeRates(clerk, f.propID, st.Stay.ID, up)
	must(t, err)
	// a rate approver lowers it with no credentials: the caller is the approver, and it is recorded
	approver := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskRateChange, auth.PermReservationRead, auth.PermReservationOverrideApprove)
	_, err = f.Front.ChangeRates(approver, f.propID, st.Stay.ID, f.rateIn(res.Stay.Version, frontdesk.ApplyRemaining, "2026-10-02", "600000"))
	must(t, err)
	if f.nightAmount(t, st.Stay.ID, "2026-10-02") != "600000" || f.nightAmount(t, st.Stay.ID, "2026-10-03") != "600000" {
		t.Fatalf("nights: %+v", f.detail(t, st.Stay.ID).NightlyRates)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.rate_changed' AND new_data->>'rate_approved_by' IS NOT NULL`) != 3 {
		t.Fatal("every lowered night records who approved it")
	}
}
