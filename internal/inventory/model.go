// Package inventory is the stock of the property: items, warehouses, the stock ledger, its balances, the costing of both methods and the opening stock
// (docs/architecture/35-inventory.md, PR 1). The ledger and its balances are written by one piece of code only, ledger.go (stockpath_guard_test.go holds it).
package inventory

import (
	"regexp"
	"strings"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/apperr"
)

// Costing methods (35-inventory.md 4). One per property, locked by the first movement.
const (
	MethodAverage = "AVERAGE"
	MethodFIFO    = "FIFO"
)

// Kinds of a movement of the ledger (inv_movements.kind). PR 1 writes OPENING; the others are the vocabulary of the ledger writer, used by the later PRs.
const (
	KindOpening         = "OPENING"
	KindReceipt         = "RECEIPT"
	KindReceiptReversal = "RECEIPT_REVERSAL"
	KindSupplierReturn  = "SUPPLIER_RETURN"
	KindTransferOut     = "TRANSFER_OUT"
	KindTransferIn      = "TRANSFER_IN"
	KindIssue           = "ISSUE"
	KindIssueReversal   = "ISSUE_REVERSAL"
	KindWaste           = "WASTE"
	KindStaffMeal       = "STAFF_MEAL"
	KindEntertainment   = "ENTERTAINMENT"
	KindCountGain       = "COUNT_GAIN"
	KindCountLoss       = "COUNT_LOSS"
	KindPosSale         = "POS_SALE"
	KindPosSaleReturn   = "POS_SALE_RETURN"
	KindCogsToWaste     = "COGS_TO_WASTE"
	KindCostAdjust      = "COST_ADJUST"
	KindNegSettle       = "NEG_SETTLE"
)

// inKinds put goods into a warehouse, outKinds take them out. The other three kinds move value only.
var (
	inKinds  = map[string]bool{KindOpening: true, KindReceipt: true, KindTransferIn: true, KindCountGain: true, KindPosSaleReturn: true, KindIssueReversal: true}
	outKinds = map[string]bool{KindReceiptReversal: true, KindSupplierReturn: true, KindTransferOut: true, KindIssue: true, KindWaste: true, KindStaffMeal: true,
		KindEntertainment: true, KindCountLoss: true, KindPosSale: true}
	// settlesShortfalls are the kinds of goods coming in that settle the stock sold below zero (4.4): a receipt and a transfer in. A count gain does not (it closes them, PR 3).
	settlesShortfalls = map[string]bool{KindReceipt: true, KindTransferIn: true}
)

// Classes (4.9): the class of the category of the item, and the class a cost is charged to.
const (
	ClassFood     = "FOOD"
	ClassBeverage = "BEVERAGE"
	ClassOther    = "OTHER"
)

// ClassOfKind is the class of the items of a category of the given kind (FOOD and BEVERAGE; supplies and the rest are OTHER).
func ClassOfKind(kind string) string {
	switch kind {
	case "FOOD":
		return ClassFood
	case "BEVERAGE":
		return ClassBeverage
	}
	return ClassOther
}

// Kinds of a category, of a warehouse; sources of a shortfall.
var (
	categoryKinds  = map[string]bool{"FOOD": true, "BEVERAGE": true, "SUPPLIES": true, "OTHER": true}
	warehouseKinds = map[string]bool{"MAIN": true, "OUTLET": true, "DEPARTMENT": true}
	costClasses    = map[string]bool{ClassFood: true, ClassBeverage: true, ClassOther: true}
)

// Limits and patterns of the master data. The database holds the same patterns; the service answers with a field error before it gets there.
const (
	maxName    = 100
	maxItem    = 150
	qtyScale   = 6 // quantities are stored with six decimals (35-inventory.md 4.1)
	maxFactor  = 1_000_000
	maxPageRow = 200
)

var (
	unitCodePattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,9}$`)
	categoryCodePattern  = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)
	itemCodePattern      = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,29}$`)
	warehouseCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)
)

// Round6 rounds a quantity to the six decimals the ledger keeps.
func Round6(d decimal.Decimal) decimal.Decimal { return d.Round(qtyScale) }

// StockQty converts a quantity in a unit of the item (1 of the unit = num/den stock units) into stock units, rounded to six decimals.
func StockQty(qty decimal.Decimal, num, den int32) decimal.Decimal {
	return Round6(qty.Mul(decimal.NewFromInt32(num)).Div(decimal.NewFromInt32(den)))
}

// UnitCost is value / qty to six decimals (information only; the value is what is booked).
func UnitCost(value, qty decimal.Decimal) decimal.Decimal {
	if qty.IsZero() {
		return decimal.Zero
	}
	return value.Div(qty).Abs().Round(qtyScale)
}

func trimmed(s string) string { return strings.TrimSpace(s) }

func errNegativeStock(warehouseID, itemID int64, available, wanted decimal.Decimal) *apperr.Error {
	return apperr.Conflict("NEGATIVE_STOCK_NOT_ALLOWED", "the warehouse does not have the quantity: only a sale at the point of sale and the amenities of the close may take stock below zero").
		WithContext("warehouse_id", warehouseID).WithContext("item_id", itemID).WithContext("available", available.String()).WithContext("requested", wanted.String())
}
