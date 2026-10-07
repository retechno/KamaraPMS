// Package bankrec is the bank reconciliation: bank statements are imported for the cash and bank accounts of the books,
// their lines are matched ("cleared") with the journal lines of the account, what the bank shows and the books lack is
// posted from the statement line, and a statement is reconciled when what the bank says at its end equals what the
// cleared journal lines add up to. Nothing here changes a journal line: clearing is a record beside the ledger.
package bankrec

import (
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
)

// BankAccount is an account of the books that is reconciled with a bank.
type BankAccount struct {
	ID             int64           `json:"id"`
	AccountID      int64           `json:"account_id"`
	AccountCode    string          `json:"account_code"`
	AccountName    string          `json:"account_name"`
	Name           string          `json:"name"`
	AccountNumber  string          `json:"account_number,omitempty"`
	IsActive       bool            `json:"is_active"`
	BookBalance    decimal.Decimal `json:"book_balance"`
	ReconciledTo   *civil.Date     `json:"reconciled_to"`
	OpenStatements int             `json:"open_statements"`
	CreatedAt      time.Time       `json:"created_at"`
}

// BankAccountInput registers an account of the books for reconciliation.
type BankAccountInput struct {
	AccountID     int64  `json:"account_id"`
	Name          string `json:"name"`
	AccountNumber string `json:"account_number"`
	IsActive      *bool  `json:"is_active"`
}

// BankAccountPatch changes a bank account; the account of the books never changes.
type BankAccountPatch struct {
	Name          *string `json:"name"`
	AccountNumber *string `json:"account_number"`
	IsActive      *bool   `json:"is_active"`
}

// Statement statuses.
const (
	StatementOpen       = "OPEN"
	StatementReconciled = "RECONCILED"
)

// Statement is an imported bank statement.
type Statement struct {
	ID             int64           `json:"id"`
	BankAccountID  int64           `json:"bank_account_id"`
	BankName       string          `json:"bank_name"`
	AccountCode    string          `json:"account_code"`
	PeriodFrom     civil.Date      `json:"period_from"`
	PeriodTo       civil.Date      `json:"period_to"`
	OpeningBalance decimal.Decimal `json:"opening_balance"`
	ClosingBalance decimal.Decimal `json:"closing_balance"`
	Status         string          `json:"status"`
	Note           string          `json:"note,omitempty"`
	LineCount      int             `json:"line_count"`
	MatchedCount   int             `json:"matched_count"`
	ImportedAt     time.Time       `json:"imported_at"`
	ReconciledAt   *time.Time      `json:"reconciled_at"`
	ReopenReason   string          `json:"reopen_reason,omitempty"`
}

// Clearing ties a journal line to a statement (and to one of its lines).
type Clearing struct {
	ID              int64           `json:"id"`
	StatementLineID *int64          `json:"statement_line_id"`
	JournalLineID   int64           `json:"journal_line_id"`
	Amount          decimal.Decimal `json:"amount"`
	JournalDate     civil.Date      `json:"journal_date"`
	JournalNumber   string          `json:"journal_number"`
	JournalType     string          `json:"journal_type"`
	Description     string          `json:"description,omitempty"`
}

// StatementLine is a line of the statement: money in is positive, money out negative.
type StatementLine struct {
	ID          int64           `json:"id"`
	LineNo      int             `json:"line_no"`
	Date        civil.Date      `json:"line_date"`
	Description string          `json:"description,omitempty"`
	Reference   string          `json:"reference,omitempty"`
	Amount      decimal.Decimal `json:"amount"`
	Cleared     decimal.Decimal `json:"cleared"`
	Matched     bool            `json:"matched"`
	Clearings   []Clearing      `json:"clearings"`
}

// Summary compares the bank with the books at the end of the statement. The statement can be reconciled when every line
// is matched and the closing balance of the bank is what the cleared journal lines add up to: then the bank balance plus
// what the books show that the bank does not yet (money in transit) less outstanding payments is the book balance.
type Summary struct {
	StatementClosing decimal.Decimal `json:"statement_closing"`
	BookBalance      decimal.Decimal `json:"book_balance"`
	ClearedTotal     decimal.Decimal `json:"cleared_total"`
	UnclearedIn      decimal.Decimal `json:"uncleared_in"`
	UnclearedOut     decimal.Decimal `json:"uncleared_out"`
	UnclearedCount   int             `json:"uncleared_count"`
	UnmatchedLines   int             `json:"unmatched_lines"`
	UnmatchedAmount  decimal.Decimal `json:"unmatched_amount"`
	AdjustedBank     decimal.Decimal `json:"adjusted_bank"`
	Difference       decimal.Decimal `json:"difference"`
	Blockers         []string        `json:"blockers"`
	CanReconcile     bool            `json:"can_reconcile"`
}

