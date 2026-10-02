package frontdesk

import (
	"context"
	"strings"

	"kamarapms/internal/availability"
	"kamarapms/internal/expected"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk/frontdeskdb"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/roomcharge"
)

// stayCtx is an in-house stay read before its locks are taken: the stay, its reservation room and the room it is in.
type stayCtx struct {
	stay frontdeskdb.Stay
	line frontdeskdb.GetStayLineRow
	seg  frontdeskdb.GetOpenSegmentRow
}

// readStay reads an OPEN stay with its line and open segment (unlocked: the caller locks and re-reads).
func (s *Service) readStay(ctx context.Context, p auth.Principal, propertyID, stayID int64) (stayCtx, error) {
	q := s.q(ctx)
	st, err := q.GetStay(ctx, frontdeskdb.GetStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID})
	if err != nil {
		return stayCtx{}, notFound(err, errStayNotFound())
	}
	if st.Status != "OPEN" {
		return stayCtx{}, errStayNotOpen(st.Status)
	}
	line, err := q.GetStayLine(ctx, frontdeskdb.GetStayLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: st.ReservationRoomID})
	if err != nil {
		return stayCtx{}, err
	}
	seg, err := q.GetOpenSegment(ctx, frontdeskdb.GetOpenSegmentParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
	if err != nil {
		return stayCtx{}, notFound(err, errStayNotOpen(st.Status))
	}
	return stayCtx{stay: st, line: line, seg: seg}, nil
}

func errStayNotOpen(status string) *apperr.Error {
	return apperr.Conflict("STAY_NOT_OPEN", "the stay is not in house").WithContext("status", status)
}

// lockStay locks the stay (L4) and re-reads it: the version must be the one the caller loaded.
func (s *Service) lockStay(ctx context.Context, p auth.Principal, propertyID, stayID int64, version int32) (frontdeskdb.Stay, error) {
	if err := db.LockRows(ctx, db.Stays, db.ForUpdate, propertyID, []int64{stayID}); err != nil {
		return frontdeskdb.Stay{}, mapNotFound(err, errStayNotFound())
	}
	st, err := s.q(ctx).GetStay(ctx, frontdeskdb.GetStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID})
	if err != nil {
		return st, err
	}
	if st.Status != "OPEN" {
		return st, errStayNotOpen(st.Status)
	}
	if st.Version != version {
		return st, apperr.Conflict("VERSION_CONFLICT", "the stay was changed by someone else").WithContext("current_version", st.Version)
	}
	return st, nil
}

func maxDate(a, b civil.Date) civil.Date {
	if b.After(a) {
		return b
	}
	return a
}

func requireVersion(v int32) []apperr.FieldError {
	if v < 1 {
		return []apperr.FieldError{fieldErr("version", "REQUIRED", "the version you loaded")}
	}
	return nil
}

