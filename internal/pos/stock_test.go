package pos_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/pos"
)

// The deduction of a POS sale from stock (docs/architecture/35-inventory.md 5.6, PR 4), on a real database: the hook of the POS, the queue, the worker, the voids, the count and the close.

// testStock is the adapter of internal/app without the plan: the POS tells the stock.
type testStock struct{ inv *inventory.Service }

func (h testStock) QueueSale(ctx context.Context, tenantID, propertyID int64, sale pos.StockSale) error {
	in := inventory.PosSale{BillLineID: sale.BillLineID, OutletID: sale.OutletID, ProductID: sale.ProductID, Quantity: sale.Quantity, BusinessDate: sale.BusinessDate}
	for _, o := range sale.Options {
		in.Options = append(in.Options, inventory.PosOption{OptionID: o.OptionID, Qty: o.Qty})
	}
	return h.inv.QueueSale(ctx, tenantID, propertyID, in)
}

func (h testStock) QueueVoid(ctx context.Context, tenantID, propertyID int64, v pos.StockVoid) error {
	return h.inv.QueueVoid(ctx, tenantID, propertyID, inventory.PosVoid{BillLineID: v.BillLineID, Waste: v.Waste, BusinessDate: v.BusinessDate})
}

// testPricer is the answer of the POS to the net price of a product, as internal/app wires it.
type testPricer struct{ pos *pos.Service }

func (p testPricer) NetPrice(ctx context.Context, tenantID, propertyID, outletID, productID int64) (decimal.Decimal, bool, error) {
	return p.pos.NetPrice(ctx, tenantID, propertyID, outletID, productID)
}

type stockFx struct {
	*billFx
	inv    *inventory.Service
	method string
	unit   map[string]int64
	item   map[string]int64
	wh     int64
	nasgor pos.Product
	opt    map[string]int64
	cnt    context.Context
	apr    context.Context
}

