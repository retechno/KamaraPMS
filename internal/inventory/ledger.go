package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The ledger writer (35-inventory.md 5.1). It is the only code that inserts inv_movements and changes inv_balances, inv_layers, inv_layer_uses and inv_shortfalls
// (stockpath_guard_test.go reads the source and fails any other writer). It knows both costing methods; nothing else in the module does.
//
// A use case locks first and writes second: Ledger.Lock takes the warehouses (FOR SHARE) and the balance rows of every pair it will touch (FOR UPDATE, in one call, ascending id) and
// returns a *Stock; only a *Stock can write, so a write without the locks cannot be expressed. The methods are In, Out, Reverse and Adjust; the settlement of stock sold below zero
// (4.4) is part of In.

// Ledger is the factory of locked stock.
type Ledger struct{}

// Book is what the writer needs to know about the property and the day, read once by the use case: the open business day (from RequireOpenBusinessDay), the decimals of the currency
// and the costing method of the settings.
type Book struct {
	TenantID   int64
	PropertyID int64
	Date       civil.Date
	Decimals   int32
	Method     string
	ActorID    *int64
}

// Pair is one item in one warehouse: the unit of locking.
type Pair struct{ WarehouseID, ItemID int64 }

// Ref names the document line a movement comes from. A line is posted once per kind (the unique key of the ledger).
type Ref struct {
	Type       string // the source document, for example INV_OPENING
	ID         int64
	Line       int
	SourceDate *civil.Date // the day of the sale when the movement is made later (a POS sale applied after a count)
}

// Counter is the other side of the journal of a movement: the account the value goes to (an expense, the opening equity) and its department. The inventory account of the category
// is the one side of every movement and is not named by the caller.
type Counter struct {
	AccountID    int64
	DepartmentID *int64
}

// LockOptions says what a lock may do. AllowFrozen is for the writers that a freeze does not stop (the POS queue, 5.5); nothing in PR 1 sets it.
type LockOptions struct{ AllowFrozen bool }

// Stock is a set of balance rows that the transaction holds. It is the only writer.
type Stock struct {
	q     *inventorydb.Queries
	b     Book
	rows  map[Pair]*balRow
	items map[int64]itemInfo
	whs   map[int64]whInfo
}

type balRow struct {
	id           int64
	qty, value   decimal.Decimal
	lastUnitCost *decimal.Decimal
}

type itemInfo struct {
	referenceCost *decimal.Decimal
	itemClass     string
	invAccountID  int64
}

type whInfo struct {
	outletID *int64
}

// Posted is the result of a write.
type Posted struct {
	MovementID int64
	Qty, Value decimal.Decimal // signed as the ledger holds them
	UnitCost   decimal.Decimal
	Estimated  bool
	ShortQty   decimal.Decimal // the part of an OUT that the shelf did not have (AllowShortfall)
	ShortID    int64
	Settled    []Settlement
	// Slices are the layers an OUT took, oldest first (FIFO): what a transfer in copies so that the age of the goods is not lost (4.3).
	Slices []LayerSlice
}

// LayerSlice is a piece of a FIFO layer: the day the goods were received, the quantity and the value.
type LayerSlice struct {
	ReceivedOn civil.Date
	Qty, Value decimal.Decimal
}

// Settlement is the stock sold below zero that an IN settled.
type Settlement struct {
	ShortfallID int64
	MovementID  int64 // the NEG_SETTLE row; 0 when the real cost equalled the estimate and no row was needed
	Qty         decimal.Decimal
	Value       decimal.Decimal
}

