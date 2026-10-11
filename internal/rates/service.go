package rates

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/availability"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rates/ratesdb"
	"kamarapms/internal/tenancy"
)

// Service is the rates application service.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
	avail *availability.Service
}

// NewService wires the rates service. avail answers how full the property is, for the occupancy rules of yield management.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service, avail *availability.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days, avail: avail}
}

func (s *Service) q(ctx context.Context) *ratesdb.Queries { return ratesdb.New(s.txm.DB(ctx)) }

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, label string, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: entity, EntityID: id, EntityLabel: label, Old: old, New: updated,
	}
}

func (s *Service) writer(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermRateManage)
}

func (s *Service) reader(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.CanAccess(ctx, propertyID)
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// ---------------------------------------------------------------------------
// Rate plans

// ListRatePlans lists rate plans by id after afterID.
func (s *Service) ListRatePlans(ctx context.Context, propertyID, afterID int64, active *bool, limit int) ([]RatePlan, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	rows, err := q.ListRatePlans(ctx, ratesdb.ListRatePlansParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Active: active, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	codes, err := q.ListChargeCodeModes(ctx, ratesdb.ListChargeCodeModesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]ratesdb.ListChargeCodeModesRow, len(codes))
	for _, c := range codes {
		byID[c.ID] = c
	}
	out := make([]RatePlan, len(rows))
	for i, r := range rows {
		c := byID[r.RoomChargeCodeID]
		out[i] = toRatePlan(r, c.Code, c.PriceMode)
	}
	return out, nil
}

// roomCode locks (share) the plan's room charge code and requires it to be an active ROOM code.
func (s *Service) roomCode(ctx context.Context, tenantID, propertyID, id int64) (ratesdb.GetChargeCodeForPlanShareRow, error) {
	c, err := s.q(ctx).GetChargeCodeForPlanShare(ctx, ratesdb.GetChargeCodeForPlanShareParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return c, orNotFound(err, errChargeCodeNotFound())
	}
	if c.ChargeType != "ROOM" || !c.IsActive {
		return c, apperr.Invalid("the room charge code is not usable",
			fieldErr("room_charge_code_id", "CHARGE_CODE_NOT_ROOM", "an active charge code of type ROOM is required"))
	}
	return c, nil
}

// CreateRatePlan adds a rate plan (rate.manage).
func (s *Service) CreateRatePlan(ctx context.Context, propertyID int64, in RatePlanInput) (RatePlan, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return RatePlan{}, err
	}
	in.Normalize()
	if fields := in.Validate(true); len(fields) > 0 {
		return RatePlan{}, apperr.Invalid("the rate plan is invalid", fields...)
	}

	var out RatePlan
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		code, err := s.roomCode(ctx, p.TenantID, propertyID, in.RoomChargeCodeID)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateRatePlan(ctx, ratesdb.CreateRatePlanParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, RatePlanDescription: nullable(in.Description),
			MealPlan: in.MealPlan, CancellationPolicy: nullable(in.CancellationPolicy), IsRefundable: in.IsRefundable,
			RoomChargeCodeID: in.RoomChargeCodeID, OccupancyKind: in.OccupancyKind, IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toRatePlan(row, code.Code, code.PriceMode)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "rate_plan.created", "rate_plan", out.ID, out.Code, nil, out))
	})
	return out, err
}

// RatePlanPatch changes selected attributes; nil fields stay unchanged.
type RatePlanPatch struct {
	Name               *string
	Description        *string
	MealPlan           *string
	CancellationPolicy *string
	IsRefundable       *bool
	RoomChargeCodeID   *int64
	IsReference        *bool
	IsActive           *bool
}

