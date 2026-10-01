-- +goose Up
-- Accounts payable: suppliers, their bills, and the payments that settle them. A bill and a payment each post a journal
-- of type PAYABLES when they are entered (a bill debits its expense, asset or tax lines against ACCOUNTS PAYABLE, a
-- payment debits ACCOUNTS PAYABLE against the cash or bank account), so the books carry the expenses of the hotel.
-- What a supplier is owed is derived: bills minus the allocations of payments that are not voided.

-- The control account of the payables.
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE'));
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id)
SELECT a.tenant_id, a.property_id, 'ACCOUNTS_PAYABLE', a.id
  FROM gl_accounts a
 WHERE a.code = '2110' AND EXISTS (SELECT 1 FROM accounting_settings s WHERE s.property_id = a.property_id)
ON CONFLICT (property_id, map_key) DO NOTHING;

ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES'));

CREATE TABLE suppliers (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    code                varchar(20)   NOT NULL,
    name                varchar(150)  NOT NULL,
    contact_name        varchar(150),
    email               varchar(254),
    phone               varchar(40),
    address             varchar(300),
    city                varchar(100),
    tax_id              varchar(40),
    payment_terms_days  smallint      NOT NULL DEFAULT 30,
    default_account_id  bigint,
    bank_details        varchar(300),
    notes               varchar(1000),
    is_active           boolean       NOT NULL DEFAULT true,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    updated_by          bigint REFERENCES users (id),
    CONSTRAINT suppliers_property_fk      FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT suppliers_account_fk       FOREIGN KEY (property_id, default_account_id)  REFERENCES gl_accounts (property_id, id),
    CONSTRAINT suppliers_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT suppliers_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT suppliers_code_ck          CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT suppliers_terms_ck         CHECK (payment_terms_days BETWEEN 0 AND 365)
);
CREATE TRIGGER suppliers_set_updated_at BEFORE UPDATE ON suppliers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE supplier_bills (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                bigint        NOT NULL,
    property_id              bigint        NOT NULL,
    bill_number              varchar(20)   NOT NULL,
    supplier_id              bigint        NOT NULL,
    supplier_invoice_number  varchar(60)   NOT NULL,
    bill_date                date          NOT NULL,
    due_date                 date          NOT NULL,
    description              varchar(300),
    total                    numeric(18,3) NOT NULL,
    status                   varchar(10)   NOT NULL DEFAULT 'POSTED',
    journal_id               bigint        NOT NULL,
    void_journal_id          bigint,
    voided_at                timestamptz,
    voided_by                bigint REFERENCES users (id),
    void_reason              varchar(500),
    approved_by              bigint REFERENCES users (id),
    idempotency_key          varchar(100),
    created_at               timestamptz   NOT NULL DEFAULT now(),
    created_by               bigint REFERENCES users (id),
    CONSTRAINT supplier_bills_property_fk     FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT supplier_bills_supplier_fk     FOREIGN KEY (property_id, supplier_id)       REFERENCES suppliers (property_id, id),
    CONSTRAINT supplier_bills_journal_fk      FOREIGN KEY (property_id, journal_id)        REFERENCES gl_journals (property_id, id),
    CONSTRAINT supplier_bills_void_journal_fk FOREIGN KEY (property_id, void_journal_id)   REFERENCES gl_journals (property_id, id),
    CONSTRAINT supplier_bills_number_uk       UNIQUE (property_id, bill_number),
    CONSTRAINT supplier_bills_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT supplier_bills_supplier_id_uk  UNIQUE (property_id, supplier_id, id),
    CONSTRAINT supplier_bills_total_ck        CHECK (total > 0),
    CONSTRAINT supplier_bills_due_ck          CHECK (due_date >= bill_date),
    CONSTRAINT supplier_bills_status_ck       CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT supplier_bills_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT supplier_bills_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
-- The same invoice of a supplier is entered once (a voided one may be entered again).
CREATE UNIQUE INDEX supplier_bills_invoice_uk     ON supplier_bills (property_id, supplier_id, supplier_invoice_number) WHERE status = 'POSTED';
CREATE UNIQUE INDEX supplier_bills_idempotency_uk ON supplier_bills (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX supplier_bills_supplier_idx ON supplier_bills (property_id, supplier_id, bill_date);
CREATE INDEX supplier_bills_due_idx      ON supplier_bills (property_id, due_date) WHERE status = 'POSTED';

CREATE TABLE supplier_bill_lines (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    bill_id      bigint        NOT NULL,
    line_no      int           NOT NULL,
    account_id   bigint        NOT NULL,
    description  varchar(300),
    amount       numeric(18,3) NOT NULL,
    CONSTRAINT supplier_bill_lines_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT supplier_bill_lines_bill_fk     FOREIGN KEY (property_id, bill_id)    REFERENCES supplier_bills (property_id, id),
    CONSTRAINT supplier_bill_lines_account_fk  FOREIGN KEY (property_id, account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT supplier_bill_lines_no_uk       UNIQUE (bill_id, line_no),
    CONSTRAINT supplier_bill_lines_amount_ck   CHECK (amount > 0)
);

CREATE TABLE supplier_payments (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    payment_number    varchar(20)   NOT NULL,
    supplier_id       bigint        NOT NULL,
    payment_date      date          NOT NULL,
    amount            numeric(18,3) NOT NULL,
    payment_method    varchar(15)   NOT NULL,
    reference_number  varchar(100),
    remarks           varchar(500),
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
    CONSTRAINT supplier_payments_property_fk     FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT supplier_payments_supplier_fk     FOREIGN KEY (property_id, supplier_id)     REFERENCES suppliers (property_id, id),
    CONSTRAINT supplier_payments_journal_fk      FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT supplier_payments_void_journal_fk FOREIGN KEY (property_id, void_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT supplier_payments_number_uk       UNIQUE (property_id, payment_number),
    CONSTRAINT supplier_payments_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT supplier_payments_supplier_id_uk  UNIQUE (property_id, supplier_id, id),
    CONSTRAINT supplier_payments_amount_ck       CHECK (amount > 0),
    CONSTRAINT supplier_payments_method_ck       CHECK (payment_method IN ('CASH', 'BANK_TRANSFER', 'OTHER')),
    CONSTRAINT supplier_payments_status_ck       CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT supplier_payments_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT supplier_payments_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX supplier_payments_idempotency_uk ON supplier_payments (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX supplier_payments_supplier_idx ON supplier_payments (property_id, supplier_id, payment_date);

-- What a payment settles: always bills of its own supplier (composite foreign keys), and all of the payment.
CREATE TABLE supplier_payment_allocations (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    supplier_id  bigint        NOT NULL,
    payment_id   bigint        NOT NULL,
    bill_id      bigint        NOT NULL,
    amount       numeric(18,3) NOT NULL,
    CONSTRAINT spa_payment_bill_uk UNIQUE (payment_id, bill_id),
    CONSTRAINT spa_tenant_fk   FOREIGN KEY (tenant_id, property_id)                REFERENCES properties (tenant_id, id),
    CONSTRAINT spa_payment_fk  FOREIGN KEY (property_id, supplier_id, payment_id)  REFERENCES supplier_payments (property_id, supplier_id, id),
    CONSTRAINT spa_bill_fk     FOREIGN KEY (property_id, supplier_id, bill_id)     REFERENCES supplier_bills (property_id, supplier_id, id),
    CONSTRAINT spa_amount_ck   CHECK (amount > 0)
);
CREATE INDEX spa_bill_idx ON supplier_payment_allocations (bill_id);

-- Bills and payments are never deleted; they only go from POSTED to VOIDED. Lines and allocations never change.
-- +goose StatementBegin
CREATE FUNCTION supplier_bills_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'supplier bills cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'supplier_bills_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by']) THEN
        RAISE EXCEPTION 'supplier bill % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'supplier_bills_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION supplier_payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'supplier payments cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'supplier_payments_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'void_journal_id', 'approved_by']) THEN
        RAISE EXCEPTION 'supplier payment % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'supplier_payments_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER supplier_bills_guard BEFORE UPDATE OR DELETE ON supplier_bills FOR EACH ROW EXECUTE FUNCTION supplier_bills_guard();
CREATE TRIGGER supplier_payments_guard BEFORE UPDATE OR DELETE ON supplier_payments FOR EACH ROW EXECUTE FUNCTION supplier_payments_guard();
CREATE TRIGGER supplier_bill_lines_append_only BEFORE UPDATE OR DELETE ON supplier_bill_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER supplier_payment_allocations_append_only BEFORE UPDATE OR DELETE ON supplier_payment_allocations FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER supplier_bills_no_truncate BEFORE TRUNCATE ON supplier_bills FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER supplier_bill_lines_no_truncate BEFORE TRUNCATE ON supplier_bill_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER supplier_payments_no_truncate BEFORE TRUNCATE ON supplier_payments FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER supplier_payment_allocations_no_truncate BEFORE TRUNCATE ON supplier_payment_allocations FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Deferred check at COMMIT: what a payment allocates is the whole payment; the lines of a bill add up to its total.
-- +goose StatementBegin
CREATE FUNCTION supplier_payment_allocated_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_payment_id bigint;
    v_amount     numeric;
    v_allocated  numeric;
BEGIN
    IF TG_TABLE_NAME = 'supplier_payments' THEN
        v_payment_id := NEW.id;
    ELSE
        v_payment_id := NEW.payment_id;
    END IF;
    SELECT amount INTO v_amount FROM supplier_payments WHERE id = v_payment_id;
    SELECT COALESCE(sum(amount), 0) INTO v_allocated FROM supplier_payment_allocations WHERE payment_id = v_payment_id;
    IF v_amount <> v_allocated THEN
        RAISE EXCEPTION 'supplier payment % allocates % of %', v_payment_id, v_allocated, v_amount
            USING ERRCODE = 'check_violation', CONSTRAINT = 'supplier_payments_fully_allocated';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER supplier_payments_allocated_check AFTER INSERT ON supplier_payments
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_payment_allocated_check();
CREATE CONSTRAINT TRIGGER supplier_allocations_allocated_check AFTER INSERT ON supplier_payment_allocations
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_payment_allocated_check();

-- +goose StatementBegin
CREATE FUNCTION supplier_bill_total_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_bill_id bigint;
    v_total   numeric;
    v_lines   numeric;
BEGIN
    IF TG_TABLE_NAME = 'supplier_bills' THEN
        v_bill_id := NEW.id;
    ELSE
        v_bill_id := NEW.bill_id;
    END IF;
    SELECT total INTO v_total FROM supplier_bills WHERE id = v_bill_id;
    SELECT COALESCE(sum(amount), 0) INTO v_lines FROM supplier_bill_lines WHERE bill_id = v_bill_id;
    IF v_total <> v_lines THEN
        RAISE EXCEPTION 'supplier bill % has lines of % for a total of %', v_bill_id, v_lines, v_total
            USING ERRCODE = 'check_violation', CONSTRAINT = 'supplier_bills_lines_match_total';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER supplier_bills_total_check AFTER INSERT ON supplier_bills
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_bill_total_check();
CREATE CONSTRAINT TRIGGER supplier_bill_lines_total_check AFTER INSERT ON supplier_bill_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION supplier_bill_total_check();

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'SUPPLIER_BILL', 'BILL' FROM properties;
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'SUPPLIER_PAYMENT', 'SPAY' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type IN ('SUPPLIER_BILL', 'SUPPLIER_PAYMENT');
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL'));
DROP TABLE IF EXISTS supplier_payment_allocations;
DROP TABLE IF EXISTS supplier_payments;
DROP TABLE IF EXISTS supplier_bill_lines;
DROP TABLE IF EXISTS supplier_bills;
DROP TABLE IF EXISTS suppliers;
DROP FUNCTION IF EXISTS supplier_payment_allocated_check();
DROP FUNCTION IF EXISTS supplier_bill_total_check();
DROP FUNCTION IF EXISTS supplier_bills_guard();
DROP FUNCTION IF EXISTS supplier_payments_guard();
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING'));
DELETE FROM gl_account_map WHERE map_key = 'ACCOUNTS_PAYABLE';
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS'));
