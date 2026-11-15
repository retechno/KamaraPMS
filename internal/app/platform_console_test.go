package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/operator"
	"kamarapms/internal/platform/dbtest"
)

// op is a signed-in operator of the console.
type op struct {
	e     *apiEnv
	token string
}

// newOperator creates an operator, enrols its authenticator and signs it in (through the service: the HTTP flow has its own test).
func newOperator(t *testing.T, e *apiEnv, email string) *op {
	t.Helper()
	ctx := context.Background()
	svc := consoleService(t, e)
	if _, err := svc.CreateOperator(ctx, email, "Op "+email, testPassword); err != nil {
		t.Fatal(err)
	}
	ch, err := svc.Login(ctx, operator.LoginInput{Email: email, Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	st, err := svc.EnrollStart(ctx, ch.Token)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := svc.EnrollConfirm(ctx, operator.EnrollConfirmInput{Challenge: ch.Token, Code: rawCode(decodeSecret(t, st.Secret), e.clock.Now())})
	if err != nil {
		t.Fatal(err)
	}
	return &op{e: e, token: sess.AccessToken}
}

func (o *op) do(method, path string, body any) response {
	rec, out := platformCall(o.e, method, "/api/v1/platform"+path, o.token, body)
	return response{rec.Code, out, rec.Result().Cookies()}
}

func (o *op) want(t *testing.T, status int, method, path string, body any) response {
	t.Helper()
	r := o.do(method, path, body)
	if r.status != status {
		t.Fatalf("%s %s: %d %v, want %d", method, path, r.status, r.body, status)
	}
	return r
}

func items(r response) []map[string]any {
	var out []map[string]any
	for _, v := range r.body["items"].([]any) {
		out = append(out, v.(map[string]any))
	}
	return out
}

func codes(r response) []string {
	var out []string
	for _, it := range items(r) {
		out = append(out, it["code"].(string))
	}
	return out
}

func countWhere(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := dbtest.Pool(t).QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestConsoleTenantList(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "list@example.test")
	// ABC: its paid period and its grace are over (read only). XYZ: past its paid period, in grace. DEF: a new tenant, on trial.
	o.want(t, 200, http.MethodPatch, "/tenants/ABC/subscription", map[string]any{"status": "ACTIVE", "paid_through": "2026-10-05", "grace_days": 3})
	o.want(t, 200, http.MethodPatch, "/tenants/XYZ/subscription", map[string]any{"status": "ACTIVE", "paid_through": "2026-10-10", "grace_days": 7})
	e.clock.Advance(15 * 24 * time.Hour) // 15 October: ABC's grace ended on the 8th, XYZ's ends on the 17th
	o = newOperator(t, e, "list2@example.test")
	o.want(t, 201, http.MethodPost, "/tenants", map[string]any{"code": "DEF", "name": "Alpha Hotel", "timezone": "Asia/Jakarta"})

	r := o.want(t, http.StatusOK, http.MethodGet, "/tenants", nil)
	if got := strings.Join(codes(r), ","); got != "ABC,XYZ,DEF" {
		t.Fatalf("the ones that need attention come first: %s", got)
	}
	states := map[string]string{}
	for _, it := range items(r) {
		states[it["code"].(string)] = it["state"].(string)
	}
	if states["ABC"] != "read_only" || states["XYZ"] != "grace" || states["DEF"] != "trial" {
		t.Fatalf("states: %v", states)
	}
	sum := r.body["summary"].(map[string]any)
	if sum["read_only"] != 1.0 || sum["grace"] != 1.0 || sum["trial"] != 1.0 || sum["active"] != 0.0 || sum["suspended"] != 0.0 {
		t.Fatalf("summary: %v", sum)
	}

	for q, want := range map[string]string{"?status=grace": "XYZ", "?status=read_only": "ABC", "?q=alpha": "DEF", "?q=xyz": "XYZ", "?plan=NOPE": "", "?status=trial&q=hotel": "DEF"} {
		got := strings.Join(codes(o.want(t, 200, http.MethodGet, "/tenants"+q, nil)), ",")
		if got != want {
			t.Errorf("%s: %q, want %q", q, got, want)
		}
	}
	// a status filter does not change the counts of the tabs
	if s := o.do(http.MethodGet, "/tenants?status=grace", nil).body["summary"].(map[string]any); s["read_only"] != 1.0 {
		t.Errorf("the summary follows the status filter: %v", s)
	}
	if r := o.do(http.MethodGet, "/tenants?status=nonsense", nil); r.status != 422 || r.body["code"] != "VALIDATION_FAILED" {
		t.Errorf("a bad status: %d %v", r.status, r.body)
	}

	rec, _ := platformCall(e, http.MethodGet, "/api/v1/platform/tenants/export.csv?status=grace", o.token, nil)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") || !strings.Contains(body, "XYZ") || strings.Contains(body, "ABC") || strings.Count(body, "\n") != 2 {
		t.Fatalf("csv: %d %q", rec.Code, body)
	}
}

func TestConsoleCreateTenant(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "create@example.test")
	r := o.want(t, 201, http.MethodPost, "/tenants", map[string]any{"code": "new1", "name": "New Hotel", "timezone": "Asia/Makassar",
		"admin": map[string]any{"email": "boss@new.test", "full_name": "The Boss"}})
	pw, _ := r.body["admin_password"].(string)
	if pw == "" || r.body["tenant"].(map[string]any)["code"] != "NEW1" {
		t.Fatalf("answer: %v", r.body)
	}
	// the generated password signs the administrator in, and the tenant starts on the default plan
	anon := &client{env: e}
	if l := anon.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "NEW1", "email": "boss@new.test", "password": pw}); l.status != 200 {
		t.Fatalf("the administrator cannot sign in: %d %v", l.status, l.body)
	}
	d := o.want(t, 200, http.MethodGet, "/tenants/NEW1", nil)
	if d.body["subscription"] == nil || d.body["summary"].(map[string]any)["plan_code"] == "" || d.body["status"] != "ACTIVE" {
		t.Fatalf("detail: %v", d.body)
	}
	admins := items(o.want(t, 200, http.MethodGet, "/tenants/NEW1/admins", nil))
	if len(admins) != 1 || admins[0]["email"] != "boss@new.test" || len(admins[0]) != 4 {
		t.Fatalf("admins: %v", admins)
	}
	// without an administrator there is no password; a bad timezone or a used code is refused and leaves nothing
	if r := o.want(t, 201, http.MethodPost, "/tenants", map[string]any{"code": "NEW2", "name": "Two", "timezone": "Asia/Jakarta"}); r.body["admin_password"] != nil {
		t.Errorf("a password without an administrator: %v", r.body)
	}
	if r := o.do(http.MethodPost, "/tenants", map[string]any{"code": "NEW3", "name": "Three", "timezone": "Mars/Base", "admin": map[string]any{"email": "a@b.test", "full_name": "A"}}); r.status != 422 {
		t.Errorf("bad zone: %d %v", r.status, r.body)
	}
	if n := countWhere(t, `SELECT count(*) FROM tenants WHERE code = 'NEW3'`); n != 0 {
		t.Error("a refused tenant left a row")
	}
	// the administrator is refused (a weak e-mail) after the tenant: the tenant must not stay
	if r := o.do(http.MethodPost, "/tenants", map[string]any{"code": "NEW4", "name": "Four", "timezone": "Asia/Jakarta", "admin": map[string]any{"email": "not-an-email", "full_name": "A"}}); r.status != 422 {
		t.Errorf("bad e-mail: %d %v", r.status, r.body)
	}
	if n := countWhere(t, `SELECT count(*) FROM tenants WHERE code = 'NEW4'`); n != 0 {
		t.Error("the tenant stayed although its administrator was refused")
	}
	if r := o.do(http.MethodGet, "/tenants/NOPE", nil); r.status != 404 || r.body["code"] != "TENANT_NOT_FOUND" {
		t.Errorf("unknown tenant: %d %v", r.status, r.body)
	}
}

