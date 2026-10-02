-- +goose Up
-- Printed documents (invoice, registration card, receipt, confirmation) carry the hotel's contact details, and the
-- confirmation e-mail goes through an outbox: it is queued in the transaction that confirms the reservation, so a
-- confirmation is never lost and a slow or failing mail server never blocks or fails a booking.
ALTER TABLE properties
    ADD COLUMN phone           varchar(40),
    ADD COLUMN email           varchar(254),
    ADD COLUMN tax_id          varchar(40),
    ADD COLUMN document_footer varchar(500);

CREATE TABLE email_outbox (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint       NOT NULL,
    property_id      bigint       NOT NULL,
    kind             varchar(30)  NOT NULL,
    reservation_id   bigint       NOT NULL,
    to_address       varchar(254) NOT NULL,
    status           varchar(10)  NOT NULL DEFAULT 'QUEUED',
    attempts         smallint     NOT NULL DEFAULT 0,
    next_attempt_at  timestamptz  NOT NULL DEFAULT now(),
    last_error       varchar(500),
    sent_at          timestamptz,
    created_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    CONSTRAINT email_outbox_property_fk    FOREIGN KEY (tenant_id, property_id)      REFERENCES properties (tenant_id, id),
    CONSTRAINT email_outbox_reservation_fk FOREIGN KEY (property_id, reservation_id) REFERENCES reservations (property_id, id),
    CONSTRAINT email_outbox_kind_ck        CHECK (kind IN ('RESERVATION_CONFIRMATION')),
    CONSTRAINT email_outbox_status_ck      CHECK (status IN ('QUEUED', 'SENT', 'FAILED', 'SKIPPED')),
    CONSTRAINT email_outbox_sent_ck        CHECK ((status = 'SENT') = (sent_at IS NOT NULL)),
    CONSTRAINT email_outbox_attempts_ck    CHECK (attempts >= 0)
);
CREATE INDEX email_outbox_due_idx         ON email_outbox (next_attempt_at, id) WHERE status = 'QUEUED';
CREATE INDEX email_outbox_reservation_idx ON email_outbox (property_id, reservation_id, id DESC);

-- +goose Down
DROP TABLE IF EXISTS email_outbox;
ALTER TABLE properties DROP COLUMN IF EXISTS document_footer, DROP COLUMN IF EXISTS tax_id, DROP COLUMN IF EXISTS email, DROP COLUMN IF EXISTS phone;
