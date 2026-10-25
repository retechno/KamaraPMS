package banquet

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The folio of an event, its deposit schedule and its cancellation policy (36 4.3, 4.4, 4.7).

const maxScheduleRows, maxTiers = 12, 12

func (s *Service) schedule(ctx context.Context, propertyID, eventID int64) ([]DepositRow, error) {
	rows, err := s.q(ctx).ListDepositSchedule(ctx, banquetdb.ListDepositScheduleParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	out := make([]DepositRow, len(rows))
	for i, r := range rows {
		out[i] = DepositRow{Seq: int(r.Seq), Label: r.Label, DueDate: r.DueDate, Percent: r.Percent, Amount: r.Amount}
	}
	return out, nil
}

func (s *Service) tiers(ctx context.Context, tenantID, propertyID int64, templateID, eventID *int64) ([]Tier, error) {
	rows, err := s.q(ctx).ListTiers(ctx, banquetdb.ListTiersParams{TenantID: tenantID, PropertyID: propertyID, TemplateID: templateID, EventID: eventID})
	if err != nil {
		return nil, err
	}
	out := make([]Tier, 0, len(rows))
	for _, r := range rows {
		t := Tier{ID: r.ID, DaysFrom: int(r.DaysFrom), Percent: r.Percent, Action: r.Action}
		if r.DaysTo != nil {
			t.DaysTo = ptr(int(*r.DaysTo))
		}
		out = append(out, t)
	}
	return out, nil
}

// folioRef reads the folio of an event with its totals, or nothing when it has none.
func (s *Service) folioRef(ctx context.Context, tenantID, propertyID, eventID int64) (*FolioRef, error) {
	f, err := s.folios.EventFolioOf(ctx, tenantID, propertyID, eventID)
	if err != nil || f == nil {
		return nil, err
	}
	t, err := s.folios.BillTotalsOf(ctx, propertyID, f.ID)
	if err != nil {
		return nil, err
	}
	return &FolioRef{ID: f.ID, Number: f.Number, Status: f.Status, Debit: t.Debit, Credit: t.Credit, Balance: t.Balance}, nil
}

// OpenFolio makes the EVENT folio of a tentative or definite event, or answers the one it has (banquet.manage): "Accept deposit" starts here, and the payment itself goes through the payment
// routes of the folio (payment.post). The two are two transactions, as the owner decided (Q2); a retry finds the same folio.
func (s *Service) OpenFolio(ctx context.Context, propertyID, eventID int64) (FolioRef, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return FolioRef{}, err
	}
	var out FolioRef
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if hdr.Status != StatusTentative && hdr.Status != StatusDefinite {
			return apperr.Conflict("EVENT_STATUS_INVALID", "only a tentative or a definite event has a folio").WithContext("status", hdr.Status)
		}
		f, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if f != nil && f.Status != "OPEN" {
			return apperr.Conflict("EVENT_FOLIO_CLOSED", "the folio of the event is closed").WithContext("folio_number", f.Number)
		}
		if f == nil {
			made, err := s.folios.OpenEventFolio(ctx, p, propertyID, eventID, hdr.BookerCompanyID)
			if err != nil {
				return err
			}
			if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.folio_opened", "banquet_event", eventID, hdr.Number, nil, map[string]any{"folio_id": made.ID, "folio_number": made.Number})); err != nil {
				return err
			}
		}
		ref, err := s.folioRef(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		out = *ref
		return nil
	})
	return out, err
}

