package inventory

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// ---------------------------------------------------------------- settings

// Settings are the settings of the module of a property (35-inventory.md 3.2). A property that has never used the module has the defaults and Exists false.
type Settings struct {
	Exists                bool             `json:"exists"`
	CostingMethod         string           `json:"costing_method"`
	CostingLockedOn       *civil.Date      `json:"costing_locked_on"`
	PriceTolerancePct     decimal.Decimal  `json:"price_tolerance_pct"`
	QtyTolerancePct       decimal.Decimal  `json:"qty_tolerance_pct"`
	OverReceiptPct        decimal.Decimal  `json:"over_receipt_pct"`
	PoRequiresPr          bool             `json:"po_requires_pr"`
	DirectReceiptLimit    *decimal.Decimal `json:"direct_receipt_limit"`
	CountBlocksNightAudit bool             `json:"count_blocks_night_audit"`
	CountBlind            bool             `json:"count_blind"`
	// EngineeringDepartmentID is the department whose manual USE issues are the engineering cost of the daily cost report (12.2); nil: none.
	EngineeringDepartmentID *int64 `json:"engineering_department_id"`
	Version                 int32  `json:"version"`
}

// SettingsInput changes the settings; the costing method is accepted only before the first stock movement.
type SettingsInput struct {
	CostingMethod           string           `json:"costing_method"`
	PriceTolerancePct       decimal.Decimal  `json:"price_tolerance_pct"`
	QtyTolerancePct         decimal.Decimal  `json:"qty_tolerance_pct"`
	OverReceiptPct          decimal.Decimal  `json:"over_receipt_pct"`
	PoRequiresPr            bool             `json:"po_requires_pr"`
	DirectReceiptLimit      *decimal.Decimal `json:"direct_receipt_limit"`
	CountBlocksNightAudit   bool             `json:"count_blocks_night_audit"`
	CountBlind              bool             `json:"count_blind"`
	EngineeringDepartmentID *int64           `json:"engineering_department_id"`
	Version                 int32            `json:"version"`
}

func defaultSettings() Settings {
	return Settings{CostingMethod: MethodAverage, PriceTolerancePct: decimal.RequireFromString("1.00"), OverReceiptPct: decimal.RequireFromString("5.00"),
		QtyTolerancePct: decimal.Zero, PoRequiresPr: true, CountBlocksNightAudit: true, CountBlind: true, Version: 1}
}

func toSettings(r inventorydb.InventorySetting) Settings {
	return Settings{Exists: true, CostingMethod: r.CostingMethod, CostingLockedOn: r.CostingLockedOn, PriceTolerancePct: r.PriceTolerancePct, QtyTolerancePct: r.QtyTolerancePct,
		OverReceiptPct: r.OverReceiptPct, PoRequiresPr: r.PoRequiresPr, DirectReceiptLimit: r.DirectReceiptLimit, CountBlocksNightAudit: r.CountBlocksNightAudit, CountBlind: r.CountBlind, EngineeringDepartmentID: r.EngineeringDepartmentID, Version: r.Version}
}

// GetSettings answers the settings of the property (inventory.view or inventory.manage).
func (s *Service) GetSettings(ctx context.Context, propertyID int64) (Settings, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return Settings{}, err
	}
	r, err := s.q(ctx).GetSettings(ctx, inventorydb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if errors.Is(err, pgx.ErrNoRows) {
		return defaultSettings(), nil
	}
	if err != nil {
		return Settings{}, err
	}
	return toSettings(r), nil
}

// UpdateSettings changes the settings (inventory.manage). The costing method is chosen before the first stock movement and then locked by the database (409 COSTING_METHOD_LOCKED).
func (s *Service) UpdateSettings(ctx context.Context, propertyID int64, in SettingsInput) (Settings, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Settings{}, err
	}
	var f []apperr.FieldError
	if in.CostingMethod != MethodAverage && in.CostingMethod != MethodFIFO {
		f = append(f, fieldErr("costing_method", "INVALID_VALUE", "AVERAGE or FIFO"))
	}
	hundred := decimal.NewFromInt(100)
	for _, c := range []struct {
		name string
		v    decimal.Decimal
	}{{"price_tolerance_pct", in.PriceTolerancePct}, {"qty_tolerance_pct", in.QtyTolerancePct}, {"over_receipt_pct", in.OverReceiptPct}} {
		if c.v.IsNegative() || c.v.GreaterThan(hundred) || !c.v.Equal(c.v.Round(2)) {
			f = append(f, fieldErr(c.name, "OUT_OF_RANGE", "between 0 and 100, two decimals at most"))
		}
	}
	if in.DirectReceiptLimit != nil && in.DirectReceiptLimit.IsNegative() {
		f = append(f, fieldErr("direct_receipt_limit", "OUT_OF_RANGE", "zero or more"))
	}
	if len(f) > 0 {
		return Settings{}, invalid("settings", f...)
	}
	var out Settings
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := q.EnsureSettings(ctx, inventorydb.EnsureSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if cur.Version != in.Version {
			return versionConflict("settings", cur.Version)
		}
		if in.CostingMethod != cur.CostingMethod && cur.CostingLockedOn != nil {
			return apperr.Conflict("COSTING_METHOD_LOCKED", "the costing method cannot change after the first stock movement").WithContext("locked_on", cur.CostingLockedOn.String())
		}
		row, err := q.UpdateSettings(ctx, inventorydb.UpdateSettingsParams{TenantID: p.TenantID, PropertyID: propertyID, CostingMethod: in.CostingMethod, PriceTolerancePct: in.PriceTolerancePct,
			QtyTolerancePct: in.QtyTolerancePct, OverReceiptPct: in.OverReceiptPct, PoRequiresPr: in.PoRequiresPr, DirectReceiptLimit: in.DirectReceiptLimit,
			CountBlocksNightAudit: in.CountBlocksNightAudit, CountBlind: in.CountBlind, EngineeringDepartmentID: in.EngineeringDepartmentID, Version: in.Version, ActorID: p.ActorID()})
		if err != nil {
			return orNotFound(err, versionConflict("settings", cur.Version))
		}
		out = toSettings(row)
		action := "inventory.settings_changed"
		if in.CostingMethod != cur.CostingMethod {
			action = "inventory.costing_method_set"
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, action, "inventory_settings", propertyID, "inventory settings", toSettings(cur), out))
	})
	return out, err
}

// ---------------------------------------------------------------- units

// Unit is a unit of measure (ml, kg, botol, karton).
type Unit struct {
	ID       int64  `json:"id"`
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive bool   `json:"is_active"`
	InUse    bool   `json:"in_use"`
}