// Lock takes the warehouses and the balance rows of the pairs and loads them. It must run inside a use case's transaction, after the business day and the documents of the use
// case (the order of 5.1: documents, warehouse, balances). A missing balance row is created first (it holds no lock that matters).
func (l *Ledger) Lock(ctx context.Context, q *inventorydb.Queries, b Book, pairs []Pair, opt LockOptions) (*Stock, error) {
	s := &Stock{q: q, b: b, rows: map[Pair]*balRow{}, items: map[int64]itemInfo{}, whs: map[int64]whInfo{}}
	if len(pairs) == 0 {
		return s, nil
	}
	pairs = slices.Clone(pairs)
	slices.SortFunc(pairs, func(a, c Pair) int {
		if a.WarehouseID != c.WarehouseID {
			return int(a.WarehouseID - c.WarehouseID)
		}
		return int(a.ItemID - c.ItemID)
	})
	pairs = slices.Compact(pairs)
	var wids, iids []int64
	for _, p := range pairs {
		wids = append(wids, p.WarehouseID)
		iids = append(iids, p.ItemID)
	}
	slices.Sort(wids)
	wids = slices.Compact(wids)
	if err := db.LockRows(ctx, db.Warehouses, db.ForShare, b.PropertyID, wids); err != nil {
		return nil, err
	}
	whs, err := q.LedgerWarehouseInfo(ctx, inventorydb.LedgerWarehouseInfoParams{TenantID: b.TenantID, PropertyID: b.PropertyID, Ids: wids})
	if err != nil {
		return nil, err
	}
	for _, w := range whs {
		if !w.IsActive {
			return nil, apperr.Conflict("WAREHOUSE_INACTIVE", "the warehouse is switched off").WithContext("warehouse_id", w.ID)
		}
		if w.CountID != nil && !opt.AllowFrozen {
			return nil, apperr.Conflict("WAREHOUSE_FROZEN", "a stock count is running in the warehouse").WithContext("warehouse_id", w.ID)
		}
		s.whs[w.ID] = whInfo{outletID: w.OutletID}
	}
	slices.Sort(iids)
	iids = slices.Compact(iids)
	items, err := q.LedgerItemInfo(ctx, inventorydb.LedgerItemInfoParams{TenantID: b.TenantID, PropertyID: b.PropertyID, Ids: iids})
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		s.items[it.ID] = itemInfo{referenceCost: it.ReferenceCost, itemClass: ClassOfKind(it.CategoryKind), invAccountID: it.InventoryAccountID}
	}
	var ids []int64
	for _, p := range pairs {
		if _, ok := s.whs[p.WarehouseID]; !ok {
			return nil, apperr.NotFound("WAREHOUSE_NOT_FOUND", "the warehouse does not exist in this property")
		}
		if _, ok := s.items[p.ItemID]; !ok {
			return nil, apperr.NotFound("ITEM_NOT_FOUND", "the item does not exist in this property")
		}
		if err := q.LedgerEnsureBalance(ctx, inventorydb.LedgerEnsureBalanceParams{TenantID: b.TenantID, PropertyID: b.PropertyID, WarehouseID: p.WarehouseID, ItemID: p.ItemID}); err != nil {
			return nil, err
		}
		id, err := q.LedgerBalanceID(ctx, inventorydb.LedgerBalanceIDParams{TenantID: b.TenantID, PropertyID: b.PropertyID, WarehouseID: p.WarehouseID, ItemID: p.ItemID})
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := db.LockRows(ctx, db.StockBalances, db.ForUpdate, b.PropertyID, ids); err != nil {
		return nil, err
	}
	rows, err := q.LedgerGetBalances(ctx, inventorydb.LedgerGetBalancesParams{TenantID: b.TenantID, PropertyID: b.PropertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		s.rows[Pair{r.WarehouseID, r.ItemID}] = &balRow{id: r.ID, qty: r.Qty, value: r.Value, lastUnitCost: r.LastUnitCost}
	}
	return s, nil
}

// Balance is the quantity and the value of a locked pair as the writer holds them now.
func (s *Stock) Balance(p Pair) (qty, value decimal.Decimal, ok bool) {
	r, found := s.rows[p]
	if !found {
		return decimal.Zero, decimal.Zero, false
	}
	return r.qty, r.value, true
}

func (s *Stock) row(p Pair) (*balRow, error) {
	r, ok := s.rows[p]
	if !ok {
		return nil, apperr.Internal(fmt.Errorf("inventory: the pair (warehouse %d, item %d) was not locked before it was written", p.WarehouseID, p.ItemID))
	}
	return r, nil
}

func (s *Stock) round(d decimal.Decimal) decimal.Decimal { return d.Round(s.b.Decimals) }

// InArgs is goods coming into a warehouse.
type InArgs struct {
	Pair
	Kind      string
	Qty       decimal.Decimal // stock units, > 0
	Value     decimal.Decimal // the money of the whole quantity, >= 0
	Ref       Ref
	Counter   *Counter // nil: the movement has no journal (a transfer)
	CostClass string   // default: the class of the item
	OutletID  *int64   // default: the outlet of the warehouse
	// ReceivedOn dates the FIFO layer; default the open day.
	ReceivedOn *civil.Date
	// Layers are the pieces of the layers a transfer out took: the transfer in makes one layer for each, with the same cost and the same date (4.3). Without them the goods make one layer.
	Layers []LayerSlice
}

// OutArgs is goods leaving a warehouse.
type OutArgs struct {
	Pair
	Kind      string
	Qty       decimal.Decimal // stock units, > 0
	Ref       Ref
	Counter   *Counter
	CostClass string
	OutletID  *int64
	// allowShortfall lets the quantity go below zero (4.4): only a POS sale and the amenities of the close, through AllowShortfall. A manual issue, a transfer, a waste, a
	// staff meal, an entertainment, a return to supplier and a count refuse.
	allowShortfall  bool
	shortfallSource string
	// basis values the goods at the cost of a receipt line, not at the average or the oldest layers (a return to the supplier, 4.6); set by ReturnAt.
	basis *returnBasis
}

type returnBasis struct {
	movementID int64
	qty, value decimal.Decimal
}

// ReturnAt values an OUT at the cost that a receipt line created (4.6): under the moving average the share of the line's value for the quantity returned, under FIFO the layer of that line first and then the
// oldest. The structural guard lists its callers like those of AllowShortfall.
func ReturnAt(a OutArgs, receiptMovementID int64, lineQty, lineValue decimal.Decimal) OutArgs {
	a.basis = &returnBasis{movementID: receiptMovementID, qty: lineQty, value: lineValue}
	return a
}

// AllowShortfall marks an OUT as one that may take the balance below zero, for the given source (POS_SALE or AMENITY). The structural guard lists its callers (stockpath_guard_test.go).
func AllowShortfall(a OutArgs, source string) OutArgs {
	a.allowShortfall, a.shortfallSource = true, source
	return a
}

func (s *Stock) classes(p Pair, cost string) (item, costClass string) {
	item = s.items[p.ItemID].itemClass
	costClass = cost
	if costClass == "" {
		costClass = item
	}
	return item, costClass
}

func (s *Stock) outlet(p Pair, o *int64) *int64 {
	if o != nil {
		return o
	}
	return s.whs[p.WarehouseID].outletID
}

// journal gives the accounts of a movement of the given value: the value of the stock rises (Dr inventory / Cr the counter account) or falls (Dr the counter account / Cr inventory).
func (s *Stock) journal(p Pair, c *Counter, value decimal.Decimal) (debit, credit *int64, dept *int64, amount decimal.Decimal) {
	if c == nil || value.IsZero() {
		return nil, nil, nil, decimal.Zero
	}
	inv, other := s.items[p.ItemID].invAccountID, c.AccountID
	if value.IsPositive() {
		return &inv, &other, c.DepartmentID, value
	}
	return &other, &inv, c.DepartmentID, value.Neg()
}

type movementOut struct {
	pair          Pair
	kind          string
	qty, value    decimal.Decimal
	ref           Ref
	counter       *Counter
	costClass     string
	outlet        *int64
	estimated     bool
	reverses      *int64
	debit, credit *int64 // set to override the accounts of a reversal
	dept          *int64
	glAmount      *decimal.Decimal
}

func (s *Stock) insert(ctx context.Context, q *inventorydb.Queries, m movementOut) (int64, error) {
	itemClass, costClass := s.classes(m.pair, m.costClass)
	debit, credit, dept, amount := m.debit, m.credit, m.dept, decimal.Zero
	if m.glAmount != nil {
		amount = *m.glAmount
	}
	if debit == nil && credit == nil && m.counter != nil {
		debit, credit, dept, amount = s.journal(m.pair, m.counter, m.value)
	}
	line := m.ref.Line
	if line < 1 {
		line = 1
	}
	id, err := q.LedgerInsertMovement(ctx, inventorydb.LedgerInsertMovementParams{
		TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: m.pair.WarehouseID, ItemID: m.pair.ItemID, Kind: m.kind,
		QtyDelta: m.qty, ValueDelta: m.value, UnitCost: UnitCost(m.value, m.qty), CostEstimated: m.estimated, BusinessDate: s.b.Date, SourceDate: m.ref.SourceDate,
		SourceType: m.ref.Type, SourceID: m.ref.ID, SourceLine: int32(line), ReversesID: m.reverses,
		GlDebitAccountID: debit, GlCreditAccountID: credit, GlAmount: amount, GlDepartmentID: dept,
		ItemClass: itemClass, CostClass: costClass, OutletID: s.outlet(m.pair, m.outlet), ActorID: s.b.ActorID,
	})
	if err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Stock) save(ctx context.Context, q *inventorydb.Queries, p Pair, r *balRow) error {
	return q.LedgerUpdateBalance(ctx, inventorydb.LedgerUpdateBalanceParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: r.id, Qty: r.qty, Value: r.value, LastUnitCost: r.lastUnitCost})
}

