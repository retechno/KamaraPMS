-- +goose Up
-- Tax invoices (faktur pajak) of a PKP property (design: docs/architecture/09-pkp-input-vat.md, step 4). One live invoice per source (a
-- city ledger invoice or a closed folio); the seller and the buyer are copied onto it when it is issued; its lines are the VAT components
-- of the folios behind the source. The official number is given by the tax authority when the invoice is uploaded and recorded afterwards.
-- The VAT return does not read these invoices: it keeps reading the VAT collected from the folios.

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'TAX_INVOICE', 'TXI' FROM properties;

CREATE TABLE tax_invoices (
    id                      bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id               bigint        NOT NULL,
    property_id             bigint        NOT NULL,
    invoice_ref             varchar(20)   NOT NULL,
    status                  varchar(10)   NOT NULL DEFAULT 'ISSUED',
    issue_date              date          NOT NULL,
    source_type             varchar(25)   NOT NULL,
    city_ledger_invoice_id  bigint,
    folio_id                bigint,
    seller_name             varchar(150)  NOT NULL,
    seller_npwp             varchar(30)   NOT NULL,
    seller_pkp_number       varchar(40),
    seller_address          varchar(400),
    signer_name             varchar(150),
    signer_title            varchar(100),
    buyer_name              varchar(150)  NOT NULL,
    buyer_npwp              varchar(16)   NOT NULL,
    buyer_address           varchar(400),
    taxable_base            numeric(18,3) NOT NULL,
    vat_amount              numeric(18,3) NOT NULL,
    djp_number              varchar(40),
    replaces_invoice_id     bigint,
    voided_at               timestamptz,
    voided_by               bigint REFERENCES users (id),
    void_reason             varchar(500),
    approved_by             bigint REFERENCES users (id),
    idempotency_key         varchar(100),
    created_at              timestamptz   NOT NULL DEFAULT now(),
    created_by              bigint REFERENCES users (id),
    CONSTRAINT tax_invoices_property_fk     FOREIGN KEY (tenant_id, property_id)                REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_invoices_cli_fk          FOREIGN KEY (property_id, city_ledger_invoice_id)   REFERENCES city_ledger_invoices (property_id, id),
    CONSTRAINT tax_invoices_folio_fk        FOREIGN KEY (property_id, folio_id)                 REFERENCES folios (property_id, id),
    CONSTRAINT tax_invoices_replaces_fk     FOREIGN KEY (property_id, replaces_invoice_id)      REFERENCES tax_invoices (property_id, id),
    CONSTRAINT tax_invoices_ref_uk          UNIQUE (property_id, invoice_ref),
    CONSTRAINT tax_invoices_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT tax_invoices_status_ck       CHECK (status IN ('ISSUED', 'VOIDED')),
    CONSTRAINT tax_invoices_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT tax_invoices_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL),
    CONSTRAINT tax_invoices_source_ck       CHECK ((source_type = 'CITY_LEDGER_INVOICE' AND city_ledger_invoice_id IS NOT NULL AND folio_id IS NULL)
                                                OR (source_type = 'FOLIO' AND folio_id IS NOT NULL AND city_ledger_invoice_id IS NULL)),
    CONSTRAINT tax_invoices_amounts_ck      CHECK (taxable_base > 0 AND vat_amount > 0),
    CONSTRAINT tax_invoices_buyer_npwp_ck   CHECK (buyer_npwp ~ '^[0-9]{15,16}$'),
    CONSTRAINT tax_invoices_replaces_ck     CHECK (replaces_invoice_id IS NULL OR replaces_invoice_id <> id)
);
-- A source has one live invoice.
CREATE UNIQUE INDEX tax_invoices_cli_live_uk   ON tax_invoices (property_id, city_ledger_invoice_id) WHERE status = 'ISSUED' AND city_ledger_invoice_id IS NOT NULL;
CREATE UNIQUE INDEX tax_invoices_folio_live_uk ON tax_invoices (property_id, folio_id) WHERE status = 'ISSUED' AND folio_id IS NOT NULL;
-- The official number is recorded once and belongs to one invoice.
CREATE UNIQUE INDEX tax_invoices_djp_uk         ON tax_invoices (property_id, djp_number) WHERE djp_number IS NOT NULL;
CREATE UNIQUE INDEX tax_invoices_idempotency_uk ON tax_invoices (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX tax_invoices_replaces_uk    ON tax_invoices (property_id, replaces_invoice_id) WHERE replaces_invoice_id IS NOT NULL;
CREATE INDEX tax_invoices_date_idx ON tax_invoices (property_id, issue_date);

CREATE TABLE tax_invoice_lines (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    invoice_id    bigint        NOT NULL,
    line_no       int           NOT NULL,
    charge_code   varchar(20)   NOT NULL,
    description   varchar(100)  NOT NULL,
    base_amount   numeric(18,3) NOT NULL,
    rate          numeric(7,4)  NOT NULL,
    vat_amount    numeric(18,3) NOT NULL,
    CONSTRAINT tax_invoice_lines_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_invoice_lines_invoice_fk  FOREIGN KEY (property_id, invoice_id)  REFERENCES tax_invoices (property_id, id),
    CONSTRAINT tax_invoice_lines_no_uk       UNIQUE (invoice_id, line_no),
    CONSTRAINT tax_invoice_lines_amounts_ck  CHECK (base_amount >= 0 AND vat_amount >= 0)
);

CREATE TABLE tax_invoice_exports (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    format         varchar(20) NOT NULL,
    period_start   date        NOT NULL,
    period_end     date        NOT NULL,
    invoice_count  int         NOT NULL,
    file_name      varchar(120) NOT NULL,
    sha256         char(64)    NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    created_by     bigint REFERENCES users (id),
    CONSTRAINT tax_invoice_exports_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_invoice_exports_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT tax_invoice_exports_period_ck      CHECK (period_end >= period_start),
    CONSTRAINT tax_invoice_exports_format_ck      CHECK (format IN ('CSV'))
);
CREATE TABLE tax_invoice_export_items (
    tenant_id    bigint NOT NULL,
    property_id  bigint NOT NULL,
    export_id    bigint NOT NULL,
    invoice_id   bigint NOT NULL,
    PRIMARY KEY (export_id, invoice_id),
    CONSTRAINT tax_invoice_export_items_export_fk  FOREIGN KEY (property_id, export_id)  REFERENCES tax_invoice_exports (property_id, id),
    CONSTRAINT tax_invoice_export_items_invoice_fk FOREIGN KEY (property_id, invoice_id) REFERENCES tax_invoices (property_id, id)
);

-- A tax invoice is ISSUED and then VOIDED, never deleted; the official number is the only other thing written, once. Lines and exports are append-only.
-- +goose StatementBegin
CREATE FUNCTION tax_invoices_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tax invoices cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_invoices_no_delete';
    END IF;
    IF OLD.status = 'ISSUED' AND NEW.status = 'VOIDED'
       AND (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) = (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) THEN
        RETURN NEW;
    END IF;
    IF OLD.status = 'ISSUED' AND NEW.status = 'ISSUED' AND OLD.djp_number IS NULL AND NEW.djp_number IS NOT NULL
       AND (to_jsonb(NEW) - 'djp_number') = (to_jsonb(OLD) - 'djp_number') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'tax invoice % may only be voided, or get its official number once', OLD.id
        USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_invoices_void_only';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_invoices_guard BEFORE UPDATE OR DELETE ON tax_invoices FOR EACH ROW EXECUTE FUNCTION tax_invoices_guard();
CREATE TRIGGER tax_invoices_no_truncate BEFORE TRUNCATE ON tax_invoices FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER tax_invoice_lines_append_only BEFORE UPDATE OR DELETE ON tax_invoice_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER tax_invoice_lines_no_truncate BEFORE TRUNCATE ON tax_invoice_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER tax_invoice_exports_append_only BEFORE UPDATE OR DELETE ON tax_invoice_exports FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER tax_invoice_export_items_append_only BEFORE UPDATE OR DELETE ON tax_invoice_export_items FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- Deferred check at COMMIT: the lines of an invoice add up to its taxable base and its VAT.
-- +goose StatementBegin
CREATE FUNCTION tax_invoice_totals_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_id    bigint;
    v_inv   record;
    v_base  numeric;
    v_vat   numeric;
BEGIN
    IF TG_TABLE_NAME = 'tax_invoices' THEN
        v_id := NEW.id;
    ELSE
        v_id := NEW.invoice_id;
    END IF;
    SELECT taxable_base, vat_amount INTO v_inv FROM tax_invoices WHERE id = v_id;
    SELECT COALESCE(sum(base_amount), 0), COALESCE(sum(vat_amount), 0) INTO v_base, v_vat FROM tax_invoice_lines WHERE invoice_id = v_id;
    IF v_inv.taxable_base <> v_base OR v_inv.vat_amount <> v_vat THEN
        RAISE EXCEPTION 'tax invoice % has lines of % and % for % and %', v_id, v_base, v_vat, v_inv.taxable_base, v_inv.vat_amount
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_invoices_lines_match_totals';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER tax_invoices_totals_check AFTER INSERT ON tax_invoices
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION tax_invoice_totals_check();
CREATE CONSTRAINT TRIGGER tax_invoice_lines_totals_check AFTER INSERT ON tax_invoice_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION tax_invoice_totals_check();

-- A city ledger invoice with a live tax invoice cannot be voided before it.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_invoices_tax_invoice_check() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'ISSUED' AND NEW.status = 'VOIDED'
       AND EXISTS (SELECT 1 FROM tax_invoices t WHERE t.property_id = OLD.property_id AND t.city_ledger_invoice_id = OLD.id AND t.status = 'ISSUED') THEN
        RAISE EXCEPTION 'city ledger invoice % has a tax invoice: void it first', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cli_has_tax_invoice';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_invoices_tax_invoice_check BEFORE UPDATE ON city_ledger_invoices
    FOR EACH ROW EXECUTE FUNCTION city_ledger_invoices_tax_invoice_check();

-- +goose Down
DROP TRIGGER IF EXISTS city_ledger_invoices_tax_invoice_check ON city_ledger_invoices;
DROP FUNCTION IF EXISTS city_ledger_invoices_tax_invoice_check();
DROP TABLE IF EXISTS tax_invoice_export_items;
DROP TABLE IF EXISTS tax_invoice_exports;
DROP TABLE IF EXISTS tax_invoice_lines;
DROP TABLE IF EXISTS tax_invoices;
DROP FUNCTION IF EXISTS tax_invoice_totals_check();
DROP FUNCTION IF EXISTS tax_invoices_guard();
DELETE FROM document_sequences WHERE sequence_type = 'TAX_INVOICE';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT'));
