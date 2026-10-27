package assets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/accounting"
	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Moves and revisions of an asset (docs/architecture/37-fixed-assets.md 4.5 and 4.6). A move changes the location or the department from the open business day on and is never backdated; a
// revision changes the life, the residual value or the method from the next run, adds value from a journal line that debited the account already, or moves the asset to another category with
// the journal that moves its cost. Both are rows that are kept (fa_moves, fa_revisions) and neither rewrites anything posted. The state of the asset is written through ledger.go only.

// Kinds of revision (fa_revisions.kind).
const (
	RevLife       = "LIFE"
	RevResidual   = "RESIDUAL"
	RevMethod     = "METHOD"
	RevCapitalize = "CAPITALIZE"
	RevCategory   = "CATEGORY"
)

const (
	maxMoveAssets  = 500
	maxReason      = 300
	moveReasonEdit = "Changed on the asset form"
)

// ---------------------------------------------------------------------------------------------------------------------
// The department of an asset at a date

// moveRef is a move as the department of a month needs it.
type moveRef struct {
	EffectiveOn civil.Date
	From, To    *int64
}

// departmentAt is the department an asset has at the end of a day (4.6): the department it was moved to by the last move on or before the day; when it was moved only later, the one it was moved
// from by its first move; when it was never moved, its own. The moves are in the order they were made. A department that is none (nil) is an answer too: the category's takes its place.
func departmentAt(own *int64, moves []moveRef, day civil.Date) *int64 {
	if len(moves) == 0 {
		return own
	}
	var last *moveRef
	for i := range moves {
		if !moves[i].EffectiveOn.After(day) {
			last = &moves[i]
		}
	}
	if last != nil {
		return last.To
	}
	return moves[0].From
}

// movesByAsset reads the moves of the assets of a run, in the order they were made.
func (s *Service) movesByAsset(ctx context.Context, tenantID, propertyID int64, ids []int64) (map[int64][]moveRef, error) {
	out := map[int64][]moveRef{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).ListMovesOfAssets(ctx, assetsdb.ListMovesOfAssetsParams{TenantID: tenantID, PropertyID: propertyID, AssetIds: ids})
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.AssetID] = append(out[r.AssetID], moveRef{EffectiveOn: r.EffectiveOn, From: r.FromDepartmentID, To: r.ToDepartmentID})
	}
	return out, nil
}

// lockAssets takes the assets FOR NO KEY UPDATE in ascending order (level 47, after the settings); an id that is not an asset of the property is ASSET_NOT_FOUND.
func lockAssets(ctx context.Context, propertyID int64, ids []int64) error {
	err := db.LockRows(ctx, db.FAAssets, db.ForNoKeyUpdate, propertyID, ids)
	if ae, ok := apperr.As(err); ok && ae.Code == "NOT_FOUND" {
		return notFound("ASSET_NOT_FOUND", "asset")
	}
	return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
}

func sameID(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ---------------------------------------------------------------------------------------------------------------------
// Moves

// MoveInput moves one asset or several. The location and the department each change only when the flag says so (a department of none is a change too).
type MoveInput struct {
	AssetIDs         []int64 `json:"asset_ids"`
	ChangeLocation   bool    `json:"change_location"`
	LocationID       *int64  `json:"location_id"`
	ChangeDepartment bool    `json:"change_department"`
	DepartmentID     *int64  `json:"department_id"`
	Reason           string  `json:"reason"`
	IdempotencyKey   string  `json:"-"`
}

// MovedAsset is an asset a move changed.
type MovedAsset struct {
	ID     int64  `json:"id"`
	Number string `json:"number"`
	Name   string `json:"name"`
}

// MoveResult is the outcome of a move of many assets: those that moved, and how many were there already.
type MoveResult struct {
	Moved    []MovedAsset `json:"moved"`
	Skipped  int          `json:"skipped"`
	Replayed bool         `json:"replayed"`
}

func idempotencyKey(raw string) (string, error) {
	key := trimmed(raw)
	if key == "" {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_REQUIRED", "the Idempotency-Key header is required")
	}
	if len(key) > 64 {
		return "", apperr.BadRequest("IDEMPOTENCY_KEY_INVALID", "the Idempotency-Key is at most 64 characters")
	}
	return key, nil
}

func reasonOf(raw string, what string) (string, error) {
	r := trimmed(raw)
	if n := len([]rune(r)); n < 1 || n > maxReason {
		return "", invalid(what, fieldErr("reason", "INVALID_VALUE", fmt.Sprintf("a reason of 1 to %d characters", maxReason)))
	}
	return r, nil
}

// MoveAsset moves one asset (asset.manage, an Idempotency-Key) and answers it as it stands.
func (s *Service) MoveAsset(ctx context.Context, propertyID, id int64, in MoveInput) (AssetDetail, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return AssetDetail{}, err
	}
	in.AssetIDs = []int64{id}
	res, err := s.MoveAssets(ctx, propertyID, in)
	if err != nil {
		return AssetDetail{}, err
	}
	_ = res
	return s.assetDetail(ctx, p, propertyID, id)
}

