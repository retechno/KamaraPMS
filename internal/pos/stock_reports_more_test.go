package pos_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory"
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
	list, err := s.inv.RecipeCosts(s.admin, s.propID, nil)
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
	only, err := s.inv.RecipeCosts(s.admin, s.propID, &s.resto.ID)
	must(t, err)
	for _, r := range only.Rows {
		if r.OutletID != s.resto.ID {
			t.Fatalf("another outlet: %+v", r)
		}
	}
}
