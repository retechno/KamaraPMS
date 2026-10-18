-- Inventory, PR 1 (docs/architecture/35-inventory.md): masters, ledger, balances, layers, shortfalls, opening stock.
-- Every query is scoped by tenant_id and property_id. The tables of the ledger (inv_movements, inv_balances, inv_layers, inv_layer_uses, inv_shortfalls) are written only by ledger.go
-- (stockpath_guard_test.go holds it): the queries that write them are named Ledger* so the guard can find their callers.

-- ---------------------------------------------------------------------------------------------------------------------
-- Settings

-- name: GetSettings :one
SELECT * FROM inventory_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- name: EnsureSettings :one
INSERT INTO inventory_settings (tenant_id, property_id, created_by, updated_by) VALUES (@tenant_id, @property_id, sqlc.narg(actor_id), sqlc.narg(actor_id))
ON CONFLICT (property_id) DO UPDATE SET property_id = EXCLUDED.property_id
RETURNING *;

-- name: UpdateSettings :one
UPDATE inventory_settings
   SET costing_method = @costing_method, price_tolerance_pct = @price_tolerance_pct, qty_tolerance_pct = @qty_tolerance_pct, over_receipt_pct = @over_receipt_pct,
       po_requires_pr = @po_requires_pr, direct_receipt_limit = sqlc.narg(direct_receipt_limit), count_blocks_night_audit = @count_blocks_night_audit, count_blind = @count_blind, engineering_department_id = sqlc.narg(engineering_department_id),
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND version = @version
RETURNING *;

-- ---------------------------------------------------------------------------------------------------------------------
-- Units

-- name: ListUnits :many
SELECT u.*, (EXISTS (SELECT 1 FROM inv_items i WHERE i.property_id = u.property_id AND i.stock_unit_id = u.id)
             OR EXISTS (SELECT 1 FROM inv_item_units x WHERE x.property_id = u.property_id AND x.unit_id = u.id))::boolean AS in_use
  FROM inv_units u
 WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id
 ORDER BY u.code;

