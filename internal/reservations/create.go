package reservations

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations/reservationsdb"
	"kamarapms/internal/tenancy"
)

// Create makes a draft reservation (reservation.create), or confirms it at once. key is the request's
// Idempotency-Key: the same key and body returns the stored reservation, the same key with another body is
// 422 IDEMPOTENCY_KEY_REUSED. Drafts hold no inventory; confirming takes the confirm locks.
func (s *Service) Create(ctx context.Context, propertyID int64, key string, in CreateInput) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCreate)
	if err != nil {
		return Reservation{}, err
	}
	fields := in.validateHeader()
	for i, l := range in.Rooms {
		if l.RoomTypeID < 1 {
			fields = append(fields, fieldErr(fmt.Sprintf("rooms[%d].room_type_id", i), "REQUIRED", "a room type"))
		}
		if l.RatePlanID < 1 {
			fields = append(fields, fieldErr(fmt.Sprintf("rooms[%d].rate_plan_id", i), "REQUIRED", "a rate plan"))
		}
		if l.RoomID != nil && !in.Confirm {
			fields = append(fields, fieldErr(fmt.Sprintf("rooms[%d].room_id", i), "REQUIRES_CONFIRM", "a room can only be assigned on a confirmed reservation"))
		}
	}
	if len(key) > 100 {
		fields = append(fields, fieldErr("Idempotency-Key", "TOO_LONG", "at most 100 characters"))
	}
	if len(fields) > 0 {
		return Reservation{}, apperr.Invalid("the reservation is invalid", fields...)
	}
	hash := in.Hash()
	for attempt := 0; attempt < 2; attempt++ {
		if key != "" {
			if res, ok, err := s.replay(ctx, p, propertyID, key, hash); ok || err != nil {
				return res, err
			}
		}
		res, err := s.create(ctx, p, propertyID, key, hash, in, nil)
		if key != "" && apperr.IsCode(err, "DUPLICATE_REQUEST") {
			continue // a concurrent request with the same key won; replay it
		}
		return res, err
	}
	return Reservation{}, apperr.Busy("REQUEST_IN_PROGRESS", "the same request is still being processed")
}

func (s *Service) replay(ctx context.Context, p auth.Principal, propertyID int64, key, hash string) (Reservation, bool, error) {
	row, err := s.q(ctx).GetReservationByKey(ctx, reservationsdb.GetReservationByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Reservation{}, false, nil
		}
		return Reservation{}, false, err
	}
	if row.IdempotencyHash == nil || *row.IdempotencyHash != hash {
		return Reservation{}, false, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used with a different request")
	}
	res, err := s.load(ctx, p.TenantID, propertyID, row)
	return res, true, err
}

// CreateHeld is Create for a caller that already holds the locks of the confirm protocol: the business day
// (share) and the room types and rooms of the request (walk-in, which must create a guest before the booking
// and so cannot let the booking take those locks after a sequence). bd is the locked business date. The input
// is validated and priced as usual; no lock is taken here.
func (s *Service) CreateHeld(ctx context.Context, propertyID int64, bd civil.Date, in CreateInput) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCreate)
	if err != nil {
		return Reservation{}, err
	}
	if fields := in.validateHeader(); len(fields) > 0 {
		return Reservation{}, apperr.Invalid("the reservation is invalid", fields...)
	}
	return s.create(ctx, p, propertyID, "", in.Hash(), in, &bd)
}

