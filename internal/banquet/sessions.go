package banquet

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

func checkSession(in SessionInput) []apperr.FieldError {
	var f []apperr.FieldError
	if n := len([]rune(strings.TrimSpace(in.Name))); n < 1 || n > 150 {
		f = append(f, fieldErr("name", "INVALID_VALUE", "1 to 150 characters"))
	}
	if in.SessionDate.IsZero() {
		f = append(f, fieldErr("session_date", "REQUIRED", "the day of the session"))
	}
	if (in.StartTime == nil) != (in.EndTime == nil) {
		f = append(f, fieldErr("end_time", "INVALID_VALUE", "a session has a start and an end time, or neither"))
	} else if in.StartTime != nil {
		switch {
		case in.EndsNextDay && in.StartTime.Compare(*in.EndTime) == 0:
			f = append(f, fieldErr("end_time", "INVALID_VALUE", "a session that ends the next day does not end at the time it starts"))
		case !in.EndsNextDay && !in.StartTime.Before(*in.EndTime):
			f = append(f, fieldErr("end_time", "INVALID_VALUE", "the end is after the start (or the session ends the next day)"))
		}
	} else if in.EndsNextDay {
		f = append(f, fieldErr("ends_next_day", "INVALID_VALUE", "the hours are needed"))
	}
	if in.PaxEstimated < 1 || in.PaxEstimated > maxPax {
		f = append(f, fieldErr("pax_estimated", "INVALID_VALUE", "the number of guests is above zero"))
	}
	if in.PaxGuaranteed != nil && (*in.PaxGuaranteed < 1 || *in.PaxGuaranteed > maxPax) {
		f = append(f, fieldErr("pax_guaranteed", "INVALID_VALUE", "the guaranteed guests are above zero"))
	}
	if in.SetupStyle != nil && !slices.Contains(SetupStyles, *in.SetupStyle) {
		f = append(f, fieldErr("setup_style", "SETUP_NOT_OFFERED", "an unknown style of set-up"))
	}
	return f
}

// checkSetup refuses a set-up the room does not offer and asks for a confirmation when the guests are more than the room holds in that set-up (a business decision, not an error).
func (s *Service) checkSetup(ctx context.Context, propertyID int64, roomID *int64, style *string, pax int, accept bool) error {
	if roomID == nil || style == nil {
		return nil
	}
	capacity, err := s.q(ctx).RoomCapacity(ctx, banquetdb.RoomCapacityParams{PropertyID: propertyID, RoomID: *roomID, SetupStyle: *style})
	if err != nil {
		return orNotFound(err, invalidCode("SETUP_NOT_OFFERED", "setup_style", "the room does not offer this set-up"))
	}
	if pax > int(capacity) && !accept {
		return apperr.Conflict("CAPACITY_EXCEEDED", "the guests are more than the room holds in this set-up").WithContext("capacity", capacity).WithContext("pax", pax)
	}
	return nil
}

// sessionOf reads a session of the event under its lock.
func (s *Service) sessionOf(ctx context.Context, tenantID, propertyID, eventID, sessionID int64) (Session, error) {
	list, err := s.sessions(ctx, tenantID, propertyID, eventID, &sessionID, "")
	if err != nil {
		return Session{}, err
	}
	if len(list) == 0 {
		return Session{}, notFound("SESSION_NOT_FOUND", "session")
	}
	return list[0], nil
}

func holdKindOf(status string) string {
	if status == StatusDefinite {
		return HoldDefinite
	}
	return HoldOption
}

