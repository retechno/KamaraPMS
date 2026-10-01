-- Accounts: every company with what was transferred to it and what it paid back (posted rows only).
-- name: ListAccounts :many
SELECT a.id, a.code, a.name, a.credit_limit, a.payment_terms_days, a.is_active, a.transferred, a.received
FROM (
    SELECT c.id, c.code, c.name, c.credit_limit, c.payment_terms_days, c.is_active,
           COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = c.property_id AND p.company_id = c.id AND p.status = 'POSTED'), 0)::numeric AS transferred,
           COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r WHERE r.property_id = c.property_id AND r.company_id = c.id AND r.status = 'POSTED'), 0)::numeric AS received
    FROM companies c
    WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.id > @after_id
      AND (sqlc.narg(q)::text IS NULL OR c.code ILIKE '%' || sqlc.narg(q)::text || '%' OR c.name ILIKE '%' || sqlc.narg(q)::text || '%')
) a
WHERE (NOT @owing::boolean OR a.transferred - a.received > 0)
ORDER BY a.id
LIMIT @row_limit;

-- name: GetAccount :one
SELECT c.id, c.code, c.name, c.credit_limit, c.payment_terms_days, c.is_active, c.email, c.phone, c.address, c.contact_name,
       COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = c.property_id AND p.company_id = c.id AND p.status = 'POSTED'), 0)::numeric AS transferred,
       COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r WHERE r.property_id = c.property_id AND r.company_id = c.id AND r.status = 'POSTED'), 0)::numeric AS received
FROM companies c
WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.id = @id;

-- Transfers of one company, oldest first, with the folio, reservation and booker they came from.
-- name: ListTransfers :many
SELECT p.id, p.payment_number, p.business_date, p.amount, p.status, p.reference_number,
       f.folio_number, r.confirmation_number,
       COALESCE(NULLIF(trim(COALESCE(g.first_name, '') || ' ' || g.last_name), ''), '')::text AS guest_name
FROM payments p
JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id
JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN guests g ON g.tenant_id = r.tenant_id AND g.id = r.guest_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.company_id = @company_id AND p.payment_type = 'PAYMENT'
ORDER BY p.business_date, p.id;

-- name: ListReceipts :many
SELECT * FROM city_ledger_receipts
WHERE tenant_id = @tenant_id AND property_id = @property_id AND company_id = @company_id
ORDER BY business_date, id;

-- name: GetReceipt :one
SELECT * FROM city_ledger_receipts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetReceiptByKey :one
SELECT * FROM city_ledger_receipts WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: InsertReceipt :one
INSERT INTO city_ledger_receipts (
    tenant_id, property_id, receipt_number, company_id, amount, payment_method, reference_number, remarks,
    business_date, paid_at, idempotency_key, created_by
) VALUES (
    @tenant_id, @property_id, @receipt_number, @company_id, @amount, @payment_method, sqlc.narg(reference_number), sqlc.narg(remarks),
    @business_date, @paid_at, sqlc.narg(idempotency_key), sqlc.narg(actor_id)
)
RETURNING *;

-- name: VoidReceipt :one
UPDATE city_ledger_receipts
SET status = 'VOIDED', voided_at = @now::timestamptz, voided_by = sqlc.narg(actor_id), void_reason = @reason, approved_by = @approved_by
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;
