package reservations

import (
	"context"
	"fmt"

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

// MarkCompleted puts a CHECKED_IN line into COMPLETED (check-out) and bumps the reservation's version.
func (s *Service) MarkCompleted(ctx context.Context, p auth.Principal, propertyID, lineID int64) error {
	line, err := s.q(ctx).GetLine(ctx, reservationsdb.GetLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
	if err != nil {
		return orNotFound(err, errLineNotFound())
	}
	if line.Status != LineCheckedIn {
		return apperr.Conflict("LINE_NOT_CHECKED_IN", "the room is not checked in").WithContext("status", line.Status)
	}
	line.Status = LineCompleted
	if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
		return err
	}
	_, err = s.bump(ctx, p, propertyID, line.ReservationID)
	return err
}

// ExtendNights prices the nights [from, to) of a line from the grid (or overrides, which need perm) with the line's
// rate plan and room type, and stores them in the line's price snapshot, replacing any rows those nights had. The
// line's own dates do not change: a stay's departure lives on the stay.
func (s *Service) ExtendNights(ctx context.Context, p auth.Principal, perm auth.Permission, propertyID, lineID int64, from, to civil.Date, overrides []NightOverride) error {
	line, err := s.q(ctx).GetLine(ctx, reservationsdb.GetLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
	if err != nil {
		return orNotFound(err, errLineNotFound())
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return err
	}
	priced, err := s.priceLinePerm(ctx, perm, propertyID, p.TenantID, "", line.RatePlanID, line.RoomTypeID, from, to, overrides, decimals, nil)
	if err != nil {
		return err
	}
	q := s.q(ctx)
	for d := from; d.Before(to); d = d.AddDays(1) {
		if err := q.DeleteNightRate(ctx, reservationsdb.DeleteNightRateParams{PropertyID: propertyID, LineID: lineID, StayDate: d}); err != nil {
			return err
		}
	}
	return s.storeRates(ctx, p, propertyID, lineID, priced)
}

// TrimNights deletes the nightly rows of a line from newDeparture on (a stay that leaves earlier).
func (s *Service) TrimNights(ctx context.Context, propertyID, lineID int64, arrival, newDeparture civil.Date) error {
	return s.q(ctx).DeleteNightRatesOutside(ctx, reservationsdb.DeleteNightRatesOutsideParams{PropertyID: propertyID, LineID: lineID, Arrival: arrival, Departure: newDeparture})
}

// OverrideNights sets the agreed amount of single nights of a line (a room move with new rates). The overrides need
// perm. Nights not listed keep their snapshot.
func (s *Service) OverrideNights(ctx context.Context, p auth.Principal, perm auth.Permission, propertyID, lineID int64, overrides []NightOverride) error {
	if len(overrides) == 0 {
		return nil
	}
	line, err := s.q(ctx).GetLine(ctx, reservationsdb.GetLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
	if err != nil {
		return orNotFound(err, errLineNotFound())
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return err
	}
	q := s.q(ctx)
	existing, err := q.ListNightRates(ctx, reservationsdb.ListNightRatesParams{TenantID: p.TenantID, PropertyID: propertyID, LineIds: []int64{lineID}})
	if err != nil {
		return err
	}
	have := map[civil.Date]bool{}
	var last civil.Date
	for _, r := range existing {
		have[r.StayDate] = true
		if r.StayDate.After(last) {
			last = r.StayDate
		}
	}
	for i, o := range overrides {
		if !have[o.Date] {
			return apperr.Invalid("the rates are invalid", fieldErr(fmt.Sprintf("new_nightly_rates[%d].date", i), "OUT_OF_RANGE", "a night of the stay"))
		}
	}
	keep := have // every night keeps its row unless it is overridden
	priced, err := s.priceLinePerm(ctx, perm, propertyID, p.TenantID, "new_", line.RatePlanID, line.RoomTypeID, line.ArrivalDate, last.AddDays(1), overrides, decimals, keep)
	if err != nil {
		return err
	}
	for _, r := range priced.rows {
		if err := q.DeleteNightRate(ctx, reservationsdb.DeleteNightRateParams{PropertyID: propertyID, LineID: lineID, StayDate: r.date}); err != nil {
			return err
		}
	}
	return s.storeRates(ctx, p, propertyID, lineID, priced)
}
