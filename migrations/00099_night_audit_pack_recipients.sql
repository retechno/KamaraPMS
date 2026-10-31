-- +goose Up
-- Night audit pack, PR 3 (docs/architecture/38-reports-night-audit-pack.md, 6, 8.1 and 10): the recipients of the pack, the e-mail and its history.
--
--   nap_recipients   who receives the pack of a property, and which sections (the catalog of fifteen codes of 00098)
--   email_outbox     freed for a third kind, NIGHT_AUDIT_PACK: the pack, the sections as sent, the part of a split message, and (Q6) the SHA-256 of the PDF as sent and the
--                    version of the renderer that drew it. The PDF itself is not kept.
--
-- No foreign key from email_outbox to nap_recipients: deleting a recipient must not be refused and must not erase the history; the row keeps the address and the sections as sent.

-- 1. The recipients ----------------------------------------------------------------------------------------------------------------------------------
-- The sections of a recipient: not empty, every element in the catalog, no duplicate. A CHECK cannot hold a subquery, so the rule is a function.
-- +goose StatementBegin
CREATE FUNCTION nap_sections_valid(p_sections text[]) RETURNS boolean
LANGUAGE sql IMMUTABLE AS $$
    SELECT p_sections IS NOT NULL
       AND cardinality(p_sections) >= 1
       AND p_sections <@ ARRAY['SUMMARY', 'FLASH', 'REVENUE', 'TAX', 'PAYMENTS', 'Z_REPORTS', 'GUEST_LEDGER', 'DEPOSIT_LEDGER', 'CORRECTIONS',
                               'CANCELLATIONS', 'OUTLETS', 'OUT_OF_ORDER', 'IN_HOUSE', 'NEXT_DAY', 'AUDIT_NOTES']::text[]
       AND cardinality(p_sections) = (SELECT count(DISTINCT s) FROM unnest(p_sections) AS s)
$$;
-- +goose StatementEnd

CREATE TABLE nap_recipients (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint       NOT NULL,
    property_id  bigint       NOT NULL,
    email        varchar(254) NOT NULL,
    name         varchar(100),
    sections     varchar(16)[] NOT NULL,
    is_active    boolean      NOT NULL DEFAULT true,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id),
    CONSTRAINT nap_recipients_property_fk    FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT nap_recipients_property_id_uk UNIQUE (property_id, id),
    -- one '@' with something on both sides, no space and no line break (a header cannot be injected through an address)
    CONSTRAINT nap_recipients_email_ck       CHECK (email ~ '^[^@[:space:]]+@[^@[:space:]]+$'),
    CONSTRAINT nap_recipients_name_ck        CHECK (name IS NULL OR (length(btrim(name)) > 0 AND name !~ '[\r\n]')),
    CONSTRAINT nap_recipients_sections_ck    CHECK (nap_sections_valid(sections::text[]))
);
-- an address is a recipient of a property once, whatever its case
CREATE UNIQUE INDEX nap_recipients_email_uk ON nap_recipients (property_id, lower(email));

