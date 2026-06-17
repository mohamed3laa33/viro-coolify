# CLAUDE.md — developer & agent guide for Vortex

This file orients human contributors **and** AI coding agents. Read it before making changes.
It captures the architecture, the non-negotiable invariants, conventions, and how to extend the
system. Keep it up to date when you change structure or invariants.

## What Vortex is

A fly.io-style application hosting platform. A **Go control plane** (`apps/api`) owns identity,
billing, the catalog and an admin panel, and deploys tenant workloads to **Kubernetes** as Helm
releases. A **Next.js dashboard** (`apps/web`) mirrors the backend. The `vortex` CLI lives in
`apps/cli`. See `README.md` for the feature overview.

## Invariants (do not violate without explicit sign-off)

1. **No static business values.** Plans, pricing, quotas, the service catalog, overcommit factors,
   default plan, and regions come from the **database + admin panel** — never hardcoded in handlers
   or the UI. Seed defaults belong only in `apps/api/internal/store/memory.go` `seed()` (in-memory)
   and the SQL migrations/seed (Postgres). The UI must fetch these from the API, not inline them.
2. **Kubernetes-only runtime.** Tenant apps/services/databases run on K8s via `kube.Backend`.
   Coolify is retired as a runtime and survives only behind the `DeployBackend` seam — do not add
   new runtime paths through it.
3. **Resource overcommit is the cost lever.** Pod `requests = factor × requested`
   (default CPU ×0.2, memory ×0.35), `limits = full requested`. Factors are admin-configurable
   platform settings (`store.DefaultSettings()` seeds them). Overcommit math lives in
   `apps/api/internal/kube/values.go`.
4. **Routing = Gateway API, not ingress-nginx** (ingress-nginx is retired upstream). One shared
   Gateway / one LoadBalancer for everything; **namespace per org-project**. Tenant URL pattern is
   `<app>.<project>.<org>.vortex.v60ai.com`.
5. **No manual kubectl in the deploy path.** Everything installs via `deploy/helmfile.yaml` and the
   bootstrap chart. Cluster comes from `deploy/terraform`.
6. **No demo / fake-success paths.** `kube.FakeBackend` is a real in-memory **test double** so the
   API boots without a cluster — it is not a path that pretends deploys succeeded. Don't add
   "pretend it worked" branches.
7. **Config is `VORTEX_*`** env vars (legacy `VIRO_*` honored via `config.lookup`). Don't read env
   directly outside `apps/api/internal/config`.

## Backend architecture (`apps/api`, Go 1.26)

Module: `github.com/mohamed3laa33/viro-coolify/apps/api`. Router: chi. Logging: slog.

| Package | Responsibility |
|---------|----------------|
| `config` | Loads `VORTEX_*` config; refuses default/empty JWT secret in production. |
| `auth` | Password hashing (bcrypt) and JWT issue/verify (HS256, access + refresh, type-checked). |
| `identity` | Signup/login/refresh, users, orgs, memberships/RBAC, invitations. |
| `httpx` | HTTP server, routes (`server.go`), middleware (authn/authz/CORS/recover), handlers, JSON helpers. |
| `platform` | App/service lifecycle; resolves plans/quotas, normalizes resources, drives `kube.Backend`. |
| `kube` | `Backend` interface, `KubeBackend` (client-go + Helm), overcommit math, workload recipes, `FakeBackend`. |
| `billing` | Plans/subscriptions/usage; `PaymentProvider` (Stripe HTTP + Mock); HMAC webhook verify. |
| `store` | `Store` interface; `memory.go` (dev/tests, seeded) and `postgres.go` (pgx v5 + migrations). |
| `catalog` | Service-template catalog logic. |
| `domain` | Core entities (User, Org, Project, App, Service, Plan, ServiceTemplate, PlatformSettings…). |
| `notify` | Email/notification templates (plaintext). |
| `version` | Build version/commit/time (ldflags-injected). |

**Dependency direction:** `httpx → identity/platform/billing → store + kube`. Keep business logic
out of handlers; handlers parse/authorize/marshal only. The `Store` and `kube.Backend` interfaces
are the seams — depend on them, not concrete types.

### Request/auth model
- Bearer JWT access tokens (15 min) + refresh tokens (30 days, **stateless** — note: no server-side
  revocation today; see Known gaps). `authMiddleware` re-loads the user from the store each request.
