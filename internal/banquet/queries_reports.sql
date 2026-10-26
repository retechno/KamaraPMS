-- Banquet, PR 4 (docs/architecture/36-banquet.md 4.9, 4.10): the stock of the banquet and the reports of B15. Every query is scoped by tenant_id and property_id. Nothing here writes.

-- ---------------------------------------------------------------- the warehouse of the banquet
-- name: WarehouseOfProperty :one
SELECT id, code, name, is_active FROM inv_warehouses WHERE property_id = @property_id AND id = @id;

-- name: ProductsWithRecipe :many
SELECT DISTINCT product_id FROM pos_recipe_lines WHERE property_id = @property_id AND product_id = ANY(@product_ids::bigint[]);

-- ---------------------------------------------------------------- the forecast
-- The active sessions of tentative and definite events in the period, with what the forecast groups by.
-- name: ReportForecastSessions :many
SELECT e.id AS event_id, e.number AS event_number, e.name AS event_name, e.status, COALESCE(e.event_type_id, 0)::bigint AS event_type_id, COALESCE(t.code, '')::text AS type_code, COALESCE(t.name, '')::text AS type_name,
       e.sales_user_id, u.full_name AS sales_name, e.option_until, s.id AS session_id, s.session_date
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
LEFT JOIN banquet_event_types t ON t.property_id = e.property_id AND t.id = e.event_type_id
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status IN ('DEFINITE', 'TENTATIVE') AND s.status = 'ACTIVE'
  AND s.session_date BETWEEN @from_date AND @to_date
ORDER BY s.session_date, e.id, s.id;

-- ---------------------------------------------------------------- revenue and occupancy by room
-- The sessions that hold a room (definite events, and completed ones), with their time: the hours of the occupancy. A session that ends the next day lasts until the end time of the next day.
-- name: ReportRoomSessions :many
SELECT s.id AS session_id, s.room_id, s.session_date, s.start_time, s.end_time, s.ends_next_day
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status IN ('DEFINITE', 'COMPLETED') AND s.status = 'ACTIVE' AND s.room_id IS NOT NULL
  AND s.start_time IS NOT NULL AND s.end_time IS NOT NULL AND s.session_date BETWEEN @from_date AND @to_date;

-- The net revenue posted by the sessions that were posted in the period, by the room of the session (a reversal of a posted line takes its net off).
-- name: ReportRoomRevenue :many
SELECT s.room_id, count(DISTINCT bp.id)::int AS sessions, (COALESCE(sum(fi.net_amount), 0) + COALESCE(sum(rv.net_amount), 0))::numeric AS net
FROM banquet_session_postings bp
JOIN banquet_sessions s ON s.property_id = bp.property_id AND s.id = bp.session_id
JOIN banquet_posting_items pi ON pi.property_id = bp.property_id AND pi.posting_id = bp.id
JOIN folio_items fi ON fi.property_id = pi.property_id AND fi.id = pi.folio_item_id
LEFT JOIN folio_items rv ON rv.property_id = fi.property_id AND rv.reverses_item_id = fi.id
WHERE bp.tenant_id = @tenant_id AND bp.property_id = @property_id AND bp.business_date BETWEEN @from_date AND @to_date AND s.room_id IS NOT NULL
GROUP BY s.room_id;

-- ---------------------------------------------------------------- guaranteed against actual
-- name: ReportGuests :many
SELECT s.id AS session_id, e.id AS event_id, e.number AS event_number, e.name AS event_name, s.session_no, s.name AS session_name, s.session_date,
       r.name AS room_name, s.pax_estimated, s.pax_guaranteed, s.pax_actual
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
LEFT JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status IN ('DEFINITE', 'COMPLETED') AND s.status = 'ACTIVE'
  AND s.session_date BETWEEN @from_date AND @to_date
ORDER BY s.session_date, e.id, s.session_no;

-- ---------------------------------------------------------------- production by sales and the lost reasons
-- The events of the period by the three dates that matter, in BUSINESS dates: opened, made definite, lost. They are kept when the change is made (an event made after midnight, before the night audit, is of the open day).
-- name: ReportSalesEvents :many
SELECT e.id AS event_id, e.status, e.sales_user_id, u.full_name AS sales_name, e.contract_value_estimate,
       to_char(e.inquiry_on, 'YYYY-MM-DD')::text AS created_on,
       COALESCE(to_char(e.definite_on, 'YYYY-MM-DD'), '')::text AS definite_on,
       (CASE WHEN e.status = 'LOST' THEN COALESCE(to_char(e.closed_on, 'YYYY-MM-DD'), '') ELSE '' END)::text AS lost_on
FROM banquet_events e
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id
  AND (e.inquiry_on BETWEEN @from_date::date AND @to_date::date
    OR e.definite_on BETWEEN @from_date::date AND @to_date::date
    OR (e.status = 'LOST' AND e.closed_on BETWEEN @from_date::date AND @to_date::date))
ORDER BY e.id;

-- name: ReportLostEvents :many
SELECT e.id AS event_id, e.number AS event_number, e.name AS event_name, e.status, e.sales_user_id, u.full_name AS sales_name, e.contract_value_estimate,
       COALESCE(r.code, '')::text AS reason_code, COALESCE(r.name, '')::text AS reason_name, e.reason_note,
       COALESCE(to_char(e.closed_on, 'YYYY-MM-DD'), '')::text AS closed_on
FROM banquet_events e
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
LEFT JOIN banquet_reasons r ON r.property_id = e.property_id AND r.id = e.reason_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status IN ('LOST', 'CANCELLED')
  AND e.closed_on BETWEEN @from_date::date AND @to_date::date
ORDER BY e.closed_on DESC, e.closed_at DESC, e.id DESC;

-- ---------------------------------------------------------------- the daily schedule
-- name: ReportSchedule :many
SELECT s.id AS session_id, e.id AS event_id, e.number AS event_number, e.name AS event_name, e.status, s.session_no, s.name AS session_name,
       s.start_time, s.end_time, s.ends_next_day, r.name AS room_name, s.setup_style, s.pax_estimated, s.pax_guaranteed, s.pax_actual,
       e.contact_name, e.contact_phone, u.full_name AS sales_name,
       COALESCE(rev.revision_no, 0)::int AS beo_revision,
       COALESCE(unread.n, 0)::int AS beo_unread
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
LEFT JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
LEFT JOIN LATERAL (SELECT max(v.revision_no) AS revision_no FROM banquet_beo_revisions v WHERE v.property_id = e.property_id AND v.event_id = e.id) rev ON true
LEFT JOIN LATERAL (SELECT count(*) AS n FROM banquet_beo_sections b
                    WHERE b.property_id = s.property_id AND b.session_id = s.id AND b.section <> 'GENERAL' AND b.body <> ''
                      AND NOT EXISTS (SELECT 1 FROM banquet_beo_ack a WHERE a.session_id = s.id AND a.section = b.section AND a.revision_no = rev.revision_no)) unread ON true
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status IN ('TENTATIVE', 'DEFINITE', 'COMPLETED') AND s.status = 'ACTIVE'
  AND s.session_date = @day
ORDER BY s.start_time NULLS LAST, e.id, s.session_no;
