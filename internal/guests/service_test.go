package guests_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/guests"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

type fixture struct {
	*roomstest.Env
	tenantID       int64
	bali, jkt      int64
	admin          context.Context
	baliT, jktType int64
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	bali := e.Property(t, tn.ID, "BALI")
	jkt := e.Property(t, tn.ID, "JKT")
	admin := roomstest.Admin(tn.ID)
	return fixture{
		Env: e, tenantID: tn.ID, bali: bali.ID, jkt: jkt.ID, admin: admin,
		baliT: e.RoomType(t, admin, bali.ID, "DLX").ID, jktType: e.RoomType(t, admin, jkt.ID, "DLX").ID,
	}
}

func (f fixture) guest(t *testing.T, ctx context.Context, origin int64, first, last string) guests.Guest {
	t.Helper()
	r, err := f.Guests.Create(ctx, origin, guests.Profile{FirstName: first, LastName: last})
	if err != nil {
		t.Fatal(err)
	}
	return r.Guest
}

// link makes a guest a reservation booker at a property (through a CONFIRMED line).
func (f fixture) link(t *testing.T, propertyID, typeID, guestID int64) {
	t.Helper()
	line := f.Line(t, f.tenantID, propertyID, typeID, 0, "2026-10-05", "2026-10-07", "CONFIRMED")
	if err := f.Exec(t, `UPDATE reservations SET guest_id = $1 WHERE id = (SELECT reservation_id FROM reservation_rooms WHERE id = $2)`, guestID, line); err != nil {
		t.Fatal(err)
	}
}

func names(list []guests.Guest) string {
	var n []string
	for _, g := range list {
		n = append(n, g.FirstName+" "+g.LastName)
	}
	return strings.Join(n, ", ")
}

func (f fixture) search(t *testing.T, ctx context.Context, q string, propertyID *int64) []guests.Guest {
	t.Helper()
	res, err := f.Guests.Search(ctx, q, propertyID, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	return res.Guests
}

func TestCreateNumbersAreGaplessAndValidated(t *testing.T) {
	f := newFixture(t)
	a := f.guest(t, f.admin, f.bali, "Ann", "Lee")
	b := f.guest(t, f.admin, f.jkt, "Bob", "Kim")
	if a.Code != "GST000001" || b.Code != "GST000002" {
		t.Fatalf("codes %s %s", a.Code, b.Code)
	}
	// A failed request returns its number: the increment is part of the business transaction.
	err := f.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
		ctx = auth.WithPrincipal(ctx, auth.Principal{TenantID: f.tenantID, IsTenantAdmin: true})
		if _, err := f.Guests.Create(ctx, f.bali, guests.Profile{LastName: "Rolled back"}); err != nil {
			return err
		}
		return context.Canceled
	})
	if err == nil {
		t.Fatal("expected the outer error")
	}
	if c := f.guest(t, f.admin, f.bali, "Cy", "Wu"); c.Code != "GST000003" {
		t.Fatalf("rolled-back number must be reused, got %s", c.Code)
	}

	_, err = f.Guests.Create(f.admin, f.bali, guests.Profile{LastName: "", Email: "bad"})
	fields := map[string]bool{}
	for _, fe := range code(t, err, "VALIDATION_FAILED").Fields {
		fields[fe.Field] = true
	}
	if !fields["last_name"] || !fields["email"] {
		t.Fatalf("fields: %v", fields)
	}
	if n := f.Count(t, `SELECT count(*) FROM guests`); n != 3 {
		t.Fatalf("guests: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'guest.created'`); n != 3 {
		t.Fatalf("audit entries: %d", n)
	}
}

func TestConcurrentCreatesGetDistinctGaplessNumbers(t *testing.T) {
	f := newFixture(t)
	const n = 8
	var wg sync.WaitGroup
	start := make(chan struct{})
	codes := make([]string, n)
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, err := f.Guests.Create(f.admin, f.bali, guests.Profile{LastName: fmt.Sprintf("G%d", i)})
			codes[i], errs[i] = r.Guest.Code, err
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(codes)
	for i, c := range codes {
		if want := fmt.Sprintf("GST%06d", i+1); c != want {
			t.Fatalf("codes %v: position %d is %s, want %s", codes, i, c, want)
		}
	}
}

