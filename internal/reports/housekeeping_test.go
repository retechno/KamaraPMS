package reports_test

import (
	"context"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/reports"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

type hkfx struct {
	*roomstest.Env
	tenantID, propID, typeID int64
	admin                    context.Context
	rooms                    []rooms.Room
}

func hkSetup(t *testing.T, n int) *hkfx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin := roomstest.Admin(tn.ID)
	typ := e.RoomType(t, admin, p.ID, "DLX")
	f := &hkfx{Env: e, tenantID: tn.ID, propID: p.ID, typeID: typ.ID, admin: admin}
	for _, num := range []string{"101", "102", "103", "104", "105", "106"}[:n] {
		f.rooms = append(f.rooms, e.Room(t, admin, p.ID, typ.ID, num, housekeeping.Dirty))
	}
	return f
}

// backdate moves the "in this status since" time of a room, which the update trigger would otherwise reset.
func (f *hkfx) backdate(t *testing.T, room int64, hours int) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.Pool.Exec(ctx, `ALTER TABLE room_housekeeping DISABLE TRIGGER room_housekeeping_set_updated_at`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := f.Pool.Exec(ctx, `ALTER TABLE room_housekeeping ENABLE TRIGGER room_housekeeping_set_updated_at`); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := f.Pool.Exec(ctx, `UPDATE room_housekeeping SET updated_at = now() - make_interval(hours => $2) WHERE room_id = $1`, room, hours); err != nil {
		t.Fatal(err)
	}
}

func person(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	p, err := auth.Require(ctx)
	if err != nil || p.ActorID() == nil {
		t.Fatalf("no user: %v", err)
	}
	return *p.ActorID()
}

