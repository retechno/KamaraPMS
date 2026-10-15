package pos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/folios"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/pos/posdb"
)

func foliosLine(l posdb.ListBillLinesRow, dept *int64) folios.OutletLine {
	return folios.OutletLine{
		ChargeCodeID: l.ChargeCodeID, Quantity: l.Quantity, UnitPrice: l.UnitPrice, Discount: l.DiscountAmount, RuleProfileID: l.RuleProfileID, DepartmentID: dept,
		Description: lineName(l), LineUUID: uuidString(l.ClientUuid),
	}
}

func foliosComp(amount decimal.Decimal, name, reason, key string) folios.OutletPayment {
	return folios.OutletPayment{Method: folios.MethodComp, Amount: amount, Remarks: name + " · " + reason, Key: key}
}

var settlementMethods = []string{PayCash, PayCard, PayQRIS, PayBankTransfer, PayOther}

func checkKey(key string) []apperr.FieldError {
	if k := strings.TrimSpace(key); len(k) < 8 || len(k) > 80 {
		return []apperr.FieldError{fieldErr("Idempotency-Key", "INVALID_VALUE", "8 to 80 characters")}
	}
	return nil
}

// PayBill pays an open bill with one or more payments, all in one transaction under one Idempotency-Key. Any draft line is sent first, so the cashier of a counter pays a bill they have just taken.
//
//   - The payments must add up to the balance, or to the balance rounded as the outlet rounds cash when every payment is cash (a POS_ROUNDING line, no tax), or be less than the balance (a part:
//     the bill stays open and the table with it). More than the balance is refused: an overpayment in cash is change, which the cashier enters as tendered.
//   - A charge to a room is decided by the gate (lock, validity, outlets, limit) before anything else is written: a refusal writes the attempt and nothing more, and answers 409.
//   - A transfer to a company needs cityledger.transfer, the outlet's setting and the company's own (companies.pos_billing_allowed).
//   - Cash goes on the open shift of the cashier for the drawer of the outlet.
//   - The bill closes, with its folio, when its balance is exactly zero.
func (s *Service) PayBill(ctx context.Context, propertyID, billID int64, key string, in PayInput) (PayResult, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return PayResult{}, err
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return PayResult{}, err
	}
	fields := checkKey(key)
	if len(in.Payments) > maxPayLines {
		fields = append(fields, fieldErr("payments", "INVALID_VALUE", "at most 8 payments"))
	}
	var rooms, companies int
	sum := decimal.Zero
	allCash := len(in.Payments) > 0
	for i, l := range in.Payments {
		f := fmt.Sprintf("payments[%d]", i)
		switch {
		case l.Method == PayRoom:
			rooms++
			if l.StayID == nil {
				fields = append(fields, fieldErr(f+".stay_id", "REQUIRED", "the room"))
			}
		case l.Method == PayCompany:
			companies++
			if l.CompanyID == nil {
				fields = append(fields, fieldErr(f+".company_id", "REQUIRED", "the company"))
			}
		case slices.Contains(settlementMethods, l.Method):
		default:
			fields = append(fields, fieldErr(f+".method", "INVALID_VALUE", "CASH, CARD, QRIS, BANK_TRANSFER, OTHER, ROOM or COMPANY"))
		}
		if l.Method != PayCash {
			allCash = false
		}
		if !l.Amount.IsPositive() || l.Amount.Exponent() < -decimals {
			fields = append(fields, fieldErr(f+".amount", "INVALID_AMOUNT", "above zero, at most the decimals of the currency"))
		}
		if len([]rune(l.Reference)) > 100 || len([]rune(l.Label)) > 100 {
			fields = append(fields, fieldErr(f+".reference", "INVALID_VALUE", "at most 100 characters"))
		}
		sum = sum.Add(l.Amount)
	}
	if rooms > 1 || companies > 1 {
		fields = append(fields, fieldErr("payments", "INVALID_VALUE", "one charge to a room and one to a company at most"))
	}
	if len(fields) > 0 {
		return PayResult{}, invalid("payment", fields...)
	}
	pre, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return PayResult{}, err
	}
	if pre.FolioID == nil {
		return PayResult{}, apperr.Conflict("BILL_NOT_SYNCED", "the bill has no folio yet")
	}
	// a repeat of the request: the bill as it is, never a second payment
	if fid, found, err := s.ledger.OutletPayKeyFolio(ctx, p.TenantID, propertyID, key); err != nil {
		return PayResult{}, err
	} else if found {
		if fid != *pre.FolioID {
			return PayResult{}, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "the Idempotency-Key was already used for another bill")
		}
		return s.payResult(ctx, p, propertyID, billID, decimal.Zero, false)
	}
	if pre.Status != BillOpen {
		return PayResult{}, errBillNotOpen().WithContext("status", pre.Status)
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, pre.OutletID)
	if err != nil {
		return PayResult{}, err
	}
	var roomLine, companyLine *PayLine
	for i := range in.Payments {
		l := &in.Payments[i]
		switch l.Method {
		case PayRoom:
			if err := s.authz.Require(ctx, propertyID, auth.PermPOSChargeRoom); err != nil {
				return PayResult{}, err
			}
			if !o.AllowRoomCharge {
				return PayResult{}, apperr.Conflict("OUTLET_ROOM_CHARGE_NOT_ALLOWED", "this outlet does not charge to rooms")
			}
			roomLine = l
		case PayCompany:
			if err := s.authz.Require(ctx, propertyID, auth.PermCityLedgerTransfer); err != nil {
				return PayResult{}, err
			}
			if !o.AllowCompanyCharge {
				return PayResult{}, apperr.Conflict("OUTLET_COMPANY_CHARGE_NOT_ALLOWED", "this outlet does not bill companies")
			}
			companyLine = l
		case PayCash:
			if o.Drawer == nil {
				return PayResult{}, apperr.Conflict("OUTLET_NO_CASH", "this outlet takes no cash: it has no drawer")
			}
		}
	}
	var refusal *apperr.Error
	var result PayResult
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		bd := day.BusinessDate
		folioIDs := []int64{*pre.FolioID}
		var target folios.OutletTarget
		if roomLine != nil {
			if err := s.ledger.LockStayShared(ctx, propertyID, *roomLine.StayID); err != nil { // level 41, before the folios
				return err
			}
			if target, err = s.ledger.ResolveOutletTarget(ctx, p, propertyID, *roomLine.StayID); err != nil {
				return err
			}
			if target.FolioID == 0 {
				return apperr.Conflict("ROUTING_TARGET_CLOSED", "the folio this stay is billed to is closed: ask the front desk")
			}
			folioIDs = append(folioIDs, target.FolioID)
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, folioIDs...); err != nil { // level 42, one call, ascending
			return err
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		if b.Status != BillOpen {
			return errBillNotOpen().WithContext("status", b.Status)
		}
		var shift *int64
		if slices.ContainsFunc(in.Payments, func(l PayLine) bool { return l.Method == PayCash }) {
			if shift, err = s.ledger.PrepareOutletCash(ctx, propertyID, *o.Drawer); err != nil { // level 43
				return err
			}
		}
		if companyLine != nil {
			if err := s.ledger.PrepareOutletCompany(ctx, p, propertyID, *companyLine.CompanyID, companyLine.Amount); err != nil { // level 44
				return err
			}
		}
		// what is to pay: the balance of the folio and the quote of the drafts, before anything is written
		lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		bal, err := s.ledger.BillBalance(ctx, propertyID, *b.FolioID)
		if err != nil {
			return err
		}
		due := bal
		sold := 0
		for _, l := range lines {
			switch l.Status {
			case LineDraft:
				q, err := s.quote(ctx, propertyID, l.ChargeCodeID, l.Quantity, l.UnitPrice, l.DiscountAmount, l.RuleProfileID)
				if err != nil {
					return err
				}
				due = due.Add(q.Total)
				sold++
			case LineSent:
				sold++
			}
		}
		if sold == 0 {
			return apperr.Conflict("BILL_EMPTY", "the bill has no line")
		}
		rounding := decimal.Zero
		switch {
		case sum.Equal(due):
		case allCash && o.RoundingUnit > 0 && sum.Equal(RoundToUnit(due, o.RoundingUnit)):
			rounding = sum.Sub(due)
		case sum.LessThan(due):
			if sum.IsZero() && !due.IsZero() {
				return apperr.Conflict("PAYMENT_AMOUNT_MISMATCH", "the payments are less than the bill").WithContext("payable", due.String())
			}
		default:
			return apperr.Conflict("PAYMENT_AMOUNT_MISMATCH", "the payments are more than the bill: an overpayment in cash is change").
				WithContext("payable", due.String()).WithContext("cash_payable", RoundToUnit(due, o.RoundingUnit).String())
		}
		if sum.IsZero() && !due.IsZero() {
			return apperr.Conflict("PAYMENT_AMOUNT_MISMATCH", "the bill has a balance").WithContext("payable", due.String())
		}
		// the gate first: a refusal leaves the attempt and nothing else
		var charge folios.OutletRoomCharge
		if roomLine != nil {
			if roomLine.Amount.GreaterThan(due) {
				return apperr.Conflict("PAYMENT_AMOUNT_MISMATCH", "the charge to the room is more than the bill").WithContext("payable", due.String())
			}
			charge = folios.OutletRoomCharge{
				PropertyID: propertyID, BusinessDate: bd, BillFolioID: *b.FolioID, BillID: billID, BillNumber: deref(b.BillNumber), OutletID: o.ID, OutletName: o.Name,
				StayID: *roomLine.StayID, TargetFolioID: target.FolioID, Amount: roomLine.Amount, Source: folios.ChargeSourceCharge, Reason: "Outlet charge " + deref(b.BillNumber), Key: key + "#room",
			}
			ref, err := s.ledger.CheckOutletRoomCharge(ctx, p, charge)
			if err != nil {
				return err
			}
			if ref != nil {
				refusal = ref
				return nil // the attempt is the only write: it commits, then the refusal is answered
			}
		}
		if _, err := s.sendDrafts(ctx, p, propertyID, bd, b, o, newUUID()); err != nil {
			return err
		}
		if !rounding.IsZero() {
			dept := o.DepartmentID
			if _, err := s.ledger.RoundingLine(ctx, p, propertyID, bd, *b.FolioID, rounding, &dept, deref(b.BillNumber)); err != nil {
				return err
			}
		}
		if roomLine != nil {
			res, err := s.ledger.ChargeOutletBillToRoom(ctx, p, charge)
			if err != nil {
				return err
			}
			if res.Refusal != nil { // the gate said yes a moment ago under the same locks: cannot happen, and nothing is kept if it does
				return res.Refusal
			}
		}
		if companyLine != nil {
			if _, err := s.ledger.PostOutletCompanyTransfer(ctx, p, propertyID, bd, *b.FolioID, *companyLine.CompanyID, companyLine.Amount, companyLine.Reference, key+"#co"); err != nil {
				return err
			}
		}
		// the cash is one payment: one entry on the shift; the lines say who paid what in its remarks
		cash := decimal.Zero
		var cashLabels []string
		change := decimal.Zero
		tendered := decimal.Zero
		n := 0
		for _, l := range in.Payments {
			switch l.Method {
			case PayRoom, PayCompany:
				continue
			case PayCash:
				cash = cash.Add(l.Amount)
				if l.Label != "" {
					cashLabels = append(cashLabels, l.Label)
				}
				if l.Tendered != nil && l.Tendered.GreaterThan(l.Amount) {
					tendered = tendered.Add(*l.Tendered)
				} else {
					tendered = tendered.Add(l.Amount)
				}
				continue
			}
			if _, err := s.ledger.PostOutletPayment(ctx, p, propertyID, bd, *b.FolioID, folios.OutletPayment{Method: l.Method, Amount: l.Amount, Reference: l.Reference, Remarks: l.Label,
				Key: fmt.Sprintf("%s#%d", key, n)}, nil); err != nil {
				return err
			}
			n++
		}
		if cash.IsPositive() {
			if _, err := s.ledger.PostOutletPayment(ctx, p, propertyID, bd, *b.FolioID, folios.OutletPayment{Method: PayCash, Amount: cash, Remarks: strings.Join(cashLabels, ", "),
				ShiftID: shift, Key: fmt.Sprintf("%s#%d", key, n)}, nil); err != nil {
				return err
			}
			change = tendered.Sub(cash)
		}
		closed, err := s.closeIfSettled(ctx, p, propertyID, bd, b)
		if err != nil {
			return err
		}
		methods := make([]string, 0, len(in.Payments))
		for _, l := range in.Payments {
			methods = append(methods, l.Method+":"+l.Amount.String())
		}
		if err := s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.bill_paid", "pos_bill", billID, deref(b.BillNumber), nil, map[string]any{
			"payments": methods, "total": sum.String(), "rounding": rounding.String(), "closed": closed, "balance_before": due.String(),
		})); err != nil {
			return err
		}
		result = PayResult{Closed: closed, Change: change, OpenDrawer: cash.IsPositive()}
		return nil
	})
	if err != nil {
		return PayResult{}, err
	}
	if refusal != nil {
		return PayResult{}, refusal
	}
	bill, err := s.loadBill(ctx, p, propertyID, billID)
	if err != nil {
		return PayResult{}, err
	}
	result.Bill = bill
	result.Closed = bill.Status == BillClosed
	return result, nil
}