// Move moves an in-house stay to another room (frontdesk.room_move). The target must be free for the rest of the
// stay and ready (same cleanliness rule as check-in); another room type needs its inventory. The current segment
// closes today and a new one opens, the old room becomes DIRTY, and tonight's room charge follows the new room.
// New nightly rates need frontdesk.rate_change and only apply to nights not yet charged.
func (s *Service) Move(ctx context.Context, propertyID, stayID int64, in MoveInput) (MoveResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskRoomMove)
	if err != nil {
		return MoveResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	fields := requireVersion(in.Version)
	if in.RoomID < 1 {
		fields = append(fields, fieldErr("room_id", "REQUIRED", "the room to move to"))
	}
	if reason == "" {
		fields = append(fields, fieldErr("reason", "REQUIRED", "a reason is required"))
	} else if len([]rune(reason)) > maxReasonLen {
		fields = append(fields, fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	if len([]rune(in.OverrideReason)) > maxReasonLen {
		fields = append(fields, fieldErr("override_reason", "TOO_LONG", "at most 500 characters"))
	}
	if len(fields) > 0 {
		return MoveResult{}, apperr.Invalid("the room move is invalid", fields...)
	}
	if len(in.NewNightlyRates) > 0 {
		if err := s.authz.Require(ctx, propertyID, auth.PermFrontdeskRateChange); err != nil {
			return MoveResult{}, err
		}
	}
	var out MoveResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		q := s.q(ctx)
		pre, err := s.readStay(ctx, p, propertyID, stayID)
		if err != nil {
			return err
		}
		target, err := q.GetRoomForCheckIn(ctx, frontdeskdb.GetRoomForCheckInParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.RoomID})
		if err != nil {
			return notFound(err, errRoomNotFound())
		}
		if target.ID == pre.seg.RoomID {
			return apperr.Conflict("SAME_ROOM", "the stay is already in this room")
		}
		if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, []int64{pre.seg.RoomTypeID, target.RoomTypeID}); err != nil { // L2
			return mapNotFound(err, apperr.NotFound("ROOM_TYPE_NOT_FOUND", "the room type does not exist in this property"))
		}
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, []int64{pre.seg.RoomID, target.ID}); err != nil { // L3
			return mapNotFound(err, errRoomNotFound())
		}
		if err := s.hk.MarkDirtyLocked(ctx, p.TenantID, propertyID, pre.seg.RoomID, bd, housekeeping.SourceRoomMove, reason, p.ActorID()); err != nil {
			return err
		}
		st, err := s.lockStay(ctx, p, propertyID, stayID, in.Version) // L4
		if err != nil {
			return err
		}
		// The target again, now that its room is locked.
		if target, err = q.GetRoomForCheckIn(ctx, frontdeskdb.GetRoomForCheckInParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.RoomID}); err != nil {
			return notFound(err, errRoomNotFound())
		}
		if !target.IsActive {
			return apperr.Conflict("ROOM_NOT_AVAILABLE", "the room is not active").WithContext("room_id", target.ID)
		}
		effDep := maxDate(st.DepartureDate, bd.AddDays(1))
		own := pre.line.ID
		issues, err := s.avail.RoomIssues(ctx, p.TenantID, propertyID, target.ID, bd, bd, effDep, &own)
		if err != nil {
			return err
		}
		if err := roomIssuesError(target.ID, issues); err != nil {
			return err
		}
		if target.RoomTypeID != pre.seg.RoomTypeID {
			extra := availability.Extra{}
			extra.Add(target.RoomTypeID, bd, effDep, 1)
			if err := s.avail.RequireAvailable(ctx, p.TenantID, propertyID, bd, extra, nil); err != nil {
				return err
			}
		}
		prop, err := s.days.GetProperty(ctx, propertyID)
		if err != nil {
			return err
		}
		if err := s.requireReady(ctx, checkInCmd{p: p, propertyID: propertyID, override: in.OverrideRoomNotReady, overrideBy: strings.TrimSpace(in.OverrideReason)},
			target.HousekeepingStatus, prop.RequireRoomInspectionForCheckin); err != nil {
			return err
		}
		if len(in.NewNightlyRates) > 0 {
			if err := s.requireUnposted(ctx, p, propertyID, stayID, in); err != nil {
				return err
			}
			if err := s.res.OverrideNights(ctx, p, auth.PermFrontdeskRateChange, propertyID, pre.line.ID, in.NewNightlyRates); err != nil {
				return err
			}
		}
		now := s.clock.Now()
		closed, err := q.CloseOpenSegment(ctx, frontdeskdb.CloseOpenSegmentParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID, CheckOutAt: &now, EndBusinessDate: &bd, Reason: &reason})
		if err != nil {
			return err
		}
		fresh, err := q.InsertStayRoom(ctx, frontdeskdb.InsertStayRoomParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID, RoomID: target.ID, CheckInAt: now, StartBusinessDate: bd, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		bumped, err := q.BumpStay(ctx, frontdeskdb.BumpStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "stay.room_moved", stayID,
			map[string]any{"room_id": pre.seg.RoomID, "room_number": pre.seg.RoomNumber},
			map[string]any{"room_id": target.ID, "room_number": target.RoomNumber, "reason": reason, "rates_changed": len(in.NewNightlyRates), "override_room_not_ready": in.OverrideRoomNotReady})); err != nil {
			return err
		}
		out = MoveResult{Stay: toStay(bumped, pre.line.ReservationID), ClosedSegment: toSegment(closed, pre.seg.RoomNumber), NewSegment: toSegment(fresh, target.RoomNumber)}
		return nil
	})
	return out, err
}

