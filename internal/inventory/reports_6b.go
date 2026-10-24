package inventory

import (
	"context"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/civil"
)

// The four reports of PR 6b (35-inventory.md 12.5, section 22.5): the stock movement per item, the purchases per supplier with the price history of an item, the slow and dead stock, and the largest usage.
// They only read the ledger and the receipts; the figures are summed with decimals, and a percentage is rounded to two places when it is answered.

// A query answers 1900-01-01 for a date that does not exist (the generated code cannot hold a NULL date): a year before epochYear is no date.
const epochYear = 1901

func missingDate(d civil.Date) bool { return d.IsZero() || d.Year() < epochYear }

// ---------------------------------------------------------------- the stock movement per item (B9)

// The buckets of the movement report. The first four are what came in, the next seven what went out (answered as positive amounts), then the two adjustments (signed).
const (
	MoveOpeningStock   = "OPENING_STOCK"
	MoveReceipts       = "RECEIPTS"
	MoveTransfersIn    = "TRANSFERS_IN"
	MoveSalesReturns   = "SALES_RETURNS"
	MovePosSales       = "POS_SALES"
	MoveUsage          = "USAGE"
	MoveWaste          = "WASTE"
	MoveStaffMeals     = "STAFF_MEALS"
	MoveEntertainment  = "ENTERTAINMENT"
	MoveTransfersOut   = "TRANSFERS_OUT"
	MoveSupplierReturn = "SUPPLIER_RETURNS"
	MoveCountVariance  = "COUNT_VARIANCE"
	MoveCostAdjust     = "COST_ADJUST"
)

var (
	// bucketsIn and bucketsOut keep the order the page shows them in.
	bucketsIn  = []string{MoveOpeningStock, MoveReceipts, MoveTransfersIn, MoveSalesReturns}
	bucketsOut = []string{MovePosSales, MoveUsage, MoveWaste, MoveStaffMeals, MoveEntertainment, MoveTransfersOut, MoveSupplierReturn}
	// bucketOfKind puts each kind of the ledger in a bucket. A reversal has the kind of the document it reverses, so it nets inside its bucket; a kind that is not listed (a cost correction, the cost of a dish
	// thrown away after a void) goes with the cost adjustments, so that nothing of the ledger is left out of the sum.
	bucketOfKind = map[string]string{
		KindOpening: MoveOpeningStock, KindReceipt: MoveReceipts, KindReceiptReversal: MoveReceipts, KindTransferIn: MoveTransfersIn, KindPosSaleReturn: MoveSalesReturns,
		KindPosSale: MovePosSales, KindIssue: MoveUsage, KindWaste: MoveWaste, KindStaffMeal: MoveStaffMeals, KindEntertainment: MoveEntertainment, KindTransferOut: MoveTransfersOut,
		KindSupplierReturn: MoveSupplierReturn, KindCountGain: MoveCountVariance, KindCountLoss: MoveCountVariance,
	}
)

func bucketOf(kind string) string {
	if b, ok := bucketOfKind[kind]; ok {
		return b
	}
	return MoveCostAdjust
}

func isOutBucket(b string) bool {
	for _, o := range bucketsOut {
		if o == b {
			return true
		}
	}
	return false
}

// QtyValue is a quantity (in the stock unit of the item) and its value.
type QtyValue struct {
	Qty   decimal.Decimal `json:"qty"`
	Value decimal.Decimal `json:"value"`
}

// ItemMovementRow is one item over the period: what it stood at before, each bucket, and what it stands at after. `buckets` has an entry for every bucket (the ones that went out as positive amounts), so that
// opening + the buckets in - the buckets out + the count variance + the cost adjustments = closing.
type ItemMovementRow struct {
	ItemID       int64               `json:"item_id"`
	ItemCode     string              `json:"item_code"`
	ItemName     string              `json:"item_name"`
	UnitCode     string              `json:"unit_code"`
	CategoryID   int64               `json:"category_id"`
	CategoryName string              `json:"category_name"`
	Opening      QtyValue            `json:"opening"`
	Buckets      map[string]QtyValue `json:"buckets"`
	Closing      QtyValue            `json:"closing"`
}

