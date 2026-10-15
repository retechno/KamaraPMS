package folios

import (
	"context"
	"errors"
	"time"

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

// The ledger side of the bills of an outlet (docs/architecture/34-pos.md sections 4, 5.2, 6.3, 7 and 17). The bill itself (lines, rounds, tickets, tables) belongs to internal/pos; what money does
// belongs here, to the one writer of folio_items and of payments. Every method runs in the transaction of the caller, which holds the open business day in share mode and the folios it names
// (level 42, in one call, ascending): none of them opens a transaction, takes a document number or commits.

// The references and the source of a line a bill posts.
const (
	// ReferencePosBillLine is the reference type of the CHARGE row of a line of a bill; its id is the uuid of the line, so a line is posted once (a unique index).
	ReferencePosBillLine = "POS_BILL_LINE"
	// SourcePOS is the source of the ledger rows a bill posts.
	SourcePOS = "POS"
)

func errOutletFolioUsePOS() *apperr.Error {
	return apperr.Conflict("OUTLET_FOLIO_USE_POS", "the folio of an outlet bill is changed in the point of sale, not here")
}

// refuseOutletFolios refuses the generic folio use cases on the folio of a bill: its lines, payments and transfers go through the point of sale, which keeps the bill in step with its folio.
func refuseOutletFolios(folios ...foliosdb.Folio) error {
	for _, f := range folios {
		if f.FolioType == FolioTypeOutlet {
			return errOutletFolioUsePOS().WithContext("folio_number", f.FolioNumber)
		}
	}
	return nil
}

// BillFolio is the folio of a bill as the point of sale sees it.
type BillFolio struct {
	ID       int64
	Number   string
	OutletID int64
	Status   string
	Version  int32
	ClosedOn *civil.Date
}

func toBillFolio(f foliosdb.Folio) BillFolio {
	b := BillFolio{ID: f.ID, Number: f.FolioNumber, Status: f.Status, Version: f.Version, ClosedOn: f.ClosedOn}
	if f.OutletID != nil {
		b.OutletID = *f.OutletID
	}
	return b
}

// OpenOutletFolio makes the folio of a new bill, numbered as the bill is. A new folio is our own uncommitted row, so it is not locked again.
func (s *Service) OpenOutletFolio(ctx context.Context, p auth.Principal, propertyID, outletID int64, number string) (BillFolio, error) {
	f, err := s.q(ctx).InsertOutletFolio(ctx, foliosdb.InsertOutletFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioNumber: number, OutletID: &outletID, ActorID: p.ActorID()})
	if err != nil {
		return BillFolio{}, err
	}
	return toBillFolio(f), nil
}

// LockFolios takes the folios of one request, FOR UPDATE or FOR SHARE, in one call and in ascending id (level 42). A bill that is only added to takes FOR SHARE: a close cannot slip in, and
// two waiters adding at once do not wait for each other.
func (s *Service) LockFolios(ctx context.Context, propertyID int64, mode db.LockMode, ids ...int64) error {
	return mapNotFound(db.LockRows(ctx, db.Folios, mode, propertyID, ids), errFolioNotFound())
}

// LockStayShared key-shares the stay a charge to a room is for (level 41), so that a check-out waits for the charge instead of deadlocking with it (docs/architecture/05-transactions-locking.md).
func (s *Service) LockStayShared(ctx context.Context, propertyID, stayID int64) error {
	err := db.LockRows(ctx, db.Stays, db.ForShare, propertyID, []int64{stayID})
	return mapNotFound(err, apperr.NotFound("STAY_NOT_FOUND", "the stay does not exist in this property"))
}

// BillFolioOf reads a folio of type OUTLET (after the caller has locked it).
func (s *Service) BillFolioOf(ctx context.Context, p auth.Principal, propertyID, folioID int64) (BillFolio, error) {
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
	if err != nil {
		return BillFolio{}, orNotFound(err, errFolioNotFound())
	}
	if f.FolioType != FolioTypeOutlet {
		return BillFolio{}, errFolioNotFound()
	}
	return toBillFolio(f), nil
}

// BillBalance is the balance of the folio of a bill: debits less credits. It is the amount to pay.
func (s *Service) BillBalance(ctx context.Context, propertyID, folioID int64) (decimal.Decimal, error) {
	bal, _, err := s.balanceOf(ctx, propertyID, folioID)
	return bal, err
}

// BillTotals is what a bill has sold and what it has been paid: the debits (charges, service and tax included, and the corrections of a payment) and the credits.
type BillTotals struct {
	Debit, Credit, Balance decimal.Decimal
}

// BillTotalsOf reads the totals of the folio of a bill.
func (s *Service) BillTotalsOf(ctx context.Context, propertyID, folioID int64) (BillTotals, error) {
	bal, t, err := s.balanceOf(ctx, propertyID, folioID)
	return BillTotals{Debit: t.Debit, Credit: t.Credit, Balance: bal}, err
}

// OutletLine is a sale line to post: the product's charge code and the price of the line (the options included), the discount, and what the outlet decides about tax and department.
type OutletLine struct {
	ChargeCodeID  int64
	Quantity      decimal.Decimal
	UnitPrice     decimal.Decimal
	Discount      decimal.Decimal // a magnitude: at most the base of the line
	RuleProfileID *int64          // the profile in force for the line (the product's or the outlet's), snapshotted on the bill line
	DepartmentID  *int64          // the sub-department of the outlet
	Description   string
	LineUUID      string
	// IsRoomCode is false for every line; the codes of type ROOM, TRANSFER and ROUNDING are refused.
}

