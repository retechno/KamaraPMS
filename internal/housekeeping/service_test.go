package housekeeping_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

type fixture struct {
	*roomstest.Env
	tenantID, propertyID, typeID int64
	admin                        context.Context
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin := roomstest.Admin(tn.ID)
	return fixture{Env: e, tenantID: tn.ID, propertyID: p.ID, typeID: e.RoomType(t, admin, p.ID, "DLX").ID, admin: admin}
}

func (f fixture) status(t *testing.T, roomID int64) housekeeping.Status {
	t.Helper()
	var s housekeeping.Status
	if err := f.Pool.QueryRow(context.Background(), `SELECT status FROM room_housekeeping WHERE room_id = $1`, roomID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f fixture) set(t *testing.T, ctx context.Context, roomID int64, to housekeeping.Status) error {
	t.Helper()
	_, err := f.HK.SetStatus(ctx, f.propertyID, roomID, to, "")
	return err
}

func TestStatusChangesAreLoggedAndAudited(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201")

	for _, to := range []housekeeping.Status{housekeeping.Cleaning, housekeeping.Clean, housekeeping.Inspected, housekeeping.Dirty} {
		if err := f.set(t, f.admin, room.ID, to); err != nil {
			t.Fatalf("→%s: %v", to, err)
		}
	}
	if _, err := f.HK.SetStatus(f.admin, f.propertyID, room.ID, housekeeping.Clean, "deep clean done"); err != nil {
		t.Fatal(err)
	}

	logs, err := f.HK.Logs(f.admin, f.propertyID, room.ID, nil, 50)
	if err != nil || len(logs) != 5 {
		t.Fatalf("logs: %v %d, want one per change", err, len(logs))
	}
	first, last := logs[4], logs[0] // newest first
	if first.FromStatus != housekeeping.Dirty || first.ToStatus != housekeeping.Cleaning || first.Source != housekeeping.SourceManual ||
		first.BusinessDate != roomstest.BD || first.ChangedBy != nil && *first.ChangedBy != 0 {
		t.Fatalf("first log: %+v", first)
	}
	if last.FromStatus != housekeeping.Dirty || last.ToStatus != housekeeping.Clean || last.Notes != "deep clean done" {
		t.Fatalf("last log: %+v", last)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.changed' AND entity_id = $1`, room.ID); n != 5 {
		t.Fatalf("audit entries: %d", n)
	}
	if got := f.status(t, room.ID); got != housekeeping.Clean {
		t.Fatalf("current status %s", got)
	}

	// Paging walks the history newest first without gaps.
	page1, _ := f.HK.Logs(f.admin, f.propertyID, room.ID, nil, 2)
	page2, _ := f.HK.Logs(f.admin, f.propertyID, room.ID, &page1[1].ID, 10)
	if len(page1) != 2 || len(page2) != 3 || page2[0].ID >= page1[1].ID {
		t.Fatalf("paging: %d + %d", len(page1), len(page2))
	}

	// The log is append-only, even for someone with direct SQL access.
	if err := f.Exec(t, `UPDATE housekeeping_logs SET notes = 'edited' WHERE room_id = $1`, room.ID); err == nil {
		t.Fatal("housekeeping logs must be immutable")
	}
}

func TestInvalidTransitionsAreRejectedWithoutSideEffects(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Clean)

	for _, to := range []housekeeping.Status{housekeeping.Cleaning, housekeeping.Clean} { // CLEAN→CLEANING, CLEAN→CLEAN
		err := f.set(t, f.admin, room.ID, to)
		c := code(t, err, "INVALID_HK_TRANSITION")
		if c.Context["from"] != "CLEAN" || c.Context["to"] != string(to) {
			t.Fatalf("context: %v", c.Context)
		}
	}
	wantCode(t, f.set(t, f.admin, room.ID, "BOGUS"), "VALIDATION_FAILED")
	_, err := f.HK.SetStatus(f.admin, f.propertyID, room.ID, housekeeping.Dirty, string(make([]byte, 501)))
	wantCode(t, err, "VALIDATION_FAILED")
	wantCode(t, f.set(t, f.admin, 987654, housekeeping.Dirty), "ROOM_NOT_FOUND")

	if got := f.status(t, room.ID); got != housekeeping.Clean {
		t.Fatalf("status changed to %s", got)
	}
	if n := f.Count(t, `SELECT count(*) FROM housekeeping_logs`); n != 0 {
		t.Fatalf("rejected changes wrote %d logs", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.changed'`); n != 0 {
		t.Fatalf("rejected changes wrote %d audit entries", n)
	}
}

func TestInspectedNeedsInspectPermission(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Clean)
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	supervisor := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate, auth.PermHousekeepingInspect)
	reader := f.User(t, f.tenantID, f.propertyID)

	wantCode(t, f.set(t, maid, room.ID, housekeeping.Inspected), "PERMISSION_DENIED")
	wantCode(t, f.set(t, reader, room.ID, housekeeping.Dirty), "PERMISSION_DENIED")
	if n := f.Count(t, `SELECT count(*) FROM housekeeping_logs`); n != 0 {
		t.Fatalf("denied changes wrote %d logs", n)
	}
	if err := f.set(t, maid, room.ID, housekeeping.Dirty); err != nil { // the maid may still do everything else
		t.Fatal(err)
	}
	if err := f.set(t, supervisor, room.ID, housekeeping.Clean); err != nil {
		t.Fatal(err)
	}
	if err := f.set(t, supervisor, room.ID, housekeeping.Inspected); err != nil {
		t.Fatalf("supervisor: %v", err)
	}
	// Reading the board needs property access only.
	if _, err := f.HK.Board(reader, f.propertyID, housekeeping.BoardFilter{}); err != nil {
		t.Fatalf("board: %v", err)
	}
}

