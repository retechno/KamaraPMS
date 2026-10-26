package assets

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

func assetFrom(r assetsdb.GetAssetRow) Asset {
	a := Asset{ID: r.ID, Number: r.Number, Name: r.Name, CategoryID: r.CategoryID, CategoryCode: r.CategoryCode, CategoryName: r.CategoryName, CategoryKind: r.CategoryKind,
		LocationID: r.LocationID, LocationCode: r.LocationCode, LocationName: r.LocationName, DepartmentID: r.DepartmentID, DepartmentCode: r.DepartmentCode, DepartmentName: r.DepartmentName,
		SupplierID: r.SupplierID, SupplierName: r.SupplierName, Source: r.Source, OpeningImportID: r.OpeningImportID, Status: r.Status, AcquisitionDate: r.AcquisitionDate,
		InServiceDate: r.InServiceDate, StartRule: r.StartRule, FirstDepMonth: r.FirstDepMonth, Cost: r.Cost, ResidualValue: r.ResidualValue, AccumDepr: r.AccumDepr,
		CarryingValue: r.Cost.Sub(r.AccumDepr), LifeMonths: r.LifeMonths, MonthsDone: r.DepMonthsDone, LastDepMonth: r.LastDepMonth, Method: r.Method, DBFactor: r.DbFactor,
		UnitCount: r.UnitCount, SerialNo: r.SerialNo, WarrantyUntil: r.WarrantyUntil, Notes: r.Notes, FiscalGroup: r.FiscalGroup, FiscalMethod: r.FiscalMethod,
		FiscalAccumOpening: r.FiscalAccumOpening, FiscalMonthsDoneOpening: r.FiscalMonthsDoneOpening, FiscalOpeningAsOf: r.FiscalOpeningAsOf,
		FiscalOpeningEstimated: r.FiscalOpeningEstimated, Version: r.Version}
	if r.LifeMonths != nil {
		left := *r.LifeMonths - r.DepMonthsDone
		a.RemainingMonths = &left
		a.FullyDepreciated = r.AccumDepr.GreaterThanOrEqual(r.Cost.Sub(r.ResidualValue))
	}
	return a
}

func (f AssetFilter) params(tenantID, propertyID int64) (assetsdb.ListAssetsParams, assetsdb.SummarizeAssetsParams) {
	var status, q *string
	if f.Status != "" {
		status = &f.Status
	}
	if t := trimmed(f.Q); t != "" {
		q = &t
	}
	lp := assetsdb.ListAssetsParams{TenantID: tenantID, PropertyID: propertyID, CategoryID: f.CategoryID, LocationID: f.LocationID, DepartmentID: f.DepartmentID, Status: status, Q: q,
		WarrantyBefore: f.WarrantyBefore, FullyDepreciated: f.FullyDepreciated}
	sp := assetsdb.SummarizeAssetsParams{TenantID: tenantID, PropertyID: propertyID, CategoryID: f.CategoryID, LocationID: f.LocationID, DepartmentID: f.DepartmentID, Status: status, Q: q,
		WarrantyBefore: f.WarrantyBefore, FullyDepreciated: f.FullyDepreciated}
	return lp, sp
}

// ListAssets lists the register, a page at a time, with the summary of the whole filter (any asset permission).
func (s *Service) ListAssets(ctx context.Context, propertyID int64, f AssetFilter) (AssetPage, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return AssetPage{}, err
	}
	if f.Limit < 1 || f.Limit > maxPageRow {
		f.Limit = 50
	}
	return s.assetPage(ctx, p, propertyID, f)
}

// ExportAssets lists the register for a CSV: up to ten thousand rows.
func (s *Service) ExportAssets(ctx context.Context, propertyID int64, f AssetFilter) ([]Asset, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	f.Limit, f.Offset = 10000, 0
	page, err := s.assetPage(ctx, p, propertyID, f)
	return page.Data, err
}

