-- 0014_api_tokens: personal access tokens (PAT).
--
-- A PAT is a long-lived "vrt_<random>" credential a user issues to authenticate
-- API/CLI requests without a browser session. Only the SHA-256 hash of the full
-- token is stored — never the plaintext, which is shown to the user exactly once
-- at creation. A request bearing "Authorization: Bearer vrt_<token>"
-- authenticates as the token's owner (user_id).
CREATE TABLE IF NOT EXISTS api_tokens (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name         text NOT NULL,
    token_hash   text NOT NULL,
    prefix       text NOT NULL DEFAULT '',
    scopes       text[] NOT NULL DEFAULT '{}',
    expires_at   timestamptz,
    last_used_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- token_hash is the auth lookup key for every PAT-authenticated request; it must
-- be unique so a hash resolves to exactly one owner.
CREATE UNIQUE INDEX IF NOT EXISTS api_tokens_hash_uniq ON api_tokens (token_hash);
CREATE INDEX IF NOT EXISTS api_tokens_user_idx ON api_tokens (user_id);
