-- +goose Up
-- Cashier shifts and the cash drawer (design: docs/architecture/11-cashier-budget-cashflow-card.md, part A). A cashier opens a shift with a float, takes cash
-- payments on it, moves cash (drop to the safe, pay-in, pay-out) and closes it with the count; the difference to the cash expected is journaled.

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'SHIFT', 'SHF' FROM properties;

-- The journal of a cashier: the over or short of a shift, a pay-in, a pay-out.
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX', 'RECEIVABLES', 'CASHIER'));

-- The account the cash over and short is booked on (a system account, with the account 6195 in the standard chart).
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT'));
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v2;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_v2()
    UNION ALL
    SELECT '6195', 'Cash over and short', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'
$$;
-- +goose StatementEnd
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
SELECT s.tenant_id, s.property_id, '6195', 'Cash over and short', 'EXPENSE', 'DEBIT',
       (SELECT p.id FROM gl_accounts p WHERE p.property_id = s.property_id AND p.code = '6100'), true, 'UND_AG'
  FROM accounting_settings s
ON CONFLICT (property_id, code) DO NOTHING;
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id)
SELECT a.tenant_id, a.property_id, 'CASH_OVER_SHORT', a.id
  FROM gl_accounts a
 WHERE a.code = '6195' AND EXISTS (SELECT 1 FROM accounting_settings s WHERE s.property_id = a.property_id)
ON CONFLICT (property_id, map_key) DO NOTHING;

-- The settings of the cashier of a property. A new property needs a shift for cash; the properties that exist now keep working as they did until they turn it on.
CREATE TABLE property_cashier_settings (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                bigint        NOT NULL,
    property_id              bigint        NOT NULL,
    require_shift_for_cash   boolean       NOT NULL DEFAULT true,
    max_variance             numeric(18,3) NOT NULL DEFAULT 0,
    block_night_audit        boolean       NOT NULL DEFAULT true,
    updated_at               timestamptz   NOT NULL DEFAULT now(),
    updated_by               bigint REFERENCES users (id),
    CONSTRAINT pcs_property_fk   FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pcs_property_uk   UNIQUE (property_id),
    CONSTRAINT pcs_variance_ck   CHECK (max_variance >= 0)
);
CREATE TRIGGER property_cashier_settings_set_updated_at BEFORE UPDATE ON property_cashier_settings FOR EACH ROW EXECUTE FUNCTION set_updated_at();
INSERT INTO property_cashier_settings (tenant_id, property_id, require_shift_for_cash, block_night_audit) SELECT tenant_id, id, false, false FROM properties;
-- +goose StatementBegin
CREATE FUNCTION properties_seed_cashier_settings() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO property_cashier_settings (tenant_id, property_id) VALUES (NEW.tenant_id, NEW.id);
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER properties_seed_cashier_settings AFTER INSERT ON properties
    FOR EACH ROW EXECUTE FUNCTION properties_seed_cashier_settings();

