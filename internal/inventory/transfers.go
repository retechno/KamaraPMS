package inventory

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/tenancy"
)

// The transfer between warehouses (35-inventory.md 4.5, 5.4, D9): one step, the goods are in the destination when it posts; the value goes with the goods (the average of the source, or the layers it took, copied with
// their dates); there is no journal, because both warehouses of an item are under the same inventory account; the source may not go below zero, the destination may (the transfer settles what was sold there).
// A transfer answers an approved or partly answered requisition (SR) or is free.

// Transfer states.
const (
	TrfPosted   = "POSTED"
	TrfReversed = "REVERSED"
)

// TransferLineInput is a line of a transfer being posted: an item and a quantity in one of its units (the stock unit when none is named).
type TransferLineInput struct {
	ItemID int64           `json:"item_id"`
	UnitID *int64          `json:"unit_id"`
	Qty    decimal.Decimal `json:"qty"`
}

// TransferInput posts a transfer. With a requisition the warehouses are the requisition's (and, when named, must agree with it).
type TransferInput struct {
	ClientUUID      string              `json:"client_uuid"`
	RequisitionID   *int64              `json:"requisition_id"`
	FromWarehouseID int64               `json:"from_warehouse_id"`
	ToWarehouseID   int64               `json:"to_warehouse_id"`
	Note            *string             `json:"note"`
	Lines           []TransferLineInput `json:"lines"`
}

// TransferLine is a line of a posted transfer.
type TransferLine struct {
	LineNo            int             `json:"line_no"`
	ItemID            int64           `json:"item_id"`
	ItemCode          string          `json:"item_code"`
	ItemName          string          `json:"item_name"`
	StockUnitCode     string          `json:"stock_unit_code"`
	RequisitionLineNo *int            `json:"requisition_line_no"`
	Qty               decimal.Decimal `json:"qty_stock"`
	Value             decimal.Decimal `json:"value"`
}

// Transfer is a posted transfer with its lines.
type Transfer struct {
	ID                int64           `json:"id"`
	Number            string          `json:"trf_number"`
	Status            string          `json:"status"`
	RequisitionID     *int64          `json:"requisition_id"`
	RequisitionNumber *string         `json:"requisition_number"`
	FromWarehouseID   int64           `json:"from_warehouse_id"`
	FromWarehouseName string          `json:"from_warehouse_name"`
	ToWarehouseID     int64           `json:"to_warehouse_id"`
	ToWarehouseName   string          `json:"to_warehouse_name"`
	BusinessDate      civil.Date      `json:"business_date"`
	Note              *string         `json:"note"`
	ReverseReason     *string         `json:"reverse_reason"`
	CreatedByName     *string         `json:"created_by_name"`
	CreatedAt         time.Time       `json:"created_at"`
	TotalValue        decimal.Decimal `json:"total_value"`
	Reversible        bool            `json:"reversible"`
	Lines             []TransferLine  `json:"lines"`
}

// TransferBrief is a row of the list of transfers.
type TransferBrief struct {
	ID                int64           `json:"id"`
	Number            string          `json:"trf_number"`
	Status            string          `json:"status"`
	RequisitionID     *int64          `json:"requisition_id"`
	RequisitionNumber *string         `json:"requisition_number"`
	FromWarehouseName string          `json:"from_warehouse_name"`
	ToWarehouseName   string          `json:"to_warehouse_name"`
	BusinessDate      civil.Date      `json:"business_date"`
	LineCount         int             `json:"line_count"`
	TotalValue        decimal.Decimal `json:"total_value"`
	CreatedAt         time.Time       `json:"created_at"`
}

// TransferFilter narrows the list of transfers.
type TransferFilter struct {
	Status        string
	RequisitionID int64
	FreeOnly      bool
	Q             string
	Limit         int
}

