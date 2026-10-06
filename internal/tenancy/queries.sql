-- Tenancy queries (sqlc). Every property query is scoped by tenant_id.

-- name: CreateTenant :one
INSERT INTO tenants (code, name, timezone)
VALUES (@code, @name, @timezone)
RETURNING *;

-- name: GetTenantByCode :one
SELECT * FROM tenants WHERE code = @code;

-- name: CreateProperty :one
INSERT INTO properties (
    tenant_id, code, name, address, city, country_code, phone, email, tax_id, document_footer, timezone, currency_code, currency_decimals,
    check_in_time, check_out_time, require_room_inspection_for_checkin,
    night_audit_marks_occupied_dirty, night_audit_earliest_time, refund_methods, created_by, updated_by
) VALUES (
    @tenant_id, @code, @name, sqlc.narg(address), sqlc.narg(city), sqlc.narg(country_code), sqlc.narg(phone), sqlc.narg(email), sqlc.narg(tax_id), sqlc.narg(document_footer), @timezone,
    @currency_code, @currency_decimals, @check_in_time, @check_out_time, @require_room_inspection_for_checkin,
    @night_audit_marks_occupied_dirty, @night_audit_earliest_time, @refund_methods, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetProperty :one
SELECT * FROM properties WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetPropertyForUpdate :one
SELECT * FROM properties WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: ListProperties :many
SELECT * FROM properties
WHERE tenant_id = @tenant_id AND id > @after_id
ORDER BY id
LIMIT @row_limit;

-- name: ListPropertiesForUser :many
SELECT p.* FROM properties p
JOIN user_properties up ON up.property_id = p.id AND up.tenant_id = p.tenant_id
WHERE p.tenant_id = @tenant_id AND up.user_id = @user_id AND p.id > @after_id
ORDER BY p.id
LIMIT @row_limit;

-- name: UpdateProperty :one
UPDATE properties SET
    name = @name,
    address = sqlc.narg(address),
    city = sqlc.narg(city),
    country_code = sqlc.narg(country_code),
    phone = sqlc.narg(phone),
    email = sqlc.narg(email),
    tax_id = sqlc.narg(tax_id),
    document_footer = sqlc.narg(document_footer),
    timezone = @timezone,
    currency_code = @currency_code,
    currency_decimals = @currency_decimals,
    check_in_time = @check_in_time,
    check_out_time = @check_out_time,
    require_room_inspection_for_checkin = @require_room_inspection_for_checkin,
    night_audit_marks_occupied_dirty = @night_audit_marks_occupied_dirty,
    night_audit_earliest_time = @night_audit_earliest_time,
    refund_methods = @refund_methods,
    status = @status,
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- The one definition of "the property has financial data" (migration 00058), shared with the trigger that locks the currency.
-- name: PropertyHasFinancialData :one
SELECT property_has_financial_data(@property_id)::boolean;

-- name: InsertBusinessDay :one
INSERT INTO business_days (tenant_id, property_id, business_date, opened_at, opened_by)
VALUES (@tenant_id, @property_id, @business_date, @opened_at, sqlc.narg(opened_by))
RETURNING *;

-- name: GetOpenBusinessDay :one
SELECT * FROM business_days WHERE property_id = @property_id AND status = 'OPEN';

-- name: LockOpenBusinessDayForShare :one
SELECT * FROM business_days WHERE property_id = @property_id AND status = 'OPEN' FOR SHARE;

-- name: LockOpenBusinessDayForUpdate :one
SELECT * FROM business_days WHERE property_id = @property_id AND status = 'OPEN' FOR UPDATE;

-- name: CloseBusinessDay :one
UPDATE business_days SET
    status = 'CLOSED',
    closed_at = @closed_at,
    closed_by = sqlc.narg(closed_by),
    summary = @summary
WHERE id = @id AND status = 'OPEN'
RETURNING *;

-- name: ListBusinessDays :many
SELECT * FROM business_days
WHERE property_id = @property_id AND business_date < @before_date
ORDER BY business_date DESC
LIMIT @row_limit;

-- name: CreateDocumentSequence :exec
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
VALUES (@tenant_id, @property_id, @sequence_type, @prefix);

-- Gapless: the increment is part of the business transaction, so a rollback returns the number.
-- name: NextDocumentNumber :one
UPDATE document_sequences
SET next_value = next_value + 1, updated_at = now()
WHERE property_id = @property_id AND sequence_type = @sequence_type
RETURNING prefix, (next_value - 1)::bigint AS number;
