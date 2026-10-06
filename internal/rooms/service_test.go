package rooms_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

func hkStatus(t *testing.T, e *roomstest.Env, roomID int64) string {
	t.Helper()
	var st string
	if err := e.Pool.QueryRow(context.Background(), `SELECT status FROM room_housekeeping WHERE room_id = $1`, roomID).Scan(&st); err != nil {
		t.Fatal(err)
	}
	return st
}

func d(s string) civil.Date { return civil.MustParseDate(s) }

func TestRoomTypeLifecycle(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)

	rt, err := e.Rooms.CreateRoomType(ctx, p.ID, rooms.RoomTypeInput{
		Code: " dlx ", Name: "Deluxe", Description: "Sea view", MaxAdult: 2, MaxChild: 1, MaxOccupancy: 3, BaseOccupancy: 2, SortOrder: 10, IsActive: true,
	})
	if err != nil || rt.Code != "DLX" || rt.MaxOccupancy != 3 || !rt.IsActive {
		t.Fatalf("create: %v %+v", err, rt)
	}
	_, err = e.Rooms.CreateRoomType(ctx, p.ID, rooms.RoomTypeInput{Code: "DLX", Name: "Again", MaxAdult: 1, MaxChild: 0, MaxOccupancy: 1, BaseOccupancy: 1, IsActive: true})
	wantCode(t, err, "CODE_TAKEN")

	_, err = e.Rooms.CreateRoomType(ctx, p.ID, rooms.RoomTypeInput{Code: "bad code", Name: "", MaxAdult: 0, MaxChild: 0, MaxOccupancy: 5, BaseOccupancy: 6})
	fields := map[string]bool{}
	for _, f := range code(t, err, "VALIDATION_FAILED").Fields {
		fields[f.Field] = true
	}
	for _, f := range []string{"code", "name", "max_adult", "max_occupancy", "base_occupancy"} {
		if !fields[f] {
			t.Errorf("missing field error %q in %v", f, fields)
		}
	}

	// Occupancy rules are checked against the merged result on update.
	three := int32(3)
	_, err = e.Rooms.UpdateRoomType(ctx, p.ID, rt.ID, rooms.RoomTypePatch{BaseOccupancy: &three, MaxOccupancy: ptr(int32(2))})
	wantCode(t, err, "VALIDATION_FAILED")
	name := "Deluxe Sea"
	upd, err := e.Rooms.UpdateRoomType(ctx, p.ID, rt.ID, rooms.RoomTypePatch{Name: &name})
	if err != nil || upd.Name != name || upd.Code != "DLX" {
		t.Fatalf("update: %v %+v", err, upd)
	}

	// Deactivation is blocked by an active room, then by a future confirmed line.
	room := e.Room(t, ctx, p.ID, rt.ID, "201")
	_, err = e.Rooms.UpdateRoomType(ctx, p.ID, rt.ID, rooms.RoomTypePatch{IsActive: ptr(false)})
	if c := code(t, err, "ROOM_TYPE_IN_USE"); c.Context["active_rooms"] != int64(1) {
		t.Fatalf("context: %v", c.Context)
	}
	if _, err := e.Rooms.UpdateRoom(ctx, p.ID, room.ID, rooms.RoomPatch{IsActive: ptr(false)}); err != nil {
		t.Fatal(err)
	}
	e.Line(t, tn.ID, p.ID, rt.ID, 0, "2026-10-05", "2026-10-07", "CONFIRMED")
	_, err = e.Rooms.UpdateRoomType(ctx, p.ID, rt.ID, rooms.RoomTypePatch{IsActive: ptr(false)})
	if c := code(t, err, "ROOM_TYPE_IN_USE"); c.Context["future_reservation_lines"] != int64(1) {
		t.Fatalf("context: %v", c.Context)
	}
	if err := e.Exec(t, `UPDATE reservation_rooms SET status = 'CANCELLED', cancelled_at = now() WHERE room_type_id = $1`, rt.ID); err != nil {
		t.Fatal(err)
	}
	if off, err := e.Rooms.UpdateRoomType(ctx, p.ID, rt.ID, rooms.RoomTypePatch{IsActive: ptr(false)}); err != nil || off.IsActive {
		t.Fatalf("deactivate: %v %+v", err, off)
	}

	// Lists and filters.
	active, err := e.Rooms.ListRoomTypes(ctx, p.ID, 0, ptr(true), 50)
	if err != nil || len(active) != 0 {
		t.Fatalf("active filter: %v %d", err, len(active))
	}
	if all, err := e.Rooms.ListRoomTypes(ctx, p.ID, 0, nil, 50); err != nil || len(all) != 1 {
		t.Fatalf("list: %v %d", err, len(all))
	}
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'room_type'`); n != 3 {
		t.Fatalf("audit entries for room types: %d, want created + updated + updated(deactivate)", n)
	}
}

func TestRoomCreateAndUpdateRules(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	other := e.Property(t, tn.ID, "JKT")
	ctx := roomstest.Admin(tn.ID)
	dlx := e.RoomType(t, ctx, p.ID, "DLX")
	std := e.RoomType(t, ctx, p.ID, "STD")
	foreign := e.RoomType(t, ctx, other.ID, "DLX")

	r201 := e.Room(t, ctx, p.ID, dlx.ID, "201") // default housekeeping: DIRTY
	if st := hkStatus(t, e, r201.ID); st != "DIRTY" {
		t.Fatalf("default housekeeping status %s", st)
	}
	clean := e.Room(t, ctx, p.ID, dlx.ID, "202", housekeeping.Clean)
	if st := hkStatus(t, e, clean.ID); st != "CLEAN" {
		t.Fatalf("initial housekeeping status %s", st)
	}
	if n := e.Count(t, `SELECT count(*) FROM housekeeping_logs`); n != 0 {
		t.Fatalf("creating a room writes no housekeeping log, got %d", n)
	}

	_, err := e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: dlx.ID, RoomNumber: "201", BedTypeID: ptr(e.FirstBedType(t, p.ID)), IsActive: true}})
	wantCode(t, err, "ROOM_NUMBER_TAKEN")
	_, err = e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: foreign.ID, RoomNumber: "301", BedTypeID: ptr(e.FirstBedType(t, p.ID)), IsActive: true}})
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND") // another property's type is invisible
	_, err = e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: dlx.ID, RoomNumber: "301", IsActive: true}, InitialHousekeeping: "NOPE"})
	wantCode(t, err, "VALIDATION_FAILED")
	if n := e.Count(t, `SELECT count(*) FROM rooms`); n != 2 {
		t.Fatalf("failed creates must leave nothing behind, rooms=%d", n)
	}

	// A type change or deactivation is blocked while a stay occupies the room...
	stay := e.Stay(t, tn.ID, p.ID, dlx.ID, r201.ID, "2026-09-29", "2026-10-02")
	_, err = e.Rooms.UpdateRoom(ctx, p.ID, r201.ID, rooms.RoomPatch{RoomTypeID: &std.ID})
	if c := code(t, err, "ROOM_IN_USE"); c.Context["conflicts"] == nil {
		t.Fatalf("conflicts missing: %v", c.Context)
	}
	_, err = e.Rooms.UpdateRoom(ctx, p.ID, r201.ID, rooms.RoomPatch{IsActive: ptr(false)})
	wantCode(t, err, "ROOM_IN_USE")
	// ...but plain attribute edits are fine.
	if upd, err := e.Rooms.UpdateRoom(ctx, p.ID, r201.ID, rooms.RoomPatch{Floor: ptr("2F"), Building: ptr("North")}); err != nil || upd.Floor != "2F" || upd.Building != "North" {
		t.Fatalf("attribute edit: %v %+v", err, upd)
	}
	// Once the stay has left, the room can change type.
	if err := e.Exec(t, `UPDATE stay_rooms SET check_out_at = now(), end_business_date = $1::date WHERE stay_id = $2`, "2026-09-30", stay); err != nil {
		t.Fatal(err)
	}
	if upd, err := e.Rooms.UpdateRoom(ctx, p.ID, r201.ID, rooms.RoomPatch{RoomTypeID: &std.ID}); err != nil || upd.RoomTypeID != std.ID {
		t.Fatalf("type change: %v %+v", err, upd)
	}

	// ...and a future confirmed assigned line blocks it again.
	e.Line(t, tn.ID, p.ID, std.ID, r201.ID, "2026-10-10", "2026-10-12", "CONFIRMED")
	_, err = e.Rooms.UpdateRoom(ctx, p.ID, r201.ID, rooms.RoomPatch{RoomTypeID: &dlx.ID})
	wantCode(t, err, "ROOM_IN_USE")

	// An inactive type cannot receive active rooms.
	if err := e.Exec(t, `UPDATE room_types SET is_active = false WHERE id = $1`, std.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.Rooms.CreateRoom(ctx, p.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: std.ID, RoomNumber: "401", BedTypeID: ptr(e.FirstBedType(t, p.ID)), IsActive: true}})
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Code != "ROOM_TYPE_INACTIVE" {
		t.Fatalf("fields: %v", fe)
	}

	if list, err := e.Rooms.ListRooms(ctx, p.ID, 0, rooms.RoomFilter{RoomTypeID: &dlx.ID}, 50); err != nil || len(list) != 1 || list[0].RoomNumber != "202" {
		t.Fatalf("filter by type: %v %+v", err, list)
	}
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'room'`); n != 4 {
		t.Fatalf("room audit entries: %d, want 2 created + 2 updated", n)
	}
}