// In books goods into a warehouse. The value is the money of the whole quantity; the writer rounds it to the currency and the quantity to six decimals. A receipt and a transfer in
// first settle the stock that was sold below zero, oldest first (4.4): the covered quantity makes no layer under FIFO, and the difference of cost goes in a NEG_SETTLE row.
func (s *Stock) In(ctx context.Context, a InArgs) (Posted, error) {
	q := s.q
	if !inKinds[a.Kind] {
		return Posted{}, apperr.Internal(fmt.Errorf("inventory: %s is not a kind of goods coming in", a.Kind))
	}
	qty, val := Round6(a.Qty), s.round(a.Value)
	if !qty.IsPositive() || val.IsNegative() {
		return Posted{}, apperr.Invalid("the movement is invalid", apperr.FieldError{Field: "qty", Code: "INVALID_VALUE", Message: "a quantity above zero and a value of zero or more"})
	}
	r, err := s.row(a.Pair)
	if err != nil {
		return Posted{}, err
	}
	unit := UnitCost(val, qty)
	id, err := s.insert(ctx, q, movementOut{pair: a.Pair, kind: a.Kind, qty: qty, value: val, ref: a.Ref, counter: a.Counter, costClass: a.CostClass, outlet: a.OutletID})
	if err != nil {
		return Posted{}, err
	}
	out := Posted{MovementID: id, Qty: qty, Value: val, UnitCost: unit}
	covered, coveredCost := decimal.Zero, decimal.Zero
	settleDelta := decimal.Zero
	if settlesShortfalls[a.Kind] && r.qty.IsNegative() {
		open, err := q.LedgerOpenShortfalls(ctx, inventorydb.LedgerOpenShortfallsParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: a.WarehouseID, ItemID: a.ItemID})
		if err != nil {
			return Posted{}, err
		}
		remaining := qty
		for _, sf := range open {
			if !remaining.IsPositive() {
				break
			}
			k := decimal.Min(sf.QtyOpen, remaining)
			remaining = remaining.Sub(k)
			cum := covered.Add(k)
			// the cost of the covered part is cut from the value of the receipt without a drift: the cumulative share, rounded, less what the earlier shortfalls took
			cost := s.round(val.Mul(cum).Div(qty)).Sub(coveredCost)
			covered, coveredCost = cum, coveredCost.Add(cost)
			est := sf.EstValueOpen
			if k.LessThan(sf.QtyOpen) {
				est = s.round(sf.EstValueOpen.Mul(k).Div(sf.QtyOpen))
			}
			delta := est.Sub(cost) // -k x (c - e): negative when the goods cost more than the estimate
			st := Settlement{ShortfallID: sf.ID, Qty: k, Value: delta}
			if !delta.IsZero() {
				inv := s.items[a.ItemID].invAccountID
				var debitAcc, creditAcc *int64
				amount := decimal.Zero
				if sf.GlDebitAccountID != nil {
					amount = delta.Abs()
					if delta.IsNegative() { // the goods cost more than the estimate: Dr cost of sales / Cr inventory
						debitAcc, creditAcc = sf.GlDebitAccountID, &inv
					} else { // the real cost was lower than the estimate: Dr inventory / Cr cost of sales
						debitAcc, creditAcc = &inv, sf.GlDebitAccountID
					}
				}
				mid, err := s.insert(ctx, q, movementOut{pair: a.Pair, kind: KindNegSettle, qty: decimal.Zero, value: delta, ref: Ref{Type: "INV_SETTLE", ID: id, Line: int(sf.ID)},
					costClass: sf.CostClass, outlet: sf.OutletID, debit: debitAcc, credit: creditAcc, dept: sf.GlDepartmentID, glAmount: &amount})
				if err != nil {
					return Posted{}, err
				}
				st.MovementID = mid
				settleDelta = settleDelta.Add(delta)
			}
			left := sf.QtyOpen.Sub(k)
			status := "OPEN"
			if left.IsZero() {
				status = "SETTLED"
			}
			if err := q.LedgerUpdateShortfall(ctx, inventorydb.LedgerUpdateShortfallParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: sf.ID, QtyOpen: left, EstValueOpen: sf.EstValueOpen.Sub(est), Status: status}); err != nil {
				return Posted{}, err
			}
			out.Settled = append(out.Settled, st)
		}
	}
	if s.b.Method == MethodFIFO {
		if layerQty := qty.Sub(covered); layerQty.IsPositive() {
			on := s.b.Date
			if a.ReceivedOn != nil {
				on = *a.ReceivedOn
			}
			pieces := []LayerSlice{{ReceivedOn: on, Qty: layerQty, Value: val.Sub(coveredCost)}}
			if len(a.Layers) > 0 {
				pieces = s.copyLayers(a.Layers, covered, val.Sub(coveredCost))
			}
			for _, sl := range pieces {
				if _, err := q.LedgerInsertLayer(ctx, inventorydb.LedgerInsertLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: a.WarehouseID, ItemID: a.ItemID,
					MovementID: id, ReceivedOn: sl.ReceivedOn, QtyIn: sl.Qty, QtyRemaining: sl.Qty, ValueRemaining: sl.Value}); err != nil {
					return Posted{}, err
				}
			}
		}
	}
	r.qty = r.qty.Add(qty)
	r.value = r.value.Add(val).Add(settleDelta)
	last := unit
	r.lastUnitCost = &last
	return out, s.save(ctx, q, a.Pair, r)
}

