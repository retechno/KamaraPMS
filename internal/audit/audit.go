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
	"kamarapms/internal/operator/operatordb"
	"kamarapms/internal/platform/auth"
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
	// OperatorID is set when an operator of the platform console made the change (the hotel then sees "KamaraPMS (operator)"). It is filled from the context when the caller leaves it empty and
	// the context holds an operator; a change that has a user never has an operator (audit_logs_actor_ck).
	OperatorID *int64
	Action     string // e.g. property.created, business_day.closed
	EntityType string // e.g. property
	EntityID   int64
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
		OperatorID:   e.operatorFrom(ctx),
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
	if err := auditdb.New(tx).InsertAuditLog(ctx, p); err != nil {
		return err
	}
	// A change made by an operator is on the operator's own trail too, with the tenant it concerns (doc 39 P12).
	if p.OperatorID != nil {
		tenant := e.TenantID
		return w.WriteOperator(ctx, OperatorEntry{OperatorID: p.OperatorID, TenantID: &tenant, Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID, EntityLabel: e.EntityLabel, Old: e.Old, New: e.New})
	}
	return nil
}

// OperatorEntry is one entry of the operator's own trail (operator_audit_logs).
type OperatorEntry struct {
	OperatorID  *int64 // nil: pms-admin or the system
	TenantID    *int64 // set when the action concerns a tenant
	Action      string
	EntityType  string
	EntityID    int64
	EntityLabel string
	Old, New    any
}

// WriteOperator records e in the ambient transaction (db.ErrNoTx otherwise).
func (w *Writer) WriteOperator(ctx context.Context, e OperatorEntry) error {
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
	p := operatordb.InsertOperatorAuditParams{
		OperatorID: e.OperatorID, Action: e.Action, EntityType: e.EntityType, EntityID: e.EntityID, EntityLabel: clipLabel(e.EntityLabel),
		TenantID: e.TenantID, OldData: oldData, NewData: newData, CreatedAt: w.clock.Now(),
	}
	if id := httpx.RequestIDFrom(ctx); id != "" {
		p.RequestID = &id
	}
	if ip, ok := httpx.ClientIPFrom(ctx); ok {
		p.IpAddress = &ip
	}
	return operatordb.New(tx).InsertOperatorAudit(ctx, p)
}

// operatorFrom is the operator of the entry: the one named, or the one that makes the call (nil for a user's own change, pms-admin and the system).
func (e Entry) operatorFrom(ctx context.Context) *int64 {
	if e.UserID != nil {
		return nil
	}
	if e.OperatorID != nil {
		return e.OperatorID
	}
	if op, ok := auth.OperatorFrom(ctx); ok {
		id := op.OperatorID
		return &id
	}
	return nil
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
