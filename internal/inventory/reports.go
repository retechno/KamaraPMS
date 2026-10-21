package inventory

import (
	"context"
	"sort"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// The cost reports (35-inventory.md 12): the daily cost of all costs, the food and beverage cost per outlet and its reconciliation. They read the snapshot rows of the closed days and the ledger for the
// other days (snapshots.go), so a figure is the same whichever way it was read. Every percentage is computed with decimals and rounded to two places when it is answered.

const maxReportDays = 400

var hundred = decimal.NewFromInt(100)

const oneDay = 1

// pct answers num / den as a percentage with two decimals, or nil when the denominator is not positive.
func pct(num, den decimal.Decimal) *decimal.Decimal {
	if !den.IsPositive() {
		return nil
	}
	v := num.Mul(hundred).Div(den).Round(2)
	return &v
}

func startOfMonth(d civil.Date) civil.Date { return civil.NewDate(d.Year(), d.Month(), 1) }

// period resolves the dates of a report: the default is the month of the open business day up to that day.
func (s *Service) period(ctx context.Context, propertyID int64, from, to *civil.Date) (civil.Date, civil.Date, civil.Date, error) {
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return civil.Date{}, civil.Date{}, civil.Date{}, err
	}
	f, t := startOfMonth(day.BusinessDate), day.BusinessDate
	if from != nil {
		f = *from
	}
	if to != nil {
		t = *to
	}
	if t.Before(f) {
		return f, t, day.BusinessDate, invalid("report", fieldErr("to", "BEFORE_FROM", "not before the start of the period"))
	}
	if f.DaysUntil(t) >= maxReportDays {
		return f, t, day.BusinessDate, invalid("report", fieldErr("to", "OUT_OF_RANGE", strconv.Itoa(maxReportDays)+" days at most"))
	}
	return f, t, day.BusinessDate, nil
}

// ---------------------------------------------------------------- targets

// CostTarget is the target cost percentage of food or beverage, for the property (OutletID nil) or for an outlet.
type CostTarget struct {
	OutletID  *int64          `json:"outlet_id"`
	Class     string          `json:"class"`
	TargetPct decimal.Decimal `json:"target_pct"`
}

// ListCostTargets answers the targets of the property (inventory.view).
func (s *Service) ListCostTargets(ctx context.Context, propertyID int64) ([]CostTarget, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	return s.targetsOf(ctx, p.TenantID, propertyID)
}

func (s *Service) targetsOf(ctx context.Context, tenantID, propertyID int64) ([]CostTarget, error) {
	rows, err := s.q(ctx).ListCostTargets(ctx, inventorydb.ListCostTargetsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]CostTarget, 0, len(rows))
	for _, r := range rows {
		out = append(out, CostTarget{OutletID: r.OutletID, Class: r.Class, TargetPct: r.TargetPct})
	}
	return out, nil
}

// SetCostTarget sets the target of a class for the property or one outlet (inventory.manage).
func (s *Service) SetCostTarget(ctx context.Context, propertyID int64, in CostTarget) (CostTarget, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return CostTarget{}, err
	}
	var f []apperr.FieldError
	if in.Class != "FOOD" && in.Class != "BEVERAGE" {
		f = append(f, fieldErr("class", "INVALID_VALUE", "FOOD or BEVERAGE"))
	}
	if !in.TargetPct.IsPositive() || in.TargetPct.GreaterThan(hundred) || !in.TargetPct.Equal(in.TargetPct.Round(2)) {
		f = append(f, fieldErr("target_pct", "OUT_OF_RANGE", "above 0 and up to 100, two decimals at most"))
	}
	if len(f) > 0 {
		return CostTarget{}, invalid("cost target", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		old, _ := s.targetsOf(ctx, p.TenantID, propertyID)
		if err := q.UpsertCostTarget(ctx, inventorydb.UpsertCostTargetParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: in.OutletID, Class: in.Class, TargetPct: in.TargetPct, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.cost_target_set", "inv_cost_target", idKey(in.OutletID), "cost target "+in.Class, targetOf(old, in.OutletID, in.Class), in))
	})
	return in, err
}

func targetOf(list []CostTarget, outlet *int64, class string) *CostTarget {
	for i := range list {
		if list[i].Class == class && idKey(list[i].OutletID) == idKey(outlet) {
			return &list[i]
		}
	}
	return nil
}

