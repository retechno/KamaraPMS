package inventory

import (
	"context"
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/civil"
)

// The other reports of the inventory (35-inventory.md 12.5, section 22): waste by reason, count variances, purchase price variances by supplier, transfers without a requisition, the amenities by month,
// the documents waiting in the approval chain, the recipe cost list, staff meals and entertainment, and the documents behind a day of the daily cost. They read the ledger and the documents; the figures
// are summed with decimals here or in SQL, and a percentage is rounded to two places when it is answered.

// ---------------------------------------------------------------- waste by reason (A1)

// WasteFilter narrows the waste report.
type WasteFilter struct {
	WarehouseID, DepartmentID, CategoryID *int64
}

// WasteRow is what was thrown away of one item, for one reason, in one warehouse and department.
type WasteRow struct {
	Reason         string          `json:"reason"`
	WarehouseID    int64           `json:"warehouse_id"`
	WarehouseCode  string          `json:"warehouse_code"`
	WarehouseName  string          `json:"warehouse_name"`
	DepartmentID   *int64          `json:"department_id"`
	DepartmentName string          `json:"department_name"`
	ItemID         int64           `json:"item_id"`
	ItemCode       string          `json:"item_code"`
	ItemName       string          `json:"item_name"`
	UnitCode       string          `json:"unit_code"`
	CategoryName   string          `json:"category_name"`
	Qty            decimal.Decimal `json:"qty"`
	Value          decimal.Decimal `json:"value"`
	Docs           int             `json:"docs"`
}

// WasteReason is the total of one reason.
type WasteReason struct {
	Reason string          `json:"reason"`
	Value  decimal.Decimal `json:"value"`
	Docs   int             `json:"docs"`
}

// WasteReport lists the waste of a period: the waste issues and the dishes thrown away after a POS void (reason POS_VOID).
type WasteReport struct {
	From    civil.Date      `json:"from"`
	To      civil.Date      `json:"to"`
	Rows    []WasteRow      `json:"rows"`
	Reasons []WasteReason   `json:"reasons"`
	Total   decimal.Decimal `json:"total"`
	Docs    int             `json:"docs"`
}

// Waste answers the report of waste by reason (inventory.view).
func (s *Service) Waste(ctx context.Context, propertyID int64, from, to *civil.Date, f WasteFilter) (WasteReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return WasteReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return WasteReport{}, err
	}
	q := s.q(ctx)
	rows, err := q.WasteReport(ctx, inventorydb.WasteReportParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, WarehouseID: f.WarehouseID, DepartmentID: f.DepartmentID, CategoryID: f.CategoryID})
	if err != nil {
		return WasteReport{}, err
	}
	out := WasteReport{From: fd, To: td, Rows: []WasteRow{}, Reasons: []WasteReason{}}
	for _, r := range rows {
		out.Rows = append(out.Rows, WasteRow{Reason: r.Reason, WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName,
			ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode, CategoryName: r.CategoryName, Qty: r.Qty, Value: r.Value, Docs: int(r.Docs)})
	}
	rs, err := q.WasteDocsByReason(ctx, inventorydb.WasteDocsByReasonParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, WarehouseID: f.WarehouseID, DepartmentID: f.DepartmentID, CategoryID: f.CategoryID})
	if err != nil {
		return WasteReport{}, err
	}
	for _, r := range rs {
		out.Reasons = append(out.Reasons, WasteReason{Reason: r.Reason, Value: r.Value, Docs: int(r.Docs)})
		out.Total, out.Docs = out.Total.Add(r.Value), out.Docs+int(r.Docs)
	}
	return out, nil
}

// ---------------------------------------------------------------- count variances (A2)

// CountVarianceCount is an approved count with its totals.
type CountVarianceCount struct {
	ID                int64           `json:"id"`
	Number            string          `json:"number"`
	WarehouseID       int64           `json:"warehouse_id"`
	WarehouseCode     string          `json:"warehouse_code"`
	WarehouseName     string          `json:"warehouse_name"`
	Scope             string          `json:"scope"`
	StartedOn         civil.Date      `json:"started_on"`
	ApprovedOn        civil.Date      `json:"approved_on"`
	ApproverName      string          `json:"approver_name"`
	Gain              decimal.Decimal `json:"gain"`
	Loss              decimal.Decimal `json:"loss"`
	Net               decimal.Decimal `json:"net"`
	Lines             int             `json:"lines"`
	LinesWithVariance int             `json:"lines_with_variance"`
}

