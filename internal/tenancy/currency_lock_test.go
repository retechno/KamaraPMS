package tenancy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"kamarapms/internal/tenancy"
)

// The currency lock (docs/architecture/18-architecture-decisions.md, decision 3): a property can change its currency and decimals only while it has no financial
// data of any kind. The definition is one database function, used by the trigger and by the service.

type sqler interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func mustExec(t *testing.T, db sqler, sql string, args ...any) {
	t.Helper()
	if _, err := db.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
}

func mustID(t *testing.T, db sqler, sql string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(context.Background(), sql, args...).Scan(&id); err != nil {
		t.Fatalf("%v\n%s", err, sql)
	}
	return id
}

// addAccounts gives the property a cash and a capital account (configuration: it does not lock the currency).
func addAccounts(t *testing.T, db sqler, tenantID, propertyID int64) (cash, capital int64) {
	t.Helper()
	cash = mustID(t, db, `INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH') RETURNING id`, tenantID, propertyID)
	capital = mustID(t, db, `INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, '3100', 'Capital', 'EQUITY', 'CREDIT', 'EQUITY') RETURNING id`, tenantID, propertyID)
	return cash, capital
}

// addJournal writes a balanced journal (the first financial data of the property when nothing else exists).
func addJournal(t *testing.T, db sqler, tenantID, propertyID, cash, capital int64) {
	t.Helper()
	j := mustID(t, db, `INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES ($1, $2, 'J1', 'MANUAL', '2026-09-30', 'Opening') RETURNING id`, tenantID, propertyID)
	mustExec(t, db, `INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) VALUES ($1, $2, $3, 1, $4, 100, 0), ($1, $2, $3, 2, $5, 0, 100)`, tenantID, propertyID, j, cash, capital)
}

func addBankStatement(t *testing.T, db sqler, tenantID, propertyID, cash int64) {
	t.Helper()
	ba := mustID(t, db, `INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES ($1, $2, $3, 'Main bank') RETURNING id`, tenantID, propertyID, cash)
	mustExec(t, db, `INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance) VALUES ($1, $2, $3, '2026-09-01', '2026-09-30', 0, 0)`, tenantID, propertyID, ba)
}

func (e env) changeCurrency(t *testing.T, tenantID, propertyID int64, code string, decimals int32) error {
	t.Helper()
	_, err := e.svc.UpdateProperty(admin(tenantID), propertyID, tenancy.PropertyPatch{CurrencyCode: &code, CurrencyDecimals: &decimals})
	return err
}

func (e env) currencyOf(t *testing.T, propertyID int64) (string, int32) {
	t.Helper()
	var code string
	var decimals int32
	if err := e.pool.QueryRow(context.Background(), `SELECT currency_code, currency_decimals FROM properties WHERE id = $1`, propertyID).Scan(&code, &decimals); err != nil {
		t.Fatal(err)
	}
	return code, decimals
}

// rawChange is the same change without the service: a plain UPDATE, which only the trigger guards.
func (e env) rawChange(db sqler, propertyID int64, code string, decimals int32) error {
	_, err := db.Exec(context.Background(), `UPDATE properties SET currency_code = $2, currency_decimals = $3 WHERE id = $1`, propertyID, code, decimals)
	return err
}

func isRestrict(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23001" && pg.ConstraintName == "properties_currency_lock"
}

func TestAPropertyWithoutFinancialDataCanChangeItsCurrency(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	p := e.property(t, tn.ID, "BALI").Property
	if has := mustID(t, e.pool, `SELECT CASE WHEN property_has_financial_data($1) THEN 1 ELSE 0 END`, p.ID); has != 0 {
		t.Fatal("a new property has no financial data")
	}
	if err := e.changeCurrency(t, tn.ID, p.ID, "USD", 2); err != nil {
		t.Fatalf("a new property: %v", err)
	}
	// configuration is not financial data: accounts, a bank account, a company
	cash, _ := addAccounts(t, e.pool, tn.ID, p.ID)
	mustID(t, e.pool, `INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES ($1, $2, $3, 'Main bank') RETURNING id`, tn.ID, p.ID, cash)
	mustExec(t, e.pool, `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme', 1000000)`, tn.ID, p.ID)
	if err := e.changeCurrency(t, tn.ID, p.ID, "KWD", 3); err != nil {
		t.Fatalf("a property with configuration only: %v", err)
	}
	if code, dec := e.currencyOf(t, p.ID); code != "KWD" || dec != 3 {
		t.Fatalf("the change is stored: %s %d", code, dec)
	}
	// the settings that are not the currency change freely whatever the data
	name := "Hotel Bali Resort"
	if _, err := e.svc.UpdateProperty(admin(tn.ID), p.ID, tenancy.PropertyPatch{Name: &name}); err != nil {
		t.Fatalf("name: %v", err)
	}
}

