-- 0011_custom_domains_verification: DNS-TXT ownership verification + TLS state
-- for custom domains (Wave 8). A custom domain is now PENDING on add and only
-- routed/issued-TLS once its ownership is verified via a DNS TXT challenge.
--
--   * status: pending | verified | failed (source of truth; verified mirrors the
--     legacy boolean `verified` column for back-compat).
--   * verification_token: the random value the tenant must publish as a TXT record
--     at _vortex-challenge.<domain> to prove ownership (crypto/rand).
--   * verified_at: when ownership was last proven.
--
-- Existing rows: a previously-verified row maps to status 'verified'; everything
-- else is treated as 'pending' until re-verified.
ALTER TABLE domains ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'pending';
ALTER TABLE domains ADD COLUMN IF NOT EXISTS verification_token text NOT NULL DEFAULT '';
ALTER TABLE domains ADD COLUMN IF NOT EXISTS verified_at timestamptz;

-- Back-fill status from the legacy boolean for rows created before this wave.
UPDATE domains SET status = 'verified' WHERE verified = true AND status = 'pending';
