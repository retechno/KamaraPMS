package rates

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/availability"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/rates/ratesdb"
	"kamarapms/internal/tenancy"
)

// The sales restrictions grid (docs/architecture/18-architecture-decisions.md, decision 2): the table, the bulk fill and the reads. What a restriction means for a stay, and which
// row wins, is decided in one place, availability.Resolve and availability.EvaluateStay; this file only keeps the grid.

// Limits of one request.
const (
	MaxRestrictionScopes = 100 // room types x rate plans of one fill
	maxRestrictionNote   = 200
)

// Restriction is one row of the grid as the API shows it. A nil attribute is "no opinion at this level"; a nil room type or rate plan is "all".
type Restriction struct {
	ID                int64      `json:"id"`
	RoomTypeID        *int64     `json:"room_type_id"`
	RatePlanID        *int64     `json:"rate_plan_id"`
	StayDate          civil.Date `json:"stay_date"`
	StopSell          *bool      `json:"stop_sell"`
	ClosedToArrival   *bool      `json:"closed_to_arrival"`
	ClosedToDeparture *bool      `json:"closed_to_departure"`
	MinStay           *int       `json:"min_stay"`
	MaxStay           *int       `json:"max_stay"`
	Note              string     `json:"note,omitempty"`
}

// RestrictionSet is what a fill sets; nil leaves an attribute as it is. A note that is empty clears the note.
type RestrictionSet struct {
	StopSell          *bool
	ClosedToArrival   *bool
	ClosedToDeparture *bool
	MinStay           *int
	MaxStay           *int
	Note              *string
}

func (s RestrictionSet) any() bool {
	return s.StopSell != nil || s.ClosedToArrival != nil || s.ClosedToDeparture != nil || s.MinStay != nil || s.MaxStay != nil || s.Note != nil
}

// The attributes a fill can clear, by the names the API uses.
const (
	attrStopSell          = "stop_sell"
	attrClosedToArrival   = "closed_to_arrival"
	attrClosedToDeparture = "closed_to_departure"
	attrMinStay           = "min_stay"
	attrMaxStay           = "max_stay"
	attrNote              = "note"
)

var clearable = map[string]bool{attrStopSell: true, attrClosedToArrival: true, attrClosedToDeparture: true, attrMinStay: true, attrMaxStay: true, attrNote: true}

// FillRestrictionsInput sets or clears attributes of the grid for every listed scope on every selected date of [From, To). Empty RoomTypeIDs is the scope "every room type" (a row
// with no room type); empty RatePlanIDs is "every rate plan". The scopes are the product of the two lists.
type FillRestrictionsInput struct {
	RoomTypeIDs []int64
	RatePlanIDs []int64
	From        civil.Date
	To          civil.Date
	Weekdays    []string // MON..SUN; empty = every day
	Set         RestrictionSet
	Clear       []string
}

// FillRestrictionsResult reports what a fill did to the grid.
type FillRestrictionsResult struct {
	Dates   int   `json:"dates"`   // the dates of the range that fall on the selected weekdays
	Scopes  int   `json:"scopes"`  // room type x rate plan scopes written
	Created int64 `json:"created"` // rows added
	Updated int64 `json:"updated"` // rows changed
	Deleted int64 `json:"deleted"` // rows removed because nothing was left in them
}

func positiveUnique(field string, ids []int64, max int) []apperr.FieldError {
	if len(ids) > max {
		return []apperr.FieldError{fieldErr(field, "TOO_MANY", "at most "+strconv.Itoa(max))}
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id < 1 || seen[id] {
			return []apperr.FieldError{fieldErr(field, "INVALID_VALUE", "positive, unique ids")}
		}
		seen[id] = true
	}
	return nil
}

