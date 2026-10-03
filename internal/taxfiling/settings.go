package taxfiling

import (
	"context"
	"strconv"
	"strings"
	"time"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/taxfiling/taxfilingdb"
)

// How the VAT paid on purchases is treated (design: docs/architecture/09-pkp-input-vat.md).
const (
	InputVATCreditable = "CREDITABLE" // claimed against the VAT collected (a PKP property only)
	InputVATExpense    = "EXPENSE"    // added to the cost of the purchase
	InputVATDeferred   = "DEFERRED"   // kept apart, not claimed
)

// TaxSettings is the tax status of a property from a date on.
type TaxSettings struct {
	ID                int64       `json:"id"`
	EffectiveFrom     civil.Date  `json:"effective_from"`
	IsPKP             bool        `json:"is_pkp"`
	NPWP              string      `json:"npwp,omitempty"`
	PKPNumber         string      `json:"pkp_number,omitempty"`
	PKPConfirmedOn    *civil.Date `json:"pkp_confirmed_on,omitempty"`
	InputVATTreatment string      `json:"input_vat_treatment"`
	SignerName        string      `json:"signer_name,omitempty"`
	SignerTitle       string      `json:"signer_title,omitempty"`
	ApprovedBy        *int64      `json:"approved_by,omitempty"`
	CreatedAt         time.Time   `json:"created_at"`
}

// SettingsView is the status in force on the business date and the whole history (newest first).
type SettingsView struct {
	Current TaxSettings   `json:"current"`
	History []TaxSettings `json:"history"`
}

// SettingsInput adds a change of the tax status. A change that begins before the business date needs an approval.
type SettingsInput struct {
	EffectiveFrom     civil.Date         `json:"effective_from"`
	IsPKP             bool               `json:"is_pkp"`
	NPWP              string             `json:"npwp"`
	PKPNumber         string             `json:"pkp_number"`
	PKPConfirmedOn    *civil.Date        `json:"pkp_confirmed_on"`
	InputVATTreatment string             `json:"input_vat_treatment"` // "" = CREDITABLE for a PKP property, EXPENSE otherwise
	SignerName        string             `json:"signer_name"`
	SignerTitle       string             `json:"signer_title"`
	Approval          *iam.ApprovalInput `json:"approval"`
}

// SettingsOn is the status in force on a date: the latest change that began on or before it. history is newest first.
func SettingsOn(history []TaxSettings, d civil.Date) (TaxSettings, bool) {
	for _, h := range history {
		if !h.EffectiveFrom.After(d) {
			return h, true
		}
	}
	return TaxSettings{}, false
}

func toSettings(r taxfilingdb.ListTaxSettingsRow) TaxSettings {
	return TaxSettings{
		ID: r.ID, EffectiveFrom: r.EffectiveFrom, IsPKP: r.IsPkp, NPWP: deref(r.Npwp), PKPNumber: deref(r.PkpNumber), PKPConfirmedOn: r.PkpConfirmedOn,
		InputVATTreatment: r.InputVatTreatment, SignerName: deref(r.SignerName), SignerTitle: deref(r.SignerTitle), ApprovedBy: r.ApprovedBy, CreatedAt: r.CreatedAt,
	}
}

func (s *Service) settingsHistory(ctx context.Context, tenantID, propertyID int64) ([]TaxSettings, error) {
	rows, err := s.q(ctx).ListTaxSettings(ctx, taxfilingdb.ListTaxSettingsParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]TaxSettings, 0, len(rows))
	for _, r := range rows {
		out = append(out, toSettings(r))
	}
	return out, nil
}

// Settings is the tax status of the property in force on the business date, with its history (tax.view).
func (s *Service) Settings(ctx context.Context, propertyID int64) (SettingsView, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxView)
	if err != nil {
		return SettingsView{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return SettingsView{}, err
	}
	history, err := s.settingsHistory(ctx, p.TenantID, propertyID)
	if err != nil {
		return SettingsView{}, err
	}
	cur, _ := SettingsOn(history, day.BusinessDate)
	return SettingsView{Current: cur, History: history}, nil
}

// SettingsOnDate is the tax status of the property on a date, read under a share lock on the settings so a change cannot
// slip in while a document of that date is being posted. It joins the ambient transaction (lock level of the tax).
func (s *Service) SettingsOnDate(ctx context.Context, tenantID, propertyID int64, d civil.Date) (TaxSettings, error) {
	q := s.q(ctx)
	first, err := q.FirstTaxSettingsID(ctx, taxfilingdb.FirstTaxSettingsIDParams{TenantID: tenantID, PropertyID: propertyID})
	if err != nil {
		return TaxSettings{}, err
	}
	if err := db.LockRows(ctx, db.TaxSettings, db.ForShare, propertyID, []int64{first}); err != nil {
		return TaxSettings{}, err
	}
	history, err := s.settingsHistory(ctx, tenantID, propertyID)
	if err != nil {
		return TaxSettings{}, err
	}
	on, _ := SettingsOn(history, d)
	return on, nil
}