// GetFolio answers the EVENT folio of an event, or 404 when it has none.
func (s *Service) GetFolio(ctx context.Context, propertyID, eventID int64) (FolioRef, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return FolioRef{}, err
	}
	if _, err := s.q(ctx).EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID}); err != nil {
		return FolioRef{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	ref, err := s.folioRef(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return FolioRef{}, err
	}
	if ref == nil {
		return FolioRef{}, notFound("FOLIO_NOT_FOUND", "folio of the event")
	}
	return *ref, nil
}

// ---------------------------------------------------------------- the money of the event page

// Money answers what the page of an event shows about its money.
func (s *Service) Money(ctx context.Context, propertyID, eventID int64) (MoneyView, error) {
	p, err := s.canRead(ctx, propertyID, eventReaders...)
	if err != nil {
		return MoneyView{}, err
	}
	q := s.q(ctx)
	hdr, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return MoneyView{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	st, err := s.settings(ctx, propertyID)
	if err != nil {
		return MoneyView{}, err
	}
	_, est, err := s.eventEstimate(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return MoneyView{}, err
	}
	v := MoneyView{PostingMode: st.PostingMode, Estimate: est, FeeItemID: hdr.CancelledFeeItemID, Schedule: []DepositView{}, Postings: []Posting{}, Groups: []GroupTarget{}}
	if hdr.PostingMode != nil {
		v.PostingMode, v.PostingModeOwn = *hdr.PostingMode, true
	}
	if v.Folio, err = s.folioRef(ctx, p.TenantID, propertyID, eventID); err != nil {
		return MoneyView{}, err
	}
	if v.Folio != nil {
		if v.DepositReceived, err = s.folios.EventDeposits(ctx, propertyID, v.Folio.ID); err != nil {
			return MoneyView{}, err
		}
	}
	today := s.today(ctx, propertyID)
	rows, err := s.schedule(ctx, propertyID, eventID)
	if err != nil {
		return MoneyView{}, err
	}
	cum := decimal.Zero
	for i, r := range rows {
		cum = cum.Add(r.Amount)
		status := RowStatus(rows, i, v.DepositReceived, today)
		v.Schedule = append(v.Schedule, DepositView{Seq: r.Seq, Label: r.Label, DueDate: r.DueDate, Percent: r.Percent, Amount: r.Amount, Cumulative: cum, Status: status})
		v.Overdue = v.Overdue || status == "OVERDUE"
	}
	v.ScheduleTotal = cum
	if v.Policy, err = s.policyOf(ctx, p.TenantID, propertyID, eventID, hdr.PolicyBase, today); err != nil {
		return MoneyView{}, err
	}
	posted, err := s.postings(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return MoneyView{}, err
	}
	for _, po := range posted {
		v.Postings = append(v.Postings, po)
	}
	slices.SortFunc(v.Postings, func(a, b Posting) int { return int(a.SessionID - b.SessionID) })
	gs, err := q.GroupsWithMaster(ctx, banquetdb.GroupsWithMasterParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return MoneyView{}, err
	}
	for _, g := range gs {
		v.Groups = append(v.Groups, GroupTarget{GroupID: g.ID, Code: g.GroupCode, Name: g.Name, FolioID: g.FolioID, FolioNumber: g.FolioNumber})
	}
	return v, nil
}

// policyOf is the policy of an event with the tier that applies today.
func (s *Service) policyOf(ctx context.Context, tenantID, propertyID, eventID int64, base *string, today civil.Date) (Policy, error) {
	tiers, err := s.tiers(ctx, tenantID, propertyID, nil, &eventID)
	if err != nil {
		return Policy{}, err
	}
	pol := Policy{Base: BaseNet, Tiers: tiers}
	if base != nil {
		pol.Base = *base
	}
	first, err := s.q(ctx).FirstSessionDate(ctx, banquetdb.FirstSessionDateParams{PropertyID: propertyID, EventID: eventID})
	if err != nil {
		return Policy{}, err
	}
	if !today.IsZero() && !first.Equal(civilEpoch) {
		pol.DaysLeft = DaysBefore(today, first)
		pol.Current = TierFor(tiers, pol.DaysLeft)
	}
	return pol, nil
}

// ---------------------------------------------------------------- the schedule of an event

func checkScheduleRow(i int, r DepositRowInput, decimals int32) []apperr.FieldError {
	var f []apperr.FieldError
	at := func(name string) string { return fmt.Sprintf("rows[%d].%s", i, name) }
	if n := len([]rune(strings.TrimSpace(r.Label))); n < 1 || n > 100 {
		f = append(f, fieldErr(at("label"), "INVALID_VALUE", "1 to 100 characters"))
	}
	if r.DueDate.IsZero() {
		f = append(f, fieldErr(at("due_date"), "REQUIRED", "the date the deposit is due"))
	}
	if (r.Percent == nil) == (r.Amount == nil) {
		f = append(f, fieldErr(at("percent"), "INVALID_VALUE", "a percent of the contract or an amount, one of the two"))
	}
	if r.Percent != nil && (!r.Percent.IsPositive() || r.Percent.GreaterThan(hundred) || r.Percent.Round(2).Cmp(*r.Percent) != 0) {
		f = append(f, fieldErr(at("percent"), "INVALID_VALUE", "above 0 up to 100, two decimals"))
	}
	if r.Amount != nil && (!r.Amount.IsPositive() || r.Amount.Round(decimals).Cmp(*r.Amount) != 0) {
		f = append(f, fieldErr(at("amount"), "INVALID_VALUE", "above zero, with the decimals of the currency"))
	}
	return f
}

// PutSchedule replaces the deposit schedule of an event (banquet.manage). A row with a percent follows the estimate of the contract until the event is closed; each change is audited.
func (s *Service) PutSchedule(ctx context.Context, propertyID, eventID int64, in ScheduleInput) ([]DepositView, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return nil, err
	}
	var f []apperr.FieldError
	if len(in.Rows) > maxScheduleRows {
		f = append(f, fieldErr("rows", "TOO_MANY_ROWS", "at most 12 rows"))
	}
	pct := decimal.Zero
	for i, r := range in.Rows {
		f = append(f, checkScheduleRow(i, r, decimals)...)
		if r.Percent != nil {
			pct = pct.Add(*r.Percent)
		}
	}
	if pct.GreaterThan(hundred) {
		f = append(f, fieldErr("rows", "INVALID_VALUE", "the percents add up to more than 100"))
	}
	if len(f) > 0 {
		return nil, invalid("deposit schedule", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		old, err := s.schedule(ctx, propertyID, eventID)
		if err != nil {
			return err
		}
		rows := make([]DepositRow, len(in.Rows))
		for i, r := range in.Rows {
			rows[i] = DepositRow{Seq: i + 1, Label: strings.TrimSpace(r.Label), DueDate: r.DueDate, Percent: r.Percent}
			if r.Amount != nil {
				rows[i].Amount = *r.Amount
			}
		}
		rows = ScheduleAmounts(rows, hdr.ContractValueEstimate, decimals)
		q := s.q(ctx)
		if err := q.DeleteDepositSchedule(ctx, banquetdb.DeleteDepositScheduleParams{PropertyID: propertyID, EventID: eventID}); err != nil {
			return err
		}
		for _, r := range rows {
			if err := q.InsertDepositRow(ctx, banquetdb.InsertDepositRowParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, Seq: int32(r.Seq), Label: r.Label, //nolint:gosec // G115: at most 12
				DueDate: r.DueDate, Percent: r.Percent, Amount: r.Amount}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.deposit_schedule_changed", "banquet_event", eventID, hdr.Number, old, rows))
	})
	if err != nil {
		return nil, err
	}
	v, err := s.Money(ctx, propertyID, eventID)
	return v.Schedule, err
}

// ---------------------------------------------------------------- the policy of an event

func checkTiers(in []TierInput) ([]Tier, []apperr.FieldError) {
	var f []apperr.FieldError
	if len(in) > maxTiers {
		f = append(f, fieldErr("tiers", "TOO_MANY_ROWS", "at most 12 tiers"))
	}
	out := make([]Tier, len(in))
	for i, t := range in {
		if t.DaysFrom < 0 || t.DaysFrom > 3650 || (t.DaysTo != nil && (*t.DaysTo < t.DaysFrom || *t.DaysTo > 3650)) {
			f = append(f, fieldErr("tiers", "INVALID_VALUE", "the days are from 0 to 3650, and the end is not before the start"))
		}
		if t.Percent.IsNegative() || t.Percent.GreaterThan(hundred) || t.Percent.Round(2).Cmp(t.Percent) != 0 {
			f = append(f, fieldErr("tiers", "INVALID_VALUE", "the percent is 0 to 100, two decimals"))
		}
		if t.Action != ActionFee && t.Action != ActionForfeit {
			f = append(f, fieldErr("tiers", "INVALID_VALUE", "FEE or FORFEIT"))
		}
		out[i] = Tier{DaysFrom: t.DaysFrom, DaysTo: t.DaysTo, Percent: t.Percent, Action: t.Action}
	}
	if len(f) == 0 && CheckTiers(out) >= 0 {
		f = append(f, apperr.FieldError{Field: "tiers", Code: "TIER_OVERLAP", Message: "two tiers share a day"})
	}
	return out, f
}

// PutPolicy sets the cancellation policy of an event: the base of the percent and the tiers (banquet.confirm). Every change is audited.
func (s *Service) PutPolicy(ctx context.Context, propertyID, eventID int64, in PolicyInput) (Policy, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetConfirm)
	if err != nil {
		return Policy{}, err
	}
	var f []apperr.FieldError
	if in.Base != BaseNet && in.Base != BaseGross {
		f = append(f, fieldErr("base", "INVALID_VALUE", "NET or GROSS"))
	}
	tiers, tf := checkTiers(in.Tiers)
	if f = append(f, tf...); len(f) > 0 {
		return Policy{}, invalid("policy", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		hdr, err := s.lockEvent(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if !editable(hdr.Status) {
			return apperr.Conflict("EVENT_STATUS_INVALID", "a closed event cannot be changed").WithContext("status", hdr.Status)
		}
		old, err := s.tiers(ctx, p.TenantID, propertyID, nil, &eventID)
		if err != nil {
			return err
		}
		if err := s.writeEventTiers(ctx, p.TenantID, propertyID, eventID, tiers); err != nil {
			return err
		}
		if err := s.q(ctx).SetEventPolicyBase(ctx, banquetdb.SetEventPolicyBaseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, PolicyBase: &in.Base, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.policy_changed", "banquet_event", eventID, hdr.Number, map[string]any{"base": hdr.PolicyBase, "tiers": old},
			map[string]any{"base": in.Base, "tiers": tiers}))
	})
	if err != nil {
		return Policy{}, err
	}
	hdr, err := s.q(ctx).EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return Policy{}, err
	}
	return s.policyOf(ctx, p.TenantID, propertyID, eventID, hdr.PolicyBase, s.today(ctx, propertyID))
}

