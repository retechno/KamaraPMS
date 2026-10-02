-- +goose Up
-- Companies (corporate accounts), group bookings and the city ledger.
--
-- A company is an account a hotel bills instead of the guest. The city ledger (accounts receivable) needs no
-- ledger table of its own for what is owed: a guest folio is cleared by a payment with the method CITY_LEDGER
-- that names the company, so the balance of a company is
--     payments(CITY_LEDGER, POSTED) - city_ledger_receipts(POSTED)
-- and cannot drift from the folios. What the company later pays is a city_ledger_receipt.

CREATE TABLE companies (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint        NOT NULL,
    property_id         bigint        NOT NULL,
    code                varchar(20)   NOT NULL,
    name                varchar(150)  NOT NULL,
    contact_name        varchar(150),
    email               varchar(254),
    phone               varchar(40),
    address             varchar(300),
    city                varchar(100),
    tax_id              varchar(40),
    -- NULL: no limit. 0: no credit at all.
    credit_limit        numeric(18,3),
    payment_terms_days  smallint      NOT NULL DEFAULT 30,
    notes               varchar(1000),
    is_active           boolean       NOT NULL DEFAULT true,
    created_at          timestamptz   NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    updated_at          timestamptz   NOT NULL DEFAULT now(),
    updated_by          bigint REFERENCES users (id),
    CONSTRAINT companies_property_fk      FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT companies_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT companies_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT companies_credit_limit_ck  CHECK (credit_limit IS NULL OR credit_limit >= 0),
    CONSTRAINT companies_terms_ck         CHECK (payment_terms_days BETWEEN 0 AND 365)
);
CREATE TRIGGER companies_set_updated_at BEFORE UPDATE ON companies
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE booking_groups (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint       NOT NULL,
    property_id      bigint       NOT NULL,
    code             varchar(20)  NOT NULL,
    name             varchar(150) NOT NULL,
    company_id       bigint,
    contact_name     varchar(150),
    contact_email    varchar(254),
    contact_phone    varchar(40),
    arrival_date     date         NOT NULL,
    departure_date   date         NOT NULL,
    notes            varchar(1000),
    is_active        boolean      NOT NULL DEFAULT true,
    created_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    updated_at       timestamptz  NOT NULL DEFAULT now(),
    updated_by       bigint REFERENCES users (id),
    CONSTRAINT booking_groups_property_fk      FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT booking_groups_company_fk       FOREIGN KEY (property_id, company_id) REFERENCES companies (property_id, id),
    CONSTRAINT booking_groups_property_code_uk UNIQUE (property_id, code),
    CONSTRAINT booking_groups_property_id_uk   UNIQUE (property_id, id),
    CONSTRAINT booking_groups_dates_ck         CHECK (departure_date > arrival_date)
);
CREATE TRIGGER booking_groups_set_updated_at BEFORE UPDATE ON booking_groups
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE INDEX booking_groups_company_idx ON booking_groups (property_id, company_id) WHERE company_id IS NOT NULL;

-- A reservation can belong to a group and name the company that is billed (a group's rooms inherit its company).
ALTER TABLE reservations
    ADD COLUMN company_id        bigint,
    ADD COLUMN booking_group_id  bigint,
    ADD CONSTRAINT reservations_company_fk FOREIGN KEY (property_id, company_id)       REFERENCES companies (property_id, id),
    ADD CONSTRAINT reservations_group_fk   FOREIGN KEY (property_id, booking_group_id) REFERENCES booking_groups (property_id, id);
CREATE INDEX reservations_company_idx ON reservations (property_id, company_id) WHERE company_id IS NOT NULL;
CREATE INDEX reservations_group_idx   ON reservations (property_id, booking_group_id) WHERE booking_group_id IS NOT NULL;

-- A payment of method CITY_LEDGER moves a balance from a guest folio to a company account.
ALTER TABLE payments DROP CONSTRAINT payments_method_ck;
ALTER TABLE payments
    ADD COLUMN company_id bigint,
    ADD CONSTRAINT payments_method_ck     CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'CITY_LEDGER')),
    ADD CONSTRAINT payments_company_fk    FOREIGN KEY (property_id, company_id) REFERENCES companies (property_id, id),
    ADD CONSTRAINT payments_company_ck    CHECK ((payment_method = 'CITY_LEDGER') = (company_id IS NOT NULL));
