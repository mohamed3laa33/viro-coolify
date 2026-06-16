# Viro architecture

## Overview

Viro is a **control-plane + UI** layered over Coolify. Coolify remains the deployment engine
(it talks to servers over SSH, builds images, runs Traefik, provisions TLS and databases).
Viro owns the **product**: identity, multi-tenancy, the fly.io-style experience, and billing.

```
            ┌──────────────────────────────────────────────┐
 Browser ──▶│  apps/web  (Next.js, fly.io theme)            │
            └───────────────┬──────────────────────────────┘
                            │ HTTPS (JSON, JWT)
            ┌───────────────▼──────────────────────────────┐
            │  apps/api  (Go control-plane)                 │
            │  ┌───────────┐ ┌──────────┐ ┌──────────────┐  │
            │  │ identity  │ │ billing  │ │ orchestration│  │
            │  │ orgs/RBAC │ │ usage    │ │  (Coolify)   │  │
            │  └─────┬─────┘ └────┬─────┘ └──────┬───────┘  │
            │        │            │              │          │
            │   Postgres     Stripe (test)   Coolify /api/v1│
            └────────┴────────────┴──────────────┴──────────┘
                                                  │ SSH
                                            servers / workloads
```

## Components

### apps/api — Go control-plane
- **HTTP**: chi router, structured `slog` logging, graceful shutdown, scoped CORS.
- **coolify**: typed client for Coolify `/api/v1` (the orchestration backend).
- **identity** *(M2)*: users, organizations, teams, memberships, roles; JWT auth.
- **store** *(M2)*: repository interfaces with an in-memory implementation (tests/dev) and a
  Postgres implementation (production).
- **billing** *(M5)*: Stripe (test-mode), plans, subscriptions, usage metering — feature-flagged.

Design rules:
- The control-plane never embeds Coolify business logic; it **calls Coolify's API** and stores
  the returned resource UUIDs against Viro records.
- Everything is interface-driven so units test without a database, network, or Stripe.

### apps/web — Next.js dashboard
- App Router, TypeScript, Tailwind + shadcn/ui, fly.io visual language (violet `#7C3AED`,
  near-black surfaces, mono for machine output). Talks only to `apps/api`.

### Data model (control-plane)
```
User ─< Membership >─ Organization ─< Team
Organization ─< App ─< Deployment
Organization ─< Database
Organization ─1─ Subscription ─< UsageRecord
App ─< Domain,  App ─< Secret
```
Each `App`/`Database` carries a `coolify_uuid` linking it to the Coolify resource.

## Multi-tenancy & RBAC
- Tenancy boundary is the **Organization**. Roles: `member < admin < owner` (rank-compared).
- Every request is authorized against the caller's membership in the target organization.

## Environments
- **Local dev**: `docker-compose` runs Postgres + Redis; the Go API and Next.js run on the host
  (or in compose). Coolify is optional locally — mock/skip when not configured.
- **DigitalOcean**: DOKS cluster; Viro API + web deployed via Helm; managed Postgres; Coolify
  runs on its own droplet/node and is reached over its API. (Prepared, not executed.)

## Security
- JWT access/refresh; bcrypt password hashing; secrets never logged.
- Coolify token and Stripe keys are injected via env/secrets, never committed.
- CORS limited to configured origins.