func (s *Service) payResult(ctx context.Context, p auth.Principal, propertyID, billID int64, change decimal.Decimal, drawer bool) (PayResult, error) {
	b, err := s.loadBill(ctx, p, propertyID, billID)
	return PayResult{Bill: b, Closed: b.Status == BillClosed, Change: change, OpenDrawer: drawer}, err
}

// -------------------------------------------------------------------------------------------------- split and merge

// SplitBill moves chosen lines of a bill to a new bill (a split by item). The new bill is made first, in a transaction of its own because it takes a number (the sequence is the last lock); then
// the lines are moved in a second one: a reversal on the first folio and a copy on the second for each sent line, nothing recomputed; drafts are re-pointed. ClientUUID is the uuid of the new
// bill: the request again returns it.
func (s *Service) SplitBill(ctx context.Context, propertyID, billID int64, in SplitInput) (Bill, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	u, fe := parseUUID("client_uuid", in.ClientUUID)
	if fe != nil {
		return Bill{}, invalid("split", *fe)
	}
	if len(in.LineIDs) == 0 {
		return Bill{}, invalid("split", fieldErr("line_ids", "REQUIRED", "the lines that go to the new bill"))
	}
	if existing, err := s.q(ctx).GetBillByUUID(ctx, posdb.GetBillByUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: u}); err == nil {
		return s.loadBill(ctx, p, propertyID, existing)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, err
	}
	src, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return Bill{}, err
	}
	if src.Status != BillOpen {
		return Bill{}, errBillNotOpen()
	}
	lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return Bill{}, err
	}
	live := 0
	for _, l := range lines {
		if l.Status != LineVoid {
			live++
		}
	}
	chosen := 0
	for _, id := range in.LineIDs {
		i := slices.IndexFunc(lines, func(l posdb.ListBillLinesRow) bool { return l.ID == id })
		if i < 0 || lines[i].Status == LineVoid {
			return Bill{}, invalid("split", fieldErr("line_ids", "BILL_LINE_NOT_FOUND", "a line of this bill that is not voided"))
		}
		chosen++
	}
	if chosen >= live {
		return Bill{}, apperr.Conflict("SPLIT_TAKES_ALL", "a split leaves at least one line on the bill")
	}
	// 1. the new bill (its own transaction)
	o, err := s.outletOf(ctx, p.TenantID, propertyID, src.OutletID)
	if err != nil {
		return Bill{}, err
	}
	nb, err := s.OpenBill(ctx, propertyID, src.OutletID, OpenBillInput{
		ClientUUID: in.ClientUUID, OrderType: src.OrderType, TableID: src.TableID, GuestCount: optInt(src.GuestCount), TargetStayID: src.TargetStayID, CustomerName: deref(src.CustomerName),
		Note: "split of " + deref(src.BillNumber),
	})
	_ = o
	if err != nil {
		return Bill{}, err
	}
	// 2. the move
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, *src.FolioID, *nb.FolioID); err != nil {
			return err
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		if b.Status != BillOpen {
			return errBillNotOpen()
		}
		return s.moveLines(ctx, p, propertyID, day.BusinessDate, b, nb.ID, *nb.FolioID, in.LineIDs, "Split of "+deref(b.BillNumber)+" to "+nb.Number)
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, nb.ID)
}

