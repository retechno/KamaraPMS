-- Departments of a property. Every query is scoped by tenant_id and property_id.

-- name: ListDepartments :many
SELECT d.id, d.parent_id, p.code AS parent_code, d.code, d.name, d.sort_order, d.is_active,
       (SELECT count(*) FROM departments c WHERE c.property_id = d.property_id AND c.parent_id = d.id)::int AS child_count,
       (EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = d.property_id AND l.department_id = d.id)
        OR EXISTS (SELECT 1 FROM folio_items i WHERE i.property_id = d.property_id AND i.department_id = d.id)
        OR EXISTS (SELECT 1 FROM charge_codes c WHERE c.property_id = d.property_id AND c.department_id = d.id)
        OR EXISTS (SELECT 1 FROM supplier_bill_lines b WHERE b.property_id = d.property_id AND b.department_id = d.id)
        OR EXISTS (SELECT 1 FROM budget_lines bl WHERE bl.property_id = d.property_id AND bl.department_id = d.id))::boolean AS in_use
FROM departments d
LEFT JOIN departments p ON p.property_id = d.property_id AND p.id = d.parent_id
WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR d.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR d.is_active = sqlc.narg(active)::boolean)
ORDER BY COALESCE(p.sort_order, d.sort_order), COALESCE(p.code, d.code), (d.parent_id IS NOT NULL), d.sort_order, d.code;

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
SELECT seed_departments(@tenant_id, @property_id, sqlc.narg(actor_id))::int AS created;
