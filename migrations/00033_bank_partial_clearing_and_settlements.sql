-- +goose Up
-- Bank reconciliation, second step:
--  1. A journal line can be cleared in parts (several bank lines against one journal line, as when the day close journal
--     carries the total of several transfers): the unique index on the journal line becomes a check that what is cleared
--     of a line stays within it.
--  2. Card and e-wallet settlements: the acquirer pays a batch net of its commission. The settlement posts a journal that
--     moves the gross from the clearing account to the bank and the commission to an expense, and records which
--     payment lines it settled, so each is settled once.
--  3. The posting view carries the payment number and the reference the guest or the bank gave, so the day close journal
--     can carry a line per payment.

ALTER TABLE bank_clearings DROP CONSTRAINT bank_clearings_journal_uk;
ALTER TABLE bank_clearings ADD CONSTRAINT bank_clearings_amount_ck CHECK (amount <> 0);
CREATE UNIQUE INDEX bank_clearings_pair_uk ON bank_clearings (journal_line_id, statement_line_id) WHERE statement_line_id IS NOT NULL;
CREATE INDEX bank_clearings_journal_idx ON bank_clearings (journal_line_id);

-- Deferred check at COMMIT: what is cleared of a journal line is on its side and within its amount.
-- +goose StatementBegin
CREATE FUNCTION bank_clearing_within_line_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_amount numeric;
    v_cleared numeric;
BEGIN
    SELECT debit - credit INTO v_amount FROM gl_journal_lines WHERE id = NEW.journal_line_id;
    SELECT COALESCE(sum(amount), 0) INTO v_cleared FROM bank_clearings WHERE journal_line_id = NEW.journal_line_id;
    IF NEW.amount * v_amount <= 0
       OR NOT ((v_amount > 0 AND v_cleared BETWEEN 0 AND v_amount) OR (v_amount < 0 AND v_cleared BETWEEN v_amount AND 0)) THEN
        RAISE EXCEPTION 'journal line % of % is cleared for %', NEW.journal_line_id, v_amount, v_cleared
            USING ERRCODE = 'check_violation', CONSTRAINT = 'bank_clearings_within_line';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER bank_clearings_within_line_check AFTER INSERT ON bank_clearings
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION bank_clearing_within_line_check();

CREATE TABLE card_settlements (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    bank_account_id  bigint        NOT NULL,
    account_key      varchar(15)   NOT NULL,
    journal_id       bigint        NOT NULL,
    gross            numeric(18,3) NOT NULL,
    net              numeric(18,3) NOT NULL,
    fee              numeric(18,3) NOT NULL,
    reference        varchar(100),
    created_at       timestamptz   NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    CONSTRAINT card_settlements_property_fk  FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT card_settlements_bank_fk      FOREIGN KEY (property_id, bank_account_id) REFERENCES bank_accounts (property_id, id),
    CONSTRAINT card_settlements_journal_fk   FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT card_settlements_journal_uk   UNIQUE (journal_id),
    CONSTRAINT card_settlements_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT card_settlements_key_ck       CHECK (account_key IN ('CARD', 'OTHER_PAYMENT')),
    CONSTRAINT card_settlements_amounts_ck   CHECK (gross > 0 AND net > 0 AND fee >= 0 AND gross = net + fee)
);

-- The payment lines a settlement settled: each line is settled once, by the credit line that clears it.
CREATE TABLE card_settlement_items (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    settlement_id    bigint        NOT NULL,
    settled_line_id  bigint        NOT NULL,
    settling_line_id bigint        NOT NULL,
    amount           numeric(18,3) NOT NULL,
    CONSTRAINT card_settlement_items_property_fk   FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT card_settlement_items_settlement_fk FOREIGN KEY (property_id, settlement_id) REFERENCES card_settlements (property_id, id),
    CONSTRAINT card_settlement_items_settled_fk    FOREIGN KEY (settled_line_id)            REFERENCES gl_journal_lines (id),
    CONSTRAINT card_settlement_items_settling_fk   FOREIGN KEY (settling_line_id)           REFERENCES gl_journal_lines (id),
    CONSTRAINT card_settlement_items_settled_uk    UNIQUE (settled_line_id),
    CONSTRAINT card_settlement_items_amount_ck     CHECK (amount <> 0)
);
CREATE INDEX card_settlement_items_settling_idx ON card_settlement_items (settling_line_id);

CREATE TRIGGER card_settlements_append_only BEFORE UPDATE OR DELETE ON card_settlements FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER card_settlement_items_append_only BEFORE UPDATE OR DELETE ON card_settlement_items FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER card_settlements_no_truncate BEFORE TRUNCATE ON card_settlements FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER card_settlement_items_no_truncate BEFORE TRUNCATE ON card_settlement_items FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The posting view, with the payment behind each item (columns added at the end).
CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND (st.id IS NULL OR rp.paid_at < st.actual_check_in_at)) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;

-- +goose Down
DROP VIEW IF EXISTS folio_item_gl;
CREATE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND (st.id IS NULL OR rp.paid_at < st.actual_check_in_at)) AS is_deposit
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;
DROP TABLE IF EXISTS card_settlement_items;
DROP TABLE IF EXISTS card_settlements;
DROP TRIGGER IF EXISTS bank_clearings_within_line_check ON bank_clearings;
DROP FUNCTION IF EXISTS bank_clearing_within_line_check();
DROP INDEX IF EXISTS bank_clearings_journal_idx;
DROP INDEX IF EXISTS bank_clearings_pair_uk;
ALTER TABLE bank_clearings DROP CONSTRAINT bank_clearings_amount_ck;
ALTER TABLE bank_clearings ADD CONSTRAINT bank_clearings_journal_uk UNIQUE (journal_line_id);
