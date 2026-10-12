package housekeeping_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/auth"
)

// The generated list holds at most one active task per room and day: a PENDING task of another kind is corrected, a task that
// is started, done or skipped is left alone and nothing is added next to it.

// autoTasks lists the generated tasks of a room on the business date as "TYPE/STATUS", oldest first.
func (f fixture) autoTasks(t *testing.T, room int64) []string {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT task_type || '/' || status FROM housekeeping_tasks WHERE room_id = $1 AND source = 'AUTO' ORDER BY id`, room)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (f fixture) generate(t *testing.T, ctx context.Context) housekeeping.GenerateResult {
	t.Helper()
	res, err := f.HK.GenerateTasks(ctx, f.propertyID)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// departs changes the departure date of the stay in a room.
func (f fixture) departs(t *testing.T, room int64, date string) {
	t.Helper()
	if err := f.Exec(t, `UPDATE stays SET departure_date = $2::date WHERE id = (SELECT stay_id FROM stay_rooms WHERE room_id = $1)`, room, date); err != nil {
		t.Fatal(err)
	}
}

func wantTasks(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tasks %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tasks %v, want %v", got, want)
		}
	}
}

func TestAnExtendedStayCorrectsThePendingCheckoutInsteadOfAddingASecondTask(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t) // 101 leaves today
	f.generate(t, f.admin)
	wantTasks(t, f.autoTasks(t, r["101"].ID), "CHECKOUT/PENDING")
	task := f.taskOf(t, r["101"].ID, "CHECKOUT")
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	maidID := f.userID(t, maid)
	if _, err := f.HK.AssignTasks(f.admin, f.propertyID, []int64{task.ID}, &maidID); err != nil {
		t.Fatal(err)
	}

	// the guest extends: the stay now ends on the 3rd of October
	f.departs(t, r["101"].ID, "2026-10-03")
	res := f.generate(t, f.admin)
	if res.Created != 0 || res.Retyped != 1 {
		t.Fatalf("generate: %+v", res)
	}
	wantTasks(t, f.autoTasks(t, r["101"].ID), "STAYOVER/PENDING")
	same := f.taskOf(t, r["101"].ID, "STAYOVER")
	if same.ID != task.ID || same.AssignedTo == nil || *same.AssignedTo != maidID {
		t.Fatalf("the same task, still given to the housekeeper: %+v", same)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.task_retyped' AND entity_id = $1 AND old_data->>'task_type' = 'CHECKOUT' AND new_data->>'task_type' = 'STAYOVER' AND new_data->>'reason' <> ''`, task.ID); n != 1 {
		t.Fatalf("the correction is audited with its reason (%d)", n)
	}

	// running it again changes nothing and audits no second correction
	again := f.generate(t, f.admin)
	if again.Created != 0 || again.Retyped != 0 {
		t.Fatalf("second run: %+v", again)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.task_retyped'`) != 1 {
		t.Fatal("one correction, one entry")
	}
}

func TestADepartureMovedUpCorrectsStayoverToCheckout(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t) // 102 stays over
	f.generate(t, f.admin)
	wantTasks(t, f.autoTasks(t, r["102"].ID), "STAYOVER/PENDING")
	f.departs(t, r["102"].ID, "2026-09-30")
	if res := f.generate(t, f.admin); res.Created != 0 || res.Retyped != 1 {
		t.Fatalf("generate: %+v", res)
	}
	wantTasks(t, f.autoTasks(t, r["102"].ID), "CHECKOUT/PENDING")
}

// The same rule for a vacant room: its DIRTY task becomes ARRIVAL when a guest is assigned to it later in the day.
func TestAVacantRoomIsCorrectedTheSameWay(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	f.generate(t, f.admin)
	wantTasks(t, f.autoTasks(t, r["104"].ID), "DIRTY/PENDING")
	f.Line(t, f.tenantID, f.propertyID, f.typeID, r["104"].ID, "2026-09-30", "2026-10-02", "CONFIRMED")
	if res := f.generate(t, f.admin); res.Created != 0 || res.Retyped != 1 {
		t.Fatalf("generate: %+v", res)
	}
	wantTasks(t, f.autoTasks(t, r["104"].ID), "ARRIVAL/PENDING")
}

func TestAStartedOrFinishedTaskIsNeitherChangedNorDoubled(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	f.generate(t, f.admin)
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	started := f.taskOf(t, r["101"].ID, "CHECKOUT")
	if _, err := f.HK.StartTask(maid, f.propertyID, started.ID); err != nil {
		t.Fatal(err)
	}
	done := f.taskOf(t, r["102"].ID, "STAYOVER")
	if _, err := f.HK.StartTask(maid, f.propertyID, done.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.HK.CompleteTask(maid, f.propertyID, done.ID, ""); err != nil {
		t.Fatal(err)
	}
	// both stays change their end
	f.departs(t, r["101"].ID, "2026-10-03")
	f.departs(t, r["102"].ID, "2026-09-30")
	if res := f.generate(t, f.admin); res.Created != 0 || res.Retyped != 0 {
		t.Fatalf("generate: %+v", res)
	}
	wantTasks(t, f.autoTasks(t, r["101"].ID), "CHECKOUT/IN_PROGRESS")
	wantTasks(t, f.autoTasks(t, r["102"].ID), "STAYOVER/DONE")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.task_retyped'`) != 0 {
		t.Fatal("no correction was made")
	}
}

