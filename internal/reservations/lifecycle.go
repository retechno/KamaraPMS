package reservations

import (
	"context"
	"slices"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations/reservationsdb"
)

func (s *Service) saveLine(ctx context.Context, p auth.Principal, propertyID int64, l reservationsdb.ReservationRoom) (reservationsdb.ReservationRoom, error) {
	return s.q(ctx).UpdateLine(ctx, reservationsdb.UpdateLineParams{
		TenantID: p.TenantID, PropertyID: propertyID, ID: l.ID, GuestID: l.GuestID, RoomTypeID: l.RoomTypeID, RoomID: l.RoomID,
		RatePlanID: l.RatePlanID, ArrivalDate: l.ArrivalDate, DepartureDate: l.DepartureDate, AdultCount: l.AdultCount, ChildCount: l.ChildCount,
		Status: l.Status, CancelledAt: l.CancelledAt, CancelledBy: l.CancelledBy, CancellationReason: l.CancellationReason,
		NoShowAt: l.NoShowAt, NoShowBy: l.NoShowBy, ActorID: p.ActorID(),
	})
}

func (s *Service) bump(ctx context.Context, p auth.Principal, propertyID, id int64) (reservationsdb.Reservation, error) {
	return s.q(ctx).BumpReservation(ctx, reservationsdb.BumpReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, ActorID: p.ActorID()})
}

func notCancelled(st state) *apperr.Error {
	if st.res.Status == StatusCancelled {
		return apperr.Conflict("RESERVATION_CANCELLED", "the reservation is cancelled")
	}
	return nil
}

// UpdateHeader edits header fields (reservation.update).
func (s *Service) UpdateHeader(ctx context.Context, propertyID, id int64, patch HeaderPatch) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationUpdate)
	if err != nil {
		return Reservation{}, err
	}
	if err := requireVersion(patch.Version); err != nil {
		return Reservation{}, err
	}
	var out Reservation
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, patch.Version, lockOpts{})
		if err != nil {
			return err
		}
		if e := notCancelled(st); e != nil {
			return e
		}
		next := st.res
		var fields []apperr.FieldError
		if patch.Source != nil {
			fields = append(fields, validateSource(*patch.Source, "source")...)
			next.Source = *patch.Source
		}
		if patch.Market != nil {
			fields = append(fields, validateText("market", *patch.Market, 30)...)
			next.Market = nullable(*patch.Market)
		}
		if patch.SpecialRequest != nil {
			fields = append(fields, validateText("special_request", *patch.SpecialRequest, maxTextLen)...)
			next.SpecialRequest = nullable(*patch.SpecialRequest)
		}
		if patch.Remarks != nil {
			fields = append(fields, validateText("remarks", *patch.Remarks, maxTextLen)...)
			next.Remarks = nullable(*patch.Remarks)
		}
		if len(fields) > 0 {
			return apperr.Invalid("the reservation is invalid", fields...)
		}
		if patch.GuestID != nil {
			if err := s.requireGuest(ctx, patch.GuestID); err != nil {
				return err
			}
			next.GuestID = patch.GuestID
		}
		next.CompanyID, next.BookingGroupID = st.res.CompanyID, st.res.BookingGroupID
		if patch.CompanyID != nil || patch.BookingGroupID != nil {
			company, group := next.CompanyID, next.BookingGroupID
			if patch.CompanyID != nil {
				company = patch.CompanyID
			}
			if patch.BookingGroupID != nil {
				group = patch.BookingGroupID
			}
			rows, err := s.q(ctx).ListLines(ctx, reservationsdb.ListLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: id})
			if err != nil {
				return err
			}
			var lines []LineInput
			for _, r := range rows {
				if r.Status != LineCancelled {
					lines = append(lines, LineInput{Arrival: r.ArrivalDate, Departure: r.DepartureDate})
				}
			}
			if next.CompanyID, next.BookingGroupID, err = s.resolveLinks(ctx, p.TenantID, propertyID, company, group, lines); err != nil {
				return err
			}
		}
		res, err := s.q(ctx).UpdateReservationHeader(ctx, reservationsdb.UpdateReservationHeaderParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, GuestID: next.GuestID, Source: next.Source, Market: next.Market,
			SpecialRequest: next.SpecialRequest, Remarks: next.Remarks, CompanyID: next.CompanyID, BookingGroupID: next.BookingGroupID, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.updated", id,
			map[string]any{"guest_id": st.res.GuestID, "source": st.res.Source, "market": deref(st.res.Market), "company_id": st.res.CompanyID, "booking_group_id": st.res.BookingGroupID},
			map[string]any{"guest_id": res.GuestID, "source": res.Source, "market": deref(res.Market), "company_id": res.CompanyID, "booking_group_id": res.BookingGroupID})); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// Confirm turns a DRAFT reservation into CONFIRMED (reservation.create): all its DRAFT lines become
