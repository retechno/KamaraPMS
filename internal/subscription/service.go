package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/entitlement"
	"kamarapms/internal/subscription/subscriptiondb"
	"kamarapms/internal/tenancy"
)

// Service is the subscription application service. It is also the entitlement.Gate of the modules
// whose records a plan limits.
type Service struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
}

var _ entitlement.Gate = (*Service)(nil)

// NewService wires the subscription service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer) *Service {
	return &Service{txm: txm, clock: c, audit: a}
}

func (s *Service) q(ctx context.Context) *subscriptiondb.Queries {
	return subscriptiondb.New(s.txm.DB(ctx))
}

// today is the calendar day of a tenant now.
func (s *Service) today(timezone string) civil.Date {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC // unreachable: tenant zones are validated on write
	}
	return civil.DateOf(s.clock.Now().In(loc))
}

func errTenantNotFound() *apperr.Error {
	return apperr.NotFound("TENANT_NOT_FOUND", "no tenant with this code")
}

func errPlanNotFound() *apperr.Error {
	return apperr.NotFound("PLAN_NOT_FOUND", "no plan with this code")
}

func errNoSubscription() *apperr.Error {
	return apperr.NotFound("SUBSCRIPTION_NOT_FOUND", "the tenant has no subscription")
}

func errInvoiceNotFound() *apperr.Error {
	return apperr.NotFound("INVOICE_NOT_FOUND", "no subscription invoice with this number")
}

func notFound(err error, nf *apperr.Error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return nf
	}
	return err
}

// ---------------------------------------------------------------------------
// The tenant's own view

// Summary returns the caller's subscription, with the limits in force and the usage.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Summary{}, err
	}
	return s.summary(ctx, p.TenantID)
}

func (s *Service) summary(ctx context.Context, tenantID int64) (Summary, error) {
	q := s.q(ctx)
	row, err := q.GetSubscription(ctx, tenantID)
	if err != nil {
		return Summary{}, notFound(err, errNoSubscription())
	}
	sub := subscriptionFrom(subscriptiondb.LockSubscriptionRow(row))
	planRow, err := q.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return Summary{}, err
	}
	plan := toPlan(planRow)
	u, err := q.Usage(ctx, tenantID)
	if err != nil {
		return Summary{}, err
	}
	today := s.today(row.Timezone)
	return Summary{
		PlanCode: plan.Code, PlanName: plan.Name, Modules: append([]Module{Core}, plan.Modules...),
		Status: sub.Status, Access: sub.Access(today), StartedOn: sub.StartedOn, PaidThrough: sub.PaidThrough,
		GraceEndsOn: sub.GraceEndsOn(), DaysLeft: daysLeft(sub, today),
		Limits: sub.Overrides.Over(plan.Limits),
		Usage:  Usage{Properties: int(u.Properties), Rooms: int(u.Rooms), Users: int(u.Users)},
	}, nil
}

// Invoices lists the caller's subscription invoices, newest first (tenant administrators).
func (s *Service) Invoices(ctx context.Context) ([]Invoice, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return nil, err
	}
	return s.invoices(ctx, p.TenantID)
}

