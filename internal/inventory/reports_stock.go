package inventory

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/civil"
)

// The stock reports (35-inventory.md 12.5): the value of the stock beside the general ledger, low stock, stock sold below zero, goods received and not invoiced, the usage of the departments, and the
// small dashboard of the inventory page.

// ---------------------------------------------------------------- the value of the stock against the general ledger

// ValueCell is the value of one category in one warehouse.
type ValueCell struct {
	CategoryID   int64           `json:"category_id"`
	CategoryCode string          `json:"category_code"`
	CategoryName string          `json:"category_name"`
	AccountID    int64           `json:"account_id"`
	Items        int             `json:"items"`
	Value        decimal.Decimal `json:"value"`
}

// ValueWarehouse is one warehouse of the report with its categories.
type ValueWarehouse struct {
	WarehouseID int64           `json:"warehouse_id"`
	Code        string          `json:"code"`
	Name        string          `json:"name"`
	Cells       []ValueCell     `json:"categories"`
	Total       decimal.Decimal `json:"total"`
}

// ValueAccount compares the stock with one control account of the general ledger.
type ValueAccount struct {
	AccountID int64           `json:"account_id"`
	Code      string          `json:"code"`
	Name      string          `json:"name"`
	Stock     decimal.Decimal `json:"stock"`
	GL        decimal.Decimal `json:"gl"`
	Diff      decimal.Decimal `json:"difference"`
}

// ValueReport is the report of the value of the stock as of a date. Live is true for the open day (read from the balances); a closed day is read from the snapshot of the close.
// The comparison with the general ledger is made on the last journaled day not after the date, because the journal of a day is posted by the close.
type ValueReport struct {
	AsOf       civil.Date       `json:"as_of"`
	Live       bool             `json:"live"`
	GLThrough  *civil.Date      `json:"gl_through"`
	Status     string           `json:"status"`
	Warehouses []ValueWarehouse `json:"warehouses"`
	Categories []ValueCell      `json:"categories"`
	Accounts   []ValueAccount   `json:"accounts"`
	Total      decimal.Decimal  `json:"total"`
}

