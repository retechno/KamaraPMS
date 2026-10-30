-- The four reports that follow the daily flash report (docs/architecture/38-reports-night-audit-pack.md, 7.2 to 7.5): cancellations and no-shows, the guest ledger and the deposit ledger,
-- the corrections, and the Z report of all drawers. Every query only reads.

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------
-- 7.2 Cancellations and no-shows. The events are the audit entries of the reservation module, by the business date of the event (audit_logs_bd_action_idx).

-- name: CancellationEvents :many
SELECT a.id AS audit_id, a.action, a.business_date AS event_date, a.created_at AS event_at, COALESCE(a.user_id, 0)::bigint AS user_id, COALESCE(u.full_name, '')::text AS user_name,
       a.entity_id AS reservation_id,
       COALESCE((a.old_data->>'reservation_room_id')::bigint, 0)::bigint AS line_id,
       COALESCE(a.new_data->>'reason', '')::text AS event_reason,
       COALESCE((a.new_data->>'rooms_cancelled')::int, 0)::int AS rooms_cancelled,
       r.confirmation_number, r.status AS reservation_status, r.source, r.market, COALESCE(r.company_id, 0)::bigint AS company_id,
       COALESCE(c.name, '')::text AS company_name, COALESCE(r.cancellation_reason, '')::text AS reservation_reason,
       trim(COALESCE(g.first_name, '') || ' ' || COALESCE(g.last_name, ''))::text AS guest_name
FROM audit_logs a
JOIN reservations r ON r.property_id = a.property_id AND r.id = a.entity_id
LEFT JOIN users u ON u.tenant_id = a.tenant_id AND u.id = a.user_id
LEFT JOIN guests g ON g.tenant_id = r.tenant_id AND g.id = r.guest_id
LEFT JOIN companies c ON c.tenant_id = r.tenant_id AND c.id = r.company_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.entity_type = 'reservation'
  AND a.business_date BETWEEN @from_date::date AND @to_date::date
  AND a.action = ANY(@actions::text[])
  AND (sqlc.narg(source)::text IS NULL OR r.source = sqlc.narg(source)::text)
  AND (sqlc.narg(company_id)::bigint IS NULL OR r.company_id = sqlc.narg(company_id)::bigint)
ORDER BY a.business_date, a.id;

-- The room lines of those reservations, with the nights they were worth (the rate snapshot of each night).
-- name: CancellationLines :many
SELECT l.id, l.reservation_id, l.status, l.arrival_date, l.departure_date, COALESCE(l.cancellation_reason, '')::text AS cancellation_reason, l.cancelled_at,
       rt.name AS room_type, rp.name AS rate_plan,
       COALESCE((SELECT sum(x.amount) FROM reservation_room_rates x WHERE x.property_id = l.property_id AND x.reservation_room_id = l.id), 0)::numeric AS night_value
FROM reservation_rooms l
JOIN room_types rt ON rt.property_id = l.property_id AND rt.id = l.room_type_id
JOIN rate_plans rp ON rp.property_id = l.property_id AND rp.id = l.rate_plan_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.reservation_id = ANY(@reservation_ids::bigint[])
ORDER BY l.reservation_id, l.id;

-- The cancellation and no-show fees posted for those reservations (the item of the charge codes CANCEL_FEE and NO_SHOW_FEE, tied by its reference).
-- name: CancellationFees :many
SELECT fi.id, fi.reference_type, COALESCE(fi.reference_id, '')::text AS reference_id, fi.business_date, fi.net_amount, (fi.debit - fi.credit)::numeric AS gross, cc.code AS charge_code,
       EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = fi.property_id AND rv.reverses_item_id = fi.id) AS reversed
FROM folio_items fi
JOIN charge_codes cc ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
WHERE fi.tenant_id = @tenant_id AND fi.property_id = @property_id AND fi.transaction_type = 'CHARGE' AND cc.code IN ('CANCEL_FEE', 'NO_SHOW_FEE')
  AND ((fi.reference_type = 'RESERVATION' AND fi.reference_id = ANY(@reservation_refs::text[]))
    OR (fi.reference_type = 'RESERVATION_ROOM' AND fi.reference_id = ANY(@line_refs::text[])))
ORDER BY fi.id;

