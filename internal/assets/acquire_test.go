package assets_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets"
	"kamarapms/internal/iam"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomstest"
)

// startRegister sets the start of the register (the first of a month) the way the owner does in the settings.
func (f *fx) startRegister(t *testing.T, from string, threshold string) {
	t.Helper()
	s, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	_, err = f.svc.UpdateSettings(f.admin, f.propID, assets.SettingsInput{RegisterFrom: ptr(d(from)), CapitalizationThreshold: dec(threshold), StartRule: s.StartRule, OEPolicy: s.OEPolicy,
		OEDefaultMonths: s.OEDefaultMonths, TaxYearStartMonth: s.TaxYearStartMonth, Version: s.Version})
	must(t, err)
}

func (f *fx) supplier(t *testing.T, code string) payables.Supplier {
	t.Helper()
	s, err := f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{Code: code, Name: "Supplier " + code})
	must(t, err)
	return s
}

func (f *fx) bill(t *testing.T, sup payables.Supplier, invoice string, lines ...payables.BillLineInput) payables.Bill {
	t.Helper()
	b, err := f.Payables.PostBill(f.admin, f.propID, payables.BillInput{SupplierID: sup.ID, SupplierInvoiceNumber: invoice, BillDate: d("2026-09-30"), Lines: lines}, "bill-"+invoice)
	must(t, err)
	return b
}

func (f *fx) approval() *iam.ApprovalInput {
	return &iam.ApprovalInput{Email: f.bossEmail, Password: roomstest.Password}
}

func (f *fx) waiting(t *testing.T, flt assets.WaitingFilter) assets.WaitingPage {
	t.Helper()
	p, err := f.svc.ListWaiting(f.admin, f.propID, flt)
	must(t, err)
	return p
}

// lineOf is the waiting line of a document number (a bill number or a journal number) and a cost account.
func (f *fx) lineOf(t *testing.T, doc string, account string) assets.WaitingLine {
	t.Helper()
	for _, l := range f.waiting(t, assets.WaitingFilter{}).Data {
		if (l.DocumentNumber == doc || l.JournalNumber == doc) && l.AccountCode == account {
			return l
		}
	}
	t.Fatalf("no waiting line of %s on %s", doc, account)
	return assets.WaitingLine{}
}

func (f *fx) makeAsset(t *testing.T, lineID int64, units int, mut func(*assets.MakeAssetInput)) assets.MakeAssetResult {
	t.Helper()
	in := assets.MakeAssetInput{JournalLineID: lineID, Units: units, IdempotencyKey: nextKey()}
	if mut != nil {
		mut(&in)
	}
	res, err := f.svc.MakeAssetFromLine(f.admin, f.propID, in)
	must(t, err)
	return res
}

// manual posts a manual journal that debits an account with the amount and credits another.
func (f *fx) manual(t *testing.T, date, debit, credit, amount string) accounting.Journal {
	t.Helper()
	_, err0 := f.Pool.Exec(context.Background(), `UPDATE accounting_settings SET start_date = '2026-08-01' WHERE property_id = $1`, f.propID)
	must(t, err0) // the books start before the dates of the test entries
	j, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d(date), Description: "Entry " + debit, Lines: []accounting.LineInput{
		{AccountID: f.accounts[debit], Debit: dec(amount), Description: "Debit " + debit}, {AccountID: f.accounts[credit], Credit: dec(amount), Description: "Credit " + credit}}}, nextKey())
	must(t, err)
	return j
}

func (f *fx) countRows(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	must(t, f.Pool.QueryRow(context.Background(), query, args...).Scan(&n))
	return n
}

