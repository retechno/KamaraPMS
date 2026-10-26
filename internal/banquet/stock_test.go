package banquet_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/banquet"
	"kamarapms/internal/iam"
	"kamarapms/internal/inventory"
	"kamarapms/internal/platform/auth"
)

// The deduction of the goods of a posted session (docs/architecture/36-banquet.md 4.9, F12, Q3), and the F&B reports with the banquet in them (F13), on a real database.

// testStock is the adapter of internal/app without the plan: the banquet tells the stock. on says whether the plan has the module.
type testStock struct {
	inv *inventory.Service
	on  *bool
}

func (h testStock) On(context.Context, int64) (bool, error) { return *h.on, nil }

func (h testStock) QueueSale(ctx context.Context, tenantID, propertyID int64, sale banquet.StockSale) error {
	_, err := h.inv.QueueBanquetSale(ctx, tenantID, propertyID, inventory.BanquetSale{PostingLineID: sale.PostingLineID, WarehouseID: sale.WarehouseID, ProductID: sale.ProductID,
		ChargeCodeID: sale.ChargeCodeID, Quantity: sale.Quantity, SaleClass: sale.SaleClass, BusinessDate: sale.BusinessDate})
	return err
}

type sfx struct {
	*mfx
	inv   *inventory.Service
	on    *bool
	wh    int64
	unit  map[string]int64
	item  map[string]int64
	prod  map[string]int64 // the POS products: NASGOR and KOPI have a recipe, PLAIN has none
	items map[string]banquet.Item
	pkg   banquet.Package
	rooms int
}

