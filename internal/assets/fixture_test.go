package assets_test

import (
	"context"
	"os"
	"sync/atomic"
	"testing"

	"github.com/shopspring/decimal"

	"kamarapms/internal/assets"
	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func ptr[T any](v T) *T            { return &v }
func d(s string) civil.Date        { return civil.MustParseDate(s) }

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

// fx is a property (IDR, no decimals) with the register wired: the seven default categories are there from the seed.
type fx struct {
	*roomstest.Env
	svc              *assets.Service
	tenantID, propID int64
	admin            context.Context
	boss             context.Context // a tenant administrator with a password, for the approvals
	bossEmail        string
	accounts         map[string]int64
}

func newFx(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.PropertyIn(t, tn.ID, "BALI", "IDR", 0)
	f := &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: roomstest.Admin(tn.ID), accounts: map[string]int64{}}
	f.svc = assets.NewService(e.TxM, e.Clock, audit.NewWriter(e.Clock), iam.NewAuthorizer(e.TxM), e.Tenancy, e.Accounting, e.Departments)
	f.svc.SetApprover(e.IAM) // the approval of a disposal
	e.Accounting.SetAssetSource(f.svc)
	e.Accounting.SetJournalGuard(f.svc) // like internal/app: a journal or a bill with a line made an asset is not undone
	e.Accounting.SetPeriodGuard(f.svc)  // and a month closes only when its depreciation is run
	e.Payables.SetAssetGuard(f.svc)
	f.boss, f.bossEmail = e.AdminAccount(t, tn.ID)
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
	return f
}

// user is a person with the given permissions at the property.
func (f *fx) user(t *testing.T, perms ...auth.Permission) context.Context {
	t.Helper()
	return f.User(t, f.tenantID, f.propID, perms...)
}

// row is a row of an opening file with the usual defaults of the tests.
func row(name, cat, cost, accum string, months int) assets.OpeningRow {
	r := assets.OpeningRow{Name: name, Category: cat, Cost: dec(cost), InServiceDate: ptr(d("2024-01-15"))}
	if accum != "" {
		r.AccumulatedDepreciation = ptr(dec(accum))
	}
	if months > 0 {
		r.MonthsDone = ptr(months)
	}
	return r
}

var keyCounter atomic.Int64

func nextKey() string {
	n := int(keyCounter.Add(1))
	return "k" + string(rune('a'+n%26)) + string(rune('0'+n/26%10)) + string(rune('0'+n/260%10))
}

func (f *fx) journalLines(t *testing.T, journalID int64) map[string]decimal.Decimal {
	t.Helper()
	rows, err := f.Pool.Query(context.Background(), `SELECT a.code, l.debit - l.credit FROM gl_journal_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id WHERE l.journal_id = $1`, journalID)
	must(t, err)
	defer rows.Close()
	out := map[string]decimal.Decimal{}
	for rows.Next() {
		var c string
		var v decimal.Decimal
		must(t, rows.Scan(&c, &v))
		out[c] = out[c].Add(v)
	}
	must(t, rows.Err())
	return out
}
