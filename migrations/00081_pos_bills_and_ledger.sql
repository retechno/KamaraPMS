-- +goose Up
-- POS stage 2: bills, orders and payment (docs/architecture/34-pos.md sections 3.1, 3.3, 7, 17).
--
-- 1. The ledger learns what an outlet bill needs:
--    folios                     folio type OUTLET with its outlet (no stay, reservation, group or company)
--    charge_codes               charge type ROUNDING, the system code POS_ROUNDING (no tax, no service), its revenue account 4591
--    folio_items                source POS
--    payments                   method COMP (a payment that settles the guest ledger against an expense account), accepted by payments only: no refund, receipt, fee or settlement
--    gl_account_map             key COMP, to the expense account 6196 "Complimentary and house use"
--    folio_transfer_batches     scopes OUTLET_BILL (split and merge) and OUTLET_CHARGE (a bill charged to a room); an outlet charge needs no approval
--    companies                  pos_billing_allowed
--    folio_item_gl (view)       the role-based is_deposit of 00075 covers OUTLET too
-- 2. The bills: pos_bills, pos_bill_lines, pos_rounds, pos_tickets, pos_ticket_lines (rows only: printing and the kitchen display are stage 3) and the holds of tables.
--
-- Differences from the design text, recorded in 34-pos.md section 18: the expense account is 6196 (under administrative and general, next to cash over and short 6195) and not 6591, which sits
-- under utilities in the standard chart.

-- 1. folios -----------------------------------------------------------------------------------------------------------------------------------------
ALTER TABLE folios ADD COLUMN outlet_id bigint;
ALTER TABLE folios ADD CONSTRAINT folios_outlet_fk FOREIGN KEY (property_id, outlet_id) REFERENCES pos_outlets (property_id, id);
ALTER TABLE folios DROP CONSTRAINT folios_type_ck;
ALTER TABLE folios DROP CONSTRAINT folios_reservation_ck;
ALTER TABLE folios
    ADD CONSTRAINT folios_type_ck          CHECK (folio_type IN ('GUEST', 'COMPANY', 'MASTER', 'OUTLET')),
    ADD CONSTRAINT folios_outlet_ck        CHECK ((folio_type = 'OUTLET') = (outlet_id IS NOT NULL)),
    ADD CONSTRAINT folios_outlet_shape_ck  CHECK (folio_type <> 'OUTLET' OR (stay_id IS NULL AND reservation_id IS NULL AND booking_group_id IS NULL AND bill_to_company_id IS NULL)),
    ADD CONSTRAINT folios_reservation_ck   CHECK (folio_type IN ('MASTER', 'OUTLET') OR reservation_id IS NOT NULL);
CREATE INDEX folios_outlet_open_idx ON folios (property_id, outlet_id) WHERE status = 'OPEN' AND folio_type = 'OUTLET';

-- folio_items source
ALTER TABLE folio_items DROP CONSTRAINT folio_items_source_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_source_ck CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION', 'TRANSFER', 'POS'));
-- A correction carries its approver (00017), except what the cashier of an outlet does on their own: the reversal of a split or a merge, the transfer pair of a charge to a room and a discount within
-- the limit of the outlet. Those rows are of the source POS, which is the one thing that may lack the approver.
ALTER TABLE folio_items DROP CONSTRAINT folio_items_approval_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_approval_ck
    CHECK ((transaction_type IN ('ADJUSTMENT', 'REVERSAL')) = (approved_by IS NOT NULL) OR (source = 'POS' AND approved_by IS NULL));
-- A complimentary payment carries the person who approved it, as a refund and a void do.
ALTER TABLE payments DROP CONSTRAINT payments_approval_ck;
ALTER TABLE payments ADD CONSTRAINT payments_approval_ck
    CHECK (((payment_type = 'REFUND') OR (status = 'VOIDED')) = (approved_by IS NOT NULL) OR payment_method = 'COMP');
-- A sale line of a bill is posted once: the CHARGE row carries the uuid of its line, whatever the race.
CREATE UNIQUE INDEX folio_items_pos_line_uk ON folio_items (property_id, reference_id)
    WHERE reference_type = 'POS_BILL_LINE' AND transaction_type = 'CHARGE' AND source = 'POS';

-- charge type ROUNDING
ALTER TABLE charge_codes DROP CONSTRAINT charge_codes_type_ck;
ALTER TABLE charge_codes ADD CONSTRAINT charge_codes_type_ck CHECK (charge_type IN ('ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER', 'TRANSFER', 'ROUNDING'));

