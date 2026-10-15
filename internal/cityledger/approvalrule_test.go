package cityledger_test

import (
	"testing"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

var code = roomstest.Code

// The rules of approval (docs/architecture/32-distinct-approver.md) for the city ledger: credit notes, write-offs and the void of an adjustment are one kind (the amount of the note,
// tax included), the void of an invoice and of a receipt another.
func TestTheRulesOfTheCityLedgerByKindAndAmount(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	clerk, clerkEmail := f.Account(t, f.tenantID, f.propID, auth.PermCityLedgerCreditNote, auth.PermCityLedgerWriteOff, auth.PermCityLedgerInvoice, auth.PermCityLedgerReceive, auth.PermCorrectionApprove)
	_, otherEmail := f.Account(t, f.tenantID, f.propID, auth.PermCorrectionApprove)
	own := &iam.ApprovalInput{Email: clerkEmail, Password: roomstest.Password}
	other := &iam.ApprovalInput{Email: otherEmail, Password: roomstest.Password}
	inv, _ := f.invoicedOne(t, "101", "500000")
	f.SetRule(t, f.tenantID, f.propID, "CITY_LEDGER_CREDIT", "DISTINCT_ABOVE", "100000")
	note := func(net, key string, ap *iam.ApprovalInput) (cityledger.Adjustment, error) {
		return f.CityLedger.CreateCreditNote(clerk, f.propID, key, cityledger.CreditNoteInput{InvoiceID: &inv.ID, Reason: "dispute", Approval: ap,
			Lines: []cityledger.CreditNoteLineInput{{Description: "Room rate dispute", AccountID: c.allowance, NetAmount: net, TaxID: &c.pb1.ID}}})
	}
	// 90,000 and the tax (10%) on it make 99,000: within the limit
	n, err := note("90000", "cn1", own)
	must(t, err)
	// 100,000 and its tax make 110,000: the amount of the note is the total, so the tax counts; the net alone is at the limit
	_, err = note("100000", "cn2", own)
	e := code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "CITY_LEDGER_CREDIT" || e.Context["amount"] != "110000" {
		t.Fatalf("the amount is the note with its tax: %v", e.Context)
	}
	if f.Count(t, `SELECT count(*) FROM city_ledger_adjustments`) != 1 {
		t.Fatal("a refused approval writes nothing")
	}
	if _, err := note("100000", "cn2", other); err != nil {
		t.Fatalf("another approver: %v", err)
	}
	// a write-off: the amount is the amount
	wo := func(amount, key string, ap *iam.ApprovalInput) (cityledger.Adjustment, error) {
		return f.CityLedger.CreateWriteOff(clerk, f.propID, key, cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: amount, AccountID: c.badDebt, Reason: "closed", Approval: ap})
	}
	_, err = wo("100000", "w1", own)
	must(t, err)
	_, err = wo("100001", "w2", own)
	wantCode(t, err, "APPROVAL_SAME_USER")
	// the void of the first note (99,000) is one more correction of the same kind: lower the limit under it
	f.SetRule(t, f.tenantID, f.propID, "CITY_LEDGER_CREDIT", "DISTINCT_ABOVE", "98999")
	_, err = f.CityLedger.VoidAdjustment(clerk, f.propID, n.ID, cityledger.VoidInput{Reason: "wrong", Approval: own})
	e = code(t, err, "APPROVAL_SAME_USER")
	if e.Context["amount"] != "99000" {
		t.Fatalf("the amount of the note voided: %v", e.Context)
	}
	if _, err := f.CityLedger.VoidAdjustment(clerk, f.propID, n.ID, cityledger.VoidInput{Reason: "wrong", Approval: other}); err != nil {
		t.Fatalf("another approver: %v", err)
	}

	// the void of a receipt and of an invoice: the other kind
	f.SetRule(t, f.tenantID, f.propID, "CITY_LEDGER_VOID", "DISTINCT")
	rc, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	_, err = f.CityLedger.VoidReceipt(clerk, f.propID, rc.Receipt.ID, cityledger.VoidInput{Reason: "wrong", Approval: own})
	wantCode(t, err, "APPROVAL_SAME_USER")
	if _, err := f.CityLedger.VoidReceipt(clerk, f.propID, rc.Receipt.ID, cityledger.VoidInput{Reason: "wrong", Approval: other}); err != nil {
		t.Fatalf("another approver: %v", err)
	}
	inv2, _ := f.invoicedOne(t, "102", "200000")
	_, err = f.CityLedger.VoidInvoice(clerk, f.propID, inv2.ID, cityledger.VoidInput{Reason: "wrong", Approval: own})
	e = code(t, err, "APPROVAL_SAME_USER")
	if e.Context["kind"] != "CITY_LEDGER_VOID" {
		t.Fatalf("context: %v", e.Context)
	}
	// the tenant administrator who acts: accepted, marked
	if _, err := f.CityLedger.VoidInvoice(f.admin, f.propID, inv2.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()}); err != nil {
		t.Fatalf("administrator: %v", err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cityledger.invoice_voided' AND new_data->>'self_approved_by_admin' = 'true'`) != 1 {
		t.Fatal("marked")
	}
}
