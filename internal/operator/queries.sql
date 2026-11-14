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
VALUES (@email, @full_name, @password_hash, @at::timestamptz, @at::timestamptz)
RETURNING *;

-- name: SetOperatorActive :exec
UPDATE operator_accounts SET is_active = @is_active WHERE id = @id;

-- name: SetOperatorPassword :exec
UPDATE operator_accounts SET password_hash = @password_hash WHERE id = @id;

-- name: TouchOperatorLastLogin :exec
UPDATE operator_accounts SET last_login_at = @at::timestamptz WHERE id = @id;

-- The enrolment: the secret, the time, and the step of the code that proved it (that code cannot be used again).
-- name: SetOperatorTotp :exec
UPDATE operator_accounts SET totp_secret_enc = @secret_enc, totp_enrolled_at = @at::timestamptz, totp_last_step = @step::bigint WHERE id = @id;

-- name: ClearOperatorTotp :exec
UPDATE operator_accounts SET totp_secret_enc = NULL, totp_enrolled_at = NULL, totp_last_step = NULL WHERE id = @id;

-- A code of this step or of an earlier one was used already: the update touches no row (the replay of P4, decided by the database under the row lock).
-- name: AdvanceOperatorTotpStep :execrows
UPDATE operator_accounts SET totp_last_step = @step::bigint
WHERE id = @id AND (totp_last_step IS NULL OR totp_last_step < @step::bigint);

-- Key rotation: every secret, under the lock.
-- name: ListOperatorSecretsForUpdate :many
SELECT id, totp_secret_enc FROM operator_accounts WHERE totp_secret_enc IS NOT NULL ORDER BY id FOR UPDATE;

-- name: SetOperatorSecretEnc :exec
UPDATE operator_accounts SET totp_secret_enc = @secret_enc WHERE id = @id;

-- Recovery codes ---------------------------------------------------------------------------------------------------------------------------------

-- name: InsertRecoveryCode :exec
INSERT INTO operator_recovery_codes (operator_id, code_hash, created_at) VALUES (@operator_id, @code_hash, @at::timestamptz);

-- name: DeleteRecoveryCodes :exec
DELETE FROM operator_recovery_codes WHERE operator_id = @operator_id;

-- One use: the row is marked in the same statement that finds it, so two calls with the same code cannot both succeed.
-- name: UseRecoveryCode :execrows
UPDATE operator_recovery_codes SET used_at = @at::timestamptz
WHERE operator_id = @operator_id AND code_hash = @code_hash AND used_at IS NULL;

-- name: CountRecoveryCodesLeft :one
SELECT count(*)::int FROM operator_recovery_codes WHERE operator_id = @operator_id AND used_at IS NULL;

-- Challenges -------------------------------------------------------------------------------------------------------------------------------------

-- name: CreateChallenge :one
INSERT INTO operator_login_challenges (operator_id, token_hash, purpose, expires_at, created_at)
VALUES (@operator_id, @token_hash, @purpose, @expires_at, @at::timestamptz)
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
UPDATE operator_login_challenges SET used_at = @at::timestamptz, pending_secret_enc = NULL WHERE id = @id;

-- A dead challenge cannot be tried again: it is used up now, whatever the reason.
-- name: KillChallenge :exec
UPDATE operator_login_challenges SET used_at = @at::timestamptz, pending_secret_enc = NULL WHERE id = @id AND used_at IS NULL;

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
UPDATE operator_sessions SET revoked_at = @at::timestamptz, revoked_reason = @reason WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeAllOperatorSessions :exec
UPDATE operator_sessions SET revoked_at = @at::timestamptz, revoked_reason = @reason WHERE operator_id = @operator_id AND revoked_at IS NULL;

-- name: HasLiveSessionSince :one
SELECT EXISTS (SELECT 1 FROM operator_sessions WHERE operator_id = @operator_id AND revoked_at IS NULL AND created_at >= @since);