// newStockFx makes the restaurant outlet sell from the warehouse KITCHEN (class FOOD), with rice, eggs and oil in stock and the recipe of "Nasi Goreng": 200 g of rice; the variant Jumbo takes 300 g of rice instead;
// the modifier Telur ceplok adds 1 egg and 10 ml of oil. Rice 10 kg at 17,000, eggs 100 at 2,000, oil 5 l at 25,000.
func newStockFx(t *testing.T, method string) *stockFx {
	t.Helper()
	b := newBillFx(t)
	s := &stockFx{billFx: b, method: method, unit: map[string]int64{}, item: map[string]int64{}, opt: map[string]int64{}}
	s.inv = inventory.NewService(b.TxM, b.Clock, audit.NewWriter(b.Clock), iam.NewAuthorizer(b.TxM), b.Tenancy, b.Departments)
	b.svc.SetStock(testStock{s.inv})
	s.inv.SetApprover(b.IAM)
	s.inv.SetPricer(testPricer{b.svc})
	ctx, pid := b.admin, b.propID
	st, err := s.inv.GetSettings(ctx, pid)
	must(t, err)
	_, err = s.inv.UpdateSettings(ctx, pid, inventory.SettingsInput{CostingMethod: method, PriceTolerancePct: st.PriceTolerancePct, QtyTolerancePct: st.QtyTolerancePct, OverReceiptPct: st.OverReceiptPct,
		PoRequiresPr: st.PoRequiresPr, CountBlocksNightAudit: st.CountBlocksNightAudit, CountBlind: st.CountBlind, Version: st.Version})
	must(t, err)
	for _, u := range [][2]string{{"kg", "Kilogram"}, {"g", "Gram"}, {"pcs", "Pieces"}, {"l", "Liter"}, {"ml", "Mililiter"}} {
		r, err := s.inv.CreateUnit(ctx, pid, inventory.UnitInput{Code: u[0], Name: u[1]})
		must(t, err)
		s.unit[u[0]] = r.ID
	}
	acc := map[string]int64{}
	rows, err := b.Pool.Query(context.Background(), `SELECT code, id FROM gl_accounts WHERE property_id = $1`, pid)
	must(t, err)
	for rows.Next() {
		var c string
		var id int64
		must(t, rows.Scan(&c, &id))
		acc[c] = id
	}
	rows.Close()
	food, err := s.inv.CreateCategory(ctx, pid, inventory.CategoryInput{Code: "FOOD", Name: "Food", Kind: "FOOD", InventoryAccountID: acc["1310"], CogsAccountID: ptr(acc["5210"])})
	must(t, err)
	mk := func(code, unit string, extra ...inventory.ItemUnit) int64 {
		it, err := s.inv.CreateItem(ctx, pid, inventory.ItemInput{Code: code, Name: code, CategoryID: food.ID, StockUnitID: s.unit[unit], Units: extra})
		must(t, err)
		s.item[code] = it.ID
		return it.ID
	}
	mk("BERAS", "kg", inventory.ItemUnit{UnitID: s.unit["g"], StockNum: 1, StockDen: 1000, IsRecipe: true})
	mk("TELUR", "pcs")
	mk("MINYAK", "l", inventory.ItemUnit{UnitID: s.unit["ml"], StockNum: 1, StockDen: 1000, IsRecipe: true})
	w, err := s.inv.CreateWarehouse(ctx, pid, inventory.WarehouseInput{Code: "KITCHEN", Name: "Kitchen", Kind: "OUTLET", OutletID: &b.resto.ID, CostClass: "FOOD"})
	must(t, err)
	s.wh = w.ID
	lines := []inventory.OpeningLineInput{{ItemID: s.item["BERAS"], UnitID: s.unit["kg"], Qty: dec("10"), UnitCost: dec("17000")}, {ItemID: s.item["TELUR"], UnitID: s.unit["pcs"], Qty: dec("100"), UnitCost: dec("2000")},
		{ItemID: s.item["MINYAK"], UnitID: s.unit["l"], Qty: dec("5"), UnitCost: dec("25000")}}
	_, err = s.inv.PostOpening(ctx, pid, inventory.OpeningInput{ClientUUID: uid(9001), WarehouseID: w.ID, Lines: lines})
	must(t, err)
	s.setOutlet(t, &w.ID, "PRINT", nil)
	// the product, with its variant and modifier, and its recipe
	food2 := b.chargeCode(t, "RESTAURANT")
	p, err := b.svc.CreateProduct(ctx, pid, pos.ProductInput{Code: "NASGOR", Name: "Nasi Goreng", CategoryID: b.k.cat.ID, ChargeCodeID: food2,
		OptionGroups: []pos.OptionGroup{
			{Name: "Portion", Kind: "VARIANT", MinSelect: 1, MaxSelect: 1, Options: []pos.Option{{Name: "Regular", MaxQty: 1, IsDefault: true}, {Name: "Jumbo", PriceDelta: dec("20000"), MaxQty: 1}}},
			{Name: "Extras", Kind: "MODIFIER", MinSelect: 0, MaxSelect: 3, Options: []pos.Option{{Name: "Telur ceplok", PriceDelta: dec("8000"), MaxQty: 2}}},
		},
		Prices: []pos.ProductPriceIn{{PriceListID: b.k.priceList.ID, Price: dec("85000"), IsSold: true}}})
	must(t, err)
	s.nasgor = p
	for _, g := range p.OptionGroups {
		for _, o := range g.Options {
			s.opt[o.Name] = o.ID
		}
	}
	r := func(opt string, item string, unit string, qty string) inventory.RecipeLineInput {
		in := inventory.RecipeLineInput{ItemID: s.item[item], UnitID: s.unit[unit], Quantity: dec(qty)}
		if opt != "" {
			id := s.opt[opt]
			in.OptionID = &id
		}
		return in
	}
	_, err = s.inv.SaveRecipe(ctx, pid, p.ID, inventory.RecipeInput{Lines: []inventory.RecipeLineInput{
		r("", "BERAS", "g", "200"), r("Jumbo", "BERAS", "g", "300"), r("Telur ceplok", "TELUR", "pcs", "1"), r("Telur ceplok", "MINYAK", "ml", "10")}})
	must(t, err)
	s.cnt = b.User(t, b.tenantID, b.propID, auth.PermInventoryCount, auth.PermInventoryView)
	s.apr = b.User(t, b.tenantID, b.propID, auth.PermInventoryApprove, auth.PermInventoryView)
	return s
}

// setOutlet changes the stock settings of the restaurant outlet and keeps the rest of its form.
func (s *stockFx) setOutlet(t *testing.T, warehouse *int64, mode string, voidMinutes *int) {
	t.Helper()
	o, err := s.svc.GetOutlet(s.admin, s.propID, s.resto.ID)
	must(t, err)
	_, err = s.svc.UpdateOutlet(s.admin, s.propID, o.ID, pos.OutletInput{Code: o.Code, Name: o.Name, OutletType: o.OutletType, OrderMode: mode, DepartmentID: o.DepartmentID, RuleProfileID: o.RuleProfileID,
		PriceListID: o.PriceListID, Drawer: o.Drawer, UsesTables: o.UsesTables, AllowRoomCharge: o.AllowRoomCharge, AllowComp: o.AllowComp, CompTreatment: o.CompTreatment, RoundingUnit: o.RoundingUnit,
		AllowCompanyCharge: o.AllowCompanyCharge, Version: o.Version, WarehouseID: warehouse, VoidWasteAfterMinutes: voidMinutes})
	must(t, err)
	s.resto, err = s.svc.GetOutlet(s.admin, s.propID, o.ID)
	must(t, err)
}

