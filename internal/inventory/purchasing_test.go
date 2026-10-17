package inventory_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/iam"
	"kamarapms/internal/inventory"
	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomstest"
	"kamarapms/internal/taxfiling"
)

// Purchasing (35-inventory.md 5.3, 8.1, 8.3, 8.7): the order, the receipt, the draft bill, the three-way match, the clearing of a price difference, the return and the credit note.

var methods = []string{inventory.MethodAverage, inventory.MethodFIFO}

// pfx is the fixture of the purchasing: the property of fx with a supplier, the purchase units (karton of 12 botol, karung of 25 kg) and the property set to order without a request.
type pfx struct {
	*fx
	sup, pasar int64
	mu         sync.Mutex
	uuid       int
}

func newPurchasing(t *testing.T, method string, decimals int32) *pfx {
	t.Helper()
	f := newFx(t, method, decimals)
	p := &pfx{fx: f}
	for _, u := range [][2]string{{"karton", "Karton"}, {"karung", "Karung"}} {
		if _, ok := f.unit[u[0]]; !ok {
			r, err := f.svc.CreateUnit(f.admin, f.propID, inventory.UnitInput{Code: u[0], Name: u[1]})
			must(t, err)
			f.unit[u[0]] = r.ID
		}
	}
	f.itemUnits(t, "SIRUP", inventory.ItemUnit{UnitID: f.unit["karton"], StockNum: 12, StockDen: 1, IsPurchase: true, IsDefaultPurchase: true})
	f.itemUnits(t, "BERAS", inventory.ItemUnit{UnitID: f.unit["karung"], StockNum: 25, StockDen: 1, IsPurchase: true, IsDefaultPurchase: true})
	s1, err := f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{Code: "SUP", Name: "Supplier One"})
	must(t, err)
	s2, err := f.Payables.CreateSupplier(f.admin, f.propID, payables.SupplierInput{Code: "PASAR", Name: "Pasar"})
	must(t, err)
	p.sup, p.pasar = s1.ID, s2.ID
	p.settings(t, func(in *inventory.SettingsInput) { in.PoRequiresPr = false })
	return p
}

func (f *fx) itemUnits(t *testing.T, code string, extra ...inventory.ItemUnit) {
	t.Helper()
	it, err := f.svc.GetItem(f.admin, f.propID, f.item[code])
	must(t, err)
	units := append([]inventory.ItemUnit{}, it.Units...)
	for _, x := range extra { // a new default takes the place of the stock unit's
		for i := range units {
			if x.IsDefaultPurchase {
				units[i].IsDefaultPurchase = false
			}
		}
	}
	units = append(units, extra...)
	_, err = f.svc.SetItemUnits(f.admin, f.propID, f.item[code], units)
	must(t, err)
}

func (f *fx) settings(t *testing.T, change func(*inventory.SettingsInput)) {
	t.Helper()
	st, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	in := inventory.SettingsInput{CostingMethod: st.CostingMethod, PriceTolerancePct: st.PriceTolerancePct, QtyTolerancePct: st.QtyTolerancePct, OverReceiptPct: st.OverReceiptPct,
		PoRequiresPr: st.PoRequiresPr, DirectReceiptLimit: st.DirectReceiptLimit, CountBlocksNightAudit: st.CountBlocksNightAudit, CountBlind: st.CountBlind, Version: st.Version}
	change(&in)
	_, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	must(t, err)
}

func (p *pfx) id() string {
	p.mu.Lock() // the races start several goroutines that all ask for a new uuid
	defer p.mu.Unlock()
	p.uuid++
	return "00000000-0000-4000-8000-" + pad12(p.uuid)
}

func pad12(n int) string {
	s := "000000000000"
	d := []byte(s)
	for i := 11; n > 0 && i >= 0; i-- {
		d[i] = byte('0' + n%10)
		n /= 10
	}
	return string(d)
}

// open posts the opening stock of an item in a warehouse (the start of a flow of 8.x).
func (p *pfx) open(t *testing.T, wh, item, unit string, qty, cost string) {
	t.Helper()
	_, err := p.svc.PostOpening(p.admin, p.propID, inventory.OpeningInput{ClientUUID: p.id(), WarehouseID: p.wh[wh], Lines: []inventory.OpeningLineInput{line(p.item[item], p.unit[unit], qty, cost)}})
	must(t, err)
}

// order writes and sends an order of one item.
func (p *pfx) order(t *testing.T, supplier int64, wh, item, unit, qty, price string) inventory.PurchaseOrder {
	t.Helper()
	po, err := p.svc.CreatePurchaseOrder(p.admin, p.propID, inventory.POInput{ClientUUID: p.id(), SupplierID: supplier, WarehouseID: p.wh[wh], Send: true,
		Lines: []inventory.POLineInput{{ItemID: p.item[item], UnitID: p.unit[unit], Qty: dec(qty), UnitPrice: dec(price)}}})
	must(t, err)
	return po
}

