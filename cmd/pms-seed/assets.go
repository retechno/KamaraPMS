package main

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets"
	"kamarapms/internal/departments"
	"kamarapms/internal/iam"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/taxfiling"
)

// pms-seed assets: demo data of the fixed asset register (docs/architecture/37-fixed-assets.md, PR 1) for a DEVELOPMENT database, through the services of the application. The default categories
// come from the migration, so the seed makes:
//
//	locations   free areas (lobby, technical store, back garden); rooms, meeting rooms and warehouses are made by the import itself when the demo of rooms, banquet or inventory is there
//	opening     one import WITH journal, as at the last day of the month before the business date: thirteen old assets in Indonesian across the categories, among them land and a building under
//	            construction (not depreciated), two assets already depreciated down to their residual value, and the wardrobe of docs/architecture/37-fixed-assets.md 10.15 (the commercial book still
//	            runs, the fiscal one is finished). The import also sets the start of the register (register_from). Its journal is dated the month end when the books allow, and one more journal
//	            books a plot of land (750000000) in the ledger only, so that a REGISTER_ONLY import of that plot ties out (and any other file does not).
//
//	waiting     five cost lines nobody registered yet (a bill of two lines, the cashier, the bank, a manual journal), dated the business date (PR 2)
//	disposals   the write-off of 50 pieces of the batch of linen, posted, and the sale of an old server that waits for an approver (the seed sets the rule of ASSET_DISPOSAL to DISTINCT) (PR 3)
//
// Safe to run again: locations that exist (by code) are left alone and the import is skipped once the register has one.

type assetLocation struct{ code, name string }

var assetAreas = []assetLocation{
	{"LOBI", "Lobi utama"},
	{"GUDTEK", "Gudang teknik"},
	{"KEBUN", "Kebun belakang"},
	{"KANTOR", "Kantor depan"},
}

// assetSpec describes an old asset by its age: the import wants dates and accumulated depreciation, which the seed derives from the month end it brings the assets in at.
type assetSpec struct {
	name, category, location string
	cost, residual           int64
	life                     int // months, 0 for land and construction in progress
	age                      int // months already depreciated at the month end (the start month counts)
	units                    int
	serial                   string
	dept                     string // code of the department; empty for none (the demo leaves one asset without, so that the depreciation shows its blocker)
	fiscal                   *assetFiscal
}

type assetFiscal struct {
	group, method string
	accum         int64
	months        int
}

var assetSpecs = []assetSpec{
	{name: "Tanah hotel", category: "LAND", location: "KEBUN", cost: 2_500_000_000},
	{name: "Gedung utama", dept: "POM", category: "BUILDING", location: "LOBI", cost: 12_000_000_000, life: 240, age: 60},
	{name: "Sofa lobi", dept: "POM", category: "FFE", location: "LOBI", cost: 36_000_000, life: 96, age: 56, serial: "SF-LB-001"},
	{name: "Lemari kayu kamar 101", dept: "ROOMS", category: "FFE", location: "ROOM:101", cost: 36_000_000, life: 96, age: 81, serial: "LK-101",
		fiscal: &assetFiscal{group: "G1", method: "SL", accum: 36_000_000, months: 48}},
	{name: "Tempat tidur kamar 102", dept: "ROOMS", category: "FFE", location: "ROOM:102", cost: 18_000_000, life: 96, age: 40},
	{name: "AC split kamar 201", dept: "ROOMS", category: "FFE", location: "ROOM:201", cost: 9_500_000, life: 96, age: 100, serial: "AC-201"},
	{name: "Genset 250 kVA", dept: "POM", category: "FFE", location: "GUDTEK", cost: 150_000_000, life: 96, age: 30, serial: "GS-250-01"},
	{name: "Sound system ballroom", dept: "FB", category: "FFE", location: "BQ:BALL", cost: 85_000_000, life: 96, age: 36},
	{name: "Toyota Innova antar-jemput", dept: "AG", category: "VEHICLE", location: "KEBUN", cost: 420_000_000, residual: 42_000_000, life: 96, age: 24, serial: "DK 1234 AB"},
	{name: "Laptop front office", category: "IT", location: "KANTOR", cost: 14_500_000, life: 48, age: 20, serial: "LT-FO-07"},
	{name: "Server reservasi", dept: "AG", category: "IT", location: "KANTOR", cost: 48_000_000, life: 48, age: 52, serial: "SRV-01"},
	{name: "Linen kamar (batch)", dept: "ROOMS", category: "OE", location: "GUDTEK", cost: 36_000_000, life: 24, age: 6, units: 200},
	{name: "Vila baru (dalam pembangunan)", category: "CIP", location: "KEBUN", cost: 1_200_000_000},
}

