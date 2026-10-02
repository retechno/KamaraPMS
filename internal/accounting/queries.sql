-- Accounting (sqlc): chart of accounts, the system account map and journals. Every query is scoped by tenant_id and property_id.

-- name: SeedChart :one
SELECT seed_chart_of_accounts(@tenant_id, @property_id, sqlc.narg(actor_id), @start_date::date)::int AS created;

-- name: GetSettings :one
SELECT * FROM accounting_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- An account with its parent's code and whether anything refers to it (children, the system map, a charge code, tax or
-- service charge that carries its code), which is what stops it from being deleted or deactivated.
-- name: ListAccounts :many
SELECT a.id, a.code, a.name, a.account_type, a.normal_side, a.parent_id, p.code AS parent_code, a.is_postable, a.is_active, a.statement_group,
       a.description, a.created_at,
       (EXISTS (SELECT 1 FROM gl_accounts c WHERE c.property_id = a.property_id AND c.parent_id = a.id)
        OR EXISTS (SELECT 1 FROM gl_account_map m WHERE m.property_id = a.property_id AND m.account_id = a.id)
        OR EXISTS (SELECT 1 FROM charge_codes cc WHERE cc.property_id = a.property_id AND cc.gl_account_code = a.code)
        OR EXISTS (SELECT 1 FROM taxes t WHERE t.property_id = a.property_id AND t.gl_account_code = a.code)
        OR EXISTS (SELECT 1 FROM service_charges s WHERE s.property_id = a.property_id AND s.gl_account_code = a.code))::boolean AS in_use
FROM gl_accounts a
LEFT JOIN gl_accounts p ON p.property_id = a.property_id AND p.id = a.parent_id
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR a.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(account_type)::text IS NULL OR a.account_type = sqlc.narg(account_type)::text)
  AND (sqlc.narg(statement_group)::text IS NULL OR a.statement_group = sqlc.narg(statement_group)::text)
  AND (sqlc.narg(active)::boolean IS NULL OR a.is_active = sqlc.narg(active)::boolean)
  AND (sqlc.narg(postable)::boolean IS NULL OR a.is_postable = sqlc.narg(postable)::boolean)
  AND (sqlc.narg(q)::text IS NULL OR a.code ILIKE '%' || sqlc.narg(q)::text || '%' OR a.name ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY a.code
LIMIT @row_limit;

-- name: GetAccountByCode :one
SELECT * FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: GetAccountRow :one
SELECT * FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: CreateAccount :one
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, is_active, statement_group, description, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @account_type, @normal_side, sqlc.narg(parent_id), @is_postable, @is_active, sqlc.narg(statement_group), sqlc.narg(description),
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateAccount :exec
UPDATE gl_accounts
SET name = @name, parent_id = sqlc.narg(parent_id), is_postable = @is_postable, is_active = @is_active, statement_group = sqlc.narg(statement_group),
    description = sqlc.narg(description), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteAccount :exec
DELETE FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- Accounts that stand above an account, to refuse a parent that would make a loop.
-- name: AncestorIDs :many
WITH RECURSIVE up AS (
    SELECT g.id, g.parent_id, 1 AS depth FROM gl_accounts g WHERE g.property_id = @property_id AND g.id = @id
    UNION ALL
    SELECT a.id, a.parent_id, up.depth + 1 FROM gl_accounts a JOIN up ON a.property_id = @property_id AND a.id = up.parent_id WHERE up.depth < 20
)
SELECT id FROM up;

-- name: CountChildren :one
SELECT count(*)::int FROM gl_accounts g WHERE g.property_id = @property_id AND g.parent_id = @id;

-- name: CountAccountReferences :one
SELECT ((SELECT count(*) FROM gl_account_map m WHERE m.property_id = @property_id AND m.account_id = @id)
      + (SELECT count(*) FROM charge_codes c WHERE c.property_id = @property_id AND c.gl_account_code = @code)
      + (SELECT count(*) FROM taxes t WHERE t.property_id = @property_id AND t.gl_account_code = @code)
      + (SELECT count(*) FROM service_charges s WHERE s.property_id = @property_id AND s.gl_account_code = @code))::int AS refs;

-- name: ListAccountMap :many
SELECT m.map_key, m.account_id, a.code AS account_code, a.name AS account_name, a.account_type, m.updated_at
FROM gl_account_map m
JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id
ORDER BY m.map_key;

-- name: SetAccountMap :exec
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id, updated_by)
VALUES (@tenant_id, @property_id, @map_key, @account_id, sqlc.narg(actor_id))
ON CONFLICT (property_id, map_key) DO UPDATE SET account_id = EXCLUDED.account_id, updated_by = EXCLUDED.updated_by, updated_at = now();

