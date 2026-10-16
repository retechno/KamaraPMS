-- +goose Up
-- POS stage 3a: realtime, the kitchen display and the queue of print jobs (docs/architecture/34-pos.md sections 3.4, 10, 19).
--
--   pos_events      the outbox of the stream: one row for each thing a screen shows that changed, written by triggers in the
--                   transaction that changed it, so no use case can forget one; ids only, no amounts or names
--   pos_print_jobs  a print job of a kitchen ticket, QUEUED until the agent of stage 3b claims it
--   pos_tickets     who started, made ready and served a ticket; an index for the board of a station
--
-- The outbox is read by (txid, id), not by id alone: an identity value is taken when the row is inserted and the row becomes
-- visible when its transaction commits, so a reader that remembered "id 7" could skip the row 6 of a slower transaction. The row
-- carries the transaction id, a reader sees only the rows whose transaction is older than every transaction still running
-- (pg_snapshot_xmin), and those are final: nothing can appear behind them any more.

CREATE TABLE pos_events (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    channel     varchar(40) NOT NULL,
    type        varchar(40) NOT NULL,
    entity_id   bigint      NOT NULL DEFAULT 0,
    txid        bigint      NOT NULL DEFAULT (pg_current_xact_id()::text::bigint),
    at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT pos_events_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_events_channel_ck  CHECK (channel ~ '^(outlet:[0-9]+|station:[0-9]+|frontdesk|printers)$'),
    CONSTRAINT pos_events_type_ck     CHECK (type IN ('table.updated', 'bill.updated', 'bill.closed', 'ticket.created', 'ticket.updated', 'product.sold_out',
                                                      'print_job.updated', 'folio_lock.changed', 'outlet_charge.attempted'))
);
CREATE INDEX pos_events_read_idx ON pos_events (property_id, txid, id);
CREATE INDEX pos_events_at_idx   ON pos_events (at);

-- Writes an event once for a transaction (the same channel, type and entity again in one transaction is one event) and wakes the
-- streams after the commit.
-- +goose StatementBegin
CREATE FUNCTION pos_emit(p_tenant bigint, p_property bigint, p_channel text, p_type text, p_entity bigint) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    x bigint := pg_current_xact_id()::text::bigint;
BEGIN
    IF EXISTS (SELECT 1 FROM pos_events WHERE property_id = p_property AND txid = x AND channel = p_channel AND type = p_type AND entity_id = p_entity) THEN
        RETURN;
    END IF;
    INSERT INTO pos_events (tenant_id, property_id, channel, type, entity_id) VALUES (p_tenant, p_property, p_channel, p_type, p_entity);
    PERFORM pg_notify('pos_events', '');
END;
$$;
-- +goose StatementEnd

-- A bill: the bill itself and the table it sits at.
-- +goose StatementBegin
CREATE FUNCTION pos_bills_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'outlet:' || NEW.outlet_id,
                     CASE WHEN NEW.status = 'OPEN' THEN 'bill.updated' ELSE 'bill.closed' END, NEW.id);
    IF NEW.table_id IS NOT NULL THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'outlet:' || NEW.outlet_id, 'table.updated', NEW.table_id);
    END IF;
    IF TG_OP = 'UPDATE' AND OLD.table_id IS NOT NULL AND OLD.table_id IS DISTINCT FROM NEW.table_id THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'outlet:' || NEW.outlet_id, 'table.updated', OLD.table_id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_bills_emit_trg AFTER INSERT OR UPDATE ON pos_bills FOR EACH ROW EXECUTE FUNCTION pos_bills_emit();

-- The lines of a bill: the bill and its table changed (what is on it, what is sent, what is voided).
-- +goose StatementBegin
CREATE FUNCTION pos_bill_lines_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    r record;
    bid bigint;
BEGIN
    bid := CASE WHEN TG_OP = 'DELETE' THEN OLD.bill_id ELSE NEW.bill_id END;
    SELECT tenant_id, property_id, outlet_id, table_id INTO r FROM pos_bills WHERE id = bid;
    IF FOUND THEN
        PERFORM pos_emit(r.tenant_id, r.property_id, 'outlet:' || r.outlet_id, 'bill.updated', bid);
        IF r.table_id IS NOT NULL THEN
            PERFORM pos_emit(r.tenant_id, r.property_id, 'outlet:' || r.outlet_id, 'table.updated', r.table_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_bill_lines_emit_trg AFTER INSERT OR UPDATE OR DELETE ON pos_bill_lines FOR EACH ROW EXECUTE FUNCTION pos_bill_lines_emit();

-- A ticket: the board of its station and the lines of its bill (the status is shown on them).
-- +goose StatementBegin
CREATE FUNCTION pos_tickets_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    oid bigint;
    typ text := CASE WHEN TG_OP = 'INSERT' THEN 'ticket.created' ELSE 'ticket.updated' END;
BEGIN
    SELECT outlet_id INTO oid FROM pos_bills WHERE id = NEW.bill_id;
    IF NEW.station_id IS NOT NULL THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'station:' || NEW.station_id, typ, NEW.id);
    END IF;
    IF oid IS NOT NULL THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'outlet:' || oid, 'ticket.updated', NEW.id);
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'outlet:' || oid, 'bill.updated', NEW.bill_id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_tickets_emit_trg AFTER INSERT OR UPDATE ON pos_tickets FOR EACH ROW EXECUTE FUNCTION pos_tickets_emit();

