package lostfound_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/housekeeping"
	"kamarapms/internal/lostfound"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
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
	r101                     rooms.Room
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
	return f
}

func (f *fx) found(t *testing.T, desc string) lostfound.Item {
	t.Helper()
	it, err := f.LostFound.Create(f.admin, f.propID, lostfound.CreateInput{
		Description: desc, Category: "electronics", RoomID: &f.r101.ID, StorageLocation: "Shelf A", PossibleOwner: "the guest of 101",
	})
	must(t, err)
	return it
}

func TestRecordAndReadItems(t *testing.T) {
	f := setup(t)
	it := f.found(t, "Black phone charger")
	if it.Status != "STORED" || it.ItemNumber != "LF000001" || it.RoomNumber != "101" || it.Category != "ELECTRONICS" || it.FoundOn != roomstest.BD ||
		it.StorageLocation != "Shelf A" || it.ClosedAt != nil {
		t.Fatalf("item: %+v", it)
	}
	pool, err := f.LostFound.Create(f.admin, f.propID, lostfound.CreateInput{Description: "Blue towel", Category: "CLOTHING", Location: "Pool"})
	must(t, err)
	if pool.RoomID != nil || pool.Location != "Pool" || pool.ItemNumber != "LF000002" {
		t.Fatalf("pool: %+v", pool)
	}
	for name, in := range map[string]lostfound.CreateInput{
		"no room or place": {Description: "x", Category: "BAGS"},
		"no description":   {Description: " ", Category: "BAGS", Location: "Lobby"},
		"bad category":     {Description: "x", Category: "MAGIC", Location: "Lobby"},
		"long storage":     {Description: "x", Category: "BAGS", Location: "Lobby", StorageLocation: string(make([]rune, 101))},
	} {
		_, err := f.LostFound.Create(f.admin, f.propID, in)
		if err == nil {
			t.Fatalf("%s must be refused", name)
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	unknown := int64(99999)
	_, err = f.LostFound.Create(f.admin, f.propID, lostfound.CreateInput{Description: "x", Category: "BAGS", RoomID: &unknown})
	wantCode(t, err, "ROOM_NOT_FOUND")

	// search, filters and paging
	list, err := f.LostFound.List(f.admin, f.propID, lostfound.Filter{}, nil, 10)
	if err != nil || len(list) != 2 || list[0].ID != pool.ID {
		t.Fatalf("list: %+v %v", list, err)
	}
	for name, tc := range map[string]struct {
		f    lostfound.Filter
		want int
	}{
		"text":     {lostfound.Filter{Query: "charger"}, 1},
		"number":   {lostfound.Filter{Query: "lf000002"}, 1},
		"storage":  {lostfound.Filter{Query: "shelf"}, 1},
		"owner":    {lostfound.Filter{Query: "guest of 101"}, 1},
		"category": {lostfound.Filter{Category: "CLOTHING"}, 1},
		"room":     {lostfound.Filter{RoomID: &f.r101.ID}, 1},
		"status":   {lostfound.Filter{Status: "RETURNED"}, 0},
		"dates":    {lostfound.Filter{FoundFrom: ptr(roomstest.BD), FoundTo: ptr(roomstest.BD)}, 2},
		"later":    {lostfound.Filter{FoundFrom: ptr(roomstest.BD.AddDays(1))}, 0},
	} {
		got, err := f.LostFound.List(f.admin, f.propID, tc.f, nil, 10)
		if err != nil || len(got) != tc.want {
			t.Fatalf("%s: %d %v", name, len(got), err)
		}
	}
	_, err = f.LostFound.List(f.admin, f.propID, lostfound.Filter{Status: "NOPE"}, nil, 10)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.LostFound.List(f.admin, f.propID, lostfound.Filter{FoundFrom: ptr(roomstest.BD.AddDays(2)), FoundTo: ptr(roomstest.BD)}, nil, 10)
	wantCode(t, err, "VALIDATION_FAILED")
	page, err := f.LostFound.List(f.admin, f.propID, lostfound.Filter{}, &pool.ID, 10)
	if err != nil || len(page) != 1 || page[0].ID != it.ID {
		t.Fatalf("page: %+v %v", page, err)
	}
	_, err = f.LostFound.Get(f.admin, f.propID, 99999)
	wantCode(t, err, "LOST_ITEM_NOT_FOUND")

	// permissions and tenant isolation
	finder := f.User(t, f.tenantID, f.propID, auth.PermLostFoundReport)
	mine, err := f.LostFound.Create(finder, f.propID, lostfound.CreateInput{Description: "Umbrella", Category: "OTHER", Location: "Lobby"})
	if err != nil || mine.FinderName == "" {
		t.Fatalf("finder: %+v %v", mine, err)
	}
	_, err = f.LostFound.Return(finder, f.propID, it.ID, lostfound.ReturnInput{ClaimantName: "X"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.LostFound.Update(finder, f.propID, it.ID, lostfound.Patch{})
	wantCode(t, err, "PERMISSION_DENIED")
	nobody := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	_, err = f.LostFound.List(nobody, f.propID, lostfound.Filter{}, nil, 10)
	wantCode(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	_, err = f.LostFound.Get(roomstest.Admin(other.ID), f.propID, it.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'lostfound.recorded'`) != 3 {
		t.Fatal("recording is audited")
	}
}

func ptr[T any](v T) *T { return &v }

func TestHandBackAndDisposeAreFinal(t *testing.T) {
	f := setup(t)
	a, b := f.found(t, "Watch"), f.found(t, "Scarf")
	keeper := f.User(t, f.tenantID, f.propID, auth.PermLostFoundReport, auth.PermLostFoundManage)

	// details change while the item is stored
	desc, storage := " Gold watch ", "Safe"
	u, err := f.LostFound.Update(keeper, f.propID, a.ID, lostfound.Patch{Description: &desc, StorageLocation: &storage})
	if err != nil || u.Description != "Gold watch" || u.StorageLocation != "Safe" || u.Category != "ELECTRONICS" {
		t.Fatalf("update: %+v %v", u, err)
	}
	empty := " "
	_, err = f.LostFound.Update(keeper, f.propID, a.ID, lostfound.Patch{Description: &empty})
	wantCode(t, err, "VALIDATION_FAILED")

	// returning needs the claimant, records who and when, and cannot be repeated or undone
	_, err = f.LostFound.Return(keeper, f.propID, a.ID, lostfound.ReturnInput{ClaimantProof: "ID card"})
	wantCode(t, err, "VALIDATION_FAILED")
	r, err := f.LostFound.Return(keeper, f.propID, a.ID, lostfound.ReturnInput{ClaimantName: "Siti Nurhaliza", ClaimantProof: "KTP 3171", Note: "collected at the desk"})
	if err != nil || r.Status != "RETURNED" || r.ClaimantName != "Siti Nurhaliza" || r.ClaimantProof != "KTP 3171" || r.ClosedAt == nil || r.ClosedOn == nil || *r.ClosedOn != roomstest.BD || r.CloserName == "" {
		t.Fatalf("return: %+v %v", r, err)
	}
	_, err = f.LostFound.Return(keeper, f.propID, a.ID, lostfound.ReturnInput{ClaimantName: "Someone else"})
	wantCode(t, err, "ITEM_NOT_STORED")
	_, err = f.LostFound.Dispose(keeper, f.propID, a.ID, lostfound.DisposeInput{Reason: "x"})
	wantCode(t, err, "ITEM_NOT_STORED")
	_, err = f.LostFound.Update(keeper, f.propID, a.ID, lostfound.Patch{Description: &desc})
	wantCode(t, err, "ITEM_NOT_STORED")

	// disposing needs a reason
	_, err = f.LostFound.Dispose(keeper, f.propID, b.ID, lostfound.DisposeInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	d, err := f.LostFound.Dispose(keeper, f.propID, b.ID, lostfound.DisposeInput{Reason: "Unclaimed after 90 days, donated"})
	if err != nil || d.Status != "DISPOSED" || d.CloseNote != "Unclaimed after 90 days, donated" || d.ClosedAt == nil {
		t.Fatalf("dispose: %+v %v", d, err)
	}
	stored, err := f.LostFound.List(keeper, f.propID, lostfound.Filter{Status: "STORED"}, nil, 10)
	if err != nil || len(stored) != 0 {
		t.Fatalf("stored: %+v %v", stored, err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('lostfound.returned', 'lostfound.disposed', 'lostfound.updated')`) != 3 {
		t.Fatal("every ending is audited")
	}
	_, err = f.LostFound.Return(keeper, f.propID, 99999, lostfound.ReturnInput{ClaimantName: "x"})
	wantCode(t, err, "LOST_ITEM_NOT_FOUND")
}

func TestPossibleOwnersAreTheGuestsOfTheRoom(t *testing.T) {
	f := setup(t)
	// 102: a guest who left two days ago; 103: left a week ago; 101: in house now; the pool has no room
	r102 := f.Room(t, f.admin, f.propID, f.typeID, "102", housekeeping.Clean)
	r103 := f.Room(t, f.admin, f.propID, f.typeID, "103", housekeeping.Clean)
	f.Stay(t, f.tenantID, f.propID, f.typeID, r102.ID, "2026-09-26", "2026-09-28")
	f.Stay(t, f.tenantID, f.propID, f.typeID, r103.ID, "2026-09-20", "2026-09-23")
	f.Stay(t, f.tenantID, f.propID, f.typeID, f.r101.ID, "2026-09-29", "2026-10-02")
	for room, end := range map[int64]string{r102.ID: "2026-09-28", r103.ID: "2026-09-23"} {
		if err := f.Exec(t, `UPDATE stay_rooms SET end_business_date = $2::date, check_out_at = now() WHERE room_id = $1`, room, end); err != nil {
			t.Fatal(err)
		}
	}
	in101 := f.found(t, "Phone")
	mk := func(room int64) lostfound.Item {
		it, err := f.LostFound.Create(f.admin, f.propID, lostfound.CreateInput{Description: "Ring", Category: "JEWELRY", RoomID: &room})
		must(t, err)
		return it
	}
	left := mk(r102.ID)
	old := mk(r103.ID)
	lobby, err := f.LostFound.Create(f.admin, f.propID, lostfound.CreateInput{Description: "Hat", Category: "CLOTHING", Location: "Lobby"})
	must(t, err)

	got, err := f.LostFound.PossibleOwners(f.admin, f.propID, in101.ID)
	if err != nil || len(got) != 1 || got[0].StayStatus != "OPEN" || got[0].GuestName == "" {
		t.Fatalf("in house: %+v %v", got, err)
	}
	got, err = f.LostFound.PossibleOwners(f.admin, f.propID, left.ID)
	if err != nil || len(got) != 1 || got[0].DepartureDate != civil.MustParseDate("2026-09-28") {
		t.Fatalf("left two days ago: %+v %v", got, err)
	}
	got, err = f.LostFound.PossibleOwners(f.admin, f.propID, old.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("a week ago is too long ago: %+v %v", got, err)
	}
	got, err = f.LostFound.PossibleOwners(f.admin, f.propID, lobby.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("no room, no owners: %+v %v", got, err)
	}
	// it shows guests of stays, so reservation.read is needed as well
	finder := f.User(t, f.tenantID, f.propID, auth.PermLostFoundReport)
	_, err = f.LostFound.PossibleOwners(finder, f.propID, left.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.LostFound.PossibleOwners(f.admin, f.propID, 99999)
	wantCode(t, err, "LOST_ITEM_NOT_FOUND")
}

// Handing the same item back from two desks, or handing it back while it is disposed of: one ending wins.
func TestConcurrentEndingsOfOneItem(t *testing.T) {
	f := setup(t)
	it := f.found(t, "Laptop")
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				_, errs[i] = f.LostFound.Return(f.admin, f.propID, it.ID, lostfound.ReturnInput{ClaimantName: "Owner"})
			} else {
				_, errs[i] = f.LostFound.Dispose(f.admin, f.propID, it.ID, lostfound.DisposeInput{Reason: "Unclaimed"})
			}
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "ITEM_NOT_STORED")
		}
	}
	if ok != 1 {
		t.Fatalf("%d endings won", ok)
	}
}

func TestLostFoundTableGuards(t *testing.T) {
	f := setup(t)
	it := f.found(t, "Phone")
	for name, sql := range map[string]string{
		"an item is from a room or a place": `UPDATE lost_found_items SET room_id = NULL, location = NULL WHERE id = $1`,
		"a known category":                  `UPDATE lost_found_items SET category = 'MAGIC' WHERE id = $1`,
		"a known status":                    `UPDATE lost_found_items SET status = 'LOST' WHERE id = $1`,
		"a closed item has its time":        `UPDATE lost_found_items SET status = 'DISPOSED', close_note = 'x' WHERE id = $1`,
		"a returned item has its claimant":  `UPDATE lost_found_items SET status = 'RETURNED', closed_at = now(), closed_on = '2026-09-30' WHERE id = $1`,
		"a disposed item has its reason":    `UPDATE lost_found_items SET status = 'DISPOSED', closed_at = now(), closed_on = '2026-09-30' WHERE id = $1`,
		"a stored item is not closed":       `UPDATE lost_found_items SET closed_at = now(), closed_on = '2026-09-30' WHERE id = $1`,
	} {
		if err := f.Exec(t, sql, it.ID); err == nil {
			t.Fatalf("%s", name)
		}
	}
}