// Validate checks the request without the database.
func (in FillRestrictionsInput) Validate() []apperr.FieldError {
	var errs []apperr.FieldError
	errs = append(errs, positiveUnique("room_type_ids", in.RoomTypeIDs, MaxRoomTypes)...)
	errs = append(errs, positiveUnique("rate_plan_ids", in.RatePlanIDs, MaxRoomTypes)...)
	if scopes := max(len(in.RoomTypeIDs), 1) * max(len(in.RatePlanIDs), 1); scopes > MaxRestrictionScopes {
		errs = append(errs, fieldErr("room_type_ids", "TOO_MANY", "at most 100 room type and rate plan pairs"))
	}
	switch {
	case in.From.IsZero() || in.To.IsZero():
		errs = append(errs, fieldErr("from", "REQUIRED", "YYYY-MM-DD, to is exclusive"))
	case !in.To.After(in.From):
		errs = append(errs, fieldErr("to", "OUT_OF_RANGE", "must be after from (to is exclusive)"))
	case in.From.DaysUntil(in.To) > availability.MaxRestrictionDays:
		errs = append(errs, fieldErr("to", "SPAN_TOO_LONG", "at most 366 days"))
	}
	seenDay := map[string]bool{}
	for _, w := range in.Weekdays {
		if _, ok := weekdays[w]; !ok || seenDay[w] {
			errs = append(errs, fieldErr("weekdays", "INVALID_VALUE", "unique values from MON, TUE, WED, THU, FRI, SAT, SUN"))
			break
		}
		seenDay[w] = true
	}
	set := in.Set
	if !set.any() && len(in.Clear) == 0 {
		errs = append(errs, fieldErr("set", "NOTHING_TO_CHANGE", "set or clear at least one attribute"))
	}
	if set.MinStay != nil && (*set.MinStay < 1 || *set.MinStay > 365) {
		errs = append(errs, fieldErr("set.min_stay", "OUT_OF_RANGE", "between 1 and 365 nights"))
	}
	if set.MaxStay != nil && (*set.MaxStay < 1 || *set.MaxStay > 365) {
		errs = append(errs, fieldErr("set.max_stay", "OUT_OF_RANGE", "between 1 and 365 nights"))
	}
	if set.MinStay != nil && set.MaxStay != nil && *set.MinStay > *set.MaxStay {
		errs = append(errs, fieldErr("set.min_stay", "INVALID_VALUE", "not above the maximum stay"))
	}
	if set.Note != nil && len([]rune(*set.Note)) > maxRestrictionNote {
		errs = append(errs, fieldErr("set.note", "TOO_LONG", "at most 200 characters"))
	}
	seenClear := map[string]bool{}
	for _, name := range in.Clear {
		switch {
		case !clearable[name]:
			errs = append(errs, fieldErr("clear", "INVALID_VALUE", "stop_sell, closed_to_arrival, closed_to_departure, min_stay, max_stay or note"))
		case seenClear[name]:
			errs = append(errs, fieldErr("clear", "INVALID_VALUE", "each attribute once"))
		case in.sets(name):
			errs = append(errs, fieldErr("clear", "CONFLICT", name+" is set and cleared in one request"))
		}
		seenClear[name] = true
	}
	return errs
}

func (in FillRestrictionsInput) sets(name string) bool {
	s := in.Set
	switch name {
	case attrStopSell:
		return s.StopSell != nil
	case attrClosedToArrival:
		return s.ClosedToArrival != nil
	case attrClosedToDeparture:
		return s.ClosedToDeparture != nil
	case attrMinStay:
		return s.MinStay != nil
	case attrMaxStay:
		return s.MaxStay != nil
	default:
		return s.Note != nil
	}
}

// Dates lists the dates of [From, To) that fall on the selected weekdays.
func (in FillRestrictionsInput) Dates() []civil.Date {
	return FillInput{From: in.From, To: in.To, Weekdays: in.Weekdays}.civilDates()
}

func (in FillInput) civilDates() []civil.Date {
	var out []civil.Date
	for _, s := range in.Dates() {
		out = append(out, civil.MustParseDate(s))
	}
	return out
}

func pgInt2(v *int) pgtype.Int2 {
	if v == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: int16(*v), Valid: true} //nolint:gosec // G115: bounded to 1..365 by Validate
}

