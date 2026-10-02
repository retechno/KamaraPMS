package migrate_test

import (
	"context"
	"os"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/platform/migrate"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

// Widening the money columns from two to three decimals (migration 00015) must keep every stored amount
// exactly: a database that already holds two-decimal data upgrades without loss.
func TestWideningMoneyColumnsKeepsExistingAmounts(t *testing.T) {
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
	}
	scale := func() int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT numeric_scale FROM information_schema.columns WHERE table_name = 'rates' AND column_name = 'amount'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if _, err := migrate.DownTo(ctx, pool, 14); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { // leave the shared database at the latest version for the rest of the run
		if _, err := migrate.Up(ctx, pool); err != nil {
			t.Error(err)
		}
	})
	if scale() != 2 {
		t.Fatalf("before 00015 the column has scale %d, want 2", scale())
	}

	exec(`INSERT INTO tenants (code, name, timezone) VALUES ('ABC', 'ABC', 'Asia/Jakarta')`)
	exec(`INSERT INTO properties (tenant_id, code, name, timezone, currency_code, currency_decimals, check_in_time, check_out_time,
	                              night_audit_earliest_time) SELECT id, 'BALI', 'Bali', 'Asia/Jakarta', 'USD', 2, '14:00', '12:00', '20:00' FROM tenants`)
	exec(`INSERT INTO room_types (tenant_id, property_id, code, name, max_adult, max_child, max_occupancy, base_occupancy)
	      SELECT tenant_id, id, 'DLX', 'Deluxe', 2, 0, 2, 2 FROM properties`)
	exec(`INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, default_unit_price)
	      SELECT tenant_id, id, 'ROOM', 'Room', 'ROOM', 99999999999999.99 FROM properties`)
	exec(`INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id)
	      SELECT tenant_id, property_id, 'BAR', 'BAR', id FROM charge_codes`)
	exec(`INSERT INTO rates (tenant_id, property_id, rate_plan_id, room_type_id, stay_date, amount)
	      SELECT p.tenant_id, p.property_id, p.id, t.id, '2026-10-01', 12.50 FROM rate_plans p JOIN room_types t ON t.property_id = p.property_id`)

	if _, err := migrate.Up(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if scale() != 3 {
		t.Fatalf("after 00015 the column has scale %d, want 3", scale())
	}
	var amount, price decimal.Decimal
	if err := pool.QueryRow(ctx, `SELECT r.amount, c.default_unit_price FROM rates r, charge_codes c`).Scan(&amount, &price); err != nil {
		t.Fatal(err)
	}
	if !amount.Equal(decimal.RequireFromString("12.5")) || !price.Equal(decimal.RequireFromString("99999999999999.99")) {
		t.Fatalf("stored amounts changed: %s and %s", amount, price)
	}
	exec(`UPDATE rates SET amount = 12.345`) // the third decimal is now kept
	if err := pool.QueryRow(ctx, `SELECT amount FROM rates`).Scan(&amount); err != nil || !amount.Equal(decimal.RequireFromString("12.345")) {
		t.Fatalf("three decimals: %v %s", err, amount)
	}
}
