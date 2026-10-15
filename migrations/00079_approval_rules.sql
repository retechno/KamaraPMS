-- +goose Up
-- Who may approve a correction (docs/architecture/32-distinct-approver.md): a rule for each kind of approval and for each
-- property. No row means FREE (the person who acts may approve their own correction, as before), so the table starts
-- empty and nothing changes for any property. DISTINCT: the approver must be someone else. DISTINCT_ABOVE: the person
-- who acts may approve up to and including the threshold; above it the approver must be someone else. The kinds are
-- validated by the application (a new kind is not a migration); the database only requires their shape.
CREATE TABLE property_approval_rules (
    tenant_id   bigint        NOT NULL,
    property_id bigint        NOT NULL,
    kind        varchar(30)   NOT NULL,
    rule        varchar(14)   NOT NULL,
    threshold   numeric(18,3),
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    updated_by  bigint REFERENCES users (id),
    PRIMARY KEY (property_id, kind),
    CONSTRAINT property_approval_rules_property_fk FOREIGN KEY (tenant_id, property_id) REFERENCES properties (tenant_id, id),
    CONSTRAINT property_approval_rules_kind_ck      CHECK (kind ~ '^[A-Z][A-Z_]*$'),
    CONSTRAINT property_approval_rules_rule_ck      CHECK (rule IN ('FREE', 'DISTINCT', 'DISTINCT_ABOVE')),
    CONSTRAINT property_approval_rules_threshold_ck CHECK ((rule = 'DISTINCT_ABOVE') = (threshold IS NOT NULL)),
    CONSTRAINT property_approval_rules_amount_ck    CHECK (threshold IS NULL OR threshold >= 0)
);

-- +goose Down
DROP TABLE property_approval_rules;
