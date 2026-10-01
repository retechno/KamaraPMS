package housekeeping

import (
	"context"
	"strings"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/housekeeping/housekeepingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Flags are what housekeeping should know about a room beyond its status.
type Flags struct {
	RoomID          int64     `json:"room_id"`
	Priority        string    `json:"priority"`
	DND             bool      `json:"dnd"`
	MakeUpRequested bool      `json:"make_up_requested"`
	Note            string    `json:"note,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// FlagsInput replaces a room's flags.
type FlagsInput struct {
	Priority        string `json:"priority"`
	DND             bool   `json:"dnd"`
	MakeUpRequested bool   `json:"make_up_requested"`
	Note            string `json:"note"`
}

// SetFlags replaces a room's flags (housekeeping.update): a priority (HIGH rooms come first on the list), do not
// disturb, a make-up request from the guest, and a free note. They do not change the cleaning status.
func (s *Service) SetFlags(ctx context.Context, propertyID, roomID int64, in FlagsInput) (Flags, error) {
	p, err := s.updater(ctx, propertyID)
	if err != nil {
		return Flags{}, err
	}
	in.Priority = strings.ToUpper(strings.TrimSpace(in.Priority))
	in.Note = strings.TrimSpace(in.Note)
	if in.Priority == "" {
		in.Priority = PriorityNormal
	}
	var fields []apperr.FieldError
	if in.Priority != PriorityNormal && in.Priority != PriorityHigh {
		fields = append(fields, apperr.FieldError{Field: "priority", Code: "INVALID_VALUE", Message: "NORMAL or HIGH"})
	}
	if len([]rune(in.Note)) > maxNotes {
		fields = append(fields, apperr.FieldError{Field: "note", Code: "TOO_LONG", Message: "at most 500 characters"})
	}
	if len(fields) > 0 {
		return Flags{}, apperr.Invalid("the flags are invalid", fields...)
	}
	var out Flags
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		exists, err := q.RoomExists(ctx, housekeepingdb.RoomExistsParams{TenantID: p.TenantID, PropertyID: propertyID, RoomID: roomID})
		if err != nil {
			return err
		}
		if !exists {
			return roomNotFound()
		}
		row, err := q.UpsertRoomFlags(ctx, housekeepingdb.UpsertRoomFlagsParams{
			TenantID: p.TenantID, PropertyID: propertyID, RoomID: roomID, Priority: in.Priority, Dnd: in.DND, MakeUpRequested: in.MakeUpRequested,
			Note: nullableText(in.Note), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = Flags{RoomID: roomID, Priority: row.Priority, DND: row.Dnd, MakeUpRequested: row.MakeUpRequested, Note: deref(row.Note), UpdatedAt: row.UpdatedAt}
		bd := day.BusinessDate
		return s.audit.Write(ctx, flagAudit(p, propertyID, bd, roomID, out))
	})
	return out, err
}

func flagAudit(p auth.Principal, propertyID int64, bd civil.Date, roomID int64, f Flags) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: "housekeeping.flags_changed", EntityType: "room", EntityID: roomID,
		New: map[string]any{"priority": f.Priority, "dnd": f.DND, "make_up_requested": f.MakeUpRequested, "note": f.Note},
	}
}
