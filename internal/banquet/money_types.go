package banquet

import (
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/iam"
	"kamarapms/internal/platform/civil"
)

// The requests and the answers of the money of an event (36 8, PR 2).

// LineComponent is a component of a package line as it was frozen when the line was sold.
type LineComponent struct {
	ItemID       int64           `json:"item_id"`
	Name         string          `json:"name"`
	ChargeCodeID int64           `json:"charge_code_id"`
	ChargeCode   string          `json:"charge_code"`
	QtyPerPax    decimal.Decimal `json:"qty_per_pax"`
	ListPrice    decimal.Decimal `json:"list_price"`
	SortOrder    int             `json:"sort_order"`
}

// Line is what is sold in a session: an item or a package, with the price and the mode frozen when it was added.
type Line struct {
	ID                 int64            `json:"id"`
	SessionID          int64            `json:"session_id"`
	LineNo             int              `json:"line_no"`
	ItemID             *int64           `json:"item_id"`
	PackageID          *int64           `json:"package_id"`
	Description        string           `json:"description"`
	Basis              string           `json:"basis"`
	Quantity           decimal.Decimal  `json:"quantity"`
	UnitPrice          decimal.Decimal  `json:"unit_price"`
	PriceMode          string           `json:"price_mode"`
	DiscountPct        *decimal.Decimal `json:"discount_pct"`
	DiscountAmount     *decimal.Decimal `json:"discount_amount"`
	DiscountBy         *int64           `json:"discount_by"`
	DiscountApprovedBy *int64           `json:"discount_approved_by"`
	ChargeCodeID       *int64           `json:"charge_code_id"`
	ChargeCode         *string          `json:"charge_code"`
	FbClass            *string          `json:"fb_class"`
	Components         []LineComponent  `json:"components"`
}

// LineInput adds a line to a session: an item or a package. The price and the mode start from the master and the setting of the property; the unit price is the price of one guest for a package.
type LineInput struct {
	SessionID      int64              `json:"session_id"`
	ItemID         *int64             `json:"item_id"`
	PackageID      *int64             `json:"package_id"`
	Quantity       *decimal.Decimal   `json:"quantity"`
	UnitPrice      *decimal.Decimal   `json:"unit_price"`
	PriceMode      string             `json:"price_mode"`
	Description    *string            `json:"description"`
	DiscountPct    *decimal.Decimal   `json:"discount_pct"`
	DiscountAmount *decimal.Decimal   `json:"discount_amount"`
	Approval       *iam.ApprovalInput `json:"approval"`
}

// LineUpdate changes a line that is not posted: the quantity, the price, the mode and the discount. The master is not read again: the line keeps what it froze unless it is changed here.
type LineUpdate struct {
	Quantity       *decimal.Decimal   `json:"quantity"`
	UnitPrice      *decimal.Decimal   `json:"unit_price"`
	PriceMode      string             `json:"price_mode"`
	Description    *string            `json:"description"`
	DiscountPct    *decimal.Decimal   `json:"discount_pct"`
	DiscountAmount *decimal.Decimal   `json:"discount_amount"`
	ClearDiscount  bool               `json:"clear_discount"`
	Approval       *iam.ApprovalInput `json:"approval"`
}

// EstimateLine is what the engine says a line comes to.
type EstimateLine struct {
	LineID  int64           `json:"line_id"`
	Pax     int             `json:"pax"`
	Net     decimal.Decimal `json:"net"`
	Service decimal.Decimal `json:"service"`
	Tax     decimal.Decimal `json:"tax"`
	Total   decimal.Decimal `json:"total"`
}

// EstimateCode is the net revenue of a charge code of the event.
type EstimateCode struct {
	ChargeCodeID int64  `json:"charge_code_id"`
	ChargeCode   string `json:"charge_code"`
	// The account of the general ledger the charge code posts its revenue to, with its name: what the estimate shows as "revenue by account".
	GLAccountCode *string         `json:"gl_account_code"`
	GLAccountName *string         `json:"gl_account_name"`
	Net           decimal.Decimal `json:"net"`
}

// Estimate is the money of an event by the charge engine (the same plan that posting follows, so the two cannot differ): the guests are the posted number, else the guaranteed (or actual) number,
// else the estimated one.
type Estimate struct {
	Lines   []EstimateLine  `json:"lines"`
	ByCode  []EstimateCode  `json:"by_code"`
	Net     decimal.Decimal `json:"net"`
	Service decimal.Decimal `json:"service"`
	Tax     decimal.Decimal `json:"tax"`
	Total   decimal.Decimal `json:"total"`
}

// LinesView is the lines of an event with their estimate.
type LinesView struct {
	Lines    []Line   `json:"lines"`
	Estimate Estimate `json:"estimate"`
}