-- 2. The e-mail outbox is freed for the pack (6, 8.1) ----------------------------------------------------------------------------------------------
ALTER TABLE email_outbox
    ADD COLUMN nap_pack_id     bigint,
    ADD COLUMN nap_sections    varchar(16)[],
    ADD COLUMN nap_part        smallint,
    ADD COLUMN nap_parts       smallint,
    ADD COLUMN nap_pdf_sha256  char(64),
    ADD COLUMN nap_renderer    varchar(20);
ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_pack_fk FOREIGN KEY (property_id, nap_pack_id) REFERENCES nap_packs (property_id, id);
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_shape_ck;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_kind_ck;
ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_kind_ck  CHECK (kind IN ('RESERVATION_CONFIRMATION', 'BANQUET_PROPOSAL', 'BANQUET_CONTRACT', 'NIGHT_AUDIT_PACK')),
    -- a confirmation belongs to a reservation, a proposal or a contract to an event and a document, and a pack e-mail to a pack with its sections and its part; the columns of the other kinds are empty
    ADD CONSTRAINT email_outbox_shape_ck CHECK (
           (kind = 'RESERVATION_CONFIRMATION' AND reservation_id IS NOT NULL AND banquet_event_id IS NULL AND document_id IS NULL AND language IS NULL
                AND nap_pack_id IS NULL AND nap_sections IS NULL AND nap_part IS NULL AND nap_parts IS NULL)
        OR (kind IN ('BANQUET_PROPOSAL', 'BANQUET_CONTRACT') AND reservation_id IS NULL AND banquet_event_id IS NOT NULL AND document_id IS NOT NULL AND language IN ('en', 'id')
                AND nap_pack_id IS NULL AND nap_sections IS NULL AND nap_part IS NULL AND nap_parts IS NULL)
        OR (kind = 'NIGHT_AUDIT_PACK' AND reservation_id IS NULL AND banquet_event_id IS NULL AND document_id IS NULL AND language = 'en'
                AND nap_pack_id IS NOT NULL AND nap_sections IS NOT NULL AND nap_part IS NOT NULL AND nap_parts IS NOT NULL
                AND nap_sections <> '{}' AND nap_part >= 1 AND nap_parts >= nap_part)),
    -- the hash and the renderer come together, only a pack e-mail has them, they have the shape of a hash and a version, and a pack e-mail that was sent has both (Q6)
    ADD CONSTRAINT email_outbox_nap_sent_ck CHECK (
           ((nap_pdf_sha256 IS NULL) = (nap_renderer IS NULL))
       AND (nap_pdf_sha256 IS NULL OR (kind = 'NIGHT_AUDIT_PACK' AND nap_pdf_sha256 ~ '^[0-9a-f]{64}$' AND length(btrim(nap_renderer)) > 0))
       AND (kind <> 'NIGHT_AUDIT_PACK' OR status <> 'SENT' OR nap_pdf_sha256 IS NOT NULL));
CREATE INDEX email_outbox_pack_idx ON email_outbox (property_id, nap_pack_id, id DESC) WHERE nap_pack_id IS NOT NULL;
-- a recipient has one waiting message for a part of a pack: a double click on "send again" queues it once
CREATE UNIQUE INDEX email_outbox_pack_queued_uk ON email_outbox (nap_pack_id, lower(to_address), nap_part) WHERE status = 'QUEUED';

-- +goose Down
-- Refused while the pack e-mail exists or a recipient is kept: they are records of what was sent and to whom. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT count(*) INTO v_n FROM email_outbox WHERE kind = 'NIGHT_AUDIT_PACK';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00099: % night audit pack e-mail(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM nap_recipients;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00099: % recipient(s) of the night audit pack exist', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX email_outbox_pack_queued_uk;
DROP INDEX email_outbox_pack_idx;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_nap_sent_ck;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_shape_ck;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_kind_ck;
ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_kind_ck  CHECK (kind IN ('RESERVATION_CONFIRMATION', 'BANQUET_PROPOSAL', 'BANQUET_CONTRACT')),
    ADD CONSTRAINT email_outbox_shape_ck CHECK ((kind = 'RESERVATION_CONFIRMATION' AND reservation_id IS NOT NULL AND banquet_event_id IS NULL AND document_id IS NULL AND language IS NULL)
                                             OR (kind IN ('BANQUET_PROPOSAL', 'BANQUET_CONTRACT') AND reservation_id IS NULL AND banquet_event_id IS NOT NULL AND document_id IS NOT NULL
                                                 AND language IN ('en', 'id')));
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_pack_fk;
ALTER TABLE email_outbox
    DROP COLUMN nap_renderer, DROP COLUMN nap_pdf_sha256, DROP COLUMN nap_parts, DROP COLUMN nap_part, DROP COLUMN nap_sections, DROP COLUMN nap_pack_id;

DROP TABLE nap_recipients;
DROP FUNCTION nap_sections_valid(text[]);