// copyLayers cuts the pieces of the layers that a transfer carries down to what the destination really holds: the quantity that settled a shortfall there (covered) takes the oldest pieces first, and the value
// that is left (total) is shared by the pieces in proportion, the last one taking the remainder, so that the layers add up to the value of the goods exactly.
func (s *Stock) copyLayers(in []LayerSlice, covered, total decimal.Decimal) []LayerSlice {
	left := covered
	var kept []LayerSlice
	for _, sl := range in {
		if left.IsPositive() {
			drop := decimal.Min(left, sl.Qty)
			left = left.Sub(drop)
			if drop.Equal(sl.Qty) {
				continue
			}
			sl.Value = s.round(sl.Value.Mul(sl.Qty.Sub(drop)).Div(sl.Qty))
			sl.Qty = sl.Qty.Sub(drop)
		}
		kept = append(kept, sl)
	}
	sum := decimal.Zero
	for _, sl := range kept {
		sum = sum.Add(sl.Qty)
	}
	remaining := total
	for i := range kept {
		if i == len(kept)-1 {
			kept[i].Value = remaining
			break
		}
		v := s.round(total.Mul(kept[i].Qty).Div(sum))
		kept[i].Value = v
		remaining = remaining.Sub(v)
	}
	return kept
}

// estimate is the unit cost of goods that were not there (4.4): the last unit cost of the balance, else the reference cost of the item, else the cost of the newest receipt of the
// item in any warehouse; with none of them, zero, and the movement is flagged so that it shows in a report.
func (s *Stock) estimate(ctx context.Context, q *inventorydb.Queries, p Pair, r *balRow) (cost decimal.Decimal, estimated bool, err error) {
	if r.lastUnitCost != nil {
		return *r.lastUnitCost, false, nil
	}
	if rc := s.items[p.ItemID].referenceCost; rc != nil {
		return *rc, false, nil
	}
	c, err := q.LedgerNewestReceiptCost(ctx, inventorydb.LedgerNewestReceiptCostParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ItemID: p.ItemID})
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, true, nil
	}
	if err != nil {
		return decimal.Zero, false, err
	}
	return c, false, nil
}

