package folios

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/chargecalc"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/tenancy"
)

// The ledger side of a banquet event (docs/architecture/36-banquet.md sections 4.4, 4.5, 4.7, 4.8, 5.3). The event, its lines and its sessions belong to internal/banquet; what money does belongs
// here, to the one writer of folio_items. The charges of a session are posted through the same charge engine as every other charge, with the price mode the line froze. Payments, refunds,
// adjustments, reversals and the close are the use cases that exist (the folio of an event is an ordinary folio for them). Every method runs in the transaction of the caller, which holds the open
// business day in share mode and the folio (level 42) before the event (level 45); none of them opens a transaction or commits.

// The source and the references of what the banquet posts.
const (
	// SourceBanquet is the source of the ledger rows the banquet posts: a session, a cancellation fee, a forfeited deposit.
	SourceBanquet = "BANQUET"
	// ReferenceBanquetLine is the reference type of the CHARGE row of a line of a session (its id is "<line>" or "<line>:<component>").
	ReferenceBanquetLine = "BANQUET_LINE"
	// ReferenceBanquetCancel is the reference type of the fee or the forfeit of a cancellation (its id is the event).
	ReferenceBanquetCancel = "BANQUET_CANCEL"
)

func errEventFolioClosed(f foliosdb.Folio) *apperr.Error {
	return apperr.Conflict("EVENT_FOLIO_CLOSED", "the folio of the event is closed").WithContext("folio_number", f.FolioNumber).WithContext("folio_id", f.ID)
}

// EventFolio is the folio of an event as the banquet sees it.
type EventFolio struct {
	ID              int64
	Number          string
	EventID         int64
	Status          string
	Version         int32
	BillToCompanyID *int64
}

func toEventFolio(f foliosdb.Folio) EventFolio {
	e := EventFolio{ID: f.ID, Number: f.FolioNumber, Status: f.Status, Version: f.Version, BillToCompanyID: f.BillToCompanyID}
	if f.BanquetEventID != nil {
		e.EventID = *f.BanquetEventID
	}
	return e
}

// EventFolioOf finds the folio of an event (the open one, else the newest), or nil when it has none. A plain read: the caller locks it when it needs to.
func (s *Service) EventFolioOf(ctx context.Context, tenantID, propertyID, eventID int64) (*EventFolio, error) {
	f, err := s.q(ctx).GetEventFolio(ctx, foliosdb.GetEventFolioParams{TenantID: tenantID, PropertyID: propertyID, EventID: &eventID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := toEventFolio(f)
	return &out, nil
}

// OpenEventFolio makes the folio of an event that has none, in the caller's transaction: the caller holds the business day and the event (level 45), and the number of the folio is taken here
// (level 50, the last lock). It is billed to the company that books the event, when there is one. A second open folio for the event is refused by the database (FOLIO_ALREADY_EXISTS).
func (s *Service) OpenEventFolio(ctx context.Context, p auth.Principal, propertyID, eventID int64, companyID *int64) (EventFolio, error) {
	number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqFolio)
	if err != nil {
		return EventFolio{}, err
	}
	f, err := s.q(ctx).InsertEventFolio(ctx, foliosdb.InsertEventFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioNumber: number, EventID: &eventID, CompanyID: companyID, ActorID: p.ActorID()})
	if err != nil {
		return EventFolio{}, err
	}
	return toEventFolio(f), nil
}

// EventFolioByID reads a folio and refuses one that is not the folio of an event.
func (s *Service) EventFolioByID(ctx context.Context, tenantID, propertyID, folioID int64) (EventFolio, error) {
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: folioID})
	if err != nil {
		return EventFolio{}, orNotFound(err, errFolioNotFound())
	}
	if f.FolioType != FolioTypeEvent {
		return EventFolio{}, errFolioNotFound()
	}
	return toEventFolio(f), nil
}

