-- Inventory, PR 3 (docs/architecture/35-inventory.md 3.4, 5.4, 5.5, 5.9): the requisition, the transfer, the issue, the count and the amenities.
-- Every query is scoped by tenant_id and property_id. The ledger tables are still written only by ledger.go; the queries that write them are named Ledger* in queries.sql.

-- ---------------------------------------------------------------------------------------------------------------------
-- The store requisition (SR)

-- name: InsertRequisition :one
INSERT INTO inv_requisitions (tenant_id, property_id, sr_number, client_uuid, to_warehouse_id, from_warehouse_id, requester_id, department_id, needed_on, note, est_total, created_by, updated_by)
VALUES (@tenant_id, @property_id, @sr_number, @client_uuid, @to_warehouse_id, @from_warehouse_id, @requester_id, sqlc.narg(department_id), sqlc.narg(needed_on), sqlc.narg(note), @est_total,
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: GetRequisitionByClientUUID :one
SELECT id FROM inv_requisitions WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetRequisition :one
SELECT r.*, u.full_name AS requester_name, d.code AS department_code, d.name AS department_name,
       tw.code AS to_warehouse_code, tw.name AS to_warehouse_name, fw.code AS from_warehouse_code, fw.name AS from_warehouse_name
  FROM inv_requisitions r
  JOIN users u ON u.tenant_id = r.tenant_id AND u.id = r.requester_id
  JOIN inv_warehouses tw ON tw.property_id = r.property_id AND tw.id = r.to_warehouse_id
  JOIN inv_warehouses fw ON fw.property_id = r.property_id AND fw.id = r.from_warehouse_id
  LEFT JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: ListRequisitions :many
SELECT r.id, r.sr_number, r.status, r.requester_id, u.full_name AS requester_name, r.department_id, d.name AS department_name,
       r.to_warehouse_id, tw.name AS to_warehouse_name, r.from_warehouse_id, fw.name AS from_warehouse_name, r.needed_on, r.est_total, r.cycle, r.submitted_at, r.created_at,
       (SELECT count(*) FROM inv_requisition_lines l WHERE l.requisition_id = r.id)::int AS line_count,
       COALESCE((SELECT string_agg(t.trf_number, ', ' ORDER BY t.id) FROM inv_transfers t WHERE t.property_id = r.property_id AND t.requisition_id = r.id AND t.status = 'POSTED'), '')::text AS answered_by
  FROM inv_requisitions r
  JOIN users u ON u.tenant_id = r.tenant_id AND u.id = r.requester_id
  JOIN inv_warehouses tw ON tw.property_id = r.property_id AND tw.id = r.to_warehouse_id
  JOIN inv_warehouses fw ON fw.property_id = r.property_id AND fw.id = r.from_warehouse_id
  LEFT JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
   AND (sqlc.narg(requester_id)::bigint IS NULL OR r.requester_id = sqlc.narg(requester_id)::bigint)
   AND (sqlc.narg(q)::text IS NULL OR r.sr_number ILIKE '%' || sqlc.narg(q)::text || '%')
 ORDER BY r.id DESC
 LIMIT @row_limit;

-- name: UpdateRequisitionDraft :one
UPDATE inv_requisitions
   SET to_warehouse_id = @to_warehouse_id, from_warehouse_id = @from_warehouse_id, department_id = sqlc.narg(department_id), needed_on = sqlc.narg(needed_on), note = sqlc.narg(note),
       est_total = @est_total, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING version;

-- name: SetRequisitionState :exec
UPDATE inv_requisitions
   SET status = @status, cycle = @cycle, submitted_at = sqlc.narg(submitted_at), returned_note = sqlc.narg(returned_note), rejected_note = sqlc.narg(rejected_note), est_total = @est_total,
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteRequisitionLines :exec
DELETE FROM inv_requisition_lines WHERE property_id = @property_id AND requisition_id = @requisition_id;

-- name: InsertRequisitionLine :exec
INSERT INTO inv_requisition_lines (tenant_id, property_id, requisition_id, line_no, item_id, unit_id, qty_requested, qty_stock, note)
VALUES (@tenant_id, @property_id, @requisition_id, @line_no, @item_id, @unit_id, @qty_requested, @qty_stock, sqlc.narg(note));

-- The lines with what the store (from) holds of each item now and the cost it would ship at (the average of its balance).
-- name: ListRequisitionLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, l.unit_id, u.code AS unit_code, l.qty_requested, l.qty_stock, l.qty_issued_stock, l.note,
       su.code AS stock_unit_code, iu.stock_num, iu.stock_den,
       COALESCE(b.qty, 0)::numeric(18,6) AS on_hand, COALESCE(CASE WHEN b.qty > 0 THEN b.value / b.qty END, b.last_unit_cost, i.reference_cost, 0)::numeric(18,6) AS avg_cost
  FROM inv_requisition_lines l
  JOIN inv_requisitions r ON r.property_id = l.property_id AND r.id = l.requisition_id
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_units u ON u.property_id = l.property_id AND u.id = l.unit_id
  JOIN inv_item_units iu ON iu.item_id = l.item_id AND iu.unit_id = l.unit_id
  LEFT JOIN inv_balances b ON b.property_id = l.property_id AND b.warehouse_id = r.from_warehouse_id AND b.item_id = l.item_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.requisition_id = @requisition_id
 ORDER BY l.line_no;

-- name: UpdateRequisitionLineEdit :exec
UPDATE inv_requisition_lines SET qty_requested = @qty_requested, qty_stock = @qty_stock
 WHERE property_id = @property_id AND requisition_id = @requisition_id AND line_no = @line_no;

-- name: AddRequisitionLineIssued :exec
UPDATE inv_requisition_lines SET qty_issued_stock = qty_issued_stock + @delta
 WHERE property_id = @property_id AND requisition_id = @requisition_id AND line_no = @line_no;

-- ---------------------------------------------------------------------------------------------------------------------
-- The chain of a requisition: steps, edits, notices, inbox (the queries of the steps of a request are in queries_purchasing.sql)

-- name: InsertSRStep :one
INSERT INTO inv_approval_steps (tenant_id, property_id, requisition_id, cycle, level_no, name, approver_kind, permission, condition_kind, threshold, edit_allowed, status, skip_reason, activated_at)
VALUES (@tenant_id, @property_id, @requisition_id, @cycle, @level_no, @name, @approver_kind, sqlc.narg(permission), @condition_kind, sqlc.narg(threshold), @edit_allowed, @status,
        sqlc.narg(skip_reason), sqlc.narg(activated_at))
RETURNING id;

-- name: ListSRSteps :many
SELECT s.*, u.full_name AS decided_by_name
  FROM inv_approval_steps s LEFT JOIN users u ON u.tenant_id = s.tenant_id AND u.id = s.decided_by
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.requisition_id = ANY(@requisition_ids::bigint[])
 ORDER BY s.requisition_id, s.cycle, s.level_no;

-- name: ListSRStepsOfCycle :many
SELECT * FROM inv_approval_steps WHERE tenant_id = @tenant_id AND property_id = @property_id AND requisition_id = @requisition_id AND cycle = @cycle ORDER BY level_no;

-- name: ListSREdits :many
SELECT e.id, e.step_id, s.level_no, s.cycle, e.line_no, e.field, e.old_value, e.new_value, e.edited_by, u.full_name AS edited_by_name, e.edited_at
  FROM inv_approval_edits e
  JOIN inv_approval_steps s ON s.property_id = e.property_id AND s.id = e.step_id
  JOIN users u ON u.id = e.edited_by
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND s.requisition_id = @requisition_id
 ORDER BY e.id;

-- name: InsertSRNotice :exec
INSERT INTO inv_notices (tenant_id, property_id, user_id, kind, step_id, requisition_id, created_at)
VALUES (@tenant_id, @property_id, @user_id, @kind, sqlc.narg(step_id), sqlc.narg(requisition_id), @created_at);

-- The requisitions whose ACTIVE step the user can decide now (the same rules as ApprovalInbox).
-- name: RequisitionInbox :many
SELECT r.id AS requisition_id, r.sr_number, r.est_total, r.department_id, d.name AS department_name, r.requester_id, u.full_name AS requester_name, r.submitted_at,
       s.id AS step_id, s.level_no, s.name AS step_name
  FROM inv_approval_steps s
  JOIN inv_requisitions r ON r.property_id = s.property_id AND r.id = s.requisition_id AND r.status = 'SUBMITTED' AND r.cycle = s.cycle
  JOIN users u ON u.tenant_id = r.tenant_id AND u.id = r.requester_id
  LEFT JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
  LEFT JOIN departments pd ON pd.property_id = d.property_id AND pd.id = d.parent_id
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'ACTIVE' AND r.requester_id <> @user_id
   AND NOT EXISTS (SELECT 1 FROM inv_approval_steps x WHERE x.requisition_id = s.requisition_id AND x.cycle = s.cycle AND x.status = 'APPROVED' AND x.decided_by = @user_id)
   AND (
        (s.approver_kind = 'PERMISSION' AND (@is_admin::boolean OR EXISTS (
            SELECT 1 FROM user_properties up JOIN role_permissions rp ON rp.role_id = up.role_id
             WHERE up.tenant_id = @tenant_id AND up.user_id = @user_id AND up.property_id = @property_id AND rp.permission_code = s.permission)))
        OR
        (s.approver_kind = 'DEPT_HEAD'
         AND @user_id IN (CASE WHEN d.head_user_id IS NOT NULL THEN d.head_user_id ELSE pd.head_user_id END, CASE WHEN d.head_user_id IS NOT NULL THEN d.deputy_user_id ELSE pd.deputy_user_id END)
         AND (@is_admin::boolean OR EXISTS (
            SELECT 1 FROM user_properties up JOIN role_permissions rp ON rp.role_id = up.role_id
             WHERE up.tenant_id = @tenant_id AND up.user_id = @user_id AND up.property_id = @property_id AND rp.permission_code = 'inventory.dept_approve')))
       )
 ORDER BY r.submitted_at, r.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The transfer

-- name: InsertTransfer :one
INSERT INTO inv_transfers (tenant_id, property_id, trf_number, client_uuid, requisition_id, from_warehouse_id, to_warehouse_id, business_date, note, created_by)
VALUES (@tenant_id, @property_id, @trf_number, @client_uuid, sqlc.narg(requisition_id), @from_warehouse_id, @to_warehouse_id, @business_date, sqlc.narg(note), sqlc.narg(actor_id))
RETURNING id;

-- name: GetTransferByClientUUID :one
SELECT id FROM inv_transfers WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetTransfer :one
SELECT t.*, fw.code AS from_warehouse_code, fw.name AS from_warehouse_name, tw.code AS to_warehouse_code, tw.name AS to_warehouse_name, r.sr_number AS requisition_number,
       u.full_name AS created_by_name,
       (SELECT COALESCE(sum(l.value), 0) FROM inv_transfer_lines l WHERE l.transfer_id = t.id)::numeric(18,3) AS total_value
  FROM inv_transfers t
  JOIN inv_warehouses fw ON fw.property_id = t.property_id AND fw.id = t.from_warehouse_id
  JOIN inv_warehouses tw ON tw.property_id = t.property_id AND tw.id = t.to_warehouse_id
  LEFT JOIN inv_requisitions r ON r.property_id = t.property_id AND r.id = t.requisition_id
  LEFT JOIN users u ON u.id = t.created_by
 WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.id = @id;

-- name: ListTransfers :many
SELECT t.id, t.trf_number, t.status, t.requisition_id, r.sr_number AS requisition_number, t.from_warehouse_id, fw.name AS from_warehouse_name, t.to_warehouse_id, tw.name AS to_warehouse_name,
       t.business_date, t.created_at,
       (SELECT count(*) FROM inv_transfer_lines l WHERE l.transfer_id = t.id)::int AS line_count,
       (SELECT COALESCE(sum(l.value), 0) FROM inv_transfer_lines l WHERE l.transfer_id = t.id)::numeric(18,3) AS total_value
  FROM inv_transfers t
  JOIN inv_warehouses fw ON fw.property_id = t.property_id AND fw.id = t.from_warehouse_id
  JOIN inv_warehouses tw ON tw.property_id = t.property_id AND tw.id = t.to_warehouse_id
  LEFT JOIN inv_requisitions r ON r.property_id = t.property_id AND r.id = t.requisition_id
 WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR t.status = sqlc.narg(status)::text)
   AND (sqlc.narg(requisition_id)::bigint IS NULL OR t.requisition_id = sqlc.narg(requisition_id)::bigint)
   AND (sqlc.narg(free_only)::boolean IS NULL OR NOT sqlc.narg(free_only)::boolean OR t.requisition_id IS NULL)
   AND (sqlc.narg(q)::text IS NULL OR t.trf_number ILIKE '%' || sqlc.narg(q)::text || '%')
 ORDER BY t.id DESC
 LIMIT @row_limit;

