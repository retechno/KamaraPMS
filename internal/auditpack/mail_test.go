package auditpack_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack"
	"kamarapms/internal/documents"
	"kamarapms/internal/iam"
	"kamarapms/internal/notifications"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// The e-mail of the pack (docs/architecture/38-reports-night-audit-pack.md 6 and 9.3): the recipients, the messages the run queues, what the worker sends, the split by size and the resend.

type fakeSender struct {
	mu    sync.Mutex
	sent  []notifications.Message
	err   error
	block bool
}

func (s *fakeSender) Send(ctx context.Context, m notifications.Message) error {
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	for i := range m.Attachments {
		m.Attachments[i].Data = append([]byte(nil), m.Attachments[i].Data...)
	}
	s.sent = append(s.sent, m)
	return nil
}

func (s *fakeSender) messages() []notifications.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]notifications.Message(nil), s.sent...)
}

// mailer wires the e-mail of the pack: the worker with a fake sender, the PDF of the pack, and the recipients' view of the e-mail being on.
func (f *fx) mailer(t *testing.T, sender notifications.Sender) *notifications.Service {
	t.Helper()
	svc := notifications.NewService(f.TxM, f.Clock, audit.NewWriter(f.Clock), iam.NewAuthorizer(f.TxM), f.Tenancy, f.Docs, f.Res, sender)
	svc.SetPacks(f.pack)
	f.Docs.SetPacks(f.pack)
	f.pack.SetPDF(f.Docs)
	f.pack.SetMailer(svc)
	return svc
}

func (f *fx) recipient(t *testing.T, email string, sections ...string) auditpack.Recipient {
	t.Helper()
	r, err := f.pack.CreateRecipient(f.admin, f.propID, auditpack.RecipientInput{Email: email, Name: "GM", Sections: sections})
	must(t, err)
	return r
}

type outboxRow struct {
	To       string
	Status   string
	Part     int
	Parts    int
	Sections []string
	Error    string
	SHA      string
	Renderer string
}