// PostedLine is what posting a line wrote: the ledger row and its amounts.
type PostedLine struct {
	ItemID                                   int64
	Base, Discount, Net, Service, Tax, Total decimal.Decimal
}

func postedOf(it foliosdb.FolioItem) PostedLine {
	return PostedLine{ItemID: it.ID, Base: it.BaseAmount, Discount: it.DiscountAmount, Net: it.NetAmount, Service: it.ServiceChargeTotal, Tax: it.TaxTotal, Total: it.Debit.Sub(it.Credit)}
}

func (s *Service) openBillFolio(ctx context.Context, p auth.Principal, propertyID, folioID int64) (foliosdb.Folio, error) {
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
	if err != nil {
		return foliosdb.Folio{}, orNotFound(err, errFolioNotFound())
	}
	if f.FolioType != FolioTypeOutlet {
		return foliosdb.Folio{}, errFolioNotFound()
	}
	return f, requireOpen(f)
}

// PostOutletLine posts a sale line to the folio of a bill: a CHARGE through the one charge engine (the profile replaces the rules of the code), source POS, in the department of the outlet. Its
// reference is the uuid of the line, which the database holds unique, so a line is never posted twice.
func (s *Service) PostOutletLine(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID int64, in OutletLine) (PostedLine, error) {
	folio, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PostedLine{}, err
	}
	rules, err := s.billing.ResolveRules(ctx, p.TenantID, propertyID, in.ChargeCodeID)
	if err != nil {
		return PostedLine{}, err
	}
	switch rules.ChargeType {
	case "ROOM", "TRANSFER", "ROUNDING":
		return PostedLine{}, apperr.Conflict("POS_CODE_NOT_SELLABLE", "the charge code of a product cannot be a room, transfer or rounding code").WithContext("charge_code", rules.Code)
	}
	b, err := s.billing.Calculate(ctx, billingconfig.ChargeRequest{
		PropertyID: propertyID, ChargeCodeID: in.ChargeCodeID, Quantity: in.Quantity, UnitPrice: in.UnitPrice, Discount: in.Discount, RuleProfileID: in.RuleProfileID,
	})
	if err != nil {
		return PostedLine{}, err
	}
	id, ref, refID := in.ChargeCodeID, ReferencePosBillLine, in.LineUUID
	desc := in.Description
	if desc == "" {
		desc = rules.Code
	}
	item, err := s.posting(p, propertyID, bd, folio).insert(ctx, itemSpec{
		transactionType: TypeCharge, chargeCodeID: &id, serviceDate: bd, description: desc, amounts: amountsOf(b), components: componentsOf(b),
		source: SourcePOS, referenceType: &ref, referenceID: &refID, departmentID: in.DepartmentID,
	})
	if err != nil {
		return PostedLine{}, err
	}
	return postedOf(item), nil
}

// AdjustOutletItem is the discount of a line that is already posted: an ADJUSTMENT of today of -base through the engine on a signed base, so that service and tax reduce in proportion, with the
// profile of the line (7.3). The approval is the one the use case asked for, when the discount is above what the outlet lets its cashier give. The result equals the same discount applied before the send.
func (s *Service) AdjustOutletItem(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID, itemID int64, base decimal.Decimal, ruleProfileID, departmentID *int64, reason string, approval iam.Approval) (PostedLine, error) {
	folio, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PostedLine{}, err
	}
	orig, err := s.q(ctx).GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
	if err != nil {
		return PostedLine{}, orNotFound(err, errItemNotFound())
	}
	if orig.FolioID != folio.ID || orig.TransactionType != TypeCharge || orig.ChargeCodeID == nil {
		return PostedLine{}, errItemNotFound()
	}
	b, err := s.billing.Calculate(ctx, billingconfig.ChargeRequest{
		PropertyID: propertyID, ChargeCodeID: *orig.ChargeCodeID, Quantity: decimal.NewFromInt(1), UnitPrice: base.Abs().Neg(), RuleProfileID: ruleProfileID,
	})
	if err != nil {
		return PostedLine{}, err
	}
	if b.TotalAmount.IsZero() {
		return PostedLine{}, apperr.Invalid("the discount is invalid", fieldErr("amount", "INVALID_AMOUNT", "the discount amounts to nothing"))
	}
	origID := orig.ID
	code := *orig.ChargeCodeID
	var approver *int64 // none when the discount is within what the outlet lets its cashier give
	if !approval.IsZero() {
		by := approval.UserID()
		approver = &by
	}
	item, err := s.posting(p, propertyID, bd, folio).insert(ctx, itemSpec{
		transactionType: TypeAdjustment, chargeCodeID: &code, copiesItemID: &origID, serviceDate: bd, description: "Discount: " + orig.Description, amounts: amountsOf(b),
		components: componentsOf(b), reason: &reason, approvedBy: approver, source: SourcePOS, referenceType: orig.ReferenceType, referenceID: orig.ReferenceID, departmentID: departmentID,
	})
	if err != nil {
		return PostedLine{}, err
	}
	return postedOf(item), nil
}