// Out takes goods out of a warehouse at the cost of the method: the moving average of the balance, or the oldest layers. Without AllowShortfall a quantity the shelf does not have is
// refused (409 NEGATIVE_STOCK_NOT_ALLOWED); with it, the part that is missing is costed at the estimate and recorded as a shortfall.
func (s *Stock) Out(ctx context.Context, a OutArgs) (Posted, error) {
	q := s.q
	if !outKinds[a.Kind] {
		return Posted{}, apperr.Internal(fmt.Errorf("inventory: %s is not a kind of goods going out", a.Kind))
	}
	qty := Round6(a.Qty)
	if !qty.IsPositive() {
		return Posted{}, apperr.Invalid("the movement is invalid", apperr.FieldError{Field: "qty", Code: "INVALID_VALUE", Message: "a quantity above zero"})
	}
	r, err := s.row(a.Pair)
	if err != nil {
		return Posted{}, err
	}
	available := decimal.Max(r.qty, decimal.Zero)
	if qty.GreaterThan(available) && !a.allowShortfall {
		return Posted{}, errNegativeStock(a.WarehouseID, a.ItemID, available, qty)
	}
	onHand := decimal.Min(qty, available)
	short := qty.Sub(onHand)
	var realValue decimal.Decimal
	var uses []layerUse
	switch {
	case !onHand.IsPositive():
	case s.b.Method == MethodFIFO:
		first := int64(0)
		if a.basis != nil {
			first = a.basis.movementID
		}
		realValue, uses, err = s.consume(ctx, q, a.Pair, onHand, first)
		if err != nil {
			return Posted{}, err
		}
	case a.basis != nil && a.basis.qty.IsPositive():
		realValue = s.round(onHand.Mul(a.basis.value).Div(a.basis.qty))
		if onHand.Equal(r.qty) || realValue.GreaterThan(r.value) {
			realValue = r.value // the balance never keeps a residue and never goes below zero in value
		}
	case onHand.Equal(r.qty):
		realValue = r.value // an OUT that empties the balance takes all the value that is left
	default:
		realValue = s.round(onHand.Mul(r.value).Div(r.qty))
	}
	estUnit, estimated, estValue := decimal.Zero, false, decimal.Zero
	if short.IsPositive() {
		estUnit, estimated, err = s.estimate(ctx, q, a.Pair, r)
		if err != nil {
			return Posted{}, err
		}
		estValue = s.round(short.Mul(estUnit))
	}
	total := realValue.Add(estValue)
	id, err := s.insert(ctx, q, movementOut{pair: a.Pair, kind: a.Kind, qty: qty.Neg(), value: total.Neg(), ref: a.Ref, counter: a.Counter, costClass: a.CostClass, outlet: a.OutletID, estimated: estimated})
	if err != nil {
		return Posted{}, err
	}
	for _, u := range uses {
		if err := q.LedgerInsertLayerUse(ctx, inventorydb.LedgerInsertLayerUseParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: id, LayerID: u.layerID, Qty: u.qty, Value: u.value}); err != nil {
			return Posted{}, err
		}
	}
	out := Posted{MovementID: id, Qty: qty.Neg(), Value: total.Neg(), UnitCost: UnitCost(total, qty), Estimated: estimated, ShortQty: short}
	for _, u := range uses {
		out.Slices = append(out.Slices, LayerSlice{ReceivedOn: u.receivedOn, Qty: u.qty, Value: u.value})
	}
	if short.IsPositive() {
		var debit, dept *int64
		if a.Counter != nil {
			acc := a.Counter.AccountID
			debit, dept = &acc, a.Counter.DepartmentID
		}
		_, costClass := s.classes(a.Pair, a.CostClass)
		sid, err := q.LedgerInsertShortfall(ctx, inventorydb.LedgerInsertShortfallParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: a.WarehouseID, ItemID: a.ItemID,
			MovementID: id, QtyOpen: short, EstUnitCost: estUnit, EstValueOpen: estValue, GlDebitAccountID: debit, GlDepartmentID: dept, SourceType: a.shortfallSource,
			CostClass: costClass, OutletID: s.outlet(a.Pair, a.OutletID), BusinessDate: s.b.Date})
		if err != nil {
			return Posted{}, err
		}
		out.ShortID = sid
	}
	r.qty = r.qty.Sub(qty)
	r.value = r.value.Sub(total)
	return out, s.save(ctx, q, a.Pair, r)
}

type layerUse struct {
	layerID    int64
	receivedOn civil.Date
	qty, value decimal.Decimal
}

// consume takes qty from the oldest layers of the pair and returns the value of what it took and the pieces (4.3).
// The layer of the receipt movement `first` (when it is not 0) is taken before the oldest ones: a return to the supplier takes back the goods of the receipt it returns.
func (s *Stock) consume(ctx context.Context, q *inventorydb.Queries, p Pair, qty decimal.Decimal, first int64) (decimal.Decimal, []layerUse, error) {
	layers, err := q.LedgerOpenLayers(ctx, inventorydb.LedgerOpenLayersParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: p.WarehouseID, ItemID: p.ItemID})
	if err != nil {
		return decimal.Zero, nil, err
	}
	if first != 0 {
		slices.SortStableFunc(layers, func(a, b inventorydb.LedgerOpenLayersRow) int {
			switch {
			case a.MovementID == first && b.MovementID != first:
				return -1
			case b.MovementID == first && a.MovementID != first:
				return 1
			}
			return 0
		})
	}
	remaining, total := qty, decimal.Zero
	var uses []layerUse
	for _, l := range layers {
		if !remaining.IsPositive() {
			break
		}
		take := decimal.Min(l.QtyRemaining, remaining)
		val := l.ValueRemaining // the last piece of a layer takes what is left
		if take.LessThan(l.QtyRemaining) {
			val = s.round(take.Mul(l.ValueRemaining).Div(l.QtyRemaining))
		}
		if err := q.LedgerUpdateLayer(ctx, inventorydb.LedgerUpdateLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: l.ID,
			QtyRemaining: l.QtyRemaining.Sub(take), ValueRemaining: l.ValueRemaining.Sub(val)}); err != nil {
			return decimal.Zero, nil, err
		}
		uses = append(uses, layerUse{layerID: l.ID, receivedOn: l.ReceivedOn, qty: take, value: val})
		remaining = remaining.Sub(take)
		total = total.Add(val)
	}
	if remaining.IsPositive() {
		return decimal.Zero, nil, apperr.Internal(fmt.Errorf("inventory: the layers of warehouse %d item %d hold %s less than the balance", p.WarehouseID, p.ItemID, remaining))
	}
	return total, uses, nil
}

// ReverseArgs undoes one movement. Kind is the kind of the reversing row (a reversal of goods in is goods out and the other way round).
type ReverseArgs struct {
	MovementID int64
	Kind       string
	Ref        Ref
}

func errNotReversible(why string, id int64) *apperr.Error {
	return apperr.Conflict("MOVEMENT_NOT_REVERSIBLE", "the movement cannot be reversed: "+why).WithContext("movement_id", id)
}