-- name: InsertTransferLine :exec
INSERT INTO inv_transfer_lines (tenant_id, property_id, transfer_id, line_no, item_id, requisition_line_no, qty_stock, value, out_movement_id, in_movement_id)
VALUES (@tenant_id, @property_id, @transfer_id, @line_no, @item_id, sqlc.narg(requisition_line_no), @qty_stock, @value, @out_movement_id, @in_movement_id);

-- name: ListTransferLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, su.code AS stock_unit_code, l.requisition_line_no, l.qty_stock, l.value, l.out_movement_id, l.in_movement_id
  FROM inv_transfer_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.transfer_id = @transfer_id
 ORDER BY l.line_no;

-- name: SetTransferReversed :exec
UPDATE inv_transfers SET status = 'REVERSED', reverse_reason = @reason, reversed_on = @reversed_on WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The transfers that answered a requisition, newest first.
-- name: ListTransfersOfRequisition :many
SELECT t.id, t.trf_number, t.status, t.business_date,
       (SELECT COALESCE(sum(l.value), 0) FROM inv_transfer_lines l WHERE l.transfer_id = t.id)::numeric(18,3) AS total_value
  FROM inv_transfers t WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.requisition_id = @requisition_id ORDER BY t.id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The issue

-- name: InsertIssue :one
INSERT INTO inv_issues (tenant_id, property_id, iss_number, client_uuid, warehouse_id, purpose, origin, department_id, outlet_id, reason_code, covers, beneficiary, reverses_issue_id,
                        business_date, source_date, note, reverse_reason, created_at, created_by)