// DeleteCostTarget removes a target; an outlet without its own target takes the property's.
func (s *Service) DeleteCostTarget(ctx context.Context, propertyID int64, outlet *int64, class string) error {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return err
	}
	if class != "FOOD" && class != "BEVERAGE" {
		return invalid("cost target", fieldErr("class", "INVALID_VALUE", "FOOD or BEVERAGE"))
	}
	return s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		old, _ := s.targetsOf(ctx, p.TenantID, propertyID)
		n, err := q.DeleteCostTarget(ctx, inventorydb.DeleteCostTargetParams{TenantID: p.TenantID, PropertyID: propertyID, OutletID: outlet, Class: class})
		if err != nil {
			return err
		}
		if n == 0 {
			return notFound("COST_TARGET_NOT_FOUND", "cost target")
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.cost_target_removed", "inv_cost_target", idKey(outlet), "cost target "+class, targetOf(old, outlet, class), nil))
	})
}

// targets answers the target of a class at an outlet: its own, else the property's, else none.
type targets struct{ list []CostTarget }

func (t targets) of(outlet *int64, class string) *decimal.Decimal {
	if outlet != nil {
		if x := targetOf(t.list, outlet, class); x != nil {
			return &x.TargetPct
		}
	}
	if x := targetOf(t.list, nil, class); x != nil {
		return &x.TargetPct
	}
	return nil
}

// weighted is the target of "all outlets": the mean of the targets of the outlets weighted by their revenue (the property's target for an outlet without its own); the property's when there was no revenue.
func (t targets) weighted(rows []FbDayRow, class string) *decimal.Decimal {
	sum, weight := decimal.Zero, decimal.Zero
	for _, r := range rows {
		if r.Class != class || !r.Revenue.IsPositive() {
			continue
		}
		tp := t.of(r.OutletID, class)
		if tp == nil {
			continue
		}
		sum, weight = sum.Add(r.Revenue.Mul(*tp)), weight.Add(r.Revenue)
	}
	if weight.IsPositive() {
		v := sum.Div(weight).Round(2)
		return &v
	}
	return t.of(nil, class)
}

// ---------------------------------------------------------------- the daily cost of all costs

// FbPart is the food or the beverage side of a day or of a period: the cost of sales against the revenue, with the target.
type FbPart struct {
	Revenue decimal.Decimal  `json:"revenue"`
	Cost    decimal.Decimal  `json:"cost"`
	Pct     *decimal.Decimal `json:"pct"`
	Target  *decimal.Decimal `json:"target_pct"`
	Above   bool             `json:"above_target"`
	// MtdPct is the percentage of the month so far, up to this day.
	MtdPct *decimal.Decimal `json:"mtd_pct"`
	// NoRecipeDays counts the days (of this part) with revenue but no cost from recipes: they have no percentage and are left out of Pct and MtdPct.
	NoRecipeDays int `json:"no_recipe_days"`
	// NoRecipeData is true when there is revenue and not one day of it has a cost from recipes: Pct is then empty.
	NoRecipeData bool `json:"no_recipe_data"`
}

// recipeBasis sums the revenue of the days that have a cost of sales from recipes (the basis of a theoretical percentage) and counts the days with revenue and no such cost. The rows are summed per day
// and class: a day with revenue and no cost of sales (the goods sold have no recipe) has no percentage and does not count in it. A date counts once however many classes lack a cost.
func recipeBasis(rows []FbDayRow) (decimal.Decimal, int) {
	type cell struct{ rev, cost decimal.Decimal }
	cells := map[string]*cell{}
	for _, r := range rows {
		k := r.Date.String() + r.Class
		c := cells[k]
		if c == nil {
			c = &cell{}
			cells[k] = c
		}
		c.rev, c.cost = c.rev.Add(r.Revenue), c.cost.Add(r.SaleCost)
	}
	dataRev := decimal.Zero
	days := map[civil.Date]bool{}
	for k, c := range cells {
		switch {
		case c.cost.IsPositive():
			dataRev = dataRev.Add(c.rev)
		case c.rev.IsPositive():
			d, err := civil.ParseDate(k[:10])
			if err == nil {
				days[d] = true
			}
		}
	}
	return dataRev, len(days)
}

// fbCells fills the revenue, the cost and the percentage of the rows of the given classes.
func fbCells(rows []FbDayRow, classes ...string) FbPart {
	var out FbPart
	var sel []FbDayRow
	for _, r := range rows {
		for _, c := range classes {
			if r.Class == c {
				sel = append(sel, r)
				out.Revenue, out.Cost = out.Revenue.Add(r.Revenue), out.Cost.Add(r.SaleCost)
				break
			}
		}
	}
	dataRev, days := recipeBasis(sel)
	out.NoRecipeDays = days
	out.Pct = pct(out.Cost, dataRev)
	out.NoRecipeData = out.Revenue.IsPositive() && out.Pct == nil
	return out
}

