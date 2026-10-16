package inventory_test

import (
	"context"
	"sync"
	"testing"

	"kamarapms/internal/inventory"
)

// ---------------------------------------------------------------- moving average (35-inventory.md 4.2)

func TestAverageReceiptsThenIssuesAndAnOutThatEmptiesTheBalanceTakesAllTheValue(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	p := f.pair("MAIN", "BERAS")
	f.in(t, inventory.KindReceipt, p, "10", "10000")
	f.in(t, inventory.KindReceipt, p, "10", "12000")
	f.wantBal(t, p, "20", "22000") // the average is 1,100
	o := f.out(t, inventory.KindIssue, p, "5")
	if !o.Value.Equal(dec("-5500")) {
		t.Fatalf("issue of 5: value %s, want -5500", o.Value)
	}
	f.wantBal(t, p, "15", "16500")
	o = f.out(t, inventory.KindIssue, p, "15")
	if !o.Value.Equal(dec("-16500")) {
		t.Fatalf("the last 15: value %s, want -16500 (all that is left)", o.Value)
	}
	f.wantBal(t, p, "0", "0")
	f.consistent(t)
}

func TestAverageRoundsEachOutOnceAndTheLastOneTakesTheRest(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decimals int32
		value    string
		want     [3]string
	}{
		{"no decimals", 0, "100", [3]string{"-33", "-34", "-33"}},          // 100/3 = 33.33 -> 33; 67/2 = 33.5 -> 34 (half away from zero); the last takes 33
		{"two decimals", 2, "10.00", [3]string{"-3.33", "-3.34", "-3.33"}}, // 6.67/2 = 3.335 -> 3.34
		{"three decimals", 3, "1.000", [3]string{"-0.333", "-0.334", "-0.333"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFx(t, "AVERAGE", tc.decimals)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "3", tc.value)
			for i, want := range tc.want {
				o := f.out(t, inventory.KindIssue, p, "1")
				if !o.Value.Equal(dec(want)) {
					t.Fatalf("out %d: value %s, want %s", i+1, o.Value, want)
				}
			}
			f.wantBal(t, p, "0", "0")
			f.consistent(t)
		})
	}
}

func TestAMovementIsValuedFromTheUnitItWasBoughtIn(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	// SIRUP: karton = 12 botol, 1 ml = 1/650 botol (35-inventory.md 8.1)
	_, err := f.svc.SetItemUnits(f.admin, f.propID, f.item["SIRUP"], []inventory.ItemUnit{
		{UnitID: f.unit["botol"], StockNum: 1, StockDen: 1, IsPurchase: true, IsRecipe: false, IsDefaultPurchase: false},
		{UnitID: f.unit["karton"], StockNum: 12, StockDen: 1, IsPurchase: true, IsDefaultPurchase: true},
		{UnitID: f.unit["ml"], StockNum: 1, StockDen: 650, IsRecipe: true, IsDefaultRecipe: true},
	})
	must(t, err)
	o, err := f.svc.PostOpening(f.admin, f.propID, inventory.OpeningInput{ClientUUID: "11111111-1111-4111-8111-111111111111", WarehouseID: f.wh["MAIN"],
		Lines: []inventory.OpeningLineInput{{ItemID: f.item["SIRUP"], UnitID: f.unit["karton"], Qty: dec("5"), UnitCost: dec("600000")}}})
	must(t, err)
	if !o.Lines[0].Qty.Equal(dec("60")) || !o.Lines[0].Value.Equal(dec("3000000")) || !o.TotalValue.Equal(dec("3000000")) {
		t.Fatalf("5 karton at 600,000: %+v", o.Lines[0])
	}
	f.wantBal(t, f.pair("MAIN", "SIRUP"), "60", "3000000")
	if got := inventory.StockQty(dec("100"), 1, 650); !got.Equal(dec("0.153846")) {
		t.Fatalf("100 ml in botol: %s", got)
	}
	if got := inventory.StockQty(dec("5"), 12, 1); !got.Equal(dec("60")) {
		t.Fatalf("5 karton in botol: %s", got)
	}
}