VALUES (@tenant_id, @property_id, @iss_number, @client_uuid, @warehouse_id, @purpose, @origin, sqlc.narg(department_id), sqlc.narg(outlet_id), sqlc.narg(reason_code), sqlc.narg(covers),
        sqlc.narg(beneficiary), sqlc.narg(reverses_issue_id), @business_date, sqlc.narg(source_date), sqlc.narg(note), sqlc.narg(reverse_reason), @created_at, sqlc.narg(actor_id))
RETURNING id;

-- name: GetIssueByClientUUID :one
SELECT id FROM inv_issues WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetIssue :one
SELECT i.*, w.code AS warehouse_code, w.name AS warehouse_name, d.code AS department_code, d.name AS department_name, u.full_name AS created_by_name,
       rv.iss_number AS reverses_number, COALESCE((SELECT x.iss_number FROM inv_issues x WHERE x.property_id = i.property_id AND x.reverses_issue_id = i.id), '')::text AS reversed_by_number,
       (SELECT COALESCE(sum(l.value), 0) FROM inv_issue_lines l WHERE l.issue_id = i.id)::numeric(18,3) AS total_value,
       (SELECT count(*) FROM inv_issue_lines l WHERE l.issue_id = i.id AND l.status = 'HELD')::int AS held_lines
  FROM inv_issues i
  JOIN inv_warehouses w ON w.property_id = i.property_id AND w.id = i.warehouse_id
  LEFT JOIN departments d ON d.property_id = i.property_id AND d.id = i.department_id
  LEFT JOIN users u ON u.id = i.created_by
  LEFT JOIN inv_issues rv ON rv.property_id = i.property_id AND rv.id = i.reverses_issue_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = @id;