-- What carries an account code and whether the code is a usable account of the chart: charge codes, taxes and service
-- charges. A code that is empty or names no active postable account of the right kind is posted to a fallback account.
-- name: ListCodeUsage :many
SELECT u.kind, u.id, u.code, u.name, u.is_active, u.gl_account_code,
       a.id AS account_id, a.is_active AS account_active, a.is_postable AS account_postable, a.account_type
FROM (
    SELECT 'CHARGE_CODE'::text AS kind, c.id, c.code, c.name, c.is_active, c.gl_account_code FROM charge_codes c
    WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
    UNION ALL
    SELECT 'TAX', t.id, t.code, t.name, t.is_active, t.gl_account_code FROM taxes t
    WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
    UNION ALL
    SELECT 'SERVICE_CHARGE', s.id, s.code, s.name, s.is_active, s.gl_account_code FROM service_charges s
    WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
) u
LEFT JOIN gl_accounts a ON a.property_id = @property_id AND a.code = u.gl_account_code
ORDER BY u.kind, u.code;

-- ---------------------------------------------------------------------------------------------------------------
-- Journals

-- What happened on a business date, as signed amounts (debit positive) against a role: GUEST_LEDGER, ADVANCE_DEPOSITS and
-- CITY_LEDGER are system accounts, METHOD is the payment method received into (key), REVENUE the charge code's revenue
-- account (key), TAX and SERVICE the component's account (key). The service resolves roles to accounts.
-- name: DayActivity :many
SELECT x.role::text AS role, x.key::text AS key, x.source_type::text AS source_type, x.source_ref::text AS source_ref, x.amount::numeric AS amount, x.detail::text AS detail
FROM (
    SELECT 'GUEST_LEDGER' AS role, '' AS key, 'CHARGE_CODE' AS source_type, COALESCE(g.charge_code, 'UNKNOWN') AS source_ref, sum(g.signed_amount) AS amount, '' AS detail
      FROM folio_item_gl g
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date = @business_date AND g.kind = 'CHARGE'
     GROUP BY COALESCE(g.charge_code, 'UNKNOWN')
    UNION ALL
    SELECT 'REVENUE', COALESCE(g.revenue_account_code, ''), 'CHARGE_CODE', COALESCE(g.charge_code, 'UNKNOWN'), -sum(g.net_amount), ''
      FROM folio_item_gl g
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date = @business_date AND g.kind = 'CHARGE'
     GROUP BY COALESCE(g.revenue_account_code, ''), COALESCE(g.charge_code, 'UNKNOWN')
    UNION ALL
    SELECT CASE c.component_type WHEN 'TAX' THEN 'TAX' ELSE 'SERVICE' END, COALESCE(c.gl_account_code, ''),
           CASE c.component_type WHEN 'TAX' THEN 'TAX' ELSE 'SERVICE_CHARGE' END, c.code, -sum(c.amount), ''
      FROM folio_item_gl g
      JOIN folio_item_components c ON c.property_id = g.property_id AND c.folio_item_id = g.item_id
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date = @business_date AND g.kind = 'CHARGE'
     GROUP BY c.component_type, COALESCE(c.gl_account_code, ''), c.code
    UNION ALL
    SELECT CASE WHEN g.is_deposit THEN 'ADVANCE_DEPOSITS' ELSE 'GUEST_LEDGER' END, '', 'PAYMENT', g.payment_method, sum(g.signed_amount), ''
      FROM folio_item_gl g
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date = @business_date AND g.kind = 'PAYMENT'
     GROUP BY g.is_deposit, g.payment_method
    UNION ALL
    SELECT 'METHOD', g.payment_method, 'PAYMENT', COALESCE(g.payment_number, g.payment_method), -sum(g.signed_amount), COALESCE(g.payment_reference, '')
      FROM folio_item_gl g
     WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.business_date = @business_date AND g.kind = 'PAYMENT'
     GROUP BY g.payment_method, COALESCE(g.payment_number, g.payment_method), COALESCE(g.payment_reference, '')
    UNION ALL
    SELECT 'METHOD', r.payment_method, 'RECEIPT', r.receipt_number, sum(r.amount), COALESCE(r.reference_number, '')
      FROM city_ledger_receipts r
     WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.business_date = @business_date AND r.status = 'POSTED'
     GROUP BY r.payment_method, r.receipt_number, COALESCE(r.reference_number, '')
    UNION ALL
    SELECT 'CITY_LEDGER', '', 'RECEIPT', r.payment_method, -sum(r.amount), ''
      FROM city_ledger_receipts r
     WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.business_date = @business_date AND r.status = 'POSTED'
     GROUP BY r.payment_method
    UNION ALL
    SELECT 'ADVANCE_DEPOSITS', '', 'DEPOSIT_RELEASE', f.folio_number, -sum(g.signed_amount), ''
      FROM folios f
      JOIN folio_item_gl g ON g.property_id = f.property_id AND g.folio_id = f.id AND g.kind = 'PAYMENT' AND g.is_deposit AND g.business_date <= @business_date
     WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.closed_on = @business_date
     GROUP BY f.folio_number HAVING sum(g.signed_amount) <> 0
    UNION ALL
    SELECT 'GUEST_LEDGER', '', 'DEPOSIT_RELEASE', f.folio_number, sum(g.signed_amount), ''
      FROM folios f
      JOIN folio_item_gl g ON g.property_id = f.property_id AND g.folio_id = f.id AND g.kind = 'PAYMENT' AND g.is_deposit AND g.business_date <= @business_date
     WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.closed_on = @business_date
     GROUP BY f.folio_number HAVING sum(g.signed_amount) <> 0
) x
WHERE x.amount <> 0;