// DailyCostDay is one day of the report, and, with the zero date, the figures of a whole period.
type DailyCostDay struct {
	Date     civil.Date                 `json:"date"`
	Live     bool                       `json:"live"`
	Kinds    map[string]decimal.Decimal `json:"kinds"`
	Total    decimal.Decimal            `json:"total"`
	Food     FbPart                     `json:"food"`
	Beverage FbPart                     `json:"beverage"`
	Fb       FbPart                     `json:"fb"`
	Rooms    int                        `json:"occupied_rooms"`
	// CostPerRoom is the amenities cost over the rooms the amenities were made for.
	CostPerRoom *decimal.Decimal           `json:"amenity_cost_per_room"`
	Departments map[string]decimal.Decimal `json:"departments"`
}

// NamedRef is an id with a code and a name, for a column of a report.
type NamedRef struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// DailyCostReport is the report of 12.2: the days of the period, the period, and the month to date.
type DailyCostReport struct {
	From         civil.Date     `json:"from"`
	To           civil.Date     `json:"to"`
	BusinessDate civil.Date     `json:"business_date"`
	MtdFrom      civil.Date     `json:"mtd_from"`
	Days         []DailyCostDay `json:"days"`
	Period       DailyCostDay   `json:"period"`
	Mtd          DailyCostDay   `json:"mtd"`
	Departments  []NamedRef     `json:"departments"`
	Targets      []CostTarget   `json:"targets"`
}

type dayInput struct {
	cost  []CostDayRow
	fb    []FbDayRow
	rooms int
}

func summarizeDays(in dayInput, tg targets) DailyCostDay {
	out := DailyCostDay{Kinds: map[string]decimal.Decimal{}, Departments: map[string]decimal.Decimal{}, Rooms: in.rooms}
	for _, k := range costKinds {
		out.Kinds[k] = decimal.Zero
	}
	for _, c := range in.cost {
		out.Kinds[c.Kind] = out.Kinds[c.Kind].Add(c.Amount)
		out.Total = out.Total.Add(c.Amount)
		key := strconv.FormatInt(idKey(c.DepartmentID), 10)
		out.Departments[key] = out.Departments[key].Add(c.Amount)
	}
	for _, class := range []string{"FOOD", "BEVERAGE"} {
		part := fbCells(in.fb, class)
		part.Target = tg.weighted(in.fb, class)
		part.Above = part.Pct != nil && part.Target != nil && part.Pct.GreaterThan(*part.Target)
		if class == "FOOD" {
			out.Food = part
		} else {
			out.Beverage = part
		}
	}
	out.Fb = fbCells(in.fb, "FOOD", "BEVERAGE")
	if in.rooms > 0 {
		v := out.Kinds[CostAmenity].Div(decimal.NewFromInt(int64(in.rooms))).Round(2)
		out.CostPerRoom = &v
	}
	return out
}

