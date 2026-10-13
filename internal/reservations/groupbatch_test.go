package reservations_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/guests"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

// Bulk rooms of a group (docs/architecture/30-group-folio.md, part A).

// newGroup makes a group that runs 1 to 10 October 2026 with a contact.
func (f *fx) newGroup(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO booking_groups (tenant_id, property_id, code, name, contact_name, contact_email, contact_phone, arrival_date, departure_date)
		VALUES ($1, $2, $3, 'Conference Alpha', 'Siti Rahmawati', 'siti@alpha.test', '+62 812 3456 7890', '2026-10-01', '2026-10-10') RETURNING id`, f.tenantID, f.propID, code).Scan(&id))
	return id
}

func (f *fx) row(typ rooms.RoomType, count int, arrival, departure string) reservations.BatchRow {
	return reservations.BatchRow{RoomTypeID: typ.ID, RatePlanID: f.plan, Count: count, Arrival: d(arrival), Departure: d(departure), Adults: 2}
}

func (f *fx) batch(rows ...reservations.BatchRow) reservations.BatchInput {
	return reservations.BatchInput{Rows: rows}
}

// counts is what a refused batch must leave exactly as it was.
func (f *fx) counts(t *testing.T) [5]int {
	t.Helper()
	return [5]int{
		f.count(t, `SELECT count(*) FROM reservations`),
		f.count(t, `SELECT count(*) FROM reservation_rooms`),
		f.count(t, `SELECT count(*) FROM guests`),
		f.count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('reservation.created', 'group.rooms_added')`),
		f.count(t, `SELECT count(*) FROM booking_groups WHERE contact_guest_id IS NOT NULL`),
	}
}

type mailCounter struct{ n atomic.Int32 }

func (m *mailCounter) ReservationConfirmed(context.Context, int64, int64, int64, civil.Date, *int64) error {
	m.n.Add(1)
	return nil
}

