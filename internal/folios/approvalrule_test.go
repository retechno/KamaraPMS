package folios_test

import (
	"context"
	"strconv"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// The rules of approval (docs/architecture/32-distinct-approver.md) through the corrections of a folio: adjustment, reversal and void,
// refund and transfer.

type approvalActors struct {
	both     context.Context // acts and holds the approver permission
	bothIn   *iam.ApprovalInput
	bothID   int64
	otherIn  *iam.ApprovalInput // another approver
	otherID  int64
	adminCtx context.Context
	adminIn  *iam.ApprovalInput
}

func (f *fx) actors(t *testing.T) approvalActors {
	t.Helper()
	perms := []auth.Permission{auth.PermFolioRead, auth.PermFolioAdjust, auth.PermFolioReverse, auth.PermPaymentVoid, auth.PermPaymentRefund, auth.PermPaymentPost, auth.PermCorrectionApprove}
	both, bothEmail := f.Account(t, f.tenantID, f.propID, perms...)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermCorrectionApprove)
	idOf := func(email string) (id int64) {
		must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, email).Scan(&id))
		return
	}
	return approvalActors{
		both: both, bothIn: &iam.ApprovalInput{Email: bothEmail, Password: roomstest.Password}, bothID: idOf(bothEmail),
		otherIn: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}, otherID: idOf(otherEmail),
		adminCtx: f.admin, adminIn: f.approval(),
	}
}

