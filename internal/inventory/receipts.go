package inventory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The goods receipt (GR, 35-inventory.md 5.3.3): goods come into a warehouse against an order, at the price of the order (D16), or without an order (the market purchase, D8) at a typed price, up to the limit of
// the property. One RECEIPT movement for each line, booked Dr inventory / Cr goods received not invoiced; the draft bill of the receipt is made in the same transaction.

// Receipt states.
const (
	ReceiptPosted   = "POSTED"
	ReceiptReversed = "REVERSED"
)

// ReceiptLineInput is a line of a receipt. Against an order a line names the order line and the quantity received in its unit; the price is the order's and is not offered. Without an order the line names the item,
// the unit and the price.
type ReceiptLineInput struct {
	POLineNo  *int             `json:"po_line_no"`
	ItemID    int64            `json:"item_id"`
	UnitID    int64            `json:"unit_id"`
	Qty       decimal.Decimal  `json:"qty"`
	UnitPrice *decimal.Decimal `json:"unit_price"`
}

// ReceiptBillInput is the invoice that "Receive and bill" confirms with the receipt: its number, its date and the VAT of each line (by the position of the receipt line).
type ReceiptBillInput struct {
	SupplierInvoiceNumber string            `json:"supplier_invoice_number"`
	BillDate              civil.Date        `json:"bill_date"`
	DueDate               *civil.Date       `json:"due_date"`
	Description           string            `json:"description"`
	VATAmounts            []decimal.Decimal `json:"vat_amounts"`
}

// ReceiptInput posts a receipt. ClientUUID makes a repeated request answer the first.
type ReceiptInput struct {
	ClientUUID       string             `json:"client_uuid"`
	SupplierID       int64              `json:"supplier_id"`
	POID             *int64             `json:"po_id"`
	WarehouseID      int64              `json:"warehouse_id"`
	DeliveryNote     *string            `json:"delivery_note"`
	DeliveryNoteDate *civil.Date        `json:"delivery_note_date"`
	Note             *string            `json:"note"`
	Lines            []ReceiptLineInput `json:"lines"`
	Bill             *ReceiptBillInput  `json:"bill"`
}

// ReceiptLine is a line of a posted receipt, with what has been billed and returned of it.
type ReceiptLine struct {
	LineNo        int             `json:"line_no"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	POLineNo      *int            `json:"po_line_no"`
	UnitID        int64           `json:"unit_id"`
	UnitCode      string          `json:"unit_code"`
	Qty           decimal.Decimal `json:"qty"`
	QtyStock      decimal.Decimal `json:"qty_stock"`
	StockUnitCode string          `json:"stock_unit_code"`
	UnitPrice     decimal.Decimal `json:"unit_price"`
	Value         decimal.Decimal `json:"value"`
	QtyBilled     decimal.Decimal `json:"qty_billed_stock"`
	QtyReturned   decimal.Decimal `json:"qty_returned_stock"`
	Billable      decimal.Decimal `json:"billable_qty_stock"`
	BillableQty   decimal.Decimal `json:"billable_qty"`
	BillableValue decimal.Decimal `json:"billable_value"`
}

// ReceiptBrief is a row of the list of receipts.
type ReceiptBrief struct {
	ID            int64           `json:"id"`
	Number        string          `json:"receipt_number"`
	Status        string          `json:"status"`
	SupplierID    int64           `json:"supplier_id"`
	SupplierName  string          `json:"supplier_name"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseName string          `json:"warehouse_name"`
	POID          *int64          `json:"po_id"`
	PONumber      *string         `json:"po_number"`
	ReceivedOn    civil.Date      `json:"received_on"`
	DeliveryNote  *string         `json:"delivery_note"`
	TotalValue    decimal.Decimal `json:"total_value"`
	LineCount     int             `json:"line_count"`
	DraftID       *int64          `json:"draft_id"`
	DraftStatus   *string         `json:"draft_status"`
}

// Receipt is a posted receipt with its lines, its matches with bills and its returns.
type Receipt struct {
	ReceiptBrief
	SupplierCode     string          `json:"supplier_code"`
	WarehouseCode    string          `json:"warehouse_code"`
	DeliveryNoteDate *civil.Date     `json:"delivery_note_date"`
	Note             *string         `json:"note"`
	ReversedAt       *time.Time      `json:"reversed_at"`
	ReverseReason    *string         `json:"reverse_reason"`
	CreatedAt        time.Time       `json:"created_at"`
	Lines            []ReceiptLine   `json:"lines"`
	Matches          []MatchOfBill   `json:"matches"`
	Returns          []ReturnBrief   `json:"returns"`
	Reversible       bool            `json:"reversible"`
	BillableValue    decimal.Decimal `json:"billable_value"`
}

