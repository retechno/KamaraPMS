package assets_test

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Tests of PR 3, first part: the moves and the revisions (docs/architecture/37-fixed-assets.md 4.5, 4.6 and the examples 10.8 and 10.13).

func (f *fx) categoryID(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM fa_categories WHERE property_id = $1 AND code = $2`, f.propID, code).Scan(&id))
	return id
}

func (f *fx) revise(t *testing.T, id int64, in assets.RevisionInput) assets.RevisionResult {
	t.Helper()
	if in.Reason == "" {
		in.Reason = "because"
	}
	in.IdempotencyKey = nextKey()
	r, err := f.svc.ReviseAsset(f.admin, f.propID, id, in)
	must(t, err)
	return r
}

func (f *fx) tryRevise(id int64, in assets.RevisionInput) error {
	if in.Reason == "" {
		in.Reason = "because"
	}
	in.IdempotencyKey = nextKey()
	_, err := f.svc.ReviseAsset(f.admin, f.propID, id, in)
	return err
}

func (f *fx) move(t *testing.T, ids []int64, in assets.MoveInput) assets.MoveResult {
	t.Helper()
	in.AssetIDs = ids
	if in.Reason == "" {
		in.Reason = "because"
	}
	in.IdempotencyKey = nextKey()
	r, err := f.svc.MoveAssets(f.admin, f.propID, in)
	must(t, err)
	return r
}

// runDept is the department the posted run of a month gave to an asset.
func (f *fx) runDept(t *testing.T, month string, assetID int64) *int64 {
	t.Helper()
	var dept *int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT l.department_id FROM fa_depr_lines l JOIN fa_depr_runs r ON r.id = l.run_id
		 WHERE r.property_id = $1 AND r.period_month = $2 AND r.status = 'POSTED' AND l.asset_id = $3`, f.propID, month+"-01", assetID).Scan(&dept))
	return dept
}

func (f *fx) nextAmount(t *testing.T, month string, assetID int64) decimal.Decimal {
	t.Helper()
	for _, l := range f.preview(t, month).Lines {
		if l.AssetID == assetID {
			return l.Amount
		}
	}
	t.Fatalf("asset %d is not in the preview of %s", assetID, month)
	return decimal.Zero
}

// ---------------------------------------------------------------------------------------------------------------------
// 10.8: life, value added, construction finished

func TestExample108NewLifeIsProspective(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	f.exec(t, `UPDATE fa_categories SET life_months = 36 WHERE property_id = $1 AND code = 'FFE'`, f.propID)
	a := f.asset(t, "2026-09-02", "1530", "36000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	f.advanceTo(t, "2026-11-01")
	f.run(t, "2026-10")
	if x := f.asDetail(t, a.ID); !x.AccumDepr.Equal(dec("2000000")) || x.MonthsDone != 2 {
		t.Fatalf("%+v", x.Asset)
	}
	if got := f.nextAmount(t, "2026-11", a.ID); !got.Equal(dec("1000000")) {
		t.Fatalf("before the change: %s", got)
	}
	journals := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)

	// a life that leaves no month for what is left is refused
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(2))}), "LIFE_TOO_SHORT")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(1))}), "LIFE_TOO_SHORT")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(36))}), "VALIDATION_FAILED") // the life it has
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife}), "VALIDATION_FAILED")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(48)), Reason: " "}), "VALIDATION_FAILED")

	ctx, trace := db.WithLockTrace(f.admin)
	key := nextKey()
	res, err := f.svc.ReviseAsset(ctx, f.propID, a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(48)), Reason: "the chairs last longer", IdempotencyKey: key})
	must(t, err)
	assertLockOrder(t, trace)
	if tb := tables(trace); !tb["fa_settings"] || !tb["fa_assets"] {
		t.Errorf("locked %v", tb)
	}
	if res.Replayed || res.RevisionID == 0 || res.JournalID != nil || res.LifeMonths == nil || *res.LifeMonths != 48 || !res.AccumDepr.Equal(dec("2000000")) {
		t.Fatalf("%+v", res)
	}
	// 10.8: the months left are 48 - 2 and the month is (36,000,000 - 2,000,000) / 46, from the next run; nothing posted changed and no journal was made
	want := dec("34000000").Div(dec("46")).Round(0)
	if got := f.nextAmount(t, "2026-11", a.ID); !got.Equal(want) {
		t.Fatalf("after the change: %s, want %s", got, want)
	}
	if n := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID); n != journals {
		t.Errorf("the revision posted %d journals", n-journals)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_revisions WHERE property_id = $1 AND asset_id = $2 AND kind = 'LIFE' AND old_value->>'life_months' = '36' AND new_value->>'life_months' = '48'`, f.propID, a.ID); n != 1 {
		t.Errorf("%d revisions", n)
	}
	// the same key replays the first answer and writes nothing
	again, err := f.svc.ReviseAsset(f.admin, f.propID, a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(48)), Reason: "the chairs last longer", IdempotencyKey: key})
	must(t, err)
	if !again.Replayed || again.RevisionID != res.RevisionID {
		t.Fatalf("%+v", again)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_revisions WHERE property_id = $1`, f.propID); n != 1 {
		t.Errorf("%d revisions after the replay", n)
	}
	// the history of the asset lists the revision and the two months
	h, err := f.svc.GetHistory(f.admin, f.propID, a.ID)
	must(t, err)
	kinds := ""
	for _, e := range h {
		kinds += e.Kind + " "
	}
	if kinds != "DEPRECIATION DEPRECIATION LIFE " {
		t.Errorf("history: %s", kinds)
	}
}