func (s *Service) writeEventTiers(ctx context.Context, tenantID, propertyID, eventID int64, tiers []Tier) error {
	q := s.q(ctx)
	if err := q.DeleteEventTiers(ctx, banquetdb.DeleteEventTiersParams{PropertyID: propertyID, EventID: &eventID}); err != nil {
		return err
	}
	for _, t := range tiers {
		if err := q.InsertTier(ctx, banquetdb.InsertTierParams{TenantID: tenantID, PropertyID: propertyID, EventID: &eventID, DaysFrom: int32(t.DaysFrom), DaysTo: int32Ptr(t.DaysTo), //nolint:gosec // G115: validated
			Percent: t.Percent, Action: t.Action}); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- the templates

// ListPolicyTemplates answers the cancellation policies the property offers, with the deposits and the tiers of each.
func (s *Service) ListPolicyTemplates(ctx context.Context, propertyID int64, activeOnly *bool) ([]PolicyTemplate, error) {
	p, err := s.canRead(ctx, propertyID, masterReaders...)
	if err != nil {
		return nil, err
	}
	return s.templates(ctx, p.TenantID, propertyID, nil, activeOnly)
}

func (s *Service) templates(ctx context.Context, tenantID, propertyID int64, id *int64, activeOnly *bool) ([]PolicyTemplate, error) {
	q := s.q(ctx)
	rows, err := q.ListPolicyTemplates(ctx, banquetdb.ListPolicyTemplatesParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Active: activeOnly})
	if err != nil {
		return nil, err
	}
	deps, err := q.ListTemplateDeposits(ctx, banquetdb.ListTemplateDepositsParams{PropertyID: propertyID, TemplateID: id})
	if err != nil {
		return nil, err
	}
	out := make([]PolicyTemplate, len(rows))
	for i, r := range rows {
		out[i] = PolicyTemplate{ID: r.ID, Code: r.Code, Name: r.Name, IsDefault: r.IsDefault, Base: r.Base, IsActive: r.IsActive, Deposits: []TemplateDeposit{}}
		for _, d := range deps {
			if d.TemplateID == r.ID {
				td := TemplateDeposit{Seq: int(d.Seq), Label: d.Label, Percent: d.Percent}
				if d.DaysBefore != nil {
					td.DaysBefore = ptr(int(*d.DaysBefore))
				}
				out[i].Deposits = append(out[i].Deposits, td)
			}
		}
		if out[i].Tiers, err = s.tiers(ctx, tenantID, propertyID, &r.ID, nil); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func checkTemplate(in PolicyTemplateInput, create bool) ([]Tier, []apperr.FieldError) {
	var f []apperr.FieldError
	if create {
		f = append(f, checkCode("code", in.Code)...)
	}
	f = append(f, checkName("name", in.Name)...)
	if in.Base != BaseNet && in.Base != BaseGross {
		f = append(f, fieldErr("base", "INVALID_VALUE", "NET or GROSS"))
	}
	if len(in.Deposits) > maxScheduleRows {
		f = append(f, fieldErr("deposits", "TOO_MANY_ROWS", "at most 12 deposits"))
	}
	sum := decimal.Zero
	for _, d := range in.Deposits {
		if n := len([]rune(strings.TrimSpace(d.Label))); n < 1 || n > 100 {
			f = append(f, fieldErr("deposits", "INVALID_VALUE", "a deposit has a label of 1 to 100 characters"))
		}
		if d.DaysBefore != nil && (*d.DaysBefore < 0 || *d.DaysBefore > 3650) {
			f = append(f, fieldErr("deposits", "INVALID_VALUE", "the days before are 0 to 3650"))
		}
		if !d.Percent.IsPositive() || d.Percent.GreaterThan(hundred) || d.Percent.Round(2).Cmp(d.Percent) != 0 {
			f = append(f, fieldErr("deposits", "INVALID_VALUE", "the percent is above 0 up to 100, two decimals"))
		}
		sum = sum.Add(d.Percent)
	}
	if sum.GreaterThan(hundred) {
		f = append(f, fieldErr("deposits", "INVALID_VALUE", "the percents add up to more than 100"))
	}
	tiers, tf := checkTiers(in.Tiers)
	return tiers, append(f, tf...)
}

// CreatePolicyTemplate adds a policy template (banquet.setup). The default one is the template a confirmed event copies when none is named.
func (s *Service) CreatePolicyTemplate(ctx context.Context, propertyID int64, in PolicyTemplateInput) (PolicyTemplate, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return PolicyTemplate{}, err
	}
	in.Code, in.Name = normalCode(in.Code), strings.TrimSpace(in.Name)
	tiers, f := checkTemplate(in, true)
	if len(f) > 0 {
		return PolicyTemplate{}, invalid("policy template", f...)
	}
	var out PolicyTemplate
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		id, err := q.InsertPolicyTemplate(ctx, banquetdb.InsertPolicyTemplateParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, IsDefault: false, Base: in.Base, IsActive: active(in.IsActive)})
		if err != nil {
			return err
		}
		if err := s.writeTemplate(ctx, p.TenantID, propertyID, id, in, tiers); err != nil {
			return err
		}
		if out, err = s.oneTemplate(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.policy_template_created", "banquet_policy_template", id, in.Code, nil, out))
	})
	return out, err
}

