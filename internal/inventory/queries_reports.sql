-- Inventory, PR 5 (docs/architecture/35-inventory.md 3.6, 6.3, 12): the snapshots of the day close and the reports.
-- Every query is scoped by tenant_id and property_id. Nothing here writes the stock ledger (ledger.go only); the tables written are the snapshot tables of migration 00088.

-- ---------------------------------------------------------------------------------------------------------------------
-- The cost of the stock by movement: the one classification both the snapshot of a closed day and the live figures of the open day read.
-- Cost-of-sales movements (POS_SALE, POS_SALE_RETURN, NEG_SETTLE) are charged to the sale class; the part of a sale that became waste after cooking (COGS_TO_WASTE, no stock change) moves from the
-- sale cost to the waste cost; the issues are told apart by the purpose of the document (waste, staff meal, entertainment) and, for a use, by its origin (the amenities) and its department (engineering).
-- A count variance is a cost on the day the count is approved; the price variance is the part of a cost adjustment that went to the account INV_PPV (a debit is a cost, a credit is a saving).

-- name: CostMovementsOfRange :many
WITH b AS (
    SELECT m.business_date, m.gl_department_id AS department_id, m.outlet_id, m.cost_class, m.kind, m.value_delta, m.gl_amount, m.gl_debit_account_id, m.gl_credit_account_id,
           COALESCE(iss.origin, '') AS origin, (st.engineering_department_id IS NOT NULL AND m.gl_department_id = st.engineering_department_id) AS engineering,
           ppv.account_id AS ppv_account_id, COALESCE(qr.source, 'POS') AS source
      FROM inv_movements m
      LEFT JOIN inv_issues iss ON m.source_type = 'INV_ISSUE' AND iss.property_id = m.property_id AND iss.id = m.source_id
      LEFT JOIN inv_consumption_queue qr ON m.source_type = 'POS_QUEUE' AND qr.property_id = m.property_id AND qr.id = m.source_id
      LEFT JOIN inventory_settings st ON st.property_id = m.property_id
      LEFT JOIN gl_account_map ppv ON ppv.property_id = m.property_id AND ppv.map_key = 'INV_PPV'
     WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date
), r AS (
    SELECT business_date, department_id, outlet_id, cost_class, source,
           CASE cost_class WHEN 'FOOD' THEN 'SALE_COST_FOOD' WHEN 'BEVERAGE' THEN 'SALE_COST_BEVERAGE' ELSE 'OTHER_USE' END AS cost_kind, -value_delta AS amount
      FROM b WHERE kind IN ('POS_SALE', 'POS_SALE_RETURN', 'NEG_SETTLE')
    UNION ALL
    SELECT business_date, department_id, outlet_id, cost_class, source,
           CASE cost_class WHEN 'FOOD' THEN 'SALE_COST_FOOD' WHEN 'BEVERAGE' THEN 'SALE_COST_BEVERAGE' ELSE 'OTHER_USE' END, -gl_amount
      FROM b WHERE kind = 'COGS_TO_WASTE'
    UNION ALL
    SELECT business_date, department_id, outlet_id, cost_class, source, 'WASTE', gl_amount FROM b WHERE kind = 'COGS_TO_WASTE'
    UNION ALL
    SELECT business_date, department_id, outlet_id, cost_class, source, kind, -value_delta FROM b WHERE kind IN ('WASTE', 'STAFF_MEAL', 'ENTERTAINMENT')
    UNION ALL
    SELECT business_date, department_id, outlet_id, cost_class, source,
           CASE WHEN origin = 'AMENITY' THEN 'AMENITY' WHEN engineering THEN 'ENGINEERING' ELSE 'OTHER_USE' END, -value_delta
      FROM b WHERE kind = 'ISSUE'
    UNION ALL
    SELECT business_date, department_id, outlet_id, cost_class, source, 'COUNT_VARIANCE', -value_delta FROM b WHERE kind IN ('COUNT_LOSS', 'COUNT_GAIN')
    UNION ALL
    SELECT business_date, department_id, outlet_id, cost_class, source, 'PRICE_VARIANCE',
           CASE WHEN gl_debit_account_id = ppv_account_id THEN gl_amount ELSE -gl_amount END
      FROM b WHERE kind = 'COST_ADJUST' AND ppv_account_id IS NOT NULL AND (gl_debit_account_id = ppv_account_id OR gl_credit_account_id = ppv_account_id)
)
SELECT business_date, department_id, outlet_id, cost_class::text AS cost_class, source::text AS source, cost_kind::text AS cost_kind, sum(amount)::numeric AS amount
  FROM r GROUP BY business_date, department_id, outlet_id, cost_class, source, cost_kind
