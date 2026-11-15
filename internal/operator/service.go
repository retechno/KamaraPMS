package operator

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/skip2/go-qrcode"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/operator/operatordb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/httpx"
	"kamarapms/internal/platform/logging"
	"kamarapms/internal/platform/loginlimit"
)

// Config configures the operator service.
type Config struct {
	Secret       []byte        // HMAC key of the access tokens: PMS_JWT_SECRET, as the hotel's
	Key          []byte        // AES-256 key of the TOTP secrets: PMS_OPERATOR_TOTP_KEY
	AccessTTL    time.Duration // default 10 minutes, at most 15
	RefreshTTL   time.Duration // default 12 hours (absolute), at most 24
	CookieSecure bool
}

// Service is the operator's identity: sign-in, enrolment, sessions, and the commands of pms-admin.
type Service struct {
	txm    *db.TxManager
	clock  clock.Clock
	audit  *audit.Writer
	cfg    Config
	tokens tokens

	accounts  *loginlimit.Limiter // failed passwords per e-mail
	sources   *loginlimit.Limiter // failed passwords per client address
	operators *loginlimit.Limiter // wrong codes per operator
}

// NewService returns the service. The limits of P10: 5 failed passwords per e-mail and 50 per address in 15 minutes, 10 wrong codes per operator in 15 minutes.
func NewService(txm *db.TxManager, c clock.Clock, a *audit.Writer, cfg Config) *Service {
	if cfg.AccessTTL <= 0 {
		cfg.AccessTTL = DefaultAccessTTL
	}
	if cfg.RefreshTTL <= 0 {
		cfg.RefreshTTL = DefaultRefreshTTL
	}
	return &Service{
		txm: txm, clock: c, audit: a, cfg: cfg,
		tokens:    tokens{secret: cfg.Secret, ttl: cfg.AccessTTL, clock: c},
		accounts:  loginlimit.New(5, 15*time.Minute, c),
		sources:   loginlimit.New(50, 15*time.Minute, c),
		operators: loginlimit.New(10, 15*time.Minute, c),
	}
}

func (s *Service) q(ctx context.Context) *operatordb.Queries { return operatordb.New(s.txm.DB(ctx)) }

// Errors of sign-in. None says whether an e-mail exists.
func invalidCredentials() *apperr.Error {
	return apperr.Unauthorized("INVALID_CREDENTIALS", "the email or password is incorrect")
}

func challengeInvalid() *apperr.Error {
	return apperr.Unauthorized("CHALLENGE_INVALID", "the sign-in step has expired or was used; sign in again")
}

func tooManyAttempts(until time.Time) *apperr.Error {
	return apperr.New(apperr.KindRateLimited, "TOO_MANY_ATTEMPTS", "too many failed attempts; try again later").WithContext("retry_after", until.UTC())
}

func sessionError(code, msg string) *apperr.Error { return apperr.Unauthorized(code, msg) }

// burn spends the time of a real password check when the account does not exist, so that the answer time does not tell a valid e-mail from another.
var (
	dummyOnce sync.Once
	dummyHash string
)

func burn(password string) {
	dummyOnce.Do(func() { dummyHash, _ = iam.HashPassword("timing-equaliser-not-a-real-password") })
	_, _ = iam.VerifyPassword(dummyHash, password)
}

// ---------------------------------------------------------------------------
// Step 1: the password

// LoginInput is the first step of a sign-in.
type LoginInput struct {
	Email    string
	Password string
}

// Challenge is the proof that the password was right, to be exchanged for a session after the second step.
type Challenge struct {
	Token     string    `json:"challenge"`
	Next      string    `json:"next"` // "totp" (the account has an authenticator) or "enroll"
	ExpiresAt time.Time `json:"expires_at"`
}

