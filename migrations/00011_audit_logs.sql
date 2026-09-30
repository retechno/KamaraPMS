-- +goose Up
-- Append-only audit trail. No foreign keys on purpose: it must outlive anything and add no lock contention.
CREATE TABLE audit_logs (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id      bigint       NOT NULL,
    property_id    bigint,
    business_date  date,
    user_id        bigint,
    action         varchar(100) NOT NULL,
    entity_type    varchar(50)  NOT NULL,
    entity_id      bigint       NOT NULL,
    old_data       jsonb,
    new_data       jsonb,
    request_id     varchar(64),
    ip_address     inet,
    created_at     timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX audit_logs_entity_idx   ON audit_logs (tenant_id, entity_type, entity_id, created_at DESC);
CREATE INDEX audit_logs_property_idx ON audit_logs (property_id, created_at DESC) WHERE property_id IS NOT NULL;
CREATE INDEX audit_logs_user_idx     ON audit_logs (user_id, created_at DESC) WHERE user_id IS NOT NULL;
CREATE TRIGGER audit_logs_append_only BEFORE UPDATE OR DELETE ON audit_logs
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();

-- +goose Down
DROP TABLE IF EXISTS audit_logs;