// MoveAssets moves assets to another location, another department or both, from the open business day, all or nothing (asset.manage, an Idempotency-Key). An asset that is where it is asked to be
// is left alone and counted; when none moves the request is refused.
func (s *Service) MoveAssets(ctx context.Context, propertyID int64, in MoveInput) (MoveResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return MoveResult{}, err
	}
	key, err := idempotencyKey(in.IdempotencyKey)
	if err != nil {
		return MoveResult{}, err
	}
	reason, err := reasonOf(in.Reason, "move")
	if err != nil {
		return MoveResult{}, err
	}
	ids := uniqueSorted(in.AssetIDs)
	var f []apperr.FieldError
	if len(ids) == 0 || len(ids) > maxMoveAssets || ids[0] < 1 {
		f = append(f, fieldErr("asset_ids", "INVALID_VALUE", fmt.Sprintf("1 to %d assets", maxMoveAssets)))
	}
	if !in.ChangeLocation && !in.ChangeDepartment {
		f = append(f, fieldErr("change_location", "REQUIRED", "a move changes the location, the department or both"))
	}
	if len(f) > 0 {
		return MoveResult{}, invalid("move", f...)
	}
	var res MoveResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		res = MoveResult{Moved: []MovedAsset{}}
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		if err := lockAssets(ctx, propertyID, ids); err != nil {
			return err
		}
		q := s.q(ctx)
		if prev, err := q.ListMovesByKey(ctx, assetsdb.ListMovesByKeyParams{PropertyID: propertyID, IdempotencyKey: &key}); err != nil {
			return err
		} else if len(prev) > 0 {
			res.Replayed = true
			for _, id := range prev {
				if a, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err == nil {
					res.Moved = append(res.Moved, MovedAsset{ID: a.ID, Number: a.Number, Name: a.Name})
				}
			}
			return nil
		}
		if in.ChangeLocation && in.LocationID != nil {
			loc, err := q.GetLocation(ctx, assetsdb.GetLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.LocationID})
			if err != nil {
				return orNotFound(err, invalid("move", fieldErr("location_id", "NOT_FOUND", "no such location in this property")))
			}
			if !loc.IsActive {
				return invalid("move", fieldErr("location_id", "INACTIVE", "the location is switched off"))
			}
		}
		if in.ChangeDepartment && in.DepartmentID != nil {
			if err := s.depts.Check(ctx, p.TenantID, propertyID, *in.DepartmentID, "department_id"); err != nil {
				return err
			}
		}
		for _, id := range ids {
			cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
			if err != nil {
				return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
			}
			if cur.Status != statusActive {
				return apperr.Conflict("ASSET_STATUS_INVALID", "only an active asset is moved").WithContext("asset", cur.Number).WithContext("status", cur.Status)
			}
			if cur.DisposalPending {
				return disposalPending(cur.Number)
			}
			loc, dept := cur.LocationID, cur.DepartmentID
			if in.ChangeLocation {
				loc = in.LocationID
			}
			if in.ChangeDepartment {
				dept = in.DepartmentID
			}
			if sameID(loc, cur.LocationID) && sameID(dept, cur.DepartmentID) {
				res.Skipped++
				continue
			}
			if err := s.applyMove(ctx, p, propertyID, day, cur, loc, dept, reason, &key); err != nil {
				return err
			}
			res.Moved = append(res.Moved, MovedAsset{ID: cur.ID, Number: cur.Number, Name: cur.Name})
		}
		if len(res.Moved) == 0 {
			return invalid("move", fieldErr("asset_ids", "NOTHING_TO_MOVE", "the assets are where you say already"))
		}
		return nil
	})
	return res, err
}

// applyMove writes one move and the new place of the asset, with its audit entry. The caller has locked the asset and the open business day.
func (s *Service) applyMove(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, cur assetsdb.GetAssetRow, loc, dept *int64, reason string, key *string) error {
	if _, err := s.q(ctx).InsertMove(ctx, assetsdb.InsertMoveParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: cur.ID, EffectiveOn: day, FromLocationID: cur.LocationID, ToLocationID: loc,
		FromDepartmentID: cur.DepartmentID, ToDepartmentID: dept, Reason: reason, IdempotencyKey: key, ActorID: p.ActorID()}); err != nil {
		return err
	}
	if err := s.moveAsset(ctx, p, propertyID, cur.ID, loc, dept); err != nil {
		return err
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.moved", "fa_asset", cur.ID, cur.Number,
		map[string]any{"location_id": cur.LocationID, "department_id": cur.DepartmentID},
		map[string]any{"location_id": loc, "department_id": dept, "effective_on": day, "reason": reason}))
}

