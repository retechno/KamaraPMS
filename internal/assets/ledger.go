package assets

import (
	"context"

	"github.com/shopspring/decimal"

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

// acquireAsset makes an asset from a journal line, with its first row of the ledger (kind ACQUIRE): the cost of its unit. The journal that debited the account is the line's own (journalID); the
// register posts none. The row is keyed by the asset, so an asset made again after a cancellation is a new row.
func (s *Service) acquireAsset(ctx context.Context, p auth.Principal, propertyID int64, a assetsdb.LedgerInsertAssetParams, eventDate civil.Date, journalID int64) (int64, error) {
	q := s.q(ctx)
	a.TenantID, a.PropertyID, a.ActorID = p.TenantID, propertyID, p.ActorID()
	id, err := q.LedgerInsertAsset(ctx, a)
	if err != nil {
		return 0, err
	}
	if _, err := q.LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id, CategoryID: a.CategoryID, Kind: LedgerAcquire,
		EventDate: eventDate, CostDelta: a.Cost, AccumDelta: decimal.Zero, JournalID: &journalID, SourceType: sourceTypeLine, SourceID: id, ActorID: p.ActorID()}); err != nil {
		return 0, err
	}
	return id, nil
}

// setStart changes the start of the depreciation of an asset that has not been depreciated (the trigger fa_assets_start_locked refuses it otherwise). The caller has locked the asset.
func (s *Service) setStart(ctx context.Context, p auth.Principal, propertyID, assetID int64, inService civil.Date, rule string, firstDep *civil.Date) error {
	return s.q(ctx).LedgerUpdateStart(ctx, assetsdb.LedgerUpdateStartParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID, InServiceDate: inService, StartRule: rule,
		FirstDepMonth: firstDep, ActorID: p.ActorID()})
}

// depreciateAsset writes the line of a run into the ledger (kind DEPRECIATE, keyed by the line, dated the last day of the month and carrying the book month of the last month it depreciates) and
// restates the asset from its ledger. The caller has locked the asset.
func (s *Service) depreciateAsset(ctx context.Context, p auth.Principal, propertyID, assetID, categoryID, lineID int64, lastMonth civil.Date, amount decimal.Decimal, months int32, journalID int64) error {
	if _, err := s.q(ctx).LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID, CategoryID: categoryID, Kind: LedgerDepreciate,
		EventDate: MonthEnd(lastMonth), BookMonth: &lastMonth, CostDelta: decimal.Zero, AccumDelta: amount, MonthsDelta: months, JournalID: &journalID, SourceType: sourceTypeDepr, SourceID: lineID,
		ActorID: p.ActorID()}); err != nil {
		return err
	}
	_, err := s.restateAsset(ctx, p, propertyID, assetID)
	return err
}

// reverseDepreciation negates the ledger row that a line of a run wrote (a REVERSAL row dated the last day of its month, carrying the journal that reverses the run) and restates the asset: its
// accumulated depreciation, its months done and its last month go back to what they were. The caller has locked the asset.
func (s *Service) reverseDepreciation(ctx context.Context, p auth.Principal, propertyID, lineID, reversalJournalID int64) error {
	q := s.q(ctx)
	e, err := q.LedgerEntryOfRunLine(ctx, assetsdb.LedgerEntryOfRunLineParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: lineID})
	if err != nil {
		return err
	}
	if _, err := q.LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: e.AssetID, CategoryID: e.CategoryID, Kind: LedgerReversal,
		EventDate: e.EventDate, BookMonth: e.BookMonth, CostDelta: e.CostDelta.Neg(), AccumDelta: e.AccumDelta.Neg(), MonthsDelta: -e.MonthsDelta, JournalID: &reversalJournalID,
		SourceType: sourceTypeDepr, SourceID: lineID, ReversesID: &e.ID, ActorID: p.ActorID()}); err != nil {
		return err
	}
	_, err = s.restateAsset(ctx, p, propertyID, e.AssetID)
	return err
}

// moveAsset sets where an asset stands and which department it belongs to; the move itself (fa_moves) is written by the caller. The caller has locked the asset.
func (s *Service) moveAsset(ctx context.Context, p auth.Principal, propertyID, assetID int64, location, department *int64) error {
	return s.q(ctx).LedgerMoveAsset(ctx, assetsdb.LedgerMoveAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID, LocationID: location, DepartmentID: department, ActorID: p.ActorID()})
}

// reviseAsset sets the life, the residual value and the method of an asset (a revision, from the next run). The caller has locked the asset and checked the new values against its state.
func (s *Service) reviseAsset(ctx context.Context, p auth.Principal, propertyID, assetID int64, life *int32, residual decimal.Decimal, method *string, factor *decimal.Decimal) error {
	return s.q(ctx).LedgerReviseAsset(ctx, assetsdb.LedgerReviseAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID, LifeMonths: life, ResidualValue: residual, Method: method,
		DbFactor: factor, ActorID: p.ActorID()})
}

// capitalizeAsset adds value to an asset: a row of the ledger (kind CAPITALIZE, the journal of the line that debited the account) and the state restated from the ledger. The revision row keys it.
func (s *Service) capitalizeAsset(ctx context.Context, p auth.Principal, propertyID, assetID, categoryID, revisionID int64, day civil.Date, amount decimal.Decimal, journalID int64) error {
	if _, err := s.q(ctx).LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: assetID, CategoryID: categoryID, Kind: LedgerCapitalize,
		EventDate: day, CostDelta: amount, AccumDelta: decimal.Zero, JournalID: &journalID, SourceType: sourceTypeRevision, SourceID: revisionID, ActorID: p.ActorID()}); err != nil {
		return err
	}
	_, err := s.restateAsset(ctx, p, propertyID, assetID)
	return err
}

// reclassAsset moves an asset to another category: the cost and the accumulated depreciation leave the old category (RECLASS_OUT) and enter the new one (RECLASS_IN), so the totals of the asset
// are what they were and each category's recap sees the change. The attributes the new category brings (life, method, residual value, fiscal group, and for a finished construction the start of
// the depreciation) are set with it. journalID is the journal that moved the accounts, or nil when both categories use the same accounts. The caller has locked the asset.
func (s *Service) reclassAsset(ctx context.Context, p auth.Principal, propertyID int64, cur assetsdb.GetAssetRow, revisionID int64, day civil.Date, journalID *int64, to assetsdb.LedgerReclassAssetParams) error {
	q := s.q(ctx)
	for _, e := range []struct {
		kind     string
		category int64
		sign     int
	}{{LedgerReclassOut, cur.CategoryID, -1}, {LedgerReclassIn, to.CategoryID, 1}} {
		cost, accum := cur.Cost, cur.AccumDepr
		if e.sign < 0 {
			cost, accum = cost.Neg(), accum.Neg()
		}
		if _, err := q.LedgerInsertEntry(ctx, assetsdb.LedgerInsertEntryParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: cur.ID, CategoryID: e.category, Kind: e.kind, EventDate: day,
			CostDelta: cost, AccumDelta: accum, JournalID: journalID, SourceType: sourceTypeRevision, SourceID: revisionID, ActorID: p.ActorID()}); err != nil {
			return err
		}
	}
	to.TenantID, to.PropertyID, to.ID, to.ActorID = p.TenantID, propertyID, cur.ID, p.ActorID()
	if err := q.LedgerReclassAsset(ctx, to); err != nil {
		return err
	}
	_, err := s.restateAsset(ctx, p, propertyID, cur.ID)
	return err
}
