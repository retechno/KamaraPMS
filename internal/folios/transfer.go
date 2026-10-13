package folios

import (
	"context"
	"errors"
	"slices"
	"sort"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/auditlabel"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// Transfers between folios (docs/architecture/30-group-folio.md, part B).
//
// Charges move by a reversal on the folio they leave and a charge on the folio they go to, which is a copy of the original, not recomputed, so the amounts, the components, the revenue account,
// the department and the service date are exactly those of the original and tax rounding cannot drift (docs/architecture/18-architecture-decisions.md section 3.7, rule 6). On the day close the
// two entries net to zero in every account and department. A balance moves by two lines of the system charge code TRF: an adjustment (a credit) on the folio it leaves and a charge on the folio
// it goes to; the code has no tax and no service charge, so the same charge engine returns no component for it, and its account is the system key FOLIO_TRANSFER, which comes to zero. Both
// kinds are written to an append-only log (folio_transfers) that says where each line came from, and either can be sent back with a return.

// ReferenceFolioTransfer is the reference type of the two items of a transfer of a charge; ReferenceBalanceTransfer the one of the two lines of a transfer of a balance (its id is the batch).
const (
	ReferenceFolioTransfer   = "FOLIO_TRANSFER"
	ReferenceBalanceTransfer = "FOLIO_BALANCE_TRANSFER"
)

// Where a transfer may go (see scopeOf), and what it moves.
const (
	ScopeReservation = "RESERVATION"
	ScopeGroupMember = "GROUP_MEMBER"
	ScopeGroupMaster = "GROUP_MASTER"
	ScopeAny         = "ANY"
	ScopeReturn      = "RETURN"

	KindItem    = "ITEM"
	KindBalance = "BALANCE"

	// MaxTransferItems is the most charges one request moves.
	MaxTransferItems = 200

	// ReturnToPrevious and ReturnToOrigin say where a return sends a charge: the folio it came from last, or the first folio it came from.
	ReturnToPrevious = "PREVIOUS"
	ReturnToOrigin   = "ORIGIN"
)

func errTransferInvalid(msg string) *apperr.Error {
	return apperr.Conflict("FOLIO_TRANSFER_INVALID", msg)
}

func errTransferCodeNotManual(code string) *apperr.Error {
	return apperr.Conflict("TRANSFER_CODE_NOT_MANUAL", "the code "+code+" is posted by the transfer of a balance between folios, not by hand")
}

// refuseTransferLine refuses a line of the code TRF where a charge is reversed or moved: such a line goes back with a return.
func (s *Service) refuseTransferLine(ctx context.Context, propertyID int64, chargeCodeID *int64) error {
	if chargeCodeID == nil {
		return nil
	}
	ct, err := s.q(ctx).GetChargeTypeOfCode(ctx, foliosdb.GetChargeTypeOfCodeParams{PropertyID: propertyID, ID: *chargeCodeID})
	if err != nil {
		return orNotFound(err, nil)
	}
	if ct == "TRANSFER" {
		return errTransferInvalid("a line of the transfer code TRF cannot be reversed or moved as a charge: send the balance back with a return")
	}
	return nil
}

// FolioRef names a folio with the room of its stay.
type FolioRef struct {
	ID     int64  `json:"id"`
	Number string `json:"number"`
	Room   string `json:"room,omitempty"`
}

// FolioBalance is the balance of a folio after a transfer.
type FolioBalance struct {
	FolioID int64  `json:"folio_id"`
	Balance string `json:"balance"`
}

// TransferredItem is one moved charge, or one pair of TRF lines.
type TransferredItem struct {
	TransferID     int64  `json:"transfer_id"`
	FromFolioID    int64  `json:"from_folio_id"`
	ToFolioID      int64  `json:"to_folio_id"`
	OriginalItemID *int64 `json:"original_item_id"`
	OutItemID      int64  `json:"out_item_id"`
	InItemID       int64  `json:"in_item_id"`
	Amount         string `json:"amount"`
}

// TransferResult is what a transfer or a return moved, with the balance of every folio it touched.
type TransferResult struct {
	BatchID  int64             `json:"batch_id"`
	Kind     string            `json:"kind"`
	Scope    string            `json:"scope"`
	Moved    []TransferredItem `json:"transfers"`
	Balances []FolioBalance    `json:"balances"`
	Replayed bool              `json:"replayed"`
}

// scopeOf says what a transfer between two folios is. Both must be OPEN (checked by the caller) and the target must belong to a stay: the deposit folio of a reservation has none.
// A folio of the same reservation, or of a reservation of the same group, is related; anything else is ANY, which needs folio.transfer_any on the person and on the approver.
// The master folio of a group (part C) is not there yet: a folio of that type is refused until it is.
func (s *Service) scopeOf(ctx context.Context, tenantID, propertyID int64, src, tgt foliosdb.Folio) (string, error) {
	if src.FolioType == "MASTER" || tgt.FolioType == "MASTER" {
		return "", errTransferInvalid("the master folio of a group takes transfers with the master folio of part C")
	}
	if tgt.StayID == nil {
		return "", errTransferInvalid("a charge cannot move to the deposit folio of a reservation, which has no stay")
	}
	if src.ReservationID == tgt.ReservationID {
		return ScopeReservation, nil
	}
	rows, err := s.q(ctx).ListReservationGroups(ctx, foliosdb.ListReservationGroupsParams{TenantID: tenantID, PropertyID: propertyID, Ids: []int64{src.ReservationID, tgt.ReservationID}})
	if err != nil {
		return "", err
	}
	groups := map[int64]*int64{}
	for _, r := range rows {
		groups[r.ID] = r.BookingGroupID
	}
	if a, b := groups[src.ReservationID], groups[tgt.ReservationID]; a != nil && b != nil && *a == *b {
		return ScopeGroupMember, nil
	}
	return ScopeAny, nil
}

// transferApproval verifies the approval of a transfer, before the transaction (a wrong password is counted by iam, which a rolled back transaction would undo). A transfer to an unrelated
// folio needs folio.transfer_any on the person and on the approver; the others the approval every correction needs.
func (s *Service) transferApproval(ctx context.Context, propertyID int64, scope string, in *iam.ApprovalInput) (iam.Approval, error) {
	if scope == ScopeAny {
		if err := s.authz.Require(ctx, propertyID, auth.PermFolioTransferAny); err != nil {
			return iam.Approval{}, err
		}
		return s.iam.VerifyApprovalFor(ctx, propertyID, in, auth.PermFolioTransferAny)
	}
	return s.iam.VerifyApproval(ctx, propertyID, in)
}

// preScope classifies a pair of folios as they are now, without a lock, to know what approval to ask for. The scope is decided again under the locks.
func (s *Service) preScope(ctx context.Context, tenantID, propertyID, srcID, tgtID int64) (string, error) {
	q := s.q(ctx)
	src, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: srcID})
	if err != nil {
		return "", orNotFound(err, errFolioNotFound())
	}
	tgt, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: tgtID})
	if err != nil {
		return "", orNotFound(err, errFolioNotFound())
	}
	return s.scopeOf(ctx, tenantID, propertyID, src, tgt)
}

