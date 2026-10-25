package banquet

import (
	"context"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/folios"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// TransferToGroup hands the bill of an event to the MASTER folio of a group, by items or by balance (36 4.8). An event that is not linked is linked first and then transferred, in one use case: the
// transfer is always of the scope EVENT_GROUP and never needs folio.transfer_any. The ledger side (the batch, the reversals and the copies) is folios', with its own trail and return.
// Order of locks: the business day, the two folios ascending (42), the group (share) and the event (45), then the sequence if a number is needed.
func (s *Service) TransferToGroup(ctx context.Context, propertyID, eventID int64, key string, in TransferInput) (folios.TransferResult, error) {
	p, err := s.need(ctx, propertyID, auth.PermBanquetManage)
	if err != nil {
		return folios.TransferResult{}, err
	}
	var f []apperr.FieldError
	if in.GroupID < 1 {
		f = append(f, fieldErr("group_id", "REQUIRED", "the group whose master folio receives the bill"))
	}
	if in.Mode != "ITEMS" && in.Mode != "BALANCE" {
		f = append(f, fieldErr("mode", "INVALID_VALUE", "ITEMS or BALANCE"))
	}
	if strings.TrimSpace(in.Reason) == "" {
		f = append(f, fieldErr("reason", "REQUIRED", "a reason of 1 to 200 characters"))
	}
	if len(f) > 0 {
		return folios.TransferResult{}, invalid("transfer", f...)
	}
	q := s.q(ctx)
	hdr, err := q.EventMoney(ctx, banquetdb.EventMoneyParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID})
	if err != nil {
		return folios.TransferResult{}, orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
	}
	if hdr.BookingGroupID != nil && *hdr.BookingGroupID != in.GroupID {
		return folios.TransferResult{}, apperr.Conflict("EVENT_GROUP_MISMATCH", "the event is linked to another group: unlink it first").WithContext("booking_group_id", *hdr.BookingGroupID)
	}
	src, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, eventID)
	if err != nil {
		return folios.TransferResult{}, err
	}
	if src == nil {
		return folios.TransferResult{}, notFound("FOLIO_NOT_FOUND", "folio of the event")
	}
	master, err := q.GroupMasterFolio(ctx, banquetdb.GroupMasterFolioParams{TenantID: p.TenantID, PropertyID: propertyID, GroupID: &in.GroupID})
	if err != nil {
		return folios.TransferResult{}, orNotFound(err, apperr.NotFound("GROUP_MASTER_NOT_FOUND", "the group has no open master folio"))
	}
	itemsIn := folios.TransferItemsInput{FolioID: master.ID, ItemIDs: in.ItemIDs, ChargeCodeID: in.ChargeCodeID, Reason: in.Reason}
	var amount decimal.Decimal
	if in.Mode == "ITEMS" {
		if amount, err = s.folios.TransferItemsAmount(ctx, propertyID, src.ID, itemsIn); err != nil {
			return folios.TransferResult{}, err
		}
	} else if amount, err = s.folios.ParseTransferAmount(ctx, propertyID, in.Amount); err != nil {
		return folios.TransferResult{}, err
	}
	approval, err := s.folios.TransferEventApproval(ctx, propertyID, in.Approval, amount)
	if err != nil {
		return folios.TransferResult{}, err
	}
	itemsIn.Approval, itemsIn.Reason = in.Approval, in.Reason
	var out folios.TransferResult
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		ids := []int64{src.ID, master.ID}
		if ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
		if err := s.folios.LockFolios(ctx, propertyID, db.ForUpdate, ids...); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.Groups, db.ForShare, propertyID, []int64{in.GroupID}); err != nil {
			return invalidCode("GROUP_NOT_FOUND", "group_id", "no such group in this property")
		}
		if err := db.LockRows(ctx, db.BanquetEvents, db.ForUpdate, propertyID, []int64{eventID}); err != nil {
			return orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
		}
		cur, err := q.EventBookingGroup(ctx, banquetdb.EventBookingGroupParams{PropertyID: propertyID, ID: eventID})
		if err != nil {
			return orNotFound(err, notFound("EVENT_NOT_FOUND", "event"))
		}
		again, err := s.folios.EventFolioOf(ctx, p.TenantID, propertyID, eventID)
		if err != nil {
			return err
		}
		if again == nil || again.ID != src.ID {
			return apperr.Conflict("VERSION_CONFLICT", "the folio of the event changed: ask again")
		}
		switch {
		case cur != nil && *cur != in.GroupID:
			return apperr.Conflict("EVENT_GROUP_MISMATCH", "the event is linked to another group: unlink it first")
		case cur == nil:
			if err := q.LinkEventGroup(ctx, banquetdb.LinkEventGroupParams{TenantID: p.TenantID, PropertyID: propertyID, ID: eventID, GroupID: &in.GroupID, ActorID: p.ActorID()}); err != nil {
				return err
			}
			if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "banquet.group_linked", "banquet_event", eventID, hdr.Number, map[string]any{"booking_group_id": nil}, map[string]any{"booking_group_id": in.GroupID, "by": "transfer"})); err != nil {
				return err
			}
		}
		if in.Mode == "ITEMS" {
			out, err = s.folios.TransferEventItems(ctx, propertyID, bd, src.ID, master.ID, itemsIn, approval)
		} else {
			out, err = s.folios.TransferEventBalance(ctx, propertyID, bd, src.ID, master.ID, key, folios.TransferBalanceInput{FolioID: master.ID, Amount: in.Amount, Reason: in.Reason, Approval: in.Approval}, approval)
		}
		return err
	})
	return out, err
}
