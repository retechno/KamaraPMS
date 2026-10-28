-- Fixed assets, PR 1 (docs/architecture/37-fixed-assets.md): settings, categories, locations, the register, the opening imports.
-- Every query is scoped by tenant_id and property_id. The ledger (fa_ledger) and the state columns of an asset (cost, unit_count, accum_depr, dep_months_done, last_dep_month, status, disposed_on)
-- are written only by ledger.go (ledgerpath_guard_test.go holds it): the queries that write them are named Ledger* so the guard can find their callers.

-- ---------------------------------------------------------------------------------------------------------------------
-- Settings (the row is also the lock target of the register)

-- name: GetSettings :one
SELECT * FROM fa_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: EnsureSettings :one
INSERT INTO fa_settings (tenant_id, property_id, created_by, updated_by) VALUES (@tenant_id, @property_id, sqlc.narg(actor_id), sqlc.narg(actor_id))
ON CONFLICT (property_id) DO UPDATE SET property_id = EXCLUDED.property_id
RETURNING *;

-- name: UpdateSettings :one
UPDATE fa_settings
   SET register_from = sqlc.narg(register_from), capitalization_threshold = @capitalization_threshold, start_rule = @start_rule, oe_policy = @oe_policy,
       oe_default_months = @oe_default_months, oe_policy_changed_on = sqlc.narg(oe_policy_changed_on), tax_year_start_month = sqlc.narg(tax_year_start_month),
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND version = @version
RETURNING *;

-- name: SetRegisterFrom :exec
UPDATE fa_settings SET register_from = @register_from, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: GetFiscalYearStartMonth :one
SELECT fiscal_year_start_month FROM accounting_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Fiscal groups

-- name: ListFiscalGroups :many
SELECT * FROM fa_fiscal_groups WHERE tenant_id = @tenant_id AND property_id = @property_id ORDER BY code;

-- name: UpdateFiscalGroup :one
UPDATE fa_fiscal_groups SET life_years = @life_years, sl_rate = @sl_rate, db_rate = sqlc.narg(db_rate), updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code
RETURNING *;

-- ---------------------------------------------------------------------------------------------------------------------
-- Accounts (pickers and checks)

-- name: ListAccountsOfType :many
SELECT a.id, a.code, a.name, a.account_type, a.statement_group, a.is_postable, a.is_active
  FROM gl_accounts a
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.is_active AND a.is_postable
   AND (sqlc.narg(account_type)::text IS NULL OR a.account_type = sqlc.narg(account_type))
 ORDER BY a.code;

-- name: GetAccount :one
SELECT a.id, a.code, a.name, a.account_type, a.is_postable, a.is_active, a.department_requirement
  FROM gl_accounts a WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = @id;

-- name: GetAccountByCode :one
SELECT a.id, a.code, a.name, a.account_type, a.is_postable, a.is_active
  FROM gl_accounts a WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.code = @code;

-- name: AccountBalances :many
-- The balance of accounts up to a date: debit minus credit of the journals dated on or before it.
SELECT l.account_id, COALESCE(SUM(l.debit - l.credit), 0)::numeric(18,3) AS balance
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = ANY(@account_ids::bigint[]) AND j.journal_date <= @as_of
 GROUP BY l.account_id;

-- name: AccountHasJournalLines :one
SELECT EXISTS (SELECT 1 FROM gl_journal_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND account_id = @account_id)::boolean;

-- ---------------------------------------------------------------------------------------------------------------------
-- Categories

-- name: ListCategories :many
SELECT c.*, ca.code AS cost_account_code, ca.name AS cost_account_name,
       aa.code AS accumulated_account_code, aa.name AS accumulated_account_name,
       ea.code AS expense_account_code, ea.name AS expense_account_name,
       d.code AS department_code, d.name AS department_name,
       (SELECT count(*) FROM fa_assets x WHERE x.property_id = c.property_id AND x.category_id = c.id)::int AS asset_count
  FROM fa_categories c
  JOIN gl_accounts ca ON ca.property_id = c.property_id AND ca.id = c.cost_account_id
  LEFT JOIN gl_accounts aa ON aa.property_id = c.property_id AND aa.id = c.accumulated_account_id
  LEFT JOIN gl_accounts ea ON ea.property_id = c.property_id AND ea.id = c.expense_account_id
  LEFT JOIN departments d ON d.property_id = c.property_id AND d.id = c.department_id
 WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
 ORDER BY c.sort_order, c.code;

-- name: GetCategory :one
SELECT * FROM fa_categories WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetCategoryByCode :one
SELECT * FROM fa_categories WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: CategoryUsingCostAccount :one
SELECT COALESCE((SELECT id FROM fa_categories WHERE tenant_id = @tenant_id AND property_id = @property_id AND cost_account_id = @account_id AND is_active
                  AND id <> @except_id LIMIT 1), 0)::bigint;

-- name: CategoryAssetCount :one
SELECT count(*)::int FROM fa_assets WHERE tenant_id = @tenant_id AND property_id = @property_id AND category_id = @id;

-- name: InsertCategory :one
INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id, accumulated_account_id, expense_account_id, department_id, method, life_months,
                           residual_pct, db_factor, fiscal_group, fiscal_method, is_active, sort_order, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @kind, @cost_account_id, sqlc.narg(accumulated_account_id), sqlc.narg(expense_account_id), sqlc.narg(department_id), sqlc.narg(method),
        sqlc.narg(life_months), @residual_pct, sqlc.narg(db_factor), @fiscal_group, sqlc.narg(fiscal_method), @is_active, @sort_order, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateCategory :one
UPDATE fa_categories
   SET name = @name, kind = @kind, cost_account_id = @cost_account_id, accumulated_account_id = sqlc.narg(accumulated_account_id), expense_account_id = sqlc.narg(expense_account_id),
       department_id = sqlc.narg(department_id), method = sqlc.narg(method), life_months = sqlc.narg(life_months), residual_pct = @residual_pct, db_factor = sqlc.narg(db_factor),
       fiscal_group = @fiscal_group, fiscal_method = sqlc.narg(fiscal_method), is_active = @is_active, sort_order = @sort_order, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING *;

-- name: ControlAccountIDs :many
SELECT DISTINCT accumulated_account_id::bigint FROM fa_categories
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND accumulated_account_id IS NOT NULL;

-- name: DisposalFlows :many
-- What the disposals posted in a range took out of each cost account and the proceeds they brought, for the cash flow statement: a disposal counts on the date of its journal, and a reversed one is
-- taken back on the date of the reversal journal, so what the books show for the range is what this returns.
SELECT c.cost_account_id::bigint AS cost_account_id,
       (COALESCE(SUM(d.cost_removed) FILTER (WHERE j.journal_date BETWEEN @from_date AND @to_date), 0)
        - COALESCE(SUM(d.cost_removed) FILTER (WHERE rj.journal_date BETWEEN @from_date AND @to_date), 0))::numeric(18,3) AS cost_removed,
       (COALESCE(SUM(d.proceeds) FILTER (WHERE j.journal_date BETWEEN @from_date AND @to_date), 0)
        - COALESCE(SUM(d.proceeds) FILTER (WHERE rj.journal_date BETWEEN @from_date AND @to_date), 0))::numeric(18,3) AS proceeds
  FROM fa_disposals d
  JOIN fa_assets a ON a.property_id = d.property_id AND a.id = d.asset_id
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  JOIN gl_journals j ON j.id = d.journal_id
  LEFT JOIN gl_journals rj ON rj.id = d.reversal_journal_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id
 GROUP BY c.cost_account_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Locations

-- name: ListLocations :many
SELECT l.*, r.room_number, br.name AS banquet_room_name, w.name AS warehouse_name,
       (SELECT count(*) FROM fa_assets x WHERE x.property_id = l.property_id AND x.location_id = l.id AND x.status = 'ACTIVE')::int AS asset_count
  FROM fa_locations l
  LEFT JOIN rooms r ON r.property_id = l.property_id AND r.id = l.room_id
  LEFT JOIN banquet_rooms br ON br.property_id = l.property_id AND br.id = l.banquet_room_id
  LEFT JOIN inv_warehouses w ON w.property_id = l.property_id AND w.id = l.warehouse_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id
   AND (sqlc.narg(kind)::text IS NULL OR l.kind = sqlc.narg(kind))
   AND (sqlc.narg(active)::boolean IS NULL OR l.is_active = sqlc.narg(active))
 ORDER BY l.kind, l.code;

