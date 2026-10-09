package reservations_test

import (
	"context"
	"testing"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms/roomstest"
)

// The reservation list: the status staff see is derived in SQL and must be the one DisplayStatus gives; the filters are asked of the database (a page is never filtered by the screen); each
// row carries its rooms with the booked price of the arrival night and the deposit, from the snapshot and the ledger.

func (f *fx) list(t *testing.T, filter reservations.ListFilter) []reservations.Summary {
	t.Helper()
	rows, err := f.Res.List(f.admin, f.propID, filter, 0, 100)
	must(t, err)
	return rows
}

func (f *fx) summaryOf(t *testing.T, id int64) reservations.Summary {
	t.Helper()
	for _, r := range f.list(t, reservations.ListFilter{}) {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("reservation %d is not listed", id)
	return reservations.Summary{}
}

func TestTheListedStatusIsTheOneOfTheDetail(t *testing.T) {
	f := setup(t)
	draft, err := f.Res.Create(f.admin, f.propID, "d", f.input(false, f.line(f.std, "2026-10-05", "2026-10-06")))
	must(t, err)
	reserved := f.book(t, f.dlx, "2026-10-05", "2026-10-07")
	cancelled := f.book(t, f.dlx, "2026-10-08", "2026-10-09")
	_, err = f.Res.Cancel(f.admin, f.propID, cancelled.ID, cancelled.Version, "changed plans")
	must(t, err)
	noShow := f.book(t, f.dlx, "2026-09-30", "2026-10-01")
	_, err = f.Res.NoShow(f.admin, f.propID, noShow.ID, noShow.Rooms[0].ID, noShow.Version, "did not come")
	must(t, err)
	// a reservation of two rooms: one cancelled, one still reserved; and one with a room in house and one reserved; and one all checked out
	two, err := f.Res.Create(f.admin, f.propID, "t", f.input(true, f.line(f.dlx, "2026-10-10", "2026-10-11"), f.line(f.std, "2026-10-10", "2026-10-11")))
	must(t, err)
	_, err = f.Res.CancelLine(f.admin, f.propID, two.ID, two.Rooms[0].ID, two.Version, "one room less")
	must(t, err)
	mixed, err := f.Res.Create(f.admin, f.propID, "m", f.input(true, f.line(f.dlx, "2026-10-12", "2026-10-13"), f.line(f.std, "2026-10-12", "2026-10-13")))
	must(t, err)
	must(t, f.Exec(t, `UPDATE reservation_rooms SET status = 'CHECKED_IN' WHERE id = $1`, mixed.Rooms[0].ID))
	done, err := f.Res.Create(f.admin, f.propID, "o", f.input(true, f.line(f.dlx, "2026-10-14", "2026-10-15")))
	must(t, err)
	must(t, f.Exec(t, `UPDATE reservation_rooms SET status = 'COMPLETED' WHERE id = $1`, done.Rooms[0].ID))

	want := map[int64]string{draft.ID: "DRAFT", reserved.ID: "CONFIRMED", cancelled.ID: "CANCELLED", noShow.ID: "NO_SHOW", two.ID: "CONFIRMED", mixed.ID: "IN_HOUSE", done.ID: "CHECKED_OUT"}
	for id, status := range want {
		detail, err := f.Res.Get(f.admin, f.propID, id)
		must(t, err)
		if got := f.summaryOf(t, id).DisplayStatus; got != status || got != detail.DisplayStatus {
			t.Fatalf("reservation %d: list says %q, detail says %q, want %q", id, got, detail.DisplayStatus, status)
		}
		// the filter by that status finds it, and finds nothing of another status
		for _, r := range f.list(t, reservations.ListFilter{DisplayStatus: status}) {
			if r.DisplayStatus != status {
				t.Fatalf("filter %s returned %s", status, r.DisplayStatus)
			}
		}
		found := false
		for _, r := range f.list(t, reservations.ListFilter{DisplayStatus: status}) {
			found = found || r.ID == id
		}
		if !found {
			t.Fatalf("filter %s misses reservation %d", status, id)
		}
	}
}

func TestListRowsCarryTheirRoomsRateAndDeposit(t *testing.T) {
	f := setup(t)
	res, err := f.Res.Create(f.admin, f.propID, "k", f.input(true, f.line(f.dlx, "2026-10-05", "2026-10-08"), f.line(f.std, "2026-10-05", "2026-10-06")))
	must(t, err)
	_, err = f.Res.AssignRoom(f.admin, f.propID, res.ID, res.Rooms[0].ID, res.Version, f.r101.ID, false)
	must(t, err)
	row := f.summaryOf(t, res.ID)
	if row.Nights != 3 || row.RoomCount != 2 || len(row.Rooms) != 2 || row.Deposit != nil {
		t.Fatalf("row: %+v", row)
	}
	dlx, std := row.Rooms[0], row.Rooms[1]
	if dlx.RoomTypeCode != "DLX" || dlx.RoomNumber != "101" || dlx.RatePlanCode != "BAR" || dlx.RateAmount != "1000000" || dlx.Nights != 3 || dlx.AdultCount != 2 ||
		std.RoomNumber != "" || std.RateAmount != "500000" || dlx.BillingCompany != "" {
		t.Fatalf("rooms: %+v", row.Rooms)
	}
	// the master price changes: the booked price of the row does not
	_, err = f.Rates.FillRates(f.admin, f.propID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID, f.std.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "2500000"})
	must(t, err)
	if got := f.summaryOf(t, res.ID).Rooms[0].RateAmount; got != "1000000" {
		t.Fatalf("the list followed the rate master: %s", got)
	}
	// a deposit is the credit on the deposit folio, from the ledger
	dep, err := f.Folios.Deposit(f.admin, f.propID, res.ID, "d1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	if d := f.summaryOf(t, res.ID).Deposit; d == nil || d.Paid != "300000" || d.FolioID != dep.Payment.FolioID {
		t.Fatalf("deposit: %+v", d)
	}
	// the company of the billing instruction of a room
	var co int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme Corp', 1000000) RETURNING id`, f.tenantID, f.propID).Scan(&co))
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, []folios.InstructionInput{{Scope: folios.ScopeRoom, CompanyID: co}})
	must(t, err)
	if got := f.summaryOf(t, res.ID).Rooms[0].BillingCompany; got != "Acme Corp" {
		t.Fatalf("billing company: %q", got)
	}
	// a reservation cancelled whole shows its rooms; one cancelled room of two is left out
	cancelled := f.book(t, f.dlx, "2026-10-12", "2026-10-13")
	_, err = f.Res.Cancel(f.admin, f.propID, cancelled.ID, cancelled.Version, "x")
	must(t, err)
	if rooms := f.summaryOf(t, cancelled.ID).Rooms; len(rooms) != 1 || rooms[0].Status != "CANCELLED" {
		t.Fatalf("cancelled reservation: %+v", rooms)
	}
}

