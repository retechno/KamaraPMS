package reports

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// The daily flash report (docs/architecture/38-reports-night-audit-pack.md, 7.1): the day, the month to date, the month to date of last month and of last year, the budget and the year to date, from the books by
// business date. It only reads.

var hundred = decimal.NewFromInt(100)

// flash holds what the builder of one report needs.
type flash struct {
	s        *Service
	ctx      context.Context
	p        auth.Principal
	pid      int64
	decimals int32
	rg       [nCols]FlashRange
	day      civil.Date
	interim  bool
}

// perCol runs a query for the range of each of the five columns.
func perCol[T any](f *flash, fn func(r FlashRange) (T, error)) ([nCols]T, error) {
	var out [nCols]T
	for i, r := range f.rg {
		v, err := fn(r)
		if err != nil {
			return out, err
		}
		out[i] = v
	}
	return out, nil
}

func (f *flash) fmtv(kind string, d decimal.Decimal) string {
	switch kind {
	case FlashMoney:
		return d.StringFixed(f.decimals)
	case FlashCount:
		return d.Round(0).String()
	}
	return d.StringFixed(2)
}

// line writes a row from its figures.
func (f *flash) line(key, label, kind string, v vals) FlashLine {
	cell := func(i int) string {
		if v.blank[i] {
			return ""
		}
		return f.fmtv(kind, v.v[i])
	}
	return FlashLine{Key: key, Label: label, Kind: kind, Today: cell(colToday), MTD: cell(colMTD), LastMonth: cell(colLastMonth), LastYear: cell(colLastYear), YTD: cell(colYTD)}
}

// withBudget sets the budget of the month to date of a row and the variance against it.
func (f *flash) withBudget(l FlashLine, mtd, budget decimal.Decimal) FlashLine {
	variance := mtd.Sub(budget)
	b, v := f.fmtv(l.Kind, budget), f.fmtv(l.Kind, variance)
	l.Budget, l.Variance = &b, &v
	if !budget.IsZero() {
		pct := variance.Mul(hundred).DivRound(budget, 2).StringFixed(2)
		l.VariancePct = &pct
	}
	fav := !variance.IsNegative()
	l.Favourable = &fav
	return l
}

func ratio(a, b decimal.Decimal, places int32) decimal.Decimal {
	if b.IsZero() {
		return decimal.Zero
	}
	return a.DivRound(b, places)
}

func pct(a, b decimal.Decimal) decimal.Decimal { return ratio(a.Mul(hundred), b, 2) }

func dInt(n int) decimal.Decimal { return decimal.NewFromInt(int64(n)) }

// roomTotals are the room counts of a range: the sum of the closing summaries of its days.
type roomTotals struct {
	total, ooo, oos, house, occupied, comp, sold, arrivals, departures int
	roomNet                                                            decimal.Decimal
}

func totalsOfRow(r reportsdb.FlashRoomDaysRow) roomTotals {
	return roomTotals{total: int(r.Total), ooo: int(r.OutOfOrder), oos: int(r.OutOfService), house: int(r.HouseUse), occupied: int(r.Occupied), comp: int(r.Complimentary),
		sold: int(r.Sold), arrivals: int(r.Arrivals), departures: int(r.Departures), roomNet: r.RoomNet}
}

func (t *roomTotals) addSummary(sum nightaudit.Summary) {
	t.total += sum.Rooms.Total
	t.ooo += sum.Rooms.OutOfOrder
	t.oos += sum.Rooms.OutOfService
	t.house += sum.Rooms.HouseUse
	t.occupied += sum.Rooms.Occupied
	t.comp += sum.Rooms.Complimentary
	t.sold += sum.Rooms.Sold
	t.arrivals += sum.Arrivals
	t.departures += sum.Departures
	if n, err := decimal.NewFromString(sum.RoomRevenue.Net); err == nil {
		t.roomNet = t.roomNet.Add(n)
	}
}

// available is the denominator of the occupancy of the closing summary: the rooms that are neither out of order nor kept for the hotel's own use.
func (t roomTotals) available() int { return t.total - t.ooo - t.house }

