package operator

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/operator/operatordb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/entitlement"
	"kamarapms/internal/subscription"
	"kamarapms/internal/tenancy"
)

// Console is what an operator does to the hotels (doc 39 section 7): it reads across tenants and calls the services of the subscription, the tenancy and the users, which already hold the rules and the
// locks. Every write runs with the operator in the context, so the audit writer records it twice (the operator's trail and the tenant's, decision P12).
type Console struct {
	txm   *db.TxManager
	clock clock.Clock
	audit *audit.Writer
	subs  *subscription.Service
	ten   *tenancy.Service
	users *iam.Service
}

// NewConsole wires the console.
func NewConsole(txm *db.TxManager, c clock.Clock, a *audit.Writer, subs *subscription.Service, ten *tenancy.Service, users *iam.Service) *Console {
	return &Console{txm: txm, clock: c, audit: a, subs: subs, ten: ten, users: users}
}

func (c *Console) q(ctx context.Context) *operatordb.Queries { return operatordb.New(c.txm.DB(ctx)) }

func errTenantNotFound() *apperr.Error {
	return apperr.NotFound("TENANT_NOT_FOUND", "no tenant with this code")
}

func errConfirmation() *apperr.Error {
	return apperr.New(apperr.KindInvalid, "CONFIRMATION_MISMATCH", "the confirmation does not match the tenant code")
}

func fieldError(field, code, msg string) *apperr.Error {
	return apperr.Invalid("the request is invalid", apperr.FieldError{Field: field, Code: code, Message: msg})
}

func limitOf(v pgtype.Int4) *int {
	if !v.Valid {
		return nil
	}
	n := int(v.Int32)
	return &n
}

func today(now time.Time, timezone string) civil.Date {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC // unreachable: a tenant's zone is validated when it is written
	}
	return civil.DateOf(now.In(loc))
}

// ---------------------------------------------------------------------------
// Tenants

// The states a tenant is listed under (the filter `status` of the list).
const (
	StateActive    = "active"
	StateTrial     = "trial"
	StateGrace     = "grace"
	StateReadOnly  = "read_only"
	StateSuspended = "suspended"
)

var tenantStates = []string{StateActive, StateTrial, StateGrace, StateReadOnly, StateSuspended}

// TrialEndingDays is how close the end of a trial must be to be listed first.
const TrialEndingDays = 14

// TenantRow is one line of the list of tenants.
type TenantRow struct {
	Code                string                   `json:"code"`
	Name                string                   `json:"name"`
	Timezone            string                   `json:"timezone"`
	State               string                   `json:"state"`
	TenantStatus        string                   `json:"tenant_status"`
	StatusReason        *string                  `json:"status_reason"`
	PlanCode            string                   `json:"plan_code"`
	PlanName            string                   `json:"plan_name"`
	SubscriptionStatus  string                   `json:"subscription_status"`
	Access              subscription.AccessState `json:"access"`
	PaidThrough         *civil.Date              `json:"paid_through"`
	DaysLeft            *int                     `json:"days_left"`
	TrialEnding         bool                     `json:"trial_ending"`
	Usage               subscription.Usage       `json:"usage"`
	Limits              subscription.Limits      `json:"limits"`
	OpenInvoiceCount    int                      `json:"open_invoice_count"`
	OverdueInvoiceCount int                      `json:"overdue_invoice_count"`
	CreatedAt           time.Time                `json:"created_at"`
}

// CurrencyTotal is an amount owed in one currency.
type CurrencyTotal struct {
	CurrencyCode string          `json:"currency_code"`
	InvoiceCount int             `json:"invoice_count"`
	Total        decimal.Decimal `json:"total"`
}

// TenantSummary counts the tenants by state and the invoices that are open (a total per currency: plans may be billed in different currencies).
type TenantSummary struct {
	Active              int             `json:"active"`
	Trial               int             `json:"trial"`
	TrialEnding         int             `json:"trial_ending"`
	Grace               int             `json:"grace"`
	ReadOnly            int             `json:"read_only"`
	Suspended           int             `json:"suspended"`
	OpenInvoiceCount    int             `json:"open_invoice_count"`
	OpenInvoiceTotals   []CurrencyTotal `json:"open_invoice_totals"`
	OverdueInvoiceCount int             `json:"overdue_invoice_count"`
}

