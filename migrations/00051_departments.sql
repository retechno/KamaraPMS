-- +goose Up
-- Departments and sub-departments as an accounting dimension (design: docs/architecture/12-departments.md). A department is master data of the property, in two levels at most. A journal
-- line, a folio item (the snapshot of the default of its charge code), a charge code (the default), a supplier bill line may name one. A department never changes its code or its parent, so
-- what was posted to it keeps its place in the hierarchy.

CREATE TABLE departments (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    parent_id   bigint,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    sort_order  int          NOT NULL DEFAULT 0,
    is_active   boolean      NOT NULL DEFAULT true,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    created_by  bigint REFERENCES users (id),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    CONSTRAINT departments_property_fk    FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT departments_parent_fk      FOREIGN KEY (property_id, parent_id)  REFERENCES departments (property_id, id),
    CONSTRAINT departments_code_uk        UNIQUE (property_id, code),
    CONSTRAINT departments_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT departments_code_ck        CHECK (code ~ '^[A-Z0-9][A-Z0-9._-]{0,19}$'),
    CONSTRAINT departments_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT departments_self_ck        CHECK (parent_id IS NULL OR parent_id <> id)
);
CREATE INDEX departments_parent_idx ON departments (property_id, parent_id);
CREATE TRIGGER departments_set_updated_at BEFORE UPDATE ON departments FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Two levels at most, and the code and the parent never change.
-- +goose StatementBegin
CREATE FUNCTION departments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_grand bigint;
BEGIN
    IF TG_OP = 'UPDATE' THEN
        IF NEW.code <> OLD.code THEN
            RAISE EXCEPTION 'the code of department % does not change', OLD.id USING ERRCODE = 'restrict_violation', CONSTRAINT = 'departments_code_fixed';
        END IF;
        IF NEW.parent_id IS DISTINCT FROM OLD.parent_id OR NEW.property_id <> OLD.property_id THEN
            RAISE EXCEPTION 'the parent of department % does not change', OLD.id USING ERRCODE = 'restrict_violation', CONSTRAINT = 'departments_parent_fixed';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.parent_id IS NOT NULL THEN
        SELECT parent_id INTO v_grand FROM departments WHERE property_id = NEW.property_id AND id = NEW.parent_id;
        IF v_grand IS NOT NULL THEN
            RAISE EXCEPTION 'a sub-department has no sub-departments of its own' USING ERRCODE = 'check_violation', CONSTRAINT = 'departments_two_levels';
        END IF;
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER departments_guard BEFORE INSERT OR UPDATE ON departments FOR EACH ROW EXECUTE FUNCTION departments_guard();

-- The dimension on what is posted. NULL is allowed everywhere: bank, payable, tax and balance sheet lines have none.
ALTER TABLE gl_journal_lines ADD COLUMN department_id bigint;
ALTER TABLE gl_journal_lines ADD CONSTRAINT gl_journal_lines_department_fk FOREIGN KEY (property_id, department_id) REFERENCES departments (property_id, id);
CREATE INDEX gl_journal_lines_department_idx ON gl_journal_lines (property_id, department_id) WHERE department_id IS NOT NULL;

ALTER TABLE folio_items ADD COLUMN department_id bigint;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_department_fk FOREIGN KEY (property_id, department_id) REFERENCES departments (property_id, id);

ALTER TABLE charge_codes ADD COLUMN department_id bigint;
ALTER TABLE charge_codes ADD CONSTRAINT charge_codes_department_fk FOREIGN KEY (property_id, department_id) REFERENCES departments (property_id, id);

ALTER TABLE supplier_bill_lines ADD COLUMN department_id bigint;
ALTER TABLE supplier_bill_lines ADD CONSTRAINT supplier_bill_lines_department_fk FOREIGN KEY (property_id, department_id) REFERENCES departments (property_id, id);

-- The standard departments of a property (the operated and the undistributed departments of the USALI), and the default department of the standard charge codes. Idempotent.
-- +goose StatementBegin
CREATE FUNCTION seed_departments(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    INSERT INTO departments (tenant_id, property_id, code, name, sort_order, created_by, updated_by)
    SELECT p_tenant_id, p_property_id, d.code, d.name, d.sort_order, p_actor_id, p_actor_id
      FROM (VALUES ('ROOMS', 'Rooms', 10), ('FB', 'Food and beverage', 20), ('OOD', 'Other operated departments', 30),
                   ('AG', 'Administrative and general', 40), ('IT', 'Information and telecommunications', 50), ('SM', 'Sales and marketing', 60),
                   ('POM', 'Property operations and maintenance', 70), ('UTIL', 'Utilities', 80)) AS d (code, name, sort_order)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;

    UPDATE charge_codes cc SET department_id = d.id
      FROM (VALUES ('ROOM', 'ROOMS'), ('FOOD_BEVERAGE', 'FB'), ('SERVICE', 'OOD')) AS m (charge_type, department_code)
      JOIN departments d ON d.property_id = p_property_id AND d.code = m.department_code
     WHERE cc.property_id = p_property_id AND cc.charge_type = m.charge_type AND cc.department_id IS NULL;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

SELECT seed_departments(p.tenant_id, p.id, NULL) FROM properties p;

-- +goose Down
DROP FUNCTION IF EXISTS seed_departments(bigint, bigint, bigint);
ALTER TABLE supplier_bill_lines DROP CONSTRAINT supplier_bill_lines_department_fk;
ALTER TABLE supplier_bill_lines DROP COLUMN department_id;
ALTER TABLE charge_codes DROP CONSTRAINT charge_codes_department_fk;
ALTER TABLE charge_codes DROP COLUMN department_id;
ALTER TABLE folio_items DROP CONSTRAINT folio_items_department_fk;
ALTER TABLE folio_items DROP COLUMN department_id;
DROP INDEX IF EXISTS gl_journal_lines_department_idx;
ALTER TABLE gl_journal_lines DROP CONSTRAINT gl_journal_lines_department_fk;
ALTER TABLE gl_journal_lines DROP COLUMN department_id;
DROP TABLE IF EXISTS departments;
DROP FUNCTION IF EXISTS departments_guard();
