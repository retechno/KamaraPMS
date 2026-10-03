-- Rooms module queries (sqlc): room types, rooms, room blocks. Every query is scoped by tenant_id and property_id.

-- name: CreateRoomType :one
INSERT INTO room_types (
    tenant_id, property_id, code, name, description, max_adult, max_child, max_occupancy, base_occupancy,
    sort_order, is_active, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @code, @name, sqlc.narg(room_type_description), @max_adult, @max_child, @max_occupancy,
    @base_occupancy, @sort_order, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetRoomType :one
SELECT * FROM room_types WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRoomTypeForUpdate :one
SELECT * FROM room_types WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListRoomTypes :many
SELECT * FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY id
LIMIT @row_limit;

-- name: UpdateRoomType :one
UPDATE room_types SET
    name = @name,
    description = sqlc.narg(room_type_description),
    max_adult = @max_adult,
    max_child = @max_child,
    max_occupancy = @max_occupancy,
    base_occupancy = @base_occupancy,
    sort_order = @sort_order,
    is_active = @is_active,
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CountActiveRoomsOfType :one
SELECT count(*) FROM rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_type_id = @room_type_id AND is_active;

-- Lines that still hold inventory of the type from the business date on.
-- name: CountFutureConfirmedLinesOfType :one
SELECT count(*) FROM reservation_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_type_id = @room_type_id
  AND status = 'CONFIRMED' AND departure_date > @business_date::date;

-- name: CreateBedType :one
INSERT INTO bed_types (tenant_id, property_id, code, name, sort_order, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @sort_order, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: GetBedType :one
SELECT * FROM bed_types WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListBedTypes :many
SELECT * FROM bed_types
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY sort_order, code;

-- name: UpdateBedType :one
UPDATE bed_types SET name = @name, sort_order = @sort_order, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CountRoomsOfBedType :one
SELECT count(*) FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND bed_type_id = @bed_type_id;

-- The standard catalogue of a new property; the codes that exist are left alone.
-- name: SeedBedTypes :one
WITH ins AS (
    INSERT INTO bed_types (tenant_id, property_id, code, name, sort_order, created_by, updated_by)
    SELECT @tenant_id, @property_id, d.code, d.name, d.sort_order, sqlc.narg(actor_id), sqlc.narg(actor_id)
    FROM (VALUES ('KING', 'King', 10), ('QUEEN', 'Queen', 20), ('DOUBLE', 'Double', 30), ('TWIN', 'Twin', 40), ('SINGLE', 'Single', 50)) AS d (code, name, sort_order)
    ON CONFLICT (property_id, code) DO NOTHING
    RETURNING 1
)
SELECT count(*) FROM ins;

-- name: CreateRoom :one
INSERT INTO rooms (tenant_id, property_id, room_type_id, room_number, floor, building, bed_type_id, is_active, created_by, updated_by)
VALUES (
    @tenant_id, @property_id, @room_type_id, @room_number, sqlc.narg(floor), sqlc.narg(building), sqlc.narg(bed_type_id), @is_active,
    sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetRoom :one
SELECT * FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListRooms :many
SELECT * FROM rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id > @after_id
  AND (sqlc.narg(room_type_id)::bigint IS NULL OR room_type_id = sqlc.narg(room_type_id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY id
LIMIT @row_limit;

-- name: UpdateRoom :one
UPDATE rooms SET
    room_type_id = @room_type_id,
    room_number = @room_number,
    floor = sqlc.narg(floor),
    building = sqlc.narg(building),
    bed_type_id = sqlc.narg(bed_type_id),
    is_active = @is_active,
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Open stay segments on the room whose held nights [business_date, end_date) overlap [start_date, end_date).
-- An overstay keeps holding the room through tonight: next_date = business_date + 1 (passed by the caller).
-- name: ListRoomOpenSegmentConflicts :many
SELECT s.id AS stay_id, s.stay_number, s.departure_date
FROM stay_rooms sr
JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.room_id = @room_id
  AND sr.check_out_at IS NULL AND s.status = 'OPEN'
  AND @business_date::date < @end_date::date
  AND @start_date::date < GREATEST(s.departure_date, @next_date::date)
ORDER BY s.id;

-- name: ListRoomConfirmedLineConflicts :many
SELECT id AS reservation_room_id, reservation_id, arrival_date, departure_date
FROM reservation_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id
  AND status = 'CONFIRMED'
  AND arrival_date < @end_date::date AND @start_date::date < departure_date
ORDER BY arrival_date, id;

-- name: CreateRoomBlock :one
INSERT INTO room_blocks (tenant_id, property_id, room_id, block_type, start_date, end_date, reason, created_by, updated_by)
VALUES (
    @tenant_id, @property_id, @room_id, @block_type, @start_date, @end_date, @reason,
    sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetRoomBlock :one
SELECT * FROM room_blocks WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRoomBlockForUpdate :one
SELECT * FROM room_blocks WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- Blocks overlapping [from_date, to_date) (either bound optional), newest first.
-- name: ListRoomBlocks :many
SELECT * FROM room_blocks
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(before_id)::bigint IS NULL OR id < sqlc.narg(before_id)::bigint)
  AND (sqlc.narg(room_id)::bigint IS NULL OR room_id = sqlc.narg(room_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR end_date > sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR start_date < sqlc.narg(to_date)::date)
ORDER BY id DESC
LIMIT @row_limit;

-- name: UpdateRoomBlock :one
UPDATE room_blocks SET
    start_date = @start_date,
    end_date = @end_date,
    reason = @reason,
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CancelRoomBlock :one
UPDATE room_blocks SET
    status = 'CANCELLED',
    cancelled_at = @cancelled_at,
    cancelled_by = sqlc.narg(actor_id),
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE'
RETURNING *;
