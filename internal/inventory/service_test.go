package inventory_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/accounting"
	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

func fieldCode(t *testing.T, err error, field string) string {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok {
		t.Fatalf("got %v, want an *apperr.Error", err)
	}
	for _, f := range e.Fields {
		if f.Field == field {
			return f.Code
		}
	}
	t.Fatalf("no error on %s: %+v", field, e.Fields)
	return ""
}

func (f *fx) opening(uuid string, lines ...inventory.OpeningLineInput) inventory.OpeningInput {
	return inventory.OpeningInput{ClientUUID: uuid, WarehouseID: f.wh["MAIN"], Lines: lines}
}

func line(item, unit int64, qty, cost string) inventory.OpeningLineInput {
	return inventory.OpeningLineInput{ItemID: item, UnitID: unit, Qty: dec(qty), UnitCost: dec(cost)}
}

// ---------------------------------------------------------------- permissions

func TestTheMastersAreReadByWhoViewsAndChangedByWhoManages(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	viewer := f.User(t, f.tenantID, f.propID, auth.PermInventoryView)
	manager := f.User(t, f.tenantID, f.propID, auth.PermInventoryManage)
	nobody := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	if _, err := f.svc.ListItems(viewer, f.propID, inventory.ItemFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ListItems(nobody, f.propID, inventory.ItemFilter{}); err == nil {
		t.Fatal("a user with no inventory right read the items")
	} else {
		wantCode(t, err, "PERMISSION_DENIED")
	}
	_, err := f.svc.CreateUnit(viewer, f.propID, inventory.UnitInput{Code: "x", Name: "X"})
	wantCode(t, err, "PERMISSION_DENIED")
	if _, err := f.svc.CreateUnit(manager, f.propID, inventory.UnitInput{Code: "x", Name: "X"}); err != nil {
		t.Fatal(err)
	}
	// the manager does not post the opening stock: it has a right of its own
	_, err = f.svc.PostOpening(manager, f.propID, f.opening("22222222-2222-4222-8222-222222222222", line(f.item["BERAS"], f.unit["kg"], "1", "1000")))
	wantCode(t, err, "PERMISSION_DENIED")
	opener := f.User(t, f.tenantID, f.propID, auth.PermInventoryOpening)
	if _, err := f.svc.PostOpening(opener, f.propID, f.opening("22222222-2222-4222-8222-222222222222", line(f.item["BERAS"], f.unit["kg"], "1", "1000"))); err != nil {
		t.Fatal(err)
	}
}

func TestAnotherTenantsPropertyIsNotFound(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	other := f.Tenant(t, "XYZ")
	op := f.Property(t, other.ID, "SG")
	_, err := f.svc.ListItems(f.admin, op.ID, inventory.ItemFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = f.svc.GetItem(f.admin, f.propID, 999999)
	wantCode(t, err, "ITEM_NOT_FOUND")
}

// ---------------------------------------------------------------- the masters

func TestAUnitCodeIsUniqueAndAUnitInUseStaysOn(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	_, err := f.svc.CreateUnit(f.admin, f.propID, inventory.UnitInput{Code: "kg", Name: "Again"})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.svc.CreateUnit(f.admin, f.propID, inventory.UnitInput{Code: "bad code", Name: "X"})
	if fieldCode(t, err, "code") != "INVALID_FORMAT" {
		t.Fatal("a code with a space")
	}
	_, err = f.svc.UpdateUnit(f.admin, f.propID, f.unit["kg"], inventory.UnitInput{Name: "Kilo", IsActive: ptr(false)})
	wantCode(t, err, "UNIT_IN_USE")
	u, err := f.svc.UpdateUnit(f.admin, f.propID, f.unit["pcs"], inventory.UnitInput{Name: "Pieces", IsActive: ptr(false)})
	must(t, err)
	if u.IsActive {
		t.Fatal("the unit stays on")
	}
}

func TestACategoryNeedsTheRightAccounts(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	bad := func(in inventory.CategoryInput, field, code string) {
		t.Helper()
		_, err := f.svc.CreateCategory(f.admin, f.propID, in)
		if got := fieldCode(t, err, field); got != code {
			t.Fatalf("%s: %s, want %s", field, got, code)
		}
	}
	base := inventory.CategoryInput{Code: "OTH", Name: "Other", Kind: "OTHER", InventoryAccountID: f.accounts["1340"]}
	in := base
	in.InventoryAccountID = f.accounts["5210"] // an expense is not an inventory account
	bad(in, "inventory_account_id", "WRONG_ACCOUNT_TYPE")
	in = base
	in.InventoryAccountID = f.accounts["1110"] // an asset of another group
	bad(in, "inventory_account_id", "WRONG_ACCOUNT_TYPE")
	in = base
	in.InventoryAccountID = f.accounts["1300"] // the header "inventories" takes no postings
	bad(in, "inventory_account_id", "NOT_POSTABLE")
	in = base
	in.WasteAccountID = ptr(f.accounts["1340"]) // an asset is not a cost account
	bad(in, "waste_account_id", "WRONG_ACCOUNT_TYPE")
	in = base
	in.Kind = "MEAT"
	bad(in, "kind", "INVALID_VALUE")
	in = base
	in.Code = "bad code"
	bad(in, "code", "INVALID_FORMAT")
	in = base
	in.InventoryAccountID = 999999
	bad(in, "inventory_account_id", "NOT_FOUND")
	c, err := f.svc.CreateCategory(f.admin, f.propID, base)
	must(t, err)
	if c.InventoryAccountCode != "1340" || c.ItemCount != 0 {
		t.Fatalf("category: %+v", c)
	}
	_, err = f.svc.CreateCategory(f.admin, f.propID, base)
	wantCode(t, err, "CODE_TAKEN")
}

func TestTheAccountAndTheClassOfACategoryCannotChangeOnceItsItemsHaveMoved(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	food := func(account int64, kind string, v int32) inventory.CategoryInput {
		return inventory.CategoryInput{Code: "FOOD", Name: "Food", Kind: kind, InventoryAccountID: account, CogsAccountID: ptr(f.accounts["5210"]), Version: v}
	}
	// no movement yet: it may change
	c, err := f.svc.UpdateCategory(f.admin, f.propID, f.cat["FOOD"], food(f.accounts["1340"], "FOOD", 1))
	must(t, err)
	if c.InventoryAccountCode != "1340" || c.Version != 2 {
		t.Fatalf("changed: %+v", c)
	}
	_, err = f.svc.UpdateCategory(f.admin, f.propID, f.cat["FOOD"], food(f.accounts["1310"], "FOOD", 1))
	wantCode(t, err, "VERSION_CONFLICT") // someone else changed it
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "1", "1000")
	_, err = f.svc.UpdateCategory(f.admin, f.propID, f.cat["FOOD"], food(f.accounts["1310"], "FOOD", 2))
	wantCode(t, err, "CATEGORY_HAS_STOCK")
	_, err = f.svc.UpdateCategory(f.admin, f.propID, f.cat["FOOD"], food(f.accounts["1340"], "BEVERAGE", 2))
	wantCode(t, err, "CATEGORY_HAS_STOCK")
	if _, err := f.svc.UpdateCategory(f.admin, f.propID, f.cat["FOOD"], food(f.accounts["1340"], "FOOD", 2)); err != nil { // the name or a cost account may change
		t.Fatal(err)
	}
}

func TestAnItemHasItsStockUnitAsUnitOneToOneAndKeepsItOnceItHasStock(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	it, err := f.svc.GetItem(f.admin, f.propID, f.item["BERAS"])
	must(t, err)
	if len(it.Units) != 1 || it.Units[0].UnitID != f.unit["kg"] || it.Units[0].StockNum != 1 || it.Units[0].StockDen != 1 || !it.Units[0].IsPurchase || !it.Units[0].IsRecipe {
		t.Fatalf("units: %+v", it.Units)
	}
	_, err = f.svc.SetItemUnits(f.admin, f.propID, f.item["BERAS"], []inventory.ItemUnit{{UnitID: f.unit["g"], StockNum: 0, StockDen: 1}})
	if fieldCode(t, err, "units[0].stock_num") != "OUT_OF_RANGE" {
		t.Fatal("a conversion of zero")
	}
	_, err = f.svc.SetItemUnits(f.admin, f.propID, f.item["BERAS"], []inventory.ItemUnit{{UnitID: f.unit["g"], StockNum: 1, StockDen: 1000}, {UnitID: f.unit["g"], StockNum: 1, StockDen: 1000}})
	if fieldCode(t, err, "units[1].unit_id") != "DUPLICATE" {
		t.Fatal("a unit twice")
	}
	_, err = f.svc.SetItemUnits(f.admin, f.propID, f.item["BERAS"], []inventory.ItemUnit{{UnitID: f.unit["kg"], StockNum: 2, StockDen: 1}})
	if fieldCode(t, err, "units[0].stock_num") != "INVALID_VALUE" {
		t.Fatal("the stock unit must be 1/1")
	}
	it, err = f.svc.SetItemUnits(f.admin, f.propID, f.item["BERAS"], []inventory.ItemUnit{{UnitID: f.unit["g"], StockNum: 1, StockDen: 1000, IsRecipe: true, IsDefaultRecipe: true}})
	must(t, err)
	if len(it.Units) != 2 {
		t.Fatalf("units: %+v", it.Units)
	}
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "1", "1000")
	_, err = f.svc.UpdateItem(f.admin, f.propID, f.item["BERAS"], inventory.ItemInput{Name: "Beras", CategoryID: f.cat["FOOD"], StockUnitID: f.unit["g"], Version: 1})
	wantCode(t, err, "ITEM_HAS_STOCK")
	_, err = f.svc.UpdateItem(f.admin, f.propID, f.item["BERAS"], inventory.ItemInput{Name: "Beras", CategoryID: f.cat["BEV"], StockUnitID: f.unit["kg"], Version: 1})
	wantCode(t, err, "ITEM_HAS_STOCK")
	if _, err := f.svc.UpdateItem(f.admin, f.propID, f.item["BERAS"], inventory.ItemInput{Name: "Beras pandan", CategoryID: f.cat["FOOD"], StockUnitID: f.unit["kg"], Version: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestAnItemCodeAndABarcodeAreUniqueInTheProperty(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	_, err := f.svc.CreateItem(f.admin, f.propID, inventory.ItemInput{Code: "beras", Name: "Again", CategoryID: f.cat["FOOD"], StockUnitID: f.unit["kg"]})
	wantCode(t, err, "CODE_TAKEN")
	_, err = f.svc.CreateItem(f.admin, f.propID, inventory.ItemInput{Code: "A1", Name: "A", CategoryID: f.cat["FOOD"], StockUnitID: f.unit["kg"], Barcode: ptr("899")})
	must(t, err)
	_, err = f.svc.CreateItem(f.admin, f.propID, inventory.ItemInput{Code: "A2", Name: "A2", CategoryID: f.cat["FOOD"], StockUnitID: f.unit["kg"], Barcode: ptr("899")})
	wantCode(t, err, "BARCODE_TAKEN")
	_, err = f.svc.CreateItem(f.admin, f.propID, inventory.ItemInput{Code: "A3", Name: "", CategoryID: 999, StockUnitID: 999})
	if fieldCode(t, err, "category_id") != "NOT_FOUND" || fieldCode(t, err, "stock_unit_id") != "NOT_FOUND" || fieldCode(t, err, "name") != "INVALID_VALUE" {
		t.Fatal("the fields")
	}
}

func TestAWarehouseOfAnOutletNamesTheOutletAndAWarehouseWithStockStaysOn(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	_, err := f.svc.CreateWarehouse(f.admin, f.propID, inventory.WarehouseInput{Code: "RESTO", Name: "Resto", Kind: "OUTLET", CostClass: "FOOD"})
	if fieldCode(t, err, "outlet_id") != "INVALID_VALUE" {
		t.Fatal("an outlet warehouse with no outlet")
	}
	_, err = f.svc.CreateWarehouse(f.admin, f.propID, inventory.WarehouseInput{Code: "RESTO", Name: "Resto", Kind: "OUTLET", OutletID: ptr(int64(999)), CostClass: "FOOD"})
	if fieldCode(t, err, "outlet_id") != "NOT_FOUND" {
		t.Fatal("an outlet that does not exist")
	}
	_, err = f.svc.CreateWarehouse(f.admin, f.propID, inventory.WarehouseInput{Code: "MAIN", Name: "Again", Kind: "MAIN"})
	wantCode(t, err, "CODE_TAKEN")
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "1", "1000")
	_, err = f.svc.UpdateWarehouse(f.admin, f.propID, f.wh["MAIN"], inventory.WarehouseInput{Name: "Main", Kind: "MAIN", CostClass: "OTHER", IsActive: ptr(false), Version: 1})
	wantCode(t, err, "WAREHOUSE_HAS_STOCK")
	w, err := f.svc.UpdateWarehouse(f.admin, f.propID, f.wh["BAR"], inventory.WarehouseInput{Name: "Bar", Kind: "DEPARTMENT", CostClass: "BEVERAGE", IsActive: ptr(false), Version: 1})
	must(t, err)
	if w.IsActive {
		t.Fatal("the empty warehouse stays on")
	}
}

func TestAnOutletHasOneActiveWarehouse(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	outlet := f.outlet(t)
	_, err := f.svc.CreateWarehouse(f.admin, f.propID, inventory.WarehouseInput{Code: "RESTO", Name: "Resto", Kind: "OUTLET", OutletID: &outlet, CostClass: "FOOD"})
	must(t, err)
	_, err = f.svc.CreateWarehouse(f.admin, f.propID, inventory.WarehouseInput{Code: "RESTO2", Name: "Resto 2", Kind: "OUTLET", OutletID: &outlet, CostClass: "FOOD"})
	wantCode(t, err, "OUTLET_HAS_WAREHOUSE")
}

// outlet makes a POS outlet straight in the table (the POS service is not under test here).
func (f *fx) outlet(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	var dept, profile, list, id int64
	must(t, f.Pool.QueryRow(ctx, `SELECT id FROM departments WHERE property_id = $1 AND code = 'FB'`, f.propID).Scan(&dept))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO pos_rule_profiles (tenant_id, property_id, code, name) VALUES ($1, $2, 'P1', 'Profile') RETURNING id`, f.tenantID, f.propID).Scan(&profile))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO pos_price_lists (tenant_id, property_id, code, name) VALUES ($1, $2, 'L1', 'List') RETURNING id`, f.tenantID, f.propID).Scan(&list))
	must(t, f.Pool.QueryRow(ctx, `INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id)
		VALUES ($1, $2, 'RESTO', 'Resto', 'RESTAURANT', 'PRINT', $3, $4, $5) RETURNING id`, f.tenantID, f.propID, dept, profile, list).Scan(&id))
	return id
}

