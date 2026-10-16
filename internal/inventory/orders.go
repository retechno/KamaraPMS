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
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The purchase order (PO, 35-inventory.md 5.3.2): lines in the purchase unit with a price for one such unit, net of VAT. It is made from an approved request (one order for each supplier) or, when the property lets it
// (po_requires_pr off), written directly. A PO has no journal: a commitment is not booked. It changes only by being sent, cancelled or closed short.

// States of an order.
const (
	POStatusDraft     = "DRAFT"
	POStatusOrdered   = "ORDERED"
	POStatusPartial   = "PARTIAL"
	POStatusReceived  = "RECEIVED"
	POStatusClosed    = "CLOSED"
	POStatusCancelled = "CANCELLED"
)

// POLineInput is a line of a new order.
type POLineInput struct {
	ItemID    int64           `json:"item_id"`
	UnitID    int64           `json:"unit_id"`
	Qty       decimal.Decimal `json:"qty"`
	UnitPrice decimal.Decimal `json:"unit_price"`
}

// POInput writes an order that comes from no request.
type POInput struct {
	ClientUUID  string        `json:"client_uuid"`
	SupplierID  int64         `json:"supplier_id"`
	WarehouseID int64         `json:"warehouse_id"`
	ExpectedOn  *civil.Date   `json:"expected_on"`
	Notes       *string       `json:"notes"`
	Send        bool          `json:"send"`
	Lines       []POLineInput `json:"lines"`
}

// POFromRequestLine says what of a line of the request goes on an order, and at what price.
type POFromRequestLine struct {
	LineNo    int              `json:"line_no"`
	Qty       *decimal.Decimal `json:"qty"`
	UnitPrice decimal.Decimal  `json:"unit_price"`
}

// POFromRequestOrder is one order of the split of a request: a supplier and its lines.
type POFromRequestOrder struct {
	ClientUUID string              `json:"client_uuid"`
	SupplierID int64               `json:"supplier_id"`
	Lines      []POFromRequestLine `json:"lines"`
}

// POFromRequestInput turns an approved request into orders, one for each supplier. Every line of the request is on exactly one order.
type POFromRequestInput struct {
	WarehouseID *int64               `json:"warehouse_id"`
	ExpectedOn  *civil.Date          `json:"expected_on"`
	Notes       *string              `json:"notes"`
	Send        bool                 `json:"send"`
	Orders      []POFromRequestOrder `json:"orders"`
}

// POLine is a line of an order with what has been received of it.
type POLine struct {
	LineNo        int             `json:"line_no"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitID        int64           `json:"unit_id"`
	UnitCode      string          `json:"unit_code"`
	Qty           decimal.Decimal `json:"qty"`
	QtyStock      decimal.Decimal `json:"qty_stock"`
	StockUnitCode string          `json:"stock_unit_code"`
	UnitPrice     decimal.Decimal `json:"unit_price"`
	Amount        decimal.Decimal `json:"amount"`
	QtyReceived   decimal.Decimal `json:"qty_received"`
	QtyReceivedSt decimal.Decimal `json:"qty_received_stock"`
	Closed        bool            `json:"closed"`
	RequestLineNo *int            `json:"request_line_no"`
}

// POBrief is a row of the list of orders (and the order of a request).
type POBrief struct {
	ID            int64           `json:"id"`
	Number        string          `json:"po_number"`
	Status        string          `json:"status"`
	SupplierID    int64           `json:"supplier_id"`
	SupplierName  string          `json:"supplier_name"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseName string          `json:"warehouse_name"`
	RequestID     *int64          `json:"request_id"`
	RequestNumber *string         `json:"request_number"`
	OrderDate     civil.Date      `json:"order_date"`
	ExpectedOn    *civil.Date     `json:"expected_on"`
	Total         decimal.Decimal `json:"total"`
	LineCount     int             `json:"line_count"`
	QtyOrdered    decimal.Decimal `json:"qty_ordered"`
	QtyReceived   decimal.Decimal `json:"qty_received"`
}

// PurchaseOrder is an order with its lines and its receipts.
type PurchaseOrder struct {
	POBrief
	SupplierCode  string         `json:"supplier_code"`
	WarehouseCode string         `json:"warehouse_code"`
	Notes         *string        `json:"notes"`
	Version       int32          `json:"version"`
	CreatedAt     time.Time      `json:"created_at"`
	Lines         []POLine       `json:"lines"`
	Receipts      []ReceiptBrief `json:"receipts"`
}

