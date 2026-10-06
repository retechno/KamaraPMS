package reservations_test

import (
	"testing"

	"kamarapms/internal/reservations"
)

func dlxOffer(t *testing.T, res reservations.SearchResult, id int64) reservations.TypeOffer {
	t.Helper()
	for _, o := range res.RoomTypes {
		if o.RoomTypeID == id {
			return o
		}
	}
	t.Fatalf("no offer for room type %d", id)
	return reservations.TypeOffer{}
}

func bedOffer(t *testing.T, o reservations.TypeOffer, code string) reservations.BedOffer {
	t.Helper()
	for _, b := range o.Beds {
		if b.Code == code {
			return b
		}
	}
	t.Fatalf("no variant %s in %+v", code, o.Beds)
	return reservations.BedOffer{}
}

func TestSearchOffersTheVariantsWithTheirStockAndPrice(t *testing.T) {
	f := setup(t)
	king, twin := f.twinForRoom102(t)
	f.addSupplement(t, king.ID, "AMOUNT", "50000", "2026-10-01")
	f.addSupplement(t, twin.ID, "PERCENT", "-10", "2026-10-01")

	search := func() reservations.TypeOffer {
		res, err := f.Res.SearchAvailability(f.admin, f.propID, d("2026-10-02"), d("2026-10-04"), 2, 0)
		must(t, err)
		return dlxOffer(t, res, f.dlx.ID)
	}
	o := search()
	if len(o.Beds) != 2 || o.Beds[0].Code != "KING" || o.Beds[1].Code != "TWIN" {
		t.Fatalf("variants in the order of the catalogue: %+v", o.Beds)
	}
	k, tw := bedOffer(t, o, "KING"), bedOffer(t, o, "TWIN")
	if k.AvailableMin != 1 || tw.AvailableMin != 1 || o.AvailableMin != 2 {
		t.Fatalf("stock: king %d, twin %d, type %d", k.AvailableMin, tw.AvailableMin, o.AvailableMin)
	}
	// the type is "any bed" at the grid price; a variant adds its supplement
	if len(o.RatePlans) != 1 || o.RatePlans[0].Nightly[0].Amount.String() != "1000000" || o.RatePlans[0].Nightly[0].BedAdjustment.String() != "0" {
		t.Fatalf("type price: %+v", o.RatePlans)
	}
	if n := k.RatePlans[0].Nightly[0]; n.Amount.String() != "1050000" || n.BedAdjustment.String() != "50000" {
		t.Fatalf("king price: %+v", n)
	}
	if n := tw.RatePlans[0].Nightly[1]; n.Amount.String() != "900000" || n.BedAdjustment.String() != "-100000" {
		t.Fatalf("twin price: %+v", n)
	}
	if k.RatePlans[0].Estimate == nil || k.RatePlans[0].Estimate.Total.IsZero() {
		t.Fatalf("the estimate follows the price of the variant: %+v", k.RatePlans[0].Estimate)
	}

	// keeping the only king: that variant is sold out, the other one and the type still have rooms
	_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	o = search()
	k, tw = bedOffer(t, o, "KING"), bedOffer(t, o, "TWIN")
	if k.AvailableMin != 0 || k.PerNight[0].Kept != 1 || tw.AvailableMin != 1 || o.AvailableMin != 1 {
		t.Fatalf("after keeping the king: king %+v, twin %+v, type %d", k, tw, o.AvailableMin)
	}
	// a variant never offers more than the type has left
	f.book(t, f.dlx, "2026-10-02", "2026-10-04") // no preference: takes the last room of the type
	o = search()
	if tw := bedOffer(t, o, "TWIN"); tw.AvailableMin != 0 || o.AvailableMin != 0 {
		t.Fatalf("the type is full: twin %+v, type %d", tw, o.AvailableMin)
	}
}

func TestCalendarMarksTheNightsOnWhichABedIsTheLimit(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-03")))
	must(t, err)
	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-04"), true)
	must(t, err)
	var dlx reservations.CalendarType
	for _, ct := range cal.RoomTypes {
		if ct.RoomTypeID == f.dlx.ID {
			dlx = ct
		}
	}
	if len(dlx.Beds) != 2 {
		t.Fatalf("beds: %+v", dlx.Beds)
	}
	k, tw := dlx.Beds[0], dlx.Beds[1]
	if n := k.Nights[0]; n.Sellable != 1 || n.Held != 1 || n.Locked != 1 || n.Available != 0 || !n.BedLimited {
		t.Fatalf("king on the night it is kept: %+v", n)
	}
	if n := k.Nights[1]; n.Held != 0 || n.Locked != 0 || n.Available != 1 || n.BedLimited {
		t.Fatalf("king on the free night: %+v", n)
	}
	if n := tw.Nights[0]; n.Held != 0 || n.Available != 1 || n.BedLimited {
		t.Fatalf("twin: %+v", n)
	}
	// the row of the type is unchanged by the lock: one room is held
	if n := dlx.Nights[0]; n.Held != 1 || n.Available != 1 {
		t.Fatalf("type: %+v", n)
	}
}
