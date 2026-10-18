package inventory

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The snapshots of the day close (35-inventory.md 3.6, 5.7, 12): the stock at the end of the day, the daily cost of all costs and the food and beverage cost beside the revenue.
// A closed day never changes (the ledger is append-only, the day trigger refuses a posting dated a closed day), so the figures of a day are a function of the ledger and of the POS lines:
// dayFigures is that function, the close stores its result, and a report reads the stored rows for the days that have them and computes the other days (the open day and the days that closed before
// the snapshots existed) with the same function. Storing a day twice writes the same rows: the day is deleted and written again.

// Cost kinds of inv_cost_days.
const (
	CostSaleFood     = "SALE_COST_FOOD"
	CostSaleBeverage = "SALE_COST_BEVERAGE"
	CostWaste        = "WASTE"
	CostStaffMeal    = "STAFF_MEAL"
	CostEntertain    = "ENTERTAINMENT"
	CostAmenity      = "AMENITY"
	CostEngineering  = "ENGINEERING"
	CostOtherUse     = "OTHER_USE"
	CostCountVar     = "COUNT_VARIANCE"
	CostPriceVar     = "PRICE_VARIANCE"
)

// costKinds is the order of the columns of the daily cost.
var costKinds = []string{CostSaleFood, CostSaleBeverage, CostWaste, CostStaffMeal, CostEntertain, CostAmenity, CostEngineering, CostOtherUse, CostCountVar, CostPriceVar}

// CostDayRow is the cost of one kind on one day, for a department and an outlet (either may be absent). The amount is signed: a gain or a favourable variance is negative.
type CostDayRow struct {
	Date         civil.Date      `json:"date"`
	DepartmentID *int64          `json:"department_id"`
	OutletID     *int64          `json:"outlet_id"`
	Kind         string          `json:"kind"`
	Amount       decimal.Decimal `json:"amount"`
}

// FbDayRow is the food or beverage figures of one day at one outlet (nil: warehouses without an outlet).
type FbDayRow struct {
	Date              civil.Date      `json:"date"`
	OutletID          *int64          `json:"outlet_id"`
	Class             string          `json:"class"`
	Revenue           decimal.Decimal `json:"revenue_net"`
	NoRecipeRevenue   decimal.Decimal `json:"no_recipe_revenue"`
	SaleCost          decimal.Decimal `json:"sale_cost"`
	WasteCost         decimal.Decimal `json:"waste_cost"`
	StaffMealCost     decimal.Decimal `json:"staff_meal_cost"`
	EntertainmentCost decimal.Decimal `json:"entertainment_cost"`
}

// DayFigures are the rows of a range of days, stored or computed.
type DayFigures struct {
	Cost []CostDayRow
	Fb   []FbDayRow
	// Stored holds the days whose rows came from the snapshot; every other day of the range was computed from the ledger.
	Stored map[civil.Date]bool
}

type costKey struct {
	date    civil.Date
	dept    int64
	outlet  int64
	kind    string
	hasDept bool
	hasOut  bool
}

type fbKey struct {
	date   civil.Date
	outlet int64
	hasOut bool
	class  string
}

func idOrZero(p *int64) (int64, bool) {
	if p == nil {
		return 0, false
	}
	return *p, true
}

func idPtr(id int64, has bool) *int64 {
	if !has {
		return nil
	}
	v := id
	return &v
}