func TestResidualAndMethodRevisions(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")

	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevResidual, ResidualValue: ptr(dec("95000001"))}), "VALIDATION_FAILED") // above the carrying value
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevResidual, ResidualValue: ptr(dec("-1"))}), "VALIDATION_FAILED")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevResidual, ResidualValue: ptr(dec("0"))}), "VALIDATION_FAILED") // the one it has
	f.revise(t, a.ID, assets.RevisionInput{Kind: assets.RevResidual, ResidualValue: ptr(dec("9500000"))})
	// (96,000,000 - 1,000,000 - 9,500,000) / 95 months left
	if got, want := f.nextAmount(t, "2026-10", a.ID), dec("85500000").Div(dec("95")).Round(0); !got.Equal(want) {
		t.Fatalf("residual: %s, want %s", got, want)
	}
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevMethod, Method: ptr("XX")}), "VALIDATION_FAILED")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevMethod, Method: ptr("SL")}), "VALIDATION_FAILED")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevMethod, Method: ptr("DB"), DBFactor: ptr(dec("9"))}), "VALIDATION_FAILED")
	f.revise(t, a.ID, assets.RevisionInput{Kind: assets.RevMethod, Method: ptr("DB")})
	x := f.asDetail(t, a.ID)
	if x.Method == nil || *x.Method != "DB" || x.DBFactor == nil || !x.DBFactor.Equal(dec("2")) {
		t.Fatalf("%+v", x.Asset)
	}
	// double declining: the larger of (V x 2 / N) and the straight line over what is left
	if got := f.nextAmount(t, "2026-10", a.ID); !got.Equal(dec("95000000").Mul(dec("2")).Div(dec("96")).Round(0)) {
		t.Fatalf("method: %s", got)
	}
	f.revise(t, a.ID, assets.RevisionInput{Kind: assets.RevMethod, Method: ptr("SL")})
	if x := f.asDetail(t, a.ID); *x.Method != "SL" || x.DBFactor != nil {
		t.Fatalf("%+v", x.Asset)
	}
}

func TestRevisionNeedsTheMonthsBeforeToBeRun(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-15")
	// September is due and was not run: a change would reach into it
	err := f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(60))})
	wantCode(t, err, "DEPRECIATION_NOT_RUN")
	f.advanceTo(t, "2026-10-16")
	f.run(t, "2026-09")
	must(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(60))}))
	// whoever cannot manage assets cannot revise or move
	_, err = f.svc.ReviseAsset(f.user(t, auth.PermAssetRead), f.propID, a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(61)), Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.svc.MoveAssets(f.user(t, auth.PermAssetRead), f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, ChangeDepartment: true, Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.svc.GetHistory(f.user(t, auth.PermAssetRead), f.propID, a.ID)
	must(t, err)
}