// AddSession adds a day to an event (banquet.manage). On a tentative or definite event the session needs a room and hours and takes its hold at once.
func (s *Service) AddSession(ctx context.Context, propertyID, eventID int64, in SessionInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkSession(in); len(f) > 0 {
		return Event{}, invalid("session", f...)
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		var extra []slot
		if in.RoomID != nil && in.StartTime != nil {
			extra = append(extra, slot{roomID: *in.RoomID, date: in.SessionDate, start: *in.StartTime, end: *in.EndTime, endsNextDay: in.EndsNextDay})
		}
		hdr, err := s.lockSchedule(ctx, propertyID, eventID, extra)
		if err != nil {
			return err
		}
		if hdr.Status != StatusInquiry && hdr.Status != StatusTentative && hdr.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		if hdr.Status != StatusInquiry && (in.RoomID == nil || in.StartTime == nil) {
			return invalid("session", fieldErr("room_id", "REQUIRED", "a room and the hours are needed to hold a room"))
		}
		if err := s.checkSetup(ctx, propertyID, in.RoomID, in.SetupStyle, in.PaxEstimated, in.AcceptCapacity); err != nil {
			return err
		}
		q := s.q(ctx)
		no, err := q.NextSessionNo(ctx, banquetdb.NextSessionNoParams{PropertyID: propertyID, EventID: eventID})
		if err != nil {
			return err
		}
		id, err := q.InsertSession(ctx, banquetdb.InsertSessionParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, SessionNo: no, Name: in.Name, SessionDate: in.SessionDate,
			StartTime: in.StartTime, EndTime: in.EndTime, EndsNextDay: in.EndsNextDay, RoomID: in.RoomID, SetupStyle: in.SetupStyle, PaxEstimated: int32(in.PaxEstimated), //nolint:gosec // G115: validated
			PaxGuaranteed: int32Ptr(in.PaxGuaranteed), Notes: blankToNil(in.Notes)})
		if err != nil {
			return err
		}
		sn, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, id)
		if err != nil {
			return err
		}
		if hdr.Status != StatusInquiry {
			mode, err := s.settings(ctx, propertyID)
			if err != nil {
				return err
			}
			if err := s.placeHolds(ctx, propertyID, sn, eventID, holdKindOf(hdr.Status), mode.TentativeConflictMode); err != nil {
				return err
			}
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{}); err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.session_added", "banquet_session", id, out.Number+" / "+in.Name, nil, sn))
	})
	return out, err
}

func int32Ptr(v *int) *int32 {
	if v == nil {
		return nil
	}
	n := int32(*v) //nolint:gosec // G115: validated
	return &n
}

func sameSlot(a, b Session) bool {
	eq := func(x, y *civil.TimeOfDay) bool { return (x == nil) == (y == nil) && (x == nil || x.Compare(*y) == 0) }
	rooms := (a.RoomID == nil) == (b.RoomID == nil) && (a.RoomID == nil || *a.RoomID == *b.RoomID)
	return rooms && a.SessionDate.Equal(b.SessionDate) && eq(a.StartTime, b.StartTime) && eq(a.EndTime, b.EndTime) && a.EndsNextDay == b.EndsNextDay
}

