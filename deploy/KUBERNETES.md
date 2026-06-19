# Vortex on Kubernetes — workload runtime

Decisions (locked with the product owner):
- **Control plane on Docker.** `vortex-api` + `vortex-web` run via Docker / `docker-compose`
  (one droplet or a small VM) — simple to operate. See `docker/docker-compose.full.yml`.
- **All customer workloads on Kubernetes** (DOKS). No Coolify runtime. Apps, services,
  WordPress, and databases (Redis/Postgres/MySQL/Mongo) all run on **one cluster** for
  cost control + K8s scheduling/limits.
- **One generic Helm chart for everything:** `deploy/charts/common-chart` (vendored from the
  team's `cross-helm-charts-template`, `common-chart` v0.6.0). Vortex renders per-workload
  values and does `helm upgrade --install` into the tenant's namespace.
- **KEDA** for autoscaling (event-driven; scale-to-zero capable) — see `keda` block in the
  chart's `values.yaml` and `templates/keda-scaledobject.yaml`.

## Tenancy & isolation
- **Namespace per org-project** (`vortex-<org>-<project>`), with a `ResourceQuota` + `LimitRange`
  derived from the org's (DB-driven) plan, plus `NetworkPolicy` for isolation. Created dynamically by
  the control-plane API in `kube.EnsureTenant` (not declared in the helmfile) and labelled with the
  Pod Security Admission `restricted` profile.
- A workload (app/service/db) = one Helm release of `common-chart` in that namespace.

## The cost lever — resource overcommit (CRITICAL)
For a workload the user requests at, say, **4 GB / 1 CPU** (the advertised/billed size),
Vortex sets the chart's `deployment.resources`:

```
requests = requested × overcommitFactor   (default 0.20)   → 0.8 GB / 0.2 CPU  (reserved, scheduled on)
limits   = requested                        (1.00)          → 4 GB   / 1 CPU    (burst ceiling)
```

So the scheduler bin-packs on the small `requests` (~5× density → ~5× lower node cost),
while tenants burst to their full size when the node has room (Burstable QoS).

- **CPU** is compressible → overcommit aggressively (factor ~0.15–0.20); contention only throttles.
- **Memory** is NOT compressible → use a safer factor (~0.30–0.40) + node autoscaling on memory
  pressure + pod priorities, or you risk OOM kills when many pods burst at once.
- Overcommit factors (separate CPU/mem, global default + per-plan override) are **admin/DB
  configurable — never hardcoded**.

## Catalog template → chart values mapping
The DB-backed catalog (WordPress, Ghost, Redis, Postgres, …) maps each template to chart values:

| Template kind | Chart shape | Notes |
|---|---|---|
| `app` (Git/image) | `deployment` (stateless) + `service` + `ingress` | image from build/registry; HTTPRoute/Ingress for domains |
| `service` (WordPress, Ghost, n8n) | `deployment` + `service` + `ingress` (+ `persistence` if needed) | image/ports from the template |
| `database` (Postgres, MySQL, Mongo, Redis) | `deployment.stateful: true` (StatefulSet) + `service-headless` + `persistence`/`volumeClaimTemplates` | PVC sizing from plan |

Per-workload values Vortex computes: image repo/tag, ports, `resources` (overcommit math above),
`keda` (triggers + min/max, scale-to-zero for idle web apps via the KEDA HTTP add-on),
`ingress`/`gateway` host (custom domains), `persistence` (DBs), env via `secret`/`config`.

## How Vortex provisions (the Go `KubernetesBackend` — next to implement)
The platform layer talks to a `DeployBackend` interface; the Kubernetes implementation:
1. Ensures the org namespace + `ResourceQuota`/`LimitRange` (from the plan).
2. Renders `common-chart` values for the workload (mapping above + overcommit).
3. `helm upgrade --install <release> deploy/charts/common-chart -n <ns> -f <values>`
   (via the Helm Go SDK or shelling out to `helm`).
4. Reads status/logs/metrics back via the K8s API (client-go) for the UI tabs.

## Edge routing — Gateway API, a shared Gateway pool on ONE LoadBalancer (cost)

> ⚠️ `ingress-nginx` (kubernetes/ingress-nginx) is **retired**
> (<https://kubernetes.io/blog/2025/11/11/ingress-nginx-retirement/>) — Vortex does **not**
> use it. Routing is the **Gateway API**.

- **A shared `Gateway` pool** (primary `vortex` Gateway in `charts/vortex-bootstrap`, backed by
  **Envoy Gateway**) fronts the whole platform. By default the pool is merged onto **one cloud
  LoadBalancer** (`gateway.merge: true` → `mergeGateways`). The primary Gateway's HTTPS/wildcard
  listeners allow routes from all namespaces; the pool only grows past one Gateway when verified
  custom-domain listeners exceed the per-Gateway ceiling (see sharding below).
- **Each app = one `HTTPRoute`** in its org-project namespace, attached to the shared Gateway
  via `parentRefs`, with `hostnames: [<app>.<project>.<org>.vortex.v60ai.com]`. Adding apps
  never adds LoadBalancers — cost stays flat.
- **Namespace per org-project** (`vortex-<org>-<project>`): admins can list orgs/projects as
  namespaces; tenants have no K8s access.
- **TLS** via cert-manager **DNS-01** (DigitalOcean) wildcard certs — per-org
  `*.<org>.vortex.v60ai.com`, managed by the control plane as orgs are created.
- **DNS** via **external-dns** (optional): when `VORTEX_EXTERNAL_DNS_ENABLED=true` the control plane
  publishes the wildcard/custom-domain records into the managed zones (`VORTEX_EXTERNAL_DNS_ZONES`,
  comma-separated) with TTL `VORTEX_DNS_RECORD_TTL` (default 300s), so you don't hand-create records
  per org/domain. When disabled, set the wildcard records by hand (the installer prints them).

### Custom-domain listener sharding (scaling past the per-Gateway listener budget)

Tenant `*.vortex.v60ai.com` hosts ride the wildcard listener and need **no** per-tenant
listener. A **verified custom domain** (e.g. `shop.acme.io`) is different: it needs its own
HTTPS listener for SNI/TLS termination. A single Gateway has a hard ceiling of **64 listeners**.

Past that ceiling the control plane (`apps/api/internal/kube`) **auto-shards**: custom-domain
listeners are allocated across a **pool** of Gateways. The primary `vortex` Gateway is shard 0
(wildcard + http + the first batch of custom listeners); when it fills, listeners overflow to
`vortex-shard-1`, `vortex-shard-2`, … which the backend **creates on demand** (and garbage-
collects when emptied). A custom domain's `HTTPRoute` gets the holding shard added to its
`parentRefs`, so traffic routes regardless of which shard the listener landed on. **No manual
"move the tenant by hand" step** — the old hard error at 64 is gone.

**LoadBalancer cost tradeoff** (the single-LB philosophy is preserved by default):

| Mode | `gateway.merge` | LBs | Tradeoff |
|------|-----------------|-----|----------|
| Merged (default) | `true` | **1** total | All shards share one Envoy fleet/LB via an `EnvoyProxy` with `mergeGateways: true`. Cheapest; one data plane = one blast radius. |
| Unmerged | `false` | 1 per shard | Each overflow shard gets its own LB (a handful only at very large custom-domain counts). Honest extra cost; better isolation per shard. |

Shards can be **pre-provisioned** statically (`gateway.shards.count > 0` in
`charts/vortex-bootstrap`) or left to dynamic creation (default `0`). Per-shard listener budget is
tunable via the control-plane `GatewayShardMaxListeners` config
(env `VORTEX_GATEWAY_SHARD_MAX_LISTENERS`, default 64 — the hard ceiling — with the primary
reserving 2 slots for its wildcard/http listeners). Merge is driven by `GatewayShardLBSharing`
(env `VORTEX_GATEWAY_SHARD_LB_SHARING`, mirrored by the bootstrap chart's `gateway.merge`). Both
values are threaded onto `kube.Config` in `httpx/server.go` — the `kube` package reads them off
Config, never from the environment (invariant #7).

> Per-Gateway-listener **TLS isolation** still applies: each custom domain terminates with its
> own cert Secret, so sharding changes only WHICH Gateway object holds the listener, not the
> per-domain certificate model.

Cluster prerequisites (install once, all current/non-deprecated): **Gateway API CRDs + Envoy
Gateway** (the Envoy Gateway chart bundles the CRDs), cert-manager, **KEDA** + the **KEDA HTTP
add-on** (HTTP scale-to-zero/wake), metrics-server, the observability stack (Prometheus + Grafana +
Loki/promtail), and Velero (only when a backup bucket is set). All of these are installed in
dependency order by `deploy/helmfile.yaml` — there is no manual `kubectl` step
(invariant #5). `deploy/install.sh` (below) is the single entrypoint that drives the helmfile.

## One-shot install — `deploy/install.sh`

`deploy/install.sh` is the **single, idempotent, env-driven entrypoint** for standing up Vortex on a
cluster. It runs, in order:

1. **Prereq checks** — `helmfile`, `helm`, `kubectl` (and `terraform`/`doctl` if provisioning), and a
   reachable cluster.
2. **Optional terraform provision** — stands up the DOKS cluster from `deploy/terraform` if asked.
3. **Namespaces + secrets from env** — creates the `vortex` namespace and the secrets the charts
   expect (JWT, secret-encryption key, DB URL, DNS/Stripe/Velero creds) — never committed.
4. **`helmfile -f deploy/helmfile.yaml apply`** — all addons + `vortex-bootstrap` (Postgres + shared
   Gateway + GatewayClass + DNS-01 issuer + wildcard TLS) + the control plane (api + web).
5. **Wait for rollouts**, then **print** the Gateway LoadBalancer IP, the wildcard DNS records to
   create (when external-dns is off), and the dashboard URL.

Required env vars (sourced via `requiredEnv` in the helmfile — a missing value fails loudly):

| Env var | Purpose |
|---|---|
| `VORTEX_JWT_SECRET` | JWT signing secret (`openssl rand -hex 32`) |
| `VORTEX_SECRET_ENCRYPTION_KEY` | app-secret encryption key (`openssl rand -hex 32`) |
| `VORTEX_PG_PASSWORD` | in-cluster Postgres password (`openssl rand -hex 24`) |
| `VORTEX_DATABASE_URL` | `postgres://vortex:<pw>@vortex-postgres.vortex.svc.cluster.local:5432/vortex?sslmode=require` |
| `VORTEX_DO_TOKEN` | DigitalOcean token for cert-manager DNS-01 wildcard issuance |

Optional (defaults shown where applicable):

| Env var | Purpose |
|---|---|
| `VORTEX_BASE_DOMAIN` | base domain (default `vortex.v60ai.com`) |
| `VORTEX_IMAGE_REGISTRY` / `VORTEX_IMAGE_TAG` | control-plane image source (default DOCR `registry.digitalocean.com/vortex` / `latest`) |
| `VORTEX_ADMIN_EMAILS` | comma list of admin-panel emails |
| `VORTEX_STRIPE_SECRET_KEY` / `VORTEX_STRIPE_WEBHOOK_SECRET` | enable Stripe billing (else Mock provider) |
| `VORTEX_EXTERNAL_DNS_ENABLED` / `VORTEX_EXTERNAL_DNS_ZONES` / `VORTEX_DNS_RECORD_TTL` | external-dns record publishing (TTL default 300) |
| `VORTEX_GATEWAY_SHARD_MAX_LISTENERS` / `VORTEX_GATEWAY_SHARD_LB_SHARING` | Gateway listener budget / merge-onto-one-LB |
| `VORTEX_BUILDPACKS_BUILDER` / `VORTEX_BUILD_BUILDPACKS_IMAGE` | CNB builder + lifecycle images for no-Dockerfile builds |
| `VORTEX_VELERO_BUCKET` / `VORTEX_VELERO_REGION` / `VORTEX_VELERO_S3_URL` | enable Velero backups (Velero installs only when the bucket is set) |

The control plane reads these off its `config` / `kube.Config` / builder-config structs (invariant
#7); they are threaded in `httpx/server.go` (`newKubeBackend`, `newBuilder`) — packages downstream do
not re-read the environment.

> **Status:** the Go `KubeBackend`, the namespace/quota controller, Gateway sharding, KEDA HTTP wake,
> external-dns, observability, backups, buildpacks builds and the installer are **in-tree and tested**
> against `kube.FakeBackend`. The one remaining manual exit criterion is a **live-cluster
> end-to-end proof** — a real `deploy/install.sh` run with DNS + wildcard TLS resolving and a tenant
> app reachable at its `<app>.<project>.<org>.<baseDomain>` host (see PROGRESS.md).