// FillRestrictions sets or clears attributes of the grid for the scopes and dates of the request (rate.manage), in one transaction, with one audit entry. A date that has no row for a
// scope gets one when something is set; a row that says nothing after the change is removed. Reservations that exist are never touched: a restriction is read when a stay is sold.
func (s *Service) FillRestrictions(ctx context.Context, propertyID int64, in FillRestrictionsInput) (FillRestrictionsResult, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return FillRestrictionsResult{}, err
	}
	if fields := in.Validate(); len(fields) > 0 {
		return FillRestrictionsResult{}, apperr.Invalid("the restrictions are invalid", fields...)
	}
	dates := in.Dates()
	if len(dates) == 0 {
		return FillRestrictionsResult{}, apperr.Invalid("the restrictions are invalid", fieldErr("weekdays", "NO_NIGHTS", "none of the selected weekdays falls in the range"))
	}
	types := append([]int64(nil), in.RoomTypeIDs...)
	plans := append([]int64(nil), in.RatePlanIDs...)
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })
	sort.Slice(plans, func(i, j int) bool { return plans[i] < plans[j] })

	var out FillRestrictionsResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		// L2: the room types (they exist and cannot change while the grid is written), then the plans, as FillRates does.
		if len(types) > 0 {
			if err := db.LockRows(ctx, db.RoomTypes, db.ForShare, propertyID, types); err != nil {
				if apperr.IsCode(err, "NOT_FOUND") {
					return errRoomTypeNotFound()
				}
				return err
			}
		}
		q := s.q(ctx)
		for _, id := range plans {
			if _, err := q.GetRatePlanForShare(ctx, ratesdb.GetRatePlanForShareParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
				return orNotFound(err, errRatePlanNotFound())
			}
		}
		scopeTypes, scopePlans := []*int64{nil}, []*int64{nil}
		if len(types) > 0 {
			scopeTypes = scopeTypes[:0]
			for i := range types {
				scopeTypes = append(scopeTypes, &types[i])
			}
		}
		if len(plans) > 0 {
			scopePlans = scopePlans[:0]
			for i := range plans {
				scopePlans = append(scopePlans, &plans[i])
			}
		}
		clear := map[string]bool{}
		for _, name := range in.Clear {
			clear[name] = true
		}
		set := in.Set
		note := set.Note
		if note != nil && *note == "" { // an empty note clears the note
			note, clear[attrNote] = nil, true
		}
		hasAttrSet := set.StopSell != nil || set.ClosedToArrival != nil || set.ClosedToDeparture != nil || set.MinStay != nil || set.MaxStay != nil
		for _, t := range scopeTypes {
			for _, pl := range scopePlans {
				out.Scopes++
				deleted, err := q.DeleteEmptiedRestrictions(ctx, ratesdb.DeleteEmptiedRestrictionsParams{
					TenantID: p.TenantID, PropertyID: propertyID, RoomTypeID: t, RatePlanID: pl, Dates: dates,
					ClearStopSell: clear[attrStopSell], SetStopSell: set.StopSell != nil, ClearCta: clear[attrClosedToArrival], SetCta: set.ClosedToArrival != nil,
					ClearCtd: clear[attrClosedToDeparture], SetCtd: set.ClosedToDeparture != nil, ClearMin: clear[attrMinStay], SetMin: set.MinStay != nil,
					ClearMax: clear[attrMaxStay], SetMax: set.MaxStay != nil, HasSet: hasAttrSet,
				})
				if err != nil {
					return err
				}
				out.Deleted += deleted
				if hasAttrSet { // a row must say something: only then can a missing row be created
					res, err := q.UpsertRestrictions(ctx, ratesdb.UpsertRestrictionsParams{
						TenantID: p.TenantID, PropertyID: propertyID, RoomTypeID: t, RatePlanID: pl, Dates: dates, ActorID: p.ActorID(),
						SetStopSell: set.StopSell != nil, StopSell: set.StopSell, ClearStopSell: clear[attrStopSell],
						SetCta: set.ClosedToArrival != nil, ClosedToArrival: set.ClosedToArrival, ClearCta: clear[attrClosedToArrival],
						SetCtd: set.ClosedToDeparture != nil, ClosedToDeparture: set.ClosedToDeparture, ClearCtd: clear[attrClosedToDeparture],
						SetMin: set.MinStay != nil, MinStay: pgInt2(set.MinStay), ClearMin: clear[attrMinStay],
						SetMax: set.MaxStay != nil, MaxStay: pgInt2(set.MaxStay), ClearMax: clear[attrMaxStay],
						SetNote: note != nil, Note: note, ClearNote: clear[attrNote],
					})
					if err != nil {
						return err
					}
					out.Created += res.Created
					out.Updated += res.Updated
					continue
				}
				updated, err := q.UpdateRestrictions(ctx, ratesdb.UpdateRestrictionsParams{
					TenantID: p.TenantID, PropertyID: propertyID, RoomTypeID: t, RatePlanID: pl, Dates: dates, ActorID: p.ActorID(),
					SetStopSell: false, ClearStopSell: clear[attrStopSell], ClearCta: clear[attrClosedToArrival], ClearCtd: clear[attrClosedToDeparture],
					ClearMin: clear[attrMinStay], ClearMax: clear[attrMaxStay], SetNote: note != nil, Note: note, ClearNote: clear[attrNote],
				})
				if err != nil {
					return err
				}
				out.Updated += updated
			}
		}
		out.Dates = len(dates)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "rate_restrictions.filled", "property", propertyID, auditlabel.Property(ctx, propertyID), nil, map[string]any{
			"room_type_ids": in.RoomTypeIDs, "rate_plan_ids": in.RatePlanIDs, "from": in.From, "to": in.To, "weekdays": in.Weekdays,
			"set": map[string]any{
				attrStopSell: set.StopSell, attrClosedToArrival: set.ClosedToArrival, attrClosedToDeparture: set.ClosedToDeparture,
				attrMinStay: set.MinStay, attrMaxStay: set.MaxStay, attrNote: set.Note,
			},
			"clear": in.Clear, "dates": out.Dates, "created": out.Created, "updated": out.Updated, "deleted": out.Deleted,
		}))
	})
	return out, err
}