-- name: ListIssues :many
SELECT i.id, i.iss_number, i.purpose, i.origin, i.status, i.warehouse_id, w.name AS warehouse_name, i.department_id, d.name AS department_name, i.reason_code, i.covers, i.beneficiary,
       i.business_date, i.source_date, i.created_at, i.reverses_issue_id,
       (SELECT count(*) FROM inv_issue_lines l WHERE l.issue_id = i.id)::int AS line_count,
       (SELECT count(*) FROM inv_issue_lines l WHERE l.issue_id = i.id AND l.status = 'HELD')::int AS held_lines,
       (SELECT COALESCE(sum(l.value), 0) FROM inv_issue_lines l WHERE l.issue_id = i.id)::numeric(18,3) AS total_value
  FROM inv_issues i
  JOIN inv_warehouses w ON w.property_id = i.property_id AND w.id = i.warehouse_id
  LEFT JOIN departments d ON d.property_id = i.property_id AND d.id = i.department_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id
   AND (sqlc.narg(purpose)::text IS NULL OR i.purpose = sqlc.narg(purpose)::text)
   AND (sqlc.narg(origin)::text IS NULL OR i.origin = sqlc.narg(origin)::text)
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR i.warehouse_id = sqlc.narg(warehouse_id)::bigint)
   AND (sqlc.narg(department_id)::bigint IS NULL OR i.department_id = sqlc.narg(department_id)::bigint)
   AND (sqlc.narg(from_date)::date IS NULL OR i.business_date >= sqlc.narg(from_date)::date)
   AND (sqlc.narg(to_date)::date IS NULL OR i.business_date <= sqlc.narg(to_date)::date)
   AND (sqlc.narg(q)::text IS NULL OR i.iss_number ILIKE '%' || sqlc.narg(q)::text || '%')
 ORDER BY i.id DESC
 LIMIT @row_limit;

-- The cost of the issues by department for a period: the card "cost per department this month" of the list (mockup 24).
-- name: IssueCostByDepartment :many
SELECT COALESCE(d.name, '')::text AS department_name, i.purpose, COALESCE(sum(l.value), 0)::numeric(18,3) AS value
  FROM inv_issues i
  JOIN inv_issue_lines l ON l.issue_id = i.id AND l.status = 'APPLIED'
  LEFT JOIN departments d ON d.property_id = i.property_id AND d.id = i.department_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date::date AND @to_date::date AND i.reverses_issue_id IS NULL AND i.status = 'POSTED'
 GROUP BY d.name, i.purpose
 ORDER BY 3 DESC;

-- name: InsertIssueLine :exec
INSERT INTO inv_issue_lines (tenant_id, property_id, issue_id, line_no, item_id, qty_stock, value, expense_account_id, department_id, movement_id, status, applied_on, applied_at)
VALUES (@tenant_id, @property_id, @issue_id, @line_no, @item_id, @qty_stock, sqlc.narg(value), sqlc.narg(expense_account_id), sqlc.narg(department_id), sqlc.narg(movement_id), @status,
        sqlc.narg(applied_on), sqlc.narg(applied_at));

-- name: ListIssueLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, su.code AS stock_unit_code, l.qty_stock, l.value, l.expense_account_id, a.code AS expense_account_code, a.name AS expense_account_name,
       l.department_id, d.name AS department_name, l.movement_id, l.status, l.applied_on, l.applied_at
  FROM inv_issue_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  LEFT JOIN gl_accounts a ON a.property_id = l.property_id AND a.id = l.expense_account_id
  LEFT JOIN departments d ON d.property_id = l.property_id AND d.id = l.department_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.issue_id = @issue_id
 ORDER BY l.line_no;

-- name: SetIssueReversed :exec
UPDATE inv_issues SET status = 'REVERSED', reverse_reason = @reason WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetIssueLineApplied :exec
UPDATE inv_issue_lines SET status = 'APPLIED', movement_id = @movement_id, value = @value, applied_on = @applied_on, applied_at = @applied_at
 WHERE property_id = @property_id AND issue_id = @issue_id AND line_no = @line_no AND status = 'HELD';

-- The held lines of a warehouse (the amenities documents of closes that fell inside a count, 5.9 D24), in the order of the documents, with when the document was made: its created_at decides the side of the count.
-- name: ListHeldIssueLines :many
SELECT l.issue_id, l.line_no, l.item_id, l.qty_stock, l.expense_account_id, l.department_id, i.iss_number, i.created_at, i.source_date, i.warehouse_id, i.purpose, i.origin
  FROM inv_issue_lines l JOIN inv_issues i ON i.property_id = l.property_id AND i.id = l.issue_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND i.warehouse_id = @warehouse_id AND l.status = 'HELD'
 ORDER BY i.business_date, i.id, l.line_no;

