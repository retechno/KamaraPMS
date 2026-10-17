-- Inventory, PR 4 (docs/architecture/35-inventory.md 3.5, 5.6, 5.7): the recipes and the deduction of a POS sale.
-- Every query is scoped by tenant_id and property_id. The ledger tables are still written only by ledger.go.

-- ---------------------------------------------------------------------------------------------------------------------
-- The recipe of a product

-- name: ListRecipeLines :many
SELECT r.id, r.option_id, COALESCE(o.name, '')::text AS option_name, COALESCE(g.id, 0)::bigint AS group_id, COALESCE(g.name, '')::text AS group_name, COALESCE(g.kind, '')::text AS group_kind,
       r.inventory_item_id AS item_id, i.code AS item_code, i.name AS item_name, r.unit_id, u.code AS unit_code, r.quantity, r.note,
       iu.stock_num, iu.stock_den, i.stock_unit_id, su.code AS stock_unit_code, i.reference_cost, c.kind AS category_kind
  FROM pos_recipe_lines r
  JOIN inv_items i ON i.property_id = r.property_id AND i.id = r.inventory_item_id
  JOIN inv_units u ON u.property_id = r.property_id AND u.id = r.unit_id
  JOIN inv_item_units iu ON iu.property_id = r.property_id AND iu.item_id = r.inventory_item_id AND iu.unit_id = r.unit_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  LEFT JOIN pos_options o ON o.property_id = r.property_id AND o.id = r.option_id
  LEFT JOIN pos_option_groups g ON g.property_id = o.property_id AND g.id = o.group_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.product_id = @product_id
 ORDER BY (r.option_id IS NOT NULL), r.option_id, r.id;

-- name: DeleteRecipeLines :exec
DELETE FROM pos_recipe_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND product_id = @product_id;

-- name: InsertRecipeLine :exec
INSERT INTO pos_recipe_lines (tenant_id, property_id, product_id, option_id, inventory_item_id, unit_id, quantity, note)
VALUES (@tenant_id, @property_id, @product_id, sqlc.narg(option_id), @item_id, @unit_id, @quantity, sqlc.narg(note));

-- The options of a product, with their group: the editor lists them even when they have no recipe line.
-- name: ListProductOptionsForRecipe :many
SELECT o.id, o.name, o.is_active, o.price_delta, g.id AS group_id, g.name AS group_name, g.kind AS group_kind
  FROM pos_options o JOIN pos_option_groups g ON g.property_id = o.property_id AND g.id = o.group_id
 WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.product_id = @product_id
 ORDER BY g.sort_order, g.id, o.sort_order, o.id;

-- name: GetProductForRecipe :one
SELECT p.id, p.code, p.name, p.is_active, p.category_id, p.version
  FROM pos_products p WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.id = @id;

-- The options of a product that the recipe lines may name (a line names an option of its own product).
-- name: ListOptionIdsOfProduct :many
SELECT o.id FROM pos_options o JOIN pos_option_groups g ON g.property_id = o.property_id AND g.id = o.group_id
 WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.product_id = @product_id;

-- The sale class a POS category gives its products (4.9): its own fb_class, else the nearest ancestor's. NULL when none says anything.
-- name: CategoryFbClass :one
WITH RECURSIVE up AS (
    SELECT c.id, c.parent_id, c.fb_class, 0 AS depth FROM pos_categories c WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.id = @category_id
    UNION ALL
    SELECT c.id, c.parent_id, c.fb_class, up.depth + 1 FROM pos_categories c JOIN up ON c.property_id = @property_id AND c.id = up.parent_id
)
SELECT COALESCE((SELECT fb_class FROM up WHERE fb_class IS NOT NULL ORDER BY depth LIMIT 1), '')::text AS fb_class;

-- name: ListBalancesOfItems :many
SELECT item_id, qty, value, last_unit_cost FROM inv_balances
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id AND item_id = ANY(@item_ids::bigint[]);

-- ---------------------------------------------------------------------------------------------------------------------
-- The outlet as the stock sees it

