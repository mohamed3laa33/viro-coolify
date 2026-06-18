-- 0012_domain_verified_host_uniq: enforce ONE verified owner per hostname at the
-- DB level. Without this, tenant B could claim + verify a host already VERIFIED by
-- tenant A (after a DNS transfer / dangling DNS / shared TXT), re-issuing the cert
-- and re-pointing the shared Gateway listener — a cross-tenant domain hijack /
-- teardown DoS. The platform also checks GetVerifiedDomainByHost before flipping a
-- domain to verified; this partial unique index is the belt-and-suspenders guard so
-- two verified rows for the same host can never coexist even under a race.
--
-- Case-insensitive (lower(domain)) and scoped to status='verified' only, so any
-- number of pending/failed rows may reference the same host (only ONE can win).
CREATE UNIQUE INDEX IF NOT EXISTS domains_verified_host_uniq
    ON domains (lower(domain))
    WHERE status = 'verified';
