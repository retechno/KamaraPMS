package reservations

import (
	"context"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations/reservationsdb"
)

// AddLine adds a room to a DRAFT or CONFIRMED reservation (reservation.update). On a CONFIRMED reservation
// the room is created CONFIRMED and goes through the availability check.
func (s *Service) AddLine(ctx context.Context, propertyID, id int64, version int32, in LineInput) (Reservation, error) {
	ctx = WithFreeApproval(WithOverrideApproval(ctx, in.RateOverrideApproval, in.RateOverrideReason), in.OccupancyApproval, in.ExceedFreeQuota)
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Reservation{}, err
	}
	if err := requireVersion(version); err != nil {
		return Reservation{}, err
	}
	if in.RoomTypeID < 1 || in.RatePlanID < 1 {
		return Reservation{}, apperr.Invalid("the room is invalid", fieldErr("room_type_id", "REQUIRED", "a room type and a rate plan"))
	}
	var out Reservation
	opts := lockOpts{inventory: true, extraTypes: []int64{in.RoomTypeID}}
	if in.RoomID != nil {
		opts.extraRooms = []int64{*in.RoomID}
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, version, opts)
		if err != nil {
			return err
		}
		if e := notCancelled(st); e != nil {
			return e
		}
		if len(st.lines) >= maxLinesPerRes {
			return apperr.Invalid("the reservation is invalid", fieldErr("rooms", "TOO_MANY", "at most 50 rooms per reservation"))
		}
		confirmed := st.res.Status == StatusConfirmed
		if in.RoomID != nil && !confirmed {
			return apperr.Invalid("the room is invalid", fieldErr("room_id", "REQUIRES_CONFIRM", "a room can only be assigned on a confirmed reservation"))
		}
		types, err := s.bookingTypes(ctx, p.TenantID, propertyID, []int64{in.RoomTypeID})
		if err != nil {
			return err
		}
		t := types[in.RoomTypeID]
		if err := requireActiveType(t); err != nil {
			return err
		}
		fields := validateDates("", in.Arrival, in.Departure, st.bd, true)
		fields = append(fields, validateOccupancy("", in.Adults, in.Children, t.MaxAdult, t.MaxChild, t.MaxOccupancy)...)
		if in.RoomID != nil && st.roomTypes[*in.RoomID] != in.RoomTypeID {
			fields = append(fields, fieldErr("room_id", "ROOM_TYPE_MISMATCH", "the room is of another room type; assign it after booking to upgrade"))
		}
		if len(fields) > 0 {
			return apperr.Invalid("the room is invalid", fields...)
		}
		if err := s.requireGuest(ctx, in.GuestID); err != nil {
			return err
		}
		if in.BedLocked && in.BedTypeID == nil {
			return apperr.Invalid("the room is invalid", fieldErr("bed_locked", "BED_LOCK_NEEDS_BED_TYPE", "choose a bed type to lock the bed"))
		}
		if err := s.requireBedType(ctx, p.TenantID, propertyID, "bed_type_id", in.BedTypeID, nil); err != nil {
			return err
		}
		decimals, err := s.decimals(ctx, propertyID)
		if err != nil {
			return err
		}
		priced, err := s.priceLine(ctx, propertyID, p.TenantID, "", in.RatePlanID, in.RoomTypeID, in.Arrival, in.Departure, in.Overrides, decimals, nil)
		if err != nil {
			return err
		}
		reason, err := s.occupancyReason(ctx, propertyID, p.TenantID, "", priced.kind, in.OccupancyReason, true, in.Arrival, in.Departure, nil)
		if err != nil {
			return err
		}
		status := LineDraft
		if confirmed {
			status = LineConfirmed
			if err := s.checkHolds(ctx, p.TenantID, propertyID, st.bd, []hold{{typeID: in.RoomTypeID, roomID: in.RoomID, bedID: lockedBed(in.BedLocked, in.BedTypeID), from: in.Arrival, to: in.Departure}}, nil); err != nil {
				return err
			}
		}
		line, err := s.q(ctx).InsertLine(ctx, reservationsdb.InsertLineParams{
			TenantID: p.TenantID, PropertyID: propertyID, ReservationID: id, GuestID: in.GuestID, RoomTypeID: in.RoomTypeID, RoomID: in.RoomID,
			RatePlanID: in.RatePlanID, ArrivalDate: in.Arrival, DepartureDate: in.Departure, AdultCount: int16(in.Adults), ChildCount: int16(in.Children), //nolint:gosec // G115: bounded by validateOccupancy
			RequestedBedTypeID: in.BedTypeID, BedLocked: in.BedLocked, OccupancyReason: reason, Status: status, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if err := s.storeRates(ctx, p, propertyID, line.ID, priced); err != nil {
			return err
		}
		res, err := s.bump(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.room_added", id, nil, withFreeRoomAudit(ctx, withOverrideAudit(ctx, map[string]any{
			"reservation_room_id": line.ID, "status": status, "room_type_id": in.RoomTypeID, "arrival_date": in.Arrival, "departure_date": in.Departure,
		})))); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// AmendLine changes a DRAFT or CONFIRMED room (reservation.update). Availability is re-checked without the
