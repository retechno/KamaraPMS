package reservations

import (
	"context"
	"strings"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// Approver verifies the approval of a rate override: the credentials of someone who holds the permission. The IAM
// service is one; reservations only needs this part of it.
type Approver interface {
	VerifyApprovalFor(ctx context.Context, propertyID int64, in *iam.ApprovalInput, perm auth.Permission) (iam.Approval, error)
}

// SetApprover installs the approver (call it once while wiring). Without one, only someone who holds the approval
// permission themselves can override a rate.
func (s *Service) SetApprover(a Approver) { s.approver = a }

const maxRateOverrideReasonLen = 500

// overrideAuth is what a request brings to justify its rate overrides: the reason, and the approver's credentials when
// the person who enters the override cannot approve it. It is verified once per request, whatever the number of lines.
type overrideAuth struct {
	approval   *iam.ApprovalInput
	reason     string
	done       bool
	approvedBy int64
	reasonText string
}

type overrideKey struct{}

// WithOverrideApproval carries the reason and the approval of the rate overrides of a request to the pricing of its
// lines. The approval is never stored: it is verified and only the approver's id and the reason are recorded.
func WithOverrideApproval(ctx context.Context, approval *iam.ApprovalInput, reason string) context.Context {
	return context.WithValue(ctx, overrideKey{}, &overrideAuth{approval: approval, reason: reason})
}

// requireOverrideApproval is the rule of a rate override: the person needs reservation.override_rate (checked by the
// caller), a reason, and an approval: their own when they also hold reservation.override_rate_approve, otherwise the
// credentials of someone who does (422 APPROVAL_REQUIRED without them).
func (s *Service) requireOverrideApproval(ctx context.Context, propertyID int64) error {
	oa, _ := ctx.Value(overrideKey{}).(*overrideAuth)
	if oa == nil {
		oa = &overrideAuth{}
	}
	if oa.done {
		return nil
	}
	reason := strings.TrimSpace(oa.reason)
	switch {
	case reason == "":
		return apperr.Invalid("the rate override is invalid", fieldErr("rate_override_reason", "REQUIRED", "say why the price is changed"))
	case len([]rune(reason)) > maxRateOverrideReasonLen:
		return apperr.Invalid("the rate override is invalid", fieldErr("rate_override_reason", "TOO_LONG", "at most 500 characters"))
	}
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if s.authz.Require(ctx, propertyID, auth.PermReservationOverrideApprove) == nil {
		if id := p.ActorID(); id != nil {
			oa.approvedBy = *id
		}
	} else {
		if s.approver == nil {
			return apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this rate override needs an approval: the approver's email and password").
				WithContext("permission", string(auth.PermReservationOverrideApprove))
		}
		ap, err := s.approver.VerifyApprovalFor(ctx, propertyID, oa.approval, auth.PermReservationOverrideApprove)
		if err != nil {
			return err
		}
		oa.approvedBy = ap.UserID()
	}
	oa.done, oa.reasonText = true, reason
	return nil
}

// OverrideAudit is what the audit entry of a request records about its rate overrides: who approved and why. It is
// empty when no override was approved in the request.
func OverrideAudit(ctx context.Context) map[string]any {
	oa, _ := ctx.Value(overrideKey{}).(*overrideAuth)
	if oa == nil || !oa.done {
		return nil
	}
	return map[string]any{"override_approved_by": oa.approvedBy, "rate_override_reason": oa.reasonText}
}

// withOverrideAudit adds the override fields to the data of an audit entry.
func withOverrideAudit(ctx context.Context, data map[string]any) map[string]any {
	for k, v := range OverrideAudit(ctx) {
		data[k] = v
	}
	return data
}
