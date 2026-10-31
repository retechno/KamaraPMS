package auditpack

import (
	"context"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditpack/auditpackdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// The recipients of the pack (docs/architecture/38-reports-night-audit-pack.md 6 and 8.1): an address of the hotel's choosing, which sections it receives, and whether it is active. An address that is
// not a user of the system is a recipient only through this list, which is audited (Q13, Q14).

// DefaultSections is what a new recipient gets (Q2): the summary and the flash. The sections with guest names or cash are opt-in.
var DefaultSections = []string{SecSummary, SecFlash}

// Mailer says whether e-mail is configured on the server (notifications.Service). Without it, or while it says no, the run queues nothing and the archive says "E-mail is off" (Q9).
type Mailer interface {
	Enabled() bool
}

// SetMailer wires the e-mail: the run queues the pack for the recipients when it is on.
func (s *Service) SetMailer(m Mailer) { s.mailer = m }

func (s *Service) mailEnabled() bool { return s.mailer != nil && s.mailer.Enabled() }

// Recipient is a recipient of the pack as the screen shows it.
type Recipient struct {
	ID        int64     `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Sections  []string  `json:"sections"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RecipientList is the list of a property. It is not paged: a property has tens of addresses at most.
type RecipientList struct {
	// EmailEnabled is false when the server has no mail host: the recipients can be kept, nothing is sent.
	EmailEnabled bool        `json:"email_enabled"`
	Items        []Recipient `json:"items"`
}

func recipientOf(id int64, email string, name *string, sections []string, active bool, created, updated time.Time) Recipient {
	r := Recipient{ID: id, Email: email, Sections: sections, IsActive: active, CreatedAt: created, UpdatedAt: updated}
	if name != nil {
		r.Name = *name
	}
	if r.Sections == nil {
		r.Sections = []string{}
	}
	return r
}

// RecipientInput is the body of a new recipient. No sections means the default (Q2).
type RecipientInput struct {
	Email    string   `json:"email"`
	Name     string   `json:"name"`
	Sections []string `json:"sections"`
	IsActive *bool    `json:"is_active"`
}

// RecipientPatch changes a recipient; a field that is absent stays as it is.
type RecipientPatch struct {
	Email    *string   `json:"email"`
	Name     *string   `json:"name"`
	Sections *[]string `json:"sections"`
	IsActive *bool     `json:"is_active"`
}

func recipientNotFound(id int64) error {
	return apperr.NotFound("PACK_RECIPIENT_NOT_FOUND", "there is no such recipient of the night audit pack in this property").WithContext("recipient_id", id)
}

// validAddress is one address as the mail server takes it: no list, no display name, no line break.
func validAddress(a string) bool {
	if a == "" || len(a) > 254 || strings.ContainsAny(a, "\r\n") {
		return false
	}
	p, err := mail.ParseAddress(a)
	return err == nil && p.Address == a
}

// cleanSections checks the sections of a recipient against the catalog and gives them in the order of the catalog, once each.
func cleanSections(in []string) ([]string, error) {
	want := map[string]bool{}
	for _, c := range in {
		if !Known(c) {
			return nil, invalidSection(c)
		}
		want[c] = true
	}
	out := make([]string, 0, len(want))
	for _, def := range Catalog {
		if want[def.Code] {
			out = append(out, def.Code)
		}
	}
	if len(out) == 0 {
		return nil, apperr.Invalid("the request is invalid", apperr.FieldError{Field: "sections", Code: "REQUIRED", Message: "choose at least one section"})
	}
	return out, nil
}

func checkRecipient(email, name string) (string, string, error) {
	email, name = strings.TrimSpace(email), strings.TrimSpace(name)
	var fields []apperr.FieldError
	if !validAddress(email) {
		fields = append(fields, apperr.FieldError{Field: "email", Code: "INVALID_FORMAT", Message: "one e-mail address, such as name@example.com"})
	}
	if len([]rune(name)) > 100 || strings.ContainsAny(name, "\r\n") {
		fields = append(fields, apperr.FieldError{Field: "name", Code: "INVALID_VALUE", Message: "at most 100 characters on one line"})
	}
	if len(fields) > 0 {
		return "", "", apperr.Invalid("the request is invalid", fields...)
	}
	return email, name, nil
}

func optName(name string) *string {
	if name == "" {
		return nil
	}
	return &name
}

// ListRecipients answers the recipients of the property. The list is shown to a user who manages them and to one who only reads the archive.
func (s *Service) ListRecipients(ctx context.Context, propertyID int64) (RecipientList, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RecipientList{}, err
	}
	if err := auth.RequireAny(ctx, s.authz, propertyID, auth.PermNightAuditPackManage, auth.PermNightAuditPackView); err != nil {
		return RecipientList{}, err
	}
	rows, err := s.q(ctx).ListRecipients(ctx, auditpackdb.ListRecipientsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return RecipientList{}, err
	}
	out := RecipientList{EmailEnabled: s.mailEnabled(), Items: make([]Recipient, len(rows))}
	for i, r := range rows {
		out.Items[i] = recipientOf(r.ID, r.Email, r.Name, r.Sections, r.IsActive, r.CreatedAt, r.UpdatedAt)
	}
	return out, nil
}

// CreateRecipient adds a recipient (nightaudit.pack_manage). The address is in the audit entry because the row can be deleted later.
func (s *Service) CreateRecipient(ctx context.Context, propertyID int64, in RecipientInput) (Recipient, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackManage)
	if err != nil {
		return Recipient{}, err
	}
	email, name, err := checkRecipient(in.Email, in.Name)
	if err != nil {
		return Recipient{}, err
	}
	sections := in.Sections
	if len(sections) == 0 {
		sections = DefaultSections
	}
	if sections, err = cleanSections(sections); err != nil {
		return Recipient{}, err
	}
	active := in.IsActive == nil || *in.IsActive
	var out Recipient
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		r, err := s.q(ctx).InsertRecipient(ctx, auditpackdb.InsertRecipientParams{TenantID: p.TenantID, PropertyID: propertyID, Email: email, Name: optName(name), Sections: sections,
			IsActive: active, Now: s.clock.Now(), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = recipientOf(r.ID, r.Email, r.Name, r.Sections, r.IsActive, r.CreatedAt, r.UpdatedAt)
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "nap_recipient.created", EntityType: "nap_recipient", EntityID: r.ID,
			EntityLabel: email, New: map[string]any{"email": email, "sections": sections, "is_active": active}})
	})
	return out, err
}

