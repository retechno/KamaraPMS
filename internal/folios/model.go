// Package folios holds the guest ledger: folios, their append-only items and the payments taken against them
// (docs/architecture/03-financial-engines.md steps 10 and 11, 06-api.md §14).
package folios

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/chargecalc"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
)

// Transaction types of a ledger item.
const (
	TypeCharge     = "CHARGE"
	TypeAdjustment = "ADJUSTMENT"
	TypePayment    = "PAYMENT"
	TypeRefund     = "REFUND"
	TypeReversal   = "REVERSAL"
)

// Payment types and statuses.
const (
	PaymentTypePayment = "PAYMENT"
	PaymentTypeRefund  = "REFUND"
	PaymentPosted      = "POSTED"
	PaymentVoided      = "VOIDED"
)

const (
	maxReasonLen      = 500
	maxDescriptionLen = 300
	maxRemarksLen     = 500
	maxReferenceLen   = 100
	maxQuantityScale  = 3
)

// MethodCityLedger is the payment method of a transfer to a company account; it is not offered as a way to pay.
const MethodCityLedger = "CITY_LEDGER"

// methodCash is the payment method that goes through a cashier shift.
const methodCash = "CASH"

var paymentMethods = []string{"CASH", "CARD", "BANK_TRANSFER", "OTHER"}

func fieldErr(field, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: field, Code: code, Message: msg}
}

// ChargeInput is a manual charge.
type ChargeInput struct {
	ChargeCodeID int64       `json:"charge_code_id"`
	Quantity     string      `json:"quantity"`
	UnitPrice    *string     `json:"unit_price"`
	PriceMode    *string     `json:"price_mode"`
	Discount     string      `json:"discount_amount"`
	ServiceDate  *civil.Date `json:"service_date"`
	Description  string      `json:"description"`
}

// AdjustmentInput is a signed correction on the current business date. Amount is read in PriceMode terms.
type AdjustmentInput struct {
	ChargeCodeID  int64              `json:"charge_code_id"`
	Amount        string             `json:"amount"`
	PriceMode     *string            `json:"price_mode"`
	Reason        string             `json:"reason"`
	RelatedItemID *int64             `json:"related_item_id"`
	Approval      *iam.ApprovalInput `json:"approval"`
}

// CorrectionInput is a reversal, void or refund request body's common part.
type CorrectionInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// PaymentInput takes a payment or a deposit.
type PaymentInput struct {
	Amount          string `json:"amount"`
	PaymentMethod   string `json:"payment_method"`
	ReferenceNumber string `json:"reference_number"`
	Remarks         string `json:"remarks"`

	companyID *int64 // set by Transfer only: a city ledger transfer is not a payment a client may post
}

// TransferInput moves part of a folio's balance to a company's city ledger account.
type TransferInput struct {
	CompanyID       int64  `json:"company_id"`
	Amount          string `json:"amount"`
	ReferenceNumber string `json:"reference_number"`
	Remarks         string `json:"remarks"`
}

// RefundInput refunds part or all of a payment.
type RefundInput struct {
	Amount          string             `json:"amount"`
	PaymentMethod   string             `json:"payment_method"`
	ReferenceNumber string             `json:"reference_number"`
	Reason          string             `json:"reason"`
	Approval        *iam.ApprovalInput `json:"approval"`
}

// FolioFilter narrows the folio list.
type FolioFilter struct {
	ReservationID *int64
	StayID        *int64
	Status        string
}

// PaymentFilter narrows the cashier list.
type PaymentFilter struct {
	BusinessDate *civil.Date
	Method       string
}

func parsePositive(field, s string, decimals int32) (decimal.Decimal, *apperr.FieldError) {
	d, err := money.Parse(strings.TrimSpace(s))
	if err != nil || !d.IsPositive() {
		return decimal.Zero, ptr(fieldErr(field, "INVALID_AMOUNT", "a positive amount"))
	}
	if !d.Equal(d.Round(decimals)) {
		return decimal.Zero, ptr(fieldErr(field, "INVALID_AMOUNT", "at most the currency's decimals"))
	}
	return d, nil
}

