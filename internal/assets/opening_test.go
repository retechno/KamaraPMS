package assets_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"kamarapms/internal/assets"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

func (f *fx) importRows(t *testing.T, asOf string, post bool, dry bool, rows ...assets.OpeningRow) (assets.OpeningResult, error) {
	t.Helper()
	jd := d(asOf)
	in := assets.OpeningInput{AsOf: jd, JournalDate: &jd, PostJournal: &post, DryRun: dry, Rows: rows}
	if !dry {
		in.IdempotencyKey = nextKey()
	}
	return f.svc.ImportOpening(f.admin, f.propID, in)
}

func TestExample105ImportWithJournal(t *testing.T) {
	f := newFx(t)
	res, err := f.importRows(t, "2026-09-30", true, false,
		row("Lobby furniture", "FFE", "50000000", "30000000", 0),
		row("Hotel van", "VEHICLE", "200000000", "80000000", 0))
	must(t, err)
	if res.JournalID == nil || res.ImportID == nil || res.Number == nil {
		t.Fatalf("journal, import or number missing: %+v", res)
	}
	if !res.TotalCost.Equal(dec("250000000")) || !res.TotalAccum.Equal(dec("110000000")) || !res.NetToEquity.Equal(dec("140000000")) {
		t.Fatalf("totals %s %s %s", res.TotalCost, res.TotalAccum, res.NetToEquity)
	}
	lines := f.journalLines(t, *res.JournalID)
	want := map[string]string{"1530": "50000000", "1550": "200000000", "1592": "-30000000", "1594": "-80000000", "3900": "-140000000"}
	if len(lines) != len(want) {
		t.Fatalf("lines %v", lines)
	}
	for c, v := range want {
		if !lines[c].Equal(dec(v)) {
			t.Errorf("account %s: %s, want %s", c, lines[c], v)
		}
	}
	if res.RegisterFrom == nil || res.RegisterFrom.String() != "2026-10-01" {
		t.Errorf("register_from %v", res.RegisterFrom)
	}
	st, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	for _, r := range st.Reconciliation {
		if !r.CostDifference.IsZero() || !r.AccumDifference.IsZero() {
			t.Errorf("the register and the ledger differ: %+v", r)
		}
	}
}

func TestExample1015FullHistoryAndFiscal(t *testing.T) {
	f := newFx(t)
	r := row("Old chairs", "FFE", "36000000", "", 0)
	r.InServiceDate = ptr(d("2020-01-15"))
	r.MonthsDone = ptr(81)
	r.AccumulatedDepreciation = ptr(dec("30375000"))
	r.FiscalGroup = "G1"
	res, err := f.importRows(t, "2026-09-30", true, false, r)
	must(t, err)
	lines := f.journalLines(t, *res.JournalID)
	if !lines["1530"].Equal(dec("36000000")) || !lines["1592"].Equal(dec("-30375000")) || !lines["3900"].Equal(dec("-5625000")) {
		t.Fatalf("journal %v", lines)
	}
	if res.Rows[0].FiscalAccum == nil || !res.Rows[0].FiscalAccum.Equal(dec("36000000")) {
		t.Fatalf("fiscal accumulated %v", res.Rows[0].FiscalAccum)
	}
	var estimated bool
	for _, w := range res.Rows[0].Warnings {
		estimated = estimated || w.Code == "FISCAL_ACCUMULATED_ESTIMATED"
	}
	if !estimated {
		t.Errorf("the estimated fiscal accumulation is not warned: %+v", res.Rows[0].Warnings)
	}
}