func TestSearchFindsByNameEmailPhoneIDAndCode(t *testing.T) {
	f := newFixture(t)
	r, err := f.Guests.Create(f.admin, f.bali, guests.Profile{
		FirstName: "Siti", LastName: "Nurhaliza", Email: "siti.n@mail.com", Phone: "+62 812-3456-7890", IDType: "KTP", IDNumber: "3171234567890001",
	})
	if err != nil {
		t.Fatal(err)
	}
	f.guest(t, f.admin, f.bali, "Budi", "Santoso")
	f.guest(t, f.admin, f.bali, "Siska", "Dewi")

	for q, want := range map[string]string{
		"nurha":                       "Siti Nurhaliza",
		"siti nur":                    "Siti Nurhaliza", // every token must match
		"SITI.N@":                     "Siti Nurhaliza",
		"81234":                       "Siti Nurhaliza", // digits inside the phone number
		"3171234":                     "Siti Nurhaliza",
		strings.ToLower(r.Guest.Code): "Siti Nurhaliza",
		"sis":                         "Siska Dewi",
		"nomatch":                     "",
		"siti santoso":                "",
	} {
		if got := names(f.search(t, f.admin, q, nil)); got != want {
			t.Errorf("search %q = %q, want %q", q, got, want)
		}
	}
	// A short digit token does not match phone numbers (too many false hits).
	if got := f.search(t, f.admin, "78", nil); len(got) != 0 {
		t.Errorf("short digit token matched %s", names(got))
	}
	// LIKE wildcards in the query are literals.
	if got := f.search(t, f.admin, "%", nil); len(got) != 0 {
		t.Errorf("'%%' matched %s", names(got))
	}
	// An empty query lists everything alphabetically, and paging walks it without gaps.
	all := f.search(t, f.admin, "", nil)
	if names(all) != "Siska Dewi, Siti Nurhaliza, Budi Santoso" {
		t.Fatalf("alphabetical by last name: %s", names(all))
	}
	p1, err := f.Guests.Search(f.admin, "", nil, nil, 2)
	if err != nil || len(p1.Guests) != 2 || p1.Next == nil {
		t.Fatalf("page 1: %v %+v", err, p1)
	}
	p2, err := f.Guests.Search(f.admin, "", nil, p1.Next, 2)
	if err != nil || len(p2.Guests) != 1 || p2.Next != nil || p2.Guests[0].LastName != "Santoso" {
		t.Fatalf("page 2: %v %+v", err, p2)
	}
}

func TestVisibilityWithAndWithoutSearchAll(t *testing.T) {
	f := newFixture(t)
	baliGuest := f.guest(t, f.admin, f.bali, "Ann", "Bali")
	jktGuest := f.guest(t, f.admin, f.jkt, "Bob", "Jakarta")
	visitor := f.guest(t, f.admin, f.jkt, "Cy", "Visitor")
	f.link(t, f.bali, f.baliT, visitor.ID) // a Jakarta-origin guest who booked at Bali

	reader := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead}})
	if got := names(f.search(t, reader, "", nil)); got != "Ann Bali, Cy Visitor" {
		t.Fatalf("linked by origin or reservation only: %q", got)
	}
	if _, err := f.Guests.Get(reader, baliGuest.ID); err != nil {
		t.Fatal(err)
	}
	_, err := f.Guests.Get(reader, jktGuest.ID)
	wantCode(t, err, "GUEST_NOT_FOUND") // not "forbidden": it does not exist for this user
	// The same profile is reachable through either property it is linked to.
	if _, err := f.Guests.Get(reader, visitor.ID); err != nil {
		t.Fatal(err)
	}

	// guest.search_all widens the view to the whole tenant.
	searcher := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead, auth.PermGuestSearchAll}})
	if got := names(f.search(t, searcher, "", nil)); got != "Ann Bali,  Guest, Bob Jakarta, Cy Visitor" {
		t.Fatalf("search_all: %q", got)
	}
	if _, err := f.Guests.Get(searcher, jktGuest.ID); err != nil {
		t.Fatal(err)
	}
	// ...but search_all alone does not allow reading without guest.read.
	blind := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestSearchAll}})
	_, err = f.Guests.Search(blind, "", nil, nil, 10)
	wantCode(t, err, "PERMISSION_DENIED")

	// The property_id filter evaluates permissions at that property only.
	both := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{
		f.bali: {auth.PermGuestRead}, f.jkt: {auth.PermGuestRead, auth.PermGuestSearchAll},
	})
	if got := names(f.search(t, both, "", &f.bali)); got != "Ann Bali, Cy Visitor" {
		t.Fatalf("scoped to Bali: %q", got)
	}
	if got := names(f.search(t, both, "", &f.jkt)); got != "Ann Bali,  Guest, Bob Jakarta, Cy Visitor" {
		t.Fatalf("scoped to Jakarta: %q", got)
	}
	other := f.Property(t, f.tenantID, "SBY")
	_, err = f.Guests.Search(both, "", &other.ID, nil, 10) // no grant there
	wantCode(t, err, "PROPERTY_NOT_FOUND")

	// Another tenant sees nothing and cannot address these ids.
	xyz := f.Tenant(t, "XYZ")
	sg := f.Property(t, xyz.ID, "SG")
	foreign := roomstest.Admin(xyz.ID)
	if got := f.search(t, foreign, "", nil); len(got) != 0 {
		t.Fatalf("other tenant sees %s", names(got))
	}
	_, err = f.Guests.Get(foreign, baliGuest.ID)
	wantCode(t, err, "GUEST_NOT_FOUND")
	_, err = f.Guests.Create(foreign, f.bali, guests.Profile{LastName: "X"}) // origin property of another tenant
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if _, err := f.Guests.Create(foreign, sg.ID, guests.Profile{LastName: "Own"}); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicateHintsRespectVisibility(t *testing.T) {
	f := newFixture(t)
	first, err := f.Guests.Create(f.admin, f.jkt, guests.Profile{FirstName: "Ann", LastName: "Lee", Email: "ann@x.com", Phone: "0812 111 222"})
	if err != nil {
		t.Fatal(err)
	}
	writer := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead, auth.PermGuestWrite}})
	// The look-alike lives at another property the writer cannot see: they get a count, not the profile.
	r, err := f.Guests.Create(writer, f.bali, guests.Profile{FirstName: "Ann", LastName: "Lee", Email: "ANN@x.com"})
	if err != nil {
		t.Fatalf("duplicates never block: %v", err)
	}
	if len(r.PossibleDuplicates) != 0 || r.HiddenDuplicateCount != 1 {
		t.Fatalf("hints: %+v hidden=%d", r.PossibleDuplicates, r.HiddenDuplicateCount)
	}
	// An administrator sees the profile and why it matches.
	r, err = f.Guests.Create(f.admin, f.bali, guests.Profile{FirstName: "A.", LastName: "Lee", Phone: "0812-111-222", Email: "ann@x.com"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.PossibleDuplicates) != 2 || r.HiddenDuplicateCount != 0 {
		t.Fatalf("admin hints: %+v", r.PossibleDuplicates)
	}
	var reasons []string
	for _, d := range r.PossibleDuplicates {
		if d.Guest.ID == first.Guest.ID {
			reasons = d.Reasons
		}
	}
	slices.Sort(reasons)
	if !slices.Equal(reasons, []string{guests.ReasonEmail, guests.ReasonPhone}) {
		t.Fatalf("reasons: %v", reasons)
	}
	// Unrelated people are not hinted.
	r, _ = f.Guests.Create(f.admin, f.bali, guests.Profile{LastName: "Zed"})
	if len(r.PossibleDuplicates) != 0 {
		t.Fatalf("unrelated: %+v", r.PossibleDuplicates)
	}
}