-- payment method COMP
ALTER TABLE payments DROP CONSTRAINT payments_method_ck;
ALTER TABLE payments ADD CONSTRAINT payments_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'CITY_LEDGER', 'QRIS', 'COMP'));
ALTER TABLE payments ADD CONSTRAINT payments_comp_ck CHECK (payment_method <> 'COMP' OR payment_type = 'PAYMENT');
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER', 'COMP'));

-- transfer scopes; an outlet charge or a split is the cashier's own act, with no approval
ALTER TABLE folio_transfer_batches ALTER COLUMN approved_by DROP NOT NULL;
ALTER TABLE folio_transfer_batches DROP CONSTRAINT folio_transfer_batches_scope_ck;
ALTER TABLE folio_transfer_batches ADD CONSTRAINT folio_transfer_batches_scope_ck
    CHECK (scope IN ('RESERVATION', 'GROUP_MEMBER', 'GROUP_MASTER', 'ANY', 'RETURN', 'OUTLET_BILL', 'OUTLET_CHARGE'));
ALTER TABLE folio_transfer_batches ADD CONSTRAINT folio_transfer_batches_approval_ck
    CHECK (approved_by IS NOT NULL OR scope IN ('OUTLET_BILL', 'OUTLET_CHARGE'));

ALTER TABLE companies ADD COLUMN pos_billing_allowed boolean NOT NULL DEFAULT false;

-- 3. The view: is_deposit by role. A GUEST or COMPANY folio keeps the expression of 00075. A MASTER and an OUTLET folio have no stay: a payment on it is a deposit only when it was taken before
-- the first live CHARGE row of the folio (a transfer's TRF charge included), and a settlement after that.
CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND CASE WHEN f.folio_type IN ('MASTER', 'OUTLET')
                 THEN NOT EXISTS (SELECT 1 FROM folio_items c
                                   WHERE c.property_id = fi.property_id AND c.folio_id = fi.folio_id AND c.transaction_type = 'CHARGE'
                                     AND c.id < (SELECT min(pi.id) FROM folio_items pi WHERE pi.property_id = rp.property_id AND pi.payment_id = rp.id)
                                     AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = c.property_id AND rv.reverses_item_id = c.id))
                 ELSE (st.id IS NULL OR rp.paid_at < st.actual_check_in_at) END) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference,
       COALESCE(fi.department_id, o.department_id) AS department_id
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;

-- 4. The standard chart of a NEW property: 4591 "Rounding" under the other revenue and 6196 "Complimentary and house use" under administrative and general.
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v5;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_v5()
    UNION ALL
    SELECT '4591', 'Rounding', 'REVENUE', 'CREDIT', '4500', true, 'REV_MISC'
    UNION ALL
    SELECT '6196', 'Complimentary and house use', 'EXPENSE', 'DEBIT', '6100', true, 'UND_AG'
$$;
-- +goose StatementEnd
ALTER FUNCTION usali_chart_names(text) RENAME TO usali_chart_names_v3;
-- +goose StatementBegin
CREATE FUNCTION usali_chart_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_names_v3(p_lang)
    UNION ALL
    SELECT '4591', 'Pembulatan' WHERE p_lang = 'id'
    UNION ALL
    SELECT '6196', 'Komplimen dan pemakaian internal' WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- The standard charge codes of a NEW property: POS_ROUNDING.
ALTER FUNCTION charge_code_names(text) RENAME TO charge_code_names_v2;
-- +goose StatementBegin
CREATE FUNCTION charge_code_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM charge_code_names_v2(p_lang)
    UNION ALL
    SELECT 'POS_ROUNDING', 'POS rounding' WHERE p_lang = 'en'
    UNION ALL
    SELECT 'POS_ROUNDING', 'Pembulatan POS' WHERE p_lang = 'id'
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION seed_charge_codes(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode, is_system, created_by, updated_by)
    SELECT p_tenant_id, p_property_id, s.code, s.name, s.charge_type, 'EXCLUSIVE', true, p_actor_id, p_actor_id
      FROM (VALUES
        ('ROOM',        'Room charge',                  'ROOM'),
        ('ROOM_EXEMPT', 'Room charge (no tax or service)', 'ROOM'),
        ('BREAKFAST',   'Breakfast',                    'FOOD_BEVERAGE'),
        ('RESTAURANT',  'Restaurant',                   'FOOD_BEVERAGE'),
        ('LAUNDRY',     'Laundry',                      'SERVICE'),
        ('MINIBAR',     'Minibar',                      'FOOD_BEVERAGE'),
        ('EXTRA_BED',   'Extra bed',                    'SERVICE'),
        ('NO_SHOW_FEE', 'No-show fee',                  'FEE'),
        ('CANCEL_FEE',  'Cancellation fee',             'FEE'),
        ('OTHER',       'Other charges',                'OTHER'),
        ('TRF',         'Transfer balance',             'TRANSFER'),
        ('POS_ROUNDING', 'POS rounding',                'ROUNDING')
      ) AS s (code, name, charge_type)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- The properties that exist. Each gets the charge code POS_ROUNDING; each that has a chart also gets the accounts 4591 and 6196 and the key COMP:
