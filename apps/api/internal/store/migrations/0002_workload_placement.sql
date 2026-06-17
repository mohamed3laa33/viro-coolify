-- 0002_workload_placement: persist the Kubernetes placement (namespace / Helm
-- release / generated host) returned by the deploy backend for apps and services.

ALTER TABLE apps ADD COLUMN IF NOT EXISTS namespace text NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN IF NOT EXISTS release   text NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN IF NOT EXISTS host      text NOT NULL DEFAULT '';

ALTER TABLE services ADD COLUMN IF NOT EXISTS namespace text NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN IF NOT EXISTS release   text NOT NULL DEFAULT '';
ALTER TABLE services ADD COLUMN IF NOT EXISTS host      text NOT NULL DEFAULT '';