// 10.1: a bill line of 10,000,000 and VAT of 1,100,000 for three chairs, on a property that is not PKP (the VAT is part of the cost).
func TestExample101NonPKPBillLineBecomesThreeAssets(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "2500000")
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-1", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Lobby chair", Amount: dec("10000000"), VATAmount: dec("1100000")})
	journals := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)
	line := f.lineOf(t, b.Number, "1530")
	if !line.Cost.Equal(dec("11100000")) || line.Source != assets.WaitingBill || line.BillLineNo == nil || *line.BillLineNo != 1 || line.SupplierName == nil || line.BelowThreshold {
		t.Fatalf("waiting line %+v", line)
	}
	res := f.makeAsset(t, line.LineID, 3, nil)
	if len(res.Assets) != 3 || len(res.Warnings) != 0 {
		t.Fatalf("%+v", res)
	}
	for i, a := range res.Assets {
		if !a.Cost.Equal(dec("3700000")) {
			t.Errorf("unit %d costs %s", i+1, a.Cost)
		}
	}
	if got := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID); got != journals {
		t.Fatalf("the register posted %d journals", got-journals)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_ledger WHERE property_id = $1 AND kind = 'ACQUIRE' AND journal_id IS NOT NULL`, f.propID); n != 3 {
		t.Errorf("%d ACQUIRE rows", n)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_source_links WHERE property_id = $1 AND bill_id = $2 AND bill_line_no = 1 AND released_at IS NULL`, f.propID, b.ID); n != 3 {
		t.Errorf("%d links to the bill line", n)
	}
	d1, err := f.svc.GetAsset(f.admin, f.propID, res.Assets[0].ID)
	must(t, err)
	if d1.Source != "JOURNAL" || d1.SourceLink == nil || d1.SourceLink.BillNumber == nil || *d1.SourceLink.BillNumber != b.Number || d1.SourceLink.Units != 3 || d1.SupplierID == nil {
		t.Fatalf("asset %+v link %+v", d1.Asset, d1.SourceLink)
	}
	if !d1.Cost.Equal(dec("3700000")) || d1.LifeMonths == nil || d1.FirstDepMonth == nil || d1.FirstDepMonth.String() != "2026-09-01" {
		t.Errorf("asset terms %+v", d1.Asset)
	}
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 0 || w.Counts.Waiting != 0 {
		t.Errorf("the line still waits: %+v", w)
	}
	// the bill line is traced to its journal line
	if n := f.countRows(t, `SELECT count(*) FROM gl_journal_lines WHERE property_id = $1 AND source_type = 'AP_BILL' AND source_line = 1`, f.propID); n != 1 {
		t.Errorf("%d journal lines carry the bill line number", n)
	}
}

// 10.1 on a PKP property: the VAT is creditable, the cost is the amount, and the rounding difference goes to the last unit.
func TestExample101PKPBillLineRoundsToTheLastUnit(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "2500000")
	_, err := f.Pool.Exec(context.Background(), `INSERT INTO property_tax_settings (tenant_id, property_id, effective_from, is_pkp, npwp, input_vat_treatment) VALUES ($1, $2, DATE '2026-01-01', true, '01.234.567.8-901.000', 'CREDITABLE')`, f.tenantID, f.propID)
	must(t, err)
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-2", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Lobby chair", Amount: dec("10000000"), VATAmount: dec("1100000")})
	line := f.lineOf(t, b.Number, "1530")
	if !line.Cost.Equal(dec("10000000")) || line.VATTreatment == nil || *line.VATTreatment != "CREDITABLE" {
		t.Fatalf("waiting line %+v", line)
	}
	res := f.makeAsset(t, line.LineID, 3, nil)
	want := []string{"3333333", "3333333", "3333334"}
	for i, a := range res.Assets {
		if !a.Cost.Equal(dec(want[i])) {
			t.Errorf("unit %d costs %s, want %s", i+1, a.Cost, want[i])
		}
	}
	// the VAT line of the bill (account 1425) is not a cost line
	for _, l := range f.waiting(t, assets.WaitingFilter{}).Data {
		t.Errorf("unexpected waiting line %+v", l)
	}
}

func TestBelowTheThresholdWarnsAndStillMakesTheAssets(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "2500000")
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-3", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Stools", Amount: dec("1800000")})
	line := f.lineOf(t, b.Number, "1530")
	if !line.BelowThreshold {
		t.Fatalf("not flagged: %+v", line)
	}
	res := f.makeAsset(t, line.LineID, 3, nil)
	if len(res.Assets) != 3 || len(res.Warnings) != 3 || res.Warnings[0].Code != "BELOW_CAPITALIZATION_THRESHOLD" {
		t.Fatalf("%+v", res)
	}
}