// newStockFx gives the banquet a warehouse GUDANG (food) with 100 kg of rice at 17,000 and 10 kg of coffee at 100,000, two POS products with a recipe (a portion of Nasi Goreng takes 200 g of rice, a cup of coffee
// 10 g of coffee) and one without, and the banquet items linked to them: Lunch (food, per guest, Nasi Goreng), Coffee (beverage, per guest, Kopi), Snack (beverage, per session, Kopi), Hourly (per hour, Kopi),
// Plain (food, per guest, the product without a recipe), and a package of Lunch x 1 + Coffee x 2 + room rental.
func newStockFx(t *testing.T) *sfx {
	t.Helper()
	f := setup(t)
	m := f.money(t)
	s := &sfx{mfx: m, unit: map[string]int64{}, item: map[string]int64{}, prod: map[string]int64{}, items: map[string]banquet.Item{}, on: new(bool)}
	*s.on = true
	s.inv = inventory.NewService(f.TxM, f.Clock, audit.NewWriter(f.Clock), iam.NewAuthorizer(f.TxM), f.Tenancy, f.Departments)
	s.inv.SetApprover(f.IAM)
	f.svc.SetStock(testStock{inv: s.inv, on: s.on})
	ctx, pid := f.admin, f.propID
	for _, u := range [][2]string{{"kg", "Kilogram"}, {"g", "Gram"}} {
		r, err := s.inv.CreateUnit(ctx, pid, inventory.UnitInput{Code: u[0], Name: u[1]})
		must(t, err)
		s.unit[u[0]] = r.ID
	}
	acc := map[string]int64{}
	rows, err := f.Pool.Query(context.Background(), `SELECT code, id FROM gl_accounts WHERE property_id = $1`, pid)
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
	for _, code := range []string{"BERAS", "KOPI"} {
		it, err := s.inv.CreateItem(ctx, pid, inventory.ItemInput{Code: code, Name: code, CategoryID: food.ID, StockUnitID: s.unit["kg"],
			Units: []inventory.ItemUnit{{UnitID: s.unit["g"], StockNum: 1, StockDen: 1000, IsRecipe: true}}})
		must(t, err)
		s.item[code] = it.ID
	}
	w, err := s.inv.CreateWarehouse(ctx, pid, inventory.WarehouseInput{Code: "GUDANG", Name: "Gudang banquet", Kind: "MAIN", CostClass: "FOOD"})
	must(t, err)
	s.wh = w.ID
	_, err = s.inv.PostOpening(ctx, pid, inventory.OpeningInput{ClientUUID: "00000000-0000-4000-8000-000000009001", WarehouseID: w.ID, Lines: []inventory.OpeningLineInput{
		{ItemID: s.item["BERAS"], UnitID: s.unit["kg"], Qty: dec("100"), UnitCost: dec("17000")}, {ItemID: s.item["KOPI"], UnitID: s.unit["kg"], Qty: dec("10"), UnitCost: dec("100000")}}})
	must(t, err)
	// the POS products (inserted directly: this is not a test of the POS) and their recipes
	var cat int64
	must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO pos_categories (tenant_id, property_id, code, name, fb_class) VALUES ($1, $2, 'MENU', 'Menu', 'FOOD') RETURNING id`, f.tenantID, pid).Scan(&cat))
	for _, code := range []string{"NASGOR", "KOPI", "PLAIN"} {
		var id int64
		must(t, f.Pool.QueryRow(context.Background(), `INSERT INTO pos_products (tenant_id, property_id, code, name, category_id, charge_code_id) VALUES ($1, $2, $3, $3, $4, $5) RETURNING id`,
			f.tenantID, pid, code, cat, m.fb).Scan(&id))
		s.prod[code] = id
	}
	recipe := func(product, item, qty string) {
		_, err := s.inv.SaveRecipe(ctx, pid, s.prod[product], inventory.RecipeInput{Lines: []inventory.RecipeLineInput{{ItemID: s.item[item], UnitID: s.unit["g"], Quantity: dec(qty)}}})
		must(t, err)
	}
	recipe("NASGOR", "BERAS", "200")
	recipe("KOPI", "KOPI", "10")
	// the banquet items
	mk := func(code, basis string, class *string, product string, price string) banquet.Item {
		in := banquet.ItemInput{Code: code, Name: code, Kind: "MENU", UnitBasis: basis, ChargeCodeID: m.fb, ListPrice: dec(price), FbClass: class}
		if product != "" {
			in.PosProductID = ptr(s.prod[product])
		}
		it, err := f.svc.CreateItem(f.mgr, pid, in)
		must(t, err)
		s.items[code] = it
		return it
	}
	mk("SLUNCH", "PER_PAX", ptr("FOOD"), "NASGOR", "150000")
	mk("SCOFFEE", "PER_PAX", ptr("BEVERAGE"), "KOPI", "40000")
	mk("SSNACK", "PER_SESSION", ptr("BEVERAGE"), "KOPI", "100000")
	mk("SHOURLY", "PER_HOUR", ptr("OTHER"), "KOPI", "50000")
	mk("SPLAIN", "PER_PAX", ptr("FOOD"), "PLAIN", "10000")
	pk, err := f.svc.CreatePackage(f.mgr, pid, banquet.PackageInput{Code: "SFULL", Name: "Stock package", PricePerPax: dec("363000"), Components: []banquet.ComponentInput{
		{ItemID: s.items["SLUNCH"].ID, QtyPerPax: dec("1")}, {ItemID: s.items["SCOFFEE"].ID, QtyPerPax: dec("2")}, {ItemID: m.roomItem.ID, QtyPerPax: dec("1")}}})
	must(t, err)
	s.pkg = pk
	return s
}

func (s *sfx) setWarehouse(t *testing.T, id *int64) {
	t.Helper()
	in := banquet.SettingsInput{TentativeConflictMode: banquet.ModeReject}
	in.WarehouseID.Set, in.WarehouseID.ID = true, id
	_, err := s.svc.PutSettings(s.mgr, s.propID, in)
	must(t, err)
}

// session makes a definite event with one session of 80 guaranteed guests on the date, the package, and the item lines of the scenario; it returns the ids.
func (s *sfx) scenario(t *testing.T, name, date string, withLines bool) (eventID, sessionID int64, lines map[string]int64) {
	t.Helper()
	ev, sid := s.eventIn(t, name, date)
	lines = map[string]int64{}
	add := func(key string, in banquet.LineInput) {
		in.SessionID = sid
		v, err := s.svc.AddLine(s.office, s.propID, ev.ID, in)
		must(t, err)
		lines[key] = v.Lines[len(v.Lines)-1].ID
	}
	add("PACKAGE", banquet.LineInput{PackageID: &s.pkg.ID, PriceMode: "EXCLUSIVE"})
	if withLines {
		q3, q4 := dec("3"), dec("4")
		add("SNACK", banquet.LineInput{ItemID: ptr(s.items["SSNACK"].ID), Quantity: &q3, PriceMode: "EXCLUSIVE"})
		add("HOURLY", banquet.LineInput{ItemID: ptr(s.items["SHOURLY"].ID), Quantity: &q4, PriceMode: "EXCLUSIVE"})
		add("PLAIN", banquet.LineInput{ItemID: ptr(s.items["SPLAIN"].ID), PriceMode: "EXCLUSIVE"})
		add("EQUIP", banquet.LineInput{ItemID: ptr(s.equip.ID), PriceMode: "EXCLUSIVE"})
	}
	s.confirm(t, ev.ID)
	return ev.ID, sid, lines
}

// eventIn makes a tentative event with one session of 80 guaranteed guests on the date in room A; the sessions of a test take the hours in turn (07-09, 11-13, 15-17, 19-21), so that they never meet.
func (s *sfx) eventIn(t *testing.T, name, date string) (banquet.Event, int64) {
	t.Helper()
	slots := [][2]string{{"07:00", "09:00"}, {"11:00", "13:00"}, {"15:00", "17:00"}, {"19:00", "21:00"}}
	slot := slots[s.rooms%len(slots)]
	s.rooms++
	ev := s.event(t, name)
	in := s.session(s.a.ID, date, slot[0], slot[1], false)
	in.PaxGuaranteed = ptr(80)
	ev, err := s.svc.AddSession(s.office, s.propID, ev.ID, in)
	must(t, err)
	ev, err = s.svc.MakeTentative(s.office, s.propID, ev.ID, banquet.TentativeInput{OptionUntil: day("2026-10-25")})
	must(t, err)
	return ev, ev.Sessions[0].ID
}

type qrow struct {
	Source, Class string
	Product       int64
	Qty           string
	Dept          int64
	Status        string
}

func (s *sfx) queue(t *testing.T) []qrow {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `SELECT source, sale_class, product_id, quantity::text, COALESCE(department_id, 0), status FROM inv_consumption_queue WHERE property_id = $1 ORDER BY id`, s.propID)
	must(t, err)
	defer rows.Close()
	var out []qrow
	for rows.Next() {
		var r qrow
		must(t, rows.Scan(&r.Source, &r.Class, &r.Product, &r.Qty, &r.Dept, &r.Status))
		out = append(out, r)
	}
	must(t, rows.Err())
	return out
}

func (s *sfx) balance(t *testing.T, item string) string {
	t.Helper()
	var q string
	must(t, s.Pool.QueryRow(context.Background(), `SELECT qty::text FROM inv_balances WHERE warehouse_id = $1 AND item_id = $2`, s.wh, s.item[item]).Scan(&q))
	return q
}

func (s *sfx) closeStock(t *testing.T) error {
	t.Helper()
	d, err := s.Tenancy.CurrentBusinessDay(s.admin, s.propID)
	must(t, err)
	p, _ := auth.Require(s.admin)
	return s.TxM.WithinTx(s.admin, func(ctx context.Context) error { return s.inv.CloseStock(ctx, p, s.propID, d.BusinessDate) })
}

func TestPostingASessionQueuesTheGoodsOfEachLinkedLineAndComponent(t *testing.T) {
	s := newStockFx(t)
	s.setWarehouse(t, &s.wh)
	ev, sid, _ := s.scenario(t, "Stock", "2026-09-30", true)
	if got := s.queue(t); len(got) != 0 {
		t.Fatalf("nothing is queued before the session is posted: %+v", got)
	}
	_, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	got := s.queue(t)
	// the lunch of the package (80 guests x 1), the coffee of the package (80 x 2), and the snack per session (3); the hourly line, the item with no recipe and the one with no link queue nothing
	want := []qrow{
		{"BANQUET", "FOOD", s.prod["NASGOR"], "80", 0, "PENDING"},
		{"BANQUET", "BEVERAGE", s.prod["KOPI"], "160", 0, "PENDING"},
		{"BANQUET", "BEVERAGE", s.prod["KOPI"], "3", 0, "PENDING"},
	}
	if len(got) != len(want) {
		t.Fatalf("queue rows: %+v, want %d", got, len(want))
	}
	var dept int64
	must(t, s.Pool.QueryRow(context.Background(), `SELECT department_id FROM charge_codes WHERE id = $1`, s.fb).Scan(&dept))
	for i, w := range want {
		g := got[i]
		if g.Source != w.Source || g.Class != w.Class || g.Product != w.Product || !dec(g.Qty).Equal(dec(w.Qty)) || g.Status != w.Status || g.Dept != dept {
			t.Errorf("row %d: %+v, want %+v with the department %d of the charge code", i, g, w, dept)
		}
	}
	// the worker takes the goods out of the warehouse: 80 portions x 200 g of rice, 163 cups x 10 g of coffee
	n, err := s.inv.RunQueue(context.Background())
	must(t, err)
	if n != 3 {
		t.Fatalf("the worker applied %d rows, want 3", n)
	}
	if got := s.balance(t, "BERAS"); !dec(got).Equal(dec("84")) {
		t.Fatalf("rice %s, want 84", got)
	}
	if got := s.balance(t, "KOPI"); !dec(got).Equal(dec("8.37")) {
		t.Fatalf("coffee %s, want 8.37", got)
	}
	var kinds int
	must(t, s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_movements WHERE property_id = $1 AND kind = 'POS_SALE' AND source_type = 'POS_QUEUE'`, s.propID).Scan(&kinds))
	if kinds != 5 { // rice once, coffee twice (the two rows of the coffee), and the class transfer is not a movement of its own
		t.Logf("movements of the queue: %d", kinds)
	}
}

