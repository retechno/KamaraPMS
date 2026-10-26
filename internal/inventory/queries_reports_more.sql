-- Inventory, PR 6a (docs/architecture/35-inventory.md 12.5, 22): the other reports. Every query is scoped by tenant_id and property_id and only reads: nothing here writes the ledger.
-- A document that was reversed is netted at the day of its reversal (the ISSUE_REVERSAL movement has the purpose of the document it reverses), so a period that holds both days adds up to nothing.

-- ---------------------------------------------------------------------------------------------------------------------
-- Waste by reason (A1): the waste issues, and the dishes that were cooked and thrown away after a POS void (COGS_TO_WASTE, no stock change: the value is the cost that moved to waste).

-- name: WasteReport :many
SELECT COALESCE(iss.reason_code, CASE WHEN m.kind = 'COGS_TO_WASTE' THEN 'POS_VOID' ELSE 'OTHER' END)::text AS reason,
       m.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name,
       m.gl_department_id AS department_id, COALESCE(d.name, '')::text AS department_name,
       i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, c.name AS category_name,
       COALESCE(sum(-m.qty_delta), 0)::numeric AS qty,
       COALESCE(sum(CASE WHEN m.kind = 'COGS_TO_WASTE' THEN m.gl_amount ELSE -m.value_delta END), 0)::numeric AS value,
       (count(DISTINCT m.source_type || ':' || m.source_id::text) FILTER (WHERE m.kind <> 'ISSUE_REVERSAL') - count(DISTINCT m.source_type || ':' || m.source_id::text) FILTER (WHERE m.kind = 'ISSUE_REVERSAL'))::int AS docs
  FROM inv_movements m
  JOIN inv_warehouses w ON w.property_id = m.property_id AND w.id = m.warehouse_id
  JOIN inv_items i ON i.property_id = m.property_id AND i.id = m.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  LEFT JOIN inv_issues iss ON m.source_type = 'INV_ISSUE' AND iss.property_id = m.property_id AND iss.id = m.source_id
  LEFT JOIN departments d ON d.property_id = m.property_id AND d.id = m.gl_department_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date
   AND (m.kind IN ('WASTE', 'COGS_TO_WASTE') OR (m.kind = 'ISSUE_REVERSAL' AND iss.purpose = 'WASTE'))
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR m.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(department_id)::bigint IS NULL OR m.gl_department_id = sqlc.narg(department_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
 GROUP BY 1, m.warehouse_id, w.code, w.name, m.gl_department_id, d.name, i.id, i.code, i.name, u.code, c.name
HAVING sum(-m.qty_delta) <> 0 OR sum(CASE WHEN m.kind = 'COGS_TO_WASTE' THEN m.gl_amount ELSE -m.value_delta END) <> 0 OR (count(DISTINCT m.source_type || ':' || m.source_id::text) FILTER (WHERE m.kind <> 'ISSUE_REVERSAL') - count(DISTINCT m.source_type || ':' || m.source_id::text) FILTER (WHERE m.kind = 'ISSUE_REVERSAL')) <> 0
 ORDER BY 1, w.code, d.name NULLS LAST, 13 DESC, i.code;

-- The documents of the same rows, by reason (a document with several items is counted once; a reversal takes its document off).
-- name: WasteDocsByReason :many
SELECT COALESCE(iss.reason_code, CASE WHEN m.kind = 'COGS_TO_WASTE' THEN 'POS_VOID' ELSE 'OTHER' END)::text AS reason,
       COALESCE(sum(CASE WHEN m.kind = 'COGS_TO_WASTE' THEN m.gl_amount ELSE -m.value_delta END), 0)::numeric AS value,
       (count(DISTINCT m.source_type || ':' || m.source_id::text) FILTER (WHERE m.kind <> 'ISSUE_REVERSAL') - count(DISTINCT m.source_type || ':' || m.source_id::text) FILTER (WHERE m.kind = 'ISSUE_REVERSAL'))::int AS docs
  FROM inv_movements m
  JOIN inv_items i ON i.property_id = m.property_id AND i.id = m.item_id
  LEFT JOIN inv_issues iss ON m.source_type = 'INV_ISSUE' AND iss.property_id = m.property_id AND iss.id = m.source_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date BETWEEN @from_date AND @to_date
   AND (m.kind IN ('WASTE', 'COGS_TO_WASTE') OR (m.kind = 'ISSUE_REVERSAL' AND iss.purpose = 'WASTE'))
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR m.warehouse_id = sqlc.narg(warehouse_id))
   AND (sqlc.narg(department_id)::bigint IS NULL OR m.gl_department_id = sqlc.narg(department_id))
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id))
 GROUP BY 1
 ORDER BY 1;