// VoidOutletItem takes a sold line back off the bill. On the day it was sold it is a REVERSAL; on a later day (the day is closed) it is an ADJUSTMENT of today that is the exact opposite, so the
// revenue of the day it was sold stays and today's reduces (7.6). It needs a verified approval. The folio must be open.
func (s *Service) VoidOutletItem(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID, itemID int64, reason string, approval iam.Approval) (PostedLine, bool, error) {
	folio, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PostedLine{}, false, err
	}
	q := s.q(ctx)
	orig, err := q.GetFolioItem(ctx, foliosdb.GetFolioItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: itemID})
	if err != nil {
		return PostedLine{}, false, orNotFound(err, errItemNotFound())
	}
	if orig.FolioID != folio.ID || orig.TransactionType != TypeCharge {
		return PostedLine{}, false, errItemNotFound()
	}
	origID := orig.ID
	if _, err := q.GetReversalOf(ctx, foliosdb.GetReversalOfParams{PropertyID: propertyID, ItemID: &origID}); err == nil {
		return PostedLine{}, false, apperr.Conflict("ALREADY_REVERSED", "the item has already been reversed").WithContext("item_id", origID)
	}
	ps := s.posting(p, propertyID, bd, folio)
	var it foliosdb.FolioItem
	reversed := orig.BusinessDate.Equal(bd)
	if reversed {
		it, err = ps.reverse(ctx, orig, reason, approval)
	} else {
		it, err = ps.negate(ctx, orig, reason, approval, nil)
	}
	if err != nil {
		return PostedLine{}, false, err
	}
	return postedOf(it), reversed, nil
}

// OutletPayment is one payment of a bill.
type OutletPayment struct {
	Method    string // CASH, CARD, BANK_TRANSFER, OTHER, QRIS or COMP
	Amount    decimal.Decimal
	Reference string
	Remarks   string
	ShiftID   *int64 // the shift PrepareOutletCash resolved, for cash
	Key       string // the Idempotency-Key of this payment: K#i
}

// PaidPayment is the payment that was written and its ledger entry.
type PaidPayment struct {
	PaymentID     int64
	PaymentNumber string
	ItemID        int64
	Amount        decimal.Decimal
	Method        string
}

// PostOutletPayment takes one payment on the folio of a bill: through the one payment writer, so the number, the shift of the drawer, the fee of a card or QRIS and the ledger entry are those of any
// payment. COMP settles the guest ledger against the expense account of the key COMP and is refused while that is not mapped.
func (s *Service) PostOutletPayment(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID int64, in OutletPayment, approvedBy *int64) (PaidPayment, error) {
	folio, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PaidPayment{}, err
	}
	if in.Method == MethodComp {
		a, err := s.q(ctx).GetCompAccount(ctx, foliosdb.GetCompAccountParams{TenantID: p.TenantID, PropertyID: propertyID})
		if err != nil || !a.IsActive || !a.IsPostable {
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return PaidPayment{}, err
			}
			return PaidPayment{}, apperr.Conflict("COMP_ACCOUNT_NOT_MAPPED", "the system account COMP is not mapped to an active account that takes postings: map it in the account mapping")
		}
	} else if fe := validateMethod(in.Method); fe != nil {
		return PaidPayment{}, apperr.Invalid("the payment is invalid", *fe)
	}
	if !in.Amount.IsPositive() {
		return PaidPayment{}, apperr.Invalid("the payment is invalid", fieldErr("amount", "INVALID_AMOUNT", "a positive amount"))
	}
	pin := PaymentInput{PaymentMethod: in.Method, ReferenceNumber: in.Reference, Remarks: in.Remarks, shiftID: in.ShiftID, shiftSet: in.Method == methodCash}
	pay, err := s.posting(p, propertyID, bd, folio).createPayment(ctx, PaymentTypePayment, in.Method, in.Amount, pin, nil, in.Key, approvedBy, "")
	if err != nil {
		return PaidPayment{}, err
	}
	item, err := s.q(ctx).GetItemOfPayment(ctx, foliosdb.GetItemOfPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, PaymentID: &pay.ID})
	if err != nil {
		return PaidPayment{}, err
	}
	return PaidPayment{PaymentID: pay.ID, PaymentNumber: pay.PaymentNumber, ItemID: item.ID, Amount: pay.Amount, Method: pay.PaymentMethod}, nil
}

// PrepareOutletCash resolves and locks (level 43, FOR SHARE) the open shift of the cashier on the drawer of the outlet, before the first document number of the request is taken. A property that does
// not require a shift answers nil; one that does answers 409 NO_OPEN_SHIFT. Pass the result to the cash payment of the request.
func (s *Service) PrepareOutletCash(ctx context.Context, propertyID int64, drawer string) (*int64, error) {
	if s.shifts == nil {
		return nil, nil
	}
	return s.shifts.CashShiftOfDrawer(ctx, propertyID, drawer)
}