-- name: CountHeldLinesOfIssue :one
SELECT count(*)::int FROM inv_issue_lines l WHERE l.property_id = @property_id AND l.issue_id = @issue_id AND l.status = 'HELD';

-- name: ListAppliedIssueLinesForReverse :many
SELECT l.line_no, l.item_id, l.movement_id, l.qty_stock FROM inv_issue_lines l
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.issue_id = @issue_id AND l.status = 'APPLIED' AND l.movement_id IS NOT NULL ORDER BY l.line_no;

-- ---------------------------------------------------------------------------------------------------------------------
-- The count and the freeze

-- name: InsertCount :one
INSERT INTO inv_counts (tenant_id, property_id, cnt_number, client_uuid, warehouse_id, scope, category_id, started_at, started_by, business_date_started, note)
VALUES (@tenant_id, @property_id, @cnt_number, @client_uuid, @warehouse_id, @scope, sqlc.narg(category_id), @started_at, @started_by, @business_date_started, sqlc.narg(note))
RETURNING id;

-- name: GetCountByClientUUID :one
SELECT id FROM inv_counts WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetCount :one
SELECT c.*, w.code AS warehouse_code, w.name AS warehouse_name, cat.name AS category_name, us.full_name AS started_by_name, ub.full_name AS submitted_by_name, ud.full_name AS decided_by_name
  FROM inv_counts c
  JOIN inv_warehouses w ON w.property_id = c.property_id AND w.id = c.warehouse_id
  LEFT JOIN inv_categories cat ON cat.property_id = c.property_id AND cat.id = c.category_id
  LEFT JOIN users us ON us.id = c.started_by
  LEFT JOIN users ub ON ub.id = c.submitted_by
  LEFT JOIN users ud ON ud.id = c.decided_by
 WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.id = @id;

-- name: ListCounts :many
SELECT c.id, c.cnt_number, c.status, c.warehouse_id, w.name AS warehouse_name, c.scope, c.started_at, c.business_date_started, c.submitted_at, c.decided_at, c.total_variance_value,
       us.full_name AS started_by_name,
       (SELECT count(*) FROM inv_count_lines l WHERE l.count_id = c.id)::int AS line_count,
       (SELECT count(*) FROM inv_count_lines l WHERE l.count_id = c.id AND l.counted_qty IS NOT NULL)::int AS counted_count
  FROM inv_counts c
  JOIN inv_warehouses w ON w.property_id = c.property_id AND w.id = c.warehouse_id
  LEFT JOIN users us ON us.id = c.started_by
 WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR c.status = sqlc.narg(status)::text)
   AND (sqlc.narg(warehouse_id)::bigint IS NULL OR c.warehouse_id = sqlc.narg(warehouse_id)::bigint)
 ORDER BY c.id DESC
 LIMIT @row_limit;

-- The count that freezes a warehouse (for the banner and for the night audit).
-- name: ListOpenCounts :many
SELECT c.id, c.cnt_number, c.status, c.warehouse_id, w.name AS warehouse_name, c.started_at, c.business_date_started
  FROM inv_counts c JOIN inv_warehouses w ON w.property_id = c.property_id AND w.id = c.warehouse_id
 WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.status IN ('OPEN', 'SUBMITTED') ORDER BY c.id;

-- name: SetCountState :exec
UPDATE inv_counts SET status = @status, submitted_at = sqlc.narg(submitted_at), submitted_by = sqlc.narg(submitted_by), decided_by = sqlc.narg(decided_by), decided_at = sqlc.narg(decided_at),
       total_variance_value = sqlc.narg(total_variance_value), version = version + 1
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The freeze is one column of the warehouse (5.5): the count that holds it, or NULL.
-- name: SetWarehouseCount :exec
UPDATE inv_warehouses SET count_id = sqlc.narg(count_id) WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- The items in the scope of a count with the book quantity now (taken at the freeze, under the balance locks).
-- name: ListCountScope :many
SELECT i.id AS item_id, COALESCE(b.qty, 0)::numeric(18,6) AS book_qty
  FROM inv_items i
  LEFT JOIN inv_balances b ON b.property_id = i.property_id AND b.warehouse_id = @warehouse_id AND b.item_id = i.id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.is_active
   AND (sqlc.narg(category_id)::bigint IS NULL OR i.category_id = sqlc.narg(category_id)::bigint)
   AND (sqlc.narg(item_ids)::bigint[] IS NULL OR i.id = ANY(sqlc.narg(item_ids)::bigint[]))
   AND (COALESCE(b.qty, 0) <> 0 OR sqlc.narg(item_ids)::bigint[] IS NOT NULL OR EXISTS (SELECT 1 FROM inv_levels l WHERE l.property_id = i.property_id AND l.warehouse_id = @warehouse_id AND l.item_id = i.id))
 ORDER BY i.id;

-- name: InsertCountLine :exec
INSERT INTO inv_count_lines (tenant_id, property_id, count_id, item_id, book_qty) VALUES (@tenant_id, @property_id, @count_id, @item_id, @book_qty);

