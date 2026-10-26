package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The deduction of a POS sale (35-inventory.md 5.6, 4.9, D4, D5, D15). A POS line becomes a sale when the POS posts it; in the same transaction the POS calls QueueSale, which writes one row of
// inv_consumption_queue and takes no stock lock. A worker (and the night audit) applies the rows: one POS_SALE movement for each component of the recipe, at the cost of the warehouse, in the class of the
// product sold, never refused for lack of stock. A void of a line already deducted writes a second row (VOID_RETURN, VOID_WASTE); a void of a line that is not deducted yet cancels its row.

// Queue kinds and statuses.
const (
	QueueSale       = "SALE"
	QueueVoidReturn = "VOID_RETURN"
	QueueVoidWaste  = "VOID_WASTE"

	QueuePending   = "PENDING"
	QueueHeld      = "HELD"
	QueueApplied   = "APPLIED"
	QueueCancelled = "CANCELLED"
	QueueFailed    = "FAILED"
)

// queueSource is the source type of the movements a queue row makes (inv_movements.source_type): the row, and the component as the line.
const queueSource = "POS_QUEUE"

// queueBatch is how many rows a worker takes at once for one warehouse.
const queueBatch = 50

// PosOption is a choice of the guest on a line of a bill.
type PosOption struct {
	OptionID int64
	Qty      int
}

// PosSale is what the POS tells the stock when a line is posted as a sale.
type PosSale struct {
	BillLineID   int64
	OutletID     int64
	ProductID    int64
	Quantity     decimal.Decimal
	Options      []PosOption
	BusinessDate civil.Date
}

// PosVoid is what the POS tells the stock when a sent line is voided: Waste is true when the kitchen has made the food (a ticket that is COOKING, READY or SERVED, or the setting of D5), so the stock is
// not returned.
type PosVoid struct {
	BillLineID   int64
	Waste        bool
	BusinessDate civil.Date
}

// queueComponent is one ingredient of a queued sale, in stock units, as the recipe said when the line was sold (a later change of the recipe changes nothing).
type queueComponent struct {
	ItemID   int64  `json:"item_id"`
	QtyStock string `json:"qty_stock"`
}

func (c queueComponent) qty() decimal.Decimal {
	q, _ := decimal.NewFromString(c.QtyStock)
	return q
}

func decodeComponents(raw []byte) ([]queueComponent, error) {
	var out []queueComponent
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, apperr.Internal(fmt.Errorf("inventory: components of a queue row: %w", err))
	}
	return out, nil
}

// expandRecipe turns the lines of a recipe and the options of a bill line into the quantities of stock one sale takes (D15): the lines of the product, then the lines of the variant chosen, which
// replace the product's line for the same item, then the lines of the modifiers, which add; each in stock units and multiplied by the quantity sold.
func expandRecipe(lines []inventorydb.ListRecipeLinesRow, chosen map[int64]int, qty decimal.Decimal) []queueComponent {
	stock := func(l inventorydb.ListRecipeLinesRow) decimal.Decimal {
		return l.Quantity.Mul(decimal.NewFromInt(int64(l.StockNum))).Div(decimal.NewFromInt(int64(l.StockDen)))
	}
	byItem := map[int64]decimal.Decimal{}
	var order []int64
	touch := func(id int64) {
		if _, ok := byItem[id]; !ok {
			order = append(order, id)
		}
	}
	for _, l := range lines {
		if l.OptionID == nil {
			touch(l.ItemID)
			byItem[l.ItemID] = byItem[l.ItemID].Add(stock(l))
		}
	}
	for _, kind := range []string{"VARIANT", "MODIFIER"} {
		replaced := map[int64]bool{}
		for _, l := range lines {
			if l.OptionID == nil || l.GroupKind != kind {
				continue
			}
			n, ok := chosen[*l.OptionID]
			if !ok {
				continue
			}
			touch(l.ItemID)
			if kind == "VARIANT" {
				if !replaced[l.ItemID] {
					byItem[l.ItemID] = decimal.Zero
					replaced[l.ItemID] = true
				}
				byItem[l.ItemID] = byItem[l.ItemID].Add(stock(l))
				continue
			}
			byItem[l.ItemID] = byItem[l.ItemID].Add(stock(l).Mul(decimal.NewFromInt(int64(n))))
		}
	}
	out := make([]queueComponent, 0, len(order))
	for _, id := range order {
		q := byItem[id].Mul(qty).Round(6)
		if q.IsPositive() {
			out = append(out, queueComponent{ItemID: id, QtyStock: q.String()})
		}
	}
	return out
}