func (s *Service) invoices(ctx context.Context, tenantID int64) ([]Invoice, error) {
	rows, err := s.q(ctx).ListInvoices(ctx, subscriptiondb.ListInvoicesParams{TenantID: tenantID, Lim: 200})
	if err != nil {
		return nil, err
	}
	out := make([]Invoice, 0, len(rows))
	for _, r := range rows {
		inv := toInvoice(subscriptiondb.SubscriptionInvoice{
			ID: r.ID, TenantID: r.TenantID, Number: r.Number, PlanID: r.PlanID, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd,
			Amount: r.Amount, CurrencyCode: r.CurrencyCode, Status: r.Status, IssuedAt: r.IssuedAt, DueOn: r.DueOn, PaidOn: r.PaidOn,
			PaidAt: r.PaidAt, PaymentReference: r.PaymentReference, VoidedAt: r.VoidedAt, VoidReason: r.VoidReason, Notes: r.Notes,
		})
		inv.PlanCode = r.PlanCode
		out = append(out, inv)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Access (the API gate, §3 and §4)

// Access is what the API gate reads for one request.
type Access struct {
	State   AccessState
	Modules []Module
}

// Has says whether the plan includes a module.
func (a Access) Has(m Module) bool { return Plan{Modules: a.Modules}.Has(m) }

// AccessOf returns a tenant's access state and modules now.
func (s *Service) AccessOf(ctx context.Context, tenantID int64) (Access, error) {
	row, err := s.q(ctx).GetAccess(ctx, tenantID)
	if err != nil {
		return Access{}, notFound(err, errNoSubscription())
	}
	sub := Subscription{Status: row.Status, PaidThrough: row.PaidThrough, GraceDays: int(row.GraceDays), StartedOn: row.StartedOn}
	return Access{State: sub.Access(s.today(row.Timezone)), Modules: toModules(row.Modules)}, nil
}

// ---------------------------------------------------------------------------
// Quotas (entitlement.Gate, §5)

// Hold locks the tenant's subscription row for the rest of the transaction.
func (s *Service) Hold(ctx context.Context, tenantID int64) error {
	if err := db.EnterLockLevel(ctx, db.LevelSubscription); err != nil {
		return err
	}
	_, err := s.q(ctx).LockSubscription(ctx, tenantID)
	return notFound(err, errNoSubscription())
}

// Check refuses with 409 PLAN_LIMIT_REACHED when adding more of a resource would go over the limit.
func (s *Service) Check(ctx context.Context, tenantID int64, r entitlement.Resource, adding int) error {
	if adding <= 0 {
		return nil
	}
	q := s.q(ctx)
	row, err := q.GetSubscription(ctx, tenantID)
	if err != nil {
		return notFound(err, errNoSubscription())
	}
	sub := subscriptionFrom(subscriptiondb.LockSubscriptionRow(row))
	plan, err := q.GetPlan(ctx, sub.PlanID)
	if err != nil {
		return err
	}
	limit := sub.Overrides.Over(toPlan(plan).Limits).Of(r)
	if limit == nil {
		return nil
	}
	u, err := q.Usage(ctx, tenantID)
	if err != nil {
		return err
	}
	used := Usage{Properties: int(u.Properties), Rooms: int(u.Rooms), Users: int(u.Users)}.Of(r)
	if used+adding > *limit {
		return apperr.Conflict("PLAN_LIMIT_REACHED", fmt.Sprintf("the plan allows %d %s and %d are in use", *limit, r, used)).
			WithContext("resource", string(r)).WithContext("limit", *limit).WithContext("used", used)
	}
	return nil
}

// Require is Hold followed by Check.
func (s *Service) Require(ctx context.Context, tenantID int64, r entitlement.Resource, adding int) error {
	if err := s.Hold(ctx, tenantID); err != nil {
		return err
	}
	return s.Check(ctx, tenantID, r, adding)
}

// ---------------------------------------------------------------------------
// A new tenant (tenancy.OnTenantCreated hook)

// StartTenant gives a new tenant the default plan, in the transaction that creates the tenant: a trial
// when the plan has trial days, otherwise an ACTIVE subscription whose first period is unpaid (its
// grace period runs from the first day).
func (s *Service) StartTenant(ctx context.Context, t tenancy.TenantCreated) error {
	q := s.q(ctx)
	plan, err := q.GetDefaultPlan(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return apperr.Conflict("NO_DEFAULT_PLAN", "no plan is marked as the default for new tenants")
	}
	if err != nil {
		return err
	}
	today := s.today(t.Timezone)
	status, through := StatusTrial, today.AddDays(int(plan.TrialDays)-1)
	if plan.TrialDays == 0 {
		status = StatusActive
	}
	row, err := q.InsertSubscription(ctx, subscriptiondb.InsertSubscriptionParams{
		TenantID: t.TenantID, PlanID: plan.ID, Status: status, StartedOn: today, PaidThrough: &through, GraceDays: defaultGraceDays,
	})
	if err != nil {
		return err
	}
	return s.audit.Write(ctx, audit.Entry{TenantID: t.TenantID, Action: "subscription.started", EntityType: "subscription",
		EntityID: row.ID, EntityLabel: plan.Code, New: subscriptionFrom(lockRowOf(row, t.Timezone))})
}

const defaultGraceDays = 14

// ---------------------------------------------------------------------------
// Plans (operator, §8)

// ListPlans returns every plan, active ones first.
func (s *Service) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.q(ctx).ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Plan, 0, len(rows))
	for _, r := range rows {
		out = append(out, toPlan(r))
	}
	return out, nil
}

