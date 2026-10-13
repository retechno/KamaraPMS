-- +goose Up
-- Transfers between folios (docs/architecture/30-group-folio.md, part B).
--
-- 1. The balance of a folio is handed to another folio with an ordinary ledger transaction of ONE system charge code, TRF "Transfer balance": a signed adjustment (credit) on the folio it leaves and
--    a charge (debit) on the folio it goes to. The code has a charge type of its own, TRANSFER, which keeps it out of the revenue reports, out of the charges a cashier may post by hand and out
--    of the rules of tax and service charge (nothing can be mapped to it: the engine returns no component for it, as for ROOM_EXEMPT).
-- 2. Its ledger account is a system key of the account map, FOLIO_TRANSFER, like QRIS: an intermediary asset account, 1215, whose balance is zero at every day close.
-- 3. Every transfer (of charges, of a balance, and the return of either) is written to an append-only log that says where each line came from: the first folio and the folio before.

ALTER TABLE charge_codes DROP CONSTRAINT charge_codes_type_ck;
ALTER TABLE charge_codes ADD CONSTRAINT charge_codes_type_ck CHECK (charge_type IN ('ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER', 'TRANSFER'));

ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS',
                                                             'FOLIO_TRANSFER'));

-- The standard chart of a NEW property: the account 1215 under the receivables, with its Indonesian name.
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v4;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_v4()
    UNION ALL
    SELECT '1215', 'Transfer between folios', 'ASSET', 'DEBIT', '1200', true, 'RECEIVABLES'
$$;
-- +goose StatementEnd
ALTER FUNCTION usali_chart_names(text) RENAME TO usali_chart_names_v2;
-- +goose StatementBegin
CREATE FUNCTION usali_chart_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_names_v2(p_lang)
    UNION ALL
    SELECT '1215', 'Transfer antar folio' WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- The standard charge codes of a NEW property: TRF, with its Indonesian name for a property that is seeded in Indonesian.
ALTER FUNCTION charge_code_names(text) RENAME TO charge_code_names_v1;
-- +goose StatementBegin
CREATE FUNCTION charge_code_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM charge_code_names_v1(p_lang)
    UNION ALL
    SELECT 'TRF', 'Transfer balance' WHERE p_lang = 'en'
    UNION ALL
    SELECT 'TRF', 'Transfer saldo' WHERE p_lang = 'id'
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

-- The properties that exist. Each gets the charge code TRF; each that has a chart also gets the account 1215 and the key FOLIO_TRANSFER pointing at it:
--   * a property that already has the key is left as it is;
--   * with no account 1215, the account 1215 is created;
--   * with an account 1215 already (the code is the user's: it is neither used nor overwritten), the next free code from 1216 to 1299 is used.
-- The names are Indonesian when the account 1210 of the property has its Indonesian standard name, else English. The number of properties that took a replacement code is reported.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_code text;
    v_name text;
    v_id bigint;
    n int;
    v_added int := 0;
    v_replaced int := 0;
    v_kept int := 0;
    v_codes int := 0;
    v_idn boolean;
BEGIN
    FOR r IN SELECT p.tenant_id, p.id AS property_id FROM properties p ORDER BY p.id LOOP
        v_idn := EXISTS (SELECT 1 FROM charge_codes c WHERE c.property_id = r.property_id AND c.code = 'OTHER' AND c.is_system AND c.name = 'Biaya lain-lain');
        INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode, is_system)
        VALUES (r.tenant_id, r.property_id, 'TRF', CASE WHEN v_idn THEN 'Transfer saldo' ELSE 'Transfer balance' END, 'TRANSFER', 'EXCLUSIVE', true)
        ON CONFLICT (property_id, code) DO NOTHING;
        IF FOUND THEN v_codes := v_codes + 1; END IF;
    END LOOP;
    FOR r IN SELECT s.tenant_id, s.property_id FROM accounting_settings s ORDER BY s.property_id LOOP
        IF EXISTS (SELECT 1 FROM gl_account_map m WHERE m.property_id = r.property_id AND m.map_key = 'FOLIO_TRANSFER') THEN
            v_kept := v_kept + 1;
            CONTINUE;
        END IF;
        v_code := '1215';
        IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = v_code) THEN
            v_code := NULL;
            FOR n IN 1216..1299 LOOP
                IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = n::text) THEN
                    v_code := n::text;
                    EXIT;
                END IF;
            END LOOP;
            IF v_code IS NULL THEN
                RAISE EXCEPTION '00074: property % has no free account code from 1215 to 1299 for the account of the transfers between folios', r.property_id;
            END IF;
            v_replaced := v_replaced + 1;
        END IF;
        v_name := 'Transfer between folios';
        IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = '1210'
                      AND a.name = (SELECT c.name FROM usali_chart_names_v2('id') c WHERE c.code = '1210')) THEN
            v_name := 'Transfer antar folio';
        END IF;
        INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
        VALUES (r.tenant_id, r.property_id, v_code, v_name, 'ASSET', 'DEBIT',
                (SELECT p.id FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = '1200'), true, 'RECEIVABLES')
        RETURNING id INTO v_id;
        INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (r.tenant_id, r.property_id, 'FOLIO_TRANSFER', v_id);
        v_added := v_added + 1;
    END LOOP;
    RAISE NOTICE '00074: TRF added to % propert(y/ies); account of the transfers added to % propert(y/ies), % of them with a replacement code because 1215 was taken, % already had the key', v_codes, v_added, v_replaced, v_kept;