// QueueSale queues the deduction of a POS line (5.6). It runs inside the transaction of the POS, takes no stock lock and never fails a sale for a stock reason: a product with no recipe, an outlet with no
// warehouse queue nothing; a replayed call (the same line) queues nothing twice.
func (s *Service) QueueSale(ctx context.Context, tenantID, propertyID int64, sale PosSale) error {
	if !db.InTx(ctx) {
		return db.ErrNoTx
	}
	q := s.q(ctx)
	o, err := q.GetOutletStock(ctx, inventorydb.GetOutletStockParams{TenantID: tenantID, PropertyID: propertyID, ID: sale.OutletID})
	if err != nil {
		return orNotFound(err, notFound("OUTLET_NOT_FOUND", "outlet"))
	}
	if o.WarehouseID == nil {
		return nil
	}
	lines, err := q.ListRecipeLines(ctx, inventorydb.ListRecipeLinesParams{TenantID: tenantID, PropertyID: propertyID, ProductID: sale.ProductID})
	if err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil
	}
	chosen := map[int64]int{}
	for _, op := range sale.Options {
		n := op.Qty
		if n < 1 {
			n = 1
		}
		chosen[op.OptionID] = n
	}
	comps := expandRecipe(lines, chosen, sale.Quantity)
	if len(comps) == 0 {
		return nil
	}
	class, err := s.saleClass(ctx, tenantID, propertyID, sale.ProductID, o.WarehouseClass)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(comps)
	if err != nil {
		return apperr.Internal(err)
	}
	_, err = q.InsertConsumptionRow(ctx, inventorydb.InsertConsumptionRowParams{TenantID: tenantID, PropertyID: propertyID, OutletID: sale.OutletID, WarehouseID: *o.WarehouseID,
		PosBillLineID: sale.BillLineID, Kind: QueueSale, ProductID: sale.ProductID, Quantity: sale.Quantity, SaleClass: class, SaleDate: sale.BusinessDate, Components: raw, CreatedAt: s.clock.Now()})
	if errors.Is(err, pgx.ErrNoRows) { // the line is queued already: the hook is idempotent
		return nil
	}
	return err
}

// saleClass is the class a sale is charged to (4.9): the fb_class of the category of the product, else its parent's, else the class of the warehouse of the outlet.
func (s *Service) saleClass(ctx context.Context, tenantID, propertyID, productID int64, warehouseClass string) (string, error) {
	q := s.q(ctx)
	p, err := q.GetProductForRecipe(ctx, inventorydb.GetProductForRecipeParams{TenantID: tenantID, PropertyID: propertyID, ID: productID})
	if err != nil {
		return "", orNotFound(err, notFound("PRODUCT_NOT_FOUND", "product"))
	}
	c, err := q.CategoryFbClass(ctx, inventorydb.CategoryFbClassParams{TenantID: tenantID, PropertyID: propertyID, CategoryID: p.CategoryID})
	if err != nil {
		return "", err
	}
	if c != "" {
		return c, nil
	}
	if warehouseClass == "" {
		return "OTHER", nil
	}
	return warehouseClass, nil
}

