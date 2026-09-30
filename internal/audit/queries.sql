-- name: InsertAuditLog :exec
INSERT INTO audit_logs (
    tenant_id, property_id, business_date, user_id, action, entity_type, entity_id,
    old_data, new_data, request_id, ip_address, created_at
) VALUES (
    @tenant_id, sqlc.narg(property_id), sqlc.narg(business_date), sqlc.narg(user_id), @action, @entity_type, @entity_id,
    @old_data, @new_data, sqlc.narg(request_id), sqlc.narg(ip_address), @created_at
);

-- name: ListAuditLogsForEntity :many
SELECT * FROM audit_logs
WHERE tenant_id = @tenant_id AND entity_type = @entity_type AND entity_id = @entity_id
ORDER BY created_at DESC, id DESC
LIMIT @row_limit;
