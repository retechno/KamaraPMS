package inventory

import (
	"context"
	"strings"
	"unicode/utf8"

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
	// Note is the text of the draft, in the language of the person (the screen sends its own translation); empty gives the English text.
	Note *string `json:"note"`
}

// maxLowStockNote is the longest note the draft takes.
const maxLowStockNote = 200

// measureUnits are the units of a quantity that is measured, not counted: a request for them is rounded up to the thousandth (a gram, a millilitre). Any other unit (a sack, a carton, a pack, a bottle) is
// bought whole.
var measureUnits = map[string]bool{"kg": true, "g": true, "gr": true, "mg": true, "l": true, "lt": true, "ml": true, "m": true, "cm": true}

// requestQty is the quantity to ask in a purchase unit: the stock quantity wanted, in that unit, rounded UP to a whole unit, or to the thousandth for a unit that is measured.
func requestQty(wantStock decimal.Decimal, unit inventorydb.ListItemUnitsOfRow) decimal.Decimal {
	q := wantStock.Mul(decimal.NewFromInt(int64(unit.StockDen))).Div(decimal.NewFromInt(int64(unit.StockNum)))
	if measureUnits[strings.ToLower(unit.UnitCode)] {
		return q.RoundCeil(3)
	}
	return q.RoundCeil(0)
}

// CreateRequestFromLowStock makes a draft purchase request (inventory.request) for the chosen items of one warehouse that are still below their minimum: each line asks for the quantity of suggestedOrder,
// in the default purchase unit of the item (else its stock unit), rounded up to a whole unit (to the thousandth for kg, l and the other measured units). The person opens the draft, changes it and submits it as any other request. A replay with the same client_uuid
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
		qty := requestQty(want, unit)
		lines = append(lines, RequestLineInput{ItemID: r.ItemID, UnitID: unit.UnitID, Qty: qty})
	}
	if len(lines) == 0 {
		return Request{}, apperr.Conflict("NOTHING_TO_ORDER", "none of the items is below its minimum in this warehouse")
	}
	note := "Created from the low stock report"
	if in.Note != nil && strings.TrimSpace(*in.Note) != "" {
		note = strings.TrimSpace(*in.Note)
		if utf8.RuneCountInString(note) > maxLowStockNote {
			return Request{}, invalid("request", fieldErr("note", "OUT_OF_RANGE", "at most "+itoa(maxLowStockNote)+" characters"))
		}
	}
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