func uniqueSorted(in []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(in))
	for _, v := range in {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ---------------------------------------------------------------------------------------------------------------------
// Revisions

// RevisionInput is one change to an asset: what the kind needs is filled, the rest is left out. LIFE takes the new total life, RESIDUAL the new residual value, METHOD the method (and the factor
// of a declining balance), CAPITALIZE the journal line that adds the value, CATEGORY the new category and, to change what it brings, the life, residual value, method and start.
type RevisionInput struct {
	Kind           string           `json:"kind"`
	Reason         string           `json:"reason"`
	LifeMonths     *int32           `json:"life_months"`
	ResidualValue  *decimal.Decimal `json:"residual_value"`
	Method         *string          `json:"method"`
	DBFactor       *decimal.Decimal `json:"db_factor"`
	JournalLineID  *int64           `json:"journal_line_id"`
	CategoryID     *int64           `json:"category_id"`
	InServiceDate  *civil.Date      `json:"in_service_date"`
	StartRule      *string          `json:"start_rule"`
	IdempotencyKey string           `json:"-"`
}

// RevisionResult is the asset after the revision, with the journal of a change of category.
type RevisionResult struct {
	AssetDetail
	RevisionID    int64   `json:"revision_id"`
	JournalID     *int64  `json:"journal_id"`
	JournalNumber *string `json:"journal_number"`
	Replayed      bool    `json:"replayed"`
}

// revision is what one kind of revision decided: the values before and after, the journal and the line it came from.
type revision struct {
	old, new  map[string]any
	journalID *int64
	lineID    *int64
	billID    *int64
	billLine  *int32
}

func stateOf(a assetsdb.GetAssetRow, decimals int32) DeprState {
	st := DeprState{Cost: a.Cost, Residual: a.ResidualValue, Accum: a.AccumDepr, MonthsDone: int(a.DepMonthsDone), Decimals: decimals, Factor: decimal.NewFromInt(2), Method: MethodSL}
	if a.LifeMonths != nil {
		st.LifeMonths = int(*a.LifeMonths)
	}
	if a.Method != nil {
		st.Method = *a.Method
	}
	if a.DbFactor != nil {
		st.Factor = *a.DbFactor
	}
	return st
}

// ReviseAsset changes an asset prospectively (asset.manage, an Idempotency-Key, a reason). The months before the open day's month must have been run: a revision never changes a month that is due.
func (s *Service) ReviseAsset(ctx context.Context, propertyID, id int64, in RevisionInput) (RevisionResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return RevisionResult{}, err
	}
	key, err := idempotencyKey(in.IdempotencyKey)
	if err != nil {
		return RevisionResult{}, err
	}
	if in.Reason, err = reasonOf(in.Reason, "revision"); err != nil {
		return RevisionResult{}, err
	}
	switch in.Kind {
	case RevLife, RevResidual, RevMethod, RevCapitalize, RevCategory:
	default:
		return RevisionResult{}, invalid("revision", fieldErr("kind", "INVALID_VALUE", "LIFE, RESIDUAL, METHOD, CAPITALIZE or CATEGORY"))
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return RevisionResult{}, err
	}
	var res RevisionResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		res = RevisionResult{}
		q := s.q(ctx)
		var po *accounting.Poster
		var line assetsdb.GetJournalLineForAssetRow
		if in.Kind == RevCapitalize || in.Kind == RevCategory {
			if po, err = s.begin(ctx, propertyID); err != nil { // the accounting settings (46) first
				return err
			}
			if po == nil {
				return apperr.NotFound("ACCOUNTING_NOT_SET_UP", "accounting is not set up for this property")
			}
		}
		if in.Kind == RevCapitalize {
			if in.JournalLineID == nil || *in.JournalLineID < 1 {
				return invalid("revision", fieldErr("journal_line_id", "REQUIRED", "the journal line that adds the value"))
			}
			line, err = q.GetJournalLineForAsset(ctx, assetsdb.GetJournalLineForAssetParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: *in.JournalLineID})
			if err != nil {
				return orNotFound(err, notFound("JOURNAL_LINE_NOT_FOUND", "journal line"))
			}
			if line.BillSupplierID != nil { // the supplier of the bill (47, share), so a void of the bill waits for us or sees our link
				if err := db.LockRows(ctx, db.Suppliers, db.ForShare, propertyID, []int64{*line.BillSupplierID}); err != nil {
					return err
				}
			}
		}
		mode := db.ForShare
		if in.Kind == RevCapitalize {
			mode = db.ForUpdate // serialises against the reversal guard of the bill and of the journal, as making an asset does
		}
		cfg, err := s.lockSettings(ctx, p, propertyID, mode)
		if err != nil {
			return err
		}
		if err := lockAssets(ctx, propertyID, []int64{id}); err != nil {
			return err
		}
		if prev, err := q.GetRevisionByKey(ctx, assetsdb.GetRevisionByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			if prev.AssetID != id {
				return apperr.Conflict("DUPLICATE_REQUEST", "the Idempotency-Key was used for another asset")
			}
			d, err := s.assetDetail(ctx, p, propertyID, id)
			if err != nil {
				return err
			}
			res = RevisionResult{AssetDetail: d, RevisionID: prev.ID, JournalID: prev.JournalID, Replayed: true}
			return s.fillJournalNumber(ctx, p, propertyID, &res)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		if cur.Status != statusActive {
			return apperr.Conflict("ASSET_STATUS_INVALID", "only an active asset is changed").WithContext("status", cur.Status)
		}
		if cur.DisposalPending {
			return disposalPending(cur.Number)
		}
		if err := requireRunThrough(cur, cfg, day, prop.CurrencyDecimals); err != nil {
			return err
		}
		var rev revision
		var apply applyFunc
		switch in.Kind {
		case RevLife:
			rev, apply, err = s.planLife(ctx, p, propertyID, cur, in)
		case RevResidual:
			rev, apply, err = s.planResidual(ctx, p, propertyID, cur, in, prop.CurrencyDecimals)
		case RevMethod:
			rev, apply, err = s.planMethod(ctx, p, propertyID, cur, in)
		case RevCapitalize:
			rev, apply, err = s.planCapitalize(ctx, p, propertyID, day, cur, cfg, in, key)
		default:
			rev, apply, err = s.planCategory(ctx, p, propertyID, day, cur, cfg, po, in, prop.CurrencyDecimals)
		}
		if err != nil {
			return err
		}
		oldJSON, err := json.Marshal(rev.old)
		if err != nil {
			return err
		}
		newJSON, err := json.Marshal(rev.new)
		if err != nil {
			return err
		}
		revID, err := q.InsertRevision(ctx, assetsdb.InsertRevisionParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id, Kind: in.Kind, EffectiveOn: day, OldValue: oldJSON, NewValue: newJSON,
			BillID: rev.billID, BillLineNo: rev.billLine, JournalLineID: rev.lineID, JournalID: rev.journalID, Reason: in.Reason, IdempotencyKey: &key, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := apply(revID); err != nil {
			return err
		}
		if err := s.auditRevision(ctx, p, propertyID, day, cur, in, rev); err != nil {
			return err
		}
		d, err := s.assetDetail(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		res = RevisionResult{AssetDetail: d, RevisionID: revID, JournalID: rev.journalID}
		return s.fillJournalNumber(ctx, p, propertyID, &res)
	})
	return res, err
}

