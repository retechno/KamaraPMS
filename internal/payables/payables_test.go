package payables_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/iam"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func d(s string) civil.Date { return civil.MustParseDate(s) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func eq(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s: %s, want %s", what, got, want)
	}
}

func ptr[T any](v T) *T { return &v }

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	email            string
	acc              map[string]int64
}

// setup: a property on 30 Sep 2026 (IDR, no decimals) with the standard chart and an administrator who can approve.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, email := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, email: email, acc: map[string]int64{}}
	list, err := e.Accounting.Accounts(admin, p.ID, accounting.AccountFilter{})
	must(t, err)
	for _, a := range list {
		f.acc[a.Code] = a.ID
	}
	return f
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.email, Password: roomstest.Password}
}

func (f *fx) supplier(t *testing.T, code string, terms int) payables.Supplier {
	t.Helper()
	s, err := f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{Code: code, Name: "Supplier " + code, PaymentTermsDays: &terms})
	must(t, err)
	return s
}

func (f *fx) bill(t *testing.T, sup payables.Supplier, invoice, amount string, key string) payables.Bill {
	t.Helper()
	b, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
		SupplierID: sup.ID, SupplierInvoiceNumber: invoice, BillDate: d("2026-09-30"),
		Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Description: "Electricity", Amount: dec(amount)}},
	}, key)
	must(t, err)
	return b
}

func (f *fx) pay(t *testing.T, sup payables.Supplier, method, key string, allocs ...payables.AllocationInput) payables.SupplierPayment {
	t.Helper()
	pay, err := f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: sup.ID, PaymentDate: d("2026-09-30"), PaymentMethod: method, Allocations: allocs}, key)
	must(t, err)
	return pay
}

func (f *fx) balance(t *testing.T, code string) decimal.Decimal {
	t.Helper()
	var s string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(l.debit - l.credit), 0)::text FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id
		WHERE l.property_id = $1 AND a.code = $2`, f.propID, code).Scan(&s))
	return dec(s)
}

func (f *fx) requireBalanced(t *testing.T) {
	t.Helper()
	var dr, cr string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(debit), 0)::text, COALESCE(sum(credit), 0)::text FROM gl_journal_lines WHERE property_id = $1`, f.propID).Scan(&dr, &cr))
	if dr != cr {
		t.Fatalf("the books do not balance: %s %s", dr, cr)
	}
}

// closeDay runs the night audit of the current business date (the property has no rooms: nothing to post).
func (f *fx) closeDay(t *testing.T) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1))
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
}

func (f *fx) supplierOf(t *testing.T, id int64) payables.Supplier {
	t.Helper()
	s, err := f.Payables.GetSupplier(f.admin, f.propID, id)
	must(t, err)
	return s
}

