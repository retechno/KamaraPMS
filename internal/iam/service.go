package iam

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam/iamdb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
)

// Service is the identity and access application service.
type Service struct {
	txm      *db.TxManager
	clock    clock.Clock
	audit    *audit.Writer
	tokens   tokens
	accounts *limiter // failed logins per tenant+email
	sources  *limiter // failed logins per client IP
}

// NewService wires the iam service.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, cfg TokenConfig) *Service {
	return &Service{
		txm:      txm,
		clock:    c,
		audit:    a,
		tokens:   tokens{cfg: cfg, clock: c},
		accounts: newLimiter(5, 15*time.Minute, c),
		sources:  newLimiter(50, 15*time.Minute, c),
	}
}

func (s *Service) q(ctx context.Context) *iamdb.Queries { return iamdb.New(s.txm.DB(ctx)) }

// ---------------------------------------------------------------------------
// Authentication

var errInvalidCredentials = apperr.Unauthorized("INVALID_CREDENTIALS", "the tenant code, email or password is incorrect")

func invalidCredentials() error { c := *errInvalidCredentials; return &c }

// LoginInput is a login attempt.
type LoginInput struct {
	TenantCode string
	Email      string
	Password   string
	UserAgent  string
}

// Login verifies credentials and opens a session. Every failure looks the same
// to the caller (no account enumeration), and attempts are rate-limited.
func (s *Service) Login(ctx context.Context, in LoginInput) (Session, error) {
	var fields []apperr.FieldError
	for _, f := range []struct{ name, value string }{{"tenant_code", in.TenantCode}, {"email", in.Email}, {"password", in.Password}} {
		if strings.TrimSpace(f.value) == "" {
			fields = append(fields, apperr.FieldError{Field: f.name, Code: "REQUIRED"})
		}
	}
	if len(fields) > 0 {
		return Session{}, apperr.Invalid("tenant code, email and password are required", fields...)
	}

	tenantCode := strings.ToUpper(strings.TrimSpace(in.TenantCode))
	email := strings.ToLower(strings.TrimSpace(in.Email))
	accountKey := tenantCode + "|" + email
	sourceKey := ""
	if ip, ok := httpx.ClientIPFrom(ctx); ok {
		sourceKey = ip.String()
	}
	for _, l := range []struct {
		lim *limiter
		key string
	}{{s.accounts, accountKey}, {s.sources, sourceKey}} {
		if l.key == "" {
			continue
		}
		if ok, until := l.lim.allowed(l.key); !ok {
			return Session{}, apperr.New(apperr.KindRateLimited, "TOO_MANY_ATTEMPTS", "too many failed sign-in attempts; try again later").
				WithContext("retry_after", until.UTC())
		}
	}

	user, ok, err := s.checkCredentials(ctx, tenantCode, email, in.Password)
	if err != nil {
		return Session{}, err
	}
	if !ok {
		s.accounts.fail(accountKey)
		if sourceKey != "" {
			s.sources.fail(sourceKey)
		}
		logging.FromContext(ctx).Info("login failed", "tenant_code", tenantCode, "email", email)
		return Session{}, invalidCredentials()
	}
	s.accounts.reset(accountKey)

	var out Session
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		sess, err := s.openSession(ctx, user, in.UserAgent)
		if err != nil {
			return err
		}
		if err := s.q(ctx).TouchLastLogin(ctx, iamdb.TouchLastLoginParams{ID: user.ID, At: s.clock.Now()}); err != nil {
			return err
		}
		out = sess
		return s.audit.Write(ctx, audit.Entry{TenantID: user.TenantID, UserID: &user.ID,
			Action: "auth.login", EntityType: "user", EntityID: user.ID})
	})
	return out, err
}

