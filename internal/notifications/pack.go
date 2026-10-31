package notifications

import (
	"context"
	"fmt"
	"html"
	"slices"
	"strings"

	"kamarapms/internal/auditpack"
	"kamarapms/internal/documents"
	"kamarapms/internal/notifications/notificationsdb"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reports"
)

// KindNightAuditPack is the e-mail of a night audit pack (docs/architecture/38-reports-night-audit-pack.md 6): the PDF of the sections a recipient chose, in English.
const KindNightAuditPack = "NIGHT_AUDIT_PACK"

// DefaultMaxBytes is the largest message sent when PMS_SMTP_MAX_BYTES is not set: many mail servers refuse more than 10 MB, and base64 makes a file a third larger.
const DefaultMaxBytes = 7 << 20

// packHeadroom is what the body, the headers and the line breaks of the encoding take besides the PDF.
const packHeadroom = 20_000

// PackSource is the stored pack the worker draws from (auditpack.Service). The PDF is made again from the stored JSON of the sections at the moment of sending; it is not kept (Q6).
type PackSource interface {
	MailView(ctx context.Context, propertyID, packID int64, email string, wantFigures bool) (auditpack.MailView, error)
	PDF(ctx context.Context, propertyID int64, date civil.Date, codes []string) (string, []byte, error)
}

// SetPacks gives the service the packs it sends (the pack is built after the notifier).
func (s *Service) SetPacks(p PackSource) { s.packs = p }

// SetMaxBytes sets the largest message; 0 or less means the default.
func (s *Service) SetMaxBytes(n int) { s.maxBytes = n }

func (s *Service) messageLimit() int {
	if s.maxBytes > 0 {
		return s.maxBytes
	}
	return DefaultMaxBytes
}

// encodedSize is the size of an attachment of n bytes in a message: base64 with a line break every 76 characters.
func encodedSize(n int) int { return (n+2)/3*4 + n/57*2 + packHeadroom }

type packGroup struct {
	sections []string
	name     string
	pdf      []byte
}

// packGroups draws the sections in as few messages as the limit allows: all of them in one PDF, or, when it is too large, halves until each fits. A section that is too large alone cannot be split.
func (s *Service) packGroups(ctx context.Context, propertyID int64, date civil.Date, sections []string, limit int) ([]packGroup, error) {
	name, pdf, err := s.packs.PDF(ctx, propertyID, date, sections)
	if err != nil {
		return nil, err
	}
	if encodedSize(len(pdf)) <= limit {
		return []packGroup{{sections: sections, name: name, pdf: pdf}}, nil
	}
	if len(sections) == 1 {
		return nil, fmt.Errorf("PACK_TOO_LARGE: the section %s is %d bytes as a PDF and a message holds %d: %w", sections[0], len(pdf), limit, ErrPermanent)
	}
	mid := len(sections) / 2
	left, err := s.packGroups(ctx, propertyID, date, sections[:mid], limit)
	if err != nil {
		return nil, err
	}
	right, err := s.packGroups(ctx, propertyID, date, sections[mid:], limit)
	if err != nil {
		return nil, err
	}
	return append(left, right...), nil
}

// composePack builds the message of a pack row. A recipient who was removed or deactivated since the row was queued gets nothing (SKIPPED). A pack that does not fit in a message is split: this row
// becomes part 1 of N and the other parts are queued as rows of their own, each with its own outcome and its own entry in the history.
func (s *Service) composePack(ctx context.Context, row notificationsdb.EmailOutbox) (Message, string, error) {
	if s.packs == nil || row.NapPackID == nil || len(row.NapSections) == 0 {
		return Message{}, "the night audit pack is not available", nil
	}
	wantFigures := slices.Contains(row.NapSections, auditpack.SecSummary) || slices.Contains(row.NapSections, auditpack.SecFlash)
	part, parts := int(row.NapPart.Int16), int(row.NapParts.Int16)
	view, err := s.packs.MailView(ctx, row.PropertyID, *row.NapPackID, row.ToAddress, wantFigures && part == 1)
	if err != nil {
		return Message{}, "", err
	}
	if !view.RecipientActive {
		return Message{}, "the address is no longer an active recipient of the night audit pack", nil
	}
	var sections []string
	for _, c := range row.NapSections { // the sections as queued, the ones that still hold data
		if slices.Contains(view.OK, c) {
			sections = append(sections, c)
		}
	}
	if len(sections) == 0 {
		return Message{}, "no section of the message holds data any more", nil
	}
	var groups []packGroup
	if parts == 1 {
		if groups, err = s.packGroups(ctx, row.PropertyID, view.Date, sections, s.messageLimit()); err != nil {
			return Message{}, "", err
		}
	} else { // a part of a split message is one PDF; it fits, or it fails
		name, pdf, err := s.packs.PDF(ctx, row.PropertyID, view.Date, sections)
		if err != nil {
			return Message{}, "", err
		}
		if encodedSize(len(pdf)) > s.messageLimit() {
			return Message{}, "", fmt.Errorf("PACK_TOO_LARGE: the PDF is %d bytes and a message holds %d: %w", len(pdf), s.messageLimit(), ErrPermanent)
		}
		groups = []packGroup{{sections: sections, name: name, pdf: pdf}}
	}
	g := groups[0]
	if len(groups) > 1 {
		if err := s.splitPack(ctx, row, groups); err != nil {
			return Message{}, "", err
		}
		parts = len(groups)
	}
	prop, err := s.days.GetProperty(ctx, row.PropertyID)
	if err != nil {
		return Message{}, "", err
	}
	return packMessage(row.ToAddress, prop.Name, prop.CurrencyCode, view, g, part, parts), "", nil
}