// ListTransfers lists the transfers, newest first (inventory.view or a permission of the movements).
func (s *Service) ListTransfers(ctx context.Context, propertyID int64, f TransferFilter) ([]TransferBrief, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	arg := inventorydb.ListTransfersParams{TenantID: p.TenantID, PropertyID: propertyID, Status: optional(&f.Status), Q: optional(&f.Q), RowLimit: limitOf(f.Limit)}
	if f.RequisitionID > 0 {
		arg.RequisitionID = &f.RequisitionID
	}
	if f.FreeOnly {
		t := true
		arg.FreeOnly = &t
	}
	rows, err := s.q(ctx).ListTransfers(ctx, arg)
	if err != nil {
		return nil, err
	}
	out := make([]TransferBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, TransferBrief{ID: r.ID, Number: r.TrfNumber, Status: r.Status, RequisitionID: r.RequisitionID, RequisitionNumber: r.RequisitionNumber, FromWarehouseName: r.FromWarehouseName,
			ToWarehouseName: r.ToWarehouseName, BusinessDate: r.BusinessDate, LineCount: int(r.LineCount), TotalValue: r.TotalValue, CreatedAt: r.CreatedAt})
	}
	return out, nil
}

// GetTransfer answers one transfer with its lines.
func (s *Service) GetTransfer(ctx context.Context, propertyID, id int64) (Transfer, error) {
	p, err := s.canReadMovements(ctx, propertyID)
	if err != nil {
		return Transfer{}, err
	}
	return s.transfer(ctx, p, propertyID, id)
}

