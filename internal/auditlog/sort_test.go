package auditlog_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"kamarapms/internal/auditlog"
	"kamarapms/internal/platform/httpx"
)

// trail calls the audit trail of the property with the query and returns the page.
func (f *fx) trail(t *testing.T, query string) (int, httpx.Page[auditlog.Record]) {
	t.Helper()
	mux := http.NewServeMux()
	auditlog.NewHandler(f.reader).Register(mux)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/audit-logs?%s", f.propID, query), nil).WithContext(f.admin)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var page httpx.Page[auditlog.Record]
	if rec.Code == http.StatusOK {
		if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, page
}

// walk reads the whole trail 3 entries at a time in the order of the query and returns the ids.
func (f *fx) walk(t *testing.T, query string) []int64 {
	t.Helper()
	var ids []int64
	cursor := ""
	for pages := 0; pages < 50; pages++ {
		q := query + "&limit=3"
		if cursor != "" {
			q += "&cursor=" + url.QueryEscape(cursor)
		}
		code, page := f.trail(t, q)
		if code != http.StatusOK {
			t.Fatalf("%s: status %d", q, code)
		}
		for _, r := range page.Data {
			ids = append(ids, r.ID)
		}
		if page.NextCursor == "" {
			return ids
		}
		cursor = page.NextCursor
	}
	t.Fatal("the trail does not end")
	return nil
}

func seedTrail(t *testing.T, f *fx, n int) {
	t.Helper()
	pid := f.propID
	for i := 0; i < n; i++ {
		f.writeEntry(t, f.tenantID, &pid, "RES"+strconv.Itoa(1000+i), "reservation.updated")
	}
}

func TestTrailSortsNewestFirstByDefaultAndOldestFirstOnRequest(t *testing.T) {
	f := setup(t)
	seedTrail(t, f, 10)

	newest := f.walk(t, "action=reservation.updated")
	oldest := f.walk(t, "action=reservation.updated&sort=created_at&order=asc")
	explicitDesc := f.walk(t, "action=reservation.updated&sort=created_at&order=desc")
	if len(newest) != 10 || len(oldest) != 10 || len(explicitDesc) != 10 {
		t.Fatalf("every entry once: %d, %d, %d", len(newest), len(oldest), len(explicitDesc))
	}
	for i := range newest {
		if i > 0 && newest[i] >= newest[i-1] {
			t.Fatalf("default is newest first: %v", newest)
		}
		if i > 0 && oldest[i] <= oldest[i-1] {
			t.Fatalf("asc is oldest first: %v", oldest)
		}
		if newest[i] != oldest[len(oldest)-1-i] || explicitDesc[i] != newest[i] {
			t.Fatalf("the same entries in the opposite order: %v / %v / %v", newest, oldest, explicitDesc)
		}
	}
}

func TestTrailRefusesACursorOfAnotherOrderAndAnUnknownSort(t *testing.T) {
	f := setup(t)
	seedTrail(t, f, 6)

	_, first := f.trail(t, "limit=2")
	if first.NextCursor == "" {
		t.Fatal("a cursor is expected")
	}
	// the cursor of the default (newest first) is not a position in the oldest-first order, nor the explicit one
	for _, q := range []string{"sort=created_at&order=asc", "sort=created_at&order=desc"} {
		if code, _ := f.trail(t, q+"&limit=2&cursor="+url.QueryEscape(first.NextCursor)); code != http.StatusBadRequest {
			t.Fatalf("%s: a cursor of another order is 400, got %d", q, code)
		}
	}
	_, asc := f.trail(t, "sort=created_at&order=asc&limit=2")
	if code, _ := f.trail(t, "limit=2&cursor="+url.QueryEscape(asc.NextCursor)); code != http.StatusBadRequest {
		t.Fatalf("an oldest-first cursor in the newest-first order is 400, got %d", code)
	}
	if code, _ := f.trail(t, "limit=2&cursor=garbage"); code != http.StatusBadRequest {
		t.Fatalf("a cursor that is not one is 400, got %d", code)
	}
	for _, q := range []string{"sort=user", "sort=created_at&order=sideways", "order=asc"} {
		if code, _ := f.trail(t, q); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s: 422 VALIDATION_FAILED, got %d", q, code)
		}
	}
}

func TestTrailOrderStaysWithinTheTenantAndTheFilters(t *testing.T) {
	f := setup(t)
	seedTrail(t, f, 4)
	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "OTHR")
	f.writeEntry(t, other.ID, &op.ID, "RES9999", "reservation.updated") // another tenant's entry, newer than all of ours

	for _, q := range []string{"action=reservation.updated", "action=reservation.updated&sort=created_at&order=asc"} {
		ids := f.walk(t, q)
		if len(ids) != 4 {
			t.Fatalf("%q: only the entries of the tenant's property, got %d", q, len(ids))
		}
	}
	// a filter and an order together: nothing but the matching entries, in order
	pid := f.propID
	f.writeEntry(t, f.tenantID, &pid, "SPECIAL-1", "reservation.cancelled")
	f.writeEntry(t, f.tenantID, &pid, "SPECIAL-2", "reservation.cancelled")
	ids := f.walk(t, "action=reservation.cancelled&sort=created_at&order=asc")
	if len(ids) != 2 || ids[0] >= ids[1] {
		t.Fatalf("filter and order: %v", ids)
	}
}
