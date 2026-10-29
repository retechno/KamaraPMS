package assets_test

import (
	"context"
	"testing"

	"kamarapms/internal/assets"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Tests of PR 4, the stock-take on a phone (docs/architecture/37-fixed-assets.md 4.10, A14).

func (f *fx) location(t *testing.T, code string) assets.Location {
	t.Helper()
	l, err := f.svc.CreateLocation(f.admin, f.propID, assets.LocationInput{Code: code, Name: "Place " + code})
	must(t, err)
	return l
}

func (f *fx) putIn(t *testing.T, loc assets.Location, ids ...int64) {
	t.Helper()
	f.move(t, ids, assets.MoveInput{ChangeLocation: true, LocationID: &loc.ID, Reason: "set up"})
}

func (f *fx) startAudit(t *testing.T, loc *assets.Location) assets.AuditDetail {
	t.Helper()
	in := assets.StartAuditInput{IdempotencyKey: nextKey()}
	if loc != nil {
		in.LocationID = &loc.ID
	}
	a, err := f.svc.StartAudit(f.admin, f.propID, in)
	must(t, err)
	return a
}

func (f *fx) scan(t *testing.T, auditID int64, in assets.ScanInput) assets.ScanResult {
	t.Helper()
	r, err := f.svc.ScanAsset(f.admin, f.propID, auditID, in)
	must(t, err)
	return r
}

// stocktake sets up the places L1 and L2 with: a, b, c in L1; d in L2; e without a place.
func (f *fx) stocktake(t *testing.T) (l1, l2 assets.Location, ids []int64) {
	t.Helper()
	ids = f.withAssets(t, chair("a"), chair("b"), chair("c"), chair("d"), chair("e"))
	l1, l2 = f.location(t, "L1"), f.location(t, "L2")
	f.putIn(t, l1, ids[0], ids[1], ids[2])
	f.putIn(t, l2, ids[3])
	return l1, l2, ids
}

func TestStocktakeScansAndCloseMakeProposals(t *testing.T) {
	f := newFx(t)
	l1, l2, ids := f.stocktake(t)
	au := f.startAudit(t, &l1)
	if au.Status != assets.AuditInProgress || au.Number == "" || au.Counts.Expected != 3 || len(au.Unscanned) != 3 {
		t.Fatalf("%+v", au.Audit)
	}
	// a second one for the same place is refused
	_, err := f.svc.StartAudit(f.admin, f.propID, assets.StartAuditInput{LocationID: &l1.ID, IdempotencyKey: nextKey()})
	wantCode(t, err, "AUDIT_ALREADY_OPEN")
	// the same key replays
	key := nextKey()
	first, err := f.svc.StartAudit(f.admin, f.propID, assets.StartAuditInput{LocationID: &l2.ID, IdempotencyKey: key})
	must(t, err)
	again, err := f.svc.StartAudit(f.admin, f.propID, assets.StartAuditInput{LocationID: &l2.ID, IdempotencyKey: key})
	must(t, err)
	if first.ID != again.ID {
		t.Errorf("replay %d %d", first.ID, again.ID)
	}

	a := f.asDetail(t, ids[0])
	var label string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT label_code FROM fa_assets WHERE id = $1`, a.ID).Scan(&label))
	r := f.scan(t, au.ID, assets.ScanInput{Code: "FA:" + label})
	if r.Line.Result != assets.ScanFound || r.Counts.Found != 1 {
		t.Fatalf("%+v", r)
	}
	// the number typed by hand, and a second scan of the same asset, are the same line
	f.scan(t, au.ID, assets.ScanInput{Code: a.Number})
	// an asset of another place found here
	r = f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[3]})
	if r.Line.Result != assets.ScanMoved || r.Line.ExpectedLocationName == nil || r.Line.FoundLocationName == nil {
		t.Fatalf("%+v", r.Line)
	}
	// an asset without a place
	r = f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[4]})
	if r.Line.Result != assets.ScanUnexpected {
		t.Fatalf("%+v", r.Line)
	}
	// damaged needs a note
	_, err = f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{AssetID: &ids[2], Damaged: true})
	wantCode(t, err, "VALIDATION_FAILED")
	r = f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[2], Damaged: true, Note: "the leg is broken"})
	if r.Line.Result != assets.ScanDamaged {
		t.Fatalf("%+v", r.Line)
	}
	// a place that is not the one of the stock-take
	_, err = f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0], LocationCode: "FL:" + l2.LabelCode})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{Code: "FA:NOSUCHCODE"})
	wantCode(t, err, "ASSET_NOT_FOUND")

	res, err := f.svc.CloseAudit(f.admin, f.propID, au.ID)
	must(t, err)
	// b was expected and not scanned
	if res.Missing != 1 || res.Audit.Status != assets.AuditClosed || res.Audit.Counts.Missing != 1 {
		t.Fatalf("%+v", res)
	}
	// proposals: d moves to L1, e moves to L1, b and c (damaged) are written off
	props, err := f.svc.ListProposals(f.admin, f.propID, "", &au.ID)
	must(t, err)
	kinds := map[int64]string{}
	for _, p := range props {
		kinds[p.AssetID] = p.Kind
		if p.Status != assets.ProposalPending {
			t.Errorf("%+v", p)
		}
	}
	want := map[int64]string{ids[1]: assets.ProposalWriteOff, ids[2]: assets.ProposalWriteOff, ids[3]: assets.ProposalMove, ids[4]: assets.ProposalMove}
	if len(kinds) != len(want) {
		t.Fatalf("%v, want %v", kinds, want)
	}
	for id, k := range want {
		if kinds[id] != k {
			t.Errorf("asset %d: %q, want %q", id, kinds[id], k)
		}
	}
	// nothing was posted and no asset changed
	if got := f.asDetail(t, ids[1]); got.Status != "ACTIVE" {
		t.Errorf("%s", got.Status)
	}
	// closed: no more scans, no second close
	_, err = f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0]})
	wantCode(t, err, "AUDIT_NOT_OPEN")
	_, err = f.svc.CloseAudit(f.admin, f.propID, au.ID)
	wantCode(t, err, "AUDIT_NOT_OPEN")
	// the report of the differences lists them with the book value at risk
	diff, err := f.svc.AuditDifferences(f.admin, f.propID, &au.ID, "", nil, nil)
	must(t, err)
	if len(diff) != 4 {
		t.Fatalf("%d differences", len(diff))
	}
	f.noDrift(t)
}

func TestStocktakeOfTheWholePropertyNeedsThePlace(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	au := f.startAudit(t, nil)
	_, err := f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0]})
	wantCode(t, err, "VALIDATION_FAILED")
	r := f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[0], LocationCode: "FL:" + l1.LabelCode})
	if r.Line.Result != assets.ScanFound {
		t.Fatalf("%+v", r.Line)
	}
	r = f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[3], LocationCode: l1.Code})
	if r.Line.Result != assets.ScanMoved {
		t.Fatalf("%+v", r.Line)
	}
}

func TestProposalsAreAppliedUnderThePermissionOfThePerson(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	au := f.startAudit(t, &l1)
	f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[0]})
	f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[3]})
	_, err := f.svc.CloseAudit(f.admin, f.propID, au.ID)
	must(t, err)
	props, err := f.svc.ListProposals(f.admin, f.propID, assets.ProposalPending, &au.ID)
	must(t, err)
	byAsset := map[int64]assets.Proposal{}
	for _, p := range props {
		byAsset[p.AssetID] = p
	}
	move, off := byAsset[ids[3]], byAsset[ids[1]]
	if move.Kind != assets.ProposalMove || off.Kind != assets.ProposalWriteOff {
		t.Fatalf("%+v %+v", move, off)
	}
	manage := f.user(t, auth.PermAssetManage, auth.PermAssetRead)
	dispose := f.user(t, auth.PermAssetDispose, auth.PermAssetRead)
	reader := f.user(t, auth.PermAssetRead)

	_, err = f.svc.ApplyProposal(reader, f.propID, move.ID, assets.ApplyProposalInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	// a write-off asks asset.dispose, a move asks asset.manage
	_, err = f.svc.ApplyProposal(manage, f.propID, off.ID, assets.ApplyProposalInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.svc.ApplyProposal(dispose, f.propID, move.ID, assets.ApplyProposalInput{})
	wantCode(t, err, "PERMISSION_DENIED")
	if got := f.asDetail(t, ids[3]); got.LocationID == nil || *got.LocationID == l1.ID {
		t.Fatalf("a refused proposal moved the asset: %+v", got.Asset)
	}

	applied, err := f.svc.ApplyProposal(manage, f.propID, move.ID, assets.ApplyProposalInput{})
	must(t, err)
	if applied.Proposal.Status != assets.ProposalApplied || applied.Proposal.ResultType == nil || *applied.Proposal.ResultType != "MOVE" || applied.Proposal.ResultID == nil {
		t.Fatalf("%+v", applied.Proposal)
	}
	if got := f.asDetail(t, ids[3]); got.LocationID == nil || *got.LocationID != l1.ID {
		t.Fatalf("%+v", got.Asset)
	}
	_, err = f.svc.ApplyProposal(manage, f.propID, move.ID, assets.ApplyProposalInput{})
	wantCode(t, err, "PROPOSAL_NOT_PENDING")

	// the write-off runs the ordinary disposal, with the approval
	done, err := f.svc.ApplyProposal(f.boss, f.propID, off.ID, assets.ApplyProposalInput{Approval: f.approval()})
	must(t, err)
	rt := done.Proposal.ResultType
	if done.Proposal.Status != assets.ProposalApplied || rt == nil || (*rt != "DISPOSAL" && *rt != "DISPOSAL_REQUEST") {
		t.Fatalf("%+v", done.Proposal)
	}
	if *rt == "DISPOSAL" {
		if got := f.asDetail(t, ids[1]); got.Status == "ACTIVE" {
			t.Errorf("the asset is still active: %s", got.Status)
		}
	}
	f.noDrift(t)
}

func TestAProposalCanBeRejectedWithAReason(t *testing.T) {
	f := newFx(t)
	l1, _, _ := f.stocktake(t)
	au := f.startAudit(t, &l1)
	_, err := f.svc.CloseAudit(f.admin, f.propID, au.ID)
	must(t, err)
	props, err := f.svc.ListProposals(f.admin, f.propID, assets.ProposalPending, &au.ID)
	must(t, err)
	if len(props) != 3 {
		t.Fatalf("%d proposals", len(props))
	}
	_, err = f.svc.RejectProposal(f.admin, f.propID, props[0].ID, "  ")
	wantCode(t, err, "VALIDATION_FAILED")
	p, err := f.svc.RejectProposal(f.admin, f.propID, props[0].ID, "found it the next day")
	must(t, err)
	if p.Status != assets.ProposalRejected || p.DecisionNote == nil {
		t.Fatalf("%+v", p)
	}
	_, err = f.svc.RejectProposal(f.admin, f.propID, props[0].ID, "again")
	wantCode(t, err, "PROPOSAL_NOT_PENDING")
	_, err = f.svc.ApplyProposal(f.admin, f.propID, props[0].ID, assets.ApplyProposalInput{})
	wantCode(t, err, "PROPOSAL_NOT_PENDING")
	_, err = f.svc.RejectProposal(f.admin, f.propID, 999999, "x")
	wantCode(t, err, "PROPOSAL_NOT_FOUND")
}

func TestStocktakePermissionsAndTenants(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	reader := f.user(t, auth.PermAssetRead)
	nobody := f.user(t)
	if _, err := f.svc.StartAudit(reader, f.propID, assets.StartAuditInput{LocationID: &l1.ID, IdempotencyKey: nextKey()}); !isCode(err, "PERMISSION_DENIED") {
		t.Errorf("start: %v", err)
	}
	au := f.startAudit(t, &l1)
	if _, err := f.svc.ScanAsset(reader, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0]}); !isCode(err, "PERMISSION_DENIED") {
		t.Errorf("scan: %v", err)
	}
	if _, err := f.svc.CloseAudit(reader, f.propID, au.ID); !isCode(err, "PERMISSION_DENIED") {
		t.Errorf("close: %v", err)
	}
	if _, err := f.svc.GetAudit(nobody, f.propID, au.ID); !isCode(err, "PERMISSION_DENIED") {
		t.Errorf("get: %v", err)
	}
	if _, err := f.svc.GetAudit(reader, f.propID, au.ID); err != nil {
		t.Errorf("a reader sees it: %v", err)
	}
	if _, err := f.svc.GetAudit(f.admin, f.propID, 999999); !isCode(err, "AUDIT_NOT_FOUND") {
		t.Errorf("unknown: %v", err)
	}
	// a property of another tenant is not found, with any id
	other := f.Tenant(t, "XYZ")
	op := f.PropertyIn(t, other.ID, "OTH", "IDR", 0)
	if _, err := f.svc.GetAudit(f.admin, op.ID, au.ID); !isCode(err, "PROPERTY_NOT_FOUND") {
		t.Errorf("foreign: %v", err)
	}
}

func TestTwoScansOfTheSameAssetAtOnceMakeOneLine(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	au := f.startAudit(t, &l1)
	errs := f.race(6, func(i int) error {
		_, err := f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0]})
		return err
	})
	for _, err := range errs {
		must(t, err)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_audit_lines WHERE audit_id = $1 AND asset_id = $2`, au.ID, ids[0]); n != 1 {
		t.Errorf("%d lines", n)
	}
}

