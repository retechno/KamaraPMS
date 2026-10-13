-- +goose Up
-- The rights of the master folio (docs/architecture/30-group-folio.md 13.2). Roles are the tenant's own data, so the grant is made here, once, for the roles that exist. Both rights go to the
-- supervisor level only: the roles that hold correction.approve (the right to approve adjustments, reversals, voids and refunds with one's own credentials). folio.post_charge and folio.reverse
-- are cashier-level rights (most cashier roles hold them) and are not enough: the bill of a whole group and its routing are not for every cashier.
-- folio.transfer_any is given to nobody. A role created later is the tenant's choice, and a right the tenant took away from a role is not given back (this runs once).
INSERT INTO role_permissions (role_id, permission_code)
SELECT rp.role_id, 'folio.master_manage' FROM role_permissions rp WHERE rp.permission_code = 'correction.approve'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_code)
SELECT rp.role_id, 'folio.master_close' FROM role_permissions rp WHERE rp.permission_code = 'correction.approve'
ON CONFLICT DO NOTHING;

-- +goose Down
-- Nothing to undo: the rows cannot be told apart from rights the tenant gave itself, and a right that stays is harmless (the catalogue still knows both codes).
SELECT 1;