// CountVarianceLine is an item that was found different from the book in a count.
type CountVarianceLine struct {
	CountID       int64           `json:"count_id"`
	CountNumber   string          `json:"count_number"`
	ApprovedOn    civil.Date      `json:"approved_on"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseCode string          `json:"warehouse_code"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	CategoryName  string          `json:"category_name"`
	BookQty       decimal.Decimal `json:"book_qty"`
	CountedQty    decimal.Decimal `json:"counted_qty"`
	VarianceQty   decimal.Decimal `json:"variance_qty"`
	VarianceValue decimal.Decimal `json:"variance_value"`
}

// CountVarianceItem is the variance of an item over the counts of the period.
type CountVarianceItem struct {
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseCode string          `json:"warehouse_code"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	Counts        int             `json:"counts"`
	VarianceQty   decimal.Decimal `json:"variance_qty"`
	VarianceValue decimal.Decimal `json:"variance_value"`
}

// CountVarianceReport is the report of the approved counts of a period: a loss is negative in the lines, positive in Loss.
type CountVarianceReport struct {
	From   civil.Date           `json:"from"`
	To     civil.Date           `json:"to"`
	Counts []CountVarianceCount `json:"counts"`
	Lines  []CountVarianceLine  `json:"lines"`
	Items  []CountVarianceItem  `json:"items"`
	Gain   decimal.Decimal      `json:"gain"`
	Loss   decimal.Decimal      `json:"loss"`
	Net    decimal.Decimal      `json:"net"`
}

// CountVariances answers the report of count variances (inventory.view). Net is the gain less the loss: the book value moved by the counts.
func (s *Service) CountVariances(ctx context.Context, propertyID int64, from, to *civil.Date, warehouseID *int64) (CountVarianceReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return CountVarianceReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return CountVarianceReport{}, err
	}
	q := s.q(ctx)
	cs, err := q.CountVarianceCounts(ctx, inventorydb.CountVarianceCountsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, WarehouseID: warehouseID})
	if err != nil {
		return CountVarianceReport{}, err
	}
	out := CountVarianceReport{From: fd, To: td, Counts: []CountVarianceCount{}, Lines: []CountVarianceLine{}, Items: []CountVarianceItem{}}
	for _, c := range cs {
		net := c.Gain.Sub(c.Loss)
		out.Counts = append(out.Counts, CountVarianceCount{ID: c.ID, Number: c.CntNumber, WarehouseID: c.WarehouseID, WarehouseCode: c.WarehouseCode, WarehouseName: c.WarehouseName, Scope: c.Scope,
			StartedOn: c.BusinessDateStarted, ApprovedOn: c.ApprovedOn, ApproverName: c.ApproverName, Gain: c.Gain, Loss: c.Loss, Net: net, Lines: int(c.Lines), LinesWithVariance: int(c.LinesWithVariance)})
		out.Gain, out.Loss = out.Gain.Add(c.Gain), out.Loss.Add(c.Loss)
	}
	out.Net = out.Gain.Sub(out.Loss)
	ls, err := q.CountVarianceLines(ctx, inventorydb.CountVarianceLinesParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, WarehouseID: warehouseID})
	if err != nil {
		return CountVarianceReport{}, err
	}
	type key struct{ w, i int64 }
	at := map[key]int{}
	for _, l := range ls {
		out.Lines = append(out.Lines, CountVarianceLine{CountID: l.CountID, CountNumber: l.CntNumber, ApprovedOn: l.ApprovedOn, WarehouseID: l.WarehouseID, WarehouseCode: l.WarehouseCode, ItemID: l.ItemID,
			ItemCode: l.ItemCode, ItemName: l.ItemName, UnitCode: l.UnitCode, CategoryName: l.CategoryName, BookQty: l.BookQty, CountedQty: l.CountedQty, VarianceQty: l.VarianceQty, VarianceValue: l.VarianceValue})
		k := key{l.WarehouseID, l.ItemID}
		i, ok := at[k]
		if !ok {
			i = len(out.Items)
			at[k] = i
			out.Items = append(out.Items, CountVarianceItem{WarehouseID: l.WarehouseID, WarehouseCode: l.WarehouseCode, ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitCode: l.UnitCode})
		}
		it := &out.Items[i]
		it.Counts++
		it.VarianceQty, it.VarianceValue = it.VarianceQty.Add(l.VarianceQty), it.VarianceValue.Add(l.VarianceValue)
	}
	sort.SliceStable(out.Items, func(i, j int) bool { return out.Items[i].VarianceValue.LessThan(out.Items[j].VarianceValue) })
	return out, nil
}

// ---------------------------------------------------------------- purchase price variances by supplier (A3)

// PriceVarianceItem is what one supplier billed of one item against the price of the receipts.
type PriceVarianceItem struct {
	ItemID        int64            `json:"item_id"`
	ItemCode      string           `json:"item_code"`
	ItemName      string           `json:"item_name"`
	UnitCode      string           `json:"unit_code"`
	Qty           decimal.Decimal  `json:"qty"`
	ReceiptValue  decimal.Decimal  `json:"receipt_value"`
	BillCost      decimal.Decimal  `json:"bill_cost"`
	ReceiptPrice  *decimal.Decimal `json:"receipt_unit_price"`
	BillPrice     *decimal.Decimal `json:"bill_unit_price"`
	Variance      decimal.Decimal  `json:"variance"`
	Pct           *decimal.Decimal `json:"pct"`
	Bills         int              `json:"bills"`
	LinesAboveTol int              `json:"lines_above_tolerance"`
	Lines         int              `json:"lines"`
}

// PriceVarianceSupplier is the total of a supplier with its items.
type PriceVarianceSupplier struct {
	SupplierID   int64               `json:"supplier_id"`
	SupplierName string              `json:"supplier_name"`
	Bills        int                 `json:"bills"`
	BillsAbove   int                 `json:"bills_above_tolerance"`
	Lines        int                 `json:"lines"`
	LinesAbove   int                 `json:"lines_above_tolerance"`
	ReceiptValue decimal.Decimal     `json:"receipt_value"`
	BillCost     decimal.Decimal     `json:"bill_cost"`
	Variance     decimal.Decimal     `json:"variance"`
	Pct          *decimal.Decimal    `json:"pct"`
	Items        []PriceVarianceItem `json:"items"`
}

// PriceVarianceReport lists the suppliers by the difference between the price they billed and the price of the goods received. The tolerance is the one of the settings: a line is above it when the
// difference is more than that percentage of the value received.
type PriceVarianceReport struct {
	From         civil.Date              `json:"from"`
	To           civil.Date              `json:"to"`
	TolerancePct decimal.Decimal         `json:"tolerance_pct"`
	Suppliers    []PriceVarianceSupplier `json:"suppliers"`
	Bills        int                     `json:"bills"`
	BillsAbove   int                     `json:"bills_above_tolerance"`
	ReceiptValue decimal.Decimal         `json:"receipt_value"`
	BillCost     decimal.Decimal         `json:"bill_cost"`
	Variance     decimal.Decimal         `json:"variance"`
	Pct          *decimal.Decimal        `json:"pct"`
}

// aboveTolerance says whether a difference is more than the tolerance percentage of the value received.
func aboveTolerance(variance, base, tolerancePct decimal.Decimal) bool {
	if variance.IsZero() {
		return false
	}
	if !base.IsPositive() {
		return true
	}
	return variance.Abs().Mul(hundred).Div(base).GreaterThan(tolerancePct)
}

func unitPrice(total, qty decimal.Decimal) *decimal.Decimal {
	if !qty.IsPositive() {
		return nil
	}
	v := total.Div(qty).Round(4)
	return &v
}

// PriceVariances answers the report of price variances by supplier (inventory.view).
func (s *Service) PriceVariances(ctx context.Context, propertyID int64, from, to *civil.Date, supplierID *int64) (PriceVarianceReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return PriceVarianceReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return PriceVarianceReport{}, err
	}
	q := s.q(ctx)
	tol, err := q.PriceToleranceOf(ctx, inventorydb.PriceToleranceOfParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		tol = decimal.Zero
	}
	rows, err := q.PriceVarianceLines(ctx, inventorydb.PriceVarianceLinesParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, SupplierID: supplierID})
	if err != nil {
		return PriceVarianceReport{}, err
	}
	out := PriceVarianceReport{From: fd, To: td, TolerancePct: tol, Suppliers: []PriceVarianceSupplier{}}
	type supState struct {
		bills, above map[int64]bool
		items        map[int64]int
		itemBills    map[int64]map[int64]bool
	}
	sup := map[int64]*supState{}
	supAt := map[int64]int{}
	allBills, allAbove := map[int64]bool{}, map[int64]bool{}
	for _, r := range rows {
		i, ok := supAt[r.SupplierID]
		if !ok {
			i = len(out.Suppliers)
			supAt[r.SupplierID] = i
			out.Suppliers = append(out.Suppliers, PriceVarianceSupplier{SupplierID: r.SupplierID, SupplierName: r.SupplierName, Items: []PriceVarianceItem{}})
			sup[r.SupplierID] = &supState{bills: map[int64]bool{}, above: map[int64]bool{}, items: map[int64]int{}, itemBills: map[int64]map[int64]bool{}}
		}
		sp, st := &out.Suppliers[i], sup[r.SupplierID]
		j, ok := st.items[r.ItemID]
		if !ok {
			j = len(sp.Items)
			st.items[r.ItemID] = j
			sp.Items = append(sp.Items, PriceVarianceItem{ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode})
			st.itemBills[r.ItemID] = map[int64]bool{}
		}
		it := &sp.Items[j]
		above := aboveTolerance(r.Variance, r.ReceiptValue, tol)
		it.Qty, it.ReceiptValue, it.BillCost, it.Variance = it.Qty.Add(r.QtyStock), it.ReceiptValue.Add(r.ReceiptValue), it.BillCost.Add(r.BillCost), it.Variance.Add(r.Variance)
		it.Lines++
		st.itemBills[r.ItemID][r.BillID] = true
		sp.Lines++
		sp.ReceiptValue, sp.BillCost, sp.Variance = sp.ReceiptValue.Add(r.ReceiptValue), sp.BillCost.Add(r.BillCost), sp.Variance.Add(r.Variance)
		st.bills[r.BillID], allBills[r.BillID] = true, true
		if above {
			it.LinesAboveTol++
			sp.LinesAbove++
			st.above[r.BillID], allAbove[r.BillID] = true, true
		}
		out.ReceiptValue, out.BillCost, out.Variance = out.ReceiptValue.Add(r.ReceiptValue), out.BillCost.Add(r.BillCost), out.Variance.Add(r.Variance)
	}
	for i := range out.Suppliers {
		sp := &out.Suppliers[i]
		st := sup[sp.SupplierID]
		sp.Bills, sp.BillsAbove = len(st.bills), len(st.above)
		sp.Pct = pct(sp.Variance, sp.ReceiptValue)
		for j := range sp.Items {
			it := &sp.Items[j]
			it.Bills = len(st.itemBills[it.ItemID])
			it.ReceiptPrice, it.BillPrice = unitPrice(it.ReceiptValue, it.Qty), unitPrice(it.BillCost, it.Qty)
			it.Pct = pct(it.Variance, it.ReceiptValue)
		}
		sort.SliceStable(sp.Items, func(a, b int) bool { return sp.Items[a].Variance.Abs().GreaterThan(sp.Items[b].Variance.Abs()) })
	}
	sort.SliceStable(out.Suppliers, func(a, b int) bool {
		return out.Suppliers[a].Variance.Abs().GreaterThan(out.Suppliers[b].Variance.Abs())
	})
	out.Bills, out.BillsAbove = len(allBills), len(allAbove)
	out.Pct = pct(out.Variance, out.ReceiptValue)
	return out, nil
}

// ---------------------------------------------------------------- transfers without a requisition (A4)

// TransferNoSRRow is a line of a transfer that was made without a requisition.
type TransferNoSRRow struct {
	TransferID    int64           `json:"transfer_id"`
	Number        string          `json:"number"`
	BusinessDate  civil.Date      `json:"business_date"`
	Status        string          `json:"status"`
	FromWarehouse string          `json:"from_warehouse"`
	ToWarehouse   string          `json:"to_warehouse"`
	CreatedByName string          `json:"created_by_name"`
	Note          *string         `json:"note"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitCode      string          `json:"unit_code"`
	Qty           decimal.Decimal `json:"qty"`
	Value         decimal.Decimal `json:"value"`
}

