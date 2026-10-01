-- Housekeeping queries (sqlc). Every query is scoped by tenant_id and property_id.

-- name: CreateRoomHousekeeping :exec
INSERT INTO room_housekeeping (tenant_id, property_id, room_id, status, updated_by)
VALUES (@tenant_id, @property_id, @room_id, @status, sqlc.narg(actor_id));

-- name: GetRoomHousekeepingForUpdate :one
SELECT * FROM room_housekeeping
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id
FOR UPDATE;

-- name: SetRoomHousekeeping :one
UPDATE room_housekeeping SET status = @status, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id
RETURNING *;

-- name: InsertHousekeepingLog :one
INSERT INTO housekeeping_logs (
    tenant_id, property_id, room_id, from_status, to_status, source, business_date, notes, changed_at, changed_by
) VALUES (
    @tenant_id, @property_id, @room_id, @from_status, @to_status, @source, @business_date, sqlc.narg(notes),
    @changed_at, sqlc.narg(actor_id)
)
RETURNING *;

-- name: RoomExists :one
SELECT EXISTS (SELECT 1 FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @room_id);

-- name: ListHousekeepingLogs :many
SELECT * FROM housekeeping_logs
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
ORDER BY id DESC
LIMIT @row_limit;

-- The board: active rooms with their housekeeping status and derived occupancy for the business date.
-- Occupancy is never stored: OCCUPIED = open stay segment, RESERVED = CONFIRMED line assigned to the room
-- covering the date, otherwise VACANT.
-- name: ListHousekeepingBoard :many
SELECT
    r.id AS room_id,
    r.room_number,
    r.floor,
    r.building,
    rt.id AS room_type_id,
    rt.code AS room_type_code,
    rt.name AS room_type_name,
    h.status AS housekeeping_status,
    h.updated_at AS housekeeping_updated_at,
    CASE
        WHEN EXISTS (
            SELECT 1 FROM stay_rooms sr
            JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
            WHERE sr.property_id = r.property_id AND sr.room_id = r.id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
        ) THEN 'OCCUPIED'
        WHEN EXISTS (
            SELECT 1 FROM reservation_rooms rr
            WHERE rr.property_id = r.property_id AND rr.room_id = r.id AND rr.status = 'CONFIRMED'
              AND rr.arrival_date <= @business_date::date AND @business_date::date < rr.departure_date
        ) THEN 'RESERVED'
        ELSE 'VACANT'
    END::text AS occupancy,
    b.block_type AS block_type,
    b.end_date AS block_end_date,
    COALESCE(f.priority, 'NORMAL')::text AS priority,
    COALESCE(f.dnd, false)::boolean AS dnd,
    COALESCE(f.make_up_requested, false)::boolean AS make_up_requested,
    f.note AS flag_note
FROM rooms r
JOIN room_types rt ON rt.property_id = r.property_id AND rt.id = r.room_type_id
JOIN room_housekeeping h ON h.property_id = r.property_id AND h.room_id = r.id
LEFT JOIN room_hk_flags f ON f.property_id = r.property_id AND f.room_id = r.id
LEFT JOIN room_blocks b ON b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
    AND b.start_date <= @business_date::date AND @business_date::date < b.end_date
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.is_active
  AND (sqlc.narg(status)::text IS NULL OR h.status = sqlc.narg(status)::text)
  AND (sqlc.narg(floor)::text IS NULL OR r.floor = sqlc.narg(floor)::text)
ORDER BY r.floor NULLS LAST, r.room_number
LIMIT @row_limit;

-- name: GetRoomFlags :one
SELECT * FROM room_hk_flags WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id;

-- name: UpsertRoomFlags :one
INSERT INTO room_hk_flags (tenant_id, property_id, room_id, priority, dnd, make_up_requested, note, updated_by)
VALUES (@tenant_id, @property_id, @room_id, @priority, @dnd, @make_up_requested, sqlc.narg(note), sqlc.narg(actor_id))
ON CONFLICT (room_id) DO UPDATE SET priority = EXCLUDED.priority, dnd = EXCLUDED.dnd, make_up_requested = EXCLUDED.make_up_requested,
    note = EXCLUDED.note, updated_by = EXCLUDED.updated_by