func (p *pfx) receive(t *testing.T, po inventory.PurchaseOrder, qty string) inventory.Receipt {
	t.Helper()
	r, err := p.tryReceive(po, qty)
	must(t, err)
	return r
}

func (p *pfx) tryReceive(po inventory.PurchaseOrder, qty string) (inventory.Receipt, error) {
	return p.svc.PostReceipt(p.admin, p.propID, inventory.ReceiptInput{ClientUUID: p.id(), POID: &po.ID, Lines: []inventory.ReceiptLineInput{{POLineNo: ptr(1), Qty: dec(qty)}}})
}

// transfer moves goods between warehouses the way PR 3 will: a pair of movements with no journal.
func (p *pfx) transfer(t *testing.T, from, to, item, qty string) {
	t.Helper()
	pf, pt := p.pair(from, item), p.pair(to, item)
	must(t, p.tx([]inventory.Pair{pf, pt}, func(ctx context.Context, st *inventory.Stock) error {
		out, err := st.Out(ctx, inventory.OutArgs{Pair: pf, Kind: inventory.KindTransferOut, Qty: dec(qty), Ref: p.ref()})
		if err != nil {
			return err
		}
		_, err = st.In(ctx, inventory.InArgs{Pair: pt, Kind: inventory.KindTransferIn, Qty: dec(qty), Value: out.Value.Neg(), Ref: p.ref()})
		return err
	}))
}

// approver makes a user who holds inventory.approve with a password, as the approval of a price difference asks.
func (p *pfx) approver(t *testing.T) *iam.ApprovalInput {
	t.Helper()
	_, email := p.Account(t, p.tenantID, p.propID, auth.PermInventoryApprove)
	return &iam.ApprovalInput{Email: email, Password: roomstest.Password}
}

// voider makes a user who may approve the void of a bill (the payables' approval).
func (p *pfx) voider(t *testing.T) *iam.ApprovalInput {
	t.Helper()
	_, email := p.Account(t, p.tenantID, p.propID, auth.PermCorrectionApprove)
	return &iam.ApprovalInput{Email: email, Password: roomstest.Password}
}

// pkp makes the property a PKP from today with the input VAT creditable (the case of 8.1); the property starts as not PKP, with the VAT part of the cost.
func (p *pfx) pkp(t *testing.T) {
	t.Helper()
	_, err := p.Tax.ChangeSettings(p.admin, p.propID, taxfiling.SettingsInput{EffectiveFrom: civil.MustParseDate("2026-09-30"), IsPKP: true, NPWP: "01.234.567.8-901.000", Approval: p.voider(t)})
	must(t, err)
}

func invoice(supplier int64, number string, lines ...inventory.ConfirmLineInput) inventory.ConfirmBillInput {
	return inventory.ConfirmBillInput{SupplierID: supplier, SupplierInvoiceNumber: number, BillDate: civil.MustParseDate("2026-09-30"), Lines: lines}
}

func billLine(r inventory.Receipt, no int, qty, price, vat string) inventory.ConfirmLineInput {
	return inventory.ConfirmLineInput{ReceiptID: r.ID, ReceiptLineNo: no, Qty: dec(qty), UnitPrice: dec(price), VATAmount: dec(vat)}
}

// grni is the balance of goods received not invoiced as the stock and the bills say it (debits less credits).
func (f *fx) grni(t *testing.T) string {
	t.Helper()
	m, err := f.svc.StockValueByAccount(f.admin, f.tenantID, f.propID, civil.MustParseDate("2030-01-01"))
	must(t, err)
	return m[f.accounts["2130"]].String()
}

func (f *fx) wantGRNI(t *testing.T, want string) {
	t.Helper()
	if got := f.grni(t); !dec(got).Equal(dec(want)) {
		t.Fatalf("goods received not invoiced: %s, want %s", got, want)
	}
}

func TestAReceiptAtTheOrderPriceMakesItsDraftAndBooksGoodsReceivedNotInvoiced(t *testing.T) {
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			f := newPurchasing(t, m, 0)
			f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
			po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
			if po.Status != "ORDERED" || !po.Total.Equal(dec("3000000")) || !po.Lines[0].QtyStock.Equal(dec("60")) {
				t.Fatalf("order: %+v", po)
			}
			r := f.receive(t, po, "5")
			if !r.TotalValue.Equal(dec("3000000")) || r.Lines[0].UnitPrice.String() != "600000" || r.DraftStatus == nil || *r.DraftStatus != "OPEN" {
				t.Fatalf("receipt: %+v", r)
			}
			f.wantBal(t, f.pair("MAIN", "SIRUP"), "80", "3960000")
			drafts, err := f.svc.ListBillDrafts(f.admin, f.propID, inventory.DraftFilter{OpenOnly: true})
			must(t, err)
			if len(drafts) != 1 || !drafts[0].RemainingValue.Equal(dec("3000000")) {
				t.Fatalf("drafts: %+v", drafts)
			}
			f.wantGRNI(t, "-3000000")
			got, err := f.svc.GetPurchaseOrder(f.admin, f.propID, po.ID)
			must(t, err)
			if got.Status != "RECEIVED" || !got.Lines[0].QtyReceived.Equal(dec("5")) {
				t.Fatalf("order after the receipt: %s %v", got.Status, got.Lines[0].QtyReceived)
			}
		})
	}
}

