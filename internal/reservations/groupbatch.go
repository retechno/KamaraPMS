package reservations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/guests"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations/reservationsdb"
)

// Bulk rooms for a booking group (docs/architecture/30-group-folio.md, part A): one reservation for each room, all in one transaction, all or nothing, through the booking engine
// that exists (`create`), never a second path.
//
// The engine takes the group lock (level 45) and then a document number (level 50). Called in a loop in one transaction, the second reservation would take the group lock after the sequence
// of the first, which the lock order refuses. So the batch takes every lock once, at the start (the business day, the room types of every row ascending, the group), and tells the engine
// that the group is held (`batchState.group`). The same state asks the engine for the short answer (the batch reads its reservations back in one query) and for no confirmation e-mail
// for each room.

// Limits of one request. A bigger group is several requests, each of them atomic.
const (
	MaxBatchRooms  = 100
	MaxBatchRows   = 20
	maxBatchKeyLen = 90 // the longest suffix, "#100", still fits the 100 characters of a key
)

// batchState is what the engine is told while a batch runs.
type batchState struct {
	group  int64 // the group whose lock the batch holds
	quiet  bool  // `create` answers with the id and the number only
	noMail bool  // no confirmation e-mail is queued for each room
}

type batchStateKey struct{}

func withBatch(ctx context.Context, b *batchState) context.Context {
	return context.WithValue(ctx, batchStateKey{}, b)
}

func batchFrom(ctx context.Context) *batchState {
	b, _ := ctx.Value(batchStateKey{}).(*batchState)
	return b
}

// BatchRow asks for Count rooms of one type and plan with the same dates and party.
type BatchRow struct {
	RoomTypeID int64      `json:"room_type_id"`
	RatePlanID int64      `json:"rate_plan_id"`
	Count      int        `json:"count"`
	Arrival    civil.Date `json:"arrival_date"`
	Departure  civil.Date `json:"departure_date"`
	Adults     int        `json:"adult_count"`
	Children   int        `json:"child_count"`
	// RatePerNight replaces the price of every night of the row (an override: it needs a reason and an approval, once for the whole request). Empty: the price in force.
	RatePerNight string `json:"rate_per_night,omitempty"`
	// OccupancyReason is why the rooms are free, for a complimentary or house use plan.
	OccupancyReason string `json:"occupancy_reason,omitempty"`
}

// BatchInput is a request to book rooms for a group.
type BatchInput struct {
	Rows []BatchRow `json:"rows"`
	// Source of the reservations; empty is OTHER.
	Source string `json:"source,omitempty"`
	// Confirm: nil is true, a block that holds nothing is not a block. False makes drafts.
	Confirm *bool `json:"confirm,omitempty"`
	// BookerGuestID overrides the booker: the guest made from the contact of the group.
	BookerGuestID *int64 `json:"booker_guest_id,omitempty"`
	// RateOverrideReason and RateOverrideApproval justify the rate of the rows that differ from the price: one reason and one approval for the whole request.
	RateOverrideReason   string             `json:"rate_override_reason,omitempty"`
	RateOverrideApproval *iam.ApprovalInput `json:"rate_override_approval,omitempty"`
	// OccupancyApproval, ExceedFreeQuota and RestrictionOverride are the approvals the booking engine knows, once for the request.
	OccupancyApproval   *iam.ApprovalInput   `json:"occupancy_approval,omitempty"`
	ExceedFreeQuota     bool                 `json:"exceed_free_quota,omitempty"`
	RestrictionOverride *RestrictionOverride `json:"restriction_override,omitempty"`
}

func (in BatchInput) confirm() bool { return in.Confirm == nil || *in.Confirm }

func (in BatchInput) source() string {
	if in.Source == "" {
		return "OTHER"
	}
	return in.Source
}

func (in BatchInput) rooms() int {
	n := 0
	for _, r := range in.Rows {
		n += r.Count
	}
	return n
}

