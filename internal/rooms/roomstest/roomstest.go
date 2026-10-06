// Package roomstest builds a real-database fixture (tenants, properties, users,
// reservations, stays) for the rooms and housekeeping tests. It is test support only.
package roomstest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kamarapms/internal/accounting"
	"kamarapms/internal/audit"
	"kamarapms/internal/availability"
	"kamarapms/internal/bankrec"
	"kamarapms/internal/billingconfig"
	"kamarapms/internal/budget"
	"kamarapms/internal/cityledger"
	"kamarapms/internal/companies"
	"kamarapms/internal/departments"
	"kamarapms/internal/documents"
	"kamarapms/internal/expected"
	"kamarapms/internal/folios"
	"kamarapms/internal/frontdesk"
	"kamarapms/internal/groups"
	"kamarapms/internal/guests"
	"kamarapms/internal/housekeeping"
	"kamarapms/internal/iam"
	"kamarapms/internal/lostfound"
	"kamarapms/internal/maintenance"
	"kamarapms/internal/nightaudit"
	"kamarapms/internal/payables"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/civil"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/rates"
	"kamarapms/internal/reports"
	"kamarapms/internal/reservations"
	"kamarapms/internal/roomcharge"
	"kamarapms/internal/rooms"
	"kamarapms/internal/shifts"
	"kamarapms/internal/taxfiling"
	"kamarapms/internal/taxinvoice"
	"kamarapms/internal/tenancy"
)

// T0 is 20:00 in Jakarta on 2026-09-30, the business date of every fixture property.
var T0 = time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)

// BD is the opening business date of every fixture property.
var BD = civil.MustParseDate("2026-09-30")

// Env is a wired set of services over a clean database.
type Env struct {
	Pool    *pgxpool.Pool
	TxM     *db.TxManager
	Clock   *clock.Fake
	Tenancy *tenancy.Service
	HK      *housekeeping.Service
	Rooms   *rooms.Service
	Guests  *guests.Service
	Billing *billingconfig.Service
	Rates   *rates.Service
	Avail   *availability.Service
	Res     *reservations.Service
	IAM     *iam.Service
	Folios  *folios.Service
	Front   *frontdesk.Service
	Charges *roomcharge.Service
	Audit   *nightaudit.Service
	Reports *reports.Service
	Docs    *documents.Service

	Companies   *companies.Service
	CityLedger  *cityledger.Service
	Groups      *groups.Service
	Maintenance *maintenance.Service
	Accounting  *accounting.Service
	Payables    *payables.Service
	BankRec     *bankrec.Service
	Tax         *taxfiling.Service
	TaxInvoice  *taxinvoice.Service
	Shifts      *shifts.Service
	Budget      *budget.Service
	Departments *departments.Service
	LostFound   *lostfound.Service

	seq int
}

