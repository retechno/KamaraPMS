package main

import (
	"testing"

	"github.com/shopspring/decimal"
)

// Every position on a floor has a type, and the ten positions spread 4/3/2/1 over the four types.
func TestTypeCodeAtSpreadsTheTenPositions(t *testing.T) {
	count := map[string]int{}
	for n := 1; n <= 10; n++ {
		count[typeCodeAt(n)]++
	}
	want := map[string]int{"STD": 4, "SUP": 3, "DLX": 2, "STE": 1}
	for code, n := range want {
		if count[code] != n {
			t.Fatalf("%s: %d rooms of ten, want %d (%v)", code, count[code], n, count)
		}
	}
}

func TestEveryTypeIsUsedOnce(t *testing.T) {
	seen := map[int]bool{}
	for _, spec := range types {
		for _, p := range spec.positions {
			if seen[p] {
				t.Fatalf("position %d is given to two types", p)
			}
			seen[p] = true
		}
	}
	if len(seen) != 10 {
		t.Fatalf("%d positions are covered, want 10", len(seen))
	}
}

// Every position has a bed type, and a Standard room has Twin or Queen beds.
func TestBedCodeAt(t *testing.T) {
	for n := 1; n <= 10; n++ {
		switch typeCodeAt(n) {
		case "STD":
			if c := bedCodeAt(n); c != "TWIN" && c != "QUEEN" {
				t.Fatalf("position %d: %s", n, c)
			}
		case "SUP":
			if bedCodeAt(n) != "DOUBLE" {
				t.Fatalf("position %d: %s", n, bedCodeAt(n))
			}
		default:
			if bedCodeAt(n) != "KING" {
				t.Fatalf("position %d: %s", n, bedCodeAt(n))
			}
		}
	}
}

// The laundry has a price list of its own that holds laundry services only, and no food is sold there (and no laundry service in a kitchen list).
func TestPosDemoKeepsTheLaundryApartFromTheKitchen(t *testing.T) {
	for _, p := range posProducts {
		switch p.category {
		case "LAUNDRY":
			if p.laundry == "" || p.resto != "" || p.rs != "" || p.charge != "LAUNDRY" || p.station != "" {
				t.Errorf("%s: a laundry service is sold in the laundry list only, with the laundry charge code and no station: %+v", p.code, p)
			}
		default:
			if p.laundry != "" {
				t.Errorf("%s: food and drinks are not sold at the laundry", p.code)
			}
			if p.resto == "" || p.station == "" {
				t.Errorf("%s: food has a restaurant price and a station", p.code)
			}
		}
	}
}

// The demo data of the inventory refers only to what it creates: a unit, a category, a warehouse; an item is opened once per warehouse and has one default unit of each kind at most.
func TestInventoryDemoRefersOnlyToWhatItCreates(t *testing.T) {
	units := map[string]bool{}
	for _, u := range invUnits {
		units[u[0]] = true
	}
	cats := map[string]bool{}
	for _, c := range invCategories {
		cats[c.code] = true
	}
	whs := map[string]bool{}
	for _, w := range invWarehouses {
		whs[w.code] = true
	}
	codes := map[string]bool{}
	for _, it := range invItems {
		if codes[it.code] {
			t.Errorf("%s: two items with the code", it.code)
		}
		codes[it.code] = true
		if !cats[it.category] || !units[it.unit] {
			t.Errorf("%s: category %q or unit %q is not made by the seed", it.code, it.category, it.unit)
		}
		dp, dr := 0, 0
		for _, x := range it.extra {
			if !units[x.UnitCode] || x.StockNum < 1 || x.StockDen < 1 || x.UnitCode == it.unit {
				t.Errorf("%s: the unit %+v", it.code, x)
			}
			if x.IsDefaultPurchase {
				dp++
			}
			if x.IsDefaultRecipe {
				dr++
			}
		}
		if dp > 1 || dr > 1 {
			t.Errorf("%s: more than one default unit", it.code)
		}
		opened := map[string]bool{}
		for _, o := range it.open {
			if !whs[o.warehouse] || opened[o.warehouse] {
				t.Errorf("%s: opening in %q", it.code, o.warehouse)
			}
			opened[o.warehouse] = true
		}
	}
	if len(invItems) < 35 {
		t.Errorf("%d items, about forty are expected", len(invItems))
	}
}

// The demo requests of the purchasing name items and units that the seed makes, in a unit the item is bought in, and stay under the third level of the default chain (5,000,000).
func TestPurchasingDemoRefersOnlyToWhatItCreates(t *testing.T) {
	byCode := map[string]invItem{}
	for _, it := range invItems {
		byCode[it.code] = it
	}
	for name, lines := range map[string][]demoLine{"running": demoRunningLines, "received": demoReceivedLines} {
		total := decimal.Zero
		for _, l := range lines {
			it, ok := byCode[l.item]
			if !ok {
				t.Errorf("%s: the item %s is not made by the seed", name, l.item)
				continue
			}
			bought := l.unit == it.unit
			for _, x := range it.extra {
				if x.UnitCode == l.unit && x.IsPurchase {
					bought = true
				}
			}
			if !bought {
				t.Errorf("%s: %s is not bought in %s", name, l.item, l.unit)
			}
			q, err1 := decimal.NewFromString(l.qty)
			p, err2 := decimal.NewFromString(l.price)
			if err1 != nil || err2 != nil {
				t.Fatalf("%s: %+v", name, l)
			}
			total = total.Add(q.Mul(p))
		}
		if total.GreaterThanOrEqual(decimal.NewFromInt(5000000)) {
			t.Errorf("%s: %s reaches the third level of the chain", name, total)
		}
	}
	q, err := decimal.NewFromString(demoReceivedQty)
	first, err2 := decimal.NewFromString(demoReceivedLines[0].qty)
	if err != nil || err2 != nil || !q.IsPositive() || !q.LessThan(first) {
		t.Errorf("the first delivery (%s) is part of the first line (%s)", demoReceivedQty, demoReceivedLines[0].qty)
	}
}

