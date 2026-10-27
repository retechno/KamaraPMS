package assets_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// ---------------------------------------------------------------------------------------------------------------------
// Helpers of the depreciation tests

func (f *fx) exec(t *testing.T, sql string, args ...any) {
	t.Helper()
	if _, err := f.Pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatal(err)
	}
}

// books moves the start of the books (the manual helper of the acquisition tests moves it to 2026-08-01 by itself).
func (f *fx) books(t *testing.T, start string) {
	t.Helper()
	f.exec(t, `UPDATE accounting_settings SET start_date = $2 WHERE property_id = $1`, f.propID, start)
}

// post posts a manual journal without touching the start of the books.
func (f *fx) post(t *testing.T, date, debit, credit, amount string) accounting.Journal {
	t.Helper()
	f.exec(t, `UPDATE accounting_settings SET start_date = LEAST(start_date, $2::date) WHERE property_id = $1`, f.propID, date)
	j, err := f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d(date), Description: "Entry " + debit, Lines: []accounting.LineInput{
		{AccountID: f.accounts[debit], Debit: dec(amount), Description: "Debit " + debit}, {AccountID: f.accounts[credit], Credit: dec(amount), Description: "Credit " + credit}}}, nextKey())
	must(t, err)
	return j
}

// dept makes a department of the property.
func (f *fx) dept(t *testing.T, code string) int64 {
	t.Helper()
	var id int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO departments (tenant_id, property_id, code, name) VALUES ($1, $2, $3, $4)
		ON CONFLICT (property_id, code) DO UPDATE SET name = departments.name RETURNING id`, f.tenantID, f.propID, code, "Department "+code).Scan(&id))
	return id
}

// asset posts a journal that debits a cost account and makes an asset of its line.
func (f *fx) asset(t *testing.T, date, account, amount string, mut func(*assets.MakeAssetInput)) assets.MadeAsset {
	t.Helper()
	j := f.post(t, date, account, "3900", amount)
	line := f.lineOf(t, j.Number, account)
	return f.makeAsset(t, line.LineID, 1, mut).Assets[0]
}

func inDept(id int64) func(*assets.MakeAssetInput) {
	return func(in *assets.MakeAssetInput) { in.DepartmentID = &id }
}

// advanceTo moves the open business day to a date by closing the days in between. No night audit runs, so nothing is posted for them: it is for the months a test runs, not for closing them.
func (f *fx) advanceTo(t *testing.T, to string) {
	t.Helper()
	for {
		var cur string
		must(t, f.Pool.QueryRow(context.Background(), `SELECT to_char(business_date, 'YYYY-MM-DD') FROM business_days WHERE property_id = $1 AND status = 'OPEN'`, f.propID).Scan(&cur))
		if cur == to {
			return
		}
		if cur > to {
			t.Fatalf("the business day is %s, after %s", cur, to)
		}
		f.exec(t, `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, f.propID)
		f.exec(t, `INSERT INTO business_days (tenant_id, property_id, business_date) SELECT $1, $2, (to_date($3, 'YYYY-MM-DD') - 0) WHERE false`, f.tenantID, f.propID, cur)
		next := d(cur).AddDays(1).String()
		f.exec(t, `INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, $3)`, f.tenantID, f.propID, next)
	}
}

// closeDay runs the night audit of the open day, so the day is closed and has its journal (what a month needs to be closable).
func (f *fx) closeDay(t *testing.T) {
	t.Helper()
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	f.Clock.Set(time.Date(day.BusinessDate.Year(), day.BusinessDate.Month(), day.BusinessDate.Day(), 17, 30, 0, 0, time.UTC).AddDate(0, 0, 1)) // 00:30 local of the next day
	_, err = f.Audit.Run(f.admin, f.propID, day.BusinessDate)
	must(t, err)
}

func (f *fx) preview(t *testing.T, month string) assets.DepreciationPreview {
	t.Helper()
	p, err := f.svc.PreviewDepreciation(f.admin, f.propID, d(month+"-01"))
	must(t, err)
	return p
}

func (f *fx) run(t *testing.T, month string) assets.DepreciationRunResult {
	t.Helper()
	r, err := f.svc.RunDepreciation(f.admin, f.propID, d(month+"-01"), nextKey())
	must(t, err)
	return r
}

func hasBlocker(p assets.DepreciationPreview, code string) bool {
	for _, b := range p.Blockers {
		if b.Code == code {
			return true
		}
	}
	return false
}

func (f *fx) asDetail(t *testing.T, id int64) assets.AssetDetail {
	t.Helper()
	a, err := f.svc.GetAsset(f.admin, f.propID, id)
	must(t, err)
	return a
}

func isoMonth(m interface{ String() string }) string { return m.String()[:7] }

// assertLockOrder fails when a use case took a lock of a lower level, or of a lower rank inside the assets level, after a higher one.
func assertLockOrder(t *testing.T, trace *db.LockTrace) {
	t.Helper()
	level, rank := db.LevelNone, 0
	ranks := map[string]int{"fa_settings": 2, "fa_assets": 3, "fa_audits": 4}
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
	}
}

func tables(trace *db.LockTrace) map[string]bool {
	out := map[string]bool{}
	for _, e := range trace.Events() {
		out[e.Table] = true
	}
	return out
}

