package banquet

import (
	"context"
	"fmt"
	"slices"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// slot is a room at a time, as a session asks for it.
type slot struct {
	roomID      int64
	date        civil.Date
	start, end  civil.TimeOfDay
	endsNextDay bool
}

func slotOf(sn Session) (slot, bool) {
	if sn.RoomID == nil || sn.StartTime == nil || sn.EndTime == nil {
		return slot{}, false
	}
	return slot{roomID: *sn.RoomID, date: sn.SessionDate, start: *sn.StartTime, end: *sn.EndTime, endsNextDay: sn.EndsNextDay}, true
}

func (s *Service) conflicts(ctx context.Context, propertyID int64, sl slot, excludeSession int64) ([]banquetdb.ConflictingHoldsRow, error) {
	return s.q(ctx).ConflictingHolds(ctx, banquetdb.ConflictingHoldsParams{PropertyID: propertyID, RoomID: sl.roomID, SessionDate: sl.date, StartTime: sl.start, EndTime: sl.end,
		EndsNextDay: sl.endsNextDay, ExcludeSessionID: excludeSession})
}

// roomSet is the rooms a use case locks: the base rooms the event holds or would hold, and the rooms its sessions are booked on (a combined room is locked with its parts).
func (s *Service) roomSet(ctx context.Context, propertyID, eventID int64, extra []slot) ([]int64, error) {
	q := s.q(ctx)
	atoms, err := q.EventAtomRooms(ctx, banquetdb.EventAtomRoomsParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	rooms := slices.Clone(atoms)
	sess, err := s.sessionRooms(ctx, propertyID, eventID)
	if err != nil {
		return nil, err
	}
	rooms = append(rooms, sess...)
	for _, sl := range extra {
		a, err := q.AtomRooms(ctx, banquetdb.AtomRoomsParams{PropertyID: propertyID, RoomID: sl.roomID})
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, a...)
		rooms = append(rooms, sl.roomID)
	}
	slices.Sort(rooms)
	return slices.Compact(rooms), nil
}

func (s *Service) sessionRooms(ctx context.Context, propertyID, eventID int64) ([]int64, error) {
	rows, err := s.txm.DB(ctx).Query(ctx, "SELECT DISTINCT room_id FROM banquet_sessions WHERE property_id = $1 AND event_id = $2 AND status = 'ACTIVE' AND room_id IS NOT NULL", propertyID, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// lockSchedule takes the locks of a use case that holds rooms, in the order of 36 6: the group of the event (share), the base rooms ascending, then the events ascending, this one and the ones
// that queue with it. An inquiry holds nothing, so it takes its event only. It answers the header of the event as it is under the lock.
func (s *Service) lockSchedule(ctx context.Context, propertyID, eventID int64, extra []slot) (banquetdb.EventHeaderRow, error) {
	q := s.q(ctx)
	hdr, err := q.EventHeader(ctx, banquetdb.EventHeaderParams{PropertyID: propertyID, ID: eventID})
	if err != nil {
		return hdr, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	if hdr.BookingGroupID != nil {
		if err := db.LockRows(ctx, db.Groups, db.ForShare, propertyID, []int64{*hdr.BookingGroupID}); err != nil {
			return hdr, err
		}
	}
	events := []int64{eventID}
	var lockedRooms []int64
	if hdr.Status != StatusInquiry || len(extra) > 0 {
		rooms, err := s.roomSet(ctx, propertyID, eventID, extra)
		if err != nil {
			return hdr, err
		}
		lockedRooms = rooms
		if err := db.LockRows(ctx, db.BanquetRooms, db.ForUpdate, propertyID, rooms); err != nil {
			return hdr, err
		}
		// the rooms are locked: the holds on them cannot change, so the events that queue with this one can be read and locked
		behind, err := q.EventsBehind(ctx, banquetdb.EventsBehindParams{PropertyID: propertyID, EventID: eventID})
		if err != nil {
			return hdr, err
		}
		events = append(events, behind...)
		for _, sl := range extra {
			cs, err := s.conflicts(ctx, propertyID, sl, 0)
			if err != nil {
				return hdr, err
			}
			for _, c := range cs {
				events = append(events, c.EventID)
			}
		}
	}
	if err := db.LockRows(ctx, db.BanquetEvents, db.ForUpdate, propertyID, events); err != nil {
		return hdr, err
	}
	// what the event held may have changed between the read and the lock: the rooms must still be inside the ones that were locked
	again, err := q.EventHeader(ctx, banquetdb.EventHeaderParams{PropertyID: propertyID, ID: eventID})
	if err != nil {
		return hdr, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	if again.Status != StatusInquiry || len(extra) > 0 {
		now, err := s.roomSet(ctx, propertyID, eventID, extra)
		if err != nil {
			return again, err
		}
		for _, r := range now {
			if !slices.Contains(lockedRooms, r) {
				return again, apperr.Conflict("VERSION_CONFLICT", "the event was changed by someone else: try again").WithContext("current_version", again.Version)
			}
		}
	}
	return again, nil
}

func unavailable(c banquetdb.ConflictingHoldsRow, same bool) *apperr.Error {
	msg := fmt.Sprintf("the meeting room is held at this time by event %s", c.EventNumber)
	if same {
		msg = "two sessions of the same event cannot hold a room at the same time"
	}
	return apperr.Conflict("BANQUET_ROOM_NOT_AVAILABLE", msg).WithContext("event_id", c.EventID).WithContext("event_number", c.EventNumber).
		WithContext("event_name", c.EventName).WithContext("room", c.AtomRoomCode)
}

// placeHolds writes the holds of a session (36 4.2, 4.3). A session never overlaps another session of its own event. A definite hold meets any live hold of someone else as a refusal (the exclusion
// constraint is the last word, for a race); an option meets it as a refusal in the mode REJECT and as a place in the queue in the mode QUEUE.
func (s *Service) placeHolds(ctx context.Context, propertyID int64, sn Session, eventID int64, kind, mode string) error {
	sl, ok := slotOf(sn)
	if !ok {
		return invalid("session", fieldErr("room_id", "REQUIRED", "a room, a date and the hours are needed to hold a room"))
	}
	cs, err := s.conflicts(ctx, propertyID, sl, sn.ID)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.EventID == eventID {
			return unavailable(c, true)
		}
	}
	for _, c := range cs {
		if kind == HoldDefinite && c.HoldKind == HoldDefinite {
			return unavailable(c, false)
		}
		if kind == HoldOption && mode == ModeReject {
			return unavailable(c, false)
		}
	}
	return s.q(ctx).InsertSessionHolds(ctx, banquetdb.InsertSessionHoldsParams{PropertyID: propertyID, SessionID: sn.ID, HoldKind: kind})
}

// releaseHolds deletes the holds of a session (sessionID) or of the whole event and promotes the options that were queued behind them: a session whose rank was above 1 and is 1 now is
// promoted, and the salesperson of its event gets a notice in the same transaction (36 4.3).
func (s *Service) releaseHolds(ctx context.Context, tenantID, propertyID, eventID int64, sessionID *int64, day civil.Date) error {
	q := s.q(ctx)
	behind, err := q.SessionsBehind(ctx, banquetdb.SessionsBehindParams{PropertyID: propertyID, EventID: eventID, SessionID: sessionID})
	if err != nil {
		return err
	}
	ids := make([]int64, len(behind))
	for i, b := range behind {
		ids[i] = b.SessionID
	}
	before, err := q.SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: ids})
	if err != nil {
		return err
	}
	if sessionID != nil {
		err = q.DeleteSessionHolds(ctx, banquetdb.DeleteSessionHoldsParams{PropertyID: propertyID, SessionID: *sessionID})
	} else {
		err = q.DeleteEventHolds(ctx, banquetdb.DeleteEventHoldsParams{PropertyID: propertyID, EventID: eventID})
	}
	if err != nil || len(ids) == 0 {
		return err
	}
	after, err := q.SessionRanks(ctx, banquetdb.SessionRanksParams{PropertyID: propertyID, Ids: ids})
	if err != nil {
		return err
	}
	sales, err := q.SessionEventSales(ctx, banquetdb.SessionEventSalesParams{PropertyID: propertyID, Ids: ids})
	if err != nil {
		return err
	}
	for _, a := range after {
		if a.Rank != 1 {
			continue
		}
		wasQueued := false
		for _, b := range before {
			if b.SessionID == a.SessionID && b.Rank > 1 {
				wasQueued = true
			}
		}
		if !wasQueued {
			continue
		}
		for _, sa := range sales {
			if sa.SessionID == a.SessionID && sa.Status == StatusTentative {
				sid := a.SessionID
				if err := q.InsertNotice(ctx, banquetdb.InsertNoticeParams{TenantID: tenantID, PropertyID: propertyID, UserID: sa.SalesUserID, Kind: NoticePromoted, EventID: sa.EventID, SessionID: &sid, NoticeDate: day}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
