package auditlog_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"kamarapms/internal/auditlog"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rooms/roomstest"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

var wantCode = roomstest.Want

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type fx struct {
	*roomstest.Env
	tenantID, propID int64
	admin            context.Context
	reader           *auditlog.Reader
}

func setup(t *testing.T) *fx {
	t.Helper()
	e := roomstest.Setup(t)
	tn := e.Tenant(t, "ABC")
	p := e.Property(t, tn.ID, "BALI")
	admin, _ := e.AdminAccount(t, tn.ID)
	return &fx{Env: e, tenantID: tn.ID, propID: p.ID, admin: admin, reader: auditlog.NewReader(e.TxM, iam.NewAuthorizer(e.TxM))}
}

func TestSearchFiltersAndPages(t *testing.T) {
	f := setup(t)
	for _, c := range []string{"VAT", "PB1", "SVC"} {
		_, err := f.Billing.CreateTax(f.admin, f.propID, billingconfig.TaxInput{Code: c, Name: c, Rate: "10", IsActive: true})
		must(t, err)
	}
	_, err := f.Billing.CreateServiceCharge(f.admin, f.propID, billingconfig.ServiceChargeInput{Code: "SC", Name: "Service", Rate: "10", IsActive: true})
	must(t, err)
	pid := f.propID
	all, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid}, 0, 50)
	must(t, err)
	if len(all) < 4 || all[0].ID < all[1].ID || all[0].Action != "service_charge.created" || all[0].BusinessDate == nil || all[0].NewData == nil {
		t.Fatalf("newest first: %+v", all)
	}
	taxes, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "tax"}, 0, 50)
	must(t, err)
	if len(taxes) != 3 {
		t.Fatalf("by entity type: %d", len(taxes))
	}
	one, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "tax", EntityID: &taxes[1].EntityID}, 0, 50)
	must(t, err)
	if len(one) != 1 || one[0].ID != taxes[1].ID {
		t.Fatalf("by entity: %+v", one)
	}
	if act, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, Action: "service_charge.created"}, 0, 50); err != nil || len(act) != 1 {
		t.Fatalf("by action: %v %d", err, len(act))
	}
	// paging: the keyset position is the id of the last row seen
	first, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "tax"}, 0, 2)
	must(t, err)
	next, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "tax"}, first[1].ID, 2)
	must(t, err)
	if len(first) != 2 || len(next) != 1 || next[0].ID >= first[1].ID {
		t.Fatalf("paging: %d %d", len(first), len(next))
	}
	// business date range
	bd := roomstest.BD
	in, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "tax", From: &bd, To: &bd}, 0, 50)
	must(t, err)
	later := civil.MustParseDate("2026-10-05")
	out, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "tax", From: &later}, 0, 50)
	must(t, err)
	if len(in) != 3 || len(out) != 0 {
		t.Fatalf("range: %d %d", len(in), len(out))
	}
	// by user: the tenant admin has no user row id (0), so attribute an entry to a real user
	user := f.User(t, f.tenantID, f.propID, auth.PermAuditRead)
	_ = user
}

func TestSearchRedactsSecretsAndNamesTheUser(t *testing.T) {
	f := setup(t)
	var uid int64
	must(t, f.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE tenant_id = $1 LIMIT 1`, f.tenantID).Scan(&uid))
	must(t, f.Exec(t, `INSERT INTO audit_logs (tenant_id, property_id, business_date, user_id, action, entity_type, entity_id, old_data, new_data)
		VALUES ($1, $2, '2026-09-30', $3, 'x.changed', 'x', 1, '{"password_hash": "abc", "n": 1}', '{"nested": {"refresh_token": "t", "ok": "yes"}, "list": [{"Secret": "s"}]}')`, f.tenantID, f.propID, uid))
	pid := f.propID
	got, err := f.reader.Search(f.admin, auditlog.Filter{PropertyID: &pid, EntityType: "x", UserID: &uid}, 0, 10)
	must(t, err)
	if len(got) != 1 || got[0].User == nil || got[0].User.ID != uid || got[0].User.Name == "" {
		t.Fatalf("user: %+v", got)
	}
	body := string(got[0].OldData) + string(got[0].NewData)
	if strings.Contains(body, "abc") || strings.Contains(body, `"t"`) || strings.Contains(body, `"s"`) || !strings.Contains(body, "yes") || !strings.Contains(body, "[redacted]") {
		t.Fatalf("redaction: %s", body)
	}
}

func TestSearchAccess(t *testing.T) {
	f := setup(t)
	pid := f.propID
	reader := f.User(t, f.tenantID, f.propID, auth.PermAuditRead)
	if _, err := f.reader.Search(reader, auditlog.Filter{PropertyID: &pid}, 0, 10); err != nil {
		t.Fatal(err)
	}
	clerk := f.User(t, f.tenantID, f.propID, auth.PermFolioRead)
	_, err := f.reader.Search(clerk, auditlog.Filter{PropertyID: &pid}, 0, 10)
	wantCode(t, err, "PERMISSION_DENIED")
	// the tenant-level trail (users, roles) is for tenant administrators
	_, err = f.reader.Search(reader, auditlog.Filter{}, 0, 10)
	wantCode(t, err, "PERMISSION_DENIED")
	tenantLevel, err := f.reader.Search(f.admin, auditlog.Filter{}, 0, 10)
	must(t, err)
	for _, r := range tenantLevel {
		if r.EntityType == "tax" {
			t.Fatalf("property entries are not tenant-level: %+v", r)
		}
	}
	// another tenant sees nothing of this one
	other := f.Tenant(t, "XYZ")
	_, err = f.reader.Search(roomstest.Admin(other.ID), auditlog.Filter{PropertyID: &pid}, 0, 10)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
}
