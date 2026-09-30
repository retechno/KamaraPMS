package housekeeping

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/housekeeping/housekeepingdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

const (
	maxNotes      = 500
	maxBoardRooms = 2000
)

// Service is the housekeeping application service.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
}

// NewService wires the housekeeping service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days}
}

func (s *Service) q(ctx context.Context) *housekeepingdb.Queries {
	return housekeepingdb.New(s.txm.DB(ctx))
}

func roomNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
}

// InitRoom creates the housekeeping row of a new room. It runs inside the
// caller's transaction (room creation) and writes no log: there is no previous status.
func (s *Service) InitRoom(ctx context.Context, tenantID, propertyID, roomID int64, status Status, actorID *int64) error {
	if !status.Valid() {
		return apperr.Invalid("the housekeeping status is invalid",
			apperr.FieldError{Field: "initial_housekeeping_status", Code: "INVALID_VALUE", Message: "CLEAN, DIRTY, CLEANING or INSPECTED"})
	}
	tx, err := db.Tx(ctx)
	if err != nil {
		return err
	}
	return housekeepingdb.New(tx).CreateRoomHousekeeping(ctx, housekeepingdb.CreateRoomHousekeepingParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: roomID, Status: string(status), ActorID: actorID,
	})
}

// SetStatus is the manual status change (POST {P}/rooms/{id}/housekeeping).
// INSPECTED additionally needs housekeeping.inspect.
func (s *Service) SetStatus(ctx context.Context, propertyID, roomID int64, to Status, notes string) (State, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return State{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermHousekeepingUpdate); err != nil {
		return State{}, err
	}
	if !to.Valid() {
		return State{}, apperr.Invalid("the housekeeping status is invalid",
			apperr.FieldError{Field: "status", Code: "INVALID_VALUE", Message: "CLEAN, DIRTY, CLEANING or INSPECTED"})
	}
	if len(notes) > maxNotes {
		return State{}, apperr.Invalid("the notes are too long", apperr.FieldError{Field: "notes", Code: "TOO_LONG", Message: "at most 500 characters"})
	}
	if to == Inspected {
		if err := s.authz.Require(ctx, propertyID, auth.PermHousekeepingInspect); err != nil {
			return State{}, err
		}
	}

	var out State
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		from, err := s.lockStatus(ctx, p.TenantID, propertyID, roomID)
		if err != nil {
			return err
		}
		if !CanTransition(from, to) {
			return apperr.Conflict("INVALID_HK_TRANSITION", "the housekeeping status cannot change from "+string(from)+" to "+string(to)).
				WithContext("from", string(from)).WithContext("to", string(to)).WithContext("allowed", NextStatuses(from))
		}
		out, err = s.apply(ctx, change{
			TenantID: p.TenantID, PropertyID: propertyID, RoomID: roomID, From: from, To: to,
			Source: SourceManual, Notes: notes, BusinessDate: day.BusinessDate, ActorID: p.ActorID(),
		})
		return err
	})
	return out, err
}

// MarkDirty sets a room DIRTY on behalf of another use case (check-out, room move,
// reverse check-in, night audit). It joins the caller's transaction, needs no
// permission of its own (the use case was authorized) and is a no-op if the room
// is already DIRTY. any→DIRTY is always a legal transition.
func (s *Service) MarkDirty(ctx context.Context, tenantID, propertyID, roomID int64, source Source, notes string, actorID *int64) error {
	if source == SourceManual {
		return errors.New("housekeeping: MarkDirty is for system sources; use SetStatus for manual changes")
	}
	if _, err := db.Tx(ctx); err != nil {
		return err
	}
	day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
	if err != nil {
		return err
	}
	from, err := s.lockStatus(ctx, tenantID, propertyID, roomID)
	if err != nil {
		return err
	}
	if from == Dirty {
		return nil
	}
	_, err = s.apply(ctx, change{
		TenantID: tenantID, PropertyID: propertyID, RoomID: roomID, From: from, To: Dirty,
		Source: source, Notes: notes, BusinessDate: day.BusinessDate, ActorID: actorID,
	})
	return err
}

// lockStatus locks the room's housekeeping row (lock level L3, taken after the business day).
func (s *Service) lockStatus(ctx context.Context, tenantID, propertyID, roomID int64) (Status, error) {
	if err := db.EnterLockLevel(ctx, db.LevelRooms); err != nil {
		return "", err
	}
	row, err := s.q(ctx).GetRoomHousekeepingForUpdate(ctx, housekeepingdb.GetRoomHousekeepingForUpdateParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: roomID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", roomNotFound()
	}
	if err != nil {
		return "", err
	}
	return Status(row.Status), nil
}