func (s *Service) fillJournalNumber(ctx context.Context, p auth.Principal, propertyID int64, res *RevisionResult) error {
	if res.JournalID == nil {
		return nil
	}
	revs, err := s.q(ctx).ListRevisionsOfAsset(ctx, assetsdb.ListRevisionsOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: res.ID})
	if err != nil {
		return err
	}
	for _, r := range revs {
		if r.ID == res.RevisionID {
			res.JournalNumber = r.JournalNumber
		}
	}
	return nil
}

func (s *Service) auditRevision(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, cur assetsdb.GetAssetRow, in RevisionInput, rev revision) error {
	nw := map[string]any{"effective_on": day, "reason": in.Reason}
	for k, v := range rev.new {
		nw[k] = v
	}
	switch in.Kind {
	case RevLife:
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.life_revised", "fa_asset", cur.ID, cur.Number, rev.old, nw))
	case RevResidual:
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.residual_revised", "fa_asset", cur.ID, cur.Number, rev.old, nw))
	case RevMethod:
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.method_revised", "fa_asset", cur.ID, cur.Number, rev.old, nw))
	case RevCapitalize:
		return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.value_added", "fa_asset", cur.ID, cur.Number, rev.old, nw))
	}
	return s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.category_changed", "fa_asset", cur.ID, cur.Number, rev.old, nw))
}

// requireRunThrough refuses a change to an asset that is due in a month before the open day's month: those months are run first, so a change never reaches back into a month that could still be run.
func requireRunThrough(cur assetsdb.GetAssetRow, cfg assetsdb.FaSetting, day civil.Date, decimals int32) error {
	if cfg.RegisterFrom == nil || cur.LifeMonths == nil || cur.Method == nil {
		return nil
	}
	if !stateOf(cur, decimals).Due() {
		return nil
	}
	from := firstDue(int(cur.DepMonthsDone), cur.LastDepMonth, cur.FirstDepMonth, *cfg.RegisterFrom)
	if from.Before(MonthStart(day)) {
		return apperr.Conflict("DEPRECIATION_NOT_RUN", "the months before this one must be run before the asset is changed").WithContext("asset", cur.Number).WithContext("month", monthLabel(from))
	}
	return nil
}

func notDepreciable() *apperr.Error {
	return invalid("revision", fieldErr("kind", "NOT_DEPRECIABLE", "the asset is not depreciated"))
}

func lifeTooShort(cur assetsdb.GetAssetRow) *apperr.Error {
	return apperr.Conflict("LIFE_TOO_SHORT", "the life would leave no month to depreciate what is left").WithContext("asset", cur.Number).WithContext("months_done", cur.DepMonthsDone)
}

// checkLife says whether a total life leaves the months an asset has done, and a month to depreciate what is left above the residual value.
func checkLife(cur assetsdb.GetAssetRow, life int32, residual decimal.Decimal) error {
	if life < cur.DepMonthsDone || (life == cur.DepMonthsDone && cur.Cost.Sub(cur.AccumDepr).GreaterThan(residual)) {
		return lifeTooShort(cur)
	}
	return nil
}