func (s *Service) openEventFolio(ctx context.Context, p auth.Principal, propertyID, folioID int64) (foliosdb.Folio, error) {
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
	if err != nil {
		return foliosdb.Folio{}, orNotFound(err, errFolioNotFound())
	}
	if f.FolioType != FolioTypeEvent {
		return foliosdb.Folio{}, errFolioNotFound()
	}
	if f.Status != "OPEN" {
		return foliosdb.Folio{}, errEventFolioClosed(f)
	}
	return f, nil
}

// EventCharge is one charge of an event: a line of a session, a component of a package, a cancellation fee or a forfeited deposit. The unit price is read in PriceMode, the mode the line froze;
// the tax and the service charge are the engine's, from the rules mapped to the charge code. Key is the idempotency key of the item (36 3.3).
type EventCharge struct {
	ChargeCodeID  int64
	Quantity      decimal.Decimal
	UnitPrice     decimal.Decimal
	PriceMode     chargecalc.PriceMode
	Discount      decimal.Decimal // an amount, at most the currency's decimals
	Description   string
	ServiceDate   civil.Date
	Key           string
	Reason        *string
	ReferenceType string
	ReferenceID   string
}

// PostEventCharge posts a CHARGE to the folio of an event: source BANQUET, through the one charge engine. The permission is the banquet's (the caller checked it), not folio.post_charge: the
// banquet office does not need to be allowed to post by hand to post the sessions of its events.
func (s *Service) PostEventCharge(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID int64, in EventCharge) (PostedLine, error) {
	folio, err := s.openEventFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PostedLine{}, err
	}
	price, mode := in.UnitPrice, in.PriceMode
	cmd := chargeCmd{
		chargeCodeID: in.ChargeCodeID, quantity: in.Quantity, unitPrice: &price, priceMode: &mode, discount: in.Discount, serviceDate: in.ServiceDate, description: in.Description,
		key: in.Key, reason: in.Reason, source: SourceBanquet,
	}
	if in.ReferenceType != "" {
		cmd.referenceType, cmd.referenceID = ptr(in.ReferenceType), ptr(in.ReferenceID)
	}
	item, err := s.posting(p, propertyID, bd, folio).postCharge(ctx, cmd)
	if err != nil {
		return PostedLine{}, err
	}
	return postedOf(item), nil
}

// EventDeposits is what the folio of an event holds as deposits: the payments taken before its first charge, less what was refunded of them (the view of the ledger decides what is a deposit).
func (s *Service) EventDeposits(ctx context.Context, propertyID, folioID int64) (decimal.Decimal, error) {
	return s.q(ctx).EventDepositsReceived(ctx, foliosdb.EventDepositsReceivedParams{PropertyID: propertyID, FolioID: folioID})
}

// TransferItemsAmount is what a transfer of charges would move, read before the locks to know the amount the rule of approval sees (folio.reverse).
func (s *Service) TransferItemsAmount(ctx context.Context, propertyID, folioID int64, in TransferItemsInput) (decimal.Decimal, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return decimal.Zero, err
	}
	return s.transferItemsAmount(ctx, p.TenantID, propertyID, folioID, in)
}

// eventPair reads the two folios of a transfer the caller has locked (level 42, ascending) and checks that they are an event bill and a folio of its group (scope EVENT_GROUP): the banquet links
// the event first, so a transfer to a master folio is never ANY and never asks folio.transfer_any.
func (s *Service) eventPair(ctx context.Context, tenantID, propertyID, srcID, tgtID int64) (src, tgt foliosdb.Folio, err error) {
	q := s.q(ctx)
	if src, err = q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: srcID}); err != nil {
		return src, tgt, orNotFound(err, errFolioNotFound())
	}
	if tgt, err = q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: tenantID, PropertyID: propertyID, ID: tgtID}); err != nil {
		return src, tgt, orNotFound(err, errFolioNotFound())
	}
	if err := requireOpen(src); err != nil {
		return src, tgt, err
	}
	if err := requireOpen(tgt); err != nil {
		return src, tgt, err
	}
	scope, err := s.scopeOf(ctx, tenantID, propertyID, src, tgt)
	if err != nil {
		return src, tgt, err
	}
	if scope != ScopeEventGroup {
		return src, tgt, apperr.Conflict("TRANSFER_SCOPE_CHANGED", "the folios changed meanwhile: ask again")
	}
	return src, tgt, nil
}

