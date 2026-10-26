-- Banquet, PR 2 (docs/architecture/36-banquet.md): the money of an event. Every query is scoped by tenant and property.

-- ---------------------------------------------------------------- charge codes
-- name: ChargeCodeByCode :one
SELECT id, code, price_mode, is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND code = @code;

-- ---------------------------------------------------------------- event money
-- name: EventMoney :one
SELECT e.status, e.posting_mode, e.policy_base, e.contract_value_estimate, e.cancelled_fee_item_id, e.booking_group_id, e.booker_company_id, e.number, e.name, e.sales_user_id
FROM banquet_events e WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.id = @id;

-- name: SetEventPosting :exec
UPDATE banquet_events SET posting_mode = sqlc.narg(posting_mode), version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetEventPolicyBase :exec
UPDATE banquet_events SET policy_base = sqlc.narg(policy_base), updated_by = sqlc.narg(actor_id) WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetContractEstimate :exec
UPDATE banquet_events SET contract_value_estimate = @estimate WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: SetCancelledFee :exec
UPDATE banquet_events SET cancelled_fee_item_id = @folio_item_id WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: EventBookingGroup :one
SELECT booking_group_id FROM banquet_events WHERE property_id = @property_id AND id = @id;

-- name: LinkEventGroup :exec
UPDATE banquet_events SET booking_group_id = @group_id, version = version + 1, updated_by = sqlc.narg(actor_id) WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GroupMasterFolio :one
-- The MASTER folio of a group of the property, open: the target of the transfer of the bill of an event (4.8).
SELECT f.id, f.folio_number FROM folios f
WHERE f.tenant_id = @tenant_id AND f.property_id = @property_id AND f.booking_group_id = @group_id AND f.folio_type = 'MASTER' AND f.status = 'OPEN'
ORDER BY f.id LIMIT 1;

-- name: GroupsWithMaster :many
SELECT g.id, g.code AS group_code, g.name, f.id AS folio_id, f.folio_number
FROM booking_groups g
JOIN folios f ON f.property_id = g.property_id AND f.booking_group_id = g.id AND f.folio_type = 'MASTER' AND f.status = 'OPEN'
WHERE g.tenant_id = @tenant_id AND g.property_id = @property_id
ORDER BY g.name, g.id;

-- ---------------------------------------------------------------- lines
-- name: ListLines :many
SELECT l.id, l.event_id, l.session_id, l.line_no, l.item_id, l.package_id, l.description, l.basis, l.quantity, l.unit_price, l.price_mode, l.discount_pct, l.discount_amount,
       l.discount_by, l.discount_approved_by, l.charge_code_id, cc.code AS charge_code, l.fb_class, l.pos_product_id, l.is_active
FROM banquet_event_lines l
LEFT JOIN charge_codes cc ON cc.property_id = l.property_id AND cc.id = l.charge_code_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.event_id = @event_id
  AND (sqlc.narg(id)::bigint IS NULL OR l.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(session_id)::bigint IS NULL OR l.session_id = sqlc.narg(session_id)::bigint)
  AND (NOT @active_only::boolean OR l.is_active)
ORDER BY l.session_id, l.line_no;

-- name: ListLineComponents :many
SELECT c.line_id, c.item_id, c.name, c.charge_code_id, cc.code AS charge_code, c.qty_per_pax, c.list_price, c.fb_class, c.pos_product_id, c.sort_order
FROM banquet_line_components c
JOIN banquet_event_lines l ON l.property_id = c.property_id AND l.id = c.line_id
JOIN charge_codes cc ON cc.property_id = c.property_id AND cc.id = c.charge_code_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.event_id = @event_id
ORDER BY c.line_id, c.sort_order, c.item_id;

-- name: NextLineNo :one
SELECT (COALESCE(max(line_no), 0) + 1)::int FROM banquet_event_lines WHERE property_id = @property_id AND event_id = @event_id;

