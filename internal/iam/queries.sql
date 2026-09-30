-- Identity and access queries (sqlc). Every user/role query is scoped by tenant_id.

-- name: GetTenantForLogin :one
SELECT id, status FROM tenants WHERE code = @code;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE tenant_id = @tenant_id AND lower(email) = lower(@email);

-- name: GetUser :one
SELECT * FROM users WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetUserForUpdate :one
SELECT * FROM users WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: ListUsers :many
SELECT * FROM users
WHERE tenant_id = @tenant_id AND id > @after_id
ORDER BY id
LIMIT @row_limit;

-- name: CreateUser :one
INSERT INTO users (tenant_id, email, password_hash, full_name, is_tenant_admin, is_active, created_by, updated_by)
VALUES (@tenant_id, lower(@email), @password_hash, @full_name, @is_tenant_admin, true, sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateUser :one
UPDATE users SET
    full_name = @full_name,
    is_active = @is_active,
    is_tenant_admin = @is_tenant_admin,
    updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: SetUserPassword :exec
UPDATE users SET password_hash = @password_hash, updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND id = @id;

-- name: TouchLastLogin :exec
UPDATE users SET last_login_at = @at::timestamptz WHERE id = @id;

-- name: CountActiveAdmins :one
SELECT count(*) FROM users WHERE tenant_id = @tenant_id AND is_tenant_admin AND is_active;

-- Sessions ------------------------------------------------------------------

-- name: CreateSession :one
INSERT INTO user_sessions (user_id, refresh_token_hash, expires_at, user_agent, ip_address, created_at)
VALUES (@user_id, @refresh_token_hash, @expires_at, sqlc.narg(user_agent), sqlc.narg(ip_address), @created_at)
RETURNING id;

-- name: GetSessionByTokenForUpdate :one
SELECT s.id, s.user_id, s.expires_at, s.revoked_at, s.revoked_reason, s.user_agent,
       u.tenant_id, u.is_active, u.is_tenant_admin, t.status AS tenant_status
FROM user_sessions s
JOIN users u ON u.id = s.user_id
JOIN tenants t ON t.id = u.tenant_id
WHERE s.refresh_token_hash = @refresh_token_hash
FOR UPDATE OF s;

-- Checked on every authenticated request so logout, revocation and deactivation
-- take effect immediately rather than when the access token expires.
-- name: GetSessionState :one
SELECT s.revoked_at, s.expires_at, u.tenant_id, u.is_active, u.is_tenant_admin, t.status AS tenant_status
FROM user_sessions s
JOIN users u ON u.id = s.user_id
JOIN tenants t ON t.id = u.tenant_id
WHERE s.id = @session_id AND s.user_id = @user_id;

-- name: RevokeSession :exec
UPDATE user_sessions SET revoked_at = @at::timestamptz, revoked_reason = @reason::varchar
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE user_sessions SET revoked_at = @at::timestamptz, revoked_reason = @reason::varchar
WHERE user_id = @user_id AND revoked_at IS NULL;

-- Roles ----------------------------------------------------------------------

-- name: ListRoles :many
SELECT * FROM roles WHERE tenant_id = @tenant_id ORDER BY lower(name), id;

-- name: GetRole :one
SELECT * FROM roles WHERE tenant_id = @tenant_id AND id = @id;

-- name: GetRoleForUpdate :one
SELECT * FROM roles WHERE tenant_id = @tenant_id AND id = @id FOR UPDATE;

-- name: CreateRole :one
INSERT INTO roles (tenant_id, name, description, created_by, updated_by)
VALUES (@tenant_id, @name, sqlc.narg(description), sqlc.narg(actor_id), sqlc.narg(actor_id))
RETURNING *;

-- name: UpdateRole :one
UPDATE roles SET name = @name, description = sqlc.narg(description), updated_by = sqlc.narg(actor_id)
WHERE tenant_id = @tenant_id AND id = @id
RETURNING *;

-- name: ListRolePermissions :many
SELECT role_id, permission_code FROM role_permissions
WHERE role_id = ANY(@role_ids::bigint[])
ORDER BY role_id, permission_code;

-- name: DeleteRolePermissions :exec
DELETE FROM role_permissions WHERE role_id = @role_id;

-- name: InsertRolePermission :exec
INSERT INTO role_permissions (role_id, permission_code) VALUES (@role_id, @permission_code);

-- Property grants -------------------------------------------------------------

-- name: ListUserGrants :many
SELECT up.property_id, p.code AS property_code, p.name AS property_name, up.role_id, r.name AS role_name
FROM user_properties up
JOIN properties p ON p.id = up.property_id
JOIN roles r ON r.id = up.role_id
WHERE up.tenant_id = @tenant_id AND up.user_id = @user_id
ORDER BY p.code;

-- name: DeleteUserGrants :exec
DELETE FROM user_properties WHERE tenant_id = @tenant_id AND user_id = @user_id;

-- name: InsertUserGrant :exec
INSERT INTO user_properties (tenant_id, user_id, property_id, role_id, created_by)
VALUES (@tenant_id, @user_id, @property_id, @role_id, sqlc.narg(actor_id));

-- name: ListTenantProperties :many
SELECT id, code, name FROM properties WHERE tenant_id = @tenant_id ORDER BY code;

-- Authorization ---------------------------------------------------------------

-- name: PropertyInTenant :one
SELECT EXISTS (SELECT 1 FROM properties WHERE tenant_id = @tenant_id AND id = @property_id);

-- name: GetGrantPermission :one
SELECT EXISTS (
    SELECT 1 FROM role_permissions rp WHERE rp.role_id = up.role_id AND rp.permission_code = @permission_code
) AS allowed
FROM user_properties up
WHERE up.tenant_id = @tenant_id AND up.user_id = @user_id AND up.property_id = @property_id;

-- name: GetTenant :one
SELECT id, code, name, status FROM tenants WHERE id = @id;

-- name: RevokeOtherSessions :exec
UPDATE user_sessions SET revoked_at = @at::timestamptz, revoked_reason = @reason::varchar
WHERE user_id = @user_id AND id <> @keep_session_id AND revoked_at IS NULL;