// 10.14: the cashier pays in cash for two laptops; there is no bill and no supplier is locked.
func TestExample1014CashPurchaseBecomesAssetsWithoutASupplierLock(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	jid := f.postModuleJournal(t, accounting.JournalCashier, "1570", "1110", "18000000")
	var lineID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT l.id FROM gl_journal_lines l WHERE l.journal_id = $1 AND l.debit > 0`, jid).Scan(&lineID))
	w := f.waiting(t, assets.WaitingFilter{Source: assets.WaitingCashier})
	if len(w.Data) != 1 || w.Data[0].LineID != lineID || w.Data[0].CategoryCode != "IT" || w.Data[0].BillNumber != nil {
		t.Fatalf("%+v", w.Data)
	}
	if bills := f.waiting(t, assets.WaitingFilter{Source: assets.WaitingBill}); len(bills.Data) != 0 {
		t.Fatalf("the cash line is listed as a bill: %+v", bills.Data)
	}
	ctx, trace := db.WithLockTrace(f.admin)
	res, err := f.svc.MakeAssetFromLine(ctx, f.propID, assets.MakeAssetInput{JournalLineID: lineID, Units: 2, Name: "Laptop", IdempotencyKey: "cash-1"})
	must(t, err)
	if len(res.Assets) != 2 || !res.Assets[0].Cost.Equal(dec("9000000")) || !res.Assets[1].Cost.Equal(dec("9000000")) || res.Assets[0].Name != "Laptop (1/2)" {
		t.Fatalf("%+v", res)
	}
	for _, e := range trace.Events() {
		if e.Table == "suppliers" {
			t.Errorf("a cash line took the supplier lock")
		}
	}
	// reversing the journal under assets is refused until they are cancelled
	err = f.TxM.WithinTx(f.admin, func(ctx context.Context) error { return f.svc.CheckReverse(ctx, f.tenantID, f.propID, jid) })
	wantCode(t, err, "JOURNAL_LINE_IS_ASSET")
}

func (f *fx) postModuleJournal(t *testing.T, typ, debit, credit, amount string) int64 {
	t.Helper()
	var id int64
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		if _, err := f.Tenancy.RequireOpenBusinessDay(ctx, f.propID, db.ForShare, nil); err != nil {
			return err
		}
		po, err := f.Accounting.BeginPosting(ctx, f.propID)
		if err != nil {
			return err
		}
		id, _, err = po.Post(ctx, accounting.SystemJournal{Type: typ, Date: d("2026-09-30"), Description: "Purchase", Lines: []accounting.SystemLine{
			{AccountID: f.accounts[debit], Debit: dec(amount), Description: "Laptops", SourceType: "TEST", SourceRef: "1"}, {AccountID: f.accounts[credit], Credit: dec(amount), Description: "Cash"}}})
		return err
	}))
	return id
}

// A donated server: a manual journal first, then "make asset" from its debit line; the journal is not reversed while the asset exists, and is after the asset is cancelled.
func TestDonationByManualJournalAndTheWayOutOfTheGuard(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-29", "1570", "3900", "6000000")
	line := f.lineOf(t, j.Number, "1570")
	if line.Source != assets.WaitingManual || line.AgeDays != 1 {
		t.Fatalf("%+v", line)
	}
	res := f.makeAsset(t, line.LineID, 1, func(in *assets.MakeAssetInput) { in.Name = "Server" })
	_, err := f.Accounting.Reverse(f.admin, f.propID, j.ID, accounting.ReverseInput{Reason: "wrong", Approval: f.approval()})
	wantCode(t, err, "JOURNAL_LINE_IS_ASSET")
	_, err = f.svc.CancelAsset(f.admin, f.propID, res.Assets[0].ID, assets.CancelInput{Reason: "not ours"})
	must(t, err)
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 1 || w.Data[0].LineID != line.LineID {
		t.Fatalf("the line did not come back: %+v", w.Data)
	}
	_, err = f.Accounting.Reverse(f.admin, f.propID, j.ID, accounting.ReverseInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 0 {
		t.Fatalf("a reversed journal still waits: %+v", w.Data)
	}
	_, err = f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: nextKey()})
	wantCode(t, err, "LINE_NOT_ELIGIBLE")
}

func TestAnAssetMadeAgainAfterACancellation(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-29", "1530", "3900", "5000000")
	line := f.lineOf(t, j.Number, "1530")
	first := f.makeAsset(t, line.LineID, 1, nil)
	_, err := f.svc.CancelAsset(f.admin, f.propID, first.Assets[0].ID, assets.CancelInput{Reason: "again"})
	must(t, err)
	second := f.makeAsset(t, line.LineID, 2, nil)
	if len(second.Assets) != 2 || second.Assets[0].ID == first.Assets[0].ID {
		t.Fatalf("%+v", second)
	}
	st, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	for _, i := range st.Issues {
		if i.Code == "LEDGER_DRIFT" {
			t.Errorf("drift: %+v", i)
		}
	}
}

func TestLineAlreadyAssetAndTheIdempotencyKey(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-29", "1530", "3900", "5000000")
	line := f.lineOf(t, j.Number, "1530")
	in := assets.MakeAssetInput{JournalLineID: line.LineID, Units: 2, IdempotencyKey: "same-key"}
	first, err := f.svc.MakeAssetFromLine(f.admin, f.propID, in)
	must(t, err)
	again, err := f.svc.MakeAssetFromLine(f.admin, f.propID, in)
	must(t, err)
	if !again.Replayed || len(again.Assets) != 2 || again.Assets[0].Number != first.Assets[0].Number || again.Assets[1].ID != first.Assets[1].ID {
		t.Fatalf("replay %+v first %+v", again, first)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_assets WHERE property_id = $1`, f.propID); n != 2 {
		t.Fatalf("%d assets", n)
	}
	in.IdempotencyKey = "other-key"
	_, err = f.svc.MakeAssetFromLine(f.admin, f.propID, in)
	wantCode(t, err, "LINE_ALREADY_ASSET")
	in.IdempotencyKey = ""
	_, err = f.svc.MakeAssetFromLine(f.admin, f.propID, in)
	wantCode(t, err, "IDEMPOTENCY_KEY_REQUIRED")
}

