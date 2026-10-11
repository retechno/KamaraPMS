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
		default: // the default order: open first, then the larger balance first
			open := func(s folios.FolioSummary) int {
				if s.Status == "OPEN" {
					return 0
				}
				return 1
			}
			if c := open(a) - open(b); c != 0 {
				return c
			}
			return -bal(a).Cmp(bal(b))
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
// is the open folios first, the largest balance first among them.
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
