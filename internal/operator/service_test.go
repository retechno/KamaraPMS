package operator

import (
	"bytes"
	"context"
	"net/netip"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kamarapms/internal/audit"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
	"kamarapms/internal/platform/clock"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
	"kamarapms/internal/platform/httpx"
)

func TestMain(m *testing.M) { os.Exit(dbtest.RunMain(m)) }

const (
	testEmail    = "ops@example.test"
	testPassword = "a long passphrase for the tests"
)

var (
	testKey    = bytes.Repeat([]byte{9}, KeyLength)
	testSecret = bytes.Repeat([]byte{3}, 32)
)

type env struct {
	pool *pgxpool.Pool
	txm  *db.TxManager
	c    *clock.Fake
	svc  *Service
	aw   *audit.Writer
}

func setup(t *testing.T) env {
	t.Helper()
	pool := dbtest.Pool(t)
	dbtest.Reset(t, pool)
	txm := db.NewTxManager(pool, 5*time.Second)
	c := clock.NewFake(time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC))
	aw := audit.NewWriter(c)
	svc := NewService(txm, c, aw, Config{Secret: testSecret, Key: testKey})
	return env{pool: pool, txm: txm, c: c, svc: svc, aw: aw}
}

var bg = context.Background()

func want(t *testing.T, err error, code string) {
	t.Helper()
	if !apperr.IsCode(err, code) {
		t.Fatalf("want %s, got %v", code, err)
	}
}

// must answers v, or fails the test (by panic: it is called from the goroutine of the test) when err is not nil.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// account creates an operator (as pms-admin does).
func (e env) account(t *testing.T, email string) {
	t.Helper()
	if _, err := e.svc.CreateOperator(bg, email, "Op "+email, testPassword); err != nil {
		t.Fatal(err)
	}
}

// enrolled creates an operator who has enrolled an authenticator, and answers its secret and the session of the enrolment. The clock moves on a step so that the code of the
// enrolment is behind it.
func (e env) enrolled(t *testing.T, email string) ([]byte, Session) {
	t.Helper()
	e.account(t, email)
	ch := must(e.svc.Login(bg, LoginInput{Email: email, Password: testPassword}))
	if ch.Next != "enroll" {
		t.Fatalf("a new account enrols first, next = %s", ch.Next)
	}
	st := must(e.svc.EnrollStart(bg, ch.Token))
	secret := must(secretEncoding.DecodeString(strings.ReplaceAll(st.Secret, " ", "")))
	sess := must(e.svc.EnrollConfirm(bg, EnrollConfirmInput{Challenge: ch.Token, Code: e.code(secret)}))
	e.c.Advance(totpStep * time.Second)
	return secret, sess
}

func (e env) code(secret []byte) string { return totpCode(secret, stepOf(e.c.Now())) }

// signIn does both steps with the authenticator.
func (e env) signIn(t *testing.T, email string, secret []byte) Session {
	t.Helper()
	ch := must(e.svc.Login(bg, LoginInput{Email: email, Password: testPassword}))
	if ch.Next != "totp" {
		t.Fatalf("next = %s, want totp", ch.Next)
	}
	sess := must(e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: e.code(secret)}))
	e.c.Advance(totpStep * time.Second) // the next sign-in needs the next step
	return sess
}