func TestClosingAgainstADisposalAndAScan(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	au := f.startAudit(t, &l1)
	var out [3]error
	errs := f.race(3, func(i int) error {
		switch i {
		case 0:
			_, err := f.svc.CloseAudit(f.admin, f.propID, au.ID)
			return err
		case 1:
			_, _ = f.svc.Dispose(f.boss, f.propID, ids[1], f.writeOff(assets.ReasonLost)) // either order is fine: the outcome is checked below
			return nil
		default:
			_, err := f.svc.ScanAsset(f.admin, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0]})
			return err
		}
	})
	copy(out[:], errs)
	if out[0] != nil {
		t.Fatalf("close: %v", out[0])
	}
	// the scan came before the close, or it was refused after it
	if out[2] != nil && !isCode(out[2], "AUDIT_NOT_OPEN") {
		t.Fatalf("scan: %v", out[2])
	}
	// whichever came first, each asset has at most one line and the audit is closed once
	if n := f.countRows(t, `SELECT count(*) FROM fa_audits WHERE id = $1 AND status = 'CLOSED'`, au.ID); n != 1 {
		t.Errorf("closed %d", n)
	}
	if n := f.countRows(t, `SELECT count(*) FROM (SELECT asset_id FROM fa_audit_lines WHERE audit_id = $1 GROUP BY asset_id HAVING count(*) > 1) x`, au.ID); n != 0 {
		t.Errorf("%d assets with two lines", n)
	}
}

