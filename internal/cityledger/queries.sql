-- Accounts: every company with what was transferred to it and what it paid back (posted rows only).
-- name: ListAccounts :many
SELECT a.id, a.code, a.name, a.credit_limit, a.payment_terms_days, a.is_active, a.transferred, a.received, a.adjusted
FROM (
    SELECT c.id, c.code, c.name, c.credit_limit, c.payment_terms_days, c.is_active,
           COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = c.property_id AND p.company_id = c.id AND p.status = 'POSTED'), 0)::numeric AS transferred,
           COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r WHERE r.property_id = c.property_id AND r.company_id = c.id AND r.status = 'POSTED'), 0)::numeric AS received,
           COALESCE((SELECT sum(a.amount) FROM city_ledger_adjustments a WHERE a.property_id = c.property_id AND a.company_id = c.id AND a.status = 'POSTED'), 0)::numeric AS adjusted
    FROM companies c
    WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.id > @after_id
      AND (sqlc.narg(q)::text IS NULL OR c.code ILIKE '%' || sqlc.narg(q)::text || '%' OR c.name ILIKE '%' || sqlc.narg(q)::text || '%')
) a
WHERE (NOT @owing::boolean OR a.transferred - a.received - a.adjusted > 0)
ORDER BY a.id
LIMIT @row_limit;

-- name: GetAccount :one
SELECT c.id, c.code, c.name, c.credit_limit, c.payment_terms_days, c.is_active, c.email, c.phone, c.address, c.contact_name,
       COALESCE((SELECT sum(p.amount) FROM payments p WHERE p.property_id = c.property_id AND p.company_id = c.id AND p.status = 'POSTED'), 0)::numeric AS transferred,
       COALESCE((SELECT sum(r.amount) FROM city_ledger_receipts r WHERE r.property_id = c.property_id AND r.company_id = c.id AND r.status = 'POSTED'), 0)::numeric AS received,
       COALESCE((SELECT sum(a.amount) FROM city_ledger_adjustments a WHERE a.property_id = c.property_id AND a.company_id = c.id AND a.status = 'POSTED'), 0)::numeric AS adjusted
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
    business_date, paid_at, idempotency_key, created_by, shift_id, mdr_rate, mdr_fee, expected_settlement_date, mdr_vat_rate, mdr_vat
) VALUES (
    @tenant_id, @property_id, @receipt_number, @company_id, @amount, @payment_method, sqlc.narg(reference_number), sqlc.narg(remarks),
    @business_date, @paid_at, sqlc.narg(idempotency_key), sqlc.narg(actor_id), sqlc.narg(shift_id), sqlc.narg(mdr_rate), sqlc.narg(mdr_fee), sqlc.narg(expected_settlement_date), sqlc.narg(mdr_vat_rate), sqlc.narg(mdr_vat)
)
RETURNING *;
-- The rate that applies to a card or e-wallet payment of a business date: the latest rule that has started.
-- name: CardFeeRule :one
SELECT mdr_rate, vat_rate, settlement_days FROM card_fee_rules
WHERE tenant_id = @tenant_id AND property_id = @property_id AND payment_method = @payment_method AND effective_from <= @on_date::date
ORDER BY effective_from DESC
LIMIT 1;


-- name: VoidReceipt :one
UPDATE city_ledger_receipts
SET status = 'VOIDED', voided_at = @now::timestamptz, voided_by = sqlc.narg(actor_id), void_reason = @reason, approved_by = @approved_by
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- Transfers of a company that are not on a live invoice, with the state of the stay behind them. Only a transfer
-- whose guest has checked out can be invoiced. ids narrows the list to the transfers an invoice asks for.
-- name: ListInvoiceCandidates :many
SELECT p.id, p.payment_number, p.business_date, p.amount, p.reference_number,
       COALESCE((SELECT sum(a.amount) FROM city_ledger_adjustments a WHERE a.property_id = p.property_id AND a.payment_id = p.id AND a.status = 'POSTED'
                  AND NOT EXISTS (SELECT 1 FROM city_ledger_adjustment_invoices x WHERE x.property_id = a.property_id AND x.adjustment_id = a.id AND x.released_at IS NULL)), 0)::numeric AS credited,
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


-- ---------------------------------------------------------------------------------------------------------------
-- Credit notes and write-offs