RETURNING *;

-- Users who can be given cleaning work at a property: an active user with a role that holds housekeeping.update there,
-- or a tenant administrator. user_id narrows the list to one user (to check an assignee).
-- name: ListHousekeepingStaff :many
SELECT u.id, u.full_name, u.email
FROM users u
WHERE u.tenant_id = @tenant_id AND u.is_active
  AND (sqlc.narg(user_id)::bigint IS NULL OR u.id = sqlc.narg(user_id)::bigint)
  AND (u.is_tenant_admin OR EXISTS (
        SELECT 1 FROM user_properties up
        JOIN role_permissions rp ON rp.role_id = up.role_id AND rp.permission_code = 'housekeeping.update'
        WHERE up.user_id = u.id AND up.property_id = @property_id))
ORDER BY u.full_name, u.id;

-- The generated list for a business date: occupied rooms (CHECKOUT when the guest leaves today or is overdue,
-- STAYOVER otherwise), rooms a guest arrives into that are not ready, and other vacant rooms that are not clean. A room
-- that already has a task that day gets no second kind, and a room under a block gets none. Running it again only adds
-- what is new (the unique index on AUTO tasks is the backstop).
-- name: GenerateHousekeepingTasks :execrows
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, priority, source, created_by)
SELECT @tenant_id, @property_id, c.room_id, @task_date::date, c.task_type,
       CASE WHEN COALESCE(f.priority, 'NORMAL') = 'HIGH' OR EXISTS (
                SELECT 1 FROM reservation_rooms a
                WHERE a.property_id = @property_id AND a.room_id = c.room_id AND a.status = 'CONFIRMED' AND a.arrival_date = @task_date::date)
            THEN 'HIGH' ELSE 'NORMAL' END,
       'AUTO', sqlc.narg(actor_id)
FROM (
    SELECT sr.room_id, CASE WHEN s.departure_date <= @task_date::date THEN 'CHECKOUT' ELSE 'STAYOVER' END AS task_type
    FROM stay_rooms sr
    JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
    WHERE sr.property_id = @property_id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
    UNION ALL
    SELECT h.room_id,
           CASE WHEN EXISTS (
                SELECT 1 FROM reservation_rooms a
                WHERE a.property_id = @property_id AND a.room_id = h.room_id AND a.status = 'CONFIRMED' AND a.arrival_date = @task_date::date)
                THEN 'ARRIVAL' ELSE 'DIRTY' END
    FROM room_housekeeping h
    WHERE h.property_id = @property_id AND h.status IN ('DIRTY', 'CLEANING')
      AND NOT EXISTS (SELECT 1 FROM stay_rooms o JOIN stays os ON os.property_id = o.property_id AND os.id = o.stay_id
                      WHERE o.property_id = @property_id AND o.room_id = h.room_id AND o.check_out_at IS NULL AND os.status = 'OPEN')
      AND NOT EXISTS (SELECT 1 FROM housekeeping_tasks t
                      WHERE t.property_id = @property_id AND t.room_id = h.room_id AND t.task_date = @task_date::date AND t.status <> 'SKIPPED')
) c
JOIN rooms ro ON ro.property_id = @property_id AND ro.id = c.room_id AND ro.is_active
LEFT JOIN room_hk_flags f ON f.property_id = @property_id AND f.room_id = c.room_id
WHERE ro.tenant_id = @tenant_id
  AND NOT EXISTS (SELECT 1 FROM room_blocks b
                  WHERE b.property_id = @property_id AND b.room_id = c.room_id AND b.status = 'ACTIVE'
                    AND b.start_date <= @task_date::date AND @task_date::date < b.end_date)
ON CONFLICT (property_id, room_id, task_date, task_type) WHERE source = 'AUTO' DO NOTHING;

