package folios_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/rooms/roomstest"
)

// sortFolios makes folios that are alike on purpose: two with the same balance (a tie that only the id breaks), one that owes nothing, one that holds a credit (a negative balance), and
// closed ones (which can only have a zero balance). It returns every folio of the property as the list shows it.
func (f *fx) sortFolios(t *testing.T) []folios.FolioSummary {
	t.Helper()
	post := func(folio int64, key, price string) {
		_, err := f.Folios.PostCharge(f.admin, f.propID, folio, key, folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr(price)})
		must(t, err)
	}
	pay := func(folio int64, key, amount string) {
		_, err := f.Folios.PostPayment(f.admin, f.propID, folio, key, folios.PaymentInput{Amount: amount, PaymentMethod: "CASH"})
		must(t, err)
	}
	closeIt := func(folio int64) {
		fo, err := f.Folios.GetFolio(f.admin, f.propID, folio)
		must(t, err)
		_, err = f.Folios.CloseFolio(f.admin, f.propID, folio, fo.Version)
		must(t, err)
	}
	a, b, c, d, e, g, h := f.newFolio(t, "F-A"), f.newFolio(t, "F-B"), f.newFolio(t, "F-C"), f.newFolio(t, "F-D"), f.newFolio(t, "F-E"), f.newFolio(t, "F-G"), f.newFolio(t, "F-H")
	post(a, "a1", "100000") // 122100
	post(b, "b1", "100000") // 122100: the same
	post(c, "c1", "50000")
	_ = d                   // nothing owed
	post(e, "e1", "100000") // paid in full, then closed
	pay(e, "e2", "122100")
	closeIt(e)
	closeIt(h)            // never used, closed
	pay(g, "g1", "30000") // a credit: -30000
	post(f.folio, "z1", "10000")
	all, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{}, listsort.Sort{Key: "folio_number"}, nil, 50)
	must(t, err)
	if len(all.Data) != 8 {
		t.Fatalf("the folios: %d", len(all.Data))
	}
	return all.Data
}

// expectedFolios puts the folios in the order of the column, computed here without the database.
func expectedFolios(all []folios.FolioSummary, key string, desc bool) []int64 {
	bal := func(s folios.FolioSummary) decimal.Decimal { return decimal.RequireFromString(s.Balance) }
	cmp := func(a, b folios.FolioSummary) int {
		switch key {
		case "folio_number":
			return compareStrings(a.FolioNumber, b.FolioNumber)
		case "status":
			return compareStrings(a.Status, b.Status)
		case "balance":
			return bal(a).Cmp(bal(b))
		default: // the default order: open first, then by room (the folios without a room after the rooms), the id breaking a tie
			open := func(s folios.FolioSummary) int {
				if s.Status == "OPEN" {
					return 0
				}
				return 1
			}
			if c := open(a) - open(b); c != 0 {
				return c
			}
			return compareStrings(roomKey(a), roomKey(b))
		}
	}
	s := slices.Clone(all)
	sort.SliceStable(s, func(i, j int) bool {
		if c := cmp(s[i], s[j]); c != 0 {
			return (c < 0) != desc
		}
		return (s[i].ID < s[j].ID) != desc
	})
	ids := make([]int64, len(s))
	for i, x := range s {
		ids[i] = x.ID
	}
	return ids
}

// roomKey is the room number padded as the list pads it, a folio without a room after every room.
func roomKey(s folios.FolioSummary) string {
	if s.RoomNumber == "" {
		return "zzzzzzzzzzzz"
	}
	return fmt.Sprintf("%012s", s.RoomNumber)
}

func (f *fx) walkFolios(t *testing.T, order listsort.Sort, filter folios.FolioFilter) []int64 {
	t.Helper()
	var ids []int64
	var pos *listsort.Position
	for pages := 0; pages < 20; pages++ {
		res, err := f.Folios.ListFolios(f.admin, f.propID, filter, order, pos, 3)
		must(t, err)
		for _, x := range res.Data {
			ids = append(ids, x.ID)
		}
		if res.Next == nil {
			return ids
		}
		pos = res.Next
	}
	t.Fatal("the list does not end")
	return nil
}

