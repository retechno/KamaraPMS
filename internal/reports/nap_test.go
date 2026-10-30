package reports_test

import (
	"encoding/csv"
	"strings"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reports"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// book is a confirmed reservation of one DLX room (1,000,000 a night) with the given source.
func (f *fx) book(t *testing.T, arrival, departure, source string, lines int) reservations.Reservation {
	t.Helper()
	ls := make([]reservations.LineInput, lines)
	for i := range ls {
		ls[i] = reservations.LineInput{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d(arrival), Departure: d(departure), Adults: 2}
	}
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: source, Confirm: true, Rooms: ls})
	must(t, err)
	return res
}

func (f *fx) reload(t *testing.T, id int64) reservations.Reservation {
	t.Helper()
	r, err := f.Res.Get(f.admin, f.propID, id)
	must(t, err)
	return r
}

func eventOf(t *testing.T, r reports.CancellationReport, kind, conf string, n int) reports.CancellationEvent {
	t.Helper()
	i := 0
	for _, e := range r.Events {
		if e.Kind == kind && e.ConfirmationNumber == conf {
			if i == n {
				return e
			}
			i++
		}
	}
	t.Fatalf("no %s event %d of %s in %+v", kind, n, conf, r.Events)
	return reports.CancellationEvent{}
}

// The day: A cancelled (2 nights = 2,000,000, 300,000 deposit, a fee of 200,000), B cancelled (1 night = 1,000,000, no fee), D cancelled and reinstated, and one reservation of three rooms
// of 2 nights each, all three marked no-show in one action, the first with a fee of 1,000,000. Fees posted 1,200,000 (2), deposits held 300,000.
func TestCancellationsReport(t *testing.T) {
	f := setup(t)
	f.Room(t, f.admin, f.propID, f.dlx.ID, "103", housekeeping.Clean)

	a := f.book(t, "2026-10-02", "2026-10-04", "PHONE", 1)
	_, err := f.Folios.Deposit(f.admin, f.propID, a.ID, "d1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	a = f.reload(t, a.ID)
	_, err = f.Res.Cancel(f.admin, f.propID, a.ID, a.Version, "Guest cancelled late")
	must(t, err)
	_, err = f.Folios.PostReservationFee(f.admin, f.propID, a.ID, folios.FeeInput{Type: "CANCEL_FEE", Amount: "200000", Reason: "late cancellation"})
	must(t, err)

	b := f.book(t, "2026-10-05", "2026-10-06", "WEBSITE", 1)
	_, err = f.Res.Cancel(f.admin, f.propID, b.ID, b.Version, "guest  cancelled LATE")
	must(t, err)

	dd := f.book(t, "2026-10-10", "2026-10-11", "PHONE", 1)
	out, err := f.Res.Cancel(f.admin, f.propID, dd.ID, dd.Version, "double booking")
	must(t, err)
	_, err = f.Res.Reinstate(f.admin, f.propID, dd.ID, out.Reservation.Version)
	must(t, err)

	n := f.book(t, "2026-09-30", "2026-10-02", "PHONE", 3)
	var ids []int64
	for _, l := range n.Rooms {
		ids = append(ids, l.ID)
	}
	_, err = f.Res.BulkNoShow(f.admin, f.propID, d("2026-09-30"), ids, "did not arrive")
	must(t, err)
	_, err = f.Folios.PostReservationFee(f.admin, f.propID, n.ID, folios.FeeInput{Type: "NO_SHOW_FEE", ReservationRoomID: &ids[0], Amount: "1000000", Reason: "no show"})
	must(t, err)

	r, err := f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CancelKindAll})
	must(t, err)
	if len(r.Events) != 6 {
		t.Fatalf("events: %+v", r.Events)
	}
	ea := eventOf(t, r, reports.CancelKindCancelled, a.ConfirmationNumber, 0)
	if ea.Rooms != 1 || *ea.Nights != 2 || *ea.ValueLost != "2000000" || *ea.LeadDays != 2 || ea.FeeNet == nil || *ea.FeeNet != "200000" || ea.DepositNow != "300000" || ea.Reinstated || ea.Source != "PHONE" {
		t.Fatalf("A: %+v", ea)
	}
	eb := eventOf(t, r, reports.CancelKindCancelled, b.ConfirmationNumber, 0)
	if *eb.ValueLost != "1000000" || *eb.LeadDays != 5 || eb.FeeNet != nil || eb.DepositNow != "0" {
		t.Fatalf("B (a missing fee is null, not zero): %+v", eb)
	}
	ed := eventOf(t, r, reports.CancelKindCancelled, dd.ConfirmationNumber, 0)
	if !ed.Reinstated || ed.Nights != nil || ed.ValueLost != nil {
		t.Fatalf("D was reinstated: %+v", ed)
	}
	var fees int
	for i := 0; i < 3; i++ {
		e := eventOf(t, r, reports.CancelKindNoShow, n.ConfirmationNumber, i)
		if e.Rooms != 1 || *e.Nights != 2 || *e.ValueLost != "2000000" || *e.LeadDays != 0 || e.Reason != "did not arrive" {
			t.Fatalf("no-show %d: %+v", i, e)
		}
		if e.FeeNet != nil {
			fees++
			if *e.FeeNet != "1000000" {
				t.Fatalf("no-show fee: %+v", e)
			}
		}
	}
	if fees != 1 {
		t.Fatalf("one of the three no-shows carries the fee, got %d", fees)
	}
	kinds := map[string]reports.CancellationGroup{}
	for _, g := range r.ByKind {
		kinds[g.Key] = g
	}
	if c := kinds[reports.CancelKindCancelled]; c.Events != 3 || c.Rooms != 3 || c.Nights != 3 || c.Value != "3000000" { // the reinstated D has its rooms back: no nights or value to show
		t.Fatalf("cancelled: %+v", c)
	}
	if c := kinds[reports.CancelKindNoShow]; c.Events != 3 || c.Rooms != 3 || c.Nights != 6 || c.Value != "6000000" {
		t.Fatalf("no-show: %+v", c)
	}
	if r.FeesPosted != "1200000" || r.FeeCount != 2 || r.DepositsHeld != "300000" {
		t.Fatalf("fees %s (%d) deposits %s", r.FeesPosted, r.FeeCount, r.DepositsHeld)
	}
	// "Guest cancelled late" and "guest  cancelled LATE" are one reason
	for _, g := range r.ByReason {
		if strings.EqualFold(g.Key, "guest cancelled late") && g.Events != 2 {
			t.Fatalf("reasons: %+v", r.ByReason)
		}
	}
	// filters
	ns, err := f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CancelKindNoShow})
	must(t, err)
	if len(ns.Events) != 3 {
		t.Fatalf("no-shows: %d", len(ns.Events))
	}
	web, err := f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CancelKindAll, Source: "WEBSITE"})
	must(t, err)
	if len(web.Events) != 1 {
		t.Fatalf("web: %+v", web.Events)
	}
	// another day has none
	none, err := f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2026-09-01"), To: d("2026-09-29"), Kind: reports.CancelKindAll})
	must(t, err)
	if len(none.Events) != 0 || none.FeesPosted != "0" {
		t.Fatalf("empty: %+v", none)
	}
}