// Login checks the password and answers a challenge. Every failure looks the same, and attempts are limited per e-mail and per address.
func (s *Service) Login(ctx context.Context, in LoginInput) (Challenge, error) {
	var fields []apperr.FieldError
	for _, f := range []struct{ name, value string }{{"email", in.Email}, {"password", in.Password}} {
		if strings.TrimSpace(f.value) == "" {
			fields = append(fields, apperr.FieldError{Field: f.name, Code: "REQUIRED"})
		}
	}
	if len(fields) > 0 {
		return Challenge{}, apperr.Invalid("email and password are required", fields...)
	}
	email := NormaliseEmail(in.Email)
	sourceKey := ""
	if ip, ok := httpx.ClientIPFrom(ctx); ok {
		sourceKey = ip.String()
	}
	for _, l := range []struct {
		lim *loginlimit.Limiter
		key string
	}{{s.accounts, email}, {s.sources, sourceKey}} {
		if l.key == "" {
			continue
		}
		if ok, until := l.lim.Allowed(l.key); !ok {
			return Challenge{}, tooManyAttempts(until)
		}
	}

	op, err := s.q(ctx).GetOperatorByEmail(ctx, email)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Challenge{}, err
	}
	ok := false
	if found {
		match, err := iam.VerifyPassword(op.PasswordHash, in.Password)
		if err != nil {
			return Challenge{}, fmt.Errorf("operator: account %d has an unreadable password hash: %w", op.ID, err)
		}
		ok = match && op.IsActive
	} else {
		burn(in.Password)
	}
	if !ok {
		s.accounts.Fail(email)
		if sourceKey != "" {
			s.sources.Fail(sourceKey)
		}
		logging.FromContext(ctx).Info("operator login failed", "email", email)
		var id int64
		var opID *int64
		if found {
			id, opID = op.ID, &op.ID
		}
		if err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
			return s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: nil, Action: ActionLoginFailed, EntityType: "operator", EntityID: id, EntityLabel: email, New: map[string]any{"known": opID != nil}})
		}); err != nil {
			return Challenge{}, err
		}
		return Challenge{}, invalidCredentials()
	}
	s.accounts.Reset(email)

	var out Challenge
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		now := s.clock.Now()
		purpose, next := PurposeVerify, "totp"
		if op.TotpEnrolledAt == nil {
			purpose, next = PurposeEnroll, "enroll"
		}
		token, hash, err := newToken()
		if err != nil {
			return err
		}
		exp := now.Add(ChallengeTTL)
		q := s.q(ctx)
		if _, err := q.CreateChallenge(ctx, operatordb.CreateChallengeParams{OperatorID: op.ID, TokenHash: hash, Purpose: purpose, ExpiresAt: exp, At: now}); err != nil {
			return err
		}
		_ = q.DeleteOldChallenges(ctx, now.Add(-time.Hour)) // the dead ones go; a failure here is not a reason to refuse a sign-in
		out = Challenge{Token: token, Next: next, ExpiresAt: exp}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &op.ID, Action: ActionLoginPasswordOK, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
	return out, err
}

// ---------------------------------------------------------------------------
// Step 2: the second factor

// Session is what a successful second step gives: the tokens, and the operator.
type Session struct {
	AccessToken      string    `json:"access_token"`
	TokenType        string    `json:"token_type"`
	ExpiresIn        int       `json:"expires_in"`
	ExpiresAt        time.Time `json:"-"`
	Operator         Operator  `json:"operator"`
	RecoveryCodes    []string  `json:"recovery_codes,omitempty"` // only when the authenticator was just enrolled
	RefreshToken     string    `json:"-"`
	RefreshExpiresAt time.Time `json:"-"`
}

// TOTPInput is the second step of a sign-in: a code of the authenticator, or a recovery code.
type TOTPInput struct {
	Challenge    string
	Code         string
	RecoveryCode string
	UserAgent    string
}

