// Package payables is accounts payable: suppliers, the bills they send, the payments that settle them and the aging of
// what the hotel owes. A bill and a payment each post a journal to the general ledger when they are entered (through
// accounting.Poster), so the expenses of the hotel are in the books and the USALI statements.
package payables

import (
	"regexp"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
)

var supplierCode = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)

// Payment methods and the system account of the books each is paid from.
var methodAccount = map[string]string{"CASH": "CASH", "BANK_TRANSFER": "BANK_TRANSFER", "OTHER": "OTHER_PAYMENT"}

// Supplier is somebody the hotel buys from.
type Supplier struct {
	ID                 int64           `json:"id"`
	Code               string          `json:"code"`
	Name               string          `json:"name"`
	ContactName        string          `json:"contact_name,omitempty"`
	Email              string          `json:"email,omitempty"`
	Phone              string          `json:"phone,omitempty"`
	Address            string          `json:"address,omitempty"`
	City               string          `json:"city,omitempty"`
	TaxID              string          `json:"tax_id,omitempty"`
	PaymentTermsDays   int             `json:"payment_terms_days"`
	DefaultAccountID   *int64          `json:"default_account_id"`
	DefaultAccountCode string          `json:"default_account_code,omitempty"`
	DefaultAccountName string          `json:"default_account_name,omitempty"`
	BankDetails        string          `json:"bank_details,omitempty"`
	Notes              string          `json:"notes,omitempty"`
	IsActive           bool            `json:"is_active"`
	Outstanding        decimal.Decimal `json:"outstanding"`
	CreatedAt          time.Time       `json:"created_at"`
}

// SupplierInput creates a supplier.
type SupplierInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	ContactName      string `json:"contact_name"`
	Email            string `json:"email"`
	Phone            string `json:"phone"`
	Address          string `json:"address"`
	City             string `json:"city"`
	TaxID            string `json:"tax_id"`
	PaymentTermsDays *int   `json:"payment_terms_days"`
	DefaultAccountID *int64 `json:"default_account_id"`
	BankDetails      string `json:"bank_details"`
	Notes            string `json:"notes"`
	IsActive         *bool  `json:"is_active"`
}

// SupplierPatch changes a supplier; the code never changes. A default account below 1 removes it.
type SupplierPatch struct {
	Name             *string `json:"name"`
	ContactName      *string `json:"contact_name"`
	Email            *string `json:"email"`
	Phone            *string `json:"phone"`
	Address          *string `json:"address"`
	City             *string `json:"city"`
	TaxID            *string `json:"tax_id"`
	PaymentTermsDays *int    `json:"payment_terms_days"`
	DefaultAccountID *int64  `json:"default_account_id"`
	BankDetails      *string `json:"bank_details"`
	Notes            *string `json:"notes"`
	IsActive         *bool   `json:"is_active"`
}

// SupplierFilter narrows the supplier list.
type SupplierFilter struct {
	Active *bool
	Q      string
}

// BillLine is a line of a bill: what was bought and the account it is charged to.
type BillLine struct {
	LineNo      int32           `json:"line_no"`
	AccountID   int64           `json:"account_id"`
	AccountCode string          `json:"account_code"`
	AccountName string          `json:"account_name"`
	Description string          `json:"description,omitempty"`
	Amount      decimal.Decimal `json:"amount"`
	// VATAmount is the VAT paid on the line (on top of Amount) and VATTreatment how it was booked on the bill date:
	// CREDITABLE, EXPENSE or DEFERRED (empty when there is no VAT).
	VATAmount    decimal.Decimal `json:"vat_amount"`
	VATTreatment string          `json:"vat_treatment,omitempty"`
	// The department or sub-department the cost belongs to, when the line names one.
	DepartmentID   *int64 `json:"department_id"`
	DepartmentCode string `json:"department_code,omitempty"`
	DepartmentName string `json:"department_name,omitempty"`
}

// Payment statuses of a bill, derived from what has been paid.
const (
	BillUnpaid  = "UNPAID"
	BillPartial = "PARTIAL"
	BillPaid    = "PAID"
	BillVoided  = "VOIDED"
)

// Bill is a supplier invoice entered in the books.
type Bill struct {
	ID                    int64           `json:"id"`
	Number                string          `json:"bill_number"`
	SupplierID            int64           `json:"supplier_id"`
	SupplierCode          string          `json:"supplier_code"`
	SupplierName          string          `json:"supplier_name"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	BillDate              civil.Date      `json:"bill_date"`
	DueDate               civil.Date      `json:"due_date"`
	Description           string          `json:"description,omitempty"`
	Total                 decimal.Decimal `json:"total"`
	Paid                  decimal.Decimal `json:"paid"`
	Outstanding           decimal.Decimal `json:"outstanding"`
	Status                string          `json:"status"`
	PaymentStatus         string          `json:"payment_status"`
	JournalID             int64           `json:"journal_id"`
	JournalNumber         string          `json:"journal_number"`
	VoidJournalID         *int64          `json:"void_journal_id"`
	VoidedAt              *time.Time      `json:"voided_at"`
	VoidReason            string          `json:"void_reason,omitempty"`
	CreatedAt             time.Time       `json:"created_at"`
	Lines                 []BillLine      `json:"lines,omitempty"`
}

// BillLineInput is a line of a new bill.
type BillLineInput struct {
	AccountID   int64           `json:"account_id"`
	Description string          `json:"description"`
	Amount      decimal.Decimal `json:"amount"`
	VATAmount   decimal.Decimal `json:"vat_amount"` // the VAT paid on the line, on top of Amount; zero when there is none
	// DepartmentID is optional: the department or sub-department the cost belongs to.
	DepartmentID *int64 `json:"department_id"`
}

// BillInput enters a supplier bill. The due date defaults to the bill date plus the supplier's payment terms.
type BillInput struct {
	SupplierID            int64           `json:"supplier_id"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	BillDate              civil.Date      `json:"bill_date"`
	DueDate               *civil.Date     `json:"due_date"`
	Description           string          `json:"description"`
	Lines                 []BillLineInput `json:"lines"`
}

