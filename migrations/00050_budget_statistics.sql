-- +goose Up
-- The statistics of a budget (design: docs/architecture/11-cashier-budget-cashflow-card.md, part B): per month of the fiscal year, the room nights available, the room nights sold
-- and the average daily rate. Occupancy, RevPAR and the room revenue are derived. They follow the status of their budget: only a draft changes.

CREATE TABLE budget_statistics (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint        NOT NULL,
    property_id     bigint        NOT NULL,
    budget_id       bigint        NOT NULL,
    month           smallint      NOT NULL,
    rooms_available int           NOT NULL,
    rooms_sold      int           NOT NULL,
    adr             numeric(18,3) NOT NULL,
    CONSTRAINT bs_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT bs_budget_fk   FOREIGN KEY (property_id, budget_id) REFERENCES budgets (property_id, id) ON DELETE CASCADE,
    CONSTRAINT bs_month_uk    UNIQUE (budget_id, month),
    CONSTRAINT bs_month_ck    CHECK (month BETWEEN 1 AND 12),
    CONSTRAINT bs_rooms_ck    CHECK (rooms_available >= 0 AND rooms_sold >= 0 AND rooms_sold <= rooms_available),
    CONSTRAINT bs_adr_ck      CHECK (adr >= 0)
);

-- +goose StatementBegin
CREATE FUNCTION budget_statistics_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_status varchar(10);
BEGIN
    SELECT status INTO v_status FROM budgets WHERE property_id = COALESCE(NEW.property_id, OLD.property_id) AND id = COALESCE(NEW.budget_id, OLD.budget_id);
    IF v_status IS NOT NULL AND v_status <> 'DRAFT' THEN -- no budget left: the budget itself is being deleted
        RAISE EXCEPTION 'budget % is %: its statistics do not change', COALESCE(NEW.budget_id, OLD.budget_id), v_status USING ERRCODE = 'restrict_violation', CONSTRAINT = 'bs_draft_only';
    END IF;
    RETURN COALESCE(NEW, OLD);
END
$$;
-- +goose StatementEnd
CREATE TRIGGER budget_statistics_guard BEFORE INSERT OR UPDATE OR DELETE ON budget_statistics FOR EACH ROW EXECUTE FUNCTION budget_statistics_guard();

-- +goose Down
DROP TABLE IF EXISTS budget_statistics;
DROP FUNCTION IF EXISTS budget_statistics_guard();
