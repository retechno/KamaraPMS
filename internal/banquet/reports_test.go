package banquet_test

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kamarapms/internal/banquet"
)

// The reports of 36-banquet.md 4.10 (B15) on a known scenario, with the CSV of each.
//
// The business date is 2026-09-30. September has two definite events on that day: Rapat (room A, 12:00-16:00, 40 guaranteed, 45 came, the package, posted) and Gala (the Ballroom, which is A + B, 18:00-21:00,
// 50 guaranteed, 40 came, not posted). October has Seminar (definite, 100 guests), Workshop (tentative, 50 guests) and Cancelled (tentative, then cancelled); November has Retreat (definite, 20 guests).
// Lost (tentative, then lost for the price). The package costs 363,000 a guest, priced ++, so 100 guests are 36,300,000 net and 43,923,000 with service and tax (x 1.21).
type rfx struct {
	*mfx
	mux *http.ServeMux
}

func newReportsFx(t *testing.T) *rfx {
	t.Helper()
	f := setup(t)
	r := &rfx{mfx: f.money(t), mux: http.NewServeMux()}
	banquet.NewHandler(f.svc).Register(r.mux)
	return r
}

// book makes an event with one session in the room and the package, and returns its ids.
func (r *rfx) book(t *testing.T, name, date string, room int64, start, end string, guaranteed int) (eventID, sessionID int64) {
	t.Helper()
	ev := r.event(t, name)
	in := r.session(room, date, start, end, false)
	in.PaxGuaranteed = ptr(guaranteed)
	ev, err := r.svc.AddSession(r.office, r.propID, ev.ID, in)
	must(t, err)
	ev, err = r.svc.MakeTentative(r.office, r.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-25")})
	must(t, err)
	r.packageLine(t, ev.ID, ev.Sessions[0].ID, "EXCLUSIVE")
	return ev.ID, ev.Sessions[0].ID
}

func (r *rfx) scenario(t *testing.T) {
	t.Helper()
	rapat, rapatS := r.book(t, "Rapat", "2026-09-30", r.a.ID, "12:00", "16:00", 40)
	gala, _ := r.book(t, "Gala", "2026-09-30", r.ballroom.ID, "18:00", "21:00", 50)
	seminar, _ := r.book(t, "Seminar", "2026-10-10", r.a.ID, "12:00", "16:00", 100)
	r.book(t, "Workshop", "2026-10-12", r.a.ID, "12:00", "16:00", 50)
	cancelled, _ := r.book(t, "Cancelled", "2026-10-20", r.a.ID, "12:00", "16:00", 10)
	retreat, _ := r.book(t, "Retreat", "2026-11-05", r.a.ID, "12:00", "16:00", 20)
	lostEv, _ := r.book(t, "Lost", "2026-10-25", r.a.ID, "12:00", "16:00", 10)
	for _, id := range []int64{rapat, gala, seminar, retreat} {
		r.confirm(t, id)
	}
	// the guests that came
	_, err := r.svc.SetActual(r.office, r.propID, rapat, rapatS, banquet.ActualInput{PaxActual: ptr(45)})
	must(t, err)
	var galaS int64
	must(t, r.Pool.QueryRow(t.Context(), `SELECT id FROM banquet_sessions WHERE event_id = $1`, gala).Scan(&galaS))
	_, err = r.svc.SetActual(r.office, r.propID, gala, galaS, banquet.ActualInput{PaxActual: ptr(40)})
	must(t, err)
	_, err = r.svc.PostSession(r.office, r.propID, rapat, rapatS, banquet.PostInput{})
	must(t, err)
	_, err = r.svc.MarkLost(r.mgr, r.propID, lostEv, banquet.CloseInput{ReasonID: r.lostReason})
	must(t, err)
	_, err = r.svc.CancelEvent(r.mgr, r.propID, cancelled, banquet.CloseInput{ReasonID: r.cancelReason})
	must(t, err)
	// the events were opened on a known day, whatever day the test runs on
	_, err = r.Pool.Exec(t.Context(), `UPDATE banquet_events SET inquiry_at = '2026-09-10T03:00:00Z' WHERE property_id = $1`, r.propID)
	must(t, err)
}

// csvOf asks for the CSV of a report as the person with the context and parses it.
func (r *rfx) csvOf(t *testing.T, as func(*http.Request) *http.Request, report, query string) [][]string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/banquet/reports/%s?format=csv&%s", r.propID, report, query), nil)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, as(req))
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("%s: %d %s %s", report, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	rows, err := csv.NewReader(rec.Body).ReadAll()
	must(t, err)
	return rows
}

func TestTheForecastKeepsTheDefiniteAndTheTentativeOnTheirOwnRows(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	v, err := r.svc.Forecast(r.reader, r.propID, day("2026-10-01"), day("2026-11-30"), false)
	must(t, err)
	if len(v.Months) != 2 || v.Months[0].Key != "2026-10" || v.Months[1].Key != "2026-11" {
		t.Fatalf("the months: %+v", v.Months)
	}
	for i, w := range []struct {
		def, ten     string
		defEv, tenEv int
	}{{"36300000", "18150000", 1, 1}, {"7260000", "0", 1, 0}} {
		m := v.Months[i]
		if !m.Definite.Equal(dec(w.def)) || !m.Tentative.Equal(dec(w.ten)) || m.DefiniteEvents != w.defEv || m.TentativeEvents != w.tenEv {
			t.Errorf("%s: %+v, want definite %s (%d) tentative %s (%d)", m.Key, m, w.def, w.defEv, w.ten, w.tenEv)
		}
	}
	if !v.Definite.Equal(dec("43560000")) || !v.Tentative.Equal(dec("18150000")) {
		t.Fatalf("the totals are kept apart: definite %s tentative %s", v.Definite, v.Tentative)
	}
	// by sales: one person sold it all; by type: no type was given
	if len(v.BySales) != 1 || !v.BySales[0].Definite.Equal(dec("43560000")) || len(v.ByType) != 1 {
		t.Fatalf("by sales %+v by type %+v", v.BySales, v.ByType)
	}
	// with service and tax the amounts are the totals of the engine
	w, err := r.svc.Forecast(r.reader, r.propID, day("2026-10-01"), day("2026-11-30"), true)
	must(t, err)
	if !w.WithTax || !w.Months[0].Definite.Equal(dec("43923000")) || !w.Months[0].Tentative.Equal(dec("21961500")) {
		t.Fatalf("with service and tax: %+v", w.Months[0])
	}
	// the sales office also watches the sessions that wait for posting (Gala) and the conversion of the last 90 days: 4 definite, 1 lost
	if v.Unposted != 1 || v.UnpostedOldestDays != 0 || v.Conversion == nil || !v.Conversion.Equal(dec("80")) {
		t.Fatalf("unposted %d (%d days), conversion %v", v.Unposted, v.UnpostedOldestDays, v.Conversion)
	}
	// a tentative event whose option date has gone is counted
	_, err = r.Pool.Exec(t.Context(), `UPDATE banquet_events SET option_until = '2026-09-01' WHERE property_id = $1 AND name = 'Workshop'`, r.propID)
	must(t, err)
	v, err = r.svc.Forecast(r.reader, r.propID, day("2026-10-01"), day("2026-11-30"), false)
	must(t, err)
	if v.TentativeOverdue != 1 {
		t.Fatalf("overdue options: %d", v.TentativeOverdue)
	}
	// the CSV
	rows := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "forecast", "from=2026-10-01&to=2026-11-30")
	if strings.Join(rows[0], ",") != "group,key,name,definite,definite_events,tentative,tentative_events" || len(rows) != 1+2+1+1 {
		t.Fatalf("csv: %v", rows)
	}
	if rows[1][0] != "month" || rows[1][1] != "2026-10" || !dec(rows[1][3]).Equal(dec("36300000")) || !dec(rows[1][5]).Equal(dec("18150000")) {
		t.Fatalf("csv october: %v", rows[1])
	}
	id := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "forecast", "from=2026-10-01&to=2026-11-30&lang=id")
	if strings.Join(id[0], ",") != "Kelompok,Kunci,Nama,Definite,Acara definite,Tentative,Acara tentative" {
		t.Fatalf("the header in Indonesian: %v", id[0])
	}
}

