package maintenance_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID, typeID int64
	admin                    context.Context
	r101, r102               rooms.Room
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin := roomstest.Admin(tn.ID)
	typ := e.RoomType(t, admin, p.ID, "DLX")
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, typeID: typ.ID, admin: admin}
	f.r101 = e.Room(t, admin, p.ID, typ.ID, "101", housekeeping.Clean)
	f.r102 = e.Room(t, admin, p.ID, typ.ID, "102", housekeeping.Clean)
	return f
}

func (f *fx) userID(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	p, err := auth.Require(ctx)
	if err != nil || p.ActorID() == nil {
		t.Fatalf("no user: %v", err)
	}
	return *p.ActorID()
}

func (f *fx) report(t *testing.T, room *int64, place, desc string) maintenance.Request {
	t.Helper()
	r, err := f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{RoomID: room, Location: place, Category: "plumbing", Description: desc, Priority: "high"})
	must(t, err)
	return r
}

func TestReportAndReadRequests(t *testing.T) {
	f := setup(t)
	r := f.report(t, &f.r101.ID, "", "Shower leaks")
	if r.Status != "OPEN" || r.RequestNumber != "MNT000001" || r.RoomNumber != "101" || r.Category != "PLUMBING" || r.Priority != "HIGH" || r.BusinessDate != roomstest.BD ||
		r.Block != nil || r.AssignedTo != nil {
		t.Fatalf("request: %+v", r)
	}
	lobby, err := f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{Location: "Lobby", Category: "ELECTRICAL", Description: "Lamp is out"})
	must(t, err)
	if lobby.RoomID != nil || lobby.Location != "Lobby" || lobby.Priority != "NORMAL" || lobby.RequestNumber != "MNT000002" {
		t.Fatalf("lobby: %+v", lobby)
	}
	// rules
	for name, in := range map[string]maintenance.CreateInput{
		"no room or place": {Category: "AC", Description: "x"},
		"bad category":     {Location: "Lobby", Category: "MAGIC", Description: "x"},
		"no description":   {Location: "Lobby", Category: "AC", Description: " "},
		"bad priority":     {Location: "Lobby", Category: "AC", Description: "x", Priority: "WHENEVER"},
	} {
		if _, err := f.Maintenance.Create(f.admin, f.propID, in); err == nil {
			t.Fatalf("%s must be refused", name)
		} else {
			wantCode(t, err, "VALIDATION_FAILED")
		}
	}
	unknown := int64(99999)
	_, err = f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{RoomID: &unknown, Category: "AC", Description: "x"})
	wantCode(t, err, "ROOM_NOT_FOUND")

	// a technician who may only report sees and files requests but cannot change them
	tech := f.User(t, f.tenantID, f.propID, auth.PermMaintenanceReport)
	mine, err := f.Maintenance.Create(tech, f.propID, maintenance.CreateInput{RoomID: &f.r102.ID, Category: "AC", Description: "Too warm"})
	must(t, err)
	if mine.ReporterName == "" {
		t.Fatalf("reporter: %+v", mine)
	}
	got, err := f.Maintenance.Get(tech, f.propID, r.ID)
	if err != nil || got.ID != r.ID {
		t.Fatalf("get: %+v %v", got, err)
	}
	_, err = f.Maintenance.Start(tech, f.propID, r.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.Maintenance.Staff(tech, f.propID)
	wantCode(t, err, "PERMISSION_DENIED")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.Maintenance.List(nobody, f.propID, maintenance.Filter{}, nil, 10)
	wantCode(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	_, err = f.Maintenance.Get(roomstest.Admin(other.ID), f.propID, r.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.Maintenance.Get(f.admin, f.propID, 99999)
	wantCode(t, err, "MAINTENANCE_REQUEST_NOT_FOUND")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'maintenance.reported'`) != 3 {
		t.Fatal("reports are audited")
	}
}

func TestLifecycleAssignStartResolveReopen(t *testing.T) {
	f := setup(t)
	r := f.report(t, &f.r101.ID, "", "Shower leaks")
	tech := f.User(t, f.tenantID, f.propID, auth.PermMaintenanceReport, auth.PermMaintenanceManage)
	techID := f.userID(t, tech)
	front := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)

	staff, err := f.Maintenance.Staff(f.admin, f.propID)
	must(t, err)
	found := false
	for _, s := range staff {
		found = found || s.ID == techID
		if s.ID == f.userID(t, front) {
			t.Fatal("a front desk user is not maintenance staff")
		}
	}
	if !found {
		t.Fatalf("staff: %+v", staff)
	}
	frontID := f.userID(t, front)
	_, err = f.Maintenance.Assign(f.admin, f.propID, r.ID, &frontID)
	wantCode(t, err, "VALIDATION_FAILED")
	a, err := f.Maintenance.Assign(f.admin, f.propID, r.ID, &techID)
	if err != nil || a.AssignedTo == nil || *a.AssignedTo != techID || a.AssigneeName == "" {
		t.Fatalf("assign: %+v %v", a, err)
	}
	un, err := f.Maintenance.Assign(f.admin, f.propID, r.ID, nil)
	if err != nil || un.AssignedTo != nil {
		t.Fatalf("take back: %+v %v", un, err)
	}

	// starting gives an unassigned request to the starter; it cannot be started twice
	s, err := f.Maintenance.Start(tech, f.propID, r.ID)
	if err != nil || s.Status != "IN_PROGRESS" || s.StartedAt == nil || s.AssignedTo == nil || *s.AssignedTo != techID {
		t.Fatalf("start: %+v %v", s, err)
	}
	_, err = f.Maintenance.Start(tech, f.propID, r.ID)
	wantCode(t, err, "REQUEST_NOT_OPEN")

	// details can change while the request is open
	cat, pri := "appliance", "urgent"
	u, err := f.Maintenance.Update(tech, f.propID, r.ID, maintenance.Patch{Category: &cat, Priority: &pri})
	if err != nil || u.Category != "APPLIANCE" || u.Priority != "URGENT" || u.Description != "Shower leaks" {
		t.Fatalf("update: %+v %v", u, err)
	}
	bad := "MAGIC"
	_, err = f.Maintenance.Update(tech, f.propID, r.ID, maintenance.Patch{Category: &bad})
	wantCode(t, err, "VALIDATION_FAILED")

	done, err := f.Maintenance.Resolve(tech, f.propID, r.ID, maintenance.CloseInput{Note: "Replaced the seal"})
	if err != nil || done.Status != "RESOLVED" || done.ClosedAt == nil || done.ResolutionNote != "Replaced the seal" {
		t.Fatalf("resolve: %+v %v", done, err)
	}
	_, err = f.Maintenance.Resolve(tech, f.propID, r.ID, maintenance.CloseInput{})
	wantCode(t, err, "REQUEST_NOT_OPEN")
	_, err = f.Maintenance.Update(tech, f.propID, r.ID, maintenance.Patch{Priority: &pri})
	wantCode(t, err, "REQUEST_NOT_OPEN")
	_, err = f.Maintenance.Assign(f.admin, f.propID, r.ID, &techID)
	wantCode(t, err, "REQUEST_NOT_OPEN")

	// a repair that did not hold: back to OPEN with the old note cleared
	re, err := f.Maintenance.Reopen(tech, f.propID, r.ID)
	if err != nil || re.Status != "OPEN" || re.ClosedAt != nil || re.ResolutionNote != "" || re.StartedAt != nil {
		t.Fatalf("reopen: %+v %v", re, err)
	}
	_, err = f.Maintenance.Reopen(tech, f.propID, r.ID)
	wantCode(t, err, "REQUEST_NOT_RESOLVED")

	// cancelling needs a reason, and a cancelled request is final
	_, err = f.Maintenance.Cancel(tech, f.propID, r.ID, maintenance.CloseInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	c, err := f.Maintenance.Cancel(tech, f.propID, r.ID, maintenance.CloseInput{Note: "Duplicate of another request"})
	if err != nil || c.Status != "CANCELLED" || c.ResolutionNote == "" {
		t.Fatalf("cancel: %+v %v", c, err)
	}
	_, err = f.Maintenance.Reopen(tech, f.propID, r.ID)
	wantCode(t, err, "REQUEST_NOT_RESOLVED")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action LIKE 'maintenance.%' AND entity_id = $1`, r.ID) != 8 {
		t.Fatal("every change is audited")
	}
}

func TestListFiltersAndPaging(t *testing.T) {
	f := setup(t)
	a := f.report(t, &f.r101.ID, "", "one")
	b := f.report(t, &f.r102.ID, "", "two")
	c, err := f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{Location: "Pool", Category: "AC", Description: "three", Priority: "low"})
	must(t, err)
	_, err = f.Maintenance.Resolve(f.admin, f.propID, a.ID, maintenance.CloseInput{})
	must(t, err)
	all, err := f.Maintenance.List(f.admin, f.propID, maintenance.Filter{}, nil, 10)
	if err != nil || len(all) != 3 || all[0].ID != c.ID || all[2].ID != a.ID {
		t.Fatalf("all: %+v %v", all, err)
	}
	open, err := f.Maintenance.List(f.admin, f.propID, maintenance.Filter{OpenOnly: true}, nil, 10)
	if err != nil || len(open) != 2 {
		t.Fatalf("open: %+v %v", open, err)
	}
	for name, tc := range map[string]struct {
		f    maintenance.Filter
		want int
	}{
		"status":   {maintenance.Filter{Status: "RESOLVED"}, 1},
		"room":     {maintenance.Filter{RoomID: &f.r102.ID}, 1},
		"category": {maintenance.Filter{Category: "AC"}, 1},
		"priority": {maintenance.Filter{Priority: "HIGH"}, 2},
	} {
		got, err := f.Maintenance.List(f.admin, f.propID, tc.f, nil, 10)
		if err != nil || len(got) != tc.want {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
	}
	_, err = f.Maintenance.List(f.admin, f.propID, maintenance.Filter{Status: "NOPE"}, nil, 10)
	wantCode(t, err, "VALIDATION_FAILED")
	page, err := f.Maintenance.List(f.admin, f.propID, maintenance.Filter{}, &b.ID, 10)
	if err != nil || len(page) != 1 || page[0].ID != a.ID {
		t.Fatalf("after %d: %+v %v", b.ID, page, err)
	}
}

func TestBlockingTheRoomForARequest(t *testing.T) {
	f := setup(t)
	r := f.report(t, &f.r101.ID, "", "AC is dead")
	lobby, err := f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{Location: "Lobby", Category: "OTHER", Description: "Door sticks"})
	must(t, err)
	end := roomstest.BD.AddDays(3)
	_, err = f.Maintenance.Block(f.admin, f.propID, lobby.ID, maintenance.BlockInput{BlockType: "OOO", EndDate: end})
	wantCode(t, err, "REQUEST_HAS_NO_ROOM")
	_, err = f.Maintenance.Block(f.admin, f.propID, r.ID, maintenance.BlockInput{BlockType: "NOPE", EndDate: end})
	wantCode(t, err, "VALIDATION_FAILED")

	// a technician without room_block.manage cannot take the room out of sale, and nothing is left behind
	tech := f.User(t, f.tenantID, f.propID, auth.PermMaintenanceManage)
	_, err = f.Maintenance.Block(tech, f.propID, r.ID, maintenance.BlockInput{BlockType: "OOO", EndDate: end})
	wantCode(t, err, "PERMISSION_DENIED")
	if f.Count(t, `SELECT count(*) FROM room_blocks`) != 0 {
		t.Fatal("no block without the permission")
	}

	blocked, err := f.Maintenance.Block(f.admin, f.propID, r.ID, maintenance.BlockInput{BlockType: "ooo", EndDate: end})
	if err != nil || blocked.Block == nil || blocked.Block.BlockType != "OOO" || blocked.Block.StartDate != roomstest.BD || blocked.Block.EndDate != end || blocked.Block.Status != "ACTIVE" {
		t.Fatalf("block: %+v %v", blocked, err)
	}
	var reason string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT reason FROM room_blocks WHERE id = $1`, blocked.Block.ID).Scan(&reason))
	if reason != "MNT000001: AC is dead" {
		t.Fatalf("block reason: %q", reason)
	}
	_, err = f.Maintenance.Block(f.admin, f.propID, r.ID, maintenance.BlockInput{BlockType: "OOO", EndDate: end})
	wantCode(t, err, "REQUEST_ALREADY_BLOCKED")

	// resolving without releasing leaves the block, resolving with it cancels the block
	other := f.report(t, &f.r102.ID, "", "Lamp")
	ob, err := f.Maintenance.Block(f.admin, f.propID, other.ID, maintenance.BlockInput{BlockType: "OOS", EndDate: end})
	must(t, err)
	kept, err := f.Maintenance.Resolve(f.admin, f.propID, other.ID, maintenance.CloseInput{})
	if err != nil || kept.Block == nil || kept.Block.Status != "ACTIVE" || ob.Block == nil {
		t.Fatalf("kept: %+v %v", kept, err)
	}
	freed, err := f.Maintenance.Resolve(f.admin, f.propID, r.ID, maintenance.CloseInput{ReleaseBlock: true, Note: "fixed"})
	if err != nil || freed.Block == nil || freed.Block.Status != "CANCELLED" {
		t.Fatalf("freed: %+v %v", freed, err)
	}
	if f.Count(t, `SELECT count(*) FROM room_blocks WHERE status = 'ACTIVE'`) != 1 {
		t.Fatal("only the block that was kept stays active")
	}
	// the room can be blocked again for a new request once the old block is gone
	again := f.report(t, &f.r101.ID, "", "AC again")
	if _, err := f.Maintenance.Block(f.admin, f.propID, again.ID, maintenance.BlockInput{BlockType: "OOO", EndDate: end}); err != nil {
		t.Fatal(err)
	}
}

func TestBlockFailsWhenAStayHoldsTheRoom(t *testing.T) {
	f := setup(t)
	f.Stay(t, f.tenantID, f.propID, f.typeID, f.r101.ID, "2026-09-28", "2026-10-03")
	r := f.report(t, &f.r101.ID, "", "Window")
	_, err := f.Maintenance.Block(f.admin, f.propID, r.ID, maintenance.BlockInput{BlockType: "OOO", EndDate: roomstest.BD.AddDays(2)})
	wantCode(t, err, "ROOM_BLOCK_CONFLICT")
	got, err := f.Maintenance.Get(f.admin, f.propID, r.ID)
	if err != nil || got.Block != nil || f.Count(t, `SELECT count(*) FROM room_blocks`) != 0 {
		t.Fatalf("a failed block changes nothing: %+v %v", got, err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'maintenance.room_blocked'`) != 0 {
		t.Fatal("nothing is audited for a block that did not happen")
	}
}

