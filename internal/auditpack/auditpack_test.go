package auditpack_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/pos"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

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
	pack             *auditpack.Service
}

// setup: a property (IDR, no decimals) at 20:00 on 30 Sep 2026, two DLX rooms at 1,000,000 a night, MINIBAR carrying 10% service and 11% VAT on service, and the night audit building its pack.
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
	vat, err := e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, GLAccountCode: "2.1.05", IsActive: true})
	must(t, err)
	_, err = e.Billing.ReplaceRules(admin, p.ID, f.minibar, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	})
	must(t, err)
	_, err = e.Billing.UpdateChargeCode(admin, p.ID, f.minibar, billingconfig.ChargeCodePatch{GLAccountCode: ptr("4-2000")})
	must(t, err)
	f.pack = auditpack.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), e.Tenancy, e.Reports)
	e.Audit.SetPackBuilder(f.pack)
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

func (f *fx) minibarCharge(t *testing.T, folio int64, key string) folios.ItemResult {
	t.Helper()
	res, err := f.Folios.PostCharge(f.admin, f.propID, folio, key, folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	return res
}

// run closes the open business day through the night audit.
func (f *fx) run(t *testing.T) (civil.Date, error) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(day.BusinessDate.At(civil.MustParseTimeOfDay("21:00"), time.FixedZone("WIB", 7*3600)))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	return day.BusinessDate, err
}

func (f *fx) closeDay(t *testing.T) civil.Date {
	t.Helper()
	bd, err := f.run(t)
	must(t, err)
	return bd
}