-- The deposits held on the folio of the reservation that has no stay: what was received less what was given back, with the date of each movement.
-- name: CancellationDeposits :many
SELECT f.reservation_id AS reservation_id, g.business_date, (-g.signed_amount)::numeric AS amount
FROM folios f
JOIN folio_item_gl g ON g.property_id = f.property_id AND g.folio_id = f.id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.stay_id IS NULL AND f.reservation_id = ANY(@reservation_ids::bigint[])
  AND g.kind = 'PAYMENT' AND g.is_deposit
ORDER BY f.reservation_id, g.business_date;

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------
-- 7.3 The guest ledger and the deposit ledger as of a closed business date.

-- The business day row of a date: whether it exists and is closed.
-- name: ReportBusinessDayStatus :one
SELECT status FROM business_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @bd::date;

-- Each folio with a balance (or a deposit liability) as of the date. The sums are of folio_item_gl up to the date, so a later posting changes nothing; the liability is what the control sources
-- of the accounts count (EVERY_DAY: held less applied; ON_CLOSE: the held amount of a folio not closed by the date).
-- name: GuestLedgerFolios :many
WITH s AS (
    SELECT g.folio_id,
           COALESCE(sum(g.signed_amount) FILTER (WHERE g.kind = 'CHARGE'), 0)::numeric AS charges,
           (-COALESCE(sum(g.signed_amount) FILTER (WHERE g.kind = 'PAYMENT' AND NOT g.is_deposit), 0))::numeric AS settlements,
           (-COALESCE(sum(g.signed_amount) FILTER (WHERE g.kind = 'PAYMENT' AND g.is_deposit), 0))::numeric AS deposits,
           COALESCE(sum(g.signed_amount), 0)::numeric AS balance,
           COALESCE(min(g.business_date) FILTER (WHERE g.kind = 'CHARGE' AND g.signed_amount > 0), '1900-01-01'::date)::date AS oldest_charge
      FROM folio_item_gl g
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date <= @as_of::date
     GROUP BY g.folio_id
), d AS (
    SELECT (t.j->>'folio_id')::bigint AS folio_id, (t.j->>'held')::numeric AS held, (t.j->>'applied')::numeric AS applied, (t.j->>'closed')::boolean AS closed
      FROM (SELECT to_jsonb(fs) AS j FROM folio_deposit_state(@property_id, @as_of::date) fs) t
)
SELECT f.id AS folio_id, f.folio_number, f.folio_type, f.closed_on, COALESCE(f.reservation_id, 0)::bigint AS reservation_id,
       COALESCE(r.confirmation_number, '')::text AS confirmation_number,
       COALESCE(rm.room_number, '')::text AS room_number,
       trim(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, ''))::text AS guest_name,
       COALESCE(co.name, '')::text AS company_name, COALESCE(bg.name, '')::text AS group_name, COALESCE(po.name, '')::text AS outlet_name,
       COALESCE(be.name, '')::text AS event_name,
       COALESCE(st.arrival_date, (SELECT min(l.arrival_date) FROM reservation_rooms l WHERE l.property_id = f.property_id AND l.reservation_id = f.reservation_id), '1900-01-01'::date)::date AS arrival_date,
       COALESCE(st.departure_date, (SELECT max(l.departure_date) FROM reservation_rooms l WHERE l.property_id = f.property_id AND l.reservation_id = f.reservation_id), '1900-01-01'::date)::date AS departure_date,
       s.charges, s.settlements, s.deposits, s.balance, s.oldest_charge, (CASE WHEN d.folio_id IS NULL THEN 0 WHEN @every_day::boolean THEN d.held - d.applied WHEN d.closed THEN 0 ELSE d.held END)::numeric AS liability
