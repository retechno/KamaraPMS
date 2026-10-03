package rates_test

import (
	"context"
	"testing"

	"kamarapms/internal/rates"
)

// A complimentary or house use plan is priced at zero for every night without a grid rate, and its kind is fixed.
func TestOccupancyKindPlans(t *testing.T) {
	f := newFixture(t)
	paid, err := f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "BAR", Name: "Best", MealPlan: "RO", RoomChargeCodeID: f.room, IsActive: true})
	if err != nil || paid.OccupancyKind != "PAID" {
		t.Fatalf("the default kind is PAID: %v %+v", err, paid)
	}
	comp, err := f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "comp", Name: "Complimentary", MealPlan: "RO", RoomChargeCodeID: f.room, OccupancyKind: "complimentary", IsActive: true})
	if err != nil || comp.OccupancyKind != "COMPLIMENTARY" {
		t.Fatalf("create: %v %+v", err, comp)
	}
	_, err = f.Rates.CreateRatePlan(f.admin, f.bali, rates.RatePlanInput{Code: "ODD", Name: "Odd", MealPlan: "RO", RoomChargeCodeID: f.room, OccupancyKind: "FREE"})
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Field != "occupancy_kind" {
		t.Fatalf("fields: %+v", c.Fields)
	}

	// no grid rate at all, yet every night is priced (zero), and the price lookup does not ask for rates
	typ := f.dlx
	prices, err := f.Rates.NightlyPrices(context.Background(), f.tenantID, f.bali, comp.ID, typ, d("2026-10-02"), d("2026-10-05"))
	if err != nil || len(prices.Nights) != 3 || prices.OccupancyKind != "COMPLIMENTARY" {
		t.Fatalf("lookup: %v %+v", err, prices)
	}
	for _, n := range prices.Nights {
		if !n.Amount.IsZero() {
			t.Fatalf("night %s costs %s", n.Date, n.Amount)
		}
	}
	// a paid plan without rates still reports the missing nights
	_, err = f.Rates.NightlyPrices(context.Background(), f.tenantID, f.bali, paid.ID, typ, d("2026-10-02"), d("2026-10-05"))
	wantCode(t, err, "RATE_NOT_SET")
}
