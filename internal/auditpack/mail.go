package auditpack

import (
	"context"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack/auditpackdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The e-mail of a pack after the run: the history of the messages and the resend (docs/architecture/38-reports-night-audit-pack.md 6.4). The worker that composes and sends is in
// internal/notifications; the rows it works on are the ones queued here and by the run.

// Delivery is one message of the history: the address and the sections as they were sent, and (once it was sent) the SHA-256 of the PDF and the version of the renderer that drew it (Q6).
type Delivery struct {
	ID        int64      `json:"id"`
	Email     string     `json:"email"`
	Sections  []string   `json:"sections"`
	Part      int        `json:"part"`
	Parts     int        `json:"parts"`
	Status    string     `json:"status"`
	Attempts  int        `json:"attempts"`
	LastError string     `json:"last_error,omitempty"`
	SentAt    *time.Time `json:"sent_at"`
	QueuedAt  time.Time  `json:"queued_at"`
	QueuedBy  string     `json:"queued_by"`
	PDFSHA256 string     `json:"pdf_sha256,omitempty"`
	Renderer  string     `json:"renderer,omitempty"`
}

// DeliveryList is the history of a pack.
type DeliveryList struct {
	EmailEnabled bool       `json:"email_enabled"`
	Items        []Delivery `json:"items"`
}

// Deliveries answers the history of the e-mail of the pack of a day, newest first (nightaudit.pack_view).
func (s *Service) Deliveries(ctx context.Context, propertyID int64, date civil.Date) (DeliveryList, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackView)
	if err != nil {
		return DeliveryList{}, err
	}
	st, err := s.load(ctx, p, propertyID, date)
	if err != nil {
		return DeliveryList{}, err
	}
	rows, err := s.q(ctx).ListPackDeliveries(ctx, auditpackdb.ListPackDeliveriesParams{TenantID: p.TenantID, PropertyID: propertyID, PackID: &st.id})
	if err != nil {
		return DeliveryList{}, err
	}
	out := DeliveryList{EmailEnabled: s.mailEnabled(), Items: make([]Delivery, len(rows))}
	for i, r := range rows {
		d := Delivery{ID: r.ID, Email: r.ToAddress, Sections: r.NapSections, Status: r.Status, Attempts: int(r.Attempts), SentAt: r.SentAt, QueuedAt: r.CreatedAt, QueuedBy: r.QueuedBy}
		if d.Sections == nil {
			d.Sections = []string{}
		}
		if r.NapPart.Valid {
			d.Part = int(r.NapPart.Int16)
		}
		if r.NapParts.Valid {
			d.Parts = int(r.NapParts.Int16)
		}
		if r.LastError != nil {
			d.LastError = *r.LastError
		}
		if r.NapPdfSha256 != nil {
			d.PDFSHA256 = *r.NapPdfSha256
		}
		if r.NapRenderer != nil {
			d.Renderer = *r.NapRenderer
		}
		out.Items[i] = d
	}
	return out, nil
}

// ResendInput names the recipients to send the pack to again; none means every active recipient.
type ResendInput struct {
	RecipientIDs []int64 `json:"recipient_ids"`
}

// SkippedRecipient is a recipient who got nothing from a resend.
type SkippedRecipient struct {
	Email  string `json:"email"`
	Reason string `json:"reason"`
}

// ResendResult says what the resend queued. Sections that failed in the pack are named: the resend sends the ones that are OK.
type ResendResult struct {
	Queued         []QueuedMail       `json:"queued"`
	Skipped        []SkippedRecipient `json:"skipped"`
	SectionsFailed []string           `json:"sections_failed"`
}

// Resend queues the pack again for recipients (nightaudit.pack_resend), only the active ones of the property (Q14: an address that is not a recipient is added first, audited, and removed after).
// 409 EMAIL_OFF when e-mail is not configured, 404 PACK_NOT_FOUND, 404 PACK_RECIPIENT_NOT_FOUND for an id that is not an active recipient, and 409 PACK_ALREADY_QUEUED for an address that has a
// message of the pack waiting (the partial unique index is the guarantee, so a double click is harmless).
func (s *Service) Resend(ctx context.Context, propertyID int64, date civil.Date, in ResendInput) (ResendResult, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackResend)
	if err != nil {
		return ResendResult{}, err
	}
	if !s.mailEnabled() {
		return ResendResult{}, apperr.Conflict("EMAIL_OFF", "e-mail is not configured on this server: ask the administrator to set the mail host")
	}
	out := ResendResult{Queued: []QueuedMail{}, Skipped: []SkippedRecipient{}, SectionsFailed: []string{}}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.load(ctx, p, propertyID, date)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		statuses := make(map[string]string, len(st.sections))
		for _, info := range st.manifest.Sections {
			statuses[info.Section] = info.Status
			if info.Status == StatusFailed {
				out.SectionsFailed = append(out.SectionsFailed, info.Section)
			}
		}
		recipients, err := q.ListActiveRecipients(ctx, auditpackdb.ListActiveRecipientsParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		if len(in.RecipientIDs) > 0 {
			byID := make(map[int64]auditpackdb.ListActiveRecipientsRow, len(recipients))
			for _, r := range recipients {
				byID[r.ID] = r
			}
			chosen := make([]auditpackdb.ListActiveRecipientsRow, 0, len(in.RecipientIDs))
			seen := map[int64]bool{}
			for _, id := range in.RecipientIDs {
				r, ok := byID[id]
				if !ok {
					return recipientNotFound(id)
				}
				if !seen[id] {
					seen[id] = true
					chosen = append(chosen, r)
				}
			}
			recipients = chosen
		}
		var queue []auditpackdb.ListActiveRecipientsRow
		for _, r := range recipients {
			if len(okSections(statuses, r.Sections)) == 0 {
				out.Skipped = append(out.Skipped, SkippedRecipient{Email: r.Email, Reason: "NO_SECTIONS"})
				continue
			}
			waiting, err := q.PackEmailQueued(ctx, auditpackdb.PackEmailQueuedParams{PropertyID: propertyID, PackID: &st.id, ToAddress: r.Email})
			if err != nil {
				return err
			}
			if waiting {
				return apperr.Conflict("PACK_ALREADY_QUEUED", "this recipient already has the pack waiting to be sent").WithContext("email", r.Email)
			}
			queue = append(queue, r)
		}
		if out.Queued, err = s.queueFor(ctx, p, propertyID, st.id, statuses, queue); err != nil {
			return err
		}
		if out.Queued == nil {
			out.Queued = []QueuedMail{}
		}
		addresses := make([]string, len(out.Queued))
		for i, m := range out.Queued {
			addresses[i] = m.Email
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &date, UserID: p.ActorID(), Action: "nap_pack.resent", EntityType: "nap_pack",
			EntityID: st.id, EntityLabel: date.String(), New: map[string]any{"business_date": date, "addresses": addresses, "queued": out.Queued, "sections_failed": out.SectionsFailed}})
	})
	return out, err
}