func (s *Service) transfer(ctx context.Context, p auth.Principal, propertyID, id int64) (Transfer, error) {
	q := s.q(ctx)
	r, err := q.GetTransfer(ctx, inventorydb.GetTransferParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Transfer{}, orNotFound(err, notFound("TRANSFER_NOT_FOUND", "transfer"))
	}
	out := Transfer{ID: r.ID, Number: r.TrfNumber, Status: r.Status, RequisitionID: r.RequisitionID, RequisitionNumber: r.RequisitionNumber, FromWarehouseID: r.FromWarehouseID, FromWarehouseName: r.FromWarehouseName,
		ToWarehouseID: r.ToWarehouseID, ToWarehouseName: r.ToWarehouseName, BusinessDate: r.BusinessDate, Note: r.Note, ReverseReason: r.ReverseReason, CreatedByName: r.CreatedByName, CreatedAt: r.CreatedAt,
		TotalValue: r.TotalValue, Lines: []TransferLine{}}
	if day, err := s.days.CurrentBusinessDay(ctx, propertyID); err == nil {
		out.Reversible = r.Status == TrfPosted && r.BusinessDate.Equal(day.BusinessDate)
	}
	lines, err := q.ListTransferLines(ctx, inventorydb.ListTransferLinesParams{TenantID: p.TenantID, PropertyID: propertyID, TransferID: id})
	if err != nil {
		return Transfer{}, err
	}
	for _, l := range lines {
		tl := TransferLine{LineNo: int(l.LineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, StockUnitCode: l.StockUnitCode, Qty: l.QtyStock, Value: l.Value}
		if l.RequisitionLineNo != nil {
			n := int(*l.RequisitionLineNo)
			tl.RequisitionLineNo = &n
		}
		out.Lines = append(out.Lines, tl)
	}
	return out, nil
}

// PostTransfer moves goods from one warehouse to another (inventory.move). A transfer that names a requisition is refused unless the chain of the requisition is done (409 SR_NOT_APPROVED) and it may not
// send more of an item than the requisition has left (409 TRANSFER_EXCEEDS_SR). A repeated client_uuid answers the first transfer.
func (s *Service) PostTransfer(ctx context.Context, propertyID int64, in TransferInput) (Transfer, error) {
	p, err := s.canMove(ctx, propertyID)
	if err != nil {
		return Transfer{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Transfer{}, invalid("transfer", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	if in.Note != nil && len([]rune(trimmed(*in.Note))) > 300 {
		return Transfer{}, invalid("transfer", fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		var sr *inventorydb.GetRequisitionRow
		from, to := in.FromWarehouseID, in.ToWarehouseID
		if in.RequisitionID != nil {
			if err := db.LockRows(ctx, db.StockRequisitions, db.ForUpdate, propertyID, []int64{*in.RequisitionID}); err != nil {
				return orNotFound(err, notFound("REQUISITION_NOT_FOUND", "requisition"))
			}
			r, err := q.GetRequisition(ctx, inventorydb.GetRequisitionParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *in.RequisitionID})
			if err != nil {
				return orNotFound(err, notFound("REQUISITION_NOT_FOUND", "requisition"))
			}
			sr = &r
			from, to = r.FromWarehouseID, r.ToWarehouseID
			if (in.FromWarehouseID != 0 && in.FromWarehouseID != from) || (in.ToWarehouseID != 0 && in.ToWarehouseID != to) {
				return invalid("transfer", fieldErr("from_warehouse_id", "INVALID_VALUE", "the warehouses of a transfer that answers a requisition are the requisition's"))
			}
		}
		if prev, err := q.GetTransferByClientUUID(ctx, inventorydb.GetTransferByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if sr != nil && sr.Status != SRApproved && sr.Status != SRPartial {
			return apperr.Conflict("SR_NOT_APPROVED", "the requisition is "+sr.Status+": a transfer answers a requisition whose approval is done").WithContext("status", sr.Status)
		}
		if from == to || from < 1 || to < 1 {
			return invalid("transfer", fieldErr("to_warehouse_id", "INVALID_VALUE", "two different warehouses"))
		}
		docLines := make([]docLine, 0, len(in.Lines))
		for _, l := range in.Lines {
			docLines = append(docLines, docLine{ItemID: l.ItemID, UnitID: l.UnitID, Qty: l.Qty})
		}
		lines, err := s.checkDocLines(ctx, p, propertyID, "transfer", docLines)
		if err != nil {
			return err
		}
		srLine := map[int64]inventorydb.ListRequisitionLinesRow{}
		if sr != nil {
			rl, err := q.ListRequisitionLines(ctx, inventorydb.ListRequisitionLinesParams{TenantID: p.TenantID, PropertyID: propertyID, RequisitionID: sr.ID})
			if err != nil {
				return err
			}
			for _, l := range rl {
				srLine[l.ItemID] = l
			}
			for i, l := range lines {
				want, ok := srLine[l.item.ItemID]
				if !ok {
					return invalid("transfer", fieldErr(lineAt(i, "item_id"), "NOT_FOUND", "the item is not on the requisition"))
				}
				if left := want.QtyStock.Sub(want.QtyIssuedStock); l.stockQty.GreaterThan(left) {
					return apperr.Conflict("TRANSFER_EXCEEDS_SR", "the transfer sends more than the requisition has left of the item").
						WithContext("item_id", l.item.ItemID).WithContext("left", left.String()).WithContext("requested", l.stockQty.String())
				}
			}
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, 2*len(lines))
		for _, l := range lines {
			pairs = append(pairs, Pair{from, l.item.ItemID}, Pair{to, l.item.ItemID})
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{}) // both warehouses and every balance, ascending: two transfers in opposite directions cannot deadlock
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvTRF)
		if err != nil {
			return err
		}
		id, err = q.InsertTransfer(ctx, inventorydb.InsertTransferParams{TenantID: p.TenantID, PropertyID: propertyID, TrfNumber: number, ClientUuid: key, RequisitionID: in.RequisitionID,
			FromWarehouseID: from, ToWarehouseID: to, BusinessDate: bd, Note: optional(in.Note), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		total := decimal.Zero
		for i, l := range lines {
			ref := Ref{Type: "INV_TRANSFER", ID: id, Line: i + 1}
			out, err := stock.Out(ctx, OutArgs{Pair: Pair{from, l.item.ItemID}, Kind: KindTransferOut, Qty: l.stockQty, Ref: ref})
			if err != nil {
				return err
			}
			value := out.Value.Neg()
			in, err := stock.In(ctx, InArgs{Pair: Pair{to, l.item.ItemID}, Kind: KindTransferIn, Qty: l.stockQty, Value: value, Ref: ref, Layers: out.Slices})
			if err != nil {
				return err
			}
			var srLineNo *int32
			if sl, ok := srLine[l.item.ItemID]; ok {
				n := sl.LineNo
				srLineNo = &n
				if err := q.AddRequisitionLineIssued(ctx, inventorydb.AddRequisitionLineIssuedParams{PropertyID: propertyID, RequisitionID: sr.ID, LineNo: sl.LineNo, Delta: l.stockQty}); err != nil {
					return err
				}
			}
			if err := q.InsertTransferLine(ctx, inventorydb.InsertTransferLineParams{TenantID: p.TenantID, PropertyID: propertyID, TransferID: id, LineNo: int32(i + 1), ItemID: l.item.ItemID, //nolint:gosec // G115: at most 200
				RequisitionLineNo: srLineNo, QtyStock: l.stockQty, Value: value, OutMovementID: out.MovementID, InMovementID: in.MovementID}); err != nil {
				return err
			}
			total = total.Add(value)
		}
		if sr != nil {
			if err := s.refreshRequisitionStatus(ctx, p, propertyID, sr.ID); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.transfer_posted", "inv_transfer", id, number, nil,
			map[string]any{"from": from, "to": to, "lines": len(lines), "value": total, "requisition_id": in.RequisitionID}))
	})
	if err != nil {
		return Transfer{}, err
	}
	return s.transfer(ctx, p, propertyID, id)
}

func lineAt(i int, field string) string { return "lines[" + itoa(i) + "]." + field }

// ReverseTransfer undoes a transfer on the day it was posted (inventory.move): the goods go back, the requisition has them again. It is refused when the destination no longer holds the goods, or when
// the transfer settled stock that was sold below zero there (409 MOVEMENT_NOT_REVERSIBLE); the way then is a transfer the other way.
func (s *Service) ReverseTransfer(ctx context.Context, propertyID, id int64, reason string) (Transfer, error) {
	p, err := s.canMove(ctx, propertyID)
	if err != nil {
		return Transfer{}, err
	}
	reason = trimmed(reason)
	if n := len([]rune(reason)); n < 1 || n > 300 {
		return Transfer{}, invalid("reversal", fieldErr("reason", "REQUIRED", "say why, 300 characters at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := q.GetTransfer(ctx, inventorydb.GetTransferParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("TRANSFER_NOT_FOUND", "transfer"))
		}
		if cur.RequisitionID != nil { // the requisition comes first in the order of the documents
			if err := db.LockRows(ctx, db.StockRequisitions, db.ForUpdate, propertyID, []int64{*cur.RequisitionID}); err != nil {
				return err
			}
		}
		if err := db.LockRows(ctx, db.StockTransfers, db.ForUpdate, propertyID, []int64{id}); err != nil {
			return orNotFound(err, notFound("TRANSFER_NOT_FOUND", "transfer"))
		}
		if cur, err = q.GetTransfer(ctx, inventorydb.GetTransferParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id}); err != nil {
			return err
		}
		if cur.Status != TrfPosted {
			return errDocState("TRANSFER", cur.Status, "posted")
		}
		if !cur.BusinessDate.Equal(bd) {
			return apperr.Conflict("TRANSFER_NOT_REVERSIBLE", "a transfer is reversed on the day it was posted: make a transfer the other way").WithContext("business_date", cur.BusinessDate.String())
		}
		lines, err := q.ListTransferLines(ctx, inventorydb.ListTransferLinesParams{TenantID: p.TenantID, PropertyID: propertyID, TransferID: id})
		if err != nil {
			return err
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		pairs := make([]Pair, 0, 2*len(lines))
		for _, l := range lines {
			pairs = append(pairs, Pair{cur.FromWarehouseID, l.ItemID}, Pair{cur.ToWarehouseID, l.ItemID})
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		for _, l := range lines {
			ref := Ref{Type: "INV_TRANSFER_REV", ID: id, Line: int(l.LineNo)}
			if _, err := stock.Reverse(ctx, ReverseArgs{MovementID: l.InMovementID, Kind: KindTransferOut, Ref: ref}); err != nil {
				return err
			}
			if _, err := stock.Reverse(ctx, ReverseArgs{MovementID: l.OutMovementID, Kind: KindTransferIn, Ref: ref}); err != nil {
				return err
			}
			if l.RequisitionLineNo != nil && cur.RequisitionID != nil {
				if err := q.AddRequisitionLineIssued(ctx, inventorydb.AddRequisitionLineIssuedParams{PropertyID: propertyID, RequisitionID: *cur.RequisitionID, LineNo: *l.RequisitionLineNo, Delta: l.QtyStock.Neg()}); err != nil {
					return err
				}
			}
		}
		if err := q.SetTransferReversed(ctx, inventorydb.SetTransferReversedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Reason: &reason, ReversedOn: &bd}); err != nil {
			return err
		}
		if cur.RequisitionID != nil {
			if err := s.refreshRequisitionStatus(ctx, p, propertyID, *cur.RequisitionID); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.transfer_reversed", "inv_transfer", id, cur.TrfNumber, map[string]any{"status": TrfPosted}, map[string]any{"status": TrfReversed, "reason": reason}))
	})
	if err != nil {
		return Transfer{}, err
	}
	return s.transfer(ctx, p, propertyID, id)
}
