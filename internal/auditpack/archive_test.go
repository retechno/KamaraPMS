package auditpack_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"kamarapms/internal/auditpack"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomstest"
)

var roomstestWant = roomstest.Want

// closedWithoutPack closes the first day with the night audit not building packs (a day from before the feature).
func closedWithoutPack(t *testing.T, f *fx) civil.Date {
	t.Helper()
	f.Audit.SetPackBuilder(nil)
	f.stay(t, f.r101, "2026-10-03")
	bd := f.closeDay(t)
	f.Audit.SetPackBuilder(f.pack)
	if f.Count(t, `SELECT count(*) FROM nap_packs`) != 0 {
		t.Fatal("a pack was built")
	}
	return bd
}

func TestALateBuildMakesThePackOfADayThatHasNone(t *testing.T) {
	f := setup(t)
	bd := closedWithoutPack(t, f)
	// the open day has nothing to build, a day that never was has none, and the permission is asked first
	open, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	_, err = f.pack.Rebuild(f.admin, f.propID, open.BusinessDate)
	roomstestWant(t, err, "DATE_NOT_CLOSED")
	_, err = f.pack.Rebuild(f.admin, f.propID, d("2020-01-01"))
	roomstestWant(t, err, "BUSINESS_DAY_NOT_FOUND")
	viewer := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackView)
	_, err = f.pack.Rebuild(viewer, f.propID, bd)
	roomstestWant(t, err, "PERMISSION_DENIED")

	m, err := f.pack.Rebuild(f.admin, f.propID, bd)
	must(t, err)
	if m.Status != auditpack.PackComplete || !m.BuiltLate || len(m.Sections) != len(auditpack.Catalog) {
		t.Fatalf("manifest: %+v", m)
	}
	for _, s := range m.Sections {
		want := auditpack.StatusOK
		switch s.Section {
		case auditpack.SecInHouse, auditpack.SecNextDay, auditpack.SecAuditNotes, auditpack.SecOutOfOrder: // what only the close of the night saw
			want = auditpack.StatusNotAvailable
		case auditpack.SecZReports, auditpack.SecOutlets:
			want = auditpack.StatusOff
		}
		if s.Status != want || !s.BuiltLate {
			t.Errorf("%s: %s late %v, want %s", s.Section, s.Status, s.BuiltLate, want)
		}
		if s.Rebuildable != auditpack.Rebuildable(s.Section) {
			t.Errorf("%s: rebuildable %v", s.Section, s.Rebuildable)
		}
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'nap_pack.built' AND new_data ->> 'built_late' = 'true'`) != 1 {
		t.Fatal("the late build is not in the audit trail")
	}
	// the summary of the late pack is the one the close stored, and the flash blanks the rows about tomorrow
	if sum := f.stored(t, bd, auditpack.SecSummary).(map[string]any); sum["closed_by"] != "Admin" {
		t.Fatalf("summary: %v", sum)
	}
	for _, row := range f.stored(t, bd, auditpack.SecFlash).(map[string]any)["activity"].([]any) {
		if k := row.(map[string]any)["key"]; (k == "arrivals_tomorrow" || k == "departures_tomorrow") && row.(map[string]any)["today"] != "" {
			t.Errorf("%v is not blank: %v", k, row)
		}
	}
	// again: the pack exists and nothing failed, so nothing changes
	again, err := f.pack.Rebuild(f.admin, f.propID, bd)
	must(t, err)
	if again.BuiltAt != m.BuiltAt || f.Count(t, `SELECT count(*) FROM nap_packs`) != 1 {
		t.Fatal("a second rebuild built another pack")
	}
}

func TestARebuildBuildsAgainTheSectionsThatFailed(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	broke := func(context.Context) (any, error) { return nil, errors.New("broke") }
	f.pack.Override(auditpack.SecRevenue, broke)
	f.pack.Override(auditpack.SecInHouse, broke)
	bd := f.closeDay(t)
	if s := f.statuses(t, bd); s[auditpack.SecRevenue] != auditpack.StatusFailed || s[auditpack.SecInHouse] != auditpack.StatusFailed {
		t.Fatalf("statuses: %v", s)
	}

	// the next audit says which days have a pack that is not complete
	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if len(pv.Warnings.PackIncomplete) != 1 || pv.Warnings.PackIncomplete[0] != bd {
		t.Fatalf("warning: %v", pv.Warnings.PackIncomplete)
	}

	f.pack.Override(auditpack.SecRevenue, nil)
	m, err := f.pack.Rebuild(f.admin, f.propID, bd)
	must(t, err)
	got := map[string]auditpack.SectionInfo{}
	for _, s := range m.Sections {
		got[s.Section] = s
	}
	if got[auditpack.SecRevenue].Status != auditpack.StatusOK || !got[auditpack.SecRevenue].BuiltLate || got[auditpack.SecRevenue].Error != "" {
		t.Fatalf("revenue: %+v", got[auditpack.SecRevenue])
	}
	// the in-house list of that night is gone for good: the section stays failed, the pack stays partial
	if got[auditpack.SecInHouse].Status != auditpack.StatusFailed || m.Status != auditpack.PackPartial {
		t.Fatalf("in house: %+v, pack %s", got[auditpack.SecInHouse], m.Status)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'nap_pack.rebuilt'`) != 1 {
		t.Fatal("the rebuild is not in the audit trail")
	}
	// only a section that cannot be made again is left: the rebuild says so
	_, err = f.pack.Rebuild(f.admin, f.propID, bd)
	roomstestWant(t, err, "SECTION_NOT_REBUILDABLE")
	if pv, err = f.Audit.Preview(f.admin, f.propID); err != nil || len(pv.Warnings.PackIncomplete) != 1 {
		t.Fatalf("the pack is still partial: %v %v", pv.Warnings.PackIncomplete, err)
	}
}