func (e env) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := e.pool.QueryRow(bg, query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEnrolmentEndToEnd(t *testing.T) {
	e := setup(t)
	e.account(t, testEmail)
	ch := must(e.svc.Login(bg, LoginInput{Email: "  OPS@Example.test ", Password: testPassword}))
	if ch.Next != "enroll" || ch.Token == "" || !ch.ExpiresAt.Equal(e.c.Now().Add(ChallengeTTL)) {
		t.Fatalf("challenge: %+v", ch)
	}
	// a challenge that is for enrolling is not for a code
	_, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: "123456"})
	want(t, err, "TOTP_NOT_ENROLLED")
	// confirm before start
	_, err = e.svc.EnrollConfirm(bg, EnrollConfirmInput{Challenge: ch.Token, Code: "123456"})
	want(t, err, "ENROLL_NOT_STARTED")

	first := must(e.svc.EnrollStart(bg, ch.Token))
	second := must(e.svc.EnrollStart(bg, ch.Token)) // replaces the pending secret
	if first.Secret == second.Secret {
		t.Fatal("a second start must make a new secret")
	}
	if !strings.HasPrefix(second.QRPNG, "data:image/png;base64,") || !strings.HasPrefix(second.OtpauthURI, "otpauth://totp/KamaraPMS%20Platform:"+testEmail) {
		t.Fatalf("qr and uri: %q %q", second.QRPNG[:30], second.OtpauthURI)
	}
	oldSecret := must(secretEncoding.DecodeString(strings.ReplaceAll(first.Secret, " ", "")))
	_, err = e.svc.EnrollConfirm(bg, EnrollConfirmInput{Challenge: ch.Token, Code: e.code(oldSecret)})
	want(t, err, "TOTP_INVALID")

	secret := must(secretEncoding.DecodeString(strings.ReplaceAll(second.Secret, " ", "")))
	sess := must(e.svc.EnrollConfirm(bg, EnrollConfirmInput{Challenge: ch.Token, Code: e.code(secret), UserAgent: "test"}))
	if sess.AccessToken == "" || sess.RefreshToken == "" || len(sess.RecoveryCodes) != RecoveryCodeCount || sess.Operator.Email != testEmail || !sess.Operator.TOTPEnrolled {
		t.Fatalf("session: %+v", sess)
	}
	if sess.Operator.RecoveryCodesLeft != RecoveryCodeCount {
		t.Fatalf("codes left: %d", sess.Operator.RecoveryCodesLeft)
	}
	// the secret is stored encrypted; the pending secret and the challenge are used up
	var stored []byte
	if err := e.pool.QueryRow(bg, `SELECT totp_secret_enc FROM operator_accounts`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, secret) || len(stored) == 0 {
		t.Fatal("the secret must not be stored in the clear")
	}
	if got := must(open(testKey, stored)); !bytes.Equal(got, secret) {
		t.Fatal("the stored secret does not open to the secret shown")
	}
	if n := e.count(t, `SELECT count(*) FROM operator_login_challenges WHERE pending_secret_enc IS NOT NULL OR used_at IS NULL`); n != 0 {
		t.Fatalf("%d challenges still hold a secret or can be used", n)
	}
	// the challenge cannot be used again
	_, err = e.svc.EnrollConfirm(bg, EnrollConfirmInput{Challenge: ch.Token, Code: e.code(secret)})
	want(t, err, "CHALLENGE_INVALID")
	// the codes of the enrolment are behind the step of the confirm
	e.c.Advance(totpStep * time.Second)
	// the session works
	ctx := e.operatorCtx(t, sess)
	me := must(e.svc.Me(ctx))
	if me.Email != testEmail || me.RecoveryCodesLeft != RecoveryCodeCount || me.LastLoginAt == nil {
		t.Fatalf("me: %+v", me)
	}
	for _, a := range []string{ActionLoginPasswordOK, ActionTOTPEnrolled, ActionLogin} {
		if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1 AND operator_id IS NOT NULL`, a) != 1 {
			t.Errorf("the operator trail lacks %s", a)
		}
	}
}

// operatorCtx is the context of a request that carries the access token of sess.
func (e env) operatorCtx(t *testing.T, s Session) context.Context {
	t.Helper()
	p := must(e.svc.Authenticate(bg, s.AccessToken))
	return auth.WithOperator(bg, p)
}

func TestLoginFailuresLookAlike(t *testing.T) {
	e := setup(t)
	e.account(t, testEmail)
	e.account(t, "off@example.test")
	if err := e.svc.SetActive(bg, "off@example.test", false); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, in := range []LoginInput{{testEmail, "wrong password"}, {"nobody@example.test", testPassword}, {"off@example.test", testPassword}} {
		_, err := e.svc.Login(bg, in)
		want(t, err, "INVALID_CREDENTIALS")
		ae, _ := apperr.As(err)
		msgs = append(msgs, ae.Message)
	}
	if msgs[0] != msgs[1] || msgs[1] != msgs[2] {
		t.Fatalf("the answers differ, so a valid e-mail can be told: %q", msgs)
	}
	if n := e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1`, ActionLoginFailed); n != 3 {
		t.Fatalf("%d failed sign-ins audited, want 3", n)
	}
	_, err := e.svc.Login(bg, LoginInput{Email: "", Password: ""})
	want(t, err, "VALIDATION_FAILED")
}

