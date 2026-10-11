package guests_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"kamarapms/internal/guests"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/listsort"
	"kamarapms/internal/rooms/roomstest"
)

// sortGuests are the guests of the sort tests. The names are chosen to be hard: two guests with the same name (a tie that only the id breaks), a lower-case first name, an accent, a single
// name (it is the last name), capitals and small letters mixed, and guests without a nationality (NULL).
var sortGuests = []struct{ first, last, nationality string }{
	{"Siti", "Rahmawati", "ID"},
	{"Siti", "Rahmawati", "ID"},
	{"adi", "Putra", "ID"},
	{"Éric", "Dupont", "FR"},
	{"", "Madonna", ""},
	{"Eva", "Zed", ""},
	{"Zed", "Alpha", "US"},
	{"Budi", "Santoso", "ID"},
}

// nameRank is the order of the names that are shown, as a person who reads the alphabet puts them (an accent beside its letter, a capital beside a small one).
var nameRank = map[string]int{"adi putra": 0, "budi santoso": 1, "éric dupont": 2, "eva zed": 3, "madonna": 4, "siti rahmawati": 5, "zed alpha": 6}

func (f fixture) sortFixture(t *testing.T) []guests.Guest {
	t.Helper()
	var out []guests.Guest
	for _, s := range sortGuests {
		r, err := f.Guests.Create(f.admin, f.bali, guests.Profile{FirstName: s.first, LastName: s.last, Nationality: s.nationality})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, r.Guest)
	}
	return out
}

// expected is the order of the guests by the column, computed here without the database: NULL last, either way, the id breaking a tie in the direction of the order.
func expected(all []guests.Guest, key string, desc bool) []int64 {
	type item struct {
		id   int64
		null bool
		rank string
		n    int
	}
	items := make([]item, len(all))
	for i, g := range all {
		it := item{id: g.ID}
		switch key {
		case "code":
			it.rank = g.Code
		case "nationality":
			it.rank = g.Nationality
			it.null = g.Nationality == ""
		default:
			display := g.LastName
			if g.FirstName != "" {
				display = g.FirstName + " " + g.LastName
			}
			it.n = nameRank[strings.ToLower(display)]
		}
		items[i] = it
	}
	sort.Slice(items, func(a, b int) bool {
		x, y := items[a], items[b]
		if x.null != y.null {
			return y.null // NULL last in both directions
		}
		if !x.null {
			if x.rank != y.rank {
				return (x.rank < y.rank) != desc
			}
			if x.n != y.n {
				return (x.n < y.n) != desc
			}
		}
		return (x.id < y.id) != desc
	})
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.id
	}
	return ids
}

// walkGuests reads the whole list two guests at a time in the order given.
func (f fixture) walkGuests(t *testing.T, order listsort.Sort, q string) []int64 {
	t.Helper()
	var ids []int64
	var pos *listsort.Position
	for pages := 0; pages < 20; pages++ {
		res, err := f.Guests.Search(f.admin, q, nil, order, pos, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range res.Guests {
			ids = append(ids, g.ID)
		}
		if res.Next == nil {
			return ids
		}
		pos = res.Next
	}
	t.Fatal("the list does not end")
	return nil
}

// Every column, both ways, paged across equal values (two pairs of equal names, three guests of one nationality) and NULL: the pages join without a gap or a repeat and the order is the one expected.
func TestGuestSortMatrix(t *testing.T) {
	f := newFixture(t)
	all := f.sortFixture(t)
	for _, key := range []string{"name", "code", "nationality"} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", key, desc), func(t *testing.T) {
				got := f.walkGuests(t, listsort.Sort{Key: key, Desc: desc}, "")
				want := expected(all, key, desc)
				if !slices.Equal(got, want) {
					t.Fatalf("got %v\nwant %v", got, want)
				}
			})
		}
	}
	// the default is the name A-Z, and the explicit name A-Z is the same list (one query serves both)
	if got, want := f.walkGuests(t, listsort.Sort{}, ""), expected(all, "name", false); !slices.Equal(got, want) {
		t.Fatalf("default: got %v want %v", got, want)
	}
}

