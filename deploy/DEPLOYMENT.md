# Vortex — Deployment Runbook

A step-by-step guide to deploy the **Vortex** application-hosting platform onto a
Kubernetes cluster. Written for an operator with **no prior knowledge of the
codebase**. Follow the sections in order.

> **One-line summary:** provision a cluster → set a handful of env vars → run
> `deploy/install.sh` → create DNS records → verify a sample app goes live over
> HTTPS.

---

## 1. What you are deploying

Vortex is a fly.io-style hosting platform with three parts:

| Component | What it is | Runs as |
|-----------|-----------|---------|
| **Control plane (API)** | Go service: identity, billing, catalog, admin, and the deploy engine that turns tenant apps into Kubernetes workloads | Deployment `vortex` in namespace `vortex` |
| **Dashboard (Web)** | Next.js UI mirroring the API | Deployment in namespace `vortex` |
| **Platform add-ons** | cert-manager, Envoy Gateway (Gateway API), KEDA + HTTP add-on, metrics-server, Prometheus/Loki/Grafana, Velero, external-dns | Their own namespaces |

**Routing model:** one shared **Gateway** behind **one LoadBalancer**. Each tenant
app is reachable at `‹app›.‹project›.‹org›.‹BASE_DOMAIN›` (e.g.
`web.shop.acme.vortex.v60ai.com`). TLS is issued automatically by cert-manager
(DNS-01). Everything installs via Helm/helmfile — **no manual `kubectl` in the
deploy path** beyond the secrets the installer creates for you.

Everything below is driven by **one script: `deploy/install.sh`**. It is
idempotent — safe to re-run; it converges the cluster instead of failing.

---

## 2. Prerequisites

### Tools (on the machine running the install)
| Tool | Min version | Notes |
|------|-------------|-------|
| `kubectl` | 1.28+ | talks to the cluster |
| `helm` | 3.14+ | chart installs |
| `helmfile` | 0.165+ | orchestrates all releases |
| `terraform` | 1.6+ | only if provisioning the cluster (skip with `--skip-provision`) |
| `doctl` | latest | only if provisioning DigitalOcean DOKS |
| `git`, `openssl`, `bash` 4+ | — | secret generation / general |

### Accounts / access
- A **Kubernetes cluster** (DigitalOcean DOKS by default) **or** the ability to
  create one (DO API token + terraform).
- A **container registry** holding the `vortex-api` and `vortex-web` images
  (DigitalOcean Container Registry, GHCR, etc.) — see §3.
- A **domain** you control DNS for (the `BASE_DOMAIN`), with API access if you
  want automated DNS (external-dns) — recommended.

---

## 3. Build & publish the images (once per release)

The installer does **not** build images; it expects them in a registry your
cluster can pull from.

**Option A — use the CI-built GHCR images (private):** the repo's
`.github/workflows/images.yml` publishes `ghcr.io/‹owner›/vortex-{api,web}`.
Provide a pull token at install time:
```bash
export VORTEX_IMAGE_REGISTRY=ghcr.io/<owner>
export VORTEX_IMAGE_TAG=latest        # or a sha-/v* tag
export GHCR_PULL_TOKEN=<PAT with read:packages>   # installer creates the ghcr-pull secret
```

**Option B — build & push yourself:**
```bash
# helper in the repo (DOCR example); or use your own docker build/push
VORTEX_IMAGE_REGISTRY=registry.digitalocean.com/<your-registry> \
VORTEX_IMAGE_TAG=$(git rev-parse --short HEAD) \
  deploy/scripts/02-build-and-push.sh
```
Then pass the same `VORTEX_IMAGE_REGISTRY` / `VORTEX_IMAGE_TAG` to the installer.

---

## 4. Configuration (environment variables)

Create a `vortex.env` you will `source` before installing. **Generate strong
secrets** — the API refuses to boot with default/empty security secrets.

