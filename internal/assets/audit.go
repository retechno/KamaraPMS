package assets

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/assets/assetsdb"
	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The stock-take on a phone (docs/architecture/37-fixed-assets.md 4.10, A14). A person opens a stock-take of one place (or of the whole property), scans the assets they find, and closes it. The
// close writes proposals (move an asset to where it was found, write off what is missing or damaged); a person decides each proposal and the normal use case runs under their permissions. Nothing
// here posts a journal.

// The status of a stock-take and of a proposal, the results of a scan, and the kinds of proposal.
const (
	AuditInProgress = "IN_PROGRESS"
	AuditClosed     = "CLOSED"

	ScanFound      = "FOUND"      // where the register says it is
	ScanMoved      = "MOVED"      // found here, the register says it is in another place
	ScanUnexpected = "UNEXPECTED" // found here, the register says it has no place
	ScanDamaged    = "DAMAGED"    // found, and broken
	ScanMissing    = "MISSING"    // expected and not scanned: written by the close

	ProposalMove     = "MOVE"
	ProposalWriteOff = "WRITE_OFF"

	ProposalPending  = "PENDING"
	ProposalApplied  = "APPLIED"
	ProposalRejected = "REJECTED"

	resultMove            = "MOVE"
	resultDisposal        = "DISPOSAL"
	resultDisposalRequest = "DISPOSAL_REQUEST"

	maxAuditNote = 500
)

// AuditCounts are the lines of a stock-take by what the scan found.
type AuditCounts struct {
	Expected   int `json:"expected"`
	Found      int `json:"found"`
	Moved      int `json:"moved"`
	Unexpected int `json:"unexpected"`
	Damaged    int `json:"damaged"`
	Missing    int `json:"missing"`
	Proposals  int `json:"proposals"`
}

// Audit is a stock-take.
type Audit struct {
	ID            int64       `json:"id"`
	Number        string      `json:"number"`
	LocationID    *int64      `json:"location_id"`
	LocationCode  *string     `json:"location_code"`
	LocationName  *string     `json:"location_name"`
	Status        string      `json:"status"`
	StartedBy     *int64      `json:"started_by"`
	StartedByName *string     `json:"started_by_name"`
	StartedAt     time.Time   `json:"started_at"`
	ClosedBy      *int64      `json:"closed_by"`
	ClosedByName  *string     `json:"closed_by_name"`
	ClosedAt      *time.Time  `json:"closed_at"`
	Note          *string     `json:"note"`
	Counts        AuditCounts `json:"counts"`
}

// AuditSummary is a row of the list of stock-takes.
type AuditSummary struct {
	ID               int64      `json:"id"`
	Number           string     `json:"number"`
	LocationID       *int64     `json:"location_id"`
	LocationCode     *string    `json:"location_code"`
	LocationName     *string    `json:"location_name"`
	Status           string     `json:"status"`
	StartedAt        time.Time  `json:"started_at"`
	ClosedAt         *time.Time `json:"closed_at"`
	Scanned          int        `json:"scanned"`
	Missing          int        `json:"missing"`
	PendingProposals int        `json:"pending_proposals"`
}

// AuditLine is what a stock-take says about one asset.
type AuditLine struct {
	AssetID              int64      `json:"asset_id"`
	AssetNumber          string     `json:"asset_number"`
	AssetName            string     `json:"asset_name"`
	Result               string     `json:"result"`
	ExpectedLocationID   *int64     `json:"expected_location_id"`
	ExpectedLocationName *string    `json:"expected_location_name"`
	FoundLocationID      *int64     `json:"found_location_id"`
	FoundLocationName    *string    `json:"found_location_name"`
	Note                 *string    `json:"note"`
	ScannedAt            *time.Time `json:"scanned_at"`
	ScannedBy            *int64     `json:"scanned_by"`
	ScannedByName        *string    `json:"scanned_by_name"`
}

