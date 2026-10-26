package assets

import (
	"context"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The register ledger and the state of an asset (docs/architecture/37-fixed-assets.md 3.2). THIS FILE IS THE ONLY WRITER of fa_ledger and of the state columns of fa_assets (cost, unit_count,
// accum_depr, dep_months_done, last_dep_month, status, disposed_on): ledgerpath_guard_test.go fails any other caller of the Ledger* queries and any other SQL that writes them. A change of the
// state is always a row of the ledger first; the state is the sum of the rows, and the drift check (setup-check) and RebuildState prove it.

// openAsset makes an asset with its first row of the ledger (kind OPEN): the cost, the accumulated depreciation and the months done it brings. The ledger row carries the book month of the
// last month depreciated, so the state can be rebuilt from the ledger alone.
func (s *Service) openAsset(ctx context.Context, p auth.Principal, propertyID int64, a assetsdb.LedgerInsertAssetParams, eventDate civil.Date, importID int64, line int32, journalID *int64) (int64, error) {
	q := s.q(ctx)
	a.TenantID, a.PropertyID, a.ActorID = p.TenantID, propertyID, p.ActorID()
	id, err := q.LedgerInsertAsset(ctx, a)
	if err != nil {
		return 0, err
	}
	var book *civil.Date
	if a.DepMonthsDone > 0 {
		book = ptr(MonthStart(eventDate))
	}
	if _, err := q.LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id, CategoryID: a.CategoryID, Kind: LedgerOpen,
		EventDate: eventDate, BookMonth: book, CostDelta: a.Cost, AccumDelta: a.AccumDepr, MonthsDelta: a.DepMonthsDone, JournalID: journalID, SourceType: sourceTypeOpening, SourceID: importID,
		SourceLine: line, ActorID: p.ActorID()}); err != nil {
		return 0, err
	}
	return id, nil
}

// cancelAsset takes an asset out of the register before the register has depreciated it: a row of the ledger that negates its first one, and the status. The caller has locked the asset.
func (s *Service) cancelAsset(ctx context.Context, p auth.Principal, propertyID int64, assetID int64, day civil.Date) error {
	q := s.q(ctx)
	first, err := q.LedgerOpenEntry(ctx, assetsdb.LedgerOpenEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return err
	}
	if _, err := q.LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID, CategoryID: first.CategoryID, Kind: LedgerReversal,
		EventDate: day, BookMonth: first.BookMonth, CostDelta: first.CostDelta.Neg(), AccumDelta: first.AccumDelta.Neg(), MonthsDelta: -first.MonthsDelta, JournalID: nil, SourceType: sourceTypeCancel,
		SourceID: assetID, SourceLine: 0, ReversesID: &first.ID, ActorID: p.ActorID()}); err != nil {
		return err
	}
	return q.LedgerSetCancelled(ctx, assetsdb.LedgerSetCancelledParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID, ActorID: p.ActorID()})
}

// restateAsset sets the state of an active asset to the sum of its ledger rows and answers whether it changed anything. The caller has locked the asset.
func (s *Service) restateAsset(ctx context.Context, p auth.Principal, propertyID, assetID int64) (bool, error) {
	q := s.q(ctx)
	cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID})
	if err != nil {
		return false, err
	}
	sum, err := q.LedgerSumsOfAsset(ctx, assetsdb.LedgerSumsOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID})
	if err != nil {
		return false, err
	}
	if !sum.Cost.IsPositive() {
		return false, apperr.Conflict("LEDGER_INCONSISTENT", "the ledger of the asset does not add up to a cost: it cannot be rebuilt").WithContext("asset", cur.Number)
	}
	if cur.Cost.Equal(sum.Cost) && cur.AccumDepr.Equal(sum.Accum) && cur.DepMonthsDone == sum.Months {
		return false, nil
	}
	var last *civil.Date // 1900-01-01 is the answer of the query for "no month"
	if sum.Months > 0 && sum.LastDepMonth.Year() > 1900 {
		last = ptr(sum.LastDepMonth)
	}
	return true, q.LedgerRestateAsset(ctx, assetsdb.LedgerRestateAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID, Cost: sum.Cost, AccumDepr: sum.Accum, DepMonthsDone: sum.Months,
		LastDepMonth: last, ActorID: p.ActorID()})
}
