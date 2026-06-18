-- 0018_pricing_default_rates: fix the billing "setup cliff". Migration 0004
-- created pricing_components with price_per_hour DEFAULT 0, and the seed inserted
-- the canonical cpu/memory/storage rows at 0 — so a fresh deploy metered every
-- workload at $0 and billed nothing until an admin manually set prices. This
-- backfills sensible NON-ZERO starting hourly rates for the three seeded
-- components on clusters that still carry the zero-price seed.
--
-- These rates are conservative defaults an operator is EXPECTED to tune to their
-- infra cost via the super-admin pricing API (PATCH /v1/admin/pricing/<key>) —
-- they are not a hardcoded business policy and live only here in the seed/
-- migration (invariant #1). They mirror store.defaultPricing() so a fresh
-- in-memory or Postgres deploy starts from the same rates.
--
-- Scope: only rows whose price is still exactly 0 are touched, so an admin who
-- has already set a real price is never overwritten. (A price deliberately set
-- back to 0 is indistinguishable from the unset seed and would be re-defaulted;
-- re-zero it via the admin API if that is intended.)

UPDATE pricing_components SET price_per_hour = 0.025  WHERE key = 'cpu'     AND price_per_hour = 0;
UPDATE pricing_components SET price_per_hour = 0.005  WHERE key = 'memory'  AND price_per_hour = 0;
UPDATE pricing_components SET price_per_hour = 0.0002 WHERE key = 'storage' AND price_per_hour = 0;
