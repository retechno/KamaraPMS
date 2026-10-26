-- +goose Up
-- Fixed assets, PR 1 (docs/architecture/37-fixed-assets.md, sections 3.1 and 3.2): the masters (settings, categories with their accounts, locations), the register (assets), the append-only register ledger,
-- the opening imports, and the fiscal groups as data. Nothing here is used by the folios, the payables or the night audit: the module is a new set of tables and a few new accounts.
--
--   fa_settings, fa_categories, fa_locations, fa_opening_imports, fa_assets, fa_ledger, fa_fiscal_groups
--   gl_accounts, usali_chart()     1570 computers and IT (cost), 1591 to 1595 accumulated depreciation by kind of asset, 7540 and 7550 depreciation of vehicles and of computers and IT (F2);
--                                  1590 becomes a header (the parent of 1591 to 1595) where it has no posting, else the new accounts sit under 1500
--   gl_journals                    journal_type accepts 'ASSET' (F3)
--   document_sequences             FIXED_ASSET (FA-000123) and FA_OPENING (FAI-000001)
--   property_has_financial_data    reads fa_assets and fa_ledger (F19)
--
-- Differences from the design text, recorded in 37-fixed-assets.md "As built: PR 1":
--   * fa_fiscal_groups is created here (the design lists it in PR 4) because the opening import takes the fiscal group of an old asset;
--   * fa_settings has tax_year_start_month (NULL = the fiscal year of the books, Q18) and oe_policy_changed_on (the date from which the OE rule applies, 4.8);
--   * fa_categories.is_depreciable is a generated column;
--   * the state columns of fa_assets (accum_depr, dep_months_done, last_dep_month, cost, unit_count, status) are kept by the register ledger writer of the service, not by a trigger.

-- 1. The new accounts, for the properties that exist -----------------------------------------------------------------------------------------------
-- The same rule as 00071 and 00084: the standard code, or, when the code is the user's already, the next free code in a short range (1571 to 1579 for 1570, 1596 to 1599 for the accumulated accounts,
-- 7541 to 7549 and 7551 to 7559 for the expense accounts). 1590 becomes a header only where no journal line and no bill line uses it; elsewhere the new accumulated accounts sit under 1500.
-- The default categories (4.1) are made for each of those properties at the same time, from the accounts that were just made.

-- +goose StatementBegin
CREATE FUNCTION fa_fiscal_group_rates() RETURNS TABLE (code text, life_years int, sl_rate numeric, db_rate numeric)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM (VALUES
        ('G1',                4,  25.000, 50.000),
        ('G2',                8,  12.500, 25.000),
        ('G3',                16,  6.250, 12.500),
        ('G4',                20,  5.000, 10.000),
        ('BLD_PERMANENT',     20,  5.000, NULL::numeric),
        ('BLD_NON_PERMANENT', 10, 10.000, NULL::numeric)
    ) AS t (code, life_years, sl_rate, db_rate)
$$;
-- +goose StatementEnd

