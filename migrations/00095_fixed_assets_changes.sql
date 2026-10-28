-- +goose Up
-- Fixed assets, PR 3 (docs/architecture/37-fixed-assets.md, section 3.4): movements, revisions (life, residual value, method, value added, change of category), disposals and the system account of
-- the gain or loss on disposal.
--
--   fa_moves        where an asset stands and which department it belongs to, dated the business day, never backdated; the department of a month is the last move at or before its end (4.6)
--   fa_revisions    a change of life, residual value or method, a value added from a journal line, a change of category: old and new values, prospective, kept (4.5)
--   fa_disposals    the sale, the write-off and the partial disposal of a batch, with the journal that posted them and the approver (4.7)
--   fa_disposal_requests  a disposal that waits for the decision of an approver (the rule of the property asks for someone other than the person who asked): no journal and no ledger row until it is approved
--   fa_source_links.kind   a journal line that adds value to an asset is linked like a unit (kind CAPITALIZE), so the waiting list, the bill guard and the journal guard see it without a new rule
--   gl_account_map  the key ASSET_DISPOSAL: the account of the gain or loss on disposal (7420), which the cash flow moves from operating to investing (F10)
--   document_sequences  FA_DISPOSAL (FAD-)
--
-- Differences from the design text, recorded in 37-fixed-assets.md "As built: PR 3":
--   * fa_revisions has journal_line_id (the line that added the value, whatever its source) beside bill_id and bill_line_no, and an idempotency key;
--   * fa_moves has an idempotency key (a move of many assets shares one) and a reason; the database refuses a move dated before the asset's last one;
--   * fa_disposals has the time and the reason of its reversal and an idempotency key, and the database ties gain_loss to the proceeds and the carrying value removed;
--   * a disposal that needs another person's approval is a row of fa_disposal_requests (status PENDING, APPROVED or REJECTED) until it is decided, so fa_disposals keeps only disposals that were posted
--     (status POSTED or REVERSED, a journal always): the decision of the signed-in approver is the mode Decision of iam.Verify, as the count of the stock (35-inventory.md 9.2).

-- 1. A value added is a link too ---------------------------------------------------------------------------------------------------------------------
ALTER TABLE fa_source_links ADD COLUMN kind varchar(10) NOT NULL DEFAULT 'UNIT';
ALTER TABLE fa_source_links ADD CONSTRAINT fa_source_links_kind_ck CHECK (kind IN ('UNIT', 'CAPITALIZE'));
-- an asset has one line of its own (kind UNIT); the lines that added value to it are as many as there were
DROP INDEX fa_source_links_asset_uk;
CREATE UNIQUE INDEX fa_source_links_asset_uk ON fa_source_links (asset_id) WHERE released_at IS NULL AND kind = 'UNIT';

-- 2. Movements --------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_moves (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint       NOT NULL,
    property_id        bigint       NOT NULL,
    asset_id           bigint       NOT NULL,
    effective_on       date         NOT NULL,
    from_location_id   bigint,
    to_location_id     bigint,
    from_department_id bigint,
    to_department_id   bigint,
    reason             varchar(300) NOT NULL,
    idempotency_key    varchar(64),
    created_by         bigint REFERENCES users (id),
    created_at         timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT fa_moves_property_fk        FOREIGN KEY (tenant_id, property_id)            REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_moves_asset_fk           FOREIGN KEY (property_id, asset_id)             REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_moves_from_location_fk   FOREIGN KEY (property_id, from_location_id)     REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_moves_to_location_fk     FOREIGN KEY (property_id, to_location_id)       REFERENCES fa_locations (property_id, id),
    CONSTRAINT fa_moves_from_department_fk FOREIGN KEY (property_id, from_department_id)   REFERENCES departments (property_id, id),
    CONSTRAINT fa_moves_to_department_fk   FOREIGN KEY (property_id, to_department_id)     REFERENCES departments (property_id, id),
    CONSTRAINT fa_moves_reason_ck          CHECK (length(btrim(reason)) > 0),
    -- a move moves something: the location, the department or both
    CONSTRAINT fa_moves_change_ck          CHECK (from_location_id IS DISTINCT FROM to_location_id OR from_department_id IS DISTINCT FROM to_department_id)
);
CREATE INDEX fa_moves_asset_idx ON fa_moves (property_id, asset_id, effective_on, id);
CREATE INDEX fa_moves_date_idx  ON fa_moves (property_id, effective_on);
CREATE UNIQUE INDEX fa_moves_idempotency_uk ON fa_moves (property_id, idempotency_key, asset_id) WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER fa_moves_append_only BEFORE UPDATE OR DELETE ON fa_moves FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_moves_no_truncate BEFORE TRUNCATE ON fa_moves FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A move is dated the business day and is never backdated: the department of a month that has been run must not change under the run (4.6). The database refuses a move dated before the last one.
-- +goose StatementBegin
CREATE FUNCTION fa_moves_not_backdated() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM fa_moves m WHERE m.property_id = NEW.property_id AND m.asset_id = NEW.asset_id AND m.effective_on > NEW.effective_on) THEN
        RAISE EXCEPTION 'a move of asset % cannot be dated % : a later move exists', NEW.asset_id, NEW.effective_on
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_moves_not_backdated';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_moves_not_backdated BEFORE INSERT ON fa_moves FOR EACH ROW EXECUTE FUNCTION fa_moves_not_backdated();

