-- The daily flash report (docs/architecture/38-reports-night-audit-pack.md, 7.1). Every query takes one range of business dates and is called once for each column
-- of the report (today, MTD, MTD of last month, MTD of last year, YTD). All of them are reads of the append-only ledger.

-- The business day of the flash, with who closed it.
-- name: FlashDay :one
SELECT d.business_date, d.status, d.closed_at, COALESCE(u.full_name, '')::text AS closed_by_name
FROM business_days d LEFT JOIN users u ON u.tenant_id = d.tenant_id AND u.id = d.closed_by
WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.business_date = @bd::date;

-- The open business day of the property (the flash of a day before it is a closed day; the one of the open day is interim).
-- name: FlashOpenDay :one
SELECT business_date FROM business_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'OPEN';

-- The room counts of the stored summaries of the closed days of a range (the closing summary is the book of the rooms: 04 12.5).
-- name: FlashRoomDays :one
SELECT count(*)::int AS days,
       COALESCE(sum((summary -> 'rooms' ->> 'total')::int), 0)::int AS total,
       COALESCE(sum((summary -> 'rooms' ->> 'out_of_order')::int), 0)::int AS out_of_order,
       COALESCE(sum((summary -> 'rooms' ->> 'out_of_service')::int), 0)::int AS out_of_service,
       COALESCE(sum((summary -> 'rooms' ->> 'house_use')::int), 0)::int AS house_use,
       COALESCE(sum((summary -> 'rooms' ->> 'occupied')::int), 0)::int AS occupied,
       COALESCE(sum((summary -> 'rooms' ->> 'complimentary')::int), 0)::int AS complimentary,
       COALESCE(sum((summary -> 'rooms' ->> 'sold')::int), 0)::int AS sold,
       COALESCE(sum((summary ->> 'arrivals')::int), 0)::int AS arrivals,
       COALESCE(sum((summary ->> 'departures')::int), 0)::int AS departures,
       COALESCE(sum((summary -> 'room_revenue' ->> 'net')::numeric), 0)::numeric AS room_net
FROM business_days
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'CLOSED' AND summary IS NOT NULL
  AND business_date BETWEEN @from_date::date AND @to_date::date;

-- The paid room nights of a range by the room type of the room of the night (the room type of the booking when the night has no room).
-- name: FlashSoldByType :many
SELECT rt.id AS room_type_id, rt.code, rt.name, count(*)::int AS sold
FROM stay_charge_postings p
JOIN stays s ON s.property_id = p.property_id AND s.id = p.stay_id
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
LEFT JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
LEFT JOIN stay_rooms sr ON sr.property_id = p.property_id AND sr.id = p.stay_room_id
LEFT JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id
JOIN room_types rt ON rt.property_id = l.property_id AND rt.id = COALESCE(rm.room_type_id, l.room_type_id)
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.service_date BETWEEN @from_date::date AND @to_date::date
  AND p.charge_source = 'ROOM_NIGHT' AND p.status = 'POSTED' AND COALESCE(rp.occupancy_kind, 'PAID') = 'PAID'
GROUP BY rt.id
ORDER BY rt.sort_order, rt.code;

-- The persons of the nights of a range, in the sold and the complimentary rooms (the rooms the hotel uses itself are left out).
-- name: FlashGuestNights :one
SELECT COALESCE(sum(s.adult_count + s.child_count), 0)::int AS guests, count(*)::int AS room_nights
FROM stay_charge_postings p
JOIN stays s ON s.property_id = p.property_id AND s.id = p.stay_id
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
LEFT JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.service_date BETWEEN @from_date::date AND @to_date::date
  AND p.charge_source = 'ROOM_NIGHT' AND p.status = 'POSTED' AND COALESCE(rp.occupancy_kind, 'PAID') <> 'HOUSE_USE';

-- Stays checked in on a range of business dates whose reservation was made at the desk.
-- name: FlashWalkIns :one
SELECT count(*)::int
FROM audit_logs a
JOIN stays s ON s.property_id = a.property_id AND s.id = a.entity_id
JOIN reservation_rooms l ON l.property_id = s.property_id AND l.id = s.reservation_room_id
JOIN reservations res ON res.property_id = l.property_id AND res.id = l.reservation_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.action = 'stay.checked_in'
  AND a.business_date BETWEEN @from_date::date AND @to_date::date AND res.source = 'WALK_IN';

