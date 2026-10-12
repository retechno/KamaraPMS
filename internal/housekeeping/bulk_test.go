package housekeeping_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

func (f fixture) bulk(t *testing.T, ctx context.Context, to housekeeping.Status, ids ...int64) (housekeeping.BulkOutcome, error) {
	t.Helper()
	return f.HK.SetStatusBulk(ctx, f.propertyID, housekeeping.BulkInput{RoomIDs: ids, Status: to})
}

// Some rooms can make the change and some cannot: the first change, the others are reported with the code that the single
// change gives, and nothing is written for them.
func TestBulkChangesWhatCanChangeAndReportsTheRest(t *testing.T) {
	f := newFixture(t)
	dirty1 := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Dirty)
	dirty2 := f.Room(t, f.admin, f.propertyID, f.typeID, "202", housekeeping.Dirty)
	clean := f.Room(t, f.admin, f.propertyID, f.typeID, "203", housekeeping.Clean)
	inspected := f.Room(t, f.admin, f.propertyID, f.typeID, "204", housekeeping.Inspected)

	out, err := f.bulk(t, f.admin, housekeeping.Clean, dirty1.ID, clean.ID, dirty2.ID, inspected.ID, 987654)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary != (housekeeping.BulkSummary{Requested: 5, Changed: 2, Failed: 3}) {
		t.Fatalf("summary: %+v", out.Summary)
	}
	order := []int64{dirty1.ID, clean.ID, dirty2.ID, inspected.ID, 987654}
	for i, r := range out.Results {
		if r.RoomID != order[i] {
			t.Fatalf("result %d is room %d, want the order asked (%d)", i, r.RoomID, order[i])
		}
	}
	if r := out.Results[0]; !r.OK || r.From != housekeeping.Dirty || r.Status != housekeeping.Clean || r.RoomNumber != "201" || r.Error != nil {
		t.Fatalf("a room that changed: %+v", r)
	}
	for _, i := range []int{1, 3} { // CLEAN→CLEAN, INSPECTED→CLEAN
		r := out.Results[i]
		if r.OK || r.Error == nil || r.Error.Code != "INVALID_HK_TRANSITION" || r.Error.Context["to"] != "CLEAN" || r.Status != r.From {
			t.Fatalf("a refused transition (%d): %+v", i, r)
		}
	}
	if r := out.Results[4]; r.OK || r.Error == nil || r.Error.Code != "ROOM_NOT_FOUND" {
		t.Fatalf("a room that does not exist: %+v", r)
	}

	for id, want := range map[int64]housekeeping.Status{dirty1.ID: housekeeping.Clean, dirty2.ID: housekeeping.Clean, clean.ID: housekeeping.Clean, inspected.ID: housekeeping.Inspected} {
		if got := f.status(t, id); got != want {
			t.Fatalf("room %d is %s, want %s", id, got, want)
		}
	}
	// One log and one audit entry for each room that changed, none for the others.
	if n := f.Count(t, `SELECT count(*) FROM housekeeping_logs`); n != 2 {
		t.Fatalf("logs: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'housekeeping.changed'`); n != 2 {
		t.Fatalf("audit entries: %d", n)
	}
	logs, err := f.HK.Logs(f.admin, f.propertyID, dirty1.ID, nil, 10)
	if err != nil || len(logs) != 1 || logs[0].Source != housekeeping.SourceManual || logs[0].Notes != "Bulk change" || logs[0].FromStatus != housekeeping.Dirty {
		t.Fatalf("the log of a room: %+v %v", logs, err)
	}
	// Notes are written on each log entry.
	if _, err := f.HK.SetStatusBulk(f.admin, f.propertyID, housekeeping.BulkInput{RoomIDs: []int64{clean.ID}, Status: housekeeping.Inspected, Notes: "floor 2 done"}); err != nil {
		t.Fatal(err)
	}
	if logs, _ := f.HK.Logs(f.admin, f.propertyID, clean.ID, nil, 1); len(logs) != 1 || logs[0].Notes != "floor 2 done" {
		t.Fatalf("notes: %+v", logs)
	}
}