HAVING sum(amount) <> 0
 ORDER BY business_date, cost_kind;

-- The net sales of the outlets by business day and class (12.3): the net amount of the lines after discount, before service charge and tax, from the folios of the outlets; a comp has no revenue;
-- the class is the one of the category of the product (its own, else the nearest ancestor's), else the class of the warehouse of the outlet. A line whose product has no recipe is also counted apart.
-- name: FbRevenueOfRange :many
WITH RECURSIVE up AS (
    SELECT c.id AS cat_id, c.parent_id, c.fb_class, 0 AS depth FROM pos_categories c WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
    UNION ALL
    SELECT up.cat_id, c.parent_id, c.fb_class, up.depth + 1 FROM up JOIN pos_categories c ON c.property_id = @property_id AND c.id = up.parent_id
), cat_class AS (
    SELECT DISTINCT ON (cat_id) cat_id, fb_class FROM up WHERE fb_class IS NOT NULL ORDER BY cat_id, depth
)
SELECT i.business_date, f.outlet_id AS outlet_id,
       COALESCE(cc.fb_class, NULLIF(w.cost_class, ''), 'OTHER')::text AS class,
       COALESCE(sum(i.net_amount), 0)::numeric AS revenue_net,
       COALESCE(sum(i.net_amount) FILTER (WHERE l.id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM pos_recipe_lines r WHERE r.property_id = l.property_id AND r.product_id = l.product_id)), 0)::numeric AS no_recipe_revenue
  FROM folio_items i
  JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id AND f.folio_type = 'OUTLET' AND f.outlet_id IS NOT NULL
  JOIN pos_outlets o ON o.property_id = f.property_id AND o.id = f.outlet_id
  LEFT JOIN inv_warehouses w ON w.property_id = o.property_id AND w.id = o.warehouse_id
  LEFT JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
  LEFT JOIN pos_bill_lines l ON l.property_id = i.property_id AND l.folio_item_id = COALESCE(i.reverses_item_id, i.id)
  LEFT JOIN pos_products p ON p.property_id = l.property_id AND p.id = l.product_id
  LEFT JOIN cat_class cc ON cc.cat_id = p.category_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date AND @to_date
   AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')
   AND NOT (i.source = 'TRANSFER' OR i.reference_type = 'FOLIO_TRANSFER')
   AND COALESCE(l.is_comp, false) = false
 GROUP BY i.business_date, f.outlet_id, COALESCE(cc.fb_class, NULLIF(w.cost_class, ''), 'OTHER')
 ORDER BY i.business_date, f.outlet_id;

