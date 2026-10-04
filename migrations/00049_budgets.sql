-- +goose Up
-- Budgets (design: docs/architecture/11-cashier-budget-cashflow-card.md, part B). A budget is the plan of the revenue and expenses of one fiscal year, a figure per
-- account and per month. A year may have several versions; one is ACTIVE. A DRAFT is edited, an ACTIVE one is fixed (a revision is a copy that becomes the
-- next version) and an ARCHIVED one, which an activation replaced, is only read. The fiscal year is its first day (gl_fiscal_years has a row only for a closed year).

CREATE TABLE budgets (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    year_start    date          NOT NULL,
    version       int           NOT NULL,
    name          varchar(100)  NOT NULL,
    description   varchar(500),
    status        varchar(10)   NOT NULL DEFAULT 'DRAFT',
    copied_from_id bigint,
    activated_at  timestamptz,
    activated_by  bigint REFERENCES users (id),
    approved_by   bigint REFERENCES users (id),
    archived_at   timestamptz,
    created_at    timestamptz   NOT NULL DEFAULT now(),
    created_by    bigint REFERENCES users (id),
    updated_at    timestamptz   NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    CONSTRAINT budgets_property_fk     FOREIGN KEY (tenant_id, property_id)          REFERENCES properties (tenant_id, id),
    CONSTRAINT budgets_copied_fk       FOREIGN KEY (property_id, copied_from_id)     REFERENCES budgets (property_id, id),
    CONSTRAINT budgets_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT budgets_version_uk      UNIQUE (property_id, year_start, version),
    CONSTRAINT budgets_name_ck         CHECK (length(btrim(name)) > 0),
    CONSTRAINT budgets_version_ck      CHECK (version >= 1),
    CONSTRAINT budgets_year_ck         CHECK (EXTRACT(day FROM year_start) = 1),
    CONSTRAINT budgets_status_ck       CHECK (status IN ('DRAFT', 'ACTIVE', 'ARCHIVED')),
    CONSTRAINT budgets_activation_ck   CHECK ((status = 'DRAFT') = (activated_at IS NULL) AND (status <> 'DRAFT' OR approved_by IS NULL) AND (status = 'DRAFT' OR approved_by IS NOT NULL)),
    CONSTRAINT budgets_archived_ck     CHECK ((status = 'ARCHIVED') = (archived_at IS NOT NULL))
);
-- One ACTIVE version per fiscal year.
CREATE UNIQUE INDEX budgets_active_uk ON budgets (property_id, year_start) WHERE status = 'ACTIVE';
CREATE INDEX budgets_year_idx ON budgets (property_id, year_start, version);
CREATE TRIGGER budgets_set_updated_at BEFORE UPDATE ON budgets FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The status only moves forward (DRAFT, ACTIVE, ARCHIVED); the year and the version never change; a DRAFT is the only budget that is renamed or deleted.
-- +goose StatementBegin
CREATE FUNCTION budgets_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status <> 'DRAFT' THEN
            RAISE EXCEPTION 'budget % is %: only a draft is deleted', OLD.id, OLD.status USING ERRCODE = 'restrict_violation', CONSTRAINT = 'budgets_delete_draft_only';
        END IF;
        RETURN OLD;
    END IF;
    IF NEW.year_start <> OLD.year_start OR NEW.version <> OLD.version OR NEW.property_id <> OLD.property_id THEN
        RAISE EXCEPTION 'the year and the version of budget % do not change', OLD.id USING ERRCODE = 'restrict_violation', CONSTRAINT = 'budgets_identity_fixed';
    END IF;
    IF OLD.status = 'DRAFT' AND NEW.status IN ('DRAFT', 'ACTIVE') THEN
        RETURN NEW;
    END IF;
    IF OLD.status = 'ACTIVE' AND NEW.status = 'ARCHIVED'
       AND (to_jsonb(NEW) - ARRAY['status', 'archived_at', 'updated_at', 'updated_by']) = (to_jsonb(OLD) - ARRAY['status', 'archived_at', 'updated_at', 'updated_by']) THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'budget % cannot change from % to %', OLD.id, OLD.status, NEW.status USING ERRCODE = 'restrict_violation', CONSTRAINT = 'budgets_status_flow';
END
$$;
-- +goose StatementEnd
CREATE TRIGGER budgets_guard BEFORE UPDATE OR DELETE ON budgets FOR EACH ROW EXECUTE FUNCTION budgets_guard();

CREATE TABLE budget_lines (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    budget_id   bigint        NOT NULL,
    account_id  bigint        NOT NULL,
    month       smallint      NOT NULL,
    amount      numeric(18,3) NOT NULL,
    CONSTRAINT bl_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT bl_budget_fk   FOREIGN KEY (property_id, budget_id)  REFERENCES budgets (property_id, id) ON DELETE CASCADE,
    CONSTRAINT bl_account_fk  FOREIGN KEY (property_id, account_id) REFERENCES gl_accounts (property_id, id),
    CONSTRAINT bl_cell_uk     UNIQUE (budget_id, account_id, month),
    CONSTRAINT bl_month_ck    CHECK (month BETWEEN 1 AND 12)
);
CREATE INDEX bl_account_idx ON budget_lines (property_id, account_id);

-- A line belongs to a DRAFT budget and to an account of revenue or expense that takes postings; only its amount changes.
-- +goose StatementBegin
CREATE FUNCTION budget_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_status varchar(10);
    v_type   varchar(20);
    v_post   boolean;
BEGIN
    SELECT status INTO v_status FROM budgets WHERE property_id = COALESCE(NEW.property_id, OLD.property_id) AND id = COALESCE(NEW.budget_id, OLD.budget_id);
    IF TG_OP = 'DELETE' THEN
        IF v_status IS NOT NULL AND v_status <> 'DRAFT' THEN -- no budget left: the budget itself is being deleted
            RAISE EXCEPTION 'budget % is %: its lines do not change', OLD.budget_id, v_status USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bl_draft_only';
        END IF;
        RETURN OLD;
    END IF;
    IF v_status IS NOT NULL AND v_status <> 'DRAFT' THEN
        RAISE EXCEPTION 'budget % is %: its lines do not change', NEW.budget_id, v_status USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bl_draft_only';
    END IF;
    IF TG_OP = 'UPDATE' AND (NEW.budget_id <> OLD.budget_id OR NEW.account_id <> OLD.account_id OR NEW.month <> OLD.month OR NEW.property_id <> OLD.property_id) THEN
        RAISE EXCEPTION 'only the amount of a budget line changes' USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bl_amount_only';
    END IF;
    SELECT account_type, is_postable INTO v_type, v_post FROM gl_accounts WHERE property_id = NEW.property_id AND id = NEW.account_id;
    IF v_type NOT IN ('REVENUE', 'EXPENSE') OR NOT v_post THEN
        RAISE EXCEPTION 'account % is not a revenue or expense account that takes postings', NEW.account_id USING ERRCODE = 'check_violation', CONSTRAINT = 'bl_account_type';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER budget_lines_guard BEFORE INSERT OR UPDATE OR DELETE ON budget_lines FOR EACH ROW EXECUTE FUNCTION budget_lines_guard();

-- +goose Down
DROP TABLE IF EXISTS budget_lines;
DROP FUNCTION IF EXISTS budget_lines_guard();
DROP TABLE IF EXISTS budgets;
DROP FUNCTION IF EXISTS budgets_guard();