// 8.1: a price 2% above the order, an invoice that needs an approval, the difference split between the stock on hand and the price variance, then unmatched.
func TestThePriceVarianceOfABillIsClearedIntoTheStockOnHandAndThePriceVariance(t *testing.T) {
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			f := newPurchasing(t, m, 0)
			f.pkp(t)
			f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
			po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
			r := f.receive(t, po, "5")
			f.transfer(t, "MAIN", "BAR", "SIRUP", "30")
			in := invoice(f.sup, "INV-1", billLine(r, 1, "5", "612000", "336600"))
			prev, err := f.svc.PreviewBill(f.admin, f.propID, in)
			must(t, err)
			if !prev.ApprovalRequired || !prev.ApprovalAmount.Equal(dec("60000")) || !prev.Variance.Equal(dec("60000")) || !prev.InventoryPart.Equal(dec("50000")) || !prev.PPVPart.Equal(dec("10000")) ||
				!prev.BillTotal.Equal(dec("3396600")) {
				t.Fatalf("preview: %+v", prev)
			}
			_, err = f.svc.ConfirmBill(f.admin, f.propID, in, "key-1")
			wantCode(t, err, "APPROVAL_REQUIRED")
			in.Approval = f.approver(t)
			res, err := f.svc.ConfirmBill(f.admin, f.propID, in, "key-1")
			must(t, err)
			if !res.Bill.Total.Equal(dec("3396600")) || len(res.Matches) != 1 || !res.Matches[0].InventoryPart.Equal(dec("50000")) || !res.Matches[0].PPVPart.Equal(dec("10000")) {
				t.Fatalf("confirmed: %+v", res)
			}
			if m == inventory.MethodAverage {
				f.wantBal(t, f.pair("MAIN", "SIRUP"), "50", "2525000")
			} else {
				f.wantBal(t, f.pair("MAIN", "SIRUP"), "50", "2550000")
			}
			f.wantBal(t, f.pair("BAR", "SIRUP"), "30", map[string]string{inventory.MethodAverage: "1485000", inventory.MethodFIFO: "1460000"}[m])
			f.wantGRNI(t, "0")
			d := f.draftOf(t, r)
			if d.Status != "BILLED" {
				t.Fatalf("draft: %s", d.Status)
			}
			// the same request again answers the first bill and posts nothing twice
			again, err := f.svc.ConfirmBill(f.admin, f.propID, in, "key-1")
			must(t, err)
			if again.Bill.ID != res.Bill.ID {
				t.Fatalf("replay made another bill: %d and %d", again.Bill.ID, res.Bill.ID)
			}
			if n := f.Count(t, `SELECT count(*) FROM supplier_bills WHERE property_id = $1`, f.propID); n != 1 {
				t.Fatalf("%d bills", n)
			}
			// a matched bill is not voided by itself, it is unmatched
			_, err = f.Payables.VoidBill(f.admin, f.propID, res.Bill.ID, payables.VoidInput{Reason: "mistake", Approval: f.voider(t)})
			wantCode(t, err, "BILL_HAS_STOCK_MATCH")
			back, err := f.svc.UnmatchBill(f.admin, f.propID, res.Bill.ID, inventory.UnmatchInput{Reason: "wrong invoice", Approval: f.voider(t)})
			must(t, err)
			if back.Status != "OPEN" {
				t.Fatalf("draft after the unmatch: %s", back.Status)
			}
			if m == inventory.MethodAverage {
				f.wantBal(t, f.pair("MAIN", "SIRUP"), "50", "2475000")
			} else {
				f.wantBal(t, f.pair("MAIN", "SIRUP"), "50", "2500000")
			}
			// the bill is voided, the receipt can be billed again, with the right invoice
			in2 := invoice(f.sup, "INV-2", billLine(r, 1, "5", "600000", "0"))
			res2, err := f.svc.ConfirmBill(f.admin, f.propID, in2, "key-2")
			must(t, err)
			if len(res2.Matches) != 1 || !res2.Matches[0].Variance.IsZero() {
				t.Fatalf("second bill: %+v", res2.Matches)
			}
			f.wantGRNI(t, "0")
		})
	}
}

func (f *fx) draftOf(t *testing.T, r inventory.Receipt) inventory.BillDraft {
	t.Helper()
	rc, err := f.svc.GetReceipt(f.admin, f.propID, r.ID)
	must(t, err)
	d, err := f.svc.GetBillDraft(f.admin, f.propID, *rc.DraftID)
	must(t, err)
	return d
}