-- The net sales of the banquet by business day and class (36 4.9, F13): the net amount of the food and beverage lines of the posted sessions, from the EVENT folios, before service charge and tax. A reversal
-- takes the line it reverses; an adjustment that names the item it corrects (reference FOLIO_ITEM) takes that item's line; a transfer to a master folio is not revenue and not read. The class is the one the
-- line (or the component of a package) froze when it was added. A line with no POS product, or whose product has no recipe, is also counted apart (no stock was deducted for it).
-- name: BanquetFbRevenueOfRange :many
SELECT i.business_date, COALESCE(NULLIF(COALESCE(c.fb_class, l.fb_class), ''), 'OTHER')::text AS class,
       COALESCE(sum(i.net_amount), 0)::numeric AS revenue_net,
       COALESCE(sum(i.net_amount) FILTER (WHERE NOT EXISTS (SELECT 1 FROM pos_recipe_lines r WHERE r.property_id = l.property_id AND r.product_id = COALESCE(c.pos_product_id, l.pos_product_id))), 0)::numeric AS no_recipe_revenue
  FROM folio_items i
  JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id AND f.folio_type = 'EVENT'
  JOIN folio_items o ON o.property_id = i.property_id
                    AND o.id = COALESCE(i.reverses_item_id, CASE WHEN i.reference_type = 'FOLIO_ITEM' AND i.reference_id ~ '^[0-9]+$' THEN i.reference_id::bigint END, i.id)
  JOIN banquet_posting_items pi ON pi.property_id = o.property_id AND pi.folio_item_id = o.id
  JOIN banquet_event_lines l ON l.property_id = pi.property_id AND l.id = pi.line_id
  LEFT JOIN banquet_line_components c ON c.property_id = pi.property_id AND c.line_id = pi.line_id AND c.item_id = pi.component_item_id
  LEFT JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date AND @to_date
   AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(cc.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')
   AND NOT (i.source = 'TRANSFER' OR i.reference_type = 'FOLIO_TRANSFER')
 GROUP BY i.business_date, COALESCE(NULLIF(COALESCE(c.fb_class, l.fb_class), ''), 'OTHER')
 ORDER BY i.business_date;

-- The warehouse the banquet takes its goods from: its own cost rows are read beside the outlets that sell from the same warehouse.
-- name: BanquetWarehouse :one
SELECT warehouse_id FROM banquet_settings WHERE property_id = @property_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The snapshots (written by the close, idempotent for a day: the day is deleted and written again)

-- name: ListSnapshotDays :many
SELECT business_date FROM inv_snapshot_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date AND @to_date ORDER BY business_date;

-- name: DeleteCostDaysOfDay :exec
DELETE FROM inv_cost_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @business_date;

-- name: DeleteFbCostDaysOfDay :exec
DELETE FROM inv_fb_cost_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @business_date;

-- name: DeleteBalanceDaysOfDay :exec
DELETE FROM inv_balance_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @business_date;

-- name: DeleteSnapshotDay :exec
DELETE FROM inv_snapshot_days WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date = @business_date;

-- name: InsertSnapshotDay :exec
INSERT INTO inv_snapshot_days (tenant_id, property_id, business_date) VALUES (@tenant_id, @property_id, @business_date);

-- name: InsertCostDay :exec
INSERT INTO inv_cost_days (tenant_id, property_id, business_date, department_id, outlet_id, cost_kind, amount)
VALUES (@tenant_id, @property_id, @business_date, sqlc.narg(department_id), sqlc.narg(outlet_id), @cost_kind, @amount);

-- name: InsertFbCostDay :exec
INSERT INTO inv_fb_cost_days (tenant_id, property_id, business_date, source, outlet_id, class, revenue_net, no_recipe_revenue, sale_cost, waste_cost, staff_meal_cost, entertainment_cost)
VALUES (@tenant_id, @property_id, @business_date, @source, sqlc.narg(outlet_id), @class, @revenue_net, @no_recipe_revenue, @sale_cost, @waste_cost, @staff_meal_cost, @entertainment_cost);

-- The balance at the end of the day of every pair that moved on it, from the ledger up to the day (so a day taken again later, or a rebuild, writes the same figures).
-- name: InsertBalanceDaysOfDay :exec
INSERT INTO inv_balance_days (tenant_id, property_id, warehouse_id, item_id, business_date, qty, value)
SELECT m.tenant_id, m.property_id, m.warehouse_id, m.item_id, @business_date::date, sum(m.qty_delta), sum(m.value_delta)
  FROM inv_movements m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date <= @business_date::date
   AND EXISTS (SELECT 1 FROM inv_movements d WHERE d.property_id = m.property_id AND d.warehouse_id = m.warehouse_id AND d.item_id = m.item_id AND d.business_date = @business_date::date)
 GROUP BY m.tenant_id, m.property_id, m.warehouse_id, m.item_id;

-- name: ListCostDays :many
SELECT business_date, department_id, outlet_id, cost_kind, amount FROM inv_cost_days
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date AND @to_date
 ORDER BY business_date, cost_kind;

-- name: ListFbCostDays :many
SELECT business_date, source, outlet_id, class, revenue_net, no_recipe_revenue, sale_cost, waste_cost, staff_meal_cost, entertainment_cost FROM inv_fb_cost_days
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND business_date BETWEEN @from_date AND @to_date
 ORDER BY business_date, source, outlet_id, class;

-- The rooms the amenities of each night were made for (the occupied rooms of the daily cost).
-- name: AmenityRoomsOfRange :many
SELECT business_date, rooms FROM inv_amenity_runs
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND status = 'POSTED' AND business_date BETWEEN @from_date AND @to_date
 ORDER BY business_date;

-- ---------------------------------------------------------------------------------------------------------------------
-- The targets of the cost percentage

-- name: ListCostTargets :many
SELECT t.id, t.source, t.outlet_id, t.class, t.target_pct, t.updated_at FROM inv_cost_targets t
 WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
 ORDER BY t.source DESC, (t.outlet_id IS NOT NULL), t.outlet_id, t.class;

-- name: UpsertCostTarget :exec
INSERT INTO inv_cost_targets (tenant_id, property_id, source, outlet_id, class, target_pct, updated_by)
VALUES (@tenant_id, @property_id, @source, sqlc.narg(outlet_id), @class, @target_pct, sqlc.narg(actor_id))
ON CONFLICT (property_id, source, COALESCE(outlet_id, 0), class) DO UPDATE SET target_pct = EXCLUDED.target_pct, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeleteCostTarget :execrows
DELETE FROM inv_cost_targets
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND source = @source AND COALESCE(outlet_id, 0) = COALESCE(sqlc.narg(outlet_id)::bigint, 0) AND class = @class;

-- ---------------------------------------------------------------------------------------------------------------------
-- The reconciliation (12.1) and the cost per outlet (12.4)

-- Stock value by item class at the start of the period (everything before it) and at its end.
-- name: ReconStockByClass :many
SELECT m.item_class::text AS item_class,
       COALESCE(sum(m.value_delta) FILTER (WHERE m.business_date < @from_date::date), 0)::numeric AS opening,
       COALESCE(sum(m.value_delta), 0)::numeric AS closing
  FROM inv_movements m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date <= @to_date::date AND m.item_class IN ('FOOD', 'BEVERAGE')
 GROUP BY m.item_class;

-- What moved in the period, by item class, cost class and kind.
-- name: ReconFlows :many
SELECT m.item_class::text AS item_class, m.cost_class::text AS cost_class, m.kind::text AS kind, COALESCE(sum(m.value_delta), 0)::numeric AS value_delta, COALESCE(sum(m.gl_amount), 0)::numeric AS gl_amount
  FROM inv_movements m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date AND m.item_class IN ('FOOD', 'BEVERAGE')
 GROUP BY m.item_class, m.cost_class, m.kind;

-- The warehouses of the food and beverage classes, and whether each has an approved count on a given day (D21).
-- name: FbWarehousesCounted :many
SELECT w.id AS warehouse_id, w.code, w.name, w.cost_class::text AS cost_class,
       (EXISTS (SELECT 1 FROM inv_counts c WHERE c.property_id = w.property_id AND c.warehouse_id = w.id AND c.status = 'APPROVED' AND c.business_date_started = @day::date)
        OR EXISTS (SELECT 1 FROM inv_movements x WHERE x.property_id = w.property_id AND x.warehouse_id = w.id AND x.business_date = @day::date AND x.kind IN ('COUNT_LOSS', 'COUNT_GAIN')))::boolean AS counted
  FROM inv_warehouses w
 WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.cost_class IN ('FOOD', 'BEVERAGE') AND w.is_active
 ORDER BY w.code;

-- Count variances by warehouse and item class in the period (a loss is positive), for the attribution to the outlets.
-- name: CountVarianceByWarehouse :many
SELECT m.warehouse_id, m.item_class::text AS item_class, COALESCE(sum(-m.value_delta), 0)::numeric AS variance
  FROM inv_movements m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date AND m.kind IN ('COUNT_LOSS', 'COUNT_GAIN') AND m.item_class IN ('FOOD', 'BEVERAGE')
 GROUP BY m.warehouse_id, m.item_class;

-- The outlets that sell from a warehouse.
-- name: OutletsWithWarehouse :many
SELECT o.id, o.code, o.name, o.warehouse_id, COALESCE(w.name, '')::text AS warehouse_name
  FROM pos_outlets o LEFT JOIN inv_warehouses w ON w.property_id = o.property_id AND w.id = o.warehouse_id
 WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id
 ORDER BY o.code;

-- The net of the accounts the cost reports are checked against, from the journals, as of a date (the period is the difference of two dates).
-- name: GLKeyBalancesOf :many
SELECT m.map_key::text AS map_key,
       COALESCE((SELECT sum(l.debit - l.credit) FROM gl_journal_lines l JOIN gl_journals j ON j.property_id = l.property_id AND j.id = l.journal_id
                  WHERE l.tenant_id = m.tenant_id AND l.property_id = m.property_id AND l.account_id = m.account_id AND j.journal_date <= @as_of::date), 0)::numeric AS balance
  FROM gl_account_map m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = ANY(@keys::text[]);

-- ---------------------------------------------------------------------------------------------------------------------
-- The value of the stock (12.5): by warehouse and category, live (the balances) or as of a closed day (the snapshot)

-- name: ValueLive :many
SELECT b.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, c.id AS category_id, c.code AS category_code, c.name AS category_name, c.inventory_account_id AS account_id,
       count(*)::int AS items, COALESCE(sum(b.value), 0)::numeric AS value
  FROM inv_balances b
  JOIN inv_warehouses w ON w.property_id = b.property_id AND w.id = b.warehouse_id
  JOIN inv_items i ON i.property_id = b.property_id AND i.id = b.item_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND (b.qty <> 0 OR b.value <> 0)
 GROUP BY b.warehouse_id, w.code, w.name, c.id, c.code, c.name, c.inventory_account_id
 ORDER BY w.code, c.code;

-- name: ValueAsOf :many
WITH last AS (
    SELECT DISTINCT ON (d.warehouse_id, d.item_id) d.warehouse_id, d.item_id, d.qty, d.value
      FROM inv_balance_days d
     WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.business_date <= @as_of::date
     ORDER BY d.warehouse_id, d.item_id, d.business_date DESC
)
SELECT b.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, c.id AS category_id, c.code AS category_code, c.name AS category_name, c.inventory_account_id AS account_id,
       count(*)::int AS items, COALESCE(sum(b.value), 0)::numeric AS value
  FROM last b
  JOIN inv_warehouses w ON w.property_id = @property_id AND w.id = b.warehouse_id
  JOIN inv_items i ON i.property_id = @property_id AND i.id = b.item_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE b.qty <> 0 OR b.value <> 0
 GROUP BY b.warehouse_id, w.code, w.name, c.id, c.code, c.name, c.inventory_account_id
 ORDER BY w.code, c.code;

-- name: AccountNames :many
SELECT a.id, a.code, a.name FROM gl_accounts a WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = ANY(@ids::bigint[]);

-- ---------------------------------------------------------------------------------------------------------------------
-- Low stock, negative stock, received and not invoiced, usage

-- name: LowStock :many
SELECT b.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code,
       b.qty, l.min_qty, l.max_qty, l.reorder_qty, b.value
  FROM inv_balances b
  JOIN inv_levels l ON l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id AND l.min_qty > 0
  JOIN inv_warehouses w ON w.property_id = b.property_id AND w.id = b.warehouse_id
  JOIN inv_items i ON i.property_id = b.property_id AND i.id = b.item_id AND i.is_active
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.qty < l.min_qty
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR b.warehouse_id = sqlc.narg(warehouse_id))
 ORDER BY (b.qty - l.min_qty), w.code, i.code;

