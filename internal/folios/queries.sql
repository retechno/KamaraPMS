-- Folios module queries (sqlc): folios, the append-only ledger (folio_items and components) and payments.
-- Scoped by tenant_id and property_id. Only posting.go writes folio_items and their components.

-- name: GetFolio :one
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertFolio :one
INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @folio_number, @reservation_id, sqlc.narg(stay_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- The folio of a bill of an outlet (docs/architecture/34-pos.md): no reservation, stay, group or company; its number is the bill number of the outlet.
-- name: InsertOutletFolio :one
INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, outlet_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @folio_number, 'OUTLET', @outlet_id, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- Every posting bumps the version, so a stale "close" screen is rejected.
-- name: BumpFolio :exec
UPDATE folios SET version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: CloseFolio :one
UPDATE folios f SET status = 'CLOSED', closed_at = @now::timestamptz, closed_by = sqlc.narg(actor_id), version = f.version + 1, updated_by = sqlc.narg(actor_id),
    closed_on = (SELECT b.business_date FROM business_days b WHERE b.property_id = f.property_id AND b.status = 'OPEN')
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.id = @id
RETURNING *;

-- The reservation's open folio that is not linked to a stay yet (deposits go here).
-- name: FindUnlinkedOpenFolio :one
SELECT id FROM folios
WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_id = @reservation_id AND stay_id IS NULL AND status = 'OPEN';

-- The folios of a property in the order asked for: by number, balance or status, either way; the default (key '') is the open ones first, by room number among them (a folio without a room,
-- as the bill of a group, after the rooms), then the closed ones in the same way. One query for every order, the key and the direction being parameters (so no index serves the order, the filters are what narrow the rows). The page goes on after (the sort value, id), the id
-- breaking a tie in the direction of the order. NULL does not occur: every folio has a number, a status and a balance.
--
-- The room is the last room of the stay of the folio and the guest is the one of the stay (or of the reservation). The room number of the default order is padded to 12 characters so that 99 comes before
-- 101 under a collation that does not read numbers; the text sort key carries the whole order, so the cursor of the default order is a text.
--
-- The balance is SUM(debit) - SUM(credit) of the folio's items, and it is read for the OPEN folios only: a folio can be closed only with a zero balance (FOLIO_BALANCE_NOT_ZERO, and no
-- posting reaches a closed folio: FOLIO_CLOSED), so a closed folio is 0 without a look at its items. A test holds that to be true after every way a folio is closed.
-- name: ListFoliosOrdered :many
SELECT f.*, bal.balance::numeric AS balance,
       COALESCE((SELECT r.confirmation_number FROM reservations r WHERE r.property_id = f.property_id AND r.id = f.reservation_id), '')::text AS confirmation_number,
       COALESCE((SELECT g.code FROM booking_groups g WHERE g.property_id = f.property_id AND g.id = f.booking_group_id), '')::text AS group_code,
       COALESCE((SELECT g.name FROM booking_groups g WHERE g.property_id = f.property_id AND g.id = f.booking_group_id), '')::text AS group_name,
       COALESCE((SELECT o.code FROM pos_outlets o WHERE o.property_id = f.property_id AND o.id = f.outlet_id), '')::text AS outlet_code,
       COALESCE((SELECT o.name FROM pos_outlets o WHERE o.property_id = f.property_id AND o.id = f.outlet_id), '')::text AS outlet_name,
       COALESCE(cr.room_number, '')::text AS room_number,
       COALESCE(NULLIF(trim(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, '')), ''), '')::text AS guest_name,
       COALESCE(k.sk_text, '')::text AS sort_text
FROM folios f
LEFT JOIN reservations res ON res.property_id = f.property_id AND res.id = f.reservation_id
LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id
                    WHERE sr.property_id = f.property_id AND sr.stay_id = f.stay_id ORDER BY sr.id DESC LIMIT 1) cr(room_number) ON true
LEFT JOIN guests gu ON gu.tenant_id = f.tenant_id AND gu.id = COALESCE(st.guest_id, res.guest_id)
CROSS JOIN LATERAL (
    SELECT CASE WHEN f.status = 'OPEN'
                THEN (SELECT COALESCE(sum(i.debit), 0) - COALESCE(sum(i.credit), 0) FROM folio_items i WHERE i.property_id = f.property_id AND i.folio_id = f.id)
                ELSE 0 END AS balance
) bal
CROSS JOIN LATERAL (
    SELECT (CASE @sort_key::text
                WHEN 'folio_number' THEN f.folio_number::text COLLATE "und-x-icu"
                WHEN 'status' THEN f.status::text COLLATE "und-x-icu"
                WHEN '' THEN ((CASE WHEN f.status = 'OPEN' THEN '0' ELSE '1' END)
                              || (CASE WHEN COALESCE(cr.room_number, '') = '' THEN 'zzzzzzzzzzzz' ELSE lpad(cr.room_number, 12, '0') END))::text COLLATE "und-x-icu"
            END) AS sk_text,
           (CASE @sort_key::text
                WHEN 'balance' THEN bal.balance
            END) AS sk_num
) k
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id
  AND (sqlc.narg(reservation_id)::bigint IS NULL OR f.reservation_id = sqlc.narg(reservation_id)::bigint)
  AND (sqlc.narg(stay_id)::bigint IS NULL OR f.stay_id = sqlc.narg(stay_id)::bigint)
  AND (sqlc.narg(booking_group_id)::bigint IS NULL OR f.booking_group_id = sqlc.narg(booking_group_id)::bigint)
  AND (sqlc.narg(folio_type)::text IS NULL OR f.folio_type = sqlc.narg(folio_type)::text)
  -- the bills of the outlets are listed only when asked for (folio_type = OUTLET): every other list of the front desk keeps its meaning (34-pos.md 11)
  AND (sqlc.narg(folio_type)::text IS NOT NULL OR f.folio_type NOT IN ('OUTLET', 'EVENT'))
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR f.outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR f.status = sqlc.narg(status)::text)
  -- the search of the cashier: the number of the folio or of the reservation, the room, or a part of the name of the guest or of the group
  AND (sqlc.narg(q)::text IS NULL
       OR f.folio_number ILIKE sqlc.narg(q)::text || '%'
       OR res.confirmation_number ILIKE sqlc.narg(q)::text || '%'
       OR cr.room_number ILIKE sqlc.narg(q)::text || '%'
       OR lower(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, '')) LIKE '%' || lower(sqlc.narg(q)::text) || '%'
       OR EXISTS (SELECT 1 FROM booking_groups bg WHERE bg.property_id = f.property_id AND bg.id = f.booking_group_id
                  AND lower(bg.code || ' ' || bg.name) LIKE '%' || lower(sqlc.narg(q)::text) || '%'))
  -- the follow-up of a cancellation: the open folio of a reservation that has no live room and no stay, that still holds something (a deposit that was kept, a fee that was not paid)
  AND (NOT @cancelled_with_balance::boolean OR (f.status = 'OPEN' AND f.stay_id IS NULL AND f.reservation_id IS NOT NULL AND bal.balance <> 0
       AND NOT EXISTS (SELECT 1 FROM reservation_rooms l WHERE l.property_id = f.property_id AND l.reservation_id = f.reservation_id AND l.status NOT IN ('CANCELLED', 'NO_SHOW'))))
  AND (sqlc.narg(after_id)::bigint IS NULL
       OR CASE WHEN NOT @sort_desc::boolean THEN
               (k.sk_text > sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num > sqlc.narg(after_num)::numeric
                OR ((k.sk_text = sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num = sqlc.narg(after_num)::numeric) AND f.id > sqlc.narg(after_id)::bigint))
           ELSE
               (k.sk_text < sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num < sqlc.narg(after_num)::numeric
                OR ((k.sk_text = sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num = sqlc.narg(after_num)::numeric) AND f.id < sqlc.narg(after_id)::bigint))
           END)
ORDER BY (CASE WHEN NOT @sort_desc::boolean THEN k.sk_text END) ASC, (CASE WHEN NOT @sort_desc::boolean THEN k.sk_num END) ASC,
         (CASE WHEN @sort_desc::boolean THEN k.sk_text END) DESC, (CASE WHEN @sort_desc::boolean THEN k.sk_num END) DESC,
         (CASE WHEN NOT @sort_desc::boolean THEN f.id END) ASC, (CASE WHEN @sort_desc::boolean THEN f.id END) DESC