type applyFunc func(revisionID int64) error

func (s *Service) planLife(ctx context.Context, p auth.Principal, propertyID int64, cur assetsdb.GetAssetRow, in RevisionInput) (revision, applyFunc, error) {
	if cur.LifeMonths == nil || cur.Method == nil {
		return revision{}, nil, notDepreciable()
	}
	if in.LifeMonths == nil || *in.LifeMonths < 1 || *in.LifeMonths > maxLifeMonths {
		return revision{}, nil, invalid("revision", fieldErr("life_months", "INVALID_VALUE", fmt.Sprintf("1 to %d months", maxLifeMonths)))
	}
	if *in.LifeMonths == *cur.LifeMonths {
		return revision{}, nil, invalid("revision", fieldErr("life_months", "UNCHANGED", "the life is the one the asset has"))
	}
	if err := checkLife(cur, *in.LifeMonths, cur.ResidualValue); err != nil {
		return revision{}, nil, err
	}
	rev := revision{old: map[string]any{"life_months": *cur.LifeMonths}, new: map[string]any{"life_months": *in.LifeMonths}}
	return rev, func(int64) error {
		return s.reviseAsset(ctx, p, propertyID, cur.ID, in.LifeMonths, cur.ResidualValue, cur.Method, cur.DbFactor)
	}, nil
}

func (s *Service) planResidual(ctx context.Context, p auth.Principal, propertyID int64, cur assetsdb.GetAssetRow, in RevisionInput, decimals int32) (revision, applyFunc, error) {
	if cur.LifeMonths == nil || cur.Method == nil {
		return revision{}, nil, notDepreciable()
	}
	if in.ResidualValue == nil || !moneyOK(*in.ResidualValue, decimals) {
		return revision{}, nil, invalid("revision", fieldErr("residual_value", "INVALID_VALUE", "an amount that is not negative, in the decimals of the currency"))
	}
	if in.ResidualValue.Equal(cur.ResidualValue) {
		return revision{}, nil, invalid("revision", fieldErr("residual_value", "UNCHANGED", "the residual value is the one the asset has"))
	}
	if in.ResidualValue.GreaterThan(cur.Cost.Sub(cur.AccumDepr)) {
		return revision{}, nil, invalid("revision", fieldErr("residual_value", "ABOVE_CARRYING_VALUE", "the residual value cannot be above the carrying value"))
	}
	rev := revision{old: map[string]any{"residual_value": cur.ResidualValue}, new: map[string]any{"residual_value": *in.ResidualValue}}
	return rev, func(int64) error {
		return s.reviseAsset(ctx, p, propertyID, cur.ID, cur.LifeMonths, *in.ResidualValue, cur.Method, cur.DbFactor)
	}, nil
}

// methodAndFactor reads a method and, for a declining balance, its factor (the one given, else the one the asset or the category had, else 2).
func methodAndFactor(method *string, factor, fallback *decimal.Decimal) (*string, *decimal.Decimal, error) {
	if method == nil || (*method != MethodSL && *method != MethodDB) {
		return nil, nil, invalid("revision", fieldErr("method", "INVALID_VALUE", "SL or DB"))
	}
	if *method == MethodSL {
		return method, nil, nil
	}
	f := decimal.NewFromInt(2)
	switch {
	case factor != nil:
		f = *factor
	case fallback != nil:
		f = *fallback
	}
	if !f.IsPositive() || f.GreaterThan(decimal.NewFromInt(5)) {
		return nil, nil, invalid("revision", fieldErr("db_factor", "INVALID_VALUE", "above 0 and not above 5"))
	}
	return method, &f, nil
}

func (s *Service) planMethod(ctx context.Context, p auth.Principal, propertyID int64, cur assetsdb.GetAssetRow, in RevisionInput) (revision, applyFunc, error) {
	if cur.LifeMonths == nil || cur.Method == nil {
		return revision{}, nil, notDepreciable()
	}
	method, factor, err := methodAndFactor(in.Method, in.DBFactor, cur.DbFactor)
	if err != nil {
		return revision{}, nil, err
	}
	if *method == *cur.Method && ((factor == nil && cur.DbFactor == nil) || (factor != nil && cur.DbFactor != nil && factor.Equal(*cur.DbFactor))) {
		return revision{}, nil, invalid("revision", fieldErr("method", "UNCHANGED", "the method is the one the asset has"))
	}
	rev := revision{old: map[string]any{"method": *cur.Method, "db_factor": cur.DbFactor}, new: map[string]any{"method": *method, "db_factor": factor}}
	return rev, func(int64) error {
		return s.reviseAsset(ctx, p, propertyID, cur.ID, cur.LifeMonths, cur.ResidualValue, method, factor)
	}, nil
}