// moveLines moves lines of the bill b to another bill whose folio is locked: the sent ones as a transfer by item (a reversal and a copy), the drafts by pointing them at the other bill.
func (s *Service) moveLines(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, b posdb.GetBillRow, toBillID, toFolioID int64, lineIDs []int64, reason string) error {
	q := s.q(ctx)
	lines, err := s.billLines(ctx, p.TenantID, propertyID, b.ID)
	if err != nil {
		return err
	}
	var itemIDs []int64
	var sent []posdb.ListBillLinesRow
	for _, l := range lines {
		if !slices.Contains(lineIDs, l.ID) {
			continue
		}
		switch l.Status {
		case LineSent:
			itemIDs = append(itemIDs, *l.FolioItemID)
			sent = append(sent, l)
		case LineDraft:
			if err := q.MoveLineToBill(ctx, posdb.MoveLineToBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.ID, BillID: toBillID}); err != nil {
				return err
			}
		}
	}
	if len(itemIDs) > 0 {
		moved, err := s.ledger.MoveOutletItems(ctx, p, propertyID, bd, *b.FolioID, toFolioID, itemIDs, reason)
		if err != nil {
			return err
		}
		for _, m := range moved {
			i := slices.IndexFunc(sent, func(l posdb.ListBillLinesRow) bool { return *l.FolioItemID == m.OldItemID })
			if i < 0 {
				continue
			}
			newItem := m.NewItemID
			if err := q.MoveLineToBill(ctx, posdb.MoveLineToBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: sent[i].ID, BillID: toBillID, FolioItemID: &newItem}); err != nil {
				return err
			}
		}
	}
	return nil
}

