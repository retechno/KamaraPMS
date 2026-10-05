-- +goose Up
-- Credit notes of suppliers (design: docs/architecture/14-supplier-credit-notes.md). A supplier takes back goods or gives a discount after the bill: the credit note is made against a bill, its lines credit
-- the lines of that bill (the account, the department and the way the VAT was treated are those of the bill line, frozen then), and its journal is the mirror of the bill (Dr payables, Cr the expense, Cr input VAT).
-- What it takes off the bill is an allocation of the credit to the bill (made when the credit note is, as far as the bill still owes); what is left is a credit of the supplier that later allocations apply to
-- other bills. Credit notes are never deleted; they go from POSTED to VOIDED, which gives the allocations back.
CREATE TABLE supplier_credit_notes (
    id                     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id              bigint        NOT NULL,
    property_id            bigint        NOT NULL,
    credit_number          varchar(20)   NOT NULL,
    supplier_id            bigint        NOT NULL,
    bill_id                bigint        NOT NULL,
    supplier_credit_number varchar(60)   NOT NULL,
    credit_date            date          NOT NULL,
    reason                 varchar(500)  NOT NULL,
    total                  numeric(18,3) NOT NULL,
    status                 varchar(10)   NOT NULL DEFAULT 'POSTED',
    journal_id             bigint        NOT NULL,
    void_journal_id        bigint,
    voided_at              timestamptz,
    voided_by              bigint REFERENCES users (id),
    void_reason            varchar(500),
    approved_by            bigint REFERENCES users (id),
    idempotency_key        varchar(100),
    created_at             timestamptz   NOT NULL DEFAULT now(),
    created_by             bigint REFERENCES users (id),
    CONSTRAINT scn_property_fk      FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT scn_supplier_fk      FOREIGN KEY (property_id, supplier_id)     REFERENCES suppliers (property_id, id),
    CONSTRAINT scn_bill_fk          FOREIGN KEY (property_id, supplier_id, bill_id) REFERENCES supplier_bills (property_id, supplier_id, id),
    CONSTRAINT scn_journal_fk       FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT scn_void_journal_fk  FOREIGN KEY (property_id, void_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT scn_number_uk        UNIQUE (property_id, credit_number),
    CONSTRAINT scn_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT scn_supplier_id_uk   UNIQUE (property_id, supplier_id, id),
    CONSTRAINT scn_total_ck         CHECK (total > 0),
    CONSTRAINT scn_status_ck        CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT scn_voided_ck        CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT scn_void_reason_ck   CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL),
    CONSTRAINT scn_reason_ck        CHECK (length(btrim(reason)) > 0)
);
CREATE UNIQUE INDEX scn_idempotency_uk ON supplier_credit_notes (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE UNIQUE INDEX scn_supplier_number_uk ON supplier_credit_notes (property_id, supplier_id, supplier_credit_number) WHERE status = 'POSTED';
CREATE INDEX scn_supplier_idx ON supplier_credit_notes (property_id, supplier_id, credit_date);
CREATE INDEX scn_bill_idx ON supplier_credit_notes (property_id, bill_id);

CREATE TABLE supplier_credit_note_lines (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint        NOT NULL,
    property_id    bigint        NOT NULL,
    credit_id      bigint        NOT NULL,
    line_no        int           NOT NULL,
    bill_id        bigint        NOT NULL,
    bill_line_no   int           NOT NULL,
    account_id     bigint        NOT NULL,
    description    varchar(300),
    amount         numeric(18,3) NOT NULL,
    vat_amount     numeric(18,3) NOT NULL DEFAULT 0,
    vat_treatment  varchar(10),
    department_id  bigint,
    CONSTRAINT scnl_property_fk     FOREIGN KEY (tenant_id, property_id)               REFERENCES properties (tenant_id, id),
    CONSTRAINT scnl_credit_fk       FOREIGN KEY (property_id, credit_id)               REFERENCES supplier_credit_notes (property_id, id),
    CONSTRAINT scnl_bill_line_fk    FOREIGN KEY (property_id, bill_id, bill_line_no)   REFERENCES supplier_bill_lines (property_id, bill_id, line_no),
    CONSTRAINT scnl_account_fk      FOREIGN KEY (property_id, account_id)              REFERENCES gl_accounts (property_id, id),
    CONSTRAINT scnl_department_fk   FOREIGN KEY (property_id, department_id)           REFERENCES departments (property_id, id),
    CONSTRAINT scnl_line_uk         UNIQUE (property_id, credit_id, line_no),
    CONSTRAINT scnl_amount_ck       CHECK (amount >= 0 AND vat_amount >= 0 AND amount + vat_amount > 0),
    CONSTRAINT scnl_treatment_ck    CHECK (vat_treatment IS NULL OR vat_treatment IN ('CREDITABLE', 'EXPENSE', 'DEFERRED')),
    CONSTRAINT scnl_vat_treatment_ck CHECK ((vat_amount = 0) = (vat_treatment IS NULL))
);
CREATE INDEX scnl_bill_line_idx ON supplier_credit_note_lines (property_id, bill_id, bill_line_no);

-- What of a credit has been taken off a bill: the bill it was made against when the credit note was made, and the bills of the same supplier it is applied to later.
CREATE TABLE supplier_credit_allocations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    supplier_id  bigint        NOT NULL,
    credit_id    bigint        NOT NULL,
    bill_id      bigint        NOT NULL,
    amount       numeric(18,3) NOT NULL,
    applied_on   date          NOT NULL,
    created_at   timestamptz   NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    CONSTRAINT sca_tenant_fk  FOREIGN KEY (tenant_id, property_id)               REFERENCES properties (tenant_id, id),
    CONSTRAINT sca_credit_fk  FOREIGN KEY (property_id, supplier_id, credit_id)  REFERENCES supplier_credit_notes (property_id, supplier_id, id),
    CONSTRAINT sca_bill_fk    FOREIGN KEY (property_id, supplier_id, bill_id)    REFERENCES supplier_bills (property_id, supplier_id, id),
    CONSTRAINT sca_amount_ck  CHECK (amount > 0)
);
CREATE INDEX sca_bill_idx ON supplier_credit_allocations (property_id, bill_id);
CREATE INDEX sca_credit_idx ON supplier_credit_allocations (property_id, credit_id);