func (f *fx) outbox(t *testing.T) []outboxRow {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT to_address, status, nap_part, nap_parts, nap_sections, COALESCE(last_error, ''), COALESCE(nap_pdf_sha256, ''), COALESCE(nap_renderer, '')
		FROM email_outbox WHERE kind = 'NIGHT_AUDIT_PACK' AND property_id = $1 ORDER BY id`, f.propID)
	must(t, err)
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		must(t, rows.Scan(&r.To, &r.Status, &r.Part, &r.Parts, &r.Sections, &r.Error, &r.SHA, &r.Renderer))
		out = append(out, r)
	}
	must(t, rows.Err())
	return out
}

func appErrCode(err error) string {
	if e, ok := apperr.As(err); ok {
		return e.Code
	}
	return ""
}

func TestTheRunQueuesOneMessageForEachActiveRecipient(t *testing.T) {
	f := setup(t)
	f.mailer(t, &fakeSender{})
	f.recipient(t, "gm@example.test")
	off := f.recipient(t, "off@example.test")
	_, err := f.pack.UpdateRecipient(f.admin, f.propID, off.ID, auditpack.RecipientPatch{IsActive: ptr(false)})
	must(t, err)
	f.recipient(t, "fo@example.test", auditpack.SecGuestLedger, auditpack.SecZReports) // Z_REPORTS is OFF in this property: only the ledger is sent
	f.stay(t, f.r101, "2026-10-03")
	f.closeDay(t)

	got := f.outbox(t)
	if len(got) != 2 {
		t.Fatalf("outbox: %+v", got)
	}
	if got[0].To != "gm@example.test" || strings.Join(got[0].Sections, ",") != "SUMMARY,FLASH" || got[0].Status != "QUEUED" || got[0].Part != 1 || got[0].Parts != 1 {
		t.Fatalf("first: %+v", got[0])
	}
	if got[1].To != "fo@example.test" || strings.Join(got[1].Sections, ",") != "GUEST_LEDGER" {
		t.Fatalf("second: %+v", got[1])
	}
	if f.Count(t, `SELECT count(*) FROM email_outbox WHERE kind = 'NIGHT_AUDIT_PACK' AND created_by IS NULL`) != 0 {
		t.Fatal("a message knows who closed the day")
	}
}

func TestWithoutAMailServerNothingIsQueued(t *testing.T) {
	f := setup(t)
	f.mailer(t, nil)
	f.recipient(t, "gm@example.test")
	f.closeDay(t)
	if got := f.outbox(t); len(got) != 0 {
		t.Fatalf("outbox: %+v", got)
	}
	_, err := f.pack.Resend(f.admin, f.propID, d("2026-09-30"), auditpack.ResendInput{})
	if appErrCode(err) != "EMAIL_OFF" {
		t.Fatalf("resend with e-mail off: %v", err)
	}
	list, err := f.pack.ListRecipients(f.admin, f.propID)
	must(t, err)
	if list.EmailEnabled || len(list.Items) != 1 {
		t.Fatalf("recipients: %+v", list)
	}
}

func TestTheWorkerSendsThePDFWithItsHashAndRenderer(t *testing.T) {
	f := setup(t)
	sender := &fakeSender{}
	svc := f.mailer(t, sender)
	f.recipient(t, "gm@example.test")
	f.recipient(t, "fo@example.test", auditpack.SecGuestLedger)
	f.stay(t, f.r101, "2026-10-03")
	bd := f.closeDay(t)

	sent, failed, err := svc.ProcessDue(context.Background())
	must(t, err)
	if sent != 2 || failed != 0 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	msgs := sender.messages()
	byTo := map[string]notifications.Message{}
	for _, m := range msgs {
		byTo[m.To] = m
	}
	gm, fo := byTo["gm@example.test"], byTo["fo@example.test"]
	if !strings.Contains(gm.Subject, "Night audit pack "+bd.String()) || len(gm.Attachments) != 1 || !strings.HasPrefix(string(gm.Attachments[0].Data), "%PDF-") {
		t.Fatalf("message: %q %d", gm.Subject, len(gm.Attachments))
	}
	if !strings.Contains(gm.Text, "Rooms sold") || !strings.Contains(gm.HTML, "Rooms sold") {
		t.Fatalf("the summary recipient reads the figures: %s", gm.Text)
	}
	if strings.Contains(fo.Text, "Rooms sold") || strings.Contains(fo.HTML, "Rooms sold") {
		t.Fatalf("a recipient of the ledger only gets no figures: %s", fo.Text)
	}
	for _, m := range msgs {
		if strings.Contains(m.Text, "http") || strings.Contains(m.HTML, "http") || strings.Contains(m.HTML, "href") {
			t.Fatalf("a message carries no link: %s", m.Text)
		}
	}
	for _, r := range f.outbox(t) {
		sum := sha256.Sum256(byTo[r.To].Attachments[0].Data)
		if r.Status != "SENT" || r.SHA != hex.EncodeToString(sum[:]) || r.Renderer != strconv.Itoa(documents.PackRendererVersion) {
			t.Fatalf("history of %s: %+v", r.To, r)
		}
	}
	// the same pack is the same bytes: the hash of a second sending is the hash of the first
	_, err = f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{})
	must(t, err)
	sent, _, err = svc.ProcessDue(context.Background())
	must(t, err)
	if sent != 2 {
		t.Fatalf("resent %d", sent)
	}
	hashes := map[string]map[string]bool{}
	for _, r := range f.outbox(t) {
		if hashes[r.To] == nil {
			hashes[r.To] = map[string]bool{}
		}
		hashes[r.To][r.SHA] = true
	}
	for to, h := range hashes {
		if len(h) != 1 {
			t.Fatalf("%s: the PDF of one pack differs between two sendings: %v", to, h)
		}
	}
}

func TestARecipientDeactivatedBeforeTheSendingGetsNothing(t *testing.T) {
	f := setup(t)
	sender := &fakeSender{}
	svc := f.mailer(t, sender)
	r := f.recipient(t, "gm@example.test")
	f.closeDay(t)
	_, err := f.pack.UpdateRecipient(f.admin, f.propID, r.ID, auditpack.RecipientPatch{IsActive: ptr(false)})
	must(t, err)
	sent, _, err := svc.ProcessDue(context.Background())
	must(t, err)
	if sent != 0 || len(sender.messages()) != 0 {
		t.Fatal("a deactivated recipient was sent a message")
	}
	if got := f.outbox(t); len(got) != 1 || got[0].Status != "SKIPPED" {
		t.Fatalf("outbox: %+v", got)
	}
}

func TestAPackLargerThanAMessageGoesInSeveral(t *testing.T) {
	f := setup(t)
	sender := &fakeSender{}
	svc := f.mailer(t, sender)
	all := []string{auditpack.SecSummary, auditpack.SecFlash, auditpack.SecRevenue, auditpack.SecTax, auditpack.SecGuestLedger, auditpack.SecInHouse}
	f.recipient(t, "gm@example.test", all...)
	f.stay(t, f.r101, "2026-10-03")
	bd := f.closeDay(t)

	// a limit that holds any section alone and not all of them together
	var biggest, total int
	for _, c := range all {
		_, pdf, err := f.pack.PDF(f.admin, f.propID, bd, []string{c})
		must(t, err)
		biggest = max(biggest, len(pdf))
	}
	_, whole, err := f.pack.PDF(f.admin, f.propID, bd, all)
	must(t, err)
	total = len(whole)
	if total <= biggest {
		t.Fatalf("the sections together (%d) must be larger than the largest alone (%d)", total, biggest)
	}
	svc.SetMaxBytes((biggest+2)/3*4 + biggest/57*2 + 20_000 + 1)

	sent, failed, err := svc.ProcessDue(context.Background())
	must(t, err)
	if failed != 0 || sent < 1 {
		t.Fatalf("first pass: sent %d failed %d", sent, failed)
	}
	for range 10 { // the other parts are queued by the first one
		if n, _, err := svc.ProcessDue(context.Background()); err != nil || n == 0 {
			must(t, err)
			break
		}
	}
	rows := f.outbox(t)
	if len(rows) < 2 {
		t.Fatalf("split into %d: %+v", len(rows), rows)
	}
	seen := map[string]bool{}
	for i, r := range rows {
		if r.Status != "SENT" || r.Parts != len(rows) || r.SHA == "" {
			t.Fatalf("part %d: %+v", i+1, r)
		}
		for _, c := range r.Sections {
			if seen[c] {
				t.Fatalf("section %s in two parts", c)
			}
			seen[c] = true
		}
	}
	for _, c := range all {
		if !seen[c] {
			t.Fatalf("section %s in no part: %+v", c, rows)
		}
	}
	msgs := sender.messages()
	if len(msgs) != len(rows) || !strings.Contains(msgs[0].Subject, fmt.Sprintf("part 1 of %d", len(rows))) {
		t.Fatalf("messages: %d, first %q", len(msgs), msgs[0].Subject)
	}
}

func TestASectionLargerThanAMessageFailsTheMessage(t *testing.T) {
	f := setup(t)
	sender := &fakeSender{}
	svc := f.mailer(t, sender)
	f.recipient(t, "gm@example.test")
	f.closeDay(t)
	svc.SetMaxBytes(20_500) // only the headroom of a message: no PDF fits
	_, failed, err := svc.ProcessDue(context.Background())
	must(t, err)
	got := f.outbox(t)
	if failed != 1 || len(got) != 1 || got[0].Status != "FAILED" || !strings.HasPrefix(got[0].Error, "PACK_TOO_LARGE") || len(sender.messages()) != 0 {
		t.Fatalf("failed %d: %+v", failed, got)
	}
}

func TestTwoResendsToTheSameAddressQueueOnce(t *testing.T) {
	f := setup(t)
	svc := f.mailer(t, &fakeSender{})
	f.recipient(t, "gm@example.test")
	bd := f.closeDay(t)
	if _, _, err := svc.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{})
		}()
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case appErrCode(err) == "PACK_ALREADY_QUEUED":
			refused++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("ok %d refused %d: %v", ok, refused, errs)
	}
	if f.Count(t, `SELECT count(*) FROM email_outbox WHERE kind = 'NIGHT_AUDIT_PACK' AND status = 'QUEUED'`) != 1 {
		t.Fatal("one message waits")
	}
	if _, err := f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{}); appErrCode(err) != "PACK_ALREADY_QUEUED" {
		t.Fatalf("a third one: %v", err)
	}
	if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'nap_pack.resent'`) != 1 {
		t.Fatal("the audit entry of the resend")
	}
}

