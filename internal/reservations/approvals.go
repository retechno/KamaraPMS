package reservations

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// approvalRun is the approval of a request that is asked once whatever the number of its lines, and whose amount is the
// total of the lines (docs/architecture/32-distinct-approver.md section 2): each line adds its amount, the first line checks
// the credentials, the next ones apply the rule again to the new total (iam.Extend).
type approvalRun struct {
	total      decimal.Decimal
	ap         iam.Approval
	done       bool
	approvedBy int64
	marked     bool // an administrator approved their own against the rule of the property
}

// approveRun adds amount to the run and approves it: the caller's own when they hold perm and the rule allows it, otherwise
// by the credentials in in (422 APPROVAL_REQUIRED without them).
func (s *Service) approveRun(ctx context.Context, propertyID int64, kind iam.ApprovalKind, perm auth.Permission, in *iam.ApprovalInput, run *approvalRun, amount decimal.Decimal) error {
	run.total = run.total.Add(amount.Abs())
	own := s.authz.Require(ctx, propertyID, perm) == nil
	if s.approver == nil { // without an approver only someone who holds the permission can approve
		if !own {
			return apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this change needs an approval: the approver's email and password").WithContext("permission", string(perm))
		}
		p, err := auth.Require(ctx)
		if err != nil {
			return err
		}
		run.approvedBy, run.done = p.UserID, true
		return nil
	}
	req := iam.ApprovalRequest{PropertyID: propertyID, Input: in, Kind: kind, Amount: run.total, Permission: perm, Own: own}
	var ap iam.Approval
	var err error
	if run.done {
		ap, err = s.approver.Extend(ctx, req, run.ap)
	} else {
		ap, err = s.approver.Verify(ctx, req)
	}
	if err != nil {
		return err
	}
	run.ap, run.approvedBy, run.marked, run.done = ap, ap.UserID(), ap.SelfApprovedByAdmin(), true
	return nil
}