-- name: GetOutletStock :one
SELECT o.id, o.code, o.name, o.department_id, o.order_mode, o.void_waste_after_minutes, o.warehouse_id, o.is_active,
       COALESCE(w.code, '')::text AS warehouse_code, COALESCE(w.name, '')::text AS warehouse_name, COALESCE(w.cost_class, 'OTHER')::text AS warehouse_class, COALESCE(w.is_active, false)::boolean AS warehouse_active
  FROM pos_outlets o LEFT JOIN inv_warehouses w ON w.property_id = o.property_id AND w.id = o.warehouse_id
 WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.id = @id;

-- name: ListOutletsStock :many
SELECT o.id, o.code, o.name, o.warehouse_id, COALESCE(w.name, '')::text AS warehouse_name
  FROM pos_outlets o LEFT JOIN inv_warehouses w ON w.property_id = o.property_id AND w.id = o.warehouse_id
 WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.is_active ORDER BY o.sort_order, o.id;

-- The cost account of the category of an item for the class OTHER (4.9).
-- name: ItemCogsAccounts :many
SELECT i.id AS item_id, c.cogs_account_id FROM inv_items i JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = ANY(@item_ids::bigint[]);

-- ---------------------------------------------------------------------------------------------------------------------
-- The queue

-- name: InsertConsumptionRow :one
INSERT INTO inv_consumption_queue (tenant_id, property_id, outlet_id, warehouse_id, pos_bill_line_id, kind, sale_queue_id, product_id, quantity, sale_class, sale_date, components, status, created_at)
VALUES (@tenant_id, @property_id, @outlet_id, @warehouse_id, @pos_bill_line_id, @kind, sqlc.narg(sale_queue_id), @product_id, @quantity, @sale_class, @sale_date, @components, 'PENDING', @created_at)
ON CONFLICT (pos_bill_line_id, kind) DO NOTHING
RETURNING id;

-- name: GetConsumptionByLine :one
SELECT * FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND pos_bill_line_id = @pos_bill_line_id AND kind = @kind;

-- name: GetConsumptionRow :one
SELECT * FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- A row locked for the decision of a void: it waits for the worker that holds it.
-- name: LockConsumptionRow :one
SELECT * FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- The warehouses that have rows to apply, and the properties (for the worker, which has no signed-in user).
-- name: ListWarehousesWithPending :many
SELECT DISTINCT warehouse_id FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'PENDING' ORDER BY warehouse_id;

-- name: ListPropertiesWithPending :many
SELECT DISTINCT tenant_id, property_id FROM inv_consumption_queue WHERE status = 'PENDING' ORDER BY tenant_id, property_id;

-- The rows the worker takes: up to a limit, in order, the ones another worker holds skipped (5.6).
-- name: ClaimConsumptionRows :many
SELECT * FROM inv_consumption_queue
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id AND status = 'PENDING'
 ORDER BY id LIMIT @row_limit FOR UPDATE SKIP LOCKED;

-- A row by id for the worker of one row, skipping a row that is held by another transaction.
-- name: ClaimConsumptionRowByID :one
SELECT * FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status IN ('PENDING', 'FAILED') FOR UPDATE SKIP LOCKED;

-- name: SetConsumptionHeld :exec
UPDATE inv_consumption_queue SET status = 'HELD' WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = ANY(@ids::bigint[]) AND status = 'PENDING';

-- name: SetConsumptionApplied :exec
UPDATE inv_consumption_queue SET status = 'APPLIED', movement_ids = @movement_ids::bigint[], applied_on = @applied_on, applied_at = @applied_at, error_code = NULL, error_message = NULL
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- One component of a held row applied: its movement joins the list; the row is applied when every component has its movement.
-- name: AppendConsumptionMovement :one
UPDATE inv_consumption_queue SET movement_ids = array_append(movement_ids, @movement_id::bigint),
       status = CASE WHEN cardinality(movement_ids) + 1 >= jsonb_array_length(components) THEN 'APPLIED' ELSE status END,
       applied_on = CASE WHEN cardinality(movement_ids) + 1 >= jsonb_array_length(components) THEN @applied_on::date ELSE applied_on END,
       applied_at = CASE WHEN cardinality(movement_ids) + 1 >= jsonb_array_length(components) THEN @applied_at::timestamptz ELSE applied_at END
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING status;