func TestAResendChoosesItsRecipients(t *testing.T) {
	f := setup(t)
	svc := f.mailer(t, &fakeSender{})
	a := f.recipient(t, "a@example.test")
	b := f.recipient(t, "b@example.test")
	off := f.recipient(t, "off@example.test")
	_, err := f.pack.UpdateRecipient(f.admin, f.propID, off.ID, auditpack.RecipientPatch{IsActive: ptr(false)})
	must(t, err)
	bd := f.closeDay(t)
	if _, _, err := svc.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{RecipientIDs: []int64{off.ID}}); appErrCode(err) != "PACK_RECIPIENT_NOT_FOUND" {
		t.Fatalf("an inactive recipient: %v", err)
	}
	if _, err := f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{RecipientIDs: []int64{999999}}); appErrCode(err) != "PACK_RECIPIENT_NOT_FOUND" {
		t.Fatalf("an id that is none: %v", err)
	}
	res, err := f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{RecipientIDs: []int64{b.ID}})
	must(t, err)
	if len(res.Queued) != 1 || res.Queued[0].Email != "b@example.test" {
		t.Fatalf("queued: %+v", res)
	}
	if _, err := f.pack.Resend(f.admin, f.propID, d("2026-01-01"), auditpack.ResendInput{RecipientIDs: []int64{a.ID}}); appErrCode(err) != "PACK_NOT_FOUND" {
		t.Fatalf("a day with no pack: %v", err)
	}
	all, err := f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{})
	if appErrCode(err) != "PACK_ALREADY_QUEUED" {
		t.Fatalf("everyone while b waits: %v %+v", err, all)
	}
	if f.Count(t, `SELECT count(*) FROM email_outbox WHERE kind = 'NIGHT_AUDIT_PACK' AND status = 'QUEUED'`) != 1 {
		t.Fatal("a refused resend queues nothing")
	}
}

