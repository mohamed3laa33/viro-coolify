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

## Milestone 2 — Identity, orgs & RBAC ⬜
- ⬜ Users, organizations, teams, memberships, roles (member/admin/owner)
- ⬜ JWT auth (access + refresh), bcrypt, signup/login/refresh
- ⬜ Repository layer with in-memory (tests) + Postgres implementations
- ⬜ Auth middleware + org-scoped authorization; unit tests

## Milestone 3 — App lifecycle & deploys ⬜
- ⬜ Create app (git/dockerfile/image), deploy/stop/restart/delete via Coolify
- ⬜ Deployments + status, logs streaming endpoint
- ⬜ Secrets/env management; unit tests

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
