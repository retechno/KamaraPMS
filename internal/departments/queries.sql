-- Departments of a property. Every query is scoped by tenant_id and property_id.

-- name: ListDepartments :many
SELECT d.id, d.parent_id, p.code AS parent_code, d.code, d.name, d.sort_order, d.is_active,
       d.head_user_id, hu.full_name AS head_name, d.deputy_user_id, du.full_name AS deputy_name,
       (SELECT count(*) FROM departments c WHERE c.property_id = d.property_id AND c.parent_id = d.id)::int AS child_count,
       (EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = d.property_id AND l.department_id = d.id)
        OR EXISTS (SELECT 1 FROM folio_items i WHERE i.property_id = d.property_id AND i.department_id = d.id)
        OR EXISTS (SELECT 1 FROM charge_codes c WHERE c.property_id = d.property_id AND c.department_id = d.id)
        OR EXISTS (SELECT 1 FROM supplier_bill_lines b WHERE b.property_id = d.property_id AND b.department_id = d.id)
        OR EXISTS (SELECT 1 FROM budget_lines bl WHERE bl.property_id = d.property_id AND bl.department_id = d.id)
        OR EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = d.property_id AND a.default_department_id = d.id))::boolean AS in_use
FROM departments d
LEFT JOIN departments p ON p.property_id = d.property_id AND p.id = d.parent_id
LEFT JOIN users hu ON hu.tenant_id = d.tenant_id AND hu.id = d.head_user_id
LEFT JOIN users du ON du.tenant_id = d.tenant_id AND du.id = d.deputy_user_id
WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR d.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR d.is_active = sqlc.narg(active)::boolean)
ORDER BY COALESCE(p.sort_order, d.sort_order), COALESCE(p.code, d.code), (d.parent_id IS NOT NULL), d.sort_order, d.code;

-- The accounts that take a department as their default, which stop it from being switched off.
-- name: AccountsDefaultingTo :many
SELECT a.code::text FROM gl_accounts a WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.default_department_id = @id ORDER BY a.code LIMIT 5;

-- name: InsertDepartment :one
INSERT INTO departments (tenant_id, property_id, parent_id, code, name, sort_order, created_by, updated_by)
VALUES (@tenant_id, @property_id, sqlc.narg(parent_id), @code, @name, @sort_order, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateDepartment :exec
UPDATE departments SET name = @name, sort_order = @sort_order, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteDepartment :exec
DELETE FROM departments WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SeedDepartments :one
SELECT seed_departments(@tenant_id, @property_id, sqlc.narg(actor_id), @data_language::text)::int AS created;

-- name: UpdateDepartmentHeads :exec
UPDATE departments SET head_user_id = sqlc.narg(head_user_id), deputy_user_id = sqlc.narg(deputy_user_id), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Whether a user can be the head of a department of the property: active, and either a tenant administrator or assigned to the property.
-- name: UserMayLead :one
SELECT (u.is_active AND (u.is_tenant_admin OR EXISTS (SELECT 1 FROM user_properties up WHERE up.tenant_id = u.tenant_id AND up.user_id = u.id AND up.property_id = @property_id)))::boolean AS ok
FROM users u WHERE u.tenant_id = @tenant_id AND u.id = @user_id;

-- The people who can be the head or the deputy of a department of the property: active users who are tenant administrators or have access to it.
-- name: ListHeadCandidates :many
SELECT u.id, u.full_name, u.email, u.is_tenant_admin FROM users u
 WHERE u.tenant_id = @tenant_id AND u.is_active
   AND (u.is_tenant_admin OR EXISTS (SELECT 1 FROM user_properties up WHERE up.tenant_id = u.tenant_id AND up.user_id = u.id AND up.property_id = @property_id))
 ORDER BY u.full_name, u.id;