// validWindow checks [from, to) of a read.
func validWindow(from, to civil.Date) []apperr.FieldError {
	switch {
	case from.IsZero() || to.IsZero():
		return []apperr.FieldError{fieldErr("from", "REQUIRED", "YYYY-MM-DD, to is exclusive")}
	case !to.After(from):
		return []apperr.FieldError{fieldErr("to", "OUT_OF_RANGE", "must be after from (to is exclusive)")}
	case from.DaysUntil(to) > availability.MaxRestrictionDays:
		return []apperr.FieldError{fieldErr("to", "SPAN_TOO_LONG", "at most 366 days")}
	}
	return nil
}

// ListRestrictions lists the rows of the grid in [from, to). With a room type or a rate plan it lists the rows that can speak for it (a row of a scope of "all" included).
func (s *Service) ListRestrictions(ctx context.Context, propertyID int64, from, to civil.Date, roomTypeID, ratePlanID *int64) ([]Restriction, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if fields := validWindow(from, to); len(fields) > 0 {
		return nil, apperr.Invalid("the range is invalid", fields...)
	}
	rows, err := s.q(ctx).ListRestrictionRows(ctx, ratesdb.ListRestrictionRowsParams{
		TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to, RoomTypeID: roomTypeID, RatePlanID: ratePlanID,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Restriction, len(rows))
	for i, r := range rows {
		out[i] = Restriction{
			ID: r.ID, RoomTypeID: r.RoomTypeID, RatePlanID: r.RatePlanID, StayDate: r.StayDate, StopSell: r.StopSell, ClosedToArrival: r.ClosedToArrival,
			ClosedToDeparture: r.ClosedToDeparture, MinStay: intPtr(r.MinStay), MaxStay: intPtr(r.MaxStay), Note: deref(r.Note),
		}
	}
	return out, nil
}

func intPtr(v pgtype.Int2) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int16)
	return &n
}

// EffectiveRestrictions is what each date of [from, to) is under for a room type and a rate plan, by the one precedence of the availability engine.
func (s *Service) EffectiveRestrictions(ctx context.Context, propertyID, roomTypeID, ratePlanID int64, from, to civil.Date) ([]availability.EffectiveDay, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	if _, err := q.GetRatePlan(ctx, ratesdb.GetRatePlanParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ratePlanID}); err != nil {
		return nil, orNotFound(err, errRatePlanNotFound())
	}
	if ok, err := q.RoomTypeExists(ctx, ratesdb.RoomTypeExistsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: roomTypeID}); err != nil {
		return nil, err
	} else if !ok {
		return nil, errRoomTypeNotFound()
	}
	return s.avail.EffectiveRestrictions(ctx, p.TenantID, propertyID, roomTypeID, ratePlanID, from, to)
}