FROM folios f
JOIN s ON s.folio_id = f.id
LEFT JOIN d ON d.folio_id = f.id
LEFT JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id
LEFT JOIN reservation_rooms rr ON rr.property_id = st.property_id AND rr.id = st.reservation_room_id
LEFT JOIN rooms rm ON rm.property_id = rr.property_id AND rm.id = rr.room_id
LEFT JOIN guests gu ON gu.tenant_id = f.tenant_id AND gu.id = COALESCE(st.guest_id, r.guest_id)
LEFT JOIN companies co ON co.tenant_id = f.tenant_id AND co.id = f.bill_to_company_id
LEFT JOIN booking_groups bg ON bg.tenant_id = f.tenant_id AND bg.id = f.booking_group_id
LEFT JOIN pos_outlets po ON po.tenant_id = f.tenant_id AND po.id = f.outlet_id
LEFT JOIN banquet_events be ON be.tenant_id = f.tenant_id AND be.id = f.banquet_event_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND (s.balance <> 0 OR (CASE WHEN d.folio_id IS NULL THEN 0 WHEN @every_day::boolean THEN d.held - d.applied WHEN d.closed THEN 0 ELSE d.held END) <> 0)
ORDER BY f.folio_number;

-- What the control sources of the accounts say as of the date (the same two sums as accounting.ControlSources) and the deposit release mode.
-- name: LedgerControlSources :one
SELECT
    (SELECT COALESCE(sum(i.debit - i.credit), 0) FROM folio_items i
      WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date <= @as_of::date)::numeric AS folio_balance,
    (SELECT CASE WHEN pr.deposit_release_mode = 'EVERY_DAY'
                 THEN COALESCE((SELECT sum(d.held - d.applied) FROM folio_deposit_state(@property_id, @as_of::date) d), 0)
                 ELSE COALESCE((SELECT sum(d.held) FROM folio_deposit_state(@property_id, @as_of::date) d WHERE NOT d.closed), 0)
            END FROM properties pr WHERE pr.id = @property_id)::numeric AS deposits_held,
    (SELECT pr.deposit_release_mode FROM properties pr WHERE pr.id = @property_id)::text AS release_mode;

-- The balance of the two control accounts of the guests as of the date, from the journals.
-- name: LedgerControlAccounts :many
SELECT m.map_key, a.code AS account_code, a.name AS account_name,
       COALESCE((SELECT sum(l.debit - l.credit) FROM gl_journal_lines l JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
                  WHERE l.tenant_id = m.tenant_id AND l.property_id = m.property_id AND l.account_id = m.account_id AND j.journal_date <= @as_of::date), 0)::numeric AS balance
FROM gl_account_map m
JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key IN ('GUEST_LEDGER', 'ADVANCE_DEPOSITS')
ORDER BY m.map_key;

-- One row for each folio that held a deposit at the date or the day before, or moved one on the day. The release (applied) is read on its own: the change of the applied amount of the folio
-- (EVERY_DAY) or the held amount of a folio that closed on the day (ON_CLOSE); the identity opening + received - applied - refunded = closing is then a check and not a definition.
-- name: DepositLedgerFolios :many
WITH cur AS (
    SELECT (t.j->>'folio_id')::bigint AS folio_id, (t.j->>'held')::numeric AS held, (t.j->>'applied')::numeric AS applied, (t.j->>'closed')::boolean AS closed
      FROM (SELECT to_jsonb(fs) AS j FROM folio_deposit_state(@property_id, @as_of::date) fs) t
), prev AS (
    SELECT (t.j->>'folio_id')::bigint AS folio_id, (t.j->>'held')::numeric AS held, (t.j->>'applied')::numeric AS applied, (t.j->>'closed')::boolean AS closed
      FROM (SELECT to_jsonb(fs) AS j FROM folio_deposit_state(@property_id, @prev_date::date) fs) t
), mv AS (
    SELECT g.folio_id,
           COALESCE(-sum(g.signed_amount) FILTER (WHERE g.signed_amount < 0), 0)::numeric AS received,
           COALESCE(sum(g.signed_amount) FILTER (WHERE g.signed_amount > 0), 0)::numeric AS refunded
      FROM folio_item_gl g
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date = @as_of::date AND g.kind = 'PAYMENT' AND g.is_deposit
     GROUP BY g.folio_id
), lv AS (
    SELECT f.id AS folio_id,
           (CASE WHEN prev.folio_id IS NULL THEN 0 WHEN @every_day::boolean THEN prev.held - prev.applied WHEN prev.closed THEN 0 ELSE prev.held END)::numeric AS opening,
           (CASE WHEN cur.folio_id IS NULL THEN 0 WHEN @every_day::boolean THEN cur.held - cur.applied WHEN cur.closed THEN 0 ELSE cur.held END)::numeric AS closing,
           (CASE WHEN @every_day::boolean THEN COALESCE(cur.applied, 0) - COALESCE(prev.applied, 0)
                 WHEN f.closed_on = @as_of::date THEN COALESCE(cur.held, 0) ELSE 0 END)::numeric AS applied
      FROM folios f
      LEFT JOIN cur ON cur.folio_id = f.id
      LEFT JOIN prev ON prev.folio_id = f.id
     WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id
)
SELECT f.id AS folio_id, f.folio_number, f.folio_type, COALESCE(f.reservation_id, 0)::bigint AS reservation_id, (f.stay_id IS NOT NULL)::boolean AS has_stay,
       COALESCE(r.confirmation_number, '')::text AS confirmation_number,
       trim(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, ''))::text AS guest_name,
       COALESCE(co.name, '')::text AS company_name, COALESCE(bg.name, '')::text AS group_name, COALESCE(be.name, '')::text AS event_name,
       COALESCE((SELECT min(l.arrival_date) FROM reservation_rooms l WHERE l.property_id = f.property_id AND l.reservation_id = f.reservation_id), '1900-01-01'::date)::date AS arrival_date,
       lv.opening, COALESCE(mv.received, 0)::numeric AS received, COALESCE(mv.refunded, 0)::numeric AS refunded, lv.closing, lv.applied