// lockPair locks two folios in ascending id (level 42) and reads them again: the state the use case decides on. Both must be OPEN.
func (s *Service) lockPair(ctx context.Context, tenantID, propertyID, aID, bID int64) (a, b foliosdb.Folio, err error) {
	ids := []int64{aID, bID}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, ids); err != nil {
		return a, b, mapNotFound(err, errFolioNotFound())
	}
	q := s.q(ctx)
	if a, err = q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: aID}); err != nil {
		return a, b, orNotFound(err, errFolioNotFound())
	}
	if b, err = q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: bID}); err != nil {
		return a, b, orNotFound(err, errFolioNotFound())
	}
	if err := requireOpen(a); err != nil {
		return a, b, err
	}
	return a, b, requireOpen(b)
}

// movement is what every line of one batch shares.
type movement struct {
	p          auth.Principal
	propertyID int64
	bd         civil.Date
	src, tgt   foliosdb.Folio
	batchID    int64
	approval   iam.Approval
	reason     string
	scope      string
}

func (s *Service) balances(ctx context.Context, propertyID int64, decimals int32, folioIDs ...int64) ([]FolioBalance, error) {
	slices.Sort(folioIDs)
	folioIDs = slices.Compact(folioIDs)
	out := make([]FolioBalance, 0, len(folioIDs))
	for _, id := range folioIDs {
		bal, _, err := s.balanceOf(ctx, propertyID, id)
		if err != nil {
			return nil, err
		}
		out = append(out, FolioBalance{FolioID: id, Balance: fixed(bal, decimals)})
	}
	return out, nil
}

