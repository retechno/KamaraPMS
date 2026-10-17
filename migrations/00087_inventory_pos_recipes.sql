-- +goose Up
-- Inventory, PR 4 (docs/architecture/35-inventory.md, sections 3.1, 3.5, 4.9, 5.6, 5.7): recipes and the deduction of a POS sale from stock.
--
--   pos_outlets.warehouse_id               the warehouse an outlet sells from (NULL: the outlet deducts nothing)
--   pos_outlets.void_waste_after_minutes   D5: for an outlet that prints tickets (its tickets never leave NEW), a void this many minutes after the send is waste, not a return (0: off)
--   pos_categories.fb_class                the sale class of the products of the category (4.9); NULL takes the parent's, then the class of the warehouse of the outlet
--   pos_recipe_lines                       the unit becomes a unit of the item (a composite foreign key), the item is required
--   inv_consumption_queue                  one row for each POS line sold (SALE) and for each void of a line already deducted (VOID_RETURN, VOID_WASTE)
--
-- Differences from the design text, recorded in 35-inventory.md "As built: PR 4":
--   * pos_recipe_lines.unit (a code, varchar) is replaced by unit_id (the table never held a row: no screen or endpoint wrote it before this PR; the migration refuses to go on if it does);
--   * a void row names the sale row it undoes (sale_queue_id) and reads its components and its movements from it;
--   * the queue row also keeps the product, the quantity of the line and the reason a row failed (error_message), for the screens.

-- 1. The outlet and its category -----------------------------------------------------------------------------------------------------------------
ALTER TABLE pos_outlets
    ADD COLUMN warehouse_id bigint,
    ADD COLUMN void_waste_after_minutes int NOT NULL DEFAULT 0,
    ADD CONSTRAINT pos_outlets_warehouse_fk FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    ADD CONSTRAINT pos_outlets_void_waste_ck CHECK (void_waste_after_minutes BETWEEN 0 AND 1440);

ALTER TABLE pos_categories
    ADD COLUMN fb_class varchar(10),
    ADD CONSTRAINT pos_categories_fb_class_ck CHECK (fb_class IS NULL OR fb_class IN ('FOOD', 'BEVERAGE', 'OTHER'));

-- 2. The recipe lines point at the items --------------------------------------------------------------------------------------------------------
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM pos_recipe_lines;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot migrate 00087: pos_recipe_lines holds % row(s) written before the inventory module existed', v_n; END IF;
END
$$;
-- +goose StatementEnd

CREATE UNIQUE INDEX inv_item_units_property_uk ON inv_item_units (property_id, item_id, unit_id);

ALTER TABLE pos_recipe_lines
    DROP COLUMN unit,
    ALTER COLUMN inventory_item_id SET NOT NULL,
    ADD COLUMN unit_id bigint NOT NULL,
    ADD CONSTRAINT pos_recipe_lines_item_fk      FOREIGN KEY (property_id, inventory_item_id) REFERENCES inv_items (property_id, id),
    ADD CONSTRAINT pos_recipe_lines_item_unit_fk FOREIGN KEY (property_id, inventory_item_id, unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id);

CREATE UNIQUE INDEX pos_recipe_lines_item_uk ON pos_recipe_lines (product_id, COALESCE(option_id, 0), inventory_item_id);
CREATE INDEX pos_recipe_lines_product_idx ON pos_recipe_lines (property_id, product_id);