func TestDryRunChangesNothing(t *testing.T) {
	f := newFx(t)
	res, err := f.importRows(t, "2026-09-30", true, true, row("Sofa", "FFE", "10000000", "2000000", 0))
	must(t, err)
	if !res.DryRun || !res.Valid || res.Number != nil {
		t.Fatalf("%+v", res)
	}
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM fa_assets WHERE property_id=$1) + (SELECT count(*) FROM fa_opening_imports WHERE property_id=$1) + (SELECT count(*) FROM gl_journals WHERE property_id=$1 AND journal_type='ASSET')`, f.propID).Scan(&n))
	if n != 0 {
		t.Fatalf("a dry run left %d rows", n)
	}
}

func TestRegisterOnlyNeedsTheLedgerToAgree(t *testing.T) {
	f := newFx(t)
	_, err := f.importRows(t, "2026-09-30", false, false, row("Sofa", "FFE", "10000000", "2000000", 0))
	wantCode(t, err, "OPENING_GL_MISMATCH")
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM fa_assets WHERE property_id=$1`, f.propID).Scan(&n))
	if n != 0 {
		t.Fatalf("a refused import left %d assets", n)
	}
}

func TestFullyDepreciatedIsAcceptedAndLifeOverWithValueIsNot(t *testing.T) {
	f := newFx(t)
	r := row("Old TV", "IT", "4800000", "4800000", 48)
	r.InServiceDate = ptr(d("2020-01-15"))
	res, err := f.importRows(t, "2026-09-30", true, true, r)
	must(t, err)
	if !res.Rows[0].FullyDepreciated {
		t.Errorf("not recognised as fully depreciated: %+v", res.Rows[0])
	}
	bad := row("Old PC", "IT", "4800000", "1000000", 0)
	bad.InServiceDate = ptr(d("2019-01-15"))
	bad.MonthsDone = ptr(48)
	res, err = f.importRows(t, "2026-09-30", true, true, bad)
	must(t, err)
	if res.Valid || len(res.Rows[0].Errors) == 0 {
		t.Errorf("life over with a value left was accepted: %+v", res.Rows[0])
	}
	_, err = f.importRows(t, "2026-09-30", true, false, bad)
	wantCode(t, err, "OPENING_ROW_INVALID")
}

func TestEveryRowErrorIsReported(t *testing.T) {
	f := newFx(t)
	res, err := f.importRows(t, "2026-09-30", true, true, row("", "NOPE", "-1", "", 0))
	must(t, err)
	if res.Valid || len(res.Rows[0].Errors) < 2 {
		t.Fatalf("the errors of the row are not all reported: %+v", res.Rows[0].Errors)
	}
	_, err = f.importRows(t, "2026-09-30", true, false, row("", "NOPE", "-1", "", 0))
	wantCode(t, err, "OPENING_ROW_INVALID")
}

func TestImportIsIdempotent(t *testing.T) {
	f := newFx(t)
	jd := d("2026-09-30")
	in := assets.OpeningInput{AsOf: jd, JournalDate: &jd, Rows: []assets.OpeningRow{row("Sofa", "FFE", "10000000", "2000000", 0)}, IdempotencyKey: "same-key"}
	a, err := f.svc.ImportOpening(f.admin, f.propID, in)
	must(t, err)
	b, err := f.svc.ImportOpening(f.admin, f.propID, in)
	must(t, err)
	if !b.Replayed || *a.ImportID != *b.ImportID {
		t.Fatalf("the replay is a new import: %+v", b)
	}
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM fa_assets WHERE property_id=$1`, f.propID).Scan(&n))
	if n != 1 {
		t.Fatalf("%d assets", n)
	}
}

func TestSecondImportMustHaveTheSameAsOf(t *testing.T) {
	f := newFx(t)
	_, err := f.importRows(t, "2026-09-30", true, false, row("A", "FFE", "1000000", "0", 0))
	must(t, err)
	_, err = f.importRows(t, "2026-08-31", true, false, row("B", "FFE", "1000000", "0", 0))
	if err == nil {
		t.Fatal("an import with another month end was accepted")
	}
}

func TestTwoImportsAtOnceBothLand(t *testing.T) {
	f := newFx(t)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.importRows(t, "2026-09-30", true, false, row("Chair", "FFE", "1000000", "0", 0))
		}()
	}
	wg.Wait()
	for _, e := range errs {
		must(t, e)
	}
	var n int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(DISTINCT number) FROM fa_assets WHERE property_id=$1`, f.propID).Scan(&n))
	if n != 2 {
		t.Fatalf("%d distinct numbers, want 2", n)
	}
}