func (s *stockFx) sell(t *testing.T, p pos.Product, qty string, opts ...string) pos.Bill {
	t.Helper()
	bill := s.open(t, s.cashier, nil)
	s.seq++
	in := pos.AddLineInput{ClientUUID: uid(1000 + s.seq), ProductID: p.ID, Quantity: dec(qty)}
	if p.ID == s.nasgor.ID { // the portion is a variant the guest must choose: Regular unless the test asks for Jumbo
		jumbo := false
		for _, o := range opts {
			jumbo = jumbo || o == "Jumbo"
		}
		if !jumbo {
			opts = append([]string{"Regular"}, opts...)
		}
	}
	for _, o := range opts {
		in.Options = append(in.Options, pos.LineOptionInput{OptionID: s.opt[o], Qty: 1})
	}
	bill, err := s.svc.AddLine(s.cashier, s.propID, bill.ID, in)
	must(t, err)
	return s.send(t, s.cashier, bill)
}

func (s *stockFx) work(t *testing.T) int {
	t.Helper()
	n, err := s.inv.RunQueue(context.Background())
	must(t, err)
	return n
}

func (s *stockFx) balance(t *testing.T, item string) (qty, value string) {
	t.Helper()
	must(t, s.Pool.QueryRow(context.Background(), `SELECT qty::text, value::text FROM inv_balances WHERE warehouse_id = $1 AND item_id = $2`, s.wh, s.item[item]).Scan(&qty, &value))
	return qty, value
}

func (s *stockFx) wantBalance(t *testing.T, item, qty, value string) {
	t.Helper()
	q, v := s.balance(t, item)
	if !dec(q).Equal(dec(qty)) || !dec(v).Equal(dec(value)) {
		t.Fatalf("%s: balance %s / %s, want %s / %s", item, q, v, qty, value)
	}
}

func (s *stockFx) queueRows(t *testing.T, where string, args ...any) int {
	t.Helper()
	var n int
	must(t, s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_consumption_queue WHERE `+where, args...).Scan(&n))
	return n
}

func bothMethods(t *testing.T, fn func(t *testing.T, method string)) {
	t.Helper()
	for _, m := range []string{inventory.MethodAverage, inventory.MethodFIFO} {
		t.Run(m, func(t *testing.T) { fn(t, m) })
	}
}

func TestASoldLineQueuesOneRowWithTheSnapshotOfTheRecipe(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	// 2 jumbo with a fried egg: rice 2 x 0.3 kg replaces the 0.2; the egg adds 1 egg and 10 ml (0.01 l) each
	bill := s.sell(t, s.nasgor, "2", "Jumbo", "Telur ceplok")
	if s.queueRows(t, `status = 'PENDING' AND kind = 'SALE' AND sale_class = 'FOOD'`) != 1 {
		t.Fatal("a row for the line")
	}
	var comps string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT components::text FROM inv_consumption_queue`).Scan(&comps))
	want := map[string]string{"BERAS": "0.6", "TELUR": "2", "MINYAK": "0.02"}
	rows, err := s.Pool.Query(context.Background(), `SELECT i.code, (c->>'qty_stock')::numeric::text FROM inv_consumption_queue q, jsonb_array_elements(q.components) c JOIN inv_items i ON i.id = (c->>'item_id')::bigint`)
	must(t, err)
	got := map[string]string{}
	for rows.Next() {
		var code, q string
		must(t, rows.Scan(&code, &q))
		got[code] = q
	}
	rows.Close()
	for k, v := range want {
		if !dec(got[k]).Equal(dec(v)) {
			t.Fatalf("components %v, want %v", got, want)
		}
	}
	// the same line again: nothing is queued twice (a replayed send)
	_, err = s.svc.SendBill(s.cashier, s.propID, bill.ID, pos.SendInput{ClientUUID: uid(2000 + s.seq)})
	must(t, err)
	if s.queueRows(t, `true`) != 1 {
		t.Fatal("a replayed request queues nothing twice")
	}
	// a product with no recipe queues nothing, and neither does an outlet with no warehouse
	s.sell(t, s.steak, "1")
	if s.queueRows(t, `true`) != 1 {
		t.Fatal("a product with no recipe queues nothing")
	}
	s.setOutlet(t, nil, "PRINT", nil)
	s.sell(t, s.nasgor, "1")
	if s.queueRows(t, `true`) != 1 {
		t.Fatal("an outlet with no warehouse deducts nothing")
	}
}

