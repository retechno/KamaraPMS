package assets

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/civil"
)

// FiscalLayer is a stretch of cost that the fiscal book depreciates from the month it starts: the acquisition (or the opening balance) and each value added (4.9).
type FiscalLayer struct {
	Start civil.Date
	Cost  decimal.Decimal
}

// FiscalDisposalInput is a disposal as the fiscal book needs it: when, and how much cost left.
type FiscalDisposalInput struct {
	Date        civil.Date
	CostRemoved decimal.Decimal
}

// FiscalInput is everything the fiscal book of one asset is computed from. It is pure: no database, no clock.
type FiscalInput struct {
	Rate         FiscalRate
	Method       string
	Layers       []FiscalLayer
	Disposals    []FiscalDisposalInput // posted disposals, by date
	OpeningAccum *decimal.Decimal      // the fiscal accumulated depreciation an old asset came with, at OpeningAsOf (4.4)
	OpeningAsOf  *civil.Date
	// TaxYearStartMonth is 1 for a tax year that starts in January.
	TaxYearStartMonth int
	Decimals          int32
}

type fiscalPart struct {
	frac decimal.Decimal
	end  *civil.Date // the month a disposed part stops depreciating (inclusive); nil for the part that is still alive
}

// FiscalBook is the fiscal depreciation of one asset (4.9). The functions are linear in the cost, so a disposal splits the asset in two parts: the part that left, frozen at the month of the
// disposal, and the part that stays. A year's amount is the difference of the accumulated amounts at its two ends.
type FiscalBook struct {
	in      FiscalInput
	parts   []fiscalPart
	removed []int // for each disposal, the index of its part
}

// NewFiscalBook prepares the fiscal book of an asset.
func NewFiscalBook(in FiscalInput) *FiscalBook {
	if in.TaxYearStartMonth < 1 || in.TaxYearStartMonth > 12 {
		in.TaxYearStartMonth = 1
	}
	if in.Method == "" {
		in.Method = MethodSL
	}
	b := &FiscalBook{in: in}
	alive := decimal.NewFromInt(1)
	for _, d := range in.Disposals {
		end := MonthStart(d.Date)
		aliveCost := alive.Mul(b.costAt(end))
		r := decimal.NewFromInt(1)
		if aliveCost.IsPositive() {
			r = d.CostRemoved.DivRound(aliveCost, 12)
			if r.GreaterThan(decimal.NewFromInt(1)) {
				r = decimal.NewFromInt(1)
			}
		}
		fr := alive.Mul(r)
		b.parts = append(b.parts, fiscalPart{frac: fr, end: &end})
		b.removed = append(b.removed, len(b.parts)-1)
		alive = alive.Sub(fr)
	}
	if alive.IsPositive() {
		b.parts = append(b.parts, fiscalPart{frac: alive})
	}
	return b
}

// costAt is the cost of the layers that have started by the month of t.
func (b *FiscalBook) costAt(t civil.Date) decimal.Decimal {
	sum := decimal.Zero
	for _, l := range b.in.Layers {
		if !MonthStart(l.Start).After(MonthStart(t)) {
			sum = sum.Add(l.Cost)
		}
	}
	return sum
}

// computed is the fiscal accumulated depreciation of the layers from their own starts through the month of t, as if nothing had been disposed of.
func (b *FiscalBook) computed(t civil.Date) decimal.Decimal {
	sum := decimal.Zero
	for _, l := range b.in.Layers {
		v, _ := FiscalAccumulated(l.Cost, b.in.Rate, b.in.Method, l.Start, t, b.in.TaxYearStartMonth, b.in.Decimals)
		sum = sum.Add(v)
	}
	return sum
}

