-- 0005_refresh_tokens: persisted refresh-token records backing rotation and
-- revocation. Each row is keyed by the refresh JWT's jti claim. A refresh token
-- is only honored while a matching, non-revoked row exists; on rotation the old
-- row is revoked and a new one inserted, so reuse of a revoked/unknown jti is
-- detected and rejected.

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    revoked    boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS refresh_tokens_user_idx ON refresh_tokens (user_id);