// POFilter narrows the list of orders.
type POFilter struct {
	Status     string
	SupplierID *int64
	OpenOnly   bool
	Q          string
	Limit      int
}

func (s *Service) canPurchase(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermInventoryPurchase)
}

func limitOf(n int) int32 {
	if n < 1 || n > maxPageRow {
		return maxPageRow
	}
	return int32(n) //nolint:gosec // G115: bounded
}

// purchaseQty is a quantity in stock units as it reads in a purchase unit of the item (1 of the unit = num/den stock units).
func purchaseQty(stock decimal.Decimal, num, den int32) decimal.Decimal {
	return Round6(stock.Mul(decimal.NewFromInt32(den)).Div(decimal.NewFromInt32(num)))
}

func toBrief(r inventorydb.ListPOsRow) POBrief {
	return POBrief{ID: r.ID, Number: r.PoNumber, Status: r.Status, SupplierID: r.SupplierID, SupplierName: r.SupplierName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName,
		RequestID: r.RequestID, RequestNumber: r.RequestNumber, OrderDate: r.OrderDate, ExpectedOn: r.ExpectedOn, Total: r.Total, LineCount: int(r.LineCount), QtyOrdered: r.QtyOrdered, QtyReceived: r.QtyReceived}
}

// ListPurchaseOrders lists the orders, newest first.
func (s *Service) ListPurchaseOrders(ctx context.Context, propertyID int64, f POFilter) ([]POBrief, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	arg := inventorydb.ListPOsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), SupplierID: f.SupplierID, Q: optional(&f.Q), RowLimit: limitOf(f.Limit)}
	if f.OpenOnly {
		t := true
		arg.OpenOnly = &t
	}
	rows, err := s.q(ctx).ListPOs(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]POBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBrief(r))
	}
	return out, nil
}

func (s *Service) ordersOfRequest(ctx context.Context, tenantID, propertyID, requestID int64) ([]POBrief, error) {
	rows, err := s.q(ctx).ListPOs(ctx, inventorydb.ListPOsParams{TenantID: tenantID, PropertyID: propertyID, RowLimit: maxPageRow})
	if err != nil {
		return nil, err
	}
	out := []POBrief{}
	for _, r := range rows {
		if r.RequestID != nil && *r.RequestID == requestID {
			out = append(out, toBrief(r))
		}
	}
	return out, nil
}

// GetPurchaseOrder answers one order with its lines and its receipts.
func (s *Service) GetPurchaseOrder(ctx context.Context, propertyID, id int64) (PurchaseOrder, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return PurchaseOrder{}, err
	}
	return s.purchaseOrder(ctx, p, propertyID, id)
}

func (s *Service) purchaseOrder(ctx context.Context, p auth.Principal, propertyID, id int64) (PurchaseOrder, error) {
	q := s.q(ctx)
	r, err := q.GetPO(ctx, inventorydb.GetPOParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return PurchaseOrder{}, orNotFound(err, notFound("PO_NOT_FOUND", "order"))
	}
	out := PurchaseOrder{POBrief: POBrief{ID: r.ID, Number: r.PoNumber, Status: r.Status, SupplierID: r.SupplierID, SupplierName: r.SupplierName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName,
		RequestID: r.RequestID, RequestNumber: r.RequestNumber, OrderDate: r.OrderDate, ExpectedOn: r.ExpectedOn, Total: r.Total}, SupplierCode: r.SupplierCode, WarehouseCode: r.WarehouseCode,
		Notes: r.Notes, Version: r.Version, CreatedAt: r.CreatedAt}
	lines, err := q.ListPOLines(ctx, inventorydb.ListPOLinesParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: id})
	if err != nil {
		return PurchaseOrder{}, err
	}
	out.Lines = make([]POLine, 0, len(lines))
	for _, l := range lines {
		pl := POLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitID: l.PurchaseUnitID, UnitCode: l.UnitCode, Qty: l.QtyPurchase, QtyStock: l.QtyStock,
			StockUnitCode: l.StockUnitCode, UnitPrice: l.UnitPrice, Amount: l.Amount, QtyReceivedSt: l.QtyReceivedStock, QtyReceived: purchaseQty(l.QtyReceivedStock, l.StockNum, l.StockDen), Closed: l.Closed}
		if l.RequestLineNo != nil {
			n := int(*l.RequestLineNo)
			pl.RequestLineNo = &n
		}
		out.Lines = append(out.Lines, pl)
		out.QtyOrdered = out.QtyOrdered.Add(l.QtyStock)
		out.QtyReceived = out.QtyReceived.Add(l.QtyReceivedStock)
	}
	out.LineCount = len(out.Lines)
	rc, err := q.ListReceipts(ctx, inventorydb.ListReceiptsParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: &id, RowLimit: maxPageRow})
	if err != nil {
		return PurchaseOrder{}, err
	}
	out.Receipts = make([]ReceiptBrief, 0, len(rc))
	for _, x := range rc {
		out.Receipts = append(out.Receipts, toReceiptBrief(x))
	}
	return out, nil
}