// MergeBill merges the bill of the path (B) into another open bill of the same outlet (A): its drafts are re-pointed, its sent lines move by item with nothing recomputed, and B becomes MERGED with
// its folio closed at zero. A payment already taken on B stops it: void or refund it first.
func (s *Service) MergeBill(ctx context.Context, propertyID, billID int64, in MergeInput) (Bill, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if in.IntoBillID < 1 || in.IntoBillID == billID {
		return Bill{}, invalid("merge", fieldErr("into_bill_id", "INVALID_VALUE", "another open bill of the outlet"))
	}
	preB, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return Bill{}, err
	}
	preA, err := s.billRow(ctx, p.TenantID, propertyID, in.IntoBillID)
	if err != nil {
		return Bill{}, err
	}
	if preA.FolioID == nil || preB.FolioID == nil {
		return Bill{}, apperr.Conflict("BILL_NOT_SYNCED", "the bill has no folio yet")
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, *preA.FolioID, *preB.FolioID); err != nil {
			return err
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		a, err := s.billRow(ctx, p.TenantID, propertyID, in.IntoBillID)
		if err != nil {
			return err
		}
		if b.Status != BillOpen || a.Status != BillOpen {
			return errBillNotOpen()
		}
		if a.OutletID != b.OutletID {
			return apperr.Conflict("MERGE_OTHER_OUTLET", "bills merge within one outlet")
		}
		pays, err := s.ledger.ListBillPayments(ctx, p, propertyID, *b.FolioID)
		if err != nil {
			return err
		}
		if len(pays) > 0 {
			return apperr.Conflict("BILL_HAS_PAYMENTS", "the bill has payments: void or refund them before merging it")
		}
		rcs, err := s.q(ctx).ListBillRoomCharges(ctx, posdb.ListBillRoomChargesParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: &billID})
		if err != nil {
			return err
		}
		if len(rcs) > 0 {
			return apperr.Conflict("BILL_HAS_PAYMENTS", "the bill was charged to a room: return that before merging it")
		}
		lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		var ids []int64
		for _, l := range lines {
			if l.Status != LineVoid {
				ids = append(ids, l.ID)
			}
		}
		if err := s.moveLines(ctx, p, propertyID, day.BusinessDate, b, a.ID, *a.FolioID, ids, "Merge of "+deref(b.BillNumber)+" into "+deref(a.BillNumber)); err != nil {
			return err
		}
		bal, err := s.ledger.BillBalance(ctx, propertyID, *b.FolioID)
		if err != nil {
			return err
		}
		if !bal.IsZero() {
			return apperr.Conflict("BILL_HAS_BALANCE", "the bill still has a balance after its lines moved").WithContext("balance", bal.String())
		}
		if err := s.ledger.CloseBillFolio(ctx, p, propertyID, *b.FolioID, day.BusinessDate); err != nil {
			return err
		}
		bd, into := day.BusinessDate, a.ID
		if _, err := s.q(ctx).CloseBill(ctx, posdb.CloseBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: billID, Status: BillMerged, At: s.clock.Now(), BusinessDate: &bd, MergedIntoBillID: &into, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.bills_merged", "pos_bill", billID, deref(b.BillNumber), nil, map[string]any{"into": a.ID, "into_number": deref(a.BillNumber), "lines": len(ids)}))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, in.IntoBillID)
}

