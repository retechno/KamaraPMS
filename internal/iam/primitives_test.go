package iam

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/clock"
)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery staple"); err != nil || !ok {
		t.Fatalf("correct password rejected: %v", err)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery stapl"); ok {
		t.Fatal("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery staple")
	if h == h2 {
		t.Fatal("hashes must be salted")
	}
	for _, bad := range []string{"", "plain", "$bcrypt$x", "$argon2id$v=19$m=1,t=1,p=1$!!$!!"} {
		if _, err := VerifyPassword(bad, "x"); err == nil {
			t.Errorf("malformed hash %q accepted", bad)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	if ValidatePassword("short") == "" || ValidatePassword(strings.Repeat("x", 129)) == "" {
		t.Fatal("length limits not enforced")
	}
	if ValidatePassword("twelve chars") != "" || ValidatePassword("ñandú pingüino ¡sí!") != "" {
		t.Fatal("valid passphrases rejected")
	}
}

func newTokens(c clock.Clock) tokens {
	return tokens{cfg: TokenConfig{Secret: []byte(strings.Repeat("k", 32)), AccessTTL: 15 * time.Minute}, clock: c}
}

func TestAccessTokenRoundTripAndExpiry(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	tk := newTokens(c)
	raw, exp, err := tk.issueAccess(7, 3, 11)
	if err != nil || !exp.Equal(c.Now().Add(15*time.Minute)) {
		t.Fatalf("issue: %v %v", err, exp)
	}
	uid, claims, err := tk.parseAccess(raw)
	if err != nil || uid != 7 || claims.TenantID != 3 || claims.SessionID != 11 {
		t.Fatalf("parse: %v %d %+v", err, uid, claims)
	}

	c.Advance(15*time.Minute + 29*time.Second) // within the 30s leeway
	if _, _, err := tk.parseAccess(raw); err != nil {
		t.Fatalf("leeway: %v", err)
	}
	c.Advance(2 * time.Second)
	if _, _, err := tk.parseAccess(raw); !apperr.IsCode(err, "TOKEN_EXPIRED") {
		t.Fatalf("expired: %v", err)
	}
}

func TestAccessTokenRejectsForgeries(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	tk := newTokens(c)
	raw, _, _ := tk.issueAccess(7, 3, 11)

	other := tokens{cfg: TokenConfig{Secret: []byte(strings.Repeat("z", 32)), AccessTTL: time.Minute}, clock: c}
	forgedKey, _, _ := other.issueAccess(7, 3, 11)

	// "alg: none" with the same claims.
	parts := strings.Split(raw, ".")
	noneHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	algNone := noneHeader + "." + parts[1] + "."

	// Tampered payload (different user) keeping the original signature.
	tampered, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims{TenantID: 3, SessionID: 11,
		RegisteredClaims: jwt.RegisteredClaims{Issuer: tokenIssuer, Subject: "1", ExpiresAt: jwt.NewNumericDate(c.Now().Add(time.Hour))}}).
		SignedString([]byte("x"))
	tp := strings.Split(tampered, ".")
	swapped := parts[0] + "." + tp[1] + "." + parts[2]

	// Correct key but another issuer.
	wrongIssuer, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims{TenantID: 3, SessionID: 11,
		RegisteredClaims: jwt.RegisteredClaims{Issuer: "evil", Subject: "7", ExpiresAt: jwt.NewNumericDate(c.Now().Add(time.Hour))}}).
		SignedString(tk.cfg.Secret)

	for name, token := range map[string]string{
		"other key": forgedKey, "alg none": algNone, "tampered payload": swapped,
		"wrong issuer": wrongIssuer, "garbage": "not.a.jwt",
	} {
		if _, _, err := tk.parseAccess(token); !apperr.IsCode(err, "TOKEN_INVALID") {
			t.Errorf("%s: got %v, want TOKEN_INVALID", name, err)
		}
	}
}

func TestRefreshTokens(t *testing.T) {
	a, ha, err := newRefreshToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := newRefreshToken()
	if a == b || len(a) < 40 {
		t.Fatal("refresh tokens must be long and unique")
	}
	if string(hashRefreshToken(a)) != string(ha) {
		t.Fatal("hash mismatch")
	}
}

func TestLimiter(t *testing.T) {
	c := clock.NewFake(time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC))
	l := newLimiter(3, 15*time.Minute, c)
	for i := 0; i < 3; i++ {
		if ok, _ := l.allowed("k"); !ok {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
		l.fail("k")
	}
	if ok, until := l.allowed("k"); ok || !until.Equal(c.Now().Add(15*time.Minute)) {
		t.Fatalf("4th attempt should be blocked until the window ends, got %v %v", ok, until)
	}
	if ok, _ := l.allowed("other"); !ok {
		t.Fatal("keys are independent")
	}
	c.Advance(15 * time.Minute)
	if ok, _ := l.allowed("k"); !ok {
		t.Fatal("window should reset")
	}
	l.fail("k")
	l.reset("k")
	if ok, _ := l.allowed("k"); !ok {
		t.Fatal("reset")
	}
}
