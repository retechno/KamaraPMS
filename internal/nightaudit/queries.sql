-- Night audit queries (sqlc). Read-only: night audit writes nothing itself; it orchestrates the services that own
-- the tables. Every query is scoped by tenant_id and property_id.

-- Check 2: CONFIRMED room lines whose arrival is due.
-- name: ListUnresolvedArrivals :many
SELECT l.id AS reservation_room_id, l.reservation_id, res.confirmation_number, t.code AS room_type_code,
       r.room_number, l.arrival_date, g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
LEFT JOIN rooms r ON r.property_id = l.property_id AND r.id = l.room_id
LEFT JOIN guests g ON g.tenant_id = res.tenant_id AND g.id = COALESCE(l.guest_id, res.guest_id)
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED' AND l.arrival_date <= @bd::date
ORDER BY l.arrival_date, l.id;

-- Check 3: OPEN stays that should have left.
-- name: ListUnresolvedDepartures :many
SELECT s.id AS stay_id, s.stay_number, s.departure_date, r.room_number, g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM stays s
LEFT JOIN stay_rooms sr ON sr.property_id = s.property_id AND sr.stay_id = s.id AND sr.check_out_at IS NULL
LEFT JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN' AND s.departure_date <= @bd::date
ORDER BY s.departure_date, s.id;

-- Warnings.
-- name: ListStaleDrafts :many
SELECT l.id AS reservation_room_id, l.reservation_id, res.confirmation_number, l.arrival_date
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'DRAFT' AND l.arrival_date <= @bd::date
ORDER BY l.arrival_date, l.id;

-- OPEN folios without a stay, with a balance, whose reservation has only cancelled or no-show rooms.
-- name: ListOpenFoliosOfDeadReservations :many
SELECT f.id AS folio_id, f.folio_number, res.confirmation_number,
       COALESCE((SELECT sum(i.debit - i.credit) FROM folio_items i WHERE i.property_id = f.property_id AND i.folio_id = f.id), 0)::numeric AS balance
FROM folios f
JOIN reservations res ON res.property_id = f.property_id AND res.id = f.reservation_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.status = 'OPEN' AND f.stay_id IS NULL
  AND NOT EXISTS (SELECT 1 FROM reservation_rooms l WHERE l.property_id = f.property_id AND l.reservation_id = f.reservation_id
                  AND l.status NOT IN ('CANCELLED', 'NO_SHOW'))
  AND COALESCE((SELECT sum(i.debit - i.credit) FROM folio_items i WHERE i.property_id = f.property_id AND i.folio_id = f.id), 0) <> 0
ORDER BY f.id;

-- name: ListBlocksEndingAt :many
SELECT b.id AS block_id, b.block_type, r.room_number, b.end_date
FROM room_blocks b JOIN rooms r ON r.property_id = b.property_id AND r.id = b.room_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'ACTIVE' AND b.end_date = @bd::date
ORDER BY b.id;

-- Housekeeping step: rooms that have an open segment of an OPEN stay.
-- name: ListOccupiedRoomIDs :many
SELECT DISTINCT sr.room_id
FROM stay_rooms sr JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.check_out_at IS NULL AND s.status = 'OPEN'
ORDER BY sr.room_id;

-- ---------------------------------------------------------------- closing summary

-- name: SummaryRooms :one
SELECT
    (SELECT count(*) FROM rooms rm WHERE rm.tenant_id = @tenant_id AND rm.property_id = @property_id AND rm.is_active)::int AS total,
    (SELECT count(DISTINCT b.room_id) FROM room_blocks b JOIN rooms r ON r.property_id = b.property_id AND r.id = b.room_id AND r.is_active
      WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'ACTIVE' AND b.block_type = 'OOO'
        AND b.start_date <= @bd::date AND @bd::date < b.end_date)::int AS out_of_order,
    (SELECT count(DISTINCT b.room_id) FROM room_blocks b JOIN rooms r ON r.property_id = b.property_id AND r.id = b.room_id AND r.is_active
      WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'ACTIVE' AND b.block_type = 'OOS'
        AND b.start_date <= @bd::date AND @bd::date < b.end_date
        AND NOT EXISTS (SELECT 1 FROM room_blocks o WHERE o.property_id = b.property_id AND o.room_id = b.room_id AND o.status = 'ACTIVE'
                        AND o.block_type = 'OOO' AND o.start_date <= @bd::date AND @bd::date < o.end_date))::int AS out_of_service,
    (SELECT count(DISTINCT sr.room_id) FROM stay_rooms sr JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
      WHERE sr.tenant_id = @tenant_id AND sr.property_id = @property_id AND sr.check_out_at IS NULL AND s.status = 'OPEN')::int AS occupied;

-- Stays that arrived on the business date.
-- name: SummaryArrivals :one
SELECT count(*)::int FROM stays WHERE tenant_id = @tenant_id AND property_id = @property_id AND arrival_date = @bd::date AND status <> 'CANCELLED';

-- Stays whose last segment ended on the business date and that were checked out.
-- name: SummaryDepartures :one
SELECT count(*)::int FROM stays s
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'CHECKED_OUT'
  AND (SELECT max(sr.end_business_date) FROM stay_rooms sr WHERE sr.property_id = s.property_id AND sr.stay_id = s.id) = @bd::date;

-- name: SummaryNoShows :one
SELECT count(*)::int FROM audit_logs
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @bd::date AND action = 'reservation.no_show';

-- Room nights charged by the room posting service for the business date (and not reversed).
-- name: SummaryRoomNights :one
SELECT count(*)::int FROM folio_items i
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.service_date = @bd::date
  AND i.transaction_type = 'CHARGE' AND i.source = 'ROOM_POSTING'
  AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = i.property_id AND rv.reverses_item_id = i.id);

-- Revenue posted on the business date by charge type (signed, so corrections net out).
-- name: SummaryRevenueByType :many
SELECT c.charge_type, sum(i.net_amount)::numeric AS net, sum(i.service_charge_total)::numeric AS service, sum(i.tax_total)::numeric AS tax
FROM folio_items i JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date = @bd::date
  AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL')
GROUP BY c.charge_type
ORDER BY c.charge_type;

-- name: SummaryPaymentsByMethod :many
SELECT payment_method,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'PAYMENT'), 0)::numeric AS payments,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'REFUND'), 0)::numeric AS refunds
FROM payments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @bd::date AND status = 'POSTED'
GROUP BY payment_method
ORDER BY payment_method;
