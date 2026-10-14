package frontdesk_test

import (
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/auth"
)

// The rule of approval of the rate of a stay (docs/architecture/32-distinct-approver.md): the amount is the sum over the nights of what the price moves.
func TestTheRuleOfAStayRateByTheSumOfTheNights(t *testing.T) {
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-04") // nights 10-01, 10-02, 10-03 at 1,000,000
	approver := f.User(t, f.tenantID, f.propID, auth.PermFrontdeskRateChange, auth.PermReservationRead, auth.PermReservationOverrideApprove)
	f.SetRule(t, f.tenantID, f.propID, "STAY_RATE", "DISTINCT_ABOVE", "150000")

	// three nights lowered by 100,000 each: the rate approver who acts may approve up to 150,000 of change, not 300,000
	_, err := f.Front.ChangeRates(approver, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyRemaining, "2026-10-01", "900000"))
	e := code(t, err, "APPROVAL_REQUIRED")
	if e.Context["kind"] != "STAY_RATE" || e.Context["rule"] != "DISTINCT_ABOVE" || e.Context["amount"] != "300000" || e.Context["threshold"] != "150000" {
		t.Fatalf("the rule is named: %v", e.Context)
	}
	if f.nightAmount(t, st.Stay.ID, "2026-10-01") != "1000000" {
		t.Fatal("a refused change moves nothing")
	}
	// one night by 100,000: within the limit, their own
	res, err := f.Front.ChangeRates(approver, f.propID, st.Stay.ID, f.rateIn(st.Stay.Version, frontdesk.ApplyNight, "2026-10-01", "900000"))
	must(t, err)
	// one night by 150,000: at the limit
	res, err = f.Front.ChangeRates(approver, f.propID, st.Stay.ID, f.rateIn(res.Stay.Version, frontdesk.ApplyNight, "2026-10-02", "850000"))
	must(t, err)
	// one night by 200,000: above it, someone else must approve
	over := f.rateIn(res.Stay.Version, frontdesk.ApplyNight, "2026-10-03", "800000")
	_, err = f.Front.ChangeRates(approver, f.propID, st.Stay.ID, over)
	wantCode(t, err, "APPROVAL_REQUIRED")
	over.Approval = f.approval(t) // the administrator is not the actor
	res, err = f.Front.ChangeRates(approver, f.propID, st.Stay.ID, over)
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.rate_changed' AND new_data->>'self_approved_by_admin' IS NOT NULL`) != 0 {
		t.Fatal("nothing is marked: the administrator approved another person's change")
	}

	// DISTINCT: the administrator who acts and approves is accepted and marked
	f.SetRule(t, f.tenantID, f.propID, "STAY_RATE", "DISTINCT")
	self := f.rateIn(res.Stay.Version, frontdesk.ApplyNight, "2026-10-03", "700000")
	self.Approval = f.approval(t)
	_, err = f.Front.ChangeRates(f.admin, f.propID, st.Stay.ID, self)
	must(t, err)
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.rate_changed' AND new_data->>'self_approved_by_admin' = 'true'`) != 1 {
		t.Fatal("the administrator's approval of their own change is marked")
	}
	// a rate approver who acts needs another person under DISTINCT, whatever the amount
	_, err = f.Front.ChangeRates(approver, f.propID, st.Stay.ID, f.rateIn(res.Stay.Version+1, frontdesk.ApplyNight, "2026-10-03", "699999"))
	wantCode(t, err, "APPROVAL_REQUIRED")
}
