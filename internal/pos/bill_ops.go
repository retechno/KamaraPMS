package pos

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/money"
	"kamarapms/internal/pos/posdb"
)

// The use cases of a bill that touch money: send, void, discount, complimentary, pay, split, merge, reopen and the corrections of a charge to a room (docs/architecture/34-pos.md 4 to 7).
// Every one opens one transaction, takes the open business day in share mode first, then the folios in one call, ascending (level 42), and writes the ledger only through internal/folios.

func newUUID() pgtype.UUID {
	var u pgtype.UUID
	_, _ = rand.Read(u.Bytes[:])
	u.Bytes[6] = (u.Bytes[6] & 0x0f) | 0x40
	u.Bytes[8] = (u.Bytes[8] & 0x3f) | 0x80
	u.Valid = true
	return u
}

func (s *Service) billLines(ctx context.Context, tenantID, propertyID, billID int64) ([]posdb.ListBillLinesRow, error) {
	return s.q(ctx).ListBillLines(ctx, posdb.ListBillLinesParams{TenantID: tenantID, PropertyID: propertyID, BillID: billID})
}

func lineName(l posdb.ListBillLinesRow) string {
	opts := decodeOptions(l.Options)
	if len(opts) == 0 {
		return l.ProductName
	}
	names := make([]string, 0, len(opts))
	for _, o := range opts {
		if o.Qty > 1 {
			names = append(names, fmt.Sprintf("%s ×%d", o.Name, o.Qty))
		} else {
			names = append(names, o.Name)
		}
	}
	return l.ProductName + " (" + strings.Join(names, ", ") + ")"
}

func optionsText(l posdb.ListBillLinesRow) *string {
	opts := decodeOptions(l.Options)
	if len(opts) == 0 {
		return nil
	}
	names := make([]string, 0, len(opts))
	for _, o := range opts {
		if o.Qty > 1 {
			names = append(names, fmt.Sprintf("%s ×%d", o.Name, o.Qty))
		} else {
			names = append(names, o.Name)
		}
	}
	t := strings.Join(names, ", ")
	return &t
}

// callerApproves is whether the person acting holds the approver permission themselves.
func (s *Service) callerApproves(ctx context.Context, propertyID int64) bool {
	return s.authz.Require(ctx, propertyID, auth.PermPOSApprove) == nil
}

func requireReason(reason string) (string, []apperr.FieldError) {
	r := strings.TrimSpace(reason)
	if n := len([]rune(r)); n < 1 || n > maxReasonLen {
		return "", []apperr.FieldError{fieldErr("reason", "REQUIRED", "a reason of 1 to 200 characters")}
	}
	return r, nil
}

// -------------------------------------------------------------------------------------------------- send