// VerifyTOTP exchanges a challenge and a code for a session. A wrong code counts against the challenge (5, then it dies), a used step is refused, and a recovery code works once.
func (s *Service) VerifyTOTP(ctx context.Context, in TOTPInput) (Session, error) {
	if strings.TrimSpace(in.Challenge) == "" {
		return Session{}, apperr.Invalid("the challenge is required", apperr.FieldError{Field: "challenge", Code: "REQUIRED"})
	}
	useRecovery := strings.TrimSpace(in.RecoveryCode) != ""
	if !useRecovery && strings.TrimSpace(in.Code) == "" {
		return Session{}, apperr.Invalid("a code or a recovery code is required", apperr.FieldError{Field: "code", Code: "REQUIRED"})
	}

	var (
		out     Session
		failure *apperr.Error
	)
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		ch, err := q.GetChallengeForUpdate(ctx, hashToken(in.Challenge))
		if errors.Is(err, pgx.ErrNoRows) {
			failure = challengeInvalid()
			return nil
		}
		if err != nil {
			return err
		}
		if ch.UsedAt != nil || !now.Before(ch.ExpiresAt) {
			failure = challengeInvalid()
			return nil
		}
		if ch.Purpose != PurposeVerify {
			failure = apperr.Conflict("TOTP_NOT_ENROLLED", "this account has no authenticator yet: enrol one first")
			return nil
		}
		op, err := q.GetOperator(ctx, ch.OperatorID)
		if err != nil {
			return err
		}
		if !op.IsActive || op.TotpSecretEnc == nil {
			if err := q.KillChallenge(ctx, operatordb.KillChallengeParams{ID: ch.ID, At: now}); err != nil {
				return err
			}
			failure = challengeInvalid()
			return nil
		}
		opKey := strconv.FormatInt(op.ID, 10)
		if ok, until := s.operators.Allowed(opKey); !ok {
			failure = tooManyAttempts(until)
			return nil
		}

		good, replayed, usedRecovery := false, false, false
		if useRecovery {
			if norm, valid := NormaliseRecoveryCode(in.RecoveryCode); valid {
				n, err := q.UseRecoveryCode(ctx, operatordb.UseRecoveryCodeParams{OperatorID: op.ID, CodeHash: HashRecoveryCode(norm), At: now})
				if err != nil {
					return err
				}
				good, usedRecovery = n == 1, n == 1
			}
		} else {
			secret, err := open(s.cfg.Key, op.TotpSecretEnc)
			if err != nil {
				return err
			}
			if step, ok := matchTOTP(secret, in.Code, now); ok {
				n, err := q.AdvanceOperatorTotpStep(ctx, operatordb.AdvanceOperatorTotpStepParams{ID: op.ID, Step: step})
				if err != nil {
					return err
				}
				good, replayed = n == 1, n == 0
			}
		}
		if !good {
			s.operators.Fail(opKey)
			attempts, err := q.CountChallengeAttempt(ctx, ch.ID)
			if err != nil {
				return err
			}
			if int(attempts) >= MaxChallengeAttempts {
				if err := q.KillChallenge(ctx, operatordb.KillChallengeParams{ID: ch.ID, At: now}); err != nil {
					return err
				}
			}
			code := "TOTP_INVALID"
			if replayed {
				code = "TOTP_REPLAYED"
			}
			failure = apperr.Unauthorized(code, "the code is not valid").WithContext("attempts_left", max(0, MaxChallengeAttempts-int(attempts)))
			return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionLoginFailed, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email, New: map[string]any{"step": "second factor", "recovery_code": useRecovery}})
		}

		s.operators.Reset(opKey)
		if err := q.UseChallenge(ctx, operatordb.UseChallengeParams{ID: ch.ID, At: now}); err != nil {
			return err
		}
		sess, err := s.openSession(ctx, op, in.UserAgent, time.Time{})
		if err != nil {
			return err
		}
		if err := q.TouchOperatorLastLogin(ctx, operatordb.TouchOperatorLastLoginParams{ID: op.ID, At: now}); err != nil {
			return err
		}
		if usedRecovery {
			if err := s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &op.ID, Action: ActionRecoveryCodeUsed, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email}); err != nil {
				return err
			}
		}
		out = sess
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &op.ID, Action: ActionLogin, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
	switch {
	case err != nil:
		return Session{}, err
	case failure != nil:
		return Session{}, failure
	}
	return out, nil
}