-- A line of a ticket struck through (a void): the ticket changed.
-- +goose StatementBegin
CREATE FUNCTION pos_ticket_lines_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    t record;
    oid bigint;
BEGIN
    SELECT tenant_id, property_id, station_id, bill_id INTO t FROM pos_tickets WHERE id = NEW.ticket_id;
    IF FOUND THEN
        SELECT outlet_id INTO oid FROM pos_bills WHERE id = t.bill_id;
        IF t.station_id IS NOT NULL THEN
            PERFORM pos_emit(t.tenant_id, t.property_id, 'station:' || t.station_id, 'ticket.updated', NEW.ticket_id);
        END IF;
        IF oid IS NOT NULL THEN
            PERFORM pos_emit(t.tenant_id, t.property_id, 'outlet:' || oid, 'ticket.updated', NEW.ticket_id);
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_ticket_lines_emit_trg AFTER UPDATE OF is_void ON pos_ticket_lines FOR EACH ROW WHEN (OLD.is_void IS DISTINCT FROM NEW.is_void) EXECUTE FUNCTION pos_ticket_lines_emit();

-- A product marked or cleared as sold out.
-- +goose StatementBegin
CREATE FUNCTION pos_sold_out_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    r record;
BEGIN
    r := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    PERFORM pos_emit(r.tenant_id, r.property_id, 'outlet:' || r.outlet_id, 'product.sold_out', r.product_id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_outlet_sold_out_emit_trg AFTER INSERT OR UPDATE OR DELETE ON pos_outlet_sold_out FOR EACH ROW EXECUTE FUNCTION pos_sold_out_emit();

-- A hold of a table.
-- +goose StatementBegin
CREATE FUNCTION pos_table_holds_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    r record;
    oid bigint;
BEGIN
    r := CASE WHEN TG_OP = 'DELETE' THEN OLD ELSE NEW END;
    SELECT a.outlet_id INTO oid FROM pos_tables t JOIN pos_areas a ON a.property_id = t.property_id AND a.id = t.area_id WHERE t.id = r.table_id;
    IF oid IS NOT NULL THEN
        PERFORM pos_emit(r.tenant_id, r.property_id, 'outlet:' || oid, 'table.updated', r.table_id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_table_holds_emit_trg AFTER INSERT OR UPDATE OR DELETE ON pos_table_holds FOR EACH ROW EXECUTE FUNCTION pos_table_holds_emit();

-- The front desk: a lock of a folio for outlet charges changed, an outlet tried to charge a room.
-- +goose StatementBegin
CREATE FUNCTION pos_frontdesk_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_TABLE_NAME = 'folio_outlet_locks' THEN
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'frontdesk', 'folio_lock.changed', NEW.folio_id);
    ELSE
        PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'frontdesk', 'outlet_charge.attempted', NEW.id);
    END IF;
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER folio_outlet_locks_emit_trg AFTER INSERT OR UPDATE ON folio_outlet_locks FOR EACH ROW EXECUTE FUNCTION pos_frontdesk_emit();
CREATE TRIGGER pos_room_charge_attempts_emit_trg AFTER INSERT ON pos_room_charge_attempts FOR EACH ROW EXECUTE FUNCTION pos_frontdesk_emit();

-- Tickets: who moved them, and the board of a station (open tickets by status).
ALTER TABLE pos_tickets
    ADD COLUMN started_by bigint REFERENCES users (id),
    ADD COLUMN ready_by   bigint REFERENCES users (id),
    ADD COLUMN served_by  bigint REFERENCES users (id),
    ADD CONSTRAINT pos_tickets_times_ck CHECK (
        (status = 'NEW'       AND started_at IS NULL AND ready_at IS NULL AND served_at IS NULL) OR
        (status = 'COOKING'   AND started_at IS NOT NULL AND ready_at IS NULL AND served_at IS NULL) OR
        (status = 'READY'     AND ready_at IS NOT NULL AND served_at IS NULL) OR
        (status = 'SERVED'    AND served_at IS NOT NULL) OR
        (status = 'CANCELLED'));
