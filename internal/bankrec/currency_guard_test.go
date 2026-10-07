package bankrec_test

import (
	"strings"
	"testing"

	"kamarapms/internal/bankrec"
	"kamarapms/internal/rooms/roomstest"
)

// The property is IDR with no decimals. A statement is in the currency of the property or it is refused, and nothing is converted
// (docs/architecture/18-architecture-decisions.md section 5.7 rule 7).
func TestAStatementInAnotherCurrencyIsRefused(t *testing.T) {
	f := setup(t)
	in := func(csv, currency string) bankrec.ImportInput {
		return bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"), OpeningBalance: dec("0"), ClosingBalance: dec("1000000"), CSV: csv, Currency: currency}
	}
	const csv = "date,description,amount\n2026-09-30,Transfer,1000000\n"

	// the request names another currency
	_, err := f.BankRec.ImportStatement(f.admin, f.propID, in(csv, "USD"))
	e := roomstest.Code(t, err, "STATEMENT_CURRENCY_MISMATCH")
	if e.Context["property_currency"] != "IDR" || e.Context["statement_currency"] != "USD" {
		t.Fatalf("context: %v", e.Context)
	}
	// a row of the currency column does
	_, err = f.BankRec.ImportStatement(f.admin, f.propID, in("date,description,amount,currency\n2026-09-30,Transfer,500000,IDR\n2026-09-30,Transfer,500000,SGD\n", ""))
	e = roomstest.Code(t, err, "STATEMENT_CURRENCY_MISMATCH")
	if e.Context["row"] != 3 || e.Context["statement_currency"] != "SGD" {
		t.Fatalf("row context: %v", e.Context)
	}
	if f.Count(t, `SELECT count(*) FROM bank_statements`) != 0 {
		t.Fatal("a refused statement imports nothing")
	}

	// the currency of the property, in any case, and blank cells, are fine
	st, err := f.BankRec.ImportStatement(f.admin, f.propID, in("date,description,amount,Currency\n2026-09-30,Transfer,500000,idr\n2026-09-30,Transfer,500000,\n", " idr "))
	must(t, err)
	if len(st.Lines) != 2 {
		t.Fatalf("lines: %d", len(st.Lines))
	}
}

// IDR has no decimals: an amount or a balance with cents is not an amount of this currency.
func TestAStatementHasTheDecimalsOfThePropertyCurrency(t *testing.T) {
	f := setup(t)
	_, err := f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"),
		OpeningBalance: dec("0"), ClosingBalance: dec("1000000.50"), CSV: "date,description,amount\n2026-09-30,Transfer,1000000.50\n"})
	ae := roomstest.Code(t, err, "VALIDATION_FAILED")
	var fields []string
	for _, fe := range ae.Fields {
		fields = append(fields, fe.Field+":"+fe.Code)
	}
	got := strings.Join(fields, ",")
	if !strings.Contains(got, "rows[2].amount:TOO_PRECISE") {
		t.Fatalf("fields: %s", got)
	}
	// the balances too, when the lines are fine
	_, err = f.BankRec.ImportStatement(f.admin, f.propID, bankrec.ImportInput{BankAccountID: f.bank.ID, PeriodFrom: d("2026-09-01"), PeriodTo: d("2026-09-30"),
		OpeningBalance: dec("0.5"), ClosingBalance: dec("1000000.5"), CSV: "date,description,amount\n2026-09-30,Transfer,1000000\n"})
	ae = roomstest.Code(t, err, "VALIDATION_FAILED")
	if len(ae.Fields) != 2 || ae.Fields[0].Code != "TOO_PRECISE" {
		t.Fatalf("balance fields: %+v", ae.Fields)
	}
}