type orderLine struct {
	item  inventorydb.GetItemRow
	unit  int64
	qty   decimal.Decimal
	stock decimal.Decimal
	price decimal.Decimal
	value decimal.Decimal
	reqNo *int32
}

// checkOrderLines validates the lines of an order: the item and its unit, the quantity, the price (positive, at the decimals of the currency), one line per item.
func (s *Service) checkOrderLines(ctx context.Context, p auth.Principal, propertyID int64, lines []POLineInput, decimals int32) ([]orderLine, error) {
	in := make([]RequestLineInput, 0, len(lines))
	for _, l := range lines {
		price := l.UnitPrice
		in = append(in, RequestLineInput{ItemID: l.ItemID, UnitID: l.UnitID, Qty: l.Qty, EstUnitPrice: &price})
	}
	checked, err := s.checkRequestLines(ctx, p, propertyID, in, decimals)
	if err != nil {
		return nil, err
	}
	out := make([]orderLine, 0, len(checked))
	for i, c := range checked {
		if !c.price.IsPositive() {
			return nil, invalid("order", fieldErr(fmt.Sprintf("lines[%d].unit_price", i), "NOT_POSITIVE", "a price above zero"))
		}
		out = append(out, orderLine{item: c.item, unit: c.in.UnitID, qty: c.in.Qty, stock: c.stockQty, price: *c.price, value: c.in.Qty.Mul(*c.price).Round(decimals)})
	}
	return out, nil
}

func (s *Service) checkOrderHeader(ctx context.Context, p auth.Principal, propertyID, supplierID, warehouseID int64, expected *civil.Date, notes *string) error {
	q := s.q(ctx)
	var f []apperr.FieldError
	sup, err := q.GetSupplierLite(ctx, inventorydb.GetSupplierLiteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: supplierID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		f = append(f, fieldErr("supplier_id", "NOT_FOUND", "no such supplier in this property"))
	case err != nil:
		return err
	case !sup.IsActive:
		f = append(f, fieldErr("supplier_id", "INACTIVE", "the supplier is switched off"))
	}
	w, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: warehouseID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		f = append(f, fieldErr("warehouse_id", "NOT_FOUND", "no such warehouse in this property"))
	case err != nil:
		return err
	case !w.IsActive:
		f = append(f, fieldErr("warehouse_id", "INACTIVE", "the warehouse is switched off"))
	}
	if notes != nil && len([]rune(trimmed(*notes))) > 300 {
		f = append(f, fieldErr("notes", "INVALID_VALUE", "300 characters at most"))
	}
	_ = expected
	if len(f) > 0 {
		return invalid("order", f...)
	}
	return nil
}

func (s *Service) insertPO(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, key pgtype.UUID, supplierID, warehouseID int64, requestID *int64, expected *civil.Date, notes *string,
	lines []orderLine, send bool) (int64, string, error) {
	q := s.q(ctx)
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvPO)
	if err != nil {
		return 0, "", err
	}
	total := decimal.Zero
	for _, l := range lines {
		total = total.Add(l.value)
	}
	status := POStatusDraft
	if send {
		status = POStatusOrdered
	}
	id, err := q.InsertPO(ctx, inventorydb.InsertPOParams{TenantID: p.TenantID, PropertyID: propertyID, PoNumber: number, ClientUuid: key, SupplierID: supplierID, WarehouseID: warehouseID, RequestID: requestID,
		OrderDate: bd, ExpectedOn: expected, Status: status, Notes: optional(notes), Total: total, ActorID: p.ActorID()})
	if err != nil {
		return 0, "", err
	}
	for i, l := range lines {
		var rid *int64
		if l.reqNo != nil {
			rid = requestID
		}
		if err := q.InsertPOLine(ctx, inventorydb.InsertPOLineParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: id, LineNo: int32(i + 1), ItemID: l.item.ID, PurchaseUnitID: l.unit, //nolint:gosec // G115: at most 200
			QtyPurchase: l.qty, QtyStock: l.stock, UnitPrice: l.price, Amount: l.value, RequestID: rid, RequestLineNo: l.reqNo}); err != nil {
			return 0, "", err
		}
	}
	return id, number, s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.po_created", "inv_purchase_order", id, number, nil, map[string]any{"number": number, "status": status, "total": total, "lines": len(lines), "request_id": requestID}))
}