// DailyFlash is the daily flash report of a business date (report.view). The open day gives an interim flash.
func (s *Service) DailyFlash(ctx context.Context, propertyID int64, date civil.Date) (DailyFlash, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return DailyFlash{}, err
	}
	q := s.q(ctx)
	day, err := q.FlashDay(ctx, reportsdb.FlashDayParams{TenantID: p.TenantID, PropertyID: propertyID, Bd: date})
	if errors.Is(err, pgx.ErrNoRows) {
		return DailyFlash{}, apperr.NotFound("BUSINESS_DAY_NOT_FOUND", "the property has no business day on that date")
	}
	if err != nil {
		return DailyFlash{}, err
	}
	startMonth, haveAccounting := 1, true
	if m, err := q.FlashFiscalStartMonth(ctx, reportsdb.FlashFiscalStartMonthParams{TenantID: p.TenantID, PropertyID: propertyID}); err == nil {
		startMonth = int(m)
	} else if errors.Is(err, pgx.ErrNoRows) {
		haveAccounting = false
	} else {
		return DailyFlash{}, err
	}
	rg := flashRanges(date, startMonth)
	f := &flash{s: s, ctx: ctx, p: p, pid: propertyID, decimals: decimals, day: date, interim: day.Status == "OPEN",
		rg: [nCols]FlashRange{rg.Today, rg.MTD, rg.LastMonth, rg.LastYear, rg.YTD}}
	out := DailyFlash{BusinessDate: date, Status: day.Status, Interim: f.interim, Decimals: decimals, Ranges: rg, Warnings: []FlashWarning{}}
	monthNo, yearStart := fiscalMonthOf(date, startMonth)
	out.FiscalYearStart = yearStart

	// The budget of the month, if an active one covers it (the budget module owns it; the flash reads it).
	var budgetID int64
	if haveAccounting {
		if id, err := q.FlashActiveBudget(ctx, reportsdb.FlashActiveBudgetParams{TenantID: p.TenantID, PropertyID: propertyID, YearStart: yearStart}); err == nil {
			budgetID = id
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return DailyFlash{}, err
		}
	}
	out.HasBudget = budgetID != 0
	bm := budgetMonth{n: int16(monthNo), id: budgetID, day: date.Day(), dim: daysIn(date.Year(), date.Month())} //nolint:gosec // G115: a fiscal month, 1 to 12

	if err := f.rooms(&out, bm); err != nil {
		return DailyFlash{}, err
	}
	rev, err := f.revenue(&out, bm, haveAccounting)
	if err != nil {
		return DailyFlash{}, err
	}
	if err := f.payments(&out); err != nil {
		return DailyFlash{}, err
	}
	if err := f.covers(&out, rev); err != nil {
		return DailyFlash{}, err
	}
	if !f.interim {
		out.Signatures = FlashSignatures{PreparedBy: day.ClosedByName}
		if day.ClosedAt != nil {
			at := day.ClosedAt.UTC().Format(time.RFC3339)
			out.Signatures.PreparedAt = &at
		}
	}
	return out, nil
}

// budgetMonth is the month of the report in the fiscal calendar: the budget (0: none), and the day of the month over the days of the month for the pro rata.
type budgetMonth struct {
	day, dim int
	n        int16
	id       int64
}

// prorata is the budget of the month to date: the budget of the month times the day over the days of the month.
func (b budgetMonth) prorata(month decimal.Decimal) decimal.Decimal {
	return month.Mul(dInt(b.day)).Div(dInt(b.dim))
}