CREATE TABLE fa_settings (
    id                       bigint        GENERATED ALWAYS AS IDENTITY NOT NULL,
    property_id              bigint        PRIMARY KEY,
    tenant_id                bigint        NOT NULL,
    register_from            date,
    capitalization_threshold numeric(18,3) NOT NULL DEFAULT 0,
    start_rule               varchar(16)   NOT NULL DEFAULT 'IN_SERVICE_MONTH',
    oe_policy                varchar(8)    NOT NULL DEFAULT 'EXPENSE',
    oe_default_months        int           NOT NULL DEFAULT 24,
    oe_policy_changed_on     date,
    tax_year_start_month     smallint,
    version                  int           NOT NULL DEFAULT 1,
    created_at               timestamptz   NOT NULL DEFAULT now(),
    created_by               bigint REFERENCES users (id),
    updated_at               timestamptz   NOT NULL DEFAULT now(),
    updated_by               bigint REFERENCES users (id),
    CONSTRAINT fa_settings_id_uk            UNIQUE (id),
    CONSTRAINT fa_settings_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_settings_register_from_ck CHECK (register_from IS NULL OR register_from = date_trunc('month', register_from)::date),
    CONSTRAINT fa_settings_threshold_ck     CHECK (capitalization_threshold >= 0),
    CONSTRAINT fa_settings_start_rule_ck    CHECK (start_rule IN ('IN_SERVICE_MONTH', 'NEXT_MONTH')),
    CONSTRAINT fa_settings_oe_policy_ck     CHECK (oe_policy IN ('EXPENSE', 'BATCH')),
    CONSTRAINT fa_settings_oe_months_ck     CHECK (oe_default_months BETWEEN 1 AND 120),
    CONSTRAINT fa_settings_tax_year_ck      CHECK (tax_year_start_month IS NULL OR tax_year_start_month BETWEEN 1 AND 12)
);
CREATE TRIGGER fa_settings_set_updated_at BEFORE UPDATE ON fa_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE fa_categories (
    id                     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id              bigint        NOT NULL,
    property_id            bigint        NOT NULL,
    code                   varchar(20)   NOT NULL,
    name                   varchar(100)  NOT NULL,
    kind                   varchar(10)   NOT NULL,
    cost_account_id        bigint        NOT NULL,
    accumulated_account_id bigint,
    expense_account_id     bigint,
    department_id          bigint,
    method                 varchar(2),
    life_months            int,
    residual_pct           numeric(5,2)  NOT NULL DEFAULT 0,
    db_factor              numeric(4,2),
    fiscal_group           varchar(20)   NOT NULL DEFAULT 'NONE',
    fiscal_method          varchar(2),
    is_depreciable         boolean GENERATED ALWAYS AS (accumulated_account_id IS NOT NULL AND expense_account_id IS NOT NULL AND method IS NOT NULL AND life_months IS NOT NULL) STORED,
    is_active              boolean       NOT NULL DEFAULT true,
    sort_order             int           NOT NULL DEFAULT 0,
    version                int           NOT NULL DEFAULT 1,
    created_at             timestamptz   NOT NULL DEFAULT now(),
    created_by             bigint REFERENCES users (id),
    updated_at             timestamptz   NOT NULL DEFAULT now(),
    updated_by             bigint REFERENCES users (id),
    CONSTRAINT fa_categories_property_fk     FOREIGN KEY (tenant_id, property_id)               REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_categories_cost_fk         FOREIGN KEY (property_id, cost_account_id)         REFERENCES gl_accounts (property_id, id),
    CONSTRAINT fa_categories_accumulated_fk  FOREIGN KEY (property_id, accumulated_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT fa_categories_expense_fk      FOREIGN KEY (property_id, expense_account_id)      REFERENCES gl_accounts (property_id, id),
    CONSTRAINT fa_categories_department_fk   FOREIGN KEY (property_id, department_id)           REFERENCES departments (property_id, id),
    CONSTRAINT fa_categories_code_uk         UNIQUE (property_id, code),
    CONSTRAINT fa_categories_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT fa_categories_code_ck         CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT fa_categories_name_ck         CHECK (length(btrim(name)) > 0),
    CONSTRAINT fa_categories_kind_ck         CHECK (kind IN ('LAND', 'BUILDING', 'FFE', 'OE', 'VEHICLE', 'IT', 'CIP', 'LICENSE', 'OTHER')),
    CONSTRAINT fa_categories_method_ck       CHECK (method IS NULL OR method IN ('SL', 'DB')),
    CONSTRAINT fa_categories_life_ck         CHECK (life_months IS NULL OR life_months BETWEEN 1 AND 1200),
    CONSTRAINT fa_categories_residual_ck     CHECK (residual_pct BETWEEN 0 AND 100),
    CONSTRAINT fa_categories_factor_ck       CHECK (db_factor IS NULL OR (db_factor > 0 AND db_factor <= 5)),
    -- the four fields of depreciation come together or not at all, and land and construction in progress never have them (A3)
    CONSTRAINT fa_categories_depreciation_ck CHECK ((accumulated_account_id IS NOT NULL) = (expense_account_id IS NOT NULL)
                                                    AND (accumulated_account_id IS NOT NULL) = (method IS NOT NULL)
                                                    AND (accumulated_account_id IS NOT NULL) = (life_months IS NOT NULL)),
    CONSTRAINT fa_categories_not_depreciated_ck CHECK (kind NOT IN ('LAND', 'CIP') OR accumulated_account_id IS NULL),
    CONSTRAINT fa_categories_fiscal_group_ck CHECK (fiscal_group IN ('NONE', 'G1', 'G2', 'G3', 'G4', 'BLD_PERMANENT', 'BLD_NON_PERMANENT')),
    CONSTRAINT fa_categories_fiscal_ck       CHECK (fiscal_method IS NULL OR (fiscal_method IN ('SL', 'DB') AND fiscal_group <> 'NONE'
                                                    AND (fiscal_method = 'SL' OR fiscal_group NOT IN ('BLD_PERMANENT', 'BLD_NON_PERMANENT'))))
);
CREATE TRIGGER fa_categories_set_updated_at BEFORE UPDATE ON fa_categories FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE INDEX fa_categories_cost_account_idx ON fa_categories (property_id, cost_account_id);

-- The accounts of a category must be of the right kind (37-fixed-assets.md 3.2): the cost and the accumulated accounts ASSET accounts, the expense account an EXPENSE account.
-- Postable and active are the service's business.
-- +goose StatementBegin
CREATE FUNCTION fa_categories_accounts_check() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = NEW.property_id AND a.id = NEW.cost_account_id AND a.account_type = 'ASSET') THEN
        RAISE EXCEPTION 'the cost account of category % must be an asset account', NEW.code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_categories_cost_account_ck';
    END IF;
    IF NEW.accumulated_account_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = NEW.property_id AND a.id = NEW.accumulated_account_id AND a.account_type = 'ASSET') THEN
        RAISE EXCEPTION 'the accumulated depreciation account of category % must be an asset account', NEW.code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_categories_accumulated_account_ck';
    END IF;
    IF NEW.expense_account_id IS NOT NULL AND NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = NEW.property_id AND a.id = NEW.expense_account_id AND a.account_type = 'EXPENSE') THEN
        RAISE EXCEPTION 'the depreciation expense account of category % must be an expense account', NEW.code
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_categories_expense_account_ck';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_categories_accounts BEFORE INSERT OR UPDATE ON fa_categories FOR EACH ROW EXECUTE FUNCTION fa_categories_accounts_check();