// The same request blocked from several screens at once: one block, the others are told it is blocked.
func TestConcurrentBlocksOfOneRequest(t *testing.T) {
	f := setup(t)
	r := f.report(t, &f.r101.ID, "", "AC")
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Maintenance.Block(f.admin, f.propID, r.ID, maintenance.BlockInput{BlockType: "OOO", EndDate: roomstest.BD.AddDays(2)})
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "REQUEST_ALREADY_BLOCKED")
		}
	}
	if ok != 1 || f.Count(t, `SELECT count(*) FROM room_blocks WHERE status = 'ACTIVE'`) != 1 {
		t.Fatalf("%d blocks won", ok)
	}
}

// Resolving and cancelling the same request at once: one wins.
func TestConcurrentCloseOfOneRequest(t *testing.T) {
	f := setup(t)
	r := f.report(t, &f.r101.ID, "", "AC")
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = f.Maintenance.Resolve(f.admin, f.propID, r.ID, maintenance.CloseInput{})
			} else {
				_, errs[i] = f.Maintenance.Cancel(f.admin, f.propID, r.ID, maintenance.CloseInput{Note: "no longer needed"})
			}
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "REQUEST_NOT_OPEN")
		}
	}
	if ok != 1 {
		t.Fatalf("%d closes won", ok)
	}
}

