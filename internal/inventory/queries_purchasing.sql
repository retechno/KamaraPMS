-- Inventory, PR 2 (docs/architecture/35-inventory.md 3.3, 5.3, 5.8): the approval chain, the purchase request, the purchase order, the goods receipt, the draft bill, the three-way match and the return.
-- Every query is scoped by tenant_id and property_id. The ledger tables are still written only by ledger.go; nothing here touches them.

-- ---------------------------------------------------------------------------------------------------------------------
-- The people

-- name: UserCanAt :one
SELECT (u.is_active AND (u.is_tenant_admin OR EXISTS (
        SELECT 1 FROM user_properties up JOIN role_permissions rp ON rp.role_id = up.role_id
         WHERE up.tenant_id = u.tenant_id AND up.user_id = u.id AND up.property_id = @property_id AND rp.permission_code = @permission)))::boolean AS ok
  FROM users u WHERE u.tenant_id = @tenant_id AND u.id = @user_id;

-- Active users who hold a permission at the property (a tenant administrator holds them all).
-- name: ListUsersWithPermission :many
SELECT u.id, u.full_name FROM users u
 WHERE u.tenant_id = @tenant_id AND u.is_active
   AND (u.is_tenant_admin OR EXISTS (
        SELECT 1 FROM user_properties up JOIN role_permissions rp ON rp.role_id = up.role_id
         WHERE up.tenant_id = u.tenant_id AND up.user_id = u.id AND up.property_id = @property_id AND rp.permission_code = @permission))
 ORDER BY u.id;

-- The head and the deputy of a department; a department with no head of its own takes its parent's (a lookup, never a copy).
-- name: DepartmentLeaders :one
SELECT d.id, d.code, d.name, d.head_user_id, d.deputy_user_id, p.head_user_id AS parent_head_user_id, p.deputy_user_id AS parent_deputy_user_id
  FROM departments d LEFT JOIN departments p ON p.property_id = d.property_id AND p.id = d.parent_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.id = @id;

-- name: GetUserName :one
SELECT full_name FROM users WHERE tenant_id = @tenant_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The chain: settings and levels

-- name: GetApprovalSettings :one
SELECT * FROM inv_approval_settings WHERE tenant_id = @tenant_id AND property_id = @property_id AND doc_type = @doc_type;

-- name: InsertApprovalSettings :execrows
INSERT INTO inv_approval_settings (tenant_id, property_id, doc_type, skip_level1_if_head, approver_may_edit, updated_by)
VALUES (@tenant_id, @property_id, @doc_type, @skip_level1_if_head, @approver_may_edit, sqlc.narg(actor_id))
ON CONFLICT (property_id, doc_type) DO NOTHING;

-- name: UpdateApprovalSettings :one
UPDATE inv_approval_settings SET skip_level1_if_head = @skip_level1_if_head, approver_may_edit = @approver_may_edit, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND doc_type = @doc_type AND version = @version
RETURNING *;

-- name: ListApprovalLevels :many
SELECT * FROM inv_approval_levels WHERE tenant_id = @tenant_id AND property_id = @property_id AND doc_type = @doc_type ORDER BY level_no;

-- name: InsertApprovalLevel :exec
INSERT INTO inv_approval_levels (tenant_id, property_id, doc_type, level_no, name, approver_kind, permission, condition_kind, threshold, updated_by)
VALUES (@tenant_id, @property_id, @doc_type, @level_no, @name, @approver_kind, sqlc.narg(permission), @condition_kind, sqlc.narg(threshold), sqlc.narg(actor_id));

-- name: DeleteApprovalLevels :exec
DELETE FROM inv_approval_levels WHERE tenant_id = @tenant_id AND property_id = @property_id AND doc_type = @doc_type;

-- ---------------------------------------------------------------------------------------------------------------------
-- Purchase requests

