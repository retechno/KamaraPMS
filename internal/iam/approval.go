package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/iam/iamdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
)

// Approval is a verified approval: the user who entered valid credentials and holds the permission of the kind at the
// property, or the person who acts approving their own when the rule of the property allows it. Only Verify can create one
// (the fields are unexported), so a posting service that requires an Approval knows a password was checked and the rule
// was applied.
type Approval struct {
	userID    int64
	selfAdmin bool
}

// SelfApprovedKey is the field of an audit entry that marks the approval of a tenant administrator of their own correction
// against the rule of the property.
const SelfApprovedKey = "self_approved_by_admin"

// UserID is the approver, to be recorded as approved_by.
func (a Approval) UserID() int64 { return a.userID }

// IsZero reports whether the value carries no approval.
func (a Approval) IsZero() bool { return a.userID == 0 }

// SelfApprovedByAdmin reports that the rule of the property would have refused this approval and that a tenant
// administrator approved their own correction by the exception of the rule (docs/architecture/32-distinct-approver.md 4).
func (a Approval) SelfApprovedByAdmin() bool { return a.selfAdmin }

// AuditFields adds what an audit entry says about the approval to its data: the approver (approved_by) and, when it was an
// administrator approving their own against the rule, self_approved_by_admin. It is the one way these fields are written.
func (a Approval) AuditFields(m map[string]any) map[string]any {
	m["approved_by"] = a.userID
	return a.Marker(m)
}

// Marker adds only the marker, for an entry that names the approver under another key (override_approved_by,
// rate_approved_by, restriction_override).
func (a Approval) Marker(m map[string]any) map[string]any {
	if a.selfAdmin {
		m[SelfApprovedKey] = true
	}
	return m
}

// ApprovalInput is the `approval` block of a correction request: the approver's own credentials.
type ApprovalInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// ApprovalRequest is what a use case asks of Verify: the kind of approval, its amount (the total of the correction in one
// request, defined once in docs/architecture/32-distinct-approver.md section 2; ignored by a kind without an amount) and
// the credentials that came with the request.
type ApprovalRequest struct {
	PropertyID int64
	Input      *ApprovalInput
	Kind       ApprovalKind
	Amount     decimal.Decimal
	// Permission replaces the permission of the kind for a use case that asks another one of its approver (a lower rate
	// needs reservation.override_rate_approve although its kind is a stay rate).
	Permission auth.Permission
	// Own is set by a use case whose actor holds the approver permission themselves: they approve their own without a
	// password, unless the rule of the property asks for someone else (then the credentials of someone else are needed).
	Own bool
}

var errApprovalInvalid = apperr.Unauthorized("APPROVAL_INVALID_CREDENTIALS", "the approver's email or password is incorrect")

