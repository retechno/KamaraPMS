-- Expected Charge Engine loader (sqlc): read-only snapshot queries. Scoped by tenant and property.

-- name: ListOpenStayIDs :many
SELECT id FROM stays WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'OPEN' ORDER BY id;

-- Stays in scope: the listed ones, or every OPEN stay when the list is empty.
-- name: ListScopeStays :many
SELECT s.id, s.stay_number, s.status, s.arrival_date, s.departure_date, s.reservation_room_id, l.status AS line_status,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM stays s
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
  AND ((cardinality(@stay_ids::bigint[]) = 0 AND s.status = 'OPEN') OR s.id = ANY(@stay_ids::bigint[]))
ORDER BY s.id;

-- name: ListScopeSegments :many
SELECT sr.id, sr.stay_id, sr.room_id, r.room_number, sr.start_business_date, sr.end_business_date
FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.stay_id = ANY(@stay_ids::bigint[])
ORDER BY sr.stay_id, sr.start_business_date, sr.id;

-- name: ListScopeNights :many
SELECT n.reservation_room_id, n.stay_date, n.rate_plan_id, n.charge_code_id, c.code AS charge_code, c.is_active, (c.charge_type = 'ROOM')::boolean AS is_room,
       n.price_mode, n.amount
FROM reservation_room_rates n
JOIN charge_codes c ON c.property_id = n.property_id AND c.id = n.charge_code_id
WHERE n.tenant_id = @tenant_id AND n.property_id = @property_id AND n.reservation_room_id = ANY(@line_ids::bigint[])
ORDER BY n.reservation_room_id, n.stay_date;

-- name: ListScopePostings :many
SELECT stay_id, service_date, folio_item_id, stay_room_id FROM stay_charge_postings
WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = ANY(@stay_ids::bigint[]) AND status = 'POSTED' AND charge_source = 'ROOM_NIGHT';