// UpdatePolicyTemplate changes a template (banquet.setup); the events that copied it keep what they copied.
func (s *Service) UpdatePolicyTemplate(ctx context.Context, propertyID, id int64, in PolicyTemplateInput) (PolicyTemplate, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetSetup)
	if err != nil {
		return PolicyTemplate{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	tiers, f := checkTemplate(in, false)
	if len(f) > 0 {
		return PolicyTemplate{}, invalid("policy template", f...)
	}
	var out PolicyTemplate
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		old, err := s.oneTemplate(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.writeTemplate(ctx, p.TenantID, propertyID, id, in, tiers); err != nil {
			return err
		}
		if out, err = s.oneTemplate(ctx, p.TenantID, propertyID, id); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.policy_template_updated", "banquet_policy_template", id, old.Code, old, out))
	})
	return out, err
}

func (s *Service) oneTemplate(ctx context.Context, tenantID, propertyID, id int64) (PolicyTemplate, error) {
	ts, err := s.templates(ctx, tenantID, propertyID, &id, nil)
	if err != nil {
		return PolicyTemplate{}, err
	}
	if len(ts) == 0 {
		return PolicyTemplate{}, notFound("POLICY_TEMPLATE_NOT_FOUND", "policy template")
	}
	return ts[0], nil
}