// PrepareOutletCompany checks that a company may be billed by the outlets (companies.pos_billing_allowed, 34-pos.md D8) and takes its row (level 44, after the folios and the shift) with the
// credit limit check for the amount. The caller then posts the transfer with PostOutletCompanyTransfer.
func (s *Service) PrepareOutletCompany(ctx context.Context, p auth.Principal, propertyID, companyID int64, amount decimal.Decimal) error {
	if s.gate == nil {
		return apperr.Conflict("CITY_LEDGER_UNAVAILABLE", "the city ledger is not available")
	}
	c, err := s.q(ctx).GetCompanyPosBilling(ctx, foliosdb.GetCompanyPosBillingParams{TenantID: p.TenantID, PropertyID: propertyID, ID: companyID})
	if err != nil {
		return orNotFound(err, apperr.NotFound("COMPANY_NOT_FOUND", "the company does not exist in this property"))
	}
	if !c.PosBillingAllowed {
		return apperr.Conflict("COMPANY_POS_BILLING_NOT_ALLOWED", "this company is not set to be billed by the outlets").WithContext("company", c.Name)
	}
	return s.gate.LockForTransfer(ctx, propertyID, companyID, amount)
}

// PostOutletCompanyTransfer moves part of the balance of a bill to the city ledger account of a company: a CITY_LEDGER payment. PrepareOutletCompany has locked the company and checked its limit. The
// caller checks cityledger.transfer and the setting of the outlet.
func (s *Service) PostOutletCompanyTransfer(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID, companyID int64, amount decimal.Decimal, reference, key string) (PaidPayment, error) {
	folio, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PaidPayment{}, err
	}
	balance, _, err := s.balanceOf(ctx, propertyID, folioID)
	if err != nil {
		return PaidPayment{}, err
	}
	if amount.GreaterThan(balance) {
		return PaidPayment{}, apperr.Conflict("TRANSFER_EXCEEDS_BALANCE", "the transfer is more than the bill's balance").WithContext("balance", balance.String())
	}
	cid := companyID
	pay, err := s.posting(p, propertyID, bd, folio).createPayment(ctx, PaymentTypePayment, MethodCityLedger, amount, PaymentInput{ReferenceNumber: reference, companyID: &cid}, nil, key, nil, "")
	if err != nil {
		return PaidPayment{}, err
	}
	item, err := s.q(ctx).GetItemOfPayment(ctx, foliosdb.GetItemOfPaymentParams{TenantID: p.TenantID, PropertyID: propertyID, PaymentID: &pay.ID})
	if err != nil {
		return PaidPayment{}, err
	}
	return PaidPayment{PaymentID: pay.ID, PaymentNumber: pay.PaymentNumber, ItemID: item.ID, Amount: pay.Amount, Method: MethodCityLedger}, nil
}

// RoundingLine posts the rounding of a bill (7.4): one line of the system code POS_ROUNDING of -r (rounding down, an ADJUSTMENT with the approver of the setting, none here: it is the cashier's own
// rounding, recorded as a CHARGE or an ADJUSTMENT of the code with no tax and no service). A positive amount is a charge, a negative one a credit.
func (s *Service) RoundingLine(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, folioID int64, amount decimal.Decimal, departmentID *int64, billNumber string) (PostedLine, error) {
	if amount.IsZero() {
		return PostedLine{}, nil
	}
	folio, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return PostedLine{}, err
	}
	code, err := s.q(ctx).GetRoundingChargeCode(ctx, foliosdb.GetRoundingChargeCodeParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return PostedLine{}, orNotFound(err, apperr.Conflict("ROUNDING_CODE_MISSING", "the charge code POS_ROUNDING does not exist in this property"))
	}
	exclusive := chargecalc.Exclusive
	b, err := s.billing.Calculate(ctx, billingconfig.ChargeRequest{PropertyID: propertyID, ChargeCodeID: code.ID, Quantity: decimal.NewFromInt(1), UnitPrice: amount, PriceMode: &exclusive})
	if err != nil {
		return PostedLine{}, err
	}
	if len(b.TaxComponents) > 0 || len(b.ServiceComponents) > 0 {
		return PostedLine{}, apperr.Conflict("ROUNDING_CODE_HAS_RULES", "the code POS_ROUNDING has a tax or a service charge mapped to it: it must have none")
	}
	id := code.ID
	reason := "Rounding of the bill " + billNumber
	spec := itemSpec{
		transactionType: TypeCharge, chargeCodeID: &id, serviceDate: bd, description: "Rounding " + billNumber, amounts: amountsOf(b), components: componentsOf(b),
		source: SourcePOS, departmentID: departmentID,
	}
	if amount.IsNegative() {
		spec.transactionType, spec.reason = TypeAdjustment, &reason
	}
	item, err := s.posting(p, propertyID, bd, folio).insert(ctx, spec)
	if err != nil {
		return PostedLine{}, err
	}
	return postedOf(item), nil
}

// CloseBillFolio closes the folio of a bill whose balance is zero; ReopenBillFolio opens a closed one again. Both are the point of sale's: a bill closes when it is paid to zero and reopens with the
// approval POS_REOPEN and folio.reopen, which the caller has checked.
func (s *Service) CloseBillFolio(ctx context.Context, p auth.Principal, propertyID, folioID int64, bd civil.Date) error {
	f, err := s.openBillFolio(ctx, p, propertyID, folioID)
	if err != nil {
		return err
	}
	bal, _, err := s.balanceOf(ctx, propertyID, folioID)
	if err != nil {
		return err
	}
	if !bal.IsZero() {
		return apperr.Conflict("FOLIO_BALANCE_NOT_ZERO", "the bill still has a balance").WithContext("balance", bal.String())
	}
	if _, err := s.q(ctx).CloseFolio(ctx, foliosdb.CloseFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: f.ID, Now: s.clock.Now(), ActorID: p.ActorID()}); err != nil {
		return err
	}
	return s.lockOnClose(ctx, p, propertyID, []int64{f.ID}, LockReasonFolioClosed)
}