func TestSecondFactor(t *testing.T) {
	e := setup(t)
	secret, _ := e.enrolled(t, testEmail)

	// a wrong code counts against the challenge: 5 and it dies, even for the right code
	ch := must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	for i := 1; i <= MaxChallengeAttempts; i++ {
		_, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: "000000"})
		want(t, err, "TOTP_INVALID")
		ae, _ := apperr.As(err)
		if got := ae.Context["attempts_left"]; got != MaxChallengeAttempts-i {
			t.Fatalf("attempt %d: attempts_left = %v", i, got)
		}
	}
	_, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: e.code(secret)})
	want(t, err, "CHALLENGE_INVALID")

	// a code is used once: the same step, and an earlier one, are refused after it
	ch = must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	good := e.code(secret)
	if _, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: good}); err != nil {
		t.Fatal(err)
	}
	ch = must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: good})
	want(t, err, "TOTP_REPLAYED")
	earlier := totpCode(secret, stepOf(e.c.Now())-1)
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: earlier})
	want(t, err, "TOTP_REPLAYED")
	// the next step is accepted (the challenge still has attempts)
	next := totpCode(secret, stepOf(e.c.Now())+1)
	if _, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: next}); err != nil {
		t.Fatalf("the next step must be accepted (window): %v", err)
	}

	// an expired challenge
	e.c.Advance(time.Minute)
	ch = must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	e.c.Advance(ChallengeTTL)
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: totpCode(secret, stepOf(e.c.Now())+1)})
	want(t, err, "CHALLENGE_INVALID")
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: "made-up", Code: "123456"})
	want(t, err, "CHALLENGE_INVALID")
}