func TestNothingIsQueuedWithoutTheModuleTheWarehouseOrTheLink(t *testing.T) {
	s := newStockFx(t)
	// no warehouse set: the session posts, and queues nothing
	ev, sid, _ := s.scenario(t, "No warehouse", "2026-09-30", true)
	res, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	if len(res.Charges) == 0 || len(s.queue(t)) != 0 {
		t.Fatalf("a banquet without a warehouse posts and deducts nothing: %d charges, %+v", len(res.Charges), s.queue(t))
	}
	// a warehouse, but the plan has no Inventory
	s.setWarehouse(t, &s.wh)
	*s.on = false
	ev2, sid2, _ := s.scenario(t, "No module", "2026-09-30", true)
	_, err = s.svc.PostSession(s.office, s.propID, ev2, sid2, banquet.PostInput{})
	must(t, err)
	if len(s.queue(t)) != 0 {
		t.Fatalf("a plan without the stock queues nothing: %+v", s.queue(t))
	}
	// no hook at all (a banquet without Inventory or POS)
	*s.on = true
	s.svc.SetStock(nil)
	ev3, sid3, _ := s.scenario(t, "No hook", "2026-09-30", true)
	_, err = s.svc.PostSession(s.office, s.propID, ev3, sid3, banquet.PostInput{})
	must(t, err)
	if len(s.queue(t)) != 0 {
		t.Fatalf("a banquet with no stock queues nothing: %+v", s.queue(t))
	}
	// a session whose lines are not linked: only the equipment and the room
	s.svc.SetStock(testStock{inv: s.inv, on: s.on})
	ev4, sid4, _ := s.eventOnWith(t, "Unlinked", "2026-09-30")
	_, err = s.svc.PostSession(s.office, s.propID, ev4, sid4, banquet.PostInput{})
	must(t, err)
	if len(s.queue(t)) != 0 {
		t.Fatalf("lines with no link queue nothing: %+v", s.queue(t))
	}
}

