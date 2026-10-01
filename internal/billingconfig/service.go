package billingconfig

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/billingconfig/billingconfigdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// Service is the billing configuration application service.
//
// Locking: every write first share-locks the OPEN business day (L1). The configuration rows themselves
// are not part of the global lock order; the order used here is charge code, then taxes, then service
// charges, and no other module takes them in a different order.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	authz auth.Authorizer
	days  *tenancy.Service
}

// NewService wires the billing configuration service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, authz auth.Authorizer, days *tenancy.Service) *Service {
	return &Service{txm: txm, clock: c, audit: a, authz: authz, days: days}
}

func (s *Service) q(ctx context.Context) *billingconfigdb.Queries {
	return billingconfigdb.New(s.txm.DB(ctx))
}

func auditEntry(p auth.Principal, propertyID int64, bd civil.Date, action, entity string, id int64, old, updated any) audit.Entry {
	return audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(),
		Action: action, EntityType: entity, EntityID: id, Old: old, New: updated,
	}
}

// writer authorizes a configuration write and returns the caller.
func (s *Service) writer(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.Require(ctx, propertyID, auth.PermBillingConfigManage)
}

// reader authorizes a read: any access to the property.
func (s *Service) reader(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, s.authz.CanAccess(ctx, propertyID)
}

// SeedProperty creates the standard charge codes of a new property. It is registered as a property
// creation hook and runs inside that transaction.
func (s *Service) SeedProperty(ctx context.Context, in tenancy.PropertyCreated) error {
	created, err := s.q(ctx).SeedChargeCodes(ctx, billingconfigdb.SeedChargeCodesParams{TenantID: in.TenantID, PropertyID: in.PropertyID, ActorID: in.ActorID})
	if err != nil {
		return err
	}
	bd := in.BusinessDate
	return s.audit.Write(ctx, audit.Entry{
		TenantID: in.TenantID, PropertyID: &in.PropertyID, BusinessDate: &bd, UserID: in.ActorID,
		Action: "charge_codes.seeded", EntityType: "property", EntityID: in.PropertyID, New: map[string]any{"created": created},
	})
}

func (s *Service) currencyDecimals(ctx context.Context, propertyID int64) (int32, error) {
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return 0, err
	}
	return prop.CurrencyDecimals, nil
}

func apply[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}

// ---------------------------------------------------------------------------
// Taxes

// ListTaxes lists taxes by id after afterID.
func (s *Service) ListTaxes(ctx context.Context, propertyID, afterID int64, active *bool, limit int) ([]Tax, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListTaxes(ctx, billingconfigdb.ListTaxesParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Active: active, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]Tax, len(rows))
	for i, r := range rows {
		out[i] = toTax(r)
	}
	return out, nil
}

// CreateTax adds a tax (billing_config.manage).
func (s *Service) CreateTax(ctx context.Context, propertyID int64, in TaxInput) (Tax, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return Tax{}, err
	}
	in.Normalize()
	if fields := in.Validate(true); len(fields) > 0 {
		return Tax{}, apperr.Invalid("the tax is invalid", fields...)
	}
	rate, _ := ParseRate(in.Rate)

	var out Tax
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateTax(ctx, billingconfigdb.CreateTaxParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, Rate: rate,
			TaxOnService: in.TaxOnService, GlAccountCode: glOrNil(in.GLAccountCode), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toTax(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "tax.created", "tax", out.ID, nil, out))
	})
	return out, err
}

// TaxPatch changes selected attributes; nil fields stay unchanged.
type TaxPatch struct {
	Name          *string
	Rate          *string
	TaxOnService  *bool
	GLAccountCode *string // "" clears it
	IsActive      *bool
}

