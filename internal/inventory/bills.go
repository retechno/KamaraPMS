package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The draft bill and the three-way match (35-inventory.md 5.3.4, F1, F13). A receipt makes a draft of the bill for its supplier; the supplier's invoice confirms it into a real bill of the payables, on the account of
// goods received not invoiced, matched line by line against the receipt (quantity) and the order (price). A difference of price is cleared out of that account into the value of the stock still on hand and into the
// purchase price variance, by COST_ADJUST movements dated the day of the confirmation.

// States of a draft bill.
const (
	DraftOpen      = "OPEN"
	DraftPartial   = "PARTIAL"
	DraftBilled    = "BILLED"
	DraftDiscarded = "DISCARDED"
)

// Payables is what the purchasing needs of the payables: to post a bill on the accounts that only the inventory posts to, to void it when it is unmatched, and to know how the VAT of a date is treated.
type Payables interface {
	PostBillWith(ctx context.Context, propertyID int64, in payables.BillInput, key string, opt payables.BillOptions) (payables.Bill, error)
	VoidBillOfInventory(ctx context.Context, propertyID, id int64, in payables.VoidInput, day civil.Date) (payables.Bill, error)
	VATTreatment(ctx context.Context, propertyID int64, date civil.Date) (string, error)
}

// Approver verifies the approval of a price difference (the kind INV_BILL_VARIANCE).
type Approver interface {
	Verify(ctx context.Context, req iam.ApprovalRequest) (iam.Approval, error)
}

// SetPayables and SetApprover wire the services the bills need; without them a bill cannot be confirmed.
func (s *Service) SetPayables(p Payables) { s.pay = p }

// SetApprover wires the verifier of approvals.
func (s *Service) SetApprover(a Approver) { s.approver = a }

// CheckVoid is the guard that payables asks before it voids a bill: a bill matched to goods received is unmatched in the inventory, never voided by itself (F13).
func (s *Service) CheckVoid(ctx context.Context, tenantID, propertyID, billID int64) error {
	n, err := s.q(ctx).CountLiveMatchesOfBill(ctx, inventorydb.CountLiveMatchesOfBillParams{TenantID: tenantID, PropertyID: propertyID, BillID: billID})
	if err != nil {
		return err
	}
	if n > 0 {
		return apperr.Conflict("BILL_HAS_STOCK_MATCH", "the bill is matched to goods received: unmatch it in the inventory (Draft bills) instead of voiding it").WithContext("matches", n)
	}
	return nil
}

// ---------------------------------------------------------------------------------------------------------------- reading

