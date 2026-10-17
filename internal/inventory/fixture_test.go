package inventory_test

import (
	"context"
	"os"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/inventory"
	"kamarapms/internal/inventory/inventorydb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func ptr[T any](v T) *T            { return &v }

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
}

// fx is a property with the inventory set up: the units kg and botol, a food and a beverage category, two warehouses (MAIN and BAR) and a few items.
type fx struct {
	*roomstest.Env
	svc              *inventory.Service
	ledger           *inventory.Ledger
	tenantID, propID int64
	admin            context.Context
	method           string
	decimals         int32
	accounts         map[string]int64
	unit             map[string]int64
	cat              map[string]int64
	wh               map[string]int64
	item             map[string]int64
	seq              int64
}

// newFx makes a property of the given currency precision whose costing method is method (chosen before any movement, as the module asks).
func newFx(t *testing.T, method string, decimals int32) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	code, cur := "BALI", "IDR"
	if decimals > 0 {
		cur = "USD"
	}
	p := e.PropertyIn(t, tn.ID, code, cur, decimals)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: roomstest.Admin(tn.ID), method: method, decimals: decimals,
		accounts: map[string]int64{}, unit: map[string]int64{}, cat: map[string]int64{}, wh: map[string]int64{}, item: map[string]int64{}}
	f.svc = inventory.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, e.Departments)
	f.ledger = &inventory.Ledger{}
	f.svc.SetPayables(e.Payables)
	f.svc.SetApprover(e.IAM)
	e.Payables.SetVoidGuard(f.svc)
	rows, err := e.Pool.Query(context.Background(), `SELECT code, id FROM gl_accounts WHERE property_id = $1`, p.ID)
	must(t, err)
	for rows.Next() {
		var c string
		var id int64
		must(t, rows.Scan(&c, &id))
		f.accounts[c] = id
	}
	rows.Close()
	must(t, rows.Err())
	st, err := f.svc.GetSettings(f.admin, p.ID)
	must(t, err)
	_, err = f.svc.UpdateSettings(f.admin, p.ID, inventory.SettingsInput{CostingMethod: method, PriceTolerancePct: st.PriceTolerancePct, QtyTolerancePct: st.QtyTolerancePct, OverReceiptPct: st.OverReceiptPct,
		PoRequiresPr: st.PoRequiresPr, CountBlocksNightAudit: st.CountBlocksNightAudit, CountBlind: st.CountBlind, Version: st.Version})
	must(t, err)
	for _, u := range [][2]string{{"kg", "Kilogram"}, {"g", "Gram"}, {"botol", "Botol"}, {"karton", "Karton"}, {"ml", "Mililiter"}, {"pcs", "Pieces"}} {
		r, err := f.svc.CreateUnit(f.admin, p.ID, inventory.UnitInput{Code: u[0], Name: u[1]})
		must(t, err)
		f.unit[u[0]] = r.ID
	}
	food, err := f.svc.CreateCategory(f.admin, p.ID, inventory.CategoryInput{Code: "FOOD", Name: "Food", Kind: "FOOD", InventoryAccountID: f.accounts["1310"], CogsAccountID: ptr(f.accounts["5210"])})
	must(t, err)
	bev, err := f.svc.CreateCategory(f.admin, p.ID, inventory.CategoryInput{Code: "BEV", Name: "Beverage", Kind: "BEVERAGE", InventoryAccountID: f.accounts["1320"], CogsAccountID: ptr(f.accounts["5220"])})
	must(t, err)
	f.cat["FOOD"], f.cat["BEV"] = food.ID, bev.ID
	for _, w := range []struct{ code, class string }{{"MAIN", "OTHER"}, {"BAR", "BEVERAGE"}} {
		kind := "MAIN"
		if w.code != "MAIN" {
			kind = "DEPARTMENT"
		}
		r, err := f.svc.CreateWarehouse(f.admin, p.ID, inventory.WarehouseInput{Code: w.code, Name: w.code + " store", Kind: kind, CostClass: w.class})
		must(t, err)
		f.wh[w.code] = r.ID
	}
	f.newItem(t, "BERAS", "Beras", "FOOD", "kg")
	f.newItem(t, "SIRUP", "Sirup", "BEV", "botol")
	return f
}

func (f *fx) newItem(t *testing.T, code, name, cat, unit string) int64 {
	t.Helper()
	it, err := f.svc.CreateItem(f.admin, f.propID, inventory.ItemInput{Code: code, Name: name, CategoryID: f.cat[cat], StockUnitID: f.unit[unit]})
	must(t, err)
	f.item[code] = it.ID
	return it.ID
}

func (f *fx) pair(w, i string) inventory.Pair {
	return inventory.Pair{WarehouseID: f.wh[w], ItemID: f.item[i]}
}

// ref is a new document line for a movement made by a test.
func (f *fx) ref() inventory.Ref {
	f.seq++
	return inventory.Ref{Type: "TEST", ID: f.seq, Line: 1}
}

