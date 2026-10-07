package availability

import (
	"context"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"

	"kamarapms/internal/availability/availabilitydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// Sales restrictions (docs/architecture/18-architecture-decisions.md, decision 2). Whether a night can be sold at all is a different question from whether a room is
// left (that is the inventory, in this package too): a restriction says stop sell, closed to arrival, closed to departure, and the shortest and longest stay. This file is the
// only place that reads the grid and the only place that decides: reservations, the front desk, search and the calendar must ask EvaluateStay and never repeat the rules.
//
// The grid is `rate_restrictions`: one row per date and scope, the scope being a room type and a rate plan, each of them optional (nil is "all"). A row says something about
// an attribute or nothing (nil): nil means "no opinion at this level".

// Restriction is one row of the grid.
type Restriction struct {
	ID                int64
	RoomTypeID        *int64 // nil: every room type
	RatePlanID        *int64 // nil: every rate plan
	Date              civil.Date
	StopSell          *bool
	ClosedToArrival   *bool
	ClosedToDeparture *bool
	MinStay           *int
	MaxStay           *int
}

// Scope names how specific a row is, which decides which row wins. Most to least specific: a room type and a plan, a room type, a plan, the whole property.
type Scope int

// The scopes, in increasing order of specificity.
const (
	ScopeProperty Scope = iota
	ScopeRatePlan
	ScopeRoomType
	ScopeRoomTypeAndPlan
)

// String is the name used in the API.
func (s Scope) String() string {
	switch s {
	case ScopeRoomTypeAndPlan:
		return "ROOM_TYPE_AND_PLAN"
	case ScopeRoomType:
		return "ROOM_TYPE"
	case ScopeRatePlan:
		return "RATE_PLAN"
	default:
		return "PROPERTY"
	}
}

// ScopeOf is the scope of a row. The room type ranks above the plan: when a row for the room type and a row for the plan both give a value, the room type wins.
func ScopeOf(r Restriction) Scope {
	switch {
	case r.RoomTypeID != nil && r.RatePlanID != nil:
		return ScopeRoomTypeAndPlan
	case r.RoomTypeID != nil:
		return ScopeRoomType
	case r.RatePlanID != nil:
		return ScopeRatePlan
	default:
		return ScopeProperty
	}
}

// Decided is the value of one attribute for a night and the row it came from.
type Decided[T any] struct {
	Value T
	Row   int64 // the row that decided; 0 when no row has a value (the attribute is open)
	Scope Scope // the scope of that row
}

// Effective is what a night is under, after the precedence: for each attribute, the value of the most specific row that has one. An attribute no row speaks of is open
// (not closed, no minimum, no maximum).
type Effective struct {
	StopSell          Decided[bool]
	ClosedToArrival   Decided[bool]
	ClosedToDeparture Decided[bool]
	MinStay           Decided[*int]
	MaxStay           Decided[*int]
}

// applies says whether a row speaks for the room type, the rate plan and the date.
func applies(r Restriction, roomTypeID, ratePlanID int64, date civil.Date) bool {
	return r.Date.Equal(date) && (r.RoomTypeID == nil || *r.RoomTypeID == roomTypeID) && (r.RatePlanID == nil || *r.RatePlanID == ratePlanID)
}

// decide picks, among the rows that apply, the one with the highest scope that has a value for the attribute. Two rows of one scope and date cannot exist (the table has a
// unique key); should a caller pass both, the lower id wins, so the answer never depends on the order of the rows.
func decide[T any](rows []Restriction, roomTypeID, ratePlanID int64, date civil.Date, get func(Restriction) (T, bool)) Decided[T] {
	var out Decided[T]
	found := false
	for _, r := range rows {
		if !applies(r, roomTypeID, ratePlanID, date) {
			continue
		}
		v, ok := get(r)
		if !ok {
			continue
		}
		sc := ScopeOf(r)
		if !found || sc > out.Scope || (sc == out.Scope && r.ID < out.Row) {
			out, found = Decided[T]{Value: v, Row: r.ID, Scope: sc}, true
		}
	}
	return out
}

// Resolve is the precedence of the grid for one night of a room type and a rate plan. It is pure: it reads only the rows it is given.
func Resolve(rows []Restriction, roomTypeID, ratePlanID int64, date civil.Date) Effective {
	b := func(f func(Restriction) *bool) func(Restriction) (bool, bool) {
		return func(r Restriction) (bool, bool) {
			if v := f(r); v != nil {
				return *v, true
			}
			return false, false
		}
	}
	n := func(f func(Restriction) *int) func(Restriction) (*int, bool) {
		return func(r Restriction) (*int, bool) {
			v := f(r)
			return v, v != nil
		}
	}
	return Effective{
		StopSell:          decide(rows, roomTypeID, ratePlanID, date, b(func(r Restriction) *bool { return r.StopSell })),
		ClosedToArrival:   decide(rows, roomTypeID, ratePlanID, date, b(func(r Restriction) *bool { return r.ClosedToArrival })),
		ClosedToDeparture: decide(rows, roomTypeID, ratePlanID, date, b(func(r Restriction) *bool { return r.ClosedToDeparture })),
		MinStay:           decide(rows, roomTypeID, ratePlanID, date, n(func(r Restriction) *int { return r.MinStay })),
		MaxStay:           decide(rows, roomTypeID, ratePlanID, date, n(func(r Restriction) *int { return r.MaxStay })),
	}
}

// ViolationType is the kind of restriction a stay breaks.
type ViolationType string

// The five restrictions.
const (
	StopSell          ViolationType = "STOP_SELL"
	ClosedToArrival   ViolationType = "CLOSED_TO_ARRIVAL"
	ClosedToDeparture ViolationType = "CLOSED_TO_DEPARTURE"
	MinStay           ViolationType = "MIN_STAY"
	MaxStay           ViolationType = "MAX_STAY"
)

// Violation is one restriction a stay breaks. Date is the night (stop sell), the arrival date (closed to arrival, minimum and maximum stay) or the departure date (closed to
// departure). Value is the limit for a minimum or maximum stay, and Nights the length that broke it. Row and Scope say which row of the grid decided.
type Violation struct {
	Type       ViolationType `json:"type"`
	Date       civil.Date    `json:"date"`
	RoomTypeID int64         `json:"room_type_id"`
	RatePlanID int64         `json:"rate_plan_id"`
	Scope      string        `json:"scope"`
	Value      *int          `json:"value,omitempty"`
	Nights     int           `json:"nights,omitempty"`
	RowID      int64         `json:"row_id"`
}

// StayDates is a stay as it was before a change.
type StayDates struct {
	Arrival, Departure civil.Date
}

// StayRequest is a stay to be sold, or a change to a stay that was sold.
//
// With Previous nil the whole stay is new: every night, the arrival, the departure and the length are asked. With Previous set, only what the change makes new is asked: the nights
// that were not in the old stay, the arrival if it moved, the departure if it moved, and the length if the arrival or the departure moved. A guest keeps what was sold.
//
// InHouse says the guest has arrived already (an extension): the arrival is behind them, so closed to arrival and the minimum stay are not asked; stop sell on the new nights,
// closed to departure on the new departure and the maximum stay still are.
type StayRequest struct {
	RoomTypeID   int64
	RatePlanID   int64
	Arrival      civil.Date
	Departure    civil.Date
	BusinessDate civil.Date
	Previous     *StayDates
	InHouse      bool
}

// Verdict is the answer to a StayRequest.
type Verdict struct {
	Violations []Violation `json:"violations"`
}

// Allowed is true when no restriction is broken.
func (v Verdict) Allowed() bool { return len(v.Violations) == 0 }

// Validate checks the request is well formed.
func (r StayRequest) Validate() error {
	var fields []apperr.FieldError
	if r.RoomTypeID < 1 {
		fields = append(fields, apperr.FieldError{Field: "room_type_id", Code: "REQUIRED", Message: "a room type"})
	}
	if r.RatePlanID < 1 {
		fields = append(fields, apperr.FieldError{Field: "rate_plan_id", Code: "REQUIRED", Message: "a rate plan"})
	}
	if !r.Departure.After(r.Arrival) || r.Arrival.DaysUntil(r.Departure) > MaxSearchNights {
		fields = append(fields, apperr.FieldError{Field: "departure_date", Code: "OUT_OF_RANGE", Message: "after arrival, at most 365 nights"})
	}
	if len(fields) > 0 {
		return apperr.Invalid("the stay is invalid", fields...)
	}
	return nil
}

// Window is the dates of the grid the request can look at: from the arrival to the departure, both included.
func (r StayRequest) Window() (from, to civil.Date) { return r.Arrival, r.Departure }

// Check is the evaluation of a request against the rows it is given. It is pure; EvaluateStay loads the rows. The order of the violations is stable: the nights of a stop
// sell in date order, then closed to arrival, closed to departure, the minimum and the maximum stay.
func Check(rows []Restriction, req StayRequest) []Violation {
	var out []Violation
	nights := req.Arrival.DaysUntil(req.Departure)
	violation := func(t ViolationType, d civil.Date, scope Scope, row int64) Violation {
		return Violation{Type: t, Date: d, RoomTypeID: req.RoomTypeID, RatePlanID: req.RatePlanID, Scope: scope.String(), RowID: row}
	}
	inOld := func(d civil.Date) bool {
		return req.Previous != nil && !d.Before(req.Previous.Arrival) && d.Before(req.Previous.Departure)
	}

	for d := req.Arrival; d.Before(req.Departure); d = d.AddDays(1) {
		if d.Before(req.BusinessDate) || inOld(d) { // a night that is past, or already sold, is not asked
			continue
		}
		if e := Resolve(rows, req.RoomTypeID, req.RatePlanID, d); e.StopSell.Row != 0 && e.StopSell.Value {
			out = append(out, violation(StopSell, d, e.StopSell.Scope, e.StopSell.Row))
		}
	}

	arrivalNew := req.Previous == nil || !req.Arrival.Equal(req.Previous.Arrival)
	departureNew := req.Previous == nil || !req.Departure.Equal(req.Previous.Departure)
	lengthNew := arrivalNew || departureNew

	atArrival := Resolve(rows, req.RoomTypeID, req.RatePlanID, req.Arrival)
	if arrivalNew && !req.InHouse {
		if e := atArrival.ClosedToArrival; e.Row != 0 && e.Value {
			out = append(out, violation(ClosedToArrival, req.Arrival, e.Scope, e.Row))
		}
	}
	if departureNew {
		if e := Resolve(rows, req.RoomTypeID, req.RatePlanID, req.Departure).ClosedToDeparture; e.Row != 0 && e.Value {
			out = append(out, violation(ClosedToDeparture, req.Departure, e.Scope, e.Row))
		}
	}
	if lengthNew && !req.InHouse {
		if e := atArrival.MinStay; e.Row != 0 && e.Value != nil && nights < *e.Value {
			v := violation(MinStay, req.Arrival, e.Scope, e.Row)
			v.Value, v.Nights = e.Value, nights
			out = append(out, v)
		}
	}
	if lengthNew {
		if e := atArrival.MaxStay; e.Row != 0 && e.Value != nil && nights > *e.Value {
			v := violation(MaxStay, req.Arrival, e.Scope, e.Row)
			v.Value, v.Nights = e.Value, nights
			out = append(out, v)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return violationRank(out[i].Type) < violationRank(out[j].Type) })
	return out
}

func violationRank(t ViolationType) int {
	switch t {
	case StopSell:
		return 0
	case ClosedToArrival:
		return 1
	case ClosedToDeparture:
		return 2
	case MinStay:
		return 3
	default:
		return 4
	}
}

// EvaluateStay answers whether a stay, or a change to one, breaks a restriction. It reads the grid for the room type and the rate plan over the dates of the stay and applies
// Check. It reads committed data inside the caller's transaction when there is one and takes no locks (a restriction is configuration, read under the locks the booking holds).
// It does not look at the inventory: a night can be unrestricted and still sold out.
func (s *Service) EvaluateStay(ctx context.Context, tenantID, propertyID int64, req StayRequest) (Verdict, error) {
	if err := req.Validate(); err != nil {
		return Verdict{}, err
	}
	from, to := req.Window()
	rows, err := s.q(ctx).ListRateRestrictions(ctx, availabilitydb.ListRateRestrictionsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomTypeID: &req.RoomTypeID, RatePlanID: &req.RatePlanID, FromDate: from, ToDate: to,
	})
	if err != nil {
		return Verdict{}, err
	}
	grid := gridOf(rows)
	return Verdict{Violations: append([]Violation{}, Check(grid, req)...)}, nil
}