--   * a property that already has the key COMP is left as it is;
--   * with no account 4591 (6196), it is created;
--   * with the account already (the code is the user's: it is neither used nor overwritten), the next free code from 4592 to 4599 (6197 to 6199) is used.
-- The code POS_ROUNDING takes the code of the rounding account that was created. The names are Indonesian when the account 1150 of the property has its Indonesian standard name.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_idn boolean;
    v_rcode text;
    v_ccode text;
    v_id bigint;
    n int;
    v_codes int := 0;
    v_added int := 0;
    v_replaced int := 0;
BEGIN
    FOR r IN SELECT p.tenant_id, p.id AS property_id FROM properties p ORDER BY p.id LOOP
        v_idn := EXISTS (SELECT 1 FROM charge_codes c WHERE c.property_id = r.property_id AND c.code = 'OTHER' AND c.is_system AND c.name = 'Biaya lain-lain');
        v_rcode := NULL;
        IF EXISTS (SELECT 1 FROM accounting_settings s WHERE s.property_id = r.property_id)
           AND NOT EXISTS (SELECT 1 FROM gl_account_map m WHERE m.property_id = r.property_id AND m.map_key = 'COMP') THEN
            -- the rounding revenue account
            v_rcode := '4591';
            IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = v_rcode) THEN
                v_rcode := NULL;
                FOR n IN 4592..4599 LOOP
                    IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = n::text) THEN
                        v_rcode := n::text;
                        EXIT;
                    END IF;
                END LOOP;
                IF v_rcode IS NULL THEN
                    RAISE EXCEPTION '00081: property % has no free account code from 4591 to 4599 for the rounding account', r.property_id;
                END IF;
                v_replaced := v_replaced + 1;
            END IF;
            INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
            VALUES (r.tenant_id, r.property_id, v_rcode, CASE WHEN v_idn THEN 'Pembulatan' ELSE 'Rounding' END, 'REVENUE', 'CREDIT',
                    (SELECT p.id FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = '4500'), true, 'REV_MISC');
            -- the complimentary expense account and its key
            v_ccode := '6196';
            IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = v_ccode) THEN
                v_ccode := NULL;
                FOR n IN 6197..6199 LOOP
                    IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = n::text) THEN
                        v_ccode := n::text;
                        EXIT;
                    END IF;
                END LOOP;
                IF v_ccode IS NULL THEN
                    RAISE EXCEPTION '00081: property % has no free account code from 6196 to 6199 for the complimentary account', r.property_id;
                END IF;
                v_replaced := v_replaced + 1;
            END IF;
            INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
            VALUES (r.tenant_id, r.property_id, v_ccode, CASE WHEN v_idn THEN 'Komplimen dan pemakaian internal' ELSE 'Complimentary and house use' END, 'EXPENSE', 'DEBIT',
                    (SELECT p.id FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = '6100'), true, 'UND_AG')
            RETURNING id INTO v_id;
            INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (r.tenant_id, r.property_id, 'COMP', v_id);
            v_added := v_added + 1;
        END IF;
        INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode, is_system, gl_account_code)
        VALUES (r.tenant_id, r.property_id, 'POS_ROUNDING', CASE WHEN v_idn THEN 'Pembulatan POS' ELSE 'POS rounding' END, 'ROUNDING', 'EXCLUSIVE', true, v_rcode)
        ON CONFLICT (property_id, code) DO NOTHING;
        IF FOUND THEN v_codes := v_codes + 1; END IF;
    END LOOP;
    RAISE NOTICE '00081: POS_ROUNDING added to % propert(y/ies); accounts 4591 and 6196 and the key COMP added to % propert(y/ies), % replacement code(s) because the standard one was taken', v_codes, v_added, v_replaced;
END
$$;
-- +goose StatementEnd