// UnitInput creates or changes a unit; the code is fixed once made.
type UnitInput struct {
	Code     string `json:"code"`
	Name     string `json:"name"`
	IsActive *bool  `json:"is_active"`
}

// ListUnits lists the units of the property.
func (s *Service) ListUnits(ctx context.Context, propertyID int64) ([]Unit, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListUnits(ctx, inventorydb.ListUnitsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Unit, 0, len(rows))
	for _, r := range rows {
		out = append(out, Unit{ID: r.ID, Code: r.Code, Name: r.Name, IsActive: r.IsActive, InUse: r.InUse})
	}
	return out, nil
}

// CreateUnit adds a unit (inventory.manage).
func (s *Service) CreateUnit(ctx context.Context, propertyID int64, in UnitInput) (Unit, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Unit{}, err
	}
	in.Code, in.Name = trimmed(in.Code), trimmed(in.Name)
	var f []apperr.FieldError
	if !unitCodePattern.MatchString(in.Code) {
		f = append(f, fieldErr("code", "INVALID_FORMAT", "1-10 characters: letters, digits, . _ -"))
	}
	f = append(f, checkName("name", in.Name, 60)...)
	if len(f) > 0 {
		return Unit{}, invalid("unit", f...)
	}
	var out Unit
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		r, err := s.q(ctx).InsertUnit(ctx, inventorydb.InsertUnitParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = Unit{ID: r.ID, Code: r.Code, Name: r.Name, IsActive: r.IsActive}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.unit_created", "inv_unit", r.ID, r.Code, nil, out))
	})
	return out, err
}

// UpdateUnit renames a unit or switches it off (inventory.manage). A unit in use cannot be switched off.
func (s *Service) UpdateUnit(ctx context.Context, propertyID, id int64, in UnitInput) (Unit, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Unit{}, err
	}
	in.Name = trimmed(in.Name)
	if f := checkName("name", in.Name, 60); len(f) > 0 {
		return Unit{}, invalid("unit", f...)
	}
	var out Unit
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := q.GetUnit(ctx, inventorydb.GetUnitParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("UNIT_NOT_FOUND", "unit"))
		}
		if !active(in.IsActive) && cur.IsActive {
			units, err := q.ListUnits(ctx, inventorydb.ListUnitsParams{TenantID: p.TenantID, PropertyID: propertyID})
			if err != nil {
				return err
			}
			for _, u := range units {
				if u.ID == id && u.InUse {
					return apperr.Conflict("UNIT_IN_USE", "an item uses the unit: it cannot be switched off")
				}
			}
		}
		r, err := q.UpdateUnit(ctx, inventorydb.UpdateUnitParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Name: in.Name, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		out = Unit{ID: r.ID, Code: r.Code, Name: r.Name, IsActive: r.IsActive}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.unit_updated", "inv_unit", r.ID, r.Code, Unit{ID: cur.ID, Code: cur.Code, Name: cur.Name, IsActive: cur.IsActive}, out))
	})
	return out, err
}

// ---------------------------------------------------------------- categories

// Category groups items and decides their accounts (35-inventory.md 6.1).
type Category struct {
	ID                   int64   `json:"id"`
	Code                 string  `json:"code"`
	Name                 string  `json:"name"`
	Kind                 string  `json:"kind"`
	InventoryAccountID   int64   `json:"inventory_account_id"`
	InventoryAccountCode string  `json:"inventory_account_code,omitempty"`
	InventoryAccountName string  `json:"inventory_account_name,omitempty"`
	CogsAccountID        *int64  `json:"cogs_account_id"`
	CogsAccountCode      *string `json:"cogs_account_code,omitempty"`
	IssueAccountID       *int64  `json:"issue_account_id"`
	IssueAccountCode     *string `json:"issue_account_code,omitempty"`
	WasteAccountID       *int64  `json:"waste_account_id"`
	WasteAccountCode     *string `json:"waste_account_code,omitempty"`
	VarianceAccountID    *int64  `json:"variance_account_id"`
	VarianceAccountCode  *string `json:"variance_account_code,omitempty"`
	IsActive             bool    `json:"is_active"`
	SortOrder            int     `json:"sort_order"`
	ItemCount            int     `json:"item_count"`
	Version              int32   `json:"version"`
}

