package assets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// ---------------------------------------------------------------------------------------------------------------------
// Settings

func settingsFrom(r assetsdb.FaSetting) Settings {
	st := Settings{RegisterFrom: r.RegisterFrom, CapitalizationThreshold: r.CapitalizationThreshold, StartRule: r.StartRule, OEPolicy: r.OePolicy, OEDefaultMonths: int(r.OeDefaultMonths),
		OEPolicyChangedOn: r.OePolicyChangedOn, Started: r.RegisterFrom != nil, Version: r.Version}
	if r.TaxYearStartMonth != nil {
		st.TaxYearStartMonth = ptr(int(*r.TaxYearStartMonth))
	}
	return st
}

// GetSettings answers the settings of the register; a property that has not touched them has the defaults (any asset permission).
func (s *Service) GetSettings(ctx context.Context, propertyID int64) (Settings, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return Settings{}, err
	}
	r, err := s.q(ctx).GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{CapitalizationThreshold: decimal.Zero, StartRule: StartInServiceMonth, OEPolicy: OEExpense, OEDefaultMonths: defaultOEMonths}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	return settingsFrom(r), nil
}

// UpdateSettings replaces the settings (asset.setup). A change of the rule for the operating equipment applies to later purchases (A10) and is dated; the start of the register is set once
// and cannot move while assets exist.
func (s *Service) UpdateSettings(ctx context.Context, propertyID int64, in SettingsInput) (Settings, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return Settings{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Settings{}, err
	}
	var f []apperr.FieldError
	if in.StartRule != StartInServiceMonth && in.StartRule != StartNextMonth {
		f = append(f, fieldErr("start_rule", "INVALID_VALUE", "IN_SERVICE_MONTH or NEXT_MONTH"))
	}
	if in.OEPolicy != OEExpense && in.OEPolicy != OEBatch {
		f = append(f, fieldErr("oe_policy", "INVALID_VALUE", "EXPENSE or BATCH"))
	}
	if in.OEDefaultMonths < 1 || in.OEDefaultMonths > 120 {
		f = append(f, fieldErr("oe_default_months", "OUT_OF_RANGE", "1 to 120 months"))
	}
	if !moneyOK(in.CapitalizationThreshold, prop.CurrencyDecimals) {
		f = append(f, fieldErr("capitalization_threshold", "INVALID_VALUE", fmt.Sprintf("zero or more, %d decimals at most", prop.CurrencyDecimals)))
	}
	if in.TaxYearStartMonth != nil && (*in.TaxYearStartMonth < 1 || *in.TaxYearStartMonth > 12) {
		f = append(f, fieldErr("tax_year_start_month", "OUT_OF_RANGE", "1 to 12"))
	}
	if in.RegisterFrom != nil && !in.RegisterFrom.Equal(MonthStart(*in.RegisterFrom)) {
		f = append(f, fieldErr("register_from", "INVALID_VALUE", "the first day of a month"))
	}
	if len(f) > 0 {
		return Settings{}, invalid("settings", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		cur, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		from := in.RegisterFrom
		if !sameDate(from, cur.RegisterFrom) {
			if has, err := q.AssetsExist(ctx, assetsdb.AssetsExistParams{TenantID: p.TenantID, PropertyID: propertyID}); err != nil {
				return err
			} else if has {
				return apperr.Conflict("REGISTER_FROM_LOCKED", "the start of the register cannot change while the register holds assets")
			}
		}
		changedOn := cur.OePolicyChangedOn
		if in.OEPolicy != cur.OePolicy {
			changedOn = &day
		}
		var tax *int16
		if in.TaxYearStartMonth != nil {
			tax = ptr(int16(*in.TaxYearStartMonth)) //nolint:gosec // G115: 1 to 12
		}
		row, err := q.UpdateSettings(ctx, assetsdb.UpdateSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, RegisterFrom: from, CapitalizationThreshold: in.CapitalizationThreshold,
			StartRule: in.StartRule, OePolicy: in.OEPolicy, OeDefaultMonths: int32(in.OEDefaultMonths), OePolicyChangedOn: changedOn, TaxYearStartMonth: tax, ActorID: p.ActorID(), Version: in.Version}) //nolint:gosec // G115: 1 to 120
		if errors.Is(err, pgx.ErrNoRows) {
			return versionConflict("settings", cur.Version)
		}
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.settings_updated", "fa_settings", row.ID, "", settingsFrom(cur), settingsFrom(row)))
	})
	if err != nil {
		return Settings{}, err
	}
	return s.GetSettings(ctx, propertyID)
}

func sameDate(a, b *civil.Date) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

// ---------------------------------------------------------------------------------------------------------------------
// Fiscal groups

func fiscalFrom(r assetsdb.FaFiscalGroup) FiscalGroup {
	return FiscalGroup{Code: r.Code, LifeYears: int(r.LifeYears), SLRate: r.SlRate, DBRate: r.DbRate}
}

// ListFiscalGroups lists the groups of the income tax law with the rates of the property (any asset permission).
func (s *Service) ListFiscalGroups(ctx context.Context, propertyID int64) ([]FiscalGroup, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListFiscalGroups(ctx, assetsdb.ListFiscalGroupsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]FiscalGroup, 0, len(rows))
	for _, r := range rows {
		out = append(out, fiscalFrom(r))
	}
	return out, nil
}

// UpdateFiscalGroup corrects the rates of one group (asset.setup). Nothing in the books depends on them (4.9).
func (s *Service) UpdateFiscalGroup(ctx context.Context, propertyID int64, code string, in FiscalGroupInput) (FiscalGroup, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return FiscalGroup{}, err
	}
	var f []apperr.FieldError
	if in.LifeYears < 1 || in.LifeYears > 100 {
		f = append(f, fieldErr("life_years", "OUT_OF_RANGE", "1 to 100 years"))
	}
	if !in.SLRate.IsPositive() || in.SLRate.GreaterThan(decHundred) || !in.SLRate.Equal(in.SLRate.Round(3)) {
		f = append(f, fieldErr("sl_rate", "OUT_OF_RANGE", "above 0 and up to 100 percent, three decimals at most"))
	}
	if in.DBRate != nil && (!in.DBRate.IsPositive() || in.DBRate.GreaterThan(decHundred) || !in.DBRate.Equal(in.DBRate.Round(3))) {
		f = append(f, fieldErr("db_rate", "OUT_OF_RANGE", "above 0 and up to 100 percent, three decimals at most"))
	}
	if in.DBRate != nil && isBuildingGroup(code) {
		f = append(f, fieldErr("db_rate", "NOT_ALLOWED", "a building has the straight line only"))
	}
	if len(f) > 0 {
		return FiscalGroup{}, invalid("fiscal group", f...)
	}
	var out FiscalGroup
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		q := s.q(ctx)
		var old *FiscalGroup
		rows, err := q.ListFiscalGroups(ctx, assetsdb.ListFiscalGroupsParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Code == code {
				old = ptr(fiscalFrom(r))
			}
		}
		row, err := q.UpdateFiscalGroup(ctx, assetsdb.UpdateFiscalGroupParams{TenantID: p.TenantID, PropertyID: propertyID, Code: code, LifeYears: int32(in.LifeYears), SlRate: in.SLRate, DbRate: in.DBRate, ActorID: p.ActorID()}) //nolint:gosec // G115: 1 to 100, checked above
		if err != nil {
			return orNotFound(err, notFound("FISCAL_GROUP_NOT_FOUND", "fiscal group"))
		}
		out = fiscalFrom(row)
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.fiscal_group_updated", "fa_fiscal_group", 0, code, old, out))
	})
	return out, err
}
