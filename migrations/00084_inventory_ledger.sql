-- +goose Up
-- Inventory, PR 1 (docs/architecture/35-inventory.md, sections 3.1 and 3.2): the masters (units, categories with their accounts, items with their units, warehouses, minimum levels), the settings with the
-- costing method that locks at the first movement, the stock ledger (append-only, dated by the open business day), the balances, the FIFO layers, the shortfalls (stock sold below zero) and the opening stock.
-- Nothing here is used by the guest ledger, the folios or the POS: the module is a new set of tables and a new set of system accounts.
--
--   inventory_settings, inv_units, inv_categories, inv_items, inv_item_units, inv_warehouses, inv_levels
--   inv_movements (the ledger), inv_balances, inv_layers, inv_layer_uses, inv_shortfalls, inv_openings, inv_opening_lines
--   gl_account_map                 the keys INV_GRNI, INV_PPV, INV_WASTE, INV_COUNT_VARIANCE, INV_COGS_FOOD, INV_COGS_BEVERAGE, INV_STAFF_MEAL, INV_ENTERTAINMENT
--   gl_accounts, usali_chart()     2130 goods received not invoiced, 5290 waste, 5295 count variance, 5299 purchase price variance (the replacement rule of 00071)
--   document_sequences             INV_OPENING (OPN-000001)
--   business_day_must_be_open      on inv_movements.business_date
--
-- Differences from the design text, recorded in 35-inventory.md "As built: PR 1":
--   * inv_warehouses.count_id has no foreign key yet (the table of counts comes with PR 3, which adds it);
--   * inv_supplier_items belongs to the purchasing PR (PR 2);
--   * the four new expense/liability accounts take no default department (the accounts 5210 and 5220 of the chart have none either);
--   * inv_shortfalls keeps est_value_open, the value of the open quantity at the estimate, so that a settlement in parts never drifts by a rounding;
--   * the opening stock has a number (OPN-000001, the sequence INV_OPENING) which the design text does not list.

-- 1. The settings ---------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inventory_settings (
    property_id              bigint        PRIMARY KEY,
    tenant_id                bigint        NOT NULL,
    costing_method           varchar(8)    NOT NULL DEFAULT 'AVERAGE',
    costing_locked_on        date,
    price_tolerance_pct      numeric(5,2)  NOT NULL DEFAULT 1.00,
    qty_tolerance_pct        numeric(5,2)  NOT NULL DEFAULT 0.00,
    over_receipt_pct         numeric(5,2)  NOT NULL DEFAULT 5.00,
    po_requires_pr           boolean       NOT NULL DEFAULT true,
    direct_receipt_limit     numeric(18,3),
    count_blocks_night_audit boolean       NOT NULL DEFAULT true,
    count_blind              boolean       NOT NULL DEFAULT true,
    version                  int           NOT NULL DEFAULT 1,
    created_at               timestamptz   NOT NULL DEFAULT now(),
    created_by               bigint REFERENCES users (id),
    updated_at               timestamptz   NOT NULL DEFAULT now(),
    updated_by               bigint REFERENCES users (id),
    CONSTRAINT inventory_settings_property_fk  FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT inventory_settings_method_ck    CHECK (costing_method IN ('AVERAGE', 'FIFO')),
    CONSTRAINT inventory_settings_pct_ck       CHECK (price_tolerance_pct BETWEEN 0 AND 100 AND qty_tolerance_pct BETWEEN 0 AND 100 AND over_receipt_pct BETWEEN 0 AND 100),
    CONSTRAINT inventory_settings_limit_ck     CHECK (direct_receipt_limit IS NULL OR direct_receipt_limit >= 0)
);
CREATE TRIGGER inventory_settings_set_updated_at BEFORE UPDATE ON inventory_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The method is chosen before the first stock movement and then locked, as the release of deposits is (00078). The first movement stamps the date (inv_movements_lock_costing); once it is stamped the
-- method cannot be written to another value, by anybody.
-- +goose StatementBegin
CREATE FUNCTION inventory_costing_method_is_locked() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.costing_method IS DISTINCT FROM OLD.costing_method AND OLD.costing_locked_on IS NOT NULL THEN
        RAISE EXCEPTION 'the costing method of property % is locked since %', OLD.property_id, OLD.costing_locked_on
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'inventory_costing_method_locked';
    END IF;
    IF NEW.costing_locked_on IS DISTINCT FROM OLD.costing_locked_on AND OLD.costing_locked_on IS NOT NULL THEN
        RAISE EXCEPTION 'the costing lock date of property % cannot be changed', OLD.property_id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'inventory_costing_method_locked';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER inventory_settings_costing_locked BEFORE UPDATE ON inventory_settings FOR EACH ROW EXECUTE FUNCTION inventory_costing_method_is_locked();

