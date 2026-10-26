package banquet

import (
	"context"

	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet/banquetdb"
	"kamarapms/internal/platform/civil"
)

// The deduction of goods (36-banquet.md 4.9, F12, Q3). When a session is posted, each charge that comes from an item (or a package component) linked to a POS product asks the stock to queue a deduction
// of the goods of that product's recipe, in the transaction of the posting. This package does not import the inventory: the stock is a hook wired in internal/app, which answers "off" when the plan has no
// Inventory. A banquet without Inventory, without a warehouse or without a link posts exactly as before.

// StockSale is one charge of a posted session that sells the product of a recipe.
type StockSale struct {
	PostingLineID int64 // banquet_posting_items.id
	WarehouseID   int64
	ProductID     int64
	ChargeCodeID  int64
	Quantity      decimal.Decimal
	SaleClass     string // the class of the item (FOOD, BEVERAGE, OTHER); empty: the class of the product's category
	BusinessDate  civil.Date
}

// StockHook is what the banquet asks of the stock.
type StockHook interface {
	// On says whether the plan of the tenant has the stock module.
	On(ctx context.Context, tenantID int64) (bool, error)
	// QueueSale queues the deduction of one charge; a product without a recipe queues nothing.
	QueueSale(ctx context.Context, tenantID, propertyID int64, sale StockSale) error
}

// SetStock wires the stock (nil: the banquet deducts nothing).
func (s *Service) SetStock(h StockHook) { s.stock = h }

// StockQty is the quantity of the product a charge of a line takes out of the stock: the billed guests x the quantity per guest for a component of a package; the guests x the quantity for an item per
// guest; the quantity for an item per session or per unit. An item charged per hour is a service, not goods, and takes nothing out.
func StockQty(l Line, c *LineComponent, pax int) decimal.Decimal {
	if c != nil {
		return decimal.NewFromInt(int64(pax)).Mul(c.QtyPerPax)
	}
	switch l.Basis {
	case "PER_PAX":
		return decimal.NewFromInt(int64(pax)).Mul(l.Quantity)
	case "PER_SESSION", "PER_UNIT":
		return l.Quantity
	}
	return decimal.Zero
}

// stockLinkOf finds the product and the class a planned charge sells: the component of its package, else the line itself.
func stockLinkOf(l Line, componentItemID int64) (product *int64, class string, c *LineComponent) {
	if componentItemID != 0 {
		for i := range l.Components {
			if l.Components[i].ItemID == componentItemID {
				c = &l.Components[i]
				product = c.PosProductID
				if c.FbClass != nil {
					class = *c.FbClass
				}
				return product, class, c
			}
		}
		return nil, "", nil
	}
	if l.FbClass != nil {
		class = *l.FbClass
	}
	return l.PosProductID, class, nil
}

// stockWarehouse answers the warehouse the goods are taken from, 0 when the stock is off or no warehouse is set.
func (s *Service) stockWarehouse(ctx context.Context, tenantID, propertyID int64) (int64, error) {
	if s.stock == nil {
		return 0, nil
	}
	on, err := s.stock.On(ctx, tenantID)
	if err != nil || !on {
		return 0, err
	}
	st, err := s.settings(ctx, propertyID)
	if err != nil || st.WarehouseID == nil {
		return 0, err
	}
	return *st.WarehouseID, nil
}

// queueStock queues the deduction of the goods of one posted charge. wh is the warehouse of the banquet (0: none).
func (s *Service) queueStock(ctx context.Context, tenantID, propertyID, wh, postingLineID int64, l Line, c PlannedCharge, pax int, bd civil.Date) error {
	if wh == 0 {
		return nil
	}
	product, class, comp := stockLinkOf(l, c.ComponentID)
	if product == nil {
		return nil
	}
	qty := StockQty(l, comp, pax)
	if !qty.IsPositive() {
		return nil
	}
	return s.stock.QueueSale(ctx, tenantID, propertyID, StockSale{PostingLineID: postingLineID, WarehouseID: wh, ProductID: *product, ChargeCodeID: c.ChargeCodeID, Quantity: qty, SaleClass: class, BusinessDate: bd})
}

// stockNote counts what posting deducts for the lines of an event, for the dialog of posting.
func (s *Service) stockNote(ctx context.Context, tenantID, propertyID int64, lines []Line) (StockNote, error) {
	wh, err := s.stockWarehouse(ctx, tenantID, propertyID)
	if err != nil || wh == 0 {
		return StockNote{}, err
	}
	var ids []int64
	for _, l := range lines {
		if l.PosProductID != nil {
			ids = append(ids, *l.PosProductID)
		}
		for _, c := range l.Components {
			if c.PosProductID != nil {
				ids = append(ids, *c.PosProductID)
			}
		}
	}
	q := s.q(ctx)
	w, err := q.WarehouseOfProperty(ctx, banquetdb.WarehouseOfPropertyParams{PropertyID: propertyID, ID: wh})
	if err != nil {
		return StockNote{}, err
	}
	note := StockNote{Enabled: true, WarehouseName: &w.Name}
	if len(ids) == 0 {
		return note, nil
	}
	withRecipe, err := q.ProductsWithRecipe(ctx, banquetdb.ProductsWithRecipeParams{PropertyID: propertyID, ProductIds: ids})
	if err != nil {
		return StockNote{}, err
	}
	has := make(map[int64]bool, len(withRecipe))
	for _, id := range withRecipe {
		has[id] = true
	}
	for _, l := range lines {
		if l.PosProductID != nil && l.Basis != "PER_HOUR" && has[*l.PosProductID] { // an item per hour is a service
			note.Lines++
		}
		for _, c := range l.Components {
			if c.PosProductID != nil && has[*c.PosProductID] {
				note.Lines++
			}
		}
	}
	return note, nil
}