-- name: InsertRequest :one
INSERT INTO inv_requests (tenant_id, property_id, request_number, client_uuid, requester_id, department_id, warehouse_id, needed_on, note, est_total, created_by, updated_by)
VALUES (@tenant_id, @property_id, @request_number, @client_uuid, @requester_id, sqlc.narg(department_id), sqlc.narg(warehouse_id), sqlc.narg(needed_on), sqlc.narg(note), @est_total,
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: GetRequestByClientUUID :one
SELECT id FROM inv_requests WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetRequest :one
SELECT r.*, u.full_name AS requester_name, d.code AS department_code, d.name AS department_name, w.code AS warehouse_code, w.name AS warehouse_name
  FROM inv_requests r
  JOIN users u ON u.tenant_id = r.tenant_id AND u.id = r.requester_id
  LEFT JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
  LEFT JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: ListRequests :many
SELECT r.id, r.request_number, r.status, r.requester_id, u.full_name AS requester_name, r.department_id, d.code AS department_code, d.name AS department_name,
       r.warehouse_id, w.name AS warehouse_name, r.needed_on, r.est_total, r.cycle, r.submitted_at, r.created_at,
       (SELECT count(*) FROM inv_request_lines l WHERE l.request_id = r.id)::int AS line_count
  FROM inv_requests r
  JOIN users u ON u.tenant_id = r.tenant_id AND u.id = r.requester_id
  LEFT JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
  LEFT JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
   AND (sqlc.narg(requester_id)::bigint IS NULL OR r.requester_id = sqlc.narg(requester_id)::bigint)
   AND (sqlc.narg(q)::text IS NULL OR r.request_number ILIKE '%' || sqlc.narg(q)::text || '%')
 ORDER BY r.id DESC
 LIMIT @row_limit;

-- name: UpdateRequestDraft :one
UPDATE inv_requests
   SET department_id = sqlc.narg(department_id), warehouse_id = sqlc.narg(warehouse_id), needed_on = sqlc.narg(needed_on), note = sqlc.narg(note), est_total = @est_total,
       version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING version;

-- name: SetRequestState :exec
UPDATE inv_requests
   SET status = @status, cycle = @cycle, submitted_at = sqlc.narg(submitted_at), returned_note = sqlc.narg(returned_note), est_total = @est_total, version = version + 1, updated_by = sqlc.narg(actor_id)
 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteRequestLines :exec
DELETE FROM inv_request_lines WHERE property_id = @property_id AND request_id = @request_id;

-- name: InsertRequestLine :exec
INSERT INTO inv_request_lines (tenant_id, property_id, request_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock, est_unit_price, supplier_id, note)
VALUES (@tenant_id, @property_id, @request_id, @line_no, @item_id, @purchase_unit_id, @qty_purchase, @qty_stock, sqlc.narg(est_unit_price), sqlc.narg(supplier_id), sqlc.narg(note));

-- name: ListRequestLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, l.purchase_unit_id, pu.code AS unit_code, l.qty_purchase, l.qty_stock, l.est_unit_price, l.supplier_id,
       s.code AS supplier_code, s.name AS supplier_name, l.note, su.code AS stock_unit_code, iu.stock_num, iu.stock_den,
       COALESCE((SELECT sum(b.qty) FROM inv_balances b WHERE b.property_id = l.property_id AND b.item_id = l.item_id AND (r.warehouse_id IS NULL OR b.warehouse_id = r.warehouse_id)), 0)::numeric(18,6) AS on_hand
  FROM inv_request_lines l
  JOIN inv_requests r ON r.property_id = l.property_id AND r.id = l.request_id
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_units pu ON pu.property_id = l.property_id AND pu.id = l.purchase_unit_id
  JOIN inv_item_units iu ON iu.item_id = l.item_id AND iu.unit_id = l.purchase_unit_id
  LEFT JOIN suppliers s ON s.property_id = l.property_id AND s.id = l.supplier_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.request_id = @request_id
 ORDER BY l.line_no;

-- The order each line of a request is on.
-- name: ListRequestLineOrders :many
SELECT pl.request_line_no, po.po_number, po.status
  FROM inv_po_lines pl JOIN inv_purchase_orders po ON po.property_id = pl.property_id AND po.id = pl.po_id
 WHERE pl.property_id = @property_id AND pl.request_id = @request_id AND pl.request_line_no IS NOT NULL;

-- name: UpdateRequestLineEdit :exec
UPDATE inv_request_lines SET qty_purchase = @qty_purchase, qty_stock = @qty_stock, est_unit_price = sqlc.narg(est_unit_price)
 WHERE property_id = @property_id AND request_id = @request_id AND line_no = @line_no;

-- ---------------------------------------------------------------------------------------------------------------------
-- The steps, the edits and the notices

-- name: InsertStep :one
INSERT INTO inv_approval_steps (tenant_id, property_id, request_id, cycle, level_no, name, approver_kind, permission, condition_kind, threshold, edit_allowed, status, skip_reason, activated_at)
VALUES (@tenant_id, @property_id, @request_id, @cycle, @level_no, @name, @approver_kind, sqlc.narg(permission), @condition_kind, sqlc.narg(threshold), @edit_allowed, @status,
        sqlc.narg(skip_reason), sqlc.narg(activated_at))
RETURNING id;

-- name: ListSteps :many
SELECT s.*, u.full_name AS decided_by_name
  FROM inv_approval_steps s LEFT JOIN users u ON u.tenant_id = s.tenant_id AND u.id = s.decided_by
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.request_id = ANY(@request_ids::bigint[])
 ORDER BY s.request_id, s.cycle, s.level_no;

-- name: ListStepsOfCycle :many
SELECT * FROM inv_approval_steps WHERE tenant_id = @tenant_id AND property_id = @property_id AND request_id = @request_id AND cycle = @cycle ORDER BY level_no;

-- name: SetStepState :exec
UPDATE inv_approval_steps
   SET status = @status, skip_reason = sqlc.narg(skip_reason), activated_at = sqlc.narg(activated_at), decided_by = sqlc.narg(decided_by), decided_at = sqlc.narg(decided_at),
       total_seen = sqlc.narg(total_seen), note = sqlc.narg(note)
 WHERE property_id = @property_id AND id = @id;

-- name: InsertEdit :exec
INSERT INTO inv_approval_edits (tenant_id, property_id, step_id, line_no, field, old_value, new_value, edited_by, edited_at)
VALUES (@tenant_id, @property_id, @step_id, @line_no, @field, @old_value, @new_value, @edited_by, @edited_at);

-- name: ListEdits :many
SELECT e.id, e.step_id, s.level_no, s.cycle, e.line_no, e.field, e.old_value, e.new_value, e.edited_by, u.full_name AS edited_by_name, e.edited_at
  FROM inv_approval_edits e
  JOIN inv_approval_steps s ON s.property_id = e.property_id AND s.id = e.step_id
  JOIN users u ON u.id = e.edited_by
 WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND s.request_id = @request_id
 ORDER BY e.id;

-- name: InsertNotice :exec
INSERT INTO inv_notices (tenant_id, property_id, user_id, kind, step_id, request_id, created_at)
VALUES (@tenant_id, @property_id, @user_id, @kind, sqlc.narg(step_id), sqlc.narg(request_id), @created_at);

-- name: ListNotices :many
SELECT n.id, n.kind, n.step_id, n.request_id, r.request_number, n.created_at, n.seen_at, st.name AS step_name, COALESCE(st.level_no, 0)::int AS level_no
  FROM inv_notices n
  LEFT JOIN inv_requests r ON r.property_id = n.property_id AND r.id = n.request_id
  LEFT JOIN inv_approval_steps st ON st.property_id = n.property_id AND st.id = n.step_id
 WHERE n.tenant_id = @tenant_id AND n.property_id = @property_id AND n.user_id = @user_id
 ORDER BY n.id DESC
 LIMIT @row_limit;

-- name: CountUnseenNotices :one
SELECT count(*)::int FROM inv_notices WHERE tenant_id = @tenant_id AND property_id = @property_id AND user_id = @user_id AND seen_at IS NULL;

-- name: MarkNoticeSeen :execrows
UPDATE inv_notices SET seen_at = @now WHERE tenant_id = @tenant_id AND property_id = @property_id AND user_id = @user_id AND id = @id AND seen_at IS NULL;

-- name: MarkAllNoticesSeen :exec
UPDATE inv_notices SET seen_at = @now WHERE tenant_id = @tenant_id AND property_id = @property_id AND user_id = @user_id AND seen_at IS NULL;

-- name: CloseStepNotices :exec
UPDATE inv_notices SET seen_at = @now WHERE property_id = @property_id AND step_id = @step_id AND kind = 'APPROVAL_WAITING' AND seen_at IS NULL;

-- The documents whose ACTIVE step the user can decide now ("Perlu saya setujui", 5.8 item 10): not their own, not one whose round they have already decided a level of, and a step they are eligible for.
-- name: ApprovalInbox :many
SELECT r.id AS request_id, r.request_number, r.est_total, r.department_id, d.name AS department_name, r.requester_id, u.full_name AS requester_name, r.submitted_at,
       s.id AS step_id, s.level_no, s.name AS step_name
  FROM inv_approval_steps s
  JOIN inv_requests r ON r.property_id = s.property_id AND r.id = s.request_id AND r.status = 'SUBMITTED' AND r.cycle = s.cycle
  JOIN users u ON u.tenant_id = r.tenant_id AND u.id = r.requester_id
  LEFT JOIN departments d ON d.property_id = r.property_id AND d.id = r.department_id
  LEFT JOIN departments pd ON pd.property_id = d.property_id AND pd.id = d.parent_id
 WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'ACTIVE' AND r.requester_id <> @user_id
   AND NOT EXISTS (SELECT 1 FROM inv_approval_steps x WHERE x.request_id = s.request_id AND x.cycle = s.cycle AND x.status = 'APPROVED' AND x.decided_by = @user_id)
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
-- Suppliers and what they sell

-- name: ListSuppliersLite :many
SELECT id, code, name, payment_terms_days, is_active FROM suppliers WHERE tenant_id = @tenant_id AND property_id = @property_id AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean) ORDER BY name, id;

