package inventory_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/departments"
	"kamarapms/internal/iam"
	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The approval chain (35-inventory.md 5.8, I13): levels in order, conditions on the total, one person one level, the requester never approves, the edits of an approver, return and resubmission, the snapshot,
// races between approvers, and who is the head of a department.

// cfx is the purchasing fixture with the people of a chain: a requester, the head and the deputy of the department, cost control and the final approver.
type cfx struct {
	*pfx
	dept                          int64
	req, head, deputy, cc, fa     context.Context
	reqID, headID, deputyID, ccID int64
}

func uid(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	p, err := auth.Require(ctx)
	must(t, err)
	return p.UserID
}

func newChain(t *testing.T) *cfx {
	t.Helper()
	p := newPurchasing(t, inventory.MethodAverage, 0)
	p.settings(t, func(s *inventory.SettingsInput) { s.PoRequiresPr = true })
	c := &cfx{pfx: p}
	list, err := p.Departments.List(p.admin, p.propID, departments.Filter{})
	must(t, err)
	for _, d := range list {
		if d.Code == "FB" {
			c.dept = d.ID
		}
	}
	c.req = p.User(t, p.tenantID, p.propID, auth.PermInventoryRequest, auth.PermInventoryPurchase, auth.PermInventoryReceive)
	c.head = p.User(t, p.tenantID, p.propID, auth.PermInventoryDeptApprove)
	c.deputy = p.User(t, p.tenantID, p.propID, auth.PermInventoryDeptApprove)
	c.cc = p.User(t, p.tenantID, p.propID, auth.PermInventoryCostControl)
	c.fa = p.User(t, p.tenantID, p.propID, auth.PermInventoryFinalApprove)
	c.reqID, c.headID, c.deputyID, c.ccID = uid(t, c.req), uid(t, c.head), uid(t, c.deputy), uid(t, c.cc)
	c.setHeads(t, c.headID, c.deputyID)
	return c
}

func (c *cfx) setHeads(t *testing.T, head, deputy int64) {
	t.Helper()
	_, err := c.Departments.Update(c.admin, c.propID, c.dept, departments.Patch{HeadUserID: &head, DeputyUserID: &deputy})
	must(t, err)
}

// request writes a request of the department for the lines (item, unit, qty, price) and returns it as a draft.
func (c *cfx) request(t *testing.T, ctx context.Context, lines ...inventory.RequestLineInput) inventory.Request {
	t.Helper()
	r, err := c.svc.CreateRequest(ctx, c.propID, inventory.RequestInput{ClientUUID: c.id(), DepartmentID: &c.dept, WarehouseID: ptr(c.wh["MAIN"]), Lines: lines})
	must(t, err)
	return r
}

func (c *cfx) line(item, unit, qty, price string) inventory.RequestLineInput {
	return inventory.RequestLineInput{ItemID: c.item[item], UnitID: c.unit[unit], Qty: dec(qty), EstUnitPrice: ptr(dec(price))}
}

func (c *cfx) submit(t *testing.T, ctx context.Context, r inventory.Request) inventory.Request {
	t.Helper()
	out, err := c.svc.SubmitRequest(ctx, c.propID, r.ID)
	must(t, err)
	return out
}

func (c *cfx) approve(t *testing.T, ctx context.Context, r inventory.Request, level int, edits ...inventory.ApproveEdit) inventory.Request {
	t.Helper()
	out, err := c.svc.ApproveRequest(ctx, c.propID, r.ID, inventory.ApproveInput{LevelNo: &level, Edits: edits})
	must(t, err)
	return out
}

func active(r inventory.Request) int {
	for _, s := range r.Steps {
		if s.Cycle == r.Cycle && s.Status == "ACTIVE" {
			return s.LevelNo
		}
	}
	return 0
}

func stepStatus(r inventory.Request, level int) string {
	for _, s := range r.Steps {
		if s.Cycle == r.Cycle && s.LevelNo == level {
			return s.Status
		}
	}
	return ""
}

// 8.9: 3,880,000 stops at level 2, 6,450,000 goes to level 3.
func TestTheConditionOfALevelIsReadOnTheTotalWhenTheLevelIsReached(t *testing.T) {
	c := newChain(t)
	small := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "8", "485000"))) // 3,880,000
	if small.Status != "SUBMITTED" || active(small) != 1 || small.Stage == nil || small.Stage.Total != 2 {
		t.Fatalf("submitted: %s level %d stage %+v", small.Status, active(small), small.Stage)
	}
	small = c.approve(t, c.head, small, 1)
	if active(small) != 2 {
		t.Fatalf("level %d", active(small))
	}
	small = c.approve(t, c.cc, small, 2)
	if small.Status != "APPROVED" || stepStatus(small, 3) != "SKIPPED" {
		t.Fatalf("%s, level 3 %s", small.Status, stepStatus(small, 3))
	}
	big := c.submit(t, c.req, c.request(t, c.req, c.line("SIRUP", "karton", "10", "645000"))) // 6,450,000
	big = c.approve(t, c.head, big, 1)
	big = c.approve(t, c.cc, big, 2)
	if big.Status != "SUBMITTED" || active(big) != 3 {
		t.Fatalf("%s level %d", big.Status, active(big))
	}
	big = c.approve(t, c.fa, big, 3)
	if big.Status != "APPROVED" {
		t.Fatal(big.Status)
	}
	// the requester heard of it, and each level heard when it was its turn
	n, err := c.svc.Notices(c.req, c.propID)
	must(t, err)
	if n.Unseen != 2 || n.Data[0].Kind != "APPROVED" {
		t.Fatalf("requester's notices: %+v", n)
	}
	n, _ = c.svc.Notices(c.fa, c.propID)
	if len(n.Data) != 1 || n.Data[0].Kind != "APPROVAL_WAITING" || n.Data[0].SeenAt == nil || n.Unseen != 0 {
		t.Fatalf("final approver's notices (closed when the level was decided): %+v", n)
	}
	// a notice that nobody has decided is unseen until it is marked seen
	pending := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	_ = pending
	n, _ = c.svc.Notices(c.head, c.propID)
	if n.Unseen != 1 {
		t.Fatalf("head's notices: %+v", n)
	}
	must(t, c.svc.MarkNoticeSeen(c.head, c.propID, n.Data[0].ID))
	n, _ = c.svc.Notices(c.head, c.propID)
	if n.Unseen != 0 {
		t.Fatal("not seen")
	}
}

