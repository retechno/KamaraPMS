package folios_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/tenancy"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID       int64
	admin                  context.Context
	adminEmail             string
	folio, reservation     int64
	minibar, room, laundry int64
}

// setup: a property (IDR, no decimals) with an open folio, and MINIBAR carrying 10% service and 11% tax on service.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, adminEmail: email}
	for code, dst := range map[string]*int64{"MINIBAR": &f.minibar, "ROOM": &f.room, "LAUNDRY": &f.laundry} {
		must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = $2`, p.ID, code).Scan(dst))
	}
	svc, err := e.Billing.CreateServiceCharge(admin, p.ID, billingconfig.ServiceChargeInput{Code: "SVC", Name: "Service", Rate: "10", IsActive: true})
	must(t, err)
	vat, err := e.Billing.CreateTax(admin, p.ID, billingconfig.TaxInput{Code: "VAT", Name: "VAT", Rate: "11", TaxOnService: true, IsActive: true})
	must(t, err)
	_, err = e.Billing.ReplaceRules(admin, p.ID, f.minibar, billingconfig.RulesInput{
		Taxes: []billingconfig.TaxRuleInput{{TaxID: vat.ID, Sequence: 1}}, ServiceCharges: []billingconfig.ServiceRuleInput{{ServiceChargeID: svc.ID, Sequence: 1}},
	})
	must(t, err)
	dlx := e.RoomType(t, admin, p.ID, "DLX")
	room := e.Room(t, admin, p.ID, dlx.ID, "101")
	line := e.Line(t, tn.ID, p.ID, dlx.ID, room.ID, "2026-10-01", "2026-10-03", "CONFIRMED")
	must(t, e.Pool.QueryRow(context.Background(), `SELECT reservation_id FROM reservation_rooms WHERE id = $1`, line).Scan(&f.reservation))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'F1', $3) RETURNING id`, tn.ID, p.ID, f.reservation).Scan(&f.folio))
	return f
}

// newFolio adds an open folio on a reservation of its own (a reservation has one open folio without a stay).
func (f *fx) newFolio(t *testing.T, number string) int64 {
	t.Helper()
	var res, id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, $3, '2026-09-30', 'PHONE', 'DRAFT') RETURNING id`,
		f.tenantID, f.propID, "R-"+number).Scan(&res))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, $3, $4) RETURNING id`,
		f.tenantID, f.propID, number, res).Scan(&id))
	return id
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.adminEmail, Password: roomstest.Password}
}

func (f *fx) charge(t *testing.T, key string) folios.ItemResult {
	t.Helper()
	res, err := f.Folios.PostCharge(f.admin, f.propID, f.folio, key, folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000")})
	must(t, err)
	return res
}

func ptr[T any](v T) *T { return &v }

func (f *fx) payment(t *testing.T, key, amount string) folios.PaymentResult {
	t.Helper()
	res, err := f.Folios.PostPayment(f.admin, f.propID, f.folio, key, folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
	must(t, err)
	return res
}

func (f *fx) nextDay(t *testing.T) {
	t.Helper()
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, roomstest.BD, nil, json.RawMessage(`{}`))
		return err
	}))
}

func TestManualChargeThroughTheEngine(t *testing.T) {
	f := setup(t)
	res := f.charge(t, "c1")
	it := res.Item
	// 100,000 + 10% service = 110,000, VAT 11% on it = 12,100
	if it.TransactionType != "CHARGE" || it.Debit != "122100" || it.Credit != "0" || it.ServiceChargeTotal != "10000" || it.TaxTotal != "12100" || it.NetAmount != "100000" || it.ChargeCode != "MINIBAR" {
		t.Fatalf("item: %+v", it)
	}
	if len(it.Components) != 2 || it.Components[0].ComponentType != "SERVICE_CHARGE" || it.Components[1].ComponentType != "TAX" || it.Components[1].BaseAmount != "110000" {
		t.Fatalf("components: %+v", it.Components)
	}
	if res.FolioBalance != "122100" || it.BusinessDate != roomstest.BD || it.ServiceDate != roomstest.BD || it.CreatedBy == nil {
		t.Fatalf("result: %+v", res)
	}
	// the same key replays the stored item; a second charge is a new item
	again := f.charge(t, "c1")
	if again.Item.ID != it.ID || f.Count(t, `SELECT count(*) FROM folio_items`) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	if second := f.charge(t, "c2"); second.FolioBalance != "244200" {
		t.Fatalf("second: %+v", second)
	}
	other := f.newFolio(t, "F2")
	_, err := f.Folios.PostCharge(f.admin, f.propID, other, "c1", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("1")})
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")

	// quantity, discount and price mode
	got, err := f.Folios.PostCharge(f.admin, f.propID, f.folio, "c3", folios.ChargeInput{
		ChargeCodeID: f.minibar, Quantity: "2", UnitPrice: ptr("50000"), Discount: "10000", PriceMode: ptr("inclusive"), Description: "Two waters",
	})
	must(t, err)
	if got.Item.PriceMode != "INCLUSIVE" || got.Item.Description != "Two waters" || got.Item.Debit != "90000" || got.Item.DiscountAmount != "10000" {
		t.Fatalf("inclusive with discount: %+v", got.Item)
	}
	if got.Item.Quantity != "2" {
		t.Fatalf("quantity: %s", got.Item.Quantity)
	}
	// the ledger identity: debit - credit = net + service + tax on every item
	if n := f.Count(t, `SELECT count(*) FROM folio_items WHERE debit - credit <> net_amount + service_charge_total + tax_total`); n != 0 {
		t.Fatalf("%d unbalanced items", n)
	}
}

