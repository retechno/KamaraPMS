package rooms

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/rooms/roomsdb"
)

func errRoomTypeNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_TYPE_NOT_FOUND", "the room type does not exist in this property")
}

func errRoomNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
}

func errBlockNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_BLOCK_NOT_FOUND", "the room block does not exist in this property")
}

// orNotFound converts pgx.ErrNoRows into nf.
func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

// farFuture is the open end of "from the business date on" range checks.
var farFuture = civil.NewDate(9999, 12, 31)

func toRoomType(r roomsdb.RoomType) RoomType {
	return RoomType{
		ID: r.ID, Code: r.Code, Name: r.Name, Description: deref(r.Description),
		MaxAdult: int32(r.MaxAdult), MaxChild: int32(r.MaxChild), MaxOccupancy: int32(r.MaxOccupancy), BaseOccupancy: int32(r.BaseOccupancy),
		SortOrder: r.SortOrder, IsActive: r.IsActive, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func toRoom(r roomsdb.Room) Room {
	return Room{
		ID: r.ID, RoomTypeID: r.RoomTypeID, RoomNumber: r.RoomNumber, Floor: deref(r.Floor), Building: deref(r.Building),
		IsActive: r.IsActive, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func toBlock(b roomsdb.RoomBlock) RoomBlock {
	return RoomBlock{
		ID: b.ID, RoomID: b.RoomID, BlockType: b.BlockType, StartDate: b.StartDate, EndDate: b.EndDate, Reason: b.Reason,
		Status: b.Status, CancelledAt: b.CancelledAt, CancelledBy: b.CancelledBy, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// int16Of narrows a validated small integer (Validate bounds every occupancy field to 0..20).
func int16Of(v int32) int16 { return int16(v) } //nolint:gosec // G115: validated to 0..20 before use

// rowLimit bounds a page size for SQL LIMIT.
func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}