// The race of two requests for the same line: one succeeds, the other is LINE_ALREADY_ASSET; the unique key of fa_source_links is the backstop.
func TestTwoMakeAssetsOfTheSameLineRace(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-29", "1530", "3900", "5000000")
	line := f.lineOf(t, j.Number, "1530")
	errs := f.race(2, func(i int) error {
		_, err := f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, Units: 1 + i, IdempotencyKey: nextKey()})
		return err
	})
	ok, already := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case isCode(err, "LINE_ALREADY_ASSET"):
			already++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || already != 1 {
		t.Fatalf("%d succeeded, %d already", ok, already)
	}
}

func isCode(err error, code string) bool {
	e, ok := apperr.As(err)
	return ok && e.Code == code
}

// race runs n copies of fn at once, released together.
func (f *fx) race(n int, fn func(i int) error) []error {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	out := make([]error, n)
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			out[i] = fn(i)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	return out
}

// F8: a bill whose line is an asset is not voided, and a credit note does not touch the line, until the asset is cancelled.
func TestVoidBillAndCreditNoteAreRefusedUnderAnAsset(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-4", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Sofa", Amount: dec("8000000")},
		payables.BillLineInput{AccountID: f.accounts["6510"], Description: "Delivery", Amount: dec("500000")})
	line := f.lineOf(t, b.Number, "1530")
	if line.BillLineNo == nil || *line.BillLineNo != 1 {
		t.Fatalf("%+v", line)
	}
	res := f.makeAsset(t, line.LineID, 1, nil)
	_, err := f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "wrong", Approval: f.approval()})
	wantCode(t, err, "BILL_LINE_IS_ASSET")
	// the credit note on the asset line is refused, on the delivery line (no asset) it is accepted
	_, err = f.Payables.PostCreditNote(f.admin, f.propID, payables.CreditNoteInput{BillID: b.ID, SupplierCreditNumber: "CN-1", CreditDate: d("2026-09-30"), Reason: "returned",
		Lines: []payables.CreditLineInput{{BillLineNo: 1, Amount: dec("1000000")}}}, "cn-1")
	wantCode(t, err, "BILL_LINE_IS_ASSET")
	_, err = f.Payables.PostCreditNote(f.admin, f.propID, payables.CreditNoteInput{BillID: b.ID, SupplierCreditNumber: "CN-2", CreditDate: d("2026-09-30"), Reason: "discount",
		Lines: []payables.CreditLineInput{{BillLineNo: 2, Amount: dec("100000")}}}, "cn-2")
	must(t, err)
	_, err = f.svc.CancelAsset(f.admin, f.propID, res.Assets[0].ID, assets.CancelInput{Reason: "wrong"})
	must(t, err)
	// a credit note stands now: it has to be voided first, which is the existing rule of the payables
	_, err = f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "wrong", Approval: f.approval()})
	wantCode(t, err, "BILL_HAS_CREDIT_NOTES")
}

