-- 0004_pricing_components: admin-managed hourly price list. Each row is a billable
-- resource (cpu, memory, storage, ...) priced per unit, per hour. Prices are set
-- by the super-admin; the platform meters workloads against these to compute cost.

CREATE TABLE IF NOT EXISTS pricing_components (
    key            text PRIMARY KEY,
    name           text NOT NULL DEFAULT '',
    unit           text NOT NULL DEFAULT '',
    price_per_hour double precision NOT NULL DEFAULT 0,
    currency       text NOT NULL DEFAULT 'usd',
    active         boolean NOT NULL DEFAULT true,
    sort_order     integer NOT NULL DEFAULT 0
);
