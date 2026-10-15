package folios

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// What the screens read of the transfers between folios (docs/architecture/30-group-folio.md, part B): the log of a folio, the folios a charge may go to, and the mark a line carries.

// ItemTransfer is the mark of a line of a folio that a transfer made or moved. Role is IN (the line came in from another folio), OUT (the line that leaves: the credit of a balance, or the
// reversal of a charge) or ORIGINAL (the charge that was moved). Hidden says the line is one half of a correction that sums to zero: the folio view shows it only when asked.
type ItemTransfer struct {
	TransferID int64    `json:"transfer_id"`
	Role       string   `json:"role"`
	Kind       string   `json:"kind"`
	IsReturn   bool     `json:"is_return"`
	Hidden     bool     `json:"hidden"`
	From       FolioRef `json:"from"`
	To         FolioRef `json:"to"`
	Origin     FolioRef `json:"origin"`
	Returned   bool     `json:"returned"`
}

// itemTransfers reads the marks of a set of lines in one query.
func (s *Service) itemTransfers(ctx context.Context, tenantID, propertyID int64, ids []int64) (map[int64]*ItemTransfer, error) {
	out := map[int64]*ItemTransfer{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).ListTransfersOfItems(ctx, foliosdb.ListTransfersOfItemsParams{TenantID: tenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return nil, err
	}
	rank := map[string]int{"IN": 1, "OUT": 2, "ORIGINAL": 3} // a line moved on is the original of the next transfer: that wins
	set := func(id int64, role string, hidden bool, r foliosdb.ListTransfersOfItemsRow) {
		if cur, ok := out[id]; ok && rank[cur.Role] >= rank[role] {
			return
		}
		out[id] = &ItemTransfer{
			TransferID: r.ID, Role: role, Kind: r.Kind, IsReturn: r.IsReturn, Hidden: hidden,
			From: FolioRef{ID: r.FromFolioID, Number: r.FromNumber, Room: r.FromRoom}, To: FolioRef{ID: r.ToFolioID, Number: r.ToNumber, Room: r.ToRoom},
			Origin: FolioRef{ID: r.OriginFolioID, Number: r.OriginNumber}, Returned: r.ReturnedByTransferID != nil,
		}
	}
	for _, r := range rows {
		set(r.InItemID, "IN", false, r)
		set(r.OutItemID, "OUT", r.Kind == KindItem, r)
		if r.OriginalItemID != nil {
			set(*r.OriginalItemID, "ORIGINAL", true, r)
		}
	}
	return out, nil
}

// FolioTransfer is one row of the log of a folio.
type FolioTransfer struct {
	ID                int64      `json:"id"`
	BatchID           int64      `json:"batch_id"`
	Kind              string     `json:"kind"`
	IsReturn          bool       `json:"is_return"`
	Scope             string     `json:"scope"`
	Reason            string     `json:"reason"`
	BusinessDate      civil.Date `json:"business_date"`
	CreatedAt         time.Time  `json:"created_at"`
	By                string     `json:"by"`
	ApprovedBy        string     `json:"approved_by"`
	From              FolioRef   `json:"from"`
	To                FolioRef   `json:"to"`
	Origin            FolioRef   `json:"origin"`
	Previous          FolioRef   `json:"previous"`
	Amount            string     `json:"amount"`
	Description       string     `json:"description"`
	ChargeCode        string     `json:"charge_code,omitempty"`
	OriginalItemID    *int64     `json:"original_item_id"`
	InItemID          int64      `json:"in_item_id"`
	OutItemID         int64      `json:"out_item_id"`
	ReturnsTransferID *int64     `json:"returns_transfer_id"`
	ReturnedByID      *int64     `json:"returned_by_transfer_id"`
	// Current is where the charge is now (the end of its chain: it may have moved on), HeadTransferID the transfer that brought it there, and Returnable says it can be sent back now.
	Current        *FolioRef `json:"current"`
	HeadTransferID int64     `json:"head_transfer_id"`
	// HeadPrevious is the folio the charge came from last: where a return "to the previous folio" sends it.
	HeadPrevious *FolioRef `json:"head_previous"`
	Returnable   bool      `json:"returnable"`
}

func (s *Service) userName(ctx context.Context, id *int64) string {
	if id == nil {
		return ""
	}
	n, err := s.q(ctx).GetUserName(ctx, *id)
	if err != nil {
		return ""
	}
	return n
}

// ListTransfers lists the transfers of a folio (folio.read): direction out (what left it), in (what came in) or all. Each says where the charge is now and whether it can be sent back.
func (s *Service) ListTransfers(ctx context.Context, propertyID, folioID int64, direction string, limit int) ([]FolioTransfer, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioRead)
	if err != nil {
		return nil, err
	}
	if direction == "" {
		direction = "all"
	}
	if direction != "out" && direction != "in" && direction != "all" {
		return nil, apperr.Invalid("the filter is invalid", fieldErr("direction", "INVALID_VALUE", "out, in or all"))
	}
	if _, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID}); err != nil {
		return nil, orNotFound(err, errFolioNotFound())
	}
	q := s.q(ctx)
	rows, err := q.ListFolioTransfers(ctx, foliosdb.ListFolioTransfersParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID, Direction: direction, RowLimit: rowLimit(limit)})
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	numbers := map[int64]FolioRef{}
	ref := func(id int64) (FolioRef, error) {
		if r, ok := numbers[id]; ok {
			return r, nil
		}
		f, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return FolioRef{}, err
		}
		pl, err := q.GetFolioPlace(ctx, foliosdb.GetFolioPlaceParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		r := FolioRef{ID: id, Number: f.FolioNumber}
		if err == nil {
			r.Room = pl.RoomNumber
		}
		numbers[id] = r
		return r, nil
	}
	out := make([]FolioTransfer, len(rows))
	for i, r := range rows {
		t := FolioTransfer{
			ID: r.ID, BatchID: r.BatchID, Kind: r.Kind, IsReturn: r.IsReturn, Scope: r.Scope, Reason: r.Reason, BusinessDate: r.BusinessDate, CreatedAt: r.CreatedAt,
			By: s.userName(ctx, r.CreatedBy), ApprovedBy: s.userName(ctx, r.ApprovedBy),
			From: FolioRef{ID: r.FromFolioID, Number: r.FromNumber, Room: r.FromRoom}, To: FolioRef{ID: r.ToFolioID, Number: r.ToNumber, Room: r.ToRoom},
			Origin: FolioRef{ID: r.OriginFolioID, Number: r.OriginNumber}, Previous: FolioRef{ID: r.PreviousFolioID, Number: r.PreviousNumber},
			Amount: fixed(r.Amount, decimals), Description: r.Description, ChargeCode: r.ChargeCode, OriginalItemID: r.OriginalItemID, InItemID: r.InItemID, OutItemID: r.OutItemID,
			ReturnsTransferID: r.ReturnsTransferID, ReturnedByID: r.ReturnedByTransferID, HeadTransferID: r.ID,
		}
		// follow a charge that moved on: the end of the chain is where it is now
		cur := foliosdb.FolioTransfer{ID: r.ID, ToFolioID: r.ToFolioID, PreviousFolioID: r.PreviousFolioID, InItemID: r.InItemID, OriginalItemID: r.OriginalItemID}
		if r.OriginalItemID != nil {
			for n := 0; n < 50; n++ {
				next, err := q.GetTransferByOriginalItem(ctx, foliosdb.GetTransferByOriginalItemParams{PropertyID: propertyID, ItemID: &cur.InItemID})
				if err != nil {
					break
				}
				cur = next
			}
		}
		here, err := ref(cur.ToFolioID)
		if err != nil {
			return nil, err
		}
		prev, err := ref(cur.PreviousFolioID)
		if err != nil {
			return nil, err
		}
		t.Current, t.HeadTransferID, t.HeadPrevious = &here, cur.ID, &prev
		headReturned := false
		if _, err := q.GetTransferReturnedBy(ctx, foliosdb.GetTransferReturnedByParams{PropertyID: propertyID, TransferID: &cur.ID}); err == nil {
			headReturned = true
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
		holder, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: cur.ToFolioID})
		if err != nil {
			return nil, err
		}
		// a return is not returned again from the folio it came back to; a charge that was reversed after it arrived cannot be sent back
		t.Returnable = holder.Status == "OPEN" && !headReturned && !r.IsReturn && cur.ToFolioID != r.FromFolioID
		out[i] = t
	}
	return out, nil
}