func TestCancellationsAccessAndRange(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	q := func(string) string { return "not-a-date" }
	// the permission is answered before the query is looked at
	_, err := f.Reports.CancellationParamsFrom(clerk, f.propID, q)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Reports.CancellationParamsFrom(f.admin, f.propID, q)
	wantCode(t, err, "VALIDATION_FAILED")
	other := f.Tenant(t, "XYZ")
	_, err = f.Reports.CancellationParamsFrom(roomstest.Admin(other.ID), f.propID, q)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2026-10-02"), To: d("2026-10-01"), Kind: reports.CancelKindAll})
	if err == nil {
		t.Fatal("a range that ends before it starts is refused")
	}
	_, err = f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2020-01-01"), To: d("2026-09-30"), Kind: reports.CancelKindAll})
	if err == nil {
		t.Fatal("a range of years is refused")
	}
}

func TestNapCSV(t *testing.T) {
	f := setup(t)
	a := f.book(t, "2026-10-02", "2026-10-04", "PHONE", 1)
	_, err := f.Res.Cancel(f.admin, f.propID, a.ID, a.Version, "=cmd|' /C calc'!A0")
	must(t, err)
	r, err := f.Reports.Cancellations(f.admin, f.propID, reports.CancellationParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CancelKindAll})
	must(t, err)
	h, rows := r.CSV()
	if len(rows) != 1 || len(rows[0]) != len(h) {
		t.Fatalf("csv %v %v", h, rows)
	}
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write(h)
	_ = w.Write(rows[0])
	w.Flush()
	// the guard against formulas is the job of SafeCell when the handler writes: the raw rows still hold the text
	if !strings.Contains(sb.String(), "calc") {
		t.Fatalf("csv: %s", sb.String())
	}
}

