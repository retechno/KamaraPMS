package assets_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Tests of PR 3, second part: the disposals, their approval and the cash flow (docs/architecture/37-fixed-assets.md 4.7, 4.8, 7.2 and the examples 10.6, 10.7, 10.11).

// withAssets starts the register on 2026-09-01, makes the life of an FF&E asset 36 months (the 1,000,000 a month of the examples) and imports the rows as at 2026-08-31, so that the open day
// (2026-09-30) is the first month of the register. It returns the ids of the assets.
func (f *fx) withAssets(t *testing.T, rows ...assets.OpeningRow) []int64 {
	t.Helper()
	f.startRegister(t, "2026-09-01", "0")
	f.books(t, "2026-08-01")
	f.exec(t, `UPDATE fa_categories SET life_months = 36 WHERE property_id = $1 AND code = 'FFE'`, f.propID)
	res, err := f.importRows(t, "2026-08-31", true, false, rows...)
	must(t, err)
	ids := make([]int64, len(res.Rows))
	for i, r := range res.Rows {
		ids[i] = *r.AssetID
	}
	return ids
}

// chair is the asset of 10.6: cost 36,000,000, accumulated 21,000,000 (21 months of 1,000,000), carrying value 15,000,000.
func chair(name string) assets.OpeningRow { return row(name, "FFE", "36000000", "21000000", 21) }

// noDrift says the register agrees with its ledger and with the accounts of the books.
func (f *fx) noDrift(t *testing.T) {
	t.Helper()
	chk, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	for _, r := range chk.Reconciliation {
		if !r.CostDifference.IsZero() || !r.AccumDifference.IsZero() {
			t.Errorf("drift %+v", r)
		}
	}
}

func (f *fx) sale(price string, account string) assets.DisposeInput {
	return assets.DisposeInput{Kind: assets.DisposalSale, ReasonCode: assets.ReasonSold, Proceeds: ptr(dec(price)), ProceedsAccountID: ptr(f.accounts[account]), IdempotencyKey: nextKey()}
}

func (f *fx) writeOff(reason string) assets.DisposeInput {
	return assets.DisposeInput{Kind: assets.DisposalWriteOff, ReasonCode: reason, Note: "gone", IdempotencyKey: nextKey()}
}

func (f *fx) disposeAs(t *testing.T, ctx context.Context, id int64, in assets.DisposeInput) assets.DisposeResult {
	t.Helper()
	r, err := f.svc.Dispose(ctx, f.propID, id, in)
	must(t, err)
	return r
}

func wantLines(t *testing.T, f *fx, journalID int64, want map[string]string) {
	t.Helper()
	got := f.journalLines(t, journalID)
	if len(got) != len(want) {
		t.Fatalf("journal lines %v, want %v", got, want)
	}
	for code, v := range want {
		if !got[code].Equal(dec(v)) {
			t.Fatalf("journal lines %v, want %v", got, want)
		}
	}
}

