package pos_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The snapshots of the day close and the cost reports (docs/architecture/35-inventory.md 3.6, 12; PR 5), on a real database: the restaurant sells, the stock deducts, the close stores the day.

// closeStock runs the stock step of the close of the open day, as the night audit does.
func (s *stockFx) closeStock(t *testing.T) civil.Date {
	t.Helper()
	day, err := s.Tenancy.CurrentBusinessDay(s.admin, s.propID)
	must(t, err)
	p, _ := auth.Require(s.admin)
	must(t, s.TxM.WithinTx(s.admin, func(ctx context.Context) error { return s.inv.CloseStock(ctx, p, s.propID, day.BusinessDate) }))
	return day.BusinessDate
}

// nextDay closes the open business day and opens the next one (the stock step has run before).
func (s *stockFx) nextDay(t *testing.T) civil.Date {
	t.Helper()
	day, err := s.Tenancy.CurrentBusinessDay(s.admin, s.propID)
	must(t, err)
	next := day.BusinessDate.AddDays(1)
	_, err = s.Pool.Exec(context.Background(), `UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1 AND status = 'OPEN'`, s.propID)
	must(t, err)
	_, err = s.Pool.Exec(context.Background(), `INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, $3)`, s.tenantID, s.propID, next)
	must(t, err)
	return next
}

// snapshotRows is everything the close stored for a day, as text, to compare one taking with the next.
func (s *stockFx) snapshotRows(t *testing.T, d civil.Date) string {
	t.Helper()
	out := ""
	for _, q := range []string{
		`SELECT 'cost ' || COALESCE(department_id, 0) || ' ' || COALESCE(outlet_id, 0) || ' ' || cost_kind || ' ' || amount FROM inv_cost_days WHERE property_id = $1 AND business_date = $2 ORDER BY 1`,
		`SELECT 'fb ' || COALESCE(outlet_id, 0) || ' ' || class || ' ' || revenue_net || ' ' || no_recipe_revenue || ' ' || sale_cost || ' ' || waste_cost || ' ' || staff_meal_cost || ' ' || entertainment_cost FROM inv_fb_cost_days WHERE property_id = $1 AND business_date = $2 ORDER BY 1`,
		`SELECT 'bal ' || warehouse_id || ' ' || item_id || ' ' || qty || ' ' || value FROM inv_balance_days WHERE property_id = $1 AND business_date = $2 ORDER BY 1`,
		`SELECT 'day ' || business_date FROM inv_snapshot_days WHERE property_id = $1 AND business_date = $2`,
	} {
		rows, err := s.Pool.Query(context.Background(), q, s.propID, d)
		must(t, err)
		for rows.Next() {
			var line string
			must(t, rows.Scan(&line))
			out += line + "\n"
		}
		rows.Close()
		must(t, rows.Err())
	}
	return out
}

func (s *stockFx) costOf(t *testing.T, from, to civil.Date) inventory.DailyCostReport {
	t.Helper()
	r, err := s.inv.DailyCost(s.admin, s.propID, &from, &to)
	must(t, err)
	return r
}

func (s *stockFx) netRevenue(t *testing.T, d civil.Date) decimal.Decimal {
	t.Helper()
	var v decimal.Decimal
	must(t, s.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(i.net_amount), 0) FROM folio_items i JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id AND f.folio_type = 'OUTLET'
		WHERE i.property_id = $1 AND i.business_date = $2 AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL')`, s.propID, d).Scan(&v))
	return v
}