// QueueVoid tells the stock that a sent line was voided (5.6). A row that is not applied yet is cancelled; an applied one gets a VOID_RETURN row (the goods come back at the cost they were taken) or a
// VOID_WASTE row (the food was made and thrown away). It runs inside the transaction of the POS and locks the queue row it decides on, so a worker that is applying it finishes first.
func (s *Service) QueueVoid(ctx context.Context, tenantID, propertyID int64, v PosVoid) error {
	if !db.InTx(ctx) {
		return db.ErrNoTx
	}
	q := s.q(ctx)
	sale, err := q.GetConsumptionByLine(ctx, inventorydb.GetConsumptionByLineParams{TenantID: tenantID, PropertyID: propertyID, PosBillLineID: &v.BillLineID, Kind: QueueSale})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the line was never queued: no recipe, no warehouse, or the module was off
	}
	if err != nil {
		return err
	}
	if err := db.LockRows(ctx, db.StockQueue, db.ForUpdate, propertyID, []int64{sale.ID}); err != nil {
		return err
	}
	sale, err = q.GetConsumptionRow(ctx, inventorydb.GetConsumptionRowParams{TenantID: tenantID, PropertyID: propertyID, ID: sale.ID})
	if err != nil {
		return err
	}
	switch sale.Status {
	case QueuePending, QueueHeld, QueueFailed:
		return q.SetConsumptionCancelled(ctx, inventorydb.SetConsumptionCancelledParams{TenantID: tenantID, PropertyID: propertyID, ID: sale.ID})
	case QueueApplied:
	default:
		return nil
	}
	if sale.OutletID == nil { // a banquet row is never voided (36 Q3)
		return nil
	}
	kind := QueueVoidReturn
	if v.Waste {
		kind = QueueVoidWaste
	}
	_, err = q.InsertConsumptionRow(ctx, inventorydb.InsertConsumptionRowParams{TenantID: tenantID, PropertyID: propertyID, OutletID: *sale.OutletID, WarehouseID: sale.WarehouseID,
		PosBillLineID: v.BillLineID, Kind: kind, SaleQueueID: &sale.ID, ProductID: sale.ProductID, Quantity: sale.Quantity, SaleClass: sale.SaleClass, SaleDate: v.BusinessDate, Components: sale.Components, CreatedAt: s.clock.Now()})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// -------------------------------------------------------------------------------------------------- the worker

// queueCtx holds what applying rows needs and that does not change inside a transaction: the cost-of-sales account of each class, the cost account of an item's category, and the department each outlet
// charges.
type queueCtx struct {
	s          *Service
	p          auth.Principal
	propertyID int64
	keys       map[string]int64
	cogs       map[int64]*int64
	depts      map[[2]int64]*int64
	outlets    map[int64]int64
	waste      int64
}

func (s *Service) newQueueCtx(p auth.Principal, propertyID int64) *queueCtx {
	return &queueCtx{s: s, p: p, propertyID: propertyID, keys: map[string]int64{}, cogs: map[int64]*int64{}, depts: map[[2]int64]*int64{}, outlets: map[int64]int64{}}
}

var cogsKey = map[string]string{"FOOD": "INV_COGS_FOOD", "BEVERAGE": "INV_COGS_BEVERAGE"}

// counter is the other side of the journal of a sale of the class for an item: the cost-of-sales account of the class (the item's category for the class OTHER) and the department of the outlet, by the
// rule of the account.
func (c *queueCtx) counter(ctx context.Context, ra inventorydb.InvConsumptionQueue, itemID int64) (*Counter, error) {
	var account int64
	if key, ok := cogsKey[ra.SaleClass]; ok {
		id, ok := c.keys[key]
		if !ok {
			var err error
			if id, err = c.s.keyAccount(ctx, c.p, c.propertyID, key); err != nil {
				return nil, err
			}
			c.keys[key] = id
		}
		account = id
	} else {
		acc, ok := c.cogs[itemID]
		if !ok {
			rows, err := c.s.q(ctx).ItemCogsAccounts(ctx, inventorydb.ItemCogsAccountsParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, ItemIds: []int64{itemID}})
			if err != nil {
				return nil, err
			}
			if len(rows) == 1 {
				acc = rows[0].CogsAccountID
			}
			c.cogs[itemID] = acc
		}
		if acc == nil {
			return nil, apperr.Conflict("CATEGORY_ACCOUNT_MISSING", "the category of the item has no cost-of-sales account for a sale of class OTHER").WithContext("item_id", itemID)
		}
		account = *acc
	}
	// the department of the cost: the one of the outlet that sold, and for the banquet (no outlet) the one of the charge code of the line, kept on the row
	dept := ra.DepartmentID
	if ra.OutletID != nil {
		od, ok := c.outlets[*ra.OutletID]
		if !ok {
			o, err := c.s.q(ctx).GetOutletStock(ctx, inventorydb.GetOutletStockParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, ID: *ra.OutletID})
			if err != nil {
				return nil, orNotFound(err, notFound("OUTLET_NOT_FOUND", "outlet"))
			}
			od = o.DepartmentID
			c.outlets[*ra.OutletID] = od
		}
		dept = &od
	}
	key := [2]int64{account, idKey(dept)}
	d, ok := c.depts[key]
	if !ok {
		var err error
		if d, err = c.s.resolveDepartment(ctx, c.p, c.propertyID, account, dept, true, "department_id"); err != nil {
			return nil, err
		}
		c.depts[key] = d
	}
	return &Counter{AccountID: account, DepartmentID: d}, nil
}