// EnrollStarted is what the operator scans or types into an authenticator.
type EnrollStarted struct {
	Secret     string `json:"secret"`      // grouped by 4
	OtpauthURI string `json:"otpauth_uri"` //nolint:revive // the name of the field of the API
	QRPNG      string `json:"qr_png"`      // a data URI
}

// EnrollStart makes the secret of an operator who has no authenticator yet and keeps it, encrypted, on the challenge until it is confirmed. Calling it again replaces the pending secret.
func (s *Service) EnrollStart(ctx context.Context, challenge string) (EnrollStarted, error) {
	var (
		out     EnrollStarted
		failure *apperr.Error
	)
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		ch, op, f, err := s.enrolmentChallenge(ctx, challenge)
		if err != nil || f != nil {
			failure = f
			return err
		}
		secret, err := newSecret()
		if err != nil {
			return err
		}
		sealed, err := seal(s.cfg.Key, secret)
		if err != nil {
			return err
		}
		if err := s.q(ctx).SetChallengePendingSecret(ctx, operatordb.SetChallengePendingSecretParams{ID: ch.ID, SecretEnc: sealed}); err != nil {
			return err
		}
		uri := otpauthURI(op.Email, secret)
		png, err := qrcode.Encode(uri, qrcode.Medium, 256)
		if err != nil {
			return fmt.Errorf("operator: qr code: %w", err)
		}
		out = EnrollStarted{Secret: groupedSecret(secret), OtpauthURI: uri, QRPNG: "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)}
		return nil
	})
	if err != nil {
		return EnrollStarted{}, err
	}
	if failure != nil {
		return EnrollStarted{}, failure
	}
	return out, nil
}

