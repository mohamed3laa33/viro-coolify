-- 0017_usage_report_state: idempotent metered-usage reporting to the payment
-- provider (Stripe). The metering loop already records per-(org,hour) compute cost;
-- a separate reporting step pushes that usage to Stripe so metered invoices are not
-- $0. Stripe usage records use action=increment, so the reporter must push only the
-- DELTA since the last report (cumulative pushes would double-bill). This table is
-- the per-org watermark of cents already reported for the CURRENT billing period;
-- period_start scopes it so a new billing period resets the reported total to zero.

CREATE TABLE IF NOT EXISTS usage_report_state (
    org_id         text        PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    period_start   timestamptz NOT NULL,
    reported_cents bigint      NOT NULL DEFAULT 0
);