-- A location is a hotel room, a banquet room, a warehouse or a free area (A1). It is made on first use (F17), so the register does not mirror the masters.
CREATE TABLE fa_locations (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    code             varchar(30)   NOT NULL,
    name             varchar(100)  NOT NULL,
    kind             varchar(12)   NOT NULL,
    room_id          bigint,
    banquet_room_id  bigint,
    warehouse_id     bigint,
    label_code       char(12)      NOT NULL,
    is_active        boolean       NOT NULL DEFAULT true,
    created_at       timestamptz   NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    updated_at       timestamptz   NOT NULL DEFAULT now(),
    updated_by       bigint REFERENCES users (id),
    CONSTRAINT fa_locations_property_fk     FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_locations_room_fk         FOREIGN KEY (property_id, room_id)         REFERENCES rooms (property_id, id),
    CONSTRAINT fa_locations_banquet_room_fk FOREIGN KEY (property_id, banquet_room_id) REFERENCES banquet_rooms (property_id, id),
    CONSTRAINT fa_locations_warehouse_fk    FOREIGN KEY (property_id, warehouse_id)    REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT fa_locations_code_uk         UNIQUE (property_id, code),
    CONSTRAINT fa_locations_label_uk        UNIQUE (label_code),
    CONSTRAINT fa_locations_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT fa_locations_code_ck         CHECK (code ~ '^[A-Za-z0-9][A-Za-z0-9._:/-]{0,29}$'),
    CONSTRAINT fa_locations_name_ck         CHECK (length(btrim(name)) > 0),
    CONSTRAINT fa_locations_kind_ck         CHECK (kind IN ('ROOM', 'BANQUET_ROOM', 'WAREHOUSE', 'AREA')),
    CONSTRAINT fa_locations_label_ck        CHECK (label_code ~ '^[A-Za-z0-9]{12}$'),
    -- exactly one reference for the first three kinds and none for a free area
    CONSTRAINT fa_locations_reference_ck    CHECK ((kind = 'ROOM'         AND room_id IS NOT NULL AND banquet_room_id IS NULL AND warehouse_id IS NULL)
                                                OR (kind = 'BANQUET_ROOM' AND banquet_room_id IS NOT NULL AND room_id IS NULL AND warehouse_id IS NULL)
                                                OR (kind = 'WAREHOUSE'    AND warehouse_id IS NOT NULL AND room_id IS NULL AND banquet_room_id IS NULL)
                                                OR (kind = 'AREA'         AND room_id IS NULL AND banquet_room_id IS NULL AND warehouse_id IS NULL))
);
CREATE TRIGGER fa_locations_set_updated_at BEFORE UPDATE ON fa_locations FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE UNIQUE INDEX fa_locations_room_uk         ON fa_locations (property_id, room_id)         WHERE room_id IS NOT NULL;
CREATE UNIQUE INDEX fa_locations_banquet_room_uk ON fa_locations (property_id, banquet_room_id) WHERE banquet_room_id IS NOT NULL;
CREATE UNIQUE INDEX fa_locations_warehouse_uk    ON fa_locations (property_id, warehouse_id)    WHERE warehouse_id IS NOT NULL;