// enrolmentChallenge locks a challenge of an operator who has to enrol and answers it with the account; failure is what to answer when it is not good.
func (s *Service) enrolmentChallenge(ctx context.Context, token string) (operatordb.OperatorLoginChallenge, operatordb.OperatorAccount, *apperr.Error, error) {
	q := s.q(ctx)
	ch, err := q.GetChallengeForUpdate(ctx, hashToken(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return ch, operatordb.OperatorAccount{}, challengeInvalid(), nil
	}
	if err != nil {
		return ch, operatordb.OperatorAccount{}, nil, err
	}
	if ch.UsedAt != nil || !s.clock.Now().Before(ch.ExpiresAt) {
		return ch, operatordb.OperatorAccount{}, challengeInvalid(), nil
	}
	if ch.Purpose != PurposeEnroll {
		return ch, operatordb.OperatorAccount{}, apperr.Conflict("TOTP_ALREADY_ENROLLED", "this account has an authenticator already"), nil
	}
	// The account row is locked: two challenges of one operator are answered one after the other, and the second sees the authenticator of the first.
	op, err := q.GetOperatorForUpdate(ctx, ch.OperatorID)
	if err != nil {
		return ch, op, nil, err
	}
	if !op.IsActive || op.TotpSecretEnc != nil {
		return ch, op, challengeInvalid(), nil
	}
	return ch, op, nil, nil
}

// EnrollConfirmInput confirms the enrolment with the first code of the new authenticator.
type EnrollConfirmInput struct {
	Challenge string
	Code      string
	UserAgent string
}

// EnrollConfirm checks the first code against the pending secret, keeps the secret, makes the 10 recovery codes (shown once, in the answer) and opens the session.
func (s *Service) EnrollConfirm(ctx context.Context, in EnrollConfirmInput) (Session, error) {
	if strings.TrimSpace(in.Challenge) == "" || strings.TrimSpace(in.Code) == "" {
		return Session{}, apperr.Invalid("the challenge and the code are required", apperr.FieldError{Field: "code", Code: "REQUIRED"})
	}
	var (
		out     Session
		failure *apperr.Error
	)
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		ch, op, f, err := s.enrolmentChallenge(ctx, in.Challenge)
		if err != nil || f != nil {
			failure = f
			return err
		}
		if ch.PendingSecretEnc == nil {
			failure = apperr.Conflict("ENROLL_NOT_STARTED", "start the enrolment first")
			return nil
		}
		opKey := strconv.FormatInt(op.ID, 10)
		if ok, until := s.operators.Allowed(opKey); !ok {
			failure = tooManyAttempts(until)
			return nil
		}
		secret, err := open(s.cfg.Key, ch.PendingSecretEnc)
		if err != nil {
			return err
		}
		step, ok := matchTOTP(secret, in.Code, now)
		if !ok {
			s.operators.Fail(opKey)
			attempts, err := q.CountChallengeAttempt(ctx, ch.ID)
			if err != nil {
				return err
			}
			if int(attempts) >= MaxChallengeAttempts {
				if err := q.KillChallenge(ctx, operatordb.KillChallengeParams{ID: ch.ID, At: now}); err != nil {
					return err
				}
			}
			failure = apperr.Unauthorized("TOTP_INVALID", "the code is not valid").WithContext("attempts_left", max(0, MaxChallengeAttempts-int(attempts)))
			return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionLoginFailed, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email, New: map[string]any{"step": "enrolment"}})
		}
		s.operators.Reset(opKey)
		n, err := q.SetOperatorTotp(ctx, operatordb.SetOperatorTotpParams{ID: op.ID, SecretEnc: ch.PendingSecretEnc, At: now, Step: step})
		if err != nil {
			return err
		}
		if n == 0 { // an authenticator was kept meanwhile: this confirmation lost
			failure = challengeInvalid()
			return nil
		}
		if err := q.UseChallenge(ctx, operatordb.UseChallengeParams{ID: ch.ID, At: now}); err != nil {
			return err
		}
		codes, err := s.newRecoveryCodes(ctx, op.ID)
		if err != nil {
			return err
		}
		op.TotpEnrolledAt = &now
		sess, err := s.openSession(ctx, op, in.UserAgent, time.Time{})
		if err != nil {
			return err
		}
		sess.RecoveryCodes = codes
		sess.Operator.RecoveryCodesLeft = len(codes)
		if err := q.TouchOperatorLastLogin(ctx, operatordb.TouchOperatorLastLoginParams{ID: op.ID, At: now}); err != nil {
			return err
		}
		if err := s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &op.ID, Action: ActionTOTPEnrolled, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email}); err != nil {
			return err
		}
		out = sess
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &op.ID, Action: ActionLogin, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
	switch {
	case err != nil:
		return Session{}, err
	case failure != nil:
		return Session{}, failure
	}
	return out, nil
}

// newRecoveryCodes replaces the recovery codes of an operator with a new set and answers it in the shown form. Only the hashes are kept.
func (s *Service) newRecoveryCodes(ctx context.Context, operatorID int64) ([]string, error) {
	q := s.q(ctx)
	now := s.clock.Now()
	if err := q.DeleteRecoveryCodes(ctx, operatorID); err != nil {
		return nil, err
	}
	codes := make([]string, 0, RecoveryCodeCount)
	seen := map[string]bool{}
	for len(codes) < RecoveryCodeCount {
		c, err := NewRecoveryCode()
		if err != nil {
			return nil, err
		}
		norm, _ := NormaliseRecoveryCode(c)
		if seen[norm] {
			continue // drawn twice in one set: draw another (a unique violation would abort the transaction)
		}
		seen[norm] = true
		if err := q.InsertRecoveryCode(ctx, operatordb.InsertRecoveryCodeParams{OperatorID: operatorID, CodeHash: HashRecoveryCode(norm), At: now}); err != nil {
			return nil, err
		}
		codes = append(codes, c)
	}
	return codes, nil
}

// ---------------------------------------------------------------------------
// Sessions

