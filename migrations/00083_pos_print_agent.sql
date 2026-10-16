-- +goose Up
-- POS stage 3b: the print agent (docs/architecture/34-pos.md sections 3.4, 10.3, 20).
--
--   pos_devices            a print agent of a hotel: one token (stored as SHA-256), one property
--   pos_device_enrollments the one-time code that enrols an agent (15 minutes, stored as SHA-256)
--   pos_printers           the device that serves it, the last status it reported and when
--   pos_print_jobs         the document, the ESC/POS bytes, the claim and its lease, the backoff, the reprint and its reason
--   pos_outlets            the head and the foot of a receipt, the number of copies of a receipt
--   pos_stations           the number of copies of a kitchen ticket
--   pos_drawer_opens       a drawer opened without a sale (a reason, the person, the shift)

-- ---------------------------------------------------------------- devices
CREATE TABLE pos_devices (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint       NOT NULL,
    property_id   bigint       NOT NULL,
    name          varchar(100) NOT NULL,
    kind          varchar(10)  NOT NULL DEFAULT 'AGENT',
    token_hash    char(64)     NOT NULL,
    token_prefix  varchar(12)  NOT NULL,
    status        varchar(8)   NOT NULL DEFAULT 'ACTIVE',
    last_seen_at  timestamptz,
    agent_version varchar(40),
    queue_depth   integer      NOT NULL DEFAULT 0,
    enrolled_by   bigint REFERENCES users (id),
    created_at    timestamptz  NOT NULL DEFAULT now(),
    rotated_at    timestamptz,
    revoked_at    timestamptz,
    revoked_by    bigint REFERENCES users (id),
    CONSTRAINT pos_devices_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_devices_token_uk       UNIQUE (token_hash),
    CONSTRAINT pos_devices_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT pos_devices_name_ck        CHECK (length(btrim(name)) > 0),
    CONSTRAINT pos_devices_kind_ck        CHECK (kind IN ('AGENT')),
    CONSTRAINT pos_devices_status_ck      CHECK (status IN ('ACTIVE', 'REVOKED')),
    CONSTRAINT pos_devices_hash_ck        CHECK (token_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT pos_devices_revoked_ck     CHECK ((status = 'REVOKED') = (revoked_at IS NOT NULL)),
    CONSTRAINT pos_devices_queue_ck       CHECK (queue_depth >= 0)
);
CREATE INDEX pos_devices_property_idx ON pos_devices (property_id, status);

CREATE TABLE pos_device_enrollments (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint       NOT NULL,
    property_id bigint       NOT NULL,
    code_hash   char(64)     NOT NULL,
    expires_at  timestamptz  NOT NULL,
    used_at     timestamptz,
    device_id   bigint,
    created_by  bigint REFERENCES users (id),
    created_at  timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT pos_device_enrollments_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_device_enrollments_device_fk   FOREIGN KEY (property_id, device_id) REFERENCES pos_devices (property_id, id),
    CONSTRAINT pos_device_enrollments_code_uk     UNIQUE (code_hash),
    CONSTRAINT pos_device_enrollments_hash_ck     CHECK (code_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT pos_device_enrollments_used_ck     CHECK ((used_at IS NULL) = (device_id IS NULL)),
    CONSTRAINT pos_device_enrollments_expiry_ck   CHECK (expires_at > created_at)
);
CREATE INDEX pos_device_enrollments_property_idx ON pos_device_enrollments (property_id, created_at);

-- ---------------------------------------------------------------- printers
ALTER TABLE pos_printers
    ADD COLUMN device_id          bigint,
    ADD COLUMN last_status        varchar(12),
    ADD COLUMN status_reported_at timestamptz,
    ADD CONSTRAINT pos_printers_device_fk FOREIGN KEY (property_id, device_id) REFERENCES pos_devices (property_id, id),
    ADD CONSTRAINT pos_printers_status_ck CHECK (last_status IS NULL OR last_status IN ('ONLINE', 'OFFLINE', 'PAPER_OUT', 'COVER_OPEN')),
    ADD CONSTRAINT pos_printers_reported_ck CHECK ((last_status IS NULL) = (status_reported_at IS NULL));
CREATE INDEX pos_printers_device_idx ON pos_printers (property_id, device_id);

-- ---------------------------------------------------------------- print jobs
-- seq orders the jobs of a printer (created_at is the start of the transaction, the same for the jobs of one payment).
ALTER TABLE pos_print_jobs
    ADD COLUMN seq               bigint GENERATED ALWAYS AS IDENTITY,
    ADD COLUMN document          jsonb,
    ADD COLUMN payload           bytea,
    ADD COLUMN payload_purged_at timestamptz,
    ADD COLUMN claimed_by        bigint,
    ADD COLUMN next_attempt_at   timestamptz,
    ADD COLUMN reprint_reason    varchar(200),
    ADD COLUMN reprint_by        bigint REFERENCES users (id),
    ADD CONSTRAINT pos_print_jobs_device_fk   FOREIGN KEY (property_id, claimed_by) REFERENCES pos_devices (property_id, id),
    ADD CONSTRAINT pos_print_jobs_payload_ck  CHECK (payload IS NULL OR octet_length(payload) <= 65536),
    ADD CONSTRAINT pos_print_jobs_claim_ck    CHECK (status <> 'CLAIMED' OR (claimed_by IS NOT NULL AND lease_until IS NOT NULL)),
    ADD CONSTRAINT pos_print_jobs_printed_ck  CHECK ((status = 'PRINTED') = (printed_at IS NOT NULL)),
    ADD CONSTRAINT pos_print_jobs_failed_ck   CHECK (status <> 'FAILED' OR error_code IS NOT NULL),
    ADD CONSTRAINT pos_print_jobs_reprint_ck  CHECK (reprint_of IS NULL OR length(btrim(coalesce(reprint_reason, ''))) > 0),
    ADD CONSTRAINT pos_print_jobs_attempts_ck CHECK (attempts <= max_attempts);
CREATE UNIQUE INDEX pos_print_jobs_seq_uk ON pos_print_jobs (seq);
-- the order of a printer: its jobs that are not final
CREATE INDEX pos_print_jobs_open_idx ON pos_print_jobs (property_id, printer_id, seq) WHERE status IN ('QUEUED', 'CLAIMED');
-- the failed ones of a bill or a ticket, for the cashier
CREATE INDEX pos_print_jobs_source_idx ON pos_print_jobs (property_id, source_type, source_id);
-- the purge of payloads
CREATE INDEX pos_print_jobs_purge_idx ON pos_print_jobs (created_at) WHERE payload IS NOT NULL;

-- ---------------------------------------------------------------- outlets and stations
ALTER TABLE pos_outlets
    ADD COLUMN receipt_title    varchar(100),
    ADD COLUMN receipt_hotel    varchar(200),
    ADD COLUMN receipt_address  varchar(300),
    ADD COLUMN receipt_tax_id   varchar(40),
    ADD COLUMN receipt_footer   varchar(300),
    ADD COLUMN receipt_copies   smallint NOT NULL DEFAULT 1,
    ADD CONSTRAINT pos_outlets_copies_ck CHECK (receipt_copies BETWEEN 0 AND 5);

ALTER TABLE pos_stations
    ADD COLUMN ticket_copies smallint NOT NULL DEFAULT 1,
    ADD CONSTRAINT pos_stations_copies_ck CHECK (ticket_copies BETWEEN 1 AND 5);

-- ---------------------------------------------------------------- a drawer opened without a sale
CREATE TABLE pos_drawer_opens (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id     bigint       NOT NULL,
    property_id   bigint       NOT NULL,
    outlet_id     bigint       NOT NULL,
    drawer        varchar(20)  NOT NULL,
    shift_id      bigint REFERENCES cashier_shifts (id),
    business_date date         NOT NULL,
    reason        varchar(200) NOT NULL,
    opened_by     bigint       NOT NULL REFERENCES users (id),
    opened_at     timestamptz  NOT NULL DEFAULT now(),
    job_id        uuid REFERENCES pos_print_jobs (id),
    CONSTRAINT pos_drawer_opens_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_drawer_opens_outlet_fk   FOREIGN KEY (property_id, outlet_id)  REFERENCES pos_outlets (property_id, id),
    CONSTRAINT pos_drawer_opens_reason_ck   CHECK (length(btrim(reason)) > 0)
);
CREATE INDEX pos_drawer_opens_outlet_idx ON pos_drawer_opens (property_id, outlet_id, business_date);
CREATE INDEX pos_drawer_opens_shift_idx  ON pos_drawer_opens (shift_id) WHERE shift_id IS NOT NULL;

-- ---------------------------------------------------------------- events: a printer that reported, a device that was seen
ALTER TABLE pos_events DROP CONSTRAINT pos_events_type_ck;
ALTER TABLE pos_events ADD CONSTRAINT pos_events_type_ck CHECK (type IN ('table.updated', 'bill.updated', 'bill.closed', 'ticket.created', 'ticket.updated',
    'product.sold_out', 'print_job.updated', 'folio_lock.changed', 'outlet_charge.attempted', 'printer.updated', 'device.updated'));

-- +goose StatementBegin
CREATE FUNCTION pos_printers_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.last_status IS DISTINCT FROM OLD.last_status OR NEW.device_id IS DISTINCT FROM OLD.device_id OR NEW.is_active IS DISTINCT FROM OLD.is_active THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'printers', 'printer.updated', NEW.id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_printers_emit_trg AFTER UPDATE ON pos_printers FOR EACH ROW EXECUTE FUNCTION pos_printers_emit();

-- +goose StatementBegin
CREATE FUNCTION pos_devices_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' OR NEW.status IS DISTINCT FROM OLD.status THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'printers', 'device.updated', NEW.id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_devices_emit_trg AFTER INSERT OR UPDATE ON pos_devices FOR EACH ROW EXECUTE FUNCTION pos_devices_emit();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pos_devices) OR EXISTS (SELECT 1 FROM pos_drawer_opens) OR EXISTS (SELECT 1 FROM pos_print_jobs WHERE payload IS NOT NULL OR document IS NOT NULL OR reprint_of IS NOT NULL) THEN
        RAISE EXCEPTION '00083: devices, drawer opens or rendered print jobs exist; archive them before migrating down';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER pos_devices_emit_trg ON pos_devices;
DROP FUNCTION pos_devices_emit();
DROP TRIGGER pos_printers_emit_trg ON pos_printers;
DROP FUNCTION pos_printers_emit();
DELETE FROM pos_events WHERE type IN ('printer.updated', 'device.updated');
ALTER TABLE pos_events DROP CONSTRAINT pos_events_type_ck;
ALTER TABLE pos_events ADD CONSTRAINT pos_events_type_ck CHECK (type IN ('table.updated', 'bill.updated', 'bill.closed', 'ticket.created', 'ticket.updated',
    'product.sold_out', 'print_job.updated', 'folio_lock.changed', 'outlet_charge.attempted'));
DROP TABLE pos_drawer_opens;
ALTER TABLE pos_stations DROP CONSTRAINT pos_stations_copies_ck, DROP COLUMN ticket_copies;
ALTER TABLE pos_outlets DROP CONSTRAINT pos_outlets_copies_ck, DROP COLUMN receipt_title, DROP COLUMN receipt_hotel, DROP COLUMN receipt_address,
    DROP COLUMN receipt_tax_id, DROP COLUMN receipt_footer, DROP COLUMN receipt_copies;
DROP INDEX pos_print_jobs_purge_idx;
DROP INDEX pos_print_jobs_source_idx;
DROP INDEX pos_print_jobs_open_idx;
DROP INDEX pos_print_jobs_seq_uk;
ALTER TABLE pos_print_jobs DROP CONSTRAINT pos_print_jobs_attempts_ck, DROP CONSTRAINT pos_print_jobs_reprint_ck, DROP CONSTRAINT pos_print_jobs_failed_ck,
    DROP CONSTRAINT pos_print_jobs_printed_ck, DROP CONSTRAINT pos_print_jobs_claim_ck, DROP CONSTRAINT pos_print_jobs_payload_ck, DROP CONSTRAINT pos_print_jobs_device_fk,
    DROP COLUMN reprint_by, DROP COLUMN reprint_reason, DROP COLUMN next_attempt_at, DROP COLUMN claimed_by, DROP COLUMN payload_purged_at, DROP COLUMN payload,
    DROP COLUMN document, DROP COLUMN seq;
DROP INDEX pos_printers_device_idx;
ALTER TABLE pos_printers DROP CONSTRAINT pos_printers_reported_ck, DROP CONSTRAINT pos_printers_status_ck, DROP CONSTRAINT pos_printers_device_fk,
    DROP COLUMN status_reported_at, DROP COLUMN last_status, DROP COLUMN device_id;
DROP TABLE pos_device_enrollments;
DROP TABLE pos_devices;