// UpdateSession changes a session (banquet.manage). A new room, day or hours on a tentative session releases the old holds (and promotes the options queued behind them) and takes new ones, so the
// session goes to the back of the queue of its new slot; a definite session takes the exclusive hold or the change is refused (36 4.3).
func (s *Service) UpdateSession(ctx context.Context, propertyID, eventID, sessionID int64, in SessionInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if f := checkSession(in); len(f) > 0 {
		return Event{}, invalid("session", f...)
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		var extra []slot
		if in.RoomID != nil && in.StartTime != nil {
			extra = append(extra, slot{roomID: *in.RoomID, date: in.SessionDate, start: *in.StartTime, end: *in.EndTime, endsNextDay: in.EndsNextDay})
		}
		hdr, err := s.lockSchedule(ctx, propertyID, eventID, extra)
		if err != nil {
			return err
		}
		if hdr.Status != StatusInquiry && hdr.Status != StatusTentative && hdr.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		old, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, sessionID)
		if err != nil {
			return err
		}
		if old.Status != "ACTIVE" {
			return apperr.Conflict("SESSION_CANCELLED", "a cancelled session cannot be changed")
		}
		if posted, err := s.q(ctx).SessionHasPosting(ctx, banquetdb.SessionHasPostingParams{PropertyID: propertyID, SessionID: sessionID}); err != nil {
			return err
		} else if posted {
			return apperr.Conflict("SESSION_POSTED", "the session is posted: correct it with an adjustment on the folio")
		}
		if hdr.Status != StatusInquiry && (in.RoomID == nil || in.StartTime == nil) {
			return invalid("session", fieldErr("room_id", "REQUIRED", "a room and the hours are needed to hold a room"))
		}
		if err := s.checkSetup(ctx, propertyID, in.RoomID, in.SetupStyle, in.PaxEstimated, in.AcceptCapacity); err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.UpdateSession(ctx, banquetdb.UpdateSessionParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, ID: sessionID, Name: in.Name, SessionDate: in.SessionDate,
			StartTime: in.StartTime, EndTime: in.EndTime, EndsNextDay: in.EndsNextDay, RoomID: in.RoomID, SetupStyle: in.SetupStyle, PaxEstimated: int32(in.PaxEstimated), //nolint:gosec // G115: validated
			PaxGuaranteed: int32Ptr(in.PaxGuaranteed), Notes: blankToNil(in.Notes)}); err != nil {
			return err
		}
		sn, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, sessionID)
		if err != nil {
			return err
		}
		if hdr.Status != StatusInquiry && !sameSlot(old, sn) {
			mode, err := s.settings(ctx, propertyID)
			if err != nil {
				return err
			}
			if err := s.releaseHolds(ctx, p.TenantID, propertyID, eventID, &sessionID, bd); err != nil {
				return err
			}
			if err := s.placeHolds(ctx, propertyID, sn, eventID, holdKindOf(hdr.Status), mode.TentativeConflictMode); err != nil {
				return err
			}
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{}); err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.session_updated", "banquet_session", sessionID, out.Number+" / "+in.Name, old, sn))
	})
	return out, err
}

// CancelSession cancels a session and releases its holds (banquet.manage); the options queued behind it are promoted.
func (s *Service) CancelSession(ctx context.Context, propertyID, eventID, sessionID int64) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockSchedule(ctx, propertyID, eventID, nil)
		if err != nil {
			return err
		}
		if hdr.Status != StatusInquiry && hdr.Status != StatusTentative && hdr.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		old, err := s.sessionOf(ctx, p.TenantID, propertyID, eventID, sessionID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if posted, err := q.SessionHasPosting(ctx, banquetdb.SessionHasPostingParams{PropertyID: propertyID, SessionID: sessionID}); err != nil {
			return err
		} else if posted {
			return apperr.Conflict("SESSION_POSTED", "the session is posted: correct it with an adjustment on the folio")
		}
		n, err := q.CancelSession(ctx, banquetdb.CancelSessionParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, ID: sessionID})
		if err != nil {
			return err
		}
		if n == 0 {
			return apperr.Conflict("SESSION_CANCELLED", "the session is cancelled already")
		}
		if err := s.releaseHolds(ctx, p.TenantID, propertyID, eventID, &sessionID, bd); err != nil {
			return err
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{}); err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.session_cancelled", "banquet_session", sessionID, out.Number+" / "+old.Name, old, nil))
	})
	return out, err
}

