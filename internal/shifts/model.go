package shifts

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/money"
)

// Cashier shifts (design: docs/architecture/11-cashier-budget-cashflow-card.md, part A). A cashier opens a shift with a float, takes cash on it, moves cash
// (a drop to the safe, a pay-in, a pay-out) and closes it with the count; the difference to the cash expected is journaled.

// Statuses of a shift.
const (
	StatusOpen   = "OPEN"
	StatusClosed = "CLOSED"
)

// Kinds of a movement of cash.
const (
	KindDrop   = "DROP"
	KindPayIn  = "PAY_IN"
	KindPayOut = "PAY_OUT"
)

const (
	maxReason     = 500
	maxDrawerLen  = 20
	defaultDrawer = "MAIN"
	maxCountRows  = 50
)

// Settings are the cashier settings of a property.
type Settings struct {
	RequireShiftForCash bool   `json:"require_shift_for_cash"`
	MaxVariance         string `json:"max_variance"`
	BlockNightAudit     bool   `json:"block_night_audit"`
}

// SettingsInput changes the settings.
type SettingsInput struct {
	RequireShiftForCash bool   `json:"require_shift_for_cash"`
	MaxVariance         string `json:"max_variance"`
	BlockNightAudit     bool   `json:"block_night_audit"`
}

// Cash is what went through the drawer of a shift, and the cash that should be in it.
type Cash struct {
	OpeningFloat     string `json:"opening_float"`
	Payments         string `json:"payments"`
	Refunds          string `json:"refunds"`
	Receipts         string `json:"receipts"`
	VoidedAfterClose string `json:"voided_after_close"`
	PayIns           string `json:"pay_ins"`
	PayOuts          string `json:"pay_outs"`
	Drops            string `json:"drops"`
	Expected         string `json:"expected"`
}

