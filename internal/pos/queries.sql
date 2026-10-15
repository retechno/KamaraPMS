-- POS masters (docs/architecture/34-pos.md, PR 1). Every query is scoped by tenant and property.

-- ---------------------------------------------------------------- outlets
-- name: ListOutlets :many
SELECT o.id, o.code, o.name, o.outlet_type, o.order_mode, o.department_id, d.name AS department_name, o.rule_profile_id, rp.name AS rule_profile_name,
       o.price_list_id, pl.name AS price_list_name, o.drawer, o.receipt_printer_id, pr.name AS receipt_printer_name, o.uses_tables, o.guest_count_required,
       o.rounding_unit, o.allow_room_charge, o.allow_company_charge, o.allow_comp, o.comp_treatment, o.max_discount_pct_no_approval,
       o.block_night_audit_with_open_bills, o.is_active, o.sort_order, o.version
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
                         block_night_audit_with_open_bills, is_active, sort_order, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, @outlet_type, @order_mode, @department_id, @rule_profile_id, @price_list_id, sqlc.narg(drawer), sqlc.narg(receipt_printer_id), @uses_tables,
        @guest_count_required, @rounding_unit, @allow_room_charge, @allow_company_charge, @allow_comp, @comp_treatment, @max_discount_pct_no_approval,
        @block_night_audit_with_open_bills, @is_active, @sort_order, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateOutlet :one
UPDATE pos_outlets SET name = @name, outlet_type = @outlet_type, order_mode = @order_mode, department_id = @department_id, rule_profile_id = @rule_profile_id,
       price_list_id = @price_list_id, drawer = sqlc.narg(drawer), receipt_printer_id = sqlc.narg(receipt_printer_id), uses_tables = @uses_tables,
       guest_count_required = @guest_count_required, rounding_unit = @rounding_unit, allow_room_charge = @allow_room_charge, allow_company_charge = @allow_company_charge,
       allow_comp = @allow_comp, comp_treatment = @comp_treatment, max_discount_pct_no_approval = @max_discount_pct_no_approval,
       block_night_audit_with_open_bills = @block_night_audit_with_open_bills, is_active = @is_active, sort_order = @sort_order,
       version = version + 1, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id AND version = @version
RETURNING id;

-- name: ChargeCodeKind :one
SELECT charge_type, is_active FROM charge_codes WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- ---------------------------------------------------------------- printers and stations
-- name: ListPrinters :many
SELECT id, name, kind, connection_info, paper_width_mm, codepage, opens_drawer, is_active
FROM pos_printers WHERE tenant_id = @tenant_id AND property_id = @property_id
ORDER BY name, id;

-- name: InsertPrinter :one
INSERT INTO pos_printers (tenant_id, property_id, name, kind, connection_info, paper_width_mm, codepage, opens_drawer, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @name, @kind, @connection_info, @paper_width_mm, @codepage, @opens_drawer, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdatePrinter :execrows
UPDATE pos_printers SET name = @name, kind = @kind, connection_info = @connection_info, paper_width_mm = @paper_width_mm, codepage = @codepage,
       opens_drawer = @opens_drawer, is_active = @is_active, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND property_id = @property_id AND id = @id;

-- name: ListStations :many
SELECT s.id, s.code, s.name, s.printer_id, pr.name AS printer_name, s.kds_enabled, s.late_after_minutes, s.is_active
FROM pos_stations s LEFT JOIN pos_printers pr ON pr.property_id = s.property_id AND pr.id = s.printer_id
WHERE s.tenant_id = @tenant_id AND s.property_id = @property_id
ORDER BY s.name, s.id;

-- name: InsertStation :one
INSERT INTO pos_stations (tenant_id, property_id, code, name, printer_id, kds_enabled, late_after_minutes, is_active, created_by, updated_by)
VALUES (@tenant_id, @property_id, @code, @name, sqlc.narg(printer_id), @kds_enabled, @late_after_minutes, @is_active, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING id;

-- name: UpdateStation :execrows
UPDATE pos_stations SET name = @name, printer_id = sqlc.narg(printer_id), kds_enabled = @kds_enabled, late_after_minutes = @late_after_minutes,
       is_active = @is_active, updated_by = sqlc.narg(actor_id)
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
SELECT o.id AS outlet_id, o.code AS outlet_code, p.id AS product_id, p.code AS product_code, s.id AS station_id, s.code AS station_code
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
