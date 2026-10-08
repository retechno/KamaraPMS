package folios

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The correction of the rate of a room night that is already charged (the in-house rate edit). The charge is never touched: an ADJUSTMENT for the difference is posted on the folio the
// night was charged to, dated the open business day, with the reason and an approval, calculated by the same engine as every adjustment (so tax and service follow the charge code).

// NightAdjustment is the rate difference of one charged room night. Delta is signed and in PriceMode terms (the mode the night was charged in).
type NightAdjustment struct {
	ItemID    int64 // the room charge of the night
	Delta     decimal.Decimal
	PriceMode string
}

// NightAdjusted is what an adjustment of a night created.
type NightAdjusted struct {
	ItemID           int64 `json:"item_id"`
	FolioID          int64 `json:"folio_id"`
	AdjustmentItemID int64 `json:"adjustment_item_id"`
}

// VerifyApproval checks the credentials of an approver of a correction (correction.approve) for a caller that corrects a charge itself.
func (s *Service) VerifyApproval(ctx context.Context, propertyID int64, in *iam.ApprovalInput) (iam.Approval, error) {
	return s.iam.VerifyApproval(ctx, propertyID, in)
}

// VerifyApprovalFor checks the credentials of an approver who must hold perm (a lower rate needs reservation.override_rate_approve).
func (s *Service) VerifyApprovalFor(ctx context.Context, propertyID int64, in *iam.ApprovalInput, perm auth.Permission) (iam.Approval, error) {
	return s.iam.VerifyApprovalFor(ctx, propertyID, in, perm)
}

// AdjustChargedNights posts the adjustments of room nights in the caller's transaction (it needs no permission of its own: the front desk use case was authorized, and the approval
// is the one of a correction). The folios of the items are locked first, FOR UPDATE in ascending id (L4), then each adjustment is posted; a folio that is closed refuses
// (FOLIO_CLOSED) and nothing is written for it. The business day must already be share-locked; bd is that day.
func (s *Service) AdjustChargedNights(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, reason string, approval iam.Approval, adj []NightAdjustment) ([]NightAdjusted, error) {
	q := s.q(ctx)
	items := make([]foliosdb.FolioItem, len(adj))
	var ids []int64
	seen := map[int64]bool{}
	for i, a := range adj {
		it, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: a.ItemID})
		if err != nil {
			return nil, orNotFound(err, errItemNotFound())
		}
		items[i] = it
		if !seen[it.FolioID] {
			seen[it.FolioID] = true
			ids = append(ids, it.FolioID)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids); err != nil { // L4, ascending, all of them before anything is written
		return nil, mapNotFound(err, errFolioNotFound())
	}
	out := make([]NightAdjusted, 0, len(adj))
	for i, a := range adj {
		folio, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: items[i].FolioID})
		if err != nil {
			return nil, orNotFound(err, errFolioNotFound())
		}
		if err := requireOpen(folio); err != nil {
			return nil, err
		}
		if items[i].ChargeCodeID == nil {
			return nil, apperr.Conflict("ITEM_NOT_ADJUSTABLE", "the item has no charge code")
		}
		if items[i].PriceMode != a.PriceMode {
			return nil, apperr.Conflict("RATE_MODE_CHANGED", "the night was charged in another price mode than its new rate: it cannot be corrected by an adjustment").
				WithContext("charged_mode", items[i].PriceMode).WithContext("new_mode", a.PriceMode)
		}
		mode, fe := priceModeOf(&a.PriceMode)
		if fe != nil {
			return nil, apperr.Invalid("the adjustment is invalid", *fe)
		}
		related := a.ItemID
		adjItem, err := s.posting(p, propertyID, bd, folio).postAdjustment(ctx, adjustmentCmd{
			chargeCodeID: *items[i].ChargeCodeID, amount: a.Delta, priceMode: mode, reason: reason, relatedItemID: &related, approval: approval,
		})
		if err != nil {
			return nil, err
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, bd, "folio.adjustment_posted", "folio", folio.ID, nil, map[string]any{
			"folio_item_id": adjItem.ID, "charge_code_id": *items[i].ChargeCodeID, "debit": adjItem.Debit.String(), "credit": adjItem.Credit.String(),
			"reason": reason, "actor": p.ActorID(), "approved_by": approval.UserID(), "related_item_id": a.ItemID, "source": "STAY_RATE_CHANGE",
		})); err != nil {
			return nil, err
		}
		out = append(out, NightAdjusted{ItemID: a.ItemID, FolioID: folio.ID, AdjustmentItemID: adjItem.ID})
	}
	return out, nil
}