// CONFIRMED, or none does (409 ROOM_TYPE_NOT_AVAILABLE / ROOM_NOT_AVAILABLE).
func (s *Service) Confirm(ctx context.Context, propertyID, id int64, version int32) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCreate)
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
		if st.res.Status != StatusDraft {
			return apperr.Conflict("RESERVATION_NOT_DRAFT", "only a draft reservation can be confirmed").WithContext("status", st.res.Status)
		}
		if st.res.GuestID == nil {
			return apperr.Invalid("a booker is required", fieldErr("guest_id", "BOOKER_REQUIRED", "set the booker before confirming"))
		}
		holds, drafts, err := s.draftHolds(ctx, p.TenantID, propertyID, st)
		if err != nil {
			return err
		}
		if len(drafts) == 0 {
			return apperr.Conflict("NO_ROOMS_TO_CONFIRM", "the reservation has no draft room")
		}
		if err := s.checkHolds(ctx, p.TenantID, propertyID, st.bd, holds, nil); err != nil {
			return err
		}
		for _, l := range drafts {
			l.Status = LineConfirmed
			if _, err := s.saveLine(ctx, p, propertyID, l); err != nil {
				return err
			}
		}
		res, err := s.q(ctx).ConfirmReservation(ctx, reservationsdb.ConfirmReservationParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: s.clock.Now(), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.confirmed", id,
			map[string]any{"status": st.res.Status}, map[string]any{"status": res.Status, "rooms": len(drafts)})); err != nil {
			return err
		}
		if err := s.confirmed(ctx, p, propertyID, id, st.bd); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// draftHolds lists the DRAFT lines of a reservation as holds, and the lines themselves. Every DRAFT line
// must arrive on or after the business date.
func (s *Service) draftHolds(ctx context.Context, tenantID, propertyID int64, st state) ([]hold, []reservationsdb.ReservationRoom, error) {
	var holds []hold
	var drafts []reservationsdb.ReservationRoom
	typeIDs := make([]int64, 0, len(st.lines))
	for _, l := range st.lines {
		if l.Status == LineDraft {
			typeIDs = append(typeIDs, l.RoomTypeID)
		}
	}
	types, err := s.bookingTypes(ctx, tenantID, propertyID, typeIDs)
	if err != nil {
		return nil, nil, err
	}
	for _, l := range st.lines {
		if l.Status != LineDraft {
			continue
		}
		if l.ArrivalDate.Before(st.bd) {
			return nil, nil, apperr.Conflict("ARRIVAL_IN_THE_PAST", "a room arrives before the business date").
				WithContext("reservation_room_id", l.ID).WithContext("arrival_date", l.ArrivalDate)
		}
		if err := requireActiveType(types[l.RoomTypeID]); err != nil {
			return nil, nil, err
		}
		drafts = append(drafts, l)
		holds = append(holds, hold{lineID: l.ID, typeID: st.effType(l), roomID: l.RoomID, from: l.ArrivalDate, to: l.DepartureDate})
	}
	return holds, drafts, nil
}