// tx runs fn in a transaction the way a use case does: the open day in share mode, then the stock of the pairs locked, then fn.
func (f *fx) tx(pairs []inventory.Pair, fn func(ctx context.Context, st *inventory.Stock) error) error {
	return f.TxM.WithinTx(f.admin, func(ctx context.Context) error {
		day, err := f.Tenancy.RequireOpenBusinessDay(ctx, f.propID, db.ForShare, nil)
		if err != nil {
			return err
		}
		book := inventory.Book{TenantID: f.tenantID, PropertyID: f.propID, Date: day.BusinessDate, Decimals: f.decimals, Method: f.method}
		st, err := f.ledger.Lock(ctx, inventorydb.New(f.TxM.DB(ctx)), book, pairs, inventory.LockOptions{})
		if err != nil {
			return err
		}
		return fn(ctx, st)
	})
}

func (f *fx) in(t *testing.T, kind string, p inventory.Pair, qty, value string) inventory.Posted {
	t.Helper()
	var out inventory.Posted
	must(t, f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
		var err error
		out, err = st.In(ctx, inventory.InArgs{Pair: p, Kind: kind, Qty: dec(qty), Value: dec(value), Ref: f.ref()})
		return err
	}))
	return out
}

func (f *fx) out(t *testing.T, kind string, p inventory.Pair, qty string) inventory.Posted {
	t.Helper()
	o, err := f.tryOut(kind, p, qty)
	must(t, err)
	return o
}

func (f *fx) tryOut(kind string, p inventory.Pair, qty string) (inventory.Posted, error) {
	var out inventory.Posted
	err := f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
		var e error
		out, e = st.Out(ctx, inventory.OutArgs{Pair: p, Kind: kind, Qty: dec(qty), Ref: f.ref()})
		return e
	})
	return out, err
}

// sale takes goods out the way a POS sale does: below zero allowed, the cost of sales account of the food class.
func (f *fx) sale(t *testing.T, p inventory.Pair, qty string) inventory.Posted {
	t.Helper()
	var out inventory.Posted
	must(t, f.tx([]inventory.Pair{p}, func(ctx context.Context, st *inventory.Stock) error {
		var err error
		out, err = st.Out(ctx, inventory.AllowShortfall(inventory.OutArgs{Pair: p, Kind: inventory.KindPosSale, Qty: dec(qty), Ref: f.ref(), Counter: &inventory.Counter{AccountID: f.accounts["5210"]}}, "POS_SALE"))
		return err
	}))
	return out
}

// bal reads the balance row of a pair.
func (f *fx) bal(t *testing.T, p inventory.Pair) (qty, value string) {
	t.Helper()
	var q, v decimal.Decimal
	err := f.Pool.QueryRow(context.Background(), `SELECT qty, value FROM inv_balances WHERE warehouse_id = $1 AND item_id = $2`, p.WarehouseID, p.ItemID).Scan(&q, &v)
	if err != nil {
		return "0", "0"
	}
	return q.String(), v.String()
}

func (f *fx) wantBal(t *testing.T, p inventory.Pair, qty, value string) {
	t.Helper()
	q, v := f.bal(t, p)
	if !dec(q).Equal(dec(qty)) || !dec(v).Equal(dec(value)) {
		t.Fatalf("balance %v: got %s / %s, want %s / %s", p, q, v, qty, value)
	}
}

type layerRow struct{ qty, value decimal.Decimal }

func (f *fx) layers(t *testing.T, p inventory.Pair) []layerRow {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT qty_remaining, value_remaining FROM inv_layers WHERE warehouse_id = $1 AND item_id = $2 AND qty_remaining > 0 ORDER BY received_on, id`, p.WarehouseID, p.ItemID)
	must(t, err)
	defer rows.Close()
	var out []layerRow
	for rows.Next() {
		var l layerRow
		must(t, rows.Scan(&l.qty, &l.value))
		out = append(out, l)
	}
	must(t, rows.Err())
	return out
}

// consistent holds the invariants of 7.5 and 4.3: every balance is the sum of its ledger, and under FIFO the layers and the open shortfalls explain it.
func (f *fx) consistent(t *testing.T) {
	t.Helper()
	d, err := f.svc.Drift(context.Background(), f.tenantID, f.propID)
	must(t, err)
	if len(d.Balances) != 0 || len(d.Layers) != 0 {
		t.Fatalf("drift: %+v", d)
	}
}

func day(s string) civil.Date { return civil.MustParseDate(s) }

func inventorydbOf(f *fx, ctx context.Context) *inventorydb.Queries {
	return inventorydb.New(f.TxM.DB(ctx))
}

func adminPrincipal(f *fx) auth.Principal {
	return auth.Principal{TenantID: f.tenantID, IsTenantAdmin: true}
}
