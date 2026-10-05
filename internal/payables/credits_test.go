package payables_test

import (
	"context"
	"testing"

	"kamarapms/internal/payables"
	"kamarapms/internal/taxfiling"
)

func (f *fx) credit(t *testing.T, bill payables.Bill, number, date string, lines ...payables.CreditLineInput) (payables.CreditNote, error) {
	t.Helper()
	return f.Payables.PostCreditNote(f.admin, f.propID, payables.CreditNoteInput{BillID: bill.ID, SupplierCreditNumber: number, CreditDate: d(date), Reason: "goods returned", Lines: lines}, "")
}

func (f *fx) billOf(t *testing.T, id int64) payables.Bill {
	t.Helper()
	b, err := f.Payables.GetBill(f.admin, f.propID, id)
	must(t, err)
	return b
}

func TestACreditNoteTakesOffWhatABillOwesAndMirrorsItsJournal(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	b := f.bill(t, sup, "INV-1", "1000", "")

	c, err := f.credit(t, b, "CR-1", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("300"), Description: "returned"})
	must(t, err)
	if c.Number != "SCN000001" || c.Status != "POSTED" || len(c.Lines) != 1 || len(c.Allocations) != 1 {
		t.Fatalf("credit note: %+v", c)
	}
	eq(t, "total", c.Total, "300")
	eq(t, "applied to the bill it credits", c.Applied, "300")
	eq(t, "nothing left over", c.Unapplied, "0")
	if c.Lines[0].AccountCode != "6510" || c.BillNumber != b.Number {
		t.Fatalf("line: %+v", c.Lines[0])
	}
	got := f.billOf(t, b.ID)
	eq(t, "credited", got.Credited, "300")
	eq(t, "outstanding", got.Outstanding, "700")
	if got.PaymentStatus != "PARTIAL" {
		t.Fatalf("a credited bill is partly settled: %s", got.PaymentStatus)
	}
	// the journal is the mirror of the bill
	eq(t, "expense", f.balance(t, "6510"), "700")
	eq(t, "payable", f.balance(t, "2110"), "-700")
	eq(t, "supplier owed", f.supplierOf(t, sup.ID).Outstanding, "700")
	f.requireBalanced(t)

	// a line cannot be credited for more than it has left, and a credit note is entered once
	_, err = f.credit(t, b, "CR-2", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("701")})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.credit(t, b, "CR-1", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("10")})
	wantCode(t, err, "DUPLICATE_CREDIT_NOTE")
	_, err = f.credit(t, b, "CR-3", "2026-09-29", payables.CreditLineInput{BillLineNo: 1, Amount: dec("10")}) // before the bill
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.credit(t, b, "CR-4", "2026-09-30", payables.CreditLineInput{BillLineNo: 9, Amount: dec("10")})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.credit(t, b, "CR-5", "2026-09-30", payables.CreditLineInput{BillLineNo: 1})
	wantCode(t, err, "VALIDATION_FAILED")
	// the rest of the line can still be credited
	_, err = f.credit(t, b, "CR-6", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("700")})
	must(t, err)
	got = f.billOf(t, b.ID)
	eq(t, "settled by credit", got.Outstanding, "0")
	if got.PaymentStatus != "PAID" {
		t.Fatalf("status %s", got.PaymentStatus)
	}
	if list, err := f.Payables.Bills(f.admin, f.propID, payables.BillFilter{OpenOnly: true}); err != nil || len(list) != 0 {
		t.Fatalf("a bill credited in full is not open: %v %v", list, err)
	}
	// a bill with credit notes is not voided before they are
	_, err = f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "BILL_HAS_CREDIT_NOTES")
}

