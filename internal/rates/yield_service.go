package rates

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/rates/ratesdb"
)

func int4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true} //nolint:gosec // G115: validated to at most maxLeadDays or maxStayNights
}

func intOf(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func toYieldRule(r ratesdb.YieldRule) YieldRule {
	return YieldRule{
		ID: r.ID, Code: r.Code, Name: r.Name, RatePlanID: r.RatePlanID, RoomTypeID: r.RoomTypeID, StayFrom: r.StayFrom, StayTo: r.StayTo,
		Weekdays: r.Weekdays, OccupancyFrom: r.OccupancyFrom, OccupancyTo: r.OccupancyTo,
		LeadMin: intOf(r.LeadMin), LeadMax: intOf(r.LeadMax), StayMin: intOf(r.StayMin), StayMax: intOf(r.StayMax),
		AdjustmentType: r.AdjustmentType, AdjustmentValue: r.AdjustmentValue, FloorAmount: r.FloorAmount, CapAmount: r.CapAmount,
		Priority: int(r.Priority), IsActive: r.IsActive, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func toYieldRules(rows []ratesdb.YieldRule) []YieldRule {
	out := make([]YieldRule, len(rows))
	for i, r := range rows {
		out[i] = toYieldRule(r)
	}
	return out
}

func errYieldRuleNotFound() *apperr.Error {
	return apperr.NotFound("YIELD_RULE_NOT_FOUND", "the yield rule does not exist in this property")
}

// checkRuleTargets makes sure the plan and room type a rule names belong to the property (404 otherwise).
func (s *Service) checkRuleTargets(ctx context.Context, tenantID, propertyID int64, pr parsedRule) error {
	q := s.q(ctx)
	if pr.ratePlanID != nil {
		if _, err := q.GetRatePlan(ctx, ratesdb.GetRatePlanParams{TenantID: tenantID, PropertyID: propertyID, ID: *pr.ratePlanID}); err != nil {
			return orNotFound(err, errRatePlanNotFound())
		}
	}
	if pr.roomTypeID != nil {
		ok, err := q.RoomTypeExists(ctx, ratesdb.RoomTypeExistsParams{TenantID: tenantID, PropertyID: propertyID, ID: *pr.roomTypeID})
		if err != nil {
			return err
		}
		if !ok {
			return errRoomTypeNotFound()
		}
	}
	return nil
}

// applicableRules loads the active rules that can apply to a plan and room type, in the order they apply.
func (s *Service) applicableRules(ctx context.Context, tenantID, propertyID, ratePlanID, roomTypeID int64) ([]YieldRule, error) {
	rows, err := s.q(ctx).ListApplicableYieldRules(ctx, ratesdb.ListApplicableYieldRulesParams{
		TenantID: tenantID, PropertyID: propertyID, RatePlanID: &ratePlanID, RoomTypeID: &roomTypeID,
	})
	if err != nil {
		return nil, err
	}
	rules := toYieldRules(rows)
	SortRules(rules)
	return rules, nil
}

func usesOccupancy(rules []YieldRule) bool {
	for _, r := range rules {
		if r.UsesOccupancy() {
			return true
		}
	}
	return false
}

// ListYieldRules lists the property's yield rules in the order they apply (any user of the property).
func (s *Service) ListYieldRules(ctx context.Context, propertyID int64, active *bool) ([]YieldRule, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListYieldRules(ctx, ratesdb.ListYieldRulesParams{TenantID: p.TenantID, PropertyID: propertyID, Active: active})
	if err != nil {
		return nil, err
	}
	return toYieldRules(rows), nil
}

// GetYieldRule returns one rule (any user of the property).
func (s *Service) GetYieldRule(ctx context.Context, propertyID, id int64) (YieldRule, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return YieldRule{}, err
	}
	row, err := s.q(ctx).GetYieldRule(ctx, ratesdb.GetYieldRuleParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return YieldRule{}, orNotFound(err, errYieldRuleNotFound())
	}
	return toYieldRule(row), nil
}

// CreateYieldRule adds a rule (rate.manage). It affects the nights priced from now on; reservations keep the prices
// they were sold at.
func (s *Service) CreateYieldRule(ctx context.Context, propertyID int64, in YieldRuleInput) (YieldRule, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return YieldRule{}, err
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return YieldRule{}, err
	}
	pr, fields := parseYieldRule(in, decimals)
	if len(fields) > 0 {
		return YieldRule{}, apperr.Invalid("the yield rule is invalid", fields...)
	}
	var out YieldRule
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.checkRuleTargets(ctx, p.TenantID, propertyID, pr); err != nil {
			return err
		}
		row, err := s.q(ctx).CreateYieldRule(ctx, ratesdb.CreateYieldRuleParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: pr.code, Name: pr.name, RatePlanID: pr.ratePlanID, RoomTypeID: pr.roomTypeID,
			StayFrom: pr.stayFrom, StayTo: pr.stayTo, Weekdays: pr.weekdays, OccupancyFrom: pr.occFrom, OccupancyTo: pr.occTo,
			LeadMin: int4(pr.leadMin), LeadMax: int4(pr.leadMax), StayMin: int4(pr.stayMin), StayMax: int4(pr.stayMax),
			AdjustmentType: pr.adjType, AdjustmentValue: pr.adjValue, FloorAmount: pr.floor, CapAmount: pr.capAmount,
			Priority: int32(pr.priority), IsActive: pr.active, ActorID: p.ActorID(), //nolint:gosec // G115: validated to 0..10000
		})
		if err != nil {
			return err
		}
		out = toYieldRule(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "yield_rule.created", "yield_rule", out.ID, nil, out))
	})
	return out, err
}