-- name: GetSupplierLite :one
SELECT id, code, name, payment_terms_days, is_active FROM suppliers WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListSupplierItems :many
SELECT si.supplier_id, si.item_id, i.code AS item_code, i.name AS item_name, si.purchase_unit_id, u.code AS unit_code, si.last_price, si.supplier_sku, si.updated_at
  FROM inv_supplier_items si
  JOIN inv_items i ON i.property_id = si.property_id AND i.id = si.item_id
  JOIN inv_units u ON u.property_id = si.property_id AND u.id = si.purchase_unit_id
 WHERE si.tenant_id = @tenant_id AND si.property_id = @property_id
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR si.supplier_id = sqlc.narg(supplier_id)::bigint)
   AND (sqlc.narg(item_id)::bigint IS NULL OR si.item_id = sqlc.narg(item_id)::bigint)
 ORDER BY i.code, si.supplier_id;

-- name: UpsertSupplierItem :exec
INSERT INTO inv_supplier_items (tenant_id, property_id, supplier_id, item_id, purchase_unit_id, last_price, updated_by)
VALUES (@tenant_id, @property_id, @supplier_id, @item_id, @purchase_unit_id, @last_price, sqlc.narg(actor_id))
ON CONFLICT (supplier_id, item_id) DO UPDATE SET purchase_unit_id = EXCLUDED.purchase_unit_id, last_price = EXCLUDED.last_price, updated_at = now(), updated_by = EXCLUDED.updated_by;

