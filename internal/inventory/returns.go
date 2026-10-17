package inventory

import (
	"context"
	"errors"
	"fmt"
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

// The return to the supplier (35-inventory.md 4.6, 5.3.6): goods go back at the cost of the receipt line they came in with, booked Dr goods received not invoiced / Cr inventory. If the receipt was billed, the supplier's
// credit note is entered in the payables against the bill line and linked to the return here; a difference between the credit note and the cost of the goods is cleared like a variance of a bill.

// ReturnLineInput is a line of a return: the line of the receipt and the quantity returned in its unit.
type ReturnLineInput struct {
	ReceiptLineNo int             `json:"receipt_line_no"`
	Qty           decimal.Decimal `json:"qty"`
}

// ReturnInput returns goods of one receipt to its supplier.
type ReturnInput struct {
	ClientUUID string            `json:"client_uuid"`
	ReceiptID  int64             `json:"receipt_id"`
	Reason     string            `json:"reason"`
	Lines      []ReturnLineInput `json:"lines"`
}

// ReturnLine is a line of a posted return.
type ReturnLine struct {
	LineNo        int             `json:"line_no"`
	ReceiptLineNo int             `json:"receipt_line_no"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	QtyStock      decimal.Decimal `json:"qty_stock"`
	StockUnitCode string          `json:"stock_unit_code"`
	Value         decimal.Decimal `json:"value"`
}

// ReturnBrief is a row of the list of returns.
type ReturnBrief struct {
	ID            int64           `json:"id"`
	Number        string          `json:"return_number"`
	SupplierID    int64           `json:"supplier_id"`
	SupplierName  string          `json:"supplier_name"`
	WarehouseID   int64           `json:"warehouse_id"`
	WarehouseName string          `json:"warehouse_name"`
	ReceiptID     int64           `json:"receipt_id"`
	ReceiptNumber string          `json:"receipt_number"`
	Reason        string          `json:"reason"`
	TotalValue    decimal.Decimal `json:"total_value"`
	CreditNoteID  *int64          `json:"credit_note_id"`
	CreditNumber  *string         `json:"credit_number"`
	CreatedAt     time.Time       `json:"created_at"`
}

// Return is a return with its lines.
type Return struct {
	ReturnBrief
	Lines []ReturnLine `json:"lines"`
}

func toReturnBrief(r inventorydb.ListReturnsRow) ReturnBrief {
	return ReturnBrief{ID: r.ID, Number: r.ReturnNumber, SupplierID: r.SupplierID, SupplierName: r.SupplierName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName, ReceiptID: r.ReceiptID,
		ReceiptNumber: r.ReceiptNumber, Reason: r.Reason, TotalValue: r.TotalValue, CreditNoteID: r.CreditNoteID, CreditNumber: r.CreditNumber, CreatedAt: r.CreatedAt}
}

// ListReturns lists the returns, newest first.
func (s *Service) ListReturns(ctx context.Context, propertyID int64, receiptID *int64) ([]ReturnBrief, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListReturns(ctx, inventorydb.ListReturnsParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: receiptID, RowLimit: maxPageRow})
	if err != nil {
		return nil, err
	}
	out := make([]ReturnBrief, 0, len(rows))
	for _, r := range rows {
		out = append(out, toReturnBrief(r))
	}
	return out, nil
}

// GetReturn answers one return with its lines.
func (s *Service) GetReturn(ctx context.Context, propertyID, id int64) (Return, error) {
	p, err := s.canReadRequests(ctx, propertyID)
	if err != nil {
		return Return{}, err
	}
	return s.returnOf(ctx, p, propertyID, id)
}

func (s *Service) returnOf(ctx context.Context, p auth.Principal, propertyID, id int64) (Return, error) {
	q := s.q(ctx)
	r, err := q.GetReturn(ctx, inventorydb.GetReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Return{}, orNotFound(err, notFound("RETURN_NOT_FOUND", "return"))
	}
	out := Return{ReturnBrief: ReturnBrief{ID: r.ID, Number: r.ReturnNumber, SupplierID: r.SupplierID, SupplierName: r.SupplierName, WarehouseID: r.WarehouseID, WarehouseName: r.WarehouseName,
		ReceiptID: r.ReceiptID, ReceiptNumber: r.ReceiptNumber, Reason: r.Reason, TotalValue: r.TotalValue, CreditNoteID: r.CreditNoteID, CreditNumber: r.CreditNumber, CreatedAt: r.CreatedAt}}
	lines, err := q.ListReturnLines(ctx, inventorydb.ListReturnLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id})
	if err != nil {
		return Return{}, err
	}
	out.Lines = make([]ReturnLine, 0, len(lines))
	for _, l := range lines {
		out.Lines = append(out.Lines, ReturnLine{LineNo: int(l.LineNo), ReceiptLineNo: int(l.ReceiptLineNo), ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, QtyStock: l.QtyStock,
			StockUnitCode: l.StockUnitCode, Value: l.Value})
	}
	return out, nil
}

// PostReturn returns goods of a receipt to its supplier (inventory.receive). The goods go at the cost of the receipt line; they must be on the shelf (a return never takes the stock below zero). The draft bill of the
// receipt falls by what was returned.
func (s *Service) PostReturn(ctx context.Context, propertyID int64, in ReturnInput) (Return, error) {
	p, err := s.canReceive(ctx, propertyID)
	if err != nil {
		return Return{}, err
	}
	var key pgtype.UUID
	if perr := key.Scan(in.ClientUUID); perr != nil || !key.Valid {
		return Return{}, invalid("return", fieldErr("client_uuid", "INVALID_VALUE", "a UUID made by the client"))
	}
	reason := trimmed(in.Reason)
	if n := len([]rune(reason)); n < 1 || n > 300 {
		return Return{}, invalid("return", fieldErr("reason", "REQUIRED", "why the goods go back, 300 characters at most"))
	}
	if len(in.Lines) == 0 || len(in.Lines) > maxDocLines {
		return Return{}, invalid("return", fieldErr("lines", "OUT_OF_RANGE", "1 to "+itoa(maxDocLines)+" lines"))
	}
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		rc, err := q.GetReceipt(ctx, inventorydb.GetReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.ReceiptID})
		if err != nil {
			return orNotFound(err, invalid("return", fieldErr("receipt_id", "NOT_FOUND", "no such receipt in this property")))
		}
		if rc.DraftID != nil {
			if err := db.LockRows(ctx, db.StockBillDrafts, db.ForUpdate, propertyID, []int64{*rc.DraftID}); err != nil {
				return err
			}
		}
		if err := db.LockRows(ctx, db.StockReceipts, db.ForUpdate, propertyID, []int64{in.ReceiptID}); err != nil {
			return err
		}
		if prev, err := q.GetReturnByClientUUID(ctx, inventorydb.GetReturnByClientUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: key}); err == nil {
			id = prev
			return nil
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if rc.Status != ReceiptPosted {
			return apperr.Conflict("RECEIPT_NOT_RETURNABLE", "the receipt was reversed: nothing of it can be returned")
		}
		var f []apperr.FieldError
		type planned struct {
			rl    inventorydb.GetReceiptLineRow
			stock decimal.Decimal
		}
		plans := make([]planned, 0, len(in.Lines))
		seen := map[int]bool{}
		pairs := make([]Pair, 0, len(in.Lines))
		for i, l := range in.Lines {
			at := fmt.Sprintf("lines[%d]", i)
			rl, err := q.GetReceiptLine(ctx, inventorydb.GetReceiptLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: in.ReceiptID, LineNo: int32(l.ReceiptLineNo)}) //nolint:gosec // G115: a line number
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				f = append(f, fieldErr(at+".receipt_line_no", "NOT_FOUND", "no such line on the receipt"))
				continue
			case err != nil:
				return err
			case seen[l.ReceiptLineNo]:
				f = append(f, fieldErr(at+".receipt_line_no", "DUPLICATE", "a line of the receipt is returned once"))
				continue
			}
			seen[l.ReceiptLineNo] = true
			if !l.Qty.IsPositive() || !l.Qty.Equal(Round6(l.Qty)) {
				f = append(f, fieldErr(at+".qty", "INVALID_VALUE", "above zero, six decimals at most"))
				continue
			}
			stock := StockQty(l.Qty, rl.StockNum, rl.StockDen)
			if left := rl.QtyStock.Sub(rl.QtyReturnedStock); !stock.IsPositive() || stock.GreaterThan(left) {
				f = append(f, fieldErr(at+".qty", "EXCEEDS_RECEIPT", "more than the "+left.String()+" of the receipt line that has not gone back"))
				continue
			}
			plans = append(plans, planned{rl: rl, stock: stock})
			pairs = append(pairs, Pair{rc.WarehouseID, rl.ItemID})
		}
		if len(f) > 0 {
			return invalid("return", f...)
		}
		grni, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: "INV_GRNI"})
		if err != nil {
			return apperr.Conflict("GRNI_ACCOUNT_MISSING", "the account of goods received not invoiced (key INV_GRNI) is missing in this property")
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		number, err := s.days.NextDocumentNumber(ctx, propertyID, tenancy.SeqInvRet)
		if err != nil {
			return err
		}
		id, err = q.InsertReturn(ctx, inventorydb.InsertReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnNumber: number, ClientUuid: key, SupplierID: rc.SupplierID, WarehouseID: rc.WarehouseID,
			ReceiptID: in.ReceiptID, Reason: reason, TotalValue: decimal.Zero, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		total := decimal.Zero
		for i, pl := range plans {
			args := ReturnAt(OutArgs{Pair: Pair{rc.WarehouseID, pl.rl.ItemID}, Kind: KindSupplierReturn, Qty: pl.stock, Ref: Ref{Type: "INV_RETURN", ID: id, Line: i + 1}, Counter: &Counter{AccountID: grni.ID}},
				pl.rl.MovementID, pl.rl.QtyStock, pl.rl.Value)
			posted, err := stock.Out(ctx, args)
			if err != nil {
				return err
			}
			value := posted.Value.Neg()
			total = total.Add(value)
			if err := q.InsertReturnLine(ctx, inventorydb.InsertReturnLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: id, LineNo: int32(i + 1), ReceiptID: in.ReceiptID, //nolint:gosec // G115: at most 200
				ReceiptLineNo: pl.rl.LineNo, ItemID: pl.rl.ItemID, QtyStock: pl.stock, Value: value, MovementID: posted.MovementID}); err != nil {
				return err
			}
			if err := q.AddReceiptLineReturned(ctx, inventorydb.AddReceiptLineReturnedParams{PropertyID: propertyID, ReceiptID: in.ReceiptID, LineNo: pl.rl.LineNo, Qty: pl.stock, Value: value}); err != nil {
				return err
			}
		}
		if err := q.SetReturnTotal(ctx, inventorydb.SetReturnTotalParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, TotalValue: total}); err != nil {
			return err
		}
		if err := s.refreshDraft(ctx, p, propertyID, in.ReceiptID); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.return_posted", "inv_return", id, number, nil,
			map[string]any{"number": number, "receipt_id": in.ReceiptID, "supplier_id": rc.SupplierID, "lines": len(plans), "total_value": total, "reason": reason}))
	})
	if err != nil {
		return Return{}, err
	}
	return s.returnOf(ctx, p, propertyID, id)
}

// LinkCreditNote links the supplier's credit note (entered in the payables against the bill line) to a return of goods that were billed (inventory.bill, 5.3.6). For each returned line that the credit note credits,
// a match with a negative quantity is written, and a difference between the credit and the cost of the goods is cleared into the purchase price variance (the goods are gone, so none of it is stock).
func (s *Service) LinkCreditNote(ctx context.Context, propertyID, returnID, creditNoteID int64) (Return, error) {
	p, err := s.canBill(ctx, propertyID)
	if err != nil {
		return Return{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		ret, err := q.GetReturn(ctx, inventorydb.GetReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ID: returnID})
		if err != nil {
			return orNotFound(err, notFound("RETURN_NOT_FOUND", "return"))
		}
		draft, err := q.GetDraftOfReceipt(ctx, inventorydb.GetDraftOfReceiptParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: ret.ReceiptID})
		if err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.StockBillDrafts, db.ForUpdate, propertyID, []int64{draft.ID}); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.StockReceipts, db.ForUpdate, propertyID, []int64{ret.ReceiptID}); err != nil {
			return err
		}
		if err := db.LockRows(ctx, db.StockReturns, db.ForUpdate, propertyID, []int64{returnID}); err != nil {
			return err
		}
		ret, err = q.GetReturn(ctx, inventorydb.GetReturnParams{TenantID: p.TenantID, PropertyID: propertyID, ID: returnID})
		if err != nil {
			return err
		}
		if ret.CreditNoteID != nil {
			return apperr.Conflict("RETURN_HAS_CREDIT_NOTE", "a credit note is linked to this return already")
		}
		credLines, err := q.ListCreditLinesForMatch(ctx, inventorydb.ListCreditLinesForMatchParams{TenantID: p.TenantID, PropertyID: propertyID, CreditID: creditNoteID})
		if err != nil {
			return err
		}
		if len(credLines) == 0 {
			return apperr.NotFound("CREDIT_NOTE_NOT_FOUND", "the credit note does not exist in this property")
		}
		if credLines[0].Status != "POSTED" || credLines[0].SupplierID != ret.SupplierID {
			return invalid("link", fieldErr("credit_note_id", "INVALID_VALUE", "a credit note of the supplier of the return that stands"))
		}
		retLines, err := q.ListReturnLines(ctx, inventorydb.ListReturnLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ReturnID: returnID})
		if err != nil {
			return err
		}
		type link struct {
			rl     inventorydb.ListReturnLinesRow
			credit inventorydb.ListCreditLinesForMatchRow
			match  inventorydb.InvBillMatch
		}
		var links []link
		pairs := make([]Pair, 0, len(retLines))
		for _, rl := range retLines {
			ms, err := q.ListMatchesOfReceiptLine(ctx, inventorydb.ListMatchesOfReceiptLineParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: rl.ReceiptID, ReceiptLineNo: rl.ReceiptLineNo})
			if err != nil {
				return err
			}
			for _, m := range ms {
				if m.CreditNoteID != nil || m.BillID != credLines[0].BillID {
					continue
				}
				for _, cl := range credLines {
					if cl.BillLineNo == m.BillLineNo {
						links = append(links, link{rl, cl, m})
						pairs = append(pairs, Pair{ret.WarehouseID, rl.ItemID})
					}
				}
			}
		}
		if len(links) == 0 {
			return invalid("link", fieldErr("credit_note_id", "NOT_MATCHED", "the credit note credits no line of a bill that is matched to the goods of this return"))
		}
		grni, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: "INV_GRNI"})
		if err != nil {
			return apperr.Conflict("GRNI_ACCOUNT_MISSING", "the account of goods received not invoiced (key INV_GRNI) is missing in this property")
		}
		ppv, err := q.GetAccountByKey(ctx, inventorydb.GetAccountByKeyParams{TenantID: p.TenantID, PropertyID: propertyID, MapKey: "INV_PPV"})
		if err != nil {
			return apperr.Conflict("PPV_ACCOUNT_MISSING", "the account of the purchase price variance (key INV_PPV) is missing in this property")
		}
		book, err := s.book(ctx, p, propertyID, bd)
		if err != nil {
			return err
		}
		stock, err := s.ledger.Lock(ctx, q, book, pairs, LockOptions{})
		if err != nil {
			return err
		}
		touched := false
		for _, l := range links {
			credit := l.credit.Amount
			if l.credit.VatTreatment != nil && *l.credit.VatTreatment == "EXPENSE" {
				credit = credit.Add(l.credit.VatAmount)
			}
			variance := l.rl.Value.Sub(credit) // bill cost - receipt value, both negative: -credit + value returned
			var moves []int64
			if !variance.IsZero() {
				v, err := stock.Variance(ctx, VarianceArgs{Pair: Pair{ret.WarehouseID, l.rl.ItemID}, Amount: variance, Ref: Ref{Type: "INV_CREDIT", ID: creditNoteID, Line: int(l.credit.LineNo)*10 + 2}, Variance: ppv.ID, Clearing: grni.ID})
				if err != nil {
					return err
				}
				moves = append(moves, v.MovementID)
			}
			mid, err := q.InsertMatch(ctx, inventorydb.InsertMatchParams{TenantID: p.TenantID, PropertyID: propertyID, ReceiptID: l.rl.ReceiptID, ReceiptLineNo: l.rl.ReceiptLineNo, BillID: l.match.BillID,
				BillLineNo: l.match.BillLineNo, QtyStock: l.rl.QtyStock.Neg(), BillCost: credit.Neg(), ReceiptValue: l.rl.Value.Neg(), Variance: variance, InventoryPart: decimal.Zero, PpvPart: variance,
				CreditNoteID: &creditNoteID, ReturnID: &returnID, ActorID: p.ActorID()})
			if err != nil {
				return err
			}
			if err := q.SetMatchClearing(ctx, inventorydb.SetMatchClearingParams{PropertyID: propertyID, ID: mid, Ids: moves}); err != nil {
				return err
			}
			if err := q.AddReceiptLineBilled(ctx, inventorydb.AddReceiptLineBilledParams{PropertyID: propertyID, ReceiptID: l.rl.ReceiptID, LineNo: l.rl.ReceiptLineNo, Qty: l.rl.QtyStock.Neg(), Value: l.rl.Value.Neg()}); err != nil {
				return err
			}
			touched = true
		}
		if touched {
			if err := s.refreshDraft(ctx, p, propertyID, ret.ReceiptID); err != nil {
				return err
			}
		}
		if err := q.SetReturnCreditNote(ctx, inventorydb.SetReturnCreditNoteParams{TenantID: p.TenantID, PropertyID: propertyID, ID: returnID, CreditNoteID: &creditNoteID}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.return_credit_linked", "inv_return", returnID, ret.ReturnNumber, nil, map[string]any{"credit_note_id": creditNoteID, "lines": len(links)}))
	})
	if err != nil {
		return Return{}, err
	}
	return s.returnOf(ctx, p, propertyID, returnID)
}
