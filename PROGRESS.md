# Viro — build progress

Live checklist for the overnight build. Each milestone is committed and pushed.

Legend: ✅ done · 🚧 in progress · ⬜ planned

## Milestone 1 — Control-plane foundation ✅
- ✅ Monorepo scaffold, `.gitignore`, Makefile, MIT license, `.env.example`
- ✅ Go API skeleton (`apps/api`): config, structured logging, graceful shutdown
- ✅ HTTP router (chi) with health/readiness, version, CORS, request logging
- ✅ Typed **Coolify API client** (`internal/coolify`) — apps, deploy lifecycle, databases, envs
- ✅ App proxy endpoints (`/v1/apps`, `/v1/databases`) over Coolify
- ✅ Unit tests (Coolify client via httptest + HTTP handlers) — `go test ./...` green
- ✅ Coolify API reference extracted to `docs/COOLIFY_API.md`

## Milestone 2 — Identity, orgs & RBAC ✅
- ✅ Users, organizations, memberships, roles (member/admin/owner, rank-compared)
- ✅ JWT auth (access + refresh, HS256), bcrypt, signup/login/refresh/me
- ✅ Repository layer: `store.Store` interface + thread-safe in-memory impl (Postgres impl plugs into same interface)
- ✅ Auth middleware + `Authorize(min role)` org-scoped checks; unit tests green
- ✅ Folded in M1 judge fixes: prod JWT-secret guard, CORS-wildcard-vs-credentials, bounded upstream body read

## Milestone 3 — App lifecycle, tenant scoping & RBAC enforcement ✅
- ✅ **Fixed M2 judge P0**: resources are now org-scoped + role-authorized at the HTTP layer
- ✅ Tenant-scoped `App`/`Database` records (org-owned, linked to Coolify UUID)
- ✅ `platform` service: create/list/get/deploy/stop/restart/delete apps; create/list databases
  (works in demo mode with no Coolify; calls Coolify when configured)
- ✅ Routes nested under `/v1/orgs/{orgID}/...`; `orgAuthz(role)` middleware (member reads, admin mutates)
- ✅ HTTP + service tests: cross-tenant read → 403, cross-tenant resource → 404, member cannot mutate → 403
- ✅ Folded M2 judge P1: password max-length (72-byte) guard
- ⬜ Deployments history + live logs streaming (next pass)

## Milestone 4 — Databases, domains, metrics ⬜
- ⬜ Managed databases (Postgres/Redis/MySQL/Mongo) via Coolify
- ⬜ Custom domains + TLS; metrics + logs providers; unit tests

## Milestone 5 — Billing & usage (Stripe test-mode) ⬜
- ⬜ Plans, subscriptions, usage metering, webhooks (feature-flagged); unit tests

## Milestone 6 — Web UI (fly.io look) ⬜
- ⬜ Next.js + Tailwind + shadcn/ui, fly.io theme (violet/near-black/mono)
- ⬜ Auth pages, dashboard, apps list/detail, deploy flow, logs/metrics
- ⬜ Databases, domains, team settings, billing; component tests

## Milestone 7 — Local dev & DO deployment (prepared) ⬜
- ⬜ Dockerfiles + `docker-compose` for full local stack
- ⬜ Helm chart + k8s manifests; Terraform for DOKS; `doctl` deploy scripts
- ⬜ GitHub Actions CI (build + test)

## Milestone 8 — Verification ⬜
- ⬜ Judge-agent review after each milestone; end-to-end `make test` green