func (c *queueCtx) wasteAccount(ctx context.Context) (int64, error) {
	if c.waste != 0 {
		return c.waste, nil
	}
	id, err := c.s.keyAccount(ctx, c.p, c.propertyID, "INV_WASTE")
	if err != nil {
		return 0, err
	}
	c.waste = id
	return id, nil
}

// sourceDate is the day of the sale when the movement is made on another day (4.7, D19).
func sourceDate(ra inventorydb.InvConsumptionQueue, bd civil.Date) *civil.Date {
	if ra.SaleDate.Equal(bd) {
		return nil
	}
	d := ra.SaleDate
	return &d
}

// applyRowComponent makes the movement of one component of a row, whichever kind it is.
func (c *queueCtx) applyRowComponent(ctx context.Context, stock *Stock, bd civil.Date, ra inventorydb.InvConsumptionQueue, k int, comp queueComponent, saleMovement int64) (int64, error) {
	pair := Pair{ra.WarehouseID, comp.ItemID}
	ref := Ref{Type: queueSource, ID: ra.ID, Line: k + 1, SourceDate: sourceDate(ra, bd)}
	switch ra.Kind {
	case QueueSale:
		counter, err := c.counter(ctx, ra, comp.ItemID)
		if err != nil {
			return 0, err
		}
		posted, err := stock.Out(ctx, AllowShortfall(OutArgs{Pair: pair, Kind: KindPosSale, Qty: comp.qty(), Ref: ref, Counter: counter, CostClass: ra.SaleClass, OutletID: ra.OutletID}, "POS_SALE"))
		return posted.MovementID, err
	case QueueVoidReturn:
		posted, err := stock.Reverse(ctx, ReverseArgs{MovementID: saleMovement, Kind: KindPosSaleReturn, Ref: ref})
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == "MOVEMENT_NOT_REVERSIBLE" { // its shortfall was settled by goods that came in: the goods come back as a new layer at the cost they were taken
			m, gerr := c.s.q(ctx).LedgerGetMovement(ctx, inventorydb.LedgerGetMovementParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, ID: saleMovement})
			if gerr != nil || m.GlDebitAccountID == nil {
				return 0, err
			}
			posted, err = stock.In(ctx, InArgs{Pair: pair, Kind: KindPosSaleReturn, Qty: m.QtyDelta.Neg(), Value: m.ValueDelta.Neg(), Ref: ref,
				Counter: &Counter{AccountID: *m.GlDebitAccountID, DepartmentID: m.GlDepartmentID}, CostClass: m.CostClass, OutletID: m.OutletID})
		}
		return posted.MovementID, err
	default: // VOID_WASTE
		waste, err := c.wasteAccount(ctx)
		if err != nil {
			return 0, err
		}
		posted, err := stock.CogsToWaste(ctx, CogsToWasteArgs{SaleMovementID: saleMovement, Waste: waste, Ref: ref})
		return posted.MovementID, err
	}
}

// saleMovements are the movements of the sale row a void row undoes, one for each component.
func (s *Service) saleMovements(ctx context.Context, tenantID, propertyID int64, ra inventorydb.InvConsumptionQueue) ([]int64, error) {
	if ra.SaleQueueID == nil {
		return nil, nil
	}
	sale, err := s.q(ctx).GetConsumptionRow(ctx, inventorydb.GetConsumptionRowParams{TenantID: tenantID, PropertyID: propertyID, ID: *ra.SaleQueueID})
	if err != nil {
		return nil, err
	}
	return sale.MovementIds, nil
}

// savepoint runs fn so that a failure leaves nothing of it in the transaction: the database goes back to the savepoint and the balances the writer holds in memory go back to what they were.
func savepoint(ctx context.Context, stock *Stock, pairs []Pair, fn func() error) error {
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT pos_queue_row"); err != nil {
		return err
	}
	restore := stock.Snapshot(pairs)
	if err := fn(); err != nil {
		restore()
		if _, rerr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT pos_queue_row"); rerr != nil {
			return rerr
		}
		return &rowFailure{err}
	}
	_, err = tx.Exec(ctx, "RELEASE SAVEPOINT pos_queue_row")
	return err
}

