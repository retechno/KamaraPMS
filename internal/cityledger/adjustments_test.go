package cityledger_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/cityledger"
	"kamarapms/internal/departments"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
)

type chart struct {
	allowance int64 // 4160 rooms allowances (revenue)
	badDebt   int64 // 6140 bad debt expense
	provision int64 // 1240 allowance for doubtful accounts
	cash      int64 // 1110 (not a revenue account)
	pb1       billingconfig.Tax
}

func (f *fx) chart(t *testing.T) chart {
	t.Helper()
	list, err := f.Accounting.Accounts(f.admin, f.propID, accounting.AccountFilter{})
	must(t, err)
	by := map[string]int64{}
	for _, a := range list {
		by[a.Code] = a.ID
	}
	pb1, err := f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: "PB1", Name: "Hotel tax", Rate: "10", GLAccountCode: "2410", IsActive: true})
	must(t, err)
	return chart{allowance: by["4160"], badDebt: by["6140"], provision: by["1240"], cash: by["1110"], pb1: pb1}
}

// balanceOf is the debit balance of an account of the books by its code.
func (f *fx) balanceOf(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.code = $2`, f.propID, code).Scan(&s))
	return decimal.RequireFromString(s)
}

func eqd(t *testing.T, what, got, want string) {
	t.Helper()
	if !decimal.RequireFromString(got).Equal(decimal.RequireFromString(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

// invoiced makes an invoice of one fresh transfer of amount.
func (f *fx) invoicedOne(t *testing.T, room, amount string) (cityledger.Invoice, int64) {
	t.Helper()
	pay, stay := f.transferred(t, f.acme.ID, room, amount)
	f.checkOut(t, stay)
	inv, err := f.invoice(f.acme.ID, "inv-"+room, pay)
	must(t, err)
	return inv, pay
}

func (f *fx) note(invoiceID *int64, paymentID *int64, key string, lines ...cityledger.CreditNoteLineInput) (cityledger.Adjustment, error) {
	return f.CityLedger.CreateCreditNote(f.admin, f.propID, key, cityledger.CreditNoteInput{InvoiceID: invoiceID, PaymentID: paymentID, Reason: "settled after the invoice", Lines: lines, Approval: f.approval()})
}

func ptr[T any](v T) *T { return &v }

func TestACreditNoteAgainstAnInvoiceLowersWhatItOwesAndIsJournaled(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	// 100,000 of allowance and the hotel tax (10%) on it: 110,000 off the invoice
	line := cityledger.CreditNoteLineInput{Description: "Room rate dispute", AccountID: c.allowance, NetAmount: "100000", TaxID: &c.pb1.ID}
	n, err := f.note(&inv.ID, nil, "cn1", line)
	must(t, err)
	if n.Kind != "CREDIT_NOTE" || n.Number != "CN000001" || n.Status != "POSTED" || n.InvoiceNumber != inv.InvoiceNumber || n.JournalNumber == "" || len(n.Lines) != 1 {
		t.Fatalf("credit note: %+v", n)
	}
	eqd(t, "amount", n.Amount, "110000")
	eqd(t, "tax of the line", n.Lines[0].TaxAmount, "10000")
	eqd(t, "net of the line", n.Lines[0].NetAmount, "100000")
	if n.Lines[0].TaxCode != "PB1" || n.Lines[0].AccountCode != "4160" {
		t.Fatalf("line: %+v", n.Lines[0])
	}
	got, err := f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "invoice outstanding", got.Outstanding, "190000")
	eqd(t, "credited", got.Credited, "110000")
	if got.PaymentStatus != "PARTIAL" {
		t.Fatalf("status: %s", got.PaymentStatus)
	}
	acc := f.balance(t, f.acme.ID)
	eqd(t, "balance", acc.Balance, "190000")
	eqd(t, "adjusted", acc.Adjusted, "110000")
	// the books: the allowance and the tax are debited, the city ledger credited
	eqd(t, "allowance", f.balanceOf(t, "4160").String(), "100000")
	eqd(t, "tax payable", f.balanceOf(t, "2410").String(), "10000")
	eqd(t, "city ledger", f.balanceOf(t, "1220").String(), "-110000")
	// the statement has it on the credit side
	st, err := f.CityLedger.Statement(f.admin, f.propID, f.acme.ID, nil, nil)
	must(t, err)
	last := st.Lines[len(st.Lines)-1]
	if last.Kind != "CREDIT_NOTE" || last.Number != n.Number {
		t.Fatalf("statement: %+v", st.Lines)
	}
	eqd(t, "statement closing", st.ClosingBalance, "190000")
	// a retry returns it; more than is owed is refused
	again, err := f.note(&inv.ID, nil, "cn1", line)
	must(t, err)
	if again.ID != n.ID || f.Count(t, `SELECT count(*) FROM city_ledger_adjustments`) != 1 {
		t.Fatalf("a replay makes nothing: %+v", again)
	}
	_, err = f.note(&inv.ID, nil, "cn2", cityledger.CreditNoteLineInput{Description: "Too much", AccountID: c.allowance, NetAmount: "200000"})
	wantCode(t, err, "ADJUSTMENT_EXCEEDS_INVOICE")
	// void: the journal is reversed and the invoice owes it again
	_, err = f.CityLedger.VoidAdjustment(f.admin, f.propID, n.ID, cityledger.VoidInput{Reason: "wrong"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	v, err := f.CityLedger.VoidAdjustment(f.admin, f.propID, n.ID, cityledger.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || v.VoidReason != "wrong" {
		t.Fatalf("voided: %+v", v)
	}
	got, err = f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "outstanding again", got.Outstanding, "300000")
	eqd(t, "allowance reversed", f.balanceOf(t, "4160").String(), "0")
	eqd(t, "city ledger reversed", f.balanceOf(t, "1220").String(), "0")
	_, err = f.CityLedger.VoidAdjustment(f.admin, f.propID, n.ID, cityledger.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "ADJUSTMENT_ALREADY_VOIDED")
	if err := f.Exec(t, `UPDATE city_ledger_adjustments SET amount = 1`); err == nil {
		t.Error("an adjustment was changed")
	}
	if err := f.Exec(t, `DELETE FROM city_ledger_adjustments`); err == nil {
		t.Error("an adjustment was deleted")
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('cityledger.credit_note_made', 'cityledger.adjustment_voided')`); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestACreditNoteMustBeBookedOnARevenueAccountWithAnApproval(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	_, err := f.note(&inv.ID, nil, "a", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.cash, NetAmount: "1000"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.note(&inv.ID, nil, "b", cityledger.CreditNoteLineInput{Description: "x", AccountID: 999999, NetAmount: "1000"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.note(&inv.ID, nil, "c", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "-5"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.note(&inv.ID, nil, "d")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.CityLedger.CreateCreditNote(f.admin, f.propID, "e", cityledger.CreditNoteInput{InvoiceID: &inv.ID, Reason: "x", Lines: []cityledger.CreditNoteLineInput{{Description: "x", AccountID: c.allowance, NetAmount: "1000"}}})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.CityLedger.CreateCreditNote(f.admin, f.propID, "f", cityledger.CreditNoteInput{InvoiceID: &inv.ID, PaymentID: ptr(int64(1)), Reason: "x", Lines: []cityledger.CreditNoteLineInput{{Description: "x", AccountID: c.allowance, NetAmount: "1000"}}, Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermCityLedgerRead)
	_, err = f.CityLedger.CreateCreditNote(viewer, f.propID, "g", cityledger.CreditNoteInput{InvoiceID: &inv.ID, Reason: "x", Lines: []cityledger.CreditNoteLineInput{{Description: "x", AccountID: c.allowance, NetAmount: "1000"}}, Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	if f.Count(t, `SELECT count(*) FROM city_ledger_adjustments`) != 0 {
		t.Fatal("nothing was made")
	}
}

// A transfer is corrected before it is invoiced: the invoice asks the net, one document for the company.
func TestACreditNoteOfATransferIsAttachedToTheInvoiceMadeFromIt(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	pay, stay := f.transferred(t, f.acme.ID, "101", "500000")
	f.checkOut(t, stay)
	n, err := f.note(nil, &pay, "n1", cityledger.CreditNoteLineInput{Description: "Charge corrected", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	if n.PaymentID == nil || n.InvoiceID != nil || n.AttachedInvoiceID != nil {
		t.Fatalf("a note of a transfer: %+v", n)
	}
	eqd(t, "company owes", f.balance(t, f.acme.ID).Balance, "400000")
	cands, err := f.CityLedger.Candidates(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(cands) != 1 || cands[0].Amount != "500000" || cands[0].Credited != "100000" || cands[0].Net != "400000" || !cands[0].Invoiceable {
		t.Fatalf("candidates: %+v", cands)
	}
	// more than the transfer says is refused
	_, err = f.note(nil, &pay, "n2", cityledger.CreditNoteLineInput{Description: "Too much", AccountID: c.allowance, NetAmount: "450000"})
	wantCode(t, err, "ADJUSTMENT_EXCEEDS_TRANSFER")
	inv, err := f.invoice(f.acme.ID, "i1", pay)
	must(t, err)
	eqd(t, "invoice total", inv.Total, "400000")
	eqd(t, "subtotal", inv.Subtotal, "500000")
	eqd(t, "outstanding", inv.Outstanding, "400000")
	if len(inv.AttachedCredits) != 1 || inv.AttachedCredits[0].Number != n.Number {
		t.Fatalf("attached: %+v", inv.AttachedCredits)
	}
	// the transfer is on an invoice now: the note is made against the invoice; the attached note is voided with the invoice
	_, err = f.note(nil, &pay, "n3", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "1000"})
	wantCode(t, err, "TRANSFER_ON_INVOICE")
	_, err = f.CityLedger.VoidAdjustment(f.admin, f.propID, n.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "ADJUSTMENT_ON_INVOICE")
	got, err := f.CityLedger.GetAdjustment(f.admin, f.propID, n.ID)
	must(t, err)
	if got.AttachedInvoiceID == nil || *got.AttachedInvoiceID != inv.ID {
		t.Fatalf("attached to the invoice: %+v", got)
	}
	// a credit note on the invoice takes off what is left; the account owes 400,000 less that
	more, err := f.note(&inv.ID, nil, "n4", cityledger.CreditNoteLineInput{Description: "Another", AccountID: c.allowance, NetAmount: "50000"})
	must(t, err)
	eqd(t, "account", f.balance(t, f.acme.ID).Balance, "350000")
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "INVOICE_HAS_ADJUSTMENTS")
	_, err = f.CityLedger.VoidAdjustment(f.admin, f.propID, more.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	must(t, err)
	if err := f.Exec(t, `UPDATE city_ledger_invoices SET status = 'VOIDED', voided_at = now(), void_reason = 'x' WHERE id = $1 AND NOT EXISTS (SELECT 1 FROM city_ledger_adjustments WHERE invoice_id = $1 AND status = 'POSTED')`, inv.ID); err != nil {
		t.Fatalf("the database lets an invoice without live notes be voided: %v", err)
	}
	if err := f.Exec(t, `UPDATE city_ledger_invoices SET status = 'ISSUED', voided_at = NULL, void_reason = NULL WHERE id = $1`, inv.ID); err == nil {
		t.Error("an invoice came back to life")
	}
}

func TestVoidingTheInvoiceSetsTheNoteOfItsTransferFree(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	pay, stay := f.transferred(t, f.acme.ID, "101", "500000")
	f.checkOut(t, stay)
	n, err := f.note(nil, &pay, "n1", cityledger.CreditNoteLineInput{Description: "Corrected", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	inv, err := f.invoice(f.acme.ID, "i1", pay)
	must(t, err)
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "re-invoice", Approval: f.approval()})
	must(t, err)
	got, err := f.CityLedger.GetAdjustment(f.admin, f.propID, n.ID)
	must(t, err)
	if got.AttachedInvoiceID != nil || got.Status != "POSTED" {
		t.Fatalf("the note is free again: %+v", got)
	}
	again, err := f.invoice(f.acme.ID, "i2", pay)
	must(t, err)
	eqd(t, "the new invoice asks the net again", again.Total, "400000")
	if len(again.AttachedCredits) != 1 {
		t.Fatalf("attached again: %+v", again.AttachedCredits)
	}
}

func TestATransferCreditedInFullHasNothingToInvoice(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	pay, stay := f.transferred(t, f.acme.ID, "101", "100000")
	f.checkOut(t, stay)
	_, err := f.note(nil, &pay, "n1", cityledger.CreditNoteLineInput{Description: "Everything", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	cands, err := f.CityLedger.Candidates(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(cands) != 1 || cands[0].Invoiceable {
		t.Fatalf("a fully credited transfer is not invoiceable: %+v", cands)
	}
	_, err = f.invoice(f.acme.ID, "i1", pay)
	wantCode(t, err, "NOTHING_TO_INVOICE")
	eqd(t, "owes nothing", f.balance(t, f.acme.ID).Balance, "0")
}

func TestAWriteOffTakesOffWhatAnInvoiceStillOwes(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	// part of the invoice is paid, part written off
	_, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	_, err = f.CityLedger.Receive(f.admin, f.propID, f.acme.ID, "r2", cityledger.ReceiptInput{Amount: "50000", PaymentMethod: "CASH", Allocations: []cityledger.AllocationInput{{InvoiceID: inv.ID, Amount: "50000"}}})
	must(t, err)
	wo := func(amount string, account int64, key string) (cityledger.Adjustment, error) {
		return f.CityLedger.CreateWriteOff(f.admin, f.propID, key, cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: amount, AccountID: account, Reason: "the company is closed", Approval: f.approval()})
	}
	_, err = wo("300000", c.badDebt, "w0")
	wantCode(t, err, "ADJUSTMENT_EXCEEDS_INVOICE")
	_, err = wo("1000", c.allowance, "w1") // a revenue account is not an expense
	wantCode(t, err, "VALIDATION_FAILED")
	w, err := wo("200000", c.badDebt, "w2")
	must(t, err)
	if w.Kind != "WRITE_OFF" || w.Number != "WO000001" || w.DebitAccountCode != "6140" || w.InvoiceNumber != inv.InvoiceNumber || w.Status != "POSTED" {
		t.Fatalf("write-off: %+v", w)
	}
	got, err := f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "outstanding", got.Outstanding, "50000")
	eqd(t, "written off", got.WrittenOff, "200000")
	eqd(t, "bad debt", f.balanceOf(t, "6140").String(), "200000")
	eqd(t, "city ledger", f.balanceOf(t, "1220").String(), "-200000")
	// the rest is paid: the invoice is settled; it cannot be voided while the write-off is live
	_, err = f.CityLedger.VoidInvoice(f.admin, f.propID, inv.ID, cityledger.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "INVOICE_HAS_ADJUSTMENTS")
	// the allowance for doubtful accounts takes a write-off too
	w2, err := wo("50000", c.provision, "w3")
	must(t, err)
	got, err = f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "outstanding", got.Outstanding, "0")
	if got.PaymentStatus != "WRITTEN_OFF" {
		t.Fatalf("status: %s", got.PaymentStatus)
	}
	_, err = wo("1", c.badDebt, "w4")
	wantCode(t, err, "ADJUSTMENT_EXCEEDS_INVOICE")
	// void: owed again
	_, err = f.CityLedger.VoidAdjustment(f.admin, f.propID, w2.ID, cityledger.VoidInput{Reason: "paid after all", Approval: f.approval()})
	must(t, err)
	got, err = f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "owed again", got.Outstanding, "50000")
	eqd(t, "the account has 100,000 on account and the invoice owes 50,000", f.balance(t, f.acme.ID).Balance, "-50000")
}

func TestAReceiptCannotPayWhatACreditNoteTookOff(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	_, err := f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	_, err = f.receive(f.acme.ID, "r1", "250000")
	wantCode(t, err, "RECEIPT_EXCEEDS_BALANCE")
	_, err = f.CityLedger.Receive(f.admin, f.propID, f.acme.ID, "r2", cityledger.ReceiptInput{Amount: "200000", PaymentMethod: "CASH", Allocations: []cityledger.AllocationInput{{InvoiceID: inv.ID, Amount: "200000"}}})
	must(t, err)
	got, err := f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "outstanding", got.Outstanding, "0")
	if got.PaymentStatus != "PAID" {
		t.Fatalf("status: %s", got.PaymentStatus)
	}
}

func TestTheAgingCountsCreditNotesLikeReceipts(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	_, err := f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	ag, err := f.CityLedger.Aging(f.admin, f.propID, f.acme.ID)
	must(t, err)
	eqd(t, "aging total", ag.Total, "200000")
	sum := decimal.Zero
	for _, b := range ag.Buckets {
		sum = sum.Add(decimal.RequireFromString(b.Amount))
	}
	if !sum.Equal(decimal.RequireFromString("200000")) {
		t.Fatalf("buckets: %+v", ag.Buckets)
	}
}

func TestTwoCreditNotesCannotTakeOffMoreThanTheInvoiceOwes(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.note(&inv.ID, nil, "race-"+string(rune('a'+i)), cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "200000"})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("%d credit notes of 200,000 were made against an invoice of 300,000: %v", ok, errs)
	}
	got, err := f.CityLedger.GetInvoice(f.admin, f.propID, inv.ID)
	must(t, err)
	eqd(t, "outstanding", got.Outstanding, "100000")
}

func TestAdjustmentsAreListedWithTheirLinesAndIsolatedByTenant(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	n, err := f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "10000", TaxID: &c.pb1.ID},
		cityledger.CreditNoteLineInput{Description: "y", AccountID: c.allowance, NetAmount: "20000"})
	must(t, err)
	list, err := f.CityLedger.Adjustments(f.admin, f.propID, f.acme.ID)
	must(t, err)
	if len(list) != 1 || list[0].ID != n.ID || len(list[0].Lines) != 2 {
		t.Fatalf("list: %+v", list)
	}
	eqd(t, "two lines", list[0].Amount, "31000")
	other := f.Tenant(t, "XYZ")
	f.Property(t, other.ID, "SG")
	otherAdmin, _ := f.AdminAccount(t, other.ID)
	_, err = f.CityLedger.GetAdjustment(otherAdmin, f.propID, n.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.CityLedger.GetAdjustment(f.admin, f.propID, 999999)
	wantCode(t, err, "ADJUSTMENT_NOT_FOUND")
}

// The day close journals the transfer against the city ledger; a credit note and a write-off of the next day take it off, and the control
// account still proves against what the company accounts say.
func TestTheControlAccountOfTheCityLedgerCountsCreditNotesAndWriteOffs(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
	_, err = f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "100000"})
	must(t, err)
	_, err = f.CityLedger.CreateWriteOff(f.admin, f.propID, "w1", cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: "50000", AccountID: c.badDebt, Reason: "uncollectible", Approval: f.approval()})
	must(t, err)
	rec, err := f.Accounting.Reconciliation(f.admin, f.propID, nil)
	must(t, err)
	for _, ctl := range rec.Controls {
		if ctl.Key == "CITY_LEDGER" {
			eqd(t, "the books", ctl.Ledger.String(), "150000")
			eqd(t, "the company accounts", ctl.Source.String(), "150000")
			eqd(t, "difference", ctl.Difference.String(), "0")
			return
		}
	}
	t.Fatal("no control for the city ledger")
}

func TestACreditNoteAndAWriteOffCarryTheirDepartment(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	dep, err := f.Departments.Create(f.admin, f.propID, departments.Input{Code: "XTRA", Name: "Extra"})
	must(t, err)
	inv, _ := f.invoicedOne(t, "101", "300000")
	deptOf := func(code string) *int64 {
		var d *int64
		must(t, f.Pool.QueryRow(context.Background(), `SELECT l.department_id FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
			WHERE l.property_id = $1 AND a.code = $2 ORDER BY l.id DESC LIMIT 1`, f.propID, code).Scan(&d))
		return d
	}
	_, err = f.note(&inv.ID, nil, "n0", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "1000", DepartmentID: ptr(int64(999999))})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.note(&inv.ID, nil, "n1", cityledger.CreditNoteLineInput{Description: "x", AccountID: c.allowance, NetAmount: "1000", DepartmentID: &dep.ID})
	must(t, err)
	if d := deptOf("4160"); d == nil || *d != dep.ID {
		t.Fatalf("credit note line department: %v", d)
	}
	_, err = f.CityLedger.CreateWriteOff(f.admin, f.propID, "w0", cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: "100", AccountID: c.badDebt, DepartmentID: ptr(int64(999999)), Reason: "x", Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.CityLedger.CreateWriteOff(f.admin, f.propID, "w1", cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: "100", AccountID: c.badDebt, DepartmentID: &dep.ID, Reason: "x", Approval: f.approval()})
	must(t, err)
	if d := deptOf("6140"); d == nil || *d != dep.ID {
		t.Fatalf("write-off department: %v", d)
	}
}

