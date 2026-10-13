-- +goose Up
-- The master folio of a booking group (design: docs/architecture/30-group-folio.md, part C). A folio of type MASTER belongs to a group, not to a reservation or a stay; the room nights of the
-- members of the group can be routed to it when they are posted (group_billing_instructions), except the lines that are excluded (folio_group_routing_exclusions).

-- 1. The folio: reservation_id may be null for a MASTER, and only for a MASTER; a MASTER names its group, has no stay and may carry the company of the group.
ALTER TABLE folios ALTER COLUMN reservation_id DROP NOT NULL;
ALTER TABLE folios ADD COLUMN booking_group_id bigint;
ALTER TABLE folios
    ADD CONSTRAINT folios_group_fk FOREIGN KEY (property_id, booking_group_id) REFERENCES booking_groups (property_id, id);
ALTER TABLE folios DROP CONSTRAINT folios_type_ck;
ALTER TABLE folios DROP CONSTRAINT folios_payer_ck;
ALTER TABLE folios
    ADD CONSTRAINT folios_type_ck         CHECK (folio_type IN ('GUEST', 'COMPANY', 'MASTER')),
    ADD CONSTRAINT folios_payer_ck        CHECK (CASE folio_type WHEN 'COMPANY' THEN bill_to_company_id IS NOT NULL WHEN 'GUEST' THEN bill_to_company_id IS NULL ELSE true END),
    ADD CONSTRAINT folios_master_group_ck CHECK ((folio_type = 'MASTER') = (booking_group_id IS NOT NULL)),
    ADD CONSTRAINT folios_master_shape_ck CHECK (folio_type <> 'MASTER' OR (stay_id IS NULL AND reservation_id IS NULL)),
    ADD CONSTRAINT folios_reservation_ck  CHECK (folio_type = 'MASTER' OR reservation_id IS NOT NULL);
-- One master for each group: a master closed by mistake is reopened (folio.reopen), not replaced.
CREATE UNIQUE INDEX folios_master_group_uk ON folios (booking_group_id) WHERE folio_type = 'MASTER';

-- 2. The routing of a group: what the room nights of its members (and the other charges that post without being told a folio) do. All of them point at the master of the group: there is no
-- target column.
CREATE TABLE group_billing_instructions (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id         bigint       NOT NULL,
    property_id       bigint       NOT NULL,
    booking_group_id  bigint       NOT NULL,
    scope             varchar(12)  NOT NULL,
    charge_code_id    bigint,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    created_by        bigint REFERENCES users (id),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    updated_by        bigint REFERENCES users (id),
    CONSTRAINT gbi_property_fk FOREIGN KEY (tenant_id, property_id)        REFERENCES properties (tenant_id, id),
    CONSTRAINT gbi_group_fk    FOREIGN KEY (property_id, booking_group_id) REFERENCES booking_groups (property_id, id),
    CONSTRAINT gbi_code_fk     FOREIGN KEY (property_id, charge_code_id)   REFERENCES charge_codes (property_id, id),
    CONSTRAINT gbi_scope_ck    CHECK (scope IN ('ALL', 'ROOM', 'CHARGE_CODE')),
    CONSTRAINT gbi_code_ck     CHECK ((scope = 'CHARGE_CODE') = (charge_code_id IS NOT NULL))
);
CREATE UNIQUE INDEX gbi_scope_uk ON group_billing_instructions (booking_group_id, scope, COALESCE(charge_code_id, 0));
CREATE TRIGGER group_billing_instructions_set_updated_at BEFORE UPDATE ON group_billing_instructions
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A line of a reservation of the group that stays out of the routing of the group (every scope): its charges go where its own instructions, or its guest folio, say.
CREATE TABLE folio_group_routing_exclusions (
    reservation_room_id  bigint       PRIMARY KEY,
    tenant_id            bigint       NOT NULL,
    property_id          bigint       NOT NULL,
    created_at           timestamptz  NOT NULL DEFAULT now(),
    created_by           bigint REFERENCES users (id),
    CONSTRAINT fgre_property_fk FOREIGN KEY (tenant_id, property_id)           REFERENCES properties (tenant_id, id),
    CONSTRAINT fgre_line_fk     FOREIGN KEY (property_id, reservation_room_id) REFERENCES reservation_rooms (property_id, id)
);
CREATE INDEX fgre_property_idx ON folio_group_routing_exclusions (property_id);