LIMIT @row_limit;

-- The number of a folio, for a list of payments that names it (a plain read, no lock).
-- name: GetFolioNumber :one
SELECT folio_number FROM folios WHERE property_id = @property_id AND id = @id;

-- name: FolioTotals :one
SELECT COALESCE(sum(debit), 0)::numeric AS debit, COALESCE(sum(credit), 0)::numeric AS credit
FROM folio_items WHERE property_id = @property_id AND folio_id = @folio_id;

-- name: GetStayStatus :one
SELECT status FROM stays WHERE property_id = @property_id AND id = @id;

-- name: GetReservationStatus :one
SELECT status FROM reservations WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------
-- Ledger items

-- name: InsertFolioItem :one
INSERT INTO folio_items (
    tenant_id, property_id, folio_id, business_date, transaction_at, service_date, transaction_type, charge_code_id, payment_id,
    reverses_item_id, stay_id, stay_room_id, reference_type, reference_id, description, quantity, unit_price, price_mode,
    base_amount, discount_amount, net_amount, rounding_adjustment, service_charge_total, tax_total, debit, credit,
    source, reason, idempotency_key, created_by, approved_by, revenue_account_code, department_id
) VALUES (
    @tenant_id, @property_id, @folio_id, @business_date, @transaction_at, @service_date, @transaction_type, sqlc.narg(charge_code_id), sqlc.narg(payment_id),
    sqlc.narg(reverses_item_id), sqlc.narg(stay_id), sqlc.narg(stay_room_id), sqlc.narg(reference_type), sqlc.narg(reference_id), @description, @quantity, @unit_price, @price_mode,
    @base_amount, @discount_amount, @net_amount, @rounding_adjustment, @service_charge_total, @tax_total, @debit, @credit,
    @source, sqlc.narg(reason), sqlc.narg(idempotency_key), sqlc.narg(actor_id), sqlc.narg(approved_by),
    -- The revenue account in force now; a reversal copies the account of the item it reverses, and so does the copy a transfer charges on the target folio (copies_item_id).
    -- The transfer of a balance names the account of the system key FOLIO_TRANSFER in force now (revenue_account_override); the code TRF has none of its own.
    COALESCE(sqlc.narg(revenue_account_override)::varchar,
    CASE WHEN COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint) IS NOT NULL
         THEN (SELECT o.revenue_account_code FROM folio_items o WHERE o.property_id = @property_id AND o.id = COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint))
         ELSE (SELECT c.gl_account_code FROM charge_codes c WHERE c.property_id = @property_id AND c.id = sqlc.narg(charge_code_id)::bigint)
    END),
    -- The department in force now: the default of the charge code, else the default of its revenue account, and none when the account takes none;
    -- a reversal copies the department of the item it reverses; a sale of an outlet names the sub-department of the outlet (department_override), which wins over the code's.
    COALESCE(sqlc.narg(department_override)::bigint,
    CASE WHEN COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint) IS NOT NULL
         THEN (SELECT o.department_id FROM folio_items o WHERE o.property_id = @property_id AND o.id = COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint))
         ELSE (SELECT CASE WHEN a.department_requirement = 'NONE' THEN NULL ELSE COALESCE(c.department_id, a.default_department_id) END
                 FROM charge_codes c LEFT JOIN gl_accounts a ON a.property_id = c.property_id AND a.code = c.gl_account_code
                WHERE c.property_id = @property_id AND c.id = sqlc.narg(charge_code_id)::bigint)
    END)
)
RETURNING *;

-- The department rule of the revenue account of a charge code, to refuse a charge that would be posted without a department the account requires.
-- name: ChargeCodeDepartmentRule :one
SELECT c.code AS charge_code, c.gl_account_code, COALESCE(a.name, '')::text AS account_name, COALESCE(a.department_requirement, 'OPTIONAL')::text AS requirement,
       (c.department_id IS NOT NULL OR (a.default_department_id IS NOT NULL AND dd.is_active))::boolean AS has_department
FROM charge_codes c
LEFT JOIN gl_accounts a ON a.property_id = c.property_id AND a.code = c.gl_account_code
LEFT JOIN departments dd ON dd.property_id = a.property_id AND dd.id = a.default_department_id
WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.id = @id;

-- name: InsertFolioItemComponent :exec
INSERT INTO folio_item_components (
    tenant_id, property_id, folio_item_id, component_type, tax_id, service_charge_id, code, name, rate, tax_on_service,
    base_amount, amount, sequence, gl_account_code
) VALUES (
    @tenant_id, @property_id, @folio_item_id, @component_type::varchar, sqlc.narg(tax_id), sqlc.narg(service_charge_id), @code, @name, @rate,
    sqlc.narg(tax_on_service), @base_amount, @amount, @sequence,
    -- The account in force now; a component of a reversal copies the account of the component it reverses.
    CASE WHEN (SELECT ni.reverses_item_id FROM folio_items ni WHERE ni.property_id = @property_id AND ni.id = @folio_item_id) IS NOT NULL
         THEN (SELECT oc.gl_account_code FROM folio_items ni
               JOIN folio_item_components oc ON oc.property_id = ni.property_id AND oc.folio_item_id = ni.reverses_item_id
                AND oc.component_type = @component_type::varchar
                AND oc.tax_id IS NOT DISTINCT FROM sqlc.narg(tax_id)::bigint
                AND oc.service_charge_id IS NOT DISTINCT FROM sqlc.narg(service_charge_id)::bigint
               WHERE ni.property_id = @property_id AND ni.id = @folio_item_id)
         WHEN @component_type::varchar = 'TAX'
         THEN (SELECT t.gl_account_code FROM taxes t WHERE t.property_id = @property_id AND t.id = sqlc.narg(tax_id)::bigint)
         ELSE (SELECT sc.gl_account_code FROM service_charges sc WHERE sc.property_id = @property_id AND sc.id = sqlc.narg(service_charge_id)::bigint)
    END
);

-- name: GetFolioItem :one
SELECT * FROM folio_items WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetFolioItemByKey :one
SELECT * FROM folio_items WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: GetItemOfPayment :one
SELECT * FROM folio_items WHERE tenant_id = @tenant_id AND property_id = @property_id AND payment_id = @payment_id;

-- name: ListItemComponents :many
SELECT * FROM folio_item_components
WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_item_id = ANY(@item_ids::bigint[])
ORDER BY folio_item_id, component_type, sequence;

-- Items of a folio in posting order, with what a screen needs: the charge code, the reversal that undid the item
-- and the room of the stay segment.
-- name: ListFolioItems :many
SELECT i.*, c.code AS charge_code, rv.id AS reversed_by_item_id,
       COALESCE(r.room_number, lr.room_number, '')::text AS room_number,
       -- on the master folio of a group a line says whose it is: the guest of its stay (and the room of the stay when the line has no room segment of its own)
       CASE WHEN fo.folio_type = 'MASTER' THEN COALESCE(NULLIF(trim(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, '')), ''), '') ELSE '' END::text AS guest_name,
       COALESCE(g.group_code, 'A')::text AS group_code -- a reversal is printed with the line it reverses; a line without a row is in group A
FROM folio_items i
JOIN folios fo ON fo.property_id = i.property_id AND fo.id = i.folio_id
LEFT JOIN stays st ON st.property_id = i.property_id AND st.id = i.stay_id AND fo.folio_type = 'MASTER'
LEFT JOIN guests gu ON gu.tenant_id = st.tenant_id AND gu.id = st.guest_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms s2 JOIN rooms rm ON rm.property_id = s2.property_id AND rm.id = s2.room_id
                    WHERE s2.property_id = st.property_id AND s2.stay_id = st.id ORDER BY s2.id DESC LIMIT 1) lr ON true
LEFT JOIN folio_item_groups g ON g.property_id = i.property_id AND g.folio_item_id = COALESCE(i.reverses_item_id, i.id)
LEFT JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
LEFT JOIN folio_items rv ON rv.property_id = i.property_id AND rv.reverses_item_id = i.id
LEFT JOIN stay_rooms sr ON sr.property_id = i.property_id AND sr.id = i.stay_room_id
LEFT JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.folio_id = @folio_id
ORDER BY i.transaction_at, i.id;