func marked(t *testing.T, f *fx, action string) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = $1 AND new_data->>'self_approved_by_admin' = 'true'`, action)
}

func TestTheRuleOfAnAdjustmentByKindAndAmount(t *testing.T) {
	f := setup(t)
	a := f.actors(t)
	f.charge(t, "c1")
	adjust := func(ctx context.Context, key, amount string, in *iam.ApprovalInput) (folios.ItemResult, error) {
		return f.Folios.PostAdjustment(ctx, f.propID, f.folio, key, folios.AdjustmentInput{ChargeCodeID: f.minibar, Amount: amount, Reason: "complaint", Approval: in})
	}
	// no rule: the person who acts approves their own
	res, err := adjust(a.both, "a0", "-1000", a.bothIn)
	must(t, err)
	if res.Item.ApprovedBy == nil || *res.Item.ApprovedBy != a.bothID {
		t.Fatalf("free: %+v", res.Item)
	}

	// DISTINCT_ABOVE 30,000: a sign does not matter, the limit itself is allowed
	f.SetRule(t, f.tenantID, f.propID, "FOLIO_ADJUSTMENT", "DISTINCT_ABOVE", "30000")
	for i, amount := range []string{"-29999", "30000", "-30000"} { // a sign does not matter, the limit itself is allowed
		_, err := adjust(a.both, "a-"+strconv.Itoa(i), amount, a.bothIn)
		must(t, err)
	}
	before := f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'ADJUSTMENT'`)
	_, err = adjust(a.both, "a-big", "-30001", a.bothIn)
	e := code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "FOLIO_ADJUSTMENT" || e.Context["rule"] != "DISTINCT_ABOVE" || e.Context["threshold"] != "30000" || e.Context["amount"] != "30001" {
		t.Fatalf("the context: %v", e.Context)
	}
	if after := f.Count(t, `SELECT count(*) FROM folio_items WHERE transaction_type = 'ADJUSTMENT'`); after != before {
		t.Fatal("a refused approval posts nothing")
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'folio.adjustment_posted' AND new_data->>'self_approved_by_admin' IS NOT NULL`) != 0 {
		t.Fatal("nothing is marked for a person who is not an administrator")
	}
	// another approver above the limit
	res, err = adjust(a.both, "a-other", "-30001", a.otherIn)
	must(t, err)
	if res.Item.ApprovedBy == nil || *res.Item.ApprovedBy != a.otherID || res.Item.CreatedBy == nil || *res.Item.CreatedBy != a.bothID {
		t.Fatalf("another approver: %+v", res.Item)
	}

	// DISTINCT: always another person
	f.SetRule(t, f.tenantID, f.propID, "FOLIO_ADJUSTMENT", "DISTINCT")
	_, err = adjust(a.both, "a-d", "-1", a.bothIn)
	wantCode(t, err, "APPROVAL_SAME_USER")

	// a tenant administrator who acts and approves: accepted, marked in the entry and found by the filter of the audit
	_, err = adjust(f.admin, "a-admin", "-5", f.approval())
	must(t, err)
	if marked(t, f, "folio.adjustment_posted") != 1 {
		t.Fatal("the administrator's own approval is marked")
	}
	// an administrator who acts and someone else who approves: no marker
	_, err = adjust(f.admin, "a-admin2", "-5", a.otherIn)
	must(t, err)
	if marked(t, f, "folio.adjustment_posted") != 1 {
		t.Fatal("approval by another person is not marked")
	}
}

func TestTheRuleOfAReversalAndAVoidByAmount(t *testing.T) {
	f := setup(t)
	a := f.actors(t)
	// the amount of a reversal is the charge reversed; the amount of a void is the payment
	c1 := f.charge(t, "c1")
	amount := c1.Item.Debit
	f.SetRule(t, f.tenantID, f.propID, "FOLIO_REVERSAL", "DISTINCT_ABOVE", amount) // the limit is the charge itself: allowed
	res, err := f.Folios.Reverse(a.both, f.propID, c1.Item.ID, folios.CorrectionInput{Reason: "wrong", Approval: a.bothIn})
	must(t, err)
	if res.Item.ApprovedBy == nil || *res.Item.ApprovedBy != a.bothID {
		t.Fatalf("at the limit: %+v", res.Item)
	}
	c2 := f.charge(t, "c2")
	f.SetRule(t, f.tenantID, f.propID, "FOLIO_REVERSAL", "DISTINCT_ABOVE", "1")
	_, err = f.Folios.Reverse(a.both, f.propID, c2.Item.ID, folios.CorrectionInput{Reason: "wrong", Approval: a.bothIn})
	e := code(t, err, "APPROVAL_SAME_USER")
	if e.Context["amount"] != c2.Item.Debit {
		t.Fatalf("the amount is the charge reversed: %v vs %s", e.Context, c2.Item.Debit)
	}
	if _, err := f.Folios.Reverse(a.both, f.propID, c2.Item.ID, folios.CorrectionInput{Reason: "wrong", Approval: a.otherIn}); err != nil {
		t.Fatalf("another approver: %v", err)
	}

	pay1, pay2 := f.payment(t, "p1", "100000"), f.payment(t, "p2", "100001")
	f.SetRule(t, f.tenantID, f.propID, "FOLIO_REVERSAL", "DISTINCT_ABOVE", "100000")
	_, err = f.Folios.Void(a.both, f.propID, pay2.Payment.ID, folios.CorrectionInput{Reason: "wrong", Approval: a.bothIn})
	wantCode(t, err, "APPROVAL_SAME_USER")
	if _, err := f.Folios.Void(a.both, f.propID, pay1.Payment.ID, folios.CorrectionInput{Reason: "wrong", Approval: a.bothIn}); err != nil {
		t.Fatalf("a void at the limit: %v", err)
	}
	if _, err := f.Folios.Void(a.both, f.propID, pay2.Payment.ID, folios.CorrectionInput{Reason: "wrong", Approval: a.otherIn}); err != nil {
		t.Fatalf("another approver: %v", err)
	}
}

func TestTheRuleOfARefundByAmount(t *testing.T) {
	f := setup(t)
	a := f.actors(t)
	pay := f.payment(t, "p1", "200000")
	refund := func(ctx context.Context, key, amount string, in *iam.ApprovalInput) (folios.PaymentResult, error) {
		return f.Folios.Refund(ctx, f.propID, pay.Payment.ID, key, folios.RefundInput{Amount: amount, Reason: "goodwill", Approval: in})
	}
	f.SetRule(t, f.tenantID, f.propID, "REFUND", "DISTINCT_ABOVE", "60000")
	_, err := refund(a.both, "r1", "60000", a.bothIn)
	must(t, err)
	_, err = refund(a.both, "r2", "60001", a.bothIn)
	e := code(t, err, "APPROVAL_SAME_USER")
	if e.Context["amount"] != "60001" || e.Context["threshold"] != "60000" {
		t.Fatalf("context: %v", e.Context)
	}
	res, err := refund(a.both, "r2", "60001", a.otherIn)
	must(t, err)
	if res.Payment.ApprovedBy == nil || *res.Payment.ApprovedBy != a.otherID {
		t.Fatalf("another approver: %+v", res.Payment)
	}
	f.SetRule(t, f.tenantID, f.propID, "REFUND", "DISTINCT")
	_, err = refund(a.both, "r3", "1", a.bothIn)
	wantCode(t, err, "APPROVAL_SAME_USER")
	// the administrator: accepted and marked
	_, err = refund(f.admin, "r4", "1", f.approval())
	must(t, err)
	if marked(t, f, "payment.refunded") != 1 {
		t.Fatal("the administrator's own approval of a refund is marked")
	}
}

func TestTheRuleOfATransferAddsEveryItemOfTheRequest(t *testing.T) {
	p := setupPayer(t)
	a := p.actors(t)
	cf := p.companyFolio(t, "FC1")
	laundry := p.codeID(t, "LAUNDRY")
	var ids []int64
	for _, k := range []string{"l1", "l2", "l3", "l4"} {
		r, err := p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, k, folios.ChargeInput{ChargeCodeID: laundry, Quantity: "1", UnitPrice: ptr("200000")})
		must(t, err)
		ids = append(ids, r.Item.ID)
	}
	p.SetRule(t, p.tenantID, p.propID, "FOLIO_TRANSFER", "DISTINCT_ABOVE", "500000")
	move := func(ctx context.Context, in folios.TransferItemsInput) (folios.TransferResult, error) {
		in.FolioID = cf
		in.Reason = "moved"
		return p.Folios.TransferItems(ctx, p.propID, p.guestFolio, in)
	}
	// three items of 200,000 against a limit of 500,000: refused although each is below it
	_, err := move(a.both, folios.TransferItemsInput{ItemIDs: ids[:3], Approval: a.bothIn})
	e := code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "FOLIO_TRANSFER" || e.Context["amount"] != "600000" {
		t.Fatalf("the amount is the sum of the items: %v", e.Context)
	}
	if p.Count(t, `SELECT count(*) FROM folio_transfers`) != 0 {
		t.Fatal("nothing moved")
	}
	// two items: 400,000, allowed
	_, err = move(a.both, folios.TransferItemsInput{ItemIDs: ids[:2], Approval: a.bothIn})
	must(t, err)
	// by code: the two that are left are 400,000 again
	_, err = move(a.both, folios.TransferItemsInput{ChargeCodeID: &laundry, Approval: a.bothIn})
	must(t, err)
	// DISTINCT: another approver is needed, and the one who approves is recorded on the batch
	var more []int64
	for _, k := range []string{"m1", "m2", "m3"} {
		r, err := p.Folios.PostCharge(p.admin, p.propID, p.guestFolio, k, folios.ChargeInput{ChargeCodeID: laundry, Quantity: "1", UnitPrice: ptr("200000")})
		must(t, err)
		more = append(more, r.Item.ID)
	}
	p.SetRule(t, p.tenantID, p.propID, "FOLIO_TRANSFER", "DISTINCT")
	_, err = move(a.both, folios.TransferItemsInput{ItemIDs: more, Approval: a.bothIn})
	wantCode(t, err, "APPROVAL_SAME_USER")
	if _, err := move(a.both, folios.TransferItemsInput{ItemIDs: more, Approval: a.otherIn}); err != nil {
		t.Fatalf("another approver: %v", err)
	}
	if p.Count(t, `SELECT count(*) FROM folio_transfer_batches WHERE approved_by = $1`, a.otherID) != 1 {
		t.Fatal("the approver is recorded on the batch")
	}
	// the amount of a transfer that is returned is what the transfers moved
	var transfers []int64
	rows, err := p.Pool.Query(context.Background(), `SELECT id FROM folio_transfers ORDER BY id`)
	must(t, err)
	for rows.Next() {
		var id int64
		must(t, rows.Scan(&id))
		transfers = append(transfers, id)
	}
	rows.Close()
	p.SetRule(t, p.tenantID, p.propID, "FOLIO_TRANSFER", "DISTINCT_ABOVE", "599999")
	_, err = p.Folios.ReturnTransfers(a.both, p.propID, cf, folios.ReturnInput{TransferIDs: transfers, To: folios.ReturnToPrevious, Reason: "back", Approval: a.bothIn})
	e = code(t, err, "APPROVAL_SAME_USER")
	if e.Context["amount"] != "1400000" { // the seven transfers of 200,000 that were made
		t.Fatalf("the return adds the transfers: %v", e.Context)
	}
}
