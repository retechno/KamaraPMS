package budget

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/shopspring/decimal"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/budget/budgetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The statistics of a budget: per month of the fiscal year the room nights available, the room nights sold and the average daily rate (ADR). The room revenue
// (sold x ADR), the occupancy (sold over available) and RevPAR (room revenue over available) follow from them. They are kept beside the money of the budget and
// not tied to it: the report only says whether the room revenue of the plan agrees with sold x ADR.

// StatRow is a month of the statistics of a budget with what follows from it.
type StatRow struct {
	Month          int    `json:"month"`
	RoomsAvailable int    `json:"rooms_available"`
	RoomsSold      int    `json:"rooms_sold"`
	ADR            string `json:"adr"`
	Occupancy      string `json:"occupancy_percent"`
	RevPAR         string `json:"revpar"`
	RoomRevenue    string `json:"room_revenue"`
}

// StatInput is a month of the statistics on its way in.
type StatInput struct {
	Month          int    `json:"month"`
	RoomsAvailable int    `json:"rooms_available"`
	RoomsSold      int    `json:"rooms_sold"`
	ADR            string `json:"adr"`
}

// StatisticsInput is the statistics of a draft: the months given replace what the draft held (none clears them).
type StatisticsInput struct {
	Rows []StatInput `json:"rows"`
}

// derive gives the occupancy, RevPAR and room revenue of rooms available and sold at an ADR.
func derive(available, sold int, adr decimal.Decimal, decimals int32) (occupancy, revpar, revenue decimal.Decimal) {
	revenue = adr.Mul(decimal.NewFromInt(int64(sold))).Round(decimals)
	return occupancyOf(available, sold), revparOf(revenue, available, decimals), revenue
}

func occupancyOf(available, sold int) decimal.Decimal {
	if available <= 0 {
		return decimal.Zero
	}
	return decimal.NewFromInt(int64(sold)).Mul(decimal.NewFromInt(100)).DivRound(decimal.NewFromInt(int64(available)), 2)
}

func revparOf(revenue decimal.Decimal, available int, decimals int32) decimal.Decimal {
	if available <= 0 {
		return decimal.Zero
	}
	return revenue.DivRound(decimal.NewFromInt(int64(available)), decimals)
}

func adrOf(revenue decimal.Decimal, sold int, decimals int32) decimal.Decimal {
	if sold <= 0 {
		return decimal.Zero
	}
	return revenue.DivRound(decimal.NewFromInt(int64(sold)), decimals)
}

func (s *Service) statistics(ctx context.Context, tenantID, propertyID, budgetID int64, decimals int32) ([]StatRow, error) {
	rows, err := s.q(ctx).ListBudgetStatistics(ctx, budgetdb.ListBudgetStatisticsParams{TenantID: tenantID, PropertyID: propertyID, BudgetID: budgetID})
	if err != nil {
		return nil, err
	}
	out := make([]StatRow, 0, len(rows))
	for _, r := range rows {
		occ, rp, rev := derive(int(r.RoomsAvailable), int(r.RoomsSold), r.Adr, decimals)
		out = append(out, StatRow{
			Month: int(r.Month), RoomsAvailable: int(r.RoomsAvailable), RoomsSold: int(r.RoomsSold), ADR: r.Adr.StringFixed(decimals),
			Occupancy: occ.StringFixed(2), RevPAR: rp.StringFixed(decimals), RoomRevenue: rev.StringFixed(decimals),
		})
	}
	return out, nil
}