func TestPropertyAndTenantIsolation(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201")
	jkt := f.Property(t, f.tenantID, "JKT")
	xyz := f.Tenant(t, "XYZ")
	sg := f.Property(t, xyz.ID, "SG")
	jktType := f.RoomType(t, f.admin, jkt.ID, "DLX")
	jktRoom := f.Room(t, f.admin, jkt.ID, jktType.ID, "301")
	user := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)

	_, err := f.HK.Board(user, jkt.ID, housekeeping.BoardFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.HK.SetStatus(user, jkt.ID, jktRoom.ID, housekeeping.Cleaning, "")
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.HK.Board(f.admin, sg.ID, housekeeping.BoardFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	// A room of another property, addressed through an accessible one, does not exist.
	wantCode(t, f.set(t, f.admin, jktRoom.ID, housekeeping.Cleaning), "ROOM_NOT_FOUND")
	_, err = f.HK.Logs(f.admin, f.propertyID, jktRoom.ID, nil, 10)
	wantCode(t, err, "ROOM_NOT_FOUND")
	if got := f.status(t, jktRoom.ID); got != housekeeping.Dirty {
		t.Fatalf("other property's room changed: %s", got)
	}
	_ = room
}

// Two supervisors press "start cleaning" on the same room at once: one change, one rejection.
func TestConcurrentStatusChanges(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201")

	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = f.set(t, f.admin, room.ID, housekeeping.Cleaning)
		}()
	}
	close(start)
	wg.Wait()
	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case apperr.IsCode(err, "INVALID_HK_TRANSITION"):
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 {
		t.Fatalf("%d changes succeeded, want exactly 1", ok)
	}
	if c := f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE room_id = $1`, room.ID); c != 1 {
		t.Fatalf("logs: %d, want 1", c)
	}
}

func TestMarkDirtyForSystemSources(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Inspected)

	// It is a building block for other use cases and needs their transaction.
	err := f.HK.MarkDirty(context.Background(), f.tenantID, f.propertyID, room.ID, housekeeping.SourceCheckOut, "", nil)
	if err == nil {
		t.Fatal("MarkDirty outside a transaction must fail")
	}
	mark := func(src housekeeping.Source) error {
		return f.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
			return f.HK.MarkDirty(ctx, f.tenantID, f.propertyID, room.ID, src, "guest left", nil)
		})
	}
	if err := mark(housekeeping.SourceManual); err == nil {
		t.Fatal("MANUAL changes go through SetStatus")
	}
	if err := mark(housekeeping.SourceCheckOut); err != nil {
		t.Fatal(err)
	}
	if err := mark(housekeeping.SourceNightAudit); err != nil { // already DIRTY: nothing to do
		t.Fatal(err)
	}
	logs, err := f.HK.Logs(f.admin, f.propertyID, room.ID, nil, 10)
	if err != nil || len(logs) != 1 || logs[0].Source != housekeeping.SourceCheckOut || logs[0].FromStatus != housekeeping.Inspected {
		t.Fatalf("logs: %v %+v", err, logs)
	}
	wantCode(t, f.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
		return f.HK.MarkDirty(ctx, f.tenantID, f.propertyID, 424242, housekeeping.SourceCheckOut, "", nil)
	}), "ROOM_NOT_FOUND")

	// A failing use case rolls the housekeeping change back with everything else.
	if err := f.set(t, f.admin, room.ID, housekeeping.Clean); err != nil {
		t.Fatal(err)
	}
	boom := context.Canceled
	err = f.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := f.HK.MarkDirty(ctx, f.tenantID, f.propertyID, room.ID, housekeeping.SourceRoomMove, "", nil); err != nil {
			return err
		}
		return boom
	})
	if err == nil || f.status(t, room.ID) != housekeeping.Clean {
		t.Fatalf("rollback: err=%v status=%s", err, f.status(t, room.ID))
	}
}

func TestBoardDerivesOccupancyAndShowsBlocks(t *testing.T) {
	f := newFixture(t)
	occupied := f.Room(t, f.admin, f.propertyID, f.typeID, "101", housekeeping.Clean)
	reserved := f.Room(t, f.admin, f.propertyID, f.typeID, "102", housekeeping.Inspected)
	vacant := f.Room(t, f.admin, f.propertyID, f.typeID, "201")
	blocked := f.Room(t, f.admin, f.propertyID, f.typeID, "202", housekeeping.Clean)
	inactive := f.Room(t, f.admin, f.propertyID, f.typeID, "203")
	if _, err := f.Rooms.UpdateRoom(f.admin, f.propertyID, inactive.ID, rooms.RoomPatch{IsActive: new(bool)}); err != nil {
		t.Fatal(err)
	}

	f.Stay(t, f.tenantID, f.propertyID, f.typeID, occupied.ID, "2026-09-29", "2026-10-02")
	f.Line(t, f.tenantID, f.propertyID, f.typeID, reserved.ID, "2026-09-30", "2026-10-01", "CONFIRMED")
	f.Line(t, f.tenantID, f.propertyID, f.typeID, vacant.ID, "2026-10-05", "2026-10-06", "CONFIRMED") // future: still VACANT today
	if _, err := f.Rooms.CreateBlock(f.admin, f.propertyID, rooms.CreateBlockInput{
		RoomID: blocked.ID, BlockType: "OOO", StartDate: roomstest.BD, EndDate: roomstest.BD.AddDays(3), Reason: "flood",
	}); err != nil {
		t.Fatal(err)
	}

	board, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]housekeeping.BoardRoom{}
	for _, r := range board {
		got[r.RoomNumber] = r
	}
	if len(board) != 4 || got["203"].RoomID != 0 {
		t.Fatalf("inactive rooms are not on the board: %d rooms", len(board))
	}
	want := map[string]housekeeping.Occupancy{"101": housekeeping.Occupied, "102": housekeeping.Reserved, "201": housekeeping.Vacant, "202": housekeeping.Vacant}
	for num, occ := range want {
		if got[num].Occupancy != occ {
			t.Errorf("room %s occupancy %s, want %s", num, got[num].Occupancy, occ)
		}
	}
	if b := got["202"].Block; b == nil || b.Type != "OOO" || b.EndDate != roomstest.BD.AddDays(3) || got["201"].Block != nil {
		t.Fatalf("blocks: %+v / %+v", got["202"].Block, got["201"].Block)
	}
	if next := got["201"].AllowedNextState; len(next) != 2 { // DIRTY → CLEANING or CLEAN
		t.Fatalf("allowed next: %v", next)
	}

	// Occupancy is derived: checking the guest out (closing the segment) makes the room vacant with no other write.
	if err := f.Exec(t, `UPDATE stay_rooms SET check_out_at = now(), end_business_date = $1::date WHERE room_id = $2`, roomstest.BD.String(), occupied.ID); err != nil {
		t.Fatal(err)
	}
	board, _ = f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{})
	for _, r := range board {
		if r.RoomNumber == "101" && r.Occupancy != housekeeping.Vacant {
			t.Fatalf("after check-out: %s", r.Occupancy)
		}
	}

	// Filters.
	dirty := housekeeping.Dirty
	if b, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Status: &dirty}); err != nil || len(b) != 1 || b[0].RoomNumber != "201" {
		t.Fatalf("status filter: %v %+v", err, b)
	}
	floor := "1"
	if b, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Floor: &floor}); err != nil || len(b) != 2 {
		t.Fatalf("floor filter: %v %d", err, len(b))
	}
	bogus := housekeeping.Status("BOGUS")
	_, err = f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Status: &bogus})
	wantCode(t, err, "VALIDATION_FAILED")
}