-- ---------------------------------------------------------------------------------------------------------------------
-- Count variances (A2): the approved counts of the period and their lines. The day of an approval is the day of its movements; a count with no variance has none, so the business day that was open at
-- the moment of the decision, else the day the count started.

-- name: CountVarianceCounts :many
WITH cnt AS (
    SELECT c.id, c.tenant_id, c.property_id, c.cnt_number, c.warehouse_id, c.scope, c.business_date_started, c.decided_by, c.decided_at, COALESCE(
               (SELECT min(m.business_date) FROM inv_movements m WHERE m.property_id = c.property_id AND m.source_type = 'INV_COUNT' AND m.source_id = c.id),
               (SELECT b.business_date FROM business_days b WHERE b.property_id = c.property_id AND b.opened_at <= c.decided_at ORDER BY b.opened_at DESC LIMIT 1),
               c.business_date_started) AS approved_on
      FROM inv_counts c
     WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.status = 'APPROVED'
)
SELECT k.id, k.cnt_number, k.warehouse_id, w.code AS warehouse_code, w.name AS warehouse_name, k.scope, k.business_date_started, k.approved_on, COALESCE(u.full_name, '')::text AS approver_name,
       COALESCE(sum(l.variance_value) FILTER (WHERE l.variance_value > 0), 0)::numeric AS gain,
       COALESCE(-sum(l.variance_value) FILTER (WHERE l.variance_value < 0), 0)::numeric AS loss,
       count(*) FILTER (WHERE l.variance_qty IS NOT NULL AND l.variance_qty <> 0)::int AS lines_with_variance,
       count(*)::int AS lines
  FROM cnt k
  JOIN inv_warehouses w ON w.property_id = k.property_id AND w.id = k.warehouse_id
  LEFT JOIN users u ON u.tenant_id = k.tenant_id AND u.id = k.decided_by
  LEFT JOIN inv_count_lines l ON l.property_id = k.property_id AND l.count_id = k.id
 WHERE k.approved_on BETWEEN @from_date::date AND @to_date::date
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR k.warehouse_id = sqlc.narg(warehouse_id))
 GROUP BY k.id, k.cnt_number, k.warehouse_id, w.code, w.name, k.scope, k.business_date_started, k.approved_on, u.full_name
 ORDER BY k.approved_on, k.id;

-- name: CountVarianceLines :many
WITH cnt AS (
    SELECT c.id, c.warehouse_id, COALESCE(
               (SELECT min(m.business_date) FROM inv_movements m WHERE m.property_id = c.property_id AND m.source_type = 'INV_COUNT' AND m.source_id = c.id),
               (SELECT b.business_date FROM business_days b WHERE b.property_id = c.property_id AND b.opened_at <= c.decided_at ORDER BY b.opened_at DESC LIMIT 1),
               c.business_date_started) AS approved_on, c.cnt_number
      FROM inv_counts c
     WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.status = 'APPROVED'
)
SELECT k.id AS count_id, k.cnt_number, k.approved_on, k.warehouse_id, w.code AS warehouse_code, i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code, cat.name AS category_name,
       COALESCE(l.expected_qty, l.book_qty)::numeric AS book_qty, COALESCE(l.counted_qty, 0)::numeric AS counted_qty, COALESCE(l.variance_qty, 0)::numeric AS variance_qty, COALESCE(l.variance_value, 0)::numeric AS variance_value
  FROM cnt k
  JOIN inv_count_lines l ON l.property_id = @property_id AND l.count_id = k.id AND l.counted_qty IS NOT NULL
  JOIN inv_warehouses w ON w.property_id = @property_id AND w.id = k.warehouse_id
  JOIN inv_items i ON i.property_id = @property_id AND i.id = l.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
  JOIN inv_categories cat ON cat.property_id = i.property_id AND cat.id = i.category_id
 WHERE k.approved_on BETWEEN @from_date::date AND @to_date::date AND COALESCE(l.variance_qty, 0) <> 0
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR k.warehouse_id = sqlc.narg(warehouse_id))
 ORDER BY k.approved_on, k.id, l.variance_value, i.code;

