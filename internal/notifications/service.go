package notifications

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/documents"
	"kamarapms/internal/notifications/notificationsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations"
	"kamarapms/internal/tenancy"
)

// KindConfirmation is the confirmation e-mail of a reservation.
const KindConfirmation = "RESERVATION_CONFIRMATION"

// Delivery bounds: a message is tried this many times, waiting longer after each failure.
const (
	maxAttempts = 5
	batchSize   = 20
	lease       = 10 * time.Minute
)

var backoff = [...]time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// Service queues and sends the e-mails of the hotel. A nil sender means e-mail is off: nothing is queued.
type Service struct {
	txm    *db.TxManager
	clock  clock.Clock
	audit  *audit.Writer
	authz  auth.Authorizer
	days   *tenancy.Service
	docs   *documents.Service
	res    *reservations.Service
	sender Sender
}

// NewService wires the service. sender may be nil (e-mail off).
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, docs *documents.Service, res *reservations.Service, sender Sender) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, docs: docs, res: res, sender: sender}
}

// Enabled reports whether e-mail is configured.
func (s *Service) Enabled() bool { return s.sender != nil }

func (s *Service) q(ctx context.Context) *notificationsdb.Queries {
	return notificationsdb.New(s.txm.DB(ctx))
}

func validAddress(a string) bool {
	if a == "" || len(a) > 254 || strings.ContainsAny(a, "\r\n") {
		return false
	}
	p, err := mail.ParseAddress(a)
	return err == nil && p.Address == a
}

// ReservationConfirmed is the hook the reservations service calls, inside its transaction, when a reservation is
// confirmed: the confirmation is queued for the booker when there is an address. It never fails a booking because
// of a missing or odd address.
func (s *Service) ReservationConfirmed(ctx context.Context, tenantID, propertyID, reservationID int64, bd civil.Date, actorID *int64) error {
	if s.sender == nil {
		return nil
	}
	_, err := s.enqueue(ctx, tenantID, propertyID, reservationID, bd, actorID, false)
	return err
}

// enqueue queues a confirmation. explicit is a staff request (a resend): then a missing address is an error.
func (s *Service) enqueue(ctx context.Context, tenantID, propertyID, reservationID int64, bd civil.Date, actorID *int64, explicit bool) (notificationsdb.EmailOutbox, error) {
	q := s.q(ctx)
	to, err := q.BookerEmail(ctx, notificationsdb.BookerEmailParams{TenantID: tenantID, PropertyID: propertyID, ID: reservationID})
	if err != nil {
		return notificationsdb.EmailOutbox{}, err
	}
	if !validAddress(to) {
		if explicit {
			return notificationsdb.EmailOutbox{}, apperr.Conflict("GUEST_HAS_NO_EMAIL", "the booker has no valid e-mail address: add one to the guest profile first")
		}
		return notificationsdb.EmailOutbox{}, nil
	}
	row, err := q.InsertOutbox(ctx, notificationsdb.InsertOutboxParams{TenantID: tenantID, PropertyID: propertyID, Kind: KindConfirmation, ReservationID: reservationID, ToAddress: to, Now: s.clock.Now(), ActorID: actorID})
	if err != nil {
		return notificationsdb.EmailOutbox{}, err
	}
	err = s.audit.Write(ctx, audit.Entry{TenantID: tenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: actorID,
		Action: "email.queued", EntityType: "reservation", EntityID: reservationID, New: map[string]any{"kind": KindConfirmation, "to": to, "outbox_id": row.ID}})
	return row, err
}

// Entry is a queued, sent or failed message as the screen shows it.
type Entry struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	To        string     `json:"to"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	SentAt    *time.Time `json:"sent_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// Status is the e-mail state of a reservation.
type Status struct {
	Enabled bool    `json:"enabled"`
	Data    []Entry `json:"data"`
}

func entryOf(r notificationsdb.EmailOutbox) Entry {
	e := Entry{ID: r.ID, Kind: r.Kind, To: r.ToAddress, Status: r.Status, Attempts: int(r.Attempts), SentAt: r.SentAt, CreatedAt: r.CreatedAt}
	if r.LastError != nil {
		e.LastError = *r.LastError
	}
	return e
}

