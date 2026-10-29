package assets

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The reports of the register (docs/architecture/37-fixed-assets.md 4.12). All of them read the ledger and the masters, never write, and need one of the asset permissions. The CSV and the print view
// are made by the handler and the screen from the same answers.

// ---------------------------------------------------------------------------------------------------------------------
// The register as of a date

// RegisterFilter picks what the register report lists.
type RegisterFilter struct {
	AsOf         *civil.Date
	CategoryID   *int64
	LocationID   *int64
	DepartmentID *int64
}

// RegisterRow is an asset as it stood at the date: the figures are the sums of its ledger up to the date. The place and the department are the current ones.
type RegisterRow struct {
	AssetID         int64           `json:"asset_id"`
	Number          string          `json:"number"`
	Name            string          `json:"name"`
	CategoryCode    string          `json:"category_code"`
	CategoryName    string          `json:"category_name"`
	LocationName    *string         `json:"location_name"`
	DepartmentName  *string         `json:"department_name"`
	SupplierName    *string         `json:"supplier_name"`
	AcquisitionDate civil.Date      `json:"acquisition_date"`
	InServiceDate   civil.Date      `json:"in_service_date"`
	Cost            decimal.Decimal `json:"cost"`
	AccumDepr       decimal.Decimal `json:"accum_depr"`
	CarryingValue   decimal.Decimal `json:"carrying_value"`
	LifeMonths      *int32          `json:"life_months"`
	MonthsDone      int             `json:"months_done"`
	MonthsLeft      *int            `json:"months_left"`
	Method          *string         `json:"method"`
	Status          string          `json:"status"`
	SerialNo        *string         `json:"serial_no"`
	WarrantyUntil   *civil.Date     `json:"warranty_until"`
}

// RegisterReport is the register as of a date with its totals.
type RegisterReport struct {
	AsOf          civil.Date      `json:"as_of"`
	Rows          []RegisterRow   `json:"rows"`
	Count         int             `json:"count"`
	Cost          decimal.Decimal `json:"cost"`
	AccumDepr     decimal.Decimal `json:"accum_depr"`
	CarryingValue decimal.Decimal `json:"carrying_value"`
}

// RegisterReport lists the assets as they stood on a date (asset.read): an asset acquired later is not in it, a disposed one is while the date is before the disposal.
func (s *Service) RegisterReport(ctx context.Context, propertyID int64, f RegisterFilter) (RegisterReport, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return RegisterReport{}, err
	}
	asOf, err := s.reportDate(ctx, propertyID, f.AsOf)
	if err != nil {
		return RegisterReport{}, err
	}
	rows, err := s.q(ctx).RegisterAsOf(ctx, assetsdb.RegisterAsOfParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: asOf, CategoryID: f.CategoryID, LocationID: f.LocationID, DepartmentID: f.DepartmentID})
	if err != nil {
		return RegisterReport{}, err
	}
	out := RegisterReport{AsOf: asOf, Rows: make([]RegisterRow, 0, len(rows))}
	for _, r := range rows {
		row := RegisterRow{AssetID: r.ID, Number: r.Number, Name: r.Name, CategoryCode: r.CategoryCode, CategoryName: r.CategoryName, LocationName: r.LocationName, DepartmentName: r.DepartmentName,
			SupplierName: r.SupplierName, AcquisitionDate: r.AcquisitionDate, InServiceDate: r.InServiceDate, Cost: r.Cost, AccumDepr: r.Accum, CarryingValue: r.Cost.Sub(r.Accum),
			LifeMonths: r.LifeMonths, MonthsDone: int(r.MonthsDone), Method: r.Method, Status: r.Status, SerialNo: r.SerialNo, WarrantyUntil: r.WarrantyUntil}
		if r.LifeMonths != nil {
			left := int(*r.LifeMonths) - int(r.MonthsDone)
			row.MonthsLeft = &left
		}
		out.Rows = append(out.Rows, row)
		out.Cost, out.AccumDepr = out.Cost.Add(r.Cost), out.AccumDepr.Add(r.Accum)
	}
	out.Count, out.CarryingValue = len(out.Rows), out.Cost.Sub(out.AccumDepr)
	return out, nil
}

// reportDate is the date asked for, else the business date of the property.
func (s *Service) reportDate(ctx context.Context, propertyID int64, d *civil.Date) (civil.Date, error) {
	if d != nil {
		return *d, nil
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return civil.Date{}, err
	}
	return day.BusinessDate, nil
}

