-- Reservations module queries (sqlc). Scoped by tenant_id and property_id.

-- name: InsertReservation :one
INSERT INTO reservations (
    tenant_id, property_id, confirmation_number, guest_id, reservation_date, source, market, special_request, remarks,
    idempotency_key, idempotency_hash, created_by, updated_by, company_id, booking_group_id
) VALUES (
    @tenant_id, @property_id, @confirmation_number, sqlc.narg(guest_id), @reservation_date, @source, sqlc.narg(market),
    sqlc.narg(special_request), sqlc.narg(remarks), sqlc.narg(idempotency_key), sqlc.narg(idempotency_hash),
    sqlc.narg(actor_id), sqlc.narg(actor_id), sqlc.narg(company_id), sqlc.narg(booking_group_id)
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
    remarks = sqlc.narg(remarks), company_id = sqlc.narg(company_id), booking_group_id = sqlc.narg(booking_group_id),
    version = version + 1, updated_by = sqlc.narg(actor_id)
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
    adult_count, child_count, requested_bed_type_id, bed_locked, occupancy_reason, status, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @reservation_id, sqlc.narg(guest_id), @room_type_id, sqlc.narg(room_id), @rate_plan_id, @arrival_date,
    @departure_date, @adult_count, @child_count, sqlc.narg(requested_bed_type_id), @bed_locked, sqlc.narg(occupancy_reason), @status, sqlc.narg(actor_id), sqlc.narg(actor_id)
)
RETURNING *;

