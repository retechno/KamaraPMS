-- +goose Up
-- Transaction Group / Split Bill (docs/architecture/20-transaction-group.md). A transaction group is a presentation and operational grouping dimension INSIDE one folio: which part of the
-- bill a ledger line is printed under (A, B, C, D in the screens). It is not a financial folio, creates no separate receivable, and has no effect on the folio balance, the general
-- ledger or the owner of any amount.
--
-- It lives in a table of its own and not as a column of folio_items or payments, because folio_items is append-only (the trigger folio_items_append_only forbids every UPDATE) and a
-- payment may only change POSTED -> VOIDED. A mutable column on them would need the ledger guarantee to be loosened. Here the ledger tables are not touched at all: a line with no
-- row below is in group A, so every existing line (and every new one) is in group A without a data migration, and moving a line between groups is a change of this table only, with no
-- reversal, no posting and no journal. A payment is a ledger line (folio_items.payment_id), so its group is the group of its line.
CREATE TABLE folio_item_groups (
    tenant_id      bigint       NOT NULL,
    property_id    bigint       NOT NULL,
    folio_item_id  bigint       NOT NULL,
    group_code     text         NOT NULL,
    updated_at     timestamptz  NOT NULL DEFAULT now(),
    updated_by     bigint REFERENCES users (id),
    CONSTRAINT folio_item_groups_pk          PRIMARY KEY (property_id, folio_item_id),
    CONSTRAINT folio_item_groups_property_fk FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_item_groups_item_fk     FOREIGN KEY (property_id, folio_item_id)   REFERENCES folio_items (property_id, id),
    -- one capital letter: the screens offer A to D, the table does not hard-code how many groups a bill may have
    CONSTRAINT folio_item_groups_code_ck     CHECK (group_code ~ '^[A-Z]$')
);
CREATE TRIGGER folio_item_groups_set_updated_at BEFORE UPDATE ON folio_item_groups
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE folio_item_groups;
