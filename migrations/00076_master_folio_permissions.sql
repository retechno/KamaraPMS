-- +goose Up
-- The rights of the master folio (docs/architecture/30-group-folio.md 13.2). Roles are the tenant's own data, so the grant is made here, once, for the roles that exist:
--   folio.master_manage (open the master, set the routing) goes to every role that holds folio.reverse, the right to move a charge to another folio (the ordinary transfer);
--   folio.master_close (close the master) goes to every role that holds folio.post_charge, the right that closes a folio of a reservation (CloseFolio).
-- folio.transfer_any is given to nobody. A role created later is the tenant's choice, and a right the tenant took away from a role is not given back (this runs once).
INSERT INTO role_permissions (role_id, permission_code)
SELECT rp.role_id, 'folio.master_manage' FROM role_permissions rp WHERE rp.permission_code = 'folio.reverse'
ON CONFLICT DO NOTHING;
INSERT INTO role_permissions (role_id, permission_code)
SELECT rp.role_id, 'folio.master_close' FROM role_permissions rp WHERE rp.permission_code = 'folio.post_charge'
ON CONFLICT DO NOTHING;

-- +goose Down
-- Nothing to undo: the rows cannot be told apart from rights the tenant gave itself, and a right that stays is harmless (the catalogue still knows both codes).
SELECT 1;