func TestConsoleChangesAreAuditedTwice(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "audit@example.test")
	o.want(t, 200, http.MethodPatch, "/tenants/ABC/subscription", map[string]any{"grace_days": 9, "limits": map[string]any{"max_rooms": 40}})

	var opID int64
	if err := dbtest.Pool(t).QueryRow(context.Background(), `SELECT id FROM operator_accounts WHERE email = 'audit@example.test'`).Scan(&opID); err != nil {
		t.Fatal(err)
	}
	hotel := countWhere(t, `SELECT count(*) FROM audit_logs a JOIN tenants t ON t.id = a.tenant_id WHERE t.code = 'ABC' AND a.operator_id = $1 AND a.user_id IS NULL`, opID)
	platform := countWhere(t, `SELECT count(*) FROM operator_audit_logs o JOIN tenants t ON t.id = o.tenant_id WHERE t.code = 'ABC' AND o.operator_id = $1`, opID)
	if hotel == 0 || hotel != platform {
		t.Fatalf("the change must be in both trails: hotel %d, platform %d", hotel, platform)
	}
	// the activity shows the operator by name
	act := items(o.want(t, 200, http.MethodGet, "/tenants/ABC/activity?limit=5", nil))
	if len(act) == 0 || act[0]["actor_kind"] != "operator" || act[0]["actor_name"] != "Op audit@example.test" {
		t.Fatalf("activity: %v", act)
	}
	if r := o.do(http.MethodGet, "/tenants/ABC/activity?limit=0", nil); r.status != 422 {
		t.Errorf("limit 0: %d", r.status)
	}
	// the hotel sees "operator" and no name; the operator's trail keeps the name (through the account) and the tenant
	hotelLog := e.login("ABC").do(http.MethodGet, "/api/v1/audit-logs?limit=50", nil)
	if hotelLog.status != 200 {
		t.Fatalf("the hotel's audit trail: %d %v", hotelLog.status, hotelLog.body)
	}
	seen := false
	rows, _ := hotelLog.body["data"].([]any)
	for _, v := range rows {
		it, _ := v.(map[string]any)
		if it["actor_kind"] == "operator" {
			seen = true
			if it["user"] != nil {
				t.Errorf("an operator's entry names a user: %v", it)
			}
		}
	}
	raw, _ := json.Marshal(hotelLog.body)
	if !seen || strings.Contains(string(raw), "audit@example.test") || strings.Contains(string(raw), "Op audit@example.test") {
		t.Errorf("the hotel must see the operator as an operator, never by name (seen %v): %s", seen, raw)
	}
	if n := countWhere(t, `SELECT count(*) FROM operator_audit_logs o JOIN operator_accounts a ON a.id = o.operator_id JOIN tenants t ON t.id = o.tenant_id
		WHERE t.code = 'ABC' AND a.email = 'audit@example.test' AND a.full_name = 'Op audit@example.test'`); n == 0 {
		t.Error("the operator's trail does not tie the change to the operator's name and the tenant")
	}
	// a plan has no tenant: only the operator's trail
	o.want(t, 201, http.MethodPost, "/plans", map[string]any{"code": "AUDITED", "name": "Audited", "modules": []string{}, "limits": map[string]any{}, "price": "100000", "currency_code": "IDR",
		"billing_period_months": 1, "trial_days": 0, "is_active": true, "is_default": false})
	if n := countWhere(t, `SELECT count(*) FROM operator_audit_logs WHERE action = 'plan.created' AND operator_id = $1 AND tenant_id IS NULL`, opID); n != 1 {
		t.Errorf("plan.created in the operator's trail: %d", n)
	}
}