// requireUnposted rejects new nightly rates for a night that has already been charged.
func (s *Service) requireUnposted(ctx context.Context, p auth.Principal, propertyID, stayID int64, in MoveInput) error {
	last, err := s.q(ctx).LastPostedNight(ctx, frontdeskdb.LastPostedNightParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
	if err != nil || last.IsZero() {
		return err
	}
	posted, err := s.postedNights(ctx, p, propertyID, stayID)
	if err != nil {
		return err
	}
	for _, o := range in.NewNightlyRates {
		if posted[o.Date] {
			return apperr.Conflict("NIGHT_ALREADY_POSTED", "a night that is already charged keeps its rate: reverse the charge first").WithContext("date", o.Date)
		}
	}
	return nil
}

func (s *Service) postedNights(ctx context.Context, p auth.Principal, propertyID, stayID int64) (map[civil.Date]bool, error) {
	rows, err := s.q(ctx).ListPostedNights(ctx, frontdeskdb.ListPostedNightsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
	if err != nil {
		return nil, err
	}
	out := map[civil.Date]bool{}
	for _, d := range rows {
		out[d] = true
	}
	return out, nil
}

// roomIssuesError turns what keeps a room from being free into the API's error.
func roomIssuesError(roomID int64, issues []availability.RoomIssue) error {
	for _, i := range issues {
		switch i.Kind {
		case availability.IssueOccupied:
			return apperr.Conflict("ROOM_OCCUPIED", "the room is occupied").WithContext("room_id", roomID)
		case availability.IssueBlocked:
			return apperr.Conflict("ROOM_BLOCKED", "the room is out of order or out of service for these dates").WithContext("room_id", roomID).WithContext("block_id", i.ID)
		}
	}
	if len(issues) > 0 {
		return apperr.Conflict("ROOM_NOT_AVAILABLE", "the room is not free for the stay").WithContext("room_id", roomID).WithContext("issues", issues)
	}
	return nil
}

// ChangeDeparture extends, shortens or corrects the departure of an in-house stay (reservation.update).
//
// Extending needs the current room free for the added nights (otherwise 409 ROOM_NOT_AVAILABLE_FOR_EXTENSION with
// other rooms of the type to move to) and the room type's inventory; the added nights are priced from the grid or
// the overrides. Shortening needs the new date to be after the business date and after every charged night (reverse
// those first); the nights beyond are dropped. The reservation room keeps its original dates.
func (s *Service) ChangeDeparture(ctx context.Context, propertyID, stayID int64, in ChangeDepartureInput) (Stay, error) {
	p, err := s.actor(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Stay{}, err
	}
	if fields := requireVersion(in.Version); len(fields) > 0 {
		return Stay{}, apperr.Invalid("the departure is invalid", fields...)
	}
	var out Stay
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		q := s.q(ctx)
		pre, err := s.readStay(ctx, p, propertyID, stayID)
		if err != nil {
			return err
		}
		extending := in.DepartureDate.After(pre.stay.DepartureDate)
		if extending {
			if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, []int64{pre.seg.RoomTypeID}); err != nil { // L2
				return mapNotFound(err, apperr.NotFound("ROOM_TYPE_NOT_FOUND", "the room type does not exist in this property"))
			}
			if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, []int64{pre.seg.RoomID}); err != nil { // L3
				return mapNotFound(err, errRoomNotFound())
			}
		}
		st, err := s.lockStay(ctx, p, propertyID, stayID, in.Version) // L4
		if err != nil {
			return err
		}
		cur := st.DepartureDate
		switch {
		case in.DepartureDate.Equal(cur):
			return apperr.Invalid("the departure is invalid", fieldErr("departure_date", "UNCHANGED", "the stay already departs on this date"))
		case in.DepartureDate.After(cur):
			if err := s.extend(ctx, p, propertyID, bd, pre, st, in); err != nil {
				return err
			}
		default:
			if !in.DepartureDate.After(bd) {
				return apperr.Invalid("the departure is invalid", fieldErr("departure_date", "OUT_OF_RANGE", "after the business date: a guest who leaves today checks out"))
			}
			last, err := q.LastPostedNight(ctx, frontdeskdb.LastPostedNightParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
			if err != nil {
				return err
			}
			if !last.IsZero() && !last.Before(in.DepartureDate) {
				return apperr.Conflict("NIGHT_ALREADY_POSTED", "the stay cannot end before a night that is already charged: reverse the charge first").WithContext("last_posted_night", last)
			}
			if err := s.res.TrimNights(ctx, propertyID, pre.line.ID, pre.line.ArrivalDate, in.DepartureDate); err != nil {
				return err
			}
		}
		updated, err := q.UpdateStayDeparture(ctx, frontdeskdb.UpdateStayDepartureParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID, DepartureDate: in.DepartureDate, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "stay.departure_changed", stayID,
			map[string]any{"departure_date": cur}, map[string]any{"departure_date": in.DepartureDate})); err != nil {
			return err
		}
		out = toStay(updated, pre.line.ReservationID)
		return nil
	})
	return out, err
}

