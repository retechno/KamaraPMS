-- +goose Up
-- The standard charge codes every property starts with (is_system = true). Rules (taxes, service charges)
-- are mapped per property afterwards; none are mapped here. One definition, used for new properties
-- (the application calls seed_charge_codes) and for the backfill below. Idempotent.
-- +goose StatementBegin
CREATE FUNCTION seed_charge_codes(p_tenant_id bigint, p_property_id bigint, p_actor_id bigint) RETURNS integer
LANGUAGE plpgsql AS $$
DECLARE
    v_inserted integer;
BEGIN
    INSERT INTO charge_codes (tenant_id, property_id, code, name, charge_type, price_mode, is_system, created_by, updated_by)
    SELECT p_tenant_id, p_property_id, s.code, s.name, s.charge_type, 'EXCLUSIVE', true, p_actor_id, p_actor_id
      FROM (VALUES
        ('ROOM',        'Room charge',                  'ROOM'),
        ('ROOM_EXEMPT', 'Room charge (no tax or service)', 'ROOM'),
        ('BREAKFAST',   'Breakfast',                    'FOOD_BEVERAGE'),
        ('RESTAURANT',  'Restaurant',                   'FOOD_BEVERAGE'),
        ('LAUNDRY',     'Laundry',                      'SERVICE'),
        ('MINIBAR',     'Minibar',                      'FOOD_BEVERAGE'),
        ('EXTRA_BED',   'Extra bed',                    'SERVICE'),
        ('NO_SHOW_FEE', 'No-show fee',                  'FEE'),
        ('CANCEL_FEE',  'Cancellation fee',             'FEE'),
        ('OTHER',       'Other charges',                'OTHER')
      ) AS s (code, name, charge_type)
    ON CONFLICT (property_id, code) DO NOTHING;
    GET DIAGNOSTICS v_inserted = ROW_COUNT;
    RETURN v_inserted;
END
$$;
-- +goose StatementEnd

-- Properties created before M5 have no charge codes yet.
SELECT seed_charge_codes(tenant_id, id, NULL) FROM properties;

-- +goose Down
DROP FUNCTION IF EXISTS seed_charge_codes(bigint, bigint, bigint);