// ---------------------------------------------------------------------------------------------------------------------
// 10.2: a monthly run, split by category and department

func TestExample102AMonthlyRunSplitByCategoryAndDepartment(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	f.exec(t, `UPDATE fa_settings SET oe_policy = 'BATCH' WHERE property_id = $1`, f.propID)
	f.exec(t, `UPDATE fa_categories SET residual_pct = 10 WHERE property_id = $1 AND code = 'VEHICLE'`, f.propID)
	rooms, fnb, ops, admin, it := f.dept(t, "ROOMS"), f.dept(t, "FNB"), f.dept(t, "POMEC"), f.dept(t, "AG"), f.dept(t, "ICT")
	f.asset(t, "2026-09-02", "1530", "96000000", inDept(rooms))    // FF&E, 96 months: 1,000,000
	f.asset(t, "2026-09-03", "1530", "48000000", inDept(fnb))      // FF&E: 500,000
	f.asset(t, "2026-09-04", "1520", "2400000000", inDept(ops))    // building, 240 months: 10,000,000
	f.asset(t, "2026-09-05", "1550", "240000000", inDept(admin))   // vehicle, residual 10%: (240,000,000 - 24,000,000) / 96 = 2,250,000
	f.asset(t, "2026-09-06", "1570", "12000000", inDept(it))       // computers, 48 months: 250,000
	oe := f.asset(t, "2026-09-07", "1540", "6000000", inDept(fnb)) // operating equipment, 24 months: 250,000
	if a := f.asDetail(t, oe.ID); a.LifeMonths == nil || *a.LifeMonths != 24 || a.FirstDepMonth == nil || a.FirstDepMonth.String() != "2026-09-01" {
		t.Fatalf("%+v", a.Asset)
	}

	// September has not ended on the 30th: the preview says so and the run refuses
	pv := f.preview(t, "2026-09")
	if pv.CanRun || !hasBlocker(pv, assets.BlockMonthInvalid) || pv.AssetCount != 6 {
		t.Fatalf("%+v", pv.Blockers)
	}
	_, err := f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
	wantCode(t, err, "DEPRECIATION_MONTH_INVALID")
	f.advanceTo(t, "2026-10-01")

	pv = f.preview(t, "2026-09")
	if !pv.CanRun || len(pv.Blockers) != 0 || pv.AssetCount != 6 || len(pv.Lines) != 6 || len(pv.Groups) != 6 || !pv.Total.Equal(dec("14250000")) || pv.JournalDate.String() != "2026-09-30" {
		t.Fatalf("preview %+v", pv)
	}
	if pv.NextMonth == nil || pv.NextMonth.String() != "2026-09-01" {
		t.Fatalf("next month %v", pv.NextMonth)
	}
	journals := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID)
	if f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1`, f.propID) != 0 {
		t.Fatal("the preview wrote a run")
	}

	ctx, trace := db.WithLockTrace(f.admin)
	key := nextKey()
	res, err := f.svc.RunDepreciation(ctx, f.propID, d("2026-09-01"), key)
	must(t, err)
	assertLockOrder(t, trace)
	if tb := tables(trace); !tb["fa_settings"] || !tb["fa_assets"] || !tb["accounting_settings"] {
		t.Errorf("the run locked %v", tb)
	}
	if res.Replayed || res.Run.Status != assets.MonthPosted || !res.Run.Total.Equal(dec("14250000")) || res.Run.AssetCount != 6 || len(res.Lines) != 6 {
		t.Fatalf("%+v", res.Run)
	}
	if got := f.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, f.propID); got != journals+1 {
		t.Fatalf("the run posted %d journals", got-journals)
	}

	// the journal of 10.2: dated the last day of the month, type ASSET, eleven lines, balanced
	var typ, date string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT journal_type, journal_date::text FROM gl_journals WHERE id = $1`, res.Run.JournalID).Scan(&typ, &date))
	if typ != "ASSET" || date != "2026-09-30" {
		t.Errorf("journal %s %s", typ, date)
	}
	if n := f.countRows(t, `SELECT count(*) FROM gl_journal_lines WHERE journal_id = $1`, res.Run.JournalID); n != 11 {
		t.Errorf("%d journal lines", n)
	}
	want := map[string]string{"7520": "1500000", "7510": "10000000", "7540": "2250000", "7550": "250000", "7530": "250000",
		"1592": "-1500000", "1591": "-10000000", "1594": "-2250000", "1595": "-250000", "1593": "-250000"}
	got := f.journalLines(t, res.Run.JournalID)
	for code, w := range want {
		if !got[code].Equal(dec(w)) {
			t.Errorf("account %s: %s, want %s", code, got[code], w)
		}
	}
	// the department of each expense line
	rows, err := f.Pool.Query(context.Background(), `SELECT d.code, l.debit FROM gl_journal_lines l JOIN gl_accounts a ON a.id = l.account_id AND a.property_id = l.property_id
		JOIN departments d ON d.id = l.department_id WHERE l.journal_id = $1 AND a.code = '7520' ORDER BY d.code`, res.Run.JournalID)
	must(t, err)
	byDept := map[string]string{}
	for rows.Next() {
		var c string
		var v decimal.Decimal
		must(t, rows.Scan(&c, &v))
		byDept[c] = v.String()
	}
	rows.Close()
	if byDept["ROOMS"] != "1000000" || byDept["FNB"] != "500000" || len(byDept) != 2 {
		t.Errorf("the departments of 7520: %v", byDept)
	}

	// the assets: the state is the sum of the ledger, one DEPRECIATE row each, and nothing drifted
	for _, l := range res.Lines {
		a := f.asDetail(t, l.AssetID)
		if !a.AccumDepr.Equal(l.Amount) || a.MonthsDone != 1 || a.LastDepMonth == nil || a.LastDepMonth.String() != "2026-09-01" || !a.CarryingValue.Equal(a.Cost.Sub(l.Amount)) {
			t.Errorf("asset %s: %+v", a.Number, a.Asset)
		}
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_ledger WHERE property_id = $1 AND kind = 'DEPRECIATE' AND journal_id = $2 AND book_month = '2026-09-01'`, f.propID, res.Run.JournalID); n != 6 {
		t.Errorf("%d DEPRECIATE rows", n)
	}
	chk, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	sawAccum := false
	for _, r := range chk.Reconciliation {
		if !r.CostDifference.IsZero() || !r.AccumDifference.IsZero() {
			t.Errorf("the register and the ledger differ for %s: %+v", r.CategoryCode, r)
		}
		sawAccum = sawAccum || r.RegisterAccum.IsPositive()
	}
	for _, i := range chk.Issues {
		if i.Code == "LEDGER_DRIFT" || i.Code == "ACCUMULATED_DIFFERS" {
			t.Errorf("setup check: %+v", i)
		}
	}
	if !sawAccum {
		t.Error("the reconciliation shows no accumulated depreciation")
	}
	if n := f.countRows(t, `SELECT count(*) FROM audit_logs WHERE property_id = $1 AND action = 'asset.depreciation_run'`, f.propID); n != 1 {
		t.Errorf("%d audit rows", n)
	}

	// the same key answers with the run again; another key says the month was run
	again, err := f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), key)
	must(t, err)
	if !again.Replayed || again.Run.ID != res.Run.ID {
		t.Fatalf("%+v", again.Run)
	}
	_, err = f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
	wantCode(t, err, "DEPRECIATION_ALREADY_RUN")
	if f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1`, f.propID) != 1 {
		t.Fatal("a second run was written")
	}
	_, err = f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), "")
	wantCode(t, err, "IDEMPOTENCY_KEY_REQUIRED")
	// October is next, and has not ended
	_, err = f.svc.RunDepreciation(f.admin, f.propID, d("2026-10-01"), nextKey())
	wantCode(t, err, "DEPRECIATION_MONTH_INVALID")
	// who may: asset.depreciate
	reader := f.user(t, auth.PermAssetRead)
	_, err = f.svc.PreviewDepreciation(reader, f.propID, d("2026-10-01"))
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = f.svc.RunDepreciation(reader, f.propID, d("2026-10-01"), nextKey())
	wantCode(t, err, "PERMISSION_DENIED")
}