func seedAssets(e *env, dryRun bool) error {
	iamSvc := iam.NewService(e.txm, e.clk, e.aw, iam.TokenConfig{Secret: randomSecret(), AccessTTL: time.Minute, RefreshTTL: time.Minute})
	acct := accounting.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, iamSvc)
	deps := departments.NewService(e.txm, e.clk, e.aw, e.authz, e.ten)
	svc := assets.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, acct, deps)
	svc.SetApprover(iamSvc)
	acct.SetAssetSource(svc)
	tax := taxfiling.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, acct, iamSvc)
	pay := payables.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, acct, iamSvc, tax)
	pay.SetAssetGuard(svc)
	day, err := e.ten.CurrentBusinessDay(e.ctx, e.propertyID)
	if err != nil {
		return err
	}
	_, err = seedAssetsWith(e.ctx, svc, acct, &seedDeps{pay: pay, txm: e.txm, days: e.ten, depts: deps, rules: iam.NewRules(iamSvc, e.authz), iam: iamSvc}, e.propertyID, day.BusinessDate, dryRun, os.Stdout)
	return err
}

func randomSecret() []byte {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return b
}

// monthEndBefore is the last day of the month before the one of d.
func monthEndBefore(d civil.Date) civil.Date {
	return civil.NewDate(d.Year(), d.Month(), 1).AddDays(-1)
}

// seedAssetsWith does the work; the return value is the number of rows created (or that would be).
func seedAssetsWith(ctx context.Context, svc *assets.Service, acct *accounting.Service, d *seedDeps, pid int64, businessDate civil.Date, dryRun bool, out io.Writer) (int, error) {
	made := 0
	deptCodes, err := departmentCodes(ctx, d, pid)
	if err != nil {
		return 0, err
	}
	existing, err := svc.ListLocations(ctx, pid, "", nil)
	if err != nil {
		return 0, err
	}
	have := map[string]bool{}
	for _, l := range existing {
		have[l.Code] = true
	}
	for _, a := range assetAreas {
		if have[a.code] {
			_, _ = fmt.Fprintf(out, "location %s exists\n", a.code)
			continue
		}
		made++
		_, _ = fmt.Fprintf(out, "location %s (%s): to create\n", a.code, a.name)
		if dryRun {
			continue
		}
		if _, err := svc.CreateLocation(ctx, pid, assets.LocationInput{Code: a.code, Name: a.name}); err != nil {
			return made, err
		}
	}

	imports, err := svc.ListOpeningImports(ctx, pid)
	if err != nil {
		return made, err
	}
	if len(imports) > 0 {
		_, _ = fmt.Fprintf(out, "opening balances exist (%s)\n", imports[0].Number)
		n, err := seedAfterOpening(ctx, svc, acct, d, pid, businessDate, dryRun, out)
		return made + n, err
	}

	asOf, journalDate := pickAsOf(ctx, svc, pid, businessDate)
	if journalDate == nil {
		// the journal is dated the month end itself, so that the ledger at the month end holds the assets: a later import in the mode REGISTER_ONLY can then tie out to it
		if jd := asOf; tryAsOf(ctx, svc, pid, asOf, &jd) {
			journalDate = &jd
		}
	}
	if dryRun && made > 0 { // the areas the rows name do not exist yet, so the import cannot be tried: say what the real run does
		_, _ = fmt.Fprintf(out, "opening balances as at %s: %d assets to import\n", asOf, len(assetSpecs))
		_, _ = fmt.Fprintln(out, "dry run: would create the import above")
		n, err := seedAfterOpening(ctx, svc, acct, d, pid, businessDate, true, out)
		return made + len(assetSpecs) + n, err
	}
	rows, err := assetRows(ctx, svc, pid, asOf, deptCodes)
	if err != nil {
		return made, err
	}
	res, err := svc.ImportOpening(ctx, pid, assets.OpeningInput{AsOf: asOf, JournalDate: journalDate, Rows: rows, DryRun: true})
	if err != nil {
		return made, err
	}
	if !res.Valid {
		for _, f := range res.Errors {
			_, _ = fmt.Fprintf(out, "row %d %s: %s\n", f.RowNo, f.Field, f.Message)
		}
		return made, fmt.Errorf("the demo assets are not valid for this property")
	}
	_, _ = fmt.Fprintf(out, "opening balances as at %s: %d assets, cost %s, accumulated %s, to 3900 %s\n", asOf, len(res.Rows), res.TotalCost, res.TotalAccum, res.NetToEquity)
	made += len(res.Rows)
	if dryRun {
		_, _ = fmt.Fprintln(out, "dry run: would create the import above")
		n, err := seedAfterOpening(ctx, svc, acct, d, pid, businessDate, true, out)
		return made + n, err
	}
	if _, err := svc.ImportOpening(ctx, pid, assets.OpeningInput{AsOf: asOf, JournalDate: journalDate, Rows: rows, IdempotencyKey: "pms-seed-assets-opening"}); err != nil {
		return made, err
	}
	_, _ = fmt.Fprintln(out, "opening balances imported")
	if journalDate != nil && !journalDate.After(asOf) {
		if err := seedUnregisteredLand(ctx, svc, acct, pid, *journalDate, out); err != nil {
			return made, err
		}
	}
	n, err := seedAfterOpening(ctx, svc, acct, d, pid, businessDate, false, out)
	return made + n, err
}