-- name: GetReversalOf :one
SELECT id FROM folio_items WHERE property_id = @property_id AND reverses_item_id = @item_id;

-- The posting register row of a room night item (POSTED), for a transfer that has to keep the night posted.
-- name: GetPostingOfItem :one
SELECT stay_id, stay_room_id, service_date, charge_code_id FROM stay_charge_postings
WHERE property_id = @property_id AND folio_item_id = @item_id AND status = 'POSTED' AND charge_source = 'ROOM_NIGHT';

-- The register row of the item a transfer charged again: the night stays posted once, for the stay that earned it.
-- name: InsertTransferPosting :exec
INSERT INTO stay_charge_postings (tenant_id, property_id, stay_id, stay_room_id, service_date, charge_source, charge_code_id, folio_item_id, business_date, posting_trigger, created_by)
VALUES (@tenant_id, @property_id, @stay_id, @stay_room_id, @service_date, 'ROOM_NIGHT', @charge_code_id, @folio_item_id, @business_date, 'MANUAL', sqlc.narg(actor_id));

-- A reversed room-charge item flips its posting register row (POSTED -> REVERSED); other items have none.
-- name: FlipPostingRegister :exec
UPDATE stay_charge_postings SET status = 'REVERSED', reversal_item_id = @reversal_item_id
WHERE property_id = @property_id AND folio_item_id = @item_id AND status = 'POSTED';

-- ---------------------------------------------------------------------------
-- Payments

-- name: InsertPayment :one
INSERT INTO payments (
    tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, paid_at, business_date,
    reference_number, refund_of_payment_id, idempotency_key, remarks, created_by, approved_by, company_id, shift_id, mdr_rate, mdr_fee, expected_settlement_date, mdr_vat_rate, mdr_vat
) VALUES (
    @tenant_id, @property_id, @payment_number, @folio_id, @payment_type, @payment_method, @amount, @paid_at, @business_date,
    sqlc.narg(reference_number), sqlc.narg(refund_of_payment_id), sqlc.narg(idempotency_key), sqlc.narg(remarks), sqlc.narg(actor_id), sqlc.narg(approved_by), sqlc.narg(company_id), sqlc.narg(shift_id), sqlc.narg(mdr_rate), sqlc.narg(mdr_fee), sqlc.narg(expected_settlement_date), sqlc.narg(mdr_vat_rate), sqlc.narg(mdr_vat)
)
RETURNING *;
-- The rate that applies to a card or e-wallet payment of a business date: the latest rule that has started.
-- name: CardFeeRule :one
SELECT mdr_rate, vat_rate, settlement_days FROM card_fee_rules
WHERE tenant_id = @tenant_id AND property_id = @property_id AND payment_method = @payment_method AND effective_from <= @on_date::date
ORDER BY effective_from DESC
LIMIT 1;


-- name: GetPayment :one
SELECT * FROM payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetPaymentByKey :one
SELECT * FROM payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: VoidPayment :one
UPDATE payments SET status = 'VOIDED', voided_at = @now::timestamptz, voided_by = sqlc.narg(actor_id), void_reason = @reason, approved_by = @approved_by
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Refunds of a payment that were not voided (REFUND payments cannot be voided, so all of them count).
-- name: SumRefundsOf :one
SELECT COALESCE(sum(amount), 0)::numeric AS refunded, count(*)::int AS refund_count
FROM payments WHERE property_id = @property_id AND refund_of_payment_id = @payment_id AND status = 'POSTED';

-- The cashier list, newest first: by the time the payment was posted (paid_at), the id breaking a tie. The page goes on after (paid_at, id); the index payments_paid_at_idx serves it.
-- name: ListPayments :many
SELECT * FROM payments
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(after_id)::bigint IS NULL OR (paid_at, id) < (sqlc.narg(after_time)::timestamptz, sqlc.narg(after_id)::bigint))
  AND (sqlc.narg(business_date)::date IS NULL OR business_date = sqlc.narg(business_date)::date)
  AND (sqlc.narg(payment_method)::text IS NULL OR payment_method = sqlc.narg(payment_method)::text)
ORDER BY paid_at DESC, id DESC
LIMIT @row_limit;

-- The same list in another order: by number, amount, method, status or time, either way (every one of these columns has a value). One query for every order, the key and the direction being
-- parameters, so it cannot use an index; the default (newest first) has the query above. The page goes on after (the sort value, id), the id breaking a tie in the direction of the order.
-- name: ListPaymentsOrdered :many
SELECT p.*
FROM payments p
CROSS JOIN LATERAL (
    SELECT (CASE @sort_key::text
                WHEN 'payment_number' THEN p.payment_number::text COLLATE "und-x-icu"
                WHEN 'payment_method' THEN p.payment_method::text COLLATE "und-x-icu"
                WHEN 'status' THEN p.status::text COLLATE "und-x-icu"
            END) AS sk_text,
           (CASE @sort_key::text WHEN 'amount' THEN p.amount END) AS sk_num,
           (CASE @sort_key::text WHEN 'paid_at' THEN p.paid_at END) AS sk_time
) k
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id
  AND (sqlc.narg(business_date)::date IS NULL OR p.business_date = sqlc.narg(business_date)::date)
  AND (sqlc.narg(payment_method)::text IS NULL OR p.payment_method = sqlc.narg(payment_method)::text)
  AND (sqlc.narg(after_id)::bigint IS NULL
       OR CASE WHEN NOT @sort_desc::boolean THEN
               (k.sk_text > sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num > sqlc.narg(after_num)::numeric OR k.sk_time > sqlc.narg(after_time)::timestamptz
                OR ((k.sk_text = sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num = sqlc.narg(after_num)::numeric OR k.sk_time = sqlc.narg(after_time)::timestamptz)
                    AND p.id > sqlc.narg(after_id)::bigint))
           ELSE
               (k.sk_text < sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num < sqlc.narg(after_num)::numeric OR k.sk_time < sqlc.narg(after_time)::timestamptz
                OR ((k.sk_text = sqlc.narg(after_text)::text COLLATE "und-x-icu" OR k.sk_num = sqlc.narg(after_num)::numeric OR k.sk_time = sqlc.narg(after_time)::timestamptz)
                    AND p.id < sqlc.narg(after_id)::bigint))
           END)
ORDER BY (CASE WHEN NOT @sort_desc::boolean THEN k.sk_text END) ASC, (CASE WHEN NOT @sort_desc::boolean THEN k.sk_num END) ASC, (CASE WHEN NOT @sort_desc::boolean THEN k.sk_time END) ASC,
         (CASE WHEN @sort_desc::boolean THEN k.sk_text END) DESC, (CASE WHEN @sort_desc::boolean THEN k.sk_num END) DESC, (CASE WHEN @sort_desc::boolean THEN k.sk_time END) DESC,
         (CASE WHEN NOT @sort_desc::boolean THEN p.id END) ASC, (CASE WHEN @sort_desc::boolean THEN p.id END) DESC
LIMIT @row_limit;

-- Net cash by method for a business date: payments minus refunds, voided payments excluded.
-- name: PaymentTotals :many
SELECT payment_method,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'PAYMENT'), 0)::numeric AS paid,
       COALESCE(sum(amount) FILTER (WHERE payment_type = 'REFUND'), 0)::numeric AS refunded
FROM payments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @business_date AND status = 'POSTED'
GROUP BY payment_method
ORDER BY payment_method;

-- ---------------------------------------------------------------------------
-- Stay folios (called by check-in and reverse check-in)

-- name: LinkFolioToStay :one
UPDATE folios SET stay_id = @stay_id, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: UnlinkFolioFromStay :one
UPDATE folios SET stay_id = NULL, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: GetFolioOfStay :one
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id AND folio_type = 'GUEST';

-- The OPEN guest folio of each of the stays: the default target of a charge (see ResolveTarget).
-- name: ListStayGuestFolios :many
SELECT id, stay_id FROM folios
WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = ANY(@stay_ids::bigint[]) AND status = 'OPEN' AND folio_type = 'GUEST';

-- name: GetCompanyName :one
SELECT name FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The CHARGE items on every folio of a stay (a check-in cannot be reversed once a night is charged, whatever the folio).
-- name: CountStayChargeItems :one
SELECT count(*)::int FROM folio_items i JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id
WHERE f.property_id = @property_id AND f.stay_id = @stay_id AND i.transaction_type = 'CHARGE';