func TestAPriceBelowTheOrderIsACreditToTheStockAndThePriceVariance(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	f.transfer(t, "MAIN", "BAR", "SIRUP", "30")
	in := invoice(f.sup, "INV-LOW", billLine(r, 1, "5", "588000", "0")) // 2% below
	in.Approval = f.approver(t)
	res, err := f.svc.ConfirmBill(f.admin, f.propID, in, "k")
	must(t, err)
	m := res.Matches[0]
	if !m.Variance.Equal(dec("-60000")) || !m.InventoryPart.Equal(dec("-50000")) || !m.PPVPart.Equal(dec("-10000")) {
		t.Fatalf("match: %+v", m)
	}
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "50", "2425000")
	f.wantGRNI(t, "0")
}

func TestADifferenceWithinTheToleranceNeedsNoApproval(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	in := invoice(f.sup, "INV-TOL", billLine(r, 1, "5", "605000", "0")) // 25,000 = 0.83%
	res, err := f.svc.ConfirmBill(f.admin, f.propID, in, "k")
	must(t, err)
	if !res.Matches[0].Variance.Equal(dec("25000")) || res.Preview.ApprovalRequired {
		t.Fatalf("%+v", res)
	}
	f.wantGRNI(t, "0")
}

func TestAnInvoiceForMoreThanWasReceivedIsRefusedAndALessOneBillsPartOfTheLine(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "4")
	_, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-5", billLine(r, 1, "5", "600000", "0")), "k1")
	wantCode(t, err, "BILL_EXCEEDS_RECEIPT")
	// 3 of the 4 karton: the rest stays on the draft and in goods received not invoiced
	res, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-3", billLine(r, 1, "3", "600000", "0")), "k2")
	must(t, err)
	if !res.Matches[0].QtyStock.Equal(dec("36")) || !res.Matches[0].ReceiptValue.Equal(dec("1800000")) {
		t.Fatalf("%+v", res.Matches[0])
	}
	d := f.draftOf(t, r)
	if d.Status != "PARTIAL" || !d.RemainingValue.Equal(dec("600000")) || !d.Lines[0].BillableQty.Equal(dec("1")) {
		t.Fatalf("draft: %+v", d)
	}
	f.wantGRNI(t, "-600000")
	res2, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-1", billLine(r, 1, "1", "600000", "0")), "k3")
	must(t, err)
	if !res2.Matches[0].ReceiptValue.Equal(dec("600000")) {
		t.Fatalf("%+v", res2.Matches[0])
	}
	if f.draftOf(t, r).Status != "BILLED" {
		t.Fatal("the draft is not billed")
	}
	f.wantGRNI(t, "0")
}

func TestALineThatIsNotStockIsOnTheBillAndNeverInTheStock(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	in := invoice(f.sup, "INV-D", billLine(r, 1, "5", "600000", "0"))
	in.ExtraLines = []inventory.ExtraBillLine{{AccountID: f.accounts["6160"], Description: "Delivery", Amount: dec("150000")}}
	res, err := f.svc.ConfirmBill(f.admin, f.propID, in, "k")
	must(t, err)
	if !res.Bill.Total.Equal(dec("3150000")) {
		t.Fatalf("bill total %s", res.Bill.Total)
	}
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "60", "3000000") // the delivery charge is not capitalised (D14)
	f.wantGRNI(t, "0")
	po2 := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "1", "600000")
	r2 := f.receive(t, po2, "1")
	bad := invoice(f.sup, "INV-X", billLine(r2, 1, "1", "600000", "0"))
	bad.ExtraLines = []inventory.ExtraBillLine{{AccountID: f.accounts["1310"], Amount: dec("10")}}
	_, err = f.svc.ConfirmBill(f.admin, f.propID, bad, "k2")
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestAnInvoiceNeedsTheReceiptOfItsSupplierAndASupplierThatIsActive(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	_, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.pasar, "INV-O", billLine(r, 1, "5", "600000", "0")), "k")
	wantCode(t, err, "VALIDATION_FAILED")
	// the same line twice on one invoice
	_, err = f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-D", billLine(r, 1, "2", "600000", "0"), billLine(r, 1, "3", "600000", "0")), "k2")
	wantCode(t, err, "VALIDATION_FAILED")
}