// ReceiptFilter narrows the list of receipts.
type ReceiptFilter struct {
	Status     string
	SupplierID *int64
	POID       *int64
	Q          string
	Limit      int
}

func (s *Service) canReceive(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryReceive)
}

func toReceiptBrief(r inventorydb.ListReceiptsRow) ReceiptBrief {
	return ReceiptBrief{ID: r.ID, Number: r.ReceiptNumber, Status: r.Status, SupplierID: r.SupplierID, SupplierName: r.SupplierName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName,
		POID: r.PoID, PONumber: r.PoNumber, ReceivedOn: r.ReceivedOn, DeliveryNote: r.DeliveryNote, TotalValue: r.TotalValue, LineCount: int(r.LineCount), DraftID: r.DraftID, DraftStatus: r.DraftStatus}
}

// ListReceipts lists the receipts, newest first.
func (s *Service) ListReceipts(ctx context.Context, propertyID int64, f ReceiptFilter) ([]ReceiptBrief, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListReceipts(ctx, inventorydb.ListReceiptsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), SupplierID: f.SupplierID, PoID: f.POID,
		Q: optional(&f.Q), RowLimit: limitOf(f.Limit)})
	if err != nil {
		return nil, err
	}
	out := make([]ReceiptBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, toReceiptBrief(r))
	}
	return out, nil
}

// GetReceipt answers one receipt.
func (s *Service) GetReceipt(ctx context.Context, propertyID, id int64) (Receipt, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return Receipt{}, err
	}
	return s.receipt(ctx, p, propertyID, id)
}

// billable is what is left of a receipt line to bill: the quantity, never below zero, and its value.
func billable(l inventorydb.ListReceiptLinesRow) (qty, value decimal.Decimal) {
	qty = l.QtyStock.Sub(l.QtyBilledStock).Sub(l.QtyReturnedStock)
	if !qty.IsPositive() {
		return decimal.Zero, decimal.Zero
	}
	return qty, l.Value.Sub(l.ValueBilled).Sub(l.ValueReturned)
}

func (s *Service) receipt(ctx context.Context, p auth.Principal, propertyID, id int64) (Receipt, error) {
	q := s.q(ctx)
	r, err := q.GetReceipt(ctx, inventorydb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Receipt{}, orNotFound(err, notFound("RECEIPT_NOT_FOUND", "receipt"))
	}
	out := Receipt{ReceiptBrief: ReceiptBrief{ID: r.ID, Number: r.ReceiptNumber, Status: r.Status, SupplierID: r.SupplierID, SupplierName: r.SupplierName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName,
		POID: r.PoID, PONumber: r.PoNumber, ReceivedOn: r.ReceivedOn, DeliveryNote: r.DeliveryNote, TotalValue: r.TotalValue, DraftID: r.DraftID, DraftStatus: r.DraftStatus},
		SupplierCode: r.SupplierCode, WarehouseCode: r.WarehouseCode, DeliveryNoteDate: r.DeliveryNoteDate, Note: r.Note, ReversedAt: r.ReversedAt, ReverseReason: r.ReverseReason, CreatedAt: r.CreatedAt}
	lines, err := q.ListReceiptLines(ctx, inventorydb.ListReceiptLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptIds: []int64{id}})
	if err != nil {
		return Receipt{}, err
	}
	out.Lines = make([]ReceiptLine, 0, len(lines))
	untouched := true
	for _, l := range lines {
		bq, bv := billable(l)
		rl := ReceiptLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitID: l.PurchaseUnitID, UnitCode: l.UnitCode, Qty: l.QtyPurchase, QtyStock: l.QtyStock,
			StockUnitCode: l.StockUnitCode, UnitPrice: l.UnitPrice, Value: l.Value, QtyBilled: l.QtyBilledStock, QtyReturned: l.QtyReturnedStock, Billable: bq, BillableQty: purchaseQty(bq, l.StockNum, l.StockDen), BillableValue: bv}
		if l.PoLineNo != nil {
			n := int(*l.PoLineNo)
			rl.POLineNo = &n
		}
		if l.QtyBilledStock.IsPositive() || l.QtyReturnedStock.IsPositive() {
			untouched = false
		}
		out.BillableValue = out.BillableValue.Add(bv)
		out.Lines = append(out.Lines, rl)
	}
	out.LineCount = len(out.Lines)
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Receipt{}, err
	}
	out.Reversible = r.Status == ReceiptPosted && untouched && r.ReceivedOn.Equal(day.BusinessDate)
	out.Matches, err = s.matchesOfReceipt(ctx, p, propertyID, id)
	if err != nil {
		return Receipt{}, err
	}
	rets, err := q.ListReturns(ctx, inventorydb.ListReturnsParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: &id, RowLimit: maxPageRow})
	if err != nil {
		return Receipt{}, err
	}
	out.Returns = make([]ReturnBrief, 0, len(rets))
	for _, x := range rets {
		out.Returns = append(out.Returns, toReturnBrief(x))
	}
	return out, nil
}