// CheckAvailability is the room check before a session is saved (a dry run, banquet.manage): the holds in the way, the rank it would have in the slot and the rank it gives up when it moves.
func (s *Service) CheckAvailability(ctx context.Context, propertyID, eventID int64, in AvailabilityInput) (Availability, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Availability{}, err
	}
	var f []apperr.FieldError
	if in.RoomID < 1 {
		f = append(f, fieldErr("room_id", "REQUIRED", "the meeting room"))
	}
	if in.SessionDate.IsZero() {
		f = append(f, fieldErr("session_date", "REQUIRED", "the day of the session"))
	}
	if in.EndsNextDay && in.StartTime.Compare(in.EndTime) == 0 || !in.EndsNextDay && !in.StartTime.Before(in.EndTime) {
		f = append(f, fieldErr("end_time", "INVALID_VALUE", "the end is after the start (or the session ends the next day)"))
	}
	if len(f) > 0 {
		return Availability{}, invalid("availability", f...)
	}
	rooms, err := s.rooms(ctx, p.TenantID, propertyID, &in.RoomID, nil)
	if err != nil {
		return Availability{}, err
	}
	if len(rooms) == 0 {
		return Availability{}, notFound("BANQUET_ROOM_NOT_FOUND", "meeting room")
	}
	room := rooms[0]
	hdr, err := s.q(ctx).EventHeader(ctx, banquetdb.EventHeaderParams{PropertyID: propertyID, ID: eventID})
	if err != nil {
		return Availability{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	st, err := s.settings(ctx, propertyID)
	if err != nil {
		return Availability{}, err
	}
	var exclude int64
	if in.SessionID != nil {
		exclude = *in.SessionID
	}
	cs, err := s.conflicts(ctx, propertyID, slot{roomID: in.RoomID, date: in.SessionDate, start: in.StartTime, end: in.EndTime, endsNextDay: in.EndsNextDay}, exclude)
	if err != nil {
		return Availability{}, err
	}
	from, to := HoldRange(in.SessionDate, in.StartTime, in.EndTime, in.EndsNextDay, room.SetupMinutes, room.TeardownMinutes)
	out := Availability{Mode: st.TentativeConflictMode, Conflicts: []Conflict{}, Available: true, SetupOffered: true, HoldFrom: from.Format("2006-01-02T15:04"), HoldTo: to.Format("2006-01-02T15:04")}
	var optionIDs []int64
	for _, c := range cs {
		if c.HoldKind == HoldOption {
			optionIDs = append(optionIDs, c.SessionID)
		}
	}
	places := map[int64]int{}
	if len(optionIDs) > 0 {
		rks, err := s.q(ctx).SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: optionIDs})
		if err != nil {
			return Availability{}, err
		}
		for _, rk := range rks {
			places[rk.SessionID] = int(rk.Rank)
		}
	}
	ahead := map[int64]bool{}
	for _, c := range cs {
		same := c.EventID == eventID
		out.Conflicts = append(out.Conflicts, Conflict{EventID: c.EventID, EventNumber: c.EventNumber, EventName: c.EventName, EventStatus: c.EventStatus, SessionID: c.SessionID,
			SessionName: c.SessionName, HoldKind: c.HoldKind, RoomCode: c.AtomRoomCode, Rank: rankOf(places, c.SessionID), SameEvent: same})
		switch {
		case same:
			out.Available = false
		case hdr.Status == StatusDefinite && c.HoldKind == HoldDefinite:
			out.Available = false
		case hdr.Status != StatusDefinite && st.TentativeConflictMode == ModeReject:
			out.Available = false
		}
		if !same {
			ahead[c.SessionID] = true
		}
	}
	out.RankInNewSlot = 1 + len(ahead)
	if in.SessionID != nil && hdr.Status == StatusTentative {
		rk, err := s.q(ctx).SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: []int64{*in.SessionID}})
		if err != nil {
			return Availability{}, err
		}
		if len(rk) == 1 {
			out.RankGivenUp = ptr(int(rk[0].Rank))
		}
	}
	if in.SetupStyle != nil {
		capacity, err := s.q(ctx).RoomCapacity(ctx, banquetdb.RoomCapacityParams{PropertyID: propertyID, RoomID: in.RoomID, SetupStyle: *in.SetupStyle})
		switch {
		case err != nil:
			out.SetupOffered = false
		default:
			out.Capacity = ptr(int(capacity))
			out.CapacityExceeds = in.Pax != nil && *in.Pax > int(capacity)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------- the transitions

// MakeTentative turns an inquiry into a tentative event (banquet.manage): it needs the option date and a room and hours for every active session, and writes the option holds. In the mode REJECT a
// slot that is held is refused; in the mode QUEUE the session takes its place behind the holds.
func (s *Service) MakeTentative(ctx context.Context, propertyID, eventID int64, in TentativeInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return Event{}, err
	}
	if in.OptionUntil.IsZero() {
		return Event{}, invalidCode("OPTION_DATE_REQUIRED", "option_until", "a tentative event has an option date")
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		// an inquiry holds nothing yet: the rooms it is going to hold are the ones its sessions name
		var extra []slot
		pre, err := s.sessions(ctx, p.TenantID, propertyID, eventID, nil, "")
		if err != nil {
			return err
		}
		for _, sn := range pre {
			if sl, ok := slotOf(sn); ok && sn.Status == "ACTIVE" {
				extra = append(extra, sl)
			}
		}
		hdr, err := s.lockSchedule(ctx, propertyID, eventID, extra)
		if err != nil {
			return err
		}
		if hdr.Status != StatusInquiry {
			return apperr.Conflict("EVENT_STATUS_INVALID", "only an inquiry can become tentative").WithContext("status", hdr.Status)
		}
		if in.OptionUntil.Before(bd) {
			return apperr.Invalid("the event is invalid", fieldErr("option_until", "INVALID_VALUE", "the option date is not before the business date"))
		}
		old, err := s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		active := 0
		for _, sn := range old.Sessions {
			if sn.Status != "ACTIVE" {
				continue
			}
			active++
			if _, ok := slotOf(sn); !ok {
				return invalid("session", fieldErr("room_id", "REQUIRED", "every session needs a room, a day and the hours before the option is held: "+sn.Name))
			}
		}
		if active == 0 {
			return invalid("event", fieldErr("sessions", "REQUIRED", "the event has no session to hold a room for"))
		}
		mode, err := s.settings(ctx, propertyID)
		if err != nil {
			return err
		}
		for _, sn := range old.Sessions {
			if sn.Status == "ACTIVE" {
				if err := s.placeHolds(ctx, propertyID, sn, eventID, HoldOption, mode.TentativeConflictMode); err != nil {
					return err
				}
			}
		}
		if err := s.q(ctx).SetEventStatus(ctx, banquetdb.SetEventStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, Status: StatusTentative, OptionUntil: &in.OptionUntil, ActorID: p.ActorID()}); err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_tentative", "banquet_event", eventID, out.Number, map[string]any{"status": old.Status},
			map[string]any{"status": out.Status, "option_until": in.OptionUntil}))
	})
	return out, err
}

