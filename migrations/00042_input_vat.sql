-- +goose Up
-- Input VAT on supplier bills (design: docs/architecture/09-pkp-input-vat.md, step 2). A bill line carries the VAT paid on it and
-- the treatment frozen on the bill date: CREDITABLE and DEFERRED book the VAT on the input VAT account (an asset), EXPENSE adds
-- it to the cost on the account of the line. The total of a bill is the lines plus their VAT, which is what is owed to the supplier.

-- The account the input VAT is booked on.
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE', 'INPUT_VAT'));

-- The standard chart gets the account 1425 (new properties are seeded from usali_chart()).
ALTER FUNCTION usali_chart() RENAME TO usali_chart_v1;
-- +goose StatementBegin
CREATE FUNCTION usali_chart() RETURNS TABLE (code text, name text, account_type text, normal_side text, parent_code text, is_postable boolean, statement_group text)
LANGUAGE sql IMMUTABLE AS $$
    SELECT * FROM usali_chart_v1()
    UNION ALL
    SELECT '1425', 'Input VAT (claimable or deferred)', 'ASSET', 'DEBIT', '1400', true, 'PREPAID'
$$;
-- +goose StatementEnd

-- Properties that have a chart already.
INSERT INTO gl_accounts (tenant_id, property_id, code, name, account_type, normal_side, parent_id, is_postable, statement_group)
SELECT s.tenant_id, s.property_id, '1425', 'Input VAT (claimable or deferred)', 'ASSET', 'DEBIT',
       (SELECT p.id FROM gl_accounts p WHERE p.property_id = s.property_id AND p.code = '1400'), true, 'PREPAID'
  FROM accounting_settings s
ON CONFLICT (property_id, code) DO NOTHING;
INSERT INTO gl_account_map (tenant_id, property_id, map_key, account_id)
SELECT a.tenant_id, a.property_id, 'INPUT_VAT', a.id
  FROM gl_accounts a
 WHERE a.code = '1425' AND EXISTS (SELECT 1 FROM accounting_settings s WHERE s.property_id = a.property_id)
ON CONFLICT (property_id, map_key) DO NOTHING;

-- The VAT of a bill line and the treatment it was booked under.
ALTER TABLE supplier_bill_lines ADD COLUMN vat_amount    numeric(18,3) NOT NULL DEFAULT 0;
ALTER TABLE supplier_bill_lines ADD COLUMN vat_treatment varchar(10);
ALTER TABLE supplier_bill_lines ADD CONSTRAINT supplier_bill_lines_vat_ck
    CHECK (vat_amount >= 0 AND (vat_amount = 0) = (vat_treatment IS NULL) AND (vat_treatment IS NULL OR vat_treatment IN ('CREDITABLE', 'EXPENSE', 'DEFERRED')));

-- The lines of a bill, with their VAT, add up to its total.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION supplier_bill_total_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_bill_id bigint;
    v_total   numeric;
    v_lines   numeric;
BEGIN
    IF TG_TABLE_NAME = 'supplier_bills' THEN
        v_bill_id := NEW.id;
    ELSE
        v_bill_id := NEW.bill_id;
    END IF;
    SELECT total INTO v_total FROM supplier_bills WHERE id = v_bill_id;
    SELECT COALESCE(sum(amount + vat_amount), 0) INTO v_lines FROM supplier_bill_lines WHERE bill_id = v_bill_id;
    IF v_total <> v_lines THEN
        RAISE EXCEPTION 'supplier bill % has lines of % for a total of %', v_bill_id, v_lines, v_total
            USING ERRCODE = 'check_violation', CONSTRAINT = 'supplier_bills_lines_match_total';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION supplier_bill_total_check() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_bill_id bigint;
    v_total   numeric;
    v_lines   numeric;
BEGIN
    IF TG_TABLE_NAME = 'supplier_bills' THEN
        v_bill_id := NEW.id;
    ELSE
        v_bill_id := NEW.bill_id;
    END IF;
    SELECT total INTO v_total FROM supplier_bills WHERE id = v_bill_id;
    SELECT COALESCE(sum(amount), 0) INTO v_lines FROM supplier_bill_lines WHERE bill_id = v_bill_id;
    IF v_total <> v_lines THEN
        RAISE EXCEPTION 'supplier bill % has lines of % for a total of %', v_bill_id, v_lines, v_total
            USING ERRCODE = 'check_violation', CONSTRAINT = 'supplier_bills_lines_match_total';
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd
ALTER TABLE supplier_bill_lines DROP CONSTRAINT supplier_bill_lines_vat_ck;
ALTER TABLE supplier_bill_lines DROP COLUMN vat_treatment;
ALTER TABLE supplier_bill_lines DROP COLUMN vat_amount;
DELETE FROM gl_account_map WHERE map_key = 'INPUT_VAT';
DROP FUNCTION IF EXISTS usali_chart();
ALTER FUNCTION usali_chart_v1() RENAME TO usali_chart;
ALTER TABLE gl_account_map DROP CONSTRAINT gl_account_map_key_ck;
ALTER TABLE gl_account_map ADD CONSTRAINT gl_account_map_key_ck CHECK (map_key IN ('CASH', 'CARD', 'BANK_TRANSFER', 'OTHER_PAYMENT', 'CITY_LEDGER', 'GUEST_LEDGER',
                                                             'ADVANCE_DEPOSITS', 'TAX_PAYABLE', 'SERVICE_PAYABLE', 'SUSPENSE', 'RETAINED_EARNINGS', 'ACCOUNTS_PAYABLE'));
