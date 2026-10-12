-- +goose Up
-- QRIS as a payment method of its own. It is received like a card or an e-wallet: the money is not in the bank yet, the acquirer pays it out (net of its MDR, and VAT on the MDR) in a settlement.
-- So it has its own clearing account, which is what the settlement on the bank statement clears, and it takes the same fee rules and the same settlement as a card.
--
-- Every CHECK that lists the methods of a payment learns QRIS:
--   payments_method_ck, clr_method_ck           the payment of a guest and the receipt of a company
--   properties_refund_methods_ck                the methods a refund may leave by (the default of a property stays CASH only)
--   cfr_method_ck                               a fee rule (MDR) per method
--   payments_mdr_ck, clr_mdr_ck                 the MDR snapshot a payment or a receipt keeps
--   card_settlements_key_ck                     the clearing account a settlement clears
--   gl_account_map_key_ck                       the system account key QRIS
-- What is paid OUT (supplier_payments_method_ck, tax_payments_method_ck) is not changed: QRIS is a way to receive money.

ALTER TABLE payments DROP CONSTRAINT payments_method_ck;
ALTER TABLE payments ADD CONSTRAINT payments_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'CITY_LEDGER', 'QRIS'));
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_method_ck;
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'QRIS'));
ALTER TABLE properties DROP CONSTRAINT properties_refund_methods_ck;
ALTER TABLE properties ADD CONSTRAINT properties_refund_methods_ck
    CHECK (cardinality(refund_methods) >= 1 AND refund_methods <@ ARRAY['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'QRIS']);
ALTER TABLE card_fee_rules DROP CONSTRAINT cfr_method_ck;
ALTER TABLE card_fee_rules ADD CONSTRAINT cfr_method_ck CHECK (payment_method IN ('CARD', 'OTHER', 'QRIS'));
ALTER TABLE payments DROP CONSTRAINT payments_mdr_ck;
ALTER TABLE payments ADD CONSTRAINT payments_mdr_ck CHECK ((mdr_rate IS NULL) = (mdr_fee IS NULL) AND (mdr_rate IS NULL) = (expected_settlement_date IS NULL)
    AND (mdr_rate IS NULL OR (mdr_rate BETWEEN 0 AND 100 AND mdr_fee >= 0 AND payment_type = 'PAYMENT' AND payment_method IN ('CARD', 'OTHER', 'QRIS'))));
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_mdr_ck;
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_mdr_ck CHECK ((mdr_rate IS NULL) = (mdr_fee IS NULL) AND (mdr_rate IS NULL) = (expected_settlement_date IS NULL)
    AND (mdr_rate IS NULL OR (mdr_rate BETWEEN 0 AND 100 AND mdr_fee >= 0 AND payment_method IN ('CARD', 'OTHER', 'QRIS'))));
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_key_ck;
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_key_ck CHECK (account_key IN ('CARD', 'OTHER_PAYMENT', 'QRIS'));
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT', 'QRIS'));

-- The standard chart of a NEW property: the account 1155 beside the card (1150) and the other (1160) clearing accounts, with its Indonesian name.
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v3;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_v3()
    UNION ALL
    SELECT '1155', 'QRIS clearing', 'ASSET', 'DEBIT', '1100', true, 'CASH'
$$;
-- +goose StatementEnd
ALTER FUNCTION usali_chart_names(text) RENAME TO usali_chart_names_v1;
-- +goose StatementBegin
CREATE FUNCTION usali_chart_names(p_lang text) RETURNS TABLE (code text, name text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_names_v1(p_lang)
    UNION ALL
    SELECT '1155', 'Akun penampung QRIS' WHERE p_lang = 'id'
$$;
-- +goose StatementEnd

-- The properties that exist. Each one that has a chart gets a QRIS clearing account and the system key QRIS pointing at it:
--   * a property that already has the key QRIS is left as it is;
--   * with no account 1155, the account 1155 is created;
--   * with an account 1155 already (the code is the user's: it is neither used nor overwritten), the next free code from 1156 to 1199 is used.
-- The name is Indonesian when the account 1150 of the property has its Indonesian standard name, else English. The number of properties that took a replacement code is reported.
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
BEGIN
    FOR r IN SELECT s.tenant_id, s.property_id FROM accounting_settings s ORDER BY s.property_id LOOP
        IF EXISTS (SELECT 1 FROM gl_account_map m WHERE m.property_id = r.property_id AND m.map_key = 'QRIS') THEN
            v_kept := v_kept + 1;
            CONTINUE;
        END IF;
        v_code := '1155';
        IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = v_code) THEN
            v_code := NULL;
            FOR n IN 1156..1199 LOOP
                IF NOT EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = n::text) THEN
                    v_code := n::text;
                    EXIT;
                END IF;
            END LOOP;
            IF v_code IS NULL THEN
                RAISE EXCEPTION '00071: property % has no free account code from 1155 to 1199 for the QRIS clearing account', r.property_id;
            END IF;
            v_replaced := v_replaced + 1;
        END IF;
        v_name := 'QRIS clearing';
        IF EXISTS (SELECT 1 FROM gl_accounts a WHERE a.property_id = r.property_id AND a.code = '1150'
                      AND a.name = (SELECT c.name FROM usali_chart_names('id') c WHERE c.code = '1150')) THEN
            v_name := 'Akun penampung QRIS';
        END IF;
        INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
        VALUES (r.tenant_id, r.property_id, v_code, v_name, 'ASSET', 'DEBIT',
                (SELECT p.id FROM gl_accounts p WHERE p.property_id = r.property_id AND p.code = '1100'), true, 'CASH')
        RETURNING id INTO v_id;
        INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id) VALUES (r.tenant_id, r.property_id, 'QRIS', v_id);
        v_added := v_added + 1;
    END LOOP;
    RAISE NOTICE '00071: QRIS clearing account added to % propert(y/ies), % of them with a replacement code because 1155 was taken, % already had the key', v_added, v_replaced, v_kept;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- Refused while anything holds QRIS: a payment, a receipt, a fee rule, a settlement, or a property that allows refunds by QRIS. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM payments WHERE payment_method = 'QRIS';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00071: % payment(s) were made by QRIS', v_n; END IF;
    SELECT count(*) INTO v_n FROM city_ledger_receipts WHERE payment_method = 'QRIS';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00071: % city ledger receipt(s) were taken by QRIS', v_n; END IF;
    SELECT count(*) INTO v_n FROM card_fee_rules WHERE payment_method = 'QRIS';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00071: % fee rule(s) are for QRIS', v_n; END IF;
    SELECT count(*) INTO v_n FROM card_settlements WHERE account_key = 'QRIS';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00071: % settlement(s) cleared the QRIS account', v_n; END IF;
    SELECT count(*) INTO v_n FROM properties WHERE refund_methods @> ARRAY['QRIS'];
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00071: % property(ies) allow refunds by QRIS', v_n; END IF;
END
$$;
-- +goose StatementEnd

-- The account of each property is found through its key QRIS (not through its code, which may be a replacement). The key goes. The account goes too when it is still what the migration
-- or the seed made (its standard name) and nothing uses it: no journal line, no other key, no child, nothing that refers to it. Otherwise it stays, as an ordinary account.
-- +goose StatementBegin
DO $$
DECLARE
    r record;
    v_dropped int := 0;
    v_kept int := 0;
BEGIN
    FOR r IN SELECT m.property_id, m.account_id FROM gl_account_map m WHERE m.map_key = 'QRIS' ORDER BY m.property_id LOOP
        DELETE FROM gl_account_map WHERE property_id = r.property_id AND map_key = 'QRIS';
        BEGIN
            DELETE FROM gl_accounts a
             WHERE a.property_id = r.property_id AND a.id = r.account_id
               AND a.name IN ('QRIS clearing', 'Akun penampung QRIS')
               AND NOT EXISTS (SELECT 1 FROM gl_journal_lines l WHERE l.property_id = a.property_id AND l.account_id = a.id);
            IF FOUND THEN v_dropped := v_dropped + 1; ELSE v_kept := v_kept + 1; END IF;
        EXCEPTION WHEN foreign_key_violation THEN
            v_kept := v_kept + 1;
        END;
    END LOOP;
    RAISE NOTICE '00071 down: % QRIS account(s) deleted, % kept as ordinary accounts', v_dropped, v_kept;
END
$$;
-- +goose StatementEnd

DROP FUNCTION usali_chart_names(text);
ALTER FUNCTION usali_chart_names_v1(text) RENAME TO usali_chart_names;
DROP FUNCTION usali_chart();
ALTER FUNCTION usali_chart_v3() RENAME TO usali_chart;

ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT', 'CASH_OVER_SHORT'));
ALTER TABLE card_settlements DROP CONSTRAINT card_settlements_key_ck;
ALTER TABLE card_settlements ADD CONSTRAINT card_settlements_key_ck CHECK (account_key IN ('CARD', 'OTHER_PAYMENT'));
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_mdr_ck;
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_mdr_ck CHECK ((mdr_rate IS NULL) = (mdr_fee IS NULL) AND (mdr_rate IS NULL) = (expected_settlement_date IS NULL)
    AND (mdr_rate IS NULL OR (mdr_rate BETWEEN 0 AND 100 AND mdr_fee >= 0 AND payment_method IN ('CARD', 'OTHER'))));
ALTER TABLE payments DROP CONSTRAINT payments_mdr_ck;
ALTER TABLE payments ADD CONSTRAINT payments_mdr_ck CHECK ((mdr_rate IS NULL) = (mdr_fee IS NULL) AND (mdr_rate IS NULL) = (expected_settlement_date IS NULL)
    AND (mdr_rate IS NULL OR (mdr_rate BETWEEN 0 AND 100 AND mdr_fee >= 0 AND payment_type = 'PAYMENT' AND payment_method IN ('CARD', 'OTHER'))));
ALTER TABLE card_fee_rules DROP CONSTRAINT cfr_method_ck;
ALTER TABLE card_fee_rules ADD CONSTRAINT cfr_method_ck CHECK (payment_method IN ('CARD', 'OTHER'));
ALTER TABLE properties DROP CONSTRAINT properties_refund_methods_ck;
ALTER TABLE properties ADD CONSTRAINT properties_refund_methods_ck
    CHECK (cardinality(refund_methods) >= 1 AND refund_methods <@ ARRAY['CASH', 'CARD', 'BANK_TRANSFER', 'OTHER']);
ALTER TABLE city_ledger_receipts DROP CONSTRAINT clr_method_ck;
ALTER TABLE city_ledger_receipts ADD CONSTRAINT clr_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER'));
ALTER TABLE payments DROP CONSTRAINT payments_method_ck;
ALTER TABLE payments ADD CONSTRAINT payments_method_ck CHECK (payment_method IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER', 'CITY_LEDGER'));
