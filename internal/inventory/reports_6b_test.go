package inventory_test

import (
	"testing"

	"kamarapms/internal/inventory"
	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
)

// The reports of PR 6b (35-inventory.md 12.5, section 22.5): the stock movement per item, the purchases per supplier and the price history, the slow and dead stock, the largest usage. Real PostgreSQL.

func wantBucket(t *testing.T, row inventory.ItemMovementRow, bucket, qty, value string) {
	t.Helper()
	b := row.Buckets[bucket]
	if !b.Qty.Equal(dec(qty)) || !b.Value.Equal(dec(value)) {
		t.Fatalf("%s %s: %s / %s, want %s / %s", row.ItemCode, bucket, b.Qty, b.Value, qty, value)
	}
}

// ---------------------------------------------------------------- the stock movement per item

func TestTheStockMovementAlwaysBalancesForAverageAndFIFO(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			m := newMovements(t, method)
			m.settings(t, func(in *inventory.SettingsInput) { in.PoRequiresPr = false })
			m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
			m.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
			m.open(t, "HK", "SOAP", "pcs", "100", "2960")
			// goods in, a transfer, every kind of use, a sale and a count
			po := m.order(t, m.sup, "MAIN", "SIRUP", "karton", "2", "600000") // 24 bottles at 50,000
			m.receive(t, po, "2")
			_, err := m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "4"))
			must(t, err)
			_, err = m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SOAP", "5"))
			must(t, err)
			_, err = m.issue(m.mover, "MAIN", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("SPOILAGE") }, m.il("BERAS", "2"))
			must(t, err)
			_, err = m.issue(m.mover, "MAIN", inventory.PurposeStaffMeal, func(in *inventory.IssueInput) { in.Covers = ptr(10) }, m.il("BERAS", "1"))
			must(t, err)
			_, err = m.issue(m.mover, "MAIN", inventory.PurposeEntertainment, func(in *inventory.IssueInput) { in.Covers = ptr(2); in.Beneficiary = ptr("Tamu") }, m.il("BERAS", "1"))
			must(t, err)
			m.sale(t, m.pair("MAIN", "BERAS"), "3")
			c := m.startCount(t, "HK", inventory.ScopeAll)
			c = m.found(t, c, "SOAP", "92")
			c = m.submitCount(t, c)
			m.approveCount(t, c)

			rep, err := m.svc.ItemMovements(m.admin, m.propID, nil, nil, inventory.ItemMovementFilter{})
			must(t, err)
			rows := map[string]inventory.ItemMovementRow{}
			for _, r := range rep.Rows {
				rows[r.ItemCode] = r
			}
			// rice over both warehouses: 40 kg in, 4 kg moved from MAIN to BAR, and what was thrown away, eaten and sold
			rice := rows["BERAS"]
			wantDec(t, "rice opening", rice.Opening.Qty, "0")
			wantBucket(t, rice, inventory.MoveOpeningStock, "40", "620000")
			wantBucket(t, rice, inventory.MoveTransfersIn, "4", "62000")
			wantBucket(t, rice, inventory.MoveTransfersOut, "4", "62000")
			wantBucket(t, rice, inventory.MoveWaste, "2", "31000")
			wantBucket(t, rice, inventory.MoveStaffMeals, "1", "15500")
			wantBucket(t, rice, inventory.MoveEntertainment, "1", "15500")
			wantBucket(t, rice, inventory.MovePosSales, "3", "46500")
			wantDec(t, "rice closing qty", rice.Closing.Qty, "33")
			wantDec(t, "rice closing value", rice.Closing.Value, "511500")
			// the syrup: opening stock and the receipt of 24 bottles
			syrup := rows["SIRUP"]
			wantBucket(t, syrup, inventory.MoveReceipts, "24", "1200000")
			wantDec(t, "syrup closing", syrup.Closing.Qty, "44")
			// the count of the soap
			soap := rows["SOAP"]
			wantBucket(t, soap, inventory.MoveUsage, "5", "14800")
			wantBucket(t, soap, inventory.MoveCountVariance, "-3", "-8880")
			wantDec(t, "soap closing", soap.Closing.Qty, "92")

			// in every row: opening + in - out + count + adjustments = closing, and the closing is what the balances say
			for code, r := range rows {
				qty, value := r.Opening.Qty, r.Opening.Value
				for _, b := range []string{inventory.MoveOpeningStock, inventory.MoveReceipts, inventory.MoveTransfersIn, inventory.MoveSalesReturns, inventory.MoveCountVariance, inventory.MoveCostAdjust} {
					qty, value = qty.Add(r.Buckets[b].Qty), value.Add(r.Buckets[b].Value)
				}
				for _, b := range []string{inventory.MovePosSales, inventory.MoveUsage, inventory.MoveWaste, inventory.MoveStaffMeals, inventory.MoveEntertainment, inventory.MoveTransfersOut, inventory.MoveSupplierReturn} {
					qty, value = qty.Sub(r.Buckets[b].Qty), value.Sub(r.Buckets[b].Value)
				}
				if !qty.Equal(r.Closing.Qty) || !value.Equal(r.Closing.Value) {
					t.Fatalf("%s does not balance: %s / %s against %s / %s", code, qty, value, r.Closing.Qty, r.Closing.Value)
				}
				var bq, bv = dec("0"), dec("0")
				for _, w := range []string{"MAIN", "BAR", "HK"} {
					q, v := m.bal(t, m.pair(w, code))
					bq, bv = bq.Add(dec(q)), bv.Add(dec(v))
				}
				if !bq.Equal(r.Closing.Qty) || !bv.Equal(r.Closing.Value) {
					t.Fatalf("%s: the closing %s / %s is not the balance %s / %s", code, r.Closing.Qty, r.Closing.Value, bq, bv)
				}
			}
			// the totals by category add up to the grand total
			var opening, closing = dec("0"), dec("0")
			for _, c := range rep.Categories {
				opening, closing = opening.Add(c.Opening), closing.Add(c.Closing)
			}
			if !opening.Equal(rep.Total.Opening) || !closing.Equal(rep.Total.Closing) || len(rep.Categories) < 2 {
				t.Fatalf("categories: %+v, total %+v", rep.Categories, rep.Total)
			}

			// a warehouse and a category narrow it; the transfer then shows on one side only
			bar, err := m.svc.ItemMovements(m.admin, m.propID, nil, nil, inventory.ItemMovementFilter{WarehouseID: ptr(m.wh["BAR"])})
			must(t, err)
			if len(bar.Rows) != 1 || bar.Rows[0].ItemCode != "BERAS" {
				t.Fatalf("the bar: %+v", bar.Rows)
			}
			wantBucket(t, bar.Rows[0], inventory.MoveTransfersIn, "4", "62000")
			wantBucket(t, bar.Rows[0], inventory.MoveTransfersOut, "0", "0")
			one, err := m.svc.ItemMovements(m.admin, m.propID, nil, nil, inventory.ItemMovementFilter{ItemID: ptr(m.item["SOAP"])})
			must(t, err)
			if len(one.Rows) != 1 {
				t.Fatalf("one item: %+v", one.Rows)
			}

			// a period after the day of the movements: everything is opening and the closing is the same
			next := m.today(t).AddDays(1)
			after, err := m.svc.ItemMovements(m.admin, m.propID, &next, &next, inventory.ItemMovementFilter{})
			must(t, err)
			for _, r := range after.Rows {
				if !r.Opening.Qty.Equal(r.Closing.Qty) || !r.Opening.Value.Equal(r.Closing.Value) || anyMoved(r) {
					t.Fatalf("%s moved after the day: %+v", r.ItemCode, r)
				}
			}
			if len(after.Rows) != len(rep.Rows)-countGone(rep.Rows) {
				t.Fatalf("rows after: %d, before: %d", len(after.Rows), len(rep.Rows))
			}
		})
	}
}

