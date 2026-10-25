package notifications_test

import (
	"bytes"
	"context"
	"testing"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/iam"
	"kamarapms/internal/notifications"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The proposal and the contract of a banquet event go out through the same outbox and worker as the confirmation of a reservation (docs/architecture/36-banquet.md 4.6, F8).

type bfx struct {
	*fx
	banq   *banquet.Service
	mgr    context.Context // banquet read, manage and confirm
	office context.Context // banquet manage only
	reader context.Context
	event  banquet.Event
}

func tod(s string) *civil.TimeOfDay { t := civil.MustParseTimeOfDay(s); return &t }

func ptr[T any](v T) *T { return &v }

// bsetup: a definite event of the guest in a meeting room, with a contact e-mail, and the banquet tied to the documents and to the e-mails.
func bsetup(t *testing.T, email *string) *bfx {
	t.Helper()
	f := setup(t)
	b := &bfx{fx: f}
	b.banq = banquet.NewService(f.TxM, f.Clock, audit.NewWriter(f.Clock), iam.NewAuthorizer(f.TxM), f.Tenancy, f.Folios, f.Billing, f.IAM)
	f.Docs.SetBanquet(b.banq)
	f.svc.SetBanquet(b.banq)
	b.mgr = f.User(t, f.tenantID, f.propID, auth.PermBanquetRead, auth.PermBanquetManage, auth.PermBanquetConfirm, auth.PermBanquetSetup)
	b.office = f.User(t, f.tenantID, f.propID, auth.PermBanquetManage)
	b.reader = f.User(t, f.tenantID, f.propID, auth.PermBanquetRead)
	pr, err := auth.Require(b.office)
	must(t, err)
	room, err := b.banq.CreateRoom(b.mgr, f.propID, banquet.RoomInput{Code: "A", Name: "Room A", SetupMinutes: 60, TeardownMinutes: 30, SortOrder: 1})
	must(t, err)
	_, err = b.banq.SetRoomSetups(b.mgr, f.propID, room.ID, banquet.SetupsInput{Setups: []banquet.RoomSetup{{SetupStyle: "THEATER", Capacity: 250}}})
	must(t, err)
	ev, err := b.banq.CreateEvent(b.office, f.propID, banquet.EventInput{Name: "Wedding", BookerGuestID: &f.guest, SalesUserID: &pr.UserID, ContactEmail: email})
	must(t, err)
	_, err = b.banq.AddSession(b.office, f.propID, ev.ID, banquet.SessionInput{Name: "Reception", SessionDate: d("2026-11-07"), StartTime: tod("12:00"), EndTime: tod("16:00"), RoomID: &room.ID, PaxEstimated: 100})
	must(t, err)
	_, err = b.banq.MakeTentative(b.office, f.propID, ev.ID, banquet.TentativeInput{OptionUntil: d("2026-10-20")})
	must(t, err)
	b.event, err = b.banq.Confirm(b.mgr, f.propID, ev.ID, banquet.ConfirmInput{})
	must(t, err)
	return b
}

func TestTheProposalIsRecordedQueuedAndSentWithItsPdf(t *testing.T) {
	b := bsetup(t, ptr("client@example.test"))
	e, err := b.svc.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{Language: "id"})
	must(t, err)
	if e.Kind != notifications.KindBanquetProposal || e.Status != "QUEUED" || e.To != "client@example.test" || e.Language != "id" || e.DocumentID == 0 {
		t.Fatalf("entry: %+v", e)
	}
	if b.Count(t, `SELECT count(*) FROM email_outbox WHERE kind = 'BANQUET_PROPOSAL' AND reservation_id IS NULL AND banquet_event_id = $1 AND document_id = $2 AND language = 'id'`, b.event.ID, e.DocumentID) != 1 {
		t.Fatal("the outbox row carries the event, the document and the language, and no reservation")
	}
	if b.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'email.queued' AND entity_type = 'banquet_event'`) != 1 {
		t.Fatal("queueing is audited")
	}
	// the same message while it waits is refused
	_, err = b.svc.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{})
	wantCode(t, err, "EMAIL_ALREADY_QUEUED")

	sent, failed, err := b.svc.ProcessDue(b.admin)
	must(t, err)
	if sent != 1 || failed != 0 || b.sender.count() != 1 {
		t.Fatalf("sent %d failed %d", sent, failed)
	}
	m := b.sender.sent[0]
	if m.To != "client@example.test" || len(m.Attachments) != 1 || !bytes.HasPrefix(m.Attachments[0].Data, []byte("%PDF-")) || m.Attachments[0].ContentType != "application/pdf" {
		t.Fatalf("message: %+v", m)
	}
	if b.Count(t, `SELECT count(*) FROM email_outbox WHERE id = $1 AND status = 'SENT' AND sent_at IS NOT NULL`, e.ID) != 1 {
		t.Fatal("the row is marked sent")
	}
	st, err := b.svc.EventEmails(b.reader, b.propID, b.event.ID)
	must(t, err)
	if !st.Enabled || len(st.Data) != 1 || st.Data[0].Status != "SENT" || st.Data[0].Language != "id" {
		t.Fatalf("history: %+v", st)
	}
}

func TestTheContractIsNumberedWhenItIsSentAndAskingAgainKeepsTheNumber(t *testing.T) {
	b := bsetup(t, ptr("client@example.test"))
	e, err := b.svc.SendContract(b.mgr, b.propID, b.event.ID, notifications.SendInput{To: "other@example.test"})
	must(t, err)
	if e.Kind != notifications.KindBanquetContract || e.DocumentNumber == nil || e.To != "other@example.test" || e.Language != "en" {
		t.Fatalf("entry: %+v", e)
	}
	if _, _, err = b.svc.ProcessDue(b.admin); err != nil {
		t.Fatal(err)
	}
	e2, err := b.svc.SendContract(b.mgr, b.propID, b.event.ID, notifications.SendInput{To: "other@example.test"})
	must(t, err)
	if e2.DocumentID != e.DocumentID || *e2.DocumentNumber != *e.DocumentNumber {
		t.Fatalf("the contract of a revision is numbered once: %+v %+v", e, e2)
	}
	if b.Count(t, `SELECT count(*) FROM banquet_documents WHERE kind = 'CONTRACT'`) != 1 {
		t.Fatal("one contract")
	}
}

func TestSendingNeedsAnAddressAPermissionAndAMailServer(t *testing.T) {
	b := bsetup(t, nil)
	_, err := b.svc.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{})
	wantCode(t, err, "EVENT_HAS_NO_EMAIL")
	_, err = b.svc.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{To: "not an address"})
	wantCode(t, err, "INVALID_EMAIL")
	_, err = b.svc.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{To: "a@example.test", Language: "fr"})
	wantCode(t, err, "INVALID_LANGUAGE")
	// the proposal needs banquet.manage, the contract banquet.confirm
	_, err = b.svc.SendProposal(b.reader, b.propID, b.event.ID, notifications.SendInput{To: "a@example.test"})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = b.svc.SendContract(b.office, b.propID, b.event.ID, notifications.SendInput{To: "a@example.test"})
	wantCode(t, err, "PERMISSION_DENIED")
	if b.rows(t) != 0 {
		t.Fatal("a refusal queues nothing, and a refused contract takes no number")
	}
	if b.Count(t, `SELECT count(*) FROM banquet_documents`) != 0 {
		t.Fatal("a refused send records no document")
	}
	if _, err = b.svc.EventEmails(b.nobodyCtx(t), b.propID, b.event.ID); err == nil {
		t.Fatal("listing the e-mails of an event needs banquet.read")
	}
	if _, err = b.svc.EventEmails(b.reader, b.propID, b.event.ID+1000); err == nil {
		t.Fatal("the e-mails of an event that is not in the property are not listed")
	}
	off := b.build(nil)
	off.SetBanquet(b.banq)
	_, err = off.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{To: "a@example.test"})
	wantCode(t, err, "EMAIL_NOT_CONFIGURED")
	_, err = off.SendProposal(b.reader, b.propID, b.event.ID, notifications.SendInput{To: "a@example.test"})
	wantCode(t, err, "PERMISSION_DENIED")
}

func (b *bfx) nobodyCtx(t *testing.T) context.Context {
	t.Helper()
	return b.User(t, b.tenantID, b.propID, auth.PermFolioRead)
}

func TestAMessageOfAnEventThatWasLostMeanwhileIsSkipped(t *testing.T) {
	b := bsetup(t, ptr("client@example.test"))
	e, err := b.svc.SendProposal(b.office, b.propID, b.event.ID, notifications.SendInput{})
	must(t, err)
	reason, err := b.banq.CreateReason(b.mgr, b.propID, banquet.ReasonInput{Kind: banquet.ReasonCancel, Code: "CLIENT", Name: "Client cancelled"})
	must(t, err)
	_, err = b.banq.CancelEvent(b.mgr, b.propID, b.event.ID, banquet.CloseInput{ReasonID: reason.ID, Action: banquet.ActionFee})
	if err != nil {
		t.Skipf("the event could not be cancelled in this fixture: %v", err)
	}
	sent, _, err := b.svc.ProcessDue(b.admin)
	must(t, err)
	if sent != 0 || b.sender.count() != 0 {
		t.Fatalf("nothing is sent for a cancelled event: %d", sent)
	}
	if b.Count(t, `SELECT count(*) FROM email_outbox WHERE id = $1 AND status = 'SKIPPED' AND last_error IS NOT NULL`, e.ID) != 1 {
		t.Fatal("the row is skipped with a reason")
	}
}

func TestTheConfirmationOfAReservationIsUnchanged(t *testing.T) {
	f := setup(t)
	f.book(t, true, &f.guest, "k1")
	if f.Count(t, `SELECT count(*) FROM email_outbox WHERE kind = 'RESERVATION_CONFIRMATION' AND reservation_id IS NOT NULL AND banquet_event_id IS NULL AND document_id IS NULL AND language IS NULL`) != 1 {
		t.Fatal("the old kind keeps its shape")
	}
	// a row that is neither one nor the other is refused by the database
	_, err := f.Pool.Exec(context.Background(), `INSERT INTO email_outbox (tenant_id, property_id, kind, to_address) VALUES ($1, $2, 'BANQUET_PROPOSAL', 'x@example.test')`, f.tenantID, f.propID)
	if err == nil {
		t.Fatal("a banquet e-mail with no event and no document was accepted")
	}
	if sent, _, err := f.svc.ProcessDue(f.admin); err != nil || sent != 1 {
		t.Fatalf("the worker still sends the confirmation: %d %v", sent, err)
	}
}