-- name: InsertLine :one
INSERT INTO banquet_event_lines (tenant_id, property_id, event_id, session_id, line_no, item_id, package_id, description, basis, quantity, unit_price, price_mode,
                                 discount_pct, discount_amount, discount_by, discount_approved_by, charge_code_id, fb_class, pos_product_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @event_id, @session_id, @line_no, sqlc.narg(item_id), sqlc.narg(package_id), @description, @basis, @quantity, @unit_price, @price_mode,
        sqlc.narg(discount_pct), sqlc.narg(discount_amount), sqlc.narg(discount_by), sqlc.narg(discount_approved_by), sqlc.narg(charge_code_id), sqlc.narg(fb_class),
        sqlc.narg(pos_product_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateLine :execrows
UPDATE banquet_event_lines SET description = @description, quantity = @quantity, unit_price = @unit_price, price_mode = @price_mode, discount_pct = sqlc.narg(discount_pct),
       discount_amount = sqlc.narg(discount_amount), discount_by = sqlc.narg(discount_by), discount_approved_by = sqlc.narg(discount_approved_by), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND event_id = @event_id AND id = @id AND is_active;

-- name: DeactivateLine :execrows
UPDATE banquet_event_lines SET is_active = false, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND event_id = @event_id AND id = @id AND is_active;

-- name: InsertLineComponent :exec
INSERT INTO banquet_line_components (property_id, line_id, item_id, name, charge_code_id, qty_per_pax, list_price, fb_class, pos_product_id, sort_order)
VALUES (@property_id, @line_id, @item_id, @name, @charge_code_id, @qty_per_pax, @list_price, sqlc.narg(fb_class), sqlc.narg(pos_product_id), @sort_order);

-- name: PackageSnapshot :many
-- The components of a package as they are when it is sold.
SELECT c.item_id, i.name, i.charge_code_id, c.qty_per_pax, i.list_price, i.fb_class, i.pos_product_id, c.sort_order
FROM banquet_package_components c JOIN banquet_items i ON i.property_id = c.property_id AND i.id = c.item_id
WHERE c.property_id = @property_id AND c.package_id = @package_id
ORDER BY c.sort_order, c.item_id;

-- name: SessionHasPosting :one
SELECT EXISTS (SELECT 1 FROM banquet_session_postings WHERE property_id = @property_id AND session_id = @session_id);

-- name: SessionOfEvent :one
SELECT status FROM banquet_sessions WHERE property_id = @property_id AND event_id = @event_id AND id = @id;

-- name: SetSessionActual :execrows
UPDATE banquet_sessions SET pax_actual = sqlc.narg(pax_actual) WHERE tenant_id = @tenant_id AND property_id = @property_id AND event_id = @event_id AND id = @id AND status = 'ACTIVE';

-- ---------------------------------------------------------------- postings
-- name: InsertSessionPosting :one
INSERT INTO banquet_session_postings (tenant_id, property_id, session_id, folio_id, business_date, pax_billed, pax_basis, auto, on_guarantee, posted_by)
VALUES (@tenant_id, @property_id, @session_id, @folio_id, @business_date, @pax_billed, @pax_basis, @auto, @on_guarantee, sqlc.narg(posted_by))
RETURNING id;

-- name: InsertPostingItem :one
INSERT INTO banquet_posting_items (property_id, posting_id, line_id, component_item_id, folio_item_id) VALUES (@property_id, @posting_id, @line_id, sqlc.narg(component_item_id), @folio_item_id)
RETURNING id;

-- name: ListEventPostings :many
SELECT p.id, p.session_id, p.folio_id, p.business_date, p.pax_billed, p.pax_basis, p.auto, p.on_guarantee, p.posted_at
FROM banquet_session_postings p JOIN banquet_sessions s ON s.property_id = p.property_id AND s.id = p.session_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND s.event_id = @event_id
ORDER BY p.id;

-- name: EventPostedTotals :many
-- What each line of the event has posted, with the account of its charge code: the money summary of the event page.
SELECT pi.line_id, COALESCE(sum(fi.net_amount), 0)::numeric AS net, COALESCE(sum(fi.debit - fi.credit), 0)::numeric AS gross
FROM banquet_posting_items pi
JOIN banquet_session_postings p ON p.property_id = pi.property_id AND p.id = pi.posting_id
JOIN banquet_sessions s ON s.property_id = p.property_id AND s.id = p.session_id
JOIN folio_items fi ON fi.property_id = pi.property_id AND fi.id = pi.folio_item_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.event_id = @event_id
GROUP BY pi.line_id;

-- name: DueSessions :many
-- The sessions the close posts by itself (AUTO): active, of a definite event, dated the day or earlier, not posted. The mode of the event overrides the one of the property.
SELECT s.id AS session_id, s.event_id
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
LEFT JOIN banquet_settings st ON st.property_id = e.property_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status = 'DEFINITE' AND s.status = 'ACTIVE' AND s.session_date <= @day::date
  AND COALESCE(e.posting_mode, st.posting_mode, 'AUTO') = 'AUTO'
  AND NOT EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = s.property_id AND p.session_id = s.id)
ORDER BY s.session_date, s.event_id, s.session_no;

-- name: UnpostedSessions :many
-- The report "sessions not posted" (4.10): active sessions of definite events, dated the business date or earlier, with no posting.
SELECT s.id AS session_id, s.event_id, e.number AS event_number, e.name AS event_name, s.session_no, s.name AS session_name, s.session_date, r.name AS room_name,
       s.pax_estimated, s.pax_guaranteed, s.pax_actual, COALESCE(e.posting_mode, st.posting_mode, 'AUTO')::text AS posting_mode, u.full_name AS sales_name,
       EXISTS (SELECT 1 FROM banquet_event_lines l WHERE l.property_id = s.property_id AND l.session_id = s.id AND l.is_active) AS has_lines,
       COALESCE((SELECT cc.code FROM banquet_event_lines l
          LEFT JOIN banquet_line_components lc ON lc.property_id = l.property_id AND lc.line_id = l.id
          JOIN charge_codes cc ON cc.property_id = l.property_id AND cc.id = COALESCE(lc.charge_code_id, l.charge_code_id)
         WHERE l.property_id = s.property_id AND l.session_id = s.id AND l.is_active AND NOT cc.is_active
         ORDER BY cc.code LIMIT 1), '')::text AS inactive_code
FROM banquet_sessions s
JOIN banquet_events e ON e.property_id = s.property_id AND e.id = s.event_id
JOIN users u ON u.tenant_id = e.tenant_id AND u.id = e.sales_user_id
LEFT JOIN banquet_rooms r ON r.property_id = s.property_id AND r.id = s.room_id
LEFT JOIN banquet_settings st ON st.property_id = e.property_id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status = 'DEFINITE' AND s.status = 'ACTIVE' AND s.session_date <= @day::date
  AND NOT EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = s.property_id AND p.session_id = s.id)
