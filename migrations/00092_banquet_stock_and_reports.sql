-- +goose Up
-- Banquet, PR 4 (docs/architecture/36-banquet.md, sections 3.5, 4.9, 4.10, F12, F13, Q3, Q11): the deduction of stock and the reports.
--
--   banquet_settings          the warehouse the banquet takes its goods from (a banquet without one deducts nothing)
--   banquet_posting_items     an id, so that a queue row can name the line of the posting it came from
--   inv_consumption_queue     widened: a row comes from a POS line or from a line of a banquet posting, exactly one (F12)
--   inv_fb_cost_days          a source (POS or BANQUET): the revenue and the cost of the banquet sit beside the outlets (F13)
--   inv_cost_targets          a source, so the banquet can have a target of its own
--   indexes                   for the reports of 4.10

-- 1. The warehouse of the banquet -----------------------------------------------------------------------------------------------------------------
ALTER TABLE banquet_settings
    ADD COLUMN warehouse_id bigint,
    ADD CONSTRAINT banquet_settings_warehouse_fk FOREIGN KEY (property_id, warehouse_id) REFERENCES inv_warehouses (property_id, id);

-- 2. A line of a posting has an id --------------------------------------------------------------------------------------------------------------
ALTER TABLE banquet_posting_items
    ADD COLUMN id bigint NOT NULL GENERATED ALWAYS AS IDENTITY,
    ADD CONSTRAINT banquet_posting_items_property_id_uk UNIQUE (property_id, id);

-- 3. The queue ----------------------------------------------------------------------------------------------------------------------------------
-- A banquet row has no POS line and no outlet; the department of the cost is the one of the charge code of the line (the department the revenue is booked to), kept on the row so that a later change of the
-- charge code changes nothing that was queued. A banquet only sells: it has no void row (Q3).
ALTER TABLE inv_consumption_queue
    ADD COLUMN source                  varchar(8) NOT NULL DEFAULT 'POS',
    ADD COLUMN banquet_posting_line_id bigint,
    ADD COLUMN department_id           bigint,
    ALTER COLUMN pos_bill_line_id DROP NOT NULL,
    ALTER COLUMN outlet_id        DROP NOT NULL,
    ADD CONSTRAINT inv_consumption_queue_banquet_line_fk  FOREIGN KEY (property_id, banquet_posting_line_id) REFERENCES banquet_posting_items (property_id, id),
    ADD CONSTRAINT inv_consumption_queue_department_fk    FOREIGN KEY (property_id, department_id)           REFERENCES departments (property_id, id),
    ADD CONSTRAINT inv_consumption_queue_banquet_kind_uk  UNIQUE (banquet_posting_line_id, kind),
    ADD CONSTRAINT inv_consumption_queue_source_ck        CHECK (source IN ('POS', 'BANQUET')),
    ADD CONSTRAINT inv_consumption_queue_one_source_ck    CHECK (
        (source = 'POS'     AND pos_bill_line_id IS NOT NULL AND outlet_id IS NOT NULL AND banquet_posting_line_id IS NULL AND department_id IS NULL)
     OR (source = 'BANQUET' AND pos_bill_line_id IS NULL     AND outlet_id IS NULL     AND banquet_posting_line_id IS NOT NULL AND kind = 'SALE'));

-- 4. The cost tables ----------------------------------------------------------------------------------------------------------------------------
-- The revenue and the cost of the banquet have no outlet: the source tells them from the rows of the warehouses that have no outlet (waste, staff meals).
ALTER TABLE inv_fb_cost_days
    ADD COLUMN source varchar(8) NOT NULL DEFAULT 'POS',
    ADD CONSTRAINT inv_fb_cost_days_source_ck CHECK (source IN ('POS', 'BANQUET') AND (source = 'POS' OR outlet_id IS NULL));
DROP INDEX inv_fb_cost_days_uk;
CREATE UNIQUE INDEX inv_fb_cost_days_uk ON inv_fb_cost_days (property_id, business_date, source, COALESCE(outlet_id, 0), class);