-- ---------------------------------------------------------------------------------------------------------------------
-- Purchase price variances (A3): the lines of the bills matched to receipts. The price of the receipt is the price of the order (or the one typed at the door); the invoice is what the supplier billed.

-- name: PriceVarianceLines :many
SELECT sb.supplier_id, s.name AS supplier_name, sb.id AS bill_id, sb.bill_number, sb.bill_date, rl.item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code,
       bm.qty_stock, bm.bill_cost, bm.receipt_value, bm.variance
  FROM inv_bill_matches bm
  JOIN supplier_bills sb ON sb.property_id = bm.property_id AND sb.id = bm.bill_id AND sb.status = 'POSTED'
  JOIN suppliers s ON s.property_id = sb.property_id AND s.id = sb.supplier_id
  JOIN inv_receipt_lines rl ON rl.property_id = bm.property_id AND rl.receipt_id = bm.receipt_id AND rl.line_no = bm.receipt_line_no
  JOIN inv_items i ON i.property_id = rl.property_id AND i.id = rl.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE bm.tenant_id = @tenant_id AND bm.property_id = @property_id AND bm.unmatched_at IS NULL AND sb.bill_date BETWEEN @from_date AND @to_date
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR sb.supplier_id = sqlc.narg(supplier_id))
 ORDER BY s.name, sb.bill_date, sb.id, bm.bill_line_no;

-- name: PriceToleranceOf :one
SELECT price_tolerance_pct FROM inventory_settings WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Transfers without a requisition (A4)

-- name: TransfersWithoutRequisition :many
SELECT t.id, t.trf_number, t.business_date, t.status, t.from_warehouse_id, fw.code AS from_code, fw.name AS from_name, t.to_warehouse_id, tw.code AS to_code, tw.name AS to_name,
       COALESCE(u.full_name, '')::text AS created_by_name, t.note,
       l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, un.code AS unit_code, l.qty_stock, l.value
  FROM inv_transfers t
  JOIN inv_transfer_lines l ON l.property_id = t.property_id AND l.transfer_id = t.id
  JOIN inv_warehouses fw ON fw.property_id = t.property_id AND fw.id = t.from_warehouse_id
  JOIN inv_warehouses tw ON tw.property_id = t.property_id AND tw.id = t.to_warehouse_id
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units un ON un.property_id = i.property_id AND un.id = i.stock_unit_id
  LEFT JOIN users u ON u.tenant_id = t.tenant_id AND u.id = t.created_by
 WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.requisition_id IS NULL AND t.business_date BETWEEN @from_date AND @to_date
   AND (sqlc.narg(from_warehouse_id)::bigint IS NULL OR t.from_warehouse_id = sqlc.narg(from_warehouse_id))
   AND (sqlc.narg(to_warehouse_id)::bigint IS NULL OR t.to_warehouse_id = sqlc.narg(to_warehouse_id))
 ORDER BY t.business_date, t.id, l.line_no;

-- ---------------------------------------------------------------------------------------------------------------------
-- Amenities by month (A5): the runs of the nights (a reversed night is out), and the cost of each item from the lines of the issue they made.

-- name: AmenityMonths :many
SELECT date_trunc('month', r.business_date)::date AS month, count(*)::int AS nights, COALESCE(sum(r.rooms), 0)::int AS rooms, COALESCE(sum(r.guests), 0)::int AS guests,
       COALESCE(sum(r.comp_rooms), 0)::int AS comp_rooms, COALESCE(sum(r.house_use_rooms), 0)::int AS house_use_rooms, COALESCE(sum(r.total_value), 0)::numeric AS cost
  FROM inv_amenity_runs r
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'POSTED' AND r.business_date BETWEEN @from_date AND @to_date
 GROUP BY 1 ORDER BY 1;

