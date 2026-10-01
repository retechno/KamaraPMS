-- +goose Up
-- Year-end closing: a closing journal moves the result of a fiscal year from the revenue and expense accounts to retained
-- earnings, so the income statement of the next year starts from zero. The closing journal (and the reversal of one)
-- is flagged is_closing: the income statement leaves such journals out, the balance sheet and the ledgers keep them.

-- The account the result of a year is closed to.
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS'));
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id)
SELECT a.tenant_id, a.property_id, 'RETAINED_EARNINGS', a.id
  FROM gl_accounts a
 WHERE a.code = '3200' AND EXISTS (SELECT 1 FROM accounting_settings s WHERE s.property_id = a.property_id)
ON CONFLICT (property_id, map_key) DO NOTHING;

ALTER TABLE gl_journals ADD COLUMN is_closing boolean NOT NULL DEFAULT false;
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL', 'CLOSING'));
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_closing_ck CHECK (journal_type <> 'CLOSING' OR is_closing);

-- A fiscal year closes once all its months are closed. Reopening reverses the closing journal and sets the year OPEN
-- again; closing it again makes a new journal. The row of a year that was never closed does not exist.
CREATE TABLE gl_fiscal_years (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint      NOT NULL,
    property_id          bigint      NOT NULL,
    year_start           date        NOT NULL,
    year_end             date        NOT NULL,
    status               varchar(10) NOT NULL DEFAULT 'CLOSED',
    closing_journal_id   bigint,
    reversal_journal_id  bigint,
    closed_at            timestamptz,
    closed_by            bigint REFERENCES users (id),
    reopened_at          timestamptz,
    reopened_by          bigint REFERENCES users (id),
    reopen_reason        varchar(500),
    approved_by          bigint REFERENCES users (id),
    CONSTRAINT gl_fiscal_years_property_fk  FOREIGN KEY (tenant_id, property_id)               REFERENCES properties (tenant_id, id),
    CONSTRAINT gl_fiscal_years_closing_fk   FOREIGN KEY (property_id, closing_journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT gl_fiscal_years_reversal_fk  FOREIGN KEY (property_id, reversal_journal_id)     REFERENCES gl_journals (property_id, id),
    CONSTRAINT gl_fiscal_years_start_uk     UNIQUE (property_id, year_start),
    CONSTRAINT gl_fiscal_years_status_ck    CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT gl_fiscal_years_range_ck     CHECK (year_end > year_start AND EXTRACT(day FROM year_start) = 1)
);
CREATE TRIGGER gl_fiscal_years_no_truncate BEFORE TRUNCATE ON gl_fiscal_years
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- +goose Down
DROP TABLE IF EXISTS gl_fiscal_years;
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_closing_ck;
ALTER TABLE gl_journals DROP CONSTRAINT gl_journals_type_ck;
ALTER TABLE gl_journals ADD CONSTRAINT gl_journals_type_ck CHECK (journal_type IN ('DAY_CLOSE', 'MANUAL', 'REVERSAL'));
ALTER TABLE gl_journals DROP COLUMN is_closing;
DELETE FROM gl_account_map WHERE map_key = 'RETAINED_EARNINGS';
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE'));
