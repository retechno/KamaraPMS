package inventory_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
)

// The movements of PR 3 (35-inventory.md 4.5, 5.4, 8.5, 8.9): the requisition with its chain, the transfer, the issue to a department, waste, staff meal and entertainment, and their reversals. Real PostgreSQL;
// both costing methods where the method matters.

// mfx is the purchasing fixture with the people and the warehouses of the movements: a housekeeping warehouse, a store keeper, the head of the department, the final approver of a requisition.
type mfx struct {
	*cfx
	hk               int64
	mover, sr, head2 context.Context
	cnt, apr         context.Context // who counts, and who approves a count
	srID             int64
}

func newMovements(t *testing.T, method string) *mfx {
	t.Helper()
	return finishMovements(t, newChainWith(t, method))
}

func finishMovements(t *testing.T, c *cfx) *mfx {
	t.Helper()
	m := &mfx{cfx: c}
	hk, err := c.svc.CreateWarehouse(c.admin, c.propID, inventory.WarehouseInput{Code: "HK", Name: "Housekeeping store", Kind: "DEPARTMENT", CostClass: "OTHER"})
	must(t, err)
	m.hk = hk.ID
	m.wh["HK"] = hk.ID
	sup, err := c.svc.CreateCategory(c.admin, c.propID, inventory.CategoryInput{Code: "SUP", Name: "Operating supplies", Kind: "SUPPLIES", InventoryAccountID: c.accounts["1330"], IssueAccountID: ptr(c.accounts["5150"])})
	must(t, err)
	c.cat["SUP"] = sup.ID
	c.newItem(t, "SOAP", "Soap 25 g", "SUP", "pcs")
	c.newItem(t, "SHAMPOO", "Shampoo 30 ml", "SUP", "pcs")
	m.mover = c.User(t, c.tenantID, c.propID, auth.PermInventoryMove, auth.PermInventoryView)
	m.sr = c.User(t, c.tenantID, c.propID, auth.PermInventorySRFinalApprove)
	m.srID = uid(t, m.sr)
	m.cnt = c.User(t, c.tenantID, c.propID, auth.PermInventoryCount, auth.PermInventoryView)
	m.apr = c.User(t, c.tenantID, c.propID, auth.PermInventoryApprove, auth.PermInventoryView)
	m.cnt = c.User(t, c.tenantID, c.propID, auth.PermInventoryCount, auth.PermInventoryView)
	m.apr = c.User(t, c.tenantID, c.propID, auth.PermInventoryApprove, auth.PermInventoryView)
	return m
}

// srLine is a line of a requisition: an item and a quantity in its stock unit.
func (m *mfx) srLine(item, qty string) inventory.RequisitionLineInput {
	return inventory.RequisitionLineInput{ItemID: m.item[item], Qty: dec(qty)}
}

// requisition writes a requisition of the bar to the main store, for the department of the chain fixture.
func (m *mfx) requisition(t *testing.T, ctx context.Context, lines ...inventory.RequisitionLineInput) inventory.Requisition {
	t.Helper()
	r, err := m.svc.CreateRequisition(ctx, m.propID, inventory.RequisitionInput{ClientUUID: m.id(), ToWarehouseID: m.wh["BAR"], FromWarehouseID: m.wh["MAIN"], DepartmentID: &m.dept, Lines: lines})
	must(t, err)
	return r
}

// approved takes a requisition through its two levels.
func (m *mfx) approved(t *testing.T, r inventory.Requisition) inventory.Requisition {
	t.Helper()
	r, err := m.svc.SubmitRequisition(m.req, m.propID, r.ID)
	must(t, err)
	r, err = m.svc.ApproveRequisition(m.head, m.propID, r.ID, inventory.ApproveInput{})
	must(t, err)
	r, err = m.svc.ApproveRequisition(m.sr, m.propID, r.ID, inventory.ApproveInput{})
	must(t, err)
	if r.Status != inventory.SRApproved {
		t.Fatalf("the requisition is %s", r.Status)
	}
	return r
}