-- One opening import (a file, or one asset of the form "add an old asset"): the as-of date is a month end, the journal is absent in the mode REGISTER_ONLY (F6).
CREATE TABLE fa_opening_imports (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    number          varchar(20)   NOT NULL,
    as_of           date          NOT NULL,
    mode            varchar(14)   NOT NULL,
    journal_id      bigint,
    asset_count     int           NOT NULL,
    total_cost      numeric(18,3) NOT NULL,
    total_accum     numeric(18,3) NOT NULL,
    file_hash       varchar(64),
    idempotency_key varchar(100),
    created_at      timestamptz   NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    CONSTRAINT fa_opening_imports_property_fk    FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_opening_imports_journal_fk     FOREIGN KEY (property_id, journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_opening_imports_number_uk      UNIQUE (property_id, number),
    CONSTRAINT fa_opening_imports_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT fa_opening_imports_as_of_ck       CHECK (as_of = (date_trunc('month', as_of) + interval '1 month - 1 day')::date),
    CONSTRAINT fa_opening_imports_mode_ck        CHECK (mode IN ('WITH_JOURNAL', 'REGISTER_ONLY')),
    CONSTRAINT fa_opening_imports_journal_ck     CHECK ((mode = 'WITH_JOURNAL') = (journal_id IS NOT NULL)),
    CONSTRAINT fa_opening_imports_totals_ck      CHECK (asset_count > 0 AND total_cost >= 0 AND total_accum >= 0 AND total_accum <= total_cost)
);
CREATE UNIQUE INDEX fa_opening_imports_idempotency_uk ON fa_opening_imports (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

CREATE TABLE fa_assets (
    id                         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                  bigint        NOT NULL,
    property_id                bigint        NOT NULL,
    number                     varchar(20)   NOT NULL,
    name                       varchar(150)  NOT NULL,
    category_id                bigint        NOT NULL,
    location_id                bigint,
    department_id              bigint,
    supplier_id                bigint,
    source                     varchar(8)    NOT NULL,
    opening_import_id          bigint,
    status                     varchar(10)   NOT NULL DEFAULT 'ACTIVE',
    acquisition_date           date          NOT NULL,
    in_service_date            date          NOT NULL,
    start_rule                 varchar(16)   NOT NULL,
    first_dep_month            date,
    cost                       numeric(18,3) NOT NULL,
    residual_value             numeric(18,3) NOT NULL DEFAULT 0,
    life_months                int,
    method                     varchar(2),
    db_factor                  numeric(4,2),
    fiscal_group               varchar(20)   NOT NULL DEFAULT 'NONE',
    fiscal_method              varchar(2),
    fiscal_accum_opening       numeric(18,3),
    fiscal_months_done_opening int,
    fiscal_opening_as_of       date,
    fiscal_opening_estimated   boolean       NOT NULL DEFAULT false,
    unit_count                 int           NOT NULL DEFAULT 1,
    serial_no                  varchar(100),
    warranty_until             date,
    notes                      varchar(500),
    label_code                 char(12)      NOT NULL,
    accum_depr                 numeric(18,3) NOT NULL DEFAULT 0,
    dep_months_done            int           NOT NULL DEFAULT 0,
    last_dep_month             date,
    disposed_on                date,
    version                    int           NOT NULL DEFAULT 1,
    created_at                 timestamptz   NOT NULL DEFAULT now(),
    created_by                 bigint REFERENCES users (id),
    updated_at                 timestamptz   NOT NULL DEFAULT now(),
    updated_by                 bigint REFERENCES users (id),
    CONSTRAINT fa_assets_property_fk        FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_assets_category_fk        FOREIGN KEY (property_id, category_id)       REFERENCES fa_categories (property_id, id),
    CONSTRAINT fa_assets_location_fk        FOREIGN KEY (property_id, location_id)       REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_assets_department_fk      FOREIGN KEY (property_id, department_id)     REFERENCES departments (property_id, id),
    CONSTRAINT fa_assets_supplier_fk        FOREIGN KEY (property_id, supplier_id)       REFERENCES suppliers (property_id, id),
    CONSTRAINT fa_assets_import_fk          FOREIGN KEY (property_id, opening_import_id) REFERENCES fa_opening_imports (property_id, id),
    CONSTRAINT fa_assets_number_uk          UNIQUE (property_id, number),
    CONSTRAINT fa_assets_label_uk           UNIQUE (label_code),
    CONSTRAINT fa_assets_property_id_uk     UNIQUE (property_id, id),
    CONSTRAINT fa_assets_name_ck            CHECK (length(btrim(name)) > 0),
    CONSTRAINT fa_assets_source_ck          CHECK (source IN ('OPENING', 'JOURNAL')),
    CONSTRAINT fa_assets_source_import_ck   CHECK ((source = 'OPENING') = (opening_import_id IS NOT NULL)),
    CONSTRAINT fa_assets_status_ck          CHECK (status IN ('ACTIVE', 'DISPOSED', 'CANCELLED')),
    CONSTRAINT fa_assets_disposed_ck        CHECK ((status = 'DISPOSED') = (disposed_on IS NOT NULL)),
    CONSTRAINT fa_assets_start_rule_ck      CHECK (start_rule IN ('IN_SERVICE_MONTH', 'NEXT_MONTH')),
    CONSTRAINT fa_assets_dates_ck           CHECK (first_dep_month IS NULL OR first_dep_month = date_trunc('month', first_dep_month)::date),
    CONSTRAINT fa_assets_amounts_ck         CHECK (cost > 0 AND residual_value >= 0 AND residual_value <= cost AND accum_depr >= 0 AND accum_depr <= cost - residual_value),
    CONSTRAINT fa_assets_unit_count_ck      CHECK (unit_count >= 1),
    CONSTRAINT fa_assets_method_ck          CHECK (method IS NULL OR method IN ('SL', 'DB')),
    CONSTRAINT fa_assets_life_ck            CHECK (life_months IS NULL OR life_months BETWEEN 1 AND 1200),
    CONSTRAINT fa_assets_factor_ck          CHECK (db_factor IS NULL OR (db_factor > 0 AND db_factor <= 5)),
    -- a depreciable asset has life, method and a start month; land and construction in progress have none and never carry depreciation
    CONSTRAINT fa_assets_depreciable_ck     CHECK ((life_months IS NULL) = (method IS NULL) AND (life_months IS NULL) = (first_dep_month IS NULL)),
    CONSTRAINT fa_assets_not_depreciated_ck CHECK (life_months IS NOT NULL OR (accum_depr = 0 AND dep_months_done = 0 AND residual_value = 0)),
    CONSTRAINT fa_assets_months_ck          CHECK (dep_months_done >= 0 AND (life_months IS NULL OR dep_months_done <= life_months)),
    CONSTRAINT fa_assets_last_month_ck      CHECK ((last_dep_month IS NULL) = (dep_months_done = 0) AND (last_dep_month IS NULL OR last_dep_month = date_trunc('month', last_dep_month)::date)),
    CONSTRAINT fa_assets_fiscal_group_ck    CHECK (fiscal_group IN ('NONE', 'G1', 'G2', 'G3', 'G4', 'BLD_PERMANENT', 'BLD_NON_PERMANENT')),
    CONSTRAINT fa_assets_fiscal_method_ck   CHECK (fiscal_method IS NULL OR (fiscal_method IN ('SL', 'DB') AND fiscal_group <> 'NONE'
                                                    AND (fiscal_method = 'SL' OR fiscal_group NOT IN ('BLD_PERMANENT', 'BLD_NON_PERMANENT')))),
    CONSTRAINT fa_assets_fiscal_opening_ck  CHECK ((fiscal_accum_opening IS NULL) = (fiscal_opening_as_of IS NULL)
                                                    AND (fiscal_accum_opening IS NULL OR (fiscal_accum_opening >= 0 AND fiscal_accum_opening <= cost))
                                                    AND (fiscal_months_done_opening IS NULL OR (fiscal_months_done_opening >= 0 AND fiscal_accum_opening IS NOT NULL))
                                                    AND (NOT fiscal_opening_estimated OR fiscal_accum_opening IS NOT NULL)),
    CONSTRAINT fa_assets_label_ck           CHECK (label_code ~ '^[A-Za-z0-9]{12}$')
);
CREATE TRIGGER fa_assets_set_updated_at BEFORE UPDATE ON fa_assets FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE INDEX fa_assets_category_idx   ON fa_assets (property_id, category_id);
CREATE INDEX fa_assets_location_idx   ON fa_assets (property_id, location_id) WHERE location_id IS NOT NULL;
CREATE INDEX fa_assets_department_idx ON fa_assets (property_id, department_id) WHERE department_id IS NOT NULL;
CREATE INDEX fa_assets_status_idx     ON fa_assets (property_id, status);
CREATE INDEX fa_assets_import_idx     ON fa_assets (property_id, opening_import_id) WHERE opening_import_id IS NOT NULL;

-- A8: the start of the depreciation is free until the asset has ever been depreciated, and locked from then on, whatever the code does. After a run is reversed the months done are zero again and the start is free.
-- +goose StatementBegin
CREATE FUNCTION fa_assets_start_is_locked() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.dep_months_done > 0 AND (NEW.in_service_date IS DISTINCT FROM OLD.in_service_date OR NEW.start_rule IS DISTINCT FROM OLD.start_rule
                                    OR NEW.first_dep_month IS DISTINCT FROM OLD.first_dep_month) THEN
        RAISE EXCEPTION 'the start of the depreciation of asset % is locked: it has been depreciated', OLD.number
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_assets_start_locked';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_assets_start_locked BEFORE UPDATE ON fa_assets FOR EACH ROW EXECUTE FUNCTION fa_assets_start_is_locked();

-- The register ledger: every change of cost and of accumulated depreciation, append-only. A correction is a row of kind REVERSAL that negates another (reverses_id). The state of the asset is kept by the
-- one writer of the service and checked against this ledger (37-fixed-assets.md 3.2).
CREATE TABLE fa_ledger (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    asset_id    bigint        NOT NULL,
    category_id bigint        NOT NULL,
    kind        varchar(12)   NOT NULL,
    event_date  date          NOT NULL,
    book_month  date,
    cost_delta  numeric(18,3) NOT NULL DEFAULT 0,
    accum_delta numeric(18,3) NOT NULL DEFAULT 0,
    months_delta int          NOT NULL DEFAULT 0,
    journal_id  bigint,
    source_type varchar(20)   NOT NULL,
    source_id   bigint        NOT NULL,
    source_line int           NOT NULL DEFAULT 0,
    reverses_id bigint,
    created_at  timestamptz   NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    CONSTRAINT fa_ledger_property_fk     FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_ledger_asset_fk        FOREIGN KEY (property_id, asset_id)     REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_ledger_category_fk     FOREIGN KEY (property_id, category_id)  REFERENCES fa_categories (property_id, id),
    CONSTRAINT fa_ledger_journal_fk      FOREIGN KEY (property_id, journal_id)   REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_ledger_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT fa_ledger_reverses_fk     FOREIGN KEY (property_id, reverses_id)  REFERENCES fa_ledger (property_id, id),
    CONSTRAINT fa_ledger_reverses_uk     UNIQUE (reverses_id),
    CONSTRAINT fa_ledger_source_uk       UNIQUE (property_id, source_type, source_id, source_line, kind),
    CONSTRAINT fa_ledger_kind_ck         CHECK (kind IN ('OPEN', 'ACQUIRE', 'CAPITALIZE', 'DEPRECIATE', 'RECLASS_OUT', 'RECLASS_IN', 'DISPOSE', 'REVERSAL')),
    CONSTRAINT fa_ledger_reversal_ck     CHECK ((kind = 'REVERSAL') = (reverses_id IS NOT NULL)),
    CONSTRAINT fa_ledger_book_month_ck   CHECK (book_month IS NULL OR book_month = date_trunc('month', book_month)::date)
);
CREATE INDEX fa_ledger_asset_idx   ON fa_ledger (property_id, asset_id, id);
CREATE INDEX fa_ledger_date_idx    ON fa_ledger (property_id, event_date);
CREATE INDEX fa_ledger_journal_idx ON fa_ledger (property_id, journal_id) WHERE journal_id IS NOT NULL;
CREATE TRIGGER fa_ledger_append_only BEFORE UPDATE OR DELETE ON fa_ledger FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_ledger_no_truncate BEFORE TRUNCATE ON fa_ledger FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The rates of the income tax law (UU PPh article 11) as data (4.9): percent a year; the property may correct them. To be confirmed by the tax consultant.
CREATE TABLE fa_fiscal_groups (
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    life_years  int          NOT NULL,
    sl_rate     numeric(6,3) NOT NULL,
    db_rate     numeric(6,3),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    PRIMARY KEY (property_id, code),
    CONSTRAINT fa_fiscal_groups_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_fiscal_groups_code_ck     CHECK (code IN ('G1', 'G2', 'G3', 'G4', 'BLD_PERMANENT', 'BLD_NON_PERMANENT')),
    CONSTRAINT fa_fiscal_groups_life_ck     CHECK (life_years BETWEEN 1 AND 100),
    CONSTRAINT fa_fiscal_groups_rates_ck    CHECK (sl_rate > 0 AND sl_rate <= 100 AND (db_rate IS NULL OR (db_rate > 0 AND db_rate <= 100))
                                                   AND (db_rate IS NULL OR code NOT IN ('BLD_PERMANENT', 'BLD_NON_PERMANENT')))
);
CREATE TRIGGER fa_fiscal_groups_set_updated_at BEFORE UPDATE ON fa_fiscal_groups FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 2. The seed of a property: the settings row (the lock target of the register), the fiscal groups and the seven default categories (4.1). ------------------
-- p_codes maps a standard account code to the code a property really uses when the standard one was taken (the migration below passes it); a category whose accounts are missing is skipped.
-- +goose StatementBegin
CREATE FUNCTION seed_fa_categories(p_tenant_id bigint, p_property_id bigint, p_lang text, p_codes jsonb DEFAULT '{}'::jsonb) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_spec record;
    v_cost bigint;
    v_accum bigint;
    v_exp bigint;
    v_inserted integer := 0;
    n integer;
BEGIN
    PERFORM seed_language_check(p_lang);
    INSERT INTO fa_settings (tenant_id, property_id) VALUES (p_tenant_id, p_property_id) ON CONFLICT (property_id) DO NOTHING;
    INSERT INTO fa_fiscal_groups (tenant_id, property_id, code, life_years, sl_rate, db_rate)
    SELECT p_tenant_id, p_property_id, r.code, r.life_years, r.sl_rate, r.db_rate FROM fa_fiscal_group_rates() r
    ON CONFLICT (property_id, code) DO NOTHING;
    FOR v_spec IN
        SELECT * FROM (VALUES
            ('LAND',     'Land',                                         'Tanah',                                      'LAND',     '1510', NULL,   NULL,   NULL, NULL, 'NONE',              NULL, 10),
            ('BUILDING', 'Buildings',                                    'Bangunan',                                   'BUILDING', '1520', '1591', '7510', 'SL', 240,  'BLD_PERMANENT',    'SL', 20),
            ('FFE',      'Furniture, fixtures and equipment (FF&E)',     'Furnitur, perlengkapan dan peralatan (FF&E)', 'FFE',     '1530', '1592', '7520', 'SL', 96,   'G2',                'SL', 30),
            ('OE',       'Operating equipment (OE)',                     'Peralatan operasional (OE)',                 'OE',       '1540', '1593', '7530', 'SL', 24,   'G1',                'SL', 40),
            ('VEHICLE',  'Vehicles',                                     'Kendaraan',                                  'VEHICLE',  '1550', '1594', '7540', 'SL', 96,   'G2',                'SL', 50),
            ('IT',       'Computers and IT',                             'Komputer dan IT',                            'IT',       '1570', '1595', '7550', 'SL', 48,   'G1',                'SL', 60),
            ('CIP',      'Construction in progress',                     'Aset dalam pembangunan',                     'CIP',      '1560', NULL,   NULL,   NULL, NULL, 'NONE',              NULL, 70)
        ) AS t (code, name_en, name_id, kind, cost_code, accum_code, expense_code, method, life_months, fiscal_group, fiscal_method, sort_order)
    LOOP
        SELECT a.id INTO v_cost FROM gl_accounts a WHERE a.property_id = p_property_id AND a.code = coalesce(p_codes ->> v_spec.cost_code, v_spec.cost_code);
        v_accum := NULL;
        v_exp := NULL;
        IF v_spec.accum_code IS NOT NULL THEN
            SELECT a.id INTO v_accum FROM gl_accounts a WHERE a.property_id = p_property_id AND a.code = coalesce(p_codes ->> v_spec.accum_code, v_spec.accum_code);
            SELECT a.id INTO v_exp   FROM gl_accounts a WHERE a.property_id = p_property_id AND a.code = coalesce(p_codes ->> v_spec.expense_code, v_spec.expense_code);
        END IF;
        IF v_cost IS NULL OR (v_spec.accum_code IS NOT NULL AND (v_accum IS NULL OR v_exp IS NULL)) THEN
            RAISE NOTICE 'seed_fa_categories: property % has no accounts for the category %, skipped', p_property_id, v_spec.code;
            CONTINUE;
        END IF;
        INSERT INTO fa_categories (tenant_id, property_id, code, name, kind, cost_account_id, accumulated_account_id, expense_account_id, method, life_months, fiscal_group, fiscal_method, sort_order)
        VALUES (p_tenant_id, p_property_id, v_spec.code, CASE WHEN p_lang = 'id' THEN v_spec.name_id ELSE v_spec.name_en END, v_spec.kind, v_cost, v_accum, v_exp,
                v_spec.method, v_spec.life_months, v_spec.fiscal_group, v_spec.fiscal_method, v_spec.sort_order)
        ON CONFLICT (property_id, code) DO NOTHING;
        GET DIAGNOSTICS n = ROW_COUNT;
        v_inserted := v_inserted + n;
    END LOOP;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- 3. The standard chart of a NEW property, and the accounts of the properties that exist ----------------------------------------------------------------
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v7;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT c.code, c.name, c.account_type, c.normal_side, c.parent_code, c.is_postable, c.statement_group FROM usali_chart_v7() c WHERE c.code <> '1590'
    UNION ALL
    SELECT '1570', 'Computers and IT equipment', 'ASSET', 'DEBIT', '1500', true, 'FIXED_ASSETS'
    UNION ALL
    SELECT '1590', 'Accumulated depreciation', 'ASSET', 'CREDIT', '1500', false, 'FIXED_ASSETS'
    UNION ALL
    SELECT '1591', 'Accumulated depreciation - buildings', 'ASSET', 'CREDIT', '1590', true, 'FIXED_ASSETS'
    UNION ALL
    SELECT '1592', 'Accumulated depreciation - furniture, fixtures and equipment', 'ASSET', 'CREDIT', '1590', true, 'FIXED_ASSETS'
    UNION ALL
    SELECT '1593', 'Accumulated amortization - operating equipment', 'ASSET', 'CREDIT', '1590', true, 'FIXED_ASSETS'
    UNION ALL
    SELECT '1594', 'Accumulated depreciation - vehicles', 'ASSET', 'CREDIT', '1590', true, 'FIXED_ASSETS'
    UNION ALL
    SELECT '1595', 'Accumulated depreciation - computers and IT', 'ASSET', 'CREDIT', '1590', true, 'FIXED_ASSETS'
    UNION ALL
    SELECT '7540', 'Depreciation - vehicles', 'EXPENSE', 'DEBIT', '7500', true, 'DEPRECIATION'
    UNION ALL
    SELECT '7550', 'Depreciation - computers and IT', 'EXPENSE', 'DEBIT', '7500', true, 'DEPRECIATION'
$$;
-- +goose StatementEnd
ALTER FUNCTION usali_chart_names(text) RENAME TO usali_chart_names_v5;
-- +goose StatementBegin
CREATE FUNCTION usali_chart_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT n.code, n.name FROM usali_chart_names_v5(p_lang) n
    UNION ALL
    SELECT '1570', 'Komputer dan peralatan TI' WHERE p_lang = 'id'
    UNION ALL
    SELECT '1591', 'Akumulasi penyusutan – bangunan' WHERE p_lang = 'id'
    UNION ALL
    SELECT '1592', 'Akumulasi penyusutan – furnitur, perlengkapan, dan peralatan' WHERE p_lang = 'id'
    UNION ALL
    SELECT '1593', 'Akumulasi amortisasi – peralatan operasional' WHERE p_lang = 'id'
    UNION ALL
    SELECT '1594', 'Akumulasi penyusutan – kendaraan' WHERE p_lang = 'id'
    UNION ALL
    SELECT '1595', 'Akumulasi penyusutan – komputer dan TI' WHERE p_lang = 'id'
    UNION ALL
    SELECT '7540', 'Penyusutan – kendaraan' WHERE p_lang = 'id'
    UNION ALL
    SELECT '7550', 'Penyusutan – komputer dan TI' WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- The properties that exist. Each one that has a chart (an accounting_settings row) and no account for the cost of computers yet gets the eight accounts and, with them, the default categories.
-- 1590 becomes a header where nothing is posted to it (no journal line, no bill line); the number of properties of each case is reported.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_idn boolean;
    v_spec record;
    v_code text;
    v_id bigint;
    v_parent bigint;
    v_1590 bigint;
    v_header boolean;
    n int;
    v_codes jsonb;
    v_props int := 0;
    v_headers int := 0;
    v_postable int := 0;
    v_nochart int := 0;
    v_replaced int := 0;
BEGIN
    FOR r IN SELECT s.tenant_id, s.property_id FROM accounting_settings s ORDER BY s.property_id LOOP
        IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = '1595') THEN
            CONTINUE;
        END IF;
        v_idn := EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = '1150'
                            AND a.name = (SELECT c.name FROM usali_chart_names('id') c WHERE c.code = '1150'));
        v_codes := '{}'::jsonb;

        SELECT a.id INTO v_1590 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = '1590';
        v_header := v_1590 IS NOT NULL
                AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = r.property_id AND l.account_id = v_1590)
                AND NOT EXISTS (SELECT 1 FROM supplier_bill_lines b WHERE b.property_id = r.property_id AND b.account_id = v_1590)
                AND NOT EXISTS (SELECT 1 FROM gl_accounts c WHERE c.property_id = r.property_id AND c.parent_id = v_1590);
        IF v_header THEN
            UPDATE gl_accounts SET is_postable = false WHERE id = v_1590;
            v_headers := v_headers + 1;
        ELSE
            v_postable := v_postable + 1;
        END IF;

        FOR v_spec IN
            SELECT * FROM (VALUES
                ('1570', 'Computers and IT equipment',                                   'Komputer dan peralatan TI',                                      'ASSET',   'DEBIT',  '1500', 'FIXED_ASSETS', 1571, 1579),
                ('1591', 'Accumulated depreciation - buildings',                          'Akumulasi penyusutan – bangunan',                                'ASSET',   'CREDIT', '1590', 'FIXED_ASSETS', 1596, 1599),
                ('1592', 'Accumulated depreciation - furniture, fixtures and equipment', 'Akumulasi penyusutan – furnitur, perlengkapan, dan peralatan', 'ASSET', 'CREDIT', '1590', 'FIXED_ASSETS', 1596, 1599),
                ('1593', 'Accumulated amortization - operating equipment',                'Akumulasi amortisasi – peralatan operasional',                   'ASSET',   'CREDIT', '1590', 'FIXED_ASSETS', 1596, 1599),
                ('1594', 'Accumulated depreciation - vehicles',                           'Akumulasi penyusutan – kendaraan',                               'ASSET',   'CREDIT', '1590', 'FIXED_ASSETS', 1596, 1599),
                ('1595', 'Accumulated depreciation - computers and IT',                   'Akumulasi penyusutan – komputer dan TI',                         'ASSET',   'CREDIT', '1590', 'FIXED_ASSETS', 1596, 1599),
                ('7540', 'Depreciation - vehicles',                                       'Penyusutan – kendaraan',                                         'EXPENSE', 'DEBIT',  '7500', 'DEPRECIATION', 7541, 7549),
                ('7550', 'Depreciation - computers and IT',                               'Penyusutan – komputer dan TI',                                   'EXPENSE', 'DEBIT',  '7500', 'DEPRECIATION', 7551, 7559)
            ) AS t (std_code, name_en, name_id, account_type, normal_side, parent_code, statement_group, first_code, last_code)
        LOOP
            v_code := v_spec.std_code;
            IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = v_code) THEN
                v_code := NULL;
                FOR n IN v_spec.first_code .. v_spec.last_code LOOP
                    IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = n::text) THEN
                        v_code := n::text;
                        EXIT;
                    END IF;
                END LOOP;
                IF v_code IS NULL THEN
                    RAISE EXCEPTION '00093: property % has no free account code from % to % for the account %', r.property_id, v_spec.std_code, v_spec.last_code, v_spec.std_code;
                END IF;
                v_codes := v_codes || jsonb_build_object(v_spec.std_code, v_code);
                v_replaced := v_replaced + 1;
            END IF;
            -- the accumulated accounts sit under 1590 only where it became a header, else under 1500
            IF v_spec.parent_code = '1590' AND NOT v_header THEN
                SELECT p.id INTO v_parent FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = '1500';
            ELSE
                SELECT p.id INTO v_parent FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = v_spec.parent_code;
            END IF;
            INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
            VALUES (r.tenant_id, r.property_id, v_code, CASE WHEN v_idn THEN v_spec.name_id ELSE v_spec.name_en END, v_spec.account_type, v_spec.normal_side, v_parent, true, v_spec.statement_group)
            RETURNING id INTO v_id;
        END LOOP;
        PERFORM seed_fa_categories(r.tenant_id, r.property_id, CASE WHEN v_idn THEN 'id' ELSE 'en' END, v_codes);
        v_props := v_props + 1;
    END LOOP;
    -- the properties without a chart get the settings and the fiscal groups only; their categories come with the chart
    SELECT count(*) INTO v_nochart FROM properties p WHERE NOT EXISTS (SELECT 1 FROM accounting_settings s WHERE s.property_id = p.id);
    INSERT INTO fa_settings (tenant_id, property_id) SELECT p.tenant_id, p.id FROM properties p ON CONFLICT (property_id) DO NOTHING;
    INSERT INTO fa_fiscal_groups (tenant_id, property_id, code, life_years, sl_rate, db_rate)
    SELECT p.tenant_id, p.id, g.code, g.life_years, g.sl_rate, g.db_rate FROM properties p CROSS JOIN fa_fiscal_group_rates() g ON CONFLICT (property_id, code) DO NOTHING;
    RAISE NOTICE '00093: fixed asset accounts and categories added to % propert(y/ies): 1590 became a header in % and stayed postable (the new accounts sit under 1500) in %; % replacement code(s) because the standard one was taken; % propert(y/ies) have no chart yet',
        v_props, v_headers, v_postable, v_replaced, v_nochart;
