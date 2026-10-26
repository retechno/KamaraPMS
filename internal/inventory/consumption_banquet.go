package inventory

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
)

// The deduction of the goods of a banquet (36-banquet.md 4.9, F12, Q3). A line of a posted session that is linked to a POS product with a recipe becomes a sale when the session is posted: in the same
// transaction the banquet calls QueueBanquetSale, which writes one row of the queue the POS uses (source BANQUET: no POS line, no outlet) and takes no stock lock. The worker, the drain of the close and
// the FAILED state that stops the close are the ones of the POS (5.6, 5.7). A correction of the guests after the posting and a cancellation move no stock: the food was prepared.

// BanquetSale is what the banquet tells the stock when a line of a posted session is a sale of goods.
type BanquetSale struct {
	PostingLineID int64 // banquet_posting_items.id: the key of the idempotency of the hook
	WarehouseID   int64 // the warehouse of the banquet (banquet_settings)
	ProductID     int64 // the POS product the item is linked to
	ChargeCodeID  int64 // its department is the department of the cost
	Quantity      decimal.Decimal
	SaleClass     string // the class of the item (FOOD, BEVERAGE, OTHER); empty: the class of the category of the product
	BusinessDate  civil.Date
}

// QueueBanquetSale queues the deduction of one line of a posted banquet session. It runs inside the transaction of the posting, takes no stock lock and writes nothing when the product has no recipe;
// a replay of the same line queues nothing twice. It reports whether a row was written.
func (s *Service) QueueBanquetSale(ctx context.Context, tenantID, propertyID int64, sale BanquetSale) (bool, error) {
	if !db.InTx(ctx) {
		return false, db.ErrNoTx
	}
	if !sale.Quantity.IsPositive() {
		return false, nil
	}
	q := s.q(ctx)
	lines, err := q.ListRecipeLines(ctx, inventorydb.ListRecipeLinesParams{TenantID: tenantID, PropertyID: propertyID, ProductID: sale.ProductID})
	if err != nil {
		return false, err
	}
	if len(lines) == 0 {
		return false, nil
	}
	comps := expandRecipe(lines, nil, sale.Quantity)
	if len(comps) == 0 {
		return false, nil
	}
	class := sale.SaleClass
	if class == "" {
		wh, err := q.WarehouseClassOf(ctx, inventorydb.WarehouseClassOfParams{TenantID: tenantID, PropertyID: propertyID, ID: sale.WarehouseID})
		if err != nil {
			return false, orNotFound(err, notFound("WAREHOUSE_NOT_FOUND", "warehouse"))
		}
		if class, err = s.saleClass(ctx, tenantID, propertyID, sale.ProductID, wh); err != nil {
			return false, err
		}
	}
	dept, err := q.ChargeCodeDepartment(ctx, inventorydb.ChargeCodeDepartmentParams{PropertyID: propertyID, ID: sale.ChargeCodeID})
	if err != nil {
		return false, orNotFound(err, notFound("CHARGE_CODE_NOT_FOUND", "charge code"))
	}
	raw, err := json.Marshal(comps)
	if err != nil {
		return false, apperr.Internal(err)
	}
	_, err = q.InsertBanquetConsumptionRow(ctx, inventorydb.InsertBanquetConsumptionRowParams{TenantID: tenantID, PropertyID: propertyID, WarehouseID: sale.WarehouseID,
		BanquetPostingLineID: sale.PostingLineID, DepartmentID: dept, ProductID: sale.ProductID, Quantity: sale.Quantity, SaleClass: class, SaleDate: sale.BusinessDate, Components: raw, CreatedAt: s.clock.Now()})
	if errors.Is(err, pgx.ErrNoRows) { // the line is queued already: the hook is idempotent
		return false, nil
	}
	return err == nil, err
}