-- 3. Revisions --------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_revisions (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    asset_id        bigint       NOT NULL,
    kind            varchar(12)  NOT NULL,
    effective_on    date         NOT NULL,
    old_value       jsonb        NOT NULL,
    new_value       jsonb        NOT NULL,
    bill_id         bigint,
    bill_line_no    int,
    journal_line_id bigint,
    journal_id      bigint,
    reason          varchar(300) NOT NULL,
    idempotency_key varchar(64),
    created_by      bigint REFERENCES users (id),
    created_at      timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT fa_revisions_property_fk     FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_revisions_asset_fk        FOREIGN KEY (property_id, asset_id)        REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_revisions_bill_line_fk    FOREIGN KEY (bill_id, bill_line_no)        REFERENCES supplier_bill_lines (bill_id, line_no),
    CONSTRAINT fa_revisions_line_fk         FOREIGN KEY (property_id, journal_line_id) REFERENCES gl_journal_lines (property_id, id),
    CONSTRAINT fa_revisions_journal_fk      FOREIGN KEY (property_id, journal_id)      REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_revisions_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT fa_revisions_kind_ck         CHECK (kind IN ('LIFE', 'RESIDUAL', 'METHOD', 'CAPITALIZE', 'CATEGORY')),
    CONSTRAINT fa_revisions_bill_ck         CHECK ((bill_id IS NULL) = (bill_line_no IS NULL)),
    -- value added comes from a journal line, and only value added does
    CONSTRAINT fa_revisions_capitalize_ck   CHECK ((kind = 'CAPITALIZE') = (journal_line_id IS NOT NULL)),
    CONSTRAINT fa_revisions_reason_ck       CHECK (length(btrim(reason)) > 0)
);
CREATE INDEX fa_revisions_asset_idx ON fa_revisions (property_id, asset_id, id);
CREATE UNIQUE INDEX fa_revisions_idempotency_uk ON fa_revisions (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER fa_revisions_append_only BEFORE UPDATE OR DELETE ON fa_revisions FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_revisions_no_truncate BEFORE TRUNCATE ON fa_revisions FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- 4. Disposals --------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_disposals (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    number              varchar(20)   NOT NULL,
    asset_id            bigint        NOT NULL,
    kind                varchar(10)   NOT NULL,
    disposal_date       date          NOT NULL,
    units               int           NOT NULL DEFAULT 1,
    cost_removed        numeric(18,3) NOT NULL,
    accum_removed       numeric(18,3) NOT NULL,
    carrying_removed    numeric(18,3) NOT NULL,
    proceeds            numeric(18,3) NOT NULL DEFAULT 0,
    proceeds_account_id bigint,
    gain_loss           numeric(18,3) NOT NULL,
    reason_code         varchar(10)   NOT NULL,
    note                varchar(500),
    journal_id          bigint        NOT NULL,
    approved_by         bigint REFERENCES users (id),
    status              varchar(8)    NOT NULL DEFAULT 'POSTED',
    reversal_journal_id bigint,
    reversed_by         bigint REFERENCES users (id),
    reversed_at         timestamptz,
    reversal_reason     varchar(500),
    idempotency_key     varchar(64),
    created_by          bigint REFERENCES users (id),
    created_at          timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT fa_disposals_property_fk    FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_disposals_asset_fk       FOREIGN KEY (property_id, asset_id)            REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_disposals_account_fk     FOREIGN KEY (property_id, proceeds_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT fa_disposals_journal_fk     FOREIGN KEY (property_id, journal_id)          REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_disposals_reversal_fk    FOREIGN KEY (property_id, reversal_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_disposals_number_uk      UNIQUE (property_id, number),
    CONSTRAINT fa_disposals_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT fa_disposals_kind_ck        CHECK (kind IN ('SALE', 'WRITE_OFF')),
    CONSTRAINT fa_disposals_reason_code_ck CHECK (reason_code IN ('SOLD', 'DAMAGED', 'LOST', 'OBSOLETE', 'DONATED', 'OTHER') AND ((kind = 'SALE') = (reason_code = 'SOLD'))),
    CONSTRAINT fa_disposals_units_ck       CHECK (units >= 1),
    CONSTRAINT fa_disposals_amounts_ck     CHECK (cost_removed > 0 AND accum_removed >= 0 AND accum_removed <= cost_removed AND proceeds >= 0),
    -- the carrying value removed and the gain or loss are what the other figures say, whatever the code does
    CONSTRAINT fa_disposals_carrying_ck    CHECK (carrying_removed = cost_removed - accum_removed),
    CONSTRAINT fa_disposals_gain_ck        CHECK (gain_loss = proceeds - carrying_removed),
    -- a sale is received into an account; a write-off brings in nothing
    CONSTRAINT fa_disposals_sale_ck        CHECK ((kind = 'SALE') = (proceeds_account_id IS NOT NULL) AND (kind = 'SALE' OR proceeds = 0)),
    CONSTRAINT fa_disposals_status_ck      CHECK (status IN ('POSTED', 'REVERSED')),
    CONSTRAINT fa_disposals_reversed_ck    CHECK ((status = 'REVERSED') = (reversal_journal_id IS NOT NULL AND reversed_at IS NOT NULL AND reversal_reason IS NOT NULL))
);
CREATE INDEX fa_disposals_asset_idx ON fa_disposals (property_id, asset_id, id);
CREATE INDEX fa_disposals_date_idx  ON fa_disposals (property_id, disposal_date);
CREATE UNIQUE INDEX fa_disposals_idempotency_uk ON fa_disposals (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER fa_disposals_no_truncate BEFORE TRUNCATE ON fa_disposals FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A disposal is never deleted and its facts never change; only the reversal of a posted one is written, once.
-- +goose StatementBegin
CREATE FUNCTION fa_disposals_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'fa_disposals rows are kept: only the reversal of a posted disposal is written'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_disposals_kept';
    END IF;
    IF OLD.status = 'REVERSED'
       OR NEW.property_id <> OLD.property_id OR NEW.asset_id <> OLD.asset_id OR NEW.number <> OLD.number OR NEW.kind <> OLD.kind OR NEW.disposal_date <> OLD.disposal_date OR NEW.units <> OLD.units
       OR NEW.cost_removed <> OLD.cost_removed OR NEW.accum_removed <> OLD.accum_removed OR NEW.proceeds <> OLD.proceeds OR NEW.gain_loss <> OLD.gain_loss OR NEW.journal_id <> OLD.journal_id THEN
        RAISE EXCEPTION 'fa_disposals rows are kept: only the reversal of a posted disposal is written'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_disposals_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_disposals_kept BEFORE UPDATE OR DELETE ON fa_disposals FOR EACH ROW EXECUTE FUNCTION fa_disposals_guard();

-- A request waits for an approver. It carries what the person asked; the amounts are worked out again when it is approved, with the asset locked. One request per asset waits at a time.
CREATE TABLE fa_disposal_requests (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    asset_id            bigint        NOT NULL,
    kind                varchar(10)   NOT NULL,
    disposal_date       date          NOT NULL,
    units               int           NOT NULL DEFAULT 1,
    proceeds            numeric(18,3) NOT NULL DEFAULT 0,
    proceeds_account_id bigint,
    reason_code         varchar(10)   NOT NULL,
    note                varchar(500),
    approval_amount     numeric(18,3) NOT NULL,
    status              varchar(8)    NOT NULL DEFAULT 'PENDING',
    requested_by        bigint REFERENCES users (id),
    requested_at        timestamptz   NOT NULL DEFAULT now(),
    decided_by          bigint REFERENCES users (id),
    decided_at          timestamptz,
    decision_note       varchar(500),
    disposal_id         bigint,
    idempotency_key     varchar(64),
    CONSTRAINT fa_disposal_requests_property_fk    FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_disposal_requests_asset_fk       FOREIGN KEY (property_id, asset_id)            REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_disposal_requests_account_fk     FOREIGN KEY (property_id, proceeds_account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT fa_disposal_requests_disposal_fk    FOREIGN KEY (property_id, disposal_id)         REFERENCES fa_disposals (property_id, id),
    CONSTRAINT fa_disposal_requests_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT fa_disposal_requests_kind_ck        CHECK (kind IN ('SALE', 'WRITE_OFF')),
    CONSTRAINT fa_disposal_requests_reason_code_ck CHECK (reason_code IN ('SOLD', 'DAMAGED', 'LOST', 'OBSOLETE', 'DONATED', 'OTHER') AND ((kind = 'SALE') = (reason_code = 'SOLD'))),
    CONSTRAINT fa_disposal_requests_units_ck       CHECK (units >= 1),
    CONSTRAINT fa_disposal_requests_proceeds_ck    CHECK (proceeds >= 0 AND approval_amount >= 0),
    CONSTRAINT fa_disposal_requests_sale_ck        CHECK ((kind = 'SALE') = (proceeds_account_id IS NOT NULL) AND (kind = 'SALE' OR proceeds = 0)),
    CONSTRAINT fa_disposal_requests_status_ck      CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED')),
    -- a decided request says who decided and when; an approved one points to the disposal it made, a rejected one has a note
    CONSTRAINT fa_disposal_requests_decided_ck     CHECK ((status = 'PENDING') = (decided_at IS NULL)),
    CONSTRAINT fa_disposal_requests_outcome_ck     CHECK ((status = 'APPROVED') = (disposal_id IS NOT NULL) AND (status <> 'REJECTED' OR (decision_note IS NOT NULL AND length(btrim(decision_note)) > 0)))
);
CREATE INDEX fa_disposal_requests_asset_idx ON fa_disposal_requests (property_id, asset_id, id);
CREATE INDEX fa_disposal_requests_status_idx ON fa_disposal_requests (property_id, status, id);
-- an asset has one request waiting at a time
CREATE UNIQUE INDEX fa_disposal_requests_pending_uk ON fa_disposal_requests (property_id, asset_id) WHERE status = 'PENDING';
CREATE UNIQUE INDEX fa_disposal_requests_idempotency_uk ON fa_disposal_requests (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TRIGGER fa_disposal_requests_no_delete BEFORE DELETE ON fa_disposal_requests FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_disposal_requests_no_truncate BEFORE TRUNCATE ON fa_disposal_requests FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- 5. The sequence of the disposals ------------------------------------------------------------------------------------------------------------------
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT',
                             'FIXED_ASSET', 'FA_OPENING', 'FA_DISPOSAL'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'FA_DISPOSAL', 'FAD-' FROM properties;

-- 6. The system account of the gain or loss on disposal ----------------------------------------------------------------------------------------------
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER', 'COMP',
                                                             'INV_GRNI', 'INV_PPV', 'INV_WASTE', 'INV_COUNT_VARIANCE', 'INV_COGS_FOOD', 'INV_COGS_BEVERAGE', 'INV_STAFF_MEAL', 'INV_ENTERTAINMENT',
                                                             'ASSET_DISPOSAL'));
-- the key points at 7420 "Gain or loss on disposal of assets" where the property has it as the standard chart does; the others choose the account in the account map
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id)
SELECT a.tenant_id, a.property_id, 'ASSET_DISPOSAL', a.id
  FROM gl_accounts a
 WHERE a.code = '7420' AND a.account_type = 'EXPENSE' AND a.is_postable
ON CONFLICT (property_id, map_key) DO NOTHING;

-- +goose Down
-- Refused while the register holds what cannot be rebuilt: a move, a revision or a disposal. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM fa_moves) + (SELECT count(*) FROM fa_revisions) + (SELECT count(*) FROM fa_disposals) + (SELECT count(*) FROM fa_disposal_requests) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00095: the fixed assets module holds % row(s) (moves, revisions, disposals)', v_n; END IF;
END
$$;
-- +goose StatementEnd

DELETE FROM gl_account_map WHERE map_key = 'ASSET_DISPOSAL';
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER', 'COMP',
                                                             'INV_GRNI', 'INV_PPV', 'INV_WASTE', 'INV_COUNT_VARIANCE', 'INV_COGS_FOOD', 'INV_COGS_BEVERAGE', 'INV_STAFF_MEAL', 'INV_ENTERTAINMENT'));

DELETE FROM document_sequences WHERE sequence_type = 'FA_DISPOSAL';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT',
                             'FIXED_ASSET', 'FA_OPENING'));

DROP TABLE fa_disposal_requests;
DROP TABLE fa_disposals;
DROP FUNCTION fa_disposals_guard();
DROP TABLE fa_revisions;
DROP TABLE fa_moves;
DROP FUNCTION fa_moves_not_backdated();

DROP INDEX fa_source_links_asset_uk;
CREATE UNIQUE INDEX fa_source_links_asset_uk ON fa_source_links (asset_id) WHERE released_at IS NULL;
ALTER TABLE fa_source_links DROP CONSTRAINT fa_source_links_kind_ck;
ALTER TABLE fa_source_links DROP COLUMN kind;
