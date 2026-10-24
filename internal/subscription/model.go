// Package subscription owns the SaaS plans, the subscription of every tenant, its invoices, and the
// three gates that follow from them: the module of a route, the quota of properties, rooms and users,
// and the read-only state after the paid period and its grace.
//
// Design: docs/architecture/26-subscriptions.md.
package subscription

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/entitlement"
)

// Module is a group of API routes that a plan switches on or off.
type Module string

// The modules. CORE is always on and is never stored on a plan.
const (
	Core         Module = "CORE"
	Housekeeping Module = "HOUSEKEEPING"
	Accounting   Module = "ACCOUNTING"
	Inventory    Module = "INVENTORY" // the outlets (docs/architecture/34-pos.md)
	Banquet      Module = "BANQUET"   // the meeting rooms, events and banquets (docs/architecture/36-banquet.md)
	POS          Module = "POS"       // the outlets (docs/architecture/34-pos.md)
)

// Optional are the modules a plan may include (the same set as subscription_plans_modules_ck).
var Optional = []Module{Housekeeping, Accounting, Inventory, Banquet, POS}

// Subscription statuses.
const (
	StatusTrial     = "TRIAL"
	StatusActive    = "ACTIVE"
	StatusCancelled = "CANCELLED"
)

// AccessState is what a tenant may do now; it is derived, never stored.
type AccessState string

// The access states (§4).
const (
	AccessFull     AccessState = "FULL"
	AccessGrace    AccessState = "GRACE"
	AccessReadOnly AccessState = "READ_ONLY"
)

// Invoice statuses.
const (
	InvoiceIssued = "ISSUED"
	InvoicePaid   = "PAID"
	InvoiceVoid   = "VOID"
)

// Limits are the quotas of a plan or a subscription; nil is unlimited.
type Limits struct {
	MaxProperties *int `json:"max_properties"`
	MaxRooms      *int `json:"max_rooms"`
	MaxUsers      *int `json:"max_users"`
}

// Of returns the limit of one resource.
func (l Limits) Of(r entitlement.Resource) *int {
	switch r {
	case entitlement.Properties:
		return l.MaxProperties
	case entitlement.Rooms:
		return l.MaxRooms
	case entitlement.Users:
		return l.MaxUsers
	}
	return nil
}

// Over returns the override where it is set and the plan's limit otherwise.
func (l Limits) Over(plan Limits) Limits {
	pick := func(o, p *int) *int {
		if o != nil {
			return o
		}
		return p
	}
	return Limits{MaxProperties: pick(l.MaxProperties, plan.MaxProperties), MaxRooms: pick(l.MaxRooms, plan.MaxRooms), MaxUsers: pick(l.MaxUsers, plan.MaxUsers)}
}

// Usage is what a tenant holds of each limited resource.
type Usage struct {
	Properties int `json:"properties"`
	Rooms      int `json:"rooms"`
	Users      int `json:"users"`
}

// Of returns the usage of one resource.
func (u Usage) Of(r entitlement.Resource) int {
	switch r {
	case entitlement.Properties:
		return u.Properties
	case entitlement.Rooms:
		return u.Rooms
	case entitlement.Users:
		return u.Users
	}
	return 0
}

// Plan is a platform-wide offer.
type Plan struct {
	ID                  int64           `json:"id"`
	Code                string          `json:"code"`
	Name                string          `json:"name"`
	Modules             []Module        `json:"modules"`
	Limits              Limits          `json:"limits"`
	Price               decimal.Decimal `json:"price"`
	CurrencyCode        string          `json:"currency_code"`
	BillingPeriodMonths int             `json:"billing_period_months"`
	TrialDays           int             `json:"trial_days"`
	IsActive            bool            `json:"is_active"`
	IsDefault           bool            `json:"is_default"`
}

// Has says whether the plan includes a module (CORE always).
func (p Plan) Has(m Module) bool { return m == Core || slices.Contains(p.Modules, m) }

