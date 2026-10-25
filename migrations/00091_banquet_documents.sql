-- +goose Up
-- Banquet, PR 3 (docs/architecture/36-banquet.md, sections 3.4, 4.6, F8, B11, B12, Q4, Q8, Q10): the documents of an event.
--
--   banquet_beo_sections      the text of the BEO of a session, by section (the kitchen, the F&B service, housekeeping, engineering, security, the front office and a general one)
--   banquet_run_of_show       the programme of a session, in time order
--   banquet_beo_revisions     after Definite every save of the event is one revision, with the snapshot of the whole document and what changed (Q10)
--   banquet_beo_ack           who saw which section at which revision: the base of "changed since revision X"
--   banquet_beo_recipients    the department of each section: its head and its deputy are told of a revision (Q8)
--   banquet_documents         the proposals that were sent (with their snapshot) and the numbered contracts (final for the revision they carry)
--   banquet_notices           the kinds BEO_REVISED and FEE_PROPOSED; a person who has not seen the last BEO_REVISED of an event gets no second one
--   email_outbox              freed from its reservation: a banquet event and a document, the kinds BANQUET_PROPOSAL and BANQUET_CONTRACT (F8); the old kind is unchanged
--   document_sequences        BANQUET_CONTRACT (the number of a contract)

-- 1. The BEO ---------------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_beo_sections (
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    session_id  bigint      NOT NULL,
    section     varchar(14) NOT NULL,
    body        text        NOT NULL DEFAULT '',
    updated_at  timestamptz NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    PRIMARY KEY (session_id, section),
    CONSTRAINT banquet_beo_sections_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_beo_sections_session_fk  FOREIGN KEY (property_id, session_id) REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_beo_sections_section_ck  CHECK (section IN ('KITCHEN', 'FB_SERVICE', 'HOUSEKEEPING', 'ENGINEERING', 'SECURITY', 'FRONT_OFFICE', 'GENERAL')),
    CONSTRAINT banquet_beo_sections_body_ck     CHECK (char_length(body) <= 4000)
);

CREATE TABLE banquet_run_of_show (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    session_id  bigint      NOT NULL,
    seq         int         NOT NULL,
    at_time     time        NOT NULL,
    body        varchar(300) NOT NULL,
    CONSTRAINT banquet_run_of_show_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_run_of_show_session_fk  FOREIGN KEY (property_id, session_id) REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_run_of_show_seq_uk      UNIQUE (session_id, seq),
    CONSTRAINT banquet_run_of_show_seq_ck      CHECK (seq >= 1),
    CONSTRAINT banquet_run_of_show_body_ck     CHECK (length(btrim(body)) > 0)
);

-- 2. The revisions and who read them ---------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_beo_revisions (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    event_id    bigint      NOT NULL,
    revision_no int         NOT NULL,
    note        varchar(300),
    changes     jsonb       NOT NULL DEFAULT '[]'::jsonb,
    snapshot    jsonb       NOT NULL,
    notified    int         NOT NULL DEFAULT 0,
    created_by  bigint REFERENCES users (id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT banquet_beo_revisions_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_beo_revisions_event_fk    FOREIGN KEY (property_id, event_id)  REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_beo_revisions_no_uk       UNIQUE (event_id, revision_no),
    CONSTRAINT banquet_beo_revisions_no_ck       CHECK (revision_no >= 1),
    CONSTRAINT banquet_beo_revisions_notified_ck CHECK (notified >= 0)
);

CREATE TABLE banquet_beo_ack (
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    event_id    bigint      NOT NULL,
    session_id  bigint      NOT NULL,
    section     varchar(14) NOT NULL,
    user_id     bigint      NOT NULL,
    revision_no int         NOT NULL,
    read_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, section, user_id),
    CONSTRAINT banquet_beo_ack_property_fk FOREIGN KEY (tenant_id, property_id)  REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_beo_ack_session_fk  FOREIGN KEY (property_id, session_id) REFERENCES banquet_sessions (property_id, id),
    CONSTRAINT banquet_beo_ack_user_fk     FOREIGN KEY (tenant_id, user_id)      REFERENCES users (tenant_id, id),
    CONSTRAINT banquet_beo_ack_revision_fk FOREIGN KEY (event_id, revision_no)   REFERENCES banquet_beo_revisions (event_id, revision_no),
    CONSTRAINT banquet_beo_ack_section_ck  CHECK (section IN ('KITCHEN', 'FB_SERVICE', 'HOUSEKEEPING', 'ENGINEERING', 'SECURITY', 'FRONT_OFFICE', 'GENERAL'))
);
CREATE INDEX banquet_beo_ack_event_idx ON banquet_beo_ack (property_id, event_id);