// TransferNoSRReport lists the transfers made without a requisition. A transfer that was reversed is listed and not counted in the totals.
type TransferNoSRReport struct {
	From  civil.Date        `json:"from"`
	To    civil.Date        `json:"to"`
	Rows  []TransferNoSRRow `json:"rows"`
	Docs  int               `json:"docs"`
	Total decimal.Decimal   `json:"total"`
}

// TransfersWithoutRequisition answers the report (inventory.view).
func (s *Service) TransfersWithoutRequisition(ctx context.Context, propertyID int64, from, to *civil.Date, fromWarehouse, toWarehouse *int64) (TransferNoSRReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return TransferNoSRReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return TransferNoSRReport{}, err
	}
	rows, err := s.q(ctx).TransfersWithoutRequisition(ctx, inventorydb.TransfersWithoutRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, FromWarehouseID: fromWarehouse, ToWarehouseID: toWarehouse})
	if err != nil {
		return TransferNoSRReport{}, err
	}
	out := TransferNoSRReport{From: fd, To: td, Rows: []TransferNoSRRow{}}
	seen := map[int64]bool{}
	for _, r := range rows {
		out.Rows = append(out.Rows, TransferNoSRRow{TransferID: r.ID, Number: r.TrfNumber, BusinessDate: r.BusinessDate, Status: r.Status, FromWarehouse: r.FromName, ToWarehouse: r.ToName,
			CreatedByName: r.CreatedByName, Note: r.Note, ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, UnitCode: r.UnitCode, Qty: r.QtyStock, Value: r.Value})
		if r.Status != "REVERSED" {
			out.Total = out.Total.Add(r.Value)
			if !seen[r.ID] {
				seen[r.ID] = true
				out.Docs++
			}
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- amenities by month (A5)

// AmenityMonthItem is what one item of the amenities cost in a month.
type AmenityMonthItem struct {
	ItemID   int64            `json:"item_id"`
	ItemCode string           `json:"item_code"`
	ItemName string           `json:"item_name"`
	UnitCode string           `json:"unit_code"`
	Qty      decimal.Decimal  `json:"qty"`
	Cost     decimal.Decimal  `json:"cost"`
	PrevCost *decimal.Decimal `json:"prev_cost"`
}

// AmenityMonth is a month of the amenities: the room-nights and guests they were made for, the cost, the cost per occupied room and the comparison with the month before.
type AmenityMonth struct {
	Month           civil.Date       `json:"month"`
	Nights          int              `json:"nights"`
	Rooms           int              `json:"occupied_rooms"`
	Guests          int              `json:"guests"`
	CompRooms       int              `json:"comp_rooms"`
	HouseUseRooms   int              `json:"house_use_rooms"`
	Cost            decimal.Decimal  `json:"cost"`
	CostPerRoom     *decimal.Decimal `json:"cost_per_room"`
	CostPerGuest    *decimal.Decimal `json:"cost_per_guest"`
	PrevCost        *decimal.Decimal `json:"prev_cost"`
	PrevCostPerRoom *decimal.Decimal `json:"prev_cost_per_room"`
	// ChangePct is the change of the cost per occupied room against the month before.
	ChangePct *decimal.Decimal   `json:"change_pct"`
	Items     []AmenityMonthItem `json:"items"`
}

// AmenityMonthsReport is the amenities by month for the months from..to (whole months).
type AmenityMonthsReport struct {
	From   civil.Date     `json:"from"`
	To     civil.Date     `json:"to"`
	Months []AmenityMonth `json:"months"`
}

func addMonths(d civil.Date, n int) civil.Date {
	return civil.NewDate(d.Year(), d.Month()+time.Month(n), 1)
}

func perUnit(cost decimal.Decimal, n int) *decimal.Decimal {
	if n <= 0 {
		return nil
	}
	v := cost.Div(decimal.NewFromInt(int64(n))).Round(2)
	return &v
}

// AmenityMonths answers the amenities by month (inventory.view). The default is the six months up to the month of the open day; the period is widened to whole months, and the month before the first is read
// for the comparison.
func (s *Service) AmenityMonths(ctx context.Context, propertyID int64, from, to *civil.Date) (AmenityMonthsReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return AmenityMonthsReport{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return AmenityMonthsReport{}, err
	}
	last := day.BusinessDate
	if to != nil {
		last = *to
	}
	first := addMonths(startOfMonth(last), -5)
	if from != nil {
		first = *from
	}
	first = startOfMonth(first)
	end := addMonths(startOfMonth(last), 1).AddDays(-1)
	if end.Before(first) {
		return AmenityMonthsReport{}, invalid("report", fieldErr("to", "BEFORE_FROM", "not before the start of the period"))
	}
	if first.DaysUntil(end) >= maxReportDays*2 {
		return AmenityMonthsReport{}, invalid("report", fieldErr("to", "OUT_OF_RANGE", "24 months at most"))
	}
	q := s.q(ctx)
	read := addMonths(first, -1)
	ms, err := q.AmenityMonths(ctx, inventorydb.AmenityMonthsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: read, ToDate: end})
	if err != nil {
		return AmenityMonthsReport{}, err
	}
	is, err := q.AmenityMonthItems(ctx, inventorydb.AmenityMonthItemsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: read, ToDate: end})
	if err != nil {
		return AmenityMonthsReport{}, err
	}
	byMonth := map[civil.Date]*AmenityMonth{}
	for _, m := range ms {
		byMonth[m.Month] = &AmenityMonth{Month: m.Month, Nights: int(m.Nights), Rooms: int(m.Rooms), Guests: int(m.Guests), CompRooms: int(m.CompRooms), HouseUseRooms: int(m.HouseUseRooms), Cost: m.Cost,
			CostPerRoom: perUnit(m.Cost, int(m.Rooms)), CostPerGuest: perUnit(m.Cost, int(m.Guests)), Items: []AmenityMonthItem{}}
	}
	itemCost := map[civil.Date]map[int64]decimal.Decimal{}
	for _, i := range is {
		if itemCost[i.Month] == nil {
			itemCost[i.Month] = map[int64]decimal.Decimal{}
		}
		itemCost[i.Month][i.ItemID] = i.Cost
		if m := byMonth[i.Month]; m != nil {
			m.Items = append(m.Items, AmenityMonthItem{ItemID: i.ItemID, ItemCode: i.ItemCode, ItemName: i.ItemName, UnitCode: i.UnitCode, Qty: i.Qty, Cost: i.Cost})
		}
	}
	out := AmenityMonthsReport{From: first, To: end, Months: []AmenityMonth{}}
	for m := first; !m.After(end); m = addMonths(m, 1) {
		cur := byMonth[m]
		if cur == nil {
			cur = &AmenityMonth{Month: m, Items: []AmenityMonthItem{}}
		}
		if prev := byMonth[addMonths(m, -1)]; prev != nil {
			c := prev.Cost
			cur.PrevCost, cur.PrevCostPerRoom = &c, prev.CostPerRoom
			if cur.CostPerRoom != nil && prev.CostPerRoom != nil && prev.CostPerRoom.IsPositive() {
				v := cur.CostPerRoom.Sub(*prev.CostPerRoom).Mul(hundred).Div(*prev.CostPerRoom).Round(1)
				cur.ChangePct = &v
			}
			for k := range cur.Items {
				if pc, ok := itemCost[prev.Month][cur.Items[k].ItemID]; ok {
					cur.Items[k].PrevCost = &pc
				}
			}
		}
		out.Months = append(out.Months, *cur)
	}
	return out, nil
}