// The names are put in order as a reader of the alphabet does: an accent beside its letter and capitals beside small letters. The collation of the database alone puts "Éric" after "adi" and "Zed".
func TestGuestNamesAreInAlphabeticalOrder(t *testing.T) {
	f := newFixture(t)
	f.sortFixture(t)
	res, err := f.Guests.Search(f.admin, "", nil, listsort.Sort{}, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range res.Guests {
		if g.FirstName != "" {
			got = append(got, g.FirstName+" "+g.LastName)
		} else {
			got = append(got, g.LastName)
		}
	}
	want := []string{"adi Putra", "Budi Santoso", "Éric Dupont", "Eva Zed", "Madonna", "Siti Rahmawati", "Siti Rahmawati", "Zed Alpha"}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// One tenant never sees the guests of another, in any order.
func TestGuestSortStaysInTheTenant(t *testing.T) {
	f := newFixture(t)
	f.sortFixture(t)
	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "OTHR")
	if _, err := f.Guests.Create(roomstest.Admin(other.ID), op.ID, guests.Profile{FirstName: "Aaron", LastName: "Aardvark", Nationality: "AA"}); err != nil {
		t.Fatal(err)
	}
	for _, order := range []listsort.Sort{{}, {Key: "name", Desc: true}, {Key: "code"}, {Key: "nationality", Desc: true}} {
		if got := f.walkGuests(t, order, ""); len(got) != len(sortGuests) {
			t.Fatalf("%+v: %d guests, want %d (the tenant's own)", order, len(got), len(sortGuests))
		}
	}
}

// A search text and an order together: only the guests that match, in order.
func TestGuestSortWithASearch(t *testing.T) {
	f := newFixture(t)
	all := f.sortFixture(t)
	got := f.walkGuests(t, listsort.Sort{Key: "name", Desc: true}, "siti")
	var siti []int64
	for _, g := range all {
		if g.FirstName == "Siti" {
			siti = append(siti, g.ID)
		}
	}
	slices.Sort(siti)
	slices.Reverse(siti) // equal names: the id, in the direction of the order
	if !slices.Equal(got, siti) {
		t.Fatalf("got %v want %v", got, siti)
	}
}

// A cursor belongs to its order, and an order the list does not have is refused.
func TestGuestCursorBelongsToItsOrder(t *testing.T) {
	f := newFixture(t)
	f.sortFixture(t)
	mux := http.NewServeMux()
	guests.NewHandler(f.Guests).Register(mux)
	call := func(query string) (int, httpx.Page[guests.Guest]) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/guests?"+query, nil).WithContext(f.admin)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var page httpx.Page[guests.Guest]
		if rec.Code == http.StatusOK {
			if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, page
	}
	code, byName := call("limit=2")
	if code != http.StatusOK || byName.NextCursor == "" {
		t.Fatalf("first page: %d %+v", code, byName)
	}
	_, byCode := call("sort=code&order=desc&limit=2")
	for _, q := range []string{"sort=name&order=desc", "sort=code", "sort=nationality"} {
		if c, _ := call(q + "&limit=2&cursor=" + url.QueryEscape(byName.NextCursor)); c != http.StatusBadRequest {
			t.Errorf("%s: the cursor of the default order is 400, got %d", q, c)
		}
	}
	if c, _ := call("limit=2&cursor=" + url.QueryEscape(byCode.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("the cursor of code desc in the default order is 400, got %d", c)
	}
	if c, _ := call("sort=code&order=desc&limit=2&cursor=" + url.QueryEscape(byCode.NextCursor)); c != http.StatusOK {
		t.Errorf("its own cursor is accepted, got %d", c)
	}
	for _, q := range []string{"sort=email", "sort=name&order=up", "order=desc"} {
		if c, _ := call(q); c != http.StatusUnprocessableEntity {
			t.Errorf("%s: 422, got %d", q, c)
		}
	}
}

// A percent sign or an underscore in the search is the character itself, found as typed (the pattern of the search escapes them), and not a wildcard.
func TestGuestSearchFindsALiteralPercentAndUnderscore(t *testing.T) {
	f := newFixture(t)
	f.guest(t, f.admin, f.bali, "", "50%Promo")
	f.guest(t, f.admin, f.bali, "", "Ad_Hoc")
	f.guest(t, f.admin, f.bali, "", "Adhoc")
	for q, want := range map[string]string{"50%": " 50%Promo", "ad_": " Ad_Hoc", "ad_h": " Ad_Hoc", "5%": ""} {
		if got := names(f.search(t, f.admin, q, nil)); got != want {
			t.Errorf("search %q = %q, want %q", q, got, want)
		}
	}
}