-- name: GetUnit :one
SELECT * FROM inv_units WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertUnit :one
INSERT INTO inv_units (tenant_id, property_id, code, name, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateUnit :one
UPDATE inv_units SET name = @name, is_active = @is_active, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING *;

-- ---------------------------------------------------------------------------------------------------------------------
-- Categories

-- name: ListCategories :many
SELECT c.*, ia.code AS inventory_account_code, ia.name AS inventory_account_name,
       ca.code AS cogs_account_code, xa.code AS issue_account_code, wa.code AS waste_account_code, va.code AS variance_account_code,
       (SELECT count(*) FROM inv_items i WHERE i.property_id = c.property_id AND i.category_id = c.id)::int AS item_count
  FROM inv_categories c
  JOIN gl_accounts ia ON ia.property_id = c.property_id AND ia.id = c.inventory_account_id
  LEFT JOIN gl_accounts ca ON ca.property_id = c.property_id AND ca.id = c.cogs_account_id
  LEFT JOIN gl_accounts xa ON xa.property_id = c.property_id AND xa.id = c.issue_account_id
  LEFT JOIN gl_accounts wa ON wa.property_id = c.property_id AND wa.id = c.waste_account_id
  LEFT JOIN gl_accounts va ON va.property_id = c.property_id AND va.id = c.variance_account_id
 WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
 ORDER BY c.sort_order, c.code;

-- name: GetCategory :one
SELECT * FROM inv_categories WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertCategory :one
INSERT INTO inv_categories (tenant_id, property_id, code, name, kind, inventory_account_id, cogs_account_id, issue_account_id, waste_account_id, variance_account_id, is_active, sort_order, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @kind, @inventory_account_id, sqlc.narg(cogs_account_id), sqlc.narg(issue_account_id), sqlc.narg(waste_account_id), sqlc.narg(variance_account_id),
        @is_active, @sort_order, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateCategory :one
UPDATE inv_categories
   SET name = @name, kind = @kind, inventory_account_id = @inventory_account_id, cogs_account_id = sqlc.narg(cogs_account_id), issue_account_id = sqlc.narg(issue_account_id),
       waste_account_id = sqlc.narg(waste_account_id), variance_account_id = sqlc.narg(variance_account_id), is_active = @is_active, sort_order = @sort_order,
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING *;

-- The accounts a category names, with what the service checks about each (the database checks the type and the group: inv_categories_accounts_check).
-- name: ListAccountsByID :many
SELECT a.id, a.code, a.name, a.account_type, a.is_postable, a.is_active, COALESCE(a.statement_group, '')::text AS statement_group
  FROM gl_accounts a
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = ANY(@ids::bigint[]);

-- name: GetAccountByCode :one
SELECT a.id, a.code, a.name, a.account_type, a.is_postable, a.is_active
  FROM gl_accounts a WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.code = @code;

-- name: ListInventoryAccounts :many
SELECT a.id, a.code, a.name FROM gl_accounts a
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.account_type = 'ASSET' AND a.statement_group = 'INVENTORIES' AND a.is_postable AND a.is_active
 ORDER BY a.code;

-- ---------------------------------------------------------------------------------------------------------------------
-- Items

-- name: ListItems :many
SELECT i.*, c.code AS category_code, c.name AS category_name, c.kind AS category_kind, u.code AS stock_unit_code,
       COALESCE((SELECT sum(b.qty) FROM inv_balances b WHERE b.property_id = i.property_id AND b.item_id = i.id AND (sqlc.narg(warehouse_id)::bigint IS NULL OR b.warehouse_id = sqlc.narg(warehouse_id))), 0)::numeric AS qty_on_hand,
       COALESCE((SELECT sum(b.value) FROM inv_balances b WHERE b.property_id = i.property_id AND b.item_id = i.id AND (sqlc.narg(warehouse_id)::bigint IS NULL OR b.warehouse_id = sqlc.narg(warehouse_id))), 0)::numeric AS value_on_hand,
       EXISTS (SELECT 1 FROM inv_levels l LEFT JOIN inv_balances b ON b.warehouse_id = l.warehouse_id AND b.item_id = l.item_id
                WHERE l.property_id = i.property_id AND l.item_id = i.id AND l.min_qty > 0 AND COALESCE(b.qty, 0) < l.min_qty)::boolean AS below_min,
       EXISTS (SELECT 1 FROM inv_balances b WHERE b.property_id = i.property_id AND b.item_id = i.id AND b.qty < 0)::boolean AS below_zero
  FROM inv_items i
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(active)::boolean IS NULL OR i.is_active = sqlc.narg(active))
   AND (sqlc.narg(q)::text IS NULL OR i.code ILIKE '%' || sqlc.narg(q) || '%' OR i.name ILIKE '%' || sqlc.narg(q) || '%' OR i.barcode = sqlc.narg(q))
   AND (NOT @only_below_min::boolean OR EXISTS (SELECT 1 FROM inv_levels l LEFT JOIN inv_balances b ON b.warehouse_id = l.warehouse_id AND b.item_id = l.item_id
                WHERE l.property_id = i.property_id AND l.item_id = i.id AND l.min_qty > 0 AND COALESCE(b.qty, 0) < l.min_qty))
 ORDER BY i.name, i.id
 LIMIT @row_limit OFFSET @row_offset;

-- name: CountItems :one
SELECT count(*)::int FROM inv_items i
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(active)::boolean IS NULL OR i.is_active = sqlc.narg(active))
   AND (sqlc.narg(q)::text IS NULL OR i.code ILIKE '%' || sqlc.narg(q) || '%' OR i.name ILIKE '%' || sqlc.narg(q) || '%' OR i.barcode = sqlc.narg(q))
   AND (NOT @only_below_min::boolean OR EXISTS (SELECT 1 FROM inv_levels l LEFT JOIN inv_balances b ON b.warehouse_id = l.warehouse_id AND b.item_id = l.item_id
                WHERE l.property_id = i.property_id AND l.item_id = i.id AND l.min_qty > 0 AND COALESCE(b.qty, 0) < l.min_qty));

-- name: CountItemsByCategory :many
SELECT i.category_id, count(*)::int AS n FROM inv_items i WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id GROUP BY i.category_id;

-- name: TotalStockValue :one
SELECT COALESCE(sum(b.value), 0)::numeric FROM inv_balances b WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id;

-- name: GetItem :one
SELECT i.*, c.code AS category_code, c.name AS category_name, c.kind AS category_kind, u.code AS stock_unit_code
  FROM inv_items i
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = @id;

-- name: GetItemByCode :one
SELECT i.id FROM inv_items i WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.code = @code;

