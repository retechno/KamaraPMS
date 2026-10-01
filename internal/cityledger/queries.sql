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

-- Transfers of a company that are not on a live invoice, with the state of the stay behind them. Only a transfer
-- whose guest has checked out can be invoiced. ids narrows the list to the transfers an invoice asks for.
-- name: ListInvoiceCandidates :many
SELECT p.id, p.payment_number, p.business_date, p.amount, p.reference_number,
       f.folio_number, r.confirmation_number,
       COALESCE(s.stay_number, '')::text AS stay_number, COALESCE(s.status, '')::text AS stay_status,
       s.arrival_date AS arrival_date, s.departure_date AS departure_date, s.actual_check_out_at AS checked_out_at,
       COALESCE(NULLIF(trim(COALESCE(g.first_name, '') || ' ' || COALESCE(g.last_name, '')), ''), '')::text AS guest_name,
       COALESCE((SELECT string_agg(ro.room_number, ', ' ORDER BY sr.start_business_date, sr.id)
                 FROM stay_rooms sr JOIN rooms ro ON ro.property_id = sr.property_id AND ro.id = sr.room_id
                 WHERE sr.property_id = s.property_id AND sr.stay_id = s.id), '')::text AS room_numbers
FROM payments p
JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id
JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN stays s ON s.property_id = f.property_id AND s.id = f.stay_id
LEFT JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.company_id = @company_id
  AND p.payment_type = 'PAYMENT' AND p.status = 'POSTED'
  AND (sqlc.narg(ids)::bigint[] IS NULL OR p.id = ANY(sqlc.narg(ids)::bigint[]))
  AND NOT EXISTS (SELECT 1 FROM city_ledger_invoice_lines l WHERE l.property_id = p.property_id AND l.payment_id = p.id AND l.released_at IS NULL)
ORDER BY p.business_date, p.id;

-- name: InsertInvoice :one
INSERT INTO city_ledger_invoices (
    tenant_id, property_id, invoice_number, company_id, invoice_date, due_date, total, notes, idempotency_key, created_by
) VALUES (
    @tenant_id, @property_id, @invoice_number, @company_id, @invoice_date, @due_date, @total, sqlc.narg(notes), sqlc.narg(idempotency_key), sqlc.narg(actor_id)
)
RETURNING *;

-- name: InsertInvoiceLine :exec
INSERT INTO city_ledger_invoice_lines (tenant_id, property_id, invoice_id, payment_id, amount)
VALUES (@tenant_id, @property_id, @invoice_id, @payment_id, @amount);

-- name: ReleaseInvoiceLines :exec
UPDATE city_ledger_invoice_lines SET released_at = @now::timestamptz
WHERE property_id = @property_id AND invoice_id = @invoice_id AND released_at IS NULL;

-- name: GetInvoice :one
SELECT * FROM city_ledger_invoices WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetInvoiceByKey :one
SELECT * FROM city_ledger_invoices WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ListInvoices :many
SELECT * FROM city_ledger_invoices
WHERE tenant_id = @tenant_id AND property_id = @property_id AND company_id = @company_id
ORDER BY invoice_date DESC, id DESC;

-- name: VoidInvoice :one
UPDATE city_ledger_invoices
SET status = 'VOIDED', voided_at = @now::timestamptz, voided_by = sqlc.narg(actor_id), void_reason = @reason, approved_by = @approved_by
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: ListInvoiceLines :many
SELECT p.id AS payment_id, p.payment_number, p.business_date, l.amount, p.reference_number,
       f.folio_number, r.confirmation_number,
       COALESCE(s.stay_number, '')::text AS stay_number,
       s.arrival_date AS arrival_date, s.departure_date AS departure_date, s.actual_check_out_at AS checked_out_at,
       COALESCE(NULLIF(trim(COALESCE(g.first_name, '') || ' ' || COALESCE(g.last_name, '')), ''), '')::text AS guest_name,
       COALESCE((SELECT string_agg(ro.room_number, ', ' ORDER BY sr.start_business_date, sr.id)
                 FROM stay_rooms sr JOIN rooms ro ON ro.property_id = sr.property_id AND ro.id = sr.room_id
                 WHERE sr.property_id = s.property_id AND sr.stay_id = s.id), '')::text AS room_numbers
FROM city_ledger_invoice_lines l
JOIN payments p ON p.property_id = l.property_id AND p.id = l.payment_id
JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id
JOIN reservations r ON r.property_id = f.property_id AND r.id = f.reservation_id
LEFT JOIN stays s ON s.property_id = f.property_id AND s.id = f.stay_id
LEFT JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.invoice_id = @invoice_id
ORDER BY p.business_date, p.id;

-- name: InsertAllocation :exec
INSERT INTO city_ledger_receipt_allocations (tenant_id, property_id, receipt_id, invoice_id, company_id, amount)
VALUES (@tenant_id, @property_id, @receipt_id, @invoice_id, @company_id, @amount);

-- What invoices have been paid: the allocations of receipts that are still posted.
-- name: InvoicePaid :many
SELECT a.invoice_id, sum(a.amount)::numeric AS paid
FROM city_ledger_receipt_allocations a
JOIN city_ledger_receipts r ON r.property_id = a.property_id AND r.id = a.receipt_id AND r.status = 'POSTED'
WHERE a.property_id = @property_id AND a.invoice_id = ANY(@invoice_ids::bigint[])
GROUP BY a.invoice_id;

-- name: ListCompanyAllocations :many
SELECT a.receipt_id, a.invoice_id, i.invoice_number, a.amount
FROM city_ledger_receipt_allocations a
JOIN city_ledger_invoices i ON i.property_id = a.property_id AND i.id = a.invoice_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.company_id = @company_id
ORDER BY a.receipt_id, a.invoice_id;