-- 3. The department of a section ---------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_beo_recipients (
    tenant_id     bigint      NOT NULL,
    property_id   bigint      NOT NULL,
    section       varchar(14) NOT NULL,
    department_id bigint      NOT NULL,
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    bigint REFERENCES users (id),
    PRIMARY KEY (property_id, section),
    CONSTRAINT banquet_beo_recipients_property_fk   FOREIGN KEY (tenant_id, property_id)       REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_beo_recipients_department_fk FOREIGN KEY (property_id, department_id)   REFERENCES departments (property_id, id),
    CONSTRAINT banquet_beo_recipients_section_ck    CHECK (section IN ('KITCHEN', 'FB_SERVICE', 'HOUSEKEEPING', 'ENGINEERING', 'SECURITY', 'FRONT_OFFICE'))
);

-- 4. The documents ---------------------------------------------------------------------------------------------------------------------------------
CREATE TABLE banquet_documents (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id   bigint      NOT NULL,
    property_id bigint      NOT NULL,
    event_id    bigint      NOT NULL,
    kind        varchar(10) NOT NULL,
    number      varchar(30),
    revision_no int,
    snapshot    jsonb,
    created_by  bigint REFERENCES users (id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT banquet_documents_property_id_uk UNIQUE (property_id, id),
    CONSTRAINT banquet_documents_tenant_fk   FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT banquet_documents_event_fk    FOREIGN KEY (property_id, event_id)  REFERENCES banquet_events (property_id, id),
    CONSTRAINT banquet_documents_revision_fk FOREIGN KEY (event_id, revision_no)  REFERENCES banquet_beo_revisions (event_id, revision_no),
    CONSTRAINT banquet_documents_number_uk   UNIQUE (property_id, number),
    CONSTRAINT banquet_documents_kind_ck     CHECK (kind IN ('PROPOSAL', 'CONTRACT')),
    -- a contract has a number and the revision it is final for, and reads its terms from the snapshot of that revision; a proposal keeps the snapshot of the moment it was sent
    CONSTRAINT banquet_documents_shape_ck    CHECK ((kind = 'CONTRACT' AND number IS NOT NULL AND revision_no IS NOT NULL AND snapshot IS NULL)
                                                 OR (kind = 'PROPOSAL' AND number IS NULL AND revision_no IS NULL AND snapshot IS NOT NULL))
);
CREATE INDEX banquet_documents_event_idx ON banquet_documents (property_id, event_id, id DESC);
-- a contract is final for one revision: asking again for the same revision gives the same contract
CREATE UNIQUE INDEX banquet_documents_contract_uk ON banquet_documents (event_id, revision_no) WHERE kind = 'CONTRACT';

ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT', 'BANQUET_CONTRACT'));
INSERT INTO document_sequences (tenant_id, property_id, sequence_type, prefix) SELECT tenant_id, id, 'BANQUET_CONTRACT', 'BQC-' FROM properties;

-- 5. The e-mail outbox is freed from the reservation (F8) --------------------------------------------------------------------------------------------
ALTER TABLE email_outbox ALTER COLUMN reservation_id DROP NOT NULL;
ALTER TABLE email_outbox
    ADD COLUMN banquet_event_id bigint,
    ADD COLUMN document_id      bigint,
    ADD COLUMN language         varchar(2);
ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_event_fk    FOREIGN KEY (property_id, banquet_event_id) REFERENCES banquet_events (property_id, id),
    ADD CONSTRAINT email_outbox_document_fk FOREIGN KEY (property_id, document_id)      REFERENCES banquet_documents (property_id, id);
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_kind_ck;
ALTER TABLE email_outbox
    ADD CONSTRAINT email_outbox_kind_ck  CHECK (kind IN ('RESERVATION_CONFIRMATION', 'BANQUET_PROPOSAL', 'BANQUET_CONTRACT')),
    ADD CONSTRAINT email_outbox_shape_ck CHECK ((kind = 'RESERVATION_CONFIRMATION' AND reservation_id IS NOT NULL AND banquet_event_id IS NULL AND document_id IS NULL AND language IS NULL)
                                             OR (kind IN ('BANQUET_PROPOSAL', 'BANQUET_CONTRACT') AND reservation_id IS NULL AND banquet_event_id IS NOT NULL AND document_id IS NOT NULL
                                                 AND language IN ('en', 'id')));
CREATE INDEX email_outbox_event_idx ON email_outbox (property_id, banquet_event_id, id DESC) WHERE banquet_event_id IS NOT NULL;

-- 6. The notices of a revision -----------------------------------------------------------------------------------------------------------------------
ALTER TABLE banquet_notices DROP CONSTRAINT banquet_notices_kind_ck;
ALTER TABLE banquet_notices ADD COLUMN revision_no int;
ALTER TABLE banquet_notices ADD CONSTRAINT banquet_notices_kind_ck CHECK (kind IN ('OPTION_PROMOTED', 'OPTION_DUE', 'OPTION_LOST', 'DEPOSIT_DUE', 'BEO_REVISED', 'FEE_PROPOSED'));
-- a person who has not seen the last revision notice of an event gets no second one: the writer updates this row (Q10, 4.6)
CREATE UNIQUE INDEX banquet_notices_beo_uk ON banquet_notices (property_id, event_id, user_id) WHERE kind = 'BEO_REVISED' AND seen_at IS NULL;

-- +goose Down
-- Refused while the documents of the banquet exist: a revision, a text of a BEO, a recipient, a document, a banquet e-mail or a revision notice. Nothing is changed then.
-- +goose StatementBegin
DO $$
DECLARE
    v_n bigint;
BEGIN
    SELECT (SELECT count(*) FROM banquet_beo_revisions) + (SELECT count(*) FROM banquet_beo_sections WHERE body <> '') + (SELECT count(*) FROM banquet_run_of_show)
         + (SELECT count(*) FROM banquet_beo_recipients) + (SELECT count(*) FROM banquet_beo_ack) INTO v_n;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00091: the banquet holds % row(s) of BEO texts, revisions or recipients', v_n; END IF;
    SELECT count(*) INTO v_n FROM banquet_documents;
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00091: % proposal(s) or contract(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM email_outbox WHERE kind <> 'RESERVATION_CONFIRMATION';
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00091: % banquet e-mail(s) exist', v_n; END IF;
    SELECT count(*) INTO v_n FROM banquet_notices WHERE kind IN ('BEO_REVISED', 'FEE_PROPOSED');
    IF v_n > 0 THEN RAISE EXCEPTION 'cannot roll back 00091: % notice(s) of a revision exist', v_n; END IF;
END
$$;
-- +goose StatementEnd

DROP INDEX banquet_notices_beo_uk;
ALTER TABLE banquet_notices DROP CONSTRAINT banquet_notices_kind_ck;
ALTER TABLE banquet_notices DROP COLUMN revision_no;
ALTER TABLE banquet_notices ADD CONSTRAINT banquet_notices_kind_ck CHECK (kind IN ('OPTION_PROMOTED', 'OPTION_DUE', 'OPTION_LOST', 'DEPOSIT_DUE'));

DROP INDEX email_outbox_event_idx;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_shape_ck;
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_kind_ck;
ALTER TABLE email_outbox ADD CONSTRAINT email_outbox_kind_ck CHECK (kind IN ('RESERVATION_CONFIRMATION'));
ALTER TABLE email_outbox DROP CONSTRAINT email_outbox_document_fk, DROP CONSTRAINT email_outbox_event_fk;
ALTER TABLE email_outbox DROP COLUMN language, DROP COLUMN document_id, DROP COLUMN banquet_event_id;
ALTER TABLE email_outbox ALTER COLUMN reservation_id SET NOT NULL;

DELETE FROM document_sequences WHERE sequence_type = 'BANQUET_CONTRACT';
ALTER TABLE document_sequences DROP CONSTRAINT document_sequences_type_ck;
ALTER TABLE document_sequences ADD CONSTRAINT document_sequences_type_ck
    CHECK (sequence_type IN ('RESERVATION', 'STAY', 'FOLIO', 'PAYMENT', 'CITY_LEDGER_RECEIPT', 'CITY_LEDGER_INVOICE', 'MAINTENANCE', 'LOST_FOUND', 'JOURNAL',
                             'SUPPLIER_BILL', 'SUPPLIER_PAYMENT', 'TAX_RETURN', 'TAX_PAYMENT', 'TAX_INVOICE', 'CREDIT_NOTE', 'WRITE_OFF', 'REMINDER', 'SHIFT', 'SUPPLIER_CREDIT',
                             'INV_OPENING', 'INV_PR', 'INV_PO', 'INV_GR', 'INV_RET', 'INV_SR', 'INV_TRF', 'INV_ISS', 'INV_CNT', 'BANQUET_EVENT'));

DROP TABLE banquet_documents;
DROP TABLE banquet_beo_recipients;
DROP TABLE banquet_beo_ack;
DROP TABLE banquet_beo_revisions;
DROP TABLE banquet_run_of_show;
DROP TABLE banquet_beo_sections;