// Cancel cancels the whole reservation (reservation.cancel). It is rejected once any room is checked in or
// completed. The result says what is left on the folios (a deposit to refund, a fee to post).
func (s *Service) Cancel(ctx context.Context, propertyID, id int64, version int32, reason string) (CancelResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCancel)
	if err != nil {
		return CancelResult{}, err
	}
	if err := requireVersion(version); err != nil {
		return CancelResult{}, err
	}
	if reason, err = requireReason(reason); err != nil {
		return CancelResult{}, err
	}
	var out CancelResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, version, lockOpts{})
		if err != nil {
			return err
		}
		if e := notCancelled(st); e != nil {
			return e
		}
		now := s.clock.Now()
		cancelled := 0
		for _, l := range st.lines {
			switch l.Status {
			case LineCheckedIn, LineCompleted:
				return apperr.Conflict("RESERVATION_HAS_STAYS", "a room is checked in or already checked out").WithContext("reservation_room_id", l.ID)
			case LineDraft, LineConfirmed:
				l.Status, l.CancelledAt, l.CancelledBy, l.CancellationReason, l.RoomID = LineCancelled, &now, p.ActorID(), &reason, nil
				if _, err := s.saveLine(ctx, p, propertyID, l); err != nil {
					return err
				}
				cancelled++
			}
		}
		res, err := s.q(ctx).CancelReservation(ctx, reservationsdb.CancelReservationParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: now, Reason: &reason, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.cancelled", id,
			map[string]any{"status": st.res.Status}, map[string]any{"status": res.Status, "reason": reason, "rooms_cancelled": cancelled})); err != nil {
			return err
		}
		out, err = s.cancelResult(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

func (s *Service) cancelResult(ctx context.Context, tenantID, propertyID int64, res reservationsdb.Reservation) (CancelResult, error) {
	view, err := s.load(ctx, tenantID, propertyID, res)
	if err != nil {
		return CancelResult{}, err
	}
	balance, open := folioTotals(view.Folios)
	return CancelResult{Reservation: view, FolioBalance: balance, RequiresFolioResolution: view.Status == StatusCancelled && open}, nil
}

// CancelLine cancels one room (reservation.cancel). When it was the last room that is not cancelled, the
// reservation is cancelled with it.
func (s *Service) CancelLine(ctx context.Context, propertyID, id, lineID int64, version int32, reason string) (CancelResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCancel)
	if err != nil {
		return CancelResult{}, err
	}
	if err := requireVersion(version); err != nil {
		return CancelResult{}, err
	}
	if reason, err = requireReason(reason); err != nil {
		return CancelResult{}, err
	}
	var out CancelResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, version, lockOpts{})
		if err != nil {
			return err
		}
		if e := notCancelled(st); e != nil {
			return e
		}
		line, ok := st.line(lineID)
		if !ok {
			return errLineNotFound()
		}
		if line.Status != LineDraft && line.Status != LineConfirmed {
			return apperr.Conflict("LINE_NOT_CANCELLABLE", "only a draft or confirmed room can be cancelled").WithContext("status", line.Status)
		}
		now := s.clock.Now()
		line.Status, line.CancelledAt, line.CancelledBy, line.CancellationReason, line.RoomID = LineCancelled, &now, p.ActorID(), &reason, nil
		if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
			return err
		}
		all := true
		for _, l := range st.lines {
			all = all && (l.ID == lineID || l.Status == LineCancelled)
		}
		var res reservationsdb.Reservation
		if all {
			res, err = s.q(ctx).CancelReservation(ctx, reservationsdb.CancelReservationParams{
				TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: now, Reason: &reason, ActorID: p.ActorID(),
			})
		} else {
			res, err = s.bump(ctx, p, propertyID, id)
		}
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.room_cancelled", id,
			map[string]any{"reservation_room_id": lineID, "status": line.Status}, map[string]any{"status": LineCancelled, "reason": reason, "reservation_cancelled": all})); err != nil {
			return err
		}
		out, err = s.cancelResult(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// Reinstate brings a cancelled reservation back (reservation.reinstate): the rooms that were cancelled with
// it become CONFIRMED again if they still arrive on or after the business date and still fit the inventory.
// Rooms cancelled on their own earlier stay cancelled.
func (s *Service) Reinstate(ctx context.Context, propertyID, id int64, version int32) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationReinstate)
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
		if st.res.Status != StatusCancelled {
			return apperr.Conflict("RESERVATION_NOT_CANCELLED", "only a cancelled reservation can be reinstated")
		}
		if st.res.GuestID == nil {
			return apperr.Invalid("a booker is required", fieldErr("guest_id", "BOOKER_REQUIRED", "set the booker before reinstating"))
		}
		var back []reservationsdb.ReservationRoom
		var holds []hold
		typeIDs := []int64{}
		for _, l := range st.lines {
			if l.Status == LineCancelled && l.CancelledAt != nil && st.res.CancelledAt != nil && l.CancelledAt.Equal(*st.res.CancelledAt) {
				back = append(back, l)
				typeIDs = append(typeIDs, l.RoomTypeID)
			}
		}
		if len(back) == 0 {
			return apperr.Conflict("NOTHING_TO_REINSTATE", "no room was cancelled together with the reservation")
		}
		types, err := s.bookingTypes(ctx, p.TenantID, propertyID, typeIDs)
		if err != nil {
			return err
		}
		for _, l := range back {
			if l.ArrivalDate.Before(st.bd) {
				return apperr.Conflict("ARRIVAL_IN_THE_PAST", "a room arrives before the business date").
					WithContext("reservation_room_id", l.ID).WithContext("arrival_date", l.ArrivalDate)
			}
			if err := requireActiveType(types[l.RoomTypeID]); err != nil {
				return err
			}
			holds = append(holds, hold{lineID: l.ID, typeID: l.RoomTypeID, from: l.ArrivalDate, to: l.DepartureDate})
		}
		if err := s.checkHolds(ctx, p.TenantID, propertyID, st.bd, holds, nil); err != nil {
			return err
		}
		for _, l := range back {
			l.Status, l.CancelledAt, l.CancelledBy, l.CancellationReason = LineConfirmed, nil, nil, nil
			if _, err := s.saveLine(ctx, p, propertyID, l); err != nil {
				return err
			}
		}
		res, err := s.q(ctx).ConfirmReservation(ctx, reservationsdb.ConfirmReservationParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Now: s.clock.Now(), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.reinstated", id,
			map[string]any{"status": st.res.Status}, map[string]any{"status": res.Status, "rooms": len(back)})); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// NoShow marks a CONFIRMED room whose arrival date has come as a no-show (nightaudit.no_show). It releases
