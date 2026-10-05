package tenancy_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/tenancy"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

// 2026-09-30 20:00 in Jakarta (UTC+7): the property's local date is 30 Sep.
var t0 = time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)

type env struct {
	pool  *pgxpool.Pool
	txm   *db.TxManager
	clock *clock.Fake
	svc   *tenancy.Service
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	txm := db.NewTxManager(pool, 5*time.Second)
	c := clock.NewFake(t0)
	return env{pool: pool, txm: txm, clock: c, svc: tenancy.NewService(txm, c, audit.NewWriter(c), iam.NewAuthorizer(txm))}
}

func admin(tenantID int64) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{TenantID: tenantID, IsTenantAdmin: true})
}

func settings() tenancy.PropertySettings {
	return tenancy.PropertySettings{
		Name: "Hotel Bali", Address: "Jl. Pantai 1", City: "Denpasar", CountryCode: "id",
		Timezone: "Asia/Jakarta", CurrencyCode: "idr", CurrencyDecimals: 0,
		CheckInTime: civil.MustParseTimeOfDay("14:00"), CheckOutTime: civil.MustParseTimeOfDay("12:00"),
		NightAuditMarksOccupiedDirty: true, NightAuditEarliestTime: civil.MustParseTimeOfDay("20:00"), RefundMethods: []string{"CASH"},
	}
}

