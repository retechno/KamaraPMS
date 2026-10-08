package frontdesk

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk/frontdeskdb"
	"kamarapms/internal/guests"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/reservations"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/tenancy"
)

// Service is the front desk application service: check-in (CheckInService), walk-in (WalkInService) and reverse
// check-in. Locks follow docs/architecture/05-transactions-locking.md row 3: business day (share), room types,
// rooms, reservation and lines, stays, folios, and sequences last.
type Service struct {
	txm     *db.TxManager
	clock   clock.Clock
	audit   *audit.Writer
	authz   auth.Authorizer
	days    *tenancy.Service
	avail   *availability.Service
	guests  *guests.Service
	hk      *housekeeping.Service
	res     *reservations.Service
	folios  *folios.Service
	charges *roomcharge.Service
}

// NewService wires the front desk service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, avail *availability.Service,
	g *guests.Service, hk *housekeeping.Service, r *reservations.Service, f *folios.Service, rc *roomcharge.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, avail: avail, guests: g, hk: hk, res: r, folios: f, charges: rc}
}

func (s *Service) q(ctx context.Context) *frontdeskdb.Queries { return frontdeskdb.New(s.txm.DB(ctx)) }

func errStayNotFound() *apperr.Error {
	return apperr.NotFound("STAY_NOT_FOUND", "the stay does not exist in this property")
}

func errRoomNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
}

func notFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

func mapNotFound(err error, nf *apperr.Error) error {
	if apperr.IsCode(err, "NOT_FOUND") {
		return nf
	}
	return err
}

func (s *Service) actor(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: "stay", EntityID: id, Old: old, New: updated,
	}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}

func guestName(first *string, last string) string {
	if first != nil && *first != "" {
		return *first + " " + last
	}
	return last
}

func toStay(s frontdeskdb.Stay, reservationID int64) Stay {
	return Stay{
		ID: s.ID, StayNumber: s.StayNumber, ReservationID: reservationID, ReservationRoomID: s.ReservationRoomID, GuestID: s.GuestID,
		ArrivalDate: s.ArrivalDate, DepartureDate: s.DepartureDate, AdultCount: int(s.AdultCount), ChildCount: int(s.ChildCount),
		Status: s.Status, CheckedInAt: s.ActualCheckInAt, CheckedOutAt: s.ActualCheckOutAt, Version: s.Version,
	}
}

func toSegment(r frontdeskdb.StayRoom, number string) Segment {
	seg := Segment{ID: r.ID, RoomID: r.RoomID, RoomNumber: number, CheckInAt: r.CheckInAt, CheckOutAt: r.CheckOutAt,
		StartBusinessDate: r.StartBusinessDate, EndBusinessDate: r.EndBusinessDate}
	if r.MoveReason != nil {
		seg.MoveReason = *r.MoveReason
	}
	return seg
}

// lineFacts is what check-in needs to know about the reservation room, whether it was just created (walk-in)
// or locked (check-in).
type lineFacts struct {
	ID            int64
	ReservationID int64
	RoomTypeID    int64
	RoomID        *int64
	Arrival       civil.Date
	Departure     civil.Date
	// BedLocked and RequestedBedTypeID: the line keeps this bed, so the room must have it.
	BedLocked          bool
	RequestedBedTypeID *int64
}

type checkInCmd struct {
	p          auth.Principal
	propertyID int64
	bd         civil.Date
	line       lineFacts
	roomID     int64
	guestID    int64
	accompany  []int64
	adults     int
	children   int
	override   bool
	overrideBy string
	key        string
	lockedFund int64 // folio locked by LockUnlinkedFolio (0 = none)
}