func TestMinimumLevelsAreReplacedAsAWholeAndMarkTheItem(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "5", "50000")
	_, err := f.svc.SetLevels(f.admin, f.propID, f.wh["MAIN"], []inventory.Level{{ItemID: f.item["BERAS"], MinQty: dec("10")}, {ItemID: f.item["BERAS"], MinQty: dec("5")}})
	if fieldCode(t, err, "levels[1].item_id") != "DUPLICATE" {
		t.Fatal("an item twice")
	}
	_, err = f.svc.SetLevels(f.admin, f.propID, f.wh["MAIN"], []inventory.Level{{ItemID: f.item["BERAS"], MinQty: dec("10"), MaxQty: ptr(dec("5"))}})
	if fieldCode(t, err, "levels[0].max_qty") != "OUT_OF_RANGE" {
		t.Fatal("a maximum below the minimum")
	}
	_, err = f.svc.SetLevels(f.admin, f.propID, f.wh["MAIN"], []inventory.Level{{ItemID: 999999, MinQty: dec("1")}})
	if fieldCode(t, err, "levels[0].item_id") != "NOT_FOUND" {
		t.Fatal("an item that does not exist")
	}
	lv, err := f.svc.SetLevels(f.admin, f.propID, f.wh["MAIN"], []inventory.Level{{ItemID: f.item["BERAS"], MinQty: dec("10"), MaxQty: ptr(dec("50"))}})
	must(t, err)
	if len(lv) != 1 || lv[0].ItemCode != "BERAS" {
		t.Fatalf("levels: %+v", lv)
	}
	page, err := f.svc.ListItems(f.admin, f.propID, inventory.ItemFilter{})
	must(t, err)
	var beras *inventory.Item
	for i := range page.Items {
		if page.Items[i].Code == "BERAS" {
			beras = &page.Items[i]
		}
	}
	if beras == nil || !beras.BelowMin || beras.BelowZero || !beras.QtyOnHand.Equal(dec("5")) || len(beras.Stock) != 1 || !beras.Stock[0].MinQty.Equal(dec("10")) {
		t.Fatalf("beras: %+v", beras)
	}
	below, err := f.svc.ListItems(f.admin, f.propID, inventory.ItemFilter{BelowMin: true})
	must(t, err)
	if below.Total != 1 || len(below.Items) != 1 {
		t.Fatalf("below minimum: %d", below.Total)
	}
	if len(page.Warehouses) != 2 || page.Method != "AVERAGE" || !page.Locked || !page.TotalValue.Equal(dec("50000")) {
		t.Fatalf("page: %+v", page)
	}
}

