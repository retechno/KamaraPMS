-- Front desk module queries (sqlc): stays, room segments and accompanying guests. Scoped by tenant and property.

-- name: InsertStay :one
INSERT INTO stays (
    tenant_id, property_id, stay_number, reservation_room_id, guest_id, arrival_date, departure_date, adult_count, child_count,
    actual_check_in_at, actual_check_in_by, idempotency_key, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @stay_number, @reservation_room_id, @guest_id, @arrival_date, @departure_date, @adult_count, @child_count,
    @check_in_at, sqlc.narg(actor_id), sqlc.narg(idempotency_key), sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetStay :one
SELECT * FROM stays WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetStayByKey :one
SELECT * FROM stays WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: CancelStay :one
UPDATE stays SET status = 'CANCELLED', version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: InsertStayRoom :one
INSERT INTO stay_rooms (tenant_id, property_id, stay_id, room_id, check_in_at, start_business_date, created_by)
VALUES (@tenant_id, @property_id, @stay_id, @room_id, @check_in_at, @start_business_date, sqlc.narg(actor_id))
RETURNING *;

-- name: CloseOpenSegment :one
UPDATE stay_rooms SET check_out_at = @check_out_at, end_business_date = @end_business_date, move_reason = sqlc.narg(reason)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id AND check_out_at IS NULL
RETURNING *;

-- name: InsertStayGuest :exec
INSERT INTO stay_guests (tenant_id, property_id, stay_id, guest_id, created_by)
VALUES (@tenant_id, @property_id, @stay_id, @guest_id, sqlc.narg(actor_id));

-- name: ListStayGuests :many
SELECT g.id, g.code, g.first_name, g.last_name FROM stay_guests sg
JOIN guests g ON g.tenant_id = sg.tenant_id AND g.id = sg.guest_id
WHERE sg.tenant_id = @tenant_id AND sg.property_id = @property_id AND sg.stay_id = @stay_id
ORDER BY g.last_name, g.id;

-- name: ListSegments :many
SELECT s.*, r.room_number FROM stay_rooms s
JOIN rooms r ON r.property_id = s.property_id AND r.id = s.room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.stay_id = @stay_id
ORDER BY s.start_business_date, s.id;

-- The reservation room of a stay, with the reservation's number.
-- name: GetStayLine :one
SELECT l.id, l.reservation_id, res.confirmation_number, l.room_type_id, t.code AS room_type_code, l.rate_plan_id, l.status, l.room_id, l.arrival_date, l.departure_date
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.id = @id;

-- Nightly snapshot of the stay's line, with whether the night is posted.
-- name: ListStayNights :many
SELECT r.stay_date, r.amount, r.price_mode, r.is_override,
       EXISTS (SELECT 1 FROM stay_charge_postings p
                WHERE p.property_id = r.property_id AND p.stay_id = @stay_id AND p.service_date = r.stay_date AND p.status = 'POSTED') AS posted
FROM reservation_room_rates r
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.reservation_room_id = @line_id
ORDER BY r.stay_date;

-- name: GetGuestBrief :one
SELECT id, code, first_name, last_name FROM guests WHERE tenant_id = @tenant_id AND id = @id;

-- Stays with the guest, the room of the open segment (else the last one) and the reservation number.
-- name: ListStays :many
SELECT s.*, g.first_name AS guest_first_name, g.last_name AS guest_last_name, res.confirmation_number, res.id AS reservation_id,
       (SELECT r.room_number FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
         WHERE sr.stay_id = s.id ORDER BY sr.check_out_at IS NULL DESC, sr.id DESC LIMIT 1) AS room_number,
       (SELECT sr.room_id FROM stay_rooms sr WHERE sr.stay_id = s.id ORDER BY sr.check_out_at IS NULL DESC, sr.id DESC LIMIT 1) AS room_id
FROM stays s
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND (@before_id::bigint = 0 OR s.id < @before_id::bigint)
  AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status)::text)
  AND (sqlc.narg(departure_date)::date IS NULL OR s.departure_date = sqlc.narg(departure_date)::date)
  AND (sqlc.narg(departure_until)::date IS NULL OR s.departure_date <= sqlc.narg(departure_until)::date)
  AND (sqlc.narg(room_id)::bigint IS NULL OR EXISTS (SELECT 1 FROM stay_rooms x WHERE x.stay_id = s.id AND x.room_id = sqlc.narg(room_id)::bigint AND x.check_out_at IS NULL))
ORDER BY s.id DESC
LIMIT @row_limit;

-- CONFIRMED rooms arriving on a date (the arrivals list).
-- name: ListArrivals :many
SELECT l.id AS line_id, l.reservation_id, res.confirmation_number, res.version AS reservation_version, l.room_type_id, t.code AS room_type_code,
       l.room_id, r.room_number, hk.status AS housekeeping_status, l.arrival_date, l.departure_date, l.adult_count, l.child_count, l.guest_id AS line_guest_id, res.guest_id AS booker_id,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name,
       l.requested_bed_type_id, rbt.code AS requested_bed_type_code, abt.code AS room_bed_type_code
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
LEFT JOIN rooms r ON r.property_id = l.property_id AND r.id = l.room_id
LEFT JOIN bed_types rbt ON rbt.property_id = l.property_id AND rbt.id = l.requested_bed_type_id
LEFT JOIN bed_types abt ON abt.property_id = r.property_id AND abt.id = r.bed_type_id
LEFT JOIN room_housekeeping hk ON hk.property_id = l.property_id AND hk.room_id = l.room_id
LEFT JOIN guests g ON g.tenant_id = res.tenant_id AND g.id = COALESCE(l.guest_id, res.guest_id)
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED' AND res.status = 'CONFIRMED' AND l.arrival_date = @arrival
ORDER BY t.sort_order, l.id;

-- name: GetRoomForCheckIn :one
SELECT r.id, r.room_number, r.room_type_id, r.is_active, h.status AS housekeeping_status
FROM rooms r JOIN room_housekeeping h ON h.property_id = r.property_id AND h.room_id = r.id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: GetRoomTypeLimits :one
SELECT id, code, is_active, max_adult, max_child, max_occupancy FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: UpdateStayDeparture :one
UPDATE stays SET departure_date = @departure_date, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: BumpStay :one
UPDATE stays SET version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CheckOutStay :one
UPDATE stays SET status = 'CHECKED_OUT', actual_check_out_at = @now::timestamptz, actual_check_out_by = sqlc.narg(actor_id),
    departure_date = @departure_date, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- The last night of the stay that has been charged (room postings); 0001-01-01 (the zero date) when none.
-- name: LastPostedNight :one
SELECT COALESCE(max(service_date), '0001-01-01'::date)::date AS last_night FROM stay_charge_postings
WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id AND status = 'POSTED';

-- The open segment of a stay: the room it is in now.
-- name: GetOpenSegment :one
SELECT s.*, r.room_number, r.room_type_id FROM stay_rooms s
JOIN rooms r ON r.property_id = s.property_id AND r.id = s.room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.stay_id = @stay_id AND s.check_out_at IS NULL;

-- name: HasStayGuest :one
SELECT EXISTS (SELECT 1 FROM stay_guests WHERE property_id = @property_id AND stay_id = @stay_id AND guest_id = @guest_id) AS present;

-- name: ListPostedNights :many
SELECT service_date FROM stay_charge_postings
WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id AND status = 'POSTED';