// StatementDetail is a statement with its lines, clearings and reconciliation summary.
type StatementDetail struct {
	Statement
	Lines     []StatementLine `json:"lines"`
	Clearings []Clearing      `json:"clearings"`
	Summary   Summary         `json:"summary"`
}

// UnclearedLine is a journal line of the account that is not cleared in full yet.
type UnclearedLine struct {
	JournalLineID int64           `json:"journal_line_id"`
	Date          civil.Date      `json:"journal_date"`
	JournalID     int64           `json:"journal_id"`
	JournalNumber string          `json:"journal_number"`
	JournalType   string          `json:"journal_type"`
	Description   string          `json:"description,omitempty"`
	Reference     string          `json:"reference,omitempty"`
	Amount        decimal.Decimal `json:"amount"`
	Cleared       decimal.Decimal `json:"cleared"`
	Remaining     decimal.Decimal `json:"remaining"`
}

// ImportInput imports a statement: its period, the balances the bank prints and the lines as CSV.
type ImportInput struct {
	BankAccountID  int64           `json:"bank_account_id"`
	PeriodFrom     civil.Date      `json:"period_from"`
	PeriodTo       civil.Date      `json:"period_to"`
	OpeningBalance decimal.Decimal `json:"opening_balance"`
	ClosingBalance decimal.Decimal `json:"closing_balance"`
	Note           string          `json:"note"`
	CSV            string          `json:"csv"`
	// Currency is optional: when the statement says its currency it must be the one of the property (a bank account is a property-currency account).
	Currency string `json:"currency"`
}

// ClearInput matches journal lines with statement lines. Either `journal_line_ids` (each cleared for what is left of it,
// against `statement_line_id`) or `allocations` (explicit parts: a journal line can be cleared in parts by several
// statement lines, as when the day close carries the total of several transfers). Without a statement line the journal
// lines are cleared on their own: those from before the bank reconciliation started (first statement of the account,
// dated before it), or ones that offset each other (a voided payment and its reversal).
type ClearInput struct {
	StatementLineID *int64            `json:"statement_line_id"`
	JournalLineIDs  []int64           `json:"journal_line_ids"`
	Allocations     []ClearAllocation `json:"allocations"`
}

// ClearAllocation clears a part of a journal line against a statement line; without an amount, what is left of it.
type ClearAllocation struct {
	StatementLineID *int64           `json:"statement_line_id"`
	JournalLineID   int64            `json:"journal_line_id"`
	Amount          *decimal.Decimal `json:"amount"`
}

// SettleInput settles card or e-wallet payments from a statement line: the acquirer paid the net amount of the line for
// the payment lines chosen, and kept the difference as its commission.
type SettleInput struct {
	AccountKey     string  `json:"account_key"`
	JournalLineIDs []int64 `json:"journal_line_ids"`
	FeeAccountID   int64   `json:"fee_account_id"`
	// VATAmount is the final VAT in the deduction of the bank, the part the acquirer charged on its commission. Absent: the VAT proposed from the payments settled is used. Present (also "0"): it is final.
	VATAmount    *string `json:"vat_amount"`
	DepartmentID *int64  `json:"department_id"` // of the commission expense, when it belongs to one
	Description  string  `json:"description"`
}

// AdjustInput posts what the bank shows and the books lack (a bank fee, interest) against another account.
type AdjustInput struct {
	AccountID    int64  `json:"account_id"`
	DepartmentID *int64 `json:"department_id"` // of the other account's line, when it belongs to one
	Description  string `json:"description"`
}

// ReopenInput reopens a reconciled statement.
type ReopenInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// AutoMatchResult says what automatic matching did.
type AutoMatchResult struct {
	Matched   int `json:"matched"`
	Remaining int `json:"remaining"`
}

// autoMatchDays is how far apart the dates of a statement line and a journal line of the same amount may be.
const autoMatchDays = 3

// autoMatchRefDays is how far apart they may be when the journal line carries the reference of the statement line.
const autoMatchRefDays = 14