// Confirm turns a tentative event into a definite one (banquet.confirm): every session must be first in its queue, the holds become exclusive, and the options queued behind lose their room.
// The folio (the one a deposit opened, or a new one), the cancellation policy and the deposit schedule of the event are made here (36 4.4).
func (s *Service) Confirm(ctx context.Context, propertyID, eventID int64, in ConfirmInput) (Event, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetConfirm)
	if err != nil {
		return Event{}, err
	}
	var out Event
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		// the folio is read, not written: a deposit may have opened it, and nothing here changes it, so it is not locked (the level 42 would come after the event)
		hdr, err := s.lockSchedule(ctx, propertyID, eventID, nil)
		if err != nil {
			return err
		}
		if hdr.Status != StatusTentative {
			return apperr.Conflict("EVENT_STATUS_INVALID", "only a tentative event can be confirmed").WithContext("status", hdr.Status)
		}
		q := s.q(ctx)
		old, err := s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		var ids []int64
		for _, sn := range old.Sessions {
			if sn.Status == "ACTIVE" {
				ids = append(ids, sn.ID)
			}
		}
		if len(ids) == 0 {
			return invalid("event", fieldErr("sessions", "REQUIRED", "the event has no session"))
		}
		ranks, err := q.SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: ids})
		if err != nil {
			return err
		}
		for _, rk := range ranks {
			if rk.Rank <= 1 {
				continue
			}
			holders, err := q.EarlierHolders(ctx, banquetdb.EarlierHoldersParams{PropertyID: propertyID, SessionID: rk.SessionID})
			if err != nil {
				return err
			}
			e := apperr.Conflict("BANQUET_OPTION_NOT_FIRST", "an earlier option still holds the room at this time").WithContext("session_id", rk.SessionID).WithContext("rank", rk.Rank)
			if len(holders) > 0 {
				e = e.WithContext("holder_event_id", holders[0].EventID).WithContext("holder_event_number", holders[0].EventNumber)
			}
			return e
		}
		behind, err := q.SessionsBehind(ctx, banquetdb.SessionsBehindParams{PropertyID: propertyID, EventID: eventID})
		if err != nil {
			return err
		}
		if err := q.SetEventHoldKind(ctx, banquetdb.SetEventHoldKindParams{PropertyID: propertyID, EventID: eventID, HoldKind: HoldDefinite}); err != nil {
			return err
		}
		now := s.clock.Now()
		if err := q.SetEventStatus(ctx, banquetdb.SetEventStatusParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, Status: StatusDefinite, DefiniteAt: &now, ActorID: p.ActorID()}); err != nil {
			return err
		}
		// the options queued behind lose their room: their salespeople are told, once for each event (the holds stay, the screen flags "room taken")
		if len(behind) > 0 {
			ids := make([]int64, len(behind))
			for i, b := range behind {
				ids[i] = b.SessionID
			}
			sales, err := q.SessionEventSales(ctx, banquetdb.SessionEventSalesParams{PropertyID: propertyID, Ids: ids})
			if err != nil {
				return err
			}
			told := map[int64]bool{}
			for _, sa := range sales {
				if sa.Status != StatusTentative || told[sa.EventID] {
					continue
				}
				told[sa.EventID] = true
				sid := sa.SessionID
				if err := q.InsertNotice(ctx, banquetdb.InsertNoticeParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: sa.SalesUserID, Kind: NoticeLost, EventID: sa.EventID, SessionID: &sid, NoticeDate: bd}); err != nil {
					return err
				}
			}
		}
		if err := s.refreshEstimate(ctx, p, propertyID, bd, eventID); err != nil {
			return err
		}
		tpl, err := s.copyPolicy(ctx, p, propertyID, bd, eventID, in.PolicyTemplateID)
		if err != nil {
			return err
		}
		mh, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
		if err != nil {
			return err
		}
		f, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if f == nil {
			if f0, err := s.folios.OpenEventFolio(ctx, p, propertyID, eventID, mh.BookerCompanyID); err != nil {
				return err
			} else if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.folio_opened", "banquet_event", eventID, mh.Number, nil, map[string]any{"folio_id": f0.ID, "folio_number": f0.Number})); err != nil {
				return err
			}
		}
		if _, err := s.revise(ctx, p, propertyID, bd, eventID, reviseOpts{confirmed: true}); err != nil {
			return err
		}
		out, err = s.event(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		after := map[string]any{"status": out.Status}
		if tpl != nil {
			after["policy_template"] = tpl.Code
			after["policy_base"] = tpl.Base
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.event_confirmed", "banquet_event", eventID, out.Number, map[string]any{"status": old.Status}, after))
	})
	return out, err
}