func TestSuppliers(t *testing.T) {
	f := setup(t)
	s, err := f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{
		Code: " pln ", Name: "PLN Electricity", Email: "bill@pln.example", TaxID: "01.234.567.8-901.000", DefaultAccountID: ptr(f.acc["6510"]),
	})
	must(t, err)
	if s.Code != "PLN" || s.PaymentTermsDays != 30 || !s.IsActive || s.DefaultAccountCode != "6510" || !s.Outstanding.IsZero() {
		t.Fatalf("supplier: %+v", s)
	}
	_, err = f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{Code: "PLN", Name: "Again"})
	wantCode(t, err, "CODE_TAKEN")
	for name, in := range map[string]payables.SupplierInput{
		"code":               {Code: "bad code", Name: "x"},
		"name":               {Code: "A1"},
		"email":              {Code: "A2", Name: "x", Email: "nope"},
		"payment_terms_days": {Code: "A3", Name: "x", PaymentTermsDays: ptr(400)},
		"default_account_id": {Code: "A4", Name: "x", DefaultAccountID: ptr(f.acc["6100"])}, // a header
	} {
		_, err := f.Payables.CreateSupplier(f.admin, f.propID, in)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		found := false
		for _, fe := range e.Fields {
			found = found || fe.Field == name
		}
		if !found {
			t.Errorf("%s: %+v", name, e.Fields)
		}
	}
	up, err := f.Payables.UpdateSupplier(f.admin, f.propID, s.ID, payables.SupplierPatch{Name: ptr("PT PLN"), PaymentTermsDays: ptr(14), DefaultAccountID: ptr(int64(0)), IsActive: ptr(false)})
	must(t, err)
	if up.Name != "PT PLN" || up.PaymentTermsDays != 14 || up.DefaultAccountID != nil || up.IsActive {
		t.Fatalf("updated: %+v", up)
	}
	act, err := f.Payables.Suppliers(f.admin, f.propID, payables.SupplierFilter{Active: ptr(true)})
	must(t, err)
	if len(act) != 0 {
		t.Fatalf("active suppliers: %+v", act)
	}
	_, err = f.Payables.GetSupplier(f.admin, f.propID, 999999)
	wantCode(t, err, "SUPPLIER_NOT_FOUND")
	// permissions and tenancy
	viewer := f.User(t, f.tenantID, f.propID, auth.PermPayablesView)
	if _, err := f.Payables.Suppliers(viewer, f.propID, payables.SupplierFilter{}); err != nil {
		t.Fatal(err)
	}
	_, err = f.Payables.CreateSupplier(viewer, f.propID, payables.SupplierInput{Code: "X", Name: "x"})
	wantCode(t, err, "PERMISSION_DENIED")
	f.Clock.Set(roomstest.T0)
	other := f.Tenant(t, "XYZ")
	f.Property(t, other.ID, "SG")
	_, err = f.Payables.Suppliers(roomstest.Admin(other.ID), f.propID, payables.SupplierFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'payables.supplier_%'`); n != 2 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestABillPostsItsJournalAndTheExpenseReachesTheStatements(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 14)
	b, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
		SupplierID: sup.ID, SupplierInvoiceNumber: "INV-001", BillDate: d("2026-09-30"), Description: "September power",
		Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Description: "Electricity", Amount: dec("1000000")}, {AccountID: f.acc["1420"], Amount: dec("110000")}},
	}, "k1")
	must(t, err)
	if b.Number != "BILL000001" || b.DueDate != d("2026-10-14") || b.PaymentStatus != payables.BillUnpaid || len(b.Lines) != 2 || b.Status != "POSTED" {
		t.Fatalf("bill: %+v", b)
	}
	eq(t, "total", b.Total, "1110000")
	eq(t, "outstanding", b.Outstanding, "1110000")
	// the journal debits the lines and credits accounts payable
	j, err := f.Accounting.GetJournal(f.admin, f.propID, b.JournalID)
	must(t, err)
	if j.Type != accounting.JournalPayables || j.Date != d("2026-09-30") || len(j.Lines) != 3 || j.Reference != b.Number || j.Lines[2].AccountCode != "2110" || j.Lines[2].SourceType != "AP_BILL" {
		t.Fatalf("journal: %+v", j)
	}
	eq(t, "expense", f.balance(t, "6510"), "1000000")
	eq(t, "input VAT", f.balance(t, "1420"), "110000")
	eq(t, "accounts payable", f.balance(t, "2110"), "-1110000")
	f.requireBalanced(t)
	eq(t, "owed to the supplier", f.supplierOf(t, sup.ID).Outstanding, "1110000")
	// the expense is on the income statement (utilities) and the payable on the balance sheet
	from, to := d("2026-09-01"), d("2026-09-30")
	is, err := f.Accounting.IncomeStatement(f.admin, f.propID, &from, &to)
	must(t, err)
	eq(t, "net income", is.NetIncome, "-1000000")
	bs, err := f.Accounting.BalanceSheet(f.admin, f.propID, &to)
	must(t, err)
	if !bs.Difference.IsZero() {
		t.Fatalf("balance sheet difference %s", bs.Difference)
	}
	// the same key returns the same bill, the same supplier invoice is refused
	again, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
		SupplierID: sup.ID, SupplierInvoiceNumber: "INV-001", BillDate: d("2026-09-30"), Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Amount: dec("1")}},
	}, "k1")
	must(t, err)
	if again.ID != b.ID || f.Count(t, `SELECT count(*) FROM supplier_bills`) != 1 {
		t.Fatalf("a replay posts nothing: %+v", again)
	}
	_, err = f.Payables.PostBill(f.admin, f.propID, payables.BillInput{
		SupplierID: sup.ID, SupplierInvoiceNumber: "INV-001", BillDate: d("2026-09-30"), Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Amount: dec("5")}},
	}, "k2")
	wantCode(t, err, "DUPLICATE_INVOICE")
	if n := f.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'PAYABLES'`); n != 1 {
		t.Fatalf("a refused bill leaves no journal: %d", n)
	}
	// the control account cannot be posted by hand
	_, err = f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: "x", Lines: []accounting.LineInput{
		{AccountID: f.acc["2110"], Debit: dec("5")}, {AccountID: f.acc["1110"], Credit: dec("5")},
	}}, "m1")
	wantCode(t, err, "VALIDATION_FAILED")
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'payables.bill_posted'`); n != 1 {
		t.Fatalf("%d audit rows", n)
	}
}

func TestBillRules(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	line := func(code, amount string) payables.BillLineInput {
		return payables.BillLineInput{AccountID: f.acc[code], Amount: dec(amount)}
	}
	in := func(mut func(*payables.BillInput)) payables.BillInput {
		b := payables.BillInput{SupplierID: sup.ID, SupplierInvoiceNumber: "R-1", BillDate: d("2026-09-30"), Lines: []payables.BillLineInput{line("6510", "1000")}}
		mut(&b)
		return b
	}
	bad := func(name, field string, mut func(*payables.BillInput)) {
		_, err := f.Payables.PostBill(f.admin, f.propID, in(mut), "bad-"+name)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		for _, fe := range e.Fields {
			if fe.Field == field {
				return
			}
		}
		t.Errorf("%s: want a field error on %s, got %+v", name, field, e.Fields)
	}
	bad("no invoice", "supplier_invoice_number", func(b *payables.BillInput) { b.SupplierInvoiceNumber = " " })
	bad("no lines", "lines", func(b *payables.BillInput) { b.Lines = nil })
	bad("zero", "lines[0].amount", func(b *payables.BillInput) { b.Lines[0].Amount = dec("0") })
	bad("negative", "lines[0].amount", func(b *payables.BillInput) { b.Lines[0].Amount = dec("-5") })
	bad("decimals", "lines[0].amount", func(b *payables.BillInput) { b.Lines[0].Amount = dec("10.5") })
	bad("no account", "lines[0].account_id", func(b *payables.BillInput) { b.Lines[0].AccountID = 0 })
	bad("unknown account", "lines[0].account_id", func(b *payables.BillInput) { b.Lines[0].AccountID = 999999 })
	bad("header", "lines[0].account_id", func(b *payables.BillInput) { b.Lines[0].AccountID = f.acc["6100"] })
	bad("guest ledger", "lines[0].account_id", func(b *payables.BillInput) { b.Lines[0].AccountID = f.acc["1210"] })
	bad("payables itself", "lines[0].account_id", func(b *payables.BillInput) { b.Lines[0].AccountID = f.acc["2110"] })
	bad("no date", "bill_date", func(b *payables.BillInput) { b.BillDate = civil.Date{} })
	bad("future", "bill_date", func(b *payables.BillInput) { b.BillDate = d("2026-10-02") })
	bad("before start", "bill_date", func(b *payables.BillInput) { b.BillDate = d("2026-09-29") })
	bad("due before bill", "due_date", func(b *payables.BillInput) { b.DueDate = ptr(d("2026-09-01")) })
	bad("no supplier", "supplier_id", func(b *payables.BillInput) { b.SupplierID = 999999 })
	// an inactive supplier takes no bills
	_, err := f.Payables.UpdateSupplier(f.admin, f.propID, sup.ID, payables.SupplierPatch{IsActive: ptr(false)})
	must(t, err)
	_, err = f.Payables.PostBill(f.admin, f.propID, in(func(*payables.BillInput) {}), "inactive")
	wantCode(t, err, "SUPPLIER_INACTIVE")
	_, err = f.Payables.UpdateSupplier(f.admin, f.propID, sup.ID, payables.SupplierPatch{IsActive: ptr(true)})
	must(t, err)
	// a closed month takes no bills
	must(t, f.Exec(t, `INSERT INTO gl_periods (tenant_id, property_id, period_start) VALUES ($1, $2, '2026-09-01')`, f.tenantID, f.propID))
	_, err = f.Payables.PostBill(f.admin, f.propID, in(func(*payables.BillInput) {}), "closed")
	wantCode(t, err, "PERIOD_CLOSED")
	// permission
	viewer := f.User(t, f.tenantID, f.propID, auth.PermPayablesView)
	_, err = f.Payables.PostBill(viewer, f.propID, in(func(*payables.BillInput) {}), "viewer")
	wantCode(t, err, "PERMISSION_DENIED")
	if f.Count(t, `SELECT count(*) FROM supplier_bills`) != 0 || f.Count(t, `SELECT count(*) FROM gl_journals`) != 0 {
		t.Fatal("refused bills leave nothing behind")
	}
}

func TestPaymentsSettleBillsAndPostTheirJournal(t *testing.T) {
	f := setup(t)
	pln, pam := f.supplier(t, "PLN", 30), f.supplier(t, "PAM", 30)
	b1 := f.bill(t, pln, "A-1", "1000000", "b1")
	b2 := f.bill(t, pln, "A-2", "500000", "b2")
	other := f.bill(t, pam, "B-1", "300000", "b3")
	// part of one bill, by bank transfer
	p1 := f.pay(t, pln, "BANK_TRANSFER", "p1", payables.AllocationInput{BillID: b1.ID, Amount: dec("400000")})
	if p1.Number != "SPAY000001" || p1.Status != "POSTED" || len(p1.Allocations) != 1 || p1.JournalNumber == "" {
		t.Fatalf("payment: %+v", p1)
	}
	eq(t, "amount", p1.Amount, "400000")
	eq(t, "bank", f.balance(t, "1130"), "-400000")
	got, err := f.Payables.GetBill(f.admin, f.propID, b1.ID)
	must(t, err)
	if got.PaymentStatus != payables.BillPartial {
		t.Fatalf("status %s", got.PaymentStatus)
	}
	eq(t, "paid", got.Paid, "400000")
	eq(t, "outstanding", got.Outstanding, "600000")
	// the rest of it and the second bill in cash, in one payment
	p2 := f.pay(t, pln, "CASH", "p2", payables.AllocationInput{BillID: b1.ID, Amount: dec("600000")}, payables.AllocationInput{BillID: b2.ID, Amount: dec("500000")})
	eq(t, "amount", p2.Amount, "1100000")
	eq(t, "cash", f.balance(t, "1110"), "-1100000")
	eq(t, "payables left (the other supplier)", f.balance(t, "2110"), "-300000")
	f.requireBalanced(t)
	for _, b := range []int64{b1.ID, b2.ID} {
		bb, err := f.Payables.GetBill(f.admin, f.propID, b)
		must(t, err)
		if bb.PaymentStatus != payables.BillPaid || !bb.Outstanding.IsZero() {
			t.Fatalf("paid bill: %+v", bb)
		}
	}
	eq(t, "owed to PLN", f.supplierOf(t, pln.ID).Outstanding, "0")
	eq(t, "owed to PAM", f.supplierOf(t, pam.ID).Outstanding, "300000")
	j, err := f.Accounting.GetJournal(f.admin, f.propID, p2.JournalID)
	must(t, err)
	if j.Type != accounting.JournalPayables || len(j.Lines) != 2 || j.Lines[0].AccountCode != "2110" || !j.Lines[0].Debit.Equal(dec("1100000")) || j.Lines[1].SourceType != "AP_PAYMENT" {
		t.Fatalf("payment journal: %+v", j)
	}
	open, err := f.Payables.OpenBills(f.admin, f.propID, pam.ID)
	must(t, err)
	if len(open) != 1 || open[0].BillID != other.ID {
		t.Fatalf("open bills: %+v", open)
	}
	// what a payment may not do
	_, err = f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH",
		Allocations: []payables.AllocationInput{{BillID: other.ID, Amount: dec("300001")}}}, "over")
	wantCode(t, err, "ALLOCATION_EXCEEDS_OUTSTANDING")
	_, err = f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH",
		Allocations: []payables.AllocationInput{{BillID: b2.ID, Amount: dec("1")}}}, "wrong supplier")
	wantCode(t, err, "ALLOCATION_EXCEEDS_OUTSTANDING")
	_, err = f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: pln.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH",
		Allocations: []payables.AllocationInput{{BillID: b1.ID, Amount: dec("1")}}}, "paid already")
	wantCode(t, err, "ALLOCATION_EXCEEDS_OUTSTANDING")
	bad := func(name, field string, in payables.PaymentInput) {
		_, err := f.Payables.PostPayment(f.admin, f.propID, in, "bad-"+name)
		e := roomstest.Code(t, err, "VALIDATION_FAILED")
		for _, fe := range e.Fields {
			if fe.Field == field {
				return
			}
		}
		t.Errorf("%s: want %s, got %+v", name, field, e.Fields)
	}
	ok := payables.AllocationInput{BillID: other.ID, Amount: dec("1000")}
	bad("method", "payment_method", payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CARD", Allocations: []payables.AllocationInput{ok}})
	bad("no allocations", "allocations", payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH"})
	bad("zero", "allocations[0].amount", payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH", Allocations: []payables.AllocationInput{{BillID: other.ID}}})
	bad("twice", "allocations[1].bill_id", payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH", Allocations: []payables.AllocationInput{ok, ok}})
	bad("no date", "payment_date", payables.PaymentInput{SupplierID: pam.ID, PaymentMethod: "CASH", Allocations: []payables.AllocationInput{ok}})
	bad("future", "payment_date", payables.PaymentInput{SupplierID: pam.ID, PaymentDate: d("2026-10-05"), PaymentMethod: "CASH", Allocations: []payables.AllocationInput{ok}})
	// a retry returns the same payment
	again := f.pay(t, pam, "OTHER", "p3", payables.AllocationInput{BillID: other.ID, Amount: dec("100000")})
	replay := f.pay(t, pam, "OTHER", "p3", payables.AllocationInput{BillID: other.ID, Amount: dec("100000")})
	if again.ID != replay.ID || f.Count(t, `SELECT count(*) FROM supplier_payments`) != 3 {
		t.Fatalf("a replay pays once: %+v", replay)
	}
	eq(t, "other payments", f.balance(t, "1160"), "-100000")
	list, err := f.Payables.Payments(f.admin, f.propID, payables.PaymentFilter{SupplierID: &pam.ID})
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("payments of PAM: %+v", list)
	}
}

func TestVoidingBillsAndPayments(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	b := f.bill(t, sup, "V-1", "1000000", "b1")
	pay := f.pay(t, sup, "CASH", "p1", payables.AllocationInput{BillID: b.ID, Amount: dec("1000000")})
	// a paid bill is not voided until its payment is
	_, err := f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "duplicate", Approval: f.approval()})
	wantCode(t, err, "BILL_HAS_PAYMENTS")
	_, err = f.Payables.VoidPayment(f.admin, f.propID, pay.ID, payables.VoidInput{Reason: "x"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	_, err = f.Payables.VoidPayment(f.admin, f.propID, pay.ID, payables.VoidInput{Approval: f.approval()})
	wantCode(t, err, "VALIDATION_FAILED")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermPayablesView)
	_, err = f.Payables.VoidPayment(viewer, f.propID, pay.ID, payables.VoidInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "PERMISSION_DENIED")
	v, err := f.Payables.VoidPayment(f.admin, f.propID, pay.ID, payables.VoidInput{Reason: "paid twice", Approval: f.approval()})
	must(t, err)
	if v.Status != "VOIDED" || v.VoidJournalID == nil || v.VoidReason != "paid twice" {
		t.Fatalf("voided payment: %+v", v)
	}
	eq(t, "cash back", f.balance(t, "1110"), "0")
	eq(t, "the bill is owed again", f.supplierOf(t, sup.ID).Outstanding, "1000000")
	got, err := f.Payables.GetBill(f.admin, f.propID, b.ID)
	must(t, err)
	if got.PaymentStatus != payables.BillUnpaid {
		t.Fatalf("bill after the void: %+v", got)
	}
	_, err = f.Payables.VoidPayment(f.admin, f.propID, pay.ID, payables.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "PAYMENT_ALREADY_VOIDED")
	// now the bill can be voided: its journal is reversed and the invoice may be entered again
	vb, err := f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "wrong supplier", Approval: f.approval()})
	must(t, err)
	if vb.Status != "VOIDED" || vb.PaymentStatus != payables.BillVoided || !vb.Outstanding.IsZero() {
		t.Fatalf("voided bill: %+v", vb)
	}
	eq(t, "expense reversed", f.balance(t, "6510"), "0")
	eq(t, "payables reversed", f.balance(t, "2110"), "0")
	eq(t, "nothing owed", f.supplierOf(t, sup.ID).Outstanding, "0")
	f.requireBalanced(t)
	_, err = f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "again", Approval: f.approval()})
	wantCode(t, err, "BILL_ALREADY_VOIDED")
	again := f.bill(t, sup, "V-1", "900000", "b2")
	if again.ID == b.ID {
		t.Fatal("the invoice is entered again as a new bill")
	}
	// a voided payment's journal cannot be reversed by hand, and the journals tell the story
	if n := f.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'REVERSAL'`); n != 2 {
		t.Fatalf("%d reversals", n)
	}
	_, err = f.Accounting.Reverse(f.admin, f.propID, pay.JournalID, accounting.ReverseInput{Reason: "x", Approval: f.approval()})
	wantCode(t, err, "JOURNAL_NOT_REVERSIBLE")
	if err := f.Exec(t, `DELETE FROM supplier_bills`); err == nil {
		t.Fatal("bills are not deleted")
	}
	if err := f.Exec(t, `UPDATE supplier_payments SET amount = 1`); err == nil {
		t.Fatal("a payment does not change")
	}
	if err := f.Exec(t, `DELETE FROM supplier_payment_allocations`); err == nil {
		t.Fatal("allocations are not deleted")
	}
}