// AuditExpected is an asset that should be in the place and was not scanned yet.
type AuditExpected struct {
	AssetID         int64           `json:"asset_id"`
	Number          string          `json:"number"`
	Name            string          `json:"name"`
	LocationID      *int64          `json:"location_id"`
	LocationName    *string         `json:"location_name"`
	CarryingValue   decimal.Decimal `json:"carrying_value"`
	DisposalPending bool            `json:"disposal_pending"`
}

// AuditDetail is a stock-take with its lines; while it is open it also lists what has not been scanned (what the close would call missing).
type AuditDetail struct {
	Audit
	Lines     []AuditLine     `json:"lines"`
	Unscanned []AuditExpected `json:"unscanned"`
}

// StartAuditInput opens a stock-take of a place, or of the whole property when LocationID is nil.
type StartAuditInput struct {
	LocationID     *int64 `json:"location_id"`
	Note           string `json:"note"`
	IdempotencyKey string `json:"-"`
}

// ScanInput is one scan: the asset (the code of its QR "FA:...", the number typed by hand, or its id) and, for a stock-take of the whole property, the place where it was found (the code of the QR
// "FL:..." or its id). Damaged says the asset is broken and needs a note.
type ScanInput struct {
	Code         string `json:"code"`
	AssetID      *int64 `json:"asset_id"`
	LocationCode string `json:"location_code"`
	LocationID   *int64 `json:"location_id"`
	Damaged      bool   `json:"damaged"`
	Note         string `json:"note"`
}

// ScanResult is the line a scan wrote and the counts of the stock-take after it.
type ScanResult struct {
	Line   AuditLine   `json:"line"`
	Counts AuditCounts `json:"counts"`
}

// CloseAuditResult is a closed stock-take with what its close wrote.
type CloseAuditResult struct {
	Audit     AuditDetail `json:"audit"`
	Missing   int         `json:"missing"`
	Proposals int         `json:"proposals"`
}

func auditNotFound() *apperr.Error { return notFound("AUDIT_NOT_FOUND", "stock-take") }

func auditNotOpen(number string) *apperr.Error {
	return apperr.Conflict("AUDIT_NOT_OPEN", "the stock-take is closed: it takes no more scans").WithContext("audit", number)
}

func auditFrom(r assetsdb.GetAuditRow) Audit {
	return Audit{ID: r.ID, Number: r.Number, LocationID: r.LocationID, LocationCode: r.LocationCode, LocationName: r.LocationName, Status: r.Status, StartedBy: r.StartedBy,
		StartedByName: r.StartedByName, StartedAt: r.StartedAt, ClosedBy: r.ClosedBy, ClosedByName: r.ClosedByName, ClosedAt: r.ClosedAt, Note: r.Note,
		Counts: AuditCounts{Expected: int(r.ExpectedCount), Found: int(r.FoundCount), Moved: int(r.MovedCount), Unexpected: int(r.UnexpectedCount), Damaged: int(r.DamagedCount),
			Missing: int(r.MissingCount), Proposals: int(r.ProposalCount)}}
}

func lineFrom(r assetsdb.ListAuditLinesRow) AuditLine {
	return AuditLine{AssetID: r.AssetID, AssetNumber: r.AssetNumber, AssetName: r.AssetName, Result: r.Result, ExpectedLocationID: r.ExpectedLocationID,
		ExpectedLocationName: r.ExpectedLocationName, FoundLocationID: r.FoundLocationID, FoundLocationName: r.FoundLocationName, Note: r.Note, ScannedAt: r.ScannedAt, ScannedBy: r.ScannedBy,
		ScannedByName: r.ScannedByName}
}