// ---------------------------------------------------------------- calendar and notices

func hoursOf(t time.Time) civil.Date { return civil.DateOf(t) }

// Calendar answers the sessions of a period with the rooms: the grid of the rooms by the hours, with the queue rank of the options (36 9). The period defaults to a week from the business date.
func (s *Service) Calendar(ctx context.Context, propertyID int64, from, to *civil.Date) (Calendar, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return Calendar{}, err
	}
	today := s.today(ctx, propertyID)
	f := today
	if from != nil {
		f = *from
	}
	if f.IsZero() {
		f = hoursOf(s.clock.Now())
	}
	t := f.AddDays(6)
	if to != nil {
		t = *to
	}
	if t.Before(f) || f.DaysUntil(t) > 62 {
		return Calendar{}, invalid("period", fieldErr("to", "INVALID_VALUE", "the end is not before the start, and the period is up to 63 days"))
	}
	rooms, err := s.rooms(ctx, p.TenantID, propertyID, nil, ptr(true))
	if err != nil {
		return Calendar{}, err
	}
	sessions, err := s.calendarSessions(ctx, p.TenantID, propertyID, f, t, today)
	if err != nil {
		return Calendar{}, err
	}
	locks, err := s.calendarLocks(ctx, p.TenantID, propertyID, f, t)
	if err != nil {
		return Calendar{}, err
	}
	return Calendar{From: f, To: t, Rooms: rooms, Sessions: sessions, Locks: locks}, nil
}

