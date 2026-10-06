-- +goose Up
-- A stay may have several folios, one for each payer (design: docs/architecture/18-architecture-decisions.md, decision 1). Until now every stay had one GUEST folio. A COMPANY folio is a folio of
-- the stay that is billed to a company: it is a folio like the other, its balance is moved to the city ledger of that company by the transfer that exists already. Nothing creates a COMPANY
-- folio yet: this step only makes room for it, and every existing folio stays what it is (a GUEST folio with no payer). 'MASTER' is reserved in the design and not added here.
ALTER TABLE folios DROP CONSTRAINT folios_type_ck;
ALTER TABLE folios
    ADD COLUMN bill_to_company_id bigint,
    ADD CONSTRAINT folios_type_ck      CHECK (folio_type IN ('GUEST', 'COMPANY')),
    ADD CONSTRAINT folios_payer_ck     CHECK ((folio_type = 'COMPANY') = (bill_to_company_id IS NOT NULL)),
    ADD CONSTRAINT folios_company_fk   FOREIGN KEY (property_id, bill_to_company_id) REFERENCES companies (property_id, id);

-- One folio for each payer of a stay: the guest, and each company. The deposit folio (no stay yet) keeps its own index, folios_unlinked_open_uk.
DROP INDEX folios_stay_guest_uk;
CREATE UNIQUE INDEX folios_stay_payer_uk ON folios (stay_id, folio_type, COALESCE(bill_to_company_id, 0)) WHERE stay_id IS NOT NULL;
CREATE INDEX folios_company_idx ON folios (property_id, bill_to_company_id) WHERE bill_to_company_id IS NOT NULL;

-- +goose Down
-- The old schema cannot represent a company folio, and a folio holds items that are never deleted: it is refused while one exists.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM folios WHERE folio_type = 'COMPANY') THEN
        RAISE EXCEPTION 'cannot roll back: COMPANY folios exist (a stay with more than one folio cannot be represented by the old schema)';
    END IF;
END $$;
-- +goose StatementEnd
DROP INDEX folios_company_idx;
DROP INDEX folios_stay_payer_uk;
CREATE UNIQUE INDEX folios_stay_guest_uk ON folios (stay_id) WHERE folio_type = 'GUEST' AND stay_id IS NOT NULL;
ALTER TABLE folios
    DROP CONSTRAINT folios_company_fk,
    DROP CONSTRAINT folios_payer_ck,
    DROP CONSTRAINT folios_type_ck,
    DROP COLUMN bill_to_company_id,
    ADD CONSTRAINT folios_type_ck CHECK (folio_type IN ('GUEST'));
