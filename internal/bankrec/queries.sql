-- Bank reconciliation (sqlc): bank accounts, imported statements, and the clearings that tie their lines to journal lines.
-- Every query is scoped by tenant_id and property_id.

-- ---------------------------------------------------------------------------------------------------------------
-- Bank accounts

-- name: ListBankAccounts :many
SELECT b.id, b.account_id, a.code AS account_code, a.name AS account_name, b.name, b.account_number, b.is_active, b.created_at,
       COALESCE((SELECT sum(l.debit - l.credit) FROM gl_journal_lines l WHERE l.property_id = b.property_id AND l.account_id = b.account_id), 0)::numeric AS book_balance,
       COALESCE((SELECT max(s.period_to) FROM bank_statements s WHERE s.property_id = b.property_id AND s.bank_account_id = b.id AND s.status = 'RECONCILED'), DATE '1900-01-01')::date AS reconciled_to, -- 1900-01-01: none yet
       (SELECT count(*) FROM bank_statements s WHERE s.property_id = b.property_id AND s.bank_account_id = b.id AND s.status = 'OPEN')::int AS open_statements
FROM bank_accounts b
JOIN gl_accounts a ON a.property_id = b.property_id AND a.id = b.account_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR b.id = sqlc.narg(id)::bigint)
ORDER BY a.code, b.id;

