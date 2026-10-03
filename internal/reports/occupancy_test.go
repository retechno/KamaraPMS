package reports_test

import (
	"context"
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
)

// stayOn books and checks in a stay on a given rate plan.
func (f *fx) stayOn(t *testing.T, plan int64, room rooms.Room, departure, reason string) frontdesk.CheckInResult {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: plan, Arrival: d("2026-09-30"), Departure: d(departure), Adults: 2, OccupancyReason: reason}}})
	must(t, err)
	out, err := f.Front.CheckIn(f.admin, f.propID, res.ID, res.Rooms[0].ID, "", frontdesk.CheckInInput{Version: res.Version, RoomID: &room.ID, GuestID: f.guest, AdultCount: 2})
	must(t, err)
	return out
}

func (f *fx) freePlan(t *testing.T, code, kind string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id, occupancy_kind)
		SELECT tenant_id, property_id, $2, $2, room_charge_code_id, $3 FROM rate_plans WHERE id = $1 RETURNING id`, f.plan, code, kind).Scan(&id))
	return id
}

// Complimentary rooms are occupied but not sold, so they do not dilute the ADR; house use rooms are neither occupied,
// nor sellable, nor available, so they do not lower the occupancy.
func TestSummaryCountsComplimentaryAndHouseUse(t *testing.T) {
	f := setup(t)
	r103 := f.Room(t, f.admin, f.propID, f.dlx.ID, "103", housekeeping.Clean)
	comp, house := f.freePlan(t, "COMP", "COMPLIMENTARY"), f.freePlan(t, "HOUSE", "HOUSE_USE")
	f.stay(t, f.r101, "2026-10-02")
	f.stayOn(t, comp, f.r102, "2026-10-02", "Owner guest")
	f.stayOn(t, house, r103, "2026-10-02", "Staff")

	// the live summary of the open day
	live, err := f.Reports.DailySummary(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if r := live.Summary.Rooms; r.Total != 3 || r.Occupied != 2 || r.Complimentary != 1 || r.HouseUse != 1 || r.Sellable != 2 {
		t.Fatalf("rooms: %+v", r)
	}

	f.audit(t) // 30 Sep: one paid night at 1,000,000, one complimentary and one house use night at zero
	closed, err := f.Reports.DailySummary(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	s := closed.Summary
	if s.Rooms.Sold != 1 || s.RoomRevenue.Net != "1000000" || s.ADR != "1000000" || s.OccupancyPercent != "100.00" || s.RevPAR != "500000" || s.RoomChargesPosted != 3 {
		t.Fatalf("closed summary: %+v", s)
	}

	stats, err := f.Reports.Statistics(f.admin, f.propID, d("2026-09-30"), d("2026-09-30"))
	must(t, err)
	tt := stats.Totals
	if tt.AvailableNights != 2 || tt.OccupiedNights != 2 || tt.ComplimentaryNights != 1 || tt.HouseUseNights != 1 || tt.RoomNightsSold != 1 || tt.ADR != "1000000" || tt.OccupancyPercent != "100.00" {
		t.Fatalf("totals: %+v", tt)
	}
	if d0 := stats.Days[0]; d0.Complimentary != 1 || d0.HouseUse != 1 || d0.Occupied != 2 {
		t.Fatalf("day: %+v", d0)
	}

	// the lists show the kind
	in, err := f.Reports.InHouse(f.admin, f.propID)
	must(t, err)
	kinds := map[string]string{}
	for _, r := range in.Rows {
		kinds[r.Room] = r.OccupancyKind
	}
	if kinds["101"] != "PAID" || kinds["102"] != "COMPLIMENTARY" || kinds["103"] != "HOUSE_USE" {
		t.Fatalf("in-house kinds: %v", kinds)
	}
	arr, err := f.Reports.Arrivals(f.admin, f.propID, d("2026-09-30"))
	must(t, err)
	if len(arr.Rows) != 3 || arr.Rows[0].OccupancyKind == "" {
		t.Fatalf("arrivals: %+v", arr.Rows)
	}
}

// The forecast of the dashboard leaves the rooms the hotel uses itself out of both the booked and the sellable rooms,
// like the closing summary, so they do not lower the occupancy ahead.
func TestForecastLeavesHouseUseOut(t *testing.T) {
	f := setup(t)
	r103 := f.Room(t, f.admin, f.propID, f.dlx.ID, "103", housekeeping.Clean)
	house := f.freePlan(t, "HOUSE", "HOUSE_USE")
	f.stay(t, f.r101, "2026-10-02")
	f.stayOn(t, house, r103, "2026-10-02", "Staff")

	dash, err := f.Reports.Dashboard(f.admin, f.propID)
	must(t, err)
	// 3 rooms, one used by the hotel: 2 can be sold and 1 of them is booked
	if fc := dash.Forecast[0]; fc.Sellable != 2 || fc.Booked != 1 || fc.OccupancyPercent != "50.00" {
		t.Fatalf("forecast: %+v", fc)
	}
}