// rooms fills the room statistics, the sold nights by room type and the activity.
func (f *flash) rooms(out *DailyFlash, bm budgetMonth) error {
	q := f.s.q(f.ctx)
	tn := f.p.TenantID
	days, err := perCol(f, func(r FlashRange) (roomTotals, error) {
		row, err := q.FlashRoomDays(f.ctx, reportsdb.FlashRoomDaysParams{TenantID: tn, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
		if err != nil {
			return roomTotals{}, err
		}
		t := totalsOfRow(row)
		if f.interim && !f.day.Before(r.From) && !f.day.After(r.To) { // the open day has no stored summary: its live one
			sum, err := f.s.audit.Summarize(f.ctx, f.p, f.pid, f.day, f.decimals, 0)
			if err != nil {
				return roomTotals{}, err
			}
			t.addSummary(sum)
		}
		return t, nil
	})
	if err != nil {
		return err
	}
	guests, err := perCol(f, func(r FlashRange) (reportsdb.FlashGuestNightsRow, error) {
		return q.FlashGuestNights(f.ctx, reportsdb.FlashGuestNightsParams{TenantID: tn, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	walk, err := perCol(f, func(r FlashRange) (int32, error) {
		return q.FlashWalkIns(f.ctx, reportsdb.FlashWalkInsParams{TenantID: tn, PropertyID: &f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	outs, err := perCol(f, func(r FlashRange) (reportsdb.FlashCheckOutsRow, error) {
		return q.FlashCheckOuts(f.ctx, reportsdb.FlashCheckOutsParams{TenantID: tn, PropertyID: &f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	acts, err := perCol(f, func(r FlashRange) (reportsdb.FlashActivityRow, error) {
		return q.FlashActivity(f.ctx, reportsdb.FlashActivityParams{TenantID: tn, PropertyID: &f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	sold, err := perCol(f, func(r FlashRange) ([]reportsdb.FlashSoldByTypeRow, error) {
		return q.FlashSoldByType(f.ctx, reportsdb.FlashSoldByTypeParams{TenantID: tn, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}

	var v = func(fn func(i int) decimal.Decimal) vals {
		var x vals
		for i := 0; i < nCols; i++ {
			x.v[i] = fn(i)
		}
		return x
	}
	roomNights := func(i int) decimal.Decimal { return dInt(days[i].sold + days[i].comp) }
	occ := v(func(i int) decimal.Decimal { return pct(dInt(days[i].occupied), dInt(days[i].available())) })
	paid := v(func(i int) decimal.Decimal { return pct(dInt(days[i].sold), dInt(days[i].available())) })
	dbl := v(func(i int) decimal.Decimal {
		return pct(dInt(int(guests[i].Guests)).Sub(roomNights(i)), roomNights(i))
	})
	perRoom := v(func(i int) decimal.Decimal { return ratio(dInt(int(guests[i].Guests)), roomNights(i), 2) })
	arr := v(func(i int) decimal.Decimal { return ratio(days[i].roomNet, dInt(days[i].sold), f.decimals) })
	arrAll := v(func(i int) decimal.Decimal { return ratio(days[i].roomNet, roomNights(i), f.decimals) })
	revpar := v(func(i int) decimal.Decimal { return ratio(days[i].roomNet, dInt(days[i].available()), f.decimals) })
	alos := v(func(i int) decimal.Decimal { return ratio(dInt(int(outs[i].Nights)), dInt(int(outs[i].Stays)), 2) })
	for i := range alos.blank { // no departure in the range: the average is not zero, there is nothing to average
		alos.blank[i] = outs[i].Stays == 0
	}

	budget := f.statBudget(bm)
	out.Rooms = []FlashLine{
		f.line("rooms_total", "Total rooms", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].total) })),
		f.line("rooms_ooo", "Out of order", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].ooo) })),
		f.line("rooms_oos", "Out of service", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].oos) })),
		f.line("rooms_house_use", "House use", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].house) })),
		f.line("rooms_available", "Rooms available", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].available()) })),
		f.line("rooms_sold", "Rooms sold", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].sold) })),
		f.line("rooms_complimentary", "Complimentary", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].comp) })),
		f.line("rooms_vacant", "Vacant", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].available() - days[i].sold - days[i].comp) })),
		f.line("walk_ins", "Walk-in", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(walk[i])) })),
		f.line("guests_in_house", "Guests in house", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(guests[i].Guests)) })),
		f.line("occupancy_pct", "Occupancy % (with complimentary)", FlashPercent, occ),
		f.line("occupancy_paid_pct", "Occupancy % (paid rooms)", FlashPercent, paid),
		f.line("double_occupancy_pct", "Double occupancy %", FlashPercent, dbl),
		f.line("guests_per_room", "Guests per room", FlashRatio, perRoom),
		f.line("arr", "ARR", FlashMoney, arr),
		f.line("arr_incl_comp", "ARR including complimentary", FlashMoney, arrAll),
		f.line("revpar", "RevPAR", FlashMoney, revpar),
		f.line("alos", "ALOS (nights)", FlashRatio, alos),
	}
	if budget.have {
		for i, l := range out.Rooms {
			switch l.Key {
			case "rooms_available":
				out.Rooms[i] = f.withBudget(l, dInt(days[colMTD].available()), budget.available)
			case "rooms_sold":
				out.Rooms[i] = f.withBudget(l, dInt(days[colMTD].sold), budget.sold)
			case "occupancy_paid_pct":
				out.Rooms[i] = f.withBudget(l, paid.v[colMTD], pct(budget.sold, budget.available))
			case "arr":
				out.Rooms[i] = f.withBudget(l, arr.v[colMTD], budget.adr)
			case "revpar":
				out.Rooms[i] = f.withBudget(l, revpar.v[colMTD], ratio(budget.adr.Mul(budget.sold), budget.available, f.decimals))
			}
		}
	}

	// The paid nights by room type: a row for each type that sold a night in any column.
	type typeRow struct {
		code, name string
		n          [nCols]int
	}
	types := map[int64]*typeRow{}
	var order []int64
	for i := range sold {
		for _, r := range sold[i] {
			t := types[r.RoomTypeID]
			if t == nil {
				t = &typeRow{code: r.Code, name: r.Name}
				types[r.RoomTypeID] = t
				order = append(order, r.RoomTypeID)
			}
			t.n[i] = int(r.Sold)
		}
	}
	sort.SliceStable(order, func(a, b int) bool { return types[order[a]].code < types[order[b]].code })
	out.SoldByType = []FlashLine{}
	for _, id := range order {
		t := types[id]
		out.SoldByType = append(out.SoldByType, f.line("sold:"+t.code, t.code+" - "+t.name, FlashCount, v(func(i int) decimal.Decimal { return dInt(t.n[i]) })))
	}

	// The activity.
	tomorrow := f.tomorrow()
	out.Activity = []FlashLine{
		f.line("reservations_made", "Reservations made", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(acts[i].Made)) })),
		f.line("reservations_cancelled", "Cancelled", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(acts[i].Cancelled)) })),
		f.line("no_shows", "No-show", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(acts[i].NoShows)) })),
		f.line("early_checkouts", "Early check-out", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(outs[i].Early)) })),
		f.line("late_checkouts", "Late check-out", FlashCount, v(func(i int) decimal.Decimal { return dInt(int(outs[i].Late)) })),
		f.line("arrivals", "Arrivals", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].arrivals) })),
		f.line("departures", "Departures", FlashCount, v(func(i int) decimal.Decimal { return dInt(days[i].departures) })),
	}
	var ta, td vals
	for i := 1; i < nCols; i++ {
		ta.blank[i], td.blank[i] = true, true
	}
	if tomorrow == nil {
		ta.blank[colToday], td.blank[colToday] = true, true
	} else {
		ta.v[colToday], td.v[colToday] = dInt(int(tomorrow.Arrivals)), dInt(int(tomorrow.Departures))
	}
	out.Activity = append(out.Activity, f.line("arrivals_tomorrow", "Arrivals tomorrow", FlashCount, ta), f.line("departures_tomorrow", "Departures tomorrow", FlashCount, td))
	return nil
}

