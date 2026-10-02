package availability_test

import (
	"context"
	"os"
	"testing"

	"kamarapms/internal/availability"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

func d(s string) civil.Date { return civil.MustParseDate(s) }

type fixture struct {
	e        *roomstest.Env
	av       *availability.Service
	tenantID int64
	propID   int64
	ctx      context.Context
	typ      rooms.RoomType
	r101     rooms.Room
	r102     rooms.Room
}

func setup(t *testing.T) *fixture {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	typ := e.RoomType(t, ctx, p.ID, "DLX")
	return &fixture{
		e: e, av: availability.NewService(e.TxM), tenantID: tn.ID, propID: p.ID, ctx: ctx, typ: typ,
		r101: e.Room(t, ctx, p.ID, typ.ID, "101"), r102: e.Room(t, ctx, p.ID, typ.ID, "102"),
	}
}

func (f *fixture) night(t *testing.T, day string, exclude *int64) availability.Night {
	t.Helper()
	inv, err := f.av.Inventory(f.ctx, f.tenantID, f.propID, []int64{f.typ.ID}, []civil.Date{d(day)}, roomstest.BD, exclude)
	if err != nil {
		t.Fatal(err)
	}
	return inv[f.typ.ID][d(day)]
}

func TestInventoryCountsOnlyConfirmedAndOpenStays(t *testing.T) {
	f := setup(t)
	e := f.e
	if n := f.night(t, "2026-10-05", nil); n.Sellable != 2 || n.Demand != 0 || n.Available != 2 {
		t.Fatalf("empty: %+v", n)
	}
	// confirmed type-level lines hold inventory; a completed line holds nothing
	line := e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-04", "2026-10-06", "CONFIRMED")
	e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-04", "2026-10-06", "CONFIRMED")
	e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-04", "2026-10-06", "COMPLETED")
	if n := f.night(t, "2026-10-05", nil); n.Demand != 2 || n.Available != 0 {
		t.Fatalf("two confirmed: %+v", n)
	}
	// departure day is free: same-day turnover
	if n := f.night(t, "2026-10-06", nil); n.Demand != 0 {
		t.Fatalf("departure night holds nothing: %+v", n)
	}
	// a line's own demand can be excluded while amending it
	if n := f.night(t, "2026-10-05", &line); n.Demand != 1 {
		t.Fatalf("exclude own: %+v", n)
	}
}

func TestInventoryOpenStayHoldsThroughTomorrow(t *testing.T) {
	f := setup(t)
	// in-house since today, overdue departure (yesterday's date) still holds the room tonight
	f.e.Stay(t, f.tenantID, f.propID, f.typ.ID, f.r101.ID, "2026-09-29", "2026-09-30")
	if n := f.night(t, "2026-09-30", nil); n.Demand != 1 {
		t.Fatalf("overstay tonight: %+v", n)
	}
	f.e.Stay(t, f.tenantID, f.propID, f.typ.ID, f.r102.ID, "2026-09-30", "2026-10-03")
	if n := f.night(t, "2026-10-02", nil); n.Demand != 1 || n.Available != 1 {
		t.Fatalf("second stay: %+v", n)
	}
}

func TestBlocksReduceSellableAndOversellIsRejected(t *testing.T) {
	f := setup(t)
	e := f.e
	block := func(room int64, typ, from, to string) error {
		_, err := e.Rooms.CreateBlock(f.ctx, f.propID, rooms.CreateBlockInput{RoomID: room, BlockType: typ, StartDate: d(from), EndDate: d(to), Reason: "x"})
		return err
	}
	e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-01", "2026-10-04", "CONFIRMED")
	if err := block(f.r101.ID, "OOO", "2026-10-01", "2026-10-03"); err != nil {
		t.Fatalf("one room may be blocked while one is booked: %v", err)
	}
	if n := f.night(t, "2026-10-02", nil); n.Sellable != 1 || n.Available != 0 {
		t.Fatalf("after OOO: %+v", n)
	}
	// blocking the second room on a booked night would oversell
	err := block(f.r102.ID, "OOS", "2026-10-02", "2026-10-03")
	got := roomstest.Code(t, err, "INVENTORY_OVERSOLD")
	if got.Context["short_nights"] != 1 {
		t.Fatalf("context: %v", got.Context)
	}
	// a night nobody booked is fine
	if err := block(f.r102.ID, "OOS", "2026-10-04", "2026-10-06"); err != nil {
		t.Fatalf("free nights: %v", err)
	}
}

func TestRoomChangeRespectsInventory(t *testing.T) {
	f := setup(t)
	e := f.e
	e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-01", "2026-10-03", "CONFIRMED")
	e.Line(t, f.tenantID, f.propID, f.typ.ID, 0, "2026-10-01", "2026-10-03", "CONFIRMED")
	off := false
	_, err := e.Rooms.UpdateRoom(f.ctx, f.propID, f.r101.ID, rooms.RoomPatch{IsActive: &off})
	roomstest.Want(t, err, "INVENTORY_OVERSOLD")
	std := e.RoomType(t, f.ctx, f.propID, "STD")
	_, err = e.Rooms.UpdateRoom(f.ctx, f.propID, f.r101.ID, rooms.RoomPatch{RoomTypeID: &std.ID})
	roomstest.Want(t, err, "INVENTORY_OVERSOLD")

	// with only one booking left there is room to spare
	if err := e.Exec(t, `UPDATE reservation_rooms SET status = 'CANCELLED', cancelled_at = now() WHERE id = (SELECT min(id) FROM reservation_rooms)`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Rooms.UpdateRoom(f.ctx, f.propID, f.r101.ID, rooms.RoomPatch{IsActive: &off}); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
}

func TestRoomIssuesAndFreeRooms(t *testing.T) {
	f := setup(t)
	e := f.e
	e.Line(t, f.tenantID, f.propID, f.typ.ID, f.r101.ID, "2026-10-01", "2026-10-03", "CONFIRMED")
	issues, err := f.av.RoomIssues(f.ctx, f.tenantID, f.propID, f.r101.ID, roomstest.BD, d("2026-10-02"), d("2026-10-04"), nil)
	if err != nil || len(issues) != 1 || issues[0].Kind != availability.IssueReserved {
		t.Fatalf("reserved: %v %+v", err, issues)
	}
	// back-to-back is fine: the earlier line departs the day the new one arrives
	if issues, err = f.av.RoomIssues(f.ctx, f.tenantID, f.propID, f.r101.ID, roomstest.BD, d("2026-10-03"), d("2026-10-05"), nil); err != nil || len(issues) != 0 {
		t.Fatalf("turnover: %v %+v", err, issues)
	}
	free, err := f.av.FreeRooms(f.ctx, f.tenantID, f.propID, f.typ.ID, roomstest.BD, d("2026-10-02"), d("2026-10-04"))
	if err != nil || len(free) != 1 || free[0].RoomNumber != "102" {
		t.Fatalf("free rooms: %v %+v", err, free)
	}
}

func TestFindShortfallsPure(t *testing.T) {
	inv := map[int64]map[civil.Date]availability.Night{1: {
		d("2026-10-01"): {Date: d("2026-10-01"), Sellable: 2, Demand: 1, Available: 1},
		d("2026-10-02"): {Date: d("2026-10-02"), Sellable: 2, Demand: 2, Available: 0},
	}}
	extra := availability.Extra{}
	extra.Add(1, d("2026-10-01"), d("2026-10-03"), 1)
	got := availability.FindShortfalls(inv, extra)
	if len(got) != 1 || got[0].Date != d("2026-10-02") || got[0].Requested != 1 {
		t.Fatalf("%+v", got)
	}
}