// openSession creates a session row and issues both tokens (inside a transaction). A zero refreshExp is a sign-in: the refresh token lives RefreshTTL from now.
func (s *Service) openSession(ctx context.Context, op operatordb.OperatorAccount, userAgent string, refreshExp time.Time) (Session, error) {
	refresh, hash, err := newToken()
	if err != nil {
		return Session{}, err
	}
	now := s.clock.Now()
	if refreshExp.IsZero() {
		refreshExp = now.Add(s.cfg.RefreshTTL)
	}
	var ua *string
	if userAgent != "" {
		u := userAgent
		if len(u) > 300 {
			u = u[:300]
		}
		ua = &u
	}
	var ip *netip.Addr
	if addr, ok := httpx.ClientIPFrom(ctx); ok {
		ip = &addr
	}
	id, err := s.q(ctx).CreateSession(ctx, operatordb.CreateSessionParams{OperatorID: op.ID, RefreshTokenHash: hash, ExpiresAt: refreshExp, UserAgent: ua, IpAddress: ip, CreatedAt: now})
	if err != nil {
		return Session{}, err
	}
	access, accessExp, err := s.tokens.issueAccess(op.ID, id)
	if err != nil {
		return Session{}, err
	}
	left, err := s.q(ctx).CountRecoveryCodesLeft(ctx, op.ID)
	if err != nil {
		return Session{}, err
	}
	return Session{
		AccessToken: access, TokenType: "Bearer", ExpiresIn: int(s.cfg.AccessTTL.Seconds()), ExpiresAt: accessExp,
		Operator:     toOperator(op, int(left)),
		RefreshToken: refresh, RefreshExpiresAt: refreshExp,
	}, nil
}

// Refresh rotates a refresh token: the session presented is revoked and a new one opened. The refresh lifetime is absolute (P7): it is counted from the sign-in with the second factor, so
// the new session ends when the old one would have ended and the operator signs in again with a code every working day. Presenting a token that was rotated earlier means it was
// copied, so every session of the operator ends (reuse detection).
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
		row, err := q.GetSessionByTokenForUpdate(ctx, hashToken(refreshToken))
		if errors.Is(err, pgx.ErrNoRows) {
			failureCode = "REFRESH_TOKEN_INVALID"
			return nil
		}
		if err != nil {
			return err
		}
		rotated := row.RevokedAt != nil && row.RevokedReason != nil && *row.RevokedReason == ReasonRotated
		graceReplay := rotated && now.Sub(*row.RevokedAt) < RotationGrace
		if graceReplay {
			alive, err := q.HasLiveSessionSince(ctx, operatordb.HasLiveSessionSinceParams{OperatorID: row.OperatorID, Since: *row.RevokedAt})
			if err != nil {
				return err
			}
			if !alive {
				failureCode = "SESSION_REVOKED"
				return nil
			}
		}
		switch {
		case graceReplay:
			// checked below like a live session; it is revoked already
		case rotated:
			reuse = true
			if err := q.RevokeAllOperatorSessions(ctx, operatordb.RevokeAllOperatorSessionsParams{OperatorID: row.OperatorID, At: now, Reason: reason(ReasonReuseDetected)}); err != nil {
				return err
			}
			return s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &row.OperatorID, Action: ActionRefreshTokenReused, EntityType: "operator", EntityID: row.OperatorID})
		case row.RevokedAt != nil:
			failureCode = "SESSION_REVOKED" // ended by logout, a reset, ...: not an attack
			return nil
		}
		switch {
		case !now.Before(row.ExpiresAt):
			failureCode = "SESSION_EXPIRED"
			return nil
		case !row.IsActive || row.TotpEnrolledAt == nil:
			failureCode = "ACCOUNT_DISABLED"
			return nil
		}
		if !graceReplay {
			if err := q.RevokeSession(ctx, operatordb.RevokeSessionParams{ID: row.ID, At: now, Reason: reason(ReasonRotated)}); err != nil {
				return err
			}
		}
		op, err := q.GetOperator(ctx, row.OperatorID)
		if err != nil {
			return err
		}
		// 12 hours absolute (P7): the session that replaces this one ends when this one would have ended.
		out, err = s.openSession(ctx, op, userAgent, row.ExpiresAt)
		return err
	})
	switch {
	case err != nil:
		return Session{}, err
	case reuse:
		logging.FromContext(ctx).Warn("operator refresh token reuse detected: all sessions of the operator were revoked")
		return Session{}, sessionError("REFRESH_TOKEN_REUSED", "this session was ended for security reasons; please sign in again")
	case failureCode != "":
		return Session{}, sessionError(failureCode, "the session is no longer valid; please sign in again")
	}
	return out, nil
}

