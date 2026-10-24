package banquet_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

func ptr[T any](v T) *T { return &v }

func tod(s string) *civil.TimeOfDay { t := civil.MustParseTimeOfDay(s); return &t }
func day(s string) civil.Date       { return civil.MustParseDate(s) }

type fx struct {
	*roomstest.Env
	svc              *banquet.Service
	tenantID, propID int64
	admin            context.Context
	mgr              context.Context // read, manage, confirm and setup
	office           context.Context // manage only
	reader           context.Context
	nobody           context.Context
	salesID          int64
	guestID          int64
	lostReason       int64
	cancelReason     int64
	// the rooms: A and B (60 minutes of set-up, 30 of tear-down), C, the Ballroom (A + B) and the Garden (B + C)
	a, b, c, ballroom, garden banquet.Room
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func code(t *testing.T, err error, want string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != want {
		t.Fatalf("got %v, want %s", err, want)
	}
	return e
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: roomstest.Admin(tn.ID)}
	f.svc = banquet.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy)
	f.mgr = e.User(t, tn.ID, p.ID, auth.PermBanquetRead, auth.PermBanquetManage, auth.PermBanquetConfirm, auth.PermBanquetSetup)
	f.office = e.User(t, tn.ID, p.ID, auth.PermBanquetManage)
	f.reader = e.User(t, tn.ID, p.ID, auth.PermBanquetRead)
	f.nobody = e.User(t, tn.ID, p.ID, auth.PermFolioRead)
	pr, err := auth.Require(f.office)
	must(t, err)
	f.salesID = pr.UserID
	ctx := context.Background()
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G1', 'Wijaya', $2) RETURNING id`, tn.ID, p.ID).Scan(&f.guestID))
	r, err := f.svc.CreateReason(f.mgr, p.ID, banquet.ReasonInput{Kind: banquet.ReasonLost, Code: "PRICE", Name: "Price"})
	must(t, err)
	f.lostReason = r.ID
	r, err = f.svc.CreateReason(f.mgr, p.ID, banquet.ReasonInput{Kind: banquet.ReasonCancel, Code: "CLIENT", Name: "Client cancelled"})
	must(t, err)
	f.cancelReason = r.ID
	room := func(code string, combo bool, setup, tear int) banquet.Room {
		rm, err := f.svc.CreateRoom(f.mgr, p.ID, banquet.RoomInput{Code: code, Name: code, SetupMinutes: setup, TeardownMinutes: tear, IsCombo: combo, SortOrder: 1})
		must(t, err)
		return rm
	}
	f.a, f.b, f.c = room("A", false, 60, 30), room("B", false, 60, 30), room("C", false, 60, 30)
	f.ballroom, f.garden = room("BALL", true, 60, 30), room("GARDEN", true, 60, 30)
	f.ballroom, err = f.svc.SetRoomParts(f.mgr, p.ID, f.ballroom.ID, banquet.PartsInput{RoomIDs: []int64{f.a.ID, f.b.ID}})
	must(t, err)
	f.garden, err = f.svc.SetRoomParts(f.mgr, p.ID, f.garden.ID, banquet.PartsInput{RoomIDs: []int64{f.b.ID, f.c.ID}})
	must(t, err)
	for _, rm := range []banquet.Room{f.a, f.b, f.c, f.ballroom, f.garden} {
		_, err = f.svc.SetRoomSetups(f.mgr, p.ID, rm.ID, banquet.SetupsInput{Setups: []banquet.RoomSetup{{SetupStyle: "THEATER", Capacity: 250}, {SetupStyle: "ROUND_TABLE", Capacity: 150}}})
		must(t, err)
	}
	return f
}

func (f *fx) mode(t *testing.T, mode string) {
	t.Helper()
	_, err := f.svc.PutSettings(f.mgr, f.propID, banquet.SettingsInput{TentativeConflictMode: mode})
	must(t, err)
}

// event opens an inquiry booked by the guest, sold by the office user.
func (f *fx) event(t *testing.T, name string) banquet.Event {
	t.Helper()
	ev, err := f.svc.CreateEvent(f.office, f.propID, banquet.EventInput{Name: name, BookerGuestID: &f.guestID, SalesUserID: &f.salesID})
	must(t, err)
	return ev
}

func (f *fx) session(roomID int64, date, start, end string, nextDay bool) banquet.SessionInput {
	return banquet.SessionInput{Name: "Session", SessionDate: day(date), StartTime: tod(start), EndTime: tod(end), EndsNextDay: nextDay, RoomID: &roomID, PaxEstimated: 100}
}

