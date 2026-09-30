package rooms

import (
	"context"
	"strings"

	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rooms/roomsdb"
	"kamarapms/internal/tenancy"
)

// Service is the rooms application service (room types, rooms, blocks).
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	hk    *housekeeping.Service
	avail *availability.Service
}

// NewService wires the rooms service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, hk *housekeeping.Service, avail *availability.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, hk: hk, avail: avail}
}

func (s *Service) q(ctx context.Context) *roomsdb.Queries { return roomsdb.New(s.txm.DB(ctx)) }

// auditEntry fills the fields every rooms audit entry shares.
func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: entity, EntityID: id, Old: old, New: updated,
	}
}

// lockRows wraps db.LockRows and reports a missing row with a specific error code.
func lockRows(ctx context.Context, table db.LockTable, mode db.LockMode, propertyID int64, nf *apperr.Error, ids ...int64) error {
	err := db.LockRows(ctx, table, mode, propertyID, ids)
	if apperr.IsCode(err, "NOT_FOUND") {
		return nf
	}
	return err
}

// ---------------------------------------------------------------------------
// Room types

// ListRoomTypes lists room types by id after afterID, optionally only active or inactive ones.
func (s *Service) ListRoomTypes(ctx context.Context, propertyID, afterID int64, active *bool, limit int) ([]RoomType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListRoomTypes(ctx, roomsdb.ListRoomTypesParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Active: active, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]RoomType, len(rows))
	for i, r := range rows {
		out[i] = toRoomType(r)
	}
	return out, nil
}

// GetRoomType returns one room type.
func (s *Service) GetRoomType(ctx context.Context, propertyID, id int64) (RoomType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomType{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return RoomType{}, err
	}
	row, err := s.q(ctx).GetRoomType(ctx, roomsdb.GetRoomTypeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return RoomType{}, orNotFound(err, errRoomTypeNotFound())
	}
	return toRoomType(row), nil
}

// CreateRoomType adds a room type (room.manage).
func (s *Service) CreateRoomType(ctx context.Context, propertyID int64, in RoomTypeInput) (RoomType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomType{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomManage); err != nil {
		return RoomType{}, err
	}
	in.Normalize()
	if fields := in.Validate(true); len(fields) > 0 {
		return RoomType{}, apperr.Invalid("the room type is invalid", fields...)
	}

	var out RoomType
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateRoomType(ctx, roomsdb.CreateRoomTypeParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, RoomTypeDescription: nullable(in.Description),
			MaxAdult: int16Of(in.MaxAdult), MaxChild: int16Of(in.MaxChild), MaxOccupancy: int16Of(in.MaxOccupancy),
			BaseOccupancy: int16Of(in.BaseOccupancy), SortOrder: in.SortOrder, IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toRoomType(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room_type.created", "room_type", out.ID, nil, out))
	})
	return out, err
}

// RoomTypePatch changes selected attributes; nil fields stay unchanged.
type RoomTypePatch struct {
	Name          *string
	Description   *string
	MaxAdult      *int32
	MaxChild      *int32
	MaxOccupancy  *int32
	BaseOccupancy *int32
	SortOrder     *int32
	IsActive      *bool
}

// UpdateRoomType edits a room type (room.manage). Deactivation is rejected while
// active rooms of the type exist or future CONFIRMED reservation lines hold it.
func (s *Service) UpdateRoomType(ctx context.Context, propertyID, id int64, patch RoomTypePatch) (RoomType, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomType{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomManage); err != nil {
		return RoomType{}, err
	}

	var out RoomType
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := lockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, errRoomTypeNotFound(), id); err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetRoomType(ctx, roomsdb.GetRoomTypeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errRoomTypeNotFound())
		}
		before := toRoomType(row)
		in := RoomTypeInput{
			Code: before.Code, Name: before.Name, Description: before.Description, MaxAdult: before.MaxAdult, MaxChild: before.MaxChild,
			MaxOccupancy: before.MaxOccupancy, BaseOccupancy: before.BaseOccupancy, SortOrder: before.SortOrder, IsActive: before.IsActive,
		}
		apply(&in.Name, patch.Name)
		apply(&in.Description, patch.Description)
		apply(&in.MaxAdult, patch.MaxAdult)
		apply(&in.MaxChild, patch.MaxChild)
		apply(&in.MaxOccupancy, patch.MaxOccupancy)
		apply(&in.BaseOccupancy, patch.BaseOccupancy)
		apply(&in.SortOrder, patch.SortOrder)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false); len(fields) > 0 {
			return apperr.Invalid("the room type is invalid", fields...)
		}

		if before.IsActive && !in.IsActive {
			if err := s.checkTypeDeactivation(ctx, p.TenantID, propertyID, id, day.BusinessDate); err != nil {
				return err
			}
		}

		updated, err := q.UpdateRoomType(ctx, roomsdb.UpdateRoomTypeParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, RoomTypeDescription: nullable(in.Description),
			MaxAdult: int16Of(in.MaxAdult), MaxChild: int16Of(in.MaxChild), MaxOccupancy: int16Of(in.MaxOccupancy),
			BaseOccupancy: int16Of(in.BaseOccupancy), SortOrder: in.SortOrder, IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toRoomType(updated)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room_type.updated", "room_type", id, before, out))
	})
	return out, err
}

