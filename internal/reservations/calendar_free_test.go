package reservations_test

import (
	"testing"

	"kamarapms/internal/rooms"
)

// The calendar counts the held rooms that are complimentary or for house use; they stay part of the held rooms.
func TestCalendarComplimentaryAndHouseUse(t *testing.T) {
	f := setup(t) // DLX: 101, 102
	comp, house := f.compPlan(t, "COMP", "COMPLIMENTARY"), f.compPlan(t, "HOUSE", "HOUSE_USE")
	king := f.bed(t, "KING")
	_, err := f.Rooms.UpdateRoom(f.admin, f.propID, f.r101.ID, rooms.RoomPatch{BedTypeID: &king.ID})
	must(t, err)

	free := f.line(f.dlx, "2026-10-02", "2026-10-04")
	free.RatePlanID, free.OccupancyReason = comp, "Owner guest"
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, free))
	must(t, err)
	_, err = f.Res.AssignRoom(f.admin, f.propID, res.ID, res.Rooms[0].ID, res.Version, f.r101.ID, false)
	must(t, err)
	used := f.line(f.dlx, "2026-10-03", "2026-10-04")
	used.RatePlanID, used.OccupancyReason = house, "Staff"
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, used))
	must(t, err)

	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-05"), true)
	must(t, err)
	dlx := cal.RoomTypes[0]
	for i, want := range []struct{ comp, house, held int }{{1, 0, 1}, {1, 1, 2}, {0, 0, 0}} {
		n := dlx.Nights[i]
		if n.Complimentary != want.comp || n.HouseUse != want.house || n.Held != want.held {
			t.Fatalf("night %d: %+v want %+v", i, n, want)
		}
		if tot := cal.Totals[i]; tot.Complimentary != want.comp || tot.HouseUse != want.house {
			t.Fatalf("totals %d: %+v", i, tot)
		}
	}
	// per bed: only the line assigned to the King room counts there
	if k := dlx.Beds[0]; k.Code != "KING" || k.Nights[0].Complimentary != 1 || k.Nights[1].HouseUse != 0 {
		t.Fatalf("King: %+v", k)
	}
}