func (f *fx) statuses(t *testing.T, bd civil.Date) map[string]string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT s.section, s.status FROM nap_pack_sections s JOIN nap_packs p ON p.id = s.pack_id WHERE p.property_id = $1 AND p.business_date = $2`, f.propID, bd)
	must(t, err)
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var code, status string
		must(t, rows.Scan(&code, &status))
		out[code] = status
	}
	must(t, rows.Err())
	return out
}

// stored is the JSON of a section as the pack keeps it, decoded.
func (f *fx) stored(t *testing.T, bd civil.Date, section string) any {
	t.Helper()
	var raw []byte
	must(t, f.Pool.QueryRow(context.Background(), `SELECT s.data FROM nap_pack_sections s JOIN nap_packs p ON p.id = s.pack_id WHERE p.property_id = $1 AND p.business_date = $2 AND s.section = $3`,
		f.propID, bd, section).Scan(&raw))
	return decode(t, raw)
}

func decode(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	must(t, json.Unmarshal(raw, &v))
	return v
}

func live(t *testing.T, v any, err error) any {
	t.Helper()
	must(t, err)
	raw, err := json.Marshal(v)
	must(t, err)
	return decode(t, raw)
}

func TestTheRunBuildsThePackOfTheClosedDay(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	f.minibarCharge(t, st.Folio.ID, "c1")
	bd := f.closeDay(t)

	got := f.statuses(t, bd)
	if len(got) != len(auditpack.Catalog) {
		t.Fatalf("sections: %v", got)
	}
	for _, def := range auditpack.Catalog {
		want := auditpack.StatusOK
		if def.Code == auditpack.SecZReports || def.Code == auditpack.SecOutlets { // no cashier shift and no point of sale in this property
			want = auditpack.StatusOff
		}
		if got[def.Code] != want {
			t.Errorf("%s: %s, want %s", def.Code, got[def.Code], want)
		}
	}
	var status string
	var version int16
	var late bool
	must(t, f.Pool.QueryRow(context.Background(), `SELECT status, format_version, built_late FROM nap_packs WHERE property_id = $1 AND business_date = $2`, f.propID, bd).Scan(&status, &version, &late))
	if status != auditpack.PackComplete || version != auditpack.FormatVersion || late {
		t.Fatalf("pack: %s v%d late %v", status, version, late)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'nap_pack.built'`) != 1 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'night_audit.completed'`) != 1 {
		t.Fatal("the audit entries of the close")
	}
	sum := f.stored(t, bd, auditpack.SecSummary).(map[string]any)
	if sum["closed_by"] != "Admin" || sum["summary"] == nil {
		t.Fatalf("summary: %v", sum)
	}
	notes := f.stored(t, bd, auditpack.SecAuditNotes).(map[string]any)
	if notes["room_charges_posted"].(float64) != 1 {
		t.Fatalf("notes: %v", notes)
	}
	if in := f.stored(t, bd, auditpack.SecInHouse).(map[string]any); len(in["rows"].([]any)) != 1 {
		t.Fatalf("in house: %v", in)
	}
	if nd := f.stored(t, bd, auditpack.SecNextDay).(map[string]any); nd["date"] != "2026-10-01" {
		t.Fatalf("next day: %v", nd)
	}
}

func TestThePackIsTheSnapshotOfTheDayNotTheLedgerOfToday(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-04")
	f.minibarCharge(t, st.Folio.ID, "c1")
	_, err := f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p1", folios.PaymentInput{Amount: "500000", PaymentMethod: "CASH"})
	must(t, err)
	day1 := f.closeDay(t)
	// the next days post more: a charge, a payment and a correction, and close
	f.minibarCharge(t, st.Folio.ID, "c2")
	_, err = f.Folios.PostPayment(f.admin, f.propID, st.Folio.ID, "p2", folios.PaymentInput{Amount: "250000", PaymentMethod: "CASH"})
	must(t, err)
	f.closeDay(t)
	f.minibarCharge(t, st.Folio.ID, "c3")

	// what the ledger says about the first day now is what the pack kept: the stored JSON equals a rebuild from the ledger
	ctx, pid := f.admin, f.propID
	r, err := f.Reports.Revenue(ctx, pid, day1, day1)
	if got := f.stored(t, day1, auditpack.SecRevenue); !reflect.DeepEqual(got, live(t, r, err)) {
		t.Errorf("revenue: stored %v, live %v", got, live(t, r, err))
	}
	tx, err := f.Reports.Tax(ctx, pid, day1, day1)
	if got := f.stored(t, day1, auditpack.SecTax); !reflect.DeepEqual(got, live(t, tx, err)) {
		t.Errorf("tax: stored %v, live %v", got, live(t, tx, err))
	}
	c, err := f.Reports.Cashier(ctx, pid, day1, day1)
	if got := f.stored(t, day1, auditpack.SecPayments); !reflect.DeepEqual(got, live(t, c, err)) {
		t.Errorf("payments: stored %v, live %v", got, live(t, c, err))
	}
	gl, err := f.Reports.GuestLedger(ctx, pid, &day1)
	if got := f.stored(t, day1, auditpack.SecGuestLedger); !reflect.DeepEqual(got, live(t, gl, err)) {
		t.Errorf("guest ledger: stored %v, live %v", got, live(t, gl, err))
	}
	dl, err := f.Reports.DepositLedger(ctx, pid, &day1)
	if got := f.stored(t, day1, auditpack.SecDepositLedger); !reflect.DeepEqual(got, live(t, dl, err)) {
		t.Errorf("deposit ledger: stored %v, live %v", got, live(t, dl, err))
	}
	// the flash report: the two rows about tomorrow are the state at the close and are not in the ledger (38 A8); everything else is
	fl, err := f.Reports.DailyFlash(ctx, pid, day1)
	want, got := live(t, fl, err).(map[string]any), f.stored(t, day1, auditpack.SecFlash).(map[string]any)
	for _, m := range []map[string]any{want, got} {
		var keep []any
		for _, row := range m["activity"].([]any) {
			if k := row.(map[string]any)["key"]; k != "arrivals_tomorrow" && k != "departures_tomorrow" {
				keep = append(keep, row)
			}
		}
		m["activity"] = keep
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("flash: stored %v, live %v", got, want)
	}
	// and the stored in-house list is the one of that night, not of today
	if in := f.stored(t, day1, auditpack.SecInHouse).(map[string]any); len(in["rows"].([]any)) != 1 {
		t.Errorf("in house: %v", in)
	}
}

func TestASectionThatFailsDoesNotFailTheRun(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.pack.Override(auditpack.SecFlash, func(context.Context) (any, error) { return nil, errors.New("the flash broke") })
	// a section that breaks the database statement itself: the savepoint takes the transaction back, the sections after it are built
	f.pack.Override(auditpack.SecTax, func(ctx context.Context) (any, error) {
		tx, err := db.Tx(ctx)
		must(t, err)
		_, err = tx.Exec(ctx, `SELECT 1 / 0`)
		return nil, err
	})
	bd := f.closeDay(t)

	got := f.statuses(t, bd)
	if got[auditpack.SecFlash] != auditpack.StatusFailed || got[auditpack.SecTax] != auditpack.StatusFailed {
		t.Fatalf("statuses: %v", got)
	}
	for _, code := range []string{auditpack.SecSummary, auditpack.SecRevenue, auditpack.SecPayments, auditpack.SecGuestLedger, auditpack.SecInHouse, auditpack.SecAuditNotes} {
		if got[code] != auditpack.StatusOK {
			t.Errorf("%s: %s", code, got[code])
		}
	}
	var status string
	var failure *string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT p.status, (SELECT error FROM nap_pack_sections WHERE pack_id = p.id AND section = 'FLASH') FROM nap_packs p WHERE p.property_id = $1`, f.propID).Scan(&status, &failure))
	if status != auditpack.PackPartial || failure == nil || *failure != "the flash broke" {
		t.Fatalf("pack %s, error %v", status, failure)
	}
	// the day is closed and the next one is open, the room night is posted
	if f.Count(t, `SELECT count(*) FROM business_days WHERE property_id = $1 AND status = 'CLOSED'`, f.propID) != 1 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'night_audit.completed'`) != 1 {
		t.Fatal("the day is not closed")
	}
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE property_id = $1 AND charge_code_id = (SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM')`, f.propID) != 1 {
		t.Fatal("the room night is not posted")
	}
}