func TestConsoleSuspendAndReactivate(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "suspend@example.test")
	abc := e.login("ABC")
	if r := abc.do(http.MethodGet, "/api/v1/auth/me", nil); r.status != 200 {
		t.Fatalf("me: %d", r.status)
	}

	// the reason is required and the confirmation must be the tenant's code (checked here, not in the browser)
	if r := o.do(http.MethodPost, "/tenants/ABC/suspend", map[string]any{"reason": "", "confirm_code": "ABC"}); r.status != 422 {
		t.Errorf("no reason: %d %v", r.status, r.body)
	}
	if r := o.do(http.MethodPost, "/tenants/ABC/suspend", map[string]any{"reason": "unpaid", "confirm_code": "XYZ"}); r.status != 422 || r.body["code"] != "CONFIRMATION_MISMATCH" {
		t.Errorf("wrong confirmation: %d %v", r.status, r.body)
	}
	if abc.do(http.MethodGet, "/api/v1/auth/me", nil).status != 200 {
		t.Fatal("a refused suspension signed the hotel out")
	}

	d := o.want(t, 200, http.MethodPost, "/tenants/ABC/suspend", map[string]any{"reason": "unpaid for 3 months", "confirm_code": "abc"})
	if d.body["status"] != "SUSPENDED" || d.body["status_reason"] != "unpaid for 3 months" {
		t.Fatalf("detail: %v", d.body)
	}
	if r := abc.do(http.MethodGet, "/api/v1/auth/me", nil); r.status != 401 {
		t.Errorf("the session of a suspended hotel still works: %d %v", r.status, r.body)
	}
	if n := countWhere(t, `SELECT count(*) FROM user_sessions s JOIN users u ON u.id = s.user_id JOIN tenants t ON t.id = u.tenant_id WHERE t.code = 'ABC' AND s.revoked_reason = 'ACCOUNT_DISABLED'`); n == 0 {
		t.Error("the sessions were not ended with ACCOUNT_DISABLED")
	}
	if l := (&client{env: e}).do(http.MethodPost, "/api/v1/auth/login", map[string]string{"tenant_code": "ABC", "email": "admin@hotel.com", "password": testPassword}); l.status == 200 {
		t.Error("a suspended hotel can sign in")
	}
	if xyz := e.login("XYZ"); xyz.do(http.MethodGet, "/api/v1/auth/me", nil).status != 200 {
		t.Error("another hotel was affected")
	}
	if r := o.do(http.MethodPost, "/tenants/ABC/suspend", map[string]any{"reason": "again", "confirm_code": "ABC"}); r.status != 409 || r.body["code"] != "TENANT_ALREADY_SUSPENDED" {
		t.Errorf("twice: %d %v", r.status, r.body)
	}
	if o.want(t, 200, http.MethodGet, "/tenants?status=suspended", nil); countWhere(t, `SELECT count(*) FROM audit_logs WHERE action = 'tenant.suspended' AND operator_id IS NOT NULL`) != 1 ||
		countWhere(t, `SELECT count(*) FROM operator_audit_logs WHERE action = 'tenant.suspended'`) != 1 {
		t.Error("the suspension is not in both trails")
	}

	// a reactivation needs a reason too, and then the hotel can sign in again
	if r := o.do(http.MethodPost, "/tenants/ABC/reactivate", map[string]any{"reason": " "}); r.status != 422 {
		t.Errorf("reactivate without a reason: %d", r.status)
	}
	if d := o.want(t, 200, http.MethodPost, "/tenants/ABC/reactivate", map[string]any{"reason": "paid"}); d.body["status"] != "ACTIVE" || d.body["status_reason"] != nil {
		t.Fatalf("reactivated: %v", d.body)
	}
	if n := countWhere(t, `SELECT count(*) FROM tenants WHERE code = 'ABC' AND status = 'ACTIVE' AND status_reason IS NULL`); n != 1 {
		t.Error("reactivation must empty tenants.status_reason in the table")
	}
	e.login("ABC")
	if r := o.do(http.MethodPost, "/tenants/ABC/reactivate", map[string]any{"reason": "again"}); r.status != 409 {
		t.Errorf("reactivate twice: %d", r.status)
	}
}