// auditDetail reads a stock-take with its lines (and, while it is open, what is not scanned yet) in the ambient transaction.
func (s *Service) auditDetail(ctx context.Context, tenantID, propertyID, id int64) (AuditDetail, error) {
	q := s.q(ctx)
	a, err := q.GetAudit(ctx, assetsdb.GetAuditParams{TenantID: tenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return AuditDetail{}, orNotFound(err, auditNotFound())
	}
	d := AuditDetail{Audit: auditFrom(a), Lines: []AuditLine{}, Unscanned: []AuditExpected{}}
	lines, err := q.ListAuditLines(ctx, assetsdb.ListAuditLinesParams{TenantID: tenantID, PropertyID: propertyID, AuditID: id})
	if err != nil {
		return d, err
	}
	for _, l := range lines {
		d.Lines = append(d.Lines, lineFrom(l))
	}
	if a.Status == AuditInProgress {
		rows, err := q.ListAuditExpected(ctx, assetsdb.ListAuditExpectedParams{TenantID: tenantID, PropertyID: propertyID, LocationID: a.LocationID, AuditID: id})
		if err != nil {
			return d, err
		}
		for _, r := range rows {
			d.Unscanned = append(d.Unscanned, AuditExpected{AssetID: r.ID, Number: r.Number, Name: r.Name, LocationID: r.LocationID, LocationName: r.LocationName,
				CarryingValue: r.CarryingValue, DisposalPending: r.DisposalPending})
		}
	}
	return d, nil
}

// StartAudit opens a stock-take (asset.manage, an Idempotency-Key). One stock-take of a place is open at a time.
func (s *Service) StartAudit(ctx context.Context, propertyID int64, in StartAuditInput) (AuditDetail, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return AuditDetail{}, err
	}
	key, err := idempotencyKey(in.IdempotencyKey)
	if err != nil {
		return AuditDetail{}, err
	}
	note := trimmed(in.Note)
	if len([]rune(note)) > maxAuditNote {
		return AuditDetail{}, invalid("stock-take", fieldErr("note", "INVALID_VALUE", fmt.Sprintf("at most %d characters", maxAuditNote)))
	}
	var out AuditDetail
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		q := s.q(ctx)
		if prev, err := q.GetAuditByKey(ctx, assetsdb.GetAuditByKeyParams{PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			out, err = s.auditDetail(ctx, p.TenantID, propertyID, prev)
			return err
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if in.LocationID != nil {
			if _, err := q.GetLocation(ctx, assetsdb.GetLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.LocationID}); err != nil {
				return orNotFound(err, notFound("LOCATION_NOT_FOUND", "location"))
			}
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFAAudit) // last in the lock order
		if err != nil {
			return err
		}
		var n *string
		if note != "" {
			n = &note
		}
		id, err := q.InsertAudit(ctx, assetsdb.InsertAuditParams{TenantID: p.TenantID, PropertyID: propertyID, Number: number, LocationID: in.LocationID, ActorID: p.ActorID(), Note: n, IdempotencyKey: &key})
		if err != nil {
			return err
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.audit_started", "fa_audit", id, number, nil, map[string]any{"location_id": in.LocationID, "note": n})); err != nil {
			return err
		}
		out, err = s.auditDetail(ctx, p.TenantID, propertyID, id)
		return err
	})
	return out, err
}

// GetAudit answers a stock-take with its lines (any asset permission).
func (s *Service) GetAudit(ctx context.Context, propertyID, id int64) (AuditDetail, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return AuditDetail{}, err
	}
	return s.auditDetail(ctx, p.TenantID, propertyID, id)
}

// ListAudits lists the stock-takes of the property, the latest first (any asset permission). Status narrows to IN_PROGRESS or CLOSED.
func (s *Service) ListAudits(ctx context.Context, propertyID int64, status string) ([]AuditSummary, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	var st *string
	if status = strings.ToUpper(trimmed(status)); status != "" {
		if status != AuditInProgress && status != AuditClosed {
			return nil, invalid("filter", fieldErr("status", "INVALID_VALUE", "IN_PROGRESS or CLOSED"))
		}
		st = &status
	}
	rows, err := s.q(ctx).ListAudits(ctx, assetsdb.ListAuditsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: st})
	if err != nil {
		return nil, err
	}
	out := make([]AuditSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuditSummary{ID: r.ID, Number: r.Number, LocationID: r.LocationID, LocationCode: r.LocationCode, LocationName: r.LocationName, Status: r.Status, StartedAt: r.StartedAt,
			ClosedAt: r.ClosedAt, Scanned: int(r.ScannedCount), Missing: int(r.MissingCount), PendingProposals: int(r.PendingProposals)})
	}
	return out, nil
}

