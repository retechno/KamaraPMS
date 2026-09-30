package roomcharge_test

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func d(s string) civil.Date { return civil.MustParseDate(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	adminEmail       string
	dlx              rooms.RoomType
	r101, r102       rooms.Room
	plan, guest      int64
}

// setup: DLX rooms 101 and 102 (clean), ROOM carrying 10% service and 11% VAT on service, a BAR plan at 1,000,000.
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
	var roomCode int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&roomCode))
	svc, err := e.Billing.CreateServiceCharge(admin, p.ID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", IsActive: true})
	must(t, err)
	vat, err := e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, IsActive: true})
	must(t, err)
	_, err = e.Billing.ReplaceRules(admin, p.ID, roomCode, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	})
	must(t, err)
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, roomCode).Scan(&f.plan))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, tn.ID, p.ID).Scan(&f.guest))
	_, err = e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	return f
}

// stay books and checks in a stay of the given nights into a room.
func (f *fx) stay(t *testing.T, room rooms.Room, departure string) frontdesk.CheckInResult {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: roomstest.BD, Departure: d(departure), Adults: 2},
	}})
	must(t, err)
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	return out
}

func (f *fx) nextDay(t *testing.T) {
	t.Helper()
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	bd := f.currentBD(t)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, bd, nil, json.RawMessage(`{}`))
		return err
	}))
}

func (f *fx) currentBD(t *testing.T) civil.Date {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	return day.BusinessDate
}

func (f *fx) preview(t *testing.T) []string {
	t.Helper()
	pv, err := f.Charges.Preview(f.admin, f.propID, f.currentBD(t), nil)
	must(t, err)
	var out []string
	for _, r := range pv.Items {
		out = append(out, r.ServiceDate.String()+":"+r.Status)
	}
	return out
}