// dayFigures computes the cost rows and the food and beverage rows of the days from..to from the ledger and the POS lines. It reads only.
func (s *Service) dayFigures(ctx context.Context, tenantID, propertyID int64, from, to civil.Date) (DayFigures, error) {
	q := s.q(ctx)
	mv, err := q.CostMovementsOfRange(ctx, inventorydb.CostMovementsOfRangeParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return DayFigures{}, err
	}
	rev, err := q.FbRevenueOfRange(ctx, inventorydb.FbRevenueOfRangeParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return DayFigures{}, err
	}
	costs := map[costKey]decimal.Decimal{}
	fbs := map[fbKey]*FbDayRow{}
	fb := func(d civil.Date, outlet *int64, class string) *FbDayRow {
		id, has := idOrZero(outlet)
		k := fbKey{date: d, outlet: id, hasOut: has, class: class}
		r, ok := fbs[k]
		if !ok {
			r = &FbDayRow{Date: d, OutletID: idPtr(id, has), Class: class}
			fbs[k] = r
		}
		return r
	}
	for _, m := range mv {
		dept, hasDept := idOrZero(m.DepartmentID)
		out, hasOut := idOrZero(m.OutletID)
		k := costKey{date: m.BusinessDate, dept: dept, outlet: out, kind: m.CostKind, hasDept: hasDept, hasOut: hasOut}
		costs[k] = costs[k].Add(m.Amount)
		if m.CostClass != "FOOD" && m.CostClass != "BEVERAGE" {
			continue
		}
		switch m.CostKind {
		case CostSaleFood, CostSaleBeverage:
			r := fb(m.BusinessDate, m.OutletID, m.CostClass)
			r.SaleCost = r.SaleCost.Add(m.Amount)
		case CostWaste:
			r := fb(m.BusinessDate, m.OutletID, m.CostClass)
			r.WasteCost = r.WasteCost.Add(m.Amount)
		case CostStaffMeal:
			r := fb(m.BusinessDate, m.OutletID, m.CostClass)
			r.StaffMealCost = r.StaffMealCost.Add(m.Amount)
		case CostEntertain:
			r := fb(m.BusinessDate, m.OutletID, m.CostClass)
			r.EntertainmentCost = r.EntertainmentCost.Add(m.Amount)
		}
	}
	for _, r := range rev {
		if r.Class != "FOOD" && r.Class != "BEVERAGE" {
			continue
		}
		row := fb(r.BusinessDate, r.OutletID, r.Class)
		row.Revenue = row.Revenue.Add(r.RevenueNet)
		row.NoRecipeRevenue = row.NoRecipeRevenue.Add(r.NoRecipeRevenue)
	}
	out := DayFigures{Stored: map[civil.Date]bool{}}
	for k, v := range costs {
		if v.IsZero() {
			continue
		}
		out.Cost = append(out.Cost, CostDayRow{Date: k.date, DepartmentID: idPtr(k.dept, k.hasDept), OutletID: idPtr(k.outlet, k.hasOut), Kind: k.kind, Amount: v})
	}
	sort.Slice(out.Cost, func(i, j int) bool {
		a, b := out.Cost[i], out.Cost[j]
		if c := a.Date.Compare(b.Date); c != 0 {
			return c < 0
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return idKey(a.DepartmentID)*1_000_000+idKey(a.OutletID) < idKey(b.DepartmentID)*1_000_000+idKey(b.OutletID)
	})
	for _, r := range fbs {
		out.Fb = append(out.Fb, *r)
	}
	sort.Slice(out.Fb, func(i, j int) bool {
		a, b := out.Fb[i], out.Fb[j]
		if c := a.Date.Compare(b.Date); c != 0 {
			return c < 0
		}
		if idKey(a.OutletID) != idKey(b.OutletID) {
			return idKey(a.OutletID) < idKey(b.OutletID)
		}
		return a.Class < b.Class
	})
	return out, nil
}

func idKey(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// TakeSnapshots stores the figures of the business day at its close (5.7 step 3: after the POS queue is drained and the amenities are posted, before the journal). Taking a day again writes the same rows
// (idempotent): the day is deleted and written again. A property that has never had a stock movement has nothing to store.
func (s *Service) TakeSnapshots(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date) error {
	q := s.q(ctx)
	used, err := q.HasMovements(ctx, propertyID)
	if err != nil || !used {
		return err
	}
	for _, del := range []func() error{
		func() error {
			return q.DeleteCostDaysOfDay(ctx, inventorydb.DeleteCostDaysOfDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd})
		},
		func() error {
			return q.DeleteFbCostDaysOfDay(ctx, inventorydb.DeleteFbCostDaysOfDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd})
		},
		func() error {
			return q.DeleteBalanceDaysOfDay(ctx, inventorydb.DeleteBalanceDaysOfDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd})
		},
		func() error {
			return q.DeleteSnapshotDay(ctx, inventorydb.DeleteSnapshotDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd})
		},
	} {
		if err := del(); err != nil {
			return err
		}
	}
	f, err := s.dayFigures(ctx, p.TenantID, propertyID, bd, bd)
	if err != nil {
		return err
	}
	for _, c := range f.Cost {
		if err := q.InsertCostDay(ctx, inventorydb.InsertCostDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd, DepartmentID: c.DepartmentID, OutletID: c.OutletID, CostKind: c.Kind, Amount: c.Amount}); err != nil {
			return err
		}
	}
	for _, r := range f.Fb {
		if err := q.InsertFbCostDay(ctx, inventorydb.InsertFbCostDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd, OutletID: r.OutletID, Class: r.Class, RevenueNet: r.Revenue,
			NoRecipeRevenue: r.NoRecipeRevenue, SaleCost: r.SaleCost, WasteCost: r.WasteCost, StaffMealCost: r.StaffMealCost, EntertainmentCost: r.EntertainmentCost}); err != nil {
			return err
		}
	}
	if err := q.InsertBalanceDaysOfDay(ctx, inventorydb.InsertBalanceDaysOfDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd}); err != nil {
		return err
	}
	return q.InsertSnapshotDay(ctx, inventorydb.InsertSnapshotDayParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd})
}