// -------------------------------------------------------------------------------------------------- charge to a room: lookup, return, move

// RoomLookupRow is a room an outlet may charge, with the lock and the limit and nothing of the folio.
type RoomLookupRow struct {
	StayID       int64            `json:"stay_id"`
	Room         string           `json:"room"`
	Guest        string           `json:"guest"`
	TargetType   string           `json:"target_type"`
	TargetNumber string           `json:"target_number"`
	Routed       bool             `json:"routed"`
	Open         bool             `json:"open"`
	Reason       string           `json:"reason,omitempty"`
	Limit        *decimal.Decimal `json:"limit"`
	Used         *decimal.Decimal `json:"used"`
	Remaining    *decimal.Decimal `json:"remaining"`
}

// RoomLookup answers the in-house rooms that match a number or a guest's name, each with where a charge of the outlet would land and what the lock says (pos.charge_room). It is advice: the gate
// decides again when the bill is charged.
func (s *Service) RoomLookup(ctx context.Context, propertyID, outletID int64, q string) ([]RoomLookupRow, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.needBilling(); err != nil {
		return nil, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSChargeRoom); err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if n := len([]rune(q)); n < 1 || n > 40 {
		return nil, apperr.Invalid("the search is invalid", fieldErr("q", "INVALID_VALUE", "1 to 40 characters"))
	}
	if _, err := s.outletOf(ctx, p.TenantID, propertyID, outletID); err != nil {
		return nil, err
	}
	day, err := s.days.CurrentBusinessDay(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).LookupRooms(ctx, posdb.LookupRoomsParams{TenantID: p.TenantID, PropertyID: propertyID, Q: q})
	if err != nil {
		return nil, err
	}
	out := make([]RoomLookupRow, 0, len(rows))
	for _, r := range rows {
		a, err := s.ledger.AdviseRoomCharge(ctx, p, propertyID, r.StayID, outletID, day.BusinessDate)
		if err != nil {
			return nil, err
		}
		out = append(out, RoomLookupRow{StayID: r.StayID, Room: r.RoomNumber, Guest: r.GuestName, TargetType: a.TargetType, TargetNumber: a.TargetNumber, Routed: a.Routed, Open: a.Open,
			Reason: a.Reason, Limit: a.Limit, Used: a.Used, Remaining: a.Remaining})
	}
	return out, nil
}