// Hash identifies the request for idempotent replays. The credentials are never part of it.
func (in BatchInput) Hash(groupID int64) string {
	in.RateOverrideApproval, in.OccupancyApproval = nil, nil
	if in.RestrictionOverride != nil {
		ro := *in.RestrictionOverride
		ro.Approval = nil
		in.RestrictionOverride = &ro
	}
	b, _ := json.Marshal(struct {
		Group int64      `json:"group"`
		In    BatchInput `json:"in"`
	}{groupID, in}) //nolint:errchkjson // plain structs cannot fail to marshal
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// validate checks what needs no database.
func (in BatchInput) validate(decimals int32) []apperr.FieldError {
	var f []apperr.FieldError
	if len(in.Rows) == 0 {
		f = append(f, fieldErr("rows", "REQUIRED", "at least one row"))
	}
	if len(in.Rows) > MaxBatchRows {
		f = append(f, fieldErr("rows", "TOO_MANY_ROWS", fmt.Sprintf("at most %d rows in a request", MaxBatchRows)))
	}
	if in.rooms() > MaxBatchRooms {
		f = append(f, fieldErr("rows", "TOO_MANY_ROOMS", fmt.Sprintf("at most %d rooms in a request", MaxBatchRooms)))
	}
	if in.Source != "" && !slices.Contains(sources, in.Source) {
		f = append(f, fieldErr("source", "INVALID_VALUE", strings.Join(sources, ", ")))
	}
	for i, r := range in.Rows {
		p := fmt.Sprintf("rows[%d].", i)
		if r.RoomTypeID < 1 {
			f = append(f, fieldErr(p+"room_type_id", "REQUIRED", "a room type"))
		}
		if r.RatePlanID < 1 {
			f = append(f, fieldErr(p+"rate_plan_id", "REQUIRED", "a rate plan"))
		}
		if r.Count < 1 || r.Count > MaxBatchRooms {
			f = append(f, fieldErr(p+"count", "OUT_OF_RANGE", fmt.Sprintf("1 to %d rooms", MaxBatchRooms)))
		}
		if !r.Departure.After(r.Arrival) {
			f = append(f, fieldErr(p+"departure_date", "BEFORE_ARRIVAL", "after arrival"))
		} else if r.Arrival.DaysUntil(r.Departure) > availability.MaxSearchNights {
			f = append(f, fieldErr(p+"departure_date", "TOO_LONG_STAY", "at most 365 nights"))
		}
		if r.Adults < 1 {
			f = append(f, fieldErr(p+"adult_count", "OUT_OF_RANGE", "at least one adult"))
		}
		if r.Children < 0 {
			f = append(f, fieldErr(p+"child_count", "OUT_OF_RANGE", "not negative"))
		}
		if r.RatePerNight != "" {
			if _, err := rates.ParseAmount(r.RatePerNight, decimals); err != nil {
				f = append(f, fieldErr(p+"rate_per_night", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals"))
			}
		}
	}
	return f
}

func (in BatchInput) typeIDs() []int64 {
	ids := make([]int64, 0, len(in.Rows))
	for _, r := range in.Rows {
		if !slices.Contains(ids, r.RoomTypeID) {
			ids = append(ids, r.RoomTypeID)
		}
	}
	slices.Sort(ids)
	return ids
}

// BatchReservation is one reservation of a batch.
type BatchReservation struct {
	// Position is the place of the room in the request, from 1: the rows in order, the rooms of a row in order.
	Position           int        `json:"position"`
	Row                int        `json:"row"`
	ReservationID      int64      `json:"reservation_id"`
	ConfirmationNumber string     `json:"confirmation_number"`
	LineID             int64      `json:"line_id"`
	GuestID            *int64     `json:"guest_id"`
	RoomTypeID         int64      `json:"room_type_id"`
	Arrival            civil.Date `json:"arrival_date"`
	Departure          civil.Date `json:"departure_date"`
}

// BatchResult is what a bulk booking made.
type BatchResult struct {
	Reservations []BatchReservation `json:"reservations"`
	Rooms        int                `json:"rooms"`
	Nights       int                `json:"nights"`
	// Replayed says the answer is the one stored for the Idempotency-Key: nothing was made now.
	Replayed bool `json:"replayed"`
}

func errGroupNotFound() *apperr.Error {
	return apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
}

// placeholderName is the name of the guest of a room that has none yet: "group name · 7", cut so that it fits the last name.
func placeholderName(group string, n int) string {
	suffix := fmt.Sprintf(" · %d", n)
	r := []rune(strings.TrimSpace(group))
	if limit := 100 - utf8.RuneCountInString(suffix); len(r) > limit {
		r = r[:limit]
	}
	return string(r) + suffix
}

// splitName is "First Last": everything before the last word is the first name, a single word is the last name.
func splitName(full string) (first, last string) {
	full = strings.TrimSpace(full)
	if i := strings.LastIndexAny(full, " \t"); i > 0 {
		return strings.TrimSpace(full[:i]), strings.TrimSpace(full[i+1:])
	}
	return "", full
}

// rowError says which row, and which room of the row, a failure belongs to: the fields of the engine ("rooms[0].arrival_date") become "rows[2].arrival_date".
func rowError(err error, row, room int) error {
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		return err
	}
	cp := *ae
	cp.Fields = make([]apperr.FieldError, len(ae.Fields))
	for i, f := range ae.Fields {
		f.Field = strings.Replace(f.Field, "rooms[0].", fmt.Sprintf("rows[%d].", row), 1)
		cp.Fields[i] = f
	}
	cp.Context = make(map[string]any, len(ae.Context)+2)
	for k, v := range ae.Context {
		cp.Context[k] = v
	}
	cp.Context["row"], cp.Context["room_in_row"] = row, room
	return &cp
}

// CreateGroupRooms books the rooms of a request for a group: one reservation for each room, all in one transaction, all or nothing (reservation.create, guest.write). key is the
// Idempotency-Key of the request: the same key and request returns what was made, another request under it is 422 IDEMPOTENCY_KEY_REUSED.
func (s *Service) CreateGroupRooms(ctx context.Context, propertyID, groupID int64, key string, in BatchInput) (BatchResult, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCreate)
	if err != nil {
		return BatchResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return BatchResult{}, err
	}
	fields := in.validate(decimals)
	switch {
	case strings.TrimSpace(key) == "":
		return BatchResult{}, apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	case len(key) > maxBatchKeyLen:
		fields = append(fields, fieldErr("Idempotency-Key", "TOO_LONG", fmt.Sprintf("at most %d characters", maxBatchKeyLen)))
	}
	if len(fields) > 0 {
		code := "VALIDATION_FAILED"
		for _, f := range fields {
			if f.Code == "TOO_MANY_ROOMS" || f.Code == "TOO_MANY_ROWS" {
				code = f.Code
			}
		}
		e := apperr.Invalid("the request is invalid", fields...)
		e.Code = code
		return BatchResult{}, e
	}
	hash := in.Hash(groupID)
	for attempt := 0; attempt < 2; attempt++ {
		if res, ok, err := s.batchReplay(ctx, p, propertyID, groupID, key, hash, in); ok || err != nil {
			return res, err
		}
		res, err := s.createGroupRooms(ctx, p, propertyID, groupID, key, hash, in, decimals)
		if apperr.IsCode(err, "DUPLICATE_REQUEST") {
			continue // a concurrent request with the same key won; replay it
		}
		return res, err
	}
	return BatchResult{}, apperr.Busy("REQUEST_IN_PROGRESS", "the same request is still being processed")
}

func (s *Service) batchReplay(ctx context.Context, p auth.Principal, propertyID, groupID int64, key, hash string, in BatchInput) (BatchResult, bool, error) {
	first := key + "#1"
	row, err := s.q(ctx).GetReservationByKey(ctx, reservationsdb.GetReservationByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &first})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return BatchResult{}, false, nil
		}
		return BatchResult{}, false, err
	}
	if row.IdempotencyHash == nil || *row.IdempotencyHash != hash {
		return BatchResult{}, false, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used with a different request")
	}
	res, err := s.batchResult(ctx, p, propertyID, groupID, key, in)
	res.Replayed = true
	return res, true, err
}

