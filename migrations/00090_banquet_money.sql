-- +goose Up
-- Banquet, PR 2 (docs/architecture/36-banquet.md, sections 3.1, 3.3, 4.4 to 4.8, 5): the money of an event.
--
--   folios                     folio type EVENT with its event (no stay, reservation, group or outlet); one OPEN folio for an event (F2)
--   folio_item_gl (view)       the role-based is_deposit covers EVENT as it covers MASTER and OUTLET (F3)
--   folio_items                source BANQUET (F4)
--   folio_transfer_batches     scope EVENT_GROUP: an event's bill and the master folio of its group, or the folio of a member (F15)
--   charge_codes               BQT_FB, BQT_ROOM, BQT_EQUIPMENT, BQT_DECOR, BQT_OTHER, BQT_FORFEIT (data, as TRF was): new properties by the seed, existing ones here
--   banquet_settings           posting mode, price mode, the discount that needs no approval
--   banquet_events             posting mode of the event, the base of the cancellation percent, the estimate, the fee that was posted
--   banquet_event_lines        what is sold in a session, with the price mode frozen when the line is added; banquet_line_components the split of a package
--   banquet_session_postings   a session is posted once (session_id is unique); banquet_posting_items what each folio item came from
--   banquet_deposit_schedule   the deposits of an event; banquet_policy_templates, _deposits and _tiers the starting point copied to an event; banquet_policy_tiers the tiers of an event
--   banquet_notices            the kind DEPOSIT_DUE

-- 1. The EVENT folio ------------------------------------------------------------------------------------------------------------------------------
ALTER TABLE folios ADD COLUMN banquet_event_id bigint;
ALTER TABLE folios ADD CONSTRAINT folios_event_fk FOREIGN KEY (property_id, banquet_event_id) REFERENCES banquet_events (property_id, id);
ALTER TABLE folios DROP CONSTRAINT folios_type_ck;
ALTER TABLE folios DROP CONSTRAINT folios_reservation_ck;
ALTER TABLE folios
    ADD CONSTRAINT folios_type_ck         CHECK (folio_type IN ('GUEST', 'COMPANY', 'MASTER', 'OUTLET', 'EVENT')),
    ADD CONSTRAINT folios_event_ck        CHECK ((folio_type = 'EVENT') = (banquet_event_id IS NOT NULL)),
    ADD CONSTRAINT folios_event_shape_ck  CHECK (folio_type <> 'EVENT' OR (stay_id IS NULL AND reservation_id IS NULL AND booking_group_id IS NULL AND outlet_id IS NULL)),
    ADD CONSTRAINT folios_reservation_ck  CHECK (folio_type IN ('MASTER', 'OUTLET', 'EVENT') OR reservation_id IS NOT NULL);
-- at most one OPEN folio for an event: the loser of two parallel "Accept deposit" gets this index
CREATE UNIQUE INDEX folios_event_open_uk ON folios (banquet_event_id) WHERE folio_type = 'EVENT' AND status = 'OPEN';
CREATE INDEX folios_event_idx ON folios (property_id, banquet_event_id) WHERE folio_type = 'EVENT';

ALTER TABLE folio_items DROP CONSTRAINT folio_items_source_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_source_ck CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION', 'TRANSFER', 'POS', 'BANQUET'));

ALTER TABLE folio_transfer_batches DROP CONSTRAINT folio_transfer_batches_scope_ck;
ALTER TABLE folio_transfer_batches ADD CONSTRAINT folio_transfer_batches_scope_ck
    CHECK (scope IN ('RESERVATION', 'GROUP_MEMBER', 'GROUP_MASTER', 'ANY', 'RETURN', 'OUTLET_BILL', 'OUTLET_CHARGE', 'EVENT_GROUP'));

-- The view: the folio of an event has no stay either, so a payment on it is a deposit only when it was taken before the first live CHARGE row of the folio.
CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND CASE WHEN f.folio_type IN ('MASTER', 'OUTLET', 'EVENT')
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

