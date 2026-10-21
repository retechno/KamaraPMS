package pos_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory"
	"kamarapms/internal/pos"
)

// The other reports that need a restaurant that sells (docs/architecture/35-inventory.md 12.5, section 22): the reconciliation on the days that have recipe data, the recipe cost list, and the dishes thrown
// away after a void in the waste report.

func TestTheReconciliationTheoryIsOverTheRevenueOfTheDaysWithRecipeDataAndTheDaysWithoutAreEstimated(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	s.sell(t, s.steak, "2") // no recipe: revenue, no cost
	s.work(t)
	d1 := s.closeStock(t)
	d2 := s.nextDay(t)
	s.sell(t, s.nasgor, "3")
	s.work(t)
	// the rice is counted: 9.4 kg are expected, 8 kg are found; the loss is what the estimate is capped at
	c, err := s.inv.StartCount(s.cnt, s.propID, inventory.CountInput{ClientUUID: uid(9300), WarehouseID: s.wh, Scope: inventory.ScopeAll})
	must(t, err)
	q := dec("8")
	_, err = s.inv.EnterCountLines(s.cnt, s.propID, c.ID, []inventory.CountLineInput{{ItemID: s.item["BERAS"], Qty: &q}})
	must(t, err)
	_, err = s.inv.SubmitCount(s.cnt, s.propID, c.ID)
	must(t, err)
	_, err = s.inv.ApproveCount(s.apr, s.propID, c.ID)
	must(t, err)
	s.closeStock(t)

	cost := s.costOf(t, d1, d2)
	rev1, rev2 := cost.Days[0].Food.Revenue, cost.Days[1].Food.Revenue
	if !rev1.IsPositive() || !rev2.IsPositive() || !cost.Days[0].Food.Cost.IsZero() {
		t.Fatalf("the days: %+v / %+v", cost.Days[0].Food, cost.Days[1].Food)
	}
	rec, err := s.inv.FbReconciliation(s.admin, s.propID, &d1, &d2)
	must(t, err)
	food := rec.Food
	if !food.Revenue.Equal(rev1.Add(rev2)) {
		t.Fatalf("the revenue is of both days: %s", food.Revenue)
	}
	if !food.TheoryRevenue.Equal(rev2) || food.NoRecipeDays != 1 || rec.Total.NoRecipeDays != 1 {
		t.Fatalf("the theory is over the second day only: revenue %s, days %d / %d", food.TheoryRevenue, food.NoRecipeDays, rec.Total.NoRecipeDays)
	}
	if !food.Theory.IsPositive() || food.TheoryPct == nil || !food.TheoryPct.Equal(*pctOf(food.Theory, rev2)) {
		t.Fatalf("the theory %%: %v of %s over %s", food.TheoryPct, food.Theory, rev2)
	}
	// the sales without a recipe are estimated at the percentage of the days that have data, inside the loss of the count
	want := rev1.Mul(*pctOf(food.Theory, rev2)).Div(decimal.NewFromInt(100))
	if !food.CountVariance.IsPositive() || !food.CountVariance.GreaterThan(want) {
		t.Fatalf("the count loss: %s (the estimate is about %s)", food.CountVariance, want)
	}
	if !food.NoRecipeEstimate.IsPositive() || food.NoRecipeEstimate.Sub(want).Abs().GreaterThan(decimal.NewFromInt(1)) {
		t.Fatalf("the estimate of the sales without a recipe: %s, want about %s", food.NoRecipeEstimate, want)
	}
	// a period with no day without data has none to report
	one, err := s.inv.FbReconciliation(s.admin, s.propID, &d2, &d2)
	must(t, err)
	if one.Food.NoRecipeDays != 0 || !one.Food.TheoryRevenue.Equal(rev2) {
		t.Fatalf("the second day alone: %+v", one.Food)
	}
}

func TestTheRecipeCostListGivesTheCostOfAPortionAndFlagsAProductWithoutARecipe(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	list, err := s.inv.RecipeCosts(s.admin, s.propID, nil, true)
	must(t, err)
	if list.WithoutRecipe < 1 {
		t.Fatalf("the steak has no recipe: %+v", list)
	}
	var nasgor, steak *inventory.RecipeCostRow
	for i := range list.Rows {
		r := &list.Rows[i]
		if r.OutletID != s.resto.ID {
			continue
		}
		switch r.ProductCode {
		case "NASGOR":
			nasgor = r
		case s.steak.Code:
			steak = r
		}
	}
	if nasgor == nil || steak == nil {
		t.Fatalf("the products of the restaurant are not in the list: %+v", list.Rows)
	}
	// 200 g of rice at 17,000 a kilogram
	if !nasgor.HasRecipe || nasgor.Cost == nil || !nasgor.Cost.Equal(dec("3400")) || nasgor.NetPrice == nil || !nasgor.NetPrice.IsPositive() {
		t.Fatalf("the nasi goreng: %+v", nasgor)
	}
	if nasgor.Margin == nil || !nasgor.Margin.Equal(nasgor.NetPrice.Sub(dec("3400"))) || nasgor.CostPct == nil || !nasgor.CostPct.Equal(*pctOf(dec("3400"), *nasgor.NetPrice)) {
		t.Fatalf("margin and cost %%: %+v", nasgor)
	}
	if steak.HasRecipe || steak.Cost != nil || steak.Margin != nil || steak.CostPct != nil || steak.NetPrice == nil {
		t.Fatalf("the steak: %+v", steak)
	}
	// the products with a recipe come first, and the products with no recipe last
	seenNone := false
	for _, r := range list.Rows {
		if !r.HasRecipe {
			seenNone = true
		} else if seenNone {
			t.Fatalf("a product with a recipe after one without: %+v", list.Rows)
		}
	}
	// narrowed to one outlet
	only, err := s.inv.RecipeCosts(s.admin, s.propID, &s.resto.ID, true)
	must(t, err)
	for _, r := range only.Rows {
		if r.OutletID != s.resto.ID {
			t.Fatalf("another outlet: %+v", r)
		}
	}
}