// the room's inventory. A fee, if any, is posted explicitly through the folio.
func (s *Service) NoShow(ctx context.Context, propertyID, id, lineID int64, version int32, reason string) (Reservation, error) {
	p, err := s.writer(ctx, propertyID, auth.PermNightAuditNoShow)
	if err != nil {
		return Reservation{}, err
	}
	if err := requireVersion(version); err != nil {
		return Reservation{}, err
	}
	if len([]rune(reason)) > maxReasonLen {
		return Reservation{}, apperr.Invalid("the reason is too long", fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	var out Reservation
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		st, err := s.lock(ctx, p.TenantID, propertyID, id, version, lockOpts{})
		if err != nil {
			return err
		}
		line, ok := st.line(lineID)
		if !ok {
			return errLineNotFound()
		}
		if line.Status != LineConfirmed {
			return apperr.Conflict("LINE_NOT_CONFIRMED", "only a confirmed room can be a no-show").WithContext("status", line.Status)
		}
		if line.ArrivalDate.After(st.bd) {
			return apperr.Conflict("ARRIVAL_NOT_DUE", "the room does not arrive yet").WithContext("arrival_date", line.ArrivalDate)
		}
		now := s.clock.Now()
		line.Status, line.NoShowAt, line.NoShowBy = LineNoShow, &now, p.ActorID()
		if _, err := s.saveLine(ctx, p, propertyID, line); err != nil {
			return err
		}
		res, err := s.bump(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, st.bd, "reservation.no_show", id,
			map[string]any{"reservation_room_id": lineID, "status": LineConfirmed}, map[string]any{"status": LineNoShow, "reason": reason})); err != nil {
			return err
		}
		out, err = s.load(ctx, p.TenantID, propertyID, res)
		return err
	})
	return out, err
}

// ChangedLine is a line of a bulk no-show that is no longer eligible.
type ChangedLine struct {
	ReservationRoomID int64  `json:"reservation_room_id"`
	Status            string `json:"status,omitempty"`
	Reason            string `json:"reason"`
}

// NoShowMarked is a line a bulk no-show marked.
type NoShowMarked struct {
	ReservationRoomID  int64  `json:"reservation_room_id"`
	ReservationID      int64  `json:"reservation_id"`
	ConfirmationNumber string `json:"confirmation_number"`
}