// UpdateRatePlan edits a rate plan (rate.manage).
//
// Changing the room charge code affects nightly snapshots taken from now on; existing reservations keep
// theirs. Because grid amounts are read in the code's price mode, a plan that already has rates cannot move
// to a code with a different price mode: the amounts would silently mean something else.
func (s *Service) UpdateRatePlan(ctx context.Context, propertyID, id int64, patch RatePlanPatch) (RatePlan, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return RatePlan{}, err
	}
	var out RatePlan
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetRatePlanForUpdate(ctx, ratesdb.GetRatePlanForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errRatePlanNotFound())
		}
		oldCode, err := q.GetChargeCodeForPlan(ctx, ratesdb.GetChargeCodeForPlanParams{TenantID: p.TenantID, PropertyID: propertyID, ID: row.RoomChargeCodeID})
		if err != nil {
			return err
		}
		before := toRatePlan(row, oldCode.Code, oldCode.PriceMode)
		in := RatePlanInput{
			Code: before.Code, Name: before.Name, Description: before.Description, MealPlan: before.MealPlan,
			CancellationPolicy: before.CancellationPolicy, IsRefundable: before.IsRefundable, RoomChargeCodeID: before.RoomChargeCodeID, OccupancyKind: before.OccupancyKind, IsReference: before.IsReference, IsActive: before.IsActive,
		}
		apply(&in.Name, patch.Name)
		apply(&in.Description, patch.Description)
		apply(&in.MealPlan, patch.MealPlan)
		apply(&in.CancellationPolicy, patch.CancellationPolicy)
		apply(&in.IsRefundable, patch.IsRefundable)
		apply(&in.RoomChargeCodeID, patch.RoomChargeCodeID)
		apply(&in.IsReference, patch.IsReference)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false); len(fields) > 0 {
			return apperr.Invalid("the rate plan is invalid", fields...)
		}

		if in.IsReference && in.OccupancyKind != KindPaid {
			return apperr.Invalid("the rate plan is invalid", fieldErr("is_reference", "NOT_PAID", "only a paid rate plan can be the reference plan"))
		}
		if in.IsReference && !row.IsReference {
			if err := q.ClearReferencePlans(ctx, ratesdb.ClearReferencePlansParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}

		codeName, mode := oldCode.Code, oldCode.PriceMode
		if in.RoomChargeCodeID != row.RoomChargeCodeID {
			nc, err := s.roomCode(ctx, p.TenantID, propertyID, in.RoomChargeCodeID)
			if err != nil {
				return err
			}
			if nc.PriceMode != oldCode.PriceMode {
				n, err := q.CountRatesOfPlan(ctx, ratesdb.CountRatesOfPlanParams{TenantID: p.TenantID, PropertyID: propertyID, RatePlanID: id})
				if err != nil {
					return err
				}
				if n > 0 {
					return apperr.Conflict("RATE_PLAN_PRICE_MODE_MISMATCH",
						"the plan already has rates read as "+oldCode.PriceMode+" prices; the new room charge code is "+nc.PriceMode).
						WithContext("rates", n).WithContext("current_price_mode", oldCode.PriceMode).WithContext("new_price_mode", nc.PriceMode)
				}
			}
			codeName, mode = nc.Code, nc.PriceMode
		}
		updated, err := q.UpdateRatePlan(ctx, ratesdb.UpdateRatePlanParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, RatePlanDescription: nullable(in.Description), MealPlan: in.MealPlan,
			CancellationPolicy: nullable(in.CancellationPolicy), IsRefundable: in.IsRefundable, RoomChargeCodeID: in.RoomChargeCodeID,
			IsReference: in.IsReference, IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toRatePlan(updated, codeName, mode)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "rate_plan.updated", "rate_plan", id, out.Code, before, out))
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Rate grid

func (s *Service) currencyDecimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

// Rates returns a slice of the grid for one plan: [from, to), optionally for one room type.
func (s *Service) Rates(ctx context.Context, propertyID, ratePlanID int64, roomTypeID *int64, from, to civil.Date) (RateGrid, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return RateGrid{}, err
	}
	var fields []apperr.FieldError
	if from.IsZero() {
		fields = append(fields, fieldErr("from", "REQUIRED", "YYYY-MM-DD"))
	}
	if to.IsZero() {
		fields = append(fields, fieldErr("to", "REQUIRED", "YYYY-MM-DD, exclusive"))
	}
	fields = append(fields, ValidateSpan(from, to)...)
	if len(fields) > 0 {
		return RateGrid{}, apperr.Invalid("the range is invalid", fields...)
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return RateGrid{}, err
	}
	q := s.q(ctx)
	plan, err := q.GetRatePlan(ctx, ratesdb.GetRatePlanParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ratePlanID})
	if err != nil {
		return RateGrid{}, orNotFound(err, errRatePlanNotFound())
	}
	code, err := q.GetChargeCodeForPlan(ctx, ratesdb.GetChargeCodeForPlanParams{TenantID: p.TenantID, PropertyID: propertyID, ID: plan.RoomChargeCodeID})
	if err != nil {
		return RateGrid{}, err
	}
	if roomTypeID != nil {
		ok, err := q.RoomTypeExists(ctx, ratesdb.RoomTypeExistsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *roomTypeID})
		if err != nil {
			return RateGrid{}, err
		}
		if !ok {
			return RateGrid{}, errRoomTypeNotFound()
		}
	}
	rows, err := q.ListRates(ctx, ratesdb.ListRatesParams{
		TenantID: p.TenantID, PropertyID: propertyID, RatePlanID: ratePlanID, FromDate: from, ToDate: to, RoomTypeID: roomTypeID,
	})
	if err != nil {
		return RateGrid{}, err
	}
	grid := RateGrid{RatePlanID: ratePlanID, PriceMode: code.PriceMode, RoomChargeCode: code.Code, From: from, To: to, Rates: make([]RateCell, len(rows))}
	for i, r := range rows {
		grid.Rates[i] = RateCell{RoomTypeID: r.RoomTypeID, StayDate: r.StayDate, Amount: r.Amount.StringFixed(decimals)}
	}
	return grid, nil
}