// The rule is the one of the single change, for every pair of statuses.
func TestBulkUsesTheRuleOfTheSingleChange(t *testing.T) {
	f := newFixture(t)
	all := []housekeeping.Status{housekeeping.Dirty, housekeeping.Cleaning, housekeeping.Clean, housekeeping.Inspected}
	for _, from := range all {
		for _, to := range all {
			room := f.Room(t, f.admin, f.propertyID, f.typeID, string(from)+string(to), from)
			single := f.set(t, f.admin, room.ID, to)
			// bring the room back to `from` for the bulk call
			if single == nil {
				if err := f.Exec(t, `UPDATE room_housekeeping SET status = $1 WHERE room_id = $2`, string(from), room.ID); err != nil {
					t.Fatal(err)
				}
			}
			out, err := f.bulk(t, f.admin, to, room.ID)
			if err != nil {
				t.Fatal(err)
			}
			r := out.Results[0]
			if single == nil && !r.OK || single != nil && (r.OK || r.Error.Code != "INVALID_HK_TRANSITION") {
				t.Fatalf("%s→%s: the single change gave %v, the bulk change %+v", from, to, single, r)
			}
			if single != nil {
				var c *apperr.Error
				if !errors.As(single, &c) || c.Message != r.Error.Message {
					t.Fatalf("%s→%s: messages differ: %v / %q", from, to, single, r.Error.Message)
				}
			}
		}
	}
}