-- name: GetLocation :one
SELECT * FROM fa_locations WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetLocationByCode :one
SELECT * FROM fa_locations WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: GetLocationByRoom :one
SELECT * FROM fa_locations WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_id = @ref_id;

-- name: GetLocationByBanquetRoom :one
SELECT * FROM fa_locations WHERE tenant_id = @tenant_id AND property_id = @property_id AND banquet_room_id = @ref_id;

-- name: GetLocationByWarehouse :one
SELECT * FROM fa_locations WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @ref_id;

-- name: GetRoomRef :one
SELECT id, room_number, is_active FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetRoomRefByNumber :one
SELECT id, room_number, is_active FROM rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND room_number = @room_number;

-- name: GetBanquetRoomRef :one
SELECT id, code, name, is_active FROM banquet_rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetBanquetRoomRefByCode :one
SELECT id, code, name, is_active FROM banquet_rooms WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: GetWarehouseRef :one
SELECT id, code, name, is_active FROM inv_warehouses WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetWarehouseRefByCode :one
SELECT id, code, name, is_active FROM inv_warehouses WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: InsertLocation :one
INSERT INTO fa_locations (tenant_id, property_id, code, name, kind, room_id, banquet_room_id, warehouse_id, label_code, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @kind, sqlc.narg(room_id), sqlc.narg(banquet_room_id), sqlc.narg(warehouse_id), @label_code, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateLocation :one
UPDATE fa_locations SET name = @name, is_active = @is_active, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- name: GetSupplierRefByCode :one
SELECT id, code, name FROM suppliers WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- name: GetDepartmentRefByCode :one
SELECT id, code, name FROM departments WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- ---------------------------------------------------------------------------------------------------------------------
-- The register

-- name: ListAssets :many
SELECT a.*, c.code AS category_code, c.name AS category_name, c.kind AS category_kind,
       l.code AS location_code, l.name AS location_name, d.code AS department_code, d.name AS department_name, s.code AS supplier_code, s.name AS supplier_name,
       EXISTS (SELECT 1 FROM fa_disposal_requests r WHERE r.property_id = a.property_id AND r.asset_id = a.id AND r.status = 'PENDING') AS disposal_pending
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
  LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.department_id
  LEFT JOIN suppliers s ON s.property_id = a.property_id AND s.id = a.supplier_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id
   AND (sqlc.narg(category_id)::bigint IS NULL OR a.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(location_id)::bigint IS NULL OR a.location_id = sqlc.narg(location_id))
   AND (sqlc.narg(department_id)::bigint IS NULL OR a.department_id = sqlc.narg(department_id))
   AND (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status))
   AND (sqlc.narg(q)::text IS NULL OR a.number ILIKE '%' || sqlc.narg(q) || '%' OR a.name ILIKE '%' || sqlc.narg(q) || '%' OR a.serial_no ILIKE '%' || sqlc.narg(q) || '%')
   AND (sqlc.narg(warranty_before)::date IS NULL OR (a.warranty_until IS NOT NULL AND a.warranty_until <= sqlc.narg(warranty_before)))
   AND (sqlc.narg(fully_depreciated)::boolean IS NULL OR ((a.life_months IS NOT NULL AND a.accum_depr >= a.cost - a.residual_value) = sqlc.narg(fully_depreciated)))
 ORDER BY a.number
 LIMIT @row_limit OFFSET @row_offset;

-- name: SummarizeAssets :one
SELECT count(*)::int AS total, COALESCE(SUM(a.cost), 0)::numeric(18,3) AS cost, COALESCE(SUM(a.accum_depr), 0)::numeric(18,3) AS accum
  FROM fa_assets a
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id
   AND (sqlc.narg(category_id)::bigint IS NULL OR a.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(location_id)::bigint IS NULL OR a.location_id = sqlc.narg(location_id))
   AND (sqlc.narg(department_id)::bigint IS NULL OR a.department_id = sqlc.narg(department_id))
   AND (sqlc.narg(status)::text IS NULL OR a.status = sqlc.narg(status))
   AND (sqlc.narg(q)::text IS NULL OR a.number ILIKE '%' || sqlc.narg(q) || '%' OR a.name ILIKE '%' || sqlc.narg(q) || '%' OR a.serial_no ILIKE '%' || sqlc.narg(q) || '%')
   AND (sqlc.narg(warranty_before)::date IS NULL OR (a.warranty_until IS NOT NULL AND a.warranty_until <= sqlc.narg(warranty_before)))
   AND (sqlc.narg(fully_depreciated)::boolean IS NULL OR ((a.life_months IS NOT NULL AND a.accum_depr >= a.cost - a.residual_value) = sqlc.narg(fully_depreciated)));

-- name: GetAsset :one
SELECT a.*, c.code AS category_code, c.name AS category_name, c.kind AS category_kind,
       l.code AS location_code, l.name AS location_name, d.code AS department_code, d.name AS department_name, s.code AS supplier_code, s.name AS supplier_name,
       EXISTS (SELECT 1 FROM fa_disposal_requests r WHERE r.property_id = a.property_id AND r.asset_id = a.id AND r.status = 'PENDING') AS disposal_pending
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
  LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.department_id
  LEFT JOIN suppliers s ON s.property_id = a.property_id AND s.id = a.supplier_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = @id;

-- name: ListAssetsByIDs :many
SELECT a.*, c.code AS category_code, c.name AS category_name, c.kind AS category_kind,
       l.code AS location_code, l.name AS location_name, d.code AS department_code, d.name AS department_name, s.code AS supplier_code, s.name AS supplier_name,
       EXISTS (SELECT 1 FROM fa_disposal_requests r WHERE r.property_id = a.property_id AND r.asset_id = a.id AND r.status = 'PENDING') AS disposal_pending
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
  LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.department_id
  LEFT JOIN suppliers s ON s.property_id = a.property_id AND s.id = a.supplier_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = ANY(@ids::bigint[])
 ORDER BY a.number;