// room's own demand. Nights that survive keep their price snapshot; changing the rate plan or room type
// prices every night again; new nights are priced from the grid.
func (s *Service) AmendLine(ctx context.Context, propertyID, id, lineID int64, patch LinePatch) (Reservation, error) {
	ctx = WithFreeApproval(WithOverrideApproval(ctx, patch.RateOverrideApproval, patch.RateOverrideReason), patch.OccupancyApproval, patch.ExceedFreeQuota)
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Reservation{}, err
	}
	if err := requireVersion(patch.Version); err != nil {
		return Reservation{}, err
	}
	opts := lockOpts{inventory: true}
	if patch.RoomTypeID != nil {
		opts.extraTypes = []int64{*patch.RoomTypeID}
	}
	var out Reservation
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, patch.Version, opts)
		if err != nil {
			return err
		}
		if e := notCancelled(st); e != nil {
			return e
		}
		old, ok := st.line(lineID)
		if !ok {
			return errLineNotFound()
		}
		if old.Status != LineDraft && old.Status != LineConfirmed {
			return apperr.Conflict("LINE_NOT_AMENDABLE", "only a draft or confirmed room can be amended").WithContext("status", old.Status)
		}
		next := old
		if patch.Arrival != nil {
			next.ArrivalDate = *patch.Arrival
		}
		if patch.Departure != nil {
			next.DepartureDate = *patch.Departure
		}
		if patch.RoomTypeID != nil {
			next.RoomTypeID = *patch.RoomTypeID
		}
		if patch.RatePlanID != nil {
			next.RatePlanID = *patch.RatePlanID
		}
		if patch.Adults != nil {
			next.AdultCount = int16(*patch.Adults) //nolint:gosec // G115: checked by validateOccupancy
		}
		if patch.Children != nil {
			next.ChildCount = int16(*patch.Children) //nolint:gosec // G115: checked by validateOccupancy
		}
		if patch.BedTypeID != nil {
			next.RequestedBedTypeID = patch.BedTypeID
			if *patch.BedTypeID == 0 {
				next.RequestedBedTypeID = nil
			}
		}
		if patch.BedLocked != nil {
			next.BedLocked = *patch.BedLocked
		}
		if next.RequestedBedTypeID == nil {
			if patch.BedLocked != nil && *patch.BedLocked {
				return apperr.Invalid("the room is invalid", fieldErr("bed_locked", "BED_LOCK_NEEDS_BED_TYPE", "choose a bed type to lock the bed"))
			}
			next.BedLocked = false // no request, nothing to lock
		}
		if err := s.requireBedType(ctx, p.TenantID, propertyID, "bed_type_id", next.RequestedBedTypeID, old.RequestedBedTypeID); err != nil {
			return err
		}
		reasonText := deref(next.OccupancyReason)
		if patch.OccupancyReason != nil {
			reasonText = *patch.OccupancyReason
		}
		typeChanged := next.RoomTypeID != old.RoomTypeID
		planChanged := next.RatePlanID != old.RatePlanID
		datesChanged := !next.ArrivalDate.Equal(old.ArrivalDate) || !next.DepartureDate.Equal(old.DepartureDate)
		if typeChanged && old.RoomID != nil {
			return apperr.Conflict("ROOM_ASSIGNED", "unassign the room before changing the room type")
		}
		types, err := s.bookingTypes(ctx, p.TenantID, propertyID, []int64{next.RoomTypeID})
		if err != nil {
			return err
		}
		t := types[next.RoomTypeID]
		if typeChanged {
			if err := requireActiveType(t); err != nil {
				return err
			}
		}
		fields := validateDates("", next.ArrivalDate, next.DepartureDate, st.bd, datesChanged)
		fields = append(fields, validateOccupancy("", int(next.AdultCount), int(next.ChildCount), t.MaxAdult, t.MaxChild, t.MaxOccupancy)...)
		if len(fields) > 0 {
			return apperr.Invalid("the room is invalid", fields...)
		}
		decimals, err := s.decimals(ctx, propertyID)
		if err != nil {
			return err
		}
		var keep map[civil.Date]bool
		if !typeChanged && !planChanged {
			keep = map[civil.Date]bool{}
			existing, err := s.q(ctx).ListNightRates(ctx, reservationsdb.ListNightRatesParams{TenantID: p.TenantID, PropertyID: propertyID, LineIds: []int64{lineID}})
			if err != nil {
				return err
			}
			for _, r := range existing {
				if !r.StayDate.Before(next.ArrivalDate) && r.StayDate.Before(next.DepartureDate) {
					keep[r.StayDate] = true
				}
			}
		}
		priced, err := s.priceLine(ctx, propertyID, p.TenantID, "", next.RatePlanID, next.RoomTypeID, next.ArrivalDate, next.DepartureDate, patch.Overrides, decimals, keep)
		if err != nil {
			return err
		}
		if next.OccupancyReason, err = s.occupancyReason(ctx, propertyID, p.TenantID, "", priced.kind, reasonText, planChanged || datesChanged, next.ArrivalDate, next.DepartureDate, &lineID); err != nil {
			return err
		}
		// Locking, unlocking or changing the bed of a locked line moves demand between the lines of the type and the bed.
		bedChanged := next.BedLocked != old.BedLocked || (next.BedLocked && !sameBed(next.RequestedBedTypeID, old.RequestedBedTypeID))
		if next.Status == LineConfirmed && (typeChanged || datesChanged || bedChanged) {
			h := hold{lineID: lineID, typeID: st.effType(next), roomID: next.RoomID, bedID: lockedBed(next.BedLocked, next.RequestedBedTypeID), from: next.ArrivalDate, to: next.DepartureDate}
			if err := s.checkHolds(ctx, p.TenantID, propertyID, st.bd, []hold{h}, &lineID); err != nil {
				return err
			}
		}
		q := s.q(ctx)
		if keep == nil {
			if err := q.DeleteNightRates(ctx, reservationsdb.DeleteNightRatesParams{PropertyID: propertyID, LineID: lineID}); err != nil {
				return err
			}
		} else {
			if err := q.DeleteNightRatesOutside(ctx, reservationsdb.DeleteNightRatesOutsideParams{
				PropertyID: propertyID, LineID: lineID, Arrival: next.ArrivalDate, Departure: next.DepartureDate}); err != nil {
				return err
			}
			for _, r := range priced.rows {
				if err := q.DeleteNightRate(ctx, reservationsdb.DeleteNightRateParams{PropertyID: propertyID, LineID: lineID, StayDate: r.date}); err != nil {
					return err
				}
			}
		}
		if _, err := s.saveLine(ctx, p, propertyID, next); err != nil {
			return err
		}
		if err := s.storeRates(ctx, p, propertyID, lineID, priced); err != nil {
			return err
		}
		res, err := s.bump(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.room_amended", id,
			lineSnapshot(old), withFreeRoomAudit(ctx, withOverrideAudit(ctx, lineSnapshot(next))))); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