type receiptLinePlan struct {
	item      inventorydb.GetItemRow
	poLineNo  *int32
	unitID    int64
	qty       decimal.Decimal
	stock     decimal.Decimal
	unitPrice decimal.Decimal
	value     decimal.Decimal
	unit      inventorydb.ListItemUnitsOfRow
}

// PostReceipt posts a goods receipt (inventory.receive). In one transaction: the order (if any), the warehouse and the balances are locked in the order of 5.1, one RECEIPT movement is booked for each line (settling
// the stock sold below zero first), the quantities received of the order are raised, the draft bill is made. "Receive and bill" (a bill block, inventory.bill and payables.post) confirms the draft in the same request.
func (s *Service) PostReceipt(ctx context.Context, propertyID int64, in ReceiptInput) (Receipt, error) {
	p, err := s.canReceive(ctx, propertyID)
	if err != nil {
		return Receipt{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Receipt{}, invalid("receipt", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	if len(in.Lines) == 0 || len(in.Lines) > maxDocLines {
		return Receipt{}, invalid("receipt", fieldErr("lines", "OUT_OF_RANGE", "1 to "+itoa(maxDocLines)+" lines"))
	}
	if in.DeliveryNote != nil && len([]rune(trimmed(*in.DeliveryNote))) > 60 {
		return Receipt{}, invalid("receipt", fieldErr("delivery_note", "INVALID_VALUE", "60 characters at most"))
	}
	if in.Note != nil && len([]rune(trimmed(*in.Note))) > 300 {
		return Receipt{}, invalid("receipt", fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	if in.Bill != nil {
		if err := s.authz.Require(ctx, propertyID, auth.PermInventoryBill); err != nil {
			return Receipt{}, err
		}
		if err := s.authz.Require(ctx, propertyID, auth.PermPayablesPost); err != nil {
			return Receipt{}, err
		}
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Receipt{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		var po *inventorydb.GetPORow
		if in.POID != nil {
			r, err := s.loadPOForChange(ctx, p, propertyID, *in.POID)
			if err != nil {
				return err
			}
			po = &r
		}
		if prev, err := q.GetReceiptByClientUUID(ctx, inventorydb.GetReceiptByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		cfg, err := s.settingsOrDefault(ctx, p, propertyID)
		if err != nil {
			return err
		}
		supplierID, warehouseID := in.SupplierID, in.WarehouseID
		var poLines []inventorydb.ListPOLinesRow
		if po != nil {
			if po.Status != POStatusOrdered && po.Status != POStatusPartial {
				return apperr.Conflict("PO_NOT_RECEIVABLE", "the order is "+po.Status+": goods are received against an order that is ordered or partly received").WithContext("status", po.Status)
			}
			supplierID, warehouseID = po.SupplierID, po.WarehouseID
			if poLines, err = q.ListPOLines(ctx, inventorydb.ListPOLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: po.ID}); err != nil {
				return err
			}
		}
		if err := s.checkOrderHeader(ctx, p, propertyID, supplierID, warehouseID, nil, nil); err != nil {
			return err
		}
		plans, err := s.planReceiptLines(ctx, p, propertyID, in, po != nil, poLines, cfg, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		total := decimal.Zero
		for _, l := range plans {
			total = total.Add(l.value)
		}
		if po == nil {
			switch {
			case cfg.DirectReceiptLimit == nil:
				return apperr.Conflict("DIRECT_RECEIPT_NOT_ALLOWED", "this property receives only against an order: write an order, or set a limit for receipts without one")
			case cfg.DirectReceiptLimit.IsPositive() && total.GreaterThan(*cfg.DirectReceiptLimit):
				return apperr.Conflict("DIRECT_RECEIPT_LIMIT", "a receipt without an order is at most "+cfg.DirectReceiptLimit.String()+": this one is "+total.String()).
					WithContext("limit", cfg.DirectReceiptLimit.String()).WithContext("total", total.String())
			}
		}
		grni, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: "INV_GRNI"})
		if err != nil {
			return apperr.Conflict("GRNI_ACCOUNT_MISSING", "the account of goods received not invoiced (key INV_GRNI) is missing in this property")
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(plans))
		for _, l := range plans {
			pairs = append(pairs, Pair{warehouseID, l.item.ID})
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{}) // after the order, in the order of 5.1; the document number comes last
		if err != nil {
			return err
		}
		// "Receive and bill": the bill is posted by the payables now, before the number of the receipt is taken (the locks of 5.1: the payables take 46, 47, 49 and 50, the number is the last)
		var posted payables.Bill
		var acc stockAccounts
		if in.Bill != nil {
			if s.pay == nil {
				return apperr.Internal(errors.New("inventory: the payables are not wired"))
			}
			if len(in.Bill.VATAmounts) != 0 && len(in.Bill.VATAmounts) != len(plans) {
				return invalid("receipt", fieldErr("bill.vat_amounts", "INVALID_VALUE", "one VAT amount for each line of the receipt, or none"))
			}
			if acc, err = s.accountsOfPurchasing(ctx, p, propertyID, nil); err != nil {
				return err
			}
			matched := make([]billedLine, 0, len(plans))
			for i, l := range plans {
				vat := decimal.Zero
				if len(in.Bill.VATAmounts) > 0 {
					vat = in.Bill.VATAmounts[i]
				}
				matched = append(matched, billedLine{description: fmt.Sprintf("%s %s x %s", l.item.Code, l.qty.String(), l.unit.UnitCode), amount: l.value, vat: vat})
			}
			bi := ConfirmBillInput{SupplierID: supplierID, SupplierInvoiceNumber: in.Bill.SupplierInvoiceNumber, BillDate: in.Bill.BillDate, DueDate: in.Bill.DueDate, Description: in.Bill.Description}
			if posted, err = s.pay.PostBillWith(ctx, propertyID, billInputOf(bi, acc.grni, matched), "recv-"+in.ClientUUID, payables.BillOptions{AllowInventory: true, Day: &bd}); err != nil {
				return err
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvGR)
		if err != nil {
			return err
		}
		var poID *int64
		if po != nil {
			poID = &po.ID
		}
		id, err = q.InsertReceipt(ctx, inventorydb.InsertReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptNumber: number, ClientUuid: key, SupplierID: supplierID, PoID: poID, WarehouseID: warehouseID,
			ReceivedOn: bd, DeliveryNote: optional(in.DeliveryNote), DeliveryNoteDate: in.DeliveryNoteDate, TotalValue: total, Note: optional(in.Note), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		counter := &Counter{AccountID: grni.ID}
		for i, l := range plans {
			posted, err := stock.In(ctx, InArgs{Pair: Pair{warehouseID, l.item.ID}, Kind: KindReceipt, Qty: l.stock, Value: l.value, Ref: Ref{Type: "INV_RECEIPT", ID: id, Line: i + 1}, Counter: counter})
			if err != nil {
				return err
			}
			if err := q.InsertReceiptLine(ctx, inventorydb.InsertReceiptLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: id, LineNo: int32(i + 1), ItemID: l.item.ID, //nolint:gosec // G115: at most 200
				PoLineNo: l.poLineNo, PurchaseUnitID: l.unitID, QtyPurchase: l.qty, QtyStock: l.stock, UnitPrice: l.unitPrice, Value: l.value, MovementID: posted.MovementID}); err != nil {
				return err
			}
			if l.poLineNo != nil {
				if err := q.AddPOLineReceived(ctx, inventorydb.AddPOLineReceivedParams{PropertyID: propertyID, PoID: po.ID, LineNo: *l.poLineNo, Qty: l.stock}); err != nil {
					return err
				}
			}
			if err := q.UpsertSupplierItem(ctx, inventorydb.UpsertSupplierItemParams{TenantID: p.TenantID, PropertyID: propertyID, SupplierID: supplierID, ItemID: l.item.ID, PurchaseUnitID: l.unitID, LastPrice: l.unitPrice, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		if po != nil {
			fresh, err := q.ListPOLines(ctx, inventorydb.ListPOLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: po.ID})
			if err != nil {
				return err
			}
			if to := orderStatusAfter(fresh, po.Status); to != po.Status {
				if err := q.SetPOStatus(ctx, inventorydb.SetPOStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: po.ID, Status: to, ActorID: p.ActorID()}); err != nil {
					return err
				}
			}
		}
		draftID, err := q.InsertDraft(ctx, inventorydb.InsertDraftParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: id, SupplierID: supplierID})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.receipt_posted", "inv_receipt", id, number, nil,
			map[string]any{"number": number, "supplier_id": supplierID, "warehouse_id": warehouseID, "po_id": poID, "lines": len(plans), "total_value": total, "direct": po == nil, "draft_id": draftID})); err != nil {
			return err
		}
		if in.Bill == nil {
			return nil
		}
		// the match of the bill with the receipt that was just posted: every line at the price it was received at
		lines, err := q.ListReceiptLines(ctx, inventorydb.ListReceiptLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptIds: []int64{id}})
		if err != nil {
			return err
		}
		bi := ConfirmBillInput{SupplierID: supplierID, SupplierInvoiceNumber: in.Bill.SupplierInvoiceNumber, BillDate: in.Bill.BillDate, DueDate: in.Bill.DueDate, Description: in.Bill.Description}
		for i, l := range lines {
			vat := decimal.Zero
			if len(in.Bill.VATAmounts) > 0 {
				vat = in.Bill.VATAmounts[i]
			}
			bi.Lines = append(bi.Lines, ConfirmLineInput{ReceiptID: id, ReceiptLineNo: int(l.LineNo), Qty: l.QtyPurchase, UnitPrice: l.UnitPrice, VATAmount: vat})
		}
		mplans, err := s.planBill(ctx, p, propertyID, bi, prop.CurrencyDecimals, cfg)
		if err != nil {
			return err
		}
		treatments := make([]string, len(mplans))
		for i := range mplans {
			if i < len(posted.Lines) {
				treatments[i] = posted.Lines[i].VATTreatment
			}
		}
		applyTreatment(mplans, treatments)
		if err := s.splitVariance(ctx, p, propertyID, cfg.CostingMethod, mplans, prop.CurrencyDecimals); err != nil {
			return err
		}
		if err := s.writeMatches(ctx, p, propertyID, stock, mplans, posted.ID, acc); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.bill_confirmed", "supplier_bill", posted.ID, posted.Number, nil,
			map[string]any{"bill_number": posted.Number, "supplier_id": supplierID, "invoice": in.Bill.SupplierInvoiceNumber, "lines": len(mplans), "receipt_id": id, "receive_and_bill": true}))
	})
	if err != nil {
		return Receipt{}, err
	}
	return s.receipt(ctx, p, propertyID, id)
}

