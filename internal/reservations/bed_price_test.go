package reservations_test

import (
	"testing"

	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
)

// The grid of DLX is 1,000,000 a night. addSupplement records a supplement of a bed on the BAR plan for DLX.
func (f *fx) addSupplement(t *testing.T, bed int64, kind, amount, from string) {
	t.Helper()
	_, err := f.Rates.AddBedAdjustment(f.admin, f.propID, f.plan, rates.BedAdjustmentInput{
		RoomTypeID: f.dlx.ID, BedTypeID: bed, AdjustKind: kind, Amount: amount, EffectiveFrom: d(from),
	})
	must(t, err)
}

func nightAmounts(l reservations.Line) []string {
	out := make([]string, len(l.NightlyRates))
	for i, n := range l.NightlyRates {
		out[i] = n.Amount.String()
	}
	return out
}

func TestALockedLineIsPricedWithTheSupplementOfItsBed(t *testing.T) {
	f := setup(t)
	king, twin := f.twinForRoom102(t)
	f.addSupplement(t, king.ID, "AMOUNT", "50000", "2026-10-01")
	f.addSupplement(t, king.ID, "PERCENT", "10", "2026-10-04") // from the 4th: 10 percent
	f.addSupplement(t, twin.ID, "AMOUNT", "-2000000", "2026-10-01")

	// the nights of the 2nd, 3rd (amount) and 4th (percent)
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-05")))
	must(t, err)
	line := res.Rooms[0]
	got := nightAmounts(line)
	if got[0] != "1050000" || got[1] != "1050000" || got[2] != "1100000" {
		t.Fatalf("nights: %v", got)
	}
	n := line.NightlyRates[0]
	if n.BedAdjustment.String() != "50000" || n.GridRate == nil || n.GridRate.String() != "1000000" || n.BaseRate == nil || n.BaseRate.String() != "1050000" {
		t.Fatalf("the night keeps the grid price, the supplement and the sold price: %+v", n)
	}
	if line.NightlyRates[2].BedAdjustment.String() != "100000" {
		t.Fatalf("a percentage of the sold price: %+v", line.NightlyRates[2])
	}

	// a request that is not kept pays the grid price, and so does a kept bed that has no supplement
	soft := f.line(f.dlx, "2026-10-02", "2026-10-03")
	soft.BedTypeID = &king.ID
	res2, err := f.Res.Create(f.admin, f.propID, "", f.input(true, soft))
	must(t, err)
	if got := nightAmounts(res2.Rooms[0]); got[0] != "1000000" {
		t.Fatalf("not kept: %v", got)
	}
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.std, king.ID, "2026-10-02", "2026-10-03")))
	must(t, err) // STD has no supplement and no discount: the grid of STD

	// a discount is cut at the price: the night is never below 0
	free := f.keep(f.dlx, twin.ID, "2026-10-10", "2026-10-11")
	res3, err := f.Res.Create(f.admin, f.propID, "", f.input(true, free))
	must(t, err)
	if got := nightAmounts(res3.Rooms[0]); got[0] != "0" || res3.Rooms[0].NightlyRates[0].BedAdjustment.String() != "-1000000" {
		t.Fatalf("a discount larger than the price: %v %+v", got, res3.Rooms[0].NightlyRates[0])
	}
}

func TestANightKeepsTheSupplementItWasPricedWith(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	f.addSupplement(t, king.ID, "AMOUNT", "50000", "2026-10-01")
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	// a new figure from the 3rd does not move the nights that are booked
	f.addSupplement(t, king.ID, "AMOUNT", "90000", "2026-10-03")
	got, err := f.Res.Get(f.admin, f.propID, res.ID)
	must(t, err)
	if a := nightAmounts(got.Rooms[0]); a[0] != "1050000" || a[1] != "1050000" {
		t.Fatalf("booked nights keep their price: %v", a)
	}
	// an amendment that does not touch the bed keeps the snapshot too (the nights are not priced again)
	adults := 1
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, res.Rooms[0].ID, reservations.LinePatch{Version: got.Version, Adults: &adults})
	must(t, err)
	if a := nightAmounts(got.Rooms[0]); a[1] != "1050000" {
		t.Fatalf("after an amendment: %v", a)
	}
	// a new line gets the figure in force on its night
	res2, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-04", "2026-10-05")))
	must(t, err)
	if a := nightAmounts(res2.Rooms[0]); a[0] != "1090000" {
		t.Fatalf("the new figure: %v", a)
	}
}

