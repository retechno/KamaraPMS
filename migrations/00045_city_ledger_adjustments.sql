-- +goose Up
-- Credit notes and write-offs of the city ledger (design: docs/architecture/10-credit-notes-writeoffs.md). Both lower what a company owes
-- and are journaled when they are made (Dr an allowance or the bad debt expense, Cr CITY_LEDGER); neither rewrites the folio or the
-- invoice. What a company owes stays derived: transfers less receipts less the adjustments that are POSTED.
--   A credit note is made against an issued invoice, or against a transfer that is not on an invoice yet; the latter is attached to the
--   invoice that is made from that transfer (the invoice then asks the net amount).
--   A write-off is made against what an issued invoice still owes.

ALTER TABLE gl_journals ALTER COLUMN journal_type TYPE varchar(20);
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX', 'RECEIVABLES'));

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'CREDIT_NOTE', 'CN' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'WRITE_OFF', 'WO' FROM properties;

CREATE TABLE city_ledger_adjustments (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    adjustment_number varchar(20)   NOT NULL,
    kind              varchar(12)   NOT NULL,
    company_id        bigint        NOT NULL,
    invoice_id        bigint,
    payment_id        bigint,
    amount            numeric(18,3) NOT NULL,
    business_date     date          NOT NULL,
    reason            varchar(500)  NOT NULL,
    debit_account_id  bigint,
    status            varchar(10)   NOT NULL DEFAULT 'POSTED',
    journal_id        bigint        NOT NULL,
    void_journal_id   bigint,
    voided_at         timestamptz,
    voided_by         bigint REFERENCES users (id),
    void_reason       varchar(500),
    approved_by       bigint REFERENCES users (id),
    idempotency_key   varchar(100),
    created_at        timestamptz   NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    CONSTRAINT cla_property_fk      FOREIGN KEY (tenant_id, property_id)                REFERENCES properties (tenant_id, id),
    CONSTRAINT cla_company_fk       FOREIGN KEY (property_id, company_id)               REFERENCES companies (property_id, id),
    CONSTRAINT cla_invoice_fk       FOREIGN KEY (property_id, invoice_id, company_id)   REFERENCES city_ledger_invoices (property_id, id, company_id),
    CONSTRAINT cla_payment_fk       FOREIGN KEY (property_id, payment_id)               REFERENCES payments (property_id, id),
    CONSTRAINT cla_account_fk       FOREIGN KEY (property_id, debit_account_id)         REFERENCES gl_accounts (property_id, id),
    CONSTRAINT cla_journal_fk       FOREIGN KEY (property_id, journal_id)               REFERENCES gl_journals (property_id, id),
    CONSTRAINT cla_void_journal_fk  FOREIGN KEY (property_id, void_journal_id)          REFERENCES gl_journals (property_id, id),
    CONSTRAINT cla_business_day_fk  FOREIGN KEY (property_id, business_date)            REFERENCES business_days (property_id, business_date),
    CONSTRAINT cla_number_uk        UNIQUE (property_id, adjustment_number),
    CONSTRAINT cla_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT cla_kind_ck          CHECK (kind IN ('CREDIT_NOTE', 'WRITE_OFF')),
    -- a credit note is against an invoice or a transfer; a write-off against an invoice and says the account that bears it
    CONSTRAINT cla_target_ck        CHECK ((kind = 'CREDIT_NOTE' AND ((invoice_id IS NOT NULL) <> (payment_id IS NOT NULL)) AND debit_account_id IS NULL)
                                        OR (kind = 'WRITE_OFF' AND invoice_id IS NOT NULL AND payment_id IS NULL AND debit_account_id IS NOT NULL)),
    CONSTRAINT cla_amount_ck        CHECK (amount > 0),
    CONSTRAINT cla_status_ck        CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT cla_voided_ck        CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT cla_void_reason_ck   CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX cla_idempotency_uk ON city_ledger_adjustments (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX cla_company_idx ON city_ledger_adjustments (property_id, company_id, business_date);
CREATE INDEX cla_invoice_idx ON city_ledger_adjustments (property_id, invoice_id) WHERE invoice_id IS NOT NULL;
CREATE INDEX cla_payment_idx ON city_ledger_adjustments (property_id, payment_id) WHERE payment_id IS NOT NULL;

-- The lines of a credit note: the revenue account of the allowance, the net amount and the tax on it.
CREATE TABLE city_ledger_credit_note_lines (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    adjustment_id  bigint        NOT NULL,
    line_no        int           NOT NULL,
    description    varchar(200)  NOT NULL,
    account_id     bigint        NOT NULL,
    net_amount     numeric(18,3) NOT NULL,
    tax_id         bigint,
    tax_rate       numeric(7,4),
    tax_amount     numeric(18,3) NOT NULL DEFAULT 0,
    CONSTRAINT clcnl_property_fk   FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT clcnl_adjustment_fk FOREIGN KEY (property_id, adjustment_id)   REFERENCES city_ledger_adjustments (property_id, id),
    CONSTRAINT clcnl_account_fk    FOREIGN KEY (property_id, account_id)      REFERENCES gl_accounts (property_id, id),
    CONSTRAINT clcnl_tax_fk        FOREIGN KEY (property_id, tax_id)          REFERENCES taxes (property_id, id),
    CONSTRAINT clcnl_no_uk         UNIQUE (adjustment_id, line_no),
    CONSTRAINT clcnl_amounts_ck    CHECK (net_amount > 0 AND tax_amount >= 0),
    CONSTRAINT clcnl_tax_ck        CHECK ((tax_id IS NULL) = (tax_rate IS NULL) AND (tax_id IS NOT NULL OR tax_amount = 0))
);

-- The credit note of a transfer that is on an invoice now: the invoice was made from the transfer and asks its net amount. Released if
-- the invoice is voided, which sets the note free on the transfer again.
CREATE TABLE city_ledger_adjustment_invoices (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    adjustment_id  bigint      NOT NULL,
    invoice_id     bigint      NOT NULL,
    released_at    timestamptz,
    CONSTRAINT claai_property_fk   FOREIGN KEY (tenant_id, property_id)     REFERENCES properties (tenant_id, id),
    CONSTRAINT claai_adjustment_fk FOREIGN KEY (property_id, adjustment_id) REFERENCES city_ledger_adjustments (property_id, id),
    CONSTRAINT claai_invoice_fk    FOREIGN KEY (property_id, invoice_id)    REFERENCES city_ledger_invoices (property_id, id)
);
CREATE UNIQUE INDEX claai_live_uk ON city_ledger_adjustment_invoices (adjustment_id) WHERE released_at IS NULL;
CREATE INDEX claai_invoice_idx ON city_ledger_adjustment_invoices (property_id, invoice_id);

-- An adjustment is POSTED and then VOIDED, never deleted. Lines are written once. A link only gets its release, once.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_adjustments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'city ledger adjustments cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cla_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by']) THEN
        RAISE EXCEPTION 'city ledger adjustment % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cla_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_adjustments_guard BEFORE UPDATE OR DELETE ON city_ledger_adjustments FOR EACH ROW EXECUTE FUNCTION city_ledger_adjustments_guard();