// BillFilter narrows the bill list.
type BillFilter struct {
	SupplierID *int64
	Status     string
	From, To   *civil.Date
	Q          string
	OpenOnly   bool
	Limit      int
}

// VoidInput voids a bill or a payment with a reason and an approval.
type VoidInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// Allocation is the part of a payment that settles one bill.
type Allocation struct {
	BillID                int64           `json:"bill_id"`
	BillNumber            string          `json:"bill_number"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	Amount                decimal.Decimal `json:"amount"`
}

// SupplierPayment is a payment to a supplier, which settles bills.
type SupplierPayment struct {
	ID              int64           `json:"id"`
	Number          string          `json:"payment_number"`
	SupplierID      int64           `json:"supplier_id"`
	SupplierCode    string          `json:"supplier_code"`
	SupplierName    string          `json:"supplier_name"`
	PaymentDate     civil.Date      `json:"payment_date"`
	Amount          decimal.Decimal `json:"amount"`
	PaymentMethod   string          `json:"payment_method"`
	ReferenceNumber string          `json:"reference_number,omitempty"`
	Remarks         string          `json:"remarks,omitempty"`
	Status          string          `json:"status"`
	JournalID       int64           `json:"journal_id"`
	JournalNumber   string          `json:"journal_number"`
	VoidJournalID   *int64          `json:"void_journal_id"`
	VoidedAt        *time.Time      `json:"voided_at"`
	VoidReason      string          `json:"void_reason,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
	Allocations     []Allocation    `json:"allocations,omitempty"`
}

// AllocationInput settles part of a bill.
type AllocationInput struct {
	BillID int64           `json:"bill_id"`
	Amount decimal.Decimal `json:"amount"`
}

// PaymentInput pays a supplier. The payment is the sum of its allocations: it always settles bills.
type PaymentInput struct {
	SupplierID      int64             `json:"supplier_id"`
	PaymentDate     civil.Date        `json:"payment_date"`
	PaymentMethod   string            `json:"payment_method"`
	ReferenceNumber string            `json:"reference_number"`
	Remarks         string            `json:"remarks"`
	Allocations     []AllocationInput `json:"allocations"`
}

// PaymentFilter narrows the payment list.
type PaymentFilter struct {
	SupplierID *int64
	Status     string
	From, To   *civil.Date
	Limit      int
}

// OpenBill is what is still owed on a bill, for choosing what a payment settles.
type OpenBill struct {
	BillID                int64           `json:"bill_id"`
	BillNumber            string          `json:"bill_number"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	BillDate              civil.Date      `json:"bill_date"`
	DueDate               civil.Date      `json:"due_date"`
	Total                 decimal.Decimal `json:"total"`
	Outstanding           decimal.Decimal `json:"outstanding"`
}

// AgingBill is an open bill in the aging.
type AgingBill struct {
	BillID                int64           `json:"bill_id"`
	BillNumber            string          `json:"bill_number"`
	SupplierInvoiceNumber string          `json:"supplier_invoice_number"`
	BillDate              civil.Date      `json:"bill_date"`
	DueDate               civil.Date      `json:"due_date"`
	DaysOverdue           int             `json:"days_overdue"`
	Outstanding           decimal.Decimal `json:"outstanding"`
}

// Aging buckets, by days past the due date as of the report date.
var agingBuckets = []string{"CURRENT", "DAYS_1_30", "DAYS_31_60", "DAYS_61_90", "DAYS_OVER_90"}

// AgingSupplier is what is owed to a supplier, split by how late it is.
type AgingSupplier struct {
	SupplierID   int64                      `json:"supplier_id"`
	SupplierCode string                     `json:"supplier_code"`
	SupplierName string                     `json:"supplier_name"`
	Buckets      map[string]decimal.Decimal `json:"buckets"`
	Total        decimal.Decimal            `json:"total"`
	Bills        []AgingBill                `json:"bills"`
}

// Aging is the payables aging as of a date.
type Aging struct {
	AsOf      civil.Date                 `json:"as_of"`
	Suppliers []AgingSupplier            `json:"suppliers"`
	Buckets   map[string]decimal.Decimal `json:"buckets"`
	Total     decimal.Decimal            `json:"total"`
}

func bucketOf(daysOverdue int) string {
	switch {
	case daysOverdue <= 0:
		return agingBuckets[0]
	case daysOverdue <= 30:
		return agingBuckets[1]
	case daysOverdue <= 60:
		return agingBuckets[2]
	case daysOverdue <= 90:
		return agingBuckets[3]
	}
	return agingBuckets[4]
}
