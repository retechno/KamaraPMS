-- Rates module queries (sqlc): rate plans and the rate grid. Scoped by tenant_id and property_id.

-- name: CreateRatePlan :one
INSERT INTO rate_plans (
    tenant_id, property_id, code, name, description, meal_plan, cancellation_policy, is_refundable,
    room_charge_code_id, occupancy_kind, is_active, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @code, @name, sqlc.narg(rate_plan_description), @meal_plan, sqlc.narg(cancellation_policy), @is_refundable,
    @room_charge_code_id, @occupancy_kind, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id)
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
    room_charge_code_id = @room_charge_code_id, is_reference = @is_reference, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Only one plan per property is the reference plan: taking the flag over clears it from the others first.
-- name: ClearReferencePlans :exec
UPDATE rate_plans SET is_reference = false, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_reference AND id <> @id;

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

-- ---------------------------------------------------------------- yield rules

-- name: CreateYieldRule :one
INSERT INTO yield_rules (
    tenant_id, property_id, code, name, rate_plan_id, room_type_id, stay_from, stay_to, weekdays, occupancy_from, occupancy_to,
    lead_min, lead_max, stay_min, stay_max, adjustment_type, adjustment_value, floor_amount, cap_amount, priority, is_active,
    created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @code, @name, sqlc.narg(rate_plan_id), sqlc.narg(room_type_id), sqlc.narg(stay_from), sqlc.narg(stay_to),
    sqlc.narg(weekdays), sqlc.narg(occupancy_from), sqlc.narg(occupancy_to), sqlc.narg(lead_min), sqlc.narg(lead_max),
    sqlc.narg(stay_min), sqlc.narg(stay_max), @adjustment_type, @adjustment_value, sqlc.narg(floor_amount), sqlc.narg(cap_amount),
    @priority, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetYieldRule :one
SELECT * FROM yield_rules WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetYieldRuleForUpdate :one
SELECT * FROM yield_rules WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListYieldRules :many
SELECT * FROM yield_rules
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY priority, id;

-- The rules that can apply to a plan and room type (the ones that name another plan or type are left out).
-- name: ListApplicableYieldRules :many
SELECT * FROM yield_rules
WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_active
  AND (rate_plan_id IS NULL OR rate_plan_id = @rate_plan_id)
  AND (room_type_id IS NULL OR room_type_id = @room_type_id)
ORDER BY priority, id;

-- name: UpdateYieldRule :one
UPDATE yield_rules SET
    name = @name, rate_plan_id = sqlc.narg(rate_plan_id), room_type_id = sqlc.narg(room_type_id),
    stay_from = sqlc.narg(stay_from), stay_to = sqlc.narg(stay_to), weekdays = sqlc.narg(weekdays),
    occupancy_from = sqlc.narg(occupancy_from), occupancy_to = sqlc.narg(occupancy_to),
    lead_min = sqlc.narg(lead_min), lead_max = sqlc.narg(lead_max), stay_min = sqlc.narg(stay_min), stay_max = sqlc.narg(stay_max),
    adjustment_type = @adjustment_type, adjustment_value = @adjustment_value,
    floor_amount = sqlc.narg(floor_amount), cap_amount = sqlc.narg(cap_amount), priority = @priority, is_active = @is_active,
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: DeleteYieldRule :execrows
DELETE FROM yield_rules WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertBedAdjustment :one
INSERT INTO rate_plan_bed_adjustments (tenant_id, property_id, rate_plan_id, room_type_id, bed_type_id, adjust_kind, amount, effective_from, created_by)
VALUES (@tenant_id, @property_id, @rate_plan_id, @room_type_id, @bed_type_id, @adjust_kind, @amount, @effective_from, sqlc.narg(actor_id))
RETURNING *;

-- name: ListBedAdjustments :many
SELECT a.id, a.rate_plan_id, a.room_type_id, rt.code AS room_type_code, a.bed_type_id, bt.code AS bed_type_code, bt.name AS bed_type_name,
       a.adjust_kind, a.amount, a.effective_from, a.created_at
FROM rate_plan_bed_adjustments a
JOIN room_types rt ON rt.property_id = a.property_id AND rt.id = a.room_type_id
JOIN bed_types bt ON bt.property_id = a.property_id AND bt.id = a.bed_type_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.rate_plan_id = @rate_plan_id
ORDER BY rt.code, bt.sort_order, bt.id, a.effective_from DESC;