ORDER BY s.session_date, e.number, s.session_no;

-- name: ActiveSessionsUnposted :one
-- How many active sessions of the event have no posting (the event cannot be completed while there are).
SELECT count(*)::int FROM banquet_sessions s
WHERE s.property_id = @property_id AND s.event_id = @event_id AND s.status = 'ACTIVE'
  AND NOT EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = s.property_id AND p.session_id = s.id);

-- ---------------------------------------------------------------- deposit schedule
-- name: ListDepositSchedule :many
SELECT seq, label, due_date, percent, amount FROM banquet_deposit_schedule WHERE property_id = @property_id AND event_id = @event_id ORDER BY seq;

-- name: DeleteDepositSchedule :exec
DELETE FROM banquet_deposit_schedule WHERE property_id = @property_id AND event_id = @event_id;

-- name: InsertDepositRow :exec
INSERT INTO banquet_deposit_schedule (tenant_id, property_id, event_id, seq, label, due_date, percent, amount)
VALUES (@tenant_id, @property_id, @event_id, @seq, @label, @due_date, sqlc.narg(percent), @amount);

-- name: SetDepositAmount :exec
UPDATE banquet_deposit_schedule SET amount = @amount WHERE property_id = @property_id AND event_id = @event_id AND seq = @seq;

-- name: ScheduledEventsDue :many
-- The events with a deposit row due on the day or earlier: the close checks each against the deposits received.
SELECT DISTINCT e.id AS event_id, e.sales_user_id
FROM banquet_events e JOIN banquet_deposit_schedule d ON d.property_id = e.property_id AND d.event_id = e.id
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.status IN ('TENTATIVE', 'DEFINITE') AND d.due_date <= @day::date
ORDER BY e.id;

-- name: InsertDepositNotice :exec
INSERT INTO banquet_notices (tenant_id, property_id, user_id, kind, event_id, notice_date)
VALUES (@tenant_id, @property_id, @user_id, 'DEPOSIT_DUE', @event_id, @notice_date)
ON CONFLICT (property_id, event_id, user_id, notice_date) WHERE kind = 'DEPOSIT_DUE' DO NOTHING;

