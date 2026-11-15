package operator

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"kamarapms/internal/audit"
	"kamarapms/internal/iam"
	"kamarapms/internal/operator/operatordb"
	"kamarapms/internal/platform/apperr"
	"kamarapms/internal/platform/auth"
)

// The commands of pms-admin on operator accounts (doc 39 section 6). There is no screen for them: an operator is created, disabled and reset only on the server. Each writes the operator's
// trail with operator_id NULL, and refuses to run from the console (an operator cannot manage operators).

func operatorNotFound() *apperr.Error {
	return apperr.NotFound("OPERATOR_NOT_FOUND", "the operator does not exist")
}

// notFromConsole refuses the commands when an operator makes the call: they are for pms-admin.
func notFromConsole(ctx context.Context) error {
	if _, ok := auth.OperatorFrom(ctx); ok {
		return apperr.Forbidden("PERMISSION_DENIED", "operator accounts are managed with pms-admin only")
	}
	return nil
}

const generatedPasswordAlphabet = "ABCDEFGHJKMNPQRSTVWXYZabcdefghjkmnpqrstvwxyz23456789"

// GeneratePassword is a random password of 20 characters from an alphabet without look-alikes (about 114 bits), for create-operator to print once.
func GeneratePassword() (string, error) {
	var b strings.Builder
	buf := make([]byte, 64)
	limit := byte(256 - 256%len(generatedPasswordAlphabet))
	for b.Len() < 20 {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("operator: password: %w", err)
		}
		for _, v := range buf {
			if v >= limit {
				continue
			}
			b.WriteByte(generatedPasswordAlphabet[int(v)%len(generatedPasswordAlphabet)])
			if b.Len() == 20 {
				break
			}
		}
	}
	return b.String(), nil
}

func validateEmail(email string) []apperr.FieldError {
	if email == "" {
		return []apperr.FieldError{{Field: "email", Code: "REQUIRED"}}
	}
	at := strings.IndexByte(email, '@')
	if utf8.RuneCountInString(email) > 254 || at < 1 || at != strings.LastIndexByte(email, '@') || at == len(email)-1 || strings.ContainsAny(email, " \t\r\n") {
		return []apperr.FieldError{{Field: "email", Code: "INVALID_VALUE"}}
	}
	return nil
}

// CreateOperator makes an account: a name, an e-mail and a password (the policy of the hotel's users). There is no TOTP yet: it is enrolled at the first sign-in.
func (s *Service) CreateOperator(ctx context.Context, email, fullName, password string) (Operator, error) {
	if err := notFromConsole(ctx); err != nil {
		return Operator{}, err
	}
	email, fullName = NormaliseEmail(email), strings.TrimSpace(fullName)
	fields := validateEmail(email)
	if fullName == "" || utf8.RuneCountInString(fullName) > 200 {
		fields = append(fields, apperr.FieldError{Field: "full_name", Code: "INVALID_VALUE"})
	}
	if msg := iam.ValidatePassword(password); msg != "" {
		fields = append(fields, apperr.FieldError{Field: "password", Code: "PASSWORD_POLICY", Message: msg})
	}
	if len(fields) > 0 {
		return Operator{}, apperr.Invalid("the operator is not valid", fields...)
	}
	hash, err := iam.HashPassword(password)
	if err != nil {
		return Operator{}, err
	}
	var out Operator
	err = s.txm.WithinTx(ctx, func(ctx context.Context) error {
		row, err := s.q(ctx).CreateOperator(ctx, operatordb.CreateOperatorParams{Email: email, FullName: fullName, PasswordHash: hash, At: s.clock.Now()})
		if err != nil {
			return err
		}
		out = toOperator(row, 0)
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionCreated, EntityType: "operator", EntityID: row.ID, EntityLabel: row.Email, New: map[string]any{"email": row.Email, "full_name": row.FullName}})
	})
	return out, err
}

// ListOperators answers every account.
func (s *Service) ListOperators(ctx context.Context) ([]Operator, error) {
	rows, err := s.q(ctx).ListOperators(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Operator, 0, len(rows))
	for _, r := range rows {
		out = append(out, Operator{ID: r.ID, Email: r.Email, FullName: r.FullName, IsActive: r.IsActive, TOTPEnrolled: r.TotpEnrolledAt != nil, LastLoginAt: r.LastLoginAt, RecoveryCodesLeft: int(r.RecoveryCodesLeft)})
	}
	return out, nil
}

// byEmail locks the account of an e-mail inside the ambient transaction.
func (s *Service) byEmail(ctx context.Context, email string) (operatordb.OperatorAccount, error) {
	q := s.q(ctx)
	row, err := q.GetOperatorByEmail(ctx, NormaliseEmail(email))
	if errors.Is(err, pgx.ErrNoRows) {
		return row, operatorNotFound()
	}
	if err != nil {
		return row, err
	}
	return q.GetOperatorForUpdate(ctx, row.ID)
}

// revokeAll ends every session of an operator with a reason.
func (s *Service) revokeAll(ctx context.Context, id int64, why string) error {
	return s.q(ctx).RevokeAllOperatorSessions(ctx, operatordb.RevokeAllOperatorSessionsParams{OperatorID: id, At: s.clock.Now(), Reason: reason(why)})
}

