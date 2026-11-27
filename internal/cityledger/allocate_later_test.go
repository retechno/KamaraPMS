package cityledger_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/cityledger"
	"kamarapms/internal/platform/auth"
)

// KD-27 (docs/architecture/41-receipt-allocation.md): a receipt of a company recorded without allocations is allocated to its
// invoices later, and a wrong allocation is released.

func (f *fx) allocate(ctx context.Context, receiptID int64, key string, allocations ...cityledger.AllocationInput) (cityledger.ReceiptResult, error) {
	return f.CityLedger.Allocate(ctx, f.propID, receiptID, key, cityledger.AllocateInput{Allocations: allocations})
}

func (f *fx) release(allocationID int64, reason string) (cityledger.ReceiptResult, error) {
	return f.CityLedger.ReleaseAllocation(f.admin, f.propID, allocationID, cityledger.VoidInput{Reason: reason, Approval: f.approval()})
}

func (f *fx) journals(t *testing.T) int {
	t.Helper()
	return f.Count(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)
}

func (f *fx) receiptOf(t *testing.T, id int64) cityledger.Receipt {
	t.Helper()
	list, err := f.CityLedger.Receipts(f.admin, f.propID, f.acme.ID)
	must(t, err)
	for _, r := range list {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("receipt %d not listed", id)
	return cityledger.Receipt{}
}

func alloc(invoiceID int64, amount string) cityledger.AllocationInput {
	return cityledger.AllocationInput{InvoiceID: invoiceID, Amount: amount}
}

// 1, 2: a receipt of day 1 without allocations is allocated on day 2 to two invoices; the books and the aging do not move.
func TestAReceiptIsAllocatedOnALaterDay(t *testing.T) {
	f := setup(t)
	a, _ := f.invoicedOne(t, "101", "300000")
	b, _ := f.invoicedOne(t, "102", "200000")
	r, err := f.payInvoice(f.acme.ID, "r1", "450000") // nothing allocated: both invoices stay unpaid
	must(t, err)
	if r.Receipt.Allocated != "0" || r.Receipt.Unallocated != "450000" || f.balance(t, f.acme.ID).Unallocated != "450000" {
		t.Fatalf("on account: %+v / %+v", r.Receipt, f.balance(t, f.acme.ID))
	}
	day1, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.advance(t) // the night audit closes day 1: the receipt can no longer be voided
	day2, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)

	journals, cl := f.journals(t), f.balanceOf(t, "1220").String()
	agingBefore, err := f.CityLedger.Aging(f.admin, f.propID, f.acme.ID)
	must(t, err)
	got, err := f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(a.ID, "300000"), alloc(b.ID, "150000"))
	must(t, err)
	if got.Receipt.Allocated != "450000" || got.Receipt.Unallocated != "0" || len(got.Receipt.Allocations) != 2 || got.Balance != "50000" {
		t.Fatalf("allocated: %+v", got)
	}
	for _, al := range got.Receipt.Allocations {
		if !al.AllocatedOn.Equal(day2.BusinessDate) || al.ReleasedAt != nil {
			t.Fatalf("allocation of day 2, live: %+v (day 1 %s)", al, day1.BusinessDate)
		}
	}
	if inv := f.invoiceState(t, a.ID); inv.PaymentStatus != "PAID" || inv.Outstanding != "0" {
		t.Fatalf("invoice A: %+v", inv)
	}
	if inv := f.invoiceState(t, b.ID); inv.PaymentStatus != "PARTIAL" || inv.Outstanding != "50000" {
		t.Fatalf("invoice B: %+v", inv)
	}
	if acc := f.balance(t, f.acme.ID); acc.Unallocated != "0" || acc.Balance != "50000" {
		t.Fatalf("account: %+v", acc)
	}
	// no journal, the control account and the aging are where they were
	if f.journals(t) != journals || f.balanceOf(t, "1220").String() != cl {
		t.Fatalf("an allocation posts no journal: %d -> %d, 1220 %s -> %s", journals, f.journals(t), cl, f.balanceOf(t, "1220"))
	}
	agingAfter, err := f.CityLedger.Aging(f.admin, f.propID, f.acme.ID)
	must(t, err)
	for i := range agingBefore.Buckets {
		if agingBefore.Buckets[i] != agingAfter.Buckets[i] {
			t.Fatalf("the aging moved: %+v -> %+v", agingBefore.Buckets, agingAfter.Buckets)
		}
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cityledger.receipt_allocated' AND entity_id = $1`, r.Receipt.ID) != 1 {
		t.Fatal("the allocation is audited")
	}
}

