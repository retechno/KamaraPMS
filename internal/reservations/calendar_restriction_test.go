package reservations_test

import (
	"context"
	"reflect"
	"testing"

	"kamarapms/internal/rates"
)

// The calendar marks the nights that carry a sales restriction, by the same precedence as a sale, and never changes a number: a restriction is not about stock.
func TestCalendarMarksTheRestrictions(t *testing.T) {
	f := setup(t) // DLX: 101, 102; STD: 201; one active plan BAR
	var second int64
	var roomCode int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT room_charge_code_id FROM rate_plans WHERE id = $1`, f.plan).Scan(&roomCode))
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'CORP', 'Corporate', $3) RETURNING id`,
		f.tenantID, f.propID, roomCode).Scan(&second))
	fill := func(in rates.FillRestrictionsInput) {
		t.Helper()
		_, err := f.Rates.FillRestrictions(f.admin, f.propID, in)
		must(t, err)
	}
	before, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-07"), false)
	must(t, err)
	for _, ty := range before.RoomTypes {
		for _, n := range ty.Nights {
			if len(n.Restrictions) != 0 || n.StopSellAll {
				t.Fatalf("no restriction yet: %+v", n)
			}
		}
	}

	// 2 Oct: DLX closed for every plan. 3 Oct: DLX closed for BAR only. 4 Oct: a minimum stay for the whole property, and closed to arrival for DLX. 5 Oct: closed for an inactive plan only.
	fill(rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-10-02"), To: d("2026-10-03"), Set: rates.RestrictionSet{StopSell: bp(true)}})
	fill(rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx.ID}, RatePlanIDs: []int64{f.plan}, From: d("2026-10-03"), To: d("2026-10-04"), Set: rates.RestrictionSet{StopSell: bp(true)}})
	fill(rates.FillRestrictionsInput{From: d("2026-10-04"), To: d("2026-10-05"), Set: rates.RestrictionSet{MinStay: ip(3)}})
	fill(rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-10-04"), To: d("2026-10-05"), Set: rates.RestrictionSet{ClosedToArrival: bp(true)}})
	var inactive int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id, is_active) VALUES ($1, $2, 'OLD', 'Old', $3, false) RETURNING id`,
		f.tenantID, f.propID, roomCode).Scan(&inactive))
	fill(rates.FillRestrictionsInput{RoomTypeIDs: []int64{f.dlx.ID}, RatePlanIDs: []int64{inactive}, From: d("2026-10-05"), To: d("2026-10-06"), Set: rates.RestrictionSet{StopSell: bp(true)}})

	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-07"), false)
	must(t, err)
	check := func(code, date string, want []string, all bool) {
		t.Helper()
		n := nightOf(t, cal, code, date)
		if !reflect.DeepEqual(n.Restrictions, want) || n.StopSellAll != all {
			t.Fatalf("%s %s: restrictions %v all=%v, want %v all=%v", code, date, n.Restrictions, n.StopSellAll, want, all)
		}
	}
	check("DLX", "2026-10-02", []string{"STOP_SELL"}, true)
	check("STD", "2026-10-02", nil, false)
	check("DLX", "2026-10-03", []string{"STOP_SELL"}, false) // closed for BAR, open for CORP
	check("DLX", "2026-10-04", []string{"CLOSED_TO_ARRIVAL", "MIN_STAY"}, false)
	check("STD", "2026-10-04", []string{"MIN_STAY"}, false)
	check("DLX", "2026-10-05", nil, false) // an inactive plan is not asked
	check("DLX", "2026-10-06", nil, false)

	// the numbers are those of the calendar without restrictions
	for i, ty := range cal.RoomTypes {
		for j, n := range ty.Nights {
			b := before.RoomTypes[i].Nights[j]
			if n.Sellable != b.Sellable || n.Held != b.Held || n.Available != b.Available || n.Blocked != b.Blocked {
				t.Fatalf("a restriction changed the stock of %s on %s: %+v vs %+v", ty.Code, n.Date, n, b)
			}
		}
	}
	// the bed rows and the totals carry no mark
	withBeds, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-03"), true)
	must(t, err)
	for _, ty := range withBeds.RoomTypes {
		for _, b := range ty.Beds {
			for _, n := range b.Nights {
				if len(n.Restrictions) != 0 || n.StopSellAll {
					t.Fatalf("a bed row has no mark: %+v", n)
				}
			}
		}
	}
}