// rowFailure is the error of one row: the row is marked FAILED and the others go on.
type rowFailure struct{ err error }

func (e *rowFailure) Error() string { return e.err.Error() }
func (e *rowFailure) Unwrap() error { return e.err }

func failureCode(err error) (code, msg string) {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		code, msg = ae.Code, ae.Message
	} else {
		code, msg = "INTERNAL", "an unexpected error stopped the deduction"
	}
	if len([]rune(msg)) > 300 {
		msg = string([]rune(msg)[:300])
	}
	return code, msg
}

// applyGroup applies the rows of one warehouse, inside the transaction of the caller, which holds the rows (FOR UPDATE) and the open business day in share mode. A frozen warehouse leaves them HELD.
// A row that cannot be applied is set FAILED and the others go on; an error that is not about a row is returned and the caller rolls the transaction back.
func (s *Service) applyGroup(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, warehouseID int64, rows []inventorydb.InvConsumptionQueue) (applied int, err error) {
	if len(rows) == 0 {
		return 0, nil
	}
	q := s.q(ctx)
	if err := db.NoteStockTable(ctx, db.StockQueue); err != nil {
		return 0, err
	}
	if err := db.LockRows(ctx, db.Warehouses, db.ForShare, propertyID, []int64{warehouseID}); err != nil {
		return 0, err
	}
	info, err := q.LedgerWarehouseInfo(ctx, inventorydb.LedgerWarehouseInfoParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: []int64{warehouseID}})
	if err != nil {
		return 0, err
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if len(info) == 1 && info[0].CountID != nil { // a count holds the warehouse: the rows wait for it (5.5)
		return 0, q.SetConsumptionHeld(ctx, inventorydb.SetConsumptionHeldParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
	}
	// the movements the void rows undo, and the pairs the rows take
	moves := map[int64][]int64{}
	pairSet := map[Pair]bool{}
	comps := map[int64][]queueComponent{}
	for _, r := range rows {
		cs, err := decodeComponents(r.Components)
		if err != nil {
			return 0, err
		}
		comps[r.ID] = cs
		for _, c := range cs {
			pairSet[Pair{warehouseID, c.ItemID}] = true
		}
		if r.Kind != QueueSale {
			if moves[r.ID], err = s.saleMovements(ctx, p.TenantID, propertyID, r); err != nil {
				return 0, err
			}
		}
	}
	pairs := make([]Pair, 0, len(pairSet))
	for pr := range pairSet {
		pairs = append(pairs, pr)
	}
	slices.SortFunc(pairs, func(a, b Pair) int { return int(a.ItemID - b.ItemID) })
	book, err := s.book(ctx, p, propertyID, bd)
	if err != nil {
		return 0, err
	}
	stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
	if err != nil {
		var ae *apperr.Error
		if errors.As(err, &ae) && ae.Code == "WAREHOUSE_INACTIVE" { // nothing can be taken from a warehouse that is switched off: the rows fail, and a person decides
			for _, r := range rows {
				if err := s.failRow(ctx, p, propertyID, bd, r.ID, ae.Code, ae.Message); err != nil {
					return 0, err
				}
			}
			return 0, nil
		}
		return 0, err
	}
	qc := s.newQueueCtx(p, propertyID)
	now := s.clock.Now()
	for _, r := range rows {
		cs := comps[r.ID]
		rowPairs := make([]Pair, 0, len(cs))
		for _, c := range cs {
			rowPairs = append(rowPairs, Pair{warehouseID, c.ItemID})
		}
		err := savepoint(ctx, stock, rowPairs, func() error {
			movementIDs := make([]int64, 0, len(cs))
			for k, c := range cs {
				var sm int64
				if r.Kind != QueueSale {
					if k >= len(moves[r.ID]) {
						return apperr.Conflict("QUEUE_ROW_INCONSISTENT", "the row to undo has fewer movements than components")
					}
					sm = moves[r.ID][k]
				}
				id, err := qc.applyRowComponent(ctx, stock, bd, r, k, c, sm)
				if err != nil {
					return err
				}
				movementIDs = append(movementIDs, id)
			}
			return q.SetConsumptionApplied(ctx, inventorydb.SetConsumptionAppliedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.ID, MovementIds: movementIDs, AppliedOn: &bd, AppliedAt: &now})
		})
		var rf *rowFailure
		switch {
		case err == nil:
			applied++
		case errors.As(err, &rf):
			code, msg := failureCode(rf.err)
			if err := s.failRow(ctx, p, propertyID, bd, r.ID, code, msg); err != nil {
				return applied, err
			}
		default:
			return applied, err
		}
	}
	return applied, nil
}