// sendDrafts posts the draft lines of a bill, in the order they were made: one CHARGE for each, a round, and one ticket for each station of an outlet that has a kitchen or a bar. The folio is
// locked FOR UPDATE by the caller. It answers how many lines were sent.
func (s *Service) sendDrafts(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, b posdb.GetBillRow, o Outlet, round pgtype.UUID) (int, error) {
	q := s.q(ctx)
	lines, err := s.billLines(ctx, p.TenantID, propertyID, b.ID)
	if err != nil {
		return 0, err
	}
	var drafts []posdb.ListBillLinesRow
	for _, l := range lines {
		if l.Status == LineDraft {
			drafts = append(drafts, l)
		}
	}
	if len(drafts) == 0 {
		return 0, nil
	}
	no, err := q.NextRoundNo(ctx, posdb.NextRoundNoParams{PropertyID: propertyID, BillID: b.ID})
	if err != nil {
		return 0, err
	}
	roundID, err := q.InsertRound(ctx, posdb.InsertRoundParams{TenantID: p.TenantID, PropertyID: propertyID, BillID: b.ID, RoundNo: no, ClientUuid: round, ActorID: p.ActorID()})
	if err != nil {
		return 0, err
	}
	tickets := map[int64]int64{} // station id (0 = none) -> ticket id
	now := s.clock.Now()
	dept := o.DepartmentID
	for _, l := range drafts {
		posted, err := s.ledger.PostOutletLine(ctx, p, propertyID, bd, *b.FolioID, foliosLine(l, &dept))
		if err != nil {
			return 0, err
		}
		if err := q.MarkLineSent(ctx, posdb.MarkLineSentParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.ID, RoundID: &roundID, FolioItemID: &posted.ItemID, ActorID: p.ActorID(), At: now}); err != nil {
			return 0, err
		}
		if err := s.queueSale(ctx, p, propertyID, bd, o, l); err != nil { // the line is a sale: the stock is told in this transaction (5.6)
			return 0, err
		}
		if o.OrderMode == "DIRECT" {
			continue
		}
		var st int64
		if l.StationID != nil {
			st = *l.StationID
		}
		tid, ok := tickets[st]
		if !ok {
			var sp *int64
			if st != 0 {
				sp = &st
			}
			if tid, err = q.InsertTicket(ctx, posdb.InsertTicketParams{TenantID: p.TenantID, PropertyID: propertyID, RoundID: roundID, BillID: b.ID, StationID: sp, Kind: "NEW"}); err != nil {
				return 0, err
			}
			tickets[st] = tid
		}
		if err := q.InsertTicketLine(ctx, posdb.InsertTicketLineParams{TenantID: p.TenantID, PropertyID: propertyID, TicketID: tid, BillLineID: l.ID, Quantity: l.Quantity, Name: l.ProductName,
			OptionsText: optionsText(l), Note: l.Note}); err != nil {
			return 0, err
		}
	}
	for st, tid := range tickets {
		var sp *int64
		if st != 0 {
			id := st
			sp = &id
		}
		if err := s.queueTicketJob(ctx, p, propertyID, o, tid, sp, "TICKET"); err != nil {
			return 0, err
		}
	}
	return len(drafts), nil
}