-- name: CountChargeItems :one
SELECT count(*)::int FROM folio_items
WHERE property_id = @property_id AND folio_id = @folio_id AND transaction_type = 'CHARGE';

-- Every folio of a stay (the guest folio first, then the companies).
-- name: ListStayFolios :many
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id ORDER BY (folio_type = 'GUEST') DESC, id;

-- name: ListStayOpenFolios :many
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id AND status = 'OPEN' ORDER BY id;

-- What a charge code has posted on a folio: the net of its charges, adjustments and reversals (a reversed charge nets out).
-- An adjustment corrects this, so it needs it to be above zero and, for a credit, to stay within it.
-- name: SumNetOfChargeCodeOnFolio :one
SELECT COALESCE(sum(net_amount), 0)::numeric AS net, count(*)::int AS items
FROM folio_items
WHERE property_id = @property_id AND folio_id = @folio_id AND charge_code_id = @charge_code_id
  AND transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL');

-- ---------------------------------------------------------------------------
-- Billing instructions and company folios (docs/architecture/18-architecture-decisions.md section 3)

-- name: ListLineInstructions :many
SELECT i.id, i.scope, i.charge_code_id, i.company_id, c.name AS company_name, cc.code AS charge_code, cc.name AS charge_code_name
FROM folio_billing_instructions i
JOIN companies c ON c.property_id = i.property_id AND c.id = i.company_id
LEFT JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.reservation_room_id = @reservation_room_id
ORDER BY (i.scope = 'ALL') DESC, (i.scope = 'ROOM') DESC, i.id;

-- name: DeleteLineInstructions :exec
DELETE FROM folio_billing_instructions WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_room_id = @reservation_room_id;

-- name: InsertInstruction :exec
INSERT INTO folio_billing_instructions (tenant_id, property_id, reservation_room_id, scope, charge_code_id, company_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @reservation_room_id, @scope, sqlc.narg(charge_code_id), @company_id, sqlc.narg(actor_id), sqlc.narg(actor_id));

-- The instructions of the lines of many stays (the resolver).
-- name: ListStayInstructions :many
SELECT s.id AS stay_id, i.scope, i.charge_code_id, i.company_id
FROM stays s
JOIN folio_billing_instructions i ON i.property_id = s.property_id AND i.reservation_room_id = s.reservation_room_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = ANY(@stay_ids::bigint[]);

-- The OPEN company folios of many stays.
-- name: ListStayOpenCompanyFolios :many
SELECT id, stay_id, bill_to_company_id FROM folios
WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = ANY(@stay_ids::bigint[]) AND status = 'OPEN' AND folio_type = 'COMPANY';

-- name: InsertCompanyFolio :one
INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, folio_type, bill_to_company_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @folio_number, @reservation_id, @stay_id, 'COMPANY', @company_id, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- The stay of a reservation line, if the guest has checked in.
-- name: GetLineStay :one
SELECT id, status FROM stays WHERE tenant_id = @tenant_id AND property_id = @property_id AND reservation_room_id = @reservation_room_id;

-- The line and its reservation (a line of another reservation or property is not found).
-- name: GetInstructionLine :one
SELECT rr.id, rr.status FROM reservation_rooms rr
WHERE rr.tenant_id = @tenant_id AND rr.property_id = @property_id AND rr.reservation_id = @reservation_id AND rr.id = @id;

-- ---------------------------------------------------------------------------
-- Transaction groups (docs/architecture/20-transaction-group.md): a presentation dimension inside one folio. Nothing here reads or writes an amount.

-- The group a ledger line is printed under. A reversal follows the line it reverses; a line with no row is in group A.
-- name: GetItemGroup :one
SELECT COALESCE(g.group_code, 'A')::text AS group_code
FROM folio_items i
LEFT JOIN folio_item_groups g ON g.property_id = i.property_id AND g.folio_item_id = COALESCE(i.reverses_item_id, i.id)
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = @item_id;

-- name: SetItemGroup :exec
INSERT INTO folio_item_groups (tenant_id, property_id, folio_item_id, group_code, updated_by)
VALUES (@tenant_id, @property_id, @folio_item_id, @group_code, sqlc.narg(actor_id))
ON CONFLICT (property_id, folio_item_id) DO UPDATE SET group_code = EXCLUDED.group_code, updated_by = EXCLUDED.updated_by;

-- The group of the ledger line of a payment or refund (one line per payment).
-- name: GetPaymentGroup :one
SELECT COALESCE(g.group_code, 'A')::text AS group_code
FROM folio_items i
LEFT JOIN folio_item_groups g ON g.property_id = i.property_id AND g.folio_item_id = i.id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.payment_id = @payment_id;

-- ---------------------------------------------------------------------------
-- Cancellation and no-show fees (audit F-08): the facts of the reservation that identify the event a fee is charged for.

-- name: GetReservationForFee :one
SELECT status, cancelled_at, confirmation_number FROM reservations WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetLineForFee :one
SELECT status, no_show_at, reservation_id FROM reservation_rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetChargeCodeByCode :one
SELECT id, is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;


-- ---------------------------------------------------------------------------------------------------------------
-- Transfers between folios (docs/architecture/30-group-folio.md, part B)

-- The code TRF and the account of the system key FOLIO_TRANSFER.
-- name: GetTransferChargeCode :one
SELECT id, is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = 'TRF' AND charge_type = 'TRANSFER';

-- name: GetTransferAccount :one
SELECT a.code, a.is_active, a.is_postable, a.account_type
FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = 'FOLIO_TRANSFER';

-- The charge type of a charge code (a TRANSFER line is moved back by a return, never by a transfer of charges, a reversal or an adjustment).
-- name: GetChargeTypeOfCode :one
SELECT charge_type FROM charge_codes WHERE property_id = @property_id AND id = @id;

-- name: ListItemsByIDs :many
SELECT * FROM folio_items WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]) ORDER BY id;

-- The charges of a code on a folio that can still be moved: a CHARGE that nothing has reversed, in posting order.
-- name: ListMovableItemsOfCode :many
SELECT i.* FROM folio_items i
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.folio_id = @folio_id AND i.charge_code_id = @charge_code_id AND i.transaction_type = 'CHARGE'
  AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = i.property_id AND rv.reverses_item_id = i.id)
ORDER BY i.id
LIMIT @row_limit;