- Org-scoped routes use `orgAuthz(role)` / `projectAuthz(role)`; admin routes use `adminMiddleware`
  (admin emails from config). Roles: owner > admin > member.

## Frontend (`apps/web`, Next.js 15, TS, Tailwind)
- App Router + TypeScript. fly.io theme (violet `#7C3AED`, near-black, Inter + JetBrains Mono).
- Org-scoped API client in `apps/web/src/lib` with refresh-on-401 and a mock fallback for local dev.
- **The UI must mirror the backend 1:1** and read catalog/plans/settings from the API (invariant #1).

## Conventions
- **Go:** idiomatic, small packages, wrap errors with `%w`, propagate `context.Context`, no ignored
  errors (errcheck), gofmt clean. Lint with golangci-lint v2 (gosec/dupl/gocritic/staticcheck etc.;
  config `.golangci.yml`). Use sentinel errors (`ErrValidation`, `ErrInvalidCredentials`,
  `store.ErrConflict`, …) and map them to status codes in `httpx`.
- **Tests:** table-driven; HTTP handlers tested via `httptest` with `kube.NewFakeBackend()` and
  `store.NewMemoryStore()` (see `httpx/*_test.go` helpers `doJSON`, `newTestServer`, `signup`).
  Assert **status-code contracts** for error paths, not just happy paths.
- **Secrets:** never commit real secrets; dev defaults are clearly marked and rejected in prod.

## How to add things

- **A new one-click service / template:** add it to the seed defaults in `store/memory.go` and the
  Postgres seed/migration; it must be editable from the admin panel (`/v1/admin/templates`). Do not
  hardcode it in handlers or the UI.
- **A new plan / pricing / quota field:** extend `domain.Plan` + admin CRUD + migrations; surface via
  the public catalog (`/v1/billing/plans`) and read it in the UI from there.
- **A new HTTP route:** register in `httpx/server.go` under the right authz wrapper; put logic in
  `platform`/`identity`/`billing`; add handler + tests.
- **A new workload behavior on K8s:** extend `kube.Backend` (and `FakeBackend`), implement in
  `KubeBackend`, keep overcommit/quota logic centralized in `kube/values.go`.

## Build, test, run

```bash
# Backend
cd apps/api && go vet ./... && go test ./... && go build ./...
cd apps/api && golangci-lint run --timeout 5m ./...   # v2; install per .github/workflows/ci.yml

# Frontend
cd apps/web && npm ci && npm run test && npm run build

# Security (manual; CI runs this job on workflow_dispatch only)
./scripts/security-scan.sh        # trivy + gitleaks against .gitleaks.toml
```

Local dev: `make dev-up` (Postgres/Redis), `make api-run`, `make web-dev`. Set
`VORTEX_DATABASE_URL` to use Postgres; otherwise the in-memory store + `FakeBackend` are used.

## CI / CD

- `.github/workflows/ci.yml` — **API** (vet, race+coverage tests, build, golangci-lint v2,
  govulncheck), **Web** (vitest, build, npm audit), **SAST** (Semgrep). The **Security (Trivy)**
  job is `workflow_dispatch`-only to keep the pipeline fast; run it locally via
  `scripts/security-scan.sh`.
- `.github/workflows/images.yml` — builds & pushes `ghcr.io/<owner>/vortex-{api,web}` (tags:
  branch, `sha-<short>`, `latest`, `v*`) using the built-in `GITHUB_TOKEN`.
- `.github/workflows/deploy.yml` — deploys GHCR images to DOKS via Helm; creates an in-cluster
  `ghcr-pull` secret from `GHCR_PULL_TOKEN` (a PAT with `read:packages`) for private packages.

## Known gaps / roadmap (see PROGRESS.md and the audit reports)

- **Security:** add rate limiting / lockout on auth endpoints; consider refresh-token rotation +
  revocation (today refresh tokens are stateless for up to 30 days) and a logout path.
- **Platform:** code-to-image builder (Kaniko/BuildKit), real metrics/logs pipeline (Prometheus/
  Loki), scale-to-zero idle detection + KEDA HTTP wake, multi-region (deferred).
- **CLI:** `apps/cli` (`vortex`) is in progress.

When you finish a feature, update `PROGRESS.md` and this file if an invariant or structure changed.
