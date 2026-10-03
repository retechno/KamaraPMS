-- +goose Up
-- PKP (taxable entrepreneur) status of a property and the kind of each tax. A hotel may or may not be PKP, so the
-- status is a setting of the property with a history: the row in force on a date is the latest one that began on or
-- before it, and a change never rewrites an old document. How the VAT paid on purchases (input VAT) is treated is part
-- of the same row: it is claimed against the VAT collected (CREDITABLE), added to the cost of the purchase (EXPENSE), or
-- kept apart without being claimed (DEFERRED). A hotel that is not PKP cannot claim it. Design: docs/architecture/09-pkp-input-vat.md.

-- VAT is the only tax that takes part in the PKP rules; the hotel tax (PB1) and the others never do.
ALTER TABLE taxes ADD COLUMN tax_kind varchar(10) NOT NULL DEFAULT 'LOCAL';
ALTER TABLE taxes ADD CONSTRAINT taxes_kind_ck CHECK (tax_kind IN ('VAT', 'LOCAL', 'OTHER'));
UPDATE taxes SET tax_kind = 'VAT' WHERE code ~* '^(VAT|PPN)' OR name ~* '(\mvat\M|\mppn\M|pertambahan nilai)';

CREATE TABLE property_tax_settings (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    effective_from       date         NOT NULL,
    is_pkp               boolean      NOT NULL DEFAULT false,
    npwp                 varchar(30),
    pkp_number           varchar(40),
    pkp_confirmed_on     date,
    input_vat_treatment  varchar(10)  NOT NULL DEFAULT 'EXPENSE',
    signer_name          varchar(150),
    signer_title         varchar(100),
    approved_by          bigint REFERENCES users (id),
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    CONSTRAINT property_tax_settings_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT property_tax_settings_from_uk        UNIQUE (property_id, effective_from),
    CONSTRAINT property_tax_settings_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT property_tax_settings_treatment_ck   CHECK (input_vat_treatment IN ('CREDITABLE', 'EXPENSE', 'DEFERRED')),
    -- A property that is not PKP cannot claim input VAT.
    CONSTRAINT property_tax_settings_pkp_ck         CHECK (is_pkp OR input_vat_treatment <> 'CREDITABLE'),
    -- A PKP property says its tax number.
    CONSTRAINT property_tax_settings_npwp_ck        CHECK (NOT is_pkp OR npwp IS NOT NULL)
);
-- The history only grows: a row is never changed or deleted.
CREATE TRIGGER property_tax_settings_append_only BEFORE UPDATE OR DELETE ON property_tax_settings
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER property_tax_settings_no_truncate BEFORE TRUNCATE ON property_tax_settings
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- Every property always has a first row (not PKP, input VAT as an expense), which also is the row the changes lock.
INSERT INTO property_tax_settings (tenant_id, property_id, effective_from)
SELECT tenant_id, id, DATE '2000-01-01' FROM properties;

-- +goose StatementBegin
CREATE FUNCTION properties_seed_tax_settings() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO property_tax_settings (tenant_id, property_id, effective_from) VALUES (NEW.tenant_id, NEW.id, DATE '2000-01-01');
    RETURN NULL;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER properties_seed_tax_settings AFTER INSERT ON properties
    FOR EACH ROW EXECUTE FUNCTION properties_seed_tax_settings();

-- +goose Down
DROP TRIGGER IF EXISTS properties_seed_tax_settings ON properties;
DROP FUNCTION IF EXISTS properties_seed_tax_settings();
DROP TABLE IF EXISTS property_tax_settings;
ALTER TABLE taxes DROP CONSTRAINT taxes_kind_ck;
ALTER TABLE taxes DROP COLUMN tax_kind;