// ReopenBillFolio opens the folio of a closed bill again.
func (s *Service) ReopenBillFolio(ctx context.Context, p auth.Principal, propertyID, folioID int64) (BillFolio, error) {
	f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID})
	if err != nil {
		return BillFolio{}, orNotFound(err, errFolioNotFound())
	}
	if f.FolioType != FolioTypeOutlet {
		return BillFolio{}, errFolioNotFound()
	}
	if f.Status != "CLOSED" {
		return BillFolio{}, apperr.Conflict("FOLIO_NOT_CLOSED", "the bill is not closed").WithContext("folio_number", f.FolioNumber)
	}
	r, err := s.q(ctx).ReopenFolio(ctx, foliosdb.ReopenFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: folioID, ActorID: p.ActorID()})
	if err != nil {
		return BillFolio{}, orNotFound(err, apperr.Conflict("FOLIO_NOT_CLOSED", "the bill is not closed"))
	}
	return toBillFolio(r), nil
}

// OutletTarget is the folio a charge to a room lands on.
type OutletTarget struct {
	FolioID int64
	Routed  bool
	Type    string // GUEST, COMPANY or MASTER
	Number  string
}

// ResolveOutletTarget resolves, without a lock, the folio a charge of an outlet for a stay lands on: the ALL instructions of the line, then those of its group, else the guest folio (the ROOM and
// CHARGE_CODE instructions do not apply to what an outlet sells, 34-pos.md 6.3). FolioID is 0 when the answer is a closed folio or none.
func (s *Service) ResolveOutletTarget(ctx context.Context, p auth.Principal, propertyID, stayID int64) (OutletTarget, error) {
	t, err := s.ResolveTarget(ctx, p.TenantID, propertyID, stayID, Route{})
	if err != nil {
		return OutletTarget{}, err
	}
	out := OutletTarget{FolioID: t.FolioID, Routed: t.Routed}
	if t.FolioID != 0 {
		f, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: t.FolioID})
		if err != nil {
			return OutletTarget{}, orNotFound(err, errFolioNotFound())
		}
		out.Type, out.Number = f.FolioType, f.FolioNumber
	}
	return out, nil
}

// OutletRoomCharge asks to charge part of a bill to a room.
type OutletRoomCharge struct {
	PropertyID    int64
	BusinessDate  civil.Date
	BillFolioID   int64
	BillID        int64
	BillNumber    string
	OutletID      int64
	OutletName    string
	StayID        int64
	TargetFolioID int64 // from ResolveOutletTarget; the caller has locked it with the folio of the bill, in one call
	Amount        decimal.Decimal
	Source        string // ChargeSourceCharge or ChargeSourceMove
	Reason        string
	Key           string
}

// OutletRoomChargeResult is the answer: a refusal (the attempt was written; the caller commits it and answers 409) or the charge.
type OutletRoomChargeResult struct {
	Refusal       *apperr.Error
	ChargeID      int64
	BatchID       int64
	TransferID    int64
	ToFolioNumber string
	Limit         decimal.Decimal
	Used          decimal.Decimal // after this charge
	Remaining     decimal.Decimal
	LimitReached  bool
}

// ChargeOutletBillToRoom decides and, when accepted, posts a charge of a bill to a room (6.3): the gate under the folio locks, then the TRF pair "{outlet} · {bill}" between the folio of the bill and
// the target, the row that counts against the limit and the attempt. If the amount brings the usage to the limit exactly the lock closes in the same transaction. It takes no approval: it is the
// cashier's own act, who holds pos.charge_room (checked by the caller).
func (s *Service) ChargeOutletBillToRoom(ctx context.Context, p auth.Principal, in OutletRoomCharge) (OutletRoomChargeResult, error) {
	decimals, err := s.decimals(ctx, in.PropertyID)
	if err != nil {
		return OutletRoomChargeResult{}, err
	}
	bill, tgt := in.BillID, in.StayID
	src, err := s.openBillFolio(ctx, p, in.PropertyID, in.BillFolioID)
	if err != nil {
		return OutletRoomChargeResult{}, err
	}
	from := src.ID
	chk := OutletChargeCheck{
		PropertyID: in.PropertyID, BusinessDate: in.BusinessDate, TargetFolioID: in.TargetFolioID, OutletID: in.OutletID, Amount: in.Amount, Source: in.Source,
		BillID: &bill, BillNumber: in.BillNumber, StayID: &tgt, FromFolioID: &from,
	}
	d, err := s.CheckOutletCharge(ctx, p, chk)
	if err != nil {
		return OutletRoomChargeResult{}, err
	}
	if d.Refusal != nil {
		return OutletRoomChargeResult{Refusal: d.Refusal}, nil
	}
	target, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: in.PropertyID, ID: in.TargetFolioID})
	if err != nil {
		return OutletRoomChargeResult{}, orNotFound(err, errFolioNotFound())
	}
	batch, err := s.q(ctx).InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
		TenantID: p.TenantID, PropertyID: in.PropertyID, Kind: KindBalance, FromFolioID: src.ID, ToFolioID: target.ID, Scope: ScopeOutletCharge, Reason: in.Reason,
		BusinessDate: in.BusinessDate, IdempotencyKey: nullable(in.Key), ActorID: p.ActorID(),
	})
	if err != nil {
		return OutletRoomChargeResult{}, err
	}
	m := movement{
		p: p, propertyID: in.PropertyID, bd: in.BusinessDate, src: src, tgt: target, batchID: batch.ID, reason: in.Reason, scope: ScopeOutletCharge,
		outDesc: "Charged to " + target.FolioNumber, inDesc: in.OutletName + " · " + in.BillNumber,
	}
	moved, err := s.moveBalance(ctx, m, in.Amount, decimals, nil)
	if err != nil {
		return OutletRoomChargeResult{}, err
	}
	id, err := s.RecordOutletCharge(ctx, p, chk, d, &batch.ID, nullable(in.Key))
	if err != nil {
		return OutletRoomChargeResult{}, err
	}
	used := d.Used.Add(in.Amount)
	return OutletRoomChargeResult{
		ChargeID: id, BatchID: batch.ID, TransferID: moved.TransferID, ToFolioNumber: target.FolioNumber, Limit: d.Limit, Used: used,
		Remaining: decimal.Max(d.Limit.Sub(used), decimal.Zero), LimitReached: !used.LessThan(d.Limit),
	}, nil
}