func (s *Service) checkTypeDeactivation(ctx context.Context, tenantID, propertyID, typeID int64, bd civil.Date) error {
	q := s.q(ctx)
	rooms, err := q.CountActiveRoomsOfType(ctx, roomsdb.CountActiveRoomsOfTypeParams{TenantID: tenantID, PropertyID: propertyID, RoomTypeID: typeID})
	if err != nil {
		return err
	}
	lines, err := q.CountFutureConfirmedLinesOfType(ctx, roomsdb.CountFutureConfirmedLinesOfTypeParams{
		TenantID: tenantID, PropertyID: propertyID, RoomTypeID: typeID, BusinessDate: bd,
	})
	if err != nil {
		return err
	}
	if rooms > 0 || lines > 0 {
		return apperr.Conflict("ROOM_TYPE_IN_USE", "the room type still has active rooms or future reservations").
			WithContext("active_rooms", rooms).WithContext("future_reservation_lines", lines)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Rooms

// CreateRoomInput is the request to add a room.
type CreateRoomInput struct {
	RoomInput
	InitialHousekeeping housekeeping.Status // default DIRTY
}

// RoomFilter narrows a room list.
type RoomFilter struct {
	RoomTypeID *int64
	Active     *bool
}

// ListRooms lists rooms by id after afterID.
func (s *Service) ListRooms(ctx context.Context, propertyID, afterID int64, f RoomFilter, limit int) ([]Room, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListRooms(ctx, roomsdb.ListRoomsParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, RoomTypeID: f.RoomTypeID, Active: f.Active, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Room, len(rows))
	for i, r := range rows {
		out[i] = toRoom(r)
	}
	return out, nil
}

// GetRoom returns one room.
func (s *Service) GetRoom(ctx context.Context, propertyID, id int64) (Room, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Room{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return Room{}, err
	}
	row, err := s.q(ctx).GetRoom(ctx, roomsdb.GetRoomParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Room{}, orNotFound(err, errRoomNotFound())
	}
	return toRoom(row), nil
}

// CreateRoom adds a room and its housekeeping row (room.manage).
func (s *Service) CreateRoom(ctx context.Context, propertyID int64, in CreateRoomInput) (Room, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Room{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomManage); err != nil {
		return Room{}, err
	}
	in.Normalize()
	fields := in.Validate()
	if in.InitialHousekeeping == "" {
		in.InitialHousekeeping = housekeeping.Dirty
	}
	if !in.InitialHousekeeping.Valid() {
		fields = append(fields, apperr.FieldError{Field: "initial_housekeeping_status", Code: "INVALID_VALUE", Message: "CLEAN, DIRTY, CLEANING or INSPECTED"})
	}
	if len(fields) > 0 {
		return Room{}, apperr.Invalid("the room is invalid", fields...)
	}

	var out Room
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		// Share-lock the type: it cannot be deactivated while rooms are being added to it.
		if err := lockRows(ctx, db.RoomTypes, db.ForShare, propertyID, errRoomTypeNotFound(), in.RoomTypeID); err != nil {
			return err
		}
		q := s.q(ctx)
		if in.IsActive {
			if err := s.requireActiveType(ctx, p.TenantID, propertyID, in.RoomTypeID); err != nil {
				return err
			}
		}
		row, err := q.CreateRoom(ctx, roomsdb.CreateRoomParams{
			TenantID: p.TenantID, PropertyID: propertyID, RoomTypeID: in.RoomTypeID, RoomNumber: in.RoomNumber,
			Floor: nullable(in.Floor), Building: nullable(in.Building), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toRoom(row)
		if err := s.hk.InitRoom(ctx, p.TenantID, propertyID, out.ID, in.InitialHousekeeping, p.ActorID()); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room.created", "room", out.ID, nil,
			map[string]any{"room": out, "housekeeping_status": in.InitialHousekeeping}))
	})
	return out, err
}

