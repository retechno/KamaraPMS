package folios_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"testing"
	"time"

	"kamarapms/internal/folios"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/listsort"
)

// sortPayments posts payments that are alike on purpose: amounts that repeat, methods that repeat, times that repeat (several are posted in the same instant), and two that are voided.
// It returns every payment as the cashier list shows it.
func (f *fx) sortPayments(t *testing.T) []folios.Payment {
	t.Helper()
	type spec struct {
		amount, method string
		advance        time.Duration
		void           bool
	}
	for i, s := range []spec{
		{"100000", "CASH", 0, false},
		{"100000", "CASH", 0, false}, // the same amount, method and instant
		{"250000", "CARD", time.Minute, false},
		{"250000", "BANK_TRANSFER", 0, true},
		{"75000", "CASH", time.Minute, false},
		{"75000", "CARD", 0, false},
		{"500000", "OTHER", time.Hour, true},
		{"20000", "CARD", 0, false},
	} {
		f.Clock.Advance(s.advance)
		res, err := f.Folios.PostPayment(f.admin, f.propID, f.folio, fmt.Sprintf("sp%d", i), folios.PaymentInput{Amount: s.amount, PaymentMethod: s.method})
		must(t, err)
		if s.void {
			_, err = f.Folios.Void(f.admin, f.propID, res.Payment.ID, folios.CorrectionInput{Reason: "x", Approval: f.approval()})
			must(t, err)
		}
	}
	all, err := f.Folios.ListPayments(f.admin, f.propID, folios.PaymentFilter{}, listsort.Sort{}, nil, 50)
	must(t, err)
	if len(all.Data) != 8 {
		t.Fatalf("the payments: %d", len(all.Data))
	}
	return all.Data
}

// expectedPayments puts the payments in the order of the column, computed here without the database; the id breaks a tie in the direction of the order.
func expectedPayments(all []folios.Payment, key string, desc bool) []int64 {
	less := func(a, b folios.Payment) int {
		switch key {
		case "payment_number":
			return compareStrings(a.PaymentNumber, b.PaymentNumber)
		case "payment_method":
			return compareStrings(a.PaymentMethod, b.PaymentMethod)
		case "status":
			return compareStrings(a.Status, b.Status)
		case "amount":
			return compareStrings(pad(a.Amount), pad(b.Amount))
		default: // paid_at
			return a.PaidAt.Compare(b.PaidAt)
		}
	}
	s := slices.Clone(all)
	sort.SliceStable(s, func(i, j int) bool {
		if c := less(s[i], s[j]); c != 0 {
			return (c < 0) != desc
		}
		return (s[i].ID < s[j].ID) != desc
	})
	ids := make([]int64, len(s))
	for i, p := range s {
		ids[i] = p.ID
	}
	return ids
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func pad(amount string) string { return fmt.Sprintf("%012s", amount) } // amounts here are whole numbers

func (f *fx) walkPayments(t *testing.T, order listsort.Sort, filter folios.PaymentFilter) []int64 {
	t.Helper()
	var ids []int64
	var pos *listsort.Position
	for pages := 0; pages < 20; pages++ {
		res, err := f.Folios.ListPayments(f.admin, f.propID, filter, order, pos, 3)
		must(t, err)
		for _, p := range res.Data {
			ids = append(ids, p.ID)
		}
		if res.Next == nil {
			return ids
		}
		pos = res.Next
	}
	t.Fatal("the list does not end")
	return nil
}

// Every column, both ways, paged three at a time across equal values (amounts, methods, statuses and instants that repeat): no gap, no repeat, the order expected.
func TestPaymentSortMatrix(t *testing.T) {
	f := setup(t)
	all := f.sortPayments(t)
	for _, key := range []string{"payment_number", "amount", "payment_method", "status", "paid_at"} {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", key, desc), func(t *testing.T) {
				got := f.walkPayments(t, listsort.Sort{Key: key, Desc: desc}, folios.PaymentFilter{})
				if want := expectedPayments(all, key, desc); !slices.Equal(got, want) {
					t.Fatalf("got  %v\nwant %v", got, want)
				}
			})
		}
	}
	// the default is the newest first, and so is the explicit paid_at desc (one query serves both)
	if got, want := f.walkPayments(t, listsort.Sort{}, folios.PaymentFilter{}), expectedPayments(all, "paid_at", true); !slices.Equal(got, want) {
		t.Fatalf("default: got %v want %v", got, want)
	}
	// a filter and an order together
	cards := f.walkPayments(t, listsort.Sort{Key: "amount", Desc: true}, folios.PaymentFilter{Method: "CARD"})
	if len(cards) != 3 {
		t.Fatalf("the card payments in order of amount: %v", cards)
	}
}

// One property's payments are never in another's list, in any order.
func TestPaymentSortStaysInTheProperty(t *testing.T) {
	f := setup(t)
	f.sortPayments(t)
	other := f.Property(t, f.tenantID, "OTHR")
	res, err := f.Folios.ListPayments(f.admin, other.ID, folios.PaymentFilter{}, listsort.Sort{Key: "amount"}, nil, 10)
	must(t, err)
	if len(res.Data) != 0 {
		t.Fatalf("another property's list is empty: %d", len(res.Data))
	}
}

// A cursor belongs to its order; an order the list does not have is refused.
func TestPaymentCursorBelongsToItsOrder(t *testing.T) {
	f := setup(t)
	f.sortPayments(t)
	mux := http.NewServeMux()
	folios.NewHandler(f.Folios).Register(mux)
	call := func(query string) (int, httpx.Page[folios.Payment]) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/payments?%s", f.propID, query), nil).WithContext(f.admin)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var page httpx.Page[folios.Payment]
		if rec.Code == http.StatusOK {
			if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, page
	}
	code, newest := call("limit=2")
	if code != http.StatusOK || newest.NextCursor == "" {
		t.Fatalf("first page: %d %+v", code, newest)
	}
	_, byAmount := call("sort=amount&order=asc&limit=2")
	for _, q := range []string{"sort=amount", "sort=paid_at&order=asc", "sort=status&order=desc"} {
		if c, _ := call(q + "&limit=2&cursor=" + url.QueryEscape(newest.NextCursor)); c != http.StatusBadRequest {
			t.Errorf("%s: the cursor of the default order is 400, got %d", q, c)
		}
	}
	if c, _ := call("limit=2&cursor=" + url.QueryEscape(byAmount.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("an amount cursor in the default order is 400, got %d", c)
	}
	if c, _ := call("sort=amount&order=desc&limit=2&cursor=" + url.QueryEscape(byAmount.NextCursor)); c != http.StatusBadRequest {
		t.Errorf("an ascending cursor in the descending order is 400, got %d", c)
	}
	if c, _ := call("sort=amount&order=asc&limit=2&cursor=" + url.QueryEscape(byAmount.NextCursor)); c != http.StatusOK {
		t.Errorf("its own cursor is accepted, got %d", c)
	}
	for _, q := range []string{"sort=folio_id", "sort=amount&order=up", "order=asc"} {
		if c, _ := call(q); c != http.StatusUnprocessableEntity {
			t.Errorf("%s: 422, got %d", q, c)
		}
	}
}