// UpdateTax edits a tax. A rate change affects future postings only (the result reports the open stays it
// will reach). Deactivation is rejected while the tax is mapped to a charge code.
func (s *Service) UpdateTax(ctx context.Context, propertyID, id int64, patch TaxPatch) (Tax, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return Tax{}, err
	}
	var out Tax
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetTaxForUpdate(ctx, billingconfigdb.GetTaxForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errTaxNotFound())
		}
		before := toTax(row)
		in := TaxInput{Code: before.Code, Name: before.Name, Rate: before.Rate, TaxOnService: before.TaxOnService, GLAccountCode: glString(before.GLAccountCode), IsActive: before.IsActive}
		apply(&in.Name, patch.Name)
		apply(&in.Rate, patch.Rate)
		apply(&in.TaxOnService, patch.TaxOnService)
		apply(&in.GLAccountCode, patch.GLAccountCode)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false); len(fields) > 0 {
			return apperr.Invalid("the tax is invalid", fields...)
		}
		rate, _ := ParseRate(in.Rate)

		if before.IsActive && !in.IsActive {
			n, err := q.CountActiveMappingsOfTax(ctx, billingconfigdb.CountActiveMappingsOfTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: id})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("TAX_IN_USE", "the tax is still mapped to charge codes; remove it from their rules first").WithContext("charge_code_rules", n)
			}
		}
		updated, err := q.UpdateTax(ctx, billingconfigdb.UpdateTaxParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, Rate: rate,
			TaxOnService: in.TaxOnService, GlAccountCode: glOrNil(in.GLAccountCode), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toTax(updated)
		if !row.Rate.Equal(rate) {
			n, err := q.CountOpenStaysAffectedByTax(ctx, billingconfigdb.CountOpenStaysAffectedByTaxParams{TenantID: p.TenantID, PropertyID: propertyID, TaxID: id})
			if err != nil {
				return err
			}
			out.AffectedOpenStays = &n
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "tax.updated", "tax", id, before, out))
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Service charges

// ListServiceCharges lists service charges by id after afterID.
func (s *Service) ListServiceCharges(ctx context.Context, propertyID, afterID int64, active *bool, limit int) ([]ServiceCharge, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListServiceCharges(ctx, billingconfigdb.ListServiceChargesParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Active: active, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	out := make([]ServiceCharge, len(rows))
	for i, r := range rows {
		out[i] = toServiceCharge(r)
	}
	return out, nil
}

// CreateServiceCharge adds a service charge (billing_config.manage).
func (s *Service) CreateServiceCharge(ctx context.Context, propertyID int64, in ServiceChargeInput) (ServiceCharge, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return ServiceCharge{}, err
	}
	in.Normalize()
	if fields := in.Validate(true); len(fields) > 0 {
		return ServiceCharge{}, apperr.Invalid("the service charge is invalid", fields...)
	}
	rate, _ := ParseRate(in.Rate)

	var out ServiceCharge
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateServiceCharge(ctx, billingconfigdb.CreateServiceChargeParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, Rate: rate, GlAccountCode: glOrNil(in.GLAccountCode), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toServiceCharge(row)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "service_charge.created", "service_charge", out.ID, nil, out))
	})
	return out, err
}

// ServiceChargePatch changes selected attributes; nil fields stay unchanged.
type ServiceChargePatch struct {
	Name          *string
	Rate          *string
	GLAccountCode *string // "" clears it
	IsActive      *bool
}

// UpdateServiceCharge edits a service charge, with the same rules as UpdateTax.
func (s *Service) UpdateServiceCharge(ctx context.Context, propertyID, id int64, patch ServiceChargePatch) (ServiceCharge, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return ServiceCharge{}, err
	}
	var out ServiceCharge
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetServiceChargeForUpdate(ctx, billingconfigdb.GetServiceChargeForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errServiceChargeNotFound())
		}
		before := toServiceCharge(row)
		in := ServiceChargeInput{Code: before.Code, Name: before.Name, Rate: before.Rate, GLAccountCode: glString(before.GLAccountCode), IsActive: before.IsActive}
		apply(&in.Name, patch.Name)
		apply(&in.Rate, patch.Rate)
		apply(&in.GLAccountCode, patch.GLAccountCode)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false); len(fields) > 0 {
			return apperr.Invalid("the service charge is invalid", fields...)
		}
		rate, _ := ParseRate(in.Rate)

		if before.IsActive && !in.IsActive {
			n, err := q.CountActiveMappingsOfServiceCharge(ctx, billingconfigdb.CountActiveMappingsOfServiceChargeParams{
				TenantID: p.TenantID, PropertyID: propertyID, ServiceChargeID: id})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("SERVICE_CHARGE_IN_USE", "the service charge is still mapped to charge codes; remove it from their rules first").
					WithContext("charge_code_rules", n)
			}
		}
		updated, err := q.UpdateServiceCharge(ctx, billingconfigdb.UpdateServiceChargeParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, Rate: rate, GlAccountCode: glOrNil(in.GLAccountCode), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toServiceCharge(updated)
		if !row.Rate.Equal(rate) {
			n, err := q.CountOpenStaysAffectedByServiceCharge(ctx, billingconfigdb.CountOpenStaysAffectedByServiceChargeParams{
				TenantID: p.TenantID, PropertyID: propertyID, ServiceChargeID: id})
			if err != nil {
				return err
			}
			out.AffectedOpenStays = &n
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "service_charge.updated", "service_charge", id, before, out))
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Charge codes