// afterPrefix cuts the kind off a code that a QR carries ("FA:K7M..." is the code K7M...).
func afterPrefix(code, prefix string) string {
	code = trimmed(code)
	if len(code) > len(prefix) && strings.EqualFold(code[:len(prefix)], prefix) {
		return trimmed(code[len(prefix):])
	}
	return code
}

// ScanAsset records one scan (asset.manage): the row of the asset in the stock-take, written at once; a second scan of the same asset is the same row. Business day not needed: nothing is posted.
// The stock-take is held in share mode, so the close (update) waits for the scans in flight and a scan after the close is refused.
func (s *Service) ScanAsset(ctx context.Context, propertyID, auditID int64, in ScanInput) (ScanResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return ScanResult{}, err
	}
	note := trimmed(in.Note)
	var f []apperr.FieldError
	if (trimmed(in.Code) == "") == (in.AssetID == nil) {
		f = append(f, fieldErr("code", "REQUIRED", "the code of the asset, or its id"))
	}
	if len([]rune(note)) > maxAuditNote {
		f = append(f, fieldErr("note", "INVALID_VALUE", fmt.Sprintf("at most %d characters", maxAuditNote)))
	}
	if in.Damaged && note == "" {
		f = append(f, fieldErr("note", "REQUIRED", "say what is wrong with the asset"))
	}
	if len(f) > 0 {
		return ScanResult{}, invalid("scan", f...)
	}
	var res ScanResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		if err := db.LockRows(ctx, db.FAAudits, db.ForShare, propertyID, []int64{auditID}); err != nil {
			return orNotFound(err, auditNotFound())
		}
		q := s.q(ctx)
		st, err := q.GetAuditState(ctx, assetsdb.GetAuditStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: auditID})
		if err != nil {
			return orNotFound(err, auditNotFound())
		}
		if st.Status != AuditInProgress {
			return auditNotOpen(st.Number)
		}
		// where it is found: the place of the stock-take, or the one the phone scanned for a stock-take of the whole property
		var where *int64
		switch {
		case in.LocationID != nil:
			where = in.LocationID
		case trimmed(in.LocationCode) != "":
			id, err := q.FindLocationByLabel(ctx, assetsdb.FindLocationByLabelParams{TenantID: p.TenantID, PropertyID: propertyID, Code: afterPrefix(in.LocationCode, "FL:")})
			if err != nil {
				return orNotFound(err, notFound("LOCATION_NOT_FOUND", "location"))
			}
			where = &id
		}
		switch {
		case st.LocationID != nil && where != nil && *where != *st.LocationID:
			return invalid("scan", fieldErr("location_code", "INVALID_VALUE", "this stock-take counts another place"))
		case st.LocationID != nil:
			where = st.LocationID
		case where == nil:
			return invalid("scan", fieldErr("location_code", "REQUIRED", "scan the place first"))
		}
		loc, err := q.GetLocation(ctx, assetsdb.GetLocationParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *where})
		if err != nil {
			return orNotFound(err, notFound("LOCATION_NOT_FOUND", "location"))
		}
		assetID := int64(0)
		if in.AssetID != nil {
			assetID = *in.AssetID
		} else if assetID, err = q.FindAssetByCode(ctx, assetsdb.FindAssetByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: afterPrefix(in.Code, "FA:")}); err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		cur, err := q.GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: assetID})
		if err != nil {
			return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
		}
		if cur.Status != statusActive {
			return apperr.Conflict("ASSET_STATUS_INVALID", "only an active asset is counted").WithContext("asset", cur.Number).WithContext("status", cur.Status)
		}
		result := ScanFound
		switch {
		case in.Damaged:
			result = ScanDamaged
		case cur.LocationID == nil:
			result = ScanUnexpected
		case *cur.LocationID != *where:
			result = ScanMoved
		}
		var n *string
		if note != "" {
			n = &note
		}
		if err := q.UpsertAuditLine(ctx, assetsdb.UpsertAuditLineParams{TenantID: p.TenantID, PropertyID: propertyID, AuditID: auditID, AssetID: cur.ID, ExpectedLocationID: cur.LocationID,
			Result: result, FoundLocationID: where, Note: n, ScannedAt: nowPtr(s.clock.Now()), ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := s.audit.Write(ctx, audit2(s.entry(p, propertyID, civil.Date{}, "asset.audit_scanned", "fa_audit", auditID, st.Number, nil,
			map[string]any{"asset": cur.Number, "result": result, "found_location": loc.Code}))); err != nil {
			return err
		}
		d, err := s.auditDetail(ctx, p.TenantID, propertyID, auditID)
		if err != nil {
			return err
		}
		res.Counts = d.Counts
		for _, l := range d.Lines {
			if l.AssetID == cur.ID {
				res.Line = l
			}
		}
		return nil
	})
	return res, err
}