-- Credit notes go from POSTED to VOIDED and nothing else changes; lines and allocations are never changed.
-- +goose StatementBegin
CREATE FUNCTION supplier_credit_notes_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'supplier credit notes cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'supplier_credit_notes_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by']) THEN
        RAISE EXCEPTION 'supplier credit note % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'supplier_credit_notes_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER supplier_credit_notes_guard BEFORE UPDATE OR DELETE ON supplier_credit_notes FOR EACH ROW EXECUTE FUNCTION supplier_credit_notes_guard();
CREATE TRIGGER supplier_credit_note_lines_append_only BEFORE UPDATE OR DELETE ON supplier_credit_note_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER supplier_credit_allocations_append_only BEFORE UPDATE OR DELETE ON supplier_credit_allocations FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER supplier_credit_notes_no_truncate BEFORE TRUNCATE ON supplier_credit_notes FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER supplier_credit_note_lines_no_truncate BEFORE TRUNCATE ON supplier_credit_note_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER supplier_credit_allocations_no_truncate BEFORE TRUNCATE ON supplier_credit_allocations FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Deferred check at COMMIT: the lines of a credit note add up to its total, and no more than the total is allocated.
-- +goose StatementBegin
CREATE FUNCTION supplier_credit_total_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_credit_id bigint;
    v_total     numeric;
    v_lines     numeric;
    v_allocated numeric;
BEGIN
    IF TG_TABLE_NAME = 'supplier_credit_notes' THEN
        v_credit_id := NEW.id;
    ELSE
        v_credit_id := NEW.credit_id;
    END IF;
    SELECT total INTO v_total FROM supplier_credit_notes WHERE id = v_credit_id;
    IF TG_TABLE_NAME <> 'supplier_credit_allocations' THEN
        SELECT COALESCE(sum(amount + vat_amount), 0) INTO v_lines FROM supplier_credit_note_lines WHERE credit_id = v_credit_id;
        IF v_total <> v_lines THEN
            RAISE EXCEPTION 'supplier credit note % has lines of % for a total of %', v_credit_id, v_lines, v_total
                USING ERRCODE = 'check_violation', CONSTRAINT = 'supplier_credit_notes_lines_match_total';
        END IF;
    END IF;
    SELECT COALESCE(sum(amount), 0) INTO v_allocated FROM supplier_credit_allocations WHERE credit_id = v_credit_id;
    IF v_allocated > v_total THEN
        RAISE EXCEPTION 'supplier credit note % allocates % of %', v_credit_id, v_allocated, v_total
            USING ERRCODE = 'check_violation', CONSTRAINT = 'supplier_credit_notes_allocated_within_total';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER supplier_credit_notes_total_check AFTER INSERT ON supplier_credit_notes
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_credit_total_check();
CREATE CONSTRAINT TRIGGER supplier_credit_lines_total_check AFTER INSERT ON supplier_credit_note_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_credit_total_check();
CREATE CONSTRAINT TRIGGER supplier_credit_allocations_total_check AFTER INSERT ON supplier_credit_allocations
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_credit_total_check();

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'SUPPLIER_CREDIT', 'SCN' FROM properties;