func (s *Service) assetPage(ctx context.Context, p auth.Principal, propertyID int64, f AssetFilter) (AssetPage, error) {
	q := s.q(ctx)
	lp, sp := f.params(p.TenantID, propertyID)
	lp.RowLimit, lp.RowOffset = int32(f.Limit), int32(f.Offset) //nolint:gosec // G115: bounded above
	rows, err := q.ListAssets(ctx, lp)
	if err != nil {
		return AssetPage{}, err
	}
	sum, err := q.SummarizeAssets(ctx, sp)
	if err != nil {
		return AssetPage{}, err
	}
	out := AssetPage{Data: make([]Asset, 0, len(rows)), Total: int(sum.Total), Summary: AssetSummary{Count: int(sum.Total), Cost: sum.Cost, AccumDepr: sum.Accum, CarryingValue: sum.Cost.Sub(sum.Accum)}}
	for _, r := range rows {
		out.Data = append(out.Data, assetFrom(assetsdb.GetAssetRow(r)))
	}
	return out, nil
}

// GetAsset answers one asset with its ledger (any asset permission).
func (s *Service) GetAsset(ctx context.Context, propertyID, id int64) (AssetDetail, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return AssetDetail{}, err
	}
	return s.assetDetail(ctx, p, propertyID, id)
}

func (s *Service) assetDetail(ctx context.Context, p auth.Principal, propertyID, id int64) (AssetDetail, error) {
	q := s.q(ctx)
	r, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return AssetDetail{}, orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
	}
	d := AssetDetail{Asset: assetFrom(r), Ledger: []LedgerEntry{}}
	if r.OpeningImportID != nil {
		if imp, err := q.GetOpeningImport(ctx, assetsdb.GetOpeningImportParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *r.OpeningImportID}); err == nil {
			d.OpeningImportNumber = &imp.Number
		}
	}
	rows, err := q.ListAssetLedger(ctx, assetsdb.ListAssetLedgerParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id})
	if err != nil {
		return AssetDetail{}, err
	}
	for _, e := range rows {
		d.Ledger = append(d.Ledger, LedgerEntry{ID: e.ID, Kind: e.Kind, EventDate: e.EventDate, BookMonth: e.BookMonth, CostDelta: e.CostDelta, AccumDelta: e.AccumDelta, MonthsDelta: e.MonthsDelta,
			JournalID: e.JournalID, SourceType: e.SourceType, SourceID: e.SourceID, ReversesID: e.ReversesID, CreatedAt: e.CreatedAt})
	}
	return d, nil
}

// LookupAssets finds active assets by number, name or serial number for a picker. It shows no value: whoever reports a maintenance problem may pick an asset (asset.read, asset.manage or
// maintenance.report).
func (s *Service) LookupAssets(ctx context.Context, propertyID int64, query string) ([]LookupAsset, error) {
	p, err := s.require(ctx, propertyID, append(append([]auth.Permission{}, readers...), auth.PermMaintenanceReport)...)
	if err != nil {
		return nil, err
	}
	var qs *string
	if t := trimmed(query); t != "" {
		qs = &t
	}
	rows, err := s.q(ctx).LookupAssets(ctx, assetsdb.LookupAssetsParams{TenantID: p.TenantID, PropertyID: propertyID, Q: qs})
	if err != nil {
		return nil, err
	}
	out := make([]LookupAsset, 0, len(rows))
	for _, r := range rows {
		out = append(out, LookupAsset{ID: r.ID, Number: r.Number, Name: r.Name, LocationName: r.LocationName})
	}
	return out, nil
}