// A skipped task is final for the day, in an occupied and in a vacant room: the next run adds nothing, and a supervisor goes
// back to the room with a manual task.
func TestASkippedTaskIsFinalForTheDay(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	f.generate(t, f.admin)
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	for room, typ := range map[string]string{"102": "STAYOVER", "104": "DIRTY"} {
		if _, err := f.HK.SkipTask(maid, f.propertyID, f.taskOf(t, r[room].ID, typ).ID, "guest asleep"); err != nil {
			t.Fatal(err)
		}
	}
	if res := f.generate(t, f.admin); res.Created != 0 || res.Retyped != 0 {
		t.Fatalf("generate: %+v", res)
	}
	wantTasks(t, f.autoTasks(t, r["102"].ID), "STAYOVER/SKIPPED")
	wantTasks(t, f.autoTasks(t, r["104"].ID), "DIRTY/SKIPPED")
	// a manual task is how to go back
	if _, err := f.HK.CreateTask(f.admin, f.propertyID, housekeeping.TaskInput{RoomID: r["102"].ID, TaskType: "STAYOVER"}); err != nil {
		t.Fatal(err)
	}
	if f.Count(t, `SELECT count(*) FROM housekeeping_tasks WHERE room_id = $1`, r["102"].ID) != 2 {
		t.Fatal("the skipped task and the manual one")
	}
}

// Manual tasks are not part of the rule: any number of them, and they neither hold back nor are corrected by the generated one.
func TestManualTasksAreNotLimitedNorCorrected(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	for i := 0; i < 2; i++ {
		if _, err := f.HK.CreateTask(f.admin, f.propertyID, housekeeping.TaskInput{RoomID: r["101"].ID, TaskType: "OTHER", Notes: "guest request"}); err != nil {
			t.Fatal(err)
		}
	}
	if res := f.generate(t, f.admin); res.Created != 4 || res.Retyped != 0 {
		t.Fatalf("generate: %+v", res)
	}
	wantTasks(t, f.autoTasks(t, r["101"].ID), "CHECKOUT/PENDING")
	if f.Count(t, `SELECT count(*) FROM housekeeping_tasks WHERE room_id = $1 AND source = 'MANUAL' AND task_type = 'OTHER'`, r["101"].ID) != 2 {
		t.Fatal("the manual tasks stay as they were")
	}
}

// Runs that meet a task whose kind has to change correct it at most once, and never leave two active generated tasks in a room,
// also while somebody starts the task.
func TestConcurrentGenerationsCorrectOnceAndNeverDouble(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	f.generate(t, f.admin)
	f.departs(t, r["101"].ID, "2026-10-03")
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	task := f.taskOf(t, r["101"].ID, "CHECKOUT")

	var wg sync.WaitGroup
	retyped := make([]int, 6)
	for i := range retyped {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := f.HK.GenerateTasks(f.admin, f.propertyID)
			if err != nil {
				t.Error(err)
			}
			retyped[i] = res.Retyped
		}()
	}
	wg.Add(1)
	go func() { // somebody starts the task while the runs go on
		defer wg.Done()
		if _, err := f.HK.StartTask(maid, f.propertyID, task.ID); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()

	total := 0
	for _, n := range retyped {
		total += n
	}
	if total > 1 {
		t.Fatalf("a task is corrected at most once: %v", retyped)
	}
	got := f.autoTasks(t, r["101"].ID)
	if len(got) != 1 || (got[0] != "STAYOVER/IN_PROGRESS" && got[0] != "CHECKOUT/IN_PROGRESS") {
		t.Fatalf("one started task, of the kind it had when it was started or the corrected one: %v", got)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.task_retyped'`); n != total {
		t.Fatalf("%d corrections, %d entries", total, n)
	}
	for _, room := range r {
		if n := f.Count(t, `SELECT count(*) FROM housekeeping_tasks WHERE room_id = $1 AND source = 'AUTO' AND status IN ('PENDING', 'IN_PROGRESS')`, room.ID); n > 1 {
			t.Fatalf("room %d has %d active generated tasks", room.ID, n)
		}
	}
}

// The unique index is the backstop: a second active generated task for a room and day is refused, whatever its kind.
func TestTheIndexRefusesASecondActiveGeneratedTask(t *testing.T) {
	f := newFixture(t)
	r := f.listing(t)
	f.generate(t, f.admin)
	err := f.Exec(t, `INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, source) VALUES ($1, $2, $3, $4::date, 'OTHER', 'AUTO')`, f.tenantID, f.propertyID, r["101"].ID, "2026-09-30")
	if err == nil {
		t.Fatal("a second PENDING generated task for room 101 was accepted")
	}
}