// ---------------------------------------------------------------- FIFO (4.3)

func TestFIFOTakesTheOldestLayersAndKeepsTheLayersAndTheBalanceInStep(t *testing.T) {
	f := newFx(t, "FIFO", 0)
	p := f.pair("MAIN", "SIRUP")
	f.in(t, inventory.KindReceipt, p, "20", "960000")  // L1: 48,000 each
	f.in(t, inventory.KindReceipt, p, "60", "3000000") // L2: 50,000 each
	o := f.out(t, inventory.KindIssue, p, "30")        // 20 from L1 + 10 from L2 (8.1)
	if !o.Value.Equal(dec("-1460000")) {
		t.Fatalf("30 out: value %s, want -1460000", o.Value)
	}
	f.wantBal(t, p, "50", "2500000")
	if l := f.layers(t, p); len(l) != 1 || !l[0].qty.Equal(dec("50")) || !l[0].value.Equal(dec("2500000")) {
		t.Fatalf("layers: %+v", l)
	}
	f.consistent(t)
	o = f.out(t, inventory.KindIssue, p, "50")
	if !o.Value.Equal(dec("-2500000")) {
		t.Fatalf("the last 50: %s", o.Value)
	}
	f.wantBal(t, p, "0", "0")
	if l := f.layers(t, p); len(l) != 0 {
		t.Fatalf("layers left: %+v", l)
	}
	f.consistent(t)
}

func TestFIFOAPartOfALayerIsValuedProportionallyAndTheLastPieceTakesWhatIsLeft(t *testing.T) {
	f := newFx(t, "FIFO", 0)
	p := f.pair("MAIN", "BERAS")
	f.in(t, inventory.KindReceipt, p, "3", "100")
	for i, want := range []string{"-33", "-34", "-33"} {
		if o := f.out(t, inventory.KindIssue, p, "1"); !o.Value.Equal(dec(want)) {
			t.Fatalf("out %d: %s, want %s", i+1, o.Value, want)
		}
	}
	f.wantBal(t, p, "0", "0")
	f.consistent(t)
}

// ---------------------------------------------------------------- stock sold below zero (4.4, 8.4)

