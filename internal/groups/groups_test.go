package groups_test

import (
	"context"
	"os"
	"sync"
	"testing"

	"kamarapms/internal/companies"
	"kamarapms/internal/groups"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func d(s string) civil.Date { return civil.MustParseDate(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	dlx              rooms.RoomType
	plan, guest      int64
	acme             companies.Company
}

// setup: a property on 30 Sep 2026 with a DLX room type of 10 rooms at 1,000,000 and ACME.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, _ := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin}
	f.dlx = e.RoomType(t, admin, p.ID, "DLX")
	for _, n := range []string{"101", "102", "103", "104", "105", "106", "107", "108", "109", "110"} {
		e.Room(t, admin, p.ID, f.dlx.ID, n)
	}
	var chargeID int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&f.plan))
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Guest', $2) RETURNING id`, tn.ID, p.ID).Scan(&f.guest))
	_, err := e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-12-01"), Amount: "1000000"})
	must(t, err)
	f.acme, err = e.Companies.Create(admin, p.ID, companies.Input{Code: "ACME", Name: "Acme", PaymentTermsDays: 30, IsActive: true})
	must(t, err)
	return f
}

func (f *fx) group(t *testing.T, code string, company *int64) groups.Group {
	t.Helper()
	g, err := f.Groups.Create(f.admin, f.propID, groups.Input{Code: code, Name: code + " conference", CompanyID: company, ArrivalDate: d("2026-10-10"), DepartureDate: d("2026-10-14"), IsActive: true})
	must(t, err)
	return g
}

func (f *fx) book(arrival, departure string, company, group *int64) (reservations.Reservation, error) {
	return f.Res.Create(f.admin, f.propID, "", reservations.CreateInput{
		GuestID: &f.guest, Source: "PHONE", CompanyID: company, BookingGroupID: group, Confirm: true,
		Rooms: []reservations.LineInput{{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d(arrival), Departure: d(departure), Adults: 2}},
	})
}

func TestGroupValidationAndRules(t *testing.T) {
	f := setup(t)
	g := f.group(t, "CONF", &f.acme.ID)
	if g.CompanyName != "Acme" || g.ReservationCount != 0 || !g.IsActive {
		t.Fatalf("group: %+v", g)
	}
	_, err := f.Groups.Create(f.admin, f.propID, groups.Input{Code: "conf", Name: "Again", ArrivalDate: d("2026-10-10"), DepartureDate: d("2026-10-11"), IsActive: true})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.Groups.Create(f.admin, f.propID, groups.Input{Code: "BAD", Name: "Dates", ArrivalDate: d("2026-10-10"), DepartureDate: d("2026-10-10"), IsActive: true})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Groups.Create(f.admin, f.propID, groups.Input{Code: "NOCO", Name: "x", CompanyID: ptr(int64(9999)), ArrivalDate: d("2026-10-10"), DepartureDate: d("2026-10-11"), IsActive: true})
	wantCode(t, err, "COMPANY_NOT_FOUND")
	inactive, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "OLD", Name: "Old", IsActive: false})
	must(t, err)
	_, err = f.Groups.Create(f.admin, f.propID, groups.Input{Code: "INACT", Name: "x", CompanyID: &inactive.ID, ArrivalDate: d("2026-10-10"), DepartureDate: d("2026-10-11"), IsActive: true})
	wantCode(t, err, "COMPANY_INACTIVE")
	_, err = f.Groups.Get(f.admin, f.propID, 9999)
	wantCode(t, err, "GROUP_NOT_FOUND")

	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	if _, err := f.Groups.Get(reader, f.propID, g.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.Groups.Create(reader, f.propID, groups.Input{Code: "NOPE", Name: "x", ArrivalDate: d("2026-10-10"), DepartureDate: d("2026-10-11"), IsActive: true})
	wantCode(t, err, "PERMISSION_DENIED")
	tn2 := f.Tenant(t, "XYZ")
	f.Property(t, tn2.ID, "OTHER")
	adm2, _ := f.AdminAccount(t, tn2.ID)
	_, err = f.Groups.Get(adm2, f.propID, g.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func ptr[T any](v T) *T { return &v }

func TestReservationsJoinTheGroup(t *testing.T) {
	f := setup(t)
	g := f.group(t, "CONF", &f.acme.ID)
	// the group's company is inherited
	r, err := f.book("2026-10-10", "2026-10-12", nil, &g.ID)
	must(t, err)
	if r.BookingGroupID == nil || *r.BookingGroupID != g.ID || r.CompanyID == nil || *r.CompanyID != f.acme.ID || r.GroupCode != "CONF" || r.CompanyName != "Acme" {
		t.Fatalf("reservation: %+v", r)
	}
	// dates must lie inside the group's
	_, err = f.book("2026-10-09", "2026-10-12", nil, &g.ID)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.book("2026-10-12", "2026-10-15", nil, &g.ID)
	wantCode(t, err, "VALIDATION_FAILED")
	// another company than the group's is refused, an unknown group or company too
	other, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "OTHER", Name: "Other", IsActive: true})
	must(t, err)
	_, err = f.book("2026-10-10", "2026-10-12", &other.ID, &g.ID)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.book("2026-10-10", "2026-10-12", nil, ptr(int64(9999)))
	wantCode(t, err, "GROUP_NOT_FOUND")
	_, err = f.book("2026-10-10", "2026-10-12", ptr(int64(9999)), nil)
	wantCode(t, err, "COMPANY_NOT_FOUND")

	// a company without a group, then the group's totals and members
	solo, err := f.book("2026-10-20", "2026-10-21", &other.ID, nil)
	must(t, err)
	if solo.CompanyID == nil || solo.BookingGroupID != nil {
		t.Fatalf("solo: %+v", solo)
	}
	r2, err := f.book("2026-10-11", "2026-10-13", nil, &g.ID)
	must(t, err)
	g2, err := f.Groups.Get(f.admin, f.propID, g.ID)
	must(t, err)
	members, err := f.Groups.Members(f.admin, f.propID, g.ID)
	must(t, err)
	if g2.ReservationCount != 2 || g2.RoomCount != 2 || len(members) != 2 || members[0].ReservationID != r.ID || members[1].ReservationID != r2.ID || members[0].GuestName == "" {
		t.Fatalf("group %+v members %+v", g2, members)
	}

	// list filters
	byGroup, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{GroupID: &g.ID}, 0, 10)
	must(t, err)
	byCompany, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{CompanyID: &other.ID}, 0, 10)
	must(t, err)
	if len(byGroup) != 2 || byGroup[0].GroupCode != "CONF" || byGroup[0].CompanyName != "Acme" || len(byCompany) != 1 || byCompany[0].ID != solo.ID {
		t.Fatalf("lists: %+v / %+v", byGroup, byCompany)
	}

	// patching: attach the solo reservation to the group (outside its dates: refused), clear the group and company
	_, err = f.Res.UpdateHeader(f.admin, f.propID, solo.ID, reservations.HeaderPatch{Version: solo.Version, BookingGroupID: &g.ID})
	wantCode(t, err, "VALIDATION_FAILED")
	cleared, err := f.Res.UpdateHeader(f.admin, f.propID, r.ID, reservations.HeaderPatch{Version: r.Version, BookingGroupID: ptr(int64(0)), CompanyID: ptr(int64(0))})
	must(t, err)
	if cleared.BookingGroupID != nil || cleared.CompanyID != nil {
		t.Fatalf("cleared: %+v", cleared)
	}
	back, err := f.Res.UpdateHeader(f.admin, f.propID, r.ID, reservations.HeaderPatch{Version: cleared.Version, BookingGroupID: &g.ID})
	must(t, err)
	if back.BookingGroupID == nil || back.CompanyID == nil || *back.CompanyID != f.acme.ID {
		t.Fatalf("attached again: %+v", back)
	}

	// an inactive group takes no more rooms
	off := false
	_, err = f.Groups.Update(f.admin, f.propID, g.ID, groups.Patch{IsActive: &off})
	must(t, err)
	_, err = f.book("2026-10-10", "2026-10-12", nil, &g.ID)
	wantCode(t, err, "GROUP_INACTIVE")
}

