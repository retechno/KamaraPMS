-- +goose Up
-- An invoice to a company: it groups transfers (CITY_LEDGER payments) of folios whose guests have checked out.
-- The amount a company owes stays derived (transfers minus receipts); an invoice is the billing document.
CREATE TABLE city_ledger_invoices (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    invoice_number  varchar(20)   NOT NULL,
    company_id      bigint        NOT NULL,
    invoice_date    date          NOT NULL,
    due_date        date          NOT NULL,
    total           numeric(18,3) NOT NULL,
    notes           varchar(500),
    status          varchar(10)   NOT NULL DEFAULT 'ISSUED',
    voided_at       timestamptz,
    voided_by       bigint REFERENCES users (id),
    void_reason     varchar(500),
    approved_by     bigint REFERENCES users (id),
    idempotency_key varchar(100),
    created_at      timestamptz   NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    CONSTRAINT cli_property_fk     FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT cli_company_fk      FOREIGN KEY (property_id, company_id) REFERENCES companies (property_id, id),
    CONSTRAINT cli_business_day_fk FOREIGN KEY (property_id, invoice_date) REFERENCES business_days (property_id, business_date),
    CONSTRAINT cli_number_uk       UNIQUE (property_id, invoice_number),
    CONSTRAINT cli_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT cli_total_ck        CHECK (total > 0),
    CONSTRAINT cli_due_ck          CHECK (due_date >= invoice_date),
    CONSTRAINT cli_status_ck       CHECK (status IN ('ISSUED', 'VOIDED')),
    CONSTRAINT cli_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT cli_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX cli_idempotency_uk ON city_ledger_invoices (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX cli_company_idx ON city_ledger_invoices (property_id, company_id, invoice_date);

CREATE TABLE city_ledger_invoice_lines (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    invoice_id  bigint        NOT NULL,
    payment_id  bigint        NOT NULL,
    amount      numeric(18,3) NOT NULL,
    released_at timestamptz,
    CONSTRAINT clil_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT clil_invoice_fk  FOREIGN KEY (property_id, invoice_id) REFERENCES city_ledger_invoices (property_id, id),
    CONSTRAINT clil_payment_fk  FOREIGN KEY (property_id, payment_id) REFERENCES payments (property_id, id),
    CONSTRAINT clil_amount_ck   CHECK (amount > 0)
);
-- A transfer is on at most one live invoice; voiding an invoice releases its lines.
CREATE UNIQUE INDEX clil_payment_live_uk ON city_ledger_invoice_lines (payment_id) WHERE released_at IS NULL;
CREATE INDEX clil_invoice_idx ON city_ledger_invoice_lines (property_id, invoice_id);

-- Invoices: ISSUED -> VOIDED only, never deleted. Lines: only released_at may be set (once), never deleted.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_invoices_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'city ledger invoices cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cli_no_delete';
    END IF;
    IF OLD.status <> 'ISSUED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) THEN
        RAISE EXCEPTION 'city ledger invoice % may only change from ISSUED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cli_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_invoices_guard BEFORE UPDATE OR DELETE ON city_ledger_invoices
    FOR EACH ROW EXECUTE FUNCTION city_ledger_invoices_guard();

-- +goose StatementBegin
CREATE FUNCTION city_ledger_invoice_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'city ledger invoice lines cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clil_no_delete';
    END IF;
    IF OLD.released_at IS NOT NULL OR NEW.released_at IS NULL
       OR (to_jsonb(NEW) - 'released_at') <> (to_jsonb(OLD) - 'released_at') THEN
        RAISE EXCEPTION 'city ledger invoice line % may only be released', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clil_release_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_invoice_lines_guard BEFORE UPDATE OR DELETE ON city_ledger_invoice_lines
    FOR EACH ROW EXECUTE FUNCTION city_ledger_invoice_lines_guard();
CREATE TRIGGER city_ledger_invoices_no_truncate BEFORE TRUNCATE ON city_ledger_invoices
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER city_ledger_invoice_lines_no_truncate BEFORE TRUNCATE ON city_ledger_invoice_lines
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'CITY_LEDGER_INVOICE', 'CINV' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type = 'CITY_LEDGER_INVOICE';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT'));
DROP TABLE IF EXISTS city_ledger_invoice_lines;
DROP TABLE IF EXISTS city_ledger_invoices;
DROP FUNCTION IF EXISTS city_ledger_invoice_lines_guard();
DROP FUNCTION IF EXISTS city_ledger_invoices_guard();