func TestALevelDecidesOnlyWhenItIsTheActiveOneAndOnlyByWhoIsEligibleForIt(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	two := 2
	_, err := c.svc.ApproveRequest(c.cc, c.propID, r.ID, inventory.ApproveInput{LevelNo: &two})
	wantCode(t, err, "APPROVAL_NOT_YOUR_TURN")
	_, err = c.svc.ApproveRequest(c.cc, c.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "APPROVAL_NOT_ELIGIBLE")
	_, err = c.svc.ApproveRequest(c.fa, c.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "APPROVAL_NOT_ELIGIBLE")
	nobody := c.User(t, c.tenantID, c.propID, auth.PermInventoryView)
	_, err = c.svc.ApproveRequest(nobody, c.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	// a head who is not a holder of inventory.dept_approve is no approver
	plain := c.User(t, c.tenantID, c.propID, auth.PermInventoryRequest, auth.PermInventoryDeptApprove)
	_ = plain
	// the inbox lists what a user can decide now
	in, err := c.svc.Inbox(c.head, c.propID)
	must(t, err)
	if len(in) != 1 || in[0].RequestID != r.ID || in[0].LevelNo != 1 {
		t.Fatalf("head's inbox: %+v", in)
	}
	for name, ctx := range map[string]context.Context{"cost control": c.cc, "requester": c.req} {
		got, err := c.svc.Inbox(ctx, c.propID)
		must(t, err)
		if len(got) != 0 {
			t.Fatalf("%s's inbox: %+v", name, got)
		}
	}
	c.approve(t, c.head, r, 1)
	in, _ = c.svc.Inbox(c.head, c.propID)
	if len(in) != 0 {
		t.Fatal("the head still waits")
	}
	in, _ = c.svc.Inbox(c.cc, c.propID)
	if len(in) != 1 || in[0].LevelNo != 2 {
		t.Fatalf("cost control's inbox: %+v", in)
	}
}

func TestOnePersonDecidesOneLevelOfARound(t *testing.T) {
	c := newChain(t)
	both := c.User(t, c.tenantID, c.propID, auth.PermInventoryCostControl, auth.PermInventoryFinalApprove)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("SIRUP", "karton", "10", "645000")))
	c.approve(t, c.head, r, 1)
	r = c.approve(t, both, r, 2)
	if active(r) != 3 {
		t.Fatalf("level %d", active(r))
	}
	_, err := c.svc.ApproveRequest(both, c.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "APPROVAL_SAME_PERSON")
	// the database holds it too: a second approved step of the same user in the round is refused
	err = c.Exec(t, `UPDATE inv_approval_steps SET status = 'APPROVED', decided_by = $2, decided_at = now(), total_seen = 0 WHERE request_id = $1 AND level_no = 3`, r.ID, uid(t, both))
	if err == nil {
		t.Fatal("the unique index let one person decide two levels")
	}
}

func TestTheRequesterNeverApprovesTheirOwnRequestNotEvenTheAdministrator(t *testing.T) {
	c := newChain(t)
	adminCtx, _ := c.AdminAccount(t, c.tenantID)
	r, err := c.svc.CreateRequest(adminCtx, c.propID, inventory.RequestInput{ClientUUID: c.id(), DepartmentID: &c.dept, Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "1", "485000")}})
	must(t, err)
	r, err = c.svc.SubmitRequest(adminCtx, c.propID, r.ID)
	must(t, err)
	r = c.approve(t, c.head, r, 1)
	_, err = c.svc.ApproveRequest(adminCtx, c.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "APPROVAL_OWN_DOCUMENT")
	_, err = c.svc.ReturnRequest(adminCtx, c.propID, r.ID, inventory.ReturnRequestInput{Note: "no"})
	wantCode(t, err, "APPROVAL_OWN_DOCUMENT")
	in, err := c.svc.Inbox(adminCtx, c.propID)
	must(t, err)
	if len(in) != 0 {
		t.Fatal("the administrator has their own request in the inbox")
	}
	// another administrator may
	other, _ := c.AdminAccount(t, c.tenantID)
	c.approve(t, other, r, 2)
}

