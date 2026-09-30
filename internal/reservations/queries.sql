-- Reservations module queries (sqlc). Scoped by tenant_id and property_id.

-- name: InsertReservation :one
INSERT INTO reservations (
    tenant_id, property_id, confirmation_number, guest_id, reservation_date, source, market, special_request, remarks,
    idempotency_key, idempotency_hash, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @confirmation_number, sqlc.narg(guest_id), @reservation_date, @source, sqlc.narg(market),
    sqlc.narg(special_request), sqlc.narg(remarks), sqlc.narg(idempotency_key), sqlc.narg(idempotency_hash),
    sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetReservation :one
SELECT * FROM reservations WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetReservationByKey :one
SELECT * FROM reservations WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- Header edit; version + 1.
-- name: UpdateReservationHeader :one
UPDATE reservations SET
    guest_id = sqlc.narg(guest_id), source = @source, market = sqlc.narg(market), special_request = sqlc.narg(special_request),
    remarks = sqlc.narg(remarks), version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: ConfirmReservation :one
UPDATE reservations SET
    status = 'CONFIRMED', confirmed_at = COALESCE(confirmed_at, @now::timestamptz), confirmed_by = COALESCE(confirmed_by, sqlc.narg(actor_id)),
    cancelled_at = NULL, cancelled_by = NULL, cancellation_reason = NULL, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: CancelReservation :one
UPDATE reservations SET
    status = 'CANCELLED', cancelled_at = @now::timestamptz, cancelled_by = sqlc.narg(actor_id), cancellation_reason = @reason,
    version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- A line change that leaves the header as it is still bumps the version (optimistic concurrency).
-- name: BumpReservation :one
UPDATE reservations SET version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: InsertLine :one
INSERT INTO reservation_rooms (
    tenant_id, property_id, reservation_id, guest_id, room_type_id, room_id, rate_plan_id, arrival_date, departure_date,
    adult_count, child_count, status, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @reservation_id, sqlc.narg(guest_id), @room_type_id, sqlc.narg(room_id), @rate_plan_id, @arrival_date,
    @departure_date, @adult_count, @child_count, @status, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- name: GetLine :one
SELECT * FROM reservation_rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListLines :many
SELECT * FROM reservation_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_id = @reservation_id
ORDER BY id;

-- name: ListLinesOfReservations :many
SELECT * FROM reservation_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_id = ANY(@reservation_ids::bigint[])
ORDER BY reservation_id, id;

-- One write for every mutable line field: the service computes the new state and passes all of it.
-- name: UpdateLine :one
UPDATE reservation_rooms SET
    guest_id = sqlc.narg(guest_id), room_type_id = @room_type_id, room_id = sqlc.narg(room_id), rate_plan_id = @rate_plan_id,
    arrival_date = @arrival_date, departure_date = @departure_date, adult_count = @adult_count, child_count = @child_count,
    status = @status, cancelled_at = sqlc.narg(cancelled_at), cancelled_by = sqlc.narg(cancelled_by),
    cancellation_reason = sqlc.narg(cancellation_reason), no_show_at = sqlc.narg(no_show_at), no_show_by = sqlc.narg(no_show_by),
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: InsertNightRate :exec
INSERT INTO reservation_room_rates (
    tenant_id, property_id, reservation_room_id, stay_date, rate_plan_id, charge_code_id, price_mode,
    base_rate, discount_amount, amount, is_override, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @line_id, @stay_date, @rate_plan_id, @charge_code_id, @price_mode,
    sqlc.narg(base_rate), @discount_amount, @amount, @is_override, sqlc.narg(actor_id), sqlc.narg(actor_id)
);

-- name: DeleteNightRates :exec
DELETE FROM reservation_room_rates WHERE property_id = @property_id AND reservation_room_id = @line_id;

-- name: DeleteNightRatesOutside :exec
DELETE FROM reservation_room_rates
WHERE property_id = @property_id AND reservation_room_id = @line_id AND (stay_date < @arrival OR stay_date >= @departure);

-- name: ListNightRates :many
SELECT * FROM reservation_room_rates
WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_room_id = ANY(@line_ids::bigint[])
ORDER BY reservation_room_id, stay_date;

-- Search: the header with dates and lines derived from the active (non-cancelled) lines, falling back to
-- all lines for a fully cancelled reservation.
-- name: SearchReservations :many
SELECT r.*,
       agg.arrival_date::date AS arrival_date, agg.departure_date::date AS departure_date, agg.room_count::int AS room_count,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM reservations r
CROSS JOIN LATERAL (
    SELECT COALESCE(min(l.arrival_date) FILTER (WHERE l.status <> 'CANCELLED'), min(l.arrival_date)) AS arrival_date,
           COALESCE(max(l.departure_date) FILTER (WHERE l.status <> 'CANCELLED'), max(l.departure_date)) AS departure_date,
           count(*) FILTER (WHERE l.status <> 'CANCELLED') AS room_count
    FROM reservation_rooms l WHERE l.reservation_id = r.id
) agg
LEFT JOIN guests g ON g.tenant_id = r.tenant_id AND g.id = r.guest_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
  AND (@before_id::bigint = 0 OR r.id < @before_id::bigint)
  AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
  AND (sqlc.narg(arrival_from)::date IS NULL OR agg.arrival_date >= sqlc.narg(arrival_from)::date)
  AND (sqlc.narg(arrival_to)::date IS NULL OR agg.arrival_date <= sqlc.narg(arrival_to)::date)
  AND (sqlc.narg(q)::text IS NULL OR r.confirmation_number ILIKE '%' || sqlc.narg(q)::text || '%'
       OR g.last_name ILIKE '%' || sqlc.narg(q)::text || '%' OR g.first_name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY r.id DESC
LIMIT @row_limit;

-- name: GuestBrief :one
SELECT id, code, first_name, last_name FROM guests WHERE tenant_id = @tenant_id AND id = @id;

-- name: ListGuestBriefs :many
SELECT id, code, first_name, last_name FROM guests WHERE tenant_id = @tenant_id AND id = ANY(@ids::bigint[]);

-- name: ListRoomNumbers :many
SELECT id, room_number FROM rooms WHERE property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: ListStaysOfLines :many
SELECT id, reservation_room_id FROM stays
WHERE property_id = @property_id AND reservation_room_id = ANY(@line_ids::bigint[]) AND status <> 'CANCELLED';

-- name: ListReservationFolios :many
SELECT f.id, f.folio_number, f.stay_id, f.status,
       COALESCE(sum(i.debit - i.credit), 0)::numeric AS balance
FROM folios f
LEFT JOIN folio_items i ON i.property_id = f.property_id AND i.folio_id = f.id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.reservation_id = @reservation_id
GROUP BY f.id
ORDER BY f.id;

-- Booking facts of a room type used to validate a line.
-- name: GetRoomTypeForBooking :one
SELECT id, code, is_active, max_adult, max_child, max_occupancy FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRoomTypesForBooking :many
SELECT id, code, name, is_active, max_adult, max_child, max_occupancy FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: ListRatePlanBriefs :many
SELECT id, code, name FROM rate_plans WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: DeleteNightRate :exec
DELETE FROM reservation_room_rates WHERE property_id = @property_id AND reservation_room_id = @line_id AND stay_date = @stay_date;

-- Type and state of rooms (to compute the effective room type of lines and validate an assignment).
-- name: ListRoomsForBooking :many
SELECT id, room_number, room_type_id, is_active FROM rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- Tape chart: active rooms with their type, the CONFIRMED / CHECKED_IN lines and the active blocks that touch [from, to).
-- name: ListTapeRooms :many
SELECT r.id, r.room_number, r.room_type_id, t.code AS room_type_code, t.sort_order
FROM rooms r JOIN room_types t ON t.property_id = r.property_id AND t.id = r.room_type_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.is_active
ORDER BY t.sort_order, t.code, r.room_number;

-- name: ListTapeLines :many
SELECT l.id, l.reservation_id, res.confirmation_number, l.status, l.room_id, l.room_type_id, l.arrival_date, l.departure_date,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
LEFT JOIN guests g ON g.tenant_id = res.tenant_id AND g.id = COALESCE(l.guest_id, res.guest_id)
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status IN ('CONFIRMED', 'CHECKED_IN')
  AND l.arrival_date < @window_end::date AND l.departure_date > @window_start::date
ORDER BY l.arrival_date, l.id;

-- name: ListTapeBlocks :many
SELECT id, room_id, block_type, start_date, end_date FROM room_blocks
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ACTIVE'
  AND start_date < @window_end::date AND end_date > @window_start::date
ORDER BY start_date, id;
