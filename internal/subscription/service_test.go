package subscription_test

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/subscription"
	"kamarapms/internal/tenancy"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

type env struct {
	txm  *db.TxManager
	c    *clock.Fake
	subs *subscription.Service
	ten  *tenancy.Service
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	txm := db.NewTxManager(pool, 5*time.Second)
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	aw := audit.NewWriter(c)
	subs := subscription.NewService(txm, c, aw)
	ten := tenancy.NewService(txm, c, aw, iam.NewAuthorizer(txm))
	ten.OnTenantCreated(subs.StartTenant)
	ten.SetQuotaGate(subs)
	return env{txm: txm, c: c, subs: subs, ten: ten}
}

func admin(tenantID int64) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{TenantID: tenantID, IsTenantAdmin: true})
}

func property(code string) tenancy.CreatePropertyInput {
	return tenancy.CreatePropertyInput{Code: code, OpeningBusinessDate: civil.MustParseDate("2026-09-30"), Settings: tenancy.PropertySettings{
		Name: "Hotel " + code, Timezone: "Asia/Jakarta", CurrencyCode: "IDR",
		CheckInTime: civil.MustParseTimeOfDay("14:00"), CheckOutTime: civil.MustParseTimeOfDay("12:00"),
		NightAuditEarliestTime: civil.MustParseTimeOfDay("20:00"), RefundMethods: []string{"CASH"},
	}}
}

func want(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.IsCode(err, code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}

// Two administrators create the last allowed property at the same moment: the subscription lock lets one through.
func TestPropertyQuotaUnderConcurrency(t *testing.T) {
	e := setup(t)
	tn, err := e.ten.CreateTenant(context.Background(), "ABC", "ABC Hotels", "Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.subs.Change(context.Background(), "ABC", subscription.ChangeInput{Limits: subscription.Limits{MaxProperties: new(1)}}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Go(func() {
			_, errs[i] = e.ten.CreateProperty(admin(tn.ID), property(string(rune('A'+i))+"HOTEL"))
		})
	}
	wg.Wait()
	ok, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case apperr.IsCode(err, "PLAN_LIMIT_REACHED"):
			refused++
		default:
			t.Errorf("unexpected: %v", err)
		}
	}
	if ok != 1 || refused != len(errs)-1 {
		t.Fatalf("one property within the limit: %d created, %d refused", ok, refused)
	}
	sum, err := e.subs.Summary(admin(tn.ID))
	if err != nil {
		t.Fatal(err)
	}
	if sum.Usage.Properties != 1 || *sum.Limits.MaxProperties != 1 {
		t.Fatalf("summary: %+v", sum)
	}
}

func TestPlansAndInvoiceRules(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	// The default plan moves, it is never dropped.
	pro, err := e.subs.CreatePlan(ctx, subscription.PlanInput{Code: "PRO", Name: "Pro", Price: decimal.RequireFromString("2500000"), CurrencyCode: "IDR",
		BillingPeriodMonths: 12, TrialDays: 14, IsActive: true, IsDefault: true})
	if err != nil || !pro.IsDefault {
		t.Fatalf("pro: %+v %v", pro, err)
	}
	trial, err := e.subs.PlanByCode(ctx, "trial")
	if err != nil || trial.IsDefault {
		t.Fatalf("TRIAL gave the flag up: %+v %v", trial, err)
	}
	in := subscription.PlanInput{Code: "PRO", Name: "Pro", Price: pro.Price, CurrencyCode: "IDR", BillingPeriodMonths: 12, TrialDays: 14, IsActive: true}
	_, err = e.subs.UpdatePlan(ctx, in)
	want(t, err, "DEFAULT_PLAN_REQUIRED")
	_, err = e.subs.CreatePlan(ctx, subscription.PlanInput{Code: "PRO", Name: "Again", CurrencyCode: "IDR", BillingPeriodMonths: 1, IsActive: true})
	want(t, err, "CODE_TAKEN")

	// A new tenant starts a trial of the default plan; a retired plan cannot be given.
	if _, err := e.ten.CreateTenant(ctx, "ABC", "ABC Hotels", "Asia/Jakarta"); err != nil {
		t.Fatal(err)
	}
	v, err := e.subs.Show(ctx, "ABC")
	if err != nil || v.Summary.PlanCode != "PRO" || v.Summary.Status != subscription.StatusTrial || v.Summary.PaidThrough.String() != "2026-10-13" {
		t.Fatalf("trial of the default: %+v %v", v.Summary, err)
	}
	_, err = e.subs.Change(ctx, "ABC", subscription.ChangeInput{PlanCode: new("LEGACY")})
	want(t, err, "PLAN_INACTIVE")
	_, err = e.subs.Change(ctx, "NOPE", subscription.ChangeInput{})
	want(t, err, "TENANT_NOT_FOUND")

	// A yearly invoice starts after the trial; voiding frees its period; a void or paid invoice is final in the database too.
	inv, err := e.subs.IssueInvoice(ctx, subscription.IssueInput{TenantCode: "ABC"})
	if err != nil || inv.PeriodStart.String() != "2026-10-14" || inv.PeriodEnd.String() != "2027-10-13" || !inv.Amount.Equal(decimal.RequireFromString("2500000")) {
		t.Fatalf("yearly invoice: %+v %v", inv, err)
	}
	if _, err := e.subs.VoidInvoice(ctx, inv.Number, "wrong price"); err != nil {
		t.Fatal(err)
	}
	_, err = e.subs.VoidInvoice(ctx, inv.Number, "again")
	want(t, err, "INVOICE_NOT_OPEN")
	amount := decimal.RequireFromString("2000000")
	again, err := e.subs.IssueInvoice(ctx, subscription.IssueInput{TenantCode: "ABC", Amount: &amount, Notes: "early bird"})
	if err != nil || again.Number != "SUB-2026-00002" || !again.Amount.Equal(amount) {
		t.Fatalf("after the void: %+v %v", again, err)
	}
	if _, err := e.txm.DB(ctx).Exec(ctx, `UPDATE subscription_invoices SET amount = 1 WHERE number = $1`, inv.Number); !apperr.IsCode(db.MapError(err), "INVOICE_FINAL") {
		t.Fatalf("a void invoice is final: %v", err)
	}
	if _, err := e.txm.DB(ctx).Exec(ctx, `DELETE FROM subscription_invoices WHERE number = $1`, again.Number); !apperr.IsCode(db.MapError(err), "INVOICE_FINAL") {
		t.Fatalf("no invoice is deleted: %v", err)
	}
	_, sub, err := e.subs.PayInvoice(ctx, again.Number, civil.MustParseDate("2026-10-01"), "")
	if err != nil || sub.Status != subscription.StatusActive || sub.PaidThrough.String() != "2027-10-13" {
		t.Fatalf("paid: %+v %v", sub, err)
	}
}