// checkCredentials returns the user if tenant, account and password are all
// valid and active. It always spends one password-hash computation.
func (s *Service) checkCredentials(ctx context.Context, tenantCode, email, password string) (iamdb.User, bool, error) {
	q := s.q(ctx)
	tenant, err := q.GetTenantForLogin(ctx, tenantCode)
	if errors.Is(err, pgx.ErrNoRows) {
		burnPasswordCheck(password)
		return iamdb.User{}, false, nil
	}
	if err != nil {
		return iamdb.User{}, false, err
	}
	user, err := q.GetUserByEmail(ctx, iamdb.GetUserByEmailParams{TenantID: tenant.ID, Email: email})
	if errors.Is(err, pgx.ErrNoRows) {
		burnPasswordCheck(password)
		return iamdb.User{}, false, nil
	}
	if err != nil {
		return iamdb.User{}, false, err
	}
	match, err := VerifyPassword(user.PasswordHash, password)
	if err != nil {
		return iamdb.User{}, false, fmt.Errorf("iam: user %d has an unreadable password hash: %w", user.ID, err)
	}
	return user, match && user.IsActive && tenant.Status == "ACTIVE", nil
}

// openSession creates a session row and issues both tokens (inside a transaction).
func (s *Service) openSession(ctx context.Context, u iamdb.User, userAgent string) (Session, error) {
	refresh, hash, err := newRefreshToken()
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	refreshExp := now.Add(s.tokens.cfg.RefreshTTL)
	var ua *string
	if userAgent != "" {
		ua = &userAgent
		if len(userAgent) > 300 {
			trimmed := userAgent[:300]
			ua = &trimmed
		}
	}
	var ip *netip.Addr
	if addr, ok := httpx.ClientIPFrom(ctx); ok {
		ip = &addr
	}
	sessionID, err := s.q(ctx).CreateSession(ctx, iamdb.CreateSessionParams{
		UserID: u.ID, RefreshTokenHash: hash, ExpiresAt: refreshExp, UserAgent: ua, IpAddress: ip, CreatedAt: now,
	})
	if err != nil {
		return Session{}, err
	}
	access, accessExp, err := s.tokens.issueAccess(u.ID, u.TenantID, sessionID)
	if err != nil {
		return Session{}, err
	}
	return Session{
		AccessToken:      access,
		TokenType:        "Bearer",
		ExpiresIn:        int(s.tokens.cfg.AccessTTL.Seconds()),
		ExpiresAt:        accessExp,
		User:             toUser(u),
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExp,
	}, nil
}

func sessionError(code, msg string) error { return apperr.Unauthorized(code, msg) }

// rotationGrace tolerates concurrent refreshes with the same token (several tabs).
const rotationGrace = 30 * time.Second

// Why a session ended (user_sessions.revoked_reason).
const (
	reasonRotated         = "ROTATED"
	reasonLogout          = "LOGOUT"
	reasonReuseDetected   = "REUSE_DETECTED"
	reasonPasswordChanged = "PASSWORD_CHANGED"
	reasonPasswordReset   = "PASSWORD_RESET"
	reasonDeactivated     = "DEACTIVATED"
	reasonAccountDisabled = "ACCOUNT_DISABLED"
)

