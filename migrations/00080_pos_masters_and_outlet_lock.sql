-- +goose Up
-- POS, stage 1 (docs/architecture/34-pos.md, PR 1): the master data of the outlets and the lock of a folio for outlet charges.
--
-- 1. Masters of the outlets: rule profiles (tax and service of an outlet or of a product), printers and stations (no agent yet), outlets, price lists, categories, products with their
--    variants and add-ons, "sold out today", the recipe structure (waits for inventory), the floor plan (areas, tables, marks).
-- 2. The lock of a folio for outlet charges: closed by default (no row), opened by the front desk with a limit, a validity and the outlets that may charge it. Every change is an event;
--    every attempt of an outlet to charge a room is a row of the attempts table, accepted or refused.
--
-- Nothing here touches the ledger: no folio, folio item, payment or journal changes. The bills (the OUTLET folio, the orders, the charge to room) are stage 2.

-- ---------------------------------------------------------------- rule profiles
CREATE TABLE pos_rule_profiles (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    price_mode  varchar(10)  NOT NULL DEFAULT 'EXCLUSIVE',
    is_active   boolean      NOT NULL DEFAULT true,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT pos_rule_profiles_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_rule_profiles_code_uk        UNIQUE (property_id, code),
    CONSTRAINT pos_rule_profiles_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_rule_profiles_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT pos_rule_profiles_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_rule_profiles_mode_ck        CHECK (price_mode IN ('EXCLUSIVE', 'INCLUSIVE'))
);
CREATE TRIGGER pos_rule_profiles_set_updated_at BEFORE UPDATE ON pos_rule_profiles FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE pos_rule_profile_rules (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint   NOT NULL,
    property_id       bigint   NOT NULL,
    profile_id        bigint   NOT NULL,
    tax_id            bigint,
    service_charge_id bigint,
    sequence          smallint NOT NULL,
    CONSTRAINT pos_rule_profile_rules_property_fk FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_rule_profile_rules_profile_fk  FOREIGN KEY (property_id, profile_id)      REFERENCES pos_rule_profiles (property_id, id),
    CONSTRAINT pos_rule_profile_rules_tax_fk      FOREIGN KEY (property_id, tax_id)          REFERENCES taxes (property_id, id),
    CONSTRAINT pos_rule_profile_rules_service_fk  FOREIGN KEY (property_id, service_charge_id) REFERENCES service_charges (property_id, id),
    CONSTRAINT pos_rule_profile_rules_one_ck      CHECK ((tax_id IS NULL) <> (service_charge_id IS NULL)),
    CONSTRAINT pos_rule_profile_rules_seq_ck      CHECK (sequence >= 0),
    CONSTRAINT pos_rule_profile_rules_tax_uk      UNIQUE (profile_id, tax_id),
    CONSTRAINT pos_rule_profile_rules_service_uk  UNIQUE (profile_id, service_charge_id)
);