func TestExample108ValueAddedFromAWaitingLine(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1520", "2400000000", nil) // a building, 240 months: 10,000,000 a month
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	j := f.post(t, "2026-10-01", "1520", "3900", "360000000") // the renovation, already debited
	line := f.lineOf(t, j.Number, "1520")
	other := f.lineOf(t, f.post(t, "2026-10-01", "1530", "3900", "5000000").Number, "1530")
	journals := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)

	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCapitalize, JournalLineID: &other.LineID}), "VALIDATION_FAILED") // the account of another category
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCapitalize}), "VALIDATION_FAILED")
	ctx, trace := db.WithLockTrace(f.admin)
	key := nextKey()
	res, err := f.svc.ReviseAsset(ctx, f.propID, a.ID, assets.RevisionInput{Kind: assets.RevCapitalize, JournalLineID: &line.LineID, Reason: "lobby renovation", IdempotencyKey: key})
	must(t, err)
	assertLockOrder(t, trace)
	if tb := tables(trace); !tb["accounting_settings"] || !tb["fa_settings"] || !tb["fa_assets"] {
		t.Errorf("locked %v", tb)
	}
	// 10.8: cost 2,760,000,000, the same 239 months left; no journal; one CAPITALIZE row and one revision
	if !res.Cost.Equal(dec("2760000000")) || !res.AccumDepr.Equal(dec("10000000")) || res.MonthsDone != 1 || res.JournalID == nil {
		t.Fatalf("%+v", res.Asset)
	}
	if n := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID); n != journals {
		t.Errorf("the value added posted %d journals", n-journals)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_ledger WHERE property_id = $1 AND asset_id = $2 AND kind = 'CAPITALIZE' AND cost_delta = 360000000`, f.propID, a.ID); n != 1 {
		t.Errorf("%d CAPITALIZE rows", n)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_source_links WHERE property_id = $1 AND asset_id = $2 AND kind = 'CAPITALIZE' AND released_at IS NULL`, f.propID, a.ID); n != 1 {
		t.Errorf("%d links", n)
	}
	if res.SourceLink == nil || res.SourceLink.Cost.Equal(dec("360000000")) { // the source of the asset is its own line, not the value added
		t.Errorf("source link %+v", res.SourceLink)
	}
	// the line leaves the waiting list, and it cannot be used twice
	for _, w := range f.waiting(t, assets.WaitingFilter{}).Data {
		if w.LineID == line.LineID {
			t.Error("the line still waits")
		}
	}
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCapitalize, JournalLineID: &line.LineID}), "LINE_ALREADY_ASSET")
	_, err = f.svc.MakeAssetFromLine(f.admin, f.propID, assets.MakeAssetInput{JournalLineID: line.LineID, Units: 1, IdempotencyKey: nextKey()})
	wantCode(t, err, "LINE_ALREADY_ASSET")
	// the asset cannot be cancelled once value was added to it
	_, err = f.svc.CancelAsset(f.admin, f.propID, a.ID, assets.CancelInput{Reason: "x"})
	wantCode(t, err, "ASSET_HAS_DEPRECIATION")
	// the same key replays
	again, err := f.svc.ReviseAsset(f.admin, f.propID, a.ID, assets.RevisionInput{Kind: assets.RevCapitalize, JournalLineID: &line.LineID, Reason: "lobby renovation", IdempotencyKey: key})
	must(t, err)
	if !again.Replayed || !again.Cost.Equal(dec("2760000000")) {
		t.Fatalf("%+v", again.Asset)
	}
	// 12,000,000 a month in the book of the example (60 months done); here one month is done: (2,760,000,000 - 10,000,000) / 239
	f.advanceTo(t, "2026-11-01")
	if got, want := f.nextAmount(t, "2026-10", a.ID), dec("2750000000").Div(dec("239")).Round(0); !got.Equal(want) {
		t.Fatalf("after the value added: %s, want %s", got, want)
	}
}

