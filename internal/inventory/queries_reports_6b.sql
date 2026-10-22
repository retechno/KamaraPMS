-- Inventory, PR 6b (docs/architecture/35-inventory.md 12.5, 22): the stock movement per item, the purchases per supplier and the price history, the slow and dead stock and the largest usage. Every query is
-- scoped by tenant_id and property_id and only reads. The reversal of a receipt or of a transfer has the kind of its own kind with the opposite sign; the reversal of an issue is an ISSUE_REVERSAL, which is counted with the
-- kind of the movement it reverses (the effective kind), so a sum by kind nets it out.

-- ---------------------------------------------------------------------------------------------------------------------
-- The stock movement per item (B9): by kind the net quantity and value of the period, and what the item stood at before the period, read from the same ledger so that the figures always agree.

-- name: ItemMovementsReport :many
SELECT i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, c.id AS category_id, c.name AS category_name, (CASE WHEN m.kind = 'ISSUE_REVERSAL' THEN o.kind ELSE m.kind END)::text AS kind,
       COALESCE(sum(m.qty_delta) FILTER (WHERE m.business_date < @from_date), 0)::numeric AS before_qty,
       COALESCE(sum(m.value_delta) FILTER (WHERE m.business_date < @from_date), 0)::numeric AS before_value,
       COALESCE(sum(m.qty_delta) FILTER (WHERE m.business_date >= @from_date), 0)::numeric AS period_qty,
       COALESCE(sum(m.value_delta) FILTER (WHERE m.business_date >= @from_date), 0)::numeric AS period_value
  FROM inv_movements m
  JOIN inv_items i ON i.property_id = m.property_id AND i.id = m.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  LEFT JOIN inv_movements o ON m.kind = 'ISSUE_REVERSAL' AND o.property_id = m.property_id AND o.id = m.reverses_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date <= @to_date
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR m.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
   AND (sqlc.narg(item_id)::bigint IS NULL OR m.item_id = sqlc.narg(item_id))
 GROUP BY i.id, i.code, i.name, u.code, c.id, c.name, 7
 ORDER BY c.name, i.code, 7;

-- ---------------------------------------------------------------------------------------------------------------------
-- Purchases per supplier and per item (B10): the lines of the receipts that stand (a reversed receipt is out; what was returned to the supplier is taken off), and the bills matched to them.

-- name: PurchaseLines :many
SELECT r.supplier_id, s.name AS supplier_name, r.id AS receipt_id, rl.item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, c.name AS category_name,
       (rl.qty_stock - rl.qty_returned_stock)::numeric AS qty, (rl.value - rl.value_returned)::numeric AS value
  FROM inv_receipts r
  JOIN inv_receipt_lines rl ON rl.property_id = r.property_id AND rl.receipt_id = r.id
  JOIN suppliers s ON s.property_id = r.property_id AND s.id = r.supplier_id
  JOIN inv_items i ON i.property_id = rl.property_id AND i.id = rl.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'POSTED' AND r.received_on BETWEEN @from_date AND @to_date
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR r.supplier_id = sqlc.narg(supplier_id))
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR r.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
 ORDER BY s.name, r.supplier_id, i.code, r.id;

-- name: PurchaseBillMatches :many
SELECT r.supplier_id, rl.item_id, bm.bill_id, bm.bill_cost
  FROM inv_bill_matches bm
  JOIN inv_receipt_lines rl ON rl.property_id = bm.property_id AND rl.receipt_id = bm.receipt_id AND rl.line_no = bm.receipt_line_no
  JOIN inv_receipts r ON r.property_id = rl.property_id AND r.id = rl.receipt_id
  JOIN supplier_bills sb ON sb.property_id = bm.property_id AND sb.id = bm.bill_id AND sb.status = 'POSTED'
  JOIN inv_items i ON i.property_id = rl.property_id AND i.id = rl.item_id
 WHERE bm.tenant_id = @tenant_id AND bm.property_id = @property_id AND bm.unmatched_at IS NULL AND r.status = 'POSTED' AND r.received_on BETWEEN @from_date AND @to_date
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR r.supplier_id = sqlc.narg(supplier_id))
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR r.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id));