// ReturnedRoomCharge is a charge to a room that was sent back.
type ReturnedRoomCharge struct {
	ChargeID, TransferID int64
	FromFolioID          int64 // the folio that held it (the guest or the master)
	ToFolioID            int64 // the bill's folio, which has the balance again
	Amount               decimal.Decimal
	StayID               *int64
	BillID               *int64
}

// ReturnOutletRoomCharge sends a charge to a room back to the folio of its bill: the return of document 30 4.5 (a charge of the TRF pair from the room back to the bill), and the row that counted
// against the limit is marked returned, so the usage goes back down. It needs a verified approval of kind FOLIO_TRANSFER, taken by the caller. The two folios are open and locked by the caller.
func (s *Service) ReturnOutletRoomCharge(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, chargeID int64, reason string, approval iam.Approval) (ReturnedRoomCharge, error) {
	decimals, err := s.decimals(ctx, propertyID)
	if err != nil {
		return ReturnedRoomCharge{}, err
	}
	q := s.q(ctx)
	ch, err := q.GetRoomCharge(ctx, foliosdb.GetRoomChargeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeID})
	if err != nil {
		return ReturnedRoomCharge{}, orNotFound(err, apperr.NotFound("ROOM_CHARGE_NOT_FOUND", "the outlet charge does not exist in this property"))
	}
	if ch.ReturnedAt != nil {
		return ReturnedRoomCharge{}, apperr.Conflict("ALREADY_RETURNED", "the charge has been returned already").WithContext("charge_id", chargeID)
	}
	if ch.FromFolioID == nil || ch.TransferBatchID == nil {
		return ReturnedRoomCharge{}, errTransferInvalid("the charge has no bill to go back to")
	}
	rows, err := q.ListBatchTransfers(ctx, foliosdb.ListBatchTransfersParams{PropertyID: propertyID, BatchID: *ch.TransferBatchID})
	if err != nil || len(rows) != 1 {
		if err == nil {
			err = errTransferInvalid("the outlet charge has no transfer")
		}
		return ReturnedRoomCharge{}, err
	}
	t := rows[0]
	if _, err := q.GetTransferReturnedBy(ctx, foliosdb.GetTransferReturnedByParams{PropertyID: propertyID, TransferID: &t.ID}); err == nil {
		return ReturnedRoomCharge{}, apperr.Conflict("ALREADY_RETURNED", "the transfer has been returned already").WithContext("transfer_id", t.ID)
	}
	holder, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: ch.ToFolioID})
	if err != nil {
		return ReturnedRoomCharge{}, orNotFound(err, errFolioNotFound())
	}
	bill, err := q.GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: *ch.FromFolioID})
	if err != nil {
		return ReturnedRoomCharge{}, orNotFound(err, errFolioNotFound())
	}
	if err := requireOpen(holder); err != nil {
		return ReturnedRoomCharge{}, err
	}
	if err := requireOpen(bill); err != nil {
		return ReturnedRoomCharge{}, err
	}
	batch, err := q.InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
		TenantID: p.TenantID, PropertyID: propertyID, Kind: KindBalance, IsReturn: true, FromFolioID: holder.ID, ToFolioID: bill.ID, Scope: ScopeReturn, Reason: reason,
		BusinessDate: bd, ApprovedBy: ptr(approval.UserID()), ActorID: p.ActorID(),
	})
	if err != nil {
		return ReturnedRoomCharge{}, err
	}
	m := movement{p: p, propertyID: propertyID, bd: bd, src: holder, tgt: bill, batchID: batch.ID, approval: approval, reason: reason, scope: ScopeReturn}
	tid := t.ID
	moved, err := s.moveBalance(ctx, m, t.Amount, decimals, &tid)
	if err != nil {
		return ReturnedRoomCharge{}, err
	}
	n, err := q.MarkRoomChargeReturned(ctx, foliosdb.MarkRoomChargeReturnedParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chargeID, ReturnedAt: s.clock.Now(), TransferID: &moved.TransferID})
	if err != nil {
		return ReturnedRoomCharge{}, err
	}
	if n != 1 {
		return ReturnedRoomCharge{}, apperr.Conflict("ALREADY_RETURNED", "the charge has been returned already").WithContext("charge_id", chargeID)
	}
	return ReturnedRoomCharge{ChargeID: chargeID, TransferID: moved.TransferID, FromFolioID: holder.ID, ToFolioID: bill.ID, Amount: t.Amount, StayID: ch.StayID, BillID: ch.BillID}, nil
}