-- 2. The charge codes of a NEW property -----------------------------------------------------------------------------------------------------------
ALTER FUNCTION charge_code_names(text) RENAME TO charge_code_names_v3;
-- +goose StatementBegin
CREATE FUNCTION charge_code_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM charge_code_names_v3(p_lang)
    UNION ALL
    SELECT n.code, n.name FROM (VALUES
        ('BQT_FB',        'Banquet food and beverage'),
        ('BQT_ROOM',      'Meeting room rental'),
        ('BQT_EQUIPMENT', 'Banquet equipment'),
        ('BQT_DECOR',     'Banquet decoration'),
        ('BQT_OTHER',     'Banquet other charges'),
        ('BQT_FORFEIT',   'Forfeited deposit')
    ) AS n (code, name) WHERE p_lang = 'en'
    UNION ALL
    SELECT n.code, n.name FROM (VALUES
        ('BQT_FB',        'Makanan dan minuman banquet'),
        ('BQT_ROOM',      'Sewa ruang pertemuan'),
        ('BQT_EQUIPMENT', 'Sewa alat banquet'),
        ('BQT_DECOR',     'Dekorasi banquet'),
        ('BQT_OTHER',     'Biaya banquet lainnya'),
        ('BQT_FORFEIT',   'Deposit hangus')
    ) AS n (code, name) WHERE p_lang = 'id'
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
        ('POS_ROUNDING', 'POS rounding',                'ROUNDING'),
        ('BQT_FB',        'Banquet food and beverage',  'FOOD_BEVERAGE'),
        ('BQT_ROOM',      'Meeting room rental',        'OTHER'),
        ('BQT_EQUIPMENT', 'Banquet equipment',          'OTHER'),
        ('BQT_DECOR',     'Banquet decoration',         'OTHER'),
        ('BQT_OTHER',     'Banquet other charges',      'OTHER'),
        ('BQT_FORFEIT',   'Forfeited deposit',          'FEE')
      ) AS s (code, name, charge_type)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- The properties that exist get the six codes, their accounts (4240 F&B, 4410 rooms and equipment, 4590 the rest, 4520 forfeited deposits: only when the account exists, and the account of a
-- code the property already has is not touched) and, for the F&B code, the department of food and beverage. The names are Indonesian when the standard code OTHER has its Indonesian name.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_idn boolean;
BEGIN
    FOR r IN SELECT p.tenant_id, p.id AS property_id FROM properties p ORDER BY p.id LOOP
        v_idn := EXISTS (SELECT 1 FROM charge_codes c WHERE c.property_id = r.property_id AND c.code = 'OTHER' AND c.is_system AND c.name = 'Biaya lain-lain');
        PERFORM seed_charge_codes(r.tenant_id, r.property_id, NULL);
        IF v_idn THEN
            UPDATE charge_codes c SET name = n.name
              FROM charge_code_names('id') n
             WHERE c.property_id = r.property_id AND c.code = n.code AND c.code LIKE 'BQT\_%' AND c.is_system;
        END IF;
        UPDATE charge_codes c SET gl_account_code = d.code
          FROM (VALUES ('BQT_FB', '4240'), ('BQT_ROOM', '4410'), ('BQT_EQUIPMENT', '4410'), ('BQT_DECOR', '4590'), ('BQT_OTHER', '4590'), ('BQT_FORFEIT', '4520')) AS d (charge_code, code)
         WHERE c.property_id = r.property_id AND c.code = d.charge_code AND c.gl_account_code IS NULL
           AND EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = c.property_id AND a.code = d.code);
        UPDATE charge_codes c SET department_id = dp.id
          FROM (VALUES ('BQT_FB', 'FB'), ('BQT_ROOM', 'OOD'), ('BQT_EQUIPMENT', 'OOD'), ('BQT_DECOR', 'OOD'), ('BQT_OTHER', 'OOD')) AS m (charge_code, department_code)
          JOIN departments dp ON dp.property_id = r.property_id AND dp.code = m.department_code
         WHERE c.property_id = r.property_id AND c.code = m.charge_code AND c.department_id IS NULL;
    END LOOP;
END
$$;
-- +goose StatementEnd

