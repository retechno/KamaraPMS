package main

import (
	"fmt"

	"github.com/shopspring/decimal"

	"kamarapms/internal/departments"
	"kamarapms/internal/inventory"
	"kamarapms/internal/pos"
)

// pms-seed inventory: demo data of the inventory (docs/architecture/35-inventory.md, PR 1) for a DEVELOPMENT database, through the services of the application. Safe to run again: what
// exists (by code) is left alone, and the opening stock of a warehouse is posted once (its client uuid is fixed, so a second run gets the first opening back).
//
//	settings    the costing method (average), when the property never saved the settings
//	units       ml, g, kg, l, botol, karton, pcs, pak, karung, rol
//	categories  MAKANAN (1310), MINUMAN (1320), PERLENGKAPAN (1330, housekeeping and operating supplies), LAINNYA (1340)
//	warehouses  Gudang Utama; the kitchen of the restaurant and the bar tied to the outlets RESTO and BAR of `pms-seed pos` when they exist (else departments); Gudang Housekeeping
//	items       about forty, in Indonesian, with the units they are bought and used in (sirup: karton = 12 botol, 1 ml = 1/650 botol)
//	opening     the stock of 1 September: one document per warehouse
//
//	purchasing  (PR 2) a supplier, a purchase request that waits at level 2 of the approval chain, and one that was ordered and received in part: see inventory_purchasing.go
//
//	movements   (PR 3) a requisition that waits at level 2, a transfer, a use, a waste, the plan of the amenities and an open count: see inventory_movements.go
//
//	recipes     (PR 4) the outlets sell from their warehouses and the demo products of the POS have a recipe: see inventory_recipes.go
//
// The reports come with the last PR of the module.

type invItem struct {
	code, name, category, unit string
	ref                        string // reference cost per stock unit, when there is one
	extra                      []inventory.ItemUnit
	// the opening stock: the warehouse code, the quantity in the stock unit and the cost of one stock unit
	open []invOpen
}

type invOpen struct{ warehouse, qty, cost string }

// u builds a unit of an item by the codes of the units (resolved when the seed runs): num/den stock units to one of it.
func u(code string, num, den int32, purchase, recipe bool) inventory.ItemUnit {
	return inventory.ItemUnit{UnitCode: code, StockNum: num, StockDen: den, IsPurchase: purchase, IsRecipe: recipe, IsDefaultPurchase: purchase, IsDefaultRecipe: recipe}
}

var invUnits = [][2]string{{"ml", "Mililiter"}, {"g", "Gram"}, {"kg", "Kilogram"}, {"l", "Liter"}, {"botol", "Botol"}, {"karton", "Karton"}, {"pcs", "Buah"}, {"pak", "Pak"}, {"karung", "Karung"}, {"rol", "Rol"}}

var invCategories = []struct{ code, name, kind, inventory, cogs, issue string }{
	{"MAKANAN", "Bahan makanan", "FOOD", "1310", "5210", ""},
	{"MINUMAN", "Bahan minuman", "BEVERAGE", "1320", "5220", ""},
	{"PERLENGKAPAN", "Perlengkapan operasional", "SUPPLIES", "1330", "", "5150"}, //nolint:misspell // Indonesian
	{"LAINNYA", "Lain-lain (ATK, suku cadang)", "OTHER", "1340", "", "6160"},
}

var invWarehouses = []struct{ code, name, kind, outlet, dept, class string }{
	{"UTAMA", "Gudang Utama", "MAIN", "", "", "OTHER"},
	{"DAPUR", "Gudang Dapur Restoran", "OUTLET", "RESTO", "FB", "FOOD"},
	{"BAR", "Gudang Bar", "OUTLET", "BAR", "FB", "BEVERAGE"},
	{"HK", "Gudang Housekeeping", "DEPARTMENT", "", "ROOMS", "OTHER"},
}