// UpdateAsset changes the data of an asset that is not money: the name, where it stands, which department, the supplier, the serial number, the warranty and the notes (asset.manage). The
// version read must be the current one.
func (s *Service) UpdateAsset(ctx context.Context, propertyID, id int64, in AssetPatch) (AssetDetail, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return AssetDetail{}, err
	}
	in.Name, in.SerialNo, in.Notes = trimmed(in.Name), optional(in.SerialNo), optional(in.Notes)
	var f []apperr.FieldError
	if n := len([]rune(in.Name)); n < 1 || n > maxName {
		f = append(f, fieldErr("name", "INVALID_VALUE", fmt.Sprintf("1 to %d characters", maxName)))
	}
	if in.SerialNo != nil && len([]rune(*in.SerialNo)) > maxSerial {
		f = append(f, fieldErr("serial_no", "INVALID_VALUE", fmt.Sprintf("%d characters at most", maxSerial)))
	}
	if in.Notes != nil && len([]rune(*in.Notes)) > maxNotes {
		f = append(f, fieldErr("notes", "INVALID_VALUE", fmt.Sprintf("%d characters at most", maxNotes)))
	}
	if len(f) > 0 {
		return AssetDetail{}, invalid("asset", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.FAAssets, db.ForNoKeyUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		q := s.q(ctx)
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		if cur.Status != statusActive {
			return apperr.Conflict("ASSET_STATUS_INVALID", "only an active asset is changed").WithContext("status", cur.Status)
		}
		if in.LocationID != nil {
			loc, err := q.GetLocation(ctx, assetsdb.GetLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.LocationID})
			if err != nil {
				return orNotFound(err, invalid("asset", fieldErr("location_id", "NOT_FOUND", "no such location in this property")))
			}
			if !loc.IsActive && (cur.LocationID == nil || *cur.LocationID != loc.ID) {
				return invalid("asset", fieldErr("location_id", "INACTIVE", "the location is switched off"))
			}
		}
		if in.DepartmentID != nil {
			if err := s.depts.Check(ctx, p.TenantID, propertyID, *in.DepartmentID, "department_id"); err != nil {
				return err
			}
		}
		if _, err := q.UpdateAssetDetails(ctx, assetsdb.UpdateAssetDetailsParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, LocationID: in.LocationID,
			DepartmentID: in.DepartmentID, SupplierID: in.SupplierID, SerialNo: in.SerialNo, WarrantyUntil: in.WarrantyUntil, Notes: in.Notes, ActorID: p.ActorID()}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return versionConflict("asset", cur.Version)
			}
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.updated", "fa_asset", id, cur.Number,
			map[string]any{"name": cur.Name, "location_id": cur.LocationID, "department_id": cur.DepartmentID, "supplier_id": cur.SupplierID, "serial_no": cur.SerialNo, "warranty_until": cur.WarrantyUntil},
			map[string]any{"name": in.Name, "location_id": in.LocationID, "department_id": in.DepartmentID, "supplier_id": in.SupplierID, "serial_no": in.SerialNo, "warranty_until": in.WarrantyUntil}))
	})
	if err != nil {
		return AssetDetail{}, err
	}
	return s.assetDetail(ctx, p, propertyID, id)
}

// CancelAsset takes an asset out of the register while the register has not depreciated it and nothing has been done to it (asset.manage, a reason). Its journal, when the asset came with one,
// is reversed by hand. This is also what lets a bill or a journal of a later PR be voided.
func (s *Service) CancelAsset(ctx context.Context, propertyID, id int64, in CancelInput) (AssetDetail, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return AssetDetail{}, err
	}
	in.Reason = trimmed(in.Reason)
	if n := len([]rune(in.Reason)); n < 1 || n > 300 {
		return AssetDetail{}, invalid("cancellation", fieldErr("reason", "INVALID_VALUE", "a reason of 1 to 300 characters"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.FAAssets, db.ForNoKeyUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		q := s.q(ctx)
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		if cur.Status != statusActive {
			return apperr.Conflict("ASSET_STATUS_INVALID", "only an active asset is cancelled").WithContext("status", cur.Status)
		}
		if has, err := q.AssetHasRegisterEvents(ctx, assetsdb.AssetHasRegisterEventsParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id}); err != nil {
			return err
		} else if has {
			return apperr.Conflict("ASSET_HAS_DEPRECIATION", "the register has depreciated or changed this asset: it cannot be cancelled").WithContext("asset", cur.Number)
		}
		if err := s.cancelAsset(ctx, p, propertyID, id, day); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.cancelled", "fa_asset", id, cur.Number, map[string]any{"status": cur.Status},
			map[string]any{"status": statusCancelled, "reason": in.Reason, "cost": cur.Cost, "accum_depr": cur.AccumDepr}))
	})
	if err != nil {
		return AssetDetail{}, err
	}
	return s.assetDetail(ctx, p, propertyID, id)
}

