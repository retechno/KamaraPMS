package availability_test

import (
	"testing"

	"kamarapms/internal/availability"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

// bedFixture is the fixture of the type with three rooms: 101 and 102 with the first bed type of the catalogue (the "king"
// below), and 103 with another one (the "twin").
type bedFixture struct {
	*fixture
	king, twin int64
	r103       rooms.Room
}

func setupBeds(t *testing.T) *bedFixture {
	t.Helper()
	f := setup(t)
	king := f.e.FirstBedType(t, f.propID)
	var twin int64
	list, err := f.e.Rooms.ListBedTypes(f.ctx, f.propID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range list {
		if b.ID != king {
			twin = b.ID
			break
		}
	}
	return &bedFixture{fixture: f, king: king, twin: twin, r103: f.e.RoomWithBed(t, f.ctx, f.propID, f.typ.ID, twin, "103")}
}

// locked seeds a CONFIRMED line without a room that keeps a bed.
func (f *bedFixture) locked(t *testing.T, bed int64, from, to string) int64 {
	t.Helper()
	id := f.e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, from, to, "CONFIRMED")
	if err := f.e.Exec(t, `UPDATE reservation_rooms SET requested_bed_type_id = $1, bed_locked = true WHERE id = $2`, bed, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *bedFixture) stock(t *testing.T, bed int64, day string, exclude *int64) availability.BedStock {
	t.Helper()
	k := availability.BedKey{RoomTypeID: f.typ.ID, BedTypeID: bed}
	got, err := f.av.BedStock(f.ctx, f.tenantID, f.propID, []availability.BedKey{k}, []civil.Date{d(day)}, roomstest.BD, exclude)
	if err != nil {
		t.Fatal(err)
	}
	return got[k][d(day)]
}

func (f *bedFixture) demand(bed int64, from, to string) availability.Demand {
	dm := availability.NewDemand()
	dm.Add(f.typ.ID, bed, d(from), d(to), 1)
	return dm
}

func TestBedStockCountsFixedAndLocked(t *testing.T) {
	f := setupBeds(t)
	if s := f.stock(t, f.king, "2026-10-01", nil); s != (availability.BedStock{Sellable: 2}) {
		t.Fatalf("empty: %+v", s)
	}
	own := f.locked(t, f.king, "2026-10-01", "2026-10-03")
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-01", "2026-10-03", "CONFIRMED") // no preference: not in the bed line
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, f.r101.ID, "2026-10-01", "2026-10-02", "CONFIRMED")
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, f.r103.ID, "2026-10-01", "2026-10-02", "CONFIRMED") // in a twin room
	if s := f.stock(t, f.king, "2026-10-01", nil); s.Sellable != 2 || s.Fixed != 1 || s.Locked != 1 || s.Free() != 0 {
		t.Fatalf("king on the first night: %+v", s)
	}
	if s := f.stock(t, f.king, "2026-10-02", nil); s.Fixed != 0 || s.Locked != 1 || s.Free() != 1 {
		t.Fatalf("king on the second night (the room line left): %+v", s)
	}
	if s := f.stock(t, f.twin, "2026-10-01", nil); s.Sellable != 1 || s.Fixed != 1 || s.Locked != 0 {
		t.Fatalf("twin: %+v", s)
	}
	// a line's own demand can be left out while it is amended
	if s := f.stock(t, f.king, "2026-10-02", &own); s.Locked != 0 {
		t.Fatalf("without the line itself: %+v", s)
	}
	// a night after the stay has no demand; a draft, cancelled or completed line holds nothing
	if err := f.e.Exec(t, `UPDATE reservation_rooms SET status = 'CANCELLED', cancelled_at = now() WHERE id = $1`, own); err != nil {
		t.Fatal(err)
	}
	if s := f.stock(t, f.king, "2026-10-02", nil); s.Locked != 0 {
		t.Fatalf("cancelled: %+v", s)
	}
}

func TestLastBedCannotBeLockedTwice(t *testing.T) {
	f := setupBeds(t)
	f.locked(t, f.king, "2026-10-01", "2026-10-03") // king: 2 rooms, 1 kept; the type: 3 rooms, 1 held
	if err := f.av.RequireAvailableFor(f.ctx, f.tenantID, f.propID, roomstest.BD, f.demand(f.king, "2026-10-01", "2026-10-03"), nil); err != nil {
		t.Fatalf("the second king: %v", err)
	}
	f.locked(t, f.king, "2026-10-01", "2026-10-03") // both kings are kept now; the type still has a room
	err := f.av.RequireAvailableFor(f.ctx, f.tenantID, f.propID, roomstest.BD, f.demand(f.king, "2026-10-01", "2026-10-03"), nil)
	got := roomstest.Code(t, err, "BED_NOT_AVAILABLE")
	if got.Context["short_nights"] != 2 {
		t.Fatalf("context: %v", got.Context)
	}
	// a booking with no preference, or one that keeps the twin, still fits
	if err := f.av.RequireAvailableFor(f.ctx, f.tenantID, f.propID, roomstest.BD, f.demand(0, "2026-10-01", "2026-10-03"), nil); err != nil {
		t.Fatalf("no preference: %v", err)
	}
	if err := f.av.RequireAvailableFor(f.ctx, f.tenantID, f.propID, roomstest.BD, f.demand(f.twin, "2026-10-01", "2026-10-03"), nil); err != nil {
		t.Fatalf("twin: %v", err)
	}
	// when the type itself is full the answer is the type's
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-01", "2026-10-03", "CONFIRMED")
	err = f.av.RequireAvailableFor(f.ctx, f.tenantID, f.propID, roomstest.BD, f.demand(f.king, "2026-10-01", "2026-10-03"), nil)
	roomstest.Want(t, err, "ROOM_TYPE_NOT_AVAILABLE")
}

