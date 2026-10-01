-- +goose Up
-- Lost and found: something a guest left behind is recorded when it is found, kept in a storage place, and later
-- handed back to its owner or disposed of. Both endings are final and say who, when and why.
CREATE TABLE lost_found_items (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint        NOT NULL,
    property_id      bigint        NOT NULL,
    item_number      varchar(20)   NOT NULL,
    description      varchar(500)  NOT NULL,
    category         varchar(12)   NOT NULL,
    room_id          bigint,
    location         varchar(150),
    found_on         date          NOT NULL,
    found_at         timestamptz   NOT NULL DEFAULT now(),
    found_by         bigint REFERENCES users (id),
    storage_location varchar(100),
    possible_owner   varchar(150),
    notes            varchar(500),
    status           varchar(8)    NOT NULL DEFAULT 'STORED',
    closed_on        date,
    closed_at        timestamptz,
    closed_by        bigint REFERENCES users (id),
    claimant_name    varchar(150),
    claimant_proof   varchar(150),
    close_note       varchar(500),
    created_at       timestamptz   NOT NULL DEFAULT now(),
    updated_at       timestamptz   NOT NULL DEFAULT now(),
    CONSTRAINT lfi_property_fk     FOREIGN KEY (tenant_id, property_id)    REFERENCES properties (tenant_id, id),
    CONSTRAINT lfi_room_fk         FOREIGN KEY (property_id, room_id)      REFERENCES rooms (property_id, id),
    CONSTRAINT lfi_found_day_fk    FOREIGN KEY (property_id, found_on)     REFERENCES business_days (property_id, business_date),
    CONSTRAINT lfi_closed_day_fk   FOREIGN KEY (property_id, closed_on)    REFERENCES business_days (property_id, business_date),
    CONSTRAINT lfi_number_uk       UNIQUE (property_id, item_number),
    CONSTRAINT lfi_property_id_uk  UNIQUE (property_id, id),
    CONSTRAINT lfi_category_ck     CHECK (category IN ('ELECTRONICS', 'CLOTHING', 'DOCUMENTS', 'JEWELRY', 'BAGS', 'OTHER')),
    CONSTRAINT lfi_status_ck       CHECK (status IN ('STORED', 'RETURNED', 'DISPOSED')),
    CONSTRAINT lfi_where_ck        CHECK (room_id IS NOT NULL OR location IS NOT NULL),
    CONSTRAINT lfi_closed_ck       CHECK ((status = 'STORED') = (closed_at IS NULL) AND (status = 'STORED') = (closed_on IS NULL)),
    CONSTRAINT lfi_returned_ck     CHECK (status <> 'RETURNED' OR claimant_name IS NOT NULL),
    CONSTRAINT lfi_disposed_ck     CHECK (status <> 'DISPOSED' OR close_note IS NOT NULL)
);
CREATE INDEX lfi_status_idx ON lost_found_items (property_id, status, found_on);
CREATE INDEX lfi_room_idx   ON lost_found_items (property_id, room_id) WHERE room_id IS NOT NULL;
CREATE TRIGGER lost_found_items_set_updated_at BEFORE UPDATE ON lost_found_items
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix)
SELECT tenant_id, id, 'LOST_FOUND', 'LF' FROM properties;

-- +goose Down
DELETE FROM document_sequences WHERE sequence_type = 'LOST_FOUND';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE'));
DROP TABLE IF EXISTS lost_found_items;
