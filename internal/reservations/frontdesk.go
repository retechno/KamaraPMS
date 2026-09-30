package reservations

import (
	"context"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations/reservationsdb"
)

// This file is the door front desk use cases (check-in, reverse check-in) use to change reservation rows: the
// reservations module stays the only writer of reservations and reservation_rooms.

// Locked is a reservation locked at L4, with what a front desk use case needs.
type Locked struct {
	Reservation  reservationsdb.Reservation
	Lines        []reservationsdb.ReservationRoom
	RoomTypes    map[int64]int64 // room id -> physical room type
	BusinessDate civil.Date
}

// Line returns one of the locked lines.
func (l Locked) Line(id int64) (reservationsdb.ReservationRoom, bool) {
	for _, x := range l.Lines {
		if x.ID == id {
			return x, true
		}
	}
	return reservationsdb.ReservationRoom{}, false
}

// Lock takes the locks of the reservation lock protocol (business day share, the room types of its lines and of
// extraRooms and extraTypes, the rooms, then the reservation and its lines) and checks the version. Must run in
// a transaction.
func (s *Service) Lock(ctx context.Context, tenantID, propertyID, id int64, version int32, extraTypes, extraRooms []int64) (Locked, error) {
	st, err := s.lock(ctx, tenantID, propertyID, id, version, lockOpts{inventory: true, extraTypes: extraTypes, extraRooms: extraRooms})
	if err != nil {
		return Locked{}, err
	}
	return Locked{Reservation: st.res, Lines: st.lines, RoomTypes: st.roomTypes, BusinessDate: st.bd}, nil
}

// MarkCheckedIn puts a CONFIRMED line into CHECKED_IN in the given room and bumps the reservation's version.
func (s *Service) MarkCheckedIn(ctx context.Context, p auth.Principal, propertyID, lineID, roomID int64) error {
	line, err := s.q(ctx).GetLine(ctx, reservationsdb.GetLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
	if err != nil {
		return orNotFound(err, errLineNotFound())
	}
	if line.Status != LineConfirmed {
		return apperr.Conflict("LINE_NOT_CONFIRMED", "only a confirmed room can be checked in").WithContext("status", line.Status)
	}
	line.Status, line.RoomID = LineCheckedIn, &roomID
	if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
		return err
	}
	_, err = s.bump(ctx, p, propertyID, line.ReservationID)
	return err
}

// MarkCheckInReversed puts a CHECKED_IN line back to CONFIRMED and bumps the reservation's version.
func (s *Service) MarkCheckInReversed(ctx context.Context, p auth.Principal, propertyID, lineID int64) error {
	line, err := s.q(ctx).GetLine(ctx, reservationsdb.GetLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
	if err != nil {
		return orNotFound(err, errLineNotFound())
	}
	if line.Status != LineCheckedIn {
		return apperr.Conflict("LINE_NOT_CHECKED_IN", "the room is not checked in").WithContext("status", line.Status)
	}
	line.Status = LineConfirmed
	if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
		return err
	}
	_, err = s.bump(ctx, p, propertyID, line.ReservationID)
	return err
}