-- name: CountLowStock :one
SELECT count(*)::int FROM inv_balances b JOIN inv_levels l ON l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id AND l.min_qty > 0
  JOIN inv_items i ON i.property_id = b.property_id AND i.id = b.item_id AND i.is_active
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.qty < l.min_qty;

-- The shortfalls that are still open: stock sold before the goods came in (the source says which kind of sale), at the estimated cost.
-- name: OpenShortfalls :many
SELECT s.id, s.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code,
       s.qty_open, s.est_unit_cost, s.est_value_open, s.business_date, s.source_type, COALESCE(b.qty, 0)::numeric AS balance_qty
  FROM inv_shortfalls s
  JOIN inv_warehouses w ON w.property_id = s.property_id AND w.id = s.warehouse_id
  JOIN inv_items i ON i.property_id = s.property_id AND i.id = s.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  LEFT JOIN inv_balances b ON b.warehouse_id = s.warehouse_id AND b.item_id = s.item_id
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN'
 ORDER BY s.business_date, s.id;

-- Receipts that nobody has billed (or billed in part): the remaining value of the receipt lines, with the date received, for the age. Receipts that were reversed are not listed.
-- name: ReceivedNotInvoiced :many
SELECT d.id AS draft_id, d.status, r.id AS receipt_id, r.receipt_number, r.received_on, s.id AS supplier_id, s.name AS supplier_name, w.name AS warehouse_name,
       COALESCE(sum(CASE WHEN l.qty_stock - l.qty_billed_stock - l.qty_returned_stock > 0 THEN l.value - l.value_billed - l.value_returned ELSE 0 END), 0)::numeric(18,3) AS remaining_value
  FROM inv_bill_drafts d
  JOIN inv_receipts r ON r.property_id = d.property_id AND r.id = d.receipt_id AND r.received_on <= @as_of::date
  JOIN suppliers s ON s.property_id = d.property_id AND s.id = d.supplier_id
  JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
  LEFT JOIN inv_receipt_lines l ON l.property_id = d.property_id AND l.receipt_id = d.receipt_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.status IN ('OPEN', 'PARTIAL') AND r.status = 'POSTED'
 GROUP BY d.id, r.id, s.id, w.id
