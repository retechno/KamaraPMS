-- POS masters (docs/architecture/34-pos.md, PR 1). Every query is scoped by tenant and property.

-- ---------------------------------------------------------------- outlets
-- name: ListOutlets :many
SELECT o.id, o.code, o.name, o.outlet_type, o.order_mode, o.department_id, d.name AS department_name, o.rule_profile_id, rp.name AS rule_profile_name,
       o.price_list_id, pl.name AS price_list_name, o.drawer, o.receipt_printer_id, pr.name AS receipt_printer_name, o.uses_tables, o.guest_count_required,
       o.rounding_unit, o.allow_room_charge, o.allow_company_charge, o.allow_comp, o.comp_treatment, o.max_discount_pct_no_approval,
       o.block_night_audit_with_open_bills, o.is_active, o.sort_order, o.version,
       o.receipt_title, o.receipt_hotel, o.receipt_address, o.receipt_tax_id, o.receipt_footer, o.receipt_copies
FROM pos_outlets o
JOIN departments d ON d.property_id = o.property_id AND d.id = o.department_id
JOIN pos_rule_profiles rp ON rp.property_id = o.property_id AND rp.id = o.rule_profile_id
JOIN pos_price_lists pl ON pl.property_id = o.property_id AND pl.id = o.price_list_id
LEFT JOIN pos_printers pr ON pr.property_id = o.property_id AND pr.id = o.receipt_printer_id
WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR o.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(active)::boolean IS NULL OR o.is_active = sqlc.narg(active)::boolean)
ORDER BY o.sort_order, o.name, o.id;

