-- Settings of the cashier of a property (a row per property, seeded with the property).
-- name: GetSettings :one
SELECT * FROM property_cashier_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: UpdateSettings :one
UPDATE property_cashier_settings
   SET require_shift_for_cash = @require_shift_for_cash, max_variance = @max_variance, block_night_audit = @block_night_audit, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id
RETURNING *;

-- name: InsertShift :one
INSERT INTO cashier_shifts (tenant_id, property_id, shift_number, user_id, drawer, opened_at, business_date_opened, opening_float)
VALUES (@tenant_id, @property_id, @shift_number, @user_id, @drawer, @opened_at, @business_date_opened, @opening_float)
RETURNING id;

-- A shift with the name of its cashier.
-- name: GetShift :one
SELECT s.*, COALESCE(u.full_name, '')::text AS user_name
FROM cashier_shifts s JOIN users u ON u.id = s.user_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = @id;

-- name: OpenShiftOfUser :one
SELECT id FROM cashier_shifts WHERE tenant_id = @tenant_id AND property_id = @property_id AND user_id = @user_id AND status = 'OPEN';

-- name: ListShifts :many
SELECT s.*, COALESCE(u.full_name, '')::text AS user_name
FROM cashier_shifts s JOIN users u ON u.id = s.user_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id < @before_id
  AND (sqlc.narg(user_id)::bigint IS NULL OR s.user_id = sqlc.narg(user_id))
  AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status))
  AND (sqlc.narg(from_date)::date IS NULL OR s.business_date_opened >= sqlc.narg(from_date))
  AND (sqlc.narg(to_date)::date IS NULL OR s.business_date_opened <= sqlc.narg(to_date))
ORDER BY s.id DESC
LIMIT @row_limit;

-- The shifts that are open now (what blocks the night audit).
-- name: ListOpenShifts :many
SELECT s.id, s.shift_number, s.drawer, s.user_id, COALESCE(u.full_name, '')::text AS user_name, s.opened_at
FROM cashier_shifts s JOIN users u ON u.id = s.user_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN'
ORDER BY s.id;

-- The cash a drawer was left with by its last closed shift: what was counted (the drops were taken out of the drawer before).
-- name: LastClosedOfDrawer :one
SELECT s.counted_cash::numeric AS left_cash
FROM cashier_shifts s
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.drawer = @drawer AND s.status = 'CLOSED'
ORDER BY s.closed_at DESC, s.id DESC
LIMIT 1;

-- name: UserInProperty :one
SELECT count(*)::int FROM user_properties WHERE tenant_id = @tenant_id AND property_id = @property_id AND user_id = @user_id;

-- name: CloseShift :one
UPDATE cashier_shifts
   SET status = 'CLOSED', closed_at = @closed_at::timestamptz, business_date_closed = @business_date_closed::date, expected_cash = @expected_cash::numeric, counted_cash = @counted_cash::numeric,
       over_short = @over_short::numeric, variance_reason = sqlc.narg(variance_reason), journal_id = sqlc.narg(journal_id), closed_by = @closed_by::bigint,
       approved_by = sqlc.narg(approved_by), handed_over_to = sqlc.narg(handed_over_to)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING id;