// Audit F-07 (migration 00065): the database refuses an invoice, a receipt or an adjustment dated a closed business day, with no service in the way.
func TestTheDatabaseRefusesCityLedgerRowsDatedAClosedDay(t *testing.T) {
	f := setup(t)
	c := f.chart(t)
	inv, _ := f.invoicedOne(t, "101", "300000")
	_, err := f.receive(f.acme.ID, "r1", "100000")
	must(t, err)
	_, err = f.CityLedger.CreateWriteOff(f.admin, f.propID, "w1", cityledger.WriteOffInput{InvoiceID: inv.ID, Amount: "50000", AccountID: c.badDebt, Reason: "the company is closed", Approval: f.approval()})
	must(t, err)
	id := func(table string) int64 {
		var n int64
		must(t, f.Pool.QueryRow(context.Background(), `SELECT min(id) FROM `+table).Scan(&n))
		return n
	}
	for _, c := range []struct{ table, column string }{{"city_ledger_receipts", "business_date"}, {"city_ledger_adjustments", "business_date"}, {"city_ledger_invoices", "invoice_date"}} {
		if err := dbtest.InsertCopyOnClosedDay(t, f.Pool, c.table, c.column, id(c.table)); !dbtest.IsClosedDayRefusal(err) {
			t.Errorf("%s: the database must refuse a row dated a closed day, got %v", c.table, err)
		}
	}
}
