package folios

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Transfer of a charge between the folios of one reservation (docs/architecture/18-architecture-decisions.md section 3.7, rule 6). It is a correction: the charge is reversed on the
// source folio and charged again on the target folio as a copy of the original, not recomputed, so the amounts, the components, the revenue account, the department and the service
// date are exactly those of the original and tax rounding cannot drift. On the day close the two entries net to zero in every account and department. The new item has the source
// TRANSFER; both items carry the reference FOLIO_TRANSFER with the id of the original, and the reversal points at it through reverses_item_id.

// ReferenceFolioTransfer is the reference type of the two items of a transfer.
const ReferenceFolioTransfer = "FOLIO_TRANSFER"

// TransferItemInput moves a charge to another folio of the reservation. Like every correction it needs a reason and an approval.
type TransferItemInput struct {
	FolioID  int64              `json:"folio_id"`
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// TransferItemResult is the reversal on the source folio and the new charge on the target folio, each with the balance of its folio.
type TransferItemResult struct {
	Reversal ItemResult `json:"reversal"`
	Charge   ItemResult `json:"charge"`
}

func errTransferInvalid(msg string) *apperr.Error {
	return apperr.Conflict("FOLIO_TRANSFER_INVALID", msg)
}

// TransferItem moves the charge itemID to the folio in.FolioID (permission folio.reverse and an approval). Both folios must be OPEN, of the same reservation, and the target must
// belong to a stay. Only a CHARGE that has not been reversed can move; a payment is voided or refunded, never transferred. A room night keeps its place in the posting register: the
// night stays posted once, so the night audit never charges it again.
func (s *Service) TransferItem(ctx context.Context, propertyID, itemID int64, in TransferItemInput) (TransferItemResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return TransferItemResult{}, err
	}
	reason, fields := requireReason(in.Reason)
	if in.FolioID < 1 {
		fields = append(fields, fieldErr("folio_id", "REQUIRED", "the folio to move the charge to"))
	}
	if len(fields) > 0 {
		return TransferItemResult{}, apperr.Invalid("the transfer is invalid", fields...)
	}
	approval, err := s.iam.VerifyApproval(ctx, propertyID, in.Approval)
	if err != nil {
		return TransferItemResult{}, err
	}
	var out TransferItemResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		q := s.q(ctx)
		pre, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
		if err != nil {
			return orNotFound(err, errItemNotFound())
		}
		if pre.FolioID == in.FolioID {
			return errTransferInvalid("the charge is on that folio already")
		}
		ids := []int64{pre.FolioID, in.FolioID}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids); err != nil { // L4, ascending
			return mapNotFound(err, errFolioNotFound())
		}
		source, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: pre.FolioID})
		if err != nil {
			return orNotFound(err, errFolioNotFound())
		}
		target, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.FolioID})
		if err != nil {
			return orNotFound(err, errFolioNotFound())
		}
		if err := requireOpen(source); err != nil {
			return err
		}
		if err := requireOpen(target); err != nil {
			return err
		}
		if source.ReservationID != target.ReservationID {
			return errTransferInvalid("a charge moves only between the folios of one reservation")
		}
		if target.StayID == nil {
			return errTransferInvalid("a charge cannot move to the deposit folio of a reservation, which has no stay")
		}
		item, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID}) // after the locks: the current state
		if err != nil {
			return orNotFound(err, errItemNotFound())
		}
		if item.TransactionType != TypeCharge {
			return errTransferInvalid("only a charge can be transferred").WithContext("transaction_type", item.TransactionType)
		}
		if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &itemID}); err == nil {
			return apperr.Conflict("ALREADY_REVERSED", "the item has already been reversed")
		}
		night, err := q.GetPostingOfItem(ctx, foliosdb.GetPostingOfItemParams{PropertyID: propertyID, ItemID: itemID})
		isNight := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		ref := ReferenceFolioTransfer
		refID := strconv.FormatInt(itemID, 10)
		rev, err := s.posting(p, propertyID, day.BusinessDate, source).reverseAs(ctx, item, reason, approval, &ref, &refID) // flips the register row of a room night
		if err != nil {
			return err
		}
		comps, err := q.ListItemComponents(ctx, foliosdb.ListItemComponentsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: []int64{itemID}})
		if err != nil {
			return err
		}
		copied := make([]foliosdb.InsertFolioItemComponentParams, len(comps))
		for i, c := range comps {
			copied[i] = foliosdb.InsertFolioItemComponentParams{
				ComponentType: c.ComponentType, TaxID: c.TaxID, ServiceChargeID: c.ServiceChargeID, Code: c.Code, Name: c.Name, Rate: c.Rate,
				TaxOnService: c.TaxOnService, BaseAmount: c.BaseAmount, Amount: c.Amount, Sequence: c.Sequence,
			}
		}
		moved, err := s.posting(p, propertyID, day.BusinessDate, target).insert(ctx, itemSpec{
			transactionType: TypeCharge, chargeCodeID: item.ChargeCodeID, copiesItemID: &itemID, stayRoomID: item.StayRoomID, source: "TRANSFER",
			referenceType: &ref, referenceID: &refID, serviceDate: item.ServiceDate, description: item.Description, reason: &reason, components: copied,
			amounts: itemAmounts{
				quantity: item.Quantity, unitPrice: item.UnitPrice, priceMode: item.PriceMode, base: item.BaseAmount, discount: item.DiscountAmount, net: item.NetAmount,
				rounding: item.RoundingAdjustment, service: item.ServiceChargeTotal, tax: item.TaxTotal, debit: item.Debit, credit: item.Credit,
			},
		})
		if err != nil {
			return err
		}
		if isNight {
			// the night stays posted, once, for the stay that earned it: the register row of the original was flipped by the reversal, and the new item takes its place
			if err := q.InsertTransferPosting(ctx, foliosdb.InsertTransferPostingParams{
				TenantID: p.TenantID, PropertyID: propertyID, StayID: night.StayID, StayRoomID: night.StayRoomID, ServiceDate: night.ServiceDate,
				ChargeCodeID: night.ChargeCodeID, FolioItemID: moved.ID, BusinessDate: day.BusinessDate, ActorID: p.ActorID(),
			}); err != nil {
				return err
			}
		}
		if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(),
			Action: "folio.item_transferred", EntityType: "folio_item", EntityID: itemID,
			New: map[string]any{"reversal_item_id": rev.ID, "new_item_id": moved.ID, "from_folio_id": source.ID, "to_folio_id": target.ID, "reason": reason,
				"actor": p.ActorID(), "approved_by": approval.UserID(), "room_night": isNight}}); err != nil {
			return err
		}
		if out.Reversal, err = s.itemResult(ctx, p.TenantID, propertyID, source.ID, rev.ID); err != nil {
			return err
		}
		out.Charge, err = s.itemResult(ctx, p.TenantID, propertyID, target.ID, moved.ID)
		return err
	})
	return out, err
}