```bash
# ---- vortex.env ----
# REQUIRED
export VORTEX_JWT_SECRET=$(openssl rand -hex 32)
export VORTEX_SECRET_ENCRYPTION_KEY=$(openssl rand -hex 32)
export VORTEX_PG_PASSWORD=$(openssl rand -hex 24)
export VORTEX_DATABASE_URL="postgres://vortex:${VORTEX_PG_PASSWORD}@vortex-postgres.vortex.svc.cluster.local:5432/vortex?sslmode=require"
export VORTEX_DO_TOKEN="dop_v1_xxx"           # DigitalOcean token: wildcard TLS (DNS-01) + terraform

# IMAGES (see §3)
export VORTEX_IMAGE_REGISTRY="ghcr.io/<owner>"
export VORTEX_IMAGE_TAG="latest"
export GHCR_PULL_TOKEN="<PAT read:packages>"  # only for a private registry

# PLATFORM
export VORTEX_BASE_DOMAIN="vortex.v60ai.com"
export VORTEX_ADMIN_EMAILS="you@example.com"   # who gets admin-panel access

# OPTIONAL — billing (Stripe). Omit to run without billing.
export VORTEX_STRIPE_SECRET_KEY=""
export VORTEX_STRIPE_WEBHOOK_SECRET=""

# OPTIONAL — automated DNS (RECOMMENDED, see §6). Publishes a record per app.
export VORTEX_EXTERNAL_DNS_ENABLED="true"
export VORTEX_EXTERNAL_DNS_ZONES="vortex.v60ai.com"
export VORTEX_EXTERNAL_DNS_DO_TOKEN="${VORTEX_DO_TOKEN}"   # or a separate DNS-scoped token

# OPTIONAL — backups (Velero). Omit the bucket to skip the backup stack.
export VORTEX_VELERO_BUCKET=""
export VORTEX_VELERO_REGION=""
export VORTEX_VELERO_S3_URL=""        # e.g. https://nyc3.digitaloceanspaces.com
export VORTEX_VELERO_CREDENTIALS_FILE=""   # AWS-style INI creds file
```

Full flag/var reference: `deploy/install.sh --help`.

---

## 5. Install (the one command)

**Always dry-run first** — it runs `terraform plan` + `helmfile template` and
changes nothing:
```bash
source vortex.env
deploy/install.sh --skip-provision --dry-run     # against an EXISTING cluster
# or, to also provision a fresh DOKS cluster:
deploy/install.sh --dry-run
```

Then apply:
```bash
source vortex.env
deploy/install.sh --yes                 # provisions cluster + installs everything
# OR on a cluster you already have (current kubeconfig):
deploy/install.sh --skip-provision --yes
```

**What it does, in order (each step idempotent):**
1. **Prereq checks** — tools, required env vars, cluster reachability.
2. **(optional) `terraform apply`** in `deploy/terraform` — creates DOKS + writes kubeconfig. Skip with `--skip-provision`.
3. **Namespaces + secrets** from env — `ghcr-pull` (image pull), `velero-cloud-credentials`, `external-dns-do`. Re-runs are no-ops.
4. **`helmfile apply`** — installs, in dependency order: cert-manager → Envoy Gateway → KEDA (+HTTP add-on) → metrics-server → Prometheus/Loki/Grafana → Velero (only if a bucket is set) → external-dns (only if enabled) → `vortex-bootstrap` (in-cluster Postgres, shared Gateway, GatewayClass, DNS-01 issuer, wildcard TLS) → the Vortex control plane.
5. **Waits** for the key rollouts.
6. **Prints** the shared Gateway LoadBalancer IP, the DNS records to create, and the dashboard URL.

**Expected duration:** ~10–20 min on a fresh cluster (LB provisioning + image
pulls + TLS issuance dominate).

`--skip-addons` installs only the bootstrap + control plane (use only on a
cluster that already has the add-ons).

---

## 6. DNS setup

After install, the script prints the **Gateway LoadBalancer external IP**. Create
DNS records pointing at it. You can also read it manually:
```bash
kubectl -n vortex get gateway vortex -o jsonpath='{.status.addresses[0].value}'
# or the Service:
kubectl -n envoy-gateway-system get svc -o wide
```

Records to create (replace `vortex.v60ai.com` with your `BASE_DOMAIN`, `‹LB-IP›`
with the printed address):

| Record | Type | Value | Purpose |
|--------|------|-------|---------|
| `api.vortex.v60ai.com` | A | `‹LB-IP›` | control-plane API |
| `vortex.v60ai.com` (and/or `app.…`) | A | `‹LB-IP›` | dashboard (use the URL the installer prints) |
| Tenant app hostnames `‹app›.‹project›.‹org›.vortex.v60ai.com` | — | — | **see note** |

> **Important — multi-level tenant hostnames.** Tenant URLs are 3 labels deep, which
> a single DNS wildcard cannot cover. **Enable `external-dns`
> (`VORTEX_EXTERNAL_DNS_ENABLED=true`)** so the platform **auto-publishes one record
> per app** pointing at the shared LB — this is the supported way to serve tenant
> apps. Without external-dns you must create the per-app/per-project records by
> hand. (cert-manager issues the matching wildcard TLS automatically either way.)

