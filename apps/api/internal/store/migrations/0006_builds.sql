-- 0006_builds: git-source image builds. Each row records one build of an app's
-- git repository into a container image that the deploy backend then rolls out.
-- Builds are tenant-scoped via the owning app (and org); they have no Helm
-- release of their own.

CREATE TABLE IF NOT EXISTS builds (
    id          text PRIMARY KEY,
    app_id      text NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    org_id      text NOT NULL DEFAULT '',
    status      text NOT NULL DEFAULT 'pending',
    commit_ref  text NOT NULL DEFAULT '',
    image       text NOT NULL DEFAULT '',
    logs        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz NOT NULL DEFAULT 'epoch'
);

CREATE INDEX IF NOT EXISTS builds_app_idx ON builds (app_id);
CREATE INDEX IF NOT EXISTS builds_org_idx ON builds (org_id);