func TestHousekeepingProductivity(t *testing.T) {
	f := hkSetup(t, 4)
	maid1 := f.User(t, f.tenantID, f.propID, auth.PermHousekeepingUpdate)
	maid2 := f.User(t, f.tenantID, f.propID, auth.PermHousekeepingUpdate)
	boss := f.User(t, f.tenantID, f.propID, auth.PermHousekeepingUpdate, auth.PermHousekeepingInspect)
	set := func(ctx context.Context, room int64, to housekeeping.Status) {
		t.Helper()
		if _, err := f.HK.SetStatus(ctx, f.propID, room, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	// maid1 cleans 101 and 102 through CLEANING, maid2 takes 103 straight to CLEAN, the supervisor inspects 101
	for _, r := range f.rooms[:2] {
		set(maid1, r.ID, housekeeping.Cleaning)
		set(maid1, r.ID, housekeeping.Clean)
	}
	set(maid2, f.rooms[2].ID, housekeeping.Clean)
	set(boss, f.rooms[0].ID, housekeeping.Inspected)

	// the cleaning list: maid1 finishes the task of 104 after 30 minutes
	if _, err := f.HK.GenerateTasks(f.admin, f.propID); err != nil {
		t.Fatal(err)
	}
	var taskID int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM housekeeping_tasks WHERE room_id = $1`, f.rooms[3].ID).Scan(&taskID))
	if _, err := f.HK.StartTask(maid1, f.propID, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.HK.CompleteTask(maid1, f.propID, taskID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Pool.Exec(context.Background(), `UPDATE housekeeping_tasks SET started_at = completed_at - interval '30 minutes' WHERE id = $1`, taskID); err != nil {
		t.Fatal(err)
	}

	rep, err := f.Reports.HousekeepingProductivity(f.admin, f.propID, roomstest.BD, roomstest.BD)
	must(t, err)
	by := map[int64]reports.HousekeepingPerson{}
	for _, p := range rep.People {
		by[p.UserID] = p
	}
	m1, m2, bs := by[person(t, maid1)], by[person(t, maid2)], by[person(t, boss)]
	if m1.Cleaned != 3 || m1.TasksDone != 1 || m1.AvgMinutes != "30.0" || m1.Days != 1 { // 101, 102 and the task's room 104
		t.Fatalf("maid1: %+v", m1)
	}
	if m2.Cleaned != 1 || m2.TasksDone != 0 || m2.AvgMinutes != "" {
		t.Fatalf("maid2: %+v", m2)
	}
	if bs.Inspected != 1 || bs.Cleaned != 0 {
		t.Fatalf("supervisor: %+v", bs)
	}
	if len(rep.Lines) != 3 || len(rep.People) != 3 || rep.Lines[0].BusinessDate != roomstest.BD || rep.People[0].User > rep.People[1].User {
		t.Fatalf("lines %+v people %+v", rep.Lines, rep.People)
	}
	head, rows := rep.CSV()
	if len(head) != 7 || len(rows) != 3 || head[1] != "user" {
		t.Fatalf("csv: %v %v", head, rows)
	}
	// a range without that day is empty; a bad range or a role without report.view is refused
	empty, err := f.Reports.HousekeepingProductivity(f.admin, f.propID, roomstest.BD.AddDays(1), roomstest.BD.AddDays(5))
	if err != nil || len(empty.Lines) != 0 || len(empty.People) != 0 {
		t.Fatalf("empty: %+v %v", empty, err)
	}
	_, err = f.Reports.HousekeepingProductivity(f.admin, f.propID, roomstest.BD.AddDays(2), roomstest.BD)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Reports.HousekeepingProductivity(maid1, f.propID, roomstest.BD, roomstest.BD)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestHousekeepingDirtyRooms(t *testing.T) {
	f := hkSetup(t, 4)
	f.Stay(t, f.tenantID, f.propID, f.typeID, f.rooms[0].ID, "2026-09-28", "2026-10-02")                // 101 is occupied
	f.Line(t, f.tenantID, f.propID, f.typeID, f.rooms[1].ID, "2026-09-30", "2026-10-01", "CONFIRMED")   // 102 is reserved
	if _, err := f.HK.SetStatus(f.admin, f.propID, f.rooms[3].ID, housekeeping.Clean, ""); err != nil { // 104 is clean: not listed
		t.Fatal(err)
	}
	if _, err := f.HK.SetFlags(f.admin, f.propID, f.rooms[0].ID, housekeeping.FlagsInput{Priority: "HIGH", DND: true}); err != nil {
		t.Fatal(err)
	}
	f.backdate(t, f.rooms[0].ID, 30)
	f.backdate(t, f.rooms[1].ID, 5)
	f.backdate(t, f.rooms[2].ID, 1)

	rep, err := f.Reports.HousekeepingDirty(f.admin, f.propID, 0)
	must(t, err)
	if len(rep.Rows) != 3 || rep.Rows[0].RoomNumber != "101" || rep.Rows[0].Hours != 30 || rep.Rows[0].Occupancy != "OCCUPIED" || !rep.Rows[0].DND || rep.Rows[0].Priority != "HIGH" {
		t.Fatalf("all: %+v", rep.Rows)
	}
	if rep.Rows[1].RoomNumber != "102" || rep.Rows[1].Occupancy != "RESERVED" || rep.Rows[1].Hours != 5 || rep.Rows[2].Occupancy != "VACANT" {
		t.Fatalf("order and occupancy: %+v", rep.Rows)
	}
	old, err := f.Reports.HousekeepingDirty(f.admin, f.propID, 4)
	if err != nil || len(old.Rows) != 2 || old.MinHours != 4 {
		t.Fatalf("older than 4 hours: %+v %v", old, err)
	}
	// a room being cleaned still counts, and one that is out of order says so
	if _, err := f.HK.SetStatus(f.admin, f.propID, f.rooms[2].ID, housekeeping.Cleaning, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Rooms.CreateBlock(f.admin, f.propID, rooms.CreateBlockInput{RoomID: f.rooms[2].ID, BlockType: "OOO", StartDate: roomstest.BD, EndDate: roomstest.BD.AddDays(2), Reason: "leak"}); err != nil {
		t.Fatal(err)
	}
	again, err := f.Reports.HousekeepingDirty(f.admin, f.propID, 0)
	if err != nil || len(again.Rows) != 3 {
		t.Fatalf("again: %+v %v", again, err)
	}
	found := false
	for _, r := range again.Rows {
		if r.RoomNumber == "103" {
			found = r.Status == "CLEANING" && r.Block == "OOO"
		}
	}
	if !found {
		t.Fatalf("103 is being cleaned and blocked: %+v", again.Rows)
	}
	head, rows := again.CSV()
	if len(head) != 10 || len(rows) != 3 {
		t.Fatalf("csv: %v %v", head, rows)
	}
	_, err = f.Reports.HousekeepingDirty(f.admin, f.propID, -1)
	wantCode(t, err, "VALIDATION_FAILED")
	maid := f.User(t, f.tenantID, f.propID, auth.PermHousekeepingUpdate)
	_, err = f.Reports.HousekeepingDirty(maid, f.propID, 0)
	wantCode(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	_, err = f.Reports.HousekeepingDirty(roomstest.Admin(other.ID), f.propID, 0)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestMaintenanceReport(t *testing.T) {
	f := hkSetup(t, 2)
	open := func(cat, prio string, room *int64, place string) maintenance.Request {
		r, err := f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{RoomID: room, Location: place, Category: cat, Description: "x", Priority: prio})
		must(t, err)
		return r
	}
	a := open("PLUMBING", "HIGH", &f.rooms[0].ID, "")
	b := open("PLUMBING", "NORMAL", &f.rooms[1].ID, "")
	open("PLUMBING", "URGENT", nil, "Pool")
	c := open("AC", "LOW", &f.rooms[0].ID, "")
	open("AC", "NORMAL", nil, "Lobby")
	for _, id := range []int64{a.ID, c.ID} {
		if _, err := f.Maintenance.Resolve(f.admin, f.propID, id, maintenance.CloseInput{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.Maintenance.Cancel(f.admin, f.propID, b.ID, maintenance.CloseInput{Note: "duplicate"}); err != nil {
		t.Fatal(err)
	}
	// resolved after 4 and 2 hours
	for id, h := range map[int64]int{a.ID: 4, c.ID: 2} {
		if _, err := f.Pool.Exec(context.Background(), `UPDATE maintenance_requests SET reported_at = closed_at - make_interval(hours => $2) WHERE id = $1`, id, h); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := f.Reports.Maintenance(f.admin, f.propID, roomstest.BD, roomstest.BD)
	must(t, err)
	if len(rep.Lines) != 2 || rep.Lines[0].Category != "AC" || rep.Lines[1].Category != "PLUMBING" {
		t.Fatalf("lines: %+v", rep.Lines)
	}
	ac, pl := rep.Lines[0], rep.Lines[1]
	if ac.Reported != 2 || ac.Resolved != 1 || ac.StillOpen != 1 || ac.AvgHours != "2.0" {
		t.Fatalf("ac: %+v", ac)
	}
	if pl.Reported != 3 || pl.Resolved != 1 || pl.Cancelled != 1 || pl.StillOpen != 1 || pl.AvgHours != "4.0" {
		t.Fatalf("plumbing: %+v", pl)
	}
	if rep.Totals.Reported != 5 || rep.Totals.Resolved != 2 || rep.Totals.AvgHours != "3.0" || rep.Totals.StillOpen != 2 {
		t.Fatalf("totals: %+v", rep.Totals)
	}
	if rep.Backlog.OpenNow != 2 || rep.Backlog.HighPriority != 1 { // the urgent pool request; the lobby one is NORMAL
		t.Fatalf("backlog: %+v", rep.Backlog)
	}
	empty, err := f.Reports.Maintenance(f.admin, f.propID, roomstest.BD.AddDays(1), roomstest.BD.AddDays(3))
	if err != nil || len(empty.Lines) != 0 || empty.Backlog.OpenNow != 2 || empty.Totals.AvgHours != "" {
		t.Fatalf("another range keeps the backlog of today: %+v %v", empty, err)
	}
	head, rows := rep.CSV()
	if len(head) != 6 || len(rows) != 2 {
		t.Fatalf("csv: %v %v", head, rows)
	}
	tech := f.User(t, f.tenantID, f.propID, auth.PermMaintenanceManage)
	_, err = f.Reports.Maintenance(tech, f.propID, roomstest.BD, roomstest.BD)
	wantCode(t, err, "PERMISSION_DENIED")
}