-- name: InsertJournal :one
INSERT INTO gl_journals (tenant_id, property_id, journal_number, journal_type, journal_date, description, reference, reverses_journal_id, reason,
                         idempotency_key, posted_at, posted_by, approved_by, is_closing)
VALUES (@tenant_id, @property_id, @journal_number, @journal_type, @journal_date, @description, sqlc.narg(reference), sqlc.narg(reverses_journal_id),
        sqlc.narg(reason), sqlc.narg(idempotency_key), @posted_at, sqlc.narg(actor_id), sqlc.narg(approved_by), @is_closing::boolean)
RETURNING id;

-- name: InsertJournalLine :exec
INSERT INTO gl_journal_lines (tenant_id, property_id, journal_id, line_no, account_id, debit, credit, description, source_type, source_ref)
VALUES (@tenant_id, @property_id, @journal_id, @line_no, @account_id, @debit, @credit, sqlc.narg(description), sqlc.narg(source_type), sqlc.narg(source_ref));

-- name: ListJournals :many
SELECT j.id, j.journal_number, j.journal_type, j.journal_date, j.description, j.reference, j.reverses_journal_id, j.reason, j.posted_at, j.posted_by,
       j.approved_by, rj.journal_number AS reverses_number, rv.id AS reversed_by_id, rv.journal_number AS reversed_by_number,
       COALESCE((SELECT sum(l.debit) FROM gl_journal_lines l WHERE l.property_id = j.property_id AND l.journal_id = j.id), 0)::numeric AS total,
       (SELECT count(*) FROM gl_journal_lines l WHERE l.property_id = j.property_id AND l.journal_id = j.id)::int AS line_count
FROM gl_journals j
LEFT JOIN gl_journals rj ON rj.property_id = j.property_id AND rj.id = j.reverses_journal_id
LEFT JOIN gl_journals rv ON rv.property_id = j.property_id AND rv.reverses_journal_id = j.id
WHERE j.tenant_id = @tenant_id AND j.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR j.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(from_date)::date IS NULL OR j.journal_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(to_date)::date IS NULL OR j.journal_date <= sqlc.narg(to_date)::date)
  AND (sqlc.narg(journal_type)::text IS NULL OR j.journal_type = sqlc.narg(journal_type)::text)
  AND (sqlc.narg(account_id)::bigint IS NULL OR EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = j.property_id AND l.journal_id = j.id AND l.account_id = sqlc.narg(account_id)::bigint))
  AND (sqlc.narg(q)::text IS NULL OR j.journal_number ILIKE '%' || sqlc.narg(q)::text || '%' OR j.description ILIKE '%' || sqlc.narg(q)::text || '%' OR j.reference ILIKE '%' || sqlc.narg(q)::text || '%')
ORDER BY j.journal_date DESC, j.id DESC
LIMIT @row_limit;

-- name: ListJournalLines :many
SELECT l.line_no, l.account_id, a.code AS account_code, a.name AS account_name, l.debit, l.credit, l.description, l.source_type, l.source_ref
FROM gl_journal_lines l
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.journal_id = @journal_id
ORDER BY l.line_no;

-- name: FindJournalByKey :one
SELECT id FROM gl_journals WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: GetDayPost :one
SELECT id FROM gl_day_posts WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @business_date;

-- name: InsertDayPost :exec
INSERT INTO gl_day_posts (tenant_id, property_id, business_date, journal_id, posted_at, posted_by)
VALUES (@tenant_id, @property_id, @business_date, sqlc.narg(journal_id), @posted_at, sqlc.narg(actor_id));

