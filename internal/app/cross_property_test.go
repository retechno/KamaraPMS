package app

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"testing"

	"kamarapms/internal/platform/dbtest"
)

// Audit F-10: an object of one property is not reachable through another property of the same tenant, in the modules where money lives.
//
// The tenant administrator has every permission on both properties, so the only thing that can refuse is the scoping by property. Property B is created first, so its objects have the id 1
// of their kind; property A is created next and is asked for them. A control proves the test is not vacuous: the same read through B's own path finds the object.

func seedFinancialObjects(t *testing.T, w world) {
	t.Helper()
	ctx := context.Background()
	pool := dbtest.Pool(t)
	id := func(sql string, args ...any) int64 {
		t.Helper()
		var v int64
		if err := pool.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
		return v
	}
	prop := mustInt(w.prop)
	var tenant int64
	if err := pool.QueryRow(ctx, `SELECT tenant_id FROM properties WHERE id = $1`, prop).Scan(&tenant); err != nil {
		t.Fatal(err)
	}
	account := func(code, name, typ, side, group string) int64 {
		var v int64
		if err := pool.QueryRow(ctx, `SELECT id FROM gl_accounts WHERE property_id = $1 AND code = $2`, prop, code).Scan(&v); err == nil {
			return v
		}
		return id(`INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, statement_group) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`, tenant, prop, code, name, typ, side, group)
	}
	cash := account("1110", "Cash", "ASSET", "DEBIT", "CASH")
	capital := account("3100", "Capital", "EQUITY", "CREDIT", "EQUITY")

	tx, err := pool.Begin(ctx) // journals are checked as a whole at commit
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	run := func(sql string, args ...any) int64 {
		t.Helper()
		var v int64
		if strings.Contains(sql, "RETURNING id") {
			if err := tx.QueryRow(ctx, sql, args...).Scan(&v); err != nil {
				t.Fatalf("%v\n%s", err, sql)
			}
			return v
		}
		if _, err := tx.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%v\n%s", err, sql)
		}
		return 0
	}
	journal := run(`INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description) VALUES ($1, $2, 'JX1', 'MANUAL', '2026-09-30', 'Fixture') RETURNING id`, tenant, prop)
	run(`INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit) VALUES ($1, $2, $3, 1, $4, 100, 0), ($1, $2, $3, 2, $5, 0, 100)`, tenant, prop, journal, cash, capital)
	company := mustInt(w.company)
	run(`INSERT INTO city_ledger_receipts (tenant_id, property_id, receipt_number, company_id, amount, payment_method, business_date) VALUES ($1, $2, 'CRX1', $3, 100, 'CASH', '2026-09-30')`, tenant, prop, company)
	run(`INSERT INTO city_ledger_invoices (tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total) VALUES ($1, $2, 'CIX1', $3, '2026-09-30', '2026-10-30', 100)`, tenant, prop, company)
	supplier := run(`INSERT INTO suppliers (tenant_id, property_id, code, name) VALUES ($1, $2, 'SUPX', 'Supplier') RETURNING id`, tenant, prop)
	bill := run(`INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, total, journal_id) VALUES ($1, $2, 'BX1', $3, 'INVX', '2026-09-30', '2026-10-30', 100, $4) RETURNING id`, tenant, prop, supplier, journal)
	run(`INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, amount) VALUES ($1, $2, $3, 1, $4, 100)`, tenant, prop, bill, cash)
	pay := run(`INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, journal_id) VALUES ($1, $2, 'SPX1', $3, '2026-09-30', 100, 'CASH', $4) RETURNING id`, tenant, prop, supplier, journal)
	run(`INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount) VALUES ($1, $2, $3, $4, $5, 100)`, tenant, prop, supplier, pay, bill)
	bank := run(`INSERT INTO bank_accounts (tenant_id, property_id, account_id, name) VALUES ($1, $2, $3, 'Main bank') RETURNING id`, tenant, prop, cash)
	run(`INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance) VALUES ($1, $2, $3, '2026-09-01', '2026-09-30', 0, 0)`, tenant, prop, bank)
	run(`INSERT INTO budgets (tenant_id, property_id, year_start, version, name) VALUES ($1, $2, '2026-01-01', 1, 'Budget 2026')`, tenant, prop)
	taxID := run(`INSERT INTO taxes (tenant_id, property_id, code, name, rate) VALUES ($1, $2, 'VATX', 'VAT', 11) RETURNING id`, tenant, prop)
	ret := run(`INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on) VALUES ($1, $2, 'TRX1', $3, '2026-09-01', '2026-09-30', '2026-10-20', 100, 11, '2026-10-01') RETURNING id`, tenant, prop, taxID)
	run(`INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, rate, items, base_amount, tax_amount) VALUES ($1, $2, $3, 1, 'ROOM', 11, 1, 100, 11)`, tenant, prop, ret)
	run(`INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, payment_method, journal_id) VALUES ($1, $2, 'TPX1', $3, '2026-09-30', 11, 'CASH', $4)`, tenant, prop, ret, journal)
	cn := run(`INSERT INTO supplier_credit_notes (tenant_id, property_id, credit_number, supplier_id, bill_id, supplier_credit_number, credit_date, reason, total, journal_id) VALUES ($1, $2, 'CNX1', $3, $4, 'SCNX1', '2026-09-30', 'returned', 10, $5) RETURNING id`, tenant, prop, supplier, bill, journal)
	run(`INSERT INTO supplier_credit_note_lines (tenant_id, property_id, credit_id, line_no, bill_id, bill_line_no, account_id, amount) VALUES ($1, $2, $3, 1, $4, 1, $5, 10)`, tenant, prop, cn, bill, cash)
	run(`INSERT INTO city_ledger_adjustments (tenant_id, property_id, adjustment_number, kind, company_id, amount, business_date, reason, journal_id, invoice_id, debit_account_id) VALUES ($1, $2, 'CAX1', 'WRITE_OFF', $3, 10, '2026-09-30', 'bad debt', $4, 1, $5)`, tenant, prop, company, journal, capital)
	run(`INSERT INTO city_ledger_reminders (tenant_id, property_id, reminder_number, company_id, level, reminder_date, total_outstanding) VALUES ($1, $2, 'RMX1', $3, 1, '2026-09-30', 100)`, tenant, prop, company)
	ti := run(`INSERT INTO tax_invoices (tenant_id, property_id, invoice_ref, issue_date, source_type, folio_id, seller_name, seller_npwp, buyer_name, buyer_npwp, taxable_base, vat_amount) VALUES ($1, $2, 'TIX1', '2026-09-30', 'FOLIO', 1, 'Seller', '000000000000000', 'Buyer', '000000000000000', 100, 11) RETURNING id`, tenant, prop)
	run(`INSERT INTO tax_invoice_lines (tenant_id, property_id, invoice_id, line_no, charge_code, description, base_amount, rate, vat_amount) VALUES ($1, $2, $3, 1, 'ROOM', 'Room', 100, 11, 11)`, tenant, prop, ti)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// objectGets are the GET routes that address one object.