-- 3. The settings and the event --------------------------------------------------------------------------------------------------------------------
ALTER TABLE banquet_settings
    ADD COLUMN posting_mode                  varchar(6)   NOT NULL DEFAULT 'AUTO',
    ADD COLUMN default_price_mode            varchar(9)   NOT NULL DEFAULT 'EXCLUSIVE',
    ADD COLUMN max_discount_pct_no_approval  numeric(5,2) NOT NULL DEFAULT 0,
    ADD CONSTRAINT banquet_settings_posting_ck  CHECK (posting_mode IN ('AUTO', 'MANUAL')),
    ADD CONSTRAINT banquet_settings_price_ck    CHECK (default_price_mode IN ('EXCLUSIVE', 'INCLUSIVE')),
    ADD CONSTRAINT banquet_settings_discount_ck CHECK (max_discount_pct_no_approval BETWEEN 0 AND 100);

ALTER TABLE banquet_events
    ADD COLUMN posting_mode           varchar(6),
    ADD COLUMN policy_base            varchar(5),
    ADD COLUMN contract_value_estimate numeric(18,3) NOT NULL DEFAULT 0,
    ADD COLUMN cancelled_fee_item_id  bigint,
    ADD CONSTRAINT banquet_events_posting_ck    CHECK (posting_mode IS NULL OR posting_mode IN ('AUTO', 'MANUAL')),
    ADD CONSTRAINT banquet_events_base_ck       CHECK (policy_base IS NULL OR policy_base IN ('NET', 'GROSS')),
    ADD CONSTRAINT banquet_events_fee_item_fk   FOREIGN KEY (property_id, cancelled_fee_item_id) REFERENCES folio_items (property_id, id);

