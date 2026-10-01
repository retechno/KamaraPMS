-- Lost and found (sqlc). Every query is scoped by tenant_id and property_id.

-- name: InsertItem :one
INSERT INTO lost_found_items (
    tenant_id, property_id, item_number, description, category, room_id, location, found_on, found_at, found_by,
    storage_location, possible_owner, notes
) VALUES (
    @tenant_id, @property_id, @item_number, @description, @category, sqlc.narg(room_id), sqlc.narg(location), @found_on, @now, sqlc.narg(actor_id),
    sqlc.narg(storage_location), sqlc.narg(possible_owner), sqlc.narg(notes)
)
RETURNING id;

-- name: GetItemForUpdate :one
SELECT * FROM lost_found_items WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: ListItems :many
SELECT i.id, i.item_number, i.description, i.category, i.room_id, ro.room_number, i.location, i.found_on, i.found_at,
       i.found_by, fu.full_name AS finder_name, i.storage_location, i.possible_owner, i.notes, i.status,
       i.closed_on, i.closed_at, cu.full_name AS closer_name, i.claimant_name, i.claimant_proof, i.close_note
FROM lost_found_items i
LEFT JOIN rooms ro ON ro.property_id = i.property_id AND ro.id = i.room_id
LEFT JOIN users fu ON fu.tenant_id = i.tenant_id AND fu.id = i.found_by
LEFT JOIN users cu ON cu.tenant_id = i.tenant_id AND cu.id = i.closed_by
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR i.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(before_id)::bigint IS NULL OR i.id < sqlc.narg(before_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR i.status = sqlc.narg(status)::text)
  AND (sqlc.narg(category)::text IS NULL OR i.category = sqlc.narg(category)::text)
  AND (sqlc.narg(room_id)::bigint IS NULL OR i.room_id = sqlc.narg(room_id)::bigint)
  AND (sqlc.narg(found_from)::date IS NULL OR i.found_on >= sqlc.narg(found_from)::date)
  AND (sqlc.narg(found_to)::date IS NULL OR i.found_on <= sqlc.narg(found_to)::date)
  AND (sqlc.narg(q)::text IS NULL OR i.item_number ILIKE '%' || sqlc.narg(q)::text || '%' OR i.description ILIKE '%' || sqlc.narg(q)::text || '%'
       OR i.claimant_name ILIKE '%' || sqlc.narg(q)::text || '%' OR i.possible_owner ILIKE '%' || sqlc.narg(q)::text || '%'
       OR i.storage_location ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY i.id DESC
LIMIT @row_limit;

-- name: UpdateItemDetails :exec
UPDATE lost_found_items
SET description = @description, category = @category, location = sqlc.narg(location), storage_location = sqlc.narg(storage_location),
    possible_owner = sqlc.narg(possible_owner), notes = sqlc.narg(notes)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: CloseItem :exec
UPDATE lost_found_items
SET status = @status, closed_on = @closed_on, closed_at = @now::timestamptz, closed_by = sqlc.narg(actor_id),
    claimant_name = sqlc.narg(claimant_name), claimant_proof = sqlc.narg(claimant_proof), close_note = sqlc.narg(close_note)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: RoomExists :one
SELECT EXISTS (SELECT 1 FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id);

-- Guests who had the room around the day the item was found: stays whose room segment covered the day, or ended in the
-- few days before it. The most recent first.
-- name: PossibleOwners :many
SELECT s.id AS stay_id, s.stay_number, s.arrival_date, s.departure_date, s.status AS stay_status,
       COALESCE(NULLIF(trim(COALESCE(g.first_name, '') || ' ' || g.last_name), ''), '')::text AS guest_name, g.phone, g.email
FROM stay_rooms sr
JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.room_id = @room_id
  AND sr.start_business_date <= @found_on::date
  AND (sr.end_business_date IS NULL OR sr.end_business_date >= @since::date)
  AND s.status <> 'CANCELLED'
ORDER BY sr.start_business_date DESC, s.id DESC
LIMIT 20;
