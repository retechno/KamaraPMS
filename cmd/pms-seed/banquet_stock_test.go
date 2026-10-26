package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/departments"
	"kamarapms/internal/iam"
	"kamarapms/internal/rooms/roomstest"
)

func TestSeedBanquetStockLinksTheItemsAndSetsTheWarehouseOnce(t *testing.T) {
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	ctx := roomstest.Admin(tn.ID)
	bg := context.Background()
	svc := banquet.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, nil, nil, nil)
	deps := departments.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy)
	standardDepartments(t, e, tn.ID, p.ID)
	var out bytes.Buffer
	if _, err := seedBanquetWith(ctx, svc, deps, e.Billing, p.ID, false, &out); err != nil {
		t.Fatal(err)
	}
	stock := func(dry bool) (int, string) {
		var o bytes.Buffer
		n, err := seedBanquetStock(ctx, svc, e.TxM.DB(ctx), p.ID, dry, &o)
		if err != nil {
			t.Fatal(err)
		}
		return n, o.String()
	}

	// without the kitchen warehouse of the inventory demo there is nothing to do, and it is said
	if n, o := stock(false); n != 0 || !strings.Contains(o, "pms-seed inventory") {
		t.Fatalf("no warehouse: %d %q", n, o)
	}

	// the inventory and POS demos: the warehouse, and two products that have a recipe (rows inserted directly: this is not a test of those seeds)
	items, err := svc.ListItems(ctx, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	var charge int64
	for _, it := range items {
		if it.Code == "LUNCH" {
			charge = it.ChargeCodeID
		}
	}
	// a recipe line needs an item of the inventory in a unit of its own: the smallest chain that satisfies the foreign keys
	one := func(query string, args ...any) int64 {
		t.Helper()
		var id int64
		if err := e.Pool.QueryRow(bg, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	if _, err := e.Pool.Exec(bg, `INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind, cost_class) VALUES ($1, $2, 'DAPUR', 'Dapur', 'MAIN', 'FOOD')`, tn.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	account := one(`INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, 'STK1', 'Stock', 'ASSET', 'DEBIT', 'INVENTORIES') RETURNING id`, tn.ID, p.ID)
	category := one(`INSERT INTO inv_categories (tenant_id, property_id, code, name, kind, inventory_account_id) VALUES ($1, $2, 'FOOD', 'Food', 'FOOD', $3) RETURNING id`, tn.ID, p.ID, account)
	unit := one(`INSERT INTO inv_units (tenant_id, property_id, code, name) VALUES ($1, $2, 'g', 'gram') RETURNING id`, tn.ID, p.ID)
	item := one(`INSERT INTO inv_items (tenant_id, property_id, code, name, category_id, stock_unit_id) VALUES ($1, $2, 'BAHAN', 'Bahan', $3, $4) RETURNING id`, tn.ID, p.ID, category, unit)
	if _, err := e.Pool.Exec(bg, `INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den, is_recipe) VALUES ($1, $2, $3, $4, 1, 1, true)`, tn.ID, p.ID, item, unit); err != nil {
		t.Fatal(err)
	}
	cat := one(`INSERT INTO pos_categories (tenant_id, property_id, code, name, fb_class) VALUES ($1, $2, 'MENU', 'Menu', 'FOOD') RETURNING id`, tn.ID, p.ID)
	for _, code := range []string{"NASGOR", "KOPIBALI"} {
		id := one(`INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES ($1, $2, $3, $3, $4, $5) RETURNING id`, tn.ID, p.ID, code, cat, charge)
		if _, err := e.Pool.Exec(bg, `INSERT INTO pos_recipe_lines (tenant_id, property_id, product_id, inventory_item_id, unit_id, quantity) VALUES ($1, $2, $3, $4, $5, 100)`, tn.ID, p.ID, id, item, unit); err != nil {
			t.Fatal(err)
		}
	}

	n, o := stock(true)
	if n != 3 || !strings.Contains(o, "item LUNCH: to link to the product NASGOR") || !strings.Contains(o, "banquet warehouse: to set to DAPUR") {
		t.Fatalf("dry run: %d %q", n, o)
	}
	if s, _ := svc.GetSettings(ctx, p.ID); s.WarehouseID != nil {
		t.Fatal("a dry run set the warehouse")
	}
	if n, _ = stock(false); n != 3 {
		t.Fatalf("created %d, the dry run said 3", n)
	}
	linked := map[string]string{"LUNCH": "NASGOR", "COFFEEBRK": "KOPIBALI"}
	items, err = svc.ListItems(ctx, p.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		want, ok := linked[it.Code]
		if !ok {
			if it.PosProductID != nil {
				t.Fatalf("item %s is linked", it.Code)
			}
			continue
		}
		var code string
		if it.PosProductID == nil {
			t.Fatalf("item %s is not linked", it.Code)
		}
		if err := e.Pool.QueryRow(bg, `SELECT code FROM pos_products WHERE id = $1`, *it.PosProductID).Scan(&code); err != nil || code != want {
			t.Fatalf("item %s is linked to %q (%v), want %s", it.Code, code, err, want)
		}
		if it.FbClass == nil || *it.FbClass != "FOOD" {
			t.Fatalf("item %s lost its class: %v", it.Code, it.FbClass)
		}
	}
	s, err := svc.GetSettings(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if s.WarehouseID == nil || s.WarehouseName == nil || *s.WarehouseName != "Dapur" {
		t.Fatalf("settings: %+v", s)
	}
	if n, _ = stock(false); n != 0 {
		t.Fatalf("the seed is not idempotent: %d", n)
	}
}
