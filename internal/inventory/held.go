package inventory

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// What a freeze holds (35-inventory.md 4.7, 5.5, 5.9 D24). While a count freezes a warehouse, anything that may not be refused is written down and applied when the freeze ends: from PR 4 the rows of the POS
// queue with the status HELD, in this PR the lines of the amenities document of a close that fell inside the count (inv_issue_lines with the status HELD, no movement). A held row is made of
//
//   - when it was made (created_at of the queue row, of the document): it decides which side of the count of its item it falls on, against that item's counted_at;
//   - the pair it will take from, the quantity, and what the movement needs (the account and the department of the cost, the day of the sale or of the night).
//
// The count asks two things of the rows: how much of an item they take before its counted_at (held_before_qty, which lowers what the count expects), and to apply them: the ones before counted_at first, the
// count itself, then the others. The POS queue of PR 4 has its own loader and its own apply; the count code below reads rows only through heldRow.

// heldRow is a row that waits for a warehouse to be unfrozen.
type heldRow struct {
	Source       string // "AMENITY" (a line of an amenities document) or "POS" (a component of a row of the POS queue)
	IssueID      int64  // the amenities document (Source AMENITY)
	QueueID      int64  // the queue row (Source POS)
	QueueKind    string // SALE, VOID_RETURN or VOID_WASTE (Source POS)
	MovementID   int64  // the sale movement a void undoes (Source POS, a void)
	LineNo       int32
	Number       string
	ItemID       int64
	Qty          decimal.Decimal // stock units taken out, > 0
	AccountID    int64
	DepartmentID *int64
	CreatedAt    time.Time
	SourceDate   *civil.Date
}

// takes is what the row does to the shelf before it is applied, signed: a sale takes goods (+), a void that returns them gives them back (-), a void that is waste moves cost only (0).
func (r heldRow) takes() decimal.Decimal {
	switch r.QueueKind {
	case QueueVoidReturn:
		return r.Qty.Neg()
	case QueueVoidWaste:
		return decimal.Zero
	}
	return r.Qty
}

// heldRows are the rows that wait for a warehouse, in the order they are applied: by the day of the document and then by its number and line.
func (s *Service) heldRows(ctx context.Context, p auth.Principal, propertyID, warehouseID int64) ([]heldRow, error) {
	rows, err := s.q(ctx).ListHeldIssueLines(ctx, inventorydb.ListHeldIssueLinesParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID})
	if err != nil {
		return nil, err
	}
	out := make([]heldRow, 0, len(rows))
	for _, r := range rows {
		h := heldRow{Source: "AMENITY", IssueID: r.IssueID, LineNo: r.LineNo, Number: r.IssNumber, ItemID: r.ItemID, Qty: r.QtyStock, DepartmentID: r.DepartmentID, CreatedAt: r.CreatedAt, SourceDate: r.SourceDate}
		if r.ExpenseAccountID != nil {
			h.AccountID = *r.ExpenseAccountID
		}
		out = append(out, h)
	}
	pos, err := s.q(ctx).ListHeldConsumption(ctx, inventorydb.ListHeldConsumptionParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID})
	if err != nil {
		return nil, err
	}
	for _, r := range pos {
		comps, err := decodeComponents(r.Components)
		if err != nil {
			return nil, err
		}
		moves, err := s.saleMovements(ctx, p.TenantID, propertyID, r)
		if err != nil {
			return nil, err
		}
		sd := r.SaleDate
		for k, c := range comps {
			h := heldRow{Source: "POS", QueueID: r.ID, QueueKind: r.Kind, LineNo: int32(k + 1), ItemID: c.ItemID, Qty: c.qty(), CreatedAt: r.CreatedAt, SourceDate: &sd, Number: fmt.Sprintf("POS-%d", r.ID)} //nolint:gosec // G115: a few components
			if r.Kind != QueueSale && k < len(moves) {
				h.MovementID = moves[k]
			}
			out = append(out, h)
		}
	}
	return out, nil
}

// heldBefore is the quantity of an item that the held rows take before an instant (4.7 held_before_qty, in absolute value): what the shelf already lacked when the counter looked at it.
func heldBefore(rows []heldRow, itemID int64, at time.Time) decimal.Decimal {
	sum := decimal.Zero
	for _, r := range rows {
		if r.ItemID == itemID && r.CreatedAt.Before(at) { // a row made at the same instant counts as after (4.7)
			sum = sum.Add(r.takes())
		}
	}
	return sum
}

// issueIDsOf lists the documents the rows belong to, for the lock of the documents (before the warehouse and the balances, 5.1).
func issueIDsOf(rows []heldRow) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, r := range rows {
		if r.Source == "AMENITY" && !seen[r.IssueID] {
			seen[r.IssueID] = true
			out = append(out, r.IssueID)
		}
	}
	return out
}

// queueIDsOf lists the queue rows the held rows belong to, for the lock of the rows (with the documents, before the warehouse, 5.1).
func queueIDsOf(rows []heldRow) []int64 {
	seen := map[int64]bool{}
	var out []int64
	for _, r := range rows {
		if r.Source == "POS" && !seen[r.QueueID] {
			seen[r.QueueID] = true
			out = append(out, r.QueueID)
		}
	}
	return out
}

// applyHeld applies the given held rows in order, dated the open day, inside the transaction of the caller that holds the balances.
func (s *Service) applyHeld(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, stock *Stock, rows []heldRow) error {
	qc := s.newQueueCtx(p, propertyID)
	cache := map[int64]inventorydb.InvConsumptionQueue{}
	for _, r := range rows {
		if r.Source != "POS" {
			if err := s.applyHeldIssueLine(ctx, p, propertyID, bd, stock, r); err != nil {
				return err
			}
			continue
		}
		ra, ok := cache[r.QueueID]
		if !ok {
			var err error
			if ra, err = s.q(ctx).GetConsumptionRow(ctx, inventorydb.GetConsumptionRowParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.QueueID}); err != nil {
				return err
			}
			cache[r.QueueID] = ra
		}
		id, err := qc.applyRowComponent(ctx, stock, bd, ra, int(r.LineNo)-1, queueComponent{ItemID: r.ItemID, QtyStock: r.Qty.String()}, r.MovementID)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if _, err := s.q(ctx).AppendConsumptionMovement(ctx, inventorydb.AppendConsumptionMovementParams{TenantID: p.TenantID, PropertyID: propertyID, ID: r.QueueID, MovementID: id, AppliedOn: bd, AppliedAt: now}); err != nil {
			return err
		}
	}
	return nil
}
