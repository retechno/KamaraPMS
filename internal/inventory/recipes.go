package inventory

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The recipe of a POS product (35-inventory.md 3.1, 5.6, D15, mockup 27): the stock an item takes for one portion, in a unit of the item; a variant of the product replaces the line of the same item, a
// modifier adds. The recipe is written in the POS master (pos.manage) and read with the cost of the ingredients at the warehouse of an outlet. The module of the stock decides whether it deducts
// anything: the routes belong to the POS (the entitlement of the INVENTORY module is asked here) and without the module the recipe is read only.

// PosPricer is what the recipe asks of the POS: the net price (before service and tax) of one portion of a product at an outlet.
type PosPricer interface {
	NetPrice(ctx context.Context, tenantID, propertyID, outletID, productID int64) (price decimal.Decimal, sold bool, err error)
}

// Entitlement says whether the plan of the tenant includes the module of the stock.
type Entitlement interface {
	HasInventory(ctx context.Context, tenantID int64) (bool, error)
}

// RecipeLineInput is a line of a recipe as it is saved: the product's own (no option) or the line of an option.
type RecipeLineInput struct {
	OptionID *int64          `json:"option_id"`
	ItemID   int64           `json:"item_id"`
	UnitID   int64           `json:"unit_id"`
	Quantity decimal.Decimal `json:"quantity"`
	Note     *string         `json:"note"`
}

// RecipeInput replaces the whole recipe of a product.
type RecipeInput struct {
	Lines []RecipeLineInput `json:"lines"`
}

// RecipeLine is a line of a recipe with its cost at the warehouse of the outlet.
type RecipeLine struct {
	ID            int64           `json:"id"`
	ItemID        int64           `json:"item_id"`
	ItemCode      string          `json:"item_code"`
	ItemName      string          `json:"item_name"`
	UnitID        int64           `json:"unit_id"`
	UnitCode      string          `json:"unit_code"`
	Quantity      decimal.Decimal `json:"quantity"`
	Note          *string         `json:"note"`
	StockUnitCode string          `json:"stock_unit_code"`
	StockQty      decimal.Decimal `json:"stock_qty"`      // for one portion, in stock units
	UnitCost      decimal.Decimal `json:"unit_cost"`      // of one stock unit
	CostBasis     string          `json:"cost_basis"`     // AVERAGE, LAST, REFERENCE or NONE
	Cost          decimal.Decimal `json:"cost"`           // of the line for one portion
	ReplacedQty   decimal.Decimal `json:"replaced_qty"`   // a variant: what its line takes the place of, in the unit of the line (0 when it replaces nothing)
	ReplacedCost  decimal.Decimal `json:"replaced_cost"`  // a variant: the cost of that
	ReplacesLabel *string         `json:"replaces_label"` // a variant: the name of the item it replaces
}

// RecipeOption is an option of the product (a variant or a modifier) with its lines.
type RecipeOption struct {
	OptionID   int64            `json:"option_id"`
	Name       string           `json:"name"`
	GroupID    int64            `json:"group_id"`
	GroupName  string           `json:"group_name"`
	Kind       string           `json:"kind"` // VARIANT or MODIFIER
	IsActive   bool             `json:"is_active"`
	PriceDelta *decimal.Decimal `json:"price_delta"`
	Lines      []RecipeLine     `json:"lines"`
	ExtraCost  decimal.Decimal  `json:"extra_cost"` // what the option adds to the cost of a portion (a variant: net of what it replaces)
}