// Setup resets the database and wires the services.
func Setup(t *testing.T) *Env {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	txm := db.NewTxManager(pool, 5*time.Second)
	c := clock.NewFake(T0)
	aw := audit.NewWriter(c)
	authz := iam.NewAuthorizer(txm)
	ten := tenancy.NewService(txm, c, aw, authz)
	hk := housekeeping.NewService(txm, c, aw, authz, ten)
	avail := availability.NewService(txm)
	billing := billingconfig.NewService(txm, c, aw, authz, ten)
	ten.OnPropertyCreated(billing.SeedProperty) // like production: every property starts with the standard charge codes
	rt := rates.NewService(txm, c, aw, authz, ten, avail)
	gs := guests.NewService(txm, c, aw, authz, ten)
	ia := iam.NewService(txm, c, aw, iam.TokenConfig{Secret: []byte(strings.Repeat("s", 32)), AccessTTL: 15 * time.Minute, RefreshTTL: time.Hour})
	acct := accounting.NewService(txm, c, aw, authz, ten, ia)
	ten.OnPropertyCreated(acct.SeedProperty) // and the standard chart of accounts
	dept := departments.NewService(txm, c, aw, authz, ten)
	ten.OnPropertyCreated(dept.SeedProperty) // and the standard departments
	acct.SetDepartments(dept)
	billing.SetDepartments(dept)
	fo := folios.NewService(txm, c, aw, authz, ten, billing, ia)
	co := companies.NewService(txm, c, aw, authz, ten)
	fo.SetCompanyGate(co)
	cl := cityledger.NewService(txm, c, aw, authz, ten, ia, co, acct)
	rc := roomcharge.NewService(txm, c, aw, authz, ten, expected.NewLoader(txm, fo), billing, fo.RoomPoster())
	rs := reservations.NewService(txm, c, aw, authz, ten, avail, rt, billing, gs)
	rs.SetApprover(ia)
	na := nightaudit.NewService(txm, c, aw, authz, ten, rc, rs, hk)
	na.SetJournaler(acct)
	fd := frontdesk.NewService(txm, c, aw, authz, ten, avail, gs, hk, rs, fo, rc)
	rm := rooms.NewService(txm, c, aw, authz, ten, hk, avail)
	ten.OnPropertyCreated(rm.SeedProperty) // and the standard bed types
	taxSvc := taxfiling.NewService(txm, c, aw, authz, ten, acct, ia)
	taxInv := taxinvoice.NewService(txm, c, aw, authz, ten, ia, taxSvc)
	sh := shifts.NewService(txm, c, aw, authz, ten, ia, acct)
	fo.SetShiftGate(sh)
	cl.SetShiftGate(sh)
	na.SetShiftChecker(sh)
	br := bankrec.NewService(txm, c, aw, authz, ten, acct, ia)
	br.SetTax(taxSvc)
	bud := budget.NewService(txm, c, aw, authz, ten, ia)
	docs := documents.NewService(c, ten, fo, fd, rs, gs, cl, co, acct, taxSvc, taxInv)
	docs.SetBudget(bud)
	docs.SetShifts(sh)
	bud.SetDepartments(dept)
	return &Env{Departments: dept, Budget: bud, Shifts: sh, TaxInvoice: taxInv, Docs: docs, Audit: na, Reports: reports.NewService(txm, authz, ten, na), IAM: ia, Folios: fo, Front: fd, Charges: rc, Pool: pool, TxM: txm, Clock: c, Tenancy: ten, HK: hk, Rooms: rm, Guests: gs, Billing: billing, Rates: rt,
		Avail: avail, Res: rs,
		Companies: co, CityLedger: cl, Groups: groups.NewService(txm, aw, authz, ten), Maintenance: maintenance.NewService(txm, c, aw, authz, ten, rm), LostFound: lostfound.NewService(txm, c, aw, authz, ten), Accounting: acct, Payables: payables.NewService(txm, c, aw, authz, ten, acct, ia, taxSvc), BankRec: br, Tax: taxSvc}
}

// Admin returns a context authenticated as the tenant administrator.
func Admin(tenantID int64) context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{TenantID: tenantID, UserID: 0, IsTenantAdmin: true})
}

