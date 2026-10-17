package main

import (
	"fmt"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory"
)

// The recipes part of `pms-seed inventory` (docs/architecture/35-inventory.md, PR 4): the outlets of the demo sell from their warehouses, and the products of the POS demo have a recipe: the Nasi Goreng Kampung
// with its variant Jumbo (which replaces the rice) and its add-on Telur ceplok (which adds an egg and oil), a tea, a coffee. It needs the POS demo (`pms-seed pos`) and says so when it is missing; a product that
// has a recipe already, and an outlet that has a warehouse already, are left as they are, so a second run changes nothing.

// the warehouse each outlet of the POS demo sells from
var demoOutletWarehouses = map[string]string{"RESTO": "DAPUR", "BAR": "BAR", "RS": "DAPUR"}

type recipeLine struct{ option, item, unit, qty string }

// the recipes, by the code of the product: option is the name of a variant or an add-on of the product, empty for the product's own line; the units are those of the items (a recipe unit or the stock unit)
var demoRecipes = []struct {
	product string
	lines   []recipeLine
}{
	{"NASGOR", []recipeLine{
		{"", "BERAS", "g", "200"}, {"", "AYAM", "g", "60"}, {"", "TELUR", "pcs", "1"}, {"", "BWMERAH", "g", "20"}, {"", "MGRNG", "ml", "30"}, {"", "KECAP", "ml", "15"},
		{"Jumbo", "BERAS", "g", "300"},
		{"Telur ceplok", "TELUR", "pcs", "1"}, {"Telur ceplok", "MGRNG", "ml", "10"},
	}},
	{"ESTEH", []recipeLine{{"", "TEHCL", "pcs", "1"}, {"", "SIRUP", "ml", "25"}}}, //nolint:misspell // Indonesian: es teh is iced tea
	{"KOPIBALI", []recipeLine{{"", "KOPI", "g", "18"}, {"", "SUSU", "ml", "100"}}},
}

func seedRecipes(e *env, svc *inventory.Service, unit, item, warehouse map[string]int64) error {
	ctx, pid := e.ctx, e.propertyID
	db := e.txm.DB(ctx)
	var outlets int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM pos_outlets WHERE property_id = $1`, pid).Scan(&outlets); err != nil {
		return err
	}
	if outlets == 0 {
		fmt.Println("recipes: skipped, the POS demo is not there (run pms-seed pos first)")
		return nil
	}
	for _, outlet := range []string{"RESTO", "BAR", "RS"} {
		wh := demoOutletWarehouses[outlet]
		tag, err := db.Exec(ctx, `UPDATE pos_outlets SET warehouse_id = $3 WHERE property_id = $1 AND code = $2 AND warehouse_id IS NULL`, pid, outlet, warehouse[wh])
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			fmt.Printf("outlet %s: sells from %s\n", outlet, wh)
		}
	}
	for _, r := range demoRecipes {
		var productID int64
		if err := db.QueryRow(ctx, `SELECT id FROM pos_products WHERE property_id = $1 AND code = $2`, pid, r.product).Scan(&productID); err != nil {
			fmt.Printf("recipe %s: skipped, the product is not in the POS demo\n", r.product)
			continue
		}
		var have int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM pos_recipe_lines WHERE property_id = $1 AND product_id = $2`, pid, productID).Scan(&have); err != nil {
			return err
		}
		if have > 0 {
			fmt.Printf("recipe %s exists\n", r.product)
			continue
		}
		options := map[string]int64{}
		rows, err := db.Query(ctx, `SELECT o.name, o.id FROM pos_options o JOIN pos_option_groups g ON g.property_id = o.property_id AND g.id = o.group_id WHERE g.property_id = $1 AND g.product_id = $2`, pid, productID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			var id int64
			if err := rows.Scan(&name, &id); err != nil {
				rows.Close()
				return err
			}
			options[name] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		in := inventory.RecipeInput{}
		for _, l := range r.lines {
			qty, err := decimal.NewFromString(l.qty)
			if err != nil {
				return err
			}
			if item[l.item] == 0 || unit[l.unit] == 0 {
				return fmt.Errorf("recipe %s: the item %s or the unit %s is not made by the seed", r.product, l.item, l.unit)
			}
			line := inventory.RecipeLineInput{ItemID: item[l.item], UnitID: unit[l.unit], Quantity: qty}
			if l.option != "" {
				id, ok := options[l.option]
				if !ok {
					fmt.Printf("recipe %s: the option %s is not on the product: its lines are left out\n", r.product, l.option)
					continue
				}
				line.OptionID = &id
			}
			in.Lines = append(in.Lines, line)
		}
		if _, err := svc.SaveRecipe(ctx, pid, productID, in); err != nil {
			return fmt.Errorf("recipe %s: %w", r.product, err)
		}
		fmt.Printf("recipe %s: %d lines\n", r.product, len(in.Lines))
	}
	return nil
}
