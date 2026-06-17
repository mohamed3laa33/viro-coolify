-- Viro control-plane schema. All identifiers are snake_case.

CREATE TABLE IF NOT EXISTS users (
    id            text PRIMARY KEY,
    email         text NOT NULL,
    name          text NOT NULL DEFAULT '',
    password_hash text NOT NULL DEFAULT '',
    is_admin      boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
-- Case-insensitive unique email (mirrors memory store's email lowercasing).
CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower_uniq ON users (lower(email));

CREATE TABLE IF NOT EXISTS organizations (
    id         text PRIMARY KEY,
    name       text NOT NULL DEFAULT '',
    slug       text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS memberships (
    org_id  text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role    text NOT NULL,
    PRIMARY KEY (org_id, user_id)
);
CREATE INDEX IF NOT EXISTS memberships_user_idx ON memberships (user_id);

CREATE TABLE IF NOT EXISTS projects (
    id         text PRIMARY KEY,
    org_id     text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name       text NOT NULL DEFAULT '',
    slug       text NOT NULL DEFAULT '',
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS projects_org_idx ON projects (org_id);

CREATE TABLE IF NOT EXISTS project_memberships (
    project_id text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX IF NOT EXISTS project_memberships_user_idx ON project_memberships (user_id);

CREATE TABLE IF NOT EXISTS invitations (
    id         text PRIMARY KEY,
    org_id     text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id text NOT NULL DEFAULT '',
    email      text NOT NULL,
    role       text NOT NULL,
    token      text NOT NULL,
    status     text NOT NULL,
    invited_by text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS invitations_token_uniq ON invitations (token);
CREATE INDEX IF NOT EXISTS invitations_org_idx ON invitations (org_id);

CREATE TABLE IF NOT EXISTS apps (
    id             text PRIMARY KEY,
    org_id         text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id     text NOT NULL DEFAULT '',
    coolify_uuid   text NOT NULL DEFAULT '',
    name           text NOT NULL DEFAULT '',
    git_repository text NOT NULL DEFAULT '',
    git_branch     text NOT NULL DEFAULT '',
    build_pack     text NOT NULL DEFAULT '',
    cpu            double precision NOT NULL DEFAULT 0,
    memory_mb      integer NOT NULL DEFAULT 0,
    status         text NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS apps_org_idx ON apps (org_id);

CREATE TABLE IF NOT EXISTS databases (
    id           text PRIMARY KEY,
    org_id       text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    coolify_uuid text NOT NULL DEFAULT '',
    name         text NOT NULL DEFAULT '',
    engine       text NOT NULL DEFAULT '',
    status       text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS databases_org_idx ON databases (org_id);

CREATE TABLE IF NOT EXISTS services (
    id           text PRIMARY KEY,
    org_id       text NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    project_id   text NOT NULL DEFAULT '',
    template     text NOT NULL DEFAULT '',
    name         text NOT NULL DEFAULT '',
    coolify_uuid text NOT NULL DEFAULT '',
    cpu          double precision NOT NULL DEFAULT 0,
    memory_mb    integer NOT NULL DEFAULT 0,
    status       text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS services_org_idx ON services (org_id);

CREATE TABLE IF NOT EXISTS app_env (
    app_id text NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    key    text NOT NULL,
    value  text NOT NULL DEFAULT '',
    PRIMARY KEY (app_id, key)
);

CREATE TABLE IF NOT EXISTS domains (
    id         text PRIMARY KEY,
    org_id     text NOT NULL DEFAULT '',
    app_id     text NOT NULL REFERENCES apps (id) ON DELETE CASCADE,
    domain     text NOT NULL,
    verified   boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS domains_app_idx ON domains (app_id);

CREATE TABLE IF NOT EXISTS subscriptions (
    org_id                 text PRIMARY KEY REFERENCES organizations (id) ON DELETE CASCADE,
    plan_id                text NOT NULL DEFAULT '',
    status                 text NOT NULL DEFAULT '',
    stripe_customer_id     text NOT NULL DEFAULT '',
    stripe_subscription_id text NOT NULL DEFAULT '',
    created_at             timestamptz NOT NULL DEFAULT now(),
    current_period_end     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS usage_records (
    id        text PRIMARY KEY,
    org_id    text NOT NULL,
    metric    text NOT NULL DEFAULT '',
    quantity  bigint NOT NULL DEFAULT 0,
    at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS usage_records_org_idx ON usage_records (org_id);

CREATE TABLE IF NOT EXISTS plans (
    id                     text PRIMARY KEY,
    name                   text NOT NULL DEFAULT '',
    description            text NOT NULL DEFAULT '',
    price_cents            integer NOT NULL DEFAULT 0,
    currency               text NOT NULL DEFAULT '',
    included_hours         integer NOT NULL DEFAULT 0,
    overage_per_hour_cents integer NOT NULL DEFAULT 0,
    stripe_price_id        text NOT NULL DEFAULT '',
    max_cpu                double precision NOT NULL DEFAULT 0,
    max_memory_mb          integer NOT NULL DEFAULT 0,
    max_apps               integer NOT NULL DEFAULT 0,
    is_default             boolean NOT NULL DEFAULT false,
    sort_order             integer NOT NULL DEFAULT 0,
    active                 boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS service_templates (
    key          text PRIMARY KEY,
    name         text NOT NULL DEFAULT '',
    description  text NOT NULL DEFAULT '',
    category     text NOT NULL DEFAULT '',
    kind         text NOT NULL DEFAULT '',
    image        text NOT NULL DEFAULT '',
    default_port integer NOT NULL DEFAULT 0,
    active       boolean NOT NULL DEFAULT false,
    sort_order   integer NOT NULL DEFAULT 0
);

-- Singleton platform settings: a single row enforced by id = true.
CREATE TABLE IF NOT EXISTS platform_settings (
    id                       boolean PRIMARY KEY DEFAULT true,
    default_cpu              double precision NOT NULL DEFAULT 0,
    default_memory_mb        integer NOT NULL DEFAULT 0,
    default_plan_id          text NOT NULL DEFAULT '',
    cpu_overcommit_factor    double precision NOT NULL DEFAULT 0,
    memory_overcommit_factor double precision NOT NULL DEFAULT 0,
    default_region           text NOT NULL DEFAULT '',
    regions                  text[] NOT NULL DEFAULT '{}',
    CONSTRAINT platform_settings_singleton CHECK (id = true)
);
