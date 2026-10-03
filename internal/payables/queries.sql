-- Payables (sqlc): suppliers, supplier bills, supplier payments and their allocations. Every query is scoped by tenant_id and property_id.

-- ---------------------------------------------------------------------------------------------------------------
-- Suppliers

-- A supplier with what is owed to it now: bills not voided less the allocations of payments not voided.
-- name: ListSuppliers :many
SELECT s.id, s.code, s.name, s.contact_name, s.email, s.phone, s.address, s.city, s.tax_id, s.payment_terms_days, s.default_account_id,
       da.code AS default_account_code, da.name AS default_account_name, s.bank_details, s.notes, s.is_active, s.created_at,
       (COALESCE((SELECT sum(b.total) FROM supplier_bills b WHERE b.property_id = s.property_id AND b.supplier_id = s.id AND b.status = 'POSTED'), 0)
        - COALESCE((SELECT sum(a.amount) FROM supplier_payment_allocations a JOIN supplier_payments x ON x.property_id = a.property_id AND x.id = a.payment_id
                     JOIN supplier_bills b ON b.property_id = a.property_id AND b.id = a.bill_id
                    WHERE a.property_id = s.property_id AND a.supplier_id = s.id AND x.status = 'POSTED' AND b.status = 'POSTED'), 0))::numeric AS outstanding
FROM suppliers s
LEFT JOIN gl_accounts da ON da.property_id = s.property_id AND da.id = s.default_account_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR s.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR s.is_active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR s.code ILIKE '%' || sqlc.narg(q)::text || '%' OR s.name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY s.name, s.id
LIMIT @row_limit;