// ItemMovementTotal is a total by value (the quantities of items with other units do not add up): the opening, the value of each bucket and the closing.
type ItemMovementTotal struct {
	CategoryID   *int64                     `json:"category_id"`
	CategoryName string                     `json:"category_name"`
	Opening      decimal.Decimal            `json:"opening"`
	Buckets      map[string]decimal.Decimal `json:"buckets"`
	Closing      decimal.Decimal            `json:"closing"`
}

// ItemMovementFilter narrows the report.
type ItemMovementFilter struct {
	WarehouseID, CategoryID, ItemID *int64
}

// ItemMovementReport is the movement of the stock of every item that had a balance or a movement in the period, with the totals by category.
type ItemMovementReport struct {
	From       civil.Date          `json:"from"`
	To         civil.Date          `json:"to"`
	Rows       []ItemMovementRow   `json:"rows"`
	Categories []ItemMovementTotal `json:"categories"`
	Total      ItemMovementTotal   `json:"total"`
}

func newBuckets() map[string]QtyValue {
	m := map[string]QtyValue{}
	for _, b := range bucketsIn {
		m[b] = QtyValue{}
	}
	for _, b := range bucketsOut {
		m[b] = QtyValue{}
	}
	m[MoveCountVariance], m[MoveCostAdjust] = QtyValue{}, QtyValue{}
	return m
}

func newValueBuckets() map[string]decimal.Decimal {
	m := map[string]decimal.Decimal{}
	for b := range newBuckets() {
		m[b] = decimal.Zero
	}
	return m
}

func (t *ItemMovementTotal) add(r ItemMovementRow) {
	t.Opening, t.Closing = t.Opening.Add(r.Opening.Value), t.Closing.Add(r.Closing.Value)
	for b, v := range r.Buckets {
		t.Buckets[b] = t.Buckets[b].Add(v.Value)
	}
}

// ItemMovements answers the stock movement report (inventory.view). The opening and the movements come from the ledger; for an item in all the warehouses the warehouses are added.
func (s *Service) ItemMovements(ctx context.Context, propertyID int64, from, to *civil.Date, f ItemMovementFilter) (ItemMovementReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return ItemMovementReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return ItemMovementReport{}, err
	}
	rows, err := s.q(ctx).ItemMovementsReport(ctx, inventorydb.ItemMovementsReportParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, WarehouseID: f.WarehouseID, CategoryID: f.CategoryID, ItemID: f.ItemID})
	if err != nil {
		return ItemMovementReport{}, err
	}
	out := ItemMovementReport{From: fd, To: td, Rows: []ItemMovementRow{}, Categories: []ItemMovementTotal{}, Total: ItemMovementTotal{CategoryName: "", Buckets: newValueBuckets()}}
	at := map[int64]int{}
	for _, r := range rows {
		i, ok := at[r.ItemID]
		if !ok {
			i = len(out.Rows)
			at[r.ItemID] = i
			out.Rows = append(out.Rows, ItemMovementRow{ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode, CategoryID: r.CategoryID, CategoryName: r.CategoryName, Buckets: newBuckets()})
		}
		row := &out.Rows[i]
		row.Opening = QtyValue{Qty: row.Opening.Qty.Add(r.BeforeQty), Value: row.Opening.Value.Add(r.BeforeValue)}
		b := bucketOf(r.Kind)
		cur := row.Buckets[b]
		row.Buckets[b] = QtyValue{Qty: cur.Qty.Add(r.PeriodQty), Value: cur.Value.Add(r.PeriodValue)}
	}
	// the buckets hold the signed change of the ledger so far: what went out is turned into a positive amount, and the closing is the opening plus every change
	kept := out.Rows[:0]
	for _, row := range out.Rows {
		closing := row.Opening
		for b, v := range row.Buckets {
			closing = QtyValue{Qty: closing.Qty.Add(v.Qty), Value: closing.Value.Add(v.Value)}
			if isOutBucket(b) {
				row.Buckets[b] = QtyValue{Qty: v.Qty.Neg(), Value: v.Value.Neg()}
			}
		}
		row.Closing = closing
		if row.Opening.Qty.IsZero() && row.Opening.Value.IsZero() && row.Closing.Qty.IsZero() && row.Closing.Value.IsZero() && !anyBucket(row.Buckets) {
			continue
		}
		kept = append(kept, row)
	}
	out.Rows = kept
	catAt := map[int64]int{}
	for _, row := range out.Rows {
		i, ok := catAt[row.CategoryID]
		if !ok {
			i = len(out.Categories)
			catAt[row.CategoryID] = i
			id := row.CategoryID
			out.Categories = append(out.Categories, ItemMovementTotal{CategoryID: &id, CategoryName: row.CategoryName, Buckets: newValueBuckets()})
		}
		out.Categories[i].add(row)
		out.Total.add(row)
	}
	return out, nil
}