-- Audit ------------------------------------------------------------------------------------------------------------------------------------------

-- name: InsertOperatorAudit :exec
INSERT INTO operator_audit_logs (operator_id, action, entity_type, entity_id, entity_label, tenant_id, old_data, new_data, request_id, ip_address, created_at)
VALUES (sqlc.narg(operator_id), @action, @entity_type, @entity_id, sqlc.narg(entity_label), sqlc.narg(tenant_id), @old_data, @new_data, sqlc.narg(request_id), sqlc.narg(ip_address), @created_at);

-- Every open challenge of an operator ends (the account was disabled, or its password or its authenticator was reset).
-- name: KillChallengesOf :exec
UPDATE operator_login_challenges SET used_at = @at::timestamptz, pending_secret_enc = NULL WHERE operator_id = @operator_id AND used_at IS NULL;

-- The console's reads of every tenant (docs/architecture/39-platform-console.md 7). They have no tenant filter by design: the operator belongs to the platform, not to a hotel.
-- The only queries outside migrations and pms-admin that read across tenants.

-- name: ConsoleListTenants :many
-- One row per tenant with its plan, its usage and its open invoices. The access state and the days left are computed in Go with the tenant's time zone; only "overdue" is decided here, with the instant that is passed in.
SELECT t.id, t.code, t.name, t.timezone, t.status AS tenant_status, t.status_reason, t.created_at,
       s.status AS sub_status, s.started_on, s.paid_through, s.grace_days,
       s.max_properties_override, s.max_rooms_override, s.max_users_override,
       p.code AS plan_code, p.name AS plan_name, p.max_properties AS plan_max_properties, p.max_rooms AS plan_max_rooms, p.max_users AS plan_max_users,
       (SELECT count(*) FROM properties pr WHERE pr.tenant_id = t.id AND pr.status = 'ACTIVE')::int AS properties,
       (SELECT count(*) FROM rooms r WHERE r.tenant_id = t.id AND r.is_active)::int                 AS rooms,
       (SELECT count(*) FROM users u WHERE u.tenant_id = t.id AND u.is_active)::int                 AS users,
       COALESCE(oi.cnt, 0)::int AS open_invoice_count, COALESCE(oi.overdue, 0)::int AS overdue_invoice_count
FROM tenants t
JOIN tenant_subscriptions s ON s.tenant_id = t.id
JOIN subscription_plans p ON p.id = s.plan_id
LEFT JOIN LATERAL (
    SELECT count(*) AS cnt, count(*) FILTER (WHERE i.due_on < timezone(t.timezone, @now::timestamptz)::date) AS overdue
    FROM subscription_invoices i WHERE i.tenant_id = t.id AND i.status = 'ISSUED'
) oi ON true
WHERE (@q::text = '' OR t.code ILIKE '%' || @q::text || '%' OR t.name ILIKE '%' || @q::text || '%')
  AND (@plan::text = '' OR p.code = @plan::text)
ORDER BY t.name, t.code;

-- name: ConsoleOpenInvoiceTotals :many
-- What is owed, per currency (a plan may be billed in another currency than another plan).
SELECT i.currency_code, count(*)::int AS invoice_count, COALESCE(sum(i.amount), 0)::numeric(18,3) AS total
FROM subscription_invoices i WHERE i.status = 'ISSUED'
GROUP BY i.currency_code ORDER BY i.currency_code;

-- name: ConsoleGetTenant :one
SELECT id, code, name, status, status_reason, status_changed_at, timezone, created_at FROM tenants WHERE code = @code::text;

-- name: ConsoleTenantAdmins :many
SELECT full_name, email, is_active, last_login_at FROM users WHERE tenant_id = @tenant_id AND is_tenant_admin ORDER BY full_name, id;