// ReturnRoomCharge sends a charge to a room back to its bill (the return of document 30 4.5): the usage of the limit goes back down. It needs folio.reverse, the approval of the kind
// FOLIO_TRANSFER, and the bill must be open (reopen it first).
func (s *Service) ReturnRoomCharge(ctx context.Context, propertyID, chargeID int64, in RoomChargeCorrectionInput) (Bill, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermFolioReverse); err != nil {
		return Bill{}, err
	}
	reason, fields := requireReason(in.Reason)
	if len(fields) > 0 {
		return Bill{}, invalid("return", fields...)
	}
	ch, err := s.q(ctx).GetRoomChargeForBill(ctx, posdb.GetRoomChargeForBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeID})
	if err != nil {
		return Bill{}, orNotFound(err, notFound("ROOM_CHARGE_NOT_FOUND", "outlet charge"))
	}
	if ch.BillID == nil || ch.FromFolioID == nil {
		return Bill{}, apperr.Conflict("ROOM_CHARGE_NO_BILL", "the charge has no bill to go back to")
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindFolioTransfer, Amount: ch.Amount})
	if err != nil {
		return Bill{}, err
	}
	billID := *ch.BillID
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, *ch.FromFolioID, ch.ToFolioID); err != nil {
			return err
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		if b.Status != BillOpen {
			return errBillNotOpen().WithContext("status", b.Status)
		}
		ret, err := s.ledger.ReturnOutletRoomCharge(ctx, p, propertyID, day.BusinessDate, chargeID, reason, approval)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.room_charge_returned", "pos_bill", billID, deref(b.BillNumber), nil, approval.AuditFields(map[string]any{
			"charge_id": chargeID, "amount": ret.Amount.String(), "reason": reason, "from_folio_id": ret.FromFolioID,
		})))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// MoveRoomCharge moves a charge to another room: the return and the new charge in one transaction, where the new charge goes through every check of the gate at the target (lock, validity,