// ---------------------------------------------------------------- documents waiting in the approval chain (A6)

// PendingDoc is a purchase request or a requisition that is still in the chain.
type PendingDoc struct {
	DocType        string     `json:"doc_type"`
	DocID          int64      `json:"doc_id"`
	Number         string     `json:"number"`
	DepartmentName string     `json:"department_name"`
	RequesterName  string     `json:"requester_name"`
	LevelNo        int        `json:"level_no"`
	StepName       string     `json:"step_name"`
	Awaiting       string     `json:"awaiting"`
	SubmittedAt    *time.Time `json:"submitted_at"`
	// EnteredAt is when the document reached the level it waits at: the age counts from it.
	EnteredAt *time.Time      `json:"entered_at"`
	AgeHours  int64           `json:"age_hours"`
	AgeDays   int             `json:"age_days"`
	Value     decimal.Decimal `json:"value"`
}

// PendingApprovalsReport lists the documents waiting for a decision, the oldest first.
type PendingApprovalsReport struct {
	Rows  []PendingDoc    `json:"rows"`
	Total decimal.Decimal `json:"total"`
}

// PendingApprovals answers the documents in the chain (inventory.view). The age is the time since the document reached its current level.
func (s *Service) PendingApprovals(ctx context.Context, propertyID int64) (PendingApprovalsReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return PendingApprovalsReport{}, err
	}
	rows, err := s.q(ctx).PendingApprovalDocs(ctx, inventorydb.PendingApprovalDocsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return PendingApprovalsReport{}, err
	}
	now := s.clock.Now()
	out := PendingApprovalsReport{Rows: []PendingDoc{}}
	for _, r := range rows {
		since := r.ActivatedAt
		if since == nil {
			since = r.SubmittedAt
		}
		d := PendingDoc{DocType: r.DocType, DocID: r.ID, Number: r.Number, DepartmentName: r.DepartmentName, RequesterName: r.RequesterName, LevelNo: int(r.LevelNo), StepName: r.StepName,
			Awaiting: r.Awaiting, SubmittedAt: r.SubmittedAt, EnteredAt: since, Value: r.EstTotal}
		if since != nil {
			h := int64(now.Sub(*since).Hours())
			if h < 0 {
				h = 0
			}
			d.AgeHours, d.AgeDays = h, int(h/24)
		}
		out.Rows = append(out.Rows, d)
		out.Total = out.Total.Add(r.EstTotal)
	}
	return out, nil
}

