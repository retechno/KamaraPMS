package tenancy_test

import (
	"context"
	"sort"
	"strings"
	"testing"
)

// Audit F-09: every table that property_has_financial_data looks at really locks the currency, through the service and through a plain UPDATE (the trigger), and
// configuration never does (docs/architecture/18-architecture-decisions.md, decision 3, migration 00058).
//
// Two layers. The definition test pins the list of tables in the function itself, so that a table added to or removed from it is a decision made in a test and not a drift.
// The behaviour tests insert one row of each table (with what the table cannot exist without) and require the refusal. The tables that are only possible with a journal
// (a supplier bill, a tax opening credit ...) are also locked by the journal they reference; for those the definition test is what proves the table itself is on the list.

// the tables the function reads, as migration 00058 defines them.
var financialRoots = []string{
	"bank_statements", "budgets", "cashier_shifts", "city_ledger_adjustments", "city_ledger_invoices", "city_ledger_receipts", "folio_items", "gl_journals",
	"payments", "supplier_bills", "supplier_credit_notes", "supplier_payments", "tax_opening_credits", "tax_payments", "tax_returns",
}

func TestTheDefinitionOfFinancialDataNamesExactlyTheseTables(t *testing.T) {
	e := setup(t)
	var def string
	if err := e.pool.QueryRow(context.Background(), `SELECT pg_get_functiondef('property_has_financial_data(bigint)'::regprocedure)`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, table := range allTables(t, e) {
		if strings.Contains(def, "FROM "+table+" ") || strings.Contains(def, "FROM public."+table+" ") {
			found = append(found, table)
		}
	}
	sort.Strings(found)
	want := append([]string(nil), financialRoots...)
	sort.Strings(want)
	if strings.Join(found, ",") != strings.Join(want, ",") {
		t.Fatalf("property_has_financial_data reads %v, the list of this test is %v: change both on purpose", found, want)
	}
}

func allTables(t *testing.T, e env) []string {
	t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		out = append(out, n)
	}
	return out
}

// journalIn writes a balanced journal inside the transaction and returns its id (the supplier, tax and city ledger documents point at one).
func journalIn(t *testing.T, tx sqler, tenantID, propertyID int64, number string) (journal, cash, capital int64) {
	t.Helper()
	cash = mustID(t, tx, `INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH') RETURNING id`, tenantID, propertyID)
	capital = mustID(t, tx, `INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, '3100', 'Capital', 'EQUITY', 'CREDIT', 'EQUITY') RETURNING id`, tenantID, propertyID)
	j := mustID(t, tx, `INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES ($1, $2, $3, 'MANUAL', '2026-09-30', 'Opening') RETURNING id`, tenantID, propertyID, number)
	mustExec(t, tx, `INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) VALUES ($1, $2, $3, 1, $4, 100, 0), ($1, $2, $3, 2, $5, 0, 100)`, tenantID, propertyID, j, cash, capital)
	return j, cash, capital
}