var invItems = []invItem{
	{code: "BERAS", name: "Beras pandan wangi", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("karung", 25, 1, true, false), u("g", 1, 1000, false, true)},
		open: []invOpen{{"UTAMA", "120", "17360"}, {"DAPUR", "18.4", "17360"}}},
	{code: "AYAM", name: "Daging ayam fillet", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "8", "72938"}, {"DAPUR", "2.1", "72938"}}},
	{code: "MGRNG", name: "Minyak goreng 2 L", category: "MAKANAN", unit: "l", extra: []inventory.ItemUnit{u("ml", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "44", "23165"}, {"DAPUR", "6.5", "23165"}}},
	{code: "UDANG", name: "Udang segar", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "6", "98000"}}},
	{code: "IKAN", name: "Ikan kakap segar", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "9", "85000"}}},
	{code: "TELUR", name: "Telur ayam", category: "MAKANAN", unit: "pcs", open: []invOpen{{"UTAMA", "300", "2300"}, {"DAPUR", "60", "2300"}}},
	{code: "BWMERAH", name: "Bawang merah", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "10", "42000"}, {"DAPUR", "2", "42000"}}},
	{code: "BWPUTIH", name: "Bawang putih", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "6", "38000"}}},
	{code: "CABAI", name: "Cabai rawit", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "3", "65000"}}},
	{code: "KECAP", name: "Kecap manis 600 ml", category: "MAKANAN", unit: "botol", extra: []inventory.ItemUnit{u("karton", 12, 1, true, false), u("ml", 1, 600, false, true)}, open: []invOpen{{"UTAMA", "24", "21500"}, {"DAPUR", "4", "21500"}}},
	{code: "GULA", name: "Gula pasir", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "40", "16500"}, {"DAPUR", "5", "16500"}}},
	{code: "GARAM", name: "Garam dapur", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "15", "9000"}}},
	{code: "TEPUNG", name: "Tepung terigu", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "25", "12500"}}},
	{code: "MIE", name: "Mie telur kering", category: "MAKANAN", unit: "pak", open: []invOpen{{"UTAMA", "50", "9500"}, {"DAPUR", "10", "9500"}}},
	{code: "SANTAN", name: "Santan kelapa 1 L", category: "MAKANAN", unit: "l", extra: []inventory.ItemUnit{u("ml", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "20", "28000"}}},
	{code: "LIME", name: "Jeruk nipis", category: "MAKANAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "5", "24000"}, {"BAR", "2", "24000"}}},
	{code: "SIRUP", name: "Sirup gula aren 650 ml", category: "MINUMAN", unit: "botol", extra: []inventory.ItemUnit{u("karton", 12, 1, true, false), u("ml", 1, 650, false, true)}, open: []invOpen{{"UTAMA", "36", "49000"}, {"BAR", "20", "49000"}}},
	{code: "TEHCL", name: "Teh celup hitam", category: "MINUMAN", unit: "pcs", extra: []inventory.ItemUnit{u("pak", 25, 1, true, false)}, open: []invOpen{{"UTAMA", "1200", "835"}, {"BAR", "140", "835"}}}, //nolint:misspell // Indonesian: teh is tea
	{code: "KOPI", name: "Biji kopi arabika Bali", category: "MINUMAN", unit: "kg", extra: []inventory.ItemUnit{u("g", 1, 1000, false, true)}, open: []invOpen{{"UTAMA", "8", "210000"}, {"BAR", "2", "210000"}}},
	{code: "SUSU", name: "Susu cair UHT 1 L", category: "MINUMAN", unit: "l", extra: []inventory.ItemUnit{u("ml", 1, 1000, false, true), u("karton", 12, 1, true, false)}, open: []invOpen{{"UTAMA", "48", "19500"}, {"BAR", "12", "19500"}}},
	{code: "AIRMIN", name: "Air mineral 600 ml", category: "MINUMAN", unit: "botol", extra: []inventory.ItemUnit{u("karton", 24, 1, true, false)}, open: []invOpen{{"UTAMA", "480", "2500"}, {"BAR", "96", "2500"}, {"HK", "960", "2500"}}},
	{code: "ES", name: "Es batu kristal 10 kg", category: "MINUMAN", unit: "kg", open: []invOpen{{"UTAMA", "50", "6500"}}},
	{code: "BIR", name: "Bir pilsner 330 ml", category: "MINUMAN", unit: "botol", extra: []inventory.ItemUnit{u("karton", 24, 1, true, false)}, open: []invOpen{{"UTAMA", "72", "21000"}, {"BAR", "48", "21000"}}},
	{code: "ANGGUR", name: "Anggur putih 750 ml", category: "MINUMAN", unit: "botol", extra: []inventory.ItemUnit{u("karton", 6, 1, true, false), u("ml", 1, 750, false, true)}, open: []invOpen{{"UTAMA", "24", "126000"}, {"BAR", "6", "126000"}}},
	{code: "ARAK", name: "Arak Bali 700 ml", category: "MINUMAN", unit: "botol", extra: []inventory.ItemUnit{u("ml", 1, 700, false, true)}, open: []invOpen{{"UTAMA", "12", "88000"}, {"BAR", "4", "88000"}}},
	{code: "SODA", name: "Air soda 330 ml", category: "MINUMAN", unit: "botol", extra: []inventory.ItemUnit{u("karton", 24, 1, true, false)}, open: []invOpen{{"UTAMA", "96", "5200"}, {"BAR", "24", "5200"}}},
	{code: "SAMPO", name: "Sampo amenities 30 ml", category: "PERLENGKAPAN", unit: "pcs", extra: []inventory.ItemUnit{u("karton", 100, 1, true, false)}, open: []invOpen{{"UTAMA", "2400", "3250"}, {"HK", "310", "3250"}}},
	{code: "SABUN", name: "Sabun batang 25 g", category: "PERLENGKAPAN", unit: "pcs", extra: []inventory.ItemUnit{u("karton", 100, 1, true, false)}, open: []invOpen{{"UTAMA", "180", "2960"}, {"HK", "42", "2960"}}},
	{code: "SIKAT", name: "Sikat gigi amenities", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "800", "2100"}, {"HK", "620", "2100"}}},
	{code: "PASTA", name: "Pasta gigi 10 g", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "800", "1800"}, {"HK", "580", "1800"}}},
	{code: "TISU", name: "Tisu wajah 250 lembar", category: "PERLENGKAPAN", unit: "pak", open: []invOpen{{"UTAMA", "96", "16000"}, {"HK", "24", "16000"}}},
	{code: "TEHKMR", name: "Teh celup kamar", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "1500", "835"}, {"HK", "1200", "835"}}}, //nolint:misspell // Indonesian: teh is tea
	{code: "KOPIKMR", name: "Kopi sachet kamar", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "1500", "1500"}, {"HK", "880", "1500"}}},
	{code: "SANDAL", name: "Sandal hotel", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "200", "6500"}, {"HK", "96", "6500"}}},
	{code: "LOTION", name: "Body lotion 30 ml", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "150", "4200"}, {"HK", "54", "4200"}}},
	{code: "DETERJEN", name: "Deterjen cair 5 L", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "12", "165000"}, {"HK", "4", "165000"}}},
	{code: "PEMBERSIH", name: "Pembersih lantai 5 L", category: "PERLENGKAPAN", unit: "pcs", open: []invOpen{{"UTAMA", "10", "98000"}, {"HK", "3", "98000"}}},
	{code: "KERTAS", name: "Kertas struk 80 mm", category: "LAINNYA", unit: "rol", open: []invOpen{{"UTAMA", "60", "7500"}}},
	{code: "KERTASA4", name: "Kertas A4 80 gram", category: "LAINNYA", unit: "pak", open: []invOpen{{"UTAMA", "20", "52000"}}},
	{code: "LAMPU", name: "Lampu LED 9 W", category: "LAINNYA", unit: "pcs", open: []invOpen{{"UTAMA", "40", "28000"}}},
	{code: "BATERAI", name: "Baterai AA", category: "LAINNYA", unit: "pcs", open: []invOpen{{"UTAMA", "60", "6500"}}},
}