CREATE INDEX pos_tickets_board_idx ON pos_tickets (property_id, station_id, status, created_at);

-- Print jobs. Stage 3a only queues them; the agent that claims and prints them is stage 3b (devices, claim, result).
CREATE TABLE pos_print_jobs (
    id           uuid         PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    bigint       NOT NULL,
    property_id  bigint       NOT NULL,
    printer_id   bigint       NOT NULL,
    kind         varchar(7)   NOT NULL,
    source_type  varchar(10)  NOT NULL,
    source_id    bigint       NOT NULL,
    dedupe_key   varchar(80)  NOT NULL,
    copy_no      smallint     NOT NULL DEFAULT 1,
    reprint_of   uuid,
    status       varchar(9)   NOT NULL DEFAULT 'QUEUED',
    attempts     smallint     NOT NULL DEFAULT 0,
    max_attempts smallint     NOT NULL DEFAULT 3,
    lease_until  timestamptz,
    error_code   varchar(40),
    created_at   timestamptz  NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    printed_at   timestamptz,
    CONSTRAINT pos_print_jobs_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT pos_print_jobs_printer_fk  FOREIGN KEY (property_id, printer_id) REFERENCES pos_printers (property_id, id),
    CONSTRAINT pos_print_jobs_reprint_fk  FOREIGN KEY (reprint_of) REFERENCES pos_print_jobs (id),
    CONSTRAINT pos_print_jobs_dedupe_uk   UNIQUE (property_id, dedupe_key),
    CONSTRAINT pos_print_jobs_kind_ck     CHECK (kind IN ('RECEIPT', 'TICKET', 'DRAWER', 'REPORT')),
    CONSTRAINT pos_print_jobs_source_ck   CHECK (source_type IN ('TICKET', 'BILL', 'SHIFT', 'OUTLET')),
    CONSTRAINT pos_print_jobs_status_ck   CHECK (status IN ('QUEUED', 'CLAIMED', 'PRINTED', 'FAILED', 'CANCELLED')),
    CONSTRAINT pos_print_jobs_copy_ck     CHECK (copy_no >= 1 AND attempts >= 0 AND max_attempts >= 1)
);
CREATE INDEX pos_print_jobs_queue_idx ON pos_print_jobs (property_id, printer_id, status, created_at);

-- +goose StatementBegin
CREATE FUNCTION pos_print_jobs_emit() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pos_emit(NEW.tenant_id, NEW.property_id, 'printers', 'print_job.updated', NEW.printer_id);
    RETURN NULL;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER pos_print_jobs_emit_trg AFTER INSERT OR UPDATE ON pos_print_jobs FOR EACH ROW EXECUTE FUNCTION pos_print_jobs_emit();

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pos_print_jobs) OR EXISTS (SELECT 1 FROM pos_tickets WHERE started_by IS NOT NULL OR ready_by IS NOT NULL OR served_by IS NOT NULL) THEN
        RAISE EXCEPTION '00082: print jobs or ticket movements exist; archive them before migrating down';
    END IF;
END;
$$;
-- +goose StatementEnd
DROP TRIGGER pos_print_jobs_emit_trg ON pos_print_jobs;
DROP FUNCTION pos_print_jobs_emit();
DROP TABLE pos_print_jobs;
DROP INDEX pos_tickets_board_idx;
ALTER TABLE pos_tickets DROP CONSTRAINT pos_tickets_times_ck, DROP COLUMN started_by, DROP COLUMN ready_by, DROP COLUMN served_by;
DROP TRIGGER pos_room_charge_attempts_emit_trg ON pos_room_charge_attempts;
DROP TRIGGER folio_outlet_locks_emit_trg ON folio_outlet_locks;
DROP FUNCTION pos_frontdesk_emit();
DROP TRIGGER pos_table_holds_emit_trg ON pos_table_holds;
DROP FUNCTION pos_table_holds_emit();
DROP TRIGGER pos_outlet_sold_out_emit_trg ON pos_outlet_sold_out;
DROP FUNCTION pos_sold_out_emit();
DROP TRIGGER pos_ticket_lines_emit_trg ON pos_ticket_lines;
DROP FUNCTION pos_ticket_lines_emit();
DROP TRIGGER pos_tickets_emit_trg ON pos_tickets;
DROP FUNCTION pos_tickets_emit();
DROP TRIGGER pos_bill_lines_emit_trg ON pos_bill_lines;
DROP FUNCTION pos_bill_lines_emit();
DROP TRIGGER pos_bills_emit_trg ON pos_bills;
DROP FUNCTION pos_bills_emit();
DROP FUNCTION pos_emit(bigint, bigint, text, text, bigint);
DROP TABLE pos_events;
