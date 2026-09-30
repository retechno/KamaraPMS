-- +goose Up
-- Folio = financial account. Owned by a reservation; linked to a stay at check-in.
-- Balance is never stored: SUM(debit) - SUM(credit).
CREATE TABLE folios (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint      NOT NULL,
    property_id     bigint      NOT NULL,
    folio_number    varchar(20) NOT NULL,
    reservation_id  bigint      NOT NULL,
    stay_id         bigint,
    folio_type      varchar(10) NOT NULL DEFAULT 'GUEST',
    status          varchar(10) NOT NULL DEFAULT 'OPEN',
    opened_at       timestamptz NOT NULL DEFAULT now(),
    closed_at       timestamptz,
    closed_by       bigint REFERENCES users (id),
    version         int         NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    updated_by      bigint REFERENCES users (id),
    CONSTRAINT folios_property_fk    FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT folios_reservation_fk FOREIGN KEY (property_id, reservation_id) REFERENCES reservations (property_id, id),
    CONSTRAINT folios_stay_fk        FOREIGN KEY (property_id, stay_id)        REFERENCES stays (property_id, id),
    CONSTRAINT folios_number_uk      UNIQUE (property_id, folio_number),
    CONSTRAINT folios_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT folios_type_ck        CHECK (folio_type IN ('GUEST')),
    CONSTRAINT folios_status_ck      CHECK (status IN ('OPEN', 'CLOSED')),
    CONSTRAINT folios_closed_ck      CHECK ((status = 'CLOSED') = (closed_at IS NOT NULL)),
    CONSTRAINT folios_version_ck     CHECK (version >= 1)
);
-- One guest folio per stay; at most one open, not-yet-linked (deposit) folio per reservation.
CREATE UNIQUE INDEX folios_stay_guest_uk ON folios (stay_id) WHERE folio_type = 'GUEST' AND stay_id IS NOT NULL;
CREATE UNIQUE INDEX folios_unlinked_open_uk ON folios (reservation_id) WHERE stay_id IS NULL AND status = 'OPEN';
CREATE INDEX folios_reservation_idx ON folios (reservation_id);
CREATE INDEX folios_open_idx ON folios (property_id) WHERE status = 'OPEN';
CREATE TRIGGER folios_set_updated_at BEFORE UPDATE ON folios
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Payment instrument details. The money movement itself is the paired folio item.
-- Currency = property currency (no currency column by design).
CREATE TABLE payments (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id             bigint        NOT NULL,
    property_id           bigint        NOT NULL,
    payment_number        varchar(20)   NOT NULL,
    folio_id              bigint        NOT NULL,
    payment_type          varchar(10)   NOT NULL,
    payment_method        varchar(15)   NOT NULL,
    amount                numeric(18,2) NOT NULL,
    paid_at               timestamptz   NOT NULL DEFAULT now(),
    business_date         date          NOT NULL,
    reference_number      varchar(100),
    refund_of_payment_id  bigint,
    status                varchar(10)   NOT NULL DEFAULT 'POSTED',
    voided_at             timestamptz,
    voided_by             bigint REFERENCES users (id),
    void_reason           varchar(500),
    idempotency_key       varchar(100),
    remarks               varchar(500),
    created_at            timestamptz   NOT NULL DEFAULT now(),
    created_by            bigint REFERENCES users (id),
    CONSTRAINT payments_property_fk     FOREIGN KEY (tenant_id, property_id)            REFERENCES properties (tenant_id, id),
    CONSTRAINT payments_folio_fk        FOREIGN KEY (property_id, folio_id)             REFERENCES folios (property_id, id),
    CONSTRAINT payments_business_day_fk FOREIGN KEY (property_id, business_date)        REFERENCES business_days (property_id, business_date),
    CONSTRAINT payments_number_uk       UNIQUE (property_id, payment_number),
    CONSTRAINT payments_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT payments_refund_of_fk    FOREIGN KEY (property_id, refund_of_payment_id) REFERENCES payments (property_id, id),
    CONSTRAINT payments_type_ck         CHECK (payment_type IN ('PAYMENT', 'REFUND')),
    CONSTRAINT payments_method_ck       CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER')),
    CONSTRAINT payments_amount_ck       CHECK (amount > 0),
    CONSTRAINT payments_refund_link_ck  CHECK ((payment_type = 'REFUND') = (refund_of_payment_id IS NOT NULL)),
    CONSTRAINT payments_status_ck       CHECK (status IN ('POSTED', 'VOIDED')),
    CONSTRAINT payments_voided_ck       CHECK ((status = 'VOIDED') = (voided_at IS NOT NULL)),
    CONSTRAINT payments_void_reason_ck  CHECK (status <> 'VOIDED' OR void_reason IS NOT NULL)
);
CREATE UNIQUE INDEX payments_idempotency_uk ON payments (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX payments_folio_idx     ON payments (folio_id);
CREATE INDEX payments_refund_of_idx ON payments (refund_of_payment_id) WHERE refund_of_payment_id IS NOT NULL;
CREATE INDEX payments_bd_idx        ON payments (property_id, business_date);

-- Payments may only change POSTED -> VOIDED (and the void_* columns). Never deleted.
-- +goose StatementBegin
CREATE FUNCTION payments_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'payments cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'payments_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'VOIDED'
       OR (to_jsonb(NEW) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason'])
          <> (to_jsonb(OLD) - ARRAY['status', 'voided_at', 'voided_by', 'void_reason']) THEN
        RAISE EXCEPTION 'payment % may only change from POSTED to VOIDED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'payments_void_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER payments_guard BEFORE UPDATE OR DELETE ON payments
    FOR EACH ROW EXECUTE FUNCTION payments_guard();

-- Immutable ledger line with the full calculation snapshot.
-- Signed columns (base..tax_total) are from the revenue perspective; debit/credit are the ledger sides.
CREATE TABLE folio_items (
    id                    bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id             bigint        NOT NULL,
    property_id           bigint        NOT NULL,
    folio_id              bigint        NOT NULL,
    business_date         date          NOT NULL,
    transaction_at        timestamptz   NOT NULL DEFAULT now(),
    service_date          date          NOT NULL,
    transaction_type      varchar(12)   NOT NULL,
    charge_code_id        bigint,
    payment_id            bigint,
    reverses_item_id      bigint,
    stay_id               bigint,
    stay_room_id          bigint,
    reference_type        varchar(30),
    reference_id          varchar(64),
    description           varchar(300)  NOT NULL,
    quantity              numeric(10,3) NOT NULL,
    unit_price            numeric(18,2) NOT NULL,
    price_mode            varchar(12)   NOT NULL,
    base_amount           numeric(18,2) NOT NULL,
    discount_amount       numeric(18,2) NOT NULL DEFAULT 0,
    net_amount            numeric(18,2) NOT NULL,
    rounding_adjustment   numeric(18,2) NOT NULL DEFAULT 0,
    service_charge_total  numeric(18,2) NOT NULL DEFAULT 0,
    tax_total             numeric(18,2) NOT NULL DEFAULT 0,
    debit                 numeric(18,2) NOT NULL DEFAULT 0,
    credit                numeric(18,2) NOT NULL DEFAULT 0,
    source                varchar(15)   NOT NULL,
    reason                varchar(500),
    idempotency_key       varchar(100),
    created_at            timestamptz   NOT NULL DEFAULT now(),
    created_by            bigint REFERENCES users (id),
    CONSTRAINT folio_items_property_fk     FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_items_folio_fk        FOREIGN KEY (property_id, folio_id)         REFERENCES folios (property_id, id),
    CONSTRAINT folio_items_business_day_fk FOREIGN KEY (property_id, business_date)    REFERENCES business_days (property_id, business_date),
    CONSTRAINT folio_items_charge_code_fk  FOREIGN KEY (property_id, charge_code_id)   REFERENCES charge_codes (property_id, id),
    CONSTRAINT folio_items_payment_fk      FOREIGN KEY (property_id, payment_id)       REFERENCES payments (property_id, id),
    CONSTRAINT folio_items_stay_fk         FOREIGN KEY (property_id, stay_id)          REFERENCES stays (property_id, id),
    CONSTRAINT folio_items_stay_room_fk    FOREIGN KEY (property_id, stay_room_id)     REFERENCES stay_rooms (property_id, id),
    CONSTRAINT folio_items_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT folio_items_reverses_fk     FOREIGN KEY (property_id, reverses_item_id) REFERENCES folio_items (property_id, id),
    CONSTRAINT folio_items_type_ck         CHECK (transaction_type IN ('CHARGE', 'ADJUSTMENT', 'PAYMENT', 'REFUND', 'REVERSAL')),
    CONSTRAINT folio_items_mode_ck         CHECK (price_mode IN ('EXCLUSIVE', 'INCLUSIVE')),
    CONSTRAINT folio_items_source_ck       CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION')),
    CONSTRAINT folio_items_service_date_ck CHECK (service_date <= business_date),
    CONSTRAINT folio_items_quantity_ck     CHECK (quantity <> 0),
    CONSTRAINT folio_items_reference_ck    CHECK ((reference_type IS NULL) = (reference_id IS NULL)),
    -- Ledger sides
    CONSTRAINT folio_items_sides_ck        CHECK (debit >= 0 AND credit >= 0 AND NOT (debit > 0 AND credit > 0)),
    CONSTRAINT folio_items_balance_ck      CHECK (debit - credit = net_amount + service_charge_total + tax_total),
    -- Price-mode consistency (rounding adjustment only exists for INCLUSIVE)
    CONSTRAINT folio_items_price_mode_ck   CHECK (
        (price_mode = 'EXCLUSIVE' AND rounding_adjustment = 0 AND net_amount = base_amount - discount_amount)
        OR (price_mode = 'INCLUSIVE' AND debit - credit = base_amount - discount_amount)),
    -- Per-type rules
    CONSTRAINT folio_items_charge_link_ck  CHECK (transaction_type NOT IN ('CHARGE', 'ADJUSTMENT')
                                                  OR (charge_code_id IS NOT NULL AND payment_id IS NULL)),
    CONSTRAINT folio_items_charge_side_ck  CHECK (transaction_type <> 'CHARGE' OR credit = 0),
    CONSTRAINT folio_items_adjust_ck       CHECK (transaction_type <> 'ADJUSTMENT' OR reason IS NOT NULL),
    CONSTRAINT folio_items_payment_ck      CHECK (transaction_type <> 'PAYMENT'
                                                  OR (payment_id IS NOT NULL AND credit > 0 AND debit = 0 AND charge_code_id IS NULL)),
    CONSTRAINT folio_items_refund_ck       CHECK (transaction_type <> 'REFUND'
                                                  OR (payment_id IS NOT NULL AND debit > 0 AND credit = 0 AND charge_code_id IS NULL)),
    CONSTRAINT folio_items_reversal_ck     CHECK ((transaction_type = 'REVERSAL') = (reverses_item_id IS NOT NULL)),
    CONSTRAINT folio_items_reversal_reason_ck CHECK (transaction_type <> 'REVERSAL' OR reason IS NOT NULL)
);
CREATE UNIQUE INDEX folio_items_payment_uk     ON folio_items (payment_id) WHERE payment_id IS NOT NULL;
CREATE UNIQUE INDEX folio_items_reverses_uk    ON folio_items (reverses_item_id) WHERE reverses_item_id IS NOT NULL;
CREATE UNIQUE INDEX folio_items_idempotency_uk ON folio_items (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX folio_items_folio_idx       ON folio_items (folio_id, transaction_at);
CREATE INDEX folio_items_bd_idx          ON folio_items (property_id, business_date);
CREATE INDEX folio_items_revenue_idx     ON folio_items (property_id, charge_code_id, business_date);
CREATE INDEX folio_items_stay_idx        ON folio_items (stay_id) WHERE stay_id IS NOT NULL;
CREATE TRIGGER folio_items_append_only BEFORE UPDATE OR DELETE ON folio_items
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- Applied tax / service charge, snapshotted at posting time.
CREATE TABLE folio_item_components (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id          bigint        NOT NULL,
    property_id        bigint        NOT NULL,
    folio_item_id      bigint        NOT NULL,
    component_type     varchar(15)   NOT NULL,
    tax_id             bigint,
    service_charge_id  bigint,
    code               varchar(20)   NOT NULL,
    name               varchar(100)  NOT NULL,
    rate               numeric(7,4)  NOT NULL,
    tax_on_service     boolean,
    base_amount        numeric(18,2) NOT NULL,
    amount             numeric(18,2) NOT NULL,
    sequence           smallint      NOT NULL,
    created_at         timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT folio_item_components_property_fk FOREIGN KEY (tenant_id, property_id)         REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_item_components_item_fk     FOREIGN KEY (property_id, folio_item_id)     REFERENCES folio_items (property_id, id),
    CONSTRAINT folio_item_components_tax_fk      FOREIGN KEY (property_id, tax_id)            REFERENCES taxes (property_id, id),
    CONSTRAINT folio_item_components_service_fk  FOREIGN KEY (property_id, service_charge_id) REFERENCES service_charges (property_id, id),
    CONSTRAINT folio_item_components_type_ck     CHECK (component_type IN ('SERVICE_CHARGE', 'TAX')),
    CONSTRAINT folio_item_components_tax_ck      CHECK ((component_type = 'TAX') = (tax_id IS NOT NULL)),
    CONSTRAINT folio_item_components_service_ck  CHECK ((component_type = 'SERVICE_CHARGE') = (service_charge_id IS NOT NULL)),
    CONSTRAINT folio_item_components_tos_ck      CHECK ((component_type = 'TAX') = (tax_on_service IS NOT NULL)),
    CONSTRAINT folio_item_components_rate_ck     CHECK (rate BETWEEN 0 AND 100),
    CONSTRAINT folio_item_components_seq_uk      UNIQUE (folio_item_id, component_type, sequence)
);
CREATE INDEX folio_item_components_item_idx ON folio_item_components (folio_item_id);
CREATE INDEX folio_item_components_tax_idx  ON folio_item_components (property_id, tax_id) WHERE tax_id IS NOT NULL;
CREATE TRIGGER folio_item_components_append_only BEFORE UPDATE OR DELETE ON folio_item_components
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- Deferred check at COMMIT: item service/tax totals equal the sum of its components.
-- Fires for new items and for components added later, so neither side can drift.
-- +goose StatementBegin
CREATE FUNCTION folio_item_totals_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_item_id bigint;
    v_item    record;
    v_svc     numeric;
    v_tax     numeric;
BEGIN
    IF TG_TABLE_NAME = 'folio_items' THEN
        v_item_id := NEW.id;
    ELSE
        v_item_id := NEW.folio_item_id;
    END IF;

    SELECT service_charge_total, tax_total INTO v_item FROM folio_items WHERE id = v_item_id;

    SELECT COALESCE(sum(amount) FILTER (WHERE component_type = 'SERVICE_CHARGE'), 0),
           COALESCE(sum(amount) FILTER (WHERE component_type = 'TAX'), 0)
      INTO v_svc, v_tax
      FROM folio_item_components
     WHERE folio_item_id = v_item_id;

    IF v_item.service_charge_total <> v_svc OR v_item.tax_total <> v_tax THEN
        RAISE EXCEPTION 'folio item % totals (service %, tax %) do not match components (service %, tax %)',
            v_item_id, v_item.service_charge_total, v_item.tax_total, v_svc, v_tax
            USING ERRCODE = 'check_violation', CONSTRAINT = 'folio_items_totals_match_components';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE CONSTRAINT TRIGGER folio_items_totals_check AFTER INSERT ON folio_items
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION folio_item_totals_check();
CREATE CONSTRAINT TRIGGER folio_item_components_totals_check AFTER INSERT ON folio_item_components
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION folio_item_totals_check();

-- Posting register: the business identity of each posted expected charge.
-- Key = (stay, night, source[, source ref]) among POSTED rows. stay_room_id and charge_code_id are
-- traceability only, so a same-day room move or a charge-code change cannot cause a double charge.
CREATE TABLE stay_charge_postings (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint      NOT NULL,
    property_id       bigint      NOT NULL,
    stay_id           bigint      NOT NULL,
    stay_room_id      bigint      NOT NULL,
    service_date      date        NOT NULL,
    charge_source     varchar(12) NOT NULL,
    source_ref_id     bigint,
    charge_code_id    bigint      NOT NULL,
    folio_item_id     bigint      NOT NULL,
    business_date     date        NOT NULL,
    posting_trigger   varchar(12) NOT NULL,
    status            varchar(10) NOT NULL DEFAULT 'POSTED',
    reversal_item_id  bigint,
    created_at        timestamptz NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    CONSTRAINT stay_charge_postings_property_fk FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT stay_charge_postings_stay_fk     FOREIGN KEY (property_id, stay_id)         REFERENCES stays (property_id, id),
    CONSTRAINT stay_charge_postings_segment_fk  FOREIGN KEY (property_id, stay_room_id)    REFERENCES stay_rooms (property_id, id),
    CONSTRAINT stay_charge_postings_code_fk     FOREIGN KEY (property_id, charge_code_id)  REFERENCES charge_codes (property_id, id),
    CONSTRAINT stay_charge_postings_item_fk     FOREIGN KEY (property_id, folio_item_id)   REFERENCES folio_items (property_id, id),
    CONSTRAINT stay_charge_postings_reversal_fk FOREIGN KEY (property_id, reversal_item_id) REFERENCES folio_items (property_id, id),
    CONSTRAINT stay_charge_postings_bd_fk       FOREIGN KEY (property_id, business_date)   REFERENCES business_days (property_id, business_date),
    CONSTRAINT stay_charge_postings_item_uk     UNIQUE (folio_item_id),
    CONSTRAINT stay_charge_postings_source_ck   CHECK (charge_source IN ('ROOM_NIGHT')),
    CONSTRAINT stay_charge_postings_ref_ck      CHECK (charge_source <> 'ROOM_NIGHT' OR source_ref_id IS NULL),
    CONSTRAINT stay_charge_postings_trigger_ck  CHECK (posting_trigger IN ('NIGHT_AUDIT', 'MANUAL', 'CHECK_OUT', 'RECOVERY')),
    CONSTRAINT stay_charge_postings_status_ck   CHECK (status IN ('POSTED', 'REVERSED')),
    CONSTRAINT stay_charge_postings_reversed_ck CHECK ((status = 'REVERSED') = (reversal_item_id IS NOT NULL)),
    CONSTRAINT stay_charge_postings_night_ck    CHECK (service_date <= business_date)
);
-- THE room-charge idempotency key.
CREATE UNIQUE INDEX stay_charge_postings_once_uk ON stay_charge_postings
    (stay_id, service_date, charge_source, COALESCE(source_ref_id, 0)) WHERE status = 'POSTED';
CREATE INDEX stay_charge_postings_night_idx ON stay_charge_postings (property_id, service_date);

-- Register rows may only flip POSTED -> REVERSED (with reversal_item_id). Never deleted.
-- +goose StatementBegin
CREATE FUNCTION stay_charge_postings_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'stay_charge_postings cannot be deleted'
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'stay_charge_postings_no_delete';
    END IF;
    IF OLD.status <> 'POSTED' OR NEW.status <> 'REVERSED'
       OR (to_jsonb(NEW) - ARRAY['status', 'reversal_item_id'])
          <> (to_jsonb(OLD) - ARRAY['status', 'reversal_item_id']) THEN
        RAISE EXCEPTION 'posting % may only change from POSTED to REVERSED', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'stay_charge_postings_reverse_only';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER stay_charge_postings_guard BEFORE UPDATE OR DELETE ON stay_charge_postings
    FOR EACH ROW EXECUTE FUNCTION stay_charge_postings_guard();

-- Property currency is locked once financial transactions exist.
-- +goose StatementBegin
CREATE FUNCTION properties_currency_lock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.currency_code IS DISTINCT FROM OLD.currency_code
        OR NEW.currency_decimals IS DISTINCT FROM OLD.currency_decimals)
       AND EXISTS (SELECT 1 FROM folio_items WHERE property_id = OLD.id) THEN
        RAISE EXCEPTION 'currency of property % is locked: financial transactions exist', OLD.id
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'properties_currency_lock';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER properties_currency_lock BEFORE UPDATE OF currency_code, currency_decimals ON properties
    FOR EACH ROW EXECUTE FUNCTION properties_currency_lock();

-- A charge code's price_mode (how rate-grid amounts are interpreted) and charge_type are locked once used.
-- +goose StatementBegin
CREATE FUNCTION charge_codes_usage_lock() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.price_mode IS DISTINCT FROM OLD.price_mode
       AND (EXISTS (SELECT 1 FROM rate_plans             WHERE property_id = OLD.property_id AND room_charge_code_id = OLD.id)
         OR EXISTS (SELECT 1 FROM reservation_room_rates WHERE property_id = OLD.property_id AND charge_code_id = OLD.id)
         OR EXISTS (SELECT 1 FROM folio_items            WHERE property_id = OLD.property_id AND charge_code_id = OLD.id)) THEN
        RAISE EXCEPTION 'price_mode of charge code % is locked: the code is in use', OLD.code
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'charge_codes_price_mode_lock';
    END IF;
    IF NEW.charge_type IS DISTINCT FROM OLD.charge_type
       AND OLD.charge_type = 'ROOM'
       AND (EXISTS (SELECT 1 FROM rate_plans             WHERE property_id = OLD.property_id AND room_charge_code_id = OLD.id)
         OR EXISTS (SELECT 1 FROM reservation_room_rates WHERE property_id = OLD.property_id AND charge_code_id = OLD.id)) THEN
        RAISE EXCEPTION 'charge_type of charge code % is locked: it is used for room revenue', OLD.code
            USING ERRCODE = 'restrict_violation', CONSTRAINT = 'charge_codes_charge_type_lock';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER charge_codes_usage_lock BEFORE UPDATE OF price_mode, charge_type ON charge_codes
    FOR EACH ROW EXECUTE FUNCTION charge_codes_usage_lock();

-- +goose Down
DROP TRIGGER IF EXISTS charge_codes_usage_lock ON charge_codes;
DROP FUNCTION IF EXISTS charge_codes_usage_lock();
DROP TRIGGER IF EXISTS properties_currency_lock ON properties;
DROP FUNCTION IF EXISTS properties_currency_lock();
DROP TABLE IF EXISTS stay_charge_postings;
DROP FUNCTION IF EXISTS stay_charge_postings_guard();
DROP TABLE IF EXISTS folio_item_components;
DROP TABLE IF EXISTS folio_items;
DROP FUNCTION IF EXISTS folio_item_totals_check();
DROP TABLE IF EXISTS payments;
DROP FUNCTION IF EXISTS payments_guard();
DROP TABLE IF EXISTS folios;
