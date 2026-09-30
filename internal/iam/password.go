package iam

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters: OWASP password storage recommendation (m=19 MiB, t=2, p=1).
// They are stored in each hash, so they can be raised later without breaking old hashes.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16

	minPasswordLen = 12
	maxPasswordLen = 128
)

var b64 = base64.RawStdEncoding

// HashPassword returns an argon2id hash in PHC string format.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("iam: salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

var errBadHash = errors.New("iam: malformed password hash")

// VerifyPassword checks password against an encoded hash in constant time.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errBadHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, errBadHash
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, errBadHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false, errBadHash
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false, errBadHash
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want))) //nolint:gosec // G115: hash length is 32
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ValidatePassword enforces the password policy (length only: long passphrases
// beat composition rules). Returns a human-readable reason or "".
func ValidatePassword(password string) string {
	n := utf8.RuneCountInString(password)
	switch {
	case n < minPasswordLen:
		return fmt.Sprintf("at least %d characters", minPasswordLen)
	case n > maxPasswordLen:
		return fmt.Sprintf("at most %d characters", maxPasswordLen)
	}
	return ""
}

// burnPasswordCheck spends the same time as a real verification. It is used when
// the account does not exist, so response time does not reveal valid emails.
var (
	dummyOnce sync.Once
	dummyHash string
)

func burnPasswordCheck(password string) {
	dummyOnce.Do(func() { dummyHash, _ = HashPassword("timing-equaliser-not-a-real-password") })
	_, _ = VerifyPassword(dummyHash, password)
}