// Every column, both ways, paged three at a time across equal values (two folios with the same balance, folios with a zero balance, a credit): no gap, no repeat, the order expected. The default
// is the open folios first, by room among them (these folios have no stay, so no room: the id breaks every tie; the order by room is TestFolioListNamesTheRoomAndTheGuestAndSearches).
func TestFolioSortMatrix(t *testing.T) {
	f := setup(t)
	all := f.sortFolios(t)
	for _, key := range []string{"folio_number", "balance", "status"} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", key, desc), func(t *testing.T) {
				got := f.walkFolios(t, listsort.Sort{Key: key, Desc: desc}, folios.FolioFilter{})
				if want := expectedFolios(all, key, desc); !slices.Equal(got, want) {
					t.Fatalf("got  %v\nwant %v", got, want)
				}
			})
		}
	}
	got := f.walkFolios(t, listsort.Sort{}, folios.FolioFilter{})
	if want := expectedFolios(all, "", false); !slices.Equal(got, want) {
		t.Fatalf("default: got %v want %v", got, want)
	}
	// the open ones come first in the default order, the closed ones after them
	var seenClosed bool
	byID := map[int64]folios.FolioSummary{}
	for _, x := range all {
		byID[x.ID] = x
	}
	for _, id := range got {
		if byID[id].Status == "CLOSED" {
			seenClosed = true
		} else if seenClosed {
			t.Fatalf("an open folio after a closed one: %v", got)
		}
	}
	// a filter and an order together: only the open folios, the largest balance first
	open := f.walkFolios(t, listsort.Sort{Key: "balance", Desc: true}, folios.FolioFilter{Status: "OPEN"})
	for _, id := range open {
		if byID[id].Status != "OPEN" {
			t.Fatalf("a closed folio in the open list: %v", open)
		}
	}
	if len(open) != 6 {
		t.Fatalf("open folios: %d", len(open))
	}
}

// The balance of the list is the balance of the folio, read from its items: the same as GetFolio gives for every folio (an open one is summed, a closed one is zero).
func TestFolioListBalanceIsTheBalanceOfTheLedger(t *testing.T) {
	f := setup(t)
	all := f.sortFolios(t)
	for _, s := range all {
		fo, err := f.Folios.GetFolio(f.admin, f.propID, s.ID)
		must(t, err)
		if fo.Balance != s.Balance {
			t.Errorf("folio %s: the list says %s, the ledger %s", s.FolioNumber, s.Balance, fo.Balance)
		}
	}
	var credit bool
	for _, s := range all {
		if s.Balance == "-30000" {
			credit = true
		}
	}
	if !credit {
		t.Fatalf("the credit folio is in the list: %+v", all)
	}
}