func anyMoved(r inventory.ItemMovementRow) bool {
	for _, b := range r.Buckets {
		if !b.Qty.IsZero() || !b.Value.IsZero() {
			return true
		}
	}
	return false
}

// countGone counts the rows that end with nothing: they leave the report of a later period.
func countGone(rows []inventory.ItemMovementRow) int {
	n := 0
	for _, r := range rows {
		if r.Closing.Qty.IsZero() && r.Closing.Value.IsZero() {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- purchases per supplier and the price history

func TestThePurchasesAreBySupplierAndItemAndTheHistoryShowsThePricePaid(t *testing.T) {
	f := newPurchasing(t, inventory.MethodAverage, 0)
	f.open(t, "MAIN", "SIRUP", "botol", "20", "48000")
	f.open(t, "MAIN", "BERAS", "kg", "10", "15500")
	// the supplier sells five cartons (60 bottles) at 600,000 and is billed 612,000; the market sells five at 624,000
	po1 := f.order(t, f.sup, "MAIN", "SIRUP", "karton", "5", "600000")
	r1 := f.receive(t, po1, "5")
	in := invoice(f.sup, "INV-1", billLine(r1, 1, "5", "612000", "0"))
	in.Approval = f.approver(t)
	_, err := f.svc.ConfirmBill(f.admin, f.propID, in, "k1")
	must(t, err)
	po2 := f.order(t, f.pasar, "MAIN", "SIRUP", "karton", "5", "624000")
	f.receive(t, po2, "5")
	po3 := f.order(t, f.pasar, "MAIN", "BERAS", "karung", "2", "400000") // 50 kg at 8,000
	f.receive(t, po3, "2")
	// a receipt that was reversed is not a purchase
	po4 := f.order(t, f.pasar, "MAIN", "SIRUP", "karton", "1", "900000")
	r4 := f.receive(t, po4, "1")
	_, err = f.svc.ReverseReceipt(f.admin, f.propID, r4.ID, "wrong goods")
	must(t, err)

	rep, err := f.svc.Purchases(f.admin, f.propID, nil, nil, inventory.PurchaseFilter{})
	must(t, err)
	wantDec(t, "total", rep.Value, "6920000")
	wantDec(t, "billed", rep.BilledValue, "3060000")
	if rep.Receipts != 3 || rep.Bills != 1 || len(rep.Suppliers) != 2 {
		t.Fatalf("report: %+v", rep)
	}
	// the market is first (the larger value) with two items; the share is the part of the total
	pasar, sup := rep.Suppliers[0], rep.Suppliers[1]
	if pasar.SupplierID != f.pasar || sup.SupplierID != f.sup {
		t.Fatalf("order: %+v", rep.Suppliers)
	}
	wantDec(t, "market value", pasar.Value, "3920000")
	wantDec(t, "supplier value", sup.Value, "3000000")
	if pasar.SharePct == nil || !pasar.SharePct.Equal(dec("56.65")) || sup.SharePct == nil || !sup.SharePct.Equal(dec("43.35")) {
		t.Fatalf("shares: %v %v", pasar.SharePct, sup.SharePct)
	}
	if pasar.Receipts != 2 || pasar.Bills != 0 || sup.Receipts != 1 || sup.Bills != 1 || len(pasar.Items) != 2 || len(sup.Items) != 1 {
		t.Fatalf("counts: %+v / %+v", pasar, sup)
	}
	syrup := sup.Items[0]
	wantDec(t, "syrup qty", syrup.Qty, "60")
	wantDec(t, "syrup value", syrup.Value, "3000000")
	wantDec(t, "syrup billed", syrup.BilledValue, "3060000")
	if syrup.AvgPrice == nil || !syrup.AvgPrice.Equal(dec("50000")) {
		t.Fatalf("average price: %v", syrup.AvgPrice)
	}
	// narrowed to a supplier, a warehouse and a category
	one, err := f.svc.Purchases(f.admin, f.propID, nil, nil, inventory.PurchaseFilter{SupplierID: &f.sup})
	must(t, err)
	if len(one.Suppliers) != 1 || !one.Value.Equal(dec("3000000")) {
		t.Fatalf("one supplier: %+v", one)
	}
	// a period with no receipt is empty
	day, err := f.Tenancy.CurrentBusinessDay(f.admin, f.propID)
	must(t, err)
	later := day.BusinessDate.AddDays(5)
	none, err := f.svc.Purchases(f.admin, f.propID, &later, &later, inventory.PurchaseFilter{})
	must(t, err)
	if len(none.Suppliers) != 0 || !none.Value.IsZero() {
		t.Fatalf("a later period: %+v", none)
	}

	// the price history of the syrup: 50,000 a bottle, then 52,000 (the reversed receipt is not in it)
	h, err := f.svc.PriceHistory(f.admin, f.propID, ptr(f.item["SIRUP"]), nil, nil, nil)
	must(t, err)
	if len(h.Rows) != 2 || h.ItemCode != "SIRUP" || h.UnitCode != "botol" {
		t.Fatalf("history: %+v", h)
	}
	wantDec(t, "first price per bottle", h.Rows[0].PricePerStock, "50000")
	wantDec(t, "first price per carton", h.Rows[0].UnitPrice, "600000")
	wantDec(t, "second price per bottle", h.Rows[1].PricePerStock, "52000")
	if h.Rows[0].PurchaseUnitCode != "karton" || h.Rows[0].ChangePct != nil || h.Rows[1].ChangePct == nil || !h.Rows[1].ChangePct.Equal(dec("4")) {
		t.Fatalf("rows: %+v", h.Rows)
	}
	if h.MinPrice == nil || !h.MinPrice.Equal(dec("50000")) || h.MaxPrice == nil || !h.MaxPrice.Equal(dec("52000")) || h.LastPrice == nil || !h.LastPrice.Equal(dec("52000")) || h.AvgPrice == nil || !h.AvgPrice.Equal(dec("51000")) {
		t.Fatalf("summary: %v %v %v %v", h.MinPrice, h.MaxPrice, h.LastPrice, h.AvgPrice)
	}
	// one supplier only; the item is required and must exist
	hs, err := f.svc.PriceHistory(f.admin, f.propID, ptr(f.item["SIRUP"]), nil, nil, &f.pasar)
	must(t, err)
	if len(hs.Rows) != 1 || hs.Rows[0].SupplierID != f.pasar {
		t.Fatalf("one supplier: %+v", hs.Rows)
	}
	_, err = f.svc.PriceHistory(f.admin, f.propID, nil, nil, nil, nil)
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = f.svc.PriceHistory(f.admin, f.propID, ptr(int64(999999)), nil, nil, nil)
	wantCode(t, err, "ITEM_NOT_FOUND")
}

// ---------------------------------------------------------------- slow and dead stock

func civ(s string) civil.Date { return civil.MustParseDate(s) }

func TestSlowStockKeepsTheItemsNotUsedForTheWindowAndGivesTheirAges(t *testing.T) {
	asOf := civ("2026-10-20")
	row := func(code, lastOut, oldest, lastIn string) inventorydb.SlowStockRow {
		r := inventorydb.SlowStockRow{ItemCode: code, ItemName: code, Qty: dec("5"), Value: dec("5000"), LastOutOn: civ("1900-01-01"), OldestLayerOn: civ("1900-01-01"), LastInOn: civ("1900-01-01")}
		if lastOut != "" {
			r.LastOutOn = civ(lastOut)
		}
		if oldest != "" {
			r.OldestLayerOn = civ(oldest)
		}
		if lastIn != "" {
			r.LastInOn = civ(lastIn)
		}
		return r
	}
	rows := []inventorydb.SlowStockRow{
		row("USED", "2026-10-10", "", "2026-10-01"), // 10 days: not slow
		row("EDGE", "2026-09-20", "", "2026-08-01"), // exactly 30 days: slow
		row("OLD", "2026-06-01", "", "2026-05-01"),  // 141 days
		row("NEVER", "", "", "2026-07-01"),          // never used, in 111 days ago
		row("NEVERNO", "", "", ""),                  // never used and no entry
	}
	rows[2].Value = dec("90000")
	rows[3].Value = dec("70000")

	got, total, never := inventory.SlowStockRows(rows, asOf, 30, "LAST_ENTRY")
	var codes []string
	for _, r := range got {
		codes = append(codes, r.ItemCode)
	}
	// the ones never used come first (the larger value first), then the longest unused
	want := []string{"NEVER", "NEVERNO", "OLD", "EDGE"}
	if len(codes) != len(want) {
		t.Fatalf("items: %v, want %v", codes, want)
	}
	for i := range want {
		if codes[i] != want[i] {
			t.Fatalf("order: %v, want %v", codes, want)
		}
	}
	wantDec(t, "total", total, "170000")
	if never != 2 {
		t.Fatalf("never used: %d", never)
	}
	if got[0].AgeDays == nil || *got[0].AgeDays != 111 || got[1].AgeDays != nil || got[2].DaysSinceOut == nil || *got[2].DaysSinceOut != 141 || *got[3].DaysSinceOut != 30 || !got[0].NeverOut || got[2].NeverOut {
		t.Fatalf("ages: %+v", got)
	}
	// a window of 60 days leaves EDGE out; the age of FIFO stock is the oldest layer
	got, _, _ = inventory.SlowStockRows(rows, asOf, 60, "OLDEST_LAYER")
	if len(got) != 3 {
		t.Fatalf("60 days: %d", len(got))
	}
	rows[2].OldestLayerOn = civ("2026-01-01")
	got, _, _ = inventory.SlowStockRows(rows, asOf, 60, "OLDEST_LAYER")
	for _, r := range got {
		if r.ItemCode == "OLD" && (r.AgeDays == nil || *r.AgeDays != 292) {
			t.Fatalf("the age of the oldest layer: %+v", r)
		}
	}
}

func TestTheSlowStockReportListsTheStockThatNothingConsumedAndReadsTheLedger(t *testing.T) {
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			m := newMovements(t, method)
			m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
			m.open(t, "MAIN", "SIRUP", "botol", "20", "50000")
			m.open(t, "HK", "SOAP", "pcs", "100", "2960")
			m.open(t, "HK", "SHAMPOO", "pcs", "10", "3250")
			m.sale(t, m.pair("MAIN", "BERAS"), "3") // used today
			// an issue that is reversed does not count as a use
			iss, err := m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SHAMPOO", "10"))
			must(t, err)
			_, err = m.svc.ReverseIssue(m.mover, m.propID, iss.ID, "wrong")
			must(t, err)
			// a transfer is not a consumption either
			_, err = m.transfer(t, m.mover, "HK", "MAIN", nil, m.tl("SOAP", "5"))
			must(t, err)

			rep, err := m.svc.SlowStock(m.admin, m.propID, 30, inventory.SlowStockFilter{})
			must(t, err)
			if rep.Days != 30 || rep.CostingMethod != method || rep.AsOf != m.today(t) {
				t.Fatalf("report: %+v", rep)
			}
			wantBasis := "LAST_ENTRY"
			if method == inventory.MethodFIFO {
				wantBasis = "OLDEST_LAYER"
			}
			if rep.AgeBasis != wantBasis {
				t.Fatalf("basis %s, want %s", rep.AgeBasis, wantBasis)
			}
			seen := map[string]inventory.SlowStockRow{}
			for _, r := range rep.Rows {
				seen[r.WarehouseCode+"/"+r.ItemCode] = r
			}
			// the rice was used today: not in the list; the others never left the warehouse
			if _, ok := seen["MAIN/BERAS"]; ok {
				t.Fatalf("a used item: %+v", seen["MAIN/BERAS"])
			}
			for _, k := range []string{"MAIN/SIRUP", "HK/SOAP", "HK/SHAMPOO", "MAIN/SOAP"} {
				r, ok := seen[k]
				if !ok || !r.NeverOut || r.LastOutOn != nil || r.DaysSinceOut != nil || r.AgeDays == nil || *r.AgeDays != 0 {
					t.Fatalf("%s: %+v", k, r)
				}
			}
			wantDec(t, "value", rep.Total, "1328500")
			if rep.NeverOut != 4 {
				t.Fatalf("never used: %d", rep.NeverOut)
			}
			// narrowed to a warehouse
			hk, err := m.svc.SlowStock(m.admin, m.propID, 90, inventory.SlowStockFilter{WarehouseID: ptr(m.wh["HK"])})
			must(t, err)
			if len(hk.Rows) != 2 {
				t.Fatalf("housekeeping: %+v", hk.Rows)
			}
			// the windows are the four of the page
			_, err = m.svc.SlowStock(m.admin, m.propID, 45, inventory.SlowStockFilter{})
			wantCode(t, err, "VALIDATION_FAILED")
			def, err := m.svc.SlowStock(m.admin, m.propID, 0, inventory.SlowStockFilter{})
			must(t, err)
			if def.Days != 90 {
				t.Fatalf("default window: %d", def.Days)
			}
		})
	}
}