func TestTheFirstLevelIsSkippedWhenTheRequesterIsTheHeadUnlessTheOptionIsOff(t *testing.T) {
	c := newChain(t)
	c.setHeads(t, c.reqID, c.deputyID) // the requester leads the department
	// the requester must hold the permission of a head
	must(t, c.Exec(t, `INSERT INTO role_permissions (role_id, permission_code) SELECT up.role_id, 'inventory.dept_approve' FROM user_properties up WHERE up.user_id = $1`, c.reqID))
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	if stepStatus(r, 1) != "SKIPPED" || active(r) != 2 {
		t.Fatalf("level 1 %s, active %d", stepStatus(r, 1), active(r))
	}
	var reason string
	for _, s := range r.Steps {
		if s.LevelNo == 1 && s.SkipReason != nil {
			reason = *s.SkipReason
		}
	}
	if reason != "REQUESTER_IS_HEAD" {
		t.Fatalf("reason %q", reason)
	}
	// with the option off the other of the two decides level 1 (the requester is out by the rule of the requester)
	chain, err := c.svc.GetApprovalChain(c.admin, c.propID, "PR")
	must(t, err)
	_, err = c.svc.SetApprovalChain(c.admin, c.propID, "PR", inventory.ApprovalChainInput{SkipLevel1IfHead: false, ApproverMayEdit: chain.ApproverMayEdit, Version: chain.Version, Levels: chain.Levels})
	must(t, err)
	r2 := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "2", "485000")))
	if active(r2) != 1 {
		t.Fatalf("level %d", active(r2))
	}
	c.approve(t, c.deputy, r2, 1)
	// a department with nobody else to decide refuses the submission
	c.setHeads(t, c.reqID, 0)
	_, err = c.svc.SubmitRequest(c.req, c.propID, c.request(t, c.req, c.line("BERAS", "karung", "3", "485000")).ID)
	wantCode(t, err, "APPROVAL_CHAIN_UNSATISFIABLE")
}

func TestADepartmentWithNoHeadMakesTheChainUnsatisfiableAndASubDepartmentTakesItsParents(t *testing.T) {
	c := newChain(t)
	c.setHeads(t, 0, 0)
	r := c.request(t, c.req, c.line("BERAS", "karung", "1", "485000"))
	_, err := c.svc.SubmitRequest(c.req, c.propID, r.ID)
	wantCode(t, err, "APPROVAL_CHAIN_UNSATISFIABLE")
	c.setHeads(t, c.headID, 0)
	sub, err := c.Departments.Create(c.admin, c.propID, departments.Input{Code: "FBK", Name: "Kitchen", ParentID: &c.dept})
	must(t, err)
	sr, err := c.svc.CreateRequest(c.req, c.propID, inventory.RequestInput{ClientUUID: c.id(), DepartmentID: &sub.ID, Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "1", "485000")}})
	must(t, err)
	sr, err = c.svc.SubmitRequest(c.req, c.propID, sr.ID)
	must(t, err)
	c.approve(t, c.head, sr, 1) // the head of the parent decides for the sub-department
	// a head who lost the permission is nobody: the chain cannot be satisfied
	must(t, c.Exec(t, `DELETE FROM role_permissions WHERE permission_code = 'inventory.dept_approve' AND role_id IN (SELECT role_id FROM user_properties WHERE user_id = $1)`, c.headID))
	_, err = c.svc.SubmitRequest(c.req, c.propID, r.ID)
	wantCode(t, err, "APPROVAL_CHAIN_UNSATISFIABLE")
}

func TestAnApproverMayChangeALineAndLaterLevelsReadTheNewTotal(t *testing.T) {
	c := newChain(t)
	// 4,200,000: level 3 does not apply; a price edit by cost control to 5,400,000 adds it
	r := c.submit(t, c.req, c.request(t, c.req, c.line("SIRUP", "karton", "7", "600000")))
	r = c.approve(t, c.head, r, 1)
	r = c.approve(t, c.cc, r, 2, inventory.ApproveEdit{LineNo: 1, EstUnitPrice: ptr(dec("780000"))})
	if !r.EstTotal.Equal(dec("5460000")) || r.Status != "SUBMITTED" || active(r) != 3 {
		t.Fatalf("total %s %s level %d", r.EstTotal, r.Status, active(r))
	}
	if len(r.Edits) != 1 || r.Edits[0].Field != "EST_UNIT_PRICE" || !r.Edits[0].OldValue.Equal(dec("600000")) || !r.Edits[0].NewValue.Equal(dec("780000")) || r.Edits[0].LevelNo != 2 {
		t.Fatalf("edits: %+v", r.Edits)
	}
	r = c.approve(t, c.fa, r, 3, inventory.ApproveEdit{LineNo: 1, Qty: ptr(dec("6"))})
	if r.Status != "APPROVED" || !r.EstTotal.Equal(dec("4680000")) || !r.Lines[0].QtyStock.Equal(dec("72")) {
		t.Fatalf("%s %s %s", r.Status, r.EstTotal, r.Lines[0].QtyStock)
	}
	// the other direction: 6,000,000 needs level 3; the head lowers it to 4,800,000 and level 3 is dropped
	r2 := c.submit(t, c.req, c.request(t, c.req, c.line("SIRUP", "karton", "10", "600000")))
	r2 = c.approve(t, c.head, r2, 1, inventory.ApproveEdit{LineNo: 1, Qty: ptr(dec("8"))})
	r2 = c.approve(t, c.cc, r2, 2)
	if r2.Status != "APPROVED" || stepStatus(r2, 3) != "SKIPPED" {
		t.Fatalf("%s, level 3 %s", r2.Status, stepStatus(r2, 3))
	}
	// the options: an approver cannot change a line when the property does not allow it, nor to a quantity of zero or a negative price
	chain, _ := c.svc.GetApprovalChain(c.admin, c.propID, "PR")
	r3 := c.submit(t, c.req, c.request(t, c.req, c.line("SIRUP", "karton", "1", "600000")))
	two := 1
	_, err := c.svc.ApproveRequest(c.head, c.propID, r3.ID, inventory.ApproveInput{LevelNo: &two, Edits: []inventory.ApproveEdit{{LineNo: 1, Qty: ptr(dec("0"))}}})
	wantCode(t, err, "APPROVAL_EDIT_NOT_ALLOWED")
	_, err = c.svc.ApproveRequest(c.head, c.propID, r3.ID, inventory.ApproveInput{LevelNo: &two, Edits: []inventory.ApproveEdit{{LineNo: 1, EstUnitPrice: ptr(dec("-1"))}}})
	wantCode(t, err, "APPROVAL_EDIT_NOT_ALLOWED")
	_, err = c.svc.SetApprovalChain(c.admin, c.propID, "PR", inventory.ApprovalChainInput{SkipLevel1IfHead: chain.SkipLevel1IfHead, ApproverMayEdit: false, Version: chain.Version, Levels: chain.Levels})
	must(t, err)
	r4 := c.submit(t, c.req, c.request(t, c.req, c.line("SIRUP", "karton", "1", "600000")))
	_, err = c.svc.ApproveRequest(c.head, c.propID, r4.ID, inventory.ApproveInput{LevelNo: &two, Edits: []inventory.ApproveEdit{{LineNo: 1, Qty: ptr(dec("2"))}}})
	wantCode(t, err, "APPROVAL_EDIT_NOT_ALLOWED")
	// the first request, submitted before the option was switched off, still lets its approvers edit (its snapshot)
	r5, err := c.svc.GetRequest(c.admin, c.propID, r3.ID)
	must(t, err)
	if !r5.Steps[0].EditAllowed {
		t.Fatal("the snapshot of a request in flight changed")
	}
}