// BulkNoShow marks exactly the given CONFIRMED lines whose arrival is due as NO_SHOW, in one transaction
// (nightaudit.no_show). It never expands the set: if any id is missing, no longer CONFIRMED or not due, nothing
// changes and the answer is 409 NO_SHOW_SET_CHANGED listing those lines. Lock order: business day (share),
// reservations, lines, both ascending. A fee, if any, is posted explicitly through the folio.
func (s *Service) BulkNoShow(ctx context.Context, propertyID int64, bd civil.Date, lineIDs []int64, reason string) ([]NoShowMarked, error) {
	p, err := s.writer(ctx, propertyID, auth.PermNightAuditNoShow)
	if err != nil {
		return nil, err
	}
	ids := uniqueSorted(lineIDs)
	var fields []apperr.FieldError
	if len(ids) == 0 {
		fields = append(fields, fieldErr("reservation_room_ids", "REQUIRED", "at least one room line"))
	}
	if len(ids) > maxBulkNoShow {
		fields = append(fields, fieldErr("reservation_room_ids", "TOO_MANY", "at most 500 lines"))
	}
	if len([]rune(reason)) > maxReasonLen {
		fields = append(fields, fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid("the no-show request is invalid", fields...)
	}
	var out []NoShowMarked
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, &bd) // L1, and the date the staff saw
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := q.ListLinesByIDs(ctx, reservationsdb.ListLinesByIDsParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
		if err != nil {
			return err
		}
		var headers, lines []int64
		for _, l := range pre {
			headers = append(headers, l.ReservationID)
			lines = append(lines, l.ID)
		}
		headers = uniqueSorted(headers)
		if err := db.LockRows(ctx, db.Reservations, db.ForUpdate, propertyID, headers); err != nil { // L4
			return mapNotFound(err, errNotFound())
		}
		if err := db.LockRows(ctx, db.ReservationRooms, db.ForUpdate, propertyID, lines); err != nil {
			return mapNotFound(err, errLineNotFound())
		}
		cur, err := q.ListLinesByIDs(ctx, reservationsdb.ListLinesByIDsParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
		if err != nil {
			return err
		}
		byID := map[int64]reservationsdb.ReservationRoom{}
		for _, l := range cur {
			byID[l.ID] = l
		}
		var changed []ChangedLine
		for _, id := range ids {
			l, ok := byID[id]
			switch {
			case !ok:
				changed = append(changed, ChangedLine{ReservationRoomID: id, Reason: "NOT_FOUND"})
			case l.Status != LineConfirmed:
				changed = append(changed, ChangedLine{ReservationRoomID: id, Status: l.Status, Reason: "NOT_CONFIRMED"})
			case l.ArrivalDate.After(day.BusinessDate):
				changed = append(changed, ChangedLine{ReservationRoomID: id, Status: l.Status, Reason: "ARRIVAL_NOT_DUE"})
			}
		}
		if len(changed) > 0 {
			return apperr.Conflict("NO_SHOW_SET_CHANGED", "some of the rooms are no longer unresolved arrivals; refresh the list").WithContext("changed", changed)
		}
		now := s.clock.Now()
		bumped := map[int64]bool{}
		for _, id := range ids {
			l := byID[id]
			l.Status, l.NoShowAt, l.NoShowBy = LineNoShow, &now, p.ActorID()
			if _, err := s.saveLine(ctx, p, propertyID, l); err != nil {
				return err
			}
			res, err := s.q(ctx).GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.ReservationID})
			if err != nil {
				return err
			}
			if !bumped[l.ReservationID] {
				if _, err := s.bump(ctx, p, propertyID, l.ReservationID); err != nil {
					return err
				}
				bumped[l.ReservationID] = true
			}
			if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "reservation.no_show", l.ReservationID,
				map[string]any{"reservation_room_id": id, "status": LineConfirmed}, map[string]any{"status": LineNoShow, "reason": reason, "bulk": true})); err != nil {
				return err
			}
			out = append(out, NoShowMarked{ReservationRoomID: id, ReservationID: l.ReservationID, ConfirmationNumber: res.ConfirmationNumber})
		}
		return nil
	})
	return out, err
}

func uniqueSorted(in []int64) []int64 {
	out := slices.Clone(in)
	slices.Sort(out)
	return slices.Compact(out)
}