// SetActive disables or enables an account. Disabling ends its sessions (DEACTIVATED).
func (s *Service) SetActive(ctx context.Context, email string, active bool) error {
	if err := notFromConsole(ctx); err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		op, err := s.byEmail(ctx, email)
		if err != nil {
			return err
		}
		if op.IsActive == active {
			return nil
		}
		if err := s.q(ctx).SetOperatorActive(ctx, operatordb.SetOperatorActiveParams{ID: op.ID, IsActive: active}); err != nil {
			return err
		}
		action := ActionEnabled
		if !active {
			action = ActionDisabled
			if err := s.revokeAll(ctx, op.ID, ReasonDeactivated); err != nil {
				return err
			}
			if err := s.q(ctx).KillChallengesOf(ctx, operatordb.KillChallengesOfParams{OperatorID: op.ID, At: s.clock.Now()}); err != nil {
				return err
			}
		}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: action, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
}

// ResetPassword sets a new password and ends every session (PASSWORD_RESET) and every challenge.
func (s *Service) ResetPassword(ctx context.Context, email, password string) error {
	if err := notFromConsole(ctx); err != nil {
		return err
	}
	if msg := iam.ValidatePassword(password); msg != "" {
		return apperr.Invalid("the password is not valid", apperr.FieldError{Field: "password", Code: "PASSWORD_POLICY", Message: msg})
	}
	hash, err := iam.HashPassword(password)
	if err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		op, err := s.byEmail(ctx, email)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if err := q.SetOperatorPassword(ctx, operatordb.SetOperatorPasswordParams{ID: op.ID, PasswordHash: hash}); err != nil {
			return err
		}
		if err := s.revokeAll(ctx, op.ID, ReasonPasswordReset); err != nil {
			return err
		}
		if err := q.KillChallengesOf(ctx, operatordb.KillChallengesOfParams{OperatorID: op.ID, At: s.clock.Now()}); err != nil {
			return err
		}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionPasswordReset, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
}

// ResetTOTP clears the secret and the recovery codes and ends every session (TOTP_RESET): the next sign-in enrols again.
func (s *Service) ResetTOTP(ctx context.Context, email string) error {
	if err := notFromConsole(ctx); err != nil {
		return err
	}
	return s.txm.WithinTx(ctx, func(ctx context.Context) error {
		op, err := s.byEmail(ctx, email)
		if err != nil {
			return err
		}
		q := s.q(ctx)
		if err := q.ClearOperatorTotp(ctx, op.ID); err != nil {
			return err
		}
		if err := q.DeleteRecoveryCodes(ctx, op.ID); err != nil {
			return err
		}
		if err := s.revokeAll(ctx, op.ID, ReasonTOTPReset); err != nil {
			return err
		}
		if err := q.KillChallengesOf(ctx, operatordb.KillChallengesOfParams{OperatorID: op.ID, At: s.clock.Now()}); err != nil {
			return err
		}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionTOTPReset, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
}

// RegenerateRecovery makes a new set of recovery codes (the old set dies) and answers it; it is shown once. The account must have an authenticator.
func (s *Service) RegenerateRecovery(ctx context.Context, email string) ([]string, error) {
	if err := notFromConsole(ctx); err != nil {
		return nil, err
	}
	var codes []string
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		op, err := s.byEmail(ctx, email)
		if err != nil {
			return err
		}
		if op.TotpEnrolledAt == nil {
			return apperr.Conflict("TOTP_NOT_ENROLLED", "this account has no authenticator yet: it gets its recovery codes when it enrols")
		}
		if codes, err = s.newRecoveryCodes(ctx, op.ID); err != nil {
			return err
		}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionRecoveryRegenerated, EntityType: "operator", EntityID: op.ID, EntityLabel: op.Email})
	})
	return codes, err
}

// RotateKey re-encrypts every TOTP secret and every pending secret from the old key to the new one, in one transaction: either all of them move or none. It answers how many secrets moved.
func (s *Service) RotateKey(ctx context.Context, oldKey, newKey []byte) (int, error) {
	if err := notFromConsole(ctx); err != nil {
		return 0, err
	}
	if len(oldKey) != KeyLength || len(newKey) != KeyLength {
		return 0, apperr.Invalid("both keys must be 32 bytes")
	}
	moved := 0
	err := s.txm.WithinTx(ctx, func(ctx context.Context) error {
		q := s.q(ctx)
		move := func(sealed []byte) ([]byte, error) {
			plain, err := open(oldKey, sealed)
			if err != nil {
				return nil, apperr.Invalid("a secret does not open with the old key: nothing was changed").WithCause(err)
			}
			return seal(newKey, plain)
		}
		accounts, err := q.ListOperatorSecretsForUpdate(ctx)
		if err != nil {
			return err
		}
		for _, a := range accounts {
			sealed, err := move(a.TotpSecretEnc)
			if err != nil {
				return err
			}
			if err := q.SetOperatorSecretEnc(ctx, operatordb.SetOperatorSecretEncParams{ID: a.ID, SecretEnc: sealed}); err != nil {
				return err
			}
			moved++
		}
		pending, err := q.ListPendingSecretsForUpdate(ctx)
		if err != nil {
			return err
		}
		for _, p := range pending {
			sealed, err := move(p.PendingSecretEnc)
			if err != nil {
				return err
			}
			if err := q.SetChallengeSecretEnc(ctx, operatordb.SetChallengeSecretEncParams{ID: p.ID, SecretEnc: sealed}); err != nil {
				return err
			}
			moved++
		}
		return s.audit.WriteOperator(ctx, audit.OperatorEntry{Action: ActionTOTPKeyRotated, EntityType: "operator", EntityID: 0, New: map[string]any{"secrets_moved": moved}})
	})
	if err != nil {
		return 0, err
	}
	return moved, nil
}