// CategoryInput creates or changes a category.
type CategoryInput struct {
	Code               string `json:"code"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	InventoryAccountID int64  `json:"inventory_account_id"`
	CogsAccountID      *int64 `json:"cogs_account_id"`
	IssueAccountID     *int64 `json:"issue_account_id"`
	WasteAccountID     *int64 `json:"waste_account_id"`
	VarianceAccountID  *int64 `json:"variance_account_id"`
	IsActive           *bool  `json:"is_active"`
	SortOrder          int    `json:"sort_order"`
	Version            int32  `json:"version"`
}

func toCategory(r inventorydb.ListCategoriesRow) Category {
	return Category{ID: r.ID, Code: r.Code, Name: r.Name, Kind: r.Kind, InventoryAccountID: r.InventoryAccountID, InventoryAccountCode: r.InventoryAccountCode, InventoryAccountName: r.InventoryAccountName,
		CogsAccountID: r.CogsAccountID, CogsAccountCode: r.CogsAccountCode, IssueAccountID: r.IssueAccountID, IssueAccountCode: r.IssueAccountCode, WasteAccountID: r.WasteAccountID,
		WasteAccountCode: r.WasteAccountCode, VarianceAccountID: r.VarianceAccountID, VarianceAccountCode: r.VarianceAccountCode, IsActive: r.IsActive, SortOrder: int(r.SortOrder),
		ItemCount: int(r.ItemCount), Version: r.Version}
}

// ListCategories lists the categories with their accounts.
func (s *Service) ListCategories(ctx context.Context, propertyID int64) ([]Category, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListCategories(ctx, inventorydb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Category, 0, len(rows))
	for _, r := range rows {
		out = append(out, toCategory(r))
	}
	return out, nil
}

// InventoryAccount is an account a category may use as its inventory account (an asset of the group INVENTORIES).
type InventoryAccount struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

// ListInventoryAccounts lists the accounts that can be the inventory account of a category.
func (s *Service) ListInventoryAccounts(ctx context.Context, propertyID int64) ([]InventoryAccount, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListInventoryAccounts(ctx, inventorydb.ListInventoryAccountsParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]InventoryAccount, 0, len(rows))
	for _, r := range rows {
		out = append(out, InventoryAccount{ID: r.ID, Code: r.Code, Name: r.Name})
	}
	return out, nil
}

// checkCategory validates the fields of a category against the chart: the inventory account is an active, postable asset of the group INVENTORIES, every cost account an active, postable expense.
func (s *Service) checkCategory(ctx context.Context, p auth.Principal, propertyID int64, in CategoryInput) ([]apperr.FieldError, error) {
	var f []apperr.FieldError
	f = append(f, checkName("name", in.Name, maxName)...)
	if !categoryKinds[in.Kind] {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "FOOD, BEVERAGE, SUPPLIES or OTHER"))
	}
	if in.SortOrder < 0 || in.SortOrder > 100000 {
		f = append(f, fieldErr("sort_order", "OUT_OF_RANGE", "0 to 100000"))
	}
	ids := []int64{in.InventoryAccountID}
	costs := map[string]*int64{"cogs_account_id": in.CogsAccountID, "issue_account_id": in.IssueAccountID, "waste_account_id": in.WasteAccountID, "variance_account_id": in.VarianceAccountID}
	for _, v := range costs {
		if v != nil {
			ids = append(ids, *v)
		}
	}
	rows, err := s.q(ctx).ListAccountsByID(ctx, inventorydb.ListAccountsByIDParams{TenantID: p.TenantID, PropertyID: propertyID, Ids: ids})
	if err != nil {
		return nil, err
	}
	byID := map[int64]inventorydb.ListAccountsByIDRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	switch a, ok := byID[in.InventoryAccountID]; {
	case !ok:
		f = append(f, fieldErr("inventory_account_id", "NOT_FOUND", "no such account in this property"))
	case a.AccountType != "ASSET" || a.StatementGroup != "INVENTORIES":
		f = append(f, fieldErr("inventory_account_id", "WRONG_ACCOUNT_TYPE", "an asset account of the group inventories"))
	case !a.IsPostable || !a.IsActive:
		f = append(f, fieldErr("inventory_account_id", "NOT_POSTABLE", "an active account that takes postings"))
	}
	for _, name := range []string{"cogs_account_id", "issue_account_id", "waste_account_id", "variance_account_id"} {
		v := costs[name]
		if v == nil {
			continue
		}
		switch a, ok := byID[*v]; {
		case !ok:
			f = append(f, fieldErr(name, "NOT_FOUND", "no such account in this property"))
		case a.AccountType != "EXPENSE":
			f = append(f, fieldErr(name, "WRONG_ACCOUNT_TYPE", "an expense account"))
		case !a.IsPostable || !a.IsActive:
			f = append(f, fieldErr(name, "NOT_POSTABLE", "an active account that takes postings"))
		}
	}
	return f, nil
}

// CreateCategory adds a category (inventory.manage).
func (s *Service) CreateCategory(ctx context.Context, propertyID int64, in CategoryInput) (Category, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Category{}, err
	}
	in.Code, in.Name = normalCode(in.Code), trimmed(in.Name)
	var out Category
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		var f []apperr.FieldError
		if !categoryCodePattern.MatchString(in.Code) {
			f = append(f, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, . _ -"))
		}
		more, err := s.checkCategory(ctx, p, propertyID, in)
		if err != nil {
			return err
		}
		if f = append(f, more...); len(f) > 0 {
			return invalid("category", f...)
		}
		r, err := s.q(ctx).InsertCategory(ctx, inventorydb.InsertCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, Kind: in.Kind,
			InventoryAccountID: in.InventoryAccountID, CogsAccountID: in.CogsAccountID, IssueAccountID: in.IssueAccountID, WasteAccountID: in.WasteAccountID, VarianceAccountID: in.VarianceAccountID,
			IsActive: active(in.IsActive), SortOrder: int32(in.SortOrder), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return err
		}
		out = s.categoryByID(ctx, p, propertyID, r.ID)
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.category_created", "inv_category", r.ID, r.Code, nil, out))
	})
	return out, err
}

func (s *Service) categoryByID(ctx context.Context, p auth.Principal, propertyID, id int64) Category {
	rows, err := s.q(ctx).ListCategories(ctx, inventorydb.ListCategoriesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err == nil {
		for _, r := range rows {
			if r.ID == id {
				return toCategory(r)
			}
		}
	}
	return Category{ID: id}
}

// UpdateCategory changes a category (inventory.manage). The account of the inventory and the class of the category cannot change once an item of the category has a movement: the stock
// control of the general ledger sums the ledger by the account of the category.
func (s *Service) UpdateCategory(ctx context.Context, propertyID, id int64, in CategoryInput) (Category, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Category{}, err
	}
	in.Name = trimmed(in.Name)
	var out Category
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := q.GetCategory(ctx, inventorydb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("CATEGORY_NOT_FOUND", "category"))
		}
		if cur.Version != in.Version {
			return versionConflict("category", cur.Version)
		}
		f, err := s.checkCategory(ctx, p, propertyID, in)
		if err != nil {
			return err
		}
		if len(f) > 0 {
			return invalid("category", f...)
		}
		if in.InventoryAccountID != cur.InventoryAccountID || ClassOfKind(in.Kind) != ClassOfKind(cur.Kind) {
			if used, err := s.categoryHasStock(ctx, p.TenantID, propertyID, id); err != nil {
				return err
			} else if used {
				return apperr.Conflict("CATEGORY_HAS_STOCK", "an item of the category has stock movements: its inventory account and its class cannot change")
			}
		}
		r, err := q.UpdateCategory(ctx, inventorydb.UpdateCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, Kind: in.Kind,
			InventoryAccountID: in.InventoryAccountID, CogsAccountID: in.CogsAccountID, IssueAccountID: in.IssueAccountID, WasteAccountID: in.WasteAccountID, VarianceAccountID: in.VarianceAccountID,
			IsActive: active(in.IsActive), SortOrder: int32(in.SortOrder), ActorID: p.ActorID()}) //nolint:gosec // G115: validated
		if err != nil {
			return orNotFound(err, versionConflict("category", cur.Version))
		}
		out = s.categoryByID(ctx, p, propertyID, r.ID)
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.category_updated", "inv_category", r.ID, r.Code, s.categoryByID(ctx, p, propertyID, cur.ID), out))
	})
	return out, err
}

func (s *Service) categoryHasStock(ctx context.Context, tenantID, propertyID, categoryID int64) (bool, error) {
	var used bool
	err := s.txm.DB(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM inv_movements m JOIN inv_items i ON i.property_id = m.property_id AND i.id = m.item_id
	                                                   WHERE m.tenant_id = $1 AND m.property_id = $2 AND i.category_id = $3)`, tenantID, propertyID, categoryID).Scan(&used)
	return used, err
}

// ---------------------------------------------------------------- items

