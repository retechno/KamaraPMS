// Package taxfiling is the filing of the taxes a hotel collects from its guests (the hotel tax, PB1, VAT): the monthly
// worksheet of the tax collected, read from the tax snapshots of the folio items; the return, which is that worksheet
// frozen when it is filed; the payments to the tax authority, which post a journal against the tax payable account of
// the books; and the report of what is owed.
package taxfiling

import (
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
)

// Profile says how a tax is filed.
type Profile struct {
	ID                 int64           `json:"id"`
	TaxID              int64           `json:"tax_id"`
	TaxCode            string          `json:"tax_code"`
	TaxName            string          `json:"tax_name"`
	TaxRate            decimal.Decimal `json:"tax_rate"`
	TaxKind            string          `json:"tax_kind"`
	GLAccountCode      string          `json:"gl_account_code,omitempty"`
	Authority          string          `json:"authority"`
	RegistrationNumber string          `json:"registration_number,omitempty"`
	DueDay             int             `json:"due_day"`
	IsActive           bool            `json:"is_active"`
	// ClaimsInputVAT is set on the profile of the VAT tax whose returns claim the input VAT of the supplier bills; OpeningCredit is the VAT credit
	// the hotel started with.
	ClaimsInputVAT bool            `json:"claims_input_vat"`
	OpeningCredit  decimal.Decimal `json:"opening_credit"`
	CreatedAt      time.Time       `json:"created_at"`
}

// ProfileInput sets up the filing of a tax.
type ProfileInput struct {
	TaxID              int64  `json:"tax_id"`
	Authority          string `json:"authority"`
	RegistrationNumber string `json:"registration_number"`
	DueDay             *int   `json:"due_day"`
	IsActive           *bool  `json:"is_active"`
	ClaimsInputVAT     *bool  `json:"claims_input_vat"`
}

// ProfilePatch changes the filing of a tax; the tax never changes.
type ProfilePatch struct {
	Authority          *string `json:"authority"`
	RegistrationNumber *string `json:"registration_number"`
	DueDay             *int    `json:"due_day"`
	IsActive           *bool   `json:"is_active"`
	ClaimsInputVAT     *bool   `json:"claims_input_vat"`
}

// WorksheetLine is the tax collected on a charge code at a rate in the month.
type WorksheetLine struct {
	ChargeCode string          `json:"charge_code"`
	ChargeName string          `json:"charge_name,omitempty"`
	Rate       decimal.Decimal `json:"rate"`
	Items      int             `json:"items"`
	Base       decimal.Decimal `json:"base_amount"`
	Tax        decimal.Decimal `json:"tax_amount"`
}

// CreditNotesCode is the charge code of the lines of a worksheet that are the credit notes to companies of the month.
const CreditNotesCode = "CREDIT_NOTE"

// Return statuses.
const (
	ReturnFiled  = "FILED"
	ReturnVoided = "VOIDED"
)

// Payment statuses of a return, derived from what has been paid.
const (
	PayUnpaid  = "UNPAID"
	PayPartial = "PARTIAL"
	PayPaid    = "PAID"
	PayVoided  = "VOIDED"
)

// Return is a tax return: the worksheet of a month, frozen when it was filed.
type Return struct {
	ID              int64           `json:"id"`
	Number          string          `json:"return_number"`
	TaxID           int64           `json:"tax_id"`
	TaxCode         string          `json:"tax_code"`
	TaxName         string          `json:"tax_name"`
	PeriodStart     civil.Date      `json:"period_start"`
	PeriodEnd       civil.Date      `json:"period_end"`
	DueDate         civil.Date      `json:"due_date"`
	Base            decimal.Decimal `json:"base_amount"`
	Tax             decimal.Decimal `json:"tax_amount"`
	Status          string          `json:"status"`
	FiledOn         civil.Date      `json:"filed_on"`
	FilingReference string          `json:"filing_reference,omitempty"`
	Notes           string          `json:"notes,omitempty"`
	FiledAt         time.Time       `json:"filed_at"`
	VoidedAt        *time.Time      `json:"voided_at"`
	VoidReason      string          `json:"void_reason,omitempty"`
	Paid            decimal.Decimal `json:"paid"`
	Outstanding     decimal.Decimal `json:"outstanding"`
	PaymentStatus   string          `json:"payment_status"`
	Overdue         bool            `json:"overdue"`
	// VATOffset is frozen with the return: what was claimed, what was brought forward, what was offset, what is payable (the return is
	// paid up to it) and what the next month starts with. Without input VAT the payable is the tax.
	VATOffset
	OffsetJournalID *int64          `json:"offset_journal_id"`
	Input           []InputClaim    `json:"input,omitempty"`
	Lines           []WorksheetLine `json:"lines,omitempty"`
	Payments        []Payment       `json:"payments,omitempty"`
}

// Worksheet is the tax collected in a month, with the checks that say whether it can be filed.
type Worksheet struct {
	Profile     Profile         `json:"profile"`
	PeriodStart civil.Date      `json:"period_start"`
	PeriodEnd   civil.Date      `json:"period_end"`
	DueDate     civil.Date      `json:"due_date"`
	Lines       []WorksheetLine `json:"lines"`
	Base        decimal.Decimal `json:"base_amount"`
	Tax         decimal.Decimal `json:"tax_amount"`
	// The input side (only the profile that claims the input VAT has any) and what it comes to against the tax collected.
	ClaimsInputVAT bool         `json:"claims_input_vat"`
	Input          []InputClaim `json:"input"`
	VATOffset
	GLCollected decimal.Decimal `json:"gl_collected"`
	Difference  decimal.Decimal `json:"difference"`
	Days        int             `json:"days"`
	PostedDays  int             `json:"posted_days"`
	Ready       bool            `json:"ready"`
	Blockers    []string        `json:"blockers"`
	Return      *Return         `json:"return"`
}

