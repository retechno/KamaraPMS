package taxinvoice_test

import (
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/taxinvoice"
)

// The rule of approval (docs/architecture/32-distinct-approver.md) for voiding a tax invoice: the amount is its VAT.
func TestTheRuleOfVoidingATaxInvoiceIsItsVAT(t *testing.T) {
	f := setup(t, true)
	cli, _ := f.cityLedgerInvoice(t, "110000", "121000")
	inv, err := f.issueCL(t, cli.ID, "k1")
	must(t, err)
	lead, leadEmail := f.Account(t, f.tenantID, f.propID, auth.PermTaxInvoice, auth.PermTaxView, auth.PermCorrectionApprove)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermCorrectionApprove)
	f.SetRule(t, f.tenantID, f.propID, "TAX_DOCUMENT", "DISTINCT_ABOVE", "1")
	_, err = f.TaxInvoice.VoidInvoice(lead, f.propID, inv.ID, taxinvoice.VoidInput{Reason: "wrong buyer", Approval: &iam.ApprovalInput{Email: leadEmail, Password: roomstest.Password}})
	e := roomstest.Code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "TAX_DOCUMENT" || e.Context["amount"] != inv.VATAmount.String() {
		t.Fatalf("the amount is the VAT of the invoice: %v vs %s", e.Context, inv.VATAmount)
	}
	v, err := f.TaxInvoice.VoidInvoice(lead, f.propID, inv.ID, taxinvoice.VoidInput{Reason: "wrong buyer", Approval: &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}})
	must(t, err)
	if v.Status != "VOIDED" {
		t.Fatalf("voided: %+v", v)
	}
}