func TestRecoveryCodes(t *testing.T) {
	e := setup(t)
	_, first := e.enrolled(t, testEmail)
	code := first.RecoveryCodes[0]
	ch := must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	// lower case, no dashes: the same code
	sess, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, RecoveryCode: strings.ToLower(strings.ReplaceAll(code, "-", ""))})
	if err != nil {
		t.Fatal(err)
	}
	if sess.Operator.RecoveryCodesLeft != RecoveryCodeCount-1 {
		t.Fatalf("codes left: %d", sess.Operator.RecoveryCodesLeft)
	}
	if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1`, ActionRecoveryCodeUsed) != 1 {
		t.Fatal("using a recovery code must be audited")
	}
	// once only
	ch = must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, RecoveryCode: code})
	want(t, err, "TOTP_INVALID")
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, RecoveryCode: "not-a-code"})
	want(t, err, "TOTP_INVALID")
	// the codes are stored as hashes
	if n := e.count(t, `SELECT count(*) FROM operator_recovery_codes WHERE code_hash = $1`, []byte(strings.ReplaceAll(code, "-", ""))); n != 0 {
		t.Fatal("a recovery code is stored in the clear")
	}
}

// Two calls with the same challenge and the same code at the same moment: one session.
func TestSingleUseUnderConcurrency(t *testing.T) {
	e := setup(t)
	secret, _ := e.enrolled(t, testEmail)
	for round := 0; round < 3; round++ {
		ch := must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
		code := e.code(secret)
		var wg sync.WaitGroup
		results := make([]error, 4)
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, results[i] = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: code})
			}()
		}
		wg.Wait()
		ok := 0
		for _, err := range results {
			if err == nil {
				ok++
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d calls got a session, want exactly 1: %v", round, ok, results)
		}
		e.c.Advance(totpStep * time.Second)
	}
	if n := e.count(t, `SELECT count(*) FROM operator_sessions WHERE revoked_at IS NULL`); n != 4 { // the enrolment and the three rounds
		t.Fatalf("%d live sessions", n)
	}
}

func TestRateLimits(t *testing.T) {
	t.Run("failed passwords per e-mail", func(t *testing.T) {
		e := setup(t)
		e.account(t, testEmail)
		for i := 0; i < 5; i++ {
			_, err := e.svc.Login(bg, LoginInput{Email: testEmail, Password: "wrong"})
			want(t, err, "INVALID_CREDENTIALS")
		}
		_, err := e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}) // the right one is refused too
		want(t, err, "TOO_MANY_ATTEMPTS")
		ae, _ := apperr.As(err)
		if _, ok := ae.Context["retry_after"]; !ok {
			t.Fatal("a refusal for too many attempts says when to retry")
		}
		e.c.Advance(15 * time.Minute)
		if _, err := e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}); err != nil {
			t.Fatalf("after the window: %v", err)
		}
	})
	t.Run("failed passwords per address", func(t *testing.T) {
		e := setup(t)
		ctx := httpx.WithClientIP(bg, netip.MustParseAddr("203.0.113.9"))
		for i := 0; i < 50; i++ {
			_, err := e.svc.Login(ctx, LoginInput{Email: "user" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + "@example.test", Password: "wrong"})
			want(t, err, "INVALID_CREDENTIALS")
		}
		_, err := e.svc.Login(ctx, LoginInput{Email: "fresh@example.test", Password: "wrong"})
		want(t, err, "TOO_MANY_ATTEMPTS")
		other := httpx.WithClientIP(bg, netip.MustParseAddr("203.0.113.10"))
		_, err = e.svc.Login(other, LoginInput{Email: "fresh@example.test", Password: "wrong"})
		want(t, err, "INVALID_CREDENTIALS")
	})
	t.Run("wrong codes per operator, across challenges", func(t *testing.T) {
		e := setup(t)
		secret, _ := e.enrolled(t, testEmail)
		wrong := 0
		for wrong < 10 {
			ch := must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
			for i := 0; i < MaxChallengeAttempts && wrong < 10; i++ {
				_, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: "000000"})
				want(t, err, "TOTP_INVALID")
				wrong++
			}
		}
		ch := must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
		_, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: e.code(secret)})
		want(t, err, "TOO_MANY_ATTEMPTS")
	})
}

func TestSessions(t *testing.T) {
	e := setup(t)
	secret, _ := e.enrolled(t, testEmail)
	signedAt := e.c.Now()
	s1 := e.signIn(t, testEmail, secret)

	// rotation: a new session that ends when the first would have (absolute: 12 hours from the sign-in)
	e.c.Advance(time.Hour)
	s2 := must(e.svc.Refresh(bg, s1.RefreshToken, "ua"))
	if s2.RefreshToken == s1.RefreshToken || s2.AccessToken == "" {
		t.Fatal("a refresh makes new tokens")
	}
	if !s2.RefreshExpiresAt.Equal(s1.RefreshExpiresAt) || !s2.RefreshExpiresAt.Before(signedAt.Add(DefaultRefreshTTL+time.Hour)) {
		t.Fatalf("the refresh lifetime is absolute: %v vs %v", s2.RefreshExpiresAt, s1.RefreshExpiresAt)
	}
	// the token presented again within the grace gets a session, after it is theft
	if _, err := e.svc.Refresh(bg, s1.RefreshToken, "ua"); err != nil {
		t.Fatalf("grace replay: %v", err)
	}
	e.c.Advance(RotationGrace + time.Second)
	_, err := e.svc.Refresh(bg, s1.RefreshToken, "ua")
	want(t, err, "REFRESH_TOKEN_REUSED")
	if n := e.count(t, `SELECT count(*) FROM operator_sessions WHERE revoked_at IS NULL`); n != 0 {
		t.Fatalf("%d sessions survive a reuse", n)
	}
	if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1`, ActionRefreshTokenReused) != 1 {
		t.Fatal("a reuse is audited")
	}
	_, err = e.svc.Refresh(bg, s2.RefreshToken, "ua")
	want(t, err, "SESSION_REVOKED")
	_, err = e.svc.Refresh(bg, "", "ua")
	want(t, err, "REFRESH_TOKEN_MISSING")
	_, err = e.svc.Refresh(bg, "unknown", "ua")
	want(t, err, "REFRESH_TOKEN_INVALID")

	// absolute expiry
	s3 := e.signIn(t, testEmail, secret)
	e.c.Advance(DefaultRefreshTTL)
	_, err = e.svc.Refresh(bg, s3.RefreshToken, "ua")
	want(t, err, "SESSION_EXPIRED")
}

