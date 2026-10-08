package frontdesk_test

import (
	"slices"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/rooms/roomstest"
)

// The arrivals and departures read models: every figure is read from the ledger, the booking snapshot or the rules the check-in applies; none is derived from a rate or guessed.

func (f *fx) arrivals(t *testing.T, filter frontdesk.ArrivalFilter) []frontdesk.Arrival {
	t.Helper()
	rows, err := f.Front.Arrivals(f.admin, f.propID, nil, filter)
	must(t, err)
	return rows
}

func TestArrivalsCarryRateCompanyDepositAndReadiness(t *testing.T) {
	f := setup(t)
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	got := f.arrivals(t, frontdesk.ArrivalFilter{})
	if len(got) != 1 {
		t.Fatalf("arrivals: %+v", got)
	}
	a := got[0]
	if a.Status != "CONFIRMED" || a.ReservationStatus != "CONFIRMED" || a.Rate.RatePlanCode != "BAR" || a.Rate.Amount != "1000000" || a.RoomTypeName == "" || a.Company != nil || a.Deposit != nil {
		t.Fatalf("arrival: %+v", a)
	}
	// no room yet: the check-in chooses one, but the list says so
	if a.Readiness.Status != "BLOCKED" || !slices.Equal(a.Readiness.Blockers, []string{"ROOM_NOT_ASSIGNED"}) {
		t.Fatalf("readiness: %+v", a.Readiness)
	}
	// a clean room assigned: ready
	_, err := f.Res.AssignRoom(f.admin, f.propID, res.ID, res.Rooms[0].ID, res.Version, f.r101.ID, false)
	must(t, err)
	if r := f.arrivals(t, frontdesk.ArrivalFilter{})[0].Readiness; r.Status != "READY" || len(r.Blockers) != 0 {
		t.Fatalf("readiness of a clean room: %+v", r)
	}
	// a dirty room, as the check-in would refuse it
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'DIRTY' WHERE room_id = $1`, f.r101.ID))
	if r := f.arrivals(t, frontdesk.ArrivalFilter{})[0].Readiness; !slices.Equal(r.Blockers, []string{"ROOM_NOT_READY"}) {
		t.Fatalf("readiness of a dirty room: %+v", r)
	}
	if _, err = f.checkIn(t, f.admin, f.reload(t, res.ID), &f.r101, ""); err == nil {
		t.Fatal("the check-in refuses what the list called a blocker")
	}
	// the inspection rule of the property is the check-in's rule
	must(t, f.Exec(t, `UPDATE room_housekeeping SET status = 'CLEAN' WHERE room_id = $1`, f.r101.ID))
	must(t, f.Exec(t, `UPDATE properties SET require_room_inspection_for_checkin = true WHERE id = $1`, f.propID))
	if r := f.arrivals(t, frontdesk.ArrivalFilter{})[0].Readiness; !slices.Equal(r.Blockers, []string{"ROOM_NOT_READY"}) {
		t.Fatalf("a clean room is not enough when an inspection is required: %+v", r)
	}
	must(t, f.Exec(t, `UPDATE properties SET require_room_inspection_for_checkin = false WHERE id = $1`, f.propID))
	// a deposit, from the ledger of its folio
	dep, err := f.Folios.Deposit(f.admin, f.propID, res.ID, "d1", folios.PaymentInput{Amount: "300000", PaymentMethod: "CASH"})
	must(t, err)
	a = f.arrivals(t, frontdesk.ArrivalFilter{})[0]
	if a.Deposit == nil || a.Deposit.Paid != "300000" || a.Deposit.FolioID != dep.Payment.FolioID {
		t.Fatalf("deposit: %+v", a.Deposit)
	}
	// the company of the billing instruction
	co := f.newCompany(t, "ACME")
	_, err = f.Folios.SetBillingInstructions(f.admin, f.propID, res.ID, res.Rooms[0].ID, f.room(co))
	must(t, err)
	if c := f.arrivals(t, frontdesk.ArrivalFilter{})[0].Company; c == nil || c.ID != co || c.Name != "ACME" {
		t.Fatalf("company: %+v", c)
	}
}

func TestArrivalReadinessOfAnOccupiedRoomAndAnotherDate(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101.ID, "2026-10-03") // 101 is in use
	res := f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	must(t, f.Exec(t, `UPDATE reservation_rooms SET room_id = $1 WHERE id = $2`, f.r101.ID, res.Rooms[0].ID))
	if r := f.arrivals(t, frontdesk.ArrivalFilter{})[0].Readiness; !slices.Contains(r.Blockers, "ROOM_OCCUPIED") {
		t.Fatalf("an occupied room: %+v", r)
	}
	// another date than the business date: the list can be read, the check-in cannot be done
	later := f.book(t, f.std, "2026-10-05", "2026-10-06")
	day := d("2026-10-05")
	rows, err := f.Front.Arrivals(f.admin, f.propID, &day, frontdesk.ArrivalFilter{})
	must(t, err)
	if len(rows) != 1 || rows[0].ReservationID != later.ID || !slices.Contains(rows[0].Readiness.Blockers, "NOT_BUSINESS_DATE") {
		t.Fatalf("arrivals of the 5th: %+v", rows)
	}
}

func TestArrivalFiltersAreAppliedByTheServer(t *testing.T) {
	f := setup(t)
	f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	std := f.book(t, f.std, "2026-09-30", "2026-10-01")
	if got := f.arrivals(t, frontdesk.ArrivalFilter{RoomTypeID: &f.std.ID}); len(got) != 1 || got[0].ReservationID != std.ID {
		t.Fatalf("room type: %+v", got)
	}
	if got := f.arrivals(t, frontdesk.ArrivalFilter{Q: std.ConfirmationNumber[len(std.ConfirmationNumber)-4:]}); len(got) < 1 {
		t.Fatalf("confirmation search: %+v", got)
	}
	if got := f.arrivals(t, frontdesk.ArrivalFilter{Q: "guest"}); len(got) != 2 { // the last name, any case
		t.Fatalf("guest search: %+v", got)
	}
	if got := f.arrivals(t, frontdesk.ArrivalFilter{Q: "100%"}); len(got) != 0 { // a text to find, not a pattern
		t.Fatalf("wildcards must not match: %+v", got)
	}
	if got := f.arrivals(t, frontdesk.ArrivalFilter{Status: "CHECKED_IN"}); len(got) != 0 {
		t.Fatalf("checked in: %+v", got)
	}
	_, err := f.checkIn(t, f.admin, f.reload(t, std.ID), &f.r201, "")
	must(t, err)
	got := f.arrivals(t, frontdesk.ArrivalFilter{Status: "CHECKED_IN"})
	if len(got) != 1 || got[0].ReservationID != std.ID || got[0].Readiness.Status != "NONE" {
		t.Fatalf("a checked-in arrival keeps its place under CHECKED_IN: %+v", got)
	}
	if got := f.arrivals(t, frontdesk.ArrivalFilter{}); len(got) != 1 {
		t.Fatalf("confirmed only: %+v", got)
	}
	_, err = f.Front.Arrivals(f.admin, f.propID, nil, frontdesk.ArrivalFilter{Status: "DRAFT"})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestArrivalsAreScopedAndAuthorized(t *testing.T) {
	f := setup(t)
	f.book(t, f.dlx, "2026-09-30", "2026-10-02")
	ubud := f.Property(t, f.tenantID, "UBUD")
	rows, err := f.Front.Arrivals(f.admin, ubud.ID, nil, frontdesk.ArrivalFilter{})
	if err == nil && len(rows) != 0 {
		t.Fatalf("another property: %+v", rows)
	}
	other := f.Tenant(t, "XYZ")
	stranger, _ := f.AdminAccount(t, other.ID)
	if _, err := f.Front.Arrivals(stranger, f.propID, nil, frontdesk.ArrivalFilter{}); err == nil {
		t.Fatal("another tenant must not read the arrivals")
	}
	_, err = f.Front.Arrivals(f.User(t, f.tenantID, f.propID, auth.PermGuestRead), f.propID, nil, frontdesk.ArrivalFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestDeparturesAreTheInHouseListThatLeavesByADate(t *testing.T) {
	f := setup(t)
	a := f.stay(t, f.r101.ID, "2026-10-01")
	_, err := f.checkIn(t, f.admin, f.reload(t, f.book(t, f.std, "2026-09-30", "2026-10-03").ID), &f.r201, "")
	must(t, err)
	until, onDay := d("2026-10-01"), d("2026-10-03")
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{DepartureUntil: &until}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].ID != a.Stay.ID {
		t.Fatalf("leaving by 1 Oct: %+v", rows)
	}
	rows, err = f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{DepartureDate: &onDay}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].Room.Number != "201" {
		t.Fatalf("leaving on 3 Oct: %+v", rows)
	}
	rows, err = f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{RoomTypeID: &f.std.ID}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].Room.RoomTypeCode != "STD" {
		t.Fatalf("room type: %+v", rows)
	}
	rows, err = f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{Q: "101"}, 0, 50)
	must(t, err)
	if len(rows) != 1 || rows[0].ID != a.Stay.ID {
		t.Fatalf("room search: %+v", rows)
	}
}

func TestCheckoutStatusFollowsTheFoliosAndNeverTheRate(t *testing.T) {
	// a stay whose last night is not charged yet: the folio will change, so zero is not "ready"
	f := setup(t)
	st := f.stay(t, f.r101.ID, "2026-10-01")
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	if rows[0].Balance.Amount != "0" || rows[0].Checkout.Status != "CHARGES_PENDING" || rows[0].Checkout.UnchargedNights != 1 {
		t.Fatalf("an uncharged night: %+v %+v", rows[0].Balance, rows[0].Checkout)
	}
	_, err = f.Charges.PostManual(f.admin, f.propID, roomstest.BD, nil)
	must(t, err)
	rows, _ = f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	if rows[0].Checkout.Status != "BALANCE_DUE" || rows[0].Checkout.UnchargedNights != 0 || rows[0].Balance.Status != "OUTSTANDING" {
		t.Fatalf("a charged night is owed: %+v %+v", rows[0].Balance, rows[0].Checkout)
	}
	pay, err := f.Folios.PostPayment(f.admin, f.propID, rows[0].Balance.Folios[0].ID, "p1", folios.PaymentInput{Amount: rows[0].Balance.Amount, PaymentMethod: "CASH"})
	must(t, err)
	_ = pay
	rows, _ = f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	if rows[0].Checkout.Status != "READY" || rows[0].Balance.Status != "SETTLED" || rows[0].Balance.Amount != "0" {
		t.Fatalf("settled: %+v %+v", rows[0].Balance, rows[0].Checkout)
	}
	_ = st
}

func TestCheckoutStatusWithACompanyFolio(t *testing.T) {
	s := withCompanyFolio(t)
	_, err := s.Charges.PostManual(s.admin, s.propID, roomstest.BD, nil) // the night goes to the company by the instruction ROOM
	must(t, err)
	rows, err := s.Front.ListInHouse(s.admin, s.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	r := rows[0]
	if r.Checkout.Status != "COMPANY_BILL" || r.Company == nil || len(r.Balance.Folios) != 2 {
		t.Fatalf("company bill: %+v %+v", r.Checkout, r.Balance)
	}
	// the total counts each folio once, and each folio is given with its own balance from the ledger
	total := decimal.Zero
	for _, fo := range r.Balance.Folios {
		got := decimal.RequireFromString(fo.Balance)
		if !got.Equal(decimal.RequireFromString(s.balance(t, fo.ID))) {
			t.Fatalf("folio %d: %s", fo.ID, fo.Balance)
		}
		total = total.Add(got)
	}
	if !total.Equal(decimal.RequireFromString(r.Balance.Amount)) || r.Balance.Folios[0].FolioType != "GUEST" || !decimal.RequireFromString(r.Balance.Folios[0].Balance).IsZero() {
		t.Fatalf("the night is on the company folio only: %+v", r.Balance)
	}
}

func TestAStayWithoutAFolioHasNoBalance(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101.ID, "2026-10-03")
	must(t, f.Exec(t, `UPDATE folios SET stay_id = NULL WHERE stay_id IS NOT NULL`))
	rows, err := f.Front.ListInHouse(f.admin, f.propID, frontdesk.InHouseFilter{}, 0, 50)
	must(t, err)
	b := rows[0].Balance
	if b.Status != "NO_FOLIO" || b.Amount != "" || len(b.Folios) != 0 || rows[0].Checkout.Status != "FOLIO_ISSUE" {
		t.Fatalf("no folio is not zero: %+v %+v", b, rows[0].Checkout)
	}
}
