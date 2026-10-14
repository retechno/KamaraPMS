package payables_test

import (
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// The rule of approval (docs/architecture/32-distinct-approver.md) for voiding a bill, a payment or a credit note: the amount is the total of the document voided.
func TestTheRuleOfVoidingAPayablesDocumentIsItsTotal(t *testing.T) {
	f := setup(t)
	lead, leadEmail := f.Account(t, f.tenantID, f.propID, auth.PermPayablesPost, auth.PermPayablesView, auth.PermCorrectionApprove)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermCorrectionApprove)
	own := &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}
	other := &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}
	sup := f.supplier(t, "PLN", 30)
	small, big := f.bill(t, sup, "INV-1", "1000", "b1"), f.bill(t, sup, "INV-2", "1001", "b2")
	f.SetRule(t, f.tenantID, f.propID, "PAYABLES", "DISTINCT_ABOVE", "1000")

	_, err := f.Payables.VoidBill(lead, f.propID, big.ID, payables.VoidInput{Reason: "duplicate", Approval: own})
	e := roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "PAYABLES" || e.Context["amount"] != "1001" || e.Context["threshold"] != "1000" {
		t.Fatalf("the amount is the total of the bill: %v", e.Context)
	}
	if _, err := f.Payables.VoidBill(lead, f.propID, small.ID, payables.VoidInput{Reason: "duplicate", Approval: own}); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	if _, err := f.Payables.VoidBill(lead, f.propID, big.ID, payables.VoidInput{Reason: "duplicate", Approval: other}); err != nil {
		t.Fatalf("another approver: %v", err)
	}

	// a payment and a credit note
	b3 := f.bill(t, sup, "INV-3", "3000", "b3")
	pay := f.pay(t, sup, "CASH", "p1", payables.AllocationInput{BillID: b3.ID, Amount: dec("2000")})
	_, err = f.Payables.VoidPayment(lead, f.propID, pay.ID, payables.VoidInput{Reason: "wrong", Approval: own})
	wantCode(t, err, "APPROVAL_SAME_USER")
	if _, err := f.Payables.VoidPayment(lead, f.propID, pay.ID, payables.VoidInput{Reason: "wrong", Approval: other}); err != nil {
		t.Fatalf("another approver: %v", err)
	}
	cr, err := f.credit(t, b3, "CR-1", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("1500")})
	must(t, err)
	_, err = f.Payables.VoidCreditNote(lead, f.propID, cr.ID, payables.VoidInput{Reason: "withdrawn", Approval: own})
	wantCode(t, err, "APPROVAL_SAME_USER")
	// the administrator who acts: accepted and marked
	if _, err := f.Payables.VoidCreditNote(f.admin, f.propID, cr.ID, payables.VoidInput{Reason: "withdrawn", Approval: f.approval()}); err != nil {
		t.Fatalf("administrator: %v", err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'payables.credit_note_voided' AND new_data->>'self_approved_by_admin' = 'true'`) != 1 {
		t.Fatal("marked")
	}
}