func TestARebuildThatFixesEverythingCompletesThePack(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.pack.Override(auditpack.SecFlash, func(context.Context) (any, error) { return nil, errors.New("broke") })
	bd := f.closeDay(t)
	f.pack.Override(auditpack.SecFlash, nil)
	m, err := f.pack.Rebuild(f.admin, f.propID, bd)
	must(t, err)
	if m.Status != auditpack.PackComplete || f.statuses(t, bd)[auditpack.SecFlash] != auditpack.StatusOK {
		t.Fatalf("manifest: %+v", m)
	}
	pv, err := f.Audit.Preview(f.admin, f.propID)
	must(t, err)
	if len(pv.Warnings.PackIncomplete) != 0 {
		t.Fatalf("warning: %v", pv.Warnings.PackIncomplete)
	}
}

func TestTheArchiveReadsWhatThePackKept(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.Docs.SetPacks(f.pack)
	f.pack.SetPDF(f.Docs)
	bd := f.closeDay(t)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackView)

	list, err := f.pack.List(viewer, f.propID, auditpack.ListInput{})
	must(t, err)
	if len(list.Items) != 1 || list.Items[0].BusinessDate != bd || list.Items[0].SectionsOK != 13 || list.NextCursor != nil {
		t.Fatalf("list: %+v", list)
	}
	m, err := f.pack.Manifest(viewer, f.propID, bd)
	must(t, err)
	if m.BuiltBy != "Admin" || len(m.Sections) != 15 || m.Sections[0].Section != auditpack.SecSummary || m.Sections[0].SizeBytes == 0 {
		t.Fatalf("manifest: %+v", m)
	}
	_, err = f.pack.Manifest(viewer, f.propID, d("2026-01-01"))
	roomstestWant(t, err, "PACK_NOT_FOUND")
	if _, err = f.pack.SectionData(viewer, f.propID, bd, "FORECAST"); err == nil {
		t.Fatal("a section that is not in the catalog was served")
	}
	roomstestWant(t, err, "PACK_SECTION_INVALID")
	_, err = f.pack.SectionData(viewer, f.propID, bd, auditpack.SecZReports) // off: nothing to download
	roomstestWant(t, err, "PACK_NOT_FOUND")
	raw, err := f.pack.SectionData(viewer, f.propID, bd, auditpack.SecRevenue)
	must(t, err)
	var rev map[string]any
	must(t, json.Unmarshal(raw, &rev))
	if rev["from"] != bd.String() {
		t.Fatalf("revenue: %v", rev)
	}

	name, pdf, err := f.pack.PDF(viewer, f.propID, bd, []string{auditpack.SecSummary, auditpack.SecRevenue})
	must(t, err)
	if name != "night-audit-pack-"+bd.String()+".pdf" || !strings.HasPrefix(string(pdf), "%PDF-") {
		t.Fatalf("pdf %s", name)
	}
	_, _, err = f.pack.PDF(viewer, f.propID, bd, []string{auditpack.SecZReports}) // nothing in the list holds data
	roomstestWant(t, err, "PACK_SECTION_INVALID")

	// the permission is asked first, and another tenant sees nothing
	none := f.User(t, f.tenantID, f.propID, auth.PermReportView)
	_, err = f.pack.List(none, f.propID, auditpack.ListInput{})
	roomstestWant(t, err, "PERMISSION_DENIED")
	_, _, err = f.pack.PDF(none, f.propID, bd, nil)
	roomstestWant(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	stranger, _ := f.AdminAccount(t, other.ID)
	_, err = f.pack.Manifest(stranger, f.propID, bd)
	roomstestWant(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.pack.Rebuild(stranger, f.propID, bd)
	roomstestWant(t, err, "PROPERTY_NOT_FOUND")
}

func TestTheArchiveOverHTTP(t *testing.T) {
	f := setup(t)
	f.stay(t, f.r101, "2026-10-03")
	f.Docs.SetPacks(f.pack)
	f.pack.SetPDF(f.Docs)
	bd := f.closeDay(t)
	mux := http.NewServeMux()
	auditpack.NewHandler(f.pack).Register(mux)
	base := "/api/v1/properties/" + strconv.FormatInt(f.propID, 10) + "/night-audit/packs"
	call := func(ctx context.Context, method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, method, base+path, nil))
		return rec
	}
	viewer := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackView)
	manager := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackManage)

	if rec := call(viewer, "GET", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), bd.String()) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	if rec := call(viewer, "GET", "/"+bd.String()); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"sections"`) {
		t.Fatalf("manifest: %d %s", rec.Code, rec.Body)
	}
	rec := call(viewer, "GET", "/"+bd.String()+".pdf?sections=SUMMARY,REVENUE")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(rec.Body.String(), "%PDF-") || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("pdf: %d %s", rec.Code, rec.Header())
	}
	if rec := call(viewer, "GET", "/"+bd.String()+".pdf?sections=NOPE"); rec.Code != 422 || !strings.Contains(rec.Body.String(), "PACK_SECTION_INVALID") {
		t.Fatalf("pdf of a section that does not exist: %d %s", rec.Code, rec.Body)
	}
	if rec := call(viewer, "GET", "/"+bd.String()+"/sections/REVENUE"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"by_charge_code"`) {
		t.Fatalf("section: %d %s", rec.Code, rec.Body)
	}
	if rec := call(viewer, "GET", "/"+bd.String()+"/sections/REVENUE?format=csv"); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(rec.Body.String(), "charge_code") {
		t.Fatalf("csv: %d %s", rec.Code, rec.Body)
	}
	if rec := call(viewer, "GET", "/"+bd.String()+"/sections/AUDIT_NOTES?format=csv"); rec.Code != 422 {
		t.Fatalf("csv of a section that is not a table: %d %s", rec.Code, rec.Body)
	}
	// the permission comes before the path is read: a bad date is a 403 for a user without the permission, a 422 for one who has it
	if rec := call(manager, "GET", "/not-a-date"); rec.Code != 403 {
		t.Fatalf("manager reads the archive: %d", rec.Code)
	}
	if rec := call(viewer, "GET", "/not-a-date"); rec.Code != 422 {
		t.Fatalf("bad date: %d", rec.Code)
	}
	if rec := call(viewer, "POST", "/"+bd.String()+"/rebuild"); rec.Code != 403 {
		t.Fatalf("viewer rebuilds: %d", rec.Code)
	}
	if rec := call(manager, "POST", "/not-a-date/rebuild"); rec.Code != 422 {
		t.Fatalf("bad date on rebuild: %d", rec.Code)
	}
	if rec := call(viewer, "POST", "/not-a-date/rebuild"); rec.Code != 403 {
		t.Fatalf("viewer rebuilds a bad date: %d", rec.Code)
	}
	if rec := call(manager, "POST", "/"+bd.String()+"/rebuild"); rec.Code != 200 {
		t.Fatalf("rebuild: %d %s", rec.Code, rec.Body)
	}
	if rec := call(viewer, "GET", "/2026-01-01"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "PACK_NOT_FOUND") {
		t.Fatalf("no pack: %d %s", rec.Code, rec.Body)
	}
	other := f.Tenant(t, "XYZ")
	stranger, _ := f.AdminAccount(t, other.ID)
	if rec := call(stranger, "GET", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "PROPERTY_NOT_FOUND") {
		t.Fatalf("another tenant: %d %s", rec.Code, rec.Body)
	}
}