// FillRates is the bulk upsert (rate.manage): one amount for every listed room type on every selected
// weekday of [from, to). Existing reservations are not affected: they hold their own nightly snapshots.
func (s *Service) FillRates(ctx context.Context, propertyID int64, in FillInput) (FillResult, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return FillResult{}, err
	}
	fields := in.Validate()
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return FillResult{}, err
	}
	amount, perr := ParseAmount(in.Amount, decimals)
	if perr != nil {
		fields = append(fields, fieldErr("amount", "INVALID_AMOUNT", "a non-negative amount with at most the currency's decimals"))
	}
	if len(fields) > 0 {
		return FillResult{}, apperr.Invalid("the rates are invalid", fields...)
	}
	dates := in.Dates()
	if len(dates) == 0 {
		return FillResult{}, apperr.Invalid("the rates are invalid", fieldErr("weekdays", "NO_NIGHTS", "none of the selected weekdays falls in the range"))
	}

	var out FillResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		// L2: the room types (existence, and they cannot change while the grid is written), then the plan.
		if err := db.LockRows(ctx, db.RoomTypes, db.ForShare, propertyID, in.RoomTypeIDs); err != nil {
			if apperr.IsCode(err, "NOT_FOUND") {
				return errRoomTypeNotFound()
			}
			return err
		}
		q := s.q(ctx)
		// Share-locked: a concurrent change of the plan's room charge code (and so of the price mode the
		// amounts are read in) waits for this write, and the other way round.
		if _, err := q.GetRatePlanForShare(ctx, ratesdb.GetRatePlanForShareParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.RatePlanID}); err != nil {
			return orNotFound(err, errRatePlanNotFound())
		}
		res, err := q.UpsertRates(ctx, ratesdb.UpsertRatesParams{
			TenantID: p.TenantID, PropertyID: propertyID, RatePlanID: in.RatePlanID, Amount: amount, ActorID: p.ActorID(),
			RoomTypeIds: in.RoomTypeIDs, Dates: dates,
		})
		if err != nil {
			return err
		}
		out = FillResult{UpdatedNights: res.Written, CreatedNights: res.Created}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "rates.filled", "rate_plan", in.RatePlanID, auditlabel.RatePlan(ctx, propertyID, in.RatePlanID), nil, map[string]any{
			"room_type_ids": in.RoomTypeIDs, "from": in.From, "to": in.To, "weekdays": in.Weekdays, "amount": amount.String(),
			"updated_nights": out.UpdatedNights, "created_nights": out.CreatedNights,
		}))
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Price lookup

// NightlyPrices is the price lookup used by reservations to snapshot nightly rates: the price of every night
// of [arrival, departure) for a plan and room type, with the room charge code and price mode they are read
// in. The plan must be active and every night must have a rate (409 RATE_NOT_SET lists the missing ones).
// It reads committed data and takes no locks; callers that write snapshots hold their own locks.
func (s *Service) NightlyPrices(ctx context.Context, tenantID, propertyID, ratePlanID, roomTypeID int64, arrival, departure civil.Date) (NightlyPrices, error) {
	prices, missing, err := s.PriceNights(ctx, tenantID, propertyID, ratePlanID, roomTypeID, arrival, departure)
	if err != nil {
		return NightlyPrices{}, err
	}
	if len(missing) > 0 {
		shown := make([]string, 0, min(len(missing), 31))
		for _, d := range missing[:min(len(missing), 31)] {
			shown = append(shown, d.String())
		}
		return NightlyPrices{}, apperr.Conflict("RATE_NOT_SET", "some nights have no rate for this plan and room type").
			WithContext("nights", shown).WithContext("missing_nights", len(missing))
	}
	return prices, nil
}