// tomorrow is the state of the books about the day after the report, when it is the state at the close: the report of the open day, or of the last closed day. For an older day nobody
// kept it (38 A8) and the cells stay empty.
func (f *flash) tomorrow() *reportsdb.FlashExpectedOnRow {
	open, err := f.s.q(f.ctx).FlashOpenDay(f.ctx, reportsdb.FlashOpenDayParams{TenantID: f.p.TenantID, PropertyID: f.pid})
	if err != nil || (!open.Equal(f.day) && !open.Equal(f.day.AddDays(1))) {
		return nil
	}
	row, err := f.s.q(f.ctx).FlashExpectedOn(f.ctx, reportsdb.FlashExpectedOnParams{TenantID: f.p.TenantID, PropertyID: f.pid, OnDate: f.day.AddDays(1)})
	if err != nil {
		return nil
	}
	return &row
}

// statBudget is the budget of the statistics of the month, pro rata for the month to date.
type statBudget struct {
	have                 bool
	available, sold, adr decimal.Decimal
}

func (f *flash) statBudget(bm budgetMonth) statBudget {
	if bm.id == 0 {
		return statBudget{}
	}
	row, err := f.s.q(f.ctx).FlashBudgetStats(f.ctx, reportsdb.FlashBudgetStatsParams{TenantID: f.p.TenantID, PropertyID: f.pid, BudgetID: bm.id, Month: bm.n})
	if err != nil {
		return statBudget{}
	}
	return statBudget{have: true, available: bm.prorata(dInt(int(row.RoomsAvailable))).Round(0), sold: bm.prorata(dInt(int(row.RoomsSold))).Round(0), adr: row.Adr}
}

// revKey names a row of the net revenue: the group, the block it is printed in, and the charge code.
type revKey struct {
	group  string
	kind   string
	outlet int64
	cc     int64
}