// CloseAudit closes a stock-take (asset.manage): every expected asset that was not scanned becomes MISSING, and the lines that are not "found" become proposals. No journal. Lock order: the settings
// of the register (share), the assets that would be missing (ascending), then the stock-take (update), so a disposal that commits first is seen and a scan in flight is waited for.
func (s *Service) CloseAudit(ctx context.Context, propertyID, auditID int64) (CloseAuditResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage)
	if err != nil {
		return CloseAuditResult{}, err
	}
	var res CloseAuditResult
	err = s.write(ctx, propertyID, func(ctx context.Context, day civil.Date) error {
		if _, err := s.lockSettings(ctx, p, propertyID, db.ForShare); err != nil {
			return err
		}
		q := s.q(ctx)
		st, err := q.GetAuditState(ctx, assetsdb.GetAuditStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: auditID})
		if err != nil {
			return orNotFound(err, auditNotFound())
		}
		if st.Status != AuditInProgress {
			return auditNotOpen(st.Number)
		}
		candidates, err := q.ListAuditExpected(ctx, assetsdb.ListAuditExpectedParams{TenantID: p.TenantID, PropertyID: propertyID, LocationID: st.LocationID, AuditID: auditID})
		if err != nil {
			return err
		}
		locked := map[int64]bool{}
		ids := make([]int64, 0, len(candidates))
		for _, c := range candidates {
			ids = append(ids, c.ID)
			locked[c.ID] = true
		}
		if len(ids) > 0 {
			if err := lockAssets(ctx, propertyID, uniqueSorted(ids)); err != nil {
				return err
			}
		}
		if err := db.LockRows(ctx, db.FAAudits, db.ForUpdate, propertyID, []int64{auditID}); err != nil {
			return orNotFound(err, auditNotFound())
		}
		if st, err = q.GetAuditState(ctx, assetsdb.GetAuditStateParams{TenantID: p.TenantID, PropertyID: propertyID, ID: auditID}); err != nil {
			return orNotFound(err, auditNotFound())
		}
		if st.Status != AuditInProgress {
			return auditNotOpen(st.Number)
		}
		// under the locks: what is still expected and has no line (a scan may have come in, an asset may have been disposed of)
		again, err := q.ListAuditExpected(ctx, assetsdb.ListAuditExpectedParams{TenantID: p.TenantID, PropertyID: propertyID, LocationID: st.LocationID, AuditID: auditID})
		if err != nil {
			return err
		}
		missing := make([]int64, 0, len(again))
		for _, a := range again {
			if locked[a.ID] {
				missing = append(missing, a.ID)
			}
		}
		if len(missing) > 0 {
			got, err := q.InsertMissingLines(ctx, assetsdb.InsertMissingLinesParams{TenantID: p.TenantID, PropertyID: propertyID, AuditID: auditID, Ids: uniqueSorted(missing)})
			if err != nil {
				return err
			}
			res.Missing = len(got)
		}
		made, err := q.InsertProposals(ctx, assetsdb.InsertProposalsParams{TenantID: p.TenantID, PropertyID: propertyID, AuditID: auditID})
		if err != nil {
			return err
		}
		res.Proposals = len(made)
		if _, err := q.CloseAudit(ctx, assetsdb.CloseAuditParams{TenantID: p.TenantID, PropertyID: propertyID, ID: auditID, ActorID: p.ActorID(), ClosedAt: nowPtr(s.clock.Now())}); err != nil {
			return orNotFound(err, auditNotOpen(st.Number))
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, day, "asset.audit_closed", "fa_audit", auditID, st.Number, map[string]any{"status": AuditInProgress},
			map[string]any{"status": AuditClosed, "missing": res.Missing, "proposals": res.Proposals})); err != nil {
			return err
		}
		res.Audit, err = s.auditDetail(ctx, p.TenantID, propertyID, auditID)
		return err
	})
	return res, err
}