func TestStockSoldBelowZeroIsCostedAtTheEstimateAndSettledWhenGoodsComeIn(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "2", "28000") // 14,000 a kilo
			o := f.sale(t, p, "3")
			if !o.Value.Equal(dec("-42000")) || !o.ShortQty.Equal(dec("1")) || o.Estimated {
				t.Fatalf("the sale: %+v", o)
			}
			f.wantBal(t, p, "-1", "-14000")
			var qty, est, estValue string
			must(t, f.Pool.QueryRow(context.Background(), `SELECT qty_open::text, est_unit_cost::text, est_value_open::text FROM inv_shortfalls WHERE status = 'OPEN'`).Scan(&qty, &est, &estValue))
			if !dec(qty).Equal(dec("1")) || !dec(est).Equal(dec("14000")) || !dec(estValue).Equal(dec("14000")) {
				t.Fatalf("the shortfall: %s / %s / %s", qty, est, estValue)
			}
			f.consistent(t)

			// the goods come in at 15,500: 10 kg, 155,000. The kilo that was not there is settled: -1,500
			in := f.in(t, inventory.KindTransferIn, p, "10", "155000")
			if len(in.Settled) != 1 || !in.Settled[0].Value.Equal(dec("-1500")) || !in.Settled[0].Qty.Equal(dec("1")) {
				t.Fatalf("settled: %+v", in.Settled)
			}
			f.wantBal(t, p, "9", "139500")
			if method == "FIFO" {
				if l := f.layers(t, p); len(l) != 1 || !l[0].qty.Equal(dec("9")) || !l[0].value.Equal(dec("139500")) {
					t.Fatalf("FIFO layers after the settlement: %+v", l)
				}
			}
			var open int
			must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_shortfalls WHERE status = 'OPEN'`).Scan(&open))
			if open != 0 {
				t.Fatalf("%d shortfalls still open", open)
			}
			// the cost of the plates: 28,000 + 42,000... the NEG_SETTLE row carries the difference, journaled Dr cost of sales / Cr inventory
			var kind string
			var value, amount string
			var debit, credit int64
			must(t, f.Pool.QueryRow(context.Background(), `SELECT kind, value_delta::text, gl_amount::text, gl_debit_account_id, gl_credit_account_id FROM inv_movements WHERE kind = 'NEG_SETTLE'`).Scan(&kind, &value, &amount, &debit, &credit))
			if !dec(value).Equal(dec("-1500")) || !dec(amount).Equal(dec("1500")) || debit != f.accounts["5210"] || credit != f.accounts["1310"] {
				t.Fatalf("the settlement row: %s %s %d %d", value, amount, debit, credit)
			}
			f.consistent(t)
		})
	}
}

func TestAShortfallSettledByGoodsThatCostLessGivesBackTheDifference(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "2", "28000")
			f.sale(t, p, "3") // 1 kilo short at 14,000
			in := f.in(t, inventory.KindReceipt, p, "10", "130000")
			if len(in.Settled) != 1 || !in.Settled[0].Value.Equal(dec("1000")) { // the kilo cost 13,000, not 14,000: +1,000
				t.Fatalf("settled: %+v", in.Settled)
			}
			f.wantBal(t, p, "9", "117000") // 13,000 each
			var debit, credit int64
			must(t, f.Pool.QueryRow(context.Background(), `SELECT gl_debit_account_id, gl_credit_account_id FROM inv_movements WHERE kind = 'NEG_SETTLE'`).Scan(&debit, &credit))
			if debit != f.accounts["1310"] || credit != f.accounts["5210"] {
				t.Fatalf("a settlement upwards is Dr inventory / Cr cost of sales: %d %d", debit, credit)
			}
			f.consistent(t)
		})
	}
}

func TestSeveralSalesBelowZeroAreSettledOldestFirstAndPartly(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "2", "28000")
			f.sale(t, p, "3") // short 1 at 14,000
			f.sale(t, p, "2") // short 2 more at 14,000
			f.wantBal(t, p, "-3", "-42000")
			in := f.in(t, inventory.KindReceipt, p, "2", "31000") // 15,500 each: covers the first (1) and 1 of the second
			if len(in.Settled) != 2 || !in.Settled[0].Qty.Equal(dec("1")) || !in.Settled[1].Qty.Equal(dec("1")) {
				t.Fatalf("settled: %+v", in.Settled)
			}
			f.wantBal(t, p, "-1", "-14000")
			f.consistent(t)
			var open string
			must(t, f.Pool.QueryRow(context.Background(), `SELECT qty_open::text FROM inv_shortfalls WHERE status = 'OPEN'`).Scan(&open))
			if !dec(open).Equal(dec("1")) {
				t.Fatalf("open: %s", open)
			}
		})
	}
}

func TestOnlyASaleMayTakeStockBelowZero(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	p := f.pair("MAIN", "BERAS")
	f.in(t, inventory.KindReceipt, p, "2", "28000")
	for _, kind := range []string{inventory.KindIssue, inventory.KindWaste, inventory.KindStaffMeal, inventory.KindEntertainment, inventory.KindTransferOut, inventory.KindSupplierReturn, inventory.KindCountLoss} {
		_, err := f.tryOut(kind, p, "3")
		wantCode(t, err, "NEGATIVE_STOCK_NOT_ALLOWED")
	}
	f.wantBal(t, p, "2", "28000")
	f.consistent(t)
}

func TestTheEstimateComesFromTheLastCostThenTheReferenceCostThenTheNewestReceiptThenZero(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	// 1. the last unit cost of the balance
	p := f.pair("MAIN", "BERAS")
	f.in(t, inventory.KindReceipt, p, "1", "20000")
	if o := f.sale(t, p, "2"); !o.Value.Equal(dec("-40000")) || o.Estimated {
		t.Fatalf("last cost: %+v", o)
	}
	// 2. no cost in the balance: the reference cost of the item
	_, err := f.svc.UpdateItem(f.admin, f.propID, f.item["SIRUP"], inventory.ItemInput{Name: "Sirup", CategoryID: f.cat["BEV"], StockUnitID: f.unit["botol"], ReferenceCost: ptr(dec("49000")), Version: 1})
	must(t, err)
	if o := f.sale(t, f.pair("BAR", "SIRUP"), "2"); !o.Value.Equal(dec("-98000")) || o.Estimated {
		t.Fatalf("reference cost: %+v", o)
	}
	// 3. no reference cost: the newest receipt of the item in another warehouse
	f.newItem(t, "TEAB", "Tea", "BEV", "kg")
	f.in(t, inventory.KindReceipt, f.pair("MAIN", "TEAB"), "10", "55000")
	if o := f.sale(t, f.pair("BAR", "TEAB"), "2"); !o.Value.Equal(dec("-11000")) || o.Estimated {
		t.Fatalf("newest receipt: %+v", o)
	}
	// 4. nothing: zero, and the movement is flagged
	f.newItem(t, "GULA", "Gula", "FOOD", "kg")
	if o := f.sale(t, f.pair("BAR", "GULA"), "2"); !o.Value.IsZero() || !o.Estimated {
		t.Fatalf("no cost at all: %+v", o)
	}
	var flagged int
	must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_movements WHERE cost_estimated`).Scan(&flagged))
	if flagged != 1 {
		t.Fatalf("%d flagged movements", flagged)
	}
}