// RecipeOutlet is an outlet the cost can be read at.
type RecipeOutlet struct {
	ID            int64   `json:"id"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	WarehouseID   *int64  `json:"warehouse_id"`
	WarehouseName *string `json:"warehouse_name"`
}

// Recipe is the recipe of a product with its cost and margin at one outlet.
type Recipe struct {
	ProductID        int64            `json:"product_id"`
	ProductCode      string           `json:"product_code"`
	ProductName      string           `json:"product_name"`
	InventoryActive  bool             `json:"inventory_active"` // the plan has the stock module: false makes the recipe read only
	Editable         bool             `json:"editable"`
	OutletID         *int64           `json:"outlet_id"`
	OutletName       *string          `json:"outlet_name"`
	WarehouseID      *int64           `json:"warehouse_id"`
	WarehouseName    *string          `json:"warehouse_name"`
	SaleClass        *string          `json:"sale_class"`
	Outlets          []RecipeOutlet   `json:"outlets"`
	Base             []RecipeLine     `json:"base"`
	Options          []RecipeOption   `json:"options"`
	BaseCost         decimal.Decimal  `json:"base_cost"`
	NetPrice         *decimal.Decimal `json:"net_price"`
	Margin           *decimal.Decimal `json:"margin"`
	FoodCostPct      *decimal.Decimal `json:"food_cost_pct"`
	CostBasisLabel   string           `json:"cost_basis_label"` // how the unit costs were taken: AVERAGE (the balance of the warehouse), or REFERENCE where an item has no stock there
	CurrencyDecimals int32            `json:"currency_decimals"`
}

func (s *Service) canRecipeRead(ctx context.Context, propertyID int64) (auth.Principal, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return p, err
	}
	return p, auth.RequireAny(ctx, s.authz, propertyID, append([]auth.Permission{auth.PermPOSManage}, readers...)...)
}

// entitled is whether the plan of the tenant has the stock module; without a wired entitlement the module is taken as on.
func (s *Service) entitled(ctx context.Context, tenantID int64) (bool, error) {
	if s.entitlement == nil {
		return true, nil
	}
	return s.entitlement.HasInventory(ctx, tenantID)
}

// unitCosts reads the cost of one stock unit of each item at a warehouse (5.6): the average of the balance when there is stock, else the last unit cost, else the reference cost of the item.
func (s *Service) unitCosts(ctx context.Context, tenantID, propertyID, warehouseID int64, itemIDs []int64, refs map[int64]*decimal.Decimal) (map[int64]decimal.Decimal, map[int64]string, error) {
	costs, basis := map[int64]decimal.Decimal{}, map[int64]string{}
	if warehouseID != 0 && len(itemIDs) > 0 {
		rows, err := s.q(ctx).ListBalancesOfItems(ctx, inventorydb.ListBalancesOfItemsParams{TenantID: tenantID, PropertyID: propertyID, WarehouseID: warehouseID, ItemIds: itemIDs})
		if err != nil {
			return nil, nil, err
		}
		for _, r := range rows {
			switch {
			case r.Qty.IsPositive():
				costs[r.ItemID], basis[r.ItemID] = r.Value.Div(r.Qty), "AVERAGE"
			case r.LastUnitCost != nil:
				costs[r.ItemID], basis[r.ItemID] = *r.LastUnitCost, "LAST"
			}
		}
	}
	for _, id := range itemIDs {
		if _, ok := costs[id]; ok {
			continue
		}
		if ref := refs[id]; ref != nil {
			costs[id], basis[id] = *ref, "REFERENCE"
		} else {
			costs[id], basis[id] = decimal.Zero, "NONE"
		}
	}
	return costs, basis, nil
}

func stockQtyOf(qty decimal.Decimal, num, den int32) decimal.Decimal {
	return qty.Mul(decimal.NewFromInt(int64(num))).Div(decimal.NewFromInt(int64(den)))
}

// GetRecipe reads the recipe of a product with the cost of its ingredients at the warehouse of an outlet (the first outlet that has one when none is named) and the margin against the net price.
func (s *Service) GetRecipe(ctx context.Context, propertyID, productID int64, outletID *int64) (Recipe, error) {
	p, err := s.canRecipeRead(ctx, propertyID)
	if err != nil {
		return Recipe{}, err
	}
	q := s.q(ctx)
	prod, err := q.GetProductForRecipe(ctx, inventorydb.GetProductForRecipeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: productID})
	if err != nil {
		return Recipe{}, orNotFound(err, notFound("PRODUCT_NOT_FOUND", "product"))
	}
	on, err := s.entitled(ctx, p.TenantID)
	if err != nil {
		return Recipe{}, err
	}
	out := Recipe{ProductID: prod.ID, ProductCode: prod.Code, ProductName: prod.Name, InventoryActive: on, Outlets: []RecipeOutlet{}, Base: []RecipeLine{}, Options: []RecipeOption{}}
	canWrite := false
	if on {
		canWrite = s.authz.Require(ctx, propertyID, auth.PermPOSManage) == nil
	}
	out.Editable = canWrite
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return Recipe{}, err
	}
	out.CurrencyDecimals = prop.CurrencyDecimals
	outlets, err := q.ListOutletsStock(ctx, inventorydb.ListOutletsStockParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return Recipe{}, err
	}
	for _, o := range outlets {
		ro := RecipeOutlet{ID: o.ID, Code: o.Code, Name: o.Name, WarehouseID: o.WarehouseID}
		if o.WarehouseID != nil {
			n := o.WarehouseName
			ro.WarehouseName = &n
		}
		out.Outlets = append(out.Outlets, ro)
	}
	var chosen *RecipeOutlet
	for i := range out.Outlets {
		o := &out.Outlets[i]
		if outletID != nil && o.ID == *outletID {
			chosen = o
		}
	}
	if chosen == nil && outletID == nil {
		for i := range out.Outlets {
			if out.Outlets[i].WarehouseID != nil {
				chosen = &out.Outlets[i]
				break
			}
		}
	}
	if outletID != nil && chosen == nil {
		return Recipe{}, notFound("OUTLET_NOT_FOUND", "outlet")
	}
	var warehouseID int64
	if chosen != nil {
		id, name := chosen.ID, chosen.Name
		out.OutletID, out.OutletName = &id, &name
		if chosen.WarehouseID != nil {
			warehouseID = *chosen.WarehouseID
			out.WarehouseID, out.WarehouseName = chosen.WarehouseID, chosen.WarehouseName
			wc := ""
			if o, err := q.GetOutletStock(ctx, inventorydb.GetOutletStockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: chosen.ID}); err == nil {
				wc = o.WarehouseClass
			}
			if class, err := s.saleClass(ctx, p.TenantID, propertyID, productID, wc); err == nil {
				out.SaleClass = &class
			}
		}
	}
	lines, err := q.ListRecipeLines(ctx, inventorydb.ListRecipeLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: productID})
	if err != nil {
		return Recipe{}, err
	}
	opts, err := q.ListProductOptionsForRecipe(ctx, inventorydb.ListProductOptionsForRecipeParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: productID})
	if err != nil {
		return Recipe{}, err
	}
	ids := make([]int64, 0, len(lines))
	refs := map[int64]*decimal.Decimal{}
	for _, l := range lines {
		ids = append(ids, l.ItemID)
		refs[l.ItemID] = l.ReferenceCost
	}
	costs, basis, err := s.unitCosts(ctx, p.TenantID, propertyID, warehouseID, ids, refs)
	if err != nil {
		return Recipe{}, err
	}
	round := func(d decimal.Decimal) decimal.Decimal { return d.Round(prop.CurrencyDecimals) }
	toLine := func(l inventorydb.ListRecipeLinesRow) RecipeLine {
		sq := stockQtyOf(l.Quantity, l.StockNum, l.StockDen)
		return RecipeLine{ID: l.ID, ItemID: l.ItemID, ItemCode: l.ItemCode, ItemName: l.ItemName, UnitID: l.UnitID, UnitCode: l.UnitCode, Quantity: l.Quantity, Note: l.Note,
			StockUnitCode: l.StockUnitCode, StockQty: sq.Round(6), UnitCost: costs[l.ItemID], CostBasis: basis[l.ItemID], Cost: round(sq.Mul(costs[l.ItemID]))}
	}
	baseByItem := map[int64]RecipeLine{}
	for _, l := range lines {
		if l.OptionID == nil {
			rl := toLine(l)
			out.Base = append(out.Base, rl)
			baseByItem[l.ItemID] = rl
			out.BaseCost = out.BaseCost.Add(rl.Cost)
		}
	}
	byOption := map[int64]*RecipeOption{}
	for _, o := range opts {
		d := o.PriceDelta
		ro := RecipeOption{OptionID: o.ID, Name: o.Name, GroupID: o.GroupID, GroupName: o.GroupName, Kind: o.GroupKind, IsActive: o.IsActive, PriceDelta: &d, Lines: []RecipeLine{}}
		out.Options = append(out.Options, ro)
	}
	for i := range out.Options {
		byOption[out.Options[i].OptionID] = &out.Options[i]
	}
	for _, l := range lines {
		if l.OptionID == nil {
			continue
		}
		o := byOption[*l.OptionID]
		if o == nil {
			continue
		}
		rl := toLine(l)
		if o.Kind == "VARIANT" {
			if b, ok := baseByItem[l.ItemID]; ok {
				rl.ReplacedQty, rl.ReplacedCost = b.Quantity, b.Cost
				name := b.ItemName
				rl.ReplacesLabel = &name
			}
		}
		o.Lines = append(o.Lines, rl)
		o.ExtraCost = o.ExtraCost.Add(rl.Cost).Sub(rl.ReplacedCost)
	}
	out.BaseCost = round(out.BaseCost)
	for i := range out.Options {
		out.Options[i].ExtraCost = round(out.Options[i].ExtraCost)
	}
	out.CostBasisLabel = "AVERAGE"
	if warehouseID == 0 {
		out.CostBasisLabel = "REFERENCE"
	}
	if s.pricer != nil && chosen != nil && out.OutletID != nil {
		price, sold, err := s.pricer.NetPrice(ctx, p.TenantID, propertyID, *out.OutletID, productID)
		if err != nil {
			return Recipe{}, err
		}
		if sold && price.IsPositive() {
			margin := round(price.Sub(out.BaseCost))
			pct := out.BaseCost.Div(price).Mul(decimal.NewFromInt(100)).Round(1)
			out.NetPrice, out.Margin, out.FoodCostPct = &price, &margin, &pct
		}
	}
	return out, nil
}

// SaveRecipe replaces the whole recipe of a product (pos.manage, the module of the stock in the plan). The lines name items and units of the property, an option of the product, a quantity above zero
// with four decimals at most, and an item once for the product and once for each option; a change applies to the orders sent after it and changes no sale that was queued already.
func (s *Service) SaveRecipe(ctx context.Context, propertyID, productID int64, in RecipeInput) (Recipe, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Recipe{}, err
	}
	if err := s.authz.Require(ctx, propertyID, auth.PermPOSManage); err != nil {
		return Recipe{}, err
	}
	on, err := s.entitled(ctx, p.TenantID)
	if err != nil {
		return Recipe{}, err
	}
	if !on {
		return Recipe{}, apperr.Forbidden("MODULE_NOT_IN_PLAN", "the inventory module is not part of the subscription plan").WithContext("module", "INVENTORY")
	}
	if len(in.Lines) > 200 {
		return Recipe{}, invalid("recipe", fieldErr("lines", "OUT_OF_RANGE", "200 lines at most"))
	}
	err = s.write(ctx, propertyID, func(ctx context.Context, bd civil.Date) error {
		q := s.q(ctx)
		prod, err := q.GetProductForRecipe(ctx, inventorydb.GetProductForRecipeParams{TenantID: p.TenantID, PropertyID: propertyID, ID: productID})
		if err != nil {
			return orNotFound(err, notFound("PRODUCT_NOT_FOUND", "product"))
		}
		optIDs, err := q.ListOptionIdsOfProduct(ctx, inventorydb.ListOptionIdsOfProductParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: productID})
		if err != nil {
			return err
		}
		isOption := map[int64]bool{}
		for _, id := range optIDs {
			isOption[id] = true
		}
		itemIDs := make([]int64, 0, len(in.Lines))
		for _, l := range in.Lines {
			itemIDs = append(itemIDs, l.ItemID)
		}
		units := map[[2]int64]bool{}
		us, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: itemIDs})
		if err != nil {
			return err
		}
		for _, u := range us {
			units[[2]int64{u.ItemID, u.UnitID}] = true
		}
		infos := map[int64]inventorydb.ItemCategoryAccountsRow{}
		rows, err := q.ItemCategoryAccounts(ctx, inventorydb.ItemCategoryAccountsParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: itemIDs})
		if err != nil {
			return err
		}
		for _, r := range rows {
			infos[r.ItemID] = r
		}
		var f []apperr.FieldError
		seen := map[[2]int64]bool{}
		for i, l := range in.Lines {
			at := lineAt(i, "")
			var opt int64
			if l.OptionID != nil {
				opt = *l.OptionID
				if !isOption[opt] {
					f = append(f, fieldErr(at+"option_id", "NOT_FOUND", "not an option of this product"))
				}
			}
			it, ok := infos[l.ItemID]
			switch {
			case !ok:
				f = append(f, fieldErr(at+"item_id", "NOT_FOUND", "no such item in this property"))
			case !it.IsActive:
				f = append(f, fieldErr(at+"item_id", "INACTIVE", "the item is switched off"))
			case !units[[2]int64{l.ItemID, l.UnitID}]:
				f = append(f, fieldErr(at+"unit_id", "NOT_FOUND", "not a unit of the item"))
			}
			if !l.Quantity.IsPositive() || l.Quantity.GreaterThan(decimal.NewFromInt(1_000_000)) || !l.Quantity.Equal(l.Quantity.Round(4)) {
				f = append(f, fieldErr(at+"quantity", "INVALID_VALUE", "above zero, four decimals at most"))
			}
			if seen[[2]int64{opt, l.ItemID}] {
				f = append(f, fieldErr(at+"item_id", "DUPLICATE", "an item is on a recipe once for the product and once for each option"))
			}
			seen[[2]int64{opt, l.ItemID}] = true
			if l.Note != nil && len([]rune(*l.Note)) > 200 {
				f = append(f, fieldErr(at+"note", "INVALID_VALUE", "at most 200 characters"))
			}
		}
		if len(f) > 0 {
			return invalid("recipe", f...)
		}
		before, err := q.ListRecipeLines(ctx, inventorydb.ListRecipeLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: productID})
		if err != nil {
			return err
		}
		if err := q.DeleteRecipeLines(ctx, inventorydb.DeleteRecipeLinesParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: productID}); err != nil {
			return err
		}
		for _, l := range in.Lines {
			if err := q.InsertRecipeLine(ctx, inventorydb.InsertRecipeLineParams{TenantID: p.TenantID, PropertyID: propertyID, ProductID: productID, OptionID: l.OptionID, ItemID: l.ItemID, UnitID: l.UnitID,
				Quantity: l.Quantity, Note: optional(l.Note)}); err != nil {
				return err
			}
		}
		return s.audit.Write(ctx, s.entry(p, propertyID, bd, "pos.recipe_changed", "pos_product", prod.ID, prod.Name, map[string]any{"lines": len(before)}, map[string]any{"lines": len(in.Lines)}))
	})
	if err != nil {
		return Recipe{}, err
	}
	return s.GetRecipe(ctx, propertyID, productID, nil)
}

// SalesWithoutDeduction is a row of the report of 5.6: a line of the POS with a recipe that has no row in the queue.
type SalesWithoutDeduction struct {
	BillLineID  int64           `json:"bill_line_id"`
	BillNumber  string          `json:"bill_number"`
	OutletID    int64           `json:"outlet_id"`
	OutletCode  string          `json:"outlet_code"`
	OutletName  string          `json:"outlet_name"`
	ProductID   int64           `json:"product_id"`
	ProductName string          `json:"product_name"`
	Quantity    decimal.Decimal `json:"quantity"`
	SentOn      civil.Date      `json:"sent_on"`
	Voided      bool            `json:"voided"`
}

// SalesWithoutDeductionReport lists the lines of the POS posted between two business dates that have a recipe and no queue row: sales made while the module was off (inventory.view).
func (s *Service) SalesWithoutDeductionReport(ctx context.Context, propertyID int64, from, to civil.Date, outletID *int64, limit int) ([]SalesWithoutDeduction, error) {
	p, err := s.canRead(ctx, propertyID)
	if err != nil {
		return nil, err
	}
	if to.Before(from) {
		return nil, invalid("report", fieldErr("to", "INVALID_VALUE", "not before the start date"))
	}
	if from.DaysUntil(to) > 400 {
		return nil, invalid("report", fieldErr("to", "OUT_OF_RANGE", "400 days at most"))
	}
	if limit < 1 || limit > 1000 {
		limit = 500
	}
	rows, err := s.q(ctx).ListSalesWithoutDeduction(ctx, inventorydb.ListSalesWithoutDeductionParams{TenantID: p.TenantID, PropertyID: propertyID, FromDate: from, ToDate: to, OutletID: outletID, RowLimit: int32(limit)}) //nolint:gosec // G115: bounded
	if err != nil {
		return nil, err
	}
	out := make([]SalesWithoutDeduction, 0, len(rows))
	for _, r := range rows {
		out = append(out, SalesWithoutDeduction{BillLineID: r.BillLineID, BillNumber: derefStr(r.BillNumber), OutletID: r.OutletID, OutletCode: r.OutletCode, OutletName: r.OutletName,
			ProductID: r.ProductID, ProductName: r.ProductName, Quantity: r.Quantity, SentOn: r.SentOn, Voided: r.Status == "VOID"})
	}
	return out, nil
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