// moveItem moves one charge from m.src to m.tgt: a reversal on the source, a copy of the original on the target, the log row. A room night keeps its place in the posting register, so the night
// audit never charges it again. returns is the transfer this one sends back, when it is a return.
func (s *Service) moveItem(ctx context.Context, m movement, item foliosdb.FolioItem, returns *int64) (TransferredItem, error) {
	q := s.q(ctx)
	if item.FolioID != m.src.ID {
		return TransferredItem{}, errTransferInvalid("the charge is not on that folio").WithContext("item_id", item.ID)
	}
	if item.TransactionType != TypeCharge {
		return TransferredItem{}, errTransferInvalid("only a charge can be transferred").WithContext("transaction_type", item.TransactionType).WithContext("item_id", item.ID)
	}
	if err := s.refuseTransferLine(ctx, m.propertyID, item.ChargeCodeID); err != nil {
		return TransferredItem{}, err
	}
	itemID := item.ID
	if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: m.propertyID, ItemID: &itemID}); err == nil {
		return TransferredItem{}, apperr.Conflict("ALREADY_REVERSED", "the item has already been reversed").WithContext("item_id", itemID)
	}
	night, err := q.GetPostingOfItem(ctx, foliosdb.GetPostingOfItemParams{PropertyID: m.propertyID, ItemID: itemID})
	isNight := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return TransferredItem{}, err
	}
	// Where the charge came from first: the log row that brought it here says, else this folio is the first.
	origin := m.src.ID
	if prev, err := q.GetTransferByInItem(ctx, foliosdb.GetTransferByInItemParams{PropertyID: m.propertyID, ItemID: itemID}); err == nil {
		origin = prev.OriginFolioID
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return TransferredItem{}, err
	}

	ref := ReferenceFolioTransfer
	refID := strconv.FormatInt(itemID, 10)
	rev, err := s.posting(m.p, m.propertyID, m.bd, m.src).reverseAs(ctx, item, m.reason, m.approval, &ref, &refID) // flips the register row of a room night
	if err != nil {
		return TransferredItem{}, err
	}
	comps, err := q.ListItemComponents(ctx, foliosdb.ListItemComponentsParams{TenantID: m.p.TenantID, PropertyID: m.propertyID, ItemIds: []int64{itemID}})
	if err != nil {
		return TransferredItem{}, err
	}
	copied := make([]foliosdb.InsertFolioItemComponentParams, len(comps))
	for i, c := range comps {
		copied[i] = foliosdb.InsertFolioItemComponentParams{
			ComponentType: c.ComponentType, TaxID: c.TaxID, ServiceChargeID: c.ServiceChargeID, Code: c.Code, Name: c.Name, Rate: c.Rate,
			TaxOnService: c.TaxOnService, BaseAmount: c.BaseAmount, Amount: c.Amount, Sequence: c.Sequence,
		}
	}
	moved, err := s.posting(m.p, m.propertyID, m.bd, m.tgt).insert(ctx, itemSpec{
		transactionType: TypeCharge, chargeCodeID: item.ChargeCodeID, copiesItemID: &itemID, stayRoomID: item.StayRoomID, source: "TRANSFER",
		referenceType: &ref, referenceID: &refID, serviceDate: item.ServiceDate, description: item.Description, reason: &m.reason, components: copied,
		amounts: itemAmounts{
			quantity: item.Quantity, unitPrice: item.UnitPrice, priceMode: item.PriceMode, base: item.BaseAmount, discount: item.DiscountAmount, net: item.NetAmount,
			rounding: item.RoundingAdjustment, service: item.ServiceChargeTotal, tax: item.TaxTotal, debit: item.Debit, credit: item.Credit,
		},
	})
	if err != nil {
		return TransferredItem{}, err
	}
	if isNight {
		// the night stays posted, once, for the stay that earned it: the register row of the original was flipped by the reversal, and the new item takes its place
		if err := q.InsertTransferPosting(ctx, foliosdb.InsertTransferPostingParams{
			TenantID: m.p.TenantID, PropertyID: m.propertyID, StayID: night.StayID, StayRoomID: night.StayRoomID, ServiceDate: night.ServiceDate,
			ChargeCodeID: night.ChargeCodeID, FolioItemID: moved.ID, BusinessDate: m.bd, ActorID: m.p.ActorID(),
		}); err != nil {
			return TransferredItem{}, err
		}
	}
	row, err := q.InsertTransfer(ctx, foliosdb.InsertTransferParams{
		TenantID: m.p.TenantID, PropertyID: m.propertyID, BatchID: m.batchID, FromFolioID: m.src.ID, ToFolioID: m.tgt.ID, OutItemID: rev.ID, InItemID: moved.ID, OriginalItemID: &itemID,
		Amount: item.Debit, OriginFolioID: origin, PreviousFolioID: m.src.ID, ReturnsTransferID: returns,
	})
	if err != nil {
		return TransferredItem{}, err
	}
	action := "folio.item_transferred"
	if returns != nil {
		action = "folio.transfer_returned"
	}
	if err := s.audit.Write(ctx, audit.Entry{TenantID: m.p.TenantID, PropertyID: &m.propertyID, BusinessDate: &m.bd, UserID: m.p.ActorID(),
		Action: action, EntityType: "folio_item", EntityID: itemID, EntityLabel: auditlabel.FolioOfItem(ctx, m.propertyID, itemID),
		New: map[string]any{"transfer_id": row.ID, "batch_id": m.batchID, "scope": m.scope, "reversal_item_id": rev.ID, "new_item_id": moved.ID, "from_folio_id": m.src.ID, "to_folio_id": m.tgt.ID,
			"origin_folio_id": origin, "returns_transfer_id": returns, "reason": m.reason, "actor": m.p.ActorID(), "approved_by": m.approval.UserID(), "room_night": isNight}}); err != nil {
		return TransferredItem{}, err
	}
	return TransferredItem{TransferID: row.ID, FromFolioID: m.src.ID, ToFolioID: m.tgt.ID, OriginalItemID: &itemID, OutItemID: rev.ID, InItemID: moved.ID, Amount: item.Debit.String()}, nil
}

// TransferItemsInput moves charges of the folio in the path to the folio FolioID: the charges named, or every charge of one charge code. Like every correction it needs a reason and an approval.
type TransferItemsInput struct {
	FolioID      int64              `json:"folio_id"`
	ItemIDs      []int64            `json:"item_ids"`
	ChargeCodeID *int64             `json:"charge_code_id"`
	Reason       string             `json:"reason"`
	Approval     *iam.ApprovalInput `json:"approval"`
}

// TransferItems moves several charges of one folio to another in one go: one reason, one approval, one transaction (folio.reverse; folio.transfer_any, and an approver who holds it, for a folio
// that is not of the same reservation or group). The charges are the ones named, or every charge of the code that can still move, found after the locks. Both folios must be OPEN. If one
// charge cannot move, none does.
func (s *Service) TransferItems(ctx context.Context, propertyID, sourceFolioID int64, in TransferItemsInput) (TransferResult, error) {
	return s.transferItems(ctx, propertyID, sourceFolioID, in, "")
}