// ---------------------------------------------------------------- reversal and adjustment

func TestAReceiptIsReversedOnceAndTakesItsLayerAndItsValueWithIt(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "5", "50000")
			rcv := f.in(t, inventory.KindReceipt, p, "10", "120000")
			var back inventory.Posted
			must(t, f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
				var e error
				back, e = st.Reverse(ctx, inventory.ReverseArgs{MovementID: rcv.MovementID, Kind: inventory.KindReceiptReversal, Ref: f.ref()})
				return e
			}))
			if !back.Qty.Equal(dec("-10")) || !back.Value.Equal(dec("-120000")) {
				t.Fatalf("reversal: %+v", back)
			}
			f.wantBal(t, p, "5", "50000")
			f.consistent(t)
			var swapped bool
			must(t, f.Pool.QueryRow(context.Background(), `SELECT r.reverses_id = $1 AND r.qty_delta = -m.qty_delta AND r.value_delta = -m.value_delta FROM inv_movements r JOIN inv_movements m ON m.id = r.reverses_id WHERE r.id = $2`,
				rcv.MovementID, back.MovementID).Scan(&swapped))
			if !swapped {
				t.Fatal("the reversing row does not negate the movement")
			}
			err := f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
				_, e := st.Reverse(ctx, inventory.ReverseArgs{MovementID: rcv.MovementID, Kind: inventory.KindReceiptReversal, Ref: f.ref()})
				return e
			})
			wantCode(t, err, "MOVEMENT_ALREADY_REVERSED")
		})
	}
}

