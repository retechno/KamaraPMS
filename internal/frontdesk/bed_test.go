package frontdesk_test

import (
	"testing"

	"kamarapms/internal/frontdesk"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
)

// bedSetup makes 102 a twin room (101 and 201 keep the first bed type of the catalogue, the king) and cleans it.
func (f *fx) bedSetup(t *testing.T) (king, twin int64) {
	t.Helper()
	king = f.FirstBedType(t, f.propID)
	list, err := f.Rooms.ListBedTypes(f.admin, f.propID, nil)
	must(t, err)
	for _, b := range list {
		if b.Code == "TWIN" {
			twin = b.ID
		}
	}
	_, err = f.Rooms.UpdateRoom(f.admin, f.propID, f.r102.ID, rooms.RoomPatch{BedTypeID: &twin})
	must(t, err)
	f.clean(t, f.r102.ID)
	return king, twin
}

// keep confirms a reservation of a DLX room for the business date that keeps a bed.
func (f *fx) keep(t *testing.T, bed int64, departure string) reservations.Reservation {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d("2026-09-30"), Departure: d(departure), Adults: 2, BedTypeID: &bed, BedLocked: true},
	}})
	must(t, err)
	return res
}

func TestCheckInOfAKeptBedNeedsARoomWithIt(t *testing.T) {
	f := setup(t)
	king, _ := f.bedSetup(t)
	res := f.keep(t, king, "2026-10-02")
	_, err := f.checkIn(t, f.admin, res, &f.r102, "kb-1") // the twin room
	c := code(t, err, "VALIDATION_FAILED")
	if len(c.Fields) != 1 || c.Fields[0].Field != "room_id" || c.Fields[0].Code != "ROOM_BED_MISMATCH" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	if _, err := f.checkIn(t, f.admin, res, &f.r101, "kb-2"); err != nil {
		t.Fatalf("the king room: %v", err)
	}
}

func TestCheckInOfAFreeLineCannotTakeTheKingThatIsKept(t *testing.T) {
	f := setup(t)
	king, _ := f.bedSetup(t)
	f.keep(t, king, "2026-10-02")                        // keeps the only king of DLX
	free := f.book(t, f.dlx, "2026-09-30", "2026-10-02") // no preference
	_, err := f.checkIn(t, f.admin, free, &f.r101, "kb-3")
	wantCode(t, err, "BED_NOT_AVAILABLE")
	if _, err := f.checkIn(t, f.admin, free, &f.r102, "kb-4"); err != nil {
		t.Fatalf("the twin room: %v", err)
	}
}

func TestMoveOutOfAKeptBedIsAuditedAndMoveIntoAKeptBedIsRefused(t *testing.T) {
	f := setup(t)
	king, twin := f.bedSetup(t)
	f.RoomWithBed(t, f.admin, f.propID, f.dlx.ID, twin, "103") // the type has a room to spare, the kings do not
	// a stay of a kept king moves to the twin room: allowed (the line is checked in, its demand is the stay), with the request in the audit
	res := f.keep(t, king, "2026-10-03")
	in, err := f.checkIn(t, f.admin, res, &f.r101, "kb-5")
	must(t, err)
	_, err = f.Front.Move(f.admin, f.propID, in.Stay.ID, frontdesk.MoveInput{Version: in.Stay.Version, RoomID: f.r102.ID, Reason: "noise"})
	must(t, err)
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'stay.room_moved' AND new_data ? 'kept_bed_type_id'`); n != 1 {
		t.Fatalf("the audit of the move names the bed that was kept: %d", n)
	}
	// the king room is free now; a stay from the other type moving into it would take the king that a reservation keeps
	f.keep(t, king, "2026-10-03")
	std := f.book(t, f.std, "2026-09-30", "2026-10-03")
	out, err := f.checkIn(t, f.admin, std, &f.r201, "kb-6")
	must(t, err)
	_, err = f.Front.Move(f.admin, f.propID, out.Stay.ID, frontdesk.MoveInput{Version: out.Stay.Version, RoomID: f.r101.ID, Reason: "upgrade"})
	wantCode(t, err, "BED_NOT_AVAILABLE")
}

func TestExtendingAStayChecksItsBed(t *testing.T) {
	f := setup(t)
	king, _ := f.bedSetup(t)
	st := f.stay(t, f.r101.ID, "2026-10-02") // in the king room until the 2nd
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN'`))
	// a reservation keeps a king for the night of the 2nd to the 3rd: the stay cannot be extended into it (the room, and the only king)
	res, err := f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{GuestID: &f.guest, Source: "PHONE", Confirm: true, Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d("2026-10-02"), Departure: d("2026-10-03"), Adults: 2, BedTypeID: &king, BedLocked: true},
	}})
	must(t, err)
	_ = res
	_, err = f.Front.ChangeDeparture(f.admin, f.propID, st.Stay.ID, frontdesk.ChangeDepartureInput{Version: st.Stay.Version, DepartureDate: d("2026-10-03")})
	wantCode(t, err, "BED_NOT_AVAILABLE")
}