// 3, 4, 5, 6: the rules of an allocation made later.
func TestAllocatingLaterRules(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "500000")
	_, err := f.payInvoice(f.acme.ID, "r1", "600000", alloc(inv.ID, "300000"))
	wantCode(t, err, "RECEIPT_EXCEEDS_BALANCE") // the company owes 500,000
	r, err := f.payInvoice(f.acme.ID, "r1b", "500000", alloc(inv.ID, "300000"))
	must(t, err)
	// the same receipt pays 150,000 more of the same invoice: a second row (A4)
	got, err := f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(inv.ID, "150000"))
	must(t, err)
	if len(got.Receipt.Allocations) != 2 || got.Receipt.Unallocated != "50000" || f.invoiceState(t, inv.ID).Paid != "450000" {
		t.Fatalf("a second allocation of the same invoice: %+v", got.Receipt)
	}
	// more than is left of the receipt
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-2", alloc(inv.ID, "50001"))
	wantCode(t, err, "ALLOCATION_EXCEEDS_UNALLOCATED")
	// more than the invoice owes, counting a credit note of it
	_, err = f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "20000"})
	must(t, err)
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-3", alloc(inv.ID, "40000")) // it owes 500,000 - 450,000 - 20,000 = 30,000
	wantCode(t, err, "ALLOCATION_EXCEEDS_INVOICE")
	// invalid requests
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-4")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-5", alloc(inv.ID, "0"))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-6", alloc(inv.ID, "100"), alloc(inv.ID, "100"))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.allocate(f.admin, r.Receipt.ID, "", alloc(inv.ID, "100"))
	wantCode(t, err, "VALIDATION_FAILED")
	// an invoice of another company, an unknown one, an unknown receipt
	other := f.company(t, "OTHER", "")
	po, so := f.transferred(t, other.ID, "103", "70000")
	f.checkOut(t, so)
	oi, err := f.invoice(other.ID, "oi", po)
	must(t, err)
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-7", alloc(oi.ID, "100"))
	wantCode(t, err, "INVOICE_NOT_FOUND")
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-8", alloc(99999, "100"))
	wantCode(t, err, "INVOICE_NOT_FOUND")
	_, err = f.allocate(f.admin, 99999, "al-9", alloc(inv.ID, "100"))
	wantCode(t, err, "RECEIPT_NOT_FOUND")
	// a voided receipt allocates nothing; a voided invoice takes nothing
	free, _ := f.invoicedOne(t, "104", "30000") // the company owes 30,000 again (the credit note took 20,000)
	plain, err := f.receive(f.acme.ID, "r2", "10000")
	must(t, err)
	_, err = f.CityLedger.VoidReceipt(f.admin, f.propID, plain.Receipt.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	_, err = f.allocate(f.admin, plain.Receipt.ID, "al-10", alloc(inv.ID, "100"))
	wantCode(t, err, "RECEIPT_ALREADY_VOIDED")
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, free.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	_, err = f.allocate(f.admin, r.Receipt.ID, "al-11", alloc(free.ID, "100"))
	wantCode(t, err, "INVOICE_NOT_PAYABLE")
}

// 7: the Idempotency-Key of an allocation.
func TestAllocatingLaterIsIdempotent(t *testing.T) {
	f := setup(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	r, err := f.payInvoice(f.acme.ID, "r1", "100000")
	must(t, err)
	first, err := f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(inv.ID, "60000"))
	must(t, err)
	again, err := f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(inv.ID, "60000"))
	must(t, err)
	if again.Receipt.Unallocated != first.Receipt.Unallocated || f.Count(t, `SELECT count(*) FROM city_ledger_receipt_allocations`) != 1 {
		t.Fatalf("a replay writes nothing: %+v", again.Receipt)
	}
	other, err := f.payInvoice(f.acme.ID, "r2", "10000")
	must(t, err)
	_, err = f.allocate(f.admin, other.Receipt.ID, "al-1", alloc(inv.ID, "100"))
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
}

