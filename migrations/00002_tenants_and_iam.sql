-- +goose Up
CREATE TABLE tenants (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    code        varchar(30)  NOT NULL,
    name        varchar(200) NOT NULL,
    status      varchar(10)  NOT NULL DEFAULT 'ACTIVE',
    timezone    varchar(64)  NOT NULL,
    created_at  timestamptz  NOT NULL DEFAULT now(),
    updated_at  timestamptz  NOT NULL DEFAULT now(),
    CONSTRAINT tenants_code_uk       UNIQUE (code),
    CONSTRAINT tenants_code_upper_ck CHECK (code = upper(code)),
    CONSTRAINT tenants_status_ck     CHECK (status IN ('ACTIVE', 'SUSPENDED'))
);
CREATE TRIGGER tenants_set_updated_at BEFORE UPDATE ON tenants
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Email is unique per tenant, not globally. Login = tenant_code + email + password.
CREATE TABLE users (
    id               bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id        bigint       NOT NULL REFERENCES tenants (id),
    email            varchar(254) NOT NULL,
    password_hash    text         NOT NULL,
    full_name        varchar(200) NOT NULL,
    is_tenant_admin  boolean      NOT NULL DEFAULT false,
    is_active        boolean      NOT NULL DEFAULT true,
    last_login_at    timestamptz,
    created_at       timestamptz  NOT NULL DEFAULT now(),
    created_by       bigint REFERENCES users (id),
    updated_at       timestamptz  NOT NULL DEFAULT now(),
    updated_by       bigint REFERENCES users (id),
    CONSTRAINT users_tenant_id_uk UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX users_tenant_email_uk ON users (tenant_id, lower(email));
CREATE TRIGGER users_set_updated_at BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

CREATE TABLE user_sessions (
    id                  bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id             bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    refresh_token_hash  bytea       NOT NULL,
    expires_at          timestamptz NOT NULL,
    revoked_at          timestamptz,
    user_agent          varchar(300),
    ip_address          inet,
    created_at          timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT user_sessions_token_uk UNIQUE (refresh_token_hash)
);
CREATE INDEX user_sessions_user_idx ON user_sessions (user_id);

CREATE TABLE roles (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id    bigint       NOT NULL REFERENCES tenants (id),
    name         varchar(100) NOT NULL,
    description  varchar(500),
    is_system    boolean      NOT NULL DEFAULT false,
    created_at   timestamptz  NOT NULL DEFAULT now(),
    created_by   bigint REFERENCES users (id),
    updated_at   timestamptz  NOT NULL DEFAULT now(),
    updated_by   bigint REFERENCES users (id),
    CONSTRAINT roles_tenant_id_uk UNIQUE (tenant_id, id)
);
CREATE UNIQUE INDEX roles_tenant_name_uk ON roles (tenant_id, lower(name));
CREATE TRIGGER roles_set_updated_at BEFORE UPDATE ON roles
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Permission codes are validated against the catalogue in Go (internal/iam).
CREATE TABLE role_permissions (
    role_id          bigint       NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    permission_code  varchar(100) NOT NULL,
    PRIMARY KEY (role_id, permission_code)
);

-- +goose Down
DROP TABLE IF EXISTS role_permissions;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS user_sessions;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