func lineSnapshot(l reservationsdb.ReservationRoom) map[string]any {
	return map[string]any{
		"reservation_room_id": l.ID, "room_type_id": l.RoomTypeID, "rate_plan_id": l.RatePlanID, "arrival_date": l.ArrivalDate,
		"departure_date": l.DepartureDate, "adult_count": l.AdultCount, "child_count": l.ChildCount, "room_id": l.RoomID,
		"requested_bed_type_id": l.RequestedBedTypeID, "bed_locked": l.BedLocked, "occupancy_reason": l.OccupancyReason,
	}
}

// AssignRoom puts a specific room on a CONFIRMED room line (reservation.update). A room of another type than
// the booked one is an upgrade: it needs the flag and reservation.upgrade, and the room's own type must
// have the inventory for the nights. The room must be free (no block, no other line, no open stay).
func (s *Service) AssignRoom(ctx context.Context, propertyID, id, lineID int64, version int32, roomID int64, upgrade bool) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Reservation{}, err
	}
	if err := requireVersion(version); err != nil {
		return Reservation{}, err
	}
	if roomID < 1 {
		return Reservation{}, apperr.Invalid("the request is invalid", fieldErr("room_id", "REQUIRED", "a room"))
	}
	var out Reservation
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, version, lockOpts{inventory: true, extraRooms: []int64{roomID}})
		if err != nil {
			return err
		}
		line, ok := st.line(lineID)
		if !ok {
			return errLineNotFound()
		}
		if line.Status != LineConfirmed {
			return apperr.Conflict("LINE_NOT_CONFIRMED", "a room can only be assigned to a confirmed reservation room").WithContext("status", line.Status)
		}
		physical := st.roomTypes[roomID]
		if physical != line.RoomTypeID {
			if !upgrade {
				return apperr.Conflict("ROOM_TYPE_MISMATCH", "the room is of another room type; set upgrade to assign it").
					WithContext("booked_room_type_id", line.RoomTypeID).WithContext("room_type_id", physical)
			}
			if err := s.authz.Require(ctx, propertyID, auth.PermReservationUpgrade); err != nil {
				return err
			}
		}
		h := hold{lineID: lineID, typeID: physical, roomID: &roomID, bedID: lockedBed(line.BedLocked, line.RequestedBedTypeID), from: line.ArrivalDate, to: line.DepartureDate}
		if err := s.checkHolds(ctx, p.TenantID, propertyID, st.bd, []hold{h}, &lineID); err != nil {
			return err
		}
		oldRoom := line.RoomID
		line.RoomID = &roomID
		if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
			return err
		}
		res, err := s.bump(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.room_assigned", id,
			map[string]any{"reservation_room_id": lineID, "room_id": oldRoom},
			map[string]any{"reservation_room_id": lineID, "room_id": roomID, "upgrade": physical != line.RoomTypeID})); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// UnassignRoom takes the specific room off a CONFIRMED room line (reservation.update). The line goes back to
