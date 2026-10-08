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
SELECT l.id, l.reservation_id, res.confirmation_number, l.room_type_id, t.code AS room_type_code, l.rate_plan_id, l.status, l.room_id, l.arrival_date, l.departure_date, l.bed_locked, l.requested_bed_type_id
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.id = @id;

-- Nightly snapshot of the stay's line, with the business date it was charged on (0001-01-01: not charged).
-- name: ListStayNights :many
SELECT r.stay_date, r.amount, r.price_mode, r.is_override,
       COALESCE((SELECT min(p.business_date) FROM stay_charge_postings p
         WHERE p.property_id = r.property_id AND p.stay_id = @stay_id AND p.service_date = r.stay_date AND p.status = 'POSTED'), '0001-01-01'::date)::date AS posted_on
FROM reservation_room_rates r
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.reservation_room_id = @line_id
ORDER BY r.stay_date;

-- The charged nights of a stay with the ledger item each one posted (the room charge), for a rate correction.
-- name: ListStayNightPostings :many
SELECT p.service_date, p.business_date, p.folio_item_id, p.charge_code_id
FROM stay_charge_postings p
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.stay_id = @stay_id AND p.status = 'POSTED'
ORDER BY p.service_date;

-- name: GetRatePlanCode :one
SELECT code, name FROM rate_plans WHERE property_id = @property_id AND id = @id;

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

-- The in-house list: the OPEN stays with the guest, the room of the open segment and its type, the rate plan of the booking and the reservation number.
-- name: ListInHouse :many
SELECT s.id, s.stay_number, s.guest_id, res.id AS reservation_id, s.arrival_date, s.departure_date, s.adult_count, s.child_count, s.version,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name, res.confirmation_number,
       rp.code AS rate_plan_code, rp.name AS rate_plan_name,
       cur.room_id, cur.room_number, cur.room_type_code, cur.room_type_name