// ChargeCodeFilter narrows a charge code list.
type ChargeCodeFilter struct {
	Active     *bool
	ChargeType *string
}

// attachRules loads the active rules of the given charge codes (all of the property's when one is nil).
func (s *Service) attachRules(ctx context.Context, tenantID, propertyID int64, only *int64, codes []ChargeCode) error {
	q := s.q(ctx)
	taxes, err := q.ListActiveTaxRules(ctx, billingconfigdb.ListActiveTaxRulesParams{TenantID: tenantID, PropertyID: propertyID, ChargeCodeID: only})
	if err != nil {
		return err
	}
	services, err := q.ListActiveServiceRules(ctx, billingconfigdb.ListActiveServiceRulesParams{TenantID: tenantID, PropertyID: propertyID, ChargeCodeID: only})
	if err != nil {
		return err
	}
	byID := make(map[int64]*ChargeCode, len(codes))
	for i := range codes {
		byID[codes[i].ID] = &codes[i]
	}
	for _, r := range taxes {
		if c := byID[r.ChargeCodeID]; c != nil {
			c.Taxes = append(c.Taxes, TaxRule{TaxID: r.TaxID, Code: r.Code, Name: r.Name, Rate: FormatRate(r.Rate), TaxOnService: r.TaxOnService, Sequence: int32(r.Sequence)})
		}
	}
	for _, r := range services {
		if c := byID[r.ChargeCodeID]; c != nil {
			c.ServiceCharges = append(c.ServiceCharges, ServiceRule{ServiceChargeID: r.ServiceChargeID, Code: r.Code, Name: r.Name, Rate: FormatRate(r.Rate), Sequence: int32(r.Sequence)})
		}
	}
	return nil
}