func TestManualChargeValidation(t *testing.T) {
	f := setup(t)
	try := func(mut func(*folios.ChargeInput)) error {
		in := folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("100000")}
		mut(&in)
		_, err := f.Folios.PostCharge(f.admin, f.propID, f.folio, "", in)
		return err
	}
	wantCode(t, try(func(in *folios.ChargeInput) { in.ChargeCodeID = f.room }), "ROOM_CHARGE_REQUIRES_ROOM_POSTING")
	wantCode(t, try(func(in *folios.ChargeInput) { in.UnitPrice = nil }), "VALIDATION_FAILED") // MINIBAR has no default price
	wantCode(t, try(func(in *folios.ChargeInput) { in.Quantity = "0" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.ChargeInput) { in.Quantity = "1.0005" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.ChargeInput) { in.UnitPrice = ptr("-5") }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.ChargeInput) { in.UnitPrice = ptr("10.5") }), "VALIDATION_FAILED") // IDR has no decimals
	wantCode(t, try(func(in *folios.ChargeInput) { in.Discount = "999999999" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.ChargeInput) { in.PriceMode = ptr("NET") }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.ChargeInput) { in.ChargeCodeID = 999999 }), "CHARGE_CODE_NOT_FOUND")
	future := roomstest.BD.AddDays(1)
	wantCode(t, try(func(in *folios.ChargeInput) { in.ServiceDate = &future }), "VALIDATION_FAILED")
	past := roomstest.BD.AddDays(-2)
	if err := try(func(in *folios.ChargeInput) { in.ServiceDate = &past }); err != nil {
		t.Fatalf("a past service date is allowed: %v", err)
	}
	must(t, f.Exec(t, `UPDATE charge_codes SET is_active = false WHERE id = $1`, f.laundry))
	wantCode(t, try(func(in *folios.ChargeInput) { in.ChargeCodeID = f.laundry }), "CHARGE_CODE_INACTIVE")

	// folio and permissions
	_, err := f.Folios.PostCharge(f.admin, f.propID, 999999, "", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("1")})
	wantCode(t, err, "FOLIO_NOT_FOUND")
	reader := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err = f.Folios.PostCharge(reader, f.propID, f.folio, "", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("1")})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := f.Folios.GetFolio(reader, f.propID, f.folio); err != nil {
		t.Fatalf("reader: %v", err)
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Folios.GetFolio(nobody, f.propID, f.folio)
	wantCode(t, err, "PERMISSION_DENIED")

	// a closed folio takes nothing
	must(t, f.Exec(t, `UPDATE folios SET status = 'CLOSED', closed_at = now() WHERE id = $1`, f.folio))
	wantCode(t, try(func(in *folios.ChargeInput) {}), "FOLIO_CLOSED")
}

