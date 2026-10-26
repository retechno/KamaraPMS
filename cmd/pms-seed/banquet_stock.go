package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"kamarapms/internal/banquet"
	"kamarapms/internal/platform/db"
)

// The stock part of `pms-seed banquet` (docs/architecture/36-banquet.md 4.9, PR 4): the lunch and the coffee break of the banquet are linked to a product of the POS demo that has a recipe, and the banquet
// takes its goods from the kitchen warehouse, so that posting a session deducts the stock. It needs the POS demo and its recipes (`pms-seed pos`, then `pms-seed inventory`) and says so when they are missing;
// an item that is linked already, and a banquet that has a warehouse already, are left as they are, so a second run changes nothing.

// bqStockLinks are the items of the banquet and the product of the POS demo each one is sold as.
var bqStockLinks = []struct{ item, product string }{{"LUNCH", "NASGOR"}, {"COFFEEBRK", "KOPIBALI"}}

// bqStockWarehouse is the warehouse of the demo kitchen (made by `pms-seed inventory`).
const bqStockWarehouse = "DAPUR"

// seedBanquetStock does the work; the return value is the number of rows changed (or that would be).
func seedBanquetStock(ctx context.Context, svc *banquet.Service, q db.DBTX, pid int64, dryRun bool, out io.Writer) (int, error) {
	var warehouse int64
	if err := q.QueryRow(ctx, `SELECT id FROM inv_warehouses WHERE property_id = $1 AND code = $2`, pid, bqStockWarehouse).Scan(&warehouse); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = fmt.Fprintf(out, "stock: skipped, the warehouse %s is not there (run pms-seed inventory first)\n", bqStockWarehouse)
			return 0, nil
		}
		return 0, err
	}
	made := 0
	items, err := svc.ListItems(ctx, pid, nil)
	if err != nil {
		return 0, err
	}
	have := map[string]banquet.Item{}
	for _, it := range items {
		have[it.Code] = it
	}
	for _, l := range bqStockLinks {
		it, ok := have[l.item]
		if !ok {
			_, _ = fmt.Fprintf(out, "item %s: stock link skipped, the item is not there\n", l.item)
			continue
		}
		if it.PosProductID != nil {
			_, _ = fmt.Fprintf(out, "item %s is linked to a product\n", l.item)
			continue
		}
		var product int64
		err := q.QueryRow(ctx, `SELECT p.id FROM pos_products p WHERE p.property_id = $1 AND p.code = $2
			AND EXISTS (SELECT 1 FROM pos_recipe_lines r WHERE r.property_id = p.property_id AND r.product_id = p.id)`, pid, l.product).Scan(&product)
		if errors.Is(err, pgx.ErrNoRows) {
			_, _ = fmt.Fprintf(out, "item %s: stock link skipped, the product %s has no recipe (run pms-seed pos and pms-seed inventory first)\n", l.item, l.product)
			continue
		}
		if err != nil {
			return 0, err
		}
		_, _ = fmt.Fprintf(out, "item %s: to link to the product %s\n", l.item, l.product)
		made++
		if dryRun {
			continue
		}
		active := it.IsActive
		if _, err := svc.UpdateItem(ctx, pid, it.ID, banquet.ItemInput{Code: it.Code, Name: it.Name, Kind: it.Kind, UnitBasis: it.UnitBasis, ChargeCodeID: it.ChargeCodeID,
			ListPrice: decimal.RequireFromString(it.ListPrice.String()), FbClass: it.FbClass, PosProductID: &product, Description: it.Description, IsActive: &active}); err != nil {
			return 0, fmt.Errorf("item %s: %w", l.item, err)
		}
	}

	s, err := svc.GetSettings(ctx, pid)
	if err != nil {
		return 0, err
	}
	if s.WarehouseID != nil {
		_, _ = fmt.Fprintln(out, "banquet warehouse is set")
		return made, nil
	}
	_, _ = fmt.Fprintf(out, "banquet warehouse: to set to %s\n", bqStockWarehouse)
	made++
	if dryRun {
		return made, nil
	}
	// the hours of operation and the money settings are sent as they are: an input that leaves the hours out clears them
	if _, err := svc.PutSettings(ctx, pid, banquet.SettingsInput{TentativeConflictMode: s.TentativeConflictMode, OperatingFrom: s.OperatingFrom, OperatingTo: s.OperatingTo, PostingMode: s.PostingMode,
		DefaultPriceMode: s.DefaultPriceMode, MaxDiscountPctNoApproval: &s.MaxDiscountPctNoApproval, WarehouseID: banquet.NullableID{Set: true, ID: &warehouse}}); err != nil {
		return 0, fmt.Errorf("banquet warehouse: %w", err)
	}
	return made, nil
}
