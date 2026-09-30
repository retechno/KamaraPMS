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
UPDATE folios SET status = 'CLOSED', closed_at = @now::timestamptz, closed_by = sqlc.narg(actor_id), version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
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
    source, reason, idempotency_key, created_by, approved_by
) VALUES (
    @tenant_id, @property_id, @folio_id, @business_date, @transaction_at, @service_date, @transaction_type, sqlc.narg(charge_code_id), sqlc.narg(payment_id),
    sqlc.narg(reverses_item_id), sqlc.narg(stay_id), sqlc.narg(stay_room_id), sqlc.narg(reference_type), sqlc.narg(reference_id), @description, @quantity, @unit_price, @price_mode,
    @base_amount, @discount_amount, @net_amount, @rounding_adjustment, @service_charge_total, @tax_total, @debit, @credit,
    @source, sqlc.narg(reason), sqlc.narg(idempotency_key), sqlc.narg(actor_id), sqlc.narg(approved_by)
)
RETURNING *;

-- name: InsertFolioItemComponent :exec
INSERT INTO folio_item_components (
    tenant_id, property_id, folio_item_id, component_type, tax_id, service_charge_id, code, name, rate, tax_on_service,
    base_amount, amount, sequence
) VALUES (
    @tenant_id, @property_id, @folio_item_id, @component_type, sqlc.narg(tax_id), sqlc.narg(service_charge_id), @code, @name, @rate,
    sqlc.narg(tax_on_service), @base_amount, @amount, @sequence
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
SELECT i.*, c.code AS charge_code, rv.id AS reversed_by_item_id, r.room_number AS room_number
FROM folio_items i
LEFT JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
LEFT JOIN folio_items rv ON rv.property_id = i.property_id AND rv.reverses_item_id = i.id
LEFT JOIN stay_rooms sr ON sr.property_id = i.property_id AND sr.id = i.stay_room_id
LEFT JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.folio_id = @folio_id
ORDER BY i.transaction_at, i.id;

-- name: GetReversalOf :one
SELECT id FROM folio_items WHERE property_id = @property_id AND reverses_item_id = @item_id;

-- A reversed room-charge item flips its posting register row (POSTED -> REVERSED); other items have none.
-- name: FlipPostingRegister :exec
UPDATE stay_charge_postings SET status = 'REVERSED', reversal_item_id = @reversal_item_id
WHERE property_id = @property_id AND folio_item_id = @item_id AND status = 'POSTED';

-- ---------------------------------------------------------------------------
-- Payments

-- name: InsertPayment :one
INSERT INTO payments (
    tenant_id, property_id, payment_number, folio_id, payment_type, payment_method, amount, paid_at, business_date,
    reference_number, refund_of_payment_id, idempotency_key, remarks, created_by, approved_by
) VALUES (
    @tenant_id, @property_id, @payment_number, @folio_id, @payment_type, @payment_method, @amount, @paid_at, @business_date,
    sqlc.narg(reference_number), sqlc.narg(refund_of_payment_id), sqlc.narg(idempotency_key), sqlc.narg(remarks), sqlc.narg(actor_id), sqlc.narg(approved_by)
)
RETURNING *;

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

-- name: CountChargeItems :one
SELECT count(*)::int FROM folio_items
WHERE property_id = @property_id AND folio_id = @folio_id AND transaction_type = 'CHARGE';

-- name: ListStayOpenFolios :many
SELECT * FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND stay_id = @stay_id AND status = 'OPEN' ORDER BY id;
