-- +goose Up
-- Bank reconciliation: the bank statements of the cash and bank accounts of the books are imported, their lines are
-- matched ("cleared") with the journal lines of the account, and a statement is reconciled when what the bank says at its
-- end equals what the cleared journal lines add up to. Journal lines are never changed: clearing is a record beside them.

ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK'));

-- The accounts of the books that are reconciled with a bank (or counted cash), one row per account.
CREATE TABLE bank_accounts (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    account_id      bigint       NOT NULL,
    name            varchar(100) NOT NULL,
    account_number  varchar(60),
    is_active       boolean      NOT NULL DEFAULT true,
    created_at      timestamptz  NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    updated_at      timestamptz  NOT NULL DEFAULT now(),
    updated_by      bigint REFERENCES users (id),
    CONSTRAINT bank_accounts_property_fk    FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT bank_accounts_account_fk     FOREIGN KEY (property_id, account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT bank_accounts_account_uk     UNIQUE (property_id, account_id),
    CONSTRAINT bank_accounts_property_id_uk UNIQUE (property_id, id)
);
CREATE TRIGGER bank_accounts_set_updated_at BEFORE UPDATE ON bank_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE bank_statements (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    bank_account_id  bigint        NOT NULL,
    period_from      date          NOT NULL,
    period_to        date          NOT NULL,
    opening_balance  numeric(18,3) NOT NULL,
    closing_balance  numeric(18,3) NOT NULL,
    status           varchar(10)   NOT NULL DEFAULT 'OPEN',
    note             varchar(300),
    imported_at      timestamptz   NOT NULL DEFAULT now(),
    imported_by      bigint REFERENCES users (id),
    reconciled_at    timestamptz,
    reconciled_by    bigint REFERENCES users (id),
    reopened_at      timestamptz,
    reopened_by      bigint REFERENCES users (id),
    reopen_reason    varchar(500),
    CONSTRAINT bank_statements_property_fk     FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT bank_statements_bank_fk         FOREIGN KEY (property_id, bank_account_id) REFERENCES bank_accounts (property_id, id),
    CONSTRAINT bank_statements_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT bank_statements_bank_id_uk      UNIQUE (property_id, bank_account_id, id),
    CONSTRAINT bank_statements_period_ck       CHECK (period_to >= period_from),
    CONSTRAINT bank_statements_status_ck       CHECK (status IN ('OPEN', 'RECONCILED')),
    CONSTRAINT bank_statements_reconciled_ck   CHECK ((status = 'RECONCILED') = (reconciled_at IS NOT NULL))
);
CREATE INDEX bank_statements_bank_idx ON bank_statements (property_id, bank_account_id, period_to);

CREATE TABLE bank_statement_lines (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    statement_id  bigint        NOT NULL,
    line_no       int           NOT NULL,
    line_date     date          NOT NULL,
    description   varchar(300),
    reference     varchar(100),
    -- Money in is positive, money out is negative, as the account of the books sees it (a debit is positive).
    amount        numeric(18,3) NOT NULL,
    CONSTRAINT bank_statement_lines_property_fk  FOREIGN KEY (tenant_id, property_id)   REFERENCES properties (tenant_id, id),
    CONSTRAINT bank_statement_lines_statement_fk FOREIGN KEY (property_id, statement_id) REFERENCES bank_statements (property_id, id),
    CONSTRAINT bank_statement_lines_no_uk        UNIQUE (statement_id, line_no),
    CONSTRAINT bank_statement_lines_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT bank_statement_lines_amount_ck    CHECK (amount <> 0)
);

-- A journal line cleared in a statement, once. With a statement line it is what the bank shows for it; without one it
-- is cleared on its own: from before the bank reconciliation started, or offsetting another (a voided payment and its
-- reversal).
CREATE TABLE bank_clearings (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    bank_account_id    bigint        NOT NULL,
    statement_id       bigint        NOT NULL,
    statement_line_id  bigint,
    journal_line_id    bigint        NOT NULL,
    amount             numeric(18,3) NOT NULL,
    cleared_at         timestamptz   NOT NULL DEFAULT now(),
    cleared_by         bigint REFERENCES users (id),
    CONSTRAINT bank_clearings_property_fk  FOREIGN KEY (tenant_id, property_id)                      REFERENCES properties (tenant_id, id),
    CONSTRAINT bank_clearings_statement_fk FOREIGN KEY (property_id, bank_account_id, statement_id)  REFERENCES bank_statements (property_id, bank_account_id, id),
    CONSTRAINT bank_clearings_line_fk      FOREIGN KEY (property_id, statement_line_id)              REFERENCES bank_statement_lines (property_id, id),
    CONSTRAINT bank_clearings_journal_fk   FOREIGN KEY (journal_line_id)                             REFERENCES gl_journal_lines (id),
    CONSTRAINT bank_clearings_journal_uk   UNIQUE (journal_line_id)
);
CREATE INDEX bank_clearings_statement_idx ON bank_clearings (statement_id);
CREATE INDEX bank_clearings_line_idx      ON bank_clearings (statement_line_id) WHERE statement_line_id IS NOT NULL;

-- Statements, their lines and their clearings change only while the statement is OPEN; a reconciled statement is final
-- (reopening it is a status change, which is the one update allowed).
-- +goose StatementBegin
CREATE FUNCTION bank_statement_open_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_status varchar;
BEGIN
    IF TG_TABLE_NAME = 'bank_statements' THEN
        IF TG_OP = 'DELETE' THEN
            IF OLD.status <> 'OPEN' THEN
                RAISE EXCEPTION 'a reconciled bank statement cannot be deleted'
                    USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bank_statements_final';
            END IF;
            RETURN OLD;
        END IF;
        IF OLD.status = 'RECONCILED' AND NEW.status = 'RECONCILED' THEN
            RAISE EXCEPTION 'a reconciled bank statement cannot change'
                USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bank_statements_final';
        END IF;
        IF (to_jsonb(NEW) - ARRAY['status', 'reconciled_at', 'reconciled_by', 'reopened_at', 'reopened_by', 'reopen_reason', 'note'])
           <> (to_jsonb(OLD) - ARRAY['status', 'reconciled_at', 'reconciled_by', 'reopened_at', 'reopened_by', 'reopen_reason', 'note']) THEN
            RAISE EXCEPTION 'a bank statement keeps its figures'
                USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bank_statements_final';
        END IF;
        RETURN NEW;
    END IF;
    SELECT status INTO v_status FROM bank_statements WHERE id = COALESCE(NEW.statement_id, OLD.statement_id);
    IF v_status IS DISTINCT FROM 'OPEN' THEN
        RAISE EXCEPTION 'the bank statement is reconciled: reopen it to change its lines or clearings'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bank_statements_final';
    END IF;
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION '% cannot be updated', TG_TABLE_NAME
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bank_statements_final';
    END IF;
    RETURN COALESCE(NEW, OLD);
END
$$;
-- +goose StatementEnd
CREATE TRIGGER bank_statements_guard BEFORE UPDATE OR DELETE ON bank_statements FOR EACH ROW EXECUTE FUNCTION bank_statement_open_guard();
CREATE TRIGGER bank_statement_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON bank_statement_lines FOR EACH ROW EXECUTE FUNCTION bank_statement_open_guard();
CREATE TRIGGER bank_clearings_guard BEFORE INSERT OR UPDATE OR DELETE ON bank_clearings FOR EACH ROW EXECUTE FUNCTION bank_statement_open_guard();
CREATE TRIGGER bank_statements_no_truncate BEFORE TRUNCATE ON bank_statements FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER bank_statement_lines_no_truncate BEFORE TRUNCATE ON bank_statement_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER bank_clearings_no_truncate BEFORE TRUNCATE ON bank_clearings FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- +goose Down
DROP TABLE IF EXISTS bank_clearings;
DROP TABLE IF EXISTS bank_statement_lines;
DROP TABLE IF EXISTS bank_statements;
DROP TABLE IF EXISTS bank_accounts;
DROP FUNCTION IF EXISTS bank_statement_open_guard();
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES'));
