-- +goose Up
-- Billing instructions (design: docs/architecture/18-architecture-decisions.md, decision 1): who pays what on a reservation line. An instruction names a company and what it pays: the room
-- (every charge of a ROOM charge code), one charge code, or everything not named by a more specific instruction. The instruction is intent, set before or during the stay; the folio
-- the charges land on is decided by folios.ResolveTarget. It is configuration, not a ledger record: it is replaced as a set and its history is the audit trail.
CREATE TABLE folio_billing_instructions (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    reservation_room_id  bigint       NOT NULL,
    scope                varchar(12)  NOT NULL,
    charge_code_id       bigint,
    company_id           bigint       NOT NULL,
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    updated_at           timestamptz  NOT NULL DEFAULT now(),
    updated_by           bigint REFERENCES users (id),
    CONSTRAINT fbi_property_fk FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT fbi_line_fk     FOREIGN KEY (property_id, reservation_room_id) REFERENCES reservation_rooms (property_id, id),
    CONSTRAINT fbi_code_fk     FOREIGN KEY (property_id, charge_code_id)      REFERENCES charge_codes (property_id, id),
    CONSTRAINT fbi_company_fk  FOREIGN KEY (property_id, company_id)          REFERENCES companies (property_id, id),
    CONSTRAINT fbi_scope_ck    CHECK (scope IN ('ALL', 'ROOM', 'CHARGE_CODE')),
    CONSTRAINT fbi_code_ck     CHECK ((scope = 'CHARGE_CODE') = (charge_code_id IS NOT NULL))
);
-- One instruction for each scope (and each charge code) of a line.
CREATE UNIQUE INDEX fbi_scope_uk ON folio_billing_instructions (reservation_room_id, scope, COALESCE(charge_code_id, 0));
CREATE INDEX fbi_company_idx ON folio_billing_instructions (property_id, company_id);
CREATE TRIGGER folio_billing_instructions_set_updated_at BEFORE UPDATE ON folio_billing_instructions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- +goose Down
DROP TABLE folio_billing_instructions;