-- ---------------------------------------------------------------------------------------------------------------------
-- Purchase orders

-- name: InsertPO :one
INSERT INTO inv_purchase_orders (tenant_id, property_id, po_number, client_uuid, supplier_id, warehouse_id, request_id, order_date, expected_on, status, notes, total, created_by, updated_by)
VALUES (@tenant_id, @property_id, @po_number, @client_uuid, @supplier_id, @warehouse_id, sqlc.narg(request_id), @order_date, sqlc.narg(expected_on), @status, sqlc.narg(notes), @total,
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: GetPOByClientUUID :one
SELECT id FROM inv_purchase_orders WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetPO :one
SELECT po.*, s.code AS supplier_code, s.name AS supplier_name, w.code AS warehouse_code, w.name AS warehouse_name, r.request_number
  FROM inv_purchase_orders po
  JOIN suppliers s ON s.property_id = po.property_id AND s.id = po.supplier_id
  JOIN inv_warehouses w ON w.property_id = po.property_id AND w.id = po.warehouse_id
  LEFT JOIN inv_requests r ON r.property_id = po.property_id AND r.id = po.request_id
 WHERE po.tenant_id = @tenant_id AND po.property_id = @property_id AND po.id = @id;

-- name: ListPOs :many
SELECT po.id, po.po_number, po.status, po.supplier_id, s.name AS supplier_name, po.warehouse_id, w.name AS warehouse_name, po.request_id, r.request_number, po.order_date, po.expected_on, po.total,
       (SELECT count(*) FROM inv_po_lines l WHERE l.po_id = po.id)::int AS line_count,
       COALESCE((SELECT sum(l.qty_received_stock) FROM inv_po_lines l WHERE l.po_id = po.id), 0)::numeric(18,6) AS qty_received,
       COALESCE((SELECT sum(l.qty_stock) FROM inv_po_lines l WHERE l.po_id = po.id), 0)::numeric(18,6) AS qty_ordered
  FROM inv_purchase_orders po
  JOIN suppliers s ON s.property_id = po.property_id AND s.id = po.supplier_id
  JOIN inv_warehouses w ON w.property_id = po.property_id AND w.id = po.warehouse_id
  LEFT JOIN inv_requests r ON r.property_id = po.property_id AND r.id = po.request_id
 WHERE po.tenant_id = @tenant_id AND po.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR po.status = sqlc.narg(status)::text)
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR po.supplier_id = sqlc.narg(supplier_id)::bigint)
   AND (sqlc.narg(open_only)::boolean IS NULL OR (po.status IN ('ORDERED', 'PARTIAL')) = sqlc.narg(open_only)::boolean)
   AND (sqlc.narg(q)::text IS NULL OR po.po_number ILIKE '%' || sqlc.narg(q)::text || '%')
 ORDER BY po.id DESC
 LIMIT @row_limit;

-- name: SetPOStatus :exec
UPDATE inv_purchase_orders SET status = @status, version = version + 1, updated_by = sqlc.narg(actor_id) WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: InsertPOLine :exec
INSERT INTO inv_po_lines (tenant_id, property_id, po_id, line_no, item_id, purchase_unit_id, qty_purchase, qty_stock, unit_price, amount, request_id, request_line_no)
VALUES (@tenant_id, @property_id, @po_id, @line_no, @item_id, @purchase_unit_id, @qty_purchase, @qty_stock, @unit_price, @amount, sqlc.narg(request_id), sqlc.narg(request_line_no));

