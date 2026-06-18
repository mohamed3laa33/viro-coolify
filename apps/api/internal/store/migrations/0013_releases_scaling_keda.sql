-- 0013_releases_scaling_keda: per-deploy release history + rollback, per-app
-- autoscaling bounds, and admin/DB-driven KEDA defaults.
--
-- Wave 9 introduces:
--   * a releases table: every successful Apply for an app records one immutable
--     revision (image + resources + config hash + status), giving the app a deploy
--     history and a target to roll back to.
--   * apps.min_replicas / max_replicas: per-app autoscaling overrides threaded into
--     the KEDA ScaledObject (a stateless app may set min_replicas=0 to scale to zero).
--   * platform_settings KEDA columns: admin-tunable autoscaling defaults so the KEDA
--     min/max/polling/cooldown/trigger are no longer hardcoded in the backend.

-- Per-app autoscaling bounds (0 => use the platform default).
ALTER TABLE apps ADD COLUMN IF NOT EXISTS min_replicas integer NOT NULL DEFAULT 0;
ALTER TABLE apps ADD COLUMN IF NOT EXISTS max_replicas integer NOT NULL DEFAULT 0;

-- Release history. Each row is one immutable deploy revision of an app; the
-- (app_id, revision) pair is unique and monotonic per app.
CREATE TABLE IF NOT EXISTS releases (
    id          text PRIMARY KEY,
    app_id      text NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    org_id      text NOT NULL DEFAULT '',
    revision    integer NOT NULL,
    image       text NOT NULL DEFAULT '',
    git_ref     text NOT NULL DEFAULT '',
    config_hash text NOT NULL DEFAULT '',
    cpu         double precision NOT NULL DEFAULT 0,
    memory_mb   integer NOT NULL DEFAULT 0,
    status      text NOT NULL DEFAULT 'deploying',
    note        text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT releases_app_revision_uniq UNIQUE (app_id, revision)
);
-- Newest revision first per app (the list endpoint orders by revision desc).
CREATE INDEX IF NOT EXISTS releases_app_revision_idx ON releases (app_id, revision DESC);
CREATE INDEX IF NOT EXISTS releases_org_idx ON releases (org_id);

-- Admin/DB-driven KEDA autoscaling defaults. Seeded conservative: a stateless app
-- floors at 1 replica unless an admin drops the default to 0 or a single app sets a
-- per-app override; databases always keep a floor of 1. The CPU trigger needs no
-- extra add-on; the HTTP trigger (keda-http-add-on) is off by default.
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_default_min_replicas integer NOT NULL DEFAULT 1;
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_default_max_replicas integer NOT NULL DEFAULT 5;
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_polling_interval integer NOT NULL DEFAULT 30;
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_cooldown_period integer NOT NULL DEFAULT 300;
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_cpu_utilization integer NOT NULL DEFAULT 70;
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_http_trigger boolean NOT NULL DEFAULT false;
-- Hard ceiling on a per-app MaxReplicas override (the scale endpoint rejects a
-- request above this so a tenant can't request an unbounded autoscaler). 0
-- disables the ceiling; admin-tunable via the settings API.
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS keda_max_replicas_ceiling integer NOT NULL DEFAULT 20;
