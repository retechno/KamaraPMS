package app

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/subscription"
)

// Inventory, PR 1, through the router (docs/architecture/35-inventory.md): the masters, the opening stock, the stock card, the setup check, the module gate and the permissions; and
// the day close, which carries the stock into the journal, the control accounts and the reconciliation.

type invWorld struct {
	e      *apiEnv
	abc    *client
	prop   string
	base   string // .../inventory
	prefix string // .../properties/{id}
	acct   map[string]float64
	unit   map[string]float64
}

func newInvWorld(t *testing.T) *invWorld {
	t.Helper()
	e := newAPI(t)
	abc := e.login("ABC")
	prop := idOf(abc.do(http.MethodPost, "/api/v1/properties", validProperty()))
	w := &invWorld{e: e, abc: abc, prop: prop, prefix: "/api/v1/properties/" + prop, base: "/api/v1/properties/" + prop + "/inventory", acct: map[string]float64{}, unit: map[string]float64{}}
	for _, a := range abc.do(http.MethodGet, w.prefix+"/accounting/accounts?limit=500", nil).body["data"].([]any) {
		m := a.(map[string]any)
		w.acct[m["code"].(string)] = m["id"].(float64)
	}
	return w
}

func (w *invWorld) must(t *testing.T, r response, status int) response {
	t.Helper()
	if r.status != status {
		t.Fatalf("got %d %v, want %d", r.status, r.body, status)
	}
	return r
}

func (w *invWorld) setup(t *testing.T) (item, warehouse float64) {
	t.Helper()
	for _, u := range [][2]string{{"kg", "Kilogram"}, {"g", "Gram"}} {
		r := w.must(t, w.abc.do(http.MethodPost, w.base+"/units", map[string]any{"code": u[0], "name": u[1]}), 201)
		w.unit[u[0]] = r.body["id"].(float64)
	}
	cat := w.must(t, w.abc.do(http.MethodPost, w.base+"/categories", map[string]any{"code": "FOOD", "name": "Makanan", "kind": "FOOD", "inventory_account_id": w.acct["1310"], "cogs_account_id": w.acct["5210"]}), 201)
	wh := w.must(t, w.abc.do(http.MethodPost, w.base+"/warehouses", map[string]any{"code": "MAIN", "name": "Gudang Utama", "kind": "MAIN", "cost_class": "OTHER"}), 201)
	it := w.must(t, w.abc.do(http.MethodPost, w.base+"/items", map[string]any{"code": "BERAS", "name": "Beras pandan wangi", "category_id": cat.body["id"], "stock_unit_id": w.unit["kg"],
		"units": []map[string]any{{"unit_id": w.unit["g"], "stock_num": 1, "stock_den": 1000, "is_recipe": true, "is_default_recipe": true}}}), 201)
	return it.body["id"].(float64), wh.body["id"].(float64)
}

func TestInventoryMastersOpeningAndStockAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc

	st := w.must(t, abc.do(http.MethodGet, w.base+"/settings", nil), 200)
	if st.body["exists"] != false || st.body["costing_method"] != "AVERAGE" || st.body["costing_locked_on"] != nil {
		t.Fatalf("settings: %v", st.body)
	}
	put := map[string]any{"costing_method": "FIFO", "price_tolerance_pct": "1.00", "qty_tolerance_pct": "0.00", "over_receipt_pct": "5.00", "po_requires_pr": true, "count_blocks_night_audit": true, "count_blind": true, "version": 1}
	st = w.must(t, abc.do(http.MethodPut, w.base+"/settings", put), 200)
	if st.body["costing_method"] != "FIFO" || st.body["version"] != float64(2) {
		t.Fatalf("settings after: %v", st.body)
	}
	put["costing_method"], put["version"] = "AVERAGE", 2
	w.must(t, abc.do(http.MethodPut, w.base+"/settings", put), 200)

	item, wh := w.setup(t)

	// the list: the item with its units, the columns of the screen
	list := w.must(t, abc.do(http.MethodGet, w.base+"/items?q=beras", nil), 200)
	items := list.body["items"].([]any)
	if len(items) != 1 || list.body["total"] != float64(1) || list.body["costing_method"] != "AVERAGE" || list.body["costing_locked"] != false || len(list.body["warehouses"].([]any)) != 1 {
		t.Fatalf("items: %v", list.body)
	}
	got := w.must(t, abc.do(http.MethodGet, w.base+"/items/"+itoaID(int64(item)), nil), 200)
	if us := got.body["units"].([]any); len(us) != 2 || us[0].(map[string]any)["unit_code"] != "kg" {
		t.Fatalf("units: %v", got.body["units"])
	}
	w.must(t, abc.do(http.MethodGet, w.base+"/items?category=abc", nil), 422)
	if r := abc.do(http.MethodGet, w.base+"/items/999999", nil); r.status != 404 || r.body["code"] != "ITEM_NOT_FOUND" {
		t.Fatalf("a missing item: %d %v", r.status, r.body)
	}

	// the opening stock
	body := map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a11", "warehouse_id": wh, "note": "Saldo awal 1 Okt",
		"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "120", "unit_cost": "17360"}}}
	op := w.must(t, abc.do(http.MethodPost, w.base+"/openings", body), 201)
	if op.body["number"] != "OPN-000001" || op.body["total_value"] != "2083200" || len(op.body["lines"].([]any)) != 1 {
		t.Fatalf("opening: %v", op.body)
	}
	again := w.must(t, abc.do(http.MethodPost, w.base+"/openings", body), 201)
	if again.body["id"] != op.body["id"] {
		t.Fatalf("the repeated request made a second opening: %v", again.body)
	}
	body["client_uuid"] = "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a12"
	if r := abc.do(http.MethodPost, w.base+"/openings", body); r.status != 422 || fieldsOf(r)["lines[0].item_id"] != "OPENING_EXISTS" {
		t.Fatalf("a second opening of the item: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, w.base+"/openings", nil); r.status != 200 || len(r.body["data"].([]any)) != 1 {
		t.Fatalf("openings: %d %v", r.status, r.body)
	}
	if r := abc.do(http.MethodGet, w.base+"/openings/"+idOf(op), nil); r.status != 200 || r.body["number"] != "OPN-000001" {
		t.Fatalf("opening: %d %v", r.status, r.body)
	}

	// the method is locked now
	put["costing_method"], put["version"] = "FIFO", 4
	if r := abc.do(http.MethodPut, w.base+"/settings", put); r.status != 409 || r.body["code"] != "VERSION_CONFLICT" {
		t.Fatalf("stale version: %d %v", r.status, r.body)
	}
	cur := w.must(t, abc.do(http.MethodGet, w.base+"/settings", nil), 200)
	put["version"] = cur.body["version"]
	if r := abc.do(http.MethodPut, w.base+"/settings", put); r.status != 409 || r.body["code"] != "COSTING_METHOD_LOCKED" {
		t.Fatalf("the method after the first movement: %d %v", r.status, r.body)
	}
	if cur.body["costing_locked_on"] != "2026-09-30" {
		t.Fatalf("locked on %v", cur.body["costing_locked_on"])
	}

	// the stock card
	card := w.must(t, abc.do(http.MethodGet, w.base+"/stock-card?warehouse="+itoaID(int64(wh))+"&item="+itoaID(int64(item)), nil), 200)
	lines := card.body["lines"].([]any)
	if len(lines) != 1 || lines[0].(map[string]any)["document_number"] != "OPN-000001" || card.body["closing_qty"] != "120" || card.body["closing_value"] != "2083200" {
		t.Fatalf("card: %v", card.body)
	}
	w.must(t, abc.do(http.MethodGet, w.base+"/stock-card?warehouse=1", nil), 422)
	list = w.must(t, abc.do(http.MethodGet, w.base+"/items", nil), 200)
	row := list.body["items"].([]any)[0].(map[string]any)
	if row["qty_on_hand"] != "120" || row["value_on_hand"] != "2083200" || list.body["total_value"] != "2083200" || list.body["costing_locked"] != true {
		t.Fatalf("the list after the opening: %v", list.body)
	}

	// the setup check: nothing wrong, the keys of the stock are mapped for a new property
	sc := w.must(t, abc.do(http.MethodGet, w.base+"/setup-check", nil), 200)
	if sc.body["ok"] != true || sc.body["costing_locked_on"] != "2026-09-30" {
		t.Fatalf("setup check: %v", sc.body)
	}
	for _, i := range sc.body["issues"].([]any) {
		if i.(map[string]any)["code"] == "ACCOUNT_MAP_INCOMPLETE" {
			t.Fatalf("a key of the stock is not mapped for a new property: %v", i)
		}
	}
}