func ptr[T any](v T) *T { return &v }

func validateMethod(m string) *apperr.FieldError {
	for _, ok := range paymentMethods {
		if m == ok {
			return nil
		}
	}
	return ptr(fieldErr("payment_method", "INVALID_VALUE", "one of "+strings.Join(paymentMethods, ", ")))
}

func validateText(field, v string, max int) []apperr.FieldError {
	if len([]rune(v)) > max {
		return []apperr.FieldError{fieldErr(field, "TOO_LONG", "too long")}
	}
	return nil
}

func requireReason(reason string) (string, []apperr.FieldError) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", []apperr.FieldError{fieldErr("reason", "REQUIRED", "a reason is required")}
	}
	return reason, validateText("reason", reason, maxReasonLen)
}

func priceModeOf(s *string) (*chargecalc.PriceMode, *apperr.FieldError) {
	if s == nil {
		return nil, nil
	}
	m := chargecalc.PriceMode(strings.ToUpper(strings.TrimSpace(*s)))
	if m != chargecalc.Exclusive && m != chargecalc.Inclusive {
		return nil, ptr(fieldErr("price_mode", "INVALID_VALUE", "EXCLUSIVE or INCLUSIVE"))
	}
	return &m, nil
}

// ---------------------------------------------------------------------------
// Views

// Component is a tax or service charge applied to an item, as snapshotted when it was posted.
type Component struct {
	ComponentType string `json:"component_type"`
	Code          string `json:"code"`
	Name          string `json:"name"`
	Rate          string `json:"rate"`
	BaseAmount    string `json:"base_amount"`
	Amount        string `json:"amount"`
	Sequence      int    `json:"sequence"`
	// GLAccountCode is the account the tax or service charge was mapped to when the item was posted.
	GLAccountCode *string `json:"gl_account_code"`
}

// Item is a ledger line.
type Item struct {
	ID              int64      `json:"id"`
	FolioID         int64      `json:"folio_id"`
	TransactionType string     `json:"transaction_type"`
	BusinessDate    civil.Date `json:"business_date"`
	ServiceDate     civil.Date `json:"service_date"`
	TransactionAt   time.Time  `json:"transaction_at"`
	Description     string     `json:"description"`
	ChargeCode      string     `json:"charge_code,omitempty"`
	ChargeCodeID    *int64     `json:"charge_code_id"`
	// RevenueAccountCode is the revenue account of the charge code when the item was posted (a reversal
	// carries the account of the item it reverses).
	RevenueAccountCode *string     `json:"revenue_account_code"`
	Quantity           string      `json:"quantity"`
	UnitPrice          string      `json:"unit_price"`
	PriceMode          string      `json:"price_mode"`
	BaseAmount         string      `json:"base_amount"`
	DiscountAmount     string      `json:"discount_amount"`
	NetAmount          string      `json:"net_amount"`
	RoundingAdjustment string      `json:"rounding_adjustment"`
	ServiceChargeTotal string      `json:"service_charge_total"`
	TaxTotal           string      `json:"tax_total"`
	Debit              string      `json:"debit"`
	Credit             string      `json:"credit"`
	Components         []Component `json:"components"`
	PaymentID          *int64      `json:"payment_id"`
	ReversesItemID     *int64      `json:"reverses_item_id"`
	ReversedByItemID   *int64      `json:"reversed_by_item_id"`
	// GroupCode is the transaction group the line is shown under (GroupDefault when it was never moved; a reversal has the group of the line it reverses). Presentation only.
	GroupCode  string `json:"group_code"`
	Reason     string `json:"reason,omitempty"`
	RoomNumber string `json:"room_number,omitempty"`
	CreatedBy  *int64 `json:"created_by"`
	ApprovedBy *int64 `json:"approved_by"`
}

// Totals are the debit and credit sums of a folio.
type Totals struct {
	Debit  string `json:"debit"`
	Credit string `json:"credit"`
}