// 8.3: a market purchase without an order, up to the limit, received and billed in one request and paid in cash.
func TestAMarketPurchaseWithoutAnOrderIsLimitedAndCanBeReceivedAndBilled(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	in := inventory.ReceiptInput{ClientUUID: f.id(), SupplierID: f.pasar, WarehouseID: f.wh["MAIN"], Lines: []inventory.ReceiptLineInput{{ItemID: f.item["BERAS"], UnitID: f.unit["karung"], Qty: dec("2"), UnitPrice: ptr(dec("375000"))}}}
	_, err := f.svc.PostReceipt(f.admin, f.propID, in)
	wantCode(t, err, "DIRECT_RECEIPT_NOT_ALLOWED")
	f.settings(t, func(s *inventory.SettingsInput) { s.DirectReceiptLimit = ptr(dec("1000000")) })
	big := in
	big.ClientUUID = f.id()
	big.Lines = []inventory.ReceiptLineInput{{ItemID: f.item["BERAS"], UnitID: f.unit["karung"], Qty: dec("4"), UnitPrice: ptr(dec("300000"))}}
	_, err = f.svc.PostReceipt(f.admin, f.propID, big)
	wantCode(t, err, "DIRECT_RECEIPT_LIMIT")
	in.Bill = &inventory.ReceiptBillInput{SupplierInvoiceNumber: "NOTA 0412", BillDate: civil.MustParseDate("2026-09-30")}
	r, err := f.svc.PostReceipt(f.admin, f.propID, in)
	must(t, err)
	if !r.TotalValue.Equal(dec("750000")) || r.DraftStatus == nil || *r.DraftStatus != "BILLED" {
		t.Fatalf("%+v", r)
	}
	f.wantBal(t, f.pair("MAIN", "BERAS"), "50", "750000")
	f.wantGRNI(t, "0")
	if n := f.Count(t, `SELECT count(*) FROM supplier_bills WHERE property_id = $1 AND total = 750000`, f.propID); n != 1 {
		t.Fatalf("%d bills of 750,000", n)
	}
	// a repeated request answers the receipt it made
	again, err := f.svc.PostReceipt(f.admin, f.propID, in)
	must(t, err)
	if again.ID != r.ID {
		t.Fatal("the repeat made another receipt")
	}
	f.settings(t, func(s *inventory.SettingsInput) { s.DirectReceiptLimit = ptr(dec("0")) })
	unlimited := big
	unlimited.ClientUUID = f.id()
	if _, err := f.svc.PostReceipt(f.admin, f.propID, unlimited); err != nil {
		t.Fatalf("a limit of 0 is no limit: %v", err)
	}
}

func TestAReceiptNeverPassesTheOverReceiptToleranceOfItsOrder(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "10", "600000") // 120 botol, 5% = 126
	_, err := f.tryReceive(po, "10.6")                                 // 127.2 botol
	wantCode(t, err, "RECEIPT_EXCEEDS_ORDER")
	r1, err := f.tryReceive(po, "6")
	must(t, err)
	got, _ := f.svc.GetPurchaseOrder(f.admin, f.propID, po.ID)
	if got.Status != "PARTIAL" || r1.Lines[0].UnitPrice.String() != "600000" {
		t.Fatalf("order %s", got.Status)
	}
	_, err = f.tryReceive(po, "4.6") // 72 + 55.2 = 127.2 > 126
	wantCode(t, err, "RECEIPT_EXCEEDS_ORDER")
	if _, err := f.tryReceive(po, "4.5"); err != nil { // 72 + 54 = 126
		t.Fatal(err)
	}
	got, _ = f.svc.GetPurchaseOrder(f.admin, f.propID, po.ID)
	if got.Status != "RECEIVED" {
		t.Fatalf("order %s", got.Status)
	}
	_, err = f.tryReceive(po, "1")
	wantCode(t, err, "PO_NOT_RECEIVABLE")
}

func TestTheReceiptDoesNotOfferAPriceAgainstAnOrder(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r, err := f.svc.PostReceipt(f.admin, f.propID, inventory.ReceiptInput{ClientUUID: f.id(), POID: &po.ID,
		Lines: []inventory.ReceiptLineInput{{POLineNo: ptr(1), Qty: dec("5"), UnitPrice: ptr(dec("1"))}}})
	must(t, err)
	if r.Lines[0].UnitPrice.String() != "600000" {
		t.Fatalf("the price of the receipt is %s", r.Lines[0].UnitPrice)
	}
}

func TestAReceiptIsReversedOnItsDayWhileItIsUnbilledAndOnTheShelf(t *testing.T) {
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			f := newPurchasing(t, m, 0)
			po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
			r := f.receive(t, po, "5")
			rev, err := f.svc.ReverseReceipt(f.admin, f.propID, r.ID, "wrong delivery")
			must(t, err)
			if rev.Status != "REVERSED" {
				t.Fatal(rev.Status)
			}
			f.wantBal(t, f.pair("MAIN", "SIRUP"), "0", "0")
			f.wantGRNI(t, "0")
			got, _ := f.svc.GetPurchaseOrder(f.admin, f.propID, po.ID)
			if got.Status != "ORDERED" || !got.Lines[0].QtyReceived.IsZero() {
				t.Fatalf("order: %s %v", got.Status, got.Lines[0].QtyReceived)
			}
			if d := f.draftOf(t, r); d.Status != "DISCARDED" {
				t.Fatalf("draft %s", d.Status)
			}
			_, err = f.svc.ReverseReceipt(f.admin, f.propID, r.ID, "again")
			wantCode(t, err, "RECEIPT_NOT_REVERSIBLE")
			// the order can be received again
			f.receive(t, po, "5")
		})
	}
}