-- The check-outs of a range of business dates: how many, how early or late against the departure date of the stay, and the nights they stayed (the length of stay).
-- name: FlashCheckOuts :one
SELECT count(*)::int AS stays,
       count(*) FILTER (WHERE a.business_date < s.departure_date)::int AS early,
       count(*) FILTER (WHERE a.business_date > s.departure_date)::int AS late,
       COALESCE(sum(a.business_date - s.arrival_date), 0)::int AS nights
FROM audit_logs a
JOIN stays s ON s.property_id = a.property_id AND s.id = a.entity_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.action = 'stay.checked_out'
  AND a.business_date BETWEEN @from_date::date AND @to_date::date;

-- The events of the reservations in a range of business dates, from the audit log (the business date of a cancellation or a no-show is only there: 38 F5).
-- name: FlashActivity :one
SELECT count(*) FILTER (WHERE action = 'reservation.created')::int AS made,
       count(*) FILTER (WHERE action IN ('reservation.cancelled', 'reservation.room_cancelled'))::int AS cancelled,
       count(*) FILTER (WHERE action = 'reservation.no_show')::int AS no_shows
FROM audit_logs
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date::date AND @to_date::date
  AND action IN ('reservation.created', 'reservation.cancelled', 'reservation.room_cancelled', 'reservation.no_show');