-- name: ListCountLines :many
SELECT l.item_id, i.code AS item_code, i.name AS item_name, c.name AS category_name, su.code AS stock_unit_code, l.book_qty, l.counted_qty, l.counted_at, l.entered_unit_id, l.entered_qty,
       l.expected_qty, l.held_before_qty, l.variance_qty, l.variance_value, l.counted_by, l.note
  FROM inv_count_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.count_id = @count_id
 ORDER BY c.sort_order, i.name, i.id;

-- name: SetCountLineCounted :exec
UPDATE inv_count_lines SET counted_qty = sqlc.narg(counted_qty), counted_at = sqlc.narg(counted_at), entered_unit_id = sqlc.narg(entered_unit_id), entered_qty = sqlc.narg(entered_qty),
       counted_by = sqlc.narg(counted_by), note = sqlc.narg(note)
 WHERE property_id = @property_id AND count_id = @count_id AND item_id = @item_id;

-- name: SetCountLineVariance :exec
UPDATE inv_count_lines SET expected_qty = @expected_qty, held_before_qty = @held_before_qty, variance_qty = @variance_qty, variance_value = @variance_value
 WHERE property_id = @property_id AND count_id = @count_id AND item_id = @item_id;

-- name: CountLineExists :one
SELECT EXISTS (SELECT 1 FROM inv_count_lines l WHERE l.property_id = @property_id AND l.count_id = @count_id AND l.item_id = @item_id)::boolean;

-- ---------------------------------------------------------------------------------------------------------------------
-- The amenities

-- name: InsertAmenityPlan :one
INSERT INTO inv_amenity_plans (tenant_id, property_id, version_no, effective_from, is_enabled, warehouse_id, department_id, expense_account_id, created_by)
VALUES (@tenant_id, @property_id, @version_no, @effective_from, @is_enabled, sqlc.narg(warehouse_id), sqlc.narg(department_id), sqlc.narg(expense_account_id), sqlc.narg(actor_id))
RETURNING id;

-- name: InsertAmenityPlanLine :exec
INSERT INTO inv_amenity_plan_lines (tenant_id, property_id, plan_id, line_no, room_type_id, item_id, unit_id, qty, basis)
VALUES (@tenant_id, @property_id, @plan_id, @line_no, sqlc.narg(room_type_id), @item_id, @unit_id, @qty, @basis);

-- name: MaxAmenityPlanVersion :one
SELECT COALESCE(max(version_no), 0)::int FROM inv_amenity_plans WHERE property_id = @property_id;

-- The plan in force on a business day: the newest version that is effective by then.
-- name: GetAmenityPlanInForce :one
SELECT p.*, w.code AS warehouse_code, w.name AS warehouse_name, d.name AS department_name, a.code AS account_code, a.name AS account_name
  FROM inv_amenity_plans p
  LEFT JOIN inv_warehouses w ON w.property_id = p.property_id AND w.id = p.warehouse_id
  LEFT JOIN departments d ON d.property_id = p.property_id AND d.id = p.department_id
  LEFT JOIN gl_accounts a ON a.property_id = p.property_id AND a.id = p.expense_account_id
 WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.effective_from <= @as_of::date
 ORDER BY p.version_no DESC LIMIT 1;

-- name: GetAmenityPlan :one
SELECT p.*, w.code AS warehouse_code, w.name AS warehouse_name, d.name AS department_name, a.code AS account_code, a.name AS account_name
  FROM inv_amenity_plans p
  LEFT JOIN inv_warehouses w ON w.property_id = p.property_id AND w.id = p.warehouse_id
  LEFT JOIN departments d ON d.property_id = p.property_id AND d.id = p.department_id
  LEFT JOIN gl_accounts a ON a.property_id = p.property_id AND a.id = p.expense_account_id
 WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.id = @id;

-- name: ListAmenityPlanLines :many
SELECT l.line_no, l.room_type_id, rt.code AS room_type_code, rt.name AS room_type_name, l.item_id, i.code AS item_code, i.name AS item_name, l.unit_id, u.code AS unit_code, l.qty, l.basis,
       iu.stock_num, iu.stock_den, su.code AS stock_unit_code, i.is_active AS item_active
  FROM inv_amenity_plan_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units u ON u.property_id = l.property_id AND u.id = l.unit_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_item_units iu ON iu.item_id = l.item_id AND iu.unit_id = l.unit_id
  LEFT JOIN room_types rt ON rt.property_id = l.property_id AND rt.id = l.room_type_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.plan_id = @plan_id
 ORDER BY l.line_no;

-- name: ListAmenityPlans :many
SELECT p.id, p.version_no, p.effective_from, p.is_enabled, p.created_at, u.full_name AS created_by_name,
       (SELECT count(*) FROM inv_amenity_plan_lines l WHERE l.plan_id = p.id)::int AS line_count
  FROM inv_amenity_plans p LEFT JOIN users u ON u.id = p.created_by
 WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id ORDER BY p.version_no DESC LIMIT 50;

