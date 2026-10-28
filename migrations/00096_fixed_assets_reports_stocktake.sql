-- +goose Up
-- Fixed assets, PR 4 (docs/architecture/37-fixed-assets.md, section 3.5): the stock-take on a phone and the link from a Maintenance request to an asset.
--
--   fa_audits       a stock-take of one location or of the whole property: IN_PROGRESS while people scan, CLOSED when it has written its proposals (4.10)
--   fa_audit_lines  one row for an asset in an audit: a second scan is the same row (idempotent); an asset not scanned becomes MISSING when the audit is closed
--   fa_proposals    what a closed audit proposes: move an asset to the place it was found, or write off what is missing or damaged; a person decides each one (no journal here)
--   maintenance_requests.asset_id   a request may name the asset it is about (4.11)
--   document_sequences  FA_AUDIT (FAO-)
--
-- The fiscal book has no table of results (4.9): it is a pure function of the asset, its layers and fa_fiscal_groups, which migration 00093 created because the opening import needs the groups.
--
-- Differences from the design text, recorded in 37-fixed-assets.md "As built: PR 4":
--   * fa_fiscal_groups already exists (00093), so it is not created here;
--   * fa_audit_lines carries the tenant and the property, and a proposal points to its line by (property_id, audit_id, asset_id), so a row of another property cannot be tied to an audit;
--   * a proposal has a decision note and says what the decision made (result_type and result_id), not one opaque reference; one proposal per asset per audit;
--   * fa_audits has an idempotency key and at most one audit of a location is IN_PROGRESS at a time (and one of the whole property);
--   * a closed audit and its lines are kept: the database refuses to change them.

-- 1. The audits --------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_audits (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    number          varchar(20)  NOT NULL,
    location_id     bigint,
    status          varchar(12)  NOT NULL DEFAULT 'IN_PROGRESS',
    started_by      bigint REFERENCES users (id),
    started_at      timestamptz  NOT NULL DEFAULT now(),
    closed_by       bigint REFERENCES users (id),
    closed_at       timestamptz,
    note            varchar(500),
    idempotency_key varchar(64),
    CONSTRAINT fa_audits_property_fk    FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_audits_location_fk    FOREIGN KEY (property_id, location_id) REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_audits_number_uk      UNIQUE (property_id, number),
    CONSTRAINT fa_audits_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT fa_audits_status_ck      CHECK (status IN ('IN_PROGRESS', 'CLOSED')),
    CONSTRAINT fa_audits_closed_ck      CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL))
);
CREATE INDEX fa_audits_status_idx ON fa_audits (property_id, status, id);
-- one audit of a place is open at a time: two people counting the same room would write two answers
CREATE UNIQUE INDEX fa_audits_open_uk ON fa_audits (property_id, COALESCE(location_id, 0)) WHERE status = 'IN_PROGRESS';
CREATE UNIQUE INDEX fa_audits_idempotency_uk ON fa_audits (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER fa_audits_no_delete   BEFORE DELETE   ON fa_audits FOR EACH ROW       EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_audits_no_truncate BEFORE TRUNCATE ON fa_audits FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A closed audit never changes: it is the record of what was counted.
-- +goose StatementBegin
CREATE FUNCTION fa_audits_closed_is_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status = 'CLOSED' THEN
        RAISE EXCEPTION 'audit % is closed and is kept as it is', OLD.number
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_audits_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_audits_closed_is_kept BEFORE UPDATE ON fa_audits FOR EACH ROW EXECUTE FUNCTION fa_audits_closed_is_kept();

-- 2. The lines ---------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_audit_lines (
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    audit_id             bigint       NOT NULL,
    asset_id             bigint       NOT NULL,
    expected_location_id bigint,
    result               varchar(10)  NOT NULL,
    found_location_id    bigint,
    note                 varchar(500),
    scanned_at           timestamptz,
    scanned_by           bigint REFERENCES users (id),
    PRIMARY KEY (audit_id, asset_id),
    CONSTRAINT fa_audit_lines_audit_fk    FOREIGN KEY (property_id, audit_id)             REFERENCES fa_audits (property_id, id),
    CONSTRAINT fa_audit_lines_asset_fk    FOREIGN KEY (property_id, asset_id)             REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_audit_lines_expected_fk FOREIGN KEY (property_id, expected_location_id) REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_audit_lines_found_fk    FOREIGN KEY (property_id, found_location_id)    REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_audit_lines_property_uk UNIQUE (property_id, audit_id, asset_id),
    CONSTRAINT fa_audit_lines_result_ck   CHECK (result IN ('FOUND', 'MISSING', 'DAMAGED', 'MOVED', 'UNEXPECTED')),
    -- an asset that was not scanned is missing, and only that one; a scan says who and when
    CONSTRAINT fa_audit_lines_scan_ck     CHECK ((result = 'MISSING') = (scanned_at IS NULL)),
    -- an asset found elsewhere says where; a damaged one says what is wrong
    CONSTRAINT fa_audit_lines_moved_ck    CHECK (result NOT IN ('MOVED', 'UNEXPECTED') OR found_location_id IS NOT NULL),
    CONSTRAINT fa_audit_lines_damaged_ck  CHECK (result <> 'DAMAGED' OR (note IS NOT NULL AND length(btrim(note)) > 0))
);
CREATE INDEX fa_audit_lines_asset_idx ON fa_audit_lines (property_id, asset_id);

-- The lines of an audit that is closed are kept; a line is written while the audit is open (the close writes the missing ones before it flips the status).
-- +goose StatementBegin
CREATE FUNCTION fa_audit_lines_open_only() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM fa_audits a WHERE a.property_id = NEW.property_id AND a.id = NEW.audit_id AND a.status = 'IN_PROGRESS') THEN
        RAISE EXCEPTION 'the audit % is not open: its lines are kept', NEW.audit_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_audit_lines_open_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_audit_lines_open_only BEFORE INSERT OR UPDATE ON fa_audit_lines FOR EACH ROW EXECUTE FUNCTION fa_audit_lines_open_only();