func TestPreviewWritesNothing(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-02")
	pv, err := f.Charges.Preview(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if len(pv.Items) != 1 || pv.Totals.ReadyCount != 1 || pv.Totals.ReadyTotal != "1221000" {
		t.Fatalf("preview: %+v", pv)
	}
	it := pv.Items[0]
	if it.Status != "READY" || it.RoomNumber != "101" || it.ChargeCode != "ROOM" || it.RoomRate != "1000000" || it.ServiceCharge != "100000" || it.Tax != "121000" || it.Total != "1221000" ||
		it.PriceMode != "EXCLUSIVE" || it.FolioID == nil || it.GuestName != "Guest" {
		t.Fatalf("item: %+v", it)
	}
	if f.Count(t, `SELECT count(*) FROM folio_items`) != 0 || f.Count(t, `SELECT count(*) FROM stay_charge_postings`) != 0 {
		t.Fatal("a preview writes nothing")
	}
	if _, err := f.Charges.Preview(f.admin, f.propID, d("2026-10-05"), nil); err == nil {
		t.Fatal("a stale business date is refused")
	} else {
		wantCode(t, err, "BUSINESS_DATE_MISMATCH")
	}
}

func TestPostThroughTheLedgerAndIdempotency(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-02")
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if len(res.Results) != 1 || res.Results[0].Status != "POSTED" || res.Results[0].Total != "1221000" || res.Results[0].FolioItemID == nil {
		t.Fatalf("post: %+v", res)
	}
	if res.Revalidation.Ready != 0 || len(res.Revalidation.Errors) != 0 || len(res.Revalidation.Invalid) != 0 {
		t.Fatalf("revalidation: %+v", res.Revalidation)
	}
	// the ledger: a CHARGE on the ROOM code with components, stamped with the segment and the source
	var typ, source, desc string
	var segment, stayID *int64
	var debit, service, tax string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT transaction_type, source, description, stay_room_id, stay_id, debit::text, service_charge_total::text, tax_total::text FROM folio_items`).
		Scan(&typ, &source, &desc, &segment, &stayID, &debit, &service, &tax))
	if typ != "CHARGE" || source != "ROOM_POSTING" || desc != "Room 101 - 30 Sep 2026" || segment == nil || *segment != st.StayRoom.ID || stayID == nil || *stayID != st.Stay.ID ||
		debit != "1221000.000" || service != "100000.000" || tax != "121000.000" {
		t.Fatalf("ledger item: %s %s %q %v %v %s %s %s", typ, source, desc, segment, stayID, debit, service, tax)
	}
	if f.Count(t, `SELECT count(*) FROM folio_item_components`) != 2 {
		t.Fatal("service and tax components")
	}
	var trigger, status string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT posting_trigger, status FROM stay_charge_postings`).Scan(&trigger, &status))
	if trigger != "MANUAL" || status != "POSTED" {
		t.Fatalf("register: %s %s", trigger, status)
	}
	fo, err := f.Folios.GetFolio(f.admin, f.propID, st.Folio.ID)
	must(t, err)
	if fo.Balance != "1221000" {
		t.Fatalf("folio balance %s", fo.Balance)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'room_charges.posted'`) != 1 {
		t.Fatal("one audit entry per run")
	}
	// posting again: nothing is posted twice
	again, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if again.Results[0].Status != "ALREADY_POSTED" || again.Results[0].FolioItemID == nil || *again.Results[0].FolioItemID != *res.Results[0].FolioItemID || f.Count(t, `SELECT count(*) FROM folio_items`) != 1 {
		t.Fatalf("second run: %+v", again)
	}
	if got := f.preview(t); len(got) != 1 || got[0] != "2026-09-30:ALREADY_POSTED" {
		t.Fatalf("preview after posting: %v", got)
	}
	// the manual route still refuses ROOM codes
	var roomCode int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, f.propID).Scan(&roomCode))
	_, err = f.Folios.PostCharge(f.admin, f.propID, st.Folio.ID, "", folios.ChargeInput{ChargeCodeID: roomCode, Quantity: "1", UnitPrice: ptr("1")})
	wantCode(t, err, "ROOM_CHARGE_REQUIRES_ROOM_POSTING")
}

func ptr[T any](v T) *T { return &v }

func TestMissingNightsAreCaughtUp(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.nextDay(t) // 1 Oct: the night of 30 Sep was never posted
	got := f.preview(t)
	if len(got) != 2 || got[0] != "2026-09-30:READY" || got[1] != "2026-10-01:READY" {
		t.Fatalf("missing and due: %v", got)
	}
	res, err := f.Charges.PostManual(f.admin, f.propID, d("2026-10-01"), nil)
	must(t, err)
	if res.Results[0].Status != "POSTED" || res.Results[1].Status != "POSTED" || f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`) != 2 {
		t.Fatalf("catch up: %+v", res)
	}
	// the earlier night is posted to the current business date but keeps its own service date
	var bd, sd string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT business_date::text, service_date::text FROM folio_items ORDER BY id LIMIT 1`).Scan(&bd, &sd))
	if bd != "2026-10-01" || sd != "2026-09-30" {
		t.Fatalf("dates: business %s service %s", bd, sd)
	}
}

func TestReversalMakesTheNightReadyAgain(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-02")
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	rev, err := f.Folios.Reverse(f.admin, f.propID, *res.Results[0].FolioItemID, folios.CorrectionInput{Reason: "wrong rate", Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}})
	must(t, err)
	var status string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT status FROM stay_charge_postings`).Scan(&status))
	if status != "REVERSED" {
		t.Fatalf("register: %s", status)
	}
	if got := f.preview(t); len(got) != 1 || got[0] != "2026-09-30:READY" {
		t.Fatalf("after the reversal: %v", got)
	}
	again, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if again.Results[0].Status != "POSTED" || *again.Results[0].FolioItemID == rev.Item.ID {
		t.Fatalf("reposted: %+v", again)
	}
	if f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`) != 1 || f.Count(t, `SELECT count(*) FROM stay_charge_postings`) != 2 {
		t.Fatal("one POSTED row and the REVERSED one")
	}
	fo, _ := f.Folios.GetFolio(f.admin, f.propID, st.Folio.ID)
	if fo.Balance != "1221000" {
		t.Fatalf("balance %s", fo.Balance)
	}
}

func TestRoomMoveOnTheBusinessDayChargesTheNewRoom(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101, "2026-10-03")
	// an arrival-day move: the first segment is closed the day it started, the stay continues in 102
	must(t, f.Exec(t, `UPDATE stay_rooms SET check_out_at = now(), end_business_date = start_business_date WHERE stay_id = $1`, st.Stay.ID))
	must(t, f.Exec(t, `INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date) VALUES ($1, $2, $3, $4, now(), '2026-09-30')`, f.tenantID, f.propID, st.Stay.ID, f.r102.ID))
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if res.Results[0].Status != "POSTED" {
		t.Fatalf("post: %+v", res)
	}
	var desc string
	var seg int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT description, stay_room_id FROM folio_items`).Scan(&desc, &seg))
	var newSeg int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM stay_rooms WHERE stay_id = $1 AND room_id = $2`, st.Stay.ID, f.r102.ID).Scan(&newSeg))
	if desc != "Room 102 - 30 Sep 2026" || seg != newSeg {
		t.Fatalf("the new room is charged: %q segment %d (want %d)", desc, seg, newSeg)
	}
	// a later move: the next night belongs to 101 again if the segments say so; the night already posted stays put
	f.nextDay(t)
	must(t, f.Exec(t, `UPDATE stay_rooms SET check_out_at = now(), end_business_date = '2026-10-01' WHERE stay_id = $1 AND check_out_at IS NULL`, st.Stay.ID))
	must(t, f.Exec(t, `INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date) VALUES ($1, $2, $3, $4, now(), '2026-10-01')`, f.tenantID, f.propID, st.Stay.ID, f.r101.ID))
	res, err = f.Charges.PostManual(f.admin, f.propID, d("2026-10-01"), nil)
	must(t, err)
	if res.Results[0].Status != "ALREADY_POSTED" || res.Results[1].Status != "POSTED" {
		t.Fatalf("second day: %+v", res)
	}
	must(t, f.Pool.QueryRow(context.Background(), `SELECT description FROM folio_items WHERE service_date = '2026-10-01'`).Scan(&desc))
	if desc != "Room 101 - 1 Oct 2026" {
		t.Fatalf("after the move: %q", desc)
	}
}

func TestConcurrentPostingChargesEachNightOnce(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-02")
	f.stay(t, f.r102, "2026-10-02")
	const n = 6
	var wg sync.WaitGroup
	posted := make([]int, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
			errs[i] = err
			for _, r := range res.Results {
				if r.Status == "POSTED" {
					posted[i]++
				}
			}
		}()
	}
	wg.Wait()
	total := 0
	for i := range errs {
		must(t, errs[i])
		total += posted[i]
	}
	if total != 2 || f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'CHARGE'`) != 2 || f.Count(t, `SELECT count(*) FROM stay_charge_postings WHERE status = 'POSTED'`) != 2 {
		t.Fatalf("%d nights posted by %d concurrent runs", total, n)
	}
}

