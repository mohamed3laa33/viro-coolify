<div align="center">

# Vortex

**A modern, fly.io-style application hosting platform — Kubernetes-native, cost-optimized, admin-driven.**

Vortex gives developers a beautiful fly.io-grade experience for launching apps, databases and
one-click services, while running everything directly on **Kubernetes** (DOKS) behind a single
shared load balancer. Plans, pricing, quotas and the service catalog are **100% database- and
admin-driven** — no hardcoded business values.

</div>

---

## What is Vortex?

Vortex is two things:

1. A **Go control plane** that owns identity, organizations/projects/apps, RBAC, billing, quotas,
   the service catalog, and an admin panel — and turns product actions into real Kubernetes
   workloads.
2. A **Next.js dashboard** in the visual language of fly.io (deep violet, near-black surfaces,
   mono accents) that mirrors the backend 1:1.

The deployment runtime is **Kubernetes only**. (An earlier Coolify integration is retired as a
runtime and survives only behind a `DeployBackend` interface.) Tenant workloads run as Helm
releases in a **namespace per org-project**, routed through **one** shared Gateway (Gateway API +
Envoy Gateway — *not* the retired ingress-nginx), with automatic wildcard TLS via cert-manager.

```
┌────────────┐     ┌─────────────────────┐     ┌────────────────────────────┐
│  Next.js   │ ──▶ │  Vortex control plane │ ──▶ │ Kubernetes (DOKS)          │
│  dashboard │     │   (Go API)            │     │  • namespace per org/project│
│  fly.io UI │     │  auth · orgs · billing│     │  • Helm releases (apps/svc) │
└────────────┘     │  catalog · admin      │     │  • Gateway API + Envoy (1 LB)│
                   └─────────────────────┘     │  • KEDA autoscale · TLS      │
                                               └────────────────────────────┘
```

Tenant apps are reachable at **`<app>.<project>.<org>.vortex.v60ai.com`**; the platform itself at
**`vortex.v60ai.com`**.

## Features

### Identity & tenancy
- Email/password **signup, login, JWT** access + refresh tokens (bcrypt hashing).
- **Org → Project → App** hierarchy with **RBAC** (owner/admin/member) and **invitations** scoped
  to an org or a project.
- Super-admin role (configurable admin emails).

### Workloads on Kubernetes
- **Apps**: create, deploy, stop, restart, delete, logs, metrics — driven by a real `kube.Backend`.
- **One-click services catalog** (WordPress, Ghost, Plausible, n8n, PostgreSQL, MySQL, MariaDB,
  MongoDB, Redis, custom Docker image) — catalog entries are DB-backed and admin-editable.
- **Databases** provisioning.
- **Env vars / secrets** and **custom domains** per app.
- **Resource overcommit** as the core cost lever: pod `requests = factor × requested`
  (default CPU ×0.2, memory ×0.35), `limits = full requested` — factors are admin-configurable.

### Routing & TLS
- **One LoadBalancer** for everything via a shared Gateway (Gateway API / Envoy Gateway).
- **Namespace per org-project** with ResourceQuota / LimitRange.
- Wildcard TLS issued by **cert-manager** (DNS-01).
- **KEDA** ScaledObjects for autoscaling.

### Business layer (no static values)
- **Plans, pricing, quotas** and the **service catalog** are stored in the DB and edited from the
  **admin panel** — never hardcoded.
- **Billing** via Stripe (HTTP provider, HMAC-verified webhooks) with a `MockProvider` for local
  dev; subscriptions, usage metering, quota enforcement.
- Platform **settings** (overcommit factors, default plan, regions) are DB-driven and admin-editable.

### Platform & delivery
- **Postgres** store (pgx v5) selected via `VORTEX_DATABASE_URL`, with migrations + seed; an
  in-memory store satisfies the same interface for dev/tests.
- **Container images** published to **GitHub Container Registry** (`ghcr.io/<owner>/vortex-{api,web}`)
  by CI; deployed to DOKS with a durable image pull secret.