// Folio is the detail view. Balance is debit minus credit; it is never stored.
type Folio struct {
	ID          int64  `json:"id"`
	FolioNumber string `json:"folio_number"`
	FolioType   string `json:"folio_type"`
	// BillToCompanyID is the company a COMPANY folio is billed to (nil on a guest folio).
	BillToCompanyID   *int64     `json:"bill_to_company_id"`
	BillToCompanyName string     `json:"bill_to_company_name,omitempty"`
	Status            string     `json:"status"`
	ReservationID     int64      `json:"reservation_id"`
	StayID            *int64     `json:"stay_id"`
	OpenedAt          time.Time  `json:"opened_at"`
	ClosedAt          *time.Time `json:"closed_at"`
	Version           int32      `json:"version"`
	Balance           string     `json:"balance"`
	Totals            Totals     `json:"totals"`
	Items             []Item     `json:"items"`
}

// FolioSummary is a row of the folio list.
type FolioSummary struct {
	ID              int64     `json:"id"`
	FolioNumber     string    `json:"folio_number"`
	FolioType       string    `json:"folio_type"`
	BillToCompanyID *int64    `json:"bill_to_company_id"`
	Status          string    `json:"status"`
	ReservationID   int64     `json:"reservation_id"`
	StayID          *int64    `json:"stay_id"`
	OpenedAt        time.Time `json:"opened_at"`
	Version         int32     `json:"version"`
	Balance         string    `json:"balance"`
}

// Payment is a payment or refund.
type Payment struct {
	ID                int64      `json:"id"`
	PaymentNumber     string     `json:"payment_number"`
	FolioID           int64      `json:"folio_id"`
	PaymentType       string     `json:"payment_type"`
	PaymentMethod     string     `json:"payment_method"`
	CompanyID         *int64     `json:"company_id"`
	Amount            string     `json:"amount"`
	PaidAt            time.Time  `json:"paid_at"`
	BusinessDate      civil.Date `json:"business_date"`
	ReferenceNumber   string     `json:"reference_number,omitempty"`
	RefundOfPaymentID *int64     `json:"refund_of_payment_id"`
	Status            string     `json:"status"`
	VoidedAt          *time.Time `json:"voided_at"`
	VoidReason        string     `json:"void_reason,omitempty"`
	Remarks           string     `json:"remarks,omitempty"`
	Refundable        *string    `json:"refundable,omitempty"`
	// GroupCode is the transaction group of the ledger line of this payment (presentation only; the payment stays on its folio).
	GroupCode string `json:"group_code"`
	// The fee the acquirer is expected to keep, from the rate that applied on the day (card and e-wallet payments only), and the day it should pay out.
	MDRRate *string `json:"mdr_rate,omitempty"`
	MDRFee  *string `json:"mdr_fee,omitempty"`
	// The VAT rate of the rule and the VAT expected on the fee; absent for a payment taken before the VAT was kept ("without rate").
	MDRVATRate             *string     `json:"mdr_vat_rate,omitempty"`
	MDRVAT                 *string     `json:"mdr_vat,omitempty"`
	ExpectedSettlementDate *civil.Date `json:"expected_settlement_date,omitempty"`
	CreatedBy              *int64      `json:"created_by"`
	ApprovedBy             *int64      `json:"approved_by"`
}

// ItemResult is a posted item with the folio's balance after it.
type ItemResult struct {
	Item         Item   `json:"item"`
	FolioBalance string `json:"folio_balance"`
}

// PaymentResult is a payment with its ledger entry and the folio's balance after it.
type PaymentResult struct {
	Payment      Payment `json:"payment"`
	FolioItem    Item    `json:"folio_item"`
	FolioBalance string  `json:"folio_balance"`
}

// MethodTotal is the cashier total of one payment method for a business date.
type MethodTotal struct {
	PaymentMethod string `json:"payment_method"`
	Paid          string `json:"paid"`
	Refunded      string `json:"refunded"`
	Net           string `json:"net"`
}

// PaymentList is the cashier list with totals.
type PaymentList struct {
	Data   []Payment     `json:"data"`
	Totals []MethodTotal `json:"totals,omitempty"`
}
