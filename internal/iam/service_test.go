package iam_test

import (
	"context"
	"os"
	"slices"
	"strings"
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

const pw = "correct horse battery staple"

type env struct {
	pool    *pgxpool.Pool
	clock   *clock.Fake
	iam     *iam.Service
	authz   *iam.Authorizer
	tenancy *tenancy.Service
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	txm := db.NewTxManager(pool, 5*time.Second)
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	aw := audit.NewWriter(c)
	authz := iam.NewAuthorizer(txm)
	cfg := iam.TokenConfig{Secret: []byte(strings.Repeat("s", 32)), AccessTTL: 15 * time.Minute, RefreshTTL: 720 * time.Hour}
	return env{pool: pool, clock: c, iam: iam.NewService(txm, c, aw, cfg), authz: authz, tenancy: tenancy.NewService(txm, c, aw, authz)}
}

func (e env) tenantWithAdmin(t *testing.T, code, email string) (tenancy.Tenant, iam.User) {
	t.Helper()
	tn, err := e.tenancy.CreateTenant(context.Background(), code, code+" Hotels", "Asia/Jakarta")
	if err != nil {
		t.Fatal(err)
	}
	u, err := e.iam.BootstrapAdmin(context.Background(), code, email, "Admin "+code, pw)
	if err != nil {
		t.Fatal(err)
	}
	return tn, u
}

func (e env) login(t *testing.T, tenant, email, password string) iam.Session {
	t.Helper()
	s, err := e.iam.Login(context.Background(), iam.LoginInput{TenantCode: tenant, Email: email, Password: password})
	if err != nil {
		t.Fatalf("login %s/%s: %v", tenant, email, err)
	}
	return s
}

// as authenticates a context with an access token, like the HTTP middleware does.
func (e env) as(t *testing.T, s iam.Session) context.Context {
	t.Helper()
	p, err := e.iam.Authenticate(context.Background(), s.AccessToken)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	return auth.WithPrincipal(context.Background(), p)
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.IsCode(err, code) {
		t.Fatalf("got %v, want %s", err, code)
	}
}

func (e env) property(t *testing.T, ctx context.Context, code string) int64 {
	t.Helper()
	p, err := e.tenancy.CreateProperty(ctx, tenancy.CreatePropertyInput{Code: code, OpeningBusinessDate: civil.MustParseDate("2026-09-30"),
		Settings: tenancy.PropertySettings{Name: code, Timezone: "Asia/Jakarta", CurrencyCode: "IDR",
			CheckInTime: civil.MustParseTimeOfDay("14:00"), CheckOutTime: civil.MustParseTimeOfDay("12:00"),
			NightAuditEarliestTime: civil.MustParseTimeOfDay("20:00")}})
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func TestLoginAndAuthenticate(t *testing.T) {
	e := setup(t)
	tn, admin := e.tenantWithAdmin(t, "ABC", "Admin@Hotel.com")
	if admin.Email != "admin@hotel.com" {
		t.Fatalf("email must be stored lower-case, got %q", admin.Email)
	}

	s := e.login(t, " abc ", "ADMIN@hotel.com", pw) // codes and emails are case-insensitive
	if s.TokenType != "Bearer" || s.ExpiresIn != 900 || s.RefreshToken == "" || s.User.ID != admin.ID {
		t.Fatalf("session: %+v", s)
	}
	p, err := e.iam.Authenticate(context.Background(), s.AccessToken)
	if err != nil || p.TenantID != tn.ID || p.UserID != admin.ID || !p.IsTenantAdmin || p.SessionID == 0 {
		t.Fatalf("principal: %v %+v", err, p)
	}

	for name, in := range map[string]iam.LoginInput{
		"wrong password": {TenantCode: "ABC", Email: "admin@hotel.com", Password: pw + "!"},
		"unknown email":  {TenantCode: "ABC", Email: "nobody@hotel.com", Password: pw},
		"unknown tenant": {TenantCode: "NOPE", Email: "admin@hotel.com", Password: pw},
	} {
		_, err := e.iam.Login(context.Background(), in)
		if !apperr.IsCode(err, "INVALID_CREDENTIALS") {
			t.Errorf("%s: got %v (every failure must look identical)", name, err)
		}
	}
	_, err = e.iam.Login(context.Background(), iam.LoginInput{TenantCode: "ABC"})
	wantCode(t, err, "VALIDATION_FAILED")
}

func TestSameEmailInTwoTenants(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	_, xyzAdmin := e.tenantWithAdmin(t, "XYZ", "admin@hotel.com")

	if s := e.login(t, "XYZ", "admin@hotel.com", pw); s.User.ID != xyzAdmin.ID {
		t.Fatal("login resolved the wrong tenant's user")
	}
	if _, err := e.iam.BootstrapAdmin(context.Background(), "ABC", "ADMIN@hotel.com", "Dup", pw); !apperr.IsCode(err, "EMAIL_TAKEN") {
		t.Fatalf("duplicate email in one tenant: %v", err)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	bad := iam.LoginInput{TenantCode: "ABC", Email: "admin@hotel.com", Password: "wrong password!!"}
	for i := 0; i < 5; i++ {
		if _, err := e.iam.Login(context.Background(), bad); !apperr.IsCode(err, "INVALID_CREDENTIALS") {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	// Even the correct password is refused while the account is throttled.
	_, err := e.iam.Login(context.Background(), iam.LoginInput{TenantCode: "ABC", Email: "admin@hotel.com", Password: pw})
	wantCode(t, err, "TOO_MANY_ATTEMPTS")

	e.clock.Advance(15 * time.Minute)
	e.login(t, "ABC", "admin@hotel.com", pw)
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	first := e.login(t, "ABC", "admin@hotel.com", pw)
	other := e.login(t, "ABC", "admin@hotel.com", pw) // a second device

	second, err := e.iam.Refresh(context.Background(), first.RefreshToken, "")
	if err != nil || second.RefreshToken == first.RefreshToken || second.AccessToken == first.AccessToken {
		t.Fatalf("rotation: %v", err)
	}
	// Another tab refreshing with the same cookie a moment later is a race, not theft.
	e.clock.Advance(5 * time.Second)
	_, err = e.iam.Refresh(context.Background(), first.RefreshToken, "")
	wantCode(t, err, "REFRESH_TOKEN_ROTATED")
	e.as(t, second)
	e.as(t, other)
	e.clock.Advance(time.Minute) // outside the grace window a replay is treated as theft
	// The rotated-out session is over immediately, even though its access token has not expired.
	_, err = e.iam.Authenticate(context.Background(), first.AccessToken)
	wantCode(t, err, "SESSION_REVOKED")
	e.as(t, second)

	// Replaying the old refresh token means it was copied: every session of the user ends.
	_, err = e.iam.Refresh(context.Background(), first.RefreshToken, "")
	wantCode(t, err, "REFRESH_TOKEN_REUSED")
	for name, s := range map[string]iam.Session{"rotated session": second, "other device": other} {
		if _, err := e.iam.Authenticate(context.Background(), s.AccessToken); !apperr.IsCode(err, "SESSION_REVOKED") {
			t.Errorf("%s still valid after reuse detection: %v", name, err)
		}
	}
	var n int
	if err := e.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_logs WHERE action = 'auth.refresh_token_reused'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("reuse must be audited: %v %d", err, n)
	}
}

// A stale tab refreshing after a normal logout is not an attack.
func TestRefreshAfterLogoutDoesNotTriggerReuseDetection(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	laptop := e.login(t, "ABC", "admin@hotel.com", pw)
	phone := e.login(t, "ABC", "admin@hotel.com", pw)

	if err := e.iam.Logout(context.Background(), laptop.RefreshToken); err != nil {
		t.Fatal(err)
	}
	_, err := e.iam.Authenticate(context.Background(), laptop.AccessToken)
	wantCode(t, err, "SESSION_REVOKED")
	_, err = e.iam.Refresh(context.Background(), laptop.RefreshToken, "")
	wantCode(t, err, "SESSION_REVOKED")
	e.as(t, phone) // the other device is unaffected
}

func TestRefreshTokenExpiry(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	s := e.login(t, "ABC", "admin@hotel.com", pw)
	e.clock.Advance(721 * time.Hour)
	_, err := e.iam.Refresh(context.Background(), s.RefreshToken, "")
	wantCode(t, err, "SESSION_EXPIRED")
	_, err = e.iam.Refresh(context.Background(), "made-up-token", "")
	wantCode(t, err, "REFRESH_TOKEN_INVALID")
}

func TestUserLifecycleAndImmediateDeactivation(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	adminCtx := e.as(t, e.login(t, "ABC", "admin@hotel.com", pw))

	u, err := e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: "fd@hotel.com", FullName: "Front Desk", Password: pw})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: "not-an-email", FullName: "", Password: "short"})
	wantCode(t, err, "VALIDATION_FAILED")

	fd := e.login(t, "ABC", "fd@hotel.com", pw)
	fdCtx := e.as(t, fd)
	_, err = e.iam.ListUsers(fdCtx, 0, 50)
	wantCode(t, err, "PERMISSION_DENIED")

	inactive := false
	if _, err := e.iam.UpdateUser(adminCtx, u.ID, iam.UserPatch{IsActive: &inactive}); err != nil {
		t.Fatal(err)
	}
	// No need to wait for the 15-minute access token to expire.
	_, err = e.iam.Authenticate(context.Background(), fd.AccessToken)
	wantCode(t, err, "SESSION_REVOKED")
	_, err = e.iam.Login(context.Background(), iam.LoginInput{TenantCode: "ABC", Email: "fd@hotel.com", Password: pw})
	wantCode(t, err, "INVALID_CREDENTIALS")
}