// Logout revokes the session of the refresh token (preferred: it works when the access token has expired) or of the authenticated caller.
func (s *Service) Logout(ctx context.Context, refreshToken string) error {
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		now := s.clock.Now()
		var opID, sessionID int64
		if refreshToken != "" {
			row, err := q.GetSessionByTokenForUpdate(ctx, hashToken(refreshToken))
			if err == nil {
				opID, sessionID = row.OperatorID, row.ID
			} else if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
		}
		if sessionID == 0 {
			if p, ok := auth.OperatorFrom(ctx); ok {
				opID, sessionID = p.OperatorID, p.SessionID
			}
		}
		if sessionID == 0 {
			return nil // logging out an unknown session is not an error
		}
		if err := q.RevokeSession(ctx, operatordb.RevokeSessionParams{ID: sessionID, At: now, Reason: reason(ReasonLogout)}); err != nil {
			return err
		}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{OperatorID: &opID, Action: ActionLogout, EntityType: "operator", EntityID: opID})
	})
}

// Authenticate turns a bearer token into an operator principal. It checks the signature, the issuer, the audience and the expiry, then the session and the account in the database
// (one query), so logout, deactivation and a reset take effect at once.
func (s *Service) Authenticate(ctx context.Context, bearer string) (auth.OperatorPrincipal, error) {
	operatorID, claims, err := s.tokens.parseAccess(bearer)
	if err != nil {
		return auth.OperatorPrincipal{}, err
	}
	st, err := s.q(ctx).GetSessionState(ctx, operatordb.GetSessionStateParams{SessionID: claims.SessionID, OperatorID: operatorID})
	if errors.Is(err, pgx.ErrNoRows) {
		return auth.OperatorPrincipal{}, sessionError("SESSION_REVOKED", "the session has ended; please sign in again")
	}
	if err != nil {
		return auth.OperatorPrincipal{}, err
	}
	switch {
	case st.RevokedAt != nil:
		return auth.OperatorPrincipal{}, sessionError("SESSION_REVOKED", "the session has ended; please sign in again")
	case !s.clock.Now().Before(st.ExpiresAt):
		return auth.OperatorPrincipal{}, sessionError("SESSION_EXPIRED", "the session has expired; please sign in again")
	case !st.IsActive || st.TotpEnrolledAt == nil:
		return auth.OperatorPrincipal{}, sessionError("ACCOUNT_DISABLED", "the account is disabled")
	}
	return auth.OperatorPrincipal{OperatorID: operatorID, SessionID: claims.SessionID}, nil
}

// Me is the operator who makes the call.
func (s *Service) Me(ctx context.Context) (Operator, error) {
	p, ok := auth.OperatorFrom(ctx)
	if !ok {
		return Operator{}, apperr.Unauthorized("PLATFORM_TOKEN_INVALID", "an operator token is required")
	}
	op, err := s.q(ctx).GetOperator(ctx, p.OperatorID)
	if err != nil {
		return Operator{}, err
	}
	left, err := s.q(ctx).CountRecoveryCodesLeft(ctx, p.OperatorID)
	if err != nil {
		return Operator{}, err
	}
	return toOperator(op, int(left)), nil
}

func toOperator(a operatordb.OperatorAccount, left int) Operator {
	return Operator{ID: a.ID, Email: a.Email, FullName: a.FullName, IsActive: a.IsActive, TOTPEnrolled: a.TotpEnrolledAt != nil, LastLoginAt: a.LastLoginAt, RecoveryCodesLeft: left}
}

func reason(r string) *string { return &r }