-- name: SetConsumptionFailed :exec
UPDATE inv_consumption_queue SET status = 'FAILED', attempts = attempts + 1, error_code = @error_code, error_message = @error_message
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status IN ('PENDING', 'FAILED');

-- name: SetConsumptionCancelled :exec
UPDATE inv_consumption_queue SET status = 'CANCELLED', error_code = NULL, error_message = NULL
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status IN ('PENDING', 'HELD', 'FAILED');

-- name: SetConsumptionRetry :exec
UPDATE inv_consumption_queue SET status = 'PENDING', error_code = NULL, error_message = NULL
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'FAILED';

-- name: CountConsumptionByStatus :many
SELECT status, count(*)::int AS n FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND status IN ('PENDING', 'HELD', 'FAILED') GROUP BY status;

-- name: CountPendingConsumption :one
SELECT count(*)::int FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'PENDING';

-- The rows the screens list: what waits and what failed, with the product and the outlet.
-- name: ListConsumptionQueue :many
SELECT q.id, q.outlet_id, o.code AS outlet_code, o.name AS outlet_name, q.warehouse_id, w.name AS warehouse_name, q.pos_bill_line_id, q.kind, q.sale_queue_id,
       q.product_id, p.name AS product_name, q.quantity, q.sale_class, q.sale_date, q.status, q.attempts, q.error_code, q.error_message, q.created_at, q.applied_on,
       COALESCE(b.bill_number, '')::text AS bill_number
  FROM inv_consumption_queue q
  JOIN pos_outlets o ON o.property_id = q.property_id AND o.id = q.outlet_id
  JOIN inv_warehouses w ON w.property_id = q.property_id AND w.id = q.warehouse_id
  JOIN pos_products p ON p.property_id = q.property_id AND p.id = q.product_id
  JOIN pos_bill_lines l ON l.property_id = q.property_id AND l.id = q.pos_bill_line_id
  JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id
 WHERE q.tenant_id = @tenant_id AND q.property_id = @property_id AND q.status = ANY(@statuses::text[]) AND (sqlc.narg(id)::bigint IS NULL OR q.id = sqlc.narg(id)::bigint)
 ORDER BY q.id DESC LIMIT @row_limit;

-- What a count holds: the rows of a frozen warehouse, SALE and VOID_RETURN and VOID_WASTE (4.7).
-- name: ListHeldConsumption :many
SELECT * FROM inv_consumption_queue
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id AND status = 'HELD' ORDER BY sale_date, id;

-- name: ListConsumptionMovementIDs :many
SELECT movement_ids FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The report of 5.6: lines of POS with a recipe that have no row in the queue (the module was off when they were sold), by the business date of the posting of the line.
-- name: ListSalesWithoutDeduction :many
SELECT l.id AS bill_line_id, b.bill_number, o.id AS outlet_id, o.code AS outlet_code, o.name AS outlet_name, l.product_id, p.name AS product_name, l.quantity,
       fi.business_date AS sent_on, l.status
  FROM pos_bill_lines l
  JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id
  JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
  JOIN pos_products p ON p.property_id = l.property_id AND p.id = l.product_id
  JOIN folio_items fi ON fi.property_id = l.property_id AND fi.id = l.folio_item_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.folio_item_id IS NOT NULL
   AND fi.business_date BETWEEN @from_date AND @to_date
   AND (sqlc.narg(outlet_id)::bigint IS NULL OR o.id = sqlc.narg(outlet_id)::bigint)
   AND EXISTS (SELECT 1 FROM pos_recipe_lines r WHERE r.property_id = l.property_id AND r.product_id = l.product_id)
   AND NOT EXISTS (SELECT 1 FROM inv_consumption_queue q WHERE q.property_id = l.property_id AND q.pos_bill_line_id = l.id AND q.kind = 'SALE')
 ORDER BY fi.business_date DESC, l.id DESC LIMIT @row_limit;

-- name: CountHeldConsumption :one
SELECT count(*)::int FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id AND status = 'HELD';

-- name: CountFailedConsumption :one
SELECT count(*)::int FROM inv_consumption_queue WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'FAILED';