// checkInCore does the writes and checks of a check-in once the locks are held (room type, room, and the
// reservation lines). It checks the room, the cleanliness rule and the party, then writes the stay, its open
// segment and guests, marks the line CHECKED_IN and attaches the folio. Nothing else is written.
func (s *Service) checkInCore(ctx context.Context, c checkInCmd) (CheckInResult, error) {
	q := s.q(ctx)
	room, err := q.GetRoomForCheckIn(ctx, frontdeskdb.GetRoomForCheckInParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, ID: c.roomID})
	if err != nil {
		return CheckInResult{}, notFound(err, errRoomNotFound())
	}
	if !room.IsActive {
		return CheckInResult{}, apperr.Conflict("ROOM_NOT_AVAILABLE", "the room is not active").WithContext("room_id", room.ID)
	}
	own := c.line.ID
	issues, err := s.avail.RoomIssues(ctx, c.p.TenantID, c.propertyID, room.ID, c.bd, c.bd, c.line.Departure, &own)
	if err != nil {
		return CheckInResult{}, err
	}
	for _, i := range issues {
		switch i.Kind {
		case availability.IssueOccupied:
			return CheckInResult{}, apperr.Conflict("ROOM_OCCUPIED", "the room is occupied").WithContext("room_id", room.ID)
		case availability.IssueBlocked:
			return CheckInResult{}, apperr.Conflict("ROOM_BLOCKED", "the room is out of order or out of service for these dates").WithContext("room_id", room.ID).WithContext("block_id", i.ID)
		}
	}
	if len(issues) > 0 {
		return CheckInResult{}, apperr.Conflict("ROOM_NOT_AVAILABLE", "the room is not free for the stay").WithContext("room_id", room.ID).WithContext("issues", issues)
	}
	// A line that keeps its bed needs a room with it.
	if c.line.BedLocked && c.line.RequestedBedTypeID != nil && room.BedTypeID != *c.line.RequestedBedTypeID {
		return CheckInResult{}, apperr.Invalid("the room does not have the bed that is kept", fieldErr("room_id", "ROOM_BED_MISMATCH", "the reservation keeps another bed than this room has"))
	}
	// A room of another type than the one that line consumes is an upgrade: it needs the permission and the
	// room type's inventory.
	consumes := c.line.RoomTypeID
	if c.line.RoomID != nil {
		if t, err := q.GetRoomForCheckIn(ctx, frontdeskdb.GetRoomForCheckInParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, ID: *c.line.RoomID}); err == nil {
			consumes = t.RoomTypeID
		}
	}
	if room.RoomTypeID != consumes {
		if err := s.authz.Require(ctx, c.propertyID, auth.PermReservationUpgrade); err != nil {
			return CheckInResult{}, err
		}
	}
	// The stay sits in a room of this bed whatever the type: the line of the bed must have room for it (the line's own demand
	// is left out, so a locked line that gets its bed changes nothing). A room of another type also needs that type's stock.
	demand := availability.NewDemand()
	if room.RoomTypeID != consumes {
		demand.Types.Add(room.RoomTypeID, c.bd, c.line.Departure, 1)
	}
	demand.Beds.Add(availability.BedKey{RoomTypeID: room.RoomTypeID, BedTypeID: room.BedTypeID}, c.bd, c.line.Departure, 1)
	if err := s.avail.RequireAvailableFor(ctx, c.p.TenantID, c.propertyID, c.bd, demand, &own); err != nil {
		return CheckInResult{}, err
	}
	prop, err := s.days.GetProperty(ctx, c.propertyID)
	if err != nil {
		return CheckInResult{}, err
	}
	if err := s.requireReady(ctx, c, room.HousekeepingStatus, prop.RequireRoomInspectionForCheckin); err != nil {
		return CheckInResult{}, err
	}
	limits, err := q.GetRoomTypeLimits(ctx, frontdeskdb.GetRoomTypeLimitsParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, ID: room.RoomTypeID})
	if err != nil {
		return CheckInResult{}, err
	}
	if !availability.FitsOccupancy(c.adults, c.children, int(limits.MaxAdult), int(limits.MaxChild), int(limits.MaxOccupancy)) {
		return CheckInResult{}, apperr.Invalid("the party does not fit the room", fieldErr("adult_count", "OCCUPANCY_EXCEEDED", "the party exceeds the room type limits"))
	}
	if err := s.guests.RequireVisible(ctx, c.guestID); err != nil {
		return CheckInResult{}, err
	}
	for _, id := range c.accompany {
		if err := s.guests.RequireVisible(ctx, id); err != nil {
			return CheckInResult{}, err
		}
	}

	now := s.clock.Now()
	number, err := s.days.NextDocumentNumber(ctx, c.propertyID, tenancy.SeqStay) // L5
	if err != nil {
		return CheckInResult{}, err
	}
	stay, err := q.InsertStay(ctx, frontdeskdb.InsertStayParams{
		TenantID: c.p.TenantID, PropertyID: c.propertyID, StayNumber: number, ReservationRoomID: c.line.ID, GuestID: c.guestID,
		ArrivalDate: c.bd, DepartureDate: c.line.Departure, AdultCount: int16(c.adults), ChildCount: int16(c.children), //nolint:gosec // G115: bounded by FitsOccupancy
		CheckInAt: now, ActorID: c.p.ActorID(), IdempotencyKey: nullable(c.key),
	})
	if err != nil {
		return CheckInResult{}, err
	}
	seg, err := q.InsertStayRoom(ctx, frontdeskdb.InsertStayRoomParams{
		TenantID: c.p.TenantID, PropertyID: c.propertyID, StayID: stay.ID, RoomID: room.ID, CheckInAt: now, StartBusinessDate: c.bd, ActorID: c.p.ActorID(),
	})
	if err != nil {
		return CheckInResult{}, err
	}
	for _, id := range c.accompany {
		if err := q.InsertStayGuest(ctx, frontdeskdb.InsertStayGuestParams{TenantID: c.p.TenantID, PropertyID: c.propertyID, StayID: stay.ID, GuestID: id, ActorID: c.p.ActorID()}); err != nil {
			return CheckInResult{}, err
		}
	}
	if err := s.res.MarkCheckedIn(ctx, c.p, c.propertyID, c.line.ID, room.ID); err != nil {
		return CheckInResult{}, err
	}
	folio, err := s.folios.AttachStayFolio(ctx, c.p, c.propertyID, c.line.ReservationID, stay.ID, c.lockedFund)
	if err != nil {
		return CheckInResult{}, err
	}
	entry := map[string]any{
		"stay_number": stay.StayNumber, "reservation_id": c.line.ReservationID, "reservation_room_id": c.line.ID, "room_id": room.ID,
		"room_number": room.RoomNumber, "guest_id": c.guestID, "folio_id": folio.ID, "housekeeping_status": room.HousekeepingStatus,
	}
	if c.override {
		entry["override_room_not_ready"] = true
		entry["override_reason"] = c.overrideBy
	}
	if err := s.audit.Write(ctx, auditEntry(c.p, c.propertyID, c.bd, "stay.checked_in", stay.ID, nil, entry)); err != nil {
		return CheckInResult{}, err
	}
	return CheckInResult{Stay: toStay(stay, c.line.ReservationID), StayRoom: toSegment(seg, room.RoomNumber), Folio: folio}, nil
}

