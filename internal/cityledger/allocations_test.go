package cityledger_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/platform/apperr"
)

// invoiced issues an invoice of 500,000 over two checked-out transfers of ACME and returns it.
func (f *fx) invoiced(t *testing.T) cityledger.Invoice {
	t.Helper()
	p1, s1 := f.transferred(t, f.acme.ID, "101", "300000")
	p2, s2 := f.transferred(t, f.acme.ID, "102", "200000")
	f.checkOut(t, s1)
	f.checkOut(t, s2)
	inv, err := f.invoice(f.acme.ID, "inv-1", p1, p2)
	must(t, err)
	return inv
}

func (f *fx) payInvoice(company int64, key, amount string, allocations ...cityledger.AllocationInput) (cityledger.ReceiptResult, error) {
	return f.CityLedger.Receive(f.admin, f.propID, company, key, cityledger.ReceiptInput{Amount: amount, PaymentMethod: "BANK_TRANSFER", Allocations: allocations})
}

func (f *fx) invoiceState(t *testing.T, id int64) cityledger.Invoice {
	t.Helper()
	inv, err := f.CityLedger.GetInvoice(f.admin, f.propID, id)
	must(t, err)
	return inv
}

func TestReceiptsPayInvoicesPartlyAndInFull(t *testing.T) {
	f := setup(t)
	inv := f.invoiced(t)
	if inv.Paid != "0" || inv.Outstanding != "500000" || inv.PaymentStatus != "UNPAID" {
		t.Fatalf("new invoice: %+v", inv)
	}
	r1, err := f.payInvoice(f.acme.ID, "r1", "100000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "100000"})
	must(t, err)
	if len(r1.Receipt.Allocations) != 1 || r1.Receipt.Allocations[0].InvoiceNumber != inv.InvoiceNumber || r1.Receipt.Allocations[0].Amount != "100000" || r1.Balance != "400000" {
		t.Fatalf("receipt: %+v", r1)
	}
	if got := f.invoiceState(t, inv.ID); got.Paid != "100000" || got.Outstanding != "400000" || got.PaymentStatus != "PARTIAL" {
		t.Fatalf("partly paid: %+v", got)
	}
	// the same key replays with its allocation
	again, err := f.payInvoice(f.acme.ID, "r1", "100000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "100000"})
	must(t, err)
	if again.Receipt.ID != r1.Receipt.ID || len(again.Receipt.Allocations) != 1 || f.Count(t, `SELECT count(*) FROM city_ledger_receipt_allocations`) != 1 {
		t.Fatalf("replay: %+v", again)
	}
	// more than the invoice still owes is refused; the exact rest pays it
	_, err = f.payInvoice(f.acme.ID, "r2", "400001", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "400001"})
	wantCode(t, err, "RECEIPT_EXCEEDS_BALANCE")
	_, err = f.payInvoice(f.acme.ID, "r3", "450000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "450000"})
	wantCode(t, err, "RECEIPT_EXCEEDS_BALANCE") // the company owes 400,000 only
	r4, err := f.payInvoice(f.acme.ID, "r4", "400000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "400000"})
	must(t, err)
	if got := f.invoiceState(t, inv.ID); got.Paid != "500000" || got.Outstanding != "0" || got.PaymentStatus != "PAID" || r4.Balance != "0" {
		t.Fatalf("paid: %+v", got)
	}
	// the receipts list carries the allocations
	list, err := f.CityLedger.Receipts(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(list) != 2 || len(list[0].Allocations) != 1 || len(list[1].Allocations) != 1 {
		t.Fatalf("receipts: %+v", list)
	}
	invoices, err := f.CityLedger.Invoices(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(invoices) != 1 || invoices[0].PaymentStatus != "PAID" {
		t.Fatalf("invoice list: %+v", invoices)
	}
}

func TestAllocationRules(t *testing.T) {
	f := setup(t)
	inv := f.invoiced(t)
	// a transfer of another company's invoice is not found; an unknown invoice either
	other := f.company(t, "OTHER", "")
	po, so := f.transferred(t, other.ID, "103", "70000")
	f.checkOut(t, so)
	oi, err := f.invoice(other.ID, "oi", po)
	must(t, err)
	_, err = f.payInvoice(f.acme.ID, "a1", "1000", cityledger.AllocationInput{InvoiceID: oi.ID, Amount: "1000"})
	wantCode(t, err, "INVOICE_NOT_FOUND")
	_, err = f.payInvoice(f.acme.ID, "a2", "1000", cityledger.AllocationInput{InvoiceID: 99999, Amount: "1000"})
	wantCode(t, err, "INVOICE_NOT_FOUND")
	// the allocations may not exceed the receipt, and must be valid
	_, err = f.payInvoice(f.acme.ID, "a3", "1000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "1001"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.payInvoice(f.acme.ID, "a4", "1000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "0"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.payInvoice(f.acme.ID, "a5", "1000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "400"}, cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "400"})
	wantCode(t, err, "VALIDATION_FAILED")
	// a receipt may pay an invoice in part and keep the rest on account
	r, err := f.payInvoice(f.acme.ID, "a6", "1000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "400"})
	must(t, err)
	if got := f.invoiceState(t, inv.ID); got.Paid != "400" || r.Receipt.Amount != "1000" {
		t.Fatalf("on account: %+v", got)
	}
	// one that pays several invoices at once
	p3, s3 := f.transferred(t, f.acme.ID, "104", "90000")
	f.checkOut(t, s3)
	second, err := f.invoice(f.acme.ID, "inv-2", p3)
	must(t, err)
	multi, err := f.payInvoice(f.acme.ID, "a7", "100000",
		cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "10000"}, cityledger.AllocationInput{InvoiceID: second.ID, Amount: "90000"})
	must(t, err)
	if len(multi.Receipt.Allocations) != 2 || f.invoiceState(t, second.ID).PaymentStatus != "PAID" || f.invoiceState(t, inv.ID).Paid != "10400" {
		t.Fatalf("several invoices: %+v", multi.Receipt)
	}
	// a plain receipt still works, and without allocations pays no invoice
	if _, err := f.receive(f.acme.ID, "a8", "10"); err != nil {
		t.Fatal(err)
	}
	if f.Count(t, `SELECT count(*) FROM city_ledger_receipt_allocations`) != 3 {
		t.Fatal("three allocations")
	}
	// allocations are append-only and tie a receipt to an invoice of its own company
	if err := f.Exec(t, `UPDATE city_ledger_receipt_allocations SET amount = 1`); err == nil {
		t.Fatal("an allocation cannot change")
	}
	if err := f.Exec(t, `DELETE FROM city_ledger_receipt_allocations`); err == nil {
		t.Fatal("an allocation cannot be deleted")
	}
	if err := f.Exec(t, `INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
		VALUES ($1, $2, $3, $4, $5, 1)`, f.tenantID, f.propID, r.Receipt.ID, oi.ID, f.acme.ID); err == nil {
		t.Fatal("a receipt cannot pay another company's invoice")
	}
}

func TestVoidingReceiptsAndInvoicesWithPayments(t *testing.T) {
	f := setup(t)
	inv := f.invoiced(t)
	r, err := f.payInvoice(f.acme.ID, "r1", "100000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "100000"})
	must(t, err)
	// a paid invoice cannot be voided until its receipts are
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "INVOICE_HAS_PAYMENTS")
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, r.Receipt.ID, cityledger.VoidInput{Reason: "bounced", Approval: f.approval()})
	must(t, err)
	if got := f.invoiceState(t, inv.ID); got.Paid != "0" || got.PaymentStatus != "UNPAID" {
		t.Fatalf("after the receipt is voided: %+v", got)
	}
	// the voided receipt still shows what it was meant to pay, and the invoice is voidable again
	list, err := f.CityLedger.Receipts(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(list) != 1 || list[0].Status != "VOIDED" || len(list[0].Allocations) != 1 {
		t.Fatalf("voided receipt: %+v", list)
	}
	v, err := f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	must(t, err)
	if v.PaymentStatus != "VOID" || v.Outstanding != "0" {
		t.Fatalf("voided: %+v", v)
	}
	// a voided invoice takes no payment
	_, err = f.payInvoice(f.acme.ID, "r2", "1000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "1000"})
	wantCode(t, err, "INVOICE_NOT_PAYABLE")
}

// Two receipts that each fit the invoice alone but not together: the company lock lets exactly one in.
func TestInvoiceIsNotOverpaidUnderRace(t *testing.T) {
	f := setup(t)
	inv := f.invoiced(t)
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.payInvoice(f.acme.ID, "race-"+string(rune('a'+i)), "300000", cityledger.AllocationInput{InvoiceID: inv.ID, Amount: "300000"})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else if !apperr.IsCode(err, "RECEIPT_EXCEEDS_BALANCE") && !apperr.IsCode(err, "ALLOCATION_EXCEEDS_INVOICE") {
			t.Fatalf("unexpected: %v", err)
		}
	}
	var paid string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(amount), 0)::text FROM city_ledger_receipt_allocations`).Scan(&paid))
	if ok != 1 || f.invoiceState(t, inv.ID).Paid != "300000" || f.invoiceState(t, inv.ID).Outstanding != "200000" {
		t.Fatalf("%d receipts of 300000 fit an invoice of 500000 (paid %s)", ok, paid)
	}
}
