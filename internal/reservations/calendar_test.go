package reservations_test

import (
	"testing"

	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func nightOf(t *testing.T, c reservations.Calendar, code, date string) reservations.CalendarNight {
	t.Helper()
	for _, ty := range c.RoomTypes {
		if ty.Code != code {
			continue
		}
		for _, n := range ty.Nights {
			if n.Date.String() == date {
				return n
			}
		}
	}
	t.Fatalf("no night %s of %s", date, code)
	return reservations.CalendarNight{}
}

// The calendar counts, per room type and night, the rooms to sell, blocked, held and left, and sums them per night.
func TestAvailabilityCalendar(t *testing.T) {
	f := setup(t) // DLX: 101, 102; STD: 201
	f.book(t, f.dlx, "2026-10-02", "2026-10-04")
	f.book(t, f.dlx, "2026-10-03", "2026-10-05")
	f.book(t, f.std, "2026-10-03", "2026-10-04")
	_, err := f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.r102.ID, BlockType: "OOO", StartDate: d("2026-10-04"), EndDate: d("2026-10-06"), Reason: "AC"})
	must(t, err)

	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-06"))
	must(t, err)
	if len(cal.RoomTypes) != 2 || len(cal.Totals) != 4 || cal.RoomTypes[0].RoomsTotal+cal.RoomTypes[1].RoomsTotal != 3 {
		t.Fatalf("shape: %+v", cal)
	}

	// 2 Oct: one DLX held, both sellable
	if n := nightOf(t, cal, "DLX", "2026-10-02"); n.Sellable != 2 || n.Held != 1 || n.Available != 1 || n.Blocked != 0 || n.OccupancyPercent.String() != "50" {
		t.Fatalf("DLX 2 Oct: %+v", n)
	}
	// 3 Oct: both DLX held (the full house), the STD room held too
	if n := nightOf(t, cal, "DLX", "2026-10-03"); n.Held != 2 || n.Available != 0 || n.OccupancyPercent.String() != "100" {
		t.Fatalf("DLX 3 Oct: %+v", n)
	}
	// 4 Oct: room 102 is blocked, so one DLX room is sellable and the second booking holds it
	if n := nightOf(t, cal, "DLX", "2026-10-04"); n.Sellable != 1 || n.Blocked != 1 || n.Held != 1 || n.Available != 0 {
		t.Fatalf("DLX 4 Oct: %+v", n)
	}
	// the property totals add the types up
	if tot := cal.Totals[1]; tot.Date.String() != "2026-10-03" || tot.Sellable != 3 || tot.Held != 3 || tot.Available != 0 || tot.OccupancyPercent.String() != "100" {
		t.Fatalf("totals 3 Oct: %+v", tot)
	}
	if tot := cal.Totals[2]; tot.Sellable != 2 || tot.Blocked != 1 || tot.Held != 1 || tot.Available != 1 || tot.OccupancyPercent.String() != "50" {
		t.Fatalf("totals 4 Oct: %+v", tot)
	}

	// A room that is out of use does not count at all: it is neither sellable nor blocked.
	extra := f.Room(t, f.admin, f.propID, f.dlx.ID, "103")
	if _, err := f.Rooms.UpdateRoom(f.admin, f.propID, extra.ID, rooms.RoomPatch{IsActive: ptrBool(false)}); err != nil {
		t.Fatal(err)
	}
	cal, err = f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-03"))
	must(t, err)
	if n := nightOf(t, cal, "DLX", "2026-10-02"); n.Sellable != 2 || n.Blocked != 0 {
		t.Fatalf("an inactive room is left out: %+v", n)
	}
}

func TestAvailabilityCalendarWindowAndPermission(t *testing.T) {
	f := setup(t)
	_, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-05"), d("2026-10-05"))
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Field != "to" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	_, err = f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-01"), d("2026-12-31")) // more than 62 days
	wantCode(t, err, "VALIDATION_FAILED")
	if cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-01"), d("2026-12-02")); err != nil || len(cal.Totals) != 62 {
		t.Fatalf("62 nights are allowed: %v", err)
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Res.AvailabilityCalendar(nobody, f.propID, d("2026-10-01"), d("2026-10-05"))
	wantCode(t, err, "PERMISSION_DENIED")
	foreign := f.Tenant(t, "XYZ")
	_, err = f.Res.AvailabilityCalendar(roomstest.Admin(foreign.ID), f.propID, d("2026-10-01"), d("2026-10-05"))
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}
