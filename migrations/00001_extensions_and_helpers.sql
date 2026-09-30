-- +goose Up
-- btree_gist: lets EXCLUDE constraints combine bigint equality with daterange overlap.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- Keeps updated_at correct no matter which code path updates a row.
-- +goose StatementBegin
CREATE FUNCTION set_updated_at() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Generic guard for append-only tables (ledger, logs).
-- SQLSTATE 23001 (restrict_violation) so the application maps it as an integrity error.
-- +goose StatementBegin
CREATE FUNCTION forbid_modification() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% is append-only: % is not allowed', TG_TABLE_NAME, TG_OP
        USING ERRCODE = 'restrict_violation', CONSTRAINT = TG_TABLE_NAME || '_append_only';
END
$$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION IF EXISTS forbid_modification();
DROP FUNCTION IF EXISTS set_updated_at();
DROP EXTENSION IF EXISTS btree_gist;