func TestAuthenticate(t *testing.T) {
	e := setup(t)
	secret, _ := e.enrolled(t, testEmail)
	s := e.signIn(t, testEmail, secret)
	p, err := e.svc.Authenticate(bg, s.AccessToken)
	if err != nil || p.OperatorID == 0 || p.SessionID == 0 {
		t.Fatalf("a good token: %+v %v", p, err)
	}
	// the token of a hotel user (another issuer, a tenant id, no audience) is refused with its own code
	hotel, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": "kamarapms", "sub": "1", "tid": 1, "sid": 1, "exp": e.c.Now().Add(time.Hour).Unix()}).SignedString(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Authenticate(bg, hotel)
	want(t, err, "PLATFORM_TOKEN_INVALID")
	_, err = e.svc.Authenticate(bg, "garbage")
	want(t, err, "PLATFORM_TOKEN_INVALID")
	// a platform token that is signed with another secret, or that has no audience
	for _, c := range []jwt.MapClaims{
		{"iss": tokenIssuer, "sub": "1", "osid": 1, "exp": e.c.Now().Add(time.Hour).Unix()},
		{"iss": tokenIssuer, "aud": "hotel", "sub": "1", "osid": 1, "exp": e.c.Now().Add(time.Hour).Unix()},
	} {
		raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(testSecret)
		_, err = e.svc.Authenticate(bg, raw)
		want(t, err, "PLATFORM_TOKEN_INVALID")
	}
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"iss": tokenIssuer, "aud": "platform", "sub": "1", "osid": 1, "exp": e.c.Now().Add(time.Hour).Unix()}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	_, err = e.svc.Authenticate(bg, none)
	want(t, err, "PLATFORM_TOKEN_INVALID")

	// expiry of the access token (10 minutes), then logout and a disabled account take effect at once
	e.c.Advance(DefaultAccessTTL + time.Minute)
	_, err = e.svc.Authenticate(bg, s.AccessToken)
	want(t, err, "TOKEN_EXPIRED")

	s = e.signIn(t, testEmail, secret)
	ctx := auth.WithOperator(bg, must(e.svc.Authenticate(bg, s.AccessToken)))
	if err := e.svc.Logout(ctx, ""); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Authenticate(bg, s.AccessToken)
	want(t, err, "SESSION_REVOKED")
	if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1`, ActionLogout) != 1 {
		t.Fatal("a logout is audited")
	}
	if err := e.svc.Logout(bg, "unknown token"); err != nil {
		t.Fatalf("logging out an unknown session is not an error: %v", err)
	}

	s = e.signIn(t, testEmail, secret)
	if err := e.svc.SetActive(bg, testEmail, false); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Authenticate(bg, s.AccessToken)
	want(t, err, "SESSION_REVOKED")
	// and a user's token is not a platform token the other way round: the hotel parser is tested in internal/iam
}

func TestAdminCommands(t *testing.T) {
	e := setup(t)
	e.account(t, testEmail)
	_, err := e.svc.CreateOperator(bg, "OPS@example.test", "Again", testPassword)
	want(t, err, "EMAIL_TAKEN")
	_, err = e.svc.CreateOperator(bg, "short@example.test", "Short", "short")
	want(t, err, "VALIDATION_FAILED")
	_, err = e.svc.CreateOperator(bg, "not an email", "Bad", testPassword)
	want(t, err, "VALIDATION_FAILED")
	_, err = e.svc.CreateOperator(auth.WithOperator(bg, auth.OperatorPrincipal{OperatorID: 1, SessionID: 1}), "x@example.test", "X", testPassword)
	want(t, err, "PERMISSION_DENIED") // an operator does not manage operators
	want(t, e.svc.SetActive(bg, "nobody@example.test", false), "OPERATOR_NOT_FOUND")

	secret, first := e.enrolled(t, "second@example.test")
	// regenerate: the old set dies
	codes := must(e.svc.RegenerateRecovery(bg, "second@example.test"))
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d codes", len(codes))
	}
	ch := must(e.svc.Login(bg, LoginInput{Email: "second@example.test", Password: testPassword}))
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, RecoveryCode: first.RecoveryCodes[0]})
	want(t, err, "TOTP_INVALID")
	if _, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, RecoveryCode: codes[0]}); err != nil {
		t.Fatalf("a code of the new set: %v", err)
	}
	_, err = e.svc.RegenerateRecovery(bg, testEmail) // has no authenticator
	want(t, err, "TOTP_NOT_ENROLLED")

	// reset the password: sessions end, the old password is refused
	s := e.signIn(t, "second@example.test", secret)
	if err := e.svc.ResetPassword(bg, "second@example.test", "another long passphrase"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Authenticate(bg, s.AccessToken)
	want(t, err, "SESSION_REVOKED")
	_, err = e.svc.Login(bg, LoginInput{Email: "second@example.test", Password: testPassword})
	want(t, err, "INVALID_CREDENTIALS")
	ch = must(e.svc.Login(bg, LoginInput{Email: "second@example.test", Password: "another long passphrase"}))
	if ch.Next != "totp" {
		t.Fatal("a password reset keeps the authenticator")
	}

	// reset the authenticator: sessions and codes go, the next sign-in enrols again, the open challenge is dead
	if err := e.svc.ResetTOTP(bg, "second@example.test"); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.VerifyTOTP(bg, TOTPInput{Challenge: ch.Token, Code: e.code(secret)})
	want(t, err, "CHALLENGE_INVALID")
	if e.count(t, `SELECT count(*) FROM operator_recovery_codes`) != 0 {
		t.Fatal("the recovery codes must go with the authenticator")
	}
	ch = must(e.svc.Login(bg, LoginInput{Email: "second@example.test", Password: "another long passphrase"}))
	if ch.Next != "enroll" {
		t.Fatalf("next = %s, want enroll", ch.Next)
	}

	// every command is on the operator's trail, with no operator (pms-admin)
	for _, a := range []string{ActionCreated, ActionPasswordReset, ActionTOTPReset, ActionRecoveryRegenerated, ActionDisabled} {
		if a == ActionDisabled {
			continue
		}
		if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1 AND operator_id IS NULL`, a) == 0 {
			t.Errorf("%s is not on the trail", a)
		}
	}
	list := must(e.svc.ListOperators(bg))
	if len(list) != 2 || list[0].Email != testEmail {
		t.Fatalf("list: %+v", list)
	}
}

