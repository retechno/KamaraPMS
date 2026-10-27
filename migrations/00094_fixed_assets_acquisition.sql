-- +goose Up
-- Fixed assets, PR 2 (docs/architecture/37-fixed-assets.md, section 3.3): the acquisition from a journal line (the waiting list, "make asset", "not an asset"), the monthly depreciation (runs and lines)
-- and the line number of a bill on its journal line.
--
--   gl_journal_lines.source_line   the number of the bill line (supplier_bill_lines.line_no) a cost line of a bill journal came from; NULL for every other line (F7, F8)
--   fa_source_links                one row for each unit made from a journal line; the live units of a line add up to its debit (deferred trigger)
--   fa_source_dismissals           "this line is not an asset"; kept when restored
--   fa_depr_runs, fa_depr_lines    the monthly run (one live run for a month) and its lines (append-only)
--
-- Differences from the design text, recorded in 37-fixed-assets.md "As built: PR 2":
--   * the table of the dismissals is fa_source_dismissals (as in 3.3), and it has an id and who restored the line;
--   * gl_journal_lines.source_line is new: a bill journal named the bill (source_ref) but not the line, so a journal line could not be traced to its bill line;
--   * fa_source_links.idempotency_key holds the Idempotency-Key of "make asset" (all the units of one request share it);
--   * the DEPRECIATE row of fa_ledger a line wrote has source_type 'FA_DEPR' and source_id = the id of the line, which is what a reversal of the run negates.

-- 1. The bill line of a journal line ---------------------------------------------------------------------------------------------------------------
ALTER TABLE gl_journal_lines ADD COLUMN source_line int;
ALTER TABLE gl_journal_lines ADD CONSTRAINT gl_journal_lines_source_line_ck CHECK (source_line IS NULL OR (source_line > 0 AND COALESCE(source_type, '') = 'AP_BILL'));
ALTER TABLE gl_journal_lines ADD CONSTRAINT gl_journal_lines_property_id_uk UNIQUE (property_id, id);

-- The bills posted before this migration: the cost lines of a bill journal are the debit lines that are not the VAT line ("VAT ..."), in the order of the bill lines. They are numbered only when
-- their count is that of the bill lines; else the line stays NULL and the guard works at the bill (BILL_LINE_IS_ASSET names the bill).
ALTER TABLE gl_journal_lines DISABLE TRIGGER gl_journal_lines_append_only;
UPDATE gl_journal_lines l
   SET source_line = x.n
  FROM (SELECT l2.id, row_number() OVER (PARTITION BY l2.journal_id ORDER BY l2.line_no) AS n, b.id AS bill_id
          FROM gl_journal_lines l2
          JOIN supplier_bills b ON b.property_id = l2.property_id AND b.journal_id = l2.journal_id
         WHERE l2.source_type = 'AP_BILL' AND l2.debit > 0 AND COALESCE(l2.description, '') NOT LIKE 'VAT %') x
 WHERE l.id = x.id
   AND (SELECT count(*) FROM supplier_bill_lines sl WHERE sl.bill_id = x.bill_id)
     = (SELECT count(*) FROM gl_journal_lines l3 WHERE l3.journal_id = l.journal_id AND l3.source_type = 'AP_BILL' AND l3.debit > 0 AND COALESCE(l3.description, '') NOT LIKE 'VAT %');
ALTER TABLE gl_journal_lines ENABLE TRIGGER gl_journal_lines_append_only;

-- 2. The units made from a journal line ------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_source_links (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    journal_line_id bigint        NOT NULL,
    unit_no         int           NOT NULL,
    asset_id        bigint        NOT NULL,
    cost            numeric(18,3) NOT NULL,
    bill_id         bigint,
    bill_line_no    int,
    idempotency_key varchar(64),
    released_at     timestamptz,
    released_by     bigint REFERENCES users (id),
    created_at      timestamptz   NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    CONSTRAINT fa_source_links_property_fk  FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_source_links_line_fk      FOREIGN KEY (property_id, journal_line_id) REFERENCES gl_journal_lines (property_id, id),
    CONSTRAINT fa_source_links_asset_fk     FOREIGN KEY (property_id, asset_id)        REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_source_links_bill_line_fk FOREIGN KEY (bill_id, bill_line_no)        REFERENCES supplier_bill_lines (bill_id, line_no),
    CONSTRAINT fa_source_links_unit_ck      CHECK (unit_no >= 1),
    CONSTRAINT fa_source_links_cost_ck      CHECK (cost > 0),
    CONSTRAINT fa_source_links_bill_ck      CHECK ((bill_id IS NULL) = (bill_line_no IS NULL))
);
CREATE UNIQUE INDEX fa_source_links_unit_uk        ON fa_source_links (journal_line_id, unit_no) WHERE released_at IS NULL;
CREATE UNIQUE INDEX fa_source_links_asset_uk       ON fa_source_links (asset_id) WHERE released_at IS NULL;
CREATE UNIQUE INDEX fa_source_links_idempotency_uk ON fa_source_links (property_id, idempotency_key, unit_no) WHERE idempotency_key IS NOT NULL;
CREATE INDEX fa_source_links_line_idx ON fa_source_links (property_id, journal_line_id);
CREATE INDEX fa_source_links_bill_idx ON fa_source_links (bill_id) WHERE bill_id IS NOT NULL AND released_at IS NULL;