// ItemUnit is a unit of an item with its conversion: 1 of the unit is StockNum / StockDen stock units.
type ItemUnit struct {
	UnitID            int64  `json:"unit_id"`
	UnitCode          string `json:"unit_code,omitempty"`
	StockNum          int32  `json:"stock_num"`
	StockDen          int32  `json:"stock_den"`
	IsPurchase        bool   `json:"is_purchase"`
	IsRecipe          bool   `json:"is_recipe"`
	IsDefaultPurchase bool   `json:"is_default_purchase"`
	IsDefaultRecipe   bool   `json:"is_default_recipe"`
}

// WarehouseStock is the stock of an item in one warehouse.
type WarehouseStock struct {
	WarehouseID   int64            `json:"warehouse_id"`
	WarehouseCode string           `json:"warehouse_code"`
	WarehouseName string           `json:"warehouse_name,omitempty"`
	Qty           decimal.Decimal  `json:"qty"`
	Value         decimal.Decimal  `json:"value"`
	AverageCost   *decimal.Decimal `json:"average_cost"` // value / qty, none when the shelf is empty or below zero
	MinQty        decimal.Decimal  `json:"min_qty"`
}

// Item is something kept in stock.
type Item struct {
	ID                int64            `json:"id"`
	Code              string           `json:"code"`
	Name              string           `json:"name"`
	CategoryID        int64            `json:"category_id"`
	CategoryCode      string           `json:"category_code"`
	CategoryName      string           `json:"category_name"`
	CategoryKind      string           `json:"category_kind"`
	StockUnitID       int64            `json:"stock_unit_id"`
	StockUnitCode     string           `json:"stock_unit_code"`
	ReferenceCost     *decimal.Decimal `json:"reference_cost"`
	DefaultSupplierID *int64           `json:"default_supplier_id"`
	Barcode           *string          `json:"barcode"`
	Note              *string          `json:"note"`
	IsActive          bool             `json:"is_active"`
	Version           int32            `json:"version"`
	QtyOnHand         decimal.Decimal  `json:"qty_on_hand"`
	ValueOnHand       decimal.Decimal  `json:"value_on_hand"`
	BelowMin          bool             `json:"below_min"`
	BelowZero         bool             `json:"below_zero"`
	HasStock          bool             `json:"has_stock"`
	Units             []ItemUnit       `json:"units"`
	Stock             []WarehouseStock `json:"stock"`
}

// ItemFilter selects the items of the list.
type ItemFilter struct {
	Q          string
	CategoryID *int64
	Active     *bool
	BelowMin   bool
	Warehouse  *int64
	Limit      int
	Offset     int
}

// ItemPage is a page of the list of items with the counts the screen shows.
type ItemPage struct {
	Items      []Item           `json:"items"`
	Total      int              `json:"total"`
	TotalValue decimal.Decimal  `json:"total_value"`
	ByCategory map[int64]int    `json:"by_category"`
	Method     string           `json:"costing_method"`
	Locked     bool             `json:"costing_locked"`
	Warehouses []WarehouseBrief `json:"warehouses"`
}

// WarehouseBrief names a warehouse for the columns of the list.
type WarehouseBrief struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	Name string `json:"name"`
}

func averageOf(qty, value decimal.Decimal) *decimal.Decimal {
	if !qty.IsPositive() {
		return nil
	}
	a := value.Div(qty).Round(2)
	return &a
}

// ListItems lists items with the stock of each per warehouse (inventory.view).
func (s *Service) ListItems(ctx context.Context, propertyID int64, f ItemFilter) (ItemPage, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return ItemPage{}, err
	}
	if f.Limit < 1 || f.Limit > maxPageRow {
		f.Limit = 50
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	var qtext *string
	if t := trimmed(f.Q); t != "" {
		qtext = &t
	}
	q := s.q(ctx)
	rows, err := q.ListItems(ctx, inventorydb.ListItemsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: f.Warehouse, CategoryID: f.CategoryID, Active: f.Active, Q: qtext,
		OnlyBelowMin: f.BelowMin, RowLimit: int32(f.Limit), RowOffset: int32(f.Offset)}) //nolint:gosec // G115: bounded above
	if err != nil {
		return ItemPage{}, err
	}
	page := ItemPage{Items: make([]Item, 0, len(rows)), ByCategory: map[int64]int{}}
	ids := make([]int64, 0, len(rows))
	index := map[int64]int{}
	for _, r := range rows {
		it := Item{ID: r.ID, Code: r.Code, Name: r.Name, CategoryID: r.CategoryID, CategoryCode: r.CategoryCode, CategoryName: r.CategoryName, CategoryKind: r.CategoryKind,
			StockUnitID: r.StockUnitID, StockUnitCode: r.StockUnitCode, ReferenceCost: r.ReferenceCost, DefaultSupplierID: r.DefaultSupplierID, Barcode: r.Barcode, Note: r.Note,
			IsActive: r.IsActive, Version: r.Version, QtyOnHand: r.QtyOnHand, ValueOnHand: r.ValueOnHand, BelowMin: r.BelowMin, BelowZero: r.BelowZero,
			Units: []ItemUnit{}, Stock: []WarehouseStock{}}
		index[r.ID] = len(page.Items)
		ids = append(ids, r.ID)
		page.Items = append(page.Items, it)
	}
	stock, err := q.ListStock(ctx, inventorydb.ListStockParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return ItemPage{}, err
	}
	for _, r := range stock {
		i := index[r.ItemID]
		page.Items[i].Stock = append(page.Items[i].Stock, WarehouseStock{WarehouseID: r.WarehouseID, WarehouseCode: r.WarehouseCode, WarehouseName: r.WarehouseName, Qty: r.Qty, Value: r.Value,
			AverageCost: averageOf(r.Qty, r.Value), MinQty: r.MinQty})
	}
	// the units of each item (the stock unit first, as the detail gives them): the forms of the movements choose a unit from the list without reading each item
	us, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return ItemPage{}, err
	}
	for _, u := range us {
		i := index[u.ItemID]
		unit := ItemUnit{UnitID: u.UnitID, UnitCode: u.UnitCode, StockNum: u.StockNum, StockDen: u.StockDen, IsPurchase: u.IsPurchase, IsRecipe: u.IsRecipe, IsDefaultPurchase: u.IsDefaultPurchase, IsDefaultRecipe: u.IsDefaultRecipe}
		if u.UnitID == page.Items[i].StockUnitID {
			page.Items[i].Units = append([]ItemUnit{unit}, page.Items[i].Units...)
		} else {
			page.Items[i].Units = append(page.Items[i].Units, unit)
		}
	}
	if page.Total, err = countInt(q.CountItems(ctx, inventorydb.CountItemsParams{TenantID: p.TenantID, PropertyID: propertyID, CategoryID: f.CategoryID, Active: f.Active, Q: qtext, OnlyBelowMin: f.BelowMin})); err != nil {
		return ItemPage{}, err
	}
	counts, err := q.CountItemsByCategory(ctx, inventorydb.CountItemsByCategoryParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return ItemPage{}, err
	}
	for _, c := range counts {
		page.ByCategory[c.CategoryID] = int(c.N)
	}
	if page.TotalValue, err = q.TotalStockValue(ctx, inventorydb.TotalStockValueParams{TenantID: p.TenantID, PropertyID: propertyID}); err != nil {
		return ItemPage{}, err
	}
	cfg, err := q.GetSettings(ctx, inventorydb.GetSettingsParams{TenantID: p.TenantID, PropertyID: propertyID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		page.Method = MethodAverage
	case err != nil:
		return ItemPage{}, err
	default:
		page.Method, page.Locked = cfg.CostingMethod, cfg.CostingLockedOn != nil
	}
	whs, err := q.ListWarehouses(ctx, inventorydb.ListWarehousesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return ItemPage{}, err
	}
	page.Warehouses = []WarehouseBrief{}
	for _, w := range whs {
		if w.IsActive {
			page.Warehouses = append(page.Warehouses, WarehouseBrief{ID: w.ID, Code: w.Code, Name: w.Name})
		}
	}
	return page, nil
}