func TestEditNeedsWriteAtALinkedProperty(t *testing.T) {
	f := newFixture(t)
	jktGuest := f.guest(t, f.admin, f.jkt, "Bob", "Jakarta")
	baliGuest, err := f.Guests.Create(f.admin, f.bali, guests.Profile{FirstName: "Ann", LastName: "Bali", IDType: "PASSPORT", IDNumber: "X1234567"})
	if err != nil {
		t.Fatal(err)
	}
	id := baliGuest.Guest.ID
	city := "Ubud"

	reader := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead}})
	v, err := f.Guests.Get(reader, id)
	if err != nil || v.CanEdit {
		t.Fatalf("read-only user: %v canEdit=%v", err, v.CanEdit)
	}
	_, err = f.Guests.Update(reader, id, guests.Patch{City: &city})
	wantCode(t, err, "PERMISSION_DENIED")

	// guest.write at a property where the guest is NOT linked is not enough...
	wrongSite := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{
		f.bali: {auth.PermGuestRead}, f.jkt: {auth.PermGuestWrite, auth.PermGuestRead},
	})
	_, err = f.Guests.Update(wrongSite, id, guests.Patch{City: &city})
	wantCode(t, err, "PERMISSION_DENIED")
	// ...until the guest is linked there (a reservation at that property).
	f.link(t, f.jkt, f.jktType, id)
	upd, err := f.Guests.Update(wrongSite, id, guests.Patch{City: &city})
	if err != nil || upd.City != "Ubud" || !upd.CanEdit {
		t.Fatalf("linked write: %v %+v", err, upd)
	}

	// Invisible guests cannot be edited (and do not reveal themselves).
	baliWriter := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead, auth.PermGuestWrite}})
	_, err = f.Guests.Update(baliWriter, jktGuest.ID, guests.Patch{City: &city})
	wantCode(t, err, "GUEST_NOT_FOUND")

	// Patch semantics: nil keeps, empty clears, validation applies to the merged profile.
	empty := ""
	upd, err = f.Guests.Update(f.admin, id, guests.Patch{City: &empty, FirstName: new(string)})
	if err != nil || upd.City != "" || upd.FirstName != "" || upd.LastName != "Bali" || upd.IDNumber != "X1234567" {
		t.Fatalf("patch: %v %+v", err, upd)
	}
	_, err = f.Guests.Update(f.admin, id, guests.Patch{IDType: &empty}) // id_number would be left without a type
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.Guests.Update(f.admin, 999999, guests.Patch{City: &city})
	wantCode(t, err, "GUEST_NOT_FOUND")

	// The audit trail records every change but never the full ID number.
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'guest.updated' AND entity_id = $1`, id); n != 2 {
		t.Fatalf("update audit entries: %d", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'guest' AND (new_data::text LIKE '%X1234567%' OR old_data::text LIKE '%X1234567%')`); n != 0 {
		t.Fatalf("ID number leaked into %d audit entries", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE entity_type = 'guest' AND new_data::text LIKE '%****4567%'`); n == 0 {
		t.Fatal("the masked ID number should be audited")
	}
}

func TestConcurrentUpdatesOfOneGuestSerialise(t *testing.T) {
	f := newFixture(t)
	g := f.guest(t, f.admin, f.bali, "Ann", "Lee")
	const n = 6
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			city := fmt.Sprintf("City%d", i)
			_, errs[i] = f.Guests.Update(f.admin, g.ID, guests.Patch{City: &city})
		}()
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if c := f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'guest.updated'`); c != n {
		t.Fatalf("every update is audited once: %d", c)
	}
}