func TestApprovalVerification(t *testing.T) {
	f := setup(t)
	adjust := func(ctx context.Context, key string, ap *iam.ApprovalInput) (folios.ItemResult, error) {
		return f.Folios.PostAdjustment(ctx, f.propID, f.folio, key, folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "-50000", Reason: "guest complaint", Approval: ap})
	}
	f.charge(t, "c1")
	// missing block, missing fields
	_, err := adjust(f.admin, "a0", nil)
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = adjust(f.admin, "a0", &iam.ApprovalInput{Email: f.adminEmail})
	wantCode(t, err, "APPROVAL_REQUIRED")
	// wrong password and unknown email are one generic error
	_, err = adjust(f.admin, "a0", &iam.ApprovalInput{Email: f.adminEmail, Password: "wrong password here"})
	e1 := code(t, err, "APPROVAL_INVALID_CREDENTIALS")
	_, err = adjust(f.admin, "a0", &iam.ApprovalInput{Email: "nobody@hotel.com", Password: "wrong password here"})
	e2 := code(t, err, "APPROVAL_INVALID_CREDENTIALS")
	if e1.Message != e2.Message {
		t.Fatalf("the errors differ: %q vs %q", e1.Message, e2.Message)
	}
	// a valid user without correction.approve
	clerkCtx, clerkEmail := f.Account(t, f.tenantID, f.propID, auth.PermFolioAdjust, auth.PermFolioRead)
	_, err = adjust(clerkCtx, "a0", &iam.ApprovalInput{Email: clerkEmail, Password: roomstest.Password})
	wantCode(t, err, "APPROVAL_NOT_PERMITTED") // no self-approval without the permission
	if f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'ADJUSTMENT'`) != 0 {
		t.Fatal("nothing is posted without an approval")
	}
	// the actor needs the operation's permission too
	approverCtx, approverEmail := f.Account(t, f.tenantID, f.propID, auth.PermCorrectionApprove)
	_ = approverCtx
	_, err = adjust(f.User(t, f.tenantID, f.propID, auth.PermFolioRead), "a0", &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password})
	wantCode(t, err, "PERMISSION_DENIED")

	// a separate approver
	res, err := adjust(clerkCtx, "a1", &iam.ApprovalInput{Email: approverEmail, Password: roomstest.Password})
	must(t, err)
	var approverID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, approverEmail).Scan(&approverID))
	if res.Item.ApprovedBy == nil || *res.Item.ApprovedBy != approverID || res.Item.CreatedBy == nil || *res.Item.CreatedBy == approverID {
		t.Fatalf("approver and actor: %+v", res.Item)
	}
	// self-approval: one user holds both permissions
	both, bothEmail := f.Account(t, f.tenantID, f.propID, auth.PermFolioAdjust, auth.PermCorrectionApprove)
	res2, err := adjust(both, "a2", &iam.ApprovalInput{Email: bothEmail, Password: roomstest.Password})
	must(t, err)
	if res2.Item.ApprovedBy == nil || res2.Item.CreatedBy == nil || *res2.Item.ApprovedBy != *res2.Item.CreatedBy {
		t.Fatalf("self approval: %+v", res2.Item)
	}
	// a replay returns the stored result and does not ask again
	replay, err := adjust(both, "a2", nil)
	if err != nil || replay.Item.ID != res2.Item.ID {
		t.Fatalf("replay: %v %+v", err, replay)
	}
	// the password is never stored: not in the audit log, not in the ledger
	var leaked int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE new_data::text LIKE '%' || $1 || '%' OR old_data::text LIKE '%' || $1 || '%'`, roomstest.Password).Scan(&leaked))
	if leaked != 0 {
		t.Fatal("the approver's password was written to the audit log")
	}
	var entry string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT new_data::text FROM audit_logs WHERE action = 'folio.adjustment_posted' ORDER BY id LIMIT 1`).Scan(&entry))
	if !strings.Contains(entry, "approved_by") || !strings.Contains(entry, "actor") {
		t.Fatalf("audit: %s", entry)
	}
	// tenant administrators approve everywhere (the first charge is credited in full by now: correct a second one)
	f.charge(t, "c2")
	must(t, mustErr(adjust(f.admin, "a3", f.approval())))
}

func mustErr(_ folios.ItemResult, err error) error { return err }

func TestApprovalIsRateLimited(t *testing.T) {
	f := setup(t)
	for i := 0; i < 5; i++ {
		_, err := f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "1", Reason: "x", Approval: &iam.ApprovalInput{Email: f.adminEmail, Password: "wrong password " + string(rune('a'+i))}})
		wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	}
	// even the right password is refused for a while, like at login
	_, err := f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "1", Reason: "x", Approval: f.approval()})
	wantCode(t, err, "TOO_MANY_ATTEMPTS")
}

func TestAdjustmentSignAndValidation(t *testing.T) {
	f := setup(t)
	f.charge(t, "c1")
	neg, err := f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "a1", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "-100000", Reason: "wrong price", Approval: f.approval()})
	must(t, err)
	it := neg.Item
	if it.TransactionType != "ADJUSTMENT" || it.Credit != "122100" || it.Debit != "0" || it.NetAmount != "-100000" || it.ServiceChargeTotal != "-10000" || it.TaxTotal != "-12100" || it.Reason != "wrong price" {
		t.Fatalf("negative adjustment: %+v", it)
	}
	if neg.FolioBalance != "0" || it.Components[1].Amount != "-12100" {
		t.Fatalf("balance and components: %+v", neg)
	}
	// the minibar was credited in full: nothing is left to correct on it, and a new charge is a charge
	_, err = f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "a2x", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "5000", Reason: "forgotten", Approval: f.approval()})
	wantCode(t, err, "ADJUSTMENT_NOTHING_POSTED")
	// a laundry charge posted first can then be corrected upwards
	lc, err := f.Folios.PostCharge(f.admin, f.propID, f.folio, "c-laundry", folios.ChargeInput{ChargeCodeID: f.laundry, Quantity: "1", UnitPrice: ptr("20000")})
	must(t, err)
	pos, err := f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "a2", folios.AdjustmentInput{ChargeCodeID: f.laundry, Amount: "5000", Reason: "under-charged", Approval: f.approval(), RelatedItemID: &lc.Item.ID})
	must(t, err)
	if pos.Item.Debit != "5000" || pos.FolioBalance != "25000" {
		t.Fatalf("positive: %+v", pos)
	}
	// a ROOM code is adjustable only once a room charge is posted on the folio (the night audit posts it)
	_, err = f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "a3", folios.AdjustmentInput{ChargeCodeID: f.room, Amount: "1000", Reason: "rate fix", Approval: f.approval()})
	wantCode(t, err, "ADJUSTMENT_NOTHING_POSTED")
	try := func(mut func(*folios.AdjustmentInput)) error {
		in := folios.AdjustmentInput{ChargeCodeID: f.laundry, Amount: "100", Reason: "r", Approval: f.approval()}
		mut(&in)
		_, err := f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "", in)
		return err
	}
	wantCode(t, try(func(in *folios.AdjustmentInput) { in.Amount = "0" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.AdjustmentInput) { in.Amount = "abc" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.AdjustmentInput) { in.Reason = "  " }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.AdjustmentInput) { in.RelatedItemID = ptr(int64(999999)) }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.AdjustmentInput) { in.ChargeCodeID = 0 }), "VALIDATION_FAILED")
}

func TestSameDayReversal(t *testing.T) {
	f := setup(t)
	it := f.charge(t, "c1").Item
	// approval and reason first
	_, err := f.Folios.Reverse(f.admin, f.propID, it.ID, folios.CorrectionInput{Reason: "posted twice"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.Folios.Reverse(f.admin, f.propID, it.ID, folios.CorrectionInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	rev, err := f.Folios.Reverse(f.admin, f.propID, it.ID, folios.CorrectionInput{Reason: "posted twice", Approval: f.approval()})
	must(t, err)
	r := rev.Item
	if r.TransactionType != "REVERSAL" || r.Credit != "122100" || r.Debit != "0" || r.NetAmount != "-100000" || r.ServiceChargeTotal != "-10000" || r.TaxTotal != "-12100" ||
		r.ReversesItemID == nil || *r.ReversesItemID != it.ID || r.ApprovedBy == nil || r.Reason != "posted twice" {
		t.Fatalf("reversal: %+v", r)
	}
	if len(r.Components) != 2 || r.Components[0].Amount != "-10000" || rev.FolioBalance != "0" {
		t.Fatalf("components/balance: %+v", rev)
	}
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	if len(fo.Items) != 2 || fo.Items[0].ReversedByItemID == nil || *fo.Items[0].ReversedByItemID != r.ID || fo.Totals.Debit != "122100" || fo.Totals.Credit != "122100" {
		t.Fatalf("folio: %+v", fo)
	}
	// once only, and a reversal cannot be reversed
	_, err = f.Folios.Reverse(f.admin, f.propID, it.ID, folios.CorrectionInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "ALREADY_REVERSED")
	_, err = f.Folios.Reverse(f.admin, f.propID, r.ID, folios.CorrectionInput{Reason: "undo", Approval: f.approval()})
	wantCode(t, err, "ITEM_NOT_REVERSIBLE")
	_, err = f.Folios.Reverse(f.admin, f.propID, 999999, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "FOLIO_ITEM_NOT_FOUND")
	// a payment is voided, not reversed
	pay := f.payment(t, "p1", "1000")
	_, err = f.Folios.Reverse(f.admin, f.propID, pay.FolioItem.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "USE_PAYMENT_CORRECTION")
	// permission of the actor
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err = f.Folios.Reverse(clerk, f.propID, it.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")

	// after the day changes the item can only be corrected with an adjustment
	next := f.charge(t, "c2").Item
	f.nextDay(t)
	_, err = f.Folios.Reverse(f.admin, f.propID, next.ID, folios.CorrectionInput{Reason: "late", Approval: f.approval()})
	wantCode(t, err, "CORRECTION_REQUIRES_ADJUSTMENT")
	adj, err := f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "a1", folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "-100000", Reason: "late fix", Approval: f.approval()})
	must(t, err)
	if adj.Item.BusinessDate != roomstest.BD.AddDays(1) {
		t.Fatalf("an adjustment posts on the current business date: %+v", adj.Item)
	}
}

func TestReversalFlipsThePostingRegister(t *testing.T) {
	f := setup(t)
	// a ledger item (a charge here; night audit posts room charges) with a register row, as night audit will write it
	adj := f.charge(t, "c1")
	var typeID, roomID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT room_type_id, id FROM rooms LIMIT 1`).Scan(&typeID, &roomID))
	stay := f.Stay(t, f.tenantID, f.propID, typeID, roomID, "2026-09-30", "2026-10-02")
	var segment int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM stay_rooms WHERE stay_id = $1`, stay).Scan(&segment))
	must(t, f.Exec(t, `INSERT INTO stay_charge_postings (tenant_id, property_id, stay_id, stay_room_id, service_date, charge_source, charge_code_id, folio_item_id, business_date, posting_trigger)
		VALUES ($1, $2, $3, $4, '2026-09-30', 'ROOM_NIGHT', $5, $6, '2026-09-30', 'MANUAL')`, f.tenantID, f.propID, stay, segment, f.room, adj.Item.ID))
	rev, err := f.Folios.Reverse(f.admin, f.propID, adj.Item.ID, folios.CorrectionInput{Reason: "wrong night", Approval: f.approval()})
	must(t, err)
	var status string
	var revID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT status, reversal_item_id FROM stay_charge_postings WHERE folio_item_id = $1`, adj.Item.ID).Scan(&status, &revID))
	if status != "REVERSED" || revID != rev.Item.ID {
		t.Fatalf("register: %s %d (want REVERSED %d)", status, revID, rev.Item.ID)
	}
}