func TestBlockAndRemovalRespectTheBedLine(t *testing.T) {
	f := setupBeds(t)
	f.locked(t, f.king, "2026-10-01", "2026-10-02")
	f.locked(t, f.king, "2026-10-01", "2026-10-02") // both kings are kept on the first night; the twin room is free
	block := func(room int64, from, to string) error {
		_, err := f.e.Rooms.CreateBlock(f.ctx, f.propID, rooms.CreateBlockInput{RoomID: room, BlockType: "OOO", StartDate: d(from), EndDate: d(to), Reason: "x"})
		return err
	}
	// the type has a room to spare (3 rooms, 2 held), but blocking a king takes one from the kings that are all kept
	roomstest.Want(t, block(f.r101.ID, "2026-10-01", "2026-10-02"), "BED_NOT_AVAILABLE")
	off := false
	_, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r102.ID, rooms.RoomPatch{IsActive: &off})
	roomstest.Want(t, err, "BED_NOT_AVAILABLE")
	// the night the kings are free, and the twin room, can be blocked
	if err := block(f.r101.ID, "2026-10-02", "2026-10-04"); err != nil {
		t.Fatalf("a night without kept kings: %v", err)
	}
	if err := block(f.r103.ID, "2026-10-01", "2026-10-02"); err != nil {
		t.Fatalf("the twin room: %v", err)
	}
}

func TestChangingTheBedOfARoom(t *testing.T) {
	f := setupBeds(t)
	toTwin, toKing := rooms.RoomPatch{BedTypeID: &f.twin}, rooms.RoomPatch{BedTypeID: &f.king}
	f.locked(t, f.king, "2026-10-01", "2026-10-03")
	f.locked(t, f.king, "2026-10-01", "2026-10-03") // both kings are kept
	// a free king room cannot become a twin: the kept kings would have no room
	_, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r101.ID, toTwin)
	roomstest.Want(t, err, "BED_NOT_AVAILABLE")
	// the twin room can become a king: that only adds
	if _, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r103.ID, toKing); err != nil {
		t.Fatalf("a twin to a king: %v", err)
	}
	// now three kings: one may leave
	if _, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r101.ID, toTwin); err != nil {
		t.Fatalf("a king to a twin with a king to spare: %v", err)
	}
	// a room that holds a booking takes it along: the line stays, so the kings left are enough
	if err := f.e.Exec(t, `UPDATE reservation_rooms SET status = 'CANCELLED', cancelled_at = now() WHERE bed_locked`); err != nil {
		t.Fatal(err)
	}
	f.locked(t, f.king, "2026-10-01", "2026-10-03") // 2 kings now (102, 103), one kept
	f.e.Line(t, f.tenantID, f.propID, f.typ.ID, f.r102.ID, "2026-10-01", "2026-10-03", "CONFIRMED")
	// king: sellable 2 (102, 103), fixed 1 (102), kept 1: full. 102 becomes a twin together with its booking.
	if _, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r102.ID, toTwin); err != nil {
		t.Fatalf("a room with its own booking moves with it: %v", err)
	}
}

func TestARoomCannotLoseTheBedAReservationKeeps(t *testing.T) {
	f := setupBeds(t)
	id := f.locked(t, f.king, "2026-10-01", "2026-10-03")
	if err := f.e.Exec(t, `UPDATE reservation_rooms SET room_id = $1 WHERE id = $2`, f.r101.ID, id); err != nil {
		t.Fatal(err)
	}
	toTwin := rooms.RoomPatch{BedTypeID: &f.twin}
	_, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r101.ID, toTwin)
	got := roomstest.Code(t, err, "ROOM_BED_LOCKED")
	if got.Context["lines"] == nil {
		t.Fatalf("context: %v", got.Context)
	}
	// the other king room has no such line
	if _, err := f.e.Rooms.UpdateRoom(f.ctx, f.propID, f.r102.ID, toTwin); err != nil {
		t.Fatalf("the other king: %v", err)
	}
}