func countInt(n int32, err error) (int, error) { return int(n), err }

// GetItem answers one item with its units and its stock per warehouse (inventory.view).
func (s *Service) GetItem(ctx context.Context, propertyID, id int64) (Item, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return Item{}, err
	}
	return s.item(ctx, p, propertyID, id)
}

func (s *Service) item(ctx context.Context, p auth.Principal, propertyID, id int64) (Item, error) {
	q := s.q(ctx)
	r, err := q.GetItem(ctx, inventorydb.GetItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
	if err != nil {
		return Item{}, orNotFound(err, notFound("ITEM_NOT_FOUND", "item"))
	}
	it := Item{ID: r.ID, Code: r.Code, Name: r.Name, CategoryID: r.CategoryID, CategoryCode: r.CategoryCode, CategoryName: r.CategoryName, CategoryKind: r.CategoryKind,
		StockUnitID: r.StockUnitID, StockUnitCode: r.StockUnitCode, ReferenceCost: r.ReferenceCost, DefaultSupplierID: r.DefaultSupplierID, Barcode: r.Barcode, Note: r.Note,
		IsActive: r.IsActive, Version: r.Version, Units: []ItemUnit{}, Stock: []WarehouseStock{}}
	units, err := q.ListItemUnits(ctx, inventorydb.ListItemUnitsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: id, StockUnitID: r.StockUnitID})
	if err != nil {
		return Item{}, err
	}
	for _, u := range units {
		it.Units = append(it.Units, ItemUnit{UnitID: u.UnitID, UnitCode: u.UnitCode, StockNum: u.StockNum, StockDen: u.StockDen, IsPurchase: u.IsPurchase, IsRecipe: u.IsRecipe,
			IsDefaultPurchase: u.IsDefaultPurchase, IsDefaultRecipe: u.IsDefaultRecipe})
	}
	stock, err := q.ListItemStock(ctx, inventorydb.ListItemStockParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: id})
	if err != nil {
		return Item{}, err
	}
	for _, w := range stock {
		it.Stock = append(it.Stock, WarehouseStock{WarehouseID: w.WarehouseID, WarehouseCode: w.WarehouseCode, WarehouseName: w.WarehouseName, Qty: w.Qty, Value: w.Value,
			AverageCost: averageOf(w.Qty, w.Value), MinQty: w.MinQty})
		it.QtyOnHand = it.QtyOnHand.Add(w.Qty)
		it.ValueOnHand = it.ValueOnHand.Add(w.Value)
		if w.Qty.IsNegative() {
			it.BelowZero = true
		}
		if w.MinQty.IsPositive() && w.Qty.LessThan(w.MinQty) {
			it.BelowMin = true
		}
	}
	if it.HasStock, err = itemHasStock(ctx, q, propertyID, id); err != nil {
		return Item{}, err
	}
	return it, nil
}

func itemHasStock(ctx context.Context, q *inventorydb.Queries, propertyID, itemID int64) (bool, error) {
	return q.ItemHasStock(ctx, inventorydb.ItemHasStockParams{PropertyID: propertyID, ItemID: itemID})
}

// ItemInput creates or changes an item.
type ItemInput struct {
	Code              string           `json:"code"`
	Name              string           `json:"name"`
	CategoryID        int64            `json:"category_id"`
	StockUnitID       int64            `json:"stock_unit_id"`
	ReferenceCost     *decimal.Decimal `json:"reference_cost"`
	DefaultSupplierID *int64           `json:"default_supplier_id"`
	Barcode           *string          `json:"barcode"`
	Note              *string          `json:"note"`
	IsActive          *bool            `json:"is_active"`
	Units             []ItemUnit       `json:"units"` // on create: the other units of the item (the stock unit is made by the service)
	Version           int32            `json:"version"`
}

func (s *Service) checkItem(ctx context.Context, p auth.Principal, propertyID int64, in ItemInput, creating bool) ([]apperr.FieldError, error) {
	var f []apperr.FieldError
	if creating && !itemCodePattern.MatchString(in.Code) {
		f = append(f, fieldErr("code", "INVALID_FORMAT", "1-30 characters: A-Z, 0-9, . _ -"))
	}
	f = append(f, checkName("name", in.Name, maxItem)...)
	if in.ReferenceCost != nil && (in.ReferenceCost.IsNegative() || !in.ReferenceCost.Equal(in.ReferenceCost.Round(6))) {
		f = append(f, fieldErr("reference_cost", "INVALID_VALUE", "zero or more, six decimals at most"))
	}
	if in.Barcode != nil && len(*in.Barcode) > 40 {
		f = append(f, fieldErr("barcode", "INVALID_VALUE", "40 characters at most"))
	}
	if in.Note != nil && len([]rune(*in.Note)) > 300 {
		f = append(f, fieldErr("note", "INVALID_VALUE", "300 characters at most"))
	}
	q := s.q(ctx)
	if _, err := q.GetCategory(ctx, inventorydb.GetCategoryParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.CategoryID}); errors.Is(err, pgx.ErrNoRows) {
		f = append(f, fieldErr("category_id", "NOT_FOUND", "no such category in this property"))
	} else if err != nil {
		return nil, err
	}
	if u, err := q.GetUnit(ctx, inventorydb.GetUnitParams{TenantID: p.TenantID, PropertyID: propertyID, ID: in.StockUnitID}); errors.Is(err, pgx.ErrNoRows) {
		f = append(f, fieldErr("stock_unit_id", "NOT_FOUND", "no such unit in this property"))
	} else if err != nil {
		return nil, err
	} else if !u.IsActive && creating {
		f = append(f, fieldErr("stock_unit_id", "INACTIVE", "the unit is switched off"))
	}
	return f, nil
}