// seedAfterOpening makes what comes after the opening balances: the lines of the waiting list and the disposals (docs/architecture/37-fixed-assets.md, PR 2 and 3).
func seedAfterOpening(ctx context.Context, svc *assets.Service, acct *accounting.Service, d *seedDeps, pid int64, businessDate civil.Date, dryRun bool, out io.Writer) (int, error) {
	n, err := seedWaiting(ctx, svc, acct, d, pid, businessDate, dryRun, out)
	if err != nil {
		return n, err
	}
	m, err := seedDisposals(ctx, svc, acct, d, pid, businessDate, dryRun, out)
	return n + m, err
}

// unregisteredLandCost is the cost of the plot that the ledger holds but the register does not (yet): an import in the mode REGISTER_ONLY with that one plot (category LAND, cost 750000000,
// in service before the month end) ties out to the ledger, while any other file shows the differences.
const unregisteredLandCost = 750_000_000

// seedUnregisteredLand books the cost of a plot of land in the ledger on its own (Dr the cost account of LAND, Cr 3900), dated the month end of the opening balances, for the demo of the
// register-only import.
func seedUnregisteredLand(ctx context.Context, svc *assets.Service, acct *accounting.Service, pid int64, date civil.Date, out io.Writer) error {
	cats, err := svc.ListCategories(ctx, pid)
	if err != nil {
		return err
	}
	var land int64
	for _, c := range cats {
		if c.Code == "LAND" {
			land = c.CostAccountID
		}
	}
	accts, err := acct.Accounts(ctx, pid, accounting.AccountFilter{Query: "3900"})
	if err != nil {
		return err
	}
	var equity int64
	for _, a := range accts {
		if a.Code == "3900" {
			equity = a.ID
		}
	}
	if land == 0 || equity == 0 {
		_, _ = fmt.Fprintln(out, "no LAND category or account 3900: the plot of the register-only demo is not booked")
		return nil
	}
	amount := decimal.NewFromInt(unregisteredLandCost)
	if _, err := acct.PostManual(ctx, pid, accounting.ManualInput{Date: date, Description: "Tanah parkir (demo saldo awal, hanya register)", Reference: "DEMO-LAND", Lines: []accounting.LineInput{
		{AccountID: land, Debit: amount},
		{AccountID: equity, Credit: amount},
	}}, "pms-seed-assets-land"); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "ledger holds a plot of land worth %s that the register does not: a REGISTER_ONLY import of it ties out\n", amount)
	return nil
}

