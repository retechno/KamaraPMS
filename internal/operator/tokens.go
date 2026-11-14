package operator

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/clock"
)

// What an operator token says it is (decision P9): another issuer and another audience than the hotel's, so that neither side accepts the other's token.
const (
	tokenIssuer   = "kamarapms-platform" //nolint:gosec // G101: a name, not a credential
	tokenAudience = "platform"
)

// operatorClaims is the JWT payload: identity only. The account's state is read from the database on every request.
type operatorClaims struct {
	SessionID int64 `json:"osid"`
	jwt.RegisteredClaims
}

type tokens struct {
	secret []byte
	ttl    time.Duration
	clock  clock.Clock
}

// issueAccess returns a signed access token and its expiry. There is no tenant in it.
func (t tokens) issueAccess(operatorID, sessionID int64) (string, time.Time, error) {
	now := t.clock.Now()
	exp := now.Add(t.ttl)
	claims := operatorClaims{
		SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Audience:  jwt.ClaimStrings{tokenAudience},
			Subject:   strconv.FormatInt(operatorID, 10),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("operator: sign access token: %w", err)
	}
	return s, exp, nil
}

// parseAccess verifies an access token: HS256 only, our issuer and audience, not expired. A token of the hotel has another issuer and no audience, so it is refused here.
func (t tokens) parseAccess(raw string) (operatorID int64, claims operatorClaims, err error) {
	_, err = jwt.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return t.secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.clock.Now),
		jwt.WithLeeway(30*time.Second),
	)
	if errors.Is(err, jwt.ErrTokenExpired) {
		return 0, operatorClaims{}, apperr.Unauthorized("TOKEN_EXPIRED", "the access token has expired")
	}
	if err != nil {
		return 0, operatorClaims{}, errPlatformToken().WithCause(err)
	}
	operatorID, err = strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || operatorID < 1 || claims.SessionID < 1 {
		return 0, operatorClaims{}, errPlatformToken()
	}
	return operatorID, claims, nil
}

func errPlatformToken() *apperr.Error {
	return apperr.Unauthorized("PLATFORM_TOKEN_INVALID", "the access token is not a platform console token")
}

// newToken returns a random opaque token (32 bytes, base64url) and the hash to store: a challenge token and a refresh token. Only the hash is persisted.
func newToken() (token string, hash []byte, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nil, fmt.Errorf("operator: token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, hashToken(token), nil
}