-- name: InsertManualTask :one
INSERT INTO housekeeping_tasks (tenant_id, property_id, room_id, task_date, task_type, priority, source, assigned_to, assigned_at, notes, created_by)
VALUES (@tenant_id, @property_id, @room_id, @task_date, @task_type, @priority, 'MANUAL', sqlc.narg(assigned_to),
        CASE WHEN sqlc.narg(assigned_to)::bigint IS NULL THEN NULL ELSE @now::timestamptz END, sqlc.narg(notes), sqlc.narg(actor_id))
RETURNING *;

-- name: GetHousekeepingTask :one
SELECT * FROM housekeeping_tasks WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListHousekeepingTasks :many
SELECT t.id, t.room_id, r.room_number, r.floor, rt.code AS room_type_code, t.task_date, t.task_type, t.status, t.priority, t.source,
       t.assigned_to, u.full_name AS assignee_name, t.notes, t.started_at, t.completed_at,
       h.status AS room_status, h.updated_at AS room_status_since,
       COALESCE(f.dnd, false)::boolean AS dnd, COALESCE(f.make_up_requested, false)::boolean AS make_up_requested, f.note AS flag_note
FROM housekeeping_tasks t
JOIN rooms r ON r.property_id = t.property_id AND r.id = t.room_id
JOIN room_types rt ON rt.property_id = r.property_id AND rt.id = r.room_type_id
JOIN room_housekeeping h ON h.property_id = t.property_id AND h.room_id = t.room_id
LEFT JOIN room_hk_flags f ON f.property_id = t.property_id AND f.room_id = t.room_id
LEFT JOIN users u ON u.tenant_id = t.tenant_id AND u.id = t.assigned_to
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.task_date = @task_date
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.narg(assigned_to)::bigint IS NULL OR t.assigned_to = sqlc.narg(assigned_to)::bigint)
  AND (NOT @unassigned::boolean OR t.assigned_to IS NULL)
  AND (sqlc.narg(floor)::text IS NULL OR r.floor = sqlc.narg(floor)::text)
ORDER BY (t.priority = 'HIGH') DESC, r.floor NULLS LAST, r.room_number, t.id
LIMIT @row_limit;

-- name: AssignHousekeepingTasks :many
UPDATE housekeeping_tasks
SET assigned_to = sqlc.narg(assigned_to), assigned_at = CASE WHEN sqlc.narg(assigned_to)::bigint IS NULL THEN NULL ELSE @now::timestamptz END
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]) AND status IN ('PENDING', 'IN_PROGRESS')
RETURNING id;

-- name: StartHousekeepingTask :one
UPDATE housekeeping_tasks
SET status = 'IN_PROGRESS', started_at = @now::timestamptz,
    assigned_to = COALESCE(assigned_to, sqlc.narg(actor_id)::bigint),
    assigned_at = CASE WHEN assigned_to IS NULL AND sqlc.narg(actor_id)::bigint IS NOT NULL THEN @now::timestamptz ELSE assigned_at END
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'PENDING'
RETURNING *;

-- name: CompleteHousekeepingTask :one
UPDATE housekeeping_tasks
SET status = 'DONE', started_at = COALESCE(started_at, @now::timestamptz), completed_at = @now::timestamptz, completed_by = sqlc.narg(actor_id),
    assigned_to = COALESCE(assigned_to, sqlc.narg(actor_id)::bigint),
    assigned_at = CASE WHEN assigned_to IS NULL AND sqlc.narg(actor_id)::bigint IS NOT NULL THEN @now::timestamptz ELSE assigned_at END,
    notes = COALESCE(sqlc.narg(notes), notes)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status IN ('PENDING', 'IN_PROGRESS')
RETURNING *;

-- name: SkipHousekeepingTask :one
UPDATE housekeeping_tasks
SET status = 'SKIPPED', completed_at = @now::timestamptz, completed_by = sqlc.narg(actor_id), notes = @reason,
    assigned_to = COALESCE(assigned_to, sqlc.narg(actor_id)::bigint),
    assigned_at = CASE WHEN assigned_to IS NULL AND sqlc.narg(actor_id)::bigint IS NOT NULL THEN @now::timestamptz ELSE assigned_at END
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status IN ('PENDING', 'IN_PROGRESS')
RETURNING *;