func TestExample108ConstructionFinished(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1560", "1200000000", nil) // 1560 holds 1,200,000,000 for the annex
	if x := f.asDetail(t, a.ID); x.LifeMonths != nil || x.FirstDepMonth != nil {
		t.Fatalf("%+v", x.Asset)
	}
	building := f.categoryID(t, "BUILDING")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCategory}), "VALIDATION_FAILED")
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCategory, CategoryID: ptr(f.categoryID(t, "CIP"))}), "VALIDATION_FAILED") // the one it has
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCategory, CategoryID: &building, InServiceDate: ptr(d("2026-12-01"))}), "VALIDATION_FAILED")
	ctx, trace := db.WithLockTrace(f.admin)
	res, err := f.svc.ReviseAsset(ctx, f.propID, a.ID, assets.RevisionInput{Kind: assets.RevCategory, CategoryID: &building, Reason: "the annex is open", IdempotencyKey: nextKey()})
	must(t, err)
	assertLockOrder(t, trace)
	if res.JournalID == nil || res.JournalNumber == nil || res.CategoryCode != "BUILDING" || res.LifeMonths == nil || *res.LifeMonths != 240 || res.Method == nil || *res.Method != "SL" ||
		res.FirstDepMonth == nil || !res.Cost.Equal(dec("1200000000")) || res.FiscalGroup != "BLD_PERMANENT" {
		t.Fatalf("%+v", res.Asset)
	}
	// 10.8: Dr 1520 / Cr 1560 of 1,200,000,000, type ASSET, dated the open day
	lines := f.journalLines(t, *res.JournalID)
	if len(lines) != 2 || !lines["1520"].Equal(dec("1200000000")) || !lines["1560"].Equal(dec("-1200000000")) {
		t.Fatalf("journal %v", lines)
	}
	var typ, date string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT journal_type, journal_date::text FROM gl_journals WHERE id = $1`, *res.JournalID).Scan(&typ, &date))
	if typ != "ASSET" || date != "2026-09-30" {
		t.Errorf("journal %s %s", typ, date)
	}
	// two rows of the ledger, one out of the old category and one into the new, and the asset keeps its totals
	if n := f.countRows(t, `SELECT count(*) FROM fa_ledger WHERE property_id = $1 AND asset_id = $2 AND ((kind = 'RECLASS_OUT' AND cost_delta = -1200000000 AND category_id = $3)
		OR (kind = 'RECLASS_IN' AND cost_delta = 1200000000 AND category_id = $4)) AND journal_id = $5`, f.propID, a.ID, f.categoryID(t, "CIP"), building, *res.JournalID); n != 2 {
		t.Errorf("%d reclass rows", n)
	}
	drift, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	for _, i := range drift.Issues {
		if i.Code == "STATE_DRIFT" {
			t.Errorf("drift %+v", i)
		}
	}
	// the asset is depreciated from the completion: 1,200,000,000 / 240 = 5,000,000
	f.advanceTo(t, "2026-11-01")
	if got := f.nextAmount(t, "2026-09", a.ID); got.IsZero() {
		t.Fatalf("not depreciated")
	}
	// it cannot be cancelled any more
	_, err = f.svc.CancelAsset(f.admin, f.propID, a.ID, assets.CancelInput{Reason: "x"})
	wantCode(t, err, "ASSET_HAS_DEPRECIATION")
}

func TestChangeOfCategoryOfADepreciatedAsset(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	// a depreciated asset cannot go to a category that is not depreciated
	wantCode(t, f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevCategory, CategoryID: ptr(f.categoryID(t, "LAND"))}), "VALIDATION_FAILED")
	res := f.revise(t, a.ID, assets.RevisionInput{Kind: assets.RevCategory, CategoryID: ptr(f.categoryID(t, "IT"))})
	if res.JournalID == nil || res.CategoryCode != "IT" || *res.LifeMonths != 48 || !res.AccumDepr.Equal(dec("1000000")) || res.MonthsDone != 1 || !res.Cost.Equal(dec("96000000")) {
		t.Fatalf("%+v", res.Asset)
	}
	// the cost moves from 1530 to 1570 and the accumulated amount from 1592 to 1595
	lines := f.journalLines(t, *res.JournalID)
	if len(lines) != 4 || !lines["1570"].Equal(dec("96000000")) || !lines["1530"].Equal(dec("-96000000")) || !lines["1592"].Equal(dec("1000000")) || !lines["1595"].Equal(dec("-1000000")) {
		t.Fatalf("journal %v", lines)
	}
	if got, want := f.nextAmount(t, "2026-10", a.ID), dec("95000000").Div(dec("47")).Round(0); !got.Equal(want) {
		t.Fatalf("next month %s, want %s", got, want)
	}
	// the reopen of the month of its depreciation is refused afterwards (F9): its depreciation cannot be reversed
	_, err := f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-09-01"), assets.ReverseRunInput{Reason: "wrong"})
	wantCode(t, err, "DEPRECIATION_NOT_REVERSIBLE")
	// a category with the same accounts changes the category and posts nothing
	f.exec(t, `INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id, accumulated_account_id, expense_account_id, method, life_months, fiscal_group, fiscal_method)
		SELECT tenant_id, property_id, 'IT2', 'IT again', kind, cost_account_id, accumulated_account_id, expense_account_id, method, 24, fiscal_group, fiscal_method FROM fa_categories WHERE property_id = $1 AND code = 'IT'`, f.propID)
	res = f.revise(t, a.ID, assets.RevisionInput{Kind: assets.RevCategory, CategoryID: ptr(f.categoryID(t, "IT2"))})
	if res.JournalID != nil || res.CategoryCode != "IT2" || *res.LifeMonths != 24 {
		t.Fatalf("%+v", res.Asset)
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// 4.6 and 10.13: moves, and the department at the end of the month

func TestExample1013TheDepartmentAtTheEndOfTheMonth(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	rooms, fnb := f.dept(t, "ROOMS"), f.dept(t, "FNB")
	a := f.asset(t, "2026-09-02", "1530", "48000000", inDept(rooms)) // 500,000 a month
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	f.advanceTo(t, "2026-11-01")
	f.run(t, "2026-10")
	f.advanceTo(t, "2026-11-20")
	res := f.move(t, []int64{a.ID}, assets.MoveInput{ChangeDepartment: true, DepartmentID: &fnb, Reason: "the lobby bar opened"})
	if len(res.Moved) != 1 || res.Skipped != 0 || res.Replayed {
		t.Fatalf("%+v", res)
	}
	if x := f.asDetail(t, a.ID); x.DepartmentID == nil || *x.DepartmentID != fnb {
		t.Fatalf("%+v", x.Asset)
	}
	f.advanceTo(t, "2026-12-01")
	f.run(t, "2026-11")
	// the whole of November goes to Food and beverage, and the months before it stay with Rooms
	if got := f.runDept(t, "2026-11", a.ID); got == nil || *got != fnb {
		t.Fatalf("November: %v", got)
	}
	if got := f.runDept(t, "2026-10", a.ID); got == nil || *got != rooms {
		t.Fatalf("October: %v", got)
	}
	// a move on 2 December does not change November, and December follows the department at 31 December
	f.advanceTo(t, "2026-12-02")
	f.move(t, []int64{a.ID}, assets.MoveInput{ChangeDepartment: true, DepartmentID: &rooms, Reason: "back"})
	if got := f.runDept(t, "2026-11", a.ID); got == nil || *got != fnb {
		t.Fatalf("November after the move of December: %v", got)
	}
	f.advanceTo(t, "2027-01-01")
	f.run(t, "2026-12")
	if got := f.runDept(t, "2026-12", a.ID); got == nil || *got != rooms {
		t.Fatalf("December: %v", got)
	}
	// the history lists the two moves
	h, err := f.svc.GetHistory(f.admin, f.propID, a.ID)
	must(t, err)
	moves := 0
	for _, e := range h {
		if e.Kind == "MOVE" {
			moves++
		}
	}
	if moves != 2 {
		t.Errorf("%d moves in the history", moves)
	}
}

func TestADepartmentOfTheMoveIsReadForTheMonthsBeforeIt(t *testing.T) {
	// an asset moved after the month has its department from before the move for that month: the run of a month made late reads the month end, not today
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	rooms, fnb := f.dept(t, "ROOMS"), f.dept(t, "FNB")
	a := f.asset(t, "2026-09-02", "1530", "48000000", inDept(rooms))
	f.advanceTo(t, "2026-10-05")
	f.move(t, []int64{a.ID}, assets.MoveInput{ChangeDepartment: true, DepartmentID: &fnb})
	f.run(t, "2026-09") // run on 5 October, after the move
	if got := f.runDept(t, "2026-09", a.ID); got == nil || *got != rooms {
		t.Fatalf("September: %v", got)
	}
}

func TestMovesAreKeptAndNeverBackdated(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	rooms, fnb := f.dept(t, "ROOMS"), f.dept(t, "FNB")
	a := f.asset(t, "2026-09-02", "1530", "48000000", inDept(rooms))
	b := f.asset(t, "2026-09-03", "1530", "12000000", inDept(rooms))
	lobby := ensureLocation(t, f, "LOBBY")
	bar := ensureLocation(t, f, "BAR")

	// a request that asks for nothing, or for what is there already, is refused
	_, err := f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, ChangeDepartment: true, DepartmentID: &rooms, Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, ChangeDepartment: true, DepartmentID: &fnb, IdempotencyKey: nextKey()})
	wantCode(t, err, "VALIDATION_FAILED") // a reason
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, ChangeDepartment: true, DepartmentID: &fnb, Reason: "x"})
	wantCode(t, err, "IDEMPOTENCY_KEY_REQUIRED")
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID, 999999}, ChangeDepartment: true, DepartmentID: &fnb, Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "ASSET_NOT_FOUND")
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, ChangeLocation: true, LocationID: ptr(int64(999999)), Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "VALIDATION_FAILED")

	// two assets, one of them where it is asked to be already: all or nothing for the others, the one there is counted
	key := nextKey()
	ctx, trace := db.WithLockTrace(f.admin)
	r, err := f.svc.MoveAssets(ctx, f.propID, assets.MoveInput{AssetIDs: []int64{b.ID, a.ID}, ChangeLocation: true, LocationID: &lobby, ChangeDepartment: true, DepartmentID: &rooms, Reason: "refit", IdempotencyKey: key})
	must(t, err)
	assertLockOrder(t, trace)
	if len(r.Moved) != 2 || r.Skipped != 0 {
		t.Fatalf("%+v", r)
	}
	r, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID, b.ID}, ChangeLocation: true, LocationID: &lobby, Reason: "again", IdempotencyKey: nextKey()})
	wantCode(t, err, "VALIDATION_FAILED") // both are there already
	a2 := f.asset(t, "2026-09-04", "1530", "12000000", inDept(rooms))
	r, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID, a2.ID}, ChangeLocation: true, LocationID: &lobby, Reason: "one is there", IdempotencyKey: nextKey()})
	must(t, err)
	if len(r.Moved) != 1 || r.Skipped != 1 || r.Moved[0].ID != a2.ID {
		t.Fatalf("%+v", r)
	}
	// the key replays
	r, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{b.ID, a.ID}, ChangeLocation: true, LocationID: &lobby, ChangeDepartment: true, DepartmentID: &rooms, Reason: "refit", IdempotencyKey: key})
	must(t, err)
	if !r.Replayed || len(r.Moved) != 2 {
		t.Fatalf("%+v", r)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_moves WHERE property_id = $1`, f.propID); n != 3 {
		t.Errorf("%d moves", n)
	}
	// the single route answers the asset; the form of an asset (PATCH) records a move too
	x, err := f.svc.MoveAsset(f.admin, f.propID, a.ID, assets.MoveInput{ChangeLocation: true, LocationID: &bar, Reason: "to the bar", IdempotencyKey: nextKey()})
	must(t, err)
	if x.LocationID == nil || *x.LocationID != bar {
		t.Fatalf("%+v", x.Asset)
	}
	cur := f.asDetail(t, b.ID)
	_, err = f.svc.UpdateAsset(f.admin, f.propID, b.ID, assets.AssetPatch{Name: cur.Name, LocationID: &bar, DepartmentID: &fnb, Version: cur.Version})
	must(t, err)
	if n := f.countRows(t, `SELECT count(*) FROM fa_moves WHERE property_id = $1 AND asset_id = $2 AND reason = 'Changed on the asset form'`, f.propID, b.ID); n != 1 {
		t.Errorf("%d moves from the form", n)
	}
	// a move is dated the open business day, never backdated: the database refuses one before the last
	_, err = f.Pool.Exec(context.Background(), `INSERT INTO fa_moves (tenant_id, property_id, asset_id, effective_on, from_location_id, to_location_id, reason)
		VALUES ($1, $2, $3, DATE '2026-01-01', $4, $5, 'old')`, f.tenantID, f.propID, a.ID, bar, lobby)
	if err == nil {
		t.Fatal("a backdated move was written")
	}
	// and a move cannot be changed or deleted
	if _, err = f.Pool.Exec(context.Background(), `DELETE FROM fa_moves`); err == nil {
		t.Error("moves were deleted")
	}
	// a disposed or cancelled asset is not moved
	c := f.asset(t, "2026-09-05", "1530", "12000000", nil)
	_, err = f.svc.CancelAsset(f.admin, f.propID, c.ID, assets.CancelInput{Reason: "x"})
	must(t, err)
	_, err = f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{c.ID}, ChangeDepartment: true, DepartmentID: &fnb, Reason: "x", IdempotencyKey: nextKey()})
	wantCode(t, err, "ASSET_STATUS_INVALID")
}

