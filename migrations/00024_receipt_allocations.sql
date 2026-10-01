-- +goose Up
-- A receipt can pay invoices: it is allocated to one or more invoices of the same company. The part of a receipt that
-- is not allocated stays "on account". What an invoice has been paid is derived: the allocations of POSTED receipts.
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_property_id_company_uk UNIQUE (property_id, id, company_id);
ALTER TABLE city_ledger_invoices ADD CONSTRAINT cli_property_id_company_uk UNIQUE (property_id, id, company_id);

CREATE TABLE city_ledger_receipt_allocations (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    receipt_id  bigint        NOT NULL,
    invoice_id  bigint        NOT NULL,
    company_id  bigint        NOT NULL,
    amount      numeric(18,3) NOT NULL,
    created_at  timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT clra_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    -- both sides must belong to the same company: a receipt can only pay an invoice of its own company
    CONSTRAINT clra_receipt_fk  FOREIGN KEY (property_id, receipt_id, company_id) REFERENCES city_ledger_receipts (property_id, id, company_id),
    CONSTRAINT clra_invoice_fk  FOREIGN KEY (property_id, invoice_id, company_id) REFERENCES city_ledger_invoices (property_id, id, company_id),
    CONSTRAINT clra_pair_uk     UNIQUE (receipt_id, invoice_id),
    CONSTRAINT clra_amount_ck   CHECK (amount > 0)
);
CREATE INDEX clra_invoice_idx ON city_ledger_receipt_allocations (property_id, invoice_id);
CREATE INDEX clra_receipt_idx ON city_ledger_receipt_allocations (property_id, receipt_id);

-- Allocations are written once with their receipt and never change. Voiding the receipt ends their effect.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_receipt_allocations_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'city ledger receipt allocations cannot be changed or deleted'
        USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clra_immutable';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_receipt_allocations_guard BEFORE UPDATE OR DELETE ON city_ledger_receipt_allocations
    FOR EACH ROW EXECUTE FUNCTION city_ledger_receipt_allocations_guard();
CREATE TRIGGER city_ledger_receipt_allocations_no_truncate BEFORE TRUNCATE ON city_ledger_receipt_allocations
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- +goose Down
DROP TABLE IF EXISTS city_ledger_receipt_allocations;
DROP FUNCTION IF EXISTS city_ledger_receipt_allocations_guard();
ALTER TABLE city_ledger_invoices DROP CONSTRAINT cli_property_id_company_uk;
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_property_id_company_uk;