// SendBill sends the draft lines of an open bill: they are posted to the ledger, a round and its tickets are written. The same ClientUUID again sends nothing twice.
func (s *Service) SendBill(ctx context.Context, propertyID, billID int64, in SendInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	u, fe := parseUUID("client_uuid", in.ClientUUID)
	if fe != nil {
		return Bill{}, invalid("round", *fe)
	}
	if r, err := s.q(ctx).GetRoundByUUID(ctx, posdb.GetRoundByUUIDParams{TenantID: p.TenantID, PropertyID: propertyID, ClientUuid: u}); err == nil {
		if r.BillID != billID {
			return Bill{}, apperr.New(apperr.KindInvalid, "IDEMPOTENCY_KEY_REUSED", "this round was made for another bill")
		}
		return s.loadBill(ctx, p, propertyID, billID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForUpdate)
		if err != nil {
			return err
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		n, err := s.sendDrafts(ctx, p, propertyID, day.BusinessDate, b, o, u)
		if err != nil {
			return err
		}
		if n == 0 {
			return apperr.Conflict("NOTHING_TO_SEND", "the bill has no draft line to send")
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.round_sent", "pos_bill", billID, deref(b.BillNumber), nil, map[string]any{"lines": n, "round_uuid": in.ClientUUID}))
	})
	if err != nil {
		if apperr.IsCode(err, "DUPLICATE_REQUEST") {
			return s.loadBill(ctx, p, propertyID, billID)
		}
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// -------------------------------------------------------------------------------------------------- void a sent line

// VoidLine voids a sent line: a reversal of its ledger row on the day it was sold, an exact adjustment of today on a later day, a VOID ticket, and the approval of the kind POS_VOID. A draft
// line is deleted instead, without an approval.
func (s *Service) VoidLine(ctx context.Context, propertyID, billID, lineID int64, in VoidLineInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSVoid); err != nil {
		return Bill{}, err
	}
	reason, fields := requireReason(in.Reason)
	if len(fields) > 0 {
		return Bill{}, invalid("void", fields...)
	}
	pre, err := s.billLines(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return Bill{}, err
	}
	var target *posdb.ListBillLinesRow
	for i := range pre {
		if pre[i].ID == lineID {
			target = &pre[i]
		}
	}
	if target == nil {
		return Bill{}, notFound("BILL_LINE_NOT_FOUND", "bill line")
	}
	if target.Status != LineSent {
		return Bill{}, apperr.Conflict("BILL_LINE_NOT_SENT", "only a sent line is voided").WithContext("status", target.Status)
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindPOSVoid, Amount: target.ItemTotal.Abs(), Own: s.callerApproves(ctx, propertyID)})
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForUpdate)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		l, err := q.GetBillLine(ctx, posdb.GetBillLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID})
		if err != nil || l.BillID != billID {
			return orNotFound(err, notFound("BILL_LINE_NOT_FOUND", "bill line"))
		}
		if l.Status != LineSent || l.FolioItemID == nil {
			return apperr.Conflict("BILL_LINE_NOT_SENT", "only a sent line is voided").WithContext("status", l.Status)
		}
		posted, reversed, err := s.ledger.VoidOutletItem(ctx, p, propertyID, day.BusinessDate, *b.FolioID, *l.FolioItemID, reason, approval)
		if err != nil {
			return err
		}
		by := approval.UserID()
		if err := q.MarkLineVoid(ctx, posdb.MarkLineVoidParams{TenantID: p.TenantID, PropertyID: propertyID, ID: lineID, VoidedItemID: &posted.ItemID, ActorID: p.ActorID(), At: s.clock.Now(), ApprovedBy: &by, Reason: &reason}); err != nil {
			return err
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		if err := s.queueVoid(ctx, p, propertyID, day.BusinessDate, o, lineID, target.SentAt); err != nil { // the stock gets the goods back, or the cost moves to waste (5.6)
			return err
		}
		if o.OrderMode != "DIRECT" && l.RoundID != nil {
			if err := q.StrikeTicketLines(ctx, posdb.StrikeTicketLinesParams{TenantID: p.TenantID, PropertyID: propertyID, BillLineID: lineID}); err != nil {
				return err
			}
			tid, err := q.InsertTicket(ctx, posdb.InsertTicketParams{TenantID: p.TenantID, PropertyID: propertyID, RoundID: *l.RoundID, BillID: billID, StationID: l.StationID, Kind: "VOID"})
			if err != nil {
				return err
			}
			if err := q.InsertTicketLine(ctx, posdb.InsertTicketLineParams{TenantID: p.TenantID, PropertyID: propertyID, TicketID: tid, BillLineID: lineID, Quantity: l.Quantity, Name: l.ProductName, Note: &reason, IsVoid: true}); err != nil {
				return err
			}
			if err := s.queueTicketJob(ctx, p, propertyID, o, tid, l.StationID, "TICKET"); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.line_voided", "pos_bill", billID, deref(b.BillNumber), nil, approval.AuditFields(map[string]any{
			"line_id": lineID, "product": l.ProductName, "quantity": l.Quantity.String(), "amount": posted.Total.Abs().String(), "reason": reason, "by_reversal": reversed, "item_id": posted.ItemID,
		})))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// -------------------------------------------------------------------------------------------------- discount

type discountTarget struct {
	line   posdb.ListBillLinesRow
	remain decimal.Decimal // the base that can still be discounted
	part   decimal.Decimal // the part of the discount that falls on it
}

// remainingBase is the base of a line that a discount can still take: the whole base of a draft (a discount on a draft replaces the one it has), and what a sent line was posted at less what has
// been discounted of it already.
func remainingBase(l posdb.ListBillLinesRow, decimals int32) decimal.Decimal {
	switch l.Status {
	case LineDraft:
		return money.Round(l.UnitPrice.Mul(l.Quantity), decimals)
	case LineSent:
		return l.ItemBase.Sub(l.ItemDiscount).Add(l.AdjustedNet) // adjusted_net is negative
	}
	return decimal.Zero
}