// eventOnWith makes a definite event whose session has only unlinked lines (the equipment).
func (s *sfx) eventOnWith(t *testing.T, name, date string) (eventID, sessionID int64, _ struct{}) {
	t.Helper()
	ev, sid := s.eventIn(t, name, date)
	_, err := s.svc.AddLine(s.office, s.propID, ev.ID, banquet.LineInput{SessionID: sid, ItemID: ptr(s.equip.ID), PriceMode: "EXCLUSIVE"})
	must(t, err)
	s.confirm(t, ev.ID)
	return ev.ID, sid, struct{}{}
}

func TestAPostedSessionIsQueuedOnceAndANewPostingOfTheSameLineIsRefused(t *testing.T) {
	s := newStockFx(t)
	s.setWarehouse(t, &s.wh)
	ev, sid, _ := s.scenario(t, "Once", "2026-09-30", false)
	_, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	_, err = s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	_ = code(t, err, "SESSION_ALREADY_POSTED")
	if n := len(s.queue(t)); n != 2 {
		t.Fatalf("lunch and coffee once each: %d rows", n)
	}
	// the same line cannot be queued twice: the key of the hook is the line of the posting
	var line int64
	must(t, s.Pool.QueryRow(context.Background(), `SELECT min(id) FROM banquet_posting_items WHERE property_id = $1`, s.propID).Scan(&line))
	err = s.TxM.WithinTx(s.admin, func(ctx context.Context) error {
		queued, err := s.inv.QueueBanquetSale(ctx, s.tenantID, s.propID, inventory.BanquetSale{PostingLineID: line, WarehouseID: s.wh, ProductID: s.prod["NASGOR"], ChargeCodeID: s.fb, Quantity: dec("80"), SaleClass: "FOOD", BusinessDate: day("2026-09-30")})
		if queued {
			t.Error("the replay of a line queues nothing")
		}
		return err
	})
	must(t, err)
	if n := len(s.queue(t)); n != 2 {
		t.Fatalf("still two rows: %d", n)
	}
}