-- name: ConsoleTenantActivity :many
SELECT a.id, a.action, a.entity_type, a.entity_id, a.created_at, a.user_id, u.full_name AS user_name, a.operator_id, o.full_name AS operator_name
FROM audit_logs a
LEFT JOIN users u ON u.tenant_id = a.tenant_id AND u.id = a.user_id
LEFT JOIN operator_accounts o ON o.id = a.operator_id
WHERE a.tenant_id = @tenant_id
  AND (a.action LIKE 'subscription.%' OR a.action LIKE 'subscription\_invoice.%' OR a.action LIKE 'tenant.%')
ORDER BY a.id DESC
LIMIT @lim;

-- name: ConsolePlanTenantCounts :many
SELECT plan_id, count(*)::int AS tenants FROM tenant_subscriptions GROUP BY plan_id;

-- name: ConsolePlanTenants :many
-- The tenants on a plan, with the overrides and the usage that decide whether a lower limit of the plan puts them over it.
SELECT t.code, s.max_properties_override, s.max_rooms_override, s.max_users_override,
       (SELECT count(*) FROM properties pr WHERE pr.tenant_id = t.id AND pr.status = 'ACTIVE')::int AS properties,
       (SELECT count(*) FROM rooms r WHERE r.tenant_id = t.id AND r.is_active)::int                 AS rooms,
       (SELECT count(*) FROM users u WHERE u.tenant_id = t.id AND u.is_active)::int                 AS users
FROM tenant_subscriptions s JOIN tenants t ON t.id = s.tenant_id
WHERE s.plan_id = @plan_id
ORDER BY t.code;

-- name: ConsoleListInvoices :many
-- Every tenant's invoices. overdue_days is today in the tenant's time zone minus the due date, for an ISSUED invoice past its due date, else 0.
SELECT i.number, i.plan_id, p.code AS plan_code, i.period_start, i.period_end, i.amount, i.currency_code, i.status, i.issued_at, i.due_on,
       i.paid_on, i.payment_reference, i.void_reason, i.notes, t.code AS tenant_code, t.name AS tenant_name,
       CASE WHEN i.status = 'ISSUED' THEN GREATEST((timezone(t.timezone, @now::timestamptz)::date - i.due_on), 0) ELSE 0 END::int AS overdue_days
FROM subscription_invoices i
JOIN tenants t ON t.id = i.tenant_id
JOIN subscription_plans p ON p.id = i.plan_id
WHERE (@tenant::text = '' OR t.code = @tenant::text)
  AND CASE @status::text
        WHEN 'open'    THEN i.status = 'ISSUED'
        WHEN 'overdue' THEN i.status = 'ISSUED' AND i.due_on < timezone(t.timezone, @now::timestamptz)::date
        WHEN 'paid'    THEN i.status = 'PAID'
        WHEN 'void'    THEN i.status = 'VOID'
        ELSE true END
ORDER BY i.issued_at DESC, i.id DESC
LIMIT @lim;

-- name: ConsoleLockTenantSubscription :one
-- The row the quota and every change of the subscription lock (db.LevelSubscription).
SELECT id FROM tenant_subscriptions WHERE tenant_id = @tenant_id FOR UPDATE;

-- name: ConsoleSetTenantStatus :one
UPDATE tenants SET status = @status::text, status_reason = NULLIF(@reason::text, ''), status_changed_at = @at::timestamptz
WHERE id = @id
RETURNING id, code, status;

-- name: ConsoleRevokeTenantSessions :execrows
-- A suspended hotel is signed out at once: every live session of its users ends with the reason ACCOUNT_DISABLED.
UPDATE user_sessions SET revoked_at = @at::timestamptz, revoked_reason = 'ACCOUNT_DISABLED'
WHERE revoked_at IS NULL AND user_id IN (SELECT id FROM users WHERE tenant_id = @tenant_id);

-- name: ConsoleNextInvoiceNumber :one
-- The number the next invoice of the year would get, read without taking it (a hint for the preview).
SELECT (COALESCE((SELECT last_value FROM subscription_invoice_sequences WHERE year = @year::smallint), 0) + 1)::int;