func seedInventory(e *env, dryRun bool) error {
	ctx, pid := e.ctx, e.propertyID
	deptSvc := departments.NewService(e.txm, e.clk, e.aw, e.authz, e.ten)
	svc := inventory.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, deptSvc)
	posSvc := pos.NewService(e.txm, e.clk, e.aw, e.authz, e.ten, deptSvc)
	say := func(kind, code string, exists bool) {
		if exists {
			fmt.Printf("%s %s exists\n", kind, code)
		} else {
			fmt.Printf("%s %s: to create\n", kind, code)
		}
	}

	// the settings: the costing method is chosen before the first movement; average unless the property chose
	st, err := svc.GetSettings(ctx, pid)
	if err != nil {
		return err
	}
	say("settings", st.CostingMethod, st.Exists)
	if !st.Exists && !dryRun {
		if _, err := svc.UpdateSettings(ctx, pid, inventory.SettingsInput{CostingMethod: inventory.MethodAverage, PriceTolerancePct: st.PriceTolerancePct, QtyTolerancePct: st.QtyTolerancePct,
			OverReceiptPct: st.OverReceiptPct, PoRequiresPr: st.PoRequiresPr, CountBlocksNightAudit: st.CountBlocksNightAudit, CountBlind: st.CountBlind, Version: st.Version}); err != nil {
			return fmt.Errorf("settings: %w", err)
		}
	}

	// units
	existing, err := svc.ListUnits(ctx, pid)
	if err != nil {
		return err
	}
	unit := map[string]int64{}
	for _, x := range existing {
		unit[x.Code] = x.ID
	}
	for _, x := range invUnits {
		_, ok := unit[x[0]]
		say("unit", x[0], ok)
		if ok || dryRun {
			continue
		}
		r, err := svc.CreateUnit(ctx, pid, inventory.UnitInput{Code: x[0], Name: x[1]})
		if err != nil {
			return fmt.Errorf("unit %s: %w", x[0], err)
		}
		unit[x[0]] = r.ID
	}

	// the accounts of the chart, by code
	accountID := map[string]int64{}
	rows, err := e.txm.DB(ctx).Query(ctx, `SELECT code, id FROM gl_accounts WHERE property_id = $1`, pid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code string
		var id int64
		if err := rows.Scan(&code, &id); err != nil {
			rows.Close()
			return err
		}
		accountID[code] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	// categories
	cats, err := svc.ListCategories(ctx, pid)
	if err != nil {
		return err
	}
	category := map[string]int64{}
	for _, c := range cats {
		category[c.Code] = c.ID
	}
	for _, c := range invCategories {
		_, ok := category[c.code]
		say("category", c.code, ok)
		if ok || dryRun {
			continue
		}
		in := inventory.CategoryInput{Code: c.code, Name: c.name, Kind: c.kind, InventoryAccountID: accountID[c.inventory]}
		if id, ok := accountID[c.cogs]; ok && c.cogs != "" {
			in.CogsAccountID = &id
		}
		if id, ok := accountID[c.issue]; ok && c.issue != "" {
			in.IssueAccountID = &id
		}
		r, err := svc.CreateCategory(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("category %s: %w (does the chart have the account %s?)", c.code, err, c.inventory)
		}
		category[c.code] = r.ID
	}

	// warehouses: those of the outlets are tied to them when the outlet exists
	outlets, err := posSvc.ListOutlets(ctx, pid, nil)
	if err != nil {
		return err
	}
	outletID := map[string]int64{}
	for _, o := range outlets {
		outletID[o.Code] = o.ID
	}
	deps, err := deptSvc.List(ctx, pid, departments.Filter{})
	if err != nil {
		return err
	}
	deptID := map[string]int64{}
	for _, d := range deps {
		if d.Active {
			deptID[d.Code] = d.ID
		}
	}
	whs, err := svc.ListWarehouses(ctx, pid)
	if err != nil {
		return err
	}
	warehouse := map[string]int64{}
	for _, w := range whs {
		warehouse[w.Code] = w.ID
	}
	for _, w := range invWarehouses {
		_, ok := warehouse[w.code]
		say("warehouse", w.code, ok)
		if ok || dryRun {
			continue
		}
		in := inventory.WarehouseInput{Code: w.code, Name: w.name, Kind: w.kind, CostClass: w.class}
		if w.kind == "OUTLET" {
			if id, ok := outletID[w.outlet]; ok {
				in.OutletID = &id
			} else {
				fmt.Printf("  the outlet %s does not exist (run pms-seed pos): %s becomes the warehouse of a department\n", w.outlet, w.code)
				in.Kind = "DEPARTMENT"
			}
		}
		if id, ok := deptID[w.dept]; ok {
			in.DepartmentID = &id
		}
		r, err := svc.CreateWarehouse(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("warehouse %s: %w", w.code, err)
		}
		warehouse[w.code] = r.ID
	}

	// items, with their units
	page, err := svc.ListItems(ctx, pid, inventory.ItemFilter{Limit: 200})
	if err != nil {
		return err
	}
	item := map[string]int64{}
	for _, it := range page.Items {
		item[it.Code] = it.ID
	}
	for _, x := range invItems {
		_, ok := item[x.code]
		say("item", x.code, ok)
		if ok || dryRun {
			continue
		}
		in := inventory.ItemInput{Code: x.code, Name: x.name, CategoryID: category[x.category], StockUnitID: unit[x.unit]}
		for _, eu := range x.extra {
			eu.UnitID = unit[eu.UnitCode]
			in.Units = append(in.Units, eu)
		}
		if x.ref != "" {
			c, err := decimal.NewFromString(x.ref)
			if err != nil {
				return err
			}
			in.ReferenceCost = &c
		}
		r, err := svc.CreateItem(ctx, pid, in)
		if err != nil {
			return fmt.Errorf("item %s: %w", x.code, err)
		}
		item[x.code] = r.ID
	}

	// the opening stock: one document per warehouse, with a fixed client uuid so that a second run gets the first back
	for n, w := range invWarehouses {
		var lines []inventory.OpeningLineInput
		for _, x := range invItems {
			for _, o := range x.open {
				if o.warehouse != w.code {
					continue
				}
				qty, err := decimal.NewFromString(o.qty)
				if err != nil {
					return err
				}
				cost, err := decimal.NewFromString(o.cost)
				if err != nil {
					return err
				}
				lines = append(lines, inventory.OpeningLineInput{ItemID: item[x.code], UnitID: unit[x.unit], Qty: qty, UnitCost: cost})
			}
		}
		note := "Saldo awal (demo)"
		uuid := fmt.Sprintf("5eed0000-0000-4000-8000-%012d", n+1)
		fmt.Printf("opening %s: %d lines\n", w.code, len(lines))
		if dryRun {
			continue
		}
		if len(lines) == 0 || warehouse[w.code] == 0 {
			continue
		}
		o, err := svc.PostOpening(ctx, pid, inventory.OpeningInput{ClientUUID: uuid, WarehouseID: warehouse[w.code], Note: &note, Lines: lines})
		if err != nil {
			return fmt.Errorf("opening %s: %w", w.code, err)
		}
		fmt.Printf("  %s %s\n", o.Number, o.TotalValue.String())
	}
	if dryRun {
		fmt.Println("purchasing: a supplier, a request that waits at level 2, and a request that was ordered and received in part (made once)")
		fmt.Println("movements: a requisition at level 2, a transfer, a use of the housekeeping, a waste, the plan of the amenities and an open count of the bar (made once)")
		fmt.Println("recipes: the outlets of the POS demo sell from their warehouses, and Nasi Goreng Kampung (variant Jumbo, add-on Telur ceplok), the iced tea and Kopi Bali get a recipe (made once)")
		return nil
	}
	if err := seedPurchasing(e, svc, deptSvc, unit, item, warehouse, deptID); err != nil {
		return err
	}
	if err := seedMovements(e, svc, deptSvc, unit, item, warehouse, deptID); err != nil {
		return err
	}
	return seedRecipes(e, svc, unit, item, warehouse)
}