func TestImportNeedsAssetSetup(t *testing.T) {
	f := newFx(t)
	u := f.user(t, auth.PermAssetRead, auth.PermAssetManage)
	_, err := f.svc.ImportOpening(u, f.propID, assets.OpeningInput{DryRun: true, AsOf: d("2026-09-30")})
	wantCode(t, err, "PERMISSION_DENIED")
}

func TestCSVImport(t *testing.T) {
	f := newFx(t)
	csv := "\xef\xbb\xbfname;category;in_service_date;cost;accumulated_depreciation;months_done\nSofa;FFE;2024-01-15;10000000;2000000;12\n"
	jd := d("2026-09-30")
	res, err := f.svc.ImportOpening(f.admin, f.propID, assets.OpeningInput{AsOf: jd, JournalDate: &jd, CSV: csv, DryRun: true})
	must(t, err)
	if len(res.Rows) != 1 || !strings.EqualFold(res.Rows[0].CategoryCode, "FFE") {
		t.Fatalf("%+v", res.Rows)
	}
}

func TestEveryImportUseCaseTakesItsLocksInOrder(t *testing.T) {
	f := newFx(t)
	ctx, trace := db.WithLockTrace(f.admin)
	jd := d("2026-09-30")
	_, err := f.svc.ImportOpening(ctx, f.propID, assets.OpeningInput{AsOf: jd, JournalDate: &jd, IdempotencyKey: "trace", Rows: []assets.OpeningRow{row("Sofa", "FFE", "10000000", "2000000", 0)}})
	must(t, err)
	level, rank := db.LevelNone, 0
	ranks := map[string]int{"fa_settings": 2, "fa_assets": 3, "fa_audits": 4}
	var sawSettings bool
	for _, e := range trace.Events() {
		if e.Begin {
			level, rank = db.LevelNone, 0
			continue
		}
		if e.Level < level {
			t.Errorf("the level fell from %d to %d (%s)", level, e.Level, e.Table)
		}
		if e.Level > level {
			rank = 0
		}
		level = e.Level
		if r, ok := ranks[e.Table]; ok {
			if r < rank {
				t.Errorf("%s taken after a later table of the level", e.Table)
			}
			rank = r
		}
		sawSettings = sawSettings || e.Table == "fa_settings"
	}
	if !sawSettings {
		t.Error("the import did not lock fa_settings")
	}
}

// A finding carries the figures it is made of, so that the screen words it in its own language.
func TestFindingsCarryTheirParams(t *testing.T) {
	f := newFx(t)
	long := row(strings.Repeat("x", 151), "FFE", "1000000", "", 0)
	long.SerialNo = strings.Repeat("s", 101)
	res, err := f.importRows(t, "2026-09-30", true, true, long, row("Sofa", "FFE", "1000000", "", 0))
	must(t, err)
	got := map[string]string{}
	for _, e := range res.Rows[0].Errors {
		got[e.Field+"."+e.Code] = e.Params["max"]
	}
	if got["name.INVALID_VALUE"] == "" || got["serial_no.INVALID_VALUE"] == "" {
		t.Fatalf("the findings of the long name and serial number have no max: %v", got)
	}
}

// A room made by the import is named by its number, not by an English word.
func TestEnsuredRoomLocationHasNoEnglishWord(t *testing.T) {
	f := newFx(t)
	rt := f.RoomType(t, f.admin, f.propID, "STD")
	room := f.Room(t, f.admin, f.propID, rt.ID, "101")
	loc, err := f.svc.EnsureLocation(f.admin, f.propID, assets.EnsureLocationInput{Kind: "ROOM", RefID: room.ID})
	must(t, err)
	if loc.Name != "101" || loc.Code != "ROOM-101" {
		t.Fatalf("location %q (%s)", loc.Name, loc.Code)
	}
}