// ListChargeCodes lists charge codes (with their rules) by id after afterID.
func (s *Service) ListChargeCodes(ctx context.Context, propertyID, afterID int64, f ChargeCodeFilter, limit int) ([]ChargeCode, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if f.ChargeType != nil && !chargeTypes[*f.ChargeType] {
		return nil, apperr.Invalid("the filter is invalid", fieldErr("charge_type", "INVALID_VALUE", "ROOM, FOOD_BEVERAGE, SERVICE, FEE or OTHER"))
	}
	rows, err := s.q(ctx).ListChargeCodes(ctx, billingconfigdb.ListChargeCodesParams{
		TenantID: p.TenantID, PropertyID: propertyID, AfterID: afterID, Active: f.Active, ChargeType: f.ChargeType, RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	out := make([]ChargeCode, len(rows))
	for i, r := range rows {
		out[i] = toChargeCode(r, decimals)
	}
	return out, s.attachRules(ctx, p.TenantID, propertyID, nil, out)
}

func (s *Service) loadChargeCode(ctx context.Context, tenantID, propertyID, id int64) (ChargeCode, error) {
	row, err := s.q(ctx).GetChargeCode(ctx, billingconfigdb.GetChargeCodeParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return ChargeCode{}, orNotFound(err, errChargeCodeNotFound())
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	one := []ChargeCode{toChargeCode(row, decimals)}
	if err := s.attachRules(ctx, tenantID, propertyID, &id, one); err != nil {
		return ChargeCode{}, err
	}
	return one[0], nil
}

// GetChargeCode returns one charge code with its rules.
func (s *Service) GetChargeCode(ctx context.Context, propertyID, id int64) (ChargeCode, error) {
	p, err := s.reader(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	return s.loadChargeCode(ctx, p.TenantID, propertyID, id)
}

// CreateChargeCode adds a charge code without rules (billing_config.manage).
func (s *Service) CreateChargeCode(ctx context.Context, propertyID int64, in ChargeCodeInput) (ChargeCode, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	in.Normalize()
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	if fields := in.Validate(true, decimals); len(fields) > 0 {
		return ChargeCode{}, apperr.Invalid("the charge code is invalid", fields...)
	}

	var out ChargeCode
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		row, err := s.q(ctx).CreateChargeCode(ctx, billingconfigdb.CreateChargeCodeParams{
			TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, ChargeType: in.ChargeType, PriceMode: in.PriceMode,
			DefaultUnitPrice: unitPrice(in.DefaultUnitPrice), GlAccountCode: glOrNil(in.GLAccountCode), IsActive: in.IsActive, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		out = toChargeCode(row, decimals)
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "charge_code.created", "charge_code", out.ID, nil, out))
	})
	return out, err
}

// ChargeCodePatch changes selected attributes; nil fields stay unchanged and an empty default_unit_price clears it.
type ChargeCodePatch struct {
	Name             *string
	ChargeType       *string
	PriceMode        *string
	DefaultUnitPrice *string
	GLAccountCode    *string // "" clears it
	IsActive         *bool
}

// UpdateChargeCode edits a charge code.
//
//   - price_mode cannot change once the code is used (PRICE_MODE_LOCKED, enforced by a trigger);
//   - the charge type of a system code is fixed, and a code used for room revenue stays ROOM;
//   - a code cannot be deactivated while an active rate plan sells through it.
func (s *Service) UpdateChargeCode(ctx context.Context, propertyID, id int64, patch ChargeCodePatch) (ChargeCode, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	decimals, err := s.currencyDecimals(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	var out ChargeCode
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		row, err := q.GetChargeCodeForUpdate(ctx, billingconfigdb.GetChargeCodeForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, errChargeCodeNotFound())
		}
		before, err := s.loadChargeCode(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		in := ChargeCodeInput{Code: before.Code, Name: before.Name, ChargeType: before.ChargeType, PriceMode: before.PriceMode, GLAccountCode: glString(before.GLAccountCode), IsActive: before.IsActive}
		if before.DefaultUnitPrice != nil {
			in.DefaultUnitPrice = *before.DefaultUnitPrice
		}
		apply(&in.Name, patch.Name)
		apply(&in.ChargeType, patch.ChargeType)
		apply(&in.PriceMode, patch.PriceMode)
		apply(&in.DefaultUnitPrice, patch.DefaultUnitPrice)
		apply(&in.GLAccountCode, patch.GLAccountCode)
		apply(&in.IsActive, patch.IsActive)
		in.Normalize()
		if fields := in.Validate(false, decimals); len(fields) > 0 {
			return apperr.Invalid("the charge code is invalid", fields...)
		}
		if row.IsSystem && in.ChargeType != before.ChargeType {
			return apperr.Conflict("SYSTEM_CHARGE_CODE_LOCKED", "the charge type of a system charge code cannot change")
		}
		if before.IsActive && !in.IsActive {
			n, err := q.CountActiveRatePlansUsingChargeCode(ctx, billingconfigdb.CountActiveRatePlansUsingChargeCodeParams{
				TenantID: p.TenantID, PropertyID: propertyID, ChargeCodeID: id})
			if err != nil {
				return err
			}
			if n > 0 {
				return apperr.Conflict("CHARGE_CODE_IN_USE", "an active rate plan still sells through this charge code").WithContext("active_rate_plans", n)
			}
		}
		if _, err := q.UpdateChargeCode(ctx, billingconfigdb.UpdateChargeCodeParams{
			TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, ChargeType: in.ChargeType, PriceMode: in.PriceMode,
			DefaultUnitPrice: unitPrice(in.DefaultUnitPrice), GlAccountCode: glOrNil(in.GLAccountCode), IsActive: in.IsActive, ActorID: p.ActorID(),
		}); err != nil {
			return err // a locked price mode or charge type is mapped by the database error table
		}
		if out, err = s.loadChargeCode(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "charge_code.updated", "charge_code", id, before, out))
	})
	return out, err
}