// ApplyDiscount gives a discount on a line or on the bill: a percentage or an amount of the base, with a reason. It is allocated to the lines by base with the largest remainder method, so the
// parts add up to the amount exactly. A draft line takes it as its discount (the engine then computes tax and service on the discounted net); a sent line takes a negative adjustment, which is
// the same total. Up to the share of the base the outlet lets its cashier give, the holder of pos.discount needs no approver; above it the approval POS_DISCOUNT is asked.
func (s *Service) ApplyDiscount(ctx context.Context, propertyID, billID int64, in DiscountInput) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSDiscount); err != nil {
		return Bill{}, err
	}
	reason, fields := requireReason(in.Reason)
	if in.Scope != "LINE" && in.Scope != "BILL" {
		fields = append(fields, fieldErr("scope", "INVALID_VALUE", "LINE or BILL"))
	}
	if (in.Percent == nil) == (in.Amount == nil) {
		fields = append(fields, fieldErr("percent", "INVALID_VALUE", "a percent or an amount, not both"))
	}
	if in.Percent != nil && (in.Percent.IsNegative() || in.Percent.GreaterThan(decimal.NewFromInt(100))) {
		fields = append(fields, fieldErr("percent", "INVALID_VALUE", "0 to 100"))
	}
	if in.Amount != nil && !in.Amount.IsPositive() {
		fields = append(fields, fieldErr("amount", "INVALID_VALUE", "above zero"))
	}
	if in.Scope == "LINE" && in.LineID == nil {
		fields = append(fields, fieldErr("line_id", "REQUIRED", "the line"))
	}
	if len(fields) > 0 {
		return Bill{}, invalid("discount", fields...)
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return Bill{}, err
	}
	// before the transaction (the approval is verified outside it): what would be discounted, without a lock
	pre, err := s.discountPlan(ctx, p, propertyID, billID, in, decimals)
	if err != nil {
		return Bill{}, err
	}
	var approval iam.Approval
	if pre.needsApproval {
		if approval, err = s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindPOSDiscount, Amount: pre.total, Own: s.callerApproves(ctx, propertyID)}); err != nil {
			return Bill{}, err
		}
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForUpdate)
		if err != nil {
			return err
		}
		plan, err := s.discountPlan(ctx, p, propertyID, billID, in, decimals)
		if err != nil {
			return err
		}
		if plan.needsApproval && approval.IsZero() {
			return apperr.New(apperr.KindConflict, "TOTAL_CHANGED", "the bill changed meanwhile: ask again")
		}
		q := s.q(ctx)
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		dept := o.DepartmentID
		for _, t := range plan.targets {
			if !t.part.IsPositive() {
				continue
			}
			switch t.line.Status {
			case LineDraft:
				var pct *decimal.Decimal
				if in.Scope == "LINE" && in.Percent != nil {
					pct = in.Percent
				}
				if _, err := q.SetLineDiscount(ctx, posdb.SetLineDiscountParams{TenantID: p.TenantID, PropertyID: propertyID, ID: t.line.ID, BillID: billID, DiscountPct: pct, DiscountAmount: t.part,
					DiscountReason: &reason, ActorID: p.ActorID(), IsComp: t.line.IsComp}); err != nil {
					return err
				}
			case LineSent:
				if _, err := s.ledger.AdjustOutletItem(ctx, p, propertyID, day.BusinessDate, *b.FolioID, *t.line.FolioItemID, t.part, t.line.RuleProfileID, &dept, reason, approval); err != nil {
					return err
				}
			}
		}
		fields := map[string]any{"scope": in.Scope, "amount": plan.total.String(), "reason": reason, "lines": len(plan.targets)}
		if in.Percent != nil {
			fields["percent"] = in.Percent.String()
		}
		if approval.IsZero() {
			fields["within_limit"] = true
		} else {
			fields = approval.AuditFields(fields)
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.discount_given", "pos_bill", billID, deref(b.BillNumber), nil, fields))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

type discountPlanResult struct {
	targets       []discountTarget
	total         decimal.Decimal
	needsApproval bool
}

func (s *Service) discountPlan(ctx context.Context, p auth.Principal, propertyID, billID int64, in DiscountInput, decimals int32) (discountPlanResult, error) {
	b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return discountPlanResult{}, err
	}
	if b.Status != BillOpen {
		return discountPlanResult{}, errBillNotOpen()
	}
	o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
	if err != nil {
		return discountPlanResult{}, err
	}
	lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return discountPlanResult{}, err
	}
	var targets []discountTarget
	billBase := decimal.Zero
	for _, l := range lines {
		if l.Status == LineVoid || l.IsComp {
			continue
		}
		rb := remainingBase(l, decimals)
		billBase = billBase.Add(rb)
		if in.Scope == "LINE" && l.ID != *in.LineID {
			continue
		}
		if rb.IsPositive() {
			targets = append(targets, discountTarget{line: l, remain: rb})
		}
	}
	if len(targets) == 0 {
		return discountPlanResult{}, apperr.Conflict("NOTHING_TO_DISCOUNT", "there is nothing on the bill that can be discounted")
	}
	sum := decimal.Zero
	bases := make([]decimal.Decimal, len(targets))
	for i, t := range targets {
		sum = sum.Add(t.remain)
		bases[i] = t.remain
	}
	var total decimal.Decimal
	if in.Percent != nil {
		total = money.Round(sum.Mul(*in.Percent).Div(decimal.NewFromInt(100)), decimals)
	} else {
		total = *in.Amount
		if total.Exponent() < -decimals {
			return discountPlanResult{}, invalid("discount", fieldErr("amount", "INVALID_VALUE", "more decimals than the currency has"))
		}
	}
	if total.GreaterThan(sum) {
		return discountPlanResult{}, apperr.Conflict("DISCOUNT_EXCEEDS_BASE", "the discount is more than what can be discounted").WithContext("base", sum.String())
	}
	parts := Allocate(total, bases, decimals)
	if parts == nil {
		return discountPlanResult{}, apperr.Conflict("DISCOUNT_EXCEEDS_BASE", "the discount is more than what can be discounted").WithContext("base", sum.String())
	}
	for i := range targets {
		targets[i].part = parts[i]
	}
	// within what the outlet lets its cashier give, as a share of the base of the bill
	needs := true
	if o.MaxDiscountPctNoApproval.IsPositive() && billBase.IsPositive() {
		pct := total.Mul(decimal.NewFromInt(100)).Div(billBase)
		needs = pct.GreaterThan(o.MaxDiscountPctNoApproval)
	}
	return discountPlanResult{targets: targets, total: total, needsApproval: needs}, nil
}

