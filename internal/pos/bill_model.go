package pos

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
)

// The bills of an outlet (docs/architecture/34-pos.md 3.3, 5, 7). This file is the pure part: the shapes, the statuses and the arithmetic that has no database in it.

// Statuses and kinds.
const (
	BillOpen   = "OPEN"
	BillClosed = "CLOSED"
	BillVoid   = "VOID"
	BillMerged = "MERGED"

	OrderDineIn      = "DINE_IN"
	OrderTakeaway    = "TAKEAWAY"
	OrderRoomService = "ROOM_SERVICE"
	OrderDirect      = "DIRECT"

	LineDraft = "DRAFT"
	LineSent  = "SENT"
	LineVoid  = "VOID"

	// The methods of a payment line of a bill.
	PayCash         = "CASH"
	PayCard         = "CARD"
	PayQRIS         = "QRIS"
	PayBankTransfer = "BANK_TRANSFER"
	PayOther        = "OTHER"
	PayRoom         = "ROOM"
	PayCompany      = "COMPANY"
	PayComp         = "COMP"

	// The derived state of a table.
	TableFree            = "FREE"
	TableOccupied        = "OCCUPIED"
	TableAwaitingPayment = "AWAITING_PAYMENT"
	TableHeld            = "HELD"

	maxBillLines  = 300
	maxPayLines   = 8
	maxNoteLen    = 200
	maxReasonLen  = 200
	maxPageOfBill = 200
)

var orderTypes = []string{OrderDineIn, OrderTakeaway, OrderRoomService, OrderDirect}

// LineOption is a choice of a line, snapshotted: a later change of the product never changes a bill.
type LineOption struct {
	OptionID   int64           `json:"option_id"`
	GroupID    int64           `json:"group_id"`
	Name       string          `json:"name"`
	PriceDelta decimal.Decimal `json:"price_delta"`
	Qty        int             `json:"qty"`
}

// LineOptionInput is a choice asked for.
type LineOptionInput struct {
	OptionID int64 `json:"option_id"`
	Qty      int   `json:"qty"`
}