func TestAgingByDaysOverdueAndAsOfADate(t *testing.T) {
	f := setup(t)
	pln, pam := f.supplier(t, "PLN", 0), f.supplier(t, "PAM", 30)
	due := d("2026-09-30")
	late, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{SupplierID: pln.ID, SupplierInvoiceNumber: "L-1", BillDate: d("2026-09-30"), DueDate: &due,
		Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Amount: dec("1000")}}}, "b1")
	must(t, err)
	fresh := f.bill(t, pam, "F-1", "2000", "b2") // due 30 days from the bill
	f.closeDay(t)                                // 1 Oct
	f.closeDay(t)                                // 2 Oct
	ag, err := f.Payables.Aging(f.admin, f.propID, nil)
	must(t, err)
	if ag.AsOf != d("2026-10-02") || len(ag.Suppliers) != 2 {
		t.Fatalf("aging: %+v", ag)
	}
	eq(t, "total", ag.Total, "3000")
	eq(t, "1-30 days late", ag.Buckets["DAYS_1_30"], "1000")
	eq(t, "not yet due", ag.Buckets["CURRENT"], "2000")
	if ag.Suppliers[0].SupplierCode != "PAM" || ag.Suppliers[1].SupplierCode != "PLN" || ag.Suppliers[1].Bills[0].DaysOverdue != 2 {
		t.Fatalf("by supplier: %+v", ag.Suppliers)
	}
	// a payment on 2 Oct shows from that date on only
	f.Clock.Set(time.Date(2026, 10, 2, 5, 0, 0, 0, time.UTC))
	_, err = f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: pln.ID, PaymentDate: d("2026-10-02"), PaymentMethod: "CASH",
		Allocations: []payables.AllocationInput{{BillID: late.ID, Amount: dec("1000")}}}, "p1")
	must(t, err)
	now, err := f.Payables.Aging(f.admin, f.propID, nil)
	must(t, err)
	eq(t, "after the payment", now.Total, "2000")
	past := d("2026-10-01")
	then, err := f.Payables.Aging(f.admin, f.propID, &past)
	must(t, err)
	eq(t, "as of the day before", then.Total, "3000")
	eq(t, "as of the day before, late", then.Buckets["DAYS_1_30"], "1000")
	before := d("2026-09-30")
	first, err := f.Payables.Aging(f.admin, f.propID, &before)
	must(t, err)
	eq(t, "as of the bill date", first.Buckets["CURRENT"], "3000")
	future := d("2026-12-01")
	_, err = f.Payables.Aging(f.admin, f.propID, &future)
	wantCode(t, err, "VALIDATION_FAILED")
	_ = fresh
	// the control account agrees with the bills and payments
	rec, err := f.Accounting.Reconciliation(f.admin, f.propID, nil)
	must(t, err)
	var ap *accounting.Control
	for i := range rec.Controls {
		if rec.Controls[i].Key == "ACCOUNTS_PAYABLE" {
			ap = &rec.Controls[i]
		}
	}
	if ap == nil || !ap.Difference.IsZero() || !ap.Ledger.Equal(dec("2000")) {
		t.Fatalf("accounts payable control: %+v", ap)
	}
}