func objectGets(t *testing.T) []routeSpec {
	var out []routeSpec
	for _, r := range serverRoutes(t) {
		if r.method == http.MethodGet && strings.Contains(r.path, "{propertyId}") && hasObjectParam(r) {
			out = append(out, r)
		}
	}
	return out
}

func hasObjectParam(r routeSpec) bool {
	for _, m := range namedParam.FindAllStringSubmatch(r.path, -1) {
		if m[1] != "propertyId" && m[1] != "start" && m[1] != "kind" {
			return true
		}
	}
	return false
}

// financialPrefixes: for each of these modules at least one read of an object must be non-vacuous (the object exists in the other property), or the test says the module is not covered.
var financialPrefixes = []string{"/folios/", "/payments/", "/city-ledger/", "/payables/", "/bank/", "/cashier/shifts/", "/accounting/journals/", "/tax/returns/", "/reservations/", "/stays/", "/budgets/"}

func TestObjectReadsDoNotCrossToAnotherPropertyOfTheTenant(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	b := buildWorld(t, abc, "BALI") // first: its objects have the ids 1
	if r := abc.do(http.MethodPost, b.base+"/cashier/shifts", map[string]any{"drawer": "MAIN", "opening_float": "0"}); r.status != 201 {
		t.Fatalf("shift: %d %v", r.status, r.body)
	}
	seedFinancialObjects(t, b)
	a := buildWorld(t, abc, "JKT") // then the property that is asked for them
	_ = a

	covered := map[string]bool{}
	var leaks, vacuous []string
	for _, r := range objectGets(t) {
		control := b.request(abc, r, nil)
		cross := a.request(abc, r, nil)
		if control.status == http.StatusOK {
			for _, p := range financialPrefixes {
				if strings.Contains(r.path, p) {
					covered[p] = true
				}
			}
			if cross.status == http.StatusOK && emptyListOfAnObject[r.key()] != "" && listIsEmpty(cross.body) {
				continue
			}
			if cross.status != http.StatusNotFound {
				leaks = append(leaks, fmt.Sprintf("%s: through its own property %d, through another property of the tenant %d %v", r.key(), control.status, cross.status, cross.body["code"]))
			}
			continue
		}
		vacuous = append(vacuous, fmt.Sprintf("%s (%d)", r.key(), control.status))
	}
	if len(leaks) > 0 {
		t.Errorf("objects reachable through another property:\n  %s", strings.Join(leaks, "\n  "))
	}
	for _, p := range financialPrefixes {
		if !covered[p] {
			t.Errorf("no read of an object under %s found its object in the other property: the module is not covered by this test", p)
		}
	}
	sort.Strings(vacuous)
	t.Logf("%d object reads proved; %d without an object in the fixture:\n  %s", len(objectGets(t))-len(vacuous), len(vacuous), strings.Join(vacuous, "\n  "))
}