// CreatePlan adds a plan. A new default plan takes the flag from the previous one.
func (s *Service) CreatePlan(ctx context.Context, in PlanInput) (Plan, error) {
	in.Normalize()
	if f := in.Validate(); len(f) > 0 {
		return Plan{}, apperr.Invalid("the plan is invalid", f...)
	}
	var out Plan
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		if in.IsDefault {
			if err := q.ClearDefaultPlan(ctx, in.Code); err != nil {
				return err
			}
		}
		row, err := q.CreatePlan(ctx, subscriptiondb.CreatePlanParams{
			Code: in.Code, Name: in.Name, Modules: fromModules(in.Modules),
			MaxProperties: int4(in.Limits.MaxProperties), MaxRooms: int4(in.Limits.MaxRooms), MaxUsers: int4(in.Limits.MaxUsers),
			Price: in.Price, CurrencyCode: in.CurrencyCode, BillingPeriodMonths: int16(in.BillingPeriodMonths), //nolint:gosec // G115: validated to 1..12
			TrialDays: int16(in.TrialDays), IsActive: in.IsActive, IsDefault: in.IsDefault, //nolint:gosec // G115: validated to 0..365
		})
		if err != nil {
			return err
		}
		out = toPlan(row)
		return nil
	})
	return out, err
}

// UpdatePlan replaces the editable part of a plan. The default plan cannot give up the flag except by
// another plan taking it (a new tenant always needs a plan).
func (s *Service) UpdatePlan(ctx context.Context, in PlanInput) (Plan, error) {
	in.Normalize()
	if f := in.Validate(); len(f) > 0 {
		return Plan{}, apperr.Invalid("the plan is invalid", f...)
	}
	var out Plan
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		before, err := q.GetPlanByCode(ctx, in.Code)
		if err != nil {
			return notFound(err, errPlanNotFound())
		}
		if before.IsDefault && !in.IsDefault {
			return apperr.Conflict("DEFAULT_PLAN_REQUIRED", "make another plan the default instead")
		}
		if in.IsDefault {
			if err := q.ClearDefaultPlan(ctx, in.Code); err != nil {
				return err
			}
		}
		row, err := q.UpdatePlan(ctx, subscriptiondb.UpdatePlanParams{
			Code: in.Code, Name: in.Name, Modules: fromModules(in.Modules),
			MaxProperties: int4(in.Limits.MaxProperties), MaxRooms: int4(in.Limits.MaxRooms), MaxUsers: int4(in.Limits.MaxUsers),
			Price: in.Price, CurrencyCode: in.CurrencyCode, BillingPeriodMonths: int16(in.BillingPeriodMonths), //nolint:gosec // G115: validated to 1..12
			TrialDays: int16(in.TrialDays), IsActive: in.IsActive, IsDefault: in.IsDefault, //nolint:gosec // G115: validated to 0..365
		})
		if err != nil {
			return err
		}
		out = toPlan(row)
		return nil
	})
	return out, err
}