CREATE INDEX payments_company_idx ON payments (property_id, company_id, business_date) WHERE company_id IS NOT NULL;

-- What a company pays against its account.
CREATE TABLE city_ledger_receipts (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    receipt_number   varchar(20)   NOT NULL,
    company_id       bigint        NOT NULL,
    amount           numeric(18,3) NOT NULL,
    payment_method   varchar(15)   NOT NULL,
    reference_number varchar(100),
    remarks          varchar(500),
    business_date    date          NOT NULL,
    paid_at          timestamptz   NOT NULL DEFAULT now(),
    status           varchar(10)   NOT NULL DEFAULT 'POSTED',
    voided_at        timestamptz,
    voided_by        bigint REFERENCES users (id),
    void_reason      varchar(500),
    idempotency_key  varchar(100),
    created_at       timestamptz   NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    approved_by      bigint REFERENCES users (id),
    CONSTRAINT clr_property_fk     FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT clr_company_fk      FOREIGN KEY (property_id, company_id)       REFERENCES companies (property_id, id),
    CONSTRAINT clr_business_day_fk FOREIGN KEY (property_id, business_date)   REFERENCES business_days (property_id, business_date),
    CONSTRAINT clr_number_uk       UNIQUE (property_id, receipt_number),
    CONSTRAINT clr_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT clr_amount_ck       CHECK (amount > 0),
    CONSTRAINT clr_method_ck       CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER')),
    CONSTRAINT clr_status_ck       CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT clr_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT clr_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX clr_idempotency_uk ON city_ledger_receipts (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX clr_company_idx ON city_ledger_receipts (property_id, company_id, business_date);

-- Like a payment: POSTED -> VOIDED only, never deleted, never truncated.
-- +goose StatementBegin
CREATE FUNCTION city_ledger_receipts_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'city ledger receipts cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clr_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason', 'approved_by']) THEN
        RAISE EXCEPTION 'city ledger receipt % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'clr_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER city_ledger_receipts_guard BEFORE UPDATE OR DELETE ON city_ledger_receipts
    FOR EACH ROW EXECUTE FUNCTION city_ledger_receipts_guard();
CREATE TRIGGER city_ledger_receipts_no_truncate BEFORE TRUNCATE ON city_ledger_receipts
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- The receipt number series, for the properties that exist and (in the application) for every new one.
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'CITY_LEDGER_RECEIPT', 'CLR' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type = 'CITY_LEDGER_RECEIPT';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT'));
DROP TABLE IF EXISTS city_ledger_receipts;
DROP FUNCTION IF EXISTS city_ledger_receipts_guard();
DROP INDEX IF EXISTS payments_company_idx;
ALTER TABLE payments DROP CONSTRAINT payments_company_ck, DROP CONSTRAINT payments_company_fk, DROP CONSTRAINT payments_method_ck;
ALTER TABLE payments DROP COLUMN company_id;
ALTER TABLE payments ADD CONSTRAINT payments_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER'));
DROP INDEX IF EXISTS reservations_group_idx;
DROP INDEX IF EXISTS reservations_company_idx;
ALTER TABLE reservations DROP CONSTRAINT reservations_group_fk, DROP CONSTRAINT reservations_company_fk;
ALTER TABLE reservations DROP COLUMN booking_group_id, DROP COLUMN company_id;
DROP TABLE IF EXISTS booking_groups;
DROP TABLE IF EXISTS companies;