// requireReady applies the cleanliness rule: CLEAN or INSPECTED, or INSPECTED only when the property requires
// an inspection. An override needs frontdesk.checkin_unready_room and a reason.
func (s *Service) requireReady(ctx context.Context, c checkInCmd, status string, requireInspection bool) error {
	ready := status == string(housekeeping.Clean) || status == string(housekeeping.Inspected)
	required := string(housekeeping.Clean)
	if requireInspection {
		ready = status == string(housekeeping.Inspected)
		required = string(housekeeping.Inspected)
	}
	if ready {
		return nil
	}
	if !c.override {
		return apperr.Conflict("ROOM_NOT_READY", "the room is not ready for check-in").WithContext("current", status).WithContext("required", required)
	}
	if err := s.authz.Require(ctx, c.propertyID, auth.PermFrontdeskCheckinUnreadyRoom); err != nil {
		return err
	}
	if strings.TrimSpace(c.overrideBy) == "" {
		return apperr.Invalid("a reason is required to override", fieldErr("override_reason", "REQUIRED", "say why the room is used unready"))
	}
	return nil
}

// checkInValidate checks what does not need the database.
func (in CheckInInput) validate() []apperr.FieldError {
	var f []apperr.FieldError
	if in.Version < 1 {
		f = append(f, fieldErr("version", "REQUIRED", "the version you loaded"))
	}
	if in.GuestID < 1 {
		f = append(f, fieldErr("guest_id", "REQUIRED", "the guest who checks in"))
	}
	if in.AdultCount < 1 {
		f = append(f, fieldErr("adult_count", "OUT_OF_RANGE", "at least one adult"))
	}
	if in.ChildCount < 0 {
		f = append(f, fieldErr("child_count", "OUT_OF_RANGE", "not negative"))
	}
	if len([]rune(in.OverrideReason)) > maxReasonLen {
		f = append(f, fieldErr("override_reason", "TOO_LONG", "at most 500 characters"))
	}
	return f
}