func TestASenderThatNeverReturnsDoesNotHoldTheRun(t *testing.T) {
	f := setup(t)
	sender := &fakeSender{block: true}
	svc := f.mailer(t, sender)
	f.recipient(t, "gm@example.test")
	f.closeDay(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = svc.ProcessDue(ctx) // the worker is stuck in the sender
	}()
	ran := make(chan error, 1)
	go func() {
		_, err := f.run(t)
		ran <- err
	}()
	select {
	case err := <-ran:
		must(t, err)
	case <-time.After(60 * time.Second):
		t.Fatal("the run waited for the mail server")
	}
	cancel()
	<-done
	if f.Count(t, `SELECT count(*) FROM nap_packs WHERE property_id = $1`, f.propID) != 2 {
		t.Fatal("both days have a pack")
	}
}

func TestARecipientEditedWhileTheRunBuildsIsSeenWhole(t *testing.T) {
	f := setup(t)
	f.mailer(t, &fakeSender{})
	r := f.recipient(t, "gm@example.test")
	var wg sync.WaitGroup
	wg.Add(2)
	var runErr, editErr error
	go func() {
		defer wg.Done()
		_, runErr = f.run(t)
	}()
	go func() {
		defer wg.Done()
		_, editErr = f.pack.UpdateRecipient(f.admin, f.propID, r.ID, auditpack.RecipientPatch{Sections: &[]string{auditpack.SecGuestLedger}})
	}()
	wg.Wait()
	must(t, runErr)
	must(t, editErr)
	got := f.outbox(t)
	if len(got) != 1 {
		t.Fatalf("outbox: %+v", got)
	}
	if s := strings.Join(got[0].Sections, ","); s != "SUMMARY,FLASH" && s != "GUEST_LEDGER" {
		t.Fatalf("sections of a recipient before or after the edit, never a mix: %s", s)
	}
}

func TestTheHistoryKeepsTheAddressOfADeletedRecipient(t *testing.T) {
	f := setup(t)
	svc := f.mailer(t, &fakeSender{})
	r := f.recipient(t, "gm@example.test")
	bd := f.closeDay(t)
	if _, _, err := svc.ProcessDue(context.Background()); err != nil {
		t.Fatal(err)
	}
	must(t, f.pack.DeleteRecipient(f.admin, f.propID, r.ID))
	h, err := f.pack.Deliveries(f.admin, f.propID, bd)
	must(t, err)
	if !h.EmailEnabled || len(h.Items) != 1 {
		t.Fatalf("history: %+v", h)
	}
	it := h.Items[0]
	if it.Email != "gm@example.test" || it.Status != "SENT" || it.SentAt == nil || len(it.PDFSHA256) != 64 || it.Renderer != "1" || it.QueuedBy == "" || it.Part != 1 || it.Parts != 1 {
		t.Fatalf("item: %+v", it)
	}
	if _, err := f.pack.Deliveries(f.admin, f.propID, d("2026-01-01")); appErrCode(err) != "PACK_NOT_FOUND" {
		t.Fatalf("a day with no pack: %v", err)
	}
	list, err := f.pack.List(f.admin, f.propID, auditpack.ListInput{})
	must(t, err)
	if len(list.Items) != 1 || list.Items[0].MailSent != 1 || list.Items[0].MailFailed != 0 || list.Items[0].MailQueued != 0 || !list.EmailEnabled {
		t.Fatalf("archive: %+v", list)
	}
}