// SetupCheck compares the register with itself and with the general ledger (any asset permission): the state of each asset against its ledger rows, the accounts of the categories, and the
// balance of the cost and accumulated accounts against what the register holds, with the difference by category (5.3). It is information, never a block.
func (s *Service) SetupCheck(ctx context.Context, propertyID int64) (SetupCheck, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return SetupCheck{}, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return SetupCheck{}, err
	}
	q := s.q(ctx)
	out := SetupCheck{Issues: []SetupIssue{}, Reconciliation: []Reconciliation{}, AsOf: day.BusinessDate}
	issue := func(code, sev, msg string, kv ...any) {
		i := SetupIssue{Code: code, Severity: sev, Message: msg}
		for k := 0; k+1 < len(kv); k += 2 {
			if i.Context == nil {
				i.Context = map[string]any{}
			}
			i.Context[fmt.Sprint(kv[k])] = kv[k+1]
		}
		out.Issues = append(out.Issues, i)
	}
	cfg, err := q.GetSettings(ctx, assetsdb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && cfg.RegisterFrom == nil) {
		issue("REGISTER_NOT_STARTED", "WARNING", "the register has no start month yet: set it in the settings or by the opening balances")
	} else if err != nil {
		return SetupCheck{}, err
	}
	cats, err := q.ListCategories(ctx, assetsdb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return SetupCheck{}, err
	}
	var ids []int64
	for _, c := range cats {
		if !c.IsActive {
			continue
		}
		ids = append(ids, c.CostAccountID)
		if c.AccumulatedAccountID != nil {
			ids = append(ids, *c.AccumulatedAccountID)
		}
		for _, acc := range []struct {
			id    *int64
			field string
		}{{ptr(c.CostAccountID), "cost"}, {c.AccumulatedAccountID, "accumulated"}, {c.ExpenseAccountID, "expense"}} {
			if acc.id == nil {
				continue
			}
			a, err := q.GetAccount(ctx, assetsdb.GetAccountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *acc.id})
			if err != nil {
				return SetupCheck{}, err
			}
			if !a.IsActive || !a.IsPostable {
				issue("CATEGORY_ACCOUNT_UNUSABLE", "ERROR", "the "+acc.field+" account of category "+c.Code+" is switched off or does not take postings", "category", c.Code, "account", a.Code)
			}
			if acc.field == "expense" && a.DepartmentRequirement == "REQUIRED" && c.DepartmentID == nil {
				issue("CATEGORY_NEEDS_DEPARTMENT", "WARNING", "the expense account of category "+c.Code+" requires a department and the category has none: the asset must carry one", "category", c.Code)
			}
		}
	}
	drift, err := q.LedgerDrift(ctx, assetsdb.LedgerDriftParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return SetupCheck{}, err
	}
	for _, d := range drift {
		issue("LEDGER_DRIFT", "ERROR", "the state of asset "+d.Number+" differs from its ledger rows: rebuild the state", "asset", d.Number, "state_cost", d.Cost.String(), "ledger_cost", d.LedgerCost.String(),
			"state_accum", d.AccumDepr.String(), "ledger_accum", d.LedgerAccum.String())
	}
	inactive, err := q.ListAssetsInInactiveLocations(ctx, assetsdb.ListAssetsInInactiveLocationsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return SetupCheck{}, err
	}
	for _, a := range inactive {
		issue("ASSET_IN_INACTIVE_LOCATION", "WARNING", "asset "+a.Number+" stands in a location that is switched off", "asset", a.Number, "location", a.LocationName)
	}
	// the books: the register of each category against the accounts of the general ledger
	if len(ids) > 0 {
		bal := map[int64]decimal.Decimal{}
		rows, err := q.AccountBalances(ctx, assetsdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountIds: ids, AsOf: day.BusinessDate})
		if err != nil {
			return SetupCheck{}, err
		}
		for _, r := range rows {
			bal[r.AccountID] = r.Balance
		}
		held, err := q.RegisterTotalsByCategory(ctx, assetsdb.RegisterTotalsByCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, AsOf: day.BusinessDate})
		if err != nil {
			return SetupCheck{}, err
		}
		reg := map[int64]assetsdb.RegisterTotalsByCategoryRow{}
		for _, h := range held {
			reg[h.CategoryID] = h
		}
		for _, c := range cats {
			if !c.IsActive {
				continue
			}
			r := Reconciliation{CategoryID: c.ID, CategoryCode: c.Code, CostAccountCode: c.CostAccountCode, AccumAccountCode: c.AccumulatedAccountCode,
				RegisterCost: reg[c.ID].Cost, LedgerCost: bal[c.CostAccountID], RegisterAccum: reg[c.ID].Accum}
			if c.AccumulatedAccountID != nil {
				r.LedgerAccum = bal[*c.AccumulatedAccountID].Neg()
			}
			r.CostDifference, r.AccumDifference = r.LedgerCost.Sub(r.RegisterCost), r.LedgerAccum.Sub(r.RegisterAccum)
			out.Reconciliation = append(out.Reconciliation, r)
			if !r.CostDifference.IsZero() || !r.AccumDifference.IsZero() {
				issue("GL_DIFFERENCE", "WARNING", "the general ledger and the register differ for category "+c.Code, "category", c.Code, "cost_difference", r.CostDifference.String(),
					"accum_difference", r.AccumDifference.String())
			}
		}
	}
	// a balance left on the old single accumulated account is outside the register
	if a, err := q.GetAccountByCode(ctx, assetsdb.GetAccountByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: "1590"}); err == nil && a.IsPostable {
		if rows, err := q.AccountBalances(ctx, assetsdb.AccountBalancesParams{TenantID: p.TenantID, PropertyID: propertyID, AccountIds: []int64{a.ID}, AsOf: day.BusinessDate}); err == nil && len(rows) == 1 && !rows[0].Balance.IsZero() {
			issue("OUTSIDE_REGISTER_1590", "WARNING", "account 1590 holds a balance that belongs to no category of the register", "balance", rows[0].Balance.Neg().String())
		}
	}
	return out, nil
}

// RebuildState sets the state of every active asset to the sum of its ledger rows (asset.setup, audited). It repairs a drift; it never invents a row of the ledger.
func (s *Service) RebuildState(ctx context.Context, propertyID int64) (RebuildResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetSetup)
	if err != nil {
		return RebuildResult{}, err
	}
	var res RebuildResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForUpdate); err != nil {
			return err
		}
		ids, err := s.q(ctx).ListActiveAssetsForRebuild(ctx, assetsdb.ListActiveAssetsForRebuildParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.FAAssets, db.ForNoKeyUpdate, propertyID, ids); err != nil {
			return err
		}
		for _, id := range ids {
			res.Checked++
			changed, err := s.restateAsset(ctx, p, propertyID, id)
			if err != nil {
				return err
			}
			if changed {
				res.Repaired++
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.state_rebuilt", "fa_settings", 0, "", nil, res))
	})
	return res, err
}