// replayLoop answers an idempotent request from the stored result of its key; a request that loses the race to
// a concurrent one with the same key (DUPLICATE_REQUEST) replays the winner.
func replayLoop[T any](key string, lookup func() (T, bool, error), run func() (T, error)) (T, error) {
	var zero T
	for attempt := 0; attempt < 2; attempt++ {
		if key != "" {
			if v, ok, err := lookup(); err != nil || ok {
				return v, err
			}
		}
		v, err := run()
		// A concurrent request with the same key may have won: look for its stay once more.
		if key != "" && attempt == 0 && (apperr.IsCode(err, "DUPLICATE_REQUEST") || apperr.IsCode(err, "VERSION_CONFLICT") || apperr.IsCode(err, "LINE_NOT_CONFIRMED") || apperr.IsCode(err, "ALREADY_CHECKED_IN")) {
			continue
		}
		return v, err
	}
	return zero, apperr.Busy("REQUEST_IN_PROGRESS", "the same request is still being processed")
}

// CheckIn checks a CONFIRMED room in (frontdesk.checkin). key is the request's Idempotency-Key: the same key
// returns the stay it created. The arrival date must be the business date.
func (s *Service) CheckIn(ctx context.Context, propertyID, reservationID, lineID int64, key string, in CheckInInput) (CheckInResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskCheckin)
	if err != nil {
		return CheckInResult{}, err
	}
	fields := in.validate()
	if len(key) > 100 {
		fields = append(fields, fieldErr("Idempotency-Key", "TOO_LONG", "at most 100 characters"))
	}
	if len(fields) > 0 {
		return CheckInResult{}, apperr.Invalid("the check-in is invalid", fields...)
	}
	return replayLoop(key,
		func() (CheckInResult, bool, error) { return s.replay(ctx, p, propertyID, key, lineID) },
		func() (CheckInResult, error) {
			var out CheckInResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				pre, err := s.q(ctx).GetStayLine(ctx, frontdeskdb.GetStayLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
				if err != nil || pre.ReservationID != reservationID {
					return apperr.NotFound("RESERVATION_ROOM_NOT_FOUND", "the reservation room does not exist")
				}
				target := in.RoomID
				if target == nil {
					target = pre.RoomID
				}
				if target == nil {
					return apperr.Invalid("a room is required", fieldErr("room_id", "REQUIRED", "the room has no room assigned yet"))
				}
				locked, err := s.res.Lock(ctx, p.TenantID, propertyID, reservationID, in.Version, nil, []int64{*target}) // L1, L2, L3, L4
				if err != nil {
					return err
				}
				if locked.Reservation.Status != reservations.StatusConfirmed {
					return apperr.Conflict("RESERVATION_NOT_CONFIRMED", "the reservation is not confirmed").WithContext("status", locked.Reservation.Status)
				}
				line, ok := locked.Line(lineID)
				if !ok {
					return apperr.NotFound("RESERVATION_ROOM_NOT_FOUND", "the reservation room does not exist")
				}
				if line.Status != reservations.LineConfirmed {
					return apperr.Conflict("LINE_NOT_CONFIRMED", "only a confirmed room can be checked in").WithContext("status", line.Status)
				}
				if !line.ArrivalDate.Equal(locked.BusinessDate) {
					return apperr.Conflict("ARRIVAL_DATE_MISMATCH", "the room does not arrive on the business date").
						WithContext("business_date", locked.BusinessDate).WithContext("arrival_date", line.ArrivalDate)
				}
				folioID, err := s.folios.LockUnlinkedFolio(ctx, p.TenantID, propertyID, reservationID) // L4 folio, before any sequence
				if err != nil {
					return err
				}
				out, err = s.checkInCore(ctx, checkInCmd{
					p: p, propertyID: propertyID, bd: locked.BusinessDate,
					line:   lineFacts{ID: line.ID, ReservationID: reservationID, RoomTypeID: line.RoomTypeID, RoomID: line.RoomID, Arrival: line.ArrivalDate, Departure: line.DepartureDate, BedLocked: line.BedLocked, RequestedBedTypeID: line.RequestedBedTypeID},
					roomID: *target, guestID: in.GuestID, accompany: dedupe(in.AccompanyingGuestIDs, in.GuestID), adults: in.AdultCount, children: in.ChildCount,
					override: in.OverrideRoomNotReady, overrideBy: strings.TrimSpace(in.OverrideReason), key: key, lockedFund: folioID,
				})
				return err
			})
			return out, err
		})
}