// The demo documents of the movements name items and units that the seed makes, in a unit the item has, and the warehouses they take from hold the stock (the source may not go below zero).
func TestMovementsDemoRefersOnlyToWhatItCreates(t *testing.T) {
	byCode := map[string]invItem{}
	for _, it := range invItems {
		byCode[it.code] = it
	}
	holds := func(code, warehouse string) bool {
		for _, o := range byCode[code].open {
			if o.warehouse == warehouse {
				return true
			}
		}
		return false
	}
	for name, c := range map[string]struct {
		lines     []demoLine
		warehouse string
	}{
		"requisition": {demoSRLines, "UTAMA"}, "transfer": {demoTransferLines, "UTAMA"}, "issue": {demoIssueLines, "HK"}, "waste": {demoWasteLines, "DAPUR"}, "amenities": {demoAmenityLines, "HK"},
	} {
		for _, l := range c.lines {
			it, ok := byCode[l.item]
			if !ok {
				t.Errorf("%s: the item %s is not made by the seed", name, l.item)
				continue
			}
			has := l.unit == it.unit
			for _, x := range it.extra {
				has = has || x.UnitCode == l.unit
			}
			if !has {
				t.Errorf("%s: %s has no unit %s", name, l.item, l.unit)
			}
			if !holds(l.item, c.warehouse) {
				t.Errorf("%s: %s has no opening stock in %s", name, l.item, c.warehouse)
			}
		}
	}
	for _, code := range demoCountItems {
		if !holds(code, "BAR") {
			t.Errorf("count: %s has no opening stock in the bar", code)
		}
	}
	// the amenities are per room in the stock unit and belong to the supplies
	for _, l := range demoAmenityLines {
		if byCode[l.item].category != "PERLENGKAPAN" || l.unit != byCode[l.item].unit {
			t.Errorf("amenities: %s is not a supply in its stock unit", l.item)
		}
	}
}

// The recipes of the demo name items of the seed in a unit they have, the warehouses of the outlets are made by the seed, and the items are in stock where the outlet sells.
func TestRecipesDemoRefersOnlyToWhatItCreates(t *testing.T) {
	byCode := map[string]invItem{}
	for _, it := range invItems {
		byCode[it.code] = it
	}
	warehouses := map[string]bool{}
	for _, w := range invWarehouses {
		warehouses[w.code] = true
	}
	for outlet, wh := range demoOutletWarehouses {
		if !warehouses[wh] {
			t.Errorf("the outlet %s sells from %s, which the seed does not make", outlet, wh)
		}
	}
	for _, r := range demoRecipes {
		for _, l := range r.lines {
			it, ok := byCode[l.item]
			if !ok {
				t.Errorf("%s: the item %s is not made by the seed", r.product, l.item)
				continue
			}
			has := l.unit == it.unit
			for _, x := range it.extra {
				has = has || x.UnitCode == l.unit
			}
			if !has {
				t.Errorf("%s: %s has no unit %s", r.product, l.item, l.unit)
			}
			q, err := decimal.NewFromString(l.qty)
			if err != nil || !q.IsPositive() {
				t.Errorf("%s: the quantity %q", r.product, l.qty)
			}
		}
	}
}

// The plan of the demo days is the same every time, every day has sales and a staff meal, and the days differ, so the daily cost has something to show.
func TestPlanDayIsDeterministicAndVaried(t *testing.T) {
	seen := map[int]bool{}
	var waste, entertain, engineering, office int
	for i := 0; i < 10; i++ {
		d := planDay(i)
		if again := planDay(i); len(again.nasgor) != len(d.nasgor) || again.wasteEggs != d.wasteEggs {
			t.Fatalf("day %d changes between two calls", i)
		}
		if len(d.nasgor) == 0 || len(d.bar) == 0 || d.staffRiceKg == 0 {
			t.Fatalf("day %d has no sales or no staff meal: %+v", i, d)
		}
		seen[d.nasgor[0].qty] = true
		if d.wasteEggs > 0 {
			waste++
		}
		if d.entertainBotol > 0 {
			entertain++
		}
		if d.engineering > 0 {
			engineering++
		}
		if d.office > 0 {
			office++
		}
	}
	if len(seen) < 3 || waste == 0 || entertain == 0 || engineering == 0 || office == 0 {
		t.Fatalf("ten days should differ and use every kind of cost: %d sizes, waste %d, entertainment %d, engineering %d, office %d", len(seen), waste, entertain, engineering, office)
	}
}

func TestInventoryDaysRefusesAnAPIThatIsNotThisMachineAndNeedsAPassword(t *testing.T) {
	if err := runInventoryDays([]string{"-tenant", "DEMO", "-property", "BALI", "-api", "https://pms.example.com"}); err == nil {
		t.Fatal("a remote API")
	}
	t.Setenv("PMS_SEED_PASSWORD", "")
	if err := runInventoryDays([]string{"-tenant", "DEMO", "-property", "BALI"}); err == nil {
		t.Fatal("no password")
	}
}
