-- +goose Up
-- Subscriptions (docs/architecture/26-subscriptions.md): the plans the platform sells, the one subscription of every tenant, and the invoices the operator bills it with.
-- Plans are platform-wide (no tenant_id): they are offers, not tenant data. A subscription and its invoices belong to one tenant.

CREATE TABLE subscription_plans (
    id                     bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code                   varchar(30)   NOT NULL,
    name                   varchar(200)  NOT NULL,
    -- The optional modules of the plan; CORE is implied and never listed. The set is closed here and in subscription.Modules.
    modules                text[]        NOT NULL DEFAULT '{}',
    max_properties         integer,
    max_rooms              integer,
    max_users              integer,
    price                  numeric(18,3) NOT NULL DEFAULT 0,
    currency_code          char(3)       NOT NULL DEFAULT 'IDR',
    billing_period_months  smallint      NOT NULL DEFAULT 1,
    trial_days             smallint      NOT NULL DEFAULT 0,
    is_active              boolean       NOT NULL DEFAULT true,
    is_default             boolean       NOT NULL DEFAULT false,
    created_at             timestamptz   NOT NULL DEFAULT now(),
    updated_at             timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT subscription_plans_code_uk       UNIQUE (code),
    CONSTRAINT subscription_plans_code_ck       CHECK (code = upper(code) AND code ~ '^[A-Z0-9_-]{2,30}$'),
    CONSTRAINT subscription_plans_modules_ck    CHECK (modules <@ ARRAY['HOUSEKEEPING', 'ACCOUNTING', 'INVENTORY', 'BANQUET', 'POS']::text[]),
    CONSTRAINT subscription_plans_limits_ck     CHECK ((max_properties IS NULL OR max_properties > 0) AND (max_rooms IS NULL OR max_rooms > 0) AND (max_users IS NULL OR max_users > 0)),
    CONSTRAINT subscription_plans_price_ck      CHECK (price >= 0),
    CONSTRAINT subscription_plans_currency_ck   CHECK (currency_code ~ '^[A-Z]{3}$'),
    CONSTRAINT subscription_plans_period_ck     CHECK (billing_period_months IN (1, 3, 6, 12)),
    CONSTRAINT subscription_plans_trial_ck      CHECK (trial_days BETWEEN 0 AND 365),
    CONSTRAINT subscription_plans_default_ck    CHECK (NOT is_default OR is_active)
);
-- At most one plan is the plan of a new tenant.
CREATE UNIQUE INDEX subscription_plans_default_uk ON subscription_plans (is_default) WHERE is_default;
CREATE TRIGGER subscription_plans_set_updated_at BEFORE UPDATE ON subscription_plans
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE tenant_subscriptions (
    id                       bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id                bigint      NOT NULL REFERENCES tenants (id),
    plan_id                  bigint      NOT NULL,
    status                   varchar(10) NOT NULL,
    started_on               date        NOT NULL,
    -- The last day that is paid for (or the last day of the trial), in the tenant's time zone. NULL: no end (tenants that existed before subscriptions, internal tenants).
    paid_through             date,
    grace_days               smallint    NOT NULL DEFAULT 14,
    max_properties_override  integer,
    max_rooms_override       integer,
    max_users_override       integer,
    version                  integer     NOT NULL DEFAULT 1,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT tenant_subscriptions_tenant_uk     UNIQUE (tenant_id),
    CONSTRAINT tenant_subscriptions_plan_fk       FOREIGN KEY (plan_id) REFERENCES subscription_plans (id),
    CONSTRAINT tenant_subscriptions_status_ck     CHECK (status IN ('TRIAL', 'ACTIVE', 'CANCELLED')),
    CONSTRAINT tenant_subscriptions_grace_ck      CHECK (grace_days BETWEEN 0 AND 90),
    CONSTRAINT tenant_subscriptions_period_ck     CHECK (paid_through IS NULL OR paid_through >= started_on - 1),
    CONSTRAINT tenant_subscriptions_trial_end_ck  CHECK (status <> 'TRIAL' OR paid_through IS NOT NULL),
    CONSTRAINT tenant_subscriptions_overrides_ck  CHECK ((max_properties_override IS NULL OR max_properties_override > 0)
                                                     AND (max_rooms_override IS NULL OR max_rooms_override > 0)
                                                     AND (max_users_override IS NULL OR max_users_override > 0))
);
CREATE INDEX tenant_subscriptions_plan_idx ON tenant_subscriptions (plan_id);
CREATE TRIGGER tenant_subscriptions_set_updated_at BEFORE UPDATE ON tenant_subscriptions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Gapless invoice numbers per calendar year (SUB-2026-00001): the row is locked while a number is taken, so a rolled-back invoice gives its number back.
CREATE TABLE subscription_invoice_sequences (
    year        smallint PRIMARY KEY,
    last_value  integer  NOT NULL DEFAULT 0
);