// ReplaceRules sets the complete ordered tax and service rules of a charge code (billing_config.manage).
// Rules that are no longer listed are deactivated (history is kept). The taxes and service charges must
// exist in the property and be active. It affects future postings only.
func (s *Service) ReplaceRules(ctx context.Context, propertyID, chargeCodeID int64, in RulesInput) (ChargeCode, error) {
	p, err := s.writer(ctx, propertyID)
	if err != nil {
		return ChargeCode{}, err
	}
	if fields := in.Validate(); len(fields) > 0 {
		return ChargeCode{}, apperr.Invalid("the rules are invalid", fields...)
	}

	var out ChargeCode
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetChargeCodeForUpdate(ctx, billingconfigdb.GetChargeCodeForUpdateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeCodeID}); err != nil {
			return orNotFound(err, errChargeCodeNotFound())
		}
		before, err := s.loadChargeCode(ctx, p.TenantID, propertyID, chargeCodeID)
		if err != nil {
			return err
		}

		// Share-lock what is being mapped so it cannot be deactivated concurrently.
		taxIDs := make([]int64, len(in.Taxes))
		for i, r := range in.Taxes {
			taxIDs[i] = r.TaxID
		}
		taxes, err := q.LockTaxesForShare(ctx, billingconfigdb.LockTaxesForShareParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: taxIDs})
		if err != nil {
			return err
		}
		if len(taxes) != len(taxIDs) {
			return errTaxNotFound()
		}
		svcIDs := make([]int64, len(in.ServiceCharges))
		for i, r := range in.ServiceCharges {
			svcIDs[i] = r.ServiceChargeID
		}
		services, err := q.LockServiceChargesForShare(ctx, billingconfigdb.LockServiceChargesForShareParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: svcIDs})
		if err != nil {
			return err
		}
		if len(services) != len(svcIDs) {
			return errServiceChargeNotFound()
		}
		var fields []apperr.FieldError
		active := map[int64]bool{}
		for _, t := range taxes {
			active[t.ID] = t.IsActive
		}
		for i, r := range in.Taxes {
			if !active[r.TaxID] {
				fields = append(fields, fieldErr("taxes["+itoa(i)+"].tax_id", "TAX_INACTIVE", "an inactive tax cannot be mapped"))
			}
		}
		activeSvc := map[int64]bool{}
		for _, sv := range services {
			activeSvc[sv.ID] = sv.IsActive
		}
		for i, r := range in.ServiceCharges {
			if !activeSvc[r.ServiceChargeID] {
				fields = append(fields, fieldErr("service_charges["+itoa(i)+"].service_charge_id", "SERVICE_CHARGE_INACTIVE", "an inactive service charge cannot be mapped"))
			}
		}
		if len(fields) > 0 {
			return apperr.Invalid("the rules are invalid", fields...)
		}

		// Deactivate everything first: the unique (charge code, sequence) index only covers active rows,
		// so a reordering never collides with itself. Then upsert the listed rules as active.
		scope := billingconfigdb.DeactivateChargeCodeTaxesParams{TenantID: p.TenantID, PropertyID: propertyID, ChargeCodeID: chargeCodeID}
		if err := q.DeactivateChargeCodeTaxes(ctx, scope); err != nil {
			return err
		}
		if err := q.DeactivateChargeCodeServiceCharges(ctx, billingconfigdb.DeactivateChargeCodeServiceChargesParams(scope)); err != nil {
			return err
		}
		for _, r := range in.Taxes {
			if err := q.UpsertChargeCodeTax(ctx, billingconfigdb.UpsertChargeCodeTaxParams{
				TenantID: p.TenantID, PropertyID: propertyID, ChargeCodeID: chargeCodeID, TaxID: r.TaxID, Sequence: int16(r.Sequence), ActorID: p.ActorID(), //nolint:gosec // G115: validated to 1..32767
			}); err != nil {
				return err
			}
		}
		for _, r := range in.ServiceCharges {
			if err := q.UpsertChargeCodeServiceCharge(ctx, billingconfigdb.UpsertChargeCodeServiceChargeParams{
				TenantID: p.TenantID, PropertyID: propertyID, ChargeCodeID: chargeCodeID, ServiceChargeID: r.ServiceChargeID, Sequence: int16(r.Sequence), ActorID: p.ActorID(), //nolint:gosec // G115: validated to 1..32767
			}); err != nil {
				return err
			}
		}
		if out, err = s.loadChargeCode(ctx, p.TenantID, propertyID, chargeCodeID); err != nil {
			return err
		}
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "charge_code.rules_replaced", "charge_code", chargeCodeID,
			map[string]any{"taxes": before.Taxes, "service_charges": before.ServiceCharges},
			map[string]any{"taxes": out.Taxes, "service_charges": out.ServiceCharges}))
	})
	return out, err
}