// planCapitalize adds the debit of a journal line to the cost of the asset: the line is of the account of the asset's category, is not an asset or dismissed, and the bill that made it already
// debited the account, so no journal is posted (4.5).
func (s *Service) planCapitalize(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, cur assetsdb.GetAssetRow, cfg assetsdb.FaSetting, in RevisionInput, key string) (revision, applyFunc, error) {
	q := s.q(ctx)
	// READ COMMITTED: a void or a reversal that committed while we waited for the locks is not in the row read before them, so the line is read again now that nothing can change it under us
	line, err := q.GetJournalLineForAsset(ctx, assetsdb.GetJournalLineForAssetParams{TenantID: p.TenantID, PropertyID: propertyID, LineID: *in.JournalLineID})
	if err != nil {
		return revision{}, nil, orNotFound(err, notFound("JOURNAL_LINE_NOT_FOUND", "journal line"))
	}
	cat, err := s.lineCategory(ctx, p, propertyID, line, cfg)
	if err != nil {
		return revision{}, nil, err
	}
	if cat.ID != cur.CategoryID {
		return revision{}, nil, invalid("revision", fieldErr("journal_line_id", "CATEGORY_MISMATCH", "the line is of the account of another category than the asset's"))
	}
	if n, err := q.CountLiveLinksOfLine(ctx, assetsdb.CountLiveLinksOfLineParams{PropertyID: propertyID, LineID: line.LineID}); err != nil {
		return revision{}, nil, err
	} else if n > 0 {
		return revision{}, nil, apperr.Conflict("LINE_ALREADY_ASSET", "the journal line is an asset already").WithContext("journal", line.JournalNumber).WithContext("line", line.LineNo)
	}
	if _, err := q.GetLiveDismissal(ctx, assetsdb.GetLiveDismissalParams{PropertyID: propertyID, LineID: line.LineID}); err == nil {
		return revision{}, nil, apperr.Conflict("LINE_DISMISSED", "the line is marked as not an asset: restore it first").WithContext("journal", line.JournalNumber).WithContext("line", line.LineNo)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return revision{}, nil, err
	}
	rev := revision{old: map[string]any{"cost": cur.Cost}, new: map[string]any{"cost": cur.Cost.Add(line.Debit), "added": line.Debit, "journal": line.JournalNumber, "journal_line": line.LineNo},
		journalID: &line.JournalID, lineID: &line.LineID}
	if line.BillID != nil && line.SourceLine != nil {
		rev.billID, rev.billLine = line.BillID, line.SourceLine
	}
	return rev, func(revisionID int64) error {
		if err := q.InsertSourceLink(ctx, assetsdb.InsertSourceLinkParams{TenantID: p.TenantID, PropertyID: propertyID, JournalLineID: line.LineID, UnitNo: 1, AssetID: cur.ID, Cost: line.Debit, Kind: linkCapitalize,
			BillID: rev.billID, BillLineNo: rev.billLine, IdempotencyKey: &key, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.capitalizeAsset(ctx, p, propertyID, cur.ID, cur.CategoryID, revisionID, day, line.Debit, line.JournalID)
	}, nil
}

// planCategory moves an asset to another category (4.5): the journal moves the cost, and the accumulated depreciation of a depreciated asset, from the accounts of the old category to those of the
// new one. A construction in progress that is finished becomes depreciable here: it gets the life, method and residual value of the new category (or the ones given) and its start.
func (s *Service) planCategory(ctx context.Context, p auth.Principal, propertyID int64, day civil.Date, cur assetsdb.GetAssetRow, cfg assetsdb.FaSetting, po *accounting.Poster, in RevisionInput,
	decimals int32) (revision, applyFunc, error) {
	q := s.q(ctx)
	if in.CategoryID == nil || *in.CategoryID < 1 {
		return revision{}, nil, invalid("revision", fieldErr("category_id", "REQUIRED", "the category the asset moves to"))
	}
	if *in.CategoryID == cur.CategoryID {
		return revision{}, nil, invalid("revision", fieldErr("category_id", "UNCHANGED", "the asset is of this category already"))
	}
	to, err := q.GetCategory(ctx, assetsdb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.CategoryID})
	if err != nil {
		return revision{}, nil, orNotFound(err, invalid("revision", fieldErr("category_id", "NOT_FOUND", "no such category in this property")))
	}
	if !to.IsActive {
		return revision{}, nil, invalid("revision", fieldErr("category_id", "INACTIVE", "the category is switched off"))
	}
	from, err := q.GetCategory(ctx, assetsdb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: cur.CategoryID})
	if err != nil {
		return revision{}, nil, err
	}
	if n, err := q.CountPostedDisposals(ctx, assetsdb.CountPostedDisposalsParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: cur.ID}); err != nil {
		return revision{}, nil, err
	} else if n > 0 {
		return revision{}, nil, apperr.Conflict("ASSET_HAS_DISPOSAL", "an asset that part of which was disposed of keeps its category").WithContext("asset", cur.Number)
	}
	if cur.UnitCount > 1 && to.Kind != KindOE {
		return revision{}, nil, invalid("revision", fieldErr("category_id", "NOT_ALLOWED", "a batch of operating equipment stays in a category of operating equipment"))
	}
	depreciable := to.LifeMonths != nil && to.Method != nil && to.AccumulatedAccountID != nil
	if !depreciable && (cur.AccumDepr.IsPositive() || cur.DepMonthsDone > 0) {
		return revision{}, nil, invalid("revision", fieldErr("category_id", "NOT_DEPRECIABLE", "the asset has been depreciated: it cannot go to a category that is not"))
	}
	arg := assetsdb.LedgerReclassAssetParams{CategoryID: to.ID, ResidualValue: decimal.Zero, InServiceDate: cur.InServiceDate, StartRule: cur.StartRule, FirstDepMonth: cur.FirstDepMonth,
		FiscalGroup: to.FiscalGroup, FiscalMethod: to.FiscalMethod}
	newState := map[string]any{"category_id": to.ID, "category": to.Code}
	if depreciable {
		life := *to.LifeMonths
		if in.LifeMonths != nil {
			life = *in.LifeMonths
		}
		if life < 1 || life > maxLifeMonths {
			return revision{}, nil, invalid("revision", fieldErr("life_months", "INVALID_VALUE", fmt.Sprintf("1 to %d months", maxLifeMonths)))
		}
		methodIn := to.Method
		if in.Method != nil {
			methodIn = in.Method
		}
		method, factor, err := methodAndFactor(methodIn, in.DBFactor, to.DbFactor)
		if err != nil {
			return revision{}, nil, err
		}
		residual := cur.Cost.Mul(to.ResidualPct).Div(decHundred).Round(decimals)
		if in.ResidualValue != nil {
			residual = *in.ResidualValue
		}
		if !moneyOK(residual, decimals) || residual.GreaterThan(cur.Cost.Sub(cur.AccumDepr)) {
			return revision{}, nil, invalid("revision", fieldErr("residual_value", "INVALID_VALUE", "an amount that is not negative and not above the carrying value"))
		}
		if err := checkLife(cur, life, residual); err != nil {
			return revision{}, nil, err
		}
		arg.LifeMonths, arg.Method, arg.DbFactor, arg.ResidualValue = &life, method, factor, residual
		if cur.LifeMonths == nil { // a construction in progress that is finished: the depreciation starts now
			inService := day
			if in.InServiceDate != nil {
				inService = *in.InServiceDate
			}
			rule := cfg.StartRule
			if in.StartRule != nil {
				rule = *in.StartRule
			}
			var f []apperr.FieldError
			if rule != StartInServiceMonth && rule != StartNextMonth {
				f = append(f, fieldErr("start_rule", "INVALID_VALUE", "IN_SERVICE_MONTH or NEXT_MONTH"))
			}
			if inService.After(day) {
				f = append(f, fieldErr("in_service_date", "IN_THE_FUTURE", "not after the open business date"))
			}
			fd := MonthStart(inService)
			if rule == StartNextMonth {
				fd = AddMonths(fd, 1)
			}
			if cfg.RegisterFrom != nil && fd.Before(MonthStart(*cfg.RegisterFrom)) {
				f = append(f, fieldErr("in_service_date", "BEFORE_REGISTER_START", "the depreciation cannot start before the register does"))
			}
			if len(f) > 0 {
				return revision{}, nil, invalid("revision", f...)
			}
			arg.InServiceDate, arg.StartRule, arg.FirstDepMonth = inService, rule, &fd
			newState["in_service_date"], newState["start_rule"] = inService, rule
		}
		newState["life_months"], newState["method"], newState["residual_value"] = life, *method, residual
	} else {
		newState["life_months"], newState["method"], newState["residual_value"] = nil, nil, decimal.Zero
	}
	// the journal: the cost moves to the new account, and so does the accumulated depreciation; it is skipped when both categories use the same accounts
	var lines []accounting.SystemLine
	desc := "Change of category " + cur.Number
	if from.CostAccountID != to.CostAccountID {
		lines = append(lines,
			accounting.SystemLine{AccountID: to.CostAccountID, Debit: cur.Cost, Description: desc, SourceType: journalSourceAsset, SourceRef: cur.Number},
			accounting.SystemLine{AccountID: from.CostAccountID, Credit: cur.Cost, Description: desc, SourceType: journalSourceAsset, SourceRef: cur.Number})
	}
	if cur.AccumDepr.IsPositive() && from.AccumulatedAccountID != nil && to.AccumulatedAccountID != nil && *from.AccumulatedAccountID != *to.AccumulatedAccountID {
		lines = append(lines,
			accounting.SystemLine{AccountID: *from.AccumulatedAccountID, Debit: cur.AccumDepr, Description: desc, SourceType: journalSourceAsset, SourceRef: cur.Number},
			accounting.SystemLine{AccountID: *to.AccumulatedAccountID, Credit: cur.AccumDepr, Description: desc, SourceType: journalSourceAsset, SourceRef: cur.Number})
	}
	var journalID *int64
	if len(lines) > 0 {
		jid, _, err := po.Post(ctx, accounting.SystemJournal{Type: accounting.JournalAsset, Date: day, Description: fmt.Sprintf("Change of category of %s: %s to %s", cur.Number, from.Code, to.Code),
			Reference: "FAREC " + cur.Number, Lines: lines})
		if err != nil {
			return revision{}, nil, err
		}
		journalID = &jid
	}
	rev := revision{old: map[string]any{"category_id": from.ID, "category": from.Code, "life_months": cur.LifeMonths, "method": cur.Method, "residual_value": cur.ResidualValue},
		new: newState, journalID: journalID}
	return rev, func(revisionID int64) error {
		return s.reclassAsset(ctx, p, propertyID, cur, revisionID, day, journalID, arg)
	}, nil
}