-- name: InsertOutlet :one
INSERT INTO pos_outlets (tenant_id, property_id, code, name, outlet_type, order_mode, department_id, rule_profile_id, price_list_id, drawer, receipt_printer_id, uses_tables,
                         guest_count_required, rounding_unit, allow_room_charge, allow_company_charge, allow_comp, comp_treatment, max_discount_pct_no_approval,
                         block_night_audit_with_open_bills, is_active, sort_order, receipt_title, receipt_hotel, receipt_address, receipt_tax_id, receipt_footer,
                         receipt_copies, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @outlet_type, @order_mode, @department_id, @rule_profile_id, @price_list_id, sqlc.narg(drawer), sqlc.narg(receipt_printer_id), @uses_tables,
        @guest_count_required, @rounding_unit, @allow_room_charge, @allow_company_charge, @allow_comp, @comp_treatment, @max_discount_pct_no_approval,
        @block_night_audit_with_open_bills, @is_active, @sort_order, sqlc.narg(receipt_title), sqlc.narg(receipt_hotel), sqlc.narg(receipt_address),
        sqlc.narg(receipt_tax_id), sqlc.narg(receipt_footer), COALESCE(sqlc.narg(receipt_copies)::smallint, 1), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateOutlet :one
UPDATE pos_outlets SET name = @name, outlet_type = @outlet_type, order_mode = @order_mode, department_id = @department_id, rule_profile_id = @rule_profile_id,
       price_list_id = @price_list_id, drawer = sqlc.narg(drawer), receipt_printer_id = sqlc.narg(receipt_printer_id), uses_tables = @uses_tables,
       guest_count_required = @guest_count_required, rounding_unit = @rounding_unit, allow_room_charge = @allow_room_charge, allow_company_charge = @allow_company_charge,
       allow_comp = @allow_comp, comp_treatment = @comp_treatment, max_discount_pct_no_approval = @max_discount_pct_no_approval,
       block_night_audit_with_open_bills = @block_night_audit_with_open_bills, is_active = @is_active, sort_order = @sort_order,
       receipt_title = sqlc.narg(receipt_title), receipt_hotel = sqlc.narg(receipt_hotel), receipt_address = sqlc.narg(receipt_address),
       receipt_tax_id = sqlc.narg(receipt_tax_id), receipt_footer = sqlc.narg(receipt_footer), receipt_copies = COALESCE(sqlc.narg(receipt_copies)::smallint, receipt_copies),
       version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING id;

-- name: ChargeCodeKind :one
SELECT charge_type, is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- printers and stations
-- name: ListPrinters :many
SELECT pr.id, pr.name, pr.kind, pr.connection_info, pr.paper_width_mm, pr.codepage, pr.opens_drawer, pr.is_active,
       pr.device_id, d.name AS device_name, COALESCE(d.status, '')::text AS device_status, pr.last_status, pr.status_reported_at
FROM pos_printers pr LEFT JOIN pos_devices d ON d.property_id = pr.property_id AND d.id = pr.device_id
WHERE pr.tenant_id = @tenant_id AND pr.property_id = @property_id
ORDER BY pr.name, pr.id;

-- name: InsertPrinter :one
INSERT INTO pos_printers (tenant_id, property_id, name, kind, connection_info, paper_width_mm, codepage, opens_drawer, is_active, device_id, created_by, updated_by)
VALUES (@tenant_id, @property_id, @name, @kind, @connection_info, @paper_width_mm, @codepage, @opens_drawer, @is_active, sqlc.narg(device_id), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdatePrinter :execrows
UPDATE pos_printers SET name = @name, kind = @kind, connection_info = @connection_info, paper_width_mm = @paper_width_mm, codepage = @codepage,
       opens_drawer = @opens_drawer, is_active = @is_active, device_id = sqlc.narg(device_id),
       last_status = CASE WHEN device_id IS DISTINCT FROM sqlc.narg(device_id)::bigint THEN NULL ELSE last_status END,
       status_reported_at = CASE WHEN device_id IS DISTINCT FROM sqlc.narg(device_id)::bigint THEN NULL ELSE status_reported_at END,
       updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListStations :many
SELECT s.id, s.code, s.name, s.printer_id, pr.name AS printer_name, s.kds_enabled, s.late_after_minutes, s.is_active, s.ticket_copies
FROM pos_stations s LEFT JOIN pos_printers pr ON pr.property_id = s.property_id AND pr.id = s.printer_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
ORDER BY s.name, s.id;

-- name: InsertStation :one
INSERT INTO pos_stations (tenant_id, property_id, code, name, printer_id, kds_enabled, late_after_minutes, is_active, ticket_copies, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(printer_id), @kds_enabled, @late_after_minutes, @is_active, COALESCE(sqlc.narg(ticket_copies)::smallint, 1),
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateStation :execrows
UPDATE pos_stations SET name = @name, printer_id = sqlc.narg(printer_id), kds_enabled = @kds_enabled, late_after_minutes = @late_after_minutes,
       is_active = @is_active, ticket_copies = COALESCE(sqlc.narg(ticket_copies)::smallint, ticket_copies), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- rule profiles
-- name: ListRuleProfiles :many
SELECT id, code, name, price_mode, is_active FROM pos_rule_profiles
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
ORDER BY name, id;

-- name: ListRuleProfileRules :many
SELECT r.profile_id, r.sequence, r.tax_id, r.service_charge_id,
       COALESCE(t.code, s.code) AS code, COALESCE(t.name, s.name) AS name, COALESCE(t.rate, s.rate) AS rate,
       t.tax_on_service, COALESCE(t.is_active, s.is_active) AS rule_is_active
FROM pos_rule_profile_rules r
LEFT JOIN taxes t ON t.property_id = r.property_id AND t.id = r.tax_id
LEFT JOIN service_charges s ON s.property_id = r.property_id AND s.id = r.service_charge_id
WHERE r.tenant_id = @tenant_id AND r.property_id = @property_id
  AND (sqlc.narg(profile_id)::bigint IS NULL OR r.profile_id = sqlc.narg(profile_id)::bigint)
ORDER BY r.profile_id, r.sequence, r.id;

-- name: InsertRuleProfile :one
INSERT INTO pos_rule_profiles (tenant_id, property_id, code, name, price_mode, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @price_mode, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateRuleProfile :execrows
UPDATE pos_rule_profiles SET name = @name, price_mode = @price_mode, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DeleteRuleProfileRules :exec
DELETE FROM pos_rule_profile_rules WHERE tenant_id = @tenant_id AND property_id = @property_id AND profile_id = @profile_id;

-- name: InsertRuleProfileRule :exec
INSERT INTO pos_rule_profile_rules (tenant_id, property_id, profile_id, tax_id, service_charge_id, sequence)
VALUES (@tenant_id, @property_id, @profile_id, sqlc.narg(tax_id), sqlc.narg(service_charge_id), @sequence);

-- ---------------------------------------------------------------- price lists
-- name: ListPriceLists :many
SELECT l.id, l.code, l.name, l.is_active, (SELECT count(*) FROM pos_price_list_items i WHERE i.property_id = l.property_id AND i.price_list_id = l.id) AS item_count
FROM pos_price_lists l WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id
ORDER BY l.name, l.id;

-- name: InsertPriceList :one
INSERT INTO pos_price_lists (tenant_id, property_id, code, name, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdatePriceList :execrows
UPDATE pos_price_lists SET name = @name, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListPriceListItems :many
SELECT price_list_id, product_id, price, is_sold, station_id
FROM pos_price_list_items WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(price_list_id)::bigint IS NULL OR price_list_id = sqlc.narg(price_list_id)::bigint)
  AND (sqlc.narg(product_id)::bigint IS NULL OR product_id = sqlc.narg(product_id)::bigint)
ORDER BY price_list_id, product_id;

-- name: UpsertPriceListItem :exec
INSERT INTO pos_price_list_items (tenant_id, property_id, price_list_id, product_id, price, is_sold, station_id, updated_by)
VALUES (@tenant_id, @property_id, @price_list_id, @product_id, @price, @is_sold, sqlc.narg(station_id), sqlc.narg(actor_id))
ON CONFLICT (price_list_id, product_id) DO UPDATE SET price = EXCLUDED.price, is_sold = EXCLUDED.is_sold, station_id = EXCLUDED.station_id,
    updated_at = now(), updated_by = EXCLUDED.updated_by;

-- name: DeletePriceListItem :exec
DELETE FROM pos_price_list_items WHERE tenant_id = @tenant_id AND property_id = @property_id AND price_list_id = @price_list_id AND product_id = @product_id;

-- ---------------------------------------------------------------- categories
-- name: ListCategories :many
SELECT c.id, c.code, c.name, c.parent_id, c.sort_order, c.is_active,
       (SELECT count(*) FROM pos_products p WHERE p.property_id = c.property_id AND p.category_id = c.id) AS product_count
FROM pos_categories c WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id
ORDER BY c.sort_order, c.name, c.id;

-- name: InsertCategory :one
INSERT INTO pos_categories (tenant_id, property_id, code, name, parent_id, sort_order, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(parent_id), @sort_order, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateCategory :execrows
UPDATE pos_categories SET name = @name, sort_order = @sort_order, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- products
-- name: ListProducts :many
SELECT p.id, p.code, p.name, p.short_name, p.category_id, c.name AS category_name, p.charge_code_id, cc.code AS charge_code, cc.name AS charge_code_name,
       p.rule_profile_id, p.station_id, p.is_active, p.version
FROM pos_products p
JOIN pos_categories c ON c.property_id = p.property_id AND c.id = p.category_id
JOIN charge_codes cc ON cc.property_id = p.property_id AND cc.id = p.charge_code_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id
  AND (sqlc.narg(id)::bigint IS NULL OR p.id = sqlc.narg(id)::bigint)
  AND (sqlc.narg(code)::text IS NULL OR p.code = sqlc.narg(code)::text)
  AND (sqlc.narg(active)::boolean IS NULL OR p.is_active = sqlc.narg(active)::boolean)
ORDER BY c.sort_order, c.name, p.name, p.id;

-- name: InsertProduct :one
INSERT INTO pos_products (tenant_id, property_id, code, name, short_name, category_id, charge_code_id, rule_profile_id, station_id, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(short_name), @category_id, @charge_code_id, sqlc.narg(rule_profile_id), sqlc.narg(station_id), @is_active,
        sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateProduct :execrows
UPDATE pos_products SET name = @name, short_name = sqlc.narg(short_name), category_id = @category_id, charge_code_id = @charge_code_id,
       rule_profile_id = sqlc.narg(rule_profile_id), station_id = sqlc.narg(station_id), is_active = @is_active, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version;

-- name: ListOptionGroups :many
SELECT id, product_id, name, kind, min_select, max_select, sort_order FROM pos_option_groups
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(product_id)::bigint IS NULL OR product_id = sqlc.narg(product_id)::bigint)
ORDER BY product_id, sort_order, id;

-- name: ListOptions :many
SELECT o.id, o.group_id, g.product_id, o.name, o.price_delta, o.is_default, o.max_qty, o.sort_order, o.is_active
FROM pos_options o JOIN pos_option_groups g ON g.property_id = o.property_id AND g.id = o.group_id
WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id
  AND (sqlc.narg(product_id)::bigint IS NULL OR g.product_id = sqlc.narg(product_id)::bigint)
ORDER BY g.product_id, g.sort_order, g.id, o.sort_order, o.id;

-- name: DeleteProductOptions :exec
DELETE FROM pos_options o USING pos_option_groups g
WHERE g.property_id = o.property_id AND g.id = o.group_id AND g.tenant_id = @tenant_id AND g.property_id = @property_id AND g.product_id = @product_id;

-- name: DeleteProductRecipeOptionLines :exec
DELETE FROM pos_recipe_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND product_id = @product_id AND option_id IS NOT NULL;

-- name: DeleteProductOptionGroups :exec
DELETE FROM pos_option_groups WHERE tenant_id = @tenant_id AND property_id = @property_id AND product_id = @product_id;

-- name: InsertOptionGroup :one
INSERT INTO pos_option_groups (tenant_id, property_id, product_id, name, kind, min_select, max_select, sort_order)
VALUES (@tenant_id, @property_id, @product_id, @name, @kind, @min_select, @max_select, @sort_order)
RETURNING id;

-- name: InsertOption :exec
INSERT INTO pos_options (tenant_id, property_id, group_id, name, price_delta, is_default, max_qty, sort_order, is_active)
VALUES (@tenant_id, @property_id, @group_id, @name, @price_delta, @is_default, @max_qty, @sort_order, @is_active);

-- name: ListRecipeLines :many
SELECT l.product_id, l.option_id, l.quantity, l.unit, l.note FROM pos_recipe_lines l
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.product_id = @product_id ORDER BY l.id;

-- ---------------------------------------------------------------- sold out today
-- name: ListSoldOut :many
SELECT product_id FROM pos_outlet_sold_out
WHERE tenant_id = @tenant_id AND property_id = @property_id AND outlet_id = @outlet_id AND business_date = @business_date
ORDER BY product_id;

-- name: MarkSoldOut :exec
INSERT INTO pos_outlet_sold_out (tenant_id, property_id, outlet_id, product_id, business_date, marked_by)
VALUES (@tenant_id, @property_id, @outlet_id, @product_id, @business_date, sqlc.narg(actor_id))
ON CONFLICT (outlet_id, product_id) DO UPDATE SET business_date = EXCLUDED.business_date, marked_by = EXCLUDED.marked_by, marked_at = now();

-- name: ClearSoldOut :exec
DELETE FROM pos_outlet_sold_out WHERE tenant_id = @tenant_id AND property_id = @property_id AND outlet_id = @outlet_id AND product_id = @product_id;

-- ---------------------------------------------------------------- floor plan
-- name: ListAreas :many
SELECT id, outlet_id, name, sort_order, grid_w, grid_h, layout_version, is_active FROM pos_areas
WHERE tenant_id = @tenant_id AND property_id = @property_id
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(id)::bigint IS NULL OR id = sqlc.narg(id)::bigint)
ORDER BY outlet_id, sort_order, id;

-- name: InsertArea :one
INSERT INTO pos_areas (tenant_id, property_id, outlet_id, name, sort_order, grid_w, grid_h, created_by, updated_by)
VALUES (@tenant_id, @property_id, @outlet_id, @name, @sort_order, @grid_w, @grid_h, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: LockArea :one
SELECT layout_version FROM pos_areas WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id FOR UPDATE;

-- name: UpdateArea :exec
UPDATE pos_areas SET name = @name, sort_order = @sort_order, grid_w = @grid_w, grid_h = @grid_h, is_active = @is_active,
       layout_version = layout_version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListTables :many
SELECT t.id, t.area_id, t.code, t.capacity, t.shape, t.x, t.y, t.w, t.h, t.is_active
FROM pos_tables t JOIN pos_areas a ON a.property_id = t.property_id AND a.id = t.area_id
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR a.outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(area_id)::bigint IS NULL OR t.area_id = sqlc.narg(area_id)::bigint)
ORDER BY t.area_id, t.code, t.id;

-- name: InsertTable :one
INSERT INTO pos_tables (tenant_id, property_id, area_id, code, capacity, shape, x, y, w, h, is_active)
VALUES (@tenant_id, @property_id, @area_id, @code, @capacity, @shape, @x, @y, @w, @h, @is_active)
RETURNING id;

-- name: UpdateTable :execrows
UPDATE pos_tables SET code = @code, capacity = @capacity, shape = @shape, x = @x, y = @y, w = @w, h = @h, is_active = @is_active
WHERE tenant_id = @tenant_id AND property_id = @property_id AND area_id = @area_id AND id = @id;

-- name: DeactivateTables :exec
UPDATE pos_tables SET is_active = false
WHERE tenant_id = @tenant_id AND property_id = @property_id AND area_id = @area_id AND NOT (id = ANY(@keep::bigint[]));

-- name: ListMarks :many
SELECT m.id, m.area_id, m.kind, m.label, m.x, m.y, m.w, m.h
FROM pos_area_marks m JOIN pos_areas a ON a.property_id = m.property_id AND a.id = m.area_id
WHERE m.tenant_id = @tenant_id AND m.property_id = @property_id
  AND (sqlc.narg(outlet_id)::bigint IS NULL OR a.outlet_id = sqlc.narg(outlet_id)::bigint)
  AND (sqlc.narg(area_id)::bigint IS NULL OR m.area_id = sqlc.narg(area_id)::bigint)
ORDER BY m.area_id, m.id;

-- name: DeleteMarks :exec
DELETE FROM pos_area_marks WHERE tenant_id = @tenant_id AND property_id = @property_id AND area_id = @area_id;

-- name: InsertMark :exec
INSERT INTO pos_area_marks (tenant_id, property_id, area_id, kind, label, x, y, w, h)
VALUES (@tenant_id, @property_id, @area_id, @kind, @label, @x, @y, @w, @h);

-- ---------------------------------------------------------------- the check of the setup
-- Outlets whose order mode needs a station with a printer or a screen, and whose products have none.
-- name: SetupCheckStations :many
SELECT o.id AS outlet_id, o.code AS outlet_code, o.order_mode, p.id AS product_id, p.code AS product_code, s.id AS station_id, s.code AS station_code,
       COALESCE(s.is_active, false)::boolean AS station_active, (s.printer_id IS NOT NULL)::boolean AS station_has_printer, COALESCE(s.kds_enabled, false)::boolean AS station_has_kds
FROM pos_outlets o
JOIN pos_price_list_items i ON i.property_id = o.property_id AND i.price_list_id = o.price_list_id AND i.is_sold
JOIN pos_products p ON p.property_id = i.property_id AND p.id = i.product_id AND p.is_active
LEFT JOIN pos_stations s ON s.property_id = p.property_id AND s.id = COALESCE(i.station_id, p.station_id)
WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.is_active AND o.order_mode IN ('PRINT', 'KDS')
  AND (s.id IS NULL OR NOT s.is_active OR (o.order_mode = 'PRINT' AND s.printer_id IS NULL) OR (o.order_mode = 'KDS' AND NOT s.kds_enabled))
ORDER BY o.code, p.code;

-- Active outlets with nothing to sell, and outlets that use tables and have none.
-- name: SetupCheckOutlets :many
SELECT o.id AS outlet_id, o.code AS outlet_code,
       (SELECT count(*) FROM pos_price_list_items i JOIN pos_products p ON p.property_id = i.property_id AND p.id = i.product_id AND p.is_active
         WHERE i.property_id = o.property_id AND i.price_list_id = o.price_list_id AND i.is_sold) AS sold_products,
       (SELECT count(*) FROM pos_tables t JOIN pos_areas a ON a.property_id = t.property_id AND a.id = t.area_id
         WHERE a.property_id = o.property_id AND a.outlet_id = o.id AND t.is_active) AS tables, o.uses_tables, (o.drawer IS NULL)::boolean AS no_drawer, (o.receipt_printer_id IS NULL)::boolean AS no_printer
FROM pos_outlets o WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.is_active
ORDER BY o.code;

-- The charge codes of the property by code, for the CSV import of products.
-- name: ListChargeCodeRefs :many
SELECT id, code, charge_type, is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id;

-- ================================================================ PR 2: bills, lines, rounds, tickets, holds (docs/architecture/34-pos.md 3.3, 5)

-- The next number of the bills of an outlet: the sequence of the outlet, the last lock of the transaction (level 50).
-- name: NextBillNumber :one
UPDATE pos_outlets SET next_bill_no = next_bill_no + 1
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id
RETURNING (next_bill_no - 1)::bigint AS number, code;

-- name: InsertBill :one
INSERT INTO pos_bills (tenant_id, property_id, outlet_id, client_uuid, folio_id, bill_number, order_type, table_id, guest_count, target_stay_id, customer_name, waiter_id,
                       comp_treatment, business_date_opened, opened_at, note, created_by, updated_by)
VALUES (@tenant_id, @property_id, @outlet_id, @client_uuid, sqlc.narg(folio_id), sqlc.narg(bill_number), @order_type, sqlc.narg(table_id), sqlc.narg(guest_count),
        sqlc.narg(target_stay_id), sqlc.narg(customer_name), sqlc.narg(actor_id), @comp_treatment, @business_date_opened, @opened_at, sqlc.narg(note), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: GetBill :one
SELECT b.id, b.outlet_id, b.client_uuid, b.folio_id, b.bill_number, b.status, b.order_type, b.table_id, b.guest_count, b.target_stay_id, b.customer_name, b.waiter_id,
       b.comp_treatment, b.business_date_opened, b.opened_at, b.bill_requested_at, b.closed_at, b.closed_business_date, b.merged_into_bill_id, b.note, b.version,
       COALESCE(t.code, '')::text AS table_code, COALESCE(u.full_name, '')::text AS waiter_name,
       COALESCE((SELECT r.room_number FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
                  WHERE sr.property_id = b.property_id AND sr.stay_id = b.target_stay_id ORDER BY sr.id DESC LIMIT 1), '')::text AS target_room
FROM pos_bills b
LEFT JOIN pos_tables t ON t.property_id = b.property_id AND t.id = b.table_id
LEFT JOIN users u ON u.id = b.waiter_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.id = @id;

-- name: GetBillByUUID :one
SELECT id FROM pos_bills WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: GetBillOfFolio :one
SELECT id FROM pos_bills WHERE tenant_id = @tenant_id AND property_id = @property_id AND folio_id = @folio_id;

-- name: UpdateBillMeta :execrows
UPDATE pos_bills SET table_id = sqlc.narg(table_id), order_type = @order_type, guest_count = sqlc.narg(guest_count), customer_name = sqlc.narg(customer_name), note = sqlc.narg(note),
       version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'OPEN';

-- name: SetBillRequested :exec
UPDATE pos_bills SET bill_requested_at = @at::timestamptz, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'OPEN';

-- name: CloseBill :execrows
UPDATE pos_bills SET status = @status, closed_at = @at::timestamptz, closed_business_date = @business_date, merged_into_bill_id = sqlc.narg(merged_into_bill_id),
       version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'OPEN';

-- name: ReopenBill :execrows
UPDATE pos_bills SET status = 'OPEN', closed_at = NULL, closed_business_date = NULL, bill_requested_at = NULL, version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'CLOSED';

-- The bills of an outlet with what a list shows. The balance of an open bill is the balance of its folio; a draft line is not in it (the screen adds the quote of the drafts).
-- name: ListBills :many
SELECT b.id, b.client_uuid, b.folio_id, b.bill_number, b.status, b.order_type, b.table_id, b.guest_count, b.target_stay_id, b.customer_name, b.waiter_id,
       b.opened_at, b.bill_requested_at, b.closed_at, b.business_date_opened, b.closed_business_date, b.version,
       COALESCE(t.code, '')::text AS table_code, COALESCE(u.full_name, '')::text AS waiter_name,
       COALESCE((SELECT sum(i.debit) - sum(i.credit) FROM folio_items i WHERE i.property_id = b.property_id AND i.folio_id = b.folio_id), 0)::numeric AS balance,
       COALESCE((SELECT sum(i.debit) FROM folio_items i WHERE i.property_id = b.property_id AND i.folio_id = b.folio_id AND i.transaction_type = 'CHARGE'), 0)::numeric AS sold_debit,
       (SELECT count(*) FROM pos_bill_lines l WHERE l.property_id = b.property_id AND l.bill_id = b.id AND l.status = 'DRAFT')::int AS draft_lines,
       (SELECT count(*) FROM pos_bill_lines l WHERE l.property_id = b.property_id AND l.bill_id = b.id AND l.status = 'SENT')::int AS sent_lines,
       COALESCE((SELECT r.room_number FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
                  WHERE sr.property_id = b.property_id AND sr.stay_id = b.target_stay_id ORDER BY sr.id DESC LIMIT 1), '')::text AS target_room
FROM pos_bills b
LEFT JOIN pos_tables t ON t.property_id = b.property_id AND t.id = b.table_id
LEFT JOIN users u ON u.id = b.waiter_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.outlet_id = @outlet_id
  AND (sqlc.narg(status)::text IS NULL OR b.status = sqlc.narg(status)::text)
  AND (sqlc.narg(table_id)::bigint IS NULL OR b.table_id = sqlc.narg(table_id)::bigint)
  AND (sqlc.narg(day)::date IS NULL OR b.business_date_opened = sqlc.narg(day)::date OR b.closed_business_date = sqlc.narg(day)::date)
  AND (sqlc.narg(q)::text IS NULL OR b.bill_number ILIKE '%' || sqlc.narg(q)::text || '%' OR COALESCE(t.code, '') ILIKE '%' || sqlc.narg(q)::text || '%'
       OR COALESCE(b.customer_name, '') ILIKE '%' || sqlc.narg(q)::text || '%'
       OR EXISTS (SELECT 1 FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
                   WHERE sr.property_id = b.property_id AND sr.stay_id = b.target_stay_id AND r.room_number ILIKE '%' || sqlc.narg(q)::text || '%'))
ORDER BY b.id DESC
LIMIT @row_limit;

-- ---------------------------------------------------------------- lines
-- name: InsertBillLine :one
INSERT INTO pos_bill_lines (tenant_id, property_id, bill_id, client_uuid, product_id, product_name, charge_code_id, rule_profile_id, options, quantity, unit_price,
                            discount_pct, discount_amount, note, station_id, created_by)
VALUES (@tenant_id, @property_id, @bill_id, @client_uuid, @product_id, @product_name, @charge_code_id, sqlc.narg(rule_profile_id), @options::jsonb, @quantity, @unit_price,
        sqlc.narg(discount_pct), @discount_amount, sqlc.narg(note), sqlc.narg(station_id), sqlc.narg(actor_id))
RETURNING id;

-- name: ListBillLines :many
SELECT l.id, l.bill_id, l.client_uuid, l.product_id, l.product_name, l.charge_code_id, l.rule_profile_id, l.options, l.quantity, l.unit_price, l.discount_pct, l.discount_amount,
       l.discount_reason, l.is_comp, l.note, l.station_id, l.status, l.round_id, l.folio_item_id, l.voided_item_id, l.created_by, l.created_at, l.sent_by, l.sent_at,
       l.voided_by, l.voided_at, l.void_approved_by, l.void_reason, COALESCE(r.round_no, 0)::int AS round_no,
       COALESCE(i.base_amount, 0)::numeric AS item_base, COALESCE(i.discount_amount, 0)::numeric AS item_discount, COALESCE(i.net_amount, 0)::numeric AS item_net,
       COALESCE(i.service_charge_total, 0)::numeric AS item_service, COALESCE(i.tax_total, 0)::numeric AS item_tax, COALESCE(i.debit - i.credit, 0)::numeric AS item_total,
       COALESCE((SELECT t.status FROM pos_ticket_lines tl JOIN pos_tickets t ON t.property_id = tl.property_id AND t.id = tl.ticket_id
                  WHERE tl.property_id = l.property_id AND tl.bill_line_id = l.id AND NOT tl.is_void ORDER BY t.id DESC LIMIT 1), '')::text AS ticket_status,
       COALESCE((SELECT sum(a.net_amount) FROM folio_items a WHERE a.property_id = l.property_id AND a.reference_type = 'POS_BILL_LINE' AND a.reference_id = l.client_uuid::text
                  AND a.transaction_type = 'ADJUSTMENT'), 0)::numeric AS adjusted_net,
       COALESCE(vu.full_name, '')::text AS voided_by_name, COALESCE(au.full_name, '')::text AS approved_by_name, COALESCE(cu.full_name, '')::text AS created_by_name
FROM pos_bill_lines l
LEFT JOIN pos_rounds r ON r.property_id = l.property_id AND r.id = l.round_id
LEFT JOIN folio_items i ON i.property_id = l.property_id AND i.id = l.folio_item_id
LEFT JOIN users vu ON vu.id = l.voided_by
LEFT JOIN users au ON au.id = l.void_approved_by
LEFT JOIN users cu ON cu.id = l.created_by
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.bill_id = @bill_id
ORDER BY l.created_at, l.client_uuid;

-- name: GetBillLine :one
SELECT id, bill_id, client_uuid, product_id, product_name, charge_code_id, rule_profile_id, options, quantity, unit_price, discount_pct, discount_amount, discount_reason, is_comp,
       note, station_id, status, round_id, folio_item_id, voided_item_id
FROM pos_bill_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: GetBillLineByUUID :one
SELECT id FROM pos_bill_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND bill_id = @bill_id AND client_uuid = @client_uuid;

-- name: UpdateDraftLine :execrows
UPDATE pos_bill_lines SET quantity = @quantity, options = @options::jsonb, unit_price = @unit_price, note = sqlc.narg(note)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND bill_id = @bill_id AND status = 'DRAFT';

-- name: SetLineDiscount :execrows
UPDATE pos_bill_lines SET discount_pct = sqlc.narg(discount_pct), discount_amount = @discount_amount, discount_reason = sqlc.narg(discount_reason), discount_by = sqlc.narg(actor_id), is_comp = @is_comp
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND bill_id = @bill_id AND status = 'DRAFT';

-- name: DeleteDraftLine :execrows
DELETE FROM pos_bill_lines WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND bill_id = @bill_id AND status = 'DRAFT';

-- name: MarkLineSent :exec
UPDATE pos_bill_lines SET status = 'SENT', round_id = @round_id, folio_item_id = @folio_item_id, sent_by = sqlc.narg(actor_id), sent_at = @at::timestamptz
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'DRAFT';

-- name: MarkLineVoid :exec
UPDATE pos_bill_lines SET status = 'VOID', voided_item_id = @voided_item_id, voided_by = sqlc.narg(actor_id), voided_at = @at::timestamptz, void_approved_by = sqlc.narg(approved_by),
       void_reason = @reason
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'SENT';

-- The line moves to another bill (merge, split); a sent line follows its copy on the folio of the other bill.
-- name: MoveLineToBill :exec
UPDATE pos_bill_lines SET bill_id = @bill_id, folio_item_id = COALESCE(sqlc.narg(folio_item_id)::bigint, folio_item_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- rounds and tickets
-- name: NextRoundNo :one
SELECT (COALESCE(max(round_no), 0) + 1)::int AS round_no FROM pos_rounds WHERE property_id = @property_id AND bill_id = @bill_id;

-- name: InsertRound :one
INSERT INTO pos_rounds (tenant_id, property_id, bill_id, round_no, client_uuid, sent_by)
VALUES (@tenant_id, @property_id, @bill_id, @round_no, @client_uuid, sqlc.narg(actor_id))
RETURNING id;

-- name: GetRoundByUUID :one
SELECT id, bill_id, round_no FROM pos_rounds WHERE tenant_id = @tenant_id AND property_id = @property_id AND client_uuid = @client_uuid;

-- name: InsertTicket :one
INSERT INTO pos_tickets (tenant_id, property_id, round_id, bill_id, station_id, kind)
VALUES (@tenant_id, @property_id, @round_id, @bill_id, sqlc.narg(station_id), @kind)
RETURNING id;

-- name: InsertTicketLine :exec
INSERT INTO pos_ticket_lines (tenant_id, property_id, ticket_id, bill_line_id, quantity, name, options_text, note, is_void)
VALUES (@tenant_id, @property_id, @ticket_id, @bill_line_id, @quantity, @name, sqlc.narg(options_text), sqlc.narg(note), @is_void);

-- A line voided before its ticket was served: the ticket line is struck through.
-- name: StrikeTicketLines :exec
UPDATE pos_ticket_lines SET is_void = true WHERE tenant_id = @tenant_id AND property_id = @property_id AND bill_line_id = @bill_line_id AND NOT is_void;

-- ---------------------------------------------------------------- holds
-- name: ListHolds :many
SELECT h.id, h.table_id, t.code AS table_code, h.business_date, h.from_time, h.party_size, h.name, h.phone, h.note, h.status
FROM pos_table_holds h JOIN pos_tables t ON t.property_id = h.property_id AND t.id = h.table_id JOIN pos_areas a ON a.property_id = t.property_id AND a.id = t.area_id
WHERE h.tenant_id = @tenant_id AND h.property_id = @property_id AND a.outlet_id = @outlet_id AND h.business_date = @business_date AND h.status = 'HELD'
ORDER BY h.from_time, h.id;

-- name: InsertHold :one
INSERT INTO pos_table_holds (tenant_id, property_id, table_id, business_date, from_time, party_size, name, phone, note, created_by, updated_by)
VALUES (@tenant_id, @property_id, @table_id, @business_date, @from_time::time, sqlc.narg(party_size), @name, sqlc.narg(phone), sqlc.narg(note), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: SetHoldStatus :execrows
UPDATE pos_table_holds SET status = @status, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'HELD';

-- name: GetTableOutlet :one
SELECT t.id, t.code, t.is_active, a.outlet_id FROM pos_tables t JOIN pos_areas a ON a.property_id = t.property_id AND a.id = t.area_id
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.id = @id;

-- ---------------------------------------------------------------- the product as sold at an outlet (the price list of the outlet)
-- name: GetSellableProduct :one
SELECT p.id, p.name, p.short_name, p.charge_code_id, p.rule_profile_id, p.is_active, i.station_id AS list_station_id, p.station_id AS product_station_id,
       i.price, COALESCE(i.is_sold, false)::boolean AS is_sold,
       EXISTS (SELECT 1 FROM pos_outlet_sold_out so WHERE so.property_id = p.property_id AND so.outlet_id = @outlet_id AND so.product_id = p.id AND so.business_date = @business_date)::boolean AS sold_out
FROM pos_products p
JOIN pos_outlets o ON o.property_id = p.property_id AND o.id = @outlet_id
LEFT JOIN pos_price_list_items i ON i.property_id = p.property_id AND i.price_list_id = o.price_list_id AND i.product_id = p.id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.id = @product_id;

-- The rooms an outlet may look up: in-house stays whose room matches the number or the guest's name.
-- name: LookupRooms :many
SELECT s.id AS stay_id, r.room_number, btrim(COALESCE(g.first_name, '') || ' ' || g.last_name)::text AS guest_name
FROM stays s
JOIN stay_rooms sr ON sr.property_id = s.property_id AND sr.stay_id = s.id AND sr.check_out_at IS NULL
JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.status = 'OPEN'
  AND (r.room_number ILIKE @q::text || '%' OR btrim(COALESCE(g.first_name, '') || ' ' || g.last_name) ILIKE '%' || @q::text || '%')
ORDER BY r.room_number
LIMIT 20;

-- What a bill has sold, rounded, been paid and charged to a room, read from its folio (the ledger is the one source of the amounts).
-- name: BillLedgerSums :one
SELECT
  COALESCE(sum(i.debit - i.credit) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')), 0)::numeric AS sold,
  COALESCE(sum(i.debit - i.credit) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND c.charge_type = 'ROUNDING'), 0)::numeric AS rounding,
  COALESCE(sum(i.credit - i.debit) FILTER (WHERE i.transaction_type IN ('PAYMENT', 'REFUND')), 0)::numeric AS paid,
  COALESCE(sum(i.credit - i.debit) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND c.charge_type = 'TRANSFER'), 0)::numeric AS transferred,
  COALESCE(sum(i.base_amount) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')), 0)::numeric AS base,
  COALESCE(sum(i.net_amount) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')), 0)::numeric AS net,
  COALESCE(sum(i.service_charge_total) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')), 0)::numeric AS service,
  COALESCE(sum(i.tax_total) FILTER (WHERE i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') NOT IN ('TRANSFER', 'ROUNDING')), 0)::numeric AS tax
FROM folio_items i LEFT JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
WHERE i.property_id = @property_id AND i.folio_id = @folio_id;

-- name: ListBillRoomCharges :many
SELECT c.id, c.amount, c.to_folio_id, c.stay_id, c.returned_at, COALESCE(r.room_number, '')::text AS room_number
FROM pos_room_charges c
LEFT JOIN stay_rooms sr ON sr.property_id = c.property_id AND sr.stay_id = c.stay_id AND sr.id = (SELECT max(x.id) FROM stay_rooms x WHERE x.property_id = c.property_id AND x.stay_id = c.stay_id)
LEFT JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.bill_id = sqlc.narg(bill_id)
ORDER BY c.id;

-- name: MarkLineComp :exec
UPDATE pos_bill_lines SET is_comp = true, discount_reason = sqlc.narg(reason), discount_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'SENT';

-- name: GetRoomChargeForBill :one
SELECT id, bill_id, from_folio_id, to_folio_id, stay_id, amount, returned_at FROM pos_room_charges WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ================================================================ reports of an outlet (34-pos.md 13.2): everything is read from the ledger of the bills' folios, never a second set of totals

-- Sales, service and tax of each business day: the lines the bills sold less what was voided and discounted after, without the lines of the transfers between folios (a split or a merge
-- changes nothing of what was sold) and without the rounding, which has its own column.
-- name: ReportDays :many
SELECT i.business_date,
       COALESCE(sum(i.net_amount) FILTER (WHERE COALESCE(c.charge_type, '') <> 'ROUNDING'), 0)::numeric AS net,
       COALESCE(sum(i.service_charge_total) FILTER (WHERE COALESCE(c.charge_type, '') <> 'ROUNDING'), 0)::numeric AS service,
       COALESCE(sum(i.tax_total) FILTER (WHERE COALESCE(c.charge_type, '') <> 'ROUNDING'), 0)::numeric AS tax,
       COALESCE(sum(i.debit - i.credit) FILTER (WHERE c.charge_type = 'ROUNDING'), 0)::numeric AS rounding,
       COALESCE(sum(i.debit - i.credit) FILTER (WHERE COALESCE(c.charge_type, '') <> 'ROUNDING'), 0)::numeric AS total,
       count(DISTINCT i.folio_id) FILTER (WHERE i.transaction_type = 'CHARGE' AND i.source = 'POS')::int AS bills
FROM folio_items i
JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id AND f.folio_type = 'OUTLET' AND f.outlet_id = @outlet_id
LEFT JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.business_date BETWEEN @from_date AND @to_date
  AND i.transaction_type IN ('CHARGE', 'ADJUSTMENT', 'REVERSAL') AND COALESCE(c.charge_type, '') <> 'TRANSFER'
  AND NOT (i.source = 'TRANSFER' OR (i.reference_type = 'FOLIO_TRANSFER'))
GROUP BY i.business_date
ORDER BY i.business_date;

-- name: ReportProducts :many
SELECT l.product_id, l.product_name, COALESCE(cat.name, '')::text AS category, sum(l.quantity)::numeric AS quantity,
       sum(i.base_amount)::numeric AS base, sum(i.discount_amount)::numeric AS discount, sum(i.net_amount)::numeric AS net, sum(i.service_charge_total)::numeric AS service,
       sum(i.tax_total)::numeric AS tax, sum(i.debit - i.credit)::numeric AS total
FROM pos_bill_lines l
JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id AND b.outlet_id = @outlet_id
JOIN folio_items i ON i.property_id = l.property_id AND i.id = COALESCE(l.voided_item_id, l.folio_item_id)
LEFT JOIN pos_products p ON p.property_id = l.property_id AND p.id = l.product_id
LEFT JOIN pos_categories cat ON cat.property_id = p.property_id AND cat.id = p.category_id
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'SENT' AND i.service_date BETWEEN @from_date AND @to_date
GROUP BY l.product_id, l.product_name, cat.name
ORDER BY sum(i.debit - i.credit) DESC, l.product_name;

-- name: ReportMethods :many
SELECT p.payment_method AS method, count(*)::int AS payments, COALESCE(sum(p.amount), 0)::numeric AS amount
FROM payments p JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id AND f.folio_type = 'OUTLET' AND f.outlet_id = @outlet_id
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date BETWEEN @from_date AND @to_date AND p.payment_type = 'PAYMENT' AND p.status = 'POSTED'
GROUP BY p.payment_method
ORDER BY p.payment_method;

-- name: ReportRoomCharges :one
SELECT count(*) FILTER (WHERE c.returned_at IS NULL)::int AS charges, COALESCE(sum(c.amount) FILTER (WHERE c.returned_at IS NULL), 0)::numeric AS amount,
       count(*) FILTER (WHERE c.returned_at IS NOT NULL)::int AS returned
FROM pos_room_charges c
WHERE c.tenant_id = @tenant_id AND c.property_id = @property_id AND c.outlet_id = @outlet_id AND c.business_date BETWEEN @from_date AND @to_date;

-- name: ReportVoids :many
SELECT l.id, COALESCE(b.bill_number, '')::text AS bill_number, l.product_name, l.quantity, (i.debit - i.credit)::numeric AS amount, l.void_reason,
       COALESCE(vu.full_name, '')::text AS voided_by, COALESCE(au.full_name, '')::text AS approved_by, v.business_date
FROM pos_bill_lines l
JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id AND b.outlet_id = @outlet_id
JOIN folio_items v ON v.property_id = l.property_id AND v.id = l.voided_item_id
JOIN folio_items i ON i.property_id = l.property_id AND i.id = v.reverses_item_id
LEFT JOIN users vu ON vu.id = l.voided_by
LEFT JOIN users au ON au.id = l.void_approved_by
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'VOID' AND v.business_date BETWEEN @from_date AND @to_date
ORDER BY v.business_date, l.id;

-- Discounts after the send are adjustments; discounts before it are on the line itself.
-- name: ReportDiscounts :many
SELECT COALESCE(b.bill_number, '')::text AS bill_number, i.description, (i.base_amount - i.discount_amount)::numeric AS amount, COALESCE(i.reason, '')::text AS reason,
       COALESCE(cu.full_name, '')::text AS given_by, COALESCE(au.full_name, '')::text AS approved_by, i.business_date, 'ADJUSTMENT'::text AS kind
FROM folio_items i
JOIN folios f ON f.property_id = i.property_id AND f.id = i.folio_id AND f.folio_type = 'OUTLET' AND f.outlet_id = @outlet_id
LEFT JOIN pos_bills b ON b.property_id = f.property_id AND b.folio_id = f.id
LEFT JOIN users cu ON cu.id = i.created_by
LEFT JOIN users au ON au.id = i.approved_by
WHERE i.tenant_id = @tenant_id AND i.property_id = @property_id AND i.transaction_type = 'ADJUSTMENT' AND i.source = 'POS' AND i.description LIKE 'Discount: %'
  AND i.business_date BETWEEN @from_date AND @to_date
UNION ALL
SELECT COALESCE(b.bill_number, '')::text, l.product_name, i.discount_amount::numeric, COALESCE(l.discount_reason, '')::text, COALESCE(cu.full_name, '')::text, ''::text, i.business_date, 'LINE'::text
FROM pos_bill_lines l
JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id AND b.outlet_id = @outlet_id
JOIN folio_items i ON i.property_id = l.property_id AND i.id = l.folio_item_id
LEFT JOIN users cu ON cu.id = l.discount_by
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status IN ('SENT', 'VOID') AND i.discount_amount > 0 AND NOT l.is_comp AND i.business_date BETWEEN @from_date AND @to_date
ORDER BY 7, 1;

-- Complimentary: the payments of the method COMP and the lines made complimentary by a 100% discount.
-- name: ReportComps :many
SELECT COALESCE(b.bill_number, '')::text AS bill_number, p.amount::numeric AS amount, COALESCE(p.remarks, '')::text AS remarks, COALESCE(au.full_name, '')::text AS approved_by,
       p.business_date, 'PAYMENT_COMP'::text AS treatment
FROM payments p
JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id AND f.folio_type = 'OUTLET' AND f.outlet_id = @outlet_id
LEFT JOIN pos_bills b ON b.property_id = f.property_id AND b.folio_id = f.id
LEFT JOIN users au ON au.id = p.approved_by
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.payment_method = 'COMP' AND p.status = 'POSTED' AND p.business_date BETWEEN @from_date AND @to_date
UNION ALL
SELECT COALESCE(b.bill_number, '')::text, (i.debit - i.credit)::numeric, COALESCE(l.discount_reason, '')::text, COALESCE(cu.full_name, '')::text, i.business_date, 'DISCOUNT_100'::text
FROM pos_bill_lines l
JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id AND b.outlet_id = @outlet_id AND b.comp_treatment = 'DISCOUNT_100'
JOIN folio_items i ON i.property_id = l.property_id AND i.id = l.folio_item_id
LEFT JOIN users cu ON cu.id = l.discount_by
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.is_comp AND l.status = 'SENT' AND i.business_date BETWEEN @from_date AND @to_date
ORDER BY 5, 1;

-- name: ReportCashiers :many
SELECT COALESCE(u.full_name, '')::text AS cashier, count(*)::int AS payments, COALESCE(sum(p.amount) FILTER (WHERE p.payment_method = 'CASH'), 0)::numeric AS cash,
       COALESCE(sum(p.amount) FILTER (WHERE p.payment_method <> 'CASH'), 0)::numeric AS other
FROM payments p JOIN folios f ON f.property_id = p.property_id AND f.id = p.folio_id AND f.folio_type = 'OUTLET' AND f.outlet_id = @outlet_id
LEFT JOIN users u ON u.id = p.created_by
WHERE p.tenant_id = @tenant_id AND p.property_id = @property_id AND p.business_date BETWEEN @from_date AND @to_date AND p.payment_type = 'PAYMENT' AND p.status = 'POSTED'
GROUP BY u.full_name
ORDER BY u.full_name;

-- name: ReportWaiters :many
SELECT COALESCE(u.full_name, '')::text AS waiter, count(DISTINCT b.id)::int AS bills, COALESCE(sum(i.debit - i.credit), 0)::numeric AS total
FROM pos_bill_lines l
JOIN pos_bills b ON b.property_id = l.property_id AND b.id = l.bill_id AND b.outlet_id = @outlet_id
JOIN folio_items i ON i.property_id = l.property_id AND i.id = l.folio_item_id
LEFT JOIN users u ON u.id = COALESCE(l.created_by, b.waiter_id)
WHERE l.tenant_id = @tenant_id AND l.property_id = @property_id AND l.status = 'SENT' AND i.service_date BETWEEN @from_date AND @to_date
GROUP BY u.full_name
ORDER BY u.full_name;

-- name: ReportOpenBills :one
SELECT count(*)::int AS bills,
       COALESCE(sum((SELECT sum(i.debit - i.credit) FROM folio_items i WHERE i.property_id = b.property_id AND i.folio_id = b.folio_id)), 0)::numeric AS balance
FROM pos_bills b WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.outlet_id = @outlet_id AND b.status = 'OPEN'
  AND EXISTS (SELECT 1 FROM pos_bill_lines l WHERE l.property_id = b.property_id AND l.bill_id = b.id AND l.status = 'SENT');

-- The open bills of the outlets for the night audit: a bill with a sold line is revenue of the day whose payment nobody counted; a bill with draft lines only has nothing in the books.
-- name: NightAuditOpenBills :many
SELECT o.id AS outlet_id, o.code AS outlet_code, o.name AS outlet_name, o.block_night_audit_with_open_bills AS blocks,
       count(*) FILTER (WHERE EXISTS (SELECT 1 FROM pos_bill_lines l WHERE l.property_id = b.property_id AND l.bill_id = b.id AND l.status = 'SENT'))::int AS with_sold_lines,
       count(*) FILTER (WHERE NOT EXISTS (SELECT 1 FROM pos_bill_lines l WHERE l.property_id = b.property_id AND l.bill_id = b.id AND l.status = 'SENT')
                          AND EXISTS (SELECT 1 FROM pos_bill_lines l WHERE l.property_id = b.property_id AND l.bill_id = b.id AND l.status = 'DRAFT'))::int AS drafts_only
FROM pos_bills b JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
WHERE b.tenant_id = @tenant_id AND b.property_id = @property_id AND b.status = 'OPEN'
GROUP BY o.id, o.code, o.name, o.block_night_audit_with_open_bills
ORDER BY o.code;

-- ================================================================ PR 3a: realtime, kitchen display, print queue (docs/architecture/34-pos.md 10, 19)

-- ---------------------------------------------------------------- the outbox (events.go)
-- An event is final once its transaction is older than every transaction still running; they are read in the order (txid, id).
-- name: EventsAfter :many
SELECT e.id, e.txid, e.channel, e.type, e.entity_id, e.at
FROM pos_events e
WHERE e.tenant_id = @tenant_id AND e.property_id = @property_id AND e.channel = ANY(@channels::text[])
  AND (e.txid, e.id) > (@after_txid::bigint, @after_id::bigint)
  AND e.txid < pg_snapshot_xmin(pg_current_snapshot())::text::bigint
ORDER BY e.txid, e.id
LIMIT @max_rows;

-- name: EventPosition :one
SELECT e.txid, e.id FROM pos_events e WHERE e.property_id = @property_id AND e.id = @id;

-- The newest final event of the property: where a stream that asked for no resume point begins.
-- name: EventHead :one
SELECT COALESCE(max(e.txid), 0)::bigint AS txid, COALESCE(max(e.id) FILTER (WHERE e.txid = (SELECT max(f.txid) FROM pos_events f WHERE f.property_id = @property_id AND f.txid < pg_snapshot_xmin(pg_current_snapshot())::text::bigint)), 0)::bigint AS id
FROM pos_events e
WHERE e.property_id = @property_id AND e.txid < pg_snapshot_xmin(pg_current_snapshot())::text::bigint;

-- name: PurgeEvents :execrows
DELETE FROM pos_events WHERE at < @before::timestamptz;

-- name: StationExists :one
SELECT EXISTS (SELECT 1 FROM pos_stations WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id);

-- name: OutletExists :one
SELECT EXISTS (SELECT 1 FROM pos_outlets WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id);

-- ---------------------------------------------------------------- the board of a station
-- name: StationBoardTickets :many
SELECT t.id, t.kind, t.status, t.created_at, t.started_at, t.ready_at, t.served_at, r.round_no,
       b.id AS bill_id, COALESCE(b.bill_number, '')::text AS bill_number, b.order_type, o.id AS outlet_id, o.name AS outlet_name,
       COALESCE(tb.code, '')::text AS table_code, COALESCE(b.customer_name, '')::text AS customer_name,
       COALESCE(u.full_name, '')::text AS waiter_name,
       COALESCE((SELECT r.room_number FROM stay_rooms sr JOIN rooms r ON r.property_id = sr.property_id AND r.id = sr.room_id
                  WHERE sr.property_id = b.property_id AND sr.stay_id = b.target_stay_id ORDER BY sr.id DESC LIMIT 1), '')::text AS room_number
FROM pos_tickets t
JOIN pos_bills b   ON b.property_id = t.property_id AND b.id = t.bill_id
JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
JOIN pos_rounds r  ON r.property_id = t.property_id AND r.id = t.round_id
LEFT JOIN pos_tables tb ON tb.property_id = b.property_id AND tb.id = b.table_id
LEFT JOIN users u  ON u.id = b.waiter_id
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.station_id = @station_id AND t.kind <> 'VOID'
  AND (t.status IN ('NEW', 'COOKING', 'READY') OR (@with_history::boolean AND t.status = 'SERVED' AND t.served_at >= @history_from::timestamptz))
ORDER BY t.created_at, t.id;

-- name: StationBoardLines :many
SELECT tl.id, tl.ticket_id, tl.quantity, tl.name, COALESCE(tl.options_text, '')::text AS options_text, COALESCE(tl.note, '')::text AS note, tl.is_void
FROM pos_ticket_lines tl
WHERE tl.tenant_id = @tenant_id AND tl.property_id = @property_id AND tl.ticket_id = ANY(@ticket_ids::bigint[])
ORDER BY tl.ticket_id, tl.id;

-- The outlets that send to a station: the outlets whose products are made there.
-- name: StationOutlets :many
SELECT DISTINCT o.id, o.name, o.code
FROM pos_outlets o
JOIN pos_price_list_items pi ON pi.property_id = o.property_id AND pi.price_list_id = o.price_list_id AND pi.is_sold
JOIN pos_products p ON p.property_id = pi.property_id AND p.id = pi.product_id
WHERE o.tenant_id = @tenant_id AND o.property_id = @property_id AND o.is_active
  AND COALESCE(pi.station_id, p.station_id) = @station_id
ORDER BY o.name;

-- name: GetTicketForUpdateState :one
SELECT t.id, t.station_id, t.status, t.kind, t.bill_id FROM pos_tickets t
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.id = @id;

-- One statement, one winner: the ticket moves only from the status the caller saw.
-- name: AdvanceTicket :one
UPDATE pos_tickets SET status = @to_status::varchar,
       started_at = CASE WHEN @to_status::varchar = 'COOKING' THEN @at::timestamptz ELSE started_at END,
       started_by = CASE WHEN @to_status::varchar = 'COOKING' THEN sqlc.narg(actor_id)::bigint ELSE started_by END,
       ready_at   = CASE WHEN @to_status::varchar = 'READY' THEN @at::timestamptz ELSE ready_at END,
       ready_by   = CASE WHEN @to_status::varchar = 'READY' THEN sqlc.narg(actor_id)::bigint ELSE ready_by END,
       served_at  = CASE WHEN @to_status::varchar = 'SERVED' THEN @at::timestamptz ELSE served_at END,
       served_by  = CASE WHEN @to_status::varchar = 'SERVED' THEN sqlc.narg(actor_id)::bigint ELSE served_by END
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = @from_status::varchar AND kind <> 'VOID'
RETURNING id, station_id, status;

-- ---------------------------------------------------------------- print jobs
-- name: InsertPrintJob :one
INSERT INTO pos_print_jobs (tenant_id, property_id, printer_id, kind, source_type, source_id, dedupe_key, copy_no, document, payload, reprint_of, reprint_reason, reprint_by, created_by, created_at)
VALUES (@tenant_id, @property_id, @printer_id, @kind, @source_type, @source_id, @dedupe_key, @copy_no, sqlc.narg(document), sqlc.narg(payload), sqlc.narg(reprint_of),
        sqlc.narg(reprint_reason), sqlc.narg(reprint_by), sqlc.narg(created_by), @created_at::timestamptz)
ON CONFLICT (property_id, dedupe_key) DO NOTHING
RETURNING id;

-- name: StationPrinter :one
SELECT s.printer_id, s.kds_enabled, s.ticket_copies FROM pos_stations s WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = @id;

-- name: ListPrintJobs :many
SELECT j.id, j.printer_id, p.name AS printer_name, j.kind, j.source_type, j.source_id, j.status, j.attempts, j.created_at, j.printed_at,
       COALESCE(j.error_code, '')::text AS error_code, j.copy_no, j.max_attempts, (j.reprint_of IS NOT NULL)::boolean AS is_reprint, COALESCE(d.name, '')::text AS device_name,
       COALESCE(b.bill_number, '')::text AS bill_number, COALESCE(tb.code, '')::text AS table_code, COALESCE(s.name, '')::text AS station_name, COALESCE(t.kind, '')::text AS ticket_kind
FROM pos_print_jobs j
JOIN pos_printers p ON p.property_id = j.property_id AND p.id = j.printer_id
LEFT JOIN pos_devices d ON d.property_id = j.property_id AND d.id = COALESCE(j.claimed_by, p.device_id)
LEFT JOIN pos_tickets t ON j.source_type = 'TICKET' AND t.property_id = j.property_id AND t.id = j.source_id
LEFT JOIN pos_stations s ON s.property_id = t.property_id AND s.id = t.station_id
LEFT JOIN pos_bills b ON b.property_id = t.property_id AND b.id = t.bill_id
LEFT JOIN pos_tables tb ON tb.property_id = b.property_id AND tb.id = b.table_id
WHERE j.tenant_id = @tenant_id AND j.property_id = @property_id
  AND (sqlc.narg(printer_id)::bigint IS NULL OR j.printer_id = sqlc.narg(printer_id)::bigint)
  AND (sqlc.narg(status)::text IS NULL OR j.status = sqlc.narg(status)::text)
ORDER BY j.created_at DESC, j.id
LIMIT @max_rows;

-- name: PrintQueueDepth :many
SELECT printer_id, count(*)::int AS queued FROM pos_print_jobs
WHERE tenant_id = @tenant_id AND property_id = @property_id AND status IN ('QUEUED', 'CLAIMED') GROUP BY printer_id;

-- A bill touched without a change of its own (a payment on its folio): the screens re-read it.
-- name: TouchBill :exec
UPDATE pos_bills SET version = version + 1 WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ================================================================ PR 3b: devices, the claim of print jobs, drawer opens (docs/architecture/34-pos.md 20)

-- name: InsertEnrollment :one
INSERT INTO pos_device_enrollments (tenant_id, property_id, code_hash, expires_at, created_by, created_at)
VALUES (@tenant_id, @property_id, @code_hash, @expires_at, sqlc.narg(created_by), @now::timestamptz)
RETURNING id;

-- The code is looked up by its hash alone: a device has no tenant yet.
-- name: EnrollmentForUpdate :one
SELECT id, tenant_id, property_id, expires_at, used_at FROM pos_device_enrollments WHERE code_hash = @code_hash FOR UPDATE;

-- name: UseEnrollment :exec
UPDATE pos_device_enrollments SET used_at = @now::timestamptz, device_id = @device_id WHERE id = @id;

-- name: PendingEnrollments :one
SELECT count(*)::int AS pending, COALESCE(max(expires_at), 'epoch'::timestamptz)::timestamptz AS last_expiry
FROM pos_device_enrollments WHERE tenant_id = @tenant_id AND property_id = @property_id AND used_at IS NULL AND expires_at > @now::timestamptz;

-- name: InsertDevice :one
INSERT INTO pos_devices (tenant_id, property_id, name, token_hash, token_prefix, agent_version, enrolled_by, created_at, last_seen_at)
VALUES (@tenant_id, @property_id, @name, @token_hash, @token_prefix, sqlc.narg(agent_version), sqlc.narg(enrolled_by), @now::timestamptz, @now::timestamptz)
RETURNING id;

-- name: ListDevices :many
SELECT d.id, d.name, d.kind, d.token_prefix, d.status, d.last_seen_at, d.agent_version, d.queue_depth, d.created_at, d.rotated_at, d.revoked_at,
       (SELECT count(*) FROM pos_printers p WHERE p.property_id = d.property_id AND p.device_id = d.id)::int AS printers
FROM pos_devices d WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id
ORDER BY (d.status = 'ACTIVE') DESC, d.name, d.id;

-- name: DeviceByToken :one
SELECT id, tenant_id, property_id, name, status FROM pos_devices WHERE token_hash = @token_hash;

-- name: DeviceSeen :exec
UPDATE pos_devices SET last_seen_at = @now::timestamptz, agent_version = COALESCE(sqlc.narg(agent_version), agent_version),
       queue_depth = COALESCE(sqlc.narg(queue_depth)::int, queue_depth)
WHERE id = @id;

-- name: RevokeDevice :execrows
UPDATE pos_devices SET status = 'REVOKED', revoked_at = @now::timestamptz, revoked_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: RotateDevice :execrows
UPDATE pos_devices SET token_hash = @token_hash, token_prefix = @token_prefix, rotated_at = @now::timestamptz
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND status = 'ACTIVE';

-- name: DeviceState :one
SELECT status, name FROM pos_devices WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: DevicePrinters :many
SELECT id, name, kind, connection_info, paper_width_mm, codepage, opens_drawer
FROM pos_printers WHERE property_id = @property_id AND device_id = @device_id AND is_active ORDER BY id;

-- name: ReportPrinterStatus :execrows
UPDATE pos_printers SET last_status = @status, status_reported_at = @now::timestamptz
WHERE property_id = @property_id AND id = @id AND device_id = @device_id;

-- A job whose lease ran out goes back to the queue (its attempt counted), or fails for good when it has no attempt left.
-- name: RequeueExpiredLeases :exec
UPDATE pos_print_jobs j
SET status = CASE WHEN j.attempts >= j.max_attempts THEN 'FAILED' ELSE 'QUEUED' END,
    error_code = CASE WHEN j.attempts >= j.max_attempts THEN 'LEASE_EXPIRED' ELSE j.error_code END,
    claimed_by = NULL, lease_until = NULL
FROM pos_printers p
WHERE p.property_id = j.property_id AND p.id = j.printer_id AND p.device_id = @device_id
  AND j.property_id = @property_id AND j.status = 'CLAIMED' AND j.lease_until < @now::timestamptz;

-- The oldest job that is not final of each printer of the device: a printer prints in order, so a later job waits for it.
-- name: ClaimCandidates :many
SELECT DISTINCT ON (j.printer_id) j.id, j.printer_id, j.status, j.next_attempt_at
FROM pos_print_jobs j
JOIN pos_printers p ON p.property_id = j.property_id AND p.id = j.printer_id
WHERE p.property_id = @property_id AND p.device_id = @device_id AND p.is_active
  AND j.property_id = @property_id AND j.status IN ('QUEUED', 'CLAIMED')
ORDER BY j.printer_id, j.seq;

-- name: ClaimJob :one
UPDATE pos_print_jobs SET status = 'CLAIMED', claimed_by = @device_id, lease_until = @lease_until, attempts = attempts + 1
WHERE id = (SELECT c.id FROM pos_print_jobs c WHERE c.id = @id AND c.property_id = @property_id AND c.status = 'QUEUED'
              AND (c.next_attempt_at IS NULL OR c.next_attempt_at <= @now::timestamptz) FOR UPDATE SKIP LOCKED)
RETURNING id, tenant_id, property_id, printer_id, kind, source_type, source_id, copy_no, attempts, max_attempts, payload, document, reprint_of, payload_purged_at;

-- name: SetJobPayload :exec
UPDATE pos_print_jobs SET document = @document, payload = @payload WHERE id = @id AND property_id = @property_id AND payload IS NULL AND payload_purged_at IS NULL;

-- name: JobForResult :one
SELECT j.id, j.tenant_id, j.property_id, j.printer_id, j.kind, j.source_type, j.source_id, j.status, j.attempts, j.max_attempts, j.claimed_by, j.error_code
FROM pos_print_jobs j WHERE j.id = @id AND j.property_id = @property_id FOR UPDATE;

-- name: JobPrinted :exec
UPDATE pos_print_jobs SET status = 'PRINTED', printed_at = @now::timestamptz, lease_until = NULL, error_code = NULL, next_attempt_at = NULL WHERE id = @id;

-- name: JobRequeue :exec
UPDATE pos_print_jobs SET status = 'QUEUED', error_code = @error_code, lease_until = NULL, next_attempt_at = @next_attempt_at WHERE id = @id;

-- name: JobFailed :exec
UPDATE pos_print_jobs SET status = 'FAILED', error_code = @error_code, lease_until = NULL, next_attempt_at = NULL WHERE id = @id;

-- A job that never printed within the retention is failed first, then its payload goes (the row stays).
-- name: ExpireOldJobs :exec
UPDATE pos_print_jobs SET status = 'FAILED', error_code = 'EXPIRED', lease_until = NULL, next_attempt_at = NULL
WHERE status IN ('QUEUED', 'CLAIMED') AND created_at < @cutoff::timestamptz
  AND (sqlc.narg(property_id)::bigint IS NULL OR property_id = sqlc.narg(property_id)::bigint);

-- name: PurgePayloads :execrows
UPDATE pos_print_jobs SET payload = NULL, document = NULL, payload_purged_at = @now::timestamptz
WHERE payload IS NOT NULL AND created_at < @cutoff::timestamptz AND status IN ('PRINTED', 'FAILED', 'CANCELLED')
  AND (sqlc.narg(property_id)::bigint IS NULL OR property_id = sqlc.narg(property_id)::bigint);

-- name: PrinterForJob :one
SELECT id, kind, paper_width_mm, codepage, opens_drawer, connection_info, device_id, is_active
FROM pos_printers WHERE property_id = @property_id AND id = @id;

-- The head of the paper when an outlet leaves a field empty: the property.
-- name: PropertyPaper :one
SELECT name, COALESCE(address, '')::text AS address, COALESCE(city, '')::text AS city, COALESCE(tax_id, '')::text AS tax_id, timezone, currency_decimals
FROM properties WHERE tenant_id = @tenant_id AND id = @id;

-- A ticket with what the paper says (the same facts as the board).
-- name: TicketForPrint :one
SELECT t.id, t.kind, t.status, t.created_at, t.station_id, r.round_no,
       b.id AS bill_id, COALESCE(b.bill_number, '')::text AS bill_number, b.order_type, o.id AS outlet_id, o.name AS outlet_name,
       COALESCE(tb.code, '')::text AS table_code, COALESCE(b.customer_name, '')::text AS customer_name,
       COALESCE(u.full_name, '')::text AS waiter_name, COALESCE(s.name, '')::text AS station_name,
       COALESCE((SELECT r2.room_number FROM stay_rooms sr JOIN rooms r2 ON r2.property_id = sr.property_id AND r2.id = sr.room_id
                  WHERE sr.property_id = b.property_id AND sr.stay_id = b.target_stay_id ORDER BY sr.id DESC LIMIT 1), '')::text AS room_number
FROM pos_tickets t
JOIN pos_bills b   ON b.property_id = t.property_id AND b.id = t.bill_id
JOIN pos_outlets o ON o.property_id = b.property_id AND o.id = b.outlet_id
JOIN pos_rounds r  ON r.property_id = t.property_id AND r.id = t.round_id
LEFT JOIN pos_stations s ON s.property_id = t.property_id AND s.id = t.station_id
LEFT JOIN pos_tables tb ON tb.property_id = b.property_id AND tb.id = b.table_id
LEFT JOIN users u  ON u.id = b.waiter_id
WHERE t.tenant_id = @tenant_id AND t.property_id = @property_id AND t.id = @id;

-- The failed jobs of an outlet that the cashier sees: what did not print, newest first (a job that was printed again is not listed).
-- name: FailedJobsOfOutlet :many
SELECT j.id, j.printer_id, p.name AS printer_name, j.kind, j.source_type, j.source_id, j.copy_no, j.attempts, COALESCE(j.error_code, '')::text AS error_code, j.created_at,
       COALESCE(COALESCE(b.bill_number, tb.bill_number), '')::text AS bill_number, COALESCE(b.id, tb.id, 0)::bigint AS bill_id
FROM pos_print_jobs j
JOIN pos_printers p ON p.property_id = j.property_id AND p.id = j.printer_id
LEFT JOIN pos_bills b ON j.source_type = 'BILL' AND b.property_id = j.property_id AND b.id = j.source_id
LEFT JOIN pos_tickets t ON j.source_type = 'TICKET' AND t.property_id = j.property_id AND t.id = j.source_id
LEFT JOIN pos_bills tb ON tb.property_id = t.property_id AND tb.id = t.bill_id
WHERE j.tenant_id = @tenant_id AND j.property_id = @property_id AND j.status = 'FAILED' AND j.created_at >= @since::timestamptz
  AND (COALESCE(b.outlet_id, tb.outlet_id) = @outlet_id OR (j.source_type = 'OUTLET' AND j.source_id = @outlet_id))
  AND NOT EXISTS (SELECT 1 FROM pos_print_jobs r WHERE r.reprint_of = j.id)
ORDER BY j.created_at DESC, j.id
LIMIT 50;

-- name: JobToReprint :one
SELECT j.id, j.printer_id, j.kind, j.source_type, j.source_id, j.copy_no, j.status, j.document, j.payload_purged_at
FROM pos_print_jobs j WHERE j.tenant_id = @tenant_id AND j.property_id = @property_id AND j.id = @id;

-- name: MaxCopyOfSource :one
SELECT COALESCE(max(copy_no), 0)::int AS max_copy FROM pos_print_jobs
WHERE property_id = @property_id AND source_type = @source_type AND source_id = @source_id AND kind = @kind;

-- The number of times a receipt (or a pre-bill) of a bill was printed as an original: the next one is a new round of the dedupe key.
-- name: ReceiptRound :one
SELECT count(*)::int AS n FROM pos_print_jobs
WHERE property_id = @property_id AND source_type = 'BILL' AND source_id = @bill_id AND kind = @kind AND reprint_of IS NULL AND dedupe_key LIKE @key_prefix::text || '%' AND copy_no = 1;

-- name: InsertDrawerOpen :one
INSERT INTO pos_drawer_opens (tenant_id, property_id, outlet_id, drawer, shift_id, business_date, reason, opened_by, opened_at, job_id)
VALUES (@tenant_id, @property_id, @outlet_id, @drawer, sqlc.narg(shift_id), @business_date, @reason, @opened_by, @opened_at, sqlc.narg(job_id))
RETURNING id;

-- name: DrawerOpensOfOutlet :many
SELECT d.id, d.opened_at, d.business_date, d.reason, d.drawer, d.shift_id, u.full_name AS opened_by_name
FROM pos_drawer_opens d JOIN users u ON u.id = d.opened_by
WHERE d.tenant_id = @tenant_id AND d.property_id = @property_id AND d.outlet_id = @outlet_id
  AND d.business_date >= @from_date AND d.business_date <= @to_date
ORDER BY d.opened_at DESC, d.id;

-- name: GuestOfStay :one
SELECT btrim(COALESCE(g.first_name, '') || ' ' || g.last_name)::text AS guest_name
FROM stays s JOIN guests g ON g.tenant_id = s.tenant_id AND g.id = s.guest_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id AND s.id = @stay_id;

-- name: UserName :one
SELECT full_name FROM users WHERE id = @id;

-- The receipt that was printed first for a bill (the original that a copy points to), when one was printed through the queue.
-- name: OriginalReceiptJob :one
SELECT id FROM pos_print_jobs
WHERE property_id = @property_id AND source_type = 'BILL' AND source_id = @bill_id AND kind = 'RECEIPT' AND reprint_of IS NULL AND copy_no = 1 AND dedupe_key LIKE 'RECEIPT:%'
ORDER BY seq DESC LIMIT 1;