// calendarLocks answers what the holds lock in the other rooms of the period, read from the holds as they are stored: the screens do not work it out again.
func (s *Service) calendarLocks(ctx context.Context, tenantID, propertyID int64, from, to civil.Date) ([]CalendarLock, error) {
	rows, err := s.q(ctx).CalendarLocks(ctx, banquetdb.CalendarLocksParams{TenantID: tenantID, PropertyID: propertyID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	out := make([]CalendarLock, 0, len(rows))
	for _, r := range rows {
		if r.EventStatus != StatusTentative && r.EventStatus != StatusDefinite {
			continue
		}
		out = append(out, CalendarLock{RoomID: r.RoomID, SessionID: r.SessionID, EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, EventStatus: r.EventStatus, HoldKind: r.HoldKind,
			ViaRoomID: derefID(r.ViaRoomID), ViaRoomCode: r.ViaRoomCode, ViaRoomName: r.ViaRoomName, HoldFrom: r.HoldFrom.Time.Format("2006-01-02T15:04"), HoldTo: r.HoldTo.Time.Format("2006-01-02T15:04")})
	}
	return out, nil
}

func (s *Service) calendarSessions(ctx context.Context, tenantID, propertyID int64, from, to, today civil.Date) ([]CalendarSession, error) {
	q := s.q(ctx)
	rows, err := q.CalendarSessions(ctx, banquetdb.CalendarSessionsParams{TenantID: tenantID, PropertyID: propertyID, DateFrom: from, DateTo: to})
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SessionID)
	}
	ranks, err := q.SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := make([]CalendarSession, 0, len(rows))
	for _, r := range rows {
		if r.StartTime == nil || r.EndTime == nil || r.RoomID == nil {
			continue
		}
		cs := CalendarSession{SessionID: r.SessionID, EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, EventStatus: r.EventStatus, EventTypeName: r.EventTypeName,
			BookerName: r.BookerName, SalesUserID: r.SalesUserID, OptionUntil: r.OptionUntil, OptionOverdue: s.overdue(r.EventStatus, r.OptionUntil, today), SessionName: r.SessionName,
			SessionDate: r.SessionDate, StartTime: *r.StartTime, EndTime: *r.EndTime, EndsNextDay: r.EndsNextDay, RoomID: *r.RoomID, RoomCode: r.RoomCode, RoomName: r.RoomName,
			SetupMinutes: int(r.SetupMinutes), TeardownMin: int(r.TeardownMinutes), SetupStyle: r.SetupStyle, PaxEstimated: int(r.PaxEstimated)}
		for _, rk := range ranks {
			if rk.SessionID == r.SessionID && r.EventStatus == StatusTentative {
				cs.Rank = ptr(int(rk.Rank))
				cs.RoomTaken = rk.RoomTaken
			}
		}
		out = append(out, cs)
	}
	return out, nil
}

// Today answers what the banquet office looks at first: the sessions of the business date and the options whose date has passed.
func (s *Service) Today(ctx context.Context, propertyID int64) (Today, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return Today{}, err
	}
	day := s.today(ctx, propertyID)
	if day.IsZero() {
		return Today{Sessions: []CalendarSession{}, Overdue: []OverdueOption{}}, nil
	}
	all, err := s.calendarSessions(ctx, p.TenantID, propertyID, day, day, day)
	if err != nil {
		return Today{}, err
	}
	sessions := []CalendarSession{}
	for _, cs := range all {
		if cs.SessionDate.Equal(day) {
			sessions = append(sessions, cs)
		}
	}
	rows, err := s.q(ctx).OverdueOptions(ctx, banquetdb.OverdueOptionsParams{TenantID: p.TenantID, PropertyID: propertyID, Today: day})
	if err != nil {
		return Today{}, err
	}
	od := make([]OverdueOption, 0, len(rows))
	for _, r := range rows {
		if r.OptionUntil != nil {
			od = append(od, OverdueOption{EventID: r.ID, Number: r.Number, Name: r.Name, OptionUntil: *r.OptionUntil, SalesUserID: r.SalesUserID, SalesName: r.SalesName})
		}
	}
	return Today{Date: day, Sessions: sessions, Overdue: od}, nil
}

