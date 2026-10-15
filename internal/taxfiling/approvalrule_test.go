package taxfiling_test

import (
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/taxfiling"
)

// The rules of approval (docs/architecture/32-distinct-approver.md) for the tax: the opening credit and its void are one kind with the tax amount, the settings of the tax another kind
// without an amount.
func TestTheRulesOfTheTax(t *testing.T) {
	v := setupVAT(t)
	lead, leadEmail := v.Account(t, v.tenantID, v.propID, auth.PermTaxManage, auth.PermTaxView, auth.PermTaxFile, auth.PermCorrectionApprove)
	_, otherEmail := v.Account(t, v.tenantID, v.propID, auth.PermCorrectionApprove)
	own := &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}
	other := &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}
	v.SetRule(t, v.tenantID, v.propID, "TAX_DOCUMENT", "DISTINCT_ABOVE", "7000")

	credit := func(amount string, ap *iam.ApprovalInput) error {
		_, err := v.Tax.SetOpeningCredit(lead, v.propID, v.profile.ID, taxfiling.OpeningCreditInput{AsOf: d("2026-09-30"), Amount: dec(amount), Approval: ap})
		return err
	}
	e := roomstest.Code(t, credit("7001", own), "APPROVAL_SAME_USER")
	if e.Context["kind"] != "TAX_DOCUMENT" || e.Context["amount"] != "7001" || e.Context["threshold"] != "7000" {
		t.Fatalf("the amount is the credit: %v", e.Context)
	}
	if v.Count(t, `SELECT count(*) FROM tax_opening_credits`) != 0 {
		t.Fatal("a refused approval writes nothing")
	}
	must(t, credit("7000", own)) // at the limit
	// the void of the credit has the amount of the credit: lower the limit under it
	v.SetRule(t, v.tenantID, v.propID, "TAX_DOCUMENT", "DISTINCT_ABOVE", "6999")
	_, err := v.Tax.VoidOpeningCredit(lead, v.propID, v.profile.ID, taxfiling.VoidInput{Reason: "wrong", Approval: own})
	e = roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["amount"] != "7000" {
		t.Fatalf("the amount of the credit voided: %v", e.Context)
	}
	if _, err := v.Tax.VoidOpeningCredit(lead, v.propID, v.profile.ID, taxfiling.VoidInput{Reason: "wrong", Approval: other}); err != nil {
		t.Fatalf("another approver: %v", err)
	}

	// the settings of the tax, backdated: no amount, DISTINCT only
	v.SetRule(t, v.tenantID, v.propID, "TAX_SETTINGS", "DISTINCT")
	back := taxfiling.SettingsInput{EffectiveFrom: d("2026-09-15"), IsPKP: true, NPWP: "01.234.567.8-901.000", InputVATTreatment: "DEFERRED", Approval: own}
	_, err = v.Tax.ChangeSettings(lead, v.propID, back)
	wantCode(t, err, "APPROVAL_SAME_USER")
	back.Approval = other
	// the approval of another person is accepted: the change is then refused for what it is (the history of this fixture already has a change from the business date)
	_, err = v.Tax.ChangeSettings(lead, v.propID, back)
	wantCode(t, err, "TAX_SETTINGS_NOT_NEWER")
}