// ---------------------------------------------------------------------------------------------------------------------
// History

// HistoryEntry is one thing that happened to an asset: a move, a revision, a month of depreciation.
type HistoryEntry struct {
	Kind          string         `json:"kind"`
	Date          civil.Date     `json:"date"`
	Reason        *string        `json:"reason"`
	Details       map[string]any `json:"details"`
	JournalID     *int64         `json:"journal_id"`
	JournalNumber *string        `json:"journal_number"`
	By            *string        `json:"by"`
	At            time.Time      `json:"at"`
}

// historyKindDepreciation is the kind of the entry of a month of depreciation.
const historyKindDepreciation = "DEPRECIATION"

// GetHistory lists the moves, revisions and months of depreciation of an asset in the order they happened (any asset permission).
func (s *Service) GetHistory(ctx context.Context, propertyID, id int64) ([]HistoryEntry, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	if _, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
		return nil, orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
	}
	out := []HistoryEntry{}
	moves, err := q.ListMovesOfAsset(ctx, assetsdb.ListMovesOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id})
	if err != nil {
		return nil, err
	}
	for _, m := range moves {
		reason := m.Reason
		out = append(out, HistoryEntry{Kind: "MOVE", Date: m.EffectiveOn, Reason: &reason, By: m.CreatedByName, At: m.CreatedAt, Details: map[string]any{
			"from_location": m.FromLocationName, "to_location": m.ToLocationName, "from_department": m.FromDepartmentName, "to_department": m.ToDepartmentName}})
	}
	revs, err := q.ListRevisionsOfAsset(ctx, assetsdb.ListRevisionsOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id})
	if err != nil {
		return nil, err
	}
	for _, r := range revs {
		reason := r.Reason
		var oldV, newV map[string]any
		_ = json.Unmarshal(r.OldValue, &oldV)
		_ = json.Unmarshal(r.NewValue, &newV)
		out = append(out, HistoryEntry{Kind: r.Kind, Date: r.EffectiveOn, Reason: &reason, By: r.CreatedByName, At: r.CreatedAt, Details: map[string]any{"old": oldV, "new": newV},
			JournalID: r.JournalID, JournalNumber: r.JournalNumber})
	}
	lines, err := q.ListDeprLinesOfAsset(ctx, assetsdb.ListDeprLinesOfAssetParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: id})
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		jn := l.JournalNumber
		out = append(out, HistoryEntry{Kind: historyKindDepreciation, Date: MonthEnd(l.PeriodMonth), Details: map[string]any{"month": monthLabel(l.PeriodMonth), "months": l.Months, "amount": l.Amount,
			"accum_after": l.AccumAfter, "carrying_after": l.CarryingAfter}, JournalNumber: &jn})
	}
	disposals, err := q.ListDisposals(ctx, assetsdb.ListDisposalsParams{TenantID: p.TenantID, PropertyID: propertyID, AssetID: &id})
	if err != nil {
		return nil, err
	}
	for _, d := range disposals {
		jn := d.JournalNumber
		reason := d.ReasonCode
		out = append(out, HistoryEntry{Kind: "DISPOSAL", Date: d.DisposalDate, Reason: &reason, By: d.CreatedByName, At: d.CreatedAt, JournalID: &d.JournalID, JournalNumber: &jn, Details: map[string]any{
			"number": d.Number, "kind": d.Kind, "units": d.Units, "cost_removed": d.CostRemoved, "accum_removed": d.AccumRemoved, "carrying_removed": d.CarryingRemoved, "proceeds": d.Proceeds,
			"gain_loss": d.GainLoss, "status": d.Status, "approved_by": d.ApprovedByName, "reversal_journal_number": d.ReversalJournalNumber, "reversal_reason": d.ReversalReason}})
	}
	reqs, err := q.ListDisposalRequests(ctx, assetsdb.ListDisposalRequestsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	for _, r := range reqs {
		if r.AssetID != id || r.Status == RequestApproved { // an approved one is the disposal above
			continue
		}
		reason := r.ReasonCode
		out = append(out, HistoryEntry{Kind: "DISPOSAL_REQUEST", Date: r.DisposalDate, Reason: &reason, By: r.RequestedByName, At: r.RequestedAt, Details: map[string]any{
			"kind": r.Kind, "units": r.Units, "proceeds": r.Proceeds, "approval_amount": r.ApprovalAmount, "status": r.Status, "decided_by": r.DecidedByName, "decision_note": r.DecisionNote}})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].Date.Equal(out[j].Date) {
			return out[i].Date.Before(out[j].Date)
		}
		return out[i].At.Before(out[j].At)
	})
	return out, nil
}
