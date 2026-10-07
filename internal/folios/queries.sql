-- Folios module queries (sqlc): folios, the append-only ledger (folio_items and components) and payments.
-- Scoped by tenant_id and property_id. Only posting.go writes folio_items and their components.

-- name: GetFolio :one
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertFolio :one
INSERT INTO folios (tenant_id, property_id, folio_number, reservation_id, stay_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @folio_number, @reservation_id, sqlc.narg(stay_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
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

-- name: ListFolios :many
SELECT f.*, COALESCE(sum(i.debit), 0)::numeric AS debit, COALESCE(sum(i.credit), 0)::numeric AS credit
FROM folios f
LEFT JOIN folio_items i ON i.property_id = f.property_id AND i.folio_id = f.id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.id > @after_id
  AND (sqlc.narg(reservation_id)::bigint IS NULL OR f.reservation_id = sqlc.narg(reservation_id)::bigint)
  AND (sqlc.narg(stay_id)::bigint IS NULL OR f.stay_id = sqlc.narg(stay_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR f.status = sqlc.narg(status)::text)
GROUP BY f.id
ORDER BY f.id
LIMIT @row_limit;

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
    CASE WHEN COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint) IS NOT NULL
         THEN (SELECT o.revenue_account_code FROM folio_items o WHERE o.property_id = @property_id AND o.id = COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint))
         ELSE (SELECT c.gl_account_code FROM charge_codes c WHERE c.property_id = @property_id AND c.id = sqlc.narg(charge_code_id)::bigint)
    END,
    -- The department in force now: the default of the charge code, else the default of its revenue account, and none when the account takes none;
    -- a reversal copies the department of the item it reverses.
    CASE WHEN COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint) IS NOT NULL
         THEN (SELECT o.department_id FROM folio_items o WHERE o.property_id = @property_id AND o.id = COALESCE(sqlc.narg(reverses_item_id)::bigint, sqlc.narg(copies_item_id)::bigint))
         ELSE (SELECT CASE WHEN a.department_requirement = 'NONE' THEN NULL ELSE COALESCE(c.department_id, a.default_department_id) END
                 FROM charge_codes c LEFT JOIN gl_accounts a ON a.property_id = c.property_id AND a.code = c.gl_account_code
                WHERE c.property_id = @property_id AND c.id = sqlc.narg(charge_code_id)::bigint)
    END
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
SELECT i.*, c.code AS charge_code, rv.id AS reversed_by_item_id, r.room_number AS room_number,
       COALESCE(g.group_code, 'A')::text AS group_code -- a reversal is printed with the line it reverses; a line without a row is in group A
FROM folio_items i
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

-- name: ListPayments :many
SELECT * FROM payments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id < @before_id_or_max::bigint
  AND (sqlc.narg(business_date)::date IS NULL OR business_date = sqlc.narg(business_date)::date)
  AND (sqlc.narg(payment_method)::text IS NULL OR payment_method = sqlc.narg(payment_method)::text)
ORDER BY id DESC
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