// -------------------------------------------------------------------------------------------------- close, void, reopen

// closeIfSettled closes the folio and the bill when the balance is exactly zero and nothing is waiting to be sent. The folio is locked FOR UPDATE by the caller.
func (s *Service) closeIfSettled(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, b posdb.GetBillRow) (bool, error) {
	bal, err := s.ledger.BillBalance(ctx, propertyID, *b.FolioID)
	if err != nil {
		return false, err
	}
	if !bal.IsZero() {
		return false, nil
	}
	lines, err := s.billLines(ctx, p.TenantID, propertyID, b.ID)
	if err != nil {
		return false, err
	}
	sold := 0
	for _, l := range lines {
		if l.Status == LineDraft {
			return false, nil
		}
		if l.Status == LineSent {
			sold++
		}
	}
	if sold == 0 {
		return false, nil // an empty bill is voided, not paid
	}
	if err := s.ledger.CloseBillFolio(ctx, p, propertyID, *b.FolioID, bd); err != nil {
		return false, err
	}
	n, err := s.q(ctx).CloseBill(ctx, posdb.CloseBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: b.ID, Status: BillClosed, At: s.clock.Now(), BusinessDate: &bd, ActorID: p.ActorID()})
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, errBillNotOpen()
	}
	return true, nil
}