// Permissions hold for the whole request: nothing changes and nothing is reported per room.
func TestBulkPermissions(t *testing.T) {
	f := newFixture(t)
	r1 := f.Room(t, f.admin, f.propertyID, f.typeID, "201", housekeeping.Clean)
	r2 := f.Room(t, f.admin, f.propertyID, f.typeID, "202", housekeeping.Clean)
	maid := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	supervisor := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate, auth.PermHousekeepingInspect)
	reader := f.User(t, f.tenantID, f.propertyID)

	_, err := f.bulk(t, maid, housekeeping.Inspected, r1.ID, r2.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.bulk(t, reader, housekeeping.Dirty, r1.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	if n := f.Count(t, `SELECT count(*) FROM housekeeping_logs`); n != 0 {
		t.Fatalf("denied changes wrote %d logs", n)
	}
	if out, err := f.bulk(t, maid, housekeeping.Dirty, r1.ID); err != nil || out.Summary.Changed != 1 { // the maid may do everything but inspect
		t.Fatalf("maid: %+v %v", out, err)
	}
	if out, err := f.bulk(t, supervisor, housekeeping.Inspected, r2.ID); err != nil || out.Summary.Changed != 1 {
		t.Fatalf("supervisor: %+v %v", out, err)
	}
}

func TestBulkRefusesAnInvalidRequest(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201")

	_, err := f.bulk(t, f.admin, "BOGUS", room.ID)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.bulk(t, f.admin, housekeeping.Clean)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.HK.SetStatusBulk(f.admin, f.propertyID, housekeeping.BulkInput{RoomIDs: []int64{room.ID}, Status: housekeeping.Clean, Notes: string(make([]byte, 501))})
	wantCode(t, err, "VALIDATION_FAILED")

	// 100 rooms are the most; the same room twice counts once.
	ids := make([]int64, 0, housekeeping.MaxBulkRooms+1)
	for i := range housekeeping.MaxBulkRooms + 1 {
		ids = append(ids, int64(1000+i))
	}
	_, err = f.bulk(t, f.admin, housekeeping.Clean, ids...)
	wantCode(t, err, "VALIDATION_FAILED")
	out, err := f.bulk(t, f.admin, housekeeping.Clean, append(ids[:99], room.ID, room.ID)...)
	if err != nil || out.Summary.Requested != 100 || out.Summary.Changed != 1 || len(out.Results) != 100 {
		t.Fatalf("100 rooms with a duplicate: %+v %v", out.Summary, err)
	}
	if err != nil || f.status(t, room.ID) != housekeeping.Clean {
		t.Fatal("the room did not change")
	}
}

// Rooms of another property or tenant do not exist for the caller: they are reported not found, nothing about them is read or changed.
func TestBulkIsolation(t *testing.T) {
	f := newFixture(t)
	mine := f.Room(t, f.admin, f.propertyID, f.typeID, "201")
	jkt := f.Property(t, f.tenantID, "JKT")
	other := f.Room(t, f.admin, jkt.ID, f.RoomType(t, f.admin, jkt.ID, "DLX").ID, "301")
	xyz := f.Tenant(t, "XYZ")
	sg := f.Property(t, xyz.ID, "SG")
	sgAdmin := roomstest.Admin(xyz.ID)
	foreign := f.Room(t, sgAdmin, sg.ID, f.RoomType(t, sgAdmin, sg.ID, "DLX").ID, "401")

	out, err := f.bulk(t, f.admin, housekeeping.Cleaning, mine.ID, other.ID, foreign.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.Summary.Changed != 1 || out.Results[1].Error == nil || out.Results[1].Error.Code != "ROOM_NOT_FOUND" || out.Results[2].Error == nil || out.Results[2].Error.Code != "ROOM_NOT_FOUND" {
		t.Fatalf("results: %+v", out.Results)
	}
	if out.Results[1].RoomNumber != "" || out.Results[2].RoomNumber != "" {
		t.Fatal("the number of a room of another property is shown")
	}
	for _, id := range []int64{other.ID, foreign.ID} {
		if got := f.status(t, id); got != housekeeping.Dirty {
			t.Fatalf("room %d changed to %s", id, got)
		}
	}
	// A property of another tenant, or one the caller has no access to, is not found.
	_, err = f.HK.SetStatusBulk(f.admin, sg.ID, housekeeping.BulkInput{RoomIDs: []int64{foreign.ID}, Status: housekeeping.Cleaning})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	user := f.User(t, f.tenantID, f.propertyID, auth.PermHousekeepingUpdate)
	_, err = f.HK.SetStatusBulk(user, jkt.ID, housekeeping.BulkInput{RoomIDs: []int64{other.ID}, Status: housekeeping.Cleaning})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// A closed business day refuses the whole request, as it does the single change.
func TestBulkNeedsAnOpenBusinessDay(t *testing.T) {
	f := newFixture(t)
	room := f.Room(t, f.admin, f.propertyID, f.typeID, "201")
	if err := f.Exec(t, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, f.propertyID); err != nil {
		t.Fatal(err)
	}
	_, single := f.HK.SetStatus(f.admin, f.propertyID, room.ID, housekeeping.Cleaning, "")
	_, err := f.bulk(t, f.admin, housekeeping.Cleaning, room.ID)
	if single == nil || err == nil {
		t.Fatalf("single: %v, bulk: %v", single, err)
	}
	var a, b *apperr.Error
	if !errors.As(single, &a) || !errors.As(err, &b) || a.Code != b.Code {
		t.Fatalf("the single change says %v, the bulk change %v", single, err)
	}
	if n := f.Count(t, `SELECT count(*) FROM housekeeping_logs`); n != 0 {
		t.Fatalf("logs: %d", n)
	}
}

// A bulk change and single changes of the same rooms at once: each room is changed once from a status that was current, so the chain of its log
// is unbroken (every entry starts where the one before ended).
func TestBulkAgainstSingleChangesOfTheSameRooms(t *testing.T) {
	f := newFixture(t)
	const n = 12
	ids := make([]int64, n)
	for i := range n {
		ids[i] = f.Room(t, f.admin, f.propertyID, f.typeID, string(rune('A'+i))+"01").ID
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	bulkErrs := make([]error, 3)
	for i := range 3 { // three bulk changes, the lists in different orders
		wg.Add(1)
		go func() {
			defer wg.Done()
			list := append([]int64(nil), ids...)
			if i%2 == 1 {
				for l, r := 0, len(list)-1; l < r; l, r = l+1, r-1 {
					list[l], list[r] = list[r], list[l]
				}
			}
			<-start
			_, bulkErrs[i] = f.bulk(t, f.admin, housekeeping.Cleaning, list...)
		}()
	}
	for _, id := range ids { // and a single change of each room
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := f.set(t, f.admin, id, housekeeping.Cleaning); err != nil && !apperr.IsCode(err, "INVALID_HK_TRANSITION") {
				t.Errorf("single: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range bulkErrs {
		if err != nil {
			t.Fatalf("bulk: %v", err) // no deadlock, no other failure
		}
	}
	for _, id := range ids {
		if got := f.status(t, id); got != housekeeping.Cleaning {
			t.Fatalf("room %d is %s", id, got)
		}
		// DIRTY→CLEANING exactly once, whoever got there first.
		if c := f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE room_id = $1 AND from_status = 'DIRTY' AND to_status = 'CLEANING'`, id); c != 1 {
			t.Fatalf("room %d: %d changes to CLEANING", id, c)
		}
		if c := f.Count(t, `SELECT count(*) FROM housekeeping_logs WHERE room_id = $1`, id); c != 1 {
			t.Fatalf("room %d: %d log entries, want 1", id, c)
		}
	}
}