// ---------------------------------------------------------------- staff meals and entertainment (A8)

// MealRow is the staff meals or the entertainment of one department.
type MealRow struct {
	DepartmentID   *int64           `json:"department_id"`
	DepartmentName string           `json:"department_name"`
	Purpose        string           `json:"purpose"`
	Docs           int              `json:"docs"`
	Covers         int              `json:"covers"`
	Value          decimal.Decimal  `json:"value"`
	CostPerCover   *decimal.Decimal `json:"cost_per_cover"`
}

// MealsReport is the staff meals and the entertainment of a period by department. A document that was reversed nets to nothing (its reversal counts -1 document and the negative of its covers and value).
type MealsReport struct {
	From   civil.Date         `json:"from"`
	To     civil.Date         `json:"to"`
	Rows   []MealRow          `json:"rows"`
	Totals map[string]MealRow `json:"totals"`
}

// Meals answers the report (inventory.view).
func (s *Service) Meals(ctx context.Context, propertyID int64, from, to *civil.Date, departmentID *int64) (MealsReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return MealsReport{}, err
	}
	fd, td, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return MealsReport{}, err
	}
	rows, err := s.q(ctx).MealIssues(ctx, inventorydb.MealIssuesParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: fd, ToDate: td, DepartmentID: departmentID})
	if err != nil {
		return MealsReport{}, err
	}
	out := MealsReport{From: fd, To: td, Rows: []MealRow{}, Totals: map[string]MealRow{}}
	type key struct {
		dept    int64
		purpose string
	}
	at := map[key]int{}
	for _, r := range rows {
		k := key{idKey(r.DepartmentID), r.Purpose}
		i, ok := at[k]
		if !ok {
			i = len(out.Rows)
			at[k] = i
			out.Rows = append(out.Rows, MealRow{DepartmentID: r.DepartmentID, DepartmentName: r.DepartmentName, Purpose: r.Purpose})
		}
		sign := 1
		if r.IsReversal {
			sign = -1
		}
		m := &out.Rows[i]
		m.Docs += sign
		m.Covers += sign * int(r.Covers)
		m.Value = m.Value.Add(r.Value)
		t := out.Totals[r.Purpose]
		t.Purpose = r.Purpose
		t.Docs += sign
		t.Covers += sign * int(r.Covers)
		t.Value = t.Value.Add(r.Value)
		out.Totals[r.Purpose] = t
	}
	for i := range out.Rows {
		out.Rows[i].CostPerCover = perUnit(out.Rows[i].Value, out.Rows[i].Covers)
	}
	for k, t := range out.Totals {
		t.CostPerCover = perUnit(t.Value, t.Covers)
		out.Totals[k] = t
	}
	return out, nil
}