-- name: AmenityMonthItems :many
SELECT date_trunc('month', r.business_date)::date AS month, i.id AS item_id, i.code AS item_code, i.name AS item_name, u.code AS unit_code,
       COALESCE(sum(l.qty_stock), 0)::numeric AS qty, COALESCE(sum(il.value), 0)::numeric AS cost
  FROM inv_amenity_runs r
  JOIN inv_amenity_run_lines l ON l.property_id = r.property_id AND l.run_id = r.id
  LEFT JOIN inv_issue_lines il ON il.property_id = r.property_id AND il.issue_id = r.issue_id AND il.line_no = l.issue_line_no
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units u ON u.property_id = i.property_id AND u.id = i.stock_unit_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'POSTED' AND r.business_date BETWEEN @from_date AND @to_date
 GROUP BY 1, i.id, i.code, i.name, u.code
 ORDER BY 1, 7 DESC, i.code;

-- ---------------------------------------------------------------------------------------------------------------------
-- Documents waiting in the approval chain (A6): the purchase requests and the requisitions that are still in the chain, with the level that holds them and who may decide it.
-- The age counts from the moment the current level was reached (activated_at), not from the submission.

-- name: PendingApprovalDocs :many
WITH docs AS (
    SELECT 'PR'::text AS doc_type, r.id, r.request_number AS number, r.est_total, r.department_id, r.requester_id, r.submitted_at, r.cycle
      FROM inv_requests r WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'SUBMITTED'
    UNION ALL
    SELECT 'SR'::text, r.id, r.sr_number, r.est_total, r.department_id, r.requester_id, r.submitted_at, r.cycle
      FROM inv_requisitions r WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.status = 'SUBMITTED'
)
SELECT d.doc_type, d.id, d.number, d.est_total, d.department_id, COALESCE(dep.name, '')::text AS department_name, d.requester_id, COALESCE(u.full_name, '')::text AS requester_name, d.submitted_at,
       s.level_no, s.name AS step_name, s.approver_kind, s.permission, s.activated_at,
       COALESCE(CASE s.approver_kind
           WHEN 'PERMISSION' THEN (SELECT string_agg(DISTINCT x.full_name, ', ') FROM users x
                                    WHERE x.tenant_id = @tenant_id AND x.is_active AND x.id <> d.requester_id
                                      AND (x.is_tenant_admin OR EXISTS (SELECT 1 FROM user_properties up JOIN role_permissions rp ON rp.role_id = up.role_id
                                                                         WHERE up.tenant_id = @tenant_id AND up.user_id = x.id AND up.property_id = @property_id AND rp.permission_code = s.permission)))
           ELSE (SELECT string_agg(DISTINCT x.full_name, ', ') FROM users x
                  WHERE x.tenant_id = @tenant_id AND x.is_active AND x.id <> d.requester_id
                    AND x.id IN (CASE WHEN dep.head_user_id IS NOT NULL THEN dep.head_user_id ELSE pd.head_user_id END, CASE WHEN dep.head_user_id IS NOT NULL THEN dep.deputy_user_id ELSE pd.deputy_user_id END))
       END, '')::text AS awaiting
  FROM docs d
  JOIN inv_approval_steps s ON s.property_id = @property_id AND s.cycle = d.cycle AND s.status = 'ACTIVE'
                           AND ((d.doc_type = 'PR' AND s.request_id = d.id) OR (d.doc_type = 'SR' AND s.requisition_id = d.id))
  LEFT JOIN users u ON u.tenant_id = @tenant_id AND u.id = d.requester_id
  LEFT JOIN departments dep ON dep.property_id = @property_id AND dep.id = d.department_id
  LEFT JOIN departments pd ON pd.property_id = @property_id AND pd.id = dep.parent_id
 ORDER BY COALESCE(s.activated_at, d.submitted_at), d.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The recipe cost list (A7): the products of the POS and the base lines of their recipes. The options are not in the list: the cost of a portion is the cost of the base recipe.

-- name: ListProductsForCostList :many
SELECT p.id, p.code, p.name, p.category_id, COALESCE(c.name, '')::text AS category_name,
       EXISTS (SELECT 1 FROM pos_recipe_lines r WHERE r.property_id = p.property_id AND r.product_id = p.id AND r.option_id IS NULL) AS has_recipe
  FROM pos_products p LEFT JOIN pos_categories c ON c.property_id = p.property_id AND c.id = p.category_id
 WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.is_active
 ORDER BY p.code;

-- name: ListBaseRecipeLinesAll :many
SELECT r.product_id, r.inventory_item_id AS item_id, r.quantity, iu.stock_num, iu.stock_den, i.reference_cost
  FROM pos_recipe_lines r
  JOIN inv_items i ON i.property_id = r.property_id AND i.id = r.inventory_item_id
  JOIN inv_item_units iu ON iu.property_id = r.property_id AND iu.item_id = r.inventory_item_id AND iu.unit_id = r.unit_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.option_id IS NULL
 ORDER BY r.product_id, r.id;