-- Closed business days from the start date that have no journal run yet, oldest first.
-- name: PendingDays :many
SELECT b.business_date FROM business_days b
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'CLOSED' AND b.business_date >= @start_date::date
  AND NOT EXISTS (SELECT 1 FROM gl_day_posts d WHERE d.property_id = b.property_id AND d.business_date = b.business_date)
ORDER BY b.business_date
LIMIT @row_limit;

-- name: CountAccountLines :one
SELECT count(*)::int FROM gl_journal_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND account_id = @account_id;

-- ---------------------------------------------------------------------------------------------------------------
-- Periods (calendar months)

-- name: ListPeriods :many
SELECT period_start, status, closed_at, closed_by, reopened_at, reopened_by, reopen_reason
FROM gl_periods WHERE tenant_id = @tenant_id AND property_id = @property_id ORDER BY period_start DESC;

-- name: GetPeriod :one
SELECT period_start, status FROM gl_periods WHERE tenant_id = @tenant_id AND property_id = @property_id AND period_start = @period_start;

-- name: ClosePeriod :exec
INSERT INTO gl_periods (tenant_id, property_id, period_start, status, closed_at, closed_by)
VALUES (@tenant_id, @property_id, @period_start, 'CLOSED', @now, sqlc.narg(actor_id))
ON CONFLICT (property_id, period_start) DO UPDATE
   SET status = 'CLOSED', closed_at = EXCLUDED.closed_at, closed_by = EXCLUDED.closed_by, reopened_at = NULL, reopened_by = NULL, reopen_reason = NULL;

-- name: ReopenPeriod :exec
UPDATE gl_periods SET status = 'OPEN', reopened_at = @now, reopened_by = sqlc.narg(actor_id), reopen_reason = @reason
WHERE tenant_id = @tenant_id AND property_id = @property_id AND period_start = @period_start;

-- name: LatestClosedPeriod :one
SELECT period_start FROM gl_periods WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'CLOSED' ORDER BY period_start DESC LIMIT 1;

-- How many days of a range are closed business days with their journal run.
-- name: CountPostedDays :one
SELECT count(*)::int FROM business_days b
JOIN gl_day_posts d ON d.property_id = b.property_id AND d.business_date = b.business_date
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'CLOSED'
  AND b.business_date BETWEEN @first_day::date AND @last_day::date;

-- ---------------------------------------------------------------------------------------------------------------
-- Reports (read-only)

-- Per account with entries up to the end date: the balance before the start, and the movement within the range.
-- name: TrialBalanceRows :many
SELECT a.id, a.code, a.name, a.account_type, a.normal_side, a.statement_group,
       COALESCE(sum(l.debit - l.credit) FILTER (WHERE j.journal_date < @from_date::date), 0)::numeric AS opening,
       COALESCE(sum(l.debit) FILTER (WHERE j.journal_date >= @from_date::date), 0)::numeric AS debit,
       COALESCE(sum(l.credit) FILTER (WHERE j.journal_date >= @from_date::date), 0)::numeric AS credit
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND j.journal_date <= @to_date::date
GROUP BY a.id
ORDER BY a.code;

-- Debit minus credit per account over a range (an open start means from the beginning).
-- name: AccountBalances :many
SELECT a.id, a.code, a.name, a.account_type, a.normal_side, a.statement_group,
       sum(l.debit - l.credit)::numeric AS balance
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND j.journal_date <= @to_date::date
  AND (sqlc.narg(from_date)::date IS NULL OR j.journal_date >= sqlc.narg(from_date)::date)
  AND (sqlc.narg(account_types)::text[] IS NULL OR a.account_type = ANY(sqlc.narg(account_types)::text[]))
  AND (NOT @exclude_closing::boolean OR NOT j.is_closing)
GROUP BY a.id
HAVING sum(l.debit - l.credit) <> 0
ORDER BY a.code;

-- name: LedgerOpening :one
SELECT COALESCE(sum(l.debit - l.credit), 0)::numeric AS opening
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = @account_id AND j.journal_date < @from_date::date;

-- name: LedgerLines :many
SELECT j.journal_date, j.id AS journal_id, j.journal_number, j.journal_type, j.description AS journal_description, l.line_no, l.debit, l.credit,
       l.description, l.source_type, l.source_ref
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = @account_id
  AND j.journal_date >= @from_date::date AND j.journal_date <= @to_date::date
ORDER BY j.journal_date, j.id, l.line_no
LIMIT @row_limit;

