package payables_test

import (
	"context"
	"testing"

	"kamarapms/internal/payables"
	"kamarapms/internal/taxfiling"
)

func (f *fx) vatBill(t *testing.T, sup payables.Supplier, invoice, date, amount, vat string) (payables.Bill, error) {
	t.Helper()
	return f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
		SupplierID: sup.ID, SupplierInvoiceNumber: invoice, BillDate: d(date),
		Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Description: "Electricity", Amount: dec(amount), VATAmount: dec(vat)}},
	}, "")
}

func TestInputVATFollowsThePKPStatusOnTheBillDate(t *testing.T) {
	f := setup(t) // 30 Sep 2026; the property starts as not PKP with the VAT as an expense
	sup := f.supplier(t, "PLN", 30)

	// Not PKP: the VAT is part of the cost.
	b, err := f.vatBill(t, sup, "INV-1", "2026-09-30", "1000", "110")
	must(t, err)
	eq(t, "total", b.Total, "1110")
	if len(b.Lines) != 1 || b.Lines[0].VATTreatment != "EXPENSE" {
		t.Fatalf("line: %+v", b.Lines)
	}
	eq(t, "line VAT", b.Lines[0].VATAmount, "110")
	eq(t, "the expense carries the VAT", f.balance(t, "6510"), "1110")
	eq(t, "no input VAT", f.balance(t, "1425"), "0")
	eq(t, "payable", f.balance(t, "2110"), "-1110")

	// The hotel becomes PKP from 1 Oct (a change in the future needs no approval; today it is still 30 Sep).
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-01"), IsPKP: true, NPWP: "01.234.567.8-901.000"})
	must(t, err)
	f.closeDay(t) // the business date is 1 Oct
	// A bill of 30 Sep (a day of the open period) still follows the status of its own date; one of 1 Oct claims the VAT.
	old, err := f.vatBill(t, sup, "INV-3", "2026-09-30", "500", "55")
	must(t, err)
	if old.Lines[0].VATTreatment != "EXPENSE" {
		t.Fatalf("before the change it was an expense: %+v", old.Lines[0])
	}
	eq(t, "no input VAT yet", f.balance(t, "1425"), "0")
	b, err = f.vatBill(t, sup, "INV-2", "2026-10-01", "2000", "220")
	must(t, err)
	if b.Lines[0].VATTreatment != "CREDITABLE" {
		t.Fatalf("PKP claims the VAT: %+v", b.Lines[0])
	}
	eq(t, "the expense is the net", f.balance(t, "6510"), "3665")
	eq(t, "input VAT", f.balance(t, "1425"), "220")
	eq(t, "payable", f.balance(t, "2110"), "-3885")

	// Deferred from 2 Oct: kept apart, not claimed.
	_, err = f.Tax.ChangeSettings(f.admin, f.propID, taxfiling.SettingsInput{EffectiveFrom: d("2026-10-02"), IsPKP: true, NPWP: "01.234.567.8-901.000", InputVATTreatment: "DEFERRED"})
	must(t, err)
	f.closeDay(t) // 2 Oct
	def, err := f.vatBill(t, sup, "INV-4", "2026-10-02", "1000", "110")
	must(t, err)
	if def.Lines[0].VATTreatment != "DEFERRED" {
		t.Fatalf("deferred: %+v", def.Lines[0])
	}
	eq(t, "input VAT holds the deferred VAT too", f.balance(t, "1425"), "330")
	f.requireBalanced(t)

	// A line without VAT has no treatment.
	plain := f.bill(t, sup, "INV-5", "300", "")
	if plain.Lines[0].VATTreatment != "" || !plain.Lines[0].VATAmount.IsZero() {
		t.Fatalf("no VAT: %+v", plain.Lines[0])
	}

	// Voiding reverses the journal as it was booked.
	_, err = f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "wrong supplier", Approval: f.approval()})
	must(t, err)
	eq(t, "input VAT after the void", f.balance(t, "1425"), "110")
	f.requireBalanced(t)
}

func TestInputVATValidationAndTotals(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	_, err := f.vatBill(t, sup, "INV-1", "2026-09-30", "1000", "-1")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.vatBill(t, sup, "INV-1", "2026-09-30", "1000", "10.5") // IDR has no decimals
	wantCode(t, err, "VALIDATION_FAILED")

	// The database holds the same rules without the service: VAT needs its treatment, and the lines add up to the total.
	b := f.bill(t, sup, "INV-2", "1000", "")
	_, err = f.Pool.Exec(context.Background(), `INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount, vat_amount) VALUES ($1, $2, $3, 2, $4, 100, 10)`,
		f.tenantID, f.propID, b.ID, f.acc["6510"])
	if err == nil {
		t.Error("VAT without a treatment was accepted")
	}
	_, err = f.Pool.Exec(context.Background(), `INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount, vat_amount, vat_treatment) VALUES ($1, $2, $3, 2, $4, 100, 10, 'SOMETIMES')`,
		f.tenantID, f.propID, b.ID, f.acc["6510"])
	if err == nil {
		t.Error("an unknown treatment was accepted")
	}
}

func TestANewPropertyHasTheInputVATAccount(t *testing.T) {
	f := setup(t)
	if f.acc["1425"] == 0 {
		t.Fatal("the chart has no account 1425")
	}
	var mapped int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT account_id FROM gl_account_map WHERE property_id = $1 AND map_key = 'INPUT_VAT'`, f.propID).Scan(&mapped))
	if mapped != f.acc["1425"] {
		t.Fatalf("INPUT_VAT is mapped to %d, want %d", mapped, f.acc["1425"])
	}
}