// batchResult reads the reservations of a batch back, in the order of the request.
func (s *Service) batchResult(ctx context.Context, p auth.Principal, propertyID, groupID int64, key string, in BatchInput) (BatchResult, error) {
	lines, err := s.q(ctx).ListBatchLines(ctx, reservationsdb.ListBatchLinesParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID, Prefix: key + "#"})
	if err != nil {
		return BatchResult{}, err
	}
	rowOf := make([]int, 0, in.rooms()) // position-1 to row
	for i, r := range in.Rows {
		for k := 0; k < r.Count; k++ {
			rowOf = append(rowOf, i)
		}
	}
	out := BatchResult{Reservations: make([]BatchReservation, 0, len(lines))}
	for _, l := range lines {
		pos := 0
		if l.IdempotencyKey != nil {
			pos, _ = strconv.Atoi(strings.TrimPrefix(*l.IdempotencyKey, key+"#"))
		}
		row := 0
		if pos >= 1 && pos <= len(rowOf) {
			row = rowOf[pos-1]
		}
		out.Reservations = append(out.Reservations, BatchReservation{
			Position: pos, Row: row, ReservationID: l.ReservationID, ConfirmationNumber: l.ConfirmationNumber, LineID: l.LineID, GuestID: l.GuestID,
			RoomTypeID: l.RoomTypeID, Arrival: l.ArrivalDate, Departure: l.DepartureDate,
		})
		out.Nights += l.ArrivalDate.DaysUntil(l.DepartureDate)
	}
	slices.SortFunc(out.Reservations, func(a, b BatchReservation) int { return a.Position - b.Position })
	out.Rooms = len(out.Reservations)
	return out, nil
}

