-- +goose Up
-- Platform console, PR 2 (docs/architecture/39-platform-console.md, 4): the operator, a person of the company that sells KamaraPMS, who is not a user of any tenant.
--
--   operator_accounts          the account: password, TOTP secret (encrypted by the application), the last accepted TOTP step
--   operator_recovery_codes    10 single-use codes per account, stored as SHA-256
--   operator_login_challenges  the short-lived proof that the password was right, exchanged for a session after the TOTP step
--   operator_sessions          refresh-token sessions (the shape of user_sessions)
--   operator_audit_logs        the operator's own append-only trail
--   audit_logs.operator_id     a tenant audit row written by an operator (the hotel sees "KamaraPMS (operator)", never the name)
--   tenants.status_reason      why a tenant is suspended (a suspension needs a reason)

CREATE TABLE operator_accounts (
    id                bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email             varchar(254) NOT NULL,
    full_name         varchar(200) NOT NULL,
    password_hash     text         NOT NULL,
    totp_secret_enc   bytea,
    totp_enrolled_at  timestamptz,
    totp_last_step    bigint,
    is_active         boolean      NOT NULL DEFAULT true,
    last_login_at     timestamptz,
    created_at        timestamptz  NOT NULL DEFAULT now(),
    updated_at        timestamptz  NOT NULL DEFAULT now(),
    -- the secret that is being enrolled lives on the challenge, not here
    CONSTRAINT operator_accounts_totp_ck CHECK ((totp_secret_enc IS NULL) = (totp_enrolled_at IS NULL))
);
CREATE UNIQUE INDEX operator_accounts_email_uk ON operator_accounts (lower(email));
CREATE TRIGGER operator_accounts_set_updated_at BEFORE UPDATE ON operator_accounts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE operator_recovery_codes (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id  bigint      NOT NULL REFERENCES operator_accounts (id) ON DELETE CASCADE,
    code_hash    bytea       NOT NULL,
    used_at      timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT operator_recovery_codes_uk UNIQUE (operator_id, code_hash)
);

CREATE TABLE operator_login_challenges (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id         bigint      NOT NULL REFERENCES operator_accounts (id) ON DELETE CASCADE,
    token_hash          bytea       NOT NULL,
    purpose             varchar(10) NOT NULL,
    pending_secret_enc  bytea,
    attempts            smallint    NOT NULL DEFAULT 0,
    expires_at          timestamptz NOT NULL,
    used_at             timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT operator_login_challenges_token_uk   UNIQUE (token_hash),
    CONSTRAINT operator_login_challenges_purpose_ck CHECK (purpose IN ('VERIFY', 'ENROLL'))
);
CREATE INDEX operator_login_challenges_operator_idx ON operator_login_challenges (operator_id);

CREATE TABLE operator_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id         bigint       NOT NULL REFERENCES operator_accounts (id) ON DELETE CASCADE,
    refresh_token_hash  bytea        NOT NULL,
    expires_at          timestamptz  NOT NULL,
    revoked_at          timestamptz,
    revoked_reason      varchar(20),
    user_agent          varchar(300),
    ip_address          inet,
    created_at          timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT operator_sessions_token_uk          UNIQUE (refresh_token_hash),
    CONSTRAINT operator_sessions_revoked_ck        CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL)),
    CONSTRAINT operator_sessions_revoked_reason_ck CHECK (revoked_reason IN
        ('ROTATED', 'LOGOUT', 'REUSE_DETECTED', 'PASSWORD_CHANGED', 'PASSWORD_RESET', 'DEACTIVATED', 'TOTP_RESET'))
);
CREATE INDEX operator_sessions_active_idx ON operator_sessions (operator_id) WHERE revoked_at IS NULL;

-- Append-only, like audit_logs: no foreign keys on purpose (operator_id NULL means pms-admin or the system).
CREATE TABLE operator_audit_logs (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    operator_id   bigint,
    action        varchar(80)  NOT NULL,
    entity_type   varchar(60)  NOT NULL,
    entity_id     bigint       NOT NULL,
    entity_label  varchar(120),
    tenant_id     bigint,
    old_data      jsonb,
    new_data      jsonb,
    request_id    varchar(64),
    ip_address    inet,
    created_at    timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX operator_audit_logs_created_idx ON operator_audit_logs (created_at DESC);
CREATE INDEX operator_audit_logs_tenant_idx  ON operator_audit_logs (tenant_id, created_at DESC) WHERE tenant_id IS NOT NULL;
CREATE TRIGGER operator_audit_logs_append_only BEFORE UPDATE OR DELETE ON operator_audit_logs
    FOR EACH ROW EXECUTE FUNCTION forbid_modification();
CREATE TRIGGER operator_audit_logs_no_truncate BEFORE TRUNCATE ON operator_audit_logs
    FOR EACH STATEMENT EXECUTE FUNCTION forbid_truncate();

-- A tenant audit row is written by a user, by an operator, or by the system (both NULL). No foreign key, as user_id.
ALTER TABLE audit_logs ADD COLUMN operator_id bigint;
ALTER TABLE audit_logs ADD CONSTRAINT audit_logs_actor_ck CHECK (user_id IS NULL OR operator_id IS NULL);

ALTER TABLE tenants ADD COLUMN status_reason varchar(500), ADD COLUMN status_changed_at timestamptz;
ALTER TABLE tenants ADD CONSTRAINT tenants_suspended_reason_ck CHECK (status = 'ACTIVE' OR status_reason IS NOT NULL);

-- +goose Down
ALTER TABLE tenants DROP CONSTRAINT IF EXISTS tenants_suspended_reason_ck;
ALTER TABLE tenants DROP COLUMN IF EXISTS status_changed_at, DROP COLUMN IF EXISTS status_reason;
ALTER TABLE audit_logs DROP CONSTRAINT IF EXISTS audit_logs_actor_ck;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS operator_id;
DROP TABLE IF EXISTS operator_audit_logs;
DROP TABLE IF EXISTS operator_sessions;
DROP TABLE IF EXISTS operator_login_challenges;
DROP TABLE IF EXISTS operator_recovery_codes;
DROP TABLE IF EXISTS operator_accounts;
