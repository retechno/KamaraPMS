-- +goose Up
-- Tax filing: the tax a hotel collects from its guests (the hotel tax, PB1, or VAT) is reported to the tax authority
-- each month and paid over. A return is the month's worksheet frozen when it is filed (taxable base and tax collected,
-- by charge code and rate, from the tax snapshots of the folio items); a tax payment settles it and posts a journal that
-- debits the tax payable account of the books. What is owed is derived: the returns not voided less the payments not
-- voided.

ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX'));

-- How a tax is filed: the authority, the registration number of the hotel with it and the day of the next month the
-- return and the payment are due.
CREATE TABLE tax_filing_profiles (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    tax_id               bigint       NOT NULL,
    authority            varchar(150) NOT NULL,
    registration_number  varchar(60),
    due_day              smallint     NOT NULL DEFAULT 15,
    is_active            boolean      NOT NULL DEFAULT true,
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT tax_filing_profiles_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_filing_profiles_tax_fk         FOREIGN KEY (property_id, tax_id)    REFERENCES taxes (property_id, id),
    CONSTRAINT tax_filing_profiles_tax_uk         UNIQUE (property_id, tax_id),
    CONSTRAINT tax_filing_profiles_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT tax_filing_profiles_due_day_ck     CHECK (due_day BETWEEN 1 AND 28)
);
CREATE TRIGGER tax_filing_profiles_set_updated_at BEFORE UPDATE ON tax_filing_profiles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE tax_returns (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    return_number     varchar(20)   NOT NULL,
    tax_id            bigint        NOT NULL,
    period_start      date          NOT NULL,
    period_end        date          NOT NULL,
    due_date          date          NOT NULL,
    base_amount       numeric(18,3) NOT NULL,
    tax_amount        numeric(18,3) NOT NULL,
    status            varchar(10)   NOT NULL DEFAULT 'FILED',
    filed_on          date          NOT NULL,
    filing_reference  varchar(100),
    notes             varchar(500),
    filed_at          timestamptz   NOT NULL DEFAULT now(),
    filed_by          bigint REFERENCES users (id),
    voided_at         timestamptz,
    voided_by         bigint REFERENCES users (id),
    void_reason       varchar(500),
    approved_by       bigint REFERENCES users (id),
    idempotency_key   varchar(100),
    CONSTRAINT tax_returns_property_fk     FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_returns_tax_fk          FOREIGN KEY (property_id, tax_id)    REFERENCES taxes (property_id, id),
    CONSTRAINT tax_returns_number_uk       UNIQUE (property_id, return_number),
    CONSTRAINT tax_returns_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT tax_returns_period_ck       CHECK (EXTRACT(day FROM period_start) = 1 AND period_end >= period_start),
    CONSTRAINT tax_returns_amounts_ck      CHECK (tax_amount >= 0),
    CONSTRAINT tax_returns_status_ck       CHECK (status IN ('FILED', 'VOIDED')),
    CONSTRAINT tax_returns_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT tax_returns_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
-- A month of a tax is filed once (a voided return may be filed again).
CREATE UNIQUE INDEX tax_returns_period_uk      ON tax_returns (property_id, tax_id, period_start) WHERE status = 'FILED';
CREATE UNIQUE INDEX tax_returns_idempotency_uk ON tax_returns (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- The worksheet of the return as it was when it was filed.
CREATE TABLE tax_return_lines (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    return_id    bigint        NOT NULL,
    line_no      int           NOT NULL,
    charge_code  varchar(20)   NOT NULL,
    charge_name  varchar(100),
    rate         numeric(7,4)  NOT NULL,
    items        int           NOT NULL,
    base_amount  numeric(18,3) NOT NULL,
    tax_amount   numeric(18,3) NOT NULL,
    CONSTRAINT tax_return_lines_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_return_lines_return_fk   FOREIGN KEY (property_id, return_id) REFERENCES tax_returns (property_id, id),
    CONSTRAINT tax_return_lines_no_uk       UNIQUE (return_id, line_no)
);

CREATE TABLE tax_payments (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    payment_number      varchar(20)   NOT NULL,
    return_id           bigint        NOT NULL,
    payment_date        date          NOT NULL,
    amount              numeric(18,3) NOT NULL,
    penalty             numeric(18,3) NOT NULL DEFAULT 0,
    payment_method      varchar(15)   NOT NULL,
    reference_number    varchar(100),
    remarks             varchar(500),
    status              varchar(10)   NOT NULL DEFAULT 'POSTED',
    journal_id          bigint        NOT NULL,
    void_journal_id     bigint,
    voided_at           timestamptz,
    voided_by           bigint REFERENCES users (id),
    void_reason         varchar(500),
    approved_by         bigint REFERENCES users (id),
    idempotency_key     varchar(100),
    created_at          timestamptz   NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    CONSTRAINT tax_payments_property_fk     FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT tax_payments_return_fk       FOREIGN KEY (property_id, return_id)       REFERENCES tax_returns (property_id, id),
    CONSTRAINT tax_payments_journal_fk      FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT tax_payments_void_journal_fk FOREIGN KEY (property_id, void_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT tax_payments_number_uk       UNIQUE (property_id, payment_number),
    CONSTRAINT tax_payments_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT tax_payments_amount_ck       CHECK (amount > 0 AND penalty >= 0),
    CONSTRAINT tax_payments_method_ck       CHECK (payment_method IN ('CASH', 'BANK_TRANSFER', 'OTHER')),
    CONSTRAINT tax_payments_status_ck       CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT tax_payments_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT tax_payments_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX tax_payments_idempotency_uk ON tax_payments (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX tax_payments_return_idx ON tax_payments (property_id, return_id);

-- Returns and payments are never deleted; they only go from the live state to VOIDED. Worksheet lines never change.
-- +goose StatementBegin
CREATE FUNCTION tax_returns_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tax returns cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_returns_no_delete';
    END IF;
    IF OLD.status <> 'FILED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) THEN
        RAISE EXCEPTION 'tax return % may only change from FILED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_returns_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION tax_payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'tax payments cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_payments_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by']) THEN
        RAISE EXCEPTION 'tax payment % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'tax_payments_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER tax_returns_guard BEFORE UPDATE OR DELETE ON tax_returns FOR EACH ROW EXECUTE FUNCTION tax_returns_guard();
CREATE TRIGGER tax_payments_guard BEFORE UPDATE OR DELETE ON tax_payments FOR EACH ROW EXECUTE FUNCTION tax_payments_guard();
CREATE TRIGGER tax_return_lines_append_only BEFORE UPDATE OR DELETE ON tax_return_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER tax_returns_no_truncate BEFORE TRUNCATE ON tax_returns FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER tax_return_lines_no_truncate BEFORE TRUNCATE ON tax_return_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER tax_payments_no_truncate BEFORE TRUNCATE ON tax_payments FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Deferred check at COMMIT: the lines of a return add up to its base and its tax.
-- +goose StatementBegin
CREATE FUNCTION tax_return_totals_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_id    bigint;
    v_ret   record;
    v_base  numeric;
    v_tax   numeric;
BEGIN
    IF TG_TABLE_NAME = 'tax_returns' THEN
        v_id := NEW.id;
    ELSE
        v_id := NEW.return_id;
    END IF;
    SELECT base_amount, tax_amount INTO v_ret FROM tax_returns WHERE id = v_id;
    SELECT COALESCE(sum(base_amount), 0), COALESCE(sum(tax_amount), 0) INTO v_base, v_tax FROM tax_return_lines WHERE return_id = v_id;
    IF v_ret.base_amount <> v_base OR v_ret.tax_amount <> v_tax THEN
        RAISE EXCEPTION 'tax return % has lines of % and % for % and %', v_id, v_base, v_tax, v_ret.base_amount, v_ret.tax_amount
            USING ERRCODE = 'check_violation', CONSTRAINT = 'tax_returns_lines_match_totals';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER tax_returns_totals_check AFTER INSERT ON tax_returns
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION tax_return_totals_check();
CREATE CONSTRAINT TRIGGER tax_return_lines_totals_check AFTER INSERT ON tax_return_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION tax_return_totals_check();

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'TAX_RETURN', 'TXR' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'TAX_PAYMENT', 'TXP' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type IN ('TAX_RETURN', 'TAX_PAYMENT');
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT'));
DROP TABLE IF EXISTS tax_payments;
DROP TABLE IF EXISTS tax_return_lines;
DROP TABLE IF EXISTS tax_returns;
DROP TABLE IF EXISTS tax_filing_profiles;
DROP FUNCTION IF EXISTS tax_return_totals_check();
DROP FUNCTION IF EXISTS tax_payments_guard();
DROP FUNCTION IF EXISTS tax_returns_guard();
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK'));
