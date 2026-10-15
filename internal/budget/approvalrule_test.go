package budget_test

import (
	"testing"

	"kamarapms/internal/budget"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// The rule of approval (docs/architecture/32-distinct-approver.md) for making a budget active: a kind without an amount.
func TestTheRuleOfTheActivationOfABudget(t *testing.T) {
	f := setup(t)
	lead, leadEmail := f.Account(t, f.tenantID, f.propID, auth.PermBudgetView, auth.PermBudgetManage, auth.PermBudgetApprove)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermBudgetApprove)
	f.SetRule(t, f.tenantID, f.propID, "BUDGET", "DISTINCT")
	b := f.create(t, "2026-01-01", "Budget 2026")
	f.save(t, b.ID, row(f.rooms, "1000000", 1, 2, 3))

	_, err := f.Budget.Activate(lead, f.propID, b.ID, budget.ActivateInput{Approval: &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}})
	e := roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "BUDGET" || e.Context["rule"] != "DISTINCT" {
		t.Fatalf("context: %v", e.Context)
	}
	got, err := f.Budget.Get(lead, f.propID, b.ID)
	must(t, err)
	if got.Status != budget.StatusDraft {
		t.Fatalf("a refused approval activates nothing: %s", got.Status)
	}
	active, err := f.Budget.Activate(lead, f.propID, b.ID, budget.ActivateInput{Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}})
	must(t, err)
	if active.Status != budget.StatusActive || active.ApprovedBy == nil {
		t.Fatalf("activated: %+v", active)
	}
	// the administrator who makes the change and approves it: accepted, marked
	b2 := f.create(t, "2027-01-01", "Budget 2027")
	f.save(t, b2.ID, row(f.rooms, "1000000", 1))
	if _, err := f.Budget.Activate(f.admin, f.propID, b2.ID, budget.ActivateInput{Approval: f.approval()}); err != nil {
		t.Fatalf("administrator: %v", err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'budget.activated' AND new_data->>'self_approved_by_admin' = 'true'`) != 1 {
		t.Fatal("marked")
	}
}