// extend checks and prices the added nights of an extension.
func (s *Service) extend(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, pre stayCtx, st frontdeskdb.Stay, in ChangeDepartureInput) error {
	cur := st.DepartureDate
	start := maxDate(cur, bd)
	issues, err := s.avail.RoomIssuesFor(ctx, p.TenantID, propertyID, pre.seg.RoomID, bd, start, in.DepartureDate, nil, &st.ID)
	if err != nil {
		return err
	}
	if len(issues) > 0 {
		alternatives, err := s.avail.FreeRooms(ctx, p.TenantID, propertyID, pre.seg.RoomTypeID, bd, start, in.DepartureDate)
		if err != nil {
			return err
		}
		return apperr.Conflict("ROOM_NOT_AVAILABLE_FOR_EXTENSION", "the room is not free for the extra nights: move the guest to another room").
			WithContext("suggest_room_move", true).WithContext("room_id", pre.seg.RoomID).WithContext("issues", issues).WithContext("alternative_rooms", alternatives)
	}
	if effDep := maxDate(cur, bd.AddDays(1)); in.DepartureDate.After(effDep) {
		extra := availability.Extra{}
		extra.Add(pre.seg.RoomTypeID, effDep, in.DepartureDate, 1)
		if err := s.avail.RequireAvailable(ctx, p.TenantID, propertyID, bd, extra, nil); err != nil {
			return err
		}
	}
	return s.res.ExtendNights(ctx, p, auth.PermReservationOverrideRate, propertyID, pre.line.ID, cur, in.DepartureDate, in.NightlyOverrides)
}

