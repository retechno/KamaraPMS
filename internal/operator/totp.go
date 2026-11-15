package operator

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 TOTP is HMAC-SHA1 (the authenticator apps all speak it); the secret is 20 random bytes and a code lives 30 seconds
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP (RFC 6238): HMAC-SHA1, 6 digits, a 30 second step (decision P4). Written with the standard library, no dependency (doc 39 F1).
const (
	totpStep   = 30
	totpDigits = 6
	// totpWindow is how many steps before and after the current one are accepted (a clock that is a little off).
	totpWindow = 1
	// secretLength is the length of a secret in bytes (RFC 4226 recommends 160 bits).
	secretLength = 20
)

var secretEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// stepOf is the 30 second step that holds t.
func stepOf(t time.Time) int64 { return t.Unix() / totpStep }

// totpCode is the code of a step (RFC 4226 HOTP with the step as the counter, dynamic truncation, 6 digits).
func totpCode(secret []byte, step int64) string {
	mac := hmac.New(sha1.New, secret)
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step)) //nolint:gosec // G115: a step is a positive count of 30 second periods
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, bin%1_000_000)
}

// matchTOTP says which step of the window around now the code is the code of. Every step of the window is compared, in constant time, whether or not an earlier one matched.
func matchTOTP(secret []byte, code string, now time.Time) (step int64, ok bool) {
	code = strings.TrimSpace(strings.ReplaceAll(code, " ", ""))
	if len(code) != totpDigits {
		return 0, false
	}
	cur := stepOf(now)
	for s := cur - totpWindow; s <= cur+totpWindow; s++ {
		if subtle.ConstantTimeCompare([]byte(code), []byte(totpCode(secret, s))) == 1 && !ok {
			step, ok = s, true
		}
	}
	return step, ok
}

// newSecret is 20 random bytes.
func newSecret() ([]byte, error) {
	b := make([]byte, secretLength)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("operator: totp secret: %w", err)
	}
	return b, nil
}

// groupedSecret is the secret as a person types it into an authenticator: base32 without padding, in groups of four.
func groupedSecret(secret []byte) string {
	s := secretEncoding.EncodeToString(secret)
	var parts []string
	for len(s) > 4 {
		parts, s = append(parts, s[:4]), s[4:]
	}
	return strings.Join(append(parts, s), " ")
}

// otpauthURI is what the QR code holds (doc 39 section 5).
func otpauthURI(email string, secret []byte) string {
	const issuer = "KamaraPMS Platform"
	label := url.PathEscape(issuer) + ":" + url.PathEscape(email)
	q := url.Values{}
	q.Set("secret", secretEncoding.EncodeToString(secret))
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	return "otpauth://totp/" + label + "?" + strings.ReplaceAll(q.Encode(), "+", "%20")
}
