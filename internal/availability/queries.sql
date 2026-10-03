-- Availability queries (sqlc): inventory per room type and night, specific-room checks, free rooms.
-- All read-only; callers that write hold the locks of docs/architecture/05-transactions-locking.md.

-- sellable(T, n) = active rooms of T without an active OOO/OOS block on n.
-- demand(T, n)   = CONFIRMED lines whose effective type is T (the assigned room's type, else the booked
--                  type) plus open stays in a room of T, holding [business date, max(departure, next date)).
-- Drafts, cancelled, no-show, checked-in and completed lines hold nothing: a checked-in line is counted through
-- its stay. @exclude_line_id removes one line's own demand (amending a line re-checks without itself).
-- name: NightInventory :many
SELECT t.room_type_id::bigint AS room_type_id, d.night::date AS night,
    (SELECT count(*) FROM rooms r
      WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.room_type_id = t.room_type_id AND r.is_active
        AND NOT EXISTS (SELECT 1 FROM room_blocks b
                         WHERE b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
                           AND b.start_date <= d.night::date AND d.night::date < b.end_date))::int AS sellable,
    ((SELECT count(*) FROM reservation_rooms l
        LEFT JOIN rooms lr ON lr.property_id = l.property_id AND lr.id = l.room_id
       WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED'
         AND COALESCE(lr.room_type_id, l.room_type_id) = t.room_type_id
         AND l.arrival_date <= d.night::date AND d.night::date < l.departure_date
         AND (sqlc.narg(exclude_line_id)::bigint IS NULL OR l.id <> sqlc.narg(exclude_line_id)::bigint))
     + (SELECT count(*) FROM stay_rooms sr
         JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
         JOIN rooms sroom ON sroom.property_id = sr.property_id AND sroom.id = sr.room_id
        WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
          AND sroom.room_type_id = t.room_type_id
          AND @business_date::date <= d.night::date AND d.night::date < GREATEST(s.departure_date, @next_date::date)))::int AS demand
FROM unnest(@room_type_ids::bigint[]) AS t (room_type_id)
CROSS JOIN unnest(@dates::text[]) AS d (night)
ORDER BY t.room_type_id, d.night;

-- The same per (room type, bed type) pair of the active rooms with a bed, per night, counting only the rooms with that bed: sellable = active rooms
-- with the bed and without an active block, held = those held by a CONFIRMED line assigned to them or by an open
-- stay. Bookings without a room are not counted (they may end up in any bed).
-- name: BedNightInventory :many
SELECT t.room_type_id::bigint AS room_type_id, t.bed_type_id::bigint AS bed_type_id, d.night::date AS night,
    (SELECT count(*) FROM rooms r
      WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.room_type_id = t.room_type_id
        AND r.is_active AND r.bed_type_id = t.bed_type_id
        AND NOT EXISTS (SELECT 1 FROM room_blocks b
                         WHERE b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
                           AND b.start_date <= d.night::date AND d.night::date < b.end_date))::int AS sellable,
    (SELECT count(*) FROM rooms r
      WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.room_type_id = t.room_type_id
        AND r.is_active AND r.bed_type_id = t.bed_type_id
        AND (EXISTS (SELECT 1 FROM reservation_rooms l
                      WHERE l.property_id = r.property_id AND l.room_id = r.id AND l.status = 'CONFIRMED'
                        AND l.arrival_date <= d.night::date AND d.night::date < l.departure_date)
          OR EXISTS (SELECT 1 FROM stay_rooms sr
                      JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
                      WHERE sr.property_id = r.property_id AND sr.room_id = r.id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
                        AND @business_date::date <= d.night::date AND d.night::date < GREATEST(s.departure_date, @next_date::date))))::int AS held
FROM (SELECT DISTINCT room_type_id, bed_type_id FROM rooms
       WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_active AND bed_type_id IS NOT NULL) AS t
CROSS JOIN unnest(@dates::text[]) AS d (night)
ORDER BY t.room_type_id, t.bed_type_id, d.night;

-- The (room type, bed type) pairs of the active rooms with a bed, and how many rooms each has.
-- name: ListRoomBeds :many
SELECT r.room_type_id, r.bed_type_id::bigint AS bed_type_id, bt.code, bt.name, count(*)::int AS rooms
FROM rooms r
JOIN bed_types bt ON bt.property_id = r.property_id AND bt.id = r.bed_type_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.is_active AND r.bed_type_id IS NOT NULL
GROUP BY r.room_type_id, r.bed_type_id, bt.code, bt.name, bt.sort_order
ORDER BY r.room_type_id, bt.sort_order, bt.code;

-- The last date any CONFIRMED line or open stay holds a room of the type (demand ends there).
-- name: MaxLineDeparture :one
SELECT coalesce(max(l.departure_date), '1900-01-01'::date)::date AS last_departure
FROM reservation_rooms l
LEFT JOIN rooms lr ON lr.property_id = l.property_id AND lr.id = l.room_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED'
  AND COALESCE(lr.room_type_id, l.room_type_id) = @room_type_id;

