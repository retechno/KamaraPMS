package folios

import (
	"context"
	"strings"

	"kamarapms/internal/auditlabel"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/db"
)

// Transaction Group / Split Bill (docs/architecture/20-transaction-group.md).
//
// A transaction group is a presentation and operational grouping dimension within one folio. It is not a financial folio, does not create separate receivables, and does not affect GL
// ownership or folio balance. The group of a ledger line says under which heading of the bill it is shown and printed. Moving a line from one group to another changes the row of
// folio_item_groups and nothing else: no amount, account, department, tax or service snapshot, no payment allocation, no reversal, no posting and no journal. The folio balance stays
// debit minus credit of the whole folio (FolioTotals); no query of the money layer reads a group.

// GroupDefault is the group of every line that has not been moved: all existing lines and every new one.
const GroupDefault = "A"

// GroupCodes are the groups the screens offer. The table accepts any capital letter, so a fifth group is a change of this list and of the screens, not of the schema.
var GroupCodes = []string{"A", "B", "C", "D"}

// ValidGroup says whether code is one of the groups offered.
func ValidGroup(code string) bool {
	for _, c := range GroupCodes {
		if c == code {
			return true
		}
	}
	return false
}

// GroupInput moves a line to a group.
type GroupInput struct {
	GroupCode string `json:"group_code"`
}

// GroupResult says where a line is after a move. Changed is false when it was in that group already (nothing was written, nothing audited).
type GroupResult struct {
	ItemID    int64  `json:"item_id"`
	FolioID   int64  `json:"folio_id"`
	PaymentID *int64 `json:"payment_id"`
	GroupCode string `json:"group_code"`
	Previous  string `json:"previous_group_code"`
	Changed   bool   `json:"changed"`
}

func normalizeGroup(in GroupInput) (string, error) {
	code := strings.ToUpper(strings.TrimSpace(in.GroupCode))
	if code == "" {
		return "", apperr.Invalid("the group is invalid", fieldErr("group_code", "REQUIRED", "one of "+strings.Join(GroupCodes, ", ")))
	}
	if !ValidGroup(code) {
		return "", apperr.Invalid("the group is invalid", fieldErr("group_code", "INVALID_VALUE", "one of "+strings.Join(GroupCodes, ", ")))
	}
	return code, nil
}

// SetItemGroup moves the ledger line itemID to a group (permission folio.post_charge: the staff who post to a bill arrange it).
//
// Eligibility, line by line, because the rules follow the ledger rules and are not a new set:
//   - CHARGE, ADJUSTMENT, PAYMENT and REFUND lines can be moved, whatever their source (a room night, a manual charge, a package or service line, a transferred line, a deposit): the
//     move changes no financial field, so the append-only ledger is not involved;
//   - a REVERSAL cannot be moved by itself (409 FOLIO_ITEM_GROUP_NOT_ALLOWED): it is shown in the group of the line it reverses, so that the pair always nets to zero in one group.
//     Moving the original moves the reversal with it;
//   - the folio must be OPEN: a closed folio is a final document and its print does not change (409 FOLIO_CLOSED);
//   - a payment stays on its folio. This moves nothing between folios (a charge moves between folios by the transfer of Architecture 18, which is a correction and stays separate).
//
// It is one transaction: the business day is share-locked (L1), the folio row is locked FOR UPDATE (L4) so that two moves of one bill, or a move and a posting, take turns, and the
// audit entry is written in the same transaction with the old and the new group.
func (s *Service) SetItemGroup(ctx context.Context, propertyID, itemID int64, in GroupInput) (GroupResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioPostCharge)
	if err != nil {
		return GroupResult{}, err
	}
	code, err := normalizeGroup(in)
	if err != nil {
		return GroupResult{}, err
	}
	var out GroupResult
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
		folio, err := s.lockFolio(ctx, p.TenantID, propertyID, pre.FolioID) // L4
		if err != nil {
			return err
		}
		if err := requireOpen(folio); err != nil {
			return err
		}
		if pre.TransactionType == TypeReversal {
			return apperr.Conflict("FOLIO_ITEM_GROUP_NOT_ALLOWED", "a reversal is shown in the group of the line it reverses: move that line").
				WithContext("reverses_item_id", pre.ReversesItemID)
		}
		old, err := q.GetItemGroup(ctx, foliosdb.GetItemGroupParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: itemID})
		if err != nil {
			return orNotFound(err, errItemNotFound())
		}
		out = GroupResult{ItemID: itemID, FolioID: pre.FolioID, PaymentID: pre.PaymentID, GroupCode: code, Previous: old}
		if old == code {
			return nil
		}
		if err := q.SetItemGroup(ctx, foliosdb.SetItemGroupParams{TenantID: p.TenantID, PropertyID: propertyID, FolioItemID: itemID, GroupCode: code, ActorID: p.ActorID()}); err != nil {
			return err
		}
		out.Changed = true
		return s.audit.Write(ctx, auditEntry(p, propertyID, day.BusinessDate, "folio_item.group_changed", "folio_item", itemID, auditlabel.FolioOfItem(ctx, propertyID, itemID),
			map[string]any{"group_code": old}, map[string]any{"group_code": code, "folio_id": pre.FolioID, "payment_id": pre.PaymentID, "transaction_type": pre.TransactionType}))
	})
	return out, err
}

// SetPaymentGroup moves the ledger line of a payment or refund to a group. A payment is a ledger line, so this is SetItemGroup on its line; the payment stays on its folio.
func (s *Service) SetPaymentGroup(ctx context.Context, propertyID, paymentID int64, in GroupInput) (GroupResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioPostCharge)
	if err != nil {
		return GroupResult{}, err
	}
	line, err := s.q(ctx).GetItemOfPayment(ctx, foliosdb.GetItemOfPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, PaymentID: &paymentID})
	if err != nil {
		return GroupResult{}, orNotFound(err, errPaymentNotFound())
	}
	return s.SetItemGroup(ctx, propertyID, line.ID, in)
}