// whole is the fiscal accumulated depreciation of the whole asset through the month of t. An asset that came with a fiscal state starts from it and adds what the rates give after that date.
func (b *FiscalBook) whole(t civil.Date) decimal.Decimal {
	if len(b.in.Layers) == 0 {
		return decimal.Zero
	}
	first := b.in.Layers[0].Start
	for _, l := range b.in.Layers {
		if l.Start.Before(first) {
			first = l.Start
		}
	}
	if MonthStart(t).Before(MonthStart(first)) {
		return decimal.Zero
	}
	capCost := b.costAt(t)
	if b.in.OpeningAccum == nil || b.in.OpeningAsOf == nil {
		return decimal.Min(b.computed(t), capCost)
	}
	opening, asOf := *b.in.OpeningAccum, *b.in.OpeningAsOf
	if MonthStart(t).Equal(MonthStart(asOf)) {
		return opening
	}
	if MonthStart(t).Before(MonthStart(asOf)) {
		// before the date of the opening state only that state is known: the rates give the shape of the way to it
		at := b.computed(asOf)
		if !at.IsPositive() {
			return decimal.Zero
		}
		return decimal.Min(opening, opening.Mul(b.computed(t)).Div(at).Round(b.in.Decimals))
	}
	v := opening.Add(b.computed(t)).Sub(b.computed(asOf))
	return decimal.Max(opening, decimal.Min(v, capCost))
}

// Accumulated is the fiscal accumulated depreciation through the month of upTo, the parts that were disposed of included at the amount they had when they left.
func (b *FiscalBook) Accumulated(upTo civil.Date) decimal.Decimal {
	sum := decimal.Zero
	for _, p := range b.parts {
		at := upTo
		if p.end != nil && MonthStart(upTo).After(*p.end) {
			at = *p.end
		}
		w := b.whole(at)
		if !p.frac.Equal(decimal.NewFromInt(1)) {
			w = w.Mul(p.frac).Round(b.in.Decimals)
		}
		sum = sum.Add(w)
	}
	return sum
}

// RemovedAccumulated is the fiscal accumulated depreciation of the part that left with the i-th disposal, at the month it left.
func (b *FiscalBook) RemovedAccumulated(i int) decimal.Decimal {
	if i < 0 || i >= len(b.removed) {
		return decimal.Zero
	}
	p := b.parts[b.removed[i]]
	w := b.whole(*p.end)
	if !p.frac.Equal(decimal.NewFromInt(1)) {
		w = w.Mul(p.frac).Round(b.in.Decimals)
	}
	return w
}

// ---------------------------------------------------------------------------------------------------------------------
// The report of the correction (4.9, 4.12)

// FiscalFilter picks the tax year (named by the calendar year in which it starts) and, optionally, a category or one asset.
type FiscalFilter struct {
	TaxYear    *int
	CategoryID *int64
	AssetID    *int64
}

// FiscalRow is one asset in one tax year: the commercial depreciation of the ledger, the fiscal one, and the difference (positive when the commercial expense is larger).
type FiscalRow struct {
	AssetID        int64           `json:"asset_id"`
	Number         string          `json:"number"`
	Name           string          `json:"name"`
	CategoryCode   string          `json:"category_code"`
	FiscalGroup    string          `json:"fiscal_group"`
	FiscalMethod   string          `json:"fiscal_method"`
	Commercial     decimal.Decimal `json:"commercial"`
	Fiscal         decimal.Decimal `json:"fiscal"`
	Difference     decimal.Decimal `json:"difference"`
	FiscalAccum    decimal.Decimal `json:"fiscal_accumulated"` // at the end of the tax year
	Partial        bool            `json:"partial_year"`       // the year starts before the register: the commercial months before it are not in the ledger
	Estimated      bool            `json:"fiscal_estimated"`   // the fiscal state of the opening balance was estimated, not given
	FromOpening    bool            `json:"from_opening"`
	DisposedInYear bool            `json:"disposed_in_year"`
}

// FiscalDisposal is a disposal of the tax year with the commercial and the fiscal result.
type FiscalDisposal struct {
	AssetID         int64           `json:"asset_id"`
	Number          string          `json:"number"`
	Name            string          `json:"name"`
	DisposalNumber  string          `json:"disposal_number"`
	DisposalDate    civil.Date      `json:"disposal_date"`
	CostRemoved     decimal.Decimal `json:"cost_removed"`
	Proceeds        decimal.Decimal `json:"proceeds"`
	CommercialGain  decimal.Decimal `json:"commercial_gain"` // a loss is negative
	FiscalBookValue decimal.Decimal `json:"fiscal_book_value"`
	FiscalGain      decimal.Decimal `json:"fiscal_gain"`
	Difference      decimal.Decimal `json:"difference"` // fiscal gain less commercial gain: positive raises the taxable income
}

