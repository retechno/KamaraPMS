-- +goose Up
-- The right to cancel the rooms of a whole group in one go (docs/architecture/30-group-folio.md part F). It goes to the supervisor level only: the roles that hold correction.approve, as folio.master_manage
-- and folio.master_close did in 00076. reservation.cancel (cancel one reservation) is a front-desk right and is not enough for a hundred rooms at once. Roles are the tenant's own data, so the grant is made
-- here, once, for the roles that exist; a role created later is the tenant's choice, and a right the tenant took away from a role is not given back (this runs once).
INSERT INTO role_permissions (role_id, permission_code)
SELECT rp.role_id, 'group.cancel' FROM role_permissions rp WHERE rp.permission_code = 'correction.approve'
ON CONFLICT DO NOTHING;

-- +goose Down
-- Nothing to undo: the rows cannot be told apart from rights the tenant gave itself, and a right that stays is harmless (the catalogue still knows the code).
SELECT 1;