func TestConcurrentPaymentsCannotOverpayABill(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	b := f.bill(t, sup, "C-1", "1000", "b1")
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: sup.ID, PaymentDate: d("2026-09-30"), PaymentMethod: "CASH",
				Allocations: []payables.AllocationInput{{BillID: b.ID, Amount: dec("1000")}}}, "race-"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		} else {
			wantCode(t, err, "ALLOCATION_EXCEEDS_OUTSTANDING")
		}
	}
	if won != 1 || f.Count(t, `SELECT count(*) FROM supplier_payments`) != 1 {
		t.Fatalf("one payment wins: %d", won)
	}
	eq(t, "cash", f.balance(t, "1110"), "-1000")
	f.requireBalanced(t)
}

func TestConcurrentBillsWithTheSameInvoiceOrKey(t *testing.T) {
	f := setup(t)
	sup := f.supplier(t, "PLN", 30)
	post := func(invoice, key string) error {
		_, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{SupplierID: sup.ID, SupplierInvoiceNumber: invoice, BillDate: d("2026-09-30"),
			Lines: []payables.BillLineInput{{AccountID: f.acc["6510"], Amount: dec("1000")}}}, key)
		return err
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i < 4 { // four callers enter the same invoice with their own keys
				errs[i] = post("SAME", "own-"+string(rune('a'+i)))
			} else { // four retries of one request
				errs[i] = post("RETRY", "one-key")
			}
		}()
	}
	wg.Wait()
	same := 0
	for i, err := range errs {
		switch {
		case i >= 4:
			must(t, err)
		case err == nil:
			same++
		default:
			wantCode(t, err, "DUPLICATE_INVOICE")
		}
	}
	if same != 1 || f.Count(t, `SELECT count(*) FROM supplier_bills`) != 2 || f.Count(t, `SELECT count(*) FROM gl_journals WHERE journal_type = 'PAYABLES'`) != 2 {
		t.Fatalf("one SAME, one RETRY: %d bills", f.Count(t, `SELECT count(*) FROM supplier_bills`))
	}
	if n := f.Count(t, `SELECT count(DISTINCT bill_number) FROM supplier_bills WHERE bill_number ~ '^BILL00000[1-9]$'`); n != 2 {
		t.Fatalf("bill numbers: %d", n)
	}
	f.requireBalanced(t)
}