// OutletItemMove is one line moved between two bills of an outlet (a split or a merge).
type OutletItemMove struct {
	OldItemID, NewItemID int64
}

// MoveOutletItems moves sold lines from one bill to another (a split by item, a merge): a reversal on the first bill and a copy on the second, exactly as any transfer of charges, in one batch of
// the scope OUTLET_BILL that needs no approval. Nothing is recomputed. Both folios are open and locked by the caller.
func (s *Service) MoveOutletItems(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, fromFolioID, toFolioID int64, itemIDs []int64, reason string) ([]OutletItemMove, error) {
	if len(itemIDs) > MaxTransferItems {
		e := apperr.Invalid("the transfer is invalid", fieldErr("item_ids", "TOO_MANY_ITEMS", "at most 200 charges in a request"))
		e.Code = "TOO_MANY_ITEMS"
		return nil, e
	}
	src, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: fromFolioID})
	if err != nil {
		return nil, orNotFound(err, errFolioNotFound())
	}
	tgt, err := s.q(ctx).GetFolio(ctx, foliosdb.GetFolioParams{TenantID: p.TenantID, PropertyID: propertyID, ID: toFolioID})
	if err != nil {
		return nil, orNotFound(err, errFolioNotFound())
	}
	if src.FolioType != FolioTypeOutlet || tgt.FolioType != FolioTypeOutlet {
		return nil, errFolioNotFound()
	}
	if err := requireOpen(src); err != nil {
		return nil, err
	}
	if err := requireOpen(tgt); err != nil {
		return nil, err
	}
	items, err := s.selectItems(ctx, p, propertyID, src, TransferItemsInput{ItemIDs: itemIDs})
	if err != nil {
		return nil, err
	}
	batch, err := s.q(ctx).InsertTransferBatch(ctx, foliosdb.InsertTransferBatchParams{
		TenantID: p.TenantID, PropertyID: propertyID, Kind: KindItem, FromFolioID: src.ID, ToFolioID: tgt.ID, Scope: ScopeOutletBill, Reason: reason, BusinessDate: bd, ActorID: p.ActorID(),
	})
	if err != nil {
		return nil, err
	}
	m := movement{p: p, propertyID: propertyID, bd: bd, src: src, tgt: tgt, batchID: batch.ID, reason: reason, scope: ScopeOutletBill}
	out := make([]OutletItemMove, 0, len(items))
	for _, it := range items {
		moved, err := s.moveItem(ctx, m, it, nil)
		if err != nil {
			return nil, err
		}
		out = append(out, OutletItemMove{OldItemID: it.ID, NewItemID: moved.InItemID})
	}
	return out, nil
}

// OutletBillPaymentView is a payment of a bill for the receipt.
type OutletBillPaymentView struct {
	PaymentID     int64
	PaymentNumber string
	Method        string
	Amount        decimal.Decimal
	Reference     string
	Remarks       string
	PaidAt        time.Time
	Status        string
	CompanyID     *int64
}

// ListBillPayments lists the payments of the folio of a bill, for the receipt and the payment screen.
func (s *Service) ListBillPayments(ctx context.Context, p auth.Principal, propertyID, folioID int64) ([]OutletBillPaymentView, error) {
	rows, err := s.q(ctx).ListPaymentsOfFolio(ctx, foliosdb.ListPaymentsOfFolioParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: folioID})
	if err != nil {
		return nil, err
	}
	out := make([]OutletBillPaymentView, 0, len(rows))
	for _, r := range rows {
		out = append(out, OutletBillPaymentView{
			PaymentID: r.ID, PaymentNumber: r.PaymentNumber, Method: r.PaymentMethod, Amount: r.Amount, Reference: deref(r.ReferenceNumber), Remarks: deref(r.Remarks), PaidAt: r.PaidAt,
			Status: r.Status, CompanyID: r.CompanyID,
		})
	}
	return out, nil
}

// OpenBillsOfStay is what a check-out warns about: the bills of the outlets still open for the room (pos_bills.target_stay_id).
type OpenBillOfStay struct {
	BillID     int64  `json:"bill_id"`
	BillNumber string `json:"bill_number"`
	OutletName string `json:"outlet_name"`
}

// OpenBillsOfStay lists them.
func (s *Service) OpenBillsOfStay(ctx context.Context, tenantID, propertyID, stayID int64) ([]OpenBillOfStay, error) {
	rows, err := s.q(ctx).ListOpenBillsOfStay(ctx, foliosdb.ListOpenBillsOfStayParams{TenantID: tenantID, PropertyID: propertyID, StayID: &stayID})
	if err != nil {
		return nil, err
	}
	out := make([]OpenBillOfStay, 0, len(rows))
	for _, r := range rows {
		out = append(out, OpenBillOfStay{BillID: r.ID, BillNumber: deref(r.BillNumber), OutletName: r.OutletName})
	}
	return out, nil
}