func TestAReturnedRequestStartsANewRoundFromTheFirstLevelWithTheChainAsItIsThen(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "8", "485000")))
	c.approve(t, c.head, r, 1)
	_, err := c.svc.ReturnRequest(c.cc, c.propID, r.ID, inventory.ReturnRequestInput{})
	wantCode(t, err, "VALIDATION_FAILED") // a return needs a note
	r, err = c.svc.ReturnRequest(c.cc, c.propID, r.ID, inventory.ReturnRequestInput{Note: "too much rice"})
	must(t, err)
	if r.Status != "RETURNED" || r.ReturnedNote == nil || stepStatus(r, 2) != "RETURNED" {
		t.Fatalf("%s %v %s", r.Status, r.ReturnedNote, stepStatus(r, 2))
	}
	n, _ := c.svc.Notices(c.req, c.propID)
	if n.Data[0].Kind != "RETURNED" {
		t.Fatalf("%+v", n.Data[0])
	}
	// the requester changes it and submits again: round 2, from level 1; the same people can decide again
	r, err = c.svc.UpdateRequest(c.req, c.propID, r.ID, inventory.RequestInput{DepartmentID: &c.dept, WarehouseID: ptr(c.wh["MAIN"]), Version: r.Version,
		Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "4", "485000")}})
	must(t, err)
	// the chain changes before the resubmission: a level is dropped
	chain, _ := c.svc.GetApprovalChain(c.admin, c.propID, "PR")
	_, err = c.svc.SetApprovalChain(c.admin, c.propID, "PR", inventory.ApprovalChainInput{SkipLevel1IfHead: true, ApproverMayEdit: true, Version: chain.Version, Levels: chain.Levels[:2]})
	must(t, err)
	r = c.submit(t, c.req, r)
	if r.Cycle != 2 || active(r) != 1 {
		t.Fatalf("round %d level %d", r.Cycle, active(r))
	}
	r = c.approve(t, c.head, r, 1)
	r = c.approve(t, c.cc, r, 2)
	if r.Status != "APPROVED" {
		t.Fatal(r.Status)
	}
	rounds := map[int]int{}
	for _, s := range r.Steps {
		rounds[s.Cycle]++
	}
	if rounds[1] != 3 || rounds[2] != 2 {
		t.Fatalf("the steps of round 1 are kept and round 2 has the chain as it was then: %v", rounds)
	}
}

func TestARequestIsRecalledByItsRequesterBeforeItIsApproved(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	_, err := c.svc.RecallRequest(c.head, c.propID, r.ID)
	wantCode(t, err, "PERMISSION_DENIED")
	other := c.User(t, c.tenantID, c.propID, auth.PermInventoryRequest)
	_, err = c.svc.RecallRequest(other, c.propID, r.ID)
	wantCode(t, err, "NOT_THE_REQUESTER")
	r, err = c.svc.RecallRequest(c.req, c.propID, r.ID)
	must(t, err)
	if r.Status != "DRAFT" || stepStatus(r, 1) != "NOT_REACHED" {
		t.Fatalf("%s %s", r.Status, stepStatus(r, 1))
	}
	in, _ := c.svc.Inbox(c.head, c.propID)
	if len(in) != 0 {
		t.Fatal("the recalled request is still waiting for the head")
	}
	_, err = c.svc.ApproveRequest(c.head, c.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "APPROVAL_NOT_IN_PROGRESS")
	r = c.submit(t, c.req, r)
	if r.Cycle != 2 {
		t.Fatalf("a recalled and resubmitted request starts a new round: %d", r.Cycle)
	}
}

