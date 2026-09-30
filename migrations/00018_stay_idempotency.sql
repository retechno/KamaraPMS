-- +goose Up
-- Check-in and walk-in carry an Idempotency-Key: the same key replays the stay it created.
ALTER TABLE stays ADD COLUMN idempotency_key varchar(100);
CREATE UNIQUE INDEX stays_idempotency_uk ON stays (property_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS stays_idempotency_uk;
ALTER TABLE stays DROP COLUMN IF EXISTS idempotency_key;