func TestAGivenUpMessageIsAWarningUntilTheDaySendsAgain(t *testing.T) {
	f := setup(t)
	sender := &fakeSender{err: fmt.Errorf("550 mailbox unavailable: %w", notifications.ErrPermanent)}
	svc := f.mailer(t, sender)
	f.recipient(t, "gm@example.test")
	bd := f.closeDay(t)
	if _, failed, err := svc.ProcessDue(context.Background()); err != nil || failed != 1 {
		t.Fatalf("failed %d: %v", failed, err)
	}
	warn := func() []string {
		t.Helper()
		pv, err := f.Audit.Preview(f.admin, f.propID)
		must(t, err)
		var out []string
		for _, w := range pv.Warnings.PackDeliveryFailed {
			out = append(out, w.BusinessDate.String()+" "+strings.Join(w.Addresses, ","))
		}
		return out
	}
	if got := warn(); len(got) != 1 || got[0] != bd.String()+" gm@example.test" {
		t.Fatalf("warning: %v", got)
	}
	sender.mu.Lock()
	sender.err = nil
	sender.mu.Unlock()
	_, err := f.pack.Resend(f.admin, f.propID, bd, auditpack.ResendInput{})
	must(t, err)
	if got := warn(); len(got) != 0 {
		t.Fatalf("a message waits, so the day is not a warning: %v", got)
	}
	if sent, _, err := svc.ProcessDue(context.Background()); err != nil || sent != 1 {
		t.Fatalf("sent %d: %v", sent, err)
	}
	if got := warn(); len(got) != 0 {
		t.Fatalf("sent: %v", got)
	}
}