// TenantList is the answer of the list.
type TenantList struct {
	Items   []TenantRow   `json:"items"`
	Summary TenantSummary `json:"summary"`
}

// TenantFilter selects the tenants of the list.
type TenantFilter struct {
	Q, Status, Plan string
}

// ListTenants answers the tenants, the ones that need attention first. The summary counts every tenant that the text and the plan select, whatever the status filter says, so the tabs keep their numbers.
func (c *Console) ListTenants(ctx context.Context, f TenantFilter) (TenantList, error) {
	f.Q, f.Plan, f.Status = strings.TrimSpace(f.Q), strings.ToUpper(strings.TrimSpace(f.Plan)), strings.ToLower(strings.TrimSpace(f.Status))
	if f.Status != "" && !slices.Contains(tenantStates, f.Status) {
		return TenantList{}, fieldError("status", "INVALID_VALUE", "one of "+strings.Join(tenantStates, ", "))
	}
	now := c.clock.Now()
	q := c.q(ctx)
	rows, err := q.ConsoleListTenants(ctx, operatordb.ConsoleListTenantsParams{Now: now, Q: f.Q, Plan: f.Plan})
	if err != nil {
		return TenantList{}, err
	}
	out := TenantList{Items: []TenantRow{}}
	for _, r := range rows {
		row := tenantRow(r, now)
		switch row.State {
		case StateActive:
			out.Summary.Active++
		case StateTrial:
			out.Summary.Trial++
		case StateGrace:
			out.Summary.Grace++
		case StateReadOnly:
			out.Summary.ReadOnly++
		case StateSuspended:
			out.Summary.Suspended++
		}
		if row.TrialEnding {
			out.Summary.TrialEnding++
		}
		out.Summary.OpenInvoiceCount += row.OpenInvoiceCount
		out.Summary.OverdueInvoiceCount += row.OverdueInvoiceCount
		if f.Status == "" || row.State == f.Status {
			out.Items = append(out.Items, row)
		}
	}
	slices.SortStableFunc(out.Items, func(a, b TenantRow) int {
		if d := attentionRank(a) - attentionRank(b); d != 0 {
			return d
		}
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	totals, err := q.ConsoleOpenInvoiceTotals(ctx)
	if err != nil {
		return TenantList{}, err
	}
	out.Summary.OpenInvoiceTotals = []CurrencyTotal{}
	for _, t := range totals {
		out.Summary.OpenInvoiceTotals = append(out.Summary.OpenInvoiceTotals, CurrencyTotal{CurrencyCode: t.CurrencyCode, InvoiceCount: int(t.InvoiceCount), Total: t.Total})
	}
	return out, nil
}

// attentionRank orders the list: read-only, grace, trials about to end, the rest.
func attentionRank(r TenantRow) int {
	switch {
	case r.State == StateReadOnly:
		return 0
	case r.State == StateGrace:
		return 1
	case r.TrialEnding:
		return 2
	}
	return 3
}

func tenantRow(r operatordb.ConsoleListTenantsRow, now time.Time) TenantRow {
	day := today(now, r.Timezone)
	sub := subscription.Subscription{Status: r.SubStatus, PaidThrough: r.PaidThrough, GraceDays: int(r.GraceDays), StartedOn: r.StartedOn}
	access := sub.Access(day)
	left := sub.DaysLeft(day)
	state := StateActive
	switch {
	case r.TenantStatus != "ACTIVE":
		state = StateSuspended
	case access == subscription.AccessReadOnly:
		state = StateReadOnly
	case access == subscription.AccessGrace:
		state = StateGrace
	case r.SubStatus == subscription.StatusTrial:
		state = StateTrial
	}
	plan := subscription.Limits{MaxProperties: limitOf(r.PlanMaxProperties), MaxRooms: limitOf(r.PlanMaxRooms), MaxUsers: limitOf(r.PlanMaxUsers)}
	over := subscription.Limits{MaxProperties: limitOf(r.MaxPropertiesOverride), MaxRooms: limitOf(r.MaxRoomsOverride), MaxUsers: limitOf(r.MaxUsersOverride)}
	return TenantRow{
		Code: r.Code, Name: r.Name, Timezone: r.Timezone, State: state, TenantStatus: r.TenantStatus, StatusReason: r.StatusReason,
		PlanCode: r.PlanCode, PlanName: r.PlanName, SubscriptionStatus: r.SubStatus, Access: access, PaidThrough: r.PaidThrough, DaysLeft: left,
		TrialEnding: state == StateTrial && left != nil && *left <= TrialEndingDays,
		Usage:       subscription.Usage{Properties: int(r.Properties), Rooms: int(r.Rooms), Users: int(r.Users)},
		Limits:      over.Over(plan), OpenInvoiceCount: int(r.OpenInvoiceCount), OverdueInvoiceCount: int(r.OverdueInvoiceCount), CreatedAt: r.CreatedAt,
	}
}

// NewTenantInput is the request to open a hotel.
type NewTenantInput struct {
	Code     string
	Name     string
	Timezone string
	Admin    *NewAdminInput
}

// NewAdminInput is the first administrator of a new tenant.
type NewAdminInput struct {
	Email    string
	FullName string
}

// NewTenantResult is what the creation answers. AdminPassword is shown here once and never again.
type NewTenantResult struct {
	Tenant        tenancy.Tenant `json:"tenant"`
	Admin         *iam.User      `json:"admin,omitempty"`
	AdminPassword string         `json:"admin_password,omitempty"`
}

// CreateTenant opens a tenant (it starts on the default plan through the hook of the tenancy service) and, when asked, its first administrator, in one transaction.
func (c *Console) CreateTenant(ctx context.Context, in NewTenantInput) (NewTenantResult, error) {
	var out NewTenantResult
	password := ""
	if in.Admin != nil {
		p, err := GeneratePassword()
		if err != nil {
			return out, err
		}
		password = p
	}
	err := c.txm.WithinTx(ctx, func(ctx context.Context) error {
		t, err := c.ten.CreateTenant(ctx, in.Code, strings.TrimSpace(in.Name), strings.TrimSpace(in.Timezone))
		if err != nil {
			return err
		}
		out.Tenant = t
		if in.Admin == nil {
			return nil
		}
		u, err := c.users.BootstrapAdmin(ctx, t.Code, in.Admin.Email, in.Admin.FullName, password)
		if err != nil {
			return err
		}
		out.Admin = &u
		return nil
	})
	if err != nil {
		return NewTenantResult{}, err
	}
	out.AdminPassword = password
	return out, nil
}

// TenantDetail is the page of one tenant.
type TenantDetail struct {
	Code            string                    `json:"code"`
	Name            string                    `json:"name"`
	Timezone        string                    `json:"timezone"`
	CreatedAt       time.Time                 `json:"created_at"`
	Status          string                    `json:"status"`
	StatusReason    *string                   `json:"status_reason"`
	StatusChangedAt *time.Time                `json:"status_changed_at"`
	Subscription    subscription.Subscription `json:"subscription"`
	Summary         subscription.Summary      `json:"summary"`
	PropertyCount   int                       `json:"property_count"`
}

// Tenant answers one tenant with its subscription, the limits in force and the usage.
func (c *Console) Tenant(ctx context.Context, code string) (TenantDetail, error) {
	t, err := c.tenantRow(ctx, code)
	if err != nil {
		return TenantDetail{}, err
	}
	v, err := c.subs.Show(ctx, t.Code)
	if err != nil {
		return TenantDetail{}, err
	}
	return TenantDetail{Code: t.Code, Name: t.Name, Timezone: t.Timezone, CreatedAt: t.CreatedAt, Status: t.Status, StatusReason: t.StatusReason, StatusChangedAt: t.StatusChangedAt,
		Subscription: v.Subscription, Summary: v.Summary, PropertyCount: v.Summary.Usage.Properties}, nil
}

func (c *Console) tenantRow(ctx context.Context, code string) (operatordb.ConsoleGetTenantRow, error) {
	t, err := c.q(ctx).ConsoleGetTenant(ctx, strings.ToUpper(strings.TrimSpace(code)))
	if errors.Is(err, pgx.ErrNoRows) {
		return t, errTenantNotFound()
	}
	return t, err
}

// TenantAdmin is an administrator of a tenant: nothing else of the user is shown.
type TenantAdmin struct {
	FullName    string     `json:"full_name"`
	Email       string     `json:"email"`
	IsActive    bool       `json:"is_active"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

// TenantAdmins lists the administrators of a tenant.
func (c *Console) TenantAdmins(ctx context.Context, code string) ([]TenantAdmin, error) {
	t, err := c.tenantRow(ctx, code)
	if err != nil {
		return nil, err
	}
	rows, err := c.q(ctx).ConsoleTenantAdmins(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	out := make([]TenantAdmin, 0, len(rows))
	for _, r := range rows {
		out = append(out, TenantAdmin{FullName: r.FullName, Email: r.Email, IsActive: r.IsActive, LastLoginAt: r.LastLoginAt})
	}
	return out, nil
}

// Actor kinds of an audit entry.
const (
	ActorUser     = "user"
	ActorOperator = "operator"
	ActorSystem   = "system"
)

// ActivityEntry is a line of what happened to a tenant's subscription.
type ActivityEntry struct {
	ID         int64     `json:"id"`
	Action     string    `json:"action"`
	EntityType string    `json:"entity_type"`
	EntityID   int64     `json:"entity_id"`
	At         time.Time `json:"at"`
	ActorKind  string    `json:"actor_kind"`
	ActorName  string    `json:"actor_name,omitempty"`
}

// TenantActivity lists the latest changes of the subscription, the invoices and the tenant, newest first.
func (c *Console) TenantActivity(ctx context.Context, code string, limit int) ([]ActivityEntry, error) {
	if limit < 1 || limit > 200 {
		return nil, fieldError("limit", "INVALID_VALUE", "1 to 200")
	}
	t, err := c.tenantRow(ctx, code)
	if err != nil {
		return nil, err
	}
	rows, err := c.q(ctx).ConsoleTenantActivity(ctx, operatordb.ConsoleTenantActivityParams{TenantID: t.ID, Lim: int32(limit)}) //nolint:gosec // G115: 1..200
	if err != nil {
		return nil, err
	}
	out := make([]ActivityEntry, 0, len(rows))
	for _, r := range rows {
		e := ActivityEntry{ID: r.ID, Action: r.Action, EntityType: r.EntityType, EntityID: r.EntityID, At: r.CreatedAt, ActorKind: ActorSystem}
		switch {
		case r.OperatorID != nil && r.OperatorName != nil:
			e.ActorKind, e.ActorName = ActorOperator, *r.OperatorName
		case r.UserID != nil && r.UserName != nil:
			e.ActorKind, e.ActorName = ActorUser, *r.UserName
		}
		out = append(out, e)
	}
	return out, nil
}

// ChangeSubscription applies an operator's change of a tenant's subscription. The audit entry is the service's; the operator comes from the context.
func (c *Console) ChangeSubscription(ctx context.Context, code string, in subscription.ChangeInput) (subscription.Subscription, error) {
	return c.subs.Change(ctx, code, in)
}

// SetSuspended suspends a tenant, or lifts the suspension. A suspension needs the reason and the tenant's code typed again; both lock the subscription row (the same one the quota
// uses), refuse the tenant's sign-in from then on, and end its sessions with ACCOUNT_DISABLED (decision P13).
func (c *Console) SetSuspended(ctx context.Context, code, reason, confirm string, suspend bool) (TenantDetail, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	reason = strings.TrimSpace(reason)
	if n := len([]rune(reason)); n < 1 || n > 500 {
		return TenantDetail{}, fieldError("reason", "INVALID_VALUE", "1 to 500 characters")
	}
	if suspend && strings.ToUpper(strings.TrimSpace(confirm)) != code {
		return TenantDetail{}, errConfirmation()
	}
	err := c.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := c.q(ctx)
		t, err := q.ConsoleGetTenant(ctx, code)
		if errors.Is(err, pgx.ErrNoRows) {
			return errTenantNotFound()
		}
		if err != nil {
			return err
		}
		if err := db.EnterLockLevel(ctx, db.LevelSubscription); err != nil {
			return err
		}
		if _, err := q.ConsoleLockTenantSubscription(ctx, t.ID); err != nil {
			return err
		}
		want, action := "ACTIVE", "tenant.reactivated"
		if suspend {
			want, action = "SUSPENDED", "tenant.suspended"
		}
		if t.Status == want {
			if suspend {
				return apperr.Conflict("TENANT_ALREADY_SUSPENDED", "the tenant is already suspended")
			}
			return apperr.Conflict("TENANT_NOT_SUSPENDED", "the tenant is not suspended")
		}
		now := c.clock.Now()
		why := "" // the reason of a suspension is shown while it lasts; the reason of the reactivation is in the audit trail
		if suspend {
			why = reason
		}
		if _, err := q.ConsoleSetTenantStatus(ctx, operatordb.ConsoleSetTenantStatusParams{ID: t.ID, Status: want, Reason: why, At: now}); err != nil {
			return err
		}
		if suspend {
			if _, err := q.ConsoleRevokeTenantSessions(ctx, operatordb.ConsoleRevokeTenantSessionsParams{TenantID: t.ID, At: now}); err != nil {
				return err
			}
		}
		type state struct {
			Status string `json:"status"`
			Reason string `json:"reason,omitempty"`
		}
		return c.audit.Write(ctx, audit.Entry{TenantID: t.ID, Action: action, EntityType: "tenant", EntityID: t.ID, EntityLabel: t.Code,
			Old: state{Status: t.Status}, New: state{Status: want, Reason: reason}})
	})
	if err != nil {
		return TenantDetail{}, err
	}
	return c.Tenant(ctx, code)
}

// ---------------------------------------------------------------------------
// Plans

// PlanRow is a plan with the number of tenants on it.
type PlanRow struct {
	subscription.Plan
	TenantCount int `json:"tenant_count"`
}

// Plans lists the plans with their tenants.
func (c *Console) Plans(ctx context.Context) ([]PlanRow, error) {
	plans, err := c.subs.ListPlans(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := c.q(ctx).ConsolePlanTenantCounts(ctx)
	if err != nil {
		return nil, err
	}
	byPlan := map[int64]int{}
	for _, n := range counts {
		byPlan[n.PlanID] = int(n.Tenants)
	}
	out := make([]PlanRow, 0, len(plans))
	for _, p := range plans {
		out = append(out, PlanRow{Plan: p, TenantCount: byPlan[p.ID]})
	}
	return out, nil
}

// Affected is a tenant that a plan's limit now puts over it: the warning of decision P8.
type Affected struct {
	TenantCode string `json:"tenant_code"`
	Resource   string `json:"resource"`
	Used       int    `json:"used"`
	Limit      int    `json:"limit"`
}

// PlanResult is the answer of a change of a plan.
type PlanResult struct {
	Plan     subscription.Plan `json:"plan"`
	Affected []Affected        `json:"affected"`
}

// CreatePlan adds a plan; the operator's trail records it (a plan belongs to no tenant).
func (c *Console) CreatePlan(ctx context.Context, in subscription.PlanInput) (PlanResult, error) {
	var out PlanResult
	err := c.txm.WithinTx(ctx, func(ctx context.Context) error {
		p, err := c.subs.CreatePlan(ctx, in)
		if err != nil {
			return err
		}
		out.Plan = p
		return c.auditPlan(ctx, "plan.created", p, nil)
	})
	out.Affected = []Affected{}
	return out, err
}

// UpdatePlan replaces the editable part of a plan and says which tenants on it are now over a limit (the plan's, where the tenant has no override of its own).
func (c *Console) UpdatePlan(ctx context.Context, in subscription.PlanInput) (PlanResult, error) {
	out := PlanResult{Affected: []Affected{}}
	err := c.txm.WithinTx(ctx, func(ctx context.Context) error {
		before, err := c.subs.PlanByCode(ctx, in.Code)
		if err != nil {
			return err
		}
		p, err := c.subs.UpdatePlan(ctx, in)
		if err != nil {
			return err
		}
		out.Plan = p
		rows, err := c.q(ctx).ConsolePlanTenants(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			for _, l := range []struct {
				res      entitlement.Resource
				limit    *int
				override pgtype.Int4
				used     int32
			}{
				{entitlement.Properties, p.Limits.MaxProperties, r.MaxPropertiesOverride, r.Properties},
				{entitlement.Rooms, p.Limits.MaxRooms, r.MaxRoomsOverride, r.Rooms},
				{entitlement.Users, p.Limits.MaxUsers, r.MaxUsersOverride, r.Users},
			} {
				if l.override.Valid || l.limit == nil || int(l.used) <= *l.limit {
					continue
				}
				out.Affected = append(out.Affected, Affected{TenantCode: r.Code, Resource: string(l.res), Used: int(l.used), Limit: *l.limit})
			}
		}
		return c.auditPlan(ctx, "plan.updated", p, &before)
	})
	return out, err
}

func (c *Console) auditPlan(ctx context.Context, action string, p subscription.Plan, before *subscription.Plan) error {
	op, ok := auth.OperatorFrom(ctx)
	if !ok {
		return nil
	}
	e := audit.OperatorEntry{OperatorID: &op.OperatorID, Action: action, EntityType: "subscription_plan", EntityID: p.ID, EntityLabel: p.Code, New: p}
	if before != nil {
		e.Old = *before
	}
	return c.audit.WriteOperator(ctx, e)
}

// ---------------------------------------------------------------------------
// Invoices

// InvoiceRow is an invoice with its tenant and how long it has been overdue.
type InvoiceRow struct {
	subscription.Invoice
	TenantCode  string `json:"tenant_code"`
	TenantName  string `json:"tenant_name"`
	OverdueDays int    `json:"overdue_days"`
}

// InvoiceList is the answer of the list of invoices.
type InvoiceList struct {
	Items     []InvoiceRow    `json:"items"`
	TotalOpen []CurrencyTotal `json:"total_open"`
}

var invoiceStates = []string{"open", "overdue", "paid", "void"}

// ListInvoices answers every tenant's invoices, newest first, at most 500.
func (c *Console) ListInvoices(ctx context.Context, status, tenant string) (InvoiceList, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "" && !slices.Contains(invoiceStates, status) {
		return InvoiceList{}, fieldError("status", "INVALID_VALUE", "one of "+strings.Join(invoiceStates, ", "))
	}
	q := c.q(ctx)
	rows, err := q.ConsoleListInvoices(ctx, operatordb.ConsoleListInvoicesParams{Now: c.clock.Now(), Tenant: strings.ToUpper(strings.TrimSpace(tenant)), Status: status, Lim: 500})
	if err != nil {
		return InvoiceList{}, err
	}
	out := InvoiceList{Items: make([]InvoiceRow, 0, len(rows)), TotalOpen: []CurrencyTotal{}}
	for _, r := range rows {
		inv := subscription.Invoice{Number: r.Number, PlanID: r.PlanID, PlanCode: r.PlanCode, PeriodStart: r.PeriodStart, PeriodEnd: r.PeriodEnd, Amount: r.Amount,
			CurrencyCode: r.CurrencyCode, Status: r.Status, IssuedAt: r.IssuedAt, DueOn: r.DueOn, PaidOn: r.PaidOn}
		if r.PaymentReference != nil {
			inv.PaymentReference = *r.PaymentReference
		}
		if r.VoidReason != nil {
			inv.VoidReason = *r.VoidReason
		}
		if r.Notes != nil {
			inv.Notes = *r.Notes
		}
		out.Items = append(out.Items, InvoiceRow{Invoice: inv, TenantCode: r.TenantCode, TenantName: r.TenantName, OverdueDays: int(r.OverdueDays)})
	}
	totals, err := q.ConsoleOpenInvoiceTotals(ctx)
	if err != nil {
		return InvoiceList{}, err
	}
	for _, t := range totals {
		out.TotalOpen = append(out.TotalOpen, CurrencyTotal{CurrencyCode: t.CurrencyCode, InvoiceCount: int(t.InvoiceCount), Total: t.Total})
	}
	return out, nil
}

// TenantInvoices lists one tenant's invoices.
func (c *Console) TenantInvoices(ctx context.Context, code string) ([]subscription.Invoice, error) {
	return c.subs.TenantInvoices(ctx, code)
}

// OpenInvoice names an invoice that is still waiting for its payment.
type OpenInvoice struct {
	Number      string     `json:"number"`
	PeriodStart civil.Date `json:"period_start"`
	PeriodEnd   civil.Date `json:"period_end"`
}

// InvoicePreview is what "Issue invoice" shows before it saves. It reads only: the number is a hint, it is not reserved.
type InvoicePreview struct {
	PlanCode     string          `json:"plan_code"`
	PeriodStart  civil.Date      `json:"period_start"`
	PeriodEnd    civil.Date      `json:"period_end"`
	Amount       decimal.Decimal `json:"amount"`
	CurrencyCode string          `json:"currency_code"`
	DueOn        civil.Date      `json:"due_on"`
	NextNumber   string          `json:"next_number"`
	OpenInvoice  *OpenInvoice    `json:"open_invoice,omitempty"`
}

// PreviewInvoice computes the period, the amount and the warning that IssueInvoice would give, without writing anything.
func (c *Console) PreviewInvoice(ctx context.Context, code, planCode string, periods int) (InvoicePreview, error) {
	if periods == 0 {
		periods = 1
	}
	if periods < 1 || periods > 36 {
		return InvoicePreview{}, fieldError("periods", "INVALID_VALUE", "1 to 36")
	}
	t, err := c.tenantRow(ctx, code)
	if err != nil {
		return InvoicePreview{}, err
	}
	v, err := c.subs.Show(ctx, t.Code)
	if err != nil {
		return InvoicePreview{}, err
	}
	if strings.TrimSpace(planCode) == "" {
		planCode = v.Summary.PlanCode
	}
	plan, err := c.subs.PlanByCode(ctx, planCode)
	if err != nil {
		return InvoicePreview{}, err
	}
	day := today(c.clock.Now(), t.Timezone)
	start, end := subscription.NextPeriod(v.Subscription, day, plan.BillingPeriodMonths, periods)
	due := start
	if due.Before(day) {
		due = day
	}
	n, err := c.q(ctx).ConsoleNextInvoiceNumber(ctx, int16(day.Year())) //nolint:gosec // G115: a calendar year fits
	if err != nil {
		return InvoicePreview{}, err
	}
	out := InvoicePreview{PlanCode: plan.Code, PeriodStart: start, PeriodEnd: end, Amount: plan.Price.Mul(decimal.NewFromInt(int64(periods))),
		CurrencyCode: plan.CurrencyCode, DueOn: due, NextNumber: subscription.InvoiceNumber(day.Year(), int(n))}
	invs, err := c.subs.TenantInvoices(ctx, t.Code)
	if err != nil {
		return InvoicePreview{}, err
	}
	for _, i := range invs {
		if i.Status == subscription.InvoiceIssued {
			out.OpenInvoice = &OpenInvoice{Number: i.Number, PeriodStart: i.PeriodStart, PeriodEnd: i.PeriodEnd}
			break
		}
	}
	return out, nil
}

// IssueInvoice bills a tenant.
func (c *Console) IssueInvoice(ctx context.Context, in subscription.IssueInput) (subscription.Invoice, error) {
	return c.subs.IssueInvoice(ctx, in)
}

// PayResult is the answer of a payment: the invoice, and the subscription it extended.
type PayResult struct {
	Invoice      subscription.Invoice      `json:"invoice"`
	Subscription subscription.Subscription `json:"subscription"`
}

// PayInvoice records a payment.
func (c *Console) PayInvoice(ctx context.Context, number string, paidOn civil.Date, reference string) (PayResult, error) {
	inv, sub, err := c.subs.PayInvoice(ctx, number, paidOn, reference)
	return PayResult{Invoice: inv, Subscription: sub}, err
}

// VoidInvoice cancels an invoice.
func (c *Console) VoidInvoice(ctx context.Context, number, reason string) (subscription.Invoice, error) {
	return c.subs.VoidInvoice(ctx, number, reason)
}