func TestListFiltersAreAppliedByTheDatabase(t *testing.T) {
	f := setup(t)
	a := f.book(t, f.dlx, "2026-10-05", "2026-10-07")
	b := f.book(t, f.std, "2026-10-10", "2026-10-12")
	_, err := f.Res.AssignRoom(f.admin, f.propID, a.ID, a.Rooms[0].ID, a.Version, f.r102.ID, false)
	must(t, err)
	ids := func(rows []reservations.Summary) map[int64]bool {
		m := map[int64]bool{}
		for _, r := range rows {
			m[r.ID] = true
		}
		return m
	}
	only := func(rows []reservations.Summary, want ...int64) bool {
		got := ids(rows)
		if len(got) != len(want) {
			return false
		}
		for _, id := range want {
			if !got[id] {
				return false
			}
		}
		return true
	}
	from, to := d("2026-10-09"), d("2026-10-12")
	if rows := f.list(t, reservations.ListFilter{DepartureFrom: &from, DepartureTo: &to}); !only(rows, b.ID) {
		t.Fatalf("departure range: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{RoomTypeID: &f.std.ID}); !only(rows, b.ID) {
		t.Fatalf("room type: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{RatePlanID: &f.plan}); !only(rows, a.ID, b.ID) {
		t.Fatalf("rate plan: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{Query: "102"}); !only(rows, a.ID) { // the room number
		t.Fatalf("room search: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{Query: "guest"}); !only(rows, a.ID, b.ID) { // the guest, any case
		t.Fatalf("guest search: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{Query: b.ConfirmationNumber}); !only(rows, b.ID) {
		t.Fatalf("confirmation search: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{Query: "%"}); len(rows) != 0 { // a text to find, not a pattern
		t.Fatalf("a wildcard matched: %+v", rows)
	}
	if rows := f.list(t, reservations.ListFilter{DisplayStatus: "IN_HOUSE"}); len(rows) != 0 {
		t.Fatalf("in house: %+v", rows)
	}
	// the page is cut by the database: the second page continues after the first, and the filter holds on both
	page1, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{DisplayStatus: "CONFIRMED"}, 0, 1)
	must(t, err)
	page2, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{DisplayStatus: "CONFIRMED"}, page1[0].ID, 1)
	must(t, err)
	if len(page1) != 1 || len(page2) != 1 || page1[0].ID == page2[0].ID {
		t.Fatalf("pages: %+v %+v", page1, page2)
	}
}

func TestListIsScopedAndAuthorized(t *testing.T) {
	f := setup(t)
	f.book(t, f.dlx, "2026-10-05", "2026-10-07")
	ubud := f.Property(t, f.tenantID, "UBUD")
	if rows, err := f.Res.List(f.admin, ubud.ID, reservations.ListFilter{}, 0, 50); err != nil || len(rows) != 0 {
		t.Fatalf("another property: %v %+v", err, rows)
	}
	other := f.Tenant(t, "XYZ")
	stranger := roomstest.Admin(other.ID)
	if _, err := f.Res.List(stranger, f.propID, reservations.ListFilter{}, 0, 50); err == nil {
		t.Fatal("another tenant must not list")
	}
	_, err := f.Res.List(f.User(t, f.tenantID, f.propID, auth.PermGuestRead), f.propID, reservations.ListFilter{}, 0, 50)
	wantCode(t, err, "PERMISSION_DENIED")
}
