package reservations

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/guests"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rates"
	"kamarapms/internal/reservations/reservationsdb"
	"kamarapms/internal/tenancy"
)

// Service is the reservations application service. Availability is decided under the lock protocol of
// docs/architecture/05-transactions-locking.md: business day (L1), room types (L2), rooms (L3), the
// reservation and its lines (L4), sequences (L5).
type Service struct {
	txm         *db.TxManager
	clock       clock.Clock
	audit       *audit.Writer
	authz       auth.Authorizer
	days        *tenancy.Service
	avail       *availability.Service
	rates       *rates.Service
	billing     *billingconfig.Service
	guests      *guests.Service
	onConfirmed ConfirmedHook
}

// ConfirmedHook is told, inside the confirming transaction, that a reservation has been confirmed. The e-mail
// outbox uses it to queue the confirmation; an error rolls the confirmation back, so a hook must only write.
type ConfirmedHook interface {
	ReservationConfirmed(ctx context.Context, tenantID, propertyID, reservationID int64, bd civil.Date, actorID *int64) error
}

// SetConfirmedHook installs the hook (nil removes it). Call it once while wiring.
func (s *Service) SetConfirmedHook(h ConfirmedHook) { s.onConfirmed = h }

func (s *Service) confirmed(ctx context.Context, p auth.Principal, propertyID, reservationID int64, bd civil.Date) error {
	if s.onConfirmed == nil {
		return nil
	}
	return s.onConfirmed.ReservationConfirmed(ctx, p.TenantID, propertyID, reservationID, bd, p.ActorID())
}

// NewService wires the reservations service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service,
	avail *availability.Service, r *rates.Service, b *billingconfig.Service, g *guests.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, avail: avail, rates: r, billing: b, guests: g}
}

func (s *Service) q(ctx context.Context) *reservationsdb.Queries {
	return reservationsdb.New(s.txm.DB(ctx))
}

func errNotFound() *apperr.Error {
	return apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property")
}

func errLineNotFound() *apperr.Error {
	return apperr.NotFound("RESERVATION_ROOM_NOT_FOUND", "the reservation room does not exist")
}

func errRoomTypeNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_TYPE_NOT_FOUND", "the room type does not exist in this property")
}

func errRoomNotFound() *apperr.Error {
	return apperr.NotFound("ROOM_NOT_FOUND", "the room does not exist in this property")
}

func orNotFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

func (s *Service) writer(ctx context.Context, propertyID int64, perm auth.Permission) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, perm)
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action string, id int64, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: "reservation", EntityID: id, Old: old, New: updated,
	}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
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

func versionConflict(current int32) *apperr.Error {
	return apperr.Conflict("VERSION_CONFLICT", "the reservation was changed by someone else").WithContext("current_version", current)
}

func requireVersion(v int32) error {
	if v < 1 {
		return apperr.Invalid("the request is invalid", fieldErr("version", "REQUIRED", "the version you loaded"))
	}
	return nil
}

func requireReason(reason string) (string, error) {
	if reason == "" {
		return "", apperr.Invalid("a reason is required", fieldErr("reason", "REQUIRED", "a reason is required"))
	}
	if len([]rune(reason)) > maxReasonLen {
		return "", apperr.Invalid("the reason is too long", fieldErr("reason", "TOO_LONG", "at most 500 characters"))
	}
	return reason, nil
}

// ---------------------------------------------------------------------------
// Locking

// state is a reservation locked at L4 with the facts the callers need.
type state struct {
	res       reservationsdb.Reservation
	lines     []reservationsdb.ReservationRoom
	roomTypes map[int64]int64 // room id -> its physical room type
	bd        civil.Date
}

type lockOpts struct {
	inventory  bool    // take L2 (room types) and L3 (rooms) before the reservation
	extraTypes []int64 // room types the operation will also consume (a new type, an upgrade)
	extraRooms []int64 // rooms the operation will assign
}

func (st state) line(id int64) (reservationsdb.ReservationRoom, bool) {
	for _, l := range st.lines {
		if l.ID == id {
			return l, true
		}
	}
	return reservationsdb.ReservationRoom{}, false
}

// effType is the room type a line consumes: its assigned room's type, else the booked type.
func (st state) effType(l reservationsdb.ReservationRoom) int64 {
	return effectiveType(l.RoomTypeID, l.RoomID, st.roomTypes)
}