-- name: ListPOLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, l.purchase_unit_id, pu.code AS unit_code, l.qty_purchase, l.qty_stock, l.unit_price, l.amount,
       l.qty_received_stock, l.closed, l.request_line_no, su.code AS stock_unit_code, iu.stock_num, iu.stock_den
  FROM inv_po_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_units pu ON pu.property_id = l.property_id AND pu.id = l.purchase_unit_id
  JOIN inv_item_units iu ON iu.item_id = l.item_id AND iu.unit_id = l.purchase_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.po_id = @po_id
 ORDER BY l.line_no;

-- name: AddPOLineReceived :exec
UPDATE inv_po_lines SET qty_received_stock = qty_received_stock + @qty WHERE property_id = @property_id AND po_id = @po_id AND line_no = @line_no;

-- name: ClosePOLines :exec
UPDATE inv_po_lines SET closed = true WHERE property_id = @property_id AND po_id = @po_id AND qty_received_stock < qty_stock;

-- name: CountPOReceipts :one
SELECT count(*)::int FROM inv_receipts WHERE tenant_id = @tenant_id AND property_id = @property_id AND po_id = @po_id AND status = 'POSTED';

-- name: ReleasePOLines :exec
UPDATE inv_po_lines SET request_id = NULL, request_line_no = NULL WHERE property_id = @property_id AND po_id = @po_id;

-- name: CountLiveOrdersOfRequest :one
SELECT count(*)::int FROM inv_purchase_orders WHERE tenant_id = @tenant_id AND property_id = @property_id AND request_id = @request_id AND id <> @po_id AND status <> 'CANCELLED';

-- Which request lines already have an order (a line of a request is ordered once).
-- name: RequestLinesOrdered :many
SELECT request_line_no FROM inv_po_lines WHERE property_id = @property_id AND request_id = @request_id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Receipts

-- name: InsertReceipt :one
INSERT INTO inv_receipts (tenant_id, property_id, receipt_number, client_uuid, supplier_id, po_id, warehouse_id, received_on, delivery_note, delivery_note_date, total_value, note, created_by)
VALUES (@tenant_id, @property_id, @receipt_number, @client_uuid, @supplier_id, sqlc.narg(po_id), @warehouse_id, @received_on, sqlc.narg(delivery_note), sqlc.narg(delivery_note_date), @total_value,
        sqlc.narg(note), sqlc.narg(actor_id))
RETURNING id;

-- name: GetReceiptByClientUUID :one
SELECT id FROM inv_receipts WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetReceipt :one
SELECT r.*, s.code AS supplier_code, s.name AS supplier_name, w.code AS warehouse_code, w.name AS warehouse_name, po.po_number, d.id AS draft_id, d.status AS draft_status
  FROM inv_receipts r
  JOIN suppliers s ON s.property_id = r.property_id AND s.id = r.supplier_id
  JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
  LEFT JOIN inv_purchase_orders po ON po.property_id = r.property_id AND po.id = r.po_id
  LEFT JOIN inv_bill_drafts d ON d.property_id = r.property_id AND d.receipt_id = r.id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id AND r.id = @id;

-- name: ListReceipts :many
SELECT r.id, r.receipt_number, r.status, r.supplier_id, s.name AS supplier_name, r.warehouse_id, w.name AS warehouse_name, r.po_id, po.po_number, r.received_on, r.delivery_note, r.total_value,
       d.id AS draft_id, d.status AS draft_status,
       (SELECT count(*) FROM inv_receipt_lines l WHERE l.receipt_id = r.id)::int AS line_count
  FROM inv_receipts r
  JOIN suppliers s ON s.property_id = r.property_id AND s.id = r.supplier_id
  JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
  LEFT JOIN inv_purchase_orders po ON po.property_id = r.property_id AND po.id = r.po_id
  LEFT JOIN inv_bill_drafts d ON d.property_id = r.property_id AND d.receipt_id = r.id
 WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR r.status = sqlc.narg(status)::text)
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR r.supplier_id = sqlc.narg(supplier_id)::bigint)
   AND (sqlc.narg(po_id)::bigint IS NULL OR r.po_id = sqlc.narg(po_id)::bigint)
   AND (sqlc.narg(q)::text IS NULL OR r.receipt_number ILIKE '%' || sqlc.narg(q)::text || '%' OR r.delivery_note ILIKE '%' || sqlc.narg(q)::text || '%')
 ORDER BY r.id DESC
 LIMIT @row_limit;

-- name: InsertReceiptLine :exec
INSERT INTO inv_receipt_lines (tenant_id, property_id, receipt_id, line_no, item_id, po_line_no, purchase_unit_id, qty_purchase, qty_stock, unit_price, value, movement_id)
VALUES (@tenant_id, @property_id, @receipt_id, @line_no, @item_id, sqlc.narg(po_line_no), @purchase_unit_id, @qty_purchase, @qty_stock, @unit_price, @value, @movement_id);