func (s *Service) transferItems(ctx context.Context, propertyID, sourceFolioID int64, in TransferItemsInput, onlyScope string) (TransferResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return TransferResult{}, err
	}
	reason, fields := requireReason(in.Reason)
	if in.FolioID < 1 {
		fields = append(fields, fieldErr("folio_id", "REQUIRED", "the folio to move the charges to"))
	}
	if (len(in.ItemIDs) == 0) == (in.ChargeCodeID == nil) {
		fields = append(fields, fieldErr("item_ids", "INVALID_VALUE", "name the charges (item_ids) or one charge code (charge_code_id), not both and not neither"))
	}
	if len(in.ItemIDs) > MaxTransferItems {
		e := apperr.Invalid("the transfer is invalid", fieldErr("item_ids", "TOO_MANY_ITEMS", "at most 200 charges in a request"))
		e.Code = "TOO_MANY_ITEMS"
		return TransferResult{}, e
	}
	if len(fields) > 0 {
		return TransferResult{}, apperr.Invalid("the transfer is invalid", fields...)
	}
	if in.FolioID == sourceFolioID {
		return TransferResult{}, errTransferInvalid("the charges are on that folio already")
	}
	pre, err := s.preScope(ctx, p.TenantID, propertyID, sourceFolioID, in.FolioID)
	if err != nil {
		return TransferResult{}, err
	}
	if onlyScope != "" && pre != onlyScope {
		return TransferResult{}, errTransferInvalid("a charge moves only between the folios of one reservation")
	}
	approval, err := s.transferApproval(ctx, propertyID, pre, in.Approval)
	if err != nil {
		return TransferResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return TransferResult{}, err
	}
	var out TransferResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		src, tgt, err := s.lockPair(ctx, p.TenantID, propertyID, sourceFolioID, in.FolioID) // L4, ascending
		if err != nil {
			return err
		}
		scope, err := s.scopeOf(ctx, p.TenantID, propertyID, src, tgt)
		if err != nil {
			return err
		}
		if scope != pre && (scope == ScopeAny || pre == ScopeAny) {
			return apperr.Conflict("TRANSFER_SCOPE_CHANGED", "the folios changed meanwhile: ask again")
		}
		if onlyScope != "" && scope != onlyScope {
			return errTransferInvalid("a charge moves only between the folios of one reservation")
		}
		items, err := s.selectItems(ctx, p, propertyID, src, in)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		batch, err := q.InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
			TenantID: p.TenantID, PropertyID: propertyID, Kind: KindItem, FromFolioID: src.ID, ToFolioID: tgt.ID, Scope: scope, Reason: reason, BusinessDate: day.BusinessDate,
			ApprovedBy: approval.UserID(), ActorID: p.ActorID(),
		})
		if err != nil {
			return err
		}
		m := movement{p: p, propertyID: propertyID, bd: day.BusinessDate, src: src, tgt: tgt, batchID: batch.ID, approval: approval, reason: reason, scope: scope}
		out = TransferResult{BatchID: batch.ID, Kind: KindItem, Scope: scope, Moved: make([]TransferredItem, 0, len(items))}
		for _, it := range items {
			moved, err := s.moveItem(ctx, m, it, nil)
			if err != nil {
				return err
			}
			out.Moved = append(out.Moved, moved)
		}
		if err := s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &day.BusinessDate, UserID: p.ActorID(), Action: "folio.items_transferred",
			EntityType: "folio", EntityID: src.ID, EntityLabel: src.FolioNumber, New: map[string]any{"batch_id": batch.ID, "scope": scope, "to_folio_id": tgt.ID, "items": len(items),
				"reason": reason, "actor": p.ActorID(), "approved_by": approval.UserID()}}); err != nil {
			return err
		}
		out.Balances, err = s.balances(ctx, propertyID, decimals, src.ID, tgt.ID)
		return err
	})
	return out, err
}