// replay returns the stay stored under an Idempotency-Key.
func (s *Service) replay(ctx context.Context, p auth.Principal, propertyID int64, key string, lineID int64) (CheckInResult, bool, error) {
	q := s.q(ctx)
	st, err := q.GetStayByKey(ctx, frontdeskdb.GetStayByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key})
	if errors.Is(err, pgx.ErrNoRows) {
		return CheckInResult{}, false, nil
	}
	if err != nil {
		return CheckInResult{}, false, err
	}
	if lineID != 0 && st.ReservationRoomID != lineID {
		return CheckInResult{}, false, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used for another request")
	}
	line, err := q.GetStayLine(ctx, frontdeskdb.GetStayLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: st.ReservationRoomID})
	if err != nil {
		return CheckInResult{}, false, err
	}
	segs, err := q.ListSegments(ctx, frontdeskdb.ListSegmentsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: st.ID})
	if err != nil || len(segs) == 0 {
		return CheckInResult{}, false, err
	}
	fs, err := s.folios.StayFolios(ctx, p.TenantID, propertyID, st.ID)
	if err != nil {
		return CheckInResult{}, false, err
	}
	out := CheckInResult{Stay: toStay(st, line.ReservationID), StayRoom: toSegment(frontdeskdb.StayRoom{
		ID: segs[0].ID, RoomID: segs[0].RoomID, CheckInAt: segs[0].CheckInAt, CheckOutAt: segs[0].CheckOutAt,
		StartBusinessDate: segs[0].StartBusinessDate, EndBusinessDate: segs[0].EndBusinessDate, MoveReason: segs[0].MoveReason,
	}, segs[0].RoomNumber)}
	if len(fs) > 0 {
		out.Folio = fs[0]
	}
	return out, true, nil
}