CREATE TABLE cashier_shifts (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint        NOT NULL,
    property_id          bigint        NOT NULL,
    shift_number         varchar(20)   NOT NULL,
    user_id              bigint        NOT NULL REFERENCES users (id),
    drawer               varchar(20)   NOT NULL DEFAULT 'MAIN',
    status               varchar(10)   NOT NULL DEFAULT 'OPEN',
    opened_at            timestamptz   NOT NULL,
    business_date_opened date          NOT NULL,
    opening_float        numeric(18,3) NOT NULL DEFAULT 0,
    closed_at            timestamptz,
    business_date_closed date,
    expected_cash        numeric(18,3),
    counted_cash         numeric(18,3),
    over_short           numeric(18,3),
    variance_reason      varchar(500),
    journal_id           bigint,
    closed_by            bigint REFERENCES users (id),
    approved_by          bigint REFERENCES users (id),
    handed_over_to       bigint REFERENCES users (id),
    created_at           timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT cs_property_fk      FOREIGN KEY (tenant_id, property_id)                REFERENCES properties (tenant_id, id),
    CONSTRAINT cs_opened_day_fk    FOREIGN KEY (property_id, business_date_opened)     REFERENCES business_days (property_id, business_date),
    CONSTRAINT cs_closed_day_fk    FOREIGN KEY (property_id, business_date_closed)     REFERENCES business_days (property_id, business_date),
    CONSTRAINT cs_journal_fk       FOREIGN KEY (property_id, journal_id)               REFERENCES gl_journals (property_id, id),
    CONSTRAINT cs_number_uk        UNIQUE (property_id, shift_number),
    CONSTRAINT cs_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT cs_status_ck        CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT cs_float_ck         CHECK (opening_float >= 0),
    CONSTRAINT cs_drawer_ck        CHECK (length(btrim(drawer)) > 0),
    CONSTRAINT cs_closed_ck        CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL AND business_date_closed IS NOT NULL AND expected_cash IS NOT NULL AND counted_cash IS NOT NULL AND over_short IS NOT NULL AND closed_by IS NOT NULL)),
    CONSTRAINT cs_counted_ck       CHECK (counted_cash IS NULL OR (counted_cash >= 0 AND over_short = counted_cash - expected_cash))
);
-- One open shift per cashier and per drawer.
CREATE UNIQUE INDEX cs_open_user_uk   ON cashier_shifts (property_id, user_id) WHERE status = 'OPEN';
CREATE UNIQUE INDEX cs_open_drawer_uk ON cashier_shifts (property_id, drawer) WHERE status = 'OPEN';
CREATE INDEX cs_user_idx ON cashier_shifts (property_id, user_id, opened_at);
-- Opening to closing is the only change a shift takes, and the columns of the opening stay.
-- +goose StatementBegin
CREATE FUNCTION cashier_shifts_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'cashier shifts cannot be deleted' USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cs_no_delete';
    END IF;
    IF OLD.status <> 'OPEN' OR NEW.status <> 'CLOSED'
       OR (to_jsonb(NEW) - ARRAY['status', 'closed_at', 'business_date_closed', 'expected_cash', 'counted_cash', 'over_short', 'variance_reason', 'journal_id', 'closed_by', 'approved_by', 'handed_over_to'])
          <> (to_jsonb(OLD) - ARRAY['status', 'closed_at', 'business_date_closed', 'expected_cash', 'counted_cash', 'over_short', 'variance_reason', 'journal_id', 'closed_by', 'approved_by', 'handed_over_to']) THEN
        RAISE EXCEPTION 'shift % may only change from OPEN to CLOSED', OLD.id USING ERRCODE = 'restrict_violation', CONSTRAINT = 'cs_close_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER cashier_shifts_guard BEFORE UPDATE OR DELETE ON cashier_shifts FOR EACH ROW EXECUTE FUNCTION cashier_shifts_guard();