-- 2. Units, categories, items --------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_units (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(10)  NOT NULL,
    name        varchar(60)  NOT NULL,
    is_active   boolean      NOT NULL DEFAULT true,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT inv_units_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_units_code_uk        UNIQUE (property_id, code),
    CONSTRAINT inv_units_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_units_code_ck        CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,9}$'),
    CONSTRAINT inv_units_name_ck        CHECK (length(btrim(name)) > 0)
);
CREATE TRIGGER inv_units_set_updated_at BEFORE UPDATE ON inv_units FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE inv_categories (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    code                 varchar(20)  NOT NULL,
    name                 varchar(100) NOT NULL,
    kind                 varchar(10)  NOT NULL,
    inventory_account_id bigint       NOT NULL,
    cogs_account_id      bigint,
    issue_account_id     bigint,
    waste_account_id     bigint,
    variance_account_id  bigint,
    is_active            boolean      NOT NULL DEFAULT true,
    sort_order           int          NOT NULL DEFAULT 0,
    version              int          NOT NULL DEFAULT 1,
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT inv_categories_property_fk    FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_categories_inventory_fk   FOREIGN KEY (property_id, inventory_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_categories_cogs_fk        FOREIGN KEY (property_id, cogs_account_id)  REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_categories_issue_fk       FOREIGN KEY (property_id, issue_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_categories_waste_fk       FOREIGN KEY (property_id, waste_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_categories_variance_fk    FOREIGN KEY (property_id, variance_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_categories_code_uk        UNIQUE (property_id, code),
    CONSTRAINT inv_categories_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_categories_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT inv_categories_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT inv_categories_kind_ck        CHECK (kind IN ('FOOD', 'BEVERAGE', 'SUPPLIES', 'OTHER'))
);
CREATE TRIGGER inv_categories_set_updated_at BEFORE UPDATE ON inv_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The accounts of a category must be of the right kind (35-inventory.md 6.1): the inventory account an ASSET of the group INVENTORIES, every cost account an EXPENSE. Postable and active are the service's
-- business (an account may be switched off later; the ledger keeps the account it was posted with).
-- +goose StatementBegin
CREATE FUNCTION inv_categories_accounts_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_bad bigint;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = NEW.property_id AND a.id = NEW.inventory_account_id
                      AND a.account_type = 'ASSET' AND a.statement_group = 'INVENTORIES') THEN
        RAISE EXCEPTION 'the inventory account of category % must be an asset account of the group INVENTORIES', NEW.code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inv_categories_inventory_account_ck';
    END IF;
    SELECT a.id INTO v_bad FROM gl_accounts a
     WHERE a.property_id = NEW.property_id AND a.id IN (NEW.cogs_account_id, NEW.issue_account_id, NEW.waste_account_id, NEW.variance_account_id) AND a.account_type <> 'EXPENSE'
     LIMIT 1;
    IF v_bad IS NOT NULL THEN
        RAISE EXCEPTION 'the cost accounts of category % must be expense accounts', NEW.code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'inv_categories_expense_account_ck';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER inv_categories_accounts BEFORE INSERT OR UPDATE ON inv_categories FOR EACH ROW EXECUTE FUNCTION inv_categories_accounts_check();

CREATE TABLE inv_items (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    code                varchar(30)   NOT NULL,
    name                varchar(150)  NOT NULL,
    category_id         bigint        NOT NULL,
    stock_unit_id       bigint        NOT NULL,
    reference_cost      numeric(18,6),
    default_supplier_id bigint,
    barcode             varchar(40),
    note                varchar(300),
    is_active           boolean       NOT NULL DEFAULT true,
    version             int           NOT NULL DEFAULT 1,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    updated_by          bigint REFERENCES users (id),
    CONSTRAINT inv_items_property_fk    FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_items_category_fk    FOREIGN KEY (property_id, category_id)       REFERENCES inv_categories (property_id, id),
    CONSTRAINT inv_items_unit_fk        FOREIGN KEY (property_id, stock_unit_id)     REFERENCES inv_units (property_id, id),
    CONSTRAINT inv_items_supplier_fk    FOREIGN KEY (property_id, default_supplier_id) REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_items_code_uk        UNIQUE (property_id, code),
    CONSTRAINT inv_items_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_items_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,29}$'),
    CONSTRAINT inv_items_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT inv_items_cost_ck        CHECK (reference_cost IS NULL OR reference_cost >= 0)
);
CREATE TRIGGER inv_items_set_updated_at BEFORE UPDATE ON inv_items FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE INDEX inv_items_category_idx ON inv_items (property_id, category_id);
CREATE UNIQUE INDEX inv_items_barcode_uk ON inv_items (property_id, barcode) WHERE barcode IS NOT NULL;

-- 1 of this unit = stock_num / stock_den stock units. The stock unit of the item is 1/1 (the service keeps it so).
CREATE TABLE inv_item_units (
    tenant_id           bigint   NOT NULL,
    property_id         bigint   NOT NULL,
    item_id             bigint   NOT NULL,
    unit_id             bigint   NOT NULL,
    stock_num           int      NOT NULL,
    stock_den           int      NOT NULL,
    is_purchase         boolean  NOT NULL DEFAULT false,
    is_recipe           boolean  NOT NULL DEFAULT false,
    is_default_purchase boolean  NOT NULL DEFAULT false,
    is_default_recipe   boolean  NOT NULL DEFAULT false,
    PRIMARY KEY (item_id, unit_id),
    CONSTRAINT inv_item_units_item_fk   FOREIGN KEY (property_id, item_id) REFERENCES inv_items (property_id, id) ON DELETE CASCADE,
    CONSTRAINT inv_item_units_unit_fk   FOREIGN KEY (property_id, unit_id) REFERENCES inv_units (property_id, id),
    CONSTRAINT inv_item_units_tenant_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_item_units_ratio_ck  CHECK (stock_num > 0 AND stock_den > 0),
    CONSTRAINT inv_item_units_default_purchase_ck CHECK (NOT is_default_purchase OR is_purchase),
    CONSTRAINT inv_item_units_default_recipe_ck   CHECK (NOT is_default_recipe OR is_recipe)
);
CREATE UNIQUE INDEX inv_item_units_default_purchase_uk ON inv_item_units (item_id) WHERE is_default_purchase;
CREATE UNIQUE INDEX inv_item_units_default_recipe_uk   ON inv_item_units (item_id) WHERE is_default_recipe;
-- the composite key a later table (a recipe line, a PO line) points at to say "a unit of this item"
ALTER TABLE inv_item_units ADD CONSTRAINT inv_item_units_property_item_unit_uk UNIQUE (property_id, item_id, unit_id);

-- 3. Warehouses and levels -----------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_warehouses (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint       NOT NULL,
    property_id   bigint       NOT NULL,
    code          varchar(20)  NOT NULL,
    name          varchar(100) NOT NULL,
    kind          varchar(10)  NOT NULL,
    outlet_id     bigint,
    department_id bigint,
    cost_class    varchar(10)  NOT NULL DEFAULT 'OTHER',
    count_id      bigint,
    is_active     boolean      NOT NULL DEFAULT true,
    version       int          NOT NULL DEFAULT 1,
    created_at    timestamptz  NOT NULL DEFAULT now(),
    created_by    bigint REFERENCES users (id),
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    CONSTRAINT inv_warehouses_property_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_warehouses_outlet_fk      FOREIGN KEY (property_id, outlet_id)       REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_warehouses_department_fk  FOREIGN KEY (property_id, department_id)  REFERENCES departments (property_id, id),
    CONSTRAINT inv_warehouses_code_uk        UNIQUE (property_id, code),
    CONSTRAINT inv_warehouses_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_warehouses_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT inv_warehouses_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT inv_warehouses_kind_ck        CHECK (kind IN ('MAIN', 'OUTLET', 'DEPARTMENT')),
    CONSTRAINT inv_warehouses_outlet_ck      CHECK ((kind = 'OUTLET') = (outlet_id IS NOT NULL)),
    CONSTRAINT inv_warehouses_class_ck       CHECK (cost_class IN ('FOOD', 'BEVERAGE', 'OTHER'))
);
CREATE TRIGGER inv_warehouses_set_updated_at BEFORE UPDATE ON inv_warehouses FOR EACH ROW EXECUTE FUNCTION set_updated_at();
-- at most one active warehouse per outlet (the outlet deducts from it)
CREATE UNIQUE INDEX inv_warehouses_outlet_uk ON inv_warehouses (property_id, outlet_id) WHERE outlet_id IS NOT NULL AND is_active;

CREATE TABLE inv_levels (
    tenant_id    bigint         NOT NULL,
    property_id  bigint         NOT NULL,
    warehouse_id bigint         NOT NULL,
    item_id      bigint         NOT NULL,
    min_qty      numeric(18,6)  NOT NULL DEFAULT 0,
    max_qty      numeric(18,6),
    reorder_qty  numeric(18,6),
    PRIMARY KEY (warehouse_id, item_id),
    CONSTRAINT inv_levels_tenant_fk    FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_levels_warehouse_fk FOREIGN KEY (property_id, warehouse_id)   REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_levels_item_fk      FOREIGN KEY (property_id, item_id)        REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_levels_qty_ck       CHECK (min_qty >= 0 AND (max_qty IS NULL OR max_qty >= min_qty) AND (reorder_qty IS NULL OR reorder_qty >= 0))
);

-- 4. Opening stock (the header and its lines; the movements are the ledger) -------------------------------------------------------------------
CREATE TABLE inv_openings (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    opening_number varchar(30)   NOT NULL,
    client_uuid    uuid          NOT NULL,
    warehouse_id   bigint        NOT NULL,
    business_date  date          NOT NULL,
    total_value    numeric(18,3) NOT NULL,
    note           varchar(300),
    created_at     timestamptz   NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    CONSTRAINT inv_openings_property_fk    FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_openings_warehouse_fk   FOREIGN KEY (property_id, warehouse_id)   REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_openings_number_uk      UNIQUE (property_id, opening_number),
    CONSTRAINT inv_openings_client_uk      UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_openings_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_openings_total_ck       CHECK (total_value >= 0)
);

-- 5. The ledger -------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_movements (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    warehouse_id        bigint        NOT NULL,
    item_id             bigint        NOT NULL,
    kind                varchar(20)   NOT NULL,
    qty_delta           numeric(18,6) NOT NULL,
    value_delta         numeric(18,3) NOT NULL,
    unit_cost           numeric(18,6) NOT NULL DEFAULT 0,
    cost_estimated      boolean       NOT NULL DEFAULT false,
    business_date       date          NOT NULL,
    source_date         date,
    source_type         varchar(20)   NOT NULL,
    source_id           bigint        NOT NULL,
    source_line         int           NOT NULL DEFAULT 1,
    reverses_id         bigint,
    gl_debit_account_id  bigint,
    gl_credit_account_id bigint,
    gl_amount           numeric(18,3) NOT NULL DEFAULT 0,
    gl_department_id    bigint,
    item_class          varchar(10)   NOT NULL,
    cost_class          varchar(10)   NOT NULL,
    outlet_id           bigint,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    CONSTRAINT inv_movements_property_fk    FOREIGN KEY (tenant_id, property_id)            REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_movements_warehouse_fk   FOREIGN KEY (property_id, warehouse_id)         REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_movements_item_fk        FOREIGN KEY (property_id, item_id)              REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_movements_day_fk         FOREIGN KEY (property_id, business_date)        REFERENCES business_days (property_id, business_date),
    CONSTRAINT inv_movements_reverses_fk    FOREIGN KEY (property_id, reverses_id)          REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_movements_debit_fk       FOREIGN KEY (property_id, gl_debit_account_id)  REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_movements_credit_fk      FOREIGN KEY (property_id, gl_credit_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_movements_department_fk  FOREIGN KEY (property_id, gl_department_id)    REFERENCES departments (property_id, id),
    CONSTRAINT inv_movements_outlet_fk      FOREIGN KEY (property_id, outlet_id)            REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_movements_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_movements_source_uk      UNIQUE (property_id, source_type, source_id, source_line, kind),
    CONSTRAINT inv_movements_reverses_uk    UNIQUE (reverses_id),
    CONSTRAINT inv_movements_kind_ck        CHECK (kind IN ('OPENING', 'RECEIPT', 'RECEIPT_REVERSAL', 'SUPPLIER_RETURN', 'TRANSFER_OUT', 'TRANSFER_IN', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT',
                                                    'COUNT_GAIN', 'COUNT_LOSS', 'POS_SALE', 'POS_SALE_RETURN', 'COGS_TO_WASTE', 'COST_ADJUST', 'NEG_SETTLE')),
    CONSTRAINT inv_movements_qty_ck         CHECK (qty_delta <> 0 OR kind IN ('COST_ADJUST', 'NEG_SETTLE', 'COGS_TO_WASTE')),
    CONSTRAINT inv_movements_sign_ck        CHECK ((qty_delta > 0 AND value_delta >= 0) OR (qty_delta < 0 AND value_delta <= 0) OR qty_delta = 0),
    CONSTRAINT inv_movements_gl_ck          CHECK ((gl_debit_account_id IS NULL) = (gl_credit_account_id IS NULL) AND gl_amount >= 0 AND (gl_debit_account_id IS NOT NULL OR gl_amount = 0)),
    CONSTRAINT inv_movements_item_class_ck  CHECK (item_class IN ('FOOD', 'BEVERAGE', 'OTHER')),
    CONSTRAINT inv_movements_cost_class_ck  CHECK (cost_class IN ('FOOD', 'BEVERAGE', 'OTHER'))
);
CREATE INDEX inv_movements_card_idx   ON inv_movements (warehouse_id, item_id, business_date, id);
CREATE INDEX inv_movements_day_idx    ON inv_movements (property_id, business_date);
CREATE INDEX inv_movements_cost_idx   ON inv_movements (property_id, business_date, kind, cost_class, outlet_id);
CREATE INDEX inv_movements_source_idx ON inv_movements (property_id, source_type, source_id);

CREATE TRIGGER inv_movements_append_only BEFORE UPDATE OR DELETE ON inv_movements FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER inv_movements_no_truncate BEFORE TRUNCATE ON inv_movements FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER inv_movements_business_day_open BEFORE INSERT ON inv_movements FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');

-- The first movement of a property stamps the date the costing method is locked from (35-inventory.md F11). The UPDATE only touches the row while it is still NULL, so a locked property's row is not written again.
-- +goose StatementBegin
CREATE FUNCTION inv_movements_lock_costing() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    UPDATE inventory_settings SET costing_locked_on = NEW.business_date WHERE property_id = NEW.property_id AND costing_locked_on IS NULL;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER inv_movements_lock_costing AFTER INSERT ON inv_movements FOR EACH ROW EXECUTE FUNCTION inv_movements_lock_costing();

CREATE TABLE inv_opening_lines (
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    opening_id   bigint        NOT NULL,
    line_no      int           NOT NULL,
    warehouse_id bigint        NOT NULL,
    item_id      bigint        NOT NULL,
    qty_stock    numeric(18,6) NOT NULL,
    unit_cost    numeric(18,6) NOT NULL,
    value        numeric(18,3) NOT NULL,
    movement_id  bigint        NOT NULL,
    PRIMARY KEY (opening_id, line_no),
    CONSTRAINT inv_opening_lines_opening_fk   FOREIGN KEY (property_id, opening_id)   REFERENCES inv_openings (property_id, id),
    CONSTRAINT inv_opening_lines_item_fk      FOREIGN KEY (property_id, item_id)      REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_opening_lines_warehouse_fk FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_opening_lines_movement_fk  FOREIGN KEY (property_id, movement_id)  REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_opening_lines_tenant_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_opening_lines_once_uk      UNIQUE (property_id, warehouse_id, item_id),   -- one opening per item and warehouse
    CONSTRAINT inv_opening_lines_ck           CHECK (qty_stock > 0 AND unit_cost >= 0 AND value >= 0)
);

-- 6. Balances, layers, shortfalls: derived from the ledger, changed only by the ledger writer ----------------------------------------------------
CREATE TABLE inv_balances (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    warehouse_id    bigint        NOT NULL,
    item_id         bigint        NOT NULL,
    qty             numeric(18,6) NOT NULL DEFAULT 0,
    value           numeric(18,3) NOT NULL DEFAULT 0,
    last_unit_cost  numeric(18,6),
    version         int           NOT NULL DEFAULT 1,
    updated_at      timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT inv_balances_property_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_balances_warehouse_fk   FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_balances_item_fk        FOREIGN KEY (property_id, item_id)      REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_balances_uk             UNIQUE (warehouse_id, item_id),
    CONSTRAINT inv_balances_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_balances_empty_ck       CHECK (qty <> 0 OR value = 0),
    CONSTRAINT inv_balances_sign_ck        CHECK ((qty >= 0 AND value >= 0) OR (qty <= 0 AND value <= 0))
);
CREATE INDEX inv_balances_item_idx ON inv_balances (property_id, item_id);

CREATE TABLE inv_layers (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    warehouse_id    bigint        NOT NULL,
    item_id         bigint        NOT NULL,
    movement_id     bigint        NOT NULL,
    received_on     date          NOT NULL,
    qty_in          numeric(18,6) NOT NULL,
    qty_remaining   numeric(18,6) NOT NULL,
    value_remaining numeric(18,3) NOT NULL,
    CONSTRAINT inv_layers_property_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_layers_warehouse_fk   FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_layers_item_fk        FOREIGN KEY (property_id, item_id)      REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_layers_movement_fk    FOREIGN KEY (property_id, movement_id)  REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_layers_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_layers_qty_ck         CHECK (qty_in > 0 AND qty_remaining >= 0 AND qty_remaining <= qty_in),
    CONSTRAINT inv_layers_empty_ck       CHECK (qty_remaining <> 0 OR value_remaining = 0),
    CONSTRAINT inv_layers_value_ck       CHECK (value_remaining >= 0)
);
CREATE INDEX inv_layers_fifo_idx ON inv_layers (warehouse_id, item_id, received_on, id) WHERE qty_remaining > 0;

-- What an OUT took from which layer, so a return to stock can restore it (35-inventory.md 4.3). Append-only.
CREATE TABLE inv_layer_uses (
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    movement_id bigint        NOT NULL,
    layer_id    bigint        NOT NULL,
    qty         numeric(18,6) NOT NULL,
    value       numeric(18,3) NOT NULL,
    PRIMARY KEY (movement_id, layer_id),
    CONSTRAINT inv_layer_uses_tenant_fk   FOREIGN KEY (tenant_id, property_id)   REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_layer_uses_movement_fk FOREIGN KEY (property_id, movement_id) REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_layer_uses_layer_fk    FOREIGN KEY (property_id, layer_id)    REFERENCES inv_layers (property_id, id),
    CONSTRAINT inv_layer_uses_ck          CHECK (qty > 0 AND value >= 0)
);
CREATE TRIGGER inv_layer_uses_append_only BEFORE UPDATE OR DELETE ON inv_layer_uses FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER inv_layer_uses_no_truncate BEFORE TRUNCATE ON inv_layer_uses FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

CREATE TABLE inv_shortfalls (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    warehouse_id        bigint        NOT NULL,
    item_id             bigint        NOT NULL,
    movement_id         bigint        NOT NULL,
    qty_open            numeric(18,6) NOT NULL,
    qty_original        numeric(18,6) NOT NULL,
    est_unit_cost       numeric(18,6) NOT NULL,
    est_value_open      numeric(18,3) NOT NULL,
    gl_debit_account_id bigint,
    gl_department_id    bigint,
    source_type         varchar(20)   NOT NULL,
    cost_class          varchar(10)   NOT NULL,
    outlet_id           bigint,
    status              varchar(16)   NOT NULL DEFAULT 'OPEN',
    business_date       date          NOT NULL,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT inv_shortfalls_property_fk    FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_shortfalls_warehouse_fk   FOREIGN KEY (property_id, warehouse_id)        REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_shortfalls_item_fk        FOREIGN KEY (property_id, item_id)             REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_shortfalls_movement_fk    FOREIGN KEY (property_id, movement_id)         REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_shortfalls_debit_fk       FOREIGN KEY (property_id, gl_debit_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_shortfalls_department_fk  FOREIGN KEY (property_id, gl_department_id)    REFERENCES departments (property_id, id),
    CONSTRAINT inv_shortfalls_outlet_fk      FOREIGN KEY (property_id, outlet_id)           REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_shortfalls_movement_uk    UNIQUE (movement_id),
    CONSTRAINT inv_shortfalls_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_shortfalls_qty_ck         CHECK (qty_original > 0 AND qty_open >= 0 AND qty_open <= qty_original),
    CONSTRAINT inv_shortfalls_status_ck      CHECK (status IN ('OPEN', 'SETTLED', 'CLOSED_BY_COUNT')),
    CONSTRAINT inv_shortfalls_open_ck        CHECK ((status = 'OPEN') = (qty_open > 0) AND (qty_open > 0 OR est_value_open = 0) AND est_value_open >= 0),
    CONSTRAINT inv_shortfalls_source_ck      CHECK (source_type IN ('POS_SALE', 'AMENITY')),
    CONSTRAINT inv_shortfalls_class_ck       CHECK (cost_class IN ('FOOD', 'BEVERAGE', 'OTHER'))
);
CREATE INDEX inv_shortfalls_open_idx ON inv_shortfalls (warehouse_id, item_id, id) WHERE status = 'OPEN';

-- 7. Document numbers of the openings ---------------------------------------------------------------------------------------------------------
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_OPENING', 'OPN-' FROM properties;

-- 8. The system account keys ----------------------------------------------------------------------------------------------------------------
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER', 'COMP',
                                                             'INV_GRNI', 'INV_PPV', 'INV_WASTE', 'INV_COUNT_VARIANCE', 'INV_COGS_FOOD', 'INV_COGS_BEVERAGE', 'INV_STAFF_MEAL', 'INV_ENTERTAINMENT'));

-- 8b. The currency lock (00058): a stock movement is an amount of money recorded in the currency, so a property that has one cannot change its currency or its decimals.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION property_has_financial_data(p_property_id bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM folio_items            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM payments               WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM gl_journals            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_bills         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_payments      WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_credit_notes  WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_receipts   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_invoices   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_adjustments WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM cashier_shifts         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM bank_statements        WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_returns            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_payments           WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_opening_credits    WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM budgets                WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM inv_movements          WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

-- The standard chart of a NEW property: 2130 under the payables, and 5290, 5295, 5299 under the food and beverage department expenses.
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v6;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_v6()
    UNION ALL
    SELECT '2130', 'Goods received not invoiced', 'LIABILITY', 'CREDIT', '2100', true, 'PAYABLES'
    UNION ALL
    SELECT '5290', 'F&B stock waste and spoilage', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'
    UNION ALL
    SELECT '5295', 'Stock count variance', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'
    UNION ALL
    SELECT '5299', 'Purchase price variance', 'EXPENSE', 'DEBIT', '5200', true, 'EXP_FB'
$$;
-- +goose StatementEnd
ALTER FUNCTION usali_chart_names(text) RENAME TO usali_chart_names_v4;
-- +goose StatementBegin
CREATE FUNCTION usali_chart_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_names_v4(p_lang)
    UNION ALL
    SELECT '2130', 'Barang diterima belum ditagih' WHERE p_lang = 'id'
    UNION ALL
    SELECT '5290', 'Susut dan kerusakan stok F&B' WHERE p_lang = 'id'
    UNION ALL
    SELECT '5295', 'Selisih opname stok' WHERE p_lang = 'id'
    UNION ALL
    SELECT '5299', 'Selisih harga beli' WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- The properties that exist. Each one that has a chart (an accounting_settings row) and no key INV_GRNI yet gets the four accounts and the eight keys:
--   * the new accounts take their standard code, or, when the code is the user's already (it is neither used nor overwritten), the next free code after it in a short range (2131 to 2139 for 2130,
--     5291 to 5294 for 5290, 5296 to 5298 for 5295); 5299 has no range after it, so a property that uses 5299 for something else stops the migration with a message that names the key;
--   * the cost-of-sales and expense keys point at the accounts that already exist in the chart (5210, 5220, 5240, 6170), when they are there;
--   * a property that has already the key INV_GRNI is left as it is.
-- The names are Indonesian when the account 1150 of the property has its Indonesian standard name, else English. The number of properties that took a replacement code is reported.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_idn boolean;
    v_spec record;
    v_code text;
    v_id bigint;
    v_parent bigint;
    n int;
    v_added int := 0;
    v_replaced int := 0;
    v_kept int := 0;
BEGIN
    FOR r IN SELECT s.tenant_id, s.property_id FROM accounting_settings s ORDER BY s.property_id LOOP
        IF EXISTS (SELECT 1 FROM gl_account_map m WHERE m.property_id = r.property_id AND m.map_key = 'INV_GRNI') THEN
            v_kept := v_kept + 1;
            CONTINUE;
        END IF;
        v_idn := EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = '1150'
                            AND a.name = (SELECT c.name FROM usali_chart_names('id') c WHERE c.code = '1150'));
        FOR v_spec IN
            SELECT * FROM (VALUES
                ('INV_GRNI',           '2130', 'Goods received not invoiced',     'Barang diterima belum ditagih',  'LIABILITY', 'CREDIT', '2100', 'PAYABLES', 2130, 2139),
                ('INV_WASTE',          '5290', 'F&B stock waste and spoilage',    'Susut dan kerusakan stok F&B',   'EXPENSE',   'DEBIT',  '5200', 'EXP_FB',   5290, 5294),
                ('INV_COUNT_VARIANCE', '5295', 'Stock count variance',            'Selisih opname stok',            'EXPENSE',   'DEBIT',  '5200', 'EXP_FB',   5295, 5298),
                ('INV_PPV',            '5299', 'Purchase price variance',         'Selisih harga beli',             'EXPENSE',   'DEBIT',  '5200', 'EXP_FB',   5299, 5299)
            ) AS t (map_key, std_code, name_en, name_id, account_type, normal_side, parent_code, statement_group, first_code, last_code)
        LOOP
            v_code := v_spec.std_code;
            IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = v_code) THEN
                v_code := NULL;
                FOR n IN v_spec.first_code + 1 .. v_spec.last_code LOOP
                    IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = n::text) THEN
                        v_code := n::text;
                        EXIT;
                    END IF;
                END LOOP;
                IF v_code IS NULL THEN
                    RAISE EXCEPTION '00084: property % has no free account code from % to % for the account of the key %', r.property_id, v_spec.std_code, v_spec.last_code, v_spec.map_key;
                END IF;
                v_replaced := v_replaced + 1;
            END IF;
            SELECT p.id INTO v_parent FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = v_spec.parent_code;
            INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
            VALUES (r.tenant_id, r.property_id, v_code, CASE WHEN v_idn THEN v_spec.name_id ELSE v_spec.name_en END, v_spec.account_type, v_spec.normal_side, v_parent, true, v_spec.statement_group)
            RETURNING id INTO v_id;
            INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (r.tenant_id, r.property_id, v_spec.map_key, v_id);
        END LOOP;
        -- the keys that point at accounts of the chart that were there already
        INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id)
        SELECT r.tenant_id, r.property_id, m.map_key, a.id
          FROM gl_accounts a
          JOIN (VALUES ('INV_COGS_FOOD', '5210'), ('INV_COGS_BEVERAGE', '5220'), ('INV_STAFF_MEAL', '5240'), ('INV_ENTERTAINMENT', '6170')) AS m (map_key, code) ON m.code = a.code
         WHERE a.property_id = r.property_id AND a.account_type = 'EXPENSE'
        ON CONFLICT (property_id, map_key) DO NOTHING;
        v_added := v_added + 1;
    END LOOP;
    RAISE NOTICE '00084: inventory accounts and keys added to % propert(y/ies), % replacement code(s) because the standard one was taken, % already had the key', v_added, v_replaced, v_kept;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Refused while the module holds anything: a setting, a unit, a category, an item, a warehouse, a movement. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM inventory_settings) + (SELECT count(*) FROM inv_units) + (SELECT count(*) FROM inv_categories) + (SELECT count(*) FROM inv_items)
         + (SELECT count(*) FROM inv_warehouses) + (SELECT count(*) FROM inv_movements) + (SELECT count(*) FROM inv_openings) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00084: the inventory module holds % row(s) (settings, units, categories, items, warehouses, movements, openings)', v_n; END IF;