-- name: InsertAdjustment :one
INSERT INTO city_ledger_adjustments (tenant_id, property_id, adjustment_number, kind, company_id, invoice_id, payment_id, amount, business_date, reason, debit_account_id,
                                     journal_id, approved_by, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @adjustment_number, @kind, @company_id, sqlc.narg(invoice_id), sqlc.narg(payment_id), @amount, @business_date, @reason, sqlc.narg(debit_account_id),
        @journal_id, sqlc.narg(approved_by), sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING *;

-- name: InsertCreditNoteLine :exec
INSERT INTO city_ledger_credit_note_lines (tenant_id, property_id, adjustment_id, line_no, description, account_id, net_amount, tax_id, tax_rate, tax_amount)
VALUES (@tenant_id, @property_id, @adjustment_id, @line_no, @description, @account_id, @net_amount, sqlc.narg(tax_id), sqlc.narg(tax_rate), @tax_amount);

-- name: GetAdjustment :one
SELECT * FROM city_ledger_adjustments WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetAdjustmentByKey :one
SELECT * FROM city_ledger_adjustments WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ListAdjustments :many
SELECT a.id, a.adjustment_number, a.kind, a.company_id, a.invoice_id, i.invoice_number, a.payment_id, p.payment_number, a.amount, a.business_date, a.reason,
       a.debit_account_id, da.code AS debit_account_code, a.status, a.journal_id, jn.journal_number, a.voided_at, a.void_reason, a.approved_by, a.created_by, a.created_at,
       COALESCE((SELECT x.invoice_id FROM city_ledger_adjustment_invoices x WHERE x.property_id = a.property_id AND x.adjustment_id = a.id AND x.released_at IS NULL), 0)::bigint AS attached_invoice_id
FROM city_ledger_adjustments a
JOIN gl_journals jn ON jn.property_id = a.property_id AND jn.id = a.journal_id
LEFT JOIN city_ledger_invoices i ON i.property_id = a.property_id AND i.id = a.invoice_id
LEFT JOIN payments p ON p.property_id = a.property_id AND p.id = a.payment_id
LEFT JOIN gl_accounts da ON da.property_id = a.property_id AND da.id = a.debit_account_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR a.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(company_id)::bigint IS NULL OR a.company_id = sqlc.narg(company_id)::bigint)
ORDER BY a.business_date DESC, a.id DESC;

-- name: ListCreditNoteLines :many
SELECT l.adjustment_id, l.line_no, l.description, l.account_id, ga.code AS account_code, ga.name AS account_name, l.net_amount, l.tax_id, t.code AS tax_code, l.tax_rate, l.tax_amount
FROM city_ledger_credit_note_lines l
JOIN gl_accounts ga ON ga.property_id = l.property_id AND ga.id = l.account_id
LEFT JOIN taxes t ON t.property_id = l.property_id AND t.id = l.tax_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.adjustment_id = ANY (@adjustment_ids::bigint[])
ORDER BY l.adjustment_id, l.line_no;

-- name: VoidAdjustment :exec
UPDATE city_ledger_adjustments SET status = 'VOIDED', voided_at = @now::timestamptz, voided_by = sqlc.narg(actor_id), void_reason = @reason, void_journal_id = @void_journal_id, approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- What posted credit notes and write-offs have taken off invoices (those made against the invoice itself).
-- name: InvoiceAdjusted :many
SELECT a.invoice_id, COALESCE(sum(a.amount) FILTER (WHERE a.kind = 'CREDIT_NOTE'), 0)::numeric AS credited, COALESCE(sum(a.amount) FILTER (WHERE a.kind = 'WRITE_OFF'), 0)::numeric AS written_off
FROM city_ledger_adjustments a
WHERE a.property_id = @property_id AND a.status = 'POSTED' AND a.invoice_id = ANY (@invoice_ids::bigint[])
GROUP BY a.invoice_id;

-- All the credit notes posted against a transfer, attached to an invoice or not.
-- name: TransferCredited :one
SELECT COALESCE(sum(amount), 0)::numeric AS credited FROM city_ledger_adjustments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND payment_id = @payment_id AND status = 'POSTED';

-- The credit notes of transfers that no invoice has taken yet.
-- name: UnattachedTransferNotes :many
SELECT a.id, a.payment_id, a.amount FROM city_ledger_adjustments a
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.payment_id = ANY (@payment_ids::bigint[]) AND a.status = 'POSTED'
  AND NOT EXISTS (SELECT 1 FROM city_ledger_adjustment_invoices x WHERE x.property_id = a.property_id AND x.adjustment_id = a.id AND x.released_at IS NULL)
ORDER BY a.id;

-- name: InsertAttachment :exec
INSERT INTO city_ledger_adjustment_invoices (tenant_id, property_id, adjustment_id, invoice_id) VALUES (@tenant_id, @property_id, @adjustment_id, @invoice_id);

-- name: ReleaseAttachments :exec
UPDATE city_ledger_adjustment_invoices SET released_at = @now::timestamptz WHERE property_id = @property_id AND invoice_id = @invoice_id AND released_at IS NULL;

-- name: AttachedNotes :many
SELECT a.id, a.adjustment_number, a.amount, p.payment_number
FROM city_ledger_adjustment_invoices x
JOIN city_ledger_adjustments a ON a.property_id = x.property_id AND a.id = x.adjustment_id
JOIN payments p ON p.property_id = a.property_id AND p.id = a.payment_id
WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id AND x.invoice_id = @invoice_id AND x.released_at IS NULL
ORDER BY a.id;

