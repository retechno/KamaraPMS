-- +goose Up
-- The department rule of an account (design: docs/architecture/12-departments.md). `department_requirement` says what a journal line of the account does with the department dimension:
-- NONE (the line has no department), OPTIONAL (it may have one, the default until a property says otherwise) or REQUIRED (every new line of the account names one, or the posting is refused).
-- `default_department_id` is the department a line gets when nothing names one (an automatic posting: the day close, the system accounts, a form that leaves it empty). An account that takes
-- no department has no default. A line keeps the department it was posted with, so changing the rule or the default never changes history.
ALTER TABLE gl_accounts ADD COLUMN department_requirement varchar(8) NOT NULL DEFAULT 'OPTIONAL';
ALTER TABLE gl_accounts ADD COLUMN default_department_id bigint;
ALTER TABLE gl_accounts ADD CONSTRAINT gl_accounts_department_requirement_ck CHECK (department_requirement IN ('NONE', 'OPTIONAL', 'REQUIRED'));
ALTER TABLE gl_accounts ADD CONSTRAINT gl_accounts_default_department_ck CHECK (default_department_id IS NULL OR department_requirement <> 'NONE');
ALTER TABLE gl_accounts ADD CONSTRAINT gl_accounts_default_department_fk FOREIGN KEY (property_id, default_department_id) REFERENCES departments (property_id, id);
CREATE INDEX gl_accounts_default_department_idx ON gl_accounts (property_id, default_department_id) WHERE default_department_id IS NOT NULL;

-- +goose Down
DROP INDEX gl_accounts_default_department_idx;
ALTER TABLE gl_accounts DROP CONSTRAINT gl_accounts_default_department_fk;
ALTER TABLE gl_accounts DROP CONSTRAINT gl_accounts_default_department_ck;
ALTER TABLE gl_accounts DROP CONSTRAINT gl_accounts_department_requirement_ck;
ALTER TABLE gl_accounts DROP COLUMN default_department_id;
ALTER TABLE gl_accounts DROP COLUMN department_requirement;