// ---------------------------------------------------------------------------------------------------------------------
// The depreciation schedule of one asset or of a category

// maxScheduleAssets is how many assets one category schedule lists.
const maxScheduleAssets = 300

// ScheduleReport lists the schedules of one asset, or of the active assets of a category (asset.read): the months posted and the months to come, by the function of the run.
func (s *Service) ScheduleReport(ctx context.Context, propertyID int64, assetID, categoryID *int64) ([]AssetSchedule, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	switch {
	case assetID != nil:
		one, err := s.AssetSchedule(ctx, propertyID, *assetID)
		return []AssetSchedule{one}, err
	case categoryID != nil:
		status := statusActive
		rows, err := s.q(ctx).ListAssets(ctx, assetsdb.ListAssetsParams{TenantID: p.TenantID, PropertyID: propertyID, CategoryID: categoryID, Status: &status, RowLimit: maxScheduleAssets, RowOffset: 0})
		if err != nil {
			return nil, err
		}
		out := make([]AssetSchedule, 0, len(rows))
		for _, r := range rows {
			one, err := s.AssetSchedule(ctx, propertyID, r.ID)
			if err != nil {
				return nil, err
			}
			if one.Depreciable {
				out = append(out, one)
			}
		}
		return out, nil
	}
	return nil, invalid("report", fieldErr("asset_id", "REQUIRED", "an asset or a category"))
}

// ---------------------------------------------------------------------------------------------------------------------
// The recap by category and the reconciliation with the general ledger (5.3)

// DifferenceCause is a part of the difference between the general ledger and the register that has a known cause. The amount is its share of "general ledger less register".
type DifferenceCause struct {
	Code   string          `json:"code"` // WAITING_LIST, DISMISSED, MANUAL_JOURNAL, REGISTER_ONLY or OTHER
	Amount decimal.Decimal `json:"amount"`
	Lines  int             `json:"lines,omitempty"`
}

const (
	causeWaiting      = "WAITING_LIST"
	causeDismissed    = "DISMISSED"
	causeManual       = "MANUAL_JOURNAL"
	causeRegisterOnly = "REGISTER_ONLY"
	causeOther        = "OTHER"
)

// categoryCauses are the amounts the register does not know, per category, up to a date.
type categoryCauses struct {
	waiting, dismissed         decimal.Decimal
	waitingLines, dismissedLns int
	manualCost, manualAccum    decimal.Decimal // the net of manual journals on the accounts, less what the lists above already count
	registerOnlyCost           decimal.Decimal
	registerOnlyAccum          decimal.Decimal
}

