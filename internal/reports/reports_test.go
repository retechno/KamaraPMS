package reports_test

import (
	"context"
	"encoding/csv"
	"os"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reports"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	wantCode = roomstest.Want
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	adminEmail       string
	dlx              rooms.RoomType
	r101, r102       rooms.Room
	plan, guest      int64
	minibar          int64
	vat              billingconfig.Tax
}

// setup: a property (IDR, no decimals) at 20:00 on 30 Sep 2026, two DLX rooms at 1,000,000 a night, and MINIBAR
// carrying 10% service and 11% VAT on service, mapped to the accounts 4-2000 / 2-2100 / 2.1.05.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, adminEmail: email}
	f.dlx = e.RoomType(t, admin, p.ID, "DLX")
	f.r101 = e.Room(t, admin, p.ID, f.dlx.ID, "101", housekeeping.Clean)
	f.r102 = e.Room(t, admin, p.ID, f.dlx.ID, "102", housekeeping.Clean)
	var chargeID int64
	ctx := context.Background()
	must(t, e.Pool.QueryRow(ctx, `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(ctx, `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'MINIBAR'`, p.ID).Scan(&f.minibar))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&f.plan))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, tn.ID, p.ID).Scan(&f.guest))
	_, err := e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	svc, err := e.Billing.CreateServiceCharge(admin, p.ID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", GLAccountCode: "2-2100", IsActive: true})
	must(t, err)
	f.vat, err = e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, GLAccountCode: "2.1.05", IsActive: true})
	must(t, err)
	_, err = e.Billing.ReplaceRules(admin, p.ID, f.minibar, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: f.vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	})
	must(t, err)
	_, err = e.Billing.UpdateChargeCode(admin, p.ID, f.minibar, billingconfig.ChargeCodePatch{GLAccountCode: ptr("4-2000")})
	must(t, err)
	return f
}

func (f *fx) stay(t *testing.T, room rooms.Room, departure string) frontdesk.CheckInResult {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d("2026-09-30"), Departure: d(departure), Adults: 2}}})
	must(t, err)
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	return out
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}
}

func (f *fx) minibarCharge(t *testing.T, folio int64, key string) folios.ItemResult {
	t.Helper()
	res, err := f.Folios.PostCharge(f.admin, f.propID, folio, key, folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	return res
}

func (f *fx) audit(t *testing.T) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(day.BusinessDate.At(civil.MustParseTimeOfDay("21:00"), time.FixedZone("WIB", 7*3600)))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
}

func (f *fx) sum(t *testing.T, query string) string {
	t.Helper()
	var s string
	must(t, f.Pool.QueryRow(context.Background(), query, f.propID).Scan(&s))
	return s
}

func TestRevenueReconcilesWithTheLedger(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.minibarCharge(t, st.Folio.ID, "c1")
	second := f.minibarCharge(t, st.Folio.ID, "c2")
	_, err := f.Folios.Reverse(f.admin, f.propID, second.Item.ID, folios.CorrectionInput{Reason: "twice", Approval: f.approval()})
	must(t, err)
	_, err = f.Folios.PostAdjustment(f.admin, f.propID, st.Folio.ID, "a1", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "-50000", Reason: "goodwill", Approval: f.approval()})
	must(t, err)
	_, err = f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "500000", PaymentMethod: "CASH"})
	must(t, err)
	f.audit(t) // posts the room night of 30 Sep

	rev, err := f.Reports.Revenue(f.admin, f.propID, d("2026-09-30"), d("2026-09-30"))
	must(t, err)
	// the report equals the ledger: revenue items only (payments are not revenue)
	ledger := func(col string) string {
		return f.sum(t, `SELECT COALESCE(sum(`+col+`), 0)::text FROM folio_items WHERE property_id = $1 AND transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL')`)
	}
	if rev.Totals.Net+"|"+rev.Totals.Service+"|"+rev.Totals.Tax != fmtLedger(ledger("net_amount"), ledger("service_charge_total"), ledger("tax_total")) ||
		rev.Totals.Total != fmtLedger1(f.sum(t, `SELECT COALESCE(sum(debit - credit), 0)::text FROM folio_items WHERE property_id = $1 AND transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL')`)) {
		t.Fatalf("totals %+v vs ledger net %s service %s tax %s", rev.Totals, ledger("net_amount"), ledger("service_charge_total"), ledger("tax_total"))
	}
	by := map[string]reports.RevenueLine{}
	for _, l := range rev.ByCode {
		by[l.ChargeCode] = l
	}
	// room: 1,000,000 with no rules; minibar: 100,000 charged twice (one reversed) less a 50,000 adjustment
	if by["ROOM"].Net != "1000000" || by["ROOM"].Items != 1 || by["MINIBAR"].Items != 4 || by["MINIBAR"].Net != "50000" {
		t.Fatalf("lines: %+v", rev.ByCode)
	}
	if by["MINIBAR"].RevenueAccountCode == nil || *by["MINIBAR"].RevenueAccountCode != "4-2000" {
		t.Fatalf("account: %+v", by["MINIBAR"])
	}
	types := map[string]string{}
	for _, tt := range rev.ByType {
		types[tt.ChargeType] = tt.Net
	}
	if types["ROOM"] != "1000000" || types["FOOD_BEVERAGE"] != "50000" && types["OTHER"] != "50000" && len(rev.ByType) != 2 {
		t.Fatalf("by type: %+v", rev.ByType)
	}
	// another day has nothing; a range outside the data is empty, not an error
	empty, err := f.Reports.Revenue(f.admin, f.propID, d("2026-10-01"), d("2026-10-05"))
	must(t, err)
	if len(empty.ByCode) != 0 || empty.Totals.Total != "0" {
		t.Fatalf("empty: %+v", empty)
	}
}

