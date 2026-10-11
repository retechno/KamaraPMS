// Package audit writes the append-only audit trail.
//
// Entries are written inside the business transaction (so a change and its
// audit record commit or roll back together) and are stamped with both clocks:
// server time (created_at) and the property business date.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"kamarapms/internal/audit/auditdb"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
)

// MaxLabelLength is the length of the column entity_label, in characters.
const MaxLabelLength = 120

// Entry is one audited change.
type Entry struct {
	TenantID     int64
	PropertyID   *int64
	BusinessDate *civil.Date
	UserID       *int64
	Action       string // e.g. property.created, business_day.closed
	EntityType   string // e.g. property
	EntityID     int64
	// EntityLabel is what the entry is about as a person reads it: the identifier alone ("305", "RES000012", "FOL000026", a code, a name), never the kind of thing (that is
	// EntityType, said in the language of the screen). Empty when the thing has no readable number (the column is then NULL). It must not hold the name of a guest: an
	// entry cannot be erased, so a guest is named by the code. A label longer than MaxLabelLength is cut at a character.
	EntityLabel string
	Old         any // state before (nil for creations)
	New         any // state after (nil for deletions)
}

// Writer persists audit entries.
type Writer struct {
	clock clock.Clock
}

// NewWriter returns a Writer.
func NewWriter(c clock.Clock) *Writer { return &Writer{clock: c} }

// Write records e in the ambient transaction (db.ErrNoTx otherwise).
func (w *Writer) Write(ctx context.Context, e Entry) error {
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	oldData, err := marshal(e.Old)
	if err != nil {
		return err
	}
	newData, err := marshal(e.New)
	if err != nil {
		return err
	}
	p := auditdb.InsertAuditLogParams{
		TenantID:     e.TenantID,
		PropertyID:   e.PropertyID,
		BusinessDate: e.BusinessDate,
		UserID:       e.UserID,
		Action:       e.Action,
		EntityType:   e.EntityType,
		EntityID:     e.EntityID,
		OldData:      oldData,
		NewData:      newData,
		CreatedAt:    w.clock.Now(),
	}
	if id := httpx.RequestIDFrom(ctx); id != "" {
		p.RequestID = &id
	}
	if ip, ok := httpx.ClientIPFrom(ctx); ok {
		p.IpAddress = &ip
	}
	p.EntityLabel = clipLabel(e.EntityLabel)
	return auditdb.New(tx).InsertAuditLog(ctx, p)
}

// clipLabel is the label as it is stored: trimmed, at most MaxLabelLength characters (cut between two characters, never inside one), and nil when nothing is left.
func clipLabel(label string) *string {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil
	}
	if utf8.RuneCountInString(label) > MaxLabelLength {
		runes := []rune(label)
		label = strings.TrimSpace(string(runes[:MaxLabelLength]))
	}
	return &label
}

func marshal(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("audit: marshal: %w", err)
	}
	return b, nil
}