// Refresh rotates a refresh token: the presented session is revoked and a new
// one issued. Presenting an already-revoked token means it was copied, so every
// session of that user is revoked (reuse detection).
func (s *Service) Refresh(ctx context.Context, refreshToken, userAgent string) (Session, error) {
	if refreshToken == "" {
		return Session{}, sessionError("REFRESH_TOKEN_MISSING", "no refresh token was presented")
	}
	var (
		out         Session
		reuse       bool
		failureCode string
	)
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		row, err := q.GetSessionByTokenForUpdate(ctx, hashRefreshToken(refreshToken))
		if errors.Is(err, pgx.ErrNoRows) {
			failureCode = "REFRESH_TOKEN_INVALID"
			return nil
		}
		if err != nil {
			return err
		}
		switch {
		case row.RevokedAt != nil && row.RevokedReason != nil && *row.RevokedReason == reasonRotated &&
			now.Sub(*row.RevokedAt) < rotationGrace:
			// Two tabs refreshed with the same cookie at the same moment. Benign: the
			// browser already holds the new cookie from the request that won.
			failureCode = "REFRESH_TOKEN_ROTATED"
			return nil
		case row.RevokedAt != nil && row.RevokedReason != nil && *row.RevokedReason == reasonRotated:
			// A token exchanged earlier is presented again: it was copied.
			reuse = true
			if err := q.RevokeAllUserSessions(ctx, iamdb.RevokeAllUserSessionsParams{UserID: row.UserID, At: now, Reason: reasonReuseDetected}); err != nil {
				return err
			}
			return s.audit.Write(ctx, audit.Entry{TenantID: row.TenantID, UserID: &row.UserID,
				Action: "auth.refresh_token_reused", EntityType: "user", EntityID: row.UserID})
		case row.RevokedAt != nil:
			failureCode = "SESSION_REVOKED" // ended by logout, password change, ...: not an attack
			return nil
		case !now.Before(row.ExpiresAt):
			failureCode = "SESSION_EXPIRED"
			return nil
		case !row.IsActive || row.TenantStatus != "ACTIVE":
			failureCode = "ACCOUNT_DISABLED"
			return q.RevokeSession(ctx, iamdb.RevokeSessionParams{ID: row.ID, At: now, Reason: reasonAccountDisabled})
		}

		if err := q.RevokeSession(ctx, iamdb.RevokeSessionParams{ID: row.ID, At: now, Reason: reasonRotated}); err != nil {
			return err
		}
		u, err := q.GetUser(ctx, iamdb.GetUserParams{TenantID: row.TenantID, ID: row.UserID})
		if err != nil {
			return err
		}
		out, err = s.openSession(ctx, u, userAgent)
		return err
	})
	switch {
	case err != nil:
		return Session{}, err
	case reuse:
		// Committed above: all sessions are revoked before we answer.
		logging.FromContext(ctx).Warn("refresh token reuse detected: all sessions of the user were revoked")
		return Session{}, sessionError("REFRESH_TOKEN_REUSED", "this session was ended for security reasons; please sign in again")
	case failureCode != "":
		return Session{}, sessionError(failureCode, "the session is no longer valid; please sign in again")
	}
	return out, nil
}

// Logout revokes the session identified by the refresh token (preferred: it
// works even when the access token has expired) or by the authenticated caller.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		if refreshToken != "" {
			row, err := q.GetSessionByTokenForUpdate(ctx, hashRefreshToken(refreshToken))
			if err == nil {
				return q.RevokeSession(ctx, iamdb.RevokeSessionParams{ID: row.ID, At: now, Reason: reasonLogout})
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if p, ok := auth.PrincipalFrom(ctx); ok && p.SessionID != 0 {
			return q.RevokeSession(ctx, iamdb.RevokeSessionParams{ID: p.SessionID, At: now, Reason: reasonLogout})
		}
		return nil // logging out an unknown session is not an error
	})
}

