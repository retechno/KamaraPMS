-- Tax invoices (sqlc): the invoices with their lines, the sources they are issued for, the exports and the coverage of the VAT collected.
-- Every query is scoped by tenant_id and property_id.

-- ---------------------------------------------------------------------------------------------------------------
-- Invoices

-- name: InsertInvoice :one
INSERT INTO tax_invoices (tenant_id, property_id, invoice_ref, issue_date, source_type, city_ledger_invoice_id, folio_id, seller_name, seller_npwp, seller_pkp_number,
                          seller_address, signer_name, signer_title, buyer_name, buyer_npwp, buyer_address, taxable_base, vat_amount, replaces_invoice_id,
                          idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @invoice_ref, @issue_date, @source_type, sqlc.narg(city_ledger_invoice_id), sqlc.narg(folio_id), @seller_name, @seller_npwp,
        sqlc.narg(seller_pkp_number), sqlc.narg(seller_address), sqlc.narg(signer_name), sqlc.narg(signer_title), @buyer_name, @buyer_npwp, sqlc.narg(buyer_address),
        @taxable_base, @vat_amount, sqlc.narg(replaces_invoice_id), sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertInvoiceLine :exec
INSERT INTO tax_invoice_lines (tenant_id, property_id, invoice_id, line_no, charge_code, description, base_amount, rate, vat_amount)
VALUES (@tenant_id, @property_id, @invoice_id, @line_no, @charge_code, @description, @base_amount, @rate, @vat_amount);

-- name: ListInvoices :many
SELECT t.id, t.invoice_ref, t.status, t.issue_date, t.source_type, t.city_ledger_invoice_id, t.folio_id, cli.invoice_number AS city_ledger_invoice_number, f.folio_number,
       t.seller_name, t.seller_npwp, t.seller_pkp_number, t.seller_address, t.signer_name, t.signer_title, t.buyer_name, t.buyer_npwp, t.buyer_address,
       t.taxable_base, t.vat_amount, t.djp_number, t.replaces_invoice_id, t.voided_at, t.void_reason, t.created_at
FROM tax_invoices t
LEFT JOIN city_ledger_invoices cli ON cli.property_id = t.property_id AND cli.id = t.city_ledger_invoice_id
LEFT JOIN folios f ON f.property_id = t.property_id AND f.id = t.folio_id
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR t.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
  AND (sqlc.narg(from_date)::date IS NULL OR t.issue_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR t.issue_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(q)::text IS NULL OR t.invoice_ref ILIKE '%' || sqlc.narg(q)::text || '%' OR t.buyer_name ILIKE '%' || sqlc.narg(q)::text || '%'
       OR t.djp_number ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY t.issue_date DESC, t.id DESC
LIMIT @row_limit;

-- name: ListInvoiceLines :many
SELECT invoice_id, line_no, charge_code, description, base_amount, rate, vat_amount FROM tax_invoice_lines
WHERE tenant_id = @tenant_id AND property_id = @property_id AND invoice_id = ANY (@invoice_ids::bigint[])
ORDER BY invoice_id, line_no;

-- name: FindInvoiceByKey :one
SELECT id FROM tax_invoices WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: VoidInvoice :exec
UPDATE tax_invoices SET status = 'VOIDED', voided_at = @now, voided_by = sqlc.narg(actor_id), void_reason = @reason, approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetDJPNumber :exec
UPDATE tax_invoices SET djp_number = @djp_number WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------
-- Sources

-- name: CityLedgerInvoiceSource :one
SELECT i.id, i.invoice_number, i.status, c.id AS company_id, c.name AS company_name, c.tax_id AS company_tax_id, c.address AS company_address, c.city AS company_city
FROM city_ledger_invoices i
JOIN companies c ON c.property_id = i.property_id AND c.id = i.company_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = @id;

-- The folios behind a city ledger invoice, what of each was transferred onto it and what the credit notes of those transfers that the invoice took off
-- when it was made came to.
-- name: CityLedgerTransfers :many
SELECT p.folio_id, sum(l.amount)::numeric AS amount,
       COALESCE(sum((SELECT sum(a.amount) FROM city_ledger_adjustments a JOIN city_ledger_adjustment_invoices x ON x.property_id = a.property_id AND x.adjustment_id = a.id AND x.released_at IS NULL AND x.invoice_id = l.invoice_id
                      WHERE a.property_id = l.property_id AND a.payment_id = l.payment_id AND a.status = 'POSTED')), 0)::numeric AS credited
FROM city_ledger_invoice_lines l
JOIN payments p ON p.property_id = l.property_id AND p.id = l.payment_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.invoice_id = @invoice_id AND l.released_at IS NULL
GROUP BY p.folio_id
ORDER BY p.folio_id;

-- What the credit notes made against the invoice itself took off (a write-off does not change what was supplied).
-- name: CityLedgerDirectCredited :one
SELECT COALESCE(sum(amount), 0)::numeric AS credited FROM city_ledger_adjustments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND invoice_id = @invoice_id AND kind = 'CREDIT_NOTE' AND status = 'POSTED';

-- name: FolioSource :one
SELECT id, folio_number, status FROM folios WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The VAT of a folio by charge code and rate, net of reversals (a reversal carries the code of the item it reverses).
-- name: FolioVATLines :many
SELECT COALESCE(cc.code, occ.code, '')::text AS charge_code, COALESCE(cc.name, occ.name, '')::text AS charge_name, k.rate,
       sum(k.base_amount)::numeric AS base, sum(k.amount)::numeric AS vat
FROM folio_item_components k
JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
JOIN taxes t ON t.property_id = k.property_id AND t.id = k.tax_id AND t.tax_kind = 'VAT'
LEFT JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
LEFT JOIN folio_items o ON o.property_id = i.property_id AND o.id = i.reverses_item_id
LEFT JOIN charge_codes occ ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.component_type = 'TAX' AND i.folio_id = @folio_id
GROUP BY COALESCE(cc.code, occ.code, ''), COALESCE(cc.name, occ.name, ''), k.rate
HAVING sum(k.amount) <> 0
ORDER BY 1, k.rate;

-- What was charged on a folio, with its taxes and service charges (payments excluded).
-- name: FolioCharged :one
SELECT COALESCE(sum(debit - credit), 0)::numeric AS charged FROM folio_items
WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_id = @folio_id AND transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL');

-- ---------------------------------------------------------------------------------------------------------------
-- Coverage of the VAT collected

-- name: VATCollectedBetween :one
SELECT COALESCE(sum(k.amount), 0)::numeric AS vat
FROM folio_item_components k
JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
JOIN taxes t ON t.property_id = k.property_id AND t.id = k.tax_id AND t.tax_kind = 'VAT'
WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.component_type = 'TAX' AND i.business_date BETWEEN @from_date::date AND @to_date::date;

-- name: VATInvoicedBetween :one
SELECT COALESCE(sum(vat_amount), 0)::numeric AS vat FROM tax_invoices
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ISSUED' AND issue_date BETWEEN @from_date::date AND @to_date::date;

-- Folios that carry VAT in the range and are on no live tax invoice, directly or through a city ledger invoice.
-- name: FoliosWithoutInvoice :many
SELECT f.id AS folio_id, f.folio_number, f.status, sum(k.amount)::numeric AS vat
FROM folio_item_components k
JOIN folio_items i ON i.property_id = k.property_id AND i.id = k.folio_item_id
JOIN taxes t ON t.property_id = k.property_id AND t.id = k.tax_id AND t.tax_kind = 'VAT'
JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id
WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.component_type = 'TAX' AND i.business_date BETWEEN @from_date::date AND @to_date::date
  AND NOT EXISTS (SELECT 1 FROM tax_invoices x WHERE x.property_id = f.property_id AND x.folio_id = f.id AND x.status = 'ISSUED')
  AND NOT EXISTS (SELECT 1 FROM tax_invoices x
                  JOIN city_ledger_invoice_lines l ON l.property_id = x.property_id AND l.invoice_id = x.city_ledger_invoice_id AND l.released_at IS NULL
                  JOIN payments p ON p.property_id = l.property_id AND p.id = l.payment_id
                  WHERE x.property_id = f.property_id AND x.status = 'ISSUED' AND p.folio_id = f.id)
GROUP BY f.id, f.folio_number, f.status
HAVING sum(k.amount) > 0
ORDER BY f.folio_number
LIMIT 200;

-- ---------------------------------------------------------------------------------------------------------------
-- Exports

-- name: InsertExport :one
INSERT INTO tax_invoice_exports (tenant_id, property_id, format, period_start, period_end, invoice_count, file_name, sha256, created_by)
VALUES (@tenant_id, @property_id, @format, @period_start, @period_end, @invoice_count, @file_name, @sha256, sqlc.narg(actor_id))
RETURNING id;

-- name: InsertExportItem :exec
INSERT INTO tax_invoice_export_items (tenant_id, property_id, export_id, invoice_id) VALUES (@tenant_id, @property_id, @export_id, @invoice_id);

-- name: ListExports :many
SELECT id, format, period_start, period_end, invoice_count, file_name, sha256, created_at FROM tax_invoice_exports
WHERE tenant_id = @tenant_id AND property_id = @property_id ORDER BY id DESC LIMIT @row_limit;