-- A link is never deleted and its facts never change: only the release can be written, once.
-- +goose StatementBegin
CREATE FUNCTION fa_source_links_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'fa_source_links rows are kept: only the release of a live one is written'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_source_links_kept';
    END IF;
    IF OLD.released_at IS NOT NULL
       OR NEW.journal_line_id <> OLD.journal_line_id OR NEW.unit_no <> OLD.unit_no OR NEW.asset_id <> OLD.asset_id OR NEW.cost <> OLD.cost
       OR NEW.property_id <> OLD.property_id OR NEW.bill_id IS DISTINCT FROM OLD.bill_id OR NEW.bill_line_no IS DISTINCT FROM OLD.bill_line_no THEN
        RAISE EXCEPTION 'fa_source_links rows are kept: only the release of a live one is written'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_source_links_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_source_links_kept BEFORE UPDATE OR DELETE ON fa_source_links FOR EACH ROW EXECUTE FUNCTION fa_source_links_guard();
CREATE TRIGGER fa_source_links_no_truncate BEFORE TRUNCATE ON fa_source_links FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The live units of a journal line add up to its debit, whatever the code does (F7). Checked at commit, so the units are inserted one by one.
-- +goose StatementBegin
CREATE FUNCTION fa_source_links_sum_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_sum   numeric;
    v_count bigint;
    v_debit numeric;
BEGIN
    SELECT COALESCE(sum(cost), 0), count(*) INTO v_sum, v_count FROM fa_source_links WHERE journal_line_id = NEW.journal_line_id AND released_at IS NULL;
    IF v_count > 0 THEN
        SELECT debit INTO v_debit FROM gl_journal_lines WHERE id = NEW.journal_line_id;
        IF v_sum <> v_debit THEN
            RAISE EXCEPTION 'the units made from journal line % cost % but the line debits %', NEW.journal_line_id, v_sum, v_debit
                USING ERRCODE = 'check_violation', CONSTRAINT = 'fa_source_links_sum';
        END IF;
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER fa_source_links_sum_check AFTER INSERT OR UPDATE ON fa_source_links
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION fa_source_links_sum_check();