- **Fully-automated deploy** via **helmfile** (cert-manager, Envoy Gateway, KEDA, metrics-server,
  bootstrap + app charts) — **no manual kubectl**. Cluster provisioned with **Terraform**.
- **CI**: GitHub Actions for the Go API (vet, race tests + coverage, build, golangci-lint v2,
  govulncheck), the web app (vitest, build, npm audit), and SAST (Semgrep). Security scanning
  (Trivy + gitleaks) runs on demand and via `./scripts/security-scan.sh`.

> **Roadmap / in progress:** code-to-image builder (Kaniko/BuildKit), real metrics/logs pipeline
> (Prometheus/Loki), scale-to-zero idle detection + KEDA HTTP wake, the `vortex` CLI, and
> multi-region (deferred). See [`PROGRESS.md`](PROGRESS.md).

## Monorepo layout

| Path | What |
|------|------|
| `apps/api` | Go 1.26 control-plane API. Packages: `auth`, `identity`, `httpx`, `platform`, `billing`, `kube`, `store` (memory + postgres), `catalog`, `domain`, `config`, `notify`, `version`. |
| `apps/web` | Next.js 15 dashboard (TypeScript, Tailwind, fly.io theme) + admin panel. |
| `apps/cli` | The `vortex` CLI (flyctl-equivalent). *In progress.* |
| `deploy/`  | `helmfile.yaml`, Helm charts (`helm/viro`, `charts/*`), raw `k8s/` manifests, and `terraform/` for DOKS. |
| `docker/`  | Dockerfiles (`Dockerfile.api`, `Dockerfile.web`) + `docker-compose` for local dev. |
| `docs/`    | Architecture and reference docs. |
| `scripts/` | Helper scripts (`security-scan.sh`, `ghcr-verify.sh`). |

## Quickstart (local development)

> Requires Go 1.26+, Node 22+, Docker.

```bash
# 1. Start local dependencies (Postgres, Redis)
make dev-up

# 2. Run the Go control-plane API (http://localhost:8080)
make api-run

# 3. Run the web UI (http://localhost:3000)
make web-dev
```

With no Kubernetes cluster reachable, the API boots against an in-memory `FakeBackend` (a real
test double, **not** a demo success path) so the full UX works locally. Set `VORTEX_DATABASE_URL`
to use Postgres instead of the in-memory store.

### Tests

```bash
cd apps/api && go test ./...      # backend
cd apps/web && npm run test       # frontend (vitest)
./scripts/security-scan.sh        # trivy + gitleaks (local)
```

## Configuration

All runtime config is read from `VORTEX_*` environment variables (a legacy `VIRO_*` fallback is
honored). Key ones:

| Var | Purpose |
|-----|---------|
| `VORTEX_DATABASE_URL` | Postgres DSN; when set, the Postgres store is used (else in-memory). |
| `VORTEX_JWT_SECRET` | JWT signing secret (required in production). |
| `VORTEX_ADMIN_EMAILS` | Comma-separated super-admin emails. |
| `VORTEX_BASE_DOMAIN` | Tenant base domain (e.g. `vortex.v60ai.com`). |
| `VORTEX_KUBECONFIG` | Path to kubeconfig (omit for in-cluster config). |
| `VORTEX_GATEWAY_NAME` / `VORTEX_GATEWAY_NAMESPACE` | Shared Gateway to attach HTTPRoutes to. |
| `VORTEX_BILLING_ENABLED`, `VORTEX_STRIPE_SECRET_KEY`, `VORTEX_STRIPE_WEBHOOK_SECRET` | Billing. |

## Deploying to DigitalOcean

Images are built and pushed to GHCR by `.github/workflows/images.yml`. The cluster is provisioned
with Terraform and the full platform is installed via `helmfile` — see [`deploy/README.md`](deploy/README.md)
and [`deploy/KUBERNETES.md`](deploy/KUBERNETES.md). No cloud resources are created until you run the
scripts with your own credentials.

## Contributing / further development

See **[`CLAUDE.md`](CLAUDE.md)** for the architecture map, invariants, conventions, and how to add
features (it's written for both human contributors and AI coding agents).

## License

MIT — see [`LICENSE`](LICENSE).