func fmtLedger(net, svc, tax string) string {
	return fmtLedger1(net) + "|" + fmtLedger1(svc) + "|" + fmtLedger1(tax)
}

// fmtLedger1 renders a numeric(18,3) text as the report does for an IDR property (no decimals).
func fmtLedger1(s string) string {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestTaxReportReadsTheSnapshotsAndSurvivesEdits(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.minibarCharge(t, st.Folio.ID, "c1") // 100,000 + 10,000 service, VAT 11% of 110,000 = 12,100
	_, err := f.Billing.UpdateTax(f.admin, f.propID, f.vat.ID, billingconfig.TaxPatch{Rate: ptr("12"), Name: ptr("PPN"), GLAccountCode: ptr("2.1.99")})
	must(t, err)
	f.minibarCharge(t, st.Folio.ID, "c2") // VAT 12% of 110,000 = 13,200
	r, err := f.Reports.Tax(f.admin, f.propID, d("2026-09-30"), d("2026-09-30"))
	must(t, err)
	if len(r.Taxes) != 2 || len(r.ServiceCharges) != 1 {
		t.Fatalf("lines: %+v", r)
	}
	old, now := r.Taxes[0], r.Taxes[1]
	if old.Rate != "11.0000" || old.Name != "VAT" || old.Amount != "12100" || old.Base != "110000" || old.GLAccountCode == nil || *old.GLAccountCode != "2.1.05" ||
		now.Rate != "12.0000" || now.Name != "PPN" || now.Amount != "13200" || *now.GLAccountCode != "2.1.99" {
		t.Fatalf("the edit must not rewrite the past: %+v", r.Taxes)
	}
	if r.ServiceCharges[0].Amount != "20000" || r.ServiceCharges[0].Items != 2 || r.TaxTotal != "25300" || r.ServiceTotal != "20000" {
		t.Fatalf("service / totals: %+v", r)
	}
	// reconciles with the components of the ledger
	if got := f.sum(t, `SELECT COALESCE(sum(amount), 0)::text FROM folio_item_components WHERE property_id = $1 AND component_type = 'TAX'`); fmtLedger1(got) != r.TaxTotal {
		t.Fatalf("tax total %s vs ledger %s", r.TaxTotal, got)
	}
	// the item totals agree with the components
	if got := f.sum(t, `SELECT sum(tax_total)::text FROM folio_items WHERE property_id = $1`); fmtLedger1(got) != r.TaxTotal {
		t.Fatalf("item tax %s", got)
	}
}

func TestCashierReport(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	cash, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p2", folios.PaymentInput{Amount: "200000", PaymentMethod: "CARD"})
	must(t, err)
	typo, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p3", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	must(t, err)
	_, err = f.Folios.Void(f.admin, f.propID, typo.Payment.ID, folios.CorrectionInput{Reason: "typo", Approval: f.approval()})
	must(t, err)
	_, err = f.Folios.Refund(f.admin, f.propID, cash.Payment.ID, "r1", folios.RefundInput{Amount: "50000", Reason: "goodwill", Approval: f.approval()})
	must(t, err)
	r, err := f.Reports.Cashier(f.admin, f.propID, d("2026-09-30"), d("2026-09-30"))
	must(t, err)
	if len(r.Lines) != 2 || r.Net != "450000" || len(r.ByMethod) != 2 {
		t.Fatalf("cashier: %+v", r)
	}
	c := r.Lines[0] // CARD sorts before CASH
	if c.Method != "CARD" || c.Payments != "200000" || c.Net != "200000" {
		t.Fatalf("card: %+v", c)
	}
	cashLine := r.Lines[1]
	if cashLine.Method != "CASH" || cashLine.Payments != "300000" || cashLine.Refunds != "50000" || cashLine.Net != "250000" || cashLine.Voided != "1000" || cashLine.VoidedCount != 1 || cashLine.Count != 2 {
		t.Fatalf("cash: %+v", cashLine)
	}
	// equals the payments table (posted payments less refunds)
	if got := f.sum(t, `SELECT (COALESCE(sum(amount) FILTER (WHERE payment_type = 'PAYMENT'), 0) - COALESCE(sum(amount) FILTER (WHERE payment_type = 'REFUND'), 0))::text FROM payments WHERE property_id = $1 AND status = 'POSTED'`); fmtLedger1(got) != r.Net {
		t.Fatalf("ledger %s", got)
	}
}

func TestStatisticsAndDailySummaryFromClosedDays(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.audit(t) // 30 Sep
	f.audit(t) // 1 Oct
	_ = st
	live, err := f.Reports.DailySummary(f.admin, f.propID, d("2026-10-02")) // the open day: live
	must(t, err)
	if live.Status != "OPEN" || !live.Live || live.Summary == nil || live.Summary.Rooms.Occupied != 1 {
		t.Fatalf("live: %+v", live)
	}
	closed, err := f.Reports.DailySummary(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if closed.Status != "CLOSED" || closed.Live || closed.Summary == nil || closed.Summary.RoomRevenue.Net != "1000000" || closed.Summary.RoomChargesPosted != 1 {
		t.Fatalf("closed: %+v", closed)
	}
	_, err = f.Reports.DailySummary(f.admin, f.propID, d("2026-08-01"))
	wantCode(t, err, "BUSINESS_DAY_NOT_FOUND")
	stats, err := f.Reports.Statistics(f.admin, f.propID, d("2026-09-30"), d("2026-10-05"))
	must(t, err)
	tt := stats.Totals
	if len(stats.Days) != 2 || tt.Days != 2 || tt.AvailableNights != 4 || tt.OccupiedNights != 2 || tt.RoomNightsSold != 2 || tt.RoomRevenue != "2000000" ||
		tt.OccupancyPercent != "50.00" || tt.ADR != "1000000" || tt.RevPAR != "500000" {
		t.Fatalf("totals: %+v", tt)
	}
	if stats.Days[0].BusinessDate != d("2026-09-30") || stats.Days[0].OccupancyPercent != "50.00" || stats.Days[1].Arrivals != 0 {
		t.Fatalf("days: %+v", stats.Days)
	}
}

func TestOperationalLists(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-01")
	f.minibarCharge(t, st.Folio.ID, "c1")
	f.stay(t, f.r102, "2026-10-03")
	f.Room(t, f.admin, f.propID, f.dlx.ID, "103", housekeeping.Clean)
	_, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d("2026-09-30"), Departure: d("2026-10-02"), Adults: 1}}})
	must(t, err)
	in, err := f.Reports.InHouse(f.admin, f.propID)
	must(t, err)
	if len(in.Rows) != 2 || in.Rows[0].Room != "101" || in.Rows[0].Balance != "122100" || in.Rows[0].Guest != "Guest" || in.Rows[1].Balance != "0" {
		t.Fatalf("in house: %+v", in.Rows)
	}
	dep, err := f.Reports.Departures(f.admin, f.propID, d("2026-10-01"))
	must(t, err)
	if len(dep.Rows) != 1 || dep.Rows[0].StayID != st.Stay.ID || dep.Rows[0].Room != "101" || dep.Rows[0].Status != "OPEN" {
		t.Fatalf("departures: %+v", dep.Rows)
	}
	arr, err := f.Reports.Arrivals(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	status := map[string]int{}
	for _, a := range arr.Rows {
		status[a.Status]++
	}
	if len(arr.Rows) != 3 || status["CHECKED_IN"] != 2 || status["CONFIRMED"] != 1 {
		t.Fatalf("arrivals: %+v", arr.Rows)
	}
}

