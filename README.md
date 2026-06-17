<div align="center">

# Vortex

**A modern, fly.io-style application hosting platform — built on top of [Coolify](https://coolify.io).**

Vortex gives you a beautiful, opinionated developer experience (like fly.io) while using a
self-hosted Coolify instance as the deployment engine, and is designed to run on
DigitalOcean Kubernetes (DOKS).

</div>

---

## What is this?

[Coolify](https://coolify.io) is a powerful open-source PaaS, but its UX is general-purpose.
**Vortex** wraps Coolify with:

- 🎨 **A new, modern UI** in the visual language of **fly.io** (deep violet, near-black surfaces, mono accents).
- 🧠 **A Go control-plane** that owns users, organizations, teams (RBAC), billing and usage — and
  translates product actions into Coolify API calls.
- 💳 **A fly.io-style business model** — usage-based billing via Stripe (test-mode wired by default).
- ☸️ **Cloud-native deployment** — Docker for local dev, Helm/Kubernetes manifests for DigitalOcean.

```
┌────────────┐      ┌────────────────────┐      ┌──────────────────┐
│  Next.js   │ ───▶ │  Vortex control-plane │ ───▶ │ Coolify (/api/v1) │ ──▶ servers / apps
│  (web UI)  │      │   (Go API)          │      │  deploy engine    │
└────────────┘      └────────────────────┘      └──────────────────┘
   fly.io look        auth · orgs · teams           builds · TLS ·
                      billing · usage               databases · domains
```

## Monorepo layout

| Path | What |
|------|------|
| `apps/api` | Go control-plane API (orchestrates Coolify, owns auth/orgs/billing). |
| `apps/web` | Next.js dashboard, fly.io-styled (Tailwind + shadcn/ui). |
| `deploy/`  | Helm chart, Kubernetes manifests, Terraform, and `doctl` deploy scripts for DigitalOcean. |
| `docker/`  | Dockerfiles + `docker-compose` for local development. |
| `docs/`    | Architecture and the extracted Coolify API reference. |

## Quickstart (local development)

> Requires Go 1.26+, Node 20+, Docker.

```bash
# 1. Start local dependencies (Postgres, Redis)
make dev-up

# 2. Run the Go control-plane API (http://localhost:8080)
make api-run

# 3. Run the web UI (http://localhost:3000)
make web-dev
```

Point Vortex at a Coolify instance with `VORTEX_COOLIFY_BASE_URL` and `VORTEX_COOLIFY_TOKEN`
(see `.env.example`). With no Coolify configured, the API still serves health/version and
the UI runs against mock data.

## Deploying to DigitalOcean

Deployment is **prepared but not executed** — no cloud resources are created until you run
the scripts with your own credentials. See [`deploy/README.md`](deploy/README.md).

## Status

This repository is built milestone-by-milestone; see [`PROGRESS.md`](PROGRESS.md) for the
live checklist. The Go control-plane, Coolify client, and the local dev story are first.

## License

MIT — see [`LICENSE`](LICENSE).