-- name: InsertSupplier :one
INSERT INTO suppliers (tenant_id, property_id, code, name, contact_name, email, phone, address, city, tax_id, payment_terms_days, default_account_id,
                       bank_details, notes, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(contact_name), sqlc.narg(email), sqlc.narg(phone), sqlc.narg(address), sqlc.narg(city), sqlc.narg(tax_id),
        @payment_terms_days, sqlc.narg(default_account_id), sqlc.narg(bank_details), sqlc.narg(notes), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateSupplier :exec
UPDATE suppliers SET name = @name, contact_name = sqlc.narg(contact_name), email = sqlc.narg(email), phone = sqlc.narg(phone), address = sqlc.narg(address),
       city = sqlc.narg(city), tax_id = sqlc.narg(tax_id), payment_terms_days = @payment_terms_days, default_account_id = sqlc.narg(default_account_id),
       bank_details = sqlc.narg(bank_details), notes = sqlc.narg(notes), is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SupplierActive :one
SELECT is_active, payment_terms_days FROM suppliers WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------
-- Bills

-- name: InsertBill :one
INSERT INTO supplier_bills (tenant_id, property_id, bill_number, supplier_id, supplier_invoice_number, bill_date, due_date, description, total, journal_id,
                            idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @bill_number, @supplier_id, @supplier_invoice_number, @bill_date, @due_date, sqlc.narg(description), @total, @journal_id,
        sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertBillLine :exec
INSERT INTO supplier_bill_lines (tenant_id, property_id, bill_id, line_no, account_id, description, amount, vat_amount, vat_treatment)
VALUES (@tenant_id, @property_id, @bill_id, @line_no, @account_id, sqlc.narg(description), @amount, @vat_amount, sqlc.narg(vat_treatment));

-- A bill with its supplier, what has been paid and the journal numbers.
-- name: ListBills :many
SELECT b.id, b.bill_number, b.supplier_id, s.code AS supplier_code, s.name AS supplier_name, b.supplier_invoice_number, b.bill_date, b.due_date, b.description,
       b.total, b.status, b.journal_id, jn.journal_number, b.void_journal_id, b.voided_at, b.void_reason, b.created_at,
       COALESCE((SELECT sum(a.amount) FROM supplier_payment_allocations a JOIN supplier_payments x ON x.property_id = a.property_id AND x.id = a.payment_id
                  WHERE a.property_id = b.property_id AND a.bill_id = b.id AND x.status = 'POSTED'), 0)::numeric AS paid
FROM supplier_bills b
JOIN suppliers s ON s.property_id = b.property_id AND s.id = b.supplier_id
JOIN gl_journals jn ON jn.property_id = b.property_id AND jn.id = b.journal_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR b.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR b.supplier_id = sqlc.narg(supplier_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR b.status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR b.bill_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR b.bill_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(q)::text IS NULL OR b.bill_number ILIKE '%' || sqlc.narg(q)::text || '%' OR b.supplier_invoice_number ILIKE '%' || sqlc.narg(q)::text || '%'
       OR s.name ILIKE '%' || sqlc.narg(q)::text || '%')
  AND (NOT @open_only::boolean OR (b.status = 'POSTED' AND b.total > COALESCE((SELECT sum(a.amount) FROM supplier_payment_allocations a
        JOIN supplier_payments x ON x.property_id = a.property_id AND x.id = a.payment_id
        WHERE a.property_id = b.property_id AND a.bill_id = b.id AND x.status = 'POSTED'), 0)))
ORDER BY b.bill_date DESC, b.id DESC
LIMIT @row_limit;

-- name: ListBillLines :many
SELECT l.line_no, l.account_id, a.code AS account_code, a.name AS account_name, l.description, l.amount, l.vat_amount, l.vat_treatment
FROM supplier_bill_lines l
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.bill_id = @bill_id
ORDER BY l.line_no;

-- name: FindBillByKey :one
SELECT id FROM supplier_bills WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: BillSupplier :one
SELECT supplier_id FROM supplier_bills WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: VoidBill :exec
UPDATE supplier_bills SET status = 'VOIDED', voided_at = @now, voided_by = sqlc.narg(actor_id), void_reason = @reason, void_journal_id = @void_journal_id,
       approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The open bills of a supplier, oldest due first, with what is left of each.
-- name: OpenBillsOfSupplier :many
SELECT b.id, b.bill_number, b.supplier_invoice_number, b.bill_date, b.due_date, b.total,
       (b.total - COALESCE((SELECT sum(a.amount) FROM supplier_payment_allocations a JOIN supplier_payments x ON x.property_id = a.property_id AND x.id = a.payment_id
                             WHERE a.property_id = b.property_id AND a.bill_id = b.id AND x.status = 'POSTED'), 0))::numeric AS outstanding
FROM supplier_bills b
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.supplier_id = @supplier_id AND b.status = 'POSTED'
ORDER BY b.due_date, b.id;

-- ---------------------------------------------------------------------------------------------------------------
-- Payments

-- name: InsertSupplierPayment :one
INSERT INTO supplier_payments (tenant_id, property_id, payment_number, supplier_id, payment_date, amount, payment_method, reference_number, remarks, journal_id,
                               idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @payment_number, @supplier_id, @payment_date, @amount, @payment_method, sqlc.narg(reference_number), sqlc.narg(remarks), @journal_id,
        sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertAllocation :exec
INSERT INTO supplier_payment_allocations (tenant_id, property_id, supplier_id, payment_id, bill_id, amount)
VALUES (@tenant_id, @property_id, @supplier_id, @payment_id, @bill_id, @amount);

-- name: ListSupplierPayments :many
SELECT x.id, x.payment_number, x.supplier_id, s.code AS supplier_code, s.name AS supplier_name, x.payment_date, x.amount, x.payment_method, x.reference_number,
       x.remarks, x.status, x.journal_id, jn.journal_number, x.void_journal_id, x.voided_at, x.void_reason, x.created_at
FROM supplier_payments x
JOIN suppliers s ON s.property_id = x.property_id AND s.id = x.supplier_id
JOIN gl_journals jn ON jn.property_id = x.property_id AND jn.id = x.journal_id
WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR x.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(supplier_id)::bigint IS NULL OR x.supplier_id = sqlc.narg(supplier_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR x.status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR x.payment_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR x.payment_date <= sqlc.narg(to_date)::date)
ORDER BY x.payment_date DESC, x.id DESC
LIMIT @row_limit;

-- name: ListAllocations :many
SELECT a.bill_id, b.bill_number, b.supplier_invoice_number, a.amount
FROM supplier_payment_allocations a
JOIN supplier_bills b ON b.property_id = a.property_id AND b.id = a.bill_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.payment_id = @payment_id
ORDER BY b.due_date, b.id;

-- name: PaymentSupplier :one
SELECT supplier_id FROM supplier_payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: VoidSupplierPayment :exec
UPDATE supplier_payments SET status = 'VOIDED', voided_at = @now, voided_by = sqlc.narg(actor_id), void_reason = @reason, void_journal_id = @void_journal_id,
       approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Allocations that make a bill unvoidable: those of payments that are not voided.
-- name: CountLiveAllocations :one
SELECT count(*)::int FROM supplier_payment_allocations a
JOIN supplier_payments x ON x.property_id = a.property_id AND x.id = a.payment_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.bill_id = @bill_id AND x.status = 'POSTED';

-- ---------------------------------------------------------------------------------------------------------------
-- Aging

-- Open bills (not voided, not fully paid) as of a date, for the aging. Payments and voids after the date are ignored, so
-- a past date reproduces what was owed then.
-- name: AgingRows :many
SELECT b.id, b.bill_number, b.supplier_id, s.code AS supplier_code, s.name AS supplier_name, b.supplier_invoice_number, b.bill_date, b.due_date,
       (b.total - COALESCE((SELECT sum(a.amount) FROM supplier_payment_allocations a JOIN supplier_payments x ON x.property_id = a.property_id AND x.id = a.payment_id
                             LEFT JOIN gl_journals pvj ON pvj.property_id = x.property_id AND pvj.id = x.void_journal_id
                            WHERE a.property_id = b.property_id AND a.bill_id = b.id AND x.payment_date <= @as_of::date
                              AND (x.status = 'POSTED' OR pvj.journal_date > @as_of::date)), 0))::numeric AS outstanding
FROM supplier_bills b
JOIN suppliers s ON s.property_id = b.property_id AND s.id = b.supplier_id
LEFT JOIN gl_journals vj ON vj.property_id = b.property_id AND vj.id = b.void_journal_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.bill_date <= @as_of::date
  AND (b.status = 'POSTED' OR vj.journal_date > @as_of::date)
ORDER BY s.name, b.due_date, b.id;

-- name: AccountUsable :one
SELECT is_postable, is_active FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: FindSupplierPaymentByKey :one
SELECT id FROM supplier_payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;