// splitPack turns the row into part 1 of N and queues the other parts, in one transaction.
func (s *Service) splitPack(ctx context.Context, row notificationsdb.EmailOutbox, groups []packGroup) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		parts := int16(len(groups)) //nolint:gosec // G115: at most fifteen sections, so at most fifteen parts
		if err := q.SplitPackPart(ctx, notificationsdb.SplitPackPartParams{ID: row.ID, Sections: groups[0].sections, Parts: parts}); err != nil {
			return err
		}
		now := s.clock.Now()
		for i, g := range groups[1:] {
			err := q.InsertPackPart(ctx, notificationsdb.InsertPackPartParams{TenantID: row.TenantID, PropertyID: row.PropertyID, ToAddress: row.ToAddress, PackID: row.NapPackID,
				Sections: g.sections, Part: int16(i + 2), Parts: parts, Now: now, ActorID: row.CreatedBy}) //nolint:gosec // G115: at most fifteen parts
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// packFooter closes every message of a pack: why the reader gets it and whom to ask.
const packFooter = "You receive this because your address is a night audit recipient of this hotel. Ask the hotel to change or stop it."

// packFigures are the lines of "The day in figures", written the English way with the currency of the property: money as IDR 780,000, a percentage as 3.00%, a count without decimals.
func packFigures(f *reports.DailyFlash, currency string) []string {
	money := func(v string) string { return currency + " " + groupThousands(v) }
	count := func(key string, lines []reports.FlashLine) string {
		for _, l := range lines {
			if l.Key == key {
				return groupThousands(l.Today)
			}
		}
		return "0"
	}
	var out []string
	for _, l := range f.Rooms {
		switch l.Key {
		case "rooms_sold":
			out = append(out, l.Label+": "+groupThousands(l.Today))
		case "occupancy_pct":
			out = append(out, "Occupancy: "+groupThousands(l.Today)+"%")
		case "arr":
			out = append(out, l.Label+": "+money(l.Today))
		}
	}
	out = append(out,
		f.Revenue.TotalNet.Label+": "+money(f.Revenue.TotalNet.Today),
		f.Revenue.GTR.Label+": "+money(f.Revenue.GTR.Today),
		f.Payments.GTP.Label+": "+money(f.Payments.GTP.Today),
		"Arrivals · departures · no-shows: "+count("arrivals", f.Activity)+" · "+count("departures", f.Activity)+" · "+count("no_shows", f.Activity))
	return out
}

// groupThousands puts a comma between the thousands of a plain decimal text such as -1234567.50.
func groupThousands(v string) string {
	sign := ""
	if strings.HasPrefix(v, "-") {
		sign, v = "-", v[1:]
	}
	whole, frac, hasFrac := strings.Cut(v, ".")
	var b strings.Builder
	for i, c := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if hasFrac {
		return sign + b.String() + "." + frac
	}
	return sign + b.String()
}

// packMessage is the e-mail: the title and the sections in English, the figures of the day when the recipient asked for the summary or the flash, and the PDF. It carries no link: the archive is for
// people who sign in (Q13).
func packMessage(to, property, currency string, view auditpack.MailView, g packGroup, part, parts int) Message {
	date := view.Date.String()
	subject := "Night audit pack " + date + " - " + property
	name := strings.TrimSuffix(g.name, ".pdf")
	if parts > 1 {
		subject += fmt.Sprintf(" (part %d of %d)", part, parts)
		name += fmt.Sprintf("-part-%d-of-%d", part, parts)
	}
	titles := make([]string, len(g.sections))
	for i, c := range g.sections {
		titles[i] = documents.PackSectionTitle(c)
	}
	var t, h strings.Builder
	t.WriteString("Night audit pack of " + date + " for " + property + ".\n")
	h.WriteString("<p>Night audit pack of <strong>" + html.EscapeString(date) + "</strong> for " + html.EscapeString(property) + ".</p>")
	if parts > 1 {
		note := fmt.Sprintf("The pack is sent in %d messages because of its size. This is part %d.", parts, part)
		t.WriteString(note + "\n")
		h.WriteString("<p>" + html.EscapeString(note) + "</p>")
	}
	if view.Flash != nil && part == 1 {
		t.WriteString("\nThe day in figures:\n")
		h.WriteString("<p>The day in figures:</p><ul>")
		for _, l := range packFigures(view.Flash, currency) {
			t.WriteString("  - " + l + "\n")
			h.WriteString("<li>" + html.EscapeString(l) + "</li>")
		}
		h.WriteString("</ul>")
	}
	t.WriteString("\nThe attached PDF holds: " + strings.Join(titles, ", ") + ".\n\nThis message was sent by the property management system.\n\n" + packFooter + "\n")
	h.WriteString("<p>The attached PDF holds: " + html.EscapeString(strings.Join(titles, ", ")) + ".</p><p>This message was sent by the property management system.</p><p>" + html.EscapeString(packFooter) + "</p>")
	return Message{To: to, FromName: property, Subject: subject, Text: t.String(), HTML: h.String(), Attachments: []Attachment{{Filename: name + ".pdf", ContentType: "application/pdf", Data: g.pdf}}}
}