// Authenticate turns a bearer token into a Principal. It checks the signature
// and expiry, then the session and account state in the database, so logout and
// deactivation take effect immediately.
func (s *Service) Authenticate(ctx context.Context, bearer string) (auth.Principal, error) {
	userID, claims, err := s.tokens.parseAccess(bearer)
	if err != nil {
		return auth.Principal{}, err
	}
	st, err := s.q(ctx).GetSessionState(ctx, iamdb.GetSessionStateParams{SessionID: claims.SessionID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.Principal{}, sessionError("SESSION_REVOKED", "the session has ended; please sign in again")
	}
	if err != nil {
		return auth.Principal{}, err
	}
	switch {
	case st.TenantID != claims.TenantID:
		return auth.Principal{}, sessionError("TOKEN_INVALID", "the access token is invalid")
	case st.RevokedAt != nil:
		return auth.Principal{}, sessionError("SESSION_REVOKED", "the session has ended; please sign in again")
	case !s.clock.Now().Before(st.ExpiresAt):
		return auth.Principal{}, sessionError("SESSION_EXPIRED", "the session has expired; please sign in again")
	case !st.IsActive || st.TenantStatus != "ACTIVE":
		return auth.Principal{}, sessionError("ACCOUNT_DISABLED", "the account is disabled")
	}
	return auth.Principal{TenantID: st.TenantID, UserID: userID, SessionID: claims.SessionID, IsTenantAdmin: st.IsTenantAdmin}, nil
}

// ChangePassword changes the caller's password and ends their other sessions.
func (s *Service) ChangePassword(ctx context.Context, current, next string) error {
	p, err := auth.Require(ctx)
	if err != nil {
		return err
	}
	if reason := ValidatePassword(next); reason != "" {
		return apperr.Invalid("the new password is too weak", apperr.FieldError{Field: "new_password", Code: "WEAK_PASSWORD", Message: reason})
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		u, err := q.GetUserForUpdate(ctx, iamdb.GetUserForUpdateParams{TenantID: p.TenantID, ID: p.UserID})
		if err != nil {
			return err
		}
		if ok, err := VerifyPassword(u.PasswordHash, current); err != nil || !ok {
			return apperr.Invalid("the current password is incorrect",
				apperr.FieldError{Field: "current_password", Code: "INCORRECT"})
		}
		hash, err := HashPassword(next)
		if err != nil {
			return err
		}
		if err := q.SetUserPassword(ctx, iamdb.SetUserPasswordParams{TenantID: p.TenantID, ID: p.UserID, PasswordHash: hash, ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := q.RevokeOtherSessions(ctx, iamdb.RevokeOtherSessionsParams{UserID: p.UserID, KeepSessionID: p.SessionID, At: s.clock.Now(), Reason: reasonPasswordChanged}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "auth.password_changed", EntityType: "user", EntityID: p.UserID})
	})
}

// Me describes the caller: profile, tenant, and permissions per property.
func (s *Service) Me(ctx context.Context) (Me, error) {
	p, err := auth.Require(ctx)
	if err != nil {
		return Me{}, err
	}
	q := s.q(ctx)
	u, err := q.GetUser(ctx, iamdb.GetUserParams{TenantID: p.TenantID, ID: p.UserID})
	if err != nil {
		return Me{}, err
	}
	t, err := q.GetTenant(ctx, p.TenantID)
	if err != nil {
		return Me{}, err
	}
	me := Me{User: toUser(u), Tenant: MeTenant{ID: t.ID, Code: t.Code, Name: t.Name}, Properties: []MeProperty{}}

	if u.IsTenantAdmin {
		props, err := q.ListTenantProperties(ctx, p.TenantID)
		if err != nil {
			return Me{}, err
		}
		all := make([]auth.Permission, len(auth.Catalogue))
		for i, c := range auth.Catalogue {
			all[i] = c.Code
		}
		for _, pr := range props {
			me.Properties = append(me.Properties, MeProperty{ID: pr.ID, Code: pr.Code, Name: pr.Name, Role: "Tenant administrator", Permissions: all})
		}
		me.User.Grants = []Grant{}
		return me, nil
	}

	grants, err := s.grants(ctx, p.TenantID, p.UserID)
	if err != nil {
		return Me{}, err
	}
	me.User.Grants = grants
	roleIDs := make([]int64, 0, len(grants))
	for _, g := range grants {
		roleIDs = append(roleIDs, g.RoleID)
	}
	perms, err := s.rolePermissions(ctx, roleIDs)
	if err != nil {
		return Me{}, err
	}
	for _, g := range grants {
		me.Properties = append(me.Properties, MeProperty{ID: g.PropertyID, Code: g.PropertyCode, Name: g.PropertyName,
			Role: g.RoleName, Permissions: nonNil(perms[g.RoleID])})
	}
	return me, nil
}

// ---------------------------------------------------------------------------
// Users (tenant administrators only)

// ListUsers pages through the tenant's users.
func (s *Service) ListUsers(ctx context.Context, afterID int64, limit int) ([]User, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListUsers(ctx, iamdb.ListUsersParams{TenantID: p.TenantID, AfterID: afterID, RowLimit: rowLimit(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]User, len(rows))
	for i, r := range rows {
		out[i] = toUser(r)
	}
	return out, nil
}

// GetUser returns one user with their property grants.
func (s *Service) GetUser(ctx context.Context, id int64) (User, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return User{}, err
	}
	row, err := s.q(ctx).GetUser(ctx, iamdb.GetUserParams{TenantID: p.TenantID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, userNotFound()
	}
	if err != nil {
		return User{}, err
	}
	u := toUser(row)
	if u.Grants, err = s.grants(ctx, p.TenantID, id); err != nil {
		return User{}, err
	}
	return u, nil
}

func userNotFound() error { return apperr.NotFound("USER_NOT_FOUND", "the user does not exist") }

// CreateUserInput creates a user.
type CreateUserInput struct {
	Email         string
	FullName      string
	Password      string
	IsTenantAdmin bool
	Grants        []GrantInput
}

// CreateUser adds a user to the caller's tenant.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (User, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return User{}, err
	}
	var fields []apperr.FieldError
	email, fe := normalizeEmail(in.Email)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if fe := validateFullName(in.FullName); fe != nil {
		fields = append(fields, *fe)
	}
	if reason := ValidatePassword(in.Password); reason != "" {
		fields = append(fields, apperr.FieldError{Field: "password", Code: "WEAK_PASSWORD", Message: reason})
	}
	fields = append(fields, validateGrants(in.Grants)...)
	if len(fields) > 0 {
		return User{}, apperr.Invalid("the user is invalid", fields...)
	}
	hash, err := HashPassword(in.Password)
	if err != nil {
		return User{}, err
	}

	var out User
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		row, err := q.CreateUser(ctx, iamdb.CreateUserParams{TenantID: p.TenantID, Email: email, PasswordHash: hash,
			FullName: strings.TrimSpace(in.FullName), IsTenantAdmin: in.IsTenantAdmin, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.writeGrants(ctx, p, row.ID, in.Grants); err != nil {
			return err
		}
		out = toUser(row)
		if out.Grants, err = s.grants(ctx, p.TenantID, row.ID); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "user.created", EntityType: "user", EntityID: row.ID, New: out})
	})
	return out, err
}