func TestReportAccessAndRanges(t *testing.T) {
	f := setup(t)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.Reports.Revenue(clerk, f.propID, d("2026-09-30"), d("2026-09-30"))
	wantCode(t, err, "PERMISSION_DENIED")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermReportView)
	if _, err := f.Reports.Revenue(viewer, f.propID, d("2026-09-30"), d("2026-09-30")); err != nil {
		t.Fatal(err)
	}
	_, err = f.Reports.Revenue(f.admin, f.propID, d("2026-10-02"), d("2026-10-01"))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Reports.Cashier(f.admin, f.propID, d("2026-01-01"), d("2027-06-01"))
	wantCode(t, err, "VALIDATION_FAILED")
	other := f.Tenant(t, "XYZ")
	foreign := roomstest.Admin(other.ID)
	_, err = f.Reports.Revenue(foreign, f.propID, d("2026-09-30"), d("2026-09-30"))
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestSafeCellNeutralisesFormulas(t *testing.T) {
	for in, want := range map[string]string{
		"=1+1": "'=1+1", "+SUM(A1)": "'+SUM(A1)", "@cmd": "'@cmd", "-2+3": "'-2+3", "-100": "-100", "-100.50": "-100.50", "plain": "plain", "": "", "Budi": "Budi",
	} {
		if got := reports.SafeCell(in); got != want {
			t.Errorf("SafeCell(%q) = %q, want %q", in, got, want)
		}
	}
	h, rows := reports.StayList{Rows: []reports.StayRow{{Guest: "=cmd|' /C calc'!A0", Balance: "-5"}}}.CSV()
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write(h)
	for _, r := range rows {
		for i := range r {
			r[i] = reports.SafeCell(r[i])
		}
		_ = w.Write(r)
	}
	w.Flush()
	if strings.Contains(sb.String(), ",=cmd") || !strings.Contains(sb.String(), "'=cmd") {
		t.Fatalf("csv: %s", sb.String())
	}
}