-- name: PriceHistoryItem :one
SELECT i.id, i.code, i.name, u.code AS unit_code
  FROM inv_items i
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = @item_id;

-- name: PriceHistoryLines :many
SELECT r.id AS receipt_id, r.receipt_number, r.received_on, r.supplier_id, s.name AS supplier_name, pu.code AS purchase_unit_code, rl.qty_purchase, rl.qty_stock, rl.unit_price, rl.value
  FROM inv_receipts r
  JOIN inv_receipt_lines rl ON rl.property_id = r.property_id AND rl.receipt_id = r.id
  JOIN suppliers s ON s.property_id = r.property_id AND s.id = r.supplier_id
  JOIN inv_units pu ON pu.property_id = rl.property_id AND pu.id = rl.purchase_unit_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'POSTED' AND rl.item_id = @item_id AND r.received_on BETWEEN @from_date AND @to_date
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR r.supplier_id = sqlc.narg(supplier_id))
 ORDER BY r.received_on, r.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Slow and dead stock (B11): the items with stock that nothing consumed for a while. The last use is the last consumption (a sale, an issue, a waste, a staff meal, an entertainment) that was not reversed;
-- the age of the stock is the oldest layer that is left (FIFO) or the last entry of goods (average). A date that does not exist is answered as 1900-01-01 (the column cannot be null for the generated code).

-- name: SlowStock :many
SELECT b.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, b.item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, c.name AS category_name, b.qty, b.value,
       COALESCE((SELECT max(m.business_date) FROM inv_movements m
         WHERE m.property_id = b.property_id AND m.warehouse_id = b.warehouse_id AND m.item_id = b.item_id AND m.qty_delta < 0
           AND m.kind IN ('POS_SALE', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT')
           AND NOT EXISTS (SELECT 1 FROM inv_movements rv WHERE rv.property_id = m.property_id AND rv.reverses_id = m.id)), DATE '1900-01-01')::date AS last_out_on,
       COALESCE((SELECT min(l.received_on) FROM inv_layers l
         WHERE l.property_id = b.property_id AND l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id AND l.qty_remaining > 0), DATE '1900-01-01')::date AS oldest_layer_on,
       COALESCE((SELECT max(m.business_date) FROM inv_movements m
         WHERE m.property_id = b.property_id AND m.warehouse_id = b.warehouse_id AND m.item_id = b.item_id AND m.qty_delta > 0 AND m.kind IN ('OPENING', 'RECEIPT', 'TRANSFER_IN')), DATE '1900-01-01')::date AS last_in_on
  FROM inv_balances b
  JOIN inv_warehouses w ON w.property_id = b.property_id AND w.id = b.warehouse_id
  JOIN inv_items i ON i.property_id = b.property_id AND i.id = b.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.qty > 0
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR b.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
 ORDER BY w.code, i.code;

-- ---------------------------------------------------------------------------------------------------------------------
-- The largest usage (B12): the net quantity and value that left, by item, for the kinds asked for (a sale return nets a sale).

-- name: TopUsage :many
SELECT i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, c.name AS category_name,
       COALESCE(sum(-m.qty_delta), 0)::numeric AS qty, COALESCE(sum(-m.value_delta), 0)::numeric AS value
  FROM inv_movements m
  JOIN inv_items i ON i.property_id = m.property_id AND i.id = m.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  LEFT JOIN inv_movements o ON m.kind = 'ISSUE_REVERSAL' AND o.property_id = m.property_id AND o.id = m.reverses_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date
   AND (CASE WHEN m.kind = 'ISSUE_REVERSAL' THEN o.kind ELSE m.kind END) = ANY(@kinds::text[])
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR m.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
 GROUP BY i.id, i.code, i.name, u.code, c.name
HAVING sum(-m.value_delta) > 0
 ORDER BY 7 DESC, i.code;