// ---------------------------------------------------------------------------------------------------------------------
// Proposals

// Proposal is what a closed stock-take proposes for one asset, and what became of it.
type Proposal struct {
	ID                   int64           `json:"id"`
	AuditID              int64           `json:"audit_id"`
	AuditNumber          string          `json:"audit_number"`
	AssetID              int64           `json:"asset_id"`
	AssetNumber          string          `json:"asset_number"`
	AssetName            string          `json:"asset_name"`
	AssetStatus          string          `json:"asset_status"`
	Kind                 string          `json:"kind"`
	ToLocationID         *int64          `json:"to_location_id"`
	ToLocationCode       *string         `json:"to_location_code"`
	ToLocationName       *string         `json:"to_location_name"`
	Status               string          `json:"status"`
	DecidedBy            *int64          `json:"decided_by"`
	DecidedByName        *string         `json:"decided_by_name"`
	DecidedAt            *time.Time      `json:"decided_at"`
	DecisionNote         *string         `json:"decision_note"`
	ResultType           *string         `json:"result_type"`
	ResultID             *int64          `json:"result_id"`
	CreatedAt            time.Time       `json:"created_at"`
	LineResult           string          `json:"line_result"`
	LineNote             *string         `json:"line_note"`
	ExpectedLocationID   *int64          `json:"expected_location_id"`
	ExpectedLocationName *string         `json:"expected_location_name"`
	FoundLocationID      *int64          `json:"found_location_id"`
	FoundLocationName    *string         `json:"found_location_name"`
	CarryingValue        decimal.Decimal `json:"carrying_value"`
}

func proposalFrom(r assetsdb.ListProposalsRow) Proposal {
	return Proposal{ID: r.ID, AuditID: r.AuditID, AuditNumber: r.AuditNumber, AssetID: r.AssetID, AssetNumber: r.AssetNumber, AssetName: r.AssetName, AssetStatus: r.AssetStatus, Kind: r.Kind,
		ToLocationID: r.ToLocationID, ToLocationCode: r.ToLocationCode, ToLocationName: r.ToLocationName, Status: r.Status, DecidedBy: r.DecidedBy, DecidedByName: r.DecidedByName,
		DecidedAt: r.DecidedAt, DecisionNote: r.DecisionNote, ResultType: r.ResultType, ResultID: r.ResultID, CreatedAt: r.CreatedAt, LineResult: r.LineResult, LineNote: r.LineNote,
		ExpectedLocationID: r.ExpectedLocationID, ExpectedLocationName: r.ExpectedLocationName, FoundLocationID: r.FoundLocationID, FoundLocationName: r.FoundLocationName,
		CarryingValue: r.CarryingValue}
}

func proposalNotFound() *apperr.Error { return notFound("PROPOSAL_NOT_FOUND", "proposal") }

func proposalNotPending(id int64) *apperr.Error {
	return apperr.Conflict("PROPOSAL_NOT_PENDING", "the proposal was decided already").WithContext("proposal", fmt.Sprint(id))
}

