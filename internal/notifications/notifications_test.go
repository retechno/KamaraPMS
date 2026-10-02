package notifications_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/notifications"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations"
	"kamarapms/internal/rooms"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var (
	code     = roomstest.Code
	wantCode = roomstest.Want
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// fakeSender records what would be sent and fails on demand.
type fakeSender struct {
	mu   sync.Mutex
	sent []notifications.Message
	err  error
	slow time.Duration
}

func (f *fakeSender) Send(_ context.Context, m notifications.Message) error {
	if f.slow > 0 {
		time.Sleep(f.slow)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	svc              *notifications.Service
	sender           *fakeSender
	dlx              rooms.RoomType
	plan, guest      int64
}

// setup: a property, a guest with an e-mail address, a rate grid, and the e-mail service wired as the confirmation hook.
func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, _ := e.AdminAccount(t, tn.ID)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, sender: &fakeSender{}}
	f.dlx = e.RoomType(t, admin, p.ID, "DLX")
	for _, n := range []string{"101", "102", "103", "104", "105", "106"} {
		e.Room(t, admin, p.ID, f.dlx.ID, n, housekeeping.Clean)
	}
	var chargeID int64
	ctx := context.Background()
	must(t, e.Pool.QueryRow(ctx, `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, p.ID).Scan(&chargeID))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`, tn.ID, p.ID, chargeID).Scan(&f.plan))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO guests (tenant_id, code, first_name, last_name, email, origin_property_id) VALUES ($1, 'G1', 'Siti', 'Nurhaliza', 'siti@example.test', $2) RETURNING id`, tn.ID, p.ID).Scan(&f.guest))
	_, err := e.Rates.FillRates(admin, p.ID, rates.FillInput{RatePlanID: f.plan, RoomTypeIDs: []int64{f.dlx.ID}, From: d("2026-09-30"), To: d("2026-10-21"), Amount: "1000000"})
	must(t, err)
	f.svc = f.build(f.sender)
	e.Res.SetConfirmedHook(f.svc)
	return f
}

func (f *fx) build(s notifications.Sender) *notifications.Service {
	return notifications.NewService(f.TxM, f.Clock, audit.NewWriter(f.Clock), iam.NewAuthorizer(f.TxM), f.Tenancy, f.Docs, f.Res, s)
}

func (f *fx) book(t *testing.T, confirm bool, guest *int64, key string) reservations.Reservation {
	t.Helper()
	res, err := f.Res.Create(f.admin, f.propID, key, reservations.CreateInput{GuestID: guest, Source: "PHONE", Confirm: confirm, SpecialRequest: "Quiet room", Rooms: []reservations.LineInput{
		{RoomTypeID: f.dlx.ID, RatePlanID: f.plan, Arrival: d("2026-10-05"), Departure: d("2026-10-07"), Adults: 2}}})
	must(t, err)
	return res
}

func (f *fx) rows(t *testing.T) int { return f.Count(t, `SELECT count(*) FROM email_outbox`) }

func TestConfirmingQueuesAndTheWorkerSendsTheConfirmation(t *testing.T) {
	f := setup(t)
	res := f.book(t, true, &f.guest, "k1")
	if f.rows(t) != 1 || f.Count(t, `SELECT count(*) FROM email_outbox WHERE status = 'QUEUED' AND to_address = 'siti@example.test'`) != 1 {
		t.Fatal("the confirmation is queued in the booking's transaction")
	}
	if f.sender.count() != 0 {
		t.Fatal("nothing is sent inside the booking")
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'email.queued'`) != 1 {
		t.Fatal("queueing is audited")
	}
	sent, failed, err := f.svc.ProcessDue(f.admin)
	must(t, err)
	if sent != 1 || failed != 0 || f.sender.count() != 1 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	m := f.sender.sent[0]
	if m.To != "siti@example.test" || !strings.Contains(m.Subject, res.ConfirmationNumber) || !strings.Contains(m.Subject, "Hotel") && !strings.Contains(m.Subject, "BALI") && !strings.Contains(m.Subject, "Bali") {
		t.Fatalf("subject: %q", m.Subject)
	}
	for _, want := range []string{"Dear Siti Nurhaliza", res.ConfirmationNumber, "DLX", "2026-10-05", "Quiet room", "Check-in is from 14:00"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("text lacks %q", want)
		}
	}
	if !strings.Contains(m.HTML, res.ConfirmationNumber) || len(m.Attachments) != 1 || m.Attachments[0].ContentType != "application/pdf" ||
		!strings.HasPrefix(string(m.Attachments[0].Data), "%PDF-") || m.Attachments[0].Filename != "confirmation-"+res.ConfirmationNumber+".pdf" {
		t.Fatalf("html / attachment: %+v", m.Attachments)
	}
	if f.Count(t, `SELECT count(*) FROM email_outbox WHERE status = 'SENT' AND sent_at IS NOT NULL AND attempts = 1 AND last_error IS NULL`) != 1 {
		t.Fatal("recorded as sent")
	}
	if sent, _, _ := f.svc.ProcessDue(f.admin); sent != 0 || f.sender.count() != 1 {
		t.Fatal("a sent message is never sent again")
	}
}

