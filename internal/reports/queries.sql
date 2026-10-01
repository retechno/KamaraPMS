-- Report queries (sqlc). Read-only. Every query is scoped by tenant_id and property_id. Money reports read the
-- ledger rows and their component snapshots by business date, never the masters.

-- name: GetBusinessDay :one
SELECT business_date, status, summary FROM business_days
WHERE property_id = @property_id AND business_date = @bd::date;

-- Closed days with a stored summary, for the statistics.
-- name: ListClosedDaySummaries :many
SELECT business_date, summary FROM business_days
WHERE property_id = @property_id AND status = 'CLOSED' AND business_date BETWEEN @from_date::date AND @to_date::date
ORDER BY business_date;

-- name: RevenueByChargeCode :many
SELECT c.id AS charge_code_id, c.code, c.name, c.charge_type, i.revenue_account_code,
       count(*)::int AS items,
       sum(i.base_amount)::numeric AS base, sum(i.discount_amount)::numeric AS discount, sum(i.net_amount)::numeric AS net,
       sum(i.service_charge_total)::numeric AS service, sum(i.tax_total)::numeric AS tax, sum(i.debit - i.credit)::numeric AS total
FROM folio_items i JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date::date AND @to_date::date
  AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL')
GROUP BY c.id, c.code, c.name, c.charge_type, i.revenue_account_code
ORDER BY c.charge_type, c.code, i.revenue_account_code NULLS FIRST;

-- Tax and service report from the component snapshots: a rate edited later shows up as its own line.
-- name: TaxReport :many
SELECT k.component_type, k.code, k.name, k.rate, k.gl_account_code,
       count(*)::int AS items, sum(k.base_amount)::numeric AS base, sum(k.amount)::numeric AS amount
FROM folio_item_components k JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date::date AND @to_date::date
GROUP BY k.component_type, k.code, k.name, k.rate, k.gl_account_code
ORDER BY k.component_type DESC, k.code, k.rate, k.gl_account_code NULLS FIRST;

-- name: CashierByMethod :many
SELECT business_date, payment_method,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'PAYMENT' AND status = 'POSTED'), 0)::numeric AS payments,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'REFUND' AND status = 'POSTED'), 0)::numeric AS refunds,
       count(*) FILTER (WHERE status = 'POSTED')::int AS count,
       COALESCE(sum(amount) FILTER (WHERE status = 'VOIDED'), 0)::numeric AS voided,
       count(*) FILTER (WHERE status = 'VOIDED')::int AS voided_count
FROM payments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date::date AND @to_date::date
GROUP BY business_date, payment_method
ORDER BY business_date, payment_method;

-- name: ReportInHouse :many
SELECT s.id AS stay_id, s.stay_number, res.confirmation_number, s.arrival_date, s.departure_date, s.adult_count, s.child_count,
       r.room_number, g.first_name AS guest_first_name, g.last_name AS guest_last_name,
       COALESCE((SELECT sum(i.debit - i.credit) FROM folio_items i JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id
                 WHERE f.property_id = s.property_id AND f.stay_id = s.id), 0)::numeric AS balance
FROM stays s
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
LEFT JOIN stay_rooms sr ON sr.property_id = s.property_id AND sr.stay_id = s.id AND sr.check_out_at IS NULL
LEFT JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN'
ORDER BY r.room_number, s.id;

-- name: ReportArrivals :many
SELECT l.id AS reservation_room_id, res.confirmation_number, l.status, l.arrival_date, l.departure_date, l.adult_count, l.child_count,
       t.code AS room_type_code, r.room_number, g.first_name AS guest_first_name, g.last_name AS guest_last_name
FROM reservation_rooms l
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN room_types t ON t.property_id = l.property_id AND t.id = l.room_type_id
LEFT JOIN rooms r ON r.property_id = l.property_id AND r.id = l.room_id
LEFT JOIN guests g ON g.tenant_id = res.tenant_id AND g.id = COALESCE(l.guest_id, res.guest_id)
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.arrival_date = @on_date::date AND l.status <> 'DRAFT'
ORDER BY l.status, t.code, l.id;

-- Stays that leave (or left) on the date, with the room of their last segment.
-- name: ReportDepartures :many
SELECT s.id AS stay_id, s.stay_number, s.status, s.arrival_date, s.departure_date, res.confirmation_number,
       (SELECT r.room_number FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
         WHERE sr.property_id = s.property_id AND sr.stay_id = s.id ORDER BY sr.start_business_date DESC, sr.id DESC LIMIT 1) AS room_number,
       g.first_name AS guest_first_name, g.last_name AS guest_last_name,
       COALESCE((SELECT sum(i.debit - i.credit) FROM folio_items i JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id
                 WHERE f.property_id = s.property_id AND f.stay_id = s.id), 0)::numeric AS balance
FROM stays s
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.departure_date = @on_date::date AND s.status <> 'CANCELLED'
ORDER BY s.status, s.id;