// 10.4 through the service: declining balance, then the next month.
func TestExample104DecliningBalanceThroughTheRuns(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "24000000", nil)
	f.exec(t, `UPDATE fa_assets SET method = 'DB', db_factor = 2, life_months = 48 WHERE id = $1`, a.ID)
	f.advanceTo(t, "2026-10-01")
	r1 := f.run(t, "2026-09")
	f.advanceTo(t, "2026-11-01")
	r2 := f.run(t, "2026-10")
	if !r1.Run.Total.Equal(dec("1000000")) || !r2.Run.Total.Equal(dec("958333")) {
		t.Fatalf("%s then %s", r1.Run.Total, r2.Run.Total)
	}
	if d := f.asDetail(t, a.ID); !d.AccumDepr.Equal(dec("1958333")) || d.MonthsDone != 2 {
		t.Fatalf("%+v", d.Asset)
	}
}

// 10.9: an asset registered late is caught up in the run of the month it is made.
func TestExample109ALateAssetIsCaughtUp(t *testing.T) {
	f := newFx(t)
	f.books(t, "2026-01-01")
	f.startRegister(t, "2026-01-01", "0")
	f.asset(t, "2026-01-10", "1530", "96000000", nil) // 1,000,000 a month from January
	for _, m := range []string{"2026-01", "2026-02", "2026-03"} {
		if r := f.run(t, m); !r.Run.Total.Equal(dec("1000000")) {
			t.Fatalf("%s: %s", m, r.Run.Total)
		}
	}
	// a bill of 5 April for a chair put in service on 15 January: 36 months, 300,000 a month, due since January
	late := f.asset(t, "2026-04-05", "1530", "10800000", func(in *assets.MakeAssetInput) { in.InServiceDate = ptr(d("2026-01-15")) })
	f.exec(t, `UPDATE fa_assets SET life_months = 36 WHERE id = $1`, late.ID)
	pv := f.preview(t, "2026-04")
	if pv.NextMonth == nil || pv.NextMonth.String() != "2026-04-01" || pv.CatchUpCount != 1 || !pv.Total.Equal(dec("2200000")) {
		t.Fatalf("%+v", pv)
	}
	res := f.run(t, "2026-04")
	var line assets.DepreciationLine
	for _, l := range res.Lines {
		if l.AssetID == late.ID {
			line = l
		}
	}
	if line.Months != 4 || !line.CatchUp || !line.Amount.Equal(dec("1200000")) || line.FromMonth.String() != "2026-01-01" || line.MonthsDoneAfter != 4 {
		t.Fatalf("catch-up line %+v", line)
	}
	if a := f.asDetail(t, late.ID); !a.AccumDepr.Equal(dec("1200000")) || a.MonthsDone != 4 || a.LastDepMonth.String() != "2026-04-01" {
		t.Fatalf("%+v", a.Asset)
	}
	// the schedule says January to April were booked in the run of April
	sch, err := f.svc.AssetSchedule(f.admin, f.propID, late.ID)
	must(t, err)
	posted, projected := 0, 0
	for _, r := range sch.Rows {
		switch r.Kind {
		case assets.SchedulePosted:
			posted++
			if r.RunMonth == nil || r.RunMonth.String() != "2026-04-01" || !r.Amount.Equal(dec("300000")) || r.JournalNumber == nil {
				t.Errorf("posted row %+v", r)
			}
		case assets.ScheduleProjected:
			projected++
		}
	}
	if posted != 4 || projected != 32 || sch.Rows[0].Month.String() != "2026-01-01" || sch.Rows[len(sch.Rows)-1].Month.String() != "2028-12-01" {
		t.Fatalf("%d posted, %d projected, from %v to %v", posted, projected, sch.Rows[0].Month, sch.Rows[len(sch.Rows)-1].Month)
	}
	if last := sch.Rows[len(sch.Rows)-1]; !last.CarryingAfter.IsZero() || last.MonthsDone != 36 {
		t.Fatalf("the last row %+v", last)
	}
}

