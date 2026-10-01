-- +goose Up
-- The general ledger: journals posted at the close of each business day from the folio ledger, manual journals and
-- their reversals, and the periods that lock them. Journals and their lines are append-only; a mistake is corrected
-- by a reversal. The folio ledger stays the source of truth for guest accounts: a day's journal is derived from it.

-- When a folio was closed, as a business date: deposits held for the guest are released from the liability that day.
ALTER TABLE folios ADD COLUMN closed_on date;
UPDATE folios f SET closed_on = COALESCE(
    (SELECT b.business_date FROM business_days b
      WHERE b.property_id = f.property_id AND b.opened_at <= f.closed_at AND (b.closed_at IS NULL OR b.closed_at >= f.closed_at)
      ORDER BY b.business_date LIMIT 1),
    (SELECT min(b.business_date) FROM business_days b WHERE b.property_id = f.property_id))
 WHERE f.status = 'CLOSED';

CREATE TABLE gl_journals (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint        NOT NULL,
    property_id          bigint        NOT NULL,
    journal_number       varchar(20)   NOT NULL,
    journal_type         varchar(10)   NOT NULL,
    journal_date         date          NOT NULL,
    description          varchar(300)  NOT NULL,
    reference            varchar(100),
    reverses_journal_id  bigint,
    reason               varchar(500),
    idempotency_key      varchar(100),
    posted_at            timestamptz   NOT NULL DEFAULT now(),
    posted_by            bigint REFERENCES users (id),
    approved_by          bigint REFERENCES users (id),
    CONSTRAINT gl_journals_property_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_journals_number_uk      UNIQUE (property_id, journal_number),
    CONSTRAINT gl_journals_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT gl_journals_reverses_fk    FOREIGN KEY (property_id, reverses_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT gl_journals_type_ck        CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL')),
    CONSTRAINT gl_journals_reverses_ck    CHECK ((journal_type = 'REVERSAL') = (reverses_journal_id IS NOT NULL)),
    CONSTRAINT gl_journals_reason_ck      CHECK (journal_type <> 'REVERSAL' OR reason IS NOT NULL)
);
CREATE UNIQUE INDEX gl_journals_day_close_uk   ON gl_journals (property_id, journal_date) WHERE journal_type = 'DAY_CLOSE';
CREATE UNIQUE INDEX gl_journals_reverses_uk    ON gl_journals (reverses_journal_id) WHERE reverses_journal_id IS NOT NULL;
CREATE UNIQUE INDEX gl_journals_idempotency_uk ON gl_journals (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX gl_journals_date_idx ON gl_journals (property_id, journal_date, id);

CREATE TABLE gl_journal_lines (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint        NOT NULL,
    property_id  bigint        NOT NULL,
    journal_id   bigint        NOT NULL,
    line_no      int           NOT NULL,
    account_id   bigint        NOT NULL,
    debit        numeric(18,3) NOT NULL DEFAULT 0,
    credit       numeric(18,3) NOT NULL DEFAULT 0,
    description  varchar(300),
    source_type  varchar(20),
    source_ref   varchar(100),
    CONSTRAINT gl_journal_lines_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_journal_lines_journal_fk  FOREIGN KEY (property_id, journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT gl_journal_lines_account_fk  FOREIGN KEY (property_id, account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT gl_journal_lines_no_uk       UNIQUE (journal_id, line_no),
    CONSTRAINT gl_journal_lines_sides_ck    CHECK (debit >= 0 AND credit >= 0 AND (debit > 0) <> (credit > 0)),
    CONSTRAINT gl_journal_lines_source_ck   CHECK ((source_type IS NULL) = (source_ref IS NULL))
);
CREATE INDEX gl_journal_lines_account_idx ON gl_journal_lines (property_id, account_id);

CREATE TRIGGER gl_journals_append_only BEFORE UPDATE OR DELETE ON gl_journals
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER gl_journal_lines_append_only BEFORE UPDATE OR DELETE ON gl_journal_lines
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER gl_journals_no_truncate BEFORE TRUNCATE ON gl_journals
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();
CREATE TRIGGER gl_journal_lines_no_truncate BEFORE TRUNCATE ON gl_journal_lines
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Deferred check at COMMIT: a journal has lines and its debits equal its credits.
-- +goose StatementBegin
CREATE FUNCTION gl_journal_balance_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_journal_id bigint;
    v_debit      numeric;
    v_credit     numeric;
    v_lines      bigint;
BEGIN
    IF TG_TABLE_NAME = 'gl_journals' THEN
        v_journal_id := NEW.id;
    ELSE
        v_journal_id := NEW.journal_id;
    END IF;
    SELECT COALESCE(sum(debit), 0), COALESCE(sum(credit), 0), count(*) INTO v_debit, v_credit, v_lines
      FROM gl_journal_lines WHERE journal_id = v_journal_id;
    IF v_lines = 0 OR v_debit <> v_credit THEN
        RAISE EXCEPTION 'journal % is not balanced (% lines, debit %, credit %)', v_journal_id, v_lines, v_debit, v_credit
            USING ERRCODE = 'check_violation', CONSTRAINT = 'gl_journals_balanced';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER gl_journals_balance_check AFTER INSERT ON gl_journals
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION gl_journal_balance_check();
CREATE CONSTRAINT TRIGGER gl_journal_lines_balance_check AFTER INSERT ON gl_journal_lines
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION gl_journal_balance_check();

-- The business days whose journal has been made. journal_id is NULL for a day with nothing to post.
CREATE TABLE gl_day_posts (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint      NOT NULL,
    property_id    bigint      NOT NULL,
    business_date  date        NOT NULL,
    journal_id     bigint,
    posted_at      timestamptz NOT NULL DEFAULT now(),
    posted_by      bigint REFERENCES users (id),
    CONSTRAINT gl_day_posts_property_fk FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_day_posts_day_fk      FOREIGN KEY (property_id, business_date)   REFERENCES business_days (property_id, business_date),
    CONSTRAINT gl_day_posts_journal_fk  FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT gl_day_posts_date_uk     UNIQUE (property_id, business_date)
);
CREATE TRIGGER gl_day_posts_append_only BEFORE UPDATE OR DELETE ON gl_day_posts
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER gl_day_posts_no_truncate BEFORE TRUNCATE ON gl_day_posts
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Accounting periods are calendar months. A row exists once a month has been closed; reopening sets it back to OPEN.
CREATE TABLE gl_periods (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    period_start  date        NOT NULL,
    status        varchar(10) NOT NULL DEFAULT 'CLOSED',
    closed_at     timestamptz,
    closed_by     bigint REFERENCES users (id),
    reopened_at   timestamptz,
    reopened_by   bigint REFERENCES users (id),
    reopen_reason varchar(500),
    CONSTRAINT gl_periods_property_fk  FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_periods_start_uk     UNIQUE (property_id, period_start),
    CONSTRAINT gl_periods_status_ck    CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT gl_periods_first_day_ck CHECK (EXTRACT(day FROM period_start) = 1)
);
CREATE TRIGGER gl_periods_no_truncate BEFORE TRUNCATE ON gl_periods
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- How each folio ledger item reaches the general ledger. kind CHARGE: guest ledger against revenue, tax and service
-- charge. kind PAYMENT: a payment method against the guest ledger, or against advance deposits when is_deposit: a
-- payment (not on the city ledger) taken before the guest checked in, or while the stay has no folio yet. A refund is
-- judged by the payment it refunds, a reversal by the item it reverses.
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

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'JOURNAL', 'JV' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type = 'JOURNAL';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND'));
DROP VIEW IF EXISTS folio_item_gl;
DROP TABLE IF EXISTS gl_periods;
DROP TABLE IF EXISTS gl_day_posts;
DROP TABLE IF EXISTS gl_journal_lines;
DROP TABLE IF EXISTS gl_journals;
DROP FUNCTION IF EXISTS gl_journal_balance_check();
ALTER TABLE folios DROP COLUMN IF EXISTS closed_on;