// lock runs the L1 -> L2 -> L3 -> L4 sequence for one reservation: the business day (share), then (for
// inventory operations) every room type and room the operation can touch, then the reservation and its
// lines. The reservation is read once unlocked to learn the lock sets and again under the lock; the version
// check makes the two reads agree. Must run inside a transaction.
func (s *Service) lock(ctx context.Context, tenantID, propertyID, id int64, version int32, o lockOpts) (state, error) {
	day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
	if err != nil {
		return state{}, err
	}
	q := s.q(ctx)
	pre, err := q.GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return state{}, orNotFound(err, errNotFound())
	}
	if pre.Version != version {
		return state{}, versionConflict(pre.Version)
	}
	preLines, err := q.ListLines(ctx, reservationsdb.ListLinesParams{TenantID: tenantID, PropertyID: propertyID, ReservationID: id})
	if err != nil {
		return state{}, err
	}
	roomIDs := slices.Clone(o.extraRooms)
	typeIDs := slices.Clone(o.extraTypes)
	lineIDs := make([]int64, 0, len(preLines))
	for _, l := range preLines {
		lineIDs = append(lineIDs, l.ID)
		typeIDs = append(typeIDs, l.RoomTypeID)
		if l.RoomID != nil {
			roomIDs = append(roomIDs, *l.RoomID)
		}
	}
	roomTypes, err := s.roomTypesOf(ctx, tenantID, propertyID, roomIDs)
	if err != nil {
		return state{}, err
	}
	if o.inventory {
		for _, t := range roomTypes {
			typeIDs = append(typeIDs, t)
		}
		if err := db.LockRows(ctx, db.RoomTypes, db.ForUpdate, propertyID, typeIDs); err != nil {
			return state{}, mapNotFound(err, errRoomTypeNotFound())
		}
		if err := db.LockRows(ctx, db.Rooms, db.ForUpdate, propertyID, roomIDs); err != nil {
			return state{}, mapNotFound(err, errRoomNotFound())
		}
	}
	if err := db.LockRows(ctx, db.Reservations, db.ForUpdate, propertyID, []int64{id}); err != nil {
		return state{}, mapNotFound(err, errNotFound())
	}
	if err := db.LockRows(ctx, db.ReservationRooms, db.ForUpdate, propertyID, lineIDs); err != nil {
		return state{}, mapNotFound(err, errLineNotFound())
	}
	res, err := q.GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return state{}, orNotFound(err, errNotFound())
	}
	if res.Version != version {
		return state{}, versionConflict(res.Version)
	}
	lines, err := q.ListLines(ctx, reservationsdb.ListLinesParams{TenantID: tenantID, PropertyID: propertyID, ReservationID: id})
	if err != nil {
		return state{}, err
	}
	return state{res: res, lines: lines, roomTypes: roomTypes, bd: day.BusinessDate}, nil
}

func mapNotFound(err error, nf *apperr.Error) error {
	if apperr.IsCode(err, "NOT_FOUND") {
		return nf
	}
	return err
}