-- name: ListBalancesOfWarehouse :many
SELECT item_id, qty, value, last_unit_cost FROM inv_balances WHERE tenant_id = @tenant_id AND property_id = @property_id AND warehouse_id = @warehouse_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Staff meals and entertainment (A8): one row for each document with its value; the reversal of a document has a negative value and negative covers in the report, so it nets to nothing.

-- name: MealIssues :many
SELECT iss.id, iss.iss_number, iss.purpose, iss.department_id, COALESCE(d.name, '')::text AS department_name, iss.business_date, COALESCE(iss.covers, 0)::int AS covers,
       (iss.reverses_issue_id IS NOT NULL)::boolean AS is_reversal, COALESCE(sum(-m.value_delta), 0)::numeric AS value
  FROM inv_issues iss
  JOIN inv_movements m ON m.property_id = iss.property_id AND m.source_type = 'INV_ISSUE' AND m.source_id = iss.id AND m.kind IN ('STAFF_MEAL', 'ENTERTAINMENT', 'ISSUE_REVERSAL')
  LEFT JOIN departments d ON d.property_id = iss.property_id AND d.id = iss.department_id
 WHERE iss.tenant_id = @tenant_id AND iss.property_id = @property_id AND iss.purpose IN ('STAFF_MEAL', 'ENTERTAINMENT') AND iss.business_date BETWEEN @from_date AND @to_date
   AND (sqlc.narg(department_id)::bigint IS NULL OR iss.department_id = sqlc.narg(department_id))
 GROUP BY iss.id, iss.iss_number, iss.purpose, iss.department_id, d.name, iss.business_date, iss.covers, iss.reverses_issue_id
 ORDER BY d.name NULLS LAST, iss.purpose, iss.business_date, iss.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The documents behind a day of the daily cost (C): the same classification of a movement into a kind of cost as CostMovementsOfRange (a test compares the two), by document.