func TestGroupUpdateGuardsItsReservations(t *testing.T) {
	f := setup(t)
	g := f.group(t, "CONF", &f.acme.ID)
	_, err := f.book("2026-10-10", "2026-10-14", nil, &g.ID)
	must(t, err)
	// the dates cannot shrink past the room, but can grow
	_, err = f.Groups.Update(f.admin, f.propID, g.ID, groups.Patch{DepartureDate: ptr(d("2026-10-13"))})
	wantCode(t, err, "GROUP_HAS_ROOMS_OUTSIDE_DATES")
	got, err := f.Groups.Update(f.admin, f.propID, g.ID, groups.Patch{DepartureDate: ptr(d("2026-10-16"))})
	must(t, err)
	if got.DepartureDate != d("2026-10-16") {
		t.Fatalf("group: %+v", got)
	}
	// the company cannot change while a reservation bills the old one
	other, err := f.Companies.Create(f.admin, f.propID, companies.Input{Code: "OTHER", Name: "Other", IsActive: true})
	must(t, err)
	_, err = f.Groups.Update(f.admin, f.propID, g.ID, groups.Patch{CompanyID: &other.ID})
	wantCode(t, err, "GROUP_HAS_RESERVATIONS")
	// but a group without reservations can be moved or lose its company
	empty := f.group(t, "EMPTY", nil)
	moved, err := f.Groups.Update(f.admin, f.propID, empty.ID, groups.Patch{CompanyID: &other.ID})
	must(t, err)
	if moved.CompanyID == nil || *moved.CompanyID != other.ID {
		t.Fatalf("moved: %+v", moved)
	}
	none, err := f.Groups.Update(f.admin, f.propID, empty.ID, groups.Patch{CompanyID: ptr(int64(0))})
	must(t, err)
	if none.CompanyID != nil {
		t.Fatalf("company removed: %+v", none)
	}
}

// Shrinking a group while rooms are booked into it: whatever the order, no room ends up outside the dates.
func TestShrinkingAGroupRacesBookings(t *testing.T) {
	f := setup(t)
	g := f.group(t, "CONF", nil)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = f.book("2026-10-13", "2026-10-14", nil, &g.ID)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = f.Groups.Update(f.admin, f.propID, g.ID, groups.Patch{DepartureDate: ptr(d("2026-10-13"))})
	}()
	wg.Wait()
	if n := f.Count(t, `SELECT count(*) FROM reservation_rooms l JOIN reservations r ON r.id = l.reservation_id JOIN booking_groups b ON b.id = r.booking_group_id
		WHERE l.departure_date > b.departure_date OR l.arrival_date < b.arrival_date`); n != 0 {
		t.Fatalf("%d rooms outside the group's dates", n)
	}
}
