// Package cityledger is the accounts receivable of companies: what folios transferred to them, what they paid
// back, their statement and aging. The balance is derived (transfers minus receipts), never stored.
package cityledger

import (
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
)

// Receipt payment methods.
var receiptMethods = []string{"CASH", "CARD", "BANK_TRANSFER", "OTHER"}

const (
	maxReferenceLen = 100
	maxRemarksLen   = 500
	maxReasonLen    = 500

	ReceiptPosted = "POSTED"
	ReceiptVoided = "VOIDED"
)

// Account is a company with its receivable balance.
type Account struct {
	CompanyID        int64   `json:"company_id"`
	Code             string  `json:"code"`
	Name             string  `json:"name"`
	IsActive         bool    `json:"is_active"`
	CreditLimit      *string `json:"credit_limit"`
	PaymentTermsDays int     `json:"payment_terms_days"`
	Transferred      string  `json:"transferred"`
	Received         string  `json:"received"`
	Balance          string  `json:"balance"`
	// Available is what can still be transferred within the credit limit (null: unlimited).
	Available *string `json:"available"`
}

// Receipt is money a company paid against its account.
type Receipt struct {
	ID              int64      `json:"id"`
	ReceiptNumber   string     `json:"receipt_number"`
	CompanyID       int64      `json:"company_id"`
	Amount          string     `json:"amount"`
	PaymentMethod   string     `json:"payment_method"`
	ReferenceNumber string     `json:"reference_number,omitempty"`
	Remarks         string     `json:"remarks,omitempty"`
	BusinessDate    civil.Date `json:"business_date"`
	PaidAt          time.Time  `json:"paid_at"`
	Status          string     `json:"status"`
	VoidedAt        *time.Time `json:"voided_at"`
	VoidReason      string     `json:"void_reason,omitempty"`
	CreatedBy       *int64     `json:"created_by"`
	ApprovedBy      *int64     `json:"approved_by"`
	// Allocations are the invoices this receipt pays; what is left of the amount is on account.
	Allocations []ReceiptAllocation `json:"allocations"`
}

// ReceiptAllocation is the part of a receipt that pays one invoice.
type ReceiptAllocation struct {
	InvoiceID     int64  `json:"invoice_id"`
	InvoiceNumber string `json:"invoice_number"`
	Amount        string `json:"amount"`
}

// AllocationInput asks for part of a receipt to pay an invoice.
type AllocationInput struct {
	InvoiceID int64  `json:"invoice_id"`
	Amount    string `json:"amount"`
}

// ReceiptResult is a receipt with the account's balance after it.
type ReceiptResult struct {
	Receipt Receipt `json:"receipt"`
	Balance string  `json:"balance"`
}

// ReceiptInput takes a receipt.
type ReceiptInput struct {
	Amount          string `json:"amount"`
	PaymentMethod   string `json:"payment_method"`
	ReferenceNumber string `json:"reference_number"`
	Remarks         string `json:"remarks"`
	// Allocations pay invoices of the company with this receipt (their sum is at most the amount).
	Allocations []AllocationInput `json:"allocations"`
}

// VoidInput voids a receipt of the current business date.
type VoidInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// StatementLine is a transfer (debit) or a receipt (credit) with the running balance of the posted lines.
type StatementLine struct {
	Date               civil.Date `json:"date"`
	Kind               string     `json:"kind"` // TRANSFER or RECEIPT
	Number             string     `json:"number"`
	Description        string     `json:"description"`
	Reference          string     `json:"reference,omitempty"`
	Status             string     `json:"status"`
	Debit              string     `json:"debit"`
	Credit             string     `json:"credit"`
	Balance            string     `json:"balance"`
	FolioNumber        string     `json:"folio_number,omitempty"`
	ConfirmationNumber string     `json:"confirmation_number,omitempty"`
	GuestName          string     `json:"guest_name,omitempty"`
}

// Statement lists the movements of an account between two business dates.
type Statement struct {
	Company        Account         `json:"company"`
	From           *civil.Date     `json:"from"`
	To             *civil.Date     `json:"to"`
	OpeningBalance string          `json:"opening_balance"`
	TotalDebit     string          `json:"total_debit"`
	TotalCredit    string          `json:"total_credit"`
	ClosingBalance string          `json:"closing_balance"`
	Lines          []StatementLine `json:"lines"`
}

// AgingBucket is the unpaid part of transfers of an age range.
type AgingBucket struct {
	Label  string `json:"label"`
	Amount string `json:"amount"`
}

// Aging is what a company owes by age of the transfer. Receipts settle the oldest transfers first.
type Aging struct {
	AsOf    civil.Date    `json:"as_of"`
	Total   string        `json:"total"`
	Buckets []AgingBucket `json:"buckets"`
}

var agingLabels = []string{"0-30", "31-60", "61-90", "90+"}

// bucketOf places a transfer age (in days) in an aging bucket.
func bucketOf(days int) int {
	switch {
	case days <= 30:
		return 0
	case days <= 60:
		return 1
	case days <= 90:
		return 2
	}
	return 3
}

type transfer struct {
	date   civil.Date
	amount decimal.Decimal
}

// ageTransfers settles received against the oldest transfers first and sums what is left per bucket.
// transfers must be sorted oldest first. A receipt surplus (never, because receipts are capped) is ignored.
func ageTransfers(transfers []transfer, received decimal.Decimal, asOf civil.Date) [4]decimal.Decimal {
	var out [4]decimal.Decimal
	left := received
	for _, t := range transfers {
		open := t.amount
		if left.IsPositive() {
			used := decimal.Min(left, open)
			open = open.Sub(used)
			left = left.Sub(used)
		}
		if open.IsPositive() {
			out[bucketOf(t.date.DaysUntil(asOf))] = out[bucketOf(t.date.DaysUntil(asOf))].Add(open)
		}
	}
	return out
}
