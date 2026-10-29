// Package assets is the fixed asset register of the property: its categories and their accounts, its locations, the register of assets with the ledger that records every change of cost and
// of accumulated depreciation, the opening balances and the QR labels (docs/architecture/37-fixed-assets.md, PR 1). The ledger and the state of an asset are written by one piece of code
// only, ledger.go (ledgerpath_guard_test.go holds it).
package assets

import (
	"regexp"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/civil"
)

// Kinds of a category (fa_categories.kind).
const (
	KindLand     = "LAND"
	KindBuilding = "BUILDING"
	KindFFE      = "FFE"
	KindOE       = "OE"
	KindVehicle  = "VEHICLE"
	KindIT       = "IT"
	KindCIP      = "CIP"
	KindLicense  = "LICENSE"
	KindOther    = "OTHER"
)

var categoryKinds = map[string]bool{KindLand: true, KindBuilding: true, KindFFE: true, KindOE: true, KindVehicle: true, KindIT: true, KindCIP: true, KindLicense: true, KindOther: true}

// Methods of depreciation, start rules, policies for the operating equipment.
const (
	MethodSL = "SL"
	MethodDB = "DB"

	StartInServiceMonth = "IN_SERVICE_MONTH"
	StartNextMonth      = "NEXT_MONTH"

	OEExpense = "EXPENSE"
	OEBatch   = "BATCH"
)

// Fiscal groups (UU PPh article 11), and NONE for what is not depreciated for tax.
const (
	FiscalNone           = "NONE"
	FiscalBldPermanent   = "BLD_PERMANENT"
	FiscalBldNonPerm     = "BLD_NON_PERMANENT"
	statusActive         = "ACTIVE"
	statusCancelled      = "CANCELLED"
	sourceOpening        = "OPENING"
	ModeWithJournal      = "WITH_JOURNAL"
	ModeRegisterOnly     = "REGISTER_ONLY"
	sourceTypeOpening    = "FA_OPENING"
	sourceTypeCancel     = "FA_CANCEL"
	sourceTypeLine       = "FA_LINE"
	sourceTypeDepr       = "FA_DEPR"
	sourceJournal        = "JOURNAL"
	LedgerAcquire        = "ACQUIRE"
	LedgerDepreciate     = "DEPRECIATE"
	sourceTypeRebuild    = "FA_REBUILD"
	LedgerOpen           = "OPEN"
	LedgerReversal       = "REVERSAL"
	LedgerCapitalize     = "CAPITALIZE"
	LedgerReclassOut     = "RECLASS_OUT"
	LedgerReclassIn      = "RECLASS_IN"
	LedgerDispose        = "DISPOSE"
	sourceTypeDisposal   = "FA_DISPOSAL"
	sourceTypeRevision   = "FA_REVISION"
	linkUnit             = "UNIT"
	linkCapitalize       = "CAPITALIZE"
	journalSourceAsset   = "ASSET"
	maxName              = 150
	maxPageRow           = 200
	maxOpeningRows       = 2000
	maxCSVBytes          = 4 << 20
	equityAccountCode    = "3900"
	labelAssetPrefix     = "FA:"
	labelLocationPrefix  = "FL:"
	labelCodeLength      = 12
	defaultOEMonths      = 24
	maxLifeMonths        = 1200
	moneyLimitExclusive  = 1_000_000_000_000
	maxNotes             = 500
	maxSerial            = 100
	maxLocationCodeChars = 30
)

var fiscalGroups = map[string]bool{FiscalNone: true, "G1": true, "G2": true, "G3": true, "G4": true, FiscalBldPermanent: true, FiscalBldNonPerm: true}

func isBuildingGroup(g string) bool { return g == FiscalBldPermanent || g == FiscalBldNonPerm }

