-- Accounting (sqlc): chart of accounts, the system account map and journals. Every query is scoped by tenant_id and property_id.

-- name: SeedChart :one
SELECT seed_chart_of_accounts(@tenant_id, @property_id, sqlc.narg(actor_id), @start_date::date)::int AS created;

-- name: GetSettings :one
SELECT * FROM accounting_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- An account with its parent's code and whether anything refers to it (children, the system map, a charge code, tax or
-- service charge that carries its code), which is what stops it from being deleted or deactivated.
-- name: ListAccounts :many
SELECT a.id, a.code, a.name, a.account_type, a.normal_side, a.parent_id, p.code AS parent_code, a.is_postable, a.is_active, a.statement_group,
       a.description, a.created_at,
       (EXISTS (SELECT 1 FROM gl_accounts c WHERE c.property_id = a.property_id AND c.parent_id = a.id)
        OR EXISTS (SELECT 1 FROM gl_account_map m WHERE m.property_id = a.property_id AND m.account_id = a.id)
        OR EXISTS (SELECT 1 FROM charge_codes cc WHERE cc.property_id = a.property_id AND cc.gl_account_code = a.code)
        OR EXISTS (SELECT 1 FROM taxes t WHERE t.property_id = a.property_id AND t.gl_account_code = a.code)
        OR EXISTS (SELECT 1 FROM service_charges s WHERE s.property_id = a.property_id AND s.gl_account_code = a.code))::boolean AS in_use
FROM gl_accounts a
LEFT JOIN gl_accounts p ON p.property_id = a.property_id AND p.id = a.parent_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR a.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(account_type)::text IS NULL OR a.account_type = sqlc.narg(account_type)::text)
  AND (sqlc.narg(statement_group)::text IS NULL OR a.statement_group = sqlc.narg(statement_group)::text)
  AND (sqlc.narg(active)::boolean IS NULL OR a.is_active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(postable)::boolean IS NULL OR a.is_postable = sqlc.narg(postable)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR a.code ILIKE '%' || sqlc.narg(q)::text || '%' OR a.name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY a.code
LIMIT @row_limit;

-- name: GetAccountByCode :one
SELECT * FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: GetAccountRow :one
SELECT * FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: CreateAccount :one
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, is_active, statement_group, description, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @account_type, @normal_side, sqlc.narg(parent_id), @is_postable, @is_active, sqlc.narg(statement_group), sqlc.narg(description),
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateAccount :exec
UPDATE gl_accounts
SET name = @name, parent_id = sqlc.narg(parent_id), is_postable = @is_postable, is_active = @is_active, statement_group = sqlc.narg(statement_group),
    description = sqlc.narg(description), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteAccount :exec
DELETE FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Accounts that stand above an account, to refuse a parent that would make a loop.
-- name: AncestorIDs :many
WITH RECURSIVE up AS (
    SELECT g.id, g.parent_id, 1 AS depth FROM gl_accounts g WHERE g.property_id = @property_id AND g.id = @id
    UNION ALL
    SELECT a.id, a.parent_id, up.depth + 1 FROM gl_accounts a JOIN up ON a.property_id = @property_id AND a.id = up.parent_id WHERE up.depth < 20
)
SELECT id FROM up;

-- name: CountChildren :one
SELECT count(*)::int FROM gl_accounts g WHERE g.property_id = @property_id AND g.parent_id = @id;

-- name: CountAccountReferences :one
SELECT ((SELECT count(*) FROM gl_account_map m WHERE m.property_id = @property_id AND m.account_id = @id)
      + (SELECT count(*) FROM charge_codes c WHERE c.property_id = @property_id AND c.gl_account_code = @code)
      + (SELECT count(*) FROM taxes t WHERE t.property_id = @property_id AND t.gl_account_code = @code)
      + (SELECT count(*) FROM service_charges s WHERE s.property_id = @property_id AND s.gl_account_code = @code))::int AS refs;

-- name: ListAccountMap :many
SELECT m.map_key, m.account_id, a.code AS account_code, a.name AS account_name, a.account_type, m.updated_at
FROM gl_account_map m
JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id
ORDER BY m.map_key;

-- name: SetAccountMap :exec
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id, updated_by)
VALUES (@tenant_id, @property_id, @map_key, @account_id, sqlc.narg(actor_id))
ON CONFLICT (property_id, map_key) DO UPDATE SET account_id = EXCLUDED.account_id, updated_by = EXCLUDED.updated_by, updated_at = now();

-- What carries an account code and whether the code is a usable account of the chart: charge codes, taxes and service
-- charges. A code that is empty or names no active postable account of the right kind is posted to a fallback account.
-- name: ListCodeUsage :many
SELECT u.kind, u.id, u.code, u.name, u.is_active, u.gl_account_code,
       a.id AS account_id, a.is_active AS account_active, a.is_postable AS account_postable, a.account_type
FROM (
    SELECT 'CHARGE_CODE'::text AS kind, c.id, c.code, c.name, c.is_active, c.gl_account_code FROM charge_codes c
    WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
    UNION ALL
    SELECT 'TAX', t.id, t.code, t.name, t.is_active, t.gl_account_code FROM taxes t
    WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
    UNION ALL
    SELECT 'SERVICE_CHARGE', s.id, s.code, s.name, s.is_active, s.gl_account_code FROM service_charges s
    WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
) u
LEFT JOIN gl_accounts a ON a.property_id = @property_id AND a.code = u.gl_account_code
ORDER BY u.kind, u.code;
