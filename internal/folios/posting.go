package folios

import (
	"context"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/billingconfig"
	"kamarapms/internal/chargecalc"
	"kamarapms/internal/folios/foliosdb"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// This file is FolioPostingService (docs/architecture/03-financial-engines.md step 10): the only code that
// writes folio_items and their components. Every function runs inside the caller's transaction, with the
// business day share-locked and the folio locked FOR UPDATE. It never verifies a password: corrections take a
// verified iam.Approval, which only iam can create.

// posting carries what every ledger write needs.
type posting struct {
	s          *Service
	p          auth.Principal
	propertyID int64
	bd         civil.Date
	at         time.Time
	folio      foliosdb.Folio
}

func (s *Service) posting(p auth.Principal, propertyID int64, bd civil.Date, folio foliosdb.Folio) posting {
	return posting{s: s, p: p, propertyID: propertyID, bd: bd, at: s.clock.Now(), folio: folio}
}

// itemAmounts are the signed columns of an item.
type itemAmounts struct {
	quantity, unitPrice                                        decimal.Decimal
	priceMode                                                  string
	base, discount, net, rounding, service, tax, debit, credit decimal.Decimal
}

// amountsOf maps the engine's breakdown to the ledger columns: debit or credit follows the sign of the total.
func amountsOf(b chargecalc.Breakdown) itemAmounts {
	a := itemAmounts{
		quantity: b.Quantity, unitPrice: b.UnitPrice, priceMode: string(b.PriceMode), base: b.BaseAmount, discount: b.Discount,
		net: b.NetAmount, rounding: b.RoundingAdjustment, service: b.ServiceTotal, tax: b.TaxTotal,
	}
	if b.TotalAmount.IsNegative() {
		a.credit = b.TotalAmount.Neg()
	} else {
		a.debit = b.TotalAmount
	}
	return a
}

// itemSpec is one item to insert.
type itemSpec struct {
	transactionType string
	chargeCodeID    *int64
	paymentID       *int64
	reversesItemID  *int64
	referenceType   *string
	referenceID     *string
	serviceDate     civil.Date
	description     string
	amounts         itemAmounts
	reason          *string
	key             *string
	approvedBy      *int64
	components      []foliosdb.InsertFolioItemComponentParams
}

// insert writes the item and its components and bumps the folio's version. The deferred trigger checks at
// commit that the item's service and tax totals equal the component sums.
func (ps posting) insert(ctx context.Context, spec itemSpec) (foliosdb.FolioItem, error) {
	q := ps.s.q(ctx)
	a := spec.amounts
	item, err := q.InsertFolioItem(ctx, foliosdb.InsertFolioItemParams{
		TenantID: ps.p.TenantID, PropertyID: ps.propertyID, FolioID: ps.folio.ID, BusinessDate: ps.bd, TransactionAt: ps.at,
		ServiceDate: spec.serviceDate, TransactionType: spec.transactionType, ChargeCodeID: spec.chargeCodeID, PaymentID: spec.paymentID,
		ReversesItemID: spec.reversesItemID, StayID: ps.folio.StayID, ReferenceType: spec.referenceType, ReferenceID: spec.referenceID,
		Description: spec.description, Quantity: a.quantity, UnitPrice: a.unitPrice, PriceMode: a.priceMode, BaseAmount: a.base,
		DiscountAmount: a.discount, NetAmount: a.net, RoundingAdjustment: a.rounding, ServiceChargeTotal: a.service, TaxTotal: a.tax,
		Debit: a.debit, Credit: a.credit, Source: "MANUAL", Reason: spec.reason, IdempotencyKey: spec.key, ActorID: ps.p.ActorID(),
		ApprovedBy: spec.approvedBy,
	})
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	for _, c := range spec.components {
		c.TenantID, c.PropertyID, c.FolioItemID = ps.p.TenantID, ps.propertyID, item.ID
		if err := q.InsertFolioItemComponent(ctx, c); err != nil {
			return foliosdb.FolioItem{}, err
		}
	}
	err = q.BumpFolio(ctx, foliosdb.BumpFolioParams{TenantID: ps.p.TenantID, PropertyID: ps.propertyID, ID: ps.folio.ID, ActorID: ps.p.ActorID()})
	return item, err
}

func componentsOf(b chargecalc.Breakdown) []foliosdb.InsertFolioItemComponentParams {
	var out []foliosdb.InsertFolioItemComponentParams
	for _, c := range b.ServiceComponents {
		id := c.RuleID
		out = append(out, foliosdb.InsertFolioItemComponentParams{
			ComponentType: chargecalc.TypeServiceCharge, ServiceChargeID: &id, Code: c.Code, Name: c.Name, Rate: c.Rate,
			BaseAmount: c.BaseAmount, Amount: c.Amount, Sequence: int16(c.Sequence), //nolint:gosec // G115: sequences are small
		})
	}
	for _, c := range b.TaxComponents {
		id := c.RuleID
		out = append(out, foliosdb.InsertFolioItemComponentParams{
			ComponentType: chargecalc.TypeTax, TaxID: &id, Code: c.Code, Name: c.Name, Rate: c.Rate, TaxOnService: c.OnService,
			BaseAmount: c.BaseAmount, Amount: c.Amount, Sequence: int16(c.Sequence), //nolint:gosec // G115: sequences are small
		})
	}
	return out
}

// chargeCmd is a manual charge to post.
type chargeCmd struct {
	chargeCodeID int64
	quantity     decimal.Decimal
	unitPrice    *decimal.Decimal
	priceMode    *chargecalc.PriceMode
	discount     decimal.Decimal
	serviceDate  civil.Date
	description  string
	key          string
}

// postCharge posts a manual charge. Its breakdown comes only from the charge calculation service. A ROOM
// charge code is refused: room revenue is posted only by RoomChargePostingService (M11).
func (ps posting) postCharge(ctx context.Context, cmd chargeCmd) (foliosdb.FolioItem, error) {
	rules, err := ps.s.billing.ResolveRules(ctx, ps.p.TenantID, ps.propertyID, cmd.chargeCodeID)
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	if rules.ChargeType == "ROOM" {
		return foliosdb.FolioItem{}, apperr.Conflict("ROOM_CHARGE_REQUIRES_ROOM_POSTING", "room revenue is posted by the room charge posting, not manually").
			WithContext("charge_code", rules.Code)
	}
	price := cmd.unitPrice
	if price == nil {
		price = rules.DefaultUnitPrice
	}
	if price == nil {
		return foliosdb.FolioItem{}, apperr.Invalid("the charge is invalid", fieldErr("unit_price", "REQUIRED", "the charge code has no default price"))
	}
	b, err := ps.s.billing.Calculate(ctx, billingconfig.ChargeRequest{
		PropertyID: ps.propertyID, ChargeCodeID: cmd.chargeCodeID, Quantity: cmd.quantity, UnitPrice: *price, PriceMode: cmd.priceMode, Discount: cmd.discount,
	})
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	desc := cmd.description
	if desc == "" {
		desc = rules.Code
	}
	id := cmd.chargeCodeID
	spec := itemSpec{
		transactionType: TypeCharge, chargeCodeID: &id, serviceDate: cmd.serviceDate, description: desc, amounts: amountsOf(b),
		key: nullable(cmd.key), components: componentsOf(b),
	}
	return ps.insert(ctx, spec)
}

// adjustmentCmd is a signed correction to post.
type adjustmentCmd struct {
	chargeCodeID  int64
	amount        decimal.Decimal
	priceMode     *chargecalc.PriceMode
	reason        string
	relatedItemID *int64
	key           string
	approval      iam.Approval
}

// postAdjustment posts a signed correction on the current business date: the engine runs on a signed base,
// so taxes and service charges follow the sign. It needs a verified approval.
func (ps posting) postAdjustment(ctx context.Context, cmd adjustmentCmd) (foliosdb.FolioItem, error) {
	if cmd.approval.IsZero() {
		return foliosdb.FolioItem{}, errApprovalRequired()
	}
	b, err := ps.s.billing.Calculate(ctx, billingconfig.ChargeRequest{
		PropertyID: ps.propertyID, ChargeCodeID: cmd.chargeCodeID, Quantity: decimal.NewFromInt(1), UnitPrice: cmd.amount, PriceMode: cmd.priceMode,
	})
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	if b.TotalAmount.IsZero() {
		return foliosdb.FolioItem{}, apperr.Invalid("the adjustment is invalid", fieldErr("amount", "INVALID_AMOUNT", "the adjustment amounts to nothing"))
	}
	id, by, reason := cmd.chargeCodeID, cmd.approval.UserID(), cmd.reason
	spec := itemSpec{
		transactionType: TypeAdjustment, chargeCodeID: &id, serviceDate: ps.bd, description: "Adjustment: " + reason, amounts: amountsOf(b),
		reason: &reason, key: nullable(cmd.key), approvedBy: &by, components: componentsOf(b),
	}
	if cmd.relatedItemID != nil {
		spec.referenceType = ptr("FOLIO_ITEM")
		spec.referenceID = ptr(strconv.FormatInt(*cmd.relatedItemID, 10))
	}
	return ps.insert(ctx, spec)
}

func errApprovalRequired() *apperr.Error {
	return apperr.New(apperr.KindInvalid, "APPROVAL_REQUIRED", "this correction needs an approval: the approver's email and password")
}

// postPaymentEntry writes the ledger entry of a payment: a credit of the paid amount.
func (ps posting) postPaymentEntry(ctx context.Context, pay foliosdb.Payment) (foliosdb.FolioItem, error) {
	one := decimal.NewFromInt(1)
	id := pay.ID
	return ps.insert(ctx, itemSpec{
		transactionType: TypePayment, paymentID: &id, serviceDate: ps.bd, description: "Payment " + pay.PaymentNumber + " (" + pay.PaymentMethod + ")",
		amounts: itemAmounts{
			quantity: one, unitPrice: pay.Amount, priceMode: string(chargecalc.Inclusive),
			base: pay.Amount.Neg(), net: pay.Amount.Neg(), credit: pay.Amount,
		},
	})
}

// postRefundEntry writes the ledger entry of a refund: a debit of the refunded amount.
func (ps posting) postRefundEntry(ctx context.Context, pay foliosdb.Payment, reason string) (foliosdb.FolioItem, error) {
	one := decimal.NewFromInt(1)
	id := pay.ID
	spec := itemSpec{
		transactionType: TypeRefund, paymentID: &id, serviceDate: ps.bd, description: "Refund " + pay.PaymentNumber + " (" + pay.PaymentMethod + ")",
		amounts: itemAmounts{
			quantity: one, unitPrice: pay.Amount, priceMode: string(chargecalc.Inclusive),
			base: pay.Amount, net: pay.Amount, debit: pay.Amount,
		},
		reason: nullable(reason),
	}
	return ps.insert(ctx, spec)
}

// reverse writes the same-day reversal of an item: debit and credit swapped, every signed column and every
// component negated. A room-charge item flips its posting register row to REVERSED.
func (ps posting) reverse(ctx context.Context, orig foliosdb.FolioItem, reason string, approval iam.Approval) (foliosdb.FolioItem, error) {
	if approval.IsZero() {
		return foliosdb.FolioItem{}, errApprovalRequired()
	}
	q := ps.s.q(ctx)
	comps, err := q.ListItemComponents(ctx, foliosdb.ListItemComponentsParams{TenantID: ps.p.TenantID, PropertyID: ps.propertyID, ItemIds: []int64{orig.ID}})
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	specComps := make([]foliosdb.InsertFolioItemComponentParams, len(comps))
	for i, c := range comps {
		specComps[i] = foliosdb.InsertFolioItemComponentParams{
			ComponentType: c.ComponentType, TaxID: c.TaxID, ServiceChargeID: c.ServiceChargeID, Code: c.Code, Name: c.Name, Rate: c.Rate,
			TaxOnService: c.TaxOnService, BaseAmount: c.BaseAmount.Neg(), Amount: c.Amount.Neg(), Sequence: c.Sequence,
		}
	}
	by, origID := approval.UserID(), orig.ID
	spec := itemSpec{
		transactionType: TypeReversal, chargeCodeID: orig.ChargeCodeID, reversesItemID: &origID, referenceType: orig.ReferenceType, referenceID: orig.ReferenceID,
		serviceDate: orig.ServiceDate, description: "Reversal: " + orig.Description, reason: &reason, approvedBy: &by, components: specComps,
		amounts: itemAmounts{
			quantity: orig.Quantity, unitPrice: orig.UnitPrice, priceMode: orig.PriceMode, base: orig.BaseAmount.Neg(), discount: orig.DiscountAmount.Neg(),
			net: orig.NetAmount.Neg(), rounding: orig.RoundingAdjustment.Neg(), service: orig.ServiceChargeTotal.Neg(), tax: orig.TaxTotal.Neg(),
			debit: orig.Credit, credit: orig.Debit,
		},
	}
	item, err := ps.insert(ctx, spec)
	if err != nil {
		return foliosdb.FolioItem{}, err
	}
	err = q.FlipPostingRegister(ctx, foliosdb.FlipPostingRegisterParams{PropertyID: ps.propertyID, ItemID: orig.ID, ReversalItemID: &item.ID})
	return item, err
}