// Period is a month of a tax in the list of its returns.
type Period struct {
	PeriodStart          civil.Date      `json:"period_start"`
	PeriodEnd            civil.Date      `json:"period_end"`
	DueDate              civil.Date      `json:"due_date"`
	Tax                  decimal.Decimal `json:"tax_amount"`
	Payable              decimal.Decimal `json:"payable"`
	CreditCarriedForward decimal.Decimal `json:"credit_carried_forward"`
	Status               string          `json:"status"` // OPEN (the month is not over or not all journaled), READY, FILED
	ReturnID             *int64          `json:"return_id"`
	Paid                 decimal.Decimal `json:"paid"`
	Outstanding          decimal.Decimal `json:"outstanding"`
	Overdue              bool            `json:"overdue"`
}

// FileInput files the return of a month.
type FileInput struct {
	TaxID           int64      `json:"tax_id"`
	PeriodStart     civil.Date `json:"period_start"`
	FiledOn         civil.Date `json:"filed_on"`
	FilingReference string     `json:"filing_reference"`
	Notes           string     `json:"notes"`
}

// VoidInput voids a return or a payment with a reason and an approval.
type VoidInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// Payment is a payment to the tax authority against a return.
type Payment struct {
	ID              int64           `json:"id"`
	Number          string          `json:"payment_number"`
	ReturnID        int64           `json:"return_id"`
	ReturnNumber    string          `json:"return_number"`
	TaxCode         string          `json:"tax_code"`
	TaxName         string          `json:"tax_name"`
	PeriodStart     civil.Date      `json:"period_start"`
	PaymentDate     civil.Date      `json:"payment_date"`
	Amount          decimal.Decimal `json:"amount"`
	Penalty         decimal.Decimal `json:"penalty"`
	Total           decimal.Decimal `json:"total"`
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
}

// PayInput pays the tax authority. The penalty (late filing or payment) is paid with it and goes to an expense account.
type PayInput struct {
	PaymentDate      civil.Date      `json:"payment_date"`
	Amount           decimal.Decimal `json:"amount"`
	Penalty          decimal.Decimal `json:"penalty"`
	PenaltyAccountID int64           `json:"penalty_account_id"`
	DepartmentID     *int64          `json:"department_id"` // of the penalty expense, when it belongs to one
	PaymentMethod    string          `json:"payment_method"`
	ReferenceNumber  string          `json:"reference_number"`
	Remarks          string          `json:"remarks"`
}

// LiabilityLine is what is owed to the authority for one tax as of a date.
type LiabilityLine struct {
	TaxID           int64           `json:"tax_id"`
	TaxCode         string          `json:"tax_code"`
	TaxName         string          `json:"tax_name"`
	Authority       string          `json:"authority,omitempty"`
	AccountCode     string          `json:"account_code"`
	Collected       decimal.Decimal `json:"collected"`
	Filed           decimal.Decimal `json:"filed"`
	Offset          decimal.Decimal `json:"offset"`
	CreditAvailable decimal.Decimal `json:"credit_available"`
	Unfiled         decimal.Decimal `json:"unfiled"`
	Paid            decimal.Decimal `json:"paid"`
	Owed            decimal.Decimal `json:"owed"`
	OverdueUnfiled  int             `json:"overdue_unfiled_months"`
	OverdueUnpaid   decimal.Decimal `json:"overdue_unpaid"`
	ReturnsFiled    int             `json:"returns_filed"`
	RegistrationNum string          `json:"registration_number,omitempty"`
}

// LiabilityAccount compares the tax payable account of the books with what the taxes using it say is owed.
type LiabilityAccount struct {
	AccountCode string          `json:"account_code"`
	Books       decimal.Decimal `json:"books"`
	Owed        decimal.Decimal `json:"owed"`
	Difference  decimal.Decimal `json:"difference"`
}

// Liability is what is owed to the tax authority as of a date.
type Liability struct {
	AsOf     civil.Date         `json:"as_of"`
	Taxes    []LiabilityLine    `json:"taxes"`
	Accounts []LiabilityAccount `json:"accounts"`
	Owed     decimal.Decimal    `json:"owed"`
}

// ReturnFilter narrows the return list.
type ReturnFilter struct {
	TaxID  *int64
	Status string
	Limit  int
}

// PaymentFilter narrows the payment list.
type PaymentFilter struct {
	ReturnID *int64
	Status   string
	Limit    int
}

var methodAccount = map[string]string{"CASH": "CASH", "BANK_TRANSFER": "BANK_TRANSFER", "OTHER": "OTHER_PAYMENT"}

// Zero value of a month for periodStart/periodEnd helpers.
func periodStart(d civil.Date) civil.Date { return civil.NewDate(d.Year(), d.Month(), 1) }

func periodEnd(start civil.Date) civil.Date {
	return civil.NewDate(start.Year(), start.Month()+1, 1).AddDays(-1)
}

// dueDate is the due day of the month after the one the return covers.
func dueDate(start civil.Date, dueDay int) civil.Date {
	return civil.NewDate(start.Year(), start.Month()+1, dueDay)
}