-- name: CountLiveAdjustmentsOfInvoice :one
SELECT count(*)::int FROM city_ledger_adjustments WHERE tenant_id = @tenant_id AND property_id = @property_id AND invoice_id = @invoice_id AND status = 'POSTED';

-- A transfer to a company, as a credit note needs to know it.
-- name: GetTransfer :one
SELECT id, payment_number, company_id, amount, status, payment_type, payment_method FROM payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: TransferOnLiveInvoice :one
SELECT count(*)::int FROM city_ledger_invoice_lines WHERE property_id = @property_id AND payment_id = @payment_id AND released_at IS NULL;

-- name: TaxForCreditNote :one
SELECT id, code, rate, is_active, gl_account_code, tax_kind FROM taxes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The tax invoices (faktur pajak) of an invoice that are not void.
-- name: CountLiveTaxInvoices :one
SELECT count(*)::int FROM tax_invoices WHERE tenant_id = @tenant_id AND property_id = @property_id AND city_ledger_invoice_id = @invoice_id AND status = 'ISSUED';

-- ---------------------------------------------------------------------------------------------------------------
-- Overdue invoices, reminders and the late fee

-- Issued invoices whose due date is before a date, with what is taken off them (the amounts that are still owed are worked out by the caller).
-- name: ListOverdueInvoices :many
SELECT i.id, i.invoice_number, i.company_id, c.code AS company_code, c.name AS company_name, i.invoice_date, i.due_date, i.total,
       COALESCE((SELECT sum(a.amount) FROM city_ledger_receipt_allocations a JOIN city_ledger_receipts r ON r.property_id = a.property_id AND r.id = a.receipt_id AND r.status = 'POSTED'
                  WHERE a.property_id = i.property_id AND a.invoice_id = i.id), 0)::numeric AS paid,
       COALESCE((SELECT sum(x.amount) FROM city_ledger_adjustments x WHERE x.property_id = i.property_id AND x.invoice_id = i.id AND x.status = 'POSTED'), 0)::numeric AS adjusted
FROM city_ledger_invoices i
JOIN companies c ON c.property_id = i.property_id AND c.id = i.company_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.status = 'ISSUED' AND i.due_date < @as_of::date
  AND (sqlc.narg(company_id)::bigint IS NULL OR i.company_id = sqlc.narg(company_id)::bigint)
ORDER BY c.code, i.due_date, i.id;

-- The latest reminder that listed each of the invoices.
-- name: LastRemindersOfInvoices :many
SELECT DISTINCT ON (it.invoice_id) it.invoice_id, m.reminder_number, m.reminder_date, m.level
FROM city_ledger_reminder_items it
JOIN city_ledger_reminders m ON m.property_id = it.property_id AND m.id = it.reminder_id
WHERE it.tenant_id = @tenant_id AND it.property_id = @property_id AND it.invoice_id = ANY (@invoice_ids::bigint[])
ORDER BY it.invoice_id, m.id DESC;

-- name: InsertReminder :one
INSERT INTO city_ledger_reminders (tenant_id, property_id, reminder_number, company_id, level, reminder_date, note, total_outstanding, total_interest, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @reminder_number, @company_id, @level, @reminder_date, sqlc.narg(note), @total_outstanding, @total_interest, sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING *;

-- name: InsertReminderItem :exec
INSERT INTO city_ledger_reminder_items (tenant_id, property_id, reminder_id, company_id, invoice_id, outstanding, days_overdue, interest)
VALUES (@tenant_id, @property_id, @reminder_id, @company_id, @invoice_id, @outstanding, @days_overdue, @interest);

-- name: ListReminders :many
SELECT * FROM city_ledger_reminders
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(company_id)::bigint IS NULL OR company_id = sqlc.narg(company_id)::bigint)
ORDER BY reminder_date DESC, id DESC;

-- name: ListReminderItems :many
SELECT it.reminder_id, it.invoice_id, i.invoice_number, i.invoice_date, i.due_date, it.outstanding, it.days_overdue, it.interest
FROM city_ledger_reminder_items it
JOIN city_ledger_invoices i ON i.property_id = it.property_id AND i.id = it.invoice_id
WHERE it.tenant_id = @tenant_id AND it.property_id = @property_id AND it.reminder_id = ANY (@reminder_ids::bigint[])
ORDER BY it.reminder_id, i.due_date, i.id;

-- name: GetReminderByKey :one
SELECT * FROM city_ledger_reminders WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: GetLateFee :one
SELECT monthly_rate, grace_days FROM city_ledger_late_fee_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: UpsertLateFee :exec
INSERT INTO city_ledger_late_fee_settings (tenant_id, property_id, monthly_rate, grace_days, updated_by)
VALUES (@tenant_id, @property_id, @monthly_rate, @grace_days, sqlc.narg(actor_id))
ON CONFLICT (property_id) DO UPDATE SET monthly_rate = EXCLUDED.monthly_rate, grace_days = EXCLUDED.grace_days, updated_by = EXCLUDED.updated_by;