// ListProposals lists the proposals of the stock-takes, the latest first (any asset permission). Status narrows to PENDING, APPLIED or REJECTED; AuditID to one stock-take.
func (s *Service) ListProposals(ctx context.Context, propertyID int64, status string, auditID *int64) ([]Proposal, error) {
	p, err := s.require(ctx, propertyID, readers...)
	if err != nil {
		return nil, err
	}
	var st *string
	if status = strings.ToUpper(trimmed(status)); status != "" {
		if status != ProposalPending && status != ProposalApplied && status != ProposalRejected {
			return nil, invalid("filter", fieldErr("status", "INVALID_VALUE", "PENDING, APPLIED or REJECTED"))
		}
		st = &status
	}
	rows, err := s.q(ctx).ListProposals(ctx, assetsdb.ListProposalsParams{TenantID: p.TenantID, PropertyID: propertyID, Status: st, AuditID: auditID})
	if err != nil {
		return nil, err
	}
	out := make([]Proposal, 0, len(rows))
	for _, r := range rows {
		out = append(out, proposalFrom(r))
	}
	return out, nil
}

func (s *Service) proposalByID(ctx context.Context, tenantID, propertyID, id int64) (Proposal, error) {
	rows, err := s.q(ctx).ListProposals(ctx, assetsdb.ListProposalsParams{TenantID: tenantID, PropertyID: propertyID, ID: &id})
	if err != nil {
		return Proposal{}, err
	}
	if len(rows) == 0 {
		return Proposal{}, proposalNotFound()
	}
	return proposalFrom(rows[0]), nil
}

// ApplyProposalInput carries the approval of a write-off (the second person's credentials, when the rule asks for them).
type ApplyProposalInput struct {
	Approval *iam.ApprovalInput `json:"approval"`
}

// ApplyProposalResult is the proposal after it was decided and what it made: a move, a disposal, or a disposal that waits for its approver.
type ApplyProposalResult struct {
	Proposal Proposal `json:"proposal"`
}

func proposalKey(id int64) string { return fmt.Sprintf("fa-proposal-%d", id) }