// A month in which nothing is due is not a run: the next month is the first one with work (an asset made after a gap).
func TestTheNextMonthIsTheFirstWithWork(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-05-01", "0")
	f.books(t, "2026-05-01")
	if o, err := f.svc.ListDepreciation(f.admin, f.propID); err != nil || o.NextMonth != nil || len(o.Months) == 0 {
		t.Fatalf("%+v %v", o, err)
	}
	f.asset(t, "2026-07-10", "1530", "96000000", nil)
	o, err := f.svc.ListDepreciation(f.admin, f.propID)
	must(t, err)
	if o.NextMonth == nil || o.NextMonth.String() != "2026-07-01" {
		t.Fatalf("next month %v", o.NextMonth)
	}
	_, err = f.svc.RunDepreciation(f.admin, f.propID, d("2026-06-01"), nextKey())
	wantCode(t, err, "DEPRECIATION_MONTH_INVALID")
	f.run(t, "2026-07")
	o, err = f.svc.ListDepreciation(f.admin, f.propID)
	must(t, err)
	byMonth := map[string]assets.DepreciationMonth{}
	for _, m := range o.Months {
		byMonth[isoMonth(m.Month)] = m
	}
	if byMonth["2026-07"].Status != assets.MonthPosted || byMonth["2026-07"].JournalNumber == nil || !byMonth["2026-07"].Reversible || byMonth["2026-06"].Status != assets.MonthNotRun || byMonth["2026-06"].IsNext {
		t.Fatalf("%+v", byMonth)
	}
	if o.NextMonth == nil || o.NextMonth.String() != "2026-08-01" || !byMonth["2026-08"].IsNext || !byMonth["2026-08"].Runnable {
		t.Fatalf("next %v %+v", o.NextMonth, byMonth["2026-08"])
	}
	// September has not ended
	if byMonth["2026-09"].Runnable {
		t.Error("a month that has not ended is runnable")
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// The blockers of the preview, and the reversal of a run

func TestThePreviewNamesWhatBlocksTheRun(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.exec(t, `UPDATE gl_accounts SET department_requirement = 'REQUIRED', default_department_id = NULL WHERE property_id = $1 AND code = '7520'`, f.propID)
	pv := f.preview(t, "2026-09")
	var b assets.DepreciationBlocker
	for _, x := range pv.Blockers {
		if x.Code == assets.BlockDepartment {
			b = x
		}
	}
	if pv.CanRun || b.Params["asset"] != a.Number || b.Params["account"] != "7520" {
		t.Fatalf("%+v", pv.Blockers)
	}
	_, err := f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
	wantCode(t, err, "DEPRECIATION_BLOCKED")
	if f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1`, f.propID) != 0 {
		t.Fatal("a refused run wrote a row")
	}
	// the department of the asset solves it
	f.exec(t, `UPDATE fa_assets SET department_id = $2 WHERE id = $1`, a.ID, f.dept(t, "ROOMS"))
	if pv := f.preview(t, "2026-09"); !pv.CanRun || len(pv.Groups) != 1 || pv.Groups[0].DepartmentCode == nil || *pv.Groups[0].DepartmentCode != "ROOMS" {
		t.Fatalf("%+v", pv)
	}
	// an account that is switched off
	f.exec(t, `UPDATE gl_accounts SET is_active = false WHERE property_id = $1 AND code = '1592'`, f.propID)
	pv = f.preview(t, "2026-09")
	if pv.CanRun || !hasBlocker(pv, "ACCOUNT_INACTIVE") {
		t.Fatalf("%+v", pv.Blockers)
	}
	_, err = f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
	wantCode(t, err, "DEPRECIATION_BLOCKED")
	f.exec(t, `UPDATE gl_accounts SET is_active = true WHERE property_id = $1 AND code = '1592'`, f.propID)
	// the register that has not started
	g := newFx(t)
	if pv, err := g.svc.PreviewDepreciation(g.admin, g.propID, d("2026-09-01")); err != nil || pv.CanRun || !hasBlocker(pv, assets.BlockRegisterNotStarted) {
		t.Fatalf("%+v %v", pv.Blockers, err)
	}
	_, err = g.svc.RunDepreciation(g.admin, g.propID, d("2026-09-01"), nextKey())
	wantCode(t, err, "REGISTER_NOT_STARTED")
}

func TestReverseTheLatestRun(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	r1 := f.run(t, "2026-09")
	f.advanceTo(t, "2026-11-01")
	r2 := f.run(t, "2026-10")
	if d := f.asDetail(t, a.ID); !d.AccumDepr.Equal(dec("2000000")) || d.MonthsDone != 2 {
		t.Fatalf("%+v", d.Asset)
	}
	// only the latest run can be reversed, with a reason
	_, err := f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-09-01"), assets.ReverseRunInput{Reason: "wrong"})
	wantCode(t, err, "DEPRECIATION_MONTH_INVALID")
	_, err = f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-10-01"), assets.ReverseRunInput{})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-12-01"), assets.ReverseRunInput{Reason: "wrong"})
	wantCode(t, err, "DEPRECIATION_RUN_NOT_FOUND")
	_, err = f.svc.ReverseDepreciation(f.user(t, auth.PermAssetRead), f.propID, d("2026-10-01"), assets.ReverseRunInput{Reason: "wrong"})
	wantCode(t, err, "PERMISSION_DENIED")

	ctx, trace := db.WithLockTrace(f.boss)
	rev, err := f.svc.ReverseDepreciation(ctx, f.propID, d("2026-10-01"), assets.ReverseRunInput{Reason: "wrong department"})
	must(t, err)
	assertLockOrder(t, trace)
	if rev.Run.Status != "REVERSED" || rev.Run.ReversalJournalID == nil || rev.Run.ReversalReason == nil || rev.Run.ID != r2.Run.ID {
		t.Fatalf("%+v", rev.Run)
	}
	// the mirror journal is dated the last day of the month and every line is the opposite
	mirror := f.journalLines(t, *rev.Run.ReversalJournalID)
	orig := f.journalLines(t, r2.Run.JournalID)
	for code, v := range orig {
		if !mirror[code].Equal(v.Neg()) {
			t.Errorf("account %s: %s against %s", code, mirror[code], v)
		}
	}
	var date, typ string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT journal_date::text, journal_type FROM gl_journals WHERE id = $1`, *rev.Run.ReversalJournalID).Scan(&date, &typ))
	if date != "2026-10-31" || typ != "REVERSAL" {
		t.Errorf("reversal %s %s", date, typ)
	}
	if d := f.asDetail(t, a.ID); !d.AccumDepr.Equal(dec("1000000")) || d.MonthsDone != 1 || d.LastDepMonth.String() != "2026-09-01" {
		t.Fatalf("%+v", d.Asset)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_ledger WHERE property_id = $1 AND kind = 'REVERSAL' AND journal_id = $2`, f.propID, *rev.Run.ReversalJournalID); n != 1 {
		t.Errorf("%d REVERSAL rows", n)
	}
	// reversed twice: no live run to reverse
	_, err = f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-10-01"), assets.ReverseRunInput{Reason: "again"})
	wantCode(t, err, "DEPRECIATION_RUN_NOT_FOUND")
	// the month can be run again: a new row, and the state is the same as before
	r3 := f.run(t, "2026-10")
	if r3.Run.ID == r2.Run.ID || r3.Run.JournalID == r2.Run.JournalID || !r3.Run.Total.Equal(r2.Run.Total) {
		t.Fatalf("%+v", r3.Run)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1 AND period_month = '2026-10-01'`, f.propID); n != 2 {
		t.Errorf("%d rows for October", n)
	}
	o, err := f.svc.ListDepreciation(f.admin, f.propID)
	must(t, err)
	for _, m := range o.Months {
		if isoMonth(m.Month) == "2026-10" && (m.Status != assets.MonthPosted || m.RunID == nil || *m.RunID != r3.Run.ID) {
			t.Errorf("%+v", m)
		}
	}
	// a run is kept: the lines of the reversed one are still there, and nothing can delete them
	if n := f.countRows(t, `SELECT count(*) FROM fa_depr_lines WHERE run_id = $1`, r2.Run.ID); n != 1 {
		t.Errorf("%d lines kept", n)
	}
	_, err = f.Pool.Exec(context.Background(), `DELETE FROM fa_depr_runs WHERE id = $1`, r1.Run.ID)
	if err == nil {
		t.Error("a run was deleted")
	}
}