// Tenant creates a tenant.
func (e *Env) Tenant(t *testing.T, code string) tenancy.Tenant {
	t.Helper()
	tn, err := e.Tenancy.CreateTenant(context.Background(), code, code+" Hotels", "Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	return tn
}

// Property creates an IDR property (no decimals) whose business date is BD.
func (e *Env) Property(t *testing.T, tenantID int64, code string) tenancy.PropertyWithDay {
	t.Helper()
	return e.PropertyIn(t, tenantID, code, "IDR", 0)
}

// PropertyIn creates a property with the given currency whose business date is BD.
func (e *Env) PropertyIn(t *testing.T, tenantID int64, code, currency string, decimals int32) tenancy.PropertyWithDay {
	t.Helper()
	p, err := e.Tenancy.CreateProperty(Admin(tenantID), tenancy.CreatePropertyInput{
		Code: code,
		Settings: tenancy.PropertySettings{
			Name: "Hotel " + code, Timezone: "Asia/Jakarta", CurrencyCode: currency, CurrencyDecimals: decimals,
			CheckInTime: civil.MustParseTimeOfDay("14:00"), CheckOutTime: civil.MustParseTimeOfDay("12:00"),
			NightAuditMarksOccupiedDirty: true, NightAuditEarliestTime: civil.MustParseTimeOfDay("20:00"), RefundMethods: []string{"CASH"},
		},
		OpeningBusinessDate: BD,
	})
	if err != nil {
		t.Fatal(err)
	}
	// The tests take cash without a shift unless a test turns the rule on (the shift tests do).
	_, err = e.Pool.Exec(context.Background(), `UPDATE property_cashier_settings SET require_shift_for_cash = false, block_night_audit = false WHERE property_id = $1`, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// User creates a non-admin user with a role holding perms at the property and returns its context.
func (e *Env) User(t *testing.T, tenantID, propertyID int64, perms ...auth.Permission) context.Context {
	t.Helper()
	ctx := context.Background()
	e.seq++
	var userID, roleID int64
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO users (tenant_id, email, password_hash, full_name) VALUES ($1, $2, 'x', 'User') RETURNING id`,
		tenantID, fmt.Sprintf("u%d@hotel.com", e.seq)).Scan(&userID))
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO roles (tenant_id, name) VALUES ($1, $2) RETURNING id`,
		tenantID, fmt.Sprintf("Role %d", e.seq)).Scan(&roleID))
	for _, p := range perms {
		_, err := e.Pool.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_code) VALUES ($1, $2)`, roleID, string(p))
		must(t, err)
	}
	_, err := e.Pool.Exec(ctx, `INSERT INTO user_properties (tenant_id, user_id, property_id, role_id) VALUES ($1, $2, $3, $4)`,
		tenantID, userID, propertyID, roleID)
	must(t, err)
	return auth.WithPrincipal(ctx, auth.Principal{TenantID: tenantID, UserID: userID})
}

// Password is the password of every account made by Account and AdminAccount.
const Password = "correct horse battery staple"

// Account creates a non-admin user with a real password (for approvals) holding perms at the property. It returns the
// user's context and email.
func (e *Env) Account(t *testing.T, tenantID, propertyID int64, perms ...auth.Permission) (context.Context, string) {
	t.Helper()
	ctx := e.User(t, tenantID, propertyID, perms...)
	p, err := auth.Require(ctx)
	must(t, err)
	e.setPassword(t, p.UserID)
	return ctx, e.emailOf(t, p.UserID)
}

// AdminAccount creates a tenant administrator with a real password. It returns the user's context and email.
func (e *Env) AdminAccount(t *testing.T, tenantID int64) (context.Context, string) {
	t.Helper()
	e.seq++
	email := fmt.Sprintf("admin%d@hotel.com", e.seq)
	var id int64
	must(t, e.Pool.QueryRow(context.Background(), `INSERT INTO users (tenant_id, email, password_hash, full_name, is_tenant_admin) VALUES ($1, $2, 'x', 'Admin', true) RETURNING id`,
		tenantID, email).Scan(&id))
	e.setPassword(t, id)
	return auth.WithPrincipal(context.Background(), auth.Principal{TenantID: tenantID, UserID: id, IsTenantAdmin: true}), email
}

func (e *Env) setPassword(t *testing.T, userID int64) {
	t.Helper()
	hash, err := iam.HashPassword(Password)
	must(t, err)
	_, err = e.Pool.Exec(context.Background(), `UPDATE users SET password_hash = $2 WHERE id = $1`, userID, hash)
	must(t, err)
}

func (e *Env) emailOf(t *testing.T, userID int64) string {
	t.Helper()
	var email string
	must(t, e.Pool.QueryRow(context.Background(), `SELECT email FROM users WHERE id = $1`, userID).Scan(&email))
	return email
}

// UserAt creates a non-admin user with one role per property (different permissions at each) and returns its context.
func (e *Env) UserAt(t *testing.T, tenantID int64, grants map[int64][]auth.Permission) context.Context {
	t.Helper()
	ctx := context.Background()
	e.seq++
	var userID int64
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO users (tenant_id, email, password_hash, full_name) VALUES ($1, $2, 'x', 'User') RETURNING id`,
		tenantID, fmt.Sprintf("u%d@hotel.com", e.seq)).Scan(&userID))
	for propertyID, perms := range grants {
		e.seq++
		var roleID int64
		must(t, e.Pool.QueryRow(ctx, `INSERT INTO roles (tenant_id, name) VALUES ($1, $2) RETURNING id`, tenantID, fmt.Sprintf("Role %d", e.seq)).Scan(&roleID))
		for _, p := range perms {
			_, err := e.Pool.Exec(ctx, `INSERT INTO role_permissions (role_id, permission_code) VALUES ($1, $2)`, roleID, string(p))
			must(t, err)
		}
		_, err := e.Pool.Exec(ctx, `INSERT INTO user_properties (tenant_id, user_id, property_id, role_id) VALUES ($1, $2, $3, $4)`,
			tenantID, userID, propertyID, roleID)
		must(t, err)
	}
	return auth.WithPrincipal(ctx, auth.Principal{TenantID: tenantID, UserID: userID})
}