-- 5. The tables of the bills ---------------------------------------------------------------------------------------------------------------------
CREATE TABLE pos_bills (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint        NOT NULL,
    property_id          bigint        NOT NULL,
    outlet_id            bigint        NOT NULL,
    client_uuid          uuid          NOT NULL,
    folio_id             bigint,
    bill_number          varchar(20),
    status               varchar(8)    NOT NULL DEFAULT 'OPEN',
    order_type           varchar(12)   NOT NULL,
    table_id             bigint,
    guest_count          smallint,
    target_stay_id       bigint,
    customer_name        varchar(100),
    waiter_id            bigint        REFERENCES users (id),
    comp_treatment       varchar(14)   NOT NULL,
    business_date_opened date          NOT NULL,
    opened_at            timestamptz   NOT NULL DEFAULT now(),
    bill_requested_at    timestamptz,
    closed_at            timestamptz,
    closed_business_date date,
    merged_into_bill_id  bigint,
    note                 varchar(200),
    version              int           NOT NULL DEFAULT 1,
    created_at           timestamptz   NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz   NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT pos_bills_property_fk   FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_bills_outlet_fk     FOREIGN KEY (property_id, outlet_id)       REFERENCES pos_outlets (property_id, id),
    CONSTRAINT pos_bills_folio_fk      FOREIGN KEY (property_id, folio_id)        REFERENCES folios (property_id, id),
    CONSTRAINT pos_bills_table_fk      FOREIGN KEY (property_id, table_id)        REFERENCES pos_tables (property_id, id),
    CONSTRAINT pos_bills_stay_fk       FOREIGN KEY (property_id, target_stay_id)  REFERENCES stays (property_id, id),
    CONSTRAINT pos_bills_merged_fk     FOREIGN KEY (property_id, merged_into_bill_id) REFERENCES pos_bills (property_id, id),
    CONSTRAINT pos_bills_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_bills_uuid_uk       UNIQUE (property_id, client_uuid),
    CONSTRAINT pos_bills_folio_uk      UNIQUE (property_id, folio_id),
    CONSTRAINT pos_bills_status_ck     CHECK (status IN ('OPEN', 'CLOSED', 'VOID', 'MERGED')),
    CONSTRAINT pos_bills_type_ck       CHECK (order_type IN ('DINE_IN', 'TAKEAWAY', 'ROOM_SERVICE', 'DIRECT')),
    CONSTRAINT pos_bills_table_ck      CHECK (table_id IS NULL OR order_type = 'DINE_IN'),
    CONSTRAINT pos_bills_comp_ck       CHECK (comp_treatment IN ('PAYMENT_COMP', 'DISCOUNT_100')),
    CONSTRAINT pos_bills_closed_ck     CHECK ((status = 'OPEN') = (closed_at IS NULL)),
    CONSTRAINT pos_bills_merged_ck     CHECK ((status = 'MERGED') = (merged_into_bill_id IS NOT NULL)),
    CONSTRAINT pos_bills_guests_ck     CHECK (guest_count IS NULL OR guest_count BETWEEN 1 AND 999),
    CONSTRAINT pos_bills_version_ck    CHECK (version >= 1)
);
CREATE INDEX pos_bills_open_idx ON pos_bills (property_id, outlet_id) WHERE status = 'OPEN';
CREATE INDEX pos_bills_table_idx ON pos_bills (property_id, table_id) WHERE status = 'OPEN' AND table_id IS NOT NULL;
CREATE INDEX pos_bills_stay_idx ON pos_bills (property_id, target_stay_id) WHERE status = 'OPEN' AND target_stay_id IS NOT NULL;
CREATE INDEX pos_bills_closed_day_idx ON pos_bills (property_id, outlet_id, closed_business_date) WHERE closed_business_date IS NOT NULL;
CREATE TRIGGER pos_bills_set_updated_at BEFORE UPDATE ON pos_bills FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE pos_rounds (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    bill_id       bigint      NOT NULL,
    round_no      int         NOT NULL,
    client_uuid   uuid        NOT NULL,
    sent_by       bigint      REFERENCES users (id),
    sent_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pos_rounds_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_rounds_bill_fk     FOREIGN KEY (property_id, bill_id)   REFERENCES pos_bills (property_id, id),
    CONSTRAINT pos_rounds_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_rounds_no_uk       UNIQUE (bill_id, round_no),
    CONSTRAINT pos_rounds_uuid_uk     UNIQUE (property_id, client_uuid)
);