// F9: the depreciation of a month cannot be reversed once an asset of it has a disposal or a change of category after the month (PR 3 writes them; the check is in place).
func TestReversalIsRefusedWhenTheAssetWasDisposedOfAfterTheMonth(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	a := f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	f.run(t, "2026-09")
	var cat int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT category_id FROM fa_assets WHERE id = $1`, a.ID).Scan(&cat))
	f.exec(t, `INSERT INTO fa_ledger (tenant_id, property_id, asset_id, category_id, kind, event_date, cost_delta, accum_delta, months_delta, source_type, source_id)
		VALUES ($1, $2, $3, $4, 'DISPOSE', '2026-10-01', 0, 0, 0, 'FA_DISPOSAL', 1)`, f.tenantID, f.propID, a.ID, cat)
	_, err := f.svc.ReverseDepreciation(f.boss, f.propID, d("2026-09-01"), assets.ReverseRunInput{Reason: "wrong"})
	wantCode(t, err, "DEPRECIATION_NOT_REVERSIBLE")
	if n := f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1 AND status = 'POSTED'`, f.propID); n != 1 {
		t.Errorf("the refused reversal changed the run")
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// 10.10: the month close and the reopen, for a hotel whose books start today (the opening balances are the first journal)

// closableHotel is a new property whose books start on the business date, 2026-09-30: the opening balances of two assets are imported (the journal date is not given: it is the day the books start),
// the day is closed by the night audit, and the business date is 2026-10-01, so September has ended and is the month to run.
func closableHotel(t *testing.T) *fx {
	t.Helper()
	f := newFx(t)
	f.books(t, "2026-09-30")
	res, err := f.svc.ImportOpening(f.admin, f.propID, assets.OpeningInput{AsOf: d("2026-08-31"), IdempotencyKey: nextKey(), Rows: []assets.OpeningRow{
		row("Old sofa", "FFE", "96000000", "", 0), row("Old van", "VEHICLE", "192000000", "", 0)}})
	must(t, err)
	if res.JournalDate == nil || res.JournalDate.String() != "2026-09-30" || res.RegisterFrom == nil || res.RegisterFrom.String() != "2026-09-01" || res.JournalID == nil {
		t.Fatalf("the opening journal: %+v", res)
	}
	f.closeDay(t)
	return f
}

// The check of the brief: a brand-new hotel whose books start today imports its opening balances. The journal is dated the day the books start and is the first one of the books.
func TestOpeningBalancesOfANewHotelWhoseBooksStartToday(t *testing.T) {
	f := newFx(t)
	f.books(t, "2026-09-30")
	var start, bd string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT s.start_date::text, b.business_date::text FROM accounting_settings s JOIN business_days b ON b.property_id = s.property_id AND b.status = 'OPEN' WHERE s.property_id = $1`, f.propID).Scan(&start, &bd))
	if start != bd {
		t.Fatalf("the books start %s, today is %s", start, bd)
	}
	// a journal date given, equal to the day the books start
	jd := d("2026-09-30")
	dry, err := f.svc.ImportOpening(f.admin, f.propID, assets.OpeningInput{AsOf: d("2026-08-31"), JournalDate: &jd, DryRun: true, Rows: []assets.OpeningRow{row("Sofa", "FFE", "96000000", "", 0)}})
	must(t, err)
	if !dry.Valid || len(dry.Errors) != 0 {
		t.Fatalf("%+v", dry.Errors)
	}
	// and none given: the default is the day after the month end, which is before the books start, so it is the day the books start
	res, err := f.svc.ImportOpening(f.admin, f.propID, assets.OpeningInput{AsOf: d("2026-08-31"), IdempotencyKey: nextKey(), Rows: []assets.OpeningRow{row("Sofa", "FFE", "96000000", "", 0)}})
	must(t, err)
	if res.JournalID == nil || res.JournalDate == nil || res.JournalDate.String() != "2026-09-30" || res.Number == nil {
		t.Fatalf("%+v", res)
	}
	var first string
	must(t, f.Pool.QueryRow(context.Background(), `SELECT min(journal_date)::text FROM gl_journals WHERE property_id = $1`, f.propID).Scan(&first))
	if first != "2026-09-30" {
		t.Errorf("the first journal of the books is dated %s", first)
	}
	// the register starts the month after the balances
	s, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if s.RegisterFrom == nil || s.RegisterFrom.String() != "2026-09-01" {
		t.Fatalf("%+v", s)
	}
}

func TestExample1010TheMonthCloseAndTheReopen(t *testing.T) {
	f := closableHotel(t)
	sep := d("2026-09-01")
	periodOf := func() accounting.Period {
		t.Helper()
		list, err := f.Accounting.Periods(f.admin, f.propID)
		must(t, err)
		for _, p := range list {
			if p.Start.Equal(sep) {
				return p
			}
		}
		t.Fatal("no period for September")
		return accounting.Period{}
	}
	// assets are due and no run: not closable, with the blocker, and the close says so
	p := periodOf()
	if p.Closable || len(p.Blockers) != 1 || p.Blockers[0].Code != "DEPRECIATION_NOT_RUN" || p.PostedDays != p.Days || p.Days != 1 {
		t.Fatalf("%+v", p)
	}
	_, err := f.Accounting.ClosePeriod(f.admin, f.propID, sep)
	wantCode(t, err, "PERIOD_NOT_READY")
	if e, _ := apperr.As(err); e == nil || e.Context["blockers"] == nil {
		t.Fatalf("the blockers are not in the context: %+v", e)
	}
	// the run, then the month is closable and closes
	res := f.run(t, "2026-09")
	if !res.Run.Total.Equal(dec("3000000")) { // 96,000,000 and 192,000,000 over what is left of 96 months, from the opening balances
		t.Fatalf("total %s", res.Run.Total)
	}
	p = periodOf()
	if !p.Closable || len(p.Blockers) != 0 {
		t.Fatalf("%+v", p)
	}
	_, err = f.Accounting.ClosePeriod(f.admin, f.propID, sep)
	must(t, err)
	// a closed month takes no reversal of its run
	_, err = f.svc.ReverseDepreciation(f.boss, f.propID, sep, assets.ReverseRunInput{Reason: "late"})
	wantCode(t, err, "PERIOD_CLOSED")
	if pv := f.preview(t, "2026-09"); pv.CanRun || !hasBlocker(pv, assets.BlockAlreadyRun) {
		t.Fatalf("%+v", pv.Blockers)
	}
	// October is run too (the days in between are closed without a night audit: only the run is under test)
	f.advanceTo(t, "2026-11-01")
	oct := f.run(t, "2026-10")
	assets2 := f.countRows(t, `SELECT count(*) FROM fa_assets WHERE property_id = $1 AND dep_months_done = 34`, f.propID)
	if assets2 != 2 {
		t.Fatalf("%d assets at 34 months", assets2)
	}
	// F9: with a disposal after the month, the reopen is refused and the month stays closed
	var aid, cat int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id, category_id FROM fa_assets WHERE property_id = $1 ORDER BY id LIMIT 1`, f.propID).Scan(&aid, &cat))
	f.exec(t, `INSERT INTO fa_ledger (tenant_id, property_id, asset_id, category_id, kind, event_date, cost_delta, accum_delta, months_delta, source_type, source_id)
		VALUES ($1, $2, $3, $4, 'DISPOSE', '2026-10-05', 0, 0, 0, 'FA_DISPOSAL', 1)`, f.tenantID, f.propID, aid, cat)
	_, err = f.Accounting.ReopenPeriod(f.boss, f.propID, sep, "a wrong department")
	wantCode(t, err, "DEPRECIATION_NOT_REVERSIBLE")
	if p := periodOf(); p.Status != "CLOSED" || f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1 AND status = 'POSTED'`, f.propID) != 2 {
		t.Fatalf("the refused reopen changed something: %+v", p)
	}
	f.exec(t, `DELETE FROM fa_ledger WHERE false`) // the row stays: it is append-only; the next part uses a new hotel
	_ = oct

	g := closableHotel(t)
	g.run(t, "2026-09")
	_, err = g.Accounting.ClosePeriod(g.admin, g.propID, sep)
	must(t, err)
	g.advanceTo(t, "2026-11-01")
	oct = g.run(t, "2026-10")
	journals := g.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, g.propID)
	ctx, trace := db.WithLockTrace(g.boss)
	per, err := g.Accounting.ReopenPeriod(ctx, g.propID, sep, "a wrong department")
	must(t, err)
	assertLockOrder(t, trace)
	if per.Status != "OPEN" {
		t.Fatalf("%+v", per)
	}
	// the runs of September and of October are reversed, in the same transaction: two mirror journals, the assets back to the opening balances
	if n := g.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1 AND status = 'REVERSED' AND reversal_reason LIKE 'Month reopened: a wrong department'`, g.propID); n != 2 {
		t.Fatalf("%d runs reversed", n)
	}
	if n := g.countRows(t, `SELECT count(*) FROM gl_journals WHERE property_id = $1`, g.propID); n != journals+2 {
		t.Fatalf("%d journals made", n-journals)
	}
	if n := g.countRows(t, `SELECT count(*) FROM fa_assets WHERE property_id = $1 AND dep_months_done = 32 AND last_dep_month = '2026-08-01'`, g.propID); n != 2 {
		t.Fatalf("%d assets back at the opening balances", n)
	}
	var octRev, sepRev string
	must(t, g.Pool.QueryRow(context.Background(), `SELECT j.journal_date::text FROM fa_depr_runs r JOIN gl_journals j ON j.id = r.reversal_journal_id WHERE r.id = $1`, oct.Run.ID).Scan(&octRev))
	must(t, g.Pool.QueryRow(context.Background(), `SELECT j.journal_date::text FROM fa_depr_runs r JOIN gl_journals j ON j.id = r.reversal_journal_id WHERE r.property_id = $1 AND r.period_month = '2026-09-01'`, g.propID).Scan(&sepRev))
	if octRev != "2026-10-31" || sepRev != "2026-09-30" {
		t.Errorf("the reversals are dated %s and %s", octRev, sepRev)
	}
	// the books of September are not closable before the run is made again; the month can be run again and closed
	list, err := g.Accounting.Periods(g.admin, g.propID)
	must(t, err)
	for _, p := range list {
		if p.Start.Equal(sep) && (p.Closable || len(p.Blockers) != 1) {
			t.Errorf("%+v", p)
		}
	}
	again := g.run(t, "2026-09")
	if again.Run.ID == 0 || !again.Run.Total.Equal(dec("3000000")) {
		t.Fatalf("%+v", again.Run)
	}
	if _, err := g.Accounting.ClosePeriod(g.admin, g.propID, sep); err != nil {
		t.Fatal(err)
	}
	chk, err := g.svc.SetupCheck(g.admin, g.propID)
	must(t, err)
	for _, r := range chk.Reconciliation {
		if !r.CostDifference.IsZero() || !r.AccumDifference.IsZero() {
			t.Errorf("the register and the books differ for %s: %+v", r.CategoryCode, r)
		}
	}
}

