package notifications

import (
	"context"
	"html"
	"net/http"
	"strconv"
	"strings"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/documents"
	"kamarapms/internal/notifications/notificationsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/tenancy"
)

// The e-mails of the banquet (docs/architecture/36-banquet.md 4.6, F8): the proposal and the contract of an event go through the same outbox and worker as the confirmation of a reservation.
const (
	KindBanquetProposal = "BANQUET_PROPOSAL"
	KindBanquetContract = "BANQUET_CONTRACT"
)

// EventEntry is one e-mail of an event, with the document it carries.
type EventEntry struct {
	Entry
	DocumentID     int64   `json:"document_id"`
	DocumentNumber *string `json:"document_number"`
	RevisionNo     *int    `json:"revision_no"`
	Language       string  `json:"language"`
}

// EventStatus is the e-mail state of an event.
type EventStatus struct {
	Enabled bool         `json:"enabled"`
	Data    []EventEntry `json:"data"`
}

// SendInput says to whom and in which language; an empty To is the contact e-mail of the event, an empty Language is English.
type SendInput struct {
	To       string `json:"to"`
	Language string `json:"language"`
}

// EventEmails lists the e-mails of an event, newest first (banquet.read).
func (s *Service) EventEmails(ctx context.Context, propertyID, eventID int64) (EventStatus, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return EventStatus{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermBanquetRead); err != nil {
		return EventStatus{}, err
	}
	if s.banq == nil {
		return EventStatus{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	if _, err := s.banq.GetEvent(ctx, propertyID, eventID); err != nil { // an event of another property is not found
		return EventStatus{}, err
	}
	rows, err := s.q(ctx).ListEventEmails(ctx, notificationsdb.ListEventEmailsParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: &eventID})
	if err != nil {
		return EventStatus{}, err
	}
	out := EventStatus{Enabled: s.Enabled(), Data: make([]EventEntry, len(rows))}
	for i, r := range rows {
		e := EventEntry{Entry: Entry{ID: r.ID, Kind: r.Kind, To: r.ToAddress, Status: r.Status, Attempts: int(r.Attempts), SentAt: r.SentAt, CreatedAt: r.CreatedAt},
			DocumentID: derefInt64(r.DocumentID), DocumentNumber: r.DocumentNumber, Language: derefStr(r.Language)}
		if r.RevisionNo.Valid {
			n := int(r.RevisionNo.Int32)
			e.RevisionNo = &n
		}
		if r.LastError != nil {
			e.LastError = *r.LastError
		}
		out.Data[i] = e
	}
	return out, nil
}

// SendProposal records the proposal as it is now and queues it (banquet.manage); SendContract numbers the contract of the revision in force and queues it (banquet.confirm). Both answer
// 409 EMAIL_NOT_CONFIGURED without a mail server, 422 EVENT_HAS_NO_EMAIL when no address is known, and 409 EMAIL_ALREADY_QUEUED while the same message is still waiting.
func (s *Service) SendProposal(ctx context.Context, propertyID, eventID int64, in SendInput) (EventEntry, error) {
	return s.sendEvent(ctx, propertyID, eventID, in, KindBanquetProposal)
}

// SendContract: see SendProposal.
func (s *Service) SendContract(ctx context.Context, propertyID, eventID int64, in SendInput) (EventEntry, error) {
	return s.sendEvent(ctx, propertyID, eventID, in, KindBanquetContract)
}

func (s *Service) sendEvent(ctx context.Context, propertyID, eventID int64, in SendInput, kind string) (EventEntry, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return EventEntry{}, err
	}
	if s.banq == nil {
		return EventEntry{}, apperr.Conflict("MODULE_NOT_IN_PLAN", "the banquet is not available")
	}
	perm := auth.PermBanquetManage
	if kind == KindBanquetContract {
		perm = auth.PermBanquetConfirm
	}
	if err := s.authz.Require(ctx, propertyID, perm); err != nil { // the permission is asked before anything about the server is told
		return EventEntry{}, err
	}
	if s.sender == nil {
		return EventEntry{}, apperr.Conflict("EMAIL_NOT_CONFIGURED", "e-mail is not configured on this server")
	}
	lang := strings.ToLower(strings.TrimSpace(in.Language))
	if lang == "" {
		lang = "en"
	}
	if lang != "en" && lang != "id" {
		return EventEntry{}, apperr.BadRequest("INVALID_LANGUAGE", "language is en or id")
	}
	var out EventEntry
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		var doc banquet.DocumentRow
		if kind == KindBanquetContract {
			doc, err = s.banq.IssueContract(ctx, propertyID, eventID)
		} else {
			doc, err = s.banq.RecordProposal(ctx, propertyID, eventID)
		}
		if err != nil {
			return err
		}
		file, err := s.banq.DocumentFileOf(ctx, propertyID, doc.ID)
		if err != nil {
			return err
		}
		to := strings.TrimSpace(in.To)
		if to == "" {
			to = strings.TrimSpace(file.Snapshot.Event.ContactEmail)
		}
		if to == "" {
			return apperr.New(apperr.KindInvalid, "EVENT_HAS_NO_EMAIL", "the event has no contact e-mail and none was given")
		}
		if !validAddress(to) {
			return apperr.BadRequest("INVALID_EMAIL", "the e-mail address is not valid")
		}
		q := s.q(ctx)
		waiting, err := q.EventEmailQueued(ctx, notificationsdb.EventEmailQueuedParams{PropertyID: propertyID, EventID: &eventID, Kind: kind, ToAddress: to})
		if err != nil {
			return err
		}
		if waiting {
			return apperr.Conflict("EMAIL_ALREADY_QUEUED", "this e-mail is already waiting to be sent")
		}
		docID := doc.ID
		row, err := q.InsertEventEmail(ctx, notificationsdb.InsertEventEmailParams{TenantID: p.TenantID, PropertyID: propertyID, Kind: kind, EventID: &eventID, DocumentID: &docID, Language: &lang, ToAddress: to, Now: s.clock.Now(), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(),
			Action: "email.queued", EntityType: "banquet_event", EntityID: eventID, EntityLabel: file.Snapshot.Event.Number,
			New: map[string]any{"kind": kind, "to": to, "outbox_id": row.ID, "document_id": doc.ID}}); err != nil {
			return err
		}
		out = EventEntry{Entry: entryOf(row), DocumentID: doc.ID, DocumentNumber: doc.Number, RevisionNo: doc.RevisionNo, Language: lang}
		return nil
	})
	return out, err
}