// 8, 9, 10: the overdue list follows the allocations; a release makes the invoice owe again; a reminder keeps what it listed.
func TestAReleaseMakesTheInvoiceOweAgain(t *testing.T) {
	f := setup(t)
	co, inv := f.dueToday(t, "ZERO", "101", "300000")
	r, err := f.payInvoice(co.ID, "r1", "300000")
	must(t, err)
	f.advance(t) // the invoice is overdue
	od, err := f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	if len(od.Companies) != 1 {
		t.Fatalf("overdue before the allocation: %+v", od.Companies)
	}
	rem, err := f.CityLedger.CreateReminder(f.admin, f.propID, co.ID, "m1", cityledger.ReminderInput{Level: 1, InvoiceIDs: []int64{inv.ID}})
	must(t, err)
	got, err := f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(inv.ID, "300000"))
	must(t, err)
	od, err = f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	if len(od.Companies) != 0 {
		t.Fatalf("a paid invoice is not overdue: %+v", od.Companies)
	}
	if kept, err := f.CityLedger.GetReminder(f.admin, f.propID, rem.ID); err != nil || kept.TotalOutstanding != "300000" || len(kept.Items) != 1 {
		t.Fatalf("the reminder keeps what it listed: %+v %v", kept, err)
	}
	// the release: a reason and an approval are required
	id := got.Receipt.Allocations[0].ID
	_, err = f.CityLedger.ReleaseAllocation(f.admin, f.propID, id, cityledger.VoidInput{Reason: "", Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.CityLedger.ReleaseAllocation(f.admin, f.propID, id, cityledger.VoidInput{Reason: "wrong invoice"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	// an invoice with a live allocation cannot be voided
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	wantCode(t, err, "INVOICE_HAS_PAYMENTS")
	rel, err := f.release(id, "wrong invoice")
	must(t, err)
	if rel.Receipt.Unallocated != "300000" || rel.Receipt.Allocations[0].ReleasedAt == nil || rel.Receipt.Allocations[0].ReleaseReason != "wrong invoice" {
		t.Fatalf("released: %+v", rel.Receipt)
	}
	if inv := f.invoiceState(t, inv.ID); inv.Outstanding != "300000" || inv.PaymentStatus != "UNPAID" {
		t.Fatalf("the invoice owes again: %+v", inv)
	}
	od, err = f.CityLedger.OverdueList(f.admin, f.propID)
	must(t, err)
	if len(od.Companies) != 1 {
		t.Fatalf("overdue again after the release: %+v", od.Companies)
	}
	_, err = f.release(id, "again")
	wantCode(t, err, "ALLOCATION_ALREADY_RELEASED")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'cityledger.allocation_released' AND entity_id = $1 AND new_data ? 'approved_by'`, r.Receipt.ID) != 1 {
		t.Fatal("the release is audited with its approver")
	}
	// with no live allocation left, the invoice can be voided
	if _, err := f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()}); err != nil {
		t.Fatalf("void after the release: %v", err)
	}
	// a release of an allocation of a voided invoice is refused (nothing is live there)
	_, err = f.release(99999, "x")
	wantCode(t, err, "ALLOCATION_NOT_FOUND")
}

// 11: permissions and tenant isolation.
func TestAllocatingLaterPermissions(t *testing.T) {
	f := setup(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	r, err := f.payInvoice(f.acme.ID, "r1", "100000")
	must(t, err)
	reader := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerRead)
	_, err = f.allocate(reader, r.Receipt.ID, "al-1", alloc(inv.ID, "100"))
	wantCode(t, err, "PERMISSION_DENIED")
	if list, err := f.CityLedger.Receipts(reader, f.propID, f.acme.ID); err != nil || list[0].Unallocated != "100000" {
		t.Fatalf("a reader sees the unallocated part: %+v %v", list, err)
	}
	clerk := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerReceive)
	got, err := f.allocate(clerk, r.Receipt.ID, "al-2", alloc(inv.ID, "100"))
	must(t, err)
	_, err = f.CityLedger.ReleaseAllocation(reader, f.propID, got.Receipt.Allocations[0].ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	// another tenant sees neither the receipt nor the allocation
	tn := f.Tenant(t, "XYZ")
	p := f.Property(t, tn.ID, "JAVA")
	stranger, _ := f.AdminAccount(t, tn.ID)
	_, err = f.CityLedger.Allocate(stranger, p.ID, r.Receipt.ID, "al-3", cityledger.AllocateInput{Allocations: []cityledger.AllocationInput{alloc(inv.ID, "100")}})
	wantCode(t, err, "RECEIPT_NOT_FOUND")
	_, err = f.CityLedger.ReleaseAllocation(stranger, p.ID, got.Receipt.Allocations[0].ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	if err == nil {
		t.Fatal("another tenant cannot release")
	}
	_, err = f.CityLedger.Allocate(f.admin, p.ID, r.Receipt.ID, "al-4", cityledger.AllocateInput{Allocations: []cityledger.AllocationInput{alloc(inv.ID, "100")}})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// The schema: an allocation is only released, once; the live allocations of a receipt stay within its amount; a closed day takes none.
func TestAllocationSchemaGuards(t *testing.T) {
	f := setup(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	r, err := f.payInvoice(f.acme.ID, "r1", "100000", alloc(inv.ID, "40000"))
	must(t, err)
	id := r.Receipt.Allocations[0].ID
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	var user int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT created_by FROM city_ledger_receipt_allocations WHERE id = $1`, id).Scan(&user))
	for what, sql := range map[string]string{
		"the amount":          `UPDATE city_ledger_receipt_allocations SET amount = 1 WHERE id = $1`,
		"a delete":            `DELETE FROM city_ledger_receipt_allocations WHERE id = $1`,
		"a partial release":   `UPDATE city_ledger_receipt_allocations SET released_at = now() WHERE id = $1`,
		"a release with more": `UPDATE city_ledger_receipt_allocations SET released_at = now(), released_on = allocated_on, released_by = created_by, release_reason = 'x', release_approved_by = created_by, amount = 2 WHERE id = $1`,
	} {
		if err := f.Exec(t, sql, id); err == nil {
			t.Errorf("%s is refused", what)
		}
	}
	if err := f.Exec(t, `UPDATE city_ledger_receipt_allocations SET released_at = now(), released_on = $2, released_by = $3, release_reason = 'x', release_approved_by = $3 WHERE id = $1`, id, day.BusinessDate, user); err != nil {
		t.Fatalf("a release is allowed: %v", err)
	}
	if err := f.Exec(t, `UPDATE city_ledger_receipt_allocations SET release_reason = 'y' WHERE id = $1`, id); err == nil {
		t.Error("a released allocation does not change again")
	}
	// two inserts that together pass the receipt's amount are refused at commit
	ctx := context.Background()
	tx, err := f.Pool.Begin(ctx)
	must(t, err)
	for _, amount := range []string{"60000", "60000"} {
		if _, err := tx.Exec(ctx, `INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount, allocated_on) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			f.tenantID, f.propID, r.Receipt.ID, inv.ID, f.acme.ID, amount, day.BusinessDate); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(ctx); err == nil {
		t.Fatal("120,000 allocated of a receipt of 100,000")
	}
	// a closed day takes no allocation
	f.advance(t)
	if err := f.Exec(t, `INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount, allocated_on) VALUES ($1, $2, $3, $4, $5, 1, $6)`,
		f.tenantID, f.propID, r.Receipt.ID, inv.ID, f.acme.ID, day.BusinessDate); err == nil {
		t.Fatal("an allocation dated a closed day")
	}
}

// 12: two allocations of the same receipt at once, each within what is left of it but together above it.
func TestAllocationsOfOneReceiptUnderRace(t *testing.T) {
	f := setup(t)
	a, _ := f.invoicedOne(t, "101", "300000")
	b, _ := f.invoicedOne(t, "102", "300000")
	r, err := f.payInvoice(f.acme.ID, "r1", "100000")
	must(t, err)
	const n = 6
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			target := a.ID
			if i%2 == 1 {
				target = b.ID
			}
			_, errs[i] = f.allocate(f.admin, r.Receipt.ID, "race-"+string(rune('a'+i)), alloc(target, "60000"))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "ALLOCATION_EXCEEDS_UNALLOCATED")
		}
	}
	if got := f.receiptOf(t, r.Receipt.ID); ok != 1 || got.Unallocated != "40000" {
		t.Fatalf("%d allocations of 60000 fit a receipt of 100000 (left %s)", ok, got.Unallocated)
	}
}

// 13: an allocation and a new receipt with an allocation to the same invoice at once, together above what it owes.
func TestAnAllocationAndANewReceiptToOneInvoiceUnderRace(t *testing.T) {
	f := setup(t)
	inv, _ := f.invoicedOne(t, "101", "100000")
	other, _ := f.invoicedOne(t, "102", "500000") // the company owes enough for both receipts
	_ = other
	r, err := f.payInvoice(f.acme.ID, "r1", "70000")
	must(t, err)
	for round := 0; round < 3; round++ {
		var wg sync.WaitGroup
		var errAlloc, errReceipt error
		var newReceipt cityledger.ReceiptResult
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, errAlloc = f.allocate(f.admin, r.Receipt.ID, "al-"+string(rune('a'+round)), alloc(inv.ID, "70000"))
		}()
		go func() {
			defer wg.Done()
			newReceipt, errReceipt = f.payInvoice(f.acme.ID, "r2-"+string(rune('a'+round)), "70000", alloc(inv.ID, "70000"))
		}()
		wg.Wait()
		if (errAlloc == nil) == (errReceipt == nil) {
			t.Fatalf("round %d: exactly one pays the invoice (allocation %v, receipt %v)", round, errAlloc, errReceipt)
		}
		if errAlloc != nil {
			wantCode(t, errAlloc, "ALLOCATION_EXCEEDS_INVOICE")
		} else {
			wantCode(t, errReceipt, "ALLOCATION_EXCEEDS_INVOICE")
		}
		if got := f.invoiceState(t, inv.ID); got.Paid != "70000" {
			t.Fatalf("round %d: the invoice is paid %s of 100000", round, got.Paid)
		}
		// undo the round so the next one starts from the same state
		if errAlloc == nil {
			rc := f.receiptOf(t, r.Receipt.ID)
			for _, al := range rc.Allocations {
				if al.ReleasedAt == nil {
					_, err := f.release(al.ID, "next round")
					must(t, err)
				}
			}
		} else {
			_, err := f.CityLedger.VoidReceipt(f.admin, f.propID, newReceipt.Receipt.ID, cityledger.VoidInput{Reason: "next round", Approval: f.approval()})
			must(t, err)
		}
	}
}

// 14: an allocation and the release of another allocation of the same receipt at once.
func TestAnAllocationAndAReleaseUnderRace(t *testing.T) {
	f := setup(t)
	a, _ := f.invoicedOne(t, "101", "300000")
	b, _ := f.invoicedOne(t, "102", "300000")
	r, err := f.payInvoice(f.acme.ID, "r1", "100000", alloc(a.ID, "50000"))
	must(t, err)
	var wg sync.WaitGroup
	var errAlloc, errRelease error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errAlloc = f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(b.ID, "50000"))
	}()
	go func() {
		defer wg.Done()
		_, errRelease = f.release(r.Receipt.Allocations[0].ID, "wrong invoice")
	}()
	wg.Wait()
	must(t, errAlloc)
	must(t, errRelease)
	if got := f.receiptOf(t, r.Receipt.ID); got.Allocated != "50000" || got.Unallocated != "50000" {
		t.Fatalf("after both: %+v", got)
	}
}

// 15: an allocation and a credit note to the same invoice at once: the invoice never owes below zero.
func TestAnAllocationAndACreditNoteUnderRace(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "100000")
	r, err := f.payInvoice(f.acme.ID, "r1", "80000")
	must(t, err)
	var wg sync.WaitGroup
	var errAlloc, errNote error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errAlloc = f.allocate(f.admin, r.Receipt.ID, "al-1", alloc(inv.ID, "80000"))
	}()
	go func() {
		defer wg.Done()
		_, errNote = f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "50000"})
	}()
	wg.Wait()
	if (errAlloc == nil) == (errNote == nil) {
		t.Fatalf("exactly one fits (allocation %v, credit note %v)", errAlloc, errNote)
	}
	if got := f.invoiceState(t, inv.ID); got.Outstanding != "20000" && got.Outstanding != "50000" {
		t.Fatalf("outstanding %s", got.Outstanding)
	}
}
