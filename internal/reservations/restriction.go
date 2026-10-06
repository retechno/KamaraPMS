package reservations

import (
	"context"
	"strings"

	"kamarapms/internal/availability"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/reservations/reservationsdb"
)

// Sales restrictions on a sale (docs/architecture/18-architecture-decisions.md, decision 2). The rules and the precedence live in availability (EvaluateStay); this file only asks the
// question on every path that sells a night and decides what a refusal looks like and who may override it. Nothing here repeats a rule.
//
// A restriction is a hard block: 409 STAY_RESTRICTED with the violations. A person who holds reservation.override_restriction can override it with a reason and the approval of someone
// who holds reservation.restriction_approve (their own, or the credentials typed in), the same pattern as the rate override. The override is audited in the entry of the operation. A
// booking that comes from the web or an OTA can never be overridden: nobody is there to approve it.

// RestrictionOverride is what a request brings to go past a restriction: why, and the approver's credentials when the person who asks cannot approve it themselves. The approval is
// verified and never stored: only the approver's id and the reason are recorded.
type RestrictionOverride struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval,omitempty"`
}

const maxRestrictionReasonLen = 500

// restrictionAuth is the override of one request, verified once whatever the number of lines.
type restrictionAuth struct {
	ro         *RestrictionOverride
	done       bool
	approvedBy int64
	reason     string
	violations []restrictedStay
}

type restrictionKey struct{}

// WithRestrictionOverride carries the override of a request to the paths that sell a night. A nil override is the usual case: a restriction then refuses the sale.
func WithRestrictionOverride(ctx context.Context, ro *RestrictionOverride) context.Context {
	return context.WithValue(ctx, restrictionKey{}, &restrictionAuth{ro: ro})
}

// restrictedStay is a violation with the line it belongs to when a request has several rooms (create).
type restrictedStay struct {
	Line *int `json:"line_index,omitempty"`
	availability.Violation
}

// stayAsk is one stay to ask the evaluator about. Line is the index of the room in a request with several (create), nil otherwise.
type stayAsk struct {
	line *int
	req  availability.StayRequest
}

// overridable says whether a booking can go past a restriction at all: a booking from the web or an OTA cannot.
func overridable(source string) bool { return source != "WEBSITE" && source != "OTA" }

// requireSellable asks the evaluator about every stay of the operation and refuses the sale if one breaks a restriction, unless the request carries an override that is allowed and
// approved. It reads the restrictions inside the transaction of the operation and takes no lock. Restrictions come before the inventory.
func (s *Service) requireSellable(ctx context.Context, tenantID, propertyID int64, source string, asks ...stayAsk) error {
	var found []restrictedStay
	for _, a := range asks {
		v, err := s.avail.EvaluateStay(ctx, tenantID, propertyID, a.req)
		if err != nil {
			return err
		}
		for _, x := range v.Violations {
			found = append(found, restrictedStay{Line: a.line, Violation: x})
		}
	}
	if len(found) == 0 {
		return nil
	}
	ra, _ := ctx.Value(restrictionKey{}).(*restrictionAuth)
	refuse := func() error {
		return apperr.Conflict("STAY_RESTRICTED", "the stay breaks a sales restriction").
			WithContext("violations", found).WithContext("overridable", overridable(source))
	}
	if ra == nil || ra.ro == nil || !overridable(source) {
		return refuse()
	}
	if ra.done { // the override of this request was verified for an earlier stay of it
		ra.violations = append(ra.violations, found...)
		return nil
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermReservationOverrideRestriction); err != nil {
		return err
	}
	reason := strings.TrimSpace(ra.ro.Reason)
	switch {
	case reason == "":
		return apperr.Invalid("the restriction override is invalid", fieldErr("restriction_override.reason", "REQUIRED", "say why the restriction is overridden"))
	case len([]rune(reason)) > maxRestrictionReasonLen:
		return apperr.Invalid("the restriction override is invalid", fieldErr("restriction_override.reason", "TOO_LONG", "at most 500 characters"))
	}
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if s.authz.Require(ctx, propertyID, auth.PermReservationRestrictionApprove) == nil {
		if id := p.ActorID(); id != nil {
			ra.approvedBy = *id
		}
	} else {
		if s.approver == nil {
			return apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this restriction override needs an approval: the approver's email and password").
				WithContext("permission", string(auth.PermReservationRestrictionApprove))
		}
		ap, err := s.approver.VerifyApprovalFor(ctx, propertyID, ra.ro.Approval, auth.PermReservationRestrictionApprove)
		if err != nil {
			return err
		}
		ra.approvedBy = ap.UserID()
	}
	ra.done, ra.reason = true, reason
	ra.violations = append(ra.violations, found...)
	return nil
}

// RequireSellableStay is requireSellable for a caller outside this package (the extension of a stay): the source of the booking is read from the reservation.
func (s *Service) RequireSellableStay(ctx context.Context, tenantID, propertyID, reservationID int64, req availability.StayRequest) error {
	res, err := s.q(ctx).GetReservation(ctx, reservationsdb.GetReservationParams{TenantID: tenantID, PropertyID: propertyID, ID: reservationID})
	if err != nil {
		return orNotFound(err, errNotFound())
	}
	return s.requireSellable(ctx, tenantID, propertyID, res.Source, stayAsk{req: req})
}

// RestrictionAudit is what the audit entry of a request records about the restrictions it went past: who approved, why, and what was broken. It is nil when nothing was overridden.
func RestrictionAudit(ctx context.Context) map[string]any {
	ra, _ := ctx.Value(restrictionKey{}).(*restrictionAuth)
	if ra == nil || !ra.done {
		return nil
	}
	return map[string]any{"restriction_override": map[string]any{"approved_by": ra.approvedBy, "reason": ra.reason, "violations": ra.violations}}
}

// withRestrictionAudit adds the override to the data of an audit entry.
func withRestrictionAudit(ctx context.Context, data map[string]any) map[string]any {
	for k, v := range RestrictionAudit(ctx) {
		data[k] = v
	}
	return data
}

// askOf is the stay request of a line that is sold as new, or changed from previous (nil: everything is new).
func askOf(typeID, planID int64, arrival, departure, bd civil.Date, previous *availability.StayDates, line *int) stayAsk {
	return stayAsk{line: line, req: availability.StayRequest{
		RoomTypeID: typeID, RatePlanID: planID, Arrival: arrival, Departure: departure, BusinessDate: bd, Previous: previous,
	}}
}