// emptyListOfAnObject are the object reads that answer a list: for a reservation of another property the list is empty and not a 404. Nothing of the other property is shown.
var emptyListOfAnObject = map[string]string{
	"GET /api/v1/properties/{propertyId}/reservations/{id}/emails": "the outbox of the reservation, filtered by tenant and property: empty for an id that is not of this property",
}

func listIsEmpty(body map[string]any) bool {
	data, ok := body["data"].([]any)
	return ok && len(data) == 0
}

// financialWrites are the changes of the money modules with a body that gets past validation, so that only the scoping by property can refuse them.
func financialWrites(w world) []call {
	approval := map[string]any{"email": "admin@hotel.com", "password": testPassword}
	b := w.base
	return []call{
		{"folio charge", http.MethodPost, b + "/folios/" + w.guestFolio + "/charges", map[string]any{"charge_code_id": mustInt(w.minibar), "quantity": "1", "unit_price": "1000"}, true},
		{"folio payment", http.MethodPost, b + "/folios/" + w.guestFolio + "/payments", map[string]any{"amount": "1000", "payment_method": "CASH"}, true},
		{"folio adjustment", http.MethodPost, b + "/folios/" + w.guestFolio + "/adjustments", map[string]any{"charge_code_id": mustInt(w.minibar), "amount": "-1000", "reason": "x", "approval": approval}, true},
		{"folio city ledger transfer", http.MethodPost, b + "/folios/" + w.guestFolio + "/city-ledger-transfers", map[string]any{"company_id": mustInt(w.company), "amount": "1000"}, true},
		{"folio close", http.MethodPost, b + "/folios/" + w.guestFolio + "/close", map[string]any{"version": 1}, false},
		{"item transfer", http.MethodPost, b + "/folio-items/" + w.item + "/transfer", map[string]any{"folio_id": mustInt(w.companyFolio), "reason": "company pays", "approval": approval}, false},
		{"item reverse", http.MethodPost, b + "/folio-items/" + w.item + "/reverse", map[string]any{"reason": "typo", "approval": approval}, false},
		{"item group", http.MethodPatch, b + "/folio-items/" + w.item + "/group", map[string]any{"group_code": "B"}, false},
		{"payment group", http.MethodPatch, b + "/payments/" + w.payment + "/group", map[string]any{"group_code": "B"}, false},
		{"payment void", http.MethodPost, b + "/payments/" + w.payment + "/void", map[string]any{"reason": "typo", "approval": approval}, false},
		{"payment refund", http.MethodPost, b + "/payments/" + w.payment + "/refunds", map[string]any{"amount": "1000", "reason": "x", "approval": approval}, true},
		{"deposit", http.MethodPost, b + "/reservations/" + w.res + "/deposits", map[string]any{"amount": "1000", "payment_method": "CASH"}, true},
		{"reservation fee", http.MethodPost, b + "/reservations/" + w.res + "/fees", map[string]any{"type": "CANCEL_FEE", "amount": "1000", "reason": "x"}, false},
		{"shift movement", http.MethodPost, b + "/cashier/shifts/1/movements", map[string]any{"kind": "DROP", "amount": "1000", "reason": "x"}, true},
		{"shift close", http.MethodPost, b + "/cashier/shifts/1/close", map[string]any{"counted_cash": "0"}, false},
		{"city ledger receipt void", http.MethodPost, b + "/city-ledger/receipts/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"city ledger invoice void", http.MethodPost, b + "/city-ledger/invoices/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"city ledger adjustment void", http.MethodPost, b + "/city-ledger/adjustments/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"supplier bill void", http.MethodPost, b + "/payables/bills/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"supplier payment void", http.MethodPost, b + "/payables/payments/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"supplier credit note void", http.MethodPost, b + "/payables/credit-notes/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"journal reverse", http.MethodPost, b + "/accounting/journals/1/reverse", map[string]any{"reason": "x", "approval": approval}, false},
		{"bank statement reopen", http.MethodPost, b + "/bank/statements/1/reopen", map[string]any{"reason": "x", "approval": approval}, false},
		{"bank statement reconcile", http.MethodPost, b + "/bank/statements/1/reconcile", map[string]any{}, false},
		{"tax return void", http.MethodPost, b + "/tax/returns/1/void", map[string]any{"reason": "x", "approval": approval}, false},
		{"tax invoice void", http.MethodPost, b + "/tax/invoices/1/void", map[string]any{"reason": "x", "approval": approval}, false},
	}
}