// ---------------------------------------------------------------- HTTP

type fillRestrictionsRequest struct {
	RoomTypeIDs []int64  `json:"room_type_ids"`
	RatePlanIDs []int64  `json:"rate_plan_ids"`
	From        *string  `json:"from"`
	To          *string  `json:"to"`
	Weekdays    []string `json:"weekdays"`
	Set         struct {
		StopSell          *bool   `json:"stop_sell"`
		ClosedToArrival   *bool   `json:"closed_to_arrival"`
		ClosedToDeparture *bool   `json:"closed_to_departure"`
		MinStay           *int    `json:"min_stay"`
		MaxStay           *int    `json:"max_stay"`
		Note              *string `json:"note"`
	} `json:"set"`
	Clear []string `json:"clear"`
}

func (h *Handler) registerRestrictions(mux *http.ServeMux) {
	const p = "/api/v1/properties/{propertyId}"
	mux.Handle("GET "+p+"/rate-restrictions", httpx.HandlerFunc(h.listRestrictions))
	mux.Handle("PUT "+p+"/rate-restrictions", httpx.HandlerFunc(h.fillRestrictions))
	mux.Handle("GET "+p+"/rate-restrictions/effective", httpx.HandlerFunc(h.effectiveRestrictions))
}

func optionalID(r *http.Request, name string, errs *[]apperr.FieldError) *int64 {
	s := r.URL.Query().Get(name)
	if s == "" {
		return nil
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id < 1 {
		*errs = append(*errs, fieldErr(name, "INVALID_VALUE", "a positive integer"))
	}
	return &id
}

func (h *Handler) listRestrictions(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	from, to := queryDate(r, "from", &errs), queryDate(r, "to", &errs)
	typeID, planID := optionalID(r, "room_type_id", &errs), optionalID(r, "rate_plan_id", &errs)
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	rows, err := h.svc.ListRestrictions(r.Context(), pid, from, to, typeID, planID)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": rows})
}

func (h *Handler) effectiveRestrictions(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var errs []apperr.FieldError
	from, to := queryDate(r, "from", &errs), queryDate(r, "to", &errs)
	typeID, planID := optionalID(r, "room_type_id", &errs), optionalID(r, "rate_plan_id", &errs)
	if typeID == nil {
		errs = append(errs, fieldErr("room_type_id", "REQUIRED", "a room type id"))
	}
	if planID == nil {
		errs = append(errs, fieldErr("rate_plan_id", "REQUIRED", "a rate plan id"))
	}
	if len(errs) > 0 {
		return apperr.Invalid("the request is invalid", errs...)
	}
	if fields := validWindow(from, to); len(fields) > 0 {
		return apperr.Invalid("the range is invalid", fields...)
	}
	days, err := h.svc.EffectiveRestrictions(r.Context(), pid, *typeID, *planID, from, to)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": days})
}

func (h *Handler) fillRestrictions(w http.ResponseWriter, r *http.Request) error {
	pid, err := tenancy.PropertyID(r)
	if err != nil {
		return err
	}
	var req fillRestrictionsRequest
	if err := httpx.DecodeJSON(w, r, &req); err != nil {
		return err
	}
	var errs []apperr.FieldError
	date := func(field string, s *string) civil.Date {
		if s == nil {
			errs = append(errs, fieldErr(field, "REQUIRED", "YYYY-MM-DD"))
			return civil.Date{}
		}
		d, err := civil.ParseDate(*s)
		if err != nil {
			errs = append(errs, fieldErr(field, "INVALID_FORMAT", "YYYY-MM-DD"))
		}
		return d
	}
	in := FillRestrictionsInput{
		RoomTypeIDs: req.RoomTypeIDs, RatePlanIDs: req.RatePlanIDs, From: date("from", req.From), To: date("to", req.To), Weekdays: req.Weekdays,
		Set: RestrictionSet(req.Set), Clear: req.Clear,
	}
	if len(errs) > 0 {
		return apperr.Invalid("the restrictions are invalid", errs...)
	}
	res, err := h.svc.FillRestrictions(r.Context(), pid, in)
	if err != nil {
		return err
	}
	return httpx.WriteJSON(w, http.StatusOK, res)
}