// PlanByCode returns one plan.
func (s *Service) PlanByCode(ctx context.Context, code string) (Plan, error) {
	row, err := s.q(ctx).GetPlanByCode(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if err != nil {
		return Plan{}, notFound(err, errPlanNotFound())
	}
	return toPlan(row), nil
}

// ---------------------------------------------------------------------------
// A tenant's subscription (operator, §8)

// TenantView is a subscription as the operator sees it.
type TenantView struct {
	TenantCode   string       `json:"tenant_code"`
	Subscription Subscription `json:"subscription"`
	Summary      Summary      `json:"summary"`
}

// Show returns a tenant's subscription and summary.
func (s *Service) Show(ctx context.Context, tenantCode string) (TenantView, error) {
	t, err := s.q(ctx).GetTenantForSubscription(ctx, strings.ToUpper(strings.TrimSpace(tenantCode)))
	if err != nil {
		return TenantView{}, notFound(err, errTenantNotFound())
	}
	row, err := s.q(ctx).GetSubscription(ctx, t.ID)
	if err != nil {
		return TenantView{}, notFound(err, errNoSubscription())
	}
	sum, err := s.summary(ctx, t.ID)
	if err != nil {
		return TenantView{}, err
	}
	return TenantView{TenantCode: t.Code, Subscription: subscriptionFrom(subscriptiondb.LockSubscriptionRow(row)), Summary: sum}, nil
}

// ChangeInput is an operator change of a subscription; nil fields stay as they are.
type ChangeInput struct {
	PlanCode *string
	// PaidThrough sets the last paid day; NoEnd removes the end (paid_through NULL).
	PaidThrough *civil.Date
	NoEnd       bool
	GraceDays   *int
	Status      *string
	// Limits overrides: a set field replaces the override; ClearLimits names the ones that go back to the plan's.
	Limits      Limits
	ClearLimits []entitlement.Resource
}

// Change applies an operator change to a tenant's subscription (subscribe, set-limits, cancel).
func (s *Service) Change(ctx context.Context, tenantCode string, in ChangeInput) (Subscription, error) {
	if in.GraceDays != nil && (*in.GraceDays < 0 || *in.GraceDays > 90) {
		return Subscription{}, apperr.Invalid("the subscription is invalid", apperr.FieldError{Field: "grace_days", Code: "INVALID_VALUE", Message: "0 to 90"})
	}
	if in.Status != nil && *in.Status != StatusTrial && *in.Status != StatusActive && *in.Status != StatusCancelled {
		return Subscription{}, apperr.Invalid("the subscription is invalid", apperr.FieldError{Field: "status", Code: "INVALID_VALUE", Message: "TRIAL, ACTIVE or CANCELLED"})
	}
	var out Subscription
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		t, err := q.GetTenantForSubscription(ctx, strings.ToUpper(strings.TrimSpace(tenantCode)))
		if err != nil {
			return notFound(err, errTenantNotFound())
		}
		if err := db.EnterLockLevel(ctx, db.LevelSubscription); err != nil {
			return err
		}
		row, err := q.LockSubscription(ctx, t.ID)
		if err != nil {
			return notFound(err, errNoSubscription())
		}
		before := subscriptionFrom(row)
		after := before
		label := ""
		if in.PlanCode != nil {
			plan, err := q.GetPlanByCode(ctx, strings.ToUpper(strings.TrimSpace(*in.PlanCode)))
			if err != nil {
				return notFound(err, errPlanNotFound())
			}
			if !plan.IsActive && plan.ID != before.PlanID {
				return apperr.Conflict("PLAN_INACTIVE", "the plan is retired and cannot be given to a tenant")
			}
			after.PlanID, label = plan.ID, plan.Code
		}
		switch {
		case in.NoEnd:
			after.PaidThrough = nil
		case in.PaidThrough != nil:
			d := *in.PaidThrough
			after.PaidThrough = &d
		}
		if in.GraceDays != nil {
			after.GraceDays = *in.GraceDays
		}
		if in.Status != nil {
			after.Status = *in.Status
		}
		if in.Limits.MaxProperties != nil {
			after.Overrides.MaxProperties = in.Limits.MaxProperties
		}
		if in.Limits.MaxRooms != nil {
			after.Overrides.MaxRooms = in.Limits.MaxRooms
		}
		if in.Limits.MaxUsers != nil {
			after.Overrides.MaxUsers = in.Limits.MaxUsers
		}
		for _, r := range in.ClearLimits {
			switch r {
			case entitlement.Properties:
				after.Overrides.MaxProperties = nil
			case entitlement.Rooms:
				after.Overrides.MaxRooms = nil
			case entitlement.Users:
				after.Overrides.MaxUsers = nil
			}
		}
		for _, v := range []*int{after.Overrides.MaxProperties, after.Overrides.MaxRooms, after.Overrides.MaxUsers} {
			if v != nil && *v < 1 {
				return apperr.Invalid("the subscription is invalid", apperr.FieldError{Field: "limits", Code: "INVALID_VALUE", Message: "a positive number, or the plan's"})
			}
		}
		if after.Status == StatusTrial && after.PaidThrough == nil {
			return apperr.Invalid("the subscription is invalid", apperr.FieldError{Field: "paid_through", Code: "REQUIRED", Message: "a trial ends on a date"})
		}
		updated, err := s.update(ctx, t.ID, after)
		if err != nil {
			return err
		}
		out = updated
		if label == "" {
			p, err := q.GetPlan(ctx, out.PlanID)
			if err != nil {
				return err
			}
			label = p.Code
		}
		action := "subscription.changed"
		if out.Status == StatusCancelled && before.Status != StatusCancelled {
			action = "subscription.cancelled"
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: t.ID, Action: action, EntityType: "subscription", EntityID: out.ID, EntityLabel: label, Old: before, New: out})
	})
	return out, err
}

