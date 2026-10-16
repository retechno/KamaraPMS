-- +goose Up
-- Inventory, PR 2 (docs/architecture/35-inventory.md, sections 3.3, 5.3 and 5.8): purchasing and the approval chain.
--
--   departments                      head_user_id, deputy_user_id (who approves level 1 of a request for the department)
--   inv_supplier_items               the last price of an item at a supplier (the default of a request or an order line)
--   inv_requests, inv_request_lines  the purchase request (PR)
--   inv_purchase_orders, inv_po_lines the purchase order (PO)
--   inv_receipts, inv_receipt_lines  the goods receipt (GR), with what has been billed and returned of each line
--   inv_bill_drafts                  the draft supplier bill that a receipt makes (the draft is inventory's own record: payables has no draft state)
--   inv_bill_matches                 the three-way match of a bill line with a receipt line, with the variance and the movements that cleared it
--   inv_returns, inv_return_lines    the return to the supplier, at the cost of the receipt line
--   inv_approval_levels, _settings, _steps, _edits, inv_notices   the approval chain (levels per property, the snapshot per document and round, the edits, the bell)
--   document_sequences               INV_PR, INV_PO, INV_GR, INV_RET
--
-- Differences from the design text, recorded in 35-inventory.md "As built: PR 2":
--   * inv_bill_draft_lines is not a table: the lines of a draft are the lines of its receipt, and what is left to bill is read from them (quantity and value billed and returned are kept on the receipt line);
--   * a receipt has a client_uuid and no separate idempotency key (the client UUID is the key, as for the opening stock);
--   * inv_approval_steps and inv_notices name a request only: the requisition column and its foreign key come with 00086, with the SR;
--   * a purchase order is not edited: it is created (as a draft or sent), sent, cancelled or closed short.

-- 1. The head and the deputy of a department ----------------------------------------------------------------------------------------------------
ALTER TABLE departments
    ADD COLUMN head_user_id   bigint,
    ADD COLUMN deputy_user_id bigint,
    ADD CONSTRAINT departments_head_fk   FOREIGN KEY (tenant_id, head_user_id)   REFERENCES users (tenant_id, id),
    ADD CONSTRAINT departments_deputy_fk FOREIGN KEY (tenant_id, deputy_user_id) REFERENCES users (tenant_id, id),
    ADD CONSTRAINT departments_head_distinct_ck CHECK (head_user_id <> deputy_user_id),
    ADD CONSTRAINT departments_deputy_needs_head_ck CHECK (deputy_user_id IS NULL OR head_user_id IS NOT NULL);

-- 2. What a supplier sells -----------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_supplier_items (
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    supplier_id      bigint        NOT NULL,
    item_id          bigint        NOT NULL,
    purchase_unit_id bigint        NOT NULL,
    last_price       numeric(18,3) NOT NULL,
    supplier_sku     varchar(60),
    updated_at       timestamptz   NOT NULL DEFAULT now(),
    updated_by       bigint REFERENCES users (id),
    PRIMARY KEY (supplier_id, item_id),
    CONSTRAINT inv_supplier_items_property_fk FOREIGN KEY (tenant_id, property_id)                REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_supplier_items_supplier_fk FOREIGN KEY (property_id, supplier_id)              REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_supplier_items_unit_fk     FOREIGN KEY (property_id, item_id, purchase_unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_supplier_items_price_ck    CHECK (last_price >= 0)
);

-- 3. The approval chain -----------------------------------------------------------------------------------------------------------------------
-- The levels of a property and a kind of document (35-inventory.md 5.8). A property that has no row in inv_approval_settings has not been given the default chain yet: the first read makes it.
CREATE TABLE inv_approval_settings (
    tenant_id           bigint      NOT NULL,
    property_id         bigint      NOT NULL,
    doc_type            varchar(2)  NOT NULL,
    skip_level1_if_head boolean     NOT NULL DEFAULT true,
    approver_may_edit   boolean     NOT NULL DEFAULT true,
    version             int         NOT NULL DEFAULT 1,
    updated_at          timestamptz NOT NULL DEFAULT now(),
    updated_by          bigint REFERENCES users (id),
    PRIMARY KEY (property_id, doc_type),
    CONSTRAINT inv_approval_settings_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_approval_settings_doc_ck      CHECK (doc_type IN ('PR', 'SR'))
);
CREATE TRIGGER inv_approval_settings_set_updated_at BEFORE UPDATE ON inv_approval_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE inv_approval_levels (
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    doc_type       varchar(2)    NOT NULL,
    level_no       smallint      NOT NULL,
    name           varchar(60)   NOT NULL,
    approver_kind  varchar(12)   NOT NULL,
    permission     varchar(30),
    condition_kind varchar(12)   NOT NULL DEFAULT 'ALWAYS',
    threshold      numeric(18,3),
    updated_at     timestamptz   NOT NULL DEFAULT now(),
    updated_by     bigint REFERENCES users (id),
    PRIMARY KEY (property_id, doc_type, level_no),
    CONSTRAINT inv_approval_levels_property_fk   FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_approval_levels_doc_ck        CHECK (doc_type IN ('PR', 'SR')),
    CONSTRAINT inv_approval_levels_no_ck         CHECK (level_no BETWEEN 1 AND 5),
    CONSTRAINT inv_approval_levels_name_ck       CHECK (length(btrim(name)) > 0),
    CONSTRAINT inv_approval_levels_kind_ck       CHECK (approver_kind IN ('DEPT_HEAD', 'PERMISSION')),
    CONSTRAINT inv_approval_levels_permission_ck CHECK ((approver_kind = 'PERMISSION') = (permission IS NOT NULL)
                                                        AND (permission IS NULL OR permission IN ('inventory.cost_control', 'inventory.final_approve', 'inventory.sr_final_approve'))),
    CONSTRAINT inv_approval_levels_condition_ck  CHECK (condition_kind IN ('ALWAYS', 'TOTAL_ABOVE') AND (condition_kind = 'TOTAL_ABOVE') = (threshold IS NOT NULL)
                                                        AND (threshold IS NULL OR threshold >= 0))
);
CREATE TRIGGER inv_approval_levels_set_updated_at BEFORE UPDATE ON inv_approval_levels FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 4. The purchase request -----------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_requests (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    request_number varchar(30)   NOT NULL,
    client_uuid    uuid          NOT NULL,
    requester_id   bigint        NOT NULL,
    department_id  bigint,
    warehouse_id   bigint,
    needed_on      date,
    status         varchar(10)   NOT NULL DEFAULT 'DRAFT',
    est_total      numeric(18,3) NOT NULL DEFAULT 0,
    cycle          smallint      NOT NULL DEFAULT 1,
    submitted_at   timestamptz,
    returned_note  varchar(500),
    note           varchar(300),
    version        int           NOT NULL DEFAULT 1,
    created_at     timestamptz   NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    updated_at     timestamptz   NOT NULL DEFAULT now(),
    updated_by     bigint REFERENCES users (id),
    CONSTRAINT inv_requests_property_fk      FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_requests_requester_fk     FOREIGN KEY (tenant_id, requester_id)       REFERENCES users (tenant_id, id),
    CONSTRAINT inv_requests_department_fk    FOREIGN KEY (property_id, department_id)    REFERENCES departments (property_id, id),
    CONSTRAINT inv_requests_warehouse_fk     FOREIGN KEY (property_id, warehouse_id)     REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_requests_number_uk        UNIQUE (property_id, request_number),
    CONSTRAINT inv_requests_client_uk        UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_requests_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT inv_requests_status_ck        CHECK (status IN ('DRAFT', 'SUBMITTED', 'APPROVED', 'RETURNED', 'CONVERTED', 'CANCELLED')),
    CONSTRAINT inv_requests_department_ck    CHECK (status IN ('DRAFT', 'CANCELLED') OR department_id IS NOT NULL),
    CONSTRAINT inv_requests_total_ck         CHECK (est_total >= 0),
    CONSTRAINT inv_requests_cycle_ck         CHECK (cycle >= 1),
    CONSTRAINT inv_requests_returned_ck      CHECK (status <> 'RETURNED' OR returned_note IS NOT NULL)
);
CREATE INDEX inv_requests_status_idx ON inv_requests (property_id, status, id DESC);
CREATE TRIGGER inv_requests_set_updated_at BEFORE UPDATE ON inv_requests FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE inv_request_lines (
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    request_id       bigint        NOT NULL,
    line_no          int           NOT NULL,
    item_id          bigint        NOT NULL,
    purchase_unit_id bigint        NOT NULL,
    qty_purchase     numeric(18,6) NOT NULL,
    qty_stock        numeric(18,6) NOT NULL,
    est_unit_price   numeric(18,3),
    supplier_id      bigint,
    note             varchar(200),
    PRIMARY KEY (request_id, line_no),
    CONSTRAINT inv_request_lines_request_fk  FOREIGN KEY (property_id, request_id)                  REFERENCES inv_requests (property_id, id),
    CONSTRAINT inv_request_lines_item_fk     FOREIGN KEY (property_id, item_id)                     REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_request_lines_unit_fk     FOREIGN KEY (property_id, item_id, purchase_unit_id)   REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_request_lines_supplier_fk FOREIGN KEY (property_id, supplier_id)                 REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_request_lines_item_uk     UNIQUE (request_id, item_id),
    CONSTRAINT inv_request_lines_qty_ck      CHECK (qty_purchase > 0 AND qty_stock > 0),
    CONSTRAINT inv_request_lines_price_ck    CHECK (est_unit_price IS NULL OR est_unit_price >= 0)
);

-- 5. The steps of the chain: the snapshot of one round of one request, and the decisions ------------------------------------------------------------
CREATE TABLE inv_approval_steps (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    request_id     bigint        NOT NULL,
    cycle          smallint      NOT NULL,
    level_no       smallint      NOT NULL,
    name           varchar(60)   NOT NULL,
    approver_kind  varchar(12)   NOT NULL,
    permission     varchar(30),
    condition_kind varchar(12)   NOT NULL,
    threshold      numeric(18,3),
    edit_allowed   boolean       NOT NULL,
    status         varchar(12)   NOT NULL,
    skip_reason    varchar(20),
    activated_at   timestamptz,
    decided_by     bigint,
    decided_at     timestamptz,
    total_seen     numeric(18,3),
    note           varchar(500),
    CONSTRAINT inv_approval_steps_property_fk  FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_approval_steps_request_fk   FOREIGN KEY (property_id, request_id)   REFERENCES inv_requests (property_id, id),
    CONSTRAINT inv_approval_steps_decider_fk   FOREIGN KEY (tenant_id, decided_by)     REFERENCES users (tenant_id, id),
    CONSTRAINT inv_approval_steps_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_approval_steps_level_uk     UNIQUE (request_id, cycle, level_no),
    CONSTRAINT inv_approval_steps_kind_ck      CHECK (approver_kind IN ('DEPT_HEAD', 'PERMISSION') AND (approver_kind = 'PERMISSION') = (permission IS NOT NULL)),
    CONSTRAINT inv_approval_steps_cond_ck      CHECK (condition_kind IN ('ALWAYS', 'TOTAL_ABOVE') AND (condition_kind = 'TOTAL_ABOVE') = (threshold IS NOT NULL)),
    CONSTRAINT inv_approval_steps_status_ck    CHECK (status IN ('WAITING', 'ACTIVE', 'APPROVED', 'RETURNED', 'SKIPPED', 'NOT_REACHED')),
    CONSTRAINT inv_approval_steps_skip_ck      CHECK ((status = 'SKIPPED') = (skip_reason IS NOT NULL) AND (skip_reason IS NULL OR skip_reason IN ('CONDITION', 'REQUESTER_IS_HEAD'))),
    CONSTRAINT inv_approval_steps_decided_ck   CHECK ((status IN ('APPROVED', 'RETURNED')) = (decided_by IS NOT NULL AND decided_at IS NOT NULL))
);
-- One person decides one level of a request in a round, and a round has one active step: both held by the database, not only by the code (R1, 5.8).
CREATE UNIQUE INDEX inv_approval_steps_one_person_uk ON inv_approval_steps (request_id, cycle, decided_by) WHERE status = 'APPROVED';
CREATE UNIQUE INDEX inv_approval_steps_one_active_uk ON inv_approval_steps (request_id, cycle) WHERE status = 'ACTIVE';
CREATE INDEX inv_approval_steps_active_idx ON inv_approval_steps (property_id, status) WHERE status = 'ACTIVE';

CREATE TABLE inv_approval_edits (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id  bigint        NOT NULL,
    property_id bigint       NOT NULL,
    step_id    bigint        NOT NULL,
    line_no    int           NOT NULL,
    field      varchar(15)   NOT NULL,
    old_value  numeric(18,6) NOT NULL,
    new_value  numeric(18,6) NOT NULL,
    edited_by  bigint        NOT NULL REFERENCES users (id),
    edited_at  timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT inv_approval_edits_step_fk  FOREIGN KEY (property_id, step_id) REFERENCES inv_approval_steps (property_id, id),
    CONSTRAINT inv_approval_edits_field_ck CHECK (field IN ('QTY', 'EST_UNIT_PRICE'))
);
CREATE TRIGGER inv_approval_edits_append_only BEFORE UPDATE OR DELETE ON inv_approval_edits FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER inv_approval_edits_no_truncate BEFORE TRUNCATE ON inv_approval_edits FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

CREATE TABLE inv_notices (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    user_id     bigint      NOT NULL,
    kind        varchar(20) NOT NULL,
    step_id     bigint,
    request_id  bigint,
    created_at  timestamptz NOT NULL DEFAULT now(),
    seen_at     timestamptz,
    CONSTRAINT inv_notices_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_notices_user_fk     FOREIGN KEY (tenant_id, user_id)      REFERENCES users (tenant_id, id),
    CONSTRAINT inv_notices_step_fk     FOREIGN KEY (property_id, step_id)    REFERENCES inv_approval_steps (property_id, id),
    CONSTRAINT inv_notices_request_fk  FOREIGN KEY (property_id, request_id) REFERENCES inv_requests (property_id, id),
    CONSTRAINT inv_notices_kind_ck     CHECK (kind IN ('APPROVAL_WAITING', 'APPROVED', 'RETURNED', 'CHAIN_STUCK'))
);
CREATE INDEX inv_notices_user_idx ON inv_notices (user_id, property_id, id DESC);
CREATE INDEX inv_notices_unseen_idx ON inv_notices (user_id, property_id) WHERE seen_at IS NULL;

-- 6. The purchase order -----------------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_purchase_orders (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    po_number    varchar(30)   NOT NULL,
    client_uuid  uuid          NOT NULL,
    supplier_id  bigint        NOT NULL,
    warehouse_id bigint        NOT NULL,
    request_id   bigint,
    order_date   date          NOT NULL,
    expected_on  date,
    status       varchar(10)   NOT NULL DEFAULT 'DRAFT',
    notes        varchar(300),
    total        numeric(18,3) NOT NULL DEFAULT 0,
    version      int           NOT NULL DEFAULT 1,
    created_at   timestamptz   NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    updated_at   timestamptz   NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id),
    CONSTRAINT inv_purchase_orders_property_fk    FOREIGN KEY (tenant_id, property_id)     REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_purchase_orders_supplier_fk    FOREIGN KEY (property_id, supplier_id)   REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_purchase_orders_warehouse_fk   FOREIGN KEY (property_id, warehouse_id)  REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_purchase_orders_request_fk     FOREIGN KEY (property_id, request_id)    REFERENCES inv_requests (property_id, id),
    CONSTRAINT inv_purchase_orders_number_uk      UNIQUE (property_id, po_number),
    CONSTRAINT inv_purchase_orders_client_uk      UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_purchase_orders_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_purchase_orders_status_ck      CHECK (status IN ('DRAFT', 'ORDERED', 'PARTIAL', 'RECEIVED', 'CLOSED', 'CANCELLED')),
    CONSTRAINT inv_purchase_orders_total_ck       CHECK (total >= 0)
);
CREATE INDEX inv_purchase_orders_status_idx ON inv_purchase_orders (property_id, status, id DESC);
CREATE TRIGGER inv_purchase_orders_set_updated_at BEFORE UPDATE ON inv_purchase_orders FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE inv_po_lines (
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    po_id              bigint        NOT NULL,
    line_no            int           NOT NULL,
    item_id            bigint        NOT NULL,
    purchase_unit_id   bigint        NOT NULL,
    qty_purchase       numeric(18,6) NOT NULL,
    qty_stock          numeric(18,6) NOT NULL,
    unit_price         numeric(18,3) NOT NULL,
    amount             numeric(18,3) NOT NULL,
    qty_received_stock numeric(18,6) NOT NULL DEFAULT 0,
    closed             boolean       NOT NULL DEFAULT false,
    request_id         bigint,
    request_line_no    int,
    PRIMARY KEY (po_id, line_no),
    CONSTRAINT inv_po_lines_po_fk       FOREIGN KEY (property_id, po_id)                   REFERENCES inv_purchase_orders (property_id, id),
    CONSTRAINT inv_po_lines_item_fk     FOREIGN KEY (property_id, item_id)                 REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_po_lines_unit_fk     FOREIGN KEY (property_id, item_id, purchase_unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_po_lines_request_fk  FOREIGN KEY (request_id, request_line_no)          REFERENCES inv_request_lines (request_id, line_no),
    CONSTRAINT inv_po_lines_item_uk     UNIQUE (po_id, item_id),
    CONSTRAINT inv_po_lines_request_uk  UNIQUE (request_id, request_line_no),
    CONSTRAINT inv_po_lines_qty_ck      CHECK (qty_purchase > 0 AND qty_stock > 0 AND qty_received_stock >= 0),
    CONSTRAINT inv_po_lines_price_ck    CHECK (unit_price >= 0 AND amount >= 0),
    CONSTRAINT inv_po_lines_request_ck  CHECK ((request_id IS NULL) = (request_line_no IS NULL))
);

-- 7. The goods receipt and its draft bill -----------------------------------------------------------------------------------------------------
CREATE TABLE inv_receipts (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    receipt_number     varchar(30)   NOT NULL,
    client_uuid        uuid          NOT NULL,
    supplier_id        bigint        NOT NULL,
    po_id              bigint,
    warehouse_id       bigint        NOT NULL,
    received_on        date          NOT NULL,
    delivery_note      varchar(60),
    delivery_note_date date,
    status             varchar(10)   NOT NULL DEFAULT 'POSTED',
    total_value        numeric(18,3) NOT NULL,
    note               varchar(300),
    reversed_at        timestamptz,
    reversed_by        bigint REFERENCES users (id),
    reverse_reason     varchar(300),
    created_at         timestamptz   NOT NULL DEFAULT now(),
    created_by         bigint REFERENCES users (id),
    CONSTRAINT inv_receipts_property_fk    FOREIGN KEY (tenant_id, property_id)     REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_receipts_supplier_fk    FOREIGN KEY (property_id, supplier_id)   REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_receipts_po_fk          FOREIGN KEY (property_id, po_id)         REFERENCES inv_purchase_orders (property_id, id),
    CONSTRAINT inv_receipts_warehouse_fk   FOREIGN KEY (property_id, warehouse_id)  REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_receipts_number_uk      UNIQUE (property_id, receipt_number),
    CONSTRAINT inv_receipts_client_uk      UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_receipts_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_receipts_status_ck      CHECK (status IN ('POSTED', 'REVERSED')),
    CONSTRAINT inv_receipts_reversed_ck    CHECK ((status = 'REVERSED') = (reversed_at IS NOT NULL)),
    CONSTRAINT inv_receipts_total_ck       CHECK (total_value >= 0)
);
CREATE INDEX inv_receipts_po_idx ON inv_receipts (property_id, po_id) WHERE po_id IS NOT NULL;
CREATE INDEX inv_receipts_date_idx ON inv_receipts (property_id, received_on DESC, id DESC);

CREATE TABLE inv_receipt_lines (
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    receipt_id         bigint        NOT NULL,
    line_no            int           NOT NULL,
    item_id            bigint        NOT NULL,
    po_line_no         int,
    purchase_unit_id   bigint        NOT NULL,
    qty_purchase       numeric(18,6) NOT NULL,
    qty_stock          numeric(18,6) NOT NULL,
    unit_price         numeric(18,3) NOT NULL,
    value              numeric(18,3) NOT NULL,
    movement_id        bigint        NOT NULL,
    qty_billed_stock   numeric(18,6) NOT NULL DEFAULT 0,
    value_billed       numeric(18,3) NOT NULL DEFAULT 0,
    qty_returned_stock numeric(18,6) NOT NULL DEFAULT 0,
    value_returned     numeric(18,3) NOT NULL DEFAULT 0,
    PRIMARY KEY (receipt_id, line_no),
    CONSTRAINT inv_receipt_lines_receipt_fk  FOREIGN KEY (property_id, receipt_id)               REFERENCES inv_receipts (property_id, id),
    CONSTRAINT inv_receipt_lines_item_fk     FOREIGN KEY (property_id, item_id)                  REFERENCES inv_items (property_id, id),
    CONSTRAINT inv_receipt_lines_unit_fk     FOREIGN KEY (property_id, item_id, purchase_unit_id) REFERENCES inv_item_units (property_id, item_id, unit_id),
    CONSTRAINT inv_receipt_lines_movement_fk FOREIGN KEY (property_id, movement_id)              REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_receipt_lines_property_line_uk UNIQUE (property_id, receipt_id, line_no),
    CONSTRAINT inv_receipt_lines_item_uk     UNIQUE (receipt_id, item_id),
    CONSTRAINT inv_receipt_lines_qty_ck      CHECK (qty_purchase > 0 AND qty_stock > 0 AND unit_price >= 0 AND value >= 0),
    CONSTRAINT inv_receipt_lines_left_ck     CHECK (qty_billed_stock >= 0 AND qty_returned_stock >= 0 AND qty_billed_stock <= qty_stock AND qty_returned_stock <= qty_stock
                                                    AND value_billed >= 0 AND value_returned >= 0)
);

CREATE TABLE inv_bill_drafts (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    receipt_id  bigint      NOT NULL,
    supplier_id bigint      NOT NULL,
    status      varchar(10) NOT NULL DEFAULT 'OPEN',
    version     int         NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT inv_bill_drafts_property_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_bill_drafts_receipt_fk     FOREIGN KEY (property_id, receipt_id)   REFERENCES inv_receipts (property_id, id),
    CONSTRAINT inv_bill_drafts_supplier_fk    FOREIGN KEY (property_id, supplier_id)  REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_bill_drafts_receipt_uk     UNIQUE (receipt_id),
    CONSTRAINT inv_bill_drafts_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_bill_drafts_status_ck      CHECK (status IN ('OPEN', 'PARTIAL', 'BILLED', 'DISCARDED'))
);
CREATE INDEX inv_bill_drafts_status_idx ON inv_bill_drafts (property_id, status, supplier_id);
CREATE TRIGGER inv_bill_drafts_set_updated_at BEFORE UPDATE ON inv_bill_drafts FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- 8. The return to the supplier ---------------------------------------------------------------------------------------------------------------
CREATE TABLE inv_returns (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    return_number  varchar(30)   NOT NULL,
    client_uuid    uuid          NOT NULL,
    supplier_id    bigint        NOT NULL,
    warehouse_id   bigint        NOT NULL,
    receipt_id     bigint        NOT NULL,
    reason         varchar(300)  NOT NULL,
    total_value    numeric(18,3) NOT NULL,
    credit_note_id bigint,
    created_at     timestamptz   NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    CONSTRAINT inv_returns_property_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_returns_supplier_fk    FOREIGN KEY (property_id, supplier_id)  REFERENCES suppliers (property_id, id),
    CONSTRAINT inv_returns_warehouse_fk   FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id),
    CONSTRAINT inv_returns_receipt_fk     FOREIGN KEY (property_id, receipt_id)   REFERENCES inv_receipts (property_id, id),
    CONSTRAINT inv_returns_credit_fk      FOREIGN KEY (property_id, credit_note_id) REFERENCES supplier_credit_notes (property_id, id),
    CONSTRAINT inv_returns_number_uk      UNIQUE (property_id, return_number),
    CONSTRAINT inv_returns_client_uk      UNIQUE (property_id, client_uuid),
    CONSTRAINT inv_returns_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT inv_returns_reason_ck      CHECK (length(btrim(reason)) > 0),
    CONSTRAINT inv_returns_total_ck       CHECK (total_value >= 0)
);

CREATE TABLE inv_return_lines (
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    return_id       bigint        NOT NULL,
    line_no         int           NOT NULL,
    receipt_id      bigint        NOT NULL,
    receipt_line_no int           NOT NULL,
    item_id         bigint        NOT NULL,
    qty_stock       numeric(18,6) NOT NULL,
    value           numeric(18,3) NOT NULL,
    movement_id     bigint        NOT NULL,
    PRIMARY KEY (return_id, line_no),
    CONSTRAINT inv_return_lines_return_fk   FOREIGN KEY (property_id, return_id)                 REFERENCES inv_returns (property_id, id),
    CONSTRAINT inv_return_lines_receipt_fk  FOREIGN KEY (property_id, receipt_id, receipt_line_no) REFERENCES inv_receipt_lines (property_id, receipt_id, line_no),
    CONSTRAINT inv_return_lines_movement_fk FOREIGN KEY (property_id, movement_id)               REFERENCES inv_movements (property_id, id),
    CONSTRAINT inv_return_lines_line_uk     UNIQUE (return_id, receipt_id, receipt_line_no),
    CONSTRAINT inv_return_lines_qty_ck      CHECK (qty_stock > 0 AND value >= 0)
);

-- 9. The three-way match ----------------------------------------------------------------------------------------------------------------------
-- One row for each receipt line of each bill line. A credit note linked to a return is a row with a negative quantity. What is billed of a receipt line is the sum of its live rows (unmatched_at IS NULL).
CREATE TABLE inv_bill_matches (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id             bigint        NOT NULL,
    property_id           bigint        NOT NULL,
    receipt_id            bigint        NOT NULL,
    receipt_line_no       int           NOT NULL,
    bill_id               bigint        NOT NULL,
    bill_line_no          int           NOT NULL,
    qty_stock             numeric(18,6) NOT NULL,
    bill_cost             numeric(18,3) NOT NULL,
    receipt_value         numeric(18,3) NOT NULL,
    variance              numeric(18,3) NOT NULL,
    inventory_part        numeric(18,3) NOT NULL DEFAULT 0,
    ppv_part              numeric(18,3) NOT NULL DEFAULT 0,
    clearing_movement_ids bigint[]      NOT NULL DEFAULT '{}',
    credit_note_id        bigint,
    return_id             bigint,
    created_at            timestamptz   NOT NULL DEFAULT now(),
    created_by            bigint REFERENCES users (id),
    unmatched_at          timestamptz,
    unmatched_by          bigint REFERENCES users (id),
    CONSTRAINT inv_bill_matches_property_fk FOREIGN KEY (tenant_id, property_id)                      REFERENCES properties (tenant_id, id),
    CONSTRAINT inv_bill_matches_line_fk     FOREIGN KEY (property_id, receipt_id, receipt_line_no)    REFERENCES inv_receipt_lines (property_id, receipt_id, line_no),
    CONSTRAINT inv_bill_matches_bill_fk     FOREIGN KEY (property_id, bill_id)                        REFERENCES supplier_bills (property_id, id),
    CONSTRAINT inv_bill_matches_credit_fk   FOREIGN KEY (property_id, credit_note_id)                 REFERENCES supplier_credit_notes (property_id, id),
    CONSTRAINT inv_bill_matches_return_fk   FOREIGN KEY (property_id, return_id)                      REFERENCES inv_returns (property_id, id),
    CONSTRAINT inv_bill_matches_qty_ck      CHECK (qty_stock <> 0),
    CONSTRAINT inv_bill_matches_credit_ck   CHECK ((credit_note_id IS NULL) = (return_id IS NULL) AND ((credit_note_id IS NOT NULL) = (qty_stock < 0))),
    CONSTRAINT inv_bill_matches_split_ck    CHECK (inventory_part + ppv_part = variance),
    CONSTRAINT inv_bill_matches_unmatch_ck  CHECK (unmatched_by IS NULL OR unmatched_at IS NOT NULL)
);
CREATE INDEX inv_bill_matches_bill_idx ON inv_bill_matches (property_id, bill_id) WHERE unmatched_at IS NULL;
CREATE INDEX inv_bill_matches_line_idx ON inv_bill_matches (property_id, receipt_id, receipt_line_no) WHERE unmatched_at IS NULL;
-- A bill line is matched to one receipt line, once, while the match stands.
CREATE UNIQUE INDEX inv_bill_matches_bill_line_uk ON inv_bill_matches (property_id, bill_id, bill_line_no) WHERE unmatched_at IS NULL AND credit_note_id IS NULL;

-- 10. Document numbers ------------------------------------------------------------------------------------------------------------------------
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_PR', 'PR-' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_PO', 'PO-' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_GR', 'GR-' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'INV_RET', 'RET-' FROM properties;

-- +goose Down
-- Refused while purchasing holds anything: a request, an order, a receipt, a return, a chain, a notice, a supplier item or a head of a department.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM inv_requests) + (SELECT count(*) FROM inv_purchase_orders) + (SELECT count(*) FROM inv_receipts) + (SELECT count(*) FROM inv_returns)
         + (SELECT count(*) FROM inv_approval_levels) + (SELECT count(*) FROM inv_approval_settings) + (SELECT count(*) FROM inv_notices) + (SELECT count(*) FROM inv_supplier_items)
         + (SELECT count(*) FROM departments WHERE head_user_id IS NOT NULL OR deputy_user_id IS NOT NULL) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00085: purchasing holds % row(s) (requests, orders, receipts, returns, approval chains, notices, supplier items, department heads)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DELETE FROM document_sequences WHERE sequence_type IN ('INV_PR', 'INV_PO', 'INV_GR', 'INV_RET');
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING'));

DROP TABLE inv_bill_matches;
DROP TABLE inv_return_lines;
DROP TABLE inv_returns;
DROP TABLE inv_bill_drafts;
DROP TABLE inv_receipt_lines;
DROP TABLE inv_receipts;
DROP TABLE inv_po_lines;
DROP TABLE inv_purchase_orders;
DROP TABLE inv_notices;
DROP TABLE inv_approval_edits;
DROP TABLE inv_approval_steps;
DROP TABLE inv_request_lines;
DROP TABLE inv_requests;
DROP TABLE inv_approval_levels;
DROP TABLE inv_approval_settings;
DROP TABLE inv_supplier_items;
ALTER TABLE departments
    DROP CONSTRAINT departments_deputy_needs_head_ck,
    DROP CONSTRAINT departments_head_distinct_ck,
    DROP CONSTRAINT departments_deputy_fk,
    DROP CONSTRAINT departments_head_fk,
    DROP COLUMN deputy_user_id,
    DROP COLUMN head_user_id;