// failRow sets a row FAILED with its code and writes the audit entry: a row that stops the close is a state change somebody has to see (and retry or cancel).
func (s *Service) failRow(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, id int64, code, msg string) error {
	if err := s.q(ctx).SetConsumptionFailed(ctx, inventorydb.SetConsumptionFailedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, ErrorCode: &code, ErrorMessage: &msg}); err != nil {
		return err
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.queue_row_failed", "inv_consumption_queue", id, fmt.Sprintf("queue row %d", id), map[string]any{"status": QueuePending}, map[string]any{"status": QueueFailed, "code": code}))
}

// RunQueue applies the pending rows of every property, one transaction for each warehouse (the worker of 5.6). It returns how many rows were applied. Two workers never take the same row: the rows are taken
// with SKIP LOCKED.
func (s *Service) RunQueue(ctx context.Context) (int, error) {
	props, err := s.q(ctx).ListPropertiesWithPending(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, pr := range props {
		p := auth.Principal{TenantID: pr.TenantID, IsTenantAdmin: true}
		n, err := s.drainProperty(auth.WithPrincipal(ctx, p), p, pr.PropertyID)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// PrepareClose applies what waits in the POS queue of a property before the night audit opens its transaction (each warehouse in batches, each batch in a transaction of its own, as the worker does), so that
// the drain inside the close finds nothing to apply in the usual case. The stock level takes its locks in one order for a transaction; a close that applied the rows of two warehouses, or of one and then
// posted the amenities, would take them out of that order (the second try finds the queue empty).
func (s *Service) PrepareClose(ctx context.Context, p auth.Principal, propertyID int64) error {
	_, err := s.drainProperty(ctx, p, propertyID)
	return err
}

// drainProperty applies what waits for a property from outside a transaction: for each warehouse, batches of rows, each batch in its own transaction.
func (s *Service) drainProperty(ctx context.Context, p auth.Principal, propertyID int64) (int, error) {
	whs, err := s.q(ctx).ListWarehousesWithPending(ctx, inventorydb.ListWarehousesWithPendingParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, w := range whs {
		for {
			n, more, err := s.applyBatch(ctx, p, propertyID, w)
			total += n
			if err != nil {
				return total, err
			}
			if !more {
				break
			}
		}
	}
	return total, nil
}

// applyBatch is one transaction of the worker: the open day in share mode, up to queueBatch rows of the warehouse, applied. A batch that fails for a reason that is not about a row is tried again row by
// row, so that a row that cannot go through does not hold the others back: it fails alone.
func (s *Service) applyBatch(ctx context.Context, p auth.Principal, propertyID, warehouseID int64) (applied int, more bool, err error) {
	var rows []inventorydb.InvConsumptionQueue
	run := func(ctx context.Context, only *int64) (int, error) {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) {
				return 0, nil // no open day (the property has none): the next round
			}
			return 0, err
		}
		if only != nil {
			r, err := s.q(ctx).ClaimConsumptionRowByID(ctx, inventorydb.ClaimConsumptionRowByIDParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *only})
			if errors.Is(err, pgx.ErrNoRows) {
				return 0, nil
			}
			if err != nil {
				return 0, err
			}
			rows = []inventorydb.InvConsumptionQueue{r}
		} else {
			if rows, err = s.q(ctx).ClaimConsumptionRows(ctx, inventorydb.ClaimConsumptionRowsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, RowLimit: queueBatch}); err != nil {
				return 0, err
			}
		}
		return s.applyGroup(ctx, p, propertyID, day.BusinessDate, warehouseID, rows)
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		n, err := run(ctx, nil)
		applied = n
		return err
	})
	if err == nil {
		return applied, len(rows) == queueBatch, nil
	}
	// the batch failed: take the rows one by one
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if len(ids) == 0 {
		return 0, false, err
	}
	applied = 0
	for _, id := range ids {
		var n int
		rerr := s.txm.WithinTx(ctx, func(ctx context.Context) error {
			var e error
			n, e = run(ctx, &id)
			return e
		})
		applied += n
		if rerr != nil {
			code, msg := failureCode(rerr)
			if ferr := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				return s.q(ctx).SetConsumptionFailed(ctx, inventorydb.SetConsumptionFailedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, ErrorCode: &code, ErrorMessage: &msg})
			}); ferr != nil {
				return applied, false, ferr
			}
		}
	}
	return applied, true, nil
}

