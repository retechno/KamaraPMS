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
    b.end_date AS block_end_date
FROM rooms r
JOIN room_types rt ON rt.property_id = r.property_id AND rt.id = r.room_type_id
JOIN room_housekeeping h ON h.property_id = r.property_id AND h.room_id = r.id
LEFT JOIN room_blocks b ON b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
    AND b.start_date <= @business_date::date AND @business_date::date < b.end_date
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.is_active
  AND (sqlc.narg(status)::text IS NULL OR h.status = sqlc.narg(status)::text)
  AND (sqlc.narg(floor)::text IS NULL OR r.floor = sqlc.narg(floor)::text)
ORDER BY r.floor NULLS LAST, r.room_number
LIMIT @row_limit;