func TestAChangeOfTheChainNeverTouchesARequestInFlightAndTheNextOneUsesIt(t *testing.T) {
	c := newChain(t)
	inFlight := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	chain, _ := c.svc.GetApprovalChain(c.admin, c.propID, "PR")
	renamed := append([]inventory.ApprovalLevel{}, chain.Levels...)
	renamed[1].Name = "Controller"
	renamed[2].Threshold = ptr(dec("1000"))
	_, err := c.svc.SetApprovalChain(c.admin, c.propID, "PR", inventory.ApprovalChainInput{SkipLevel1IfHead: true, ApproverMayEdit: true, Version: chain.Version, Levels: renamed})
	must(t, err)
	got, _ := c.svc.GetRequest(c.admin, c.propID, inFlight.ID)
	if got.Steps[1].Name != "Cost control" || got.Steps[2].Threshold == nil || !got.Steps[2].Threshold.Equal(dec("5000000")) {
		t.Fatalf("the steps of a request in flight changed: %+v", got.Steps)
	}
	next := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "2", "485000"))) // 970,000 is above the new threshold of 1,000? no: below
	if next.Steps[1].Name != "Controller" {
		t.Fatalf("the next request did not take the new chain: %+v", next.Steps)
	}
	// the version counts: a stale chain is refused
	_, err = c.svc.SetApprovalChain(c.admin, c.propID, "PR", inventory.ApprovalChainInput{Version: chain.Version, Levels: renamed})
	wantCode(t, err, "VERSION_CONFLICT")
}