func (s *Service) requireActiveType(ctx context.Context, tenantID, propertyID, typeID int64) error {
	t, err := s.q(ctx).GetRoomType(ctx, roomsdb.GetRoomTypeParams{TenantID: tenantID, PropertyID: propertyID, ID: typeID})
	if err != nil {
		return orNotFound(err, errRoomTypeNotFound())
	}
	if !t.IsActive {
		return apperr.Invalid("the room type is inactive",
			apperr.FieldError{Field: "room_type_id", Code: "ROOM_TYPE_INACTIVE", Message: "rooms can only be active in an active room type"})
	}
	return nil
}

// RoomPatch changes selected attributes; nil fields stay unchanged.
type RoomPatch struct {
	RoomTypeID *int64
	RoomNumber *string
	Floor      *string
	Building   *string
	IsActive   *bool
}

// UpdateRoom edits a room (room.manage). Changing the type or deactivating a room
// is rejected while a stay occupies it or a CONFIRMED reservation line is
// assigned to it from the business date on. (The per-type oversell check joins
// with the inventory engine in M8.)
func (s *Service) UpdateRoom(ctx context.Context, propertyID, id int64, patch RoomPatch) (Room, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Room{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomManage); err != nil {
		return Room{}, err
	}

	var out Room
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		// Read once to learn which types to lock, then lock (L2 types, L3 room) and re-read.
		peek, err := q.GetRoom(ctx, roomsdb.GetRoomParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errRoomNotFound())
		}
		// A type change or deactivation moves inventory between types: lock them (L2).
		if patch.RoomTypeID != nil || (patch.IsActive != nil && !*patch.IsActive) {
			typeIDs := []int64{peek.RoomTypeID}
			if patch.RoomTypeID != nil {
				typeIDs = append(typeIDs, *patch.RoomTypeID)
			}
			if err := lockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, errRoomTypeNotFound(), typeIDs...); err != nil {
				return err
			}
		}
		if err := lockRows(ctx, db.Rooms, db.ForUpdate, propertyID, errRoomNotFound(), id); err != nil {
			return err
		}
		row, err := q.GetRoom(ctx, roomsdb.GetRoomParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errRoomNotFound())
		}
		if row.RoomTypeID != peek.RoomTypeID {
			return apperr.Busy("RESOURCE_BUSY", "the room was changed concurrently, please retry")
		}
		before := toRoom(row)
		in := RoomInput{RoomTypeID: before.RoomTypeID, RoomNumber: before.RoomNumber, Floor: before.Floor, Building: before.Building, IsActive: before.IsActive}
		apply(&in.RoomTypeID, patch.RoomTypeID)
		apply(&in.RoomNumber, patch.RoomNumber)
		apply(&in.Floor, patch.Floor)
		apply(&in.Building, patch.Building)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(); len(fields) > 0 {
			return apperr.Invalid("the room is invalid", fields...)
		}

		typeChanged := in.RoomTypeID != before.RoomTypeID
		deactivated := before.IsActive && !in.IsActive
		if typeChanged || deactivated {
			conflicts, err := s.roomConflicts(ctx, p.TenantID, propertyID, id, day.BusinessDate, day.BusinessDate, farFuture)
			if err != nil {
				return err
			}
			if len(conflicts) > 0 {
				return apperr.Conflict("ROOM_IN_USE", "the room is occupied or assigned to future reservations").WithContext("conflicts", conflicts)
			}
			if before.IsActive { // the room leaves the old type's stock: that type must not end up oversold
				short, err := s.avail.RemovalShortfalls(ctx, p.TenantID, propertyID, before.RoomTypeID, id, day.BusinessDate)
				if err != nil {
					return err
				}
				if err := oversold(short); err != nil {
					return err
				}
			}
		}
		if in.IsActive && (typeChanged || !before.IsActive) {
			if err := s.requireActiveType(ctx, p.TenantID, propertyID, in.RoomTypeID); err != nil {
				return err
			}
		}

		updated, err := q.UpdateRoom(ctx, roomsdb.UpdateRoomParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, RoomTypeID: in.RoomTypeID, RoomNumber: in.RoomNumber,
			Floor: nullable(in.Floor), Building: nullable(in.Building), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toRoom(updated)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room.updated", "room", id, before, out))
	})
	return out, err
}