func TestTheRoomReportCountsTheHoursOnceAndTheRevenueOfWhatWasPosted(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	v, err := r.svc.Rooms(r.reader, r.propID, day("2026-09-01"), day("2026-09-30"))
	must(t, err)
	if v.Days != 30 || v.OperatingFrom.String() != "07:00" || v.OperatingTo.String() != "23:00" {
		t.Fatalf("the period and the hours of operation: %+v", v)
	}
	by := map[string]banquet.RoomRow{}
	for _, x := range v.Rooms {
		by[x.Code] = x
	}
	// A: Rapat 4 hours + Gala (the Ballroom holds A) 3 hours; B: 3; the Ballroom: its own 3 + A's 4; C: none; the Garden (B + C) shares B with the Ballroom: 3
	for code, want := range map[string]string{"A": "7", "B": "3", "BALL": "7", "C": "0", "GARDEN": "3"} {
		if got := by[code].Hours; !got.Equal(dec(want)) {
			t.Errorf("room %s: %s hours, want %s", code, got, want)
		}
	}
	if !by["A"].AvailableHours.Equal(dec("480")) || by["A"].OccupancyPct == nil || !by["A"].OccupancyPct.Equal(dec("1.5")) {
		t.Fatalf("7 of 480 hours is 1.5%%: %+v", by["A"])
	}
	if by["A"].Sessions != 1 || by["BALL"].Sessions != 1 {
		t.Fatalf("sessions held in the room itself: A %d BALL %d", by["A"].Sessions, by["BALL"].Sessions)
	}
	// the revenue is the net of the session that was posted: 45 billed guests x 363,000
	if !by["A"].Revenue.Equal(dec("16335000")) || by["A"].PostedSessions != 1 || !v.Revenue.Equal(dec("16335000")) {
		t.Fatalf("revenue of room A %s, total %s", by["A"].Revenue, v.Revenue)
	}
	if by["A"].RevenuePerHour == nil || !by["A"].RevenuePerHour.Equal(dec("2333571.43")) {
		t.Fatalf("revenue per hour %v", by["A"].RevenuePerHour)
	}
	if by["B"].RevenuePerHour == nil || !by["B"].Revenue.IsZero() {
		t.Fatalf("B sold nothing yet: %+v", by["B"])
	}
	rows := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "rooms", "from=2026-09-01&to=2026-09-30")
	if rows[0][0] != "room_code" || rows[1][0] != "A" || !dec(rows[1][3]).Equal(dec("7")) || !dec(rows[1][7]).Equal(dec("16335000")) {
		t.Fatalf("csv: %v", rows[:2])
	}
}

