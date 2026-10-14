package shifts_test

import (
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/shifts"
)

// The rule of approval (docs/architecture/32-distinct-approver.md) for the difference of a shift: the amount is the difference of the count, whatever its sign.
func TestTheRuleOfTheDifferenceOfAShift(t *testing.T) {
	f := setup(t)
	lead, leadEmail := f.Account(t, f.tenantID, f.propID, auth.PermCashierShift, auth.PermCashierShiftApprove, auth.PermPaymentPost, auth.PermFolioRead)
	own := &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}
	f.SetRule(t, f.tenantID, f.propID, "SHIFT_VARIANCE", "DISTINCT_ABOVE", "5000")
	sh := f.open(t, lead, "", "100000")
	_, err := f.pay(lead, f.folio(t), "p1", "CASH", "400000") // expected 500,000
	must(t, err)
	closeIt := func(counted string, ap *iam.ApprovalInput) (shifts.Shift, error) {
		return f.Shifts.Close(lead, f.propID, sh.ID, shifts.CloseInput{CountedCash: counted, Reason: "a difference", Approval: ap})
	}
	_, err = closeIt("494999", own) // 5,001 short: above the limit
	e := roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "SHIFT_VARIANCE" || e.Context["amount"] != "5001" || e.Context["threshold"] != "5000" {
		t.Fatalf("the difference is the amount: %v", e.Context)
	}
	if f.Count(t, `SELECT count(*) FROM cashier_shifts WHERE status = 'CLOSED'`) != 0 {
		t.Fatal("a refused close closes nothing")
	}
	if _, err := closeIt("505000", own); err != nil { // 5,000 over: at the limit, the sign does not matter
		t.Fatalf("at the limit: %v", err)
	}

	// above the limit with another person: accepted, and the shift records who approved
	sh = f.open(t, lead, "", "0")
	_, err = f.pay(lead, f.folio(t), "p2", "CASH", "100000")
	must(t, err)
	closed, err := closeIt("90000", f.approval()) // 10,000 short, approved by the administrator
	must(t, err)
	if closed.ApprovedBy == nil || *closed.ApprovedBy == f.cashierID {
		t.Fatalf("approved by: %+v", closed.ApprovedBy)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cashier.shift_closed' AND new_data->>'self_approved_by_admin' IS NOT NULL`) != 0 {
		t.Fatal("nothing is marked")
	}

	// the administrator closes their own shift and approves the difference: accepted, marked
	f.SetRule(t, f.tenantID, f.propID, "SHIFT_VARIANCE", "DISTINCT")
	adminShift := f.open(t, f.admin, "", "0")
	_, err = f.pay(f.admin, f.folio(t), "p3", "CASH", "100000")
	must(t, err)
	if _, err := f.Shifts.Close(f.admin, f.propID, adminShift.ID, shifts.CloseInput{CountedCash: "99000", Reason: "a difference", Approval: f.approval()}); err != nil {
		t.Fatalf("administrator: %v", err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cashier.shift_closed' AND new_data->>'self_approved_by_admin' = 'true'`) != 1 {
		t.Fatal("the administrator's own approval is marked")
	}
}
