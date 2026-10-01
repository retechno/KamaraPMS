-- Companies (corporate accounts). Every query is scoped by tenant_id and property_id.

-- name: CreateCompany :one
INSERT INTO companies (tenant_id, property_id, code, name, contact_name, email, phone, address, city, tax_id, credit_limit, payment_terms_days, notes, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(contact_name), sqlc.narg(email), sqlc.narg(phone), sqlc.narg(address), sqlc.narg(city), sqlc.narg(tax_id),
        sqlc.narg(credit_limit), @payment_terms_days, sqlc.narg(notes), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: GetCompany :one
SELECT * FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetCompanyForUpdate :one
SELECT * FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListCompanies :many
SELECT * FROM companies
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR code ILIKE '%' || sqlc.narg(q)::text || '%' OR name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY id
LIMIT @row_limit;

-- name: UpdateCompany :one
UPDATE companies SET
    name = @name, contact_name = sqlc.narg(contact_name), email = sqlc.narg(email), phone = sqlc.narg(phone), address = sqlc.narg(address),
    city = sqlc.narg(city), tax_id = sqlc.narg(tax_id), credit_limit = sqlc.narg(credit_limit), payment_terms_days = @payment_terms_days,
    notes = sqlc.narg(notes), is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Open balance of a company: transfers still owed less receipts (used to refuse deactivating an account that owes).
-- name: CompanyOwes :one
SELECT (
    COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = @property_id AND p.company_id = @id AND p.status = 'POSTED'), 0)
  - COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r WHERE r.property_id = @property_id AND r.company_id = @id AND r.status = 'POSTED'), 0)
)::numeric AS balance;
