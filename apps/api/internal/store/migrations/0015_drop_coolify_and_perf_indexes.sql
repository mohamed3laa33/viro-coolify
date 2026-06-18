-- 0015_drop_coolify_and_perf_indexes: remove the dead Coolify-era columns and add
-- the missing hot-path indexes for the growth-prone reads.
--
-- Wave 11 cleanup:
--   * Drop apps/services/databases.coolify_uuid — the internal/coolify package was
--     removed long ago and the K8s deploy path never reads or writes these columns.
--     The Go structs no longer reference them, so the columns are pure dead weight.
--   * Add time-ordered/foreign-key indexes the paginated list reads rely on so they
--     never fall back to a full table scan:
--       - usage_records(org_id, at DESC): hourly metering writes + period reads.
--       - apps(project_id): project-scoped app listings and the DeleteProject guard.
--       - builds(app_id, created_at DESC): newest-first build listings.
--   * audit_events(org_id, at DESC) already exists (0009); released history index
--     releases(app_id, revision DESC) already exists (0013).

-- Drop the unused Coolify identity columns.
ALTER TABLE apps DROP COLUMN IF EXISTS coolify_uuid;
ALTER TABLE services DROP COLUMN IF EXISTS coolify_uuid;
ALTER TABLE databases DROP COLUMN IF EXISTS coolify_uuid;

-- Time-ordered usage index (the prior usage_records_org_idx covered only org_id;
-- the period/paginated reads order by at DESC).
CREATE INDEX IF NOT EXISTS usage_records_org_at_idx ON usage_records (org_id, at DESC);

-- Project-scoped app lookups (listings + the DeleteProject in-use guard).
CREATE INDEX IF NOT EXISTS apps_project_idx ON apps (project_id);

-- Newest-first build listings per app.
CREATE INDEX IF NOT EXISTS builds_app_created_idx ON builds (app_id, created_at DESC);