FROM folios f
JOIN lv ON lv.folio_id = f.id
LEFT JOIN mv ON mv.folio_id = f.id
LEFT JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN guests gu ON gu.tenant_id = f.tenant_id AND gu.id = r.guest_id
LEFT JOIN companies co ON co.tenant_id = f.tenant_id AND co.id = f.bill_to_company_id
LEFT JOIN booking_groups bg ON bg.tenant_id = f.tenant_id AND bg.id = f.booking_group_id
LEFT JOIN banquet_events be ON be.tenant_id = f.tenant_id AND be.id = f.banquet_event_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id
  AND (lv.closing <> 0 OR lv.opening <> 0 OR COALESCE(mv.received, 0) <> 0 OR COALESCE(mv.refunded, 0) <> 0)
ORDER BY f.folio_number;

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------
-- 7.4 Voids, corrections, discounts and allowances.

-- The ledger items: a reversal (not of a payment: a payment void is read from payments, and not the reversal a POS line void caused: that is a POS_VOID), an adjustment, a charge posted to an
-- allowance account, and a charge with a discount. The POS line of the item, when it has one, gives the bill and the discount's reason.
-- name: CorrectionFolioItems :many
SELECT fi.id, fi.business_date, fi.transaction_at, fi.transaction_type, fi.folio_id, f.folio_number, f.folio_type, COALESCE(f.outlet_id, 0)::bigint AS outlet_id, COALESCE(po.name, '')::text AS outlet_name,
       COALESCE(r.confirmation_number, '')::text AS confirmation_number, COALESCE(pb.bill_number, '')::text AS bill_number, COALESCE(pb.id, 0)::bigint AS bill_id,
       COALESCE(fi.reverses_item_id, 0)::bigint AS original_item_id, orig.business_date AS original_date, COALESCE(cc.code, '')::text AS charge_code, COALESCE(dep.name, '')::text AS department,
       fi.net_amount, fi.service_charge_total, fi.tax_total, (fi.debit - fi.credit)::numeric AS gross, fi.discount_amount,
       COALESCE(fi.reason, '')::text AS reason, COALESCE(pl.discount_reason, '')::text AS discount_reason, fi.description,
       COALESCE(fi.created_by, 0)::bigint AS done_by_id, COALESCE(cu.full_name, '')::text AS done_by,
       COALESCE(fi.approved_by, 0)::bigint AS approved_by_id, COALESCE(au.full_name, '')::text AS approved_by,
       COALESCE(du.full_name, '')::text AS discount_by,
       (al.id IS NOT NULL)::boolean AS is_allowance, COALESCE(fi.revenue_account_code, orig.revenue_account_code, '')::text AS revenue_account_code,
       -- the adjustment of a complimentary line under the treatment DISCOUNT_100 (the POS gives the line away as a 100% discount): the report shows it as a COMP, once
       (fi.transaction_type = 'ADJUSTMENT' AND fi.source = 'POS' AND COALESCE(fi.reason, '') LIKE 'Complimentary: %'
        AND EXISTS (SELECT 1 FROM pos_bills cb JOIN pos_bill_lines cl ON cl.property_id = cb.property_id AND cl.bill_id = cb.id AND cl.is_comp
                     WHERE cb.property_id = fi.property_id AND cb.folio_id = fi.folio_id AND cb.comp_treatment = 'DISCOUNT_100'))::boolean AS is_comp