func TestTheChainIsEditedByWhoManagesThePropertyAndIsValidated(t *testing.T) {
	c := newChain(t)
	chain, err := c.svc.GetApprovalChain(c.admin, c.propID, "PR")
	must(t, err)
	if len(chain.Levels) != 3 || chain.Levels[0].ApproverKind != "DEPT_HEAD" || chain.Levels[2].Threshold == nil || !chain.Levels[2].Threshold.Equal(dec("5000000")) || !chain.SkipLevel1IfHead || !chain.ApproverMayEdit {
		t.Fatalf("the default chain: %+v", chain)
	}
	sr, err := c.svc.GetApprovalChain(c.admin, c.propID, "SR")
	must(t, err)
	if len(sr.Levels) != 2 || sr.Levels[1].Permission == nil || *sr.Levels[1].Permission != "inventory.sr_final_approve" {
		t.Fatalf("the default chain of a requisition: %+v", sr)
	}
	_, err = c.svc.GetApprovalChain(c.admin, c.propID, "XX")
	wantCode(t, err, "APPROVAL_CHAIN_NOT_FOUND")
	manager := c.User(t, c.tenantID, c.propID, auth.PermPropertyManage)
	in := inventory.ApprovalChainInput{SkipLevel1IfHead: true, ApproverMayEdit: true, Version: chain.Version, Levels: chain.Levels}
	if _, err := c.svc.SetApprovalChain(c.req, c.propID, "PR", in); err == nil {
		t.Fatal("a requester edited the chain")
	}
	for name, lv := range map[string][]inventory.ApprovalLevel{
		"six levels":                append(append([]inventory.ApprovalLevel{}, chain.Levels...), chain.Levels...),
		"a permission off the list": {{Name: "X", ApproverKind: "PERMISSION", Permission: ptr("folio.read"), ConditionKind: "ALWAYS"}},
		"the sr permission on a pr": {{Name: "X", ApproverKind: "PERMISSION", Permission: ptr("inventory.sr_final_approve"), ConditionKind: "ALWAYS"}},
		"no name":                   {{Name: "", ApproverKind: "DEPT_HEAD", ConditionKind: "ALWAYS"}},
		"a threshold on always":     {{Name: "X", ApproverKind: "DEPT_HEAD", ConditionKind: "ALWAYS", Threshold: ptr(dec("1"))}},
		"above without amount":      {{Name: "X", ApproverKind: "DEPT_HEAD", ConditionKind: "TOTAL_ABOVE"}},
		"a head with a permission":  {{Name: "X", ApproverKind: "DEPT_HEAD", Permission: ptr("inventory.cost_control"), ConditionKind: "ALWAYS"}},
	} {
		bad := in
		bad.Levels = lv
		_, err := c.svc.SetApprovalChain(manager, c.propID, "PR", bad)
		if e := err; e == nil {
			t.Fatalf("%s: accepted", name)
		}
		wantCode(t, err, "APPROVAL_CHAIN_INVALID")
	}
	// a chain with no level approves at the submission
	in.Levels = nil
	_, err = c.svc.SetApprovalChain(manager, c.propID, "PR", in)
	must(t, err)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	if r.Status != "APPROVED" {
		t.Fatalf("a request with no level to pass: %s", r.Status)
	}
	if n := c.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'inventory.approval_chain_changed'`); n < 1 {
		t.Fatalf("%d audit entries of a chain", n)
	}
}

func TestTwoEligibleApproversDecidingTheSameLevelAtOnceMakeOneDecision(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, who := range []context.Context{c.head, c.deputy} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			one := 1
			_, results[i] = c.svc.ApproveRequest(who, c.propID, r.ID, inventory.ApproveInput{LevelNo: &one})
		}()
	}
	close(start)
	wg.Wait()
	ok, turn := 0, 0
	for _, e := range results {
		if e == nil {
			ok++
		} else if code, _ := apperrOf(e); code == "APPROVAL_NOT_YOUR_TURN" {
			turn++
		}
	}
	if ok != 1 || turn != 1 {
		t.Fatalf("results: %v", results)
	}
	got, _ := c.svc.GetRequest(c.admin, c.propID, r.ID)
	approved := 0
	for _, s := range got.Steps {
		if s.Status == "APPROVED" {
			approved++
		}
	}
	if approved != 1 || active(got) != 2 {
		t.Fatalf("approved steps %d, level %d", approved, active(got))
	}
}

func TestAnApproverAndTheRecallOfTheRequesterAtOnceLeaveOneOutcome(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	start := make(chan struct{})
	var wg sync.WaitGroup
	var approveErr, recallErr error
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, approveErr = c.svc.ApproveRequest(c.head, c.propID, r.ID, inventory.ApproveInput{})
	}()
	go func() {
		defer wg.Done()
		<-start
		_, recallErr = c.svc.RecallRequest(c.req, c.propID, r.ID)
	}()
	close(start)
	wg.Wait()
	got, _ := c.svc.GetRequest(c.admin, c.propID, r.ID)
	switch {
	case approveErr == nil && recallErr != nil: // approved first, the recall found a request that is not at the first level any more: it is still submitted (the second level), so it can be recalled too
		t.Fatalf("approve won but the recall failed: %v", recallErr)
	case approveErr != nil && recallErr == nil:
		if got.Status != "DRAFT" {
			t.Fatalf("recalled: %s", got.Status)
		}
	case approveErr == nil && recallErr == nil:
		if got.Status != "DRAFT" { // approved, then recalled at the second level
			t.Fatalf("both worked and the request is %s", got.Status)
		}
	default:
		t.Fatalf("both failed: %v and %v", approveErr, recallErr)
	}
}

func TestADecisionIsSafeToRepeat(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	c.approve(t, c.head, r, 1)
	again := c.approve(t, c.head, r, 1) // the same decision of the same level answers the request as it is
	if active(again) != 2 {
		t.Fatalf("level %d", active(again))
	}
	approved := 0
	for _, s := range again.Steps {
		if s.Status == "APPROVED" {
			approved++
		}
	}
	if approved != 1 {
		t.Fatalf("%d approved steps", approved)
	}
}

func TestAnApprovedRequestBecomesOrdersOneForEachSupplierAndNeverPassesWhatWasApproved(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "3", "485000"), c.line("SIRUP", "karton", "5", "600000")))
	one := func(u inventory.POFromRequestInput) error {
		_, err := c.svc.CreateOrdersFromRequest(c.req, c.propID, r.ID, u)
		return err
	}
	wantCode(t, one(inventory.POFromRequestInput{Orders: []inventory.POFromRequestOrder{{ClientUUID: c.id(), SupplierID: c.sup, Lines: []inventory.POFromRequestLine{{LineNo: 1, UnitPrice: dec("485000")}}}}}), "PR_NOT_APPROVED")
	r = c.approve(t, c.head, r, 1)
	r = c.approve(t, c.cc, r, 2)
	if r.Status != "APPROVED" {
		t.Fatal(r.Status)
	}
	// every line is on exactly one order, and none above what was approved
	wantCode(t, one(inventory.POFromRequestInput{Orders: []inventory.POFromRequestOrder{{ClientUUID: c.id(), SupplierID: c.sup, Lines: []inventory.POFromRequestLine{{LineNo: 1, UnitPrice: dec("485000")}}}}}), "VALIDATION_FAILED")
	wantCode(t, one(inventory.POFromRequestInput{Orders: []inventory.POFromRequestOrder{{ClientUUID: c.id(), SupplierID: c.sup, Lines: []inventory.POFromRequestLine{
		{LineNo: 1, Qty: ptr(dec("4")), UnitPrice: dec("485000")}, {LineNo: 2, UnitPrice: dec("600000")}}}}}), "VALIDATION_FAILED")
	in := inventory.POFromRequestInput{Send: true, Orders: []inventory.POFromRequestOrder{
		{ClientUUID: c.id(), SupplierID: c.sup, Lines: []inventory.POFromRequestLine{{LineNo: 1, Qty: ptr(dec("2")), UnitPrice: dec("490000")}}},
		{ClientUUID: c.id(), SupplierID: c.pasar, Lines: []inventory.POFromRequestLine{{LineNo: 2, UnitPrice: dec("590000")}}}}}
	pos, err := c.svc.CreateOrdersFromRequest(c.req, c.propID, r.ID, in)
	if e, ok := apperr.As(err); ok && len(e.Fields) > 0 {
		t.Fatalf("%v", e.Fields)
	}
	must(t, err)
	if len(pos) != 2 || pos[0].Status != "ORDERED" || !pos[0].Total.Equal(dec("980000")) || !pos[1].Total.Equal(dec("2950000")) || pos[0].RequestNumber == nil {
		t.Fatalf("%+v", pos)
	}
	got, _ := c.svc.GetRequest(c.admin, c.propID, r.ID)
	if got.Status != "CONVERTED" || len(got.Orders) != 2 || got.Lines[0].PONumber == nil {
		t.Fatalf("%s %d orders", got.Status, len(got.Orders))
	}
	// the same request answers the orders it made
	again, err := c.svc.CreateOrdersFromRequest(c.req, c.propID, r.ID, in)
	must(t, err)
	if again[0].ID != pos[0].ID {
		t.Fatal("the repeat made other orders")
	}
	// receive against an order from a request
	rc, err := c.svc.PostReceipt(c.req, c.propID, inventory.ReceiptInput{ClientUUID: c.id(), POID: &pos[1].ID, Lines: []inventory.ReceiptLineInput{{POLineNo: ptr(1), Qty: dec("3")}}})
	must(t, err)
	if !rc.TotalValue.Equal(dec("1770000")) {
		t.Fatal(rc.TotalValue)
	}
	// cancelling the orders gives the request back
	_, err = c.svc.CancelPurchaseOrder(c.req, c.propID, pos[0].ID)
	must(t, err)
	_, err = c.svc.CancelPurchaseOrder(c.req, c.propID, pos[1].ID)
	wantCode(t, err, "PO_NOT_ORDERABLE") // goods came in against it
	got, _ = c.svc.GetRequest(c.admin, c.propID, r.ID)
	if got.Status != "CONVERTED" {
		t.Fatalf("one order still stands: %s", got.Status)
	}
	_, err = c.svc.ClosePurchaseOrder(c.req, c.propID, pos[1].ID)
	must(t, err)
	// a direct order is refused while the property orders from requests (D13)
	_, err = c.svc.CreatePurchaseOrder(c.req, c.propID, inventory.POInput{ClientUUID: c.id(), SupplierID: c.sup, WarehouseID: c.wh["MAIN"],
		Lines: []inventory.POLineInput{{ItemID: c.item["SIRUP"], UnitID: c.unit["karton"], Qty: dec("1"), UnitPrice: dec("600000")}}})
	wantCode(t, err, "PO_REQUIRES_REQUEST")
}

func TestACancelledOrderFreesTheLinesOfItsRequest(t *testing.T) {
	c := newChain(t)
	r := c.submit(t, c.req, c.request(t, c.req, c.line("BERAS", "karung", "1", "485000")))
	r = c.approve(t, c.head, r, 1)
	r = c.approve(t, c.cc, r, 2)
	pos, err := c.svc.CreateOrdersFromRequest(c.req, c.propID, r.ID, inventory.POFromRequestInput{Orders: []inventory.POFromRequestOrder{{ClientUUID: c.id(), SupplierID: c.sup,
		Lines: []inventory.POFromRequestLine{{LineNo: 1, UnitPrice: dec("485000")}}}}})
	must(t, err)
	if pos[0].Status != "DRAFT" {
		t.Fatal(pos[0].Status)
	}
	_, err = c.svc.CancelPurchaseOrder(c.req, c.propID, pos[0].ID)
	must(t, err)
	got, _ := c.svc.GetRequest(c.admin, c.propID, r.ID)
	if got.Status != "APPROVED" {
		t.Fatalf("the request after its order was cancelled: %s", got.Status)
	}
	if _, err := c.svc.CreateOrdersFromRequest(c.req, c.propID, r.ID, inventory.POFromRequestInput{Orders: []inventory.POFromRequestOrder{{ClientUUID: c.id(), SupplierID: c.pasar,
		Lines: []inventory.POFromRequestLine{{LineNo: 1, UnitPrice: dec("480000")}}}}}); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheRequesterChangesARequestAndOnlyWhileItIsADraftOrReturned(t *testing.T) {
	c := newChain(t)
	r := c.request(t, c.req, c.line("BERAS", "karung", "1", "485000"))
	other := c.User(t, c.tenantID, c.propID, auth.PermInventoryRequest)
	_, err := c.svc.UpdateRequest(other, c.propID, r.ID, inventory.RequestInput{Version: r.Version, Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "2", "485000")}})
	wantCode(t, err, "NOT_THE_REQUESTER")
	_, err = c.svc.UpdateRequest(c.req, c.propID, r.ID, inventory.RequestInput{Version: r.Version + 5, DepartmentID: &c.dept, Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "2", "485000")}})
	wantCode(t, err, "VERSION_CONFLICT")
	_, err = c.svc.SubmitRequest(c.req, c.propID, r.ID)
	must(t, err)
	_, err = c.svc.UpdateRequest(c.req, c.propID, r.ID, inventory.RequestInput{Version: r.Version, DepartmentID: &c.dept, Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "2", "485000")}})
	wantCode(t, err, "REQUEST_STATE_INVALID")
	_, err = c.svc.CancelRequest(c.req, c.propID, r.ID)
	wantCode(t, err, "REQUEST_STATE_INVALID")
	// a draft needs its department to be submitted, and a line needs an item once
	nodept, err := c.svc.CreateRequest(c.req, c.propID, inventory.RequestInput{ClientUUID: c.id(), Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "1", "485000")}})
	must(t, err)
	_, err = c.svc.SubmitRequest(c.req, c.propID, nodept.ID)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = c.svc.CreateRequest(c.req, c.propID, inventory.RequestInput{ClientUUID: c.id(), Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "1", "1"), c.line("BERAS", "karung", "2", "1")}})
	wantCode(t, err, "VALIDATION_FAILED")
	// a repeated creation answers the first
	in := inventory.RequestInput{ClientUUID: c.id(), DepartmentID: &c.dept, Lines: []inventory.RequestLineInput{c.line("BERAS", "karung", "1", "485000")}}
	a, _ := c.svc.CreateRequest(c.req, c.propID, in)
	b, _ := c.svc.CreateRequest(c.req, c.propID, in)
	if a.ID != b.ID {
		t.Fatal("the repeat made another request")
	}
	cancelled, err := c.svc.CancelRequest(c.req, c.propID, a.ID)
	must(t, err)
	if cancelled.Status != "CANCELLED" {
		t.Fatal(cancelled.Status)
	}
}

func TestTheHeadOfADepartmentIsAnActiveUserOfTheProperty(t *testing.T) {
	c := newChain(t)
	stranger := c.Tenant(t, "OTHER")
	op := c.PropertyIn(t, stranger.ID, "OT", "IDR", 0)
	foreign := uid(t, c.User(t, stranger.ID, op.ID, auth.PermInventoryDeptApprove))
	must(t, c.Exec(t, `INSERT INTO users (tenant_id, email, password_hash, full_name, is_active) VALUES ($1, 'off@hotel.com', 'x', 'Off', false)`, c.tenantID))
	var off int64
	must(t, c.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = 'off@hotel.com'`).Scan(&off))
	other := c.PropertyIn(t, c.tenantID, "B2", "IDR", 0)
	noAccess := uid(t, c.User(t, c.tenantID, other.ID, auth.PermInventoryDeptApprove)) // a user of the tenant who has no access to this property
	for name, id := range map[string]int64{"another tenant": foreign, "inactive": off, "no access to the property": noAccess} {
		_, err := c.Departments.Update(c.admin, c.propID, c.dept, departments.Patch{HeadUserID: &id})
		if err == nil {
			t.Fatalf("%s: accepted", name)
		}
		wantCode(t, err, "VALIDATION_FAILED")
	}
	zero := int64(0)
	_, err := c.Departments.Update(c.admin, c.propID, c.dept, departments.Patch{HeadUserID: &zero})
	wantCode(t, err, "VALIDATION_FAILED") // the deputy stays and needs a head
	same := c.headID
	_, err = c.Departments.Update(c.admin, c.propID, c.dept, departments.Patch{DeputyUserID: &same})
	wantCode(t, err, "VALIDATION_FAILED")
	// only who manages accounting changes it, and every change is audited
	_, err = c.Departments.Update(c.req, c.propID, c.dept, departments.Patch{HeadUserID: &c.reqID})
	wantCode(t, err, "PERMISSION_DENIED")
	if n := c.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'department.head_changed'`); n != 1 {
		t.Fatalf("%d audit entries", n)
	}
	list, _ := c.Departments.List(c.admin, c.propID, departments.Filter{})
	for _, d := range list {
		if d.ID == c.dept && (d.HeadUserID == nil || *d.HeadUserID != c.headID || d.DeputyName == "") {
			t.Fatalf("%+v", d)
		}
	}
}

func apperrOf(err error) (string, bool) {
	e, ok := apperr.As(err)
	if !ok {
		return "", false
	}
	return e.Code, true
}

func TestAUserWhoLeadsADepartmentIsReplacedBeforeTheyAreSwitchedOffAndTheSetupCheckSaysSo(t *testing.T) {
	c := newChain(t)
	adminCtx, _ := c.AdminAccount(t, c.tenantID)
	off := false
	_, err := c.IAM.UpdateUser(adminCtx, c.headID, iam.UserPatch{IsActive: &off})
	wantCode(t, err, "USER_IS_DEPARTMENT_HEAD")
	// the setup check finds a head that was switched off behind the screen's back
	must(t, c.Exec(t, `UPDATE users SET is_active = false WHERE id = $1`, c.deputyID))
	rep, err := c.svc.SetupCheck(c.admin, c.propID)
	must(t, err)
	found := false
	for _, i := range rep.Issues {
		found = found || i.Code == "DEPARTMENT_HEAD_INACTIVE"
	}
	if !found {
		t.Fatalf("issues: %+v", rep.Issues)
	}
	c.setHeads(t, c.headID, 0)
	_, err = c.IAM.UpdateUser(adminCtx, c.deputyID, iam.UserPatch{IsActive: &off})
	must(t, err)
	// a chain with no level is a warning
	chain, _ := c.svc.GetApprovalChain(c.admin, c.propID, "PR")
	_, err = c.svc.SetApprovalChain(c.admin, c.propID, "PR", inventory.ApprovalChainInput{SkipLevel1IfHead: true, ApproverMayEdit: true, Version: chain.Version})
	must(t, err)
	rep, _ = c.svc.SetupCheck(c.admin, c.propID)
	found = false
	for _, i := range rep.Issues {
		found = found || (i.Code == "APPROVAL_CHAIN_EMPTY" && i.Ref == "PR")
	}
	if !found {
		t.Fatalf("issues: %+v", rep.Issues)
	}
}

func TestAReceiptThatNobodyBilledForThirtyDaysIsAWarningOfTheNightAudit(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	po := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r := f.receive(t, po, "5")
	_, warnings, err := f.svc.NightAuditStock(f.admin, f.tenantID, f.propID, civil.MustParseDate("2026-09-30"))
	must(t, err)
	for _, w := range warnings {
		if w.Code == inventory.FindUnbilled {
			t.Fatalf("a receipt of today is not old: %+v", w)
		}
	}
	must(t, f.Exec(t, `UPDATE inv_receipts SET received_on = DATE '2026-08-01' WHERE id = $1`, r.ID))
	_, warnings, err = f.svc.NightAuditStock(f.admin, f.tenantID, f.propID, civil.MustParseDate("2026-09-30"))
	must(t, err)
	seen := false
	for _, w := range warnings {
		seen = seen || w.Code == inventory.FindUnbilled
	}
	if !seen {
		t.Fatalf("warnings: %+v", warnings)
	}
}