// composeBanquet builds the proposal or the contract e-mail of an outbox row. The PDF is the recorded document in the language of the row, so what is sent is what was recorded.
func (s *Service) composeBanquet(ctx context.Context, row notificationsdb.EmailOutbox) (Message, string, error) {
	if s.banq == nil || row.DocumentID == nil {
		return Message{}, "the banquet document is not available", nil
	}
	file, err := s.banq.DocumentFileOf(ctx, row.PropertyID, *row.DocumentID)
	if err != nil {
		if ae, ok := apperr.As(err); ok && ae.Kind == apperr.KindNotFound {
			return Message{}, "the document or the event no longer exists", nil
		}
		return Message{}, "", err
	}
	// the snapshot says what the event was when the document was recorded; whether it is still worth sending depends on what it is now
	if row.BanquetEventID != nil {
		now, err := s.banq.GetEvent(ctx, row.PropertyID, *row.BanquetEventID)
		if err != nil {
			if ae, ok := apperr.As(err); ok && ae.Kind == apperr.KindNotFound {
				return Message{}, "the event no longer exists", nil
			}
			return Message{}, "", err
		}
		if now.Status == "CANCELLED" || now.Status == "LOST" {
			return Message{}, "the event is closed", nil
		}
	}
	prop, err := s.days.GetProperty(ctx, row.PropertyID)
	if err != nil {
		return Message{}, "", err
	}
	lang := documents.LangEN
	if row.Language != nil && *row.Language == "id" {
		lang = documents.LangID
	}
	doc, err := s.docs.BanquetRecorded(ctx, row.PropertyID, *row.DocumentID, lang)
	if err != nil {
		return Message{}, "", err
	}
	ev := file.Snapshot.Event
	name := ev.ContactName
	if name == "" {
		name = ev.Booker
	}
	if name == "" {
		name = "Sir or Madam"
	}
	what, subject := "proposal", "Proposal "+ev.Name
	if row.Kind == KindBanquetContract {
		num := ""
		if file.Number != nil {
			num = *file.Number
		}
		what, subject = "contract "+num, "Contract "+num+" "+ev.Name
	}
	contact := strings.Join(nonEmpty(prop.Phone, prop.Email), "  |  ")
	var t strings.Builder
	t.WriteString("Dear " + name + ",\n\nPlease find attached the " + what + " for " + ev.Name + " (" + ev.Number + ") at " + prop.Name + ".\n")
	if row.Kind == KindBanquetContract {
		t.WriteString("Please sign it and return a copy to us.\n")
	} else {
		t.WriteString("Please let us know if you would like any change.\n")
	}
	t.WriteString("\nKind regards,\n" + prop.Name + "\n")
	var h strings.Builder
	h.WriteString("<p>Dear " + html.EscapeString(name) + ",</p><p>Please find attached the " + html.EscapeString(what) + " for <strong>" + html.EscapeString(ev.Name) + "</strong> (" + html.EscapeString(ev.Number) + ") at " + html.EscapeString(prop.Name) + ".</p>")
	if row.Kind == KindBanquetContract {
		h.WriteString("<p>Please sign it and return a copy to us.</p>")
	} else {
		h.WriteString("<p>Please let us know if you would like any change.</p>")
	}
	h.WriteString("<p>Kind regards,<br>" + html.EscapeString(prop.Name))
	for _, l := range nonEmpty(prop.Address, prop.City, contact) {
		t.WriteString(l + "\n")
		h.WriteString("<br>" + html.EscapeString(l))
	}
	h.WriteString("</p>")
	return Message{To: row.ToAddress, Subject: subject + " - " + prop.Name, Text: t.String(), HTML: h.String(),
		Attachments: []Attachment{{Filename: doc.Filename, ContentType: "application/pdf", Data: doc.PDF}}}, "", nil
}