func TestTheCloseStoresTheDayAndStoringItAgainWritesTheSameRows(t *testing.T) {
	bothMethods(t, func(t *testing.T, method string) {
		s := newStockFx(t, method)
		s.sell(t, s.nasgor, "3") // 3 x 0.2 kg of rice at 17,000
		s.work(t)
		d, err := s.Tenancy.CurrentBusinessDay(s.admin, s.propID)
		must(t, err)
		live := s.costOf(t, d.BusinessDate, d.BusinessDate)
		if !live.Days[0].Live {
			t.Fatal("the open day is computed from the ledger")
		}
		if !live.Days[0].Kinds[inventory.CostSaleFood].Equal(dec("10200")) || !live.Days[0].Total.Equal(dec("10200")) {
			t.Fatalf("the cost of the day: %v", live.Days[0].Kinds)
		}
		revenue := s.netRevenue(t, d.BusinessDate)
		if !revenue.IsPositive() || !live.Days[0].Food.Revenue.Equal(revenue) {
			t.Fatalf("the food revenue is the net sales of the outlet: %s, %s", live.Days[0].Food.Revenue, revenue)
		}

		s.closeStock(t)
		first := s.snapshotRows(t, d.BusinessDate)
		for _, want := range []string{fmt.Sprintf("cost %d %d SALE_COST_FOOD 10200.000", s.dept, s.resto.ID), "day " + d.BusinessDate.String()} {
			if !contains(first, want) {
				t.Fatalf("the snapshot lacks %q:\n%s", want, first)
			}
		}
		// the balances at the end of the day are the balances: three items moved (the opening and the sale)
		var n int
		must(t, s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_balance_days d JOIN inv_balances b ON b.warehouse_id = d.warehouse_id AND b.item_id = d.item_id AND b.qty = d.qty AND b.value = d.value
			WHERE d.property_id = $1 AND d.business_date = $2`, s.propID, d.BusinessDate).Scan(&n))
		if n != 3 {
			t.Fatalf("balance rows equal to the balances: %d", n)
		}
		// a second close of the same day (a retry) writes the same rows, once
		s.closeStock(t)
		if again := s.snapshotRows(t, d.BusinessDate); again != first {
			t.Fatalf("storing the day again changed it:\n%s\nagainst\n%s", first, again)
		}
		// the stored day answers what the open day computed
		stored := s.costOf(t, d.BusinessDate, d.BusinessDate)
		if stored.Days[0].Live {
			t.Fatal("a stored day is not live")
		}
		for _, k := range []string{inventory.CostSaleFood, inventory.CostWaste, inventory.CostAmenity} {
			if !stored.Days[0].Kinds[k].Equal(live.Days[0].Kinds[k]) {
				t.Fatalf("%s: stored %s, computed %s", k, stored.Days[0].Kinds[k], live.Days[0].Kinds[k])
			}
		}
		if !stored.Days[0].Food.Revenue.Equal(live.Days[0].Food.Revenue) {
			t.Fatal("the stored revenue is the computed revenue")
		}
	})
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestTheDailyCostIsTheSumOfTheMovementsOfTheDayAndTheMonthToDateAddsTheDays(t *testing.T) {
	bothMethods(t, func(t *testing.T, method string) {
		s := newStockFx(t, method)
		d1 := s.closeAfterSelling(t, "3")
		d2 := s.nextDay(t)
		s.sell(t, s.nasgor, "2")
		s.work(t)
		// four eggs thrown away
		reason := "SPOILAGE"
		_, err := s.inv.PostIssue(s.admin, s.propID, inventory.IssueInput{ClientUUID: uid(7001), WarehouseID: s.wh, Purpose: "WASTE", ReasonCode: &reason,
			Lines: []inventory.IssueLineInput{{ItemID: s.item["TELUR"], Qty: dec("4")}}})
		must(t, err)
		s.closeStock(t)

		r := s.costOf(t, d1, d2)
		if len(r.Days) != 2 {
			t.Fatalf("two days: %d", len(r.Days))
		}
		one, two := r.Days[0], r.Days[1]
		if one.Live || two.Live {
			t.Fatal("both days were stored by their close")
		}
		// each column is the sum of the movements of the day
		for i, day := range []civil.Date{d1, d2} {
			for kind, column := range map[string]string{inventory.CostSaleFood: `kind IN ('POS_SALE', 'POS_SALE_RETURN', 'NEG_SETTLE') AND cost_class = 'FOOD'`, inventory.CostWaste: `kind = 'WASTE'`} {
				var v decimal.Decimal
				must(t, s.Pool.QueryRow(context.Background(), `SELECT COALESCE(sum(-value_delta), 0) FROM inv_movements WHERE property_id = $1 AND business_date = $2 AND `+column, s.propID, day).Scan(&v))
				if got := r.Days[i].Kinds[kind]; !got.Equal(v) {
					t.Fatalf("day %s %s: report %s, movements %s", day, kind, got, v)
				}
			}
		}
		if !one.Kinds[inventory.CostSaleFood].Equal(dec("10200")) || !two.Kinds[inventory.CostSaleFood].Equal(dec("6800")) || !two.Kinds[inventory.CostWaste].Equal(dec("8000")) {
			t.Fatalf("the figures: %v / %v", one.Kinds, two.Kinds)
		}
		if !two.Total.Equal(dec("14800")) || !one.Total.Equal(dec("10200")) || !r.Period.Total.Equal(dec("25000")) {
			t.Fatalf("the totals: %s %s %s", one.Total, two.Total, r.Period.Total)
		}
		// the percentage of the month to date on the second day covers both days; the first day covers itself
		rev1, rev2 := s.netRevenue(t, d1), s.netRevenue(t, d2)
		// the month to date starts on the first of the month: a month that ends between the two days leaves the first day out of it
		mtdCost, mtdRevenue, mtdTotal := dec("17000"), rev1.Add(rev2), dec("25000")
		if d1.Month() != d2.Month() {
			mtdCost, mtdRevenue, mtdTotal = dec("6800"), rev2, dec("14800")
		}
		if !r.Mtd.Total.Equal(mtdTotal) {
			t.Fatalf("the month to date: %s, want %s", r.Mtd.Total, mtdTotal)
		}
		wantMtd := mtdCost.Mul(dec("100")).Div(mtdRevenue).Round(2)
		if two.Food.MtdPct == nil || !two.Food.MtdPct.Equal(wantMtd) {
			t.Fatalf("food cost %% to date: %v, want %s", two.Food.MtdPct, wantMtd)
		}
		wantOne := dec("10200").Mul(dec("100")).Div(rev1).Round(2)
		if one.Food.Pct == nil || !one.Food.Pct.Equal(wantOne) || !one.Food.MtdPct.Equal(wantOne) {
			t.Fatalf("food cost %% of the first day: %v / %v, want %s", one.Food.Pct, one.Food.MtdPct, wantOne)
		}
		// the by-department view adds up to the same total
		sum := decimal.Zero
		for _, v := range two.Departments {
			sum = sum.Add(v)
		}
		if !sum.Equal(two.Total) {
			t.Fatalf("departments %s, total %s", sum, two.Total)
		}
	})
}

// closeAfterSelling sells, drains the queue and closes the stock of the open day, and answers that day.
func (s *stockFx) closeAfterSelling(t *testing.T, qty string) civil.Date {
	t.Helper()
	s.sell(t, s.nasgor, qty)
	s.work(t)
	return s.closeStock(t)
}

func TestATargetIsSetPerOutletAndTheOutletWithoutOneTakesThePropertys(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	pid := s.propID
	for _, in := range []inventory.CostTarget{{Class: "FOOD", TargetPct: dec("30")}, {OutletID: &s.resto.ID, Class: "FOOD", TargetPct: dec("25.5")}} {
		_, err := s.inv.SetCostTarget(s.admin, pid, in)
		must(t, err)
	}
	_, err := s.inv.SetCostTarget(s.admin, pid, inventory.CostTarget{Class: "SUPPLIES", TargetPct: dec("30")})
	if err == nil {
		t.Fatal("a class that is not food or beverage")
	}
	_, err = s.inv.SetCostTarget(s.admin, pid, inventory.CostTarget{Class: "FOOD", TargetPct: dec("0")})
	if err == nil {
		t.Fatal("a target above zero")
	}
	// setting it again replaces it
	_, err = s.inv.SetCostTarget(s.admin, pid, inventory.CostTarget{OutletID: &s.resto.ID, Class: "FOOD", TargetPct: dec("26")})
	must(t, err)
	list, err := s.inv.ListCostTargets(s.admin, pid)
	must(t, err)
	if len(list) != 2 || !list[0].TargetPct.Equal(dec("30")) || list[0].OutletID != nil || !list[1].TargetPct.Equal(dec("26")) {
		t.Fatalf("targets: %+v", list)
	}
	d := s.closeAfterSelling(t, "3")
	// the daily cost of "all outlets" is read against the target weighted by revenue: the restaurant has its own, 26
	r := s.costOf(t, d, d)
	if r.Days[0].Food.Target == nil || !r.Days[0].Food.Target.Equal(dec("26")) {
		t.Fatalf("the weighted target: %v", r.Days[0].Food.Target)
	}
	must(t, s.inv.DeleteCostTarget(s.admin, pid, &s.resto.ID, "FOOD"))
	s.closeStock(t)
	if r = s.costOf(t, d, d); r.Days[0].Food.Target == nil || !r.Days[0].Food.Target.Equal(dec("30")) {
		t.Fatalf("without its own target the outlet takes the property's: %v", r.Days[0].Food.Target)
	}
	if err := s.inv.DeleteCostTarget(s.admin, pid, &s.resto.ID, "FOOD"); err == nil {
		t.Fatal("a target that is not there")
	}
	// somebody who only reads cannot set one
	if _, err := s.inv.SetCostTarget(s.cnt, pid, inventory.CostTarget{Class: "FOOD", TargetPct: dec("28")}); err == nil {
		t.Fatal("setting a target needs inventory.manage")
	}
}

func TestTheOutletCostIsTheTheoryPlusTheCountVarianceOfItsWarehouse(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	d := s.closeAfterSelling(t, "5") // 1 kg of rice: 17,000
	rep, err := s.inv.OutletCost(s.admin, s.propID, &d, &d)
	must(t, err)
	if len(rep.Rows) != 1 {
		t.Fatalf("rows: %+v", rep.Rows)
	}
	row := rep.Rows[0]
	if row.OutletCode != "R" || row.Class != "FOOD" || !row.Theory.Equal(dec("17000")) || !row.CountVariance.IsZero() || !row.Actual.Equal(dec("17000")) || row.Basis != "BOOK" {
		t.Fatalf("the outlet: %+v", row)
	}
	if row.TheoryPct == nil || !row.TheoryPct.Equal(row.ActualPct.Round(2)) {
		t.Fatalf("theory and actual agree without a count: %v %v", row.TheoryPct, row.ActualPct)
	}
}