// selectItems finds the charges a request moves, after the locks: the ones named (all of them on the source, all of them movable) or every movable charge of the code.
func (s *Service) selectItems(ctx context.Context, p auth.Principal, propertyID int64, src foliosdb.Folio, in TransferItemsInput) ([]foliosdb.FolioItem, error) {
	q := s.q(ctx)
	if in.ChargeCodeID != nil {
		if err := s.refuseTransferLine(ctx, propertyID, in.ChargeCodeID); err != nil {
			return nil, err
		}
		items, err := q.ListMovableItemsOfCode(ctx, foliosdb.ListMovableItemsOfCodeParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: src.ID, ChargeCodeID: in.ChargeCodeID, RowLimit: MaxTransferItems + 1})
		if err != nil {
			return nil, err
		}
		if len(items) > MaxTransferItems {
			e := apperr.Invalid("the transfer is invalid", fieldErr("charge_code_id", "TOO_MANY_ITEMS", "the code has more than 200 charges to move: name them"))
			e.Code = "TOO_MANY_ITEMS"
			return nil, e
		}
		if len(items) == 0 {
			return nil, apperr.Conflict("TRANSFER_SELECTION_EMPTY", "the folio has no charge of that code that can move")
		}
		return items, nil
	}
	ids := slices.Clone(in.ItemIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	items, err := q.ListItemsByIDs(ctx, foliosdb.ListItemsByIDsParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	var bad []int64
	found := map[int64]bool{}
	for _, it := range items {
		found[it.ID] = true
		itemID := it.ID
		if len(ids) == 1 && it.FolioID == src.ID && it.TransactionType == TypeCharge {
			if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &itemID}); err == nil {
				return nil, apperr.Conflict("ALREADY_REVERSED", "the item has already been reversed").WithContext("item_id", itemID)
			}
		}
		if it.FolioID != src.ID || it.TransactionType != TypeCharge {
			bad = append(bad, it.ID)
			continue
		}
		if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &itemID}); err == nil {
			bad = append(bad, it.ID)
		}
	}
	for _, id := range ids {
		if !found[id] {
			bad = append(bad, id)
		}
	}
	if len(bad) > 0 {
		slices.Sort(bad)
		return nil, errTransferInvalid("some of the items cannot move: each must be a charge of the folio that nothing has reversed").WithContext("item_ids", bad)
	}
	return items, nil
}

// TransferItemInput is the old request that moves one charge; it is TransferItems with one item, between folios of one reservation.
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

// TransferItem moves the charge itemID to the folio in.FolioID (folio.reverse and an approval). Both folios must be OPEN and of the same reservation. It is the transfer of
// charges for one item; the folios of other reservations are reached with TransferItems.
func (s *Service) TransferItem(ctx context.Context, propertyID, itemID int64, in TransferItemInput) (TransferItemResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return TransferItemResult{}, err
	}
	pre, err := s.q(ctx).GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
	if err != nil {
		return TransferItemResult{}, orNotFound(err, errItemNotFound())
	}
	res, err := s.transferItems(ctx, propertyID, pre.FolioID, TransferItemsInput{FolioID: in.FolioID, ItemIDs: []int64{itemID}, Reason: in.Reason, Approval: in.Approval}, ScopeReservation)
	if err != nil {
		return TransferItemResult{}, err
	}
	var out TransferItemResult
	moved := res.Moved[0]
	if out.Reversal, err = s.itemResult(ctx, p.TenantID, propertyID, moved.FromFolioID, moved.OutItemID); err != nil {
		return out, err
	}
	out.Charge, err = s.itemResult(ctx, p.TenantID, propertyID, moved.ToFolioID, moved.InItemID)
	return out, err
}

// ---------------------------------------------------------------------------------------------------------------
// The transfer of a balance