// VoidBill voids a bill that sold nothing and took nothing: its drafts go with it and its folio closes at zero.
func (s *Service) VoidBill(ctx context.Context, propertyID, billID int64) (Bill, error) {
	p, err := s.orderer(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForUpdate)
		if err != nil {
			return err
		}
		lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		for _, l := range lines {
			if l.Status != LineDraft {
				return apperr.Conflict("BILL_HAS_SOLD_LINES", "a bill that sold something is paid or its lines are voided")
			}
		}
		bal, err := s.ledger.BillBalance(ctx, propertyID, *b.FolioID)
		if err != nil {
			return err
		}
		if !bal.IsZero() {
			return apperr.Conflict("BILL_HAS_BALANCE", "the bill has a balance")
		}
		for _, l := range lines {
			if _, err := s.q(ctx).DeleteDraftLine(ctx, posdb.DeleteDraftLineParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.ID, BillID: billID}); err != nil {
				return err
			}
		}
		if err := s.ledger.CloseBillFolio(ctx, p, propertyID, *b.FolioID, day.BusinessDate); err != nil {
			return err
		}
		bd := day.BusinessDate
		if _, err := s.q(ctx).CloseBill(ctx, posdb.CloseBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: billID, Status: BillVoid, At: s.clock.Now(), BusinessDate: &bd, ActorID: p.ActorID()}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: "pos.bill_voided",
			EntityType: "pos_bill", EntityID: billID, EntityLabel: deref(b.BillNumber)})
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// ReopenBill opens a closed bill again: folio.reopen on the person, the approval POS_REOPEN (the sum of what the bill sold), a reason. The corrections that follow are posted today.
func (s *Service) ReopenBill(ctx context.Context, propertyID, billID int64, in ReopenInput) (Bill, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermFolioReopen); err != nil {
		return Bill{}, err
	}
	reason, fields := requireReason(in.Reason)
	if len(fields) > 0 {
		return Bill{}, invalid("reopening", fields...)
	}
	pre, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return Bill{}, err
	}
	if pre.Status != BillClosed || pre.FolioID == nil {
		return Bill{}, apperr.Conflict("BILL_NOT_CLOSED", "only a closed bill is reopened").WithContext("status", pre.Status)
	}
	sums, err := s.q(ctx).BillLedgerSums(ctx, posdb.BillLedgerSumsParams{PropertyID: propertyID, FolioID: *pre.FolioID})
	if err != nil {
		return Bill{}, err
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindPOSReopen, Amount: sums.Sold.Abs(), Own: s.callerApproves(ctx, propertyID)})
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		if err := s.ledger.LockFolios(ctx, propertyID, db.ForUpdate, *pre.FolioID); err != nil {
			return err
		}
		b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		if b.Status != BillClosed {
			return apperr.Conflict("BILL_NOT_CLOSED", "only a closed bill is reopened").WithContext("status", b.Status)
		}
		if _, err := s.ledger.ReopenBillFolio(ctx, p, propertyID, *b.FolioID); err != nil {
			return err
		}
		if n, err := s.q(ctx).ReopenBill(ctx, posdb.ReopenBillParams{TenantID: p.TenantID, PropertyID: propertyID, ID: billID, ActorID: p.ActorID()}); err != nil || n != 1 {
			if err == nil {
				err = apperr.Conflict("BILL_NOT_CLOSED", "only a closed bill is reopened")
			}
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.bill_reopened", "pos_bill", billID, deref(b.BillNumber), nil, approval.AuditFields(map[string]any{
			"reason": reason, "closed_on": b.ClosedBusinessDate, "sold": sums.Sold.String(),
		})))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// -------------------------------------------------------------------------------------------------- complimentary