-- name: ListAssetsForLabels :many
SELECT a.id, a.number, a.name, a.label_code, l.name AS location_name, c.name AS category_name
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status = 'ACTIVE'
   AND (sqlc.narg(location_id)::bigint IS NULL OR a.location_id = sqlc.narg(location_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR a.category_id = sqlc.narg(category_id))
   AND (cardinality(@ids::bigint[]) = 0 OR a.id = ANY(@ids::bigint[]))
 ORDER BY a.number
 LIMIT 500;

-- name: LookupAssets :many
SELECT a.id, a.number, a.name, a.status, l.name AS location_name
  FROM fa_assets a
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status = 'ACTIVE'
   AND (sqlc.narg(q)::text IS NULL OR a.number ILIKE '%' || sqlc.narg(q) || '%' OR a.name ILIKE '%' || sqlc.narg(q) || '%' OR a.serial_no ILIKE '%' || sqlc.narg(q) || '%')
 ORDER BY a.number
 LIMIT 30;

-- name: UpdateAssetDetails :one
UPDATE fa_assets
   SET name = @name, location_id = sqlc.narg(location_id), department_id = sqlc.narg(department_id), supplier_id = sqlc.narg(supplier_id),
       serial_no = sqlc.narg(serial_no), warranty_until = sqlc.narg(warranty_until), notes = sqlc.narg(notes), version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING id;

-- name: AssetHasRegisterEvents :one
-- Whether the register ever depreciated, revalued, disposed of or reclassified the asset: what stops a cancellation.
SELECT EXISTS (SELECT 1 FROM fa_ledger e WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.asset_id = @asset_id
                AND e.kind NOT IN ('OPEN', 'ACQUIRE', 'REVERSAL')
                AND NOT EXISTS (SELECT 1 FROM fa_ledger r WHERE r.property_id = e.property_id AND r.reverses_id = e.id))::boolean;

-- name: ListAssetLedger :many
SELECT * FROM fa_ledger WHERE tenant_id = @tenant_id AND property_id = @property_id AND asset_id = @asset_id ORDER BY id;

-- name: FindDuplicateAssets :many
SELECT id, number FROM fa_assets
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ACTIVE' AND category_id = @category_id AND lower(name) = lower(@name::text) AND cost = @cost
   AND serial_no IS NOT DISTINCT FROM sqlc.narg(serial_no)::text;

-- name: RegisterTotalsByCategory :many
-- What the register holds per category up to a date (the ledger up to the date, cancelled assets net to zero).
SELECT e.category_id, COALESCE(SUM(e.cost_delta), 0)::numeric(18,3) AS cost, COALESCE(SUM(e.accum_delta), 0)::numeric(18,3) AS accum
  FROM fa_ledger e
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.event_date <= @as_of
 GROUP BY e.category_id;

-- name: AssetsExist :one
SELECT EXISTS (SELECT 1 FROM fa_assets WHERE tenant_id = @tenant_id AND property_id = @property_id)::boolean;

-- name: LedgerDrift :many
-- Assets whose state differs from the sum of their ledger rows (active assets; a cancelled one must net to zero).
SELECT a.id, a.number, a.status, a.cost, a.accum_depr, a.dep_months_done,
       COALESCE(SUM(e.cost_delta), 0)::numeric(18,3) AS ledger_cost, COALESCE(SUM(e.accum_delta), 0)::numeric(18,3) AS ledger_accum, COALESCE(SUM(e.months_delta), 0)::int AS ledger_months
  FROM fa_assets a
  LEFT JOIN fa_ledger e ON e.property_id = a.property_id AND e.asset_id = a.id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status IN ('ACTIVE', 'CANCELLED')
 GROUP BY a.id
HAVING CASE WHEN a.status = 'ACTIVE' THEN (a.cost <> COALESCE(SUM(e.cost_delta), 0) OR a.accum_depr <> COALESCE(SUM(e.accum_delta), 0) OR a.dep_months_done <> COALESCE(SUM(e.months_delta), 0))
            ELSE (COALESCE(SUM(e.cost_delta), 0) <> 0 OR COALESCE(SUM(e.accum_delta), 0) <> 0 OR COALESCE(SUM(e.months_delta), 0) <> 0) END
 ORDER BY a.number;

-- name: ListActiveAssetsForRebuild :many
SELECT id FROM fa_assets WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ACTIVE' ORDER BY id;

-- name: ListAssetsInInactiveLocations :many
SELECT a.id, a.number, l.name AS location_name
  FROM fa_assets a JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status = 'ACTIVE' AND NOT l.is_active
 ORDER BY a.number;

-- ---------------------------------------------------------------------------------------------------------------------
-- The one writer of the ledger and of the state of an asset (ledger.go)

-- name: LedgerInsertAsset :one
INSERT INTO fa_assets (tenant_id, property_id, number, name, category_id, location_id, department_id, supplier_id, source, opening_import_id, acquisition_date, in_service_date,
                       start_rule, first_dep_month, cost, residual_value, life_months, method, db_factor, fiscal_group, fiscal_method, fiscal_accum_opening, fiscal_months_done_opening,
                       fiscal_opening_as_of, fiscal_opening_estimated, unit_count, serial_no, warranty_until, notes, label_code, accum_depr, dep_months_done, last_dep_month,
                       created_by, updated_by)
VALUES (@tenant_id, @property_id, @number, @name, @category_id, sqlc.narg(location_id), sqlc.narg(department_id), sqlc.narg(supplier_id), @source, sqlc.narg(opening_import_id), @acquisition_date,
        @in_service_date, @start_rule, sqlc.narg(first_dep_month), @cost, @residual_value, sqlc.narg(life_months), sqlc.narg(method), sqlc.narg(db_factor), @fiscal_group,
        sqlc.narg(fiscal_method), sqlc.narg(fiscal_accum_opening), sqlc.narg(fiscal_months_done_opening), sqlc.narg(fiscal_opening_as_of), @fiscal_opening_estimated, @unit_count,
        sqlc.narg(serial_no), sqlc.narg(warranty_until), sqlc.narg(notes), @label_code, @accum_depr, @dep_months_done, sqlc.narg(last_dep_month), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: LedgerInsertEntry :one
INSERT INTO fa_ledger (tenant_id, property_id, asset_id, category_id, kind, event_date, book_month, cost_delta, accum_delta, months_delta, journal_id, source_type, source_id, source_line,
                       reverses_id, created_by)
VALUES (@tenant_id, @property_id, @asset_id, @category_id, @kind, @event_date, sqlc.narg(book_month), @cost_delta, @accum_delta, @months_delta, sqlc.narg(journal_id), @source_type, @source_id,
        @source_line, sqlc.narg(reverses_id), sqlc.narg(actor_id))
RETURNING id;

-- name: LedgerSetCancelled :exec
UPDATE fa_assets SET status = 'CANCELLED', version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: LedgerRestateAsset :exec
UPDATE fa_assets SET cost = @cost, accum_depr = @accum_depr, dep_months_done = @dep_months_done, last_dep_month = sqlc.narg(last_dep_month), version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: LedgerSumsOfAsset :one
SELECT COALESCE(SUM(e.cost_delta), 0)::numeric(18,3) AS cost, COALESCE(SUM(e.accum_delta), 0)::numeric(18,3) AS accum, COALESCE(SUM(e.months_delta), 0)::int AS months,
       COALESCE(MAX(e.book_month) FILTER (WHERE e.kind IN ('OPEN', 'DEPRECIATE') AND e.months_delta > 0
                                          AND NOT EXISTS (SELECT 1 FROM fa_ledger r WHERE r.property_id = e.property_id AND r.reverses_id = e.id)), DATE '1900-01-01')::date AS last_dep_month
  FROM fa_ledger e WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.asset_id = @asset_id;

-- name: LedgerOpenEntry :one
SELECT * FROM fa_ledger WHERE tenant_id = @tenant_id AND property_id = @property_id AND asset_id = @asset_id AND kind IN ('OPEN', 'ACQUIRE') ORDER BY id LIMIT 1;

-- ---------------------------------------------------------------------------------------------------------------------
-- Opening imports

-- name: InsertOpeningImport :one
INSERT INTO fa_opening_imports (tenant_id, property_id, number, as_of, mode, journal_id, asset_count, total_cost, total_accum, file_hash, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @number, @as_of, @mode, sqlc.narg(journal_id), @asset_count, @total_cost, @total_accum, sqlc.narg(file_hash), sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING *;

-- name: GetOpeningImportByKey :one
SELECT * FROM fa_opening_imports WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: GetOpeningImport :one
SELECT * FROM fa_opening_imports WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListOpeningImports :many
SELECT i.*, j.journal_number FROM fa_opening_imports i LEFT JOIN gl_journals j ON j.property_id = i.property_id AND j.id = i.journal_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id ORDER BY i.id DESC LIMIT 100;

-- name: ListAssetsOfImport :many
SELECT id, number, name, category_id, cost, accum_depr FROM fa_assets WHERE tenant_id = @tenant_id AND property_id = @property_id AND opening_import_id = @import_id ORDER BY id;

-- name: LedgerUpdateStart :exec
-- The start of the depreciation, free until the asset has been depreciated (the trigger fa_assets_start_locked is the backstop).
UPDATE fa_assets SET in_service_date = @in_service_date, start_rule = @start_rule, first_dep_month = sqlc.narg(first_dep_month), version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- ---------------------------------------------------------------------------------------------------------------------
-- Acquisition from a journal line (acquire.go)

-- name: ListWaitingLines :many
-- The journal lines that debit the cost account of an active category and are not an asset yet (37-fixed-assets.md 4.2.1). A line of a journal that is reversed, of a reversal, of the register itself
-- (type ASSET) or of a closing is never in the list. The cost is the debit of the line (F7). dismissed = true lists the lines marked "not an asset" instead.
WITH w AS (
  SELECT l.id AS line_id, l.journal_id, j.journal_number, j.journal_type, j.journal_date, j.description AS journal_description, l.line_no, l.account_id,
         ga.code AS account_code, ga.name AS account_name, l.debit AS cost, l.description, l.source_type, l.source_ref, l.source_line, l.department_id,
         c.id AS category_id, c.code AS category_code, c.name AS category_name, c.kind AS category_kind,
         b.id AS bill_id, b.bill_number, b.supplier_invoice_number, s.id AS supplier_id, s.code AS supplier_code, s.name AS supplier_name, bl.vat_treatment AS bill_vat_treatment,
         CASE WHEN l.source_type = 'AP_BILL' THEN 'BILL' WHEN j.journal_type = 'CASHIER' THEN 'CASHIER' WHEN j.journal_type = 'BANK' THEN 'BANK'
              WHEN j.journal_type = 'MANUAL' THEN 'MANUAL' ELSE 'OTHER' END::text AS source,
         (d.id IS NOT NULL)::boolean AS dismissed, d.reason AS dismissed_reason, d.dismissed_at
    FROM gl_journal_lines l
    JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
    JOIN gl_accounts ga ON ga.property_id = l.property_id AND ga.id = l.account_id
    JOIN fa_categories c ON c.property_id = l.property_id AND c.cost_account_id = l.account_id AND c.is_active
    JOIN fa_settings st ON st.property_id = l.property_id
    LEFT JOIN supplier_bills b ON b.property_id = l.property_id AND b.journal_id = l.journal_id AND l.source_type = 'AP_BILL'
    LEFT JOIN suppliers s ON s.property_id = b.property_id AND s.id = b.supplier_id
    LEFT JOIN supplier_bill_lines bl ON bl.bill_id = b.id AND bl.line_no = l.source_line
    LEFT JOIN fa_source_dismissals d ON d.property_id = l.property_id AND d.journal_line_id = l.id AND d.restored_at IS NULL
   WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.debit > 0
     AND j.journal_type NOT IN ('ASSET', 'REVERSAL', 'CLOSING') AND j.reverses_journal_id IS NULL
     AND COALESCE(l.source_type, '') <> 'ASSET'
     AND NOT EXISTS (SELECT 1 FROM gl_journals r WHERE r.property_id = j.property_id AND r.reverses_journal_id = j.id)
     AND st.register_from IS NOT NULL AND j.journal_date >= st.register_from
     AND NOT (c.kind = 'OE' AND st.oe_policy = 'EXPENSE')
     AND NOT EXISTS (SELECT 1 FROM fa_source_links k WHERE k.property_id = l.property_id AND k.journal_line_id = l.id AND k.released_at IS NULL)
     AND ((d.id IS NOT NULL) = @dismissed::boolean)
)
SELECT w.*, count(*) OVER ()::int AS total_rows, COALESCE(sum(w.cost) OVER (), 0)::numeric(18,3) AS total_cost
  FROM w
 WHERE (sqlc.narg(source)::text IS NULL OR w.source = sqlc.narg(source))
   AND (sqlc.narg(category_id)::bigint IS NULL OR w.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(date_from)::date IS NULL OR w.journal_date >= sqlc.narg(date_from))
   AND (sqlc.narg(date_to)::date IS NULL OR w.journal_date <= sqlc.narg(date_to))
   AND (sqlc.narg(q)::text IS NULL OR w.description ILIKE '%' || sqlc.narg(q) || '%' OR w.journal_number ILIKE '%' || sqlc.narg(q) || '%' OR w.supplier_name ILIKE '%' || sqlc.narg(q) || '%'
        OR w.bill_number ILIKE '%' || sqlc.narg(q) || '%' OR w.supplier_invoice_number ILIKE '%' || sqlc.narg(q) || '%')
 ORDER BY w.journal_date, w.line_id
 LIMIT @row_limit OFFSET @row_offset;

-- name: GetJournalLineForAsset :one
-- One journal line with what "make asset" must know about it, whether it qualifies or not.
SELECT l.id AS line_id, l.journal_id, j.journal_number, j.journal_type, j.journal_date, l.line_no, l.account_id, l.debit, l.description, l.source_type, l.source_ref, l.source_line, l.department_id,
       (j.reverses_journal_id IS NOT NULL OR EXISTS (SELECT 1 FROM gl_journals r WHERE r.property_id = j.property_id AND r.reverses_journal_id = j.id))::boolean AS is_void,
       b.id AS bill_id, b.supplier_id AS bill_supplier_id
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
  LEFT JOIN supplier_bills b ON b.property_id = l.property_id AND b.journal_id = l.journal_id AND l.source_type = 'AP_BILL'
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.id = @line_id;

-- name: GetCategoryByCostAccount :one
SELECT * FROM fa_categories WHERE tenant_id = @tenant_id AND property_id = @property_id AND cost_account_id = @account_id AND is_active LIMIT 1;

-- name: CountLiveLinksOfLine :one
SELECT count(*)::int FROM fa_source_links WHERE property_id = @property_id AND journal_line_id = @line_id AND released_at IS NULL;

-- name: GetLiveDismissal :one
SELECT id FROM fa_source_dismissals WHERE property_id = @property_id AND journal_line_id = @line_id AND restored_at IS NULL;

-- name: InsertDismissal :exec
INSERT INTO fa_source_dismissals (tenant_id, property_id, journal_line_id, reason, dismissed_by) VALUES (@tenant_id, @property_id, @line_id, @reason, sqlc.narg(actor_id));

-- name: RestoreDismissal :execrows
UPDATE fa_source_dismissals SET restored_at = now(), restored_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND journal_line_id = @line_id AND restored_at IS NULL;

-- name: ListLinksByKey :many
SELECT * FROM fa_source_links WHERE property_id = @property_id AND idempotency_key = @idempotency_key ORDER BY unit_no;

-- name: InsertSourceLink :exec
INSERT INTO fa_source_links (tenant_id, property_id, journal_line_id, unit_no, asset_id, cost, kind, bill_id, bill_line_no, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @journal_line_id, @unit_no, @asset_id, @cost, @kind, sqlc.narg(bill_id), sqlc.narg(bill_line_no), sqlc.narg(idempotency_key), sqlc.narg(actor_id));

-- name: ReleaseLinksOfAsset :exec
UPDATE fa_source_links SET released_at = now(), released_by = sqlc.narg(actor_id) WHERE property_id = @property_id AND asset_id = @asset_id AND released_at IS NULL;

-- name: GetLiveLinkOfAsset :one
SELECT k.journal_line_id, k.unit_no, k.cost, k.bill_id, k.bill_line_no, j.id AS journal_id, j.journal_number, j.journal_date, j.journal_type, l.source_type, b.bill_number,
       (SELECT count(*) FROM fa_source_links o WHERE o.property_id = k.property_id AND o.journal_line_id = k.journal_line_id AND o.released_at IS NULL)::int AS units
  FROM fa_source_links k
  JOIN gl_journal_lines l ON l.property_id = k.property_id AND l.id = k.journal_line_id
  JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
  LEFT JOIN supplier_bills b ON b.id = k.bill_id
 WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.asset_id = @asset_id AND k.released_at IS NULL AND k.kind = 'UNIT';

-- name: GuardBillAssets :many
-- The live assets made from the lines of a bill (line_no 0: any line), whether the link knows its bill line or only its journal (a bill posted before migration 00094).
SELECT a.number, k.bill_line_no
  FROM fa_source_links k
  JOIN fa_assets a ON a.property_id = k.property_id AND a.id = k.asset_id
  JOIN gl_journal_lines l ON l.property_id = k.property_id AND l.id = k.journal_line_id
  JOIN supplier_bills b ON b.property_id = k.property_id AND b.journal_id = l.journal_id
 WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND b.id = @bill_id AND k.released_at IS NULL
   AND (@line_no::int = 0 OR k.bill_line_no = @line_no OR k.bill_line_no IS NULL)
 ORDER BY a.number;

-- name: JournalDebitsCostAccount :one
-- Whether the journal has a debit line on the cost account of a category (the only journals the register can hold a link to).
SELECT EXISTS (SELECT 1 FROM gl_journal_lines l JOIN fa_categories c ON c.property_id = l.property_id AND c.cost_account_id = l.account_id
                WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.journal_id = @journal_id AND l.debit > 0)::boolean;

-- name: GuardJournalAssets :many
SELECT a.number, l.line_no
  FROM fa_source_links k
  JOIN fa_assets a ON a.property_id = k.property_id AND a.id = k.asset_id
  JOIN gl_journal_lines l ON l.property_id = k.property_id AND l.id = k.journal_line_id
 WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND l.journal_id = @journal_id AND k.released_at IS NULL
 ORDER BY a.number;

-- name: WaitingTotals :one
-- What waits in the list, for the badge: the lines and their cost, the dismissed ones apart.
SELECT count(*) FILTER (WHERE NOT dismissed)::int AS waiting, COALESCE(sum(cost) FILTER (WHERE NOT dismissed), 0)::numeric(18,3) AS waiting_cost,
       count(*) FILTER (WHERE dismissed)::int AS dismissed, COALESCE(sum(cost) FILTER (WHERE dismissed), 0)::numeric(18,3) AS dismissed_cost
  FROM (
    SELECT l.debit AS cost, (d.id IS NOT NULL) AS dismissed
      FROM gl_journal_lines l
      JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
      JOIN fa_categories c ON c.property_id = l.property_id AND c.cost_account_id = l.account_id AND c.is_active
      JOIN fa_settings st ON st.property_id = l.property_id
      LEFT JOIN fa_source_dismissals d ON d.property_id = l.property_id AND d.journal_line_id = l.id AND d.restored_at IS NULL
     WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.debit > 0
       AND j.journal_type NOT IN ('ASSET', 'REVERSAL', 'CLOSING') AND j.reverses_journal_id IS NULL AND COALESCE(l.source_type, '') <> 'ASSET'
       AND NOT EXISTS (SELECT 1 FROM gl_journals r WHERE r.property_id = j.property_id AND r.reverses_journal_id = j.id)
       AND st.register_from IS NOT NULL AND j.journal_date >= st.register_from AND NOT (c.kind = 'OE' AND st.oe_policy = 'EXPENSE')
       AND NOT EXISTS (SELECT 1 FROM fa_source_links k WHERE k.property_id = l.property_id AND k.journal_line_id = l.id AND k.released_at IS NULL)
  ) x;

-- ---------------------------------------------------------------------------------------------------------------------
-- Depreciation: the runs, their lines and the month close (depreciation.go)

-- name: ListDepreciationAssets :many
-- The active assets that have months left to depreciate, with what the run needs of their category. The run decides who is due in its month. An asset whose disposal waits for an approver is left out (the month of a disposal is not depreciated, Q5); if the request is rejected the next run catches the asset up.
SELECT a.id, a.number, a.name, a.category_id, a.department_id, a.cost, a.residual_value, a.accum_depr, a.life_months, a.method, a.db_factor, a.dep_months_done, a.last_dep_month, a.first_dep_month,
       c.code AS category_code, c.name AS category_name, c.expense_account_id, c.accumulated_account_id, c.department_id AS category_department_id
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status = 'ACTIVE' AND a.life_months IS NOT NULL AND a.method IS NOT NULL AND a.dep_months_done < a.life_months
   AND NOT EXISTS (SELECT 1 FROM fa_disposal_requests r WHERE r.property_id = a.property_id AND r.asset_id = a.id AND r.status = 'PENDING')
 ORDER BY a.id;

-- name: LastLiveRun :one
SELECT * FROM fa_depr_runs WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'POSTED' ORDER BY period_month DESC LIMIT 1;

-- name: GetLiveRunByMonth :one
SELECT * FROM fa_depr_runs WHERE tenant_id = @tenant_id AND property_id = @property_id AND period_month = @period_month AND status = 'POSTED';

-- name: GetRunByKey :one
SELECT * FROM fa_depr_runs WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ListLiveRunsFrom :many
-- The posted runs of a month and of the months after it, the latest first (the reopen of a month reverses them in that order).
SELECT * FROM fa_depr_runs WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'POSTED' AND period_month >= @period_month ORDER BY period_month DESC;

-- name: ListRuns :many
SELECT r.id, r.period_month, r.status, r.journal_id, j.journal_number, r.reversal_journal_id, rj.journal_number AS reversal_journal_number, r.total, r.asset_count, r.run_by, r.run_at,
       r.reversed_by, r.reversed_at, r.reversal_reason
  FROM fa_depr_runs r
  JOIN gl_journals j ON j.property_id = r.property_id AND j.id = r.journal_id
  LEFT JOIN gl_journals rj ON rj.property_id = r.property_id AND rj.id = r.reversal_journal_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
 ORDER BY r.period_month DESC, r.id DESC
 LIMIT 600;

-- name: GetRun :one
SELECT r.id, r.period_month, r.status, r.journal_id, j.journal_number, r.reversal_journal_id, rj.journal_number AS reversal_journal_number, r.total, r.asset_count, r.run_by, r.run_at,
       r.reversed_by, r.reversed_at, r.reversal_reason
  FROM fa_depr_runs r
  JOIN gl_journals j ON j.property_id = r.property_id AND j.id = r.journal_id
  LEFT JOIN gl_journals rj ON rj.property_id = r.property_id AND rj.id = r.reversal_journal_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: InsertRun :one
INSERT INTO fa_depr_runs (tenant_id, property_id, period_month, status, journal_id, total, asset_count, idempotency_key, run_by)
VALUES (@tenant_id, @property_id, @period_month, 'POSTED', @journal_id, @total, @asset_count, sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertRunLine :one
INSERT INTO fa_depr_lines (tenant_id, property_id, run_id, asset_id, category_id, expense_account_id, department_id, months, from_month, amount, accum_after, carrying_after, months_done_after)
VALUES (@tenant_id, @property_id, @run_id, @asset_id, @category_id, @expense_account_id, sqlc.narg(department_id), @months, @from_month, @amount, @accum_after, @carrying_after, @months_done_after)
RETURNING id;

-- name: MarkRunReversed :exec
UPDATE fa_depr_runs SET status = 'REVERSED', reversal_journal_id = @reversal_journal_id, reversed_by = sqlc.narg(actor_id), reversed_at = @reversed_at, reversal_reason = @reason
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'POSTED';

-- name: ListRunLines :many
SELECT l.id, l.asset_id, a.number AS asset_number, a.name AS asset_name, l.category_id, c.code AS category_code, c.name AS category_name, l.expense_account_id, ea.code AS expense_account_code,
       l.department_id, d.code AS department_code, d.name AS department_name, l.months, l.from_month, l.amount, l.accum_after, l.carrying_after, l.months_done_after
  FROM fa_depr_lines l
  JOIN fa_assets a ON a.property_id = l.property_id AND a.id = l.asset_id
  JOIN fa_categories c ON c.property_id = l.property_id AND c.id = l.category_id
  JOIN gl_accounts ea ON ea.property_id = l.property_id AND ea.id = l.expense_account_id
  LEFT JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.run_id = @run_id
 ORDER BY l.asset_id;

-- name: LedgerEntryOfRunLine :one
-- The row of the ledger that a line of a run wrote; a reversal of the run negates it.
SELECT * FROM fa_ledger WHERE tenant_id = @tenant_id AND property_id = @property_id AND source_type = 'FA_DEPR' AND source_id = @line_id AND kind = 'DEPRECIATE';

-- name: LaterAssetEvents :many
-- The assets of a run that were disposed of or moved to another category after the month end: their depreciation of the month cannot be reversed (F9).
SELECT DISTINCT a.number
  FROM fa_ledger e JOIN fa_assets a ON a.property_id = e.property_id AND a.id = e.asset_id
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.asset_id = ANY(@asset_ids::bigint[]) AND e.kind IN ('DISPOSE', 'RECLASS_OUT', 'RECLASS_IN') AND e.event_date > @month_end
   AND NOT EXISTS (SELECT 1 FROM fa_ledger r WHERE r.property_id = e.property_id AND r.reverses_id = e.id)
 ORDER BY a.number;

-- name: DepartmentNames :many
SELECT id, code, name FROM departments WHERE property_id = @property_id AND id = ANY(@ids::bigint[]);

-- name: ListPeriodStates :many
SELECT period_start, status FROM gl_periods WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: CountAssetsDueIn :one
-- The assets a run of the month would depreciate (the month close asks): active, months left, a carrying value above the residual value, and a first due month at or before the month.
SELECT count(*)::int
  FROM fa_assets a
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status = 'ACTIVE' AND a.life_months IS NOT NULL AND a.dep_months_done < a.life_months
   AND a.cost - a.accum_depr > a.residual_value
   AND NOT EXISTS (SELECT 1 FROM fa_disposal_requests r WHERE r.property_id = a.property_id AND r.asset_id = a.id AND r.status = 'PENDING')
   AND GREATEST(CASE WHEN a.dep_months_done > 0 THEN (a.last_dep_month + INTERVAL '1 month')::date ELSE a.first_dep_month END, @register_from::date) <= @month::date;

-- name: LiveRunFrom :one
SELECT count(*)::int FROM fa_depr_runs WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'POSTED' AND period_month >= @period_month;

-- name: ListDeprLinesOfAsset :many
-- The lines of the live runs for one asset: what the schedule shows as posted.
SELECT l.id, l.run_id, r.period_month, j.journal_number, l.from_month, l.months, l.amount, l.accum_after, l.carrying_after, l.months_done_after
  FROM fa_depr_lines l
  JOIN fa_depr_runs r ON r.property_id = l.property_id AND r.id = l.run_id AND r.status = 'POSTED'
  JOIN gl_journals j ON j.property_id = r.property_id AND j.id = r.journal_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.asset_id = @asset_id
 ORDER BY r.period_month;

-- ---------------------------------------------------------------------------------------------------------------------
-- Moves and revisions (changes.go)

-- name: LedgerMoveAsset :exec
-- Where the asset stands and which department it belongs to (the history is fa_moves).
UPDATE fa_assets SET location_id = sqlc.narg(location_id), department_id = sqlc.narg(department_id), version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: InsertMove :one
INSERT INTO fa_moves (tenant_id, property_id, asset_id, effective_on, from_location_id, to_location_id, from_department_id, to_department_id, reason, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @asset_id, @effective_on, sqlc.narg(from_location_id), sqlc.narg(to_location_id), sqlc.narg(from_department_id), sqlc.narg(to_department_id), @reason,
        sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: ListMovesByKey :many
SELECT asset_id FROM fa_moves WHERE property_id = @property_id AND idempotency_key = @idempotency_key ORDER BY asset_id;

-- name: ListMovesOfAssets :many
-- Every move of the assets, in the order they happened: the department of an asset at a date is read from them (4.6).
SELECT asset_id, effective_on, from_department_id, to_department_id
  FROM fa_moves WHERE tenant_id = @tenant_id AND property_id = @property_id AND asset_id = ANY(@asset_ids::bigint[]) ORDER BY asset_id, effective_on, id;

-- name: ListMovesOfAsset :many
SELECT m.id, m.effective_on, m.reason, m.created_at, m.created_by, u.full_name AS created_by_name,
       m.from_location_id, fl.name AS from_location_name, m.to_location_id, tl.name AS to_location_name,
       m.from_department_id, fd.name AS from_department_name, m.to_department_id, td.name AS to_department_name
  FROM fa_moves m
  LEFT JOIN fa_locations fl ON fl.property_id = m.property_id AND fl.id = m.from_location_id
  LEFT JOIN fa_locations tl ON tl.property_id = m.property_id AND tl.id = m.to_location_id
  LEFT JOIN departments fd ON fd.property_id = m.property_id AND fd.id = m.from_department_id
  LEFT JOIN departments td ON td.property_id = m.property_id AND td.id = m.to_department_id
  LEFT JOIN users u ON u.id = m.created_by
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.asset_id = @asset_id
 ORDER BY m.effective_on, m.id;

-- name: LedgerReviseAsset :exec
-- The life, the residual value and the method of an asset, from the next run (a revision, 4.5).
UPDATE fa_assets SET life_months = sqlc.narg(life_months), residual_value = @residual_value, method = sqlc.narg(method), db_factor = sqlc.narg(db_factor), version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: LedgerReclassAsset :exec
-- The asset in its new category: the category, what depreciation it has (a construction in progress that is finished gets its life, method and start) and its fiscal group.
UPDATE fa_assets SET category_id = @category_id, life_months = sqlc.narg(life_months), method = sqlc.narg(method), db_factor = sqlc.narg(db_factor), residual_value = @residual_value,
       in_service_date = @in_service_date, start_rule = @start_rule, first_dep_month = sqlc.narg(first_dep_month), fiscal_group = @fiscal_group, fiscal_method = sqlc.narg(fiscal_method),
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: InsertRevision :one
INSERT INTO fa_revisions (tenant_id, property_id, asset_id, kind, effective_on, old_value, new_value, bill_id, bill_line_no, journal_line_id, journal_id, reason, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @asset_id, @kind, @effective_on, @old_value, @new_value, sqlc.narg(bill_id), sqlc.narg(bill_line_no), sqlc.narg(journal_line_id), sqlc.narg(journal_id), @reason,
        sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: GetRevisionByKey :one
SELECT * FROM fa_revisions WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ListRevisionsOfAsset :many
SELECT r.id, r.kind, r.effective_on, r.old_value, r.new_value, r.journal_id, j.journal_number, r.reason, r.created_at, r.created_by, u.full_name AS created_by_name
  FROM fa_revisions r
  LEFT JOIN gl_journals j ON j.property_id = r.property_id AND j.id = r.journal_id
  LEFT JOIN users u ON u.id = r.created_by
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.asset_id = @asset_id
 ORDER BY r.effective_on, r.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Disposals (37-fixed-assets.md 4.7, 4.8)

-- name: InsertDisposalRequest :one
INSERT INTO fa_disposal_requests (tenant_id, property_id, asset_id, kind, disposal_date, units, proceeds, proceeds_account_id, reason_code, note, approval_amount, requested_by, idempotency_key)
VALUES (@tenant_id, @property_id, @asset_id, @kind, @disposal_date, @units, @proceeds, sqlc.narg(proceeds_account_id), @reason_code, sqlc.narg(note), @approval_amount, sqlc.narg(actor_id),
        sqlc.narg(idempotency_key))
RETURNING id;

-- name: GetDisposalRequest :one
SELECT r.*, a.number AS asset_number, a.name AS asset_name, ru.full_name AS requested_by_name, du.full_name AS decided_by_name, pa.code AS proceeds_account_code, pa.name AS proceeds_account_name,
       d.number AS disposal_number
  FROM fa_disposal_requests r
  JOIN fa_assets a ON a.property_id = r.property_id AND a.id = r.asset_id
  LEFT JOIN users ru ON ru.id = r.requested_by
  LEFT JOIN users du ON du.id = r.decided_by
  LEFT JOIN gl_accounts pa ON pa.property_id = r.property_id AND pa.id = r.proceeds_account_id
  LEFT JOIN fa_disposals d ON d.property_id = r.property_id AND d.id = r.disposal_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: GetDisposalRequestByKey :one
SELECT id, asset_id FROM fa_disposal_requests WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: GetPendingRequestOfAsset :one
SELECT id FROM fa_disposal_requests WHERE tenant_id = @tenant_id AND property_id = @property_id AND asset_id = @asset_id AND status = 'PENDING';

-- name: ListDisposalRequests :many
SELECT r.*, a.number AS asset_number, a.name AS asset_name, ru.full_name AS requested_by_name, du.full_name AS decided_by_name, pa.code AS proceeds_account_code, pa.name AS proceeds_account_name,
       d.number AS disposal_number
  FROM fa_disposal_requests r
  JOIN fa_assets a ON a.property_id = r.property_id AND a.id = r.asset_id
  LEFT JOIN users ru ON ru.id = r.requested_by
  LEFT JOIN users du ON du.id = r.decided_by
  LEFT JOIN gl_accounts pa ON pa.property_id = r.property_id AND pa.id = r.proceeds_account_id
  LEFT JOIN fa_disposals d ON d.property_id = r.property_id AND d.id = r.disposal_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status))
 ORDER BY r.id DESC LIMIT 200;

-- name: DecideDisposalRequest :exec
UPDATE fa_disposal_requests SET status = @status, decided_by = sqlc.narg(actor_id), decided_at = @decided_at, decision_note = sqlc.narg(decision_note), disposal_id = sqlc.narg(disposal_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'PENDING';

-- name: InsertDisposal :one
INSERT INTO fa_disposals (tenant_id, property_id, number, asset_id, kind, disposal_date, units, cost_removed, accum_removed, carrying_removed, proceeds, proceeds_account_id, gain_loss, reason_code, note,
                          journal_id, approved_by, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @number, @asset_id, @kind, @disposal_date, @units, @cost_removed, @accum_removed, @carrying_removed, @proceeds, sqlc.narg(proceeds_account_id), @gain_loss, @reason_code,
        sqlc.narg(note), @journal_id, sqlc.narg(approved_by), sqlc.narg(idempotency_key), sqlc.narg(actor_id))
RETURNING id;

-- name: GetDisposal :one
SELECT d.*, a.number AS asset_number, a.name AS asset_name, j.journal_number, rj.journal_number AS reversal_journal_number, cu.full_name AS created_by_name, au.full_name AS approved_by_name,
       ru.full_name AS reversed_by_name, pa.code AS proceeds_account_code, pa.name AS proceeds_account_name
  FROM fa_disposals d
  JOIN fa_assets a ON a.property_id = d.property_id AND a.id = d.asset_id
  JOIN gl_journals j ON j.property_id = d.property_id AND j.id = d.journal_id
  LEFT JOIN gl_journals rj ON rj.property_id = d.property_id AND rj.id = d.reversal_journal_id
  LEFT JOIN users cu ON cu.id = d.created_by
  LEFT JOIN users au ON au.id = d.approved_by
  LEFT JOIN users ru ON ru.id = d.reversed_by
  LEFT JOIN gl_accounts pa ON pa.property_id = d.property_id AND pa.id = d.proceeds_account_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.id = @id;

-- name: GetDisposalByKey :one
SELECT id, asset_id FROM fa_disposals WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @idempotency_key;

-- name: ListDisposals :many
SELECT d.*, a.number AS asset_number, a.name AS asset_name, j.journal_number, rj.journal_number AS reversal_journal_number, cu.full_name AS created_by_name, au.full_name AS approved_by_name,
       ru.full_name AS reversed_by_name, pa.code AS proceeds_account_code, pa.name AS proceeds_account_name
  FROM fa_disposals d
  JOIN fa_assets a ON a.property_id = d.property_id AND a.id = d.asset_id
  JOIN gl_journals j ON j.property_id = d.property_id AND j.id = d.journal_id
  LEFT JOIN gl_journals rj ON rj.property_id = d.property_id AND rj.id = d.reversal_journal_id
  LEFT JOIN users cu ON cu.id = d.created_by
  LEFT JOIN users au ON au.id = d.approved_by
  LEFT JOIN users ru ON ru.id = d.reversed_by
  LEFT JOIN gl_accounts pa ON pa.property_id = d.property_id AND pa.id = d.proceeds_account_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND (sqlc.narg(asset_id)::bigint IS NULL OR d.asset_id = sqlc.narg(asset_id))
 ORDER BY d.id DESC LIMIT 200;

-- name: MarkDisposalReversed :exec
UPDATE fa_disposals SET status = 'REVERSED', reversal_journal_id = @reversal_journal_id, reversed_by = sqlc.narg(actor_id), reversed_at = @reversed_at, reversal_reason = @reversal_reason
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'POSTED';

-- name: LedgerDisposeEntry :one
-- The row of the ledger a disposal wrote (it is reversed by a REVERSAL row).
SELECT * FROM fa_ledger WHERE tenant_id = @tenant_id AND property_id = @property_id AND kind = 'DISPOSE' AND source_type = 'FA_DISPOSAL' AND source_id = @disposal_id;

-- name: LedgerSetDisposed :exec
-- A whole asset leaves the register: the status and the day. Its state (cost, accumulated amount) stays as it was the day it left; the ledger holds the DISPOSE row that takes it to zero.
UPDATE fa_assets SET status = 'DISPOSED', disposed_on = @disposed_on, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: LedgerSetUnits :exec
-- The pieces of a batch that are left after a partial disposal (or back after its reversal).
UPDATE fa_assets SET unit_count = @unit_count, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: LedgerReinstate :exec
-- The reversal of the disposal of a whole asset: it is active again (the caller restates it from the ledger).
UPDATE fa_assets SET status = 'ACTIVE', disposed_on = NULL, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'DISPOSED';

-- name: CountPostedDisposals :one
SELECT count(*)::int FROM fa_disposals WHERE tenant_id = @tenant_id AND property_id = @property_id AND asset_id = @asset_id AND status = 'POSTED';

-- name: GetProceedsAccount :one
-- An account that may receive the proceeds of a sale: its type and its statement group (CASH for the cash and bank accounts; 1230 is allowed by its code). Inactive or not postable is no account for it.
SELECT code, account_type, statement_group FROM gl_accounts
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND is_active AND is_postable;

-- name: PKPOnDate :one
-- Whether the property is a taxable entrepreneur (PKP) on a date: the sale of an asset is then subject to VAT, which this module does not handle (4.7). No settings: not PKP.
SELECT COALESCE((SELECT t.is_pkp FROM property_tax_settings t WHERE t.property_id = @property_id AND t.effective_from <= @on_date::date ORDER BY t.effective_from DESC, t.id DESC LIMIT 1), false)::boolean;

-- name: LaterEntriesOfAsset :one
-- What happened to the asset after a disposal that has not been undone: another disposal, a value added or a change of category. The disposal cannot be reversed over them.
SELECT count(*)::int FROM fa_ledger e
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.asset_id = @asset_id AND e.kind IN ('DISPOSE', 'RECLASS_OUT', 'RECLASS_IN', 'CAPITALIZE')
   AND e.id > (SELECT d.id FROM fa_ledger d WHERE d.property_id = @property_id AND d.kind = 'DISPOSE' AND d.source_type = 'FA_DISPOSAL' AND d.source_id = @disposal_id)
   AND NOT EXISTS (SELECT 1 FROM fa_ledger r WHERE r.property_id = e.property_id AND r.reverses_id = e.id);

-- ---------------------------------------------------------------------------------------------------------------------
-- Reports and the fiscal book (reports.go, fiscalbook.go; PR 4)

-- name: FiscalBookAssets :many
-- The assets that have a fiscal group (land and construction in progress have none), cancelled ones left out.
SELECT a.id, a.number, a.name, a.category_id, c.code AS category_code, c.name AS category_name, a.source, a.status, a.acquisition_date, a.disposed_on, a.fiscal_group, a.fiscal_method,
       a.fiscal_accum_opening, a.fiscal_months_done_opening, a.fiscal_opening_as_of, a.fiscal_opening_estimated
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.fiscal_group <> 'NONE' AND a.status <> 'CANCELLED'
   AND (sqlc.narg(category_id)::bigint IS NULL OR a.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(asset_id)::bigint IS NULL OR a.id = sqlc.narg(asset_id))
 ORDER BY a.number;

-- name: FiscalBookLayers :many
-- The cost layers of the fiscal book: what the acquisition (or the opening balance) and each value added brought, a reversal counted at the date of the row it negates.
SELECT e.asset_id, COALESCE(o.kind, e.kind)::text AS layer_kind, COALESCE(o.event_date, e.event_date)::date AS layer_date, COALESCE(SUM(e.cost_delta), 0)::numeric(18,3) AS cost
  FROM fa_ledger e
  LEFT JOIN fa_ledger o ON o.property_id = e.property_id AND o.id = e.reverses_id
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND COALESCE(o.kind, e.kind) IN ('OPEN', 'ACQUIRE', 'CAPITALIZE')
   AND (sqlc.narg(asset_id)::bigint IS NULL OR e.asset_id = sqlc.narg(asset_id))
 GROUP BY e.asset_id, COALESCE(o.kind, e.kind), COALESCE(o.event_date, e.event_date)
 ORDER BY e.asset_id, layer_date;

-- name: FiscalBookDisposals :many
SELECT d.asset_id, d.number, d.disposal_date, d.cost_removed, d.accum_removed, d.carrying_removed, d.proceeds, d.gain_loss
  FROM fa_disposals d
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.status = 'POSTED' AND (sqlc.narg(asset_id)::bigint IS NULL OR d.asset_id = sqlc.narg(asset_id))
 ORDER BY d.disposal_date, d.id;

-- name: CommercialDepreciationByMonth :many
-- The depreciation the runs booked (and took back) per asset and book month.
SELECT e.asset_id, e.book_month::date AS book_month, COALESCE(SUM(e.accum_delta), 0)::numeric(18,3) AS amount
  FROM fa_ledger e
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.source_type = 'FA_DEPR' AND e.book_month IS NOT NULL
   AND (sqlc.narg(asset_id)::bigint IS NULL OR e.asset_id = sqlc.narg(asset_id))
 GROUP BY e.asset_id, e.book_month
HAVING SUM(e.accum_delta) <> 0
 ORDER BY e.asset_id, e.book_month;

-- name: RegisterAsOf :many
-- The register as of a date: each asset with the sums of its ledger up to the date (an asset acquired later has no row).
SELECT a.id, a.number, a.name, a.category_id, c.code AS category_code, c.name AS category_name, c.kind AS category_kind, a.location_id, l.name AS location_name, a.department_id, d.name AS department_name,
       s.name AS supplier_name, a.acquisition_date, a.in_service_date, a.life_months, a.method, a.serial_no, a.warranty_until, a.status, a.disposed_on,
       x.cost, x.accum, x.months_done
  FROM fa_assets a
  JOIN (SELECT e.asset_id, SUM(e.cost_delta)::numeric(18,3) AS cost, SUM(e.accum_delta)::numeric(18,3) AS accum, SUM(e.months_delta)::int AS months_done
          FROM fa_ledger e WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.event_date <= @as_of GROUP BY e.asset_id) x ON x.asset_id = a.id
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
  LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.department_id
  LEFT JOIN suppliers s ON s.property_id = a.property_id AND s.id = a.supplier_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND (x.cost <> 0 OR x.accum <> 0)
   AND (sqlc.narg(category_id)::bigint IS NULL OR a.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(location_id)::bigint IS NULL OR a.location_id = sqlc.narg(location_id))
   AND (sqlc.narg(department_id)::bigint IS NULL OR a.department_id = sqlc.narg(department_id))
 ORDER BY a.number
 LIMIT 10000;

-- name: RecapMovements :many
-- The movement of the register per category in a period, by what the row is (a reversal counts as the row it negates): before the period (the opening), then inside it.
SELECT e.category_id,
       COALESCE(SUM(e.cost_delta) FILTER (WHERE e.event_date < @date_from), 0)::numeric(18,3) AS cost_opening,
       COALESCE(SUM(e.cost_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) IN ('OPEN', 'ACQUIRE', 'CAPITALIZE')), 0)::numeric(18,3) AS cost_additions,
       COALESCE(SUM(e.cost_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) IN ('RECLASS_IN', 'RECLASS_OUT')), 0)::numeric(18,3) AS cost_reclass,
       COALESCE(SUM(e.cost_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) = 'DISPOSE'), 0)::numeric(18,3) AS cost_disposals,
       COALESCE(SUM(e.cost_delta) FILTER (WHERE e.event_date <= @date_to), 0)::numeric(18,3) AS cost_closing,
       COALESCE(SUM(e.accum_delta) FILTER (WHERE e.event_date < @date_from), 0)::numeric(18,3) AS accum_opening,
       COALESCE(SUM(e.accum_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) IN ('OPEN', 'ACQUIRE', 'CAPITALIZE')), 0)::numeric(18,3) AS accum_additions,
       COALESCE(SUM(e.accum_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) = 'DEPRECIATE'), 0)::numeric(18,3) AS accum_depreciation,
       COALESCE(SUM(e.accum_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) IN ('RECLASS_IN', 'RECLASS_OUT')), 0)::numeric(18,3) AS accum_reclass,
       COALESCE(SUM(e.accum_delta) FILTER (WHERE e.event_date BETWEEN @date_from AND @date_to AND COALESCE(o.kind, e.kind) = 'DISPOSE'), 0)::numeric(18,3) AS accum_disposals,
       COALESCE(SUM(e.accum_delta) FILTER (WHERE e.event_date <= @date_to), 0)::numeric(18,3) AS accum_closing
  FROM fa_ledger e
  LEFT JOIN fa_ledger o ON o.property_id = e.property_id AND o.id = e.reverses_id
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id
 GROUP BY e.category_id;