func TestPaymentPostReplayAndValidation(t *testing.T) {
	f := setup(t)
	f.charge(t, "c1")
	res := f.payment(t, "p1", "100000")
	pay := res.Payment
	if pay.PaymentType != "PAYMENT" || pay.Status != "POSTED" || pay.Amount != "100000" || pay.PaymentNumber == "" || pay.BusinessDate != roomstest.BD ||
		pay.Refundable == nil || *pay.Refundable != "100000" || pay.ApprovedBy != nil {
		t.Fatalf("payment: %+v", pay)
	}
	if res.FolioItem.TransactionType != "PAYMENT" || res.FolioItem.Credit != "100000" || res.FolioItem.PaymentID == nil || res.FolioBalance != "22100" {
		t.Fatalf("entry: %+v", res)
	}
	again := f.payment(t, "p1", "100000")
	if again.Payment.ID != pay.ID || f.Count(t, `SELECT count(*) FROM payments`) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	try := func(mut func(*folios.PaymentInput)) error {
		in := folios.PaymentInput{Amount: "1000", PaymentMethod: "CARD", ReferenceNumber: "AUTH-1"}
		mut(&in)
		_, err := f.Folios.PostPayment(f.admin, f.propID, f.folio, "", in)
		return err
	}
	wantCode(t, try(func(in *folios.PaymentInput) { in.Amount = "0" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.PaymentInput) { in.Amount = "-5" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.PaymentInput) { in.Amount = "10.5" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.PaymentInput) { in.PaymentMethod = "BITCOIN" }), "VALIDATION_FAILED")
	wantCode(t, try(func(in *folios.PaymentInput) { in.ReferenceNumber = strings.Repeat("x", 101) }), "VALIDATION_FAILED")
	must(t, try(func(in *folios.PaymentInput) {}))
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.Folios.PostPayment(clerk, f.propID, f.folio, "", folios.PaymentInput{Amount: "1", PaymentMethod: "CASH"})
	wantCode(t, err, "PERMISSION_DENIED")

	// a retry storm with one key makes one payment
	var wg sync.WaitGroup
	ids := make([]int64, 6)
	errs := make([]error, 6)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := f.Folios.PostPayment(f.admin, f.propID, f.folio, "storm", folios.PaymentInput{Amount: "2000", PaymentMethod: "CASH"})
			ids[i], errs[i] = r.Payment.ID, err
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("request %d: %v id %d (want %d)", i, errs[i], ids[i], ids[0])
		}
	}
	if got := f.Count(t, `SELECT count(*) FROM payments WHERE idempotency_key = 'storm'`); got != 1 {
		t.Fatalf("%d payments for one key", got)
	}
}