func TestEveryRootTableOfFinancialDataLocksTheCurrency(t *testing.T) {
	supplier := func(t *testing.T, tx sqler, tn, p int64) int64 {
		return mustID(t, tx, `INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES ($1, $2, 'SUP', 'Supplier') RETURNING id`, tn, p)
	}
	company := func(t *testing.T, tx sqler, tn, p int64) int64 {
		return mustID(t, tx, `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme', 1000000) RETURNING id`, tn, p)
	}
	tax := func(t *testing.T, tx sqler, tn, p int64) int64 {
		return mustID(t, tx, `INSERT INTO taxes (tenant_id, property_id, code, name, rate) VALUES ($1, $2, 'VAT', 'VAT', 11) RETURNING id`, tn, p)
	}
	bill := func(t *testing.T, tx sqler, tn, p, sup, j, account int64) int64 {
		id := mustID(t, tx, `INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id) VALUES ($1, $2, 'B1', $3, 'INV1', '2026-09-30', '2026-10-30', 100, $4) RETURNING id`, tn, p, sup, j)
		mustExec(t, tx, `INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) VALUES ($1, $2, $3, 1, $4, 100)`, tn, p, id, account)
		return id
	}
	invoice := func(t *testing.T, tx sqler, tn, p, co int64) int64 {
		return mustID(t, tx, `INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total) VALUES ($1, $2, 'CI1', $3, '2026-09-30', '2026-10-30', 100) RETURNING id`, tn, p, co)
	}
	taxReturn := func(t *testing.T, tx sqler, tn, p, taxID int64) int64 {
		id := mustID(t, tx, `INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on) VALUES ($1, $2, 'TR1', $3, '2026-09-01', '2026-09-30', '2026-10-20', 100, 11, '2026-10-01') RETURNING id`, tn, p, taxID)
		mustExec(t, tx, `INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount) VALUES ($1, $2, $3, 1, 'ROOM', 11, 1, 100, 11)`, tn, p, id)
		return id
	}
	cases := []struct {
		table string
		add   func(t *testing.T, tx sqler, tn, p int64)
	}{
		{"folio_items", func(t *testing.T, tx sqler, tn, p int64) {
			code := mustID(t, tx, `INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type) VALUES ($1, $2, 'LAUNDRY', 'Laundry', 'SERVICE') RETURNING id`, tn, p)
			res := mustID(t, tx, `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source) VALUES ($1, $2, 'R1', '2026-09-30', 'PHONE') RETURNING id`, tn, p)
			folio := mustID(t, tx, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'F1', $3) RETURNING id`, tn, p, res)
			mustExec(t, tx, `INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type, charge_code_id, description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
				VALUES ($1, $2, $3, '2026-09-30', '2026-09-30', 'CHARGE', $4, 'Laundry', 1, 50, 'EXCLUSIVE', 50, 50, 50, 'MANUAL')`, tn, p, folio, code)
		}},
		{"payments", func(t *testing.T, tx sqler, tn, p int64) {
			res := mustID(t, tx, `INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source) VALUES ($1, $2, 'R1', '2026-09-30', 'PHONE') RETURNING id`, tn, p)
			folio := mustID(t, tx, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'F1', $3) RETURNING id`, tn, p, res)
			mustExec(t, tx, `INSERT INTO payments (tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, business_date) VALUES ($1, $2, 'P1', $3, 'PAYMENT', 'CASH', 100, '2026-09-30')`, tn, p, folio)
		}},
		{"gl_journals", func(t *testing.T, tx sqler, tn, p int64) { _, _, _ = journalIn(t, tx, tn, p, "J1") }},
		{"supplier_bills", func(t *testing.T, tx sqler, tn, p int64) {
			j, cash, _ := journalIn(t, tx, tn, p, "J1")
			bill(t, tx, tn, p, supplier(t, tx, tn, p), j, cash)
		}},
		{"supplier_payments", func(t *testing.T, tx sqler, tn, p int64) {
			j, cash, _ := journalIn(t, tx, tn, p, "J1")
			sup := supplier(t, tx, tn, p)
			b := bill(t, tx, tn, p, sup, j, cash)
			pay := mustID(t, tx, `INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id) VALUES ($1, $2, 'SP1', $3, '2026-09-30', 100, 'CASH', $4) RETURNING id`, tn, p, sup, j)
			mustExec(t, tx, `INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount) VALUES ($1, $2, $3, $4, $5, 100)`, tn, p, sup, pay, b)
		}},
		{"supplier_credit_notes", func(t *testing.T, tx sqler, tn, p int64) {
			j, cash, _ := journalIn(t, tx, tn, p, "J1")
			sup := supplier(t, tx, tn, p)
			b := bill(t, tx, tn, p, sup, j, cash)
			cn := mustID(t, tx, `INSERT INTO supplier_credit_notes (tenant_id, property_id, credit_number, supplier_id, bill_id, supplier_credit_number, credit_date, reason, total, journal_id) VALUES ($1, $2, 'CN1', $3, $4, 'SCN1', '2026-09-30', 'returned', 10, $5) RETURNING id`, tn, p, sup, b, j)
			mustExec(t, tx, `INSERT INTO supplier_credit_note_lines (tenant_id, property_id, credit_id, line_no, bill_id, bill_line_no, account_id, amount) VALUES ($1, $2, $3, 1, $4, 1, $5, 10)`, tn, p, cn, b, cash)
		}},
		{"city_ledger_receipts", func(t *testing.T, tx sqler, tn, p int64) {
			mustExec(t, tx, `INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date) VALUES ($1, $2, 'CR1', $3, 100, 'CASH', '2026-09-30')`, tn, p, company(t, tx, tn, p))
		}},
		{"city_ledger_invoices", func(t *testing.T, tx sqler, tn, p int64) { invoice(t, tx, tn, p, company(t, tx, tn, p)) }},
		{"city_ledger_adjustments", func(t *testing.T, tx sqler, tn, p int64) {
			co := company(t, tx, tn, p)
			inv := invoice(t, tx, tn, p, co)
			j, _, capital := journalIn(t, tx, tn, p, "J1")
			mustExec(t, tx, `INSERT INTO city_ledger_adjustments (tenant_id, property_id, adjustment_number, kind, company_id, amount, business_date, reason, journal_id, invoice_id, debit_account_id) VALUES ($1, $2, 'CA1', 'WRITE_OFF', $3, 10, '2026-09-30', 'bad debt', $4, $5, $6)`,
				tn, p, co, j, inv, capital)
		}},
		{"cashier_shifts", func(t *testing.T, tx sqler, tn, p int64) {
			user := mustID(t, tx, `INSERT INTO users (tenant_id, email, password_hash, full_name) VALUES ($1, 'cashier@example.com', 'x', 'Cashier') RETURNING id`, tn)
			mustExec(t, tx, `INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, opened_at, business_date_opened) VALUES ($1, $2, 'S1', $3, now(), '2026-09-30')`, tn, p, user)
		}},
		{"bank_statements", func(t *testing.T, tx sqler, tn, p int64) {
			cash := mustID(t, tx, `INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, '1110', 'Cash', 'ASSET', 'DEBIT', 'CASH') RETURNING id`, tn, p)
			addBankStatement(t, tx, tn, p, cash)
		}},
		{"tax_returns", func(t *testing.T, tx sqler, tn, p int64) { taxReturn(t, tx, tn, p, tax(t, tx, tn, p)) }},
		{"tax_payments", func(t *testing.T, tx sqler, tn, p int64) {
			ret := taxReturn(t, tx, tn, p, tax(t, tx, tn, p))
			j, _, _ := journalIn(t, tx, tn, p, "J1")
			mustExec(t, tx, `INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, payment_method, journal_id) VALUES ($1, $2, 'TP1', $3, '2026-10-02', 11, 'CASH', $4)`, tn, p, ret, j)
		}},
		{"tax_opening_credits", func(t *testing.T, tx sqler, tn, p int64) {
			j, _, _ := journalIn(t, tx, tn, p, "J1")
			mustExec(t, tx, `INSERT INTO tax_opening_credits (tenant_id, property_id, tax_id, as_of, amount, journal_id) VALUES ($1, $2, $3, '2026-09-01', 50, $4)`, tn, p, tax(t, tx, tn, p), j)
		}},
		{"budgets", func(t *testing.T, tx sqler, tn, p int64) {
			mustExec(t, tx, `INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES ($1, $2, '2026-01-01', 1, 'Budget 2026')`, tn, p)
		}},
	}
	covered := map[string]bool{}
	for _, c := range cases {
		covered[c.table] = true
	}
	for _, table := range financialRoots {
		if !covered[table] {
			t.Fatalf("the table %s of the definition has no case in this test", table)
		}
	}
	for _, c := range cases {
		t.Run(c.table, func(t *testing.T) {
			e := setup(t)
			tn := e.tenant(t, "ABC")
			p := e.property(t, tn.ID, "BALI").Property
			other := e.property(t, tn.ID, "JKT").Property
			if err := e.changeCurrency(t, tn.ID, p.ID, "USD", 2); err != nil {
				t.Fatalf("before the row the currency changes: %v", err)
			}
			tx, err := e.pool.Begin(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background()) //nolint:errcheck
			c.add(t, tx, tn.ID, p.ID)
			if err := tx.Commit(context.Background()); err != nil {
				t.Fatalf("the row of %s: %v", c.table, err)
			}
			if mustID(t, e.pool, `SELECT CASE WHEN property_has_financial_data($1) THEN 1 ELSE 0 END`, p.ID) != 1 {
				t.Fatalf("a row in %s is financial data", c.table)
			}
			if n := mustID(t, e.pool, `SELECT count(*) FROM `+c.table+` WHERE property_id = $1`, p.ID); n < 1 {
				t.Fatalf("the case wrote no row in %s", c.table)
			}
			wantCode(t, e.changeCurrency(t, tn.ID, p.ID, "IDR", 0), "CURRENCY_LOCKED")
			if err := e.rawChange(e.pool, p.ID, "IDR", 0); !isRestrict(err) {
				t.Fatalf("the trigger must refuse after a row in %s: %v", c.table, err)
			}
			wantCode(t, e.changeCurrency(t, tn.ID, p.ID, "USD", 3), "CURRENCY_LOCKED") // the decimals alone
			if code, dec := e.currencyOf(t, p.ID); code != "USD" || dec != 2 {
				t.Fatalf("nothing changed: %s %d", code, dec)
			}
			// another property of the tenant stays free: the lock is per property
			if err := e.changeCurrency(t, tn.ID, other.ID, "SGD", 2); err != nil {
				t.Fatalf("the other property: %v", err)
			}
			if mustID(t, e.pool, `SELECT CASE WHEN property_has_financial_data($1) THEN 1 ELSE 0 END`, other.ID) != 0 {
				t.Fatal("the other property has no financial data")
			}
		})
	}
}

