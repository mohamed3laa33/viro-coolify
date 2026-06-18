-- 0016_workload_region: multi-region SEAM.
--
-- Adds a `region` placement column to apps, services and databases. The region is
-- set on create from the request or the platform default region
-- (platform_settings.default_region) and validated against the admin-managed
-- platform_settings.regions list. Today a single cluster IGNORES it; it is plumbed
-- onto the workload as a label/annotation so a future multi-cluster router can place
-- by region. This makes region a real, validated, persisted field (no behavior
-- change today) rather than dead config.

ALTER TABLE apps      ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '';
ALTER TABLE services  ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN IF NOT EXISTS region text NOT NULL DEFAULT '';