func anyBucket(m map[string]QtyValue) bool {
	for _, v := range m {
		if !v.Qty.IsZero() || !v.Value.IsZero() {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- purchases per supplier (B10)

// PurchaseFilter narrows the purchases report.
type PurchaseFilter struct {
	SupplierID, WarehouseID, CategoryID *int64
}

// PurchaseItem is what was bought of one item from one supplier.
type PurchaseItem struct {
	ItemID       int64            `json:"item_id"`
	ItemCode     string           `json:"item_code"`
	ItemName     string           `json:"item_name"`
	UnitCode     string           `json:"unit_code"`
	CategoryName string           `json:"category_name"`
	Qty          decimal.Decimal  `json:"qty"`
	Value        decimal.Decimal  `json:"value"`
	AvgPrice     *decimal.Decimal `json:"avg_price"`
	Receipts     int              `json:"receipts"`
	Bills        int              `json:"bills"`
	BilledValue  decimal.Decimal  `json:"billed_value"`
}

// PurchaseSupplier is the total of a supplier with its items.
type PurchaseSupplier struct {
	SupplierID   int64            `json:"supplier_id"`
	SupplierName string           `json:"supplier_name"`
	Value        decimal.Decimal  `json:"value"`
	SharePct     *decimal.Decimal `json:"share_pct"`
	Receipts     int              `json:"receipts"`
	Bills        int              `json:"bills"`
	BilledValue  decimal.Decimal  `json:"billed_value"`
	Items        []PurchaseItem   `json:"items"`
}

// PurchaseReport lists the purchases of a period by supplier and by item: the goods received that stand (a reversed receipt is out, what went back to the supplier is taken off), and the bills matched to them.
type PurchaseReport struct {
	From        civil.Date         `json:"from"`
	To          civil.Date         `json:"to"`
	Suppliers   []PurchaseSupplier `json:"suppliers"`
	Value       decimal.Decimal    `json:"value"`
	BilledValue decimal.Decimal    `json:"billed_value"`
	Receipts    int                `json:"receipts"`
	Bills       int                `json:"bills"`
}

// Purchases answers the report of purchases by supplier and by item (inventory.view).
func (s *Service) Purchases(ctx context.Context, propertyID int64, from, to *civil.Date, f PurchaseFilter) (PurchaseReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return PurchaseReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return PurchaseReport{}, err
	}
	q := s.q(ctx)
	lines, err := q.PurchaseLines(ctx, inventorydb.PurchaseLinesParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, SupplierID: f.SupplierID, WarehouseID: f.WarehouseID, CategoryID: f.CategoryID})
	if err != nil {
		return PurchaseReport{}, err
	}
	matches, err := q.PurchaseBillMatches(ctx, inventorydb.PurchaseBillMatchesParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, SupplierID: f.SupplierID, WarehouseID: f.WarehouseID, CategoryID: f.CategoryID})
	if err != nil {
		return PurchaseReport{}, err
	}
	out := PurchaseReport{From: fd, To: td, Suppliers: []PurchaseSupplier{}}
	type key struct{ supplier, item int64 }
	supAt, itemAt := map[int64]int{}, map[key]int{}
	supReceipts, supBills := map[int64]map[int64]bool{}, map[int64]map[int64]bool{}
	itemReceipts, itemBills := map[key]map[int64]bool{}, map[key]map[int64]bool{}
	allReceipts, allBills := map[int64]bool{}, map[int64]bool{}
	for _, r := range lines {
		i, ok := supAt[r.SupplierID]
		if !ok {
			i = len(out.Suppliers)
			supAt[r.SupplierID] = i
			out.Suppliers = append(out.Suppliers, PurchaseSupplier{SupplierID: r.SupplierID, SupplierName: r.SupplierName, Items: []PurchaseItem{}})
			supReceipts[r.SupplierID], supBills[r.SupplierID] = map[int64]bool{}, map[int64]bool{}
		}
		sp := &out.Suppliers[i]
		k := key{r.SupplierID, r.ItemID}
		j, ok := itemAt[k]
		if !ok {
			j = len(sp.Items)
			itemAt[k] = j
			sp.Items = append(sp.Items, PurchaseItem{ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode, CategoryName: r.CategoryName})
			itemReceipts[k], itemBills[k] = map[int64]bool{}, map[int64]bool{}
		}
		it := &sp.Items[j]
		it.Qty, it.Value = it.Qty.Add(r.Qty), it.Value.Add(r.Value)
		sp.Value, out.Value = sp.Value.Add(r.Value), out.Value.Add(r.Value)
		itemReceipts[k][r.ReceiptID], supReceipts[r.SupplierID][r.ReceiptID], allReceipts[r.ReceiptID] = true, true, true
	}
	for _, m := range matches {
		i, ok := supAt[m.SupplierID]
		if !ok {
			continue
		}
		k := key{m.SupplierID, m.ItemID}
		j, ok := itemAt[k]
		if !ok {
			continue
		}
		sp := &out.Suppliers[i]
		sp.Items[j].BilledValue, sp.BilledValue, out.BilledValue = sp.Items[j].BilledValue.Add(m.BillCost), sp.BilledValue.Add(m.BillCost), out.BilledValue.Add(m.BillCost)
		itemBills[k][m.BillID], supBills[m.SupplierID][m.BillID], allBills[m.BillID] = true, true, true
	}
	for i := range out.Suppliers {
		sp := &out.Suppliers[i]
		sp.Receipts, sp.Bills = len(supReceipts[sp.SupplierID]), len(supBills[sp.SupplierID])
		sp.SharePct = pct(sp.Value, out.Value)
		for j := range sp.Items {
			it := &sp.Items[j]
			k := key{sp.SupplierID, it.ItemID}
			it.Receipts, it.Bills = len(itemReceipts[k]), len(itemBills[k])
			it.AvgPrice = unitPrice(it.Value, it.Qty)
		}
		sort.SliceStable(sp.Items, func(a, b int) bool { return sp.Items[a].Value.GreaterThan(sp.Items[b].Value) })
	}
	out.Receipts, out.Bills = len(allReceipts), len(allBills)
	sort.SliceStable(out.Suppliers, func(a, b int) bool { return out.Suppliers[a].Value.GreaterThan(out.Suppliers[b].Value) })
	return out, nil
}