// CreatePurchaseOrder writes an order that comes from no request (inventory.purchase). The property must allow it (po_requires_pr off, D13): 409 PO_REQUIRES_REQUEST otherwise.
func (s *Service) CreatePurchaseOrder(ctx context.Context, propertyID int64, in POInput) (PurchaseOrder, error) {
	p, err := s.canPurchase(ctx, propertyID)
	if err != nil {
		return PurchaseOrder{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return PurchaseOrder{}, invalid("order", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return PurchaseOrder{}, err
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		if prev, err := q.GetPOByClientUUID(ctx, inventorydb.GetPOByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		cfg, err := s.settingsOrDefault(ctx, p, propertyID)
		if err != nil {
			return err
		}
		if cfg.PoRequiresPr {
			return apperr.Conflict("PO_REQUIRES_REQUEST", "this property orders from an approved purchase request: make the order from the request")
		}
		if err := s.checkOrderHeader(ctx, p, propertyID, in.SupplierID, in.WarehouseID, in.ExpectedOn, in.Notes); err != nil {
			return err
		}
		lines, err := s.checkOrderLines(ctx, p, propertyID, in.Lines, prop.CurrencyDecimals)
		if err != nil {
			return err
		}
		id, _, err = s.insertPO(ctx, p, propertyID, bd, key, in.SupplierID, in.WarehouseID, nil, in.ExpectedOn, in.Notes, lines, in.Send)
		return err
	})
	if err != nil {
		return PurchaseOrder{}, err
	}
	return s.purchaseOrder(ctx, p, propertyID, id)
}

// settingsOrDefault reads the settings, or the defaults of a property that never had any.
func (s *Service) settingsOrDefault(ctx context.Context, p auth.Principal, propertyID int64) (Settings, error) {
	r, err := s.q(ctx).GetSettings(ctx, inventorydb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return toSettings(r), nil
}

// CreateOrdersFromRequest turns an approved request into orders, one for each supplier (inventory.purchase). Every line of the request is on exactly one order; a quantity may be less than the approved one and
// never more. The request becomes CONVERTED.
func (s *Service) CreateOrdersFromRequest(ctx context.Context, propertyID, requestID int64, in POFromRequestInput) ([]PurchaseOrder, error) {
	p, err := s.canPurchase(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if len(in.Orders) == 0 || len(in.Orders) > 50 {
		return nil, invalid("order", fieldErr("orders", "OUT_OF_RANGE", "1 to 50 orders"))
	}
	var created []int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadForChange(ctx, p, propertyID, requestID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		// a repeated request: the orders with these client_uuids exist already
		keys := make([]pgtype.UUID, 0, len(in.Orders))
		var existing []int64
		for i, o := range in.Orders {
			var key pgtype.UUID
			if perr := key.Scan(o.ClientUUID); perr != nil || !key.Valid {
				return invalid("order", fieldErr(fmt.Sprintf("orders[%d].client_uuid", i), "INVALID_VALUE", "a UUID made by the client"))
			}
			keys = append(keys, key)
			if prev, err := q.GetPOByClientUUID(ctx, inventorydb.GetPOByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
				existing = append(existing, prev)
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if len(existing) == len(in.Orders) {
			created = existing
			return nil
		}
		if cur.Status != ReqApproved {
			return apperr.Conflict("PR_NOT_APPROVED", "only an approved purchase request is turned into orders").WithContext("status", cur.Status)
		}
		warehouseID := int64(0)
		switch {
		case in.WarehouseID != nil:
			warehouseID = *in.WarehouseID
		case cur.WarehouseID != nil:
			warehouseID = *cur.WarehouseID
		default:
			return invalid("order", fieldErr("warehouse_id", "REQUIRED", "the warehouse the goods are delivered to"))
		}
		reqLines, err := q.ListRequestLines(ctx, inventorydb.ListRequestLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequestID: requestID})
		if err != nil {
			return err
		}
		byNo := map[int32]inventorydb.ListRequestLinesRow{}
		for _, l := range reqLines {
			byNo[l.LineNo] = l
		}
		used := map[int]bool{}
		var f []apperr.FieldError
		type planned struct {
			supplier int64
			lines    []orderLine
		}
		plans := make([]planned, 0, len(in.Orders))
		for i, o := range in.Orders {
			at := fmt.Sprintf("orders[%d]", i)
			pl := planned{supplier: o.SupplierID}
			if len(o.Lines) == 0 {
				f = append(f, fieldErr(at+".lines", "OUT_OF_RANGE", "at least one line"))
			}
			for j, l := range o.Lines {
				la := fmt.Sprintf("%s.lines[%d]", at, j)
				rl, ok := byNo[int32(l.LineNo)] //nolint:gosec // G115: checked below
				switch {
				case !ok:
					f = append(f, fieldErr(la+".line_no", "NOT_FOUND", "no such line on the request"))
					continue
				case used[l.LineNo]:
					f = append(f, fieldErr(la+".line_no", "DUPLICATE", "a line of the request is on one order"))
					continue
				}
				used[l.LineNo] = true
				qty := rl.QtyPurchase
				if l.Qty != nil {
					qty = *l.Qty
				}
				if !qty.IsPositive() || qty.GreaterThan(rl.QtyPurchase) || !qty.Equal(Round6(qty)) {
					f = append(f, fieldErr(la+".qty", "EXCEEDS_REQUEST", "above zero and not more than the "+rl.QtyPurchase.String()+" approved"))
					continue
				}
				if !l.UnitPrice.IsPositive() || !l.UnitPrice.Equal(l.UnitPrice.Round(prop.CurrencyDecimals)) {
					f = append(f, fieldErr(la+".unit_price", "INVALID_VALUE", fmt.Sprintf("above zero, %d decimals at most", prop.CurrencyDecimals)))
					continue
				}
				it, err := q.GetItem(ctx, inventorydb.GetItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: rl.ItemID})
				if err != nil {
					return err
				}
				no := int32(l.LineNo) //nolint:gosec // G115: checked above
				pl.lines = append(pl.lines, orderLine{item: it, unit: rl.PurchaseUnitID, qty: qty, stock: StockQty(qty, rl.StockNum, rl.StockDen), price: l.UnitPrice,
					value: qty.Mul(l.UnitPrice).Round(prop.CurrencyDecimals), reqNo: &no})
			}
			plans = append(plans, pl)
		}
		for no := range byNo {
			if !used[int(no)] {
				f = append(f, fieldErr("orders", "LINE_NOT_ORDERED", fmt.Sprintf("line %d of the request is on no order", no)))
			}
		}
		if len(f) > 0 {
			return invalid("order", f...)
		}
		for i, pl := range plans {
			if err := s.checkOrderHeader(ctx, p, propertyID, pl.supplier, warehouseID, in.ExpectedOn, in.Notes); err != nil {
				return err
			}
			id, _, err := s.insertPO(ctx, p, propertyID, bd, keys[i], pl.supplier, warehouseID, &requestID, in.ExpectedOn, in.Notes, pl.lines, in.Send)
			if err != nil {
				return err
			}
			created = append(created, id)
		}
		if err := q.SetRequestState(ctx, inventorydb.SetRequestStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: requestID, Status: ReqConverted, Cycle: cur.Cycle, SubmittedAt: cur.SubmittedAt,
			ReturnedNote: nil, EstTotal: cur.EstTotal, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.pr_converted", "inv_request", requestID, cur.RequestNumber, map[string]any{"status": ReqApproved},
			map[string]any{"status": ReqConverted, "orders": len(created)}))
	})
	if err != nil {
		return nil, err
	}
	out := make([]PurchaseOrder, 0, len(created))
	for _, id := range created {
		po, err := s.purchaseOrder(ctx, p, propertyID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, po)
	}
	return out, nil
}

func errPOState(status, want string) *apperr.Error {
	return apperr.Conflict("PO_NOT_ORDERABLE", "the order is "+status+": this is for an order that is "+want).WithContext("status", status)
}

// loadPOForChange locks an order and reads it; the caller is inside s.write.
func (s *Service) loadPOForChange(ctx context.Context, p auth.Principal, propertyID, id int64) (inventorydb.GetPORow, error) {
	if err := db.LockRows(ctx, db.StockOrders, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return inventorydb.GetPORow{}, orNotFound(err, notFound("PO_NOT_FOUND", "order"))
	}
	r, err := s.q(ctx).GetPO(ctx, inventorydb.GetPOParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	return r, orNotFound(err, notFound("PO_NOT_FOUND", "order"))
}

// SendPurchaseOrder tells the supplier: the order goes from DRAFT to ORDERED, which is the state a receipt needs (inventory.purchase).
func (s *Service) SendPurchaseOrder(ctx context.Context, propertyID, id int64) (PurchaseOrder, error) {
	return s.changePO(ctx, propertyID, id, "inventory.po_sent", func(ctx context.Context, p auth.Principal, cur inventorydb.GetPORow) (string, error) {
		if cur.Status != POStatusDraft {
			return "", errPOState(cur.Status, "a draft")
		}
		return POStatusOrdered, nil
	})
}

// CancelPurchaseOrder cancels an order that nothing has been received against (inventory.purchase). The lines it took from a request are released, and the request is approved again when no order of it stands.
func (s *Service) CancelPurchaseOrder(ctx context.Context, propertyID, id int64) (PurchaseOrder, error) {
	return s.changePO(ctx, propertyID, id, "inventory.po_cancelled", func(ctx context.Context, p auth.Principal, cur inventorydb.GetPORow) (string, error) {
		if cur.Status != POStatusDraft && cur.Status != POStatusOrdered {
			return "", errPOState(cur.Status, "a draft or ordered with nothing received")
		}
		if n, err := s.q(ctx).CountPOReceipts(ctx, inventorydb.CountPOReceiptsParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: &id}); err != nil {
			return "", err
		} else if n > 0 {
			return "", errPOState(cur.Status, "ordered with nothing received: close it short instead")
		}
		return POStatusCancelled, nil
	})
}

// ClosePurchaseOrder closes an order short: what is not received is not coming (inventory.purchase). Something must have been received; an order with nothing received is cancelled.
func (s *Service) ClosePurchaseOrder(ctx context.Context, propertyID, id int64) (PurchaseOrder, error) {
	return s.changePO(ctx, propertyID, id, "inventory.po_closed", func(ctx context.Context, p auth.Principal, cur inventorydb.GetPORow) (string, error) {
		if cur.Status != POStatusOrdered && cur.Status != POStatusPartial {
			return "", errPOState(cur.Status, "ordered or partly received")
		}
		if n, err := s.q(ctx).CountPOReceipts(ctx, inventorydb.CountPOReceiptsParams{TenantID: p.TenantID, PropertyID: propertyID, PoID: &id}); err != nil {
			return "", err
		} else if n == 0 {
			return "", apperr.Conflict("PO_NOTHING_RECEIVED", "nothing was received against this order: cancel it instead")
		}
		return POStatusClosed, nil
	})
}

func (s *Service) changePO(ctx context.Context, propertyID, id int64, action string, decide func(context.Context, auth.Principal, inventorydb.GetPORow) (string, error)) (PurchaseOrder, error) {
	p, err := s.canPurchase(ctx, propertyID)
	if err != nil {
		return PurchaseOrder{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.loadPOForChange(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		to, err := decide(ctx, p, cur)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if to == POStatusClosed {
			if err := q.ClosePOLines(ctx, inventorydb.ClosePOLinesParams{PropertyID: propertyID, PoID: id}); err != nil {
				return err
			}
		}
		if to == POStatusCancelled && cur.RequestID != nil {
			if err := s.releaseRequest(ctx, p, propertyID, bd, id, *cur.RequestID); err != nil {
				return err
			}
		}
		if err := q.SetPOStatus(ctx, inventorydb.SetPOStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: to, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, action, "inv_purchase_order", id, cur.PoNumber, map[string]any{"status": cur.Status}, map[string]any{"status": to}))
	})
	if err != nil {
		return PurchaseOrder{}, err
	}
	return s.purchaseOrder(ctx, p, propertyID, id)
}

// orderStatusAfter is the state of an order once its lines are known: every line received or closed makes it RECEIVED, otherwise PARTIAL while something is received.
func orderStatusAfter(lines []inventorydb.ListPOLinesRow, current string) string {
	received, all := false, true
	for _, l := range lines {
		if l.QtyReceivedStock.IsPositive() {
			received = true
		}
		if !l.Closed && l.QtyReceivedStock.LessThan(l.QtyStock) {
			all = false
		}
	}
	switch {
	case received && all:
		return POStatusReceived
	case received:
		return POStatusPartial
	case current == POStatusPartial || current == POStatusReceived:
		return POStatusOrdered
	}
	return current
}
