package folios

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Moving to the master what is already posted (docs/architecture/30-group-folio.md 5.3, 13.4). It is the transfer of part B, by charge code, over all the member folios the group's instructions
// would now send to the master, in ONE transaction: every folio involved is locked once, in ascending order, with the master; one reason and one approval cover it; each folio and code is a batch of the
// log under the Idempotency-Key of the request (key#folio#code), so a repeat of the request answers with what was moved and moves nothing twice. The request carries the count and the total the person
// was shown: if the list changed before the locks were taken (a night posted, a charge reversed) it is refused (409 POSTED_LIST_CHANGED) and nothing moves, so the approval is for what was seen.

// MoveToMasterInput asks for the move. ExpectedCount and ExpectedTotal are the charges and the amount of the list the person saw (GET /groups/{id}/routing/posted).
type MoveToMasterInput struct {
	Reason        string             `json:"reason"`
	Approval      *iam.ApprovalInput `json:"approval"`
	ExpectedCount int                `json:"expected_count"`
	ExpectedTotal string             `json:"expected_total"`
}

// MoveToMasterResult is what moved: one entry for each folio and charge code, and the totals.
type MoveToMasterResult struct {
	Batches  []TransferResult `json:"batches"`
	Moved    int              `json:"moved"`
	Total    string           `json:"total"`
	Replayed bool             `json:"replayed"`
}

// MoveToMaster moves the posted charges of the members that the instructions send to the master (folio.reverse and an approval, as any transfer; the group's master must be open).
func (s *Service) MoveToMaster(ctx context.Context, propertyID, groupID int64, key string, in MoveToMasterInput) (MoveToMasterResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return MoveToMasterResult{}, err
	}
	if key == "" {
		return MoveToMasterResult{}, apperr.Invalid("the move is invalid", fieldErr("Idempotency-Key", "REQUIRED", "a key for the request: a repeat answers with what was moved"))
	}
	if fields := checkKey(key); len(fields) > 0 {
		return MoveToMasterResult{}, apperr.Invalid("the move is invalid", fields...)
	}
	reason, fields := requireReason(in.Reason)
	expected, perr := decimal.NewFromString(in.ExpectedTotal)
	if perr != nil {
		fields = append(fields, fieldErr("expected_total", "INVALID_VALUE", "the total of the list that was shown"))
	}
	if in.ExpectedCount < 1 {
		fields = append(fields, fieldErr("expected_count", "REQUIRED", "the number of charges of the list that was shown"))
	}
	if len(fields) > 0 {
		return MoveToMasterResult{}, apperr.Invalid("the move is invalid", fields...)
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return MoveToMasterResult{}, err
	}
	if res, ok, err := s.moveReplay(ctx, p.TenantID, propertyID, key, decimals); err != nil || ok {
		return res, err
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval) // before the transaction: no lock is held while the password is hashed
	if err != nil {
		return MoveToMasterResult{}, err
	}
	var out MoveToMasterResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		q := s.q(ctx)
		master, err := q.GetMasterFolio(ctx, foliosdb.GetMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return apperr.NotFound("MASTER_FOLIO_NOT_FOUND", "the group has no master folio")
			}
			return err
		}
		// what moves, read before the locks only to know which folios to lock; the selection is made again under them
		rows, err := s.postedRows(ctx, p.TenantID, propertyID, groupID)
		if err != nil {
			return err
		}
		ids := map[int64]bool{master.ID: true}
		for _, r := range rows {
			ids[r.FolioID] = true
		}
		locked := make([]int64, 0, len(ids))
		for id := range ids {
			locked = append(locked, id)
		}
		sort.Slice(locked, func(i, j int) bool { return locked[i] < locked[j] })
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, locked); err != nil { // L42, ascending
			return mapNotFound(err, errFolioNotFound())
		}
		if master, err = q.GetMasterFolio(ctx, foliosdb.GetMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &groupID}); err != nil {
			return err
		}
		if err := requireOpen(master); err != nil {
			return err
		}
		if rows, err = s.postedRows(ctx, p.TenantID, propertyID, groupID); err != nil { // the list as the locks guarantee it
			return err
		}
		count, total := 0, decimal.Zero
		for _, r := range rows {
			count += r.Count
			t, _ := decimal.NewFromString(r.Total)
			total = total.Add(t)
		}
		if count == 0 {
			return apperr.Conflict("TRANSFER_SELECTION_EMPTY", "nothing posted to the members matches the instructions of the group")
		}
		if count != in.ExpectedCount || !total.Equal(expected) {
			return apperr.Conflict("POSTED_LIST_CHANGED", "what is posted changed since the list was shown: read it again").
				WithContext("count", count).WithContext("total", fixed(total, decimals))
		}
		for _, r := range rows {
			src, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.FolioID})
			if err != nil {
				return orNotFound(err, errFolioNotFound())
			}
			if err := requireOpen(src); err != nil {
				return err
			}
			scope, err := s.scopeOf(ctx, p.TenantID, propertyID, src, master)
			if err != nil {
				return err
			}
			if scope != ScopeGroupMaster {
				return errTransferInvalid("the folio is not one of a member of the group")
			}
			batchKey := fmt.Sprintf("%s#%d#%d", key, r.FolioID, r.ChargeCodeID)
			code := r.ChargeCodeID
			res, err := s.moveSelected(ctx, p, propertyID, day.BusinessDate, src, master, TransferItemsInput{FolioID: master.ID, ChargeCodeID: &code}, reason, approval, scope, &batchKey, decimals)
			if err != nil {
				return err
			}
			out.Batches = append(out.Batches, res)
			out.Moved += len(res.Moved)
		}
		out.Total = fixed(total, decimals)
		return nil
	})
	return out, err
}

