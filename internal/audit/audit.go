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

	"kamarapms/internal/audit/auditdb"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
)

// Entry is one audited change.
type Entry struct {
	TenantID     int64
	PropertyID   *int64
	BusinessDate *civil.Date
	UserID       *int64
	Action       string // e.g. property.created, business_day.closed
	EntityType   string // e.g. property
	EntityID     int64
	Old          any // state before (nil for creations)
	New          any // state after (nil for deletions)
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
	return auditdb.New(tx).InsertAuditLog(ctx, p)
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
