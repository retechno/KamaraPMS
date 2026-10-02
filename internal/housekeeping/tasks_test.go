package housekeeping_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func (f fixture) userID(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	p, err := auth.Require(ctx)
	if err != nil || p.ActorID() == nil {
		t.Fatalf("no user in context: %v", err)
	}
	return *p.ActorID()
}

// taskOf returns the task of a room and type on the list of the current business date.
func (f fixture) taskOf(t *testing.T, room int64, typ string) housekeeping.Task {
	t.Helper()
	list, err := f.HK.Tasks(f.admin, f.propertyID, housekeeping.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range list.Data {
		if task.RoomID == room && task.TaskType == typ {
			return task
		}
	}
	t.Fatalf("no %s task for room %d in %+v", typ, room, list.Data)
	return housekeeping.Task{}
}

// listing sets up the rooms of a morning: 101 a guest leaves today, 102 stays over, 103 dirty and a guest arrives,
// 104 dirty and empty, 105 clean, 106 dirty but out of order.
func (f fixture) listing(t *testing.T) map[string]rooms.Room {
	t.Helper()
	out := map[string]rooms.Room{}
	for _, n := range []string{"101", "102", "103", "104", "105", "106"} {
		st := housekeeping.Dirty
		if n == "105" {
			st = housekeeping.Clean
		}
		out[n] = f.Room(t, f.admin, f.propertyID, f.typeID, n, st)
	}
	f.Stay(t, f.tenantID, f.propertyID, f.typeID, out["101"].ID, "2026-09-28", "2026-09-30")
	f.Stay(t, f.tenantID, f.propertyID, f.typeID, out["102"].ID, "2026-09-28", "2026-10-03")
	f.Line(t, f.tenantID, f.propertyID, f.typeID, out["103"].ID, "2026-09-30", "2026-10-02", "CONFIRMED")
	if _, err := f.Rooms.CreateBlock(f.admin, f.propertyID, rooms.CreateBlockInput{RoomID: out["106"].ID, BlockType: "OOO", StartDate: roomstest.BD, EndDate: roomstest.BD.AddDays(2), Reason: "leak"}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestGeneratedListFollowsStaysAndReservations(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	// a room flagged HIGH gets its task at high priority
	if _, err := f.HK.SetFlags(f.admin, f.propertyID, r["104"].ID, housekeeping.FlagsInput{Priority: "HIGH", Note: "VIP early check-in"}); err != nil {
		t.Fatal(err)
	}
	res, err := f.HK.GenerateTasks(f.admin, f.propertyID)
	if err != nil || res.Created != 4 || res.Date != roomstest.BD {
		t.Fatalf("generate: %+v %v", res, err)
	}
	for room, typ := range map[string]string{"101": "CHECKOUT", "102": "STAYOVER", "103": "ARRIVAL", "104": "DIRTY"} {
		if task := f.taskOf(t, r[room].ID, typ); task.Status != "PENDING" || task.Source != "AUTO" || task.AssignedTo != nil {
			t.Fatalf("%s: %+v", room, task)
		}
	}
	if f.taskOf(t, r["103"].ID, "ARRIVAL").Priority != "HIGH" || f.taskOf(t, r["104"].ID, "DIRTY").Priority != "HIGH" || f.taskOf(t, r["102"].ID, "STAYOVER").Priority != "NORMAL" {
		t.Fatal("arrivals and flagged rooms come first")
	}
	if n := f.Count(t, `SELECT count(*) FROM housekeeping_tasks WHERE room_id IN ($1, $2)`, r["105"].ID, r["106"].ID); n != 0 {
		t.Fatal("a clean room and a room out of order get no task")
	}
	// the list is ordered with high priority first, and running it again adds nothing
	list, err := f.HK.Tasks(f.admin, f.propertyID, housekeeping.TaskFilter{})
	if err != nil || len(list.Data) != 4 || list.Data[0].Priority != "HIGH" || list.Data[1].Priority != "HIGH" || list.Data[3].Priority != "NORMAL" {
		t.Fatalf("list: %+v %v", list.Data, err)
	}
	again, err := f.HK.GenerateTasks(f.admin, f.propertyID)
	if err != nil || again.Created != 0 {
		t.Fatalf("second run: %+v %v", again, err)
	}
	// a room that falls dirty later in the day joins at the next run
	if err := f.set(t, f.admin, r["105"].ID, housekeeping.Dirty); err != nil {
		t.Fatal(err)
	}
	later, err := f.HK.GenerateTasks(f.admin, f.propertyID)
	if err != nil || later.Created != 1 {
		t.Fatalf("third run: %+v %v", later, err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.tasks_generated'`) != 3 {
		t.Fatal("every run is audited")
	}
}

func TestAssignStartCompleteMovesTheRoom(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	_, err := f.HK.GenerateTasks(f.admin, f.propertyID)
	if err != nil {
		t.Fatal(err)
	}
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	maidID := f.userID(t, maid)
	front := f.User(t, f.tenantID, f.propertyID, auth.PermReservationRead) // cannot do housekeeping
	task := f.taskOf(t, r["104"].ID, "DIRTY")

	staff, err := f.HK.Staff(f.admin, f.propertyID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range staff {
		found = found || s.ID == maidID
		if s.ID == f.userID(t, front) {
			t.Fatal("a user without housekeeping.update is not staff")
		}
	}
	if !found {
		t.Fatalf("the housekeeper is staff: %+v", staff)
	}

	// only staff can be assigned; assigning needs housekeeping.assign
	frontID := f.userID(t, front)
	_, err = f.HK.AssignTasks(f.admin, f.propertyID, []int64{task.ID}, &frontID)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.HK.AssignTasks(maid, f.propertyID, []int64{task.ID}, &maidID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.HK.AssignTasks(f.admin, f.propertyID, []int64{task.ID, 99999}, &maidID)
	wantCode(t, err, "TASK_NOT_ASSIGNABLE")
	if f.taskOf(t, r["104"].ID, "DIRTY").AssignedTo != nil {
		t.Fatal("nothing changes when one task cannot be assigned")
	}
	n, err := f.HK.AssignTasks(f.admin, f.propertyID, []int64{task.ID}, &maidID)
	if err != nil || n != 1 {
		t.Fatalf("assign: %d %v", n, err)
	}
	if got := f.taskOf(t, r["104"].ID, "DIRTY"); got.AssignedTo == nil || *got.AssignedTo != maidID || got.AssigneeName == "" {
		t.Fatalf("assigned: %+v", got)
	}

	// start: the room becomes CLEANING; finishing it: CLEAN
	started, err := f.HK.StartTask(maid, f.propertyID, task.ID)
	if err != nil || started.Status != "IN_PROGRESS" || started.StartedAt == nil || started.RoomStatus != housekeeping.Cleaning {
		t.Fatalf("start: %+v %v", started, err)
	}
	if f.status(t, r["104"].ID) != housekeeping.Cleaning {
		t.Fatal("the room is being cleaned")
	}
	_, err = f.HK.StartTask(maid, f.propertyID, task.ID)
	wantCode(t, err, "TASK_NOT_PENDING")
	done, err := f.HK.CompleteTask(maid, f.propertyID, task.ID, "all good")
	if err != nil || done.Status != "DONE" || done.CompletedAt == nil || done.RoomStatus != housekeeping.Clean || done.Notes != "all good" {
		t.Fatalf("complete: %+v %v", done, err)
	}
	_, err = f.HK.CompleteTask(maid, f.propertyID, task.ID, "")
	wantCode(t, err, "TASK_ALREADY_CLOSED")
	_, err = f.HK.SkipTask(maid, f.propertyID, task.ID, "x")
	wantCode(t, err, "TASK_ALREADY_CLOSED")
	_, err = f.HK.AssignTasks(f.admin, f.propertyID, []int64{task.ID}, nil)
	wantCode(t, err, "TASK_NOT_ASSIGNABLE") // finished tasks stay with whoever did them
	logs, err := f.HK.Logs(f.admin, f.propertyID, r["104"].ID, nil, 10)
	if err != nil || len(logs) != 2 || logs[0].ToStatus != housekeeping.Clean || logs[1].ToStatus != housekeeping.Cleaning || logs[0].ChangedBy == nil {
		t.Fatalf("logs: %+v %v", logs, err)
	}

	// an unassigned task goes to whoever completes it directly, a room that is already CLEAN keeps its status
	stay := f.taskOf(t, r["102"].ID, "STAYOVER")
	if err := f.set(t, f.admin, r["102"].ID, housekeeping.Clean); err != nil {
		t.Fatal(err)
	}
	if err := f.set(t, f.admin, r["102"].ID, housekeeping.Inspected); err != nil {
		t.Fatal(err)
	}
	direct, err := f.HK.CompleteTask(maid, f.propertyID, stay.ID, "")
	if err != nil || direct.AssignedTo == nil || *direct.AssignedTo != maidID || direct.RoomStatus != housekeeping.Inspected {
		t.Fatalf("direct: %+v %v", direct, err)
	}

	// skipping needs a reason and leaves the room as it is
	co := f.taskOf(t, r["101"].ID, "CHECKOUT")
	_, err = f.HK.SkipTask(maid, f.propertyID, co.ID, " ")
	wantCode(t, err, "VALIDATION_FAILED")
	skipped, err := f.HK.SkipTask(maid, f.propertyID, co.ID, "guest asked not to be disturbed")
	if err != nil || skipped.Status != "SKIPPED" || skipped.Notes != "guest asked not to be disturbed" {
		t.Fatalf("skip: %+v %v", skipped, err)
	}
	_, err = f.HK.StartTask(maid, f.propertyID, 99999)
	wantCode(t, err, "TASK_NOT_FOUND")

	// the list: filters and the workload per person
	list, err := f.HK.Tasks(f.admin, f.propertyID, housekeeping.TaskFilter{})
	if err != nil {
		t.Fatal(err)
	}
	var mine, open *housekeeping.Workload
	for i := range list.Workload {
		w := &list.Workload[i]
		if w.UserID != nil && *w.UserID == maidID {
			mine = w
		}
		if w.UserID == nil {
			open = w
		}
	}
	if mine == nil || mine.Total != 3 || mine.Done != 2 || mine.Skipped != 1 || open == nil || open.Total != 1 || open.Pending != 1 {
		t.Fatalf("workload: %+v", list.Workload)
	}
	pending, err := f.HK.Tasks(f.admin, f.propertyID, housekeeping.TaskFilter{Status: "PENDING"})
	if err != nil || len(pending.Data) != 1 || pending.Data[0].TaskType != "ARRIVAL" || pending.Workload[0].UserID != nil {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	un, err := f.HK.Tasks(f.admin, f.propertyID, housekeeping.TaskFilter{Unassigned: true})
	if err != nil || len(un.Data) != 1 {
		t.Fatalf("unassigned: %+v %v", un, err)
	}
	if _, err := f.HK.Tasks(f.admin, f.propertyID, housekeeping.TaskFilter{Status: "NOPE"}); err == nil {
		t.Fatal("a bad status is refused")
	}
}

func TestManualTasksAndPermissions(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Clean)
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	maidID := f.userID(t, maid)
	task, err := f.HK.CreateTask(f.admin, f.propertyID, housekeeping.TaskInput{RoomID: room.ID, TaskType: "deep", Priority: "high", AssignedTo: &maidID, Notes: "turn the mattress"})
	if err != nil || task.TaskType != "DEEP" || task.Source != "MANUAL" || task.Priority != "HIGH" || task.AssignedTo == nil || task.Notes != "turn the mattress" {
		t.Fatalf("create: %+v %v", task, err)
	}
	// the same kind of task can be added again (a supervisor decides), bad input is refused
	if _, err := f.HK.CreateTask(f.admin, f.propertyID, housekeeping.TaskInput{RoomID: room.ID, TaskType: "DEEP"}); err != nil {
		t.Fatal(err)
	}
	_, err = f.HK.CreateTask(f.admin, f.propertyID, housekeeping.TaskInput{RoomID: room.ID, TaskType: "NAP"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.HK.CreateTask(f.admin, f.propertyID, housekeeping.TaskInput{RoomID: 99999, TaskType: "DEEP"})
	wantCode(t, err, "ROOM_NOT_FOUND")
	_, err = f.HK.CreateTask(maid, f.propertyID, housekeeping.TaskInput{RoomID: room.ID, TaskType: "DEEP"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.HK.GenerateTasks(maid, f.propertyID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.HK.Staff(maid, f.propertyID)
	wantCode(t, err, "PERMISSION_DENIED")
	// a housekeeper sees the list but a user without any access to the property does not
	if _, err := f.HK.Tasks(maid, f.propertyID, housekeeping.TaskFilter{}); err != nil {
		t.Fatal(err)
	}
	other := f.Tenant(t, "XYZ")
	_, err = f.HK.Tasks(roomstest.Admin(other.ID), f.propertyID, housekeeping.TaskFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	reader := f.User(t, f.tenantID, f.propertyID, auth.PermReservationRead)
	_, err = f.HK.StartTask(reader, f.propertyID, task.ID)
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestFlagsShowOnTheBoard(t *testing.T) {
	f := newFixture(t)
	a := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Dirty)
	f.Room(t, f.admin, f.propertyID, f.typeID, "202", housekeeping.Dirty)
	flags, err := f.HK.SetFlags(f.admin, f.propertyID, a.ID, housekeeping.FlagsInput{Priority: "high", DND: true, MakeUpRequested: true, Note: "allergic to feathers"})
	if err != nil || flags.Priority != "HIGH" || !flags.DND || !flags.MakeUpRequested || flags.Note != "allergic to feathers" {
		t.Fatalf("flags: %+v %v", flags, err)
	}
	board, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{})
	if err != nil || len(board) != 2 || board[0].Priority != "HIGH" || !board[0].DND || board[0].FlagNote == "" || board[1].Priority != "NORMAL" || board[1].DND {
		t.Fatalf("board: %+v %v", board, err)
	}
	flagged, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Flagged: true})
	if err != nil || len(flagged) != 1 || flagged[0].RoomID != a.ID {
		t.Fatalf("flagged: %+v %v", flagged, err)
	}
	vacant := housekeeping.Vacant
	only, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Occupancy: &vacant})
	if err != nil || len(only) != 2 {
		t.Fatalf("occupancy filter: %+v %v", only, err)
	}
	occ := housekeeping.Occupied
	none, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Occupancy: &occ})
	if err != nil || len(none) != 0 {
		t.Fatalf("occupied filter: %+v %v", none, err)
	}
	bad := housekeeping.Occupancy("MAYBE")
	if _, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Occupancy: &bad}); err == nil {
		t.Fatal("a bad occupancy filter is refused")
	}
	// a flag change is not a status change: the room has been dirty since before
	var before, after string
	if err := f.Pool.QueryRow(context.Background(), `SELECT updated_at::text FROM room_housekeeping WHERE room_id = $1`, a.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := f.HK.SetFlags(f.admin, f.propertyID, a.ID, housekeeping.FlagsInput{}); err != nil {
		t.Fatal(err)
	}
	if err := f.Pool.QueryRow(context.Background(), `SELECT updated_at::text FROM room_housekeeping WHERE room_id = $1`, a.ID).Scan(&after); err != nil || before != after {
		t.Fatalf("flags must not restart the clock: %s %s %v", before, after, err)
	}
	cleared, err := f.HK.Board(f.admin, f.propertyID, housekeeping.BoardFilter{Flagged: true})
	if err != nil || len(cleared) != 0 {
		t.Fatalf("cleared: %+v", cleared)
	}
	_, err = f.HK.SetFlags(f.admin, f.propertyID, a.ID, housekeeping.FlagsInput{Priority: "URGENT"})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.HK.SetFlags(f.admin, f.propertyID, 99999, housekeeping.FlagsInput{})
	wantCode(t, err, "ROOM_NOT_FOUND")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.flags_changed'`) != 2 {
		t.Fatal("flag changes are audited")
	}
}

// Two supervisors generating the list at once add every task once.
func TestConcurrentGenerationAddsEachTaskOnce(t *testing.T) {
	f := newFixture(t)
	f.listing(t)
	var wg sync.WaitGroup
	created := make([]int, 6)
	for i := range created {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.HK.GenerateTasks(f.admin, f.propertyID)
			if err != nil {
				t.Error(err)
			}
			created[i] = res.Created
		}()
	}
	wg.Wait()
	total := 0
	for _, n := range created {
		total += n
	}
	if total != 4 || f.Count(t, `SELECT count(*) FROM housekeeping_tasks`) != 4 {
		t.Fatalf("created %v, want 4 in all", created)
	}
}

// Finishing one task from two screens at once: one wins, the other is told it is finished, and the room's log shows
// one change.
func TestConcurrentCompletionOfOneTask(t *testing.T) {
	f := newFixture(t)
	f.listing(t)
	if _, err := f.HK.GenerateTasks(f.admin, f.propertyID); err != nil {
		t.Fatal(err)
	}
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	var id, room int64
	if err := f.Pool.QueryRow(context.Background(), `SELECT id, room_id FROM housekeeping_tasks WHERE task_type = 'DIRTY'`).Scan(&id, &room); err != nil {
		t.Fatal(err)
	}
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.HK.CompleteTask(maid, f.propertyID, id, "")
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "TASK_ALREADY_CLOSED")
		}
	}
	if ok != 1 || f.status(t, room) != housekeeping.Clean || f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE room_id = $1`, room) != 1 {
		t.Fatalf("%d completions won", ok)
	}
}

func TestTaskTablesGuardTheirRows(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Dirty)
	if _, err := f.HK.GenerateTasks(f.admin, f.propertyID); err != nil {
		t.Fatal(err)
	}
	if err := f.Exec(t, `UPDATE housekeeping_tasks SET status = 'DONE' WHERE room_id = $1`, room.ID); err == nil {
		t.Fatal("a finished task needs its completion time")
	}
	if err := f.Exec(t, `UPDATE housekeeping_tasks SET status = 'SKIPPED', completed_at = now() WHERE room_id = $1`, room.ID); err == nil {
		t.Fatal("a skipped task needs its reason")
	}
	if err := f.Exec(t, `UPDATE housekeeping_tasks SET task_type = 'NAP' WHERE room_id = $1`, room.ID); err == nil {
		t.Fatal("a task has a known type")
	}
	if err := f.Exec(t, `UPDATE housekeeping_tasks SET assigned_to = 999999, assigned_at = now() WHERE room_id = $1`, room.ID); err == nil {
		t.Fatal("an assignee is a user of the tenant")
	}
	if err := f.Exec(t, `INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type) VALUES ($1, $2, $3, '2026-09-30', 'DIRTY')`, f.tenantID, f.propertyID, room.ID); err == nil {
		t.Fatal("one AUTO task per room, date and type")
	}
}