func TestTheGuestsReportSetsTheGuaranteeAgainstWhoCame(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	v, err := r.svc.Guests(r.reader, r.propID, day("2026-09-01"), day("2026-09-30"))
	must(t, err)
	if len(v.Rows) != 2 || v.ComparedCount != 2 || v.Guaranteed != 90 || v.Actual != 85 || v.Difference != -5 || v.Over != 1 || v.Under != 1 {
		t.Fatalf("guests: %+v", v)
	}
	if d := v.Rows[0].Difference; v.Rows[0].EventName != "Rapat" || d == nil || *d != 5 {
		t.Fatalf("Rapat: 45 came against 40: %+v", v.Rows[0])
	}
	if d := v.Rows[1].Difference; v.Rows[1].EventName != "Gala" || d == nil || *d != -10 {
		t.Fatalf("Gala: 40 came against 50: %+v", v.Rows[1])
	}
	rows := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "guests", "from=2026-09-01&to=2026-09-30")
	if len(rows) != 3 || rows[1][3] != "Session" || rows[1][6] != "40" || rows[1][7] != "45" || rows[1][8] != "5" {
		t.Fatalf("csv: %v", rows)
	}
}

func TestTheProductionOfSalesIsDefiniteOverDefinitePlusLost(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	v, err := r.svc.Sales(r.reader, r.propID, day("2026-09-01"), day("2026-10-31"))
	must(t, err)
	if len(v.Rows) != 1 {
		t.Fatalf("one sales person: %+v", v.Rows)
	}
	x := v.Rows[0]
	// opened: Rapat, Gala, Seminar, Workshop, Cancelled, Retreat, Lost; definite: Rapat, Gala, Seminar, Retreat; lost: Lost
	if x.Created != 7 || x.Definite != 4 || x.Lost != 1 || x.Conversion == nil || !x.Conversion.Equal(dec("80")) {
		t.Fatalf("production: %+v", x)
	}
	if v.Total.Definite != 4 || v.Total.Lost != 1 || v.Total.Conversion == nil || !v.Total.Conversion.Equal(dec("80")) {
		t.Fatalf("total: %+v", v.Total)
	}
	// a period before everything has nobody to count
	e, err := r.svc.Sales(r.reader, r.propID, day("2026-01-01"), day("2026-01-31"))
	must(t, err)
	if len(e.Rows) != 0 || e.Total.Conversion != nil {
		t.Fatalf("an empty period: %+v", e)
	}
	rows := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "sales", "from=2026-09-01&to=2026-10-31")
	last := rows[len(rows)-1]
	if rows[0][0] != "sales" || last[0] != "Total" || last[2] != "4" || last[6] != "80" {
		t.Fatalf("csv: %v", rows)
	}
}