END
$$;
-- +goose StatementEnd

-- 4. The journal type, the sequences, the currency lock -----------------------------------------------------------------------------------------------
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX', 'RECEIVABLES', 'CASHIER', 'ASSET'));

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT',
                             'FIXED_ASSET', 'FA_OPENING'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'FIXED_ASSET', 'FA-' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'FA_OPENING', 'FAI-' FROM properties;

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
        OR EXISTS (SELECT 1 FROM fa_assets              WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM fa_ledger              WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

-- +goose Down
-- Refused while the module holds anything that cannot be rebuilt: an asset, a ledger row, an opening import or a journal of the register. Nothing is changed then. (The default categories and settings are not "data".)
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM fa_assets) + (SELECT count(*) FROM fa_ledger) + (SELECT count(*) FROM fa_opening_imports) + (SELECT count(*) FROM gl_journals WHERE journal_type = 'ASSET') INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00093: the fixed assets module holds % row(s) (assets, ledger rows, opening imports, journals)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE fa_ledger;
DROP TABLE fa_assets;
DROP FUNCTION fa_assets_start_is_locked();
DROP TABLE fa_opening_imports;
DROP TABLE fa_locations;
DROP TABLE fa_categories;
DROP FUNCTION fa_categories_accounts_check();
DROP TABLE fa_fiscal_groups;
DROP TABLE fa_settings;
DROP FUNCTION seed_fa_categories(bigint, bigint, text, jsonb);
DROP FUNCTION fa_fiscal_group_rates();