// RoomType creates an active type with capacity 2+1.
func (e *Env) RoomType(t *testing.T, ctx context.Context, propertyID int64, code string) rooms.RoomType {
	t.Helper()
	rt, err := e.Rooms.CreateRoomType(ctx, propertyID, rooms.RoomTypeInput{
		Code: code, Name: code + " room", MaxAdult: 2, MaxChild: 1, MaxOccupancy: 3, BaseOccupancy: 2, IsActive: true,
	})
	must(t, err)
	return rt
}

// Room creates an active room (housekeeping DIRTY unless status is given).
func (e *Env) Room(t *testing.T, ctx context.Context, propertyID, typeID int64, number string, status ...housekeeping.Status) rooms.Room {
	t.Helper()
	in := rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: typeID, RoomNumber: number, Floor: number[:1], IsActive: true}}
	bed := e.FirstBedType(t, propertyID) // a room has a bed type: the first of the catalogue unless a test gives another
	in.BedTypeID = &bed
	if len(status) > 0 {
		in.InitialHousekeeping = status[0]
	}
	r, err := e.Rooms.CreateRoom(ctx, propertyID, in)
	must(t, err)
	return r
}

// FirstBedType is the first bed type of the catalogue of a property (by its sort order).
func (e *Env) FirstBedType(t *testing.T, propertyID int64) int64 {
	t.Helper()
	var id int64
	must(t, e.Pool.QueryRow(context.Background(), `SELECT id FROM bed_types WHERE property_id = $1 ORDER BY sort_order, id LIMIT 1`, propertyID).Scan(&id))
	return id
}

// RoomWithBed creates a room that has the given bed type.
func (e *Env) RoomWithBed(t *testing.T, ctx context.Context, propertyID, typeID, bedTypeID int64, number string) rooms.Room {
	t.Helper()
	r, err := e.Rooms.CreateRoom(ctx, propertyID, rooms.CreateRoomInput{RoomInput: rooms.RoomInput{RoomTypeID: typeID, RoomNumber: number, Floor: number[:1], BedTypeID: &bedTypeID, IsActive: true}})
	must(t, err)
	return r
}

// Count runs a count query.
func (e *Env) Count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	must(t, e.Pool.QueryRow(context.Background(), sql, args...).Scan(&n))
	return n
}

// Exec runs a statement.
func (e *Env) Exec(t *testing.T, sql string, args ...any) error {
	t.Helper()
	_, err := e.Pool.Exec(context.Background(), sql, args...)
	return err
}

type base struct{ guestID, ratePlanID int64 }