// UpdateRecipient changes a recipient (nightaudit.pack_manage); the audit entry holds the old and the new sections and state.
func (s *Service) UpdateRecipient(ctx context.Context, propertyID, id int64, patch RecipientPatch) (Recipient, error) {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackManage)
	if err != nil {
		return Recipient{}, err
	}
	var out Recipient
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetRecipient(ctx, auditpackdb.GetRecipientParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return recipientNotFound(id)
		}
		if err != nil {
			return err
		}
		email, name, sections, active := cur.Email, "", cur.Sections, cur.IsActive
		if cur.Name != nil {
			name = *cur.Name
		}
		if patch.Email != nil {
			email = *patch.Email
		}
		if patch.Name != nil {
			name = *patch.Name
		}
		if patch.Sections != nil {
			sections = *patch.Sections
		}
		if patch.IsActive != nil {
			active = *patch.IsActive
		}
		if email, name, err = checkRecipient(email, name); err != nil {
			return err
		}
		if sections, err = cleanSections(sections); err != nil {
			return err
		}
		r, err := q.UpdateRecipient(ctx, auditpackdb.UpdateRecipientParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Email: email, Name: optName(name), Sections: sections,
			IsActive: active, Now: s.clock.Now(), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = recipientOf(r.ID, r.Email, r.Name, r.Sections, r.IsActive, r.CreatedAt, r.UpdatedAt)
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "nap_recipient.updated", EntityType: "nap_recipient", EntityID: id,
			EntityLabel: email, Old: map[string]any{"email": cur.Email, "sections": cur.Sections, "is_active": cur.IsActive}, New: map[string]any{"email": email, "sections": sections, "is_active": active}})
	})
	return out, err
}

// DeleteRecipient removes a recipient (nightaudit.pack_manage). The history of the messages already sent keeps the address and the sections as sent.
func (s *Service) DeleteRecipient(ctx context.Context, propertyID, id int64) error {
	p, err := s.access(ctx, propertyID, auth.PermNightAuditPackManage)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		cur, err := q.GetRecipient(ctx, auditpackdb.GetRecipientParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return recipientNotFound(id)
		}
		if err != nil {
			return err
		}
		if _, err := q.DeleteRecipient(ctx, auditpackdb.DeleteRecipientParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "nap_recipient.deleted", EntityType: "nap_recipient", EntityID: id,
			EntityLabel: cur.Email, Old: map[string]any{"email": cur.Email, "sections": cur.Sections, "is_active": cur.IsActive}})
	})
}

// queueFor queues one message for each of the recipients (6.1): the sections of the recipient that exist in the pack and are OK, in catalog order. A recipient with none gets nothing. It returns the
// addresses that were queued. statuses maps a section code to its status in the pack.
func (s *Service) queueFor(ctx context.Context, p auth.Principal, propertyID, packID int64, statuses map[string]string, recipients []auditpackdb.ListActiveRecipientsRow) ([]QueuedMail, error) {
	if len(recipients) == 0 {
		return nil, nil
	}
	now := s.clock.Now()
	q := s.q(ctx)
	var out []QueuedMail
	for _, r := range recipients {
		sections := okSections(statuses, r.Sections)
		if len(sections) == 0 {
			continue
		}
		if _, err := q.InsertPackEmail(ctx, auditpackdb.InsertPackEmailParams{TenantID: p.TenantID, PropertyID: propertyID, ToAddress: r.Email, PackID: &packID, Sections: sections, Now: now, ActorID: p.ActorID()}); err != nil {
			return nil, err
		}
		out = append(out, QueuedMail{Email: r.Email, Sections: sections})
	}
	return out, nil
}

// QueuedMail is a message the run or a resend put in the outbox.
type QueuedMail struct {
	Email    string   `json:"email"`
	Sections []string `json:"sections"`
}

// okSections keeps, from what a recipient wants, the sections that are OK in the pack, in the order of the catalog.
func okSections(statuses map[string]string, want []string) []string {
	have := map[string]bool{}
	for _, c := range want {
		have[c] = true
	}
	var out []string
	for _, def := range Catalog {
		if have[def.Code] && statuses[def.Code] == StatusOK {
			out = append(out, def.Code)
		}
	}
	return out
}
