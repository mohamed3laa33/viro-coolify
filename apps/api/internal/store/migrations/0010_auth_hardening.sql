-- 0010_auth_hardening: refresh-token expiry, password-reset tokens, and
-- invitation expiry. These back Wave 6 auth hardening:
--   * refresh_tokens.expires_at lets the cleanup ticker GC expired rows and lets
--     refresh reject an expired stored token.
--   * password_reset_tokens is a single-use, time-limited, hashed-at-rest table
--     backing the forgot/reset password flow.
--   * invitations.expires_at lets accept reject an expired invitation.

-- Refresh-token expiry. Existing rows get a NULL -> treated as "no stored
-- expiry" (the JWT's own exp still bounds them); new rows always set it.
ALTER TABLE refresh_tokens ADD COLUMN IF NOT EXISTS expires_at timestamptz;
CREATE INDEX IF NOT EXISTS refresh_tokens_expires_idx ON refresh_tokens (expires_at);

-- Single-use, time-limited password-reset tokens. Only the SHA-256 hash of the
-- emailed token is stored; used_at NULL means unconsumed.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash text NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS password_reset_tokens_hash_uniq ON password_reset_tokens (token_hash);
CREATE INDEX IF NOT EXISTS password_reset_tokens_user_idx ON password_reset_tokens (user_id);

-- Invitation expiry. Existing rows get NULL -> treated as "never expires".
ALTER TABLE invitations ADD COLUMN IF NOT EXISTS expires_at timestamptz;