func TestTheLostAndCancelledReportGroupsByReason(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	v, err := r.svc.Lost(r.reader, r.propID, day("2026-09-01"), day("2026-10-31"))
	must(t, err)
	if v.Lost != 1 || v.Cancelled != 1 || len(v.Rows) != 2 || len(v.Groups) != 2 {
		t.Fatalf("lost: %+v", v)
	}
	seen := map[string]int{}
	for _, g := range v.Groups {
		seen[g.Status+"/"+g.ReasonCode] += g.Events
	}
	if seen["LOST/PRICE"] != 1 || seen["CANCELLED/CLIENT"] != 1 {
		t.Fatalf("the reasons: %+v", v.Groups)
	}
	for _, x := range v.Rows {
		if x.ClosedOn.IsZero() || x.SalesName == "" {
			t.Errorf("a row says when it closed and who sold it: %+v", x)
		}
	}
	rows := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "lost", "from=2026-09-01&to=2026-10-31")
	if rows[0][0] != "closed_on" || len(rows) != 3 {
		t.Fatalf("csv: %v", rows)
	}
}

func TestTheDailyScheduleListsTheSessionsOfTheDayByTheirStart(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	v, err := r.svc.Schedule(r.reader, r.propID, day("2026-09-30"))
	must(t, err)
	if len(v.Rows) != 2 || v.Rows[0].EventName != "Rapat" || v.Rows[1].EventName != "Gala" {
		t.Fatalf("the sessions by start time: %+v", v.Rows)
	}
	g := v.Rows[1]
	if g.StartTime.String() != "18:00" || g.EndTime.String() != "21:00" || g.RoomName == nil || *g.RoomName != "BALL" || g.PaxGuaranteed == nil || *g.PaxGuaranteed != 50 || g.PaxActual == nil || *g.PaxActual != 40 {
		t.Fatalf("Gala: %+v", g)
	}
	// the default is the business date
	d, err := r.svc.Schedule(r.reader, r.propID, day("0001-01-01"))
	if err == nil && len(d.Rows) != 2 {
		t.Fatalf("the business date by default: %+v", d)
	}
	rows := r.csvOf(t, func(q *http.Request) *http.Request { return q.WithContext(r.reader) }, "schedule", "day=2026-09-30")
	if len(rows) != 3 || rows[1][0] != "12:00" || rows[2][2] != "BALL" {
		t.Fatalf("csv: %v", rows)
	}
}

func TestTheUnpostedReportHasACsvToo(t *testing.T) {
	r := newReportsFx(t)
	r.scenario(t)
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/banquet/reports/unposted?format=csv&lang=id", r.propID), nil).WithContext(r.reader)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	rows, err := csv.NewReader(rec.Body).ReadAll()
	must(t, err)
	if rec.Code != http.StatusOK || len(rows) != 2 || rows[0][0] != "Tanggal" || rows[1][2] != "Gala" {
		t.Fatalf("csv: %d %v", rec.Code, rows)
	}
}

func TestTheReportsAreForWhoWorksWithEventsOrReadsAndForNoOneElse(t *testing.T) {
	r := newReportsFx(t)
	if _, err := r.svc.Forecast(r.nobody, r.propID, day("2026-10-01"), day("2026-10-31"), false); err == nil {
		t.Fatal("a person with no banquet permission reads no forecast")
	}
	if _, err := r.svc.Rooms(r.nobody, r.propID, day("2026-10-01"), day("2026-10-31")); err == nil {
		t.Fatal("nor the rooms")
	}
	if _, err := r.svc.Guests(r.nobody, r.propID, day("2026-10-01"), day("2026-10-31")); err == nil {
		t.Fatal("nor the guests")
	}
	if _, err := r.svc.Sales(r.nobody, r.propID, day("2026-10-01"), day("2026-10-31")); err == nil {
		t.Fatal("nor the production")
	}
	if _, err := r.svc.Lost(r.nobody, r.propID, day("2026-10-01"), day("2026-10-31")); err == nil {
		t.Fatal("nor the lost")
	}
	if _, err := r.svc.Schedule(r.nobody, r.propID, day("2026-10-01")); err == nil {
		t.Fatal("nor the schedule")
	}
	if _, err := r.svc.Forecast(r.reader, r.propID, day("2026-10-31"), day("2026-10-01"), false); err == nil {
		t.Fatal("a period that ends before it starts is refused")
	}
	if _, err := r.svc.Forecast(r.reader, r.propID, day("2025-01-01"), day("2026-10-01"), false); err == nil {
		t.Fatal("more than 400 days is refused")
	}
	// the other property of the tenant is not seen: the request names a property the person is not assigned to
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/properties/%d/banquet/reports/forecast", r.propID+999), nil).WithContext(r.reader))
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Fatalf("another property: %d", rec.Code)
	}
}