func TestRotateKey(t *testing.T) {
	e := setup(t)
	secret, _ := e.enrolled(t, testEmail)
	// a pending secret too
	e.account(t, "pending@example.test")
	ch := must(e.svc.Login(bg, LoginInput{Email: "pending@example.test", Password: testPassword}))
	st := must(e.svc.EnrollStart(bg, ch.Token))
	pending := must(secretEncoding.DecodeString(strings.ReplaceAll(st.Secret, " ", "")))

	newKey := bytes.Repeat([]byte{5}, KeyLength)
	if _, err := e.svc.RotateKey(bg, bytes.Repeat([]byte{1}, KeyLength), newKey); err == nil {
		t.Fatal("a wrong old key must refuse the rotation")
	}
	// nothing changed: the old key still opens
	if _, err := e.svc.VerifyTOTP(bg, TOTPInput{Challenge: must(e.svc.Login(bg, LoginInput{Email: testEmail, Password: testPassword})).Token, Code: e.code(secret)}); err != nil {
		t.Fatalf("after a refused rotation: %v", err)
	}
	e.c.Advance(totpStep * time.Second)

	moved := must(e.svc.RotateKey(bg, testKey, newKey))
	if moved != 2 {
		t.Fatalf("moved %d secrets, want 2 (one account, one pending)", moved)
	}
	// the old key is dead; the new one works for the account and for the pending enrolment
	old := NewService(e.txm, e.c, e.aw, Config{Secret: testSecret, Key: testKey})
	c1 := must(old.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	if _, err := old.VerifyTOTP(bg, TOTPInput{Challenge: c1.Token, Code: e.code(secret)}); err == nil {
		t.Fatal("the old key must not open the rotated secret")
	}
	rotated := NewService(e.txm, e.c, e.aw, Config{Secret: testSecret, Key: newKey})
	c2 := must(rotated.Login(bg, LoginInput{Email: testEmail, Password: testPassword}))
	if _, err := rotated.VerifyTOTP(bg, TOTPInput{Challenge: c2.Token, Code: e.code(secret)}); err != nil {
		t.Fatalf("the new key: %v", err)
	}
	if _, err := rotated.EnrollConfirm(bg, EnrollConfirmInput{Challenge: ch.Token, Code: e.code(pending)}); err != nil {
		t.Fatalf("a pending enrolment survives the rotation: %v", err)
	}
	if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE action = $1`, ActionTOTPKeyRotated) != 1 {
		t.Fatal("a rotation is audited")
	}
}

// A change made by an operator is on the hotel's trail with the operator, and on the operator's trail with the tenant; a user's own change has no operator.
func TestAuditTrailOfAnOperatorsChange(t *testing.T) {
	e := setup(t)
	var tenantID int64
	if err := e.pool.QueryRow(bg, `INSERT INTO tenants (code, name, timezone) VALUES ('ABC', 'ABC Hotels', 'Asia/Jakarta') RETURNING id`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	e.account(t, testEmail)
	var opID int64
	if err := e.pool.QueryRow(bg, `SELECT id FROM operator_accounts`).Scan(&opID); err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithOperator(bg, auth.OperatorPrincipal{OperatorID: opID, SessionID: 1})
	write := func(ctx context.Context, en audit.Entry) {
		t.Helper()
		if err := e.txm.WithinTx(ctx, func(ctx context.Context) error { return e.aw.Write(ctx, en) }); err != nil {
			t.Fatal(err)
		}
	}
	write(ctx, audit.Entry{TenantID: tenantID, Action: "subscription.changed", EntityType: "subscription", EntityID: 7, EntityLabel: "ABC", New: map[string]any{"plan": "PRO"}})
	write(bg, audit.Entry{TenantID: tenantID, Action: "subscription.changed", EntityType: "subscription", EntityID: 8}) // pms-admin or the system
	userID := int64(1)
	write(ctx, audit.Entry{TenantID: tenantID, UserID: &userID, Action: "property.created", EntityType: "property", EntityID: 9}) // a user's change never has an operator

	if e.count(t, `SELECT count(*) FROM audit_logs WHERE operator_id = $1 AND entity_id = 7`, opID) != 1 {
		t.Fatal("the hotel's trail must say that an operator made the change")
	}
	if e.count(t, `SELECT count(*) FROM audit_logs WHERE operator_id IS NOT NULL`) != 1 {
		t.Fatal("only the change of the operator has an operator")
	}
	if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE operator_id = $1 AND tenant_id = $2 AND entity_id = 7 AND new_data ->> 'plan' = 'PRO'`, opID, tenantID) != 1 {
		t.Fatal("the operator's trail must say which tenant the change concerned")
	}
	if e.count(t, `SELECT count(*) FROM operator_audit_logs WHERE entity_id IN (8, 9)`) != 0 {
		t.Fatal("a change without an operator is not on the operator's trail")
	}
	for _, stmt := range []string{`UPDATE operator_audit_logs SET action = 'x'`, `DELETE FROM operator_audit_logs`} {
		if _, err := e.pool.Exec(bg, stmt); err == nil {
			t.Fatalf("%s must be refused: the trail is append-only", stmt)
		}
	}
}