ALTER TABLE inv_cost_targets
    ADD COLUMN source varchar(8) NOT NULL DEFAULT 'POS',
    ADD CONSTRAINT inv_cost_targets_source_ck CHECK (source IN ('POS', 'BANQUET') AND (source = 'POS' OR outlet_id IS NULL));
DROP INDEX inv_cost_targets_uk;
CREATE UNIQUE INDEX inv_cost_targets_uk ON inv_cost_targets (property_id, source, COALESCE(outlet_id, 0), class);

-- The days that closed with a banquet posting were stored without the banquet revenue (the revenue is read from this PR on). The stored rows of those days are derived data: they are dropped, and the reports compute
-- the days again from the ledger with the same function (35 21: a figure is the same whichever way it was read). The balances of the stock (inv_balance_days) did not change, so they stay.
DELETE FROM inv_fb_cost_days d WHERE EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = d.property_id AND p.business_date = d.business_date);
DELETE FROM inv_cost_days d    WHERE EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = d.property_id AND p.business_date = d.business_date);
DELETE FROM inv_snapshot_days d WHERE EXISTS (SELECT 1 FROM banquet_session_postings p WHERE p.property_id = d.property_id AND p.business_date = d.business_date);

-- 5. The reports of 4.10 --------------------------------------------------------------------------------------------------------------------------
CREATE INDEX banquet_events_created_idx  ON banquet_events (property_id, created_at);
CREATE INDEX banquet_events_definite_idx ON banquet_events (property_id, definite_at) WHERE definite_at IS NOT NULL;
CREATE INDEX banquet_events_closed_idx   ON banquet_events (property_id, closed_at)   WHERE closed_at IS NOT NULL;

-- +goose Down
-- Refused while the stock of the banquet holds anything: a warehouse set, a queue row or a cost row of the banquet, or a target of its own. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM banquet_settings WHERE warehouse_id IS NOT NULL) + (SELECT count(*) FROM inv_consumption_queue WHERE source = 'BANQUET')
         + (SELECT count(*) FROM inv_fb_cost_days WHERE source = 'BANQUET') + (SELECT count(*) FROM inv_cost_targets WHERE source = 'BANQUET') INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00092: the stock of the banquet holds % row(s) (a warehouse, queue rows, cost rows or a target)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX banquet_events_closed_idx;
DROP INDEX banquet_events_definite_idx;
DROP INDEX banquet_events_created_idx;

DROP INDEX inv_cost_targets_uk;
ALTER TABLE inv_cost_targets DROP CONSTRAINT inv_cost_targets_source_ck, DROP COLUMN source;
CREATE UNIQUE INDEX inv_cost_targets_uk ON inv_cost_targets (property_id, COALESCE(outlet_id, 0), class);

DROP INDEX inv_fb_cost_days_uk;
ALTER TABLE inv_fb_cost_days DROP CONSTRAINT inv_fb_cost_days_source_ck, DROP COLUMN source;
CREATE UNIQUE INDEX inv_fb_cost_days_uk ON inv_fb_cost_days (property_id, business_date, COALESCE(outlet_id, 0), class);

ALTER TABLE inv_consumption_queue
    DROP CONSTRAINT inv_consumption_queue_one_source_ck,
    DROP CONSTRAINT inv_consumption_queue_source_ck,
    DROP CONSTRAINT inv_consumption_queue_banquet_kind_uk,
    DROP CONSTRAINT inv_consumption_queue_department_fk,
    DROP CONSTRAINT inv_consumption_queue_banquet_line_fk,
    DROP COLUMN department_id,
    DROP COLUMN banquet_posting_line_id,
    DROP COLUMN source,
    ALTER COLUMN outlet_id        SET NOT NULL,
    ALTER COLUMN pos_bill_line_id SET NOT NULL;

ALTER TABLE banquet_posting_items DROP CONSTRAINT banquet_posting_items_property_id_uk, DROP COLUMN id;

ALTER TABLE banquet_settings DROP CONSTRAINT banquet_settings_warehouse_fk, DROP COLUMN warehouse_id;