func TestMaintenanceTableGuards(t *testing.T) {
	f := setup(t)
	r := f.report(t, &f.r101.ID, "", "Shower")
	f.User(t, f.tenantID, f.propID, auth.PermMaintenanceReport) // a user to point at
	for name, sql := range map[string]string{
		"a request is about a room or a place": `UPDATE maintenance_requests SET room_id = NULL, location = NULL WHERE id = $1`,
		"a known category":                     `UPDATE maintenance_requests SET category = 'MAGIC' WHERE id = $1`,
		"a known priority":                     `UPDATE maintenance_requests SET priority = 'WHENEVER' WHERE id = $1`,
		"a closed request has its time":        `UPDATE maintenance_requests SET status = 'RESOLVED' WHERE id = $1`,
		"a cancelled request has its reason":   `UPDATE maintenance_requests SET status = 'CANCELLED', closed_at = now() WHERE id = $1`,
		"in progress has its start":            `UPDATE maintenance_requests SET status = 'IN_PROGRESS' WHERE id = $1`,
		"an assignee has a time":               `UPDATE maintenance_requests SET assigned_to = (SELECT id FROM users LIMIT 1) WHERE id = $1`,
		"a block needs a room":                 `UPDATE maintenance_requests SET room_id = NULL, location = 'x', room_block_id = 1 WHERE id = $1`,
	} {
		if err := f.Exec(t, sql, r.ID); err == nil {
			t.Fatalf("%s", name)
		}
	}
}