func intOf(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int16)
	return &n
}

// Source says which row of the grid decided an attribute of a night.
type Source struct {
	RowID int64  `json:"row_id"`
	Scope string `json:"scope"`
}

// EffectiveDay is what one night is under, after the precedence, in the form the API and the screens show. An attribute no row speaks of is open (false, or no limit);
// Sources has an entry only for the attributes a row decided, so a screen can say where a value comes from. The screens never resolve the precedence themselves.
type EffectiveDay struct {
	Date              civil.Date        `json:"date"`
	StopSell          bool              `json:"stop_sell"`
	ClosedToArrival   bool              `json:"closed_to_arrival"`
	ClosedToDeparture bool              `json:"closed_to_departure"`
	MinStay           *int              `json:"min_stay"`
	MaxStay           *int              `json:"max_stay"`
	Sources           map[string]Source `json:"sources"`
}

// EffectiveOf turns the result of Resolve into an EffectiveDay.
func EffectiveOf(date civil.Date, e Effective) EffectiveDay {
	out := EffectiveDay{
		Date: date, StopSell: e.StopSell.Value, ClosedToArrival: e.ClosedToArrival.Value, ClosedToDeparture: e.ClosedToDeparture.Value,
		MinStay: e.MinStay.Value, MaxStay: e.MaxStay.Value, Sources: map[string]Source{},
	}
	add := func(name string, row int64, sc Scope) {
		if row != 0 {
			out.Sources[name] = Source{RowID: row, Scope: sc.String()}
		}
	}
	add("stop_sell", e.StopSell.Row, e.StopSell.Scope)
	add("closed_to_arrival", e.ClosedToArrival.Row, e.ClosedToArrival.Scope)
	add("closed_to_departure", e.ClosedToDeparture.Row, e.ClosedToDeparture.Scope)
	add("min_stay", e.MinStay.Row, e.MinStay.Scope)
	add("max_stay", e.MaxStay.Row, e.MaxStay.Scope)
	return out
}