CREATE TRIGGER cashier_shifts_no_truncate BEFORE TRUNCATE ON cashier_shifts FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Cash moved in or out of the drawer while a shift is open: a drop to the safe (no journal), a pay-in or a pay-out (journaled against the account chosen).
CREATE TABLE cashier_shift_movements (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    shift_id      bigint        NOT NULL,
    kind          varchar(10)   NOT NULL,
    amount        numeric(18,3) NOT NULL,
    account_id    bigint,
    reason        varchar(500)  NOT NULL,
    business_date date          NOT NULL,
    journal_id    bigint,
    idempotency_key varchar(100),
    created_at    timestamptz   NOT NULL DEFAULT now(),
    created_by    bigint REFERENCES users (id),
    CONSTRAINT csm_property_fk  FOREIGN KEY (tenant_id, property_id)            REFERENCES properties (tenant_id, id),
    CONSTRAINT csm_shift_fk     FOREIGN KEY (property_id, shift_id)             REFERENCES cashier_shifts (property_id, id),
    CONSTRAINT csm_account_fk   FOREIGN KEY (property_id, account_id)           REFERENCES gl_accounts (property_id, id),
    CONSTRAINT csm_journal_fk   FOREIGN KEY (property_id, journal_id)           REFERENCES gl_journals (property_id, id),
    CONSTRAINT csm_day_fk       FOREIGN KEY (property_id, business_date)        REFERENCES business_days (property_id, business_date),
    CONSTRAINT csm_kind_ck      CHECK (kind IN ('DROP', 'PAY_IN', 'PAY_OUT')),
    CONSTRAINT csm_amount_ck    CHECK (amount > 0),
    CONSTRAINT csm_reason_ck    CHECK (length(btrim(reason)) > 0),
    -- A drop is not journaled and has no account; a pay-in and a pay-out have both.
    CONSTRAINT csm_journal_ck   CHECK ((kind = 'DROP') = (account_id IS NULL AND journal_id IS NULL) AND (kind = 'DROP' OR (account_id IS NOT NULL AND journal_id IS NOT NULL)))
);
CREATE INDEX csm_shift_idx ON cashier_shift_movements (property_id, shift_id);
CREATE UNIQUE INDEX csm_idempotency_uk ON cashier_shift_movements (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER cashier_shift_movements_append_only BEFORE UPDATE OR DELETE ON cashier_shift_movements FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER cashier_shift_movements_no_truncate BEFORE TRUNCATE ON cashier_shift_movements FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The count at close by denomination (optional).
CREATE TABLE cashier_shift_counts (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    shift_id     bigint        NOT NULL,
    denomination numeric(18,3) NOT NULL,
    quantity     int           NOT NULL,
    CONSTRAINT csc_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT csc_shift_fk    FOREIGN KEY (property_id, shift_id)  REFERENCES cashier_shifts (property_id, id),
    CONSTRAINT csc_pair_uk     UNIQUE (shift_id, denomination),
    CONSTRAINT csc_ck          CHECK (denomination > 0 AND quantity >= 0)
);
CREATE TRIGGER cashier_shift_counts_append_only BEFORE UPDATE OR DELETE ON cashier_shift_counts FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER cashier_shift_counts_no_truncate BEFORE TRUNCATE ON cashier_shift_counts FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The shift a cash payment, a cash refund or a cash receipt of the city ledger went through.
ALTER TABLE payments ADD COLUMN shift_id bigint;
ALTER TABLE payments ADD CONSTRAINT payments_shift_fk FOREIGN KEY (property_id, shift_id) REFERENCES cashier_shifts (property_id, id);
CREATE INDEX payments_shift_idx ON payments (shift_id) WHERE shift_id IS NOT NULL;
ALTER TABLE city_ledger_receipts ADD COLUMN shift_id bigint;
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_shift_fk FOREIGN KEY (property_id, shift_id) REFERENCES cashier_shifts (property_id, id);
CREATE INDEX clr_shift_idx ON city_ledger_receipts (shift_id) WHERE shift_id IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS clr_shift_idx;
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_shift_fk;
ALTER TABLE city_ledger_receipts DROP COLUMN shift_id;
DROP INDEX IF EXISTS payments_shift_idx;
ALTER TABLE payments DROP CONSTRAINT payments_shift_fk;
ALTER TABLE payments DROP COLUMN shift_id;
DROP TABLE IF EXISTS cashier_shift_counts;
DROP TABLE IF EXISTS cashier_shift_movements;
DROP TABLE IF EXISTS cashier_shifts;
DROP FUNCTION IF EXISTS cashier_shifts_guard();
DROP TRIGGER IF EXISTS properties_seed_cashier_settings ON properties;
DROP FUNCTION IF EXISTS properties_seed_cashier_settings();
DROP TABLE IF EXISTS property_cashier_settings;
DELETE FROM gl_account_map WHERE map_key = 'CASH_OVER_SHORT';
DROP FUNCTION IF EXISTS usali_chart();
ALTER FUNCTION usali_chart_v2() RENAME TO usali_chart;
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT'));
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING', 'PAYABLES', 'BANK', 'TAX', 'RECEIVABLES'));
DELETE FROM document_sequences WHERE sequence_type = 'SHIFT';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER'));