-- ---------------------------------------------------------------- printers and stations
CREATE TABLE pos_printers (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    name            varchar(100) NOT NULL,
    kind            varchar(10)  NOT NULL,
    connection_info varchar(200) NOT NULL DEFAULT '',
    paper_width_mm  smallint     NOT NULL DEFAULT 80,
    codepage        varchar(20)  NOT NULL DEFAULT 'CP437',
    opens_drawer    boolean      NOT NULL DEFAULT false,
    is_active       boolean      NOT NULL DEFAULT true,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    updated_by      bigint REFERENCES users (id),
    CONSTRAINT pos_printers_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_printers_name_uk        UNIQUE (property_id, name),
    CONSTRAINT pos_printers_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_printers_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_printers_kind_ck        CHECK (kind IN ('RECEIPT', 'KITCHEN', 'LABEL')),
    CONSTRAINT pos_printers_width_ck       CHECK (paper_width_mm IN (58, 80))
);
CREATE TRIGGER pos_printers_set_updated_at BEFORE UPDATE ON pos_printers FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE pos_stations (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint       NOT NULL,
    property_id        bigint       NOT NULL,
    code               varchar(20)  NOT NULL,
    name               varchar(100) NOT NULL,
    printer_id         bigint,
    kds_enabled        boolean      NOT NULL DEFAULT false,
    late_after_minutes smallint     NOT NULL DEFAULT 15,
    is_active          boolean      NOT NULL DEFAULT true,
    created_at         timestamptz  NOT NULL DEFAULT now(),
    created_by         bigint REFERENCES users (id),
    updated_at         timestamptz  NOT NULL DEFAULT now(),
    updated_by         bigint REFERENCES users (id),
    CONSTRAINT pos_stations_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_stations_printer_fk     FOREIGN KEY (property_id, printer_id) REFERENCES pos_printers (property_id, id),
    CONSTRAINT pos_stations_code_uk        UNIQUE (property_id, code),
    CONSTRAINT pos_stations_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_stations_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT pos_stations_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_stations_late_ck        CHECK (late_after_minutes BETWEEN 1 AND 240)
);
CREATE TRIGGER pos_stations_set_updated_at BEFORE UPDATE ON pos_stations FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- price lists and outlets
CREATE TABLE pos_price_lists (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    is_active   boolean      NOT NULL DEFAULT true,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT pos_price_lists_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_price_lists_code_uk        UNIQUE (property_id, code),
    CONSTRAINT pos_price_lists_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_price_lists_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT pos_price_lists_name_ck        CHECK (length(btrim(name)) > 0)
);
CREATE TRIGGER pos_price_lists_set_updated_at BEFORE UPDATE ON pos_price_lists FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE pos_outlets (
    id                           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                    bigint        NOT NULL,
    property_id                  bigint        NOT NULL,
    code                         varchar(6)    NOT NULL,
    name                         varchar(100)  NOT NULL,
    outlet_type                  varchar(20)   NOT NULL,
    order_mode                   varchar(10)   NOT NULL,
    department_id                bigint        NOT NULL,
    rule_profile_id              bigint        NOT NULL,
    price_list_id                bigint        NOT NULL,
    drawer                       varchar(20),
    receipt_printer_id           bigint,
    uses_tables                  boolean       NOT NULL DEFAULT true,
    guest_count_required         boolean       NOT NULL DEFAULT false,
    rounding_unit                smallint      NOT NULL DEFAULT 0,
    allow_room_charge            boolean       NOT NULL DEFAULT true,
    allow_company_charge         boolean       NOT NULL DEFAULT false,
    allow_comp                   boolean       NOT NULL DEFAULT false,
    comp_treatment               varchar(14)   NOT NULL DEFAULT 'PAYMENT_COMP',
    max_discount_pct_no_approval numeric(5,2)  NOT NULL DEFAULT 0,
    block_night_audit_with_open_bills boolean  NOT NULL DEFAULT false,
    next_bill_no                 bigint        NOT NULL DEFAULT 1,
    is_active                    boolean       NOT NULL DEFAULT true,
    sort_order                   int           NOT NULL DEFAULT 0,
    version                      int           NOT NULL DEFAULT 1,
    created_at                   timestamptz   NOT NULL DEFAULT now(),
    created_by                   bigint REFERENCES users (id),
    updated_at                   timestamptz   NOT NULL DEFAULT now(),
    updated_by                   bigint REFERENCES users (id),
    CONSTRAINT pos_outlets_property_fk    FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_outlets_department_fk  FOREIGN KEY (property_id, department_id)    REFERENCES departments (property_id, id),
    CONSTRAINT pos_outlets_profile_fk     FOREIGN KEY (property_id, rule_profile_id)  REFERENCES pos_rule_profiles (property_id, id),
    CONSTRAINT pos_outlets_price_list_fk  FOREIGN KEY (property_id, price_list_id)    REFERENCES pos_price_lists (property_id, id),
    CONSTRAINT pos_outlets_printer_fk     FOREIGN KEY (property_id, receipt_printer_id) REFERENCES pos_printers (property_id, id),
    CONSTRAINT pos_outlets_code_uk        UNIQUE (property_id, code),
    CONSTRAINT pos_outlets_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_outlets_code_ck        CHECK (code ~ '^[A-Z]{1,6}$' AND code NOT IN ('FOL', 'PAY', 'RSV', 'STY', 'SHF', 'TRF', 'CNF')),
    CONSTRAINT pos_outlets_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_outlets_type_ck        CHECK (outlet_type IN ('RESTAURANT', 'BAR', 'CAFE', 'ROOM_SERVICE', 'SPA', 'LAUNDRY', 'BUSINESS_CENTER', 'MINIBAR', 'OTHER')),
    CONSTRAINT pos_outlets_mode_ck        CHECK (order_mode IN ('PRINT', 'KDS', 'DIRECT')),
    CONSTRAINT pos_outlets_rounding_ck    CHECK (rounding_unit IN (0, 100, 500, 1000)),
    CONSTRAINT pos_outlets_comp_ck        CHECK (comp_treatment IN ('PAYMENT_COMP', 'DISCOUNT_100')),
    CONSTRAINT pos_outlets_discount_ck    CHECK (max_discount_pct_no_approval BETWEEN 0 AND 100),
    CONSTRAINT pos_outlets_drawer_ck      CHECK (drawer IS NULL OR length(btrim(drawer)) > 0)
);
CREATE TRIGGER pos_outlets_set_updated_at BEFORE UPDATE ON pos_outlets FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------- catalogue
CREATE TABLE pos_categories (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    parent_id   bigint,
    sort_order  int          NOT NULL DEFAULT 0,
    is_active   boolean      NOT NULL DEFAULT true,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT pos_categories_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_categories_parent_fk      FOREIGN KEY (property_id, parent_id) REFERENCES pos_categories (property_id, id),
    CONSTRAINT pos_categories_code_uk        UNIQUE (property_id, code),
    CONSTRAINT pos_categories_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_categories_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT pos_categories_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_categories_self_ck        CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE TRIGGER pos_categories_set_updated_at BEFORE UPDATE ON pos_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE pos_products (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    code            varchar(30)  NOT NULL,
    name            varchar(100) NOT NULL,
    short_name      varchar(24),
    category_id     bigint       NOT NULL,
    charge_code_id  bigint       NOT NULL,
    rule_profile_id bigint,
    station_id      bigint,
    is_active       boolean      NOT NULL DEFAULT true,
    version         int          NOT NULL DEFAULT 1,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    updated_by      bigint REFERENCES users (id),
    CONSTRAINT pos_products_property_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_products_category_fk    FOREIGN KEY (property_id, category_id)     REFERENCES pos_categories (property_id, id),
    CONSTRAINT pos_products_charge_code_fk FOREIGN KEY (property_id, charge_code_id)  REFERENCES charge_codes (property_id, id),
    CONSTRAINT pos_products_profile_fk     FOREIGN KEY (property_id, rule_profile_id) REFERENCES pos_rule_profiles (property_id, id),
    CONSTRAINT pos_products_station_fk     FOREIGN KEY (property_id, station_id)      REFERENCES pos_stations (property_id, id),
    CONSTRAINT pos_products_code_uk        UNIQUE (property_id, code),
    CONSTRAINT pos_products_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_products_code_ck        CHECK (length(btrim(code)) > 0),
    CONSTRAINT pos_products_name_ck        CHECK (length(btrim(name)) > 0)
);
CREATE INDEX pos_products_category_idx ON pos_products (property_id, category_id);
CREATE TRIGGER pos_products_set_updated_at BEFORE UPDATE ON pos_products FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Variants (kind VARIANT: pick exactly one of the group when it is required, price difference) and add-ons (kind MODIFIER: priced or not, with a limit on how many).
CREATE TABLE pos_option_groups (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    product_id  bigint       NOT NULL,
    name        varchar(100) NOT NULL,
    kind        varchar(8)   NOT NULL,
    min_select  smallint     NOT NULL DEFAULT 0,
    max_select  smallint     NOT NULL DEFAULT 1,
    sort_order  int          NOT NULL DEFAULT 0,
    CONSTRAINT pos_option_groups_property_fk    FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_option_groups_product_fk     FOREIGN KEY (property_id, product_id) REFERENCES pos_products (property_id, id),
    CONSTRAINT pos_option_groups_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_option_groups_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_option_groups_kind_ck        CHECK (kind IN ('VARIANT', 'MODIFIER')),
    CONSTRAINT pos_option_groups_range_ck       CHECK (min_select >= 0 AND max_select >= 1 AND min_select <= max_select),
    CONSTRAINT pos_option_groups_variant_ck     CHECK (kind <> 'VARIANT' OR (max_select = 1 AND min_select <= 1))
);
CREATE INDEX pos_option_groups_product_idx ON pos_option_groups (property_id, product_id);

CREATE TABLE pos_options (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    group_id    bigint        NOT NULL,
    name        varchar(100)  NOT NULL,
    price_delta numeric(18,3) NOT NULL DEFAULT 0,
    is_default  boolean       NOT NULL DEFAULT false,
    max_qty     smallint      NOT NULL DEFAULT 1,
    sort_order  int           NOT NULL DEFAULT 0,
    is_active   boolean       NOT NULL DEFAULT true,
    CONSTRAINT pos_options_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_options_group_fk       FOREIGN KEY (property_id, group_id)  REFERENCES pos_option_groups (property_id, id),
    CONSTRAINT pos_options_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_options_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_options_qty_ck         CHECK (max_qty BETWEEN 1 AND 99)
);
CREATE INDEX pos_options_group_idx ON pos_options (property_id, group_id);

CREATE TABLE pos_price_list_items (
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    price_list_id bigint        NOT NULL,
    product_id    bigint        NOT NULL,
    price         numeric(18,3) NOT NULL,
    is_sold       boolean       NOT NULL DEFAULT true,
    station_id    bigint,
    updated_at    timestamptz   NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    PRIMARY KEY (price_list_id, product_id),
    CONSTRAINT pos_price_list_items_property_fk FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_price_list_items_list_fk     FOREIGN KEY (property_id, price_list_id)  REFERENCES pos_price_lists (property_id, id),
    CONSTRAINT pos_price_list_items_product_fk  FOREIGN KEY (property_id, product_id)     REFERENCES pos_products (property_id, id),
    CONSTRAINT pos_price_list_items_station_fk  FOREIGN KEY (property_id, station_id)     REFERENCES pos_stations (property_id, id),
    CONSTRAINT pos_price_list_items_price_ck    CHECK (price >= 0)
);

-- "Sold out today": counts only while business_date is the open business day, so it clears itself when the day changes.
CREATE TABLE pos_outlet_sold_out (
    tenant_id     bigint NOT NULL,
    property_id   bigint NOT NULL,
    outlet_id     bigint NOT NULL,
    product_id    bigint NOT NULL,
    business_date date   NOT NULL,
    marked_by     bigint REFERENCES users (id),
    marked_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (outlet_id, product_id),
    CONSTRAINT pos_outlet_sold_out_property_fk FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_outlet_sold_out_outlet_fk   FOREIGN KEY (property_id, outlet_id)    REFERENCES pos_outlets (property_id, id),
    CONSTRAINT pos_outlet_sold_out_product_fk  FOREIGN KEY (property_id, product_id)   REFERENCES pos_products (property_id, id)
);

-- The structure of a recipe, for the inventory module (PR 4): no endpoint and no screen writes it yet. inventory_item_id gets its foreign key when that module exists.
CREATE TABLE pos_recipe_lines (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint         NOT NULL,
    property_id       bigint         NOT NULL,
    product_id        bigint         NOT NULL,
    option_id         bigint,
    inventory_item_id bigint,
    quantity          numeric(12,4)  NOT NULL,
    unit              varchar(10)    NOT NULL,
    note              varchar(200),
    CONSTRAINT pos_recipe_lines_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_recipe_lines_product_fk  FOREIGN KEY (property_id, product_id) REFERENCES pos_products (property_id, id),
    CONSTRAINT pos_recipe_lines_option_fk   FOREIGN KEY (property_id, option_id)  REFERENCES pos_options (property_id, id),
    CONSTRAINT pos_recipe_lines_qty_ck      CHECK (quantity > 0)
);

-- ---------------------------------------------------------------- floor plan
CREATE TABLE pos_areas (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint       NOT NULL,
    property_id    bigint       NOT NULL,
    outlet_id      bigint       NOT NULL,
    name           varchar(100) NOT NULL,
    sort_order     int          NOT NULL DEFAULT 0,
    grid_w         smallint     NOT NULL DEFAULT 40,
    grid_h         smallint     NOT NULL DEFAULT 24,
    layout_version int          NOT NULL DEFAULT 1,
    is_active      boolean      NOT NULL DEFAULT true,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    updated_by     bigint REFERENCES users (id),
    CONSTRAINT pos_areas_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_areas_outlet_fk      FOREIGN KEY (property_id, outlet_id) REFERENCES pos_outlets (property_id, id),
    CONSTRAINT pos_areas_name_uk        UNIQUE (outlet_id, name),
    CONSTRAINT pos_areas_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_areas_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_areas_grid_ck        CHECK (grid_w BETWEEN 10 AND 200 AND grid_h BETWEEN 10 AND 200)
);
CREATE TRIGGER pos_areas_set_updated_at BEFORE UPDATE ON pos_areas FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE pos_tables (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    area_id     bigint      NOT NULL,
    code        varchar(10) NOT NULL,
    capacity    smallint    NOT NULL DEFAULT 2,
    shape       varchar(6)  NOT NULL DEFAULT 'SQUARE',
    x           smallint    NOT NULL,
    y           smallint    NOT NULL,
    w           smallint    NOT NULL DEFAULT 4,
    h           smallint    NOT NULL DEFAULT 4,
    is_active   boolean     NOT NULL DEFAULT true,
    CONSTRAINT pos_tables_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_tables_area_fk        FOREIGN KEY (property_id, area_id)   REFERENCES pos_areas (property_id, id),
    CONSTRAINT pos_tables_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_tables_code_ck        CHECK (length(btrim(code)) > 0),
    CONSTRAINT pos_tables_shape_ck       CHECK (shape IN ('SQUARE', 'RECT', 'ROUND')),
    CONSTRAINT pos_tables_capacity_ck    CHECK (capacity BETWEEN 1 AND 99),
    CONSTRAINT pos_tables_box_ck         CHECK (x >= 0 AND y >= 0 AND w BETWEEN 1 AND 60 AND h BETWEEN 1 AND 60)
);
CREATE INDEX pos_tables_area_idx ON pos_tables (property_id, area_id);
-- A table code is unique in its outlet (an area is of one outlet), among the tables in use.
-- +goose StatementBegin
CREATE FUNCTION pos_tables_code_unique() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.is_active AND EXISTS (
        SELECT 1 FROM pos_tables t JOIN pos_areas a ON a.property_id = t.property_id AND a.id = t.area_id
        WHERE t.property_id = NEW.property_id AND t.code = NEW.code AND t.is_active AND t.id <> NEW.id
          AND a.outlet_id = (SELECT outlet_id FROM pos_areas WHERE property_id = NEW.property_id AND id = NEW.area_id)
    ) THEN
        RAISE EXCEPTION 'table code % is already in use in this outlet', NEW.code USING ERRCODE = 'unique_violation', CONSTRAINT = 'pos_tables_code_uk';
    END IF;
    RETURN NEW;
END $$;
-- +goose StatementEnd
CREATE TRIGGER pos_tables_code_unique BEFORE INSERT OR UPDATE OF code, area_id, is_active ON pos_tables FOR EACH ROW EXECUTE FUNCTION pos_tables_code_unique();

CREATE TABLE pos_area_marks (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    area_id     bigint       NOT NULL,
    kind        varchar(8)   NOT NULL,
    label       varchar(40)  NOT NULL DEFAULT '',
    x           smallint     NOT NULL,
    y           smallint     NOT NULL,
    w           smallint     NOT NULL DEFAULT 4,
    h           smallint     NOT NULL DEFAULT 2,
    CONSTRAINT pos_area_marks_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_area_marks_area_fk     FOREIGN KEY (property_id, area_id)   REFERENCES pos_areas (property_id, id),
    CONSTRAINT pos_area_marks_kind_ck     CHECK (kind IN ('WINDOW', 'DOOR', 'CASHIER', 'OTHER')),
    CONSTRAINT pos_area_marks_box_ck      CHECK (x >= 0 AND y >= 0 AND w BETWEEN 1 AND 60 AND h BETWEEN 1 AND 60)
);
CREATE INDEX pos_area_marks_area_idx ON pos_area_marks (property_id, area_id);

-- ---------------------------------------------------------------- the lock of a folio for outlet charges
-- No row means LOCKED (the default). Usage against the limit is never stored: it is read from the charges (stage 2) under the folio's lock.
CREATE TABLE folio_outlet_locks (
    folio_id      bigint        PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    status        varchar(6)    NOT NULL,
    limit_amount  numeric(18,3) NOT NULL,
    valid_mode    varchar(11)   NOT NULL,
    valid_on      date,
    period_no     int           NOT NULL DEFAULT 1,
    locked_reason varchar(14),
    opened_by     bigint REFERENCES users (id),
    opened_at     timestamptz   NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    updated_at    timestamptz   NOT NULL DEFAULT now(),
    version       int           NOT NULL DEFAULT 1,
    CONSTRAINT folio_outlet_locks_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_outlet_locks_folio_fk    FOREIGN KEY (property_id, folio_id)  REFERENCES folios (property_id, id),
    CONSTRAINT folio_outlet_locks_status_ck   CHECK (status IN ('OPEN', 'LOCKED')),
    CONSTRAINT folio_outlet_locks_limit_ck    CHECK (limit_amount > 0),
    CONSTRAINT folio_outlet_locks_mode_ck     CHECK (valid_mode IN ('UNTIL_CLOSE', 'TODAY')),
    CONSTRAINT folio_outlet_locks_valid_ck    CHECK ((valid_mode = 'TODAY') = (valid_on IS NOT NULL)),
    CONSTRAINT folio_outlet_locks_reason_ck   CHECK (locked_reason IS NULL OR locked_reason IN ('MANUAL', 'LIMIT_REACHED', 'CHECKOUT', 'EXPIRED', 'FOLIO_CLOSED')),
    CONSTRAINT folio_outlet_locks_state_ck    CHECK ((status = 'LOCKED') = (locked_reason IS NOT NULL))
);
CREATE INDEX folio_outlet_locks_open_idx ON folio_outlet_locks (property_id) WHERE status = 'OPEN';
CREATE TRIGGER folio_outlet_locks_set_updated_at BEFORE UPDATE ON folio_outlet_locks FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE folio_outlet_lock_outlets (
    tenant_id   bigint NOT NULL,
    property_id bigint NOT NULL,
    folio_id    bigint NOT NULL,
    outlet_id   bigint NOT NULL,
    PRIMARY KEY (folio_id, outlet_id),
    CONSTRAINT folio_outlet_lock_outlets_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_outlet_lock_outlets_lock_fk     FOREIGN KEY (folio_id)               REFERENCES folio_outlet_locks (folio_id) ON DELETE CASCADE,
    CONSTRAINT folio_outlet_lock_outlets_outlet_fk   FOREIGN KEY (property_id, outlet_id) REFERENCES pos_outlets (property_id, id)
);

CREATE TABLE folio_outlet_lock_events (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    folio_id      bigint      NOT NULL,
    kind          varchar(8)  NOT NULL,
    reason        varchar(14),
    old_value     jsonb,
    new_value     jsonb,
    business_date date        NOT NULL,
    actor_id      bigint REFERENCES users (id),
    created_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT folio_outlet_lock_events_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_outlet_lock_events_folio_fk    FOREIGN KEY (property_id, folio_id)  REFERENCES folios (property_id, id),
    CONSTRAINT folio_outlet_lock_events_kind_ck     CHECK (kind IN ('OPENED', 'CHANGED', 'LOCKED'))
);
CREATE INDEX folio_outlet_lock_events_folio_idx ON folio_outlet_lock_events (property_id, folio_id, id);
CREATE TRIGGER folio_outlet_lock_events_append_only BEFORE UPDATE OR DELETE ON folio_outlet_lock_events FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- An outlet charge to a room that was accepted: what counts against the limit of the folio (never the balance), per lock period. Stage 2 fills bill_id and the transfer batch; the gate of
-- stage 1 writes the row so that the limit can be tested before the bills exist. A row is "returned" when the charge was returned or moved away through POS.
CREATE TABLE pos_room_charges (
    id                      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id               bigint        NOT NULL,
    property_id             bigint        NOT NULL,
    outlet_id               bigint        NOT NULL,
    bill_id                 bigint,
    bill_number             varchar(20),
    from_folio_id           bigint,
    to_folio_id             bigint        NOT NULL,
    stay_id                 bigint,
    amount                  numeric(18,3) NOT NULL,
    lock_period_no          int           NOT NULL,
    transfer_batch_id       bigint,
    returned_at             timestamptz,
    returned_by_transfer_id bigint,
    business_date           date          NOT NULL,
    idempotency_key         varchar(100),
    created_at              timestamptz   NOT NULL DEFAULT now(),
    created_by              bigint REFERENCES users (id),
    CONSTRAINT pos_room_charges_property_fk FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_room_charges_outlet_fk   FOREIGN KEY (property_id, outlet_id)           REFERENCES pos_outlets (property_id, id),
    CONSTRAINT pos_room_charges_to_fk       FOREIGN KEY (property_id, to_folio_id)         REFERENCES folios (property_id, id),
    CONSTRAINT pos_room_charges_from_fk     FOREIGN KEY (property_id, from_folio_id)       REFERENCES folios (property_id, id),
    CONSTRAINT pos_room_charges_batch_fk    FOREIGN KEY (property_id, transfer_batch_id)   REFERENCES folio_transfer_batches (property_id, id),
    CONSTRAINT pos_room_charges_day_fk      FOREIGN KEY (property_id, business_date)       REFERENCES business_days (property_id, business_date),
    CONSTRAINT pos_room_charges_batch_uk    UNIQUE (transfer_batch_id),
    CONSTRAINT pos_room_charges_key_uk      UNIQUE (property_id, idempotency_key),
    CONSTRAINT pos_room_charges_amount_ck   CHECK (amount > 0),
    CONSTRAINT pos_room_charges_return_ck   CHECK ((returned_at IS NULL) = (returned_by_transfer_id IS NULL) OR returned_by_transfer_id IS NULL)
);
CREATE INDEX pos_room_charges_folio_idx ON pos_room_charges (property_id, to_folio_id, lock_period_no) WHERE returned_at IS NULL;
CREATE TRIGGER pos_room_charges_business_day_open BEFORE INSERT ON pos_room_charges FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');

-- Every attempt of an outlet to charge a room, accepted or refused (stage 2 writes them; stage 1 has the gate and the list). bill_id and pos_room_charge_id get their foreign keys with the bills.
CREATE TABLE pos_room_charge_attempts (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    source             varchar(6)    NOT NULL,
    folio_id           bigint        NOT NULL,
    requested_stay_id  bigint,
    outlet_id          bigint        NOT NULL,
    bill_id            bigint,
    bill_number        varchar(20),
    amount             numeric(18,3) NOT NULL,
    result             varchar(8)    NOT NULL,
    code               varchar(40),
    context            jsonb         NOT NULL DEFAULT '{}'::jsonb,
    pos_room_charge_id bigint,
    business_date      date          NOT NULL,
    created_at         timestamptz   NOT NULL DEFAULT now(),
    created_by         bigint REFERENCES users (id),
    CONSTRAINT pos_room_charge_attempts_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_room_charge_attempts_folio_fk    FOREIGN KEY (property_id, folio_id)  REFERENCES folios (property_id, id),
    CONSTRAINT pos_room_charge_attempts_outlet_fk   FOREIGN KEY (property_id, outlet_id) REFERENCES pos_outlets (property_id, id),
    CONSTRAINT pos_room_charge_attempts_source_ck   CHECK (source IN ('CHARGE', 'MOVE')),
    CONSTRAINT pos_room_charge_attempts_result_ck   CHECK (result IN ('ACCEPTED', 'REJECTED')),
    CONSTRAINT pos_room_charge_attempts_amount_ck   CHECK (amount > 0),
    CONSTRAINT pos_room_charge_attempts_code_ck     CHECK ((result = 'REJECTED') = (code IS NOT NULL))
);
CREATE INDEX pos_room_charge_attempts_day_idx ON pos_room_charge_attempts (property_id, business_date, id DESC);
CREATE INDEX pos_room_charge_attempts_folio_idx ON pos_room_charge_attempts (property_id, folio_id, id DESC);
CREATE TRIGGER pos_room_charge_attempts_append_only BEFORE UPDATE OR DELETE ON pos_room_charge_attempts FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- +goose Down
-- The masters and the lock are configuration and a trail of it: refuse while an outlet, a product or a lock exists (a Down must not lose what a hotel set up).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pos_outlets) OR EXISTS (SELECT 1 FROM pos_products) OR EXISTS (SELECT 1 FROM folio_outlet_locks) OR EXISTS (SELECT 1 FROM pos_room_charge_attempts) OR EXISTS (SELECT 1 FROM pos_room_charges) THEN
        RAISE EXCEPTION 'migration 00080 cannot be undone while outlets, products or folio locks exist';
    END IF;
END $$;
-- +goose StatementEnd
DROP TABLE pos_room_charge_attempts;
DROP TABLE pos_room_charges;
DROP TABLE folio_outlet_lock_events;
DROP TABLE folio_outlet_lock_outlets;
DROP TABLE folio_outlet_locks;
DROP TABLE pos_area_marks;
DROP TABLE pos_tables;
DROP FUNCTION pos_tables_code_unique();
DROP TABLE pos_areas;
DROP TABLE pos_recipe_lines;
DROP TABLE pos_outlet_sold_out;
DROP TABLE pos_price_list_items;
DROP TABLE pos_options;
DROP TABLE pos_option_groups;
DROP TABLE pos_products;
DROP TABLE pos_categories;
DROP TABLE pos_outlets;
DROP TABLE pos_price_lists;
DROP TABLE pos_stations;
DROP TABLE pos_printers;
DROP TABLE pos_rule_profile_rules;
DROP TABLE pos_rule_profiles;