func TestConsolePlans(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "plans@example.test")
	abc := e.login("ABC")
	small := map[string]any{"code": "SMALL", "name": "Small", "modules": []string{}, "limits": map[string]any{"max_users": 5}, "price": "250000", "currency_code": "IDR",
		"billing_period_months": 1, "trial_days": 14, "is_active": true, "is_default": false}
	o.want(t, 201, http.MethodPost, "/plans", small)
	o.want(t, 200, http.MethodPatch, "/tenants/ABC/subscription", map[string]any{"plan_code": "SMALL"})
	role := abc.do(http.MethodPost, "/api/v1/roles", map[string]any{"name": "Nothing", "permissions": []string{}})
	if role.status != 201 {
		t.Fatalf("role: %d %v", role.status, role.body)
	}
	if r := abc.do(http.MethodPost, "/api/v1/users", map[string]any{"email": "second@hotel.com", "full_name": "Second", "password": testPassword, "grants": []map[string]any{}}); r.status != 201 {
		t.Fatalf("second user: %d %v", r.status, r.body)
	}

	list := items(o.want(t, 200, http.MethodGet, "/plans", nil))
	counts := map[string]float64{}
	for _, p := range list {
		counts[p["code"].(string)] = p["tenant_count"].(float64)
	}
	if counts["SMALL"] != 1 {
		t.Fatalf("tenant counts: %v", counts)
	}

	// a lower limit puts ABC (two users) over it: the answer says so, and nothing stops the change
	small["limits"] = map[string]any{"max_users": 1}
	r := o.want(t, 200, http.MethodPatch, "/plans/SMALL", small)
	aff := r.body["affected"].([]any)
	if len(aff) != 1 {
		t.Fatalf("affected: %v", r.body["affected"])
	}
	a := aff[0].(map[string]any)
	if a["tenant_code"] != "ABC" || a["resource"] != "users" || a["used"] != 2.0 || a["limit"] != 1.0 {
		t.Fatalf("affected: %v", a)
	}
	// an override of the tenant's own is not over the plan's limit
	o.want(t, 200, http.MethodPatch, "/tenants/ABC/subscription", map[string]any{"limits": map[string]any{"max_users": 10}})
	small["limits"] = map[string]any{"max_users": 2}
	if r := o.want(t, 200, http.MethodPatch, "/plans/SMALL", small); len(r.body["affected"].([]any)) != 0 {
		t.Errorf("affected: %v", r.body["affected"])
	}
	// the code does not change; an unknown plan is 404
	small["code"] = "OTHER"
	if r := o.do(http.MethodPatch, "/plans/SMALL", small); r.status != 422 {
		t.Errorf("code change: %d %v", r.status, r.body)
	}
	if r := o.do(http.MethodPatch, "/plans/NOPE", map[string]any{"name": "x"}); r.status != 422 && r.status != 404 {
		t.Errorf("unknown plan: %d %v", r.status, r.body)
	}
}