func TestInventoryPermissionsAndTheModuleGateAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	viewer := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Store viewer", "permissions": []string{"inventory.view"}})
	if viewer.status != 201 {
		t.Fatalf("role: %d %v", viewer.status, viewer.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": "viewer@hotel.com", "full_name": "Viewer", "password": testPassword,
		"grants": []map[string]any{{"property_id": mustInt(w.prop), "role_id": viewer.body["id"]}}}); r.status != 201 {
		t.Fatalf("user: %d %v", r.status, r.body)
	}
	v := &client{env: w.e}
	if r := v.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "viewer@hotel.com", "password": testPassword}); r.status != 200 {
		t.Fatalf("login: %d %v", r.status, r.body)
	}
	if r := v.do(http.MethodGet, w.base+"/items", nil); r.status != 200 {
		t.Fatalf("a viewer reads the items: %d %v", r.status, r.body)
	}
	if r := v.do(http.MethodPost, w.base+"/units", map[string]any{"code": "x", "name": "X"}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("a viewer adds a unit: %d %v", r.status, r.body)
	}
	if r := v.do(http.MethodPost, w.base+"/openings", map[string]any{}); r.status != 403 || r.body["code"] != "PERMISSION_DENIED" {
		t.Fatalf("a viewer posts an opening: %d %v", r.status, r.body)
	}
	// another tenant's property is not found
	xyz := w.e.login("XYZ")
	if r := xyz.do(http.MethodGet, w.base+"/items", nil); r.status != 404 || r.body["code"] != "PROPERTY_NOT_FOUND" {
		t.Fatalf("another tenant: %d %v", r.status, r.body)
	}

	// a plan without the module
	txm := db.NewTxManager(dbtest.Pool(t), 5*time.Second)
	svc := subscription.NewService(txm, w.e.clock, audit.NewWriter(w.e.clock))
	ctx := context.Background()
	if _, err := svc.CreatePlan(ctx, subscription.PlanInput{Code: "NOINV", Name: "No inventory", Modules: []subscription.Module{subscription.Housekeeping}, Price: decimal.RequireFromString("1"), CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("NOINV")}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/items", "/units", "/settings", "/stock-card?warehouse=1&item=1"} {
		if r := abc.do(http.MethodGet, w.base+p, nil); r.status != 403 || r.body["code"] != "MODULE_NOT_IN_PLAN" || r.body["context"].(map[string]any)["module"] != "INVENTORY" {
			t.Fatalf("%s without the module: %d %v", p, r.status, r.body)
		}
	}
}