-- name: ListReceiptLines :many
SELECT l.line_no, l.item_id, i.code AS item_code, i.name AS item_name, l.po_line_no, l.purchase_unit_id, pu.code AS unit_code, l.qty_purchase, l.qty_stock, l.unit_price, l.value, l.movement_id,
       l.qty_billed_stock, l.value_billed, l.qty_returned_stock, l.value_returned, su.code AS stock_unit_code, iu.stock_num, iu.stock_den
  FROM inv_receipt_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
  JOIN inv_units pu ON pu.property_id = l.property_id AND pu.id = l.purchase_unit_id
  JOIN inv_item_units iu ON iu.item_id = l.item_id AND iu.unit_id = l.purchase_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.receipt_id = ANY(@receipt_ids::bigint[])
 ORDER BY l.receipt_id, l.line_no;

-- name: GetReceiptLine :one
SELECT l.*, i.code AS item_code, i.name AS item_name, pu.code AS unit_code, iu.stock_num, iu.stock_den, r.warehouse_id, r.supplier_id, r.status AS receipt_status, r.receipt_number
  FROM inv_receipt_lines l
  JOIN inv_receipts r ON r.property_id = l.property_id AND r.id = l.receipt_id
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units pu ON pu.property_id = l.property_id AND pu.id = l.purchase_unit_id
  JOIN inv_item_units iu ON iu.item_id = l.item_id AND iu.unit_id = l.purchase_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.receipt_id = @receipt_id AND l.line_no = @line_no;

-- name: SetReceiptReversed :exec
UPDATE inv_receipts SET status = 'REVERSED', reversed_at = @now, reversed_by = sqlc.narg(actor_id), reverse_reason = sqlc.narg(reason) WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: AddReceiptLineBilled :exec
UPDATE inv_receipt_lines SET qty_billed_stock = qty_billed_stock + @qty, value_billed = value_billed + @value WHERE property_id = @property_id AND receipt_id = @receipt_id AND line_no = @line_no;

-- name: AddReceiptLineReturned :exec
UPDATE inv_receipt_lines SET qty_returned_stock = qty_returned_stock + @qty, value_returned = value_returned + @value WHERE property_id = @property_id AND receipt_id = @receipt_id AND line_no = @line_no;

-- ---------------------------------------------------------------------------------------------------------------------
-- Draft bills

-- name: InsertDraft :one
INSERT INTO inv_bill_drafts (tenant_id, property_id, receipt_id, supplier_id) VALUES (@tenant_id, @property_id, @receipt_id, @supplier_id) RETURNING id;

-- name: GetDraft :one
SELECT d.*, r.receipt_number, r.received_on, r.warehouse_id, w.name AS warehouse_name, r.delivery_note, r.po_id, po.po_number, s.code AS supplier_code, s.name AS supplier_name, r.status AS receipt_status
  FROM inv_bill_drafts d
  JOIN inv_receipts r ON r.property_id = d.property_id AND r.id = d.receipt_id
  JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
  JOIN suppliers s ON s.property_id = d.property_id AND s.id = d.supplier_id
  LEFT JOIN inv_purchase_orders po ON po.property_id = r.property_id AND po.id = r.po_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.id = @id;

-- name: GetDraftOfReceipt :one
SELECT * FROM inv_bill_drafts WHERE tenant_id = @tenant_id AND property_id = @property_id AND receipt_id = @receipt_id;

-- name: ListDrafts :many
SELECT d.id, d.receipt_id, d.status, d.supplier_id, s.name AS supplier_name, r.receipt_number, r.received_on, r.po_id, po.po_number, r.warehouse_id, w.name AS warehouse_name,
       COALESCE(sum(CASE WHEN l.qty_stock - l.qty_billed_stock - l.qty_returned_stock > 0 THEN l.value - l.value_billed - l.value_returned ELSE 0 END), 0)::numeric(18,3) AS remaining_value,
       COALESCE(sum(l.value), 0)::numeric(18,3) AS receipt_value
  FROM inv_bill_drafts d
  JOIN inv_receipts r ON r.property_id = d.property_id AND r.id = d.receipt_id
  JOIN suppliers s ON s.property_id = d.property_id AND s.id = d.supplier_id
  JOIN inv_warehouses w ON w.property_id = r.property_id AND w.id = r.warehouse_id
  LEFT JOIN inv_purchase_orders po ON po.property_id = r.property_id AND po.id = r.po_id
  LEFT JOIN inv_receipt_lines l ON l.property_id = d.property_id AND l.receipt_id = d.receipt_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id
   AND (sqlc.narg(status)::text IS NULL OR d.status = sqlc.narg(status)::text)
   AND (sqlc.narg(open_only)::boolean IS NULL OR (d.status IN ('OPEN', 'PARTIAL')) = sqlc.narg(open_only)::boolean)
   AND (sqlc.narg(supplier_id)::bigint IS NULL OR d.supplier_id = sqlc.narg(supplier_id)::bigint)
 GROUP BY d.id, r.id, s.id, w.id, po.id
 ORDER BY r.received_on, d.id
 LIMIT @row_limit;