// UserPatch changes selected user attributes.
type UserPatch struct {
	FullName      *string
	IsActive      *bool
	IsTenantAdmin *bool
}

// UpdateUser edits a user. The last active tenant administrator can be neither
// deactivated nor demoted (the tenant would become unmanageable). Deactivation
// ends all the user's sessions immediately.
func (s *Service) UpdateUser(ctx context.Context, id int64, patch UserPatch) (User, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return User{}, err
	}
	if patch.FullName != nil {
		if fe := validateFullName(*patch.FullName); fe != nil {
			return User{}, apperr.Invalid("the user is invalid", *fe)
		}
	}
	var out User
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		row, err := q.GetUserForUpdate(ctx, iamdb.GetUserForUpdateParams{TenantID: p.TenantID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return userNotFound()
		}
		if err != nil {
			return err
		}
		before := toUser(row)
		name, active, admin := row.FullName, row.IsActive, row.IsTenantAdmin
		if patch.FullName != nil {
			name = strings.TrimSpace(*patch.FullName)
		}
		if patch.IsActive != nil {
			active = *patch.IsActive
		}
		if patch.IsTenantAdmin != nil {
			admin = *patch.IsTenantAdmin
		}
		if row.IsTenantAdmin && row.IsActive && (!admin || !active) {
			n, err := q.CountActiveAdmins(ctx, p.TenantID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return apperr.Conflict("LAST_ADMIN", "the tenant must keep at least one active administrator")
			}
		}
		updated, err := q.UpdateUser(ctx, iamdb.UpdateUserParams{TenantID: p.TenantID, ID: id, FullName: name,
			IsActive: active, IsTenantAdmin: admin, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if !active && row.IsActive {
			if err := q.RevokeAllUserSessions(ctx, iamdb.RevokeAllUserSessionsParams{UserID: id, At: s.clock.Now(), Reason: reasonDeactivated}); err != nil {
				return err
			}
		}
		out = toUser(updated)
		if out.Grants, err = s.grants(ctx, p.TenantID, id); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "user.updated", EntityType: "user", EntityID: id, Old: before, New: out})
	})
	return out, err
}