// SaveStatistics replaces the statistics of a draft (budget.manage).
func (s *Service) SaveStatistics(ctx context.Context, propertyID, id int64, in StatisticsInput) (Budget, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetManage)
	if err != nil {
		return Budget{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return Budget{}, err
	}
	type cellJSON struct {
		Month          int    `json:"month"`
		RoomsAvailable int    `json:"rooms_available"`
		RoomsSold      int    `json:"rooms_sold"`
		ADR            string `json:"adr"`
	}
	var fields []apperr.FieldError
	cells := make([]cellJSON, 0, len(in.Rows))
	seen := map[int]bool{}
	for i, r := range in.Rows {
		at := func(f string) string { return "rows[" + strconv.Itoa(i) + "]." + f }
		switch {
		case r.Month < 1 || r.Month > monthsInYear:
			fields = append(fields, fieldErr(at("month"), "INVALID_VALUE", "a month from 1 to 12 of the fiscal year"))
			continue
		case seen[r.Month]:
			fields = append(fields, fieldErr(at("month"), "DUPLICATE", "a month has one set of statistics"))
			continue
		}
		seen[r.Month] = true
		ok := true
		if r.RoomsAvailable < 0 || r.RoomsAvailable > maxRooms {
			fields = append(fields, fieldErr(at("rooms_available"), "INVALID_VALUE", "a number of room nights, not negative"))
			ok = false
		}
		if r.RoomsSold < 0 || r.RoomsSold > r.RoomsAvailable {
			fields = append(fields, fieldErr(at("rooms_sold"), "INVALID_VALUE", "not negative and not above the rooms available"))
			ok = false
		}
		adr, fe := parseAmount(at("adr"), r.ADR, decimals)
		switch {
		case fe != nil:
			fields = append(fields, *fe)
			ok = false
		case adr.IsNegative():
			fields = append(fields, fieldErr(at("adr"), "INVALID_AMOUNT", "not negative"))
			ok = false
		}
		if ok {
			cells = append(cells, cellJSON{Month: r.Month, RoomsAvailable: r.RoomsAvailable, RoomsSold: r.RoomsSold, ADR: adr.StringFixed(decimals)})
		}
	}
	if len(fields) > 0 {
		return Budget{}, apperr.Invalid("the statistics are invalid", fields...)
	}
	raw, err := json.Marshal(cells)
	if err != nil {
		return Budget{}, err
	}
	var out Budget
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if _, err := s.lockDraft(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		q := s.q(ctx)
		if err := q.DeleteBudgetStatistics(ctx, budgetdb.DeleteBudgetStatisticsParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: id}); err != nil {
			return err
		}
		if len(cells) > 0 {
			if err := q.InsertBudgetStatistics(ctx, budgetdb.InsertBudgetStatisticsParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: id, Cells: raw}); err != nil {
				return err
			}
		}
		if out, err = s.detail(ctx, p.TenantID, propertyID, id, decimals); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "budget.statistics_saved", id, auditlabel.Budget(ctx, propertyID, id), nil, map[string]any{"months": len(cells)}))
	})
	return out, err
}

// ---------------------------------------------------------------------------------------------------------------
// Against the actuals

// StatMetric is one statistic with its period and year to date cells. The unit tells how to show it.
type StatMetric struct {
	Key    string `json:"key"`
	Unit   string `json:"unit"`
	Period Cell   `json:"period"`
	YTD    Cell   `json:"ytd"`
}

// RoomRevenueCheck says whether the room revenue of the money budget (the REV_ROOMS accounts) agrees with the rooms sold at the ADR of the statistics.
type RoomRevenueCheck struct {
	PeriodMoney      decimal.Decimal `json:"period_money"`
	PeriodStatistics decimal.Decimal `json:"period_statistics"`
	YTDMoney         decimal.Decimal `json:"ytd_money"`
	YTDStatistics    decimal.Decimal `json:"ytd_statistics"`
	Agrees           bool            `json:"agrees"`
}

// StatsVsActual is the statistics of the budget against the closed days of the books for the same range as VsActual.
type StatsVsActual struct {
	YearStart        civil.Date       `json:"year_start"`
	YearEnd          civil.Date       `json:"year_end"`
	YearLabel        string           `json:"year_label"`
	From             civil.Date       `json:"from"`
	To               civil.Date       `json:"to"`
	Budget           BudgetRef        `json:"budget"`
	HasStatistics    bool             `json:"has_statistics"`
	ClosedDays       int              `json:"closed_days"`
	Metrics          []StatMetric     `json:"metrics"`
	RoomRevenueCheck RoomRevenueCheck `json:"room_revenue_check"`
}

// daySummary is what the statistics read of the summary a closed day stores.
type daySummary struct {
	Rooms struct {
		Total      int `json:"total"`
		OutOfOrder int `json:"out_of_order"`
		HouseUse   int `json:"house_use"`
		Sold       int `json:"sold"`
	} `json:"rooms"`
	RoomRevenue struct {
		Net string `json:"net"`
	} `json:"room_revenue"`
}

type totals struct {
	available, sold, days int
	revenue               decimal.Decimal
}

// actualTotals adds up the summaries of the closed days of a range: the rooms the hotel uses itself are not available, as in the statistics report.
func (s *Service) actualTotals(ctx context.Context, propertyID int64, from, to civil.Date) (totals, error) {
	rows, err := s.q(ctx).ListClosedDaySummaries(ctx, budgetdb.ListClosedDaySummariesParams{PropertyID: propertyID, FromDate: from, ToDate: to})
	if err != nil {
		return totals{}, err
	}
	t := totals{revenue: decimal.Zero}
	for _, r := range rows {
		var sum daySummary
		if json.Unmarshal(r.Summary, &sum) != nil {
			continue
		}
		rev, _ := decimal.NewFromString(sum.RoomRevenue.Net)
		t.days++
		t.available += sum.Rooms.Total - sum.Rooms.OutOfOrder - sum.Rooms.HouseUse
		t.sold += sum.Rooms.Sold
		t.revenue = t.revenue.Add(rev)
	}
	return t, nil
}