// What is written to a property is counted by its audit entries and by the rows of the ledger: neither moves when the call is refused.
func propertyFingerprint(t *testing.T, propertyID string) string {
	t.Helper()
	pool := dbtest.Pool(t)
	var out []string
	for _, table := range []string{"audit_logs", "folio_items", "payments", "folios", "cashier_shift_movements", "city_ledger_receipts", "city_ledger_invoices", "city_ledger_adjustments", "supplier_bills", "supplier_payments", "supplier_credit_notes", "gl_journals", "bank_statements", "tax_returns", "tax_invoices"} {
		var n int
		col := "property_id"
		if table == "audit_logs" {
			col = "property_id"
		}
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE `+col+` = $1`, mustInt(propertyID)).Scan(&n); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		out = append(out, fmt.Sprintf("%s=%d", table, n))
	}
	var status string
	_ = pool.QueryRow(context.Background(), `SELECT string_agg(status, ',' ORDER BY id) FROM payments WHERE property_id = $1`, mustInt(propertyID)).Scan(&status)
	return strings.Join(out, " ") + " payments:" + status
}

func TestMoneyWritesDoNotCrossToAnotherPropertyOfTheTenant(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	b := buildWorld(t, abc, "BALI")
	if r := abc.do(http.MethodPost, b.base+"/cashier/shifts", map[string]any{"drawer": "MAIN", "opening_float": "0"}); r.status != 201 {
		t.Fatalf("shift: %d %v", r.status, r.body)
	}
	seedFinancialObjects(t, b)
	a := buildWorld(t, abc, "JKT")
	// the control: property B really holds an object of each kind that is asked for, with the id 1 (a 404 would otherwise prove nothing)
	for _, table := range []string{"folios", "folio_items", "payments", "reservations", "cashier_shifts", "city_ledger_receipts", "city_ledger_invoices", "city_ledger_adjustments", "supplier_bills", "supplier_payments", "supplier_credit_notes", "gl_journals", "bank_statements", "tax_returns", "tax_invoices"} {
		if n := e.countWhere(t, table, "id = 1 AND property_id = "+b.prop); n != 1 {
			t.Fatalf("property B has no %s with the id 1", table)
		}
	}
	before := propertyFingerprint(t, b.prop)
	aBefore := propertyFingerprint(t, a.prop)

	ghost := b // B's objects under A's path
	ghost.base, ghost.prop = a.base, a.prop
	for _, cl := range financialWrites(ghost) {
		r := cl.run(abc, "x-"+cl.name)
		code, _ := r.body["code"].(string)
		if r.status != http.StatusNotFound || !strings.HasSuffix(code, "_NOT_FOUND") {
			t.Errorf("%s with an object of another property: %d %v, want 404 *_NOT_FOUND", cl.name, r.status, code)
		}
	}
	if got := propertyFingerprint(t, b.prop); got != before {
		t.Errorf("property B changed: %s then %s", before, got)
	}
	if got := propertyFingerprint(t, a.prop); got != aBefore {
		t.Errorf("property A changed: %s then %s", aBefore, got)
	}
}

func (e *apiEnv) countWhere(t *testing.T, table, where string) int {
	t.Helper()
	var n int
	if err := dbtest.Pool(t).QueryRow(context.Background(), `SELECT count(*) FROM `+table+` WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