FROM folio_items fi
JOIN folios f ON f.property_id = fi.property_id AND f.id = fi.folio_id
LEFT JOIN folio_items orig ON orig.property_id = fi.property_id AND orig.id = fi.reverses_item_id
LEFT JOIN charge_codes cc ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
LEFT JOIN departments dep ON dep.tenant_id = fi.tenant_id AND dep.id = fi.department_id
LEFT JOIN pos_outlets po ON po.tenant_id = f.tenant_id AND po.id = f.outlet_id
LEFT JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN pos_bill_lines pl ON pl.property_id = fi.property_id AND pl.folio_item_id = fi.id
LEFT JOIN pos_bills pb ON pb.property_id = pl.property_id AND pb.id = pl.bill_id
LEFT JOIN users cu ON cu.tenant_id = fi.tenant_id AND cu.id = fi.created_by
LEFT JOIN users au ON au.tenant_id = fi.tenant_id AND au.id = fi.approved_by
LEFT JOIN users du ON du.tenant_id = fi.tenant_id AND du.id = pl.discount_by
LEFT JOIN gl_accounts al ON al.property_id = fi.property_id AND al.code = COALESCE(fi.revenue_account_code, orig.revenue_account_code)
                        AND al.account_type = 'REVENUE' AND al.normal_side = 'DEBIT'
WHERE fi.tenant_id = @tenant_id AND fi.property_id = @property_id AND fi.business_date BETWEEN @from_date::date AND @to_date::date
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR f.outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(user_id)::bigint IS NULL OR fi.created_by = sqlc.narg(user_id)::bigint)
  AND NOT EXISTS (SELECT 1 FROM pos_bill_lines v WHERE v.property_id = fi.property_id AND v.voided_item_id = fi.id)
  AND ((fi.transaction_type = 'REVERSAL' AND orig.payment_id IS NULL)
    OR fi.transaction_type = 'ADJUSTMENT'
    OR (fi.transaction_type = 'CHARGE' AND (al.id IS NOT NULL OR fi.discount_amount > 0)))
ORDER BY fi.business_date, fi.id;

-- The payments that were refunded, voided or made in complimentary money, by the business date of the payment.
-- name: CorrectionPayments :many
SELECT p.id, p.payment_number, p.payment_type, p.payment_method, p.status, p.amount, p.business_date, p.paid_at, p.voided_at, COALESCE(p.void_reason, '')::text AS void_reason,
       COALESCE(p.remarks, '')::text AS remarks, p.folio_id, f.folio_number, COALESCE(f.outlet_id, 0)::bigint AS outlet_id, COALESCE(po.name, '')::text AS outlet_name,
       COALESCE(r.confirmation_number, '')::text AS confirmation_number,
       COALESCE(p.created_by, 0)::bigint AS done_by_id, COALESCE(cu.full_name, '')::text AS done_by,
       COALESCE(p.approved_by, 0)::bigint AS approved_by_id, COALESCE(au.full_name, '')::text AS approved_by,
       COALESCE(vu.full_name, '')::text AS voided_by
FROM payments p
JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id
LEFT JOIN pos_outlets po ON po.tenant_id = f.tenant_id AND po.id = f.outlet_id
LEFT JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN users cu ON cu.tenant_id = p.tenant_id AND cu.id = p.created_by
LEFT JOIN users au ON au.tenant_id = p.tenant_id AND au.id = p.approved_by
LEFT JOIN users vu ON vu.tenant_id = p.tenant_id AND vu.id = p.voided_by
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date BETWEEN @from_date::date AND @to_date::date
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR f.outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(user_id)::bigint IS NULL OR p.created_by = sqlc.narg(user_id)::bigint)
  AND ((p.payment_type = 'REFUND' AND p.status = 'POSTED')
    OR p.status = 'VOIDED'
    OR (p.payment_type = 'PAYMENT' AND p.payment_method = 'COMP' AND p.status = 'POSTED'))