-- 3. The queue ----------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_consumption_queue (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    outlet_id        bigint        NOT NULL,
    warehouse_id     bigint        NOT NULL,
    pos_bill_line_id bigint        NOT NULL,
    kind             varchar(12)   NOT NULL,
    sale_queue_id    bigint,
    product_id       bigint        NOT NULL,
    quantity         numeric(10,3) NOT NULL,
    sale_class       varchar(10)   NOT NULL,
    sale_date        date          NOT NULL,
    components       jsonb         NOT NULL,
    status           varchar(10)   NOT NULL DEFAULT 'PENDING',
    attempts         int           NOT NULL DEFAULT 0,
    error_code       varchar(40),
    error_message    varchar(300),
    movement_ids     bigint[]      NOT NULL DEFAULT '{}',
    applied_on       date,
    created_at       timestamptz   NOT NULL DEFAULT now(),
    applied_at       timestamptz,
    CONSTRAINT inv_consumption_queue_tenant_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_consumption_queue_outlet_fk    FOREIGN KEY (property_id, outlet_id)       REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_consumption_queue_warehouse_fk FOREIGN KEY (property_id, warehouse_id)    REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_consumption_queue_line_fk      FOREIGN KEY (property_id, pos_bill_line_id) REFERENCES pos_bill_lines (property_id, id),
    CONSTRAINT inv_consumption_queue_product_fk   FOREIGN KEY (property_id, product_id)      REFERENCES pos_products (property_id, id),
    CONSTRAINT inv_consumption_queue_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_consumption_queue_sale_fk      FOREIGN KEY (property_id, sale_queue_id)   REFERENCES inv_consumption_queue (property_id, id),
    CONSTRAINT inv_consumption_queue_line_kind_uk UNIQUE (pos_bill_line_id, kind),
    CONSTRAINT inv_consumption_queue_kind_ck      CHECK (kind IN ('SALE', 'VOID_RETURN', 'VOID_WASTE')),
    CONSTRAINT inv_consumption_queue_class_ck     CHECK (sale_class IN ('FOOD', 'BEVERAGE', 'OTHER')),
    CONSTRAINT inv_consumption_queue_status_ck    CHECK (status IN ('PENDING', 'HELD', 'APPLIED', 'CANCELLED', 'FAILED')),
    CONSTRAINT inv_consumption_queue_void_ck      CHECK ((kind = 'SALE') = (sale_queue_id IS NULL)),
    CONSTRAINT inv_consumption_queue_qty_ck       CHECK (quantity > 0),
    CONSTRAINT inv_consumption_queue_components_ck CHECK (jsonb_typeof(components) = 'array'),
    CONSTRAINT inv_consumption_queue_applied_ck   CHECK ((status = 'APPLIED') = (applied_on IS NOT NULL))
);
CREATE INDEX inv_consumption_queue_open_idx ON inv_consumption_queue (property_id, status, id) WHERE status IN ('PENDING', 'HELD', 'FAILED');
CREATE INDEX inv_consumption_queue_sale_idx ON inv_consumption_queue (property_id, sale_queue_id) WHERE sale_queue_id IS NOT NULL;

-- +goose Down
-- Refused while the POS deduction holds anything: a queue row, a recipe line, an outlet with a warehouse or a category with a class.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM inv_consumption_queue) + (SELECT count(*) FROM pos_recipe_lines) + (SELECT count(*) FROM pos_outlets WHERE warehouse_id IS NOT NULL OR void_waste_after_minutes <> 0)
         + (SELECT count(*) FROM pos_categories WHERE fb_class IS NOT NULL) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00087: the POS deduction holds % row(s) (queue rows, recipe lines, outlets with a warehouse, categories with a class)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE inv_consumption_queue;
DROP INDEX pos_recipe_lines_product_idx;
DROP INDEX pos_recipe_lines_item_uk;
ALTER TABLE pos_recipe_lines
    DROP CONSTRAINT pos_recipe_lines_item_unit_fk,
    DROP CONSTRAINT pos_recipe_lines_item_fk,
    DROP COLUMN unit_id,
    ADD COLUMN unit varchar(10) NOT NULL,
    ALTER COLUMN inventory_item_id DROP NOT NULL;
DROP INDEX inv_item_units_property_uk;
ALTER TABLE pos_categories DROP CONSTRAINT pos_categories_fb_class_ck, DROP COLUMN fb_class;
ALTER TABLE pos_outlets DROP CONSTRAINT pos_outlets_void_waste_ck, DROP CONSTRAINT pos_outlets_warehouse_fk, DROP COLUMN void_waste_after_minutes, DROP COLUMN warehouse_id;
