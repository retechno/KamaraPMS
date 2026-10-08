package folios

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Manual cancellation fee and no-show fee (audit F-08, docs/architecture/21-cancellation-and-no-show-fee.md).
//
// A fee is an ordinary charge: it is posted by the same posting path as any manual charge (posting.postCharge: the charge calculation engine, the charge code snapshot, tax and service
// charge, department, business date, journal at the day close, audit), to the charge code CANCEL_FEE or NO_SHOW_FEE of the property. Nothing here computes an amount: the person gives
// it. The cancellation policy of a rate plan stays informational text, and nothing posts a fee by itself (not the cancellation, not the no-show, not the night audit).
//
// The same event is never charged twice. The event is identified by the facts of the reservation, not by the amount: a cancellation by the time the reservation was cancelled
// (reservations.cancelled_at, which a second cancellation after a reinstatement sets again), a no-show by the room line and the time it was marked (reservation_rooms.no_show_at). The
// charge carries that identity as its idempotency key, so the unique index folio_items_idempotency_uk (property, key) is the database's guarantee; the reservation row is locked
// first, so the second of two concurrent requests finds the first one's charge and is refused with FEE_ALREADY_POSTED instead of waiting for the index. A fee that was reversed is
// still the fee of that event: a replacement is a new posting by the correction mechanism that exists (an adjustment or a manual charge), not by posting the fee again.

// Fee types.
const (
	FeeCancel = "CANCEL_FEE"
	FeeNoShow = "NO_SHOW_FEE"
)

// FeeInput is the request to post a fee. ReservationRoomID is required for NO_SHOW_FEE (the room that did not arrive) and refused for CANCEL_FEE.
type FeeInput struct {
	Type              string `json:"type"`
	ReservationRoomID *int64 `json:"reservation_room_id"`
	Amount            string `json:"amount"`
	Reason            string `json:"reason"`
}

// FeeResult is the posted fee, the folio it went to and whether that folio was opened by it.
type FeeResult struct {
	Item         Item   `json:"item"`
	FolioID      int64  `json:"folio_id"`
	FolioNumber  string `json:"folio_number"`
	FolioCreated bool   `json:"folio_created"`
	FolioBalance string `json:"folio_balance"`
}

func errFeeAlreadyPosted(itemID, folioID int64) *apperr.Error {
	return apperr.Conflict("FEE_ALREADY_POSTED", "this fee was already posted for this cancellation or no-show: correct the posted fee instead of posting it again").
		WithContext("folio_item_id", itemID).WithContext("folio_id", folioID)
}

func errReservationNotFound() *apperr.Error {
	return apperr.NotFound("RESERVATION_NOT_FOUND", "the reservation does not exist in this property")
}