CREATE TRIGGER city_ledger_adjustments_no_truncate BEFORE TRUNCATE ON city_ledger_adjustments FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER city_ledger_credit_note_lines_append_only BEFORE UPDATE OR DELETE ON city_ledger_credit_note_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER city_ledger_credit_note_lines_no_truncate BEFORE TRUNCATE ON city_ledger_credit_note_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
-- +goose StatementBegin
CREATE FUNCTION city_ledger_adjustment_invoices_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'city ledger adjustment links cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'claai_no_delete';
    END IF;
    IF OLD.released_at IS NOT NULL OR NEW.released_at IS NULL OR (to_jsonb(NEW) - 'released_at') <> (to_jsonb(OLD) - 'released_at') THEN
        RAISE EXCEPTION 'city ledger adjustment link % may only be released', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'claai_release_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_adjustment_invoices_guard BEFORE UPDATE OR DELETE ON city_ledger_adjustment_invoices FOR EACH ROW EXECUTE FUNCTION city_ledger_adjustment_invoices_guard();
CREATE TRIGGER city_ledger_adjustment_invoices_no_truncate BEFORE TRUNCATE ON city_ledger_adjustment_invoices FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Deferred check at COMMIT: the lines of a credit note (net and tax) add up to its amount; a write-off has no lines.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_adjustment_totals_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_id    bigint;
    v_adj   record;
    v_sum   numeric;
    v_lines int;
BEGIN
    IF TG_TABLE_NAME = 'city_ledger_adjustments' THEN
        v_id := NEW.id;
    ELSE
        v_id := NEW.adjustment_id;
    END IF;
    SELECT kind, amount INTO v_adj FROM city_ledger_adjustments WHERE id = v_id;
    SELECT COALESCE(sum(net_amount + tax_amount), 0), count(*) INTO v_sum, v_lines FROM city_ledger_credit_note_lines WHERE adjustment_id = v_id;
    IF (v_adj.kind = 'CREDIT_NOTE' AND v_sum <> v_adj.amount) OR (v_adj.kind = 'WRITE_OFF' AND v_lines > 0) THEN
        RAISE EXCEPTION 'city ledger adjustment % has lines of % for an amount of %', v_id, v_sum, v_adj.amount
            USING ERRCODE = 'check_violation', CONSTRAINT = 'cla_lines_match_amount';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER city_ledger_adjustments_totals_check AFTER INSERT ON city_ledger_adjustments
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION city_ledger_adjustment_totals_check();
CREATE CONSTRAINT TRIGGER city_ledger_credit_note_lines_totals_check AFTER INSERT ON city_ledger_credit_note_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION city_ledger_adjustment_totals_check();

-- An invoice with a credit note or a write-off against it that is posted cannot be voided; void them first.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_invoices_adjustment_check() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'ISSUED' AND NEW.status = 'VOIDED'
       AND EXISTS (SELECT 1 FROM city_ledger_adjustments a WHERE a.property_id = OLD.property_id AND a.invoice_id = OLD.id AND a.status = 'POSTED') THEN
        RAISE EXCEPTION 'city ledger invoice % has credit notes or write-offs: void them first', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cli_has_adjustments';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_invoices_adjustment_check BEFORE UPDATE ON city_ledger_invoices
    FOR EACH ROW EXECUTE FUNCTION city_ledger_invoices_adjustment_check();

-- +goose Down
DROP TRIGGER IF EXISTS city_ledger_invoices_adjustment_check ON city_ledger_invoices;
DROP FUNCTION IF EXISTS city_ledger_invoices_adjustment_check();
DROP TABLE IF EXISTS city_ledger_adjustment_invoices;
DROP TABLE IF EXISTS city_ledger_credit_note_lines;
DROP TABLE IF EXISTS city_ledger_adjustments;
DROP FUNCTION IF EXISTS city_ledger_adjustment_totals_check();
DROP FUNCTION IF EXISTS city_ledger_adjustment_invoices_guard();
DROP FUNCTION IF EXISTS city_ledger_adjustments_guard();
DELETE FROM document_sequences WHERE sequence_type IN ('CREDIT_NOTE', 'WRITE_OFF');
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE'));
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX'));