// PriceHistoryRow is one receipt of the item: the price per unit of purchase and per unit of stock, and the change from the receipt before it (of any supplier).
type PriceHistoryRow struct {
	ReceiptID        int64            `json:"receipt_id"`
	ReceiptNumber    string           `json:"receipt_number"`
	ReceivedOn       civil.Date       `json:"received_on"`
	SupplierID       int64            `json:"supplier_id"`
	SupplierName     string           `json:"supplier_name"`
	PurchaseUnitCode string           `json:"purchase_unit_code"`
	QtyPurchase      decimal.Decimal  `json:"qty_purchase"`
	UnitPrice        decimal.Decimal  `json:"unit_price"`
	PricePerStock    decimal.Decimal  `json:"price_per_stock"`
	ChangePct        *decimal.Decimal `json:"change_pct"`
}

// PriceHistoryReport is the price paid for an item over a period: every receipt, and the lowest, the highest, the last and the average price (weighted by the quantity) per unit of stock.
type PriceHistoryReport struct {
	From      civil.Date        `json:"from"`
	To        civil.Date        `json:"to"`
	ItemID    int64             `json:"item_id"`
	ItemCode  string            `json:"item_code"`
	ItemName  string            `json:"item_name"`
	UnitCode  string            `json:"unit_code"`
	Rows      []PriceHistoryRow `json:"rows"`
	MinPrice  *decimal.Decimal  `json:"min_price"`
	MaxPrice  *decimal.Decimal  `json:"max_price"`
	LastPrice *decimal.Decimal  `json:"last_price"`
	AvgPrice  *decimal.Decimal  `json:"avg_price"`
}