func (e *Env) base(t *testing.T, tenantID, propertyID int64) base {
	t.Helper()
	ctx := context.Background()
	var b base
	err := e.Pool.QueryRow(ctx, `SELECT id FROM rate_plans WHERE property_id = $1 AND code = 'BAR'`, propertyID).Scan(&b.ratePlanID)
	if err == nil {
		must(t, e.Pool.QueryRow(ctx, `SELECT id FROM guests WHERE tenant_id = $1 AND code = 'G1'`, tenantID).Scan(&b.guestID))
		return b
	}
	var ccID int64
	must(t, e.Pool.QueryRow(ctx, `SELECT id FROM charge_codes WHERE property_id = $1 AND code = 'ROOM'`, propertyID).Scan(&ccID)) // seeded with the property
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO rate_plans (tenant_id, property_id, code, name, room_charge_code_id) VALUES ($1, $2, 'BAR', 'Best', $3) RETURNING id`,
		tenantID, propertyID, ccID).Scan(&b.ratePlanID))
	if err := e.Pool.QueryRow(ctx, `SELECT id FROM guests WHERE tenant_id = $1 AND code = 'G1'`, tenantID).Scan(&b.guestID); err != nil {
		must(t, e.Pool.QueryRow(ctx, `INSERT INTO guests (tenant_id, code, last_name) VALUES ($1, 'G1', 'Guest') RETURNING id`,
			tenantID).Scan(&b.guestID))
	}
	return b
}

// Line seeds a reservation line. status is CONFIRMED or CHECKED_IN; roomID may be 0 (type-level booking).
func (e *Env) Line(t *testing.T, tenantID, propertyID, typeID, roomID int64, arrival, departure, status string) int64 {
	t.Helper()
	b := e.base(t, tenantID, propertyID)
	e.seq++
	ctx := context.Background()
	var resID, lineID int64
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO reservations (tenant_id, property_id, confirmation_number, guest_id, reservation_date, source, status, confirmed_at)
		VALUES ($1, $2, $3, $4, $5, 'PHONE', 'CONFIRMED', now()) RETURNING id`,
		tenantID, propertyID, fmt.Sprintf("R%d", e.seq), b.guestID, BD).Scan(&resID))
	var room *int64
	if roomID != 0 {
		room = &roomID
	}
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO reservation_rooms (tenant_id, property_id, reservation_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date, adult_count, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7::date, $8::date, 2, $9) RETURNING id`,
		tenantID, propertyID, resID, typeID, room, b.ratePlanID, arrival, departure, status).Scan(&lineID))
	return lineID
}

// Stay seeds an in-house stay: a CHECKED_IN line, the stay and its open segment in the room.
func (e *Env) Stay(t *testing.T, tenantID, propertyID, typeID, roomID int64, arrival, departure string) int64 {
	t.Helper()
	line := e.Line(t, tenantID, propertyID, typeID, roomID, arrival, departure, "CHECKED_IN")
	b := e.base(t, tenantID, propertyID)
	ctx := context.Background()
	var stayID int64
	must(t, e.Pool.QueryRow(ctx, `INSERT INTO stays (tenant_id, property_id, stay_number, reservation_room_id, guest_id, arrival_date, departure_date, adult_count, actual_check_in_at)
		VALUES ($1, $2, $3, $4, $5, $6::date, $7::date, 2, now()) RETURNING id`,
		tenantID, propertyID, fmt.Sprintf("S%d", line), line, b.guestID, arrival, departure).Scan(&stayID))
	_, err := e.Pool.Exec(ctx, `INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date)
		VALUES ($1, $2, $3, $4, now(), $5::date)`, tenantID, propertyID, stayID, roomID, arrival)
	must(t, err)
	return stayID
}

// Code asserts err is an *apperr.Error with the given code and returns it.
func Code(t *testing.T, err error, code string) *apperr.Error {
	t.Helper()
	e, ok := apperr.As(err)
	if !ok || e.Code != code {
		t.Fatalf("got %v, want error %s", err, code)
	}
	return e
}

// Want asserts err is an *apperr.Error with the given code.
func Want(t *testing.T, err error, code string) {
	t.Helper()
	_ = Code(t, err, code)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