func TestConsoleInvoices(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "invoices@example.test")
	o.want(t, 200, http.MethodPatch, "/tenants/ABC/subscription", map[string]any{"status": "ACTIVE", "paid_through": "2026-09-30"})

	pv := o.want(t, 200, http.MethodGet, "/tenants/ABC/invoices/preview?periods=2", nil)
	if pv.body["period_start"] != "2026-10-01" || pv.body["period_end"] == nil || pv.body["next_number"] != "SUB-2026-00001" || pv.body["open_invoice"] != nil {
		t.Fatalf("preview: %v", pv.body)
	}
	if n := countWhere(t, `SELECT count(*) FROM subscription_invoices`); n != 0 {
		t.Fatal("the preview wrote an invoice")
	}
	if r := o.do(http.MethodGet, "/tenants/ABC/invoices/preview?periods=99", nil); r.status != 422 {
		t.Errorf("periods 99: %d", r.status)
	}

	// due on the 30th: in Jakarta it is the 30th at 20:00, so it is not overdue yet
	inv := o.want(t, 201, http.MethodPost, "/tenants/ABC/invoices", map[string]any{"periods": 1, "due_on": "2026-09-30", "notes": "first"})
	number := inv.body["number"].(string)
	if number != "SUB-2026-00001" || inv.body["status"] != "ISSUED" {
		t.Fatalf("invoice: %v", inv.body)
	}
	if pv := o.want(t, 200, http.MethodGet, "/tenants/ABC/invoices/preview", nil); pv.body["open_invoice"] == nil || pv.body["next_number"] != "SUB-2026-00002" {
		t.Errorf("preview with an open invoice: %v", pv.body)
	}
	l := o.want(t, 200, http.MethodGet, "/invoices?status=overdue", nil)
	if len(items(l)) != 0 {
		t.Fatalf("overdue before the day is over in the tenant's zone: %v", items(l))
	}

	// 18:00 UTC is already the next day in Jakarta, and still the same day in UTC
	e.clock.Advance(5 * time.Hour)
	o = newOperator(t, e, "invoices2@example.test")
	l = o.want(t, 200, http.MethodGet, "/invoices?status=overdue", nil)
	if it := items(l); len(it) != 1 || it[0]["overdue_days"] != 1.0 || it[0]["tenant_code"] != "ABC" || it[0]["tenant_name"] == nil {
		t.Fatalf("overdue: %v", l.body)
	}
	if got := o.want(t, 200, http.MethodGet, "/invoices?status=open", nil); len(items(got)) != 1 || len(got.body["total_open"].([]any)) != 1 {
		t.Fatalf("open: %v", got.body)
	}
	if o.want(t, 200, http.MethodGet, "/invoices?tenant=XYZ", nil); len(items(o.do(http.MethodGet, "/invoices?tenant=XYZ", nil))) != 0 {
		t.Error("the tenant filter")
	}
	if ts := o.want(t, 200, http.MethodGet, "/tenants", nil); fmt.Sprint(items(ts)[0]["overdue_invoice_count"]) != "1" {
		t.Errorf("the list does not count the overdue invoice: %v", items(ts)[0])
	}

	// paying extends the subscription to the end of the period; a second payment is refused
	if r := o.do(http.MethodPost, "/invoices/"+number+"/pay", map[string]any{"reference": "x"}); r.status != 422 {
		t.Errorf("pay without a date: %d", r.status)
	}
	p := o.want(t, 200, http.MethodPost, "/invoices/"+number+"/pay", map[string]any{"paid_on": "2026-10-01", "reference": "TRF-1"})
	if p.body["invoice"].(map[string]any)["status"] != "PAID" || p.body["subscription"].(map[string]any)["paid_through"] != "2026-10-31" {
		t.Fatalf("paid: %v", p.body)
	}
	if r := o.do(http.MethodPost, "/invoices/"+number+"/pay", map[string]any{"paid_on": "2026-10-01"}); r.status != 409 || r.body["code"] != "INVOICE_NOT_OPEN" {
		t.Errorf("pay twice: %d %v", r.status, r.body)
	}
	if got := o.want(t, 200, http.MethodGet, "/invoices?status=overdue", nil); len(items(got)) != 0 {
		t.Error("a paid invoice is overdue")
	}

	// void needs a reason
	second := o.want(t, 201, http.MethodPost, "/tenants/XYZ/invoices", map[string]any{"amount": "0"})
	n2 := second.body["number"].(string)
	if r := o.do(http.MethodPost, "/invoices/"+n2+"/void", map[string]any{"reason": ""}); r.status != 422 {
		t.Errorf("void without a reason: %d", r.status)
	}
	if v := o.want(t, 200, http.MethodPost, "/invoices/"+n2+"/void", map[string]any{"reason": "issued by mistake"}); v.body["status"] != "VOID" {
		t.Fatalf("void: %v", v.body)
	}
	if r := o.do(http.MethodPost, "/invoices/SUB-2026-99999/void", map[string]any{"reason": "x"}); r.status != 404 {
		t.Errorf("unknown invoice: %d", r.status)
	}
	if got := items(o.want(t, 200, http.MethodGet, "/tenants/ABC/invoices", nil)); len(got) != 1 {
		t.Errorf("tenant invoices: %v", got)
	}
}