// ---------------------------------------------------------------- the largest usage

func TestTheLargestUsageRanksTheItemsByValueWithTheirShareAndNetsAReversal(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	m.open(t, "MAIN", "BERAS", "kg", "40", "15500")
	m.open(t, "BAR", "SIRUP", "botol", "20", "50000")
	m.open(t, "HK", "SOAP", "pcs", "100", "2960")
	use, err := m.issue(m.mover, "HK", inventory.PurposeUse, nil, m.il("SOAP", "5")) // 14,800
	must(t, err)
	_, err = m.issue(m.mover, "MAIN", inventory.PurposeWaste, func(in *inventory.IssueInput) { in.ReasonCode = ptr("SPOILAGE") }, m.il("BERAS", "2")) // 31,000
	must(t, err)
	m.sale(t, m.pair("MAIN", "BERAS"), "3")                                                                                                  // 46,500
	_, err = m.issue(m.mover, "BAR", inventory.PurposeStaffMeal, func(in *inventory.IssueInput) { in.Covers = ptr(10) }, m.il("SIRUP", "2")) // 100,000
	must(t, err)
	_, err = m.issue(m.mover, "BAR", inventory.PurposeEntertainment, func(in *inventory.IssueInput) { in.Covers = ptr(2); in.Beneficiary = ptr("Tamu") }, m.il("SIRUP", "1")) // 50,000
	must(t, err)
	_, err = m.transfer(t, m.mover, "MAIN", "BAR", nil, m.tl("BERAS", "10")) // a transfer is not a use by default
	must(t, err)

	rep, err := m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{})
	must(t, err)
	if rep.Limit != 20 || rep.Items != 3 || len(rep.Rows) != 3 {
		t.Fatalf("report: %+v", rep)
	}
	wantDec(t, "total", rep.Total, "242300")
	wantDec(t, "shown", rep.Shown, "242300")
	syrup, rice, soap := rep.Rows[0], rep.Rows[1], rep.Rows[2]
	if syrup.ItemCode != "SIRUP" || syrup.Rank != 1 || rice.ItemCode != "BERAS" || rice.Rank != 2 || soap.ItemCode != "SOAP" || soap.Rank != 3 {
		t.Fatalf("order: %+v", rep.Rows)
	}
	wantDec(t, "syrup qty", syrup.Qty, "3")
	wantDec(t, "syrup value", syrup.Value, "150000")
	wantDec(t, "syrup share", syrup.SharePct, "61.91")
	wantDec(t, "syrup cumulative", syrup.CumulativePct, "61.91")
	wantDec(t, "rice qty", rice.Qty, "5")
	wantDec(t, "rice value", rice.Value, "77500")
	wantDec(t, "rice share", rice.SharePct, "31.99")
	wantDec(t, "rice cumulative", rice.CumulativePct, "93.89")
	wantDec(t, "soap share", soap.SharePct, "6.11")
	wantDec(t, "soap cumulative", soap.CumulativePct, "100")

	// the kinds asked for, the warehouse and the list size
	waste, err := m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{Kinds: []string{inventory.KindWaste}})
	must(t, err)
	if len(waste.Rows) != 1 || !waste.Total.Equal(dec("31000")) || !waste.Rows[0].SharePct.Equal(dec("100")) {
		t.Fatalf("waste: %+v", waste)
	}
	moved, err := m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{Kinds: []string{inventory.KindTransferOut}})
	must(t, err)
	if len(moved.Rows) != 1 || !moved.Total.Equal(dec("155000")) {
		t.Fatalf("transfers: %+v", moved)
	}
	bar, err := m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{WarehouseID: ptr(m.wh["BAR"])})
	must(t, err)
	if len(bar.Rows) != 1 || bar.Rows[0].ItemCode != "SIRUP" {
		t.Fatalf("the bar: %+v", bar.Rows)
	}
	// a reversed issue nets out of the usage
	_, err = m.svc.ReverseIssue(m.mover, m.propID, use.ID, "counted twice")
	must(t, err)
	net, err := m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{})
	must(t, err)
	wantDec(t, "after the reversal", net.Total, "227500")
	if len(net.Rows) != 2 || net.Rows[1].ItemCode != "BERAS" {
		t.Fatalf("after the reversal: %+v", net.Rows)
	}
	// the list is cut at the limit while the share stays that of the total
	cut, err := m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{Limit: 50})
	must(t, err)
	if cut.Limit != 50 || len(cut.Rows) != 2 {
		t.Fatalf("limit: %+v", cut)
	}
	_, err = m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{Limit: 10})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = m.svc.TopUsage(m.admin, m.propID, nil, nil, inventory.TopUsageFilter{Kinds: []string{"RECEIPT"}})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestThe6bReportsAreForWhoMayReadTheStockAndOnlyInTheirTenant(t *testing.T) {
	m := newMovements(t, inventory.MethodAverage)
	nobody := m.User(t, m.tenantID, m.propID, auth.PermHousekeepingUpdate)
	_, err := m.svc.ItemMovements(nobody, m.propID, nil, nil, inventory.ItemMovementFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.Purchases(nobody, m.propID, nil, nil, inventory.PurchaseFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.PriceHistory(nobody, m.propID, ptr(m.item["BERAS"]), nil, nil, nil)
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.SlowStock(nobody, m.propID, 30, inventory.SlowStockFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = m.svc.TopUsage(nobody, m.propID, nil, nil, inventory.TopUsageFilter{})
	wantCode(t, err, "PERMISSION_DENIED")
	other := m.Tenant(t, "OTH")
	op := m.PropertyIn(t, other.ID, "OTH1", "IDR", 0)
	stranger := m.User(t, other.ID, op.ID, auth.PermInventoryView)
	_, err = m.svc.ItemMovements(stranger, m.propID, nil, nil, inventory.ItemMovementFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	_, err = m.svc.SlowStock(stranger, m.propID, 30, inventory.SlowStockFilter{})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}