func (s *Service) update(ctx context.Context, tenantID int64, sub Subscription) (Subscription, error) {
	row, err := s.q(ctx).UpdateSubscription(ctx, subscriptiondb.UpdateSubscriptionParams{
		TenantID: tenantID, PlanID: sub.PlanID, Status: sub.Status, PaidThrough: sub.PaidThrough,
		GraceDays:             int16(sub.GraceDays), //nolint:gosec // G115: validated to 0..90
		MaxPropertiesOverride: int4(sub.Overrides.MaxProperties), MaxRoomsOverride: int4(sub.Overrides.MaxRooms), MaxUsersOverride: int4(sub.Overrides.MaxUsers),
	})
	if err != nil {
		return Subscription{}, err
	}
	return subscriptionFrom(lockRowOf(row, "")), nil
}

// ---------------------------------------------------------------------------
// Invoices (operator, §6)

// IssueInput is the request to bill a tenant for its next period.
type IssueInput struct {
	TenantCode string
	// PlanCode is the plan billed (the current one when empty); paying the invoice moves the tenant to it.
	PlanCode string
	// Periods is the number of billing periods of the plan (1 when 0).
	Periods int
	// Amount replaces Periods × price (a discount, an agreed price).
	Amount *decimal.Decimal
	// DueOn defaults to the first day of the period, or today when that is earlier.
	DueOn *civil.Date
	Notes string
}

// IssueInvoice bills a tenant for the period after its paid period.
func (s *Service) IssueInvoice(ctx context.Context, in IssueInput) (Invoice, error) {
	if in.Periods == 0 {
		in.Periods = 1
	}
	if in.Periods < 1 || in.Periods > 36 {
		return Invoice{}, apperr.Invalid("the invoice is invalid", apperr.FieldError{Field: "periods", Code: "INVALID_VALUE", Message: "1 to 36"})
	}
	if in.Amount != nil && (in.Amount.IsNegative() || in.Amount.Exponent() < -3) {
		return Invoice{}, apperr.Invalid("the invoice is invalid", apperr.FieldError{Field: "amount", Code: "INVALID_VALUE", Message: "zero or more, at most 3 decimals"})
	}
	var out Invoice
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		t, err := q.GetTenantForSubscription(ctx, strings.ToUpper(strings.TrimSpace(in.TenantCode)))
		if err != nil {
			return notFound(err, errTenantNotFound())
		}
		if err := db.EnterLockLevel(ctx, db.LevelSubscription); err != nil {
			return err
		}
		row, err := q.LockSubscription(ctx, t.ID)
		if err != nil {
			return notFound(err, errNoSubscription())
		}
		sub := subscriptionFrom(row)
		var plan subscriptiondb.SubscriptionPlan
		if in.PlanCode == "" {
			plan, err = q.GetPlan(ctx, sub.PlanID)
		} else {
			plan, err = q.GetPlanByCode(ctx, strings.ToUpper(strings.TrimSpace(in.PlanCode)))
		}
		if err != nil {
			return notFound(err, errPlanNotFound())
		}
		if !plan.IsActive && plan.ID != sub.PlanID {
			return apperr.Conflict("PLAN_INACTIVE", "the plan is retired and cannot be given to a tenant")
		}
		today := s.today(row.Timezone)
		start, end := NextPeriod(sub, today, int(plan.BillingPeriodMonths), in.Periods)
		amount := plan.Price.Mul(decimal.NewFromInt(int64(in.Periods)))
		if in.Amount != nil {
			amount = *in.Amount
		}
		due := start
		if in.DueOn != nil {
			due = *in.DueOn
		} else if due.Before(today) {
			due = today
		}
		n, err := q.NextInvoiceNumber(ctx, int16(today.Year())) //nolint:gosec // G115: a calendar year fits
		if err != nil {
			return err
		}
		inv, err := q.InsertInvoice(ctx, subscriptiondb.InsertInvoiceParams{
			TenantID: t.ID, Number: InvoiceNumber(today.Year(), int(n)), PlanID: plan.ID, PeriodStart: start, PeriodEnd: end,
			Amount: amount, CurrencyCode: plan.CurrencyCode, IssuedAt: s.clock.Now(), DueOn: due, Notes: nullable(in.Notes),
		})
		if err != nil {
			return err
		}
		out = toInvoice(inv)
		out.PlanCode = plan.Code
		return s.audit.Write(ctx, audit.Entry{TenantID: t.ID, Action: "subscription_invoice.issued", EntityType: "subscription_invoice",
			EntityID: inv.ID, EntityLabel: inv.Number, New: out})
	})
	return out, err
}

