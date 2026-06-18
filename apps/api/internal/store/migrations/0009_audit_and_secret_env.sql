-- 0009_audit_and_secret_env (Wave 5: security & compliance).
--
--   * audit_events: append-only audit trail of privileged mutations and
--     security-relevant events (admin config changes, secret writes, role/member
--     changes, invitation lifecycle, subscription changes, auth login/logout/
--     failure). Indexed by (org_id, at desc) for the org and platform listings.
--     A secret WRITE logs only the KEY (target_id) — never the value.
--   * app_env.secret: per-entry flag distinguishing a SECRET (encrypted at rest,
--     masked over the API) from plain config. Existing rows default to false.

CREATE TABLE IF NOT EXISTS audit_events (
    id            text PRIMARY KEY,
    org_id        text NOT NULL DEFAULT '',
    actor_user_id text NOT NULL DEFAULT '',
    actor_email   text NOT NULL DEFAULT '',
    action        text NOT NULL,
    target_type   text NOT NULL DEFAULT '',
    target_id     text NOT NULL DEFAULT '',
    metadata      text NOT NULL DEFAULT '',
    at            timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS audit_events_org_at_idx ON audit_events (org_id, at DESC);

ALTER TABLE app_env ADD COLUMN IF NOT EXISTS secret boolean NOT NULL DEFAULT false;