-- ---------------------------------------------------------------- policy templates and tiers
-- name: ListPolicyTemplates :many
SELECT id, code, name, is_default, base, is_active FROM banquet_policy_templates
WHERE tenant_id = @tenant_id AND property_id = @property_id AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR is_active = sqlc.narg(active)::boolean)
ORDER BY is_default DESC, name, id;

-- name: InsertPolicyTemplate :one
INSERT INTO banquet_policy_templates (tenant_id, property_id, code, name, is_default, base, is_active) VALUES (@tenant_id, @property_id, @code, @name, @is_default, @base, @is_active) RETURNING id;

-- name: UpdatePolicyTemplate :execrows
UPDATE banquet_policy_templates SET name = @name, is_default = @is_default, base = @base, is_active = @is_active WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ClearDefaultTemplate :exec
UPDATE banquet_policy_templates SET is_default = false WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_default AND id <> @except_id;

-- name: DefaultPolicyTemplate :one
SELECT id, base FROM banquet_policy_templates WHERE tenant_id = @tenant_id AND property_id = @property_id AND is_default AND is_active;

-- name: ListTemplateDeposits :many
SELECT template_id, seq, label, days_before, percent FROM banquet_policy_deposits
WHERE property_id = @property_id AND (sqlc.narg(template_id)::bigint IS NULL OR template_id = sqlc.narg(template_id)::bigint) ORDER BY template_id, seq;

-- name: DeleteTemplateDeposits :exec
DELETE FROM banquet_policy_deposits WHERE property_id = @property_id AND template_id = @template_id;

-- name: InsertTemplateDeposit :exec
INSERT INTO banquet_policy_deposits (property_id, template_id, seq, label, days_before, percent) VALUES (@property_id, @template_id, @seq, @label, sqlc.narg(days_before), @percent);

-- name: ListTiers :many
SELECT id, template_id, event_id, days_from, days_to, percent, action FROM banquet_policy_tiers
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(template_id)::bigint IS NULL OR template_id = sqlc.narg(template_id)::bigint)
  AND (sqlc.narg(event_id)::bigint IS NULL OR event_id = sqlc.narg(event_id)::bigint)
ORDER BY template_id, event_id, days_from DESC;

-- name: DeleteTemplateTiers :exec
DELETE FROM banquet_policy_tiers WHERE property_id = @property_id AND template_id = @template_id;

-- name: DeleteEventTiers :exec
DELETE FROM banquet_policy_tiers WHERE property_id = @property_id AND event_id = @event_id;

-- name: InsertTier :exec
INSERT INTO banquet_policy_tiers (tenant_id, property_id, template_id, event_id, days_from, days_to, percent, action)
VALUES (@tenant_id, @property_id, sqlc.narg(template_id), sqlc.narg(event_id), @days_from, sqlc.narg(days_to), @percent, @action);

-- ---------------------------------------------------------------- the event page
-- name: FirstSessionDate :one
SELECT COALESCE(min(session_date), '1900-01-01'::date)::date FROM banquet_sessions WHERE property_id = @property_id AND event_id = @event_id AND status = 'ACTIVE';

-- name: ChargeCodeNames :many
SELECT cc.id, cc.code, cc.gl_account_code, a.name AS gl_account_name
FROM charge_codes cc
LEFT JOIN gl_accounts a ON a.property_id = cc.property_id AND a.code = cc.gl_account_code
WHERE cc.tenant_id = @tenant_id AND cc.property_id = @property_id AND cc.id = ANY(@ids::bigint[]);

-- name: SessionInactiveCode :one
-- The first inactive charge code a session's lines point at, '' when there is none: the close does not post such a session (it stays on the report) instead of failing.
SELECT COALESCE((SELECT cc.code FROM banquet_event_lines l
          LEFT JOIN banquet_line_components lc ON lc.property_id = l.property_id AND lc.line_id = l.id
          JOIN charge_codes cc ON cc.property_id = l.property_id AND cc.id = COALESCE(lc.charge_code_id, l.charge_code_id)
         WHERE l.property_id = @property_id AND l.session_id = @session_id AND l.is_active AND NOT cc.is_active
         ORDER BY cc.code LIMIT 1), '')::text AS code;
