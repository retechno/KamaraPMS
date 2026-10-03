-- +goose Up
-- Overdue invoices of the city ledger, the reminders sent for them and the late fee shown on them (design: docs/architecture/10-credit-notes-writeoffs.md).
-- A reminder is a record that a letter of level 1, 2 or 3 went to a company, with the invoices it listed and what they owed that day frozen; it does not
-- touch the books. The late fee is a setting of the property (a percent per month after some days of grace) that the overdue list uses to show the interest
-- due; nothing is posted.

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'REMINDER', 'REM' FROM properties;

CREATE TABLE city_ledger_reminders (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    reminder_number   varchar(20)   NOT NULL,
    company_id        bigint        NOT NULL,
    level             smallint      NOT NULL,
    reminder_date     date          NOT NULL,
    note              varchar(500),
    total_outstanding numeric(18,3) NOT NULL,
    total_interest    numeric(18,3) NOT NULL DEFAULT 0,
    idempotency_key   varchar(100),
    created_at        timestamptz   NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    CONSTRAINT clrem_property_fk     FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT clrem_company_fk      FOREIGN KEY (property_id, company_id)     REFERENCES companies (property_id, id),
    CONSTRAINT clrem_business_day_fk FOREIGN KEY (property_id, reminder_date)  REFERENCES business_days (property_id, business_date),
    CONSTRAINT clrem_number_uk       UNIQUE (property_id, reminder_number),
    CONSTRAINT clrem_company_id_uk   UNIQUE (property_id, id, company_id),
    CONSTRAINT clrem_level_ck        CHECK (level BETWEEN 1 AND 3),
    CONSTRAINT clrem_amounts_ck      CHECK (total_outstanding > 0 AND total_interest >= 0)
);
CREATE UNIQUE INDEX clrem_idempotency_uk ON city_ledger_reminders (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX clrem_company_idx ON city_ledger_reminders (property_id, company_id, reminder_date);

-- The invoices a reminder listed, with what each owed that day.
CREATE TABLE city_ledger_reminder_items (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    reminder_id   bigint        NOT NULL,
    company_id    bigint        NOT NULL,
    invoice_id    bigint        NOT NULL,
    outstanding   numeric(18,3) NOT NULL,
    days_overdue  int           NOT NULL,
    interest      numeric(18,3) NOT NULL DEFAULT 0,
    CONSTRAINT clremi_property_fk FOREIGN KEY (tenant_id, property_id)               REFERENCES properties (tenant_id, id),
    CONSTRAINT clremi_reminder_fk FOREIGN KEY (property_id, reminder_id, company_id) REFERENCES city_ledger_reminders (property_id, id, company_id),
    CONSTRAINT clremi_invoice_fk  FOREIGN KEY (property_id, invoice_id, company_id)  REFERENCES city_ledger_invoices (property_id, id, company_id),
    CONSTRAINT clremi_pair_uk     UNIQUE (reminder_id, invoice_id),
    CONSTRAINT clremi_amounts_ck  CHECK (outstanding > 0 AND days_overdue >= 1 AND interest >= 0)
);
CREATE INDEX clremi_invoice_idx ON city_ledger_reminder_items (property_id, invoice_id);

CREATE TRIGGER city_ledger_reminders_append_only BEFORE UPDATE OR DELETE ON city_ledger_reminders FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER city_ledger_reminders_no_truncate BEFORE TRUNCATE ON city_ledger_reminders FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER city_ledger_reminder_items_append_only BEFORE UPDATE OR DELETE ON city_ledger_reminder_items FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER city_ledger_reminder_items_no_truncate BEFORE TRUNCATE ON city_ledger_reminder_items FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The late fee of a property: a percent per month after some days of grace; off at 0.
CREATE TABLE city_ledger_late_fee_settings (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint       NOT NULL,
    property_id   bigint       NOT NULL,
    monthly_rate  numeric(7,4) NOT NULL DEFAULT 0,
    grace_days    smallint     NOT NULL DEFAULT 0,
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    CONSTRAINT cllf_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT cllf_property_uk UNIQUE (property_id),
    CONSTRAINT cllf_rate_ck     CHECK (monthly_rate BETWEEN 0 AND 100),
    CONSTRAINT cllf_grace_ck    CHECK (grace_days BETWEEN 0 AND 365)
);
CREATE TRIGGER city_ledger_late_fee_settings_set_updated_at BEFORE UPDATE ON city_ledger_late_fee_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS city_ledger_late_fee_settings;
DROP TABLE IF EXISTS city_ledger_reminder_items;
DROP TABLE IF EXISTS city_ledger_reminders;
DELETE FROM document_sequences WHERE sequence_type = 'REMINDER';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF'));