// Reverse writes the row that undoes a movement, once: the quantity and the value negated, the accounts swapped, reverses_id set. The cases it takes are the simple ones: goods that
// came in and are all still on the shelf (and settled nothing), and goods that went out whose shortfall, if any, nobody has settled. Anything else is refused with
// MOVEMENT_NOT_REVERSIBLE: the later PRs add the cases that a use case needs.
func (s *Stock) Reverse(ctx context.Context, a ReverseArgs) (Posted, error) {
	q := s.q
	m, err := q.LedgerGetMovement(ctx, inventorydb.LedgerGetMovementParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: a.MovementID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Posted{}, apperr.NotFound("MOVEMENT_NOT_FOUND", "the movement does not exist in this property")
	}
	if err != nil {
		return Posted{}, err
	}
	p := Pair{m.WarehouseID, m.ItemID}
	r, err := s.row(p)
	if err != nil {
		return Posted{}, err
	}
	if done, err := q.LedgerMovementReversed(ctx, inventorydb.LedgerMovementReversedParams{PropertyID: s.b.PropertyID, ID: &m.ID}); err != nil {
		return Posted{}, err
	} else if done {
		return Posted{}, apperr.Conflict("MOVEMENT_ALREADY_REVERSED", "the movement was reversed already").WithContext("movement_id", m.ID)
	}
	if m.QtyDelta.IsZero() || m.ReversesID != nil {
		return Posted{}, errNotReversible("it moves no goods or it is itself a reversal", m.ID)
	}
	qty, value := m.QtyDelta.Neg(), m.ValueDelta.Neg()
	out := Posted{Qty: qty}
	switch {
	case m.QtyDelta.IsPositive(): // goods that came in go out again
		if !outKinds[a.Kind] {
			return Posted{}, apperr.Internal(fmt.Errorf("inventory: %s does not reverse goods coming in", a.Kind))
		}
		if n, err := q.LedgerCountSettlementsOf(ctx, inventorydb.LedgerCountSettlementsOfParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: m.ID}); err != nil {
			return Posted{}, err
		} else if n > 0 {
			return Posted{}, errNotReversible("it settled stock that was sold below zero", m.ID)
		}
		if s.b.Method != MethodFIFO && r.qty.LessThan(m.QtyDelta) {
			return Posted{}, errNegativeStock(p.WarehouseID, p.ItemID, decimal.Max(r.qty, decimal.Zero), m.QtyDelta)
		}
		if s.b.Method == MethodFIFO {
			layers, err := q.LedgerListLayersOfMovement(ctx, inventorydb.LedgerListLayersOfMovementParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: m.ID})
			if err != nil {
				return Posted{}, err
			}
			if len(layers) == 0 {
				return Posted{}, errNotReversible("part of its layer was used", m.ID)
			}
			value = decimal.Zero
			for _, layer := range layers {
				if !layer.QtyRemaining.Equal(layer.QtyIn) {
					return Posted{}, errNotReversible("part of its layer was used", m.ID)
				}
				value = value.Sub(layer.ValueRemaining)
			}
			for _, layer := range layers {
				if err := q.LedgerUpdateLayer(ctx, inventorydb.LedgerUpdateLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: layer.ID, QtyRemaining: decimal.Zero, ValueRemaining: decimal.Zero}); err != nil {
					return Posted{}, err
				}
			}
		} else if r.qty.Equal(m.QtyDelta) {
			value = r.value.Neg() // it empties the balance: all the value that is left goes with it
		}
	default: // goods that went out come back
		if !inKinds[a.Kind] {
			return Posted{}, apperr.Internal(fmt.Errorf("inventory: %s does not reverse goods going out", a.Kind))
		}
		// the part of the quantity that was sold below zero has its shortfall cancelled: it has nothing on a layer to go back to
		sf, err := q.LedgerGetShortfallOfMovement(ctx, inventorydb.LedgerGetShortfallOfMovementParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: m.ID})
		switch {
		case err == nil:
			if sf.Status != "OPEN" || !sf.QtyOpen.Equal(sf.QtyOriginal) {
				return Posted{}, errNotReversible("its shortfall was settled", m.ID)
			}
			if err := q.LedgerUpdateShortfall(ctx, inventorydb.LedgerUpdateShortfallParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: sf.ID, QtyOpen: decimal.Zero, EstValueOpen: decimal.Zero, Status: "SETTLED"}); err != nil {
				return Posted{}, err
			}
		case errors.Is(err, pgx.ErrNoRows):
		default:
			return Posted{}, err
		}
		if s.b.Method == MethodFIFO {
			uses, err := q.LedgerListLayerUses(ctx, inventorydb.LedgerListLayerUsesParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: m.ID})
			if err != nil {
				return Posted{}, err
			}
			ids := make([]int64, 0, len(uses))
			for _, u := range uses {
				ids = append(ids, u.LayerID)
			}
			layers, err := q.LedgerGetLayersByID(ctx, inventorydb.LedgerGetLayersByIDParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, Ids: ids})
			if err != nil {
				return Posted{}, err
			}
			byID := map[int64]inventorydb.LedgerGetLayersByIDRow{}
			for _, l := range layers {
				byID[l.ID] = l
			}
			for _, u := range uses { // the quantity goes back into the layers it came from, with the value that was taken
				l := byID[u.LayerID]
				if err := q.LedgerUpdateLayer(ctx, inventorydb.LedgerUpdateLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: l.ID,
					QtyRemaining: l.QtyRemaining.Add(u.Qty), ValueRemaining: l.ValueRemaining.Add(u.Value)}); err != nil {
					return Posted{}, err
				}
			}
		}
	}
	var debit, credit *int64
	amount := m.GlAmount
	if m.GlDebitAccountID != nil {
		debit, credit = m.GlCreditAccountID, m.GlDebitAccountID // the accounts swap
		if !value.Equal(m.ValueDelta.Neg()) {
			amount = value.Abs()
		}
	}
	id, err := s.insert(ctx, q, movementOut{pair: p, kind: a.Kind, qty: qty, value: value, ref: a.Ref, costClass: m.CostClass, outlet: m.OutletID, estimated: m.CostEstimated,
		reverses: &m.ID, debit: debit, credit: credit, dept: m.GlDepartmentID, glAmount: &amount})
	if err != nil {
		return Posted{}, err
	}
	out.MovementID, out.Value, out.UnitCost = id, value, UnitCost(value, qty)
	r.qty = r.qty.Add(qty)
	r.value = r.value.Add(value)
	if r.qty.IsZero() {
		r.value = decimal.Zero
	}
	return out, s.save(ctx, q, p, r)
}

