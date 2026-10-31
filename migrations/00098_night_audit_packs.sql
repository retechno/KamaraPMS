-- +goose Up
-- Night audit pack, PR 2 (docs/architecture/38-reports-night-audit-pack.md, 5, 8.1 and 10): the snapshot of the close of a business day.
--
--   nap_packs          one row for each (property, business date): the pack the night audit built when it closed the day, or a user built later from the ledger
--   nap_pack_sections  one row for each section of a pack (fifteen codes): its JSON, or the reason it failed, or the mark that it is off or not available
--
-- The recipients, the e-mail and the history (nap_recipients, the columns of email_outbox) are PR 3 (migration 00099).
--
-- Differences from the design text, recorded in 38 section 14 (as built, PR 2):
--   * nap_packs refuses a business day that is not CLOSED (a trigger): a pack is the record of a closed day;
--   * nap_packs.status may change (a rebuilt section can turn PARTIAL into COMPLETE); every other column of the pack is kept as it is;
--   * the columns of a section are kept as they are, except those of a FAILED section that a rebuild makes OK (a trigger); a section row never changes after that.

-- 1. The packs ---------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE nap_packs (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    business_date   date         NOT NULL,
    format_version  smallint     NOT NULL,
    status          varchar(10)  NOT NULL,
    built_at        timestamptz  NOT NULL,
    built_by        bigint REFERENCES users (id),
    build_ms        integer      NOT NULL,
    built_late      boolean      NOT NULL DEFAULT false,
    CONSTRAINT nap_packs_property_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT nap_packs_day_fk         FOREIGN KEY (property_id, business_date)   REFERENCES business_days (property_id, business_date),
    CONSTRAINT nap_packs_date_uk        UNIQUE (property_id, business_date),
    CONSTRAINT nap_packs_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT nap_packs_version_ck     CHECK (format_version >= 1),
    CONSTRAINT nap_packs_status_ck      CHECK (status IN ('COMPLETE', 'PARTIAL')),
    CONSTRAINT nap_packs_build_ms_ck    CHECK (build_ms >= 0)
);
CREATE INDEX nap_packs_status_idx ON nap_packs (property_id, status, business_date);
CREATE TRIGGER nap_packs_no_delete   BEFORE DELETE   ON nap_packs FOR EACH ROW       EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER nap_packs_no_truncate BEFORE TRUNCATE ON nap_packs FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A pack is the record of a closed day: the day is closed before the pack is made (in the same transaction of the night audit, or later).
-- +goose StatementBegin
CREATE FUNCTION nap_packs_day_closed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM business_days d WHERE d.property_id = NEW.property_id AND d.business_date = NEW.business_date AND d.status = 'CLOSED') THEN
        RAISE EXCEPTION 'the business day % is not closed: a night audit pack is made for a closed day', NEW.business_date
            USING ERRCODE = 'check_violation', CONSTRAINT = 'nap_packs_day_closed';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER nap_packs_day_closed BEFORE INSERT ON nap_packs FOR EACH ROW EXECUTE FUNCTION nap_packs_day_closed();

-- The status is the only thing of a pack that changes (a section that was rebuilt); the day, the version, the time and the user are the record.
-- +goose StatementBegin
CREATE FUNCTION nap_packs_status_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id OR NEW.property_id IS DISTINCT FROM OLD.property_id
       OR NEW.business_date IS DISTINCT FROM OLD.business_date OR NEW.format_version IS DISTINCT FROM OLD.format_version OR NEW.built_at IS DISTINCT FROM OLD.built_at
       OR NEW.built_by IS DISTINCT FROM OLD.built_by OR NEW.build_ms IS DISTINCT FROM OLD.build_ms OR NEW.built_late IS DISTINCT FROM OLD.built_late THEN
        RAISE EXCEPTION 'the night audit pack of % is kept as it is: only its status can change', OLD.business_date
            USING ERRCODE = 'check_violation', CONSTRAINT = 'nap_packs_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER nap_packs_status_only BEFORE UPDATE ON nap_packs FOR EACH ROW EXECUTE FUNCTION nap_packs_status_only();