// Movement is a drop, a pay-in or a pay-out.
type Movement struct {
	ID            int64      `json:"id"`
	Kind          string     `json:"kind"`
	Amount        string     `json:"amount"`
	AccountID     *int64     `json:"account_id"`
	AccountCode   string     `json:"account_code,omitempty"`
	AccountName   string     `json:"account_name,omitempty"`
	Reason        string     `json:"reason"`
	BusinessDate  civil.Date `json:"business_date"`
	JournalID     *int64     `json:"journal_id"`
	JournalNumber string     `json:"journal_number,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Count is the notes or coins of one denomination counted at close.
type Count struct {
	Denomination string `json:"denomination"`
	Quantity     int    `json:"quantity"`
}

// Shift is a cashier shift.
type Shift struct {
	ID                 int64       `json:"id"`
	Number             string      `json:"number"`
	UserID             int64       `json:"user_id"`
	UserName           string      `json:"user_name"`
	Drawer             string      `json:"drawer"`
	Status             string      `json:"status"`
	OpenedAt           time.Time   `json:"opened_at"`
	BusinessDateOpened civil.Date  `json:"business_date_opened"`
	OpeningFloat       string      `json:"opening_float"`
	ClosedAt           *time.Time  `json:"closed_at"`
	BusinessDateClosed *civil.Date `json:"business_date_closed"`
	ExpectedCash       *string     `json:"expected_cash"`
	CountedCash        *string     `json:"counted_cash"`
	OverShort          *string     `json:"over_short"`
	VarianceReason     string      `json:"variance_reason,omitempty"`
	JournalID          *int64      `json:"journal_id"`
	ApprovedBy         *int64      `json:"approved_by"`
	HandedOverTo       *int64      `json:"handed_over_to"`
	// Cash is computed on the detail of a shift (the expected cash of an open shift so far).
	Cash      *Cash      `json:"cash,omitempty"`
	Movements []Movement `json:"movements,omitempty"`
	Counts    []Count    `json:"counts,omitempty"`
}

// OpenInput opens a shift. An empty drawer is MAIN; an empty float is what the last shift of the drawer left.
type OpenInput struct {
	Drawer       string  `json:"drawer"`
	OpeningFloat *string `json:"opening_float"`
}

// MovementInput records a drop, a pay-in or a pay-out.
type MovementInput struct {
	Kind      string `json:"kind"`
	Amount    string `json:"amount"`
	AccountID int64  `json:"account_id"`
	Reason    string `json:"reason"`
}

// CloseInput closes a shift with the cash counted.
type CloseInput struct {
	CountedCash string             `json:"counted_cash"`
	Counts      []Count            `json:"counts"`
	Reason      string             `json:"reason"`
	Approval    *iam.ApprovalInput `json:"approval"`
	HandOverTo  *int64             `json:"hand_over_to"`
}

// Filter narrows the list of shifts.
type Filter struct {
	UserID *int64
	Status string
	From   *civil.Date
	To     *civil.Date
}

// OpenShift is a shift that is open, as the night audit lists the ones that block it.
type OpenShift struct {
	ID       int64     `json:"id"`
	Number   string    `json:"number"`
	Drawer   string    `json:"drawer"`
	UserID   int64     `json:"user_id"`
	UserName string    `json:"user_name"`
	OpenedAt time.Time `json:"opened_at"`
}

// Expected is the cash that should be in the drawer: the float, plus what came in (cash payments, city ledger receipts, pay-ins), less what went out
// (refunds, pay-outs, drops, and the cash payments of closed shifts that this cashier voided).
func Expected(openingFloat, payments, refunds, receipts, voidedAfterClose, payIns, payOuts, drops decimal.Decimal) decimal.Decimal {
	return openingFloat.Add(payments).Add(receipts).Add(payIns).Sub(refunds).Sub(voidedAfterClose).Sub(payOuts).Sub(drops)
}

// Exceeds says whether a difference is beyond the limit the cashier may close with, which is no approval at all when the limit is 0 and there is a difference.
func Exceeds(overShort, maxVariance decimal.Decimal) bool {
	return overShort.Abs().GreaterThan(maxVariance)
}

func fieldErr(f, code, msg string) apperr.FieldError {
	return apperr.FieldError{Field: f, Code: code, Message: msg}
}

// parseAmount reads an amount of money at the decimals of the currency; zero is allowed when allowZero.
func parseAmount(field, s string, decimals int32, allowZero bool) (decimal.Decimal, *apperr.FieldError) {
	d, err := money.Parse(strings.TrimSpace(s))
	if err != nil || d.IsNegative() || (!allowZero && d.IsZero()) {
		msg := "an amount above zero"
		if allowZero {
			msg = "an amount, not negative"
		}
		fe := fieldErr(field, "INVALID_AMOUNT", msg)
		return decimal.Zero, &fe
	}
	if !d.Equal(d.Round(decimals)) {
		fe := fieldErr(field, "INVALID_AMOUNT", "at most the currency's decimals")
		return decimal.Zero, &fe
	}
	return d, nil
}

func (in MovementInput) parse(decimals int32) (decimal.Decimal, []apperr.FieldError) {
	var fields []apperr.FieldError
	amount, fe := parseAmount("amount", in.Amount, decimals, false)
	if fe != nil {
		fields = append(fields, *fe)
	}
	switch in.Kind {
	case KindDrop:
		if in.AccountID != 0 {
			fields = append(fields, fieldErr("account_id", "NOT_ALLOWED", "a drop has no account: the cash stays in the books"))
		}
	case KindPayIn, KindPayOut:
		if in.AccountID < 1 {
			fields = append(fields, fieldErr("account_id", "REQUIRED", "the account on the other side"))
		}
	default:
		fields = append(fields, fieldErr("kind", "INVALID_VALUE", "DROP, PAY_IN or PAY_OUT"))
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" || len(reason) > maxReason {
		fields = append(fields, fieldErr("reason", "REQUIRED", "1 to 500 characters"))
	}
	return amount, fields
}

func (in OpenInput) parseDrawer() (string, *apperr.FieldError) {
	d := strings.ToUpper(strings.TrimSpace(in.Drawer))
	if d == "" {
		return defaultDrawer, nil
	}
	if len(d) > maxDrawerLen {
		fe := fieldErr("drawer", "INVALID_VALUE", "at most 20 characters")
		return "", &fe
	}
	return d, nil
}
