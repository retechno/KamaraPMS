-- The fiscal calendar of a property (the first month of its fiscal year and the day its books start).
-- name: GetFiscalSettings :one
SELECT fiscal_year_start_month, start_date FROM accounting_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: InsertBudget :one
INSERT INTO budgets (tenant_id, property_id, year_start, version, name, description, copied_from_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @year_start,
        (SELECT COALESCE(max(b.version), 0) + 1 FROM budgets b WHERE b.property_id = @property_id AND b.year_start = @year_start),
        @name, sqlc.narg(description), sqlc.narg(copied_from_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- A budget with the figures of its lines (the number of accounts and the revenue and expense of the year).
-- name: GetBudget :one
SELECT b.*,
       (SELECT count(DISTINCT l.account_id) FROM budget_lines l WHERE l.property_id = b.property_id AND l.budget_id = b.id)::int AS account_count,
       COALESCE((SELECT sum(l.amount) FROM budget_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
                  WHERE l.property_id = b.property_id AND l.budget_id = b.id AND a.account_type = 'REVENUE'), 0)::numeric AS total_revenue,
       COALESCE((SELECT sum(l.amount) FROM budget_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
                  WHERE l.property_id = b.property_id AND l.budget_id = b.id AND a.account_type = 'EXPENSE'), 0)::numeric AS total_expense
FROM budgets b
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.id = @id;

-- name: ListBudgets :many
SELECT b.*,
       (SELECT count(DISTINCT l.account_id) FROM budget_lines l WHERE l.property_id = b.property_id AND l.budget_id = b.id)::int AS account_count,
       COALESCE((SELECT sum(l.amount) FROM budget_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
                  WHERE l.property_id = b.property_id AND l.budget_id = b.id AND a.account_type = 'REVENUE'), 0)::numeric AS total_revenue,
       COALESCE((SELECT sum(l.amount) FROM budget_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
                  WHERE l.property_id = b.property_id AND l.budget_id = b.id AND a.account_type = 'EXPENSE'), 0)::numeric AS total_expense
FROM budgets b
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id
  AND (sqlc.narg(year_start)::date IS NULL OR b.year_start = sqlc.narg(year_start)::date)
  AND (sqlc.narg(status)::text IS NULL OR b.status = sqlc.narg(status)::text)
ORDER BY b.year_start DESC, b.version DESC;

-- The versions of one fiscal year (the rows a change of the year locks together).
-- name: BudgetIDsOfYear :many
SELECT id FROM budgets WHERE tenant_id = @tenant_id AND property_id = @property_id AND year_start = @year_start ORDER BY id;

-- name: ActiveBudgetOfYear :one
SELECT id FROM budgets WHERE tenant_id = @tenant_id AND property_id = @property_id AND year_start = @year_start AND status = 'ACTIVE';

-- name: UpdateBudgetText :one
UPDATE budgets SET name = @name, description = sqlc.narg(description), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING id;

-- name: DeleteBudget :exec
DELETE FROM budgets WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ArchiveBudget :exec
UPDATE budgets SET status = 'ARCHIVED', archived_at = @archived_at::timestamptz, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: ActivateBudget :exec
UPDATE budgets SET status = 'ACTIVE', activated_at = @activated_at::timestamptz, activated_by = sqlc.narg(actor_id), approved_by = @approved_by::bigint, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'DRAFT';

-- The accounts a budget may cover: revenue and expense accounts that take postings.
-- name: ListBudgetAccounts :many
SELECT a.id, a.code, a.name, a.account_type, COALESCE(a.statement_group, '')::text AS statement_group, a.is_active
FROM gl_accounts a
WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.account_type IN ('REVENUE', 'EXPENSE') AND a.is_postable
ORDER BY a.code;

-- The figures of a budget, an account per month.
-- name: ListBudgetLines :many
SELECT l.account_id, l.month, l.amount
FROM budget_lines l
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.budget_id = @budget_id
ORDER BY l.account_id, l.month;

-- name: DeleteBudgetLines :exec
DELETE FROM budget_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND budget_id = @budget_id;

-- name: DeleteBudgetLinesOfAccount :exec
DELETE FROM budget_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND budget_id = @budget_id AND account_id = @account_id;

-- The cells arrive as a JSON array of {account_id, month, amount} (the amount as text), so a whole grid is one statement.
-- name: InsertBudgetLines :exec
INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount)
SELECT @tenant_id::bigint, @property_id::bigint, @budget_id::bigint, (c ->> 'account_id')::bigint, (c ->> 'month')::smallint, (c ->> 'amount')::numeric
FROM jsonb_array_elements(@cells::jsonb) AS c;

-- name: CopyBudgetLines :exec
INSERT INTO budget_lines (tenant_id, property_id, budget_id, account_id, month, amount)
SELECT l.tenant_id, l.property_id, @to_budget_id::bigint, l.account_id, l.month, l.amount
FROM budget_lines l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.budget_id = @from_budget_id;

-- Debit minus credit per account over a range of days, closing journals left out (what the income statement adds up).
-- name: ActualByAccount :many
SELECT a.id, a.code, a.name, a.account_type, COALESCE(a.statement_group, '')::text AS statement_group, sum(l.debit - l.credit)::numeric AS balance
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND a.account_type IN ('REVENUE', 'EXPENSE')
  AND j.journal_date >= @from_date::date AND j.journal_date <= @to_date::date AND NOT j.is_closing
GROUP BY a.id
ORDER BY a.code;

-- The same per account and calendar month.
-- name: ActualByAccountMonth :many
SELECT a.id AS account_id, date_trunc('month', j.journal_date)::date AS month_start, sum(l.debit - l.credit)::numeric AS balance
FROM gl_journal_lines l
JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND a.account_type IN ('REVENUE', 'EXPENSE') AND a.is_postable
  AND j.journal_date >= @from_date::date AND j.journal_date <= @to_date::date AND NOT j.is_closing
GROUP BY a.id, date_trunc('month', j.journal_date)
ORDER BY a.id, month_start;

-- The budget of a range of months of the fiscal year (the months are 1 to 12 from the first month of the year).
-- name: BudgetByAccount :many
SELECT a.id, a.code, a.name, a.account_type, COALESCE(a.statement_group, '')::text AS statement_group, sum(l.amount)::numeric AS amount
FROM budget_lines l
JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.budget_id = @budget_id AND l.month >= @from_month::smallint AND l.month <= @to_month::smallint
GROUP BY a.id
ORDER BY a.code;

-- The statistics of a budget, by month.
-- name: ListBudgetStatistics :many
SELECT month, rooms_available, rooms_sold, adr FROM budget_statistics
WHERE tenant_id = @tenant_id AND property_id = @property_id AND budget_id = @budget_id
ORDER BY month;

-- name: DeleteBudgetStatistics :exec
DELETE FROM budget_statistics WHERE tenant_id = @tenant_id AND property_id = @property_id AND budget_id = @budget_id;

-- name: InsertBudgetStatistics :exec
INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr)
SELECT @tenant_id::bigint, @property_id::bigint, @budget_id::bigint, (c ->> 'month')::smallint, (c ->> 'rooms_available')::int, (c ->> 'rooms_sold')::int, (c ->> 'adr')::numeric
FROM jsonb_array_elements(@cells::jsonb) AS c;

