-- +goose Up
-- Accounting-ready ledger. The PMS has no chart of accounts yet; when accounting arrives it owns the COA and these
-- columns hold ACCOUNT CODES (text, never an id), so the link needs no data migration: accounting only has to
-- validate the codes against its COA. Until then the code is optional and free of any foreign key.
--   charge_codes.gl_account_code     revenue account of what is sold
--   taxes.gl_account_code            tax payable account
--   service_charges.gl_account_code  service charge payable account
-- A posted ledger row carries the codes that were in force when it was posted (the ledger is append-only and
-- accounting must reproduce history, so a later remapping never rewrites it). A reversal copies the codes of the
-- item it reverses.
ALTER TABLE charge_codes ADD COLUMN gl_account_code varchar(30);
ALTER TABLE taxes ADD COLUMN gl_account_code varchar(30);
ALTER TABLE service_charges ADD COLUMN gl_account_code varchar(30);
ALTER TABLE folio_items ADD COLUMN revenue_account_code varchar(30);
ALTER TABLE folio_item_components ADD COLUMN gl_account_code varchar(30);

ALTER TABLE charge_codes ADD CONSTRAINT charge_codes_gl_account_code_ck
    CHECK (gl_account_code IS NULL OR gl_account_code ~ '^[A-Z0-9][A-Z0-9._:/-]{0,29}$');
ALTER TABLE taxes ADD CONSTRAINT taxes_gl_account_code_ck
    CHECK (gl_account_code IS NULL OR gl_account_code ~ '^[A-Z0-9][A-Z0-9._:/-]{0,29}$');
ALTER TABLE service_charges ADD CONSTRAINT service_charges_gl_account_code_ck
    CHECK (gl_account_code IS NULL OR gl_account_code ~ '^[A-Z0-9][A-Z0-9._:/-]{0,29}$');
ALTER TABLE folio_items ADD CONSTRAINT folio_items_revenue_account_code_ck
    CHECK (revenue_account_code IS NULL OR revenue_account_code ~ '^[A-Z0-9][A-Z0-9._:/-]{0,29}$');
ALTER TABLE folio_item_components ADD CONSTRAINT folio_item_components_gl_account_code_ck
    CHECK (gl_account_code IS NULL OR gl_account_code ~ '^[A-Z0-9][A-Z0-9._:/-]{0,29}$');

-- Accounting reads the ledger by account code.
CREATE INDEX folio_items_revenue_account_idx ON folio_items (property_id, revenue_account_code, business_date)
    WHERE revenue_account_code IS NOT NULL;
CREATE INDEX folio_item_components_gl_idx ON folio_item_components (property_id, gl_account_code)
    WHERE gl_account_code IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS folio_item_components_gl_idx;
DROP INDEX IF EXISTS folio_items_revenue_account_idx;
ALTER TABLE folio_item_components DROP COLUMN IF EXISTS gl_account_code;
ALTER TABLE folio_items DROP COLUMN IF EXISTS revenue_account_code;
ALTER TABLE service_charges DROP COLUMN IF EXISTS gl_account_code;
ALTER TABLE taxes DROP COLUMN IF EXISTS gl_account_code;
ALTER TABLE charge_codes DROP COLUMN IF EXISTS gl_account_code;