func TestOnlyConfirmedBookingsWithAnAddressAreQueued(t *testing.T) {
	f := setup(t)
	f.book(t, false, &f.guest, "draft") // a draft is not a confirmation
	if f.rows(t) != 0 {
		t.Fatal("a draft sends nothing")
	}
	var noMail int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G2', 'Nomail', $2) RETURNING id`, f.tenantID, f.propID).Scan(&noMail))
	f.book(t, true, &noMail, "nomail")
	if f.rows(t) != 0 {
		t.Fatal("no address, no e-mail, and the booking still succeeds")
	}
	must(t, f.Exec(t, `UPDATE guests SET email = 'not an address' WHERE id = $1`, noMail))
	f.book(t, true, &noMail, "oddmail")
	if f.rows(t) != 0 {
		t.Fatal("an invalid address is ignored")
	}
	// confirming the draft later queues it
	draft := f.book(t, false, &f.guest, "d2")
	_, err := f.Res.Confirm(f.admin, f.propID, draft.ID, draft.Version)
	must(t, err)
	if f.rows(t) != 1 {
		t.Fatal("Confirm queues the confirmation")
	}
	// a replayed booking request (same Idempotency-Key) does not queue a second message
	f.book(t, true, &f.guest, "same")
	n := f.rows(t)
	f.book(t, true, &f.guest, "same")
	if f.rows(t) != n {
		t.Fatalf("a replay queued another e-mail: %d -> %d", n, f.rows(t))
	}
}

func TestEmailOffQueuesNothing(t *testing.T) {
	f := setup(t)
	off := f.build(nil)
	f.Res.SetConfirmedHook(off)
	f.book(t, true, &f.guest, "k1")
	if f.rows(t) != 0 || off.Enabled() {
		t.Fatal("without a mail server nothing is queued")
	}
	_, err := off.Resend(f.admin, f.propID, 1)
	wantCode(t, err, "EMAIL_NOT_CONFIGURED")
	if sent, failed, err := off.ProcessDue(f.admin); err != nil || sent+failed != 0 {
		t.Fatal(err)
	}
	st, err := off.List(f.admin, f.propID, 1)
	must(t, err)
	if st.Enabled {
		t.Fatal("the screen is told e-mail is off")
	}
}

func TestFailuresAreRetriedWithBackoffAndGiveUp(t *testing.T) {
	f := setup(t)
	f.book(t, true, &f.guest, "k1")
	f.sender.err = errors.New("dial tcp: connection refused")
	for attempt, wait := range []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour} {
		if _, failed, err := f.svc.ProcessDue(f.admin); err != nil || failed != 1 {
			t.Fatalf("attempt %d: %v failed=%d", attempt+1, err, failed)
		}
		var status string
		var attempts int
		var next time.Time
		var last *string
		must(t, f.Pool.QueryRow(context.Background(), `SELECT status, attempts, next_attempt_at, last_error FROM email_outbox`).Scan(&status, &attempts, &next, &last))
		if status != "QUEUED" || attempts != attempt+1 || !next.Equal(f.Clock.Now().Add(wait)) || last == nil || !strings.Contains(*last, "connection refused") {
			t.Fatalf("after attempt %d: %s %d %v %v", attempt+1, status, attempts, next, last)
		}
		if _, failed, _ := f.svc.ProcessDue(f.admin); failed != 0 {
			t.Fatal("not retried before its time")
		}
		f.Clock.Set(next)
	}
	_, failed, err := f.svc.ProcessDue(f.admin)
	must(t, err)
	if failed != 1 || f.Count(t, `SELECT count(*) FROM email_outbox WHERE status = 'FAILED' AND attempts = 5`) != 1 {
		t.Fatal("given up after five attempts")
	}
	f.Clock.Set(f.Clock.Now().Add(48 * time.Hour))
	if _, failed, _ := f.svc.ProcessDue(f.admin); failed != 0 {
		t.Fatal("a failed message is not retried again")
	}
}