// WalkIn creates, confirms, assigns and checks in one room in a single transaction, through the same services
// (frontdesk.checkin and reservation.create). Arrival is the business date and the source is WALK_IN.
func (s *Service) WalkIn(ctx context.Context, propertyID int64, key string, in WalkInInput) (CheckInResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskCheckin)
	if err != nil {
		return CheckInResult{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermReservationCreate); err != nil {
		return CheckInResult{}, err
	}
	var fields []apperr.FieldError
	if (in.GuestID == nil) == (in.NewGuest == nil) {
		fields = append(fields, fieldErr("guest_id", "REQUIRED", "give guest_id or new_guest"))
	}
	if in.RoomID < 1 {
		fields = append(fields, fieldErr("room_id", "REQUIRED", "a room"))
	}
	if in.RatePlanID < 1 {
		fields = append(fields, fieldErr("rate_plan_id", "REQUIRED", "a rate plan"))
	}
	if in.AdultCount < 1 {
		fields = append(fields, fieldErr("adult_count", "OUT_OF_RANGE", "at least one adult"))
	}
	if len([]rune(in.OverrideReason)) > maxReasonLen {
		fields = append(fields, fieldErr("override_reason", "TOO_LONG", "at most 500 characters"))
	}
	if len(key) > 100 {
		fields = append(fields, fieldErr("Idempotency-Key", "TOO_LONG", "at most 100 characters"))
	}
	if len(fields) > 0 {
		return CheckInResult{}, apperr.Invalid("the walk-in is invalid", fields...)
	}
	return replayLoop(key,
		func() (CheckInResult, bool, error) { return s.replay(ctx, p, propertyID, key, 0) },
		func() (CheckInResult, error) {
			var out CheckInResult
			err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
				if err != nil {
					return err
				}
				bd := day.BusinessDate
				if !in.DepartureDate.After(bd) {
					return apperr.Invalid("the walk-in is invalid", fieldErr("departure_date", "BEFORE_ARRIVAL", "after the business date"))
				}
				room, err := s.q(ctx).GetRoomForCheckIn(ctx, frontdeskdb.GetRoomForCheckInParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.RoomID})
				if err != nil {
					return notFound(err, errRoomNotFound())
				}
				// L2 and L3 first: a new guest takes a sequence number, after which no lower lock may be taken.
				if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, []int64{room.RoomTypeID}); err != nil {
					return mapNotFound(err, apperr.NotFound("ROOM_TYPE_NOT_FOUND", "the room type does not exist in this property"))
				}
				if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, []int64{room.ID}); err != nil {
					return mapNotFound(err, errRoomNotFound())
				}
				guestID, err := s.walkInGuest(ctx, propertyID, in)
				if err != nil {
					return err
				}
				// Create, confirm and assign in one go, under the locks taken above. The rows it writes are ours and
				// need no further locks.
				res, err := s.res.CreateHeld(ctx, propertyID, bd, reservations.CreateInput{
					RateOverrideReason: in.RateOverrideReason, RateOverrideApproval: in.RateOverrideApproval, OccupancyApproval: in.OccupancyApproval, ExceedFreeQuota: in.ExceedFreeQuota, RestrictionOverride: in.RestrictionOverride,
					GuestID: &guestID, Source: "WALK_IN", Confirm: true, Rooms: []reservations.LineInput{{
						RoomTypeID: room.RoomTypeID, RatePlanID: in.RatePlanID, Arrival: bd, Departure: in.DepartureDate, Adults: in.AdultCount, Children: in.ChildCount,
						RoomID: &in.RoomID, Overrides: in.NightlyOverrides, OccupancyReason: in.OccupancyReason,
					}},
				})
				if err != nil {
					return err
				}
				line := res.Rooms[0]
				out, err = s.checkInCore(ctx, checkInCmd{
					p: p, propertyID: propertyID, bd: bd,
					line:   lineFacts{ID: line.ID, ReservationID: res.ID, RoomTypeID: line.RoomTypeID, RoomID: line.RoomID, Arrival: bd, Departure: in.DepartureDate},
					roomID: in.RoomID, guestID: guestID, accompany: dedupe(in.AccompanyingGuestIDs, guestID), adults: in.AdultCount, children: in.ChildCount,
					override: in.OverrideRoomNotReady, overrideBy: strings.TrimSpace(in.OverrideReason), key: key,
				})
				if err != nil {
					return err
				}
				out.Reservation = &ReservationRef{ID: res.ID, ConfirmationNumber: res.ConfirmationNumber, Status: res.Status, Version: res.Version + 1}
				return nil
			})
			return out, err
		})
}

func (s *Service) walkInGuest(ctx context.Context, propertyID int64, in WalkInInput) (int64, error) {
	if in.GuestID != nil {
		return *in.GuestID, s.guests.RequireVisible(ctx, *in.GuestID)
	}
	created, err := s.guests.Create(ctx, propertyID, *in.NewGuest)
	if err != nil {
		return 0, err
	}
	return created.Guest.ID, nil
}

