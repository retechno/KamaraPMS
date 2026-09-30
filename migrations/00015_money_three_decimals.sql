-- +goose Up
-- A property may declare 0 to 3 currency decimals (properties.currency_decimals, e.g. KWD and BHD use 3),
-- but every money column stored two. Widening to three makes the schema match: the engine still rounds at
-- the property's precision, the columns just no longer cut a three-decimal amount. The change keeps every
-- existing value exactly (12.50 becomes 12.500). It rewrites the tables without firing their triggers, so
-- the append-only ledger guards are not involved.
ALTER TABLE charge_codes         ALTER COLUMN default_unit_price  TYPE numeric(18,3);
ALTER TABLE rates                ALTER COLUMN amount              TYPE numeric(18,3);
ALTER TABLE reservation_room_rates
    ALTER COLUMN base_rate       TYPE numeric(18,3),
    ALTER COLUMN discount_amount TYPE numeric(18,3),
    ALTER COLUMN amount          TYPE numeric(18,3);
ALTER TABLE payments             ALTER COLUMN amount              TYPE numeric(18,3);
ALTER TABLE folio_items
    ALTER COLUMN unit_price           TYPE numeric(18,3),
    ALTER COLUMN base_amount          TYPE numeric(18,3),
    ALTER COLUMN discount_amount      TYPE numeric(18,3),
    ALTER COLUMN net_amount           TYPE numeric(18,3),
    ALTER COLUMN rounding_adjustment  TYPE numeric(18,3),
    ALTER COLUMN service_charge_total TYPE numeric(18,3),
    ALTER COLUMN tax_total            TYPE numeric(18,3),
    ALTER COLUMN debit                TYPE numeric(18,3),
    ALTER COLUMN credit               TYPE numeric(18,3);
ALTER TABLE folio_item_components
    ALTER COLUMN base_amount TYPE numeric(18,3),
    ALTER COLUMN amount      TYPE numeric(18,3);

-- +goose Down
-- Narrowing rounds any third decimal away: only run it on data that has none (0- and 2-decimal currencies).
ALTER TABLE folio_item_components
    ALTER COLUMN base_amount TYPE numeric(18,2),
    ALTER COLUMN amount      TYPE numeric(18,2);
ALTER TABLE folio_items
    ALTER COLUMN unit_price           TYPE numeric(18,2),
    ALTER COLUMN base_amount          TYPE numeric(18,2),
    ALTER COLUMN discount_amount      TYPE numeric(18,2),
    ALTER COLUMN net_amount           TYPE numeric(18,2),
    ALTER COLUMN rounding_adjustment  TYPE numeric(18,2),
    ALTER COLUMN service_charge_total TYPE numeric(18,2),
    ALTER COLUMN tax_total            TYPE numeric(18,2),
    ALTER COLUMN debit                TYPE numeric(18,2),
    ALTER COLUMN credit               TYPE numeric(18,2);
ALTER TABLE payments             ALTER COLUMN amount              TYPE numeric(18,2);
ALTER TABLE reservation_room_rates
    ALTER COLUMN base_rate       TYPE numeric(18,2),
    ALTER COLUMN discount_amount TYPE numeric(18,2),
    ALTER COLUMN amount          TYPE numeric(18,2);
ALTER TABLE rates                ALTER COLUMN amount              TYPE numeric(18,2);
ALTER TABLE charge_codes         ALTER COLUMN default_unit_price  TYPE numeric(18,2);
