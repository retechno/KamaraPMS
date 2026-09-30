-- +goose Up
-- Why a session ended. Refresh-token reuse detection must only fire for tokens
-- that were superseded by rotation (a copied token); a token presented after a
-- normal logout is just a stale browser tab and must not end every session.
ALTER TABLE user_sessions ADD COLUMN revoked_reason varchar(20);
UPDATE user_sessions SET revoked_reason = 'LOGOUT' WHERE revoked_at IS NOT NULL;
ALTER TABLE user_sessions
    ADD CONSTRAINT user_sessions_revoked_reason_ck CHECK (
        revoked_reason IS NULL OR revoked_reason IN
            ('ROTATED', 'LOGOUT', 'REUSE_DETECTED', 'PASSWORD_CHANGED', 'PASSWORD_RESET', 'DEACTIVATED', 'ACCOUNT_DISABLED')),
    ADD CONSTRAINT user_sessions_revoked_ck CHECK ((revoked_at IS NULL) = (revoked_reason IS NULL));
CREATE INDEX user_sessions_active_idx ON user_sessions (user_id) WHERE revoked_at IS NULL;

-- +goose Down
DROP INDEX IF EXISTS user_sessions_active_idx;
ALTER TABLE user_sessions DROP CONSTRAINT IF EXISTS user_sessions_revoked_ck;
ALTER TABLE user_sessions DROP CONSTRAINT IF EXISTS user_sessions_revoked_reason_ck;
ALTER TABLE user_sessions DROP COLUMN IF EXISTS revoked_reason;