// ApplyProposal applies a proposal under the permissions of the person who applies it (asset.manage for a move, asset.dispose for a write-off, with the approval of 7.2): the normal use case runs,
// so nothing bypasses its rules, and the proposal is marked in the same transaction. The use case takes its own locks first; the proposal row is the last thing written, and a proposal that was
// decided meanwhile undoes the use case with PROPOSAL_NOT_PENDING.
func (s *Service) ApplyProposal(ctx context.Context, propertyID, id int64, in ApplyProposalInput) (ApplyProposalResult, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage, auth.PermAssetDispose)
	if err != nil {
		return ApplyProposalResult{}, err
	}
	var out ApplyProposalResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		pr, err := s.proposalByID(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if pr.Status != ProposalPending {
			return proposalNotPending(id)
		}
		key := proposalKey(id)
		var resType string
		var resID int64
		switch pr.Kind {
		case ProposalMove:
			if pr.ToLocationID == nil {
				return apperr.Internal(errors.New("assets: a move proposal without a place"))
			}
			reason := fmt.Sprintf("Stock-take %s", pr.AuditNumber)
			if _, err := s.MoveAssets(ctx, propertyID, MoveInput{AssetIDs: []int64{pr.AssetID}, ChangeLocation: true, LocationID: pr.ToLocationID, Reason: reason, IdempotencyKey: key}); err != nil {
				return err
			}
			mid, err := s.q(ctx).GetMoveByKey(ctx, assetsdb.GetMoveByKeyParams{PropertyID: propertyID, IdempotencyKey: &key, AssetID: pr.AssetID})
			if err != nil {
				return err
			}
			resType, resID = resultMove, mid
		case ProposalWriteOff:
			cur, err := s.q(ctx).GetAsset(ctx, assetsdb.GetAssetParams{TenantID: p.TenantID, PropertyID: propertyID, ID: pr.AssetID})
			if err != nil {
				return orNotFound(err, notFound("ASSET_NOT_FOUND", "asset"))
			}
			reason := ReasonLost
			if pr.LineResult == ScanDamaged {
				reason = ReasonDamaged
			}
			note := fmt.Sprintf("Stock-take %s", pr.AuditNumber)
			if pr.LineNote != nil && trimmed(*pr.LineNote) != "" {
				note += ": " + trimmed(*pr.LineNote)
			}
			units := cur.UnitCount
			r, err := s.Dispose(ctx, propertyID, pr.AssetID, DisposeInput{Kind: DisposalWriteOff, ReasonCode: reason, Units: &units, Note: note, Approval: in.Approval, IdempotencyKey: key})
			if err != nil {
				return err
			}
			switch {
			case r.Disposal != nil:
				resType, resID = resultDisposal, r.Disposal.ID
			case r.Request != nil:
				resType, resID = resultDisposalRequest, r.Request.ID
			default:
				return apperr.Internal(errors.New("assets: a write-off made neither a disposal nor a request"))
			}
		default:
			return apperr.Internal(fmt.Errorf("assets: unknown kind of proposal %q", pr.Kind))
		}
		if _, err := s.q(ctx).DecideProposal(ctx, assetsdb.DecideProposalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ProposalApplied, ActorID: p.ActorID(),
			DecidedAt: nowPtr(s.clock.Now()), ResultType: &resType, ResultID: &resID}); err != nil {
			return orNotFound(err, proposalNotPending(id))
		}
		if err := s.audit.Write(ctx, audit2(s.entry(p, propertyID, civil.Date{}, "asset.proposal_applied", "fa_proposal", id, pr.AssetNumber, map[string]any{"status": ProposalPending},
			map[string]any{"status": ProposalApplied, "kind": pr.Kind, "result_type": resType, "result_id": resID, "audit": pr.AuditNumber}))); err != nil {
			return err
		}
		out.Proposal, err = s.proposalByID(ctx, p.TenantID, propertyID, id)
		return err
	})
	return out, err
}

// RejectProposal keeps a proposal as it is and says why it is not applied (asset.manage or asset.dispose; the note is required).
func (s *Service) RejectProposal(ctx context.Context, propertyID, id int64, note string) (Proposal, error) {
	p, err := s.require(ctx, propertyID, auth.PermAssetManage, auth.PermAssetDispose)
	if err != nil {
		return Proposal{}, err
	}
	note = trimmed(note)
	if n := len([]rune(note)); n < 1 || n > maxAuditNote {
		return Proposal{}, invalid("proposal", fieldErr("note", "REQUIRED", fmt.Sprintf("a reason of 1 to %d characters", maxAuditNote)))
	}
	var out Proposal
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		pr, err := s.proposalByID(ctx, p.TenantID, propertyID, id)
		if err != nil {
			return err
		}
		if pr.Status != ProposalPending {
			return proposalNotPending(id)
		}
		if _, err := s.q(ctx).DecideProposal(ctx, assetsdb.DecideProposalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Status: ProposalRejected, ActorID: p.ActorID(),
			DecidedAt: nowPtr(s.clock.Now()), DecisionNote: &note}); err != nil {
			return orNotFound(err, proposalNotPending(id))
		}
		if err := s.audit.Write(ctx, audit2(s.entry(p, propertyID, civil.Date{}, "asset.proposal_rejected", "fa_proposal", id, pr.AssetNumber, map[string]any{"status": ProposalPending},
			map[string]any{"status": ProposalRejected, "kind": pr.Kind, "note": note, "audit": pr.AuditNumber}))); err != nil {
			return err
		}
		out, err = s.proposalByID(ctx, p.TenantID, propertyID, id)
		return err
	})
	return out, err
}

func nowPtr(t time.Time) *time.Time { return &t }

// audit2 marks an entry as not dated by a business day: a scan or a decision on a proposal posts nothing.
func audit2(e audit.Entry) audit.Entry {
	e.BusinessDate = nil
	return e
}