// StatisticsVsActual sets the statistics of the budget against the closed days of the books, for the period and the year to date (budget.view). Occupancy is the
// rooms sold over the rooms available on both sides, so the two compare like with like.
func (s *Service) StatisticsVsActual(ctx context.Context, propertyID int64, in VsActualQuery) (StatsVsActual, error) {
	p, err := s.need(ctx, propertyID, auth.PermBudgetView)
	if err != nil {
		return StatsVsActual{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return StatsVsActual{}, err
	}
	cal, err := s.calendar(ctx, p.TenantID, propertyID)
	if err != nil {
		return StatsVsActual{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return StatsVsActual{}, err
	}
	yearStart, yearEnd, from, to, err := in.resolve(cal, day.BusinessDate)
	if err != nil {
		return StatsVsActual{}, err
	}
	ref, err := s.resolveBudget(ctx, p.TenantID, propertyID, in, yearStart)
	if err != nil {
		return StatsVsActual{}, err
	}
	q := s.q(ctx)
	periodFrom, periodTo := monthStart(yearStart, from), monthEnd(yearStart, to)
	out := StatsVsActual{
		YearStart: yearStart, YearEnd: yearEnd, YearLabel: yearLabel(yearEnd), From: periodFrom, To: periodTo,
		Budget: BudgetRef{ID: ref.ID, Name: ref.Name, Version: int(ref.Version), Status: ref.Status},
	}
	have, err := q.ListBudgetStatistics(ctx, budgetdb.ListBudgetStatisticsParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: ref.ID})
	if err != nil {
		return StatsVsActual{}, err
	}
	out.HasStatistics = len(have) > 0

	type side struct {
		bAvail, bSold int
		bRev          decimal.Decimal
		money         decimal.Decimal
		a             totals
	}
	var sides [2]side
	for i, r := range []struct {
		fromMonth, toMonth int
		fromDate           civil.Date
	}{{from, to, periodFrom}, {1, to, yearStart}} {
		b, err := q.BudgetedStatistics(ctx, budgetdb.BudgetedStatisticsParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: ref.ID, FromMonth: int16(r.fromMonth), ToMonth: int16(r.toMonth)}) //nolint:gosec // G115: a month of the fiscal year is 1 to 12
		if err != nil {
			return StatsVsActual{}, err
		}
		money, err := q.BudgetedRoomRevenue(ctx, budgetdb.BudgetedRoomRevenueParams{TenantID: p.TenantID, PropertyID: propertyID, BudgetID: ref.ID, FromMonth: int16(r.fromMonth), ToMonth: int16(r.toMonth)}) //nolint:gosec // G115: a month of the fiscal year is 1 to 12
		if err != nil {
			return StatsVsActual{}, err
		}
		a, err := s.actualTotals(ctx, propertyID, r.fromDate, periodTo)
		if err != nil {
			return StatsVsActual{}, err
		}
		sides[i] = side{bAvail: int(b.RoomsAvailable), bSold: int(b.RoomsSold), bRev: b.RoomRevenue.Round(decimals), money: money, a: a}
	}
	out.ClosedDays = sides[1].a.days

	metric := func(key, unit string, value func(sd side) (actual, budget decimal.Decimal)) StatMetric {
		var cells [2]Cell
		for i, sd := range sides {
			a, b := value(sd)
			cells[i] = newCell(a, b, true)
		}
		return StatMetric{Key: key, Unit: unit, Period: cells[0], YTD: cells[1]}
	}
	dec := func(n int) decimal.Decimal { return decimal.NewFromInt(int64(n)) }
	out.Metrics = []StatMetric{
		metric("rooms_available", "NIGHTS", func(sd side) (decimal.Decimal, decimal.Decimal) { return dec(sd.a.available), dec(sd.bAvail) }),
		metric("rooms_sold", "NIGHTS", func(sd side) (decimal.Decimal, decimal.Decimal) { return dec(sd.a.sold), dec(sd.bSold) }),
		metric("occupancy", "PERCENT", func(sd side) (decimal.Decimal, decimal.Decimal) {
			return occupancyOf(sd.a.available, sd.a.sold), occupancyOf(sd.bAvail, sd.bSold)
		}),
		metric("adr", "MONEY", func(sd side) (decimal.Decimal, decimal.Decimal) {
			return adrOf(sd.a.revenue, sd.a.sold, decimals), adrOf(sd.bRev, sd.bSold, decimals)
		}),
		metric("revpar", "MONEY", func(sd side) (decimal.Decimal, decimal.Decimal) {
			return revparOf(sd.a.revenue, sd.a.available, decimals), revparOf(sd.bRev, sd.bAvail, decimals)
		}),
		metric("room_revenue", "MONEY", func(sd side) (decimal.Decimal, decimal.Decimal) { return sd.a.revenue, sd.bRev }),
	}
	out.RoomRevenueCheck = RoomRevenueCheck{
		PeriodMoney: sides[0].money, PeriodStatistics: sides[0].bRev, YTDMoney: sides[1].money, YTDStatistics: sides[1].bRev,
		Agrees: sides[0].money.Equal(sides[0].bRev) && sides[1].money.Equal(sides[1].bRev),
	}
	return out, nil
}