// ResetPassword sets a user's password (administrator action) and ends all
// their sessions.
func (s *Service) ResetPassword(ctx context.Context, id int64, password string) error {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return err
	}
	if reason := ValidatePassword(password); reason != "" {
		return apperr.Invalid("the password is too weak", apperr.FieldError{Field: "password", Code: "WEAK_PASSWORD", Message: reason})
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		if _, err := q.GetUserForUpdate(ctx, iamdb.GetUserForUpdateParams{TenantID: p.TenantID, ID: id}); errors.Is(err, pgx.ErrNoRows) {
			return userNotFound()
		} else if err != nil {
			return err
		}
		if err := q.SetUserPassword(ctx, iamdb.SetUserPasswordParams{TenantID: p.TenantID, ID: id, PasswordHash: hash, ActorID: p.ActorID()}); err != nil {
			return err
		}
		if err := q.RevokeAllUserSessions(ctx, iamdb.RevokeAllUserSessionsParams{UserID: id, At: s.clock.Now(), Reason: reasonPasswordReset}); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "user.password_reset", EntityType: "user", EntityID: id})
	})
}

// ReplaceGrants replaces all property grants of a user (one role per property).
func (s *Service) ReplaceGrants(ctx context.Context, userID int64, grants []GrantInput) (User, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return User{}, err
	}
	if fields := validateGrants(grants); len(fields) > 0 {
		return User{}, apperr.Invalid("the grants are invalid", fields...)
	}
	var out User
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		row, err := q.GetUserForUpdate(ctx, iamdb.GetUserForUpdateParams{TenantID: p.TenantID, ID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return userNotFound()
		}
		if err != nil {
			return err
		}
		before, err := s.grants(ctx, p.TenantID, userID)
		if err != nil {
			return err
		}
		if err := q.DeleteUserGrants(ctx, iamdb.DeleteUserGrantsParams{TenantID: p.TenantID, UserID: userID}); err != nil {
			return err
		}
		if err := s.writeGrants(ctx, p, userID, grants); err != nil {
			return err
		}
		out = toUser(row)
		if out.Grants, err = s.grants(ctx, p.TenantID, userID); err != nil {
			return err
		}
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "user.grants_replaced", EntityType: "user", EntityID: userID, Old: before, New: out.Grants})
	})
	return out, err
}

func validateGrants(grants []GrantInput) []apperr.FieldError {
	seen := map[int64]bool{}
	var errs []apperr.FieldError
	for _, g := range grants {
		if g.PropertyID < 1 || g.RoleID < 1 {
			errs = append(errs, apperr.FieldError{Field: "grants", Code: "INVALID_GRANT", Message: "property_id and role_id are required"})
			continue
		}
		if seen[g.PropertyID] {
			errs = append(errs, apperr.FieldError{Field: "grants", Code: "DUPLICATE_PROPERTY_GRANT",
				Message: fmt.Sprintf("property %d is granted more than once (one role per property)", g.PropertyID)})
		}
		seen[g.PropertyID] = true
	}
	return errs
}

