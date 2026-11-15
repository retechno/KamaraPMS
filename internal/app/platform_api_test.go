package app

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 uses HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kamarapms/internal/audit"
	"kamarapms/internal/operator"
	"kamarapms/internal/platform/db"
	"kamarapms/internal/platform/dbtest"
)

// consoleEnv turns the console on (a key is set) and creates one operator.
func consoleEnv(t *testing.T) *apiEnv {
	t.Helper()
	e := newAPI(t)
	e.operator = operator.Config{Secret: []byte(strings.Repeat("s", 32)), Key: bytes.Repeat([]byte{9}, operator.KeyLength), AccessTTL: 10 * time.Minute, RefreshTTL: 12 * time.Hour}
	e.app = e.build(0)
	e.handler = e.app.Handler
	if _, err := consoleService(t, e).CreateOperator(context.Background(), "ops@example.test", "Ops", testPassword); err != nil {
		t.Fatal(err)
	}
	return e
}

func consoleService(t *testing.T, e *apiEnv) *operator.Service {
	t.Helper()
	return operator.NewService(db.NewTxManager(dbtest.Pool(t), 5*time.Second), e.clock, audit.NewWriter(e.clock), e.operator)
}

// rawCode is RFC 6238 written out again here, so that the test does not lean on the code it tests.
func rawCode(secret []byte, at time.Time) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(at.Unix()/30)) //nolint:gosec // positive
	m := hmac.New(sha1.New, secret)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0xf
	n := (binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff) % 1000000
	return fmt.Sprintf("%06d", n)
}

func decodeSecret(t *testing.T, grouped string) []byte {
	t.Helper()
	b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ReplaceAll(grouped, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// platformCall calls the console with an optional bearer token and cookies.
func platformCall(e *apiEnv, method, path, token string, body any, cookies ...*http.Cookie) (*httptest.ResponseRecorder, map[string]any) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, r)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func concrete(path string) string { return strings.ReplaceAll(path, "{}", "1") }

// platformPublicRoutes are the only console routes that answer without an operator's token.
var platformPublicRoutes = map[string]string{
	"POST /api/v1/platform/auth/login":          "password: gives a challenge",
	"POST /api/v1/platform/auth/totp":           "second step: gives a session",
	"POST /api/v1/platform/auth/enroll/start":   "first sign-in: the secret of the authenticator, for the holder of a challenge",
	"POST /api/v1/platform/auth/enroll/confirm": "first sign-in: confirms the authenticator, gives a session",
	"POST /api/v1/platform/auth/refresh":        "uses the refresh cookie",
	"POST /api/v1/platform/auth/logout":         "ends the session of the cookie",
}

// Without PMS_OPERATOR_TOTP_KEY the console is not there (decision P1): every route answers 404 ROUTE_NOT_FOUND, with or without a token.
func TestConsoleIsOffWithoutAKey(t *testing.T) {
	e := newAPI(t)
	abc := e.login("ABC")
	routes := platformServerRoutes(t)
	if len(routes) == 0 {
		t.Fatal("no console routes found")
	}
	for _, r := range routes {
		for _, token := range []string{"", abc.token} {
			rec, body := platformCall(e, r.method, concrete(r.path), token, map[string]string{})
			if rec.Code != http.StatusNotFound || body["code"] != "ROUTE_NOT_FOUND" {
				t.Errorf("%s (token %v): %d %v", r.key(), token != "", rec.Code, body["code"])
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Errorf("%s: Cache-Control %q", r.key(), rec.Header().Get("Cache-Control"))
			}
		}
	}
}

func TestConsoleSignInOverHTTP(t *testing.T) {
	e := consoleEnv(t)

	rec, body := platformCall(e, http.MethodPost, "/api/v1/platform/auth/login", "", map[string]string{"email": "ops@example.test", "password": testPassword})
	if rec.Code != 200 || body["next"] != "enroll" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("login: %d %v", rec.Code, body)
	}
	ch := body["challenge"].(string)
	_, body = platformCall(e, http.MethodPost, "/api/v1/platform/auth/enroll/start", "", map[string]string{"challenge": ch})
	secret := decodeSecret(t, body["secret"].(string))
	rec, body = platformCall(e, http.MethodPost, "/api/v1/platform/auth/enroll/confirm", "", map[string]string{"challenge": ch, "code": rawCode(secret, e.clock.Now())})
	if rec.Code != 200 || body["access_token"] == nil || len(body["recovery_codes"].([]any)) != 10 {
		t.Fatalf("confirm: %d %v", rec.Code, body)
	}
	var ck *http.Cookie
	for _, c := range rec.Result().Cookies() {
		switch c.Name {
		case "pms_platform_refresh":
			ck = c
		case "pms_refresh":
			t.Fatal("the hotel's cookie must not be set by the console")
		}
	}
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/api/v1/platform/auth" || ck.MaxAge != int((12*time.Hour).Seconds()) {
		t.Fatalf("the refresh cookie is wrong: %+v", ck)
	}
	token := body["access_token"].(string)
	if rec, body = platformCall(e, http.MethodGet, "/api/v1/platform/auth/me", token, nil); rec.Code != 200 || body["email"] != "ops@example.test" {
		t.Fatalf("me: %d %v", rec.Code, body)
	}

	// the refresh token rotates
	rec, body = platformCall(e, http.MethodPost, "/api/v1/platform/auth/refresh", "", nil, ck)
	if rec.Code != 200 || body["access_token"] == nil {
		t.Fatalf("refresh: %d %v", rec.Code, body)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == "pms_platform_refresh" && c.Value == ck.Value {
			t.Fatal("the refresh token was not rotated")
		}
	}
	// the hotel's refresh route does not take the console's cookie
	if rec, _ = platformCall(e, http.MethodPost, "/api/v1/auth/refresh", "", nil, &http.Cookie{Name: "pms_platform_refresh", Value: ck.Value}); rec.Code != 401 {
		t.Fatalf("the hotel refresh answered %d to the console's cookie", rec.Code)
	}
}

// A token of the other kind is refused on both sides, for every route (from the list of routes, not from a sample).
func TestConsoleTokenSeparation(t *testing.T) {
	e := consoleEnv(t)
	abc := e.login("ABC")
	svc := consoleService(t, e)
	ctx := context.Background()
	ch, err := svc.Login(ctx, operator.LoginInput{Email: "ops@example.test", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	st, err := svc.EnrollStart(ctx, ch.Token)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := svc.EnrollConfirm(ctx, operator.EnrollConfirmInput{Challenge: ch.Token, Code: rawCode(decodeSecret(t, st.Secret), e.clock.Now())})
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range platformServerRoutes(t) {
		if _, public := platformPublicRoutes[r.key()]; public {
			continue
		}
		for name, tok := range map[string]string{"no": "", "hotel": abc.token, "junk": "abc.def.ghi"} {
			rec, body := platformCall(e, r.method, concrete(r.path), tok, map[string]string{})
			if rec.Code != 401 || body["code"] != "PLATFORM_TOKEN_INVALID" {
				t.Errorf("%s with a %s token: %d %v", r.key(), name, rec.Code, body["code"])
			}
		}
	}
	for _, r := range serverRoutes(t) {
		if _, public := publicRoutes[r.key()]; public {
			continue
		}
		rec, body := platformCall(e, r.method, concrete(r.path), sess.AccessToken, map[string]string{})
		if rec.Code != 401 {
			t.Errorf("%s accepts an operator's token: %d %v", r.key(), rec.Code, body["code"])
		}
	}
}