// A folio can only be closed with a zero balance, and nothing is posted to a closed one: so the list does not read the items of a closed folio. This holds for the way the folios list tests close a folio
// (CloseFolio, here), and for every other way (the check-out, the reverse of a check-in) through the check that every database test ends with (dbtest.Reset): no closed folio with a balance.
func TestNoClosedFolioHasABalance(t *testing.T) {
	f := setup(t)
	f.sortFolios(t)
	assertClosedFoliosAreZero(t, f)
	// a balance that is not zero keeps the folio open
	fo, err := f.Folios.GetFolio(f.admin, f.propID, f.folio)
	must(t, err)
	_, err = f.Folios.CloseFolio(f.admin, f.propID, f.folio, fo.Version)
	wantCode(t, err, "FOLIO_BALANCE_NOT_ZERO")
	// and a closed folio takes no posting (a charge, a payment)
	var closed int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM folios WHERE property_id = $1 AND status = 'CLOSED' ORDER BY id LIMIT 1`, f.propID).Scan(&closed))
	_, err = f.Folios.PostCharge(f.admin, f.propID, closed, "late", folios.ChargeInput{ChargeCodeID: f.minibar, Quantity: "1", UnitPrice: ptr("1000")})
	wantCode(t, err, "FOLIO_CLOSED")
	_, err = f.Folios.PostPayment(f.admin, f.propID, closed, "latepay", folios.PaymentInput{Amount: "1000", PaymentMethod: "CASH"})
	wantCode(t, err, "FOLIO_CLOSED")
	assertClosedFoliosAreZero(t, f)
}

func assertClosedFoliosAreZero(t *testing.T, f *fx) {
	t.Helper()
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `
		SELECT count(*) FROM folios fo
		WHERE fo.status = 'CLOSED' AND COALESCE((SELECT sum(i.debit) - sum(i.credit) FROM folio_items i WHERE i.folio_id = fo.id), 0) <> 0`).Scan(&n))
	if n != 0 {
		t.Fatalf("%d closed folios have a balance that is not zero", n)
	}
}

// A cursor belongs to its order; an order the list does not have is refused; one tenant's folios are not in another's list.
func TestFolioCursorBelongsToItsOrderAndStaysInTheTenant(t *testing.T) {
	f := setup(t)
	f.sortFolios(t)
	mux := http.NewServeMux()
	folios.NewHandler(f.Folios).Register(mux)
	call := func(query string) (int, httpx.Page[folios.FolioSummary]) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/folios?%s", f.propID, query), nil).WithContext(f.admin)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var page httpx.Page[folios.FolioSummary]
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
	_, byBalance := call("sort=balance&order=desc&limit=2")
	for _, q := range []string{"sort=balance&order=desc", "sort=folio_number", "sort=status&order=asc"} {
		if c, _ := call(q + "&limit=2&cursor=" + url.QueryEscape(def.NextCursor)); c != http.StatusBadRequest {
			t.Errorf("%s: the cursor of the default order is 400, got %d", q, c)
		}
	}
	if c, _ := call("limit=2&cursor=" + url.QueryEscape(byBalance.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("a balance cursor in the default order is 400, got %d", c)
	}
	if c, _ := call("sort=balance&order=asc&limit=2&cursor=" + url.QueryEscape(byBalance.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("a descending cursor in the ascending order is 400, got %d", c)
	}
	if c, _ := call("sort=balance&order=desc&limit=2&cursor=" + url.QueryEscape(byBalance.NextCursor)); c != http.StatusOK {
		t.Errorf("its own cursor is accepted, got %d", c)
	}
	for _, q := range []string{"sort=reservation_id", "sort=balance&order=up", "order=desc"} {
		if c, _ := call(q); c != http.StatusUnprocessableEntity {
			t.Errorf("%s: 422, got %d", q, c)
		}
	}
	// another tenant sees neither the property nor its folios, whatever the order
	foreign := f.Tenant(t, "XYZ")
	_, err := f.Folios.ListFolios(roomstest.Admin(foreign.ID), f.propID, folios.FolioFilter{}, listsort.Sort{Key: "balance"}, nil, 10)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// The folio list names the room and the guest of each folio, the default order is by room (the folios without a room last, the closed ones after the open ones), and the search finds a folio by the
// number of the folio or of the reservation, the room or the name of the guest, inside the property of the tenant only.
func TestFolioListNamesTheRoomAndTheGuestAndSearches(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	var typeID int64
	must(t, f.Pool.QueryRow(ctx, `SELECT id FROM room_types WHERE property_id = $1 LIMIT 1`, f.propID).Scan(&typeID))
	r99 := f.Room(t, f.admin, f.propID, typeID, "99")
	r307 := f.Room(t, f.admin, f.propID, typeID, "307")
	r1001 := f.Room(t, f.admin, f.propID, typeID, "1001")
	// three stays in rooms 307, 99 and 1001, one folio each; the guest of the fixture gets a name
	must(t, f.Exec(t, `UPDATE guests SET first_name = 'Agus', last_name = 'Salim' WHERE tenant_id = $1`, f.tenantID))
	for _, r := range []struct {
		room string
		id   int64
	}{{"307", r307.ID}, {"99", r99.ID}, {"1001", r1001.ID}} {
		stay := f.Stay(t, f.tenantID, f.propID, typeID, r.id, "2026-09-30", "2026-10-02")
		must(t, f.Pool.QueryRow(ctx, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id)
			SELECT $1, $2, $3, rr.reservation_id, $4 FROM stays s JOIN reservation_rooms rr ON rr.id = s.reservation_room_id WHERE s.id = $4 RETURNING id`,
			f.tenantID, f.propID, "FR-"+r.room, stay).Scan(new(int64)))
	}
	// a closed folio (no room, no guest): after the open ones
	closedID := f.newFolio(t, "F-CLOSED")
	fo, err := f.Folios.GetFolio(f.admin, f.propID, closedID)
	must(t, err)
	_, err = f.Folios.CloseFolio(f.admin, f.propID, closedID, fo.Version)
	must(t, err)

	all, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{}, listsort.Sort{}, nil, 50)
	must(t, err)
	var order []string
	for _, s := range all.Data {
		order = append(order, s.FolioNumber)
	}
	// the open folios by room (99, 307, 1001 as numbers of one length), then the open folio without a room (F1), then the closed one
	if want := []string{"FR-99", "FR-307", "FR-1001", "F1", "F-CLOSED"}; !slices.Equal(order, want) {
		t.Fatalf("the default order: %v, want %v", order, want)
	}
	if first := all.Data[1]; first.RoomNumber != "307" || first.GuestName != "Agus Salim" {
		t.Fatalf("the room and the guest of the row: %+v", first)
	}
	// a page of two keeps the order across pages
	var walked []int64
	var pos *listsort.Position
	for i := 0; i < 10; i++ {
		page, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{}, listsort.Sort{}, pos, 2)
		must(t, err)
		for _, s := range page.Data {
			walked = append(walked, s.ID)
		}
		if page.Next == nil {
			break
		}
		pos = page.Next
	}
	var listed []int64
	for _, s := range all.Data {
		listed = append(listed, s.ID)
	}
	if !slices.Equal(walked, listed) {
		t.Fatalf("paged %v, whole %v", walked, listed)
	}
	search := func(q string) []string {
		res, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{Search: q}, listsort.Sort{}, nil, 50)
		must(t, err)
		var out []string
		for _, s := range res.Data {
			out = append(out, s.FolioNumber)
		}
		return out
	}
	if got := search("307"); !slices.Equal(got, []string{"FR-307"}) {
		t.Errorf("by room: %v", got)
	}
	if got := search("  salim "); len(got) != 4 || got[0] != "FR-99" { // by a part of the guest name, case aside: the three stays and F1, whose reservation has the same guest
		t.Errorf("by guest: %v", got)
	}
	if got := search("fr-1001"); !slices.Equal(got, []string{"FR-1001"}) {
		t.Errorf("by folio number: %v", got)
	}
	if got := search("R-F-CLOSED"); !slices.Equal(got, []string{"F-CLOSED"}) {
		t.Errorf("by confirmation number: %v", got)
	}
	if got := search("nobody"); len(got) != 0 {
		t.Errorf("no match: %v", got)
	}
	// the search keeps the other filters, and the tenant: another tenant's folios are not found by the same words
	open, err := f.Folios.ListFolios(f.admin, f.propID, folios.FolioFilter{Search: "salim", Status: "CLOSED"}, listsort.Sort{}, nil, 50)
	must(t, err)
	if len(open.Data) != 0 {
		t.Errorf("status and search together: %+v", open.Data)
	}
	// over HTTP: ?q= reaches the search and the row carries the room and the guest
	mux := http.NewServeMux()
	folios.NewHandler(f.Folios).Register(mux)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/folios?q=307", f.propID), nil).WithContext(f.admin)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var page httpx.Page[folios.FolioSummary]
	must(t, json.NewDecoder(rec.Body).Decode(&page))
	if rec.Code != http.StatusOK || len(page.Data) != 1 || page.Data[0].RoomNumber != "307" || page.Data[0].GuestName != "Agus Salim" {
		t.Errorf("?q=307: %d %+v", rec.Code, page.Data)
	}
	foreign := f.Tenant(t, "XYZ")
	_, err = f.Folios.ListFolios(roomstest.Admin(foreign.ID), f.propID, folios.FolioFilter{Search: "salim"}, listsort.Sort{}, nil, 10)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}
