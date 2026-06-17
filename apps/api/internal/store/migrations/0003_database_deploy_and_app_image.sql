-- 0003_database_deploy_and_app_image: make managed databases real deployable
-- workloads (project placement, requested resources, and the Kubernetes
-- namespace/Helm release/host returned by the deploy backend), and let apps
-- deploy directly from a container image.

ALTER TABLE apps ADD COLUMN IF NOT EXISTS image text NOT NULL DEFAULT '';

ALTER TABLE databases ADD COLUMN IF NOT EXISTS project_id text NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN IF NOT EXISTS cpu        double precision NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS memory_mb  integer NOT NULL DEFAULT 0;
ALTER TABLE databases ADD COLUMN IF NOT EXISTS namespace  text NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN IF NOT EXISTS "release"  text NOT NULL DEFAULT '';
ALTER TABLE databases ADD COLUMN IF NOT EXISTS host       text NOT NULL DEFAULT '';
