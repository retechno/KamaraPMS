-- The platform console's operators (docs/architecture/39-platform-console.md 4 and 5). These tables hold no tenant data: an operator belongs to no tenant.

-- Accounts ---------------------------------------------------------------------------------------------------------------------------------------

-- name: GetOperatorByEmail :one
SELECT * FROM operator_accounts WHERE lower(email) = lower(@email::text);

-- name: GetOperator :one
SELECT * FROM operator_accounts WHERE id = @id;

-- name: GetOperatorForUpdate :one
SELECT * FROM operator_accounts WHERE id = @id FOR UPDATE;

-- name: ListOperators :many
SELECT a.*, (SELECT count(*) FROM operator_recovery_codes c WHERE c.operator_id = a.id AND c.used_at IS NULL)::int AS recovery_codes_left
FROM operator_accounts a ORDER BY lower(a.email);

-- name: CreateOperator :one
INSERT INTO operator_accounts (email, full_name, password_hash, created_at, updated_at)
VALUES (@email, @full_name, @password_hash, @at, @at)
RETURNING *;

-- name: SetOperatorActive :exec
UPDATE operator_accounts SET is_active = @is_active WHERE id = @id;

-- name: SetOperatorPassword :exec
UPDATE operator_accounts SET password_hash = @password_hash WHERE id = @id;

-- name: TouchOperatorLastLogin :exec
UPDATE operator_accounts SET last_login_at = @at WHERE id = @id;

-- The enrolment: the secret, the time, and the step of the code that proved it (that code cannot be used again).
-- name: SetOperatorTotp :exec
UPDATE operator_accounts SET totp_secret_enc = @secret_enc, totp_enrolled_at = @at, totp_last_step = @step WHERE id = @id;

-- name: ClearOperatorTotp :exec
UPDATE operator_accounts SET totp_secret_enc = NULL, totp_enrolled_at = NULL, totp_last_step = NULL WHERE id = @id;

-- A code of this step or of an earlier one was used already: the update touches no row (the replay of P4, decided by the database under the row lock).
-- name: AdvanceOperatorTotpStep :execrows
UPDATE operator_accounts SET totp_last_step = @step
WHERE id = @id AND (totp_last_step IS NULL OR totp_last_step < @step);

-- Key rotation: every secret, under the lock.
-- name: ListOperatorSecretsForUpdate :many
SELECT id, totp_secret_enc FROM operator_accounts WHERE totp_secret_enc IS NOT NULL ORDER BY id FOR UPDATE;

-- name: SetOperatorSecretEnc :exec
UPDATE operator_accounts SET totp_secret_enc = @secret_enc WHERE id = @id;

-- Recovery codes ---------------------------------------------------------------------------------------------------------------------------------

-- name: InsertRecoveryCode :exec
INSERT INTO operator_recovery_codes (operator_id, code_hash, created_at) VALUES (@operator_id, @code_hash, @at);

-- name: DeleteRecoveryCodes :exec
DELETE FROM operator_recovery_codes WHERE operator_id = @operator_id;

-- One use: the row is marked in the same statement that finds it, so two calls with the same code cannot both succeed.
-- name: UseRecoveryCode :execrows
UPDATE operator_recovery_codes SET used_at = @at
WHERE operator_id = @operator_id AND code_hash = @code_hash AND used_at IS NULL;

-- name: CountRecoveryCodesLeft :one
SELECT count(*)::int FROM operator_recovery_codes WHERE operator_id = @operator_id AND used_at IS NULL;

-- Challenges -------------------------------------------------------------------------------------------------------------------------------------

-- name: CreateChallenge :one
INSERT INTO operator_login_challenges (operator_id, token_hash, purpose, expires_at, created_at)
VALUES (@operator_id, @token_hash, @purpose, @expires_at, @at)
RETURNING id;

-- The row is locked: attempts are counted and "single use" holds under concurrency.
-- name: GetChallengeForUpdate :one
SELECT * FROM operator_login_challenges WHERE token_hash = @token_hash FOR UPDATE;

-- name: CountChallengeAttempt :one
UPDATE operator_login_challenges SET attempts = attempts + 1 WHERE id = @id RETURNING attempts;

-- name: SetChallengePendingSecret :exec
UPDATE operator_login_challenges SET pending_secret_enc = @secret_enc WHERE id = @id;

-- A used challenge keeps no secret.
-- name: UseChallenge :exec
UPDATE operator_login_challenges SET used_at = @at, pending_secret_enc = NULL WHERE id = @id;

-- A dead challenge cannot be tried again: it is used up now, whatever the reason.
-- name: KillChallenge :exec
UPDATE operator_login_challenges SET used_at = @at, pending_secret_enc = NULL WHERE id = @id AND used_at IS NULL;

-- name: DeleteOldChallenges :exec
DELETE FROM operator_login_challenges WHERE expires_at < @before;

-- name: ListPendingSecretsForUpdate :many
SELECT id, pending_secret_enc FROM operator_login_challenges WHERE pending_secret_enc IS NOT NULL ORDER BY id FOR UPDATE;

-- name: SetChallengeSecretEnc :exec
UPDATE operator_login_challenges SET pending_secret_enc = @secret_enc WHERE id = @id;

-- Sessions ---------------------------------------------------------------------------------------------------------------------------------------

-- name: CreateSession :one
INSERT INTO operator_sessions (operator_id, refresh_token_hash, expires_at, user_agent, ip_address, created_at)
VALUES (@operator_id, @refresh_token_hash, @expires_at, sqlc.narg(user_agent), sqlc.narg(ip_address), @created_at)
RETURNING id;

-- name: GetSessionByTokenForUpdate :one
SELECT s.id, s.operator_id, s.expires_at, s.revoked_at, s.revoked_reason, a.is_active, a.totp_enrolled_at
FROM operator_sessions s JOIN operator_accounts a ON a.id = s.operator_id
WHERE s.refresh_token_hash = @token_hash FOR UPDATE OF s;

-- The state of the session and of the account of a bearer token: one query per request, so logout and deactivation take effect at once.
-- name: GetSessionState :one
SELECT s.expires_at, s.revoked_at, a.is_active, a.totp_enrolled_at
FROM operator_sessions s JOIN operator_accounts a ON a.id = s.operator_id
WHERE s.id = @session_id AND s.operator_id = @operator_id;

-- name: RevokeSession :exec
UPDATE operator_sessions SET revoked_at = @at, revoked_reason = @reason WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeAllOperatorSessions :exec
UPDATE operator_sessions SET revoked_at = @at, revoked_reason = @reason WHERE operator_id = @operator_id AND revoked_at IS NULL;

-- name: HasLiveSessionSince :one
SELECT EXISTS (SELECT 1 FROM operator_sessions WHERE operator_id = @operator_id AND revoked_at IS NULL AND created_at >= @since);

-- Audit ------------------------------------------------------------------------------------------------------------------------------------------

-- name: InsertOperatorAudit :exec
INSERT INTO operator_audit_logs (operator_id, action, entity_type, entity_id, entity_label, tenant_id, old_data, new_data, request_id, ip_address, created_at)
VALUES (sqlc.narg(operator_id), @action, @entity_type, @entity_id, sqlc.narg(entity_label), sqlc.narg(tenant_id), @old_data, @new_data, sqlc.narg(request_id), sqlc.narg(ip_address), @created_at);