func TestHistoryFiltersByPropertyAndCountsHidden(t *testing.T) {
	f := newFixture(t)
	g := f.guest(t, f.admin, f.bali, "Ann", "Lee")
	baliRoom := f.Room(t, f.admin, f.bali, f.baliT, "101")
	jktRoom := f.Room(t, f.admin, f.jkt, f.jktType, "201")
	f.link(t, f.bali, f.baliT, g.ID)
	f.link(t, f.jkt, f.jktType, g.ID)
	stay := f.Stay(t, f.tenantID, f.jkt, f.jktType, jktRoom.ID, "2026-09-28", "2026-10-01")
	if err := f.Exec(t, `UPDATE stays SET guest_id = $1 WHERE id = $2`, g.ID, stay); err != nil {
		t.Fatal(err)
	}
	_ = baliRoom

	reader := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead}})
	h, err := f.Guests.History(reader, g.ID, 0, 50)
	if err != nil || len(h.Items) != 1 || h.Items[0].PropertyCode != "BALI" || h.Items[0].Type != "RESERVATION" || h.Items[0].Role != "BOOKER" || h.HiddenCount != 2 {
		t.Fatalf("own property only: %v %+v hidden=%d", err, h.Items, h.HiddenCount)
	}
	wide := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead, auth.PermGuestHistoryAllProperties}})
	h, err = f.Guests.History(wide, g.ID, 0, 50)
	if err != nil || len(h.Items) != 3 || h.HiddenCount != 0 {
		t.Fatalf("history_all_properties: %v %d hidden=%d", err, len(h.Items), h.HiddenCount)
	}
	kinds := map[string]int{}
	for _, it := range h.Items {
		kinds[it.Type+"/"+it.Role]++
	}
	if kinds["RESERVATION/BOOKER"] != 2 || kinds["STAY/PRIMARY"] != 1 {
		t.Fatalf("kinds: %v", kinds)
	}
	if h, _ := f.Guests.History(f.admin, g.ID, 0, 50); len(h.Items) != 3 || h.HiddenCount != 0 {
		t.Fatalf("admin: %d hidden=%d", len(h.Items), h.HiddenCount)
	}
	if h, _ := f.Guests.History(wide, g.ID, 1, 1); len(h.Items) != 1 {
		t.Fatalf("offset paging: %d", len(h.Items))
	}
	// An accompanying guest's history shows the stay too.
	comp := f.guest(t, f.admin, f.jkt, "Cy", "Companion")
	if err := f.Exec(t, `INSERT INTO stay_guests (tenant_id, property_id, stay_id, guest_id) VALUES ($1, $2, $3, $4)`, f.tenantID, f.jkt, stay, comp.ID); err != nil {
		t.Fatal(err)
	}
	if h, _ := f.Guests.History(f.admin, comp.ID, 0, 50); len(h.Items) != 1 || h.Items[0].Role != "ACCOMPANYING" {
		t.Fatalf("companion: %+v", h.Items)
	}
	// Invisible guest: no history either.
	blind := f.UserAt(t, f.tenantID, map[int64][]auth.Permission{f.bali: {auth.PermGuestRead}})
	_, err = f.Guests.History(blind, comp.ID, 0, 10)
	wantCode(t, err, "GUEST_NOT_FOUND")
}