-- The bed types a request can name: id, code, name and whether it can still be chosen.
-- name: ListBedTypeBriefs :many
SELECT id, code, name, is_active FROM bed_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

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
    requested_bed_type_id = sqlc.narg(requested_bed_type_id), bed_locked = @bed_locked, occupancy_reason = sqlc.narg(occupancy_reason), status = @status, cancelled_at = sqlc.narg(cancelled_at), cancelled_by = sqlc.narg(cancelled_by),
    cancellation_reason = sqlc.narg(cancellation_reason), no_show_at = sqlc.narg(no_show_at), no_show_by = sqlc.narg(no_show_by),
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: InsertNightRate :exec
INSERT INTO reservation_room_rates (
    tenant_id, property_id, reservation_room_id, stay_date, rate_plan_id, charge_code_id, price_mode,
    base_rate, discount_amount, amount, is_override, grid_rate, yield_rules, bed_adjustment, created_by, updated_by
) VALUES (
    @tenant_id, @property_id, @line_id, @stay_date, @rate_plan_id, @charge_code_id, @price_mode,
    sqlc.narg(base_rate), @discount_amount, @amount, @is_override, sqlc.narg(grid_rate), sqlc.narg(yield_rules), @bed_adjustment, sqlc.narg(actor_id), sqlc.narg(actor_id)
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
-- all lines for a fully cancelled reservation. display_status is DisplayStatus (model.go) in SQL: a test holds the two together.
--
-- The order is asked of the server: by arrival (the date, plain), confirmation number, guest (the name that is shown, in the ICU collation), status (the one that staff see) or rate (the lowest
-- price of the arrival night of the rooms), either way, NULL last. The default (the empty key) is "nearest first": the arrivals of the reference date (the business date) and after it, soonest
-- first, then the arrivals before it, the most recent first; the confirmation number breaks a tie. That is one text key: 0 or 1 (on or after the date, or before it), the distance in days to
-- seven digits, a bar and the number. One query for every order, the key and the direction being parameters (the arrival, the rate and the status are derived from the rooms, so no index could
-- serve them anyway). The page goes on after (the sort value, id): after_null says that the value of the last row was NULL; the id breaks a tie in the direction of the order.
--
-- Two steps, for the cost: the inner query finds the page on narrow rows (the id and what the order is made of), the outer one reads the wide rows of those ids only (and the guest, the company,
-- the group). Measured on 75 000 reservations: twice as fast as one query that carries the wide rows through every join.
-- name: SearchReservations :many
SELECT r.*,
       p.arrival_date::date AS arrival_date, p.departure_date::date AS departure_date, p.room_count::int AS room_count, p.display_status::text AS display_status,
       gu.first_name AS guest_first_name, gu.last_name AS guest_last_name, co.name AS company_name, bg.code AS group_code,
       p.sort_null::boolean AS sort_null, p.sort_text::text AS sort_text, p.sort_num::numeric AS sort_num, p.sort_date::date AS sort_date
FROM (
    SELECT r.id, agg.arrival_date, agg.departure_date, coalesce(agg.room_count, 0) AS room_count, d.display_status,
           (k.sk_text IS NULL AND k.sk_num IS NULL AND k.sk_date IS NULL) AS sort_null, coalesce(k.sk_text, '') AS sort_text, coalesce(k.sk_num, 0) AS sort_num,
           coalesce(k.sk_date, '1970-01-01'::date) AS sort_date, k.sk_text, k.sk_num, k.sk_date
    FROM reservations r
    LEFT JOIN (
        -- the rooms of every reservation of the property in one pass (a hash join with the reservations), not a probe of the rooms for each reservation: the list is sorted by what is derived from them
        SELECT l.reservation_id,
               COALESCE(min(l.arrival_date) FILTER (WHERE l.status <> 'CANCELLED'), min(l.arrival_date)) AS arrival_date,
               COALESCE(max(l.departure_date) FILTER (WHERE l.status <> 'CANCELLED'), max(l.departure_date)) AS departure_date,
               count(*) FILTER (WHERE l.status <> 'CANCELLED') AS room_count,
               COALESCE(min(n.amount) FILTER (WHERE l.status <> 'CANCELLED'), min(n.amount)) AS rate,
               count(*) FILTER (WHERE l.status = 'CHECKED_IN') AS n_checked_in,
               count(*) FILTER (WHERE l.status IN ('CONFIRMED', 'DRAFT')) AS n_waiting,
               count(*) FILTER (WHERE l.status = 'COMPLETED') AS n_completed,
               count(*) FILTER (WHERE l.status = 'NO_SHOW') AS n_no_show
        FROM reservation_rooms l
        LEFT JOIN reservation_room_rates n ON n.property_id = l.property_id AND n.reservation_room_id = l.id AND n.stay_date = l.arrival_date
        WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id
        GROUP BY l.reservation_id
    ) agg ON agg.reservation_id = r.id
    CROSS JOIN LATERAL (
        SELECT CASE WHEN r.status = 'DRAFT' THEN 'DRAFT'
                    WHEN r.status = 'CANCELLED' THEN 'CANCELLED'
                    WHEN coalesce(agg.n_checked_in, 0) > 0 THEN 'IN_HOUSE'
                    WHEN coalesce(agg.n_waiting, 0) > 0 THEN 'CONFIRMED'
                    WHEN coalesce(agg.n_completed, 0) > 0 THEN 'CHECKED_OUT'
                    WHEN coalesce(agg.n_no_show, 0) > 0 THEN 'NO_SHOW'
                    ELSE 'CANCELLED' END AS display_status
    ) d
    LEFT JOIN guests g ON g.tenant_id = r.tenant_id AND g.id = r.guest_id
    CROSS JOIN LATERAL (
        SELECT (CASE @sort_key::text
                    WHEN '' THEN (CASE WHEN agg.arrival_date >= sqlc.arg(ref_date)::date THEN '0' || lpad((agg.arrival_date - sqlc.arg(ref_date)::date)::text, 7, '0')
                                       ELSE '1' || lpad((sqlc.arg(ref_date)::date - agg.arrival_date)::text, 7, '0') END || '|' || r.confirmation_number) COLLATE "und-x-icu"
                    WHEN 'confirmation_number' THEN r.confirmation_number::text COLLATE "und-x-icu"
                    WHEN 'guest_name' THEN (lower(coalesce(nullif(g.first_name, '') || ' ', '') || g.last_name) COLLATE "und-x-icu")
                    WHEN 'status' THEN d.display_status::text COLLATE "und-x-icu"
                END) AS sk_text,
               (CASE @sort_key::text WHEN 'rate' THEN agg.rate END) AS sk_num,
               (CASE @sort_key::text WHEN 'arrival_date' THEN agg.arrival_date END) AS sk_date
    ) k
    WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
      AND (sqlc.narg(company_id)::bigint IS NULL OR r.company_id = sqlc.narg(company_id)::bigint)
      AND (sqlc.narg(booking_group_id)::bigint IS NULL OR r.booking_group_id = sqlc.narg(booking_group_id)::bigint)
      AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
      AND (sqlc.narg(display_status)::text IS NULL OR d.display_status = sqlc.narg(display_status)::text)
      AND (sqlc.narg(arrival_from)::date IS NULL OR agg.arrival_date >= sqlc.narg(arrival_from)::date)
      AND (sqlc.narg(arrival_to)::date IS NULL OR agg.arrival_date <= sqlc.narg(arrival_to)::date)
      AND (sqlc.narg(departure_from)::date IS NULL OR agg.departure_date >= sqlc.narg(departure_from)::date)
      AND (sqlc.narg(departure_to)::date IS NULL OR agg.departure_date <= sqlc.narg(departure_to)::date)
      AND (sqlc.narg(room_type_id)::bigint IS NULL OR EXISTS (SELECT 1 FROM reservation_rooms x WHERE x.reservation_id = r.id AND x.room_type_id = sqlc.narg(room_type_id)::bigint))
      AND (sqlc.narg(rate_plan_id)::bigint IS NULL OR EXISTS (SELECT 1 FROM reservation_rooms x WHERE x.reservation_id = r.id AND x.rate_plan_id = sqlc.narg(rate_plan_id)::bigint))
      AND (sqlc.narg(q)::text IS NULL OR r.confirmation_number ILIKE '%' || sqlc.narg(q)::text || '%'
           OR g.last_name ILIKE '%' || sqlc.narg(q)::text || '%' OR g.first_name ILIKE '%' || sqlc.narg(q)::text || '%'
           OR concat_ws(' ', g.first_name, g.last_name) ILIKE '%' || sqlc.narg(q)::text || '%'
           OR EXISTS (SELECT 1 FROM reservation_rooms x JOIN rooms rm ON rm.property_id = x.property_id AND rm.id = x.room_id
                      WHERE x.reservation_id = r.id AND rm.room_number ILIKE '%' || sqlc.narg(q)::text || '%'))
      AND (sqlc.narg(after_id)::bigint IS NULL
           OR CASE WHEN NOT @sort_desc::boolean THEN
                   CASE WHEN @after_null::boolean THEN (k.sk_text IS NULL AND k.sk_num IS NULL AND k.sk_date IS NULL AND r.id > sqlc.narg(after_id)::bigint)
                        ELSE ((k.sk_text IS NULL AND k.sk_num IS NULL AND k.sk_date IS NULL)
                              OR k.sk_text > sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num > sqlc.narg(after_num)::numeric OR k.sk_date > sqlc.narg(after_date)::date
                              OR ((k.sk_text = sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num = sqlc.narg(after_num)::numeric OR k.sk_date = sqlc.narg(after_date)::date)
                                  AND r.id > sqlc.narg(after_id)::bigint)) END
               ELSE
                   CASE WHEN @after_null::boolean THEN (k.sk_text IS NULL AND k.sk_num IS NULL AND k.sk_date IS NULL AND r.id < sqlc.narg(after_id)::bigint)
                        ELSE ((k.sk_text IS NULL AND k.sk_num IS NULL AND k.sk_date IS NULL)
                              OR k.sk_text < sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num < sqlc.narg(after_num)::numeric OR k.sk_date < sqlc.narg(after_date)::date
                              OR ((k.sk_text = sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num = sqlc.narg(after_num)::numeric OR k.sk_date = sqlc.narg(after_date)::date)
                                  AND r.id < sqlc.narg(after_id)::bigint)) END
               END)
    ORDER BY (CASE WHEN NOT @sort_desc::boolean THEN k.sk_text END) ASC NULLS LAST, (CASE WHEN NOT @sort_desc::boolean THEN k.sk_num END) ASC NULLS LAST,
             (CASE WHEN NOT @sort_desc::boolean THEN k.sk_date END) ASC NULLS LAST,
             (CASE WHEN @sort_desc::boolean THEN k.sk_text END) DESC NULLS LAST, (CASE WHEN @sort_desc::boolean THEN k.sk_num END) DESC NULLS LAST,
             (CASE WHEN @sort_desc::boolean THEN k.sk_date END) DESC NULLS LAST,
             (CASE WHEN NOT @sort_desc::boolean THEN r.id END) ASC, (CASE WHEN @sort_desc::boolean THEN r.id END) DESC
    LIMIT @row_limit
) p
JOIN reservations r ON r.id = p.id
LEFT JOIN guests gu ON gu.tenant_id = r.tenant_id AND gu.id = r.guest_id
LEFT JOIN companies co ON co.property_id = r.property_id AND co.id = r.company_id
LEFT JOIN booking_groups bg ON bg.property_id = r.property_id AND bg.id = r.booking_group_id
ORDER BY (CASE WHEN NOT @sort_desc::boolean THEN p.sk_text END) ASC NULLS LAST, (CASE WHEN NOT @sort_desc::boolean THEN p.sk_num END) ASC NULLS LAST,
         (CASE WHEN NOT @sort_desc::boolean THEN p.sk_date END) ASC NULLS LAST,
         (CASE WHEN @sort_desc::boolean THEN p.sk_text END) DESC NULLS LAST, (CASE WHEN @sort_desc::boolean THEN p.sk_num END) DESC NULLS LAST,
         (CASE WHEN @sort_desc::boolean THEN p.sk_date END) DESC NULLS LAST,
         (CASE WHEN NOT @sort_desc::boolean THEN p.id END) ASC, (CASE WHEN @sort_desc::boolean THEN p.id END) DESC;

-- The rooms of many reservations for the list: type, assigned room, plan, the booked price of the arrival night (the snapshot) and the company of the first billing instruction.
-- name: ListSummaryLines :many
SELECT l.id, l.reservation_id, l.status, l.room_type_id, l.room_id, l.requested_bed_type_id, l.bed_locked, t.code AS room_type_code, r.room_number, rp.code AS rate_plan_code, l.arrival_date, l.departure_date, l.adult_count, l.child_count,
       night.amount AS night_amount, night.price_mode AS night_price_mode, COALESCE(co.name, '')::text AS billing_company, st.id AS stay_id
FROM reservation_rooms l
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
LEFT JOIN rooms r ON r.property_id = l.property_id AND r.id = l.room_id
LEFT JOIN stays st ON st.property_id = l.property_id AND st.reservation_room_id = l.id
LEFT JOIN reservation_room_rates night ON night.property_id = l.property_id AND night.reservation_room_id = l.id AND night.stay_date = l.arrival_date
LEFT JOIN LATERAL (
    SELECT c.name FROM folio_billing_instructions i JOIN companies c ON c.property_id = i.property_id AND c.id = i.company_id
    WHERE i.property_id = l.property_id AND i.reservation_room_id = l.id
    ORDER BY (i.scope = 'ALL') DESC, (i.scope = 'ROOM') DESC, i.id LIMIT 1
) co ON true
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.reservation_id = ANY(@reservation_ids::bigint[])
ORDER BY l.reservation_id, l.id;

-- What the reservations hold before check-in: the open folio that has no stay yet (the deposit folio) and the credit on it, from the ledger like every balance.
-- name: ListDepositsPaid :many
SELECT f.reservation_id, f.id AS folio_id, COALESCE(sum(i.credit - i.debit), 0)::numeric AS paid
FROM folios f
LEFT JOIN folio_items i ON i.property_id = f.property_id AND i.folio_id = f.id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.reservation_id = ANY(@reservation_ids::bigint[]) AND f.stay_id IS NULL AND f.status = 'OPEN'
GROUP BY f.reservation_id, f.id;

-- name: CompanyRef :one
SELECT id, code, name, is_active FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GroupRef :one
SELECT id, code, name, is_active, company_id, arrival_date, departure_date FROM booking_groups
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

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
SELECT f.id, f.folio_number, f.stay_id, f.status, f.folio_type, f.bill_to_company_id, c.name AS bill_to_company_name,
       COALESCE(sum(i.debit - i.credit), 0)::numeric AS balance
FROM folios f
LEFT JOIN folio_items i ON i.property_id = f.property_id AND i.folio_id = f.id
LEFT JOIN companies c ON c.property_id = f.property_id AND c.id = f.bill_to_company_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.reservation_id = @reservation_id
GROUP BY f.id, c.name
ORDER BY f.id;

-- Booking facts of a room type used to validate a line.
-- name: GetRoomTypeForBooking :one
SELECT id, code, is_active, max_adult, max_child, max_occupancy FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRoomTypesForBooking :many
SELECT id, code, name, is_active, max_adult, max_child, max_occupancy FROM room_types
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: ListRatePlanBriefs :many
SELECT id, code, name, occupancy_kind FROM rate_plans WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

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
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED'
  AND l.arrival_date < @window_end::date AND l.departure_date > @window_start::date
ORDER BY l.arrival_date, l.id;

-- Checked-in lines are drawn from their open stay: one bar per room segment, the open one up to the stay's
-- departure (at least one night), so room moves, extensions and shortened stays show as they are.
-- name: ListTapeSegments :many
SELECT l.id, l.reservation_id, res.confirmation_number, sr.room_id, sr.start_business_date,
       COALESCE(sr.end_business_date, GREATEST(st.departure_date, sr.start_business_date + 1))::date AS end_date,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM stays st
JOIN stay_rooms sr ON sr.property_id = st.property_id AND sr.stay_id = st.id
JOIN reservation_rooms l ON l.property_id = st.property_id AND l.id = st.reservation_room_id
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
LEFT JOIN guests g ON g.tenant_id = res.tenant_id AND g.id = st.guest_id
WHERE st.tenant_id = @tenant_id AND st.property_id = @property_id AND st.status = 'OPEN'
  AND sr.start_business_date < @window_end::date
  AND COALESCE(sr.end_business_date, GREATEST(st.departure_date, sr.start_business_date + 1)) > @window_start::date
  AND COALESCE(sr.end_business_date, GREATEST(st.departure_date, sr.start_business_date + 1)) > sr.start_business_date
ORDER BY sr.start_business_date, sr.id;

-- name: ListTapeBlocks :many
SELECT id, room_id, block_type, start_date, end_date FROM room_blocks
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ACTIVE'
  AND start_date < @window_end::date AND end_date > @window_start::date
ORDER BY start_date, id;

-- Room lines by id (the bulk no-show reads them before and after locking).
-- name: ListLinesByIDs :many
SELECT * FROM reservation_rooms
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[])
ORDER BY id;