func TestDepositCreatesAndReusesTheUnlinkedFolio(t *testing.T) {
	f := setup(t)
	var res2 int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, 'D1', '2026-09-30', 'PHONE', 'DRAFT') RETURNING id`, f.tenantID, f.propID).Scan(&res2))
	in := folios.PaymentInput{Amount: "300000", PaymentMethod: "BANK_TRANSFER", ReferenceNumber: "TRF-9"}
	first, err := f.Folios.Deposit(f.admin, f.propID, res2, "d1", in)
	must(t, err)
	second, err := f.Folios.Deposit(f.admin, f.propID, res2, "d2", in)
	must(t, err)
	if first.Payment.FolioID != second.Payment.FolioID || second.FolioBalance != "-600000" {
		t.Fatalf("one folio for deposits: %+v %+v", first.Payment, second)
	}
	if f.Count(t, `SELECT count(*) FROM folios WHERE reservation_id = $1 AND stay_id IS NULL`, res2) != 1 {
		t.Fatal("exactly one unlinked folio")
	}
	// deposits made at the same time converge on one folio too
	var res3 int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source, status) VALUES ($1, $2, 'D2', '2026-09-30', 'PHONE', 'DRAFT') RETURNING id`, f.tenantID, f.propID).Scan(&res3))
	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Folios.Deposit(f.admin, f.propID, res3, "race-"+string(rune('a'+i)), in)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		must(t, err)
	}
	if f.Count(t, `SELECT count(*) FROM folios WHERE reservation_id = $1`, res3) != 1 || f.Count(t, `SELECT count(*) FROM payments p JOIN folios f ON f.id = p.folio_id WHERE f.reservation_id = $1`, res3) != 5 {
		t.Fatal("five deposits, one folio")
	}
	// cancelled and unknown reservations
	must(t, f.Exec(t, `UPDATE reservations SET status = 'CANCELLED', cancelled_at = now(), guest_id = (SELECT id FROM guests LIMIT 1) WHERE id = $1`, res2))
	_, err = f.Folios.Deposit(f.admin, f.propID, res2, "d3", in)
	wantCode(t, err, "RESERVATION_NOT_OPEN")
	_, err = f.Folios.Deposit(f.admin, f.propID, 999999, "d4", in)
	wantCode(t, err, "RESERVATION_NOT_FOUND")
}