// 10.6: the four disposals of the same asset, each with its journal, its gain or loss and the amount of its approval.
func TestExample106Disposals(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("Chair A"), chair("Chair B"), chair("Chair C"), chair("Chair D"))
	cases := []struct {
		in       assets.DisposeInput
		lines    map[string]string
		gain     string
		approval string
	}{
		{f.sale("18000000", "1110"), map[string]string{"1110": "18000000", "1592": "21000000", "1530": "-36000000", "7420": "-3000000"}, "3000000", "18000000"},
		{f.sale("12000000", "1110"), map[string]string{"1110": "12000000", "1592": "21000000", "7420": "3000000", "1530": "-36000000"}, "-3000000", "15000000"},
		{f.writeOff(assets.ReasonLost), map[string]string{"1592": "21000000", "7420": "15000000", "1530": "-36000000"}, "-15000000", "15000000"},
		{f.sale("18000000", "1230"), map[string]string{"1230": "18000000", "1592": "21000000", "1530": "-36000000", "7420": "-3000000"}, "3000000", "18000000"},
	}
	for i, c := range cases {
		pv, err := f.svc.PreviewDisposal(f.admin, f.propID, ids[i], c.in)
		must(t, err)
		if !pv.GainLoss.Equal(dec(c.gain)) || !pv.ApprovalAmount.Equal(dec(c.approval)) || !pv.CarryingRemoved.Equal(dec("15000000")) {
			t.Fatalf("case %d preview: %+v", i, pv)
		}
		r := f.disposeAs(t, f.admin, ids[i], c.in)
		if r.Status != assets.DisposalPosted || r.Disposal == nil || r.Disposal.Number == "" {
			t.Fatalf("case %d: %+v", i, r)
		}
		wantLines(t, f, r.Disposal.JournalID, c.lines)
		if !r.Disposal.GainLoss.Equal(dec(c.gain)) || !r.Disposal.CostRemoved.Equal(dec("36000000")) || !r.Disposal.AccumRemoved.Equal(dec("21000000")) {
			t.Errorf("case %d: %+v", i, r.Disposal)
		}
		a := f.asDetail(t, ids[i])
		if a.Status != "DISPOSED" || a.Version == 0 {
			t.Errorf("case %d: %+v", i, a.Asset)
		}
		sum := f.countRows(t, `SELECT COALESCE(sum(cost_delta), 0)::int FROM fa_ledger WHERE asset_id = $1`, ids[i])
		if sum != 0 {
			t.Errorf("case %d: the ledger nets to %d", i, sum)
		}
		// the same key replays the first answer
		again, err := f.svc.Dispose(f.admin, f.propID, ids[i], c.in)
		must(t, err)
		if !again.Replayed || again.Disposal.ID != r.Disposal.ID {
			t.Errorf("case %d replay: %+v", i, again)
		}
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_disposals WHERE property_id = $1`, f.propID); n != 4 {
		t.Errorf("%d disposals", n)
	}
	// the register agrees with the ledger and with the control accounts
	f.noDrift(t)
}

func TestADisposalIsRefusedWhenItsRulesAreBroken(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("Chair A"))
	id := ids[0]
	bad := func(in assets.DisposeInput, code string) {
		t.Helper()
		_, err := f.svc.Dispose(f.admin, f.propID, id, in)
		wantCode(t, err, code)
	}
	sale := f.sale("1000000", "1110")
	noPrice := sale
	noPrice.Proceeds, noPrice.IdempotencyKey = nil, nextKey()
	bad(noPrice, "VALIDATION_FAILED")
	creditSale := f.sale("1000000", "1110")
	creditSale.ProceedsAccountID, creditSale.IdempotencyKey = ptr(f.accounts["6510"]), nextKey() // an expense account takes no proceeds
	bad(creditSale, "VALIDATION_FAILED")
	wrongReason := f.writeOff(assets.ReasonSold)
	bad(wrongReason, "VALIDATION_FAILED")
	other := f.writeOff(assets.ReasonOther)
	other.Note = ""
	bad(other, "VALIDATION_FAILED")
	tooMany := f.writeOff(assets.ReasonLost)
	tooMany.Units = ptr(int32(2)) // a chair is one piece
	bad(tooMany, "VALIDATION_FAILED")
	future := f.writeOff(assets.ReasonLost)
	future.DisposalDate = ptr(d("2026-10-02"))
	bad(future, "VALIDATION_FAILED")
	noKey := f.writeOff(assets.ReasonLost)
	noKey.IdempotencyKey = ""
	bad(noKey, "IDEMPOTENCY_KEY_REQUIRED")
	// a person without the permission is refused before anything is read
	_, err := f.svc.Dispose(f.user(t, auth.PermAssetRead), f.propID, id, f.writeOff(assets.ReasonLost))
	wantCode(t, err, "PERMISSION_DENIED")
	if n := f.countRows(t, `SELECT count(*) FROM fa_disposals`); n != 0 {
		t.Errorf("%d disposals made by refused requests", n)
	}
	// Q5: the months before the disposal are run, and the month of it is not depreciated
	f.advanceTo(t, "2026-10-02")
	wantCode(t, func() error { _, e := f.svc.Dispose(f.admin, f.propID, id, f.writeOff(assets.ReasonLost)); return e }(), "DEPRECIATION_NOT_RUN")
	f.run(t, "2026-09")
	late := f.writeOff(assets.ReasonLost)
	late.DisposalDate = ptr(d("2026-09-30")) // September has been depreciated
	bad(late, "DISPOSAL_MONTH_DEPRECIATED")
	ok := f.disposeAs(t, f.admin, id, f.writeOff(assets.ReasonLost)) // dated 2026-10-02: October is not depreciated
	if ok.Disposal == nil || ok.Disposal.DisposalDate.String() != "2026-10-02" {
		t.Fatalf("%+v", ok)
	}
	// a disposed asset is not disposed of again, nor run
	bad(f.writeOff(assets.ReasonLost), "ASSET_STATUS_INVALID")
	f.advanceTo(t, "2026-11-01")
	for _, l := range f.preview(t, "2026-10").Lines {
		if l.AssetID == id {
			t.Errorf("the disposed asset is in the run of October")
		}
	}
}

// 10.7: ten of 200 sets of linen are lost.
func TestExample107ABatchLosesTenSets(t *testing.T) {
	f := newFx(t)
	r := row("Linen sets", "OE", "36000000", "9000000", 6)
	r.UnitCount = ptr(200)
	id := f.withAssets(t, r)[0]
	in := f.writeOff(assets.ReasonLost)
	in.Units = ptr(int32(10))
	res := f.disposeAs(t, f.admin, id, in)
	dsp := res.Disposal
	if !dsp.CostRemoved.Equal(dec("1800000")) || !dsp.AccumRemoved.Equal(dec("450000")) || !dsp.CarryingRemoved.Equal(dec("1350000")) || !dsp.GainLoss.Equal(dec("-1350000")) {
		t.Fatalf("%+v", dsp)
	}
	wantLines(t, f, dsp.JournalID, map[string]string{"1593": "450000", "7420": "1350000", "1540": "-1800000"})
	a := f.asDetail(t, id)
	if a.Status != "ACTIVE" || a.UnitCount != 190 || !a.Cost.Equal(dec("34200000")) || !a.AccumDepr.Equal(dec("8550000")) {
		t.Fatalf("%+v", a.Asset)
	}
	f.advanceTo(t, "2026-10-01")
	if got := f.nextAmount(t, "2026-09", id); !got.Equal(dec("1425000")) { // (34,200,000 - 8,550,000) / 18
		t.Fatalf("the month after: %s", got)
	}
	// all that is left closes the batch exactly, whatever the rounding
	rest := f.writeOff(assets.ReasonLost)
	rest.Units = ptr(int32(190))
	rest.DisposalDate = ptr(d("2026-09-30"))
	last, err := f.svc.Dispose(f.admin, f.propID, id, rest)
	must(t, err)
	if !last.Disposal.CostRemoved.Equal(dec("34200000")) || !last.Disposal.AccumRemoved.Equal(dec("8550000")) || f.asDetail(t, id).Status != "DISPOSED" {
		t.Fatalf("%+v", last.Disposal)
	}
}

func TestADisposalIsReversedOnceWhileItsMonthIsOpen(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("Chair A"), chair("Chair B"))
	whole := f.disposeAs(t, f.admin, ids[0], f.sale("18000000", "1110")).Disposal
	before := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)
	rev, err := f.svc.ReverseDisposal(f.boss, f.propID, whole.ID, assets.ReverseDisposalInput{Reason: "wrong asset"})
	must(t, err)
	if rev.Status != assets.DisposalReversed || rev.ReversalJournalID == nil || f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID) != before+1 {
		t.Fatalf("%+v", rev)
	}
	wantLines(t, f, *rev.ReversalJournalID, map[string]string{"1110": "-18000000", "1592": "-21000000", "1530": "36000000", "7420": "3000000"})
	a := f.asDetail(t, ids[0])
	if a.Status != "ACTIVE" || f.countRows(t, `SELECT count(*) FROM fa_assets WHERE id = $1 AND disposed_on IS NOT NULL`, ids[0]) != 0 || !a.Cost.Equal(dec("36000000")) || !a.AccumDepr.Equal(dec("21000000")) || a.MonthsDone != 21 {
		t.Fatalf("%+v", a.Asset)
	}
	_, err = f.svc.ReverseDisposal(f.boss, f.propID, whole.ID, assets.ReverseDisposalInput{Reason: "again"})
	wantCode(t, err, "DISPOSAL_ALREADY_REVERSED")
	_, err = f.svc.ReverseDisposal(f.boss, f.propID, whole.ID, assets.ReverseDisposalInput{Reason: " "})
	wantCode(t, err, "VALIDATION_FAILED")
	// the asset can be disposed of again, and the register still agrees with its ledger
	f.disposeAs(t, f.admin, ids[0], f.writeOff(assets.ReasonDamaged))
	f.noDrift(t)
}

func TestAPartialDisposalIsReversedAndGetsItsPiecesBack(t *testing.T) {
	f := newFx(t)
	r := row("Linen sets", "OE", "36000000", "9000000", 6)
	r.UnitCount = ptr(200)
	id := f.withAssets(t, r)[0]
	in := f.writeOff(assets.ReasonLost)
	in.Units = ptr(int32(10))
	dsp := f.disposeAs(t, f.admin, id, in).Disposal
	_, err := f.svc.ReverseDisposal(f.boss, f.propID, dsp.ID, assets.ReverseDisposalInput{Reason: "counted again"})
	must(t, err)
	a := f.asDetail(t, id)
	if a.UnitCount != 200 || !a.Cost.Equal(dec("36000000")) || !a.AccumDepr.Equal(dec("9000000")) {
		t.Fatalf("%+v", a.Asset)
	}
	// a later change of the asset is a reason to keep a disposal: the second of two is not undone under the first
	again := f.writeOff(assets.ReasonLost)
	again.Units = ptr(int32(10))
	one := f.disposeAs(t, f.admin, id, again).Disposal
	two := f.writeOff(assets.ReasonLost)
	two.Units = ptr(int32(5))
	f.disposeAs(t, f.admin, id, two)
	_, err = f.svc.ReverseDisposal(f.boss, f.propID, one.ID, assets.ReverseDisposalInput{Reason: "no"})
	wantCode(t, err, "DISPOSAL_NOT_REVERSIBLE")
}

// The rule of the property (document 32): FREE posts at once; DISTINCT makes a request that another person decides; DISTINCT_ABOVE asks only above its limit.
func TestTheApprovalOfADisposalFollowsTheRuleOfTheProperty(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("A"), chair("B"), chair("C"), chair("D"), chair("E"))
	clerk := f.user(t, auth.PermAssetDispose, auth.PermAssetRead)
	manager := f.user(t, auth.PermAssetDispose)

	// FREE: the clerk disposes at once
	if r := f.disposeAs(t, clerk, ids[0], f.writeOff(assets.ReasonLost)); r.Status != assets.DisposalPosted {
		t.Fatalf("FREE: %+v", r)
	}

	// DISTINCT: a request waits; nothing is posted until the manager decides
	f.SetRule(t, f.tenantID, f.propID, string(iam.KindAssetDisposal), "DISTINCT")
	journals := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)
	in := f.sale("18000000", "1110")
	req := f.disposeAs(t, clerk, ids[1], in)
	if req.Status != assets.RequestPending || req.Request == nil || req.Disposal != nil || !req.Request.ApprovalAmount.Equal(dec("18000000")) {
		t.Fatalf("DISTINCT: %+v", req)
	}
	if n := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID); n != journals {
		t.Errorf("a request posted %d journals", n-journals)
	}
	if a := f.asDetail(t, ids[1]); !a.DisposalPending || a.Status != "ACTIVE" {
		t.Errorf("the register does not show the request: %+v", a.Asset)
	}
	if again, err := f.svc.Dispose(clerk, f.propID, ids[1], in); err != nil || !again.Replayed || again.Request.ID != req.Request.ID {
		t.Errorf("replay %+v %v", again, err)
	}
	// the asset is left alone while it waits: not disposed of twice, not moved, not changed, not depreciated
	wantCode(t, func() error { _, e := f.svc.Dispose(clerk, f.propID, ids[1], f.writeOff(assets.ReasonLost)); return e }(), "DISPOSAL_PENDING")
	wantCode(t, f.tryRevise(ids[1], assets.RevisionInput{Kind: assets.RevResidual, ResidualValue: ptr(dec("1"))}), "DISPOSAL_PENDING")
	_, err := f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{ids[1]}, ChangeDepartment: true, DepartmentID: ptr(f.dept(t, "ROOMS")), Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "DISPOSAL_PENDING")

	// the person who asked cannot approve their own; another with the permission can
	_, err = f.svc.ApproveDisposal(clerk, f.propID, req.Request.ID)
	wantCode(t, err, "APPROVAL_SAME_USER")
	ok, err := f.svc.ApproveDisposal(manager, f.propID, req.Request.ID)
	must(t, err)
	if ok.Status != assets.DisposalPosted || ok.Disposal == nil || ok.Request == nil || ok.Request.Status != assets.RequestApproved || ok.Request.DisposalID == nil {
		t.Fatalf("approved: %+v", ok)
	}
	wantLines(t, f, ok.Disposal.JournalID, map[string]string{"1110": "18000000", "1592": "21000000", "1530": "-36000000", "7420": "-3000000"})
	_, err = f.svc.ApproveDisposal(manager, f.propID, req.Request.ID)
	wantCode(t, err, "DISPOSAL_REQUEST_DECIDED")

	// a request is refused with a reason, and the asset is free again
	req2 := f.disposeAs(t, clerk, ids[2], f.writeOff(assets.ReasonLost))
	_, err = f.svc.RejectDisposal(manager, f.propID, req2.Request.ID, " ")
	wantCode(t, err, "VALIDATION_FAILED")
	no, err := f.svc.RejectDisposal(manager, f.propID, req2.Request.ID, "it is still in use")
	must(t, err)
	if no.Status != assets.RequestRejected || f.asDetail(t, ids[2]).DisposalPending {
		t.Fatalf("%+v", no)
	}
	_, err = f.svc.ApproveDisposal(manager, f.propID, req2.Request.ID)
	wantCode(t, err, "DISPOSAL_REQUEST_DECIDED")

	// the approver's credentials with the request post at once (a person at the desk with the manager beside them)
	in3 := f.writeOff(assets.ReasonDamaged)
	in3.Approval = f.approval()
	if r := f.disposeAs(t, clerk, ids[2], in3); r.Status != assets.DisposalPosted {
		t.Fatalf("credentials: %+v", r)
	}

	// DISTINCT_ABOVE: below the limit the clerk posts at once, above it a request waits
	f.SetRule(t, f.tenantID, f.propID, string(iam.KindAssetDisposal), "DISTINCT_ABOVE", "16000000")
	if r := f.disposeAs(t, clerk, ids[3], f.writeOff(assets.ReasonLost)); r.Status != assets.DisposalPosted { // the carrying value 15,000,000 is below
		t.Fatalf("below: %+v", r)
	}
	if r := f.disposeAs(t, clerk, ids[4], f.sale("20000000", "1110")); r.Status != assets.RequestPending { // the price 20,000,000 is above
		t.Fatalf("above: %+v", r)
	}
	// the reversal asks the same approval: the clerk needs the manager's credentials above the limit
	posted, err := f.svc.ListDisposals(f.admin, f.propID, nil)
	must(t, err)
	var big int64
	for _, p := range posted {
		if p.Proceeds.IsPositive() {
			big = p.ID
		}
	}
	_, err = f.svc.ReverseDisposal(clerk, f.propID, big, assets.ReverseDisposalInput{Reason: "no"})
	wantCode(t, err, "APPROVAL_REQUIRED")
	if _, err := f.svc.ReverseDisposal(clerk, f.propID, big, assets.ReverseDisposalInput{Reason: "no", Approval: f.approval()}); err != nil {
		t.Errorf("the reversal with the credentials: %v", err)
	}
}

// The depreciation of the month after the disposals and the cash flow that shows the proceeds under investing (F10, Q11, 10.11).
func TestExample1011TheCashFlowOfADisposal(t *testing.T) {
	f := newFx(t)
	id := f.withAssets(t, chair("Chair A"))[0]
	from, to := d("2026-09-02"), d("2026-09-30")
	flow, err := f.Accounting.CashFlow(f.admin, f.propID, &from, &to)
	must(t, err)
	if !flow.Investing.IsZero() || !flow.Operating.IsZero() {
		t.Fatalf("before the sale: %+v", flow)
	}
	f.disposeAs(t, f.admin, id, f.sale("18000000", "1110"))
	flow, err = f.Accounting.CashFlow(f.admin, f.propID, &from, &to)
	must(t, err)
	if !flow.NetIncome.Equal(dec("3000000")) || !flow.Operating.IsZero() || !flow.Investing.Equal(dec("18000000")) || !flow.NetChange.Equal(dec("18000000")) || !flow.Reconciled {
		t.Fatalf("net income %s, operating %s, investing %s, net %s, reconciled %v", flow.NetIncome, flow.Operating, flow.Investing, flow.NetChange, flow.Reconciled)
	}
	// a loss is added back under operating and taken off the proceeds under investing
	g := newFx(t)
	id2 := g.withAssets(t, chair("Chair A"))[0]
	g.disposeAs(t, g.admin, id2, g.sale("12000000", "1110"))
	flow, err = g.Accounting.CashFlow(g.admin, g.propID, &from, &to)
	must(t, err)
	if !flow.NetIncome.Equal(dec("-3000000")) || !flow.Operating.IsZero() || !flow.Investing.Equal(dec("12000000")) || !flow.Reconciled {
		t.Fatalf("loss: net income %s, operating %s, investing %s, reconciled %v", flow.NetIncome, flow.Operating, flow.Investing, flow.Reconciled)
	}
}

// F9: the depreciation of a month cannot be reversed once an asset of it was disposed of after the month (the writer of the row is the disposal itself now).
func TestReversalIsRefusedWhenARealDisposalCameAfterTheMonth(t *testing.T) {
	f := newFx(t)
	id := f.withAssets(t, chair("Chair A"), chair("Chair B"))[0]
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	f.advanceTo(t, "2026-10-05")
	f.disposeAs(t, f.admin, id, f.writeOff(assets.ReasonLost)) // dated 2026-10-05, after the run of September
	_, err := f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-09-01"), assets.ReverseRunInput{Reason: "wrong"})
	wantCode(t, err, "DEPRECIATION_NOT_REVERSIBLE")
	// undo the disposal and the run can be reversed again
	list, err := f.svc.ListDisposals(f.admin, f.propID, &id)
	must(t, err)
	_, err = f.svc.ReverseDisposal(f.boss, f.propID, list[0].ID, assets.ReverseDisposalInput{Reason: "kept"})
	must(t, err)
	if _, err := f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-09-01"), assets.ReverseRunInput{Reason: "wrong"}); err != nil {
		t.Errorf("after the reversal of the disposal: %v", err)
	}
}

// The lock order of the new use cases (level 47 after the accounting settings; the settings, then the asset).
func TestTheDisposalsTakeTheirLocksInOrder(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("A"), chair("B"))
	ctx, trace := db.WithLockTrace(f.admin)
	res, err := f.svc.Dispose(ctx, f.propID, ids[0], f.sale("18000000", "1110"))
	must(t, err)
	assertLockOrder(t, trace)
	if tb := tables(trace); !tb["fa_settings"] || !tb["fa_assets"] {
		t.Errorf("locked %v", tb)
	}
	ctx, trace = db.WithLockTrace(f.boss)
	_, err = f.svc.ReverseDisposal(ctx, f.propID, res.Disposal.ID, assets.ReverseDisposalInput{Reason: "x"})
	must(t, err)
	assertLockOrder(t, trace)

	f.SetRule(t, f.tenantID, f.propID, string(iam.KindAssetDisposal), "DISTINCT")
	clerk := f.user(t, auth.PermAssetDispose)
	manager := f.user(t, auth.PermAssetDispose)
	req := f.disposeAs(t, clerk, ids[1], f.writeOff(assets.ReasonLost))
	ctx, trace = db.WithLockTrace(manager)
	_, err = f.svc.ApproveDisposal(ctx, f.propID, req.Request.ID)
	must(t, err)
	assertLockOrder(t, trace)
}

// Races (the defined error for the one that loses).
func TestTwoDisposalsOfTheSameAssetMakeOne(t *testing.T) {
	for round := 0; round < 3; round++ {
		f := newFx(t)
		id := f.withAssets(t, chair("A"))[0]
		errs := f.race(2, func(i int) error {
			_, err := f.svc.Dispose(f.admin, f.propID, id, f.writeOff(assets.ReasonLost))
			return err
		})
		wins := 0
		for _, e := range errs {
			if e == nil {
				wins++
			} else if !isCode(e, "ASSET_STATUS_INVALID") {
				t.Fatalf("round %d: %v", round, e)
			}
		}
		if wins != 1 || f.countRows(t, `SELECT count(*) FROM fa_disposals WHERE asset_id = $1`, id) != 1 {
			t.Fatalf("round %d: %d disposals", round, wins)
		}
	}
}

func TestADisposalAndTheRunOfItsMonthDoNotBothDepreciateTheAsset(t *testing.T) {
	for round := 0; round < 3; round++ {
		f := newFx(t)
		id := f.withAssets(t, chair("A"), chair("B"))[0]
		f.advanceTo(t, "2026-10-01")
		in := f.writeOff(assets.ReasonLost)
		in.DisposalDate = ptr(d("2026-09-30"))
		errs := f.race(2, func(i int) error {
			if i == 0 {
				_, err := f.svc.Dispose(f.admin, f.propID, id, in)
				return err
			}
			_, err := f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
			return err
		})
		if errs[1] != nil {
			t.Fatalf("round %d: the run said %v", round, errs[1])
		}
		disposed := f.countRows(t, `SELECT count(*) FROM fa_disposals WHERE asset_id = $1`, id)
		lines := f.countRows(t, `SELECT count(*) FROM fa_depr_lines l JOIN fa_depr_runs r ON r.id = l.run_id WHERE l.asset_id = $1 AND r.status = 'POSTED'`, id)
		switch {
		case errs[0] == nil && (disposed != 1 || lines != 0):
			t.Fatalf("round %d: disposed and depreciated in the same month (%d, %d)", round, disposed, lines)
		case errs[0] != nil && !isCode(errs[0], "DISPOSAL_MONTH_DEPRECIATED"):
			t.Fatalf("round %d: the disposal said %v", round, errs[0])
		case errs[0] != nil && lines != 1:
			t.Fatalf("round %d: neither happened", round)
		}
	}
}

func TestADisposalAndAMoveOfTheSameAssetDoNotCross(t *testing.T) {
	for round := 0; round < 3; round++ {
		f := newFx(t)
		id := f.withAssets(t, chair("A"))[0]
		dept := f.dept(t, "ROOMS")
		errs := f.race(2, func(i int) error {
			if i == 0 {
				_, err := f.svc.Dispose(f.admin, f.propID, id, f.writeOff(assets.ReasonLost))
				return err
			}
			_, err := f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{id}, ChangeDepartment: true, DepartmentID: &dept, Reason: "x", IdempotencyKey: nextKey()})
			return err
		})
		if errs[0] != nil {
			t.Fatalf("round %d: the disposal said %v", round, errs[0])
		}
		if errs[1] != nil && !isCode(errs[1], "ASSET_STATUS_INVALID") {
			t.Fatalf("round %d: the move said %v", round, errs[1])
		}
		if f.asDetail(t, id).Status != "DISPOSED" {
			t.Fatalf("round %d: not disposed", round)
		}
	}
}

func TestTwoApprovalsOfTheSameRequestMakeOneDisposal(t *testing.T) {
	f := newFx(t)
	id := f.withAssets(t, chair("A"))[0]
	f.SetRule(t, f.tenantID, f.propID, string(iam.KindAssetDisposal), "DISTINCT")
	clerk := f.user(t, auth.PermAssetDispose)
	req := f.disposeAs(t, clerk, id, f.writeOff(assets.ReasonLost))
	m1, m2 := f.user(t, auth.PermAssetDispose), f.user(t, auth.PermAssetDispose)
	errs := f.race(2, func(i int) error {
		ctx := m1
		if i == 1 {
			ctx = m2
		}
		_, err := f.svc.ApproveDisposal(ctx, f.propID, req.Request.ID)
		return err
	})
	wins := 0
	for _, e := range errs {
		if e == nil {
			wins++
		} else if !isCode(e, "DISPOSAL_REQUEST_DECIDED") {
			t.Fatalf("%v", e)
		}
	}
	if wins != 1 || f.countRows(t, `SELECT count(*) FROM fa_disposals WHERE asset_id = $1`, id) != 1 {
		t.Fatalf("%d approvals won", wins)
	}
}

var _ = time.Second
var _ = decimal.Zero

func TestAnAssetThatWaitsForItsDisposalIsLeftOutOfTheRun(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("A"), chair("B"))
	f.SetRule(t, f.tenantID, f.propID, string(iam.KindAssetDisposal), "DISTINCT")
	clerk := f.user(t, auth.PermAssetDispose)
	req := f.disposeAs(t, clerk, ids[0], f.writeOff(assets.ReasonLost))
	if req.Status != assets.RequestPending {
		t.Fatalf("%+v", req)
	}
	f.advanceTo(t, "2026-10-01")
	seen := map[int64]bool{}
	for _, l := range f.preview(t, "2026-09").Lines {
		seen[l.AssetID] = true
	}
	if seen[ids[0]] || !seen[ids[1]] {
		t.Errorf("the run has %v", seen)
	}
}