// PostReservationFee posts CANCEL_FEE or NO_SHOW_FEE for a reservation (folio.post_charge).
//
// The folio: the open folio of the reservation that has no stay (the folio deposits go to), created when there is none. A cancelled reservation and a room that did not arrive have no
// stay, and the routing of a charge to a company folio (billing instructions) exists only for a stay in house, so this is the folio the existing rules give; the fee is never put on the
// folio of another room that is in house.
//
// Lock order: the business day (share), the reservation and, for a no-show, its room line, then the folio (and the folio number from the sequence, last).
func (s *Service) PostReservationFee(ctx context.Context, propertyID, reservationID int64, in FeeInput) (FeeResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioPostCharge)
	if err != nil {
		return FeeResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return FeeResult{}, err
	}
	typ := strings.ToUpper(strings.TrimSpace(in.Type))
	var fields []apperr.FieldError
	switch typ {
	case FeeCancel:
		if in.ReservationRoomID != nil {
			fields = append(fields, fieldErr("reservation_room_id", "NOT_ALLOWED", "a cancellation fee is for the reservation, not for one room"))
		}
	case FeeNoShow:
		if in.ReservationRoomID == nil || *in.ReservationRoomID < 1 {
			fields = append(fields, fieldErr("reservation_room_id", "REQUIRED", "the room that did not arrive"))
		}
	default:
		fields = append(fields, fieldErr("type", "INVALID_VALUE", FeeCancel+" or "+FeeNoShow))
	}
	amount, perr := billingconfig.ParseUnitPrice(strings.TrimSpace(in.Amount), decimals)
	if perr != nil || !amount.IsPositive() {
		fields = append(fields, fieldErr("amount", "INVALID_AMOUNT", "an amount above zero with at most the currency's decimals"))
	}
	reason, rf := requireReason(in.Reason)
	fields = append(fields, rf...)
	if len(fields) > 0 {
		return FeeResult{}, apperr.Invalid("the fee is invalid", fields...)
	}

	var out FeeResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1; the trigger of migration 00065 is the safety net behind this
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if _, err := q.GetReservationForFee(ctx, foliosdb.GetReservationForFeeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: reservationID}); err != nil {
			return orNotFound(err, errReservationNotFound())
		}
		// L4: the reservation (and the room line of a no-show) serialises the fees of one event, as it serialises a deposit.
		if err := db.LockRows(ctx, db.Reservations, db.ForUpdate, propertyID, []int64{reservationID}); err != nil {
			return mapNotFound(err, errReservationNotFound())
		}
		res, err := q.GetReservationForFee(ctx, foliosdb.GetReservationForFeeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: reservationID}) // under the lock
		if err != nil {
			return err
		}
		var event, subject, refType string
		var subjectID int64
		switch typ {
		case FeeCancel:
			if res.Status != "CANCELLED" || res.CancelledAt == nil {
				return apperr.Conflict("RESERVATION_NOT_CANCELLED", "a cancellation fee is posted to a cancelled reservation").WithContext("status", res.Status)
			}
			subjectID, refType = reservationID, "RESERVATION"
			event, subject = strconv.FormatInt(res.CancelledAt.UnixMicro(), 10), "Cancellation fee "+res.ConfirmationNumber
		case FeeNoShow:
			lineID := *in.ReservationRoomID
			line, err := q.GetLineForFee(ctx, foliosdb.GetLineForFeeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
			if err != nil || line.ReservationID != reservationID {
				return apperr.NotFound("RESERVATION_ROOM_NOT_FOUND", "the room does not exist in this reservation")
			}
			if err := db.LockRows(ctx, db.ReservationRooms, db.ForUpdate, propertyID, []int64{lineID}); err != nil {
				return mapNotFound(err, apperr.NotFound("RESERVATION_ROOM_NOT_FOUND", "the room does not exist in this reservation"))
			}
			line, err = q.GetLineForFee(ctx, foliosdb.GetLineForFeeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
			if err != nil {
				return err
			}
			if line.Status != "NO_SHOW" || line.NoShowAt == nil {
				return apperr.Conflict("RESERVATION_ROOM_NOT_NO_SHOW", "a no-show fee is posted to a room that is marked as a no-show").WithContext("status", line.Status)
			}
			subjectID, refType = lineID, "RESERVATION_ROOM"
			event, subject = strconv.FormatInt(line.NoShowAt.UnixMicro(), 10), "No-show fee "+res.ConfirmationNumber
		}
		key := "fee:" + typ + ":" + strconv.FormatInt(subjectID, 10) + ":" + event // the identity of the event, 100 characters at most
		if it, err := q.GetFolioItemByKey(ctx, foliosdb.GetFolioItemByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, IdempotencyKey: &key}); err == nil {
			return errFeeAlreadyPosted(it.ID, it.FolioID)
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		code, err := q.GetChargeCodeByCode(ctx, foliosdb.GetChargeCodeByCodeParams{TenantID: p.TenantID, PropertyID: propertyID, Code: typ})
		if err != nil || !code.IsActive {
			return apperr.Conflict("FEE_CHARGE_CODE_UNAVAILABLE", "the charge code "+typ+" is missing or switched off in this property").WithContext("charge_code", typ)
		}

		_, err = q.FindUnlinkedOpenFolio(ctx, foliosdb.FindUnlinkedOpenFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ReservationID: reservationID})
		existed := err == nil // otherwise depositFolio opens the folio of the reservation
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		folio, err := s.depositFolio(ctx, p, propertyID, reservationID)
		if err != nil {
			return err
		}
		if err := requireOpen(folio); err != nil {
			return err
		}
		amt := amount
		item, err := s.posting(p, propertyID, day.BusinessDate, folio).postCharge(ctx, chargeCmd{
			chargeCodeID: code.ID, quantity: decimal.NewFromInt(1), unitPrice: &amt, serviceDate: day.BusinessDate, description: subject, key: key,
			reason: &reason, referenceType: &refType, referenceID: nullable(strconv.FormatInt(subjectID, 10)),
		})
		if err != nil {
			if apperr.IsCode(err, "DUPLICATE_REQUEST") { // the unique index is the last word
				return errFeeAlreadyPosted(0, folio.ID)
			}
			return err
		}
		action := "reservation.cancel_fee_posted"
		details := map[string]any{"type": typ, "reservation_id": reservationID, "amount": amt.String(), "reason": reason, "folio_id": folio.ID, "folio_number": folio.FolioNumber,
			"folio_created": !existed, "folio_item_id": item.ID, "charge_code_id": code.ID, "debit": item.Debit.String()}
		if typ == FeeNoShow {
			action = "reservation.no_show_fee_posted"
			details["reservation_room_id"] = subjectID
		}
		if err := s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, action, "reservation", reservationID, nil, details)); err != nil {
			return err
		}
		res2, err := s.itemResult(ctx, p.TenantID, propertyID, folio.ID, item.ID)
		if err != nil {
			return err
		}
		out = FeeResult{Item: res2.Item, FolioID: folio.ID, FolioNumber: folio.FolioNumber, FolioCreated: !existed, FolioBalance: res2.FolioBalance}
		return nil
	})
	return out, err
}