func TestAnAssetDisposedOfBeforeTheCloseIsNotMissing(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	au := f.startAudit(t, &l1)
	f.disposeAs(t, f.boss, ids[1], f.writeOff(assets.ReasonLost))
	f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[0]})
	f.scan(t, au.ID, assets.ScanInput{AssetID: &ids[2]})
	res, err := f.svc.CloseAudit(f.admin, f.propID, au.ID)
	must(t, err)
	if res.Missing != 0 || res.Proposals != 0 {
		t.Fatalf("%+v", res)
	}
}

func TestStocktakeLocksFollowTheOrder(t *testing.T) {
	f := newFx(t)
	l1, _, ids := f.stocktake(t)
	au := f.startAudit(t, &l1)
	ctx, trace := db.WithLockTrace(f.admin)
	_, err := f.svc.ScanAsset(ctx, f.propID, au.ID, assets.ScanInput{AssetID: &ids[0]})
	must(t, err)
	assertLockOrder(t, trace)
	ctx, trace = db.WithLockTrace(f.admin)
	_, err = f.svc.CloseAudit(ctx, f.propID, au.ID)
	must(t, err)
	assertLockOrder(t, trace)
	if tb := tables(trace); !tb["fa_settings"] || !tb["fa_assets"] || !tb["fa_audits"] {
		t.Errorf("locked %v", tb)
	}
}