// writeTemplate replaces the deposits and the tiers of a template and keeps one default: making this one the default takes it from the other (the database allows only one).
func (s *Service) writeTemplate(ctx context.Context, tenantID, propertyID, id int64, in PolicyTemplateInput, tiers []Tier) error {
	q := s.q(ctx)
	if in.IsDefault && active(in.IsActive) {
		if err := q.ClearDefaultTemplate(ctx, banquetdb.ClearDefaultTemplateParams{TenantID: tenantID, PropertyID: propertyID, ExceptID: id}); err != nil {
			return err
		}
	}
	if _, err := q.UpdatePolicyTemplate(ctx, banquetdb.UpdatePolicyTemplateParams{TenantID: tenantID, PropertyID: propertyID, ID: id, Name: in.Name, IsDefault: in.IsDefault && active(in.IsActive), Base: in.Base,
		IsActive: active(in.IsActive)}); err != nil {
		return err
	}
	if err := q.DeleteTemplateDeposits(ctx, banquetdb.DeleteTemplateDepositsParams{PropertyID: propertyID, TemplateID: id}); err != nil {
		return err
	}
	for i, d := range in.Deposits {
		if err := q.InsertTemplateDeposit(ctx, banquetdb.InsertTemplateDepositParams{PropertyID: propertyID, TemplateID: id, Seq: int32(i + 1), Label: strings.TrimSpace(d.Label), //nolint:gosec // G115: at most 12
			DaysBefore: int32Ptr(d.DaysBefore), Percent: d.Percent}); err != nil {
			return err
		}
	}
	if err := q.DeleteTemplateTiers(ctx, banquetdb.DeleteTemplateTiersParams{PropertyID: propertyID, TemplateID: &id}); err != nil {
		return err
	}
	for _, t := range tiers {
		if err := q.InsertTier(ctx, banquetdb.InsertTierParams{TenantID: tenantID, PropertyID: propertyID, TemplateID: &id, DaysFrom: int32(t.DaysFrom), DaysTo: int32Ptr(t.DaysTo), //nolint:gosec // G115: validated
			Percent: t.Percent, Action: t.Action}); err != nil {
			return err
		}
	}
	return nil
}