CREATE TABLE pos_bill_lines (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    bill_id          bigint        NOT NULL,
    client_uuid      uuid          NOT NULL,
    product_id       bigint        NOT NULL,
    product_name     varchar(100)  NOT NULL,
    charge_code_id   bigint        NOT NULL,
    rule_profile_id  bigint,
    options          jsonb         NOT NULL DEFAULT '[]'::jsonb,
    quantity         numeric(10,3) NOT NULL,
    unit_price       numeric(18,3) NOT NULL,
    discount_pct     numeric(5,2),
    discount_amount  numeric(18,3) NOT NULL DEFAULT 0,
    discount_reason  varchar(200),
    discount_by      bigint        REFERENCES users (id),
    is_comp          boolean       NOT NULL DEFAULT false,
    note             varchar(200),
    station_id       bigint,
    status           varchar(5)    NOT NULL DEFAULT 'DRAFT',
    round_id         bigint,
    folio_item_id    bigint,
    voided_item_id   bigint,
    created_by       bigint        REFERENCES users (id),
    created_at       timestamptz   NOT NULL DEFAULT now(),
    sent_by          bigint        REFERENCES users (id),
    sent_at          timestamptz,
    voided_by        bigint        REFERENCES users (id),
    voided_at        timestamptz,
    void_approved_by bigint        REFERENCES users (id),
    void_reason      varchar(200),
    CONSTRAINT pos_bill_lines_property_fk FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_bill_lines_bill_fk     FOREIGN KEY (property_id, bill_id)          REFERENCES pos_bills (property_id, id),
    CONSTRAINT pos_bill_lines_product_fk  FOREIGN KEY (property_id, product_id)       REFERENCES pos_products (property_id, id),
    CONSTRAINT pos_bill_lines_code_fk     FOREIGN KEY (property_id, charge_code_id)   REFERENCES charge_codes (property_id, id),
    CONSTRAINT pos_bill_lines_round_fk    FOREIGN KEY (property_id, round_id)         REFERENCES pos_rounds (property_id, id),
    CONSTRAINT pos_bill_lines_item_fk     FOREIGN KEY (property_id, folio_item_id)    REFERENCES folio_items (property_id, id),
    CONSTRAINT pos_bill_lines_voided_fk   FOREIGN KEY (property_id, voided_item_id)   REFERENCES folio_items (property_id, id),
    CONSTRAINT pos_bill_lines_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_bill_lines_uuid_uk     UNIQUE (bill_id, client_uuid),
    CONSTRAINT pos_bill_lines_item_uk     UNIQUE (property_id, folio_item_id),
    CONSTRAINT pos_bill_lines_status_ck   CHECK (status IN ('DRAFT', 'SENT', 'VOID')),
    CONSTRAINT pos_bill_lines_qty_ck      CHECK (quantity > 0),
    CONSTRAINT pos_bill_lines_price_ck    CHECK (unit_price >= 0),
    CONSTRAINT pos_bill_lines_discount_ck CHECK (discount_amount >= 0 AND (discount_pct IS NULL OR discount_pct BETWEEN 0 AND 100)),
    CONSTRAINT pos_bill_lines_posted_ck   CHECK ((status = 'DRAFT') = (folio_item_id IS NULL)),
    CONSTRAINT pos_bill_lines_void_ck     CHECK ((status = 'VOID') = (voided_at IS NOT NULL))
);
CREATE INDEX pos_bill_lines_bill_idx ON pos_bill_lines (property_id, bill_id, created_at, client_uuid);
-- A sent or voided line is what the ledger says it was: its product, quantity, price and discount never change, and only a draft line is deleted. A split or a merge moves a line to another bill
-- (bill_id, round_id and folio_item_id follow its copy), and a sent line becomes void once.
-- +goose StatementBegin
CREATE FUNCTION pos_bill_lines_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        IF OLD.status <> 'DRAFT' THEN
            RAISE EXCEPTION 'a sent or voided line of a bill is never deleted' USING ERRCODE = 'restrict_violation', CONSTRAINT = 'pos_bill_lines_no_delete';
        END IF;
        RETURN OLD;
    END IF;
    IF OLD.status <> 'DRAFT' AND (NEW.product_id <> OLD.product_id OR NEW.quantity <> OLD.quantity OR NEW.unit_price <> OLD.unit_price
                                  OR NEW.discount_amount <> OLD.discount_amount OR NEW.charge_code_id <> OLD.charge_code_id OR NEW.options <> OLD.options) THEN
        RAISE EXCEPTION 'a sent line of a bill cannot be changed' USING ERRCODE = 'restrict_violation', CONSTRAINT = 'pos_bill_lines_immutable';
    END IF;
    IF OLD.status = 'VOID' AND NEW.status <> 'VOID' THEN
        RAISE EXCEPTION 'a voided line stays voided' USING ERRCODE = 'restrict_violation', CONSTRAINT = 'pos_bill_lines_immutable';
    END IF;
    IF OLD.status = 'SENT' AND NEW.status = 'DRAFT' THEN
        RAISE EXCEPTION 'a sent line does not go back to draft' USING ERRCODE = 'restrict_violation', CONSTRAINT = 'pos_bill_lines_immutable';
    END IF;
    RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_bill_lines_guard BEFORE UPDATE OR DELETE ON pos_bill_lines FOR EACH ROW EXECUTE FUNCTION pos_bill_lines_guard();

