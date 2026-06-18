-- 0008_billing_correctness (Wave 4): make billing correct and real.
--
--   * processed_stripe_events: webhook idempotency. A redelivered Stripe event id
--     is inserted at most once (PK), so SetSubscriptionStatus side effects run once.
--   * meter_state: singleton row tracking the last whole UTC hour already metered,
--     so a restart/second replica never double-counts and a downtime gap is filled
--     exactly once on catch-up.
--   * organizations.spend_cap_cents: per-org hard ceiling on the current-period
--     charge. 0 => fall back to the platform default cap.
--   * platform_settings.grace_past_due / default_spend_cap_cents: admin/DB-driven
--     gating policy (no hardcoded values).
--   * subscriptions lookup indexes by stripe id / customer id, so a webhook can map
--     an event back to an org without metadata.

CREATE TABLE IF NOT EXISTS processed_stripe_events (
    event_id     text PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS meter_state (
    id                boolean PRIMARY KEY DEFAULT true CHECK (id),
    last_metered_hour timestamptz NOT NULL
);

ALTER TABLE organizations ADD COLUMN IF NOT EXISTS spend_cap_cents bigint NOT NULL DEFAULT 0;

ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS grace_past_due boolean NOT NULL DEFAULT false;
ALTER TABLE platform_settings ADD COLUMN IF NOT EXISTS default_spend_cap_cents bigint NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS subscriptions_stripe_sub_idx ON subscriptions (stripe_subscription_id);
CREATE INDEX IF NOT EXISTS subscriptions_stripe_cus_idx ON subscriptions (stripe_customer_id);

-- The metered subscription-ITEM id (si_…) captured from the subscription's items on
-- a customer.subscription.* webhook. Stripe's usage_records endpoint is per-ITEM, so
-- metered usage must be reported against this id (a sub_ id 404s).
ALTER TABLE subscriptions ADD COLUMN IF NOT EXISTS stripe_subscription_item_id text NOT NULL DEFAULT '';

-- Idempotent metering: a usage record id is the deterministic per-(org,hour) bucket
-- id. INSERT ... ON CONFLICT (id) DO NOTHING makes the per-hour write atomic so a
-- restart / second replica / concurrent tick never double-counts. (usage_records.id
-- is already the PRIMARY KEY from 0001_init, which provides the unique constraint.)
