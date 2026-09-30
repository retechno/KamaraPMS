-- Rates module queries (sqlc): rate plans and the rate grid. Scoped by tenant_id and property_id.

-- name: CreateRatePlan :one
INSERT INTO rate_plans (
    tenant_id, property_id, code, name, description, meal_plan, cancellation_policy, is_refundable,
    room_charge_code_id, is_active, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @code, @name, sqlc.narg(rate_plan_description), @meal_plan, sqlc.narg(cancellation_policy), @is_refundable,
    @room_charge_code_id, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetRatePlan :one
SELECT * FROM rate_plans WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRatePlanForUpdate :one
SELECT * FROM rate_plans WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: GetRatePlanForShare :one
SELECT * FROM rate_plans WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR SHARE;

-- name: ListRatePlans :many
SELECT * FROM rate_plans
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY id
LIMIT @row_limit;

-- name: UpdateRatePlan :one
UPDATE rate_plans SET
    name = @name, description = sqlc.narg(rate_plan_description), meal_plan = @meal_plan,
    cancellation_policy = sqlc.narg(cancellation_policy), is_refundable = @is_refundable,
    room_charge_code_id = @room_charge_code_id, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- The room charge code of a plan: what the plan sells through, and how its grid amounts are read.
-- name: GetChargeCodeForPlan :one
SELECT id, code, charge_type, price_mode, is_active FROM charge_codes
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Share-locked, so the code cannot be deactivated (or re-typed) while a plan is being pointed at it.
-- name: GetChargeCodeForPlanShare :one
SELECT id, code, charge_type, price_mode, is_active FROM charge_codes
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
FOR SHARE;

-- id, code and price mode of every charge code of the property (to describe plans).
-- name: ListChargeCodeModes :many
SELECT id, code, price_mode FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: RoomTypeExists :one
SELECT EXISTS (SELECT 1 FROM room_types WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id);

-- name: CountRatesOfPlan :one
SELECT count(*) FROM rates WHERE tenant_id = @tenant_id AND property_id = @property_id AND rate_plan_id = @rate_plan_id;

-- name: ListRates :many
SELECT room_type_id, stay_date, amount FROM rates
WHERE tenant_id = @tenant_id AND property_id = @property_id AND rate_plan_id = @rate_plan_id
  AND stay_date >= @from_date AND stay_date < @to_date
  AND (sqlc.narg(room_type_id)::bigint IS NULL OR room_type_id = sqlc.narg(room_type_id)::bigint)
ORDER BY stay_date, room_type_id;

-- Bulk upsert of every (room type, date) pair. Dates arrive as ISO strings (already filtered by weekday).
-- Returns how many nights were written and how many of them are new.
-- name: UpsertRates :one
WITH written AS (
    INSERT INTO rates (tenant_id, property_id, rate_plan_id, room_type_id, stay_date, amount, updated_by)
    SELECT @tenant_id::bigint, @property_id::bigint, @rate_plan_id::bigint, t.room_type_id, d.stay_date::date, @amount::numeric, sqlc.narg(actor_id)::bigint
    FROM unnest(@room_type_ids::bigint[]) AS t (room_type_id)
    CROSS JOIN unnest(@dates::text[]) AS d (stay_date)
    ON CONFLICT (rate_plan_id, room_type_id, stay_date) DO UPDATE SET amount = EXCLUDED.amount, updated_by = EXCLUDED.updated_by
    RETURNING (xmax = 0) AS inserted
)
SELECT count(*)::bigint AS written, (count(*) FILTER (WHERE inserted))::bigint AS created FROM written;

-- The nights of a stay priced from the grid: [arrival, departure) for one plan and room type.
-- name: ListNightRates :many
SELECT stay_date, amount FROM rates
WHERE tenant_id = @tenant_id AND property_id = @property_id AND rate_plan_id = @rate_plan_id AND room_type_id = @room_type_id
  AND stay_date >= @arrival AND stay_date < @departure
ORDER BY stay_date;