// tentative makes an event with one session in the room and holds it as an option.
func (f *fx) tentative(t *testing.T, name string, roomID int64, date, start, end string) banquet.Event {
	t.Helper()
	ev := f.event(t, name)
	_, err := f.svc.AddSession(f.office, f.propID, ev.ID, f.session(roomID, date, start, end, false))
	must(t, err)
	ev, err = f.svc.MakeTentative(f.office, f.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	must(t, err)
	return ev
}

func (f *fx) definite(t *testing.T, name string, roomID int64, date, start, end string) banquet.Event {
	t.Helper()
	ev := f.tentative(t, name, roomID, date, start, end)
	ev, err := f.svc.Confirm(f.mgr, f.propID, ev.ID)
	must(t, err)
	return ev
}

func (f *fx) rank(t *testing.T, eventID int64) int {
	t.Helper()
	ev, err := f.svc.GetEvent(f.mgr, f.propID, eventID)
	must(t, err)
	if len(ev.Sessions) == 0 || ev.Sessions[0].Rank == nil {
		t.Fatalf("event %d has no rank: %+v", eventID, ev.Sessions)
	}
	return *ev.Sessions[0].Rank
}

func (f *fx) notices(t *testing.T, ctx context.Context, kind string) int {
	t.Helper()
	list, err := f.svc.ListNotices(ctx, f.propID, false)
	must(t, err)
	n := 0
	for _, x := range list {
		if x.Kind == kind {
			n++
		}
	}
	return n
}

// assertLockOrder reads a lock trace: inside each transaction the levels never go down, the group comes before the rooms and the rooms before the events (36 6).
func assertLockOrder(t *testing.T, trace *db.LockTrace) {
	t.Helper()
	var level db.LockLevel
	rank := map[string]int{"booking_groups": 1, "banquet_rooms": 2, "banquet_events": 3}
	last := 0
	for _, ev := range trace.Events() {
		if ev.Begin {
			level, last = 0, 0
			continue
		}
		if ev.Level < level {
			t.Fatalf("lock order: level %d (%s) after level %d", ev.Level, ev.Table, level)
		}
		level = ev.Level
		if r := rank[ev.Table]; r > 0 {
			if r < last {
				t.Fatalf("lock order: %s after a later table of the events level", ev.Table)
			}
			last = r
		}
	}
}

// ---------------------------------------------------------------- 1. the calendar and the exclusion

// A second definite event in the same room and time is refused, by the use case and by the constraint itself.
func TestASecondDefiniteInTheSameRoomIsRefused(t *testing.T) {
	f := setup(t)
	f.mode(t, banquet.ModeQueue)
	f.definite(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")

	// a QUEUE option takes its place behind; it cannot become definite
	y := f.tentative(t, "Meeting", f.a.ID, "2026-11-07", "15:00", "19:00")
	_, err := f.svc.Confirm(f.mgr, f.propID, y.ID)
	_ = code(t, err, "BANQUET_OPTION_NOT_FIRST")

	// by direct SQL the exclusion answers 23P01 and the error map names it
	_, err = f.Pool.Exec(context.Background(), `UPDATE banquet_holds SET hold_kind = 'DEFINITE' WHERE event_id = $1`, y.ID)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.Code != "23P01" {
		t.Fatalf("direct SQL: got %v, want an exclusion violation", err)
	}
}

// The Ballroom (A + B) and each of its parts exclude each other, in both directions, and a base room in two combinations (B) excludes both.
func TestACombinedRoomAndItsPartsExcludeEachOther(t *testing.T) {
	f := setup(t)
	f.definite(t, "Gala", f.ballroom.ID, "2026-11-07", "12:00", "16:00") // holds A and B
	for name, room := range map[string]int64{"part A": f.a.ID, "part B": f.b.ID, "garden (B + C)": f.garden.ID, "the ballroom again": f.ballroom.ID} {
		ev := f.event(t, name)
		_, err := f.svc.AddSession(f.office, f.propID, ev.ID, f.session(room, "2026-11-07", "13:00", "15:00", false))
		must(t, err)
		_, err = f.svc.MakeTentative(f.office, f.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
		_ = code(t, err, "BANQUET_ROOM_NOT_AVAILABLE") // mode REJECT
	}
	// C alone is free
	f.definite(t, "Meeting in C", f.c.ID, "2026-11-07", "13:00", "15:00")
	// and the other direction: a part is definite, the combination that covers it is not available
	f2 := setup(t)
	f2.definite(t, "Meeting in A", f2.a.ID, "2026-11-07", "13:00", "15:00")
	ev := f2.event(t, "Gala")
	_, err := f2.svc.AddSession(f2.office, f2.propID, ev.ID, f2.session(f2.ballroom.ID, "2026-11-07", "12:00", "16:00", false))
	must(t, err)
	_, err = f2.svc.MakeTentative(f2.office, f2.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	_ = code(t, err, "BANQUET_ROOM_NOT_AVAILABLE")
}

// The set-up and the tear-down time are part of the hold: 20 minutes apart conflict, 90 minutes apart (60 + 30) do not.
func TestTheBuffersMakeTwoSessionsConflict(t *testing.T) {
	f := setup(t)
	f.definite(t, "First", f.a.ID, "2026-11-07", "10:00", "12:00") // holds 09:00 to 12:30
	ev := f.event(t, "Too close")
	_, err := f.svc.AddSession(f.office, f.propID, ev.ID, f.session(f.a.ID, "2026-11-07", "12:20", "14:00", false)) // sets up from 11:20
	must(t, err)
	_, err = f.svc.MakeTentative(f.office, f.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	_ = code(t, err, "BANQUET_ROOM_NOT_AVAILABLE")

	// starts at 13:30: sets up from 12:30, when the first session's tear-down ends (a half-open range)
	f.definite(t, "Back to back", f.a.ID, "2026-11-07", "13:30", "15:00")
}

// A session that ends after midnight holds the next morning's set-up, and an event may last several days.
func TestASessionAfterMidnightAndAMultiDayEvent(t *testing.T) {
	f := setup(t)
	night := f.event(t, "Night party")
	_, err := f.svc.AddSession(f.office, f.propID, night.ID, f.session(f.a.ID, "2026-11-07", "20:00", "02:00", true))
	must(t, err)
	night, err = f.svc.MakeTentative(f.office, f.propID, night.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	must(t, err)
	must(t, f.holdEnds(night.ID, "2026-11-08T02:30"))
	// the morning after, a meeting from 03:30 sets up from 02:30 and fits; one from 03:00 does not
	f.definite(t, "Early meeting", f.a.ID, "2026-11-08", "03:30", "05:00")
	ev := f.event(t, "Too early")
	_, err = f.svc.AddSession(f.office, f.propID, ev.ID, f.session(f.a.ID, "2026-11-08", "03:00", "05:00", false))
	must(t, err)
	_, err = f.svc.MakeTentative(f.office, f.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	_ = code(t, err, "BANQUET_ROOM_NOT_AVAILABLE")

	// three days, one session each, the third in another room
	multi := f.event(t, "Conference")
	for i, d := range []string{"2026-12-01", "2026-12-02", "2026-12-03"} {
		room := f.c.ID
		if i == 2 {
			room = f.ballroom.ID
		}
		_, err = f.svc.AddSession(f.office, f.propID, multi.ID, f.session(room, d, "09:00", "17:00", false))
		must(t, err)
	}
	multi, err = f.svc.MakeTentative(f.office, f.propID, multi.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	must(t, err)
	multi, err = f.svc.Confirm(f.mgr, f.propID, multi.ID)
	must(t, err)
	if multi.Status != banquet.StatusDefinite || len(multi.Sessions) != 3 {
		t.Fatalf("event: %+v", multi)
	}
	if n := f.Count(t, `SELECT count(*) FROM banquet_holds WHERE event_id = $1 AND hold_kind = 'DEFINITE'`, multi.ID); n != 4 { // C, C, and A + B
		t.Fatalf("%d definite holds, want 4", n)
	}
}

func (f *fx) holdEnds(eventID int64, want string) error {
	var got string
	if err := f.Pool.QueryRow(context.Background(), `SELECT to_char(max(upper(during)), 'YYYY-MM-DD"T"HH24:MI') FROM banquet_holds WHERE event_id = $1`, eventID).Scan(&got); err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("hold ends %s, want %s", got, want)
	}
	return nil
}

// A definite event with a session that has no hold cannot be committed, whatever the code does.
func TestTheDeferredGuardRefusesADefiniteSessionWithoutHolds(t *testing.T) {
	f := setup(t)
	ev := f.tentative(t, "Wedding", f.a.ID, "2026-11-07", "12:00", "16:00")
	ctx := context.Background()
	tx, err := f.Pool.Begin(ctx)
	must(t, err)
	_, err = tx.Exec(ctx, `DELETE FROM banquet_holds WHERE event_id = $1`, ev.ID)
	must(t, err)
	_, err = tx.Exec(ctx, `UPDATE banquet_events SET status = 'DEFINITE', definite_at = now(), option_until = NULL WHERE id = $1`, ev.ID)
	must(t, err)
	err = tx.Commit(ctx)
	var pg *pgconn.PgError
	if !errors.As(err, &pg) || pg.ConstraintName != "banquet_holds_missing" {
		t.Fatalf("commit: got %v, want banquet_holds_missing", err)
	}
	// and a definite event with its holds goes through
	f.definite(t, "Another", f.c.ID, "2026-11-07", "12:00", "16:00")
}

// ---------------------------------------------------------------- 2. the options

// The numbers of 36 10.4 in the mode QUEUE; in the mode REJECT the second request is refused at once.
func TestTheQueueOfOptions(t *testing.T) {
	f := setup(t)
	f.mode(t, banquet.ModeQueue)
	x := f.tentative(t, "X", f.ballroom.ID, "2026-11-07", "12:00", "16:00") // holds 11:00 to 16:30 on A and on B
	y := f.tentative(t, "Y", f.a.ID, "2026-11-07", "15:00", "19:00")        // 14:00 to 19:30 on A: behind X
	z := f.tentative(t, "Z", f.b.ID, "2026-11-07", "08:00", "10:00")        // 07:00 to 10:30 on B: nothing before it
	if f.rank(t, x.ID) != 1 || f.rank(t, y.ID) != 2 || f.rank(t, z.ID) != 1 {
		t.Fatalf("ranks %d %d %d, want 1 2 1", f.rank(t, x.ID), f.rank(t, y.ID), f.rank(t, z.ID))
	}
	// Y cannot become definite while X holds
	e, err := f.svc.Confirm(f.mgr, f.propID, y.ID)
	_ = e
	ce := code(t, err, "BANQUET_OPTION_NOT_FIRST")
	if ce.Context["holder_event_number"] != x.Number {
		t.Fatalf("the holder is %v, want %s", ce.Context["holder_event_number"], x.Number)
	}
	// X is confirmed: Y loses its room and its salesperson is told, once
	_, err = f.svc.Confirm(f.mgr, f.propID, x.ID)
	must(t, err)
	if n := f.notices(t, f.office, banquet.NoticeLost); n != 1 {
		t.Fatalf("%d OPTION_LOST notices, want 1", n)
	}
	yy, err := f.svc.GetEvent(f.mgr, f.propID, y.ID)
	must(t, err)
	if !yy.Sessions[0].RoomTaken || yy.Status != banquet.StatusTentative {
		t.Fatalf("Y: %+v", yy.Sessions[0])
	}

	// mode REJECT: the same request is refused at creation
	g := setup(t)
	g.definite(t, "X", g.a.ID, "2026-11-07", "12:00", "16:00")
	ev := g.event(t, "Y")
	_, err = g.svc.AddSession(g.office, g.propID, ev.ID, g.session(g.a.ID, "2026-11-07", "15:00", "19:00", false))
	must(t, err)
	_, err = g.svc.MakeTentative(g.office, g.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	e2 := code(t, err, "BANQUET_ROOM_NOT_AVAILABLE")
	if e2.Context["event_number"] == nil {
		t.Fatalf("the refusal does not name the holder: %+v", e2.Context)
	}
}

// When option 1 is lost, option 2 is promoted and its salesperson is told in the same transaction; a rollback leaves no notice.
func TestLosingOptionOnePromotesOptionTwo(t *testing.T) {
	f := setup(t)
	f.mode(t, banquet.ModeQueue)
	x := f.tentative(t, "X", f.a.ID, "2026-11-07", "12:00", "16:00")
	y := f.tentative(t, "Y", f.a.ID, "2026-11-07", "13:00", "17:00")
	w := f.tentative(t, "W", f.a.ID, "2026-11-07", "14:00", "18:00")
	if f.rank(t, y.ID) != 2 || f.rank(t, w.ID) != 3 {
		t.Fatalf("ranks %d %d, want 2 3", f.rank(t, y.ID), f.rank(t, w.ID))
	}
	// a lost event without a reason changes nothing
	_, err := f.svc.MarkLost(f.office, f.propID, x.ID, banquet.CloseInput{})
	_ = code(t, err, "REASON_REQUIRED")
	if f.rank(t, y.ID) != 2 {
		t.Fatal("the failed request changed the queue")
	}
	// a failed transaction leaves no notice: the reason is of the wrong kind, after nothing was written
	_, err = f.svc.MarkLost(f.office, f.propID, x.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	_ = code(t, err, "REASON_NOT_FOUND")
	if n := f.Count(t, `SELECT count(*) FROM banquet_notices WHERE kind = 'OPTION_PROMOTED'`); n != 0 {
		t.Fatalf("%d notices after a failed request", n)
	}

	_, err = f.svc.MarkLost(f.office, f.propID, x.ID, banquet.CloseInput{ReasonID: f.lostReason})
	must(t, err)
	if f.rank(t, y.ID) != 1 || f.rank(t, w.ID) != 2 {
		t.Fatalf("ranks %d %d, want 1 2", f.rank(t, y.ID), f.rank(t, w.ID))
	}
	if n := f.Count(t, `SELECT count(*) FROM banquet_notices WHERE kind = 'OPTION_PROMOTED' AND event_id = $1`, y.ID); n != 1 {
		t.Fatalf("%d promotions for Y, want 1", n)
	}
	if n := f.Count(t, `SELECT count(*) FROM banquet_notices WHERE kind = 'OPTION_PROMOTED' AND event_id = $1`, w.ID); n != 0 {
		t.Fatalf("W (still behind Y) got %d promotions", n)
	}
	// the holds of X are gone, the session is kept for the history, and Y can be confirmed now
	if n := f.Count(t, `SELECT count(*) FROM banquet_holds WHERE event_id = $1`, x.ID); n != 0 {
		t.Fatalf("%d holds left for a lost event", n)
	}
	_, err = f.svc.Confirm(f.mgr, f.propID, y.ID)
	must(t, err)
}

// The setting is audited and changes only the bookings that come after it.
func TestTheConflictModeIsAuditedAndAppliesToNewBookings(t *testing.T) {
	f := setup(t)
	f.mode(t, banquet.ModeQueue)
	x := f.tentative(t, "X", f.a.ID, "2026-11-07", "12:00", "16:00")
	y := f.tentative(t, "Y", f.a.ID, "2026-11-07", "13:00", "17:00")
	f.mode(t, banquet.ModeReject)
	if f.rank(t, x.ID) != 1 || f.rank(t, y.ID) != 2 {
		t.Fatal("the change of the mode touched what existed")
	}
	if n := f.Count(t, `SELECT count(*) FROM audit_logs WHERE property_id = $1 AND action = 'banquet.settings_changed'`, f.propID); n != 2 {
		t.Fatalf("%d audit rows, want 3", n)
	}
	ev := f.event(t, "W")
	_, err := f.svc.AddSession(f.office, f.propID, ev.ID, f.session(f.a.ID, "2026-11-07", "14:00", "18:00", false))
	must(t, err)
	_, err = f.svc.MakeTentative(f.office, f.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
	_ = code(t, err, "BANQUET_ROOM_NOT_AVAILABLE")
	// only banquet.setup changes it
	_, err = f.svc.PutSettings(f.office, f.propID, banquet.SettingsInput{TentativeConflictMode: banquet.ModeQueue})
	_ = code(t, err, "PERMISSION_DENIED")
}

// An option date in the past is flagged, the event is not cancelled, and the day close writes one reminder for the event and the day.
func TestAnOptionDateInThePastIsFlaggedAndRemindedOnce(t *testing.T) {
	f := setup(t)
	ev := f.tentative(t, "Old option", f.a.ID, "2026-11-07", "12:00", "16:00")
	_, err := f.Pool.Exec(context.Background(), `UPDATE banquet_events SET option_until = '2026-09-29' WHERE id = $1`, ev.ID)
	must(t, err)
	got, err := f.svc.GetEvent(f.mgr, f.propID, ev.ID)
	must(t, err)
	if !got.OptionOverdue || got.Status != banquet.StatusTentative {
		t.Fatalf("event: overdue %v, status %s", got.OptionOverdue, got.Status)
	}
	list, err := f.svc.ListEvents(f.mgr, f.propID, banquet.EventFilter{OptionOverdue: true})
	must(t, err)
	if len(list) != 1 {
		t.Fatalf("%d overdue events, want 1", len(list))
	}
	closeDay := func(d string) int {
		var n int
		must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) (err error) {
			n, err = f.svc.WriteDueNotices(ctx, f.tenantID, f.propID, day(d))
			return err
		}))
		return n
	}
	if closeDay("2026-09-30") != 1 || closeDay("2026-09-30") != 0 { // the second run of the same close writes nothing
		t.Fatal("the reminder is not written exactly once for a day")
	}
	if closeDay("2026-10-01") != 1 {
		t.Fatal("the next day's close does not remind again")
	}
	if n := f.notices(t, f.office, banquet.NoticeDue); n != 2 {
		t.Fatalf("%d reminders in the bell, want 2", n)
	}
}

// Moving a tentative session makes new holds: it goes to the back of the new slot, the old slot's options are promoted, the dry run answers both ranks, and REJECT refuses a held slot.
func TestMovingATentativeSessionGoesToTheBackOfTheQueue(t *testing.T) {
	f := setup(t)
	f.mode(t, banquet.ModeQueue)
	x := f.tentative(t, "X", f.a.ID, "2026-11-07", "12:00", "16:00")
	y := f.tentative(t, "Y", f.a.ID, "2026-11-07", "15:00", "19:00") // option 2 behind X
	z := f.tentative(t, "Z", f.b.ID, "2026-11-07", "08:00", "10:00") // option 1 on B

	// Z asks for X's slot in A: the dry run says option 3 there, and that it gives up option 1
	av, err := f.svc.CheckAvailability(f.office, f.propID, z.ID, banquet.AvailabilityInput{SessionID: &z.Sessions[0].ID, RoomID: f.a.ID, SessionDate: day("2026-11-07"),
		StartTime: *tod("15:00"), EndTime: *tod("18:00")})
	must(t, err)
	if av.RankInNewSlot != 3 || av.RankGivenUp == nil || *av.RankGivenUp != 1 || !av.Available || len(av.Conflicts) != 2 {
		t.Fatalf("availability: %+v", av)
	}
	in := f.session(f.a.ID, "2026-11-07", "15:00", "18:00", false)
	in.Name = z.Sessions[0].Name
	_, err = f.svc.UpdateSession(f.office, f.propID, z.ID, z.Sessions[0].ID, in)
	must(t, err)
	if f.rank(t, z.ID) != 3 {
		t.Fatalf("Z is option %d, want 3", f.rank(t, z.ID))
	}

	// X moves away: Y is promoted to option 1 and told; Z moves up to 2
	in = f.session(f.c.ID, "2026-11-07", "12:00", "16:00", false)
	in.Name = x.Sessions[0].Name
	_, err = f.svc.UpdateSession(f.office, f.propID, x.ID, x.Sessions[0].ID, in)
	must(t, err)
	if f.rank(t, y.ID) != 1 || f.rank(t, z.ID) != 2 || f.rank(t, x.ID) != 1 {
		t.Fatalf("ranks Y %d, Z %d, X %d, want 1 2 1", f.rank(t, y.ID), f.rank(t, z.ID), f.rank(t, x.ID))
	}
	if n := f.Count(t, `SELECT count(*) FROM banquet_notices WHERE kind = 'OPTION_PROMOTED' AND event_id = $1`, y.ID); n != 1 {
		t.Fatalf("%d promotions for Y, want 1", n)
	}

	// REJECT: a move onto a held slot is refused and the session stays where it was
	f.mode(t, banquet.ModeReject)
	in = f.session(f.a.ID, "2026-11-07", "16:00", "17:00", false)
	in.Name = x.Sessions[0].Name
	_, err = f.svc.UpdateSession(f.office, f.propID, x.ID, x.Sessions[0].ID, in)
	_ = code(t, err, "BANQUET_ROOM_NOT_AVAILABLE")
	if f.rank(t, x.ID) != 1 {
		t.Fatal("the refused move changed the session")
	}
	got, err := f.svc.GetEvent(f.mgr, f.propID, x.ID)
	must(t, err)
	if *got.Sessions[0].RoomID != f.c.ID {
		t.Fatalf("the session is in room %d after a refused move, want C", *got.Sessions[0].RoomID)
	}
}

// ---------------------------------------------------------------- 3. races

func race(t *testing.T, fns ...func() error) []error {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, len(fns))
	for i, fn := range fns {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}()
	}
	close(start)
	wg.Wait()
	return errs
}

func oneOK(t *testing.T, errs []error, wantCode string) {
	t.Helper()
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
			continue
		}
		_ = code(t, err, wantCode)
	}
	if ok != 1 {
		t.Fatalf("%d of %d succeeded, want exactly one: %v", ok, len(errs), errs)
	}
}

func TestTwoRequestsForTheSameRoomHaveOneWinner(t *testing.T) {
	f := setup(t)
	// one trace per transaction: two transactions in one trace would interleave their locks
	ctxA, traceA := db.WithLockTrace(f.office)
	ctxB, traceB := db.WithLockTrace(f.office)

	// two inquiries become tentative on the same slot (REJECT): one wins
	a, b := f.event(t, "A"), f.event(t, "B")
	for _, ev := range []banquet.Event{a, b} {
		_, err := f.svc.AddSession(f.office, f.propID, ev.ID, f.session(f.a.ID, "2026-11-07", "12:00", "16:00", false))
		must(t, err)
	}
	errs := race(t,
		func() error {
			_, err := f.svc.MakeTentative(ctxA, f.propID, a.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
			return err
		},
		func() error {
			_, err := f.svc.MakeTentative(ctxB, f.propID, b.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
			return err
		})
	oneOK(t, errs, "BANQUET_ROOM_NOT_AVAILABLE")
	assertLockOrder(t, traceA)
	assertLockOrder(t, traceB)

	// two options on a combined room and its part (QUEUE) are confirmed together: the queue lets one through
	f2 := setup(t)
	f2.mode(t, banquet.ModeQueue)
	x := f2.tentative(t, "X", f2.ballroom.ID, "2026-11-07", "12:00", "16:00")
	y := f2.tentative(t, "Y", f2.a.ID, "2026-11-07", "13:00", "15:00")
	ctxX, traceX := db.WithLockTrace(f2.mgr)
	ctxY, traceY := db.WithLockTrace(f2.mgr)
	errs = race(t,
		func() error { _, err := f2.svc.Confirm(ctxX, f2.propID, x.ID); return err },
		func() error { _, err := f2.svc.Confirm(ctxY, f2.propID, y.ID); return err })
	oneOK(t, errs, "BANQUET_OPTION_NOT_FIRST")
	assertLockOrder(t, traceX)
	assertLockOrder(t, traceY)
	if n := f2.Count(t, `SELECT count(*) FROM banquet_holds WHERE hold_kind = 'DEFINITE' AND atom_room_id = $1`, f2.a.ID); n != 1 {
		t.Fatalf("%d definite holds on A, want 1", n)
	}
}

// A confirmation and the creation of an option on the same slot, and a move and a confirmation: the locks are taken in order and the outcome is one of the two valid ones.
func TestAConfirmationAgainstACreationAndAMove(t *testing.T) {
	f := setup(t)
	f.mode(t, banquet.ModeQueue)
	x := f.tentative(t, "X", f.a.ID, "2026-11-07", "12:00", "16:00")
	late := f.event(t, "Late")
	_, err := f.svc.AddSession(f.office, f.propID, late.ID, f.session(f.a.ID, "2026-11-07", "14:00", "18:00", false))
	must(t, err)
	ctxM, traceM := db.WithLockTrace(f.mgr)
	ctxO, traceO := db.WithLockTrace(f.office)
	errs := race(t,
		func() error { _, err := f.svc.Confirm(ctxM, f.propID, x.ID); return err },
		func() error {
			_, err := f.svc.MakeTentative(ctxO, f.propID, late.ID, banquet.TentativeInput{OptionUntil: day("2026-10-20")})
			return err
		})
	for _, err := range errs {
		must(t, err) // both are valid in the mode QUEUE: the late option queues behind the definite event
	}
	assertLockOrder(t, traceM)
	assertLockOrder(t, traceO)
	if f.rank(t, late.ID) != 2 {
		t.Fatalf("the late option is %d, want 2", f.rank(t, late.ID))
	}

	// a move of the event into the room another event is confirming
	g := setup(t)
	g.mode(t, banquet.ModeQueue)
	p := g.tentative(t, "P", g.a.ID, "2026-11-07", "12:00", "16:00")
	q := g.tentative(t, "Q", g.c.ID, "2026-11-07", "12:00", "16:00")
	in := g.session(g.a.ID, "2026-11-07", "12:00", "16:00", false)
	in.Name = q.Sessions[0].Name
	ctxM, traceM = db.WithLockTrace(g.mgr)
	ctxO, traceO = db.WithLockTrace(g.office)
	errs = race(t,
		func() error { _, err := g.svc.Confirm(ctxM, g.propID, p.ID); return err },
		func() error { _, err := g.svc.UpdateSession(ctxO, g.propID, q.ID, q.Sessions[0].ID, in); return err })
	for _, err := range errs {
		must(t, err)
	}
	assertLockOrder(t, traceM)
	assertLockOrder(t, traceO)
	if g.rank(t, q.ID) != 2 {
		t.Fatalf("Q is option %d behind the definite P, want 2", g.rank(t, q.ID))
	}
}

// ---------------------------------------------------------------- 4. masters and rules

func TestTheRulesOfTheMasters(t *testing.T) {
	f := setup(t)
	pid := f.propID
	// a combined room is made of two or more base rooms, none of them combined
	_, err := f.svc.SetRoomParts(f.mgr, pid, f.ballroom.ID, banquet.PartsInput{RoomIDs: []int64{f.a.ID}})
	_ = code(t, err, "ROOM_COMBINATION_INVALID")
	_, err = f.svc.SetRoomParts(f.mgr, pid, f.ballroom.ID, banquet.PartsInput{RoomIDs: []int64{f.a.ID, f.garden.ID}})
	_ = code(t, err, "ROOM_COMBINATION_INVALID") // one level only: the garden is combined itself
	_, err = f.svc.SetRoomParts(f.mgr, pid, f.a.ID, banquet.PartsInput{RoomIDs: []int64{f.b.ID, f.c.ID}})
	_ = code(t, err, "ROOM_COMBINATION_INVALID") // A is a base room
	_, err = f.svc.SetRoomParts(f.mgr, pid, f.ballroom.ID, banquet.PartsInput{RoomIDs: []int64{f.ballroom.ID, f.a.ID}})
	_ = code(t, err, "ROOM_COMBINATION_INVALID")
	// a room that is a part cannot become combined, a combination cannot become a base room
	a, err := f.svc.GetRoom(f.mgr, pid, f.a.ID)
	must(t, err)
	_, err = f.svc.UpdateRoom(f.mgr, pid, a.ID, banquet.RoomInput{Name: a.Name, IsCombo: true, SetupMinutes: 60, TeardownMinutes: 30, Version: a.Version})
	_ = code(t, err, "ROOM_COMBINATION_INVALID")
	// the buffers, the code, the capacities
	_, err = f.svc.CreateRoom(f.mgr, pid, banquet.RoomInput{Code: "X", Name: "X", SetupMinutes: -1})
	_ = code(t, err, "VALIDATION_FAILED")
	_, err = f.svc.CreateRoom(f.mgr, pid, banquet.RoomInput{Code: "A", Name: "Again"})
	_ = code(t, err, "CODE_TAKEN")
	_, err = f.svc.SetRoomSetups(f.mgr, pid, f.a.ID, banquet.SetupsInput{Setups: []banquet.RoomSetup{{SetupStyle: "THEATER", Capacity: 0}}})
	_ = code(t, err, "VALIDATION_FAILED")
	// a stale version is a conflict
	_, err = f.svc.UpdateRoom(f.mgr, pid, a.ID, banquet.RoomInput{Name: "A2", SetupMinutes: 60, TeardownMinutes: 30, Version: a.Version - 1})
	_ = code(t, err, "VERSION_CONFLICT")

	// the set-up must be one the room offers; the guests above the capacity need a confirmation
	ev := f.event(t, "Wedding")
	in := f.session(f.a.ID, "2026-11-07", "12:00", "16:00", false)
	in.SetupStyle = ptr("U_SHAPE")
	_, err = f.svc.AddSession(f.office, pid, ev.ID, in)
	_ = code(t, err, "SETUP_NOT_OFFERED")
	in.SetupStyle, in.PaxEstimated = ptr("ROUND_TABLE"), 300
	_, err = f.svc.AddSession(f.office, pid, ev.ID, in)
	ce := code(t, err, "CAPACITY_EXCEEDED")
	if ce.Context["capacity"] == nil {
		t.Fatal("the warning does not name the capacity")
	}
	in.AcceptCapacity = true
	_, err = f.svc.AddSession(f.office, pid, ev.ID, in)
	must(t, err)
	// a session ends after it starts, or ends the next day
	bad := f.session(f.a.ID, "2026-11-08", "16:00", "12:00", false)
	_, err = f.svc.AddSession(f.office, pid, ev.ID, bad)
	_ = code(t, err, "VALIDATION_FAILED")
	// a tentative event needs the reason and the date; lost and cancelled need a reason of their kind
	_, err = f.svc.MakeTentative(f.office, pid, ev.ID, banquet.TentativeInput{})
	_ = code(t, err, "OPTION_DATE_REQUIRED")
	_, err = f.svc.MarkLost(f.office, pid, ev.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	_ = code(t, err, "REASON_NOT_FOUND")
	// the booker is a guest or a company
	_, err = f.svc.CreateEvent(f.office, pid, banquet.EventInput{Name: "No booker", SalesUserID: &f.salesID})
	_ = code(t, err, "VALIDATION_FAILED")
	// a definite event can be cancelled only with banquet.confirm
	d := f.definite(t, "Gala", f.c.ID, "2026-11-07", "12:00", "16:00")
	_, err = f.svc.CancelEvent(f.office, pid, d.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.CancelEvent(f.mgr, pid, d.ID, banquet.CloseInput{ReasonID: f.cancelReason})
	must(t, err)
	if n := f.Count(t, `SELECT count(*) FROM banquet_holds WHERE event_id = $1`, d.ID); n != 0 {
		t.Fatalf("%d holds left for a cancelled event", n)
	}
	// and a closed event does not change
	_, err = f.svc.AddSession(f.office, pid, d.ID, f.session(f.c.ID, "2026-11-09", "12:00", "16:00", false))
	_ = code(t, err, "EVENT_STATUS_INVALID")
}

func TestItemsAndPackages(t *testing.T) {
	f := setup(t)
	var chargeCode int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM charge_codes WHERE property_id = $1 AND is_active ORDER BY id LIMIT 1`, f.propID).Scan(&chargeCode))
	it, err := f.svc.CreateItem(f.mgr, f.propID, banquet.ItemInput{Code: "LUNCH", Name: "Set lunch", Kind: "MENU", UnitBasis: "PER_PAX", ChargeCodeID: chargeCode, ListPrice: decimal.NewFromInt(150000), FbClass: ptr("FOOD")})
	must(t, err)
	_, err = f.svc.CreateItem(f.mgr, f.propID, banquet.ItemInput{Code: "BAD", Name: "Bad", Kind: "MENU", UnitBasis: "PER_PAX", ChargeCodeID: chargeCode, ListPrice: decimal.RequireFromString("1.5")})
	_ = code(t, err, "VALIDATION_FAILED") // more decimals than IDR has
	_, err = f.svc.CreateItem(f.mgr, f.propID, banquet.ItemInput{Code: "BAD", Name: "Bad", Kind: "MENU", UnitBasis: "PER_PAX", ChargeCodeID: 99999999})
	_ = code(t, err, "VALIDATION_FAILED")
	pkg, err := f.svc.CreatePackage(f.mgr, f.propID, banquet.PackageInput{Code: "FULLDAY", Name: "Fullday meeting", PricePerPax: decimal.NewFromInt(350000),
		Components: []banquet.ComponentInput{{ItemID: it.ID, QtyPerPax: decimal.NewFromInt(1)}}})
	must(t, err)
	if len(pkg.Components) != 1 || pkg.Components[0].ItemCode != "LUNCH" {
		t.Fatalf("package: %+v", pkg)
	}
	_, err = f.svc.UpdatePackage(f.mgr, f.propID, pkg.ID, banquet.PackageInput{Name: "Fullday", PricePerPax: decimal.NewFromInt(350000),
		Components: []banquet.ComponentInput{{ItemID: it.ID, QtyPerPax: decimal.NewFromInt(1)}, {ItemID: it.ID, QtyPerPax: decimal.NewFromInt(2)}}})
	_ = code(t, err, "VALIDATION_FAILED") // an item is listed once
}

// Reading follows document 33; changing needs the permission that manages it; another tenant's property and another property's room are not found.
func TestReadsByPermissionAndIsolation(t *testing.T) {
	f := setup(t)
	pid := f.propID
	for name, ctx := range map[string]context.Context{"reader": f.reader, "office": f.office, "manager": f.mgr} {
		list, err := f.svc.ListRooms(ctx, pid, nil)
		if err != nil || len(list) != 5 {
			t.Fatalf("%s lists %d rooms: %v", name, len(list), err)
		}
	}
	_, err := f.svc.ListRooms(f.nobody, pid, nil)
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.CreateRoom(f.reader, pid, banquet.RoomInput{Code: "Z", Name: "Z"})
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.CreateRoom(f.office, pid, banquet.RoomInput{Code: "Z", Name: "Z"}) // manage is not setup
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.CreateEvent(f.reader, pid, banquet.EventInput{Name: "E", BookerGuestID: &f.guestID})
	_ = code(t, err, "PERMISSION_DENIED")
	_, err = f.svc.Calendar(f.nobody, pid, nil, nil)
	_ = code(t, err, "PERMISSION_DENIED")

	// another tenant
	tn2 := f.Tenant(t, "XYZ")
	other := roomstest.Admin(tn2.ID)
	_, err = f.svc.ListRooms(other, pid, nil)
	_ = code(t, err, "PROPERTY_NOT_FOUND")
	// another property of the same tenant: the room is not found there
	p2 := f.Property(t, f.tenantID, "UBUD")
	_, err = f.svc.GetRoom(f.admin, p2.ID, f.a.ID)
	_ = code(t, err, "BANQUET_ROOM_NOT_FOUND")
	_, err = f.svc.GetEvent(f.admin, p2.ID, f.event(t, "E").ID)
	_ = code(t, err, "EVENT_NOT_FOUND")
	list, err := f.svc.ListRooms(f.admin, p2.ID, nil)
	if err != nil || len(list) != 0 {
		t.Fatalf("the other property lists %d rooms: %v", len(list), err)
	}
}

// The room masters lock the rooms they change, in ascending order, at the level of the events (36 5.1).
func TestEditingRoomsLocksThemInOrder(t *testing.T) {
	f := setup(t)
	pid := f.propID
	ctx, trace := db.WithLockTrace(f.mgr)
	a, err := f.svc.GetRoom(ctx, pid, f.a.ID)
	must(t, err)
	_, err = f.svc.UpdateRoom(ctx, pid, a.ID, banquet.RoomInput{Name: a.Name, SetupMinutes: 45, TeardownMinutes: 30, Version: a.Version})
	must(t, err)
	_, err = f.svc.SetRoomSetups(ctx, pid, f.a.ID, banquet.SetupsInput{Setups: []banquet.RoomSetup{{SetupStyle: "THEATER", Capacity: 90}}})
	must(t, err)
	_, err = f.svc.SetRoomParts(ctx, pid, f.ballroom.ID, banquet.PartsInput{RoomIDs: []int64{f.b.ID, f.a.ID}})
	must(t, err)
	assertLockOrder(t, trace)
	rooms := 0
	for _, ev := range trace.Events() {
		if !ev.Begin && ev.Table == "banquet_rooms" {
			rooms++
		}
	}
	if rooms != 3 {
		t.Fatalf("the three room changes locked the rooms %d times", rooms)
	}
}

// A use case of the banquet never reaches both the stock and the events: the two share a lock level and have no order between them (36 5.1).
func TestNoUseCaseTouchesStockAndEventLocks(t *testing.T) {
	files, err := os.ReadDir(".")
	must(t, err)
	for _, e := range files {
		if e.IsDir() || filepath.Ext(e.Name()) != ".go" || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, err := os.ReadFile(e.Name())
		must(t, err)
		if regexp.MustCompile(`db\.(Stock[A-Z]|Warehouses|StockBalances)`).Match(src) {
			t.Fatalf("%s locks stock tables: a banquet use case must not", e.Name())
		}
	}
}
