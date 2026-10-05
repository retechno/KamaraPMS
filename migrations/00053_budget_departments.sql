-- +goose Up
-- The budget by department (design: docs/architecture/12-departments.md): a line of a budget is an account, a department (or none) and a month. The figures of one account may be given
-- for several departments; the unique key counts "no department" as one department of its own.

ALTER TABLE budget_lines ADD COLUMN department_id bigint;
ALTER TABLE budget_lines ADD CONSTRAINT bl_department_fk FOREIGN KEY (property_id, department_id) REFERENCES departments (property_id, id);
ALTER TABLE budget_lines DROP CONSTRAINT bl_cell_uk;
CREATE UNIQUE INDEX bl_cell_uk ON budget_lines (budget_id, account_id, COALESCE(department_id, 0), month);

-- Only the amount of a line changes (the account, the department and the month are its identity).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION budget_lines_guard() RETURNS trigger
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
    IF TG_OP = 'UPDATE' AND (NEW.budget_id <> OLD.budget_id OR NEW.account_id <> OLD.account_id OR NEW.month <> OLD.month OR NEW.property_id <> OLD.property_id
                             OR NEW.department_id IS DISTINCT FROM OLD.department_id) THEN
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

-- +goose Down
-- Without departments a cell is an account and a month: of the figures that were given per department the first is kept (the guard of the lines is off for this).
ALTER TABLE budget_lines DISABLE TRIGGER budget_lines_guard;
DELETE FROM budget_lines a USING budget_lines b WHERE a.id > b.id AND a.budget_id = b.budget_id AND a.account_id = b.account_id AND a.month = b.month;
ALTER TABLE budget_lines ENABLE TRIGGER budget_lines_guard;
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION budget_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_status varchar(10);
    v_type   varchar(20);
    v_post   boolean;
BEGIN
    SELECT status INTO v_status FROM budgets WHERE property_id = COALESCE(NEW.property_id, OLD.property_id) AND id = COALESCE(NEW.budget_id, OLD.budget_id);
    IF TG_OP = 'DELETE' THEN
        IF v_status IS NOT NULL AND v_status <> 'DRAFT' THEN
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
DROP INDEX IF EXISTS bl_cell_uk;
ALTER TABLE budget_lines ADD CONSTRAINT bl_cell_uk UNIQUE (budget_id, account_id, month);
ALTER TABLE budget_lines DROP CONSTRAINT bl_department_fk;
ALTER TABLE budget_lines DROP COLUMN department_id;
