package reports

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/nightaudit"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports/reportsdb"
)

// Dashboard windows: the trend shows the last TrendDays closed days, the forecast the next ForecastDays nights.
const (
	TrendDays    = 14
	ForecastDays = 14
)

// Movements of the open business day: what is still to do at the front desk and what is in house.
type Movements struct {
	ArrivalsExpected   int    `json:"arrivals_expected"`
	ArrivalsCheckedIn  int    `json:"arrivals_checked_in"`
	DeparturesExpected int    `json:"departures_expected"`
	DeparturesDone     int    `json:"departures_checked_out"`
	InHouse            int    `json:"in_house"`
	InHouseBalance     string `json:"in_house_balance"`
}

// RoomStatusCounts are the active rooms by housekeeping status.
type RoomStatusCounts struct {
	Clean     int `json:"clean"`
	Dirty     int `json:"dirty"`
	Cleaning  int `json:"cleaning"`
	Inspected int `json:"inspected"`
}

// ForecastDay is one night ahead: the rooms held against the rooms that can be sold.
type ForecastDay struct {
	Date             civil.Date `json:"date"`
	Sellable         int        `json:"rooms_sellable"`
	Booked           int        `json:"rooms_booked"`
	OccupancyPercent string     `json:"occupancy_percent"`
}

// Dashboard is the manager's page: the open day live, the front desk's work left, the housekeeping state, the last
// days, the month against the one before, and the nights ahead.
type Dashboard struct {
	BusinessDate civil.Date          `json:"business_date"`
	Today        *nightaudit.Summary `json:"today"`
	Movements    Movements           `json:"movements"`
	Rooms        RoomStatusCounts    `json:"rooms"`
	Trend        []StatDay           `json:"trend"`
	// MonthToDate is the closed days of the month so far; PreviousMonth the same number of days of the month before.
	MonthToDate   StatTotals    `json:"month_to_date"`
	PreviousMonth StatTotals    `json:"previous_month"`
	Forecast      []ForecastDay `json:"forecast"`
}

// Dashboard computes the manager's dashboard of the open business day (report.view). Every part reads what the
// reports already show, so the numbers agree with them.
func (s *Service) Dashboard(ctx context.Context, propertyID int64) (Dashboard, error) {
	p, decimals, err := s.access(ctx, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	bd := day.BusinessDate
	out := Dashboard{BusinessDate: bd}

	live, err := s.DailySummary(ctx, propertyID, bd)
	if err != nil {
		return Dashboard{}, err
	}
	out.Today = live.Summary

	arrivals, err := s.Arrivals(ctx, propertyID, bd)
	if err != nil {
		return Dashboard{}, err
	}
	for _, a := range arrivals.Rows {
		switch a.Status {
		case "CONFIRMED":
			out.Movements.ArrivalsExpected++
		case "CHECKED_IN", "COMPLETED":
			out.Movements.ArrivalsCheckedIn++
		}
	}
	departures, err := s.Departures(ctx, propertyID, bd)
	if err != nil {
		return Dashboard{}, err
	}
	for _, d := range departures.Rows {
		switch d.Status {
		case "OPEN":
			out.Movements.DeparturesExpected++
		case "CHECKED_OUT":
			out.Movements.DeparturesDone++
		}
	}
	inHouse, err := s.InHouse(ctx, propertyID)
	if err != nil {
		return Dashboard{}, err
	}
	balance := decimal.Zero
	for _, r := range inHouse.Rows {
		b, _ := decimal.NewFromString(r.Balance)
		balance = balance.Add(b)
	}
	out.Movements.InHouse = len(inHouse.Rows)
	out.Movements.InHouseBalance = fx(balance, decimals)

	q := s.q(ctx)
	statuses, err := q.DashboardRoomStatus(ctx, reportsdb.DashboardRoomStatusParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Dashboard{}, err
	}
	for _, r := range statuses {
		switch r.Status {
		case "CLEAN":
			out.Rooms.Clean = int(r.Rooms)
		case "DIRTY":
			out.Rooms.Dirty = int(r.Rooms)
		case "CLEANING":
			out.Rooms.Cleaning = int(r.Rooms)
		case "INSPECTED":
			out.Rooms.Inspected = int(r.Rooms)
		}
	}

	trend, err := s.Statistics(ctx, propertyID, bd.AddDays(-TrendDays), bd.AddDays(-1))
	if err != nil {
		return Dashboard{}, err
	}
	out.Trend = trend.Days

	// The month so far against the same days of the month before (closed days only).
	monthStart := civil.NewDate(bd.Year(), bd.Month(), 1)
	elapsed := bd.Day() - 1
	zero := fx(decimal.Zero, decimals)
	empty := StatTotals{RoomRevenue: zero, OccupancyPercent: "0.00", ADR: zero, RevPAR: zero}
	out.MonthToDate, out.PreviousMonth = empty, empty
	if elapsed > 0 {
		st, err := s.Statistics(ctx, propertyID, monthStart, bd.AddDays(-1))
		if err != nil {
			return Dashboard{}, err
		}
		out.MonthToDate = st.Totals
		prevStart := civil.NewDate(bd.Year(), bd.Month()-1, 1)
		prevEnd := prevStart.AddDays(elapsed - 1)
		if last := monthStart.AddDays(-1); prevEnd.After(last) {
			prevEnd = last
		}
		pst, err := s.Statistics(ctx, propertyID, prevStart, prevEnd)
		if err != nil {
			return Dashboard{}, err
		}
		out.PreviousMonth = pst.Totals
	}

	dates := make([]string, ForecastDays)
	for i := range dates {
		dates[i] = bd.AddDays(i).String()
	}
	rows, err := q.DashboardForecast(ctx, reportsdb.DashboardForecastParams{TenantID: p.TenantID, PropertyID: propertyID, BusinessDate: bd, NextDate: bd.AddDays(1), Dates: dates})
	if err != nil {
		return Dashboard{}, err
	}
	out.Forecast = make([]ForecastDay, 0, len(rows))
	for _, r := range rows {
		pct := "0.00"
		if r.Sellable > 0 {
			pct = decimal.NewFromInt(int64(r.Booked)).Mul(decimal.NewFromInt(100)).DivRound(decimal.NewFromInt(int64(r.Sellable)), 2).StringFixed(2)
		}
		out.Forecast = append(out.Forecast, ForecastDay{Date: r.Night, Sellable: int(r.Sellable), Booked: int(r.Booked), OccupancyPercent: pct})
	}
	return out, nil
}