// DepositView is a row of the deposit schedule with where it stands.
type DepositView struct {
	Seq        int              `json:"seq"`
	Label      string           `json:"label"`
	DueDate    civil.Date       `json:"due_date"`
	Percent    *decimal.Decimal `json:"percent"`
	Amount     decimal.Decimal  `json:"amount"`
	Cumulative decimal.Decimal  `json:"cumulative"`
	Status     string           `json:"status"`
}

// DepositRowInput is a row of a schedule in a request: a percent of the contract total or a fixed amount, due on a date.
type DepositRowInput struct {
	Label   string           `json:"label"`
	DueDate civil.Date       `json:"due_date"`
	Percent *decimal.Decimal `json:"percent"`
	Amount  *decimal.Decimal `json:"amount"`
}

// ScheduleInput replaces the deposit schedule of an event.
type ScheduleInput struct {
	Rows []DepositRowInput `json:"rows"`
}

// TierInput is a tier in a request.
type TierInput struct {
	DaysFrom int             `json:"days_from"`
	DaysTo   *int            `json:"days_to"`
	Percent  decimal.Decimal `json:"percent"`
	Action   string          `json:"action"`
}

// PolicyInput sets the cancellation policy of an event (banquet.confirm): the base of the percent and the tiers.
type PolicyInput struct {
	Base  string      `json:"base"`
	Tiers []TierInput `json:"tiers"`
}

// Policy is the cancellation policy of an event.
type Policy struct {
	Base  string `json:"base"`
	Tiers []Tier `json:"tiers"`
	// Current is the tier in force today, nothing when none covers the days left or the event has no policy.
	Current  *Tier `json:"current"`
	DaysLeft int   `json:"days_left"`
}

// FolioRef is the EVENT folio of an event.
type FolioRef struct {
	ID      int64           `json:"id"`
	Number  string          `json:"number"`
	Status  string          `json:"status"`
	Debit   decimal.Decimal `json:"debit"`
	Credit  decimal.Decimal `json:"credit"`
	Balance decimal.Decimal `json:"balance"`
}

// Posting is how a session was posted.
type Posting struct {
	SessionID    int64      `json:"session_id"`
	BusinessDate civil.Date `json:"business_date"`
	PaxBilled    int        `json:"pax_billed"`
	PaxBasis     string     `json:"pax_basis"`
	Auto         bool       `json:"auto"`
	OnGuarantee  bool       `json:"on_guarantee"`
	PostedAt     time.Time  `json:"posted_at"`
}

// MoneyView is the money of an event for its page: the estimate, the folio, the deposits and the policy.
type MoneyView struct {
	PostingMode     string          `json:"posting_mode"`
	PostingModeOwn  bool            `json:"posting_mode_own"`
	Estimate        Estimate        `json:"estimate"`
	Folio           *FolioRef       `json:"folio"`
	DepositReceived decimal.Decimal `json:"deposit_received"`
	Schedule        []DepositView   `json:"schedule"`
	ScheduleTotal   decimal.Decimal `json:"schedule_total"`
	Overdue         bool            `json:"overdue"`
	Policy          Policy          `json:"policy"`
	Postings        []Posting       `json:"postings"`
	FeeItemID       *int64          `json:"cancelled_fee_item_id"`
	Groups          []GroupTarget   `json:"groups"`
}

// GroupTarget is a group of the property with a master folio, a place the bill of an event can be transferred to.
type GroupTarget struct {
	GroupID     int64  `json:"group_id"`
	Code        string `json:"code"`
	Name        string `json:"name"`
	FolioID     int64  `json:"folio_id"`
	FolioNumber string `json:"folio_number"`
}

// PostInput posts a session. PaxActual is the number of guests that came, entered with the posting; UseEstimate allows a session with no guaranteed and no actual number to be billed on the
// estimated guests (a person's decision, never the close's).
type PostInput struct {
	PaxActual   *int `json:"pax_actual"`
	UseEstimate bool `json:"use_estimate"`
}

// ActualInput enters the number of guests that came.
type ActualInput struct {
	PaxActual *int `json:"pax_actual"`
}

// PostedCharge is a charge a session posted.
type PostedCharge struct {
	LineID       int64           `json:"line_id"`
	ComponentID  int64           `json:"component_item_id"`
	FolioItemID  int64           `json:"folio_item_id"`
	Description  string          `json:"description"`
	Net          decimal.Decimal `json:"net"`
	Service      decimal.Decimal `json:"service"`
	Tax          decimal.Decimal `json:"tax"`
	Total        decimal.Decimal `json:"total"`
	ChargeCodeID int64           `json:"charge_code_id"`
}