func TestADirectOutletQueuesAtPaymentAndABillThatWasNeverSentToo(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	s.setOutlet(t, &s.wh, "DIRECT", nil)
	s.shift(t)
	bill := s.open(t, s.cashier, nil)
	s.seq++
	bill, err := s.svc.AddLine(s.cashier, s.propID, bill.ID, pos.AddLineInput{ClientUUID: uid(1000 + s.seq), ProductID: s.nasgor.ID, Quantity: dec("1"), Options: []pos.LineOptionInput{{OptionID: s.opt["Regular"], Qty: 1}}})
	must(t, err)
	if s.queueRows(t, `true`) != 0 {
		t.Fatal("a draft is not a sale")
	}
	r := s.pay(t, s.cashier, bill, "pay-key-0002", pos.PayLine{Method: "CASH", Amount: bill.Totals.CashPayable})
	_ = r
	if s.queueRows(t, `kind = 'SALE'`) != 1 {
		t.Fatal("the payment of a DIRECT bill posts the line and queues the sale")
	}
}

func TestTheWorkerDeductsAtTheCostOfTheWarehouseInTheClassAndDepartmentOfTheOutlet(t *testing.T) {
	bothMethods(t, func(t *testing.T, method string) {
		s := newStockFx(t, method)
		s.sell(t, s.nasgor, "3") // 3 x 0.2 kg
		if n := s.work(t); n != 1 {
			t.Fatalf("applied %d", n)
		}
		s.wantBalance(t, "BERAS", "9.4", "159800") // 9.4 kg at 17,000
		var kind, class string
		var dept int64
		var debit, credit int64
		var amount string
		must(t, s.Pool.QueryRow(context.Background(), `SELECT kind, cost_class, COALESCE(gl_department_id, 0), gl_debit_account_id, gl_credit_account_id, gl_amount::text FROM inv_movements WHERE source_type = 'POS_QUEUE'`).Scan(&kind, &class, &dept, &debit, &credit, &amount))
		if kind != "POS_SALE" || class != "FOOD" || dept != s.dept || !dec(amount).Equal(dec("10200")) {
			t.Fatalf("movement: %s %s dept %d amount %s", kind, class, dept, amount)
		}
		var code string
		must(t, s.Pool.QueryRow(context.Background(), `SELECT code FROM gl_accounts WHERE id = $1`, debit).Scan(&code))
		if code != "5210" {
			t.Fatalf("a food sale is charged to the cost of food sales, not %s", code)
		}
		if s.queueRows(t, `status = 'APPLIED' AND applied_on IS NOT NULL AND cardinality(movement_ids) = 1`) != 1 {
			t.Fatal("the row is applied with its movement")
		}
	})
}

func TestASaleIsNeverRefusedForLackOfStockAndNeverWaitsForAStockLock(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	// a transaction holds the balance of the rice: the send commits all the same, in a fraction of a second
	hold := make(chan struct{})
	held := make(chan struct{})
	go func() {
		_ = s.TxM.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := s.TxM.DB(ctx).Exec(ctx, `SELECT id FROM inv_balances WHERE warehouse_id = $1 FOR UPDATE`, s.wh); err != nil {
				return err
			}
			close(held)
			<-hold
			return nil
		})
	}()
	<-held
	start := time.Now()
	s.sell(t, s.nasgor, "100") // 100 x 0.2 = 20 kg, with 10 kg on the shelf
	if time.Since(start) > 3*time.Second {
		t.Fatal("the send waited for the lock of the stock")
	}
	close(hold)
	time.Sleep(200 * time.Millisecond)
	if s.work(t) != 1 {
		t.Fatal("the row is applied once the lock is gone")
	}
	s.wantBalance(t, "BERAS", "-10", "-170000")
	if s.Count(t, `SELECT count(*) FROM inv_shortfalls WHERE status = 'OPEN'`) != 1 {
		t.Fatal("what the shelf did not hold is a shortfall, corrected when goods come in")
	}
}