func ensureLocation(t *testing.T, f *fx, code string) int64 {
	t.Helper()
	loc, err := f.svc.CreateLocation(f.admin, f.propID, assets.LocationInput{Code: code, Name: code})
	must(t, err)
	return loc.ID
}

func TestMoveAndRunRace(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	rooms, fnb := f.dept(t, "ROOMS"), f.dept(t, "FNB")
	a := f.asset(t, "2026-09-02", "1530", "48000000", inDept(rooms))
	f.advanceTo(t, "2026-10-01")
	// the run of September and a move made on 1 October at the same moment: the move is after the month, so the run gives September to Rooms whichever goes first
	errs := f.race(2, func(i int) error {
		if i == 0 {
			_, err := f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
			return err
		}
		_, err := f.svc.MoveAssets(f.admin, f.propID, assets.MoveInput{AssetIDs: []int64{a.ID}, ChangeDepartment: true, DepartmentID: &fnb, Reason: "race", IdempotencyKey: nextKey()})
		return err
	})
	for _, err := range errs {
		must(t, err)
	}
	if got := f.runDept(t, "2026-09", a.ID); got == nil || *got != rooms {
		t.Fatalf("September: %v", got)
	}
	if x := f.asDetail(t, a.ID); x.DepartmentID == nil || *x.DepartmentID != fnb || x.AccumDepr.IsZero() {
		t.Fatalf("%+v", x.Asset)
	}
}

func TestTwoRevisionsOfTheSameAssetRace(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	// the same revision twice with different keys: both are applied one after the other, the second finds the life it asks for already (UNCHANGED)
	errs := f.race(2, func(i int) error {
		return f.tryRevise(a.ID, assets.RevisionInput{Kind: assets.RevLife, LifeMonths: ptr(int32(120))})
	})
	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		} else {
			wantCode(t, err, "VALIDATION_FAILED")
		}
	}
	if ok != 1 {
		t.Fatalf("%d revisions succeeded: %v", ok, errs)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_revisions WHERE property_id = $1 AND asset_id = $2`, f.propID, a.ID); n != 1 {
		t.Errorf("%d revisions", n)
	}
}