// PriceHistory answers the price history of an item (inventory.view); the item is required.
func (s *Service) PriceHistory(ctx context.Context, propertyID int64, itemID *int64, from, to *civil.Date, supplierID *int64) (PriceHistoryReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return PriceHistoryReport{}, err
	}
	if itemID == nil {
		return PriceHistoryReport{}, invalid("report", fieldErr("item_id", "REQUIRED", "the item whose prices are shown"))
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return PriceHistoryReport{}, err
	}
	q := s.q(ctx)
	item, err := q.PriceHistoryItem(ctx, inventorydb.PriceHistoryItemParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: *itemID})
	if err != nil {
		return PriceHistoryReport{}, orNotFound(err, notFound("ITEM_NOT_FOUND", "item"))
	}
	lines, err := q.PriceHistoryLines(ctx, inventorydb.PriceHistoryLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: *itemID, FromDate: fd, ToDate: td, SupplierID: supplierID})
	if err != nil {
		return PriceHistoryReport{}, err
	}
	out := PriceHistoryReport{From: fd, To: td, ItemID: item.ID, ItemCode: item.Code, ItemName: item.Name, UnitCode: item.UnitCode, Rows: []PriceHistoryRow{}}
	var totalQty, totalValue decimal.Decimal
	var prev *decimal.Decimal
	for _, l := range lines {
		perStock := l.Value.Div(l.QtyStock).Round(4)
		row := PriceHistoryRow{ReceiptID: l.ReceiptID, ReceiptNumber: l.ReceiptNumber, ReceivedOn: l.ReceivedOn, SupplierID: l.SupplierID, SupplierName: l.SupplierName, PurchaseUnitCode: l.PurchaseUnitCode,
			QtyPurchase: l.QtyPurchase, UnitPrice: l.UnitPrice, PricePerStock: perStock}
		if prev != nil {
			row.ChangePct = pct(perStock.Sub(*prev), *prev)
		}
		v := perStock
		prev = &v
		out.Rows = append(out.Rows, row)
		totalQty, totalValue = totalQty.Add(l.QtyStock), totalValue.Add(l.Value)
		if out.MinPrice == nil || perStock.LessThan(*out.MinPrice) {
			out.MinPrice = &v
		}
		if out.MaxPrice == nil || perStock.GreaterThan(*out.MaxPrice) {
			out.MaxPrice = &v
		}
		last := perStock
		out.LastPrice = &last
	}
	out.AvgPrice = unitPrice(totalValue, totalQty)
	return out, nil
}

// ---------------------------------------------------------------- slow and dead stock (B11)

// SlowStockDays are the windows the report offers.
var SlowStockDays = []int{30, 60, 90, 180}

// SlowStockFilter narrows the report.
type SlowStockFilter struct {
	WarehouseID, CategoryID *int64
}

