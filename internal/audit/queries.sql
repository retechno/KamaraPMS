-- name: InsertAuditLog :exec
INSERT INTO audit_logs (
    tenant_id, property_id, business_date, user_id, action, entity_type, entity_id, entity_label,
    old_data, new_data, request_id, ip_address, created_at
) VALUES (
    @tenant_id, sqlc.narg(property_id), sqlc.narg(business_date), sqlc.narg(user_id), @action, @entity_type, @entity_id, sqlc.narg(entity_label),
    @old_data, @new_data, sqlc.narg(request_id), sqlc.narg(ip_address), @created_at
);

-- name: ListAuditLogsForEntity :many
SELECT * FROM audit_logs
WHERE tenant_id = @tenant_id AND entity_type = @entity_type AND entity_id = @entity_id
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;

-- The audit trail of a property, newest first, with the user's name. Keyset paging on the id (ids grow with time).
-- name: SearchAuditLogs :many
SELECT a.id, a.created_at, a.business_date, a.user_id, u.full_name AS user_name, a.action, a.entity_type, a.entity_id, a.entity_label,
       a.old_data, a.new_data, a.request_id, a.ip_address
FROM audit_logs a LEFT JOIN users u ON u.id = a.user_id
WHERE a.tenant_id = @tenant_id
  AND (CASE WHEN sqlc.narg(property_id)::bigint IS NULL THEN a.property_id IS NULL ELSE a.property_id = sqlc.narg(property_id)::bigint END)
  AND (sqlc.narg(entity_type)::text IS NULL OR a.entity_type = sqlc.narg(entity_type)::text)
  AND (sqlc.narg(entity_id)::bigint IS NULL OR a.entity_id = sqlc.narg(entity_id)::bigint)
  AND (sqlc.narg(user_id)::bigint IS NULL OR a.user_id = sqlc.narg(user_id)::bigint)
  AND (sqlc.narg(action)::text IS NULL OR a.action = sqlc.narg(action)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR a.business_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR a.business_date <= sqlc.narg(to_date)::date)
  -- a part of the label, in any case; the caller has escaped backslash, percent and underscore (the pattern is "%<q>%" with ESCAPE of a backslash), and asks for 3 characters or more so the trigram index serves it
  AND (sqlc.narg(label_like)::text IS NULL OR a.entity_label ILIKE sqlc.narg(label_like)::text ESCAPE '\')
  AND (sqlc.narg(before_id)::bigint IS NULL OR a.id < sqlc.narg(before_id)::bigint)
ORDER BY a.id DESC
LIMIT @row_limit;