func (e env) tenant(t *testing.T, code string) tenancy.Tenant {
	t.Helper()
	tn, err := e.svc.CreateTenant(context.Background(), code, code+" Hotels", "Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

func (e env) property(t *testing.T, tenantID int64, code string) tenancy.PropertyWithDay {
	t.Helper()
	p, err := e.svc.CreateProperty(admin(tenantID), tenancy.CreatePropertyInput{
		Code: code, Settings: settings(), OpeningBusinessDate: civil.MustParseDate("2026-09-30"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (e env) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// wantCode asserts the error code and returns the field errors (not an error value).
func wantCode(t *testing.T, err error, code string) []apperr.FieldError {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("got %v, want %s", err, code)
	}
	return e.Fields
}

func TestCreatePropertyOpensDayAndSequences(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	p := e.property(t, tn.ID, " bali ")

	if p.Code != "BALI" || p.CountryCode != "ID" || p.CurrencyCode != "IDR" || p.BusinessDate.String() != "2026-09-30" {
		t.Fatalf("unexpected property %+v", p)
	}
	if n := e.count(t, `SELECT count(*) FROM business_days WHERE property_id = $1 AND status = 'OPEN'`, p.ID); n != 1 {
		t.Fatalf("open business days: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM document_sequences WHERE property_id = $1`, p.ID); n != 19 {
		t.Fatalf("sequences: %d", n)
	}
	// tenant.created + property.created + business_day.opened, stamped with the business date.
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE tenant_id = $1`, tn.ID); n != 3 {
		t.Fatalf("audit entries: %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'property.created' AND business_date = '2026-09-30' AND new_data->>'code' = 'BALI'`); n != 1 {
		t.Fatal("property.created audit entry missing or incomplete")
	}
}

func TestCreatePropertyValidation(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	st := settings()
	st.Timezone = "WIB"
	st.CurrencyDecimals = 7
	_, err := e.svc.CreateProperty(admin(tn.ID), tenancy.CreatePropertyInput{
		Code: "hotel bali", Settings: st, OpeningBusinessDate: civil.MustParseDate("2026-09-30"),
	})
	fieldErrs := wantCode(t, err, "VALIDATION_FAILED")
	fields := map[string]bool{}
	for _, f := range fieldErrs {
		fields[f.Field] = true
	}
	for _, f := range []string{"code", "timezone", "currency_decimals"} {
		if !fields[f] {
			t.Errorf("missing field error %q in %+v", f, fieldErrs)
		}
	}

	// A future opening date would put the business date ahead of reality.
	_, err = e.svc.CreateProperty(admin(tn.ID), tenancy.CreatePropertyInput{
		Code: "BALI", Settings: settings(), OpeningBusinessDate: civil.MustParseDate("2026-10-01"),
	})
	wantCode(t, err, "VALIDATION_FAILED")

	e.property(t, tn.ID, "BALI")
	_, err = e.svc.CreateProperty(admin(tn.ID), tenancy.CreatePropertyInput{
		Code: "BALI", Settings: settings(), OpeningBusinessDate: civil.MustParseDate("2026-09-30"),
	})
	wantCode(t, err, "CODE_TAKEN")
	if n := e.count(t, `SELECT count(*) FROM properties`); n != 1 {
		t.Fatalf("a failed create must leave nothing behind, got %d properties", n)
	}
}

func TestPropertyAccessAndTenantIsolation(t *testing.T) {
	e := setup(t)
	abc, xyz := e.tenant(t, "ABC"), e.tenant(t, "XYZ")
	bali := e.property(t, abc.ID, "BALI")
	jkt := e.property(t, abc.ID, "JKT")
	e.property(t, xyz.ID, "SG")

	// Another tenant cannot see the property: 404, never 403.
	_, err := e.svc.GetProperty(admin(xyz.ID), bali.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	list, err := e.svc.ListProperties(admin(abc.ID), 0, 50)
	if err != nil || len(list) != 2 {
		t.Fatalf("tenant admin sees own properties only: %v %d", err, len(list))
	}

	// A non-admin user sees only granted properties and cannot create or edit.
	var userID, roleID int64
	ctx := context.Background()
	if err := e.pool.QueryRow(ctx, `INSERT INTO users (tenant_id, email, password_hash, full_name) VALUES ($1, 'fd@hotel.com', 'x', 'Front Desk') RETURNING id`, abc.ID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := e.pool.QueryRow(ctx, `INSERT INTO roles (tenant_id, name) VALUES ($1, 'Front Desk') RETURNING id`, abc.ID).Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(ctx, `INSERT INTO user_properties (tenant_id, user_id, property_id, role_id) VALUES ($1, $2, $3, $4)`, abc.ID, userID, bali.ID, roleID); err != nil {
		t.Fatal(err)
	}
	user := auth.WithPrincipal(ctx, auth.Principal{TenantID: abc.ID, UserID: userID})
	if _, err := e.svc.GetProperty(user, bali.ID); err != nil {
		t.Fatalf("granted property: %v", err)
	}
	_, err = e.svc.GetProperty(user, jkt.ID)
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if list, err := e.svc.ListProperties(user, 0, 50); err != nil || len(list) != 1 || list[0].ID != bali.ID {
		t.Fatalf("user list: %v %+v", err, list)
	}
	_, err = e.svc.CreateProperty(user, tenancy.CreatePropertyInput{Code: "X", Settings: settings(), OpeningBusinessDate: civil.MustParseDate("2026-09-30")})
	wantCode(t, err, "PERMISSION_DENIED")

	// Editing settings needs property.manage at that property: 403 without it, 404 elsewhere.
	newName := "Renamed"
	_, err = e.svc.UpdateProperty(user, bali.ID, tenancy.PropertyPatch{Name: &newName})
	wantCode(t, err, "PERMISSION_DENIED")
	_, err = e.svc.UpdateProperty(user, jkt.ID, tenancy.PropertyPatch{Name: &newName})
	wantCode(t, err, "PROPERTY_NOT_FOUND")
	if _, err := e.pool.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_code) VALUES ($1, 'property.manage')`, roleID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateProperty(user, bali.ID, tenancy.PropertyPatch{Name: &newName}); err != nil {
		t.Fatalf("with property.manage: %v", err)
	}

	_, err = e.svc.GetProperty(context.Background(), bali.ID)
	wantCode(t, err, "UNAUTHENTICATED")
}

func TestUpdatePropertyAndCurrencyLock(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	p := e.property(t, tn.ID, "BALI")
	ctx := admin(tn.ID)

	name, inspect := "Hotel Bali Resort", true
	updated, err := e.svc.UpdateProperty(ctx, p.ID, tenancy.PropertyPatch{Name: &name, RequireRoomInspectionForCheckin: &inspect})
	if err != nil || updated.Name != name || !updated.RequireRoomInspectionForCheckin || updated.CurrencyCode != "IDR" {
		t.Fatalf("patch: %v %+v", err, updated)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE action = 'property.updated'
	                     AND old_data->>'name' = 'Hotel Bali' AND new_data->>'name' = 'Hotel Bali Resort'`); n != 1 {
		t.Fatal("update must be audited with old and new data")
	}

	bad := "Nowhere/Nothing"
	_, err = e.svc.UpdateProperty(ctx, p.ID, tenancy.PropertyPatch{Timezone: &bad})
	wantCode(t, err, "VALIDATION_FAILED")

	// Currency may change while there are no financial transactions...
	usd, two := "USD", int32(2)
	if _, err := e.svc.UpdateProperty(ctx, p.ID, tenancy.PropertyPatch{CurrencyCode: &usd, CurrencyDecimals: &two}); err != nil {
		t.Fatalf("currency change before transactions: %v", err)
	}
	// ...and is locked once one exists.
	postLaundryCharge(t, e.pool, tn.ID, p.ID)
	idr, zero := "IDR", int32(0)
	_, err = e.svc.UpdateProperty(ctx, p.ID, tenancy.PropertyPatch{CurrencyCode: &idr, CurrencyDecimals: &zero})
	wantCode(t, err, "CURRENCY_LOCKED")
}

// postLaundryCharge writes a minimal valid ledger entry directly (the billing
// module arrives in M9); it only needs to exist for the currency lock.
func postLaundryCharge(t *testing.T, pool *pgxpool.Pool, tenantID, propertyID int64) {
	t.Helper()
	ctx := context.Background()
	var codeID, resID, folioID int64
	steps := []struct {
		sql string
		dst *int64
	}{
		{`INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type) VALUES ($1, $2, 'LAUNDRY', 'Laundry', 'SERVICE') RETURNING id`, &codeID},
		{`INSERT INTO reservations (tenant_id, property_id, confirmation_number, reservation_date, source) VALUES ($1, $2, 'R1', '2026-09-30', 'PHONE') RETURNING id`, &resID},
	}
	for _, s := range steps {
		if err := pool.QueryRow(ctx, s.sql, tenantID, propertyID).Scan(s.dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id) VALUES ($1, $2, 'F1', $3) RETURNING id`,
		tenantID, propertyID, resID).Scan(&folioID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO folio_items (tenant_id, property_id, folio_id, business_date, service_date, transaction_type,
	        charge_code_id, description, quantity, unit_price, price_mode, base_amount, net_amount, debit, source)
	        VALUES ($1, $2, $3, '2026-09-30', '2026-09-30', 'CHARGE', $4, 'Laundry', 1, 50, 'EXCLUSIVE', 50, 50, 50, 'MANUAL')`,
		tenantID, propertyID, folioID, codeID); err != nil {
		t.Fatal(err)
	}
}

func TestCloseAndOpenNext(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	pw := e.property(t, tn.ID, "BALI")
	prop := pw.Property
	bd := civil.MustParseDate("2026-09-30")
	closer := int64(0)
	_ = closer

	closeDay := func(expected civil.Date) (tenancy.BusinessDay, tenancy.BusinessDay, error) {
		var c, o tenancy.BusinessDay
		err := e.txm.WithinTx(admin(tn.ID), func(ctx context.Context) error {
			var err error
			c, o, err = e.svc.CloseAndOpenNext(ctx, prop, expected, nil, json.RawMessage(`{"occupied_rooms": 0}`))
			return err
		})
		return c, o, err
	}

	// 19:59 local: too early.
	e.clock.Set(time.Date(2026, 9, 30, 12, 59, 0, 0, time.UTC))
	_, _, err := closeDay(bd)
	wantCode(t, err, "NIGHT_AUDIT_TOO_EARLY")

	// 20:00 local: allowed.
	e.clock.Set(t0)
	closed, opened, err := closeDay(bd)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Status != tenancy.DayClosed || closed.ClosedAt == nil || string(closed.Summary) != `{"occupied_rooms": 0}` ||
		opened.Status != tenancy.DayOpen || opened.BusinessDate.String() != "2026-10-01" {
		t.Fatalf("closed=%+v opened=%+v", closed, opened)
	}

	// A stale screen still showing 30 Sep is rejected.
	_, _, err = closeDay(bd)
	wantCode(t, err, "BUSINESS_DATE_MISMATCH")

	// 1 Oct cannot be closed at 20:30 on 30 Sep: the business date never runs ahead.
	e.clock.Set(t0.Add(30 * time.Minute))
	_, _, err = closeDay(bd.AddDays(1))
	wantCode(t, err, "NIGHT_AUDIT_TOO_EARLY")

	history, err := e.svc.BusinessDayHistory(context.Background(), prop.ID, nil, 10)
	if err != nil || len(history) != 2 || history[0].BusinessDate.String() != "2026-10-01" || history[1].Status != tenancy.DayClosed {
		t.Fatalf("history: %v %+v", err, history)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_logs WHERE action IN ('business_day.closed', 'business_day.opened')`); n != 3 {
		t.Fatalf("audit entries for day changes: %d", n)
	}

	// Outside a transaction the business-day lock is refused.
	if _, _, err := e.svc.CloseAndOpenNext(admin(tn.ID), prop, bd.AddDays(1), nil, nil); !errors.Is(err, db.ErrNoTx) {
		t.Fatalf("got %v, want ErrNoTx", err)
	}
}

// A posting queued behind night audit must land on the NEW business day, not
// fail with "no open business day" (READ COMMITTED re-check + snapshot issue).
func TestWriterQueuedBehindNightAuditSeesNewDay(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	prop := e.property(t, tn.ID, "BALI").Property

	auditHolds, release, auditDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		auditDone <- e.txm.WithinTx(admin(tn.ID), func(ctx context.Context) error {
			if _, err := e.svc.RequireOpenBusinessDay(ctx, prop.ID, db.ForUpdate, nil); err != nil {
				close(auditHolds)
				return err
			}
			close(auditHolds)
			<-release
			_, _, err := e.svc.CloseAndOpenNext(ctx, prop, civil.MustParseDate("2026-09-30"), nil, nil)
			return err
		})
	}()
	<-auditHolds

	writerDone := make(chan tenancy.BusinessDay, 1)
	writerErr := make(chan error, 1)
	go func() {
		var day tenancy.BusinessDay
		err := e.txm.WithinTx(admin(tn.ID), func(ctx context.Context) error {
			var err error
			day, err = e.svc.RequireOpenBusinessDay(ctx, prop.ID, db.ForShare, nil)
			return err
		})
		writerErr <- err
		writerDone <- day
	}()

	time.Sleep(200 * time.Millisecond) // let the writer queue on the lock
	close(release)
	if err := <-auditDone; err != nil {
		t.Fatalf("night audit: %v", err)
	}
	if err := <-writerErr; err != nil {
		t.Fatalf("queued writer: %v", err)
	}
	if day := <-writerDone; day.BusinessDate.String() != "2026-10-01" {
		t.Fatalf("queued writer saw %s, want the new business date 2026-10-01", day.BusinessDate)
	}
}

func TestDocumentNumbersAreGaplessAndOrdered(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	prop := e.property(t, tn.ID, "BALI")
	ctx := admin(tn.ID)

	next := func() (string, error) {
		var n string
		err := e.txm.WithinTx(ctx, func(ctx context.Context) error {
			var err error
			n, err = e.svc.NextDocumentNumber(ctx, prop.ID, tenancy.SeqReservation)
			return err
		})
		return n, err
	}
	if n, err := next(); err != nil || n != "RES000001" {
		t.Fatalf("first: %q %v", n, err)
	}
	// A rolled-back business transaction gives its number back.
	rollback := errors.New("rollback")
	_ = e.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := e.svc.NextDocumentNumber(ctx, prop.ID, tenancy.SeqReservation); err != nil {
			return err
		}
		return rollback
	})
	if n, err := next(); err != nil || n != "RES000002" {
		t.Fatalf("after rollback: %q %v", n, err)
	}

	// Numbers are lock level L5: taking the business-day lock afterwards is a lock-order bug.
	err := e.txm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := e.svc.NextDocumentNumber(ctx, prop.ID, tenancy.SeqFolio); err != nil {
			return err
		}
		_, err := e.svc.RequireOpenBusinessDay(ctx, prop.ID, db.ForShare, nil)
		return err
	})
	if !errors.Is(err, db.ErrLockOrder) {
		t.Fatalf("got %v, want ErrLockOrder", err)
	}
}

func TestDayClock(t *testing.T) {
	e := setup(t)
	tn := e.tenant(t, "ABC")
	prop := e.property(t, tn.ID, "BALI").Property

	e.clock.Set(time.Date(2026, 9, 30, 19, 30, 0, 0, time.UTC)) // 02:30 on 1 Oct in Jakarta
	c, err := e.svc.DayClock(context.Background(), prop)
	if err != nil {
		t.Fatal(err)
	}
	if c.BusinessDate.String() != "2026-09-30" || c.PropertyLocalTime != "2026-10-01T02:30:00+07:00" || !c.NightAuditAllowed || c.NightAuditOverdue {
		t.Fatalf("got %+v", c)
	}
}
