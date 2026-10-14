package accounting_test

import (
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

var code = roomstest.Code

// The rules of approval (docs/architecture/32-distinct-approver.md) for the books: the reversal of a manual journal (the amount is its total debit) and the reopening of a fiscal year (no amount).
func TestTheRuleOfTheReversalOfAJournalIsItsTotalDebit(t *testing.T) {
	h := setupHotel(t)
	clerk, clerkEmail := h.Account(t, h.tenantID, h.propID, auth.PermAccountingPost, auth.PermAccountingView, auth.PermCorrectionApprove)
	_, otherEmail := h.Account(t, h.tenantID, h.propID, auth.PermCorrectionApprove)
	own := &iam.ApprovalInput{Email: clerkEmail, Password: roomstest.Password}
	other := &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}
	small, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100000"), "m1")
	must(t, err)
	big, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("100001"), "m2")
	must(t, err)
	h.SetRule(t, h.tenantID, h.propID, "JOURNAL", "DISTINCT_ABOVE", "100000")

	_, err = h.Accounting.Reverse(clerk, h.propID, big.ID, accounting.ReverseInput{Reason: "mistake", Approval: own})
	e := code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "JOURNAL" || e.Context["amount"] != "100001" || e.Context["threshold"] != "100000" {
		t.Fatalf("the amount is the total debit of the journal: %v", e.Context)
	}
	if h.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'REVERSAL'`) != 0 {
		t.Fatal("a refused approval posts nothing")
	}
	if _, err := h.Accounting.Reverse(clerk, h.propID, small.ID, accounting.ReverseInput{Reason: "mistake", Approval: own}); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
	rev, err := h.Accounting.Reverse(clerk, h.propID, big.ID, accounting.ReverseInput{Reason: "mistake", Approval: other})
	must(t, err)
	if h.Count(t, `SELECT count(*) FROM gl_journals WHERE id = $1 AND approved_by = (SELECT id FROM users WHERE email = $2)`, rev.ID, otherEmail) != 1 {
		t.Fatal("the journal records who approved")
	}
	// the administrator who acts and approves their own reversal under DISTINCT: accepted, marked
	h.SetRule(t, h.tenantID, h.propID, "JOURNAL", "DISTINCT")
	again, err := h.Accounting.PostManual(h.admin, h.propID, h.simple("5"), "m3")
	must(t, err)
	if _, err := h.Accounting.Reverse(h.admin, h.propID, again.ID, accounting.ReverseInput{Reason: "mistake", Approval: h.approval()}); err != nil {
		t.Fatalf("administrator: %v", err)
	}
	if h.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'accounting.journal_reversed' AND new_data->>'self_approved_by_admin' = 'true'`) != 1 {
		t.Fatal("marked")
	}
}

func TestTheRuleOfReopeningAFiscalYearHasNoAmount(t *testing.T) {
	h := yearHotel(t)
	fy := h.year(t, "FY2026")
	_, err := h.Accounting.ClosePeriod(h.admin, h.propID, d("2026-09-01"))
	must(t, err)
	_, err = h.Accounting.CloseFiscalYear(h.admin, h.propID, fy.Start)
	must(t, err)
	clerk, clerkEmail := h.Account(t, h.tenantID, h.propID, auth.PermAccountingClose, auth.PermAccountingView, auth.PermCorrectionApprove)
	_, otherEmail := h.Account(t, h.tenantID, h.propID, auth.PermCorrectionApprove)
	h.SetRule(t, h.tenantID, h.propID, "FISCAL_YEAR", "DISTINCT")
	_, err = h.Accounting.ReopenFiscalYear(clerk, h.propID, fy.Start, accounting.ReopenYearInput{Reason: "audit", Approval: &iam.ApprovalInput{Email: clerkEmail, Password: roomstest.Password}})
	wantCode(t, err, "APPROVAL_SAME_USER")
	if _, err := h.Accounting.ReopenFiscalYear(clerk, h.propID, fy.Start, accounting.ReopenYearInput{Reason: "audit", Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}}); err != nil {
		t.Fatalf("another approver: %v", err)
	}
}