// roomTypesOf maps room ids to their physical room type (an unknown room id is 404 ROOM_NOT_FOUND).
func (s *Service) roomTypesOf(ctx context.Context, tenantID, propertyID int64, roomIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(roomIDs) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).ListRoomsForBooking(ctx, reservationsdb.ListRoomsForBookingParams{TenantID: tenantID, PropertyID: propertyID, Ids: roomIDs})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r.RoomTypeID
	}
	for _, id := range roomIDs {
		if _, ok := out[id]; !ok {
			return nil, errRoomNotFound()
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Inventory checks

// hold is a CONFIRMED line to test against the inventory: it will consume typeID on [from, to), and hold
// roomID when assigned.
type hold struct {
	lineID int64
	typeID int64
	roomID *int64
	from   civil.Date
	to     civil.Date
}

// checkHolds tests that every hold fits: the room types have availability on every night (aggregated over
// all holds) and every assigned room is free. excludeLine removes one existing line's own demand (a line
// being amended, assigned or re-typed). Nights before the business date are not tested: they are past.
func (s *Service) checkHolds(ctx context.Context, tenantID, propertyID int64, bd civil.Date, holds []hold, excludeLine *int64) error {
	extra := availability.Extra{}
	for _, h := range holds {
		from := h.from
		if from.Before(bd) {
			from = bd
		}
		if h.to.After(from) {
			extra.Add(h.typeID, from, h.to, 1)
		}
	}
	if err := s.avail.RequireAvailable(ctx, tenantID, propertyID, bd, extra, excludeLine); err != nil {
		return err
	}
	for _, h := range holds {
		if h.roomID == nil {
			continue
		}
		own := h.lineID
		var excl *int64
		if own != 0 {
			excl = &own
		}
		issues, err := s.avail.RoomIssues(ctx, tenantID, propertyID, *h.roomID, bd, h.from, h.to, excl)
		if err != nil {
			return err
		}
		if len(issues) > 0 {
			return apperr.Conflict("ROOM_NOT_AVAILABLE", "the room is not free for these dates").
				WithContext("room_id", *h.roomID).WithContext("issues", issues)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Pricing

type rateRow struct {
	date       civil.Date
	base       *decimal.Decimal
	discount   decimal.Decimal
	amount     decimal.Decimal
	isOverride bool
}

type pricedLine struct {
	ratePlanID   int64
	chargeCodeID int64
	priceMode    string
	rows         []rateRow
}

// priceLine prices every night of [arrival, departure) from the grid; overrides replace single nights and
// need reservation.override_rate. A night with neither a grid price nor an override is 409 RATE_NOT_SET.
// keep lists nights whose stored snapshot is retained unchanged (the caller does not rewrite them).
func (s *Service) priceLine(ctx context.Context, propertyID, tenantID int64, prefix string, planID, typeID int64, arrival, departure civil.Date,
	overrides []NightOverride, decimals int32, keep map[civil.Date]bool) (pricedLine, error) {
	return s.priceLinePerm(ctx, auth.PermReservationOverrideRate, propertyID, tenantID, prefix, planID, typeID, arrival, departure, overrides, decimals, keep)
}

// priceLinePerm is priceLine with the permission overrides need (reservation.override_rate when booking,
// frontdesk.rate_change when a room move changes rates).
func (s *Service) priceLinePerm(ctx context.Context, perm auth.Permission, propertyID, tenantID int64, prefix string, planID, typeID int64, arrival, departure civil.Date,
	overrides []NightOverride, decimals int32, keep map[civil.Date]bool) (pricedLine, error) {
	byDate := map[civil.Date]NightOverride{}
	if len(overrides) > 0 {
		if err := s.authz.Require(ctx, propertyID, perm); err != nil {
			return pricedLine{}, err
		}
	}
	for i, o := range overrides {
		field := fmt.Sprintf("%snightly_overrides[%d]", prefix, i)
		if o.Date.Before(arrival) || !o.Date.Before(departure) {
			return pricedLine{}, apperr.Invalid("the override is invalid", fieldErr(field+".date", "OUT_OF_RANGE", "a night of the stay"))
		}
		if _, dup := byDate[o.Date]; dup {
			return pricedLine{}, apperr.Invalid("the override is invalid", fieldErr(field+".date", "DUPLICATE", "one override per night"))
		}
		if _, err := rates.ParseAmount(o.Amount, decimals); err != nil {
			return pricedLine{}, apperr.Invalid("the override is invalid", fieldErr(field+".amount", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals"))
		}
		if o.DiscountAmount != "" {
			if _, err := rates.ParseAmount(o.DiscountAmount, decimals); err != nil {
				return pricedLine{}, apperr.Invalid("the override is invalid", fieldErr(field+".discount_amount", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals"))
			}
		}
		byDate[o.Date] = o
	}
	grid, missing, err := s.rates.PriceNights(ctx, tenantID, propertyID, planID, typeID, arrival, departure)
	if err != nil {
		return pricedLine{}, err
	}
	gridBy := make(map[civil.Date]decimal.Decimal, len(grid.Nights))
	for _, n := range grid.Nights {
		gridBy[n.Date] = n.Amount
	}
	var unpriced []string
	out := pricedLine{ratePlanID: grid.RatePlanID, chargeCodeID: grid.RoomChargeCodeID, priceMode: grid.PriceMode}
	for _, m := range missing {
		if _, ok := byDate[m]; !ok && !keep[m] {
			unpriced = append(unpriced, m.String())
		}
	}
	if len(unpriced) > 0 {
		shown := unpriced[:min(len(unpriced), 31)]
		return pricedLine{}, apperr.Conflict("RATE_NOT_SET", "some nights have no rate for this plan and room type").
			WithContext("nights", shown).WithContext("missing_nights", len(unpriced))
	}
	for d := arrival; d.Before(departure); d = d.AddDays(1) {
		if keep[d] {
			if _, over := byDate[d]; !over {
				continue
			}
		}
		row := rateRow{date: d}
		if g, ok := gridBy[d]; ok {
			gg := g
			row.base = &gg
			row.amount = g
		}
		if o, ok := byDate[d]; ok {
			row.isOverride = true
			row.amount, _ = rates.ParseAmount(o.Amount, decimals)
			if o.DiscountAmount != "" {
				row.discount, _ = rates.ParseAmount(o.DiscountAmount, decimals)
			}
		}
		out.rows = append(out.rows, row)
	}
	return out, nil
}

func (s *Service) storeRates(ctx context.Context, p auth.Principal, propertyID, lineID int64, priced pricedLine) error {
	q := s.q(ctx)
	for _, r := range priced.rows {
		if err := q.InsertNightRate(ctx, reservationsdb.InsertNightRateParams{
			TenantID: p.TenantID, PropertyID: propertyID, LineID: lineID, StayDate: r.date, RatePlanID: priced.ratePlanID,
			ChargeCodeID: priced.chargeCodeID, PriceMode: priced.priceMode, BaseRate: r.base, DiscountAmount: r.discount,
			Amount: r.amount, IsOverride: r.isOverride, ActorID: p.ActorID(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) decimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

// bookingTypes loads the room types of a set of lines and rejects unknown or inactive ones.
func (s *Service) bookingTypes(ctx context.Context, tenantID, propertyID int64, ids []int64) (map[int64]reservationsdb.GetRoomTypesForBookingRow, error) {
	rows, err := s.q(ctx).GetRoomTypesForBooking(ctx, reservationsdb.GetRoomTypesForBookingParams{TenantID: tenantID, PropertyID: propertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	out := map[int64]reservationsdb.GetRoomTypesForBookingRow{}
	for _, r := range rows {
		out[r.ID] = r
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, errRoomTypeNotFound()
		}
	}
	return out, nil
}

func requireActiveType(t reservationsdb.GetRoomTypesForBookingRow) error {
	if !t.IsActive {
		return apperr.Conflict("ROOM_TYPE_INACTIVE", "the room type is inactive").WithContext("room_type", t.Code)
	}
	return nil
}

func (s *Service) requireGuest(ctx context.Context, id *int64) error {
	if id == nil {
		return nil
	}
	return s.guests.RequireVisible(ctx, *id)
}

// resolveLinks checks the company and booking group of a reservation. A group fixes the company when it has one,
// and every line must fall inside the group's dates. A link below 1 is no link.
func (s *Service) resolveLinks(ctx context.Context, tenantID, propertyID int64, company, group *int64, lines []LineInput) (*int64, *int64, error) {
	if company != nil && *company < 1 {
		company = nil
	}
	if group != nil && *group < 1 {
		group = nil
	}
	q := s.q(ctx)
	if group != nil {
		// L4: a share lock keeps the group's dates, company and active flag as read until this transaction ends.
		if err := db.LockRows(ctx, db.Groups, db.ForShare, propertyID, []int64{*group}); err != nil {
			return nil, nil, mapNotFound(err, apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property"))
		}
		g, err := q.GroupRef(ctx, reservationsdb.GroupRefParams{TenantID: tenantID, PropertyID: propertyID, ID: *group})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, apperr.NotFound("GROUP_NOT_FOUND", "the group does not exist in this property")
		}
		if err != nil {
			return nil, nil, err
		}
		if !g.IsActive {
			return nil, nil, apperr.Conflict("GROUP_INACTIVE", "the group is inactive")
		}
		var fields []apperr.FieldError
		for i, l := range lines {
			if l.Arrival.Before(g.ArrivalDate) || l.Departure.After(g.DepartureDate) {
				fields = append(fields, fieldErr(fmt.Sprintf("rooms[%d].arrival_date", i), "OUTSIDE_GROUP_DATES", "the stay must be within the group's dates"))
			}
		}
		if len(fields) > 0 {
			return nil, nil, apperr.Invalid("the reservation is invalid", fields...)
		}
		switch {
		case company == nil:
			company = g.CompanyID
		case g.CompanyID != nil && *g.CompanyID != *company:
			return nil, nil, apperr.Invalid("the reservation is invalid", fieldErr("company_id", "GROUP_COMPANY_MISMATCH", "the group belongs to another company"))
		}
	}
	if company != nil {
		c, err := q.CompanyRef(ctx, reservationsdb.CompanyRefParams{TenantID: tenantID, PropertyID: propertyID, ID: *company})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, apperr.NotFound("COMPANY_NOT_FOUND", "the company does not exist in this property")
		}
		if err != nil {
			return nil, nil, err
		}
		if !c.IsActive {
			return nil, nil, apperr.Conflict("COMPANY_INACTIVE", "the company is inactive")
		}
	}
	return company, group, nil
}