// causesOf reads what explains a difference between the general ledger and the register, per category, as of a date.
func (s *Service) causesOf(ctx context.Context, tenantID, propertyID int64, cats []assetsdb.ListCategoriesRow, asOf civil.Date) (map[int64]*categoryCauses, error) {
	q := s.q(ctx)
	out := map[int64]*categoryCauses{}
	for _, c := range cats {
		out[c.ID] = &categoryCauses{}
	}
	waiting, err := q.WaitingByCategory(ctx, assetsdb.WaitingByCategoryParams{TenantID: tenantID, PropertyID: propertyID, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	manualInWaiting := map[int64]decimal.Decimal{}
	for _, w := range waiting {
		c := out[w.CategoryID]
		if c == nil {
			continue
		}
		if w.Dismissed {
			c.dismissed, c.dismissedLns = c.dismissed.Add(w.Amount), c.dismissedLns+int(w.LineCount)
		} else {
			c.waiting, c.waitingLines = c.waiting.Add(w.Amount), c.waitingLines+int(w.LineCount)
		}
		if w.Source == "MANUAL" {
			manualInWaiting[w.CategoryID] = manualInWaiting[w.CategoryID].Add(w.Amount)
		}
	}
	var ids []int64
	for _, c := range cats {
		ids = append(ids, c.CostAccountID)
		if c.AccumulatedAccountID != nil {
			ids = append(ids, *c.AccumulatedAccountID)
		}
	}
	if len(ids) > 0 {
		rows, err := q.ManualJournalBalances(ctx, assetsdb.ManualJournalBalancesParams{TenantID: tenantID, PropertyID: propertyID, AccountIds: ids, AsOf: asOf})
		if err != nil {
			return nil, err
		}
		bal := map[int64]decimal.Decimal{}
		for _, r := range rows {
			bal[r.AccountID] = r.Balance
		}
		for _, c := range cats {
			cc := out[c.ID]
			cc.manualCost = bal[c.CostAccountID].Sub(manualInWaiting[c.ID])
			if c.AccumulatedAccountID != nil {
				cc.manualAccum = bal[*c.AccumulatedAccountID].Neg()
			}
		}
	}
	only, err := q.RegisterOnlyByCategory(ctx, assetsdb.RegisterOnlyByCategoryParams{TenantID: tenantID, PropertyID: propertyID, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	for _, r := range only {
		if c := out[r.CategoryID]; c != nil {
			c.registerOnlyCost, c.registerOnlyAccum = r.Cost, r.Accum
		}
	}
	return out, nil
}

// costCauses names the parts of the cost difference (general ledger less register); what no cause explains is OTHER.
func (c *categoryCauses) costCauses(diff decimal.Decimal) []DifferenceCause {
	out := []DifferenceCause{}
	rest := diff
	add := func(code string, amount decimal.Decimal, lines int) {
		if amount.IsZero() {
			return
		}
		out = append(out, DifferenceCause{Code: code, Amount: amount, Lines: lines})
		rest = rest.Sub(amount)
	}
	add(causeWaiting, c.waiting, c.waitingLines)
	add(causeDismissed, c.dismissed, c.dismissedLns)
	add(causeManual, c.manualCost, 0)
	add(causeRegisterOnly, c.registerOnlyCost.Neg(), 0)
	if !rest.IsZero() {
		out = append(out, DifferenceCause{Code: causeOther, Amount: rest})
	}
	return out
}

// accumCauses names the parts of the accumulated depreciation difference.
func (c *categoryCauses) accumCauses(diff decimal.Decimal) []DifferenceCause {
	out := []DifferenceCause{}
	rest := diff
	add := func(code string, amount decimal.Decimal) {
		if amount.IsZero() {
			return
		}
		out = append(out, DifferenceCause{Code: code, Amount: amount})
		rest = rest.Sub(amount)
	}
	add(causeManual, c.manualAccum)
	add(causeRegisterOnly, c.registerOnlyAccum.Neg())
	if !rest.IsZero() {
		out = append(out, DifferenceCause{Code: causeOther, Amount: rest})
	}
	return out
}

// RecapRow is a category for a period: the movement of the register, the balances of the general ledger and the difference with its causes.
type RecapRow struct {
	CategoryID         int64             `json:"category_id"`
	CategoryCode       string            `json:"category_code"`
	CategoryName       string            `json:"category_name"`
	CostAccountCode    string            `json:"cost_account_code"`
	AccumAccountCode   *string           `json:"accumulated_account_code"`
	CostOpening        decimal.Decimal   `json:"cost_opening"`
	CostAdditions      decimal.Decimal   `json:"cost_additions"`
	CostReclass        decimal.Decimal   `json:"cost_reclass"`
	CostDisposals      decimal.Decimal   `json:"cost_disposals"`
	CostClosing        decimal.Decimal   `json:"cost_closing"`
	AccumOpening       decimal.Decimal   `json:"accum_opening"`
	AccumAdditions     decimal.Decimal   `json:"accum_additions"`
	AccumDepreciation  decimal.Decimal   `json:"accum_depreciation"`
	AccumReclass       decimal.Decimal   `json:"accum_reclass"`
	AccumDisposals     decimal.Decimal   `json:"accum_disposals"`
	AccumClosing       decimal.Decimal   `json:"accum_closing"`
	CarryingClosing    decimal.Decimal   `json:"carrying_closing"`
	LedgerCostOpening  decimal.Decimal   `json:"ledger_cost_opening"`
	LedgerCostClosing  decimal.Decimal   `json:"ledger_cost_closing"`
	LedgerAccumOpening decimal.Decimal   `json:"ledger_accum_opening"`
	LedgerAccumClosing decimal.Decimal   `json:"ledger_accum_closing"`
	CostDifference     decimal.Decimal   `json:"cost_difference"`  // general ledger less register, at the end of the period
	AccumDifference    decimal.Decimal   `json:"accum_difference"` // idem
	CostCauses         []DifferenceCause `json:"cost_causes"`
	AccumCauses        []DifferenceCause `json:"accum_causes"`
}

// RecapReport is the recap by category.
type RecapReport struct {
	DateFrom civil.Date `json:"date_from"`
	DateTo   civil.Date `json:"date_to"`
	Rows     []RecapRow `json:"rows"`
	Total    RecapRow   `json:"total"`
	// Account1590 is the balance (credit positive) of the old single accumulated depreciation account, which belongs to no category.
	Account1590 decimal.Decimal `json:"account_1590"`
}

// RecapReport is the recap of the register by category for a period, beside the general ledger (asset.read).
func (s *Service) RecapReport(ctx context.Context, propertyID int64, from, to *civil.Date) (RecapReport, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return RecapReport{}, err
	}
	end, err := s.reportDate(ctx, propertyID, to)
	if err != nil {
		return RecapReport{}, err
	}
	start := civil.NewDate(end.Year(), 1, 1)
	if from != nil {
		start = *from
	}
	if start.After(end) {
		return RecapReport{}, invalid("period", fieldErr("date_from", "INVALID_VALUE", "on or before the end date"))
	}
	if end.DaysUntil(start) < -3660 {
		return RecapReport{}, invalid("period", fieldErr("date_from", "INVALID_VALUE", "a period of ten years at most"))
	}
	q := s.q(ctx)
	cats, err := q.ListCategories(ctx, assetsdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return RecapReport{}, err
	}
	moves, err := q.RecapMovements(ctx, assetsdb.RecapMovementsParams{TenantID: p.TenantID, PropertyID: propertyID, DateFrom: start, DateTo: end})
	if err != nil {
		return RecapReport{}, err
	}
	byCat := map[int64]assetsdb.RecapMovementsRow{}
	for _, m := range moves {
		byCat[m.CategoryID] = m
	}
	var ids []int64
	for _, c := range cats {
		ids = append(ids, c.CostAccountID)
		if c.AccumulatedAccountID != nil {
			ids = append(ids, *c.AccumulatedAccountID)
		}
	}
	balance := func(asOf civil.Date) (map[int64]decimal.Decimal, error) {
		out := map[int64]decimal.Decimal{}
		if len(ids) == 0 {
			return out, nil
		}
		rows, err := q.AccountBalances(ctx, assetsdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountIds: ids, AsOf: asOf})
		for _, r := range rows {
			out[r.AccountID] = r.Balance
		}
		return out, err
	}
	glOpen, err := balance(start.AddDays(-1))
	if err != nil {
		return RecapReport{}, err
	}
	glClose, err := balance(end)
	if err != nil {
		return RecapReport{}, err
	}
	causes, err := s.causesOf(ctx, p.TenantID, propertyID, cats, end)
	if err != nil {
		return RecapReport{}, err
	}
	out := RecapReport{DateFrom: start, DateTo: end, Rows: []RecapRow{}}
	for _, c := range cats {
		m, has := byCat[c.ID]
		r := RecapRow{CategoryID: c.ID, CategoryCode: c.Code, CategoryName: c.Name, CostAccountCode: c.CostAccountCode, AccumAccountCode: c.AccumulatedAccountCode,
			CostOpening: m.CostOpening, CostAdditions: m.CostAdditions, CostReclass: m.CostReclass, CostDisposals: m.CostDisposals, CostClosing: m.CostClosing,
			AccumOpening: m.AccumOpening, AccumAdditions: m.AccumAdditions, AccumDepreciation: m.AccumDepreciation, AccumReclass: m.AccumReclass, AccumDisposals: m.AccumDisposals,
			AccumClosing: m.AccumClosing, CarryingClosing: m.CostClosing.Sub(m.AccumClosing), LedgerCostOpening: glOpen[c.CostAccountID], LedgerCostClosing: glClose[c.CostAccountID]}
		if c.AccumulatedAccountID != nil {
			r.LedgerAccumOpening, r.LedgerAccumClosing = glOpen[*c.AccumulatedAccountID].Neg(), glClose[*c.AccumulatedAccountID].Neg()
		}
		r.CostDifference, r.AccumDifference = r.LedgerCostClosing.Sub(r.CostClosing), r.LedgerAccumClosing.Sub(r.AccumClosing)
		r.CostCauses, r.AccumCauses = causes[c.ID].costCauses(r.CostDifference), causes[c.ID].accumCauses(r.AccumDifference)
		if !has && r.LedgerCostClosing.IsZero() && r.LedgerAccumClosing.IsZero() && r.LedgerCostOpening.IsZero() && r.LedgerAccumOpening.IsZero() && !c.IsActive {
			continue
		}
		out.Rows = append(out.Rows, r)
		t := &out.Total
		t.CostOpening, t.CostAdditions, t.CostReclass = t.CostOpening.Add(r.CostOpening), t.CostAdditions.Add(r.CostAdditions), t.CostReclass.Add(r.CostReclass)
		t.CostDisposals, t.CostClosing = t.CostDisposals.Add(r.CostDisposals), t.CostClosing.Add(r.CostClosing)
		t.AccumOpening, t.AccumAdditions, t.AccumDepreciation = t.AccumOpening.Add(r.AccumOpening), t.AccumAdditions.Add(r.AccumAdditions), t.AccumDepreciation.Add(r.AccumDepreciation)
		t.AccumReclass, t.AccumDisposals, t.AccumClosing = t.AccumReclass.Add(r.AccumReclass), t.AccumDisposals.Add(r.AccumDisposals), t.AccumClosing.Add(r.AccumClosing)
		t.CarryingClosing = t.CarryingClosing.Add(r.CarryingClosing)
		t.LedgerCostOpening, t.LedgerCostClosing = t.LedgerCostOpening.Add(r.LedgerCostOpening), t.LedgerCostClosing.Add(r.LedgerCostClosing)
		t.LedgerAccumOpening, t.LedgerAccumClosing = t.LedgerAccumOpening.Add(r.LedgerAccumOpening), t.LedgerAccumClosing.Add(r.LedgerAccumClosing)
		t.CostDifference, t.AccumDifference = t.CostDifference.Add(r.CostDifference), t.AccumDifference.Add(r.AccumDifference)
	}
	out.Total.CategoryCode = "TOTAL"
	out.Total.CostCauses, out.Total.AccumCauses = []DifferenceCause{}, []DifferenceCause{}
	if a, err := q.GetAccountByCode(ctx, assetsdb.GetAccountByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: "1590"}); err == nil && a.IsPostable {
		if rows, err := q.AccountBalances(ctx, assetsdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountIds: []int64{a.ID}, AsOf: end}); err == nil && len(rows) == 1 {
			out.Account1590 = rows[0].Balance.Neg()
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------------
// By location and by department

// PlacementRow is a location or a department with the assets it holds (active ones, at the current state).
type PlacementRow struct {
	ID            *int64          `json:"id"`
	ParentID      *int64          `json:"parent_id,omitempty"`
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	Kind          string          `json:"kind,omitempty"`
	Active        bool            `json:"is_active"`
	Assets        int             `json:"assets"`
	Cost          decimal.Decimal `json:"cost"`
	AccumDepr     decimal.Decimal `json:"accum_depr"`
	CarryingValue decimal.Decimal `json:"carrying_value"`
	// the same with the sub-departments of a department
	TotalAssets        int             `json:"total_assets"`
	TotalCost          decimal.Decimal `json:"total_cost"`
	TotalAccumDepr     decimal.Decimal `json:"total_accum_depr"`
	TotalCarryingValue decimal.Decimal `json:"total_carrying_value"`
}

// PlacementReport counts the assets by location and by department.
type PlacementReport struct {
	Locations    []PlacementRow `json:"locations"`
	NoLocation   PlacementRow   `json:"no_location"`
	Departments  []PlacementRow `json:"departments"`
	NoDepartment PlacementRow   `json:"no_department"`
}

func placement(id *int64, parent *int64, code, name, kind string, active bool, n int, cost, accum decimal.Decimal) PlacementRow {
	carrying := cost.Sub(accum)
	return PlacementRow{ID: id, ParentID: parent, Code: code, Name: name, Kind: kind, Active: active, Assets: n, Cost: cost, AccumDepr: accum, CarryingValue: carrying,
		TotalAssets: n, TotalCost: cost, TotalAccumDepr: accum, TotalCarryingValue: carrying}
}

// PlacementReport is the count, the cost and the carrying value of the active assets of each location and each department; a department includes its sub-departments (asset.read).
func (s *Service) PlacementReport(ctx context.Context, propertyID int64) (PlacementReport, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return PlacementReport{}, err
	}
	q := s.q(ctx)
	out := PlacementReport{Locations: []PlacementRow{}, Departments: []PlacementRow{}}
	locs, err := q.LocationSummary(ctx, assetsdb.LocationSummaryParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	for _, l := range locs {
		if l.Assets == 0 && !l.IsActive {
			continue
		}
		id := l.LocationID
		out.Locations = append(out.Locations, placement(&id, nil, l.LocationCode, l.LocationName, l.LocationKind, l.IsActive, int(l.Assets), l.Cost, l.Accum))
	}
	un, err := q.UnplacedSummary(ctx, assetsdb.UnplacedSummaryParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	out.NoLocation = placement(nil, nil, "", "", "", true, int(un.Assets), un.Cost, un.Accum)
	depts, err := q.DepartmentSummary(ctx, assetsdb.DepartmentSummaryParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	rows := map[int64]*PlacementRow{}
	var order []int64
	for _, d := range depts {
		id := d.DepartmentID
		r := placement(&id, d.ParentID, d.DepartmentCode, d.DepartmentName, "", true, int(d.Assets), d.Cost, d.Accum)
		rows[id] = &r
		order = append(order, id)
	}
	for _, id := range order {
		r := rows[id]
		if r.ParentID == nil {
			continue
		}
		if parent := rows[*r.ParentID]; parent != nil {
			parent.TotalAssets += r.Assets
			parent.TotalCost, parent.TotalAccumDepr = parent.TotalCost.Add(r.Cost), parent.TotalAccumDepr.Add(r.AccumDepr)
			parent.TotalCarryingValue = parent.TotalCost.Sub(parent.TotalAccumDepr)
		}
	}
	for _, id := range order {
		if r := rows[id]; r.TotalAssets > 0 {
			out.Departments = append(out.Departments, *r)
		}
	}
	nd, err := q.NoDepartmentSummary(ctx, assetsdb.NoDepartmentSummaryParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	out.NoDepartment = placement(nil, nil, "", "", "", true, int(nd.Assets), nd.Cost, nd.Accum)
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------------
// Warranty and life

// ExpiringRow is an active asset whose warranty is about to end, whose life is nearly over, or that is fully depreciated and still in use.
type ExpiringRow struct {
	AssetID          int64           `json:"asset_id"`
	Number           string          `json:"number"`
	Name             string          `json:"name"`
	CategoryCode     string          `json:"category_code"`
	LocationName     *string         `json:"location_name"`
	Cost             decimal.Decimal `json:"cost"`
	CarryingValue    decimal.Decimal `json:"carrying_value"`
	WarrantyUntil    *civil.Date     `json:"warranty_until"`
	WarrantyEnding   bool            `json:"warranty_ending"`
	WarrantyExpired  bool            `json:"warranty_expired"`
	MonthsLeft       *int            `json:"months_left"`
	LifeEnding       bool            `json:"life_ending"`
	FullyDepreciated bool            `json:"fully_depreciated"`
}

// ExpiringReport lists the assets whose warranty ends within days days, or that have monthsLeft months or fewer to depreciate (asset.read).
func (s *Service) ExpiringReport(ctx context.Context, propertyID int64, days, monthsLeft int) ([]ExpiringRow, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	if days < 0 || days > 3660 {
		return nil, invalid("filter", fieldErr("days", "INVALID_VALUE", "0 to 3660 days"))
	}
	if monthsLeft < 0 || monthsLeft > 1200 {
		return nil, invalid("filter", fieldErr("months", "INVALID_VALUE", "0 to 1200 months"))
	}
	today, err := s.reportDate(ctx, propertyID, nil)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ExpiringAssets(ctx, assetsdb.ExpiringAssetsParams{TenantID: p.TenantID, PropertyID: propertyID, WarrantyBefore: today.AddDays(days), MonthsLeft: int32(monthsLeft)}) //nolint:gosec // G115: 0 to 1200
	if err != nil {
		return nil, err
	}
	out := make([]ExpiringRow, 0, len(rows))
	for _, r := range rows {
		x := ExpiringRow{AssetID: r.ID, Number: r.Number, Name: r.Name, CategoryCode: r.CategoryCode, LocationName: r.LocationName, Cost: r.Cost, CarryingValue: r.Cost.Sub(r.AccumDepr), WarrantyUntil: r.WarrantyUntil}
		if r.WarrantyUntil != nil {
			x.WarrantyExpired = r.WarrantyUntil.Before(today)
			x.WarrantyEnding = !x.WarrantyExpired
		}
		if r.LifeMonths != nil {
			left := int(*r.LifeMonths) - int(r.DepMonthsDone)
			x.MonthsLeft = &left
			x.LifeEnding = left <= monthsLeft
			x.FullyDepreciated = r.AccumDepr.GreaterThanOrEqual(r.Cost.Sub(r.ResidualValue))
		}
		out = append(out, x)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number < out[j].Number })
	return out, nil
}

// requireRead is the permission check of the report handlers, made before they read their filters, so a user without the permission gets 403 whatever the query says.
func (s *Service) requireRead(ctx context.Context, propertyID int64) (auth.Principal, error) {
	return s.require(ctx, propertyID, readers...)
}