// outlets, limit), for everyone, the holder of folio.transfer_any included. When the gate refuses nothing moves and the first room keeps the charge (34-pos.md D21).
func (s *Service) MoveRoomCharge(ctx context.Context, propertyID, chargeID int64, in RoomChargeCorrectionInput) (Bill, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermFolioReverse); err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSChargeRoom); err != nil {
		return Bill{}, err
	}
	reason, fields := requireReason(in.Reason)
	if in.StayID == nil {
		fields = append(fields, fieldErr("stay_id", "REQUIRED", "the room the charge goes to"))
	}
	if len(fields) > 0 {
		return Bill{}, invalid("move", fields...)
	}
	ch, err := s.q(ctx).GetRoomChargeForBill(ctx, posdb.GetRoomChargeForBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeID})
	if err != nil {
		return Bill{}, orNotFound(err, notFound("ROOM_CHARGE_NOT_FOUND", "outlet charge"))
	}
	if ch.BillID == nil || ch.FromFolioID == nil {
		return Bill{}, apperr.Conflict("ROOM_CHARGE_NO_BILL", "the charge has no bill to go back to")
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindFolioTransfer, Amount: ch.Amount})
	if err != nil {
		return Bill{}, err
	}
	billID := *ch.BillID
	var refusal *apperr.Error
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.ledger.LockStayShared(ctx, propertyID, *in.StayID); err != nil {
			return err
		}
		target, err := s.ledger.ResolveOutletTarget(ctx, p, propertyID, *in.StayID)
		if err != nil {
			return err
		}
		if target.FolioID == 0 {
			return apperr.Conflict("ROUTING_TARGET_CLOSED", "the folio this stay is billed to is closed: ask the front desk")
		}
		if target.FolioID == ch.ToFolioID {
			return apperr.Conflict("MOVE_SAME_FOLIO", "the charge is on that folio already")
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, *ch.FromFolioID, ch.ToFolioID, target.FolioID); err != nil {
			return err
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		if b.Status != BillOpen {
			return errBillNotOpen().WithContext("status", b.Status)
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		charge := folios.OutletRoomCharge{
			PropertyID: propertyID, BusinessDate: day.BusinessDate, BillFolioID: *b.FolioID, BillID: billID, BillNumber: deref(b.BillNumber), OutletID: o.ID, OutletName: o.Name,
			StayID: *in.StayID, TargetFolioID: target.FolioID, Amount: ch.Amount, Source: folios.ChargeSourceMove, Reason: reason,
		}
		ref, err := s.ledger.CheckOutletRoomCharge(ctx, p, charge)
		if err != nil {
			return err
		}
		if ref != nil {
			refusal = ref
			return nil
		}
		if _, err := s.ledger.ReturnOutletRoomCharge(ctx, p, propertyID, day.BusinessDate, chargeID, reason, approval); err != nil {
			return err
		}
		res, err := s.ledger.ChargeOutletBillToRoom(ctx, p, charge)
		if err != nil {
			return err
		}
		if res.Refusal != nil {
			return res.Refusal
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.room_charge_moved", "pos_bill", billID, deref(b.BillNumber), nil, approval.AuditFields(map[string]any{
			"charge_id": chargeID, "new_charge_id": res.ChargeID, "amount": ch.Amount.String(), "to_folio_id": target.FolioID, "reason": reason,
		})))
	})
	if err != nil {
		return Bill{}, err
	}
	if refusal != nil {
		return Bill{}, refusal
	}
	return s.loadBill(ctx, p, propertyID, billID)
}