// Verify checks an approval for a correction at a property (docs/architecture/06-api.md 14.1 and
// docs/architecture/32-distinct-approver.md 4). The password is verified with the login verifier and counts toward the
// login rate limits; it is never stored, logged or echoed, and no session or token is created.
//
// A missing block is 422 APPROVAL_REQUIRED. Wrong email, wrong password or an inactive user is one generic 401
// APPROVAL_INVALID_CREDENTIALS. A valid approver without the permission of the kind at the property is 403
// APPROVAL_NOT_PERMITTED. When the rule of the property for the kind and the amount asks for someone other than the actor
// and the approver is the actor: 403 APPROVAL_SAME_USER (the credentials were right, so it is not a failed attempt),
// except for a tenant administrator, whose approval is accepted and marked (Approval.AuditFields). Tenant administrators
// hold every permission.
func (s *Service) Verify(ctx context.Context, req ApprovalRequest) (Approval, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Approval{}, err
	}
	info, ok := KindOf(req.Kind)
	if !ok {
		return Approval{}, fmt.Errorf("iam: %q is not a kind of approval", req.Kind)
	}
	perm := info.Permission
	if req.Permission != "" {
		perm = req.Permission
	}
	in, propertyID := req.Input, req.PropertyID
	amount := decimal.Zero
	if info.HasAmount {
		amount = req.Amount.Abs()
	}
	rule, err := s.approvalRuleOf(ctx, p.TenantID, propertyID, req.Kind)
	if err != nil {
		return Approval{}, err
	}
	distinct := rule.asksForSomeoneElse(amount)
	withRule := func(e *apperr.Error) *apperr.Error {
		e = e.WithContext("kind", string(req.Kind)).WithContext("rule", rule.Rule).WithContext("amount", amount.String())
		if rule.Threshold != nil {
			e = e.WithContext("threshold", rule.Threshold.String())
		}
		return e
	}
	blank := in == nil || strings.TrimSpace(in.Email) == "" || in.Password == ""
	if req.Own && !distinct {
		return Approval{userID: p.UserID}, nil // the actor approves their own, as the rule allows; any credentials sent are not looked at
	}
	if req.Own && blank {
		if p.IsTenantAdmin {
			return Approval{userID: p.UserID, selfAdmin: true}, nil
		}
		return Approval{}, withRule(apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "the rule of this property asks for an approver other than the person who makes the change: the approver's email and password").
			WithContext("permission", string(perm)))
	}
	if blank {
		return Approval{}, withRule(apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this change needs an approval: the approver's email and password").
			WithContext("permission", string(perm)))
	}
	email := strings.ToLower(strings.TrimSpace(in.Email))
	accountKey := fmt.Sprintf("approval|%d|%s", p.TenantID, email)
	sourceKey := ""
	if ip, ok := httpx.ClientIPFrom(ctx); ok {
		sourceKey = ip.String()
	}
	for _, l := range []struct {
		lim *limiter
		key string
	}{{s.accounts, accountKey}, {s.sources, sourceKey}} {
		if l.key == "" {
			continue
		}
		if ok, until := l.lim.allowed(l.key); !ok {
			return Approval{}, apperr.New(apperr.KindRateLimited, "TOO_MANY_ATTEMPTS", "too many failed attempts; try again later").
				WithContext("retry_after", until.UTC())
		}
	}
	fail := func() {
		s.accounts.fail(accountKey)
		if sourceKey != "" {
			s.sources.fail(sourceKey)
		}
	}

	q := s.q(ctx)
	user, err := q.GetUserByEmail(ctx, iamdb.GetUserByEmailParams{TenantID: p.TenantID, Email: email})
	if errors.Is(err, pgx.ErrNoRows) {
		burnPasswordCheck(in.Password)
		fail()
		logging.FromContext(ctx).Info("approval failed", "tenant_id", p.TenantID)
		return Approval{}, approvalInvalid()
	}
	if err != nil {
		return Approval{}, err
	}
	match, err := VerifyPassword(user.PasswordHash, in.Password)
	if err != nil {
		return Approval{}, fmt.Errorf("iam: user %d has an unreadable password hash: %w", user.ID, err)
	}
	if !match || !user.IsActive {
		fail()
		logging.FromContext(ctx).Info("approval failed", "tenant_id", p.TenantID)
		return Approval{}, approvalInvalid()
	}
	allowed := user.IsTenantAdmin
	if !allowed {
		allowed, err = q.GetGrantPermission(ctx, iamdb.GetGrantPermissionParams{
			TenantID: p.TenantID, UserID: user.ID, PropertyID: propertyID, PermissionCode: string(perm),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			allowed, err = false, nil // no grant at this property
		}
		if err != nil {
			return Approval{}, err
		}
	}
	if !allowed {
		fail()
		return Approval{}, apperr.Forbidden("APPROVAL_NOT_PERMITTED", "the approver may not approve this at this property").
			WithContext("permission", string(perm))
	}
	s.accounts.reset(accountKey) // the credentials were right: what follows is the rule, not a failed attempt
	if distinct && user.ID == p.UserID {
		if user.IsTenantAdmin {
			return Approval{userID: user.ID, selfAdmin: true}, nil
		}
		return Approval{}, withRule(apperr.Forbidden("APPROVAL_SAME_USER", "the rule of this property asks for an approver other than the person who makes the change"))
	}
	return Approval{userID: user.ID}, nil
}

func approvalInvalid() error { c := *errApprovalInvalid; return &c }

// Extend applies the rule of the property again to an approval already given, for an amount that has grown: a request that
// prices several lines is approved once (one password), and the amount of the request is the total of its lines. An approval
// of someone other than the actor stays as it is. One of the actor, now above what the rule lets them approve themselves, is
// replaced by the approval of the credentials in req.Input when there are any; without them it is refused (APPROVAL_REQUIRED
// for a use case whose actor approves without a password, APPROVAL_SAME_USER otherwise), except for a tenant administrator
// (accepted, marked).
func (s *Service) Extend(ctx context.Context, req ApprovalRequest, a Approval) (Approval, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Approval{}, err
	}
	if a.userID != p.UserID || a.selfAdmin {
		return a, nil
	}
	info, ok := KindOf(req.Kind)
	if !ok {
		return Approval{}, fmt.Errorf("iam: %q is not a kind of approval", req.Kind)
	}
	amount := decimal.Zero
	if info.HasAmount {
		amount = req.Amount.Abs()
	}
	rule, err := s.approvalRuleOf(ctx, p.TenantID, req.PropertyID, req.Kind)
	if err != nil {
		return Approval{}, err
	}
	if !rule.asksForSomeoneElse(amount) {
		return a, nil
	}
	if in := req.Input; in != nil && strings.TrimSpace(in.Email) != "" && in.Password != "" {
		req.Own = false
		return s.Verify(ctx, req)
	}
	if p.IsTenantAdmin {
		return Approval{userID: a.userID, selfAdmin: true}, nil
	}
	code := "APPROVAL_SAME_USER"
	if req.Own {
		code = "APPROVAL_REQUIRED"
	}
	e := apperr.New(apperr.KindForbidden, code, "the rule of this property asks for an approver other than the person who makes the change").
		WithContext("kind", string(req.Kind)).WithContext("rule", rule.Rule).WithContext("amount", amount.String())
	if req.Own {
		e.Kind = apperr.KindInvalid
	}
	if rule.Threshold != nil {
		e = e.WithContext("threshold", rule.Threshold.String())
	}
	return Approval{}, e
}