func TestGroupRoomsOneReservationPerRoom(t *testing.T) {
	f := setup(t)
	mail := &mailCounter{}
	f.Res.SetConfirmedHook(mail)
	g := f.newGroup(t, "ALPHA")

	res, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k-1", f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04"), f.row(f.std, 1, "2026-10-03", "2026-10-05")))
	must(t, err)
	if res.Rooms != 3 || res.Nights != 2+2+2 || len(res.Reservations) != 3 || res.Replayed {
		t.Fatalf("result: %+v", res)
	}
	if res.Reservations[0].Row != 0 || res.Reservations[1].Row != 0 || res.Reservations[2].Row != 1 || res.Reservations[2].Position != 3 {
		t.Fatalf("rows: %+v", res.Reservations)
	}
	// one reservation for each room, confirmed, in the group, booked by the contact, with a guest of its own
	var contact int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT contact_guest_id FROM booking_groups WHERE id = $1`, g).Scan(&contact))
	for i, r := range res.Reservations {
		var status string
		var group, booker int64
		must(t, f.Pool.QueryRow(context.Background(), `SELECT status, booking_group_id, guest_id FROM reservations WHERE id = $1`, r.ReservationID).Scan(&status, &group, &booker))
		if status != "CONFIRMED" || group != g || booker != contact {
			t.Fatalf("reservation %d: %s group %d booker %d (contact %d)", i, status, group, booker, contact)
		}
		var name string
		var placeholder bool
		must(t, f.Pool.QueryRow(context.Background(), `SELECT last_name, is_placeholder FROM guests WHERE id = $1`, *r.GuestID).Scan(&name, &placeholder))
		if want := fmt.Sprintf("Conference Alpha · %d", i+1); name != want || !placeholder {
			t.Fatalf("placeholder %d: %q %v", i, name, placeholder)
		}
	}
	var contactName, contactEmail string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT coalesce(first_name, '') || '|' || last_name, email FROM guests WHERE id = $1`, contact).Scan(&contactName, &contactEmail))
	if contactName != "Siti|Rahmawati" || contactEmail != "siti@alpha.test" {
		t.Fatalf("contact guest: %s %s", contactName, contactEmail)
	}
	// the inventory is held, no room is assigned, and the audit says what happened: one entry for each reservation and one for the batch
	if n := f.available(t, f.dlx, "2026-10-03"); n.Available != 0 || n.Demand != 2 {
		t.Fatalf("dlx: %+v", n)
	}
	if f.count(t, `SELECT count(*) FROM reservation_rooms WHERE room_id IS NOT NULL`) != 0 {
		t.Fatal("a bulk booking assigns no room")
	}
	if f.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'reservation.created'`) != 3 || f.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'group.rooms_added' AND entity_id = $1`, g) != 1 {
		t.Fatal("audit: one entry for each reservation and one for the batch")
	}
	if mail.n.Load() != 0 {
		t.Fatalf("a bulk booking sends no confirmation for each room: %d", mail.n.Load())
	}
	// a second batch of the same group continues the numbering and reuses the contact guest
	res2, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k-2", f.batch(f.row(f.std, 1, "2026-10-06", "2026-10-07")))
	must(t, err)
	var name string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT last_name FROM guests WHERE id = $1`, *res2.Reservations[0].GuestID).Scan(&name))
	if name != "Conference Alpha · 4" || f.count(t, `SELECT count(*) FROM guests WHERE code <> 'G1' AND NOT is_placeholder`) != 1 {
		t.Fatalf("second batch: %q", name)
	}
	// an ordinary booking still sends its confirmation
	f.book(t, f.dlx, "2026-10-08", "2026-10-09")
	if mail.n.Load() != 1 {
		t.Fatalf("an ordinary booking confirms: %d", mail.n.Load())
	}
}

func TestGroupRoomsAllOrNothingWhenTheRoomsRunOutInTheMiddle(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	before := f.counts(t)

	// DLX has two rooms: the second row asks for one more on the same nights. The third row (STD) would have fit.
	_, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k-1", f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04"), f.row(f.dlx, 1, "2026-10-03", "2026-10-05"), f.row(f.std, 1, "2026-10-02", "2026-10-04")))
	e := code(t, err, "ROOM_TYPE_NOT_AVAILABLE")
	if e.Context["row"] != 1 || e.Context["room_in_row"] != 1 {
		t.Fatalf("the error names the row and the room: %v", e.Context)
	}
	if after := f.counts(t); after != before {
		t.Fatalf("nothing is kept: before %v after %v", before, after)
	}
	// the same when the last room of a row is the one that does not fit
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, g, "k-2", f.batch(f.row(f.dlx, 3, "2026-10-02", "2026-10-04")))
	if e := code(t, err, "ROOM_TYPE_NOT_AVAILABLE"); e.Context["row"] != 0 || e.Context["room_in_row"] != 3 {
		t.Fatalf("context: %v", e.Context)
	}
	if after := f.counts(t); after != before {
		t.Fatalf("nothing is kept (3 rooms): before %v after %v", before, after)
	}
	// and a row outside the dates of the group, found before anything is written
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, g, "k-3", f.batch(f.row(f.std, 1, "2026-10-02", "2026-10-04"), f.row(f.dlx, 1, "2026-10-08", "2026-10-12")))
	if e := code(t, err, "VALIDATION_FAILED"); e.Fields[0].Field != "rows[1].arrival_date" || e.Fields[0].Code != "OUTSIDE_GROUP_DATES" {
		t.Fatalf("fields: %+v", e.Fields)
	}
	if after := f.counts(t); after != before {
		t.Fatalf("nothing is kept (dates): before %v after %v", before, after)
	}
	// the refused request left the sequence free of nothing but its own rollback: a booking right after works
	if _, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k-4", f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04"))); err != nil {
		t.Fatal(err)
	}
}

func TestGroupRoomsIdempotency(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	in := f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04"))
	first, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "same-key", in)
	must(t, err)
	before := f.counts(t)

	again, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "same-key", in)
	must(t, err)
	if !again.Replayed || len(again.Reservations) != 2 || again.Reservations[0].ReservationID != first.Reservations[0].ReservationID || again.Reservations[1].ReservationID != first.Reservations[1].ReservationID {
		t.Fatalf("replay: %+v vs %+v", again, first)
	}
	if after := f.counts(t); after != before {
		t.Fatalf("a replay writes nothing: %v then %v", before, after)
	}
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, g, "same-key", f.batch(f.row(f.dlx, 1, "2026-10-02", "2026-10-04")))
	wantCode(t, err, "IDEMPOTENCY_KEY_REUSED")
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, g, "", in)
	wantCode(t, err, "IDEMPOTENCY_KEY_REQUIRED")
	if after := f.counts(t); after != before {
		t.Fatalf("a refused repeat writes nothing: %v then %v", before, after)
	}
}

func TestGroupRoomsConcurrentBatchesThatDoNotBothFit(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.Res.CreateGroupRooms(f.admin, f.propID, g, fmt.Sprintf("race-%d", i), f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04")))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "ROOM_TYPE_NOT_AVAILABLE")
		}
	}
	if ok != 1 || f.count(t, `SELECT count(*) FROM reservations`) != 2 || f.count(t, `SELECT count(*) FROM guests WHERE is_placeholder`) != 2 {
		t.Fatalf("exactly one batch wins: %d won, %d reservations", ok, f.count(t, `SELECT count(*) FROM reservations`))
	}
	// the numbering of the placeholders did not repeat
	if f.count(t, `SELECT count(DISTINCT last_name) FROM guests WHERE is_placeholder`) != 2 {
		t.Fatal("two placeholders share a name")
	}
}

func TestGroupRoomsAFewMoreReservationsStayInTheLockOrder(t *testing.T) {
	f := setup(t)
	f.Res.SetConfirmedHook(&mailCounter{})
	g := f.newGroup(t, "ALPHA")
	for _, n := range []string{"103", "104", "105", "106", "107", "108", "109", "110"} {
		f.Room(t, f.admin, f.propID, f.dlx.ID, n)
	}
	// ten reservations in one transaction: the group lock of the second one comes after the document number of the first, and must not be asked for again
	res, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "many", f.batch(f.row(f.dlx, 6, "2026-10-02", "2026-10-04"), f.row(f.dlx, 4, "2026-10-03", "2026-10-05")))
	must(t, err)
	if res.Rooms != 10 {
		t.Fatalf("rooms: %d", res.Rooms)
	}
}

func TestGroupRoomsDraftsHoldNothing(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	no := false
	in := f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04"))
	in.Confirm = &no
	if _, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "drafts", in); err != nil {
		t.Fatal(err)
	}
	if f.count(t, `SELECT count(*) FROM reservations WHERE status = 'DRAFT'`) != 2 {
		t.Fatal("drafts")
	}
	if n := f.available(t, f.dlx, "2026-10-03"); n.Available != 2 {
		t.Fatalf("a draft holds nothing: %+v", n)
	}
}

func TestGroupRoomsLimits(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	_, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k", f.batch(f.row(f.std, reservations.MaxBatchRooms+1, "2026-10-02", "2026-10-04")))
	wantCode(t, err, "TOO_MANY_ROOMS")
	var rows []reservations.BatchRow
	for i := 0; i <= reservations.MaxBatchRows; i++ {
		rows = append(rows, f.row(f.std, 1, "2026-10-02", "2026-10-04"))
	}
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, g, "k", f.batch(rows...))
	wantCode(t, err, "TOO_MANY_ROWS")
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, g, strings.Repeat("k", 91), f.batch(f.row(f.std, 1, "2026-10-02", "2026-10-04")))
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Res.CreateGroupRooms(f.admin, f.propID, 999999, "k", f.batch(f.row(f.std, 1, "2026-10-02", "2026-10-04")))
	wantCode(t, err, "GROUP_NOT_FOUND")
}

type countingApprover struct {
	inner reservations.Approver
	calls atomic.Int32
}

func (c *countingApprover) VerifyApprovalFor(ctx context.Context, propertyID int64, in *iam.ApprovalInput, perm auth.Permission) (iam.Approval, error) {
	c.calls.Add(1)
	return c.inner.VerifyApprovalFor(ctx, propertyID, in, perm)
}

func TestGroupRoomsOneApprovalForTheRateOfTheGroup(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	ap := &countingApprover{inner: f.IAM}
	f.Res.SetApprover(ap)
	clerk := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermGuestWrite, auth.PermReservationRead, auth.PermReservationCreate, auth.PermReservationUpdate, auth.PermReservationOverrideRate)
	_, approver := f.Account(t, f.tenantID, f.propID, auth.PermReservationOverrideApprove)
	row := f.row(f.dlx, 2, "2026-10-02", "2026-10-04")
	row.RatePerNight = "700000"
	std := f.row(f.std, 1, "2026-10-02", "2026-10-04") // at the price in force: no override
	in := f.batch(row, std)
	before := f.counts(t)

	// no reason, then no approval: nothing is made
	_, err := f.Res.CreateGroupRooms(clerk, f.propID, g, "k-1", in)
	wantCode(t, err, "VALIDATION_FAILED")
	in.RateOverrideReason = "Group rate agreed in the contract"
	_, err = f.Res.CreateGroupRooms(clerk, f.propID, g, "k-2", in)
	wantCode(t, err, "APPROVAL_REQUIRED")
	if after := f.counts(t); after != before {
		t.Fatalf("a refused override makes nothing: %v then %v", before, after)
	}
	// the permission is needed to enter the rate at all
	nobody := f.User(t, f.tenantID, f.propID, auth.PermGuestRead, auth.PermGuestWrite, auth.PermReservationRead, auth.PermReservationCreate)
	_, err = f.Res.CreateGroupRooms(nobody, f.propID, g, "k-3", in)
	wantCode(t, err, "PERMISSION_DENIED")

	in.RateOverrideApproval = &iam.ApprovalInput{Email: approver, Password: roomstest.Password}
	ap.calls.Store(0)
	res, err := f.Res.CreateGroupRooms(clerk, f.propID, g, "k-4", in)
	must(t, err)
	if res.Rooms != 3 || ap.calls.Load() != 1 {
		t.Fatalf("rooms %d, the approval was verified %d times: one for the whole request", res.Rooms, ap.calls.Load())
	}
	got := f.count(t, `SELECT count(*) FROM reservation_room_rates WHERE is_override AND amount = 700000`)
	if got != 4 { // two rooms, two nights
		t.Fatalf("override nights: %d", got)
	}
	if f.count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('reservation.created', 'group.rooms_added') AND new_data ->> 'rate_override_reason' = 'Group rate agreed in the contract'`) != 4 {
		t.Fatal("the reason and the approver are on every reservation and on the batch")
	}
	// a row at the price in force needs no approval at all
	ap.calls.Store(0)
	if _, err := f.Res.CreateGroupRooms(clerk, f.propID, g, "k-5", f.batch(f.row(f.std, 1, "2026-10-06", "2026-10-07"))); err != nil || ap.calls.Load() != 0 {
		t.Fatalf("no override, no approval: %v %d", err, ap.calls.Load())
	}
}