func TestAReceiptThatWasBilledUsedOrOfAnotherDayIsNotReversed(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	f.transfer(t, "MAIN", "BAR", "SIRUP", "50")
	_, err := f.svc.ReverseReceipt(f.admin, f.propID, r.ID, "x") // only 10 left on the shelf
	wantCode(t, err, "RECEIPT_NOT_REVERSIBLE")
	f.transfer(t, "BAR", "MAIN", "SIRUP", "50")
	_, err = f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "I", billLine(r, 1, "5", "600000", "0")), "k")
	must(t, err)
	_, err = f.svc.ReverseReceipt(f.admin, f.propID, r.ID, "x")
	wantCode(t, err, "RECEIPT_NOT_REVERSIBLE")
	// a receipt of an earlier day
	po2 := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "1", "600000")
	r2 := f.receive(t, po2, "1")
	must(t, f.Exec(t, `UPDATE inv_receipts SET received_on = received_on - 1 WHERE id = $1`, r2.ID))
	_, err = f.svc.ReverseReceipt(f.admin, f.propID, r2.ID, "x")
	wantCode(t, err, "RECEIPT_NOT_REVERSIBLE")
}

// 8.7: a return at the cost of the receipt line before the bill, and after it with the credit note linked.
func TestAReturnGoesBackAtTheCostOfTheReceiptLineAndLowersTheDraft(t *testing.T) {
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			f := newPurchasing(t, m, 0)
			f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
			po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
			r := f.receive(t, po, "5")
			ret, err := f.svc.PostReturn(f.admin, f.propID, inventory.ReturnInput{ClientUUID: f.id(), ReceiptID: r.ID, Reason: "damaged", Lines: []inventory.ReturnLineInput{{ReceiptLineNo: 1, Qty: dec("2")}}})
			must(t, err)
			if !ret.TotalValue.Equal(dec("1200000")) || !ret.Lines[0].QtyStock.Equal(dec("24")) {
				t.Fatalf("%+v", ret)
			}
			f.wantBal(t, f.pair("MAIN", "SIRUP"), "56", "2760000")
			d := f.draftOf(t, r)
			if !d.RemainingValue.Equal(dec("1800000")) || !d.Lines[0].BillableQty.Equal(dec("3")) {
				t.Fatalf("draft: %+v", d)
			}
			f.wantGRNI(t, "-1800000")
			res, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "I", billLine(r, 1, "3", "600000", "0")), "k")
			must(t, err)
			if !res.Matches[0].ReceiptValue.Equal(dec("1800000")) || !res.Matches[0].Variance.IsZero() {
				t.Fatalf("%+v", res.Matches[0])
			}
			f.wantGRNI(t, "0")
			if f.draftOf(t, r).Status != "BILLED" {
				t.Fatal("the draft is not billed")
			}
			_, err = f.svc.PostReturn(f.admin, f.propID, inventory.ReturnInput{ClientUUID: f.id(), ReceiptID: r.ID, Reason: "too much", Lines: []inventory.ReturnLineInput{{ReceiptLineNo: 1, Qty: dec("4")}}})
			wantCode(t, err, "VALIDATION_FAILED") // 3 karton are left that have not gone back
		})
	}
}

func TestAReturnOfBilledGoodsIsLinkedToTheCreditNoteAndTheDifferenceIsCleared(t *testing.T) {
	for _, tc := range []struct{ credit, ppv string }{{"1200000", "0"}, {"1260000", "-60000"}, {"1140000", "60000"}} {
		t.Run(tc.credit, func(t *testing.T) {
			f := newPurchasing(t, inventory.MethodAverage, 0)
			po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
			r := f.receive(t, po, "5")
			res, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "I", billLine(r, 1, "5", "600000", "0")), "k")
			must(t, err)
			ret, err := f.svc.PostReturn(f.admin, f.propID, inventory.ReturnInput{ClientUUID: f.id(), ReceiptID: r.ID, Reason: "damaged", Lines: []inventory.ReturnLineInput{{ReceiptLineNo: 1, Qty: dec("2")}}})
			must(t, err)
			f.wantGRNI(t, "1200000") // the bill took 3,000,000 and the return gave back 1,200,000: the credit note is still to come
			cn, err := f.Payables.PostCreditNote(f.admin, f.propID, payables.CreditNoteInput{BillID: res.Bill.ID, SupplierCreditNumber: "CN-1", CreditDate: civil.MustParseDate("2026-09-30"), Reason: "damaged goods",
				Lines: []payables.CreditLineInput{{BillLineNo: 1, Amount: dec(tc.credit)}}}, "cn-key")
			must(t, err)
			linked, err := f.svc.LinkCreditNote(f.admin, f.propID, ret.ID, cn.ID)
			must(t, err)
			if linked.CreditNoteID == nil || *linked.CreditNoteID != cn.ID {
				t.Fatalf("%+v", linked)
			}
			f.wantGRNI(t, "0")
			if got := f.Count(t, `SELECT count(*) FROM inv_bill_matches WHERE return_id = $1 AND ppv_part = $2`, ret.ID, dec(tc.ppv)); got != 1 {
				t.Fatalf("the credit note of %s should clear %s into the price variance", tc.credit, tc.ppv)
			}
			_, err = f.svc.LinkCreditNote(f.admin, f.propID, ret.ID, cn.ID)
			wantCode(t, err, "RETURN_HAS_CREDIT_NOTE")
			d := f.draftOf(t, r)
			if d.Status != "BILLED" {
				t.Fatalf("draft %s", d.Status)
			}
		})
	}
}