// DailyCost answers the report of 12.2 for the days from..to (the month to date up to the last day is added): inventory.view.
func (s *Service) DailyCost(ctx context.Context, propertyID int64, from, to *civil.Date) (DailyCostReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return DailyCostReport{}, err
	}
	f, t, open, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return DailyCostReport{}, err
	}
	// the month to date of every day needs the days from the first of its month
	window := startOfMonth(f)
	fig, err := s.figuresOf(ctx, p.TenantID, propertyID, window, t)
	if err != nil {
		return DailyCostReport{}, err
	}
	tl, err := s.targetsOf(ctx, p.TenantID, propertyID)
	if err != nil {
		return DailyCostReport{}, err
	}
	tg := targets{list: tl}
	rooms, err := s.q(ctx).AmenityRoomsOfRange(ctx, inventorydb.AmenityRoomsOfRangeParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: window, ToDate: t})
	if err != nil {
		return DailyCostReport{}, err
	}
	roomsOf := map[civil.Date]int{}
	for _, r := range rooms {
		roomsOf[r.BusinessDate] += int(r.Rooms)
	}
	costOf := map[civil.Date][]CostDayRow{}
	for _, c := range fig.Cost {
		costOf[c.Date] = append(costOf[c.Date], c)
	}
	fbOf := map[civil.Date][]FbDayRow{}
	for _, r := range fig.Fb {
		fbOf[r.Date] = append(fbOf[r.Date], r)
	}
	// the sum of a run of days, for the month to date and the period
	run := func(a, b civil.Date) dayInput {
		var in dayInput
		for d := a; !d.After(b); d = d.AddDays(oneDay) {
			in.cost, in.fb, in.rooms = append(in.cost, costOf[d]...), append(in.fb, fbOf[d]...), in.rooms+roomsOf[d]
		}
		return in
	}
	out := DailyCostReport{From: f, To: t, BusinessDate: open, MtdFrom: startOfMonth(t), Days: []DailyCostDay{}, Targets: tl}
	depts := map[int64]bool{}
	for d := f; !d.After(t); d = d.AddDays(oneDay) {
		day := summarizeDays(dayInput{cost: costOf[d], fb: fbOf[d], rooms: roomsOf[d]}, tg)
		day.Date, day.Live = d, !fig.Stored[d] && !d.Before(open) // only the open day is running: an earlier day without a snapshot closed before the snapshots existed
		mtd := summarizeDays(run(startOfMonth(d), d), tg)
		day.Food.MtdPct, day.Beverage.MtdPct, day.Fb.MtdPct = mtd.Food.Pct, mtd.Beverage.Pct, mtd.Fb.Pct
		out.Days = append(out.Days, day)
	}
	for _, c := range fig.Cost {
		if c.DepartmentID != nil && !c.Date.Before(f) {
			depts[*c.DepartmentID] = true
		}
	}
	out.Period = summarizeDays(run(f, t), tg)
	out.Period.Date = t
	out.Mtd = summarizeDays(run(out.MtdFrom, t), tg)
	out.Mtd.Date = t
	out.Mtd.Food.MtdPct, out.Mtd.Beverage.MtdPct, out.Mtd.Fb.MtdPct = out.Mtd.Food.Pct, out.Mtd.Beverage.Pct, out.Mtd.Fb.Pct
	out.Period.Food.MtdPct, out.Period.Beverage.MtdPct, out.Period.Fb.MtdPct = out.Period.Food.Pct, out.Period.Beverage.Pct, out.Period.Fb.Pct
	names, err := s.q(ctx).DepartmentNames(ctx, inventorydb.DepartmentNamesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return DailyCostReport{}, err
	}
	out.Departments = []NamedRef{}
	for _, n := range names {
		if depts[n.ID] {
			out.Departments = append(out.Departments, NamedRef{ID: n.ID, Code: n.Code, Name: n.Name})
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- the check against the general ledger

// GLCheckLine is one control: what the stock says and what the general ledger holds for the same period.
type GLCheckLine struct {
	Key   string          `json:"key"`
	Stock decimal.Decimal `json:"stock"`
	GL    decimal.Decimal `json:"gl"`
	Diff  decimal.Decimal `json:"difference"`
}

// GLCheck compares the cost the stock recorded with the cost accounts of the general ledger for the days that are journaled (6.3). Status is MATCH, DIFFERENCE, or PENDING when none of the days is journaled yet.
type GLCheck struct {
	Status    string          `json:"status"`
	Through   *civil.Date     `json:"gl_through"`
	Lines     []GLCheckLine   `json:"lines"`
	StockCost decimal.Decimal `json:"stock_total"`
	GLCost    decimal.Decimal `json:"gl_total"`
	Diff      decimal.Decimal `json:"difference"`
}

var glKeys = []struct{ key, kind string }{
	{"INV_COGS_FOOD", CostSaleFood}, {"INV_COGS_BEVERAGE", CostSaleBeverage}, {"INV_WASTE", CostWaste}, {"INV_COUNT_VARIANCE", CostCountVar}, {"INV_STAFF_MEAL", CostStaffMeal}, {"INV_ENTERTAINMENT", CostEntertain},
}

func (s *Service) glCheck(ctx context.Context, tenantID, propertyID int64, from, to civil.Date) (GLCheck, error) {
	q := s.q(ctx)
	out := GLCheck{Lines: []GLCheckLine{}}
	last, err := q.LastJournaledDay(ctx, inventorydb.LastJournaledDayParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return out, err
	}
	through := to
	if last.BusinessDate.Before(through) {
		through = last.BusinessDate
	}
	if !last.Found || through.Before(from) {
		out.Status = "PENDING"
		return out, nil
	}
	out.Through = &through
	fig, err := s.figuresOf(ctx, tenantID, propertyID, from, through)
	if err != nil {
		return out, err
	}
	stock := map[string]decimal.Decimal{}
	for _, c := range fig.Cost {
		stock[c.Kind] = stock[c.Kind].Add(c.Amount)
	}
	keys := make([]string, 0, len(glKeys))
	for _, g := range glKeys {
		keys = append(keys, g.key)
	}
	after, err := q.GLKeyBalancesOf(ctx, inventorydb.GLKeyBalancesOfParams{TenantID: tenantID, PropertyID: propertyID, AsOf: through, Keys: keys})
	if err != nil {
		return out, err
	}
	before, err := q.GLKeyBalancesOf(ctx, inventorydb.GLKeyBalancesOfParams{TenantID: tenantID, PropertyID: propertyID, AsOf: from.AddDays(-1), Keys: keys})
	if err != nil {
		return out, err
	}
	gl := map[string]decimal.Decimal{}
	for _, b := range after {
		gl[b.MapKey] = b.Balance
	}
	for _, b := range before {
		gl[b.MapKey] = gl[b.MapKey].Sub(b.Balance)
	}
	mapped := map[string]bool{}
	for _, b := range after {
		mapped[b.MapKey] = true
	}
	out.Status = "MATCH"
	for _, g := range glKeys {
		if !mapped[g.key] {
			continue // a property that has not mapped the account has nothing to compare
		}
		line := GLCheckLine{Key: g.key, Stock: stock[g.kind], GL: gl[g.key]}
		line.Diff = line.Stock.Sub(line.GL)
		if !line.Diff.IsZero() {
			out.Status = "DIFFERENCE"
		}
		out.StockCost, out.GLCost = out.StockCost.Add(line.Stock), out.GLCost.Add(line.GL)
		out.Lines = append(out.Lines, line)
	}
	out.Diff = out.StockCost.Sub(out.GLCost)
	return out, nil
}

// ---------------------------------------------------------------- the reconciliation of the food and beverage cost

// ReconColumn is one column of the reconciliation (12.1): the figures of food, of beverage, or of both.
type ReconColumn struct {
	Opening decimal.Decimal `json:"opening"`
	// OpeningPosted is the opening stock (OPN) posted inside the period: it adds to the stock like a purchase but is not one.
	OpeningPosted    decimal.Decimal  `json:"opening_posted"`
	Purchases        decimal.Decimal  `json:"purchases"`
	TransferIn       decimal.Decimal  `json:"transfer_in"`
	TransferOut      decimal.Decimal  `json:"transfer_out"`
	Closing          decimal.Decimal  `json:"closing"`
	GoodsUsed        decimal.Decimal  `json:"goods_used"`
	StaffMeal        decimal.Decimal  `json:"staff_meal"`
	Entertainment    decimal.Decimal  `json:"entertainment"`
	NetCost          decimal.Decimal  `json:"net_cost"`
	Revenue          decimal.Decimal  `json:"revenue"`
	ActualPct        *decimal.Decimal `json:"actual_pct"`
	Theory           decimal.Decimal  `json:"theory"`
	TheoryPct        *decimal.Decimal `json:"theory_pct"`
	Difference       decimal.Decimal  `json:"difference"`
	DifferencePoints *decimal.Decimal `json:"difference_points"`
	Waste            decimal.Decimal  `json:"waste"`
	CountVariance    decimal.Decimal  `json:"count_variance"`
	NoRecipeEstimate decimal.Decimal  `json:"no_recipe_estimate"`
	OtherOutflows    decimal.Decimal  `json:"other_outflows"`
	TargetPct        *decimal.Decimal `json:"target_pct"`
	// TheoryRevenue is the revenue of the days that have a cost of sales from recipes: the theoretical % is the theory over it, not over all the revenue (the days with no recipe data would pull it down).
	TheoryRevenue decimal.Decimal `json:"theory_revenue"`
	// NoRecipeDays counts the days with revenue and no cost from recipes; they are left out of the theoretical %.
	NoRecipeDays int `json:"no_recipe_days"`
}

// Reconciliation is the report of 12.1 for a period.
type Reconciliation struct {
	From     civil.Date  `json:"from"`
	To       civil.Date  `json:"to"`
	Food     ReconColumn `json:"food"`
	Beverage ReconColumn `json:"beverage"`
	Total    ReconColumn `json:"total"`
	// ClosingBasis is COUNT when every food and beverage warehouse has an approved count on the last day, otherwise BOOK: the closing stock is estimated from the book stock (D21).
	ClosingBasis string  `json:"closing_basis"`
	GL           GLCheck `json:"gl"`
}

func addCols(a, b ReconColumn) ReconColumn {
	return ReconColumn{Opening: a.Opening.Add(b.Opening), OpeningPosted: a.OpeningPosted.Add(b.OpeningPosted), Purchases: a.Purchases.Add(b.Purchases), TransferIn: a.TransferIn.Add(b.TransferIn), TransferOut: a.TransferOut.Add(b.TransferOut),
		Closing: a.Closing.Add(b.Closing), StaffMeal: a.StaffMeal.Add(b.StaffMeal), Entertainment: a.Entertainment.Add(b.Entertainment), Revenue: a.Revenue.Add(b.Revenue),
		Theory: a.Theory.Add(b.Theory), Waste: a.Waste.Add(b.Waste), CountVariance: a.CountVariance.Add(b.CountVariance), NoRecipeEstimate: a.NoRecipeEstimate.Add(b.NoRecipeEstimate),
		TheoryRevenue: a.TheoryRevenue.Add(b.TheoryRevenue)}
}

// finish computes the lines that follow from the others.
func (c *ReconColumn) finish() {
	c.GoodsUsed = c.Opening.Add(c.OpeningPosted).Add(c.Purchases).Add(c.TransferIn).Sub(c.TransferOut).Sub(c.Closing)
	c.NetCost = c.GoodsUsed.Sub(c.StaffMeal).Sub(c.Entertainment)
	c.ActualPct, c.TheoryPct = pct(c.NetCost, c.Revenue), pct(c.Theory, c.TheoryRevenue)
	c.Difference = c.NetCost.Sub(c.Theory)
	if c.ActualPct != nil && c.TheoryPct != nil {
		v := c.ActualPct.Sub(*c.TheoryPct)
		c.DifferencePoints = &v
	}
	c.OtherOutflows = c.Difference.Sub(c.Waste).Sub(c.CountVariance)
}

// noRecipeEstimates estimates, per class, the cost of the sales whose products have no recipe: their revenue at the theoretical percentage. A row that has a cost of sales uses its own percentage; a row
// that has none (a day with no recipe data) uses the percentage of the same outlet and class over the days that have data, else the one of the class. A class with no day of data has no estimate.
func noRecipeEstimates(rows []FbDayRow) map[string]decimal.Decimal {
	type acc struct{ rev, cost decimal.Decimal }
	byOutlet, byClass := map[string]*acc{}, map[string]*acc{}
	add := func(m map[string]*acc, k string, r FbDayRow) {
		a := m[k]
		if a == nil {
			a = &acc{}
			m[k] = a
		}
		a.rev, a.cost = a.rev.Add(r.Revenue), a.cost.Add(r.SaleCost)
	}
	for _, r := range rows {
		if r.SaleCost.IsPositive() {
			add(byOutlet, strconv.FormatInt(idKey(r.OutletID), 10)+r.Class, r)
			add(byClass, r.Class, r)
		}
	}
	out := map[string]decimal.Decimal{}
	for _, r := range rows {
		if !r.NoRecipeRevenue.IsPositive() {
			continue
		}
		var rate *decimal.Decimal
		if r.SaleCost.IsPositive() {
			rate = pct(r.SaleCost, r.Revenue)
		} else if a := byOutlet[strconv.FormatInt(idKey(r.OutletID), 10)+r.Class]; a != nil {
			rate = pct(a.cost, a.rev)
		} else if a := byClass[r.Class]; a != nil {
			rate = pct(a.cost, a.rev)
		}
		if rate != nil {
			out[r.Class] = out[r.Class].Add(r.NoRecipeRevenue.Mul(*rate).Div(hundred))
		}
	}
	return out
}

// FbReconciliation answers the report of 12.1 for the days from..to (inventory.view).
func (s *Service) FbReconciliation(ctx context.Context, propertyID int64, from, to *civil.Date) (Reconciliation, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return Reconciliation{}, err
	}
	f, t, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return Reconciliation{}, err
	}
	q := s.q(ctx)
	cols := map[string]*ReconColumn{"FOOD": {}, "BEVERAGE": {}}
	stock, err := q.ReconStockByClass(ctx, inventorydb.ReconStockByClassParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: f, ToDate: t})
	if err != nil {
		return Reconciliation{}, err
	}
	for _, r := range stock {
		cols[r.ItemClass].Opening, cols[r.ItemClass].Closing = r.Opening, r.Closing
	}
	flows, err := q.ReconFlows(ctx, inventorydb.ReconFlowsParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: f, ToDate: t})
	if err != nil {
		return Reconciliation{}, err
	}
	for _, r := range flows {
		item, cost := cols[r.ItemClass], cols[r.CostClass]
		switch r.Kind {
		case "OPENING":
			item.OpeningPosted = item.OpeningPosted.Add(r.ValueDelta)
		case "RECEIPT", "RECEIPT_REVERSAL", "SUPPLIER_RETURN", "COST_ADJUST":
			item.Purchases = item.Purchases.Add(r.ValueDelta)
		case "STAFF_MEAL":
			item.StaffMeal = item.StaffMeal.Sub(r.ValueDelta)
		case "ENTERTAINMENT":
			item.Entertainment = item.Entertainment.Sub(r.ValueDelta)
		case "WASTE":
			item.Waste = item.Waste.Sub(r.ValueDelta)
		case "COUNT_LOSS", "COUNT_GAIN":
			item.CountVariance = item.CountVariance.Sub(r.ValueDelta)
		case "POS_SALE", "POS_SALE_RETURN", "NEG_SETTLE":
			// the cost of a sale of the other class is moved: measured, never posted (4.9 item 6)
			if cost != nil {
				cost.Theory = cost.Theory.Sub(r.ValueDelta)
				if r.ItemClass != r.CostClass {
					item.TransferOut = item.TransferOut.Sub(r.ValueDelta)
					cost.TransferIn = cost.TransferIn.Sub(r.ValueDelta)
				}
			}
		case "COGS_TO_WASTE":
			// the cost of a dish thrown away after it was cooked: it leaves the cost of sales and becomes waste (no stock moves)
			if cost != nil {
				cost.Theory = cost.Theory.Sub(r.GlAmount)
				cost.Waste = cost.Waste.Add(r.GlAmount)
			}
		}
	}
	fig, err := s.figuresOf(ctx, p.TenantID, propertyID, f, t)
	if err != nil {
		return Reconciliation{}, err
	}
	norecipe := noRecipeEstimates(fig.Fb)
	var byClass = map[string][]FbDayRow{}
	for _, r := range fig.Fb {
		cols[r.Class].Revenue = cols[r.Class].Revenue.Add(r.Revenue)
		byClass[r.Class] = append(byClass[r.Class], r)
	}
	for class, rows := range byClass {
		cols[class].TheoryRevenue, cols[class].NoRecipeDays = recipeBasis(rows)
	}
	tl, err := s.targetsOf(ctx, p.TenantID, propertyID)
	if err != nil {
		return Reconciliation{}, err
	}
	tg := targets{list: tl}
	out := Reconciliation{From: f, To: t}
	for _, class := range []string{"FOOD", "BEVERAGE"} {
		c := cols[class]
		c.Theory = c.Theory.Round(3)
		c.finish()
		// the goods that were not deducted appear in the count: the estimate is capped at the loss of the count
		est := norecipe[class].Round(3)
		if est.GreaterThan(c.CountVariance) {
			est = c.CountVariance
		}
		if est.IsNegative() {
			est = decimal.Zero
		}
		c.NoRecipeEstimate = est
		c.TargetPct = tg.weighted(fig.Fb, class)
	}
	out.Food, out.Beverage = *cols["FOOD"], *cols["BEVERAGE"]
	total := addCols(out.Food, out.Beverage)
	_, total.NoRecipeDays = recipeBasis(fig.Fb)
	total.finish()
	total.TargetPct = nil
	out.Total = total
	wh, err := q.FbWarehousesCounted(ctx, inventorydb.FbWarehousesCountedParams{TenantID: p.TenantID, PropertyID: propertyID, Day: t})
	if err != nil {
		return Reconciliation{}, err
	}
	out.ClosingBasis = "COUNT"
	if len(wh) == 0 {
		out.ClosingBasis = "BOOK"
	}
	for _, w := range wh {
		if !w.Counted {
			out.ClosingBasis = "BOOK"
		}
	}
	if out.GL, err = s.glCheck(ctx, p.TenantID, propertyID, f, t); err != nil {
		return Reconciliation{}, err
	}
	return out, nil
}