func (m *mfx) transfer(t *testing.T, ctx context.Context, from, to string, srID *int64, lines ...inventory.TransferLineInput) (inventory.Transfer, error) {
	t.Helper()
	in := inventory.TransferInput{ClientUUID: m.id(), RequisitionID: srID, FromWarehouseID: m.wh[from], ToWarehouseID: m.wh[to], Lines: lines}
	return m.svc.PostTransfer(ctx, m.propID, in)
}

func (m *mfx) tl(item, qty string) inventory.TransferLineInput {
	return inventory.TransferLineInput{ItemID: m.item[item], Qty: dec(qty)}
}

// journalRows counts the movements of a transfer that carry a journal (there must be none).
func (m *mfx) journalRows(t *testing.T, trfID int64) int {
	t.Helper()
	return m.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type IN ('INV_TRANSFER', 'INV_TRANSFER_REV') AND source_id = $1 AND (gl_amount <> 0 OR gl_debit_account_id IS NOT NULL)`, trfID)
}

// ---------------------------------------------------------------- the transfer

// 8.5: 10 kg of rice from the store to the bar: the value goes with the goods, nothing is journaled.
func TestATransferCarriesTheValueOfTheGoodsAndMakesNoJournal(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500") // 40 kg, 620,000
	trf, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "10"))
	must(t, err)
	if trf.Status != inventory.TrfPosted || !trf.TotalValue.Equal(dec("155000")) || len(trf.Lines) != 1 {
		t.Fatalf("transfer: %+v", trf)
	}
	m.wantBal(t, m.pair("MAIN", "BERAS"), "30", "465000")
	m.wantBal(t, m.pair("BAR", "BERAS"), "10", "155000")
	if n := m.journalRows(t, trf.ID); n != 0 {
		t.Fatalf("%d movements of the transfer carry a journal", n)
	}
	m.consistent(t)
	// the same client_uuid answers the first transfer
	again, err := m.svc.PostTransfer(m.mover, m.propID, inventory.TransferInput{ClientUUID: m.lastUUID(), FromWarehouseID: m.wh["MAIN"], ToWarehouseID: m.wh["BAR"], Lines: []inventory.TransferLineInput{m.tl("BERAS", "10")}})
	must(t, err)
	if again.ID != trf.ID {
		t.Fatalf("a replay made a second transfer: %d and %d", again.ID, trf.ID)
	}
	m.wantBal(t, m.pair("MAIN", "BERAS"), "30", "465000")
}

func (m *mfx) lastUUID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return "00000000-0000-4000-8000-" + pad12(m.uuid)
}

func TestTheSourceOfATransferMayNotGoBelowZeroAndTheDestinationSettlesWhatWasSoldThere(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "5", "15500")
	_, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "6"))
	wantCode(t, err, "NEGATIVE_STOCK_NOT_ALLOWED")
	m.wantBal(t, m.pair("MAIN", "BERAS"), "5", "77500")
	// the bar sold 2 kg that it did not have (estimated at the last cost of the item), and the transfer covers them
	m.sale(t, m.pair("BAR", "BERAS"), "2")
	q, _ := m.bal(t, m.pair("BAR", "BERAS"))
	if !dec(q).Equal(dec("-2")) {
		t.Fatalf("the bar holds %s", q)
	}
	trf, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "5"))
	must(t, err)
	m.wantBal(t, m.pair("BAR", "BERAS"), "3", "46500")
	if m.Count(t, `SELECT count(*) FROM inv_shortfalls WHERE warehouse_id = $1 AND status = 'OPEN'`, m.wh["BAR"]) != 0 {
		t.Fatal("the shortfall of the bar is still open")
	}
	// a transfer that settled a shortfall is not reversed by the simple rules
	_, err = m.svc.ReverseTransfer(m.mover, m.propID, trf.ID, "wrong store")
	wantCode(t, err, "MOVEMENT_NOT_REVERSIBLE")
	m.consistent(t)
}

func TestATransferIsReversedOnItsDayAndNotAfter(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	trf, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "10"))
	must(t, err)
	_, err = m.svc.ReverseTransfer(m.mover, m.propID, trf.ID, "")
	wantCode(t, err, "VALIDATION_FAILED")
	rev, err := m.svc.ReverseTransfer(m.mover, m.propID, trf.ID, "sent by mistake")
	must(t, err)
	if rev.Status != inventory.TrfReversed {
		t.Fatalf("status %s", rev.Status)
	}
	m.wantBal(t, m.pair("MAIN", "BERAS"), "40", "620000")
	m.wantBal(t, m.pair("BAR", "BERAS"), "0", "0")
	_, err = m.svc.ReverseTransfer(m.mover, m.propID, trf.ID, "again")
	wantCode(t, err, "TRANSFER_STATE_INVALID")
	// the next day it is a new transfer the other way
	trf2, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "4"))
	must(t, err)
	m.nextBusinessDay(t)
	_, err = m.svc.ReverseTransfer(m.mover, m.propID, trf2.ID, "late")
	wantCode(t, err, "TRANSFER_NOT_REVERSIBLE")
	m.consistent(t)
}

// Two transfers in opposite directions at once do not deadlock: both warehouses and every balance are locked in ascending order, in one call.
func TestTwoTransfersInOppositeDirectionsAtOnceDoNotDeadlock(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "BAR", "BERAS", "kg", "40", "15500")
	m.open(t, "MAIN", "SIRUP", "botol", "30", "50000")
	m.open(t, "BAR", "SIRUP", "botol", "30", "50000")
	for round := 0; round < 3; round++ {
		errs := race(
			func() error {
				_, e := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "5"), m.tl("SIRUP", "3"))
				return e
			},
			func() error {
				_, e := m.transfer(t, m.mover, "BAR", "MAIN", nil, m.tl("SIRUP", "4"), m.tl("BERAS", "6"))
				return e
			},
		)
		if succeeded(errs) != 2 {
			t.Fatalf("round %d: %v", round, errs)
		}
	}
	m.consistent(t)
}

// ---------------------------------------------------------------- the requisition

func TestATransferThatNamesARequisitionWaitsForItsTwoLevels(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	r := m.requisition(t, m.req, m.srLine("BERAS", "20"))
	if r.Status != inventory.SRDraft || !r.EstTotal.Equal(dec("310000")) { // 8.9: 20 kg x 15,500
		t.Fatalf("requisition %s %s", r.Status, r.EstTotal)
	}
	_, err := m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "5"))
	wantCode(t, err, "SR_NOT_APPROVED")
	r, err = m.svc.SubmitRequisition(m.req, m.propID, r.ID)
	must(t, err)
	if r.Stage == nil || r.Stage.Total != 2 || active2(r) != 1 {
		t.Fatalf("submitted: %+v level %d", r.Stage, active2(r))
	}
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "5"))
	wantCode(t, err, "SR_NOT_APPROVED")
	r, err = m.svc.ApproveRequisition(m.head, m.propID, r.ID, inventory.ApproveInput{})
	must(t, err)
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "5")) // still level 2
	wantCode(t, err, "SR_NOT_APPROVED")
	r, err = m.svc.ApproveRequisition(m.sr, m.propID, r.ID, inventory.ApproveInput{})
	must(t, err)
	if r.Status != inventory.SRApproved {
		t.Fatal(r.Status)
	}
	// the requester heard of it
	n, err := m.svc.Notices(m.req, m.propID)
	must(t, err)
	if n.Unseen != 1 || n.Data[0].Kind != "APPROVED" || n.Data[0].RequisitionID == nil || *n.Data[0].RequisitionID != r.ID {
		t.Fatalf("notices %+v", n)
	}
	// two transfers fill it: 12 and then 8 (a transfer may not send more than is left)
	t1, err := m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "12"))
	must(t, err)
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "9"))
	wantCode(t, err, "TRANSFER_EXCEEDS_SR")
	got, err := m.svc.GetRequisition(m.mover, m.propID, r.ID)
	must(t, err)
	if got.Status != inventory.SRPartial || !got.Lines[0].QtyLeft.Equal(dec("8")) || len(got.Transfers) != 1 {
		t.Fatalf("after the first transfer: %s, left %s, %d transfers", got.Status, got.Lines[0].QtyLeft, len(got.Transfers))
	}
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "8"))
	must(t, err)
	got, _ = m.svc.GetRequisition(m.mover, m.propID, r.ID)
	if got.Status != inventory.SRFulfilled || len(got.Transfers) != 2 {
		t.Fatalf("after the second: %s, %d transfers", got.Status, len(got.Transfers))
	}
	// reversing the first gives its quantity back to the requisition
	_, err = m.svc.ReverseTransfer(m.mover, m.propID, t1.ID, "wrong rice")
	must(t, err)
	got, _ = m.svc.GetRequisition(m.mover, m.propID, r.ID)
	if got.Status != inventory.SRPartial || !got.Lines[0].QtyLeft.Equal(dec("12")) {
		t.Fatalf("after the reversal: %s, left %s", got.Status, got.Lines[0].QtyLeft)
	}
	m.consistent(t)
}

func active2(r inventory.Requisition) int {
	for _, s := range r.Steps {
		if s.Cycle == r.Cycle && s.Status == "ACTIVE" {
			return s.LevelNo
		}
	}
	return 0
}

func TestAFreeTransferNeedsNoRequisitionAndIsListedAsFree(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "MAIN", "SIRUP", "botol", "30", "50000")
	free, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "1"))
	must(t, err)
	r := m.approved(t, m.requisition(t, m.req, m.srLine("SIRUP", "6")))
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("SIRUP", "6"))
	must(t, err)
	list, err := m.svc.ListTransfers(m.mover, m.propID, inventory.TransferFilter{FreeOnly: true})
	must(t, err)
	if len(list) != 1 || list[0].ID != free.ID {
		t.Fatalf("free transfers: %+v", list)
	}
}

func TestTheStoreMayRejectAnApprovedRequisition(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	r := m.approved(t, m.requisition(t, m.req, m.srLine("BERAS", "20")))
	_, err := m.svc.RejectRequisition(m.mover, m.propID, r.ID, "")
	wantCode(t, err, "VALIDATION_FAILED")
	rej, err := m.svc.RejectRequisition(m.mover, m.propID, r.ID, "the rice is for the buffet")
	must(t, err)
	if rej.Status != inventory.SRRejected || rej.RejectedNote == nil {
		t.Fatalf("%s", rej.Status)
	}
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", &r.ID, m.tl("BERAS", "5"))
	wantCode(t, err, "SR_NOT_APPROVED")
	// a draft cannot be rejected by the store
	d := m.requisition(t, m.req, m.srLine("BERAS", "1"))
	_, err = m.svc.RejectRequisition(m.mover, m.propID, d.ID, "no")
	wantCode(t, err, "SR_NOT_APPROVED")
}

// An SR with a threshold on its value: the level of the final approval applies only above it (5.8, the conditions are those of a PR).
func TestARequisitionChainCanHaveAConditionOnTheValue(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "400", "15500")
	ch, err := m.svc.GetApprovalChain(m.admin, m.propID, "SR")
	must(t, err)
	sr := string(auth.PermInventorySRFinalApprove)
	above := dec("1000000")
	_, err = m.svc.SetApprovalChain(m.admin, m.propID, "SR", inventory.ApprovalChainInput{SkipLevel1IfHead: ch.SkipLevel1IfHead, ApproverMayEdit: true, Version: ch.Version, Levels: []inventory.ApprovalLevel{
		{Name: "Kepala departemen", ApproverKind: "DEPT_HEAD", ConditionKind: "ALWAYS"},
		{Name: "Persetujuan akhir SR", ApproverKind: "PERMISSION", Permission: &sr, ConditionKind: "TOTAL_ABOVE", Threshold: &above},
	}})
	must(t, err)
	small := m.requisition(t, m.req, m.srLine("BERAS", "20")) // 310,000
	small, err = m.svc.SubmitRequisition(m.req, m.propID, small.ID)
	must(t, err)
	small, err = m.svc.ApproveRequisition(m.head, m.propID, small.ID, inventory.ApproveInput{})
	must(t, err)
	if small.Status != inventory.SRApproved {
		t.Fatalf("a requisition under the threshold stops at level 1: %s", small.Status)
	}
	big := m.requisition(t, m.req, m.srLine("BERAS", "100")) // 1,550,000
	big, err = m.svc.SubmitRequisition(m.req, m.propID, big.ID)
	must(t, err)
	big, err = m.svc.ApproveRequisition(m.head, m.propID, big.ID, inventory.ApproveInput{})
	must(t, err)
	if big.Status != inventory.SRSubmitted || active2(big) != 2 {
		t.Fatalf("above the threshold it goes on: %s level %d", big.Status, active2(big))
	}
	// the approver of level 2 lowers the quantity under the threshold: the value is worked out again and an edit is recorded
	lvl := 2
	big, err = m.svc.ApproveRequisition(m.sr, m.propID, big.ID, inventory.ApproveInput{LevelNo: &lvl, Edits: []inventory.ApproveEdit{{LineNo: 1, Qty: ptr(dec("60"))}}})
	must(t, err)
	if big.Status != inventory.SRApproved || !big.EstTotal.Equal(dec("930000")) || len(big.Edits) != 1 || big.Edits[0].Field != "QTY" {
		t.Fatalf("%s %s %+v", big.Status, big.EstTotal, big.Edits)
	}
}

// Two approvers decide the same active level of a requisition at once: exactly one succeeds.
func TestTwoApproversOfARequisitionAtOnceDecideItOnce(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	sr2 := m.User(t, m.tenantID, m.propID, auth.PermInventorySRFinalApprove)
	r := m.requisition(t, m.req, m.srLine("BERAS", "20"))
	r, err := m.svc.SubmitRequisition(m.req, m.propID, r.ID)
	must(t, err)
	_, err = m.svc.ApproveRequisition(m.head, m.propID, r.ID, inventory.ApproveInput{})
	must(t, err)
	lvl := 2
	errs := race(
		func() error {
			_, e := m.svc.ApproveRequisition(m.sr, m.propID, r.ID, inventory.ApproveInput{LevelNo: &lvl})
			return e
		},
		func() error {
			_, e := m.svc.ApproveRequisition(sr2, m.propID, r.ID, inventory.ApproveInput{LevelNo: &lvl})
			return e
		},
	)
	if succeeded(errs) != 1 {
		t.Fatalf("results: %v", errs)
	}
	for _, e := range errs {
		if e != nil {
			wantCode(t, e, "APPROVAL_NOT_IN_PROGRESS")
		}
	}
	got, err := m.svc.GetRequisition(m.mover, m.propID, r.ID)
	must(t, err)
	if got.Status != inventory.SRApproved {
		t.Fatal(got.Status)
	}
}

func TestTheRequesterOfARequisitionNeverApprovesIt(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	boss, _ := m.AdminAccount(t, m.tenantID)             // a tenant administrator who is a person, with a user of their own
	r := m.requisition(t, boss, m.srLine("BERAS", "20")) // the administrator writes it
	r, err := m.svc.SubmitRequisition(boss, m.propID, r.ID)
	if err != nil {
		wantCode(t, err, "APPROVAL_CHAIN_UNSATISFIABLE") // nobody but the administrator can decide level 2, and the head is level 1
		return
	}
	_, err = m.svc.ApproveRequisition(boss, m.propID, r.ID, inventory.ApproveInput{})
	wantCode(t, err, "APPROVAL_OWN_DOCUMENT")
}

func TestAReturnedRequisitionGoesBackToItsRequesterWithTheNote(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	r := m.requisition(t, m.req, m.srLine("BERAS", "20"))
	r, err := m.svc.SubmitRequisition(m.req, m.propID, r.ID)
	must(t, err)
	_, err = m.svc.ReturnRequisition(m.head, m.propID, r.ID, inventory.ReturnRequestInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	r, err = m.svc.ReturnRequisition(m.head, m.propID, r.ID, inventory.ReturnRequestInput{Note: "ask for less"})
	must(t, err)
	if r.Status != inventory.SRReturned || r.ReturnedNote == nil || *r.ReturnedNote != "ask for less" {
		t.Fatalf("%s", r.Status)
	}
	// the requester changes it and submits again: round 2
	r, err = m.svc.UpdateRequisition(m.req, m.propID, r.ID, inventory.RequisitionInput{ToWarehouseID: m.wh["BAR"], FromWarehouseID: m.wh["MAIN"], DepartmentID: &m.dept, Version: r.Version,
		Lines: []inventory.RequisitionLineInput{m.srLine("BERAS", "10")}})
	must(t, err)
	r, err = m.svc.SubmitRequisition(m.req, m.propID, r.ID)
	must(t, err)
	if r.Cycle != 2 || active2(r) != 1 {
		t.Fatalf("round %d level %d", r.Cycle, active2(r))
	}
	// the inbox of the head lists the requisition with the kind SR
	in, err := m.svc.Inbox(m.head, m.propID)
	must(t, err)
	found := false
	for _, it := range in {
		if it.DocType == "SR" && it.DocID == r.ID && it.Number == r.Number {
			found = true
		}
	}
	if !found {
		t.Fatalf("inbox: %+v", in)
	}
}

// ---------------------------------------------------------------- the issue

func (m *mfx) issue(ctx context.Context, wh, purpose string, mut func(in *inventory.IssueInput), lines ...inventory.IssueLineInput) (inventory.Issue, error) {
	in := inventory.IssueInput{ClientUUID: m.id(), WarehouseID: m.wh[wh], Purpose: purpose, Lines: lines}
	if purpose != inventory.PurposeWaste {
		in.DepartmentID = &m.dept
	}
	if mut != nil {
		mut(&in)
	}
	return m.svc.PostIssue(ctx, m.propID, in)
}

func (m *mfx) il(item, qty string) inventory.IssueLineInput {
	return inventory.IssueLineInput{ItemID: m.item[item], Qty: dec(qty)}
}

// 8.5: 200 pcs of shampoo from housekeeping at 3,500: Dr 5150 700,000 / Cr 1330 700,000, with the department.
func TestAnIssueToADepartmentBooksTheExpenseOfTheCategoryWithTheDepartment(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "HK", "SHAMPOO", "pcs", "300", "3500")
	iss, err := m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SHAMPOO", "200"))
	must(t, err)
	if !iss.TotalValue.Equal(dec("700000")) || iss.Lines[0].ExpenseAccountID == nil || *iss.Lines[0].ExpenseAccountID != m.accounts["5150"] {
		t.Fatalf("issue: %+v", iss)
	}
	m.wantBal(t, m.pair("HK", "SHAMPOO"), "100", "350000")
	var debit, credit, dept int64
	var amount string
	must(t, m.Pool.QueryRow(context.Background(), `SELECT gl_debit_account_id, gl_credit_account_id, COALESCE(gl_department_id, 0), gl_amount::text FROM inv_movements WHERE source_type = 'INV_ISSUE' AND source_id = $1`, iss.ID).Scan(&debit, &credit, &dept, &amount))
	if debit != m.accounts["5150"] || credit != m.accounts["1330"] || dept != m.dept || !dec(amount).Equal(dec("700000")) {
		t.Fatalf("journal: Dr %d Cr %d dept %d %s", debit, credit, dept, amount)
	}
	// more than the shelf holds: refused (a manual issue never takes stock below zero)
	_, err = m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SHAMPOO", "101"))
	wantCode(t, err, "NEGATIVE_STOCK_NOT_ALLOWED")
	// the department is required for a use
	_, err = m.issue(m.mover, "HK", inventory.PurposeUse, func(in *inventory.IssueInput) { in.DepartmentID = nil }, m.il("SHAMPOO", "1"))
	wantCode(t, err, "VALIDATION_FAILED")
	m.consistent(t)
}

// A department that the account requires and the issue does not name is refused when the issue is posted, never at the day close (5.4).
func TestTheDepartmentRuleOfTheAccountIsAppliedWhenTheIssueIsPosted(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	must(t, m.Exec(t, `UPDATE gl_accounts SET department_requirement = 'REQUIRED', default_department_id = NULL WHERE id = $1`, m.accounts["5290"]))
	// a waste takes the department of its warehouse, and housekeeping holds none: the account that requires one refuses at the issue
	_, err := m.issue(m.mover, "HK", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("EXPIRED") }, m.il("SOAP", "5"))
	if got := fieldCode(t, err, "lines[0].expense_account_id"); got != "DEPARTMENT_REQUIRED" {
		t.Fatalf("got %q (%v)", got, err)
	}
	m.wantBal(t, m.pair("HK", "SOAP"), "100", "296000")
	// naming the department is enough
	_, err = m.issue(m.mover, "HK", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("EXPIRED"); in.DepartmentID = &m.dept }, m.il("SOAP", "5"))
	must(t, err)
	m.wantBal(t, m.pair("HK", "SOAP"), "95", "281200")
	// an account that takes no department drops the one that came from the warehouse
	must(t, m.Exec(t, `UPDATE gl_accounts SET department_requirement = 'NONE', default_department_id = NULL WHERE id = $1`, m.accounts["5290"]))
	_, err = m.issue(m.mover, "HK", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("EXPIRED") }, m.il("SOAP", "1"))
	must(t, err)
	// but a department that is named for it is refused
	_, err = m.issue(m.mover, "HK", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("EXPIRED"); in.DepartmentID = &m.dept }, m.il("SOAP", "1"))
	if got := fieldCode(t, err, "lines[0].expense_account_id"); got != "DEPARTMENT_NOT_ALLOWED" {
		t.Fatalf("got %q (%v)", got, err)
	}
}

func TestAWasteHasAReasonAndGoesToTheWasteAccount(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "BAR", "SIRUP", "botol", "20", "50000")
	_, err := m.issue(m.mover, "BAR", inventory.PurposeWaste, nil, m.il("SIRUP", "2")) // no reason
	wantCode(t, err, "VALIDATION_FAILED")
	iss, err := m.issue(m.mover, "BAR", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("SPOILAGE") }, m.il("SIRUP", "2"))
	must(t, err)
	if !iss.TotalValue.Equal(dec("100000")) || iss.ReasonCode == nil {
		t.Fatalf("%+v", iss)
	}
	var debit int64
	must(t, m.Pool.QueryRow(context.Background(), `SELECT gl_debit_account_id FROM inv_movements WHERE source_type = 'INV_ISSUE' AND source_id = $1 AND kind = 'WASTE'`, iss.ID).Scan(&debit))
	if debit != m.accounts["5290"] {
		t.Fatalf("the waste went to account %d, want 5290 (%d)", debit, m.accounts["5290"])
	}
	// a staff meal is not a waste and a waste has no covers
	_, err = m.issue(m.mover, "BAR", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("OTHER"); in.Covers = ptr(3) }, m.il("SIRUP", "1"))
	wantCode(t, err, "VALIDATION_FAILED")
}

// 5.4: the staff meal and the entertainment have their own accounts and kinds; the item class decides the class column; the department of the document bears the cost.
func TestAStaffMealAndAnEntertainmentHaveTheirOwnAccountsAndKinds(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "BAR", "SIRUP", "botol", "20", "50000")
	_, err := m.issue(m.mover, "MAIN", inventory.PurposeStaffMeal, nil, m.il("BERAS", "5")) // no covers
	wantCode(t, err, "VALIDATION_FAILED")
	meal, err := m.issue(m.mover, "MAIN", inventory.PurposeStaffMeal, func(in *inventory.IssueInput) { in.Covers = ptr(25) }, m.il("BERAS", "5"))
	must(t, err)
	if !meal.TotalValue.Equal(dec("77500")) || meal.CostPerCover == nil || !meal.CostPerCover.Equal(dec("3100")) {
		t.Fatalf("meal %s per cover %v", meal.TotalValue, meal.CostPerCover)
	}
	_, err = m.issue(m.mover, "BAR", inventory.PurposeEntertainment, func(in *inventory.IssueInput) { in.Covers = ptr(4) }, m.il("SIRUP", "2")) // no beneficiary
	wantCode(t, err, "VALIDATION_FAILED")
	ent, err := m.issue(m.mover, "BAR", inventory.PurposeEntertainment, func(in *inventory.IssueInput) { in.Covers = ptr(4); in.Beneficiary = ptr("Bapak Gubernur") }, m.il("SIRUP", "2"))
	must(t, err)
	type row struct {
		kind, class string
		debit       int64
	}
	var mealRow, entRow row
	must(t, m.Pool.QueryRow(context.Background(), `SELECT kind, cost_class, gl_debit_account_id FROM inv_movements WHERE source_type = 'INV_ISSUE' AND source_id = $1`, meal.ID).Scan(&mealRow.kind, &mealRow.class, &mealRow.debit))
	must(t, m.Pool.QueryRow(context.Background(), `SELECT kind, cost_class, gl_debit_account_id FROM inv_movements WHERE source_type = 'INV_ISSUE' AND source_id = $1`, ent.ID).Scan(&entRow.kind, &entRow.class, &entRow.debit))
	if mealRow.kind != "STAFF_MEAL" || mealRow.class != "FOOD" || mealRow.debit != m.accounts["5240"] {
		t.Fatalf("meal: %+v (5240 is %d)", mealRow, m.accounts["5240"])
	}
	if entRow.kind != "ENTERTAINMENT" || entRow.class != "BEVERAGE" || entRow.debit != m.accounts["6170"] {
		t.Fatalf("entertainment: %+v (6170 is %d)", entRow, m.accounts["6170"])
	}
	// both are reversed on the day, and not twice
	rev, err := m.svc.ReverseIssue(m.mover, m.propID, meal.ID, "counted twice")
	must(t, err)
	if rev.ReversesNumber == nil || *rev.ReversesNumber != meal.Number {
		t.Fatalf("%+v", rev)
	}
	m.wantBal(t, m.pair("MAIN", "BERAS"), "40", "620000")
	_, err = m.svc.ReverseIssue(m.mover, m.propID, meal.ID, "again")
	wantCode(t, err, "ISSUE_STATE_INVALID")
	_, err = m.svc.ReverseIssue(m.mover, m.propID, rev.ID, "the reversal")
	wantCode(t, err, "ISSUE_STATE_INVALID")
	m.consistent(t)
}

func TestTheMovementsNeedTheirPermissionsAndTheTenantWall(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	viewer := m.User(t, m.tenantID, m.propID, auth.PermInventoryView)
	_, err := m.transfer(t, viewer, "MAIN", "BAR", nil, m.tl("BERAS", "1"))
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.issue(viewer, "MAIN", inventory.PurposeUse, nil, m.il("BERAS", "1"))
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.StartCount(viewer, m.propID, inventory.CountInput{ClientUUID: m.id(), WarehouseID: m.wh["MAIN"], Scope: inventory.ScopeAll})
	wantCode(t, err, "PERMISSION_DENIED")
	nobody := m.User(t, m.tenantID, m.propID, auth.PermHousekeepingUpdate)
	_, err = m.svc.ListTransfers(nobody, m.propID, inventory.TransferFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	// another tenant's property is not found
	other := m.Env.Tenant(t, "OTH")
	op := m.Env.PropertyIn(t, other.ID, "OTH1", "IDR", 0)
	stranger := m.User(t, other.ID, op.ID, auth.PermInventoryMove)
	_, err = m.svc.ListTransfers(stranger, m.propID, inventory.TransferFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}

// nextBusinessDay closes the open business day and opens the next one, the way the night audit does after its steps.
func (f *fx) nextBusinessDay(t *testing.T) {
	t.Helper()
	f.Clock.Advance(24 * time.Hour) // the day is over: the server clock has moved past its end
	prop, err := f.Tenancy.GetProperty(f.admin, f.propID)
	must(t, err)
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	must(t, f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		_, _, err := f.Tenancy.CloseAndOpenNext(ctx, prop, day.BusinessDate, nil, json.RawMessage(`{}`))
		return err
	}))
}