// consuming its booked room type, so an upgraded line needs that type to have the inventory again. This is
// why it takes the room type locks, not only the reservation lock (a deviation from 06-api.md §12).
func (s *Service) UnassignRoom(ctx context.Context, propertyID, id, lineID int64, version int32) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Reservation{}, err
	}
	if err := requireVersion(version); err != nil {
		return Reservation{}, err
	}
	var out Reservation
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, version, lockOpts{inventory: true})
		if err != nil {
			return err
		}
		line, ok := st.line(lineID)
		if !ok {
			return errLineNotFound()
		}
		if line.Status != LineConfirmed || line.RoomID == nil {
			return apperr.Conflict("NO_ROOM_ASSIGNED", "the reservation room is not a confirmed room with an assigned room")
		}
		if st.roomTypes[*line.RoomID] != line.RoomTypeID {
			h := hold{lineID: lineID, typeID: line.RoomTypeID, bedID: lockedBed(line.BedLocked, line.RequestedBedTypeID), from: line.ArrivalDate, to: line.DepartureDate}
			if err := s.checkHolds(ctx, p.TenantID, propertyID, st.bd, []hold{h}, &lineID); err != nil {
				return err
			}
		}
		oldRoom := *line.RoomID
		line.RoomID = nil
		if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
			return err
		}
		res, err := s.bump(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.room_unassigned", id,
			map[string]any{"reservation_room_id": lineID, "room_id": oldRoom}, map[string]any{"reservation_room_id": lineID, "room_id": nil})); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

func sameBed(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