-- 4. The lines --------------------------------------------------------------------------------------------------------------------------------------
-- A line is what is sold in a session: an item or a package. The price and its mode are frozen when the line is added (B6). For a line per guest the quantity is a multiplier of the billed
-- guests (1 for a package); for the others it is the count (a session, hours, units).
CREATE TABLE banquet_event_lines (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint        NOT NULL,
    property_id       bigint        NOT NULL,
    event_id          bigint        NOT NULL,
    session_id        bigint        NOT NULL,
    line_no           int           NOT NULL,
    item_id           bigint,
    package_id        bigint,
    description       varchar(200)  NOT NULL,
    basis             varchar(11)   NOT NULL,
    quantity          numeric(10,3) NOT NULL,
    unit_price        numeric(18,3) NOT NULL,
    price_mode        varchar(9)    NOT NULL,
    discount_pct      numeric(5,2),
    discount_amount   numeric(18,3),
    discount_by       bigint REFERENCES users (id),
    discount_approved_by bigint REFERENCES users (id),
    charge_code_id    bigint,
    fb_class          varchar(8),
    pos_product_id    bigint,
    is_active         boolean       NOT NULL DEFAULT true,
    created_at        timestamptz   NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    updated_at        timestamptz   NOT NULL DEFAULT now(),
    updated_by        bigint REFERENCES users (id),
    CONSTRAINT banquet_event_lines_property_fk    FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_event_lines_event_fk       FOREIGN KEY (property_id, event_id)        REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_event_lines_session_fk     FOREIGN KEY (property_id, session_id)      REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_event_lines_item_fk        FOREIGN KEY (property_id, item_id)         REFERENCES banquet_items (property_id, id),
    CONSTRAINT banquet_event_lines_package_fk     FOREIGN KEY (property_id, package_id)      REFERENCES banquet_packages (property_id, id),
    CONSTRAINT banquet_event_lines_charge_code_fk FOREIGN KEY (property_id, charge_code_id)  REFERENCES charge_codes (property_id, id),
    CONSTRAINT banquet_event_lines_product_fk     FOREIGN KEY (property_id, pos_product_id)  REFERENCES pos_products (property_id, id),
    CONSTRAINT banquet_event_lines_no_uk          UNIQUE (event_id, line_no),
    CONSTRAINT banquet_event_lines_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_event_lines_what_ck        CHECK ((item_id IS NULL) <> (package_id IS NULL)),
    CONSTRAINT banquet_event_lines_code_ck        CHECK ((item_id IS NULL) = (charge_code_id IS NULL)),
    CONSTRAINT banquet_event_lines_basis_ck       CHECK (basis IN ('PER_PAX', 'PER_SESSION', 'PER_HOUR', 'PER_UNIT')),
    CONSTRAINT banquet_event_lines_package_basis_ck CHECK (package_id IS NULL OR basis = 'PER_PAX'),
    CONSTRAINT banquet_event_lines_quantity_ck    CHECK (quantity > 0),
    CONSTRAINT banquet_event_lines_price_ck       CHECK (unit_price >= 0),
    CONSTRAINT banquet_event_lines_mode_ck        CHECK (price_mode IN ('EXCLUSIVE', 'INCLUSIVE')),
    CONSTRAINT banquet_event_lines_class_ck       CHECK (fb_class IS NULL OR fb_class IN ('FOOD', 'BEVERAGE', 'OTHER')),
    CONSTRAINT banquet_event_lines_discount_ck    CHECK ((discount_pct IS NULL OR discount_amount IS NULL)
                                                         AND (discount_pct IS NULL OR discount_pct BETWEEN 0 AND 100)
                                                         AND (discount_amount IS NULL OR discount_amount >= 0)),
    CONSTRAINT banquet_event_lines_who_ck         CHECK ((discount_pct IS NULL AND discount_amount IS NULL) = (discount_by IS NULL))
);
CREATE INDEX banquet_event_lines_session_idx ON banquet_event_lines (property_id, session_id) WHERE is_active;
CREATE TRIGGER banquet_event_lines_set_updated_at BEFORE UPDATE ON banquet_event_lines FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- The snapshot of a package when it is sold: a later edit of the master changes nothing (5.2).
CREATE TABLE banquet_line_components (
    property_id    bigint        NOT NULL,
    line_id        bigint        NOT NULL,
    item_id        bigint        NOT NULL,
    name           varchar(100)  NOT NULL,
    charge_code_id bigint        NOT NULL,
    qty_per_pax    numeric(10,3) NOT NULL,
    list_price     numeric(18,3) NOT NULL,
    fb_class       varchar(8),
    pos_product_id bigint,
    sort_order     int           NOT NULL DEFAULT 0,
    PRIMARY KEY (line_id, item_id),
    CONSTRAINT banquet_line_components_line_fk        FOREIGN KEY (property_id, line_id)        REFERENCES banquet_event_lines (property_id, id),
    CONSTRAINT banquet_line_components_item_fk        FOREIGN KEY (property_id, item_id)        REFERENCES banquet_items (property_id, id),
    CONSTRAINT banquet_line_components_charge_code_fk FOREIGN KEY (property_id, charge_code_id) REFERENCES charge_codes (property_id, id),
    CONSTRAINT banquet_line_components_product_fk     FOREIGN KEY (property_id, pos_product_id) REFERENCES pos_products (property_id, id),
    CONSTRAINT banquet_line_components_qty_ck         CHECK (qty_per_pax > 0),
    CONSTRAINT banquet_line_components_price_ck       CHECK (list_price >= 0),
    CONSTRAINT banquet_line_components_class_ck       CHECK (fb_class IS NULL OR fb_class IN ('FOOD', 'BEVERAGE', 'OTHER'))
);