// TransferBalanceInput hands part of the balance of the folio in the path to the folio FolioID.
type TransferBalanceInput struct {
	FolioID  int64              `json:"folio_id"`
	Amount   string             `json:"amount"`
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// transferLine posts one line of the code TRF through the charge engine, which finds no tax or service charge for it: a charge (debit) when sign is positive, an adjustment (credit) when it is
// negative. Its account is the one of the system key FOLIO_TRANSFER now.
func (s *Service) transferLine(ctx context.Context, m movement, folio foliosdb.Folio, trf int64, account string, amount decimal.Decimal, sign int, description string) (foliosdb.FolioItem, error) {
	exclusive := chargecalc.Exclusive
	signed := amount
	if sign < 0 {
		signed = amount.Neg()
	}
	b, err := s.billing.Calculate(ctx, billingconfig.ChargeRequest{PropertyID: m.propertyID, ChargeCodeID: trf, Quantity: decimal.NewFromInt(1), UnitPrice: signed, PriceMode: &exclusive})
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	if len(b.TaxComponents) > 0 || len(b.ServiceComponents) > 0 {
		return foliosdb.FolioItem{}, apperr.Conflict("TRANSFER_CODE_HAS_RULES", "the code TRF has a tax or a service charge mapped to it: it must have none")
	}
	ref := ReferenceBalanceTransfer
	refID := strconv.FormatInt(m.batchID, 10)
	by := m.approval.UserID()
	spec := itemSpec{
		transactionType: TypeCharge, chargeCodeID: &trf, serviceDate: m.bd, description: description, amounts: amountsOf(b), components: componentsOf(b),
		reason: &m.reason, source: "TRANSFER", referenceType: &ref, referenceID: &refID, revenueAccount: &account,
	}
	if sign < 0 { // the credit is an adjustment: it carries the approver, like every correction (a charge carries none)
		spec.transactionType, spec.approvedBy = TypeAdjustment, &by
	}
	return s.posting(m.p, m.propertyID, m.bd, folio).insert(ctx, spec)
}

// moveBalance hands amount from m.src to m.tgt: a credit on the source, a debit on the target, the log row. The balance of the source after the lock must cover it, so a transfer cannot make a
// credit (a deposit that was never paid) and two transfers cannot take the same balance.
func (s *Service) moveBalance(ctx context.Context, m movement, amount decimal.Decimal, decimals int32, returns *int64) (TransferredItem, error) {
	q := s.q(ctx)
	code, err := q.GetTransferChargeCode(ctx, foliosdb.GetTransferChargeCodeParams{TenantID: m.p.TenantID, PropertyID: m.propertyID})
	if err != nil {
		return TransferredItem{}, orNotFound(err, apperr.Conflict("TRANSFER_CODE_MISSING", "the charge code TRF does not exist in this property"))
	}
	acct, err := q.GetTransferAccount(ctx, foliosdb.GetTransferAccountParams{TenantID: m.p.TenantID, PropertyID: m.propertyID})
	if err != nil || !acct.IsActive || !acct.IsPostable {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return TransferredItem{}, err
		}
		return TransferredItem{}, apperr.Conflict("TRANSFER_ACCOUNT_NOT_MAPPED", "the system account FOLIO_TRANSFER is not mapped to an active account that takes postings: map it in the account mapping")
	}
	balance, _, err := s.balanceOf(ctx, m.propertyID, m.src.ID)
	if err != nil {
		return TransferredItem{}, err
	}
	if amount.GreaterThan(balance) {
		return TransferredItem{}, apperr.Conflict("TRANSFER_EXCEEDS_BALANCE", "the transfer is more than the folio's balance").WithContext("balance", fixed(balance, decimals)).WithContext("folio_id", m.src.ID)
	}
	out, err := s.transferLine(ctx, m, m.src, code.ID, acct.Code, amount, -1, "Transfer to "+m.tgt.FolioNumber)
	if err != nil {
		return TransferredItem{}, err
	}
	in, err := s.transferLine(ctx, m, m.tgt, code.ID, acct.Code, amount, +1, "Transfer from "+m.src.FolioNumber)
	if err != nil {
		return TransferredItem{}, err
	}
	row, err := q.InsertTransfer(ctx, foliosdb.InsertTransferParams{
		TenantID: m.p.TenantID, PropertyID: m.propertyID, BatchID: m.batchID, FromFolioID: m.src.ID, ToFolioID: m.tgt.ID, OutItemID: out.ID, InItemID: in.ID,
		Amount: amount, OriginFolioID: m.src.ID, PreviousFolioID: m.src.ID, ReturnsTransferID: returns, // a balance has no identity to follow: it is the folio it left
	})
	if err != nil {
		return TransferredItem{}, err
	}
	action := "folio.balance_transferred"
	if returns != nil {
		action = "folio.transfer_returned"
	}
	if err := s.audit.Write(ctx, audit.Entry{TenantID: m.p.TenantID, PropertyID: &m.propertyID, BusinessDate: &m.bd, UserID: m.p.ActorID(), Action: action,
		EntityType: "folio", EntityID: m.src.ID, EntityLabel: m.src.FolioNumber, New: map[string]any{"transfer_id": row.ID, "batch_id": m.batchID, "scope": m.scope, "amount": fixed(amount, decimals),
			"from_folio_id": m.src.ID, "to_folio_id": m.tgt.ID, "out_item_id": out.ID, "in_item_id": in.ID, "returns_transfer_id": returns, "reason": m.reason, "actor": m.p.ActorID(), "approved_by": m.approval.UserID()}}); err != nil {
		return TransferredItem{}, err
	}
	return TransferredItem{TransferID: row.ID, FromFolioID: m.src.ID, ToFolioID: m.tgt.ID, OutItemID: out.ID, InItemID: in.ID, Amount: fixed(amount, decimals)}, nil
}

// TransferBalance hands part of the balance of a folio to another (folio.reverse and an approval, always; folio.transfer_any on the person and the approver for an unrelated folio). key is the
// Idempotency-Key: the same request again returns the transfer that was made. Revenue and tax stay on the folio the balance leaves: a tax invoice in the name of the payer needs the charges
// themselves moved.
func (s *Service) TransferBalance(ctx context.Context, propertyID, sourceFolioID int64, key string, in TransferBalanceInput) (TransferResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return TransferResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return TransferResult{}, err
	}
	reason, fields := requireReason(in.Reason)
	amount, fe := parsePositive("amount", in.Amount, decimals)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if in.FolioID < 1 {
		fields = append(fields, fieldErr("folio_id", "REQUIRED", "the folio to hand the balance to"))
	}
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return TransferResult{}, apperr.Invalid("the transfer is invalid", fields...)
	}
	if in.FolioID == sourceFolioID {
		return TransferResult{}, errTransferInvalid("a balance cannot be handed to the same folio")
	}
	return replayLoop(key,
		func() (TransferResult, bool, error) {
			return s.balanceReplay(ctx, p.TenantID, propertyID, key, sourceFolioID, in.FolioID, amount, decimals)
		},
		func() (TransferResult, error) {
			pre, err := s.preScope(ctx, p.TenantID, propertyID, sourceFolioID, in.FolioID)
			if err != nil {
				return TransferResult{}, err
			}
			approval, err := s.transferApproval(ctx, propertyID, pre, in.Approval)
			if err != nil {
				return TransferResult{}, err
			}
			var out TransferResult
			err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
				day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
				if err != nil {
					return err
				}
				src, tgt, err := s.lockPair(ctx, p.TenantID, propertyID, sourceFolioID, in.FolioID) // L4, ascending
				if err != nil {
					return err
				}
				scope, err := s.scopeOf(ctx, p.TenantID, propertyID, src, tgt)
				if err != nil {
					return err
				}
				if scope != pre && (scope == ScopeAny || pre == ScopeAny) {
					return apperr.Conflict("TRANSFER_SCOPE_CHANGED", "the folios changed meanwhile: ask again")
				}
				batch, err := s.q(ctx).InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
					TenantID: p.TenantID, PropertyID: propertyID, Kind: KindBalance, FromFolioID: src.ID, ToFolioID: tgt.ID, Scope: scope, Reason: reason, BusinessDate: day.BusinessDate,
					ApprovedBy: approval.UserID(), IdempotencyKey: nullable(key), ActorID: p.ActorID(),
				})
				if err != nil {
					return err
				}
				m := movement{p: p, propertyID: propertyID, bd: day.BusinessDate, src: src, tgt: tgt, batchID: batch.ID, approval: approval, reason: reason, scope: scope}
				moved, err := s.moveBalance(ctx, m, amount, decimals, nil)
				if err != nil {
					return err
				}
				out = TransferResult{BatchID: batch.ID, Kind: KindBalance, Scope: scope, Moved: []TransferredItem{moved}}
				out.Balances, err = s.balances(ctx, propertyID, decimals, src.ID, tgt.ID)
				return err
			})
			return out, err
		})
}