-- 2. The sections ------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE nap_pack_sections (
    pack_id         bigint       NOT NULL,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    section         varchar(16)  NOT NULL,
    status          varchar(14)  NOT NULL,
    format_version  smallint     NOT NULL,
    data            jsonb,
    error           varchar(300),
    built_late      boolean      NOT NULL DEFAULT false,
    CONSTRAINT nap_pack_sections_pkey        PRIMARY KEY (pack_id, section),
    CONSTRAINT nap_pack_sections_pack_fk     FOREIGN KEY (property_id, pack_id) REFERENCES nap_packs (property_id, id),
    CONSTRAINT nap_pack_sections_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT nap_pack_sections_code_ck     CHECK (section IN ('SUMMARY', 'FLASH', 'REVENUE', 'TAX', 'PAYMENTS', 'Z_REPORTS', 'GUEST_LEDGER', 'DEPOSIT_LEDGER', 'CORRECTIONS',
                                                                'CANCELLATIONS', 'OUTLETS', 'OUT_OF_ORDER', 'IN_HOUSE', 'NEXT_DAY', 'AUDIT_NOTES')),
    CONSTRAINT nap_pack_sections_status_ck   CHECK (status IN ('OK', 'FAILED', 'OFF', 'NOT_AVAILABLE')),
    CONSTRAINT nap_pack_sections_version_ck  CHECK (format_version >= 1),
    CONSTRAINT nap_pack_sections_data_ck     CHECK ((status = 'OK') = (data IS NOT NULL)),
    CONSTRAINT nap_pack_sections_error_ck    CHECK ((status = 'FAILED') = (error IS NOT NULL))
);
CREATE INDEX nap_pack_sections_property_idx ON nap_pack_sections (property_id, pack_id);
CREATE TRIGGER nap_pack_sections_no_delete   BEFORE DELETE   ON nap_pack_sections FOR EACH ROW       EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER nap_pack_sections_no_truncate BEFORE TRUNCATE ON nap_pack_sections FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A section is the snapshot of what the close saw. The one change is a FAILED section that a rebuild makes OK (status, data, error, version, built_late).
-- +goose StatementBegin
CREATE FUNCTION nap_pack_sections_failed_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status <> 'FAILED' OR NEW.pack_id IS DISTINCT FROM OLD.pack_id OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
       OR NEW.property_id IS DISTINCT FROM OLD.property_id OR NEW.section IS DISTINCT FROM OLD.section THEN
        RAISE EXCEPTION 'the section % of a night audit pack is kept as it is: only a failed section can be built again', OLD.section
            USING ERRCODE = 'check_violation', CONSTRAINT = 'nap_pack_sections_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER nap_pack_sections_failed_only BEFORE UPDATE ON nap_pack_sections FOR EACH ROW EXECUTE FUNCTION nap_pack_sections_failed_only();

-- 3. The currency lock -------------------------------------------------------------------------------------------------------------------------------
-- The JSON of a pack holds money in the currency of the property: the currency cannot change once a pack exists (as for the other ledgers).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION property_has_financial_data(p_property_id bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM folio_items            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM payments               WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM gl_journals            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_bills         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_payments      WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_credit_notes  WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_receipts   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_invoices   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_adjustments WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM cashier_shifts         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM bank_statements        WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_returns            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_payments           WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_opening_credits    WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM budgets                WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM inv_movements          WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM fa_assets              WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM fa_ledger              WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM nap_packs              WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

-- +goose Down
-- Refused while a pack exists: a pack is the record of a closed day and cannot be made again for the days it covered (the in-house list, tomorrow, the notes and the blocks of that night are
-- not in the ledger). Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM nap_packs;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00098: % night audit pack(s) exist', v_n; END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION property_has_financial_data(p_property_id bigint) RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT EXISTS (SELECT 1 FROM folio_items            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM payments               WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM gl_journals            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_bills         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_payments      WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM supplier_credit_notes  WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_receipts   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_invoices   WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM city_ledger_adjustments WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM cashier_shifts         WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM bank_statements        WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_returns            WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_payments           WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM tax_opening_credits    WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM budgets                WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM inv_movements          WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM fa_assets              WHERE property_id = p_property_id)
        OR EXISTS (SELECT 1 FROM fa_ledger              WHERE property_id = p_property_id)
$$;
-- +goose StatementEnd

DROP TABLE nap_pack_sections;
DROP FUNCTION nap_pack_sections_failed_only();
DROP TABLE nap_packs;
DROP FUNCTION nap_packs_status_only();
DROP FUNCTION nap_packs_day_closed();