-- name: SetDraftStatus :exec
UPDATE inv_bill_drafts SET status = @status, version = version + 1 WHERE property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------------
-- Matches

-- name: InsertMatch :one
INSERT INTO inv_bill_matches (tenant_id, property_id, receipt_id, receipt_line_no, bill_id, bill_line_no, qty_stock, bill_cost, receipt_value, variance, inventory_part, ppv_part,
                              credit_note_id, return_id, created_by)
VALUES (@tenant_id, @property_id, @receipt_id, @receipt_line_no, @bill_id, @bill_line_no, @qty_stock, @bill_cost, @receipt_value, @variance, @inventory_part, @ppv_part,
        sqlc.narg(credit_note_id), sqlc.narg(return_id), sqlc.narg(actor_id))
RETURNING id;

-- name: SetMatchClearing :exec
UPDATE inv_bill_matches SET clearing_movement_ids = COALESCE(@ids::bigint[], '{}') WHERE property_id = @property_id AND id = @id;

-- name: ListMatchesOfBill :many
SELECT m.*, l.movement_id AS receipt_movement_id, r.warehouse_id, l.item_id
  FROM inv_bill_matches m
  JOIN inv_receipt_lines l ON l.property_id = m.property_id AND l.receipt_id = m.receipt_id AND l.line_no = m.receipt_line_no
  JOIN inv_receipts r ON r.property_id = m.property_id AND r.id = m.receipt_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.bill_id = @bill_id AND m.unmatched_at IS NULL
 ORDER BY m.id;

-- name: ListMatchesOfReceiptLine :many
SELECT m.* FROM inv_bill_matches m
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.receipt_id = @receipt_id AND m.receipt_line_no = @receipt_line_no AND m.unmatched_at IS NULL
 ORDER BY m.id;

-- name: ListMatchesOfReceipt :many
SELECT m.id, m.receipt_id, m.receipt_line_no, m.bill_id, b.bill_number, b.supplier_invoice_number, m.bill_line_no, m.qty_stock, m.bill_cost, m.receipt_value, m.variance, m.inventory_part, m.ppv_part,
       m.credit_note_id, m.return_id, m.created_at
  FROM inv_bill_matches m JOIN supplier_bills b ON b.property_id = m.property_id AND b.id = m.bill_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.receipt_id = @receipt_id AND m.unmatched_at IS NULL
 ORDER BY m.id;

-- name: SetMatchUnmatched :exec
UPDATE inv_bill_matches SET unmatched_at = @now, unmatched_by = sqlc.narg(actor_id) WHERE property_id = @property_id AND id = @id AND unmatched_at IS NULL;

-- name: CountLiveMatchesOfBill :one
SELECT count(*)::int FROM inv_bill_matches WHERE tenant_id = @tenant_id AND property_id = @property_id AND bill_id = @bill_id AND unmatched_at IS NULL;

-- name: ListBillLinesForMatch :many
SELECT bill_id, line_no, account_id, amount, vat_amount, vat_treatment FROM supplier_bill_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND bill_id = @bill_id ORDER BY line_no;

-- name: ListCreditLinesForMatch :many
SELECT l.line_no, l.bill_id, l.bill_line_no, l.account_id, l.amount, l.vat_amount, l.vat_treatment, c.supplier_id, c.status, c.credit_number
  FROM supplier_credit_note_lines l JOIN supplier_credit_notes c ON c.property_id = l.property_id AND c.id = l.credit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.credit_id = @credit_id ORDER BY l.line_no;

-- ---------------------------------------------------------------------------------------------------------------------
-- Returns

-- name: InsertReturn :one
INSERT INTO inv_returns (tenant_id, property_id, return_number, client_uuid, supplier_id, warehouse_id, receipt_id, reason, total_value, created_by)
VALUES (@tenant_id, @property_id, @return_number, @client_uuid, @supplier_id, @warehouse_id, @receipt_id, @reason, @total_value, sqlc.narg(actor_id))
RETURNING id;

-- name: GetReturnByClientUUID :one
SELECT id FROM inv_returns WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetReturn :one
SELECT x.*, s.code AS supplier_code, s.name AS supplier_name, w.code AS warehouse_code, w.name AS warehouse_name, r.receipt_number, c.credit_number
  FROM inv_returns x
  JOIN suppliers s ON s.property_id = x.property_id AND s.id = x.supplier_id
  JOIN inv_warehouses w ON w.property_id = x.property_id AND w.id = x.warehouse_id
  JOIN inv_receipts r ON r.property_id = x.property_id AND r.id = x.receipt_id
  LEFT JOIN supplier_credit_notes c ON c.property_id = x.property_id AND c.id = x.credit_note_id
 WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id AND x.id = @id;