func TestLockingAndUnlockingPricesTheNightsAgain(t *testing.T) {
	f := setup(t)
	king, twin := f.twinForRoom102(t)
	f.addSupplement(t, king.ID, "AMOUNT", "50000", "2026-10-01")
	f.addSupplement(t, twin.ID, "AMOUNT", "30000", "2026-10-01")
	in := f.input(true, f.line(f.dlx, "2026-10-02", "2026-10-04"))
	in.Rooms[0].BedTypeID = &king.ID
	res, err := f.Res.Create(f.admin, f.propID, "", in)
	must(t, err)
	id := res.Rooms[0].ID
	if a := nightAmounts(res.Rooms[0]); a[0] != "1000000" {
		t.Fatalf("a request is the grid: %v", a)
	}
	on, off := true, false
	got, err := f.Res.AmendLine(f.admin, f.propID, res.ID, id, reservations.LinePatch{Version: res.Version, BedLocked: &on})
	must(t, err)
	if a := nightAmounts(got.Rooms[0]); a[0] != "1050000" || a[1] != "1050000" {
		t.Fatalf("locked: %v", a)
	}
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, id, reservations.LinePatch{Version: got.Version, BedTypeID: &twin.ID})
	must(t, err)
	if a := nightAmounts(got.Rooms[0]); a[0] != "1030000" {
		t.Fatalf("another bed: %v", a)
	}
	got, err = f.Res.AmendLine(f.admin, f.propID, res.ID, id, reservations.LinePatch{Version: got.Version, BedLocked: &off})
	must(t, err)
	if a := nightAmounts(got.Rooms[0]); a[0] != "1000000" || got.Rooms[0].NightlyRates[0].BedAdjustment.String() != "0" {
		t.Fatalf("unlocked: %v", a)
	}
}

func TestSupplementAndComplimentaryAndOverride(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	f.addSupplement(t, king.ID, "AMOUNT", "50000", "2026-10-01")

	// a complimentary plan stays at zero whatever the supplement
	comp := f.compPlan(t, "COMP", "COMPLIMENTARY")
	line := f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-03")
	line.RatePlanID, line.OccupancyReason = comp, "Owner guest"
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, line))
	must(t, err)
	if a := nightAmounts(res.Rooms[0]); a[0] != "0" || res.Rooms[0].NightlyRates[0].BedAdjustment.String() != "0" {
		t.Fatalf("complimentary: %v %+v", a, res.Rooms[0].NightlyRates[0])
	}

	// an override works on the price after the supplement: it replaces the amount, the supplement stays on record
	over := f.input(true, f.keep(f.dlx, king.ID, "2026-10-05", "2026-10-07"))
	overrideOn(&over, "2026-10-05", "700000")
	over.RateOverrideReason = "Negotiated"
	res2, err := f.Res.Create(f.admin, f.propID, "", over)
	must(t, err)
	l := res2.Rooms[0]
	if a := nightAmounts(l); a[0] != "700000" || a[1] != "1050000" {
		t.Fatalf("override: %v", a)
	}
	if !l.NightlyRates[0].IsOverride || l.NightlyRates[0].BaseRate == nil || l.NightlyRates[0].BaseRate.String() != "1050000" {
		t.Fatalf("the base of an overridden night is the price after the supplement: %+v", l.NightlyRates[0])
	}
}