CREATE TABLE subscription_invoices (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL REFERENCES tenants (id),
    number             varchar(20)   NOT NULL,
    plan_id            bigint        NOT NULL,
    period_start       date          NOT NULL,
    period_end         date          NOT NULL,
    amount             numeric(18,3) NOT NULL,
    currency_code      char(3)       NOT NULL,
    status             varchar(10)   NOT NULL DEFAULT 'ISSUED',
    issued_at          timestamptz   NOT NULL,
    due_on             date          NOT NULL,
    paid_on            date,
    paid_at            timestamptz,
    payment_reference  varchar(120),
    voided_at          timestamptz,
    void_reason        varchar(500),
    notes              varchar(1000),
    created_at         timestamptz   NOT NULL DEFAULT now(),
    updated_at         timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT subscription_invoices_number_uk  UNIQUE (number),
    CONSTRAINT subscription_invoices_plan_fk    FOREIGN KEY (plan_id) REFERENCES subscription_plans (id),
    CONSTRAINT subscription_invoices_period_ck  CHECK (period_start <= period_end),
    CONSTRAINT subscription_invoices_amount_ck  CHECK (amount >= 0),
    CONSTRAINT subscription_invoices_status_ck  CHECK (status IN ('ISSUED', 'PAID', 'VOID')),
    CONSTRAINT subscription_invoices_paid_ck    CHECK ((status = 'PAID') = (paid_on IS NOT NULL AND paid_at IS NOT NULL)),
    CONSTRAINT subscription_invoices_void_ck    CHECK ((status = 'VOID') = (voided_at IS NOT NULL AND void_reason IS NOT NULL))
);
CREATE INDEX subscription_invoices_tenant_idx ON subscription_invoices (tenant_id, period_start DESC);
CREATE INDEX subscription_invoices_plan_idx ON subscription_invoices (plan_id);
-- Two open (not void) invoices of one tenant never cover the same day.
ALTER TABLE subscription_invoices ADD CONSTRAINT subscription_invoices_overlap_ex
    EXCLUDE USING gist (tenant_id WITH =, daterange(period_start, period_end, '[]') WITH &&) WHERE (status <> 'VOID');
CREATE TRIGGER subscription_invoices_set_updated_at BEFORE UPDATE ON subscription_invoices
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A PAID or VOID invoice is final; an ISSUED one may only become PAID or VOID. Nothing is deleted.
-- +goose StatementBegin
CREATE FUNCTION subscription_invoice_guard() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'subscription invoices cannot be deleted' USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'subscription_invoice_final';
    END IF;
    IF OLD.status <> 'ISSUED'
       OR NEW.tenant_id <> OLD.tenant_id OR NEW.number <> OLD.number OR NEW.plan_id <> OLD.plan_id
       OR NEW.period_start <> OLD.period_start OR NEW.period_end <> OLD.period_end
       OR NEW.amount <> OLD.amount OR NEW.currency_code <> OLD.currency_code OR NEW.issued_at <> OLD.issued_at THEN
        RAISE EXCEPTION 'subscription invoice % is final', OLD.number USING ERRCODE = 'integrity_constraint_violation', CONSTRAINT = 'subscription_invoice_final';
    END IF;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER subscription_invoice_final BEFORE UPDATE OR DELETE ON subscription_invoices
    FOR EACH ROW EXECUTE FUNCTION subscription_invoice_guard();

-- The standard plans, one definition (the migration and the test reset both call it): LEGACY keeps every tenant that exists exactly as it was (every module, no limit, no
-- end; not for sale); TRIAL is the plan of a new tenant until the operator chooses another default. Idempotent: a plan that exists is left as the operator made it.
-- +goose StatementBegin
CREATE FUNCTION seed_subscription_plans() RETURNS void
    LANGUAGE sql AS
$$
INSERT INTO subscription_plans (code, name, modules, is_active, is_default, trial_days)
VALUES ('LEGACY', 'Legacy (all modules, no limits)', ARRAY['HOUSEKEEPING', 'ACCOUNTING', 'INVENTORY', 'BANQUET', 'POS'], false, false, 0),
       ('TRIAL', 'Trial (all modules)', ARRAY['HOUSEKEEPING', 'ACCOUNTING', 'INVENTORY', 'BANQUET', 'POS'], true, NOT EXISTS (SELECT 1 FROM subscription_plans WHERE is_default), 30)
ON CONFLICT (code) DO NOTHING;
$$;
-- +goose StatementEnd
SELECT seed_subscription_plans();

INSERT INTO tenant_subscriptions (tenant_id, plan_id, status, started_on, paid_through)
SELECT t.id, p.id, 'ACTIVE', (t.created_at AT TIME ZONE t.timezone)::date, NULL
FROM tenants t CROSS JOIN subscription_plans p
WHERE p.code = 'LEGACY';

-- +goose Down
DROP TABLE IF EXISTS subscription_invoices;
DROP FUNCTION IF EXISTS subscription_invoice_guard();
DROP FUNCTION IF EXISTS seed_subscription_plans();
DROP TABLE IF EXISTS subscription_invoice_sequences;
DROP TABLE IF EXISTS tenant_subscriptions;
DROP TABLE IF EXISTS subscription_plans;
