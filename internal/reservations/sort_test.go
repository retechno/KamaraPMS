package reservations_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

// advance closes the business day and opens the next one, n times (the step of the night audit, nothing else); it returns the business date that is open then.
func (f *fx) advance(t *testing.T, from civil.Date, n int) civil.Date {
	t.Helper()
	bd := from
	for i := 0; i < n; i++ {
		f.Clock.Advance(24 * time.Hour) // the day cannot be closed before its end
		prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
		must(t, err)
		current := bd
		must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
			_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, current, nil, json.RawMessage(`{}`))
			return err
		}))
		bd = bd.AddDays(1)
	}
	return bd
}

// sortReservations books reservations that are alike on purpose: the same arrival, the same rate, the same status; one without a guest (NULL), one without a price (NULL), one cancelled; and,
// after the business day has moved on to 2026-10-02, two that arrived before it. In the default order ("nearest first") they are: c, d, e, f, g (on or after the 2nd, soonest first, the number
// breaking the tie of d and e), then a, b (before it, the most recent first).
func (f *fx) sortReservations(t *testing.T) []reservations.Summary {
	t.Helper()
	for _, number := range []string{"202", "203", "204", "205"} { // enough rooms for the bookings that overlap
		f.Room(t, f.admin, f.propID, f.std.ID, number)
	}
	for _, number := range []string{"103", "104"} {
		f.Room(t, f.admin, f.propID, f.dlx.ID, number)
	}
	var second int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, first_name, last_name) VALUES ($1, 'G2', 'Éric', 'Dupont') RETURNING id`, f.tenantID).Scan(&second))
	book := func(typ rooms.RoomType, arrival, departure string, guest *int64, confirm bool) reservations.Reservation {
		t.Helper()
		in := f.input(confirm, f.line(typ, arrival, departure))
		in.GuestID = guest
		res, err := f.Res.Create(f.admin, f.propID, "", in)
		must(t, err)
		return res
	}
	first := f.guest
	book(f.std, "2026-10-01", "2026-10-02", &first, true)  // a
	book(f.std, "2026-10-01", "2026-10-03", &second, true) // b: the same arrival and rate as a
	book(f.std, "2026-10-02", "2026-10-03", &first, true)  // c
	book(f.dlx, "2026-10-05", "2026-10-06", &second, true) // d
	book(f.dlx, "2026-10-05", "2026-10-07", nil, false)    // e: a draft without a guest
	cancelled := book(f.std, "2026-10-08", "2026-10-09", &first, true)
	_, err := f.Res.Cancel(f.admin, f.propID, cancelled.ID, cancelled.Version, "x") // f
	must(t, err)
	unpriced := book(f.dlx, "2026-10-09", "2026-10-10", &first, true) // g: its price is lost
	must(t, f.Exec(t, `DELETE FROM reservation_room_rates WHERE reservation_room_id IN (SELECT id FROM reservation_rooms WHERE reservation_id = $1)`, unpriced.ID))
	f.advance(t, roomstest.BD, 2)

	all, err := f.Res.List(f.admin, f.propID, reservations.ListFilter{}, 50)
	must(t, err)
	if len(all) != 7 {
		t.Fatalf("the reservations: %d", len(all))
	}
	return all
}

func rateOf(s reservations.Summary) *decimal.Decimal {
	if len(s.Rooms) == 0 || s.Rooms[0].RateAmount == "" {
		return nil
	}
	d := decimal.RequireFromString(s.Rooms[0].RateAmount)
	return &d
}

// expectedReservations puts the reservations in the order asked for, computed here without the database: NULL last either way, the id breaking a tie in the direction of the order. The default
// order ("") is "nearest first" to the reference date.
func expectedReservations(all []reservations.Summary, key string, desc bool, ref civil.Date) []int64 {
	type item struct {
		s    reservations.Summary
		null bool
		text string
		num  decimal.Decimal
		day  string
	}
	items := make([]item, len(all))
	for i, s := range all {
		it := item{s: s}
		switch key {
		case "":
			distance := s.ArrivalDate.DaysUntil(ref)
			bucket := "1"
			if !s.ArrivalDate.Before(ref) {
				bucket, distance = "0", ref.DaysUntil(s.ArrivalDate)
			}
			it.text = fmt.Sprintf("%s%07d|%s", bucket, distance, s.ConfirmationNumber)
		case "confirmation_number":
			it.text = s.ConfirmationNumber
		case "guest_name":
			it.null = s.GuestID == nil
			it.text = map[string]string{"Éric Dupont": "1", "Guest": "2"}[s.GuestName] // the order of the alphabet: "Éric" beside "E", before "G" (by the bytes it would come after)
		case "status":
			it.text = s.DisplayStatus
		case "rate":
			if r := rateOf(s); r != nil {
				it.num = *r
			} else {
				it.null = true
			}
		case "arrival_date":
			it.day = s.ArrivalDate.String()
		}
		items[i] = it
	}
	sort.SliceStable(items, func(a, b int) bool {
		x, y := items[a], items[b]
		if x.null != y.null {
			return y.null // NULL last in both directions
		}
		if !x.null {
			c := strings.Compare(x.text, y.text)
			if c == 0 {
				c = x.num.Cmp(y.num)
			}
			if c == 0 {
				c = strings.Compare(x.day, y.day)
			}
			if c != 0 {
				return (c < 0) != desc
			}
		}
		return (x.s.ID < y.s.ID) != desc
	})
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.s.ID
	}
	return ids
}

func (f *fx) walkReservations(t *testing.T, order listsort.Sort, filter reservations.ListFilter, between func(page int)) []int64 {
	t.Helper()
	var ids []int64
	var pos *listsort.Position
	for pages := 0; pages < 20; pages++ {
		res, err := f.Res.Search(f.admin, f.propID, filter, order, pos, 2)
		must(t, err)
		for _, s := range res.Data {
			ids = append(ids, s.ID)
		}
		if res.Next == nil {
			return ids
		}
		pos = res.Next
		if between != nil {
			between(pages)
		}
	}
	t.Fatal("the list does not end")
	return nil
}

// Every column, both ways, paged two at a time across equal values (the same arrival, rate, status) and NULL (a guest, a price): no gap, no repeat, the order expected.
func TestReservationSortMatrix(t *testing.T) {
	f := setup(t)
	all := f.sortReservations(t)
	ref := civil.MustParseDate("2026-10-02")
	for _, key := range []string{"arrival_date", "confirmation_number", "guest_name", "status", "rate"} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", key, desc), func(t *testing.T) {
				got := f.walkReservations(t, listsort.Sort{Key: key, Desc: desc}, reservations.ListFilter{}, nil)
				if want := expectedReservations(all, key, desc, ref); !slices.Equal(got, want) {
					t.Fatalf("got  %v\nwant %v", got, want)
				}
			})
		}
	}
	// the default order: nearest first to the business date, the number breaking a tie
	got := f.walkReservations(t, listsort.Sort{}, reservations.ListFilter{}, nil)
	if want := expectedReservations(all, "", false, ref); !slices.Equal(got, want) {
		t.Fatalf("default: got %v want %v", got, want)
	}
	byID := map[int64]reservations.Summary{}
	for _, s := range all {
		byID[s.ID] = s
	}
	var arrivals []string
	for _, id := range got {
		arrivals = append(arrivals, byID[id].ArrivalDate.String())
	}
	if want := []string{"2026-10-02", "2026-10-05", "2026-10-05", "2026-10-08", "2026-10-09", "2026-10-01", "2026-10-01"}; !slices.Equal(arrivals, want) {
		t.Fatalf("nearest first (from the 2nd on, then before it, the most recent first): %v", arrivals)
	}
	// a filter and an order together
	conf := f.walkReservations(t, listsort.Sort{Key: "arrival_date", Desc: true}, reservations.ListFilter{DisplayStatus: "CONFIRMED"}, nil)
	if len(conf) != 5 {
		t.Fatalf("the confirmed ones: %v", conf)
	}
	for i := 1; i < len(conf); i++ {
		if byID[conf[i]].ArrivalDate.After(byID[conf[i-1]].ArrivalDate) {
			t.Fatalf("latest arrival first: %v", conf)
		}
	}
}

// The default order is relative to the business date, and the cursor carries the date it was made with: a night audit between two pages does not move the list under the reader.
func TestReservationDefaultOrderHoldsWhenTheBusinessDayMovesOn(t *testing.T) {
	f := setup(t)
	all := f.sortReservations(t) // the business date is the 2nd
	want := expectedReservations(all, "", false, civil.MustParseDate("2026-10-02"))
	got := f.walkReservations(t, listsort.Sort{}, reservations.ListFilter{}, func(page int) {
		if page == 0 {
			f.advance(t, civil.MustParseDate("2026-10-02"), 4) // the 6th: the order of a new reader would differ
		}
	})
	if !slices.Equal(got, want) {
		t.Fatalf("the pages join as they began: got %v want %v", got, want)
	}
	// a reader who begins now sees the list from the new date: the arrivals of the 5th and later first
	fresh := f.walkReservations(t, listsort.Sort{}, reservations.ListFilter{}, nil)
	if slices.Equal(fresh, want) {
		t.Fatalf("a new list is relative to the new business date: %v", fresh)
	}
}

// One property's reservations are not in another's list, in any order; another tenant cannot read the list at all.
func TestReservationSortStaysInItsPropertyAndTenant(t *testing.T) {
	f := setup(t)
	other := f.Property(t, f.tenantID, "JKT") // before the business day moves on: a property opens on the date of the day
	foreign := f.Tenant(t, "XYZ")             // likewise
	f.sortReservations(t)
	for _, key := range []string{"", "arrival_date", "guest_name", "rate"} {
		res, err := f.Res.Search(f.admin, other.ID, reservations.ListFilter{}, listsort.Sort{Key: key}, nil, 10)
		must(t, err)
		if len(res.Data) != 0 {
			t.Fatalf("%q: another property's list holds %d reservations", key, len(res.Data))
		}
	}
	_, err := f.Res.Search(roomstest.Admin(foreign.ID), f.propID, reservations.ListFilter{}, listsort.Sort{Key: "rate"}, nil, 10)
	if err == nil {
		t.Fatal("another tenant reads the reservations of this property")
	}
}

// A cursor belongs to its order; an order the list does not have is refused.
func TestReservationCursorBelongsToItsOrder(t *testing.T) {
	f := setup(t)
	f.sortReservations(t)
	mux := http.NewServeMux()
	reservations.NewHandler(f.Res).Register(mux)
	call := func(query string) (int, httpx.Page[reservations.Summary]) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/reservations?%s", f.propID, query), nil).WithContext(f.admin)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var page httpx.Page[reservations.Summary]
		if rec.Code == http.StatusOK {
			if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, page
	}
	code, def := call("limit=2")
	if code != http.StatusOK || def.NextCursor == "" {
		t.Fatalf("first page: %d %+v", code, def)
	}
	_, byRate := call("sort=rate&order=desc&limit=2")
	for _, q := range []string{"sort=arrival_date", "sort=rate&order=desc", "sort=status&order=asc"} {
		if c, _ := call(q + "&limit=2&cursor=" + url.QueryEscape(def.NextCursor)); c != http.StatusBadRequest {
			t.Errorf("%s: the cursor of the default order is 400, got %d", q, c)
		}
	}
	if c, _ := call("limit=2&cursor=" + url.QueryEscape(byRate.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("a rate cursor in the default order is 400, got %d", c)
	}
	if c, _ := call("sort=rate&order=asc&limit=2&cursor=" + url.QueryEscape(byRate.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("a descending cursor in the ascending order is 400, got %d", c)
	}
	if c, _ := call("sort=rate&order=desc&limit=2&cursor=" + url.QueryEscape(byRate.NextCursor)); c != http.StatusOK {
		t.Errorf("its own cursor is accepted, got %d", c)
	}
	if c, _ := call("limit=2&cursor=" + url.QueryEscape(def.NextCursor)); c != http.StatusOK {
		t.Errorf("the cursor of the default order is accepted in the default order, got %d", c)
	}
	for _, q := range []string{"sort=guest_id", "sort=rate&order=up", "order=desc"} {
		if c, _ := call(q); c != http.StatusUnprocessableEntity {
			t.Errorf("%s: 422, got %d", q, c)
		}
	}
}