// ---------------------------------------------------------------- the cost per outlet

// OutletCostRow is the theory and the actual cost of one class at one outlet (12.4).
type OutletCostRow struct {
	OutletID        *int64           `json:"outlet_id"`
	OutletCode      string           `json:"outlet_code"`
	OutletName      string           `json:"outlet_name"`
	WarehouseName   string           `json:"warehouse_name"`
	Class           string           `json:"class"`
	Revenue         decimal.Decimal  `json:"revenue"`
	NoRecipeRevenue decimal.Decimal  `json:"no_recipe_revenue"`
	Theory          decimal.Decimal  `json:"theory"`
	TheoryPct       *decimal.Decimal `json:"theory_pct"`
	CountVariance   decimal.Decimal  `json:"count_variance"`
	Actual          decimal.Decimal  `json:"actual"`
	ActualPct       *decimal.Decimal `json:"actual_pct"`
	// Basis is COUNT when the warehouse of the outlet was counted in the period, otherwise BOOK: the actual cost is estimated from the book stock (D21).
	Basis         string           `json:"basis"`
	Difference    decimal.Decimal  `json:"difference"`
	Waste         decimal.Decimal  `json:"waste"`
	StaffMeal     decimal.Decimal  `json:"staff_meal"`
	Entertainment decimal.Decimal  `json:"entertainment"`
	TargetPct     *decimal.Decimal `json:"target_pct"`
	// NoRecipeDays counts the days with revenue and no cost from recipes: they are left out of the percentages. NoRecipeData is true when no day has one.
	NoRecipeDays int  `json:"no_recipe_days"`
	NoRecipeData bool `json:"no_recipe_data"`
}

