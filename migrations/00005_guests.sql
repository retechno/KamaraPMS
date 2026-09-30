-- +goose Up
-- Guest profiles are TENANT-WIDE: one profile across all properties of the tenant.
CREATE TABLE guests (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id           bigint       NOT NULL REFERENCES tenants (id),
    code                varchar(20)  NOT NULL,
    origin_property_id  bigint,
    first_name          varchar(100),
    last_name           varchar(100) NOT NULL,
    email               varchar(254),
    phone               varchar(30),
    nationality         char(2),
    country_code        char(2),
    date_of_birth       date,
    gender              varchar(12),
    id_type             varchar(30),
    id_number           varchar(50),
    address             varchar(300),
    city                varchar(100),
    notes               text,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    created_by          bigint REFERENCES users (id),
    updated_at          timestamptz  NOT NULL DEFAULT now(),
    updated_by          bigint REFERENCES users (id),
    CONSTRAINT guests_tenant_code_uk     UNIQUE (tenant_id, code),
    CONSTRAINT guests_tenant_id_uk       UNIQUE (tenant_id, id),
    CONSTRAINT guests_origin_property_fk FOREIGN KEY (tenant_id, origin_property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT guests_gender_ck          CHECK (gender IS NULL OR gender IN ('MALE', 'FEMALE', 'OTHER', 'UNDISCLOSED')),
    CONSTRAINT guests_nationality_ck     CHECK (nationality IS NULL OR nationality ~ '^[A-Z]{2}$'),
    CONSTRAINT guests_country_ck         CHECK (country_code IS NULL OR country_code ~ '^[A-Z]{2}$')
);
-- Search indexes. Not unique: duplicates are merged later, never blocked at insert.
CREATE INDEX guests_name_idx      ON guests (tenant_id, lower(last_name), lower(first_name));
CREATE INDEX guests_email_idx     ON guests (tenant_id, lower(email));
CREATE INDEX guests_phone_idx     ON guests (tenant_id, phone);
CREATE INDEX guests_id_number_idx ON guests (tenant_id, id_number);
CREATE TRIGGER guests_set_updated_at BEFORE UPDATE ON guests
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE IF EXISTS guests;