func TestGroupRoomsPreview(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	before := f.counts(t)
	over := f.row(f.dlx, 2, "2026-10-03", "2026-10-05") // overlaps the first row: together they want 3 of the 2 rooms on the 3rd and 4th
	over.RatePerNight = "800000"
	pv, err := f.Res.PreviewGroupRooms(f.admin, f.propID, g, f.batch(f.row(f.dlx, 1, "2026-10-02", "2026-10-04"), over, f.row(f.std, 1, "2026-10-02", "2026-10-03")))
	must(t, err)
	if len(pv.Rows) != 3 || pv.Rooms != 4 || pv.OK {
		t.Fatalf("preview: %+v", pv)
	}
	r0, r1, r2 := pv.Rows[0], pv.Rows[1], pv.Rows[2]
	if !r0.Uniform || r0.RatePerNight != "1000000.00" && r0.RatePerNight != "1000000" || len(r0.Nightly) != 2 || r0.Short != nil || r0.Override {
		t.Fatalf("row 0: %+v", r0)
	}
	if r0.RoomEstimate.Total.IsZero() || !r0.RowEstimate.Total.Equal(r0.RoomEstimate.Total) {
		t.Fatalf("row 0 estimate: %+v", r0.RoomEstimate)
	}
	if !r1.Override || r1.Short == nil || r1.Short.Missing != 1 || r1.Short.Date != d("2026-10-03") || !r1.RowEstimate.Total.Equal(r1.RoomEstimate.Total.Mul(decimal.NewFromInt(2))) {
		t.Fatalf("row 1: %+v", r1)
	}
	if r2.Short != nil || len(r2.Problems) != 0 {
		t.Fatalf("row 2: %+v", r2)
	}
	for _, a := range pv.Availability { // the nights, earliest first
		for i := 1; i < len(a.Nights); i++ {
			if !a.Nights[i-1].Date.Before(a.Nights[i].Date) {
				t.Fatalf("the nights of %s are not in date order: %+v", a.Code, a.Nights)
			}
		}
	}
	if len(pv.Availability) != 2 {
		t.Fatalf("availability: %+v", pv.Availability)
	}
	for _, a := range pv.Availability {
		if a.Code == "DLX" {
			for _, n := range a.Nights {
				if n.Date == d("2026-10-03") && (n.Available != 2 || n.Needed != 3 || n.Left != -1) {
					t.Fatalf("DLX 3 Oct: %+v", n)
				}
			}
		}
	}
	if after := f.counts(t); after != before {
		t.Fatalf("a preview writes nothing: %v then %v", before, after)
	}
	// a row that cannot be sold is a problem of that row, not an error of the request
	bad := f.row(f.dlx, 1, "2026-09-25", "2026-09-28")
	pv, err = f.Res.PreviewGroupRooms(f.admin, f.propID, g, f.batch(bad))
	must(t, err)
	if pv.OK || len(pv.Rows[0].Problems) == 0 {
		t.Fatalf("problems: %+v", pv.Rows[0])
	}
}