func TestABlockerCancelsThePack(t *testing.T) {
	f := setup(t)
	// an arrival nobody checked in or marked no-show blocks the audit
	_, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d("2026-09-30"), Departure: d("2026-10-02"), Adults: 2}}})
	must(t, err)
	_, err = f.run(t)
	roomstest.Want(t, err, "NIGHT_AUDIT_BLOCKED")
	if f.Count(t, `SELECT count(*) FROM nap_packs`) != 0 || f.Count(t, `SELECT count(*) FROM nap_pack_sections`) != 0 || f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'nap_pack.built'`) != 0 {
		t.Fatal("a pack was kept of a day that did not close")
	}
	if f.Count(t, `SELECT count(*) FROM business_days WHERE property_id = $1 AND status = 'CLOSED'`, f.propID) != 0 {
		t.Fatal("the day closed")
	}
}

func TestTwoConcurrentRunsMakeOnePack(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.Clock.Set(d("2026-09-30").At(civil.MustParseTimeOfDay("21:00"), time.FixedZone("WIB", 7*3600)))
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Audit.Run(f.admin, f.propID, d("2026-09-30"))
		}()
	}
	wg.Wait()
	wins := 0
	for _, err := range errs {
		switch {
		case err == nil:
			wins++
		case apperr.IsCode(err, "NIGHT_AUDIT_IN_PROGRESS"), apperr.IsCode(err, "BUSINESS_DATE_MISMATCH"):
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if wins != 1 || f.Count(t, `SELECT count(*) FROM nap_packs WHERE property_id = $1`, f.propID) != 1 || f.Count(t, `SELECT count(*) FROM nap_pack_sections`) != len(auditpack.Catalog) {
		t.Fatalf("wins %d", wins)
	}
}

func TestTheTimeToBuildIsRecorded(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.pack.Override(auditpack.SecRevenue, func(context.Context) (any, error) {
		f.Clock.Advance(250 * time.Millisecond)
		return map[string]string{}, nil
	})
	bd := f.closeDay(t)
	var ms int32
	must(t, f.Pool.QueryRow(context.Background(), `SELECT build_ms FROM nap_packs WHERE property_id = $1 AND business_date = $2`, f.propID, bd).Scan(&ms))
	if ms != 250 {
		t.Fatalf("build_ms %d", ms)
	}
}

func TestRoomsOutOfOrderOnTheNight(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r102, "2026-10-03")
	ins := func(room int64, kind, from, to, status string) {
		t.Helper()
		_, err := f.Pool.Exec(context.Background(), `INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason, status, cancelled_at)
			VALUES ($1, $2, $3, $4, $5::date, $6::date, 'leak', $7::varchar, CASE WHEN $7::varchar = 'CANCELLED' THEN now() END)`, f.tenantID, f.propID, room, kind, from, to, status)
		must(t, err)
	}
	ins(f.r101.ID, "OOO", "2026-09-30", "2026-10-01", "ACTIVE") // the night: from the day, until the day after (half open)
	ins(f.r101.ID, "OOS", "2026-10-01", "2026-10-03", "ACTIVE") // starts the day after: not the night
	ins(f.r102.ID, "OOO", "2026-09-28", "2026-09-30", "ACTIVE") // ended the evening before the night
	ins(f.r102.ID, "OOS", "2026-09-29", "2026-10-02", "CANCELLED")
	bd := f.closeDay(t)
	got := f.stored(t, bd, auditpack.SecOutOfOrder).(map[string]any)
	rooms := got["rooms"].([]any)
	if len(rooms) != 1 || got["out_of_order"].(float64) != 1 || got["out_of_service"].(float64) != 0 {
		t.Fatalf("out of order: %v", got)
	}
	row := rooms[0].(map[string]any)
	if row["room"] != "101" || row["kind"] != "OOO" || row["from"] != "2026-09-30" || row["until"] != "2026-10-01" || row["reason"] != "leak" {
		t.Fatalf("row: %v", row)
	}
}

// fakeOutlets is a point of sale with one restaurant that sold 1,000,000 net and voided one line.
type fakeOutlets struct{}

func (fakeOutlets) ListOutlets(context.Context, int64, *bool) ([]pos.Outlet, error) {
	return []pos.Outlet{{ID: 7, Code: "REST", Name: "Restaurant", IsActive: true}, {ID: 8, Code: "OLD", Name: "Old bar", IsActive: false}}, nil
}

func (fakeOutlets) Report(_ context.Context, _, outletID int64, from, to civil.Date) (pos.OutletReport, error) {
	r := pos.OutletReport{OutletID: outletID, From: from, To: to}
	if outletID == 7 {
		r.Totals = pos.ReportTotals{Bills: 4, Net: decimal.RequireFromString("1000000"), Service: decimal.RequireFromString("100000"), Tax: decimal.RequireFromString("121000"), Total: decimal.RequireFromString("1221000")}
		r.Voids = []pos.ReportEvent{{Amount: decimal.RequireFromString("55000")}}
	}
	return r, nil
}

type plan bool

func (p plan) HasPOS(context.Context, int64) (bool, error) { return bool(p), nil }

func TestTheOutletsSection(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.pack.SetOutlets(fakeOutlets{}, plan(true))
	bd := f.closeDay(t)
	got := f.stored(t, bd, auditpack.SecOutlets).(map[string]any)
	outlets := got["outlets"].([]any)
	if len(outlets) != 1 { // the outlet that is closed and sold nothing is not on the list
		t.Fatalf("outlets: %v", got)
	}
	row := outlets[0].(map[string]any)
	if row["code"] != "REST" || row["net"] != "1000000" || row["total"] != "1221000" || row["bills"].(float64) != 4 || row["voids"].(map[string]any)["amount"] != "55000" {
		t.Fatalf("row: %v", row)
	}
	if tot := got["total"].(map[string]any); tot["net"] != "1000000" || tot["voids"].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("total: %v", tot)
	}

	// without the module in the plan the section is off
	f2 := setup(t)
	f2.stay(t, f2.r101, "2026-10-03")
	f2.pack.SetOutlets(fakeOutlets{}, plan(false))
	if s := f2.statuses(t, f2.closeDay(t)); s[auditpack.SecOutlets] != auditpack.StatusOff {
		t.Fatalf("statuses: %v", s)
	}
}