-- 3. "This line is not an asset" -------------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_source_dismissals (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    journal_line_id bigint       NOT NULL,
    reason          varchar(300) NOT NULL,
    dismissed_by    bigint REFERENCES users (id),
    dismissed_at    timestamptz  NOT NULL DEFAULT now(),
    restored_at     timestamptz,
    restored_by     bigint REFERENCES users (id),
    CONSTRAINT fa_source_dismissals_property_fk FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_source_dismissals_line_fk     FOREIGN KEY (property_id, journal_line_id) REFERENCES gl_journal_lines (property_id, id),
    CONSTRAINT fa_source_dismissals_reason_ck   CHECK (length(btrim(reason)) > 0)
);
CREATE UNIQUE INDEX fa_source_dismissals_live_uk ON fa_source_dismissals (journal_line_id) WHERE restored_at IS NULL;
CREATE INDEX fa_source_dismissals_line_idx ON fa_source_dismissals (property_id, journal_line_id);
CREATE TRIGGER fa_source_dismissals_no_truncate BEFORE TRUNCATE ON fa_source_dismissals FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- 4. The monthly run and its lines -----------------------------------------------------------------------------------------------------------------
CREATE TABLE fa_depr_runs (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    period_month        date          NOT NULL,
    status              varchar(8)    NOT NULL DEFAULT 'POSTED',
    journal_id          bigint        NOT NULL,
    reversal_journal_id bigint,
    total               numeric(18,3) NOT NULL,
    asset_count         int           NOT NULL,
    idempotency_key     varchar(64),
    run_by              bigint REFERENCES users (id),
    run_at              timestamptz   NOT NULL DEFAULT now(),
    reversed_by         bigint REFERENCES users (id),
    reversed_at         timestamptz,
    reversal_reason     varchar(500),
    CONSTRAINT fa_depr_runs_property_fk    FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_depr_runs_journal_fk     FOREIGN KEY (property_id, journal_id)          REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_depr_runs_reversal_fk    FOREIGN KEY (property_id, reversal_journal_id) REFERENCES gl_journals (property_id, id),
    CONSTRAINT fa_depr_runs_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT fa_depr_runs_month_ck       CHECK (period_month = date_trunc('month', period_month)::date),
    CONSTRAINT fa_depr_runs_status_ck      CHECK (status IN ('POSTED', 'REVERSED')),
    CONSTRAINT fa_depr_runs_reversed_ck    CHECK ((status = 'REVERSED') = (reversal_journal_id IS NOT NULL AND reversed_at IS NOT NULL AND reversal_reason IS NOT NULL)),
    CONSTRAINT fa_depr_runs_totals_ck      CHECK (total >= 0 AND asset_count >= 0)
);
-- one live run for a month: the backstop of two runs at once
CREATE UNIQUE INDEX fa_depr_runs_live_uk        ON fa_depr_runs (property_id, period_month) WHERE status = 'POSTED';
CREATE UNIQUE INDEX fa_depr_runs_idempotency_uk ON fa_depr_runs (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX fa_depr_runs_month_idx ON fa_depr_runs (property_id, period_month);
CREATE TRIGGER fa_depr_runs_no_truncate BEFORE TRUNCATE ON fa_depr_runs FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A run is never deleted; only its reversal is written, once.
-- +goose StatementBegin
CREATE FUNCTION fa_depr_runs_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'fa_depr_runs rows are kept: only the reversal of a posted run is written'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_depr_runs_kept';
    END IF;
    IF OLD.status = 'REVERSED'
       OR NEW.period_month <> OLD.period_month OR NEW.journal_id <> OLD.journal_id OR NEW.total <> OLD.total OR NEW.asset_count <> OLD.asset_count OR NEW.property_id <> OLD.property_id THEN
        RAISE EXCEPTION 'fa_depr_runs rows are kept: only the reversal of a posted run is written'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'fa_depr_runs_kept';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER fa_depr_runs_kept BEFORE UPDATE OR DELETE ON fa_depr_runs FOR EACH ROW EXECUTE FUNCTION fa_depr_runs_guard();

CREATE TABLE fa_depr_lines (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    run_id             bigint        NOT NULL,
    asset_id           bigint        NOT NULL,
    category_id        bigint        NOT NULL,
    expense_account_id bigint        NOT NULL,
    department_id      bigint,
    months             int           NOT NULL,
    from_month         date          NOT NULL,
    amount             numeric(18,3) NOT NULL,
    accum_after        numeric(18,3) NOT NULL,
    carrying_after     numeric(18,3) NOT NULL,
    months_done_after  int           NOT NULL,
    CONSTRAINT fa_depr_lines_property_fk   FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT fa_depr_lines_run_fk        FOREIGN KEY (property_id, run_id)              REFERENCES fa_depr_runs (property_id, id),
    CONSTRAINT fa_depr_lines_asset_fk      FOREIGN KEY (property_id, asset_id)            REFERENCES fa_assets (property_id, id),
    CONSTRAINT fa_depr_lines_category_fk   FOREIGN KEY (property_id, category_id)         REFERENCES fa_categories (property_id, id),
    CONSTRAINT fa_depr_lines_expense_fk    FOREIGN KEY (property_id, expense_account_id)  REFERENCES gl_accounts (property_id, id),
    CONSTRAINT fa_depr_lines_department_fk FOREIGN KEY (property_id, department_id)       REFERENCES departments (property_id, id),
    CONSTRAINT fa_depr_lines_run_asset_uk  UNIQUE (run_id, asset_id),
    CONSTRAINT fa_depr_lines_months_ck     CHECK (months > 0),
    CONSTRAINT fa_depr_lines_from_ck       CHECK (from_month = date_trunc('month', from_month)::date),
    CONSTRAINT fa_depr_lines_amounts_ck    CHECK (amount >= 0 AND accum_after >= 0 AND carrying_after >= 0 AND months_done_after >= months)
);
CREATE INDEX fa_depr_lines_asset_idx ON fa_depr_lines (property_id, asset_id);
CREATE TRIGGER fa_depr_lines_append_only BEFORE UPDATE OR DELETE ON fa_depr_lines FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER fa_depr_lines_no_truncate BEFORE TRUNCATE ON fa_depr_lines FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- +goose Down
-- Refused while the register holds what cannot be rebuilt: a unit made from a journal line, a line marked "not an asset" or a run of depreciation. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM fa_source_links) + (SELECT count(*) FROM fa_source_dismissals) + (SELECT count(*) FROM fa_depr_runs) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00094: the fixed assets module holds % row(s) (units made from journal lines, dismissals, depreciation runs)', v_n; END IF;
END
$$;
-- +goose StatementEnd
DROP TABLE IF EXISTS fa_depr_lines;
DROP TABLE IF EXISTS fa_depr_runs;
DROP FUNCTION IF EXISTS fa_depr_runs_guard();
DROP TABLE IF EXISTS fa_source_dismissals;
DROP TABLE IF EXISTS fa_source_links;
DROP FUNCTION IF EXISTS fa_source_links_sum_check();
DROP FUNCTION IF EXISTS fa_source_links_guard();
ALTER TABLE gl_journal_lines DROP CONSTRAINT IF EXISTS gl_journal_lines_property_id_uk;
ALTER TABLE gl_journal_lines DROP CONSTRAINT IF EXISTS gl_journal_lines_source_line_ck;
ALTER TABLE gl_journal_lines DISABLE TRIGGER gl_journal_lines_append_only;
ALTER TABLE gl_journal_lines DROP COLUMN IF EXISTS source_line;
ALTER TABLE gl_journal_lines ENABLE TRIGGER gl_journal_lines_append_only;