CREATE TABLE pos_tickets (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    round_id      bigint      NOT NULL,
    bill_id       bigint      NOT NULL,
    station_id    bigint,
    kind          varchar(6)  NOT NULL DEFAULT 'NEW',
    status        varchar(10) NOT NULL DEFAULT 'NEW',
    created_at    timestamptz NOT NULL DEFAULT now(),
    started_at    timestamptz,
    ready_at      timestamptz,
    served_at     timestamptz,
    CONSTRAINT pos_tickets_property_fk FOREIGN KEY (tenant_id, property_id)   REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_tickets_round_fk    FOREIGN KEY (property_id, round_id)    REFERENCES pos_rounds (property_id, id),
    CONSTRAINT pos_tickets_bill_fk     FOREIGN KEY (property_id, bill_id)     REFERENCES pos_bills (property_id, id),
    CONSTRAINT pos_tickets_station_fk  FOREIGN KEY (property_id, station_id)  REFERENCES pos_stations (property_id, id),
    CONSTRAINT pos_tickets_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_tickets_kind_ck     CHECK (kind IN ('NEW', 'CHANGE', 'VOID')),
    CONSTRAINT pos_tickets_status_ck   CHECK (status IN ('NEW', 'COOKING', 'READY', 'SERVED', 'CANCELLED'))
);
CREATE INDEX pos_tickets_bill_idx ON pos_tickets (property_id, bill_id);

CREATE TABLE pos_ticket_lines (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint        NOT NULL,
    property_id   bigint        NOT NULL,
    ticket_id     bigint        NOT NULL,
    bill_line_id  bigint        NOT NULL,
    quantity      numeric(10,3) NOT NULL,
    name          varchar(100)  NOT NULL,
    options_text  varchar(300),
    note          varchar(200),
    is_void       boolean       NOT NULL DEFAULT false,
    CONSTRAINT pos_ticket_lines_property_fk FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_ticket_lines_ticket_fk   FOREIGN KEY (property_id, ticket_id)    REFERENCES pos_tickets (property_id, id),
    CONSTRAINT pos_ticket_lines_line_fk     FOREIGN KEY (property_id, bill_line_id) REFERENCES pos_bill_lines (property_id, id)
);
CREATE INDEX pos_ticket_lines_ticket_idx ON pos_ticket_lines (property_id, ticket_id);

-- "Dipesan": a table held for a party, not linked to a reservation of the PMS.
CREATE TABLE pos_table_holds (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint       NOT NULL,
    property_id   bigint       NOT NULL,
    table_id      bigint       NOT NULL,
    business_date date         NOT NULL,
    from_time     time         NOT NULL,
    party_size    smallint,
    name          varchar(100) NOT NULL,
    phone         varchar(40),
    note          varchar(200),
    status        varchar(9)   NOT NULL DEFAULT 'HELD',
    created_at    timestamptz  NOT NULL DEFAULT now(),
    created_by    bigint REFERENCES users (id),
    updated_at    timestamptz  NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    CONSTRAINT pos_table_holds_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_table_holds_table_fk    FOREIGN KEY (property_id, table_id)   REFERENCES pos_tables (property_id, id),
    CONSTRAINT pos_table_holds_status_ck   CHECK (status IN ('HELD', 'SEATED', 'CANCELLED')),
    CONSTRAINT pos_table_holds_name_ck     CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_table_holds_party_ck    CHECK (party_size IS NULL OR party_size > 0)
);
CREATE INDEX pos_table_holds_day_idx ON pos_table_holds (property_id, business_date, table_id) WHERE status = 'HELD';
CREATE TRIGGER pos_table_holds_set_updated_at BEFORE UPDATE ON pos_table_holds FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The links that waited for the bills (00080).
ALTER TABLE pos_room_charges ADD CONSTRAINT pos_room_charges_property_id_uk UNIQUE (property_id, id);
ALTER TABLE pos_room_charges ADD CONSTRAINT pos_room_charges_bill_fk FOREIGN KEY (property_id, bill_id) REFERENCES pos_bills (property_id, id);
ALTER TABLE pos_room_charge_attempts ADD CONSTRAINT pos_room_charge_attempts_bill_fk FOREIGN KEY (property_id, bill_id) REFERENCES pos_bills (property_id, id);
ALTER TABLE pos_room_charge_attempts ADD CONSTRAINT pos_room_charge_attempts_charge_fk FOREIGN KEY (property_id, pos_room_charge_id) REFERENCES pos_room_charges (property_id, id);
CREATE INDEX pos_room_charges_bill_idx ON pos_room_charges (property_id, bill_id) WHERE bill_id IS NOT NULL;