// AddGuest adds an accompanying guest to an in-house stay (frontdesk.checkin).
func (s *Service) AddGuest(ctx context.Context, propertyID, stayID int64, in AddGuestInput) (StayDetail, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskCheckin)
	if err != nil {
		return StayDetail{}, err
	}
	if in.GuestID < 1 {
		return StayDetail{}, apperr.Invalid("the guest is invalid", fieldErr("guest_id", "REQUIRED", "a guest"))
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := s.readStay(ctx, p, propertyID, stayID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Stays, db.ForUpdate, propertyID, []int64{stayID}); err != nil { // L4
			return mapNotFound(err, errStayNotFound())
		}
		if err := s.guests.RequireVisible(ctx, in.GuestID); err != nil {
			return err
		}
		present, err := q.HasStayGuest(ctx, frontdeskdb.HasStayGuestParams{PropertyID: propertyID, StayID: stayID, GuestID: in.GuestID})
		if err != nil {
			return err
		}
		if present || in.GuestID == pre.stay.GuestID {
			return apperr.Conflict("GUEST_ALREADY_ON_STAY", "the guest is already on this stay")
		}
		if err := q.InsertStayGuest(ctx, frontdeskdb.InsertStayGuestParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID, GuestID: in.GuestID, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "stay.guest_added", stayID, nil, map[string]any{"guest_id": in.GuestID}))
	})
	if err != nil {
		return StayDetail{}, err
	}
	return s.stayDetail(ctx, p, propertyID, stayID)
}

// CheckOut checks an in-house stay out (frontdesk.checkout), in one transaction: the departure is settled (today:
// at most today, an earlier departure needs confirmation; after midnight and before the audit: at most tomorrow, so
// the night of the business date is charged), the missing room charges are posted, nothing may remain READY or in
// ERROR, every open folio must have a zero balance and is closed, the segment closes, the stay and its room become
// CHECKED_OUT / COMPLETED and the room DIRTY. A stay is at least one night long, so a guest who arrives and leaves on
// the same business date is charged that night.
func (s *Service) CheckOut(ctx context.Context, propertyID, stayID int64, in CheckOutInput) (CheckOutResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskCheckout)
	if err != nil {
		return CheckOutResult{}, err
	}
	if fields := requireVersion(in.Version); len(fields) > 0 {
		return CheckOutResult{}, apperr.Invalid("the check-out is invalid", fields...)
	}
	var out CheckOutResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		q := s.q(ctx)
		st0, err := q.GetStay(ctx, frontdeskdb.GetStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID})
		if err != nil {
			return notFound(err, errStayNotFound())
		}
		if st0.Status == "CHECKED_OUT" && st0.Version == in.Version+1 {
			// A retry of a check-out that went through: answer with its result.
			out, err = s.checkedOutResult(ctx, p, propertyID, st0)
			return err
		}
		pre, err := s.readStay(ctx, p, propertyID, stayID)
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, []int64{pre.seg.RoomID}); err != nil { // L3
			return mapNotFound(err, errRoomNotFound())
		}
		if err := s.hk.MarkDirtyLocked(ctx, p.TenantID, propertyID, pre.seg.RoomID, bd, housekeeping.SourceCheckOut, "", p.ActorID()); err != nil {
			return err
		}
		st, err := s.lockStay(ctx, p, propertyID, stayID, in.Version) // L4
		if err != nil {
			return err
		}
		prop, err := s.days.GetProperty(ctx, propertyID)
		if err != nil {
			return err
		}
		newDep, err := s.settleDeparture(ctx, p, propertyID, bd, civil.DateOf(s.clock.Now().In(prop.Location())), st, pre, in.ConfirmEarlyDeparture)
		if err != nil {
			return err
		}
		rep, err := s.charges.PostLocked(ctx, p, propertyID, day, roomcharge.PostCmd{BusinessDate: bd, StayIDs: []int64{stayID}, Trigger: roomcharge.TriggerCheckOut})
		if err != nil {
			return err
		}
		var posted []roomcharge.Result
		var problems []roomcharge.Result
		for _, r := range rep.Items {
			switch r.Status {
			case roomcharge.StatusPosted:
				posted = append(posted, r)
			case expected.StatusError, expected.StatusReady:
				problems = append(problems, r)
			}
		}
		if len(problems) > 0 {
			return apperr.Conflict("REQUIRED_CHARGES_NOT_POSTED", "room charges of the stay cannot be posted: fix the errors first").WithContext("items", problems)
		}
		closedFolios, err := s.folios.CloseStayFolios(ctx, p, propertyID, stayID) // L4 folios; every balance must be zero
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := q.CloseOpenSegment(ctx, frontdeskdb.CloseOpenSegmentParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID, CheckOutAt: &now, EndBusinessDate: &bd}); err != nil {
			return err
		}
		done, err := q.CheckOutStay(ctx, frontdeskdb.CheckOutStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID, Now: now, DepartureDate: newDep, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.res.MarkCompleted(ctx, p, propertyID, pre.line.ID); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "stay.checked_out", stayID,
			map[string]any{"status": "OPEN", "departure_date": st.DepartureDate},
			map[string]any{"status": done.Status, "departure_date": newDep, "room_charges_posted": len(posted), "folios_closed": len(closedFolios)})); err != nil {
			return err
		}
		if posted == nil {
			posted = []roomcharge.Result{}
		}
		out = CheckOutResult{Stay: toStay(done, pre.line.ReservationID), PostedRoomCharges: posted, Folios: closedFolios, Housekeeping: string(housekeeping.Dirty)}
		return nil
	})
	return out, err
}

