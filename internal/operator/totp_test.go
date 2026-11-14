package operator

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// The SHA-1 vectors of RFC 6238, appendix B: the secret is the ASCII "12345678901234567890"; the RFC gives 8 digits, a code of 6 is the last 6.
func TestTOTPVectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	for _, v := range []struct {
		unix int64
		code string
	}{{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"}, {1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"}} {
		if got := totpCode(secret, v.unix/30); got != v.code {
			t.Errorf("t=%d: got %s, want %s", v.unix, got, v.code)
		}
		if step, ok := matchTOTP(secret, v.code, time.Unix(v.unix, 0)); !ok || step != v.unix/30 {
			t.Errorf("t=%d: the code %s is not accepted at its own time (%d, %v)", v.unix, v.code, step, ok)
		}
	}
}

func TestTOTPWindow(t *testing.T) {
	secret := []byte("12345678901234567890")
	now := time.Unix(1111111109, 0)
	cur := stepOf(now)
	for _, c := range []struct {
		step int64
		ok   bool
	}{{cur - 2, false}, {cur - 1, true}, {cur, true}, {cur + 1, true}, {cur + 2, false}} {
		step, ok := matchTOTP(secret, totpCode(secret, c.step), now)
		if ok != c.ok || (ok && step != c.step) {
			t.Errorf("step %+d: got (%d, %v), want accepted=%v", c.step-cur, step, ok, c.ok)
		}
	}
	for _, bad := range []string{"", "12345", "1234567", "abcdef"} {
		if _, ok := matchTOTP(secret, bad, now); ok {
			t.Errorf("%q should not be accepted", bad)
		}
	}
	// spaces typed between the digits are tolerated, as authenticators show "123 456"
	code := totpCode(secret, cur)
	if _, ok := matchTOTP(secret, code[:3]+" "+code[3:], now); !ok {
		t.Error("a code written as 123 456 should be accepted")
	}
}

func TestSecretAndURI(t *testing.T) {
	s, err := newSecret()
	if err != nil || len(s) != secretLength {
		t.Fatalf("secret: %v, %d bytes", err, len(s))
	}
	g := groupedSecret(s)
	if strings.Count(g, " ") != 7 || len(strings.ReplaceAll(g, " ", "")) != 32 {
		t.Fatalf("the grouped secret %q should be 32 characters in 8 groups of 4", g)
	}
	back, err := secretEncoding.DecodeString(strings.ReplaceAll(g, " ", ""))
	if err != nil || !bytes.Equal(back, s) {
		t.Fatal("the grouped secret does not decode to the secret")
	}
	u := otpauthURI("ops@example.test", s)
	for _, want := range []string{"otpauth://totp/KamaraPMS%20Platform:ops@example.test?", "secret=" + secretEncoding.EncodeToString(s), "issuer=KamaraPMS%20Platform", "algorithm=SHA1", "digits=6", "period=30"} {
		if !strings.Contains(u, want) {
			t.Errorf("the uri %q lacks %q", u, want)
		}
	}
}

func TestSealOpen(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeyLength)
	other := bytes.Repeat([]byte{8}, KeyLength)
	secret := []byte("12345678901234567890")
	sealed, err := seal(key, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, secret) {
		t.Fatal("the secret is in the clear in what is stored")
	}
	if got, err := open(key, sealed); err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("round trip: %v %q", err, got)
	}
	if _, err := open(other, sealed); err == nil {
		t.Fatal("a wrong key must not open it")
	}
	again, _ := seal(key, secret)
	if bytes.Equal(again, sealed) {
		t.Fatal("two seals of the same secret must differ (a fresh nonce each)")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := open(key, sealed); err == nil {
		t.Fatal("a changed byte must not open")
	}
	if _, err := seal(key[:16], secret); err == nil {
		t.Fatal("a short key is refused")
	}
}