func TestTwoWorkersNeverTakeTheSameRow(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	for i := 0; i < 24; i++ {
		s.sell(t, s.nasgor, "1")
	}
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.inv.RunQueue(context.Background()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	s.work(t) // what a worker skipped because another held it
	if n := s.queueRows(t, `status = 'APPLIED'`); n != 24 {
		t.Fatalf("%d rows applied, want 24", n)
	}
	if s.Count(t, `SELECT count(*) FROM inv_movements WHERE source_type = 'POS_QUEUE' AND kind = 'POS_SALE'`) != 24 {
		t.Fatal("one movement for each row, never two")
	}
	s.wantBalance(t, "BERAS", "5.2", "88400") // 10 - 24 x 0.2
}

func TestAVoidBeforeTheWorkerCancelsTheRowAndNothingMoves(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	bill := s.sell(t, s.nasgor, "1")
	_, err := s.svc.VoidLine(s.cashier, s.propID, bill.ID, bill.Lines[0].ID, pos.VoidLineInput{Reason: "wrong order", Approval: s.superIn})
	must(t, err)
	if s.queueRows(t, `status = 'CANCELLED' AND kind = 'SALE'`) != 1 || s.queueRows(t, `kind <> 'SALE'`) != 0 {
		t.Fatal("the pending row is cancelled and no void row is made")
	}
	if s.work(t) != 0 {
		t.Fatal("nothing to apply")
	}
	s.wantBalance(t, "BERAS", "10", "170000")
}

func TestAVoidWhileTheTicketIsNewReturnsTheGoodsAndAfterCookingItIsWaste(t *testing.T) {
	bothMethods(t, func(t *testing.T, method string) {
		s := newStockFx(t, method)
		a := s.sell(t, s.nasgor, "2") // 0.4 kg, ticket NEW
		b := s.sell(t, s.nasgor, "1") // 0.2 kg, will be READY
		s.work(t)
		s.wantBalance(t, "BERAS", "9.4", "159800")
		// the first: the ticket has not begun: the goods come back at the cost they were taken
		_, err := s.svc.VoidLine(s.cashier, s.propID, a.ID, a.Lines[0].ID, pos.VoidLineInput{Reason: "guest left", Approval: s.superIn})
		must(t, err)
		if s.queueRows(t, `kind = 'VOID_RETURN' AND status = 'PENDING'`) != 1 {
			t.Fatal("a void return row")
		}
		s.work(t)
		s.wantBalance(t, "BERAS", "9.8", "166600")
		if s.Count(t, `SELECT count(*) FROM inv_movements WHERE kind = 'POS_SALE_RETURN'`) != 1 {
			t.Fatal("POS_SALE_RETURN")
		}
		if method == inventory.MethodFIFO {
			var qty, remaining string
			must(t, s.Pool.QueryRow(context.Background(), `SELECT sum(qty_in)::text, sum(qty_remaining)::text FROM inv_layers WHERE warehouse_id = $1 AND item_id = $2`, s.wh, s.item["BERAS"]).Scan(&qty, &remaining))
			if !dec(remaining).Equal(dec("9.8")) {
				t.Fatalf("the layer is restored: %s of %s", remaining, qty)
			}
		}
		// the second: the kitchen made it: the stock stays out and the cost moves to waste
		_, err = s.Pool.Exec(context.Background(), `UPDATE pos_tickets SET status = 'READY', started_at = now(), ready_at = now() WHERE bill_id = $1 AND kind = 'NEW'`, b.ID)
		must(t, err)
		_, err = s.svc.VoidLine(s.cashier, s.propID, b.ID, b.Lines[0].ID, pos.VoidLineInput{Reason: "dropped", Approval: s.superIn})
		must(t, err)
		if s.queueRows(t, `kind = 'VOID_WASTE' AND status = 'PENDING'`) != 1 {
			t.Fatal("a void waste row")
		}
		s.work(t)
		s.wantBalance(t, "BERAS", "9.8", "166600") // not returned
		var debit, credit string
		var amount string
		must(t, s.Pool.QueryRow(context.Background(), `SELECT d.code, c.code, m.gl_amount::text FROM inv_movements m JOIN gl_accounts d ON d.id = m.gl_debit_account_id JOIN gl_accounts c ON c.id = m.gl_credit_account_id WHERE m.kind = 'COGS_TO_WASTE'`).Scan(&debit, &credit, &amount))
		if debit != "5290" || credit != "5210" || !dec(amount).Equal(dec("3400")) {
			t.Fatalf("waste: Dr %s Cr %s %s", debit, credit, amount)
		}
	})
}

func TestAVoidOfADirectOutletReturnsTheGoodsAndAPrintOutletTurnsToWasteAfterTheMinutesItSays(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	// a PRINT outlet never moves its tickets: with the setting at 10 minutes, a void after 15 is waste
	ten := 10
	s.setOutlet(t, &s.wh, "PRINT", &ten)
	early := s.sell(t, s.nasgor, "1")
	late := s.sell(t, s.nasgor, "1")
	s.work(t)
	_, err := s.svc.VoidLine(s.cashier, s.propID, early.ID, early.Lines[0].ID, pos.VoidLineInput{Reason: "x", Approval: s.superIn})
	must(t, err)
	s.Clock.Advance(15 * time.Minute)
	_, err = s.svc.VoidLine(s.cashier, s.propID, late.ID, late.Lines[0].ID, pos.VoidLineInput{Reason: "y", Approval: s.superIn})
	must(t, err)
	if s.queueRows(t, `kind = 'VOID_RETURN'`) != 1 || s.queueRows(t, `kind = 'VOID_WASTE'`) != 1 {
		t.Fatal("the first void is within the minutes (a return), the second after them (waste)")
	}
	// the same with the setting off: always a return
	s2 := newStockFx(t, inventory.MethodAverage)
	x := s2.sell(t, s2.nasgor, "1")
	s2.work(t)
	s2.Clock.Advance(3 * time.Hour)
	_, err = s2.svc.VoidLine(s2.cashier, s2.propID, x.ID, x.Lines[0].ID, pos.VoidLineInput{Reason: "z", Approval: s2.superIn})
	must(t, err)
	if s2.queueRows(t, `kind = 'VOID_RETURN'`) != 1 {
		t.Fatal("the setting is off by default: a void is a return")
	}
}

func (s *stockFx) unmapCogs(t *testing.T) (restore func()) {
	t.Helper()
	var account int64
	must(t, s.Pool.QueryRow(context.Background(), `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key = 'INV_COGS_FOOD' RETURNING account_id`, s.propID).Scan(&account))
	return func() {
		_, err := s.Pool.Exec(context.Background(), `INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES ($1, $2, 'INV_COGS_FOOD', $3)`, s.tenantID, s.propID, account)
		must(t, err)
	}
}

func TestAFailedRowStopsTheCloseAndRetryOrCancelSetsItRight(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	s.sell(t, s.nasgor, "1")
	// the account of the cost of food sales is not mapped when the worker comes: the row fails and says why
	restore := s.unmapCogs(t)
	s.work(t)
	if s.queueRows(t, `status = 'FAILED' AND error_code = 'ACCOUNT_KEY_MISSING'`) != 1 {
		t.Fatal("the row fails with its code")
	}
	s.wantBalance(t, "BERAS", "10", "170000") // nothing of it is in the stock
	day, err := s.Tenancy.CurrentBusinessDay(s.admin, s.propID)
	must(t, err)
	blockers, _, err := s.inv.NightAuditStock(s.admin, s.tenantID, s.propID, day.BusinessDate)
	must(t, err)
	found := false
	for _, f := range blockers {
		if f.Code == inventory.FindQueueFailed && f.Params["n"] == "1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("a failed row blocks the night audit: %+v", blockers)
	}
	p, _ := auth.Require(s.admin)
	err = s.TxM.WithinTx(s.admin, func(ctx context.Context) error { return s.inv.CloseStock(ctx, p, s.propID, day.BusinessDate) })
	wantCode(t, err, "INVENTORY_QUEUE_FAILED")
	// the account is mapped again, and the row is tried again
	restore()
	var id int64
	must(t, s.Pool.QueryRow(context.Background(), `SELECT id FROM inv_consumption_queue`).Scan(&id))
	if _, err := s.inv.RetryQueueRow(s.waiterC, s.propID, id); err == nil {
		t.Fatal("a retry needs inventory.manage")
	}
	row, err := s.inv.RetryQueueRow(s.admin, s.propID, id)
	must(t, err)
	if row.Status != inventory.QueueApplied {
		t.Fatalf("the retry applies it: %+v", row)
	}
	s.wantBalance(t, "BERAS", "9.8", "166600")
	// a row that is given up is cancelled and the stock is left alone
	s.sell(t, s.nasgor, "1")
	s.unmapCogs(t)
	s.work(t)
	must(t, s.Pool.QueryRow(context.Background(), `SELECT id FROM inv_consumption_queue WHERE status = 'FAILED'`).Scan(&id))
	row, err = s.inv.CancelQueueRow(s.admin, s.propID, id)
	must(t, err)
	if row.Status != inventory.QueueCancelled {
		t.Fatalf("cancelled: %+v", row)
	}
	s.wantBalance(t, "BERAS", "9.8", "166600")
}

func TestTheNightAuditDrainsTheQueueBeforeTheJournal(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	s.sell(t, s.nasgor, "5")
	if s.queueRows(t, `status = 'PENDING'`) != 1 {
		t.Fatal("a row waits")
	}
	day, err := s.Tenancy.CurrentBusinessDay(s.admin, s.propID)
	must(t, err)
	p, _ := auth.Require(s.admin)
	must(t, s.TxM.WithinTx(s.admin, func(ctx context.Context) error { return s.inv.CloseStock(ctx, p, s.propID, day.BusinessDate) }))
	if s.queueRows(t, `status = 'APPLIED'`) != 1 {
		t.Fatal("the close applies what waits")
	}
	s.wantBalance(t, "BERAS", "9", "153000")
}

func TestASaleHeldByACountIsAppliedOnTheSideOfTheCountedAtOfItsItem(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	c, err := s.inv.StartCount(s.cnt, s.propID, inventory.CountInput{ClientUUID: uid(9100), WarehouseID: s.wh, Scope: inventory.ScopeAll})
	must(t, err)
	// a sale before the counter reaches the rice: held, and already missing from the shelf
	s.sell(t, s.nasgor, "1")
	s.work(t)
	if s.queueRows(t, `status = 'HELD'`) != 1 {
		t.Fatal("a frozen warehouse holds the row")
	}
	s.Clock.Advance(time.Minute)
	q := dec("9.8") // the counter finds 9.8 kg
	_, err = s.inv.EnterCountLines(s.cnt, s.propID, c.ID, []inventory.CountLineInput{{ItemID: s.item["BERAS"], Qty: &q}})
	must(t, err)
	s.Clock.Advance(time.Minute)
	// a sale after it
	s.sell(t, s.nasgor, "1")
	s.work(t)
	if s.queueRows(t, `status = 'HELD'`) != 2 {
		t.Fatal("two held rows")
	}
	_, err = s.inv.SubmitCount(s.cnt, s.propID, c.ID)
	must(t, err)
	preview, err := s.inv.GetCount(s.apr, s.propID, c.ID)
	must(t, err)
	for _, l := range preview.Lines {
		if l.ItemID == s.item["BERAS"] && (l.VarianceQty == nil || !l.VarianceQty.IsZero() || l.ExpectedQty == nil || !l.ExpectedQty.Equal(dec("9.8"))) {
			t.Fatalf("the sale before the count lowers what it expects: %+v", l)
		}
	}
	if preview.HeldSales != 2 {
		t.Fatalf("the count says it holds 2 sales, not %d", preview.HeldSales)
	}
	_, err = s.inv.ApproveCount(s.apr, s.propID, c.ID)
	must(t, err)
	// the first sale before the adjustment (no variance), the second after it: 9.8 - 0.2
	s.wantBalance(t, "BERAS", "9.6", "163200")
	if s.queueRows(t, `status = 'APPLIED'`) != 2 {
		t.Fatal("both rows are applied, once")
	}
	if s.Count(t, `SELECT count(*) FROM inv_movements WHERE kind = 'POS_SALE'`) != 2 || s.Count(t, `SELECT count(*) FROM inv_movements WHERE kind IN ('COUNT_GAIN', 'COUNT_LOSS') AND item_id = $1`, s.item["BERAS"]) != 0 {
		t.Fatal("two sales, no variance of the rice")
	}
}

func TestASaleHeldByACountThatIsCancelledIsAppliedWhole(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	c, err := s.inv.StartCount(s.cnt, s.propID, inventory.CountInput{ClientUUID: uid(9101), WarehouseID: s.wh, Scope: inventory.ScopeAll})
	must(t, err)
	s.sell(t, s.nasgor, "2", "Telur ceplok")
	s.work(t)
	_, err = s.inv.CancelCount(s.cnt, s.propID, c.ID)
	must(t, err)
	s.wantBalance(t, "BERAS", "9.6", "163200")
	s.wantBalance(t, "TELUR", "98", "196000")
	if s.queueRows(t, `status = 'APPLIED'`) != 1 {
		t.Fatal("the held row is applied when the freeze ends")
	}
}

func TestAVoidWhileTheWarehouseIsFrozenWaitsForTheCountToo(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	bill := s.sell(t, s.nasgor, "1")
	s.work(t)
	c, err := s.inv.StartCount(s.cnt, s.propID, inventory.CountInput{ClientUUID: uid(9102), WarehouseID: s.wh, Scope: inventory.ScopeAll})
	must(t, err)
	_, err = s.svc.VoidLine(s.cashier, s.propID, bill.ID, bill.Lines[0].ID, pos.VoidLineInput{Reason: "x", Approval: s.superIn})
	must(t, err)
	s.work(t)
	if s.queueRows(t, `kind = 'VOID_RETURN' AND status = 'HELD'`) != 1 {
		t.Fatal("the void return is held")
	}
	_, err = s.inv.CancelCount(s.cnt, s.propID, c.ID)
	must(t, err)
	s.wantBalance(t, "BERAS", "10", "170000")
}

func TestTheRecipeIsReadWithItsCostAndMarginAndSavedWholeByWhoManagesThePOS(t *testing.T) {
	s := newStockFx(t, inventory.MethodAverage)
	r, err := s.inv.GetRecipe(s.admin, s.propID, s.nasgor.ID, nil)
	must(t, err)
	// 0.2 kg of rice at 17,000 = 3,400 per portion; the net price is 85,000 before service and tax
	if !r.BaseCost.Equal(dec("3400")) || r.NetPrice == nil || !r.NetPrice.Equal(dec("85000")) || r.FoodCostPct == nil || !r.FoodCostPct.Equal(dec("4")) || !r.Editable || !r.InventoryActive {
		t.Fatalf("recipe: base %s price %v pct %v", r.BaseCost, r.NetPrice, r.FoodCostPct)
	}
	var jumbo, egg *inventory.RecipeOption
	for i := range r.Options {
		switch r.Options[i].Name {
		case "Jumbo":
			jumbo = &r.Options[i]
		case "Telur ceplok":
			egg = &r.Options[i]
		}
	}
	// the variant costs 0.3 kg (5,100) and replaces 3,400: 1,700 more; the egg: 2,000 + 0.01 l of oil at 25,000 = 250
	if jumbo == nil || !jumbo.ExtraCost.Equal(dec("1700")) || jumbo.Lines[0].ReplacesLabel == nil || egg == nil || !egg.ExtraCost.Equal(dec("2250")) {
		t.Fatalf("options: %+v", r.Options)
	}
	// a person who only reads cannot save it, and an item twice is refused
	if _, err := s.inv.SaveRecipe(s.cnt, s.propID, s.nasgor.ID, inventory.RecipeInput{}); err == nil {
		t.Fatal("saving a recipe needs pos.manage")
	}
	_, err = s.inv.SaveRecipe(s.admin, s.propID, s.nasgor.ID, inventory.RecipeInput{Lines: []inventory.RecipeLineInput{
		{ItemID: s.item["BERAS"], UnitID: s.unit["g"], Quantity: dec("200")}, {ItemID: s.item["BERAS"], UnitID: s.unit["kg"], Quantity: dec("1")}}})
	wantCode(t, err, "VALIDATION_FAILED")
	// a unit that is not the item's
	_, err = s.inv.SaveRecipe(s.admin, s.propID, s.nasgor.ID, inventory.RecipeInput{Lines: []inventory.RecipeLineInput{{ItemID: s.item["TELUR"], UnitID: s.unit["kg"], Quantity: dec("1")}}})
	wantCode(t, err, "VALIDATION_FAILED")
	// a change applies to the sales after it: the row that was queued keeps its snapshot
	s.sell(t, s.nasgor, "1")
	_, err = s.inv.SaveRecipe(s.admin, s.propID, s.nasgor.ID, inventory.RecipeInput{Lines: []inventory.RecipeLineInput{{ItemID: s.item["BERAS"], UnitID: s.unit["g"], Quantity: dec("500")}}})
	must(t, err)
	s.work(t)
	s.wantBalance(t, "BERAS", "9.8", "166600")
}

func TestAVoidAndTheWorkerAtTheSameTimeLeaveTheStockRightWhicheverComesFirst(t *testing.T) {
	for i := 0; i < 4; i++ {
		s := newStockFx(t, inventory.MethodAverage)
		bill := s.sell(t, s.nasgor, "1")
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.inv.RunQueue(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.svc.VoidLine(s.cashier, s.propID, bill.ID, bill.Lines[0].ID, pos.VoidLineInput{Reason: "x", Approval: s.superIn}); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wg.Wait()
		s.work(t)
		// the void came first (the row was cancelled and nothing moved) or the sale was applied and the return undid it: the rice is all there either way
		s.wantBalance(t, "BERAS", "10", "170000")
		if s.queueRows(t, `status IN ('PENDING', 'HELD', 'FAILED')`) != 0 {
			t.Fatal("nothing is left waiting")
		}
	}
}

func TestACountEntryAndASaleAtTheSameTimeDeductTheSaleOnceOnOneSideOfTheCount(t *testing.T) {
	for i := 0; i < 4; i++ {
		s := newStockFx(t, inventory.MethodAverage)
		c, err := s.inv.StartCount(s.cnt, s.propID, inventory.CountInput{ClientUUID: uid(9200 + i), WarehouseID: s.wh, Scope: inventory.ScopeAll})
		must(t, err)
		q := dec("9.8")
		var wg sync.WaitGroup
		wg.Add(2)
		start := make(chan struct{})
		go func() {
			defer wg.Done()
			<-start
			s.sell(t, s.nasgor, "1")
			s.work(t)
		}()
		go func() {
			defer wg.Done()
			<-start
			if _, err := s.inv.EnterCountLines(s.cnt, s.propID, c.ID, []inventory.CountLineInput{{ItemID: s.item["BERAS"], Qty: &q}}); err != nil {
				t.Error(err)
			}
		}()
		close(start)
		wg.Wait()
		_, err = s.inv.SubmitCount(s.cnt, s.propID, c.ID)
		must(t, err)
		_, err = s.inv.ApproveCount(s.apr, s.propID, c.ID)
		must(t, err)
		if s.Count(t, `SELECT count(*) FROM inv_movements WHERE kind = 'POS_SALE'`) != 1 || s.queueRows(t, `status = 'APPLIED'`) != 1 || s.queueRows(t, `status = 'HELD'`) != 0 {
			t.Fatal("the sale is deducted exactly once")
		}
		// the sale was before the entry (9.8 is what was on the shelf after it) or after it (the shelf had 9.8 and the sale takes 0.2 more): the book is one of the two
		if q, _ := s.balance(t, "BERAS"); q != "9.800000" && q != "9.600000" {
			t.Fatalf("the rice is %s kg", q)
		}
	}
}