// balanceReplay returns the transfer stored under an Idempotency-Key; the same key for another transfer is refused.
func (s *Service) balanceReplay(ctx context.Context, tenantID, propertyID int64, key string, srcID, tgtID int64, amount decimal.Decimal, decimals int32) (TransferResult, bool, error) {
	q := s.q(ctx)
	batch, err := q.GetTransferBatchByKey(ctx, foliosdb.GetTransferBatchByKeyParams{TenantID: tenantID, PropertyID: propertyID, Key: &key})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return TransferResult{}, false, nil
		}
		return TransferResult{}, false, err
	}
	rows, err := q.ListBatchTransfers(ctx, foliosdb.ListBatchTransfersParams{PropertyID: propertyID, BatchID: batch.ID})
	if err != nil {
		return TransferResult{}, false, err
	}
	if batch.Kind != KindBalance || batch.FromFolioID != srcID || batch.ToFolioID != tgtID || len(rows) != 1 || !rows[0].Amount.Equal(amount) {
		return TransferResult{}, false, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used for another transfer")
	}
	out := TransferResult{BatchID: batch.ID, Kind: batch.Kind, Scope: batch.Scope, Replayed: true,
		Moved: []TransferredItem{{TransferID: rows[0].ID, FromFolioID: rows[0].FromFolioID, ToFolioID: rows[0].ToFolioID, OutItemID: rows[0].OutItemID, InItemID: rows[0].InItemID, Amount: fixed(rows[0].Amount, decimals)}}}
	out.Balances, err = s.balances(ctx, propertyID, decimals, srcID, tgtID)
	return out, true, err
}

// ---------------------------------------------------------------------------------------------------------------
// The return

// ReturnInput sends transfers back from the folio in the path (the folio that holds what they moved): to the folio each came from last, or to the first folio it came from.
type ReturnInput struct {
	TransferIDs []int64            `json:"transfer_ids"`
	To          string             `json:"to"`
	Reason      string             `json:"reason"`
	Approval    *iam.ApprovalInput `json:"approval"`
}

