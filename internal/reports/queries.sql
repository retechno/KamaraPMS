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

-- Folio payments plus the money companies paid against their city ledger account (receipts count as payments).
-- name: CashierByMethod :many
SELECT business_date, payment_method,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'PAYMENT' AND status = 'POSTED'), 0)::numeric AS payments,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'REFUND' AND status = 'POSTED'), 0)::numeric AS refunds,
       count(*) FILTER (WHERE status = 'POSTED')::int AS count,
       COALESCE(sum(amount) FILTER (WHERE status = 'VOIDED'), 0)::numeric AS voided,
       count(*) FILTER (WHERE status = 'VOIDED')::int AS voided_count
FROM (
    SELECT p.business_date, p.payment_method, p.payment_type, p.status, p.amount FROM payments p
    WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date BETWEEN @from_date::date AND @to_date::date
    UNION ALL
    SELECT r.business_date, r.payment_method, 'PAYMENT'::varchar, r.status, r.amount FROM city_ledger_receipts r
    WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.business_date BETWEEN @from_date::date AND @to_date::date
) x
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

-- Housekeeping work per person and business date. Rooms taken to CLEAN or INSPECTED by hand come from the status log
-- (a room cleaned twice counts twice); finished and skipped tasks come from the cleaning list, with the minutes
-- between starting and finishing the tasks that were started.
-- name: ReportHousekeepingCleaned :many
SELECT l.business_date, l.changed_by AS user_id, u.full_name,
       count(*) FILTER (WHERE l.to_status = 'CLEAN')::int AS cleaned,
       count(*) FILTER (WHERE l.to_status = 'INSPECTED')::int AS inspected
FROM housekeeping_logs l
JOIN users u ON u.tenant_id = l.tenant_id AND u.id = l.changed_by
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.business_date BETWEEN @from_date::date AND @to_date::date
  AND l.source = 'MANUAL' AND l.to_status IN ('CLEAN', 'INSPECTED')
GROUP BY l.business_date, l.changed_by, u.full_name;

-- name: ReportHousekeepingTasks :many
SELECT t.task_date, t.completed_by AS user_id, u.full_name,
       count(*) FILTER (WHERE t.status = 'DONE')::int AS done,
       count(*) FILTER (WHERE t.status = 'SKIPPED')::int AS skipped,
       count(*) FILTER (WHERE t.status = 'DONE' AND t.started_at IS NOT NULL)::int AS timed,
       COALESCE(sum(extract(epoch FROM (t.completed_at - t.started_at)) / 60) FILTER (WHERE t.status = 'DONE' AND t.started_at IS NOT NULL), 0)::numeric AS minutes
FROM housekeeping_tasks t
JOIN users u ON u.tenant_id = t.tenant_id AND u.id = t.completed_by
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.task_date BETWEEN @from_date::date AND @to_date::date
  AND t.status IN ('DONE', 'SKIPPED')
GROUP BY t.task_date, t.completed_by, u.full_name;

-- Rooms that are not clean yet and for how long (database time, so the age does not depend on the application clock).
-- name: ReportHousekeepingDirty :many
SELECT r.room_number, r.floor, rt.code AS room_type_code, h.status, h.updated_at AS since,
       floor(extract(epoch FROM (now() - h.updated_at)) / 3600)::int AS hours,
       CASE
           WHEN EXISTS (SELECT 1 FROM stay_rooms sr JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
                        WHERE sr.property_id = r.property_id AND sr.room_id = r.id AND sr.check_out_at IS NULL AND s.status = 'OPEN') THEN 'OCCUPIED'
           WHEN EXISTS (SELECT 1 FROM reservation_rooms rr
                        WHERE rr.property_id = r.property_id AND rr.room_id = r.id AND rr.status = 'CONFIRMED'
                          AND rr.arrival_date <= @business_date::date AND @business_date::date < rr.departure_date) THEN 'RESERVED'
           ELSE 'VACANT'
       END::text AS occupancy,
       COALESCE(f.priority, 'NORMAL')::text AS priority, COALESCE(f.dnd, false)::boolean AS dnd,
       b.block_type
FROM room_housekeeping h
JOIN rooms r ON r.property_id = h.property_id AND r.id = h.room_id AND r.is_active
JOIN room_types rt ON rt.property_id = r.property_id AND rt.id = r.room_type_id
LEFT JOIN room_hk_flags f ON f.property_id = r.property_id AND f.room_id = r.id
LEFT JOIN room_blocks b ON b.property_id = r.property_id AND b.room_id = r.id AND b.status = 'ACTIVE'
    AND b.start_date <= @business_date::date AND @business_date::date < b.end_date
WHERE h.tenant_id = @tenant_id AND h.property_id = @property_id AND h.status IN ('DIRTY', 'CLEANING')
  AND floor(extract(epoch FROM (now() - h.updated_at)) / 3600) >= @min_hours::int
ORDER BY h.updated_at, r.room_number;

-- Maintenance requests reported in the range, by category, with how they ended.
-- name: ReportMaintenanceByCategory :many
SELECT category,
       count(*)::int AS reported,
       count(*) FILTER (WHERE status = 'RESOLVED')::int AS resolved,
       count(*) FILTER (WHERE status = 'CANCELLED')::int AS cancelled,
       count(*) FILTER (WHERE status IN ('OPEN', 'IN_PROGRESS'))::int AS still_open,
       COALESCE(sum(extract(epoch FROM (closed_at - reported_at)) / 3600) FILTER (WHERE status = 'RESOLVED'), 0)::numeric AS resolve_hours
FROM maintenance_requests
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date::date AND @to_date::date
GROUP BY category
ORDER BY category;

-- What is open now, whatever its age.
-- name: ReportMaintenanceBacklog :one
SELECT count(*)::int AS open_now,
       count(*) FILTER (WHERE priority IN ('HIGH', 'URGENT'))::int AS high_priority,
       COALESCE(floor(max(extract(epoch FROM (now() - reported_at))) / 3600), 0)::int AS oldest_hours
FROM maintenance_requests
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status IN ('OPEN', 'IN_PROGRESS');

-- Active rooms by housekeeping status, for the dashboard.
-- name: DashboardRoomStatus :many
SELECT h.status::text AS status, count(*)::int AS rooms
FROM room_housekeeping h
JOIN rooms r ON r.property_id = h.property_id AND r.id = h.room_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.is_active
GROUP BY h.status;

-- Rooms sold per night ahead, whatever their type: sellable rooms (active, not blocked) against the rooms held by
-- confirmed lines and open stays (the same demand as the availability inventory).
-- name: DashboardForecast :many
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