func TestAnUnpaidCreditNoteOnAMatchedBillIsAcceptedByThePayablesOnTheStockAccount(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	res, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "I", billLine(r, 1, "5", "600000", "0")), "k")
	must(t, err)
	if _, err := f.Payables.PostCreditNote(f.admin, f.propID, payables.CreditNoteInput{BillID: res.Bill.ID, SupplierCreditNumber: "CN", CreditDate: civil.MustParseDate("2026-09-30"), Reason: "r",
		Lines: []payables.CreditLineInput{{BillLineNo: 1, Amount: dec("100")}}}, "ck"); err != nil {
		t.Fatal(err)
	}
}

func TestPurchasingNeedsItsPermissions(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermInventoryView)
	for name, fn := range map[string]func() error{
		"order":   func() error { _, e := f.svc.CreatePurchaseOrder(viewer, f.propID, inventory.POInput{}); return e },
		"receive": func() error { _, e := f.svc.PostReceipt(viewer, f.propID, inventory.ReceiptInput{}); return e },
		"return":  func() error { _, e := f.svc.PostReturn(viewer, f.propID, inventory.ReturnInput{}); return e },
		"confirm": func() error { _, e := f.svc.ConfirmBill(viewer, f.propID, inventory.ConfirmBillInput{}, "k"); return e },
		"send":    func() error { _, e := f.svc.SendPurchaseOrder(viewer, f.propID, po.ID); return e },
	} {
		wantCode(t, fn(), "PERMISSION_DENIED")
		_ = name
	}
	if _, err := f.svc.ListPurchaseOrders(viewer, f.propID, inventory.POFilter{}); err != nil {
		t.Fatalf("who views the stock reads the orders: %v", err)
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.svc.ListPurchaseOrders(nobody, f.propID, inventory.POFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	// a person with only the permission to receive cannot bill, and one with only the permission to bill cannot post the payables' bill
	storekeeper := f.User(t, f.tenantID, f.propID, auth.PermInventoryReceive, auth.PermInventoryPurchase)
	_, err = f.svc.ConfirmBill(storekeeper, f.propID, inventory.ConfirmBillInput{}, "k")
	wantCode(t, err, "PERMISSION_DENIED")
	billerOnly := f.User(t, f.tenantID, f.propID, auth.PermInventoryBill)
	_, err = f.svc.ConfirmBill(billerOnly, f.propID, inventory.ConfirmBillInput{}, "k")
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestAnotherTenantsOrderIsNotFound(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	other := f.Tenant(t, "XYZ")
	op := f.PropertyIn(t, other.ID, "BALI2", "IDR", 0)
	otherCtx := roomstest.Admin(other.ID)
	_, err := f.svc.GetPurchaseOrder(otherCtx, op.ID, po.ID)
	wantCode(t, err, "PO_NOT_FOUND")
	_, err = f.svc.GetPurchaseOrder(otherCtx, f.propID, po.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestTheDraftsAndTheOrdersAreLockedInTheOrderOfTheStockLevel(t *testing.T) {
	// the run-time rule of 5.1: a request, a draft bill, a receipt, a return, an order, then the warehouse and the balances
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	err := f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		if _, err := f.Tenancy.RequireOpenBusinessDay(ctx, f.propID, db.ForShare, nil); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.StockOrders, db.ForUpdate, f.propID, []int64{po.ID}); err != nil {
			return err
		}
		return db.LockRows(ctx, db.StockReceipts, db.ForUpdate, f.propID, []int64{r.ID}) // a receipt after an order: against the order
	})
	if err == nil {
		t.Fatal("the lock order was not enforced")
	}
	_ = inventorydb.New
}

// When the VAT is part of the cost (the property is not a PKP) the cost basis of the line includes it, and it is cleared like any difference of cost (document 14, 5.3.4).
func TestTheVATThatIsPartOfTheCostIsPartOfTheCostBasisOfTheMatch(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	f.transfer(t, "MAIN", "BAR", "SIRUP", "30")
	in := invoice(f.sup, "INV-V", billLine(r, 1, "5", "600000", "330000")) // the price is the order's: the difference is the VAT
	res, err := f.svc.ConfirmBill(f.admin, f.propID, in, "k")
	must(t, err)
	m := res.Matches[0]
	if !m.BillCost.Equal(dec("3330000")) || !m.Variance.Equal(dec("330000")) || !m.InventoryPart.Equal(dec("275000")) || !m.PPVPart.Equal(dec("55000")) || res.Preview.ApprovalRequired {
		t.Fatalf("%+v", m)
	}
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "50", "2750000")
	f.wantGRNI(t, "0")
}