-- The input VAT of a credit note is claimed back on the return like that of a bill, with the sign changed: a negative claim per credit note line whose VAT was creditable, and a positive one that reverses it
-- when the credit note is voided after it was claimed. A claim names a bill line or a credit note line, never both.
ALTER TABLE tax_return_input_claims ADD COLUMN credit_id bigint;
ALTER TABLE tax_return_input_claims ADD COLUMN credit_line_no int;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_credit_fk FOREIGN KEY (property_id, credit_id, credit_line_no) REFERENCES supplier_credit_note_lines (property_id, credit_id, line_no);
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_credit_pair_ck CHECK ((credit_id IS NULL) = (credit_line_no IS NULL));
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_sign_ck;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_sign_ck CHECK (
    (credit_id IS NULL AND ((reverses_claim_id IS NULL AND amount > 0) OR (reverses_claim_id IS NOT NULL AND amount < 0)))
    OR (credit_id IS NOT NULL AND ((reverses_claim_id IS NULL AND amount < 0) OR (reverses_claim_id IS NOT NULL AND amount > 0))));
ALTER TABLE tax_return_input_claims ALTER COLUMN bill_id DROP NOT NULL;
ALTER TABLE tax_return_input_claims ALTER COLUMN line_no DROP NOT NULL;
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_line_fk;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_line_fk FOREIGN KEY (property_id, bill_id, line_no) REFERENCES supplier_bill_lines (property_id, bill_id, line_no);
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_source_ck CHECK ((bill_id IS NOT NULL AND line_no IS NOT NULL AND credit_id IS NULL) OR (bill_id IS NULL AND line_no IS NULL AND credit_id IS NOT NULL));
DROP INDEX tax_return_input_claims_live_uk;
CREATE UNIQUE INDEX tax_return_input_claims_live_uk ON tax_return_input_claims (property_id, bill_id, line_no) WHERE released_at IS NULL AND reverses_claim_id IS NULL AND credit_id IS NULL;
CREATE UNIQUE INDEX tax_return_input_claims_live_credit_uk ON tax_return_input_claims (property_id, credit_id, credit_line_no) WHERE released_at IS NULL AND reverses_claim_id IS NULL AND credit_id IS NOT NULL;

-- +goose Down
DROP INDEX tax_return_input_claims_live_credit_uk;
DROP INDEX tax_return_input_claims_live_uk;
DELETE FROM tax_return_input_claims WHERE credit_id IS NOT NULL;
CREATE UNIQUE INDEX tax_return_input_claims_live_uk ON tax_return_input_claims (property_id, bill_id, line_no) WHERE released_at IS NULL AND reverses_claim_id IS NULL;
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_source_ck;
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_line_fk;
ALTER TABLE tax_return_input_claims ALTER COLUMN line_no SET NOT NULL;
ALTER TABLE tax_return_input_claims ALTER COLUMN bill_id SET NOT NULL;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_line_fk FOREIGN KEY (property_id, bill_id, line_no) REFERENCES supplier_bill_lines (property_id, bill_id, line_no);
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_sign_ck;
ALTER TABLE tax_return_input_claims ADD CONSTRAINT tax_return_input_claims_sign_ck CHECK ((reverses_claim_id IS NULL AND amount > 0) OR (reverses_claim_id IS NOT NULL AND amount < 0));
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_credit_pair_ck;
ALTER TABLE tax_return_input_claims DROP CONSTRAINT tax_return_input_claims_credit_fk;
ALTER TABLE tax_return_input_claims DROP COLUMN credit_line_no;
ALTER TABLE tax_return_input_claims DROP COLUMN credit_id;
DELETE FROM document_sequences WHERE sequence_type = 'SUPPLIER_CREDIT';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT'));
DROP TABLE supplier_credit_allocations;
DROP TABLE supplier_credit_note_lines;
DROP TABLE supplier_credit_notes;
DROP FUNCTION supplier_credit_total_check();
DROP FUNCTION supplier_credit_notes_guard();