func ptr[T any](v T) *T { return &v }

func TestBlockRules(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	dlx := e.RoomType(t, ctx, p.ID, "DLX")
	r201 := e.Room(t, ctx, p.ID, dlx.ID, "201")
	r202 := e.Room(t, ctx, p.ID, dlx.ID, "202")

	block := func(room int64, typ, from, to string) (rooms.RoomBlock, error) {
		return e.Rooms.CreateBlock(ctx, p.ID, rooms.CreateBlockInput{RoomID: room, BlockType: typ, StartDate: d(from), EndDate: d(to), Reason: "AC repair"})
	}

	b, err := block(r201.ID, "ooo", "2026-10-01", "2026-10-05")
	if err != nil || b.BlockType != "OOO" || b.Status != "ACTIVE" {
		t.Fatalf("create: %v %+v", err, b)
	}
	// Half-open ranges: back-to-back blocks are fine, overlaps are not, and other rooms are independent.
	if _, err := block(r201.ID, "OOS", "2026-10-05", "2026-10-06"); err != nil {
		t.Fatalf("adjacent block: %v", err)
	}
	_, err = block(r201.ID, "OOS", "2026-10-04", "2026-10-07")
	wantCode(t, err, "ROOM_BLOCK_CONFLICT")
	if _, err := block(r202.ID, "OOS", "2026-10-04", "2026-10-07"); err != nil {
		t.Fatalf("other room: %v", err)
	}

	// Validation: start >= business date, end > start, reason required, type.
	_, err = block(r201.ID, "OOO", "2026-09-29", "2026-10-01")
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 1 || fe[0].Field != "start_date" {
		t.Fatalf("past start: %v", fe)
	}
	_, err = block(r201.ID, "OOO", "2026-11-05", "2026-11-05")
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = e.Rooms.CreateBlock(ctx, p.ID, rooms.CreateBlockInput{RoomID: r201.ID, BlockType: "XXX", StartDate: d("2026-11-01"), EndDate: d("2026-11-02")})
	if fe := code(t, err, "VALIDATION_FAILED").Fields; len(fe) != 2 {
		t.Fatalf("type and reason: %v", fe)
	}
	_, err = block(999999, "OOO", "2026-11-01", "2026-11-02")
	wantCode(t, err, "ROOM_NOT_FOUND")

	// A stay or confirmed line in the range is a conflict, listed in the context.
	e.Stay(t, tn.ID, p.ID, dlx.ID, r202.ID, "2026-09-29", "2026-10-02")
	if err := e.Exec(t, `UPDATE room_blocks SET status = 'CANCELLED', cancelled_at = now() WHERE room_id = $1`, r202.ID); err != nil {
		t.Fatal(err)
	}
	_, err = block(r202.ID, "OOO", "2026-09-30", "2026-10-01")
	conflicts := code(t, err, "ROOM_BLOCK_CONFLICT").Context["conflicts"].([]rooms.Conflict)
	if len(conflicts) != 1 || conflicts[0].Type != rooms.ConflictStay || conflicts[0].To != d("2026-10-02") {
		t.Fatalf("conflicts: %+v", conflicts)
	}
	if _, err := block(r202.ID, "OOO", "2026-10-02", "2026-10-04"); err != nil { // after the stay departs
		t.Fatalf("block after departure: %v", err)
	}
	// An overstay keeps the room through tonight: departure 09-30 <= BD still holds [BD, BD+1).
	overstay := e.Room(t, ctx, p.ID, dlx.ID, "203")
	e.Stay(t, tn.ID, p.ID, dlx.ID, overstay.ID, "2026-09-28", "2026-09-30")
	_, err = block(overstay.ID, "OOS", "2026-09-30", "2026-10-01")
	wantCode(t, err, "ROOM_BLOCK_CONFLICT")

	r204 := e.Room(t, ctx, p.ID, dlx.ID, "204")
	e.Line(t, tn.ID, p.ID, dlx.ID, r204.ID, "2026-10-10", "2026-10-12", "CONFIRMED")
	_, err = block(r204.ID, "OOS", "2026-10-11", "2026-10-13")
	conflicts = code(t, err, "ROOM_BLOCK_CONFLICT").Context["conflicts"].([]rooms.Conflict)
	if len(conflicts) != 1 || conflicts[0].Type != rooms.ConflictReservation {
		t.Fatalf("conflicts: %+v", conflicts)
	}
	if _, err := block(r204.ID, "OOS", "2026-10-12", "2026-10-13"); err != nil { // departure day is free
		t.Fatalf("block on departure day: %v", err)
	}
	// Unassigned or draft lines do not conflict with a room block.
	e.Line(t, tn.ID, p.ID, dlx.ID, 0, "2026-11-01", "2026-11-03", "CONFIRMED")
	if _, err := block(r204.ID, "OOS", "2026-11-01", "2026-11-03"); err != nil {
		t.Fatalf("type-level booking: %v", err)
	}

	// Update: extend into a conflict, shorten (early release), bad dates.
	_, err = e.Rooms.UpdateBlock(ctx, p.ID, b.ID, rooms.BlockPatch{EndDate: ptr(d("2026-10-06"))})
	wantCode(t, err, "ROOM_BLOCK_CONFLICT") // the adjacent OOS block holds 10-05
	if upd, err := e.Rooms.UpdateBlock(ctx, p.ID, b.ID, rooms.BlockPatch{EndDate: ptr(d("2026-10-03")), Reason: ptr("Fixed early")}); err != nil || upd.EndDate != d("2026-10-03") || upd.Reason != "Fixed early" {
		t.Fatalf("shorten: %v %+v", err, upd)
	}
	if _, err := e.Rooms.UpdateBlock(ctx, p.ID, b.ID, rooms.BlockPatch{EndDate: ptr(d("2026-10-05"))}); err != nil {
		t.Fatalf("extend back into free nights: %v", err)
	}
	_, err = e.Rooms.UpdateBlock(ctx, p.ID, b.ID, rooms.BlockPatch{EndDate: ptr(d("2026-09-29"))})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = e.Rooms.UpdateBlock(ctx, p.ID, b.ID, rooms.BlockPatch{Reason: ptr("  ")})
	wantCode(t, err, "VALIDATION_FAILED")

	// Cancel: reason required, releases the dates, cannot be repeated or edited afterwards.
	_, err = e.Rooms.CancelBlock(ctx, p.ID, b.ID, "")
	wantCode(t, err, "VALIDATION_FAILED")
	cancelled, err := e.Rooms.CancelBlock(ctx, p.ID, b.ID, "Repair cancelled")
	if err != nil || cancelled.Status != "CANCELLED" || cancelled.CancelledAt == nil {
		t.Fatalf("cancel: %v %+v", err, cancelled)
	}
	_, err = e.Rooms.CancelBlock(ctx, p.ID, b.ID, "again")
	wantCode(t, err, "ROOM_BLOCK_NOT_ACTIVE")
	_, err = e.Rooms.UpdateBlock(ctx, p.ID, b.ID, rooms.BlockPatch{Reason: ptr("x")})
	wantCode(t, err, "ROOM_BLOCK_NOT_ACTIVE")
	if _, err := block(r201.ID, "OOO", "2026-10-01", "2026-10-05"); err != nil {
		t.Fatalf("re-block released dates: %v", err)
	}

	// Listing and filters.
	active := "ACTIVE"
	list, err := e.Rooms.ListBlocks(ctx, p.ID, nil, rooms.BlockFilter{RoomID: &r201.ID, Status: &active}, 50)
	if err != nil || len(list) != 2 {
		t.Fatalf("active blocks of 201: %v %d", err, len(list))
	}
	from, to := d("2026-10-05"), d("2026-10-06")
	if list, err := e.Rooms.ListBlocks(ctx, p.ID, nil, rooms.BlockFilter{RoomID: &r201.ID, From: &from, To: &to}, 50); err != nil || len(list) != 1 || list[0].BlockType != "OOS" {
		t.Fatalf("overlap filter: %v %+v", err, list)
	}
	if n := e.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'room_block' AND action = 'room_block.cancelled'`); n != 1 {
		t.Fatalf("cancel audit entries: %d", n)
	}
}

// The EXCLUDE constraint is the backstop even when the application is bypassed.
func TestBlockOverlapExcludeConstraint(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	dlx := e.RoomType(t, ctx, p.ID, "DLX")
	r := e.Room(t, ctx, p.ID, dlx.ID, "201")
	ins := `INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason) VALUES ($1, $2, $3, 'OOO', $4::date, $5::date, 'x')`
	if err := e.Exec(t, ins, tn.ID, p.ID, r.ID, "2026-10-01", "2026-10-05"); err != nil {
		t.Fatal(err)
	}
	if err := e.Exec(t, ins, tn.ID, p.ID, r.ID, "2026-10-04", "2026-10-06"); err == nil {
		t.Fatal("overlapping active blocks must be rejected by the database")
	}
	if err := e.Exec(t, ins, tn.ID, p.ID, r.ID, "2026-10-05", "2026-10-06"); err != nil {
		t.Fatalf("half-open ranges must not collide: %v", err)
	}
}

// Two clerks block the same room for overlapping dates at the same time: exactly one wins.
func TestConcurrentBlocksOfSameRoom(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	dlx := e.RoomType(t, ctx, p.ID, "DLX")
	r := e.Room(t, ctx, p.ID, dlx.ID, "201")

	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = e.Rooms.CreateBlock(ctx, p.ID, rooms.CreateBlockInput{
				RoomID: r.ID, BlockType: "OOO", StartDate: d("2026-10-01"), EndDate: d("2026-10-05"), Reason: "race",
			})
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case apperr.IsCode(err, "ROOM_BLOCK_CONFLICT"):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d blocks succeeded, want exactly 1", ok)
	}
	if c := e.Count(t, `SELECT count(*) FROM room_blocks WHERE status = 'ACTIVE'`); c != 1 {
		t.Fatalf("active blocks: %d", c)
	}
}

// A block and a type change of the same room take the same locks, so they serialise.
func TestBlockVersusRoomDeactivationRace(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	dlx := e.RoomType(t, ctx, p.ID, "DLX")
	r := e.Room(t, ctx, p.ID, dlx.ID, "201")

	var wg sync.WaitGroup
	start := make(chan struct{})
	var blockErr, deactErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, blockErr = e.Rooms.CreateBlock(ctx, p.ID, rooms.CreateBlockInput{RoomID: r.ID, BlockType: "OOS", StartDate: d("2026-10-01"), EndDate: d("2026-10-02"), Reason: "race"})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, deactErr = e.Rooms.UpdateRoom(ctx, p.ID, r.ID, rooms.RoomPatch{IsActive: ptr(false)})
	}()
	close(start)
	wg.Wait()
	if deactErr != nil {
		t.Fatalf("deactivate: %v", deactErr) // an unassigned room can always be deactivated
	}
	// Whichever order they ran in, there is never an active block on an inactive room from the block path
	// unless the block committed first; in that case the block stays valid history.
	if blockErr != nil {
		wantCode(t, blockErr, "ROOM_INACTIVE")
	}
}

func TestPermissionsAndIsolation(t *testing.T) {
	e := roomstest.Setup(t)
	abc := e.Tenant(t, "ABC")
	xyz := e.Tenant(t, "XYZ")
	bali := e.Property(t, abc.ID, "BALI")
	jkt := e.Property(t, abc.ID, "JKT")
	foreign := e.Property(t, xyz.ID, "SG")
	admin := roomstest.Admin(abc.ID)
	dlx := e.RoomType(t, admin, bali.ID, "DLX")
	room := e.Room(t, admin, bali.ID, dlx.ID, "201")
	blk, err := e.Rooms.CreateBlock(admin, bali.ID, rooms.CreateBlockInput{RoomID: room.ID, BlockType: "OOO", StartDate: d("2026-10-01"), EndDate: d("2026-10-02"), Reason: "x"})
	if err != nil {
		t.Fatal(err)
	}

	reader := e.User(t, abc.ID, bali.ID) // a grant with no permissions
	in := rooms.RoomTypeInput{Code: "STD", Name: "Std", MaxAdult: 1, MaxChild: 0, MaxOccupancy: 1, BaseOccupancy: 1, IsActive: true}
	if _, err := e.Rooms.ListRoomTypes(reader, bali.ID, 0, nil, 10); err != nil {
		t.Fatalf("read needs property access only: %v", err)
	}
	_, err = e.Rooms.CreateRoomType(reader, bali.ID, in)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = e.Rooms.UpdateRoomType(reader, bali.ID, dlx.ID, rooms.RoomTypePatch{Name: ptr("x")})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = e.Rooms.CreateRoom(reader, bali.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: dlx.ID, RoomNumber: "9", IsActive: true}})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = e.Rooms.CreateBlock(reader, bali.ID, rooms.CreateBlockInput{RoomID: room.ID, BlockType: "OOO", StartDate: d("2026-11-01"), EndDate: d("2026-11-02"), Reason: "x"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = e.Rooms.CancelBlock(reader, bali.ID, blk.ID, "x")
	wantCode(t, err, "PERMISSION_DENIED")

	// room.manage does not imply room_block.manage.
	manager := e.User(t, abc.ID, bali.ID, auth.PermRoomManage)
	if _, err := e.Rooms.CreateRoomType(manager, bali.ID, in); err != nil {
		t.Fatalf("room.manage: %v", err)
	}
	_, err = e.Rooms.CancelBlock(manager, bali.ID, blk.ID, "x")
	wantCode(t, err, "PERMISSION_DENIED")
	blocker := e.User(t, abc.ID, bali.ID, auth.PermRoomBlockManage)
	if _, err := e.Rooms.CancelBlock(blocker, bali.ID, blk.ID, "done"); err != nil {
		t.Fatalf("room_block.manage: %v", err)
	}

	// A property the user has no grant for, and another tenant's property, are both 404.
	for _, pid := range []int64{jkt.ID, foreign.ID} {
		_, err = e.Rooms.ListRoomTypes(manager, pid, 0, nil, 10)
		wantCode(t, err, "PROPERTY_NOT_FOUND")
		_, err = e.Rooms.CreateRoomType(manager, pid, in)
		wantCode(t, err, "PROPERTY_NOT_FOUND")
	}
	_, err = e.Rooms.ListRooms(admin, foreign.ID, 0, rooms.RoomFilter{}, 10) // a tenant admin still cannot cross tenants
	wantCode(t, err, "PROPERTY_NOT_FOUND")

	// Ids from another property or tenant never resolve, even through an accessible property.
	other := e.RoomType(t, admin, jkt.ID, "DLX")
	otherRoom := e.Room(t, admin, jkt.ID, other.ID, "301")
	_, err = e.Rooms.GetRoom(admin, bali.ID, otherRoom.ID)
	wantCode(t, err, "ROOM_NOT_FOUND")
	_, err = e.Rooms.GetRoomType(admin, bali.ID, other.ID)
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")
	_, err = e.Rooms.CreateBlock(admin, bali.ID, rooms.CreateBlockInput{RoomID: otherRoom.ID, BlockType: "OOO", StartDate: d("2026-11-01"), EndDate: d("2026-11-02"), Reason: "x"})
	wantCode(t, err, "ROOM_NOT_FOUND")
	_, err = e.Rooms.UpdateRoom(admin, bali.ID, otherRoom.ID, rooms.RoomPatch{Floor: ptr("9")})
	wantCode(t, err, "ROOM_NOT_FOUND")
	_, err = e.Rooms.CreateRoom(admin, bali.ID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: other.ID, RoomNumber: "9", BedTypeID: ptr(e.FirstBedType(t, bali.ID)), IsActive: true}})
	wantCode(t, err, "ROOM_TYPE_NOT_FOUND")

	_, err = e.Rooms.ListRoomTypes(context.Background(), bali.ID, 0, nil, 10)
	wantCode(t, err, "UNAUTHENTICATED")
}