// planReceiptLines validates the lines of a receipt against the order (or against what a receipt without an order needs) and works out the quantities and values. Nothing is written.
func (s *Service) planReceiptLines(ctx context.Context, p auth.Principal, propertyID int64, in ReceiptInput, hasPO bool, poLines []inventorydb.ListPOLinesRow, cfg Settings, decimals int32) ([]receiptLinePlan, error) {
	q := s.q(ctx)
	var f []apperr.FieldError
	itemIDs := make([]int64, 0, len(in.Lines))
	byPO := map[int32]inventorydb.ListPOLinesRow{}
	for _, l := range poLines {
		byPO[l.LineNo] = l
	}
	for _, l := range in.Lines {
		if hasPO && l.POLineNo != nil {
			if pl, ok := byPO[int32(*l.POLineNo)]; ok { //nolint:gosec // G115: a line number
				itemIDs = append(itemIDs, pl.ItemID)
			}
		} else {
			itemIDs = append(itemIDs, l.ItemID)
		}
	}
	units := map[[2]int64]inventorydb.ListItemUnitsOfRow{}
	rows, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: itemIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		units[[2]int64{r.ItemID, r.UnitID}] = r
	}
	newlyReceived := map[int32]decimal.Decimal{}
	seen := map[int64]bool{}
	out := make([]receiptLinePlan, 0, len(in.Lines))
	for i, l := range in.Lines {
		at := fmt.Sprintf("lines[%d]", i)
		var pl inventorydb.ListPOLinesRow
		itemID, unitID := l.ItemID, l.UnitID
		var price decimal.Decimal
		var poNo *int32
		if hasPO {
			if l.POLineNo == nil {
				f = append(f, fieldErr(at+".po_line_no", "REQUIRED", "the line of the order that is received"))
				continue
			}
			no := int32(*l.POLineNo) //nolint:gosec // G115: a line number
			found, ok := byPO[no]
			if !ok {
				f = append(f, fieldErr(at+".po_line_no", "NOT_FOUND", "no such line on the order"))
				continue
			}
			pl, poNo = found, &no
			itemID, unitID, price = pl.ItemID, pl.PurchaseUnitID, pl.UnitPrice // the price is the order's: the receipt does not offer one (D16)
		} else {
			if l.UnitPrice == nil || l.UnitPrice.IsNegative() || l.UnitPrice.GreaterThan(decimal.NewFromInt(1_000_000_000_000)) || !l.UnitPrice.Equal(l.UnitPrice.Round(decimals)) {
				f = append(f, fieldErr(at+".unit_price", "INVALID_VALUE", fmt.Sprintf("the price of one unit, zero or more, %d decimals at most", decimals)))
				continue
			}
			price = *l.UnitPrice
		}
		it, err := q.GetItem(ctx, inventorydb.GetItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			f = append(f, fieldErr(at+".item_id", "NOT_FOUND", "no such item in this property"))
			continue
		case err != nil:
			return nil, err
		case !it.IsActive:
			f = append(f, fieldErr(at+".item_id", "INACTIVE", "the item is switched off"))
			continue
		case seen[itemID]:
			f = append(f, fieldErr(at+".item_id", "DUPLICATE", "an item is on the receipt once"))
			continue
		}
		seen[itemID] = true
		u, ok := units[[2]int64{itemID, unitID}]
		if !ok {
			f = append(f, fieldErr(at+".unit_id", "NOT_FOUND", "not a unit of the item"))
			continue
		}
		if !l.Qty.IsPositive() || l.Qty.GreaterThan(decimal.NewFromInt(1_000_000_000)) || !l.Qty.Equal(Round6(l.Qty)) {
			f = append(f, fieldErr(at+".qty", "INVALID_VALUE", "above zero, six decimals at most"))
			continue
		}
		stock := StockQty(l.Qty, u.StockNum, u.StockDen)
		if !stock.IsPositive() {
			f = append(f, fieldErr(at+".qty", "TOO_SMALL", "less than the smallest quantity of the stock unit"))
			continue
		}
		if hasPO {
			// the quantity never passes the over-receipt tolerance, over all the receipts of the order (the order is locked)
			limit := Round6(pl.QtyStock.Mul(decimal.NewFromInt(100).Add(cfg.OverReceiptPct)).Div(decimal.NewFromInt(100)))
			if pl.Closed {
				limit = pl.QtyReceivedStock
			}
			if total := pl.QtyReceivedStock.Add(newlyReceived[no(poNo)]).Add(stock); total.GreaterThan(limit) {
				return nil, apperr.Conflict("RECEIPT_EXCEEDS_ORDER", fmt.Sprintf("more than the order allows for %s: %s ordered, %s received, at most %s", pl.ItemCode, pl.QtyStock, pl.QtyReceivedStock, limit)).
					WithContext("item_id", pl.ItemID).WithContext("ordered", pl.QtyStock.String()).WithContext("received", pl.QtyReceivedStock.String()).WithContext("limit", limit.String())
			}
			newlyReceived[no(poNo)] = newlyReceived[no(poNo)].Add(stock)
		}
		out = append(out, receiptLinePlan{item: it, poLineNo: poNo, unitID: unitID, qty: l.Qty, stock: stock, unitPrice: price, value: l.Qty.Mul(price).Round(decimals), unit: u})
	}
	if len(f) > 0 {
		return nil, invalid("receipt", f...)
	}
	return out, nil
}