// Races of 13 (PR 2), with two goroutines and a barrier.

func race(fns ...func() error) []error {
	start := make(chan struct{})
	errs := make([]error, len(fns))
	var wg sync.WaitGroup
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

func succeeded(errs []error) int {
	n := 0
	for _, e := range errs {
		if e == nil {
			n++
		}
	}
	return n
}

func TestTwoReceiptsOfOneOrderAtOnceNeverPassTheToleranceOfTheOrder(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "10", "600000") // 120 botol, at most 126
	errs := race(
		func() error { _, e := f.tryReceive(po, "6"); return e },
		func() error { _, e := f.tryReceive(po, "6"); return e }, // 144 botol together: only one fits
	)
	if succeeded(errs) != 1 {
		t.Fatalf("results: %v", errs)
	}
	for _, e := range errs {
		if e != nil {
			wantCode(t, e, "RECEIPT_EXCEEDS_ORDER")
		}
	}
	got, _ := f.svc.GetPurchaseOrder(f.admin, f.propID, po.ID)
	if !got.Lines[0].QtyReceivedSt.Equal(dec("72")) {
		t.Fatalf("received %s", got.Lines[0].QtyReceivedSt)
	}
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "72", "3600000")
}

func TestAReceiptAndItsReversalAtOnceLeaveAConsistentOrder(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "10", "600000")
	r := f.receive(t, po, "5")
	errs := race(
		func() error { _, e := f.svc.ReverseReceipt(f.admin, f.propID, r.ID, "mistake"); return e },
		func() error { _, e := f.tryReceive(po, "5"); return e },
	)
	if succeeded(errs) != 2 {
		t.Fatalf("results: %v", errs)
	}
	got, _ := f.svc.GetPurchaseOrder(f.admin, f.propID, po.ID)
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "60", "3000000")
	if !got.Lines[0].QtyReceivedSt.Equal(dec("60")) || got.Status != "PARTIAL" {
		t.Fatalf("order %s received %s", got.Status, got.Lines[0].QtyReceivedSt)
	}
}

func TestTwoConfirmationsOfTheSameDraftBillItOnce(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	errs := race(
		func() error {
			_, e := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-A", billLine(r, 1, "5", "600000", "0")), "ka")
			return e
		},
		func() error {
			_, e := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-B", billLine(r, 1, "5", "600000", "0")), "kb")
			return e
		},
	)
	if succeeded(errs) != 1 {
		t.Fatalf("results: %v", errs)
	}
	for _, e := range errs {
		if e != nil {
			wantCode(t, e, "BILL_EXCEEDS_RECEIPT")
		}
	}
	if n := f.Count(t, `SELECT count(*) FROM supplier_bills WHERE property_id = $1`, f.propID); n != 1 {
		t.Fatalf("%d bills", n)
	}
	f.wantGRNI(t, "0")
}

func TestAReceiptAndATransferOnTheSameBalanceDoNotDeadlock(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	f.open(t, "MAIN", "SIRUP", "botol", "100", "48000")
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	errs := race(
		func() error { _, e := f.tryReceive(po, "5"); return e },
		func() error {
			pf, pt := f.pair("MAIN", "SIRUP"), f.pair("BAR", "SIRUP")
			return f.tx([]inventory.Pair{pf, pt}, func(ctx context.Context, st *inventory.Stock) error {
				out, err := st.Out(ctx, inventory.OutArgs{Pair: pf, Kind: inventory.KindTransferOut, Qty: dec("10"), Ref: f.ref()})
				if err != nil {
					return err
				}
				_, err = st.In(ctx, inventory.InArgs{Pair: pt, Kind: inventory.KindTransferIn, Qty: dec("10"), Value: out.Value.Neg(), Ref: f.ref()})
				return err
			})
		},
	)
	if succeeded(errs) != 2 {
		t.Fatalf("results: %v", errs)
	}
}

func TestAConfirmationAndAPaymentOfTheSameSupplierAtOnceKeepTheLockOrder(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	first, err := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-1", billLine(r, 1, "3", "600000", "0")), "k1")
	must(t, err)
	errs := race(
		func() error {
			_, e := f.svc.ConfirmBill(f.admin, f.propID, invoice(f.sup, "INV-2", billLine(r, 1, "2", "600000", "0")), "k2")
			return e
		},
		func() error {
			_, e := f.Payables.PostPayment(f.admin, f.propID, payables.PaymentInput{SupplierID: f.sup, PaymentDate: civil.MustParseDate("2026-09-30"), PaymentMethod: "CASH",
				Allocations: []payables.AllocationInput{{BillID: first.Bill.ID, Amount: dec("100000")}}}, "pk")
			return e
		},
	)
	if succeeded(errs) != 2 {
		t.Fatalf("results: %v", errs)
	}
}
