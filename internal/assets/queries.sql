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
       l.code AS location_code, l.name AS location_name, d.code AS department_code, d.name AS department_name, s.code AS supplier_code, s.name AS supplier_name
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
       l.code AS location_code, l.name AS location_name, d.code AS department_code, d.name AS department_name, s.code AS supplier_code, s.name AS supplier_name
  FROM fa_assets a
  JOIN fa_categories c ON c.property_id = a.property_id AND c.id = a.category_id
  LEFT JOIN fa_locations l ON l.property_id = a.property_id AND l.id = a.location_id
  LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.department_id
  LEFT JOIN suppliers s ON s.property_id = a.property_id AND s.id = a.supplier_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = @id;

-- name: ListAssetsByIDs :many
SELECT a.*, c.code AS category_code, c.name AS category_name, c.kind AS category_kind,
       l.code AS location_code, l.name AS location_name, d.code AS department_code, d.name AS department_name, s.code AS supplier_code, s.name AS supplier_name
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
INSERT INTO fa_source_links (tenant_id, property_id, journal_line_id, unit_no, asset_id, cost, bill_id, bill_line_no, idempotency_key, created_by)
VALUES (@tenant_id, @property_id, @journal_line_id, @unit_no, @asset_id, @cost, sqlc.narg(bill_id), sqlc.narg(bill_line_no), sqlc.narg(idempotency_key), sqlc.narg(actor_id));

-- name: ReleaseLinksOfAsset :exec
UPDATE fa_source_links SET released_at = now(), released_by = sqlc.narg(actor_id) WHERE property_id = @property_id AND asset_id = @asset_id AND released_at IS NULL;

-- name: GetLiveLinkOfAsset :one
SELECT k.journal_line_id, k.unit_no, k.cost, k.bill_id, k.bill_line_no, j.id AS journal_id, j.journal_number, j.journal_date, j.journal_type, l.source_type, b.bill_number,
       (SELECT count(*) FROM fa_source_links o WHERE o.property_id = k.property_id AND o.journal_line_id = k.journal_line_id AND o.released_at IS NULL)::int AS units
  FROM fa_source_links k
  JOIN gl_journal_lines l ON l.property_id = k.property_id AND l.id = k.journal_line_id
  JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
  LEFT JOIN supplier_bills b ON b.id = k.bill_id
 WHERE k.tenant_id = @tenant_id AND k.property_id = @property_id AND k.asset_id = @asset_id AND k.released_at IS NULL;

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