// OutletCostReport is the report of 12.4.
type OutletCostReport struct {
	From civil.Date      `json:"from"`
	To   civil.Date      `json:"to"`
	Rows []OutletCostRow `json:"rows"`
	GL   GLCheck         `json:"gl"`
}

// OutletCost answers the report of 12.4: per outlet and class, the theory (recipes x sales) against the actual cost from the counts of the warehouse of the outlet (inventory.view).
func (s *Service) OutletCost(ctx context.Context, propertyID int64, from, to *civil.Date) (OutletCostReport, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return OutletCostReport{}, err
	}
	f, t, _, err := s.period(ctx, propertyID, from, to)
	if err != nil {
		return OutletCostReport{}, err
	}
	q := s.q(ctx)
	fig, err := s.figuresOf(ctx, p.TenantID, propertyID, f, t)
	if err != nil {
		return OutletCostReport{}, err
	}
	outlets, err := q.OutletsWithWarehouse(ctx, inventorydb.OutletsWithWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return OutletCostReport{}, err
	}
	byID := map[int64]inventorydb.OutletsWithWarehouseRow{}
	for _, o := range outlets {
		byID[o.ID] = o
	}
	rows := map[fbKey]*OutletCostRow{}
	dataRev := map[fbKey]decimal.Decimal{} // the revenue of the days that have a cost from recipes: the percentages are over these days only
	for _, r := range fig.Fb {
		id, has := idOrZero(r.OutletID)
		k := fbKey{outlet: id, hasOut: has, class: r.Class}
		x, ok := rows[k]
		if !ok {
			x = &OutletCostRow{OutletID: r.OutletID, Class: r.Class}
			rows[k] = x
		}
		x.Revenue = x.Revenue.Add(r.Revenue)
		switch {
		case r.SaleCost.IsPositive():
			dataRev[k] = dataRev[k].Add(r.Revenue)
		case r.Revenue.IsPositive():
			x.NoRecipeDays++
		}
		x.NoRecipeRevenue = x.NoRecipeRevenue.Add(r.NoRecipeRevenue)
		x.Theory = x.Theory.Add(r.SaleCost)
		x.Waste = x.Waste.Add(r.WasteCost)
		x.StaffMeal = x.StaffMeal.Add(r.StaffMealCost)
		x.Entertainment = x.Entertainment.Add(r.EntertainmentCost)
	}
	// the count variances of a warehouse go to the outlets that sell from it, in proportion to their theoretical cost (equally when there is none)
	cv, err := q.CountVarianceByWarehouse(ctx, inventorydb.CountVarianceByWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: f, ToDate: t})
	if err != nil {
		return OutletCostReport{}, err
	}
	counted := map[int64]bool{}
	for _, c := range cv {
		counted[c.WarehouseID] = true
		var owners []*OutletCostRow
		weight := decimal.Zero
		for k, r := range rows {
			if k.hasOut && k.class == c.ItemClass && byID[k.outlet].WarehouseID != nil && *byID[k.outlet].WarehouseID == c.WarehouseID {
				owners, weight = append(owners, r), weight.Add(r.Theory)
			}
		}
		if len(owners) == 0 {
			continue
		}
		sort.Slice(owners, func(i, j int) bool { return idKey(owners[i].OutletID) < idKey(owners[j].OutletID) })
		left := c.Variance
		for i, r := range owners {
			share := c.Variance.Div(decimal.NewFromInt(int64(len(owners))))
			if weight.IsPositive() {
				share = c.Variance.Mul(r.Theory).Div(weight)
			}
			share = share.Round(3)
			if i == len(owners)-1 {
				share = left // the last owner takes what is left, so the parts add up to the variance
			}
			left = left.Sub(share)
			r.CountVariance = r.CountVariance.Add(share)
		}
	}
	wh, err := q.FbWarehousesCounted(ctx, inventorydb.FbWarehousesCountedParams{TenantID: p.TenantID, PropertyID: propertyID, Day: t})
	if err != nil {
		return OutletCostReport{}, err
	}
	for _, w := range wh {
		if w.Counted {
			counted[w.WarehouseID] = true
		}
	}
	tl, err := s.targetsOf(ctx, p.TenantID, propertyID)
	if err != nil {
		return OutletCostReport{}, err
	}
	tg := targets{list: tl}
	out := OutletCostReport{From: f, To: t, Rows: []OutletCostRow{}}
	for k, r := range rows {
		if r.OutletID != nil {
			o := byID[*r.OutletID]
			r.OutletCode, r.OutletName = o.Code, o.Name
			r.WarehouseName = o.WarehouseName
			r.Basis = "BOOK"
			if o.WarehouseID != nil && counted[*o.WarehouseID] {
				r.Basis = "COUNT"
			}
		} else {
			r.Basis = "BOOK"
		}
		r.Actual = r.Theory.Add(r.CountVariance)
		r.Difference = r.Actual.Sub(r.Theory)
		r.TheoryPct, r.ActualPct = pct(r.Theory, dataRev[k]), pct(r.Actual, dataRev[k])
		r.NoRecipeData = r.Revenue.IsPositive() && r.TheoryPct == nil
		r.TargetPct = tg.of(r.OutletID, r.Class)
		out.Rows = append(out.Rows, *r)
	}
	sort.Slice(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i], out.Rows[j]
		if (a.OutletID == nil) != (b.OutletID == nil) {
			return b.OutletID == nil
		}
		if a.OutletCode != b.OutletCode {
			return a.OutletCode < b.OutletCode
		}
		return a.Class < b.Class
	})
	if out.GL, err = s.glCheck(ctx, p.TenantID, propertyID, f, t); err != nil {
		return OutletCostReport{}, err
	}
	return out, nil
}