// assetRows turns the specs into the rows of an import as at asOf. A location that names a room, a meeting room or a warehouse falls back to a free area when that master is not there.
func assetRows(ctx context.Context, svc *assets.Service, pid int64, asOf civil.Date, deptCodes map[string]bool) ([]assets.OpeningRow, error) {
	rows := make([]assets.OpeningRow, 0, len(assetSpecs))
	for _, s := range assetSpecs {
		start := monthsBefore(asOf, s.age-1)
		inService := civil.NewDate(start.Year(), start.Month(), 15)
		r := assets.OpeningRow{Name: s.name, Category: s.category, Location: s.location, Cost: decimal.NewFromInt(s.cost), InServiceDate: &inService, SerialNo: s.serial}
		if !isMaster(s.location) {
			// a free area: nothing to resolve
		} else if !masterExists(ctx, svc, pid, asOf, s.location) {
			r.Location = "LOBI"
		}
		if deptCodes[s.dept] {
			r.Department = s.dept
		}
		if s.residual > 0 {
			r.ResidualValue = ptr(decimal.NewFromInt(s.residual))
		}
		if s.life > 0 {
			r.LifeMonths = ptr(s.life)
			done := min(s.age, s.life)
			accum := decimal.NewFromInt(s.cost - s.residual).Mul(decimal.NewFromInt(int64(done))).Div(decimal.NewFromInt(int64(s.life))).Round(0)
			r.AccumulatedDepreciation = &accum
			r.MonthsDone = ptr(done)
		}
		if s.units > 0 {
			r.UnitCount = ptr(s.units)
		}
		if s.fiscal != nil {
			r.FiscalGroup, r.FiscalMethod = s.fiscal.group, s.fiscal.method
			r.FiscalAccumulatedDepreciation = ptr(decimal.NewFromInt(s.fiscal.accum))
			r.FiscalMonthsDone = ptr(s.fiscal.months)
		}
		rows = append(rows, r)
	}
	return rows, nil
}

func ptr[T any](v T) *T { return &v }

func isMaster(loc string) bool {
	return strings.HasPrefix(loc, "ROOM:") || strings.HasPrefix(loc, "BQ:") || strings.HasPrefix(loc, "WH:")
}

// masterExists asks whether a room, meeting room or warehouse named by a cell of the file exists, by trying a dry run of one row on it.
func masterExists(ctx context.Context, svc *assets.Service, pid int64, asOf civil.Date, cell string) bool {
	inService := civil.NewDate(asOf.Year(), asOf.Month(), 1)
	res, err := svc.ImportOpening(ctx, pid, assets.OpeningInput{AsOf: asOf, DryRun: true, Rows: []assets.OpeningRow{{Name: "x", Category: "LAND", Location: cell, Cost: decimal.NewFromInt(1), InServiceDate: &inService}}})
	if err != nil {
		return false
	}
	for _, f := range res.Errors {
		if f.Field == "location" {
			return false
		}
	}
	return true
}

// monthsBefore is the same day n months earlier, as the last day of that month when d is a month end.
func monthsBefore(d civil.Date, n int) civil.Date {
	first := civil.NewDate(d.Year(), d.Month(), 1)
	for i := 0; i < n; i++ {
		first = civil.NewDate(first.Year(), first.Month(), 1).AddDays(-1)
		first = civil.NewDate(first.Year(), first.Month(), 1)
	}
	return first
}

// pickAsOf chooses the month end the old assets come in at, and the date of the journal: the last day of the month before the business date, or the business date itself when it is a month end
// and the books start too late for the first (a one-row dry run tells). When the books start after both, the month end before stays and the journal is dated the business date.
func pickAsOf(ctx context.Context, svc *assets.Service, pid int64, bd civil.Date) (civil.Date, *civil.Date) {
	candidates := []civil.Date{monthEndBefore(bd)}
	if bd.AddDays(1).Day() == 1 {
		candidates = append(candidates, bd)
	}
	// the journal dated the month end itself comes first: the ledger at the month end then holds the assets, which the register-only demo needs
	for _, c := range candidates {
		if jd := c; tryAsOf(ctx, svc, pid, c, &jd) {
			return c, &jd
		}
	}
	for _, c := range candidates {
		if tryAsOf(ctx, svc, pid, c, nil) {
			return c, nil
		}
	}
	return candidates[0], &bd
}

func tryAsOf(ctx context.Context, svc *assets.Service, pid int64, c civil.Date, journal *civil.Date) bool {
	inService := civil.NewDate(c.Year(), c.Month(), 1)
	res, err := svc.ImportOpening(ctx, pid, assets.OpeningInput{AsOf: c, JournalDate: journal, DryRun: true, Rows: []assets.OpeningRow{{Name: "x", Category: "LAND", Cost: decimal.NewFromInt(1), InServiceDate: &inService}}})
	if err != nil {
		return false
	}
	for _, f := range res.Errors {
		if f.Field == "journal_date" || f.Field == "as_of" {
			return false
		}
	}
	return true
}
