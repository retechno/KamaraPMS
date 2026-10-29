package main

import (
	"context"
	"fmt"
	"io"
	"sort"

	"kamarapms/internal/assets"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/platform/civil"
)

// The demo of the stock-take and of the repair request that names an asset (docs/architecture/37-fixed-assets.md, PR 4): a stock-take of one place that is still open, with a scan of each kind
// (found where it should be, found in another place, found and damaged) and what has not been scanned yet (what the close would call missing), and a repair request about an asset.
const (
	demoAuditKey         = "pms-seed-assets-audit"
	demoAuditNote        = "Opname triwulanan (demo)"
	demoAuditDamagedNote = "Casing retak dan engsel longgar (demo opname)"
	demoRepairCategory   = "APPLIANCE"
	demoRepairPriority   = "NORMAL"
	demoRepairDesc       = "Unit mati mendadak, minta diperiksa teknisi (demo perbaikan aset)"
)

// auditRows is how many rows the demo makes: the stock-take, and the repair request.
const auditRows = 2

// auditPlaces are the places the demo may take for the stock-take, in the order it tries them: the first that has enough assets.
var auditPlaces = []string{"GUDTEK", "LOBI", "KANTOR"}

// seedAudit makes the open stock-take and the repair request of the demo. Safe to run again: each is found again (a stock-take that exists in any state is left alone, a request is found by its text).
func seedAudit(ctx context.Context, svc *assets.Service, d *seedDeps, pid int64, businessDate civil.Date, dryRun bool, out io.Writer) (int, error) {
	list, err := svc.ListAssets(ctx, pid, assets.AssetFilter{Limit: 100})
	if err != nil {
		return 0, err
	}
	if len(list.Data) == 0 {
		// the register of the demo is not there yet (a dry run before the import): say what the real run does
		if dryRun {
			_, _ = fmt.Fprintf(out, "stock-take: %d (an open stock-take of a place, a repair request about an asset)\n", auditRows)
			return auditRows, nil
		}
		_, _ = fmt.Fprintln(out, "stock-take: skipped, the demo assets are not in the register")
		return 0, nil
	}
	if cfg, err := svc.GetSettings(ctx, pid); err != nil {
		return 0, err
	} else if cfg.RegisterFrom == nil || cfg.RegisterFrom.After(businessDate) {
		_, _ = fmt.Fprintln(out, "stock-take: skipped, the register starts after the business date")
		return 0, nil
	}
	made := 0
	n, err := seedStockTake(ctx, svc, pid, list.Data, dryRun, out)
	if err != nil {
		return made, err
	}
	made += n
	n, err = seedRepair(ctx, d, pid, list.Data, dryRun, out)
	return made + n, err
}

// seedStockTake opens the stock-take of the demo and writes its scans.
func seedStockTake(ctx context.Context, svc *assets.Service, pid int64, all []assets.Asset, dryRun bool, out io.Writer) (int, error) {
	audits, err := svc.ListAudits(ctx, pid, "")
	if err != nil {
		return 0, err
	}
	if len(audits) > 0 {
		_, _ = fmt.Fprintf(out, "stock-take: %s exists\n", audits[0].Number)
		return 0, nil
	}
	locs, err := svc.ListLocations(ctx, pid, "", nil)
	if err != nil {
		return 0, err
	}
	byCode := map[string]assets.Location{}
	for _, l := range locs {
		byCode[l.Code] = l
	}
	// the place with the most assets among the places of the demo: the more, the more kinds of scan it can show
	var place assets.Location
	var expected []assets.Asset
	for _, code := range auditPlaces {
		l, ok := byCode[code]
		if !ok {
			continue
		}
		var here []assets.Asset
		for _, a := range all {
			if a.Status == "ACTIVE" && a.LocationID != nil && *a.LocationID == l.ID {
				here = append(here, a)
			}
		}
		if len(here) > len(expected) {
			place, expected = l, here
		}
	}
	if len(expected) == 0 {
		_, _ = fmt.Fprintln(out, "stock-take: skipped, no demo place holds an asset")
		return 0, nil
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].Number < expected[j].Number })
	_, _ = fmt.Fprintf(out, "stock-take: one of %s (%d assets expected) to open\n", place.Code, len(expected))
	if dryRun {
		return 1, nil
	}
	a, err := svc.StartAudit(ctx, pid, assets.StartAuditInput{LocationID: &place.ID, Note: demoAuditNote, IdempotencyKey: demoAuditKey})
	if err != nil {
		return 0, err
	}
	scans := 0
	scan := func(in assets.ScanInput) error {
		if _, err := svc.ScanAsset(ctx, pid, a.ID, in); err != nil {
			return err
		}
		scans++
		return nil
	}
	// the first is found where it should be, the second is broken, the rest stay unscanned (the close would call them missing)
	if err := scan(assets.ScanInput{Code: expected[0].Number}); err != nil {
		return 1, err
	}
	if len(expected) > 2 {
		if err := scan(assets.ScanInput{Code: expected[1].Number, Damaged: true, Note: demoAuditDamagedNote}); err != nil {
			return 1, err
		}
	}
	// an asset of another place that turned up here
	for _, o := range all {
		if o.Status == "ACTIVE" && (o.LocationID == nil || *o.LocationID != place.ID) && o.CategoryCode != "LAND" && o.CategoryCode != "CIP" && o.CategoryCode != "BUILDING" {
			if err := scan(assets.ScanInput{Code: o.Number}); err != nil {
				return 1, err
			}
			break
		}
	}
	_, _ = fmt.Fprintf(out, "stock-take: %s opened, %d scans\n", a.Number, scans)
	return 1, nil
}

// seedRepair files the repair request about an asset of the demo; it is skipped when the plan has no asset register for it.
func seedRepair(ctx context.Context, d *seedDeps, pid int64, all []assets.Asset, dryRun bool, out io.Writer) (int, error) {
	if d == nil || d.maint == nil {
		return 0, nil
	}
	var target *assets.Asset
	for i := range all {
		if all[i].Status == "ACTIVE" && all[i].CategoryCode == "FFE" {
			target = &all[i]
			break
		}
	}
	if target == nil {
		_, _ = fmt.Fprintln(out, "repair request: skipped, no demo asset to name")
		return 0, nil
	}
	have, err := d.maint.List(ctx, pid, maintenance.Filter{}, nil, 200)
	if err != nil {
		return 0, err
	}
	for _, r := range have {
		if r.Description == demoRepairDesc {
			_, _ = fmt.Fprintf(out, "repair request: %s exists\n", r.RequestNumber)
			return 0, nil
		}
	}
	_, _ = fmt.Fprintf(out, "repair request about %s to file\n", target.Number)
	if dryRun {
		return 1, nil
	}
	r, err := d.maint.Create(ctx, pid, maintenance.CreateInput{AssetID: &target.ID, Location: derefOr(target.LocationName, "Properti"), Category: demoRepairCategory, Description: demoRepairDesc, Priority: demoRepairPriority})
	if err != nil {
		_, _ = fmt.Fprintf(out, "repair request: skipped, %v\n", err)
		return 0, nil
	}
	_, _ = fmt.Fprintf(out, "repair request %s filed about %s\n", r.RequestNumber, target.Number)
	return 1, nil
}

func derefOr(s *string, def string) string {
	if s == nil || *s == "" {
		return def
	}
	return *s
}