// DrainQueue applies every pending row of the property inside the transaction of the caller: the night audit does it before the journal (5.7 step 1). A row that cannot be applied is left FAILED, which
// stops the close (INVENTORY_QUEUE_FAILED).
func (s *Service) DrainQueue(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) (int, error) {
	if !db.InTx(ctx) {
		return 0, db.ErrNoTx
	}
	q := s.q(ctx)
	whs, err := q.ListWarehousesWithPending(ctx, inventorydb.ListWarehousesWithPendingParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return 0, err
	}
	total := 0
	for _, w := range whs {
		for {
			rows, err := q.ClaimConsumptionRows(ctx, inventorydb.ClaimConsumptionRowsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: w, RowLimit: queueBatch})
			if err != nil {
				return total, err
			}
			if len(rows) == 0 {
				break
			}
			n, err := s.applyGroup(ctx, p, propertyID, bd, w, rows)
			total += n
			if err != nil {
				return total, err
			}
			if len(rows) < queueBatch {
				break
			}
		}
	}
	return total, nil
}

// -------------------------------------------------------------------------------------------------- the screens

// QueueRow is a row of the queue as the screens show it.
type QueueRow struct {
	ID            int64  `json:"id"`
	Source        string `json:"source"`
	OutletID      *int64 `json:"outlet_id"`
	OutletCode    string `json:"outlet_code"`
	OutletName    string `json:"outlet_name"`
	WarehouseID   int64  `json:"warehouse_id"`
	WarehouseName string `json:"warehouse_name"`
	BillLineID    *int64 `json:"bill_line_id"`
	// BillNumber is the number of the POS bill, or for a banquet row the number of the event and the name of the session.
	BillNumber   string          `json:"bill_number"`
	Kind         string          `json:"kind"`
	ProductID    int64           `json:"product_id"`
	ProductName  string          `json:"product_name"`
	Quantity     decimal.Decimal `json:"quantity"`
	SaleClass    string          `json:"sale_class"`
	SaleDate     civil.Date      `json:"sale_date"`
	Status       string          `json:"status"`
	Attempts     int             `json:"attempts"`
	ErrorCode    *string         `json:"error_code"`
	ErrorMessage *string         `json:"error_message"`
	CreatedAt    time.Time       `json:"created_at"`
	AppliedOn    *civil.Date     `json:"applied_on"`
}

// QueueSummary is what waits, for the setup check and the badge of the screens.
type QueueSummary struct {
	Pending int `json:"pending"`
	Held    int `json:"held"`
	Failed  int `json:"failed"`
}

// QueueList is the queue of a property: the counts and the rows of the statuses asked for (the failed ones by default).
type QueueList struct {
	Summary QueueSummary `json:"summary"`
	Rows    []QueueRow   `json:"rows"`
}