// PostResult is what posting a session did.
type PostResult struct {
	Posting Posting         `json:"posting"`
	Charges []PostedCharge  `json:"charges"`
	Total   decimal.Decimal `json:"total"`
}

// Unposted is a session of a definite event, dated the business date or earlier, that has not been posted (36 4.10).
type Unposted struct {
	SessionID     int64      `json:"session_id"`
	EventID       int64      `json:"event_id"`
	EventNumber   string     `json:"event_number"`
	EventName     string     `json:"event_name"`
	SessionNo     int        `json:"session_no"`
	SessionName   string     `json:"session_name"`
	SessionDate   civil.Date `json:"session_date"`
	RoomName      *string    `json:"room_name"`
	PaxEstimated  int        `json:"pax_estimated"`
	PaxGuaranteed *int       `json:"pax_guaranteed"`
	PaxActual     *int       `json:"pax_actual"`
	PostingMode   string     `json:"posting_mode"`
	SalesName     string     `json:"sales_name"`
	DaysLate      int        `json:"days_late"`
	Blocked       bool       `json:"blocked"`
	HasLines      bool       `json:"has_lines"`
	// InactiveCode is a charge code a line of the session points at that is inactive: the close does not post the session until the code is active again or the line is changed.
	InactiveCode *string `json:"inactive_code"`
}

// PolicyTemplate is a cancellation policy a property offers to its events, with the deposits it asks for.
type PolicyTemplate struct {
	ID        int64             `json:"id"`
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	IsDefault bool              `json:"is_default"`
	Base      string            `json:"base"`
	IsActive  bool              `json:"is_active"`
	Deposits  []TemplateDeposit `json:"deposits"`
	Tiers     []Tier            `json:"tiers"`
}

// TemplateDeposit is a deposit a template asks for: a percent of the contract total, due so many days before the first session (nothing: at the signature, the confirmation).
type TemplateDeposit struct {
	Seq        int             `json:"seq"`
	Label      string          `json:"label"`
	DaysBefore *int            `json:"days_before"`
	Percent    decimal.Decimal `json:"percent"`
}

// PolicyTemplateInput creates or changes a template; the deposits and the tiers replace the ones it has.
type PolicyTemplateInput struct {
	Code      string            `json:"code"`
	Name      string            `json:"name"`
	IsDefault bool              `json:"is_default"`
	Base      string            `json:"base"`
	IsActive  *bool             `json:"is_active"`
	Deposits  []TemplateDeposit `json:"deposits"`
	Tiers     []TierInput       `json:"tiers"`
}

// ConfirmInput confirms a tentative event: the policy template to copy (the default one when none is named, none at all when the property has no default).
type ConfirmInput struct {
	PolicyTemplateID *int64 `json:"policy_template_id"`
}

// CancelQuote is what cancelling an event would cost today: the tier in force, the fee, the deposits held and what would be applied and left (36 4.7).
type CancelQuote struct {
	Status        string          `json:"status"`
	Base          string          `json:"base"`
	BaseAmount    decimal.Decimal `json:"base_amount"`
	DaysBefore    int             `json:"days_before"`
	Tier          *Tier           `json:"tier"`
	Fee           decimal.Decimal `json:"fee"`
	Action        string          `json:"action"`
	DepositsHeld  decimal.Decimal `json:"deposits_held"`
	Applied       decimal.Decimal `json:"applied"`
	Refundable    decimal.Decimal `json:"refundable"`
	StillOwed     decimal.Decimal `json:"still_owed"`
	FolioID       *int64          `json:"folio_id"`
	FeeItemID     *int64          `json:"fee_item_id"`
	PolicyDefined bool            `json:"policy_defined"`
}

// CancelResult is the event after the close and the money it posted.
type CancelResult struct {
	Event      Event           `json:"event"`
	Quote      CancelQuote     `json:"quote"`
	Posted     decimal.Decimal `json:"posted"`
	FolioItem  *int64          `json:"folio_item_id"`
	Refundable decimal.Decimal `json:"refundable"`
	FolioID    *int64          `json:"folio_id"`
}

// TransferInput hands the bill of an event to the master folio of a group. GroupID names the group (the linked one when the event has one); the items or the amount are as in the common
// transfer, a reason is required, and the approval is the one of folio.reverse.
type TransferInput struct {
	GroupID      int64              `json:"group_id"`
	Mode         string             `json:"mode"` // ITEMS or BALANCE
	ItemIDs      []int64            `json:"item_ids"`
	Amount       string             `json:"amount"`
	Reason       string             `json:"reason"`
	Approval     *iam.ApprovalInput `json:"approval"`
	ChargeCodeID *int64             `json:"charge_code_id"`
}