// Two sign-ins of an operator who has not enrolled give two ENROLL challenges, each with its own pending secret. Confirmed at the same moment, exactly one may win: one
// session, one set of 10 recovery codes, and the stored secret is the winner's (the loser must not overwrite it or throw away the codes the winner was shown).
func TestEnrolmentConfirmedTwiceConcurrently(t *testing.T) {
	e := setup(t)
	for round := 0; round < 8; round++ {
		email := "race" + strings.Repeat("x", round) + "@example.test"
		e.account(t, email)
		type attempt struct {
			token  string
			secret []byte
		}
		var tries [2]attempt
		for i := range tries {
			ch := must(e.svc.Login(bg, LoginInput{Email: email, Password: testPassword}))
			if ch.Next != "enroll" {
				t.Fatalf("next = %s", ch.Next)
			}
			st := must(e.svc.EnrollStart(bg, ch.Token))
			tries[i] = attempt{ch.Token, must(secretEncoding.DecodeString(strings.ReplaceAll(st.Secret, " ", "")))}
		}
		var (
			wg       sync.WaitGroup
			start    = make(chan struct{})
			sessions [2]Session
			errs     [2]error
		)
		for i := range tries {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				sessions[i], errs[i] = e.svc.EnrollConfirm(bg, EnrollConfirmInput{Challenge: tries[i].token, Code: e.code(tries[i].secret)})
			}()
		}
		close(start)
		wg.Wait()

		winner := -1
		for i, err := range errs {
			if err == nil {
				if winner >= 0 {
					t.Fatalf("round %d: both confirmations got a session", round)
				}
				winner = i
			}
		}
		if winner < 0 {
			t.Fatalf("round %d: nobody won: %v", round, errs)
		}
		if len(sessions[winner].RecoveryCodes) != 10 {
			t.Fatalf("round %d: the winner holds %d codes", round, len(sessions[winner].RecoveryCodes))
		}
		var opID int64
		var sealed []byte
		if err := e.pool.QueryRow(bg, `SELECT id, totp_secret_enc FROM operator_accounts WHERE lower(email) = $1`, email).Scan(&opID, &sealed); err != nil {
			t.Fatal(err)
		}
		stored, err := open(e.svc.cfg.Key, sealed)
		if err != nil || !bytes.Equal(stored, tries[winner].secret) {
			t.Fatalf("round %d: the stored secret is not the winner's (%v)", round, err)
		}
		if n := e.count(t, `SELECT count(*) FROM operator_sessions WHERE operator_id = $1`, opID); n != 1 {
			t.Fatalf("round %d: %d sessions, want 1", round, n)
		}
		if n := e.count(t, `SELECT count(*) FROM operator_recovery_codes WHERE operator_id = $1 AND used_at IS NULL`, opID); n != 10 {
			t.Fatalf("round %d: %d recovery codes, want 10", round, n)
		}
		e.c.Advance(totpStep * time.Second)
	}
}