func TestACreditThatTheBillCannotTakeStaysACreditOfTheSupplier(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	b1 := f.bill(t, sup, "INV-1", "1000", "")
	f.pay(t, sup, "CASH", "p1", payables.AllocationInput{BillID: b1.ID, Amount: dec("1000")}) // paid in full
	c, err := f.credit(t, b1, "CR-1", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("300")})
	must(t, err)
	eq(t, "nothing to take off the paid bill", c.Applied, "0")
	eq(t, "the whole credit is left", c.Unapplied, "300")
	s := f.supplierOf(t, sup.ID)
	eq(t, "the supplier owes the hotel", s.Outstanding, "-300")
	eq(t, "unapplied credit", s.UnappliedCredit, "300")
	if list, err := f.Payables.CreditNotes(f.admin, f.propID, payables.CreditFilter{UnappliedOnly: true}); err != nil || len(list) != 1 {
		t.Fatalf("unapplied credit notes: %v %v", list, err)
	}

	// the aging shows the credit apart from the bills
	ag, err := f.Payables.Aging(f.admin, f.propID, nil)
	must(t, err)
	eq(t, "no open bills", ag.Total, "0")
	eq(t, "unapplied credit", ag.UnappliedCredit, "300")
	eq(t, "net", ag.Net, "-300")
	if len(ag.Suppliers) != 1 || len(ag.Suppliers[0].Credits) != 1 {
		t.Fatalf("aging: %+v", ag.Suppliers)
	}

	// a later bill takes the credit
	b2 := f.bill(t, sup, "INV-2", "500", "")
	if _, err := f.Payables.ApplyCredit(f.admin, f.propID, c.ID, payables.ApplyCreditInput{Allocations: []payables.AllocationInput{{BillID: b2.ID, Amount: dec("301")}}}); err == nil {
		t.Fatal("more than the credit has left")
	}
	if _, err := f.Payables.ApplyCredit(f.admin, f.propID, c.ID, payables.ApplyCreditInput{Allocations: []payables.AllocationInput{{BillID: b1.ID, Amount: dec("10")}}}); err == nil {
		t.Fatal("a bill that is paid takes no credit")
	}
	got, err := f.Payables.ApplyCredit(f.admin, f.propID, c.ID, payables.ApplyCreditInput{Allocations: []payables.AllocationInput{{BillID: b2.ID, Amount: dec("200")}}})
	must(t, err)
	eq(t, "applied", got.Applied, "200")
	eq(t, "left", got.Unapplied, "100")
	eq(t, "the new bill owes less", f.billOf(t, b2.ID).Outstanding, "300")
	eq(t, "supplier", f.supplierOf(t, sup.ID).Outstanding, "200")
	// a payment then settles the rest of that bill, and no more
	f.pay(t, sup, "CASH", "p2", payables.AllocationInput{BillID: b2.ID, Amount: dec("300")})
	_, err = f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: sup.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH", Allocations: []payables.AllocationInput{{BillID: b2.ID, Amount: dec("1")}}}, "p3")
	wantCode(t, err, "ALLOCATION_EXCEEDS_OUTSTANDING")
	f.requireBalanced(t)

	// voiding the credit note gives it back: the bill it was applied to owes that much again
	v, err := f.Payables.VoidCreditNote(f.admin, f.propID, c.ID, payables.VoidInput{Reason: "supplier withdrew it", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || !v.Applied.IsZero() {
		t.Fatalf("voided: %+v", v)
	}
	eq(t, "the bill owes the 200 again", f.billOf(t, b2.ID).Outstanding, "200")
	eq(t, "supplier owed", f.supplierOf(t, sup.ID).Outstanding, "200")
	eq(t, "the expense is back", f.balance(t, "6510"), "1500")
	_, err = f.Payables.VoidCreditNote(f.admin, f.propID, c.ID, payables.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "CREDIT_NOTE_ALREADY_VOIDED")
	f.requireBalanced(t)
}

func TestTheVATOfACreditNoteFollowsTheTreatmentOfTheBillLine(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	// not PKP: the VAT of the bill was part of the cost, so the credit note gives it back to the expense
	b, err := f.vatBill(t, sup, "INV-1", "2026-09-30", "1000", "110")
	must(t, err)
	c, err := f.credit(t, b, "CR-1", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("200"), VATAmount: dec("22")})
	must(t, err)
	eq(t, "total", c.Total, "222")
	if c.Lines[0].VATTreatment != "EXPENSE" {
		t.Fatalf("treatment of the bill line: %+v", c.Lines[0])
	}
	eq(t, "expense", f.balance(t, "6510"), "888")
	eq(t, "no input VAT", f.balance(t, "1425"), "0")
	// the VAT cannot be more than the bill line paid
	_, err = f.credit(t, b, "CR-2", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("1"), VATAmount: dec("89")})
	wantCode(t, err, "VALIDATION_FAILED")

	// PKP: the VAT was input VAT, so the credit note takes it back from account 1425
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01"), IsPKP: true, NPWP: "01.234.567.8-901.000"})
	must(t, err)
	f.closeDay(t)
	b2, err := f.vatBill(t, sup, "INV-2", "2026-10-01", "2000", "220")
	must(t, err)
	eq(t, "input VAT of the bill", f.balance(t, "1425"), "220")
	c2, err := f.credit(t, b2, "CR-3", "2026-10-01", payables.CreditLineInput{BillLineNo: 1, Amount: dec("500"), VATAmount: dec("55")})
	must(t, err)
	if c2.Lines[0].VATTreatment != "CREDITABLE" {
		t.Fatalf("treatment: %+v", c2.Lines[0])
	}
	eq(t, "input VAT after the credit note", f.balance(t, "1425"), "165")
	eq(t, "expense", f.balance(t, "6510"), "2388")
	eq(t, "payable", f.balance(t, "2110"), "-2553")
	f.requireBalanced(t)
	_, err = f.Payables.VoidCreditNote(f.admin, f.propID, c2.ID, payables.VoidInput{Reason: "x", Approval: f.approval()})
	must(t, err)
	eq(t, "input VAT is back after the void", f.balance(t, "1425"), "220")
}