// CreateItem adds an item (inventory.manage). The stock unit becomes its unit 1/1; Units lists the others.
func (s *Service) CreateItem(ctx context.Context, propertyID int64, in ItemInput) (Item, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Item{}, err
	}
	in.Code, in.Name, in.Barcode, in.Note = normalCode(in.Code), trimmed(in.Name), optional(in.Barcode), optional(in.Note)
	var id int64
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		f, err := s.checkItem(ctx, p, propertyID, in, true)
		if err != nil {
			return err
		}
		if len(f) > 0 {
			return invalid("item", f...)
		}
		id, err = s.q(ctx).InsertItem(ctx, inventorydb.InsertItemParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, CategoryID: in.CategoryID, StockUnitID: in.StockUnitID,
			ReferenceCost: in.ReferenceCost, DefaultSupplierID: in.DefaultSupplierID, Barcode: in.Barcode, Note: in.Note, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.setUnits(ctx, p, propertyID, id, in.StockUnitID, in.Units); err != nil {
			return err
		}
		it, err := s.item(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.item_created", "inv_item", id, in.Code, nil, it))
	})
	if err != nil {
		return Item{}, err
	}
	return s.item(ctx, p, propertyID, id)
}

// UpdateItem changes an item (inventory.manage). Its code is fixed; an item with stock keeps its stock unit and the account of its category.
func (s *Service) UpdateItem(ctx context.Context, propertyID, id int64, in ItemInput) (Item, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Item{}, err
	}
	in.Name, in.Barcode, in.Note = trimmed(in.Name), optional(in.Barcode), optional(in.Note)
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := s.item(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if cur.Version != in.Version {
			return versionConflict("item", cur.Version)
		}
		f, err := s.checkItem(ctx, p, propertyID, in, false)
		if err != nil {
			return err
		}
		if len(f) > 0 {
			return invalid("item", f...)
		}
		if cur.HasStock {
			if in.StockUnitID != cur.StockUnitID {
				return apperr.Conflict("ITEM_HAS_STOCK", "the item has stock movements: its stock unit cannot change")
			}
			if in.CategoryID != cur.CategoryID {
				return apperr.Conflict("ITEM_HAS_STOCK", "the item has stock movements: it cannot move to another category")
			}
		}
		if _, err := q.UpdateItem(ctx, inventorydb.UpdateItemParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, CategoryID: in.CategoryID,
			StockUnitID: in.StockUnitID, ReferenceCost: in.ReferenceCost, DefaultSupplierID: in.DefaultSupplierID, Barcode: in.Barcode, Note: in.Note, IsActive: active(in.IsActive), ActorID: p.ActorID()}); err != nil {
			return orNotFound(err, versionConflict("item", cur.Version))
		}
		if in.StockUnitID != cur.StockUnitID { // no stock yet: the units are re-made around the new stock unit
			if err := s.setUnits(ctx, p, propertyID, id, in.StockUnitID, nil); err != nil {
				return err
			}
		}
		now, err := s.item(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.item_updated", "inv_item", id, cur.Code, cur, now))
	})
	if err != nil {
		return Item{}, err
	}
	return s.item(ctx, p, propertyID, id)
}

// SetItemUnits replaces the units of an item (inventory.manage): the stock unit is always there with 1/1.
func (s *Service) SetItemUnits(ctx context.Context, propertyID, id int64, units []ItemUnit) (Item, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Item{}, err
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		cur, err := s.item(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		if err := s.setUnits(ctx, p, propertyID, id, cur.StockUnitID, units); err != nil {
			return err
		}
		now, err := s.item(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.item_units_set", "inv_item", id, cur.Code, cur.Units, now.Units))
	})
	if err != nil {
		return Item{}, err
	}
	return s.item(ctx, p, propertyID, id)
}