// AuditOutletBill writes an audit entry of the bill's folio with the standard label. The point of sale uses it for the entries that belong to the ledger of the bill.
func (s *Service) AuditOutletBill(ctx context.Context, p auth.Principal, propertyID int64, bd civil.Date, action string, folioID int64, data map[string]any) error {
	return s.audit.Write(ctx, audit.Entry{
		TenantID: p.TenantID, PropertyID: &propertyID, BusinessDate: &bd, UserID: p.ActorID(), Action: action, EntityType: "folio", EntityID: folioID,
		EntityLabel: auditlabel.Folio(ctx, propertyID, folioID), New: data,
	})
}

// RoomAdvice is what the lookup of a room tells the cashier: where a charge would land and whether the lock lets an outlet charge it now. It is advice; the gate decides again (6.3).
type RoomAdvice struct {
	TargetFolioID int64
	TargetType    string // GUEST, COMPANY or MASTER
	TargetNumber  string
	Routed        bool
	Open          bool
	Reason        string // why it is locked: NEVER_OPENED, MANUAL, LIMIT_REACHED, CHECKOUT, EXPIRED, FOLIO_CLOSED, OUTLET_NOT_ALLOWED, ROUTING_TARGET_CLOSED
	Limit         *decimal.Decimal
	Used          *decimal.Decimal
	Remaining     *decimal.Decimal
}

// AdviseRoomCharge reads, without a lock, what the gate would say for an outlet and a stay. The balance and the lines of the folio are not read: the cashier sees the lock, the limit and what was
// used, and nothing else.
func (s *Service) AdviseRoomCharge(ctx context.Context, p auth.Principal, propertyID, stayID, outletID int64, today civil.Date) (RoomAdvice, error) {
	t, err := s.ResolveOutletTarget(ctx, p, propertyID, stayID)
	if err != nil {
		return RoomAdvice{}, err
	}
	out := RoomAdvice{TargetFolioID: t.FolioID, TargetType: t.Type, TargetNumber: t.Number, Routed: t.Routed}
	if t.FolioID == 0 {
		out.Reason = "ROUTING_TARGET_CLOSED"
		return out, nil
	}
	q := s.q(ctx)
	row, err := q.GetOutletLock(ctx, foliosdb.GetOutletLockParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: t.FolioID})
	if errors.Is(err, pgx.ErrNoRows) {
		out.Reason = LockReasonNeverOpened
		return out, nil
	}
	if err != nil {
		return RoomAdvice{}, err
	}
	limit := row.LimitAmount
	out.Limit = &limit
	used, err := q.OutletChargesUsed(ctx, foliosdb.OutletChargesUsedParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: t.FolioID, PeriodNo: row.PeriodNo})
	if err != nil {
		return RoomAdvice{}, err
	}
	rem := decimal.Max(limit.Sub(used), decimal.Zero)
	out.Used, out.Remaining = &used, &rem
	switch {
	case row.Status != OutletLockOpen:
		out.Reason = LockReasonManual
		if row.LockedReason != nil {
			out.Reason = *row.LockedReason
		}
	case row.ValidMode == ValidToday && row.ValidOn != nil && row.ValidOn.Before(today):
		out.Reason = LockReasonExpired
	default:
		allowed, err := q.ListLockOutlets(ctx, foliosdb.ListLockOutletsParams{TenantID: p.TenantID, PropertyID: propertyID, FolioID: t.FolioID})
		if err != nil {
			return RoomAdvice{}, err
		}
		out.Open = false
		out.Reason = "OUTLET_NOT_ALLOWED"
		for _, o := range allowed {
			if o.ID == outletID {
				out.Open, out.Reason = true, ""
			}
		}
	}
	return out, nil
}

// CheckOutletRoomCharge asks the gate and nothing else: the caller has locked the folios and the stay. A refusal has written its attempt; the caller commits that and answers it. It is called
// before the lines of the bill are sent, so that a refusal leaves nothing but the attempt (34-pos.md 6.3).
func (s *Service) CheckOutletRoomCharge(ctx context.Context, p auth.Principal, in OutletRoomCharge) (*apperr.Error, error) {
	bill, stay, from := in.BillID, in.StayID, in.BillFolioID
	d, err := s.CheckOutletCharge(ctx, p, OutletChargeCheck{
		PropertyID: in.PropertyID, BusinessDate: in.BusinessDate, TargetFolioID: in.TargetFolioID, OutletID: in.OutletID, Amount: in.Amount, Source: in.Source,
		BillID: &bill, BillNumber: in.BillNumber, StayID: &stay, FromFolioID: &from,
	})
	return d.Refusal, err
}

// OutletPayKeyFolio answers the folio an Idempotency-Key of a request to pay was applied to, when it was (0, false otherwise).
func (s *Service) OutletPayKeyFolio(ctx context.Context, tenantID, propertyID int64, key string) (int64, bool, error) {
	id, err := s.q(ctx).OutletPayKeyFolio(ctx, foliosdb.OutletPayKeyFolioParams{TenantID: tenantID, PropertyID: propertyID, Prefix: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	return id, err == nil, err
}