-- The new accounts go when they are still what the migration made and nothing uses them; 1590 is postable again where it was made a header (nothing posted to it, so nothing changes for the books).
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_dropped int := 0;
    v_kept int := 0;
BEGIN
    FOR r IN SELECT a.id, a.property_id FROM gl_accounts a
              WHERE a.code IN ('1570', '1591', '1592', '1593', '1594', '1595', '7540', '7550', '1571', '1572', '1573', '1574', '1575', '1576', '1577', '1578', '1579', '1596', '1597', '1598', '1599',
                               '7541', '7542', '7543', '7544', '7545', '7546', '7547', '7548', '7549', '7551', '7552', '7553', '7554', '7555', '7556', '7557', '7558', '7559')
                AND a.name IN (SELECT n.name FROM usali_chart_names('en') n UNION SELECT n.name FROM usali_chart_names('id') n)
              ORDER BY a.property_id, a.id LOOP
        BEGIN
            DELETE FROM gl_accounts a WHERE a.id = r.id AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = a.property_id AND l.account_id = a.id);
            IF FOUND THEN v_dropped := v_dropped + 1; ELSE v_kept := v_kept + 1; END IF;
        EXCEPTION WHEN foreign_key_violation THEN
            v_kept := v_kept + 1;
        END;
    END LOOP;
    UPDATE gl_accounts a SET is_postable = true
     WHERE a.code = '1590' AND NOT a.is_postable AND NOT EXISTS (SELECT 1 FROM gl_accounts c WHERE c.property_id = a.property_id AND c.parent_id = a.id);
    RAISE NOTICE '00093 down: % fixed asset account(s) deleted, % kept as ordinary accounts', v_dropped, v_kept;
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
        OR EXISTS (SELECT 1 FROM inv_movements          WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

DROP FUNCTION usali_chart_names(text);
ALTER FUNCTION usali_chart_names_v5(text) RENAME TO usali_chart_names;
DROP FUNCTION usali_chart();
ALTER FUNCTION usali_chart_v7() RENAME TO usali_chart;

DELETE FROM document_sequences WHERE sequence_type IN ('FIXED_ASSET', 'FA_OPENING');
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT'));

ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX', 'RECEIVABLES', 'CASHIER'));