-- name: ListReservationGroups :many
SELECT id, booking_group_id FROM reservations WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: InsertTransferBatch :one
INSERT INTO folio_transfer_batches (tenant_id, property_id, kind, is_return, from_folio_id, to_folio_id, scope, reason, business_date, approved_by, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @kind, @is_return, @from_folio_id, @to_folio_id, @scope, @reason, @business_date, sqlc.narg(approved_by), sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING *;

-- name: InsertTransfer :one
INSERT INTO folio_transfers (tenant_id, property_id, batch_id, from_folio_id, to_folio_id, out_item_id, in_item_id, original_item_id, amount, origin_folio_id, previous_folio_id, returns_transfer_id)
VALUES (@tenant_id, @property_id, @batch_id, @from_folio_id, @to_folio_id, @out_item_id, @in_item_id, sqlc.narg(original_item_id), @amount, @origin_folio_id, @previous_folio_id, sqlc.narg(returns_transfer_id))
RETURNING *;

-- name: GetTransferByInItem :one
SELECT * FROM folio_transfers WHERE property_id = @property_id AND in_item_id = @item_id;

-- name: GetTransferByOriginalItem :one
SELECT * FROM folio_transfers WHERE property_id = @property_id AND original_item_id = @item_id;

-- name: GetTransfer :one
SELECT * FROM folio_transfers WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetTransferReturnedBy :one
SELECT id FROM folio_transfers WHERE property_id = @property_id AND returns_transfer_id = @transfer_id;

-- name: GetTransferBatch :one
SELECT * FROM folio_transfer_batches WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetTransferBatchByKey :one
SELECT * FROM folio_transfer_batches WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @key;

-- name: ListBatchTransfers :many
SELECT * FROM folio_transfers WHERE property_id = @property_id AND batch_id = @batch_id ORDER BY id;

-- The log of a folio, with what a screen shows: the folios on both ends, the first origin and the folio before, the room of each, the line, who and when. direction: out, in or all.
-- name: ListFolioTransfers :many
SELECT t.id, t.batch_id, b.kind, b.is_return, b.scope, b.reason, b.business_date, b.approved_by, b.created_by, b.created_at,
       t.from_folio_id, ff.folio_number AS from_number, t.to_folio_id, tf.folio_number AS to_number,
       t.origin_folio_id, ofo.folio_number AS origin_number, t.previous_folio_id, pf.folio_number AS previous_number,
       t.out_item_id, t.in_item_id, t.original_item_id, t.amount, t.returns_transfer_id,
       COALESCE(oi.description, ii.description)::text AS description, COALESCE(oc.code, ic.code)::text AS charge_code,
       rt.id AS returned_by_transfer_id, nx.id AS moved_on_transfer_id, COALESCE(fr.room_number, '')::text AS from_room, COALESCE(tr.room_number, '')::text AS to_room
FROM folio_transfers t
JOIN folio_transfer_batches b ON b.property_id = t.property_id AND b.id = t.batch_id
JOIN folios ff ON ff.property_id = t.property_id AND ff.id = t.from_folio_id
JOIN folios tf ON tf.property_id = t.property_id AND tf.id = t.to_folio_id
JOIN folios ofo ON ofo.property_id = t.property_id AND ofo.id = t.origin_folio_id
JOIN folios pf ON pf.property_id = t.property_id AND pf.id = t.previous_folio_id
JOIN folio_items ii ON ii.property_id = t.property_id AND ii.id = t.in_item_id
LEFT JOIN charge_codes ic ON ic.property_id = ii.property_id AND ic.id = ii.charge_code_id
LEFT JOIN folio_items oi ON oi.property_id = t.property_id AND oi.id = t.original_item_id
LEFT JOIN charge_codes oc ON oc.property_id = oi.property_id AND oc.id = oi.charge_code_id
LEFT JOIN folio_transfers rt ON rt.property_id = t.property_id AND rt.returns_transfer_id = t.id
LEFT JOIN folio_transfers nx ON nx.property_id = t.property_id AND nx.original_item_id = t.in_item_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id WHERE sr.property_id = ff.property_id AND sr.stay_id = ff.stay_id ORDER BY sr.id DESC LIMIT 1) fr(room_number) ON true
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id WHERE sr.property_id = tf.property_id AND sr.stay_id = tf.stay_id ORDER BY sr.id DESC LIMIT 1) tr(room_number) ON true
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
  AND ((@direction::text IN ('out', 'all') AND t.from_folio_id = @folio_id) OR (@direction::text IN ('in', 'all') AND t.to_folio_id = @folio_id))
ORDER BY t.id DESC
LIMIT @row_limit;

-- The log rows that touch some lines of a folio, for the folio view: a line that came in, the line a transfer reversed, and its reversal.
-- name: ListTransfersOfItems :many
SELECT t.id, b.kind, b.is_return, t.from_folio_id, ff.folio_number AS from_number, t.to_folio_id, tf.folio_number AS to_number, t.origin_folio_id, ofo.folio_number AS origin_number,
       t.previous_folio_id, t.out_item_id, t.in_item_id, t.original_item_id,
       COALESCE(fr.room_number, '')::text AS from_room, COALESCE(tr.room_number, '')::text AS to_room, rt.id AS returned_by_transfer_id
FROM folio_transfers t
JOIN folio_transfer_batches b ON b.property_id = t.property_id AND b.id = t.batch_id
JOIN folios ff ON ff.property_id = t.property_id AND ff.id = t.from_folio_id
JOIN folios tf ON tf.property_id = t.property_id AND tf.id = t.to_folio_id
JOIN folios ofo ON ofo.property_id = t.property_id AND ofo.id = t.origin_folio_id
LEFT JOIN folio_transfers rt ON rt.property_id = t.property_id AND rt.returns_transfer_id = t.id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id WHERE sr.property_id = ff.property_id AND sr.stay_id = ff.stay_id ORDER BY sr.id DESC LIMIT 1) fr(room_number) ON true
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id WHERE sr.property_id = tf.property_id AND sr.stay_id = tf.stay_id ORDER BY sr.id DESC LIMIT 1) tr(room_number) ON true
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
  AND (t.in_item_id = ANY(@item_ids::bigint[]) OR t.out_item_id = ANY(@item_ids::bigint[]) OR t.original_item_id = ANY(@item_ids::bigint[]));

-- The folios a charge or a balance may be sent to, for the choice in the transfer dialog: open folios of a stay, the folios of the same reservation and of the same group first.
-- include_any adds every other open folio of the property (only for who holds folio.transfer_any). q matches the folio number, the room or the name of the guest.
-- name: SearchTransferTargets :many
-- The open folios a charge can go to from a folio: those of the same reservation or group, the master folio of the group, and (include_any) any other. A master has no stay and no reservation.
SELECT f.id, f.folio_number, f.folio_type, f.reservation_id, f.stay_id, COALESCE(res.confirmation_number, '')::text AS confirmation_number, res.booking_group_id,
       COALESCE((SELECT sum(i.debit - i.credit) FROM folio_items i WHERE i.property_id = f.property_id AND i.folio_id = f.id), 0)::numeric AS balance,
       COALESCE(cr.room_number, '')::text AS room_number,
       g.first_name, g.last_name, COALESCE(comp.name, '')::text AS company_name, COALESCE(bg.name, '')::text AS group_name,
       COALESCE(sqlc.narg(from_reservation_id)::bigint IS NOT NULL AND f.reservation_id = sqlc.narg(from_reservation_id)::bigint, false)::boolean AS same_reservation,
       COALESCE(sqlc.narg(from_group_id)::bigint IS NOT NULL AND f.folio_type <> 'MASTER' AND res.booking_group_id = sqlc.narg(from_group_id)::bigint AND NOT @from_is_master::boolean, false)::boolean AS same_group,
       COALESCE(sqlc.narg(from_group_id)::bigint IS NOT NULL AND ((f.folio_type = 'MASTER' AND f.booking_group_id = sqlc.narg(from_group_id)::bigint)
                                                                   OR (@from_is_master::boolean AND res.booking_group_id = sqlc.narg(from_group_id)::bigint)), false)::boolean AS group_master
FROM folios f
LEFT JOIN reservations res ON res.property_id = f.property_id AND res.id = f.reservation_id
LEFT JOIN booking_groups bg ON bg.property_id = f.property_id AND bg.id = f.booking_group_id
LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id WHERE sr.property_id = f.property_id AND sr.stay_id = f.stay_id ORDER BY sr.id DESC LIMIT 1) cr(room_number) ON true
LEFT JOIN guests g ON g.tenant_id = f.tenant_id AND g.id = COALESCE(st.guest_id, res.guest_id)
LEFT JOIN companies comp ON comp.property_id = f.property_id AND comp.id = f.bill_to_company_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.status = 'OPEN' AND (f.stay_id IS NOT NULL OR f.folio_type = 'MASTER') AND f.id <> @from_folio_id
  AND (@include_any::boolean
       OR (sqlc.narg(from_reservation_id)::bigint IS NOT NULL AND f.reservation_id = sqlc.narg(from_reservation_id)::bigint)
       OR (sqlc.narg(from_group_id)::bigint IS NOT NULL AND (res.booking_group_id = sqlc.narg(from_group_id)::bigint OR f.booking_group_id = sqlc.narg(from_group_id)::bigint)))
  AND (sqlc.narg(q)::text IS NULL
       OR f.folio_number ILIKE sqlc.narg(q)::text || '%'
       OR lower(coalesce(bg.name, '') || ' ' || coalesce(bg.code, '')) LIKE '%' || lower(sqlc.narg(q)::text) || '%'
       OR EXISTS (SELECT 1 FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id
                  WHERE sr.property_id = f.property_id AND sr.stay_id = f.stay_id AND rm.room_number ILIKE sqlc.narg(q)::text || '%')
       OR lower(coalesce(g.first_name, '') || ' ' || coalesce(g.last_name, '')) LIKE '%' || lower(sqlc.narg(q)::text) || '%')