// Configuration never locks the currency: accounts, a bank account, companies, suppliers, taxes, charge codes, rate plans, a department, a rate grid.
func TestConfigurationDoesNotLockTheCurrency(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	p := e.property(t, tn.ID, "BALI").Property
	cash, _ := addAccounts(t, e.pool, tn.ID, p.ID)
	mustID(t, e.pool, `INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES ($1, $2, $3, 'Main bank') RETURNING id`, tn.ID, p.ID, cash)
	mustExec(t, e.pool, `INSERT INTO companies (tenant_id, property_id, code, name, credit_limit) VALUES ($1, $2, 'ACME', 'Acme', 1000000)`, tn.ID, p.ID)
	mustExec(t, e.pool, `INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES ($1, $2, 'SUP', 'Supplier')`, tn.ID, p.ID)
	mustExec(t, e.pool, `INSERT INTO taxes (tenant_id, property_id, code, name, rate) VALUES ($1, $2, 'VAT', 'VAT', 11)`, tn.ID, p.ID)
	mustExec(t, e.pool, `INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type) VALUES ($1, $2, 'LAUNDRY', 'Laundry', 'SERVICE')`, tn.ID, p.ID)
	if mustID(t, e.pool, `SELECT CASE WHEN property_has_financial_data($1) THEN 1 ELSE 0 END`, p.ID) != 0 {
		t.Fatal("configuration is not financial data")
	}
	if err := e.changeCurrency(t, tn.ID, p.ID, "KWD", 3); err != nil {
		t.Fatalf("a property with configuration only: %v", err)
	}
	if err := e.rawChange(e.pool, p.ID, "IDR", 0); err != nil {
		t.Fatalf("the trigger agrees: %v", err)
	}
}