// UpdateYieldRule replaces a rule's name, conditions and adjustment (rate.manage). The code never changes.
func (s *Service) UpdateYieldRule(ctx context.Context, propertyID, id int64, in YieldRuleInput) (YieldRule, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return YieldRule{}, err
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return YieldRule{}, err
	}
	pr, fields := parseYieldRule(in, decimals)
	if len(fields) > 0 {
		return YieldRule{}, apperr.Invalid("the yield rule is invalid", fields...)
	}
	var out YieldRule
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		before, err := q.GetYieldRuleForUpdate(ctx, ratesdb.GetYieldRuleForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errYieldRuleNotFound())
		}
		if before.Code != pr.code {
			return apperr.Invalid("the yield rule is invalid", fieldErr("code", "IMMUTABLE", "the code of a rule cannot change"))
		}
		if err := s.checkRuleTargets(ctx, p.TenantID, propertyID, pr); err != nil {
			return err
		}
		row, err := q.UpdateYieldRule(ctx, ratesdb.UpdateYieldRuleParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: pr.name, RatePlanID: pr.ratePlanID, RoomTypeID: pr.roomTypeID,
			StayFrom: pr.stayFrom, StayTo: pr.stayTo, Weekdays: pr.weekdays, OccupancyFrom: pr.occFrom, OccupancyTo: pr.occTo,
			LeadMin: int4(pr.leadMin), LeadMax: int4(pr.leadMax), StayMin: int4(pr.stayMin), StayMax: int4(pr.stayMax),
			AdjustmentType: pr.adjType, AdjustmentValue: pr.adjValue, FloorAmount: pr.floor, CapAmount: pr.capAmount,
			Priority: int32(pr.priority), IsActive: pr.active, ActorID: p.ActorID(), //nolint:gosec // G115: validated to 0..10000
		})
		if err != nil {
			return err
		}
		out = toYieldRule(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "yield_rule.updated", "yield_rule", id, toYieldRule(before), out))
	})
	return out, err
}

// DeleteYieldRule removes a rule (rate.manage). Reservations keep the codes of the rules that priced them.
func (s *Service) DeleteYieldRule(ctx context.Context, propertyID, id int64) error {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		before, err := q.GetYieldRuleForUpdate(ctx, ratesdb.GetYieldRuleForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errYieldRuleNotFound())
		}
		if _, err := q.DeleteYieldRule(ctx, ratesdb.DeleteYieldRuleParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "yield_rule.deleted", "yield_rule", id, toYieldRule(before), nil))
	})
}

// QuoteNight is one night of a price quote: the grid price, how full the property is, the rules that moved the price
// and the price the night is sold at.
type QuoteNight struct {
	Date      civil.Date       `json:"date"`
	GridRate  *decimal.Decimal `json:"grid_rate"`
	Occupancy string           `json:"occupancy_percent"`
	Steps     []Step           `json:"steps"`
	Amount    *decimal.Decimal `json:"amount"`
}

// Quote is what a stay would be priced at today, night by night, rules included.
type Quote struct {
	RatePlanID int64        `json:"rate_plan_id"`
	RoomTypeID int64        `json:"room_type_id"`
	PriceMode  string       `json:"price_mode"`
	Nights     []QuoteNight `json:"nights"`
	Total      string       `json:"total"`
	GridTotal  string       `json:"grid_total"`
	Missing    int          `json:"missing_nights"`
}

// Quote prices a stay as a booking made now would be, showing each rule that moved a night (any user of the property).
func (s *Service) Quote(ctx context.Context, propertyID, ratePlanID, roomTypeID int64, arrival, departure civil.Date) (Quote, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return Quote{}, err
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return Quote{}, err
	}
	ok, err := s.q(ctx).RoomTypeExists(ctx, ratesdb.RoomTypeExistsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: roomTypeID})
	if err != nil {
		return Quote{}, err
	}
	if !ok {
		return Quote{}, errRoomTypeNotFound()
	}
	prices, missing, err := s.PriceNights(ctx, p.TenantID, propertyID, ratePlanID, roomTypeID, arrival, departure)
	if err != nil {
		return Quote{}, err
	}
	out := Quote{RatePlanID: prices.RatePlanID, RoomTypeID: roomTypeID, PriceMode: prices.PriceMode, Missing: len(missing), Nights: []QuoteNight{}}
	byDate := make(map[civil.Date]NightPrice, len(prices.Nights))
	for _, n := range prices.Nights {
		byDate[n.Date] = n
	}
	total, grid := decimal.Zero, decimal.Zero
	for d := arrival; d.Before(departure); d = d.AddDays(1) {
		n, ok := byDate[d]
		if !ok {
			out.Nights = append(out.Nights, QuoteNight{Date: d, Occupancy: "0.00", Steps: []Step{}})
			continue
		}
		g, a := n.Grid, n.Amount
		steps := n.Steps
		if steps == nil {
			steps = []Step{}
		}
		out.Nights = append(out.Nights, QuoteNight{Date: d, GridRate: &g, Occupancy: n.Occupancy.StringFixed(2), Steps: steps, Amount: &a})
		total, grid = total.Add(a), grid.Add(g)
	}
	out.Total, out.GridTotal = total.StringFixed(decimals), grid.StringFixed(decimals)
	return out, nil
}
