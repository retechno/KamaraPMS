-- Billing configuration queries (sqlc): taxes, service charges, charge codes and their ordered rules.
-- Every query is scoped by tenant_id and property_id.

-- Seeds the standard charge codes (idempotent). Returns how many were created.
-- name: SeedChargeCodes :one
SELECT seed_charge_codes(@tenant_id::bigint, @property_id::bigint, sqlc.narg(actor_id)::bigint)::integer AS created;

-- ---------------------------------------------------------------- taxes

-- name: CreateTax :one
INSERT INTO taxes (tenant_id, property_id, code, name, rate, tax_on_service, tax_kind, gl_account_code, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @rate, @tax_on_service, @tax_kind, sqlc.narg(gl_account_code), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: GetTax :one
SELECT * FROM taxes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetTaxForUpdate :one
SELECT * FROM taxes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListTaxes :many
SELECT * FROM taxes
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY id
LIMIT @row_limit;

-- name: UpdateTax :one
UPDATE taxes SET name = @name, rate = @rate, tax_on_service = @tax_on_service, tax_kind = @tax_kind, gl_account_code = sqlc.narg(gl_account_code), is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CountActiveMappingsOfTax :one
SELECT count(*) FROM charge_code_taxes
WHERE tenant_id = @tenant_id AND property_id = @property_id AND tax_id = @tax_id AND is_active;

-- Open stays with nights still to be charged through a charge code that maps this tax.
-- name: CountOpenStaysAffectedByTax :one
SELECT count(DISTINCT s.id) FROM stays s
JOIN reservation_room_rates r ON r.property_id = s.property_id AND r.reservation_room_id = s.reservation_room_id
JOIN charge_code_taxes m ON m.property_id = r.property_id AND m.charge_code_id = r.charge_code_id AND m.is_active
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN' AND m.tax_id = @tax_id;

-- Share-locks the taxes a rule set maps, so they cannot be deactivated while the mapping is written.
-- name: LockTaxesForShare :many
SELECT id, is_active FROM taxes
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY (@ids::bigint[])
ORDER BY id
FOR SHARE;

-- ---------------------------------------------------------------- service charges

-- name: CreateServiceCharge :one
INSERT INTO service_charges (tenant_id, property_id, code, name, rate, gl_account_code, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @rate, sqlc.narg(gl_account_code), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: GetServiceCharge :one
SELECT * FROM service_charges WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetServiceChargeForUpdate :one
SELECT * FROM service_charges WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListServiceCharges :many
SELECT * FROM service_charges
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY id
LIMIT @row_limit;

-- name: UpdateServiceCharge :one
UPDATE service_charges SET name = @name, rate = @rate, gl_account_code = sqlc.narg(gl_account_code), is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CountActiveMappingsOfServiceCharge :one
SELECT count(*) FROM charge_code_service_charges
WHERE tenant_id = @tenant_id AND property_id = @property_id AND service_charge_id = @service_charge_id AND is_active;

-- name: CountOpenStaysAffectedByServiceCharge :one
SELECT count(DISTINCT s.id) FROM stays s
JOIN reservation_room_rates r ON r.property_id = s.property_id AND r.reservation_room_id = s.reservation_room_id
JOIN charge_code_service_charges m ON m.property_id = r.property_id AND m.charge_code_id = r.charge_code_id AND m.is_active
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN' AND m.service_charge_id = @service_charge_id;

-- name: LockServiceChargesForShare :many
SELECT id, is_active FROM service_charges
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY (@ids::bigint[])
ORDER BY id
FOR SHARE;

-- ---------------------------------------------------------------- charge codes

-- name: CreateChargeCode :one
INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode, default_unit_price, gl_account_code, department_id, is_system, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @charge_type, @price_mode, sqlc.narg(default_unit_price), sqlc.narg(gl_account_code), sqlc.narg(department_id), false, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: GetChargeCode :one
SELECT * FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetChargeCodeForUpdate :one
SELECT * FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListChargeCodes :many
SELECT * FROM charge_codes
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(charge_type)::text IS NULL OR charge_type = sqlc.narg(charge_type)::text)
ORDER BY id
LIMIT @row_limit;

-- name: UpdateChargeCode :one
UPDATE charge_codes SET
    name = @name, charge_type = @charge_type, price_mode = @price_mode,
    default_unit_price = sqlc.narg(default_unit_price), gl_account_code = sqlc.narg(gl_account_code), department_id = sqlc.narg(department_id), is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Active rate plans that sell through this charge code (it cannot be deactivated while they do).
-- name: CountActiveRatePlansUsingChargeCode :one
SELECT count(*) FROM rate_plans
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_charge_code_id = @charge_code_id AND is_active;

-- Active rules of every charge code of the property (ordered), joined with the tax or service charge.
-- name: ListActiveTaxRules :many
SELECT m.charge_code_id, m.sequence, t.id AS tax_id, t.code, t.name, t.rate, t.tax_on_service, t.is_active AS tax_is_active
FROM charge_code_taxes m
JOIN taxes t ON t.property_id = m.property_id AND t.id = m.tax_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.is_active
  AND (sqlc.narg(charge_code_id)::bigint IS NULL OR m.charge_code_id = sqlc.narg(charge_code_id)::bigint)
ORDER BY m.charge_code_id, m.sequence;

-- name: ListActiveServiceRules :many
SELECT m.charge_code_id, m.sequence, s.id AS service_charge_id, s.code, s.name, s.rate, s.is_active AS service_is_active
FROM charge_code_service_charges m
JOIN service_charges s ON s.property_id = m.property_id AND s.id = m.service_charge_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.is_active
  AND (sqlc.narg(charge_code_id)::bigint IS NULL OR m.charge_code_id = sqlc.narg(charge_code_id)::bigint)
ORDER BY m.charge_code_id, m.sequence;

-- Rule replacement: deactivate everything, then upsert the listed rules. The partial unique index
-- (charge code, sequence) only covers active rows, so reordering never collides with itself.
-- name: DeactivateChargeCodeTaxes :exec
UPDATE charge_code_taxes SET is_active = false
WHERE tenant_id = @tenant_id AND property_id = @property_id AND charge_code_id = @charge_code_id AND is_active;

-- name: UpsertChargeCodeTax :exec
INSERT INTO charge_code_taxes (tenant_id, property_id, charge_code_id, tax_id, sequence, is_active, created_by)
VALUES (@tenant_id, @property_id, @charge_code_id, @tax_id, @sequence, true, sqlc.narg(actor_id))
ON CONFLICT (charge_code_id, tax_id) DO UPDATE SET sequence = EXCLUDED.sequence, is_active = true;

-- name: DeactivateChargeCodeServiceCharges :exec
UPDATE charge_code_service_charges SET is_active = false
WHERE tenant_id = @tenant_id AND property_id = @property_id AND charge_code_id = @charge_code_id AND is_active;

-- name: UpsertChargeCodeServiceCharge :exec
INSERT INTO charge_code_service_charges (tenant_id, property_id, charge_code_id, service_charge_id, sequence, is_active, created_by)
VALUES (@tenant_id, @property_id, @charge_code_id, @service_charge_id, @sequence, true, sqlc.narg(actor_id))
ON CONFLICT (charge_code_id, service_charge_id) DO UPDATE SET sequence = EXCLUDED.sequence, is_active = true;

-- The department rule of an account, to check a charge code, a tax or a service charge against it before it is saved.
-- name: AccountDepartmentRule :one
SELECT a.code::text AS code, a.name::text AS name, a.department_requirement::text AS requirement, (a.default_department_id IS NOT NULL AND d.is_active)::boolean AS default_ok
FROM gl_accounts a
LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.default_department_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.code = @code;
