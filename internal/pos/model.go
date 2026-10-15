// Package pos holds the point of sale of the outlets of a property (design: docs/architecture/34-pos.md). Stage 1 is the master data: outlets, stations and printers, tax and service
// profiles, price lists, categories, products with their variants and add-ons, "sold out today", the floor plan. The lock of a folio for outlet charges lives with the folios
// (internal/folios/outletlock.go) because the front desk, the check-in and the check-out use it. Nothing here writes the ledger.
package pos

import (
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

const (
	maxName    = 100
	maxShort   = 24
	maxSort    = 100000
	maxOptions = 60
	maxGroups  = 12
	maxTables  = 300
	maxMarks   = 100
)

var (
	codePattern       = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]{0,19}$`)
	outletCodePattern = regexp.MustCompile(`^[A-Z]{1,6}$`)
	reservedPrefixes  = map[string]bool{"FOL": true, "PAY": true, "RSV": true, "STY": true, "SHF": true, "TRF": true, "CNF": true}

	outletTypes = map[string]bool{"RESTAURANT": true, "BAR": true, "CAFE": true, "ROOM_SERVICE": true, "SPA": true, "LAUNDRY": true, "BUSINESS_CENTER": true, "MINIBAR": true, "OTHER": true}
	orderModes  = map[string]bool{"PRINT": true, "KDS": true, "DIRECT": true}
	compModes   = map[string]bool{"PAYMENT_COMP": true, "DISCOUNT_100": true}
	printerKind = map[string]bool{"RECEIPT": true, "KITCHEN": true, "LABEL": true}
	tableShapes = map[string]bool{"SQUARE": true, "RECT": true, "ROUND": true}
	markKinds   = map[string]bool{"WINDOW": true, "DOOR": true, "CASHIER": true, "OTHER": true}
)

func normalCode(code string) string { return strings.ToUpper(strings.TrimSpace(code)) }

// Outlet is a place that sells.
type Outlet struct {
	ID                           int64           `json:"id"`
	Code                         string          `json:"code"`
	Name                         string          `json:"name"`
	OutletType                   string          `json:"outlet_type"`
	OrderMode                    string          `json:"order_mode"`
	DepartmentID                 int64           `json:"department_id"`
	DepartmentName               string          `json:"department_name"`
	RuleProfileID                int64           `json:"rule_profile_id"`
	RuleProfileName              string          `json:"rule_profile_name"`
	PriceListID                  int64           `json:"price_list_id"`
	PriceListName                string          `json:"price_list_name"`
	Drawer                       *string         `json:"drawer"`
	ReceiptPrinterID             *int64          `json:"receipt_printer_id"`
	ReceiptPrinterName           *string         `json:"receipt_printer_name"`
	UsesTables                   bool            `json:"uses_tables"`
	GuestCountRequired           bool            `json:"guest_count_required"`
	RoundingUnit                 int             `json:"rounding_unit"`
	AllowRoomCharge              bool            `json:"allow_room_charge"`
	AllowCompanyCharge           bool            `json:"allow_company_charge"`
	AllowComp                    bool            `json:"allow_comp"`
	CompTreatment                string          `json:"comp_treatment"`
	MaxDiscountPctNoApproval     decimal.Decimal `json:"max_discount_pct_no_approval"`
	BlockNightAuditWithOpenBills bool            `json:"block_night_audit_with_open_bills"`
	IsActive                     bool            `json:"is_active"`
	SortOrder                    int             `json:"sort_order"`
	Version                      int32           `json:"version"`
}

// OutletInput creates or replaces an outlet (the whole form is sent). The code never changes.
type OutletInput struct {
	Code                         string          `json:"code"`
	Name                         string          `json:"name"`
	OutletType                   string          `json:"outlet_type"`
	OrderMode                    string          `json:"order_mode"`
	DepartmentID                 int64           `json:"department_id"`
	RuleProfileID                int64           `json:"rule_profile_id"`
	PriceListID                  int64           `json:"price_list_id"`
	Drawer                       *string         `json:"drawer"`
	ReceiptPrinterID             *int64          `json:"receipt_printer_id"`
	UsesTables                   bool            `json:"uses_tables"`
	GuestCountRequired           bool            `json:"guest_count_required"`
	RoundingUnit                 int             `json:"rounding_unit"`
	AllowRoomCharge              bool            `json:"allow_room_charge"`
	AllowCompanyCharge           bool            `json:"allow_company_charge"`
	AllowComp                    bool            `json:"allow_comp"`
	CompTreatment                string          `json:"comp_treatment"`
	MaxDiscountPctNoApproval     decimal.Decimal `json:"max_discount_pct_no_approval"`
	BlockNightAuditWithOpenBills bool            `json:"block_night_audit_with_open_bills"`
	IsActive                     *bool           `json:"is_active"`
	SortOrder                    int             `json:"sort_order"`
	Version                      int32           `json:"version"`
}

// Printer is a printer of the property; the agent reaches it (stage 3), the master only records it.
type Printer struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	ConnectionInfo string `json:"connection_info"`
	PaperWidthMM   int    `json:"paper_width_mm"`
	Codepage       string `json:"codepage"`
	OpensDrawer    bool   `json:"opens_drawer"`
	IsActive       bool   `json:"is_active"`
}

// PrinterInput creates or replaces a printer.
type PrinterInput struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	ConnectionInfo string `json:"connection_info"`
	PaperWidthMM   int    `json:"paper_width_mm"`
	Codepage       string `json:"codepage"`
	OpensDrawer    bool   `json:"opens_drawer"`
	IsActive       *bool  `json:"is_active"`
}

// Station is a place of production: a kitchen, a bar, a pastry.
type Station struct {
	ID               int64   `json:"id"`
	Code             string  `json:"code"`
	Name             string  `json:"name"`
	PrinterID        *int64  `json:"printer_id"`
	PrinterName      *string `json:"printer_name"`
	KDSEnabled       bool    `json:"kds_enabled"`
	LateAfterMinutes int     `json:"late_after_minutes"`
	IsActive         bool    `json:"is_active"`
}

// StationInput creates or replaces a station. The code never changes.
type StationInput struct {
	Code             string `json:"code"`
	Name             string `json:"name"`
	PrinterID        *int64 `json:"printer_id"`
	KDSEnabled       bool   `json:"kds_enabled"`
	LateAfterMinutes int    `json:"late_after_minutes"`
	IsActive         *bool  `json:"is_active"`
}

// RuleRef is a tax or a service charge of a profile, with its rate as the configuration has it now.
type RuleRef struct {
	RuleType        string          `json:"rule_type"` // TAX or SERVICE
	TaxID           *int64          `json:"tax_id"`
	ServiceChargeID *int64          `json:"service_charge_id"`
	Code            string          `json:"code"`
	Name            string          `json:"name"`
	Rate            decimal.Decimal `json:"rate"`
	OnService       *bool           `json:"tax_on_service"`
	Sequence        int             `json:"sequence"`
	IsActive        bool            `json:"is_active"`
}

// RuleProfile is the tax and service of an outlet or of a product.
type RuleProfile struct {
	ID        int64     `json:"id"`
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	PriceMode string    `json:"price_mode"`
	IsActive  bool      `json:"is_active"`
	Rules     []RuleRef `json:"rules"`
}

// RuleInput names a rule of a profile: one tax or one service charge.
type RuleInput struct {
	TaxID           *int64 `json:"tax_id"`
	ServiceChargeID *int64 `json:"service_charge_id"`
}

// RuleProfileInput creates or replaces a profile; the order of the rules is the order of calculation and of display.
type RuleProfileInput struct {
	Code      string      `json:"code"`
	Name      string      `json:"name"`
	PriceMode string      `json:"price_mode"`
	IsActive  *bool       `json:"is_active"`
	Rules     []RuleInput `json:"rules"`
}

// PriceList is a list of prices that outlets use.
type PriceList struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	IsActive  bool   `json:"is_active"`
	ItemCount int64  `json:"item_count"`
}

// PriceListInput creates or replaces a price list.
type PriceListInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// PriceItem is the price of a product in a price list.
type PriceItem struct {
	PriceListID int64           `json:"price_list_id"`
	ProductID   int64           `json:"product_id"`
	Price       decimal.Decimal `json:"price"`
	IsSold      bool            `json:"is_sold"`
	StationID   *int64          `json:"station_id"`
}

// PriceItemInput sets the price of a product in a list.
type PriceItemInput struct {
	ProductID int64           `json:"product_id"`
	Price     decimal.Decimal `json:"price"`
	IsSold    bool            `json:"is_sold"`
	StationID *int64          `json:"station_id"`
}

// Category groups products on the screen.
type Category struct {
	ID           int64  `json:"id"`
	Code         string `json:"code"`
	Name         string `json:"name"`
	ParentID     *int64 `json:"parent_id"`
	SortOrder    int    `json:"sort_order"`
	IsActive     bool   `json:"is_active"`
	ProductCount int64  `json:"product_count"`
}

// CategoryInput creates or replaces a category. The code and the parent never change.
type CategoryInput struct {
	Code      string `json:"code"`
	Name      string `json:"name"`
	ParentID  *int64 `json:"parent_id"`
	SortOrder int    `json:"sort_order"`
	IsActive  *bool  `json:"is_active"`
}

// Option is a choice of a group: a variant (a price difference, pick one) or an add-on (priced or not, with a limit).
type Option struct {
	ID         int64           `json:"id,omitempty"`
	Name       string          `json:"name"`
	PriceDelta decimal.Decimal `json:"price_delta"`
	IsDefault  bool            `json:"is_default"`
	MaxQty     int             `json:"max_qty"`
	SortOrder  int             `json:"sort_order"`
	IsActive   bool            `json:"is_active"`
}

// OptionGroup is a group of choices of a product: kind VARIANT (pick exactly one; required or optional) or MODIFIER (min and max how many).
type OptionGroup struct {
	ID        int64    `json:"id,omitempty"`
	Name      string   `json:"name"`
	Kind      string   `json:"kind"`
	MinSelect int      `json:"min_select"`
	MaxSelect int      `json:"max_select"`
	SortOrder int      `json:"sort_order"`
	Options   []Option `json:"options"`
}

// Product is something an outlet sells.
type Product struct {
	ID            int64         `json:"id"`
	Code          string        `json:"code"`
	Name          string        `json:"name"`
	ShortName     *string       `json:"short_name"`
	CategoryID    int64         `json:"category_id"`
	CategoryName  string        `json:"category_name"`
	ChargeCodeID  int64         `json:"charge_code_id"`
	ChargeCode    string        `json:"charge_code"`
	ChargeName    string        `json:"charge_code_name"`
	RuleProfileID *int64        `json:"rule_profile_id"`
	StationID     *int64        `json:"station_id"`
	IsActive      bool          `json:"is_active"`
	Version       int32         `json:"version"`
	OptionGroups  []OptionGroup `json:"option_groups,omitempty"`
	Prices        []PriceItem   `json:"prices,omitempty"`
	// Price and IsSold are those of the price list of the outlet asked for in the list.
	Price  *decimal.Decimal `json:"price,omitempty"`
	IsSold *bool            `json:"is_sold,omitempty"`
}

// ProductInput creates or replaces a product with its option groups and its prices in the price lists (the whole form is sent).
type ProductInput struct {
	Code          string           `json:"code"`
	Name          string           `json:"name"`
	ShortName     *string          `json:"short_name"`
	CategoryID    int64            `json:"category_id"`
	ChargeCodeID  int64            `json:"charge_code_id"`
	RuleProfileID *int64           `json:"rule_profile_id"`
	StationID     *int64           `json:"station_id"`
	IsActive      *bool            `json:"is_active"`
	Version       int32            `json:"version"`
	OptionGroups  []OptionGroup    `json:"option_groups"`
	Prices        []ProductPriceIn `json:"prices"`
}

// ProductPriceIn is the price of the product in one price list.
type ProductPriceIn struct {
	PriceListID int64           `json:"price_list_id"`
	Price       decimal.Decimal `json:"price"`
	IsSold      bool            `json:"is_sold"`
	StationID   *int64          `json:"station_id"`
}

// Table is a table of the floor plan; x, y, w and h are in grid units of its area.
type Table struct {
	ID       int64  `json:"id,omitempty"`
	AreaID   int64  `json:"area_id,omitempty"`
	Code     string `json:"code"`
	Capacity int    `json:"capacity"`
	Shape    string `json:"shape"`
	X        int    `json:"x"`
	Y        int    `json:"y"`
	W        int    `json:"w"`
	H        int    `json:"h"`
	IsActive bool   `json:"is_active"`
}

// Mark is something drawn on the plan with no behaviour: a window, a door, the cashier desk.
type Mark struct {
	ID    int64  `json:"id,omitempty"`
	Kind  string `json:"kind"`
	Label string `json:"label"`
	X     int    `json:"x"`
	Y     int    `json:"y"`
	W     int    `json:"w"`
	H     int    `json:"h"`
}

// Area is a part of the floor of an outlet (indoors, terrace, bar) with its tables and marks.
type Area struct {
	ID            int64   `json:"id"`
	OutletID      int64   `json:"outlet_id"`
	Name          string  `json:"name"`
	SortOrder     int     `json:"sort_order"`
	GridW         int     `json:"grid_w"`
	GridH         int     `json:"grid_h"`
	LayoutVersion int32   `json:"layout_version"`
	IsActive      bool    `json:"is_active"`
	Tables        []Table `json:"tables"`
	Marks         []Mark  `json:"marks"`
}

// AreaInput creates an area.
type AreaInput struct {
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	GridW     int    `json:"grid_w"`
	GridH     int    `json:"grid_h"`
}

// LayoutInput saves a whole area at once: its name and size, its tables and its marks. A table that is not in the list is switched off (it may be used by a bill later), not deleted.
type LayoutInput struct {
	Name          string  `json:"name"`
	SortOrder     int     `json:"sort_order"`
	GridW         int     `json:"grid_w"`
	GridH         int     `json:"grid_h"`
	IsActive      *bool   `json:"is_active"`
	LayoutVersion int32   `json:"layout_version"`
	Tables        []Table `json:"tables"`
	Marks         []Mark  `json:"marks"`
}

// SetupIssue is one thing the configuration check found.
type SetupIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // ERROR or WARNING
	Outlet   string `json:"outlet,omitempty"`
	Product  string `json:"product,omitempty"`
	Station  string `json:"station,omitempty"`
}

// ImportRowError is one row of a CSV import that was not accepted.
type ImportRowError struct {
	Row    int    `json:"row"`
	Field  string `json:"field"`
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// ImportResult is the answer of a CSV import: nothing is written when there are errors (all or nothing) or when it is a dry run.
type ImportResult struct {
	DryRun  bool             `json:"dry_run"`
	Rows    int              `json:"rows"`
	Created int              `json:"created"`
	Updated int              `json:"updated"`
	Errors  []ImportRowError `json:"errors"`
}