func TestPlaceholdersAreNotFoundUntilNamed(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	res, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k", f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04")))
	must(t, err)
	search := func(q string) int {
		t.Helper()
		out, err := f.Guests.Search(f.admin, q, nil, listsort.Sort{}, nil, 50)
		must(t, err)
		return len(out.Guests)
	}
	if search("Conference") != 0 || search("Siti") != 1 {
		t.Fatalf("placeholders are not found, the contact is: %d %d", search("Conference"), search("Siti"))
	}
	list, err := f.Res.RoomingList(f.admin, f.propID, g)
	must(t, err)
	if len(list) != 2 || !list[0].IsPlaceholder || list[0].Name != "Conference Alpha · 1" {
		t.Fatalf("rooming list: %+v", list)
	}
	out, err := f.Res.UpdateRoomingList(f.admin, f.propID, g, []reservations.RoomingEdit{{LineID: res.Reservations[0].LineID, Name: "Budi Santoso", Email: "budi@alpha.test", Phone: "+62 811 1111 2222"}, {LineID: res.Reservations[1].LineID, Name: list[1].Name}})
	must(t, err)
	if out.Updated != 1 || out.Unchanged != 1 {
		t.Fatalf("update: %+v", out)
	}
	if search("Budi") != 1 || search("Conference") != 0 {
		t.Fatal("a named guest is found, the other placeholder is still not")
	}
	var first, last string
	var placeholder bool
	must(t, f.Pool.QueryRow(context.Background(), `SELECT first_name, last_name, is_placeholder FROM guests WHERE id = $1`, *res.Reservations[0].GuestID).Scan(&first, &last, &placeholder))
	if first != "Budi" || last != "Santoso" || placeholder {
		t.Fatalf("named guest: %s %s %v", first, last, placeholder)
	}
	// all or none: a bad e-mail on the second row keeps the first row unchanged
	_, err = f.Res.UpdateRoomingList(f.admin, f.propID, g, []reservations.RoomingEdit{{LineID: res.Reservations[1].LineID, Name: "Ayu Lestari"}, {LineID: res.Reservations[0].LineID, Name: "Budi Santoso", Email: "not-an-email"}})
	if e := code(t, err, "VALIDATION_FAILED"); e.Fields[0].Field != "rows[1].email" {
		t.Fatalf("fields: %+v", e.Fields)
	}
	must(t, f.Pool.QueryRow(context.Background(), `SELECT last_name FROM guests WHERE id = $1`, *res.Reservations[1].GuestID).Scan(&last))
	if !strings.HasPrefix(last, "Conference") {
		t.Fatalf("the first row was applied: %s", last)
	}
	// a room of another group is refused
	other := f.newGroup(t, "BETA")
	_, err = f.Res.UpdateRoomingList(f.admin, f.propID, other, []reservations.RoomingEdit{{LineID: res.Reservations[0].LineID, Name: "X Y"}})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestRoomingListImportReportsEveryWrongRow(t *testing.T) {
	f := setup(t)
	g := f.newGroup(t, "ALPHA")
	res, err := f.Res.CreateGroupRooms(f.admin, f.propID, g, "k", f.batch(f.row(f.dlx, 2, "2026-10-02", "2026-10-04")))
	must(t, err)
	n1, n2 := res.Reservations[0].ConfirmationNumber, res.Reservations[1].ConfirmationNumber
	csv := "confirmation_number,name,email,phone\n" +
		n1 + ",Budi Santoso,budi@alpha.test,+62 811 1111 2222\n" +
		"NOPE-1,Ayu Lestari,ayu@alpha.test,\n" +
		n2 + ",Ayu Lestari,not-an-email,\n" +
		n1 + ",Twice,,\n"
	before := f.counts(t)

	dry, err := f.Res.ImportRoomingList(f.admin, f.propID, g, []byte(csv), true)
	must(t, err)
	if !dry.DryRun || dry.Applied || len(dry.Problems) != 3 {
		t.Fatalf("dry run: %+v", dry)
	}
	got := map[int]string{}
	for _, p := range dry.Problems {
		got[p.Row] = p.Code
	}
	if got[3] != "NOT_IN_GROUP" || got[4] == "" || got[5] != "DUPLICATE" {
		t.Fatalf("every wrong row is reported, with its line: %+v", dry.Problems)
	}
	if f.count(t, `SELECT count(*) FROM guests WHERE last_name = 'Santoso'`) != 0 {
		t.Fatal("a dry run writes nothing")
	}
	// applied with wrong rows: nothing changes
	_, err = f.Res.ImportRoomingList(f.admin, f.propID, g, []byte(csv), false)
	wantCode(t, err, "ROOMING_IMPORT_INVALID")
	if f.count(t, `SELECT count(*) FROM guests WHERE last_name = 'Santoso'`) != 0 {
		t.Fatal("all or nothing")
	}
	// a clean file: the dry run says what would change, the import does it
	clean := "confirmation_number,name,email,phone\n" + n1 + ",Budi Santoso,budi@alpha.test,\n" + n2 + ",Ayu Lestari,,\n"
	dry, err = f.Res.ImportRoomingList(f.admin, f.propID, g, []byte(clean), true)
	must(t, err)
	if len(dry.Problems) != 0 || len(dry.Changes) != 2 || dry.Changes[0].ToName != "Budi Santoso" {
		t.Fatalf("clean dry run: %+v", dry)
	}
	done, err := f.Res.ImportRoomingList(f.admin, f.propID, g, []byte(clean), false)
	must(t, err)
	if !done.Applied || f.count(t, `SELECT count(*) FROM guests WHERE last_name = 'Santoso' AND NOT is_placeholder`) != 1 {
		t.Fatalf("import: %+v", done)
	}
	_ = before
	// a file without the columns
	_, err = f.Res.ImportRoomingList(f.admin, f.propID, g, []byte("a,b\n1,2\n"), false)
	wantCode(t, err, "ROOMING_IMPORT_INVALID")
}

var _ = guests.Profile{}