// List returns the e-mails of a reservation, newest first (reservation.read).
func (s *Service) List(ctx context.Context, propertyID, reservationID int64) (Status, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Status{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermReservationRead); err != nil {
		return Status{}, err
	}
	rows, err := s.q(ctx).ListOutboxForReservation(ctx, notificationsdb.ListOutboxForReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID})
	if err != nil {
		return Status{}, err
	}
	out := Status{Enabled: s.Enabled(), Data: make([]Entry, len(rows))}
	for i, r := range rows {
		out.Data[i] = entryOf(r)
	}
	return out, nil
}

// Resend queues the confirmation again (reservation.update): the booker did not get it, or the address was fixed.
// 409 EMAIL_NOT_CONFIGURED without a mail server, RESERVATION_NOT_CONFIRMED for a draft or cancelled reservation,
// GUEST_HAS_NO_EMAIL, and EMAIL_ALREADY_QUEUED while one is still waiting.
func (s *Service) Resend(ctx context.Context, propertyID, reservationID int64) (Entry, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Entry{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermReservationUpdate); err != nil {
		return Entry{}, err
	}
	if s.sender == nil {
		return Entry{}, apperr.Conflict("EMAIL_NOT_CONFIGURED", "e-mail is not configured on this server")
	}
	var out Entry
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		res, err := s.res.Get(ctx, propertyID, reservationID)
		if err != nil {
			return err
		}
		if res.Status != reservations.StatusConfirmed {
			return apperr.Conflict("RESERVATION_NOT_CONFIRMED", "only a confirmed reservation has a confirmation to send")
		}
		rows, err := s.q(ctx).ListOutboxForReservation(ctx, notificationsdb.ListOutboxForReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Status == "QUEUED" {
				return apperr.Conflict("EMAIL_ALREADY_QUEUED", "a confirmation is already waiting to be sent")
			}
		}
		row, err := s.enqueue(ctx, p.TenantID, propertyID, reservationID, day.BusinessDate, p.ActorID(), true)
		if err != nil {
			return err
		}
		out = entryOf(row)
		return nil
	})
	return out, err
}

// ProcessDue sends the messages that are due and records the outcome of each. It returns how many were sent and
// how many failed an attempt. Messages are claimed in their own short transaction with a lease, so concurrent
// workers never send one twice, and the sending itself holds no transaction and no lock.
func (s *Service) ProcessDue(ctx context.Context) (sent, failed int, err error) {
	if s.sender == nil {
		return 0, 0, nil
	}
	var rows []notificationsdb.EmailOutbox
	now := s.clock.Now()
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		var err error
		rows, err = s.q(ctx).ClaimDue(ctx, notificationsdb.ClaimDueParams{LeaseUntil: now.Add(lease), Now: now, RowLimit: batchSize})
		return err
	})
	if err != nil {
		return 0, 0, err
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			break
		}
		if s.deliver(ctx, row) {
			sent++
		} else {
			failed++
		}
	}
	return sent, failed, nil
}

// deliver sends one claimed message and records what happened; it reports whether it was sent.
func (s *Service) deliver(ctx context.Context, row notificationsdb.EmailOutbox) bool {
	pctx := auth.WithPrincipal(ctx, auth.Principal{TenantID: row.TenantID, IsTenantAdmin: true}) // read-only rendering, as the tenant
	msg, skip, err := s.compose(pctx, row)
	switch {
	case skip != "":
		s.record(ctx, func(q *notificationsdb.Queries) error {
			return q.MarkSkipped(ctx, notificationsdb.MarkSkippedParams{ID: row.ID, LastError: &skip})
		})
		return false
	case err == nil:
		sctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		err = s.sender.Send(sctx, msg)
		cancel()
	}
	if err == nil {
		s.record(ctx, func(q *notificationsdb.Queries) error {
			return q.MarkSent(ctx, notificationsdb.MarkSentParams{ID: row.ID, Now: s.clock.Now()})
		})
		return true
	}
	text := truncate(err.Error(), 480)
	attempt := int(row.Attempts) + 1
	if errors.Is(err, ErrPermanent) || attempt >= maxAttempts {
		s.record(ctx, func(q *notificationsdb.Queries) error {
			return q.MarkFailed(ctx, notificationsdb.MarkFailedParams{ID: row.ID, LastError: &text})
		})
		slog.Warn("e-mail given up", "outbox_id", row.ID, "attempts", attempt, "error", text)
		return false
	}
	next := s.clock.Now().Add(backoff[attempt-1])
	s.record(ctx, func(q *notificationsdb.Queries) error {
		return q.MarkRetry(ctx, notificationsdb.MarkRetryParams{ID: row.ID, NextAttemptAt: next, LastError: &text})
	})
	slog.Warn("e-mail will be retried", "outbox_id", row.ID, "attempt", attempt, "error", text)
	return false
}