// settleDeparture applies the departure handling of a check-out and returns the departure date the stay ends with.
func (s *Service) settleDeparture(ctx context.Context, p auth.Principal, propertyID int64, bd, localDate civil.Date, st frontdeskdb.Stay, pre stayCtx, confirmEarly bool) (civil.Date, error) {
	limit := bd // the local date is the business date: nothing past today has been consumed
	if localDate.After(bd) {
		limit = bd.AddDays(1) // after midnight, before the audit: the night of the business date was consumed
	}
	newDep := st.DepartureDate
	if newDep.After(limit) {
		newDep = limit
	}
	if !newDep.After(st.ArrivalDate) {
		newDep = st.ArrivalDate.AddDays(1) // no day-use: the night of arrival is a night
	}
	if newDep.Equal(st.DepartureDate) {
		return newDep, nil
	}
	if !confirmEarly {
		return newDep, apperr.Conflict("EARLY_DEPARTURE_NOT_CONFIRMED", "the guest leaves before the booked departure: confirm the early departure").
			WithContext("departure_date", st.DepartureDate).WithContext("new_departure_date", newDep)
	}
	q := s.q(ctx)
	last, err := q.LastPostedNight(ctx, frontdeskdb.LastPostedNightParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: st.ID})
	if err != nil {
		return newDep, err
	}
	if !last.IsZero() && !last.Before(newDep) {
		return newDep, apperr.Conflict("NIGHT_ALREADY_POSTED", "a night after the departure is already charged: reverse the charge first").WithContext("last_posted_night", last)
	}
	if err := s.res.TrimNights(ctx, propertyID, pre.line.ID, pre.line.ArrivalDate, newDep); err != nil {
		return newDep, err
	}
	if _, err := q.UpdateStayDeparture(ctx, frontdeskdb.UpdateStayDepartureParams{TenantID: p.TenantID, PropertyID: propertyID, ID: st.ID, DepartureDate: newDep, ActorID: p.ActorID()}); err != nil {
		return newDep, err
	}
	return newDep, nil
}

// checkedOutResult rebuilds the answer of a check-out that already happened.
func (s *Service) checkedOutResult(ctx context.Context, p auth.Principal, propertyID int64, st frontdeskdb.Stay) (CheckOutResult, error) {
	line, err := s.q(ctx).GetStayLine(ctx, frontdeskdb.GetStayLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: st.ReservationRoomID})
	if err != nil {
		return CheckOutResult{}, err
	}
	fs, err := s.folios.StayFolios(ctx, p.TenantID, propertyID, st.ID)
	if err != nil {
		return CheckOutResult{}, err
	}
	closed := make([]folios.ClosedFolio, len(fs))
	for i, f := range fs {
		closed[i] = folios.ClosedFolio{ID: f.ID, FolioNumber: f.FolioNumber, Status: f.Status}
	}
	return CheckOutResult{Stay: toStay(st, line.ReservationID), PostedRoomCharges: []roomcharge.Result{}, Folios: closed, Housekeeping: string(housekeeping.Dirty)}, nil
}