func TestPermanentRefusalFailsAtOnceAndCancelledBookingsAreSkipped(t *testing.T) {
	f := setup(t)
	f.book(t, true, &f.guest, "k1")
	f.sender.err = notifications.ErrPermanent
	f.svc.ProcessDue(f.admin) //nolint:errcheck // the outcome is read from the table
	if f.Count(t, `SELECT count(*) FROM email_outbox WHERE status = 'FAILED' AND attempts = 1`) != 1 {
		t.Fatal("a refusal that retrying cannot fix fails at once")
	}
	f.sender.err = nil
	res := f.book(t, true, &f.guest, "k2")
	_, err := f.Res.Cancel(f.admin, f.propID, res.ID, res.Version, "changed plans")
	must(t, err)
	sent, _, err := f.svc.ProcessDue(f.admin)
	must(t, err)
	if sent != 0 || f.sender.count() != 0 || f.Count(t, `SELECT count(*) FROM email_outbox WHERE status = 'SKIPPED' AND last_error LIKE '%no longer confirmed%'`) != 1 {
		t.Fatalf("a confirmation for a cancelled booking is dropped, not sent (sent %d)", sent)
	}
}

func TestTwoWorkersNeverSendTheSameMessageTwice(t *testing.T) {
	f := setup(t)
	f.sender.slow = 150 * time.Millisecond
	f.book(t, true, &f.guest, "k1")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = f.svc.ProcessDue(f.admin)
		}()
	}
	wg.Wait()
	if f.sender.count() != 1 {
		t.Fatalf("sent %d times", f.sender.count())
	}
}

func TestResend(t *testing.T) {
	f := setup(t)
	res := f.book(t, true, &f.guest, "k1")
	_, err := f.svc.Resend(f.admin, f.propID, res.ID)
	wantCode(t, err, "EMAIL_ALREADY_QUEUED") // the first one has not gone yet
	f.svc.ProcessDue(f.admin)                //nolint:errcheck // the outcome is read below
	e, err := f.svc.Resend(f.admin, f.propID, res.ID)
	must(t, err)
	if e.Status != "QUEUED" || e.To != "siti@example.test" || f.rows(t) != 2 {
		t.Fatalf("resend: %+v", e)
	}
	st, err := f.svc.List(f.admin, f.propID, res.ID)
	must(t, err)
	if !st.Enabled || len(st.Data) != 2 || st.Data[0].ID != e.ID || st.Data[1].Status != "SENT" {
		t.Fatalf("list: %+v", st)
	}
	draft := f.book(t, false, &f.guest, "d")
	_, err = f.svc.Resend(f.admin, f.propID, draft.ID)
	wantCode(t, err, "RESERVATION_NOT_CONFIRMED")
	var noMail int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO guests (tenant_id, code, last_name, origin_property_id) VALUES ($1, 'G2', 'Nomail', $2) RETURNING id`, f.tenantID, f.propID).Scan(&noMail))
	nm := f.book(t, true, &noMail, "nm")
	_, err = f.svc.Resend(f.admin, f.propID, nm.ID)
	wantCode(t, err, "GUEST_HAS_NO_EMAIL")
	_, err = f.svc.Resend(f.admin, f.propID, 999999)
	wantCode(t, err, "RESERVATION_NOT_FOUND")
	// permissions
	reader := f.User(t, f.tenantID, f.propID, auth.PermReservationRead)
	if _, err := f.svc.List(reader, f.propID, res.ID); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Resend(reader, f.propID, res.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	none := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err = f.svc.List(none, f.propID, res.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	other := f.Tenant(t, "XYZ")
	_, err = f.svc.List(roomstest.Admin(other.ID), f.propID, res.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	var _ = code
}