---

## 7. Post-install verification

```bash
# 1. All control-plane pods Running
kubectl -n vortex get pods
# 2. Add-ons healthy
kubectl get pods -A | grep -Ei 'cert-manager|envoy|keda|prometheus|loki|grafana|velero|external-dns'
# 3. Wildcard cert issued (READY=True)
kubectl -n vortex get certificate
# 4. API health
curl -fsS https://api.<BASE_DOMAIN>/healthz   # or the readiness path the installer prints
# 5. Dashboard loads
open https://<dashboard URL printed by installer>
```

### ✅ The real exit criterion — prove one app goes live
CI cannot prove this; **you must**. Sign up in the dashboard (the first admin
email auto-provisions an org + project), then deploy a tiny **prebuilt container
image** app and confirm:
1. The deploy reaches **Running** (watch the app's deploy progress / Logs tab).
2. Its **HTTPS URL** (`‹app›.‹project›.‹org›.‹BASE_DOMAIN›`) returns the app over
   valid TLS.

If that works end-to-end, the platform is genuinely live. (Git/buildpack source
builds are supported but image deploys are the simplest first proof.)

---

## 8. Day-2 operations

- **Upgrade / re-deploy:** bump `VORTEX_IMAGE_TAG` and re-run `deploy/install.sh
  --skip-provision --yes` (idempotent). CI/CD (`.github/workflows/deploy.yml`)
  does the same control-plane upgrade automatically on merge.
- **Backups (Velero):** set `VORTEX_VELERO_BUCKET/REGION/S3_URL` +
  `VORTEX_VELERO_CREDENTIALS_FILE` and re-run; tenant DB backups are also exposed
  per-database in the dashboard.
- **Observability:** Grafana/Prometheus/Loki run in-cluster. Port-forward Grafana
  (`kubectl -n monitoring port-forward svc/...-grafana 3000:80`) — dashboards +
  SLO alert rules ship with the install.
- **Scale to zero / wake:** apps marked HTTP-scalable scale down when idle and
  **wake on the next request** via the KEDA HTTP add-on (no action needed).
- **Secrets rotation:** update the env var and re-run the installer; secrets are
  re-applied in place.

## 9. Teardown / rollback
- **Rollback** a control-plane release: `helm -n vortex rollback vortex` (or
  re-install a previous `VORTEX_IMAGE_TAG`).
- **Full teardown:** `deploy/scripts/teardown.sh` (removes releases) and
  `terraform destroy` in `deploy/terraform` (removes the cluster). **Destroys
  tenant data** — back up first.

## 10. Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| Gateway LB IP stays `<pending>` | cloud LB still provisioning / quota | wait; check `kubectl -n envoy-gateway-system describe svc` |
| Wildcard `Certificate` not `Ready` | DNS-01 token wrong / propagation | check `kubectl -n vortex describe certificate` + cert-manager logs; verify `VORTEX_DO_TOKEN` has DNS write |
| Pods `ImagePullBackOff` | registry/pull secret | verify `VORTEX_IMAGE_REGISTRY/TAG` and `GHCR_PULL_TOKEN`; `kubectl -n vortex get secret ghcr-pull` |
| Tenant app 404 / no DNS | external-dns off or record missing | set `VORTEX_EXTERNAL_DNS_ENABLED=true` (§6) or create the record manually |
| external-dns not publishing | secret/zone mismatch | confirm `external-dns-do` secret exists in ns `external-dns` and `VORTEX_EXTERNAL_DNS_ZONES` matches your zone |
| API `CrashLoopBackOff` on boot | missing/weak security secret | ensure `VORTEX_JWT_SECRET` + `VORTEX_SECRET_ENCRYPTION_KEY` are set and non-default |
| `helmfile` release fails on `needs` | a prerequisite release errored | fix that release first; re-run (idempotent) |

## 11. Security notes
- Tenants run with a hardened, non-root, drop-ALL-caps securityContext under Pod
  Security Admission; namespaces are isolated per org-project with NetworkPolicies.
- Sessions are HttpOnly cookies; refresh-token rotation/revocation enabled; tenant
  DB credentials are encrypted at rest (AES-GCM).
- Use a **separate DNS-scoped token** for external-dns rather than reusing the
  cluster-admin DO token where possible (least privilege).

---

*Generated for the Vortex platform. For the full env/flag reference run
`deploy/install.sh --help`. Questions on internals: see `CLAUDE.md` and
`deploy/KUBERNETES.md`.*