// The database holds the same rules without the service.
func TestTheDatabaseKeepsCreditNotesWhole(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	b := f.bill(t, sup, "INV-1", "1000", "")
	c, err := f.credit(t, b, "CR-1", "2026-09-30", payables.CreditLineInput{BillLineNo: 1, Amount: dec("300")})
	must(t, err)
	ctx := context.Background()
	// a credit note is never deleted and only goes from POSTED to VOIDED
	if _, err := f.Pool.Exec(ctx, `DELETE FROM supplier_credit_notes WHERE id = $1`, c.ID); err == nil {
		t.Fatal("credit notes are not deleted")
	}
	if _, err := f.Pool.Exec(ctx, `UPDATE supplier_credit_notes SET total = 1 WHERE id = $1`, c.ID); err == nil {
		t.Fatal("a credit note does not change")
	}
	if _, err := f.Pool.Exec(ctx, `UPDATE supplier_credit_note_lines SET amount = 1 WHERE credit_id = $1`, c.ID); err == nil {
		t.Fatal("lines never change")
	}
	if _, err := f.Pool.Exec(ctx, `DELETE FROM supplier_credit_allocations WHERE credit_id = $1`, c.ID); err == nil {
		t.Fatal("allocations never change")
	}
	// no more than the total is applied
	if _, err := f.Pool.Exec(ctx, `INSERT INTO supplier_credit_allocations (tenant_id, property_id, supplier_id, credit_id, bill_id, amount, applied_on) VALUES ($1, $2, $3, $4, $5, 1, '2026-09-30')`,
		f.tenantID, f.propID, sup.ID, c.ID, b.ID); err == nil {
		t.Fatal("a credit note is applied for no more than its total")
	}
	// the lines add up to the total
	if _, err := f.Pool.Exec(ctx, `INSERT INTO supplier_credit_note_lines (tenant_id, property_id, credit_id, line_no, bill_id, bill_line_no, account_id, amount) SELECT $1, $2, $3, 2, $4, 1, account_id, 5 FROM supplier_bill_lines WHERE bill_id = $4`,
		f.tenantID, f.propID, c.ID, b.ID); err == nil {
		t.Fatal("the lines of a credit note add up to its total")
	}
	// a line with VAT has its treatment
	if _, err := f.Pool.Exec(ctx, `INSERT INTO supplier_credit_notes (tenant_id, property_id, credit_number, supplier_id, bill_id, supplier_credit_number, credit_date, reason, total, journal_id)
		SELECT $1, $2, 'X1', $3, $4, 'X1', '2026-09-30', 'x', 10, journal_id FROM supplier_bills WHERE id = $4`, f.tenantID, f.propID, sup.ID, b.ID); err == nil {
		t.Fatal("a credit note without lines is refused at commit")
	}
}