HAVING COALESCE(sum(CASE WHEN l.qty_stock - l.qty_billed_stock - l.qty_returned_stock > 0 THEN l.value - l.value_billed - l.value_returned ELSE 0 END), 0) > 0
 ORDER BY r.received_on, r.id;

-- What the departments took from the stock in the period: the issues (a use, the amenities, waste, staff meals, entertainment) by department and item, at the cost of the movement.
-- name: UsageByDepartment :many
SELECT m.gl_department_id AS department_id, COALESCE(d.name, '')::text AS department_name, i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code,
       c.name AS category_name, m.kind::text AS kind, COALESCE(sum(-m.qty_delta), 0)::numeric AS qty, COALESCE(sum(-m.value_delta), 0)::numeric AS value
  FROM inv_movements m
  JOIN inv_items i ON i.property_id = m.property_id AND i.id = m.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  LEFT JOIN departments d ON d.property_id = m.property_id AND d.id = m.gl_department_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date AND m.kind IN ('ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT')
   AND (sqlc.narg(department_id)::bigint IS NULL OR m.gl_department_id = sqlc.narg(department_id))
 GROUP BY m.gl_department_id, d.name, i.id, i.code, i.name, u.code, c.name, m.kind
HAVING sum(-m.value_delta) <> 0 OR sum(-m.qty_delta) <> 0
 ORDER BY d.name NULLS LAST, m.kind, sum(-m.value_delta) DESC, i.code;