// ReverseCheckIn undoes a check-in of the same business date (frontdesk.reverse_checkin): the stay is
// cancelled, its segment closed, the room line back to CONFIRMED, the guest folio unlinked, the company folios closed and the room DIRTY. It is refused once a
// charge is posted to a folio of the stay (CHECK_IN_HAS_CHARGES) or while a company folio holds a payment (CHECK_IN_HAS_PAYMENTS): a cancelled stay has no open folio.
func (s *Service) ReverseCheckIn(ctx context.Context, propertyID, stayID int64, in ReverseInput) (ReverseResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFrontdeskReverseCheckin)
	if err != nil {
		return ReverseResult{}, err
	}
	reason := strings.TrimSpace(in.Reason)
	var fields []apperr.FieldError
	if in.Version < 1 {
		fields = append(fields, fieldErr("version", "REQUIRED", "the version you loaded"))
	}
	if reason == "" {
		fields = append(fields, fieldErr("reason", "REQUIRED", "a reason is required"))
	} else if len([]rune(reason)) > maxReasonLen {
		fields = append(fields, fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	if len(fields) > 0 {
		return ReverseResult{}, apperr.Invalid("the reversal is invalid", fields...)
	}
	var out ReverseResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := q.GetStay(ctx, frontdeskdb.GetStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID})
		if err != nil {
			return notFound(err, errStayNotFound())
		}
		line, err := q.GetStayLine(ctx, frontdeskdb.GetStayLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: pre.ReservationRoomID})
		if err != nil {
			return err
		}
		segs, err := q.ListSegments(ctx, frontdeskdb.ListSegmentsParams{TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID})
		if err != nil {
			return err
		}
		var roomIDs []int64
		for _, sg := range segs {
			roomIDs = append(roomIDs, sg.RoomID)
		}
		// The room goes DIRTY (it locks the housekeeping row at L3 and re-enters the business day, so it comes before the room locks). The
		// whole transaction rolls back if a later check refuses the reversal.
		if err := s.hk.MarkDirty(ctx, p.TenantID, propertyID, segs[0].RoomID, housekeeping.SourceCheckInReversal, reason, p.ActorID()); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, roomIDs); err != nil { // L3
			return mapNotFound(err, errRoomNotFound())
		}
		if err := db.LockRows(ctx, db.Reservations, db.ForUpdate, propertyID, []int64{line.ReservationID}); err != nil { // L4
			return mapNotFound(err, apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property"))
		}
		if err := db.LockRows(ctx, db.ReservationRooms, db.ForUpdate, propertyID, []int64{line.ID}); err != nil {
			return err
		}
		// NO KEY UPDATE and not UPDATE: a payment or a charge that is being posted to a folio of this stay holds the folio and key-shares this row; the reversal waits for that folio next,
		// and under FOR UPDATE the two would wait for each other (a deadlock, found by the race test of audit F-06). The status and the version are not key columns.
		if err := db.LockRows(ctx, db.Stays, db.ForNoKeyUpdate, propertyID, []int64{stayID}); err != nil {
			return mapNotFound(err, errStayNotFound())
		}
		st, err := q.GetStay(ctx, frontdeskdb.GetStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID})
		if err != nil {
			return err
		}
		if st.Version != in.Version {
			return apperr.Conflict("VERSION_CONFLICT", "the stay was changed by someone else").WithContext("current_version", st.Version)
		}
		if st.Status != "OPEN" {
			return apperr.Conflict("STAY_NOT_OPEN", "only an open stay can have its check-in reversed").WithContext("status", st.Status)
		}
		if !st.ArrivalDate.Equal(day.BusinessDate) || len(segs) != 1 {
			return apperr.Conflict("CHECK_IN_NOT_REVERSIBLE", "a check-in can only be reversed on the day it happened, before any room move").
				WithContext("arrival_date", st.ArrivalDate).WithContext("business_date", day.BusinessDate)
		}
		folio, closedFolios, err := s.folios.DetachStayFolio(ctx, p, propertyID, stayID, day.BusinessDate) // L4 folios, all of the stay, ascending; refuses a charge, or a payment on a company folio
		if err != nil {
			return err
		}
		now, bd := s.clock.Now(), day.BusinessDate
		if _, err := q.CloseOpenSegment(ctx, frontdeskdb.CloseOpenSegmentParams{
			TenantID: p.TenantID, PropertyID: propertyID, StayID: stayID, CheckOutAt: &now, EndBusinessDate: &bd, Reason: &reason,
		}); err != nil {
			return err
		}
		cancelled, err := q.CancelStay(ctx, frontdeskdb.CancelStayParams{TenantID: p.TenantID, PropertyID: propertyID, ID: stayID, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.res.MarkCheckInReversed(ctx, p, propertyID, line.ID); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "stay.check_in_reversed", stayID,
			map[string]any{"status": st.Status}, map[string]any{"status": cancelled.Status, "reason": reason, "room_id": segs[0].RoomID, "closed_folios": closedFolios})); err != nil {
			return err
		}
		out = ReverseResult{Stay: toStay(cancelled, line.ReservationID), Folio: folio, ClosedFolios: closedFolios}
		return nil
	})
	return out, err
}