// StockValue answers the value of the stock by warehouse and category, and against the accounts 1310-1340 (inventory.view).
func (s *Service) StockValue(ctx context.Context, propertyID int64, asOf *civil.Date) (ValueReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return ValueReport{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return ValueReport{}, err
	}
	at := day.BusinessDate
	if asOf != nil && asOf.Before(at) {
		at = *asOf
	}
	q := s.q(ctx)
	out := ValueReport{AsOf: at, Live: !at.Before(day.BusinessDate), Warehouses: []ValueWarehouse{}, Categories: []ValueCell{}, Accounts: []ValueAccount{}}
	type row struct {
		wid          int64
		wcode, wname string
		cid          int64
		ccode, cname string
		account      int64
		items        int
		value        decimal.Decimal
	}
	var rows []row
	if out.Live {
		rs, err := q.ValueLive(ctx, inventorydb.ValueLiveParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return ValueReport{}, err
		}
		for _, r := range rs {
			rows = append(rows, row{r.WarehouseID, r.WarehouseCode, r.WarehouseName, r.CategoryID, r.CategoryCode, r.CategoryName, r.AccountID, int(r.Items), r.Value})
		}
	} else {
		rs, err := q.ValueAsOf(ctx, inventorydb.ValueAsOfParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: at})
		if err != nil {
			return ValueReport{}, err
		}
		for _, r := range rs {
			rows = append(rows, row{r.WarehouseID, r.WarehouseCode, r.WarehouseName, r.CategoryID, r.CategoryCode, r.CategoryName, r.AccountID, int(r.Items), r.Value})
		}
	}
	byCat := map[int64]*ValueCell{}
	var order []int64
	for _, r := range rows {
		if n := len(out.Warehouses); n == 0 || out.Warehouses[n-1].WarehouseID != r.wid {
			out.Warehouses = append(out.Warehouses, ValueWarehouse{WarehouseID: r.wid, Code: r.wcode, Name: r.wname, Cells: []ValueCell{}})
		}
		w := &out.Warehouses[len(out.Warehouses)-1]
		w.Cells = append(w.Cells, ValueCell{CategoryID: r.cid, CategoryCode: r.ccode, CategoryName: r.cname, AccountID: r.account, Items: r.items, Value: r.value})
		w.Total = w.Total.Add(r.value)
		out.Total = out.Total.Add(r.value)
		c, ok := byCat[r.cid]
		if !ok {
			c = &ValueCell{CategoryID: r.cid, CategoryCode: r.ccode, CategoryName: r.cname, AccountID: r.account}
			byCat[r.cid] = c
			order = append(order, r.cid)
		}
		c.Items, c.Value = c.Items+r.items, c.Value.Add(r.value)
	}
	for _, id := range order {
		out.Categories = append(out.Categories, *byCat[id])
	}
	// the general ledger holds a day once the close has posted it
	last, err := q.LastJournaledDay(ctx, inventorydb.LastJournaledDayParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return ValueReport{}, err
	}
	through := at
	if last.BusinessDate.Before(through) {
		through = last.BusinessDate
	}
	if !last.Found {
		out.Status = "PENDING"
		return out, nil
	}
	out.GLThrough = &through
	stock, err := s.StockValueByAccount(ctx, p.TenantID, propertyID, through)
	if err != nil {
		return ValueReport{}, err
	}
	ids := make([]int64, 0, len(stock))
	for id := range stock {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	gl, err := q.GLBalancesOf(ctx, inventorydb.GLBalancesOfParams{TenantID: p.TenantID, PropertyID: propertyID, AccountIds: ids, AsOf: through})
	if err != nil {
		return ValueReport{}, err
	}
	glOf := map[int64]decimal.Decimal{}
	for _, g := range gl {
		glOf[g.AccountID] = g.Balance
	}
	names, err := q.AccountNames(ctx, inventorydb.AccountNamesParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
	if err != nil {
		return ValueReport{}, err
	}
	nameOf := map[int64]inventorydb.AccountNamesRow{}
	for _, n := range names {
		nameOf[n.ID] = n
	}
	out.Status = "MATCH"
	for _, id := range ids {
		a := ValueAccount{AccountID: id, Code: nameOf[id].Code, Name: nameOf[id].Name, Stock: stock[id], GL: glOf[id]}
		a.Diff = a.Stock.Sub(a.GL)
		if !a.Diff.IsZero() {
			out.Status = "DIFFERENCE"
		}
		out.Accounts = append(out.Accounts, a)
	}
	sort.Slice(out.Accounts, func(i, j int) bool { return out.Accounts[i].Code < out.Accounts[j].Code })
	return out, nil
}

// ---------------------------------------------------------------- low stock

// LowStockRow is an item below its minimum in a warehouse.
type LowStockRow struct {
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseCode string          `json:"warehouse_code"`
	WarehouseName string          `json:"warehouse_name"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	Qty           decimal.Decimal `json:"qty"`
	MinQty        decimal.Decimal `json:"min_qty"`
	ReorderQty    decimal.Decimal `json:"reorder_qty"`
	// Missing is how far the item is below its minimum.
	Missing decimal.Decimal `json:"missing"`
	Value   decimal.Decimal `json:"value"`
}

// LowStock lists the items whose quantity is below the minimum of the warehouse (inventory.view).
func (s *Service) LowStock(ctx context.Context, propertyID int64, warehouseID *int64) ([]LowStockRow, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).LowStock(ctx, inventorydb.LowStockParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID})
	if err != nil {
		return nil, err
	}
	out := make([]LowStockRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, LowStockRow{WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName,
			UnitCode: r.UnitCode, Qty: r.Qty, MinQty: r.MinQty, ReorderQty: derefDec(r.ReorderQty), Missing: r.MinQty.Sub(r.Qty), Value: r.Value})
	}
	return out, nil
}

// ---------------------------------------------------------------- negative stock

// NegativeStockRow is stock that was sold before the goods came in, still waiting for its settlement.
type NegativeStockRow struct {
	ShortfallID   int64           `json:"shortfall_id"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseCode string          `json:"warehouse_code"`
	WarehouseName string          `json:"warehouse_name"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	QtyOpen       decimal.Decimal `json:"qty_open"`
	EstUnitCost   decimal.Decimal `json:"est_unit_cost"`
	EstValue      decimal.Decimal `json:"est_value"`
	BalanceQty    decimal.Decimal `json:"balance_qty"`
	Since         civil.Date      `json:"since"`
	AgeDays       int             `json:"age_days"`
	Source        string          `json:"source"`
}

// NegativeStockReport lists the open shortfalls with the total at the estimated cost.
type NegativeStockReport struct {
	BusinessDate civil.Date         `json:"business_date"`
	Rows         []NegativeStockRow `json:"rows"`
	Total        decimal.Decimal    `json:"total"`
}

// NegativeStock lists the shortfalls that are still open (inventory.view).
func (s *Service) NegativeStock(ctx context.Context, propertyID int64) (NegativeStockReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return NegativeStockReport{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return NegativeStockReport{}, err
	}
	rows, err := s.q(ctx).OpenShortfalls(ctx, inventorydb.OpenShortfallsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return NegativeStockReport{}, err
	}
	out := NegativeStockReport{BusinessDate: day.BusinessDate, Rows: []NegativeStockRow{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, NegativeStockRow{ShortfallID: r.ID, WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, ItemID: r.ItemID, ItemCode: r.ItemCode,
			ItemName: r.ItemName, UnitCode: r.UnitCode, QtyOpen: r.QtyOpen, EstUnitCost: r.EstUnitCost, EstValue: r.EstValueOpen, BalanceQty: r.BalanceQty, Since: r.BusinessDate,
			AgeDays: r.BusinessDate.DaysUntil(day.BusinessDate), Source: r.SourceType})
		out.Total = out.Total.Add(r.EstValueOpen)
	}
	return out, nil
}

// ---------------------------------------------------------------- goods received and not invoiced

// GrniRow is a receipt that is not billed yet (or not in full).
type GrniRow struct {
	DraftID       int64           `json:"draft_id"`
	ReceiptID     int64           `json:"receipt_id"`
	ReceiptNumber string          `json:"receipt_number"`
	ReceivedOn    civil.Date      `json:"received_on"`
	SupplierID    int64           `json:"supplier_id"`
	SupplierName  string          `json:"supplier_name"`
	WarehouseName string          `json:"warehouse_name"`
	Status        string          `json:"status"`
	AgeDays       int             `json:"age_days"`
	Bucket        string          `json:"bucket"`
	Value         decimal.Decimal `json:"value"`
}

// GrniBucket is the total of one age band.
type GrniBucket struct {
	Bucket string          `json:"bucket"`
	Count  int             `json:"count"`
	Value  decimal.Decimal `json:"value"`
}

// GrniReport lists the unbilled receipts by age. LedgerBalance is what the account of goods received not invoiced holds according to the stock ledger and the bills, positive for a liability.
type GrniReport struct {
	AsOf          civil.Date      `json:"as_of"`
	Rows          []GrniRow       `json:"rows"`
	Buckets       []GrniBucket    `json:"buckets"`
	Total         decimal.Decimal `json:"total"`
	LedgerBalance decimal.Decimal `json:"ledger_balance"`
}

// grniBuckets are the age bands in days.
var grniBuckets = []struct {
	name string
	max  int
}{{"0-30", 30}, {"31-60", 60}, {"61-90", 90}, {"90+", 1 << 30}}

func grniBucket(age int) string {
	for _, b := range grniBuckets {
		if age <= b.max {
			return b.name
		}
	}
	return grniBuckets[len(grniBuckets)-1].name
}

// ReceivedNotInvoiced lists the receipts nobody has billed yet by age (inventory.view).
func (s *Service) ReceivedNotInvoiced(ctx context.Context, propertyID int64) (GrniReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return GrniReport{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return GrniReport{}, err
	}
	q := s.q(ctx)
	rows, err := q.ReceivedNotInvoiced(ctx, inventorydb.ReceivedNotInvoicedParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: day.BusinessDate})
	if err != nil {
		return GrniReport{}, err
	}
	out := GrniReport{AsOf: day.BusinessDate, Rows: []GrniRow{}, Buckets: []GrniBucket{}}
	for _, b := range grniBuckets {
		out.Buckets = append(out.Buckets, GrniBucket{Bucket: b.name})
	}
	for _, r := range rows {
		age := r.ReceivedOn.DaysUntil(day.BusinessDate)
		name := grniBucket(age)
		out.Rows = append(out.Rows, GrniRow{DraftID: r.DraftID, ReceiptID: r.ReceiptID, ReceiptNumber: r.ReceiptNumber, ReceivedOn: r.ReceivedOn, SupplierID: r.SupplierID, SupplierName: r.SupplierName,
			WarehouseName: r.WarehouseName, Status: r.Status, AgeDays: age, Bucket: name, Value: r.RemainingValue})
		for i := range out.Buckets {
			if out.Buckets[i].Bucket == name {
				out.Buckets[i].Count++
				out.Buckets[i].Value = out.Buckets[i].Value.Add(r.RemainingValue)
			}
		}
		out.Total = out.Total.Add(r.RemainingValue)
	}
	g, err := q.GRNIFromSources(ctx, inventorydb.GRNIFromSourcesParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: day.BusinessDate})
	if err != nil {
		return GrniReport{}, err
	}
	out.LedgerBalance = g.Balance.Neg()
	return out, nil
}

// ---------------------------------------------------------------- usage of the departments

// UsageItem is what one department took of one item.
type UsageItem struct {
	ItemID       int64           `json:"item_id"`
	ItemCode     string          `json:"item_code"`
	ItemName     string          `json:"item_name"`
	UnitCode     string          `json:"unit_code"`
	CategoryName string          `json:"category_name"`
	Kind         string          `json:"kind"`
	Qty          decimal.Decimal `json:"qty"`
	Value        decimal.Decimal `json:"value"`
}

// UsageDepartment is the usage of one department (nil id: movements with no department, such as the waste of a warehouse).
type UsageDepartment struct {
	DepartmentID *int64                     `json:"department_id"`
	Name         string                     `json:"name"`
	Total        decimal.Decimal            `json:"total"`
	ByKind       map[string]decimal.Decimal `json:"by_kind"`
	Items        []UsageItem                `json:"items"`
}

// UsageReport is the report of what the departments took from the stock in a period.
type UsageReport struct {
	From        civil.Date        `json:"from"`
	To          civil.Date        `json:"to"`
	Departments []UsageDepartment `json:"departments"`
	Total       decimal.Decimal   `json:"total"`
}

// Usage lists what the departments took from the stock between two days (inventory.view).
func (s *Service) Usage(ctx context.Context, propertyID int64, from, to *civil.Date, departmentID *int64) (UsageReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return UsageReport{}, err
	}
	f, t, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return UsageReport{}, err
	}
	rows, err := s.q(ctx).UsageByDepartment(ctx, inventorydb.UsageByDepartmentParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: f, ToDate: t, DepartmentID: departmentID})
	if err != nil {
		return UsageReport{}, err
	}
	out := UsageReport{From: f, To: t, Departments: []UsageDepartment{}}
	at := map[int64]int{}
	for _, r := range rows {
		k := idKey(r.DepartmentID)
		i, ok := at[k]
		if !ok {
			i = len(out.Departments)
			at[k] = i
			out.Departments = append(out.Departments, UsageDepartment{DepartmentID: r.DepartmentID, Name: r.DepartmentName, ByKind: map[string]decimal.Decimal{}, Items: []UsageItem{}})
		}
		d := &out.Departments[i]
		d.Total, d.ByKind[r.Kind] = d.Total.Add(r.Value), d.ByKind[r.Kind].Add(r.Value)
		d.Items = append(d.Items, UsageItem{ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode, CategoryName: r.CategoryName, Kind: r.Kind, Qty: r.Qty, Value: r.Value})
		out.Total = out.Total.Add(r.Value)
	}
	return out, nil
}

// ---------------------------------------------------------------- the dashboard

// Dashboard is the small summary at the top of the inventory page.
type Dashboard struct {
	BusinessDate     civil.Date      `json:"business_date"`
	MtdFrom          civil.Date      `json:"mtd_from"`
	StockValue       decimal.Decimal `json:"stock_value"`
	Food             FbPart          `json:"food"`
	Beverage         FbPart          `json:"beverage"`
	Fb               FbPart          `json:"fb"`
	NegativeItems    int             `json:"negative_items"`
	NegativeValue    decimal.Decimal `json:"negative_value"`
	BelowMin         int             `json:"below_min"`
	WaitingApprovals int             `json:"waiting_approvals"`
}

// Dashboard answers the summary of the module for the open business day (inventory.view).
func (s *Service) Dashboard(ctx context.Context, propertyID int64) (Dashboard, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	q := s.q(ctx)
	out := Dashboard{BusinessDate: day.BusinessDate, MtdFrom: startOfMonth(day.BusinessDate)}
	if out.StockValue, err = q.TotalStockValue(ctx, inventorydb.TotalStockValueParams{TenantID: p.TenantID, PropertyID: propertyID}); err != nil {
		return Dashboard{}, err
	}
	fig, err := s.figuresOf(ctx, p.TenantID, propertyID, out.MtdFrom, day.BusinessDate)
	if err != nil {
		return Dashboard{}, err
	}
	tl, err := s.targetsOf(ctx, p.TenantID, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	m := summarizeDays(dayInput{cost: fig.Cost, fb: fig.Fb}, targets{list: tl})
	out.Food, out.Beverage, out.Fb = m.Food, m.Beverage, m.Fb
	sf, err := q.OpenShortfalls(ctx, inventorydb.OpenShortfallsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Dashboard{}, err
	}
	for _, r := range sf {
		out.NegativeValue = out.NegativeValue.Add(r.EstValueOpen)
	}
	n, err := q.CountNegativeBalances(ctx, inventorydb.CountNegativeBalancesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Dashboard{}, err
	}
	out.NegativeItems = int(n)
	low, err := q.CountLowStock(ctx, inventorydb.CountLowStockParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Dashboard{}, err
	}
	out.BelowMin = int(low)
	inbox, err := s.Inbox(ctx, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	out.WaitingApprovals = len(inbox)
	return out, nil
}

func derefDec(d *decimal.Decimal) decimal.Decimal {
	if d == nil {
		return decimal.Zero
	}
	return *d
}