// ListQueue lists the rows of the queue by status (inventory.view). Statuses empty: the rows that need attention (HELD and FAILED).
func (s *Service) ListQueue(ctx context.Context, propertyID int64, statuses []string, limit int) (QueueList, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return QueueList{}, err
	}
	q := s.q(ctx)
	out := QueueList{Rows: []QueueRow{}}
	counts, err := q.CountConsumptionByStatus(ctx, inventorydb.CountConsumptionByStatusParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	for _, c := range counts {
		switch c.Status {
		case QueuePending:
			out.Summary.Pending = int(c.N)
		case QueueHeld:
			out.Summary.Held = int(c.N)
		case QueueFailed:
			out.Summary.Failed = int(c.N)
		}
	}
	if len(statuses) == 0 {
		statuses = []string{QueueHeld, QueueFailed}
	}
	for _, st := range statuses {
		switch st {
		case QueuePending, QueueHeld, QueueApplied, QueueCancelled, QueueFailed:
		default:
			return out, invalid("queue filter", fieldErr("status", "INVALID_VALUE", "PENDING, HELD, APPLIED, CANCELLED or FAILED"))
		}
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := q.ListConsumptionQueue(ctx, inventorydb.ListConsumptionQueueParams{TenantID: p.TenantID, PropertyID: propertyID, Statuses: statuses, RowLimit: int32(limit)}) //nolint:gosec // G115: bounded
	if err != nil {
		return out, err
	}
	for _, r := range rows {
		out.Rows = append(out.Rows, toQueueRow(r))
	}
	return out, nil
}

// RetryQueueRow puts a FAILED row back to PENDING and tries it now (inventory.manage); the answer is the row as it is afterwards.
func (s *Service) RetryQueueRow(ctx context.Context, propertyID, id int64) (QueueRow, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return QueueRow{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.StockQueue, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("QUEUE_ROW_NOT_FOUND", "queue row"))
		}
		r, err := s.q(ctx).GetConsumptionRow(ctx, inventorydb.GetConsumptionRowParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("QUEUE_ROW_NOT_FOUND", "queue row"))
		}
		if r.Status != QueueFailed {
			return apperr.Conflict("QUEUE_ROW_NOT_FAILED", "only a failed row is tried again").WithContext("status", r.Status)
		}
		if err := s.q(ctx).SetConsumptionRetry(ctx, inventorydb.SetConsumptionRetryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		r.Status = QueuePending
		if _, err := s.applyGroup(ctx, p, propertyID, bd, r.WarehouseID, []inventorydb.InvConsumptionQueue{r}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.queue_row_retried", "inv_consumption_queue", id, fmt.Sprintf("queue row %d", id), map[string]any{"status": QueueFailed}, nil))
	})
	if err != nil {
		return QueueRow{}, err
	}
	return s.queueRow(ctx, p, propertyID, id)
}

// CancelQueueRow gives up a FAILED, HELD or PENDING row (inventory.manage): the sale is not deducted from stock, and the audit says who decided.
func (s *Service) CancelQueueRow(ctx context.Context, propertyID, id int64) (QueueRow, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return QueueRow{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		if err := db.LockRows(ctx, db.StockQueue, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("QUEUE_ROW_NOT_FOUND", "queue row"))
		}
		r, err := s.q(ctx).GetConsumptionRow(ctx, inventorydb.GetConsumptionRowParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("QUEUE_ROW_NOT_FOUND", "queue row"))
		}
		if r.Status != QueueFailed && r.Status != QueueHeld && r.Status != QueuePending {
			return apperr.Conflict("QUEUE_ROW_NOT_OPEN", "the row is applied or cancelled already").WithContext("status", r.Status)
		}
		if err := s.q(ctx).SetConsumptionCancelled(ctx, inventorydb.SetConsumptionCancelledParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.queue_row_cancelled", "inv_consumption_queue", id, fmt.Sprintf("queue row %d", id), map[string]any{"status": r.Status}, map[string]any{"status": QueueCancelled}))
	})
	if err != nil {
		return QueueRow{}, err
	}
	return s.queueRow(ctx, p, propertyID, id)
}

func (s *Service) queueRow(ctx context.Context, p auth.Principal, propertyID, id int64) (QueueRow, error) {
	rows, err := s.q(ctx).ListConsumptionQueue(ctx, inventorydb.ListConsumptionQueueParams{TenantID: p.TenantID, PropertyID: propertyID, ID: &id,
		Statuses: []string{QueuePending, QueueHeld, QueueApplied, QueueCancelled, QueueFailed}, RowLimit: 1})
	if err != nil {
		return QueueRow{}, err
	}
	if len(rows) == 0 {
		return QueueRow{}, notFound("QUEUE_ROW_NOT_FOUND", "queue row")
	}
	return toQueueRow(rows[0]), nil
}

func toQueueRow(r inventorydb.ListConsumptionQueueRow) QueueRow {
	return QueueRow{ID: r.ID, Source: r.Source, OutletID: r.OutletID, OutletCode: r.OutletCode, OutletName: r.OutletName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName, BillLineID: r.PosBillLineID,
		BillNumber: r.BillNumber, Kind: r.Kind, ProductID: r.ProductID, ProductName: r.ProductName, Quantity: r.Quantity, SaleClass: r.SaleClass, SaleDate: r.SaleDate, Status: r.Status,
		Attempts: int(r.Attempts), ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage, CreatedAt: r.CreatedAt, AppliedOn: r.AppliedOn}
}