// postedRows is what the instructions of the group would send to the master and is on the folios of the members now, one row for each folio and charge code (no permission: the caller has its own).
func (s *Service) postedRows(ctx context.Context, tenantID, propertyID, groupID int64) ([]PostedCharges, error) {
	instr, err := s.groupInstructions(ctx, tenantID, propertyID, groupID)
	if err != nil || len(instr) == 0 {
		return nil, err
	}
	return s.postedFor(ctx, tenantID, propertyID, groupID, instr)
}

// moveReplay answers a repeat of a request that went through: the batches that carry its key, in the order they were made.
func (s *Service) moveReplay(ctx context.Context, tenantID, propertyID int64, key string, decimals int32) (MoveToMasterResult, bool, error) {
	q := s.q(ctx)
	batches, err := q.ListBatchesByKeyPrefix(ctx, foliosdb.ListBatchesByKeyPrefixParams{TenantID: tenantID, PropertyID: propertyID, Prefix: key + "#"})
	if err != nil || len(batches) == 0 {
		return MoveToMasterResult{}, false, err
	}
	out := MoveToMasterResult{Replayed: true}
	total := decimal.Zero
	for _, b := range batches {
		rows, err := q.ListBatchTransfers(ctx, foliosdb.ListBatchTransfersParams{PropertyID: propertyID, BatchID: b.ID})
		if err != nil {
			return MoveToMasterResult{}, false, err
		}
		res := TransferResult{BatchID: b.ID, Kind: b.Kind, Scope: b.Scope, Replayed: true, Moved: make([]TransferredItem, 0, len(rows))}
		for _, r := range rows {
			res.Moved = append(res.Moved, TransferredItem{TransferID: r.ID, FromFolioID: r.FromFolioID, ToFolioID: r.ToFolioID, OutItemID: r.OutItemID, InItemID: r.InItemID, Amount: fixed(r.Amount, decimals)})
			total = total.Add(r.Amount)
		}
		out.Batches = append(out.Batches, res)
		out.Moved += len(rows)
	}
	out.Total = fixed(total, decimals)
	return out, true, nil
}