-- name: InsertAmenityRun :one
INSERT INTO inv_amenity_runs (tenant_id, property_id, business_date, plan_id, issue_id, rooms, guests, comp_rooms, house_use_rooms, total_value, created_at)
VALUES (@tenant_id, @property_id, @business_date, @plan_id, @issue_id, @rooms, @guests, @comp_rooms, @house_use_rooms, @total_value, @created_at)
ON CONFLICT (property_id, business_date) DO NOTHING
RETURNING id;

-- name: InsertAmenityRunRoom :exec
INSERT INTO inv_amenity_run_rooms (tenant_id, property_id, run_id, room_id, room_type_id, stay_id, guests, occupancy_kind) VALUES (@tenant_id, @property_id, @run_id, @room_id, @room_type_id, @stay_id, @guests, @occupancy_kind);

-- name: InsertAmenityRunLine :exec
INSERT INTO inv_amenity_run_lines (tenant_id, property_id, run_id, line_no, plan_id, plan_line_no, item_id, room_type_id, basis, qty_per, multiplier, qty_stock, stock_before, issue_line_no)
VALUES (@tenant_id, @property_id, @run_id, @line_no, @plan_id, @plan_line_no, @item_id, sqlc.narg(room_type_id), @basis, @qty_per, @multiplier, @qty_stock, sqlc.narg(stock_before), @issue_line_no);

-- name: GetAmenityRunByDate :one
SELECT r.*, i.iss_number, p.version_no AS plan_version, ri.iss_number AS reversal_number
  FROM inv_amenity_runs r
  JOIN inv_issues i ON i.property_id = r.property_id AND i.id = r.issue_id
  JOIN inv_amenity_plans p ON p.property_id = r.property_id AND p.id = r.plan_id
  LEFT JOIN inv_issues ri ON ri.property_id = r.property_id AND ri.id = r.reversal_issue_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.business_date = @business_date;

-- name: GetAmenityRunByIssue :one
SELECT r.id, r.business_date, r.status FROM inv_amenity_runs r WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.issue_id = @issue_id;

-- name: ListAmenityRuns :many
SELECT r.id, r.business_date, r.rooms, r.guests, r.comp_rooms, r.house_use_rooms, r.total_value, r.status, i.iss_number, p.version_no AS plan_version,
       (SELECT count(*) FROM inv_issue_lines l WHERE l.issue_id = r.issue_id AND l.status = 'HELD')::int AS held_lines
  FROM inv_amenity_runs r
  JOIN inv_issues i ON i.property_id = r.property_id AND i.id = r.issue_id
  JOIN inv_amenity_plans p ON p.property_id = r.property_id AND p.id = r.plan_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
   AND (sqlc.narg(from_date)::date IS NULL OR r.business_date >= sqlc.narg(from_date)::date)
   AND (sqlc.narg(to_date)::date IS NULL OR r.business_date <= sqlc.narg(to_date)::date)
 ORDER BY r.business_date DESC LIMIT @row_limit;

-- name: ListAmenityRunRooms :many
SELECT rr.room_id, rm.room_number, rr.room_type_id, rt.code AS room_type_code, rt.name AS room_type_name, rr.stay_id, rr.guests, rr.occupancy_kind
  FROM inv_amenity_run_rooms rr
  JOIN rooms rm ON rm.property_id = rr.property_id AND rm.id = rr.room_id
  JOIN room_types rt ON rt.property_id = rr.property_id AND rt.id = rr.room_type_id
 WHERE rr.tenant_id = @tenant_id AND rr.property_id = @property_id AND rr.run_id = @run_id
 ORDER BY rm.room_number;

-- name: ListAmenityRunLines :many
SELECT l.line_no, l.plan_line_no, l.item_id, i.code AS item_code, i.name AS item_name, su.code AS stock_unit_code, l.room_type_id, rt.name AS room_type_name, l.basis, l.qty_per, l.multiplier,
       l.qty_stock, l.stock_before, l.issue_line_no, il.value, il.status, il.applied_on
  FROM inv_amenity_run_lines l
  JOIN inv_amenity_runs r ON r.property_id = l.property_id AND r.id = l.run_id
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_issue_lines il ON il.property_id = l.property_id AND il.issue_id = r.issue_id AND il.item_id = l.item_id
  LEFT JOIN room_types rt ON rt.property_id = l.property_id AND rt.id = l.room_type_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.run_id = @run_id
 ORDER BY l.line_no;

-- name: SetAmenityRunReversed :exec
UPDATE inv_amenity_runs SET status = 'REVERSED', reversal_issue_id = @reversal_issue_id WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetAmenityRunTotal :exec
UPDATE inv_amenity_runs SET total_value = @total_value WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------------
-- What the ledger writer needs for the movements of PR 3 (the queries that write the ledger tables are named Ledger*)

-- All the layers a movement made (a transfer in under FIFO makes one for each layer the transfer out took).
-- name: LedgerListLayersOfMovement :many
SELECT l.id, l.received_on, l.qty_in, l.qty_remaining, l.value_remaining, l.movement_id
  FROM inv_layers l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.movement_id = @movement_id ORDER BY l.id;

-- The layers of one item in a warehouse that the count must replace: the open layers (a count sets the quantity, 4.7).
-- name: LedgerLayerRemainingSum :one
SELECT COALESCE(sum(l.qty_remaining), 0)::numeric(18,6) AS qty, COALESCE(sum(l.value_remaining), 0)::numeric(18,3) AS value
  FROM inv_layers l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.warehouse_id = @warehouse_id AND l.item_id = @item_id AND l.qty_remaining > 0;

