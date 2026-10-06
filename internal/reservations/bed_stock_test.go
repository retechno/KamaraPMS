package reservations_test

import (
	"testing"

	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
)

// bedStock: DLX has two rooms, 101 with a king and 102 with a twin (the first bed type of the catalogue is King, then Queen,
// Double and Twin); STD has 201 with a king. So the king variant of DLX has one room.
func (f *fx) twinForRoom102(t *testing.T) (king, twin rooms.BedType) {
	t.Helper()
	king, twin = f.bed(t, "KING"), f.bed(t, "TWIN")
	_, err := f.Rooms.UpdateRoom(f.admin, f.propID, f.r102.ID, rooms.RoomPatch{BedTypeID: &twin.ID})
	must(t, err)
	return king, twin
}

func (f *fx) keep(typ rooms.RoomType, bed int64, arrival, departure string) reservations.LineInput {
	l := f.line(typ, arrival, departure)
	l.BedTypeID, l.BedLocked = &bed, true
	return l
}

func TestLockedLineUsesTheStockOfItsVariant(t *testing.T) {
	f := setup(t)
	king, twin := f.twinForRoom102(t)

	// The only king of DLX can be kept once; the type still has a room, which a booking with no preference can take.
	_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-03", "2026-10-05")))
	e := code(t, err, "BED_NOT_AVAILABLE")
	if e.Context["short_nights"] != 1 { // only the night of the 3rd is shared
		t.Fatalf("context: %v", e.Context)
	}
	// a soft request for the king is not kept: it only uses the type
	soft := f.line(f.dlx, "2026-10-02", "2026-10-04")
	soft.BedTypeID = &king.ID
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, soft))
	must(t, err)
	// now the type is full: the answer is the type's, whatever bed is asked for
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, twin.ID, "2026-10-02", "2026-10-04")))
	wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	// the other type is not touched
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.std, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
}

func TestLockingAndUnlockingMovesTheStock(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	a, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	soft := f.line(f.dlx, "2026-10-02", "2026-10-04")
	soft.BedTypeID = &king.ID
	b, err := f.Res.Create(f.admin, f.propID, "", f.input(true, soft))
	must(t, err)

	on, off := true, false
	// the second line cannot keep the king while the first one does
	_, err = f.Res.AmendLine(f.admin, f.propID, b.ID, b.Rooms[0].ID, reservations.LinePatch{Version: b.Version, BedLocked: &on})
	wantCode(t, err, "BED_NOT_AVAILABLE")
	// unlocking the first one frees it, and the second can keep it
	_, err = f.Res.AmendLine(f.admin, f.propID, a.ID, a.Rooms[0].ID, reservations.LinePatch{Version: a.Version, BedLocked: &off})
	must(t, err)
	got, err := f.Res.AmendLine(f.admin, f.propID, b.ID, b.Rooms[0].ID, reservations.LinePatch{Version: b.Version, BedLocked: &on})
	must(t, err)
	if !got.Rooms[0].BedLocked {
		t.Fatalf("locked: %+v", got.Rooms[0])
	}
	// and now the first one cannot lock it again
	_, err = f.Res.AmendLine(f.admin, f.propID, a.ID, a.Rooms[0].ID, reservations.LinePatch{Version: a.Version + 1, BedLocked: &on})
	wantCode(t, err, "BED_NOT_AVAILABLE")
}

func TestDraftLockedLinesAreCheckedWhenConfirmed(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	a, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	b, err := f.Res.Create(f.admin, f.propID, "", f.input(false, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	_, err = f.Res.Confirm(f.admin, f.propID, a.ID, a.Version)
	must(t, err)
	_, err = f.Res.Confirm(f.admin, f.propID, b.ID, b.Version)
	wantCode(t, err, "BED_NOT_AVAILABLE")
}

func TestAssigningARoomToALockedLine(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	locked, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")))
	must(t, err)
	// the twin room does not have the bed that is kept
	_, err = f.Res.AssignRoom(f.admin, f.propID, locked.ID, locked.Rooms[0].ID, locked.Version, f.r102.ID, false)
	c := code(t, err, "VALIDATION_FAILED")
	if len(c.Fields) != 1 || c.Fields[0].Field != "room_id" || c.Fields[0].Code != "ROOM_BED_MISMATCH" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	// a line that has no preference takes the king room only when no kept bed needs it
	free, err := f.Res.Create(f.admin, f.propID, "", f.input(true, f.line(f.dlx, "2026-10-02", "2026-10-04")))
	must(t, err)
	_, err = f.Res.AssignRoom(f.admin, f.propID, free.ID, free.Rooms[0].ID, free.Version, f.r101.ID, false)
	wantCode(t, err, "BED_NOT_AVAILABLE") // the king is kept for the locked line
	got, err := f.Res.AssignRoom(f.admin, f.propID, free.ID, free.Rooms[0].ID, free.Version, f.r102.ID, false)
	must(t, err)
	if got.Rooms[0].RoomNumber != "102" {
		t.Fatalf("the twin room: %+v", got.Rooms[0])
	}
	// the locked line takes its own king room, and unassigning gives the demand back unchanged
	got, err = f.Res.AssignRoom(f.admin, f.propID, locked.ID, locked.Rooms[0].ID, locked.Version, f.r101.ID, false)
	must(t, err)
	if got.Rooms[0].RoomNumber != "101" || !got.Rooms[0].BedLocked {
		t.Fatalf("the king room: %+v", got.Rooms[0])
	}
	got, err = f.Res.UnassignRoom(f.admin, f.propID, locked.ID, locked.Rooms[0].ID, got.Version)
	must(t, err)
	if got.Rooms[0].RoomID != nil || !got.Rooms[0].BedLocked {
		t.Fatalf("unassigned: %+v", got.Rooms[0])
	}
}

func TestBookingWithARoomAndAKeptBed(t *testing.T) {
	f := setup(t)
	king, _ := f.twinForRoom102(t)
	l := f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-04")
	l.RoomID = &f.r102.ID // the twin room
	_, err := f.Res.Create(f.admin, f.propID, "", f.input(true, l))
	c := code(t, err, "VALIDATION_FAILED")
	if len(c.Fields) != 1 || c.Fields[0].Field != "rooms[0].room_id" || c.Fields[0].Code != "ROOM_BED_MISMATCH" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	l.RoomID = &f.r101.ID
	res, err := f.Res.Create(f.admin, f.propID, "", f.input(true, l))
	must(t, err)
	if res.Rooms[0].RoomNumber != "101" || !res.Rooms[0].BedLocked {
		t.Fatalf("booked: %+v", res.Rooms[0])
	}
	// the king is taken by that line now, whether it has the room or not
	_, err = f.Res.Create(f.admin, f.propID, "", f.input(true, f.keep(f.dlx, king.ID, "2026-10-02", "2026-10-03")))
	wantCode(t, err, "BED_NOT_AVAILABLE")
}