// A budget exists only for the figures that have one (38 7.1): the month has a line for the rooms account (group A) of 30,000,000, nothing for food and beverage or for the rest. On the last
// day of a 30-day month the pro rata is the whole month. Group A shows the budget and the variance (1,000,000 - 30,000,000 = -29,000,000, -96.67 %); the groups without a line, the total of the
// groups without one, the counts of rooms and the days other than MTD stay blank, never zero.
func TestTheDailyFlashBudgetIsOnlyForTheFiguresThatHaveOne(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	var user, bud int64
	must(t, f.Pool.QueryRow(ctx, `SELECT id FROM users WHERE tenant_id = $1 ORDER BY id LIMIT 1`, f.tenantID).Scan(&user))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO budgets (tenant_id, property_id, year_start, version, name, status) VALUES ($1, $2, '2026-01-01', 1, 'Budget 2026', 'DRAFT') RETURNING id`, f.tenantID, f.propID).Scan(&bud))
	_, err := f.Pool.Exec(ctx, `INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount)
		SELECT $1, $2, $3, id, 9, 30000000 FROM gl_accounts WHERE property_id = $2 AND code = '4110'`, f.tenantID, f.propID, bud)
	must(t, err)
	_, err = f.Pool.Exec(ctx, `UPDATE budgets SET status = 'ACTIVE', activated_at = now(), activated_by = $2, approved_by = $2 WHERE id = $1`, bud, user)
	must(t, err)

	f.stay(t, f.r101, "2026-10-03")
	f.audit(t)
	r, err := f.Reports.DailyFlash(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if !r.HasBudget {
		t.Fatal("the budget of the year is active")
	}
	by := map[string]reports.FlashLine{}
	for _, g := range r.Revenue.Groups {
		by[g.Code] = g.Total
	}
	a := by["A"]
	if a.MTD != "1000000" || a.Budget == nil || *a.Budget != "30000000" || a.Variance == nil || *a.Variance != "-29000000" || a.VariancePct == nil || *a.VariancePct != "-96.67" || a.Favourable == nil || *a.Favourable {
		t.Fatalf("group A: %+v", a)
	}
	if by["B"].Budget != nil || by["C"].Budget != nil || by["B"].Variance != nil {
		t.Fatalf("a group without a budget line is blank: B %+v C %+v", by["B"], by["C"])
	}
	// the total is the budget of the groups that have one, against the actual of those groups
	tn := r.Revenue.TotalNet
	if tn.Budget == nil || *tn.Budget != "30000000" || tn.Variance == nil || *tn.Variance != "-29000000" {
		t.Fatalf("total: %+v", tn)
	}
	if a.Today != "1000000" || a.YTD != "1000000" {
		t.Fatalf("today and YTD are actuals: %+v", a)
	}
	for _, l := range r.Rooms {
		if l.Key == "rooms_total" || l.Key == "out_of_order" || l.Key == "guests_in_house" {
			if l.Budget != nil || l.Variance != nil {
				t.Fatalf("a count of rooms or guests has no money budget: %+v", l)
			}
		}
	}
}

// The day of the folio corrections (the minibar carries 10% service and 11% VAT on both, so 100,000 of net is 122,100):
//
//	c1 100,000 posted and reversed "twice"                         VOID        net -100,000 service -10,000 tax -12,100 gross -122,100
//	c2 100,000 posted, then an adjustment of -50,000 "goodwill"    ADJUSTMENT  net  -50,000 service  -5,000 tax  -6,050 gross  -61,050
//	c3 100,000 less a discount of 10,000                           DISCOUNT    net  -10,000
//	a rebate of -40,000 on a charge code that posts to 4160        ALLOWANCE   net  -40,000
//	a payment of 1,000 voided "typo"                               VOID        gross 1,000 (money, no net)
//	a refund of 50,000 of a deposit of 100,000                     REFUND      gross 50,000
//
// Taken back from the revenue: 100,000 + 50,000 + 10,000 + 40,000 = 200,000. The net revenue of the day: (100,000 - 100,000) + (100,000 - 50,000) + 90,000 - 40,000 = 100,000.
func TestCorrectionsOfEveryKindOnTheFolios(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	c1 := f.minibarCharge(t, st.Folio.ID, "c1")
	_, err := f.Folios.Reverse(f.admin, f.propID, c1.Item.ID, folios.CorrectionInput{Reason: "twice", Approval: f.approval()})
	must(t, err)
	f.minibarCharge(t, st.Folio.ID, "c2")
	_, err = f.Folios.PostAdjustment(f.admin, f.propID, st.Folio.ID, "a1", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "-50000", Reason: "goodwill", Approval: f.approval()})
	must(t, err)
	_, err = f.Folios.PostCharge(f.admin, f.propID, st.Folio.ID, "c3", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000"), Discount: "10000"})
	must(t, err)
	var other int64
	must(t, f.Pool.QueryRow(t.Context(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'OTHER'`, f.propID).Scan(&other))
	_, err = f.Billing.UpdateChargeCode(f.admin, f.propID, other, billingconfig.ChargeCodePatch{GLAccountCode: ptr("4160")})
	must(t, err)
	_, err = f.Folios.PostCharge(f.admin, f.propID, st.Folio.ID, "a2", folios.ChargeInput{ChargeCodeID: other, Quantity: "1", UnitPrice: ptr("40000"), Description: "rebate"})
	must(t, err)
	typo, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = f.Folios.Void(f.admin, f.propID, typo.Payment.ID, folios.CorrectionInput{Reason: "typo", Approval: f.approval()})
	must(t, err)
	st2 := f.stay(t, f.r102, "2026-10-02")
	dep, err := f.Folios.PostPayment(f.admin, f.propID, st2.Folio.ID, "p2", folios.PaymentInput{Amount: "100000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = f.Folios.Refund(f.admin, f.propID, dep.Payment.ID, "r1", folios.RefundInput{Amount: "50000", Reason: "goodwill", Approval: f.approval()})
	must(t, err)

	r, err := f.Reports.Corrections(f.admin, f.propID, reports.CorrectionParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CorrAll})
	must(t, err)
	type want struct{ net, service, tax, gross, reason, by string }
	wants := map[string][]want{
		reports.CorrVoid:      {{"-100000", "-10000", "-12100", "-122100", "twice", "Admin"}, {"0", "0", "0", "1000", "typo", "Admin"}},
		reports.CorrAdjust:    {{"-50000", "-5000", "-6050", "-61050", "goodwill", "Admin"}},
		reports.CorrDiscount:  {{"-10000", "0", "0", "-10000", "", "Admin"}},
		reports.CorrAllowance: {{"40000", "0", "0", "40000", "", "Admin"}}, // the charge on an allowance account, as the ledger holds it
		reports.CorrRefund:    {{"0", "0", "0", "50000", "goodwill", "Admin"}},
	}
	got := map[string][]reports.CorrectionRow{}
	for _, x := range r.Rows {
		got[x.Kind] = append(got[x.Kind], x)
	}
	if len(r.Rows) != 6 {
		t.Fatalf("rows: %+v", r.Rows)
	}
	for kind, ws := range wants {
		if len(got[kind]) != len(ws) {
			t.Fatalf("%s: %+v", kind, got[kind])
		}
		for i, w := range ws {
			x := got[kind][i]
			if x.Net != w.net || x.Service != w.service || x.Tax != w.tax || x.Gross != w.gross || x.Reason != w.reason || x.DoneBy != w.by {
				t.Fatalf("%s %d: %+v, want %+v", kind, i, x, w)
			}
		}
	}
	// a reversal and an adjustment carry the approver; a discount does not need one here
	if got[reports.CorrVoid][0].ApprovedBy != "Admin" || got[reports.CorrVoid][0].OriginalItemID == nil || got[reports.CorrVoid][0].OriginalDate == nil {
		t.Fatalf("the void names the item it reversed: %+v", got[reports.CorrVoid][0])
	}
	// 100,000 + 50,000 + 10,000 taken back (the allowance charge adds to the account, it takes nothing back) of a net revenue of 0 + 50,000 + 90,000 + 40,000 = 180,000
	if r.RevenueTakenBack != "160000" || r.NetRevenue != "180000" || r.SharePercent == nil || *r.SharePercent != "88.89" {
		t.Fatalf("share: taken %s of %s = %v", r.RevenueTakenBack, r.NetRevenue, r.SharePercent)
	}
	kinds := map[string]reports.CorrectionTotal{}
	for _, k := range r.ByKind {
		kinds[k.Key] = k
	}
	if k := kinds[reports.CorrVoid]; k.Count != 2 || k.Net != "-100000" || k.Gross != "-121100" {
		t.Fatalf("by kind: %+v", r.ByKind)
	}
	if len(r.ByUser) != 1 || r.ByUser[0].Key != "Admin" || r.ByUser[0].Count != 6 {
		t.Fatalf("by user: %+v", r.ByUser)
	}
	// reasons are folded: "goodwill" appears on the adjustment and the refund
	for _, g := range r.ByReason {
		if g.Key == "goodwill" && g.Count != 2 {
			t.Fatalf("by reason: %+v", r.ByReason)
		}
	}
	// filters: one kind; another user has none
	one, err := f.Reports.Corrections(f.admin, f.propID, reports.CorrectionParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CorrRefund})
	must(t, err)
	if len(one.Rows) != 1 {
		t.Fatalf("filter by kind: %+v", one.Rows)
	}
	nobody := int64(999999)
	none, err := f.Reports.Corrections(f.admin, f.propID, reports.CorrectionParams{From: d("2026-09-30"), To: d("2026-09-30"), Kind: reports.CorrAll, UserID: &nobody})
	must(t, err)
	if len(none.Rows) != 0 {
		t.Fatalf("filter by user: %+v", none.Rows)
	}
}