// 300 tenants: the list is one query and stays fast.
func TestConsoleTenantListWithManyTenants(t *testing.T) {
	e := consoleEnv(t)
	o := newOperator(t, e, "many@example.test")
	if _, err := dbtest.Pool(t).Exec(context.Background(), `
		WITH new_tenants AS (
			INSERT INTO tenants (code, name, timezone) SELECT 'T' || lpad(g::text, 4, '0'), 'Hotel ' || g, 'Asia/Jakarta' FROM generate_series(1, 300) g RETURNING id)
		INSERT INTO tenant_subscriptions (tenant_id, plan_id, status, started_on, paid_through, grace_days)
		SELECT n.id, (SELECT id FROM subscription_plans WHERE is_default), 'ACTIVE', DATE '2026-01-01', DATE '2026-12-31', 7 FROM new_tenants n`); err != nil {
		t.Fatal(err)
	}
	start := time.Now() //nolint:forbidigo // a measurement of the test, not a business date
	r := o.want(t, 200, http.MethodGet, "/tenants", nil)
	took := time.Since(start) //nolint:forbidigo // see start
	if len(items(r)) != 302 {
		t.Fatalf("tenants: %d", len(items(r)))
	}
	t.Logf("the list of 302 tenants took %s", took)
	if took > 2*time.Second {
		t.Errorf("the list took %s", took)
	}
}
