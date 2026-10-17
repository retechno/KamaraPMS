-- +goose Up
-- Inventory, PR 3 (docs/architecture/35-inventory.md, sections 3.4, 5.4, 5.5, 5.9): the requisition (SR) with its approval chain, the transfer, the issue to a department, waste, staff meal and entertainment, the
-- stock count with the freeze, and the automatic amenities of the day close.
--
--   inv_requisitions, inv_requisition_lines   the store requisition (SR); its chain uses the steps and the notices of 00085
--   inv_transfers, inv_transfer_lines         the transfer between warehouses (one step, no journal), answering an approved SR or free
--   inv_issues, inv_issue_lines               the issue to a department (USE), waste, staff meal, entertainment, and the amenities document of a close (origin AMENITY)
--   inv_counts, inv_count_lines               the stock count; inv_warehouses.count_id becomes a foreign key to it (the freeze)
--   inv_amenity_plans, _plan_lines            the plan of the amenities, a new immutable version for each save
--   inv_amenity_runs, _run_rooms, _run_lines  what the close made for one business day
--   inv_movements                             one more kind: ISSUE_REVERSAL (goods of an issue, a waste, a staff meal or an entertainment that come back)
--   document_sequences                        INV_SR, INV_TRF, INV_ISS, INV_CNT
--
-- Differences from the design text, recorded in 35-inventory.md "As built: PR 3":
--   * the status of a held amenities line (HELD, APPLIED) and the day it was applied are kept on inv_issue_lines, not on inv_amenity_run_lines: the issue line is the one that has the movement;
--   * a line of an issue has its department (the amenities plan's, a manual issue's), so the cost line of the journal is not read from the header alone;
--   * a reversal of an issue, a waste, a staff meal or an entertainment is a movement of the kind ISSUE_REVERSAL (the design lists no kind for it); the original is told by reverses_id;
--   * a count line keeps the unit the counter typed (entered_unit_id, entered_qty) next to the quantity in stock units.