// TransferTarget is a folio a charge or a balance may be sent to.
type TransferTarget struct {
	ID                 int64  `json:"id"`
	FolioNumber        string `json:"folio_number"`
	FolioType          string `json:"folio_type"`
	ConfirmationNumber string `json:"confirmation_number"`
	Room               string `json:"room,omitempty"`
	Guest              string `json:"guest,omitempty"`
	Company            string `json:"company,omitempty"`
	Balance            string `json:"balance"`
	Scope              string `json:"scope"`
}

// TransferTargets finds the folios the folio fromID may send to (folio.reverse): the open folios of a stay, those of the same reservation and of the same group first, and every other open folio
// of the property only for who holds folio.transfer_any. q matches the folio number, the room or the name of the guest.
func (s *Service) TransferTargets(ctx context.Context, propertyID, fromID int64, q string, limit int) ([]TransferTarget, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return nil, err
	}
	db := s.q(ctx)
	from, err := db.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: fromID})
	if err != nil {
		return nil, orNotFound(err, errFolioNotFound())
	}
	var group *int64
	if from.FolioType == "MASTER" {
		group = from.BookingGroupID
	} else if from.ReservationID != nil {
		groups, err := db.ListReservationGroups(ctx, foliosdb.ListReservationGroupsParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: []int64{*from.ReservationID}})
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			group = g.BookingGroupID
		}
	}
	includeAny := s.authz.Require(ctx, propertyID, auth.PermFolioTransferAny) == nil
	q = strings.TrimSpace(q)
	rows, err := db.SearchTransferTargets(ctx, foliosdb.SearchTransferTargetsParams{
		TenantID: p.TenantID, PropertyID: propertyID, FromFolioID: fromID, FromReservationID: from.ReservationID, FromGroupID: group, FromIsMaster: from.FolioType == "MASTER", IncludeAny: includeAny,
		Q: nullable(q), RowLimit: rowLimit(limit),
	})
	if err != nil {
		return nil, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	out := make([]TransferTarget, len(rows))
	for i, r := range rows {
		scope := ScopeAny
		switch {
		case r.SameReservation:
			scope = ScopeReservation
		case r.GroupMaster:
			scope = ScopeGroupMaster
		case r.SameGroup:
			scope = ScopeGroupMember
		}
		out[i] = TransferTarget{
			ID: r.ID, FolioNumber: r.FolioNumber, FolioType: r.FolioType, ConfirmationNumber: r.ConfirmationNumber, Room: r.RoomNumber, Guest: guestOrGroup(r.GroupName, deref(r.FirstName), deref(r.LastName)),
			Company: r.CompanyName, Balance: fixed(r.Balance, decimals), Scope: scope,
		}
	}
	return out, nil
}

// guestOrGroup is what names a target on the screen: the guest of a folio of a stay, the name of the group for the master folio of a group.
func guestOrGroup(group, first, last string) string {
	if group != "" {
		return group
	}
	return strings.TrimSpace(first + " " + last)
}