func derefInt64(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// registerBanquet mounts the e-mails of an event.
func (h *Handler) registerBanquet(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}/banquet/events/{id}"
	mux.Handle("GET "+p+"/emails", httpx.HandlerFunc(h.eventEmails))
	mux.Handle("POST "+p+"/emails/proposal", httpx.HandlerFunc(h.sendEvent(KindBanquetProposal)))
	mux.Handle("POST "+p+"/emails/contract", httpx.HandlerFunc(h.sendEvent(KindBanquetContract)))
}

func eventRoute(r *http.Request) (propertyID, id int64, err error) {
	if propertyID, err = tenancy.PropertyID(r); err != nil {
		return 0, 0, err
	}
	id, err = strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0, 0, apperr.NotFound("EVENT_NOT_FOUND", "the event does not exist in this property")
	}
	return propertyID, id, nil
}

func (h *Handler) eventEmails(w http.ResponseWriter, r *http.Request) error {
	pid, id, err := eventRoute(r)
	if err != nil {
		return err
	}
	st, err := h.svc.EventEmails(r.Context(), pid, id)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, st)
}

func (h *Handler) sendEvent(kind string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		pid, id, err := eventRoute(r)
		if err != nil {
			return err
		}
		var in SendInput
		if r.ContentLength != 0 {
			if err := httpx.DecodeJSON(w, r, &in); err != nil {
				return err
			}
		}
		var e EventEntry
		if kind == KindBanquetContract {
			e, err = h.svc.SendContract(r.Context(), pid, id, in)
		} else {
			e, err = h.svc.SendProposal(r.Context(), pid, id, in)
		}
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusAccepted, e)
	}
}
