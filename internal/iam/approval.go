package iam

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/iam/iamdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
)

// Approval is a verified correction approval: the user who entered valid credentials and holds
// correction.approve at the property. Only VerifyApproval can create one (the field is unexported), so a
// posting service that requires an Approval knows a password was checked.
type Approval struct{ userID int64 }

// UserID is the approver, to be recorded as approved_by.
func (a Approval) UserID() int64 { return a.userID }

// IsZero reports whether the value carries no approval.
func (a Approval) IsZero() bool { return a.userID == 0 }

// ApprovalInput is the `approval` block of a correction request: the approver's own credentials.
type ApprovalInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

var errApprovalInvalid = apperr.Unauthorized("APPROVAL_INVALID_CREDENTIALS", "the approver's email or password is incorrect")

// VerifyApproval checks an approver's credentials for a correction at a property (docs/architecture/06-api.md
// §14.1). The approver may be the caller. The password is verified with the login verifier and counts toward
// the login rate limits; it is never stored, logged or echoed, and no session or token is created.
//
// A missing block is 422 APPROVAL_REQUIRED. Wrong email, wrong password or an inactive user is one generic 401
// APPROVAL_INVALID_CREDENTIALS. A valid approver without correction.approve at the property is 403
// APPROVAL_NOT_PERMITTED. Tenant administrators hold the permission everywhere.
func (s *Service) VerifyApproval(ctx context.Context, propertyID int64, in *ApprovalInput) (Approval, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Approval{}, err
	}
	if in == nil || strings.TrimSpace(in.Email) == "" || in.Password == "" {
		return Approval{}, apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this correction needs an approval: the approver's email and password").
			WithContext("permission", string(auth.PermCorrectionApprove))
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
			TenantID: p.TenantID, UserID: user.ID, PropertyID: propertyID, PermissionCode: string(auth.PermCorrectionApprove),
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
		return Approval{}, apperr.Forbidden("APPROVAL_NOT_PERMITTED", "the approver may not approve corrections at this property").
			WithContext("permission", string(auth.PermCorrectionApprove))
	}
	s.accounts.reset(accountKey)
	return Approval{userID: user.ID}, nil
}

func approvalInvalid() error { c := *errApprovalInvalid; return &c }