// contactProfile is the guest the contact of a group makes: the booker of its rooms.
func contactProfile(name, email, phone string) guests.Profile {
	first, last := splitName(name)
	return guests.Profile{FirstName: first, LastName: last, Email: strings.TrimSpace(email), Phone: strings.TrimSpace(phone)}
}

func (s *Service) createGroupRooms(ctx context.Context, p auth.Principal, propertyID, groupID int64, key, hash string, in BatchInput, decimals int32) (BatchResult, error) {
	var out BatchResult
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		// Every lock now, in order, before the first document number: the room types of every row, ascending (L2), then the group (L4: its dates and company stay as read,
		// and two bulk bookings of one group take turns, so the running numbers of the placeholders do not repeat).
		if in.confirm() {
			if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, in.typeIDs()); err != nil {
				return mapNotFound(err, errRoomTypeNotFound())
			}
		}
		if err := db.LockRows(ctx, db.Groups, db.ForUpdate, propertyID, []int64{groupID}); err != nil {
			return mapNotFound(err, errGroupNotFound())
		}
		q := s.q(ctx)
		g, err := q.GroupForBatch(ctx, reservationsdb.GroupForBatchParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
		if err != nil {
			return orNotFound(err, errGroupNotFound())
		}
		if !g.IsActive {
			return apperr.Conflict("GROUP_INACTIVE", "the group is inactive")
		}
		var fields []apperr.FieldError
		for i, r := range in.Rows {
			if r.Arrival.Before(g.ArrivalDate) || r.Departure.After(g.DepartureDate) {
				fields = append(fields, fieldErr(fmt.Sprintf("rows[%d].arrival_date", i), "OUTSIDE_GROUP_DATES", "the stay must be within the group's dates"))
			}
		}
		if len(fields) > 0 {
			return apperr.Invalid("the request is invalid", fields...)
		}

		// The booker: the one the request names, else the guest made from the contact of the group (made now, once).
		booker := in.BookerGuestID
		if booker != nil {
			if err := s.requireGuest(ctx, booker); err != nil {
				return err
			}
		} else if g.ContactGuestID != nil {
			booker = g.ContactGuestID
		}
		var contact *guests.Profile
		if booker == nil {
			if g.ContactName == nil || strings.TrimSpace(*g.ContactName) == "" {
				return apperr.Conflict("GROUP_CONTACT_REQUIRED", "the group has no contact: give it one, or name the booker of the rooms")
			}
			c := contactProfile(*g.ContactName, deref(g.ContactEmail), deref(g.ContactPhone))
			contact = &c
		}
		base, err := q.CountGroupLines(ctx, reservationsdb.CountGroupLinesParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
		if err != nil {
			return err
		}
		names := make([]string, in.rooms())
		for i := range names {
			names[i] = placeholderName(g.Name, int(base)+i+1)
		}
		made, err := s.guests.CreateForGroup(ctx, propertyID, contact, names) // sequences: after every lock above
		if err != nil {
			return err
		}
		if made.Contact != nil {
			booker = &made.Contact.ID
			if err := q.SetGroupContactGuest(ctx, reservationsdb.SetGroupContactGuestParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID, GuestID: booker, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}

		// The rate of a row that differs from the price is an override of each night that differs: one reason and one approval for the request (the engine verifies it once for
		// the context, whatever the number of rooms).
		overrides := make([][]NightOverride, len(in.Rows))
		for i, r := range in.Rows {
			if r.RatePerNight == "" {
				continue
			}
			if overrides[i], err = s.rowOverrides(ctx, p, propertyID, r, decimals); err != nil {
				return rowError(err, i, 1)
			}
		}
		bctx := withBatch(ctx, &batchState{group: groupID, quiet: true, noMail: true})
		bctx = WithRestrictionOverride(bctx, in.RestrictionOverride)
		bctx = WithFreeApproval(WithOverrideApproval(bctx, in.RateOverrideApproval, in.RateOverrideReason), in.OccupancyApproval, in.ExceedFreeQuota)

		pos, nights := 0, 0
		for i, r := range in.Rows {
			for k := 0; k < r.Count; k++ {
				pos++
				guest := made.Placeholders[pos-1].ID
				ci := CreateInput{
					GuestID: booker, Source: in.source(), BookingGroupID: &groupID, Confirm: in.confirm(),
					Rooms: []LineInput{{
						RoomTypeID: r.RoomTypeID, RatePlanID: r.RatePlanID, Arrival: r.Arrival, Departure: r.Departure, Adults: r.Adults, Children: r.Children,
						GuestID: &guest, Overrides: overrides[i], OccupancyReason: r.OccupancyReason,
					}},
				}
				if _, err := s.create(bctx, p, propertyID, fmt.Sprintf("%s#%d", key, pos), hash, ci, &bd); err != nil {
					return rowError(err, i, k+1)
				}
				nights += r.Arrival.DaysUntil(r.Departure)
			}
		}
		data := withOverrideAudit(bctx, map[string]any{
			"rows": len(in.Rows), "rooms": pos, "nights": nights, "confirmed": in.confirm(), "key": key, "booker_guest_id": booker,
			"placeholder_guests": len(made.Placeholders),
		})
		if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: "group.rooms_added",
			EntityType: "booking_group", EntityID: groupID, EntityLabel: g.Code, New: data}); err != nil {
			return err
		}
		out, err = s.batchResult(ctx, p, propertyID, groupID, key, in)
		return err
	})
	return out, err
}