// The opening stock goes into the journal of the day it was posted on, and the books and the stock agree afterwards.
func TestInventoryDayCloseJournalControlAccountsAndReconciliationAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	item, wh := w.setup(t)
	w.must(t, abc.do(http.MethodPost, w.base+"/openings", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a21", "warehouse_id": wh,
		"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "120", "unit_cost": "17360"}}}), 201)
	pool := dbtest.Pool(t)

	// before the close the books are empty: the stock is ahead of them by the day that is open
	rec := w.must(t, abc.do(http.MethodGet, w.prefix+"/accounting/reconciliation", nil), 200)
	var ctl map[string]any
	for _, c := range rec.body["controls"].([]any) {
		if c.(map[string]any)["key"] == "INVENTORY_1310" {
			ctl = c.(map[string]any)
		}
	}
	if ctl == nil || ctl["source"] != "2083200" || ctl["ledger"] != "0" {
		t.Fatalf("the control of 1310 before the close: %v", ctl)
	}

	// the close: the preview has no finding of the stock, the journal carries the opening
	pv := w.must(t, abc.do(http.MethodGet, w.prefix+"/night-audit/preview", nil), 200)
	if len(pv.body["blockers"].(map[string]any)["stock"].([]any)) != 0 {
		t.Fatalf("stock blockers: %v", pv.body["blockers"])
	}
	w.must(t, abc.do(http.MethodPost, w.prefix+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}), 200)
	rows, err := pool.Query(context.Background(), `SELECT a.code, l.debit::text, l.credit::text, l.source_type, l.source_ref FROM gl_journal_lines l
		JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
		WHERE j.property_id = $1 AND j.journal_type = 'DAY_CLOSE' ORDER BY a.code`, mustInt(w.prop))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type jl struct{ code, debit, credit, src, ref string }
	var got []jl
	for rows.Next() {
		var l jl
		if err := rows.Scan(&l.code, &l.debit, &l.credit, &l.src, &l.ref); err != nil {
			t.Fatal(err)
		}
		got = append(got, l)
	}
	if len(got) != 2 || got[0] != (jl{"1310", "2083200.000", "0.000", "INV_OPENING", "OPN-000001"}) || got[1] != (jl{"3900", "0.000", "2083200.000", "INV_OPENING", "OPN-000001"}) {
		t.Fatalf("the journal of the day: %+v", got)
	}

	// the books and the stock agree
	rec = w.must(t, abc.do(http.MethodGet, w.prefix+"/accounting/reconciliation", nil), 200)
	for _, c := range rec.body["controls"].([]any) {
		if m := c.(map[string]any); m["key"] == "INVENTORY_1310" && (m["ledger"] != "2083200" || m["source"] != "2083200" || m["difference"] != "0") {
			t.Fatalf("the control of 1310 after the close: %v", m)
		}
	}

	// from the first movement the inventory account is the stock's own
	manual := map[string]any{"journal_date": "2026-10-01", "description": "By hand", "lines": []map[string]any{
		{"account_id": w.acct["1310"], "debit": "1000", "credit": "0"}, {"account_id": w.acct["3900"], "debit": "0", "credit": "1000"}}}
	if r := abc.doWith(http.MethodPost, w.prefix+"/accounting/journals", manual, map[string]string{"Idempotency-Key": "m1"}); r.status != 422 || fieldsOf(r)["lines[0].account_id"] != "CONTROL_ACCOUNT" {
		t.Fatalf("a manual journal on the inventory account: %d %v", r.status, r.body)
	}
	sup := w.must(t, abc.do(http.MethodPost, w.prefix+"/payables/suppliers", map[string]any{"code": "PLN", "name": "PLN"}), 201)
	bill := map[string]any{"supplier_id": sup.body["id"], "supplier_invoice_number": "INV-1", "bill_date": "2026-10-01", "lines": []map[string]any{{"account_id": w.acct["1310"], "amount": "1000", "description": "Rice"}}}
	if r := abc.doWith(http.MethodPost, w.prefix+"/payables/bills", bill, map[string]string{"Idempotency-Key": "b1"}); r.status != 422 || fieldsOf(r)["lines[0].account_id"] != "CONTROL_ACCOUNT" {
		t.Fatalf("a bill line on the inventory account: %d %v", r.status, r.body)
	}
	// an expense account is still open to a bill
	bill["lines"] = []map[string]any{{"account_id": w.acct["6510"], "amount": "1000", "description": "Electricity"}}
	w.must(t, abc.doWith(http.MethodPost, w.prefix+"/payables/bills", bill, map[string]string{"Idempotency-Key": "b2"}), 201)
}

