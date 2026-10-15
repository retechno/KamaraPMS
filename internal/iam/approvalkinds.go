package iam

import (
	"kamarapms/internal/platform/auth"
)

// ApprovalKind is a kind of approval a person understands (docs/architecture/32-distinct-approver.md section 2). The kind
// of a use case is fixed in the code; the rule of a kind, and the limit of its amount, are the property's.
type ApprovalKind string

// The kinds. A use case that asks for an approval names one of them; a kind that is not in approvalKinds is refused.
const (
	KindFolioAdjustment  ApprovalKind = "FOLIO_ADJUSTMENT"
	KindFolioReversal    ApprovalKind = "FOLIO_REVERSAL"
	KindRefund           ApprovalKind = "REFUND"
	KindFolioTransfer    ApprovalKind = "FOLIO_TRANSFER"
	KindStayRate         ApprovalKind = "STAY_RATE"
	KindRateOverride     ApprovalKind = "RATE_OVERRIDE"
	KindSalesRestriction ApprovalKind = "SALES_RESTRICTION"
	KindFreeRoom         ApprovalKind = "FREE_ROOM"
	KindCityLedgerCredit ApprovalKind = "CITY_LEDGER_CREDIT" //nolint:gosec // G101: the name of a kind of approval (a credit note), not a credential
	KindCityLedgerVoid   ApprovalKind = "CITY_LEDGER_VOID"
	KindJournal          ApprovalKind = "JOURNAL"
	KindFiscalYear       ApprovalKind = "FISCAL_YEAR"
	KindBank             ApprovalKind = "BANK"
	KindBudget           ApprovalKind = "BUDGET"
	KindPayables         ApprovalKind = "PAYABLES"
	KindTaxDocument      ApprovalKind = "TAX_DOCUMENT"
	KindTaxSettings      ApprovalKind = "TAX_SETTINGS"
	KindShiftVariance    ApprovalKind = "SHIFT_VARIANCE"
	// The kinds of the point of sale (docs/architecture/34-pos.md 8.2): the void of a sold line, a discount, a complimentary bill and the reopening of a bill.
	KindPOSVoid     ApprovalKind = "POS_VOID"
	KindPOSDiscount ApprovalKind = "POS_DISCOUNT"
	KindPOSComp     ApprovalKind = "POS_COMP"
	KindPOSReopen   ApprovalKind = "POS_REOPEN"
)

// KindInfo describes a kind: whether it has an amount (a rule DISTINCT_ABOVE needs one) and the permission its approver
// must hold at the property. The label of a kind is a key of the screens (approvalRules.kind.<KIND>).
type KindInfo struct {
	Kind       ApprovalKind    `json:"kind"`
	HasAmount  bool            `json:"has_amount"`
	Permission auth.Permission `json:"permission"`
}

// approvalKinds is the one table of the kinds, in the order the screen lists them.
var approvalKinds = []KindInfo{
	{KindFolioAdjustment, true, auth.PermCorrectionApprove},
	{KindFolioReversal, true, auth.PermCorrectionApprove},
	{KindRefund, true, auth.PermCorrectionApprove},
	{KindFolioTransfer, true, auth.PermCorrectionApprove},
	{KindStayRate, true, auth.PermCorrectionApprove},
	{KindRateOverride, true, auth.PermReservationOverrideApprove},
	{KindSalesRestriction, false, auth.PermReservationRestrictionApprove},
	{KindFreeRoom, true, auth.PermReservationComplimentaryApprove},
	{KindCityLedgerCredit, true, auth.PermCorrectionApprove},
	{KindCityLedgerVoid, true, auth.PermCorrectionApprove},
	{KindJournal, true, auth.PermCorrectionApprove},
	{KindFiscalYear, false, auth.PermCorrectionApprove},
	{KindBank, false, auth.PermCorrectionApprove},
	{KindBudget, false, auth.PermBudgetApprove},
	{KindPayables, true, auth.PermCorrectionApprove},
	{KindTaxDocument, true, auth.PermCorrectionApprove},
	{KindTaxSettings, false, auth.PermCorrectionApprove},
	{KindShiftVariance, true, auth.PermCashierShiftApprove},
	{KindPOSVoid, true, auth.PermPOSApprove},
	{KindPOSDiscount, true, auth.PermPOSApprove},
	{KindPOSComp, true, auth.PermPOSApprove},
	{KindPOSReopen, true, auth.PermPOSApprove},
}

// ApprovalKinds lists the kinds, in screen order.
func ApprovalKinds() []KindInfo { return append([]KindInfo(nil), approvalKinds...) }

// KindOf finds a kind in the table.
func KindOf(k ApprovalKind) (KindInfo, bool) {
	for _, ki := range approvalKinds {
		if ki.Kind == k {
			return ki, true
		}
	}
	return KindInfo{}, false
}