// rowOverrides is the nightly overrides of a row that has its own rate: every night whose price in force is not that rate.
func (s *Service) rowOverrides(ctx context.Context, p auth.Principal, propertyID int64, r BatchRow, decimals int32) ([]NightOverride, error) {
	rate, err := rates.ParseAmount(r.RatePerNight, decimals)
	if err != nil {
		return nil, apperr.Invalid("the rate is invalid", fieldErr("rooms[0].rate_per_night", "INVALID_AMOUNT", "a non-negative amount"))
	}
	grid, _, err := s.rates.PriceNights(ctx, p.TenantID, propertyID, r.RatePlanID, r.RoomTypeID, r.Arrival, r.Departure)
	if err != nil {
		return nil, err
	}
	by := map[civil.Date]decimal.Decimal{}
	for _, n := range grid.Nights {
		by[n.Date] = n.Amount
	}
	var out []NightOverride
	for d := r.Arrival; d.Before(r.Departure); d = d.AddDays(1) {
		if sold, ok := by[d]; ok && sold.Equal(rate) {
			continue
		}
		out = append(out, NightOverride{Date: d, Amount: r.RatePerNight})
	}
	return out, nil
}

// ---------------------------------------------------------------------------------------------------------------
// The preview

// PreviewNight is the price of one night.
type PreviewNight struct {
	Date   civil.Date `json:"date"`
	Amount string     `json:"amount"`
}