// copyPolicy copies a template to the event at Definite (36 4.4): its base, its tiers and its deposit schedule. What the event already has stays: tiers or a schedule set by hand on the tentative
// event are not overwritten. The caller holds the event and the open business day.
func (s *Service) copyPolicy(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, eventID int64, templateID *int64) (*PolicyTemplate, error) {
	q := s.q(ctx)
	id := templateID
	if id == nil {
		d, err := q.DefaultPolicyTemplate(ctx, banquetdb.DefaultPolicyTemplateParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return nil, orNil(err)
		}
		id = &d.ID
	}
	tpl, err := s.oneTemplate(ctx, p.TenantID, propertyID, *id)
	if err != nil {
		return nil, err
	}
	if !tpl.IsActive {
		return nil, invalidCode("POLICY_TEMPLATE_NOT_FOUND", "policy_template_id", "the policy template is not active")
	}
	have, err := s.tiers(ctx, p.TenantID, propertyID, nil, &eventID)
	if err != nil {
		return nil, err
	}
	hdr, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return nil, err
	}
	if len(have) == 0 && hdr.PolicyBase == nil {
		if err := s.writeEventTiers(ctx, p.TenantID, propertyID, eventID, tpl.Tiers); err != nil {
			return nil, err
		}
		if err := q.SetEventPolicyBase(ctx, banquetdb.SetEventPolicyBaseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, PolicyBase: &tpl.Base, ActorID: p.ActorID()}); err != nil {
			return nil, err
		}
	}
	rows, err := s.schedule(ctx, propertyID, eventID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && len(tpl.Deposits) > 0 {
		decimals, err := s.decimals(ctx, p.TenantID, propertyID)
		if err != nil {
			return nil, err
		}
		first, err := q.FirstSessionDate(ctx, banquetdb.FirstSessionDateParams{PropertyID: propertyID, EventID: eventID})
		if err != nil {
			return nil, err
		}
		var plan []DepositRow
		for i, d := range tpl.Deposits {
			pct := d.Percent
			plan = append(plan, DepositRow{Seq: i + 1, Label: d.Label, DueDate: dueDate(bd, first, d.DaysBefore), Percent: &pct})
		}
		for _, r := range ScheduleAmounts(plan, hdr.ContractValueEstimate, decimals) {
			if err := q.InsertDepositRow(ctx, banquetdb.InsertDepositRowParams{TenantID: p.TenantID, PropertyID: propertyID, EventID: eventID, Seq: int32(r.Seq), Label: r.Label, //nolint:gosec // G115: at most 12
				DueDate: r.DueDate, Percent: r.Percent, Amount: r.Amount}); err != nil {
				return nil, err
			}
		}
	}
	return &tpl, nil
}

// dueDate is the day a deposit of a template is due: so many days before the first session, never before the day of the confirmation, which is the day of the signature.
func dueDate(bd, first civil.Date, daysBefore *int) civil.Date {
	if daysBefore == nil || first.IsZero() || first.Equal(civilEpoch) {
		return bd
	}
	d := first.AddDays(-*daysBefore)
	if d.Before(bd) {
		return bd
	}
	return d
}

func orNil(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}