-- The names of the departments and outlets that appear in the cost rows.
-- name: DepartmentNames :many
SELECT d.id, d.code, d.name FROM departments d WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id ORDER BY d.code;

-- ---------------------------------------------------------------------------------------------------------------------
-- The setup check FB_CLASS_REVENUE_MISMATCH (35 4.9, 36 F13): the class of a POS category (its own, else its parent's) or of a banquet item against the revenue account of the charge code it sells on
-- (food 4210, beverage 4220). The cost reports follow the class and the general ledger follows the charge code; the warning says when the two part.
-- name: ListFbClassMismatches :many
SELECT 'POS'::text AS source, p.code AS ref, p.name AS name, COALESCE(c.fb_class, pc.fb_class)::text AS class, cc.code AS charge_code, cc.gl_account_code AS account
  FROM pos_products p
  JOIN pos_categories c ON c.property_id = p.property_id AND c.id = p.category_id
  LEFT JOIN pos_categories pc ON pc.property_id = c.property_id AND pc.id = c.parent_id
  JOIN charge_codes cc ON cc.property_id = p.property_id AND cc.id = p.charge_code_id
 WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.is_active
   AND ((COALESCE(c.fb_class, pc.fb_class) = 'FOOD' AND cc.gl_account_code = '4220') OR (COALESCE(c.fb_class, pc.fb_class) = 'BEVERAGE' AND cc.gl_account_code = '4210'))
UNION ALL
SELECT 'BANQUET'::text, i.code, i.name, i.fb_class::text, cc.code, cc.gl_account_code
  FROM banquet_items i
  JOIN charge_codes cc ON cc.property_id = i.property_id AND cc.id = i.charge_code_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.is_active
   AND ((i.fb_class = 'FOOD' AND cc.gl_account_code = '4220') OR (i.fb_class = 'BEVERAGE' AND cc.gl_account_code = '4210'))
ORDER BY 1, 2
LIMIT 100;