-- 5. The posting of a session ---------------------------------------------------------------------------------------------------------------------
-- A session is posted once: session_id is unique, so the loser of two parallel requests gets the key (SESSION_ALREADY_POSTED). The row is dated by the business day, so it takes the trigger
-- of every posting table (00065); the folio items it makes have it already.
CREATE TABLE banquet_session_postings (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    session_id    bigint      NOT NULL,
    folio_id      bigint      NOT NULL,
    business_date date        NOT NULL,
    pax_billed    int         NOT NULL,
    pax_basis     varchar(10) NOT NULL,
    auto          boolean     NOT NULL,
    on_guarantee  boolean     NOT NULL DEFAULT false,
    posted_by     bigint REFERENCES users (id),
    posted_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT banquet_session_postings_property_fk    FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_session_postings_session_fk     FOREIGN KEY (property_id, session_id)   REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_session_postings_folio_fk       FOREIGN KEY (property_id, folio_id)     REFERENCES folios (property_id, id),
    CONSTRAINT banquet_session_postings_day_fk         FOREIGN KEY (property_id, business_date) REFERENCES business_days (property_id, business_date),
    CONSTRAINT banquet_session_postings_session_uk     UNIQUE (session_id),
    CONSTRAINT banquet_session_postings_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_session_postings_pax_ck         CHECK (pax_billed >= 0),
    CONSTRAINT banquet_session_postings_basis_ck       CHECK (pax_basis IN ('ACTUAL', 'GUARANTEED', 'ESTIMATED'))
);
CREATE INDEX banquet_session_postings_day_idx ON banquet_session_postings (property_id, business_date);
CREATE TRIGGER banquet_session_postings_business_day_open BEFORE INSERT ON banquet_session_postings FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');

CREATE TABLE banquet_posting_items (
    property_id       bigint NOT NULL,
    posting_id        bigint NOT NULL,
    line_id           bigint NOT NULL,
    component_item_id bigint,
    folio_item_id     bigint NOT NULL,
    CONSTRAINT banquet_posting_items_posting_fk    FOREIGN KEY (property_id, posting_id)    REFERENCES banquet_session_postings (property_id, id),
    CONSTRAINT banquet_posting_items_line_fk       FOREIGN KEY (property_id, line_id)       REFERENCES banquet_event_lines (property_id, id),
    CONSTRAINT banquet_posting_items_folio_item_fk FOREIGN KEY (property_id, folio_item_id) REFERENCES folio_items (property_id, id),
    CONSTRAINT banquet_posting_items_folio_item_uk UNIQUE (folio_item_id)
);
CREATE INDEX banquet_posting_items_posting_idx ON banquet_posting_items (property_id, posting_id);

-- 6. Deposits and the cancellation policy ----------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_deposit_schedule (
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    event_id    bigint        NOT NULL,
    seq         int           NOT NULL,
    label       varchar(100)  NOT NULL,
    due_date    date          NOT NULL,
    percent     numeric(5,2),
    amount      numeric(18,3) NOT NULL,
    PRIMARY KEY (event_id, seq),
    CONSTRAINT banquet_deposit_schedule_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_deposit_schedule_event_fk    FOREIGN KEY (property_id, event_id)  REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_deposit_schedule_percent_ck  CHECK (percent IS NULL OR percent BETWEEN 0 AND 100),
    CONSTRAINT banquet_deposit_schedule_amount_ck   CHECK (amount >= 0)
);

CREATE TABLE banquet_policy_templates (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code        varchar(20)  NOT NULL,
    name        varchar(100) NOT NULL,
    is_default  boolean      NOT NULL DEFAULT false,
    base        varchar(5)   NOT NULL DEFAULT 'NET',
    is_active   boolean      NOT NULL DEFAULT true,
    CONSTRAINT banquet_policy_templates_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_policy_templates_code_uk        UNIQUE (property_id, code),
    CONSTRAINT banquet_policy_templates_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_policy_templates_base_ck        CHECK (base IN ('NET', 'GROSS'))
);
CREATE UNIQUE INDEX banquet_policy_templates_default_uk ON banquet_policy_templates (property_id) WHERE is_default;

-- The deposits a template asks for: a percent of the base, due so many days before the first session (0 = on the day of the signature, the confirmation).
CREATE TABLE banquet_policy_deposits (
    property_id bigint        NOT NULL,
    template_id bigint        NOT NULL,
    seq         int           NOT NULL,
    label       varchar(100)  NOT NULL,
    days_before int           NULL,
    percent     numeric(5,2)  NOT NULL,
    PRIMARY KEY (template_id, seq),
    CONSTRAINT banquet_policy_deposits_template_fk FOREIGN KEY (property_id, template_id) REFERENCES banquet_policy_templates (property_id, id),
    CONSTRAINT banquet_policy_deposits_days_ck     CHECK (days_before IS NULL OR days_before >= 0),
    CONSTRAINT banquet_policy_deposits_percent_ck  CHECK (percent > 0 AND percent <= 100)
);