func TestVoidBillAfterTheAssetIsCancelled(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-5", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Sofa", Amount: dec("8000000")})
	line := f.lineOf(t, b.Number, "1530")
	res := f.makeAsset(t, line.LineID, 1, nil)
	_, err := f.svc.CancelAsset(f.admin, f.propID, res.Assets[0].ID, assets.CancelInput{Reason: "wrong"})
	must(t, err)
	_, err = f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "wrong", Approval: f.approval()})
	must(t, err)
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 0 {
		t.Fatalf("the line of a voided bill waits: %+v", w.Data)
	}
}

// Make asset and VoidBill of the same bill: one of them wins, whichever comes first, and the books and the register agree (the supplier lock serialises the two).
func TestMakeAssetAndVoidBillRace(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-6", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Sofa", Amount: dec("8000000")})
	line := f.lineOf(t, b.Number, "1530")
	errs := f.race(2, func(i int) error {
		if i == 0 {
			_, err := f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: nextKey()})
			return err
		}
		_, err := f.Payables.VoidBill(f.admin, f.propID, b.ID, payables.VoidInput{Reason: "wrong", Approval: f.approval()})
		return err
	})
	madeAsset := f.countRows(t, `SELECT count(*) FROM fa_source_links WHERE property_id = $1 AND released_at IS NULL`, f.propID)
	voided := f.countRows(t, `SELECT count(*) FROM supplier_bills WHERE property_id = $1 AND status = 'VOIDED'`, f.propID)
	if madeAsset+voided != 1 {
		t.Fatalf("asset %d voided %d (%v)", madeAsset, voided, errs)
	}
	switch {
	case madeAsset == 1 && !isCode(errs[1], "BILL_LINE_IS_ASSET"):
		t.Errorf("the void said %v", errs[1])
	case voided == 1 && !isCode(errs[0], "LINE_NOT_ELIGIBLE"):
		t.Errorf("the asset said %v", errs[0])
	}
}

