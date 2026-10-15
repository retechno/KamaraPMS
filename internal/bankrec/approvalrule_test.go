package bankrec_test

import (
	"testing"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// The rule of approval (docs/architecture/32-distinct-approver.md) for reopening a reconciled statement: a kind without an amount.
func TestTheRuleOfReopeningAStatement(t *testing.T) {
	f := setup(t)
	f.books(t)
	first := f.importFirst(t)
	_, err := f.BankRec.AutoMatch(f.admin, f.propID, first.ID)
	must(t, err)
	fee := lineOf(t, first, "fee")
	_, err = f.BankRec.Adjust(f.admin, f.propID, first.ID, fee.ID, bankrec.AdjustInput{AccountID: f.acc["6130"]})
	must(t, err)
	_, err = f.BankRec.Reconcile(f.admin, f.propID, first.ID)
	must(t, err)
	lead, leadEmail := f.Account(t, f.tenantID, f.propID, auth.PermBankReconcile, auth.PermBankView, auth.PermCorrectionApprove)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermCorrectionApprove)
	f.SetRule(t, f.tenantID, f.propID, "BANK", "DISTINCT")
	_, err = f.BankRec.Reopen(lead, f.propID, first.ID, bankrec.ReopenInput{Reason: "missed a fee", Approval: &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}})
	e := roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "BANK" {
		t.Fatalf("context: %v", e.Context)
	}
	re, err := f.BankRec.Reopen(lead, f.propID, first.ID, bankrec.ReopenInput{Reason: "missed a fee", Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}})
	must(t, err)
	if re.Status != "OPEN" {
		t.Fatalf("reopened: %+v", re.Statement)
	}
}