CREATE TRIGGER fa_audit_lines_no_delete   BEFORE DELETE   ON fa_audit_lines FOR EACH ROW       EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_audit_lines_no_truncate BEFORE TRUNCATE ON fa_audit_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- 3. The proposals -----------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_proposals (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint       NOT NULL,
    property_id    bigint       NOT NULL,
    audit_id       bigint       NOT NULL,
    asset_id       bigint       NOT NULL,
    kind           varchar(10)  NOT NULL,
    to_location_id bigint,
    status         varchar(8)   NOT NULL DEFAULT 'PENDING',
    decided_by     bigint REFERENCES users (id),
    decided_at     timestamptz,
    decision_note  varchar(500),
    result_type    varchar(20),
    result_id      bigint,
    created_at     timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT fa_proposals_property_fk    FOREIGN KEY (tenant_id, property_id)                 REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_proposals_line_fk        FOREIGN KEY (property_id, audit_id, asset_id)        REFERENCES fa_audit_lines (property_id, audit_id, asset_id),
    CONSTRAINT fa_proposals_location_fk    FOREIGN KEY (property_id, to_location_id)            REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_proposals_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT fa_proposals_asset_uk       UNIQUE (audit_id, asset_id),
    CONSTRAINT fa_proposals_kind_ck        CHECK (kind IN ('MOVE', 'WRITE_OFF')),
    CONSTRAINT fa_proposals_status_ck      CHECK (status IN ('PENDING', 'APPLIED', 'REJECTED')),
    CONSTRAINT fa_proposals_move_ck        CHECK ((kind = 'MOVE') = (to_location_id IS NOT NULL)),
    CONSTRAINT fa_proposals_decided_ck     CHECK ((status = 'PENDING') = (decided_at IS NULL)),
    CONSTRAINT fa_proposals_outcome_ck     CHECK ((status = 'APPLIED') = (result_id IS NOT NULL AND result_type IS NOT NULL)
                                                  AND (status <> 'REJECTED' OR (decision_note IS NOT NULL AND length(btrim(decision_note)) > 0))),
    CONSTRAINT fa_proposals_result_type_ck CHECK (result_type IS NULL OR result_type IN ('MOVE', 'DISPOSAL', 'DISPOSAL_REQUEST'))
);
CREATE INDEX fa_proposals_status_idx ON fa_proposals (property_id, status, id);
CREATE INDEX fa_proposals_asset_idx  ON fa_proposals (property_id, asset_id);
CREATE TRIGGER fa_proposals_no_delete   BEFORE DELETE   ON fa_proposals FOR EACH ROW       EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_proposals_no_truncate BEFORE TRUNCATE ON fa_proposals FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A proposal that was decided is kept as it was decided.
-- +goose StatementBegin
CREATE FUNCTION fa_proposals_decided_is_kept() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status <> 'PENDING' THEN
        RAISE EXCEPTION 'proposal % was decided and is kept as it is', OLD.id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_proposals_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_proposals_decided_is_kept BEFORE UPDATE ON fa_proposals FOR EACH ROW EXECUTE FUNCTION fa_proposals_decided_is_kept();

-- 4. A request of Maintenance may name an asset -------------------------------------------------------------------------------------------------------
ALTER TABLE maintenance_requests ADD COLUMN asset_id bigint;
ALTER TABLE maintenance_requests ADD CONSTRAINT mr_asset_fk FOREIGN KEY (property_id, asset_id) REFERENCES fa_assets (property_id, id);
CREATE INDEX mr_asset_idx ON maintenance_requests (property_id, asset_id) WHERE asset_id IS NOT NULL;

-- 5. The sequence of the stock-takes -----------------------------------------------------------------------------------------------------------------
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT',
                             'FIXED_ASSET', 'FA_OPENING', 'FA_DISPOSAL', 'FA_AUDIT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'FA_AUDIT', 'FAO-' FROM properties;

-- +goose Down
-- Refused while the register holds what cannot be rebuilt: a stock-take, a proposal, or a request that names an asset. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM fa_audits) + (SELECT count(*) FROM fa_audit_lines) + (SELECT count(*) FROM fa_proposals)
         + (SELECT count(*) FROM maintenance_requests WHERE asset_id IS NOT NULL) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00096: the fixed assets module holds % row(s) (stock-takes, proposals, requests tied to an asset)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DELETE FROM document_sequences WHERE sequence_type = 'FA_AUDIT';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT',
                             'FIXED_ASSET', 'FA_OPENING', 'FA_DISPOSAL'));

DROP INDEX mr_asset_idx;
ALTER TABLE maintenance_requests DROP CONSTRAINT mr_asset_fk;
ALTER TABLE maintenance_requests DROP COLUMN asset_id;

DROP TABLE fa_proposals;
DROP FUNCTION fa_proposals_decided_is_kept();
DROP TABLE fa_audit_lines;
DROP FUNCTION fa_audit_lines_open_only();
DROP TABLE fa_audits;
DROP FUNCTION fa_audits_closed_is_kept();