// PayInvoice records the payment of an issued invoice and extends the subscription to the end of its
// period, on its plan (§6).
func (s *Service) PayInvoice(ctx context.Context, number string, paidOn civil.Date, reference string) (Invoice, Subscription, error) {
	if paidOn.IsZero() {
		return Invoice{}, Subscription{}, apperr.Invalid("the payment is invalid", apperr.FieldError{Field: "paid_on", Code: "REQUIRED"})
	}
	var inv Invoice
	var sub Subscription
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		target, before, err := s.lockInvoice(ctx, number)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		now := s.clock.Now()
		paid, err := q.MarkInvoicePaid(ctx, subscriptiondb.MarkInvoicePaidParams{ID: target.ID, PaidOn: &paidOn, PaidAt: &now, PaymentReference: nullable(reference)})
		if err != nil {
			return err
		}
		inv = toInvoice(paid)
		after := before
		after.PlanID = target.PlanID
		after.Status = StatusActive
		if after.PaidThrough != nil && after.PaidThrough.Before(target.PeriodEnd) {
			end := target.PeriodEnd
			after.PaidThrough = &end
		}
		if sub, err = s.update(ctx, target.TenantID, after); err != nil {
			return err
		}
		plan, err := q.GetPlan(ctx, sub.PlanID)
		if err != nil {
			return err
		}
		inv.PlanCode = plan.Code
		if err := s.audit.Write(ctx, audit.Entry{TenantID: target.TenantID, Action: "subscription_invoice.paid", EntityType: "subscription_invoice",
			EntityID: inv.ID, EntityLabel: inv.Number, Old: toInvoice(target), New: inv}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: target.TenantID, Action: "subscription.renewed", EntityType: "subscription",
			EntityID: sub.ID, EntityLabel: plan.Code, Old: before, New: sub})
	})
	return inv, sub, err
}

// VoidInvoice cancels an issued invoice.
func (s *Service) VoidInvoice(ctx context.Context, number, reason string) (Invoice, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Invoice{}, apperr.Invalid("the void is invalid", apperr.FieldError{Field: "reason", Code: "REQUIRED"})
	}
	var out Invoice
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		target, _, err := s.lockInvoice(ctx, number)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		row, err := s.q(ctx).MarkInvoiceVoid(ctx, subscriptiondb.MarkInvoiceVoidParams{ID: target.ID, VoidedAt: &now, VoidReason: &reason})
		if err != nil {
			return err
		}
		out = toInvoice(row)
		return s.audit.Write(ctx, audit.Entry{TenantID: target.TenantID, Action: "subscription_invoice.voided", EntityType: "subscription_invoice",
			EntityID: out.ID, EntityLabel: out.Number, Old: toInvoice(target), New: out})
	})
	return out, err
}

