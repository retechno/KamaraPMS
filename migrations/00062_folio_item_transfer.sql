-- +goose Up
-- Transfer of a charge between folios of one reservation (design: docs/architecture/18-architecture-decisions.md section 3.7, rule 6). The charge is reversed on the source folio and
-- charged again on the target folio as a copy of the original (amounts, components, revenue account, department, service date), so tax rounding cannot drift. The new item has the
-- source TRANSFER, and the two items carry the reference FOLIO_TRANSFER with the id of the original.
ALTER TABLE folio_items DROP CONSTRAINT folio_items_source_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_source_ck CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION', 'TRANSFER'));

-- +goose Down
-- The old constraint cannot hold a transferred item, and the ledger is append-only: the way down is refused while one exists.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM folio_items WHERE source = 'TRANSFER') THEN
        RAISE EXCEPTION 'cannot roll back: transferred folio items exist (the old schema has no source TRANSFER)';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE folio_items DROP CONSTRAINT folio_items_source_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_source_ck CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION'));
