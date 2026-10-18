-- +goose Up
-- Inventory, PR 5 (docs/architecture/35-inventory.md, sections 3.6, 6, 12): the snapshots of the day close and the targets the reports are read against.
--
--   inv_snapshot_days       one row for each business day the close has taken the snapshots of (the marker that makes the close idempotent and tells a report which days are stored)
--   inv_balance_days        quantity and value of an item in a warehouse at the end of a business day; written for the pairs that moved that day (backfilled below for the days before this migration)
--   inv_cost_days           the daily cost of all costs (D31): one row for each day, department, outlet and kind of cost
--   inv_fb_cost_days        the food and beverage cost of a day by outlet and class, beside the revenue it is read against (I17)
--   inv_cost_targets        the target cost percentage of food and of beverage, for the property and for an outlet
--   inventory_settings      engineering_department_id: the department whose manual USE issues are the engineering cost (NULL: none)
--
-- Differences from the design text, recorded in 35-inventory.md "As built: PR 5":
--   * inv_snapshot_days is new: a day with no cost has no row in the other tables, so only a marker can say "the close took this day";
--   * the days that closed before this migration have no marker: the reports compute them from the ledger, which is final for a closed day, with the same query the close uses.

-- 1. The settings --------------------------------------------------------------------------------------------------------------------------------
ALTER TABLE inventory_settings
    ADD COLUMN engineering_department_id bigint,
    ADD CONSTRAINT inventory_settings_engineering_fk FOREIGN KEY (property_id, engineering_department_id) REFERENCES departments (property_id, id);

-- 2. The marker of the day -----------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_snapshot_days (
    property_id   bigint      NOT NULL,
    tenant_id     bigint      NOT NULL,
    business_date date        NOT NULL,
    taken_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (property_id, business_date),
    CONSTRAINT inv_snapshot_days_property_fk FOREIGN KEY (tenant_id, property_id)     REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_snapshot_days_day_fk      FOREIGN KEY (property_id, business_date) REFERENCES business_days (property_id, business_date)
);

-- 3. The balance at the end of a day --------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_balance_days (
    property_id   bigint        NOT NULL,
    tenant_id     bigint        NOT NULL,
    warehouse_id  bigint        NOT NULL,
    item_id       bigint        NOT NULL,
    business_date date          NOT NULL,
    qty           numeric(18,6) NOT NULL,
    value         numeric(18,3) NOT NULL,
    PRIMARY KEY (warehouse_id, item_id, business_date),
    CONSTRAINT inv_balance_days_property_fk  FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_balance_days_warehouse_fk FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_balance_days_item_fk      FOREIGN KEY (property_id, item_id)      REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_balance_days_zero_ck      CHECK (qty <> 0 OR value = 0)
);
CREATE INDEX inv_balance_days_day_idx ON inv_balance_days (property_id, business_date);

-- the days before this migration: what each pair stood at, at the end of each day it moved
INSERT INTO inv_balance_days (property_id, tenant_id, warehouse_id, item_id, business_date, qty, value)
SELECT property_id, tenant_id, warehouse_id, item_id, business_date,
       sum(day_qty) OVER w, sum(day_value) OVER w
  FROM (SELECT property_id, tenant_id, warehouse_id, item_id, business_date, sum(qty_delta) AS day_qty, sum(value_delta) AS day_value
          FROM inv_movements GROUP BY property_id, tenant_id, warehouse_id, item_id, business_date) d
WINDOW w AS (PARTITION BY warehouse_id, item_id ORDER BY business_date);

-- 4. The cost of a day ----------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_cost_days (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    business_date date          NOT NULL,
    department_id bigint,
    outlet_id     bigint,
    cost_kind     varchar(20)   NOT NULL,
    amount        numeric(18,3) NOT NULL,
    CONSTRAINT inv_cost_days_property_fk   FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_cost_days_day_fk        FOREIGN KEY (property_id, business_date)   REFERENCES business_days (property_id, business_date),
    CONSTRAINT inv_cost_days_department_fk FOREIGN KEY (property_id, department_id)  REFERENCES departments (property_id, id),
    CONSTRAINT inv_cost_days_outlet_fk     FOREIGN KEY (property_id, outlet_id)       REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_cost_days_kind_ck       CHECK (cost_kind IN ('SALE_COST_FOOD', 'SALE_COST_BEVERAGE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT', 'AMENITY', 'ENGINEERING', 'OTHER_USE', 'COUNT_VARIANCE', 'PRICE_VARIANCE'))
);
CREATE UNIQUE INDEX inv_cost_days_uk ON inv_cost_days (property_id, business_date, COALESCE(department_id, 0), COALESCE(outlet_id, 0), cost_kind);

CREATE TABLE inv_fb_cost_days (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    business_date       date          NOT NULL,
    outlet_id           bigint,
    class               varchar(10)   NOT NULL,
    revenue_net         numeric(18,3) NOT NULL DEFAULT 0,
    no_recipe_revenue   numeric(18,3) NOT NULL DEFAULT 0,
    sale_cost           numeric(18,3) NOT NULL DEFAULT 0,
    waste_cost          numeric(18,3) NOT NULL DEFAULT 0,
    staff_meal_cost     numeric(18,3) NOT NULL DEFAULT 0,
    entertainment_cost  numeric(18,3) NOT NULL DEFAULT 0,
    CONSTRAINT inv_fb_cost_days_property_fk FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_fb_cost_days_day_fk      FOREIGN KEY (property_id, business_date)  REFERENCES business_days (property_id, business_date),
    CONSTRAINT inv_fb_cost_days_outlet_fk   FOREIGN KEY (property_id, outlet_id)      REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_fb_cost_days_class_ck    CHECK (class IN ('FOOD', 'BEVERAGE', 'OTHER'))
);
CREATE UNIQUE INDEX inv_fb_cost_days_uk ON inv_fb_cost_days (property_id, business_date, COALESCE(outlet_id, 0), class);

-- 5. The targets ----------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_cost_targets (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    outlet_id   bigint,
    class       varchar(10)  NOT NULL,
    target_pct  numeric(5,2) NOT NULL,
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT inv_cost_targets_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_cost_targets_outlet_fk   FOREIGN KEY (property_id, outlet_id) REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_cost_targets_class_ck    CHECK (class IN ('FOOD', 'BEVERAGE')),
    CONSTRAINT inv_cost_targets_pct_ck      CHECK (target_pct > 0 AND target_pct <= 100)
);
CREATE UNIQUE INDEX inv_cost_targets_uk ON inv_cost_targets (property_id, COALESCE(outlet_id, 0), class);

-- +goose Down
-- Refused while the close has taken a snapshot, or a target is set: the tables are the only copy of what the owner typed and of what the days looked like when they closed.
-- (inv_balance_days alone can be rebuilt from the ledger, so it does not hold the Down.)
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM inv_snapshot_days) + (SELECT count(*) FROM inv_cost_days) + (SELECT count(*) FROM inv_fb_cost_days) + (SELECT count(*) FROM inv_cost_targets)
         + (SELECT count(*) FROM inventory_settings WHERE engineering_department_id IS NOT NULL) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00088: the reports hold % row(s) (snapshots, targets, the engineering department)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE inv_cost_targets;
DROP TABLE inv_fb_cost_days;
DROP TABLE inv_cost_days;
DROP TABLE inv_balance_days;
DROP TABLE inv_snapshot_days;
ALTER TABLE inventory_settings DROP CONSTRAINT inventory_settings_engineering_fk, DROP COLUMN engineering_department_id;