// PreviewProblem is something that stands in the way of a row.
type PreviewProblem struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Date    *civil.Date `json:"date,omitempty"`
}

// PreviewShort says the row does not fit: the first night it is short, and by how many rooms.
type PreviewShort struct {
	Date    civil.Date `json:"date"`
	Missing int        `json:"missing"`
}

// PreviewRow is what one row would do.
type PreviewRow struct {
	Row          int    `json:"row"`
	RoomTypeID   int64  `json:"room_type_id"`
	RoomTypeCode string `json:"room_type_code"`
	Rooms        int    `json:"rooms"`
	Nights       int    `json:"nights"`
	// Nightly is the price in force of each night; Uniform says they are all the same, and then RatePerNight is that price (the default of the form).
	Nightly      []PreviewNight   `json:"nightly"`
	Uniform      bool             `json:"uniform"`
	GridRate     string           `json:"grid_rate,omitempty"`
	RatePerNight string           `json:"rate_per_night"`
	Override     bool             `json:"override"`
	RoomEstimate Estimate         `json:"room_estimate"`
	RowEstimate  Estimate         `json:"row_estimate"`
	Short        *PreviewShort    `json:"short,omitempty"`
	Problems     []PreviewProblem `json:"problems"`
}

// PreviewAvailNight is a night of a room type after the whole request: what is free, what the request takes, what is left (negative: short).
type PreviewAvailNight struct {
	Date      civil.Date `json:"date"`
	Available int        `json:"available"`
	Needed    int        `json:"needed"`
	Left      int        `json:"left"`
}

// PreviewAvail is a room type over the nights the request touches.
type PreviewAvail struct {
	RoomTypeID int64               `json:"room_type_id"`
	Code       string              `json:"code"`
	Nights     []PreviewAvailNight `json:"nights"`
}

// BatchPreview is what a request would do, read without a lock and without a write: a snapshot, and the save asks again under the locks.
type BatchPreview struct {
	Rows         []PreviewRow   `json:"rows"`
	Availability []PreviewAvail `json:"availability"`
	Rooms        int            `json:"rooms"`
	Nights       int            `json:"nights"`
	Total        Estimate       `json:"total"`
	// NeedsApproval says a row has a rate of its own and the caller cannot approve it: the save will ask for a reason and the credentials of an approver.
	NeedsApproval bool `json:"needs_approval"`
	OK            bool `json:"ok"`
}

func problemOf(err error) PreviewProblem {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return PreviewProblem{Code: ae.Code, Message: ae.Message}
	}
	return PreviewProblem{Code: "ERROR", Message: err.Error()}
}

