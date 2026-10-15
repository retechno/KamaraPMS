package iam_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// The rules of approval (docs/architecture/32-distinct-approver.md): who may approve a correction, by kind and by amount.

type approvalWorld struct {
	env
	rules    *iam.Rules
	prop     int64
	admin    context.Context // the tenant administrator
	clerk    context.Context // holds correction.approve (an approver who is also the one who acts)
	other    context.Context // holds correction.approve
	nobody   context.Context // holds nothing
	managers context.Context // holds property.manage only
	clerkIn  *iam.ApprovalInput
	otherIn  *iam.ApprovalInput
	adminIn  *iam.ApprovalInput
	nobodyIn *iam.ApprovalInput
}

func newApprovalWorld(t *testing.T) approvalWorld {
	t.Helper()
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	adminCtx := e.as(t, e.login(t, "ABC", "admin@hotel.com", pw))
	bali := e.property(t, adminCtx, "BALI")
	mk := func(name string, perms ...auth.Permission) iam.Role {
		r, err := e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &name, Permissions: &perms})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	approver := mk("Approver", auth.PermCorrectionApprove, auth.PermFolioRead)
	setter := mk("Setter", auth.PermPropertyManage)
	empty := mk("Empty", auth.PermFolioRead)
	user := func(email string, role iam.Role) context.Context {
		if _, err := e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: email, FullName: email, Password: pw, Grants: []iam.GrantInput{{PropertyID: bali, RoleID: role.ID}}}); err != nil {
			t.Fatal(err)
		}
		return e.as(t, e.login(t, "ABC", email, pw))
	}
	w := approvalWorld{env: e, rules: iam.NewRules(e.iam, e.authz), prop: bali, admin: adminCtx}
	w.clerk, w.other, w.nobody, w.managers = user("clerk@hotel.com", approver), user("other@hotel.com", approver), user("nobody@hotel.com", empty), user("setter@hotel.com", setter)
	w.clerkIn = &iam.ApprovalInput{Email: "clerk@hotel.com", Password: pw}
	w.otherIn = &iam.ApprovalInput{Email: "other@hotel.com", Password: pw}
	w.adminIn = &iam.ApprovalInput{Email: "admin@hotel.com", Password: pw}
	w.nobodyIn = &iam.ApprovalInput{Email: "nobody@hotel.com", Password: pw}
	return w
}

func amount(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func (w approvalWorld) set(t *testing.T, kind, rule string, threshold *string) {
	t.Helper()
	if err := w.rules.Set(w.admin, w.prop, 0, []iam.ApprovalRuleInput{{Kind: kind, Rule: rule, Threshold: threshold}}); err != nil {
		t.Fatalf("set %s %s: %v", kind, rule, err)
	}
}

func ptr(s string) *string { return &s }

func (w approvalWorld) verify(ctx context.Context, in *iam.ApprovalInput, kind iam.ApprovalKind, amt string) (iam.Approval, error) {
	return w.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: w.prop, Input: in, Kind: kind, Amount: amount(amt)})
}