FROM stays s
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
JOIN LATERAL (
    SELECT sr.room_id, r.room_number, t.id AS room_type_id, t.code AS room_type_code, t.name AS room_type_name
    FROM stay_rooms sr
    JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
    JOIN room_types t ON t.property_id = r.property_id AND t.id = r.room_type_id
    WHERE sr.stay_id = s.id
    ORDER BY sr.check_out_at IS NULL DESC, sr.id DESC LIMIT 1
) cur ON true
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN' AND (@before_id::bigint = 0 OR s.id < @before_id::bigint)
  AND (sqlc.narg(departure_date)::date IS NULL OR s.departure_date = sqlc.narg(departure_date)::date)
  AND (sqlc.narg(departure_until)::date IS NULL OR s.departure_date <= sqlc.narg(departure_until)::date)
  AND (sqlc.narg(room_type_id)::bigint IS NULL OR cur.room_type_id = sqlc.narg(room_type_id)::bigint)
  AND (sqlc.narg(q)::text IS NULL OR s.stay_number ILIKE '%' || sqlc.narg(q)::text || '%' OR res.confirmation_number ILIKE '%' || sqlc.narg(q)::text || '%'
       OR cur.room_number ILIKE '%' || sqlc.narg(q)::text || '%' OR concat_ws(' ', g.first_name, g.last_name) ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY s.id DESC
LIMIT @row_limit;

-- Room nights up to a date that are not charged yet, for many stays: what a check-out would still post.
-- name: ListUnchargedNights :many
SELECT s.id AS stay_id, count(*)::int AS nights
FROM stays s
JOIN reservation_room_rates x ON x.property_id = s.property_id AND x.reservation_room_id = s.reservation_room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = ANY(@stay_ids::bigint[])
  AND x.stay_date < s.departure_date AND x.stay_date <= @through::date
  AND NOT EXISTS (SELECT 1 FROM stay_charge_postings p WHERE p.property_id = s.property_id AND p.stay_id = s.id AND p.service_date = x.stay_date AND p.status = 'POSTED')
GROUP BY s.id;

-- The price of the night the stay is in (the last night of its snapshot that is not after the date; the arrival date when no date is given): the snapshot of the booking, never the rate master.
-- name: ListInHouseRates :many
SELECT DISTINCT ON (s.id) s.id AS stay_id, x.amount, x.price_mode, x.is_override
FROM stays s
JOIN reservation_room_rates x ON x.property_id = s.property_id AND x.reservation_room_id = s.reservation_room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = ANY(@stay_ids::bigint[])
  AND x.stay_date <= COALESCE(sqlc.narg(on_date)::date, s.arrival_date)
ORDER BY s.id, x.stay_date DESC;

-- The billing instructions of the lines of many stays, with the company and the charge code they name.
-- name: ListInHouseInstructions :many
SELECT s.id AS stay_id, i.scope, i.company_id, c.name AS company_name, cc.code AS charge_code
FROM stays s
JOIN folio_billing_instructions i ON i.property_id = s.property_id AND i.reservation_room_id = s.reservation_room_id
JOIN companies c ON c.property_id = i.property_id AND c.id = i.company_id
LEFT JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = ANY(@stay_ids::bigint[])
ORDER BY s.id, (i.scope = 'ALL') DESC, (i.scope = 'ROOM') DESC, i.id;

-- name: ListCompanyNames :many
SELECT id, name FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- The rooms arriving on a date (the arrivals list): CONFIRMED by default, or CHECKED_IN, CANCELLED or NO_SHOW. The search reads the guest, the confirmation number and the room; the type narrows
-- the list. The company is the one of the first billing instruction of the line; the rate is the booked night of the arrival date (the snapshot, never the rate master).
-- name: ListArrivals :many
SELECT l.id AS line_id, l.reservation_id, res.confirmation_number, res.version AS reservation_version, l.room_type_id, t.code AS room_type_code, t.name AS room_type_name,
       l.room_id, r.room_number, hk.status AS housekeeping_status, l.arrival_date, l.departure_date, l.adult_count, l.child_count, l.guest_id AS line_guest_id, res.guest_id AS booker_id,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name,
       l.requested_bed_type_id, l.bed_locked, rbt.code AS requested_bed_type_code, abt.code AS room_bed_type_code,
       l.status AS line_status, res.status AS reservation_status,
       rp.code AS rate_plan_code, rp.name AS rate_plan_name,
       night.amount AS night_amount, night.price_mode AS night_price_mode,
       COALESCE(co.id, 0)::bigint AS company_id, COALESCE(co.name, '')::text AS company_name
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
LEFT JOIN rooms r ON r.property_id = l.property_id AND r.id = l.room_id
LEFT JOIN bed_types rbt ON rbt.property_id = l.property_id AND rbt.id = l.requested_bed_type_id
LEFT JOIN bed_types abt ON abt.property_id = r.property_id AND abt.id = r.bed_type_id
LEFT JOIN room_housekeeping hk ON hk.property_id = l.property_id AND hk.room_id = l.room_id
LEFT JOIN guests g ON g.tenant_id = res.tenant_id AND g.id = COALESCE(l.guest_id, res.guest_id)
LEFT JOIN reservation_room_rates night ON night.property_id = l.property_id AND night.reservation_room_id = l.id AND night.stay_date = l.arrival_date
LEFT JOIN LATERAL (
    SELECT c.id, c.name FROM folio_billing_instructions i JOIN companies c ON c.property_id = i.property_id AND c.id = i.company_id
    WHERE i.property_id = l.property_id AND i.reservation_room_id = l.id
    ORDER BY (i.scope = 'ALL') DESC, (i.scope = 'ROOM') DESC, i.id LIMIT 1
) co ON true
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.arrival_date = @arrival
  AND l.status = @line_status::text AND (@line_status::text <> 'CONFIRMED' OR res.status = 'CONFIRMED')
  AND (sqlc.narg(room_type_id)::bigint IS NULL OR l.room_type_id = sqlc.narg(room_type_id)::bigint)
  AND (sqlc.narg(q)::text IS NULL OR res.confirmation_number ILIKE '%' || sqlc.narg(q)::text || '%' OR r.room_number ILIKE '%' || sqlc.narg(q)::text || '%'
       OR concat_ws(' ', g.first_name, g.last_name) ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY t.sort_order, l.id;

-- name: GetRoomForCheckIn :one
SELECT r.id, r.room_number, r.room_type_id, r.bed_type_id, r.is_active, h.status AS housekeeping_status
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