-- name: WaitingByCategory :many
-- The lines of the waiting list (and the dismissed ones) up to a date, per category and source: what the general ledger holds that the register does not.
SELECT c.id AS category_id, (d.id IS NOT NULL)::boolean AS dismissed,
       CASE WHEN l.source_type = 'AP_BILL' THEN 'BILL' WHEN j.journal_type = 'CASHIER' THEN 'CASHIER' WHEN j.journal_type = 'BANK' THEN 'BANK'
            WHEN j.journal_type = 'MANUAL' THEN 'MANUAL' ELSE 'OTHER' END::text AS source,
       count(*)::int AS line_count, COALESCE(SUM(l.debit), 0)::numeric(18,3) AS amount
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
  JOIN fa_categories c ON c.property_id = l.property_id AND c.cost_account_id = l.account_id AND c.is_active
  JOIN fa_settings st ON st.property_id = l.property_id
  LEFT JOIN fa_source_dismissals d ON d.property_id = l.property_id AND d.journal_line_id = l.id AND d.restored_at IS NULL
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.debit > 0 AND j.journal_date <= @as_of
   AND j.journal_type NOT IN ('ASSET', 'REVERSAL', 'CLOSING') AND j.reverses_journal_id IS NULL AND COALESCE(l.source_type, '') <> 'ASSET'
   AND NOT EXISTS (SELECT 1 FROM gl_journals r WHERE r.property_id = j.property_id AND r.reverses_journal_id = j.id)
   AND st.register_from IS NOT NULL AND j.journal_date >= st.register_from AND NOT (c.kind = 'OE' AND st.oe_policy = 'EXPENSE')
   AND NOT EXISTS (SELECT 1 FROM fa_source_links k WHERE k.property_id = l.property_id AND k.journal_line_id = l.id AND k.released_at IS NULL)
 GROUP BY c.id, (d.id IS NOT NULL), 3;