-- name: MaxStayDeparture :one
SELECT coalesce(max(s.departure_date), '1900-01-01'::date)::date AS last_departure
FROM stay_rooms sr
JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
  AND r.room_type_id = @room_type_id;

-- The active rooms of each room type, for the availability calendar (blocked = this minus sellable).
-- name: CountActiveRoomsByType :many
SELECT room_type_id, count(*)::int AS rooms FROM rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_active
GROUP BY room_type_id;

-- name: GetRoomForCheck :one
SELECT id, room_type_id, room_number, is_active FROM rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Active blocks of a room (they take it out of the sellable count).
-- name: ListActiveRoomBlocks :many
SELECT block_type, start_date, end_date FROM room_blocks
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id AND status = 'ACTIVE'
ORDER BY start_date;

-- name: ListRoomBlockOverlaps :many
SELECT id, block_type, start_date, end_date FROM room_blocks
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id AND status = 'ACTIVE'
  AND start_date < @end_date::date AND @start_date::date < end_date
ORDER BY start_date;

-- CONFIRMED lines assigned to the room that overlap [start, end), optionally ignoring one line.
-- name: ListRoomLineOverlaps :many
SELECT id, reservation_id, arrival_date, departure_date FROM reservation_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @room_id AND status = 'CONFIRMED'
  AND arrival_date < @end_date::date AND @start_date::date < departure_date
  AND (sqlc.narg(exclude_line_id)::bigint IS NULL OR id <> sqlc.narg(exclude_line_id)::bigint)
ORDER BY arrival_date, id;

-- Open stay segments on the room: it is held [business date, max(departure, next date)).
-- name: ListRoomStayOverlaps :many
SELECT s.id, s.stay_number, s.departure_date FROM stay_rooms sr
JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.room_id = @room_id
  AND sr.check_out_at IS NULL AND s.status = 'OPEN'
  AND (sqlc.narg(exclude_stay_id)::bigint IS NULL OR s.id <> sqlc.narg(exclude_stay_id)::bigint)
  AND @business_date::date < @end_date::date
  AND @start_date::date < GREATEST(s.departure_date, @next_date::date)
ORDER BY s.id;

-- Free specific rooms of a type for [arrival, departure), with their housekeeping status.
-- name: ListFreeRooms :many
SELECT r.id AS room_id, r.room_number, r.floor, r.building, coalesce(h.status, 'DIRTY')::text AS housekeeping_status,
       r.bed_type_id, bt.code AS bed_type_code, bt.name AS bed_type_name
FROM rooms r
LEFT JOIN room_housekeeping h ON h.property_id = r.property_id AND h.room_id = r.id
LEFT JOIN bed_types bt ON bt.property_id = r.property_id AND bt.id = r.bed_type_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.room_type_id = @room_type_id AND r.is_active
  AND NOT EXISTS (SELECT 1 FROM room_blocks b
                   WHERE b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
                     AND b.start_date < @departure::date AND @arrival::date < b.end_date)
  AND NOT EXISTS (SELECT 1 FROM reservation_rooms l
                   WHERE l.property_id = r.property_id AND l.room_id = r.id AND l.status = 'CONFIRMED'
                     AND l.arrival_date < @departure::date AND @arrival::date < l.departure_date)
  AND NOT EXISTS (SELECT 1 FROM stay_rooms sr
                   JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
                   WHERE sr.property_id = r.property_id AND sr.room_id = r.id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
                     AND @business_date::date < @departure::date
                     AND @arrival::date < GREATEST(s.departure_date, @next_date::date))
ORDER BY r.floor NULLS LAST, r.room_number;

-- Active room types with their capacity, for the availability search.
-- name: ListSellableRoomTypes :many
SELECT id, code, name, max_adult, max_child, max_occupancy, sort_order FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_active
ORDER BY sort_order, code;

-- Active rate plans with the room charge code they sell through.
-- name: ListSellableRatePlans :many
SELECT p.id, p.code, p.name, p.meal_plan, p.is_refundable, p.room_charge_code_id, c.price_mode
FROM rate_plans p JOIN charge_codes c ON c.property_id = p.property_id AND c.id = p.room_charge_code_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.is_active
ORDER BY p.code;

-- How full the whole property is on each night: rooms held (confirmed lines and open stays, whatever their type)
-- against the rooms that can be sold (active, not blocked). The same demand as the inventory above, summed over types.
-- name: PropertyOccupancy :many
SELECT d.night::date AS night,
    (SELECT count(*) FROM rooms r
      WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.is_active
        AND NOT EXISTS (SELECT 1 FROM room_blocks b
                         WHERE b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
                           AND b.start_date <= d.night::date AND d.night::date < b.end_date))::int AS sellable,
    ((SELECT count(*) FROM reservation_rooms l
       WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED'
         AND l.arrival_date <= d.night::date AND d.night::date < l.departure_date)
     + (SELECT count(*) FROM stay_rooms sr
         JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
        WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
          AND @business_date::date <= d.night::date AND d.night::date < GREATEST(s.departure_date, @next_date::date)))::int AS booked
FROM unnest(@dates::text[]) AS d (night)
ORDER BY d.night;