-- +goose Down
-- Refused while anything of the stage exists: a bill, an OUTLET folio, a POS item, a COMP payment, a ROUNDING line or a transfer of an outlet. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM pos_bills;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % bill(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM folios WHERE folio_type = 'OUTLET';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % OUTLET folio(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM folio_items WHERE source = 'POS';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % ledger line(s) were posted by POS', v_n; END IF;
    SELECT count(*) INTO v_n FROM payments WHERE payment_method = 'COMP';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % complimentary payment(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM folio_items i JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id WHERE c.charge_type = 'ROUNDING';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % ledger line(s) are of the code POS_ROUNDING', v_n; END IF;
    SELECT count(*) INTO v_n FROM folio_transfer_batches WHERE scope IN ('OUTLET_BILL', 'OUTLET_CHARGE');
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % transfer(s) of an outlet exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM companies WHERE pos_billing_allowed;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % company(ies) are allowed to be billed by outlets', v_n; END IF;
    SELECT count(*) INTO v_n FROM pos_room_charges WHERE bill_id IS NOT NULL;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00081: % outlet charge(s) to a room belong to a bill', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX pos_room_charges_bill_idx;
ALTER TABLE pos_room_charge_attempts DROP CONSTRAINT pos_room_charge_attempts_charge_fk;
ALTER TABLE pos_room_charge_attempts DROP CONSTRAINT pos_room_charge_attempts_bill_fk;
ALTER TABLE pos_room_charges DROP CONSTRAINT pos_room_charges_bill_fk;
ALTER TABLE pos_room_charges DROP CONSTRAINT pos_room_charges_property_id_uk;
DROP TABLE pos_table_holds;
DROP TABLE pos_ticket_lines;
DROP TABLE pos_tickets;
DROP TRIGGER pos_bill_lines_guard ON pos_bill_lines;
DROP TABLE pos_bill_lines;
DROP FUNCTION pos_bill_lines_guard();
DROP TABLE pos_rounds;
DROP TABLE pos_bills;

-- The keys and accounts of each property are found through their keys (not through their codes, which may be replacements). An account goes only when it is still what the migration made
-- (its standard name) and nothing uses it.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_code text;
BEGIN
    FOR r IN SELECT m.property_id, m.account_id FROM gl_account_map m WHERE m.map_key = 'COMP' ORDER BY m.property_id LOOP
        DELETE FROM gl_account_map WHERE property_id = r.property_id AND map_key = 'COMP';
        BEGIN
            DELETE FROM gl_accounts a
             WHERE a.property_id = r.property_id AND a.id = r.account_id AND a.name IN ('Complimentary and house use', 'Komplimen dan pemakaian internal')
               AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = a.property_id AND l.account_id = a.id);
        EXCEPTION WHEN foreign_key_violation THEN
            NULL;
        END;
    END LOOP;
    FOR r IN SELECT c.property_id, c.gl_account_code FROM charge_codes c WHERE c.charge_type = 'ROUNDING' AND c.gl_account_code IS NOT NULL LOOP
        v_code := r.gl_account_code;
        DELETE FROM charge_codes WHERE property_id = r.property_id AND charge_type = 'ROUNDING';
        BEGIN
            DELETE FROM gl_accounts a
             WHERE a.property_id = r.property_id AND a.code = v_code AND a.name IN ('Rounding', 'Pembulatan')
               AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = a.property_id AND l.account_id = a.id);
        EXCEPTION WHEN foreign_key_violation THEN
            NULL;
        END;
    END LOOP;
    DELETE FROM charge_codes WHERE charge_type = 'ROUNDING';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION seed_charge_codes(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode, is_system, created_by, updated_by)
    SELECT p_tenant_id, p_property_id, s.code, s.name, s.charge_type, 'EXCLUSIVE', true, p_actor_id, p_actor_id
      FROM (VALUES
        ('ROOM',        'Room charge',                  'ROOM'),
        ('ROOM_EXEMPT', 'Room charge (no tax or service)', 'ROOM'),
        ('BREAKFAST',   'Breakfast',                    'FOOD_BEVERAGE'),
        ('RESTAURANT',  'Restaurant',                   'FOOD_BEVERAGE'),
        ('LAUNDRY',     'Laundry',                      'SERVICE'),
        ('MINIBAR',     'Minibar',                      'FOOD_BEVERAGE'),
        ('EXTRA_BED',   'Extra bed',                    'SERVICE'),
        ('NO_SHOW_FEE', 'No-show fee',                  'FEE'),
        ('CANCEL_FEE',  'Cancellation fee',             'FEE'),
        ('OTHER',       'Other charges',                'OTHER'),
        ('TRF',         'Transfer balance',             'TRANSFER')
      ) AS s (code, name, charge_type)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd
DROP FUNCTION charge_code_names(text);
ALTER FUNCTION charge_code_names_v2(text) RENAME TO charge_code_names;
DROP FUNCTION usali_chart_names(text);
ALTER FUNCTION usali_chart_names_v3(text) RENAME TO usali_chart_names;
DROP FUNCTION usali_chart();
ALTER FUNCTION usali_chart_v5() RENAME TO usali_chart;

CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND CASE WHEN f.folio_type = 'MASTER'
                 THEN NOT EXISTS (SELECT 1 FROM folio_items c
                                   WHERE c.property_id = fi.property_id AND c.folio_id = fi.folio_id AND c.transaction_type = 'CHARGE'
                                     AND c.id < (SELECT min(pi.id) FROM folio_items pi WHERE pi.property_id = rp.property_id AND pi.payment_id = rp.id)
                                     AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = c.property_id AND rv.reverses_item_id = c.id))
                 ELSE (st.id IS NULL OR rp.paid_at < st.actual_check_in_at) END) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference,
       COALESCE(fi.department_id, o.department_id) AS department_id
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;