// CompBill makes lines (the whole bill when none is named) complimentary, in the treatment the bill kept when it was opened (7.5): a payment of the method COMP, or a 100% discount that leaves no
// revenue, tax or expense. It needs pos.comp, an outlet that allows it, a reason, a name and the approval POS_COMP of the total with tax that is given away.
func (s *Service) CompBill(ctx context.Context, propertyID, billID int64, in CompInput) (Bill, error) {
	p, err := s.cashier(ctx, propertyID)
	if err != nil {
		return Bill{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSComp); err != nil {
		return Bill{}, err
	}
	reason, fields := requireReason(in.Reason)
	name := strings.TrimSpace(in.Name)
	if n := len([]rune(name)); n < 1 || n > 100 {
		fields = append(fields, fieldErr("name", "REQUIRED", "the guest or the manager who authorises it"))
	}
	if len(fields) > 0 {
		return Bill{}, invalid("complimentary", fields...)
	}
	decimals, err := s.decimals(ctx, p.TenantID, propertyID)
	if err != nil {
		return Bill{}, err
	}
	amount, err := s.compAmount(ctx, p, propertyID, billID, in.LineIDs)
	if err != nil {
		return Bill{}, err
	}
	approval, err := s.iam.Verify(ctx, iam.ApprovalRequest{PropertyID: propertyID, Input: in.Approval, Kind: iam.KindPOSComp, Amount: amount, Own: s.callerApproves(ctx, propertyID)})
	if err != nil {
		return Bill{}, err
	}
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		day, err := s.days.RequireOpenBusinessDay(ctx, propertyID, db.ForShare, nil)
		if err != nil {
			return err
		}
		b, err := s.lockBill(ctx, p, propertyID, billID, db.ForUpdate)
		if err != nil {
			return err
		}
		o, err := s.outletOf(ctx, p.TenantID, propertyID, b.OutletID)
		if err != nil {
			return err
		}
		if !o.AllowComp {
			return apperr.Conflict("OUTLET_COMP_NOT_ALLOWED", "this outlet does not give complimentary bills")
		}
		if _, err := s.sendDrafts(ctx, p, propertyID, day.BusinessDate, b, o, newUUID()); err != nil {
			return err
		}
		lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
		if err != nil {
			return err
		}
		chosen := map[int64]bool{}
		for _, id := range in.LineIDs {
			chosen[id] = true
		}
		q := s.q(ctx)
		dept := o.DepartmentID
		given := decimal.Zero
		for _, l := range lines {
			if l.Status != LineSent || l.IsComp || (len(chosen) > 0 && !chosen[l.ID]) {
				continue
			}
			value := l.ItemTotal.Add(adjustedTotal(l))
			if !value.IsPositive() {
				continue
			}
			given = given.Add(value)
			if b.CompTreatment == "DISCOUNT_100" {
				rem := remainingBase(l, decimals)
				if rem.IsPositive() {
					if _, err := s.ledger.AdjustOutletItem(ctx, p, propertyID, day.BusinessDate, *b.FolioID, *l.FolioItemID, rem, l.RuleProfileID, &dept, "Complimentary: "+reason, approval); err != nil {
						return err
					}
				}
			}
			by := approval.UserID()
			_ = by
			if err := q.MarkLineComp(ctx, posdb.MarkLineCompParams{TenantID: p.TenantID, PropertyID: propertyID, ID: l.ID, Reason: &reason, ActorID: p.ActorID()}); err != nil {
				return err
			}
		}
		if !given.IsPositive() {
			return apperr.Conflict("NOTHING_TO_COMP", "there is nothing on the bill to give away")
		}
		if b.CompTreatment == "PAYMENT_COMP" {
			if _, err := s.ledger.PostOutletPayment(ctx, p, propertyID, day.BusinessDate, *b.FolioID, foliosComp(given, name, reason, uuidString(newUUID())), ptr(approval.UserID())); err != nil {
				return err
			}
		}
		if _, err := s.closeIfSettled(ctx, p, propertyID, day.BusinessDate, b); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, day.BusinessDate, "pos.bill_comped", "pos_bill", billID, deref(b.BillNumber), nil, approval.AuditFields(map[string]any{
			"treatment": b.CompTreatment, "amount": given.String(), "name": name, "reason": reason,
		})))
	})
	if err != nil {
		return Bill{}, err
	}
	return s.loadBill(ctx, p, propertyID, billID)
}

// adjustedTotal is what the adjustments (a discount after the send) took off a line: its tax and service reduced in proportion, so the value left is the item total less them. It is read from the
// net adjusted and the rates of the line.
func adjustedTotal(l posdb.ListBillLinesRow) decimal.Decimal {
	if l.ItemNet.IsZero() || l.AdjustedNet.IsZero() {
		return decimal.Zero
	}
	// the adjustment carries the same service and tax rates as the line: its total is its net times (item total / item net)
	return l.AdjustedNet.Mul(l.ItemTotal).Div(l.ItemNet)
}

// compAmount is what a complimentary would give away, read before the transaction: the total with tax of the lines named (sent lines at their value now, drafts at their quote).
func (s *Service) compAmount(ctx context.Context, p auth.Principal, propertyID, billID int64, lineIDs []int64) (decimal.Decimal, error) {
	b, err := s.billRow(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return decimal.Zero, err
	}
	if b.Status != BillOpen {
		return decimal.Zero, errBillNotOpen()
	}
	lines, err := s.billLines(ctx, p.TenantID, propertyID, billID)
	if err != nil {
		return decimal.Zero, err
	}
	total := decimal.Zero
	for _, l := range lines {
		if l.IsComp || l.Status == LineVoid || (len(lineIDs) > 0 && !slices.Contains(lineIDs, l.ID)) {
			continue
		}
		switch l.Status {
		case LineDraft:
			q, err := s.quote(ctx, propertyID, l.ChargeCodeID, l.Quantity, l.UnitPrice, l.DiscountAmount, l.RuleProfileID)
			if err != nil {
				return decimal.Zero, err
			}
			total = total.Add(q.Total)
		case LineSent:
			total = total.Add(l.ItemTotal).Add(adjustedTotal(l))
		}
	}
	return total, nil
}
