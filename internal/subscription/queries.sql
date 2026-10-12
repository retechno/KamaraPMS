-- Plans (platform-wide) -----------------------------------------------------

-- name: ListPlans :many
SELECT * FROM subscription_plans ORDER BY is_active DESC, code;

-- name: GetPlanByCode :one
SELECT * FROM subscription_plans WHERE code = @code;

-- name: GetPlan :one
SELECT * FROM subscription_plans WHERE id = @id;

-- name: GetDefaultPlan :one
SELECT * FROM subscription_plans WHERE is_default;

-- name: ClearDefaultPlan :exec
UPDATE subscription_plans SET is_default = false WHERE is_default AND code <> @code;

-- name: CreatePlan :one
INSERT INTO subscription_plans (code, name, modules, max_properties, max_rooms, max_users, price, currency_code, billing_period_months, trial_days, is_active, is_default)
VALUES (@code, @name, @modules::text[], sqlc.narg(max_properties), sqlc.narg(max_rooms), sqlc.narg(max_users), @price, @currency_code, @billing_period_months, @trial_days, @is_active, @is_default)
RETURNING *;

-- name: UpdatePlan :one
UPDATE subscription_plans
SET name = @name, modules = @modules::text[], max_properties = sqlc.narg(max_properties), max_rooms = sqlc.narg(max_rooms), max_users = sqlc.narg(max_users),
    price = @price, currency_code = @currency_code, billing_period_months = @billing_period_months, trial_days = @trial_days, is_active = @is_active, is_default = @is_default
WHERE code = @code
RETURNING *;

-- Subscriptions ---------------------------------------------------------------

-- name: GetTenantForSubscription :one
SELECT id, code, timezone FROM tenants WHERE code = @code;

-- name: GetTenantTimezone :one
SELECT timezone FROM tenants WHERE id = @tenant_id;

-- name: LockSubscription :one
-- The quota lock (level db.LevelSubscription) and the row every change of the subscription starts from.
SELECT s.*, t.timezone
FROM tenant_subscriptions s JOIN tenants t ON t.id = s.tenant_id
WHERE s.tenant_id = @tenant_id
FOR UPDATE OF s;

-- name: GetAccess :one
-- What the API gate needs for one request: the subscription, its plan's modules and the tenant's time zone.
SELECT s.status, s.paid_through, s.grace_days, s.started_on, p.modules, t.timezone
FROM tenant_subscriptions s
JOIN subscription_plans p ON p.id = s.plan_id
JOIN tenants t ON t.id = s.tenant_id
WHERE s.tenant_id = @tenant_id;

-- name: GetSubscription :one
SELECT s.*, t.timezone FROM tenant_subscriptions s JOIN tenants t ON t.id = s.tenant_id WHERE s.tenant_id = @tenant_id;

-- name: InsertSubscription :one
INSERT INTO tenant_subscriptions (tenant_id, plan_id, status, started_on, paid_through, grace_days)
VALUES (@tenant_id, @plan_id, @status, @started_on, sqlc.narg(paid_through), @grace_days)
RETURNING *;

-- name: UpdateSubscription :one
UPDATE tenant_subscriptions
SET plan_id = @plan_id, status = @status, paid_through = sqlc.narg(paid_through), grace_days = @grace_days,
    max_properties_override = sqlc.narg(max_properties_override), max_rooms_override = sqlc.narg(max_rooms_override),
    max_users_override = sqlc.narg(max_users_override), version = version + 1
WHERE tenant_id = @tenant_id
RETURNING *;

-- name: Usage :one
SELECT
    (SELECT count(*) FROM properties p WHERE p.tenant_id = @tenant_id AND p.status = 'ACTIVE')::int AS properties,
    (SELECT count(*) FROM rooms r WHERE r.tenant_id = @tenant_id AND r.is_active)::int              AS rooms,
    (SELECT count(*) FROM users u WHERE u.tenant_id = @tenant_id AND u.is_active)::int              AS users;

-- Invoices --------------------------------------------------------------------

-- name: NextInvoiceNumber :one
-- Gapless per year: the row stays locked until the transaction ends.
INSERT INTO subscription_invoice_sequences (year, last_value) VALUES (@year, 1)
ON CONFLICT (year) DO UPDATE SET last_value = subscription_invoice_sequences.last_value + 1
RETURNING last_value;

-- name: InsertInvoice :one
INSERT INTO subscription_invoices (tenant_id, number, plan_id, period_start, period_end, amount, currency_code, issued_at, due_on, notes)
VALUES (@tenant_id, @number, @plan_id, @period_start, @period_end, @amount, @currency_code, @issued_at, @due_on, sqlc.narg(notes))
RETURNING *;

-- name: GetInvoiceTenant :one
-- Which tenant an invoice belongs to, read without a lock: the subscription is locked before the invoice.
SELECT tenant_id FROM subscription_invoices WHERE number = @number;

-- name: GetInvoiceForUpdate :one
SELECT * FROM subscription_invoices WHERE number = @number FOR UPDATE;

-- name: MarkInvoicePaid :one
UPDATE subscription_invoices SET status = 'PAID', paid_on = @paid_on, paid_at = @paid_at, payment_reference = sqlc.narg(payment_reference)
WHERE id = @id
RETURNING *;

-- name: MarkInvoiceVoid :one
UPDATE subscription_invoices SET status = 'VOID', voided_at = @voided_at, void_reason = @void_reason
WHERE id = @id
RETURNING *;

-- name: ListInvoices :many
SELECT i.*, p.code AS plan_code
FROM subscription_invoices i JOIN subscription_plans p ON p.id = i.plan_id
WHERE i.tenant_id = @tenant_id
ORDER BY i.period_start DESC, i.id DESC
LIMIT @lim;
