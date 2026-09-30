package db_test

import (
	"context"
	"errors"
	"io/fs"
	"regexp"
	"testing"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/db"
	"kamarapms/migrations"
)

func TestMapErrorPassesThroughNonDatabaseErrors(t *testing.T) {
	if db.MapError(nil) != nil {
		t.Fatal("nil must stay nil")
	}
	plain := errors.New("plain")
	if !errors.Is(db.MapError(plain), plain) {
		t.Fatal("non-database errors must be returned unchanged")
	}
	app := apperr.Conflict("X", "x")
	if !errors.Is(db.MapError(app), app) {
		t.Fatal("*apperr.Error must be returned unchanged")
	}
}

// Every database rejection reaches the service layer as a stable API error.
func TestMapErrorTranslatesSchemaViolations(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) error {
		return f.txm.WithinTx(ctx, func(ctx context.Context) error {
			tx, err := db.Tx(ctx)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, sql, args...)
			return err
		})
	}
	mustExec := func(sql string, args ...any) {
		t.Helper()
		if err := exec(sql, args...); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}

	// Business-day history: close 30 Sep, open 1 Oct (night audit shape).
	mustExec(`INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, '2026-09-30')`, f.tenantID, f.propertyID)
	mustExec(`UPDATE business_days SET status = 'CLOSED', closed_at = now() WHERE property_id = $1`, f.propertyID)
	mustExec(`INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, '2026-10-01')`, f.tenantID, f.propertyID)
	mustExec(`INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason)
	          VALUES ($1, $2, $3, 'OOO', '2026-10-10', '2026-10-12', 'repair')`, f.tenantID, f.propertyID, f.roomIDs[0])
	mustExec(`INSERT INTO housekeeping_logs (tenant_id, property_id, room_id, from_status, to_status, source, business_date)
	          VALUES ($1, $2, $3, 'CLEAN', 'DIRTY', 'MANUAL', '2026-10-01')`, f.tenantID, f.propertyID, f.roomIDs[0])

	cases := []struct {
		name string
		sql  string
		args []any
		kind apperr.Kind
		code string
	}{
		{"unique constraint", `INSERT INTO tenants (code, name, timezone) VALUES ('ABC', 'dup', 'UTC')`, nil,
			apperr.KindConflict, "CODE_TAKEN"},
		{"trigger-raised constraint", `UPDATE business_days SET summary = '{}' WHERE property_id = $1 AND business_date = '2026-09-30'`,
			[]any{f.propertyID}, apperr.KindConflict, "BUSINESS_DAY_CLOSED"},
		{"second open day", `INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, '2026-10-02')`,
			[]any{f.tenantID, f.propertyID}, apperr.KindConflict, "BUSINESS_DAY_ALREADY_OPEN"},
		{"exclusion constraint", `INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason)
		                          VALUES ($1, $2, $3, 'OOS', '2026-10-11', '2026-10-13', 'paint')`,
			[]any{f.tenantID, f.propertyID, f.roomIDs[0]}, apperr.KindConflict, "ROOM_BLOCK_CONFLICT"},
		{"generic check constraint", `INSERT INTO room_types (tenant_id, property_id, code, name, max_adult, max_child, max_occupancy, base_occupancy)
		                              VALUES ($1, $2, 'STD', 'Standard', 2, 0, 3, 2)`,
			[]any{f.tenantID, f.propertyID}, apperr.KindInvalid, "VALIDATION_FAILED"},
		{"append-only table", `UPDATE housekeeping_logs SET notes = 'edit'`, nil,
			apperr.KindConflict, "RECORD_IMMUTABLE"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := exec(c.sql, c.args...)
			e, ok := apperr.As(err)
			if !ok || e.Kind != c.kind || e.Code != c.code {
				t.Fatalf("got %v, want %s %s", err, c.kind, c.code)
			}
			if e.Err == nil {
				t.Fatal("the database error must be kept as the cause")
			}
		})
	}
}

// Deferred constraint triggers fire at COMMIT; that failure must be mapped too.
func TestMapErrorAtCommit(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	err := f.txm.WithinTx(ctx, func(ctx context.Context) error {
		tx, _ := db.Tx(ctx)
		var chargeCodeID, reservationID, folioID int64
		steps := []struct {
			sql  string
			args []any
			dst  *int64
		}{
			{`INSERT INTO business_days (tenant_id, property_id, business_date) VALUES ($1, $2, '2026-10-01') RETURNING id`,
				[]any{f.tenantID, f.propertyID}, new(int64)},
			{`INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type) VALUES ($1, $2, 'LAUNDRY', 'Laundry', 'SERVICE') RETURNING id`,
				[]any{f.tenantID, f.propertyID}, &chargeCodeID},
			{`INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source) VALUES ($1, $2, 'R1', '2026-10-01', 'PHONE') RETURNING id`,
				[]any{f.tenantID, f.propertyID}, &reservationID},
		}
		for _, s := range steps {
			if err := tx.QueryRow(ctx, s.sql, s.args...).Scan(s.dst); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'F1', $3) RETURNING id`,
			f.tenantID, f.propertyID, reservationID).Scan(&folioID); err != nil {
			return err
		}
		// tax_total claims 11,000 but no tax component is written: only the deferred check catches it.
		_, err := tx.Exec(ctx, `INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type,
		                        charge_code_id, description, quantity, unit_price, price_mode, base_amount, net_amount, tax_total, debit, source)
		                        VALUES ($1, $2, $3, '2026-10-01', '2026-10-01', 'CHARGE', $4, 'Laundry', 1, 100000, 'EXCLUSIVE',
		                                100000, 100000, 11000, 111000, 'MANUAL')`,
			f.tenantID, f.propertyID, folioID, chargeCodeID)
		return err // succeeds here; the violation is only detected at COMMIT
	})
	if e, ok := apperr.As(err); !ok || e.Code != "LEDGER_INCONSISTENT" || e.Kind != apperr.KindInternal {
		t.Fatalf("got %v, want LEDGER_INCONSISTENT at commit", err)
	}
}

// Guards against typos and schema renames: every mapped name must exist, either as a
// real constraint/index or as a name raised by a trigger (RAISE ... CONSTRAINT = '...').
func TestConstraintMapMatchesSchema(t *testing.T) {
	f := setup(t)
	known := map[string]bool{}

	rows, err := f.pool.Query(context.Background(), `
		SELECT conname FROM pg_constraint c JOIN pg_namespace n ON n.oid = c.connamespace WHERE n.nspname = 'public'
		UNION
		SELECT indexname FROM pg_indexes WHERE schemaname = 'public'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		known[name] = true
	}
	rows.Close()

	raised := regexp.MustCompile(`CONSTRAINT = '([a-z0-9_]+)'`)
	err = fs.WalkDir(migrations.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(migrations.FS, path)
		for _, m := range raised.FindAllStringSubmatch(string(b), -1) {
			known[m[1]] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range db.MappedConstraintNames() {
		if !known[name] {
			t.Errorf("error map references %q, which does not exist in the schema", name)
		}
	}
}
