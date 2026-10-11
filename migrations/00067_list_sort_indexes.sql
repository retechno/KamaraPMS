-- +goose Up
-- The order of the paged lists (reservations, folios, payments, guests, the audit trail) is asked of the server. Two indexes serve the orders that a screen opens with.
--
-- Guests by the name that is shown ("Siti Rahmawati": the first name, then the last; a single name is the last name), in the collation of ICU. The collation of the database is not
-- one for names: on the Alpine image of PostgreSQL (musl) the "en_US" collation is not applied and the order is by code point, so "Eva" < "Zed" < "adi" < "Éric". The ICU root collation
-- (und-x-icu) puts the letters in the order of the alphabet, an accent beside its letter and a capital beside a small one: adi < Éric < Eva < Zed. The expression is the one of the query
-- (SearchGuests / ListGuestsSorted) letter for letter, or the index is not used. If ICU is missing from the server, the migration stops here, with this message, not later in a query.
--
-- A new version of ICU on the server can change the order of some characters and then the index must be rebuilt (REINDEX): see docs/deployment.md.
-- +goose StatementBegin
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_collation WHERE collname = 'und-x-icu') THEN
        RAISE EXCEPTION 'the ICU collation "und-x-icu" is required (PostgreSQL built with ICU); the order of guest names depends on it';
    END IF;
END
$$;
-- +goose StatementEnd

CREATE INDEX guests_display_name_idx ON guests (tenant_id, (lower(coalesce(nullif(first_name, '') || ' ', '') || last_name) COLLATE "und-x-icu"), id);

-- Payments, newest first by the time they were posted (paid_at), the id breaking a tie. A write costs one entry more in this index: payments are posted a few hundred times a day.
CREATE INDEX payments_paid_at_idx ON payments (property_id, paid_at DESC, id DESC);

-- +goose Down
DROP INDEX IF EXISTS payments_paid_at_idx;
DROP INDEX IF EXISTS guests_display_name_idx;