-- 1. The store requisition ----------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_requisitions (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    sr_number         varchar(30)   NOT NULL,
    client_uuid       uuid          NOT NULL,
    to_warehouse_id   bigint        NOT NULL,
    from_warehouse_id bigint        NOT NULL,
    requester_id      bigint        NOT NULL,
    department_id     bigint,
    needed_on         date,
    status            varchar(10)   NOT NULL DEFAULT 'DRAFT',
    est_total         numeric(18,3) NOT NULL DEFAULT 0,
    cycle             smallint      NOT NULL DEFAULT 1,
    submitted_at      timestamptz,
    returned_note     varchar(500),
    rejected_note     varchar(300),
    note              varchar(300),
    version           int           NOT NULL DEFAULT 1,
    created_at        timestamptz   NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    updated_by        bigint REFERENCES users (id),
    CONSTRAINT inv_requisitions_property_fk      FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_requisitions_to_fk            FOREIGN KEY (property_id, to_warehouse_id)   REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_requisitions_from_fk          FOREIGN KEY (property_id, from_warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_requisitions_requester_fk     FOREIGN KEY (tenant_id, requester_id)        REFERENCES users (tenant_id, id),
    CONSTRAINT inv_requisitions_department_fk    FOREIGN KEY (property_id, department_id)     REFERENCES departments (property_id, id),
    CONSTRAINT inv_requisitions_number_uk        UNIQUE (property_id, sr_number),
    CONSTRAINT inv_requisitions_client_uk        UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_requisitions_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT inv_requisitions_status_ck        CHECK (status IN ('DRAFT', 'SUBMITTED', 'RETURNED', 'APPROVED', 'PARTIAL', 'FULFILLED', 'REJECTED', 'CANCELLED')),
    CONSTRAINT inv_requisitions_warehouses_ck    CHECK (to_warehouse_id <> from_warehouse_id),
    CONSTRAINT inv_requisitions_department_ck    CHECK (status IN ('DRAFT', 'CANCELLED') OR department_id IS NOT NULL),
    CONSTRAINT inv_requisitions_total_ck         CHECK (est_total >= 0),
    CONSTRAINT inv_requisitions_cycle_ck         CHECK (cycle >= 1),
    CONSTRAINT inv_requisitions_returned_ck      CHECK (status <> 'RETURNED' OR returned_note IS NOT NULL),
    CONSTRAINT inv_requisitions_rejected_ck      CHECK (status <> 'REJECTED' OR rejected_note IS NOT NULL)
);
CREATE INDEX inv_requisitions_status_idx ON inv_requisitions (property_id, status, id DESC);
CREATE TRIGGER inv_requisitions_set_updated_at BEFORE UPDATE ON inv_requisitions FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE inv_requisition_lines (
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    requisition_id   bigint        NOT NULL,
    line_no          int           NOT NULL,
    item_id          bigint        NOT NULL,
    unit_id          bigint        NOT NULL,
    qty_requested    numeric(18,6) NOT NULL,
    qty_stock        numeric(18,6) NOT NULL,
    qty_issued_stock numeric(18,6) NOT NULL DEFAULT 0,
    note             varchar(200),
    PRIMARY KEY (requisition_id, line_no),
    CONSTRAINT inv_requisition_lines_requisition_fk FOREIGN KEY (property_id, requisition_id) REFERENCES inv_requisitions (property_id, id),
    CONSTRAINT inv_requisition_lines_item_fk        FOREIGN KEY (property_id, item_id)        REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_requisition_lines_unit_fk        FOREIGN KEY (property_id, item_id, unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_requisition_lines_item_uk        UNIQUE (requisition_id, item_id),
    CONSTRAINT inv_requisition_lines_qty_ck         CHECK (qty_requested > 0 AND qty_stock > 0),
    CONSTRAINT inv_requisition_lines_issued_ck      CHECK (qty_issued_stock >= 0)
);

-- 2. The chain and the bell learn the requisition -----------------------------------------------------------------------------------------------
ALTER TABLE inv_approval_steps
    ALTER COLUMN request_id DROP NOT NULL,
    ADD COLUMN requisition_id bigint,
    ADD CONSTRAINT inv_approval_steps_requisition_fk FOREIGN KEY (property_id, requisition_id) REFERENCES inv_requisitions (property_id, id),
    ADD CONSTRAINT inv_approval_steps_requisition_level_uk UNIQUE (requisition_id, cycle, level_no),
    ADD CONSTRAINT inv_approval_steps_doc_ck CHECK ((request_id IS NULL) <> (requisition_id IS NULL));
CREATE UNIQUE INDEX inv_approval_steps_sr_one_person_uk ON inv_approval_steps (requisition_id, cycle, decided_by) WHERE status = 'APPROVED';
CREATE UNIQUE INDEX inv_approval_steps_sr_one_active_uk ON inv_approval_steps (requisition_id, cycle) WHERE status = 'ACTIVE';

ALTER TABLE inv_notices
    ADD COLUMN requisition_id bigint,
    ADD CONSTRAINT inv_notices_requisition_fk FOREIGN KEY (property_id, requisition_id) REFERENCES inv_requisitions (property_id, id);

-- 3. The transfer -----------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_transfers (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint       NOT NULL,
    property_id       bigint       NOT NULL,
    trf_number        varchar(30)  NOT NULL,
    client_uuid       uuid         NOT NULL,
    requisition_id    bigint,
    from_warehouse_id bigint       NOT NULL,
    to_warehouse_id   bigint       NOT NULL,
    status            varchar(10)  NOT NULL DEFAULT 'POSTED',
    business_date     date         NOT NULL,
    note              varchar(300),
    reverse_reason    varchar(300),
    reversed_on       date,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    CONSTRAINT inv_transfers_property_fk    FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_transfers_requisition_fk FOREIGN KEY (property_id, requisition_id)    REFERENCES inv_requisitions (property_id, id),
    CONSTRAINT inv_transfers_from_fk        FOREIGN KEY (property_id, from_warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_transfers_to_fk          FOREIGN KEY (property_id, to_warehouse_id)   REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_transfers_number_uk      UNIQUE (property_id, trf_number),
    CONSTRAINT inv_transfers_client_uk      UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_transfers_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_transfers_status_ck      CHECK (status IN ('POSTED', 'REVERSED')),
    CONSTRAINT inv_transfers_warehouses_ck  CHECK (from_warehouse_id <> to_warehouse_id),
    CONSTRAINT inv_transfers_reversed_ck    CHECK ((status = 'REVERSED') = (reverse_reason IS NOT NULL AND reversed_on IS NOT NULL))
);
CREATE INDEX inv_transfers_requisition_idx ON inv_transfers (property_id, requisition_id) WHERE requisition_id IS NOT NULL;
CREATE INDEX inv_transfers_day_idx ON inv_transfers (property_id, business_date DESC, id DESC);

CREATE TABLE inv_transfer_lines (
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    transfer_id      bigint        NOT NULL,
    line_no          int           NOT NULL,
    item_id          bigint        NOT NULL,
    requisition_line_no int,
    qty_stock        numeric(18,6) NOT NULL,
    value            numeric(18,3) NOT NULL,
    out_movement_id  bigint        NOT NULL,
    in_movement_id   bigint        NOT NULL,
    PRIMARY KEY (transfer_id, line_no),
    CONSTRAINT inv_transfer_lines_transfer_fk FOREIGN KEY (property_id, transfer_id)     REFERENCES inv_transfers (property_id, id),
    CONSTRAINT inv_transfer_lines_item_fk     FOREIGN KEY (property_id, item_id)         REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_transfer_lines_out_fk      FOREIGN KEY (property_id, out_movement_id) REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_transfer_lines_in_fk       FOREIGN KEY (property_id, in_movement_id)  REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_transfer_lines_item_uk     UNIQUE (transfer_id, item_id),
    CONSTRAINT inv_transfer_lines_qty_ck      CHECK (qty_stock > 0 AND value >= 0)
);

-- 4. The issue: to a department, waste, staff meal, entertainment, and the amenities of a close --------------------------------------------------
CREATE TABLE inv_issues (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint       NOT NULL,
    property_id      bigint       NOT NULL,
    iss_number       varchar(30)  NOT NULL,
    client_uuid      uuid         NOT NULL,
    warehouse_id     bigint       NOT NULL,
    purpose          varchar(14)  NOT NULL,
    origin           varchar(8)   NOT NULL DEFAULT 'MANUAL',
    department_id    bigint,
    outlet_id        bigint,
    reason_code      varchar(10),
    covers           int,
    beneficiary      varchar(100),
    reverses_issue_id bigint,
    status           varchar(10)  NOT NULL DEFAULT 'POSTED',
    business_date    date         NOT NULL,
    source_date      date,
    note             varchar(300),
    reverse_reason   varchar(300),
    created_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    CONSTRAINT inv_issues_property_fk      FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_issues_warehouse_fk     FOREIGN KEY (property_id, warehouse_id)     REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_issues_department_fk    FOREIGN KEY (property_id, department_id)    REFERENCES departments (property_id, id),
    CONSTRAINT inv_issues_outlet_fk        FOREIGN KEY (property_id, outlet_id)        REFERENCES pos_outlets (property_id, id),
    CONSTRAINT inv_issues_reverses_fk      FOREIGN KEY (property_id, reverses_issue_id) REFERENCES inv_issues (property_id, id),
    CONSTRAINT inv_issues_number_uk        UNIQUE (property_id, iss_number),
    CONSTRAINT inv_issues_client_uk        UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_issues_reverses_uk      UNIQUE (reverses_issue_id),
    CONSTRAINT inv_issues_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT inv_issues_purpose_ck       CHECK (purpose IN ('USE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT')),
    CONSTRAINT inv_issues_origin_ck        CHECK (origin IN ('MANUAL', 'AMENITY')),
    CONSTRAINT inv_issues_status_ck        CHECK (status IN ('POSTED', 'REVERSED')),
    CONSTRAINT inv_issues_reason_ck        CHECK ((purpose = 'WASTE') = (reason_code IS NOT NULL) AND (reason_code IS NULL OR reason_code IN ('SPOILAGE', 'EXPIRED', 'BREAKAGE', 'TASTING', 'OTHER'))),
    CONSTRAINT inv_issues_department_ck    CHECK (purpose = 'WASTE' OR department_id IS NOT NULL),
    CONSTRAINT inv_issues_covers_ck        CHECK ((purpose IN ('STAFF_MEAL', 'ENTERTAINMENT')) = (covers IS NOT NULL) AND (covers IS NULL OR covers > 0)),
    CONSTRAINT inv_issues_beneficiary_ck   CHECK ((purpose = 'ENTERTAINMENT') = (beneficiary IS NOT NULL AND length(btrim(beneficiary)) > 0)),
    CONSTRAINT inv_issues_amenity_ck       CHECK (origin <> 'AMENITY' OR (purpose = 'USE' AND source_date IS NOT NULL))
);
CREATE INDEX inv_issues_day_idx ON inv_issues (property_id, business_date DESC, id DESC);
CREATE INDEX inv_issues_purpose_idx ON inv_issues (property_id, purpose, business_date DESC);

CREATE TABLE inv_issue_lines (
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    issue_id           bigint        NOT NULL,
    line_no            int           NOT NULL,
    item_id            bigint        NOT NULL,
    qty_stock          numeric(18,6) NOT NULL,
    value              numeric(18,3),
    expense_account_id bigint,
    department_id      bigint,
    movement_id        bigint,
    status             varchar(8)    NOT NULL DEFAULT 'APPLIED',
    applied_on         date,
    applied_at         timestamptz,
    PRIMARY KEY (issue_id, line_no),
    CONSTRAINT inv_issue_lines_issue_fk      FOREIGN KEY (property_id, issue_id)          REFERENCES inv_issues (property_id, id),
    CONSTRAINT inv_issue_lines_item_fk       FOREIGN KEY (property_id, item_id)           REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_issue_lines_account_fk    FOREIGN KEY (property_id, expense_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_issue_lines_department_fk FOREIGN KEY (property_id, department_id)    REFERENCES departments (property_id, id),
    CONSTRAINT inv_issue_lines_movement_fk   FOREIGN KEY (property_id, movement_id)       REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_issue_lines_item_uk       UNIQUE (issue_id, item_id),
    CONSTRAINT inv_issue_lines_status_ck     CHECK (status IN ('APPLIED', 'HELD')),
    CONSTRAINT inv_issue_lines_qty_ck        CHECK (qty_stock > 0 AND (value IS NULL OR value >= 0)),
    -- a held line has no movement, no value and no day yet; an applied line has all three
    CONSTRAINT inv_issue_lines_held_ck       CHECK ((status = 'HELD') = (movement_id IS NULL AND value IS NULL AND applied_on IS NULL AND applied_at IS NULL))
);
-- the lines that a count's end releases: the held lines of a warehouse in the order of the documents
CREATE INDEX inv_issue_lines_held_idx ON inv_issue_lines (property_id, issue_id) WHERE status = 'HELD';

-- 5. The stock count and the freeze -----------------------------------------------------------------------------------------------------------
CREATE TABLE inv_counts (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id             bigint        NOT NULL,
    property_id           bigint        NOT NULL,
    cnt_number            varchar(30)   NOT NULL,
    client_uuid           uuid          NOT NULL,
    warehouse_id          bigint        NOT NULL,
    scope                 varchar(8)    NOT NULL,
    category_id           bigint,
    status                varchar(10)   NOT NULL DEFAULT 'OPEN',
    started_at            timestamptz   NOT NULL,
    started_by            bigint        NOT NULL REFERENCES users (id),
    business_date_started date          NOT NULL,
    submitted_at          timestamptz,
    submitted_by          bigint REFERENCES users (id),
    decided_by            bigint REFERENCES users (id),
    decided_at            timestamptz,
    total_variance_value  numeric(18,3),
    note                  varchar(300),
    version               int           NOT NULL DEFAULT 1,
    CONSTRAINT inv_counts_property_fk      FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_counts_warehouse_fk     FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_counts_category_fk      FOREIGN KEY (property_id, category_id)  REFERENCES inv_categories (property_id, id),
    CONSTRAINT inv_counts_number_uk        UNIQUE (property_id, cnt_number),
    CONSTRAINT inv_counts_client_uk        UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_counts_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT inv_counts_status_ck        CHECK (status IN ('OPEN', 'SUBMITTED', 'APPROVED', 'CANCELLED')),
    CONSTRAINT inv_counts_scope_ck         CHECK (scope IN ('ALL', 'CATEGORY', 'LIST') AND (scope = 'CATEGORY') = (category_id IS NOT NULL)),
    CONSTRAINT inv_counts_submitted_ck     CHECK (status IN ('OPEN', 'CANCELLED') OR (submitted_at IS NOT NULL AND submitted_by IS NOT NULL)),
    CONSTRAINT inv_counts_decided_ck       CHECK (status <> 'APPROVED' OR (decided_by IS NOT NULL AND decided_at IS NOT NULL AND total_variance_value IS NOT NULL))
);
-- A warehouse is counted once at a time (5.5 item 1).
CREATE UNIQUE INDEX inv_counts_one_open_uk ON inv_counts (warehouse_id) WHERE status IN ('OPEN', 'SUBMITTED');
CREATE INDEX inv_counts_status_idx ON inv_counts (property_id, status, id DESC);
ALTER TABLE inv_warehouses
    ADD CONSTRAINT inv_warehouses_count_fk FOREIGN KEY (property_id, count_id) REFERENCES inv_counts (property_id, id);
CREATE UNIQUE INDEX inv_warehouses_count_uk ON inv_warehouses (count_id) WHERE count_id IS NOT NULL;

CREATE TABLE inv_count_lines (
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    count_id         bigint        NOT NULL,
    item_id          bigint        NOT NULL,
    book_qty         numeric(18,6) NOT NULL,
    counted_qty      numeric(18,6),
    counted_at       timestamptz,
    entered_unit_id  bigint,
    entered_qty      numeric(18,6),
    expected_qty     numeric(18,6),
    held_before_qty  numeric(18,6),
    variance_qty     numeric(18,6),
    variance_value   numeric(18,3),
    counted_by       bigint REFERENCES users (id),
    note             varchar(200),
    PRIMARY KEY (count_id, item_id),
    CONSTRAINT inv_count_lines_count_fk   FOREIGN KEY (property_id, count_id) REFERENCES inv_counts (property_id, id),
    CONSTRAINT inv_count_lines_item_fk    FOREIGN KEY (property_id, item_id)  REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_count_lines_unit_fk    FOREIGN KEY (property_id, item_id, entered_unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_count_lines_counted_ck CHECK ((counted_qty IS NULL) = (counted_at IS NULL) AND (counted_qty IS NULL OR counted_qty >= 0)),
    CONSTRAINT inv_count_lines_entered_ck CHECK ((entered_unit_id IS NULL) = (entered_qty IS NULL))
);

-- 6. The amenities --------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_amenity_plans (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint      NOT NULL,
    property_id        bigint      NOT NULL,
    version_no         int         NOT NULL,
    effective_from     date        NOT NULL,
    is_enabled         boolean     NOT NULL,
    warehouse_id       bigint,
    department_id      bigint,
    expense_account_id bigint,
    created_at         timestamptz NOT NULL DEFAULT now(),
    created_by         bigint REFERENCES users (id),
    CONSTRAINT inv_amenity_plans_property_fk   FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_amenity_plans_warehouse_fk  FOREIGN KEY (property_id, warehouse_id)        REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_amenity_plans_department_fk FOREIGN KEY (property_id, department_id)       REFERENCES departments (property_id, id),
    CONSTRAINT inv_amenity_plans_account_fk    FOREIGN KEY (property_id, expense_account_id)  REFERENCES gl_accounts (property_id, id),
    CONSTRAINT inv_amenity_plans_version_uk    UNIQUE (property_id, version_no),
    CONSTRAINT inv_amenity_plans_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_amenity_plans_enabled_ck    CHECK (NOT is_enabled OR (warehouse_id IS NOT NULL AND expense_account_id IS NOT NULL))
);
-- a plan is immutable: a save is a new version (4.9 of the amenities, 5.9)
CREATE TRIGGER inv_amenity_plans_append_only BEFORE UPDATE OR DELETE ON inv_amenity_plans FOR EACH ROW EXECUTE FUNCTION forbid_modification();

CREATE TABLE inv_amenity_plan_lines (
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    plan_id      bigint        NOT NULL,
    line_no      int           NOT NULL,
    room_type_id bigint,
    item_id      bigint        NOT NULL,
    unit_id      bigint        NOT NULL,
    qty          numeric(18,6) NOT NULL,
    basis        varchar(10)   NOT NULL,
    PRIMARY KEY (plan_id, line_no),
    CONSTRAINT inv_amenity_plan_lines_plan_fk      FOREIGN KEY (property_id, plan_id)      REFERENCES inv_amenity_plans (property_id, id),
    CONSTRAINT inv_amenity_plan_lines_room_type_fk FOREIGN KEY (property_id, room_type_id) REFERENCES room_types (property_id, id),
    CONSTRAINT inv_amenity_plan_lines_item_fk      FOREIGN KEY (property_id, item_id)      REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_amenity_plan_lines_unit_fk      FOREIGN KEY (property_id, item_id, unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_amenity_plan_lines_qty_ck       CHECK (qty > 0),
    CONSTRAINT inv_amenity_plan_lines_basis_ck     CHECK (basis IN ('PER_ROOM', 'PER_GUEST'))
);
CREATE TRIGGER inv_amenity_plan_lines_append_only BEFORE UPDATE OR DELETE ON inv_amenity_plan_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();

CREATE TABLE inv_amenity_runs (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    business_date      date          NOT NULL,
    plan_id            bigint        NOT NULL,
    issue_id           bigint        NOT NULL,
    rooms              int           NOT NULL,
    guests             int           NOT NULL,
    comp_rooms         int           NOT NULL DEFAULT 0,
    house_use_rooms    int           NOT NULL DEFAULT 0,
    total_value        numeric(18,3) NOT NULL DEFAULT 0,
    status             varchar(10)   NOT NULL DEFAULT 'POSTED',
    reversal_issue_id  bigint,
    created_at         timestamptz   NOT NULL,
    CONSTRAINT inv_amenity_runs_property_fk  FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_amenity_runs_plan_fk      FOREIGN KEY (property_id, plan_id)        REFERENCES inv_amenity_plans (property_id, id),
    CONSTRAINT inv_amenity_runs_issue_fk     FOREIGN KEY (property_id, issue_id)       REFERENCES inv_issues (property_id, id),
    CONSTRAINT inv_amenity_runs_reversal_fk  FOREIGN KEY (property_id, reversal_issue_id) REFERENCES inv_issues (property_id, id),
    -- one document for each business day: the idempotency key of the close (5.9)
    CONSTRAINT inv_amenity_runs_day_uk       UNIQUE (property_id, business_date),
    CONSTRAINT inv_amenity_runs_issue_uk     UNIQUE (issue_id),
    CONSTRAINT inv_amenity_runs_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_amenity_runs_status_ck    CHECK (status IN ('POSTED', 'REVERSED') AND (status = 'REVERSED') = (reversal_issue_id IS NOT NULL)),
    CONSTRAINT inv_amenity_runs_counts_ck    CHECK (rooms >= 0 AND guests >= 0 AND comp_rooms >= 0 AND house_use_rooms >= 0 AND comp_rooms + house_use_rooms <= rooms AND total_value >= 0)
);

CREATE TABLE inv_amenity_run_rooms (
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    run_id         bigint      NOT NULL,
    room_id        bigint      NOT NULL,
    room_type_id   bigint      NOT NULL,
    stay_id        bigint      NOT NULL,
    guests         smallint    NOT NULL,
    occupancy_kind varchar(14) NOT NULL,
    PRIMARY KEY (run_id, room_id),
    CONSTRAINT inv_amenity_run_rooms_run_fk       FOREIGN KEY (property_id, run_id)       REFERENCES inv_amenity_runs (property_id, id),
    CONSTRAINT inv_amenity_run_rooms_room_fk      FOREIGN KEY (property_id, room_id)      REFERENCES rooms (property_id, id),
    CONSTRAINT inv_amenity_run_rooms_room_type_fk FOREIGN KEY (property_id, room_type_id) REFERENCES room_types (property_id, id),
    CONSTRAINT inv_amenity_run_rooms_stay_fk      FOREIGN KEY (property_id, stay_id)      REFERENCES stays (property_id, id),
    CONSTRAINT inv_amenity_run_rooms_kind_ck      CHECK (occupancy_kind IN ('PAID', 'COMPLIMENTARY', 'HOUSE_USE')),
    CONSTRAINT inv_amenity_run_rooms_guests_ck    CHECK (guests >= 1)
);

CREATE TABLE inv_amenity_run_lines (
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    run_id         bigint        NOT NULL,
    line_no        int           NOT NULL,
    plan_id        bigint        NOT NULL,
    plan_line_no   int           NOT NULL,
    item_id        bigint        NOT NULL,
    room_type_id   bigint,
    basis          varchar(10)   NOT NULL,
    qty_per        numeric(18,6) NOT NULL,
    multiplier     int           NOT NULL,
    qty_stock      numeric(18,6) NOT NULL,
    stock_before   numeric(18,6),
    issue_line_no  int           NOT NULL,
    PRIMARY KEY (run_id, line_no),
    CONSTRAINT inv_amenity_run_lines_run_fk       FOREIGN KEY (property_id, run_id)        REFERENCES inv_amenity_runs (property_id, id),
    CONSTRAINT inv_amenity_run_lines_plan_line_fk FOREIGN KEY (plan_id, plan_line_no)      REFERENCES inv_amenity_plan_lines (plan_id, line_no),
    CONSTRAINT inv_amenity_run_lines_item_fk      FOREIGN KEY (property_id, item_id)       REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_amenity_run_lines_basis_ck     CHECK (basis IN ('PER_ROOM', 'PER_GUEST')),
    CONSTRAINT inv_amenity_run_lines_qty_ck       CHECK (qty_per > 0 AND multiplier >= 0 AND qty_stock >= 0)
);

-- 7. One more kind of movement ---------------------------------------------------------------------------------------------------------------
ALTER TABLE inv_movements DROP CONSTRAINT inv_movements_kind_ck;
ALTER TABLE inv_movements ADD CONSTRAINT inv_movements_kind_ck
    CHECK (kind IN ('OPENING', 'RECEIPT', 'RECEIPT_REVERSAL', 'SUPPLIER_RETURN', 'TRANSFER_OUT', 'TRANSFER_IN', 'ISSUE', 'ISSUE_REVERSAL', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT',
                    'COUNT_GAIN', 'COUNT_LOSS', 'POS_SALE', 'POS_SALE_RETURN', 'COGS_TO_WASTE', 'COST_ADJUST', 'NEG_SETTLE'));

-- 8. Document numbers ------------------------------------------------------------------------------------------------------------------------
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_SR',  'SR-'  FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_TRF', 'TRF-' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_ISS', 'ISS-' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_CNT', 'CNT-' FROM properties;

-- +goose Down
-- Refused while the movements hold anything: a requisition, a transfer, an issue, a count, an amenities plan or run.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM inv_requisitions) + (SELECT count(*) FROM inv_transfers) + (SELECT count(*) FROM inv_issues) + (SELECT count(*) FROM inv_counts)
         + (SELECT count(*) FROM inv_amenity_plans) + (SELECT count(*) FROM inv_movements WHERE kind = 'ISSUE_REVERSAL') INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00086: the movements hold % row(s) (requisitions, transfers, issues, counts, amenities plans, reversals of issues)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DELETE FROM document_sequences WHERE sequence_type IN ('INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT');
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET'));

ALTER TABLE inv_movements DROP CONSTRAINT inv_movements_kind_ck;
ALTER TABLE inv_movements ADD CONSTRAINT inv_movements_kind_ck
    CHECK (kind IN ('OPENING', 'RECEIPT', 'RECEIPT_REVERSAL', 'SUPPLIER_RETURN', 'TRANSFER_OUT', 'TRANSFER_IN', 'ISSUE', 'WASTE', 'STAFF_MEAL', 'ENTERTAINMENT',
                    'COUNT_GAIN', 'COUNT_LOSS', 'POS_SALE', 'POS_SALE_RETURN', 'COGS_TO_WASTE', 'COST_ADJUST', 'NEG_SETTLE'));

DROP TABLE inv_amenity_run_lines;
DROP TABLE inv_amenity_run_rooms;
DROP TABLE inv_amenity_runs;
DROP TABLE inv_amenity_plan_lines;
DROP TABLE inv_amenity_plans;
DROP INDEX inv_warehouses_count_uk;
ALTER TABLE inv_warehouses DROP CONSTRAINT inv_warehouses_count_fk;
DROP TABLE inv_count_lines;
DROP TABLE inv_counts;
DROP TABLE inv_issue_lines;
DROP TABLE inv_issues;
DROP TABLE inv_transfer_lines;
DROP TABLE inv_transfers;
ALTER TABLE inv_notices DROP COLUMN requisition_id;
DROP INDEX inv_approval_steps_sr_one_active_uk;
DROP INDEX inv_approval_steps_sr_one_person_uk;
ALTER TABLE inv_approval_steps
    DROP CONSTRAINT inv_approval_steps_doc_ck,
    DROP CONSTRAINT inv_approval_steps_requisition_level_uk,
    DROP CONSTRAINT inv_approval_steps_requisition_fk,
    DROP COLUMN requisition_id,
    ALTER COLUMN request_id SET NOT NULL;
DROP TABLE inv_requisition_lines;
DROP TABLE inv_requisitions;
