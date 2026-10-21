package inventory

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
)

// LowStockRequestInput asks for a draft purchase request for items that are below their minimum in one warehouse.
type LowStockRequestInput struct {
	ClientUUID   string  `json:"client_uuid"`
	WarehouseID  int64   `json:"warehouse_id"`
	ItemIDs      []int64 `json:"item_ids"`
	DepartmentID *int64  `json:"department_id"`
}

// CreateRequestFromLowStock makes a draft purchase request (inventory.request) for the chosen items of one warehouse that are still below their minimum: each line asks for the quantity of suggestedOrder,
// in the default purchase unit of the item (else its stock unit), rounded up to six places. The person opens the draft, changes it and submits it as any other request. A replay with the same client_uuid
// answers the request made the first time. An item that is no longer below its minimum is left out; when none is, nothing is made.
func (s *Service) CreateRequestFromLowStock(ctx context.Context, propertyID int64, in LowStockRequestInput) (Request, error) {
	p, err := s.canRequest(ctx, propertyID)
	if err != nil {
		return Request{}, err
	}
	if len(in.ItemIDs) == 0 || len(in.ItemIDs) > maxDocLines {
		return Request{}, invalid("request", fieldErr("item_ids", "OUT_OF_RANGE", "1 to "+itoa(maxDocLines)+" items"))
	}
	q := s.q(ctx)
	rows, err := q.LowStockOfItems(ctx, inventorydb.LowStockOfItemsParams{TenantID: p.TenantID, PropertyID: propertyID, WarehouseID: in.WarehouseID, ItemIds: in.ItemIDs})
	if err != nil {
		return Request{}, err
	}
	if len(rows) == 0 {
		return Request{}, apperr.Conflict("NOTHING_TO_ORDER", "none of the items is below its minimum in this warehouse")
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ItemID)
	}
	units, err := q.ListItemUnitsOf(ctx, inventorydb.ListItemUnitsOfParams{TenantID: p.TenantID, PropertyID: propertyID, ItemIds: ids})
	if err != nil {
		return Request{}, err
	}
	byItem := map[int64][]inventorydb.ListItemUnitsOfRow{}
	for _, u := range units {
		byItem[u.ItemID] = append(byItem[u.ItemID], u)
	}
	lines := make([]RequestLineInput, 0, len(rows))
	for _, r := range rows {
		want := suggestedOrder(r.Qty, r.MinQty, r.MaxQty, r.ReorderQty)
		unit, ok := purchaseUnitOf(byItem[r.ItemID], r.StockUnitID)
		if !ok || !want.IsPositive() {
			continue
		}
		// one purchase unit is num/den stock units: a quantity in stock units divided by that
		qty := want.Mul(decimal.NewFromInt(int64(unit.StockDen))).Div(decimal.NewFromInt(int64(unit.StockNum))).RoundCeil(6)
		lines = append(lines, RequestLineInput{ItemID: r.ItemID, UnitID: unit.UnitID, Qty: qty})
	}
	if len(lines) == 0 {
		return Request{}, apperr.Conflict("NOTHING_TO_ORDER", "none of the items is below its minimum in this warehouse")
	}
	note := "Created from the low stock report"
	wh := in.WarehouseID
	return s.CreateRequest(ctx, propertyID, RequestInput{ClientUUID: in.ClientUUID, DepartmentID: in.DepartmentID, WarehouseID: &wh, Note: &note, Lines: lines})
}

// purchaseUnitOf picks the unit a purchase request asks in: the default purchase unit of the item, else its stock unit.
func purchaseUnitOf(units []inventorydb.ListItemUnitsOfRow, stockUnitID int64) (inventorydb.ListItemUnitsOfRow, bool) {
	var stock *inventorydb.ListItemUnitsOfRow
	for i := range units {
		if units[i].IsDefaultPurchase {
			return units[i], true
		}
		if units[i].UnitID == stockUnitID {
			stock = &units[i]
		}
	}
	if stock != nil {
		return *stock, true
	}
	return inventorydb.ListItemUnitsOfRow{}, false
}