func TestVoid(t *testing.T) {
	f := setup(t)
	pay := f.payment(t, "p1", "100000")
	_, err := f.Folios.Void(f.admin, f.propID, pay.Payment.ID, folios.CorrectionInput{Reason: "wrong folio"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.Folios.Void(f.admin, f.propID, pay.Payment.ID, folios.CorrectionInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	res, err := f.Folios.Void(f.admin, f.propID, pay.Payment.ID, folios.CorrectionInput{Reason: "wrong folio", Approval: f.approval()})
	must(t, err)
	if res.Payment.Status != "VOIDED" || res.Payment.VoidReason != "wrong folio" || res.Payment.ApprovedBy == nil || res.Payment.VoidedAt == nil || res.FolioBalance != "0" || res.Payment.Refundable != nil {
		t.Fatalf("void: %+v", res)
	}
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	if len(fo.Items) != 2 || fo.Items[1].TransactionType != "REVERSAL" || fo.Items[1].Debit != "100000" || fo.Items[0].ReversedByItemID == nil {
		t.Fatalf("ledger after void: %+v", fo.Items)
	}
	_, err = f.Folios.Void(f.admin, f.propID, pay.Payment.ID, folios.CorrectionInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_ALREADY_VOIDED")
	// a voided payment cannot be refunded
	_, err = f.Folios.Refund(f.admin, f.propID, pay.Payment.ID, "r1", folios.RefundInput{Amount: "1", Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_NOT_REFUNDABLE")
	// a payment with a refund cannot be voided; a refund cannot be voided
	p2 := f.payment(t, "p2", "50000")
	refund, err := f.Folios.Refund(f.admin, f.propID, p2.Payment.ID, "r2", folios.RefundInput{Amount: "10000", Reason: "part", Approval: f.approval()})
	must(t, err)
	_, err = f.Folios.Void(f.admin, f.propID, p2.Payment.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_HAS_REFUNDS")
	_, err = f.Folios.Void(f.admin, f.propID, refund.Payment.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_NOT_VOIDABLE")
	_, err = f.Folios.Void(f.admin, f.propID, 999999, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_NOT_FOUND")
	// permission of the actor
	clerk := f.User(t, f.tenantID, f.propID, auth.PermPaymentPost)
	_, err = f.Folios.Void(clerk, f.propID, p2.Payment.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	// on a later day a payment is refunded, not voided
	p3 := f.payment(t, "p3", "70000")
	f.nextDay(t)
	_, err = f.Folios.Void(f.admin, f.propID, p3.Payment.ID, folios.CorrectionInput{Reason: "late", Approval: f.approval()})
	wantCode(t, err, "CORRECTION_REQUIRES_ADJUSTMENT")
	late, err := f.Folios.Refund(f.admin, f.propID, p3.Payment.ID, "r3", folios.RefundInput{Amount: "70000", Reason: "late", Approval: f.approval()})
	must(t, err)
	if late.Payment.BusinessDate != roomstest.BD.AddDays(1) {
		t.Fatalf("a refund posts on the current business date: %+v", late.Payment)
	}
}

func TestRefundRules(t *testing.T) {
	f := setup(t)
	pay := f.payment(t, "p1", "100000")
	try := func(key, amount string) (folios.PaymentResult, error) {
		return f.Folios.Refund(f.admin, f.propID, pay.Payment.ID, key, folios.RefundInput{Amount: amount, Reason: "goodwill", Approval: f.approval()})
	}
	_, err := f.Folios.Refund(f.admin, f.propID, pay.Payment.ID, "r0", folios.RefundInput{Amount: "1000", Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	wantCode(t, mustErr2(try("r0", "0")), "VALIDATION_FAILED")
	wantCode(t, mustErr2(try("r0", "5.5")), "VALIDATION_FAILED")
	_, err = f.Folios.Refund(f.admin, f.propID, pay.Payment.ID, "r0", folios.RefundInput{Amount: "1000", Reason: "", Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	res, err := try("r1", "60000")
	must(t, err)
	if res.Payment.PaymentType != "REFUND" || res.Payment.RefundOfPaymentID == nil || *res.Payment.RefundOfPaymentID != pay.Payment.ID || res.Payment.PaymentMethod != "CASH" ||
		res.Payment.ApprovedBy == nil || res.FolioItem.TransactionType != "REFUND" || res.FolioItem.Debit != "60000" || res.FolioBalance != "-40000" {
		t.Fatalf("refund: %+v", res)
	}
	// replay, over-refund with what is left, then the rest
	if again, err := try("r1", "60000"); err != nil || again.Payment.ID != res.Payment.ID {
		t.Fatalf("replay: %v", err)
	}
	e := code(t, mustErr2(try("r2", "50000")), "REFUND_EXCEEDS_PAYMENT")
	if e.Context["refundable"] != "40000" {
		t.Fatalf("context: %v", e.Context)
	}
	must(t, mustErr2(try("r3", "40000")))
	wantCode(t, mustErr2(try("r4", "1")), "REFUND_EXCEEDS_PAYMENT")
	other := f.newFolio(t, "F2")
	_ = other
	clerk := f.User(t, f.tenantID, f.propID, auth.PermPaymentVoid)
	_, err = f.Folios.Refund(clerk, f.propID, pay.Payment.ID, "r5", folios.RefundInput{Amount: "1", Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Folios.Refund(f.admin, f.propID, 999999, "r6", folios.RefundInput{Amount: "1", Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_NOT_FOUND")
}

func mustErr2(_ folios.PaymentResult, err error) error { return err }

func TestConcurrentRefundsCannotExceedThePayment(t *testing.T) {
	f := setup(t)
	pay := f.payment(t, "p1", "100000")
	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Folios.Refund(f.admin, f.propID, pay.Payment.ID, "race-"+string(rune('a'+i)), folios.RefundInput{Amount: "40000", Reason: "race", Approval: f.approval()})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "REFUND_EXCEEDS_PAYMENT")
		}
	}
	var refunded string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount), 0)::text FROM payments WHERE refund_of_payment_id = $1`, pay.Payment.ID).Scan(&refunded))
	if ok != 2 || refunded != "80000.000" {
		t.Fatalf("%d refunds succeeded, %s refunded of 100000", ok, refunded)
	}
}

func TestCashierList(t *testing.T) {
	f := setup(t)
	f.payment(t, "p1", "100000")
	card, err := f.Folios.PostPayment(f.admin, f.propID, f.folio, "p2", folios.PaymentInput{Amount: "50000", PaymentMethod: "CARD"})
	must(t, err)
	_, err = f.Folios.Refund(f.admin, f.propID, card.Payment.ID, "r1", folios.RefundInput{Amount: "20000", Reason: "x", Approval: f.approval()}) // a card payment is refunded in cash
	must(t, err)
	gone := f.payment(t, "p3", "7000")
	_, err = f.Folios.Void(f.admin, f.propID, gone.Payment.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
	must(t, err)
	bd := roomstest.BD
	list, err := f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{BusinessDate: &bd}, 0, 10)
	must(t, err)
	if len(list.Data) != 4 || list.Data[0].ID < list.Data[1].ID {
		t.Fatalf("list: %+v", list.Data)
	}
	totals := map[string]folios.MethodTotal{}
	for _, tt := range list.Totals {
		totals[tt.PaymentMethod] = tt
	}
	if totals["CASH"].Paid != "100000" || totals["CASH"].Refunded != "20000" || totals["CASH"].Net != "80000" || totals["CARD"].Paid != "50000" || totals["CARD"].Net != "50000" {
		t.Fatalf("totals (a voided payment is not counted): %+v", list.Totals)
	}
	cards, err := f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{Method: "CARD"}, 0, 10)
	must(t, err)
	if len(cards.Data) != 1 || len(cards.Totals) != 0 {
		t.Fatalf("filter: %+v", cards)
	}
	_, err = f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{Method: "X"}, 0, 10)
	wantCode(t, err, "VALIDATION_FAILED")
	page, err := f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{}, list.Data[1].ID, 10)
	must(t, err)
	if len(page.Data) != 2 {
		t.Fatalf("keyset: %+v", page.Data)
	}
}

func TestCloseFolio(t *testing.T) {
	f := setup(t)
	f.charge(t, "c1")
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	_, err = f.Folios.CloseFolio(f.admin, f.propID, f.folio, fo.Version)
	wantCode(t, err, "FOLIO_BALANCE_NOT_ZERO")
	pay := f.payment(t, "p1", "122100")
	_ = pay
	_, err = f.Folios.CloseFolio(f.admin, f.propID, f.folio, fo.Version) // stale: postings bump the version
	wantCode(t, err, "VERSION_CONFLICT")
	fo, err = f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	if fo.Balance != "0" {
		t.Fatalf("balance %s", fo.Balance)
	}
	closed, err := f.Folios.CloseFolio(f.admin, f.propID, f.folio, fo.Version)
	must(t, err)
	if closed.Status != "CLOSED" || closed.ClosedAt == nil {
		t.Fatalf("closed: %+v", closed)
	}
	_, err = f.Folios.CloseFolio(f.admin, f.propID, f.folio, closed.Version)
	wantCode(t, err, "FOLIO_CLOSED")
	// a folio of an in-house stay closes with the check-out
	var typeID, roomID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT room_type_id, id FROM rooms LIMIT 1`).Scan(&typeID, &roomID))
	stay := f.Stay(t, f.tenantID, f.propID, typeID, roomID, "2026-09-30", "2026-10-02")
	linked := f.newFolio(t, "F9")
	must(t, f.Exec(t, `UPDATE folios SET stay_id = $2 WHERE id = $1`, linked, stay))
	lf, err := f.Folios.GetFolio(f.admin, f.propID, linked)
	must(t, err)
	_, err = f.Folios.CloseFolio(f.admin, f.propID, linked, lf.Version)
	wantCode(t, err, "FOLIO_LINKED_TO_OPEN_STAY")
}

func TestListFoliosAndIsolation(t *testing.T) {
	f := setup(t)
	f.charge(t, "c1")
	f.newFolio(t, "F2")
	list, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{}, 0, 10)
	must(t, err)
	if len(list) != 2 || list[0].Balance != "122100" || list[1].Balance != "0" {
		t.Fatalf("list: %+v", list)
	}
	byRes, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{ReservationID: &f.reservation}, 0, 10)
	must(t, err)
	if len(byRes) != 1 || byRes[0].ID != f.folio {
		t.Fatalf("by reservation: %+v", byRes)
	}
	open, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{Status: "CLOSED"}, 0, 10)
	must(t, err)
	if len(open) != 0 {
		t.Fatalf("closed: %+v", open)
	}
	// another tenant sees neither the property nor the folio
	foreign := f.Tenant(t, "XYZ")
	fp := f.Property(t, foreign.ID, "FOR")
	fctx, _ := f.AdminAccount(t, foreign.ID)
	_, err = f.Folios.GetFolio(fctx, f.propID, f.folio)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Folios.GetFolio(fctx, fp.ID, f.folio)
	wantCode(t, err, "FOLIO_NOT_FOUND")
	_, err = f.Folios.PostPayment(fctx, fp.ID, f.folio, "", folios.PaymentInput{Amount: "1", PaymentMethod: "CASH"})
	wantCode(t, err, "FOLIO_NOT_FOUND")
	_, err = f.Folios.Reverse(fctx, fp.ID, 1, folios.CorrectionInput{Reason: "x", Approval: nil})
	wantCode(t, err, "APPROVAL_REQUIRED")
}

func TestLedgerIsAppendOnlyAndConstrained(t *testing.T) {
	f := setup(t)
	it := f.charge(t, "c1").Item
	pay := f.payment(t, "p1", "1000")
	for _, sql := range []string{
		`UPDATE folio_items SET description = 'x'`,
		`DELETE FROM folio_items`,
		`UPDATE folio_item_components SET amount = 0`,
		`DELETE FROM folio_item_components`,
		`DELETE FROM payments`,
		`UPDATE payments SET amount = 1`,
	} {
		if err := f.Exec(t, sql); err == nil {
			t.Errorf("%s was allowed", sql)
		}
	}
	// a correction row without an approver is refused by the database, whoever writes it
	if err := f.Exec(t, `INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, reverses_item_id, description, quantity, unit_price, price_mode, base_amount, net_amount, credit, source, reason)
		SELECT tenant_id, property_id, folio_id, business_date, service_date, 'REVERSAL', id, 'x', 1, 1, 'EXCLUSIVE', -1, -1, 0, 'MANUAL', 'r' FROM folio_items WHERE id = $1`, it.ID); err == nil {
		t.Error("a reversal without approved_by was accepted")
	}
	if err := f.Exec(t, `UPDATE payments SET status = 'VOIDED', voided_at = now(), void_reason = 'x' WHERE id = $1`, pay.Payment.ID); err == nil {
		t.Error("a void without approved_by was accepted")
	}
	// an item whose totals do not match its components fails at commit
	err := f.Exec(t, `INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id, description, quantity, unit_price, price_mode, base_amount, net_amount, service_charge_total, debit, source)
		VALUES ($1, $2, $3, '2026-09-30', '2026-09-30', 'CHARGE', $4, 'x', 1, 100, 'EXCLUSIVE', 100, 100, 10, 110, 'MANUAL')`, f.tenantID, f.propID, f.folio, f.minibar)
	if err == nil {
		t.Error("an item with an unexplained service charge was accepted")
	}
	_ = civil.Date{}
}

// A refund leaves by the methods the property allows (cash only by default), whatever the payment came in by.
func TestRefundMethodFollowsTheProperty(t *testing.T) {
	f := setup(t)
	pay, err := f.Folios.PostPayment(f.admin, f.propID, f.folio, "tp", folios.PaymentInput{Amount: "100000", PaymentMethod: "BANK_TRANSFER"})
	must(t, err)
	refund := func(key, method string) (folios.PaymentResult, error) {
		return f.Folios.Refund(f.admin, f.propID, pay.Payment.ID, key, folios.RefundInput{Amount: "10000", PaymentMethod: method, Reason: "goodwill", Approval: f.approval()})
	}
	// default: cash only; no method given means cash, a transfer is refused
	res, err := refund("m1", "")
	must(t, err)
	if res.Payment.PaymentMethod != "CASH" {
		t.Fatalf("default method: %s", res.Payment.PaymentMethod)
	}
	wantCode(t, mustErr2(refund("m2", "BANK_TRANSFER")), "VALIDATION_FAILED")
	wantCode(t, mustErr2(refund("m3", "BITCOIN")), "VALIDATION_FAILED")
	// the property allows transfers too: the payment's own method becomes the default again
	_, err = f.Tenancy.UpdateProperty(f.admin, f.propID, tenancy.PropertyPatch{RefundMethods: []string{"CASH", "BANK_TRANSFER"}})
	must(t, err)
	res, err = refund("m4", "")
	must(t, err)
	if res.Payment.PaymentMethod != "BANK_TRANSFER" {
		t.Fatalf("default method: %s", res.Payment.PaymentMethod)
	}
	res, err = refund("m5", "CASH")
	must(t, err)
	if res.Payment.PaymentMethod != "CASH" {
		t.Fatalf("method: %s", res.Payment.PaymentMethod)
	}
	wantCode(t, mustErr2(refund("m6", "CARD")), "VALIDATION_FAILED")
	// the setting itself is validated
	_, err = f.Tenancy.UpdateProperty(f.admin, f.propID, tenancy.PropertyPatch{RefundMethods: []string{"CASH", "CASH"}})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Tenancy.UpdateProperty(f.admin, f.propID, tenancy.PropertyPatch{RefundMethods: []string{}})
	wantCode(t, err, "VALIDATION_FAILED")
}

// An adjustment corrects what is already posted on the folio: its charge code needs a posted net above zero, and a
// credit cannot take it below zero.
func TestAdjustmentCorrectsOnlyWhatIsPosted(t *testing.T) {
	f := setup(t)
	adjust := func(key, code string, codeID int64, amount string) (folios.ItemResult, error) {
		_ = code
		return f.Folios.PostAdjustment(f.admin, f.propID, f.folio, key, folios.AdjustmentInput{ChargeCodeID: codeID, Amount: amount, Reason: "fix", Approval: f.approval()})
	}
	// nothing posted yet: neither a credit nor a debit
	_, err := adjust("n1", "minibar", f.minibar, "-1000")
	wantCode(t, err, "ADJUSTMENT_NOTHING_POSTED")
	_, err = adjust("n2", "minibar", f.minibar, "1000")
	e := code(t, err, "ADJUSTMENT_NOTHING_POSTED")
	if e.Context["charge_code_id"] != f.minibar {
		t.Fatalf("context: %v", e.Context)
	}

	f.charge(t, "c1") // 100,000 net on the minibar
	// another code is still not adjustable
	_, err = adjust("n3", "laundry", f.laundry, "-1000")
	wantCode(t, err, "ADJUSTMENT_NOTHING_POSTED")
	// a credit within what is posted, and the rest of it, but not more
	_, err = adjust("x1", "minibar", f.minibar, "-60000")
	must(t, err)
	e = code(t, mustErr3(adjust("x2", "minibar", f.minibar, "-50000")), "ADJUSTMENT_EXCEEDS_POSTED")
	if e.Context["posted"] != "40000" {
		t.Fatalf("context: %v", e.Context)
	}
	_, err = adjust("x3", "minibar", f.minibar, "-40000")
	must(t, err)
	_, err = adjust("x4", "minibar", f.minibar, "-1")
	wantCode(t, err, "ADJUSTMENT_NOTHING_POSTED") // credited in full

	// a reversed charge nets out: nothing is left to correct
	lc, err := f.Folios.PostCharge(f.admin, f.propID, f.folio, "c-laundry", folios.ChargeInput{ChargeCodeID: f.laundry, Quantity: "1", UnitPrice: ptr("20000")})
	must(t, err)
	_, err = adjust("y1", "laundry", f.laundry, "1000")
	must(t, err) // an increase of what is posted
	_, err = f.Folios.Reverse(f.admin, f.propID, lc.Item.ID, folios.CorrectionInput{Reason: "twice", Approval: f.approval()})
	must(t, err)
	_, err = adjust("y2", "laundry", f.laundry, "-500") // 21,000 posted, 20,000 reversed: 1,000 left
	must(t, err)
	_, err = adjust("y3", "laundry", f.laundry, "-1000")
	wantCode(t, err, "ADJUSTMENT_EXCEEDS_POSTED")
}

func mustErr3(_ folios.ItemResult, err error) error { return err }

// Two credits that fit one after the other must not both be posted: the folio row is locked.
func TestConcurrentAdjustmentsCannotCreditMoreThanIsPosted(t *testing.T) {
	f := setup(t)
	f.charge(t, "c1") // 100,000 posted
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Folios.PostAdjustment(f.admin, f.propID, f.folio, "race-"+string(rune('a'+i)), folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: "-40000", Reason: "race", Approval: f.approval()})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "ADJUSTMENT_EXCEEDS_POSTED")
		}
	}
	if ok != 2 { // 40,000 + 40,000 fit in 100,000, a third does not
		t.Fatalf("%d adjustments were posted, want 2", ok)
	}
	var net string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(net_amount), 0)::text FROM folio_items WHERE folio_id = $1 AND charge_code_id = $2`, f.folio, f.minibar).Scan(&net))
	if net != "20000.000" && net != "20000" {
		t.Fatalf("net on the minibar is %s", net)
	}
}