func TestAReceiptThatWasUsedCannotBeReversed(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			rcv := f.in(t, inventory.KindReceipt, p, "10", "100000")
			f.out(t, inventory.KindIssue, p, "2")
			err := f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
				_, e := st.Reverse(ctx, inventory.ReverseArgs{MovementID: rcv.MovementID, Kind: inventory.KindReceiptReversal, Ref: f.ref()})
				return e
			})
			if method == "FIFO" {
				wantCode(t, err, "MOVEMENT_NOT_REVERSIBLE") // part of its layer was used
			} else {
				wantCode(t, err, "NEGATIVE_STOCK_NOT_ALLOWED") // 8 are left, 10 would go
			}
			f.wantBal(t, p, "8", "80000")
		})
	}
}

func TestAnIssueIsBroughtBackIntoTheLayersItCameFrom(t *testing.T) {
	f := newFx(t, "FIFO", 0)
	p := f.pair("MAIN", "SIRUP")
	f.in(t, inventory.KindReceipt, p, "20", "960000")
	f.in(t, inventory.KindReceipt, p, "60", "3000000")
	issue := f.out(t, inventory.KindIssue, p, "30")
	f.wantBal(t, p, "50", "2500000")
	var back inventory.Posted
	must(t, f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
		var e error
		back, e = st.Reverse(ctx, inventory.ReverseArgs{MovementID: issue.MovementID, Kind: inventory.KindPosSaleReturn, Ref: f.ref()})
		return e
	}))
	if !back.Value.Equal(dec("1460000")) || !back.Qty.Equal(dec("30")) {
		t.Fatalf("reversal: %+v", back)
	}
	f.wantBal(t, p, "80", "3960000")
	if l := f.layers(t, p); len(l) != 2 || !l[0].qty.Equal(dec("20")) || !l[0].value.Equal(dec("960000")) || !l[1].qty.Equal(dec("60")) {
		t.Fatalf("layers: %+v", l)
	}
	f.consistent(t)
	// once
	err := f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
		_, e := st.Reverse(ctx, inventory.ReverseArgs{MovementID: issue.MovementID, Kind: inventory.KindPosSaleReturn, Ref: f.ref()})
		return e
	})
	wantCode(t, err, "MOVEMENT_ALREADY_REVERSED")
}