-- 3. is_deposit by role (docs/architecture/18-architecture-decisions.md 3.9). A GUEST or COMPANY folio keeps the old expression (paid before the check-in of its stay, or it has no stay). A MASTER
-- has no stay, so a payment on it is a deposit only when it was taken before the first live CHARGE row of the folio (a TRF charge included; a charge that was reversed does not count, a reversal being
-- possible only on the day of the charge), and a settlement after that.
CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND CASE WHEN f.folio_type = 'MASTER'
                 THEN NOT EXISTS (SELECT 1 FROM folio_items c
                                   WHERE c.property_id = fi.property_id AND c.folio_id = fi.folio_id AND c.transaction_type = 'CHARGE'
                                     AND c.id < (SELECT min(pi.id) FROM folio_items pi WHERE pi.property_id = rp.property_id AND pi.payment_id = rp.id)
                                     AND NOT EXISTS (SELECT 1 FROM folio_items rv WHERE rv.property_id = c.property_id AND rv.reverses_item_id = c.id))
                 ELSE (st.id IS NULL OR rp.paid_at < st.actual_check_in_at) END) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference,
       COALESCE(fi.department_id, o.department_id) AS department_id
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;

-- +goose Down
-- A master folio holds ledger rows that are never deleted, and the old schema has no place for a folio without a reservation: refused while one exists.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM folios WHERE folio_type = 'MASTER') THEN
        RAISE EXCEPTION 'cannot roll back: MASTER folios exist (a folio of a group has no reservation in the old schema)';
    END IF;
END $$;
-- +goose StatementEnd
CREATE OR REPLACE VIEW folio_item_gl AS
SELECT fi.tenant_id, fi.property_id, fi.id AS item_id, fi.folio_id, fi.business_date,
       CASE WHEN COALESCE(o.transaction_type, fi.transaction_type) IN ('PAYMENT', 'REFUND') THEN 'PAYMENT' ELSE 'CHARGE' END AS kind,
       fi.debit - fi.credit AS signed_amount,
       fi.net_amount,
       COALESCE(fi.revenue_account_code, o.revenue_account_code) AS revenue_account_code,
       COALESCE(cc.code, occ.code) AS charge_code,
       p.payment_method,
       (p.id IS NOT NULL AND rp.payment_method <> 'CITY_LEDGER'
        AND (st.id IS NULL OR rp.paid_at < st.actual_check_in_at)) AS is_deposit,
       p.id AS payment_id, p.payment_number, p.reference_number AS payment_reference,
       COALESCE(fi.department_id, o.department_id) AS department_id
  FROM folio_items fi
  LEFT JOIN folio_items o      ON o.property_id = fi.property_id AND o.id = fi.reverses_item_id
  LEFT JOIN charge_codes cc    ON cc.property_id = fi.property_id AND cc.id = fi.charge_code_id
  LEFT JOIN charge_codes occ   ON occ.property_id = o.property_id AND occ.id = o.charge_code_id
  LEFT JOIN payments p         ON p.property_id = fi.property_id AND p.id = COALESCE(fi.payment_id, o.payment_id)
  LEFT JOIN payments rp        ON rp.property_id = p.property_id AND rp.id = COALESCE(p.refund_of_payment_id, p.id)
  LEFT JOIN folios f           ON f.property_id = fi.property_id AND f.id = fi.folio_id
  LEFT JOIN stays st           ON st.property_id = f.property_id AND st.id = f.stay_id;
DROP TABLE folio_group_routing_exclusions;
DROP TABLE group_billing_instructions;
DROP INDEX folios_master_group_uk;
ALTER TABLE folios
    DROP CONSTRAINT folios_reservation_ck,
    DROP CONSTRAINT folios_master_shape_ck,
    DROP CONSTRAINT folios_master_group_ck,
    DROP CONSTRAINT folios_payer_ck,
    DROP CONSTRAINT folios_type_ck,
    DROP CONSTRAINT folios_group_fk;
ALTER TABLE folios
    ADD CONSTRAINT folios_type_ck  CHECK (folio_type IN ('GUEST', 'COMPANY')),
    ADD CONSTRAINT folios_payer_ck CHECK ((folio_type = 'COMPANY') = (bill_to_company_id IS NOT NULL));
ALTER TABLE folios DROP COLUMN booking_group_id;
ALTER TABLE folios ALTER COLUMN reservation_id SET NOT NULL;