// Subscription is the one subscription of a tenant.
type Subscription struct {
	ID          int64       `json:"id"`
	TenantID    int64       `json:"-"`
	PlanID      int64       `json:"plan_id"`
	Status      string      `json:"status"`
	StartedOn   civil.Date  `json:"started_on"`
	PaidThrough *civil.Date `json:"paid_through"`
	GraceDays   int         `json:"grace_days"`
	Overrides   Limits      `json:"overrides"`
	Version     int         `json:"version"`
}

// Access derives the access state on a calendar day of the tenant (§4).
func (s Subscription) Access(today civil.Date) AccessState {
	if s.PaidThrough == nil || !today.After(*s.PaidThrough) {
		return AccessFull
	}
	if s.Status != StatusCancelled && !today.After(s.PaidThrough.AddDays(s.GraceDays)) {
		return AccessGrace
	}
	return AccessReadOnly
}

// GraceEndsOn is the last day of the grace period (nil without an end, or for a cancelled subscription).
func (s Subscription) GraceEndsOn() *civil.Date {
	if s.PaidThrough == nil || s.Status == StatusCancelled {
		return nil
	}
	d := s.PaidThrough.AddDays(s.GraceDays)
	return &d
}

// Invoice is one subscription invoice.
type Invoice struct {
	ID               int64           `json:"id"`
	TenantID         int64           `json:"-"`
	Number           string          `json:"number"`
	PlanID           int64           `json:"plan_id"`
	PlanCode         string          `json:"plan_code"`
	PeriodStart      civil.Date      `json:"period_start"`
	PeriodEnd        civil.Date      `json:"period_end"`
	Amount           decimal.Decimal `json:"amount"`
	CurrencyCode     string          `json:"currency_code"`
	Status           string          `json:"status"`
	IssuedAt         time.Time       `json:"issued_at"`
	DueOn            civil.Date      `json:"due_on"`
	PaidOn           *civil.Date     `json:"paid_on,omitempty"`
	PaymentReference string          `json:"payment_reference,omitempty"`
	VoidReason       string          `json:"void_reason,omitempty"`
	Notes            string          `json:"notes,omitempty"`
}

// Summary is what a tenant sees of its subscription (GET /api/v1/subscription).
type Summary struct {
	PlanCode    string      `json:"plan_code"`
	PlanName    string      `json:"plan_name"`
	Modules     []Module    `json:"modules"`
	Status      string      `json:"status"`
	Access      AccessState `json:"access"`
	StartedOn   civil.Date  `json:"started_on"`
	PaidThrough *civil.Date `json:"paid_through"`
	GraceEndsOn *civil.Date `json:"grace_ends_on"`
	// DaysLeft counts the days of writing left: to paid_through while FULL, to the end of the grace while GRACE; nil without an end, 0 when READ_ONLY.
	DaysLeft *int   `json:"days_left"`
	Limits   Limits `json:"limits"`
	Usage    Usage  `json:"usage"`
}

// daysLeft fills Summary.DaysLeft.
func daysLeft(s Subscription, today civil.Date) *int {
	var end *civil.Date
	switch s.Access(today) {
	case AccessFull:
		end = s.PaidThrough
	case AccessGrace:
		end = s.GraceEndsOn()
	case AccessReadOnly:
		n := 0
		return &n
	}
	if end == nil {
		return nil
	}
	n := today.DaysUntil(*end) + 1
	return &n
}

// ---------------------------------------------------------------------------
// Validation of plan input (operator)

var (
	planCode     = regexp.MustCompile(`^[A-Z0-9_-]{2,30}$`)
	currencyCode = regexp.MustCompile(`^[A-Z]{3}$`)
)

// PlanInput is the editable part of a plan.
type PlanInput struct {
	Code                string
	Name                string
	Modules             []Module
	Limits              Limits
	Price               decimal.Decimal
	CurrencyCode        string
	BillingPeriodMonths int
	TrialDays           int
	IsActive            bool
	IsDefault           bool
}