// SlowStockRow is an item with stock that nothing consumed for the window. `days_since_out` is null (and `never_out` true) when nothing was ever consumed from the warehouse. The age of the stock is the age of the
// oldest layer that is left (FIFO) or of the last entry of goods (average).
type SlowStockRow struct {
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseCode string          `json:"warehouse_code"`
	WarehouseName string          `json:"warehouse_name"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	CategoryName  string          `json:"category_name"`
	Qty           decimal.Decimal `json:"qty"`
	Value         decimal.Decimal `json:"value"`
	LastOutOn     *civil.Date     `json:"last_out_on"`
	DaysSinceOut  *int            `json:"days_since_out"`
	NeverOut      bool            `json:"never_out"`
	AgeDays       *int            `json:"age_days"`
}

// SlowStockReport lists the items by how long they have not been used.
type SlowStockReport struct {
	AsOf          civil.Date      `json:"as_of"`
	Days          int             `json:"days"`
	CostingMethod string          `json:"costing_method"`
	AgeBasis      string          `json:"age_basis"`
	Rows          []SlowStockRow  `json:"rows"`
	Total         decimal.Decimal `json:"total"`
	NeverOut      int             `json:"never_out"`
}

// SlowStock answers the report of slow and dead stock (inventory.view): the items with stock whose last consumption is `days` days old or more, or that were never consumed. The day is the open business day.
func (s *Service) SlowStock(ctx context.Context, propertyID int64, days int, f SlowStockFilter) (SlowStockReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return SlowStockReport{}, err
	}
	if days == 0 {
		days = 90
	}
	okDays := false
	for _, d := range SlowStockDays {
		okDays = okDays || d == days
	}
	if !okDays {
		return SlowStockReport{}, invalid("report", fieldErr("days", "INVALID_VALUE", "30, 60, 90 or 180"))
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return SlowStockReport{}, err
	}
	cfg, err := s.settingsOrDefault(ctx, p, propertyID)
	if err != nil {
		return SlowStockReport{}, err
	}
	rows, err := s.q(ctx).SlowStock(ctx, inventorydb.SlowStockParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: f.WarehouseID, CategoryID: f.CategoryID})
	if err != nil {
		return SlowStockReport{}, err
	}
	asOf := day.BusinessDate
	basis := "LAST_ENTRY"
	if cfg.CostingMethod == MethodFIFO {
		basis = "OLDEST_LAYER"
	}
	out := SlowStockReport{AsOf: asOf, Days: days, CostingMethod: cfg.CostingMethod, AgeBasis: basis}
	out.Rows, out.Total, out.NeverOut = slowStockRows(rows, asOf, days, basis)
	return out, nil
}

// slowStockRows keeps the items whose last use is `days` days old or more (or that were never used), works out their ages and puts them in order: the ones never used first, then the longest unused, then the
// largest value.
func slowStockRows(rows []inventorydb.SlowStockRow, asOf civil.Date, days int, basis string) (out []SlowStockRow, total decimal.Decimal, never int) {
	out = []SlowStockRow{}
	for _, r := range rows {
		row := SlowStockRow{WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode,
			CategoryName: r.CategoryName, Qty: r.Qty, Value: r.Value}
		if missingDate(r.LastOutOn) {
			row.NeverOut = true
		} else {
			d, last := r.LastOutOn.DaysUntil(asOf), r.LastOutOn
			if d < days {
				continue
			}
			row.LastOutOn, row.DaysSinceOut = &last, &d
		}
		entry := r.LastInOn
		if basis == "OLDEST_LAYER" && !missingDate(r.OldestLayerOn) {
			entry = r.OldestLayerOn
		}
		if !missingDate(entry) {
			a := entry.DaysUntil(asOf)
			row.AgeDays = &a
		}
		out = append(out, row)
		total = total.Add(r.Value)
		if row.NeverOut {
			never++
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		x, y := out[a], out[b]
		if x.NeverOut != y.NeverOut {
			return x.NeverOut
		}
		if !x.NeverOut && *x.DaysSinceOut != *y.DaysSinceOut {
			return *x.DaysSinceOut > *y.DaysSinceOut
		}
		return x.Value.GreaterThan(y.Value)
	})
	return out, total, never
}

// ---------------------------------------------------------------- the largest usage (B12)

// UsageKinds are the kinds of movement the largest-usage report can add up. The default is the consumption: sales, issues, waste, staff meals and entertainment. A sale return is taken off the sales.
var (
	UsageKinds        = []string{KindPosSale, KindIssue, KindWaste, KindStaffMeal, KindEntertainment, KindTransferOut, KindSupplierReturn}
	defaultUsageKinds = []string{KindPosSale, KindIssue, KindWaste, KindStaffMeal, KindEntertainment}
	// TopUsageLimits are the sizes of the list.
	TopUsageLimits = []int{20, 50}
)

// TopUsageFilter narrows the report.
type TopUsageFilter struct {
	WarehouseID, CategoryID *int64
	Limit                   int
	Kinds                   []string
}

// TopUsageRow is an item with the value that left in the period, its share of the total and the running share.
type TopUsageRow struct {
	Rank          int             `json:"rank"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	CategoryName  string          `json:"category_name"`
	Qty           decimal.Decimal `json:"qty"`
	Value         decimal.Decimal `json:"value"`
	SharePct      decimal.Decimal `json:"share_pct"`
	CumulativePct decimal.Decimal `json:"cumulative_pct"`
}