// lockInvoice locks the tenant's subscription and then the invoice, which must still be ISSUED.
func (s *Service) lockInvoice(ctx context.Context, number string) (subscriptiondb.SubscriptionInvoice, Subscription, error) {
	q := s.q(ctx)
	number = strings.ToUpper(strings.TrimSpace(number))
	tenantID, err := q.GetInvoiceTenant(ctx, number) // learn the tenant; the invoice is locked below, after the subscription
	if err != nil {
		return subscriptiondb.SubscriptionInvoice{}, Subscription{}, notFound(err, errInvoiceNotFound())
	}
	if err := db.EnterLockLevel(ctx, db.LevelSubscription); err != nil {
		return subscriptiondb.SubscriptionInvoice{}, Subscription{}, err
	}
	row, err := q.LockSubscription(ctx, tenantID)
	if err != nil {
		return subscriptiondb.SubscriptionInvoice{}, Subscription{}, notFound(err, errNoSubscription())
	}
	inv, err := q.GetInvoiceForUpdate(ctx, number)
	if err != nil {
		return subscriptiondb.SubscriptionInvoice{}, Subscription{}, err
	}
	if inv.Status != InvoiceIssued {
		return subscriptiondb.SubscriptionInvoice{}, Subscription{}, apperr.Conflict("INVOICE_NOT_OPEN", "the invoice is already "+strings.ToLower(inv.Status)).WithContext("status", inv.Status)
	}
	return inv, subscriptionFrom(row), nil
}

// TenantInvoices lists a tenant's invoices (operator).
func (s *Service) TenantInvoices(ctx context.Context, tenantCode string) ([]Invoice, error) {
	t, err := s.q(ctx).GetTenantForSubscription(ctx, strings.ToUpper(strings.TrimSpace(tenantCode)))
	if err != nil {
		return nil, notFound(err, errTenantNotFound())
	}
	return s.invoices(ctx, t.ID)
}

// ---------------------------------------------------------------------------
// Mapping

func toPlan(r subscriptiondb.SubscriptionPlan) Plan {
	return Plan{
		ID: r.ID, Code: r.Code, Name: r.Name, Modules: toModules(r.Modules),
		Limits: Limits{MaxProperties: fromInt4(r.MaxProperties), MaxRooms: fromInt4(r.MaxRooms), MaxUsers: fromInt4(r.MaxUsers)},
		Price:  r.Price, CurrencyCode: r.CurrencyCode, BillingPeriodMonths: int(r.BillingPeriodMonths), TrialDays: int(r.TrialDays),
		IsActive: r.IsActive, IsDefault: r.IsDefault,
	}
}

func subscriptionFrom(r subscriptiondb.LockSubscriptionRow) Subscription {
	return Subscription{
		ID: r.ID, TenantID: r.TenantID, PlanID: r.PlanID, Status: r.Status, StartedOn: r.StartedOn, PaidThrough: r.PaidThrough,
		GraceDays: int(r.GraceDays), Version: int(r.Version),
		Overrides: Limits{MaxProperties: fromInt4(r.MaxPropertiesOverride), MaxRooms: fromInt4(r.MaxRoomsOverride), MaxUsers: fromInt4(r.MaxUsersOverride)},
	}
}

func lockRowOf(r subscriptiondb.TenantSubscription, timezone string) subscriptiondb.LockSubscriptionRow {
	return subscriptiondb.LockSubscriptionRow{
		ID: r.ID, TenantID: r.TenantID, PlanID: r.PlanID, Status: r.Status, StartedOn: r.StartedOn, PaidThrough: r.PaidThrough,
		GraceDays: r.GraceDays, MaxPropertiesOverride: r.MaxPropertiesOverride, MaxRoomsOverride: r.MaxRoomsOverride,
		MaxUsersOverride: r.MaxUsersOverride, Version: r.Version, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, Timezone: timezone,
	}
}

func toInvoice(r subscriptiondb.SubscriptionInvoice) Invoice {
	return Invoice{
		ID: r.ID, TenantID: r.TenantID, Number: r.Number, PlanID: r.PlanID, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd,
		Amount: r.Amount, CurrencyCode: r.CurrencyCode, Status: r.Status, IssuedAt: r.IssuedAt, DueOn: r.DueOn, PaidOn: r.PaidOn,
		PaymentReference: deref(r.PaymentReference), VoidReason: deref(r.VoidReason), Notes: deref(r.Notes),
	}
}

func toModules(ss []string) []Module {
	out := make([]Module, 0, len(ss))
	for _, m := range ss {
		out = append(out, Module(m))
	}
	return out
}

func fromModules(ms []Module) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, string(m))
	}
	return out
}

func int4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true} //nolint:gosec // G115: limits are small positive numbers
}

func fromInt4(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func nullable(s string) *string {
	s = strings.TrimSpace(s)
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