-- name: CopyBudgetStatistics :exec
INSERT INTO budget_statistics (tenant_id, property_id, budget_id, month, rooms_available, rooms_sold, adr)
SELECT s.tenant_id, s.property_id, @to_budget_id::bigint, s.month, s.rooms_available, s.rooms_sold, s.adr
FROM budget_statistics s WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.budget_id = @from_budget_id;

-- The rooms the hotel sells today (a suggestion for the rooms available).
-- name: CountActiveRooms :one
SELECT count(*)::int FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_active;

-- The budget of the room revenue (the accounts of the REV_ROOMS group) for a range of months.
-- name: BudgetedRoomRevenue :one
SELECT COALESCE(sum(l.amount), 0)::numeric AS amount
FROM budget_lines l JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.account_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.budget_id = @budget_id AND a.statement_group = 'REV_ROOMS'
  AND l.month >= @from_month::smallint AND l.month <= @to_month::smallint;

-- The budget of the statistics for a range of months.
-- name: BudgetedStatistics :one
SELECT COALESCE(sum(rooms_available), 0)::int AS rooms_available, COALESCE(sum(rooms_sold), 0)::int AS rooms_sold, COALESCE(sum(rooms_sold * adr), 0)::numeric AS room_revenue
FROM budget_statistics
WHERE tenant_id = @tenant_id AND property_id = @property_id AND budget_id = @budget_id AND month >= @from_month::smallint AND month <= @to_month::smallint;

-- Closed days with a stored summary, for the actual statistics.
-- name: ListClosedDaySummaries :many
SELECT business_date, summary FROM business_days
WHERE property_id = @property_id AND status = 'CLOSED' AND business_date BETWEEN @from_date::date AND @to_date::date
ORDER BY business_date;
