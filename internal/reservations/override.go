package reservations

import (
	"context"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// Approver verifies the approval of a rate override: the credentials of someone who holds the permission. The IAM
// service is one; reservations only needs this part of it.
type Approver interface {
	Verify(ctx context.Context, req iam.ApprovalRequest) (iam.Approval, error)
	Extend(ctx context.Context, req iam.ApprovalRequest, a iam.Approval) (iam.Approval, error)
}

// SetApprover installs the approver (call it once while wiring). Without one, only someone who holds the approval
// permission themselves can override a rate.
func (s *Service) SetApprover(a Approver) { s.approver = a }

const maxRateOverrideReasonLen = 500

// overrideAuth is what a request brings to justify its rate overrides: the reason, and the approver's credentials when
// the person who enters the override cannot approve it. It is verified once per request, whatever the number of lines; the
// amount the rule of the property sees is the total of what the overrides move in the request.
type overrideAuth struct {
	approval   *iam.ApprovalInput
	reason     string
	reasonOK   bool
	run        approvalRun
	reasonText string
}

type overrideKey struct{}

// WithOverrideApproval carries the reason and the approval of the rate overrides of a request to the pricing of its
// lines. The approval is never stored: it is verified and only the approver's id and the reason are recorded.
func WithOverrideApproval(ctx context.Context, approval *iam.ApprovalInput, reason string) context.Context {
	return context.WithValue(ctx, overrideKey{}, &overrideAuth{approval: approval, reason: reason})
}

// requireOverrideReason is the first half of the rule of a rate override: a reason (the permission is checked by the caller).
func (s *Service) requireOverrideReason(ctx context.Context) error {
	oa, _ := ctx.Value(overrideKey{}).(*overrideAuth)
	if oa == nil {
		oa = &overrideAuth{}
	}
	if oa.reasonOK {
		return nil
	}
	reason := strings.TrimSpace(oa.reason)
	switch {
	case reason == "":
		return apperr.Invalid("the rate override is invalid", fieldErr("rate_override_reason", "REQUIRED", "say why the price is changed"))
	case len([]rune(reason)) > maxRateOverrideReasonLen:
		return apperr.Invalid("the rate override is invalid", fieldErr("rate_override_reason", "TOO_LONG", "at most 500 characters"))
	}
	oa.reasonOK, oa.reasonText = true, reason
	return nil
}

// approveOverrides is the second half: the approval of the overrides of a priced line, whose amount (the sum over its nights of
// what the price moves) is added to the total of the request. The approval is their own when the person also holds
// reservation.override_rate_approve and the rule of the property allows it, otherwise the credentials of someone who does
// (422 APPROVAL_REQUIRED without them).
func (s *Service) approveOverrides(ctx context.Context, propertyID int64, moved decimal.Decimal) error {
	oa, _ := ctx.Value(overrideKey{}).(*overrideAuth)
	if oa == nil {
		oa = &overrideAuth{}
	}
	return s.approveRun(ctx, propertyID, iam.KindRateOverride, auth.PermReservationOverrideApprove, oa.approval, &oa.run, moved)
}

// OverrideAudit is what the audit entry of a request records about its rate overrides: who approved and why. It is
// empty when no override was approved in the request.
func OverrideAudit(ctx context.Context) map[string]any {
	oa, _ := ctx.Value(overrideKey{}).(*overrideAuth)
	if oa == nil || !oa.run.done {
		return nil
	}
	out := map[string]any{"override_approved_by": oa.run.approvedBy, "rate_override_reason": oa.reasonText}
	if oa.run.marked {
		out[iam.SelfApprovedKey] = true
	}
	return out
}

// withOverrideAudit adds the override fields to the data of an audit entry.
func withOverrideAudit(ctx context.Context, data map[string]any) map[string]any {
	for k, v := range OverrideAudit(ctx) {
		data[k] = v
	}
	return data
}