ALTER TABLE companies DROP COLUMN pos_billing_allowed;

ALTER TABLE folio_transfer_batches DROP CONSTRAINT folio_transfer_batches_approval_ck;
ALTER TABLE folio_transfer_batches DROP CONSTRAINT folio_transfer_batches_scope_ck;
ALTER TABLE folio_transfer_batches ADD CONSTRAINT folio_transfer_batches_scope_ck CHECK (scope IN ('RESERVATION', 'GROUP_MEMBER', 'GROUP_MASTER', 'ANY', 'RETURN'));
ALTER TABLE folio_transfer_batches ALTER COLUMN approved_by SET NOT NULL;

ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER'));
ALTER TABLE payments DROP CONSTRAINT payments_comp_ck;
ALTER TABLE payments DROP CONSTRAINT payments_method_ck;
ALTER TABLE payments ADD CONSTRAINT payments_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'CITY_LEDGER', 'QRIS'));
ALTER TABLE charge_codes DROP CONSTRAINT charge_codes_type_ck;
ALTER TABLE charge_codes ADD CONSTRAINT charge_codes_type_ck CHECK (charge_type IN ('ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER', 'TRANSFER'));
DROP INDEX folio_items_pos_line_uk;
ALTER TABLE payments DROP CONSTRAINT payments_approval_ck;
ALTER TABLE payments ADD CONSTRAINT payments_approval_ck CHECK (((payment_type = 'REFUND') OR (status = 'VOIDED')) = (approved_by IS NOT NULL));
ALTER TABLE folio_items DROP CONSTRAINT folio_items_approval_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_approval_ck CHECK ((transaction_type IN ('ADJUSTMENT', 'REVERSAL')) = (approved_by IS NOT NULL));
ALTER TABLE folio_items DROP CONSTRAINT folio_items_source_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_source_ck CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION', 'TRANSFER'));

DROP INDEX folios_outlet_open_idx;
ALTER TABLE folios DROP CONSTRAINT folios_reservation_ck;
ALTER TABLE folios
    DROP CONSTRAINT folios_outlet_shape_ck,
    DROP CONSTRAINT folios_outlet_ck,
    DROP CONSTRAINT folios_type_ck,
    DROP CONSTRAINT folios_outlet_fk;
ALTER TABLE folios
    ADD CONSTRAINT folios_type_ck        CHECK (folio_type IN ('GUEST', 'COMPANY', 'MASTER')),
    ADD CONSTRAINT folios_reservation_ck CHECK (folio_type = 'MASTER' OR reservation_id IS NOT NULL);
ALTER TABLE folios DROP COLUMN outlet_id;