var (
	categoryCodePattern = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)
	locationCodePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,29}$`)
)

// ---------------------------------------------------------------------------------------------------------------------
// Months. A book month is the first day of a calendar month.

// MonthStart is the first day of the month of d.
func MonthStart(d civil.Date) civil.Date { return civil.NewDate(d.Year(), d.Month(), 1) }

// MonthEnd is the last day of the month of d.
func MonthEnd(d civil.Date) civil.Date { return civil.NewDate(d.Year(), d.Month()+1, 0) }

// AddMonths moves a book month (the first of a month) by n months.
func AddMonths(d civil.Date, n int) civil.Date {
	return civil.NewDate(d.Year(), d.Month()+time.Month(n), 1)
}

// MonthsBetween counts the months from a (inclusive) to b (inclusive) of two book months: 1 when they are the same month, 0 when b is before a.
func MonthsBetween(a, b civil.Date) int {
	n := (b.Year()-a.Year())*12 + int(b.Month()) - int(a.Month()) + 1
	if n < 0 {
		return 0
	}
	return n
}

func isMonthEnd(d civil.Date) bool { return d.Equal(MonthEnd(d)) }

func trimmed(s string) string { return strings.TrimSpace(s) }

func optional(s *string) *string {
	if s == nil {
		return nil
	}
	t := trimmed(*s)
	if t == "" {
		return nil
	}
	return &t
}

// ---------------------------------------------------------------------------------------------------------------------
// The views of the API.

// Settings are the settings of the register of a property.
type Settings struct {
	RegisterFrom            *civil.Date     `json:"register_from"`
	CapitalizationThreshold decimal.Decimal `json:"capitalization_threshold"`
	StartRule               string          `json:"start_rule"`
	OEPolicy                string          `json:"oe_policy"`
	OEDefaultMonths         int             `json:"oe_default_months"`
	OEPolicyChangedOn       *civil.Date     `json:"oe_policy_changed_on"`
	TaxYearStartMonth       *int            `json:"tax_year_start_month"`
	Started                 bool            `json:"started"`
	Version                 int32           `json:"version"`
}

// SettingsInput replaces the settings; Version is the version read.
type SettingsInput struct {
	RegisterFrom            *civil.Date     `json:"register_from"`
	CapitalizationThreshold decimal.Decimal `json:"capitalization_threshold"`
	StartRule               string          `json:"start_rule"`
	OEPolicy                string          `json:"oe_policy"`
	OEDefaultMonths         int             `json:"oe_default_months"`
	TaxYearStartMonth       *int            `json:"tax_year_start_month"`
	Version                 int32           `json:"version"`
}

// FiscalGroup is a group of the income tax law with its rates (percent a year).
type FiscalGroup struct {
	Code      string           `json:"code"`
	LifeYears int              `json:"life_years"`
	SLRate    decimal.Decimal  `json:"sl_rate"`
	DBRate    *decimal.Decimal `json:"db_rate"`
}

// FiscalGroupInput corrects the rates of one group.
type FiscalGroupInput struct {
	LifeYears int              `json:"life_years"`
	SLRate    decimal.Decimal  `json:"sl_rate"`
	DBRate    *decimal.Decimal `json:"db_rate"`
}

// Category is a category of assets with its accounts and defaults.
type Category struct {
	ID                     int64            `json:"id"`
	Code                   string           `json:"code"`
	Name                   string           `json:"name"`
	Kind                   string           `json:"kind"`
	CostAccountID          int64            `json:"cost_account_id"`
	CostAccountCode        string           `json:"cost_account_code"`
	CostAccountName        string           `json:"cost_account_name"`
	AccumulatedAccountID   *int64           `json:"accumulated_account_id"`
	AccumulatedAccountCode *string          `json:"accumulated_account_code"`
	AccumulatedAccountName *string          `json:"accumulated_account_name"`
	ExpenseAccountID       *int64           `json:"expense_account_id"`
	ExpenseAccountCode     *string          `json:"expense_account_code"`
	ExpenseAccountName     *string          `json:"expense_account_name"`
	DepartmentID           *int64           `json:"department_id"`
	DepartmentCode         *string          `json:"department_code"`
	DepartmentName         *string          `json:"department_name"`
	Method                 *string          `json:"method"`
	LifeMonths             *int32           `json:"life_months"`
	ResidualPct            decimal.Decimal  `json:"residual_pct"`
	DBFactor               *decimal.Decimal `json:"db_factor"`
	FiscalGroup            string           `json:"fiscal_group"`
	FiscalMethod           *string          `json:"fiscal_method"`
	IsDepreciable          bool             `json:"is_depreciable"`
	IsActive               bool             `json:"is_active"`
	SortOrder              int32            `json:"sort_order"`
	AssetCount             int              `json:"asset_count"`
	Version                int32            `json:"version"`
}

// CategoryInput creates a category, or (with Version) replaces the editable fields of one. The code never changes.
type CategoryInput struct {
	Code                 string           `json:"code"`
	Name                 string           `json:"name"`
	Kind                 string           `json:"kind"`
	CostAccountID        int64            `json:"cost_account_id"`
	AccumulatedAccountID *int64           `json:"accumulated_account_id"`
	ExpenseAccountID     *int64           `json:"expense_account_id"`
	DepartmentID         *int64           `json:"department_id"`
	Method               *string          `json:"method"`
	LifeMonths           *int32           `json:"life_months"`
	ResidualPct          decimal.Decimal  `json:"residual_pct"`
	DBFactor             *decimal.Decimal `json:"db_factor"`
	FiscalGroup          string           `json:"fiscal_group"`
	FiscalMethod         *string          `json:"fiscal_method"`
	IsActive             *bool            `json:"is_active"`
	SortOrder            int32            `json:"sort_order"`
	Version              int32            `json:"version"`
}

// Location is a place where assets stand: a hotel room, a banquet room, a warehouse or a free area.
type Location struct {
	ID             int64   `json:"id"`
	Code           string  `json:"code"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	RoomID         *int64  `json:"room_id"`
	BanquetRoomID  *int64  `json:"banquet_room_id"`
	WarehouseID    *int64  `json:"warehouse_id"`
	LabelCode      string  `json:"label_code"`
	IsActive       bool    `json:"is_active"`
	AssetCount     int     `json:"asset_count"`
	ReferenceLabel *string `json:"reference_label"`
}