// The close of the day refuses when the stock disagrees with its own ledger or with the general ledger.
func TestInventoryNightAuditBlocksWhenTheBooksDisagreeAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	item, wh := w.setup(t)
	w.must(t, abc.do(http.MethodPost, w.base+"/openings", map[string]any{"client_uuid": "0b1c0f7a-9d4e-4f6b-8a2e-1c3d5e7f9a31", "warehouse_id": wh,
		"lines": []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "10", "unit_cost": "1000"}}}), 201)
	pool := dbtest.Pool(t)
	blockers := func() []any {
		pv := w.must(t, abc.do(http.MethodGet, w.prefix+"/night-audit/preview", nil), 200)
		return pv.body["blockers"].(map[string]any)["stock"].([]any)
	}
	if len(blockers()) != 0 {
		t.Fatalf("blockers: %v", blockers())
	}
	// a balance that someone changed behind the writer
	if _, err := pool.Exec(context.Background(), `UPDATE inv_balances SET qty = qty + 1, value = value + 100`); err != nil {
		t.Fatal(err)
	}
	b := blockers()
	if len(b) != 1 || b[0].(map[string]any)["code"] != "INVENTORY_BALANCE_DRIFT" {
		t.Fatalf("drift: %v", b)
	}
	if r := abc.do(http.MethodPost, w.prefix+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}); r.status != 409 || r.body["code"] != "NIGHT_AUDIT_BLOCKED" {
		t.Fatalf("the close with a drifting balance: %d %v", r.status, r.body)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE inv_balances SET qty = qty - 1, value = value - 100`); err != nil {
		t.Fatal(err)
	}
	if len(blockers()) != 0 {
		t.Fatal("the blocker stays after the balance is right again")
	}
	// a day whose journal does not carry the stock: the general ledger is behind
	if _, err := pool.Exec(context.Background(), `INSERT INTO gl_day_posts (tenant_id, property_id, business_date, posted_at) SELECT tenant_id, property_id, business_date, now() FROM business_days WHERE property_id = $1 AND status = 'OPEN'`, mustInt(w.prop)); err != nil {
		t.Fatal(err)
	}
	b = blockers()
	if len(b) != 1 || b[0].(map[string]any)["code"] != "INVENTORY_GL_MISMATCH" || b[0].(map[string]any)["ref"] != "1310" {
		t.Fatalf("GL mismatch: %v", b)
	}
}

// Inventory, PR 2, through the router (docs/architecture/35-inventory.md 5.3, 5.8): a purchase request passes its approval chain, becomes an order, is received in part and leaves a draft bill; the
// permissions of each step, the own-document rule, the tenant wall, and the books that agree with the stock on the account of goods received not invoiced.
func TestInventoryPurchasingFlowAPI(t *testing.T) {
	w := newInvWorld(t)
	abc := w.abc
	item, wh := w.setup(t)
	// the people: the requester, the head of the department, the cost controller and the buyer
	requester := userWith(t, w.e, abc, w.prop, "requester@hotel.com", "inventory.view", "inventory.request")
	head := userWith(t, w.e, abc, w.prop, "head@hotel.com", "inventory.view", "inventory.dept_approve")
	headID := idOfMe(t, head)
	cost := userWith(t, w.e, abc, w.prop, "cost@hotel.com", "inventory.view", "inventory.cost_control")
	buyer := userWith(t, w.e, abc, w.prop, "buyer@hotel.com", "inventory.view", "inventory.purchase", "inventory.receive", "inventory.bill")

	sup := w.must(t, abc.do(http.MethodPost, w.prefix+"/payables/suppliers", map[string]any{"code": "SUMBER", "name": "CV Sumber Segar"}), 201)
	deps := w.must(t, abc.do(http.MethodGet, w.prefix+"/departments", nil), 200).body["data"].([]any)
	dep := deps[0].(map[string]any)["id"]
	w.must(t, abc.do(http.MethodPatch, w.prefix+"/departments/"+strconv.FormatInt(int64(dep.(float64)), 10), map[string]any{"head_user_id": headID}), 200)

	line := []map[string]any{{"item_id": item, "unit_id": w.unit["kg"], "qty": "10", "est_unit_price": "50000", "supplier_id": sup.body["id"]}}
	pr := w.must(t, requester.do(http.MethodPost, w.base+"/requests", map[string]any{"client_uuid": "1d1f0c2a-0c4e-4b5a-9a77-3b6f0a1c2d01", "department_id": dep, "lines": line}), 201)
	id := idOf(pr)
	// only the requester writes it; a person without the permission cannot
	w.must(t, buyer.do(http.MethodPost, w.base+"/requests", map[string]any{"client_uuid": "1d1f0c2a-0c4e-4b5a-9a77-3b6f0a1c2d02", "department_id": dep, "lines": line}), 403)
	w.must(t, requester.do(http.MethodPost, w.base+"/requests/"+id+"/submit", nil), 200)

	// level 1 waits for the head: the inbox of the head has it, the inbox of the requester and of the cost controller does not (yet)
	if n := len(w.must(t, head.do(http.MethodGet, w.base+"/approvals/inbox", nil), 200).body["data"].([]any)); n != 1 {
		t.Fatalf("the inbox of the head: %d", n)
	}
	if n := len(w.must(t, cost.do(http.MethodGet, w.base+"/approvals/inbox", nil), 200).body["data"].([]any)); n != 0 {
		t.Fatalf("the inbox of the cost controller before level 1: %d", n)
	}
	// the cost controller cannot jump the queue, and the requester cannot approve their own request
	w.must(t, cost.do(http.MethodPost, w.base+"/requests/"+id+"/approve", map[string]any{}), 403)
	w.must(t, requester.do(http.MethodPost, w.base+"/requests/"+id+"/approve", map[string]any{}), 403)
	w.must(t, head.do(http.MethodPost, w.base+"/requests/"+id+"/approve", map[string]any{}), 200)
	got := w.must(t, cost.do(http.MethodPost, w.base+"/requests/"+id+"/approve", map[string]any{}), 200)
	if got.body["status"] != "APPROVED" {
		t.Fatalf("after both levels: %v", got.body["status"])
	}

	// the order, from the request, split by supplier; sent at once
	ord := w.must(t, buyer.do(http.MethodPost, w.base+"/requests/"+id+"/purchase-orders", map[string]any{"send": true, "warehouse_id": wh,
		"orders": []map[string]any{{"client_uuid": "2e2f0c2a-0c4e-4b5a-9a77-3b6f0a1c2d01", "supplier_id": sup.body["id"], "lines": []map[string]any{{"line_no": 1, "unit_price": "50000"}}}}}), 201)
	orders := ord.body["data"].([]any)
	if len(orders) != 1 || orders[0].(map[string]any)["status"] != "ORDERED" {
		t.Fatalf("orders: %v", ord.body)
	}
	po := orders[0].(map[string]any)["id"]

	// the receipt: 4 of the 10, at the price of the order; the stock and the draft bill follow
	rc := w.must(t, buyer.do(http.MethodPost, w.base+"/receipts", map[string]any{"client_uuid": "3f3f0c2a-0c4e-4b5a-9a77-3b6f0a1c2d01", "supplier_id": sup.body["id"], "po_id": po, "warehouse_id": wh,
		"lines": []map[string]any{{"po_line_no": 1, "item_id": item, "unit_id": w.unit["kg"], "qty": "4"}}}), 201)
	if rc.body["total_value"] != "200000" {
		t.Fatalf("the receipt: %v", rc.body)
	}
	w.must(t, requester.do(http.MethodPost, w.base+"/receipts", map[string]any{"client_uuid": "3f3f0c2a-0c4e-4b5a-9a77-3b6f0a1c2d02", "supplier_id": sup.body["id"], "po_id": po, "warehouse_id": wh,
		"lines": []map[string]any{{"po_line_no": 1, "item_id": item, "unit_id": w.unit["kg"], "qty": "1"}}}), 403)
	// more than the order allows (10 ordered, 4 in, 5% over-receipt): refused
	w.must(t, buyer.do(http.MethodPost, w.base+"/receipts", map[string]any{"client_uuid": "3f3f0c2a-0c4e-4b5a-9a77-3b6f0a1c2d03", "supplier_id": sup.body["id"], "po_id": po, "warehouse_id": wh,
		"lines": []map[string]any{{"po_line_no": 1, "item_id": item, "unit_id": w.unit["kg"], "qty": "7"}}}), 409)
	drafts := w.must(t, buyer.do(http.MethodGet, w.base+"/bill-drafts?open=true", nil), 200).body["data"].([]any)
	if len(drafts) != 1 || drafts[0].(map[string]any)["status"] != "OPEN" || drafts[0].(map[string]any)["remaining_value"] != "200000" {
		t.Fatalf("the draft bills: %v", drafts)
	}
	if o := w.must(t, buyer.do(http.MethodGet, w.base+"/purchase-orders/"+strconv.FormatInt(int64(po.(float64)), 10), nil), 200); o.body["status"] != "PARTIAL" {
		t.Fatalf("the order after a part: %v", o.body["status"])
	}

	// the books: the stock is ahead of the ledger by the day that is open; after the close the account of goods received not invoiced agrees with the receipts
	w.must(t, abc.do(http.MethodPost, w.prefix+"/night-audit/run", map[string]any{"business_date": "2026-09-30"}), 200)
	rec := w.must(t, abc.do(http.MethodGet, w.prefix+"/accounting/reconciliation", nil), 200)
	var seen bool
	for _, c := range rec.body["controls"].([]any) {
		if m := c.(map[string]any); m["key"] == "INVENTORY_2130" {
			seen = true
			if m["difference"] != "0" || m["source"] != "-200000" {
				t.Fatalf("the control of goods received not invoiced: %v", m)
			}
		}
	}
	if !seen {
		t.Fatalf("no control for goods received not invoiced: %v", rec.body["controls"])
	}

	// the tenant wall: another tenant finds neither the property nor the request
	xyz := w.e.login("XYZ")
	w.must(t, xyz.do(http.MethodGet, w.base+"/requests/"+id, nil), 404)
	// the module is not for who has no permission of it at all
	nobody := userWith(t, w.e, abc, w.prop, "nobody@hotel.com", "folio.read")
	w.must(t, nobody.do(http.MethodGet, w.base+"/requests/"+id, nil), 403)
}

// idOfMe is the id of the signed-in user.
func idOfMe(t *testing.T, c *client) int64 {
	t.Helper()
	me := c.do(http.MethodGet, "/api/v1/auth/me", nil)
	if me.status != 200 {
		t.Fatalf("me: %d %v", me.status, me.body)
	}
	return int64(me.body["user"].(map[string]any)["id"].(float64))
}