-- name: ListReturns :many
SELECT x.id, x.return_number, x.supplier_id, s.name AS supplier_name, x.warehouse_id, w.name AS warehouse_name, x.receipt_id, r.receipt_number, x.reason, x.total_value, x.credit_note_id, c.credit_number, x.created_at
  FROM inv_returns x
  JOIN suppliers s ON s.property_id = x.property_id AND s.id = x.supplier_id
  JOIN inv_warehouses w ON w.property_id = x.property_id AND w.id = x.warehouse_id
  JOIN inv_receipts r ON r.property_id = x.property_id AND r.id = x.receipt_id
  LEFT JOIN supplier_credit_notes c ON c.property_id = x.property_id AND c.id = x.credit_note_id
 WHERE x.tenant_id = @tenant_id AND x.property_id = @property_id
   AND (sqlc.narg(receipt_id)::bigint IS NULL OR x.receipt_id = sqlc.narg(receipt_id)::bigint)
 ORDER BY x.id DESC
 LIMIT @row_limit;

-- name: InsertReturnLine :exec
INSERT INTO inv_return_lines (tenant_id, property_id, return_id, line_no, receipt_id, receipt_line_no, item_id, qty_stock, value, movement_id)
VALUES (@tenant_id, @property_id, @return_id, @line_no, @receipt_id, @receipt_line_no, @item_id, @qty_stock, @value, @movement_id);

-- name: ListReturnLines :many
SELECT l.line_no, l.receipt_id, l.receipt_line_no, l.item_id, i.code AS item_code, i.name AS item_name, l.qty_stock, l.value, l.movement_id, su.code AS stock_unit_code
  FROM inv_return_lines l
  JOIN inv_items i ON i.property_id = l.property_id AND i.id = l.item_id
  JOIN inv_units su ON su.property_id = i.property_id AND su.id = i.stock_unit_id
 WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.return_id = @return_id ORDER BY l.line_no;

-- name: SetReturnTotal :exec
UPDATE inv_returns SET total_value = @total_value WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetReturnCreditNote :exec
UPDATE inv_returns SET credit_note_id = @credit_note_id WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------------------------------------------------------------
-- The accounts of the purchasing path

-- name: GetAccountByKey :one
SELECT a.id, a.code, a.name, a.account_type, a.is_active, a.is_postable, a.default_department_id
  FROM gl_account_map m JOIN gl_accounts a ON a.property_id = m.property_id AND a.id = m.account_id
 WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id AND m.map_key = @map_key;

-- The goods of a receipt that are still there to be billed or returned in the receiving warehouse (the balance quantity), and the stock account of the item.
-- name: ItemStockAccount :one
SELECT c.inventory_account_id, c.kind AS category_kind FROM inv_items i JOIN inv_categories c ON c.property_id = i.property_id AND c.id = i.category_id
 WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.id = @id;

-- name: ListNonStockBillLinesOfBill :many
SELECT line_no FROM supplier_bill_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND bill_id = @bill_id ORDER BY line_no;

-- The aging of the receipts that nobody has billed (a warning of the night audit and a line of the setup check).
-- name: CountUnbilledReceiptsOlderThan :one
SELECT count(*)::int FROM inv_bill_drafts d JOIN inv_receipts r ON r.property_id = d.property_id AND r.id = d.receipt_id
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.status IN ('OPEN', 'PARTIAL') AND r.received_on < @before;

-- The goods of a receipt line that are on hand: the balance of the warehouse (the moving average) and the layer of the receipt (FIFO).
-- name: OnHandForReceipt :one
SELECT COALESCE((SELECT b.qty FROM inv_balances b WHERE b.property_id = @property_id AND b.warehouse_id = @warehouse_id AND b.item_id = @item_id), 0)::numeric(18,6) AS balance_qty,
       COALESCE((SELECT l.qty_remaining FROM inv_layers l WHERE l.property_id = @property_id AND l.movement_id = @movement_id), 0)::numeric(18,6) AS layer_qty;

-- The bill a confirmation made under an Idempotency-Key (a repeated request answers it).
-- name: FindBillByKey :one
SELECT id FROM supplier_bills WHERE tenant_id = @tenant_id AND property_id = @property_id AND idempotency_key = @key;

-- The setup check of the chain: a head or a deputy who is not active any more, with the department.
-- name: InactiveDepartmentLeaders :many
SELECT d.id, d.code, u.full_name, (d.head_user_id = u.id)::boolean AS is_head
  FROM departments d JOIN users u ON u.tenant_id = d.tenant_id AND (u.id = d.head_user_id OR u.id = d.deputy_user_id)
 WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND NOT u.is_active
 ORDER BY d.code, u.id;

-- name: CountApprovalLevels :one
SELECT count(*)::int FROM inv_approval_levels WHERE tenant_id = @tenant_id AND property_id = @property_id AND doc_type = @doc_type;