func validateSettings(in SettingsInput) []apperr.FieldError {
	var fields []apperr.FieldError
	for _, l := range []struct {
		name, v string
		max     int
	}{{"npwp", in.NPWP, 30}, {"pkp_number", in.PKPNumber, 40}, {"signer_name", in.SignerName, 150}, {"signer_title", in.SignerTitle, 100}} {
		if len([]rune(l.v)) > l.max {
			fields = append(fields, fieldErr(l.name, "TOO_LONG", "at most "+strconv.Itoa(l.max)+" characters"))
		}
	}
	if in.IsPKP && in.NPWP == "" {
		fields = append(fields, fieldErr("npwp", "REQUIRED", "the tax number (NPWP) of a PKP property"))
	}
	switch in.InputVATTreatment {
	case InputVATCreditable:
		if !in.IsPKP {
			fields = append(fields, fieldErr("input_vat_treatment", "NOT_PKP", "a property that is not PKP cannot claim input VAT"))
		}
	case InputVATExpense, InputVATDeferred:
	default:
		fields = append(fields, fieldErr("input_vat_treatment", "INVALID_VALUE", "CREDITABLE, EXPENSE or DEFERRED"))
	}
	return fields
}

// ChangeSettings adds a change of the tax status (tax.manage). History only moves forward: the change must begin after the
// latest one, and one that begins before the business date is backdated and needs an approval. Documents already posted
// keep the status they were posted under; the change only governs documents dated on or after it.
func (s *Service) ChangeSettings(ctx context.Context, propertyID int64, in SettingsInput) (SettingsView, error) {
	p, err := s.need(ctx, propertyID, auth.PermTaxManage)
	if err != nil {
		return SettingsView{}, err
	}
	in.NPWP, in.PKPNumber = strings.TrimSpace(in.NPWP), strings.TrimSpace(in.PKPNumber)
	in.SignerName, in.SignerTitle = strings.TrimSpace(in.SignerName), strings.TrimSpace(in.SignerTitle)
	in.InputVATTreatment = strings.ToUpper(strings.TrimSpace(in.InputVATTreatment))
	if in.InputVATTreatment == "" {
		in.InputVATTreatment = InputVATExpense
		if in.IsPKP {
			in.InputVATTreatment = InputVATCreditable
		}
	}
	if in.EffectiveFrom.IsZero() {
		return SettingsView{}, apperr.Invalid("the tax status is invalid", fieldErr("effective_from", "REQUIRED", "the date the change begins"))
	}
	if fields := validateSettings(in); len(fields) > 0 {
		return SettingsView{}, apperr.Invalid("the tax status is invalid", fields...)
	}
	today, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return SettingsView{}, err
	}
	var approvedBy *int64
	if in.EffectiveFrom.Before(today.BusinessDate) {
		approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
		if err != nil {
			return SettingsView{}, err
		}
		by := approval.UserID()
		approvedBy = &by
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.CurrentBusinessDay(ctx, propertyID)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		first, err := q.FirstTaxSettingsID(ctx, taxfilingdb.FirstTaxSettingsIDParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.TaxSettings, db.ForUpdate, propertyID, []int64{first}); err != nil {
			return err
		}
		history, err := s.settingsHistory(ctx, p.TenantID, propertyID)
		if err != nil {
			return err
		}
		latest := history[0]
		if !in.EffectiveFrom.After(latest.EffectiveFrom) {
			return apperr.Conflict("TAX_SETTINGS_NOT_NEWER", "a change begins after the latest one; the history only moves forward").
				WithContext("latest_effective_from", latest.EffectiveFrom.String())
		}
		id, err := q.InsertTaxSettings(ctx, taxfilingdb.InsertTaxSettingsParams{
			TenantID: p.TenantID, PropertyID: propertyID, EffectiveFrom: in.EffectiveFrom, IsPkp: in.IsPKP, Npwp: nullable(in.NPWP), PkpNumber: nullable(in.PKPNumber),
			PkpConfirmedOn: in.PKPConfirmedOn, InputVatTreatment: in.InputVATTreatment, SignerName: nullable(in.SignerName), SignerTitle: nullable(in.SignerTitle),
			ApprovedBy: approvedBy, ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, entry(p, propertyID, day.BusinessDate, "tax.settings_changed", "property_tax_settings", id,
			map[string]any{"is_pkp": latest.IsPKP, "input_vat_treatment": latest.InputVATTreatment, "effective_from": latest.EffectiveFrom.String()},
			map[string]any{"is_pkp": in.IsPKP, "input_vat_treatment": in.InputVATTreatment, "effective_from": in.EffectiveFrom.String(), "backdated": approvedBy != nil, "approved_by": approvedBy}))
	})
	if err != nil {
		return SettingsView{}, err
	}
	return s.Settings(ctx, propertyID)
}