-- name: InsertMovement :one
INSERT INTO cashier_shift_movements (tenant_id, property_id, shift_id, kind, amount, account_id, reason, business_date, journal_id, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @shift_id, @kind, @amount, sqlc.narg(account_id), @reason, @business_date, sqlc.narg(journal_id), sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: GetMovementByKey :one
SELECT * FROM cashier_shift_movements WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ListMovements :many
SELECT m.*, COALESCE(a.code, '')::text AS account_code, COALESCE(a.name, '')::text AS account_name, COALESCE(j.journal_number, '')::text AS journal_number
FROM cashier_shift_movements m
LEFT JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
LEFT JOIN gl_journals j ON j.property_id = m.property_id AND j.id = m.journal_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.shift_id = @shift_id
ORDER BY m.id;

-- name: InsertCount :exec
INSERT INTO cashier_shift_counts (tenant_id, property_id, shift_id, denomination, quantity)
VALUES (@tenant_id, @property_id, @shift_id, @denomination, @quantity);

-- name: ListCounts :many
SELECT denomination, quantity FROM cashier_shift_counts
WHERE tenant_id = @tenant_id AND property_id = @property_id AND shift_id = @shift_id
ORDER BY denomination DESC;

-- What went through the drawer of a shift. Payments and receipts of the shift itself count while they stand; a cash payment or receipt of an earlier shift
-- that is already closed and is voided by this cashier while this shift is open comes out of this drawer.
-- name: ShiftCash :one
SELECT
    COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = @property_id AND p.shift_id = @shift_id::bigint AND p.payment_type = 'PAYMENT' AND p.payment_method = 'CASH' AND (p.status = 'POSTED' OR (p.status = 'VOIDED' AND p.voided_at >= @until::timestamptz))), 0)::numeric AS payments_in,
    COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = @property_id AND p.shift_id = @shift_id::bigint AND p.payment_type = 'REFUND' AND p.payment_method = 'CASH' AND (p.status = 'POSTED' OR (p.status = 'VOIDED' AND p.voided_at >= @until::timestamptz))), 0)::numeric AS refunds_out,
    COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r WHERE r.property_id = @property_id AND r.shift_id = @shift_id::bigint AND r.payment_method = 'CASH' AND (r.status = 'POSTED' OR (r.status = 'VOIDED' AND r.voided_at >= @until::timestamptz))), 0)::numeric AS receipts_in,
    (COALESCE((SELECT sum(p.amount) FROM payments p JOIN cashier_shifts o ON o.property_id = p.property_id AND o.id = p.shift_id
                WHERE p.property_id = @property_id AND p.shift_id <> @shift_id::bigint AND p.payment_type = 'PAYMENT' AND p.payment_method = 'CASH' AND p.status = 'VOIDED'
                  AND p.voided_by = @user_id::bigint AND p.voided_at >= @opened_at::timestamptz AND p.voided_at <= @until::timestamptz AND o.status = 'CLOSED' AND p.voided_at >= o.closed_at), 0)
   + COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r JOIN cashier_shifts o ON o.property_id = r.property_id AND o.id = r.shift_id
                WHERE r.property_id = @property_id AND r.shift_id <> @shift_id::bigint AND r.payment_method = 'CASH' AND r.status = 'VOIDED'
                  AND r.voided_by = @user_id::bigint AND r.voided_at >= @opened_at::timestamptz AND r.voided_at <= @until::timestamptz AND o.status = 'CLOSED' AND r.voided_at >= o.closed_at), 0))::numeric AS voided_after_close,
    COALESCE((SELECT sum(m.amount) FROM cashier_shift_movements m WHERE m.property_id = @property_id AND m.shift_id = @shift_id AND m.kind = 'PAY_IN'), 0)::numeric AS pay_ins,
    COALESCE((SELECT sum(m.amount) FROM cashier_shift_movements m WHERE m.property_id = @property_id AND m.shift_id = @shift_id AND m.kind = 'PAY_OUT'), 0)::numeric AS pay_outs,
    COALESCE((SELECT sum(m.amount) FROM cashier_shift_movements m WHERE m.property_id = @property_id AND m.shift_id = @shift_id AND m.kind = 'DROP'), 0)::numeric AS drops;

-- The cash payments of the shift, for the report.
-- name: ListShiftPayments :many
SELECT p.id, p.payment_number, p.payment_type, p.amount, p.status, p.paid_at, p.reference_number, 'PAYMENT'::text AS source
FROM payments p
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.shift_id = @shift_id::bigint AND p.payment_method = 'CASH'
UNION ALL
SELECT r.id, r.receipt_number, 'RECEIPT', r.amount, r.status, r.paid_at, r.reference_number, 'RECEIPT'
FROM city_ledger_receipts r
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.shift_id = @shift_id::bigint AND r.payment_method = 'CASH'
ORDER BY 6, 1;

-- Whether an account of the property may be the other side of a pay-in or pay-out.
-- name: GetAccountForMovement :one
SELECT id, code, name, account_type, is_postable, is_active FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The people a drawer can be handed to: active users who can run a shift at the property (the owner of the tenant counts).
-- name: ListCashiers :many
SELECT u.id, COALESCE(u.full_name, '')::text AS full_name
FROM users u
WHERE u.tenant_id = @tenant_id AND u.is_active AND u.id <> @except_user_id::bigint
  AND (u.is_tenant_admin OR EXISTS (
        SELECT 1 FROM user_properties up
        JOIN role_permissions rp ON rp.role_id = up.role_id AND rp.permission_code IN ('cashier.shift', 'cashier.shift_manage')
        WHERE up.user_id = u.id AND up.property_id = @property_id))
ORDER BY full_name, u.id;

-- The drawers handed to a user: the closed shift that named them, while no later shift has used the drawer.
-- name: HandoversTo :many
SELECT s.id, s.shift_number::text AS shift_number, s.drawer::text AS drawer, s.user_id, COALESCE(u.full_name, '')::text AS from_name, s.closed_at::timestamptz AS closed_at,
       s.counted_cash::numeric AS left_cash
FROM cashier_shifts s JOIN users u ON u.id = s.user_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'CLOSED' AND s.handed_over_to = @user_id
  AND NOT EXISTS (SELECT 1 FROM cashier_shifts n WHERE n.property_id = s.property_id AND n.drawer = s.drawer AND n.id > s.id)
ORDER BY s.closed_at DESC, s.id DESC;

-- What the cashier took in other tenders while the shift was open (card, transfer, other): not drawer cash, shown on the report so that all tenders add up.
-- name: ShiftOtherTenders :many
SELECT p.payment_method::text AS method, p.payment_type::text AS payment_type, count(*)::int AS n, sum(p.amount)::numeric AS amount
FROM payments p
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.created_by = @user_id AND p.payment_method <> 'CASH' AND p.status = 'POSTED'
  AND p.paid_at >= @opened_at::timestamptz AND p.paid_at <= @until::timestamptz
GROUP BY 1, 2
ORDER BY 1, 2;

-- The names of the users on a report (who closed, who approved, who gets the drawer).
-- name: UserNames :many
SELECT u.id, COALESCE(u.full_name, '')::text AS full_name FROM users u WHERE u.tenant_id = @tenant_id AND u.id = ANY(@ids::bigint[]);