// BillLine is a line of a bill, as the screens show it.
type BillLine struct {
	ID             int64            `json:"id"`
	ClientUUID     string           `json:"client_uuid"`
	ProductID      int64            `json:"product_id"`
	ProductName    string           `json:"product_name"`
	Options        []LineOption     `json:"options"`
	Quantity       decimal.Decimal  `json:"quantity"`
	UnitPrice      decimal.Decimal  `json:"unit_price"`
	DiscountPct    *decimal.Decimal `json:"discount_pct"`
	DiscountAmount decimal.Decimal  `json:"discount_amount"`
	DiscountReason string           `json:"discount_reason,omitempty"`
	IsComp         bool             `json:"is_comp"`
	Note           string           `json:"note,omitempty"`
	Status         string           `json:"status"`
	Round          int              `json:"round"`
	// The amounts of the line: of its ledger row when it was sent, the quote of the one charge engine while it is a draft.
	Base    decimal.Decimal `json:"base_amount"`
	Net     decimal.Decimal `json:"net_amount"`
	Service decimal.Decimal `json:"service_amount"`
	Tax     decimal.Decimal `json:"tax_amount"`
	Total   decimal.Decimal `json:"total"`
	// TicketStatus is the status of the ticket of the line (NEW, COOKING, READY, SERVED, CANCELLED), empty while it is a draft or has no ticket.
	TicketStatus   string     `json:"ticket_status,omitempty"`
	FolioItemID    *int64     `json:"folio_item_id"`
	CreatedByName  string     `json:"created_by_name,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	SentAt         *time.Time `json:"sent_at"`
	VoidReason     string     `json:"void_reason,omitempty"`
	VoidedByName   string     `json:"voided_by_name,omitempty"`
	ApprovedByName string     `json:"approved_by_name,omitempty"`
	VoidedAt       *time.Time `json:"voided_at"`
}

// BillPayment is a payment of the folio of a bill.
type BillPayment struct {
	ID        int64           `json:"id"`
	Number    string          `json:"payment_number"`
	Method    string          `json:"method"`
	Amount    decimal.Decimal `json:"amount"`
	Reference string          `json:"reference,omitempty"`
	Label     string          `json:"label,omitempty"`
	PaidAt    time.Time       `json:"paid_at"`
	Status    string          `json:"status"`
	CompanyID *int64          `json:"company_id"`
}

// BillRoomCharge is a charge of the bill to a room.
type BillRoomCharge struct {
	ID         int64           `json:"id"`
	Amount     decimal.Decimal `json:"amount"`
	ToFolioID  int64           `json:"to_folio_id"`
	StayID     *int64          `json:"stay_id"`
	Room       string          `json:"room,omitempty"`
	ReturnedAt *time.Time      `json:"returned_at"`
}

// BillTotals are the amounts of a bill. Sold is what the lines come to (service and tax included), voided lines out; Rounding is the line of the system code POS_ROUNDING; Paid is what the
// payments took; Balance is Sold plus Rounding less Paid, and Payable adds the quote of the drafts.
type BillTotals struct {
	Base     decimal.Decimal `json:"base"`
	Discount decimal.Decimal `json:"discount"`
	Net      decimal.Decimal `json:"net"`
	Service  decimal.Decimal `json:"service"`
	Tax      decimal.Decimal `json:"tax"`
	Sold     decimal.Decimal `json:"sold"`
	Drafts   decimal.Decimal `json:"drafts"`
	Rounding decimal.Decimal `json:"rounding"`
	Paid     decimal.Decimal `json:"paid"`
	// Transferred is what was charged to a room (the pair of TRF lines of the bills that were charged to a room).
	Transferred decimal.Decimal `json:"transferred"`
	Balance     decimal.Decimal `json:"balance"`
	// Payable is what is left to pay now, drafts included: the balance of the folio and the quote of the lines that are not sent yet.
	Payable decimal.Decimal `json:"payable"`
	// CashPayable is Payable rounded as the outlet rounds cash (equal to Payable when it does not round).
	CashPayable decimal.Decimal `json:"cash_payable"`
}

// Bill is a bill of an outlet with its lines, payments and totals.
type Bill struct {
	ID                 int64            `json:"id"`
	ClientUUID         string           `json:"client_uuid"`
	OutletID           int64            `json:"outlet_id"`
	OutletCode         string           `json:"outlet_code"`
	OutletName         string           `json:"outlet_name"`
	FolioID            *int64           `json:"folio_id"`
	Number             string           `json:"bill_number"`
	Status             string           `json:"status"`
	OrderType          string           `json:"order_type"`
	TableID            *int64           `json:"table_id"`
	TableCode          string           `json:"table_code,omitempty"`
	GuestCount         *int             `json:"guest_count"`
	TargetStayID       *int64           `json:"target_stay_id"`
	TargetRoom         string           `json:"target_room,omitempty"`
	CustomerName       string           `json:"customer_name,omitempty"`
	WaiterID           *int64           `json:"waiter_id"`
	WaiterName         string           `json:"waiter_name,omitempty"`
	CompTreatment      string           `json:"comp_treatment"`
	BusinessDateOpened civil.Date       `json:"business_date_opened"`
	OpenedAt           time.Time        `json:"opened_at"`
	BillRequestedAt    *time.Time       `json:"bill_requested_at"`
	ClosedAt           *time.Time       `json:"closed_at"`
	ClosedBusinessDate *civil.Date      `json:"closed_business_date"`
	MergedIntoBillID   *int64           `json:"merged_into_bill_id"`
	Note               string           `json:"note,omitempty"`
	Version            int32            `json:"version"`
	Lines              []BillLine       `json:"lines"`
	Payments           []BillPayment    `json:"payments"`
	RoomCharges        []BillRoomCharge `json:"room_charges"`
	Totals             BillTotals       `json:"totals"`
	Currency           string           `json:"currency,omitempty"`
}

// BillSummary is a row of a list of bills.
type BillSummary struct {
	ID           int64           `json:"id"`
	Number       string          `json:"bill_number"`
	Status       string          `json:"status"`
	OrderType    string          `json:"order_type"`
	TableID      *int64          `json:"table_id"`
	TableCode    string          `json:"table_code,omitempty"`
	GuestCount   *int            `json:"guest_count"`
	TargetStayID *int64          `json:"target_stay_id"`
	TargetRoom   string          `json:"target_room,omitempty"`
	CustomerName string          `json:"customer_name,omitempty"`
	WaiterName   string          `json:"waiter_name,omitempty"`
	OpenedAt     time.Time       `json:"opened_at"`
	Minutes      int             `json:"minutes"`
	RequestedAt  *time.Time      `json:"bill_requested_at"`
	ClosedAt     *time.Time      `json:"closed_at"`
	Total        decimal.Decimal `json:"total"`
	DraftLines   int             `json:"draft_lines"`
	SentLines    int             `json:"sent_lines"`
	Version      int32           `json:"version"`
}

// OpenBillInput opens a bill. ClientUUID is the id the client made (a retry returns the bill that exists).
type OpenBillInput struct {
	ClientUUID   string `json:"client_uuid"`
	OrderType    string `json:"order_type"`
	TableID      *int64 `json:"table_id"`
	GuestCount   *int   `json:"guest_count"`
	TargetStayID *int64 `json:"target_stay_id"`
	CustomerName string `json:"customer_name"`
	Note         string `json:"note"`
}

// UpdateBillInput changes what is told about a bill: the table (a move), the guests, the name, the note. Version is the one read.
type UpdateBillInput struct {
	TableID      *int64 `json:"table_id"`
	OrderType    string `json:"order_type"`
	GuestCount   *int   `json:"guest_count"`
	CustomerName string `json:"customer_name"`
	Note         string `json:"note"`
	Version      int32  `json:"version"`
}

// AddLineInput adds a draft line. The price is the price list's, never the client's.
type AddLineInput struct {
	ClientUUID string            `json:"client_uuid"`
	ProductID  int64             `json:"product_id"`
	Quantity   decimal.Decimal   `json:"quantity"`
	Options    []LineOptionInput `json:"options"`
	Note       string            `json:"note"`
}

// UpdateLineInput changes a draft line.
type UpdateLineInput struct {
	Quantity decimal.Decimal   `json:"quantity"`
	Options  []LineOptionInput `json:"options"`
	Note     string            `json:"note"`
}

// SendInput sends the draft lines of a bill. ClientUUID is the id of the round (a retry sends nothing twice).
type SendInput struct {
	ClientUUID string `json:"client_uuid"`
}

// VoidLineInput voids a sent line with a reason and the approval of the kind POS_VOID.
type VoidLineInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// DiscountInput gives a discount on a line or on the bill: a percentage or an amount of the base, with a reason.
type DiscountInput struct {
	Scope    string             `json:"scope"` // LINE or BILL
	LineID   *int64             `json:"line_id"`
	Percent  *decimal.Decimal   `json:"percent"`
	Amount   *decimal.Decimal   `json:"amount"`
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// CompInput makes lines (or the whole bill) complimentary under the treatment the bill kept (DISCOUNT_100), with the name of the guest or of the authorising manager.
type CompInput struct {
	LineIDs  []int64            `json:"line_ids"`
	Reason   string             `json:"reason"`
	Name     string             `json:"name"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// PayLine is one payment of a request to pay.
type PayLine struct {
	Method    string           `json:"method"`
	Amount    decimal.Decimal  `json:"amount"`
	Reference string           `json:"reference"`
	Label     string           `json:"label"`
	StayID    *int64           `json:"stay_id"`
	CompanyID *int64           `json:"company_id"`
	Tendered  *decimal.Decimal `json:"tendered"`
}

// PayInput pays a bill with one or more payments, all in one transaction. Name and Reason are asked of a complimentary payment.
type PayInput struct {
	Payments []PayLine          `json:"payments"`
	Reason   string             `json:"reason"`
	Name     string             `json:"name"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// PayResult is the bill after the payment and what the cashier needs at the till: the change due and whether the drawer opens.
type PayResult struct {
	Bill       Bill            `json:"bill"`
	Change     decimal.Decimal `json:"change"`
	Closed     bool            `json:"closed"`
	OpenDrawer bool            `json:"open_drawer"`
}

// SplitInput moves lines of a bill to a new bill (a split by item). ClientUUID is the uuid of the new bill.
type SplitInput struct {
	ClientUUID string  `json:"client_uuid"`
	LineIDs    []int64 `json:"line_ids"`
}

// MergeInput merges the bill of the path into another open bill of the same outlet.
type MergeInput struct {
	IntoBillID int64 `json:"into_bill_id"`
}

// ReopenInput reopens a closed bill: a reason and the approval POS_REOPEN.
type ReopenInput struct {
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// RoomChargeCorrectionInput returns a charge to a room, or moves it to another room (StayID).
type RoomChargeCorrectionInput struct {
	StayID   *int64             `json:"stay_id"`
	Reason   string             `json:"reason"`
	Approval *iam.ApprovalInput `json:"approval"`
}

// ---------------------------------------------------------------- arithmetic

// RoundToUnit rounds an amount to the nearest unit, half up (the cash of an outlet that rounds, 7.4). A unit of 0 does not round.
func RoundToUnit(amount decimal.Decimal, unit int) decimal.Decimal {
	if unit <= 0 {
		return amount
	}
	u := decimal.NewFromInt(int64(unit))
	half := u.Div(decimal.NewFromInt(2))
	if amount.IsNegative() {
		return amount.Sub(half).Div(u).Ceil().Mul(u)
	}
	return amount.Add(half).Div(u).Floor().Mul(u)
}

// Allocate divides total over the bases in proportion, in whole units of the currency's smallest step, so that the parts add up to total exactly (the largest remainder method; ties go to the
// first). No part is above its base. A total above the sum of the bases is not allocated (nil).
func Allocate(total decimal.Decimal, bases []decimal.Decimal, decimals int32) []decimal.Decimal {
	sum := decimal.Zero
	for _, b := range bases {
		sum = sum.Add(b)
	}
	if total.IsNegative() || total.GreaterThan(sum) || sum.IsZero() {
		return nil
	}
	step := decimal.New(1, -decimals)
	parts := make([]decimal.Decimal, len(bases))
	type rem struct {
		i int
		r decimal.Decimal
	}
	rems := make([]rem, len(bases))
	given := decimal.Zero
	for i, b := range bases {
		exact := total.Mul(b).Div(sum)
		part := exact.Div(step).Floor().Mul(step)
		parts[i] = part
		rems[i] = rem{i, exact.Sub(part)}
		given = given.Add(part)
	}
	sort.SliceStable(rems, func(a, b int) bool { return rems[a].r.GreaterThan(rems[b].r) })
	left := total.Sub(given).Div(step).IntPart()
	for k := 0; left > 0 && k < len(rems); k++ {
		i := rems[k].i
		if parts[i].Add(step).GreaterThan(bases[i]) {
			continue
		}
		parts[i] = parts[i].Add(step)
		left--
	}
	for k := 0; left > 0; k = (k + 1) % len(rems) { // a base was already full: the rest goes where there is room
		i := rems[k].i
		if parts[i].Add(step).GreaterThan(bases[i]) {
			continue
		}
		parts[i] = parts[i].Add(step)
		left--
	}
	return parts
}

// SplitEqually divides an amount between n payers at the currency's decimals: the last takes the remainder (7.6, 5.3).
func SplitEqually(total decimal.Decimal, n int, decimals int32) []decimal.Decimal {
	if n < 1 {
		return nil
	}
	share := total.Div(decimal.NewFromInt(int64(n))).RoundDown(decimals)
	out := make([]decimal.Decimal, n)
	given := decimal.Zero
	for i := 0; i < n-1; i++ {
		out[i] = share
		given = given.Add(share)
	}
	out[n-1] = total.Sub(given)
	return out
}

func errBillNotOpen() *apperr.Error {
	return apperr.Conflict("BILL_NOT_OPEN", "the bill is not open")
}

func errBillNotFound() *apperr.Error { return notFound("BILL_NOT_FOUND", "bill") }
