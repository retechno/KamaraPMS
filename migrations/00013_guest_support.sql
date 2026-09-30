-- +goose Up
-- Guest profiles are tenant-wide, so their numbers come from a tenant-wide series
-- (document_sequences is per property). Same gapless rule: the increment runs in the
-- business transaction, so a rollback returns the number.
CREATE TABLE tenant_sequences (
    tenant_id      bigint      NOT NULL REFERENCES tenants (id),
    sequence_type  varchar(20) NOT NULL,
    prefix         varchar(10) NOT NULL,
    next_value     bigint      NOT NULL DEFAULT 1,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, sequence_type),
    CONSTRAINT tenant_sequences_type_ck CHECK (sequence_type IN ('GUEST')),
    CONSTRAINT tenant_sequences_next_ck CHECK (next_value >= 1)
);
CREATE TRIGGER tenant_sequences_set_updated_at BEFORE UPDATE ON tenant_sequences
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Guest visibility rule in one place: a guest is linked to a property when it is the guest's origin
-- property, or the guest is a booker or occupant on a reservation there, or a primary or accompanying
-- guest of a stay there. Used by search, profile access, duplicate hints and history.
-- +goose StatementBegin
CREATE FUNCTION guest_linked_to(p_tenant_id bigint, p_guest_id bigint, p_origin_property_id bigint, p_property_ids bigint[])
RETURNS boolean
LANGUAGE sql STABLE AS $$
    SELECT p_origin_property_id = ANY (p_property_ids)
        OR EXISTS (SELECT 1 FROM reservations r
                    WHERE r.tenant_id = p_tenant_id AND r.guest_id = p_guest_id AND r.property_id = ANY (p_property_ids))
        OR EXISTS (SELECT 1 FROM reservation_rooms rr
                    WHERE rr.tenant_id = p_tenant_id AND rr.guest_id = p_guest_id AND rr.property_id = ANY (p_property_ids))
        OR EXISTS (SELECT 1 FROM stays s
                    WHERE s.tenant_id = p_tenant_id AND s.guest_id = p_guest_id AND s.property_id = ANY (p_property_ids))
        OR EXISTS (SELECT 1 FROM stay_guests sg
                    WHERE sg.tenant_id = p_tenant_id AND sg.guest_id = p_guest_id AND sg.property_id = ANY (p_property_ids))
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS guest_linked_to(bigint, bigint, bigint, bigint[]);
DROP TABLE IF EXISTS tenant_sequences;