-- name: InsertBankAccount :one
INSERT INTO bank_accounts (tenant_id, property_id, account_id, name, account_number, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @account_id, @name, sqlc.narg(account_number), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateBankAccount :exec
UPDATE bank_accounts SET name = @name, account_number = sqlc.narg(account_number), is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: AccountEligible :one
SELECT account_type, is_postable, is_active FROM gl_accounts WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------
-- Statements

-- name: ListStatements :many
SELECT s.id, s.bank_account_id, b.name AS bank_name, b.account_id, a.code AS account_code, s.period_from, s.period_to, s.opening_balance, s.closing_balance, s.status,
       s.note, s.imported_at, s.reconciled_at, s.reopen_reason,
       (SELECT count(*) FROM bank_statement_lines l WHERE l.property_id = s.property_id AND l.statement_id = s.id)::int AS line_count,
       (SELECT count(*) FROM bank_statement_lines l WHERE l.property_id = s.property_id AND l.statement_id = s.id
           AND l.amount = COALESCE((SELECT sum(c.amount) FROM bank_clearings c WHERE c.statement_line_id = l.id), 0))::int AS matched_count
FROM bank_statements s
JOIN bank_accounts b ON b.property_id = s.property_id AND b.id = s.bank_account_id
JOIN gl_accounts a ON a.property_id = b.property_id AND a.id = b.account_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR s.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(bank_account_id)::bigint IS NULL OR s.bank_account_id = sqlc.narg(bank_account_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR s.status = sqlc.narg(status)::text)
ORDER BY s.period_to DESC, s.id DESC
LIMIT @row_limit;

-- name: InsertStatement :one
INSERT INTO bank_statements (tenant_id, property_id, bank_account_id, period_from, period_to, opening_balance, closing_balance, note, imported_at, imported_by)
VALUES (@tenant_id, @property_id, @bank_account_id, @period_from, @period_to, @opening_balance, @closing_balance, sqlc.narg(note), @now, sqlc.narg(actor_id))
RETURNING id;

-- name: InsertStatementLine :exec
INSERT INTO bank_statement_lines (tenant_id, property_id, statement_id, line_no, line_date, description, reference, amount)
VALUES (@tenant_id, @property_id, @statement_id, @line_no, @line_date, sqlc.narg(description), sqlc.narg(reference), @amount);

-- name: ListStatementLines :many
SELECT l.id, l.line_no, l.line_date, l.description, l.reference, l.amount,
       COALESCE((SELECT sum(c.amount) FROM bank_clearings c WHERE c.statement_line_id = l.id), 0)::numeric AS cleared
FROM bank_statement_lines l
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.statement_id = @statement_id
ORDER BY l.line_no;

-- Statements of a bank account that overlap a range, other than one given.
-- name: CountOverlappingStatements :one
SELECT count(*)::int FROM bank_statements
WHERE tenant_id = @tenant_id AND property_id = @property_id AND bank_account_id = @bank_account_id AND period_from <= @period_to AND period_to >= @period_from;

-- The statement of a bank account just before a date (by its end), for the continuity of balances and the order of reconciling.
-- name: PreviousStatement :one
SELECT id, period_to, closing_balance, status FROM bank_statements
WHERE tenant_id = @tenant_id AND property_id = @property_id AND bank_account_id = @bank_account_id AND period_to < @before::date
ORDER BY period_to DESC LIMIT 1;

-- name: NextStatementExists :one
SELECT EXISTS (SELECT 1 FROM bank_statements WHERE tenant_id = @tenant_id AND property_id = @property_id AND bank_account_id = @bank_account_id AND period_from > @after::date)::boolean;

-- name: LaterReconciledExists :one
SELECT EXISTS (SELECT 1 FROM bank_statements WHERE tenant_id = @tenant_id AND property_id = @property_id AND bank_account_id = @bank_account_id AND period_to > @after::date AND status = 'RECONCILED')::boolean;

-- name: DeleteStatementClearings :exec
DELETE FROM bank_clearings WHERE tenant_id = @tenant_id AND property_id = @property_id AND statement_id = @statement_id;

-- name: DeleteStatementLines :exec
DELETE FROM bank_statement_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND statement_id = @statement_id;

-- name: DeleteStatement :exec
DELETE FROM bank_statements WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: MarkReconciled :exec
UPDATE bank_statements SET status = 'RECONCILED', reconciled_at = @now, reconciled_by = sqlc.narg(actor_id), reopened_at = NULL, reopened_by = NULL, reopen_reason = NULL
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: MarkReopened :exec
UPDATE bank_statements SET status = 'OPEN', reconciled_at = NULL, reconciled_by = NULL, reopened_at = @now, reopened_by = sqlc.narg(actor_id), reopen_reason = @reason
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------
-- Journal lines and clearings

-- The journal lines of an account up to a date that are not cleared in full (a line can be cleared in parts).
-- name: UnclearedLines :many
SELECT l.id, j.journal_date, j.id AS journal_id, j.journal_number, j.journal_type, j.description AS journal_description, l.description, l.source_ref,
       (l.debit - l.credit)::numeric AS amount,
       COALESCE((SELECT sum(c.amount) FROM bank_clearings c WHERE c.journal_line_id = l.id), 0)::numeric AS cleared
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = @account_id AND j.journal_date <= @to_date::date
  AND (l.debit - l.credit) <> COALESCE((SELECT sum(c.amount) FROM bank_clearings c WHERE c.journal_line_id = l.id), 0)
ORDER BY j.journal_date, l.id
LIMIT @row_limit;

-- name: UnclearedTotals :one
SELECT COALESCE(sum(GREATEST(r.rem, 0)), 0)::numeric AS money_in, COALESCE(sum(GREATEST(-r.rem, 0)), 0)::numeric AS money_out, count(*)::int AS lines
FROM (
    SELECT l.debit - l.credit - COALESCE((SELECT sum(c.amount) FROM bank_clearings c WHERE c.journal_line_id = l.id), 0) AS rem
    FROM gl_journal_lines l
    JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
    WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = @account_id AND j.journal_date <= @to_date::date
) r
WHERE r.rem <> 0;

-- name: BookBalance :one
SELECT COALESCE(sum(l.debit - l.credit), 0)::numeric AS balance
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = @account_id AND j.journal_date <= @to_date::date;

-- What the bank has cleared on an account up to the end of a statement: the clearings of that statement and of the ones before it.
-- name: ClearedTotal :one
SELECT COALESCE(sum(c.amount), 0)::numeric AS total
FROM bank_clearings c
JOIN bank_statements s ON s.property_id = c.property_id AND s.id = c.statement_id
WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.bank_account_id = @bank_account_id AND s.period_to <= @period_to::date;

-- name: GetJournalLine :one
SELECT l.id, l.account_id, (l.debit - l.credit)::numeric AS amount, j.journal_date, j.journal_number,
       COALESCE((SELECT sum(c.amount) FROM bank_clearings c WHERE c.journal_line_id = l.id), 0)::numeric AS cleared
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.id = @id;

-- name: InsertClearing :exec
INSERT INTO bank_clearings (tenant_id, property_id, bank_account_id, statement_id, statement_line_id, journal_line_id, amount, cleared_at, cleared_by)
VALUES (@tenant_id, @property_id, @bank_account_id, @statement_id, sqlc.narg(statement_line_id), @journal_line_id, @amount, @now, sqlc.narg(actor_id));

-- name: ListClearings :many
SELECT c.id, c.statement_line_id, c.journal_line_id, c.amount, j.journal_date, j.journal_number, j.journal_type, COALESCE(l.description, j.description) AS description
FROM bank_clearings c
JOIN gl_journal_lines l ON l.id = c.journal_line_id
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.statement_id = @statement_id
ORDER BY j.journal_date, c.id;

-- name: GetClearing :one
SELECT id, statement_id, statement_line_id, journal_line_id, amount FROM bank_clearings WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteClearing :exec
DELETE FROM bank_clearings WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetStatementLine :one
SELECT id, line_no, line_date, description, reference, amount FROM bank_statement_lines
WHERE tenant_id = @tenant_id AND property_id = @property_id AND statement_id = @statement_id AND id = @id;

-- The line of an account in a journal (the bank side of a journal posted from a statement line).
-- name: JournalLineOfAccount :one
SELECT id FROM gl_journal_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND journal_id = @journal_id AND account_id = @account_id ORDER BY line_no LIMIT 1;

-- What is cleared of a statement line so far.
-- name: LineCleared :one
SELECT COALESCE(sum(amount), 0)::numeric AS cleared FROM bank_clearings WHERE tenant_id = @tenant_id AND property_id = @property_id AND statement_line_id = @statement_line_id;

-- ---------------------------------------------------------------------------------------------------------------
-- Card and e-wallet settlements

-- The lines of a clearing account (card, e-wallet) up to a date that no settlement has settled and that are not the
-- credit of a settlement themselves.
-- name: SettlementCandidates :many
SELECT l.id, j.journal_date, j.id AS journal_id, j.journal_number, j.journal_type, j.description AS journal_description, l.description, l.source_ref,
       (l.debit - l.credit)::numeric AS amount
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = @account_id AND j.journal_date <= @to_date::date
  AND NOT EXISTS (SELECT 1 FROM card_settlement_items i WHERE i.settled_line_id = l.id OR i.settling_line_id = l.id)
ORDER BY j.journal_date, l.id
LIMIT @row_limit;

-- name: IsSettledOrSettling :one
SELECT EXISTS (SELECT 1 FROM card_settlement_items WHERE settled_line_id = @line_id OR settling_line_id = @line_id)::boolean;

-- name: InsertSettlement :one
INSERT INTO card_settlements (tenant_id, property_id, bank_account_id, account_key, journal_id, gross, net, fee, expected_fee, reference, created_by)
VALUES (@tenant_id, @property_id, @bank_account_id, @account_key, @journal_id, @gross, @net, @fee, sqlc.narg(expected_fee), sqlc.narg(reference), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertSettlementItem :exec
INSERT INTO card_settlement_items (tenant_id, property_id, settlement_id, settled_line_id, settling_line_id, amount)
VALUES (@tenant_id, @property_id, @settlement_id, @settled_line_id, @settling_line_id, @amount);

-- The account a system key of the books points at.
-- name: MapAccount :one
SELECT account_id FROM gl_account_map WHERE tenant_id = @tenant_id AND property_id = @property_id AND map_key = @map_key;

-- ---------------------------------------------------------------------------
-- Card fee rules (the fee the acquirer is expected to keep) and what the payments expected

-- name: ListCardFeeRules :many
SELECT * FROM card_fee_rules WHERE tenant_id = @tenant_id AND property_id = @property_id ORDER BY payment_method, effective_from DESC;

-- name: InsertCardFeeRule :one
INSERT INTO card_fee_rules (tenant_id, property_id, payment_method, mdr_rate, settlement_days, effective_from, created_by)
VALUES (@tenant_id, @property_id, @payment_method, @mdr_rate, @settlement_days, @effective_from, sqlc.narg(actor_id))
RETURNING *;

-- The snapshots of the payments and the city ledger receipts of some numbers (the reference of a journal line is the number of its document).
-- name: PaymentFeeSnapshots :many
SELECT payment_number AS doc_number, mdr_rate::numeric AS mdr_rate, mdr_fee::numeric AS mdr_fee, expected_settlement_date::date AS expected_date
FROM payments
WHERE tenant_id = @tenant_id AND property_id = @property_id AND payment_number = ANY(@numbers::text[]) AND mdr_rate IS NOT NULL;

-- name: ReceiptFeeSnapshots :many
SELECT receipt_number AS doc_number, mdr_rate::numeric AS mdr_rate, mdr_fee::numeric AS mdr_fee, expected_settlement_date::date AS expected_date
FROM city_ledger_receipts
WHERE tenant_id = @tenant_id AND property_id = @property_id AND receipt_number = ANY(@numbers::text[]) AND mdr_rate IS NOT NULL;

-- name: JournalLineRefs :many
SELECT id, COALESCE(source_ref, '')::text AS source_ref FROM gl_journal_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: ListSettlements :many
SELECT s.id, s.bank_account_id, s.account_key, s.gross, s.net, s.fee, s.expected_fee, s.reference, s.created_at, j.journal_date, j.journal_number,
       (SELECT count(*) FROM card_settlement_items i WHERE i.settlement_id = s.id)::int AS payments
FROM card_settlements s
JOIN gl_journals j ON j.property_id = s.property_id AND j.id = s.journal_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id < @before_id
ORDER BY s.id DESC
LIMIT @row_limit;