type change struct {
	TenantID, PropertyID, RoomID int64
	From, To                     Status
	Source                       Source
	Notes                        string
	BusinessDate                 civil.Date
	ActorID                      *int64
}

// apply updates the current row, appends the log and audits, in the ambient transaction.
func (s *Service) apply(ctx context.Context, c change) (State, error) {
	q := s.q(ctx)
	row, err := q.SetRoomHousekeeping(ctx, housekeepingdb.SetRoomHousekeepingParams{
		TenantID: c.TenantID, PropertyID: c.PropertyID, RoomID: c.RoomID, Status: string(c.To), ActorID: c.ActorID,
	})
	if err != nil {
		return State{}, err
	}
	var notes *string
	if c.Notes != "" {
		notes = &c.Notes
	}
	log, err := q.InsertHousekeepingLog(ctx, housekeepingdb.InsertHousekeepingLogParams{
		TenantID: c.TenantID, PropertyID: c.PropertyID, RoomID: c.RoomID,
		FromStatus: string(c.From), ToStatus: string(c.To), Source: string(c.Source),
		BusinessDate: c.BusinessDate, Notes: notes, ChangedAt: s.clock.Now(), ActorID: c.ActorID,
	})
	if err != nil {
		return State{}, err
	}
	bd := c.BusinessDate
	err = s.audit.Write(ctx, audit.Entry{
		TenantID: c.TenantID, PropertyID: &c.PropertyID, BusinessDate: &bd, UserID: c.ActorID,
		Action: "housekeeping.changed", EntityType: "room", EntityID: c.RoomID,
		Old: map[string]any{"status": c.From}, New: toLog(log),
	})
	return State{RoomID: c.RoomID, Status: Status(row.Status), UpdatedAt: row.UpdatedAt}, err
}

// BoardFilter narrows the housekeeping board.
type BoardFilter struct {
	Status *Status
	Floor  *string
}

// Board lists the active rooms with housekeeping status and derived occupancy
// for the current business date.
func (s *Service) Board(ctx context.Context, propertyID int64, f BoardFilter) ([]BoardRoom, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	if f.Status != nil && !f.Status.Valid() {
		return nil, apperr.Invalid("the filter is invalid", apperr.FieldError{Field: "status", Code: "INVALID_VALUE"})
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	arg := housekeepingdb.ListHousekeepingBoardParams{
		BusinessDate: day.BusinessDate, TenantID: p.TenantID, PropertyID: propertyID, Floor: f.Floor, RowLimit: maxBoardRooms,
	}
	if f.Status != nil {
		st := string(*f.Status)
		arg.Status = &st
	}
	rows, err := s.q(ctx).ListHousekeepingBoard(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]BoardRoom, len(rows))
	for i, r := range rows {
		b := BoardRoom{
			RoomID: r.RoomID, RoomNumber: r.RoomNumber, Floor: deref(r.Floor), Building: deref(r.Building),
			RoomTypeID: r.RoomTypeID, RoomTypeCode: r.RoomTypeCode, RoomTypeName: r.RoomTypeName,
			Status: Status(r.HousekeepingStatus), StatusUpdatedAt: r.HousekeepingUpdatedAt,
			Occupancy: Occupancy(r.Occupancy), AllowedNextState: NextStatuses(Status(r.HousekeepingStatus)),
		}
		if r.BlockType != nil && r.BlockEndDate != nil {
			b.Block = &BoardBlock{Type: *r.BlockType, EndDate: *r.BlockEndDate}
		}
		out[i] = b
	}
	return out, nil
}

// Logs lists a room's status changes, newest first, before the given log id.
func (s *Service) Logs(ctx context.Context, propertyID, roomID int64, beforeID *int64, limit int) ([]Log, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	q := s.q(ctx)
	exists, err := q.RoomExists(ctx, housekeepingdb.RoomExistsParams{TenantID: p.TenantID, PropertyID: propertyID, RoomID: roomID})
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, roomNotFound()
	}
	rows, err := q.ListHousekeepingLogs(ctx, housekeepingdb.ListHousekeepingLogsParams{
		TenantID: p.TenantID, PropertyID: propertyID, RoomID: roomID, BeforeID: beforeID, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Log, len(rows))
	for i, r := range rows {
		out[i] = toLog(r)
	}
	return out, nil
}

func toLog(r housekeepingdb.HousekeepingLog) Log {
	return Log{
		ID: r.ID, RoomID: r.RoomID, FromStatus: Status(r.FromStatus), ToStatus: Status(r.ToStatus), Source: Source(r.Source),
		BusinessDate: r.BusinessDate, Notes: deref(r.Notes), ChangedAt: r.ChangedAt, ChangedBy: r.ChangedBy,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}