-- name: LedgerOpenShortfallQty :one
SELECT COALESCE(sum(s.qty_open), 0)::numeric(18,6) FROM inv_shortfalls s
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.warehouse_id = @warehouse_id AND s.item_id = @item_id AND s.status = 'OPEN';

-- ---------------------------------------------------------------------------------------------------------------------
-- The documents of the stock a check needs

-- The held lines of amenities (and, from PR 4, of the POS queue) that wait for a warehouse: for the warning AMENITY_HELD and the document screen.
-- name: ListHeldAmenityDocs :many
SELECT i.id, i.iss_number, i.source_date, count(*)::int AS held_lines
  FROM inv_issues i JOIN inv_issue_lines l ON l.property_id = i.property_id AND l.issue_id = i.id AND l.status = 'HELD'
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.warehouse_id = @warehouse_id
 GROUP BY i.id, i.iss_number, i.source_date ORDER BY i.source_date, i.id;

-- The cost per room type and per guest of the rooms of the night is read by the amenities preview from the balances; this is the average cost of an item in a warehouse now.
-- name: ItemAverageCosts :many
SELECT b.item_id, b.qty, b.value, b.last_unit_cost, i.reference_cost
  FROM inv_balances b JOIN inv_items i ON i.property_id = b.property_id AND i.id = b.item_id
 WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.warehouse_id = @warehouse_id AND b.item_id = ANY(@item_ids::bigint[]);

-- name: ListWarehouseCountsByID :many
SELECT w.id, w.count_id, c.cnt_number FROM inv_warehouses w LEFT JOIN inv_counts c ON c.property_id = w.property_id AND c.id = w.count_id
 WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.id = ANY(@ids::bigint[]);

-- A document number of the amenities of a day: its issue, whatever the state.
-- name: GetIssueByNumber :one
SELECT id FROM inv_issues WHERE tenant_id = @tenant_id AND property_id = @property_id AND iss_number = @iss_number;

-- name: ListRoomTypesLite :many
SELECT rt.id, rt.code, rt.name FROM room_types rt WHERE rt.tenant_id = @tenant_id AND rt.property_id = @property_id ORDER BY rt.sort_order, rt.code;

-- ---------------------------------------------------------------------------------------------------------------------
-- Accounts and departments of the movements

-- The accounts the category of each item names: the issue, the waste and the count variance, and the inventory account.
-- name: ItemCategoryAccounts :many
SELECT i.id AS item_id, i.code AS item_code, i.name AS item_name, i.is_active, c.issue_account_id, c.waste_account_id, c.variance_account_id, c.inventory_account_id, c.kind AS category_kind,
       su.code AS stock_unit_code
  FROM inv_items i JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = ANY(@item_ids::bigint[]);

-- The department rule of an account (document 12): what a line of the account does with the department.
-- name: AccountRule :one
SELECT a.id, a.code, a.name, a.account_type, a.is_active, a.is_postable, a.department_requirement, a.default_department_id, COALESCE(d.is_active, false)::boolean AS default_department_active
  FROM gl_accounts a LEFT JOIN departments d ON d.property_id = a.property_id AND d.id = a.default_department_id
 WHERE a.tenant_id = @tenant_id AND a.property_id = @property_id AND a.id = @id;

-- The department a warehouse stands for: its own, else its outlet's.
-- name: WarehouseDepartment :one
SELECT w.id, w.kind, w.outlet_id, COALESCE(w.department_id, o.department_id, 0)::bigint AS department_id
  FROM inv_warehouses w LEFT JOIN pos_outlets o ON o.property_id = w.property_id AND o.id = w.outlet_id
 WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.id = @id;

-- The journal an issue makes: its movements grouped by the accounts they carry (the amenities document shows it).
-- name: IssueJournal :many
SELECT da.code AS debit_code, da.name AS debit_name, ca.code AS credit_code, ca.name AS credit_name, d.name AS department_name, sum(m.gl_amount)::numeric(18,3) AS amount
  FROM inv_movements m
  JOIN gl_accounts da ON da.property_id = m.property_id AND da.id = m.gl_debit_account_id
  JOIN gl_accounts ca ON ca.property_id = m.property_id AND ca.id = m.gl_credit_account_id
  LEFT JOIN departments d ON d.property_id = m.property_id AND d.id = m.gl_department_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.source_type = 'INV_ISSUE' AND m.source_id = @issue_id AND m.gl_amount > 0
 GROUP BY da.code, da.name, ca.code, ca.name, d.name
 ORDER BY da.code, ca.code;

-- name: SumIssueLineValues :one
SELECT COALESCE(sum(l.value), 0)::numeric(18,3) FROM inv_issue_lines l WHERE l.property_id = @property_id AND l.issue_id = @issue_id;

-- name: GetWarehouseLite :one
SELECT w.id, w.code, w.name, w.is_active, w.count_id, c.cnt_number
  FROM inv_warehouses w LEFT JOIN inv_counts c ON c.property_id = w.property_id AND c.id = w.count_id
 WHERE w.tenant_id = @tenant_id AND w.property_id = @property_id AND w.id = @id;
