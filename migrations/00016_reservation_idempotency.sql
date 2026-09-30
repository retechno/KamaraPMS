-- +goose Up
-- POST /reservations carries an Idempotency-Key: the same key and body replays the stored reservation.
-- The hash of the request body tells a replay from a different request that reuses the key.
ALTER TABLE reservations
    ADD COLUMN idempotency_key  varchar(100),
    ADD COLUMN idempotency_hash char(64),
    ADD CONSTRAINT reservations_idempotency_ck CHECK ((idempotency_key IS NULL) = (idempotency_hash IS NULL));
CREATE UNIQUE INDEX reservations_idempotency_uk ON reservations (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS reservations_idempotency_uk;
ALTER TABLE reservations
    DROP CONSTRAINT IF EXISTS reservations_idempotency_ck,
    DROP COLUMN IF EXISTS idempotency_hash,
    DROP COLUMN IF EXISTS idempotency_key;