func TestTheListOfItemsSearchesAndFilters(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	p, err := f.svc.ListItems(f.admin, f.propID, inventory.ItemFilter{Q: "sir"})
	must(t, err)
	if p.Total != 1 || p.Items[0].Code != "SIRUP" {
		t.Fatalf("search: %+v", p.Items)
	}
	p, err = f.svc.ListItems(f.admin, f.propID, inventory.ItemFilter{CategoryID: ptr(f.cat["FOOD"])})
	must(t, err)
	if p.Total != 1 || p.Items[0].Code != "BERAS" || p.ByCategory[f.cat["FOOD"]] != 1 || p.ByCategory[f.cat["BEV"]] != 1 {
		t.Fatalf("category: %+v", p)
	}
	p, err = f.svc.ListItems(f.admin, f.propID, inventory.ItemFilter{Limit: 1})
	must(t, err)
	if p.Total != 2 || len(p.Items) != 1 {
		t.Fatalf("page: %d %d", p.Total, len(p.Items))
	}
}

// ---------------------------------------------------------------- settings and the lock of the method

func TestTheCostingMethodChangesUntilTheFirstMovementAndIsLockedAfterIt(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	st, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if st.CostingMethod != "AVERAGE" || st.CostingLockedOn != nil || !st.Exists {
		t.Fatalf("settings: %+v", st)
	}
	in := inventory.SettingsInput{CostingMethod: "FIFO", PriceTolerancePct: st.PriceTolerancePct, QtyTolerancePct: st.QtyTolerancePct, OverReceiptPct: st.OverReceiptPct, PoRequiresPr: st.PoRequiresPr,
		CountBlocksNightAudit: st.CountBlocksNightAudit, CountBlind: st.CountBlind, Version: st.Version}
	st, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	must(t, err)
	if st.CostingMethod != "FIFO" || st.Version != in.Version+1 {
		t.Fatalf("after the change: %+v", st)
	}
	_, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	wantCode(t, err, "VERSION_CONFLICT")
	in.Version = st.Version
	in.CostingMethod = "LIFO"
	_, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	if fieldCode(t, err, "costing_method") != "INVALID_VALUE" {
		t.Fatal("LIFO")
	}
	in.CostingMethod = "AVERAGE"
	in.PriceTolerancePct = dec("101")
	_, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	if fieldCode(t, err, "price_tolerance_pct") != "OUT_OF_RANGE" {
		t.Fatal("a tolerance above 100")
	}
	in.PriceTolerancePct = dec("2.5")
	st, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	must(t, err)
	// the first movement locks the method
	f.method = "AVERAGE"
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "1", "1000")
	locked, err := f.svc.GetSettings(f.admin, f.propID)
	must(t, err)
	if locked.CostingLockedOn == nil || locked.CostingLockedOn.String() != "2026-09-30" {
		t.Fatalf("locked on %v", locked.CostingLockedOn)
	}
	in.Version, in.CostingMethod = locked.Version, "FIFO"
	_, err = f.svc.UpdateSettings(f.admin, f.propID, in)
	wantCode(t, err, "COSTING_METHOD_LOCKED")
	// the same method and the other settings are still the manager's
	in.CostingMethod, in.CountBlind = "AVERAGE", false
	if _, err := f.svc.UpdateSettings(f.admin, f.propID, in); err != nil {
		t.Fatal(err)
	}
	// the database says the same to anybody who tries it behind the service
	_, err = f.Pool.Exec(context.Background(), `UPDATE inventory_settings SET costing_method = 'FIFO' WHERE property_id = $1`, f.propID)
	if err == nil {
		t.Fatal("the database accepted a change of the method after the first movement")
	}
}