func TestASaleBelowZeroThatNobodySettledCanBeReversedAndItsShortfallGoes(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "2", "28000")
			sale := f.sale(t, p, "3")
			must(t, f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
				_, e := st.Reverse(ctx, inventory.ReverseArgs{MovementID: sale.MovementID, Kind: inventory.KindPosSaleReturn, Ref: f.ref()})
				return e
			}))
			f.wantBal(t, p, "2", "28000")
			var open int
			must(t, f.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_shortfalls WHERE status = 'OPEN'`).Scan(&open))
			if open != 0 {
				t.Fatalf("%d open shortfalls", open)
			}
			f.consistent(t)
		})
	}
}

func TestACostAdjustmentMovesValueOnHandWithoutMovingQuantity(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "SIRUP")
			rcv := f.in(t, inventory.KindReceipt, p, "60", "3000000")
			must(t, f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
				_, e := st.Adjust(ctx, inventory.AdjustArgs{Pair: p, Value: dec("50000"), Ref: f.ref(), ForMovementID: rcv.MovementID})
				return e
			}))
			f.wantBal(t, p, "60", "3050000")
			f.consistent(t)
			// nothing on hand to take it: refused
			q := f.pair("BAR", "SIRUP")
			err := f.tx([]inventory.Pair{q}, func(ctx context.Context, st *inventory.Stock) error {
				_, e := st.Adjust(ctx, inventory.AdjustArgs{Pair: q, Value: dec("1000"), Ref: f.ref(), ForMovementID: rcv.MovementID})
				return e
			})
			wantCode(t, err, "COST_ADJUST_NOT_APPLICABLE")
		})
	}
}

// ---------------------------------------------------------------- the writer insists on the locks

func TestAPairThatWasNotLockedCannotBeWritten(t *testing.T) {
	f := newFx(t, "AVERAGE", 0)
	err := f.tx([]inventory.Pair{f.pair("MAIN", "BERAS")}, func(ctx context.Context, st *inventory.Stock) error {
		_, e := st.In(ctx, inventory.InArgs{Pair: f.pair("MAIN", "SIRUP"), Kind: inventory.KindReceipt, Qty: dec("1"), Value: dec("1"), Ref: f.ref()})
		return e
	})
	if err == nil {
		t.Fatal("a write on a pair that was not locked was accepted")
	}
	// and outside a transaction nothing can be locked at all
	_, err = f.ledger.Lock(context.Background(), nil, inventory.Book{}, []inventory.Pair{f.pair("MAIN", "BERAS")}, inventory.LockOptions{})
	if err == nil {
		t.Fatal("the stock was locked outside a transaction")
	}
}

// ---------------------------------------------------------------- races

// Two transactions take 7 kg each from a shelf of 10 kg at the same moment: the balance row is locked, so exactly one gets it and the other is refused.
func TestTwoIssuesAtOnceCannotTakeMoreThanTheShelfHolds(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "5", "50000")
			f.in(t, inventory.KindReceipt, p, "5", "60000")
			var wg sync.WaitGroup
			barrier := make(chan struct{})
			errs := make([]error, 2)
			for i := range errs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-barrier
					_, errs[i] = f.tryOut(inventory.KindIssue, p, "7")
				}()
			}
			close(barrier)
			wg.Wait()
			ok, refused := 0, 0
			for _, e := range errs {
				if e == nil {
					ok++
					continue
				}
				wantCode(t, e, "NEGATIVE_STOCK_NOT_ALLOWED")
				refused++
			}
			if ok != 1 || refused != 1 {
				t.Fatalf("%d taken, %d refused: %v", ok, refused, errs)
			}
			if method == "AVERAGE" {
				f.wantBal(t, p, "3", "33000") // 7 at the average of 11,000
			} else {
				f.wantBal(t, p, "3", "36000") // the first layer (5) and 2 of the second, 12,000 each
			}
			f.consistent(t)
		})
	}
}

// Two issues that both fit take their layers one after the other: the layers and the balance agree afterwards.
func TestTwoIssuesAtOnceKeepTheFIFOLayersInStep(t *testing.T) {
	f := newFx(t, "FIFO", 0)
	p := f.pair("MAIN", "BERAS")
	f.in(t, inventory.KindReceipt, p, "5", "50000")
	f.in(t, inventory.KindReceipt, p, "5", "60000")
	var wg sync.WaitGroup
	barrier := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-barrier
			if _, err := f.tryOut(inventory.KindIssue, p, "4"); err != nil {
				t.Error(err)
			}
		}()
	}
	close(barrier)
	wg.Wait()
	f.wantBal(t, p, "2", "24000") // the 4 + 4 came from the first layer (5 @ 10,000) and then 3 of the second (12,000 each)
	f.consistent(t)
}

// A sale below zero and a receipt at the same time: whichever is first, the shortfall ends settled and the books agree.
func TestASaleAndAReceiptAtOnceEndInTheSameBooks(t *testing.T) {
	for _, method := range []string{"AVERAGE", "FIFO"} {
		t.Run(method, func(t *testing.T) {
			f := newFx(t, method, 0)
			p := f.pair("MAIN", "BERAS")
			f.in(t, inventory.KindReceipt, p, "2", "28000")
			var wg sync.WaitGroup
			barrier := make(chan struct{})
			wg.Add(2)
			go func() { defer wg.Done(); <-barrier; f.sale(t, p, "3") }()
			go func() { defer wg.Done(); <-barrier; f.in(t, inventory.KindTransferIn, p, "10", "155000") }()
			close(barrier)
			wg.Wait()
			f.consistent(t)
			q, _ := f.bal(t, p)
			if !dec(q).Equal(dec("9")) {
				t.Fatalf("quantity %s, want 9", q)
			}
		})
	}
}