func no(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

// ReverseReceipt undoes a receipt on the day it was posted, while nothing was billed or returned of it and the goods are still on the shelf (inventory.receive, 5.3.5). The movements are reversed, the quantities of
// the order and the draft are restored. Later it is a return to the supplier.
func (s *Service) ReverseReceipt(ctx context.Context, propertyID, id int64, reason string) (Receipt, error) {
	p, err := s.canReceive(ctx, propertyID)
	if err != nil {
		return Receipt{}, err
	}
	reason = trimmed(reason)
	if n := len([]rune(reason)); n < 1 || n > 300 {
		return Receipt{}, invalid("receipt", fieldErr("reason", "REQUIRED", "why the receipt is reversed, 300 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := q.GetReceipt(ctx, inventorydb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("RECEIPT_NOT_FOUND", "receipt"))
		}
		// the order of locks: the draft, the receipt, the order, then the stock
		if cur.DraftID != nil {
			if err := db.LockRows(ctx, db.StockBillDrafts, db.ForUpdate, propertyID, []int64{*cur.DraftID}); err != nil {
				return err
			}
		}
		if err := db.LockRows(ctx, db.StockReceipts, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return err
		}
		if cur.PoID != nil {
			if err := db.LockRows(ctx, db.StockOrders, db.ForUpdate, propertyID, []int64{*cur.PoID}); err != nil {
				return err
			}
		}
		cur, err = q.GetReceipt(ctx, inventorydb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return err
		}
		notReversible := func(why string) error {
			return apperr.Conflict("RECEIPT_NOT_REVERSIBLE", "the receipt cannot be reversed: "+why+" (a return to the supplier does the same later)").WithContext("receipt_number", cur.ReceiptNumber)
		}
		switch {
		case cur.Status != ReceiptPosted:
			return notReversible("it is reversed already")
		case !cur.ReceivedOn.Equal(bd):
			return notReversible("it was received on " + cur.ReceivedOn.String() + ", and a receipt is reversed on its own day")
		}
		lines, err := q.ListReceiptLines(ctx, inventorydb.ListReceiptLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptIds: []int64{id}})
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, len(lines))
		for _, l := range lines {
			if l.QtyBilledStock.IsPositive() || l.QtyReturnedStock.IsPositive() {
				return notReversible("it has been billed or returned")
			}
			pairs = append(pairs, Pair{cur.WarehouseID, l.ItemID})
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		for _, l := range lines {
			if _, err := stock.Reverse(ctx, ReverseArgs{MovementID: l.MovementID, Kind: KindReceiptReversal, Ref: Ref{Type: "INV_RECEIPT", ID: id, Line: int(l.LineNo)}}); err != nil {
				var ae *apperr.Error
				if errors.As(err, &ae) && (ae.Code == "MOVEMENT_NOT_REVERSIBLE" || ae.Code == "NEGATIVE_STOCK_NOT_ALLOWED") {
					return notReversible("the goods of " + l.ItemCode + " are not all on the shelf any more")
				}
				return err
			}
			if cur.PoID != nil && l.PoLineNo != nil {
				if err := q.AddPOLineReceived(ctx, inventorydb.AddPOLineReceivedParams{PropertyID: propertyID, PoID: *cur.PoID, LineNo: *l.PoLineNo, Qty: l.QtyStock.Neg()}); err != nil {
					return err
				}
			}
		}
		if cur.PoID != nil {
			po, err := q.GetPO(ctx, inventorydb.GetPOParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *cur.PoID})
			if err != nil {
				return err
			}
			fresh, err := q.ListPOLines(ctx, inventorydb.ListPOLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: *cur.PoID})
			if err != nil {
				return err
			}
			if to := orderStatusAfter(fresh, po.Status); to != po.Status {
				if err := q.SetPOStatus(ctx, inventorydb.SetPOStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *cur.PoID, Status: to, ActorID: p.ActorID()}); err != nil {
					return err
				}
			}
		}
		if cur.DraftID != nil {
			if err := q.SetDraftStatus(ctx, inventorydb.SetDraftStatusParams{PropertyID: propertyID, ID: *cur.DraftID, Status: DraftDiscarded}); err != nil {
				return err
			}
		}
		now := s.clock.Now()
		if err := q.SetReceiptReversed(ctx, inventorydb.SetReceiptReversedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: &now, ActorID: p.ActorID(), Reason: &reason}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.receipt_reversed", "inv_receipt", id, cur.ReceiptNumber, map[string]any{"status": ReceiptPosted},
			map[string]any{"status": ReceiptReversed, "reason": reason, "total_value": cur.TotalValue}))
	})
	if err != nil {
		return Receipt{}, err
	}
	return s.receipt(ctx, p, propertyID, id)
}