// figuresOf answers the rows of the days from..to: the stored rows of the days the close has taken, and the figures computed from the ledger for every other day.
func (s *Service) figuresOf(ctx context.Context, tenantID, propertyID int64, from, to civil.Date) (DayFigures, error) {
	q := s.q(ctx)
	days, err := q.ListSnapshotDays(ctx, inventorydb.ListSnapshotDaysParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return DayFigures{}, err
	}
	out := DayFigures{Stored: map[civil.Date]bool{}}
	for _, d := range days {
		out.Stored[d] = true
	}
	if len(days) > 0 {
		cost, err := q.ListCostDays(ctx, inventorydb.ListCostDaysParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
		if err != nil {
			return DayFigures{}, err
		}
		for _, c := range cost {
			out.Cost = append(out.Cost, CostDayRow{Date: c.BusinessDate, DepartmentID: c.DepartmentID, OutletID: c.OutletID, Kind: c.CostKind, Amount: c.Amount})
		}
		fb, err := q.ListFbCostDays(ctx, inventorydb.ListFbCostDaysParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to})
		if err != nil {
			return DayFigures{}, err
		}
		for _, r := range fb {
			out.Fb = append(out.Fb, FbDayRow{Date: r.BusinessDate, OutletID: r.OutletID, Class: r.Class, Revenue: r.RevenueNet, NoRecipeRevenue: r.NoRecipeRevenue, SaleCost: r.SaleCost,
				WasteCost: r.WasteCost, StaffMealCost: r.StaffMealCost, EntertainmentCost: r.EntertainmentCost})
		}
	}
	// the days without a snapshot: the first and the last of them bound the one computation
	var first, last civil.Date
	for d := from; !d.After(to); d = d.AddDays(1) {
		if out.Stored[d] {
			continue
		}
		if first.IsZero() {
			first = d
		}
		last = d
	}
	if first.IsZero() {
		return out, nil
	}
	live, err := s.dayFigures(ctx, tenantID, propertyID, first, last)
	if err != nil {
		return DayFigures{}, err
	}
	for _, c := range live.Cost {
		if !out.Stored[c.Date] {
			out.Cost = append(out.Cost, c)
		}
	}
	for _, r := range live.Fb {
		if !out.Stored[r.Date] {
			out.Fb = append(out.Fb, r)
		}
	}
	return out, nil
}
