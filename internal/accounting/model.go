// Package accounting is the general ledger of a property: its chart of accounts (laid out after USALI), the accounts the
// system posts to, the journals that the day close writes from the guest ledger and that accountants add by hand, the
// accounting periods, and the ledgers and financial statements read from the journals.
package accounting

import "time"

// Account types and the side each normally carries.
const (
	TypeAsset     = "ASSET"
	TypeLiability = "LIABILITY"
	TypeEquity    = "EQUITY"
	TypeRevenue   = "REVENUE"
	TypeExpense   = "EXPENSE"

	SideDebit  = "DEBIT"
	SideCredit = "CREDIT"
)

var accountTypes = []string{TypeAsset, TypeLiability, TypeEquity, TypeRevenue, TypeExpense}

// groupsOf lists the statement groups an account of a type may belong to. A statement group is what the balance sheet
// and the income statement add up.
var groupsOf = map[string][]string{
	TypeAsset:     {"CASH", "RECEIVABLES", "INVENTORIES", "PREPAID", "FIXED_ASSETS", "OTHER_ASSETS"},
	TypeLiability: {"PAYABLES", "ACCRUED", "DEPOSITS", "TAXES_PAYABLE", "OTHER_CURRENT_LIABILITIES", "LONG_TERM_DEBT", "SUSPENSE"},
	TypeEquity:    {"EQUITY"},
	TypeRevenue:   {"REV_ROOMS", "REV_FB", "REV_OOD", "REV_RENTAL_OTHER", "REV_MISC"},
	TypeExpense:   {"EXP_ROOMS", "EXP_FB", "EXP_OOD", "UND_AG", "UND_IT", "UND_SM", "UND_POM", "UND_UTIL", "MGMT_FEES", "NONOP", "DEPRECIATION", "INTEREST", "INCOME_TAX"},
}

// defaultSide is the normal balance of a type; a contra account (accumulated depreciation, allowances) carries the other.
func defaultSide(accountType string) string {
	if accountType == TypeAsset || accountType == TypeExpense {
		return SideDebit
	}
	return SideCredit
}

// Keys of the accounts the system posts to (gl_account_map).
const (
	KeyCash             = "CASH"
	KeyCard             = "CARD"
	KeyBankTransfer     = "BANK_TRANSFER"
	KeyOtherPayment     = "OTHER_PAYMENT"
	KeyCityLedger       = "CITY_LEDGER"
	KeyGuestLedger      = "GUEST_LEDGER"
	KeyAdvanceDeposits  = "ADVANCE_DEPOSITS"
	KeyTaxPayable       = "TAX_PAYABLE"
	KeyServicePayable   = "SERVICE_PAYABLE"
	KeySuspense         = "SUSPENSE"
	KeyRetainedEarnings = "RETAINED_EARNINGS"
)

// mapKeys lists the keys with the account type each must point to ("" = any).
var mapKeys = []struct{ Key, Type, Meaning string }{
	{KeyCash, TypeAsset, "Cash payments"},
	{KeyCard, TypeAsset, "Card payments (clearing)"},
	{KeyBankTransfer, TypeAsset, "Bank transfer payments"},
	{KeyOtherPayment, TypeAsset, "Other payments (e-wallet, vouchers)"},
	{KeyCityLedger, TypeAsset, "What companies owe (city ledger)"},
	{KeyGuestLedger, TypeAsset, "What in-house guests owe (guest ledger)"},
	{KeyAdvanceDeposits, TypeLiability, "Deposits held until the guest checks out"},
	{KeyTaxPayable, TypeLiability, "Tax with no account of its own"},
	{KeyServicePayable, TypeLiability, "Service charge with no account of its own"},
	{KeySuspense, "", "Anything that cannot be placed: look here after each day close"},
	{KeyRetainedEarnings, TypeEquity, "Where the result of a fiscal year is closed to"},
}

// Account is an account of the chart.
type Account struct {
	ID             int64     `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	AccountType    string    `json:"account_type"`
	NormalSide     string    `json:"normal_side"`
	ParentID       *int64    `json:"parent_id"`
	ParentCode     string    `json:"parent_code,omitempty"`
	IsPostable     bool      `json:"is_postable"`
	IsActive       bool      `json:"is_active"`
	StatementGroup string    `json:"statement_group,omitempty"`
	Description    string    `json:"description,omitempty"`
	InUse          bool      `json:"in_use"`
	CreatedAt      time.Time `json:"created_at"`
}

// AccountInput creates an account. Postable and Active default to true; NormalSide to the side of the type.
type AccountInput struct {
	Code           string `json:"code"`
	Name           string `json:"name"`
	AccountType    string `json:"account_type"`
	NormalSide     string `json:"normal_side"`
	ParentID       *int64 `json:"parent_id"`
	IsPostable     *bool  `json:"is_postable"`
	IsActive       *bool  `json:"is_active"`
	StatementGroup string `json:"statement_group"`
	Description    string `json:"description"`
}

// AccountPatch changes an account; nil fields stay. The code, the type and the normal side never change.
type AccountPatch struct {
	Name           *string `json:"name"`
	ParentID       *int64  `json:"parent_id"` // below 1 removes the parent
	IsPostable     *bool   `json:"is_postable"`
	IsActive       *bool   `json:"is_active"`
	StatementGroup *string `json:"statement_group"`
	Description    *string `json:"description"`
}

// AccountFilter narrows the account list.
type AccountFilter struct {
	AccountType    string
	StatementGroup string
	Active         *bool
	Postable       *bool
	Query          string
}

// MapEntry is one account the system posts to.
type MapEntry struct {
	Key         string `json:"map_key"`
	Meaning     string `json:"meaning"`
	AccountID   int64  `json:"account_id"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	AccountType string `json:"account_type"`
}

// MapInput points a key at an account.
type MapInput struct {
	Key       string `json:"map_key"`
	AccountID int64  `json:"account_id"`
}

// CodeIssue is a charge code, tax or service charge whose account code the journals cannot use.
type CodeIssue struct {
	Kind    string `json:"kind"` // CHARGE_CODE, TAX or SERVICE_CHARGE
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Account string `json:"gl_account_code,omitempty"`
	Problem string `json:"problem"` // NO_CODE, UNKNOWN_ACCOUNT, INACTIVE_ACCOUNT, HEADER_ACCOUNT or WRONG_TYPE
	// PostedTo is where its amounts go instead.
	PostedTo string `json:"posted_to"`
}

// CodeReport lists what is not mapped to the chart of accounts.
type CodeReport struct {
	Checked int         `json:"checked"`
	Issues  []CodeIssue `json:"issues"`
}

// ImportResult says what a CSV import did (or would do, for a dry run).
type ImportResult struct {
	DryRun  bool `json:"dry_run"`
	Created int  `json:"created"`
	Updated int  `json:"updated"`
}

// Journal sources.
const (
	SourceDayClose = "DAY_CLOSE"
	SourceManual   = "MANUAL"
	SourceReversal = "REVERSAL"
)