// EffectiveRestrictions is the effective restrictions of a room type and a rate plan for every date of [from, to), by the same Resolve that EvaluateStay uses. It is what a grid
// screen shows and what a channel manager would export. At most 366 dates.
func (s *Service) EffectiveRestrictions(ctx context.Context, tenantID, propertyID, roomTypeID, ratePlanID int64, from, to civil.Date) ([]EffectiveDay, error) {
	var fields []apperr.FieldError
	if roomTypeID < 1 {
		fields = append(fields, apperr.FieldError{Field: "room_type_id", Code: "REQUIRED", Message: "a room type"})
	}
	if ratePlanID < 1 {
		fields = append(fields, apperr.FieldError{Field: "rate_plan_id", Code: "REQUIRED", Message: "a rate plan"})
	}
	if !to.After(from) || from.DaysUntil(to) > MaxRestrictionDays {
		fields = append(fields, apperr.FieldError{Field: "to", Code: "OUT_OF_RANGE", Message: "after from (exclusive), at most 366 days"})
	}
	if len(fields) > 0 {
		return nil, apperr.Invalid("the request is invalid", fields...)
	}
	rows, err := s.q(ctx).ListRateRestrictions(ctx, availabilitydb.ListRateRestrictionsParams{
		TenantID: tenantID, PropertyID: propertyID, RoomTypeID: &roomTypeID, RatePlanID: &ratePlanID, FromDate: from, ToDate: to.AddDays(-1),
	})
	if err != nil {
		return nil, err
	}
	grid := gridOf(rows)
	out := make([]EffectiveDay, 0, from.DaysUntil(to))
	for d := from; d.Before(to); d = d.AddDays(1) {
		out = append(out, EffectiveOf(d, Resolve(grid, roomTypeID, ratePlanID, d)))
	}
	return out, nil
}