-- name: ManualJournalBalances :many
-- What manual journals (not reversed, not a reversal) hold on accounts up to a date.
SELECT l.account_id, COALESCE(SUM(l.debit - l.credit), 0)::numeric(18,3) AS balance
  FROM gl_journal_lines l
  JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = ANY(@account_ids::bigint[]) AND j.journal_date <= @as_of
   AND j.journal_type = 'MANUAL' AND j.reverses_journal_id IS NULL
   AND NOT EXISTS (SELECT 1 FROM gl_journals r WHERE r.property_id = j.property_id AND r.reverses_journal_id = j.id)
 GROUP BY l.account_id;

-- name: LocationSummary :many
SELECT l.id AS location_id, l.code AS location_code, l.name AS location_name, l.kind AS location_kind, l.is_active, count(a.id)::int AS assets,
       COALESCE(SUM(a.cost), 0)::numeric(18,3) AS cost, COALESCE(SUM(a.accum_depr), 0)::numeric(18,3) AS accum
  FROM fa_locations l
  LEFT JOIN fa_assets a ON a.property_id = l.property_id AND a.location_id = l.id AND a.status = 'ACTIVE'
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id
 GROUP BY l.id
 ORDER BY l.code;

-- name: UnplacedSummary :one
SELECT count(*)::int AS assets, COALESCE(SUM(cost), 0)::numeric(18,3) AS cost, COALESCE(SUM(accum_depr), 0)::numeric(18,3) AS accum
  FROM fa_assets WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ACTIVE' AND location_id IS NULL;