// ReturnTransfers sends back what earlier transfers moved, several at once (folio.reverse and an approval, always). The folio in the path must hold them: the transfer brought them here and
// nothing moved them on. A transfer is returned once. The way back must be open on both ends: a closed folio is refused, with its number. A balance goes back to the folio it left, only.
func (s *Service) ReturnTransfers(ctx context.Context, propertyID, holderFolioID int64, in ReturnInput) (TransferResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return TransferResult{}, err
	}
	reason, fields := requireReason(in.Reason)
	if in.To != ReturnToPrevious && in.To != ReturnToOrigin {
		fields = append(fields, fieldErr("to", "INVALID_VALUE", "PREVIOUS or ORIGIN"))
	}
	if len(in.TransferIDs) == 0 {
		fields = append(fields, fieldErr("transfer_ids", "REQUIRED", "at least one transfer"))
	}
	if len(in.TransferIDs) > MaxTransferItems {
		e := apperr.Invalid("the return is invalid", fieldErr("transfer_ids", "TOO_MANY_ITEMS", "at most 200 transfers in a request"))
		e.Code = "TOO_MANY_ITEMS"
		return TransferResult{}, e
	}
	if len(fields) > 0 {
		return TransferResult{}, apperr.Invalid("the return is invalid", fields...)
	}
	ids := slices.Clone(in.TransferIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	q := s.q(ctx)
	targetOf := func(t foliosdb.FolioTransfer) (int64, error) {
		if in.To == ReturnToOrigin {
			if t.OriginalItemID == nil {
				return 0, errTransferInvalid("a balance goes back to the folio it left, not to a first origin").WithContext("transfer_id", t.ID)
			}
			return t.OriginFolioID, nil
		}
		return t.PreviousFolioID, nil
	}
	// before the transaction: where each goes, and so what approval is asked
	pre := ScopeReturn
	for _, id := range ids {
		t, err := q.GetTransfer(ctx, foliosdb.GetTransferParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return TransferResult{}, orNotFound(err, apperr.NotFound("TRANSFER_NOT_FOUND", "the transfer does not exist in this property"))
		}
		to, err := targetOf(t)
		if err != nil {
			return TransferResult{}, err
		}
		if to == holderFolioID {
			return TransferResult{}, errTransferInvalid("the charges are on that folio already").WithContext("transfer_id", id)
		}
		sc, err := s.preScope(ctx, p.TenantID, propertyID, holderFolioID, to)
		if err != nil {
			return TransferResult{}, err
		}
		if sc == ScopeAny {
			pre = ScopeAny
		}
	}
	approval, err := s.transferApproval(ctx, propertyID, pre, in.Approval)
	if err != nil {
		return TransferResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return TransferResult{}, err
	}
	var out TransferResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil) // L1
		if err != nil {
			return err
		}
		q := s.q(ctx)
		transfers := make([]foliosdb.FolioTransfer, 0, len(ids))
		folioSet := []int64{holderFolioID}
		for _, id := range ids {
			t, err := q.GetTransfer(ctx, foliosdb.GetTransferParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
			if err != nil {
				return orNotFound(err, apperr.NotFound("TRANSFER_NOT_FOUND", "the transfer does not exist in this property"))
			}
			to, err := targetOf(t)
			if err != nil {
				return err
			}
			transfers = append(transfers, t)
			folioSet = append(folioSet, to)
		}
		slices.Sort(folioSet)
		folioSet = slices.Compact(folioSet)
		if err := db.LockRows(ctx, db.Folios, db.ForUpdate, propertyID, folioSet); err != nil { // L4, ascending
			return mapNotFound(err, errFolioNotFound())
		}
		folios := map[int64]foliosdb.Folio{}
		for _, id := range folioSet {
			f, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
			if err != nil {
				return orNotFound(err, errFolioNotFound())
			}
			if err := requireOpen(f); err != nil {
				return err
			}
			folios[id] = f
		}
		holder := folios[holderFolioID]
		type key struct {
			to   int64
			kind string
		}
		batches := map[key]foliosdb.FolioTransferBatch{}
		var order []key
		touched := slices.Clone(folioSet)
		for _, t := range transfers {
			to, _ := targetOf(t)
			tgt := folios[to]
			if t.ToFolioID != holderFolioID {
				return errTransferInvalid("the folio does not hold that transfer: the charge is on another folio now").WithContext("transfer_id", t.ID)
			}
			if _, err := q.GetTransferReturnedBy(ctx, foliosdb.GetTransferReturnedByParams{PropertyID: propertyID, TransferID: &t.ID}); err == nil {
				return apperr.Conflict("ALREADY_RETURNED", "the transfer has been returned already").WithContext("transfer_id", t.ID)
			}
			kind := KindBalance
			if t.OriginalItemID != nil {
				kind = KindItem
				inID := t.InItemID
				if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &inID}); err == nil {
					return errTransferInvalid("the charge was reversed or moved on: return it from the folio that holds it").WithContext("transfer_id", t.ID)
				}
				if _, err := q.GetTransferByOriginalItem(ctx, foliosdb.GetTransferByOriginalItemParams{PropertyID: propertyID, ItemID: &inID}); err == nil {
					return errTransferInvalid("the charge moved on to another folio: return it from the folio that holds it").WithContext("transfer_id", t.ID)
				}
			}
			sc, err := s.scopeOf(ctx, p.TenantID, propertyID, holder, tgt)
			if err != nil {
				return err
			}
			if sc == ScopeAny {
				if pre != ScopeAny {
					return apperr.Conflict("TRANSFER_SCOPE_CHANGED", "the folios changed meanwhile: ask again")
				}
			}
			k := key{to, kind}
			b, ok := batches[k]
			if !ok {
				if b, err = q.InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
					TenantID: p.TenantID, PropertyID: propertyID, Kind: kind, IsReturn: true, FromFolioID: holderFolioID, ToFolioID: to, Scope: ScopeReturn, Reason: reason,
					BusinessDate: day.BusinessDate, ApprovedBy: approval.UserID(), ActorID: p.ActorID(),
				}); err != nil {
					return err
				}
				batches[k] = b
				order = append(order, k)
			}
			m := movement{p: p, propertyID: propertyID, bd: day.BusinessDate, src: holder, tgt: tgt, batchID: b.ID, approval: approval, reason: reason, scope: ScopeReturn}
			var moved TransferredItem
			tid := t.ID
			if kind == KindItem {
				item, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: t.InItemID})
				if err != nil {
					return orNotFound(err, errItemNotFound())
				}
				moved, err = s.moveItem(ctx, m, item, &tid)
				if err != nil {
					return err
				}
			} else if moved, err = s.moveBalance(ctx, m, t.Amount, decimals, &tid); err != nil {
				return err
			}
			out.Moved = append(out.Moved, moved)
		}
		out.Kind, out.Scope = KindItem, ScopeReturn
		if len(order) > 0 {
			out.BatchID = batches[order[0]].ID
			out.Kind = order[0].kind
		}
		out.Balances, err = s.balances(ctx, propertyID, decimals, touched...)
		return err
	})
	return out, err
}