// The trigger writes the settings row once, at the first movement; a later movement does not touch it (document 31 6).
func TestALockedMethodRowIsNotWrittenByLaterMovements(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "1", "1000")
	var before, after string
	q := `SELECT xmin::text FROM inventory_settings WHERE property_id = $1`
	must(t, f.Pool.QueryRow(context.Background(), q, f.propID).Scan(&before))
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "BERAS"), "1", "1000")
	f.in(t, inventory.KindReceipt, f.pair("BAR", "SIRUP"), "1", "1000")
	must(t, f.Pool.QueryRow(context.Background(), q, f.propID).Scan(&after))
	if before != after {
		t.Fatalf("the settings row was written again: xmin %s -> %s", before, after)
	}
}

// Two first movements at once, in two transactions: both go in, and the date is stamped once.
func TestTwoFirstMovementsAtOnceBothGoInAndTheLockIsStampedOnce(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	for _, c := range []struct{ w, i string }{{"MAIN", "BERAS"}, {"BAR", "SIRUP"}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			f.in(t, inventory.KindReceipt, f.pair(c.w, c.i), "1", "1000")
		}()
	}
	close(barrier)
	wg.Wait()
	var n int
	var on *civil.Date
	must(t, f.Pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM inv_movements), costing_locked_on FROM inventory_settings WHERE property_id = $1`, f.propID).Scan(&n, &on))
	if n != 2 || on == nil {
		t.Fatalf("%d movements, locked on %v", n, on)
	}
}

// ---------------------------------------------------------------- the opening stock

func TestTheOpeningStockBooksEachLineAgainstTheOpeningEquity(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	o, err := f.svc.PostOpening(f.admin, f.propID, f.opening("33333333-3333-4333-8333-333333333333",
		line(f.item["BERAS"], f.unit["kg"], "120", "17360"), line(f.item["SIRUP"], f.unit["botol"], "36", "49000")))
	must(t, err)
	if o.Number != "OPN-000001" || len(o.Lines) != 2 || !o.TotalValue.Equal(dec("3847200")) || o.BusinessDate.String() != "2026-09-30" {
		t.Fatalf("opening: %+v", o)
	}
	f.wantBal(t, f.pair("MAIN", "BERAS"), "120", "2083200")
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "36", "1764000")
	rows, err := f.Pool.Query(context.Background(), `SELECT i.code, m.kind, m.gl_amount, m.gl_debit_account_id, m.gl_credit_account_id, m.item_class, m.cost_class, m.source_type
		FROM inv_movements m JOIN inv_items i ON i.id = m.item_id ORDER BY m.id`)
	must(t, err)
	defer rows.Close()
	want := map[string][3]int64{"BERAS": {2083200, f.accounts["1310"], f.accounts["3900"]}, "SIRUP": {1764000, f.accounts["1320"], f.accounts["3900"]}}
	n := 0
	for rows.Next() {
		var code, kind, ic, cc, src string
		var amount int64
		var d, c int64
		must(t, rows.Scan(&code, &kind, &amount, &d, &c, &ic, &cc, &src))
		w := want[code]
		if kind != "OPENING" || amount != w[0] || d != w[1] || c != w[2] || src != "INV_OPENING" {
			t.Fatalf("%s: %s %d %d %d %s", code, kind, amount, d, c, src)
		}
		if (code == "BERAS" && (ic != "FOOD" || cc != "FOOD")) || (code == "SIRUP" && (ic != "BEVERAGE" || cc != "BEVERAGE")) {
			t.Fatalf("%s: class %s / %s", code, ic, cc)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("%d movements", n)
	}
	f.consistent(t)
	// the card
	card, err := f.svc.StockCard(f.admin, f.propID, f.wh["MAIN"], f.item["SIRUP"], nil, nil)
	must(t, err)
	if len(card.Lines) != 1 || card.Lines[0].DocumentNumber != "OPN-000001" || !card.ClosingQty.Equal(dec("36")) || !card.Lines[0].AverageCost.Equal(dec("49000")) {
		t.Fatalf("card: %+v", card)
	}
}

func TestAnOpeningRequestIsSafeToRepeat(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	in := f.opening("44444444-4444-4444-8444-444444444444", line(f.item["BERAS"], f.unit["kg"], "10", "1000"))
	a, err := f.svc.PostOpening(f.admin, f.propID, in)
	must(t, err)
	b, err := f.svc.PostOpening(f.admin, f.propID, in)
	must(t, err)
	if a.ID != b.ID || a.Number != b.Number {
		t.Fatalf("the second request made a new opening: %+v %+v", a, b)
	}
	f.wantBal(t, f.pair("MAIN", "BERAS"), "10", "10000")
	n := f.Count(t, `SELECT count(*) FROM inv_movements`)
	if n != 1 {
		t.Fatalf("%d movements", n)
	}
}

func TestAnItemHasOneOpeningPerWarehouse(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	_, err := f.svc.PostOpening(f.admin, f.propID, f.opening("55555555-5555-4555-8555-555555555555", line(f.item["BERAS"], f.unit["kg"], "10", "1000")))
	must(t, err)
	_, err = f.svc.PostOpening(f.admin, f.propID, f.opening("66666666-6666-4666-8666-666666666666", line(f.item["BERAS"], f.unit["kg"], "5", "1000")))
	if fieldCode(t, err, "lines[0].item_id") != "OPENING_EXISTS" {
		t.Fatal("a second opening of the item")
	}
	// another warehouse is another opening
	_, err = f.svc.PostOpening(f.admin, f.propID, inventory.OpeningInput{ClientUUID: "77777777-7777-4777-8777-777777777777", WarehouseID: f.wh["BAR"],
		Lines: []inventory.OpeningLineInput{line(f.item["BERAS"], f.unit["kg"], "5", "1000")}})
	must(t, err)
}

func TestAnOpeningIsAllOrNothing(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	f.newItem(t, "TEAB", "Tea", "BEV", "kg")
	f.newItem(t, "GULA", "Gula", "FOOD", "kg")
	_, err := f.svc.PostOpening(f.admin, f.propID, f.opening("88888888-8888-4888-8888-888888888888",
		line(f.item["BERAS"], f.unit["kg"], "10", "1000"), line(f.item["SIRUP"], f.unit["kg"], "1", "1000"), line(f.item["BERAS"], f.unit["kg"], "1", "1"), line(999999, f.unit["kg"], "1", "1"),
		line(f.item["TEAB"], f.unit["kg"], "0", "1"), line(f.item["GULA"], f.unit["kg"], "1", "-1")))
	if fieldCode(t, err, "lines[1].unit_id") != "NOT_FOUND" || fieldCode(t, err, "lines[2].item_id") != "DUPLICATE" || fieldCode(t, err, "lines[3].item_id") != "NOT_FOUND" ||
		fieldCode(t, err, "lines[4].qty") != "INVALID_VALUE" || fieldCode(t, err, "lines[5].unit_cost") != "INVALID_VALUE" {
		t.Fatalf("the errors: %v", err)
	}
	if n := f.Count(t, `SELECT (SELECT count(*) FROM inv_movements) + (SELECT count(*) FROM inv_openings) + (SELECT count(*) FROM inv_balances WHERE qty <> 0)`); n != 0 {
		t.Fatalf("%d rows were written by a refused opening", n)
	}
	_, err = f.svc.PostOpening(f.admin, f.propID, inventory.OpeningInput{ClientUUID: "not-a-uuid", WarehouseID: f.wh["MAIN"], Lines: []inventory.OpeningLineInput{line(f.item["BERAS"], f.unit["kg"], "1", "1")}})
	if fieldCode(t, err, "client_uuid") != "INVALID_VALUE" {
		t.Fatal("a client uuid that is not one")
	}
	_, err = f.svc.PostOpening(f.admin, f.propID, inventory.OpeningInput{ClientUUID: "99999999-9999-4999-8999-999999999999", WarehouseID: f.wh["MAIN"]})
	if fieldCode(t, err, "lines") != "OUT_OF_RANGE" {
		t.Fatal("no lines")
	}
}

func TestAnOpeningInAMoneyOfTwoDecimalsKeepsTheCents(t *testing.T) {
	f := newFx(t, "FIFO", 2)
	o, err := f.svc.PostOpening(f.admin, f.propID, f.opening("12121212-1212-4212-8212-121212121212", line(f.item["BERAS"], f.unit["kg"], "3", "3.33")))
	must(t, err)
	if !o.TotalValue.Equal(dec("9.99")) {
		t.Fatalf("total %s", o.TotalValue)
	}
	_, err = f.svc.PostOpening(f.admin, f.propID, f.opening("13131313-1313-4313-8313-131313131313", line(f.item["SIRUP"], f.unit["botol"], "3", "3.333")))
	if fieldCode(t, err, "lines[0].unit_cost") != "INVALID_VALUE" {
		t.Fatal("a cost with a third decimal in a currency of two")
	}
	if l := f.layers(t, f.pair("MAIN", "BERAS")); len(l) != 1 || !l[0].qty.Equal(dec("3")) || !l[0].value.Equal(dec("9.99")) {
		t.Fatalf("FIFO layers: %+v", l)
	}
}

func TestTheOpeningIsRefusedWhileTheInventoryAccountsHoldABalance(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	// an accountant booked a purchase by hand on 1310 before the module started
	_, err := f.Accounting.PostManual(f.admin, f.propID, accountingManual(f), "hand-1")
	must(t, err)
	_, err = f.svc.PostOpening(f.admin, f.propID, f.opening("14141414-1414-4414-8414-141414141414", line(f.item["BERAS"], f.unit["kg"], "1", "1000")))
	wantCode(t, err, "INVENTORY_GL_NOT_EMPTY")
	if n := f.Count(t, `SELECT count(*) FROM inv_movements`); n != 0 {
		t.Fatalf("%d movements", n)
	}
}

func TestTheOpeningNeedsTheEquityAccountAndAnActiveWarehouse(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	_, err := f.Pool.Exec(context.Background(), `UPDATE gl_accounts SET is_active = false WHERE property_id = $1 AND code = '3900'`, f.propID)
	must(t, err)
	_, err = f.svc.PostOpening(f.admin, f.propID, f.opening("15151515-1515-4515-8515-151515151515", line(f.item["BERAS"], f.unit["kg"], "1", "1000")))
	wantCode(t, err, "OPENING_ACCOUNT_MISSING")
	_, err = f.Pool.Exec(context.Background(), `UPDATE gl_accounts SET is_active = true WHERE property_id = $1 AND code = '3900'`, f.propID)
	must(t, err)
	_, err = f.svc.UpdateWarehouse(f.admin, f.propID, f.wh["BAR"], inventory.WarehouseInput{Name: "Bar", Kind: "DEPARTMENT", CostClass: "BEVERAGE", IsActive: ptr(false), Version: 1})
	must(t, err)
	_, err = f.svc.PostOpening(f.admin, f.propID, inventory.OpeningInput{ClientUUID: "16161616-1616-4616-8616-161616161616", WarehouseID: f.wh["BAR"], Lines: []inventory.OpeningLineInput{line(f.item["BERAS"], f.unit["kg"], "1", "1")}})
	if fieldCode(t, err, "warehouse_id") != "INACTIVE" {
		t.Fatal("an inactive warehouse")
	}
}

func TestTheOpeningIsDatedTheOpenDayAndNeedsOne(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	_, err := f.Pool.Exec(context.Background(), `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, f.propID)
	must(t, err)
	_, err = f.svc.PostOpening(f.admin, f.propID, f.opening("17171717-1717-4717-8717-171717171717", line(f.item["BERAS"], f.unit["kg"], "1", "1000")))
	if err == nil {
		t.Fatal("an opening with no open business day")
	}
}

// accountingManual is a hand-made journal that debits an inventory account: a purchase booked by hand before the module started.
func accountingManual(f *fx) accounting.ManualInput {
	return accounting.ManualInput{Date: day("2026-09-30"), Description: "Stock bought by hand", Lines: []accounting.LineInput{
		{AccountID: f.accounts["1310"], Debit: dec("500000")}, {AccountID: f.accounts["3900"], Credit: dec("500000")}}}
}
