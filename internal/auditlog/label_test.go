package auditlog_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditlog"
	"kamarapms/internal/rooms/roomstest"
)

// writeEntry writes an audit entry through the writer, in a transaction of its own (as a use case does).
func (f *fx) writeEntry(t *testing.T, tenantID int64, propertyID *int64, label string, action string) {
	t.Helper()
	bd := roomstest.BD
	w := audit.NewWriter(f.Clock)
	must(t, f.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
		return w.Write(ctx, audit.Entry{TenantID: tenantID, PropertyID: propertyID, BusinessDate: &bd, Action: action, EntityType: "reservation", EntityID: 1, EntityLabel: label, New: map[string]any{"x": 1}})
	}))
}

func (f *fx) search(t *testing.T, ctx context.Context, q string) []auditlog.Record {
	t.Helper()
	pid := f.propID
	got, err := f.reader.Search(ctx, auditlog.Filter{PropertyID: &pid, EntityType: "reservation", Q: q}, 0, 50)
	must(t, err)
	return got
}

func labels(rows []auditlog.Record) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.EntityLabel != nil {
			out = append(out, *r.EntityLabel)
		}
	}
	return out
}

func TestTheWriterStoresTheLabelAndTheReaderReturnsIt(t *testing.T) {
	f := setup(t)
	pid := f.propID
	f.writeEntry(t, f.tenantID, &pid, "RES000012", "reservation.created")
	f.writeEntry(t, f.tenantID, &pid, "", "reservation.updated")       // no readable number: NULL
	f.writeEntry(t, f.tenantID, &pid, "   \t ", "reservation.updated") // only blanks: NULL as well
	got := f.search(t, f.admin, "")
	if len(got) != 3 {
		t.Fatalf("entries: %d", len(got))
	}
	// newest first: the two without a label, then the one that has it
	if got[0].EntityLabel != nil || got[1].EntityLabel != nil || got[2].EntityLabel == nil || *got[2].EntityLabel != "RES000012" {
		t.Fatalf("labels: %+v", labels(got))
	}
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE entity_label IS NULL AND entity_type = 'reservation'`).Scan(&n))
	if n != 2 {
		t.Fatalf("NULL labels in the table: %d", n)
	}
}

func TestALongLabelIsCutAtACharacterAndNeverRefused(t *testing.T) {
	f := setup(t)
	pid := f.propID
	long := strings.Repeat("é", 200) // two bytes each: a cut by bytes would split a character, and a varchar(120) counts characters
	f.writeEntry(t, f.tenantID, &pid, long, "budget.created")
	mixed := "Budget " + strings.Repeat("日本語", 60)
	f.writeEntry(t, f.tenantID, &pid, mixed, "budget.created")
	got := f.search(t, f.admin, "")
	if len(got) != 2 {
		t.Fatalf("entries: %d", len(got))
	}
	for _, r := range got {
		l := *r.EntityLabel
		if n := len([]rune(l)); n != audit.MaxLabelLength {
			t.Fatalf("stored %d characters, want %d: %q", n, audit.MaxLabelLength, l)
		}
		if !strings.HasPrefix(long, l) && !strings.HasPrefix(mixed, l) {
			t.Fatalf("the label is not the start of what was given: %q", l)
		}
	}
	// a label that fits is kept as it is
	f.writeEntry(t, f.tenantID, &pid, strings.Repeat("a", audit.MaxLabelLength), "budget.created")
	if got := f.search(t, f.admin, ""); len([]rune(*got[0].EntityLabel)) != audit.MaxLabelLength {
		t.Fatal("a label of exactly the length was changed")
	}
}

func TestQFindsAPartOfALabelInAnyCase(t *testing.T) {
	f := setup(t)
	pid := f.propID
	for _, l := range []string{"RES000012", "RES000013", "FOL000026", "305"} {
		f.writeEntry(t, f.tenantID, &pid, l, "x.created")
	}
	f.writeEntry(t, f.tenantID, &pid, "", "x.created") // no label: never found by q
	cases := map[string][]string{
		"RES000012": {"RES000012"},
		"res000012": {"RES000012"},
		"0012":      {"RES000012"},
		"RES0000":   {"RES000013", "RES000012"}, // newest first
		"fol":       {"FOL000026"},
		"305":       {"305"},
		"zzz":       nil,
	}
	for q, want := range cases {
		got := labels(f.search(t, f.admin, q))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("q=%q: got %v, want %v", q, got, want)
		}
	}
	// the other filters still apply with q
	pidv := f.propID
	only, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pidv, Q: "RES", Action: "none.such"}, 0, 50)
	must(t, err)
	if len(only) != 0 {
		t.Fatalf("q with another filter: %d", len(only))
	}
	// and so does paging: a page of one, then the next
	first, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pidv, Q: "RES"}, 0, 1)
	must(t, err)
	next, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pidv, Q: "RES"}, first[0].ID, 1)
	must(t, err)
	if len(first) != 1 || len(next) != 1 || *first[0].EntityLabel != "RES000013" || *next[0].EntityLabel != "RES000012" {
		t.Fatalf("paging with q: %v %v", labels(first), labels(next))
	}
}

func TestQTakesBackslashPercentAndUnderscoreLiterally(t *testing.T) {
	f := setup(t)
	pid := f.propID
	for _, l := range []string{"50%OFF", "AB_CD", `a\b-1`, "ABXCD", "5000OFF", "plain"} {
		f.writeEntry(t, f.tenantID, &pid, l, "x.created")
	}
	cases := map[string][]string{
		"50%":     {"50%OFF"}, // not "50" followed by anything
		"%OFF":    {"50%OFF"}, // a percent is not a wildcard
		"B_C":     {"AB_CD"},  // an underscore is not a single character: ABXCD is not found
		`a\b`:     {`a\b-1`},  // a backslash is not an escape
		`\%`:      nil,        // nothing holds a backslash and then a percent
		"%%%":     nil,        // three percent signs are three characters of text, not "everything"
		"___":     nil,
		"b_c":     {"AB_CD"}, // in any case
		"50%off":  {"50%OFF"},
		`%\_`:     nil,
		"%_%_%":   nil,
		"5000OFF": {"5000OFF"},
	}
	for q, want := range cases {
		got := labels(f.search(t, f.admin, q))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("q=%q: got %v, want %v", q, got, want)
		}
	}
}

func TestQDoesNotSeeAnotherTenantOrAnotherProperty(t *testing.T) {
	f := setup(t)
	pid := f.propID
	// the same label in the property that is searched, in another property of the tenant, in another tenant, and in the tenant-level trail
	other := f.Property(t, f.tenantID, "UBUD")
	otherTenant := f.Tenant(t, "XYZ")
	foreign := f.Property(t, otherTenant.ID, "SGP")
	f.writeEntry(t, f.tenantID, &pid, "SHARED-1", "x.created")
	f.writeEntry(t, f.tenantID, &other.ID, "SHARED-2", "x.created")
	f.writeEntry(t, otherTenant.ID, &foreign.ID, "SHARED-3", "x.created")
	f.writeEntry(t, f.tenantID, nil, "SHARED-4", "x.created")
	if got := labels(f.search(t, f.admin, "SHARED")); strings.Join(got, ",") != "SHARED-1" {
		t.Fatalf("property search: %v", got)
	}
	// the tenant-level trail: only its own, whatever the label
	tenantLevel, err := f.reader.Search(f.admin, auditlog.Filter{Q: "SHARED"}, 0, 50)
	must(t, err)
	if got := labels(tenantLevel); strings.Join(got, ",") != "SHARED-4" {
		t.Fatalf("tenant-level search: %v", got)
	}
	// the other tenant's administrator finds only theirs, in their property; and cannot ask for ours
	theirs, err := f.reader.Search(roomstest.Admin(otherTenant.ID), auditlog.Filter{PropertyID: &foreign.ID, Q: "SHARED"}, 0, 50)
	must(t, err)
	if got := labels(theirs); strings.Join(got, ",") != "SHARED-3" {
		t.Fatalf("other tenant: %v", got)
	}
	_, err = f.reader.Search(roomstest.Admin(otherTenant.ID), auditlog.Filter{PropertyID: &pid, Q: "SHARED"}, 0, 50)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

func TestTheSearchTextIsThreeToSixtyFourCharacters(t *testing.T) {
	f := setup(t)
	h := auditlog.NewHandler(f.reader)
	mux := http.NewServeMux()
	h.Register(mux)
	call := func(q string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/audit-logs?q="+q, nil).WithContext(f.admin)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec.Code
	}
	for q, want := range map[string]int{
		"ab":                          http.StatusUnprocessableEntity, // under 3
		"%20ab%20":                    http.StatusUnprocessableEntity, // blanks do not count
		"%E6%97%A5%E6%9C%AC%E8%AA%9E": http.StatusOK,                  // 3 characters, 9 bytes: counted as characters
		"abc":                         http.StatusOK,
		strings.Repeat("a", 64):       http.StatusOK,
		strings.Repeat("a", 65):       http.StatusUnprocessableEntity,
		strings.Repeat("%C3%A9", 64):  http.StatusOK,
	} {
		if got := call(q); got != want {
			t.Fatalf("q=%q: status %d, want %d", q, got, want)
		}
	}
}
