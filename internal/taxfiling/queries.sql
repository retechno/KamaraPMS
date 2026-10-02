-- Tax filing (sqlc): filing profiles, monthly returns with their worksheet lines, and tax payments. Every query is scoped by
-- tenant_id and property_id.

-- ---------------------------------------------------------------------------------------------------------------
-- Profiles

-- name: ListProfiles :many
SELECT p.id, p.tax_id, t.code AS tax_code, t.name AS tax_name, t.rate AS tax_rate, t.gl_account_code, p.authority, p.registration_number, p.due_day, p.is_active, p.created_at
FROM tax_filing_profiles p
JOIN taxes t ON t.property_id = p.property_id AND t.id = p.tax_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR p.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(tax_id)::bigint IS NULL OR p.tax_id = sqlc.narg(tax_id)::bigint)
ORDER BY t.code, p.id;

-- name: InsertProfile :one
INSERT INTO tax_filing_profiles (tenant_id, property_id, tax_id, authority, registration_number, due_day, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @tax_id, @authority, sqlc.narg(registration_number), @due_day, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateProfile :exec
UPDATE tax_filing_profiles SET authority = @authority, registration_number = sqlc.narg(registration_number), due_day = @due_day, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: TaxOfProperty :one
SELECT id, code, is_active FROM taxes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ProfileOfReturn :one
SELECT p.id FROM tax_filing_profiles p JOIN tax_returns r ON r.property_id = p.property_id AND r.tax_id = p.tax_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @return_id;

-- ---------------------------------------------------------------------------------------------------------------
-- The worksheet of a month

-- Tax collected in a range by charge code and rate, from the snapshots of the folio items (a reversal carries the code of
-- the item it reverses and negative amounts).
-- name: WorksheetLines :many
SELECT COALESCE(cc.code, occ.code, '')::text AS charge_code, COALESCE(cc.name, occ.name, '')::text AS charge_name, k.rate,
       count(DISTINCT i.id)::int AS items, sum(k.base_amount)::numeric AS base, sum(k.amount)::numeric AS tax
FROM folio_item_components k
JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
LEFT JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
LEFT JOIN folio_items o ON o.property_id = i.property_id AND o.id = i.reverses_item_id
LEFT JOIN charge_codes occ ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.component_type = 'TAX' AND k.tax_id = @tax_id
  AND i.business_date BETWEEN @from_date::date AND @to_date::date
GROUP BY COALESCE(cc.code, occ.code, ''), COALESCE(cc.name, occ.name, ''), k.rate
ORDER BY 1, k.rate;

-- What the day close journals credited for a tax in a range (the lines carry the tax code as their source).
-- name: GLCollected :one
SELECT COALESCE(sum(l.credit - l.debit), 0)::numeric AS collected
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.source_type = 'TAX' AND l.source_ref = @tax_code
  AND j.journal_type = 'DAY_CLOSE' AND j.journal_date BETWEEN @from_date::date AND @to_date::date;

-- name: AccountingStart :one
SELECT start_date FROM accounting_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- Days of a range that are closed business days with their journal.
-- name: CountPostedDays :one
SELECT count(*)::int FROM business_days b
JOIN gl_day_posts d ON d.property_id = b.property_id AND d.business_date = b.business_date
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'CLOSED' AND b.business_date BETWEEN @from_date::date AND @to_date::date;

-- name: CollectedBetween :one
SELECT COALESCE(sum(k.amount), 0)::numeric AS tax
FROM folio_item_components k
JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.component_type = 'TAX' AND k.tax_id = @tax_id
  AND i.business_date BETWEEN @from_date::date AND @to_date::date;

-- ---------------------------------------------------------------------------------------------------------------
-- Returns

-- name: InsertReturn :one
INSERT INTO tax_returns (tenant_id, property_id, return_number, tax_id, period_start, period_end, due_date, base_amount, tax_amount, filed_on, filing_reference, notes,
                         filed_at, filed_by, idempotency_key)
VALUES (@tenant_id, @property_id, @return_number, @tax_id, @period_start, @period_end, @due_date, @base_amount, @tax_amount, @filed_on, sqlc.narg(filing_reference),
        sqlc.narg(notes), @now, sqlc.narg(actor_id), sqlc.narg(idempotency_key))
RETURNING id;

-- name: InsertReturnLine :exec
INSERT INTO tax_return_lines (tenant_id, property_id, return_id, line_no, charge_code, charge_name, rate, items, base_amount, tax_amount)
VALUES (@tenant_id, @property_id, @return_id, @line_no, @charge_code, sqlc.narg(charge_name), @rate, @items, @base_amount, @tax_amount);

-- name: ListReturns :many
SELECT r.id, r.return_number, r.tax_id, t.code AS tax_code, t.name AS tax_name, r.period_start, r.period_end, r.due_date, r.base_amount, r.tax_amount, r.status, r.filed_on,
       r.filing_reference, r.notes, r.filed_at, r.voided_at, r.void_reason,
       COALESCE((SELECT sum(x.amount) FROM tax_payments x WHERE x.property_id = r.property_id AND x.return_id = r.id AND x.status = 'POSTED'), 0)::numeric AS paid
FROM tax_returns r
JOIN taxes t ON t.property_id = r.property_id AND t.id = r.tax_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR r.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(tax_id)::bigint IS NULL OR r.tax_id = sqlc.narg(tax_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
ORDER BY r.period_start DESC, r.id DESC
LIMIT @row_limit;

-- name: ListReturnLines :many
SELECT line_no, charge_code, charge_name, rate, items, base_amount, tax_amount FROM tax_return_lines
WHERE tenant_id = @tenant_id AND property_id = @property_id AND return_id = @return_id ORDER BY line_no;

-- name: FindReturnByKey :one
SELECT id FROM tax_returns WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: LiveReturnOfPeriod :one
SELECT id FROM tax_returns WHERE tenant_id = @tenant_id AND property_id = @property_id AND tax_id = @tax_id AND period_start = @period_start AND status = 'FILED';

-- name: VoidReturn :exec
UPDATE tax_returns SET status = 'VOIDED', voided_at = @now, voided_by = sqlc.narg(actor_id), void_reason = @reason, approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: CountLivePayments :one
SELECT count(*)::int FROM tax_payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND return_id = @return_id AND status = 'POSTED';

-- ---------------------------------------------------------------------------------------------------------------
-- Payments

-- name: InsertTaxPayment :one
INSERT INTO tax_payments (tenant_id, property_id, payment_number, return_id, payment_date, amount, penalty, payment_method, reference_number, remarks, journal_id, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @payment_number, @return_id, @payment_date, @amount, @penalty, @payment_method, sqlc.narg(reference_number), sqlc.narg(remarks), @journal_id,
        sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: ListTaxPayments :many
SELECT x.id, x.payment_number, x.return_id, r.return_number, t.code AS tax_code, t.name AS tax_name, r.period_start, x.payment_date, x.amount, x.penalty, x.payment_method,
       x.reference_number, x.remarks, x.status, x.journal_id, jn.journal_number, x.void_journal_id, x.voided_at, x.void_reason, x.created_at
FROM tax_payments x
JOIN tax_returns r ON r.property_id = x.property_id AND r.id = x.return_id
JOIN taxes t ON t.property_id = r.property_id AND t.id = r.tax_id
JOIN gl_journals jn ON jn.property_id = x.property_id AND jn.id = x.journal_id
WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR x.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(return_id)::bigint IS NULL OR x.return_id = sqlc.narg(return_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR x.status = sqlc.narg(status)::text)
ORDER BY x.payment_date DESC, x.id DESC
LIMIT @row_limit;

-- name: FindTaxPaymentByKey :one
SELECT id FROM tax_payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ReturnOfPayment :one
SELECT return_id FROM tax_payments WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: VoidTaxPayment :exec
UPDATE tax_payments SET status = 'VOIDED', voided_at = @now, voided_by = sqlc.narg(actor_id), void_reason = @reason, void_journal_id = @void_journal_id, approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: AccountByCode :one
SELECT id, account_type, is_postable, is_active FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- ---------------------------------------------------------------------------------------------------------------
-- Liability

-- Tax collected on folios up to a date, by tax.
-- name: CollectedToDate :many
SELECT k.tax_id, sum(k.amount)::numeric AS tax
FROM folio_item_components k
JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.component_type = 'TAX' AND i.business_date <= @as_of::date
GROUP BY k.tax_id;

-- Returns filed (not voided, or voided after the date) with a period ended by a date, and what was paid on them by it.
-- name: FiledAndPaidToDate :many
SELECT r.tax_id, COALESCE(sum(r.tax_amount), 0)::numeric AS filed, count(*)::int AS returns,
       COALESCE(sum((SELECT COALESCE(sum(x.amount), 0) FROM tax_payments x LEFT JOIN gl_journals xv ON xv.property_id = x.property_id AND xv.id = x.void_journal_id
                      WHERE x.property_id = r.property_id AND x.return_id = r.id AND x.payment_date <= @as_of::date AND (x.status = 'POSTED' OR xv.journal_date > @as_of::date))), 0)::numeric AS paid
FROM tax_returns r
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.filed_on <= @as_of::date AND (r.status = 'FILED' OR r.voided_at::date > @as_of::date)
GROUP BY r.tax_id;

-- The account of the books as of a date: credit balance (what is owed).
-- name: AccountCreditBalance :one
SELECT COALESCE(sum(l.credit - l.debit), 0)::numeric AS balance
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND a.code = @code AND j.journal_date <= @as_of::date;

-- name: MapAccountCode :one
SELECT a.code FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = @map_key;

-- What was paid on each return up to a date (a payment voided after the date still counts).
-- name: PaidByReturnToDate :many
SELECT x.return_id, COALESCE(sum(x.amount), 0)::numeric AS paid
FROM tax_payments x LEFT JOIN gl_journals xv ON xv.property_id = x.property_id AND xv.id = x.void_journal_id
WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id AND x.payment_date <= @as_of::date AND (x.status = 'POSTED' OR xv.journal_date > @as_of::date)
GROUP BY x.return_id;