ORDER BY COALESCE(sqlc.narg(from_reservation_id)::bigint IS NOT NULL AND f.reservation_id = sqlc.narg(from_reservation_id)::bigint, false) DESC,
         (f.folio_type = 'MASTER') DESC,
         COALESCE(sqlc.narg(from_group_id)::bigint IS NOT NULL AND res.booking_group_id = sqlc.narg(from_group_id)::bigint, false) DESC, f.id
LIMIT @row_limit;

-- The folio of an event (docs/architecture/36-banquet.md): at most one is open; the newest is the one that counts.
-- name: InsertEventFolio :one
INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, banquet_event_id, bill_to_company_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @folio_number, 'EVENT', @event_id, sqlc.narg(company_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: GetEventFolio :one
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_type = 'EVENT' AND banquet_event_id = @event_id
ORDER BY (status = 'OPEN') DESC, id DESC LIMIT 1;

-- The group an event is linked to, read without a lock to classify a transfer; the transfer decides again under the locks.
-- name: GetEventGroup :one
SELECT booking_group_id FROM banquet_events WHERE property_id = @property_id AND id = @id;

-- The deposits received on a folio that has no stay: the payments taken before its first charge, less what was refunded of them (the view folio_item_gl decides what a deposit is).
-- name: EventDepositsReceived :one
SELECT COALESCE(-sum(signed_amount), 0)::numeric AS received FROM folio_item_gl
WHERE property_id = @property_id AND folio_id = @folio_id AND kind = 'PAYMENT' AND is_deposit;

-- The room and the guest of a folio, for the line "from FOLxxx, room N".
-- name: GetFolioPlace :one
SELECT COALESCE(cr.room_number, '')::text AS room_number, g.first_name, g.last_name, COALESCE(bg.name, '')::text AS group_name
FROM folios f
LEFT JOIN reservations res ON res.property_id = f.property_id AND res.id = f.reservation_id
LEFT JOIN booking_groups bg ON bg.property_id = f.property_id AND bg.id = f.booking_group_id
LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id WHERE sr.property_id = f.property_id AND sr.stay_id = f.stay_id ORDER BY sr.id DESC LIMIT 1) cr(room_number) ON true
LEFT JOIN guests g ON g.tenant_id = f.tenant_id AND g.id = COALESCE(st.guest_id, res.guest_id)
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.id = @id;

-- name: GetUserName :one
SELECT full_name FROM users WHERE id = @id;

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
-- The master folio of a booking group and the routing of its members (docs/architecture/30-group-folio.md, part C).

-- name: GetMasterFolio :one
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND booking_group_id = @group_id AND folio_type = 'MASTER';