// FiscalTotals adds the year up. The correction is toward the taxable income: positive where the commercial expense is larger, negative where the fiscal one is.
type FiscalTotals struct {
	Commercial           decimal.Decimal `json:"commercial"`
	Fiscal               decimal.Decimal `json:"fiscal"`
	PositiveCorrection   decimal.Decimal `json:"positive_correction"`
	NegativeCorrection   decimal.Decimal `json:"negative_correction"` // shown as a positive amount
	DepreciationNet      decimal.Decimal `json:"depreciation_net"`
	DisposalCorrection   decimal.Decimal `json:"disposal_correction"`
	NetCorrection        decimal.Decimal `json:"net_correction"`
	PartialYearAssets    int             `json:"partial_year_assets"`
	EstimatedFiscalCount int             `json:"estimated_fiscal_assets"`
}

// FiscalRateInfo is a rate of the groups as the report used it.
type FiscalRateInfo struct {
	Code      string           `json:"code"`
	LifeYears int              `json:"life_years"`
	SLRate    decimal.Decimal  `json:"sl_rate"`
	DBRate    *decimal.Decimal `json:"db_rate"`
}

// FiscalReport is the report "commercial against fiscal" for a tax year.
type FiscalReport struct {
	TaxYear   int              `json:"tax_year"`
	YearStart civil.Date       `json:"year_start"`
	YearEnd   civil.Date       `json:"year_end"`
	Rows      []FiscalRow      `json:"rows"`
	Disposals []FiscalDisposal `json:"disposals"`
	Totals    FiscalTotals     `json:"totals"`
	Rates     []FiscalRateInfo `json:"rates"`
	// RatesToConfirm is always true: the rates are data and the tax consultant must confirm them (Q15).
	RatesToConfirm bool `json:"rates_to_confirm"`
}

type fiscalAsset struct {
	row        assetsdb.FiscalBookAssetsRow
	book       *FiscalBook
	disposals  []assetsdb.FiscalBookDisposalsRow
	commercial map[civil.Date]decimal.Decimal
}

type fiscalData struct {
	startMonth   int
	registerFrom *civil.Date
	decimals     int32
	rates        []FiscalRateInfo
	assets       []*fiscalAsset
}

// taxYearStartMonth is the first month of the tax year: the setting of the register, else the fiscal year of the books, else January.
func (s *Service) taxYearStartMonth(ctx context.Context, tenantID, propertyID int64, cfg *int16) int {
	if cfg != nil {
		return int(*cfg)
	}
	if m, err := s.q(ctx).GetFiscalYearStartMonth(ctx, assetsdb.GetFiscalYearStartMonthParams{TenantID: tenantID, PropertyID: propertyID}); err == nil {
		return int(m)
	}
	return 1
}