// roomConflicts lists the stays and CONFIRMED reservation lines that hold the room
// during any night of [start, end). An open stay holds [business date, max(departure, BD+1)).
func (s *Service) roomConflicts(ctx context.Context, tenantID, propertyID, roomID int64, bd, start, end civil.Date) ([]Conflict, error) {
	q := s.q(ctx)
	segs, err := q.ListRoomOpenSegmentConflicts(ctx, roomsdb.ListRoomOpenSegmentConflictsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: roomID, BusinessDate: bd, EndDate: end, StartDate: start, NextDate: bd.AddDays(1),
	})
	if err != nil {
		return nil, err
	}
	lines, err := q.ListRoomConfirmedLineConflicts(ctx, roomsdb.ListRoomConfirmedLineConflictsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomID: &roomID, EndDate: end, StartDate: start,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Conflict, 0, len(segs)+len(lines))
	for _, sg := range segs {
		to := sg.DepartureDate
		if !to.After(bd) {
			to = bd.AddDays(1)
		}
		out = append(out, Conflict{Type: ConflictStay, ID: sg.StayID, Reference: sg.StayNumber, From: bd, To: to})
	}
	for _, l := range lines {
		out = append(out, Conflict{Type: ConflictReservation, ID: l.ReservationRoomID, From: l.ArrivalDate, To: l.DepartureDate})
	}
	return out, nil
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// ---------------------------------------------------------------------------
// Room blocks

// CreateBlockInput is the request to block a room.
type CreateBlockInput struct {
	RoomID    int64
	BlockType string
	StartDate civil.Date
	EndDate   civil.Date
	Reason    string
}

// BlockFilter narrows a block list. From/To select blocks overlapping [From, To).
type BlockFilter struct {
	RoomID *int64
	Status *string
	From   *civil.Date
	To     *civil.Date
}

// ListBlocks lists blocks newest first, before beforeID.
func (s *Service) ListBlocks(ctx context.Context, propertyID int64, beforeID *int64, f BlockFilter, limit int) ([]RoomBlock, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	if f.Status != nil && *f.Status != BlockActive && *f.Status != BlockCancelled {
		return nil, apperr.Invalid("the filter is invalid", apperr.FieldError{Field: "status", Code: "INVALID_VALUE", Message: "ACTIVE or CANCELLED"})
	}
	rows, err := s.q(ctx).ListRoomBlocks(ctx, roomsdb.ListRoomBlocksParams{
		TenantID: p.TenantID, PropertyID: propertyID, BeforeID: beforeID, RoomID: f.RoomID, Status: f.Status,
		FromDate: f.From, ToDate: f.To, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]RoomBlock, len(rows))
	for i, r := range rows {
		out[i] = toBlock(r)
	}
	return out, nil
}

// GetBlock returns one block.
func (s *Service) GetBlock(ctx context.Context, propertyID, id int64) (RoomBlock, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomBlock{}, err
	}
	if err := s.authz.CanAccess(ctx, propertyID); err != nil {
		return RoomBlock{}, err
	}
	row, err := s.q(ctx).GetRoomBlock(ctx, roomsdb.GetRoomBlockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return RoomBlock{}, orNotFound(err, errBlockNotFound())
	}
	return toBlock(row), nil
}

// lockRoomForBlock takes L2 (the room's type) and L3 (the room) and returns the
// room as read under the locks.
func (s *Service) lockRoomForBlock(ctx context.Context, tenantID, propertyID, roomID int64) (roomsdb.Room, error) {
	q := s.q(ctx)
	peek, err := q.GetRoom(ctx, roomsdb.GetRoomParams{TenantID: tenantID, PropertyID: propertyID, ID: roomID})
	if err != nil {
		return roomsdb.Room{}, orNotFound(err, errRoomNotFound())
	}
	if err := lockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, errRoomTypeNotFound(), peek.RoomTypeID); err != nil {
		return roomsdb.Room{}, err
	}
	if err := lockRows(ctx, db.Rooms, db.ForUpdate, propertyID, errRoomNotFound(), roomID); err != nil {
		return roomsdb.Room{}, err
	}
	room, err := q.GetRoom(ctx, roomsdb.GetRoomParams{TenantID: tenantID, PropertyID: propertyID, ID: roomID})
	if err != nil {
		return roomsdb.Room{}, orNotFound(err, errRoomNotFound())
	}
	if room.RoomTypeID != peek.RoomTypeID {
		return roomsdb.Room{}, apperr.Busy("RESOURCE_BUSY", "the room was changed concurrently, please retry")
	}
	return room, nil
}