// A dish whose ingredient has no price at the warehouse of the outlet has no cost that can be trusted: its cost, margin and cost % are empty, it is counted apart and it stays out of the order by margin. The
// products of an outlet that sells no food or drink (the class OTHER) are not in the list unless the person asks for them.
func TestTheRecipeCostListSeparatesTheDishesWithoutAPriceAndLeavesOutTheProductsThatAreNotFoodOrDrink(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	ctx, pid := s.admin, s.propID
	// a sauce that was never bought: no stock, no last cost, no reference cost
	saus, err := s.inv.CreateItem(ctx, pid, inventory.ItemInput{Code: "SAUS", Name: "Saus", CategoryID: s.food, StockUnitID: s.unit["kg"],
		Units: []inventory.ItemUnit{{UnitID: s.unit["g"], StockNum: 1, StockDen: 1000, IsRecipe: true}}})
	must(t, err)
	sate, err := s.svc.CreateProduct(ctx, pid, pos.ProductInput{Code: "SATE", Name: "Sate", CategoryID: s.k.cat.ID, ChargeCodeID: s.chargeCode(t, "RESTAURANT"),
		Prices: []pos.ProductPriceIn{{PriceListID: s.k.priceList.ID, Price: dec("60000"), IsSold: true}}})
	must(t, err)
	_, err = s.inv.SaveRecipe(ctx, pid, sate.ID, inventory.RecipeInput{Lines: []inventory.RecipeLineInput{{ItemID: saus.ID, UnitID: s.unit["g"], Quantity: dec("100")}}})
	must(t, err)
	// an outlet with no warehouse sells what is not food or drink
	_, err = s.svc.CreateOutlet(ctx, pid, pos.OutletInput{Code: "LAUND", Name: "Laundry", OutletType: "LAUNDRY", OrderMode: "PRINT", DepartmentID: s.dept, RuleProfileID: s.k.profile.ID,
		PriceListID: s.k.priceList.ID, CompTreatment: "PAYMENT_COMP"})
	must(t, err)

	list, err := s.inv.RecipeCosts(ctx, pid, nil, false)
	must(t, err)
	var nasgor, sateRow *inventory.RecipeCostRow
	for i := range list.Rows {
		r := &list.Rows[i]
		if r.Class == "OTHER" || r.OutletCode == "LAUND" {
			t.Fatalf("a product that is not food or drink is in the list: %+v", r)
		}
		switch r.ProductCode {
		case "NASGOR":
			nasgor = r
		case "SATE":
			sateRow = r
		}
	}
	if nasgor == nil || sateRow == nil {
		t.Fatalf("the dishes are not in the list: %+v", list.Rows)
	}
	if !sateRow.HasRecipe || !sateRow.NoPrice || sateRow.Cost != nil || sateRow.Margin != nil || sateRow.CostPct != nil || sateRow.NetPrice == nil || list.WithoutPrice != 1 {
		t.Fatalf("the sate has no price for its sauce: %+v, without price %d", sateRow, list.WithoutPrice)
	}
	if nasgor.NoPrice || nasgor.CostPct == nil {
		t.Fatalf("the nasi goreng is priced: %+v", nasgor)
	}
	// by margin first, then the dishes without a price, then the products without a recipe
	stage := func(r inventory.RecipeCostRow) int {
		switch {
		case !r.HasRecipe:
			return 2
		case r.NoPrice:
			return 1
		}
		return 0
	}
	last := 0
	for _, r := range list.Rows {
		if st := stage(r); st < last {
			t.Fatalf("the order is by margin, then no price, then no recipe: %+v", list.Rows)
		} else {
			last = st
		}
	}
	if stage(list.Rows[0]) != 0 || stage(list.Rows[len(list.Rows)-1]) != 2 {
		t.Fatalf("the ends of the list: %+v", list.Rows)
	}
	// with the other products the laundry has its rows, all of the class OTHER
	all, err := s.inv.RecipeCosts(ctx, pid, nil, true)
	must(t, err)
	n := 0
	for _, r := range all.Rows {
		if r.OutletCode == "LAUND" {
			n++
			if r.Class != "OTHER" {
				t.Fatalf("the class of the laundry: %+v", r)
			}
		}
	}
	if n == 0 || len(all.Rows) <= len(list.Rows) {
		t.Fatalf("the products that are not food or drink: %d rows of the laundry, %d against %d", n, len(all.Rows), len(list.Rows))
	}
}