-- name: InsertItem :one
INSERT INTO inv_items (tenant_id, property_id, code, name, category_id, stock_unit_id, reference_cost, default_supplier_id, barcode, note, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @category_id, @stock_unit_id, sqlc.narg(reference_cost), sqlc.narg(default_supplier_id), sqlc.narg(barcode), sqlc.narg(note), @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateItem :one
UPDATE inv_items
   SET name = @name, category_id = @category_id, stock_unit_id = @stock_unit_id, reference_cost = sqlc.narg(reference_cost), default_supplier_id = sqlc.narg(default_supplier_id),
       barcode = sqlc.narg(barcode), note = sqlc.narg(note), is_active = @is_active, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING id;

-- An item that has a movement or a balance cannot change its stock unit or its category's class (the quantities are in that unit).
-- name: ItemHasStock :one
SELECT (EXISTS (SELECT 1 FROM inv_movements m WHERE m.property_id = @property_id AND m.item_id = @item_id)
        OR EXISTS (SELECT 1 FROM inv_balances b WHERE b.property_id = @property_id AND b.item_id = @item_id AND (b.qty <> 0 OR b.value <> 0)))::boolean;

-- name: ListItemUnits :many
SELECT x.*, u.code AS unit_code, u.name AS unit_name
  FROM inv_item_units x JOIN inv_units u ON u.property_id = x.property_id AND u.id = x.unit_id
 WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id AND x.item_id = @item_id
 ORDER BY (x.unit_id = @stock_unit_id) DESC, u.code;

-- name: ListItemUnitsOf :many
SELECT x.item_id, x.unit_id, x.stock_num, x.stock_den, x.is_purchase, x.is_recipe, x.is_default_purchase, x.is_default_recipe, u.code AS unit_code
  FROM inv_item_units x JOIN inv_units u ON u.property_id = x.property_id AND u.id = x.unit_id
 WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id AND x.item_id = ANY(@item_ids::bigint[])
 ORDER BY x.item_id, u.code;

-- name: DeleteItemUnits :exec
DELETE FROM inv_item_units WHERE tenant_id = @tenant_id AND property_id = @property_id AND item_id = @item_id;

-- name: InsertItemUnit :exec
INSERT INTO inv_item_units (tenant_id, property_id, item_id, unit_id, stock_num, stock_den, is_purchase, is_recipe, is_default_purchase, is_default_recipe)
VALUES (@tenant_id, @property_id, @item_id, @unit_id, @stock_num, @stock_den, @is_purchase, @is_recipe, @is_default_purchase, @is_default_recipe);

-- ---------------------------------------------------------------------------------------------------------------------
-- Warehouses and levels

-- name: ListWarehouses :many
SELECT w.*, o.code AS outlet_code, d.code AS department_code,
       COALESCE((SELECT sum(b.value) FROM inv_balances b WHERE b.property_id = w.property_id AND b.warehouse_id = w.id), 0)::numeric AS stock_value,
       (SELECT count(*) FROM inv_balances b WHERE b.property_id = w.property_id AND b.warehouse_id = w.id AND b.qty <> 0)::int AS item_count
  FROM inv_warehouses w
  LEFT JOIN pos_outlets o ON o.property_id = w.property_id AND o.id = w.outlet_id
  LEFT JOIN departments d ON d.property_id = w.property_id AND d.id = w.department_id
 WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id
 ORDER BY w.code;

-- name: GetWarehouse :one
SELECT * FROM inv_warehouses WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertWarehouse :one
INSERT INTO inv_warehouses (tenant_id, property_id, code, name, kind, outlet_id, department_id, cost_class, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @kind, sqlc.narg(outlet_id), sqlc.narg(department_id), @cost_class, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateWarehouse :one
UPDATE inv_warehouses
   SET name = @name, kind = @kind, outlet_id = sqlc.narg(outlet_id), department_id = sqlc.narg(department_id), cost_class = @cost_class, is_active = @is_active,
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING *;

-- A warehouse that holds stock (a balance with a quantity or a value) cannot be switched off.
-- name: WarehouseHasStock :one
SELECT EXISTS (SELECT 1 FROM inv_balances b WHERE b.property_id = @property_id AND b.warehouse_id = @warehouse_id AND (b.qty <> 0 OR b.value <> 0))::boolean;

-- name: WarehouseHasMovements :one
SELECT EXISTS (SELECT 1 FROM inv_movements m WHERE m.property_id = @property_id AND m.warehouse_id = @warehouse_id)::boolean;

-- name: ListLevels :many
SELECT l.*, i.code AS item_code, i.name AS item_name FROM inv_levels l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.warehouse_id = @warehouse_id
 ORDER BY i.name, i.id;

-- name: DeleteLevels :exec
DELETE FROM inv_levels WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id;

-- name: InsertLevel :exec
INSERT INTO inv_levels (tenant_id, property_id, warehouse_id, item_id, min_qty, max_qty, reorder_qty)
VALUES (@tenant_id, @property_id, @warehouse_id, @item_id, @min_qty, sqlc.narg(max_qty), sqlc.narg(reorder_qty));

-- ---------------------------------------------------------------------------------------------------------------------
-- Stock and the stock card (reads)

-- name: ListStock :many
SELECT b.warehouse_id, b.item_id, b.qty, b.value, b.last_unit_cost, w.code AS warehouse_code, w.name AS warehouse_name,
       COALESCE(l.min_qty, 0)::numeric AS min_qty
  FROM inv_balances b
  JOIN inv_warehouses w ON w.property_id = b.property_id AND w.id = b.warehouse_id
  LEFT JOIN inv_levels l ON l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR b.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(item_id)::bigint IS NULL OR b.item_id = sqlc.narg(item_id))
   AND b.item_id = ANY(@item_ids::bigint[])
 ORDER BY b.item_id, w.code;

-- name: ListItemStock :many
SELECT w.id AS warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, COALESCE(b.qty, 0)::numeric AS qty, COALESCE(b.value, 0)::numeric AS value, b.last_unit_cost,
       COALESCE(l.min_qty, 0)::numeric AS min_qty
  FROM inv_warehouses w
  LEFT JOIN inv_balances b ON b.warehouse_id = w.id AND b.item_id = @item_id
  LEFT JOIN inv_levels l ON l.warehouse_id = w.id AND l.item_id = @item_id
 WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.is_active
 ORDER BY w.code;

-- name: StockCard :many
SELECT m.id, m.business_date, m.kind, m.qty_delta, m.value_delta, m.unit_cost, m.cost_estimated, m.source_type, m.source_id, m.source_date,
       COALESCE(o.opening_number, '')::text AS document_number, m.reverses_id, m.created_at
  FROM inv_movements m
  LEFT JOIN inv_openings o ON m.source_type = 'INV_OPENING' AND o.property_id = m.property_id AND o.id = m.source_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.warehouse_id = @warehouse_id AND m.item_id = @item_id
   AND (sqlc.narg(from_date)::date IS NULL OR m.business_date >= sqlc.narg(from_date))
   AND (sqlc.narg(to_date)::date IS NULL OR m.business_date <= sqlc.narg(to_date))
 ORDER BY m.business_date, m.id
 LIMIT @row_limit;

-- What the card stood at the start of the period: the sum of everything before it.
-- name: StockCardOpening :one
SELECT COALESCE(sum(m.qty_delta), 0)::numeric AS qty, COALESCE(sum(m.value_delta), 0)::numeric AS value
  FROM inv_movements m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.warehouse_id = @warehouse_id AND m.item_id = @item_id
   AND m.business_date < @from_date::date;

-- name: ListItemLayers :many
SELECT l.id, l.received_on, l.qty_in, l.qty_remaining, l.value_remaining, l.movement_id
  FROM inv_layers l
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.warehouse_id = @warehouse_id AND l.item_id = @item_id AND l.qty_remaining > 0
 ORDER BY l.received_on, l.id;

-- name: ListOpenShortfallsOf :many
SELECT s.id, s.qty_open, s.est_unit_cost, s.est_value_open, s.business_date, s.source_type
  FROM inv_shortfalls s
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.warehouse_id = @warehouse_id AND s.item_id = @item_id AND s.status = 'OPEN'
 ORDER BY s.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The ledger writer (ledger.go only)

-- name: LedgerEnsureBalance :exec
INSERT INTO inv_balances (tenant_id, property_id, warehouse_id, item_id) VALUES (@tenant_id, @property_id, @warehouse_id, @item_id)
ON CONFLICT (warehouse_id, item_id) DO NOTHING;

-- The id of the balance row of a pair: the caller collects them, sorts them and locks them in one call (db.LockRows).
-- name: LedgerBalanceID :one
SELECT b.id FROM inv_balances b WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.warehouse_id = @warehouse_id AND b.item_id = @item_id;

-- name: LedgerGetBalances :many
SELECT b.id, b.warehouse_id, b.item_id, b.qty, b.value, b.last_unit_cost, b.version
  FROM inv_balances b WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.id = ANY(@ids::bigint[]) ORDER BY b.id;

-- name: LedgerUpdateBalance :exec
UPDATE inv_balances SET qty = @qty, value = @value, last_unit_cost = sqlc.narg(last_unit_cost), version = version + 1, updated_at = now()
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: LedgerItemInfo :many
SELECT i.id, i.reference_cost, c.kind AS category_kind, c.inventory_account_id
  FROM inv_items i JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = ANY(@ids::bigint[]);

-- name: LedgerWarehouseInfo :many
SELECT w.id, w.outlet_id, w.is_active, w.count_id FROM inv_warehouses w WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.id = ANY(@ids::bigint[]);

-- The newest receipt of the item in any warehouse of the property: the third source of the estimate of 4.4.
-- name: LedgerNewestReceiptCost :one
SELECT m.unit_cost FROM inv_movements m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.item_id = @item_id AND m.kind = 'RECEIPT' AND m.cost_estimated = false
 ORDER BY m.id DESC LIMIT 1;

-- name: LedgerInsertMovement :one
INSERT INTO inv_movements (tenant_id, property_id, warehouse_id, item_id, kind, qty_delta, value_delta, unit_cost, cost_estimated, business_date, source_date, source_type, source_id, source_line,
                           reverses_id, gl_debit_account_id, gl_credit_account_id, gl_amount, gl_department_id, item_class, cost_class, outlet_id, created_by)
VALUES (@tenant_id, @property_id, @warehouse_id, @item_id, @kind, @qty_delta, @value_delta, @unit_cost, @cost_estimated, @business_date, sqlc.narg(source_date), @source_type, @source_id, @source_line,
        sqlc.narg(reverses_id), sqlc.narg(gl_debit_account_id), sqlc.narg(gl_credit_account_id), @gl_amount, sqlc.narg(gl_department_id), @item_class, @cost_class, sqlc.narg(outlet_id), sqlc.narg(actor_id))
RETURNING id;

-- name: LedgerGetMovement :one
SELECT * FROM inv_movements WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: LedgerMovementReversed :one
SELECT EXISTS (SELECT 1 FROM inv_movements m WHERE m.property_id = @property_id AND m.reverses_id = @id)::boolean;

-- How many settlement rows an IN movement wrote (a movement that settled shortfalls cannot be reversed by the simple rules).
-- name: LedgerCountSettlementsOf :one
SELECT count(*)::int FROM inv_movements m WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.source_type = 'INV_SETTLE' AND m.source_id = @movement_id;

-- name: LedgerInsertLayer :one
INSERT INTO inv_layers (tenant_id, property_id, warehouse_id, item_id, movement_id, received_on, qty_in, qty_remaining, value_remaining)
VALUES (@tenant_id, @property_id, @warehouse_id, @item_id, @movement_id, @received_on, @qty_in, @qty_remaining, @value_remaining)
RETURNING id;

-- name: LedgerOpenLayers :many
SELECT l.id, l.received_on, l.qty_in, l.qty_remaining, l.value_remaining, l.movement_id
  FROM inv_layers l
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.warehouse_id = @warehouse_id AND l.item_id = @item_id AND l.qty_remaining > 0
 ORDER BY l.received_on, l.id;

-- name: LedgerGetLayersByID :many
SELECT l.id, l.received_on, l.qty_in, l.qty_remaining, l.value_remaining, l.movement_id
  FROM inv_layers l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.id = ANY(@ids::bigint[]) ORDER BY l.id;

-- name: LedgerGetLayerOfMovement :one
SELECT l.id, l.received_on, l.qty_in, l.qty_remaining, l.value_remaining, l.movement_id
  FROM inv_layers l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.movement_id = @movement_id;

-- name: LedgerUpdateLayer :exec
UPDATE inv_layers SET qty_remaining = @qty_remaining, value_remaining = @value_remaining
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: LedgerInsertLayerUse :exec
INSERT INTO inv_layer_uses (tenant_id, property_id, movement_id, layer_id, qty, value) VALUES (@tenant_id, @property_id, @movement_id, @layer_id, @qty, @value);

-- name: LedgerListLayerUses :many
SELECT u.layer_id, u.qty, u.value FROM inv_layer_uses u
 WHERE u.tenant_id = @tenant_id AND u.property_id = @property_id AND u.movement_id = @movement_id ORDER BY u.layer_id;

-- name: LedgerInsertShortfall :one
INSERT INTO inv_shortfalls (tenant_id, property_id, warehouse_id, item_id, movement_id, qty_open, qty_original, est_unit_cost, est_value_open, gl_debit_account_id, gl_department_id,
                            source_type, cost_class, outlet_id, business_date)
VALUES (@tenant_id, @property_id, @warehouse_id, @item_id, @movement_id, @qty_open, @qty_open, @est_unit_cost, @est_value_open, sqlc.narg(gl_debit_account_id), sqlc.narg(gl_department_id),
        @source_type, @cost_class, sqlc.narg(outlet_id), @business_date)
RETURNING id;

-- name: LedgerOpenShortfalls :many
SELECT s.id, s.movement_id, s.qty_open, s.est_unit_cost, s.est_value_open, s.gl_debit_account_id, s.gl_department_id, s.source_type, s.cost_class, s.outlet_id
  FROM inv_shortfalls s
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.warehouse_id = @warehouse_id AND s.item_id = @item_id AND s.status = 'OPEN'
 ORDER BY s.id;

-- name: LedgerGetShortfallOfMovement :one
SELECT s.id, s.qty_open, s.qty_original, s.est_unit_cost, s.est_value_open, s.status FROM inv_shortfalls s
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.movement_id = @movement_id;

-- name: LedgerUpdateShortfall :exec
UPDATE inv_shortfalls SET qty_open = @qty_open, est_value_open = @est_value_open, status = @status
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The open shortfalls of an item in a warehouse are closed by a count (4.4 item 4).
-- name: LedgerCloseShortfallsByCount :exec
UPDATE inv_shortfalls SET qty_open = 0, est_value_open = 0, status = 'CLOSED_BY_COUNT'
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id AND item_id = @item_id AND status = 'OPEN';

-- ---------------------------------------------------------------------------------------------------------------------
-- Opening stock

-- name: InsertOpening :one
INSERT INTO inv_openings (tenant_id, property_id, opening_number, client_uuid, warehouse_id, business_date, total_value, note, created_by)
VALUES (@tenant_id, @property_id, @opening_number, @client_uuid, @warehouse_id, @business_date, @total_value, sqlc.narg(note), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertOpeningLine :exec
INSERT INTO inv_opening_lines (tenant_id, property_id, opening_id, line_no, warehouse_id, item_id, qty_stock, unit_cost, value, movement_id)
VALUES (@tenant_id, @property_id, @opening_id, @line_no, @warehouse_id, @item_id, @qty_stock, @unit_cost, @value, @movement_id);

-- name: GetOpeningByClientUUID :one
SELECT * FROM inv_openings WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetOpening :one
SELECT o.*, w.code AS warehouse_code, w.name AS warehouse_name FROM inv_openings o JOIN inv_warehouses w ON w.property_id = o.property_id AND w.id = o.warehouse_id
 WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.id = @id;

-- name: ListOpenings :many
SELECT o.*, w.code AS warehouse_code, w.name AS warehouse_name, (SELECT count(*) FROM inv_opening_lines l WHERE l.opening_id = o.id)::int AS line_count
  FROM inv_openings o JOIN inv_warehouses w ON w.property_id = o.property_id AND w.id = o.warehouse_id
 WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id
 ORDER BY o.id DESC;

-- name: ListOpeningLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, l.qty_stock, l.unit_cost, l.value
  FROM inv_opening_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.opening_id = @opening_id
 ORDER BY l.line_no;

-- The items of the warehouse that already have an opening (one opening per item and warehouse).
-- name: OpenedItems :many
SELECT l.item_id FROM inv_opening_lines l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.warehouse_id = @warehouse_id AND l.item_id = ANY(@item_ids::bigint[]);

-- F9: a property that already has a balance on its inventory accounts (hand-entered bills and journals) cannot open the module until the accountant has cleared them.
-- The balance of an account is read from the journals, as the general ledger does it.
-- name: InventoryAccountBalances :many
SELECT a.id, a.code, a.name, COALESCE(sum(l.debit - l.credit), 0)::numeric AS balance
  FROM gl_accounts a
  LEFT JOIN gl_journal_lines l ON l.property_id = a.property_id AND l.account_id = a.id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.account_type = 'ASSET' AND a.statement_group = 'INVENTORIES'
 GROUP BY a.id, a.code, a.name
 HAVING COALESCE(sum(l.debit - l.credit), 0) <> 0
 ORDER BY a.code;

-- name: HasMovements :one
SELECT EXISTS (SELECT 1 FROM inv_movements m WHERE m.property_id = @property_id)::boolean;

-- ---------------------------------------------------------------------------------------------------------------------
-- The day close, the control accounts and the checks of the night audit

-- The lines the stock adds to the journal of a business date (6.2): one pair of rows per document and kind. Amount is signed (debit +, credit -). The department goes to the counter
-- account (the cost or the equity line), never to the inventory account.
-- name: StockDayLines :many
WITH g AS (
    SELECT m.gl_debit_account_id AS debit_id, m.gl_credit_account_id AS credit_id, m.gl_department_id AS department_id, m.kind, m.source_type, m.source_id,
           sum(m.gl_amount) AS amount
      FROM inv_movements m
     WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date = @business_date::date AND m.gl_amount > 0
     GROUP BY m.gl_debit_account_id, m.gl_credit_account_id, m.gl_department_id, m.kind, m.source_type, m.source_id
), named AS (
    SELECT g.*, COALESCE(o.opening_number, rc.receipt_number, rt.return_number, b.bill_number, cn.credit_number, iss.iss_number, cnt.cnt_number, g.source_type || ' ' || g.source_id)::text AS ref
      FROM g
      LEFT JOIN inv_openings o ON g.source_type = 'INV_OPENING' AND o.property_id = @property_id AND o.id = g.source_id
      LEFT JOIN inv_receipts rc ON g.source_type = 'INV_RECEIPT' AND rc.property_id = @property_id AND rc.id = g.source_id
      LEFT JOIN inv_returns rt ON g.source_type = 'INV_RETURN' AND rt.property_id = @property_id AND rt.id = g.source_id
      LEFT JOIN supplier_bills b ON g.source_type = 'INV_BILL' AND b.property_id = @property_id AND b.id = g.source_id
      LEFT JOIN supplier_credit_notes cn ON g.source_type = 'INV_CREDIT' AND cn.property_id = @property_id AND cn.id = g.source_id
      LEFT JOIN inv_issues iss ON g.source_type = 'INV_ISSUE' AND iss.property_id = @property_id AND iss.id = g.source_id
      LEFT JOIN inv_counts cnt ON g.source_type = 'INV_COUNT' AND cnt.property_id = @property_id AND cnt.id = g.source_id
)
SELECT n.debit_id::bigint AS account_id, n.department_id, n.kind::text AS kind, n.source_type::text AS source_type, n.ref AS source_ref, n.amount::numeric AS amount, true AS is_debit,
       (n.debit_id IN (SELECT c.inventory_account_id FROM inv_categories c WHERE c.property_id = @property_id))::boolean AS is_inventory
  FROM named n
UNION ALL
SELECT n.credit_id::bigint, n.department_id, n.kind::text, n.source_type::text, n.ref, (-n.amount)::numeric, false,
       (n.credit_id IN (SELECT c.inventory_account_id FROM inv_categories c WHERE c.property_id = @property_id))::boolean
  FROM named n
ORDER BY 1, 3, 5, 7;

-- The stock value by inventory account as of a business date: the sum of the ledger (a transfer nets to zero over the property), by the account of the category of the item.
-- name: StockValueByAccount :many
SELECT c.inventory_account_id AS account_id, COALESCE(sum(m.value_delta), 0)::numeric AS value
  FROM inv_categories c
  JOIN inv_items i ON i.property_id = c.property_id AND i.category_id = c.id
  LEFT JOIN inv_movements m ON m.property_id = i.property_id AND m.item_id = i.id AND m.business_date <= @as_of::date
 WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
 GROUP BY c.inventory_account_id;

-- The accounts the control rule protects: the inventory account of every category (and, from PR 2, the account of the key INV_GRNI).
-- name: ControlAccounts :many
SELECT DISTINCT c.inventory_account_id AS account_id FROM inv_categories c WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
UNION
SELECT m.account_id FROM gl_account_map m WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = 'INV_GRNI';

-- The last business day that has a journal: the GL is compared with the stock up to that day.
-- name: LastJournaledDay :one
SELECT (count(*) > 0)::boolean AS found, COALESCE(max(d.business_date), DATE '2000-01-01')::date AS business_date FROM gl_day_posts d WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id;

-- The balance of an account in the general ledger as of a date, from the journals.
-- name: GLBalancesOf :many
SELECT l.account_id, COALESCE(sum(l.debit - l.credit), 0)::numeric AS balance
  FROM gl_journal_lines l JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = ANY(@account_ids::bigint[]) AND j.journal_date <= @as_of::date
 GROUP BY l.account_id;

-- The drift check (7.5): the balance rows against the sum of the ledger, and, under FIFO, the layers against the balance and the open shortfalls.
-- name: DriftBalances :many
SELECT COALESCE(b.warehouse_id, m.warehouse_id)::bigint AS warehouse_id, COALESCE(b.item_id, m.item_id)::bigint AS item_id,
       COALESCE(b.qty, 0)::numeric AS balance_qty, COALESCE(b.value, 0)::numeric AS balance_value, COALESCE(m.qty, 0)::numeric AS ledger_qty, COALESCE(m.value, 0)::numeric AS ledger_value
  FROM (SELECT x.warehouse_id, x.item_id, x.qty, x.value FROM inv_balances x WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id) b
  FULL JOIN (SELECT y.warehouse_id, y.item_id, sum(y.qty_delta) AS qty, sum(y.value_delta) AS value FROM inv_movements y WHERE y.tenant_id = @tenant_id AND y.property_id = @property_id GROUP BY y.warehouse_id, y.item_id) m
         ON m.warehouse_id = b.warehouse_id AND m.item_id = b.item_id
 WHERE COALESCE(b.qty, 0) <> COALESCE(m.qty, 0) OR COALESCE(b.value, 0) <> COALESCE(m.value, 0)
 ORDER BY 1, 2
 LIMIT 50;

-- name: DriftLayers :many
SELECT b.warehouse_id, b.item_id, b.qty AS balance_qty, b.value AS balance_value,
       COALESCE((SELECT sum(l.qty_remaining) FROM inv_layers l WHERE l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id), 0)::numeric AS layer_qty,
       COALESCE((SELECT sum(l.value_remaining) FROM inv_layers l WHERE l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id), 0)::numeric AS layer_value,
       COALESCE((SELECT sum(s.qty_open) FROM inv_shortfalls s WHERE s.warehouse_id = b.warehouse_id AND s.item_id = b.item_id AND s.status = 'OPEN'), 0)::numeric AS short_qty,
       COALESCE((SELECT sum(s.est_value_open) FROM inv_shortfalls s WHERE s.warehouse_id = b.warehouse_id AND s.item_id = b.item_id AND s.status = 'OPEN'), 0)::numeric AS short_value
  FROM inv_balances b
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id
   AND (b.qty <> COALESCE((SELECT sum(l.qty_remaining) FROM inv_layers l WHERE l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id), 0)
                 - COALESCE((SELECT sum(s.qty_open) FROM inv_shortfalls s WHERE s.warehouse_id = b.warehouse_id AND s.item_id = b.item_id AND s.status = 'OPEN'), 0)
     OR b.value <> COALESCE((SELECT sum(l.value_remaining) FROM inv_layers l WHERE l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id), 0)
                 - COALESCE((SELECT sum(s.est_value_open) FROM inv_shortfalls s WHERE s.warehouse_id = b.warehouse_id AND s.item_id = b.item_id AND s.status = 'OPEN'), 0))
 ORDER BY 1, 2
 LIMIT 50;

-- name: CountNegativeBalances :one
SELECT count(*)::int FROM inv_balances b WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.qty < 0;

-- The first close after the lock audits it once (as accounting.deposit_release_locked is audited).
-- name: CostingLockToAudit :one
SELECT s.costing_method, s.costing_locked_on FROM inventory_settings s WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.costing_locked_on IS NOT NULL;

-- name: CostingLockAudited :one
SELECT EXISTS (SELECT 1 FROM audit_logs a WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.action = 'inventory.costing_locked')::boolean;

-- The keys of the stock that the property has mapped (the setup check lists the ones that are missing).
-- name: MappedInventoryKeys :many
SELECT m.map_key FROM gl_account_map m WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = ANY(@keys::text[]) ORDER BY m.map_key;

-- name: CountActive :one
SELECT (SELECT count(*) FROM inv_warehouses w WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.is_active)::int AS warehouses,
       (SELECT count(*) FROM inv_categories c WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.is_active)::int AS categories,
       (SELECT count(*) FROM inv_items i WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.is_active)::int AS items;

-- Goods received not invoiced as the stock and the bills say it (6.3), as a balance in the convention of the ledger (debits less credits): the receipts, returns and clearings of the stock ledger, the bill lines on the
-- account (with their VAT when it is part of the cost), less the credit note lines on it, all dated up to the day; a bill or a credit note voided by then is out.
-- name: GRNIFromSources :one
WITH acc AS (
    SELECT m.account_id FROM gl_account_map m WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = 'INV_GRNI'
)
SELECT (SELECT a.account_id FROM acc a)::bigint AS account_id,
       (COALESCE((SELECT sum(CASE WHEN mv.gl_debit_account_id = a.account_id THEN mv.gl_amount ELSE 0 END) - sum(CASE WHEN mv.gl_credit_account_id = a.account_id THEN mv.gl_amount ELSE 0 END)
                    FROM inv_movements mv, acc a WHERE mv.tenant_id = @tenant_id AND mv.property_id = @property_id AND mv.business_date <= @as_of::date AND mv.gl_amount > 0), 0)
        + COALESCE((SELECT sum(l.amount + CASE WHEN l.vat_treatment = 'EXPENSE' THEN l.vat_amount ELSE 0 END)
                      FROM supplier_bill_lines l JOIN supplier_bills b ON b.property_id = l.property_id AND b.id = l.bill_id
                      LEFT JOIN gl_journals vj ON vj.property_id = b.property_id AND vj.id = b.void_journal_id, acc a
                     WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = a.account_id AND b.bill_date <= @as_of::date
                       AND (vj.id IS NULL OR vj.journal_date > @as_of::date)), 0)
        - COALESCE((SELECT sum(l.amount + CASE WHEN l.vat_treatment = 'EXPENSE' THEN l.vat_amount ELSE 0 END)
                      FROM supplier_credit_note_lines l JOIN supplier_credit_notes c ON c.property_id = l.property_id AND c.id = l.credit_id
                      LEFT JOIN gl_journals vj ON vj.property_id = c.property_id AND vj.id = c.void_journal_id, acc a
                     WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.account_id = a.account_id AND c.credit_date <= @as_of::date
                       AND (vj.id IS NULL OR vj.journal_date > @as_of::date)), 0))::numeric AS balance;