// setUnits validates and writes the units of an item. The stock unit is 1/1 and is always a unit of the item; its flags may be given in the list, else it is a purchase and a recipe unit.
func (s *Service) setUnits(ctx context.Context, p auth.Principal, propertyID, itemID, stockUnitID int64, in []ItemUnit) error {
	q := s.q(ctx)
	var f []apperr.FieldError
	// the stock unit is a purchase and a recipe unit, and the default of each unless another unit of the list claims the default
	claimed := func(f func(ItemUnit) bool) bool {
		for _, u := range in {
			if u.UnitID != stockUnitID && f(u) {
				return true
			}
		}
		return false
	}
	stock := ItemUnit{UnitID: stockUnitID, StockNum: 1, StockDen: 1, IsPurchase: true, IsRecipe: true,
		IsDefaultPurchase: !claimed(func(u ItemUnit) bool { return u.IsDefaultPurchase }), IsDefaultRecipe: !claimed(func(u ItemUnit) bool { return u.IsDefaultRecipe })}
	seen := map[int64]bool{stockUnitID: true}
	rows := []ItemUnit{}
	for i, u := range in {
		at := fmt.Sprintf("units[%d]", i)
		if u.UnitID == stockUnitID {
			if u.StockNum != 1 || u.StockDen != 1 {
				f = append(f, fieldErr(at+".stock_num", "INVALID_VALUE", "the stock unit is 1 / 1"))
			}
			stock = ItemUnit{UnitID: stockUnitID, StockNum: 1, StockDen: 1, IsPurchase: u.IsPurchase, IsRecipe: u.IsRecipe, IsDefaultPurchase: u.IsDefaultPurchase, IsDefaultRecipe: u.IsDefaultRecipe}
			continue
		}
		if seen[u.UnitID] {
			f = append(f, fieldErr(at+".unit_id", "DUPLICATE", "the unit is listed twice"))
			continue
		}
		seen[u.UnitID] = true
		if u.StockNum < 1 || u.StockNum > maxFactor || u.StockDen < 1 || u.StockDen > maxFactor {
			f = append(f, fieldErr(at+".stock_num", "OUT_OF_RANGE", "a conversion of positive whole numbers up to 1000000"))
		}
		if _, err := q.GetUnit(ctx, inventorydb.GetUnitParams{TenantID: p.TenantID, PropertyID: propertyID, ID: u.UnitID}); errors.Is(err, pgx.ErrNoRows) {
			f = append(f, fieldErr(at+".unit_id", "NOT_FOUND", "no such unit in this property"))
		} else if err != nil {
			return err
		}
		rows = append(rows, u)
	}
	rows = append([]ItemUnit{stock}, rows...)
	var dp, dr int
	for i, u := range rows {
		at := fmt.Sprintf("units[%d]", i)
		if u.IsDefaultPurchase {
			dp++
			if !u.IsPurchase {
				f = append(f, fieldErr(at+".is_default_purchase", "INVALID_VALUE", "a default purchase unit is a purchase unit"))
			}
		}
		if u.IsDefaultRecipe {
			dr++
			if !u.IsRecipe {
				f = append(f, fieldErr(at+".is_default_recipe", "INVALID_VALUE", "a default recipe unit is a recipe unit"))
			}
		}
	}
	if dp > 1 || dr > 1 {
		f = append(f, fieldErr("units", "DUPLICATE", "one default purchase unit and one default recipe unit at most"))
	}
	if len(f) > 0 {
		return invalid("units", f...)
	}
	if err := q.DeleteItemUnits(ctx, inventorydb.DeleteItemUnitsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: itemID}); err != nil {
		return err
	}
	for _, u := range rows {
		if err := q.InsertItemUnit(ctx, inventorydb.InsertItemUnitParams{TenantID: p.TenantID, PropertyID: propertyID, ItemID: itemID, UnitID: u.UnitID, StockNum: u.StockNum, StockDen: u.StockDen,
			IsPurchase: u.IsPurchase, IsRecipe: u.IsRecipe, IsDefaultPurchase: u.IsDefaultPurchase, IsDefaultRecipe: u.IsDefaultRecipe}); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- warehouses and levels

// Warehouse is a place that holds stock.
type Warehouse struct {
	ID             int64           `json:"id"`
	Code           string          `json:"code"`
	Name           string          `json:"name"`
	Kind           string          `json:"kind"`
	OutletID       *int64          `json:"outlet_id"`
	OutletCode     *string         `json:"outlet_code,omitempty"`
	DepartmentID   *int64          `json:"department_id"`
	DepartmentCode *string         `json:"department_code,omitempty"`
	CostClass      string          `json:"cost_class"`
	IsActive       bool            `json:"is_active"`
	Frozen         bool            `json:"frozen"`
	StockValue     decimal.Decimal `json:"stock_value"`
	ItemCount      int             `json:"item_count"`
	Version        int32           `json:"version"`
}

// WarehouseInput creates or changes a warehouse; the code is fixed once made.
type WarehouseInput struct {
	Code         string `json:"code"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	OutletID     *int64 `json:"outlet_id"`
	DepartmentID *int64 `json:"department_id"`
	CostClass    string `json:"cost_class"`
	IsActive     *bool  `json:"is_active"`
	Version      int32  `json:"version"`
}

// ListWarehouses lists the warehouses with the value of their stock.
func (s *Service) ListWarehouses(ctx context.Context, propertyID int64) ([]Warehouse, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListWarehouses(ctx, inventorydb.ListWarehousesParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return nil, err
	}
	out := make([]Warehouse, 0, len(rows))
	for _, r := range rows {
		out = append(out, Warehouse{ID: r.ID, Code: r.Code, Name: r.Name, Kind: r.Kind, OutletID: r.OutletID, OutletCode: r.OutletCode, DepartmentID: r.DepartmentID, DepartmentCode: r.DepartmentCode,
			CostClass: r.CostClass, IsActive: r.IsActive, Frozen: r.CountID != nil, StockValue: r.StockValue, ItemCount: int(r.ItemCount), Version: r.Version})
	}
	return out, nil
}

func (s *Service) warehouseByID(ctx context.Context, p auth.Principal, propertyID, id int64) (Warehouse, error) {
	list, err := s.ListWarehouses(ctx, propertyID)
	if err != nil {
		return Warehouse{}, err
	}
	for _, w := range list {
		if w.ID == id {
			return w, nil
		}
	}
	return Warehouse{}, notFound("WAREHOUSE_NOT_FOUND", "warehouse")
}

func (s *Service) checkWarehouse(ctx context.Context, p auth.Principal, propertyID int64, in WarehouseInput, creating bool) ([]apperr.FieldError, error) {
	var f []apperr.FieldError
	if creating && !warehouseCodePattern.MatchString(in.Code) {
		f = append(f, fieldErr("code", "INVALID_FORMAT", "1-20 characters: A-Z, 0-9, . _ -"))
	}
	f = append(f, checkName("name", in.Name, maxName)...)
	if !warehouseKinds[in.Kind] {
		f = append(f, fieldErr("kind", "INVALID_VALUE", "MAIN, OUTLET or DEPARTMENT"))
	}
	if !costClasses[in.CostClass] {
		f = append(f, fieldErr("cost_class", "INVALID_VALUE", "FOOD, BEVERAGE or OTHER"))
	}
	if (in.Kind == "OUTLET") != (in.OutletID != nil) {
		f = append(f, fieldErr("outlet_id", "INVALID_VALUE", "a warehouse of an outlet names the outlet, any other does not"))
	}
	if in.OutletID != nil {
		var ok bool
		if err := s.txm.DB(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pos_outlets WHERE tenant_id = $1 AND property_id = $2 AND id = $3)`, p.TenantID, propertyID, *in.OutletID).Scan(&ok); err != nil {
			return nil, err
		} else if !ok {
			f = append(f, fieldErr("outlet_id", "NOT_FOUND", "no such outlet in this property"))
		}
	}
	if in.DepartmentID != nil && s.depts != nil {
		if err := s.depts.Check(ctx, p.TenantID, propertyID, *in.DepartmentID, "department_id"); err != nil {
			var ae *apperr.Error
			if errors.As(err, &ae) && len(ae.Fields) > 0 {
				f = append(f, ae.Fields...)
			} else {
				return nil, err
			}
		}
	}
	return f, nil
}

// CreateWarehouse adds a warehouse (inventory.manage).
func (s *Service) CreateWarehouse(ctx context.Context, propertyID int64, in WarehouseInput) (Warehouse, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Warehouse{}, err
	}
	in.Code, in.Name = normalCode(in.Code), trimmed(in.Name)
	if in.CostClass == "" {
		in.CostClass = ClassOther
	}
	var out Warehouse
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		f, err := s.checkWarehouse(ctx, p, propertyID, in, true)
		if err != nil {
			return err
		}
		if len(f) > 0 {
			return invalid("warehouse", f...)
		}
		r, err := s.q(ctx).InsertWarehouse(ctx, inventorydb.InsertWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, Code: in.Code, Name: in.Name, Kind: in.Kind, OutletID: in.OutletID,
			DepartmentID: in.DepartmentID, CostClass: in.CostClass, IsActive: active(in.IsActive), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if out, err = s.warehouseByID(ctx, p, propertyID, r.ID); err != nil {
			return err
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.warehouse_created", "inv_warehouse", r.ID, r.Code, nil, out))
	})
	return out, err
}