-- What the folios and the city ledger say as of a business date, to compare with the control accounts.
-- name: ControlSources :one
SELECT
    (SELECT COALESCE(sum(i.debit - i.credit), 0) FROM folio_items i
      WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date <= @as_of::date)::numeric AS folio_balance,
    (SELECT COALESCE(-sum(g.signed_amount), 0) FROM folio_item_gl g
       JOIN folios f ON f.property_id = g.property_id AND f.id = g.folio_id
      WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.kind = 'PAYMENT' AND g.is_deposit AND g.business_date <= @as_of::date
        AND (f.closed_on IS NULL OR f.closed_on > @as_of::date))::numeric AS deposits_held,
    (SELECT COALESCE(-sum(g.signed_amount), 0) FROM folio_item_gl g
      WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.kind = 'PAYMENT' AND g.payment_method = 'CITY_LEDGER'
        AND g.business_date <= @as_of::date)::numeric AS city_transferred,
    (SELECT COALESCE(sum(r.amount), 0) FROM city_ledger_receipts r
      WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'POSTED' AND r.business_date <= @as_of::date)::numeric AS city_received,
    -- Payables: bills entered by the date less payments made by the date; a bill or payment voided after the date still counts.
    (SELECT COALESCE(sum(b.total), 0) FROM supplier_bills b LEFT JOIN gl_journals vj ON vj.property_id = b.property_id AND vj.id = b.void_journal_id
      WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.bill_date <= @as_of::date
        AND (b.status = 'POSTED' OR vj.journal_date > @as_of::date))::numeric AS bills_entered,
    (SELECT COALESCE(sum(x.amount), 0) FROM supplier_payments x LEFT JOIN gl_journals vj ON vj.property_id = x.property_id AND vj.id = x.void_journal_id
      WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id AND x.payment_date <= @as_of::date
        AND (x.status = 'POSTED' OR vj.journal_date > @as_of::date))::numeric AS payments_made;

-- Closed days from the start date up to a date that have no journal run yet.
-- name: CountPendingDays :one
SELECT count(*)::int FROM business_days b
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'CLOSED'
  AND b.business_date >= @start_date::date AND b.business_date <= @as_of::date
  AND NOT EXISTS (SELECT 1 FROM gl_day_posts d WHERE d.property_id = b.property_id AND d.business_date = b.business_date);

-- ---------------------------------------------------------------------------------------------------------------
-- Fiscal years

-- name: ListFiscalYears :many
SELECT f.year_start, f.year_end, f.status, f.closing_journal_id, cj.journal_number AS closing_number, f.closed_at, f.closed_by, f.reopened_at, f.reopen_reason
FROM gl_fiscal_years f
LEFT JOIN gl_journals cj ON cj.property_id = f.property_id AND cj.id = f.closing_journal_id
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id
ORDER BY f.year_start DESC;

-- name: UpsertFiscalYearClosed :exec
INSERT INTO gl_fiscal_years (tenant_id, property_id, year_start, year_end, status, closing_journal_id, closed_at, closed_by)
VALUES (@tenant_id, @property_id, @year_start, @year_end, 'CLOSED', sqlc.narg(closing_journal_id), @now, sqlc.narg(actor_id))
ON CONFLICT (property_id, year_start) DO UPDATE
   SET status = 'CLOSED', closing_journal_id = EXCLUDED.closing_journal_id, closed_at = EXCLUDED.closed_at, closed_by = EXCLUDED.closed_by,
       reversal_journal_id = NULL, reopened_at = NULL, reopened_by = NULL, reopen_reason = NULL, approved_by = NULL;

-- name: MarkFiscalYearReopened :exec
UPDATE gl_fiscal_years SET status = 'OPEN', reversal_journal_id = sqlc.narg(reversal_journal_id), reopened_at = @now, reopened_by = sqlc.narg(actor_id),
       reopen_reason = @reason, approved_by = sqlc.narg(approved_by)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND year_start = @year_start;

-- name: CountClosedPeriodsBetween :one
SELECT count(*)::int FROM gl_periods
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'CLOSED' AND period_start BETWEEN @first_day::date AND @last_day::date;

-- The system accounts added after the first chart (retained earnings, accounts payable) for a new property.
-- name: SeedRetainedEarningsMap :exec
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id, updated_by)
SELECT a.tenant_id, a.property_id, m.map_key, a.id, sqlc.narg(actor_id)
  FROM gl_accounts a
  JOIN (VALUES ('RETAINED_EARNINGS', '3200'), ('ACCOUNTS_PAYABLE', '2110')) AS m (map_key, code) ON m.code = a.code
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id
ON CONFLICT (property_id, map_key) DO NOTHING;
