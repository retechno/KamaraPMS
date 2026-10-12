package subscription

import (
	"testing"

	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/entitlement"
)

func d(s string) civil.Date { return civil.MustParseDate(s) }

func dp(s string) *civil.Date {
	x := d(s)
	return &x
}

func TestAccessState(t *testing.T) {
	sub := Subscription{Status: StatusActive, PaidThrough: dp("2026-10-31"), GraceDays: 14}
	for _, c := range []struct {
		today string
		want  AccessState
	}{
		{"2026-10-01", AccessFull}, {"2026-10-31", AccessFull}, // the last paid day is still paid
		{"2026-11-01", AccessGrace}, {"2026-11-14", AccessGrace}, // grace_days days after it
		{"2026-11-15", AccessReadOnly},
	} {
		if got := sub.Access(d(c.today)); got != c.want {
			t.Errorf("%s: %s, want %s", c.today, got, c.want)
		}
	}
	if got := (Subscription{Status: StatusActive}).Access(d("2099-01-01")); got != AccessFull {
		t.Errorf("no end: %s", got)
	}
	cancelled := Subscription{Status: StatusCancelled, PaidThrough: dp("2026-10-31"), GraceDays: 14}
	if cancelled.Access(d("2026-10-31")) != AccessFull || cancelled.Access(d("2026-11-01")) != AccessReadOnly || cancelled.GraceEndsOn() != nil {
		t.Error("a cancelled subscription ends at paid_through, without grace")
	}
	trial := Subscription{Status: StatusTrial, PaidThrough: dp("2026-10-31"), GraceDays: 0}
	if trial.Access(d("2026-11-01")) != AccessReadOnly {
		t.Error("no grace days: read only the day after")
	}
}

func TestDaysLeft(t *testing.T) {
	sub := Subscription{Status: StatusActive, PaidThrough: dp("2026-10-31"), GraceDays: 14}
	for today, want := range map[string]int{"2026-10-31": 1, "2026-10-02": 30, "2026-11-01": 14, "2026-11-14": 1, "2026-12-01": 0} {
		if got := daysLeft(sub, d(today)); got == nil || *got != want {
			t.Errorf("%s: %v, want %d", today, got, want)
		}
	}
	if daysLeft(Subscription{Status: StatusActive}, d("2026-10-01")) != nil {
		t.Error("no end: nil")
	}
}

func TestAddMonthsAndNextPeriod(t *testing.T) {
	for in, want := range map[[2]any]string{
		{"2026-01-31", 1}: "2026-02-28", {"2028-01-31", 1}: "2028-02-29", {"2026-11-20", 1}: "2026-12-20",
		{"2026-11-20", 2}: "2027-01-20", {"2026-12-15", 12}: "2027-12-15", {"2026-03-31", -1}: "2026-02-28",
	} {
		if got := AddMonths(d(in[0].(string)), in[1].(int)).String(); got != want {
			t.Errorf("%v: %s, want %s", in, got, want)
		}
	}
	paid := Subscription{PaidThrough: dp("2026-12-19")}
	if s, e := NextPeriod(paid, d("2026-11-20"), 1, 2); s.String() != "2026-12-20" || e.String() != "2027-02-19" {
		t.Errorf("after the paid period: %s..%s", s, e)
	}
	if s, e := NextPeriod(paid, d("2027-01-05"), 3, 1); s.String() != "2027-01-05" || e.String() != "2027-04-04" {
		t.Errorf("lapsed: from today: %s..%s", s, e)
	}
	if s, _ := NextPeriod(Subscription{}, d("2026-10-12"), 1, 1); s.String() != "2026-10-12" {
		t.Errorf("no end: from today: %s", s)
	}
}

func TestLimitsAndPlanInput(t *testing.T) {
	two, five := 2, 5
	got := Limits{MaxRooms: &five}.Over(Limits{MaxRooms: &two, MaxUsers: &two})
	if *got.Of(entitlement.Rooms) != 5 || *got.Of(entitlement.Users) != 2 || got.Of(entitlement.Properties) != nil {
		t.Errorf("an override wins, the plan fills the rest: %+v", got)
	}
	in := PlanInput{Code: " pro ", Name: "Pro", Modules: []Module{"accounting", "CORE", "ACCOUNTING", "housekeeping"}, CurrencyCode: "idr", BillingPeriodMonths: 1, IsActive: true}
	in.Normalize()
	if in.Code != "PRO" || len(in.Modules) != 2 || in.Modules[0] != Accounting || in.Modules[1] != Housekeeping || in.CurrencyCode != "IDR" {
		t.Fatalf("normalize: %+v", in)
	}
	if f := in.Validate(); len(f) != 0 {
		t.Fatalf("valid: %v", f)
	}
	bad := PlanInput{Code: "x", Modules: []Module{"SPA"}, Limits: Limits{MaxRooms: new(0)}, CurrencyCode: "rupiah", BillingPeriodMonths: 2, TrialDays: 400, IsDefault: true}
	fields := map[string]bool{}
	for _, f := range bad.Validate() {
		fields[f.Field] = true
	}
	for _, want := range []string{"code", "name", "modules", "max_rooms", "currency_code", "billing_period_months", "trial_days", "is_default"} {
		if !fields[want] {
			t.Errorf("%s should be refused: %v", want, fields)
		}
	}
	if InvoiceNumber(2026, 7) != "SUB-2026-00007" {
		t.Error("invoice number format")
	}
}

func TestModuleOf(t *testing.T) {
	for pattern, want := range map[string]Module{
		"GET /api/v1/properties/{propertyId}/taxes":              Core, // not "tax"
		"GET /api/v1/properties/{propertyId}/tax/returns":        Accounting,
		"GET /api/v1/properties/{propertyId}/budgets/{id}":       Accounting,
		"POST /api/v1/properties/{propertyId}/housekeeping/bulk": Housekeeping,
		"GET /api/v1/properties/{propertyId}":                    Core,
		"GET /api/v1/guests/{id}/history":                        Core,
		"/api/v1/subscription/invoices":                          Core,
	} {
		if got, ok := ModuleOf(pattern); !ok || got != want {
			t.Errorf("%s: %q %v, want %q", pattern, got, ok, want)
		}
	}
	for _, p := range []string{"/api/", "", "GET /api/v1/properties/{propertyId}/spa"} {
		if _, ok := ModuleOf(p); ok {
			t.Errorf("%q should not be classified", p)
		}
	}
}