func (s *Service) record(ctx context.Context, f func(q *notificationsdb.Queries) error) {
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error { return f(s.q(ctx)) })
	if err != nil {
		slog.Error("e-mail outcome not recorded", "error", err)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// compose builds the message of an outbox row. skip is a reason to drop it without an error (the reservation is no
// longer confirmed).
func (s *Service) compose(ctx context.Context, row notificationsdb.EmailOutbox) (msg Message, skip string, err error) {
	res, err := s.res.Get(ctx, row.PropertyID, row.ReservationID)
	if err != nil {
		return Message{}, "", err
	}
	if res.Status != reservations.StatusConfirmed {
		return Message{}, "the reservation is no longer confirmed", nil
	}
	prop, err := s.days.GetProperty(ctx, row.PropertyID)
	if err != nil {
		return Message{}, "", err
	}
	doc, err := s.docs.Confirmation(ctx, row.PropertyID, row.ReservationID)
	if err != nil {
		return Message{}, "", err
	}
	name := "guest"
	if res.Guest != nil {
		name = strings.TrimSpace(res.Guest.FirstName + " " + res.Guest.LastName)
	}
	var lines []string
	for _, l := range res.Rooms {
		if l.Status == reservations.LineCancelled {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s: %s to %s (%d night(s), %d adult(s))", l.RoomTypeCode, l.ArrivalDate, l.DepartureDate, l.Nights, l.AdultCount))
	}
	contact := strings.Join(nonEmpty(prop.Phone, prop.Email), "  |  ")
	var t strings.Builder
	t.WriteString("Dear " + name + ",\n\nThank you for your reservation at " + prop.Name + ". Your booking is confirmed.\n\n")
	t.WriteString("Confirmation number: " + res.ConfirmationNumber + "\n")
	for _, l := range lines {
		t.WriteString("  - " + l + "\n")
	}
	t.WriteString("\nCheck-in is from " + prop.CheckInTime.String() + " and check-out by " + prop.CheckOutTime.String() + ".\n")
	if res.SpecialRequest != "" {
		t.WriteString("Your request: " + res.SpecialRequest + "\n")
	}
	t.WriteString("\nThe confirmation is attached as a PDF. Please quote the confirmation number when you contact us.\n\n")
	t.WriteString("Kind regards,\n" + prop.Name + "\n")
	for _, l := range nonEmpty(prop.Address, prop.City, contact) {
		t.WriteString(l + "\n")
	}
	var h strings.Builder
	h.WriteString("<p>Dear " + html.EscapeString(name) + ",</p><p>Thank you for your reservation at <strong>" + html.EscapeString(prop.Name) + "</strong>. Your booking is confirmed.</p>")
	h.WriteString("<p>Confirmation number: <strong>" + html.EscapeString(res.ConfirmationNumber) + "</strong></p><ul>")
	for _, l := range lines {
		h.WriteString("<li>" + html.EscapeString(l) + "</li>")
	}
	h.WriteString("</ul><p>Check-in is from " + prop.CheckInTime.String() + " and check-out by " + prop.CheckOutTime.String() + ".</p>")
	if res.SpecialRequest != "" {
		h.WriteString("<p>Your request: " + html.EscapeString(res.SpecialRequest) + "</p>")
	}
	h.WriteString("<p>The confirmation is attached as a PDF. Please quote the confirmation number when you contact us.</p><p>Kind regards,<br>" + html.EscapeString(prop.Name))
	for _, l := range nonEmpty(prop.Address, prop.City, contact) {
		h.WriteString("<br>" + html.EscapeString(l))
	}
	h.WriteString("</p>")
	return Message{
		To: row.ToAddress, Subject: "Reservation confirmation " + res.ConfirmationNumber + " - " + prop.Name, Text: t.String(), HTML: h.String(),
		Attachments: []Attachment{{Filename: doc.Filename, ContentType: "application/pdf", Data: doc.PDF}},
	}, "", nil
}

func nonEmpty(in ...string) []string {
	var out []string
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

// Run is the background worker: it sends what is due every interval until ctx ends.
func (s *Service) Run(ctx context.Context, interval time.Duration) {
	if s.sender == nil {
		return
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		sent, failed, err := s.ProcessDue(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("e-mail worker", "error", err)
		} else if sent+failed > 0 {
			slog.Info("e-mails processed", "sent", sent, "not_sent", failed)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