func TestDashboard(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-02")
	dash, err := f.Reports.Dashboard(f.admin, f.propID)
	must(t, err)
	if dash.BusinessDate != d("2026-09-30") || dash.Today == nil || dash.Today.Rooms.Occupied != 1 {
		t.Fatalf("today: %+v", dash.Today)
	}
	if m := dash.Movements; m.InHouse != 1 || m.ArrivalsCheckedIn != 1 || m.ArrivalsExpected != 0 || m.DeparturesExpected != 0 || m.InHouseBalance != "0" {
		t.Fatalf("movements: %+v", m)
	}
	if n := dash.Rooms.Clean + dash.Rooms.Dirty + dash.Rooms.Cleaning + dash.Rooms.Inspected; n != 2 {
		t.Fatalf("room statuses: %+v", dash.Rooms)
	}
	if len(dash.Forecast) != reports.ForecastDays || dash.Forecast[0].Booked != 1 || dash.Forecast[0].Sellable != 2 || dash.Forecast[0].OccupancyPercent != "50.00" ||
		dash.Forecast[1].Booked != 1 || dash.Forecast[2].Booked != 0 {
		t.Fatalf("forecast: %+v", dash.Forecast)
	}
	if len(dash.Trend) != 0 || dash.MonthToDate.Days != 0 {
		t.Fatalf("nothing is closed yet: %+v %+v", dash.Trend, dash.MonthToDate)
	}

	// After two night audits: 30 Sep and 1 Oct are closed, today is 2 Oct and the stay leaves today.
	f.audit(t)
	f.audit(t)
	dash, err = f.Reports.Dashboard(f.admin, f.propID)
	must(t, err)
	if dash.BusinessDate != d("2026-10-02") || len(dash.Trend) != 2 || dash.Trend[0].BusinessDate != d("2026-09-30") {
		t.Fatalf("trend: %+v", dash.Trend)
	}
	if dash.MonthToDate.Days != 1 || dash.MonthToDate.RoomRevenue == "0" || dash.PreviousMonth.Days != 0 { // 1 Sep was not a business day here
		t.Fatalf("month: %+v previous %+v", dash.MonthToDate, dash.PreviousMonth)
	}
	if dash.Movements.DeparturesExpected != 1 || dash.Movements.InHouse != 1 {
		t.Fatalf("movements on the departure day: %+v", dash.Movements)
	}

	// report.view is needed
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err = f.Reports.Dashboard(clerk, f.propID)
	wantCode(t, err, "PERMISSION_DENIED")
}