// ---------------------------------------------------------------------------
// ChargeRuleResolver

// ChargeRules is a charge code with the rules that apply to it right now, in calculation order.
type ChargeRules struct {
	ChargeCodeID     int64
	Code             string
	ChargeType       string
	PriceMode        string
	DefaultUnitPrice *decimal.Decimal
	IsActive         bool
	Taxes            []ResolvedTax
	ServiceCharges   []ResolvedService
}

// ResolvedTax is a mapped tax with its rate as a decimal percentage.
type ResolvedTax struct {
	ID        int64
	Code      string
	Name      string
	Rate      decimal.Decimal
	OnService bool
	Sequence  int
}

// ResolvedService is a mapped service charge with its rate as a decimal percentage.
type ResolvedService struct {
	ID       int64
	Code     string
	Name     string
	Rate     decimal.Decimal
	Sequence int
}

// ResolveRules is the ChargeRuleResolver: the charge code of a property with its active, ordered tax and
// service rules. A mapping only applies while the tax or service charge itself is active. It reads the
// committed configuration and takes no locks, so it can run in any transaction (or none). Amounts are
// never computed here.
func (s *Service) ResolveRules(ctx context.Context, tenantID, propertyID, chargeCodeID int64) (ChargeRules, error) {
	q := s.q(ctx)
	row, err := q.GetChargeCode(ctx, billingconfigdb.GetChargeCodeParams{TenantID: tenantID, PropertyID: propertyID, ID: chargeCodeID})
	if err != nil {
		return ChargeRules{}, orNotFound(err, errChargeCodeNotFound())
	}
	out := ChargeRules{
		ChargeCodeID: row.ID, Code: row.Code, ChargeType: row.ChargeType, PriceMode: row.PriceMode,
		DefaultUnitPrice: row.DefaultUnitPrice, IsActive: row.IsActive, Taxes: []ResolvedTax{}, ServiceCharges: []ResolvedService{},
	}
	taxes, err := q.ListActiveTaxRules(ctx, billingconfigdb.ListActiveTaxRulesParams{TenantID: tenantID, PropertyID: propertyID, ChargeCodeID: &chargeCodeID})
	if err != nil {
		return ChargeRules{}, err
	}
	for _, t := range taxes {
		if t.TaxIsActive {
			out.Taxes = append(out.Taxes, ResolvedTax{ID: t.TaxID, Code: t.Code, Name: t.Name, Rate: t.Rate, OnService: t.TaxOnService, Sequence: int(t.Sequence)})
		}
	}
	services, err := q.ListActiveServiceRules(ctx, billingconfigdb.ListActiveServiceRulesParams{TenantID: tenantID, PropertyID: propertyID, ChargeCodeID: &chargeCodeID})
	if err != nil {
		return ChargeRules{}, err
	}
	for _, sv := range services {
		if sv.ServiceIsActive {
			out.ServiceCharges = append(out.ServiceCharges, ResolvedService{ID: sv.ServiceChargeID, Code: sv.Code, Name: sv.Name, Rate: sv.Rate, Sequence: int(sv.Sequence)})
		}
	}
	return out, nil
}
