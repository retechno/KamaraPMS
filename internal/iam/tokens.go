package iam

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/clock"
)

const tokenIssuer = "kamarapms"

// TokenConfig configures access and refresh tokens.
type TokenConfig struct {
	Secret       []byte        // HMAC key for access tokens (>= 32 bytes)
	AccessTTL    time.Duration // e.g. 15m
	RefreshTTL   time.Duration // e.g. 30 days
	CookieSecure bool          // send the refresh cookie over HTTPS only
}

// accessClaims is the JWT payload. It carries identity only: the admin flag and
// active status are read from the database on every request.
type accessClaims struct {
	TenantID  int64 `json:"tid"`
	SessionID int64 `json:"sid"`
	jwt.RegisteredClaims
}

// tokens issues and verifies tokens.
type tokens struct {
	cfg   TokenConfig
	clock clock.Clock
}

// issueAccess returns a signed access token and its expiry.
func (t tokens) issueAccess(userID, tenantID, sessionID int64) (string, time.Time, error) {
	now := t.clock.Now()
	exp := now.Add(t.cfg.AccessTTL)
	claims := accessClaims{
		TenantID:  tenantID,
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   strconv.FormatInt(userID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.cfg.Secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("iam: sign access token: %w", err)
	}
	return s, exp, nil
}

// parseAccess verifies an access token: HS256 only (no "alg: none" or algorithm
// confusion), our issuer, not expired.
func (t tokens) parseAccess(raw string) (userID int64, claims accessClaims, err error) {
	_, err = jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return t.cfg.Secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.clock.Now),
		jwt.WithLeeway(30*time.Second),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return 0, accessClaims{}, apperr.Unauthorized("TOKEN_EXPIRED", "the access token has expired")
	}
	if err != nil {
		return 0, accessClaims{}, apperr.Unauthorized("TOKEN_INVALID", "the access token is invalid").WithCause(err)
	}
	userID, err = strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID < 1 || claims.TenantID < 1 || claims.SessionID < 1 {
		return 0, accessClaims{}, apperr.Unauthorized("TOKEN_INVALID", "the access token is invalid")
	}
	return userID, claims, nil
}

// newRefreshToken returns an opaque refresh token and the hash to store.
// Only the hash is persisted: a database leak does not yield usable tokens.
func newRefreshToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("iam: refresh token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashRefreshToken(token), nil
}

func hashRefreshToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