// TopUsageReport is the list of the items that cost the most to use. `total` is the value of every item (not only the ones listed), which is what the shares are taken of.
type TopUsageReport struct {
	From  civil.Date      `json:"from"`
	To    civil.Date      `json:"to"`
	Limit int             `json:"limit"`
	Kinds []string        `json:"kinds"`
	Rows  []TopUsageRow   `json:"rows"`
	Items int             `json:"items"`
	Shown decimal.Decimal `json:"shown"`
	Total decimal.Decimal `json:"total"`
}

// TopUsage answers the report of the largest usage (inventory.view).
func (s *Service) TopUsage(ctx context.Context, propertyID int64, from, to *civil.Date, f TopUsageFilter) (TopUsageReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return TopUsageReport{}, err
	}
	limit := f.Limit
	if limit == 0 {
		limit = 20
	}
	okLimit := false
	for _, l := range TopUsageLimits {
		okLimit = okLimit || l == limit
	}
	if !okLimit {
		return TopUsageReport{}, invalid("report", fieldErr("limit", "INVALID_VALUE", "20 or 50"))
	}
	kinds := f.Kinds
	if len(kinds) == 0 {
		kinds = defaultUsageKinds
	}
	for _, k := range kinds {
		known := false
		for _, u := range UsageKinds {
			known = known || u == k
		}
		if !known {
			return TopUsageReport{}, invalid("report", fieldErr("kinds", "INVALID_VALUE", strings.Join(UsageKinds, ", ")))
		}
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return TopUsageReport{}, err
	}
	// a sale return takes off what was sold
	query := append([]string{}, kinds...)
	for _, k := range kinds {
		if k == KindPosSale {
			query = append(query, KindPosSaleReturn)
		}
	}
	rows, err := s.q(ctx).TopUsage(ctx, inventorydb.TopUsageParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, Kinds: query, WarehouseID: f.WarehouseID, CategoryID: f.CategoryID})
	if err != nil {
		return TopUsageReport{}, err
	}
	out := TopUsageReport{From: fd, To: td, Limit: limit, Kinds: kinds, Rows: []TopUsageRow{}, Items: len(rows)}
	for _, r := range rows {
		out.Total = out.Total.Add(r.Value)
	}
	var running decimal.Decimal
	for i, r := range rows {
		if i >= limit {
			break
		}
		running = running.Add(r.Value)
		share, cum := pct(r.Value, out.Total), pct(running, out.Total)
		out.Rows = append(out.Rows, TopUsageRow{Rank: i + 1, ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode, CategoryName: r.CategoryName, Qty: r.Qty, Value: r.Value,
			SharePct: *share, CumulativePct: *cum})
	}
	out.Shown = running
	return out, nil
}