// loadFiscal reads what the fiscal book needs and prepares the book of each asset that has a fiscal group and a rate.
func (s *Service) loadFiscal(ctx context.Context, tenantID, propertyID int64, categoryID, assetID *int64) (*fiscalData, error) {
	q := s.q(ctx)
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	cfg, err := q.GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	out := &fiscalData{decimals: prop.CurrencyDecimals, registerFrom: cfg.RegisterFrom, startMonth: s.taxYearStartMonth(ctx, tenantID, propertyID, cfg.TaxYearStartMonth)}
	groups, err := q.ListFiscalGroups(ctx, assetsdb.ListFiscalGroupsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	rates := map[string]FiscalRate{}
	for _, g := range groups {
		r := FiscalRate{LifeYears: int(g.LifeYears), SLRate: g.SlRate}
		if g.DbRate != nil {
			r.DBRate = *g.DbRate
		}
		rates[g.Code] = r
		out.rates = append(out.rates, FiscalRateInfo{Code: g.Code, LifeYears: int(g.LifeYears), SLRate: g.SlRate, DBRate: g.DbRate})
	}
	assetRows, err := q.FiscalBookAssets(ctx, assetsdb.FiscalBookAssetsParams{TenantID: tenantID, PropertyID: propertyID, CategoryID: categoryID, AssetID: assetID})
	if err != nil {
		return nil, err
	}
	layerRows, err := q.FiscalBookLayers(ctx, assetsdb.FiscalBookLayersParams{TenantID: tenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return nil, err
	}
	dispRows, err := q.FiscalBookDisposals(ctx, assetsdb.FiscalBookDisposalsParams{TenantID: tenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return nil, err
	}
	deprRows, err := q.CommercialDepreciationByMonth(ctx, assetsdb.CommercialDepreciationByMonthParams{TenantID: tenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return nil, err
	}
	layers := map[int64][]assetsdb.FiscalBookLayersRow{}
	for _, l := range layerRows {
		layers[l.AssetID] = append(layers[l.AssetID], l)
	}
	disposals := map[int64][]assetsdb.FiscalBookDisposalsRow{}
	for _, d := range dispRows {
		disposals[d.AssetID] = append(disposals[d.AssetID], d)
	}
	commercial := map[int64]map[civil.Date]decimal.Decimal{}
	for _, d := range deprRows {
		if commercial[d.AssetID] == nil {
			commercial[d.AssetID] = map[civil.Date]decimal.Decimal{}
		}
		commercial[d.AssetID][d.BookMonth] = d.Amount
	}
	for _, a := range assetRows {
		rate, ok := rates[a.FiscalGroup]
		if !ok {
			continue
		}
		in := FiscalInput{Rate: rate, Method: derefString(a.FiscalMethod), TaxYearStartMonth: out.startMonth, Decimals: out.decimals}
		if a.FiscalAccumOpening != nil && a.FiscalOpeningAsOf != nil {
			in.OpeningAccum, in.OpeningAsOf = a.FiscalAccumOpening, a.FiscalOpeningAsOf
		}
		var acquired decimal.Decimal
		for _, l := range layers[a.ID] {
			switch l.LayerKind {
			case LedgerOpen, LedgerAcquire:
				acquired = acquired.Add(l.Cost)
			default:
				if l.Cost.IsPositive() {
					in.Layers = append(in.Layers, FiscalLayer{Start: l.LayerDate, Cost: l.Cost})
				}
			}
		}
		if acquired.IsPositive() {
			in.Layers = append([]FiscalLayer{{Start: a.AcquisitionDate, Cost: acquired}}, in.Layers...)
		}
		if len(in.Layers) == 0 {
			continue
		}
		for _, d := range disposals[a.ID] {
			in.Disposals = append(in.Disposals, FiscalDisposalInput{Date: d.DisposalDate, CostRemoved: d.CostRemoved})
		}
		out.assets = append(out.assets, &fiscalAsset{row: a, book: NewFiscalBook(in), disposals: disposals[a.ID], commercial: commercial[a.ID]})
	}
	return out, nil
}

// FiscalReport computes the report "commercial against fiscal" of a tax year (asset.read): nothing is posted and nothing is stored.
func (s *Service) FiscalReport(ctx context.Context, propertyID int64, f FiscalFilter) (FiscalReport, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return FiscalReport{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return FiscalReport{}, err
	}
	data, err := s.loadFiscal(ctx, p.TenantID, propertyID, f.CategoryID, f.AssetID)
	if err != nil {
		return FiscalReport{}, err
	}
	year := taxYearStart(day.BusinessDate, data.startMonth).Year()
	if f.TaxYear != nil {
		year = *f.TaxYear
		if year < 1990 || year > 2200 {
			return FiscalReport{}, invalid("tax year", fieldErr("tax_year", "INVALID_VALUE", "a year between 1990 and 2200"))
		}
	}
	start := civil.NewDate(year, time.Month(data.startMonth), 1)
	end, prev := AddMonths(start, 11), AddMonths(start, -1)
	out := FiscalReport{TaxYear: year, YearStart: start, YearEnd: MonthEnd(end), Rows: []FiscalRow{}, Disposals: []FiscalDisposal{}, Rates: data.rates, RatesToConfirm: true}
	if out.Rates == nil {
		out.Rates = []FiscalRateInfo{}
	}
	for _, a := range data.assets {
		commercial := decimal.Zero
		for m, v := range a.commercial {
			if !m.Before(start) && !m.After(end) {
				commercial = commercial.Add(v)
			}
		}
		endAccum := a.book.Accumulated(end)
		fiscal := endAccum.Sub(a.book.Accumulated(prev))
		fromOpening := a.row.Source == sourceOpening
		partial := fromOpening && data.registerFrom != nil && start.Before(*data.registerFrom) && !end.Before(MonthStart(*data.registerFrom))
		disposedInYear := false
		for i, d := range a.disposals {
			if d.DisposalDate.Before(start) || d.DisposalDate.After(out.YearEnd) {
				continue
			}
			disposedInYear = true
			accumRemoved := a.book.RemovedAccumulated(i)
			bookValue := d.CostRemoved.Sub(accumRemoved)
			fiscalGain := d.Proceeds.Sub(bookValue)
			fd := FiscalDisposal{AssetID: a.row.ID, Number: a.row.Number, Name: a.row.Name, DisposalNumber: d.Number, DisposalDate: d.DisposalDate, CostRemoved: d.CostRemoved, Proceeds: d.Proceeds,
				CommercialGain: d.GainLoss, FiscalBookValue: bookValue, FiscalGain: fiscalGain, Difference: fiscalGain.Sub(d.GainLoss)}
			out.Disposals = append(out.Disposals, fd)
			out.Totals.DisposalCorrection = out.Totals.DisposalCorrection.Add(fd.Difference)
		}
		if commercial.IsZero() && fiscal.IsZero() && !disposedInYear {
			continue
		}
		diff := commercial.Sub(fiscal)
		out.Rows = append(out.Rows, FiscalRow{AssetID: a.row.ID, Number: a.row.Number, Name: a.row.Name, CategoryCode: a.row.CategoryCode, FiscalGroup: a.row.FiscalGroup,
			FiscalMethod: orString(a.row.FiscalMethod, MethodSL), Commercial: commercial, Fiscal: fiscal, Difference: diff, FiscalAccum: endAccum, Partial: partial,
			Estimated: a.row.FiscalOpeningEstimated, FromOpening: fromOpening, DisposedInYear: disposedInYear})
		t := &out.Totals
		t.Commercial, t.Fiscal = t.Commercial.Add(commercial), t.Fiscal.Add(fiscal)
		switch {
		case diff.IsPositive():
			t.PositiveCorrection = t.PositiveCorrection.Add(diff)
		case diff.IsNegative():
			t.NegativeCorrection = t.NegativeCorrection.Add(diff.Neg())
		}
		if partial {
			t.PartialYearAssets++
		}
		if a.row.FiscalOpeningEstimated {
			t.EstimatedFiscalCount++
		}
	}
	sort.SliceStable(out.Disposals, func(i, j int) bool { return out.Disposals[i].DisposalDate.Before(out.Disposals[j].DisposalDate) })
	out.Totals.DepreciationNet = out.Totals.PositiveCorrection.Sub(out.Totals.NegativeCorrection)
	out.Totals.NetCorrection = out.Totals.DepreciationNet.Add(out.Totals.DisposalCorrection)
	return out, nil
}

// fillFiscalSchedule adds the fiscal amount and accumulated depreciation to each month of an asset's schedule (the column "fiscal" of the tab).
func (s *Service) fillFiscalSchedule(ctx context.Context, tenantID, propertyID, assetID int64, sch *AssetSchedule) error {
	data, err := s.loadFiscal(ctx, tenantID, propertyID, nil, &assetID)
	if err != nil || len(data.assets) == 0 {
		return err
	}
	a := data.assets[0]
	g, m := a.row.FiscalGroup, orString(a.row.FiscalMethod, MethodSL)
	sch.FiscalGroup, sch.FiscalMethod = &g, &m
	for i := range sch.Rows {
		r := &sch.Rows[i]
		after := a.book.Accumulated(r.Month)
		amount := after.Sub(a.book.Accumulated(AddMonths(r.Month, -max(r.Months, 1))))
		if r.Kind == ScheduleOpening { // the opening balances brought the whole state
			amount = after
		}
		r.FiscalAmount, r.FiscalAccumAfter = &amount, &after
	}
	return nil
}

func orString(p *string, def string) string {
	if p == nil || *p == "" {
		return def
	}
	return *p
}