func TestTheRecipientsAreChecked(t *testing.T) {
	f := setup(t)
	f.mailer(t, &fakeSender{})
	r := f.recipient(t, "gm@example.test")
	for name, in := range map[string]auditpack.RecipientInput{
		"no @":            {Email: "gm"},
		"two addresses":   {Email: "a@b.test, c@d.test"},
		"a line break":    {Email: "a@b.test\r\nBcc: x@y.test"},
		"a name with one": {Email: "x@y.test", Name: "GM\r\nBcc: z@z.test"},
	} {
		if _, err := f.pack.CreateRecipient(f.admin, f.propID, in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := f.pack.CreateRecipient(f.admin, f.propID, auditpack.RecipientInput{Email: "x@y.test", Sections: []string{"FORECAST"}}); appErrCode(err) != "PACK_SECTION_INVALID" {
		t.Errorf("a section that does not exist: %v", err)
	}
	if _, err := f.pack.CreateRecipient(f.admin, f.propID, auditpack.RecipientInput{Email: "GM@Example.TEST"}); appErrCode(err) != "PACK_RECIPIENT_EXISTS" {
		t.Errorf("the same address in another case: %v", err)
	}
	if r.IsActive != true || strings.Join(r.Sections, ",") != "SUMMARY,FLASH" {
		t.Errorf("the default sections: %+v", r)
	}
	if _, err := f.pack.UpdateRecipient(f.admin, f.propID, 999999, auditpack.RecipientPatch{Name: ptr("x")}); appErrCode(err) != "PACK_RECIPIENT_NOT_FOUND" {
		t.Errorf("update of none: %v", err)
	}
	if err := f.pack.DeleteRecipient(f.admin, f.propID, 999999); appErrCode(err) != "PACK_RECIPIENT_NOT_FOUND" {
		t.Errorf("delete of none: %v", err)
	}
	must(t, f.pack.DeleteRecipient(f.admin, f.propID, r.ID))
	for _, a := range []string{"nap_recipient.created", "nap_recipient.deleted"} {
		if f.Count(t, `SELECT count(*) FROM audit_logs WHERE action = $1 AND (new_data::text LIKE '%gm@example.test%' OR old_data::text LIKE '%gm@example.test%')`, a) != 1 {
			t.Errorf("audit %s with the address", a)
		}
	}
}

func TestTheMailRoutes(t *testing.T) {
	f := setup(t)
	f.mailer(t, &fakeSender{})
	bd := f.closeDay(t)
	mux := http.NewServeMux()
	auditpack.NewHandler(f.pack).Register(mux)
	base := "/api/v1/properties/" + strconv.FormatInt(f.propID, 10) + "/night-audit/"
	call := func(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, method, base+path, strings.NewReader(body))
		if body == "" {
			req = httptest.NewRequestWithContext(ctx, method, base+path, nil)
		} else {
			req.Header.Set("Content-Type", "application/json")
		}
		mux.ServeHTTP(rec, req)
		return rec
	}
	viewer := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackView)
	manager := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackManage)
	sender := f.User(t, f.tenantID, f.propID, auth.PermNightAuditPackResend)
	nobody := f.User(t, f.tenantID, f.propID, auth.PermReportView)

	rec := call(manager, "POST", "pack-recipients", `{"email":"gm@example.test","name":"GM"}`)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"SUMMARY"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	id := "1"
	if rec := call(viewer, "GET", "pack-recipients", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "gm@example.test") {
		t.Fatalf("a viewer reads the list: %d %s", rec.Code, rec.Body)
	}
	if rec := call(nobody, "GET", "pack-recipients", ""); rec.Code != 403 {
		t.Fatalf("list without a permission: %d", rec.Code)
	}
	if rec := call(viewer, "POST", "pack-recipients", `{"email":"x@y.test"}`); rec.Code != 403 {
		t.Fatalf("a viewer creates: %d", rec.Code)
	}
	if rec := call(viewer, "POST", "pack-recipients", `not json`); rec.Code != 403 {
		t.Fatalf("the permission comes before the body: %d", rec.Code)
	}
	if rec := call(manager, "POST", "pack-recipients", `not json`); rec.Code != 422 && rec.Code != 400 {
		t.Fatalf("a body that is not JSON: %d", rec.Code)
	}
	if rec := call(manager, "PATCH", "pack-recipients/x", `{}`); rec.Code != 422 {
		t.Fatalf("a bad id: %d", rec.Code)
	}
	if rec := call(viewer, "PATCH", "pack-recipients/x", `{}`); rec.Code != 403 {
		t.Fatalf("a bad id without the permission: %d", rec.Code)
	}
	if rec := call(manager, "PATCH", "pack-recipients/"+id, `{"name":"Manager","is_active":false}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"is_active":false`) {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body)
	}
	if rec := call(manager, "PATCH", "pack-recipients/"+id, `{"is_active":true}`); rec.Code != 200 {
		t.Fatalf("patch back: %d %s", rec.Code, rec.Body)
	}
	if rec := call(viewer, "GET", "packs/"+bd.String()+"/deliveries", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"email_enabled":true`) {
		t.Fatalf("deliveries: %d %s", rec.Code, rec.Body)
	}
	if rec := call(sender, "GET", "packs/"+bd.String()+"/deliveries", ""); rec.Code != 403 {
		t.Fatalf("the one who resends cannot read the history: %d", rec.Code)
	}
	if rec := call(viewer, "POST", "packs/"+bd.String()+"/resend", ""); rec.Code != 403 {
		t.Fatalf("a viewer resends: %d", rec.Code)
	}
	if rec := call(viewer, "POST", "packs/not-a-date/resend", ""); rec.Code != 403 {
		t.Fatalf("the permission comes before the date: %d", rec.Code)
	}
	if rec := call(sender, "POST", "packs/not-a-date/resend", ""); rec.Code != 422 {
		t.Fatalf("a bad date: %d", rec.Code)
	}
	if rec := call(sender, "POST", "packs/"+bd.String()+"/resend", `{"recipient_ids":[`+id+`]}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "gm@example.test") {
		t.Fatalf("resend: %d %s", rec.Code, rec.Body)
	}
	if rec := call(sender, "POST", "packs/"+bd.String()+"/resend", ""); rec.Code != 409 || !strings.Contains(rec.Body.String(), "PACK_ALREADY_QUEUED") {
		t.Fatalf("resend again: %d %s", rec.Code, rec.Body)
	}
	if rec := call(manager, "DELETE", "pack-recipients/"+id, ""); rec.Code != 204 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := call(manager, "DELETE", "pack-recipients/"+id, ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "PACK_RECIPIENT_NOT_FOUND") {
		t.Fatalf("delete twice: %d %s", rec.Code, rec.Body)
	}
	other := f.Tenant(t, "XYZ")
	stranger, _ := f.AdminAccount(t, other.ID)
	if rec := call(stranger, "GET", "pack-recipients", ""); rec.Code != 404 || !strings.Contains(rec.Body.String(), "PROPERTY_NOT_FOUND") {
		t.Fatalf("another tenant: %d %s", rec.Code, rec.Body)
	}
}