// MatchOfBill is a line of a bill matched to a receipt line.
type MatchOfBill struct {
	ID                    int64           `json:"id"`
	ReceiptID             int64           `json:"receipt_id"`
	ReceiptLineNo         int             `json:"receipt_line_no"`
	BillID                int64           `json:"bill_id"`
	BillNumber            string          `json:"bill_number"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	BillLineNo            int             `json:"bill_line_no"`
	QtyStock              decimal.Decimal `json:"qty_stock"`
	BillCost              decimal.Decimal `json:"bill_cost"`
	ReceiptValue          decimal.Decimal `json:"receipt_value"`
	Variance              decimal.Decimal `json:"variance"`
	InventoryPart         decimal.Decimal `json:"inventory_part"`
	PPVPart               decimal.Decimal `json:"ppv_part"`
	CreditNoteID          *int64          `json:"credit_note_id"`
	ReturnID              *int64          `json:"return_id"`
	CreatedAt             time.Time       `json:"created_at"`
}

func (s *Service) matchesOfReceipt(ctx context.Context, p auth.Principal, propertyID, receiptID int64) ([]MatchOfBill, error) {
	rows, err := s.q(ctx).ListMatchesOfReceipt(ctx, inventorydb.ListMatchesOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: receiptID})
	if err != nil {
		return nil, err
	}
	out := make([]MatchOfBill, 0, len(rows))
	for _, r := range rows {
		out = append(out, MatchOfBill{ID: r.ID, ReceiptID: r.ReceiptID, ReceiptLineNo: int(r.ReceiptLineNo), BillID: r.BillID, BillNumber: r.BillNumber, SupplierInvoiceNumber: r.SupplierInvoiceNumber,
			BillLineNo: int(r.BillLineNo), QtyStock: r.QtyStock, BillCost: r.BillCost, ReceiptValue: r.ReceiptValue, Variance: r.Variance, InventoryPart: r.InventoryPart, PPVPart: r.PpvPart,
			CreditNoteID: r.CreditNoteID, ReturnID: r.ReturnID, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// BillDraftBrief is a row of the list of draft bills.
type BillDraftBrief struct {
	ID             int64           `json:"id"`
	ReceiptID      int64           `json:"receipt_id"`
	ReceiptNumber  string          `json:"receipt_number"`
	Status         string          `json:"status"`
	SupplierID     int64           `json:"supplier_id"`
	SupplierName   string          `json:"supplier_name"`
	ReceivedOn     civil.Date      `json:"received_on"`
	PONumber       *string         `json:"po_number"`
	WarehouseName  string          `json:"warehouse_name"`
	ReceiptValue   decimal.Decimal `json:"receipt_value"`
	RemainingValue decimal.Decimal `json:"remaining_value"`
}

// BillDraft is a draft bill with the lines of its receipt (what is left to bill of each) and the bills already matched.
type BillDraft struct {
	BillDraftBrief
	DeliveryNote *string       `json:"delivery_note"`
	Lines        []ReceiptLine `json:"lines"`
	Matches      []MatchOfBill `json:"matches"`
	Version      int32         `json:"version"`
}

// DraftFilter narrows the list of draft bills.
type DraftFilter struct {
	Status     string
	SupplierID *int64
	OpenOnly   bool
	Limit      int
}

func (s *Service) canBill(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryBill)
}

// billReaders read the drafts: who bills, who receives, who purchases and who manages the module.
var billReaders = []auth.Permission{auth.PermInventoryBill, auth.PermInventoryReceive, auth.PermInventoryPurchase, auth.PermInventoryView, auth.PermInventoryManage}

// ListBillDrafts lists the draft bills, oldest receipt first.
func (s *Service) ListBillDrafts(ctx context.Context, propertyID int64, f DraftFilter) ([]BillDraftBrief, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := auth.RequireAny(ctx, s.authz, propertyID, billReaders...); err != nil {
		return nil, err
	}
	arg := inventorydb.ListDraftsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), SupplierID: f.SupplierID, RowLimit: limitOf(f.Limit)}
	if f.OpenOnly {
		t := true
		arg.OpenOnly = &t
	}
	rows, err := s.q(ctx).ListDrafts(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]BillDraftBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, BillDraftBrief{ID: r.ID, ReceiptID: r.ReceiptID, ReceiptNumber: r.ReceiptNumber, Status: r.Status, SupplierID: r.SupplierID, SupplierName: r.SupplierName, ReceivedOn: r.ReceivedOn,
			PONumber: r.PoNumber, WarehouseName: r.WarehouseName, ReceiptValue: r.ReceiptValue, RemainingValue: r.RemainingValue})
	}
	return out, nil
}

// GetBillDraft answers one draft with the lines of its receipt.
func (s *Service) GetBillDraft(ctx context.Context, propertyID, id int64) (BillDraft, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return BillDraft{}, err
	}
	if err := auth.RequireAny(ctx, s.authz, propertyID, billReaders...); err != nil {
		return BillDraft{}, err
	}
	d, err := s.q(ctx).GetDraft(ctx, inventorydb.GetDraftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return BillDraft{}, orNotFound(err, notFound("BILL_DRAFT_NOT_FOUND", "draft bill"))
	}
	rc, err := s.receipt(ctx, p, propertyID, d.ReceiptID)
	if err != nil {
		return BillDraft{}, err
	}
	return BillDraft{BillDraftBrief: BillDraftBrief{ID: d.ID, ReceiptID: d.ReceiptID, ReceiptNumber: d.ReceiptNumber, Status: d.Status, SupplierID: d.SupplierID, SupplierName: d.SupplierName,
		ReceivedOn: d.ReceivedOn, PONumber: d.PoNumber, WarehouseName: d.WarehouseName, ReceiptValue: rc.TotalValue, RemainingValue: rc.BillableValue}, DeliveryNote: d.DeliveryNote,
		Lines: rc.Lines, Matches: rc.Matches, Version: d.Version}, nil
}

// DiscardBillDraft sets a draft aside: the receipt will never be billed by a bill of this system, which is for a delivery that was not charged (inventory.bill). Nothing may be billed of it yet.
func (s *Service) DiscardBillDraft(ctx context.Context, propertyID, id int64) (BillDraft, error) {
	p, err := s.canBill(ctx, propertyID)
	if err != nil {
		return BillDraft{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.StockBillDrafts, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("BILL_DRAFT_NOT_FOUND", "draft bill"))
		}
		q := s.q(ctx)
		d, err := q.GetDraft(ctx, inventorydb.GetDraftParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("BILL_DRAFT_NOT_FOUND", "draft bill"))
		}
		if d.Status != DraftOpen {
			return apperr.Conflict("BILL_DRAFT_STATE_INVALID", "only a draft with nothing billed is discarded").WithContext("status", d.Status)
		}
		if err := q.SetDraftStatus(ctx, inventorydb.SetDraftStatusParams{PropertyID: propertyID, ID: id, Status: DraftDiscarded}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.bill_draft_discarded", "inv_bill_draft", id, d.ReceiptNumber, map[string]any{"status": d.Status}, map[string]any{"status": DraftDiscarded}))
	})
	if err != nil {
		return BillDraft{}, err
	}
	return s.GetBillDraft(ctx, propertyID, id)
}

// ---------------------------------------------------------------------------------------------------------------- the match

// ConfirmLineInput is a line of the supplier's invoice that is matched to a line of a receipt: the quantity and the price as they are on the invoice, in the unit of the receipt line.
type ConfirmLineInput struct {
	ReceiptID     int64           `json:"receipt_id"`
	ReceiptLineNo int             `json:"receipt_line_no"`
	Qty           decimal.Decimal `json:"qty"`
	UnitPrice     decimal.Decimal `json:"unit_price"`
	VATAmount     decimal.Decimal `json:"vat_amount"`
}

// ExtraBillLine is a line of the invoice that is not stock (a delivery charge, a discount that is not a price): it is booked to the account named and is never capitalised into the stock (D14).
type ExtraBillLine struct {
	AccountID    int64           `json:"account_id"`
	Description  string          `json:"description"`
	Amount       decimal.Decimal `json:"amount"`
	VATAmount    decimal.Decimal `json:"vat_amount"`
	DepartmentID *int64          `json:"department_id"`
}

// ConfirmBillInput confirms receipts into a bill of one supplier: the number and the date of the supplier's invoice, the lines matched to receipt lines, the lines that are not stock, and the approval of the
// price difference when it is above the tolerance.
type ConfirmBillInput struct {
	SupplierID            int64              `json:"supplier_id"`
	SupplierInvoiceNumber string             `json:"supplier_invoice_number"`
	BillDate              civil.Date         `json:"bill_date"`
	DueDate               *civil.Date        `json:"due_date"`
	Description           string             `json:"description"`
	Lines                 []ConfirmLineInput `json:"lines"`
	ExtraLines            []ExtraBillLine    `json:"extra_lines"`
	Approval              *iam.ApprovalInput `json:"approval"`
}

// PreviewLine is what the match makes of one line of the invoice: the three sources side by side and the split of the difference.
type PreviewLine struct {
	ReceiptID      int64           `json:"receipt_id"`
	ReceiptLineNo  int             `json:"receipt_line_no"`
	ReceiptNumber  string          `json:"receipt_number"`
	ItemCode       string          `json:"item_code"`
	ItemName       string          `json:"item_name"`
	UnitCode       string          `json:"unit_code"`
	ReceiptQty     decimal.Decimal `json:"receipt_qty"`
	BillableQty    decimal.Decimal `json:"billable_qty"`
	ReceiptPrice   decimal.Decimal `json:"receipt_price"`
	InvoiceQty     decimal.Decimal `json:"invoice_qty"`
	InvoicePrice   decimal.Decimal `json:"invoice_price"`
	BilledQtyStock decimal.Decimal `json:"billed_qty_stock"`
	Amount         decimal.Decimal `json:"amount"`
	VATAmount      decimal.Decimal `json:"vat_amount"`
	BillCost       decimal.Decimal `json:"bill_cost"`
	ReceiptValue   decimal.Decimal `json:"receipt_value"`
	PriceVariance  decimal.Decimal `json:"price_variance"`
	Variance       decimal.Decimal `json:"variance"`
	OnHandQtyStock decimal.Decimal `json:"on_hand_qty_stock"`
	InventoryPart  decimal.Decimal `json:"inventory_part"`
	PPVPart        decimal.Decimal `json:"ppv_part"`
	QtyOverReceipt bool            `json:"qty_over_receipt"`
}

// BillPreview is the match of an invoice before it is confirmed: the lines, the total difference and whether it needs an approval.
type BillPreview struct {
	VATTreatment     string          `json:"vat_treatment"`
	Lines            []PreviewLine   `json:"lines"`
	ReceiptTotal     decimal.Decimal `json:"receipt_total"`
	StockInvoiceNet  decimal.Decimal `json:"stock_invoice_net"`
	ExtraTotal       decimal.Decimal `json:"extra_total"`
	VATTotal         decimal.Decimal `json:"vat_total"`
	BillTotal        decimal.Decimal `json:"bill_total"`
	Variance         decimal.Decimal `json:"variance"`
	ApprovalAmount   decimal.Decimal `json:"approval_amount"`
	ToleranceAmount  decimal.Decimal `json:"tolerance_amount"`
	ApprovalRequired bool            `json:"approval_required"`
	InventoryPart    decimal.Decimal `json:"inventory_part"`
	PPVPart          decimal.Decimal `json:"ppv_part"`
}

type matchPlan struct {
	in           ConfirmLineInput
	rl           inventorydb.GetReceiptLineRow
	itemCode     string
	billable     decimal.Decimal // stock units
	billedStock  decimal.Decimal
	amount       decimal.Decimal
	cost         decimal.Decimal // the invoice amount, and its VAT when the VAT is part of what the purchase cost
	receiptValue decimal.Decimal
	variance     decimal.Decimal // cost - receipt value
	netVariance  decimal.Decimal // amount - receipt value: the price difference, without the VAT
	onHand       decimal.Decimal
	invPart      decimal.Decimal
	ppvPart      decimal.Decimal
	invStock     decimal.Decimal
}

// planBill reads the receipt lines of an invoice and works the match out. It writes nothing; confirming it runs it again under the locks.
func (s *Service) planBill(ctx context.Context, p auth.Principal, propertyID int64, in ConfirmBillInput, decimals int32, cfg Settings) ([]matchPlan, error) {
	q := s.q(ctx)
	var f []apperr.FieldError
	if len(in.Lines) == 0 || len(in.Lines) > 100 {
		return nil, invalid("bill", fieldErr("lines", "OUT_OF_RANGE", "1 to 100 lines matched to a receipt"))
	}
	seen := map[[2]int64]bool{}
	plans := make([]matchPlan, 0, len(in.Lines))
	for i, l := range in.Lines {
		at := fmt.Sprintf("lines[%d]", i)
		key := [2]int64{l.ReceiptID, int64(l.ReceiptLineNo)}
		if seen[key] {
			f = append(f, fieldErr(at+".receipt_line_no", "DUPLICATE", "a line of a receipt is on the invoice once"))
			continue
		}
		seen[key] = true
		if !l.Qty.IsPositive() || l.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !l.Qty.Equal(Round6(l.Qty)) {
			f = append(f, fieldErr(at+".qty", "INVALID_VALUE", "above zero, six decimals at most"))
			continue
		}
		if !l.UnitPrice.IsPositive() || l.UnitPrice.GreaterThan(decimal.NewFromInt(1_000_000_000_000)) || !l.UnitPrice.Equal(l.UnitPrice.Round(decimals)) {
			f = append(f, fieldErr(at+".unit_price", "INVALID_VALUE", fmt.Sprintf("above zero, %d decimals at most", decimals)))
			continue
		}
		if l.VATAmount.IsNegative() || !l.VATAmount.Equal(l.VATAmount.Round(decimals)) {
			f = append(f, fieldErr(at+".vat_amount", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", decimals)))
			continue
		}
		rl, err := q.GetReceiptLine(ctx, inventorydb.GetReceiptLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: l.ReceiptID, LineNo: int32(l.ReceiptLineNo)}) //nolint:gosec // G115: a line number
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f = append(f, fieldErr(at+".receipt_line_no", "NOT_FOUND", "no such receipt line in this property"))
			continue
		case err != nil:
			return nil, err
		case rl.ReceiptStatus != ReceiptPosted:
			f = append(f, fieldErr(at+".receipt_id", "REVERSED", "the receipt was reversed"))
			continue
		case rl.SupplierID != in.SupplierID:
			f = append(f, fieldErr(at+".receipt_id", "OTHER_SUPPLIER", "the receipt is from another supplier"))
			continue
		}
		draft, err := q.GetDraftOfReceipt(ctx, inventorydb.GetDraftOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: l.ReceiptID})
		if err != nil || draft.Status == DraftDiscarded {
			f = append(f, fieldErr(at+".receipt_id", "NO_DRAFT", "the draft bill of this receipt was set aside"))
			continue
		}
		pl := matchPlan{in: l, rl: rl, itemCode: rl.ItemCode}
		pl.billable = rl.QtyStock.Sub(rl.QtyBilledStock).Sub(rl.QtyReturnedStock)
		if pl.billable.IsNegative() {
			pl.billable = decimal.Zero
		}
		remainingValue := rl.Value.Sub(rl.ValueBilled).Sub(rl.ValueReturned)
		pl.invStock = StockQty(l.Qty, rl.StockNum, rl.StockDen)
		limit := Round6(pl.billable.Mul(decimal.NewFromInt(100).Add(cfg.QtyTolerancePct)).Div(decimal.NewFromInt(100)))
		if !pl.billable.IsPositive() || pl.invStock.GreaterThan(limit) {
			return nil, apperr.Conflict("BILL_EXCEEDS_RECEIPT", fmt.Sprintf("the invoice has more of %s than was received and not yet billed (%s): record the delivery that is missing, or ask the supplier to correct the invoice", rl.ItemCode, pl.billable)).
				WithContext("item_id", rl.ItemID).WithContext("receipt_number", rl.ReceiptNumber).WithContext("billable", pl.billable.String()).WithContext("invoice", pl.invStock.String())
		}
		pl.billedStock = decimal.Min(pl.invStock, pl.billable)
		switch {
		case pl.billedStock.Equal(pl.billable):
			pl.receiptValue = remainingValue // the last piece of a line takes what is left of its value
		default:
			pl.receiptValue = remainingValue.Mul(pl.billedStock).Div(pl.billable).Round(decimals)
		}
		pl.amount = l.Qty.Mul(l.UnitPrice).Round(decimals)
		pl.netVariance = pl.amount.Sub(pl.receiptValue)
		plans = append(plans, pl)
	}
	if len(f) > 0 {
		return nil, invalid("bill", f...)
	}
	return plans, nil
}

// applyTreatment works the cost of each line out: the invoice amount, and its VAT when the VAT is part of what the purchase cost (the treatment EXPENSE of the date of the bill, document 14); the variance is that
// cost less the value of what was received. treatments say the treatment of each line (a line with no VAT has none).
func applyTreatment(plans []matchPlan, treatments []string) {
	for i := range plans {
		pl := &plans[i]
		pl.cost = pl.amount
		if i < len(treatments) && treatments[i] == "EXPENSE" {
			pl.cost = pl.cost.Add(pl.in.VATAmount)
		}
		pl.variance = pl.cost.Sub(pl.receiptValue)
	}
}

// splitVariance divides a variance between the stock still on hand and the purchase price variance (D2): the part of the billed quantity that is still on the shelf takes its share, the rest is already sold, issued
// or moved. Under the moving average "on hand" is the balance of the receiving warehouse; under FIFO it is the layer of the receipt.
func (s *Service) splitVariance(ctx context.Context, p auth.Principal, propertyID int64, method string, plans []matchPlan, decimals int32) error {
	for i := range plans {
		pl := &plans[i]
		oh, err := s.q(ctx).OnHandForReceipt(ctx, inventorydb.OnHandForReceiptParams{PropertyID: propertyID, WarehouseID: pl.rl.WarehouseID, ItemID: pl.rl.ItemID, MovementID: pl.rl.MovementID})
		if err != nil {
			return err
		}
		pl.onHand = oh.BalanceQty
		if method == MethodFIFO {
			pl.onHand = oh.LayerQty
		}
		pl.invPart = decimal.Zero
		if !pl.variance.IsZero() && pl.onHand.IsPositive() && pl.billedStock.IsPositive() {
			pl.invPart = pl.variance.Mul(decimal.Min(pl.billedStock, pl.onHand)).Div(pl.billedStock).Round(decimals)
		}
		pl.ppvPart = pl.variance.Sub(pl.invPart)
	}
	return nil
}

func uniform(treatment string, plans []matchPlan) []string {
	out := make([]string, len(plans))
	for i := range out {
		out[i] = treatment
	}
	return out
}

func summarize(plans []matchPlan, extra []ExtraBillLine, treatment string, cfg Settings) BillPreview {
	out := BillPreview{VATTreatment: treatment, Lines: make([]PreviewLine, 0, len(plans))}
	absNet := decimal.Zero
	for _, pl := range plans {
		out.ReceiptTotal = out.ReceiptTotal.Add(pl.receiptValue)
		out.StockInvoiceNet = out.StockInvoiceNet.Add(pl.amount)
		out.VATTotal = out.VATTotal.Add(pl.in.VATAmount)
		out.Variance = out.Variance.Add(pl.variance)
		out.InventoryPart = out.InventoryPart.Add(pl.invPart)
		out.PPVPart = out.PPVPart.Add(pl.ppvPart)
		absNet = absNet.Add(pl.netVariance.Abs())
		out.Lines = append(out.Lines, PreviewLine{ReceiptID: pl.in.ReceiptID, ReceiptLineNo: pl.in.ReceiptLineNo, ReceiptNumber: pl.rl.ReceiptNumber, ItemCode: pl.rl.ItemCode, ItemName: pl.rl.ItemName, UnitCode: pl.rl.UnitCode,
			ReceiptQty: pl.rl.QtyPurchase, BillableQty: purchaseQty(pl.billable, pl.rl.StockNum, pl.rl.StockDen), ReceiptPrice: pl.rl.UnitPrice, InvoiceQty: pl.in.Qty, InvoicePrice: pl.in.UnitPrice,
			BilledQtyStock: pl.billedStock, Amount: pl.amount, VATAmount: pl.in.VATAmount, BillCost: pl.cost, ReceiptValue: pl.receiptValue, PriceVariance: pl.netVariance, Variance: pl.variance,
			OnHandQtyStock: pl.onHand, InventoryPart: pl.invPart, PPVPart: pl.ppvPart, QtyOverReceipt: pl.invStock.GreaterThan(pl.billable)})
	}
	for _, e := range extra {
		out.ExtraTotal = out.ExtraTotal.Add(e.Amount)
		out.VATTotal = out.VATTotal.Add(e.VATAmount)
	}
	out.BillTotal = out.StockInvoiceNet.Add(out.ExtraTotal).Add(out.VATTotal)
	out.ApprovalAmount = absNet
	out.ToleranceAmount = out.ReceiptTotal.Mul(cfg.PriceTolerancePct).Div(decimal.NewFromInt(100))
	out.ApprovalRequired = absNet.GreaterThan(out.ToleranceAmount)
	return out
}

func (s *Service) checkExtraLines(extra []ExtraBillLine, decimals int32) error {
	var f []apperr.FieldError
	for i, e := range extra {
		at := fmt.Sprintf("extra_lines[%d].", i)
		if e.AccountID < 1 {
			f = append(f, fieldErr(at+"account_id", "REQUIRED", "the account a line that is not stock is charged to"))
		}
		if !e.Amount.IsPositive() || !e.Amount.Equal(e.Amount.Round(decimals)) {
			f = append(f, fieldErr(at+"amount", "INVALID_VALUE", fmt.Sprintf("above zero, %d decimals at most", decimals)))
		}
		if e.VATAmount.IsNegative() || !e.VATAmount.Equal(e.VATAmount.Round(decimals)) {
			f = append(f, fieldErr(at+"vat_amount", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", decimals)))
		}
		if len([]rune(trimmed(e.Description))) > 300 {
			f = append(f, fieldErr(at+"description", "INVALID_VALUE", "300 characters at most"))
		}
	}
	if len(f) > 0 {
		return invalid("bill", f...)
	}
	return nil
}

// PreviewBill works the match of an invoice out without writing anything (inventory.bill): the lines of the receipts and of the invoice side by side, the difference, how it splits, and whether an approval is needed.
func (s *Service) PreviewBill(ctx context.Context, propertyID int64, in ConfirmBillInput) (BillPreview, error) {
	p, err := s.canBill(ctx, propertyID)
	if err != nil {
		return BillPreview{}, err
	}
	if s.pay == nil {
		return BillPreview{}, apperr.Internal(errors.New("inventory: the payables are not wired"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return BillPreview{}, err
	}
	if in.BillDate.IsZero() {
		return BillPreview{}, invalid("bill", fieldErr("bill_date", "REQUIRED", "the date of the invoice"))
	}
	var treatment string
	if err := s.txm.WithinTx(ctx, func(ctx context.Context) error { // the PKP status of the date takes a lock of its own: it is read in a short transaction, before the one that posts
		var err error
		treatment, err = s.pay.VATTreatment(ctx, propertyID, in.BillDate)
		return err
	}); err != nil {
		return BillPreview{}, err
	}
	cfg, err := s.settingsOrDefault(ctx, p, propertyID)
	if err != nil {
		return BillPreview{}, err
	}
	if err := s.checkExtraLines(in.ExtraLines, prop.CurrencyDecimals); err != nil {
		return BillPreview{}, err
	}
	plans, err := s.planBill(ctx, p, propertyID, in, prop.CurrencyDecimals, cfg)
	if err != nil {
		return BillPreview{}, err
	}
	applyTreatment(plans, uniform(treatment, plans))
	if err := s.splitVariance(ctx, p, propertyID, cfg.CostingMethod, plans, prop.CurrencyDecimals); err != nil {
		return BillPreview{}, err
	}
	return summarize(plans, in.ExtraLines, treatment, cfg), nil
}

// ConfirmResult is what a confirmed bill answers: the bill of the payables, the matches and the difference.
type ConfirmResult struct {
	Bill    payables.Bill `json:"bill"`
	Matches []MatchOfBill `json:"matches"`
	Preview BillPreview   `json:"preview"`
}

// ConfirmBill confirms the receipts into a bill (inventory.bill and payables.post). The invoice is matched against the receipts (quantity) and the order (price); a difference above the tolerance needs the approval of
// someone who holds inventory.approve (kind INV_BILL_VARIANCE); the bill is posted by the payables on goods received not invoiced; the difference is cleared into the stock on hand and the purchase price variance.
// Lock order: the drafts and the receipts, then the stock, then the payables (the accounting settings, the supplier, the number): 45 before 46, 47 and 50.
func (s *Service) ConfirmBill(ctx context.Context, propertyID int64, in ConfirmBillInput, key string) (ConfirmResult, error) {
	p, err := s.canBill(ctx, propertyID)
	if err != nil {
		return ConfirmResult{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPayablesPost); err != nil {
		return ConfirmResult{}, err
	}
	if s.pay == nil || s.approver == nil {
		return ConfirmResult{}, apperr.Internal(errors.New("inventory: the payables are not wired"))
	}
	if res, ok, err := s.replayOf(ctx, p, propertyID, key); err != nil || ok {
		return res, err
	}
	prev, err := s.PreviewBill(ctx, propertyID, in) // the same reading, before the transaction: it says whether an approval is needed
	if err != nil {
		return ConfirmResult{}, err
	}
	var approval *iam.Approval
	if prev.ApprovalRequired {
		a, err := s.approver.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindInvBillVariance, Amount: prev.ApprovalAmount})
		if err != nil {
			return ConfirmResult{}, err
		}
		approval = &a
	}
	var out ConfirmResult
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		recIDs := make([]int64, 0, len(in.Lines))
		for _, l := range in.Lines {
			recIDs = append(recIDs, l.ReceiptID)
		}
		slices.Sort(recIDs)
		recIDs = slices.Compact(recIDs)
		var draftIDs []int64
		for _, rid := range recIDs {
			d, err := s.q(ctx).GetDraftOfReceipt(ctx, inventorydb.GetDraftOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: rid})
			if err != nil {
				return orNotFound(err, invalid("bill", fieldErr("lines", "NOT_FOUND", "no such receipt in this property")))
			}
			draftIDs = append(draftIDs, d.ID)
		}
		if err := db.LockRows(ctx, db.StockBillDrafts, db.ForUpdate, propertyID, draftIDs); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.StockReceipts, db.ForUpdate, propertyID, recIDs); err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(in.Lines))
		for _, l := range in.Lines {
			rl, err := s.q(ctx).GetReceiptLine(ctx, inventorydb.GetReceiptLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: l.ReceiptID, LineNo: int32(l.ReceiptLineNo)}) //nolint:gosec // G115: a line number
			if err != nil {
				return orNotFound(err, invalid("bill", fieldErr("lines", "NOT_FOUND", "no such receipt line in this property")))
			}
			pairs = append(pairs, Pair{rl.WarehouseID, rl.ItemID})
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		stock, err := s.ledger.Lock(ctx, s.q(ctx), book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		if res, ok, err := s.replayOf(ctx, p, propertyID, key); err != nil || ok { // a request that was waiting for the locks while the same one was confirmed
			out = res
			return err
		}
		res, err := s.matchAndPost(ctx, p, propertyID, bd, stock, in, approval, key)
		out = res
		return err
	})
	return out, err
}

// replayOf answers the confirmation that an Idempotency-Key made already, so that a repeated request returns the first result and decides and posts nothing twice.
func (s *Service) replayOf(ctx context.Context, p auth.Principal, propertyID int64, key string) (ConfirmResult, bool, error) {
	if key == "" {
		return ConfirmResult{}, false, nil
	}
	id, err := s.q(ctx).FindBillByKey(ctx, inventorydb.FindBillByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, Key: &key})
	if errors.Is(err, pgx.ErrNoRows) {
		return ConfirmResult{}, false, nil
	}
	if err != nil {
		return ConfirmResult{}, false, err
	}
	matches, err := s.matchesOfBill(ctx, p.TenantID, propertyID, id)
	if err != nil || len(matches) == 0 {
		return ConfirmResult{}, false, err
	}
	return ConfirmResult{Matches: matches, Bill: payables.Bill{ID: id}}, true, nil
}

// billedLine is what a line of the bill says about the goods it is for.
type billedLine struct {
	description string
	amount      decimal.Decimal
	vat         decimal.Decimal
}

// billInputOf builds the bill of the payables: a line for each matched line on goods received not invoiced, then the lines that are not stock.
func billInputOf(in ConfirmBillInput, grniID int64, matched []billedLine) payables.BillInput {
	bill := payables.BillInput{SupplierID: in.SupplierID, SupplierInvoiceNumber: in.SupplierInvoiceNumber, BillDate: in.BillDate, DueDate: in.DueDate, Description: in.Description}
	for _, m := range matched {
		bill.Lines = append(bill.Lines, payables.BillLineInput{AccountID: grniID, Description: m.description, Amount: m.amount, VATAmount: m.vat})
	}
	for _, e := range in.ExtraLines {
		bill.Lines = append(bill.Lines, payables.BillLineInput{AccountID: e.AccountID, Description: trimmed(e.Description), Amount: e.Amount, VATAmount: e.VATAmount, DepartmentID: e.DepartmentID})
	}
	return bill
}

// stockAccounts are the accounts of the clearing.
type stockAccounts struct{ grni, ppv int64 }

// accountsOfPurchasing reads the accounts of goods received not invoiced and of the purchase price variance, and refuses a line that is not stock on one of the stock accounts (D14).
func (s *Service) accountsOfPurchasing(ctx context.Context, p auth.Principal, propertyID int64, extra []ExtraBillLine) (stockAccounts, error) {
	q := s.q(ctx)
	grni, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: "INV_GRNI"})
	if err != nil {
		return stockAccounts{}, apperr.Conflict("GRNI_ACCOUNT_MISSING", "the account of goods received not invoiced (key INV_GRNI) is missing in this property")
	}
	ppv, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: "INV_PPV"})
	if err != nil {
		return stockAccounts{}, apperr.Conflict("PPV_ACCOUNT_MISSING", "the account of the purchase price variance (key INV_PPV) is missing in this property")
	}
	control, err := s.ControlAccountIDs(ctx, p.TenantID, propertyID)
	if err != nil {
		return stockAccounts{}, err
	}
	for i, e := range extra {
		if slices.Contains(control, e.AccountID) || e.AccountID == grni.ID {
			return stockAccounts{}, invalid("bill", fieldErr(fmt.Sprintf("extra_lines[%d].account_id", i), "CONTROL_ACCOUNT", "a line that is not stock is not charged to the stock accounts: they are posted by the receipts (D14)"))
		}
	}
	return stockAccounts{grni: grni.ID, ppv: ppv.ID}, nil
}

// matchAndPost is the part of a confirmation that runs under the locks: the match is worked out on what the locks show, the bill is posted by the payables, the matches are written and the differences cleared. The
// payables are called before the cost is known because the VAT treatment of the date is read there (the locks of 5.1 do not let the stock read it first); the treatment comes back on the lines of the bill.
func (s *Service) matchAndPost(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, stock *Stock, in ConfirmBillInput, approval *iam.Approval, key string) (ConfirmResult, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return ConfirmResult{}, err
	}
	cfg, err := s.settingsOrDefault(ctx, p, propertyID)
	if err != nil {
		return ConfirmResult{}, err
	}
	if err := s.checkExtraLines(in.ExtraLines, prop.CurrencyDecimals); err != nil {
		return ConfirmResult{}, err
	}
	plans, err := s.planBill(ctx, p, propertyID, in, prop.CurrencyDecimals, cfg)
	if err != nil {
		return ConfirmResult{}, err
	}
	acc, err := s.accountsOfPurchasing(ctx, p, propertyID, in.ExtraLines)
	if err != nil {
		return ConfirmResult{}, err
	}
	matched := make([]billedLine, 0, len(plans))
	for _, pl := range plans {
		matched = append(matched, billedLine{description: fmt.Sprintf("%s %s x %s (%s)", pl.rl.ItemCode, pl.in.Qty.String(), pl.rl.UnitCode, pl.rl.ReceiptNumber), amount: pl.amount, vat: pl.in.VATAmount})
	}
	posted, err := s.pay.PostBillWith(ctx, propertyID, billInputOf(in, acc.grni, matched), key, payables.BillOptions{AllowInventory: true, Day: &bd})
	if err != nil {
		return ConfirmResult{}, err
	}
	treatments := make([]string, len(plans))
	for i := range plans {
		if i < len(posted.Lines) {
			treatments[i] = posted.Lines[i].VATTreatment
		}
	}
	applyTreatment(plans, treatments)
	if err := s.splitVariance(ctx, p, propertyID, cfg.CostingMethod, plans, prop.CurrencyDecimals); err != nil {
		return ConfirmResult{}, err
	}
	prev := summarize(plans, in.ExtraLines, "", cfg)
	if prev.ApprovalRequired && approval == nil {
		return ConfirmResult{}, apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "the invoice differs from the receipts by more than the tolerance: it needs the approval of someone who holds inventory.approve").
			WithContext("kind", string(iam.KindInvBillVariance)).WithContext("amount", prev.ApprovalAmount.String()).WithContext("tolerance", prev.ToleranceAmount.String())
	}
	if err := s.writeMatches(ctx, p, propertyID, stock, plans, posted.ID, acc); err != nil {
		return ConfirmResult{}, err
	}
	matches, err := s.matchesOfBill(ctx, p.TenantID, propertyID, posted.ID)
	if err != nil {
		return ConfirmResult{}, err
	}
	data := map[string]any{"bill_number": posted.Number, "supplier_id": in.SupplierID, "invoice": in.SupplierInvoiceNumber, "lines": len(plans), "variance": prev.Variance, "inventory_part": prev.InventoryPart, "ppv_part": prev.PPVPart}
	if approval != nil {
		data = approval.AuditFields(data)
	}
	if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.bill_confirmed", "supplier_bill", posted.ID, posted.Number, nil, data)); err != nil {
		return ConfirmResult{}, err
	}
	return ConfirmResult{Bill: posted, Matches: matches, Preview: prev}, nil
}

// writeMatches records the match of each line of a bill with its receipt line and clears the difference of cost: the part of it that belongs to goods still on hand goes into their value, the rest into the purchase
// price variance. The stock is locked already; nothing here takes a lock.
func (s *Service) writeMatches(ctx context.Context, p auth.Principal, propertyID int64, stock *Stock, plans []matchPlan, billID int64, acc stockAccounts) error {
	q := s.q(ctx)
	touched := map[int64]bool{}
	for i, pl := range plans {
		line := i + 1
		pair := Pair{pl.rl.WarehouseID, pl.rl.ItemID}
		var moves []int64
		invPart, ppvPart := pl.invPart, pl.ppvPart
		if !invPart.IsZero() {
			adj, err := stock.Adjust(ctx, AdjustArgs{Pair: pair, Value: invPart, Ref: Ref{Type: "INV_BILL", ID: billID, Line: line*10 + 1}, Counter: &Counter{AccountID: acc.grni}, ForMovementID: pl.rl.MovementID})
			var ae *apperr.Error
			switch {
			case err == nil:
				moves = append(moves, adj.MovementID)
			case errors.As(err, &ae) && ae.Code == "COST_ADJUST_NOT_APPLICABLE":
				invPart, ppvPart = decimal.Zero, pl.variance // nothing of the receipt is on hand any more: the whole difference is the price variance
			default:
				return err
			}
		}
		if !ppvPart.IsZero() {
			v, err := stock.Variance(ctx, VarianceArgs{Pair: pair, Amount: ppvPart, Ref: Ref{Type: "INV_BILL", ID: billID, Line: line*10 + 2}, Variance: acc.ppv, Clearing: acc.grni})
			if err != nil {
				return err
			}
			moves = append(moves, v.MovementID)
		}
		mid, err := q.InsertMatch(ctx, inventorydb.InsertMatchParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: pl.in.ReceiptID, ReceiptLineNo: int32(pl.in.ReceiptLineNo), //nolint:gosec // G115: a line number
			BillID: billID, BillLineNo: int32(line), QtyStock: pl.billedStock, BillCost: pl.cost, ReceiptValue: pl.receiptValue, Variance: pl.variance, InventoryPart: invPart, PpvPart: ppvPart, ActorID: p.ActorID()}) //nolint:gosec // G115: at most 100
		if err != nil {
			return err
		}
		if err := q.SetMatchClearing(ctx, inventorydb.SetMatchClearingParams{PropertyID: propertyID, ID: mid, Ids: moves}); err != nil {
			return err
		}
		if err := q.AddReceiptLineBilled(ctx, inventorydb.AddReceiptLineBilledParams{PropertyID: propertyID, ReceiptID: pl.in.ReceiptID, LineNo: int32(pl.in.ReceiptLineNo), Qty: pl.billedStock, Value: pl.receiptValue}); err != nil { //nolint:gosec // G115: a line number
			return err
		}
		touched[pl.in.ReceiptID] = true
	}
	for rid := range touched {
		if err := s.refreshDraft(ctx, p, propertyID, rid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) matchesOfBill(ctx context.Context, tenantID, propertyID, billID int64) ([]MatchOfBill, error) {
	rows, err := s.q(ctx).ListMatchesOfBill(ctx, inventorydb.ListMatchesOfBillParams{TenantID: tenantID, PropertyID: propertyID, BillID: billID})
	if err != nil {
		return nil, err
	}
	out := make([]MatchOfBill, 0, len(rows))
	for _, m := range rows {
		out = append(out, MatchOfBill{ID: m.ID, ReceiptID: m.ReceiptID, ReceiptLineNo: int(m.ReceiptLineNo), BillID: m.BillID, BillLineNo: int(m.BillLineNo), QtyStock: m.QtyStock, BillCost: m.BillCost,
			ReceiptValue: m.ReceiptValue, Variance: m.Variance, InventoryPart: m.InventoryPart, PPVPart: m.PpvPart, CreditNoteID: m.CreditNoteID, ReturnID: m.ReturnID, CreatedAt: m.CreatedAt})
	}
	return out, nil
}

// refreshDraft sets the state of the draft of a receipt from what is left to bill of its lines.
func (s *Service) refreshDraft(ctx context.Context, p auth.Principal, propertyID, receiptID int64) error {
	q := s.q(ctx)
	d, err := q.GetDraftOfReceipt(ctx, inventorydb.GetDraftOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: receiptID})
	if err != nil || d.Status == DraftDiscarded {
		return err
	}
	lines, err := q.ListReceiptLines(ctx, inventorydb.ListReceiptLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptIds: []int64{receiptID}})
	if err != nil {
		return err
	}
	left, billed := false, false
	for _, l := range lines {
		if bq, _ := billable(l); bq.IsPositive() {
			left = true
		}
		if l.QtyBilledStock.IsPositive() {
			billed = true
		}
	}
	to := DraftOpen
	switch {
	case !left:
		to = DraftBilled
	case billed:
		to = DraftPartial
	}
	if to == d.Status {
		return nil
	}
	return q.SetDraftStatus(ctx, inventorydb.SetDraftStatusParams{PropertyID: propertyID, ID: d.ID, Status: to})
}

// UnmatchInput voids the bill that a draft was confirmed into, with its reason and the approval the payables ask of a void.
type UnmatchInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// UnmatchBill takes a bill back from the receipts it was matched to (inventory.bill and payables.post, 5.3.7): the clearing of its differences is reversed, the bill is voided by the payables (it needs the approval of
// a void and no payment), and the draft lines are open again. Nothing is edited: every step is a reversing row.
func (s *Service) UnmatchBill(ctx context.Context, propertyID, billID int64, in UnmatchInput) (BillDraftBrief, error) {
	p, err := s.canBill(ctx, propertyID)
	if err != nil {
		return BillDraftBrief{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPayablesPost); err != nil {
		return BillDraftBrief{}, err
	}
	if s.pay == nil {
		return BillDraftBrief{}, apperr.Internal(errors.New("inventory: the payables are not wired"))
	}
	var receiptID int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		ms, err := q.ListMatchesOfBill(ctx, inventorydb.ListMatchesOfBillParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: billID})
		if err != nil {
			return err
		}
		if len(ms) == 0 {
			return apperr.NotFound("BILL_NOT_MATCHED", "the bill is not matched to goods received")
		}
		recIDs := make([]int64, 0, len(ms))
		for _, m := range ms {
			recIDs = append(recIDs, m.ReceiptID)
		}
		slices.Sort(recIDs)
		recIDs = slices.Compact(recIDs)
		var draftIDs []int64
		for _, rid := range recIDs {
			d, err := q.GetDraftOfReceipt(ctx, inventorydb.GetDraftOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: rid})
			if err != nil {
				return err
			}
			draftIDs = append(draftIDs, d.ID)
		}
		if err := db.LockRows(ctx, db.StockBillDrafts, db.ForUpdate, propertyID, draftIDs); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.StockReceipts, db.ForUpdate, propertyID, recIDs); err != nil {
			return err
		}
		ms, err = q.ListMatchesOfBill(ctx, inventorydb.ListMatchesOfBillParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: billID}) // again, under the locks
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(ms))
		for _, m := range ms {
			if m.CreditNoteID != nil {
				return apperr.Conflict("BILL_HAS_CREDIT_NOTES", "a credit note is linked to a return of these goods: void the credit note first")
			}
			pairs = append(pairs, Pair{m.WarehouseID, m.ItemID})
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		now := s.clock.Now()
		for _, m := range ms {
			for k, mid := range m.ClearingMovementIds {
				if _, err := stock.ReverseAdjust(ctx, mid, m.ReceiptMovementID, Ref{Type: "INV_BILL", ID: billID, Line: int(m.BillLineNo)*10 + 3 + k}); err != nil {
					var ae *apperr.Error
					if errors.As(err, &ae) && ae.Code == "MOVEMENT_NOT_REVERSIBLE" {
						return apperr.Conflict("BILL_NOT_UNMATCHABLE", "the difference of cost of this bill cannot be taken back: "+ae.Message).WithContext("bill_id", billID)
					}
					return err
				}
			}
			if err := q.SetMatchUnmatched(ctx, inventorydb.SetMatchUnmatchedParams{PropertyID: propertyID, ID: m.ID, Now: &now, ActorID: p.ActorID()}); err != nil {
				return err
			}
			if err := q.AddReceiptLineBilled(ctx, inventorydb.AddReceiptLineBilledParams{PropertyID: propertyID, ReceiptID: m.ReceiptID, LineNo: m.ReceiptLineNo, Qty: m.QtyStock.Neg(), Value: m.ReceiptValue.Neg()}); err != nil {
				return err
			}
			receiptID = m.ReceiptID
		}
		if _, err := s.pay.VoidBillOfInventory(ctx, propertyID, billID, payables.VoidInput{Reason: in.Reason, Approval: in.Approval}, bd); err != nil {
			return err
		}
		for _, rid := range recIDs {
			if err := s.refreshDraft(ctx, p, propertyID, rid); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.bill_unmatched", "supplier_bill", billID, fmt.Sprint(billID), nil, map[string]any{"reason": trimmed(in.Reason), "receipts": recIDs}))
	})
	if err != nil {
		return BillDraftBrief{}, err
	}
	d, err := s.q(ctx).GetDraftOfReceipt(ctx, inventorydb.GetDraftOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: receiptID})
	if err != nil {
		return BillDraftBrief{}, err
	}
	full, err := s.GetBillDraft(ctx, propertyID, d.ID)
	return full.BillDraftBrief, err
}