-- name: ListFreeNightQuotas :many
SELECT occupancy_kind, monthly_nights FROM free_night_quotas
WHERE tenant_id = @tenant_id AND property_id = @property_id ORDER BY occupancy_kind;

-- name: GetFreeNightQuota :one
SELECT monthly_nights FROM free_night_quotas
WHERE tenant_id = @tenant_id AND property_id = @property_id AND occupancy_kind = @occupancy_kind;

-- name: UpsertFreeNightQuota :exec
INSERT INTO free_night_quotas (tenant_id, property_id, occupancy_kind, monthly_nights, updated_by)
VALUES (@tenant_id, @property_id, @occupancy_kind, @monthly_nights, sqlc.narg(actor_id))
ON CONFLICT (property_id, occupancy_kind) DO UPDATE SET monthly_nights = EXCLUDED.monthly_nights, updated_by = EXCLUDED.updated_by, updated_at = now();

-- name: DeleteFreeNightQuota :exec
DELETE FROM free_night_quotas WHERE tenant_id = @tenant_id AND property_id = @property_id AND occupancy_kind = @occupancy_kind;

-- The free nights of a kind in [from_date, to_date) that the quota counts: the nightly snapshot of CONFIRMED, CHECKED_IN
-- and COMPLETED lines on a plan of that kind (a line leaves the count with exclude_line_id when it is being amended).
-- name: CountFreeNights :one
SELECT count(*)::int FROM reservation_room_rates n
JOIN reservation_rooms l ON l.property_id = n.property_id AND l.id = n.reservation_room_id
JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
WHERE n.tenant_id = @tenant_id AND n.property_id = @property_id AND rp.occupancy_kind = @occupancy_kind
  AND l.status IN ('CONFIRMED', 'CHECKED_IN', 'COMPLETED')
  AND n.stay_date >= @from_date::date AND n.stay_date < @to_date::date
  AND (sqlc.narg(exclude_line_id)::bigint IS NULL OR l.id <> sqlc.narg(exclude_line_id)::bigint);