// PriceNights is NightlyPrices without the RATE_NOT_SET error: it returns the priced nights and the nights
// that have no rate, so a search can show a plan as incomplete instead of failing.
func (s *Service) PriceNights(ctx context.Context, tenantID, propertyID, ratePlanID, roomTypeID int64, arrival, departure civil.Date) (NightlyPrices, []civil.Date, error) {
	if !departure.After(arrival) || arrival.DaysUntil(departure) > MaxLookupNights {
		return NightlyPrices{}, nil, apperr.Invalid("the stay dates are invalid", fieldErr("departure_date", "OUT_OF_RANGE", "after arrival, at most 365 nights"))
	}
	q := s.q(ctx)
	plan, err := q.GetRatePlan(ctx, ratesdb.GetRatePlanParams{TenantID: tenantID, PropertyID: propertyID, ID: ratePlanID})
	if err != nil {
		return NightlyPrices{}, nil, orNotFound(err, errRatePlanNotFound())
	}
	if !plan.IsActive {
		return NightlyPrices{}, nil, apperr.Conflict("RATE_PLAN_INACTIVE", "the rate plan is inactive").WithContext("rate_plan", plan.Code)
	}
	code, err := q.GetChargeCodeForPlan(ctx, ratesdb.GetChargeCodeForPlanParams{TenantID: tenantID, PropertyID: propertyID, ID: plan.RoomChargeCodeID})
	if err != nil {
		return NightlyPrices{}, nil, err
	}
	rows, err := q.ListNightRates(ctx, ratesdb.ListNightRatesParams{
		TenantID: tenantID, PropertyID: propertyID, RatePlanID: ratePlanID, RoomTypeID: roomTypeID, Arrival: arrival, Departure: departure,
	})
	if err != nil {
		return NightlyPrices{}, nil, err
	}
	rules, err := s.applicableRules(ctx, tenantID, propertyID, ratePlanID, roomTypeID)
	if err != nil {
		return NightlyPrices{}, nil, err
	}
	// The grid price stands when no rule can apply; only then does pricing need nothing but the grid.
	var decimals int32
	var bd civil.Date
	var occupancy map[civil.Date]availability.Occupancy
	if len(rules) > 0 {
		if decimals, err = s.currencyDecimals(ctx, propertyID); err != nil {
			return NightlyPrices{}, nil, err
		}
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return NightlyPrices{}, nil, err
		}
		bd = day.BusinessDate
		if usesOccupancy(rules) {
			var dates []civil.Date
			for d := arrival; d.Before(departure); d = d.AddDays(1) {
				dates = append(dates, d)
			}
			if occupancy, err = s.avail.PropertyOccupancy(ctx, tenantID, propertyID, bd, dates); err != nil {
				return NightlyPrices{}, nil, err
			}
		}
	}
	have := make(map[civil.Date]decimal.Decimal, len(rows))
	for _, r := range rows {
		have[r.StayDate] = r.Amount
	}
	out := NightlyPrices{RatePlanID: plan.ID, OccupancyKind: plan.OccupancyKind, RoomChargeCodeID: code.ID, RoomChargeCode: code.Code, PriceMode: code.PriceMode}
	if plan.OccupancyKind != KindPaid {
		// complimentary and house use cost nothing: every night is priced at zero, with no grid and no yield rules
		for d := arrival; d.Before(departure); d = d.AddDays(1) {
			out.Nights = append(out.Nights, NightPrice{Date: d, Amount: decimal.Zero, Grid: decimal.Zero})
		}
		return out, nil, nil
	}
	var missing []civil.Date
	for d := arrival; d.Before(departure); d = d.AddDays(1) {
		amount, ok := have[d]
		if !ok {
			missing = append(missing, d)
			continue
		}
		night := NightPrice{Date: d, Amount: amount, Grid: amount}
		if len(rules) > 0 {
			occ := occupancy[d].Percent()
			night.Occupancy = occ
			night.Amount, night.Steps = ApplyYield(rules, amount, NightContext{
				RatePlanID: ratePlanID, RoomTypeID: roomTypeID, Date: d, Occupancy: occ, LeadDays: max(0, bd.DaysUntil(d)), StayNights: arrival.DaysUntil(departure),
			}, decimals)
		}
		out.Nights = append(out.Nights, night)
	}
	return out, missing, nil
}