-- name: CostDayDocuments :many
WITH b AS (
    SELECT m.business_date, m.gl_department_id AS department_id, m.outlet_id, m.cost_class, m.kind, m.value_delta, m.gl_amount, m.gl_debit_account_id, m.gl_credit_account_id,
           m.source_type, m.source_id, m.warehouse_id, m.item_id, m.qty_delta,
           COALESCE(iss.origin, '') AS origin, (st.engineering_department_id IS NOT NULL AND m.gl_department_id = st.engineering_department_id) AS engineering,
           ppv.account_id AS ppv_account_id
      FROM inv_movements m
      LEFT JOIN inv_issues iss ON m.source_type = 'INV_ISSUE' AND iss.property_id = m.property_id AND iss.id = m.source_id
      LEFT JOIN inventory_settings st ON st.property_id = m.property_id
      LEFT JOIN gl_account_map ppv ON ppv.property_id = m.property_id AND ppv.map_key = 'INV_PPV'
     WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.business_date = @day
), r AS (
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id,
           CASE cost_class WHEN 'FOOD' THEN 'SALE_COST_FOOD' WHEN 'BEVERAGE' THEN 'SALE_COST_BEVERAGE' ELSE 'OTHER_USE' END AS cost_kind, -value_delta AS amount, -qty_delta AS qty
      FROM b WHERE kind IN ('POS_SALE', 'POS_SALE_RETURN', 'NEG_SETTLE')
    UNION ALL
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id,
           CASE cost_class WHEN 'FOOD' THEN 'SALE_COST_FOOD' WHEN 'BEVERAGE' THEN 'SALE_COST_BEVERAGE' ELSE 'OTHER_USE' END, -gl_amount, 0 FROM b WHERE kind = 'COGS_TO_WASTE'
    UNION ALL
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id, 'WASTE', gl_amount, 0 FROM b WHERE kind = 'COGS_TO_WASTE'
    UNION ALL
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id, kind, -value_delta, -qty_delta FROM b WHERE kind IN ('WASTE', 'STAFF_MEAL', 'ENTERTAINMENT')
    UNION ALL
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id,
           CASE WHEN origin = 'AMENITY' THEN 'AMENITY' WHEN engineering THEN 'ENGINEERING' ELSE 'OTHER_USE' END, -value_delta, -qty_delta
      FROM b WHERE kind = 'ISSUE'
    UNION ALL
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id, 'COUNT_VARIANCE', -value_delta, -qty_delta FROM b WHERE kind IN ('COUNT_LOSS', 'COUNT_GAIN')
    UNION ALL
    SELECT department_id, outlet_id, source_type, source_id, warehouse_id, item_id, 'PRICE_VARIANCE', CASE WHEN gl_debit_account_id = ppv_account_id THEN gl_amount ELSE -gl_amount END, 0
      FROM b WHERE kind = 'COST_ADJUST' AND ppv_account_id IS NOT NULL AND (gl_debit_account_id = ppv_account_id OR gl_credit_account_id = ppv_account_id)
)
SELECT r.cost_kind::text AS cost_kind, r.source_type::text AS source_type, r.source_id, r.department_id, COALESCE(d.name, '')::text AS department_name, r.outlet_id, COALESCE(o.name, '')::text AS outlet_name,
       COALESCE(CASE r.source_type
           WHEN 'INV_ISSUE' THEN (SELECT x.iss_number FROM inv_issues x WHERE x.property_id = @property_id AND x.id = r.source_id)
           WHEN 'INV_COUNT' THEN (SELECT x.cnt_number FROM inv_counts x WHERE x.property_id = @property_id AND x.id = r.source_id)
           WHEN 'INV_BILL' THEN (SELECT x.bill_number FROM supplier_bills x WHERE x.property_id = @property_id AND x.id = r.source_id)
           WHEN 'POS_QUEUE' THEN (SELECT COALESCE(pb.bill_number, be.number) FROM inv_consumption_queue q
                                    LEFT JOIN pos_bill_lines bl ON bl.property_id = q.property_id AND bl.id = q.pos_bill_line_id
                                    LEFT JOIN pos_bills pb ON pb.property_id = bl.property_id AND pb.id = bl.bill_id
                                    LEFT JOIN banquet_posting_items bpi ON bpi.property_id = q.property_id AND bpi.id = q.banquet_posting_line_id
                                    LEFT JOIN banquet_session_postings bp ON bp.property_id = bpi.property_id AND bp.id = bpi.posting_id
                                    LEFT JOIN banquet_sessions bs ON bs.property_id = bp.property_id AND bs.id = bp.session_id
                                    LEFT JOIN banquet_events be ON be.property_id = bs.property_id AND be.id = bs.event_id WHERE q.property_id = @property_id AND q.id = r.source_id)
       END, '')::text AS number,
       COALESCE(CASE r.source_type
           WHEN 'POS_QUEUE' THEN (SELECT bl.bill_id FROM inv_consumption_queue q JOIN pos_bill_lines bl ON bl.property_id = q.property_id AND bl.id = q.pos_bill_line_id WHERE q.property_id = @property_id AND q.id = r.source_id)
           ELSE r.source_id
       END, 0)::bigint AS link_id,
       count(*)::int AS lines, COALESCE(sum(r.amount), 0)::numeric AS amount
  FROM r
  LEFT JOIN departments d ON d.property_id = @property_id AND d.id = r.department_id
  LEFT JOIN pos_outlets o ON o.property_id = @property_id AND o.id = r.outlet_id
 GROUP BY r.cost_kind, r.source_type, r.source_id, r.department_id, d.name, r.outlet_id, o.name
HAVING sum(r.amount) <> 0
 ORDER BY r.cost_kind, 8, r.source_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- A purchase request from the items below their minimum (C): the levels and the purchase units of the items asked for.

-- name: LowStockOfItems :many
SELECT b.warehouse_id, i.id AS item_id, i.code AS item_code, i.name AS item_name, i.stock_unit_id, b.qty, l.min_qty, l.max_qty, l.reorder_qty
  FROM inv_balances b
  JOIN inv_levels l ON l.warehouse_id = b.warehouse_id AND l.item_id = b.item_id AND l.min_qty > 0
  JOIN inv_items i ON i.property_id = b.property_id AND i.id = b.item_id AND i.is_active
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.warehouse_id = @warehouse_id AND b.item_id = ANY(@item_ids::bigint[]) AND b.qty < l.min_qty
 ORDER BY i.code;
