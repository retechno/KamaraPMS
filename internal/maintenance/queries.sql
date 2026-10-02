-- Maintenance requests (sqlc). Every query is scoped by tenant_id and property_id.

-- name: InsertRequest :one
INSERT INTO maintenance_requests (
    tenant_id, property_id, request_number, room_id, location, category, description, priority, business_date, reported_by, reported_at
) VALUES (
    @tenant_id, @property_id, @request_number, sqlc.narg(room_id), sqlc.narg(location), @category, @description, @priority, @business_date,
    sqlc.narg(actor_id), @now
)
RETURNING id;

-- name: GetRequestForUpdate :one
SELECT * FROM maintenance_requests WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- A request with the names around it: the room, who reported it, who works on it, and the block taken for it.
-- name: ListRequests :many
SELECT m.id, m.request_number, m.room_id, ro.room_number, m.location, m.category, m.description, m.priority, m.status, m.business_date,
       m.reported_by, rep.full_name AS reporter_name, m.reported_at, m.assigned_to, asg.full_name AS assignee_name, m.started_at, m.closed_at,
       m.resolution_note, m.room_block_id, b.block_type, b.start_date AS block_start, b.end_date AS block_end, b.status AS block_status
FROM maintenance_requests m
LEFT JOIN rooms ro ON ro.property_id = m.property_id AND ro.id = m.room_id
LEFT JOIN users rep ON rep.tenant_id = m.tenant_id AND rep.id = m.reported_by
LEFT JOIN users asg ON asg.tenant_id = m.tenant_id AND asg.id = m.assigned_to
LEFT JOIN room_blocks b ON b.property_id = m.property_id AND b.id = m.room_block_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR m.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(before_id)::bigint IS NULL OR m.id < sqlc.narg(before_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR m.status = sqlc.narg(status)::text)
  AND (NOT @open_only::boolean OR m.status IN ('OPEN', 'IN_PROGRESS'))
  AND (sqlc.narg(room_id)::bigint IS NULL OR m.room_id = sqlc.narg(room_id)::bigint)
  AND (sqlc.narg(assigned_to)::bigint IS NULL OR m.assigned_to = sqlc.narg(assigned_to)::bigint)
  AND (sqlc.narg(category)::text IS NULL OR m.category = sqlc.narg(category)::text)
  AND (sqlc.narg(priority)::text IS NULL OR m.priority = sqlc.narg(priority)::text)
ORDER BY m.id DESC
LIMIT @row_limit;

-- name: UpdateRequestDetails :exec
UPDATE maintenance_requests SET category = @category, description = @description, priority = @priority, location = sqlc.narg(location)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: AssignRequest :exec
UPDATE maintenance_requests
SET assigned_to = sqlc.narg(assigned_to), assigned_at = CASE WHEN sqlc.narg(assigned_to)::bigint IS NULL THEN NULL ELSE @now::timestamptz END
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: StartRequest :exec
UPDATE maintenance_requests
SET status = 'IN_PROGRESS', started_at = @now::timestamptz,
    assigned_to = COALESCE(assigned_to, sqlc.narg(actor_id)::bigint),
    assigned_at = CASE WHEN assigned_to IS NULL AND sqlc.narg(actor_id)::bigint IS NOT NULL THEN @now::timestamptz ELSE assigned_at END
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: CloseRequest :exec
UPDATE maintenance_requests
SET status = @status, closed_at = @now::timestamptz, closed_by = sqlc.narg(actor_id), resolution_note = sqlc.narg(note)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ReopenRequest :exec
UPDATE maintenance_requests SET status = 'OPEN', closed_at = NULL, closed_by = NULL, resolution_note = NULL, started_at = NULL
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: LinkRequestBlock :exec
UPDATE maintenance_requests SET room_block_id = @room_block_id
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: RoomNumber :one
SELECT room_number FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Users who can take maintenance work: an active user with a role that holds maintenance.report or maintenance.manage
-- at the property, or a tenant administrator. user_id narrows the list to one user (to check an assignee).
-- name: ListMaintenanceStaff :many
SELECT u.id, u.full_name, u.email
FROM users u
WHERE u.tenant_id = @tenant_id AND u.is_active
  AND (sqlc.narg(user_id)::bigint IS NULL OR u.id = sqlc.narg(user_id)::bigint)
  AND (u.is_tenant_admin OR EXISTS (
        SELECT 1 FROM user_properties up
        JOIN role_permissions rp ON rp.role_id = up.role_id AND rp.permission_code IN ('maintenance.report', 'maintenance.manage')
        WHERE up.user_id = u.id AND up.property_id = @property_id))
ORDER BY u.full_name, u.id;