// CreateBlock takes a room out of sale (room_block.manage). Both OOO and OOS make
// the room unsellable. It is rejected while a stay or a CONFIRMED assigned
// reservation line holds the room in the range; an overlapping active block is
// rejected by the EXCLUDE constraint. (The type-level inventory check comes with M8.)
func (s *Service) CreateBlock(ctx context.Context, propertyID int64, in CreateBlockInput) (RoomBlock, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomBlock{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomBlockManage); err != nil {
		return RoomBlock{}, err
	}
	in.Reason = strings.TrimSpace(in.Reason)
	in.BlockType = strings.ToUpper(strings.TrimSpace(in.BlockType))
	var fields []apperr.FieldError
	if !validBlockType(in.BlockType) {
		fields = append(fields, apperr.FieldError{Field: "block_type", Code: "INVALID_VALUE", Message: "OOO or OOS"})
	}
	fields = append(fields, validateReason("reason", in.Reason)...)

	var out RoomBlock
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if dateFields := ValidateBlockDates(in.StartDate, in.EndDate, day.BusinessDate); len(dateFields) > 0 {
			fields = append(fields, dateFields...)
		}
		if len(fields) > 0 {
			return apperr.Invalid("the room block is invalid", fields...)
		}
		room, err := s.lockRoomForBlock(ctx, p.TenantID, propertyID, in.RoomID)
		if err != nil {
			return err
		}
		if !room.IsActive {
			return apperr.Conflict("ROOM_INACTIVE", "an inactive room cannot be blocked")
		}
		if err := s.requireNoConflicts(ctx, p.TenantID, propertyID, in.RoomID, day.BusinessDate, in.StartDate, in.EndDate); err != nil {
			return err
		}
		short, err := s.avail.BlockShortfalls(ctx, p.TenantID, propertyID, room.RoomTypeID, day.BusinessDate, in.StartDate, in.EndDate)
		if err != nil {
			return err
		}
		if err := oversold(short); err != nil {
			return err
		}
		row, err := s.q(ctx).CreateRoomBlock(ctx, roomsdb.CreateRoomBlockParams{
			TenantID: p.TenantID, PropertyID: propertyID, RoomID: in.RoomID, BlockType: in.BlockType,
			StartDate: in.StartDate, EndDate: in.EndDate, Reason: in.Reason, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toBlock(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room_block.created", "room_block", out.ID, nil, out))
	})
	return out, err
}

func (s *Service) requireNoConflicts(ctx context.Context, tenantID, propertyID, roomID int64, bd, start, end civil.Date) error {
	conflicts, err := s.roomConflicts(ctx, tenantID, propertyID, roomID, bd, start, end)
	if err != nil {
		return err
	}
	if len(conflicts) > 0 {
		return apperr.Conflict("ROOM_BLOCK_CONFLICT", "the room is occupied or assigned to reservations in this period").
			WithContext("conflicts", conflicts)
	}
	return nil
}

// BlockPatch changes selected attributes; nil fields stay unchanged.
type BlockPatch struct {
	StartDate *civil.Date
	EndDate   *civil.Date
	Reason    *string
}

// UpdateBlock changes the dates or reason of an ACTIVE block, or releases it early
// by moving end_date to the business date or later (room_block.manage).
// Added nights are re-checked for conflicts.
func (s *Service) UpdateBlock(ctx context.Context, propertyID, id int64, patch BlockPatch) (RoomBlock, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomBlock{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomBlockManage); err != nil {
		return RoomBlock{}, err
	}

	var out RoomBlock
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		peek, err := q.GetRoomBlock(ctx, roomsdb.GetRoomBlockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errBlockNotFound())
		}
		room, err := s.lockRoomForBlock(ctx, p.TenantID, propertyID, peek.RoomID)
		if err != nil {
			return err
		}
		row, err := q.GetRoomBlockForUpdate(ctx, roomsdb.GetRoomBlockForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errBlockNotFound())
		}
		if row.Status != BlockActive {
			return apperr.Conflict("ROOM_BLOCK_NOT_ACTIVE", "the room block is cancelled")
		}
		before := toBlock(row)
		start, end, reason := before.StartDate, before.EndDate, before.Reason
		apply(&start, patch.StartDate)
		apply(&end, patch.EndDate)
		apply(&reason, patch.Reason)
		reason = strings.TrimSpace(reason)

		fields := validateReason("reason", reason)
		if !end.After(start) {
			fields = append(fields, apperr.FieldError{Field: "end_date", Code: "OUT_OF_RANGE", Message: "must be after start_date"})
		}
		if end.Before(day.BusinessDate) {
			fields = append(fields, apperr.FieldError{Field: "end_date", Code: "OUT_OF_RANGE", Message: "must not be before the business date " + day.BusinessDate.String()})
		}
		if !start.Equal(before.StartDate) && start.Before(day.BusinessDate) {
			fields = append(fields, apperr.FieldError{Field: "start_date", Code: "OUT_OF_RANGE", Message: "must not be before the business date " + day.BusinessDate.String()})
		}
		if len(fields) > 0 {
			return apperr.Invalid("the room block is invalid", fields...)
		}

		if start.Before(before.StartDate) || end.After(before.EndDate) { // nights were added
			from := start
			if from.Before(day.BusinessDate) {
				from = day.BusinessDate
			}
			if err := s.requireNoConflicts(ctx, p.TenantID, propertyID, before.RoomID, day.BusinessDate, from, end); err != nil {
				return err
			}
			// Only the added nights take another room out of the stock.
			var added []availability.Shortfall
			if start.Before(before.StartDate) {
				sh, err := s.avail.BlockShortfalls(ctx, p.TenantID, propertyID, room.RoomTypeID, day.BusinessDate, start, before.StartDate)
				if err != nil {
					return err
				}
				added = append(added, sh...)
			}
			if end.After(before.EndDate) {
				sh, err := s.avail.BlockShortfalls(ctx, p.TenantID, propertyID, room.RoomTypeID, day.BusinessDate, before.EndDate, end)
				if err != nil {
					return err
				}
				added = append(added, sh...)
			}
			if err := oversold(added); err != nil {
				return err
			}
		}
		updated, err := q.UpdateRoomBlock(ctx, roomsdb.UpdateRoomBlockParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, StartDate: start, EndDate: end, Reason: reason, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toBlock(updated)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room_block.updated", "room_block", id, before, out))
	})
	return out, err
}