// Normalize upper-cases the codes and removes duplicate modules.
func (in *PlanInput) Normalize() {
	in.Code = strings.ToUpper(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	in.CurrencyCode = strings.ToUpper(strings.TrimSpace(in.CurrencyCode))
	ms := make([]Module, 0, len(in.Modules))
	for _, m := range in.Modules {
		m = Module(strings.ToUpper(strings.TrimSpace(string(m))))
		if m != "" && m != Core && !slices.Contains(ms, m) {
			ms = append(ms, m)
		}
	}
	slices.Sort(ms)
	in.Modules = ms
}

// Validate returns the field errors of the input.
func (in PlanInput) Validate() []apperr.FieldError {
	var f []apperr.FieldError
	if !planCode.MatchString(in.Code) {
		f = append(f, apperr.FieldError{Field: "code", Code: "INVALID_VALUE", Message: "2 to 30 of A-Z, 0-9, - or _"})
	}
	if in.Name == "" || len(in.Name) > 200 {
		f = append(f, apperr.FieldError{Field: "name", Code: "REQUIRED", Message: "1 to 200 characters"})
	}
	for _, m := range in.Modules {
		if !slices.Contains(Optional, m) {
			f = append(f, apperr.FieldError{Field: "modules", Code: "INVALID_VALUE", Message: fmt.Sprintf("unknown module %q", m)})
		}
	}
	for _, l := range []struct {
		field string
		v     *int
	}{{"max_properties", in.Limits.MaxProperties}, {"max_rooms", in.Limits.MaxRooms}, {"max_users", in.Limits.MaxUsers}} {
		if l.v != nil && *l.v < 1 {
			f = append(f, apperr.FieldError{Field: l.field, Code: "INVALID_VALUE", Message: "a positive number, or unlimited"})
		}
	}
	if in.Price.IsNegative() || in.Price.Exponent() < -3 {
		f = append(f, apperr.FieldError{Field: "price", Code: "INVALID_VALUE", Message: "zero or more, at most 3 decimals"})
	}
	if !currencyCode.MatchString(in.CurrencyCode) {
		f = append(f, apperr.FieldError{Field: "currency_code", Code: "INVALID_VALUE", Message: "an ISO 4217 code"})
	}
	if !slices.Contains([]int{1, 3, 6, 12}, in.BillingPeriodMonths) {
		f = append(f, apperr.FieldError{Field: "billing_period_months", Code: "INVALID_VALUE", Message: "1, 3, 6 or 12"})
	}
	if in.TrialDays < 0 || in.TrialDays > 365 {
		f = append(f, apperr.FieldError{Field: "trial_days", Code: "INVALID_VALUE", Message: "0 to 365"})
	}
	if in.IsDefault && !in.IsActive {
		f = append(f, apperr.FieldError{Field: "is_default", Code: "INVALID_VALUE", Message: "the default plan must be active"})
	}
	return f
}

// ---------------------------------------------------------------------------
// Invoice periods (§6)

// NextPeriod is the period an invoice of n billing periods covers: it starts the day after the paid
// period (or today, when that day is already past) and runs n × months, ending the day before the
// same day of the month that many months later.
func NextPeriod(s Subscription, today civil.Date, months, n int) (start, end civil.Date) {
	start = today
	if s.PaidThrough != nil && !s.PaidThrough.Before(today) {
		start = s.PaidThrough.AddDays(1)
	}
	return start, AddMonths(start, months*n).AddDays(-1)
}

// AddMonths adds whole months, clamping to the last day of a shorter month (31 Jan + 1 month = 28/29 Feb).
func AddMonths(d civil.Date, months int) civil.Date {
	y, m := d.Year(), int(d.Month())-1+months
	y += m / 12
	m %= 12
	if m < 0 {
		m += 12
		y--
	}
	last := civil.NewDate(y, time.Month(m+2), 0).Day() // day 0 of the next month = last day of this one
	return civil.NewDate(y, time.Month(m+1), min(d.Day(), last))
}

// InvoiceNumber formats a subscription invoice number.
func InvoiceNumber(year, n int) string { return fmt.Sprintf("SUB-%04d-%05d", year, n) }