// LocationInput creates a free area.
type LocationInput struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// LocationPatch renames a location or switches it off.
type LocationPatch struct {
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// EnsureLocationInput names a room, a banquet room or a warehouse whose location is wanted (made on first use).
type EnsureLocationInput struct {
	Kind  string `json:"kind"` // ROOM, BANQUET_ROOM or WAREHOUSE
	RefID int64  `json:"ref_id"`
}

// Asset is a row of the register.
type Asset struct {
	ID                      int64            `json:"id"`
	Number                  string           `json:"number"`
	Name                    string           `json:"name"`
	CategoryID              int64            `json:"category_id"`
	CategoryCode            string           `json:"category_code"`
	CategoryName            string           `json:"category_name"`
	CategoryKind            string           `json:"category_kind"`
	LocationID              *int64           `json:"location_id"`
	LocationCode            *string          `json:"location_code"`
	LocationName            *string          `json:"location_name"`
	DepartmentID            *int64           `json:"department_id"`
	DepartmentCode          *string          `json:"department_code"`
	DepartmentName          *string          `json:"department_name"`
	SupplierID              *int64           `json:"supplier_id"`
	SupplierName            *string          `json:"supplier_name"`
	Source                  string           `json:"source"`
	OpeningImportID         *int64           `json:"opening_import_id"`
	Status                  string           `json:"status"`
	AcquisitionDate         civil.Date       `json:"acquisition_date"`
	InServiceDate           civil.Date       `json:"in_service_date"`
	StartRule               string           `json:"start_rule"`
	FirstDepMonth           *civil.Date      `json:"first_dep_month"`
	Cost                    decimal.Decimal  `json:"cost"`
	ResidualValue           decimal.Decimal  `json:"residual_value"`
	AccumDepr               decimal.Decimal  `json:"accum_depr"`
	CarryingValue           decimal.Decimal  `json:"carrying_value"`
	LifeMonths              *int32           `json:"life_months"`
	MonthsDone              int32            `json:"dep_months_done"`
	RemainingMonths         *int32           `json:"remaining_months"`
	LastDepMonth            *civil.Date      `json:"last_dep_month"`
	Method                  *string          `json:"method"`
	DBFactor                *decimal.Decimal `json:"db_factor"`
	FullyDepreciated        bool             `json:"fully_depreciated"`
	UnitCount               int32            `json:"unit_count"`
	SerialNo                *string          `json:"serial_no"`
	WarrantyUntil           *civil.Date      `json:"warranty_until"`
	Notes                   *string          `json:"notes"`
	FiscalGroup             string           `json:"fiscal_group"`
	FiscalMethod            *string          `json:"fiscal_method"`
	FiscalAccumOpening      *decimal.Decimal `json:"fiscal_accum_opening"`
	FiscalMonthsDoneOpening *int32           `json:"fiscal_months_done_opening"`
	FiscalOpeningAsOf       *civil.Date      `json:"fiscal_opening_as_of"`
	FiscalOpeningEstimated  bool             `json:"fiscal_opening_estimated"`
	DisposalPending         bool             `json:"disposal_pending"`
	Version                 int32            `json:"version"`
}

// LedgerEntry is a row of the register ledger of an asset.
type LedgerEntry struct {
	ID          int64           `json:"id"`
	Kind        string          `json:"kind"`
	EventDate   civil.Date      `json:"event_date"`
	BookMonth   *civil.Date     `json:"book_month"`
	CostDelta   decimal.Decimal `json:"cost_delta"`
	AccumDelta  decimal.Decimal `json:"accum_delta"`
	MonthsDelta int32           `json:"months_delta"`
	JournalID   *int64          `json:"journal_id"`
	SourceType  string          `json:"source_type"`
	SourceID    int64           `json:"source_id"`
	ReversesID  *int64          `json:"reverses_id"`
	CreatedAt   time.Time       `json:"created_at"`
}

// AssetDetail is one asset with its ledger.
type AssetDetail struct {
	Asset
	OpeningImportNumber *string          `json:"opening_import_number"`
	SourceLink          *AssetSourceLink `json:"source_link"`
	Ledger              []LedgerEntry    `json:"ledger"`
}

// AssetFilter narrows the register.
type AssetFilter struct {
	CategoryID       *int64
	LocationID       *int64
	DepartmentID     *int64
	Status           string
	Q                string
	WarrantyBefore   *civil.Date
	FullyDepreciated *bool
	Limit            int
	Offset           int
}

// AssetSummary adds up the assets of a filter.
type AssetSummary struct {
	Count         int             `json:"count"`
	Cost          decimal.Decimal `json:"cost"`
	AccumDepr     decimal.Decimal `json:"accum_depr"`
	CarryingValue decimal.Decimal `json:"carrying_value"`
}

// AssetPage is a page of the register with the summary of the whole filter.
type AssetPage struct {
	Data    []Asset      `json:"data"`
	Total   int          `json:"total"`
	Summary AssetSummary `json:"summary"`
}

// AssetPatch changes the data of an asset that is not money.
type AssetPatch struct {
	InServiceDate *civil.Date `json:"in_service_date"` // free until the asset has been depreciated (the start rule too)
	StartRule     *string     `json:"start_rule"`
	Name          string      `json:"name"`
	LocationID    *int64      `json:"location_id"`
	DepartmentID  *int64      `json:"department_id"`
	SupplierID    *int64      `json:"supplier_id"`
	SerialNo      *string     `json:"serial_no"`
	WarrantyUntil *civil.Date `json:"warranty_until"`
	Notes         *string     `json:"notes"`
	Version       int32       `json:"version"`
}

// CancelInput cancels an asset.
type CancelInput struct {
	Reason string `json:"reason"`
}

// LookupAsset is the short form of an asset for a picker: no value.
type LookupAsset struct {
	ID           int64   `json:"id"`
	Number       string  `json:"number"`
	Name         string  `json:"name"`
	LocationName *string `json:"location_name"`
}

// AccountOption is an account for a picker of the setup.
type AccountOption struct {
	ID             int64  `json:"id"`
	Code           string `json:"code"`
	Name           string `json:"name"`
	AccountType    string `json:"account_type"`
	StatementGroup string `json:"statement_group,omitempty"`
}

// SetupIssue is one finding of the setup check.
type SetupIssue struct {
	Code     string         `json:"code"`
	Severity string         `json:"severity"` // ERROR or WARNING
	Message  string         `json:"message"`
	Context  map[string]any `json:"context,omitempty"`
}

// Reconciliation compares the register of a category with the general ledger.
type Reconciliation struct {
	CategoryID       int64           `json:"category_id"`
	CategoryCode     string          `json:"category_code"`
	CategoryName     string          `json:"category_name"`
	CostAccountCode  string          `json:"cost_account_code"`
	RegisterCost     decimal.Decimal `json:"register_cost"`
	LedgerCost       decimal.Decimal `json:"ledger_cost"`
	CostDifference   decimal.Decimal `json:"cost_difference"`
	AccumAccountCode *string         `json:"accumulated_account_code"`
	RegisterAccum    decimal.Decimal `json:"register_accum"`
	LedgerAccum      decimal.Decimal `json:"ledger_accum"`
	AccumDifference  decimal.Decimal `json:"accum_difference"`
	// the known causes of each difference, "general ledger less register" (5.3); what nothing explains is OTHER
	CostCauses  []DifferenceCause `json:"cost_causes"`
	AccumCauses []DifferenceCause `json:"accum_causes"`
}

// SetupCheck is the answer of the setup check.
type SetupCheck struct {
	Issues         []SetupIssue     `json:"issues"`
	Reconciliation []Reconciliation `json:"reconciliation"`
	AsOf           civil.Date       `json:"as_of"`
}

// RebuildResult says how many assets the rebuild of the state changed.
type RebuildResult struct {
	Checked  int `json:"checked"`
	Repaired int `json:"repaired"`
}

// AssetSourceLink is where an asset made from a journal line comes from: the journal, its line, and the bill when it is a line of one.
type AssetSourceLink struct {
	JournalLineID int64           `json:"journal_line_id"`
	UnitNo        int32           `json:"unit_no"`
	Units         int32           `json:"units"`
	Cost          decimal.Decimal `json:"cost"`
	JournalID     int64           `json:"journal_id"`
	JournalNumber string          `json:"journal_number"`
	JournalDate   civil.Date      `json:"journal_date"`
	JournalType   string          `json:"journal_type"`
	BillID        *int64          `json:"bill_id"`
	BillNumber    *string         `json:"bill_number"`
	BillLineNo    *int32          `json:"bill_line_no"`
}