func TestLastAdminIsProtected(t *testing.T) {
	e := setup(t)
	_, admin := e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	ctx := e.as(t, e.login(t, "ABC", "admin@hotel.com", pw))
	no := false
	_, err := e.iam.UpdateUser(ctx, admin.ID, iam.UserPatch{IsTenantAdmin: &no})
	wantCode(t, err, "LAST_ADMIN")
	_, err = e.iam.UpdateUser(ctx, admin.ID, iam.UserPatch{IsActive: &no})
	wantCode(t, err, "LAST_ADMIN")

	yes := true
	second, err := e.iam.CreateUser(ctx, iam.CreateUserInput{Email: "second@hotel.com", FullName: "Second", Password: pw, IsTenantAdmin: yes})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.iam.UpdateUser(ctx, second.ID, iam.UserPatch{IsTenantAdmin: &no}); err != nil {
		t.Fatalf("demoting a non-last admin: %v", err)
	}
}

func TestRolesGrantsAndAuthorization(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	e.tenantWithAdmin(t, "XYZ", "admin@xyz.com")
	adminCtx := e.as(t, e.login(t, "ABC", "admin@hotel.com", pw))
	xyzCtx := e.as(t, e.login(t, "XYZ", "admin@xyz.com", pw))
	bali, jkt := e.property(t, adminCtx, "BALI"), e.property(t, adminCtx, "JKT")
	sg := e.property(t, xyzCtx, "SG")

	name := "Front Desk"
	perms := []auth.Permission{auth.PermFolioRead, auth.PermFolioRead, auth.PermReservationRead}
	role, err := e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &name, Permissions: &perms})
	if err != nil || len(role.Permissions) != 2 {
		t.Fatalf("create role (duplicates removed): %v %+v", err, role)
	}
	bad := []auth.Permission{"folio.delete_everything"}
	_, err = e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &name, Permissions: &bad})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = e.iam.CreateRole(adminCtx, iam.RoleInput{Name: &name, Permissions: &perms})
	wantCode(t, err, "NAME_TAKEN")

	u, err := e.iam.CreateUser(adminCtx, iam.CreateUserInput{Email: "fd@hotel.com", FullName: "Front Desk", Password: pw,
		Grants: []iam.GrantInput{{PropertyID: bali, RoleID: role.ID}}})
	if err != nil || len(u.Grants) != 1 || u.Grants[0].PropertyCode != "BALI" || u.Grants[0].RoleName != "Front Desk" {
		t.Fatalf("create user with grant: %v %+v", err, u)
	}
	_, err = e.iam.ReplaceGrants(adminCtx, u.ID, []iam.GrantInput{{PropertyID: bali, RoleID: role.ID}, {PropertyID: bali, RoleID: role.ID}})
	wantCode(t, err, "VALIDATION_FAILED")
	_, err = e.iam.ReplaceGrants(adminCtx, u.ID, []iam.GrantInput{{PropertyID: sg, RoleID: role.ID}})
	wantCode(t, err, "PROPERTY_NOT_FOUND") // another tenant's property: rejected by the composite FK

	fdCtx := e.as(t, e.login(t, "ABC", "fd@hotel.com", pw))
	if err := e.authz.Require(fdCtx, bali, auth.PermFolioRead); err != nil {
		t.Fatalf("granted permission: %v", err)
	}
	wantCode(t, e.authz.Require(fdCtx, bali, auth.PermPaymentPost), "PERMISSION_DENIED")
	wantCode(t, e.authz.Require(fdCtx, jkt, auth.PermFolioRead), "PROPERTY_NOT_FOUND")
	wantCode(t, e.authz.Require(fdCtx, sg, auth.PermFolioRead), "PROPERTY_NOT_FOUND")
	wantCode(t, e.authz.Require(adminCtx, sg, auth.PermFolioRead), "PROPERTY_NOT_FOUND") // admins stay inside their tenant

	// PropertiesWith: only granted properties with the permission; admins get every property of their tenant.
	if ids, err := e.authz.PropertiesWith(fdCtx, auth.PermFolioRead); err != nil || len(ids) != 1 || ids[0] != bali {
		t.Fatalf("PropertiesWith(folio.read): %v %v", err, ids)
	}
	if ids, err := e.authz.PropertiesWith(fdCtx, auth.PermPaymentPost); err != nil || len(ids) != 0 {
		t.Fatalf("PropertiesWith(payment.post) for a role without it: %v %v", err, ids)
	}
	if ids, err := e.authz.PropertiesWith(adminCtx, auth.PermPaymentPost); err != nil || slices.Contains(ids, sg) || !slices.Contains(ids, bali) || !slices.Contains(ids, jkt) {
		t.Fatalf("PropertiesWith for an admin: %v %v", err, ids)
	}

	// Role changes apply on the next request.
	more := []auth.Permission{auth.PermFolioRead, auth.PermPaymentPost}
	if _, err := e.iam.UpdateRole(adminCtx, role.ID, iam.RoleInput{Permissions: &more}); err != nil {
		t.Fatal(err)
	}
	if err := e.authz.Require(fdCtx, bali, auth.PermPaymentPost); err != nil {
		t.Fatalf("after role update: %v", err)
	}

	me, err := e.iam.Me(fdCtx)
	if err != nil || len(me.Properties) != 1 || me.Properties[0].Code != "BALI" || me.Properties[0].Role != "Front Desk" ||
		len(me.Properties[0].Permissions) != 2 || me.Tenant.Code != "ABC" {
		t.Fatalf("me: %v %+v", err, me)
	}
	adminMe, err := e.iam.Me(adminCtx)
	if err != nil || len(adminMe.Properties) != 2 || len(adminMe.Properties[0].Permissions) != len(auth.Catalogue) {
		t.Fatalf("admin me: %v %+v", err, adminMe)
	}
}

func TestChangePasswordEndsOtherSessions(t *testing.T) {
	e := setup(t)
	e.tenantWithAdmin(t, "ABC", "admin@hotel.com")
	current := e.login(t, "ABC", "admin@hotel.com", pw)
	other := e.login(t, "ABC", "admin@hotel.com", pw)
	ctx := e.as(t, current)

	wantCode(t, e.iam.ChangePassword(ctx, "not my password", "a brand new passphrase"), "VALIDATION_FAILED")
	wantCode(t, e.iam.ChangePassword(ctx, pw, "short"), "VALIDATION_FAILED")
	if err := e.iam.ChangePassword(ctx, pw, "a brand new passphrase"); err != nil {
		t.Fatal(err)
	}
	e.as(t, current) // this device stays signed in
	_, err := e.iam.Authenticate(context.Background(), other.AccessToken)
	wantCode(t, err, "SESSION_REVOKED")
	e.login(t, "ABC", "admin@hotel.com", "a brand new passphrase")
}
