-- +goose Up
-- What an audit entry is about, as a person reads it: "305", "RES000012", "FOL000026", a code or a name, written by the module that writes the entry. The kind of thing
-- ("Room", "Reservation") is entity_type and is said in the language of the screen, so the label is the identifier alone. It is NULL for the entries written before this column
-- and for those that have no readable number (the audit trail is append-only: nothing is filled in afterwards). It never holds the name of a guest (the code of the guest is
-- used), because an entry cannot be erased and a person may ask for their data to be.
--
-- varchar(120): the longest label is the e-mail of a user (a name of a budget or a company is shorter); the writer cuts a longer one at a character, never in the middle of one.
ALTER TABLE audit_logs ADD COLUMN entity_label varchar(120);

-- pg_trgm: the search of the audit trail finds a part of a label ("0012" in RES000012) through this index, tenant and property being filtered as before. Trusted since
-- PostgreSQL 13: the owner of the database may create it, like btree_gist.
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX audit_logs_label_trgm_idx ON audit_logs USING gin (entity_label gin_trgm_ops) WHERE entity_label IS NOT NULL;

-- +goose Down
DROP INDEX IF EXISTS audit_logs_label_trgm_idx;
ALTER TABLE audit_logs DROP COLUMN IF EXISTS entity_label;
DROP EXTENSION IF EXISTS pg_trgm;