// MaxRestrictionDays bounds a window of the grid: a request, a fill or a list.
const MaxRestrictionDays = 366

func gridOf(rows []availabilitydb.ListRateRestrictionsRow) []Restriction {
	grid := make([]Restriction, len(rows))
	for i, r := range rows {
		grid[i] = Restriction{
			ID: r.ID, RoomTypeID: r.RoomTypeID, RatePlanID: r.RatePlanID, Date: r.StayDate,
			StopSell: r.StopSell, ClosedToArrival: r.ClosedToArrival, ClosedToDeparture: r.ClosedToDeparture,
			MinStay: intOf(r.MinStay), MaxStay: intOf(r.MaxStay),
		}
	}
	return grid
}

// NightMark says what the grid does to a room type on one night, over the active rate plans: which restrictions are in force for at least one of them, and whether the
// night is closed for sale for every one of them (nothing can be sold). The calendar shows it; the rules are those of Resolve, never repeated.
type NightMark struct {
	Types       []ViolationType
	StopSellAll bool
}

// NightMarks is the NightMark of each room type and night of [from, to). A night with nothing in force has no entry.
func (s *Service) NightMarks(ctx context.Context, tenantID, propertyID int64, roomTypeIDs []int64, from, to civil.Date) (map[int64]map[civil.Date]NightMark, error) {
	out := map[int64]map[civil.Date]NightMark{}
	if !to.After(from) || len(roomTypeIDs) == 0 {
		return out, nil
	}
	q := s.q(ctx)
	plans, err := q.ListActiveRatePlanIDs(ctx, availabilitydb.ListActiveRatePlanIDsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil || len(plans) == 0 {
		return out, err
	}
	rows, err := q.ListRestrictionRowsInWindow(ctx, availabilitydb.ListRestrictionRowsInWindowParams{TenantID: tenantID, PropertyID: propertyID, FromDate: from, ToDate: to.AddDays(-1)})
	if err != nil {
		return nil, err
	}
	byDate := map[civil.Date][]Restriction{}
	for _, r := range gridOf2(rows) {
		byDate[r.Date] = append(byDate[r.Date], r)
	}
	for _, typeID := range roomTypeIDs {
		for d := from; d.Before(to); d = d.AddDays(1) {
			day := byDate[d]
			if len(day) == 0 {
				continue
			}
			var m NightMark
			seen := map[ViolationType]bool{}
			closed := 0
			for _, planID := range plans {
				e := Resolve(day, typeID, planID, d)
				if e.StopSell.Value {
					seen[StopSell] = true
					closed++
				}
				if e.ClosedToArrival.Value {
					seen[ClosedToArrival] = true
				}
				if e.ClosedToDeparture.Value {
					seen[ClosedToDeparture] = true
				}
				if e.MinStay.Value != nil {
					seen[MinStay] = true
				}
				if e.MaxStay.Value != nil {
					seen[MaxStay] = true
				}
			}
			for _, t := range []ViolationType{StopSell, ClosedToArrival, ClosedToDeparture, MinStay, MaxStay} {
				if seen[t] {
					m.Types = append(m.Types, t)
				}
			}
			m.StopSellAll = closed == len(plans)
			if len(m.Types) == 0 {
				continue
			}
			if out[typeID] == nil {
				out[typeID] = map[civil.Date]NightMark{}
			}
			out[typeID][d] = m
		}
	}
	return out, nil
}

// gridOf2 is gridOf for the rows of the window query (the same columns).
func gridOf2(rows []availabilitydb.ListRestrictionRowsInWindowRow) []Restriction {
	grid := make([]Restriction, len(rows))
	for i, r := range rows {
		grid[i] = Restriction{
			ID: r.ID, RoomTypeID: r.RoomTypeID, RatePlanID: r.RatePlanID, Date: r.StayDate,
			StopSell: r.StopSell, ClosedToArrival: r.ClosedToArrival, ClosedToDeparture: r.ClosedToDeparture,
			MinStay: intOf(r.MinStay), MaxStay: intOf(r.MaxStay),
		}
	}
	return grid
}