END
$$;
-- +goose StatementEnd

-- The log of transfers. Append only, like the ledger it describes. A batch is one request: one reason, one approval, one transaction. A transfer is one moved charge, or one pair of TRF lines.
--   kind ITEM: a charge was reversed on the folio it left (out_item_id is the reversal, original_item_id the charge) and charged again on the folio it went to (in_item_id).
--   kind BALANCE: an adjustment of the code TRF on the folio it left (out_item_id) and a charge of the code TRF on the folio it went to (in_item_id); no original.
--   origin_folio_id is the first folio the charge came from (kept through A -> B -> C), previous_folio_id the folio just before the one it went to. For a balance both are the folio it left.
--   returns_transfer_id is set when the transfer sends back what an earlier one moved; an earlier transfer can be returned once.
CREATE TABLE folio_transfer_batches (
    id              bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id       bigint       NOT NULL,
    property_id     bigint       NOT NULL,
    kind            varchar(8)   NOT NULL,
    is_return       boolean      NOT NULL DEFAULT false,
    from_folio_id   bigint       NOT NULL,
    to_folio_id     bigint       NOT NULL,
    scope           varchar(20)  NOT NULL,
    reason          varchar(500) NOT NULL,
    business_date   date         NOT NULL,
    approved_by     bigint       NOT NULL REFERENCES users (id),
    idempotency_key varchar(100),
    created_at      timestamptz  NOT NULL DEFAULT now(),
    created_by      bigint REFERENCES users (id),
    CONSTRAINT folio_transfer_batches_property_fk FOREIGN KEY (tenant_id, property_id)     REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_transfer_batches_from_fk     FOREIGN KEY (property_id, from_folio_id) REFERENCES folios (property_id, id),
    CONSTRAINT folio_transfer_batches_to_fk       FOREIGN KEY (property_id, to_folio_id)   REFERENCES folios (property_id, id),
    CONSTRAINT folio_transfer_batches_day_fk      FOREIGN KEY (property_id, business_date) REFERENCES business_days (property_id, business_date),
    CONSTRAINT folio_transfer_batches_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT folio_transfer_batches_key_uk      UNIQUE (property_id, idempotency_key),
    CONSTRAINT folio_transfer_batches_kind_ck     CHECK (kind IN ('ITEM', 'BALANCE')),
    CONSTRAINT folio_transfer_batches_scope_ck    CHECK (scope IN ('RESERVATION', 'GROUP_MEMBER', 'GROUP_MASTER', 'ANY', 'RETURN')),
    CONSTRAINT folio_transfer_batches_folios_ck   CHECK (from_folio_id <> to_folio_id)
);
CREATE INDEX folio_transfer_batches_from_idx ON folio_transfer_batches (property_id, from_folio_id);
CREATE INDEX folio_transfer_batches_to_idx   ON folio_transfer_batches (property_id, to_folio_id);
CREATE TRIGGER folio_transfer_batches_append_only BEFORE UPDATE OR DELETE ON folio_transfer_batches FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER folio_transfer_batches_business_day_open BEFORE INSERT ON folio_transfer_batches FOR EACH ROW EXECUTE FUNCTION business_day_must_be_open('business_date');