ORDER BY p.business_date, p.id;

-- The POS lines voided after they were sent: the ledger entry the void caused (a reversal on the day, an adjustment later) gives the date and the amounts. A line that was still a draft was
-- deleted and never reached the ledger, so it is not here.
-- name: CorrectionPosVoids :many
SELECT l.id AS line_id, l.product_name, l.quantity, COALESCE(l.void_reason, '')::text AS void_reason, COALESCE(l.voided_at, vi.transaction_at) AS voided_at,
       vi.id AS item_id, vi.business_date, vi.net_amount, vi.service_charge_total, vi.tax_total, (vi.debit - vi.credit)::numeric AS gross,
       orig.business_date AS original_date, COALESCE(orig.id, 0)::bigint AS original_item_id,
       b.id AS bill_id, b.bill_number, COALESCE(b.outlet_id, 0)::bigint AS outlet_id, COALESCE(po.name, '')::text AS outlet_name, COALESCE(cc.code, '')::text AS charge_code,
       COALESCE(dep.name, '')::text AS department,
       COALESCE(l.voided_by, 0)::bigint AS done_by_id, COALESCE(vu.full_name, '')::text AS done_by,
       COALESCE(l.void_approved_by, 0)::bigint AS approved_by_id, COALESCE(au.full_name, '')::text AS approved_by
FROM pos_bill_lines l
JOIN folio_items vi ON vi.property_id = l.property_id AND vi.id = l.voided_item_id
LEFT JOIN folio_items orig ON orig.property_id = l.property_id AND orig.id = l.folio_item_id
JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id
LEFT JOIN pos_outlets po ON po.tenant_id = b.tenant_id AND po.id = b.outlet_id
LEFT JOIN charge_codes cc ON cc.property_id = vi.property_id AND cc.id = vi.charge_code_id
LEFT JOIN departments dep ON dep.tenant_id = vi.tenant_id AND dep.id = vi.department_id
LEFT JOIN users vu ON vu.tenant_id = l.tenant_id AND vu.id = l.voided_by
LEFT JOIN users au ON au.tenant_id = l.tenant_id AND au.id = l.void_approved_by
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'VOID' AND vi.business_date BETWEEN @from_date::date AND @to_date::date
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR b.outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(user_id)::bigint IS NULL OR l.voided_by = sqlc.narg(user_id)::bigint)
ORDER BY vi.business_date, l.id;

-- The net revenue of the range as the revenue report adds it up (charges, adjustments and reversals), the base of the share.
-- name: CorrectionNetRevenue :one
SELECT COALESCE(sum(i.net_amount), 0)::numeric AS net
FROM folio_items i
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date::date AND @to_date::date
  AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND i.charge_code_id IS NOT NULL;

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------
-- 7.5 The Z report of all drawers.

-- The shifts that were closed on the day or open at any time of it (the figures of each come from the shift report).
-- name: ZShiftIDs :many
SELECT s.id
FROM cashier_shifts s
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.business_date_opened <= @day::date
  AND (s.status = 'OPEN' OR s.business_date_closed >= @day::date)
ORDER BY s.drawer, s.opened_at, s.id;

-- What was taken on the business date, by method: on a shift, and without one (payments and, for cash, the receipts of the city ledger; refunds are taken off).
-- name: ZTakingsByMethod :many
SELECT method::text AS method,
       COALESCE(sum(CASE WHEN shift_id IS NOT NULL THEN amount ELSE 0 END), 0)::numeric AS on_shift,
       COALESCE(sum(CASE WHEN shift_id IS NULL THEN amount ELSE 0 END), 0)::numeric AS off_shift
FROM (
    SELECT p.payment_method AS method, p.shift_id, CASE WHEN p.payment_type = 'REFUND' THEN -p.amount ELSE p.amount END AS amount
      FROM payments p
     WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date = @day::date AND p.status = 'POSTED'
    UNION ALL
    SELECT r.payment_method, r.shift_id, r.amount
      FROM city_ledger_receipts r
     WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.business_date = @day::date AND r.status = 'POSTED'
) x
GROUP BY method
ORDER BY method;