-- The tiers of a template, and the tiers of an event (a copy made at Definite): a tier applies when the days before the first session are from days_from to days_to (no end: from days_from up).
CREATE TABLE banquet_policy_tiers (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    template_id bigint,
    event_id    bigint,
    days_from   int          NOT NULL,
    days_to     int,
    percent     numeric(5,2) NOT NULL,
    action      varchar(7)   NOT NULL DEFAULT 'FEE',
    CONSTRAINT banquet_policy_tiers_property_fk FOREIGN KEY (tenant_id, property_id)     REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_policy_tiers_template_fk FOREIGN KEY (property_id, template_id)   REFERENCES banquet_policy_templates (property_id, id),
    CONSTRAINT banquet_policy_tiers_event_fk    FOREIGN KEY (property_id, event_id)      REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_policy_tiers_owner_ck    CHECK ((template_id IS NULL) <> (event_id IS NULL)),
    CONSTRAINT banquet_policy_tiers_days_ck     CHECK (days_from >= 0 AND (days_to IS NULL OR days_to >= days_from)),
    CONSTRAINT banquet_policy_tiers_percent_ck  CHECK (percent BETWEEN 0 AND 100),
    CONSTRAINT banquet_policy_tiers_action_ck   CHECK (action IN ('FEE', 'FORFEIT'))
);
CREATE UNIQUE INDEX banquet_policy_tiers_template_uk ON banquet_policy_tiers (template_id, days_from) WHERE template_id IS NOT NULL;
CREATE UNIQUE INDEX banquet_policy_tiers_event_uk    ON banquet_policy_tiers (event_id, days_from) WHERE event_id IS NOT NULL;

-- 7. The reminder of a deposit ---------------------------------------------------------------------------------------------------------------------
ALTER TABLE banquet_notices DROP CONSTRAINT banquet_notices_kind_ck;
ALTER TABLE banquet_notices ADD CONSTRAINT banquet_notices_kind_ck CHECK (kind IN ('OPTION_PROMOTED', 'OPTION_DUE', 'OPTION_LOST', 'DEPOSIT_DUE'));
CREATE UNIQUE INDEX banquet_notices_deposit_uk ON banquet_notices (property_id, event_id, user_id, notice_date) WHERE kind = 'DEPOSIT_DUE';

