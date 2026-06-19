# Vortex — build progress

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

## Milestone 5 — Billing & usage (Stripe test-mode) ✅
- ✅ Plan catalog (Hobby/Launch/Scale, fly.io-style usage pricing), public `/v1/billing/plans`
- ✅ `PaymentProvider` interface: MockProvider (default/dev) + StripeProvider (HTTP, no SDK), feature-flagged
- ✅ Subscriptions + usage metering; org-scoped `/v1/orgs/{orgID}/billing` (member) + `/subscribe` (admin)
- ✅ Stripe webhook with HMAC-SHA256 signature verification (timestamp tolerance)
- ✅ Unit tests: catalog, subscribe/usage/summary, signature verify (valid/tampered/wrong-secret), HTTP authz

## Milestone 6 — Web UI (fly.io look) ✅
- ✅ Next.js 15 (App Router, RSC) + Tailwind + fly.io theme (violet #7C3AED / near-black / mono)
- ✅ Balloon logo (violet→magenta gradient), marketing landing, login/signup
- ✅ Dashboard shell (sidebar + topbar), overview, apps list + app detail (Overview/Logs/Metrics/Env/Settings tabs)
- ✅ Databases, domains, metrics, settings (General/Team/Billing) pages
- ✅ Typed API client + AuthProvider; graceful mock fallback when API is down
- ✅ `npm run build` green (12 routes); vitest 17/17 passing
- ✅ Security: upgraded Next 15.1.6 → **15.5.19** (patched CVE-2025-66478)

## Milestone 7 — Local dev & DO deployment (prepared) ✅
- ✅ Dockerfiles (API: distroless static; Web: Next standalone) + full-stack `docker-compose.full.yml`
- ✅ Helm chart (`deploy/helm/viro`): api/web deployments+services, ingress, secret, values
- ✅ Terraform for DOKS (cluster, VPC, registry, managed Postgres) + `doctl` scripts (provision/build/deploy/teardown)
- ✅ GitHub Actions CI (Go vet/test/build + web install/test/build); `deploy/README.md`
- ⬜ (Live run deferred to tomorrow with your DO credentials — by design)

## Milestone 9 — Org → Project → App hierarchy & invitations ✅
- ✅ `Project` layer (every org auto-gets a `default` project); apps carry `projectId`
- ✅ Project memberships for fine-grained, project-scoped access
- ✅ Invitations: invite by email to an **org** (org role) or a **specific project** (project role),
  token-based accept (email must match), single-use; list + members endpoints
- ✅ `AuthorizeProject` — org admins/owners get all projects; project members scoped to theirs
- ✅ Routes: `/v1/orgs/{orgID}/projects[...]`, `/projects/{projectID}/apps`, `/members`,
  `/invitations`, and `/v1/invitations/accept`
- ✅ Tests: default project, admin-only project create, org invite→admin, project invite→scoped
  access (not org admin, no access to other projects), wrong-email + reuse rejected

## Milestone 10 — CI/CD ✅
- ✅ CI (`ci.yml`): Go vet/test(-race)/build + web install/test/build on every push/PR
- ✅ CD (`deploy.yml`): tag/manual → DOCR login → build+push API & web images → DOKS Helm rollout

## Milestone 8 — Verification (continuous) 🚧
- ✅ Judge agent after each milestone (M1 PASS, M2→P0 found+fixed, M3 PASS, web PASS)
- ✅ Deprecation audits (backend CLEAN; web flagged Next CVE → upgraded to 15.5.19)
- 🚧 Final end-to-end sweep + frontend build verification

## Deploy-readiness waves — delivered ✅
Hardening + platform completeness to make Vortex deploy-ready. All in-tree and tested with the
`kube.FakeBackend`/in-memory store; the one item still pending is a live-cluster run (see below).

- ✅ **One-shot installer** — `deploy/install.sh` is the single env-driven, idempotent entrypoint:
  prereq checks → optional terraform provision → create namespaces + secrets from env →
  `helmfile -f deploy/helmfile.yaml apply` (all addons + `vortex-bootstrap` + control plane) →
  wait for rollouts → print the Gateway LB IP, the wildcard DNS records to set, and the dashboard URL.
- ✅ **Buildpacks (no-Dockerfile) builds** — Cloud Native Buildpacks build source apps without a
  Dockerfile (`VORTEX_BUILDPACKS_BUILDER` → `BuildpacksBuilderImage`,
  `VORTEX_BUILD_BUILDPACKS_IMAGE` → `BuildBuildpacksImage`; threaded onto the builder config in
  `httpx/server.go newBuilder`).
- ✅ **KEDA HTTP wake-from-zero** — KEDA HTTP add-on (interceptor + external scaler) installed via
  helmfile; an app's `http` ScaledObject now actually wakes a 0-replica workload on the first request.
- ✅ **external-dns** — wildcard/custom-domain records published automatically
  (`VORTEX_EXTERNAL_DNS_ENABLED` → `ExternalDNSEnabled`, `VORTEX_EXTERNAL_DNS_ZONES` →
  `ExternalDNSZones`, `VORTEX_DNS_RECORD_TTL` → `DNSRecordTTL`; threaded onto `kube.Config`).
- ✅ **Observability stack** — Prometheus + Grafana (+ Prometheus Operator for the tenant
  common-chart ServiceMonitors) and Loki + promtail, installed by helmfile into `monitoring`.
- ✅ **DB backups** — Velero, installed only when a backup bucket is configured
  (`VORTEX_VELERO_BUCKET` / `VORTEX_VELERO_REGION` / `VORTEX_VELERO_S3_URL`) so we never ship a
  backup tool with nowhere to back up to (invariant #6).
- ✅ **Billing maturity** — admin-set hourly per-component pricing + metering and the public catalog
  flow are wired (still DB/admin-driven, invariant #1).
- ✅ **Gateway sharding** — custom-domain HTTPS listeners auto-shard across a Gateway pool past
  `VORTEX_GATEWAY_SHARD_MAX_LISTENERS` (`GatewayShardMaxListeners`, default 64); with
  `VORTEX_GATEWAY_SHARD_LB_SHARING` (`GatewayShardLBSharing`, bootstrap `gateway.merge: true`) the
  pool shares one Envoy fleet / one LoadBalancer. Config rides `kube.Config`.
- ✅ **Region-aware scheduling** — workloads are scheduled with region awareness (single cluster
  today; multi-region cluster fabric remains deferred).

### Remaining manual exit criterion ⬜
- ⬜ **Live-cluster end-to-end proof** — run `deploy/install.sh` against a real DOKS cluster and
  confirm DNS + wildcard TLS resolve and a tenant app is reachable at its
  `<app>.<project>.<org>.<baseDomain>` host. This is the single step that cannot be proven in-tree.