// Every way of changing the currency answers the same rule: the service (the API) and a plain UPDATE (the trigger) both refuse once the property has financial
// data of any kind, and both accept before. The function that defines "financial data" is the one place.
func TestEveryPathRefusesTheCurrencyOnceFinancialDataExists(t *testing.T) {
	cases := []struct {
		name string
		add  func(t *testing.T, e env, tenantID, propertyID int64)
	}{
		{"a folio item", func(t *testing.T, e env, tenantID, propertyID int64) {
			postLaundryCharge(t, e.pool, tenantID, propertyID)
		}},
		{"a journal", func(t *testing.T, e env, tenantID, propertyID int64) {
			cash, capital := addAccounts(t, e.pool, tenantID, propertyID)
			tx, err := e.pool.Begin(context.Background()) // a journal is checked as a whole at commit
			if err != nil {
				t.Fatal(err)
			}
			addJournal(t, tx, tenantID, propertyID, cash, capital)
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
		}},
		{"a bank statement", func(t *testing.T, e env, tenantID, propertyID int64) {
			cash, _ := addAccounts(t, e.pool, tenantID, propertyID)
			addBankStatement(t, e.pool, tenantID, propertyID, cash)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := setup(t)
			tn := e.tenant(t, "ABC")
			p := e.property(t, tn.ID, "BALI").Property
			// before: both paths accept (and the change is undone by the next one)
			if err := e.changeCurrency(t, tn.ID, p.ID, "USD", 2); err != nil {
				t.Fatalf("service before: %v", err)
			}
			if err := e.rawChange(e.pool, p.ID, "SGD", 2); err != nil {
				t.Fatalf("trigger before: %v", err)
			}
			// the property says whether it is locked, by the same definition (the form shows the currency read-only)
			if v, err := e.svc.GetPropertyWithDay(admin(tn.ID), p.ID); err != nil || v.CurrencyLocked {
				t.Fatalf("before the data the currency is not locked: %v %v", v.CurrencyLocked, err)
			}
			c.add(t, e, tn.ID, p.ID)
			if v, err := e.svc.GetPropertyWithDay(admin(tn.ID), p.ID); err != nil || !v.CurrencyLocked {
				t.Fatalf("after the data the currency is locked: %v %v", v.CurrencyLocked, err)
			}
			if has := mustID(t, e.pool, `SELECT CASE WHEN property_has_financial_data($1) THEN 1 ELSE 0 END`, p.ID); has != 1 {
				t.Fatal("the definition sees the data")
			}
			wantCode(t, e.changeCurrency(t, tn.ID, p.ID, "IDR", 0), "CURRENCY_LOCKED")
			if err := e.rawChange(e.pool, p.ID, "IDR", 0); !isRestrict(err) {
				t.Fatalf("the trigger must refuse with properties_currency_lock: %v", err)
			}
			// the decimals alone are locked too
			wantCode(t, e.changeCurrency(t, tn.ID, p.ID, "SGD", 3), "CURRENCY_LOCKED")
			if code, dec := e.currencyOf(t, p.ID); code != "SGD" || dec != 2 {
				t.Fatalf("nothing changed: %s %d", code, dec)
			}
			// the same values are not a change
			if err := e.changeCurrency(t, tn.ID, p.ID, "SGD", 2); err != nil {
				t.Fatalf("an unchanged currency passes: %v", err)
			}
			// another property of the tenant is not affected
			other := e.property(t, tn.ID, "JKT").Property
			if err := e.changeCurrency(t, tn.ID, other.ID, "USD", 2); err != nil {
				t.Fatalf("another property: %v", err)
			}
		})
	}
}

// A currency update and the first financial write of a property race: the one that commits first wins and the other sees it. Both orders are tested, through the
// service and through a plain UPDATE.
func TestACurrencyUpdateWaitsForAFirstFinancialWriteInFlight(t *testing.T) {
	for _, path := range []string{"service", "trigger"} {
		t.Run(path, func(t *testing.T) {
			e := setup(t)
			tn := e.tenant(t, "ABC")
			p := e.property(t, tn.ID, "BALI").Property
			cash, capital := addAccounts(t, e.pool, tn.ID, p.ID)

			tx, err := e.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.Background()) }()
			addJournal(t, tx, tn.ID, p.ID, cash, capital) // the first financial write, not committed yet

			done := make(chan error, 1)
			go func() {
				if path == "service" {
					done <- e.changeCurrency(t, tn.ID, p.ID, "USD", 2)
				} else {
					done <- e.rawChange(e.pool, p.ID, "USD", 2)
				}
			}()
			select {
			case err := <-done:
				t.Fatalf("the update must wait for the write in flight, it returned: %v", err)
			case <-time.After(600 * time.Millisecond):
			}
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if path == "service" {
					wantCode(t, err, "CURRENCY_LOCKED")
				} else if !isRestrict(err) {
					t.Fatalf("the trigger must refuse once the write is committed: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the update never finished after the write committed")
			}
			if code, dec := e.currencyOf(t, p.ID); code != "IDR" || dec != 0 {
				t.Fatalf("the currency did not change: %s %d", code, dec)
			}
		})
	}
}

func TestAFirstFinancialWriteWaitsForACurrencyUpdateInFlight(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	p := e.property(t, tn.ID, "BALI").Property

	tx, err := e.pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := e.rawChange(tx, p.ID, "USD", 2); err != nil { // the update is in flight, not committed yet
		t.Fatalf("the update: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		wtx, err := e.pool.Begin(context.Background())
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = wtx.Rollback(context.Background()) }()
		if _, err := wtx.Exec(context.Background(), `INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES ($1, $2, 'J1', 'MANUAL', '2026-09-30', 'Opening')`, tn.ID, p.ID); err != nil {
			done <- err
			return
		}
		done <- nil // the row went in (it is rolled back: only the wait matters)
	}()
	select {
	case err := <-done:
		t.Fatalf("the first write must wait for the currency update in flight, it returned: %v", err)
	case <-time.After(600 * time.Millisecond):
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the write follows the update: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the write never finished after the update committed")
	}
	if code, dec := e.currencyOf(t, p.ID); code != "USD" || dec != 2 {
		t.Fatalf("the update stands: %s %d", code, dec)
	}
}