// create runs the creation inside a transaction. held, when set, is the business date of a caller that has
// already taken the business day, room type and room locks.
func (s *Service) create(ctx context.Context, p auth.Principal, propertyID int64, key, hash string, in CreateInput, held *civil.Date) (Reservation, error) {
	var out Reservation
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		var bd civil.Date
		if held != nil {
			bd = *held
		} else {
			day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
			if err != nil {
				return err
			}
			bd = day.BusinessDate
		}
		typeIDs := make([]int64, 0, len(in.Rooms))
		var roomIDs []int64
		for _, l := range in.Rooms {
			typeIDs = append(typeIDs, l.RoomTypeID)
			if l.RoomID != nil {
				roomIDs = append(roomIDs, *l.RoomID)
			}
		}
		// L2 and L3 first when the reservation is confirmed at once (a draft holds nothing).
		if in.Confirm && held == nil {
			if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, typeIDs); err != nil {
				return mapNotFound(err, errRoomTypeNotFound())
			}
			if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, roomIDs); err != nil {
				return mapNotFound(err, errRoomNotFound())
			}
		}
		types, err := s.bookingTypes(ctx, p.TenantID, propertyID, typeIDs)
		if err != nil {
			return err
		}
		roomTypes, err := s.roomTypesOf(ctx, p.TenantID, propertyID, roomIDs)
		if err != nil {
			return err
		}
		if err := s.requireGuest(ctx, in.GuestID); err != nil {
			return err
		}
		decimals, err := s.decimals(ctx, propertyID)
		if err != nil {
			return err
		}
		var fields []apperr.FieldError
		priced := make([]pricedLine, len(in.Rooms))
		holds := make([]hold, len(in.Rooms))
		for i, l := range in.Rooms {
			prefix := fmt.Sprintf("rooms[%d].", i)
			t := types[l.RoomTypeID]
			if err := requireActiveType(t); err != nil {
				return err
			}
			fields = append(fields, validateDates(prefix, l.Arrival, l.Departure, bd, true)...)
			fields = append(fields, validateOccupancy(prefix, l.Adults, l.Children, t.MaxAdult, t.MaxChild, t.MaxOccupancy)...)
			if l.RoomID != nil && roomTypes[*l.RoomID] != l.RoomTypeID {
				fields = append(fields, fieldErr(prefix+"room_id", "ROOM_TYPE_MISMATCH", "the room is of another room type; assign it after booking to upgrade"))
			}
			if err := s.requireGuest(ctx, l.GuestID); err != nil {
				return err
			}
			holds[i] = hold{typeID: l.RoomTypeID, roomID: l.RoomID, from: l.Arrival, to: l.Departure}
		}
		if len(fields) > 0 {
			return apperr.Invalid("the reservation is invalid", fields...)
		}
		for i, l := range in.Rooms {
			if priced[i], err = s.priceLine(ctx, propertyID, p.TenantID, fmt.Sprintf("rooms[%d].", i), l.RatePlanID, l.RoomTypeID, l.Arrival, l.Departure, l.Overrides, decimals, nil); err != nil {
				return err
			}
		}
		if in.Confirm {
			if err := s.checkHolds(ctx, p.TenantID, propertyID, bd, holds, nil); err != nil {
				return err
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqReservation) // L5, last
		if err != nil {
			return err
		}
		q := s.q(ctx)
		params := reservationsdb.InsertReservationParams{
			TenantID: p.TenantID, PropertyID: propertyID, ConfirmationNumber: number, GuestID: in.GuestID, ReservationDate: bd,
			Source: in.Source, Market: nullable(in.Market), SpecialRequest: nullable(in.SpecialRequest), Remarks: nullable(in.Remarks),
			ActorID: p.ActorID(),
		}
		if key != "" {
			params.IdempotencyKey, params.IdempotencyHash = &key, &hash
		}
		res, err := q.InsertReservation(ctx, params)
		if err != nil {
			return err
		}
		lineStatus := LineDraft
		if in.Confirm {
			lineStatus = LineConfirmed
		}
		for i, l := range in.Rooms {
			line, err := q.InsertLine(ctx, reservationsdb.InsertLineParams{
				TenantID: p.TenantID, PropertyID: propertyID, ReservationID: res.ID, GuestID: l.GuestID, RoomTypeID: l.RoomTypeID, RoomID: l.RoomID,
				RatePlanID: l.RatePlanID, ArrivalDate: l.Arrival, DepartureDate: l.Departure, AdultCount: int16(l.Adults), ChildCount: int16(l.Children), //nolint:gosec // G115: bounded by validateOccupancy
				Status: lineStatus, ActorID: p.ActorID(),
			})
			if err != nil {
				return err
			}
			if err := s.storeRates(ctx, p, propertyID, line.ID, priced[i]); err != nil {
				return err
			}
		}
		if in.Confirm {
			if res, err = q.ConfirmReservation(ctx, reservationsdb.ConfirmReservationParams{
				TenantID: p.TenantID, PropertyID: propertyID, ID: res.ID, Now: s.clock.Now(), ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "reservation.created", res.ID, nil, map[string]any{
			"confirmation_number": res.ConfirmationNumber, "status": res.Status, "rooms": len(in.Rooms), "source": res.Source,
		})); err != nil {
			return err
		}
		if in.Confirm {
			if err := s.confirmed(ctx, p, propertyID, res.ID, bd); err != nil {
				return err
			}
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}