CREATE TABLE folio_transfers (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint        NOT NULL,
    property_id          bigint        NOT NULL,
    batch_id             bigint        NOT NULL,
    from_folio_id        bigint        NOT NULL,
    to_folio_id          bigint        NOT NULL,
    out_item_id          bigint        NOT NULL,
    in_item_id           bigint        NOT NULL,
    original_item_id     bigint,
    amount               numeric(18,3) NOT NULL,
    origin_folio_id      bigint        NOT NULL,
    previous_folio_id    bigint        NOT NULL,
    returns_transfer_id  bigint,
    created_at           timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT folio_transfers_property_fk FOREIGN KEY (tenant_id, property_id)   REFERENCES properties (tenant_id, id),
    CONSTRAINT folio_transfers_batch_fk    FOREIGN KEY (property_id, batch_id)    REFERENCES folio_transfer_batches (property_id, id),
    CONSTRAINT folio_transfers_from_fk     FOREIGN KEY (property_id, from_folio_id)     REFERENCES folios (property_id, id),
    CONSTRAINT folio_transfers_to_fk       FOREIGN KEY (property_id, to_folio_id)       REFERENCES folios (property_id, id),
    CONSTRAINT folio_transfers_origin_fk   FOREIGN KEY (property_id, origin_folio_id)   REFERENCES folios (property_id, id),
    CONSTRAINT folio_transfers_previous_fk FOREIGN KEY (property_id, previous_folio_id) REFERENCES folios (property_id, id),
    CONSTRAINT folio_transfers_out_fk      FOREIGN KEY (property_id, out_item_id)       REFERENCES folio_items (property_id, id),
    CONSTRAINT folio_transfers_in_fk       FOREIGN KEY (property_id, in_item_id)        REFERENCES folio_items (property_id, id),
    CONSTRAINT folio_transfers_original_fk FOREIGN KEY (property_id, original_item_id)  REFERENCES folio_items (property_id, id),
    CONSTRAINT folio_transfers_returns_fk  FOREIGN KEY (property_id, returns_transfer_id) REFERENCES folio_transfers (property_id, id),
    CONSTRAINT folio_transfers_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT folio_transfers_in_uk       UNIQUE (property_id, in_item_id),
    CONSTRAINT folio_transfers_out_uk      UNIQUE (property_id, out_item_id),
    CONSTRAINT folio_transfers_amount_ck   CHECK (amount > 0),
    CONSTRAINT folio_transfers_folios_ck   CHECK (from_folio_id <> to_folio_id)
);
-- A transfer is returned once, whatever the race: the database decides.
CREATE UNIQUE INDEX folio_transfers_returns_uk ON folio_transfers (property_id, returns_transfer_id) WHERE returns_transfer_id IS NOT NULL;
CREATE INDEX folio_transfers_original_idx ON folio_transfers (property_id, original_item_id) WHERE original_item_id IS NOT NULL;
CREATE INDEX folio_transfers_from_idx ON folio_transfers (property_id, from_folio_id);
CREATE INDEX folio_transfers_to_idx   ON folio_transfers (property_id, to_folio_id);
CREATE INDEX folio_transfers_batch_idx ON folio_transfers (property_id, batch_id);
CREATE TRIGGER folio_transfers_append_only BEFORE UPDATE OR DELETE ON folio_transfers FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- +goose Down
-- Refused while anything holds a transfer: a line of the code TRF, or a row of the log. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM folio_items i JOIN charge_codes c ON c.property_id = i.property_id AND c.id = i.charge_code_id WHERE c.charge_type = 'TRANSFER';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00074: % ledger line(s) are of the code TRF', v_n; END IF;
    SELECT count(*) INTO v_n FROM folio_transfers;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00074: % transfer(s) are in the log', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE folio_transfers;
DROP TABLE folio_transfer_batches;

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
        ('OTHER',       'Other charges',                'OTHER')
      ) AS s (code, name, charge_type)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd
DROP FUNCTION charge_code_names(text);
ALTER FUNCTION charge_code_names_v1(text) RENAME TO charge_code_names;
DELETE FROM charge_codes WHERE charge_type = 'TRANSFER';

-- The account of each property is found through its key (not through its code, which may be a replacement). The key goes. The account goes too when it is still what the migration or the seed
-- made (its standard name) and nothing uses it; otherwise it stays, as an ordinary account.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_dropped int := 0;
    v_kept int := 0;
BEGIN
    FOR r IN SELECT m.property_id, m.account_id FROM gl_account_map m WHERE m.map_key = 'FOLIO_TRANSFER' ORDER BY m.property_id LOOP
        DELETE FROM gl_account_map WHERE property_id = r.property_id AND map_key = 'FOLIO_TRANSFER';
        BEGIN
            DELETE FROM gl_accounts a
             WHERE a.property_id = r.property_id AND a.id = r.account_id
               AND a.name IN ('Transfer between folios', 'Transfer antar folio')
               AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = a.property_id AND l.account_id = a.id);
            IF FOUND THEN v_dropped := v_dropped + 1; ELSE v_kept := v_kept + 1; END IF;
        EXCEPTION WHEN foreign_key_violation THEN
            v_kept := v_kept + 1;
        END;
    END LOOP;
    RAISE NOTICE '00074 down: % transfer account(s) deleted, % kept as ordinary accounts', v_dropped, v_kept;
END
$$;
-- +goose StatementEnd

DROP FUNCTION usali_chart_names(text);
ALTER FUNCTION usali_chart_names_v2(text) RENAME TO usali_chart_names;
DROP FUNCTION usali_chart();
ALTER FUNCTION usali_chart_v4() RENAME TO usali_chart;

ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS'));
ALTER TABLE charge_codes DROP CONSTRAINT charge_codes_type_ck;
ALTER TABLE charge_codes ADD CONSTRAINT charge_codes_type_ck CHECK (charge_type IN ('ROOM', 'FOOD_BEVERAGE', 'SERVICE', 'FEE', 'OTHER'));