// ---------------------------------------------------------------- the documents behind a day of the daily cost

// CostDayDoc is a document (or a group of movements) that made cost on a day.
type CostDayDoc struct {
	CostKind   string `json:"cost_kind"`
	SourceType string `json:"source_type"`
	SourceID   int64  `json:"source_id"`
	// LinkID is the id of the page of the document: the bill for a POS sale, the document itself for the others.
	LinkID         int64           `json:"link_id"`
	Number         string          `json:"number"`
	DepartmentName string          `json:"department_name"`
	OutletName     string          `json:"outlet_name"`
	Lines          int             `json:"lines"`
	Amount         decimal.Decimal `json:"amount"`
}

// CostDayKind is one kind of cost of the day with its documents.
type CostDayKind struct {
	CostKind string          `json:"cost_kind"`
	Total    decimal.Decimal `json:"total"`
	Docs     []CostDayDoc    `json:"docs"`
}

// CostDayReport is what made the cost of one business day, by kind of cost and document.
type CostDayReport struct {
	Date  civil.Date      `json:"date"`
	Kinds []CostDayKind   `json:"kinds"`
	Total decimal.Decimal `json:"total"`
}

// CostDay answers the documents and movements of a day of the daily cost (inventory.view). Its totals per kind are the columns of the daily cost for the same day.
func (s *Service) CostDay(ctx context.Context, propertyID int64, date civil.Date) (CostDayReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return CostDayReport{}, err
	}
	open, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return CostDayReport{}, err
	}
	if date.After(open.BusinessDate) {
		return CostDayReport{}, invalid("report", fieldErr("date", "OUT_OF_RANGE", "not after the open business day"))
	}
	rows, err := s.q(ctx).CostDayDocuments(ctx, inventorydb.CostDayDocumentsParams{TenantID: p.TenantID, PropertyID: propertyID, Day: date})
	if err != nil {
		return CostDayReport{}, err
	}
	out := CostDayReport{Date: date, Kinds: []CostDayKind{}}
	at := map[string]int{}
	for _, r := range rows {
		i, ok := at[r.CostKind]
		if !ok {
			i = len(out.Kinds)
			at[r.CostKind] = i
			out.Kinds = append(out.Kinds, CostDayKind{CostKind: r.CostKind, Docs: []CostDayDoc{}})
		}
		k := &out.Kinds[i]
		k.Docs = append(k.Docs, CostDayDoc{CostKind: r.CostKind, SourceType: r.SourceType, SourceID: r.SourceID, LinkID: r.LinkID, Number: r.Number, DepartmentName: r.DepartmentName, OutletName: r.OutletName, Lines: int(r.Lines), Amount: r.Amount})
		k.Total = k.Total.Add(r.Amount)
		out.Total = out.Total.Add(r.Amount)
	}
	order := map[string]int{}
	for i, k := range costKinds {
		order[k] = i
	}
	sort.SliceStable(out.Kinds, func(a, b int) bool { return order[out.Kinds[a].CostKind] < order[out.Kinds[b].CostKind] })
	for i := range out.Kinds {
		sort.SliceStable(out.Kinds[i].Docs, func(a, b int) bool {
			return out.Kinds[i].Docs[a].Amount.Abs().GreaterThan(out.Kinds[i].Docs[b].Amount.Abs())
		})
	}
	return out, nil
}