// Make asset and the reversal of the manual journal of the same line.
func TestMakeAssetAndJournalReversalRace(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-29", "1530", "3900", "5000000")
	line := f.lineOf(t, j.Number, "1530")
	errs := f.race(2, func(i int) error {
		if i == 0 {
			_, err := f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: nextKey()})
			return err
		}
		_, err := f.Accounting.Reverse(f.admin, f.propID, j.ID, accounting.ReverseInput{Reason: "wrong", Approval: f.approval()})
		return err
	})
	madeAsset := f.countRows(t, `SELECT count(*) FROM fa_source_links WHERE property_id = $1 AND released_at IS NULL`, f.propID)
	reversed := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1 AND reverses_journal_id = $2`, f.propID, j.ID)
	if madeAsset+reversed != 1 {
		t.Fatalf("asset %d reversal %d (%v)", madeAsset, reversed, errs)
	}
}

func TestDismissAndRestoreALine(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-29", "1530", "3900", "5000000")
	line := f.lineOf(t, j.Number, "1530")
	wantCode(t, f.svc.DismissLine(f.admin, f.propID, assets.DismissInput{JournalLineID: line.LineID}), "VALIDATION_FAILED")
	must(t, f.svc.DismissLine(f.admin, f.propID, assets.DismissInput{JournalLineID: line.LineID, Reason: "a repair"}))
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 0 || w.Counts.Dismissed != 1 {
		t.Fatalf("%+v", w)
	}
	if w := f.waiting(t, assets.WaitingFilter{Dismissed: true}); len(w.Data) != 1 || w.Data[0].DismissedReason == nil || *w.Data[0].DismissedReason != "a repair" {
		t.Fatalf("%+v", w.Data)
	}
	_, err := f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: nextKey()})
	wantCode(t, err, "LINE_DISMISSED")
	wantCode(t, f.svc.DismissLine(f.admin, f.propID, assets.DismissInput{JournalLineID: line.LineID, Reason: "twice"}), "LINE_ALREADY_DISMISSED")
	must(t, f.svc.RestoreLine(f.admin, f.propID, line.LineID))
	wantCode(t, f.svc.RestoreLine(f.admin, f.propID, line.LineID), "DISMISSAL_NOT_FOUND")
	f.makeAsset(t, line.LineID, 1, nil)
	if n := f.countRows(t, `SELECT count(*) FROM fa_source_dismissals WHERE property_id = $1`, f.propID); n != 1 {
		t.Errorf("the dismissal was not kept: %d", n)
	}
}

// What the waiting list leaves out: the journals before the register starts, the lines of the register itself, the cost accounts of no category, the credits, and operating equipment under the rule EXPENSE.
func TestWaitingListFilters(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	f.manual(t, "2026-08-15", "1530", "3900", "1000000") // before the register starts
	f.manual(t, "2026-09-10", "6510", "3900", "2000000") // not a cost account
	f.manual(t, "2026-09-10", "3900", "1530", "3000000") // a credit to a cost account
	f.manual(t, "2026-09-11", "1540", "3900", "4000000") // operating equipment, expensed by the default rule
	j := f.manual(t, "2026-09-12", "1530", "3900", "5000000")
	w := f.waiting(t, assets.WaitingFilter{})
	if len(w.Data) != 1 || w.Data[0].JournalNumber != j.Number {
		t.Fatalf("%+v", w.Data)
	}
	// the register's own journal (an opening import) never appears
	_, err := f.importRows(t, "2026-08-31", true, false, row("Old sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 1 {
		t.Fatalf("the journal of the register is listed: %+v", w.Data)
	}
	// filters
	if w := f.waiting(t, assets.WaitingFilter{DateFrom: ptr(d("2026-09-13"))}); len(w.Data) != 0 {
		t.Errorf("date filter: %+v", w.Data)
	}
	if w := f.waiting(t, assets.WaitingFilter{Q: j.Number}); len(w.Data) != 1 {
		t.Errorf("q filter: %+v", w.Data)
	}
	if w := f.waiting(t, assets.WaitingFilter{Source: assets.WaitingBank}); len(w.Data) != 0 {
		t.Errorf("source filter: %+v", w.Data)
	}
	_, err = f.svc.ListWaiting(f.admin, f.propID, assets.WaitingFilter{Source: "NOPE"})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestOperatingEquipmentFollowsThePolicyOfTheProperty(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	f.manual(t, "2026-09-11", "1540", "3900", "4000000")
	if w := f.waiting(t, assets.WaitingFilter{}); len(w.Data) != 0 {
		t.Fatalf("operating equipment waits under EXPENSE: %+v", w.Data)
	}
	s, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	_, err = f.svc.UpdateSettings(f.admin, f.propID, assets.SettingsInput{RegisterFrom: s.RegisterFrom, CapitalizationThreshold: s.CapitalizationThreshold, StartRule: s.StartRule, OEPolicy: assets.OEBatch,
		OEDefaultMonths: s.OEDefaultMonths, TaxYearStartMonth: s.TaxYearStartMonth, Version: s.Version})
	must(t, err)
	w := f.waiting(t, assets.WaitingFilter{})
	if len(w.Data) != 1 || w.Data[0].CategoryKind != assets.KindOE {
		t.Fatalf("%+v", w.Data)
	}
}

func TestNothingWaitsBeforeTheRegisterStarts(t *testing.T) {
	f := newFx(t)
	f.manual(t, "2026-09-10", "1530", "3900", "1000000")
	w := f.waiting(t, assets.WaitingFilter{})
	if len(w.Data) != 0 || w.RegisterFrom != nil {
		t.Fatalf("%+v", w)
	}
	_, err := f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: 1, IdempotencyKey: nextKey()})
	if err == nil {
		t.Fatal("made an asset before the register started")
	}
}

// The start of the depreciation is free until the first depreciation: the in-service date and the start rule change the first month.
func TestStartOfTheDepreciationIsFreeUntilItRuns(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-20", "1530", "3900", "9600000")
	line := f.lineOf(t, j.Number, "1530")
	res := f.makeAsset(t, line.LineID, 1, func(in *assets.MakeAssetInput) { in.StartRule = assets.StartNextMonth })
	a, err := f.svc.GetAsset(f.admin, f.propID, res.Assets[0].ID)
	must(t, err)
	if a.StartRule != assets.StartNextMonth || a.FirstDepMonth == nil || a.FirstDepMonth.String() != "2026-10-01" {
		t.Fatalf("%+v", a.Asset)
	}
	rule := assets.StartInServiceMonth
	got, err := f.svc.UpdateAsset(f.admin, f.propID, a.ID, assets.AssetPatch{Name: a.Name, StartRule: &rule, InServiceDate: ptr(d("2026-09-25")), Version: a.Version})
	must(t, err)
	if got.FirstDepMonth.String() != "2026-09-01" || got.InServiceDate.String() != "2026-09-25" || got.Version <= a.Version {
		t.Fatalf("%+v", got.Asset)
	}
	late := d("2026-08-31")
	_, err = f.svc.UpdateAsset(f.admin, f.propID, a.ID, assets.AssetPatch{Name: a.Name, InServiceDate: &late, Version: got.Version})
	wantCode(t, err, "VALIDATION_FAILED")
}

// A bill line is traced to its asset in both directions, and a make-asset needs asset.manage.
func TestMakeAssetNeedsThePermission(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	j := f.manual(t, "2026-09-20", "1530", "3900", "9600000")
	line := f.lineOf(t, j.Number, "1530")
	reader := f.user(t, auth.PermAssetRead)
	_, err := f.svc.MakeAssetFromLine(reader, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: nextKey()})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := f.svc.ListWaiting(reader, f.propID, assets.WaitingFilter{}); err != nil {
		t.Errorf("a reader lists the waiting lines: %v", err)
	}
	wantCode(t, f.svc.DismissLine(reader, f.propID, assets.DismissInput{JournalLineID: line.LineID, Reason: "x"}), "PERMISSION_DENIED")
	other := f.user(t, auth.PermAssetManage)
	if _, err := f.svc.MakeAssetFromLine(other, f.propID+999, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: nextKey()}); err == nil {
		t.Error("another property answered")
	}
}

func TestMakeAssetTakesItsLocksInOrder(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	sup := f.supplier(t, "FURN")
	b := f.bill(t, sup, "INV-7", payables.BillLineInput{AccountID: f.accounts["1530"], Description: "Sofa", Amount: dec("8000000")})
	line := f.lineOf(t, b.Number, "1530")
	ctx, trace := db.WithLockTrace(f.admin)
	_, err := f.svc.MakeAssetFromLine(ctx, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, IdempotencyKey: "trace-1"})
	must(t, err)
	level, rank := db.LevelNone, 0
	ranks := map[string]int{"suppliers": 1, "fa_settings": 2, "fa_assets": 3, "fa_audits": 4}
	var sawSupplier, sawSettings bool
	for _, e := range trace.Events() {
		if e.Begin {
			level, rank = db.LevelNone, 0
			continue
		}
		if e.Level < level {
			t.Errorf("the level fell from %d to %d (%s)", level, e.Level, e.Table)
		}
		if e.Level > level {
			rank = 0
		}
		level = e.Level
		if r, ok := ranks[e.Table]; ok {
			if r < rank {
				t.Errorf("%s taken after a later table of the level", e.Table)
			}
			rank = r
		}
		sawSupplier, sawSettings = sawSupplier || e.Table == "suppliers", sawSettings || e.Table == "fa_settings"
	}
	if !sawSupplier || !sawSettings {
		t.Errorf("supplier %v settings %v", sawSupplier, sawSettings)
	}
}
