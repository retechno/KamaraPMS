package inventory

import (
	"context"
	"sort"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
)

// The recipe cost list (35-inventory.md 12.5, A7): every product the outlets sell, with the cost of a portion from its base recipe, the net price, the margin and the cost percentage against the target of the
// outlet. A product with no recipe is in the list and marked: its cost is not known. The cost of an ingredient is the one of the recipe editor (unitCosts): the average of the warehouse of the outlet, else the last
// cost, else the reference cost of the item. The options (variants and modifiers) are not in it.

// RecipeCostRow is a product at an outlet.
type RecipeCostRow struct {
	OutletID     int64            `json:"outlet_id"`
	OutletCode   string           `json:"outlet_code"`
	OutletName   string           `json:"outlet_name"`
	ProductID    int64            `json:"product_id"`
	ProductCode  string           `json:"product_code"`
	ProductName  string           `json:"product_name"`
	CategoryName string           `json:"category_name"`
	Class        string           `json:"class"`
	HasRecipe    bool             `json:"has_recipe"`
	Cost         *decimal.Decimal `json:"cost"`
	NetPrice     *decimal.Decimal `json:"net_price"`
	Margin       *decimal.Decimal `json:"margin"`
	CostPct      *decimal.Decimal `json:"cost_pct"`
	TargetPct    *decimal.Decimal `json:"target_pct"`
	AboveTarget  bool             `json:"above_target"`
}

// RecipeCostReport is the list, lowest margin first (in percent of the price), the products with no recipe last.
type RecipeCostReport struct {
	Rows             []RecipeCostRow `json:"rows"`
	WithoutRecipe    int             `json:"without_recipe"`
	CurrencyDecimals int32           `json:"currency_decimals"`
}

// RecipeCosts answers the list (inventory.view, or the permission to read recipes). outletID narrows it to one outlet.
func (s *Service) RecipeCosts(ctx context.Context, propertyID int64, outletID *int64) (RecipeCostReport, error) {
	p, err := s.canRecipeRead(ctx, propertyID)
	if err != nil {
		return RecipeCostReport{}, err
	}
	prop, err := s.days.GetProperty(ctx, propertyID)
	if err != nil {
		return RecipeCostReport{}, err
	}
	q := s.q(ctx)
	outlets, err := q.ListOutletsStock(ctx, inventorydb.ListOutletsStockParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return RecipeCostReport{}, err
	}
	products, err := q.ListProductsForCostList(ctx, inventorydb.ListProductsForCostListParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return RecipeCostReport{}, err
	}
	lines, err := q.ListBaseRecipeLinesAll(ctx, inventorydb.ListBaseRecipeLinesAllParams{TenantID: p.TenantID, PropertyID: propertyID})
	if err != nil {
		return RecipeCostReport{}, err
	}
	byProduct := map[int64][]inventorydb.ListBaseRecipeLinesAllRow{}
	seen := map[int64]bool{}
	var itemIDs []int64
	refs := map[int64]*decimal.Decimal{}
	for _, l := range lines {
		byProduct[l.ProductID] = append(byProduct[l.ProductID], l)
		if !seen[l.ItemID] {
			seen[l.ItemID] = true
			itemIDs = append(itemIDs, l.ItemID)
			refs[l.ItemID] = l.ReferenceCost
		}
	}
	tl, err := s.targetsOf(ctx, p.TenantID, propertyID)
	if err != nil {
		return RecipeCostReport{}, err
	}
	tg := targets{list: tl}
	out := RecipeCostReport{Rows: []RecipeCostRow{}, CurrencyDecimals: prop.CurrencyDecimals}
	for _, o := range outlets {
		if outletID != nil && o.ID != *outletID {
			continue
		}
		var wid int64
		wclass := ""
		if o.WarehouseID != nil {
			wid = *o.WarehouseID
			if ow, err := q.GetOutletStock(ctx, inventorydb.GetOutletStockParams{TenantID: p.TenantID, PropertyID: propertyID, ID: o.ID}); err == nil {
				wclass = ow.WarehouseClass
			}
		}
		costs, _, err := s.unitCosts(ctx, p.TenantID, propertyID, wid, itemIDs, refs)
		if err != nil {
			return RecipeCostReport{}, err
		}
		oid := o.ID
		for _, pr := range products {
			var price decimal.Decimal
			sold := false
			if s.pricer != nil {
				if price, sold, err = s.pricer.NetPrice(ctx, p.TenantID, propertyID, o.ID, pr.ID); err != nil {
					return RecipeCostReport{}, err
				}
			}
			if !sold {
				continue
			}
			class, err := s.saleClass(ctx, p.TenantID, propertyID, pr.ID, wclass)
			if err != nil {
				return RecipeCostReport{}, err
			}
			row := RecipeCostRow{OutletID: o.ID, OutletCode: o.Code, OutletName: o.Name, ProductID: pr.ID, ProductCode: pr.Code, ProductName: pr.Name, CategoryName: pr.CategoryName, Class: class, HasRecipe: pr.HasRecipe}
			net := price
			row.NetPrice = &net
			if t := tg.of(&oid, class); t != nil {
				row.TargetPct = t
			}
			if !pr.HasRecipe {
				out.WithoutRecipe++
				out.Rows = append(out.Rows, row)
				continue
			}
			cost := decimal.Zero
			for _, l := range byProduct[pr.ID] {
				cost = cost.Add(stockQtyOf(l.Quantity, l.StockNum, l.StockDen).Mul(costs[l.ItemID]))
			}
			cost = cost.Round(prop.CurrencyDecimals)
			row.Cost = &cost
			if price.IsPositive() {
				m := price.Sub(cost).Round(prop.CurrencyDecimals)
				row.Margin = &m
				row.CostPct = pct(cost, price)
			}
			row.AboveTarget = row.CostPct != nil && row.TargetPct != nil && row.CostPct.GreaterThan(*row.TargetPct)
			out.Rows = append(out.Rows, row)
		}
	}
	// lowest margin first (the margin as a share of the price, so the highest cost percentage first); the products with no recipe, which have no margin, last
	sort.SliceStable(out.Rows, func(i, j int) bool {
		a, b := out.Rows[i].CostPct, out.Rows[j].CostPct
		if a == nil || b == nil {
			return a != nil && b == nil
		}
		return a.GreaterThan(*b)
	})
	return out, nil
}
