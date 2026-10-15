package iam

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam/iamdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/money"
)

// The rules of approval (docs/architecture/32-distinct-approver.md section 3).
const (
	RuleFree          = "FREE"           // anyone who holds the permission may approve, including the person who acts (the default)
	RuleDistinct      = "DISTINCT"       // the approver must be someone other than the person who acts
	RuleDistinctAbove = "DISTINCT_ABOVE" // the person who acts may approve up to and including the limit; above it someone else
)

// ApprovalRule is the rule of a kind at a property.
type ApprovalRule struct {
	Rule      string
	Threshold *decimal.Decimal
}

// asksForSomeoneElse says whether the rule refuses the person who acts as the approver of a correction of this amount. Equal
// to the limit is allowed.
func (r ApprovalRule) asksForSomeoneElse(amount decimal.Decimal) bool {
	switch r.Rule {
	case RuleDistinct:
		return true
	case RuleDistinctAbove:
		return r.Threshold != nil && amount.GreaterThan(*r.Threshold)
	}
	return false
}

// approvalRuleOf reads the rule of a kind (one read by the primary key); no row is FREE.
func (s *Service) approvalRuleOf(ctx context.Context, tenantID, propertyID int64, kind ApprovalKind) (ApprovalRule, error) {
	row, err := s.q(ctx).GetApprovalRule(ctx, iamdb.GetApprovalRuleParams{TenantID: tenantID, PropertyID: propertyID, Kind: string(kind)})
	if errors.Is(err, pgx.ErrNoRows) {
		return ApprovalRule{Rule: RuleFree}, nil
	}
	if err != nil {
		return ApprovalRule{}, err
	}
	return ApprovalRule{Rule: row.Rule, Threshold: row.Threshold}, nil
}

// ApprovalRuleView is a kind with the rule in force, as the screen of the rules lists it.
type ApprovalRuleView struct {
	Kind       ApprovalKind    `json:"kind"`
	Rule       string          `json:"rule"`
	Threshold  *string         `json:"threshold"`
	HasAmount  bool            `json:"has_amount"`
	Permission auth.Permission `json:"permission"`
	// Approvers is the number of active users, other than the tenant administrators, who hold the permission of the kind at
	// the property: fewer than two with a rule that asks for someone else is a warning of the screen (it is answered for every
	// kind, so that the screen can warn before a rule is saved).
	Approvers int64      `json:"approvers"`
	UpdatedAt *time.Time `json:"updated_at"`
}

// ApprovalRuleInput is the rule a request sets for a kind.
type ApprovalRuleInput struct {
	Kind      string  `json:"kind"`
	Rule      string  `json:"rule"`
	Threshold *string `json:"threshold"`
}

// Rules is the use cases of the rules of approval of a property.
type Rules struct {
	svc   *Service
	authz auth.Authorizer
}

// NewRules wires the rules of approval.
func NewRules(svc *Service, authz auth.Authorizer) *Rules { return &Rules{svc: svc, authz: authz} }