END
$$;
-- +goose StatementEnd

-- The accounts of each property are found through their keys (not through their codes, which may be replacements). The keys go. An account goes too when it is still what the migration made
-- (its standard name) and nothing uses it: no journal line, no other key, no child, nothing that refers to it. Otherwise it stays, as an ordinary account.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_dropped int := 0;
    v_kept int := 0;
BEGIN
    FOR r IN SELECT m.property_id, m.map_key, m.account_id FROM gl_account_map m
              WHERE m.map_key IN ('INV_GRNI', 'INV_PPV', 'INV_WASTE', 'INV_COUNT_VARIANCE', 'INV_COGS_FOOD', 'INV_COGS_BEVERAGE', 'INV_STAFF_MEAL', 'INV_ENTERTAINMENT') ORDER BY m.property_id LOOP
        DELETE FROM gl_account_map WHERE property_id = r.property_id AND map_key = r.map_key;
        IF r.map_key IN ('INV_GRNI', 'INV_PPV', 'INV_WASTE', 'INV_COUNT_VARIANCE') THEN
            BEGIN
                DELETE FROM gl_accounts a
                 WHERE a.property_id = r.property_id AND a.id = r.account_id
                   AND a.name IN ('Goods received not invoiced', 'Barang diterima belum ditagih', 'F&B stock waste and spoilage', 'Susut dan kerusakan stok F&B',
                                  'Stock count variance', 'Selisih opname stok', 'Purchase price variance', 'Selisih harga beli')
                   AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = a.property_id AND l.account_id = a.id);
                IF FOUND THEN v_dropped := v_dropped + 1; ELSE v_kept := v_kept + 1; END IF;
            EXCEPTION WHEN foreign_key_violation THEN
                v_kept := v_kept + 1;
            END;
        END IF;
    END LOOP;
    RAISE NOTICE '00084 down: % inventory account(s) deleted, % kept as ordinary accounts', v_dropped, v_kept;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION property_has_financial_data(p_property_id bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM folio_items            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM payments               WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM gl_journals            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_bills         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_payments      WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_credit_notes  WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_receipts   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_invoices   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_adjustments WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM cashier_shifts         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM bank_statements        WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_returns            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_payments           WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_opening_credits    WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM budgets                WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

DROP FUNCTION usali_chart_names(text);
ALTER FUNCTION usali_chart_names_v4(text) RENAME TO usali_chart_names;
DROP FUNCTION usali_chart();
ALTER FUNCTION usali_chart_v6() RENAME TO usali_chart;

ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER', 'COMP'));

DELETE FROM document_sequences WHERE sequence_type = 'INV_OPENING';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT'));

DROP TABLE inv_shortfalls;
DROP TABLE inv_layer_uses;
DROP TABLE inv_layers;
DROP TABLE inv_balances;
DROP TABLE inv_opening_lines;
DROP TRIGGER inv_movements_lock_costing ON inv_movements;
DROP FUNCTION inv_movements_lock_costing();
DROP TABLE inv_movements;
DROP TABLE inv_openings;
DROP TABLE inv_levels;
DROP TABLE inv_warehouses;
DROP TABLE inv_item_units;
DROP TABLE inv_items;
DROP TRIGGER inv_categories_accounts ON inv_categories;
DROP TABLE inv_categories;
DROP FUNCTION inv_categories_accounts_check();
DROP TABLE inv_units;
DROP TRIGGER inventory_settings_costing_locked ON inventory_settings;
DROP TABLE inventory_settings;
DROP FUNCTION inventory_costing_method_is_locked();
