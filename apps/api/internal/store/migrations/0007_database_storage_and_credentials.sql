-- 0007_database_storage_and_credentials: make managed databases durable and
-- usable. Add the persistent-volume size and the generated connection
-- credentials (db user/password/name) so the connection-info endpoint can return
-- a ready-to-use connection string and the engine container initializes with
-- them on first boot.
--
-- SECURITY: db_password is stored plaintext-at-rest for now; a later security
-- wave must encrypt it or move the source of truth to a Kubernetes Secret.

ALTER TABLE databases ADD COLUMN IF NOT EXISTS storage_gb  integer NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS db_user     text NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN IF NOT EXISTS db_password text NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN IF NOT EXISTS db_name     text NOT NULL DEFAULT '';

-- Backfill legacy rows: a storage_gb of 0 would render a volume-less StatefulSet
-- with Delete retention (data wiped on restart/Stop). Clamp to the built-in
-- minimum (1Gi) so every existing database has a durable, retained volume.
UPDATE databases SET storage_gb = 1 WHERE storage_gb <= 0;