// revRow is a row of the net revenue in the five columns. source is the folio it came from (HOTEL, OUTLET or BANQUET), whatever block the row is printed in.
type revRow struct {
	key               revKey
	title, name       string
	source            string
	outletID          int64
	net, service, tax [nCols]decimal.Decimal
}

// revenueSet is the merged net revenue of the five columns.
type revenueSet struct {
	rows []*revRow
}

func (r *revenueSet) sum(pick func(*revRow) *[nCols]decimal.Decimal, keep func(*revRow) bool) vals {
	var out vals
	for _, row := range r.rows {
		if keep != nil && !keep(row) {
			continue
		}
		a := pick(row)
		for i := range out.v {
			out.v[i] = out.v[i].Add(a[i])
		}
	}
	return out
}

func groupOf(statement string) string {
	switch statement {
	case "REV_ROOMS":
		return "A"
	case "REV_FB":
		return "B"
	}
	return "C"
}

// revenue fills the net revenue in groups A, B and C, the service charge, the tax, the gross total revenue and the summary by source; it returns the merged rows for the covers and the control.
func (f *flash) revenue(out *DailyFlash, bm budgetMonth, haveAccounting bool) (*revenueSet, error) {
	q := f.s.q(f.ctx)
	tn := f.p.TenantID
	merged := map[revKey]*revRow{}
	var rows []*revRow
	unmapped := 0
	for ci, r := range f.rg {
		part, err := q.FlashRevenue(f.ctx, reportsdb.FlashRevenueParams{TenantID: tn, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
		if err != nil {
			return nil, err
		}
		for _, x := range part {
			src, kind, title := "HOTEL", "HOTEL", "Hotel folios"
			switch {
			case x.IsEvent:
				src, kind, title = "BANQUET", "BANQUET", "Banquet"
			case x.OutletID != 0:
				src, kind, title = "OUTLET", "OUTLET", x.OutletName
			}
			g := groupOf(x.StatementGroup)
			if g == "A" {
				kind, title = "ROOMS", ""
			}
			if x.StatementGroup == "" { // no revenue account, or an account that is not in the chart
				g, kind, title = "C", "UNMAPPED", "Unmapped"
				if ci == colMTD {
					unmapped++
				}
			}
			outlet := x.OutletID
			if kind != "OUTLET" {
				outlet = 0
			}
			k := revKey{group: g, kind: kind, outlet: outlet, cc: x.ChargeCodeID}
			row := merged[k]
			if row == nil {
				row = &revRow{key: k, title: title, name: x.ChargeCodeName, source: src, outletID: x.OutletID}
				merged[k] = row
				rows = append(rows, row)
			}
			row.net[ci] = row.net[ci].Add(x.Net)
			row.service[ci] = row.service[ci].Add(x.Service)
			row.tax[ci] = row.tax[ci].Add(x.Tax)
		}
	}
	set := &revenueSet{rows: rows}
	budgetOf, haveBudget := f.groupBudget(bm)

	net := func(r *revRow) *[nCols]decimal.Decimal { return &r.net }
	groups := []struct{ code, title string }{{"A", "Rooms"}, {"B", "Food and beverage"}, {"C", "Other"}}
	rev := FlashRevenueSection{Groups: []FlashRevenueGroup{}}
	var totalNet vals
	groupMTD := map[string]decimal.Decimal{}
	for _, g := range groups {
		grp := FlashRevenueGroup{Code: g.code, Title: g.title, Blocks: []FlashBlock{}}
		type blk struct {
			kind, title string
			outlet      int64
			rows        []*revRow
		}
		blocks := map[string]*blk{}
		var border []string
		for _, r := range rows {
			if r.key.group != g.code {
				continue
			}
			bk := r.key.kind + ":" + strconv.FormatInt(r.key.outlet, 10)
			b := blocks[bk]
			if b == nil {
				b = &blk{kind: r.key.kind, title: r.title, outlet: r.key.outlet}
				blocks[bk] = b
				border = append(border, bk)
			}
			b.rows = append(b.rows, r)
		}
		rank := map[string]int{"ROOMS": 0, "HOTEL": 1, "OUTLET": 2, "BANQUET": 3, "UNMAPPED": 4}
		sort.SliceStable(border, func(i, j int) bool {
			bi, bj := blocks[border[i]], blocks[border[j]]
			if rank[bi.kind] != rank[bj.kind] {
				return rank[bi.kind] < rank[bj.kind]
			}
			return bi.title < bj.title
		})
		var gTotal vals
		for _, bk := range border {
			b := blocks[bk]
			sort.SliceStable(b.rows, func(i, j int) bool { return b.rows[i].name < b.rows[j].name })
			fb := FlashBlock{Kind: b.kind, Title: b.title, Rows: []FlashLine{}}
			var bTotal vals
			for _, r := range b.rows {
				v := vals{v: r.net}
				fb.Rows = append(fb.Rows, f.line("cc:"+strconv.FormatInt(r.key.cc, 10), r.name, FlashMoney, v))
				bTotal = bTotal.add(v)
			}
			fb.Total = f.line("block:"+bk, "Total "+b.title, FlashMoney, bTotal)
			grp.Blocks = append(grp.Blocks, fb)
			gTotal = gTotal.add(bTotal)
		}
		grp.Total = f.line("group:"+g.code, "Total "+g.title, FlashMoney, gTotal)
		groupMTD[g.code] = gTotal.v[colMTD]
		if haveBudget {
			if b, ok := budgetOf(g.code); ok {
				grp.Total = f.withBudget(grp.Total, gTotal.v[colMTD], b)
			}
		}
		rev.Groups = append(rev.Groups, grp)
		totalNet = totalNet.add(gTotal)
	}
	rev.TotalNet = f.line("total_net", "Total revenue (net)", FlashMoney, totalNet)
	if haveBudget { // the total of the groups that have a budget, against the actual of those groups
		var all, actual decimal.Decimal
		have := false
		for _, g := range []string{"A", "B", "C"} {
			if b, ok := budgetOf(g); ok {
				have = true
				all = all.Add(b)
				actual = actual.Add(groupMTD[g])
			}
		}
		if have {
			rev.TotalNet = f.withBudget(rev.TotalNet, actual, all)
		}
	}
	service := set.sum(func(r *revRow) *[nCols]decimal.Decimal { return &r.service }, nil)
	tax := set.sum(func(r *revRow) *[nCols]decimal.Decimal { return &r.tax }, nil)
	rev.Service = f.line("service", "Service charge", FlashMoney, service)
	rev.Tax = f.line("tax", "Tax", FlashMoney, tax)
	rev.GTR = f.line("gtr", "Gross total revenue (GTR)", FlashMoney, totalNet.add(service).add(tax))

	// The tie to the books: revenue posted by a journal and not from a folio, so that A + B + C + this row is the income statement of the range.
	var journal vals
	if haveAccounting && !f.interim {
		for ci, r := range f.rg {
			jr, err := q.FlashJournalRevenue(f.ctx, reportsdb.FlashJournalRevenueParams{TenantID: tn, PropertyID: f.pid, AccountType: accounting.TypeRevenue, FromDate: r.From, ToDate: r.To})
			if err != nil {
				return nil, err
			}
			sum := decimal.Zero
			for _, j := range jr {
				sum = sum.Add(j.Amount)
			}
			journal.v[ci] = sum.Sub(totalNet.v[ci])
		}
		l := f.line("journal", "Revenue posted by journal, not from a folio", FlashMoney, journal)
		rev.Journal = &l
	}
	hotel := set.sum(net, func(r *revRow) bool { return r.source == "HOTEL" })
	pos := set.sum(net, func(r *revRow) bool { return r.source == "OUTLET" })
	banq := set.sum(net, func(r *revRow) bool { return r.source == "BANQUET" })
	rev.BySource = []FlashLine{
		f.line("source_hotel", "Hotel", FlashMoney, hotel),
		f.line("source_pos", "POS outlets", FlashMoney, pos),
		f.line("source_banquet", "Banquet", FlashMoney, banq),
		f.line("source_other", "Other (journals)", FlashMoney, journal),
	}
	out.Revenue = rev
	if unmapped > 0 {
		out.Warnings = append(out.Warnings, FlashWarning{Code: "UNMAPPED_REVENUE", Params: map[string]string{"rows": strconv.Itoa(unmapped)}})
	}
	return set, nil
}

// groupBudget is the money budget of the month to date of a revenue group (A rooms, B food and beverage, C the rest).
func (f *flash) groupBudget(bm budgetMonth) (func(string) (decimal.Decimal, bool), bool) {
	if bm.id == 0 {
		return nil, false
	}
	rows, err := f.s.q(f.ctx).FlashBudgetByGroup(f.ctx, reportsdb.FlashBudgetByGroupParams{TenantID: f.p.TenantID, PropertyID: f.pid, BudgetID: bm.id, Month: bm.n, AccountType: accounting.TypeRevenue})
	if err != nil || len(rows) == 0 {
		return nil, false
	}
	by := map[string]decimal.Decimal{}
	for _, r := range rows {
		by[groupOf(r.StatementGroup)] = by[groupOf(r.StatementGroup)].Add(r.Amount)
	}
	// a group with no budget line has no budget: its cells stay blank, they are not zero
	return func(g string) (decimal.Decimal, bool) {
		v, ok := by[g]
		return bm.prorata(v).Round(f.decimals), ok
	}, true
}

// payments fills the money taken (GTP) by cashier and by method, the refunds, the transfers to the city ledger and the control balance.
func (f *flash) payments(out *DailyFlash) error {
	q := f.s.q(f.ctx)
	parts, err := perCol(f, func(r FlashRange) ([]reportsdb.FlashPaymentsRow, error) {
		return q.FlashPayments(f.ctx, reportsdb.FlashPaymentsParams{TenantID: f.p.TenantID, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	type acc struct {
		label string
		v     [nCols]decimal.Decimal
	}
	byMethod, byRefund, byCashier := map[string]*acc{}, map[string]*acc{}, map[string]*acc{}
	var gtp, refunds, cityLedger vals
	touch := func(m map[string]*acc, key, label string, ci int, amt decimal.Decimal) {
		a := m[key]
		if a == nil {
			a = &acc{label: label}
			m[key] = a
		}
		a.v[ci] = a.v[ci].Add(amt)
	}
	for ci, rows := range parts {
		for _, r := range rows {
			switch {
			case r.PaymentType == "REFUND":
				touch(byRefund, r.PaymentMethod, r.PaymentMethod, ci, r.Amount)
				refunds.v[ci] = refunds.v[ci].Add(r.Amount)
			case r.PaymentMethod == "CITY_LEDGER":
				cityLedger.v[ci] = cityLedger.v[ci].Add(r.Amount)
			default:
				touch(byMethod, r.PaymentMethod, r.PaymentMethod, ci, r.Amount)
				gtp.v[ci] = gtp.v[ci].Add(r.Amount)
				switch r.Cut {
				case "OUTLET":
					touch(byCashier, "2:"+strconv.FormatInt(r.OutletID, 10), "Outlet: "+r.OutletName, ci, r.Amount)
				case "FRONT":
					label := "Front office: " + r.CashierName
					if r.Drawer != "" {
						label += " (" + r.Drawer + ")"
					}
					touch(byCashier, "1:"+strconv.FormatInt(r.CashierID, 10)+":"+r.Drawer, label, ci, r.Amount)
				default:
					touch(byCashier, "3", "Back office", ci, r.Amount)
				}
			}
		}
	}
	lines := func(m map[string]*acc, prefix string) []FlashLine {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ls := []FlashLine{}
		for _, k := range keys {
			ls = append(ls, f.line(prefix+k, m[k].label, FlashMoney, vals{v: m[k].v}))
		}
		return ls
	}
	out.Payments = FlashPaymentsSection{
		ByCashier: lines(byCashier, "cashier:"), ByMethod: lines(byMethod, "method:"), GTP: f.line("gtp", "Payments received (GTP)", FlashMoney, gtp),
		Refunds: lines(byRefund, "refund:"), RefundsTotal: f.line("refunds_total", "Refunds", FlashMoney, refunds),
		CityLedger: f.line("to_city_ledger", "Transferred to the city ledger", FlashMoney, cityLedger),
	}
	if f.interim {
		return nil
	}
	gtr := out.Revenue.GTR
	control := FlashControl{Balanced: true}
	for _, ci := range []int{colToday, colMTD} {
		r := f.rg[ci]
		actual, err := q.FlashLedgerChange(f.ctx, reportsdb.FlashLedgerChangeParams{TenantID: f.p.TenantID, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
		if err != nil {
			return err
		}
		g, _ := decimal.NewFromString(map[int]string{colToday: gtr.Today, colMTD: gtr.MTD}[ci])
		expected := g.Sub(gtp.v[ci]).Add(refunds.v[ci]).Sub(cityLedger.v[ci])
		col := FlashControlColumn{
			GTR: f.fmtv(FlashMoney, g), GTP: f.fmtv(FlashMoney, gtp.v[ci]), Refunds: f.fmtv(FlashMoney, refunds.v[ci]), CityLedger: f.fmtv(FlashMoney, cityLedger.v[ci]),
			Expected: f.fmtv(FlashMoney, expected), ActualChange: f.fmtv(FlashMoney, actual), Difference: f.fmtv(FlashMoney, actual.Sub(expected)),
		}
		if !actual.Sub(expected).Round(f.decimals).IsZero() {
			control.Balanced = false
		}
		if ci == colToday {
			control.Today = col
		} else {
			control.MTD = col
		}
	}
	out.Control = &control
	if !control.Balanced {
		out.Warnings = append(out.Warnings, FlashWarning{Code: "CONTROL_DIFFERENCE", Params: map[string]string{"today": control.Today.Difference, "mtd": control.MTD.Difference}})
	}
	return nil
}

// covers fills the covers and the average per cover of each POS outlet, and the pax and the average per pax of the banquet.
func (f *flash) covers(out *DailyFlash, rev *revenueSet) error {
	q := f.s.q(f.ctx)
	pos, err := perCol(f, func(r FlashRange) ([]reportsdb.FlashPosCoversRow, error) {
		return q.FlashPosCovers(f.ctx, reportsdb.FlashPosCoversParams{TenantID: f.p.TenantID, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	pax, err := perCol(f, func(r FlashRange) (reportsdb.FlashBanquetPaxRow, error) {
		return q.FlashBanquetPax(f.ctx, reportsdb.FlashBanquetPaxParams{TenantID: f.p.TenantID, PropertyID: f.pid, FromDate: r.From, ToDate: r.To})
	})
	if err != nil {
		return err
	}
	type outlet struct {
		name           string
		covers, noBill [nCols]int
	}
	outlets := map[int64]*outlet{}
	var order []int64
	for ci, rows := range pos {
		for _, r := range rows {
			o := outlets[r.OutletID]
			if o == nil {
				o = &outlet{name: r.Name}
				outlets[r.OutletID] = o
				order = append(order, r.OutletID)
			}
			o.covers[ci], o.noBill[ci] = int(r.Covers), int(r.BillsWithoutCovers)
		}
	}
	out.Covers = []FlashLine{}
	missing := 0
	for _, id := range order {
		o := outlets[id]
		var c, n, avg vals
		sales := rev.sum(func(r *revRow) *[nCols]decimal.Decimal { return &r.net }, func(r *revRow) bool { return r.source == "OUTLET" && r.outletID == id })
		for i := 0; i < nCols; i++ {
			c.v[i], n.v[i] = dInt(o.covers[i]), dInt(o.noBill[i])
			avg.v[i] = ratio(sales.v[i], c.v[i], f.decimals)
		}
		missing += o.noBill[colMTD]
		out.Covers = append(out.Covers,
			f.line("covers:"+strconv.FormatInt(id, 10), o.name+": covers", FlashCount, c),
			f.line("covers_missing:"+strconv.FormatInt(id, 10), o.name+": bills without a guest count", FlashCount, n),
			f.line("per_cover:"+strconv.FormatInt(id, 10), o.name+": average per cover", FlashMoney, avg))
	}
	anyPax := false
	for _, p := range pax {
		anyPax = anyPax || p.Pax > 0
	}
	if anyPax {
		var c, avg vals
		sales := rev.sum(func(r *revRow) *[nCols]decimal.Decimal { return &r.net }, func(r *revRow) bool { return r.source == "BANQUET" })
		for i := 0; i < nCols; i++ {
			c.v[i] = dInt(int(pax[i].Pax))
			avg.v[i] = ratio(sales.v[i], c.v[i], f.decimals)
		}
		out.Covers = append(out.Covers, f.line("banquet_pax", "Banquet: pax", FlashCount, c), f.line("banquet_per_pax", "Banquet: average per pax", FlashMoney, avg))
		if n := pax[colMTD].FallbackSessions; n > 0 {
			out.Warnings = append(out.Warnings, FlashWarning{Code: "BANQUET_PAX_FALLBACK", Params: map[string]string{"sessions": strconv.Itoa(int(n))}})
		}
	}
	if missing > 0 {
		out.Warnings = append(out.Warnings, FlashWarning{Code: "BILLS_WITHOUT_COVERS", Params: map[string]string{"bills": strconv.Itoa(missing)}})
	}
	return nil
}