// a plan that has the stock module, or not
type plan bool

func (p plan) HasInventory(context.Context, int64) (bool, error) { return bool(p), nil }

func TestARepairRequestNamesAnAsset(t *testing.T) {
	f := newFx(t)
	ids := f.withAssets(t, chair("a"), chair("b"))
	f.Maintenance.SetAssetModule(plan(true))
	req, err := f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{AssetID: &ids[0], Location: "Lobby", Category: "furniture", Description: "the chair wobbles", Priority: "low"})
	must(t, err)
	if req.AssetID == nil || *req.AssetID != ids[0] || req.AssetNumber == "" {
		t.Fatalf("%+v", req)
	}
	// the Repairs tab lists it, with no money
	reps, err := f.svc.AssetRepairs(f.user(t, auth.PermAssetRead), f.propID, ids[0])
	must(t, err)
	if len(reps) != 1 || reps[0].RequestNumber != req.RequestNumber {
		t.Fatalf("%+v", reps)
	}
	if other, err := f.svc.AssetRepairs(f.admin, f.propID, ids[1]); err != nil || len(other) != 0 {
		t.Fatalf("%v %+v", err, other)
	}
	_, err = f.svc.AssetRepairs(f.user(t), f.propID, ids[0])
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.svc.AssetRepairs(f.admin, f.propID, 999999)
	wantCode(t, err, "ASSET_NOT_FOUND")
	// the link is changed and taken away with a patch
	req, err = f.Maintenance.Update(f.admin, f.propID, req.ID, maintenance.Patch{AssetID: &ids[1]})
	must(t, err)
	if req.AssetID == nil || *req.AssetID != ids[1] {
		t.Fatalf("%+v", req)
	}
	zero := int64(0)
	req, err = f.Maintenance.Update(f.admin, f.propID, req.ID, maintenance.Patch{AssetID: &zero})
	must(t, err)
	if req.AssetID != nil {
		t.Fatalf("%+v", req)
	}
	// an unknown asset, an asset of another property, an asset that was disposed of
	_, err = f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{AssetID: ptr(int64(999999)), Location: "x", Category: "other", Description: "x", Priority: "low"})
	wantCode(t, err, "ASSET_NOT_FOUND")
	f.disposeAs(t, f.boss, ids[1], f.writeOff(assets.ReasonLost))
	_, err = f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{AssetID: &ids[1], Location: "x", Category: "other", Description: "x", Priority: "low"})
	wantCode(t, err, "ASSET_STATUS_INVALID")
	// a plan without the stock module cannot name an asset, and a request without one still works
	f.Maintenance.SetAssetModule(plan(false))
	_, err = f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{AssetID: &ids[0], Location: "x", Category: "other", Description: "x", Priority: "low"})
	wantCode(t, err, "MODULE_NOT_IN_PLAN")
	_, err = f.Maintenance.Create(f.admin, f.propID, maintenance.CreateInput{Location: "x", Category: "other", Description: "no asset", Priority: "low"})
	must(t, err)
}