// TransferEventItems moves charges of the bill of an event to a folio of its group, inside the transaction of the caller, which holds the business day and both folios (level 42) and has verified
// the approval (folio.reverse on the person). One batch of the scope EVENT_GROUP, one reversal and one copy for each charge, the audit entries of every transfer.
func (s *Service) TransferEventItems(ctx context.Context, propertyID int64, bd civil.Date, srcID, tgtID int64, in TransferItemsInput, approval iam.Approval) (TransferResult, error) {
	p, err := s.actor(ctx, propertyID, auth.PermFolioReverse)
	if err != nil {
		return TransferResult{}, err
	}
	reason, fields := requireReason(in.Reason)
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
	src, tgt, err := s.eventPair(ctx, p.TenantID, propertyID, srcID, tgtID)
	if err != nil {
		return TransferResult{}, err
	}
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return TransferResult{}, err
	}
	return s.moveSelected(ctx, p, propertyID, bd, src, tgt, in, reason, approval, ScopeEventGroup, nil, decimals)
}

// TransferEventBalance hands part of the balance of the bill of an event to a folio of its group, in the transaction of the caller (as TransferEventItems). key is the Idempotency-Key of the batch.
func (s *Service) TransferEventBalance(ctx context.Context, propertyID int64, bd civil.Date, srcID, tgtID int64, key string, in TransferBalanceInput, approval iam.Approval) (TransferResult, error) {
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
	fields = append(fields, checkKey(key)...)
	if len(fields) > 0 {
		return TransferResult{}, apperr.Invalid("the transfer is invalid", fields...)
	}
	src, tgt, err := s.eventPair(ctx, p.TenantID, propertyID, srcID, tgtID)
	if err != nil {
		return TransferResult{}, err
	}
	if replay, found, err := s.balanceReplay(ctx, p.TenantID, propertyID, key, src.ID, tgt.ID, amount, decimals); err != nil || found {
		return replay, err
	}
	batch, err := s.q(ctx).InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
		TenantID: p.TenantID, PropertyID: propertyID, Kind: KindBalance, FromFolioID: src.ID, ToFolioID: tgt.ID, Scope: ScopeEventGroup, Reason: reason, BusinessDate: bd,
		ApprovedBy: ptr(approval.UserID()), IdempotencyKey: nullable(key), ActorID: p.ActorID(),
	})
	if err != nil {
		return TransferResult{}, err
	}
	m := movement{p: p, propertyID: propertyID, bd: bd, src: src, tgt: tgt, batchID: batch.ID, approval: approval, reason: reason, scope: ScopeEventGroup}
	moved, err := s.moveBalance(ctx, m, amount, decimals, nil)
	if err != nil {
		return TransferResult{}, err
	}
	out := TransferResult{BatchID: batch.ID, Kind: KindBalance, Scope: ScopeEventGroup, Moved: []TransferredItem{moved}}
	out.Balances, err = s.balances(ctx, propertyID, decimals, src.ID, tgt.ID)
	return out, err
}

// TransferEventApproval verifies, before the transaction, the approval of the transfer of the bill of an event to a folio of its group (scope EVENT_GROUP: the approval every correction needs, never
// folio.transfer_any). amount is the sum the transfer moves.
func (s *Service) TransferEventApproval(ctx context.Context, propertyID int64, in *iam.ApprovalInput, amount decimal.Decimal) (iam.Approval, error) {
	return s.transferApproval(ctx, propertyID, ScopeEventGroup, in, amount)
}

// ParseTransferAmount reads the amount of a transfer of a balance with the decimals of the property.
func (s *Service) ParseTransferAmount(ctx context.Context, propertyID int64, raw string) (decimal.Decimal, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return decimal.Zero, err
	}
	amount, fe := parsePositive("amount", raw, decimals)
	if fe != nil {
		return decimal.Zero, apperr.Invalid("the transfer is invalid", *fe)
	}
	return amount, nil
}