func TestErrorsAreReportedAndDoNotStopTheRest(t *testing.T) {
	f := setup(t)
	a := f.stay(t, f.r101, "2026-10-02")
	b := f.stay(t, f.r102, "2026-10-02")
	must(t, f.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, a.Folio.ID))                                                // no open folio for the first stay
	must(t, f.Exec(t, `DELETE FROM reservation_room_rates WHERE reservation_room_id = (SELECT reservation_room_id FROM stays WHERE id = $1)`, b.Stay.ID)) // no nightly rate for the second
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if res.Results[0].Status != "ERROR" || res.Results[0].Reason != "NO_OPEN_FOLIO" || res.Results[1].Status != "ERROR" || res.Results[1].Reason != "MISSING_NIGHTLY_RATE" {
		t.Fatalf("errors: %+v", res.Results)
	}
	if len(res.Revalidation.Errors) != 2 || f.Count(t, `SELECT count(*) FROM folio_items`) != 0 {
		t.Fatalf("revalidation: %+v", res.Revalidation)
	}
	// a third stay in the same run is still posted
	must(t, f.Exec(t, `UPDATE folios SET status = 'OPEN', closed_at = NULL WHERE id = $1`, a.Folio.ID)) // reopened: the error clears
	res, err = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, []int64{a.Stay.ID})
	must(t, err)
	if len(res.Results) != 1 || res.Results[0].Status != "POSTED" {
		t.Fatalf("one stay by id: %+v", res)
	}
}

func TestScopePermissionsAndGuards(t *testing.T) {
	f := setup(t)
	a := f.stay(t, f.r101, "2026-10-02")
	b := f.stay(t, f.r102, "2026-10-02")
	// only the listed stays
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, []int64{b.Stay.ID})
	must(t, err)
	if len(res.Results) != 1 || res.Results[0].StayID != b.Stay.ID || f.Count(t, `SELECT count(*) FROM folio_items`) != 1 {
		t.Fatalf("scope: %+v", res)
	}
	_, err = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, []int64{99999})
	wantCode(t, err, "STAY_NOT_FOUND")
	_, err = f.Charges.PostManual(f.admin, f.propID, d("2026-10-05"), []int64{a.Stay.ID})
	wantCode(t, err, "BUSINESS_DATE_MISMATCH")
	// nightaudit.run or folio.post_charge, nothing else
	none := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Charges.Preview(none, f.propID, roomstest.BD, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Charges.PostManual(none, f.propID, roomstest.BD, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	audit := f.User(t, f.tenantID, f.propID, auth.PermNightAuditRun)
	if _, err := f.Charges.Preview(audit, f.propID, roomstest.BD, nil); err != nil {
		t.Fatalf("nightaudit.run: %v", err)
	}
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioPostCharge)
	if _, err := f.Charges.PostManual(clerk, f.propID, roomstest.BD, nil); err != nil {
		t.Fatalf("folio.post_charge: %v", err)
	}
	// another tenant
	foreign := f.Tenant(t, "XYZ")
	fctx, _ := f.AdminAccount(t, foreign.ID)
	_, err = f.Charges.Preview(fctx, f.propID, roomstest.BD, nil)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestRoomPostingNeedsARoomCodeAndAnOpenFolio(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-02")
	// a nightly row pointing at a non-ROOM code is an ERROR, not a posting (the DB also refuses such a row)
	var laundry int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'LAUNDRY'`, f.propID).Scan(&laundry))
	if err := f.Exec(t, `UPDATE reservation_room_rates SET charge_code_id = $1`, laundry); err == nil {
		t.Fatal("a nightly row cannot carry a non-ROOM charge code")
	}
	must(t, f.Exec(t, `UPDATE charge_codes SET is_active = false WHERE property_id = $1 AND code = 'ROOM'`, f.propID))
	res, err := f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	if res.Results[0].Status != "ERROR" || res.Results[0].Reason != "INVALID_CHARGE_CODE" {
		t.Fatalf("inactive code: %+v", res.Results[0])
	}
}