// PreviewGroupRooms says what a request would do for a group (reservation.create): the price of each night, the estimate, whether the rooms are there after the rows before them, and what
// stands in the way of a row. A row that cannot be priced or sold is a problem of that row, not an error of the request.
func (s *Service) PreviewGroupRooms(ctx context.Context, propertyID, groupID int64, in BatchInput) (BatchPreview, error) {
	p, err := s.writer(ctx, propertyID, auth.PermReservationCreate)
	if err != nil {
		return BatchPreview{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return BatchPreview{}, err
	}
	if fields := in.validate(decimals); len(fields) > 0 {
		return BatchPreview{}, apperr.Invalid("the request is invalid", fields...)
	}
	var out BatchPreview
	err = s.txm.WithinReadTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		g, err := s.q(ctx).GroupForBatch(ctx, reservationsdb.GroupForBatchParams{TenantID: p.TenantID, PropertyID: propertyID, ID: groupID})
		if err != nil {
			return orNotFound(err, errGroupNotFound())
		}
		types, err := s.bookingTypes(ctx, p.TenantID, propertyID, in.typeIDs())
		if err != nil {
			return err
		}
		canOverride := s.authz.Require(ctx, propertyID, auth.PermReservationOverrideRate) == nil
		canApprove := s.authz.Require(ctx, propertyID, auth.PermReservationOverrideApprove) == nil

		var dates []civil.Date
		for _, r := range in.Rows {
			for d := r.Arrival; d.Before(r.Departure); d = d.AddDays(1) {
				if !slices.Contains(dates, d) {
					dates = append(dates, d)
				}
			}
		}
		slices.SortFunc(dates, func(a, b civil.Date) int { return a.DaysUntil(b) })
		inv, err := s.avail.Inventory(ctx, p.TenantID, propertyID, in.typeIDs(), dates, bd, nil)
		if err != nil {
			return err
		}
		needed := map[int64]map[civil.Date]int{}

		for i, r := range in.Rows {
			t := types[r.RoomTypeID]
			row := PreviewRow{Row: i, RoomTypeID: r.RoomTypeID, RoomTypeCode: t.Code, Rooms: r.Count, Nights: r.Arrival.DaysUntil(r.Departure), Nightly: []PreviewNight{}, Problems: []PreviewProblem{}}
			add := func(code, msg string, d *civil.Date) {
				row.Problems = append(row.Problems, PreviewProblem{Code: code, Message: msg, Date: d})
			}
			if !t.IsActive {
				add("ROOM_TYPE_INACTIVE", "the room type is inactive", nil)
			}
			if r.Arrival.Before(bd) {
				add("IN_THE_PAST", "the arrival is before the business date", nil)
			}
			if r.Arrival.Before(g.ArrivalDate) || r.Departure.After(g.DepartureDate) {
				add("OUTSIDE_GROUP_DATES", "the stay must be within the group's dates", nil)
			}
			if !availability.FitsOccupancy(r.Adults, r.Children, int(t.MaxAdult), int(t.MaxChild), int(t.MaxOccupancy)) {
				add("OCCUPANCY_EXCEEDED", "the party exceeds the room type limits", nil)
			}
			if v, err := s.avail.EvaluateStay(ctx, p.TenantID, propertyID, availability.StayRequest{RoomTypeID: r.RoomTypeID, RatePlanID: r.RatePlanID, Arrival: r.Arrival, Departure: r.Departure, BusinessDate: bd}); err != nil {
				row.Problems = append(row.Problems, problemOf(err))
			} else {
				for _, x := range v.Violations {
					d := x.Date
					add("STAY_RESTRICTED", string(x.Type), &d)
				}
			}
			grid, missing, err := s.rates.PriceNights(ctx, p.TenantID, propertyID, r.RatePlanID, r.RoomTypeID, r.Arrival, r.Departure)
			if err != nil {
				row.Problems = append(row.Problems, problemOf(err))
			} else {
				by := map[civil.Date]decimal.Decimal{}
				for _, n := range grid.Nights {
					by[n.Date] = n.Amount
					row.Nightly = append(row.Nightly, PreviewNight{Date: n.Date, Amount: n.Amount.StringFixed(decimals)})
				}
				if len(missing) > 0 {
					d := missing[0]
					add("RATE_NOT_SET", fmt.Sprintf("%d night(s) have no rate for this plan and room type", len(missing)), &d)
				}
				row.Uniform = len(row.Nightly) > 0 && len(missing) == 0
				for _, n := range row.Nightly {
					if n.Amount != row.Nightly[0].Amount {
						row.Uniform = false
					}
				}
				if row.Uniform {
					row.GridRate = row.Nightly[0].Amount
					row.RatePerNight = row.GridRate
				}
				sold := func(d civil.Date) (decimal.Decimal, bool) { a, ok := by[d]; return a, ok }
				if r.RatePerNight != "" {
					rate, _ := rates.ParseAmount(r.RatePerNight, decimals)
					row.RatePerNight = rate.StringFixed(decimals)
					for d := r.Arrival; d.Before(r.Departure); d = d.AddDays(1) {
						if a, ok := by[d]; !ok || !a.Equal(rate) {
							row.Override = true
						}
					}
					sold = func(civil.Date) (decimal.Decimal, bool) { return rate, true }
					if row.Override && !canOverride {
						add("PERMISSION_DENIED", "a rate that differs from the price needs reservation.override_rate", nil)
					}
					if row.Override && grid.OccupancyKind != rates.KindPaid {
						add("OVERRIDE_NOT_ALLOWED", "a complimentary or house use room is not priced", nil)
					}
					if row.Override && canOverride && !canApprove {
						out.NeedsApproval = true
					}
				}
				var charges []billingconfig.NightCharge
				for d := r.Arrival; d.Before(r.Departure); d = d.AddDays(1) {
					if a, ok := sold(d); ok {
						charges = append(charges, billingconfig.NightCharge{ChargeCodeID: grid.RoomChargeCodeID, PriceMode: chargecalc.PriceMode(grid.PriceMode), Amount: a})
					}
				}
				if est, err := s.billing.Estimate(ctx, p.TenantID, propertyID, charges); err == nil {
					row.RoomEstimate = Estimate{Net: est.Net, Service: est.Service, Tax: est.Tax, Total: est.Total}
					n := decimal.NewFromInt(int64(r.Count))
					row.RowEstimate = Estimate{Net: est.Net.Mul(n), Service: est.Service.Mul(n), Tax: est.Tax.Mul(n), Total: est.Total.Mul(n)}
				}
			}
			// the rooms left after this row and the rows before it
			if needed[r.RoomTypeID] == nil {
				needed[r.RoomTypeID] = map[civil.Date]int{}
			}
			for d := r.Arrival; d.Before(r.Departure); d = d.AddDays(1) {
				needed[r.RoomTypeID][d] += r.Count
			}
			for d := r.Arrival; d.Before(r.Departure); d = d.AddDays(1) {
				if left := inv[r.RoomTypeID][d].Available - needed[r.RoomTypeID][d]; left < 0 && (row.Short == nil || -left > row.Short.Missing) {
					row.Short = &PreviewShort{Date: d, Missing: -left}
				}
			}
			if row.Short != nil {
				d := row.Short.Date
				add("ROOM_TYPE_NOT_AVAILABLE", fmt.Sprintf("short by %d room(s)", row.Short.Missing), &d)
			}
			out.Rows = append(out.Rows, row)
			out.Rooms += r.Count
			out.Nights += r.Count * row.Nights
			out.Total = Estimate{Net: out.Total.Net.Add(row.RowEstimate.Net), Service: out.Total.Service.Add(row.RowEstimate.Service), Tax: out.Total.Tax.Add(row.RowEstimate.Tax), Total: out.Total.Total.Add(row.RowEstimate.Total)}
		}
		for _, id := range in.typeIDs() {
			pa := PreviewAvail{RoomTypeID: id, Code: types[id].Code}
			for _, d := range dates {
				if n, ok := needed[id][d]; ok {
					av := inv[id][d].Available
					pa.Nights = append(pa.Nights, PreviewAvailNight{Date: d, Available: av, Needed: n, Left: av - n})
				}
			}
			out.Availability = append(out.Availability, pa)
		}
		out.OK = true
		for _, r := range out.Rows {
			if len(r.Problems) > 0 {
				out.OK = false
			}
		}
		return nil
	})
	return out, err
}