-- The state of the books about tomorrow: confirmed rooms arriving, and stays in house leaving, on a date.
-- name: FlashExpectedOn :one
SELECT
    (SELECT count(*) FROM reservation_rooms l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'CONFIRMED' AND l.arrival_date = @on_date::date)::int AS arrivals,
    (SELECT count(*) FROM stays s WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN' AND s.departure_date = @on_date::date)::int AS departures;

-- The covers of the POS outlets: the guests of the bills closed in a range, and the bills that did not count them.
-- name: FlashPosCovers :many
SELECT o.id AS outlet_id, o.name, COALESCE(sum(b.guest_count), 0)::int AS covers, count(*)::int AS bills,
       count(*) FILTER (WHERE b.guest_count IS NULL)::int AS bills_without_covers
FROM pos_bills b JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'CLOSED' AND b.closed_business_date BETWEEN @from_date::date AND @to_date::date
GROUP BY o.id
ORDER BY o.sort_order, o.name;

-- The pax of the banquet sessions held in a range: the actual count, else the guaranteed one (the sessions that used the fallback are counted).
-- name: FlashBanquetPax :one
SELECT COALESCE(sum(COALESCE(s.pax_actual, s.pax_guaranteed)), 0)::int AS pax,
       count(*) FILTER (WHERE s.pax_actual IS NULL AND s.pax_guaranteed IS NOT NULL)::int AS fallback_sessions
FROM banquet_sessions s JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'ACTIVE' AND e.status IN ('DEFINITE', 'COMPLETED')
  AND s.session_date BETWEEN @from_date::date AND @to_date::date;

-- Net revenue of a range by the group of the revenue account, the folio (outlet, banquet event or hotel) and the charge code. Charges, adjustments and reversals count;
-- a transfer between folios is not revenue; the reversal of a payment (a void) is not a charge. An item with no known revenue account has the group ''.
-- name: FlashRevenue :many
SELECT COALESCE(a.statement_group, '')::text AS statement_group,
       COALESCE(f.outlet_id, 0)::bigint AS outlet_id, COALESCE(o.name, '')::text AS outlet_name,
       (f.banquet_event_id IS NOT NULL)::boolean AS is_event,
       c.id AS charge_code_id, c.code AS charge_code, c.name AS charge_code_name,
       COALESCE(i.revenue_account_code, '')::text AS account_code,
       sum(i.net_amount)::numeric AS net, sum(i.service_charge_total)::numeric AS service, sum(i.tax_total)::numeric AS tax
FROM folio_items i
JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id
JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
LEFT JOIN pos_outlets o ON o.property_id = f.property_id AND o.id = f.outlet_id
LEFT JOIN gl_accounts a ON a.property_id = i.property_id AND a.code = i.revenue_account_code
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date::date AND @to_date::date
  AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND c.charge_type <> 'TRANSFER'
  AND NOT EXISTS (SELECT 1 FROM folio_items r WHERE r.property_id = i.property_id AND r.id = i.reverses_item_id AND r.transaction_type IN ('PAYMENT', 'REFUND'))
GROUP BY a.statement_group, f.outlet_id, o.name, (f.banquet_event_id IS NOT NULL), c.id, i.revenue_account_code
ORDER BY 1, 2, 4, c.code;

-- Revenue of a range in the journals, by revenue group: the figure of the income statement (the closing journals are left out).
-- name: FlashJournalRevenue :many
SELECT COALESCE(a.statement_group, '')::text AS statement_group, sum(l.credit - l.debit)::numeric AS amount
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND a.account_type = @account_type::text AND NOT j.is_closing
  AND j.journal_date BETWEEN @from_date::date AND @to_date::date
GROUP BY a.statement_group;

-- The payments and refunds of a range that stand (a voided payment is out), cut by who took them: the outlet of the folio, else the shift (the cashier and drawer),
-- else nobody (back office).
-- name: FlashPayments :many
SELECT p.payment_method, p.payment_type,
       CASE WHEN f.outlet_id IS NOT NULL THEN 'OUTLET' WHEN p.shift_id IS NOT NULL THEN 'FRONT' ELSE 'BACK' END::text AS cut,
       COALESCE(f.outlet_id, 0)::bigint AS outlet_id, COALESCE(o.name, '')::text AS outlet_name,
       COALESCE(sh.user_id, 0)::bigint AS cashier_id, COALESCE(u.full_name, '')::text AS cashier_name, COALESCE(sh.drawer, '')::text AS drawer,
       count(*)::int AS payments, sum(p.amount)::numeric AS amount
FROM payments p
JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id
LEFT JOIN pos_outlets o ON o.property_id = f.property_id AND o.id = f.outlet_id
LEFT JOIN cashier_shifts sh ON sh.property_id = p.property_id AND sh.id = p.shift_id
LEFT JOIN users u ON u.tenant_id = sh.tenant_id AND u.id = sh.user_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.status = 'POSTED' AND p.business_date BETWEEN @from_date::date AND @to_date::date
GROUP BY p.payment_method, p.payment_type, f.outlet_id, o.name, p.shift_id, sh.user_id, u.full_name, sh.drawer;

-- The change of the guest ledger in a range: debit less credit of every folio item (the same figure as the folio balance of the control sources of the accounts).
-- name: FlashLedgerChange :one
SELECT COALESCE(sum(debit - credit), 0)::numeric AS change
FROM folio_items
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date::date AND @to_date::date;

-- The fiscal calendar and the active budget of a fiscal year (the budget module owns them; the flash only reads).
-- name: FlashFiscalStartMonth :one
SELECT fiscal_year_start_month FROM accounting_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: FlashActiveBudget :one
SELECT id FROM budgets WHERE tenant_id = @tenant_id AND property_id = @property_id AND year_start = @year_start::date AND status = 'ACTIVE';

-- The money budget of one month by revenue group.
-- name: FlashBudgetByGroup :many
SELECT COALESCE(a.statement_group, '')::text AS statement_group, sum(l.amount)::numeric AS amount
FROM budget_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.budget_id = @budget_id AND l.month = @month::smallint AND a.account_type = @account_type::text
GROUP BY a.statement_group;

-- The budget of the statistics of one month.
-- name: FlashBudgetStats :one
SELECT rooms_available, rooms_sold, adr FROM budget_statistics
WHERE tenant_id = @tenant_id AND property_id = @property_id AND budget_id = @budget_id AND month = @month::smallint;

-- The last closed business day of the property (the default date of the flash).
-- name: FlashLastClosedDay :one
SELECT business_date FROM business_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'CLOSED' ORDER BY business_date DESC LIMIT 1;