// VarianceArgs books a difference of cost that no goods carry any more (the part of a bill variance that is not stock, the purchase price variance): a COST_ADJUST row with no quantity and no value
// in the balance, only the journal of the close. A positive amount is Dr the variance account / Cr the clearing account (the bill cost more than the receipt), a negative one the other way.
type VarianceArgs struct {
	Pair
	Amount    decimal.Decimal // signed
	Ref       Ref
	Variance  int64 // the account of the purchase price variance
	Clearing  int64 // goods received not invoiced
	Dept      *int64
	CostClass string
}

// Variance writes the row. The pair must have been locked (the row names the warehouse and the item; the balance does not change).
func (s *Stock) Variance(ctx context.Context, a VarianceArgs) (Posted, error) {
	amount := s.round(a.Amount)
	if amount.IsZero() {
		return Posted{}, apperr.Invalid("the movement is invalid", apperr.FieldError{Field: "amount", Code: "INVALID_VALUE", Message: "not zero"})
	}
	if _, err := s.row(a.Pair); err != nil {
		return Posted{}, err
	}
	debit, credit := a.Variance, a.Clearing
	if amount.IsNegative() {
		debit, credit = a.Clearing, a.Variance
	}
	abs := amount.Abs()
	id, err := s.insert(ctx, s.q, movementOut{pair: a.Pair, kind: KindCostAdjust, qty: decimal.Zero, value: decimal.Zero, ref: a.Ref, costClass: a.CostClass, debit: &debit, credit: &credit, dept: a.Dept, glAmount: &abs})
	if err != nil {
		return Posted{}, err
	}
	return Posted{MovementID: id, Value: amount}, nil
}

// ReverseAdjust undoes a COST_ADJUST row, whatever part of a variance it carried (unmatching a bill, 5.3.7): a row that moved the value of the goods on hand takes it back (the balance, and under FIFO the layer of the
// receipt `forMovementID`, must still hold it), a row that only carried the journal of the purchase price variance is mirrored. The reversing row has the accounts swapped and reverses_id set.
func (s *Stock) ReverseAdjust(ctx context.Context, movementID, forMovementID int64, ref Ref) (Posted, error) {
	q := s.q
	m, err := q.LedgerGetMovement(ctx, inventorydb.LedgerGetMovementParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: movementID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Posted{}, apperr.NotFound("MOVEMENT_NOT_FOUND", "the movement does not exist in this property")
	}
	if err != nil {
		return Posted{}, err
	}
	if m.Kind != KindCostAdjust || m.ReversesID != nil || !m.QtyDelta.IsZero() {
		return Posted{}, errNotReversible("it is not a difference of cost", m.ID)
	}
	if done, err := q.LedgerMovementReversed(ctx, inventorydb.LedgerMovementReversedParams{PropertyID: s.b.PropertyID, ID: &m.ID}); err != nil {
		return Posted{}, err
	} else if done {
		return Posted{}, apperr.Conflict("MOVEMENT_ALREADY_REVERSED", "the movement was reversed already").WithContext("movement_id", m.ID)
	}
	p := Pair{m.WarehouseID, m.ItemID}
	r, err := s.row(p)
	if err != nil {
		return Posted{}, err
	}
	value := m.ValueDelta.Neg()
	if !value.IsZero() {
		if r.value.Add(value).IsNegative() || (!r.qty.IsPositive() && !r.value.Add(value).IsZero()) {
			return Posted{}, errNotReversible("the goods that took the difference of cost have been used", m.ID)
		}
		if s.b.Method == MethodFIFO {
			layer, err := q.LedgerGetLayerOfMovement(ctx, inventorydb.LedgerGetLayerOfMovementParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: forMovementID})
			if err != nil || !layer.QtyRemaining.IsPositive() || layer.ValueRemaining.Add(value).IsNegative() {
				return Posted{}, errNotReversible("the layer of the receipt has been used", m.ID)
			}
			if err := q.LedgerUpdateLayer(ctx, inventorydb.LedgerUpdateLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: layer.ID, QtyRemaining: layer.QtyRemaining, ValueRemaining: layer.ValueRemaining.Add(value)}); err != nil {
				return Posted{}, err
			}
		}
	}
	amount := m.GlAmount
	id, err := s.insert(ctx, q, movementOut{pair: p, kind: KindCostAdjust, qty: decimal.Zero, value: value, ref: ref, costClass: m.CostClass, outlet: m.OutletID, reverses: &m.ID,
		debit: m.GlCreditAccountID, credit: m.GlDebitAccountID, dept: m.GlDepartmentID, glAmount: &amount})
	if err != nil {
		return Posted{}, err
	}
	r.value = r.value.Add(value)
	return Posted{MovementID: id, Value: value}, s.save(ctx, q, p, r)
}