// ListNotices answers the bell of the caller: the notices written for them.
func (s *Service) ListNotices(ctx context.Context, propertyID int64, unseenOnly bool) ([]Notice, error) {
	p, err := s.canRead(ctx, propertyID, allBanquet...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListNotices(ctx, banquetdb.ListNoticesParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, UnseenOnly: unseenOnly, RowLimit: 100})
	if err != nil {
		return nil, err
	}
	out := make([]Notice, len(rows))
	for i, r := range rows {
		out[i] = Notice{ID: r.ID, Kind: r.Kind, EventID: r.EventID, EventNumber: r.EventNumber, EventName: r.EventName, SessionID: r.SessionID, NoticeDate: r.NoticeDate, CreatedAt: r.CreatedAt, SeenAt: r.SeenAt}
		if r.RevisionNo != nil {
			out[i].RevisionNo = ptr(int(*r.RevisionNo))
		}
	}
	return out, nil
}

// MarkNoticesSeen marks the notices of the caller as seen: the ids given, or all of them.
func (s *Service) MarkNoticesSeen(ctx context.Context, propertyID int64, ids []int64) (int64, error) {
	p, err := s.canRead(ctx, propertyID, allBanquet...)
	if err != nil {
		return 0, err
	}
	if ids == nil {
		ids = []int64{}
	}
	return s.q(ctx).MarkNoticesSeen(ctx, banquetdb.MarkNoticesSeenParams{TenantID: p.TenantID, PropertyID: propertyID, UserID: p.UserID, Ids: ids})
}

// WriteDueNotices is the day close (36 F9): a reminder for the salesperson of each tentative event whose option date has come, once for the event and the day. It writes in the transaction of the
// close and returns the number of events reminded.
func (s *Service) WriteDueNotices(ctx context.Context, tenantID, propertyID int64, day civil.Date) (int, error) {
	q := s.q(ctx)
	ids, err := q.InsertDueNotices(ctx, banquetdb.InsertDueNoticesParams{TenantID: tenantID, PropertyID: propertyID, NoticeDate: day})
	if err != nil {
		return 0, err
	}
	n, err := s.writeDepositNotices(ctx, tenantID, propertyID, day)
	return len(ids) + n, err
}

// writeDepositNotices is the reminder of a deposit (36 4.7): for each tentative or definite event with a row of the schedule that is due and not covered by the deposits received, a notice for its
// salesperson, once for the event and the day.
func (s *Service) writeDepositNotices(ctx context.Context, tenantID, propertyID int64, day civil.Date) (int, error) {
	q := s.q(ctx)
	evs, err := q.ScheduledEventsDue(ctx, banquetdb.ScheduledEventsDueParams{TenantID: tenantID, PropertyID: propertyID, Day: day})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range evs {
		rows, err := s.schedule(ctx, propertyID, e.EventID)
		if err != nil {
			return 0, err
		}
		received := zeroDec
		if f, err := s.folios.EventFolioOf(ctx, tenantID, propertyID, e.EventID); err != nil {
			return 0, err
		} else if f != nil {
			if received, err = s.folios.EventDeposits(ctx, propertyID, f.ID); err != nil {
				return 0, err
			}
		}
		for i, r := range rows {
			if !r.DueDate.After(day) && RowStatus(rows, i, received, day) != "PAID" {
				if err := q.InsertDepositNotice(ctx, banquetdb.InsertDepositNoticeParams{TenantID: tenantID, PropertyID: propertyID, UserID: e.SalesUserID, EventID: e.EventID, NoticeDate: day}); err != nil {
					return 0, err
				}
				n++
				break
			}
		}
	}
	return n, nil
}

var zeroDec = decimal.Zero

func derefID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

// rankOf is the place of a session in the map, or nothing for a session that is not an option.
func rankOf(places map[int64]int, sessionID int64) *int {
	if r, ok := places[sessionID]; ok {
		return &r
	}
	return nil
}