// UpdateWarehouse changes a warehouse (inventory.manage). A warehouse that holds stock cannot be switched off.
func (s *Service) UpdateWarehouse(ctx context.Context, propertyID, id int64, in WarehouseInput) (Warehouse, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return Warehouse{}, err
	}
	in.Name = trimmed(in.Name)
	if in.CostClass == "" {
		in.CostClass = ClassOther
	}
	var out Warehouse
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		cur, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id})
		if err != nil {
			return orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
		}
		if cur.Version != in.Version {
			return versionConflict("warehouse", cur.Version)
		}
		f, err := s.checkWarehouse(ctx, p, propertyID, in, false)
		if err != nil {
			return err
		}
		if len(f) > 0 {
			return invalid("warehouse", f...)
		}
		if !active(in.IsActive) && cur.IsActive {
			if has, err := q.WarehouseHasStock(ctx, inventorydb.WarehouseHasStockParams{PropertyID: propertyID, WarehouseID: id}); err != nil {
				return err
			} else if has {
				return apperr.Conflict("WAREHOUSE_HAS_STOCK", "the warehouse holds stock: it cannot be switched off")
			}
		}
		if in.Kind != cur.Kind || !equalPtr(in.OutletID, cur.OutletID) {
			if has, err := q.WarehouseHasMovements(ctx, inventorydb.WarehouseHasMovementsParams{PropertyID: propertyID, WarehouseID: id}); err != nil {
				return err
			} else if has {
				return apperr.Conflict("WAREHOUSE_HAS_STOCK", "the warehouse has stock movements: its kind and its outlet cannot change")
			}
		}
		if _, err := q.UpdateWarehouse(ctx, inventorydb.UpdateWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: id, Version: in.Version, Name: in.Name, Kind: in.Kind, OutletID: in.OutletID,
			DepartmentID: in.DepartmentID, CostClass: in.CostClass, IsActive: active(in.IsActive), ActorID: p.ActorID()}); err != nil {
			return orNotFound(err, versionConflict("warehouse", cur.Version))
		}
		before, err := s.warehouseByID(ctx, p, propertyID, id)
		if err != nil {
			return err
		}
		out = before
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.warehouse_updated", "inv_warehouse", id, cur.Code, nil, out))
	})
	return out, err
}

func equalPtr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// Level is the minimum (and the optional maximum and reorder quantity) of an item in a warehouse.
type Level struct {
	ItemID     int64            `json:"item_id"`
	ItemCode   string           `json:"item_code,omitempty"`
	ItemName   string           `json:"item_name,omitempty"`
	MinQty     decimal.Decimal  `json:"min_qty"`
	MaxQty     *decimal.Decimal `json:"max_qty"`
	ReorderQty *decimal.Decimal `json:"reorder_qty"`
}

// GetLevels lists the minimum levels of a warehouse.
func (s *Service) GetLevels(ctx context.Context, propertyID, warehouseID int64) ([]Level, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	q := s.q(ctx)
	if _, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: warehouseID}); err != nil {
		return nil, orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
	}
	rows, err := q.ListLevels(ctx, inventorydb.ListLevelsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID})
	if err != nil {
		return nil, err
	}
	out := make([]Level, 0, len(rows))
	for _, r := range rows {
		out = append(out, Level{ItemID: r.ItemID, ItemCode: r.ItemCode, ItemName: r.ItemName, MinQty: r.MinQty, MaxQty: r.MaxQty, ReorderQty: r.ReorderQty})
	}
	return out, nil
}

// SetLevels replaces the minimum levels of a warehouse (inventory.manage).
func (s *Service) SetLevels(ctx context.Context, propertyID, warehouseID int64, in []Level) ([]Level, error) {
	p, err := s.manage(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	var f []apperr.FieldError
	seen := map[int64]bool{}
	for i, l := range in {
		at := fmt.Sprintf("levels[%d]", i)
		switch {
		case seen[l.ItemID]:
			f = append(f, fieldErr(at+".item_id", "DUPLICATE", "the item is listed twice"))
		case l.MinQty.IsNegative() || !l.MinQty.Equal(Round6(l.MinQty)):
			f = append(f, fieldErr(at+".min_qty", "INVALID_VALUE", "zero or more, six decimals at most"))
		case l.MaxQty != nil && l.MaxQty.LessThan(l.MinQty):
			f = append(f, fieldErr(at+".max_qty", "OUT_OF_RANGE", "not below the minimum"))
		case l.ReorderQty != nil && l.ReorderQty.IsNegative():
			f = append(f, fieldErr(at+".reorder_qty", "INVALID_VALUE", "zero or more"))
		}
		seen[l.ItemID] = true
	}
	if len(in) > 2000 {
		f = append(f, fieldErr("levels", "OUT_OF_RANGE", "2000 levels at most"))
	}
	if len(f) > 0 {
		return nil, invalid("levels", f...)
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		wh, err := q.GetWarehouse(ctx, inventorydb.GetWarehouseParams{TenantID: p.TenantID, PropertyID: propertyID, ID: warehouseID})
		if err != nil {
			return orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
		}
		ids := make([]int64, 0, len(in))
		for _, l := range in {
			ids = append(ids, l.ItemID)
		}
		slices.Sort(ids)
		known := map[int64]bool{}
		if len(ids) > 0 {
			rows, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
			if err != nil {
				return err
			}
			for _, r := range rows {
				known[r.ItemID] = true
			}
		}
		for i, l := range in {
			if !known[l.ItemID] {
				return invalid("levels", fieldErr(fmt.Sprintf("levels[%d].item_id", i), "NOT_FOUND", "no such item in this property"))
			}
		}
		if err := q.DeleteLevels(ctx, inventorydb.DeleteLevelsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID}); err != nil {
			return err
		}
		for _, l := range in {
			if err := q.InsertLevel(ctx, inventorydb.InsertLevelParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemID: l.ItemID, MinQty: Round6(l.MinQty), MaxQty: l.MaxQty, ReorderQty: l.ReorderQty}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "inventory.levels_set", "inv_warehouse", warehouseID, wh.Code, nil, map[string]any{"levels": len(in)}))
	})
	if err != nil {
		return nil, err
	}
	return s.GetLevels(ctx, propertyID, warehouseID)
}