// List answers every kind with its rule (FREE for a kind without a row). Any grant on the property may read it: the dialog of
// approval says the rule in force.
func (r *Rules) List(ctx context.Context, propertyID int64, decimals int32) ([]ApprovalRuleView, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.authz.CanAccess(ctx, propertyID); err != nil {
		return nil, err
	}
	q := r.svc.q(ctx)
	rows, err := q.ListApprovalRules(ctx, iamdb.ListApprovalRulesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	byKind := map[string]iamdb.ListApprovalRulesRow{}
	for _, row := range rows {
		byKind[row.Kind] = row
	}
	out := make([]ApprovalRuleView, 0, len(approvalKinds))
	count := map[auth.Permission]int64{}
	for _, ki := range approvalKinds {
		v := ApprovalRuleView{Kind: ki.Kind, Rule: RuleFree, HasAmount: ki.HasAmount, Permission: ki.Permission}
		if row, ok := byKind[string(ki.Kind)]; ok {
			v.Rule = row.Rule
			if row.Threshold != nil {
				t := row.Threshold.StringFixed(decimals)
				v.Threshold = &t
			}
			u := row.UpdatedAt
			v.UpdatedAt = &u
		}
		n, seen := count[ki.Permission]
		if !seen {
			n, err = q.CountApproversWith(ctx, iamdb.CountApproversWithParams{TenantID: p.TenantID, PropertyID: propertyID, PermissionCode: string(ki.Permission)})
			if err != nil {
				return nil, err
			}
			count[ki.Permission] = n
		}
		v.Approvers = n
		out = append(out, v)
	}
	return out, nil
}

// Set changes the rules of the kinds named (property.manage). FREE deletes the row. Each change is audited with the old and
// the new rule. Nothing about the books depends on a rule, so it may be changed at any time.
func (r *Rules) Set(ctx context.Context, propertyID int64, decimals int32, in []ApprovalRuleInput) error {
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if err := r.authz.Require(ctx, propertyID, auth.PermPropertyManage); err != nil {
		return err
	}
	type change struct {
		kind ApprovalKind
		rule ApprovalRule
	}
	var fields []apperr.FieldError
	var changes []change
	seen := map[string]bool{}
	for i, x := range in {
		field := func(name string) string { return "rules[" + strconv.Itoa(i) + "]." + name }
		kind := strings.TrimSpace(x.Kind)
		ki, ok := KindOf(ApprovalKind(kind))
		switch {
		case !ok:
			fields = append(fields, apperr.FieldError{Field: field("kind"), Code: "INVALID_VALUE", Message: "a kind of approval"})
			continue
		case seen[kind]:
			fields = append(fields, apperr.FieldError{Field: field("kind"), Code: "DUPLICATE", Message: "the kind is named twice"})
			continue
		}
		seen[kind] = true
		c := change{kind: ki.Kind, rule: ApprovalRule{Rule: x.Rule}}
		switch x.Rule {
		case RuleFree, RuleDistinct:
			if x.Threshold != nil && strings.TrimSpace(*x.Threshold) != "" {
				fields = append(fields, apperr.FieldError{Field: field("threshold"), Code: "NOT_ALLOWED", Message: "only a rule DISTINCT_ABOVE has a limit"})
			}
		case RuleDistinctAbove:
			if !ki.HasAmount {
				fields = append(fields, apperr.FieldError{Field: field("rule"), Code: "RULE_NOT_ALLOWED_FOR_KIND", Message: "this kind has no amount: FREE or DISTINCT"})
				continue
			}
			var t decimal.Decimal
			var perr error
			if x.Threshold != nil {
				t, perr = money.Parse(strings.TrimSpace(*x.Threshold))
			}
			switch {
			case x.Threshold == nil || strings.TrimSpace(*x.Threshold) == "":
				fields = append(fields, apperr.FieldError{Field: field("threshold"), Code: "REQUIRED", Message: "a limit"})
			case perr != nil || t.IsNegative() || !t.Equal(t.Round(decimals)):
				fields = append(fields, apperr.FieldError{Field: field("threshold"), Code: "INVALID_AMOUNT", Message: "a limit of zero or more, with at most the currency decimals"})
			default:
				c.rule.Threshold = &t
			}
		default:
			fields = append(fields, apperr.FieldError{Field: field("rule"), Code: "INVALID_VALUE", Message: "FREE, DISTINCT or DISTINCT_ABOVE"})
		}
		changes = append(changes, c)
	}
	if len(fields) > 0 {
		// a kind without an amount refused DISTINCT_ABOVE is a 422 with its own code
		for _, f := range fields {
			if f.Code == "RULE_NOT_ALLOWED_FOR_KIND" {
				e := apperr.New(apperr.KindInvalid, "RULE_NOT_ALLOWED_FOR_KIND", "this kind of approval has no amount: its rule is FREE or DISTINCT")
				e.Fields = fields
				return e
			}
		}
		return apperr.Invalid("the rules of approval are invalid", fields...)
	}
	return r.svc.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := r.svc.q(ctx)
		for _, c := range changes {
			old, err := r.svc.approvalRuleOf(ctx, p.TenantID, propertyID, c.kind)
			if err != nil {
				return err
			}
			if old.Rule == c.rule.Rule && sameThreshold(old.Threshold, c.rule.Threshold) {
				continue
			}
			if c.rule.Rule == RuleFree {
				err = q.DeleteApprovalRule(ctx, iamdb.DeleteApprovalRuleParams{TenantID: p.TenantID, PropertyID: propertyID, Kind: string(c.kind)})
			} else {
				err = q.UpsertApprovalRule(ctx, iamdb.UpsertApprovalRuleParams{
					TenantID: p.TenantID, PropertyID: propertyID, Kind: string(c.kind), Rule: c.rule.Rule, Threshold: c.rule.Threshold, ActorID: p.ActorID(),
				})
			}
			if err != nil {
				return err
			}
			if err := r.svc.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, UserID: p.ActorID(), Action: "property.approval_rule_changed",
				EntityType: "approval_rule", EntityLabel: string(c.kind), Old: ruleAudit(old, decimals), New: ruleAudit(c.rule, decimals)}); err != nil {
				return err
			}
		}
		return nil
	})
}

func sameThreshold(a, b *decimal.Decimal) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func ruleAudit(r ApprovalRule, decimals int32) map[string]any {
	m := map[string]any{"rule": r.Rule}
	if r.Threshold != nil {
		m["threshold"] = r.Threshold.StringFixed(decimals)
	}
	return m
}

// Decimals gives the currency decimals of a property: the rules are listed and checked at them.
type Decimals func(ctx context.Context, propertyID int64) (int32, error)

// RegisterRules adds the routes of the rules (GET and PUT {P}/approval-rules).
func (r *Rules) RegisterRules(mux *http.ServeMux, propertyID func(*http.Request) (int64, error), decimals Decimals) {
	const p = "/api/v1/properties/{propertyId}/approval-rules"
	mux.Handle("GET "+p, httpx.HandlerFunc(func(w http.ResponseWriter, req *http.Request) error {
		pid, err := propertyID(req)
		if err != nil {
			return err
		}
		places, err := decimals(req.Context(), pid)
		if err != nil {
			return err
		}
		out, err := r.List(req.Context(), pid, places)
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
	}))
	mux.Handle("PUT "+p, httpx.HandlerFunc(func(w http.ResponseWriter, req *http.Request) error {
		pid, err := propertyID(req)
		if err != nil {
			return err
		}
		var body struct {
			Rules []ApprovalRuleInput `json:"rules"`
		}
		if err := httpx.DecodeJSON(w, req, &body); err != nil {
			return err
		}
		places, err := decimals(req.Context(), pid)
		if err != nil {
			return err
		}
		if err := r.Set(req.Context(), pid, places, body.Rules); err != nil {
			return err
		}
		out, err := r.List(req.Context(), pid, places)
		if err != nil {
			return err
		}
		return httpx.WriteJSON(w, http.StatusOK, map[string]any{"data": out})
	}))
}