// AdjustArgs moves the value of the goods on hand without moving the quantity (a bill whose price differs from the receipt, 5.3.4). ForMovementID names the receipt whose layer is
// revalued under FIFO.
type AdjustArgs struct {
	Pair
	Value         decimal.Decimal // signed
	Ref           Ref
	Counter       *Counter
	ForMovementID int64
}

// Adjust writes a COST_ADJUST row. Under the moving average the value of the balance changes; under FIFO the value of the layer of the receipt. It refuses (409
// COST_ADJUST_NOT_APPLICABLE) when no goods of that receipt are on hand: the caller then takes the whole difference to the variance account.
func (s *Stock) Adjust(ctx context.Context, a AdjustArgs) (Posted, error) {
	q := s.q
	val := s.round(a.Value)
	if val.IsZero() {
		return Posted{}, apperr.Invalid("the movement is invalid", apperr.FieldError{Field: "value", Code: "INVALID_VALUE", Message: "not zero"})
	}
	r, err := s.row(a.Pair)
	if err != nil {
		return Posted{}, err
	}
	notApplicable := apperr.Conflict("COST_ADJUST_NOT_APPLICABLE", "no goods of this receipt are on hand to take the difference of cost").WithContext("item_id", a.ItemID)
	if !r.qty.IsPositive() {
		return Posted{}, notApplicable
	}
	if s.b.Method == MethodFIFO {
		layer, err := q.LedgerGetLayerOfMovement(ctx, inventorydb.LedgerGetLayerOfMovementParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, MovementID: a.ForMovementID})
		if err != nil || !layer.QtyRemaining.IsPositive() || layer.ValueRemaining.Add(val).IsNegative() {
			return Posted{}, notApplicable
		}
		if err := q.LedgerUpdateLayer(ctx, inventorydb.LedgerUpdateLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, ID: layer.ID, QtyRemaining: layer.QtyRemaining, ValueRemaining: layer.ValueRemaining.Add(val)}); err != nil {
			return Posted{}, err
		}
	}
	if r.value.Add(val).IsNegative() {
		return Posted{}, notApplicable
	}
	id, err := s.insert(ctx, q, movementOut{pair: a.Pair, kind: KindCostAdjust, qty: decimal.Zero, value: val, ref: a.Ref, counter: a.Counter})
	if err != nil {
		return Posted{}, err
	}
	r.value = r.value.Add(val)
	return Posted{MovementID: id, Value: val}, s.save(ctx, q, a.Pair, r)
}

// Gain books the goods a count found that the books did not have (4.7): an IN of the quantity at the last unit cost of the balance (the estimate of 4.4 when it has none). It ends the shortfalls of the pair
// without a settlement (4.4 item 4): what was sold below zero is covered by the goods that were really there, so under FIFO the layer holds only what is on hand after them and the balance equals the
// quantity that was counted. The kind is COUNT_GAIN; the pair is locked.
func (s *Stock) Gain(ctx context.Context, p Pair, qty decimal.Decimal, ref Ref, counter *Counter) (Posted, error) {
	q := s.q
	qty = Round6(qty)
	if !qty.IsPositive() {
		return Posted{}, apperr.Invalid("the movement is invalid", apperr.FieldError{Field: "qty", Code: "INVALID_VALUE", Message: "a quantity above zero"})
	}
	r, err := s.row(p)
	if err != nil {
		return Posted{}, err
	}
	cost, estimated, err := s.estimate(ctx, q, p, r)
	if err != nil {
		return Posted{}, err
	}
	val := s.round(qty.Mul(cost))
	openQty, err := q.LedgerOpenShortfallQty(ctx, inventorydb.LedgerOpenShortfallQtyParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: p.WarehouseID, ItemID: p.ItemID})
	if err != nil {
		return Posted{}, err
	}
	id, err := s.insert(ctx, q, movementOut{pair: p, kind: KindCountGain, qty: qty, value: val, ref: ref, counter: counter, estimated: estimated})
	if err != nil {
		return Posted{}, err
	}
	if s.b.Method == MethodFIFO {
		layerQty, layerValue := qty.Sub(openQty), val
		if openQty.IsPositive() { // the layers hold nothing while stock is owed: what the balance owes in value is taken from the gain
			layerValue = decimal.Max(r.value.Add(val), decimal.Zero)
		}
		if layerQty.IsPositive() {
			if _, err := q.LedgerInsertLayer(ctx, inventorydb.LedgerInsertLayerParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: p.WarehouseID, ItemID: p.ItemID,
				MovementID: id, ReceivedOn: s.b.Date, QtyIn: layerQty, QtyRemaining: layerQty, ValueRemaining: layerValue}); err != nil {
				return Posted{}, err
			}
		}
	}
	if openQty.IsPositive() {
		if err := q.LedgerCloseShortfallsByCount(ctx, inventorydb.LedgerCloseShortfallsByCountParams{TenantID: s.b.TenantID, PropertyID: s.b.PropertyID, WarehouseID: p.WarehouseID, ItemID: p.ItemID}); err != nil {
			return Posted{}, err
		}
	}
	r.qty = r.qty.Add(qty)
	r.value = r.value.Add(val)
	last := cost
	r.lastUnitCost = &last
	if r.qty.IsZero() {
		r.value = decimal.Zero
	}
	return Posted{MovementID: id, Qty: qty, Value: val, UnitCost: cost, Estimated: estimated}, s.save(ctx, q, p, r)
}

// ExpectedQty is the quantity of a locked pair as the writer holds it now: what a count compares the counted quantity with after the held rows before it were applied.
func (s *Stock) ExpectedQty(p Pair) decimal.Decimal {
	if r, ok := s.rows[p]; ok {
		return r.qty
	}
	return decimal.Zero
}