func userID(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	p, err := auth.Require(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return p.UserID
}

// Without a rule, anyone who holds the permission approves, the person who acts included, as before.
func TestApprovalRuleFreeIsTheDefault(t *testing.T) {
	w := newApprovalWorld(t)
	ap, err := w.verify(w.clerk, w.clerkIn, iam.KindRefund, "9999999")
	if err != nil || ap.UserID() != userID(t, w.clerk) || ap.SelfApprovedByAdmin() {
		t.Fatalf("a person approves their own with no rule: %v %+v", err, ap)
	}
	// FREE written down is no rule
	w.set(t, "REFUND", "DISTINCT", nil)
	w.set(t, "REFUND", "FREE", nil)
	if _, err := w.verify(w.clerk, w.clerkIn, iam.KindRefund, "9999999"); err != nil {
		t.Fatalf("FREE: %v", err)
	}
	var n int
	if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM property_approval_rules`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("FREE deletes the row: %d %v", n, err)
	}
	// the right is still needed, and a wrong password is still refused
	_, err = w.verify(w.nobody, w.nobodyIn, iam.KindRefund, "1")
	wantCode(t, err, "APPROVAL_NOT_PERMITTED")
	_, err = w.verify(w.clerk, &iam.ApprovalInput{Email: "clerk@hotel.com", Password: "wrong wrong wrong"}, iam.KindRefund, "1")
	wantCode(t, err, "APPROVAL_INVALID_CREDENTIALS")
	_, err = w.verify(w.clerk, nil, iam.KindRefund, "1")
	wantCode(t, err, "APPROVAL_REQUIRED")
}

func TestApprovalRuleDistinct(t *testing.T) {
	w := newApprovalWorld(t)
	w.set(t, "REFUND", "DISTINCT", nil)
	// the person who acts approving their own: refused with the rule in the context; the credentials were right, so it is not a failed attempt
	for i := 0; i < 8; i++ { // the limit of failed attempts is 5
		_, err := w.verify(w.clerk, w.clerkIn, iam.KindRefund, "100")
		wantCode(t, err, "APPROVAL_SAME_USER")
		if i == 0 {
			e, _ := apperr.As(err)
			if e.Kind != apperr.KindForbidden || e.Context["kind"] != "REFUND" || e.Context["rule"] != "DISTINCT" || e.Context["amount"] != "100" {
				t.Fatalf("the context names the rule: %+v", e)
			}
		}
	}
	// someone else is accepted and is the approver
	ap, err := w.verify(w.clerk, w.otherIn, iam.KindRefund, "100")
	if err != nil || ap.UserID() != userID(t, w.other) || ap.SelfApprovedByAdmin() {
		t.Fatalf("another approver: %v %+v", err, ap)
	}
	// another kind is untouched
	if _, err := w.verify(w.clerk, w.clerkIn, iam.KindFolioAdjustment, "100"); err != nil {
		t.Fatalf("another kind is FREE: %v", err)
	}
	// the rule of one property is not the rule of another
}

func TestApprovalRuleDistinctAboveIsInclusive(t *testing.T) {
	w := newApprovalWorld(t)
	w.set(t, "FOLIO_ADJUSTMENT", "DISTINCT_ABOVE", ptr("500000"))
	for _, tc := range []struct {
		amt  string
		fail bool
	}{{"499999", false}, {"500000", false}, {"500000.001", true}, {"-500000", false}, {"-500001", true}, {"0", false}} {
		_, err := w.verify(w.clerk, w.clerkIn, iam.KindFolioAdjustment, tc.amt)
		if tc.fail {
			wantCode(t, err, "APPROVAL_SAME_USER")
			e, _ := apperr.As(err)
			if e.Context["threshold"] != "500000" {
				t.Fatalf("the limit is in the context: %+v", e.Context)
			}
		} else if err != nil {
			t.Fatalf("%s: %v", tc.amt, err)
		}
	}
	if ap, err := w.verify(w.clerk, w.otherIn, iam.KindFolioAdjustment, "9000000"); err != nil || ap.UserID() != userID(t, w.other) {
		t.Fatalf("another user above the limit: %v", err)
	}
}

func TestApprovalRuleTenantAdminIsExempt(t *testing.T) {
	w := newApprovalWorld(t)
	w.set(t, "REFUND", "DISTINCT", nil)
	w.set(t, "FOLIO_ADJUSTMENT", "DISTINCT_ABOVE", ptr("500000"))
	// an administrator approving their own: accepted and marked
	ap, err := w.verify(w.admin, w.adminIn, iam.KindRefund, "100")
	if err != nil || !ap.SelfApprovedByAdmin() || ap.UserID() != userID(t, w.admin) {
		t.Fatalf("DISTINCT: %v %+v", err, ap)
	}
	fields := ap.AuditFields(map[string]any{})
	if fields["approved_by"] != userID(t, w.admin) || fields[iam.SelfApprovedKey] != true {
		t.Fatalf("the entry says so: %v", fields)
	}
	// above the limit: marked; below it: nothing is marked (the rule is satisfied)
	ap, err = w.verify(w.admin, w.adminIn, iam.KindFolioAdjustment, "500001")
	if err != nil || !ap.SelfApprovedByAdmin() {
		t.Fatalf("above the limit: %v %+v", err, ap)
	}
	ap, err = w.verify(w.admin, w.adminIn, iam.KindFolioAdjustment, "500000")
	if err != nil || ap.SelfApprovedByAdmin() {
		t.Fatalf("at the limit nothing is marked: %v %+v", err, ap)
	}
	// an administrator who acts and another user who approves: no marker
	ap, err = w.verify(w.admin, w.otherIn, iam.KindRefund, "100")
	if err != nil || ap.SelfApprovedByAdmin() || ap.UserID() != userID(t, w.other) {
		t.Fatalf("another approver: %v %+v", err, ap)
	}
	// someone else who acts, the administrator approves: no marker (they are not the actor)
	ap, err = w.verify(w.clerk, w.adminIn, iam.KindRefund, "100")
	if err != nil || ap.SelfApprovedByAdmin() {
		t.Fatalf("the administrator approves another's: %v %+v", err, ap)
	}
	// FREE: no marker
	w.set(t, "REFUND", "FREE", nil)
	ap, err = w.verify(w.admin, w.adminIn, iam.KindRefund, "100")
	if err != nil || ap.SelfApprovedByAdmin() {
		t.Fatalf("FREE: %v %+v", err, ap)
	}
}

func TestApprovalOwnAndExtend(t *testing.T) {
	w := newApprovalWorld(t)
	own := func(ctx context.Context, in *iam.ApprovalInput, kind iam.ApprovalKind, amt string) (iam.Approval, error) {
		return w.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: w.prop, Input: in, Kind: kind, Amount: amount(amt), Own: true})
	}
	// the actor holds the right and the rule is FREE: their own, no password
	if ap, err := own(w.clerk, nil, iam.KindJournal, "100"); err != nil || ap.UserID() != userID(t, w.clerk) {
		t.Fatalf("own, FREE: %v", err)
	}
	w.set(t, "JOURNAL", "DISTINCT_ABOVE", ptr("1000"))
	if _, err := own(w.clerk, nil, iam.KindJournal, "1000"); err != nil {
		t.Fatalf("own, at the limit: %v", err)
	}
	// above the limit: someone else is asked for; the credentials of another are accepted
	_, err := own(w.clerk, nil, iam.KindJournal, "1001")
	wantCode(t, err, "APPROVAL_REQUIRED")
	if e, _ := apperr.As(err); e.Context["rule"] != "DISTINCT_ABOVE" || e.Context["threshold"] != "1000" {
		t.Fatalf("the context: %+v", e.Context)
	}
	if ap, err := own(w.clerk, w.otherIn, iam.KindJournal, "1001"); err != nil || ap.UserID() != userID(t, w.other) {
		t.Fatalf("another: %v", err)
	}
	// their own credentials above the limit are refused too
	_, err = own(w.clerk, w.clerkIn, iam.KindJournal, "1001")
	wantCode(t, err, "APPROVAL_SAME_USER")
	// an administrator approves their own above the limit without a password, marked
	if ap, err := own(w.admin, nil, iam.KindJournal, "1001"); err != nil || !ap.SelfApprovedByAdmin() {
		t.Fatalf("admin own: %v %+v", err, ap)
	}

	// Extend: a request approved once, whose amount grows with each line, applies the rule to the total
	first, err := own(w.clerk, nil, iam.KindJournal, "600")
	if err != nil {
		t.Fatal(err)
	}
	req := iam.ApprovalRequest{PropertyID: w.prop, Kind: iam.KindJournal, Amount: amount("1000")}
	if _, err := w.iam.Extend(w.clerk, req, first); err != nil {
		t.Fatalf("extend to the limit: %v", err)
	}
	req.Amount = amount("1001")
	_, err = w.iam.Extend(w.clerk, req, first)
	wantCode(t, err, "APPROVAL_SAME_USER")
	if ap, err := w.iam.Extend(w.admin, req, mustApproval(t, w, w.admin, w.adminIn, iam.KindJournal, "10")); err != nil || !ap.SelfApprovedByAdmin() {
		t.Fatalf("extend for an administrator: %v %+v", err, ap)
	}
	// an approval of someone else stays as it is whatever the total
	other := mustApproval(t, w, w.clerk, w.otherIn, iam.KindJournal, "10")
	if ap, err := w.iam.Extend(w.clerk, req, other); err != nil || ap.UserID() != userID(t, w.other) {
		t.Fatalf("extend keeps another's approval: %v", err)
	}
}

func mustApproval(t *testing.T, w approvalWorld, ctx context.Context, in *iam.ApprovalInput, kind iam.ApprovalKind, amt string) iam.Approval {
	t.Helper()
	ap, err := w.verify(ctx, in, kind, amt)
	if err != nil {
		t.Fatal(err)
	}
	return ap
}

// A kind without an amount takes no limit: its amount is not looked at.
func TestApprovalRuleOfAKindWithoutAnAmount(t *testing.T) {
	w := newApprovalWorld(t)
	err := w.rules.Set(w.admin, w.prop, 0, []iam.ApprovalRuleInput{{Kind: "BANK", Rule: "DISTINCT_ABOVE", Threshold: ptr("1")}})
	wantCode(t, err, "RULE_NOT_ALLOWED_FOR_KIND")
	w.set(t, "BANK", "DISTINCT", nil)
	_, err = w.verify(w.clerk, w.clerkIn, iam.KindBank, "0")
	wantCode(t, err, "APPROVAL_SAME_USER")
	_, err = w.verify(w.clerk, w.clerkIn, iam.KindBank, "123456789")
	wantCode(t, err, "APPROVAL_SAME_USER")
	if _, err := w.verify(w.clerk, w.otherIn, iam.KindBank, "0"); err != nil {
		t.Fatal(err)
	}
}

func TestApprovalRulesAreChangedAndRead(t *testing.T) {
	w := newApprovalWorld(t)
	list, err := w.rules.List(w.managers, w.prop, 2)
	if err != nil || len(list) != len(iam.ApprovalKinds()) || len(list) != 22 {
		t.Fatalf("every kind is answered: %d %v", len(list), err)
	}
	for _, r := range list {
		if r.Rule != "FREE" || r.Threshold != nil {
			t.Fatalf("a kind without a rule is FREE: %+v", r)
		}
	}
	// the full table is for who manages the property; the dialog of approval reads only the rule in force, any grant may
	_, err = w.rules.List(w.clerk, w.prop, 2)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = w.rules.List(w.nobody, w.prop, 2)
	wantCode(t, err, "PERMISSION_DENIED")
	inForce, err := w.rules.InForce(w.nobody, w.prop, 2)
	if err != nil || len(inForce) != 22 {
		t.Fatalf("the rule in force is read by any grant: %d %v", len(inForce), err)
	}
	// invalid
	bad := func(in iam.ApprovalRuleInput, field string) {
		t.Helper()
		err := w.rules.Set(w.admin, w.prop, 0, []iam.ApprovalRuleInput{in})
		wantCode(t, err, "VALIDATION_FAILED")
		if e, _ := apperr.As(err); len(e.Fields) != 1 || e.Fields[0].Field != field {
			t.Fatalf("%+v: %+v", in, e.Fields)
		}
	}
	bad(iam.ApprovalRuleInput{Kind: "NOPE", Rule: "DISTINCT"}, "rules[0].kind")
	bad(iam.ApprovalRuleInput{Kind: "REFUND", Rule: "SOMETIMES"}, "rules[0].rule")
	bad(iam.ApprovalRuleInput{Kind: "REFUND", Rule: "DISTINCT_ABOVE"}, "rules[0].threshold")
	bad(iam.ApprovalRuleInput{Kind: "REFUND", Rule: "DISTINCT_ABOVE", Threshold: ptr("-1")}, "rules[0].threshold")
	bad(iam.ApprovalRuleInput{Kind: "REFUND", Rule: "DISTINCT_ABOVE", Threshold: ptr("1.5")}, "rules[0].threshold") // the call is made at 0 decimals
	bad(iam.ApprovalRuleInput{Kind: "REFUND", Rule: "DISTINCT", Threshold: ptr("5")}, "rules[0].threshold")
	if err := w.rules.Set(w.admin, w.prop, 0, []iam.ApprovalRuleInput{{Kind: "REFUND", Rule: "FREE"}, {Kind: "REFUND", Rule: "FREE"}}); !apperr.IsCode(err, "VALIDATION_FAILED") {
		t.Fatalf("a kind named twice: %v", err)
	}
	// property.manage is needed; a grant alone reads
	wantCode(t, w.rules.Set(w.clerk, w.prop, 0, []iam.ApprovalRuleInput{{Kind: "REFUND", Rule: "DISTINCT"}}), "PERMISSION_DENIED")
	if err := w.rules.Set(w.managers, w.prop, 0, []iam.ApprovalRuleInput{{Kind: "REFUND", Rule: "DISTINCT_ABOVE", Threshold: ptr("250000")}}); err != nil {
		t.Fatalf("property.manage: %v", err)
	}
	// the old and the new are audited, and nothing is audited for a change that changes nothing
	count := func() int {
		var n int
		if err := w.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE action = 'property.approval_rule_changed'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("one change, one entry: %d", count())
	}
	w.set(t, "REFUND", "DISTINCT_ABOVE", ptr("250000"))
	if count() != 1 {
		t.Fatalf("the same rule again writes nothing: %d", count())
	}
	w.set(t, "REFUND", "DISTINCT", nil)
	var oldRule, newRule, label string
	if err := w.pool.QueryRow(context.Background(), `SELECT old_data->>'rule' || ':' || coalesce(old_data->>'threshold', ''), new_data->>'rule', entity_label FROM audit_logs
		WHERE action = 'property.approval_rule_changed' ORDER BY id DESC LIMIT 1`).Scan(&oldRule, &newRule, &label); err != nil {
		t.Fatal(err)
	}
	if oldRule != "DISTINCT_ABOVE:250000" || newRule != "DISTINCT" || label != "REFUND" {
		t.Fatalf("the entry has the old and the new rule: %s -> %s (%s)", oldRule, newRule, label)
	}
	// the list says the rule, the limit and how many people besides the administrators can approve
	w.set(t, "FOLIO_ADJUSTMENT", "DISTINCT_ABOVE", ptr("500000"))
	list, err = w.rules.List(w.managers, w.prop, 2)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]iam.ApprovalRuleView{}
	for _, r := range list {
		got[string(r.Kind)] = r
	}
	if r := got["REFUND"]; r.Rule != "DISTINCT" || r.Approvers != 2 || r.Permission != auth.PermCorrectionApprove || !r.HasAmount {
		t.Fatalf("REFUND: %+v", r)
	}
	if r := got["FOLIO_ADJUSTMENT"]; r.Rule != "DISTINCT_ABOVE" || r.Threshold == nil || *r.Threshold != "500000.00" {
		t.Fatalf("FOLIO_ADJUSTMENT: %+v", r)
	}
	if r := got["BANK"]; r.HasAmount || r.Rule != "FREE" {
		t.Fatalf("BANK: %+v", r)
	}
	// and what the dialog reads says the same, without the permission or the people
	inForce, err = w.rules.InForce(w.nobody, w.prop, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range inForce {
		switch r.Kind {
		case "REFUND":
			if r.Rule != "DISTINCT" || r.Threshold != nil {
				t.Fatalf("REFUND in force: %+v", r)
			}
		case "FOLIO_ADJUSTMENT":
			if r.Rule != "DISTINCT_ABOVE" || r.Threshold == nil || *r.Threshold != "500000" {
				t.Fatalf("FOLIO_ADJUSTMENT in force: %+v", r)
			}
		}
	}
}

// Another tenant's property is not found, whoever asks.
func TestApprovalRulesStayInTheTenant(t *testing.T) {
	w := newApprovalWorld(t)
	w.tenantWithAdmin(t, "XYZ", "admin@xyz.com")
	xyz := w.as(t, w.login(t, "XYZ", "admin@xyz.com", pw))
	_, err := w.rules.List(xyz, w.prop, 0)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = w.rules.InForce(xyz, w.prop, 0)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	wantCode(t, w.rules.Set(xyz, w.prop, 0, []iam.ApprovalRuleInput{{Kind: "REFUND", Rule: "DISTINCT"}}), "PROPERTY_NOT_FOUND")
	// a rule of one tenant's property never applies in another's: the rule is read by the property
	w.set(t, "REFUND", "DISTINCT", nil)
	sg := w.property(t, xyz, "SG")
	if _, err := w.iam.Verify(xyz, iam.ApprovalRequest{PropertyID: sg, Input: &iam.ApprovalInput{Email: "admin@xyz.com", Password: pw}, Kind: iam.KindRefund, Amount: amount("1")}); err != nil {
		t.Fatalf("the other tenant has no rule: %v", err)
	}
}