// The first run after an opening import: the reconciliation of the accumulated depreciation accounts with the register holds, with the opening balances and the first month in the books.
func TestControlAccountsReconcileAfterTheFirstRun(t *testing.T) {
	f := closableHotel(t)
	f.run(t, "2026-09")
	chk, err := f.svc.SetupCheck(f.admin, f.propID)
	must(t, err)
	n := 0
	for _, r := range chk.Reconciliation {
		if r.RegisterAccum.IsPositive() {
			n++
		}
		if !r.CostDifference.IsZero() || !r.AccumDifference.IsZero() {
			t.Errorf("%s: %+v", r.CategoryCode, r)
		}
	}
	if n != 2 {
		t.Errorf("%d categories with depreciation", n)
	}
	// the accumulated account takes no manual journal and no bill line (F5): only the register posts to it
	_, err = f.Accounting.PostManual(f.admin, f.propID, accounting.ManualInput{Date: d("2026-09-30"), Description: "x", Lines: []accounting.LineInput{
		{AccountID: f.accounts["1592"], Credit: dec("1000")}, {AccountID: f.accounts["3900"], Debit: dec("1000")}}}, nextKey())
	if err == nil {
		t.Fatal("a manual journal posted to an accumulated depreciation account")
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// Races

func TestTwoRunsOfTheSameMonthRace(t *testing.T) {
	f := newFx(t)
	f.startRegister(t, "2026-09-01", "0")
	f.asset(t, "2026-09-02", "1530", "96000000", nil)
	f.advanceTo(t, "2026-10-01")
	errs := f.race(2, func(i int) error {
		_, err := f.svc.RunDepreciation(f.admin, f.propID, d("2026-09-01"), nextKey())
		return err
	})
	ok, already := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case isCode(err, "DEPRECIATION_ALREADY_RUN"):
			already++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || already != 1 {
		t.Fatalf("%d succeeded, %d already run", ok, already)
	}
	if n := f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1 AND status = 'POSTED'`, f.propID); n != 1 {
		t.Fatalf("%d live runs", n)
	}
	if a := f.countRows(t, `SELECT count(*) FROM fa_assets WHERE property_id = $1 AND dep_months_done = 1`, f.propID); a != 1 {
		t.Fatalf("the asset was depreciated more than once")
	}
}

// A run and the close of its month: the run always succeeds, and the close either waits for the run and closes, or comes first and is refused for the missing run. A month is never closed without it.
func TestRunAndClosePeriodRace(t *testing.T) {
	for round := 0; round < 3; round++ {
		f := closableHotel(t)
		sep := d("2026-09-01")
		errs := f.race(2, func(i int) error {
			if i == 0 {
				_, err := f.svc.RunDepreciation(f.admin, f.propID, sep, nextKey())
				return err
			}
			_, err := f.Accounting.ClosePeriod(f.admin, f.propID, sep)
			return err
		})
		if errs[0] != nil {
			t.Fatalf("round %d: the run said %v", round, errs[0])
		}
		closed := f.countRows(t, `SELECT count(*) FROM gl_periods WHERE property_id = $1 AND status = 'CLOSED'`, f.propID)
		live := f.countRows(t, `SELECT count(*) FROM fa_depr_runs WHERE property_id = $1 AND status = 'POSTED'`, f.propID)
		switch {
		case errs[1] == nil && closed != 1:
			t.Fatalf("round %d: the close succeeded and the month is not closed", round)
		case errs[1] != nil && !isCode(errs[1], "PERIOD_NOT_READY"):
			t.Fatalf("round %d: the close said %v", round, errs[1])
		case closed == 1 && live != 1:
			t.Fatalf("round %d: the month is closed without a run", round)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------------
// The depreciation never meets a stock or an event lock, and the guard of the close reads without locks

func TestCloseBlockersIgnoreMonthsBeforeTheRegisterAndPropertiesWithoutOne(t *testing.T) {
	f := newFx(t)
	bl, err := f.svc.CloseBlockers(f.admin, f.tenantID, f.propID, d("2026-09-01"))
	must(t, err)
	if len(bl) != 0 {
		t.Fatalf("a property without a register is blocked: %+v", bl)
	}
	f.startRegister(t, "2026-09-01", "0")
	f.asset(t, "2026-09-02", "1530", "96000000", nil)
	if bl, err := f.svc.CloseBlockers(f.admin, f.tenantID, f.propID, d("2026-08-01")); err != nil || len(bl) != 0 {
		t.Fatalf("%+v %v", bl, err)
	}
	if bl, err := f.svc.CloseBlockers(f.admin, f.tenantID, f.propID, d("2026-09-01")); err != nil || len(bl) != 1 || bl[0].Code != "DEPRECIATION_NOT_RUN" {
		t.Fatalf("%+v %v", bl, err)
	}
}