-- +goose Down
-- Refused while the money of the banquet exists: an EVENT folio, a line, a posted session, an item of the source BANQUET or a transfer of the scope EVENT_GROUP. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM folios WHERE folio_type = 'EVENT';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: % EVENT folio(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM folio_items WHERE source = 'BANQUET';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: % ledger line(s) were posted by the banquet', v_n; END IF;
    SELECT count(*) INTO v_n FROM folio_transfer_batches WHERE scope = 'EVENT_GROUP';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: % transfer(s) of an event exist', v_n; END IF;
    SELECT (SELECT count(*) FROM banquet_event_lines) + (SELECT count(*) FROM banquet_session_postings) + (SELECT count(*) FROM banquet_deposit_schedule)
         + (SELECT count(*) FROM banquet_policy_templates) + (SELECT count(*) FROM banquet_policy_tiers) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: the banquet holds % row(s) of lines, postings, deposits or policies', v_n; END IF;
    SELECT count(*) INTO v_n FROM banquet_notices WHERE kind = 'DEPOSIT_DUE';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: % deposit reminder(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM banquet_events WHERE posting_mode IS NOT NULL OR policy_base IS NOT NULL OR cancelled_fee_item_id IS NOT NULL OR contract_value_estimate <> 0;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: % event(s) carry money data', v_n; END IF;
    SELECT count(*) INTO v_n FROM banquet_settings WHERE posting_mode <> 'AUTO' OR default_price_mode <> 'EXCLUSIVE' OR max_discount_pct_no_approval <> 0;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00090: % property(ies) changed the money settings', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX banquet_notices_deposit_uk;
ALTER TABLE banquet_notices DROP CONSTRAINT banquet_notices_kind_ck;
ALTER TABLE banquet_notices ADD CONSTRAINT banquet_notices_kind_ck CHECK (kind IN ('OPTION_PROMOTED', 'OPTION_DUE', 'OPTION_LOST'));

DROP TABLE banquet_policy_tiers;
DROP TABLE banquet_policy_deposits;
DROP TABLE banquet_policy_templates;
DROP TABLE banquet_deposit_schedule;
DROP TABLE banquet_posting_items;
DROP TABLE banquet_session_postings;
DROP TABLE banquet_line_components;
DROP TABLE banquet_event_lines;

ALTER TABLE banquet_events
    DROP CONSTRAINT banquet_events_fee_item_fk,
    DROP CONSTRAINT banquet_events_base_ck,
    DROP CONSTRAINT banquet_events_posting_ck,
    DROP COLUMN cancelled_fee_item_id,
    DROP COLUMN contract_value_estimate,
    DROP COLUMN policy_base,
    DROP COLUMN posting_mode;
ALTER TABLE banquet_settings
    DROP CONSTRAINT banquet_settings_discount_ck,
    DROP CONSTRAINT banquet_settings_price_ck,
    DROP CONSTRAINT banquet_settings_posting_ck,
    DROP COLUMN max_discount_pct_no_approval,
    DROP COLUMN default_price_mode,
    DROP COLUMN posting_mode;

-- The charge codes of the banquet go when nothing uses them (a code an item, a line or a rule points at stays: it is the user's by then).
-- +goose StatementBegin
DO $$
BEGIN
    DELETE FROM charge_codes c
     WHERE c.code LIKE 'BQT\_%' AND c.is_system
       AND NOT EXISTS (SELECT 1 FROM banquet_items i WHERE i.property_id = c.property_id AND i.charge_code_id = c.id)
       AND NOT EXISTS (SELECT 1 FROM folio_items f WHERE f.property_id = c.property_id AND f.charge_code_id = c.id);
EXCEPTION WHEN foreign_key_violation THEN
    NULL;
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
        ('TRF',         'Transfer balance',             'TRANSFER'),
        ('POS_ROUNDING', 'POS rounding',                'ROUNDING')
      ) AS s (code, name, charge_type)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd
DROP FUNCTION charge_code_names(text);
ALTER FUNCTION charge_code_names_v3(text) RENAME TO charge_code_names;

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

ALTER TABLE folio_transfer_batches DROP CONSTRAINT folio_transfer_batches_scope_ck;
ALTER TABLE folio_transfer_batches ADD CONSTRAINT folio_transfer_batches_scope_ck
    CHECK (scope IN ('RESERVATION', 'GROUP_MEMBER', 'GROUP_MASTER', 'ANY', 'RETURN', 'OUTLET_BILL', 'OUTLET_CHARGE'));
ALTER TABLE folio_items DROP CONSTRAINT folio_items_source_ck;
ALTER TABLE folio_items ADD CONSTRAINT folio_items_source_ck CHECK (source IN ('MANUAL', 'ROOM_POSTING', 'SYSTEM', 'INTEGRATION', 'TRANSFER', 'POS'));

DROP INDEX folios_event_idx;
DROP INDEX folios_event_open_uk;
ALTER TABLE folios DROP CONSTRAINT folios_reservation_ck;
ALTER TABLE folios
    DROP CONSTRAINT folios_event_shape_ck,
    DROP CONSTRAINT folios_event_ck,
    DROP CONSTRAINT folios_type_ck,
    DROP CONSTRAINT folios_event_fk;
ALTER TABLE folios
    ADD CONSTRAINT folios_type_ck        CHECK (folio_type IN ('GUEST', 'COMPANY', 'MASTER', 'OUTLET')),
    ADD CONSTRAINT folios_reservation_ck CHECK (folio_type IN ('MASTER', 'OUTLET') OR reservation_id IS NOT NULL);
ALTER TABLE folios DROP COLUMN banquet_event_id;
