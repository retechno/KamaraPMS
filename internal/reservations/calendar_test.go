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

	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-06"), false)
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
	cal, err = f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-03"), false)
	must(t, err)
	if n := nightOf(t, cal, "DLX", "2026-10-02"); n.Sellable != 2 || n.Blocked != 0 {
		t.Fatalf("an inactive room is left out: %+v", n)
	}
}

func TestAvailabilityCalendarWindowAndPermission(t *testing.T) {
	f := setup(t)
	_, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-05"), d("2026-10-05"), false)
	if c := code(t, err, "VALIDATION_FAILED"); c.Fields[0].Field != "to" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	_, err = f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-01"), d("2026-12-31"), false) // more than 62 days
	wantCode(t, err, "VALIDATION_FAILED")
	if cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-01"), d("2026-12-02"), false); err != nil || len(cal.Totals) != 62 {
		t.Fatalf("62 nights are allowed: %v", err)
	}
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead)
	_, err = f.Res.AvailabilityCalendar(nobody, f.propID, d("2026-10-01"), d("2026-10-05"), false)
	wantCode(t, err, "PERMISSION_DENIED")
	foreign := f.Tenant(t, "XYZ")
	_, err = f.Res.AvailabilityCalendar(roomstest.Admin(foreign.ID), f.propID, d("2026-10-01"), d("2026-10-05"), false)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// With byBed each room type lists its bed types, counted over the rooms with that bed and the bookings assigned to them.
func TestAvailabilityCalendarBeds(t *testing.T) {
	f := setup(t) // DLX: 101, 102; STD: 201
	king, twin := f.bed(t, "KING"), f.bed(t, "TWIN")
	for _, r := range []struct {
		id  int64
		bed int64
	}{{f.r101.ID, king.ID}, {f.r102.ID, twin.ID}, {f.r201.ID, king.ID}} {
		_, err := f.Rooms.UpdateRoom(f.admin, f.propID, r.id, rooms.RoomPatch{BedTypeID: &r.bed})
		must(t, err)
	}
	res := f.book(t, f.dlx, "2026-10-02", "2026-10-04")
	_, err := f.Res.AssignRoom(f.admin, f.propID, res.ID, res.Rooms[0].ID, res.Version, f.r101.ID, false)
	must(t, err)
	f.book(t, f.dlx, "2026-10-02", "2026-10-04") // no room yet: not counted per bed

	plain, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-04"), false)
	must(t, err)
	if len(plain.RoomTypes[0].Beds) != 0 {
		t.Fatalf("beds only when asked for: %+v", plain.RoomTypes[0].Beds)
	}

	cal, err := f.Res.AvailabilityCalendar(f.admin, f.propID, d("2026-10-02"), d("2026-10-04"), true)
	must(t, err)
	dlx, std := cal.RoomTypes[0], cal.RoomTypes[1]
	if dlx.Code != "DLX" || len(dlx.Beds) != 2 || len(std.Beds) != 1 {
		t.Fatalf("beds per type: %+v", cal.RoomTypes)
	}
	k, tw := dlx.Beds[0], dlx.Beds[1] // catalogue order: King before Twin
	if k.Code != "KING" || k.RoomsTotal != 1 || k.Nights[0].Sellable != 1 || k.Nights[0].Held != 1 || k.Nights[0].Available != 0 {
		t.Fatalf("DLX King: %+v", k)
	}
	if tw.Code != "TWIN" || tw.Nights[0].Held != 0 || tw.Nights[0].Available != 1 {
		t.Fatalf("DLX Twin: %+v", tw)
	}
	if std.Beds[0].Code != "KING" || std.Beds[0].Nights[0].Available != 1 {
		t.Fatalf("STD King: %+v", std.Beds[0])
	}
	// the type row still counts the unassigned booking, and the totals stay per room type
	if dlx.Nights[0].Held != 2 || cal.Totals[0].Held != 2 {
		t.Fatalf("type and totals are unchanged: %+v %+v", dlx.Nights[0], cal.Totals[0])
	}
}
