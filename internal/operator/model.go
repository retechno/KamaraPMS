// Package operator is the platform console's identity and API (docs/architecture/39-platform-console.md): the operator accounts of the company that sells KamaraPMS, their password and
// mandatory TOTP second factor, their sessions, and the routes under /api/v1/platform/. An operator is not a user of any tenant, and a tenant token is never accepted here.
package operator

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

// What a login challenge is for (operator_login_challenges.purpose).
const (
	PurposeVerify = "VERIFY" // the account has TOTP: the next step is a code
	PurposeEnroll = "ENROLL" // the account has none yet: the next step is to enrol one
)

// Why an operator session ended (operator_sessions.revoked_reason).
const (
	ReasonRotated         = "ROTATED"
	ReasonLogout          = "LOGOUT"
	ReasonReuseDetected   = "REUSE_DETECTED"
	ReasonPasswordChanged = "PASSWORD_CHANGED"
	ReasonPasswordReset   = "PASSWORD_RESET"
	ReasonDeactivated     = "DEACTIVATED"
	ReasonTOTPReset       = "TOTP_RESET"
)

// The rules of sign-in (decisions P4 to P10).
const (
	ChallengeTTL         = 5 * time.Minute // a challenge is exchanged for a session within this time
	MaxChallengeAttempts = 5               // wrong codes per challenge: then it dies and the operator starts again
	RotationGrace        = 30 * time.Second
	RecoveryCodeCount    = 10 // recovery codes per set
	RecoveryLowWater     = 3  // the console warns when this many or fewer are left
	DefaultAccessTTL     = 10 * time.Minute
	MaxAccessTTL         = 15 * time.Minute
	DefaultRefreshTTL    = 12 * time.Hour
	MaxRefreshTTL        = 24 * time.Hour
)

// The operator's audit actions (operator_audit_logs.action).
const (
	ActionLoginPasswordOK     = "operator.login_password_ok"
	ActionLoginFailed         = "operator.login_failed"
	ActionTOTPEnrolled        = "operator.totp_enrolled"
	ActionLogin               = "operator.login"
	ActionRecoveryCodeUsed    = "operator.recovery_code_used"
	ActionLogout              = "operator.logout"
	ActionRefreshTokenReused  = "operator.refresh_token_reused"
	ActionCreated             = "operator.created"
	ActionDisabled            = "operator.disabled"
	ActionEnabled             = "operator.enabled"
	ActionPasswordReset       = "operator.password_reset"
	ActionTOTPReset           = "operator.totp_reset"
	ActionRecoveryRegenerated = "operator.recovery_regenerated"
	ActionTOTPKeyRotated      = "operator.totp_key_rotated"
)

// Operator is an account as the console shows it. The password hash and the TOTP secret never leave the package.
type Operator struct {
	ID                int64      `json:"id"`
	Email             string     `json:"email"`
	FullName          string     `json:"full_name"`
	IsActive          bool       `json:"is_active"`
	TOTPEnrolled      bool       `json:"totp_enrolled"`
	LastLoginAt       *time.Time `json:"last_login_at"`
	RecoveryCodesLeft int        `json:"recovery_codes_left"`
}

// NormaliseEmail is the e-mail as it is compared: trimmed, in lower case.
func NormaliseEmail(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// recoveryAlphabet has 30 characters and none that can be taken for another (no I, L, O, U, 0, 1): about 4.9 bits per character, 49 bits for a code.
const recoveryAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

const recoveryLength = 10

// NewRecoveryCode returns a recovery code in the shown form XXXX-XXXX-XX. Every character is drawn without bias (a byte that would favour the first characters is dropped).
func NewRecoveryCode() (string, error) {
	var b strings.Builder
	buf := make([]byte, 32)
	for n := 0; n < recoveryLength; {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("operator: recovery code: %w", err)
		}
		for _, v := range buf {
			if v >= byte(len(recoveryAlphabet)*8) { // 240: a multiple of 30, so every character is equally likely
				continue
			}
			b.WriteByte(recoveryAlphabet[int(v)%len(recoveryAlphabet)])
			if n++; n == recoveryLength {
				break
			}
		}
	}
	c := b.String()
	return c[:4] + "-" + c[4:8] + "-" + c[8:], nil
}

// NormaliseRecoveryCode is the code as it is stored and compared: upper case, without dashes or spaces. It is false when what remains cannot be a recovery code.
func NormaliseRecoveryCode(s string) (string, bool) {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
	if len(s) != recoveryLength {
		return "", false
	}
	for _, r := range s {
		if !strings.ContainsRune(recoveryAlphabet, r) {
			return "", false
		}
	}
	return s, true
}

// HashRecoveryCode is what is stored of a normalised recovery code (a recovery code is random and long enough that a plain SHA-256 is enough; it is single use).
func HashRecoveryCode(normalised string) []byte {
	sum := sha256.Sum256([]byte(normalised))
	return sum[:]
}

// hashToken is what is stored of a challenge token or a refresh token.
func hashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