// CancelBlock releases a block (room_block.manage). A reason is required and audited.
func (s *Service) CancelBlock(ctx context.Context, propertyID, id int64, reason string) (RoomBlock, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return RoomBlock{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermRoomBlockManage); err != nil {
		return RoomBlock{}, err
	}
	reason = strings.TrimSpace(reason)
	if fields := validateReason("reason", reason); len(fields) > 0 {
		return RoomBlock{}, apperr.Invalid("the cancellation is invalid", fields...)
	}

	var out RoomBlock
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetRoomBlockForUpdate(ctx, roomsdb.GetRoomBlockForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errBlockNotFound())
		}
		if row.Status != BlockActive {
			return apperr.Conflict("ROOM_BLOCK_NOT_ACTIVE", "the room block is already cancelled")
		}
		now := s.clock.Now()
		cancelled, err := q.CancelRoomBlock(ctx, roomsdb.CancelRoomBlockParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, CancelledAt: &now, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toBlock(cancelled)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "room_block.cancelled", "room_block", id,
			toBlock(row), map[string]any{"block": out, "cancel_reason": reason}))
	})
	return out, err
}

// oversold turns a non-empty shortfall into 409 INVENTORY_OVERSOLD (the type would have fewer sellable rooms
// than it has demand), listing the nights.
func oversold(short []availability.Shortfall) error {
	if len(short) == 0 {
		return nil
	}
	shown := short
	if len(shown) > 60 {
		shown = shown[:60]
	}
	return apperr.Conflict("INVENTORY_OVERSOLD", "the change would leave the room type oversold on some nights").
		WithContext("nights", shown).WithContext("short_nights", len(short))
}