func (s *Service) writeGrants(ctx context.Context, p auth.Principal, userID int64, grants []GrantInput) error {
	q := s.q(ctx)
	for _, g := range grants {
		if err := q.InsertUserGrant(ctx, iamdb.InsertUserGrantParams{TenantID: p.TenantID, UserID: userID,
			PropertyID: g.PropertyID, RoleID: g.RoleID, ActorID: p.ActorID()}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) grants(ctx context.Context, tenantID, userID int64) ([]Grant, error) {
	rows, err := s.q(ctx).ListUserGrants(ctx, iamdb.ListUserGrantsParams{TenantID: tenantID, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make([]Grant, len(rows))
	for i, r := range rows {
		out[i] = Grant(r)
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Roles (tenant administrators only)

// ListRoles returns the tenant's roles with their permissions.
func (s *Service) ListRoles(ctx context.Context) ([]Role, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.q(ctx).ListRoles(ctx, p.TenantID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	perms, err := s.rolePermissions(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Role, len(rows))
	for i, r := range rows {
		out[i] = toRole(r, perms[r.ID])
	}
	return out, nil
}

// GetRole returns one role.
func (s *Service) GetRole(ctx context.Context, id int64) (Role, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return Role{}, err
	}
	row, err := s.q(ctx).GetRole(ctx, iamdb.GetRoleParams{TenantID: p.TenantID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, roleNotFound()
	}
	if err != nil {
		return Role{}, err
	}
	perms, err := s.rolePermissions(ctx, []int64{id})
	if err != nil {
		return Role{}, err
	}
	return toRole(row, perms[id]), nil
}

func roleNotFound() error { return apperr.NotFound("ROLE_NOT_FOUND", "the role does not exist") }

// RoleInput creates or (with nil fields left unchanged) updates a role.
type RoleInput struct {
	Name        *string
	Description *string
	Permissions *[]auth.Permission
}

// CreateRole adds a role.
func (s *Service) CreateRole(ctx context.Context, in RoleInput) (Role, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return Role{}, err
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	perms, fields := s.validateRole(name, in.Permissions)
	if len(fields) > 0 {
		return Role{}, apperr.Invalid("the role is invalid", fields...)
	}
	var out Role
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		row, err := s.q(ctx).CreateRole(ctx, iamdb.CreateRoleParams{TenantID: p.TenantID, Name: name,
			Description: trimmedOrNil(in.Description), ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if err := s.setRolePermissions(ctx, row.ID, perms); err != nil {
			return err
		}
		out = toRole(row, perms)
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "role.created", EntityType: "role", EntityID: row.ID, New: out})
	})
	return out, err
}

// UpdateRole edits a role. Permission changes apply to every user holding it
// on their next request (authorization reads role_permissions live).
func (s *Service) UpdateRole(ctx context.Context, id int64, in RoleInput) (Role, error) {
	p, err := auth.RequireTenantAdmin(ctx)
	if err != nil {
		return Role{}, err
	}
	var out Role
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		row, err := q.GetRoleForUpdate(ctx, iamdb.GetRoleForUpdateParams{TenantID: p.TenantID, ID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			return roleNotFound()
		}
		if err != nil {
			return err
		}
		current, err := s.rolePermissions(ctx, []int64{id})
		if err != nil {
			return err
		}
		before := toRole(row, current[id])

		name := row.Name
		if in.Name != nil {
			if row.IsSystem && strings.TrimSpace(*in.Name) != row.Name {
				return apperr.Conflict("SYSTEM_ROLE", "system roles cannot be renamed")
			}
			name = strings.TrimSpace(*in.Name)
		}
		perms := before.Permissions
		if in.Permissions != nil {
			perms = *in.Permissions
		}
		perms, fields := s.validateRole(name, &perms)
		if len(fields) > 0 {
			return apperr.Invalid("the role is invalid", fields...)
		}
		desc := row.Description
		if in.Description != nil {
			desc = trimmedOrNil(in.Description)
		}
		updated, err := q.UpdateRole(ctx, iamdb.UpdateRoleParams{TenantID: p.TenantID, ID: id, Name: name, Description: desc, ActorID: p.ActorID()})
		if err != nil {
			return err
		}
		if in.Permissions != nil {
			if err := s.setRolePermissions(ctx, id, perms); err != nil {
				return err
			}
		}
		out = toRole(updated, perms)
		return s.audit.Write(ctx, audit.Entry{TenantID: p.TenantID, UserID: p.ActorID(),
			Action: "role.updated", EntityType: "role", EntityID: id, Old: before, New: out})
	})
	return out, err
}

func (s *Service) validateRole(name string, perms *[]auth.Permission) ([]auth.Permission, []apperr.FieldError) {
	var fields []apperr.FieldError
	if name == "" || len(name) > 100 {
		fields = append(fields, apperr.FieldError{Field: "name", Code: "REQUIRED", Message: "1-100 characters"})
	}
	var in []auth.Permission
	if perms != nil {
		in = *perms
	}
	out, permErrs := normalizePermissions(in)
	return out, append(fields, permErrs...)
}

func (s *Service) setRolePermissions(ctx context.Context, roleID int64, perms []auth.Permission) error {
	q := s.q(ctx)
	if err := q.DeleteRolePermissions(ctx, roleID); err != nil {
		return err
	}
	for _, p := range perms {
		if err := q.InsertRolePermission(ctx, iamdb.InsertRolePermissionParams{RoleID: roleID, PermissionCode: string(p)}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) rolePermissions(ctx context.Context, roleIDs []int64) (map[int64][]auth.Permission, error) {
	out := map[int64][]auth.Permission{}
	if len(roleIDs) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).ListRolePermissions(ctx, roleIDs)
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.RoleID] = append(out[r.RoleID], auth.Permission(r.PermissionCode))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Bootstrap (operator tooling, no principal)

// BootstrapAdmin creates a tenant administrator directly. It is used by
// cmd/pms-admin to create the first user of a tenant.
func (s *Service) BootstrapAdmin(ctx context.Context, tenantCode, email, fullName, password string) (User, error) {
	var fields []apperr.FieldError
	email, fe := normalizeEmail(email)
	if fe != nil {
		fields = append(fields, *fe)
	}
	if fe := validateFullName(fullName); fe != nil {
		fields = append(fields, *fe)
	}
	if reason := ValidatePassword(password); reason != "" {
		fields = append(fields, apperr.FieldError{Field: "password", Code: "WEAK_PASSWORD", Message: reason})
	}
	if len(fields) > 0 {
		return User{}, apperr.Invalid("the user is invalid", fields...)
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var out User
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		tenant, err := q.GetTenantForLogin(ctx, strings.ToUpper(strings.TrimSpace(tenantCode)))
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("TENANT_NOT_FOUND", "no tenant with this code")
		}
		if err != nil {
			return err
		}
		row, err := q.CreateUser(ctx, iamdb.CreateUserParams{TenantID: tenant.ID, Email: email, PasswordHash: hash,
			FullName: strings.TrimSpace(fullName), IsTenantAdmin: true})
		if err != nil {
			return err
		}
		out = toUser(row)
		out.Grants = []Grant{}
		return s.audit.Write(ctx, audit.Entry{TenantID: tenant.ID, Action: "user.created", EntityType: "user", EntityID: row.ID, New: out})
	})
	return out, err
}

// ---------------------------------------------------------------------------

func toUser(u iamdb.User) User {
	return User{ID: u.ID, Email: u.Email, FullName: u.FullName, IsTenantAdmin: u.IsTenantAdmin, IsActive: u.IsActive,
		LastLoginAt: u.LastLoginAt, CreatedAt: u.CreatedAt, Grants: []Grant{}}
}

func toRole(r iamdb.Role, perms []auth.Permission) Role {
	sorted := append([]auth.Permission{}, perms...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	desc := ""
	if r.Description != nil {
		desc = *r.Description
	}
	return Role{ID: r.ID, Name: r.Name, Description: desc, IsSystem: r.IsSystem, Permissions: sorted, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

func nonNil(p []auth.Permission) []auth.Permission {
	if p == nil {
		return []auth.Permission{}
	}
	return p
}

func trimmedOrNil(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	if t == "" {
		return nil
	}
	return &t
}

// rowLimit bounds a page size for SQL LIMIT.
func rowLimit(n int) int32 {
	switch {
	case n < 1:
		return 1
	case n > 1000:
		return 1000
	}
	return int32(n) //nolint:gosec // G115: bounded to 1..1000 above
}