-- name: GetBookingGroupBrief :one
SELECT id, code, name, company_id, contact_name, contact_email, contact_phone, arrival_date, departure_date, is_active
FROM booking_groups WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertMasterFolio :one
INSERT INTO folios (tenant_id, property_id, folio_number, folio_type, booking_group_id, bill_to_company_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @folio_number, 'MASTER', @group_id, sqlc.narg(company_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: ListGroupInstructions :many
SELECT i.scope, i.charge_code_id, c.code AS charge_code, c.name AS charge_code_name
FROM group_billing_instructions i
LEFT JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.booking_group_id = @group_id
ORDER BY CASE i.scope WHEN 'CHARGE_CODE' THEN 0 WHEN 'ROOM' THEN 1 ELSE 2 END, c.code;

-- name: DeleteGroupInstructions :exec
DELETE FROM group_billing_instructions WHERE tenant_id = @tenant_id AND property_id = @property_id AND booking_group_id = @group_id;

-- name: InsertGroupInstruction :exec
INSERT INTO group_billing_instructions (tenant_id, property_id, booking_group_id, scope, charge_code_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @group_id, @scope, sqlc.narg(charge_code_id), sqlc.narg(actor_id), sqlc.narg(actor_id));

-- The lines of the reservations of a group, with the open stay of each and whether it stays out of the routing of the group.
-- name: ListGroupLines :many
SELECT rr.id AS reservation_room_id, r.id AS reservation_id, r.confirmation_number, rr.status,
       COALESCE((SELECT s.id FROM stays s WHERE s.property_id = rr.property_id AND s.reservation_room_id = rr.id AND s.status = 'OPEN' ORDER BY s.id DESC LIMIT 1), 0)::bigint AS open_stay_id,
       COALESCE((SELECT rm.room_number FROM stays s JOIN stay_rooms s2 ON s2.property_id = s.property_id AND s2.stay_id = s.id JOIN rooms rm ON rm.property_id = s2.property_id AND rm.id = s2.room_id
                  WHERE s.property_id = rr.property_id AND s.reservation_room_id = rr.id AND s.status = 'OPEN' ORDER BY s.id DESC, s2.id DESC LIMIT 1), '')::text AS room_number,
       COALESCE((SELECT NULLIF(trim(COALESCE(gu.first_name, '') || ' ' || COALESCE(gu.last_name, '')), '') FROM stays s JOIN guests gu ON gu.tenant_id = s.tenant_id AND gu.id = s.guest_id
                  WHERE s.property_id = rr.property_id AND s.reservation_room_id = rr.id AND s.status = 'OPEN' ORDER BY s.id DESC LIMIT 1), '')::text AS guest_name,
       (x.reservation_room_id IS NOT NULL)::boolean AS excluded
FROM reservation_rooms rr
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id
LEFT JOIN folio_group_routing_exclusions x ON x.reservation_room_id = rr.id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.booking_group_id = @group_id
ORDER BY rr.id;

-- The open stays of the members of a group, ascending: what a change of the routing locks (level 41), so that it and a posting run take turns.
-- name: ListGroupOpenStayIDs :many
SELECT s.id FROM stays s
JOIN reservation_rooms rr ON rr.property_id = s.property_id AND rr.id = s.reservation_room_id
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND r.booking_group_id = @group_id AND s.status = 'OPEN'
ORDER BY s.id;

-- name: DeleteGroupExclusions :exec
DELETE FROM folio_group_routing_exclusions x
USING reservation_rooms rr, reservations r
WHERE x.reservation_room_id = rr.id AND rr.property_id = r.property_id AND rr.reservation_id = r.id
  AND x.tenant_id = @tenant_id AND x.property_id = @property_id AND r.booking_group_id = @group_id;

-- name: InsertGroupExclusion :exec
INSERT INTO folio_group_routing_exclusions (reservation_room_id, tenant_id, property_id, created_by)
VALUES (@reservation_room_id, @tenant_id, @property_id, sqlc.narg(actor_id));

-- The group rules that apply to the lines of many stays (the resolver): one row for each instruction of the group of the stay, with whether the line is excluded and the master folio of the group, open or not.
-- name: ListStayGroupRules :many
SELECT s.id AS stay_id, i.scope, i.charge_code_id, (x.reservation_room_id IS NOT NULL)::boolean AS excluded,
       COALESCE(m.id, 0)::bigint AS master_id, COALESCE(m.status, '')::text AS master_status
FROM stays s
JOIN reservation_rooms rr ON rr.property_id = s.property_id AND rr.id = s.reservation_room_id
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id AND r.booking_group_id IS NOT NULL
JOIN group_billing_instructions i ON i.property_id = r.property_id AND i.booking_group_id = r.booking_group_id
LEFT JOIN folio_group_routing_exclusions x ON x.reservation_room_id = rr.id
LEFT JOIN folios m ON m.property_id = r.property_id AND m.booking_group_id = r.booking_group_id AND m.folio_type = 'MASTER'
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = ANY(@stay_ids::bigint[]);

-- The group of a line of a reservation, with its master folio and whether the line is excluded (the notice on a reservation).
-- name: GetLineGroupRouting :one
SELECT r.booking_group_id, g.code AS group_code, g.name AS group_name, (x.reservation_room_id IS NOT NULL)::boolean AS excluded,
       COALESCE(m.id, 0)::bigint AS master_id, COALESCE(m.folio_number, '')::text AS master_number, COALESCE(m.status, '')::text AS master_status
FROM reservation_rooms rr
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id
JOIN booking_groups g ON g.property_id = r.property_id AND g.id = r.booking_group_id
LEFT JOIN folio_group_routing_exclusions x ON x.reservation_room_id = rr.id
LEFT JOIN folios m ON m.property_id = r.property_id AND m.booking_group_id = r.booking_group_id AND m.folio_type = 'MASTER'
WHERE rr.tenant_id = @tenant_id AND rr.property_id = @property_id AND rr.reservation_id = @reservation_id AND rr.id = @id;

-- The member lines still to be over (a master cannot close before): not COMPLETED, CANCELLED or NO_SHOW. A line that stays out of the master (folio_group_routing_exclusions) is not waited for:
-- it puts nothing on the master.
-- name: ListGroupLinesNotOver :many
SELECT rr.id AS reservation_room_id, r.confirmation_number, rr.status
FROM reservation_rooms rr
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.booking_group_id = @group_id AND rr.status NOT IN ('COMPLETED', 'CANCELLED', 'NO_SHOW')
  AND NOT EXISTS (SELECT 1 FROM folio_group_routing_exclusions x WHERE x.reservation_room_id = rr.id)
ORDER BY rr.id;

-- Is a line one that stays out of the master of its group?
-- name: IsLineExcluded :one
SELECT EXISTS (SELECT 1 FROM folio_group_routing_exclusions x WHERE x.reservation_room_id = @reservation_room_id)::boolean AS excluded;

-- The numbers of the rooms of the stays of a group, for the invoice of its master folio.
-- name: ListGroupRoomNumbers :many
SELECT DISTINCT rm.room_number::text AS room_number
FROM stay_rooms sr
JOIN stays s ON s.property_id = sr.property_id AND s.id = sr.stay_id
JOIN reservation_rooms rr ON rr.property_id = s.property_id AND rr.id = s.reservation_room_id
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id
JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.booking_group_id = @group_id
ORDER BY 1;

-- The charges already posted to the open folios of the members of a group that its instructions would send to the master now: one row for each folio and charge code, with how many and how much.
-- A line that is excluded from the routing and a charge something has reversed are left out.
-- name: ListPostedForMaster :many
SELECT f.id AS folio_id, f.folio_number, r.id AS reservation_id, r.confirmation_number, i.charge_code_id, cc.code AS charge_code, cc.name AS charge_code_name, cc.charge_type,
       count(*)::int AS item_count, sum(i.debit - i.credit)::numeric AS total
FROM folios f
JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
JOIN stays s ON s.property_id = f.property_id AND s.id = f.stay_id
LEFT JOIN folio_group_routing_exclusions x ON x.reservation_room_id = s.reservation_room_id
JOIN folio_items i ON i.property_id = f.property_id AND i.folio_id = f.id AND i.transaction_type = 'CHARGE'
JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.status = 'OPEN' AND f.folio_type IN ('GUEST', 'COMPANY') AND r.booking_group_id = @group_id
  AND x.reservation_room_id IS NULL AND cc.charge_type <> 'TRANSFER'
  AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = i.property_id AND rv.reverses_item_id = i.id)
GROUP BY f.id, f.folio_number, r.id, r.confirmation_number, i.charge_code_id, cc.code, cc.name, cc.charge_type
ORDER BY f.id, cc.code;

-- A stay of a member of the group of a master folio (a charge posted to the master can name whose it is).
-- name: StayOfGroup :one
SELECT s.id FROM stays s
JOIN reservation_rooms rr ON rr.property_id = s.property_id AND rr.id = s.reservation_room_id
JOIN reservations r ON r.property_id = rr.property_id AND r.id = rr.reservation_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = @stay_id AND r.booking_group_id = @group_id AND s.status <> 'CANCELLED';

-- The batches of a request that moves many folios in one transaction: each carries the key of the request and a suffix.
-- name: ListBatchesByKeyPrefix :many
SELECT * FROM folio_transfer_batches WHERE tenant_id = @tenant_id AND property_id = @property_id AND starts_with(idempotency_key, @prefix::text) ORDER BY id;

-- Where the room charges of a stay stand now: the room-night lines that were not reversed, counted and added up for each folio (a night posted before the group's routing is on the guest folio, one after it on the master).
-- name: ListStayRoomChargesByFolio :many
SELECT i.folio_id, f.folio_number::text AS folio_number, f.folio_type::text AS folio_type, count(*)::int AS nights, COALESCE(sum(i.debit), 0)::numeric AS total
FROM folio_items i
JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.stay_id = @stay_id AND i.stay_room_id IS NOT NULL AND i.transaction_type = 'CHARGE'
  AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = i.property_id AND rv.reverses_item_id = i.id)
GROUP BY i.folio_id, f.folio_number, f.folio_type
ORDER BY min(i.id);

-- The payments (not refunds) that carry one of these idempotency keys, with what was refunded of each: the deposits of a group check-in, read by the key made from the room.
-- name: ListPaymentsByKeys :many
SELECT p.id, p.payment_number, p.amount, p.status, p.idempotency_key,
       COALESCE((SELECT sum(r.amount) FROM payments r WHERE r.property_id = p.property_id AND r.refund_of_payment_id = p.id AND r.status = 'POSTED'), 0)::numeric AS refunded
FROM payments p
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.payment_type = 'PAYMENT' AND p.idempotency_key = ANY(@keys::text[]);

-- ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------
-- The lock of a folio for outlet charges (docs/architecture/34-pos.md section 6).

-- name: GetOutletLock :one
SELECT folio_id, status, limit_amount, valid_mode, valid_on, period_no, locked_reason, opened_by, opened_at, updated_by, updated_at, version
FROM folio_outlet_locks WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_id = @folio_id;

-- name: InsertOutletLock :exec
INSERT INTO folio_outlet_locks (folio_id, tenant_id, property_id, status, limit_amount, valid_mode, valid_on, period_no, locked_reason, opened_by, updated_by)
VALUES (@folio_id, @tenant_id, @property_id, @status, @limit_amount, @valid_mode, sqlc.narg(valid_on), @period_no, sqlc.narg(locked_reason), sqlc.narg(actor_id), sqlc.narg(actor_id));

-- name: UpdateOutletLock :exec
UPDATE folio_outlet_locks SET status = @status, limit_amount = @limit_amount, valid_mode = @valid_mode, valid_on = sqlc.narg(valid_on), period_no = @period_no,
       locked_reason = sqlc.narg(locked_reason), opened_by = COALESCE(sqlc.narg(opened_by)::bigint, opened_by), opened_at = COALESCE(sqlc.narg(opened_at)::timestamptz, opened_at),
       updated_by = sqlc.narg(actor_id), version = version + 1
WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_id = @folio_id;

-- name: DeleteLockOutlets :exec
DELETE FROM folio_outlet_lock_outlets WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_id = @folio_id;

-- name: InsertLockOutlet :exec
INSERT INTO folio_outlet_lock_outlets (tenant_id, property_id, folio_id, outlet_id) VALUES (@tenant_id, @property_id, @folio_id, @outlet_id);

-- name: ListLockOutlets :many
SELECT o.id, o.code, o.name, o.is_active
FROM folio_outlet_lock_outlets l JOIN pos_outlets o ON o.property_id = l.property_id AND o.id = l.outlet_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.folio_id = @folio_id
ORDER BY o.sort_order, o.name, o.id;

-- name: InsertOutletLockEvent :exec
INSERT INTO folio_outlet_lock_events (tenant_id, property_id, folio_id, kind, reason, old_value, new_value, business_date, actor_id)
VALUES (@tenant_id, @property_id, @folio_id, @kind, sqlc.narg(reason), sqlc.narg(old_value)::jsonb, sqlc.narg(new_value)::jsonb, @business_date, sqlc.narg(actor_id));

-- name: ListOutletLockEvents :many
SELECT e.id, e.kind, e.reason, e.old_value, e.new_value, e.business_date, e.actor_id, COALESCE(u.full_name, '')::text AS actor_name, e.created_at
FROM folio_outlet_lock_events e LEFT JOIN users u ON u.id = e.actor_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.folio_id = @folio_id
ORDER BY e.id DESC LIMIT 50;

-- What counts against the limit: the accepted charges of the lock period that were not returned or moved away through POS. Never the balance of the folio.
-- name: OutletChargesUsed :one
SELECT COALESCE(sum(amount), 0)::numeric AS used
FROM pos_room_charges
WHERE tenant_id = @tenant_id AND property_id = @property_id AND to_folio_id = @folio_id AND lock_period_no = @period_no AND returned_at IS NULL;

-- name: InsertRoomChargeAccepted :one
INSERT INTO pos_room_charges (tenant_id, property_id, outlet_id, bill_id, bill_number, from_folio_id, to_folio_id, stay_id, amount, lock_period_no, transfer_batch_id,
                              business_date, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @outlet_id, sqlc.narg(bill_id), sqlc.narg(bill_number), sqlc.narg(from_folio_id), @to_folio_id, sqlc.narg(stay_id), @amount, @period_no,
        sqlc.narg(transfer_batch_id), @business_date, sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertChargeAttempt :one
INSERT INTO pos_room_charge_attempts (tenant_id, property_id, source, folio_id, requested_stay_id, outlet_id, bill_id, bill_number, amount, result, code, context,
                                      pos_room_charge_id, business_date, created_by)
VALUES (@tenant_id, @property_id, @source, @folio_id, sqlc.narg(requested_stay_id), @outlet_id, sqlc.narg(bill_id), sqlc.narg(bill_number), @amount, @result,
        sqlc.narg(code), @context::jsonb, sqlc.narg(pos_room_charge_id), @business_date, sqlc.narg(actor_id))
RETURNING id;

-- The incoming outlet charges of a business date, every room, with the attempts that were refused.
-- name: ListChargeAttempts :many
SELECT a.id, a.source, a.folio_id, f.folio_number, f.folio_type, a.requested_stay_id, a.outlet_id, o.code AS outlet_code, o.name AS outlet_name, a.bill_number, a.amount, a.result, a.code,
       a.context, a.business_date, a.created_at, COALESCE(u.full_name, '')::text AS created_by_name,
       COALESCE(cr.room_number, '')::text AS room_number, COALESCE(NULLIF(trim(COALESCE(g.first_name, '') || ' ' || COALESCE(g.last_name, '')), ''), '')::text AS guest_name,
       COALESCE(bg.name, '')::text AS group_name,
       COALESCE((SELECT l.status FROM folio_outlet_locks l WHERE l.folio_id = a.folio_id), '')::text AS lock_status
FROM pos_room_charge_attempts a
JOIN folios f ON f.property_id = a.property_id AND f.id = a.folio_id
JOIN pos_outlets o ON o.property_id = a.property_id AND o.id = a.outlet_id
LEFT JOIN users u ON u.id = a.created_by
LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = COALESCE(a.requested_stay_id, f.stay_id)
LEFT JOIN reservations res ON res.property_id = f.property_id AND res.id = f.reservation_id
LEFT JOIN booking_groups bg ON bg.property_id = f.property_id AND bg.id = f.booking_group_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id
                    WHERE sr.property_id = st.property_id AND sr.stay_id = st.id ORDER BY sr.id DESC LIMIT 1) cr(room_number) ON true
LEFT JOIN guests g ON g.tenant_id = f.tenant_id AND g.id = COALESCE(st.guest_id, res.guest_id)
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.business_date = @business_date
  AND (sqlc.narg(result)::text IS NULL OR a.result = sqlc.narg(result)::text)
ORDER BY a.id DESC
LIMIT @row_limit;

-- The open locks of the folios of a stay (the check-out locks them).
-- name: ListOpenOutletLocksOfStay :many
SELECT l.folio_id, l.period_no FROM folio_outlet_locks l JOIN folios f ON f.property_id = l.property_id AND f.id = l.folio_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND f.stay_id = @stay_id AND l.status = 'OPEN';

-- name: ListExpiredTodayLocks :many
SELECT folio_id FROM folio_outlet_locks
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'OPEN' AND valid_mode = 'TODAY' AND valid_on <= @business_date
ORDER BY folio_id;

-- name: GetFolioForOutletLock :one
SELECT f.id, f.folio_number, f.folio_type, f.status, f.stay_id, f.booking_group_id, f.version, COALESCE(st.status, '')::text AS stay_status,
       COALESCE(cr.room_number, '')::text AS room_number, g.first_name, g.last_name, COALESCE(bg.name, '')::text AS group_name, rrm.departure_date AS checkout_date
FROM folios f
LEFT JOIN reservations res ON res.property_id = f.property_id AND res.id = f.reservation_id
LEFT JOIN booking_groups bg ON bg.property_id = f.property_id AND bg.id = f.booking_group_id
LEFT JOIN stays st ON st.property_id = f.property_id AND st.id = f.stay_id
LEFT JOIN reservation_rooms rrm ON rrm.property_id = st.property_id AND rrm.id = st.reservation_room_id
LEFT JOIN LATERAL (SELECT rm.room_number FROM stay_rooms sr JOIN rooms rm ON rm.property_id = sr.property_id AND rm.id = sr.room_id
                    WHERE sr.property_id = f.property_id AND sr.stay_id = f.stay_id ORDER BY sr.id DESC LIMIT 1) cr(room_number) ON true
LEFT JOIN guests g ON g.tenant_id = f.tenant_id AND g.id = COALESCE(st.guest_id, res.guest_id)
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.id = @id;

-- name: ListActiveOutletIDs :many
SELECT id FROM pos_outlets WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_active ORDER BY id;

-- name: OutletInfo :one
SELECT id, code, name, is_active, allow_room_charge FROM pos_outlets WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetOutletBrief :one
SELECT id, code, name, department_id, drawer, rounding_unit, comp_treatment FROM pos_outlets WHERE property_id = @property_id AND id = @id;

-- The account of the system key COMP (complimentary): a COMP payment is refused while it is not mapped to an active account that takes postings.
-- name: GetCompAccount :one
SELECT a.code, a.is_active, a.is_postable, a.account_type
FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = 'COMP';

-- A company an outlet may bill (companies.pos_billing_allowed, 34-pos.md D8).
-- name: GetCompanyPosBilling :one
SELECT id, name, is_active, pos_billing_allowed FROM companies WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRoomCharge :one
SELECT * FROM pos_room_charges WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- A charge to a room is returned once, with the transfer that sent it back.
-- name: MarkRoomChargeReturned :execrows
UPDATE pos_room_charges SET returned_at = @returned_at::timestamptz, returned_by_transfer_id = @transfer_id
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND returned_at IS NULL;

-- The open bills (with a stay of their own as the room they are for) that a check-out should warn about.
-- name: ListOpenBillsOfStay :many
SELECT b.id, b.bill_number, o.name AS outlet_name
FROM pos_bills b JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.target_stay_id = @stay_id AND b.status = 'OPEN'
ORDER BY b.id;

-- name: ReopenFolio :one
UPDATE folios f SET status = 'OPEN', closed_at = NULL, closed_by = NULL, closed_on = NULL, version = f.version + 1, updated_by = sqlc.narg(actor_id)
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.id = @id AND f.status = 'CLOSED'
RETURNING *;

-- The system code POS_ROUNDING of a property (charge type ROUNDING).
-- name: GetRoundingChargeCode :one
SELECT id, code FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND charge_type = 'ROUNDING' AND is_active ORDER BY id LIMIT 1;

-- name: ListPaymentsOfFolio :many
SELECT * FROM payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_id = @folio_id ORDER BY id;

-- The folio a request to pay a bill of an outlet was applied to (its payments are K#0, K#1, ... and its charge to a room K#room): a repeat of the request finds it and answers the bill as it is.
-- name: OutletPayKeyFolio :one
SELECT folio_id FROM (
    SELECT p.folio_id FROM payments p WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.idempotency_key LIKE @prefix::text || '#%'
    UNION ALL
    SELECT c.from_folio_id FROM pos_room_charges c WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.idempotency_key LIKE @prefix::text || '#%' AND c.from_folio_id IS NOT NULL
) k LIMIT 1;

-- The bill of an outlet a line of a folio comes from: the line TRF "{outlet} · {bill}" that a charge to a room made points at its bill (the batch of the transfer is the one of the charge).
-- name: ListPosBillsOfItems :many
SELECT i.id AS item_id, b.id AS bill_id, COALESCE(b.bill_number, '')::text AS bill_number, o.name AS outlet_name
FROM folio_items i
JOIN pos_room_charges c ON c.property_id = i.property_id AND c.transfer_batch_id::text = i.reference_id AND i.reference_type = 'FOLIO_BALANCE_TRANSFER' AND c.to_folio_id = i.folio_id
JOIN pos_bills b ON b.property_id = c.property_id AND b.id = c.bill_id
JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = ANY(@ids::bigint[]);