-- name: DepartmentSummary :many
SELECT d.id AS department_id, d.parent_id, d.code AS department_code, d.name AS department_name, count(a.id)::int AS assets,
       COALESCE(SUM(a.cost), 0)::numeric(18,3) AS cost, COALESCE(SUM(a.accum_depr), 0)::numeric(18,3) AS accum
  FROM departments d
  LEFT JOIN fa_assets a ON a.property_id = d.property_id AND a.department_id = d.id AND a.status = 'ACTIVE'
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id
 GROUP BY d.id
 ORDER BY d.code;

-- name: NoDepartmentSummary :one
SELECT count(*)::int AS assets, COALESCE(SUM(cost), 0)::numeric(18,3) AS cost, COALESCE(SUM(accum_depr), 0)::numeric(18,3) AS accum
  FROM fa_assets WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'ACTIVE' AND department_id IS NULL;

-- name: ExpiringAssets :many
-- Active assets whose warranty ends by a date or that have few months of life left (those fully depreciated are among them, with zero months left).
SELECT a.id, a.number, a.name, c.code AS category_code, l.name AS location_name, a.cost, a.accum_depr, a.warranty_until, a.life_months, a.dep_months_done, a.residual_value
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.status = 'ACTIVE'
   AND ((a.warranty_until IS NOT NULL AND a.warranty_until <= @warranty_before::date)
        OR (a.life_months IS NOT NULL AND a.life_months - a.dep_months_done <= @months_left::int))
 ORDER BY a.number
 LIMIT 5000;

-- name: RegisterOnlyByCategory :many
-- What the opening balances in the mode REGISTER_ONLY brought to the register (no journal was made for them), per category, up to a date.
SELECT e.category_id, COALESCE(SUM(e.cost_delta), 0)::numeric(18,3) AS cost, COALESCE(SUM(e.accum_delta), 0)::numeric(18,3) AS accum
  FROM fa_ledger e
  LEFT JOIN fa_ledger o ON o.property_id = e.property_id AND o.id = e.reverses_id
  JOIN fa_assets a ON a.property_id = e.property_id AND a.id = e.asset_id
  JOIN fa_opening_imports i ON i.property_id = a.property_id AND i.id = a.opening_import_id
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND i.mode = 'REGISTER_ONLY' AND COALESCE(o.kind, e.kind) = 'OPEN' AND e.event_date <= @as_of
 GROUP BY e.category_id;