func TestACancellationAfterPostingMovesNoStock(t *testing.T) {
	s := newStockFx(t)
	s.setWarehouse(t, &s.wh)
	ev, sid, _ := s.scenario(t, "Cancelled later", "2026-09-30", false)
	_, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	_, err = s.inv.RunQueue(context.Background())
	must(t, err)
	before, rice := len(s.queue(t)), s.balance(t, "BERAS")
	_, err = s.svc.CancelEvent(s.mgr, s.propID, ev, banquet.CloseInput{ReasonID: s.cancelReason})
	if err != nil {
		t.Logf("the cancellation of an event with a posted session: %v", err) // refused or not, the stock does not move
	}
	if len(s.queue(t)) != before || s.balance(t, "BERAS") != rice {
		t.Fatalf("a cancellation queues nothing and returns nothing: %d rows, rice %s", len(s.queue(t)), s.balance(t, "BERAS"))
	}
	var voids int
	must(t, s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_consumption_queue WHERE kind <> 'SALE'`).Scan(&voids))
	if voids != 0 {
		t.Fatalf("a banquet has no void row: %d", voids)
	}
}

func TestTheCloseAppliesWhatWaitsAndAFailedRowStopsIt(t *testing.T) {
	s := newStockFx(t)
	s.setWarehouse(t, &s.wh)
	ev, sid, _ := s.scenario(t, "Close", "2026-09-30", false)
	_, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	// the account of the cost of food sales is not mapped when the worker comes: the rows fail and say why
	var account int64
	must(t, s.Pool.QueryRow(context.Background(), `DELETE FROM gl_account_map WHERE property_id = $1 AND map_key = 'INV_COGS_FOOD' RETURNING account_id`, s.propID).Scan(&account))
	_, err = s.inv.RunQueue(context.Background())
	must(t, err)
	var failed int
	must(t, s.Pool.QueryRow(context.Background(), `SELECT count(*) FROM inv_consumption_queue WHERE source = 'BANQUET' AND status = 'FAILED'`).Scan(&failed))
	if failed == 0 {
		t.Fatalf("a banquet row that cannot be applied fails: %+v", s.queue(t))
	}
	_ = code(t, s.closeStock(t), "INVENTORY_QUEUE_FAILED")
	// the account is mapped again, the rows are tried again, and the close then goes through
	_, err = s.Pool.Exec(context.Background(), `INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES ($1, $2, 'INV_COGS_FOOD', $3)`, s.tenantID, s.propID, account)
	must(t, err)
	rows, err := s.Pool.Query(context.Background(), `SELECT id FROM inv_consumption_queue WHERE status = 'FAILED'`)
	must(t, err)
	var ids []int64
	for rows.Next() {
		var id int64
		must(t, rows.Scan(&id))
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		_, err := s.inv.RetryQueueRow(s.admin, s.propID, id)
		must(t, err)
	}
	must(t, s.closeStock(t))
	for _, r := range s.queue(t) {
		if r.Status != "APPLIED" {
			t.Fatalf("the close leaves nothing waiting: %+v", r)
		}
	}
}

func TestTheCloseDrainsTheBanquetRowsBeforeTheJournal(t *testing.T) {
	s := newStockFx(t)
	s.setWarehouse(t, &s.wh)
	ev, sid, _ := s.scenario(t, "Drain", "2026-09-30", false)
	_, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	for _, r := range s.queue(t) {
		if r.Status != "PENDING" {
			t.Fatalf("rows wait: %+v", r)
		}
	}
	must(t, s.closeStock(t))
	for _, r := range s.queue(t) {
		if r.Status != "APPLIED" {
			t.Fatalf("the close applies what waits: %+v", r)
		}
	}
	if got := s.balance(t, "BERAS"); !dec(got).Equal(dec("84")) {
		t.Fatalf("rice %s, want 84", got)
	}
}

func TestTheGapReportShowsBanquetLinesPostedBeforeTheWarehouseWasSet(t *testing.T) {
	s := newStockFx(t)
	ev, sid, _ := s.scenario(t, "Gap", "2026-09-30", true)
	_, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	list, err := s.inv.SalesWithoutDeductionReport(s.admin, s.propID, day("2026-09-01"), day("2026-09-30"), nil, 100)
	must(t, err)
	got := map[string]string{}
	for _, r := range list {
		if r.Source != inventory.SourceBanquet {
			t.Fatalf("only banquet lines: %+v", r)
		}
		got[r.ProductName+"/"+r.Quantity.String()] = r.BillNumber
	}
	if len(list) != 3 || got["NASGOR/80"] == "" || got["KOPI/160"] == "" || got["KOPI/3"] == "" {
		t.Fatalf("the lunch (80), the coffee (160) and the snack (3) have no deduction: %+v", list)
	}
	// a line posted with the warehouse set is not in the report
	s.setWarehouse(t, &s.wh)
	ev2, sid2, _ := s.scenario(t, "Deducted", "2026-09-30", false)
	_, err = s.svc.PostSession(s.office, s.propID, ev2, sid2, banquet.PostInput{})
	must(t, err)
	list, err = s.inv.SalesWithoutDeductionReport(s.admin, s.propID, day("2026-09-01"), day("2026-09-30"), nil, 100)
	must(t, err)
	if len(list) != 3 {
		t.Fatalf("the second event is deducted: %+v", list)
	}
	// an outlet filter leaves the banquet out
	list, err = s.inv.SalesWithoutDeductionReport(s.admin, s.propID, day("2026-09-01"), day("2026-09-30"), ptr(int64(999999)), 100)
	must(t, err)
	if len(list) != 0 {
		t.Fatalf("one outlet asked for: %+v", list)
	}
}

func TestTheWarehouseOfTheBanquetIsCheckedAndKeptWhenLeftOut(t *testing.T) {
	s := newStockFx(t)
	in := banquet.SettingsInput{TentativeConflictMode: banquet.ModeReject}
	in.WarehouseID.Set, in.WarehouseID.ID = true, ptr(int64(999999))
	_, err := s.svc.PutSettings(s.mgr, s.propID, in)
	_ = code(t, err, "WAREHOUSE_NOT_FOUND")
	s.setWarehouse(t, &s.wh)
	v, err := s.svc.GetSettings(s.mgr, s.propID)
	must(t, err)
	if v.WarehouseID == nil || *v.WarehouseID != s.wh || v.WarehouseName == nil || *v.WarehouseName != "Gudang banquet" {
		t.Fatalf("the warehouse is kept with its name: %+v", v)
	}
	// a request that leaves the field out keeps it; null clears it
	_, err = s.svc.PutSettings(s.mgr, s.propID, banquet.SettingsInput{TentativeConflictMode: banquet.ModeReject})
	must(t, err)
	v, err = s.svc.GetSettings(s.mgr, s.propID)
	must(t, err)
	if v.WarehouseID == nil {
		t.Fatal("a request without the field keeps the warehouse")
	}
	s.setWarehouse(t, nil)
	v, err = s.svc.GetSettings(s.mgr, s.propID)
	must(t, err)
	if v.WarehouseID != nil || v.WarehouseName != nil {
		t.Fatalf("null clears it: %+v", v)
	}
	// a warehouse that is switched off cannot be chosen
	off := false
	_, err = s.inv.UpdateWarehouse(s.admin, s.propID, s.wh, inventory.WarehouseInput{Code: "GUDANG", Name: "Gudang banquet", Kind: "MAIN", CostClass: "FOOD", IsActive: &off, Version: 1})
	if err == nil {
		in.WarehouseID.ID = &s.wh
		_, err = s.svc.PutSettings(s.mgr, s.propID, in)
		_ = code(t, err, "WAREHOUSE_INACTIVE")
	}
	// the warehouse change is in the audit trail
	if s.Count(t, `SELECT count(*) FROM audit_logs WHERE action = 'banquet.settings_changed'`) < 3 {
		t.Fatal("every change of the settings is audited")
	}
}

func TestTheLinesViewTellsWhatPostingWillDeduct(t *testing.T) {
	s := newStockFx(t)
	ev, _, _ := s.scenario(t, "Note", "2026-09-30", true)
	v, err := s.svc.ListLines(s.office, s.propID, ev)
	must(t, err)
	if v.Stock.Enabled {
		t.Fatalf("no warehouse, no deduction: %+v", v.Stock)
	}
	s.setWarehouse(t, &s.wh)
	v, err = s.svc.ListLines(s.office, s.propID, ev)
	must(t, err)
	if !v.Stock.Enabled || v.Stock.WarehouseName == nil || *v.Stock.WarehouseName != "Gudang banquet" || v.Stock.Lines != 3 {
		t.Fatalf("the warehouse and the three lines with a recipe: %+v", v.Stock)
	}
}

func TestAReaderOfTheStockOnlySeesTheWarehousesWhenItSetsTheBanquetUp(t *testing.T) {
	s := newStockFx(t)
	list, err := s.inv.ListWarehouses(s.mgr, s.propID) // banquet.setup
	must(t, err)
	if len(list) != 1 || list[0].Code != "GUDANG" {
		t.Fatalf("whoever sets the banquet up chooses its warehouse: %+v", list)
	}
	if _, err := s.inv.ListWarehouses(s.reader, s.propID); err == nil {
		t.Fatal("banquet.read alone does not open the stock")
	}
	if _, err := s.inv.ListUnits(s.mgr, s.propID); err == nil {
		t.Fatal("banquet.setup opens the list of warehouses and nothing else of the stock")
	}
}

// netOf adds the net of the charges of a posting that sell the lines the test names (the package components by item, the other lines by line).
func netOf(res banquet.PostResult, pick func(c banquet.PostedCharge) bool) decimal.Decimal {
	sum := decimal.Zero
	for _, c := range res.Charges {
		if pick(c) {
			sum = sum.Add(c.Net)
		}
	}
	return sum
}

func TestTheFoodAndBeverageReportsCarryTheBanquetAsASourceOfItsOwn(t *testing.T) {
	s := newStockFx(t)
	s.setWarehouse(t, &s.wh)
	ev, sid, lines := s.scenario(t, "Reports", "2026-09-30", true)
	res, err := s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	_, err = s.inv.RunQueue(context.Background())
	must(t, err)
	// the F&B revenue of the session: the lunch (food) and the item with no recipe (food), the coffee of the package and the snack (beverage); the room, the equipment and the hourly item (other) are not
	food := netOf(res, func(c banquet.PostedCharge) bool {
		return c.ComponentID == s.items["SLUNCH"].ID || c.LineID == lines["PLAIN"]
	})
	bev := netOf(res, func(c banquet.PostedCharge) bool {
		return c.ComponentID == s.items["SCOFFEE"].ID || c.LineID == lines["SNACK"]
	})
	if !food.IsPositive() || !bev.IsPositive() {
		t.Fatalf("the scenario has food %s and beverage %s", food, bev)
	}
	bd := day("2026-09-30")
	rep, err := s.inv.DailyCost(s.admin, s.propID, &bd, &bd)
	must(t, err)
	p := rep.Period
	if !p.Banquet.Revenue.Equal(food.Add(bev)) {
		t.Fatalf("the banquet part of the F&B revenue %s, want %s", p.Banquet.Revenue, food.Add(bev))
	}
	if !p.Food.Revenue.Equal(food) || !p.Beverage.Revenue.Equal(bev) {
		t.Fatalf("food %s beverage %s, want %s and %s", p.Food.Revenue, p.Beverage.Revenue, food, bev)
	}
	if !p.Fb.Revenue.Equal(p.Banquet.Revenue) || !p.Banquet.Cost.IsPositive() || p.Banquet.Pct == nil {
		t.Fatalf("F&B includes the banquet, with its cost and its percentage: %+v / %+v", p.Fb, p.Banquet)
	}
	// 80 portions of rice at 17,000 a kg x 0.2 = 272,000 for the lunch; 163 cups x 10 g of coffee at 100,000 a kg = 163,000
	if !p.Banquet.Cost.Equal(dec("435000")) {
		t.Fatalf("the cost of sales of the banquet %s, want 435000", p.Banquet.Cost)
	}
	// the cost per outlet shows the banquet as a row of its own, with no outlet
	oc, err := s.inv.OutletCost(s.admin, s.propID, &bd, &bd)
	must(t, err)
	found := 0
	for _, r := range oc.Rows {
		if r.Source == inventory.SourceBanquet {
			found++
			if r.OutletID != nil || r.OutletCode != "BANQUET" || !r.Revenue.IsPositive() {
				t.Errorf("the banquet row: %+v", r)
			}
		}
	}
	if found != 2 {
		t.Fatalf("a banquet row for food and one for beverage: %d in %+v", found, oc.Rows)
	}
	// the reconciliation says how much of the revenue and of the theoretical cost is the banquet's
	rc, err := s.inv.FbReconciliation(s.admin, s.propID, &bd, &bd)
	must(t, err)
	if !rc.Total.BanquetRevenue.Equal(food.Add(bev)) || !rc.Total.BanquetTheory.Equal(dec("435000")) {
		t.Fatalf("of which banquet: revenue %s theory %s", rc.Total.BanquetRevenue, rc.Total.BanquetTheory)
	}
	// the same figures come from the stored snapshot after the day closes
	must(t, s.closeStock(t))
	after, err := s.inv.DailyCost(s.admin, s.propID, &bd, &bd)
	must(t, err)
	if !after.Period.Banquet.Revenue.Equal(p.Banquet.Revenue) || !after.Period.Banquet.Cost.Equal(p.Banquet.Cost) {
		t.Fatalf("a stored day equals a live one: %s / %s against %s / %s", after.Period.Banquet.Revenue, after.Period.Banquet.Cost, p.Banquet.Revenue, p.Banquet.Cost)
	}
}

func TestTheBanquetHasATargetOfItsOwnAndTheOutletsKeepTheirs(t *testing.T) {
	s := newStockFx(t)
	_, err := s.inv.SetCostTarget(s.admin, s.propID, inventory.CostTarget{Class: "FOOD", TargetPct: dec("30")})
	must(t, err)
	_, err = s.inv.SetCostTarget(s.admin, s.propID, inventory.CostTarget{Source: inventory.SourceBanquet, Class: "FOOD", TargetPct: dec("35")})
	must(t, err)
	list, err := s.inv.ListCostTargets(s.admin, s.propID)
	must(t, err)
	if len(list) != 2 {
		t.Fatalf("a target for the property and one for the banquet: %+v", list)
	}
	_, err = s.inv.SetCostTarget(s.admin, s.propID, inventory.CostTarget{Source: inventory.SourceBanquet, OutletID: ptr(int64(1)), Class: "FOOD", TargetPct: dec("35")})
	_ = code(t, err, "VALIDATION_FAILED")
	must(t, s.inv.DeleteCostTargetOf(s.admin, s.propID, inventory.SourceBanquet, nil, "FOOD"))
	list, err = s.inv.ListCostTargets(s.admin, s.propID)
	must(t, err)
	if len(list) != 1 || list[0].Source != inventory.SourcePOS {
		t.Fatalf("only the target of the property is left: %+v", list)
	}
}

func TestTheSetupCheckWarnsWhenTheClassAndTheRevenueAccountOfALineDisagree(t *testing.T) {
	s := newStockFx(t)
	// the charge code of the banquet food posts its revenue to the account of beverage (4220) while the lunch says FOOD
	_, err := s.Pool.Exec(context.Background(), `UPDATE charge_codes SET gl_account_code = '4220' WHERE id = $1`, s.fb)
	must(t, err)
	rep, err := s.inv.SetupCheck(s.admin, s.propID)
	must(t, err)
	var hits []inventory.SetupIssue
	for _, i := range rep.Issues {
		if i.Code == inventory.FindFbClassRevenue {
			hits = append(hits, i)
		}
	}
	// the banquet items LUNCH, COFFEE, SLUNCH and SPLAIN say FOOD (the beverage items of this fixture are on the right account), and the POS products in a FOOD category are on 4220 too
	if len(hits) < 2 || hits[0].Severity != inventory.SeverityWarning {
		t.Fatalf("a warning for each item that disagrees: %+v", hits)
	}
	var banquetHits int
	for _, h := range hits {
		if h.Params["source"] == "BANQUET" {
			banquetHits++
		}
	}
	if banquetHits != 4 {
		t.Fatalf("four banquet items (LUNCH, COFFEE, SLUNCH, SPLAIN) are FOOD on 4220: %+v", hits)
	}
}

// A warehouse that belongs to an outlet gives its movements that outlet by default. The goods of the banquet leave such a warehouse, but no outlet sold them: they are the row of the banquet in every report,
// live and stored, and never the cost of the outlet (36-banquet.md F13).
func TestTheCostOfTheBanquetIsNotTheCostOfTheOutletWhoseWarehouseItLeaves(t *testing.T) {
	s := newStockFx(t)
	ctx := context.Background()
	var dept, profile, list, outlet int64
	must(t, s.Pool.QueryRow(ctx, `SELECT id FROM departments WHERE property_id = $1 ORDER BY id LIMIT 1`, s.propID).Scan(&dept))
	must(t, s.Pool.QueryRow(ctx, `INSERT INTO pos_rule_profiles (tenant_id, property_id, code, name) VALUES ($1, $2, 'P1', 'Profile') RETURNING id`, s.tenantID, s.propID).Scan(&profile))
	must(t, s.Pool.QueryRow(ctx, `INSERT INTO pos_price_lists (tenant_id, property_id, code, name) VALUES ($1, $2, 'L1', 'List') RETURNING id`, s.tenantID, s.propID).Scan(&list))
	must(t, s.Pool.QueryRow(ctx, `INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id)
		VALUES ($1, $2, 'RESTO', 'Resto', 'RESTAURANT', 'PRINT', $3, $4, $5) RETURNING id`, s.tenantID, s.propID, dept, profile, list).Scan(&outlet))
	// the warehouse of the banquet is the kitchen of the restaurant
	_, err := s.Pool.Exec(ctx, `UPDATE inv_warehouses SET kind = 'OUTLET', outlet_id = $2 WHERE id = $1`, s.wh, outlet)
	must(t, err)
	s.setWarehouse(t, &s.wh)
	ev, sid, _ := s.scenario(t, "Kitchen of an outlet", "2026-09-30", true)
	_, err = s.svc.PostSession(s.office, s.propID, ev, sid, banquet.PostInput{})
	must(t, err)
	_, err = s.inv.RunQueue(ctx)
	must(t, err)
	var onOutlet int
	must(t, s.Pool.QueryRow(ctx, `SELECT count(*) FROM inv_movements WHERE property_id = $1 AND source_type = 'POS_QUEUE' AND outlet_id = $2`, s.propID, outlet).Scan(&onOutlet))
	if onOutlet == 0 {
		t.Fatal("the premise: the movements of the banquet carry the outlet of the warehouse")
	}

	check := func(when string) {
		t.Helper()
		bd := day("2026-09-30")
		oc, err := s.inv.OutletCost(s.admin, s.propID, &bd, &bd)
		must(t, err)
		banquetRows, banquetTheory := 0, decimal.Zero
		for _, r := range oc.Rows {
			switch r.Source {
			case inventory.SourceBanquet:
				banquetRows++
				banquetTheory = banquetTheory.Add(r.Theory)
				if r.OutletID != nil || r.OutletCode != "BANQUET" {
					t.Errorf("%s: the banquet row has no outlet: %+v", when, r)
				}
			default:
				if !r.Theory.IsZero() || !r.Actual.IsZero() {
					t.Errorf("%s: the restaurant did not sell this: %+v", when, r)
				}
			}
		}
		// one row for each class (food, beverage), never a second one for the outlet of the warehouse
		if banquetRows != 2 || !banquetTheory.Equal(dec("435000")) {
			t.Fatalf("%s: %d rows of the banquet with the cost %s in %+v", when, banquetRows, banquetTheory, oc.Rows)
		}
		// the cost of the department is the one of the charge code of the lines, not the one of the outlet
		rep, err := s.inv.DailyCost(s.admin, s.propID, &bd, &bd)
		must(t, err)
		var chargeDept int64
		must(t, s.Pool.QueryRow(ctx, `SELECT department_id FROM charge_codes WHERE id = $1`, s.fb).Scan(&chargeDept))
		total := decimal.Zero
		for _, v := range rep.Period.Departments {
			total = total.Add(v)
		}
		if !rep.Period.Banquet.Cost.IsPositive() || !total.Equal(rep.Period.Banquet.Cost) || !rep.Period.Departments[fmt.Sprint(chargeDept)].Equal(total) {
			t.Errorf("%s: the cost %s is all of the department %d of the charge code: %v", when, rep.Period.Banquet.Cost, chargeDept, rep.Period.Departments)
		}
	}
	check("live")

	// stored: the day closes and the rows of inv_fb_cost_days say the same
	must(t, s.closeStock(t))
	check("stored")
	var banquetRows, onRestaurant int
	must(t, s.Pool.QueryRow(ctx, `SELECT count(*) FILTER (WHERE source = 'BANQUET' AND sale_cost > 0), count(*) FILTER (WHERE outlet_id = $2 AND sale_cost > 0) FROM inv_fb_cost_days WHERE property_id = $1`,
		s.propID, outlet).Scan(&banquetRows, &onRestaurant))
	if banquetRows != 2 || onRestaurant != 0 {
		t.Fatalf("inv_fb_cost_days: %d rows of the banquet with a cost, %d of the outlet (want 2 and 0)", banquetRows, onRestaurant)
	}
	var withOutlet int
	must(t, s.Pool.QueryRow(ctx, `SELECT count(*) FROM inv_fb_cost_days WHERE property_id = $1 AND source = 'BANQUET' AND outlet_id IS NOT NULL`, s.propID).Scan(&withOutlet))
	if withOutlet != 0 {
		t.Fatalf("%d stored rows of the banquet have an outlet", withOutlet)
	}
}
